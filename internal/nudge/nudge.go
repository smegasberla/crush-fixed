// Package nudge turns observations about a turn into short, specific notes that
// tell the model how to get unstuck. It is the "why is this going wrong" layer:
// it names the exact failure mode and the smallest action that fixes it, and it
// never fires twice for the same state.
package nudge

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// Reason identifies why a nudge fired. The set is the failure taxonomy: each
// one names a distinct way a turn wastes rounds, because a single "stop
// looping" message cannot tell the model what to do differently.
type Reason string

const (
	// ReasonStutter is repeated prose: the same sentence said again.
	ReasonStutter Reason = "stutter"
	// ReasonNoProgress is consecutive rounds that changed nothing.
	ReasonNoProgress Reason = "no_progress"
	// ReasonParallel is one-tool-call-per-round serialisation.
	ReasonParallel Reason = "parallel"
	// ReasonRemainingWork is declared work still open at the end of a turn.
	ReasonRemainingWork Reason = "remaining_work"
	// ReasonFormat is a malformed tool call.
	ReasonFormat Reason = "format"
	// ReasonReadStorm is rounds of reads with no change.
	ReasonReadStorm Reason = "read_storm"
	// ReasonRepeatCall is an identical tool call.
	ReasonRepeatCall Reason = "repeat_call"
	// ReasonPlanOpenUnfinished is a plan marked complete with nothing changed.
	ReasonPlanOpenUnfinished Reason = "plan_open_unfinished"
	// ReasonFalseCompletion is a finish claim the turn's evidence does not
	// support.
	ReasonFalseCompletion Reason = "false_completion"
	// ReasonRecapOnly is a reply that was only the closing summary.
	ReasonRecapOnly Reason = "recap_only"
	// ReasonBackgroundPolling is rounds spent checking a long-running job.
	ReasonBackgroundPolling Reason = "background_polling"
	// ReasonEmptyAnswer is reasoning with neither prose nor an action.
	ReasonEmptyAnswer Reason = "empty_answer"
	// ReasonReasoningOverrun is thinking that never converged on an action.
	ReasonReasoningOverrun Reason = "reasoning_overrun"
	// ReasonSmallestEdit is reading long past the point of acting.
	ReasonSmallestEdit Reason = "smallest_edit"
)

// Note is one nudge: why it fired and what it says.
type Note struct {
	Reason Reason
	// Text is the instruction delivered to the model.
	Text string
	// Severity is 1 for advice and 2 for a blocked claim, so a caller can
	// tell a steer from a refusal.
	Severity int
}

// Counters are the per-turn observations the engine decides on. They are
// deliberately explicit rather than inferred here: the caller knows what a
// round was, and the engine owns only the policy.
type Counters struct {
	// Round is the current round number, from 1.
	Round int
	// TotalRounds is how many rounds the turn has taken.
	TotalRounds int
	// ConsecutiveReadOnly is rounds in a row with no mutation.
	ConsecutiveReadOnly int
	// ConsecutiveSingleCall is rounds in a row that each made exactly one
	// tool call.
	ConsecutiveSingleCall int
	// ConsecutiveNoProgress is rounds in a row that changed nothing.
	ConsecutiveNoProgress int
	// RepeatedCallCount is how many times the current call already ran.
	RepeatedCallCount int
	// RepeatedProseCount is how many sentences were collapsed as repeats.
	RepeatedProseCount int
	// MalformedCalls is how many tool calls could not be parsed this round.
	MalformedCalls int
	// BackgroundPolls is how many rounds in a row were only job checks.
	BackgroundPolls int
	// ReasoningChars is how much thinking this round produced.
	ReasoningChars int
	// ProseChars is how much answer text this round produced.
	ProseChars int
	// ToolCalls is how many tool calls this round made.
	ToolCalls int
	// Edits is how many files this turn has changed.
	Edits int
	// Runs is how many commands the turn has executed.
	Runs int
	// TodosOpen is how many todo items are not completed.
	TodosOpen int
	// TodosTotal is how many todo items exist.
	TodosTotal int
	// Planning is true when the turn declared a plan.
	Planning bool
	// RecapBlock is true when the round's prose reads as a closing summary.
	// The caller decides this with [LooksLikeRecap]; it is not inferred here
	// because only the caller knows what the round actually produced.
	RecapBlock bool
	// ClaimsCompletion is true when the round announced it was finished. It
	// separates "claimed done, evidence missing" from "a plan closed with
	// nothing landed", which are different failures with different fixes.
	ClaimsCompletion bool
}

// Config holds the thresholds. Defaults are tuned so a normal turn never sees a
// note: a read storm needs four consecutive read-only rounds, and serialisation
// needs three.
type Config struct {
	// Disabled turns every note off.
	Disabled bool
	// ReadStormRounds is how many read-only rounds before the read-storm note.
	ReadStormRounds int
	// SingleCallRounds is how many one-call rounds before the parallel note.
	SingleCallRounds int
	// NoProgressRounds is how many unchanged rounds before the no-progress
	// note.
	NoProgressRounds int
	// RepeatCallAfter is how many identical runs of a call before it is
	// called out.
	RepeatCallAfter int
	// StutterSentences is how many collapsed repeats trigger the stutter
	// note.
	StutterSentences int
	// BackgroundPolls is how many consecutive job checks trigger the polling
	// note.
	BackgroundPolls int
	// ReasoningOverrunChars is how much thinking with no output or action
	// counts as overrun.
	ReasoningOverrunChars int
	// SmallestEditReads is how many read-only rounds plus which minimum
	// total rounds make "act now" appropriate.
	SmallestEditReads  int
	SmallestEditRounds int
}

// DefaultConfig returns the tuned thresholds.
func DefaultConfig() Config {
	return Config{
		ReadStormRounds:       4,
		SingleCallRounds:      3,
		NoProgressRounds:      3,
		RepeatCallAfter:       1,
		StutterSentences:      1,
		BackgroundPolls:       2,
		ReasoningOverrunChars: 4000,
		SmallestEditReads:     5,
		SmallestEditRounds:    6,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.ReadStormRounds <= 0 {
		c.ReadStormRounds = d.ReadStormRounds
	}
	if c.SingleCallRounds <= 0 {
		c.SingleCallRounds = d.SingleCallRounds
	}
	if c.NoProgressRounds <= 0 {
		c.NoProgressRounds = d.NoProgressRounds
	}
	if c.RepeatCallAfter <= 0 {
		c.RepeatCallAfter = d.RepeatCallAfter
	}
	if c.StutterSentences <= 0 {
		c.StutterSentences = d.StutterSentences
	}
	if c.BackgroundPolls <= 0 {
		c.BackgroundPolls = d.BackgroundPolls
	}
	if c.ReasoningOverrunChars <= 0 {
		c.ReasoningOverrunChars = d.ReasoningOverrunChars
	}
	if c.SmallestEditReads <= 0 {
		c.SmallestEditReads = d.SmallestEditReads
	}
	if c.SmallestEditRounds <= 0 {
		c.SmallestEditRounds = d.SmallestEditRounds
	}
	return c
}

// Engine decides which nudges to emit. It remembers what it already said so a
// note is delivered once per state, not on every round that state persists.
type Engine struct {
	mu        sync.Mutex
	cfg       Config
	delivered map[Reason]int
}

// New creates an engine.
func New(cfg Config) *Engine {
	return &Engine{cfg: cfg.withDefaults(), delivered: make(map[Reason]int)}
}

// Reset clears the delivered state, for a new turn.
func (e *Engine) Reset() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.delivered = make(map[Reason]int)
}

// Evaluate returns the notes that should be delivered for the current counters,
// in priority order. At most one advisory note is returned per call: two
// competing instructions are worse than one, and the highest-severity finding
// is the one that decides the next round.
func (e *Engine) Evaluate(c Counters) []Note {
	if e == nil || e.cfg.Disabled {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// Refusals are evaluated first and are not de-duplicated: a false
	// completion claim must be rejected every time it is made.
	if note, ok := falseCompletionNote(c); ok {
		return []Note{note}
	}
	if note, ok := recapOnlyNote(c); ok {
		return []Note{note}
	}
	if note, ok := planOpenUnfinishedNote(c); ok {
		return []Note{note}
	}

	candidates := []func(Counters) (Note, bool){
		e.repeatCallNote,
		e.malformedNote,
		e.backgroundPollingNote,
		e.noProgressNote,
		e.stutterNote,
		// Reasoning overrun is the more specific diagnosis, so it is asked
		// before the generic "nothing arrived" note.
		e.reasoningOverrunNote,
		e.emptyAnswerNote,
		// The "act now" note supersedes the read-storm note: it says the same
		// thing with a smaller, concrete instruction.
		e.smallestEditNote,
		e.readStormNote,
		e.parallelNote,
		e.remainingWorkNote,
	}
	for _, candidate := range candidates {
		note, ok := candidate(c)
		if !ok {
			continue
		}
		if note.Severity == 0 {
			note.Severity = 1
		}
		if e.alreadyDelivered(note.Reason, c) {
			continue
		}
		e.delivered[note.Reason] = c.Round
		return []Note{note}
	}
	return nil
}

// alreadyDelivered reports whether the note was already sent at this severity
// for the current state. A reason that keeps escalating is allowed to speak
// again after a few rounds, because the situation has changed.
func (e *Engine) alreadyDelivered(reason Reason, c Counters) bool {
	round, ok := e.delivered[reason]
	if !ok {
		return false
	}
	return c.Round-round < 4
}

func (e *Engine) repeatCallNote(c Counters) (Note, bool) {
	if c.RepeatedCallCount <= e.cfg.RepeatCallAfter {
		return Note{}, false
	}
	return Note{
		Reason: ReasonRepeatCall,
		Text: "You have repeated a call this turn, and repeating it will not change what it returns. " +
			"If what you are repeating is a file range: fetching it again will not change what is in it, so if the copy in your context is marked trimmed or capped, take ONLY the range the next edit needs. " +
			"If what you are repeating is a command: nothing has changed underneath it, so run it only after the edit it checks. Either way, if the answer is already in front of you, make the change now instead of looking again.",
	}, true
}

func (e *Engine) malformedNote(c Counters) (Note, bool) {
	if c.MalformedCalls == 0 {
		return Note{}, false
	}
	return Note{
		Reason: ReasonFormat,
		Text: "tool calls in your last message could not be parsed and were NOT executed. " +
			"The usual cause is an unescaped double quote inside a string value. " +
			"Escape every quote inside the value as \\\" , or emit the file as several smaller writes, each with a short value.",
	}, true
}

func (e *Engine) backgroundPollingNote(c Counters) (Note, bool) {
	if c.BackgroundPolls < e.cfg.BackgroundPolls {
		return Note{}, false
	}
	return Note{
		Reason: ReasonBackgroundPolling,
		Text: "The last rounds were nothing but checks of a task that is still running, and each check costs a full round-trip to learn 'still running'. " +
			"Do not check it on a timer. Three ways forward, and only three: " +
			"(1) block on the result with wait=true, which holds this round until the task finishes and hands you its exit code; " +
			"(2) spend the time on the rest of your remaining work now, in this round; " +
			"(3) end your turn — the completion notice reaches you on its own, and you are woken to verify the result.",
	}, true
}

func (e *Engine) noProgressNote(c Counters) (Note, bool) {
	if c.ConsecutiveNoProgress < e.cfg.NoProgressRounds {
		return Note{}, false
	}
	return Note{
		Reason: ReasonNoProgress,
		Text: "Several rounds have now passed with no new progress — no file changed and no command ran that had not already run since the last change. " +
			"Repeating the same reads or builds cannot produce new information. " +
			"Act now: make the change the failing output implies, or answer the user directly.",
	}, true
}

func (e *Engine) stutterNote(c Counters) (Note, bool) {
	if c.RepeatedProseCount < e.cfg.StutterSentences {
		return Note{}, false
	}
	return Note{
		Reason: ReasonStutter,
		Text:   "Your previous message repeated itself. Say it once: one statement per fact, no restating, no summary of what you just wrote.",
	}, true
}

func (e *Engine) emptyAnswerNote(c Counters) (Note, bool) {
	if c.ProseChars > 0 || c.ToolCalls > 0 {
		return Note{}, false
	}
	return Note{
		Reason: ReasonEmptyAnswer,
		Text: "Your previous message reached the user as NOTHING — the thinking was not sent, and no tool ran either. " +
			"Everything you looked up above is yours to use. Write the answer to the user's question now, in prose, and send it.",
	}, true
}

func (e *Engine) reasoningOverrunNote(c Counters) (Note, bool) {
	if c.ReasoningChars < e.cfg.ReasoningOverrunChars {
		return Note{}, false
	}
	if c.ProseChars > 0 || c.ToolCalls > 0 {
		return Note{}, false
	}
	return Note{
		Reason: ReasonReasoningOverrun,
		Text: fmt.Sprintf("You produced %d characters of reasoning and no action. "+
			"The plan you already have is the one to run: emit the next concrete call now.", c.ReasoningChars),
	}, true
}

func (e *Engine) readStormNote(c Counters) (Note, bool) {
	if c.ConsecutiveReadOnly < e.cfg.ReadStormRounds {
		return Note{}, false
	}
	return Note{
		Reason: ReasonReadStorm,
		Text: "Reading has run ahead of changing: several rounds of nothing but reads, no file changed yet, and your own plan's next unfinished step is a change. " +
			"A further read cannot tell you whether you have read enough — only an edit and its check can. " +
			"Emit the SMALLEST edit you can already state: one line, symbol or value whose current text you have been shown above. " +
			"Put the command that checks it — build, test, or the run that exercises it — in the SAME round as that edit, then let the result choose the next step. " +
			"If your plan really contains no change, answer the user instead.",
	}, true
}

func (e *Engine) smallestEditNote(c Counters) (Note, bool) {
	if c.TotalRounds < e.cfg.SmallestEditRounds {
		return Note{}, false
	}
	if c.ConsecutiveReadOnly < e.cfg.SmallestEditReads {
		return Note{}, false
	}
	return Note{
		Reason: ReasonSmallestEdit,
		Text: "This turn has now run many rounds. Stop gathering and act: make the smallest edit you can already state, or answer the user directly with what you have. " +
			"A line-addressed edit needs no verbatim copy and no re-read.",
	}, true
}

func (e *Engine) parallelNote(c Counters) (Note, bool) {
	if c.ConsecutiveSingleCall < e.cfg.SingleCallRounds {
		return Note{}, false
	}
	return Note{
		Reason: ReasonParallel,
		Text: "You executed exactly ONE tool call in each of your last rounds. " +
			"Independent lookups belong in one round: read several files in a single call, and emit a search plus the reads it implies together when you already know the paths. " +
			"Never batch a read whose path depends on a result you have not seen yet.",
	}, true
}

func (e *Engine) remainingWorkNote(c Counters) (Note, bool) {
	if c.TodosOpen == 0 {
		return Note{}, false
	}
	if c.Round < 3 {
		return Note{}, false
	}
	return Note{
		Reason: ReasonRemainingWork,
		Text: fmt.Sprintf("%d of %d plan step(s) are not completed. Do not end the turn with the plan only partially done: "+
			"finish the next unfinished step, or say plainly which step is blocked and why.", c.TodosOpen, c.TodosTotal),
	}, true
}

// falseCompletionNote refuses a finish claim the turn's own evidence does not
// support. It applies to a claim made without a plan: with a plan, closing it
// while nothing landed is the more specific plan_open_unfinished finding.
func falseCompletionNote(c Counters) (Note, bool) {
	if !c.ClaimsCompletion || c.Planning {
		return Note{}, false
	}
	if c.Edits > 0 || c.Runs > 0 {
		return Note{}, false
	}
	return Note{
		Reason:   ReasonFalseCompletion,
		Severity: 2,
		Text: "Your completion claims edits or successful verification that this turn's tool results do not support. " +
			"The completion was not accepted. Perform the work with real tool calls, or report accurately what remains and any blockers. " +
			"If the user's message needs no tool work at all — a greeting, a question about the code, something to discuss — reply to it directly and end the turn; do not call tools to satisfy this note.",
	}, true
}

// recapOnlyNote refuses a reply that was only the closing summary.
func recapOnlyNote(c Counters) (Note, bool) {
	if c.Planning {
		return Note{}, false
	}
	if c.ProseChars == 0 || c.ToolCalls > 0 {
		return Note{}, false
	}
	if !c.RecapBlock {
		return Note{}, false
	}
	return Note{
		Reason:   ReasonRecapOnly,
		Severity: 2,
		Text: "NOT A FINISHED ANSWER — your entire reply was the closing recap / next-steps block, and no tool ran this turn, so the requested work was not done. " +
			"The closing block belongs only on the message that delivers the work. Do the requested work now, verify it, and only then report the result and close. " +
			"If the work genuinely cannot proceed without the user, ask the question that unblocks it instead of closing.",
	}, true
}

// planOpenUnfinishedNote refuses a plan that closed with nothing landed.
func planOpenUnfinishedNote(c Counters) (Note, bool) {
	if !c.Planning || c.TodosTotal == 0 {
		return Note{}, false
	}
	if c.TodosOpen > 0 || c.Edits > 0 || c.Runs > 0 || !c.ClaimsCompletion {
		return Note{}, false
	}
	return Note{
		Reason:   ReasonPlanOpenUnfinished,
		Severity: 2,
		Text: "your summary says the work is not finished, but no plan exists and no change has landed. " +
			"Take the next concrete step now: make the edit, run the check, and report the result.",
	}, true
}

// ScoreOverthinking summarizes a turn's reasoning behaviour. It is the measured
// half of the anti-overthinking story: post-decision tail length and restart
// density are what tell an operator that the model is spinning, so tuning is
// driven by numbers rather than by impression.
type Score struct {
	// Rounds is how many rounds were measured.
	Rounds int
	// ThinkingRounds is how many of them produced reasoning.
	ThinkingRounds int
	// ProgressRounds is how many changed something.
	ProgressRounds int
	// ReasonChars is the total reasoning text generated.
	ReasonChars int
	// Restarts is how many times the reasoning revised itself.
	Restarts int
	// WorstTailChars is the longest post-decision tail seen.
	WorstTailChars int
	// TailRatio is WorstTailChars divided by the largest answer seen in the
	// same turn; 0 when there was no answer.
	TailRatio float64
}

// ReasoningShare returns reasoning tokens as a share of all generated text.
func (s Score) ReasoningShare() float64 {
	if s.ReasonChars == 0 {
		return 0
	}
	return float64(s.ReasonChars) / float64(s.ReasonChars+s.Rounds*answerScale)
}

// answerScale keeps ReasoningShare meaningful without tracking answer size
// separately: a round's answer is assumed to be this many characters until
// measured.
const answerScale = 200

// RestartDensity returns self-revisions per 1000 reasoning characters. A model
// that reconsiders more than about two times per thousand characters is not
// converging.
func (s Score) RestartDensity() float64 {
	if s.ReasonChars <= 0 {
		return 0
	}
	return float64(s.Restarts) / (float64(s.ReasonChars) / 1000)
}

// NeedsRecovery reports whether the measured behaviour calls for an
// intervention rather than another attempt.
func (s Score) NeedsRecovery() bool {
	return s.RestartDensity() >= 2 || (s.ThinkingRounds >= 3 && s.ProgressRounds == 0)
}

// ReasoningNote renders the advice for a spin.
func ReasoningNote() string {
	return "[Reasoning] The previous strategy has not changed the observed outcome. Do not repeat it unchanged. " +
		"Review the existing failure, recent changes, and relevant evidence. Identify the unresolved assumption or choose a materially different approach before another attempt."
}

// ReassessNote tells the model to reuse evidence it already has.
func ReassessNote() string {
	return "[Reasoning] Reassess the current state using the evidence already available. Avoid repeating previous reasoning or broad exploration without new information. Select a reasonable next step and make concrete progress."
}

// ReasoningRefusedNote explains that a provider dropped a reasoning parameter.
func ReasoningRefusedNote() string {
	return "[Reasoning] One of the reasoning parameters this app sent was refused by the provider and has been dropped for the rest of this turn. Nothing about your task changed and no work was lost — continue."
}

// Clamp tails a ratio into [0,1] for reporting.
func Clamp(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

// SummarizeReasoning folds one round into a running score. tailChars is the
// reasoning produced after the model's decision, which is the part that never
// pays for itself.
func SummarizeReasoning(score Score, reasonChars, tailChars, proseChars int, madeProgress bool, restarts int) Score {
	score.Rounds++
	if reasonChars > 0 {
		score.ThinkingRounds++
	}
	if madeProgress {
		score.ProgressRounds++
	}
	score.ReasonChars += reasonChars
	score.Restarts += restarts
	if tailChars > score.WorstTailChars {
		score.WorstTailChars = tailChars
		ratio := 1.0
		if proseChars > 0 {
			ratio = float64(tailChars) / float64(proseChars)
		}
		if ratio > score.TailRatio {
			score.TailRatio = Clamp(ratio)
		}
	}
	return score
}

// RecapMarkers are the phrases a closing summary uses. They are matched
// case-insensitively and only at the start of the round's prose, so a
// substantive answer that happens to mention "next steps" is not flagged.
var RecapMarkers = []string{
	"### suggested next steps",
	"### next steps",
	"## next steps",
	"next steps:",
	"summary of changes",
	"in summary,",
	"to summarise",
	"to summarize",
}

// LooksLikeRecap reports whether prose is only a wrap-up. It requires the
// marker to open the text, because that is the shape a malformed turn takes:
// the model closes instead of doing the work.
func LooksLikeRecap(prose string) bool {
	trimmed := strings.ToLower(strings.TrimLeft(strings.TrimSpace(prose), "#* \t\r\n"))
	if trimmed == "" {
		return false
	}
	for _, marker := range RecapMarkers {
		clean := strings.ToLower(strings.TrimLeft(strings.TrimSpace(marker), "#* \t"))
		if strings.HasPrefix(trimmed, clean) {
			return true
		}
	}
	return false
}
