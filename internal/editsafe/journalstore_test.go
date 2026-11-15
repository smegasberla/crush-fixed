package editsafe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJournalStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store := NewJournalStore(t.TempDir())
	require.True(t, store.Enabled())

	entries := []JournalEntry{{
		Path:       "/repo/a.go",
		Before:     "before\n",
		After:      "after\n",
		BeforeHash: Hash([]byte("before\n")),
		AfterHash:  Hash([]byte("after\n")),
		At:         time.Unix(1700000000, 0).UTC(),
		Mode:       0o644,
	}}
	require.NoError(t, store.Save("session-1", entries))

	loaded := store.Load("session-1")
	require.Len(t, loaded, 1)
	require.Equal(t, "/repo/a.go", loaded[0].Path)
	require.Equal(t, "before\n", loaded[0].Before)
	require.Equal(t, "after\n", loaded[0].After)
	require.Equal(t, os.FileMode(0o644), loaded[0].Mode)
	require.True(t, time.Unix(1700000000, 0).Equal(loaded[0].At), "times compare across zones")

	sessions, err := store.List()
	require.NoError(t, err)
	require.Equal(t, []string{"session-1"}, sessions)
}

func TestJournalStoreMissingIsEmpty(t *testing.T) {
	t.Parallel()

	store := NewJournalStore(t.TempDir())
	require.Nil(t, store.Load("never-saved"))

	sessions, err := store.List()
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func TestJournalStoreCorruptIsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewJournalStore(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "journal-bad.json"), []byte("{not json"), 0o644))
	require.Nil(t, store.Load("bad"))
}

func TestJournalStoreDisabled(t *testing.T) {
	t.Parallel()

	store := NewJournalStore("")
	require.False(t, store.Enabled())
	require.NoError(t, store.Save("s", nil))
	require.Nil(t, store.Load("s"))
	require.NoError(t, store.Delete("s"))
	sessions, err := store.List()
	require.NoError(t, err)
	require.Empty(t, sessions)

	var nilStore *JournalStore
	require.False(t, nilStore.Enabled())
	require.NoError(t, nilStore.Save("s", nil))
	require.Nil(t, nilStore.Load("s"))
	require.NoError(t, nilStore.Delete("s"))
	sessions, err = nilStore.List()
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func TestJournalStoreRejectsEmptySession(t *testing.T) {
	t.Parallel()

	store := NewJournalStore(t.TempDir())
	require.Error(t, store.Save("", nil))
}

func TestJournalStoreDelete(t *testing.T) {
	t.Parallel()

	store := NewJournalStore(t.TempDir())
	require.NoError(t, store.Save("s", []JournalEntry{{Path: "/a.go"}}))
	require.NoError(t, store.Delete("s"))
	require.Nil(t, store.Load("s"))
	require.NoError(t, store.Delete("s"), "deleting a missing journal is not an error")
}

func TestJournalStoreLeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewJournalStore(dir)
	require.NoError(t, store.Save("s", []JournalEntry{{Path: "/a.go"}}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, ".journal-", entry.Name()[:min(len(entry.Name()), 9)])
	}
}

func TestSafeName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "abc-123_ok", safeName("abc-123_ok"))
	require.Equal(t, "a_b", safeName("a/b"))
	require.Equal(t, "a_b", safeName("a\\b"))
	require.Equal(t, "a__b", safeName("a..b")[:4])
	require.Equal(t, "session", safeName(""))
	require.Equal(t, "_____", safeName("/////"))
}

func TestJournalFromPersistedEntriesReverts(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("after\n"), 0o644))

	entries := []JournalEntry{{
		Path:      path,
		Before:    "before\n",
		After:     "after\n",
		AfterHash: Hash([]byte("after\n")),
	}}
	journal := NewJournalFromEntries(entries)
	require.Equal(t, 1, journal.Len())

	result, err := journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Restored)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "before\n", string(raw))
}
