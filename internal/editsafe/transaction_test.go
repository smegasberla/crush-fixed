package editsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFixture writes content to a temp file and returns its path.
func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// readFixture returns the file's current content.
func readFixture(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func TestLoadRefusesBinaryAndNonUTF8(t *testing.T) {
	t.Parallel()

	_, err := LoadBytes("bin", []byte("a\x00b"))
	require.True(t, IsCode(err, CodeBinaryFile))

	_, err = LoadBytes("bad", []byte("a\xffb"))
	require.True(t, IsCode(err, CodeEncodingDamage))

	_, err = Load("does-not-exist")
	require.Error(t, err)
	require.False(t, IsCode(err, CodeBinaryFile))
}

func TestCommitReplacesText(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\nbeta\ngamma\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "beta", NewText: "BETA"})

	result, err := tx.Commit()
	require.NoError(t, err)
	require.Contains(t, result.Codes, CodeSuccess)
	require.Equal(t, 1, result.Applied)
	require.Equal(t, "alpha\nBETA\ngamma\n", readFixture(t, path))
	require.Equal(t, Hash([]byte(readFixture(t, path))), result.Hash)
	require.Equal(t, result.Hash, result.ReadBackHash)
}

func TestCommitRefusesMultiMatch(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "same\nother\nsame\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "same", NewText: "diff"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeMultiMatch))
	require.Contains(t, err.Error(), "matches 2 places")
	require.Contains(t, err.Error(), "add surrounding context")
	require.Equal(t, "same\nother\nsame\n", readFixture(t, path), "nothing was written")
}

func TestCommitReplaceAllIsExplicit(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "same\nother\nsame\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "same", NewText: "diff", ReplaceAll: true})

	result, err := tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "diff\nother\ndiff\n", readFixture(t, path))
	require.Positive(t, result.Additions)
}

func TestCommitReturnsNearMissHint(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "func main() {\n    return\n}\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "not present anywhere", NewText: "x"})
	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeAnchorGone))
	require.Contains(t, err.Error(), "old_text not found")
}

func TestNearMissHintNamesCloseLine(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\n  beta gamma\nomega\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "beta  gamma", NewText: "x"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeAnchorGone))
	require.Contains(t, err.Error(), "line 2 has similar text")
}

func TestCommitAddressesByLineWithExpect(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 2, Expect: "two", NewText: "TWO"})

	result, err := tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", readFixture(t, path))
	require.Equal(t, 1, result.Applied)
}

func TestCommitRefusesMissingExpect(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 1, NewText: "x"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeMissingAnchor))
	require.Contains(t, err.Error(), "needs an expect token")
}

func TestCommitRelocatesStaleAnchor(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\n")
	tx, err := Load(path)
	require.NoError(t, err)
	// The address is wrong: "three" is on line 3.
	tx.Add(Op{Line: 1, Expect: "three", NewText: "THREE"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeAnchorGone))
	require.Contains(t, err.Error(), "relocated: addressed line 1, expect token is line 3")
	require.Contains(t, err.Error(), "re-send this op with line 3")
	require.Equal(t, "one\ntwo\nthree\n", readFixture(t, path))
}

func TestCommitLineOutsideFile(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 9, Expect: "x", NewText: "y"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeAnchorGone))
	require.Contains(t, err.Error(), "outside the file")
}

func TestCommitDeletesByAddress(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 2, Expect: "two", Delete: true})

	_, err = tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "one\nthree\n", readFixture(t, path))
}

func TestCommitDeletesARange(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\nfour\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 2, EndLine: 3, Expect: "two", Delete: true})

	_, err = tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "one\nfour\n", readFixture(t, path))
}

func TestCommitRefusesDeleteWithText(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 1, Expect: "one", Delete: true, NewText: "replacement"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeRejected))
	require.Contains(t, err.Error(), "carries replacement text")
}

func TestCommitRefusesNoTarget(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeNoTargetText))
}

func TestCommitRefusesBothAddressModes(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{Line: 1, Expect: "one", OldText: "one", NewText: "x"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeRejected))
	require.Contains(t, err.Error(), "not both")
}

func TestCommitRefusesDuplicateOpIDs(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(
		Op{ID: "a", Line: 1, Expect: "one", NewText: "1"},
		Op{ID: "a", Line: 2, Expect: "two", NewText: "2"},
	)

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeOpIDConflict))
}

func TestCommitRefusesOverlappingRegions(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\nfour\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(
		Op{ID: "first", Line: 2, EndLine: 3, Expect: "two", NewText: "x"},
		Op{ID: "second", Line: 3, EndLine: 4, Expect: "three", NewText: "y"},
	)

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeRangesOverlap))
	require.Contains(t, err.Error(), "first")
	require.Contains(t, err.Error(), "second")
	require.Equal(t, "one\ntwo\nthree\nfour\n", readFixture(t, path))
}

func TestCommitAppliesMultiRegionAtomic(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\nfour\nfive\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(
		Op{Line: 5, Expect: "five", NewText: "FIVE"},
		Op{Line: 1, Expect: "one", NewText: "ONE"},
		Op{Line: 3, Expect: "three", Delete: true},
	)

	result, err := tx.Commit()
	require.NoError(t, err)
	require.Equal(t, 3, result.Applied)
	require.Equal(t, "ONE\ntwo\nfour\nFIVE\n", readFixture(t, path))
}

func TestCommitIsAllOrNothing(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(
		Op{Line: 1, Expect: "one", NewText: "ONE"},
		Op{Line: 3, Expect: "wrong", NewText: "THREE"},
	)

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeAnchorGone))
	require.Equal(t, "one\ntwo\nthree\n", readFixture(t, path), "a failing op aborts the whole batch")
}

func TestCommitRefusesNoOp(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "two", NewText: "two"})

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeNoOpResolved))
	require.Contains(t, err.Error(), "byte-identical")
}

func TestCommitRequiresOps(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\n")
	tx, err := Load(path)
	require.NoError(t, err)

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeNoOpResolved))
}

func TestCommitPreservesCRLF(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\r\nbeta\r\ngamma\r\n")
	tx, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, LineEndingCRLF, tx.Conventions().LineEnding)
	tx.Add(Op{OldText: "beta", NewText: "BETA"})

	_, err = tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "alpha\r\nBETA\r\ngamma\r\n", readFixture(t, path))
}

func TestCommitPreservesBOM(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "\xef\xbb\xbfalpha\nbeta\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "beta", NewText: "BETA"})

	_, err = tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "\xef\xbb\xbfalpha\nBETA\n", readFixture(t, path))
}

func TestCommitPreservesMissingFinalNewline(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\nbeta")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "beta", NewText: "BETA"})

	_, err = tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "alpha\nBETA", readFixture(t, path))
}

func TestCommitRestoresFinalNewlineAndSaysSo(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\nbeta\n")
	tx, err := Load(path)
	require.NoError(t, err)
	// Replacing through the end of the file would drop the trailing newline.
	tx.Add(Op{OldText: "alpha\nbeta\n", NewText: "gamma"})

	result, err := tx.Commit()
	require.NoError(t, err)
	require.Equal(t, "gamma\n", readFixture(t, path))
	require.Contains(t, result.Codes, CodeConventionRestored)
}

func TestCommitPreservesFileMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "script.sh")
	require.NoError(t, os.WriteFile(path, []byte("echo one\n"), 0o755))
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "one", NewText: "two"})

	_, err = tx.Commit()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestCommitLeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\n"), 0o644))
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "b", NewText: "B"})
	_, err = tx.Commit()
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".edit-"), "leftover temp file %s", entry.Name())
	}
}

func TestCommitMixedBatchRefusesLineShift(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "one\ntwo\nthree\n")
	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(
		Op{OldText: "one\ntwo", NewText: "one"},
		Op{Line: 3, Expect: "three", NewText: "THREE"},
	)

	_, err = tx.Commit()
	require.True(t, IsCode(err, CodeTargetRegionChanged))
	require.Contains(t, err.Error(), "separate batches")
}

func TestResultString(t *testing.T) {
	t.Parallel()

	result := Result{Path: "a.go", Applied: 2, Additions: 3, Removals: 1, Hash: strings.Repeat("a", 64)}
	text := result.String()
	require.Contains(t, text, "a.go")
	require.Contains(t, text, "+3/-1")
	require.Contains(t, text, "sha256:aaaaaaaaaaaa")
	require.NotContains(t, text, strings.Repeat("a", 20), "the hash is shortened for display")
}

func TestResultHas(t *testing.T) {
	t.Parallel()

	result := Result{Codes: []Code{CodeSuccess, CodeConventionRestored}}
	require.True(t, result.Has(CodeSuccess))
	require.True(t, result.Has(CodeConventionRestored))
	require.False(t, result.Has(CodeRejected))
}

func TestErrorFormatting(t *testing.T) {
	t.Parallel()

	err := newError(CodeMultiMatch, "a.go", "matched twice", "narrow it", "or use replace_all")
	text := err.Error()
	require.Contains(t, text, "multi_match")
	require.Contains(t, text, "a.go")
	require.Contains(t, text, "matched twice")
	require.Contains(t, text, "narrow it")

	plain := newError(CodeRejected, "", "bare")
	require.Equal(t, "EDIT_REJECTED — bare", plain.Error())
}

func TestIsCodeIgnoresOtherErrors(t *testing.T) {
	t.Parallel()

	require.False(t, IsCode(nil, CodeRejected))
	require.False(t, IsCode(os.ErrNotExist, CodeRejected))
	// A wrapped refusal is still recognised.
	wrapped := fmt.Errorf("wrap: %w", newError(CodeAnchorGone, "a.go", "gone"))
	require.True(t, IsCode(wrapped, CodeAnchorGone))
}
