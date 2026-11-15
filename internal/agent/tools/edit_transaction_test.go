package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/editsafe"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// turnContext builds a context with a session and the turn's edit state.
func turnContext(t *testing.T, expected ...string) (context.Context, *EditTurnState) {
	t.Helper()
	state := NewEditTurnState(EditTurnConfig{
		Limits:         editsafe.DefaultLimits(),
		ExpectedPaths:  expected,
		InterceptShell: true,
		Journal:        true,
	})
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	return WithEditTurnState(ctx, state), state
}

// newEditToolForTest returns an edit tool wired to a mock permission service.
func newEditToolForTest(t *testing.T, workingDir string) fantasy.AgentTool {
	t.Helper()
	permissions := &testutil.MockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	return NewEditTool(nil, permissions, nil, mockFileTracker{}, workingDir)
}

// runEditTool executes the edit tool with the given params.
func runEditTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params EditParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func TestEditToolLineAddressed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 2, Expect: "two", Delete: true})

	require.False(t, resp.IsError, "response: %s", resp.Content)
	require.Contains(t, resp.Content, "1 op(s) applied")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\nthree\n", string(raw), "a delete removes the addressed line")
}

func TestEditToolLineAddressedReplacement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 2, Expect: "two", NewString: "TWO"})
	require.False(t, resp.IsError, "response: %s", resp.Content)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", string(raw))
}

func TestEditToolMultiRegionBatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{
		FilePath: path,
		Ops: []EditOpParams{
			{ID: "first", Line: 1, Expect: "one", NewString: "ONE"},
			{ID: "second", Line: 4, Expect: "four", NewString: "FOUR"},
		},
	})
	require.False(t, resp.IsError, "response: %s", resp.Content)
	require.Contains(t, resp.Content, "2 op(s) applied")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "ONE\ntwo\nthree\nFOUR\n", string(raw))
}

func TestEditToolBatchIsAllOrNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{
		FilePath: path,
		Ops: []EditOpParams{
			{Line: 1, Expect: "one", NewString: "ONE"},
			{Line: 2, Expect: "wrong", NewString: "TWO"},
		},
	})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "anchor_gone")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\n", string(raw), "nothing was written")
}

func TestEditToolRefusesMissingExpect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 1, NewString: "x"})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "missing_anchor")
	require.Contains(t, resp.Content, "expect token")
}

func TestEditToolRefusesCollapse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("line\n", 100)), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	// The addressed path reaches the engine directly, which is what applies
	// the size bounds.
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 1, Expect: "line", NewString: ""})
	require.False(t, resp.IsError, "a single-line edit of a large file is fine: %s", resp.Content)

	// A change this large is refused by the size bounds. Which of the two
	// bounds trips depends on the shape: a whole-file replacement that keeps
	// almost nothing is a collapse, and the engine names whichever it sees
	// first rather than pretending one is the other.
	resp = runEditTool(t, tool, ctx, EditParams{
		FilePath: path,
		Ops: []EditOpParams{{
			OldString:  "line\n",
			NewString:  "one\n",
			ReplaceAll: true,
		}},
	})
	require.True(t, resp.IsError)
	require.True(t,
		strings.Contains(resp.Content, "file_collapsed") || strings.Contains(resp.Content, "diff_explosion"),
		"a whole-file rewrite is refused by a size bound, got: %s", resp.Content)
	require.Contains(t, resp.Content, "the whole file is being rewritten")
}

func TestEditToolJournalsTheChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, state := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 2, Expect: "two", NewString: "TWO"})
	require.False(t, resp.IsError, "response: %s", resp.Content)

	require.Equal(t, 1, state.Journal.Len())
	entries := state.Journal.Entries()
	require.Equal(t, path, entries[0].Path)
	require.Equal(t, "one\ntwo\n", entries[0].Before)
	require.Equal(t, "one\nTWO\n", entries[0].After)

	result, err := state.Journal.Revert()
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Restored)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\n", string(raw))
}

func TestEditToolRecordsOwnership(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, state := turnContext(t, path)
	runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 1, Expect: "one", NewString: "ONE"})
	require.True(t, state.Ownership.Owned(path))
	require.Zero(t, state.Ownership.Unexpected(), "a file the request named is not unexpected")
}

func TestEditToolRefusesBeyondUnexpectedCap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := NewEditTurnState(EditTurnConfig{
		Limits:         editsafe.Limits{MaxUnexpectedFiles: 1, MaxDiffPercent: 80, CollapsePercent: 20},
		InterceptShell: true,
		Journal:        true,
	})
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "s")
	ctx = WithEditTurnState(ctx, state)
	tool := newEditToolForTest(t, dir)

	first := filepath.Join(dir, "a.go")
	second := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(first, []byte("one\n"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("two\n"), 0o644))

	resp := runEditTool(t, tool, ctx, EditParams{FilePath: first, Line: 1, Expect: "one", NewString: "ONE"})
	require.False(t, resp.IsError, "response: %s", resp.Content)

	resp = runEditTool(t, tool, ctx, EditParams{FilePath: second, Line: 1, Expect: "two", NewString: "TWO"})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "EDIT_RISK_DETECTED")
}

func TestEditToolPreservesCRLF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\r\ntwo\r\n"), 0o644))

	tool := newEditToolForTest(t, dir)
	ctx, _ := turnContext(t, path)
	resp := runEditTool(t, tool, ctx, EditParams{FilePath: path, Line: 2, Expect: "two", NewString: "TWO"})
	require.False(t, resp.IsError, "response: %s", resp.Content)
	require.Contains(t, resp.Content, "line_endings: crlf")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\r\nTWO\r\n", string(raw))
}

func TestInterceptShellEditRefusesInPlaceWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	ctx, _ := turnContext(t, path)
	message, intercepted := interceptShellEdit(ctx, "sed -i 's/one/ONE/' a.go", dir)
	require.True(t, intercepted)
	require.Contains(t, message, "[Edit Routing]")
	require.Contains(t, message, path)
	require.Contains(t, message, "Nothing was run")

	// The file was not touched.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "one\n", string(raw))
}

func TestInterceptShellEditAllowsOtherCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, _ := turnContext(t)

	for _, command := range []string{"go build ./...", "ls", "git status"} {
		_, intercepted := interceptShellEdit(ctx, command, dir)
		require.False(t, intercepted, "should not intercept %q", command)
	}
}

func TestInterceptShellEditDisabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	state := NewEditTurnState(EditTurnConfig{
		Limits:         editsafe.DefaultLimits(),
		InterceptShell: false,
		Journal:        true,
	})
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "s")
	ctx = WithEditTurnState(ctx, state)

	_, intercepted := interceptShellEdit(ctx, "sed -i 's/one/ONE/' a.go", dir)
	require.False(t, intercepted, "interception is off")
}

func TestInterceptShellEditWithoutTurnState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o644))

	_, intercepted := interceptShellEdit(context.Background(), "sed -i 's/one/ONE/' a.go", dir)
	require.False(t, intercepted)
}

func TestInterceptShellEditAllowsNewFileRedirect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, _ := turnContext(t)
	// The target does not exist: creating it with a redirect is a write, not
	// an edit, and the shell is the right tool.
	_, intercepted := interceptShellEdit(ctx, "echo hello > new.txt", dir)
	require.False(t, intercepted)
}

func TestRevertReportEmpty(t *testing.T) {
	t.Parallel()

	require.True(t, RevertReport{}.Empty())
	require.False(t, RevertReport{Restored: []string{"a"}}.Empty())
	require.False(t, RevertReport{Removed: []string{"a"}}.Empty())
	require.False(t, RevertReport{Recreated: []string{"a"}}.Empty())
}
