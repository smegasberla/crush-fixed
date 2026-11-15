package agent

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimsEdit(t *testing.T) {
	t.Parallel()

	claims := []string{
		"I changed main.go to guard the nil check.",
		"We updated the schema and added the index.",
		"I've fixed the panic and implemented the retry.",
		"The change has been applied.",
		"I created the missing migration.",
	}
	for _, prose := range claims {
		require.True(t, ClaimsEdit(prose), "should claim an edit: %q", prose)
	}

	notClaims := []string{
		// A bare acknowledgement answers the question; it never claims the
		// workspace changed, so the gate must not treat it as one.
		"Done.",
		"The bug is in the wait loop.",
		"Here is how you should change it.",
		"I did not change anything.",
		"No changes were made.",
		"I would change the retry policy.",
		"To fix this, update the dependency.",
		"It depends on the schema.",
		"",
	}
	for _, prose := range notClaims {
		require.False(t, ClaimsEdit(prose), "should not claim an edit: %q", prose)
	}
}

func TestClaimsVerification(t *testing.T) {
	t.Parallel()

	claims := []string{
		"Tests pass.",
		"The build passed.",
		"I verified the change with go test.",
		"Checks are green.",
		"Lint succeeded.",
	}
	for _, prose := range claims {
		require.True(t, ClaimsVerification(prose), "should claim verification: %q", prose)
	}

	notClaims := []string{
		"The tests will pass once you fix the import.",
		"Nothing was verified.",
		"Here is the test to run.",
		"",
	}
	for _, prose := range notClaims {
		require.False(t, ClaimsVerification(prose), "should not claim verification: %q", prose)
	}
}

func TestEvaluateCompletionAllowsHonestTurns(t *testing.T) {
	t.Parallel()

	// Nothing claimed, nothing needed.
	require.False(t, evaluateCompletion(completionEvidence{Prose: "Hello!"}).Blocked)
	require.False(t, evaluateCompletion(completionEvidence{Prose: "The bug is in main.go."}).Blocked)

	// A claim backed by evidence.
	require.False(t, evaluateCompletion(completionEvidence{
		Prose:      "I changed main.go.",
		ClaimsEdit: true,
		Edits:      1,
	}).Blocked)
	require.False(t, evaluateCompletion(completionEvidence{
		Prose:              "Tests pass.",
		ClaimsVerification: true,
		Runs:               1,
	}).Blocked)
}

func TestEvaluateCompletionBlocksUnsupportedClaims(t *testing.T) {
	t.Parallel()

	editClaim := evaluateCompletion(completionEvidence{
		Prose:      "I fixed the panic.",
		ClaimsEdit: true,
		Edits:      0,
	})
	require.True(t, editClaim.Blocked)
	require.Contains(t, editClaim.Reason, "no edit ran")

	verifyClaim := evaluateCompletion(completionEvidence{
		Prose:              "Tests pass.",
		ClaimsVerification: true,
		Runs:               0,
		Edits:              3,
	})
	require.True(t, verifyClaim.Blocked)
	require.Contains(t, verifyClaim.Reason, "no command ran")
}

func TestCompletionGateFollowUpNamesTheMismatch(t *testing.T) {
	t.Parallel()

	text := completionGateFollowUp("no edit ran this turn")
	require.Contains(t, text, "no edit ran this turn")
	require.Contains(t, text, "tool results do not support")
	require.Contains(t, text, "needs no tool work at all", "an honest prose answer must stay allowed")
}

func TestDropsReasoningParameter(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unsupported parameter: reasoning_effort":       "reasoning_effort",
		"invalid request: reasoning is not supported":   "reasoning",
		"400 thinking: unsupported value":               "thinking",
		"budget_tokens is not supported for this model": "budget_tokens",
		"unknown parameter verbosity":                   "verbosity",
	}
	for message, want := range cases {
		parameter, ok := dropsReasoningParameter(errors.New(message))
		require.True(t, ok, "should detect a drop in %q", message)
		require.Equal(t, want, parameter)
	}

	ignored := []string{
		"invalid api key",
		"rate limit reached",
		"the model returned malformed JSON",
		"reasoning_effort is set to high",
	}
	for _, message := range ignored {
		_, ok := dropsReasoningParameter(errors.New(message))
		require.False(t, ok, "should not detect a drop in %q", message)
	}

	_, ok := dropsReasoningParameter(nil)
	require.False(t, ok)

	// An auth failure mentioning a parameter is a credential problem, not a
	// parameter problem.
	_, ok = dropsReasoningParameter(&providerErrorStatus{
		StatusCode: http.StatusUnauthorized,
		Message:    "unauthorized: unsupported reasoning_effort",
	})
	require.False(t, ok)
}

func TestReasoningTruthTracksHonesty(t *testing.T) {
	t.Parallel()

	truth := newReasoningTruth()

	honored, observed := truth.honorsReasoning()
	require.False(t, honored)
	require.False(t, observed)

	// A response without reasoning while reasoning was sent does not count as
	// honoured, but it does count as an observation.
	truth.observeResponse(true, false)
	honored, observed = truth.honorsReasoning()
	require.False(t, honored)
	require.True(t, observed)

	truth.observeResponse(true, true)
	honored, observed = truth.honorsReasoning()
	require.True(t, honored)
	require.True(t, observed)

	// Dropped parameters are recorded and readable.
	_, dropped := truth.droppedParameter()
	require.False(t, dropped)
	truth.noteDropped("reasoning_effort")
	parameter, dropped := truth.droppedParameter()
	require.True(t, dropped)
	require.Equal(t, "reasoning_effort", parameter)
}

func TestReasoningTruthNilIsSafe(t *testing.T) {
	t.Parallel()

	var truth *reasoningTruth
	truth.noteDropped("reasoning")
	_, dropped := truth.droppedParameter()
	require.False(t, dropped)
	truth.observeResponse(true, true)
	honored, observed := truth.honorsReasoning()
	require.False(t, honored)
	require.False(t, observed)
}

func TestReasoningDroppedNote(t *testing.T) {
	t.Parallel()

	note := reasoningDroppedNote("reasoning_effort")
	require.Contains(t, note, "reasoning_effort")
	require.Contains(t, note, "refused by the provider")
	require.Contains(t, note, "Nothing about your task changed")
}

func TestNegatedSpans(t *testing.T) {
	t.Parallel()

	spans := negatedSpans("I did not change the file.")
	require.NotEmpty(t, spans)
	require.True(t, insideAny(11, spans))
	require.False(t, insideAny(0, spans))

	require.Empty(t, negatedSpans("I changed the file."))
}
