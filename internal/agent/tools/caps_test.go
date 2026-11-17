package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBashOutputFitsOneThousandTokens pins the per-call result ceiling. Output
// is the dominant recurring cost in a session: it enters the conversation once
// and is re-sent on every later request, so a large cap here is paid for many
// times over. The dropped middle still goes to a spill file named in the
// marker, so the information is not lost, only moved out of the prompt.
func TestBashOutputFitsOneThousandTokens(t *testing.T) {
	t.Parallel()

	require.LessOrEqual(t, MaxOutputLength/4, 1000,
		"one bash call must stay within about 1000 tokens")
}

// TestOversizedBashOutputKeepsAPointer checks that cutting the output still
// leaves the agent a way back to it, rather than silently discarding it.
func TestOversizedBashOutputKeepsAPointer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	output := strings.Repeat("a line of build output\n", 2000)
	got := TruncateOutput(output, dir)

	require.LessOrEqual(t, len(got), MaxOutputLength+400,
		"truncated output must fit the cap plus its marker")
	require.Contains(t, got, "truncated", "the marker must say what happened")
	require.Contains(t, got, ".log", "the marker must point at the full output")
	require.Contains(t, got, "a line of build output", "the head must survive")
}

// TestViewReadDefaultsAreBounded pins the read side: a default read must be a
// range the agent can act on, not a whole file re-sent on every request.
func TestViewReadDefaultsAreBounded(t *testing.T) {
	t.Parallel()

	require.LessOrEqual(t, DefaultReadLimit, 100,
		"a default read should be around a thousand tokens, not a whole file")
	require.LessOrEqual(t, MaxViewSize/1024, 64,
		"one read must not be able to return a quarter megabyte")
}
