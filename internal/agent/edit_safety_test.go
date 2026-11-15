package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/editsafe"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func editSafetyAgent(t *testing.T, settings *config.EditSafetyOptions) *sessionAgent {
	t.Helper()
	return &sessionAgent{editSafety: settings, workingDir: t.TempDir()}
}

func TestEditLimitsDefaults(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	limits := agent.editLimits()
	require.Equal(t, editsafe.DefaultLimits().MaxDiffPercent, limits.MaxDiffPercent)
	require.Equal(t, editsafe.DefaultLimits().MaxUnexpectedFiles, limits.MaxUnexpectedFiles)
}

func TestEditLimitsFollowSettings(t *testing.T) {
	t.Parallel()

	percent := 40
	files := 3
	agent := editSafetyAgent(t, &config.EditSafetyOptions{
		MaxExpectedDiffPercent: &percent,
		MaxUnexpectedFiles:     &files,
	})
	limits := agent.editLimits()
	require.Equal(t, 40, limits.MaxDiffPercent)
	require.Equal(t, 3, limits.MaxUnexpectedFiles)
}

func TestInterceptAndJournalToggles(t *testing.T) {
	t.Parallel()

	off := false
	agent := editSafetyAgent(t, &config.EditSafetyOptions{
		InterceptShell: &off,
		Journal:        &off,
	})
	require.False(t, agent.interceptShellEdits())
	require.False(t, agent.journalEdits())

	agent = editSafetyAgent(t, &config.EditSafetyOptions{})
	require.True(t, agent.interceptShellEdits())
	require.True(t, agent.journalEdits())

	// A nil settings block means the defaults, which are on.
	require.True(t, (&sessionAgent{}).interceptShellEdits())
	require.True(t, (&sessionAgent{}).journalEdits())
}

func TestExpectedEditPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agent := &sessionAgent{workingDir: dir}
	msgs := []message.Message{{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "please update internal/agent/agent.go and also main.go"},
		},
	}}
	paths := agent.expectedEditPaths(msgs, SessionAgentCall{Prompt: "and docs/README.md"})

	require.Contains(t, paths, filepath.Join(dir, "internal/agent/agent.go"))
	require.Contains(t, paths, filepath.Join(dir, "main.go"))
	require.Contains(t, paths, filepath.Join(dir, "docs/README.md"))

	// An absolute path stays absolute.
	absolute := filepath.Join(dir, "abs.go")
	paths = agent.expectedEditPaths(nil, SessionAgentCall{Prompt: absolute})
	require.Contains(t, paths, absolute)
}

func TestRevertTurnWithoutJournal(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	_, err := agent.RevertTurn("session")
	require.True(t, errors.Is(err, ErrNothingToRevert))
}

func TestRevertTurnRestoresTheLastTurn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("after\n"), 0o644))

	journal := editsafe.NewJournal()
	journal.Record(editsafe.JournalEntry{
		Path:      path,
		Before:    "before\n",
		After:     "after\n",
		AfterHash: editsafe.Hash([]byte("after\n")),
	})

	agent := &sessionAgent{lastJournal: journal}
	report, err := agent.RevertTurn("session")
	require.NoError(t, err)
	require.Equal(t, []string{path}, report.Restored)
	require.False(t, report.Empty())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "before\n", string(raw))
}

func TestNewEditTurnStateHonoursToggles(t *testing.T) {
	t.Parallel()

	state := tools.NewEditTurnState(tools.EditTurnConfig{
		Limits:         editsafe.DefaultLimits(),
		ExpectedPaths:  []string{"/repo/a.go"},
		InterceptShell: false,
		Journal:        false,
	})
	require.NotNil(t, state.Ownership)
	require.Nil(t, state.Journal, "journaling was turned off")
	require.False(t, state.InterceptShell)

	state = tools.NewEditTurnState(tools.EditTurnConfig{Limits: editsafe.DefaultLimits(), Journal: true})
	require.NotNil(t, state.Journal)
}
