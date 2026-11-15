package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// verifyTool is a stub tool that fails the first n calls, for exercising the
// recheck guard.
type verifyTool struct {
	name  string
	calls int
	out   string
}

func (v *verifyTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: v.name, Parameters: map[string]any{}}
}

func (v *verifyTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (v *verifyTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (v *verifyTool) Run(_ context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	v.calls++
	return fantasy.NewTextResponse(v.out), nil
}

func speedTurn(t *testing.T) *turnSpeed {
	t.Helper()
	return newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
}

func TestRecheckGuardRefusesRepeatedBuild(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &verifyTool{name: "bash", out: "ok  \tgithub.com/x\t0.1s"}
	tool := &cachedTool{
		inner:   inner,
		turn:    func() *turnSpeed { return turn },
		recheck: true,
	}

	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"go test ./..."}`}
	first, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Contains(t, first.Content, "ok")
	require.Contains(t, first.Content, "does not need to be run again",
		"the first result says the answer is now known")

	second, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, []string{inner.name}, 1)
	require.Equal(t, 1, inner.calls, "the identical build was not run again")
	require.Contains(t, second.Content, "Not run again")
	require.Equal(t, 1, turn.rechecks)
}

func TestRecheckGuardRunsAgainAfterAMutation(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &verifyTool{name: "bash", out: "ok"}
	tool := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, recheck: true}

	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"go build ./..."}`}
	_, err := tool.Run(context.Background(), call)
	require.NoError(t, err)

	// An edit invalidates every cached answer, so the next build is real.
	turn.invalidate()
	_, err = tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls, "a changed workspace means the check must run")
}

func TestRecheckGuardNeverCachesAFailure(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &verifyTool{name: "bash", out: "--- FAIL: TestThing\nbuild failed"}
	tool := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, recheck: true}

	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"go test ./..."}`}
	_, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	_, err = tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls, "a failing check must be re-run to see the fix")
}

func TestRecheckGuardOnlyForValidationCommands(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &verifyTool{name: "bash", out: "some output"}
	tool := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, recheck: true}

	// `ls` is not a validation command: its answer is not a verdict.
	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"ls -la"}`}
	_, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	_, err = tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls)
}

func TestRecheckGuardSkipsWritingCommands(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &verifyTool{name: "bash", out: "ok"}
	tool := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, recheck: true}

	// `go generate` is a build-adjacent command that writes.
	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"go generate ./..."}`}
	_, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	_, err = tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls, "a command that writes is not a recheck")
}

func TestCallRecheckEligible(t *testing.T) {
	t.Parallel()

	require.True(t, callRecheckEligible(fantasy.ToolCall{Name: "bash", Input: `{"command":"go test ./..."}`}))
	require.True(t, callRecheckEligible(fantasy.ToolCall{Name: "bash", Input: `{"command":"cargo check"}`}))
	require.True(t, callRecheckEligible(fantasy.ToolCall{Name: "diagnostics", Input: `{}`}))

	require.False(t, callRecheckEligible(fantasy.ToolCall{Name: "bash", Input: `{"command":"ls"}`}))
	require.False(t, callRecheckEligible(fantasy.ToolCall{Name: "bash", Input: `{"command":"go generate ./..."}`}))
	require.False(t, callRecheckEligible(fantasy.ToolCall{Name: "bash", Input: `{"command":"sed -i 's/a/b/' f.go"}`}))
}

func TestInputCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "go build", inputCommand(`{"command":"go build"}`))
	require.Equal(t, "raw", inputCommand("raw"), "an unparseable input is passed through")
	require.Empty(t, inputCommand(`{}`))
}

func TestResponseIsFailure(t *testing.T) {
	t.Parallel()

	for _, out := range []string{
		"build failed", "--- FAIL: TestX", "error: cannot find package",
		"exit status 1", "panic: runtime error", "fatal: not a repo",
	} {
		require.True(t, responseIsFailure(out), "should read as a failure: %q", out)
	}
	for _, out := range []string{
		"ok  \tgithub.com/x\t0.1s", "PASS", "BUILD SUCCESSFUL", "3 tests passed",
	} {
		require.False(t, responseIsFailure(out), "should read as success: %q", out)
	}
}

func TestRecheckNotesReadCorrectly(t *testing.T) {
	t.Parallel()

	require.Contains(t, firstRunNote(), "does not need to be run again")
	note := notReRunNote("bash", "ok")
	require.Contains(t, note, "Not run again")
	require.Contains(t, note, "bash")
	require.Contains(t, note, "ok")
}

func TestReadCacheStillWorksAlongsideRecheck(t *testing.T) {
	t.Parallel()

	turn := speedTurn(t)
	inner := &stubTool{name: "view", body: "file body"}
	tool := &cachedTool{
		inner:     inner,
		turn:      func() *turnSpeed { return turn },
		cacheable: true,
	}
	call := fantasy.ToolCall{Name: "view", Input: `{"file_path":"a.go"}`}
	_, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	second, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, "file body", second.Content, "a read is answered plainly, with no note")
	require.Len(t, inner.runs, 1)
	require.False(t, strings.Contains(second.Content, "Not run again"))
}
