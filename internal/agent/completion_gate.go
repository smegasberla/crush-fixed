package agent

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// The completion gate is the "did this turn actually do what it says" check. It
// only ever fires on evidence the turn itself produced: a claim with no tool
// result behind it, or a plan closed with nothing landed. It never infers
// intent, so a question answered in prose is never blocked.
//
// A blocked finish queues one follow-up asking for the work, which is a cheap
// correction. Retrying forever is not: after completionGateMaxBlocks the turn
// ends so a model that cannot satisfy the gate cannot loop on it.

const (
	// completionGateMaxBlocks is how many times one turn may have its finish
	// refused before the turn is allowed to end anyway.
	completionGateMaxBlocks = 1
)

// completionVerdict is the gate's decision for a finished turn.
type completionVerdict struct {
	// Blocked is true when the claimed work has no support in this turn.
	Blocked bool
	// Reason explains the refusal in one clause, for the follow-up prompt.
	Reason string
}

// completionEvidence is what the turn actually did, as the gate sees it.
type completionEvidence struct {
	// ToolCalls is how many tool calls ran this turn.
	ToolCalls int
	// Edits is how many of them mutated the workspace.
	Edits int
	// Runs is how many commands executed.
	Runs int
	// FailedCalls is how many tool calls errored.
	FailedCalls int
	// ClaimsEdit is true when the answer says it changed something.
	ClaimsEdit bool
	// ClaimsVerification is true when the answer says it verified something.
	ClaimsVerification bool
	// Prose is the finished answer, for the claim scan.
	Prose string
}

// claimPatterns recognise a claim of completed work. They are deliberately
// narrow: "I changed nothing" and "here is what I would change" must not match,
// because those are honest answers the gate must never punish.
var claimPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:i|we)(?:'ve|'re|\s+have|\s+had)?\s+(?:changed|modified|updated|edited|added|removed|deleted|created|fixed|implemented|refactored|renamed|landed|applied)\b`),
	regexp.MustCompile(`(?i)\b(?:i|we)\s+(?:have\s+)?(?:made|applied|landed|committed)\s+(?:the|these|those|a|an)\b`),
	regexp.MustCompile(`(?i)\bthe\s+change\s+(?:is|has been)\s+(?:made|applied|landed)`),
}

// negationPatterns mark a claim as retracted. A sentence that denies doing the
// work must not be read as claiming it.
var negationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:not|never|didn'?t|did not|haven'?t|have not|couldn'?t|could not)\b[^.\n]{0,40}\b(?:change|edit|modify|update|fix|implement|create|delete)`),
	regexp.MustCompile(`(?i)\b(?:no|nothing)\b[^.\n]{0,20}\b(?:changes?|edits?|modifications?)\b`),
	regexp.MustCompile(`(?i)\b(?:would|could|should)\s+(?:change|edit|modify|update|fix)\b`),
	regexp.MustCompile(`(?i)\b(?:to\s+make|you\s+(?:should|need\s+to)|i\s+would)\b[^.\n]{0,30}\b(?:change|edit|update|fix)\b`),
}

// verificationPatterns recognise a claim that the work was checked.
var verificationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:tests?|build|checks?|lint|typecheck|type check)\s+(?:pass|passed|succeed|succeeded|is green|are green)\b`),
	regexp.MustCompile(`(?i)\b(?:i|we)\s+(?:verified|confirmed|ran the tests?|ran the build)\b`),
	regexp.MustCompile(`(?i)\b(?:verified|tested)\s+(?:it|this|that|the change)\b`),
}

// ClaimsEdit reports whether prose claims a completed change.
func ClaimsEdit(prose string) bool {
	negated := negatedSpans(prose)
	for _, pattern := range claimPatterns {
		loc := pattern.FindStringIndex(prose)
		if loc == nil {
			continue
		}
		if insideAny(loc[0], negated) {
			continue
		}
		return true
	}
	return false
}

// ClaimsVerification reports whether prose claims the work was checked.
func ClaimsVerification(prose string) bool {
	for _, pattern := range verificationPatterns {
		if pattern.MatchString(prose) {
			return true
		}
	}
	return false
}

// negatedSpans returns the byte ranges covered by a negated claim.
func negatedSpans(prose string) [][2]int {
	var spans [][2]int
	for _, pattern := range negationPatterns {
		for _, loc := range pattern.FindAllStringIndex(prose, -1) {
			spans = append(spans, [2]int{loc[0], loc[1]})
		}
	}
	return spans
}

// insideAny reports whether position falls inside one of the spans.
func insideAny(position int, spans [][2]int) bool {
	for _, span := range spans {
		if position >= span[0] && position < span[1] {
			return true
		}
	}
	return false
}

// evaluateCompletion decides whether a finish claim is supported.
//
// The rules are one-way: evidence can only clear a claim, never create one. A
// turn that changed a file and says nothing is fine; a turn that changed
// nothing and says it did is refused.
func evaluateCompletion(evidence completionEvidence) completionVerdict {
	if !evidence.ClaimsEdit && !evidence.ClaimsVerification {
		return completionVerdict{}
	}
	if evidence.ClaimsEdit && evidence.Edits == 0 {
		return completionVerdict{
			Blocked: true,
			Reason:  "the answer says the work was changed, but no edit ran this turn",
		}
	}
	if evidence.ClaimsVerification && evidence.Runs == 0 {
		return completionVerdict{
			Blocked: true,
			Reason:  "the answer says the change was verified, but no command ran this turn",
		}
	}
	return completionVerdict{}
}

// completionGateFollowUp is the message queued after a blocked finish. It names
// the mismatch and asks for the smallest thing that resolves it, so the retry
// costs one round instead of another whole turn.
func completionGateFollowUp(reason string) string {
	return "Your previous reply claimed work that this turn's tool results do not support: " + reason + ". " +
		"Do the work now with real tool calls and verify it, or state plainly what remains and why. " +
		"If the user's message needs no tool work at all — a greeting, a question about the code, something to discuss — reply to it directly and end the turn."
}

// reasoningTruth tracks whether the provider actually honoured the reasoning
// parameters Crush sent. A provider that silently ignores reasoning_effort makes
// every effort knob meaningless, so the fact is recorded and exposed instead of
// assumed.
type reasoningTruth struct {
	mu sync.Mutex
	// dropped records a parameter the provider refused, by name.
	dropped map[string]string
	// honored is true once a response carried reasoning content while a
	// reasoning parameter was sent.
	honored bool
	// observed is true once any response has been inspected.
	observed bool
}

// newReasoningTruth returns an empty tracker.
func newReasoningTruth() *reasoningTruth {
	return &reasoningTruth{dropped: make(map[string]string)}
}

// dropsReasoningParameter reports whether err says a reasoning parameter was
// rejected. Providers word this differently, so the check is on the parameter
// name plus an unsupported-parameter signal, never on the message alone.
func dropsReasoningParameter(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var providerErr *providerErrorStatus
	if errors.As(err, &providerErr) {
		if providerErr.StatusCode == http.StatusUnauthorized || providerErr.StatusCode == http.StatusForbidden {
			return "", false
		}
	}
	msg := strings.ToLower(err.Error())
	parameter := ""
	switch {
	case strings.Contains(msg, "reasoning_effort"):
		parameter = "reasoning_effort"
	case strings.Contains(msg, "reasoning"):
		parameter = "reasoning"
	case strings.Contains(msg, "thinking"):
		parameter = "thinking"
	case strings.Contains(msg, "budget_tokens"):
		parameter = "budget_tokens"
	case strings.Contains(msg, "verbosity"):
		parameter = "verbosity"
	default:
		return "", false
	}
	unsupported := strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "unknown") ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "unrecognized")
	if !unsupported {
		return "", false
	}
	return parameter, true
}

// providerErrorStatus is the minimal surface of a provider error the tracker
// needs. It is declared here rather than imported so the check stays a pure
// function of the error text in tests.
type providerErrorStatus struct {
	StatusCode int
	Message    string
}

func (e *providerErrorStatus) Error() string { return e.Message }

// noteDropped records that a parameter was refused for the rest of the turn.
func (t *reasoningTruth) noteDropped(parameter string) {
	if t == nil || parameter == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropped[parameter] = parameter
}

// droppedParameter returns a parameter the provider refused, if any.
func (t *reasoningTruth) droppedParameter() (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for parameter := range t.dropped {
		return parameter, true
	}
	return "", false
}

// observeResponse records whether a response actually carried reasoning. It is
// the check that catches a provider accepting reasoning_effort and ignoring it.
func (t *reasoningTruth) observeResponse(sentReasoning bool, gotReasoning bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observed = true
	if sentReasoning && gotReasoning {
		t.honored = true
	}
}

// honorsReasoning reports whether the provider has ever been observed to honour
// a reasoning parameter, and whether that has been observed at all.
func (t *reasoningTruth) honorsReasoning() (honored bool, observed bool) {
	if t == nil {
		return false, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.honored, t.observed
}

// reasoningDroppedNote tells the model one of its parameters was refused, so a
// change in behaviour is not mistaken for a change in task.
func reasoningDroppedNote(parameter string) string {
	return "[Reasoning] The " + parameter + " parameter this app sent was refused by the provider and has been dropped for the rest of this turn. Nothing about your task changed and no work was lost — continue."
}
