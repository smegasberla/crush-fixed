package contextreduction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollapseRepeatedProseRemovesLosslessRepeat(t *testing.T) {
	t.Parallel()

	text := "The validator rejects empty input because the schema requires a value. " +
		"The validator rejects empty input because the schema requires a value. " +
		"The patch is complete."
	result := CollapseRepeatedProse(text)

	require.Equal(t, 1, result.RemovedSentences)
	require.Positive(t, result.RemovedRunes)
	require.Equal(t, 1, strings.Count(result.Text, "requires a value"))
	require.Contains(t, result.Text, "The patch is complete.")
}

func TestCollapseRepeatedProseKeepsParaphrase(t *testing.T) {
	t.Parallel()

	// The second sentence introduces "keeps", so removing it would drop
	// information the model chose to convey.
	text := "The cache stores the whole body for later reuse. " +
		"The cache keeps the whole body for later reuse."
	result := CollapseRepeatedProse(text)
	require.Zero(t, result.RemovedSentences)
	require.Equal(t, text, result.Text)
}

func TestCollapseRepeatedProseKeepsEmphasis(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"No. No. No.", "Yes. Yes.", "Very very very good."} {
		result := CollapseRepeatedProse(text)
		require.Zero(t, result.RemovedSentences, "short repeats are emphasis, not ramble: %q", text)
		require.Equal(t, text, result.Text)
	}
}

func TestCollapseRepeatedProseIsIdempotent(t *testing.T) {
	t.Parallel()

	text := "The build fails on the missing import. The build fails on the missing import. Fix the import in main.go."
	once := CollapseRepeatedProse(text)
	twice := CollapseRepeatedProse(once.Text)
	require.Equal(t, once.Text, twice.Text)
	require.Zero(t, twice.RemovedSentences)
}

func TestCollapseRepeatedProseNoOpOnCleanText(t *testing.T) {
	t.Parallel()

	text := "First install the dependency. Then run the migration. Finally start the server."
	result := CollapseRepeatedProse(text)
	require.Zero(t, result.RemovedSentences)
	require.Equal(t, text, result.Text)
}

func TestCollapseRepeatedProseEmptyAndSingleSentence(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", CollapseRepeatedProse("").Text)
	require.Equal(t, "One sentence only.", CollapseRepeatedProse("One sentence only.").Text)
}

func TestCollapseRepeatedProseDoesNotSplitAbbreviations(t *testing.T) {
	t.Parallel()

	text := "The U.S. market grew. The U.S. market grew. That is all."
	result := CollapseRepeatedProse(text)
	require.Equal(t, 1, result.RemovedSentences, "the abbreviation must not be split into its own sentence")
}

func TestCollapseRepeatedProseCollapsesSentenceWithNewWordOnlyStopWords(t *testing.T) {
	t.Parallel()

	// Function words only: the repeat carries no new content.
	text := "The deploy step rebuilds the binary from the source tree. " +
		"A deploy step rebuilds the binary from the source tree. " +
		"Nothing else changed."
	result := CollapseRepeatedProse(text)
	require.Equal(t, 1, result.RemovedSentences)
	require.Contains(t, result.Text, "Nothing else changed.")
}

func TestSplitSentences(t *testing.T) {
	t.Parallel()

	got := splitSentences("One. Two! Three? Four")
	require.Equal(t, []string{"One.", " Two!", " Three?", " Four"}, got)

	// CJK terminators count.
	got = splitSentences("完了。次！")
	require.Len(t, got, 2)

	// A period inside an abbreviation does not split.
	got = splitSentences("Use the U.S. dollar")
	require.Len(t, got, 1)
}

func TestJoinSentencesRestoresSeparators(t *testing.T) {
	t.Parallel()

	got := joinSentences([]string{"One.", "Two.", "Three"})
	require.Equal(t, "One. Two. Three", got)
	require.Equal(t, "", joinSentences(nil))
}

func TestContentWords(t *testing.T) {
	t.Parallel()

	words := contentWords("The Validator REJECTS the empty input, and the schema.")
	require.Contains(t, words, "validator")
	require.Contains(t, words, "rejects")
	require.Contains(t, words, "schema")
	require.NotContains(t, words, "the")
	require.NotContains(t, words, "and")
	require.NotContains(t, words, "it")
}
