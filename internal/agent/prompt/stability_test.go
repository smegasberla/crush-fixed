package prompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSystemPromptIsStableWhileTheTreeChanges is the cache contract: the system
// prompt is the cacheable prefix, so it must not change when the working tree
// does. Git state used to be rendered into it, which meant every file a turn
// touched invalidated the provider's cache and the next round paid full price
// for the whole prefix again.
func TestSystemPromptIsStableWhileTheTreeChanges(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "first")

	cfg, err := config.Init(context.Background(), dir, "", false)
	require.NoError(t, err)
	p, err := NewPrompt("coder", testCoderTemplate(t), WithPlatform("linux"), WithWorkingDir(dir))
	require.NoError(t, err)

	before, err := p.Build(context.Background(), "p", "m", cfg)
	require.NoError(t, err)

	// Change the tree: dirty a file and add an untracked one.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new"), 0o644))

	after, err := p.Build(context.Background(), "p", "m", cfg)
	require.NoError(t, err)

	require.Equal(t, before, after,
		"the system prompt must stay byte-identical when the tree changes, or the cache is lost every round")

	// The state is still available, on its own, for the user turn.
	require.NotEmpty(t, GitStatusBlock(context.Background(), dir))
}
