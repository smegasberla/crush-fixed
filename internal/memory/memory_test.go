package memory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "memories.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestSaveAndList(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()

	saved, err := store.Save(ctx, Entry{
		ProjectRoot: "/repo",
		Topic:       "build",
		Trigger:     "running the build",
		Lesson:      "use make BUILD_DIR=build, not plain make",
	})
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)
	require.Positive(t, saved.CreatedAt)

	entries, err := store.List(ctx, "/repo")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "build", entries[0].Topic)
	require.Contains(t, entries[0].Lesson, "BUILD_DIR")
}

func TestSaveDeduplicates(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	first, err := store.Save(ctx, Entry{ProjectRoot: "/repo", Topic: "build", Lesson: "same lesson"})
	require.NoError(t, err)

	second, err := store.Save(ctx, Entry{ProjectRoot: "/repo", Topic: "build", Lesson: "same lesson", Trigger: "new trigger"})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "the same lesson is one entry")

	entries, err := store.List(ctx, "/repo")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "new trigger", entries[0].Trigger)
}

func TestSaveValidates(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()

	_, err := store.Save(ctx, Entry{Lesson: "no root"})
	require.Error(t, err)

	_, err = store.Save(ctx, Entry{ProjectRoot: "/repo"})
	require.Error(t, err, "a memory needs a lesson")
}

func TestSaveDefaultsTopic(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	saved, err := store.Save(context.Background(), Entry{ProjectRoot: "/repo", Lesson: "note"})
	require.NoError(t, err)
	require.Equal(t, "general", saved.Topic)
}

func TestSearch(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	for _, entry := range []Entry{
		{ProjectRoot: "/repo", Topic: "build", Trigger: "compiling", Lesson: "the build needs make BUILD_DIR=build"},
		{ProjectRoot: "/repo", Topic: "tests", Trigger: "running tests", Lesson: "tests must run with -count=1"},
		{ProjectRoot: "/other", Topic: "build", Trigger: "compiling", Lesson: "not this project"},
	} {
		_, err := store.Save(ctx, entry)
		require.NoError(t, err)
	}

	entries, err := store.Search(ctx, "/repo", "build", 10)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		require.Equal(t, "/repo", entry.ProjectRoot, "search is scoped to one project")
	}
}

func TestSearchBlankQuery(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	entries, err := store.Search(context.Background(), "/repo", "   ", 10)
	require.NoError(t, err)
	require.Empty(t, entries, "an empty query retrieves nothing")
}

func TestSearchPunctuationIsSafe(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	_, err := store.Save(ctx, Entry{ProjectRoot: "/repo", Topic: "x", Lesson: "alpha beta"})
	require.NoError(t, err)

	// FTS5 syntax characters in a query must not become syntax.
	_, err = store.Search(ctx, "/repo", `alpha" OR "beta`, 10)
	require.NoError(t, err)
	_, err = store.Search(ctx, "/repo", "alpha*", 10)
	require.NoError(t, err)
	_, err = store.Search(ctx, "/repo", "alpha NEAR(beta)", 10)
	require.NoError(t, err)
}

func TestDelete(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	saved, err := store.Save(ctx, Entry{ProjectRoot: "/repo", Topic: "t", Lesson: "l"})
	require.NoError(t, err)

	require.NoError(t, store.Delete(ctx, saved.ID))
	entries, err := store.List(ctx, "/repo")
	require.NoError(t, err)
	require.Empty(t, entries)

	require.NoError(t, store.Delete(ctx, ""), "an empty id is a no-op")
}

func TestDeleteProject(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	_, err := store.Save(ctx, Entry{ProjectRoot: "/repo", Topic: "t", Lesson: "l"})
	require.NoError(t, err)
	_, err = store.Save(ctx, Entry{ProjectRoot: "/other", Topic: "t", Lesson: "l"})
	require.NoError(t, err)

	require.NoError(t, store.DeleteProject(ctx, "/repo"))

	repo, err := store.List(ctx, "/repo")
	require.NoError(t, err)
	require.Empty(t, repo)
	other, err := store.List(ctx, "/other")
	require.NoError(t, err)
	require.Len(t, other, 1, "another project is untouched")
}

func TestNilStoreIsSafe(t *testing.T) {
	t.Parallel()

	var store *Store
	require.NoError(t, store.Close())
	entries, err := store.List(context.Background(), "/repo")
	require.NoError(t, err)
	require.Nil(t, entries)
	searched, err := store.Search(context.Background(), "/repo", "x", 10)
	require.NoError(t, err)
	require.Nil(t, searched)
	require.NoError(t, store.Delete(context.Background(), "id"))
	require.NoError(t, store.DeleteProject(context.Background(), "/repo"))

	_, err = store.Save(context.Background(), Entry{ProjectRoot: "/repo", Lesson: "l"})
	require.Error(t, err)
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()

	require.Empty(t, DefaultPath(""))
	require.Equal(t, filepath.Join("/data", "memories.db"), DefaultPath("/data"))
}

func TestInMemoryStore(t *testing.T) {
	t.Parallel()

	// An empty path is an in-memory database, which is what a project without a
	// data directory uses.
	store, err := Open("")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	saved, err := store.Save(context.Background(), Entry{ProjectRoot: "/repo", Topic: "t", Lesson: "l"})
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)
}

func TestOpenBytesReadOnlyStore(t *testing.T) {
	t.Parallel()

	// A directory that does not exist yet is created by the driver only when
	// the path is valid; an impossible path must fail rather than panic.
	_, err := Open("/proc/definitely/not/writable/memories.db")
	require.Error(t, err)
}

func TestPromptBlock(t *testing.T) {
	t.Parallel()

	require.Empty(t, PromptBlock(nil))

	block := PromptBlock([]Entry{
		{Topic: "build", Trigger: "compiling", Lesson: "use make BUILD_DIR=build"},
		{Topic: "tests", Lesson: "always pass -count=1"},
	})
	require.Contains(t, block, "<project_memories>")
	require.Contains(t, block, "STRICTLY check and honor")
	require.Contains(t, block, "remove it with the memory removal tool")
	require.Contains(t, block, "[build] when compiling: use make BUILD_DIR=build")
	require.Contains(t, block, "[tests] always pass -count=1")
	require.Contains(t, block, "</project_memories>")
}

func TestFtsQuery(t *testing.T) {
	t.Parallel()

	require.Equal(t, `"alpha"* OR "beta"*`, ftsQuery("alpha beta"))
	require.Equal(t, `"alpha"*`, ftsQuery(`alpha""`))
	require.Equal(t, `""`, ftsQuery("   "))
	require.Equal(t, `"a1"* OR "b2"*`, ftsQuery("a1,b2"))
}

func TestCleanRoot(t *testing.T) {
	t.Parallel()

	require.Empty(t, cleanRoot(""))
	require.Equal(t, filepath.Clean("/repo"), cleanRoot("/repo/"))
	require.Equal(t, filepath.Clean("/repo"), cleanRoot("/repo/./"))
}

func TestFNV64IsStable(t *testing.T) {
	t.Parallel()

	require.Equal(t, fnv64("a"), fnv64("a"))
	require.NotEqual(t, fnv64("a"), fnv64("b"))
	require.NotEqual(t, newID("/r", "t", "l", 1), newID("/r", "t", "l", 2))
	require.Equal(t, newID("/r", "t", "l", 1), newID("/r", "t", "l", 1))
}
