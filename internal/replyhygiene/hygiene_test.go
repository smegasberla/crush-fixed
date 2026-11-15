package replyhygiene

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanLeavesGoodReplyAlone(t *testing.T) {
	t.Parallel()

	text := "The bug is in the retry loop.\n\n```go\nfor i := 0; i < 3; i++ {}\n```\n\nThat fixes it."
	result := Clean(text)
	require.Equal(t, text, result.Text)
	require.False(t, result.Changed())
}

func TestStripEnvelope(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"<|assistant|>\nthe answer\n<|end|>":                    "the answer",
		"<|start_header_id|>assistant<|end_header_id|>\nanswer": "answer",
		"[INST]\nthe answer":                                    "the answer",
		"<|im_start|>assistant\nthe answer\n<|im_end|>":         "the answer",
	}
	for input, want := range cases {
		result := Clean(input)
		require.Equal(t, want, result.Text, "input %q", input)
		require.True(t, result.Changed())
	}
}

func TestEnvelopeOnlyStripsWholeReply(t *testing.T) {
	t.Parallel()

	// A reply that mentions a template mid-text is content, not an envelope.
	text := "The template uses <|im_end|> as its stop token."
	result := Clean(text)
	require.Equal(t, text, result.Text)
}

func TestUnwrapFences(t *testing.T) {
	t.Parallel()

	result := Clean("```markdown\n# Heading\n\nSome text.\n```")
	require.Equal(t, "# Heading\n\nSome text.", result.Text)
	require.Contains(t, result.Applied, "fence")

	// A fence around part of the reply is content.
	partial := "Here:\n\n```go\nx := 1\n```\n\nDone."
	result = Clean(partial)
	require.Equal(t, partial, result.Text)
}

func TestStripParamDebris(t *testing.T) {
	t.Parallel()

	text := "<environment_details>\nfile tree\n</environment_details>\n\nThe answer is 5."
	result := Clean(text)
	require.Equal(t, "The answer is 5.", result.Text)
	require.Contains(t, result.Applied, "param_debris")
}

func TestCollapseRepeatedSentence(t *testing.T) {
	t.Parallel()

	sentence := "The validator rejects empty input because the schema requires a value."
	text := sentence + " " + sentence + " Then it returns."
	result := Clean(text)
	require.Equal(t, 1, strings.Count(result.Text, "requires a value"))
	require.Contains(t, result.Text, "Then it returns.")
}

func TestCollapseDoesNotTouchParaphrase(t *testing.T) {
	t.Parallel()

	text := "The cache stores the whole body for later reuse. The cache keeps the whole body for later reuse."
	result := Clean(text)
	require.Equal(t, text, result.Text)
}

func TestCollapseDoesNotTouchShortRepeats(t *testing.T) {
	t.Parallel()

	text := "No. No. No."
	result := Clean(text)
	require.Equal(t, text, result.Text, "short repeats are emphasis")
}

func TestStripImitatedToolCards(t *testing.T) {
	t.Parallel()

	text := "› run_command {\"command\":\"ls\"}\nThe directory has three files."
	result := Clean(text)
	require.NotContains(t, result.Text, "run_command")
	require.Contains(t, result.Text, "three files")

	// Prose that names a tool is content.
	prose := "Call the view tool to read the file."
	result = Clean(prose)
	require.Equal(t, prose, result.Text)
}

func TestStripImitatedToolCardsKeepsOriginalIfEmpty(t *testing.T) {
	t.Parallel()

	text := "› run_command {\"command\":\"ls\"}"
	result := Clean(text)
	require.Equal(t, text, result.Text, "a reply that is only a card is kept rather than emptied")
}

func TestTrimTrailingDebris(t *testing.T) {
	t.Parallel()

	text := "The answer is 5.\n\n---"
	result := Clean(text)
	require.Equal(t, "The answer is 5.", result.Text)
}

func TestCleanIsIdempotent(t *testing.T) {
	t.Parallel()

	text := "<|assistant|>\n```\nThe answer is 5.\n```\n---\n<|end|>"
	once := Clean(text)
	twice := Clean(once.Text)
	require.Equal(t, once.Text, twice.Text)
	require.False(t, twice.Changed())
}

func TestCleanEmpty(t *testing.T) {
	t.Parallel()

	result := Clean("")
	require.Empty(t, result.Text)
	require.False(t, result.Changed())
}

func TestAuditLeaks(t *testing.T) {
	t.Parallel()

	require.Empty(t, AuditLeaks("a clean reply"))
	leaks := AuditLeaks("answer <|im_end|> more </s>")
	require.Len(t, leaks, 2)
	require.Contains(t, leaks, "<|im_end|>")
	require.Contains(t, leaks, "</s>")
}

func TestStopSequences(t *testing.T) {
	t.Parallel()

	require.Contains(t, StopSequences, "<|im_end|>")
	require.Contains(t, StopSequences, "</s>")
	require.Contains(t, StopSequences, "<|end▁of▁sentence|>")
	require.Contains(t, StopSequences, "[INST]")
}

func TestResultChanged(t *testing.T) {
	t.Parallel()

	require.False(t, Result{}.Changed())
	require.True(t, Result{Applied: []string{"fence"}}.Changed())
}
