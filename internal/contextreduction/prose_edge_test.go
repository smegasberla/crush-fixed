package contextreduction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollapseRepeatedProseThreeTimes(t *testing.T) {
	t.Parallel()

	sentence := "The migration drops the legacy table and rebuilds the index. "
	text := sentence + sentence + sentence + "Then commit."
	result := CollapseRepeatedProse(text)
	require.Equal(t, 2, result.RemovedSentences)
	require.Equal(t, 1, strings.Count(result.Text, "rebuilds the index"))
}

func TestCollapseRepeatedProsePreservesInteriorSpacing(t *testing.T) {
	t.Parallel()

	// Identical repeats collapse; a paraphrase with a new subject word
	// ("Beta") is a different sentence and stays.
	text := "Alpha builds the thing.\nAlpha builds the thing.\nAlpha builds the thing.\nDelta stops."
	result := CollapseRepeatedProse(text)
	require.Equal(t, 2, result.RemovedSentences)
	require.Contains(t, result.Text, "Alpha builds the thing.")
	require.Contains(t, result.Text, "Delta stops.")

	paraphrase := "Alpha builds the thing.\nBeta builds the thing."
	result = CollapseRepeatedProse(paraphrase)
	require.Zero(t, result.RemovedSentences)
}

func TestCollapseRepeatedProseLeavesCodeBlocksAlone(t *testing.T) {
	t.Parallel()

	// Two identical code lines are not prose; the function works on
	// sentences and a fence-free body has no terminators to collapse.
	text := "```go\nfoo()\nfoo()\n```"
	result := CollapseRepeatedProse(text)
	require.Equal(t, text, result.Text)
}

func TestNeedsSpace(t *testing.T) {
	t.Parallel()

	require.True(t, needsSpace("One.", "Two"))
	require.False(t, needsSpace("One. ", "Two"))
	require.False(t, needsSpace("", "Two"))
	require.False(t, needsSpace("One.", ""))
	require.False(t, needsSpace("One.", " Two"))
}
