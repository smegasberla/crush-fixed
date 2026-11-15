package validate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractGoCompileErrors(t *testing.T) {
	t.Parallel()

	output := `# github.com/charmbracelet/crush/internal/agent
internal/agent/agent.go:42:5: undefined: foo
internal/agent/agent.go:43:6: cannot use x (variable of type int) as string
FAIL	github.com/charmbracelet/crush/internal/agent [build failed]`
	failures := Extract(output)
	require.Len(t, failures, 2)
	require.Equal(t, KindCompile, failures[0].Kind)
	require.Equal(t, "internal/agent/agent.go", failures[0].File)
	require.Equal(t, 42, failures[0].Line)
	require.Contains(t, failures[0].Message, "undefined")
}

func TestExtractRustErrors(t *testing.T) {
	t.Parallel()

	failures := Extract("src/main.rs:10:5: error[E0425]: cannot find value `x` in this scope")
	require.Len(t, failures, 1)
	require.Equal(t, "src/main.rs", failures[0].File)
	require.Equal(t, 10, failures[0].Line)
}

func TestExtractTypeScriptErrors(t *testing.T) {
	t.Parallel()

	failures := Extract("src/app.ts(12,5): error TS2322: Type 'number' is not assignable to type 'string'.")
	require.Len(t, failures, 1)
	require.Equal(t, "src/app.ts", failures[0].File)
	require.Equal(t, 12, failures[0].Line)
}

func TestExtractTestFailures(t *testing.T) {
	t.Parallel()

	output := `--- FAIL: TestFoo (0.01s)
    foo_test.go:12: expected 1, got 2
FAIL
ok  	github.com/foo/bar	0.02s`
	failures := Extract(output)
	require.NotEmpty(t, failures)
	for _, failure := range failures {
		require.NotContains(t, failure.Message, "ok  ")
	}
}

func TestExtractIgnoresNoise(t *testing.T) {
	t.Parallel()

	output := `ok  	github.com/foo/bar	0.02s
PASS
tests passed
0 errors
`
	require.Empty(t, Extract(output))
}

func TestExtractDeduplicates(t *testing.T) {
	t.Parallel()

	output := `internal/a.go:1:1: undefined: foo
internal/a.go:2:1: undefined: foo`
	failures := Extract(output)
	require.Len(t, failures, 1, "the same shape is one failure")
}

func TestFingerprintIgnoresIdentifiers(t *testing.T) {
	t.Parallel()

	first := Fingerprint(KindCompile, "undefined: foo")
	second := Fingerprint(KindCompile, "undefined: bar")
	require.Equal(t, first, second, "the same error shape has one fingerprint")

	other := Fingerprint(KindCompile, "cannot assign to foo")
	require.NotEqual(t, first, other)
	require.NotEqual(t, first, Fingerprint(KindTest, "undefined: foo"), "the kind is part of the shape")
}

func TestNormaliseForFingerprint(t *testing.T) {
	t.Parallel()

	normalised := normaliseForFingerprint("undefined: myVariable42 at line 12")
	require.Contains(t, normalised, "<id>")
	require.Contains(t, normalised, "<n>")
}

func TestCompare(t *testing.T) {
	t.Parallel()

	previous := []Failure{
		{Fingerprint: "a", Message: "first"},
		{Fingerprint: "b", Message: "second"},
	}
	current := []Failure{
		{Fingerprint: "b", Message: "second"},
		{Fingerprint: "c", Message: "third"},
	}
	added, resolved, persistent := Compare(previous, current)
	require.Len(t, added, 1)
	require.Equal(t, "c", added[0].Fingerprint)
	require.Len(t, resolved, 1)
	require.Equal(t, "a", resolved[0].Fingerprint)
	require.Len(t, persistent, 1)
	require.Equal(t, "b", persistent[0].Fingerprint)
}

func TestReportRegression(t *testing.T) {
	t.Parallel()

	require.False(t, Report{}.Regression())
	require.False(t, Report{Resolved: []Failure{{Fingerprint: "a"}}}.Regression(), "fixing something is not a regression")
	require.True(t, Report{New: []Failure{{Fingerprint: "a"}}}.Regression())
}

func TestPreExistingFromBaseline(t *testing.T) {
	t.Parallel()

	baseline := []Failure{{Fingerprint: "old"}}
	current := []Failure{{Fingerprint: "old"}, {Fingerprint: "new"}}
	preExisting, introduced := PreExistingFromBaseline(baseline, current)
	require.Len(t, preExisting, 1)
	require.Len(t, introduced, 1)
	require.Equal(t, "new", introduced[0].Fingerprint)
}

func TestSortFailures(t *testing.T) {
	t.Parallel()

	failures := []Failure{
		{File: "b.go", Line: 1},
		{File: "a.go", Line: 9},
		{File: "a.go", Line: 2},
	}
	SortFailures(failures)
	require.Equal(t, "a.go", failures[0].File)
	require.Equal(t, 2, failures[0].Line)
	require.Equal(t, "b.go", failures[2].File)
}

func TestDetectCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"go.mod":         "go build ./...",
		"Cargo.toml":     "cargo check --all-targets",
		"package.json":   "npm run --silent build",
		"pyproject.toml": "python -m pytest -q",
		"Makefile":       "make",
	}
	for marker, want := range cases {
		exists := func(name string) bool { return name == marker }
		command, ok := DetectCommand(exists)
		require.True(t, ok, "should detect %s", marker)
		require.Equal(t, want, command.Run)
		require.Contains(t, command.Reason, marker)
	}
}

func TestDetectCommandNothingFound(t *testing.T) {
	t.Parallel()

	none := func(string) bool { return false }
	_, ok := DetectCommand(none)
	require.False(t, ok, "inventing a command is worse than reporting none")

	_, ok = DetectCommand(nil)
	require.False(t, ok)
}

func TestDetectCommandPrefersPrimary(t *testing.T) {
	t.Parallel()

	// A repository with both go.mod and a Makefile is a Go project.
	exists := func(name string) bool { return name == "go.mod" || name == "Makefile" }
	command, ok := DetectCommand(exists)
	require.True(t, ok)
	require.Equal(t, "go", command.Kind)
}

func TestIsValidationCommand(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"go build ./...",
		"go test ./internal/agent/",
		"cargo check",
		"npm run build",
		"pytest -q",
		"npx tsc --noEmit",
		"golangci-lint run",
		"make test",
	} {
		require.True(t, IsValidationCommand(command), "should be a check: %q", command)
	}
	for _, command := range []string{
		"ls -la",
		"git status",
		"cat file.go",
		"rm -rf build",
		"echo hello",
	} {
		require.False(t, IsValidationCommand(command), "should not be a check: %q", command)
	}
}

func TestExtractHandlesEmptyOutput(t *testing.T) {
	t.Parallel()

	require.Empty(t, Extract(""))
	require.Empty(t, Extract(strings.Repeat("\n", 10)))
}
