package contextreduction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDigestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewDigestStore(dir)
	require.True(t, store.Enabled())
	require.Equal(t, dir, store.Dir())

	chain := ChainHash("", "absorbed prefix")
	_, ok := store.Load(chain)
	require.False(t, ok, "an absent memo is a miss, not an error")

	require.NoError(t, store.Store(DigestMemo{Chain: chain, Absorbed: 12, Brief: "## Continuation Brief\nall good"}))
	memo, ok := store.Load(chain)
	require.True(t, ok)
	require.Equal(t, 12, memo.Absorbed)
	require.Contains(t, memo.Brief, "Continuation Brief")
	require.NotEmpty(t, memo.UpdatedAt)

	chains, err := store.Chains()
	require.NoError(t, err)
	require.Equal(t, []string{chain}, chains)
}

func TestDigestStoreRejectsEmptyChain(t *testing.T) {
	t.Parallel()

	store := NewDigestStore(t.TempDir())
	require.Error(t, store.Store(DigestMemo{Brief: "x"}))
}

func TestDigestStoreDisabled(t *testing.T) {
	t.Parallel()

	store := NewDigestStore("")
	require.False(t, store.Enabled())
	require.NoError(t, store.Store(DigestMemo{Chain: "abc", Brief: "x"}))
	_, ok := store.Load("abc")
	require.False(t, ok)
	require.NoError(t, store.Delete("abc"))
	chains, err := store.Chains()
	require.NoError(t, err)
	require.Empty(t, chains)

	var nilStore *DigestStore
	require.False(t, nilStore.Enabled())
	require.Empty(t, nilStore.Dir())
	_, ok = nilStore.Load("abc")
	require.False(t, ok)
	require.NoError(t, nilStore.Store(DigestMemo{}))
}

func TestDigestStoreTreatsCorruptMemoAsMiss(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewDigestStore(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644))
	_, ok := store.Load("broken")
	require.False(t, ok)

	// A valid JSON document with no brief is also a miss.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.json"), []byte(`{"chain":"empty"}`), 0o644))
	_, ok = store.Load("empty")
	require.False(t, ok)
}

func TestDigestStoreDelete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewDigestStore(dir)
	chain := ChainHash("", "x")
	require.NoError(t, store.Store(DigestMemo{Chain: chain, Brief: "b"}))
	require.NoError(t, store.Delete(chain))
	_, ok := store.Load(chain)
	require.False(t, ok)
	require.NoError(t, store.Delete(chain), "deleting a missing memo is not an error")

	chains, err := store.Chains()
	require.NoError(t, err)
	require.Empty(t, chains)
}

func TestDigestStoreChainsSkipsTemporaryFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewDigestStore(dir)
	require.NoError(t, store.Store(DigestMemo{Chain: "keep", Brief: "b"}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".memo-123.tmp"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644))

	chains, err := store.Chains()
	require.NoError(t, err)
	require.Equal(t, []string{"keep"}, chains)

	// A missing directory is not an error.
	missing := NewDigestStore(filepath.Join(dir, "does-not-exist"))
	chains, err = missing.Chains()
	require.NoError(t, err)
	require.Empty(t, chains)
}

func TestDigestStoreCreatesDirectory(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "digests")
	store := NewDigestStore(dir)
	require.NoError(t, store.Store(DigestMemo{Chain: "c", Brief: "b"}))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestDigestStoreOverwritesAtomically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewDigestStore(dir)
	require.NoError(t, store.Store(DigestMemo{Chain: "c", Brief: "first"}))
	require.NoError(t, store.Store(DigestMemo{Chain: "c", Brief: "second"}))

	memo, ok := store.Load("c")
	require.True(t, ok)
	require.Equal(t, "second", memo.Brief)

	// No temporary files survive a successful write.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".memo-"), "leftover temp file %s", entry.Name())
	}
}

func TestMergeBrief(t *testing.T) {
	t.Parallel()

	require.Equal(t, "delta", MergeBrief("", "delta"))
	require.Equal(t, "previous", MergeBrief("previous", ""))
	require.Equal(t, "", MergeBrief("  ", ""))

	merged := MergeBrief("## Decisions\nuse sqlite", "## New\nadd FTS5")
	require.True(t, strings.HasPrefix(merged, "## Decisions\nuse sqlite"))
	require.Contains(t, merged, "### Update")
	require.Contains(t, merged, "## New\nadd FTS5")
}
