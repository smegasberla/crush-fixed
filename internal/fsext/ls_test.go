package fsext

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListDirectory(t *testing.T) {
	tmp := t.TempDir()

	testFiles := map[string]string{
		"regular.txt":     "content",
		".hidden":         "hidden content",
		".gitignore":      ".*\n*.log\n",
		"subdir/file.go":  "package main",
		"subdir/.another": "more hidden",
		"build.log":       "build output",
	}

	for name, content := range testFiles {
		fp := filepath.Join(tmp, name)
		dir := filepath.Dir(fp)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
	}

	t.Run("no limit", func(t *testing.T) {
		files, truncated, err := ListDirectory(tmp, nil, -1, -1)
		require.NoError(t, err)
		require.False(t, truncated)
		// The .gitignore has ".*" pattern which ignores hidden files anywhere
		// (like real git does), so subdir/.another is ignored.
		require.Len(t, files, 3)
		require.ElementsMatch(t, []string{
			"regular.txt",
			"subdir",
			"subdir/file.go",
		}, relPaths(t, files, tmp))
	})
	t.Run("limit", func(t *testing.T) {
		files, truncated, err := ListDirectory(tmp, nil, -1, 2)
		require.NoError(t, err)
		require.True(t, truncated)
		require.Len(t, files, 2)
	})
}

func relPaths(tb testing.TB, in []string, base string) []string {
	tb.Helper()
	out := make([]string, 0, len(in))
	for _, p := range in {
		rel, err := filepath.Rel(base, p)
		require.NoError(tb, err)
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

// TestListDirectoryReportsTruncation covers a listing that hit its cap: it must
// say so. The flag used to be inferred from the walk's error, which fastwalk
// does not always report for SkipAll, so a listing cut short claimed to be
// complete and callers had no way to know files were missing.
func TestListDirectoryReportsTruncation(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	for i := range 20 {
		name := filepath.Join(tmp, fmt.Sprintf("file%02d.go", i))
		require.NoError(t, os.WriteFile(name, []byte("x"), 0o644))
	}

	files, truncated, err := ListDirectory(tmp, nil, 0, 5)
	require.NoError(t, err)
	require.Len(t, files, 5)
	require.True(t, truncated, "a listing cut short must report truncation")

	files, truncated, err = ListDirectory(tmp, nil, 0, 0)
	require.NoError(t, err)
	require.Len(t, files, 20)
	require.False(t, truncated, "an unlimited listing of a small directory is complete")
}

// TestListDirectoryCanKeepIgnoredFiles covers the @ completion path. A file the
// user names by hand is often one the repository ignores (.env, a scratch doc),
// and dropping it from the list makes it impossible to reference at all.
func TestListDirectoryCanKeepIgnoredFiles(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("*.md\nnode_modules/\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "CHASER.md"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "keep.go"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "node_modules", "dep.js"), []byte("x"), 0o644))

	// The default listing is what a scanning tool wants: ignored paths gone.
	files, _, err := ListDirectory(tmp, nil, 0, 0)
	require.NoError(t, err)
	require.NotContains(t, relPaths(t, files, tmp), "CHASER.md")

	// Naming a file by hand is different: the ignored file is listed, while
	// the ignored directory tree is still pruned so the walk stays cheap.
	files, _, err = ListDirectoryWithOptions(tmp, nil, 0, 0, ListOptions{KeepIgnoredFiles: true})
	require.NoError(t, err)
	got := relPaths(t, files, tmp)
	require.Contains(t, got, "CHASER.md", "an ignored file must still be nameable")
	require.Contains(t, got, "keep.go")
	require.NotContains(t, got, "node_modules/dep.js", "ignored directories stay pruned")
}
