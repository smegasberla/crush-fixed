package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func newMemoViewTool(t *testing.T, workingDir string) (fantasy.AgentTool, *ReadMemo) {
	t.Helper()
	permissions := &testutil.MockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	memo := NewReadMemo()
	return NewViewToolWithMemo(memo, nil, permissions, mockFileTracker{}, nil, workingDir), memo
}

func viewContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, false)
	return ctx
}

func TestViewShortReadIsFullBody(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\n"), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path})
	require.Contains(t, resp.Content, "<file>")
	require.Contains(t, resp.Content, "package a")
}

func TestViewPointerOnRepeatedUnchangedRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\nvar x = 1\n"), 0o644))

	tool, memo := newMemoViewTool(t, dir)
	ctx := viewContext(t)

	first := runViewTool(t, tool, ctx, ViewParams{FilePath: path})
	require.Contains(t, first.Content, "var x = 1")
	require.Equal(t, 1, memo.Len("test-session"))

	second := runViewTool(t, tool, ctx, ViewParams{FilePath: path})
	text := second.Content
	require.Contains(t, text, "file unchanged")
	require.Contains(t, text, "already in this conversation")
	require.NotContains(t, text, "var x = 1", "the body is not sent twice")

	// "keep" forces the body back.
	third := runViewTool(t, tool, ctx, ViewParams{FilePath: path, Keep: true})
	require.Contains(t, third.Content, "var x = 1")
}

func TestViewPointerInvalidatedByChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\n"), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	ctx := viewContext(t)
	runViewTool(t, tool, ctx, ViewParams{FilePath: path})

	require.NoError(t, os.WriteFile(path, []byte("package a\n// changed\n"), 0o644))
	resp := runViewTool(t, tool, ctx, ViewParams{FilePath: path})
	require.NotContains(t, resp.Content, "file unchanged")
	require.Contains(t, resp.Content, "// changed")
}

func TestViewPointerOnlyForSameRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	var lines []string
	for i := range 50 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	ctx := viewContext(t)
	runViewTool(t, tool, ctx, ViewParams{FilePath: path, Offset: 0, Limit: 10})

	resp := runViewTool(t, tool, ctx, ViewParams{FilePath: path, Offset: 20, Limit: 10})
	require.NotContains(t, resp.Content, "file unchanged")
	require.Contains(t, resp.Content, "line 20")
}

func TestViewMetadataHeaderOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\r\nvar x = 1\r\n"), 0o644))

	tool, memo := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path, Metadata: true})
	text := resp.Content

	require.Contains(t, text, "<file-metadata")
	require.Contains(t, text, "bytes: 22")
	require.Contains(t, text, "lines: 2")
	require.Contains(t, text, "line_endings: crlf")
	require.Contains(t, text, "content omitted")
	require.NotContains(t, text, "var x = 1")
	require.Zero(t, memo.Len("test-session"), "a header-only read records nothing")
}

func TestViewMetadataHasMore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	tool, _ := newMemoViewTool(t, dir)
	resp := runViewTool(t, tool, viewContext(t), ViewParams{FilePath: path, Limit: 5, Metadata: true})
	require.Contains(t, resp.Content, "more: yes")
	require.Contains(t, resp.Content, "range: 1-5")
}

func TestViewSkillFileNeverPointered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))
	path := filepath.Join(skillsDir, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte("---\nname: s\ndescription: d\n---\nbody\n"), 0o644))

	permissions := &testutil.MockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	memo := NewReadMemo()
	tool := NewViewToolWithMemo(memo, nil, permissions, mockFileTracker{}, nil, dir, skillsDir)
	ctx := viewContext(t)

	first := runViewTool(t, tool, ctx, ViewParams{FilePath: path})
	require.Contains(t, first.Content, "body")
	second := runViewTool(t, tool, ctx, ViewParams{FilePath: path})
	require.Contains(t, second.Content, "body", "skill files are always sent in full")
	require.Zero(t, memo.Len("test-session"))
}

func TestLineEndingName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "none", lineEndingName(0, 0))
	require.Equal(t, "lf", lineEndingName(0, 3))
	require.Equal(t, "crlf", lineEndingName(2, 2))
	require.Equal(t, "mixed", lineEndingName(1, 2))
}
