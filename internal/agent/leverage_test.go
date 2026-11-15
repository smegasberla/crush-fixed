package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/goaltree"
	"github.com/stretchr/testify/require"
)

func leverageForTest(t *testing.T, settings *config.LeverageOptions) *leverageState {
	t.Helper()
	dir := t.TempDir()
	state := newLeverageState(&LeverageOptions{
		Settings:    settings,
		DataDir:     dir,
		ProjectRoot: filepath.Join(dir, "repo"),
	})
	require.NotNil(t, state)
	t.Cleanup(state.Close)
	return state
}

func TestNewLeverageStateDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, newLeverageState(nil))
	require.Nil(t, newLeverageState(&LeverageOptions{}))

	off := false
	require.Nil(t, newLeverageState(&LeverageOptions{
		Settings: &config.LeverageOptions{Enabled: &off},
	}))
}

func TestLeverageNilIsSafe(t *testing.T) {
	t.Parallel()

	var state *leverageState
	require.False(t, state.enabled())
	state.Close()
	require.Empty(t, state.memoryBlock(context.Background(), "q"))
	require.NoError(t, state.rememberLesson(context.Background(), "t", "x", "l"))
	require.Nil(t, state.lessons(context.Background()))
	require.NoError(t, state.forgetLesson(context.Background(), "id"))
	require.Equal(t, "core", state.layeredPrompt("s", "core", "", ""))
	require.Equal(t, "reply", state.cleanReply("reply"))
	require.False(t, state.resumeProbeEnabled())
	require.False(t, state.goalTreeEnabled())
	require.False(t, state.verificationMemoryEnabled())
	state.noteEditForBisect("a.go")
	require.Empty(t, state.bisectHint())
	require.Zero(t, state.resumeMaxAge())
}

func TestLeverageMemoryRoundTrip(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{})
	ctx := context.Background()

	require.NoError(t, state.rememberLesson(ctx, "build", "compiling", "use make BUILD_DIR=build"))
	lessons := state.lessons(ctx)
	require.Len(t, lessons, 1)
	require.Equal(t, "build", lessons[0].Topic)

	block := state.memoryBlock(ctx, "build")
	require.Contains(t, block, "<project_memories>")
	require.Contains(t, block, "BUILD_DIR")

	require.NoError(t, state.forgetLesson(ctx, lessons[0].ID))
	require.Empty(t, state.lessons(ctx))
}

func TestLeverageMemoryDisabled(t *testing.T) {
	t.Parallel()

	off := false
	state := leverageForTest(t, &config.LeverageOptions{ProjectMemory: &off})
	require.Nil(t, state.memories)
	require.NoError(t, state.rememberLesson(context.Background(), "t", "x", "l"))
	require.Empty(t, state.memoryBlock(context.Background(), "t"))
}

func TestLayeredPrompt(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{})
	rendered := state.layeredPrompt("session", "core text", "project context", "the contract", "a note")
	require.Contains(t, rendered, "core text")
	require.Contains(t, rendered, "project context")
	require.Contains(t, rendered, "the contract")
	require.Contains(t, rendered, "a note")

	// The digest is retained, so the next call compares against it.
	require.NotEmpty(t, state.lastPrefixDigest)

	// With layering off, the layers are still ordered but nothing is tracked.
	off := false
	disabled := leverageForTest(t, &config.LeverageOptions{PromptLayering: &off})
	rendered = disabled.layeredPrompt("session", "core", "", "")
	require.Equal(t, "core", rendered)
	require.Empty(t, disabled.lastPrefixDigest)
}

func TestCleanReply(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{})
	require.Equal(t, "clean answer", state.cleanReply("clean answer"))
	require.Equal(t, "the answer", state.cleanReply("<|assistant|>\nthe answer\n<|end|>"))

	off := false
	disabled := leverageForTest(t, &config.LeverageOptions{ReplyHygiene: &off})
	raw := "<|assistant|>\nthe answer\n<|end|>"
	require.Equal(t, raw, disabled.cleanReply(raw), "the pass is off")
}

func TestGoalContract(t *testing.T) {
	t.Parallel()

	withFiles := goalContract("fix the loop", []string{"agent.go"})
	require.Contains(t, withFiles, "GOAL: fix the loop")
	require.Contains(t, withFiles, "agent.go")
	require.Contains(t, withFiles, "validate the change")

	withoutFiles := goalContract("answer the question", nil)
	require.Contains(t, withoutFiles, "identify the cause")
	require.NotContains(t, withoutFiles, "validate the change")
}

func TestResumeCandidate(t *testing.T) {
	t.Parallel()

	candidate, ok := resumeCandidate("s", "Fix the loop", 100, []sessionTodoLite{
		{Content: "done step", Status: "completed"},
		{Content: "open step", Status: "pending"},
		{Content: "cancelled step", Status: "cancelled"},
	})
	require.True(t, ok)
	require.Equal(t, []string{"open step"}, candidate.OpenTasks)
	require.Equal(t, "Fix the loop", candidate.Title)

	_, ok = resumeCandidate("s", "done", 100, []sessionTodoLite{{Content: "x", Status: "completed"}})
	require.False(t, ok)
	_, ok = resumeCandidate("s", "empty", 100, nil)
	require.False(t, ok)
}

func TestResumeSelection(t *testing.T) {
	t.Parallel()

	first, ok := resumeCandidate("old", "Old", 100, []sessionTodoLite{{Content: "a", Status: "pending"}})
	require.True(t, ok)
	second, ok := resumeCandidate("new", "New", 500, []sessionTodoLite{{Content: "b", Status: "in_progress"}})
	require.True(t, ok)

	picked, found := goaltree.PickResume([]goaltree.ResumeCandidate{first, second}, 600, 3600)
	require.True(t, found)
	require.Equal(t, "new", picked.SessionID)
	require.Contains(t, goaltree.ResumePrompt(picked), "b")
}

func TestLeverageToggles(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{})
	require.True(t, state.resumeProbeEnabled())
	require.True(t, state.goalTreeEnabled())
	require.True(t, state.verificationMemoryEnabled())
	require.Equal(t, 72*3600*1000000000, int(state.resumeMaxAge()))

	off := false
	disabled := leverageForTest(t, &config.LeverageOptions{
		ResumeProbe:        &off,
		GoalTree:           &off,
		VerificationMemory: &off,
	})
	require.False(t, disabled.resumeProbeEnabled())
	require.False(t, disabled.goalTreeEnabled())
	require.False(t, disabled.verificationMemoryEnabled())
}

func TestBisectLedger(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{})
	require.Contains(t, state.bisectHint(), "nothing changed")
	state.noteEditForBisect("internal/agent/agent.go")
	require.Contains(t, state.bisectHint(), "agent.go")
}

func TestLeverageStateCloseIsSafeWhenMemoryDisabled(t *testing.T) {
	t.Parallel()

	off := false
	state := leverageForTest(t, &config.LeverageOptions{ProjectMemory: &off})
	state.Close()
	state.Close()
}

func TestResumeMaxAgeDuration(t *testing.T) {
	t.Parallel()

	state := leverageForTest(t, &config.LeverageOptions{ResumeMaxAgeHours: ptrInt(1)})
	require.Equal(t, int64(3600*1000000000), int64(state.resumeMaxAge()))
}

func ptrInt(v int) *int { return &v }
