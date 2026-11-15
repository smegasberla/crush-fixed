package editsafe

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckDiffAllowsSmallChange(t *testing.T) {
	t.Parallel()

	oldText := strings.Repeat("line\n", 100)
	newText := "changed\n" + strings.Repeat("line\n", 99)
	require.NoError(t, CheckDiff("a.go", oldText, newText, DefaultLimits()))
}

func TestCheckDiffRefusesCollapse(t *testing.T) {
	t.Parallel()

	oldText := strings.Repeat("line\n", 100)
	err := CheckDiff("a.go", oldText, "one line left\n", DefaultLimits())
	require.True(t, IsCode(err, CodeFileCollapsed))
	require.Contains(t, err.Error(), "keeps 1% of the file's 100 lines")
	require.Contains(t, err.Error(), "truncated write")
}

func TestCheckDiffRefusesExplosion(t *testing.T) {
	t.Parallel()

	// A 40-line file rewritten entirely keeps enough lines to avoid the
	// collapse check but changes far more than the bound allows.
	oldText := strings.Repeat("a\n", 40)
	var b strings.Builder
	for range 40 {
		b.WriteString("b\n")
	}
	err := CheckDiff("a.go", oldText, b.String(), Limits{MaxDiffPercent: 50, CollapsePercent: 10})
	require.True(t, IsCode(err, CodeDiffExplosion))
	require.Contains(t, err.Error(), "50%")
}

func TestCheckDiffSkipsCollapseForSmallFiles(t *testing.T) {
	t.Parallel()

	// A five-line file shrinking to one is normal, not a collapse.
	require.NoError(t, CheckDiff("a.go", "1\n2\n3\n4\n5\n", "1\n", DefaultLimits()))
}

func TestCheckDiffDisabledBounds(t *testing.T) {
	t.Parallel()

	limits := Limits{MaxDiffPercent: 100, CollapsePercent: 1, MaxUnexpectedFiles: 1}
	require.NoError(t, CheckDiff("a.go", strings.Repeat("a\n", 100), "b\n", limits))
}

func TestOwnershipClaimsExpectedPaths(t *testing.T) {
	t.Parallel()

	owner := NewOwnership([]string{"/repo/a.go"}, DefaultLimits())
	require.NoError(t, owner.Claim("/repo/a.go"))
	require.True(t, owner.Owned("/repo/a.go"))
	require.Zero(t, owner.Unexpected())
	require.Equal(t, []string{"/repo/a.go"}, owner.Paths())
}

func TestOwnershipLimitsUnexpected(t *testing.T) {
	t.Parallel()

	owner := NewOwnership(nil, Limits{MaxUnexpectedFiles: 2})
	require.NoError(t, owner.Claim("/repo/a.go"))
	require.NoError(t, owner.Claim("/repo/b.go"))
	require.Equal(t, 2, owner.Unexpected())

	err := owner.Claim("/repo/c.go")
	require.True(t, IsCode(err, CodeRiskDetected))
	require.Contains(t, err.Error(), "already edited 2 files")

	// Claiming an owned file again is free.
	require.NoError(t, owner.Claim("/repo/a.go"))
}

func TestOwnershipNilIsSafe(t *testing.T) {
	t.Parallel()

	var owner *Ownership
	require.NoError(t, owner.Claim("/repo/a.go"))
	require.False(t, owner.Owned("/repo/a.go"))
	require.Nil(t, owner.Paths())
	require.Zero(t, owner.Unexpected())
}

func TestOwnershipNormalisesPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	owner := NewOwnership([]string{dir + "/a.go"}, DefaultLimits())
	require.NoError(t, owner.Claim(dir+"/./a.go"))
	require.True(t, owner.Owned(filepath.Join(dir, "a.go")))
	require.Zero(t, owner.Unexpected())
}

func TestDetectShellWriteSed(t *testing.T) {
	t.Parallel()

	write, ok := DetectShellWrite(`sed -i 's/foo/bar/' internal/a.go`)
	require.True(t, ok)
	require.Equal(t, "internal/a.go", write.Path)
	require.Equal(t, "sed", write.Kind)
}

func TestDetectShellWritePerl(t *testing.T) {
	t.Parallel()

	write, ok := DetectShellWrite(`perl -pi -e 's/foo/bar/' internal/a.go`)
	require.True(t, ok)
	require.Equal(t, "internal/a.go", write.Path)
	require.Equal(t, "perl", write.Kind)
}

func TestDetectShellWriteRedirect(t *testing.T) {
	t.Parallel()

	write, ok := DetectShellWrite(`echo hello > notes.txt`)
	require.True(t, ok)
	require.Equal(t, "notes.txt", write.Path)
	require.Equal(t, "redirect", write.Kind)
}

func TestDetectShellWriteIgnoresNonWrites(t *testing.T) {
	t.Parallel()

	commands := []string{
		"go build ./...",
		"go test ./internal/agent/",
		"grep -rn foo .",
		"ls -la",
		"cat file.go",
		"echo hello",
		"git status",
		// A loop over many files is exactly what the edit tool cannot do.
		"for f in *.go; do sed -i 's/a/b/' $f; done",
		"find . -name '*.go' -exec sed -i 's/a/b/' {} \\;",
		"echo x | xargs sed -i 's/a/b/'",
	}
	for _, command := range commands {
		_, ok := DetectShellWrite(command)
		require.False(t, ok, "should not look like a single-file edit: %q", command)
	}
}

func TestDetectShellWriteIgnoresDevices(t *testing.T) {
	t.Parallel()

	_, ok := DetectShellWrite("echo x > /dev/null")
	require.False(t, ok)
}

func TestDetectShellWriteEmpty(t *testing.T) {
	t.Parallel()

	_, ok := DetectShellWrite("")
	require.False(t, ok)
	_, ok = DetectShellWrite("   ")
	require.False(t, ok)
}

func TestShellRoutingNote(t *testing.T) {
	t.Parallel()

	note := ShellRoutingNote("internal/a.go")
	require.Contains(t, note, "[Edit Routing]")
	require.Contains(t, note, "internal/a.go")
	require.Contains(t, note, "one snapshot")
	require.Contains(t, note, "line endings")
}

func TestLimitsDefaults(t *testing.T) {
	t.Parallel()

	limits := Limits{}.withDefaults()
	defaults := DefaultLimits()
	require.Equal(t, defaults.MaxDiffPercent, limits.MaxDiffPercent)
	require.Equal(t, defaults.MaxUnexpectedFiles, limits.MaxUnexpectedFiles)
	require.Equal(t, defaults.CollapsePercent, limits.CollapsePercent)
}
