package contextreduction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCollapseRepeatedLinesKeepsDistinctOutput is the quality contract: a run
// of identical lines is folded with a count, and every other line survives
// verbatim and in order. A failure, a stack frame or a summary must never be
// altered by this.
func TestCollapseRepeatedLinesKeepsDistinctOutput(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"FAIL github.com/x/y",
		"  --- FAIL: TestOne",
		"    same error",
		"    same error",
		"    same error",
		"    same error",
		"  --- FAIL: TestTwo",
		"FAIL",
	}, "\n")

	got, folded := CollapseRepeatedLines(input, 3)
	require.True(t, folded)

	require.Contains(t, got, "FAIL github.com/x/y", "the first distinct line survives")
	require.Contains(t, got, "--- FAIL: TestOne")
	require.Contains(t, got, "--- FAIL: TestTwo", "distinct lines after the run survive")
	require.Contains(t, got, "4 identical lines", "the run is counted, not hidden")
	require.Less(t, len(got), len(input))
}

// TestCollapseRepeatedLinesLeavesUniqueOutputAlone checks the transform is a
// no-op where there is nothing to fold.
func TestCollapseRepeatedLinesLeavesUniqueOutputAlone(t *testing.T) {
	t.Parallel()

	input := "line one\nline two\nline three"
	got, folded := CollapseRepeatedLines(input, 3)
	require.False(t, folded)
	require.Equal(t, input, got)
}

// TestCollapseRepeatedLinesIgnoresBlankLines checks blank lines are not folded,
// since a run of blanks carries no repetition to remove.
func TestCollapseRepeatedLinesIgnoresBlankLines(t *testing.T) {
	t.Parallel()

	input := "a\n\n\n\nb"
	got, folded := CollapseRepeatedLines(input, 3)
	require.False(t, folded)
	require.Equal(t, input, got)
}
