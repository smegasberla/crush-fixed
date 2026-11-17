package contextreduction

import (
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// toolCall builds an assistant message that requested a tool.
func toolCall(id, name, input string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.ToolCallPart{ToolCallID: id, ToolName: name, Input: input},
		},
	}
}

// toolResult builds a tool message carrying a text result.
func toolResult(id, text string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{
				ToolCallID: id,
				Output:     fantasy.ToolResultOutputContentText{Text: text},
			},
		},
	}
}

// errorResult builds a tool message carrying an error result.
func errorResult(id, text string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{
				ToolCallID: id,
				Output:     fantasy.ToolResultOutputContentError{Error: errors.New(text)},
			},
		},
	}
}

func userText(text string) fantasy.Message { return fantasy.NewUserMessage(text) }

// resultTextOf returns the text payload of the first tool result in msg.
func resultTextOf(t *testing.T, msg fantasy.Message) string {
	t.Helper()
	for _, part := range msg.Content {
		if tr, ok := part.(fantasy.ToolResultPart); ok {
			if text, ok := tr.Output.(fantasy.ToolResultOutputContentText); ok {
				return text.Text
			}
		}
	}
	t.Fatal("no text tool result in message")
	return ""
}

// errorTextOf returns the text payload of the first error tool result in msg.
func errorTextOf(t *testing.T, msg fantasy.Message) string {
	t.Helper()
	for _, part := range msg.Content {
		if tr, ok := part.(fantasy.ToolResultPart); ok {
			if e, ok := tr.Output.(fantasy.ToolResultOutputContentError); ok {
				return e.Error.Error()
			}
		}
	}
	t.Fatal("no error tool result in message")
	return ""
}

func TestDigestToolResultsStubsAgedBodies(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("package main\n", 400) + "tail marker\n"
	messages := []fantasy.Message{
		userText("start"),
		toolCall("c1", "bash", `{"command":"cat big.go"}`),
		toolResult("c1", big),
		userText("continue"),
		userText("more"),
	}

	out, stats := DigestToolResults(messages, DigestOptions{MinTokens: 100})
	require.Equal(t, 1, stats.Digested)
	require.Positive(t, stats.Saved())

	stubbed := resultTextOf(t, out[2])
	require.Contains(t, stubbed, "digested to fit the context window")
	require.Contains(t, stubbed, "sha256:")
	require.Contains(t, stubbed, "tail marker", "the tail survives so the model can recognise the end")
	require.Less(t, len(stubbed), len(big))

	// The input is never mutated.
	require.Equal(t, big, resultTextOf(t, messages[2]))
}

func TestDigestToolResultsProtectsErrors(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("error line\n", 200)
	messages := []fantasy.Message{
		toolCall("c1", "bash", `{}`),
		errorResult("c1", body),
		userText("a"),
		userText("b"),
	}
	out, stats := DigestToolResults(messages, DigestOptions{MinTokens: 10, ProtectErrors: true})
	require.Zero(t, stats.Digested)
	require.Equal(t, body, errorTextOf(t, out[1]))

	// An error payload keeps its exact wording even with protection off: a
	// stub cannot express it, and claiming otherwise would be a lie.
	out, stats = DigestToolResults(messages, DigestOptions{MinTokens: 10, ProtectErrors: false})
	require.Zero(t, stats.Digested)
	require.Equal(t, body, errorTextOf(t, out[1]))
}

func TestDigestToolResultsKeepsRecentMessages(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 4000)
	messages := []fantasy.Message{
		toolCall("c1", "bash", `{}`),
		toolResult("c1", big),
		userText("last"),
	}
	// KeepRecentMessages 2 means only the first message is aged.
	out, stats := DigestToolResults(messages, DigestOptions{MinTokens: 10, KeepRecentMessages: 2})
	require.Zero(t, stats.Digested)
	require.Equal(t, big, resultTextOf(t, out[1]))
}

func TestDigestToolResultsDeduplicatesRepeatedBodies(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("same output line\n", 200)
	messages := []fantasy.Message{
		toolCall("c1", "bash", `{}`),
		toolResult("c1", body),
		userText("between"),
		toolCall("c2", "bash", `{}`),
		toolResult("c2", body),
		userText("after"),
		userText("tail"),
	}
	// The first copy stays; the later duplicate is stubbed. Keeping the
	// earliest byte-for-byte is what lets a provider reuse the cached prefix.
	out, stats := DigestToolResults(messages, DigestOptions{MinTokens: 10, KeepRecentMessages: 2})
	require.Equal(t, 1, stats.Deduplicated)

	first := resultTextOf(t, out[1])
	require.NotContains(t, first, "already in this conversation earlier",
		"the first occurrence must not be replaced by the duplicate stub")
	require.Contains(t, resultTextOf(t, out[4]), "already in this conversation earlier")
}

func TestDigestToolResultsPerToolCaps(t *testing.T) {
	t.Parallel()

	listing := strings.Repeat("listing line\n", 100)
	buildLog := strings.Repeat("build output\n", 100)
	messages := []fantasy.Message{
		toolCall("c1", "ls", `{}`),
		toolResult("c1", listing),
		toolCall("c2", "bash", `{}`),
		toolResult("c2", buildLog),
		userText("a"),
		userText("b"),
	}
	opts := DigestOptions{MinTokens: 1000, ToolCaps: map[string]int{"ls": 10}}
	_, stats := DigestToolResults(messages, opts)
	// Only the ls result crosses its (smaller) cap; the bash one is under
	// the base cap and under the recent-window protection anyway.
	require.Equal(t, 1, stats.Digested)
}

func TestDigestToolResultsLeavesCleanInputAlone(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{userText("hi"), toolCall("c1", "ls", `{}`), toolResult("c1", "a.go\nb.go")}
	out, stats := DigestToolResults(messages, DigestOptions{})
	require.Zero(t, stats.Digested)
	require.Equal(t, messages, out)
}

func TestSupersededReadStubs(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("func filler() {}\n", 200)
	messages := []fantasy.Message{
		toolCall("c1", "view", `{"file_path":"internal/a.go"}`),
		toolResult("c1", "internal/a.go\n"+body),
		userText("change it"),
		toolCall("c2", "view", `{"file_path":"internal/a.go"}`),
		toolResult("c2", "internal/a.go\n"+body),
		userText("done"),
	}
	out, stats := SupersededReadStubs(messages, PointerOptions{MinTokens: 10, KeepRecentMessages: 2})
	require.Equal(t, 1, stats.Stubbed)

	superseded := resultTextOf(t, out[1])
	require.Contains(t, superseded, "superseded")
	require.Contains(t, superseded, "internal/a.go")
	// The newest reading keeps its body.
	require.Contains(t, resultTextOf(t, out[4]), body[:20])
}

func TestSupersededReadStubsIgnoresNonReadTools(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("output\n", 300)
	messages := []fantasy.Message{
		toolCall("c1", "bash", `{}`),
		toolResult("c1", "internal/a.go\n"+body),
		userText("x"),
		toolCall("c2", "bash", `{}`),
		toolResult("c2", "internal/a.go\n"+body),
		userText("y"),
	}
	_, stats := SupersededReadStubs(messages, PointerOptions{MinTokens: 10, KeepRecentMessages: 1})
	require.Zero(t, stats.Stubbed, "only file-reading tools are superseded")
}

func TestSupersededReadStubsKeepsNewestReading(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("line\n", 300)
	messages := []fantasy.Message{
		toolCall("c1", "view", `{}`),
		toolResult("c1", "only.go\n"+body),
		userText("x"),
	}
	_, stats := SupersededReadStubs(messages, PointerOptions{MinTokens: 10, KeepRecentMessages: 2})
	require.Zero(t, stats.Stubbed)
}

func TestSummarizeBudget(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		userText(strings.Repeat("u", 400)),
		toolCall("c1", "bash", strings.Repeat("i", 400)),
		toolResult("c1", strings.Repeat("r", 400)),
	}
	budget := Summarize(strings.Repeat("s", 400), messages)

	// 400 bytes each -> 100 tokens, plus the 1-token tool name on the call.
	require.Equal(t, 100, budget.System)
	require.Equal(t, 301, budget.Messages)
	require.Equal(t, 100, budget.ToolResults)
	require.Equal(t, 101, budget.ToolInputs)
	require.Equal(t, 401, budget.Total())
	require.Len(t, budget.PerMessage, 3)
	require.True(t, budget.Counted)
}

func TestSummarizeReasoningAndFiles(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.ReasoningPart{Text: strings.Repeat("t", 80)},
			fantasy.FilePart{Filename: "a.png", MediaType: "image/png"},
		},
	}}
	budget := Summarize("", messages)
	require.Equal(t, 20, budget.Reasoning)
	require.Equal(t, 1500, budget.Files)
	require.Equal(t, 1520, budget.Messages)
	require.Equal(t, 80, budget.PerMessage[0].ReasoningChars)
}
