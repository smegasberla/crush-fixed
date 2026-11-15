package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestViewAroundCentresTheRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path, Around: 50, Context: 5})
	text := resp.Content

	require.Contains(t, text, "line 50")
	require.Contains(t, text, "line 45")
	require.Contains(t, text, "line 55")
	require.NotContains(t, text, "line 44")
	require.NotContains(t, text, "line 1")
}

func TestViewAroundDefaultContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path, Around: 100})
	text := resp.Content

	require.Contains(t, text, "line 100")
	require.Contains(t, text, fmt.Sprintf("line %d", 100-DefaultAroundContext))
	require.Contains(t, text, fmt.Sprintf("line %d", 100+DefaultAroundContext))
}

func TestViewAroundNearStartClampsToZero(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path, Around: 1, Context: 5})
	require.Contains(t, resp.Content, "one")
	require.False(t, resp.IsError)
}

func TestViewBatchedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "a.go")
	second := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(first, []byte("package a\n"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("package b\n"), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{
		Files: []ViewFileParams{
			{FilePath: first},
			{FilePath: second},
		},
	})
	text := resp.Content
	require.Contains(t, text, "package a")
	require.Contains(t, text, "package b")
	require.False(t, resp.IsError)
}

func TestViewBatchedFilesKeepsGoodOnesOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(good, []byte("package good\n"), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{
		Files: []ViewFileParams{
			{FilePath: good},
			{FilePath: filepath.Join(dir, "missing.go")},
		},
	})
	text := resp.Content
	require.Contains(t, text, "package good", "one bad path must not lose the good one")
	require.Contains(t, text, "missing.go")
	require.Contains(t, text, "1 of 2 paths could not be read")
}

func TestViewPermissionsParamsProjectsFiles(t *testing.T) {
	t.Parallel()

	params := viewPermissionsParams(ViewParams{
		FilePath: "a.go",
		Around:   10,
		Context:  5,
		Metadata: true,
		Files:    []ViewFileParams{{FilePath: "b.go"}, {FilePath: "c.go"}},
	})
	require.Equal(t, "a.go", params.FilePath)
	require.Equal(t, 10, params.Around)
	require.Equal(t, 5, params.Context)
	require.True(t, params.Metadata)
	require.Equal(t, []string{"b.go", "c.go"}, params.Files)
}
