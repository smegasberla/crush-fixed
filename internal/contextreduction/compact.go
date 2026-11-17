package contextreduction

import (
	"fmt"
	"strings"

	"charm.land/fantasy"
)

// Plan is the decision for one prompt build: whether the prompt must be
// compacted, what the target size is, and which messages survive as the
// recent tail.
type Plan struct {
	// Compact is true when the prompt exceeded the trigger.
	Compact bool
	// Trigger and Budget are the token sizes the decision was made against,
	// after the learned ceiling was applied.
	Trigger int
	Budget  int
	// Reserve is the part of Budget held back for the answer.
	Reserve int
	// Total is the prompt's estimated size before any change.
	Total int
	// TailStart is the index of the first message kept verbatim. Everything
	// before it is folded into the summary.
	TailStart int
	// KeptMessages and DroppedMessages count the tail and the folded prefix.
	KeptMessages    int
	DroppedMessages int
	// Reason explains the decision in one clause, for logs.
	Reason string
}

// CompactOptions configures the planner.
type CompactOptions struct {
	// TriggerTokens starts a compaction.
	TriggerTokens int
	// BudgetTokens is the target size after compaction.
	BudgetTokens int
	// ReserveTokens is held back from the budget for the answer.
	ReserveTokens int
	// MinTailMessages is the smallest recent tail worth keeping. Below it
	// there is nothing left to continue from.
	MinTailMessages int
	// MaxTailMessages bounds the tail so a compaction always removes
	// something. Zero means unlimited (bounded only by the budget).
	MaxTailMessages int
	// OverheadTokens is the part of every request that is not conversation:
	// the system prompt and the tool definitions. It is paid on every turn,
	// so a budget that ignored it would size a request larger than the user
	// asked for by exactly this much.
	OverheadTokens int
}

// WithDefaults fills unset fields.
func (o CompactOptions) WithDefaults() CompactOptions {
	if o.TriggerTokens <= 0 {
		o.TriggerTokens = 70000
	}
	if o.BudgetTokens <= 0 {
		o.BudgetTokens = 70000
	}
	if o.ReserveTokens <= 0 {
		o.ReserveTokens = 8192
	}
	if o.MinTailMessages <= 0 {
		o.MinTailMessages = 2
	}
	return o
}

// Compact compacts a conversation that no longer fits.
//
// The result is the recent tail that still fits the budget after the summary
// and the answer reserve. The tail always contains at least MinTailMessages
// messages when the conversation has them, even if that overruns the budget:
// a summary with nothing to continue from is worse than a prompt that is
// slightly too big, and the caller is told the overrun rather than silently
// getting an unusable tail.
func Compact(system string, messages []fantasy.Message, summaryTokens int, opts CompactOptions) ([]fantasy.Message, Plan) {
	opts = opts.WithDefaults()
	budget := Summarize(system, messages)
	plan := Plan{
		Budget:  opts.BudgetTokens,
		Trigger: opts.TriggerTokens,
		Reserve: opts.ReserveTokens,
		Total:   budget.Total(),
	}
	if plan.Total <= opts.TriggerTokens {
		plan.Reason = "within trigger"
		plan.TailStart = 0
		plan.KeptMessages = len(messages)
		return messages, plan
	}
	plan.Compact = true
	plan.Reason = "prompt over trigger"

	available := opts.BudgetTokens - opts.ReserveTokens - summaryTokens
	if available < 0 {
		available = 0
	}

	// Walk the messages from the newest backwards, accumulating sizes until
	// the budget cannot hold one more. The accumulated size is computed from
	// the same estimator the decision above used, so the tail is exactly as
	// big as it was measured to be.
	used := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		size := estimateSingle(messages[i])
		kept := len(messages) - i
		if used+size > available && kept > opts.MinTailMessages {
			break
		}
		used += size
		start = i
		if opts.MaxTailMessages > 0 && kept >= opts.MaxTailMessages {
			break
		}
	}
	plan.TailStart = start
	plan.DroppedMessages = start
	plan.KeptMessages = len(messages) - start
	if plan.DroppedMessages == 0 {
		plan.Compact = false
		plan.Reason = "nothing left to compact"
		return messages, plan
	}
	return messages[start:], plan
}

// estimateSingle returns one message's estimated size.
func estimateSingle(msg fantasy.Message) int {
	return AccountMessage(msg).Total()
}

// SummaryNote is the message that replaces everything the compaction folded
// away. It states what was removed and what stayed, so the model never assumes
// the visible tail is the whole conversation.
func SummaryNote(summary string, dropped int, kept int) string {
	summary = strings.TrimSpace(summary)
	var sb strings.Builder
	if summary != "" {
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "[System note: %d earlier messages were compacted out to fit the context window and are summarized above. The recent conversation and all tool results below are intact; ask the user to re-state anything from the omitted part if you need it.]", dropped)
	if kept > 0 {
		fmt.Fprintf(&sb, " (%d messages kept verbatim.)", kept)
	}
	return sb.String()
}

// DroppedAllNote is used when nothing of the transcript fit the window at all.
const DroppedAllNote = "[... dropped to fit the model's context window: nothing of this message fit alongside the rest of the conversation]"

// FitToCeiling bounds the prompt by the model's ceiling. It first digests aged
// tool results, then supersedes old readings, then, only if the prompt is
// still over, replaces the oldest messages with DroppedAllNote-equivalent
// stubs, newest kept last. It reports what it had to do.
func FitToCeiling(system string, messages []fantasy.Message, ceilingTokens int, digest DigestOptions, pointer PointerOptions) ([]fantasy.Message, FitReport) {
	report := FitReport{
		Ceiling:      ceilingTokens,
		TokensBefore: Summarize(system, messages).Total(),
	}
	if ceilingTokens <= 0 {
		report.TokensAfter = report.TokensBefore
		return messages, report
	}
	if report.TokensBefore <= ceilingTokens {
		report.TokensAfter = report.TokensBefore
		return messages, report
	}

	out, dstat := DigestToolResults(messages, digest)
	if dstat.Saved() > 0 {
		report.Digest = dstat
	}
	out, pstat := SupersededReadStubs(out, pointer)
	if pstat.Saved() > 0 {
		report.Pointer = pstat
	}
	current := Summarize(system, out).Total()
	if current > ceilingTokens {
		out, report.DroppedMessages = dropOldest(out, system, ceilingTokens)
		current = Summarize(system, out).Total()
	}
	report.TokensAfter = current
	report.OverBudget = current > ceilingTokens
	return out, report
}

// FitReport explains what bounding a prompt had to do.
type FitReport struct {
	// Ceiling is the token limit the prompt was fitted to.
	Ceiling int
	// TokensBefore and TokensAfter are the prompt sizes around the fit.
	TokensBefore int
	TokensAfter  int
	// Digest and Pointer are the per-stage results, zero when a stage ran but
	// saved nothing.
	Digest  DigestStats
	Pointer PointerStats
	// DroppedMessages is how many whole messages had to be dropped after the
	// cheap stages were exhausted.
	DroppedMessages int
	// OverBudget is true when even dropping everything cheap did not fit.
	OverBudget bool
}

// dropOldest removes whole messages from the front until the prompt fits,
// never removing the newest MinTailMessages-equivalent pair.
func dropOldest(messages []fantasy.Message, system string, ceiling int) ([]fantasy.Message, int) {
	const keepNewest = 2
	if len(messages) <= keepNewest {
		return messages, 0
	}
	// The note that says what happened costs tokens too, so it is counted
	// while searching for the cut. Otherwise the "fitted" prompt is over the
	// ceiling by exactly the size of its own explanation.
	noteTokens := estimateSingle(fantasy.NewUserMessage(DroppedAllNote))
	start := 0
	for start < len(messages)-keepNewest {
		if Summarize(system, messages[start:]).Total()+noteTokens <= ceiling {
			break
		}
		start++
	}
	if start == 0 {
		return messages, 0
	}
	out := make([]fantasy.Message, 0, len(messages)-start+1)
	out = append(out, fantasy.NewUserMessage(DroppedAllNote))
	out = append(out, messages[start:]...)
	return out, start
}

// CompactLocally builds the prompt a compacted session sends, without a model
// call. It is the whole point of the design: measuring the transcript, choosing
// a recent tail that fits the budget and stamping a note costs microseconds,
// where asking a model to write a summary costs a full round-trip (30-40
// seconds on a large transcript).
//
// The transcript itself is not touched. Compaction chooses where the visible
// history starts; every message remains in the database, so the decision is
// reversible and the user can scroll back through what was folded.
//
// brief, when non-empty, is a continuation brief already written for an earlier
// fold. It is placed ahead of the tail so its invariants survive, and it is
// never generated here: a brief is produced off the critical path and this
// function only reuses what exists.
func CompactLocally(system string, messages []fantasy.Message, brief string, opts CompactOptions) ([]fantasy.Message, Plan) {
	opts = opts.WithDefaults()
	plan := Plan{
		Budget:  opts.BudgetTokens,
		Trigger: opts.TriggerTokens,
		Reserve: opts.ReserveTokens,
		Total:   Summarize(system, messages).Total(),
	}
	if plan.Total <= opts.TriggerTokens {
		plan.Reason = "within trigger"
		plan.KeptMessages = len(messages)
		return messages, plan
	}

	// The budget is the target size of the prompt itself, and the reserve is
	// headroom the answer gets on top of it, so the reserve is not taken out
	// of the budget. Subtracting it would make a compaction fold further than
	// the user asked for while the prompt sent is smaller than the budget
	// anyway.
	//
	// The brief and the note are paid for out of the budget, because they are
	// part of the prompt being built. So is the overhead: the system prompt
	// and tool definitions ride on every request, and leaving them out made a
	// "25000 token" budget arrive at the provider as 30K.
	available := opts.BudgetTokens - opts.OverheadTokens
	if available < 0 {
		available = 0
	}
	if brief != "" {
		available -= estimateSingle(fantasy.NewUserMessage(brief))
	}
	noteTokens := estimateSingle(fantasy.NewUserMessage(SummaryNote("", len(messages), 0)))
	available -= noteTokens
	if available < 0 {
		available = 0
	}

	// Walk from the newest backwards until the budget cannot hold one more.
	// The tail is verbatim: compaction drops old context, it never rewrites
	// what it keeps.
	used := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		size := estimateSingle(messages[i])
		kept := len(messages) - i
		if used+size > available && kept > opts.MinTailMessages {
			break
		}
		used += size
		start = i
		if opts.MaxTailMessages > 0 && kept >= opts.MaxTailMessages {
			break
		}
	}

	plan.TailStart = start
	plan.DroppedMessages = start
	plan.KeptMessages = len(messages) - start
	if plan.DroppedMessages == 0 {
		plan.Reason = "nothing left to compact"
		return messages, plan
	}

	plan.Compact = true
	plan.Reason = "prompt over trigger"
	note := SummaryNote(brief, plan.DroppedMessages, plan.KeptMessages)
	out := make([]fantasy.Message, 0, plan.KeptMessages+1)
	out = append(out, fantasy.NewUserMessage(note))
	out = append(out, messages[start:]...)
	return out, plan
}
