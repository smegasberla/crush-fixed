package agent

import (
	"log/slog"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/charmbracelet/crush/internal/editsafe"
	"github.com/charmbracelet/crush/internal/message"
)

// ReductionOptions carries the context-reduction settings an agent uses. It is
// a tiny adapter over the config so the agent package does not depend on the
// whole config tree in tests, and so a nil value cleanly disables the stack.
type ReductionOptions struct {
	// Settings are the user's context-reduction options. Nil means the
	// documented defaults, which is different from "disabled": disabling is
	// Options.Enabled == false.
	Settings *config.ContextReductionOptions
	// DigestDir is where continuation-brief digests are memoized. Empty
	// disables persistence while keeping the in-memory memo.
	DigestDir string
	// Provider and Model name the model the ceiling is learned for.
	Provider string
	Model    string
}

// reductionState is the per-agent runtime for the context-reduction stack.
type reductionState struct {
	settings *config.ContextReductionOptions
	ceiling  *contextreduction.Ceiling
	store    *contextreduction.DigestStore
	// memo memoizes the most recent brief in memory so a rebuild within the
	// same process costs nothing even when persistence is off.
	memo map[string]contextreduction.DigestMemo
	// modelKey identifies the model the ceiling belongs to.
	modelKey string
}

// newReductionState builds the runtime, or nil when the stack is disabled.
func newReductionState(opts *ReductionOptions) *reductionState {
	if opts == nil || opts.Settings == nil {
		return nil
	}
	if !opts.Settings.ContextReductionEnabled() {
		return nil
	}
	return &reductionState{
		settings: opts.Settings,
		ceiling:  contextreduction.NewCeiling(),
		store:    contextreduction.NewDigestStore(opts.DigestDir),
		memo:     make(map[string]contextreduction.DigestMemo),
		modelKey: contextreduction.ModelKey(opts.Provider, opts.Model),
	}
}

// enabled reports whether the stack should run. A nil state is disabled, which
// keeps every pre-existing test path unchanged.
func (r *reductionState) enabled() bool {
	return r != nil && r.settings != nil
}

// ceilingFor returns the prompt size that may be sent, honouring a learned
// ceiling from real provider overflows. When the stack is disabled it returns
// the advertised window unchanged.
func (r *reductionState) ceilingFor(advertised int) int {
	if !r.enabled() {
		return advertised
	}
	if !r.settings.GetLearnedCeiling() {
		if advertised <= 0 {
			return advertised
		}
		return advertised * r.settings.GetCeilingSafetyPercent() / 100
	}
	return r.ceiling.Effective(r.modelKey, advertised, r.settings.GetCeilingSafetyPercent())
}

// digestOptions builds the tool-result digesting options from the settings.
func (r *reductionState) digestOptions() contextreduction.DigestOptions {
	if !r.enabled() {
		return contextreduction.DigestOptions{}
	}
	base := r.settings.GetToolResultDigestTokens()
	return contextreduction.DigestOptions{
		MinTokens:     base,
		ProtectErrors: r.settings.GetProtectErrors(),
		ToolCaps: map[string]int{
			// Cheap, structurally re-derivable listings go early.
			"ls":           base / 4,
			"glob":         base / 4,
			"grep":         base / 2,
			"crush_info":   base / 4,
			"crush_logs":   base / 4,
			"job_output":   base / 2,
			"agent_status": base / 4,
			// Build/test output is the evidence a fix rests on, so it keeps
			// its head and tail longer.
			"bash":            base * 2,
			"diagnostics":     base * 2,
			"lsp_diagnostics": base * 2,
		},
	}
}

// pointerOptions builds the superseded-read options from the settings.
func (r *reductionState) pointerOptions() contextreduction.PointerOptions {
	if !r.enabled() || !r.settings.GetReadPointerStubs() {
		return contextreduction.PointerOptions{MinTokens: 1 << 30}
	}
	return contextreduction.PointerOptions{MinTokens: r.settings.GetReadPointerStubTokens()}
}

// compactOptions builds the compaction planner options from the settings.
func (r *reductionState) compactOptions() contextreduction.CompactOptions {
	return r.compactOptionsWithOverhead(0)
}

// compactOptionsWithOverhead adds the part of a request that is not
// conversation, so the budget describes the whole prompt the provider sees
// rather than only its messages.
func (r *reductionState) compactOptionsWithOverhead(overhead int) contextreduction.CompactOptions {
	if !r.enabled() {
		return contextreduction.CompactOptions{}
	}
	return contextreduction.CompactOptions{
		TriggerTokens:  r.settings.GetCompactionTriggerTokens(),
		BudgetTokens:   r.settings.GetCompactionBudgetTokens(),
		ReserveTokens:  r.settings.GetResponseReserveTokens(),
		OverheadTokens: overhead,
	}
}

// learnOverflow records a real provider overflow so the next prompt is smaller.
func (r *reductionState) learnOverflow(promptTokens int, err error) {
	if !r.enabled() || !r.settings.GetLearnedCeiling() {
		return
	}
	learned := r.ceiling.ObserveOverflow(r.modelKey, promptTokens, err)
	slog.Info("Learned a lower context ceiling from a provider overflow",
		"model", r.modelKey, "prompt_tokens", promptTokens, "ceiling", learned)
}

// summaryFor returns a memoized continuation brief for the chain, or false.
func (r *reductionState) summaryFor(chain string) (string, bool) {
	if !r.enabled() || chain == "" {
		return "", false
	}
	if memo, ok := r.memo[chain]; ok && memo.Brief != "" {
		return memo.Brief, true
	}
	if memo, ok := r.store.Load(chain); ok {
		r.memo[chain] = memo
		return memo.Brief, true
	}
	return "", false
}

// rememberSummary stores a continuation brief under its chain hash.
func (r *reductionState) rememberSummary(chain string, dropped int, brief string) {
	if !r.enabled() || chain == "" || brief == "" {
		return
	}
	memo := contextreduction.DigestMemo{Chain: chain, Absorbed: dropped, Brief: brief}
	r.memo[chain] = memo
	if err := r.store.Store(memo); err != nil {
		slog.Warn("Failed to persist a continuation brief", "error", err)
	}
}

// digestDirFor resolves the digest directory for a data directory and the
// user's override.
func digestDirFor(settings *config.ContextReductionOptions, dataDir string) string {
	if settings != nil {
		if dir := settings.GetDigestCacheDir(); dir != "" {
			return dir
		}
	}
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "digests")
}

// reductionRepack is the last step before a request goes out: it shrinks what
// the prompt carries so a long session stays answerable. It is deliberately
// ordered cheapest-first, because each stage is a no-op when the prompt already
// fits.
//
// The order matters. Superseded reads go first: they are pure duplication, and
// removing them can bring the prompt under the ceiling without touching
// anything else. Digested tool results come next, since a stub keeps the head
// and tail the model may still need. Only if the prompt is still too large are
// whole messages dropped, and only from the front.
func (r *reductionState) repack(system string, messages []fantasy.Message, advertisedWindow int) ([]fantasy.Message, reductionOutcome) {
	outcome := reductionOutcome{Messages: len(messages)}
	if !r.enabled() {
		return messages, outcome
	}
	// Without an advertised window the stack still bounds itself by the
	// configured compaction trigger: a model whose window the catalogue does
	// not know is exactly the case where an unbounded prompt is riskiest.
	ceiling := r.ceilingFor(advertisedWindow)
	if ceiling <= 0 {
		ceiling = int(float64(r.settings.GetCompactionTriggerTokens()) * float64(r.settings.GetCeilingSafetyPercent()) / 100)
	}
	outcome.Ceiling = ceiling
	outcome.Before = contextreduction.Summarize(system, messages).Total()
	outcome.After = outcome.Before

	// A provider caches an identical prefix from the first token, at roughly
	// a tenth of the price. Rewriting an older tool result to save its body
	// changes that prefix and loses the cache for everything after it, which
	// costs more than keeping a stale body in cache. Digesting and read
	// stubbing therefore run only when the prompt actually exceeds the
	// ceiling; below it the transcript is left byte-for-byte intact.
	out := messages
	var pointerStats contextreduction.PointerStats
	var digestStats contextreduction.DigestStats
	if outcome.Before > ceiling {
		if outcome.Before > 0 {
			out, pointerStats = contextreduction.SupersededReadStubs(messages, r.pointerOptions())
			out, digestStats = contextreduction.DigestToolResults(out, r.digestOptions())
		}
		outcome.PointerStubs = pointerStats.Stubbed
		outcome.Digested = digestStats.Digested
		outcome.Deduplicated = digestStats.Deduplicated
		outcome.After = contextreduction.Summarize(system, out).Total()
	}

	if outcome.Before <= ceiling {
		return out, outcome
	}

	// Whole messages, front first, once the cheap stages were not enough.
	out, dropped := dropOldestToCeiling(system, out, ceiling)
	outcome.DroppedMessages = dropped
	outcome.After = contextreduction.Summarize(system, out).Total()
	outcome.OverBudget = outcome.After > ceiling
	return out, outcome
}

// reductionOutcome reports what a repack had to do, for one log line per turn.
type reductionOutcome struct {
	Ceiling         int
	Before          int
	After           int
	PointerStubs    int
	Digested        int
	Deduplicated    int
	DroppedMessages int
	OverBudget      bool
	Messages        int
}

// Saved returns the tokens the repack removed.
func (o reductionOutcome) Saved() int { return o.Before - o.After }

// dropOldestToCeiling removes whole messages from the front until the prompt
// fits, always keeping the newest two so the live round survives.
func dropOldestToCeiling(system string, messages []fantasy.Message, ceiling int) ([]fantasy.Message, int) {
	const keepNewest = 2
	if ceiling <= 0 || len(messages) <= keepNewest {
		return messages, 0
	}
	if contextreduction.Summarize(system, messages).Total() <= ceiling {
		return messages, 0
	}
	start := 0
	for start < len(messages)-keepNewest {
		if contextreduction.Summarize(system, messages[start:]).Total() <= ceiling {
			return messages[start:], start
		}
		start++
	}
	return messages[start:], start
}

// absorbedPrefix returns how many leading messages a previous compaction
// already folded into a continuation brief, given the session's current
// summary message ID.
//
// getSessionMessages starts the transcript at that summary message and rewrites
// it to a user message, so the brief is the first message and everything before
// it is gone. The count is therefore the position of the brief plus one, and
// everything at or after the count is the delta a new compaction must read.
func absorbedPrefix(msgs []message.Message, summaryMessageID string) int {
	if summaryMessageID == "" {
		return 0
	}
	for i, msg := range msgs {
		if msg.ID == summaryMessageID {
			return i + 1
		}
	}
	return 0
}

// chainFor computes the digest chain hash for a message prefix, so a brief is
// memoized against exactly the content it absorbed.
func chainFor(previous string, messages []fantasy.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(string(msg.Role))
		sb.WriteString(":")
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.TextPart:
				sb.WriteString(p.Text)
			case *fantasy.TextPart:
				sb.WriteString(p.Text)
			case fantasy.ReasoningPart:
				sb.WriteString(p.Text)
			case *fantasy.ReasoningPart:
				sb.WriteString(p.Text)
			case fantasy.ToolCallPart:
				sb.WriteString(p.ToolName)
				sb.WriteString(p.Input)
			case *fantasy.ToolCallPart:
				sb.WriteString(p.ToolName)
				sb.WriteString(p.Input)
			case fantasy.ToolResultPart:
				sb.WriteString(toolResultTextOf(p))
			case *fantasy.ToolResultPart:
				sb.WriteString(toolResultTextOf(*p))
			}
			sb.WriteString("\x1e")
		}
		sb.WriteString("\x1f")
	}
	return contextreduction.ChainHash(previous, sb.String())
}

func toolResultTextOf(part fantasy.ToolResultPart) string {
	switch out := part.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		return out.Text
	case *fantasy.ToolResultOutputContentText:
		if out == nil {
			return ""
		}
		return out.Text
	case fantasy.ToolResultOutputContentError:
		if out.Error == nil {
			return ""
		}
		return out.Error.Error()
	case *fantasy.ToolResultOutputContentError:
		if out == nil || out.Error == nil {
			return ""
		}
		return out.Error.Error()
	default:
		return ""
	}
}

// reducePrompt runs the context-reduction stack over a prepared prompt. It
// returns the prompt unchanged when the stack is disabled, which is the case
// for every test that does not opt in.
func (a *sessionAgent) reducePrompt(systemPrompt string, history []fantasy.Message, advertisedWindow int) ([]fantasy.Message, reductionOutcome) {
	if !a.reduction.enabled() {
		return history, reductionOutcome{Messages: len(history)}
	}
	return a.reduction.repack(systemPrompt, history, advertisedWindow)
}

// promptTokens estimates the size of the messages a request will send, which is
// what a learned ceiling is compared against.
func promptTokens(messages []fantasy.Message) int {
	return contextreduction.Summarize("", messages).Total()
}

// reductionOptions builds the per-agent reduction options from the loaded
// config. A nil return disables the stack, which is what happens when the user
// turned it off.
func (c *coordinator) reductionOptions(model Model) *ReductionOptions {
	cfg := c.cfg.Config()
	settings := cfg.Options.GetContextReduction()
	if !settings.ContextReductionEnabled() {
		return nil
	}
	return &ReductionOptions{
		Settings:  settings,
		DigestDir: digestDirFor(settings, cfg.Options.DataDirectory),
		Provider:  model.ModelCfg.Provider,
		Model:     model.ModelCfg.Model,
	}
}

// editLimits returns the bounds one edit must respect, from the governance
// settings.
func (a *sessionAgent) editLimits() editsafe.Limits {
	limits := editsafe.DefaultLimits()
	if a.editSafety == nil {
		return limits
	}
	if percent := a.editSafety.GetMaxExpectedDiffPercent(); percent > 0 {
		limits.MaxDiffPercent = percent
	}
	if files := a.editSafety.GetMaxUnexpectedFiles(); files > 0 {
		limits.MaxUnexpectedFiles = files
	}
	return limits
}

// interceptShellEdits reports whether a single-file in-place shell edit is
// routed to the edit engine.
func (a *sessionAgent) interceptShellEdits() bool {
	if a.editSafety == nil {
		return true
	}
	return a.editSafety.GetInterceptShell()
}

// journalEdits reports whether edits are recorded for a revert.
func (a *sessionAgent) journalEdits() bool {
	if a.editSafety == nil {
		return true
	}
	return a.editSafety.GetJournal()
}

// expectedEditPaths returns the files the request named, so an edit to them is
// never counted as unexpected. It reads the paths out of the user's own text:
// the turn's contract is what the user asked for, not what the model decided.
func (a *sessionAgent) expectedEditPaths(msgs []message.Message, call SessionAgentCall) []string {
	text := call.Prompt
	for _, msg := range msgs {
		if msg.Role != message.User {
			continue
		}
		text += "\n" + msg.Content().Text
	}
	paths := contextreduction.PathsIn(text, 32)
	abs := make([]string, 0, len(paths))
	for _, path := range paths {
		if filepath.IsAbs(path) {
			abs = append(abs, path)
			continue
		}
		abs = append(abs, filepath.Join(a.workingDir, path))
	}
	return abs
}

// editSafetyConfig returns the edit-safety settings from the loaded config, or
// nil when the user disabled them.
func (c *coordinator) editSafetyConfig() *config.EditSafetyOptions {
	settings := c.cfg.Config().Options.GetEditSafety()
	if !settings.EditSafetyEnabled() {
		return nil
	}
	return settings
}

// compactionTriggerTokens returns the prompt size that starts a compaction, or 0
// to fall back to the model's window.
//
// It is the number the user chose for cost. Compacting only when the window is
// nearly full means every request in between carries the whole transcript, so a
// long session pays for the entire history on every round.
func (a *sessionAgent) compactionTriggerTokens() int64 {
	if a.reduction == nil || a.reduction.settings == nil {
		return 0
	}
	return int64(a.reduction.settings.GetCompactionTriggerTokens())
}

// canCompact reports whether there is enough transcript for a compaction to do
// anything. Compacted below this, a summary would cost more than it saves.
func (r *reductionState) canCompact() bool {
	return r != nil && r.settings != nil
}

// noteFor returns the compaction note written for a summarised window, keyed
// by the message the window starts at. The note is not a stored message: it is
// assembled into the prompt ahead of the kept tail, which is the shape a
// compacted request takes (system, note, tail). Storing it as a message would
// timestamp it after the tail it precedes, and the transcript is read in time
// order.
func (r *reductionState) noteFor(summaryMessageID string) (string, bool) {
	if !r.enabled() || summaryMessageID == "" {
		return "", false
	}
	if memo, ok := r.memo[summaryMessageID]; ok && memo.Brief != "" {
		return memo.Brief, true
	}
	if memo, ok := r.store.Load(summaryMessageID); ok {
		r.memo[summaryMessageID] = memo
		return memo.Brief, true
	}
	return "", false
}

// rememberNote persists the note that goes ahead of a compacted window.
func (r *reductionState) rememberNote(summaryMessageID, note string) {
	if !r.enabled() || summaryMessageID == "" || note == "" {
		return
	}
	memo := contextreduction.DigestMemo{Chain: summaryMessageID, Brief: note}
	r.memo[summaryMessageID] = memo
	if err := r.store.Store(memo); err != nil {
		slog.Warn("Failed to persist a compaction note", "error", err)
	}
}

// briefFor returns the continuation brief that already covers this prefix, if
// one was written before. It never generates one: a brief costs a model call
// over the whole transcript, and compaction runs inside a turn.
//
// The chain is tried for the full prefix first, then for successively shorter
// prefixes. A brief written when the transcript was shorter still holds the
// invariants of the part it covered, which is worth more than no brief at all.
func (r *reductionState) briefFor(messages []fantasy.Message) string {
	if !r.enabled() || len(messages) == 0 {
		return ""
	}
	if brief, ok := r.summaryFor(chainFor("", messages)); ok {
		return brief
	}
	return ""
}

// compactionRecord is one compaction, kept so the UI and logs can report what
// it cost and what it saved without re-measuring the transcript.
type compactionRecord struct {
	SessionID       string
	Trigger         string
	OriginalTokens  int
	BuiltTokens     int
	BudgetTokens    int
	ReserveTokens   int
	KeptMessages    int
	DroppedMessages int
	ReusedBrief     bool
}
