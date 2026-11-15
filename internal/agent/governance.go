package agent

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/charmbracelet/crush/internal/governor"
	"github.com/charmbracelet/crush/internal/nudge"
	"github.com/charmbracelet/crush/internal/session"
)

// GovernanceOptions carries the governance settings an agent uses. Nil disables
// the whole layer, which is what the tests that predate it rely on.
type GovernanceOptions struct {
	Settings *config.GovernanceOptions
}

// governanceState is the per-agent runtime for turn governance: one exploration
// governor and one nudge engine per turn, plus the reasoning score.
type governanceState struct {
	settings *config.GovernanceOptions
	// gateMu guards gateBlocks: refusals are counted per session so a model
	// that cannot satisfy the gate cannot loop on it.
	gateMu     sync.Mutex
	gateBlocks map[string]int
}

// newGovernanceState builds the runtime, or nil when governance is disabled.
func newGovernanceState(opts *GovernanceOptions) *governanceState {
	if opts == nil || opts.Settings == nil {
		return nil
	}
	if !opts.Settings.GovernanceEnabled() {
		return nil
	}
	return &governanceState{settings: opts.Settings, gateBlocks: make(map[string]int)}
}

// enabled reports whether the layer should run.
func (g *governanceState) enabled() bool { return g != nil && g.settings != nil }

// governorConfig builds a governor config from the user's settings.
func (g *governanceState) governorConfig() governor.Config {
	cfg := governor.DefaultConfig()
	if !g.enabled() {
		cfg.Disabled = true
		return cfg
	}
	s := g.settings
	cfg.SoftThreshold = s.GetSoftThreshold()
	cfg.ModerateThreshold = s.GetModerateThreshold()
	cfg.StrongThreshold = s.GetStrongThreshold()
	cfg.HardThreshold = s.GetHardThreshold()
	cfg.RepeatedSearchThreshold = s.GetRepeatedSearchThreshold()
	cfg.RepeatedReadThreshold = s.GetRepeatedReadThreshold()
	cfg.NoProgressThreshold = s.GetNoProgressThreshold()
	cfg.TimeWarning = time.Duration(s.GetExplorationTimeWarningSec()) * time.Second
	cfg.TimeCritical = time.Duration(s.GetExplorationTimeCriticalSec()) * time.Second
	cfg.HistorySize = s.GetHistorySize()
	return cfg
}

// nudgeThresholds builds the nudge thresholds from the governor settings, so a
// user who raises the repeated-search limit does not get scolded by the nudge
// engine at the old one.
func (g *governanceState) nudgeThresholds() nudge.Config {
	cfg := nudge.DefaultConfig()
	if !g.enabled() {
		cfg.Disabled = true
		return cfg
	}
	s := g.settings
	cfg.RepeatCallAfter = max(s.GetRepeatedSearchThreshold()-1, 1)
	cfg.ReadStormRounds = max(s.GetNoProgressThreshold()-1, 2)
	return cfg
}

// gateBlocksUsed returns and increments how many times this session has had a
// finish refused. The budget lives on the session, not the run, because a
// refused finish re-enters Run with a queued follow-up and a per-run counter
// would let the gate loop forever.
func (g *governanceState) gateBlocksUsed(sessionID string, limit int) int {
	g.gateMu.Lock()
	defer g.gateMu.Unlock()
	used := g.gateBlocks[sessionID]
	if used >= limit {
		return used
	}
	g.gateBlocks[sessionID] = used + 1
	return used
}

// clearGateBlocks forgets one session's refusal budget, so the next turn starts
// clean.
func (g *governanceState) clearGateBlocks(sessionID string) {
	if g == nil {
		return
	}
	g.gateMu.Lock()
	defer g.gateMu.Unlock()
	delete(g.gateBlocks, sessionID)
}

// completionGateEnabled reports whether unsupported completions are refused.
func (g *governanceState) completionGateEnabled() bool {
	return g.enabled() && g.settings.GetCompletionGate()
}

// reasoningMetricsEnabled reports whether per-turn reasoning metrics are kept.
func (g *governanceState) reasoningMetricsEnabled() bool {
	return g.enabled() && g.settings.GetReasoningMetrics()
}

// maxRounds returns the per-turn round cap, or 0 for none.
func (g *governanceState) maxRounds() int {
	if !g.enabled() {
		return 0
	}
	return g.settings.GetMaxRounds()
}

// turnGovernor tracks one turn: the pressure model, the nudge engine, the
// counters the nudges read, and the reasoning score.
//
// It is created per run and discarded with it, so nothing leaks between turns.
// Every method is safe for concurrent use because parallel tool calls report
// into it from separate goroutines.
type turnGovernor struct {
	mu       sync.Mutex
	state    *governanceState
	gov      *governor.Governor
	nudges   *nudge.Engine
	counters nudge.Counters
	score    nudge.Score
	// pending holds notes produced since the last prompt build. They are
	// injected into the next round's system prompt, which is the only place
	// the model reliably reads.
	pending []string
	// lastCallFingerprints remembers the previous round's tool calls, so a
	// repeat can be detected across rounds.
	lastCallFingerprints []string
	// reasoningChars tracks the current round's thinking length.
	reasoningChars int
	// proseChars tracks the current round's answer length.
	proseChars int
	// callsThisRound counts tool calls in the current round.
	callsThisRound int
	// started is when the turn began, for elapsed-time pressure.
	started time.Time
}

// newTurnGovernor creates the turn's tracker.
func newTurnGovernor(state *governanceState) *turnGovernor {
	if !state.enabled() {
		return nil
	}
	return &turnGovernor{
		state:   state,
		gov:     governor.New(state.governorConfig()),
		nudges:  nudge.New(state.nudgeThresholds()),
		started: time.Now(),
	}
}

// beginRound resets the per-round counters and increments the round number.
func (t *turnGovernor) beginRound() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counters.Round++
	t.counters.TotalRounds = t.counters.Round
	t.reasoningChars = 0
	t.proseChars = 0
	t.callsThisRound = 0
	t.counters.MalformedCalls = 0
	t.counters.ToolCalls = 0
	t.counters.ReasoningChars = 0
	t.counters.ProseChars = 0
}

// observeReasoning records thinking text for the round.
func (t *turnGovernor) observeReasoning(text string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reasoningChars += len(text)
	t.counters.ReasoningChars = t.reasoningChars
}

// observeProse records answer text for the round.
func (t *turnGovernor) observeProse(text string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.proseChars += len(text)
	t.counters.ProseChars = t.proseChars
}

// observeToolCall records a tool call about to run and reports whether it is a
// redundant repeat of one already answered this turn. The caller refuses the
// repeat when it returns true.
func (t *turnGovernor) observeToolCall(toolName, input string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.callsThisRound++
	t.counters.ToolCalls = t.callsThisRound

	fingerprint := toolName + "\x00" + input
	repeat := 0
	for _, previous := range t.lastCallFingerprints {
		if previous == fingerprint {
			repeat++
		}
	}
	t.lastCallFingerprints = append(t.lastCallFingerprints, fingerprint)
	if len(t.lastCallFingerprints) > t.state.settings.GetHistorySize() {
		t.lastCallFingerprints = t.lastCallFingerprints[len(t.lastCallFingerprints)-t.state.settings.GetHistorySize():]
	}
	t.counters.RepeatedCallCount = repeat

	report := t.gov.Observe(governor.Action{
		Kind:        governor.ActionSearch,
		Fingerprint: fingerprint,
		At:          time.Now(),
	})
	// Only the hard level refuses anything, and only an already-answered
	// repeat. Everything below it steers. A repeat seen often enough to be
	// the dominant pattern is refused even before the pressure ladder
	// reaches hard, because the answer it would return is already present.
	threshold := t.state.settings.GetRepeatedSearchThreshold()
	if repeat >= threshold {
		return true
	}
	return report.Level == governor.LevelHard && repeat > 0
}

// observeToolResult records a completed tool call.
func (t *turnGovernor) observeToolResult(toolName string, isError bool, output string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	kind := actionKindForTool(toolName)
	t.gov.Observe(governor.Action{
		Kind:        kind,
		Fingerprint: toolName,
		Empty:       isError || strings.TrimSpace(output) == "",
		At:          time.Now(),
	})
	t.counters.Edits = t.gov.Report().Edits
	t.counters.Runs = t.gov.Report().Runs
}

// actionKindForTool maps a Crush tool name to its governor action kind. The
// mapping is by tool, not by result, so a failing build still counts as
// evidence gathered rather than as exploration.
func actionKindForTool(name string) governor.ActionKind {
	switch name {
	case "ls", "glob", "grep", "view", "lsp_symbols", "lsp_definition",
		"lsp_references", "lsp_call_hierarchy", "lsp_diagnostics", "crush_info",
		"crush_logs", "list_mcp_resources", "read_mcp_resource", "agent_status":
		return governor.ActionRead
	case "web_search", "web_fetch", "sourcegraph", "fetch", "agentic_fetch", "download":
		return governor.ActionSearch
	case "edit", "multiedit", "write", "lsp_replace_symbol", "lsp_rename":
		return governor.ActionEdit
	case "bash", "diagnostics", "lsp_restart", "job_output", "job_kill":
		return governor.ActionRun
	default:
		return governor.ActionOther
	}
}

// endRound evaluates the nudges for the round and queues whatever they say.
// It returns the notes queued so the caller can log or assert them.
func (t *turnGovernor) endRound(out roundOutcome) []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.counters.ConsecutiveReadOnly = out.consecutiveReadOnly
	t.counters.ConsecutiveSingleCall = out.consecutiveSingleCall
	t.counters.ConsecutiveNoProgress = out.consecutiveNoProgress
	t.counters.BackgroundPolls = out.backgroundPolls
	t.counters.RepeatedProseCount = out.repeatedProse
	t.counters.TodosOpen = out.todosOpen
	t.counters.TodosTotal = out.todosTotal
	t.counters.Planning = out.planning
	t.counters.RecapBlock = out.recapOnly
	if out.edits > 0 {
		t.counters.Edits = out.edits
	}

	notes := t.nudges.Evaluate(t.counters)
	var texts []string
	for _, note := range notes {
		texts = append(texts, note.Text)
		slog.Debug("Governance nudge", "reason", note.Reason, "severity", note.Severity, "round", t.counters.Round)
	}
	// The governor's own steering note rides along, at most one per level.
	if level, text := t.gov.TakeNote(); text != "" {
		texts = append(texts, text)
		slog.Debug("Governor steering note", "level", level, "pressure", t.gov.Report().Pressure, "round", t.counters.Round)
	}
	t.pending = append(t.pending, texts...)
	return texts
}

// takeNotes returns and clears the notes queued for the next prompt.
func (t *turnGovernor) takeNotes() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	notes := t.pending
	t.pending = nil
	return notes
}

// recordReasoning folds the finished round into the reasoning score.
func (t *turnGovernor) recordReasoning(tailChars int, madeProgress bool, restarts int) {
	if t == nil || !t.state.reasoningMetricsEnabled() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.score = nudge.SummarizeReasoning(t.score, t.reasoningChars, tailChars, t.proseChars, madeProgress, restarts)
}

// runCount returns how many commands the turn executed, which is the evidence a
// verification claim needs.
func (t *turnGovernor) runCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counters.Runs
}

// scoreSnapshot returns the accumulated reasoning score.
func (t *turnGovernor) scoreSnapshot() nudge.Score {
	if t == nil {
		return nudge.Score{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.score
}

// roundOutcome is what the caller observed about a finished round. The caller
// owns the "what happened" facts because only it sees the calls, the results
// and the reply together.
type roundOutcome struct {
	consecutiveReadOnly   int
	consecutiveSingleCall int
	consecutiveNoProgress int
	backgroundPolls       int
	repeatedProse         int
	todosOpen             int
	todosTotal            int
	planning              bool
	recapOnly             bool
	edits                 int
}

// roundTracker counts the round-level facts the nudges need. It is updated by
// the run loop as each round finishes.
type roundTracker struct {
	readOnly        int
	singleCall      int
	noProgress      int
	backgroundPolls int
	edits           int
	totalRounds     int
}

// observe folds one round into the tracker.
func (r *roundTracker) observe(toolCalls int, mutated bool, onlyBackgroundChecks bool) {
	r.totalRounds++
	if mutated {
		r.readOnly = 0
		r.noProgress = 0
	} else {
		r.readOnly++
		r.noProgress++
	}
	if toolCalls == 1 {
		r.singleCall++
	} else {
		r.singleCall = 0
	}
	if onlyBackgroundChecks {
		r.backgroundPolls++
	} else {
		r.backgroundPolls = 0
	}
}

// outcome builds the roundOutcome for the governor.
func (r *roundTracker) outcome(todos []todoState, planning bool, recapOnly bool, repeatedProse int) roundOutcome {
	open, total := todoCounts(todos)
	return roundOutcome{
		consecutiveReadOnly:   r.readOnly,
		consecutiveSingleCall: r.singleCall,
		consecutiveNoProgress: r.noProgress,
		backgroundPolls:       r.backgroundPolls,
		repeatedProse:         repeatedProse,
		todosOpen:             open,
		todosTotal:            total,
		planning:              planning,
		recapOnly:             recapOnly,
		edits:                 r.edits,
	}
}

// todoState is the subset of a session todo the gate reads.
type todoState struct {
	Content string
	Status  string
}

// todoCounts returns how many todo items are open and how many exist.
func todoCounts(todos []todoState) (open, total int) {
	for _, todo := range todos {
		total++
		switch todo.Status {
		case "completed", "cancelled":
		default:
			open++
		}
	}
	return open, total
}

// governanceNotesText renders queued notes as a block the model reads. Notes are
// wrapped in a single element so the prompt has one extra node, not one per
// note, which keeps the cached prefix stable for longer.
func governanceNotesText(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<governance_notes>\n")
	for _, note := range notes {
		sb.WriteString(note)
		sb.WriteString("\n")
	}
	sb.WriteString("</governance_notes>\n\n")
	return sb.String()
}

// governorFor returns the turn governor, or nil when the layer is off. It is a
// method on sessionAgent so the run loop reads as one call.
func (a *sessionAgent) governorFor() *turnGovernor {
	return newTurnGovernor(a.governance)
}

// governanceConfigFor builds the governance options from the loaded config.
func (c *coordinator) governanceConfigFor() *GovernanceOptions {
	cfg := c.cfg.Config()
	settings := cfg.Options.GetGovernance()
	if !settings.GovernanceEnabled() {
		return nil
	}
	return &GovernanceOptions{Settings: settings}
}

// newFilesTouched reports whether a step's content contains a mutation. It is
// what separates "the turn did something" from "the turn looked at something",
// which is the distinction the whole pressure model rests on.
func newFilesTouched(content fantasy.ResponseContent) bool {
	for _, call := range content.ToolCalls() {
		switch actionKindForTool(call.ToolName) {
		case governor.ActionEdit:
			return true
		}
	}
	// A shell command that writes is a mutation too. The tool name cannot
	// tell, so the arguments are inspected for the flags that write.
	for _, call := range content.ToolCalls() {
		if call.ToolName == "bash" && shellArgumentsWrite(call.Input) {
			return true
		}
	}
	return false
}

// shellArgumentsWrite reports whether a shell invocation is likely to modify the
// workspace. It is deliberately conservative: a false negative just means the
// turn is scored as exploration, a false positive would suppress steering.
func shellArgumentsWrite(input string) bool {
	lower := strings.ToLower(input)
	writers := []string{
		"sed -i", "perl -pi", "tee ", " > ", ">>", "rm ", "mv ", "cp ",
		"mkdir ", "touch ", "git add", "git commit", "git checkout", "git restore",
		"go fmt", "gofmt", "npm install", "go mod tidy", "chmod ",
	}
	for _, writer := range writers {
		if strings.Contains(lower, writer) {
			return true
		}
	}
	return false
}

// onlyJobChecks reports whether every call in a step was a background-job
// inspection. Rounds of nothing else are the polling failure mode.
func onlyJobChecks(content fantasy.ResponseContent) bool {
	calls := content.ToolCalls()
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		switch call.ToolName {
		case "job_output", "job_kill", "agent_status":
		default:
			return false
		}
	}
	return true
}

// postDecisionTail measures how much reasoning came after the last tool call or
// the last sentence, which is the part of thinking that never pays for itself.
// Returning the whole reasoning length is the honest fallback: there is no
// decision boundary, so all of it is tail.
func postDecisionTail(content fantasy.ResponseContent) int {
	reasoning := ""
	for _, part := range content {
		if r, ok := part.(fantasy.ReasoningContent); ok {
			reasoning += r.Text
		}
	}
	if reasoning == "" {
		return 0
	}
	// A decision is announced by a tool call. When the round made one, the
	// text before it is planning and the text after it is tail; without a
	// boundary, the last paragraph is the tail.
	if len(content.ToolCalls()) > 0 {
		return len(reasoning) / 3
	}
	if idx := strings.LastIndex(reasoning, "\n\n"); idx > 0 {
		return len(reasoning) - idx
	}
	return len(reasoning)
}

// reasoningRestarts counts the self-corrections in the reasoning text: the
// "wait, no" pattern that marks a model re-deciding.
func reasoningRestarts(content fantasy.ResponseContent) int {
	markers := []string{"wait,", "wait —", "wait -", "no, that", "actually,", "on second thought", "let me reconsider", "hmm,", "i was wrong"}
	restarts := 0
	for _, part := range content {
		r, ok := part.(fantasy.ReasoningContent)
		if !ok {
			continue
		}
		lower := strings.ToLower(r.Text)
		for _, marker := range markers {
			restarts += strings.Count(lower, marker)
		}
	}
	return restarts
}

// sessionTodos converts session todos into the subset the gate reads.
func sessionTodos(todos []session.Todo) []todoState {
	out := make([]todoState, 0, len(todos))
	for _, todo := range todos {
		out = append(out, todoState{Content: todo.Content, Status: string(todo.Status)})
	}
	return out
}

// repeatedProseCount counts sentences a reply repeats verbatim, which is the
// stutter the nudge engine reacts to. It uses the same lossless test as the
// context-reduction prose cleaner so the two never disagree.
func repeatedProseCount(text string) int {
	return contextreduction.CollapseRepeatedProse(text).RemovedSentences
}

// RevertTurn undoes the edits of the last turn in this session, newest change
// first, and reports what happened.
//
// It refuses to overwrite a file that changed after the edit: discarding a
// change the agent did not make is a bigger loss than an incomplete revert, and
// the skipped file is named so the caller can decide.
func (a *sessionAgent) RevertTurn(sessionID string) (tools.RevertReport, error) {
	a.editMu.Lock()
	journal := a.lastJournal
	a.editMu.Unlock()
	if journal == nil {
		return tools.RevertReport{}, ErrNothingToRevert
	}
	result, err := journal.Revert()
	if err != nil {
		return tools.RevertReport{}, err
	}
	return tools.RevertReport{
		Restored:  result.Restored,
		Removed:   result.Removed,
		Recreated: result.Recreated,
		Skipped:   result.Skipped,
	}, nil
}

// ErrNothingToRevert is returned when a session has no recorded edits.
var ErrNothingToRevert = errors.New("no edits to revert in this session")
