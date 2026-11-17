package contextreduction

import (
	"testing"

	"charm.land/fantasy"

	"github.com/stretchr/testify/require"
)

// TestContinuityNoteCarriesFilePointers is the quality contract for a fold: the
// next assistant must know which files were read and changed, so it does not
// re-open them. Only pointers are carried, never file bodies.
func TestContinuityNoteCarriesFilePointers(t *testing.T) {
	t.Parallel()

	text := "read internal/agent/agent.go:100 and internal/agent/tools/bash.go:40"
	msgs := []fantasy.Message{
		fantasy.NewUserMessage(text),
		fantasy.NewUserMessage("fix the retry loop"),
	}
	note := BuildContinuityNote(msgs)
	rendered := note.Render()

	require.Contains(t, rendered, "internal/agent/agent.go")
	require.Contains(t, rendered, "internal/agent/tools/bash.go")
	require.Contains(t, rendered, "Request in progress: read internal/agent/agent.go:100")
	require.Contains(t, rendered, "Files already read:",
		"the note carries pointers to what was read")
}

// TestContinuityNoteIsStable checks the note is deterministic for a transcript,
// which matters both for testing and for a stable request prefix.
func TestContinuityNoteIsStable(t *testing.T) {
	t.Parallel()

	msgs := []fantasy.Message{
		fantasy.NewUserMessage("see z.go then a.go then z.go"),
	}
	first := BuildContinuityNote(msgs).Render()
	second := BuildContinuityNote(msgs).Render()
	require.Equal(t, first, second)
	require.Equal(t, "Request in progress: see z.go then a.go then z.go\nFiles already read: a.go, z.go", first)
}

// TestContinuityNoteEmptyOnNoFacts checks a fold with nothing addressable does
// not grow the prompt with empty sections.
func TestContinuityNoteEmptyOnNoFacts(t *testing.T) {
	t.Parallel()

	require.True(t, BuildContinuityNote(nil).Empty())
	require.Equal(t, "Request in progress: hello",
		BuildContinuityNote([]fantasy.Message{fantasy.NewUserMessage("hello")}).Render())
}
