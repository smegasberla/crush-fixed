package editsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJournalRecordAndRead(t *testing.T) {
	t.Parallel()

	journal := NewJournal()
	require.Zero(t, journal.Len())

	journal.Record(JournalEntry{Path: "/repo/a.go", Before: "old", After: "new"})
	journal.Record(JournalEntry{Path: "/repo/b.go", Before: "", After: "created", Created: true})

	require.Equal(t, 2, journal.Len())
	entries := journal.Entries()
	require.Len(t, entries, 2)
	require.False(t, entries[0].At.IsZero(), "the entry is stamped with a time")
	require.Equal(t, []string{"/repo/a.go", "/repo/b.go"}, journal.Paths())
}

func TestJournalNilIsSafe(t *testing.T) {
	t.Parallel()

	var journal *Journal
	journal.Record(JournalEntry{Path: "/repo/a.go"})
	require.Zero(t, journal.Len())
	require.Nil(t, journal.Entries())
	require.Nil(t, journal.Paths())
	result, err := journal.Revert()
	require.NoError(t, err)
	require.Empty(t, result.Restored)
	require.Empty(t, result.Skipped)
}

func TestJournalRevertRestoresContent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("after\n"), 0o644))

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:       path,
		Before:     "before\n",
		After:      "after\n",
		AfterHash:  Hash([]byte("after\n")),
		BeforeHash: Hash([]byte("before\n")),
	})

	result, err := journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Restored)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "before\n", string(raw))
}

func TestJournalRevertRemovesCreatedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "new.go")
	require.NoError(t, os.WriteFile(path, []byte("created\n"), 0o644))

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:      path,
		After:     "created\n",
		AfterHash: Hash([]byte("created\n")),
		Created:   true,
	})

	result, err := journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Removed)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr))
}

func TestJournalRevertSkipsChangedFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("changed by someone else\n"), 0o644))

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:      path,
		Before:    "before\n",
		After:     "after\n",
		AfterHash: Hash([]byte("after\n")),
	})

	result, err := journal.Revert()
	require.NoError(t, err)
	require.Empty(t, result.Restored)
	require.Contains(t, result.Skipped[path], "changed after this turn")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "changed by someone else\n", string(raw), "an unrelated change is never overwritten")
}

func TestJournalRevertNewestFirst(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("v3\n"), 0o644))

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path: path, Before: "v1\n", After: "v2\n", AfterHash: Hash([]byte("v2\n")),
	})
	journal.Record(JournalEntry{
		Path: path, Before: "v2\n", After: "v3\n", AfterHash: Hash([]byte("v3\n")),
	})

	result, err := journal.Revert()
	require.NoError(t, err)
	require.Len(t, result.Restored, 2)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "v1\n", string(raw), "reverting walks back to the original")
}

func TestJournalRevertSkipsAlreadyAbsentCreated(t *testing.T) {
	t.Parallel()

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:      filepath.Join(t.TempDir(), "gone.go"),
		Created:   true,
		AfterHash: Hash([]byte("x")),
	})
	result, err := journal.Revert()
	require.NoError(t, err)
	require.Empty(t, result.Removed)
	require.NotEmpty(t, result.Skipped)
}

func TestJournalRevertRecreatesDeletedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "deleted.go")
	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:    path,
		Before:  "restored content\n",
		After:   "",
		Removed: true,
	})
	// The file does not exist: the turn deleted it.
	result, err := journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Recreated)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "restored content\n", string(raw))
}

func TestJournalRevertRestoresMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "script.sh")
	require.NoError(t, os.WriteFile(path, []byte("echo after\n"), 0o600))

	journal := NewJournal()
	journal.Record(JournalEntry{
		Path:      path,
		Before:    "echo before\n",
		After:     "echo after\n",
		AfterHash: Hash([]byte("echo after\n")),
		Mode:      0o755,
	})
	_, err := journal.Revert()
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestJournalRevertLeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("after\n"), 0o644))

	journal := NewJournal()
	journal.Record(JournalEntry{Path: path, Before: "before\n", After: "after\n", AfterHash: Hash([]byte("after\n"))})
	_, err := journal.Revert()
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".edit-"), "leftover temp file %s", entry.Name())
	}
}

func TestJournalEndToEndWithTransaction(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "alpha\nbeta\n")
	journal := NewJournal()

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	tx, err := Load(path)
	require.NoError(t, err)
	tx.Add(Op{OldText: "beta", NewText: "BETA"})
	result, err := tx.Commit()
	require.NoError(t, err)

	journal.Record(JournalEntry{
		Path:       path,
		Before:     string(before),
		After:      result.NewContent,
		BeforeHash: Hash(before),
		AfterHash:  result.Hash,
	})
	require.Equal(t, "alpha\nBETA\n", readFixture(t, path))

	revertResult, err := journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, revertResult.Restored)
	require.Equal(t, "alpha\nbeta\n", readFixture(t, path))
}
