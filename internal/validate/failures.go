// Package validate turns build and test output into something a turn can
// remember: each distinct failure gets a fingerprint, a repeated check is
// answered from a cache instead of re-run, and an unknown project's verification
// command is detected from its own files.
//
// The point is to stop re-discovering the same failure. Without this, an agent
// fixes one error, reruns the build, and spends the next three rounds meeting
// errors it has already seen and reported.
package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Failure is one distinct problem a check reported.
type Failure struct {
	// Fingerprint identifies the failure by its shape, not its location, so
	// the same error in a different file is still the same failure.
	Fingerprint string
	// Message is the failure text, trimmed to one line.
	Message string
	// File and Line locate it when the output carries a location.
	File string
	Line int
	// Kind is the class of failure: compile, test, lint, or generic.
	Kind string
}

// Failure kinds.
const (
	KindCompile = "compile"
	KindTest    = "test"
	KindLint    = "lint"
	KindGeneric = "generic"
)

// locationPatterns recognise a file:line prefix, which is how most compilers and
// linters report where a problem is.
var locationPatterns = []*regexp.Regexp{
	// Go: path/file.go:12:5:
	regexp.MustCompile(`^([^\s:]+\.go):(\d+)(?::\d+)?:\s*(.*)$`),
	// Rust / generic: path/file.rs:12:5
	regexp.MustCompile(`^([^\s:]+\.(?:rs|c|cc|cpp|h|hpp|java|kt|py|ts|tsx|js|jsx|rb|php)):(\d+)(?::\d+)?:\s*(.*)$`),
	// TypeScript: file.ts(12,5): error TS1234: message
	regexp.MustCompile(`^([^\s:]+\.(?:ts|tsx|js|jsx))\((\d+),(\d+)\):\s*(.*)$`),
}

// failureLinePatterns recognise a line that reports a problem even without a
// location prefix.
var failureLinePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*(?:error|fatal)\b[: ]\s*(.+)$`),
	regexp.MustCompile(`(?i)^\s*FAILED\s+(.+)$`),
	regexp.MustCompile(`(?i)^\s*--- FAIL:\s*(.+)$`),
	regexp.MustCompile(`(?i)^\s*panic:\s*(.+)$`),
	regexp.MustCompile(`(?i)^\s*\d+\s+(?:error|errors)\b`),
	regexp.MustCompile(`(?i)^\s*E\s+\S+`),
}

// noisePatterns are lines that look like failures but report progress or counts.
var noisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*(?:ok|PASS)\b`),
	regexp.MustCompile(`(?i)^\s*(?:\d+\s+)?(?:test|tests) (?:passed|passing)`),
	regexp.MustCompile(`(?i)^\s*0 (?:errors?|failures?)`),
	regexp.MustCompile(`^\s*$`),
}

// Extract returns the distinct failures in a check's output, in the order they
// first appeared. It is deliberately conservative: a false failure makes a
// caller chase a problem that is not there, which is worse than missing one that
// the raw output still shows.
func Extract(output string) []Failure {
	seen := make(map[string]struct{})
	var failures []Failure
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || isNoise(line) {
			continue
		}
		failure, ok := parseFailure(line)
		if !ok {
			continue
		}
		if _, dup := seen[failure.Fingerprint]; dup {
			continue
		}
		seen[failure.Fingerprint] = struct{}{}
		failures = append(failures, failure)
	}
	return failures
}

// isNoise reports whether a line is progress rather than a problem.
func isNoise(line string) bool {
	for _, pattern := range noisePatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// parseFailure classifies one line.
func parseFailure(line string) (Failure, bool) {
	for _, pattern := range locationPatterns {
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		file := match[1]
		lineNumber := 0
		message := ""
		switch len(match) {
		case 4:
			lineNumber = atoiSafe(match[2])
			message = match[3]
		case 5:
			lineNumber = atoiSafe(match[2])
			message = match[4]
		}
		if message == "" {
			continue
		}
		return Failure{
			Fingerprint: Fingerprint(KindCompile, message),
			Message:     message,
			File:        file,
			Line:        lineNumber,
			Kind:        KindCompile,
		}, true
	}
	for _, pattern := range failureLinePatterns {
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		message := line
		if len(match) > 1 && match[1] != "" {
			message = match[1]
		}
		kind := KindTest
		if strings.Contains(strings.ToLower(line), "panic") || strings.Contains(strings.ToLower(line), "error") {
			kind = KindGeneric
		}
		return Failure{
			Fingerprint: Fingerprint(kind, message),
			Message:     message,
			Kind:        kind,
		}, true
	}
	return Failure{}, false
}

// Fingerprint hashes a failure's shape. Identifiers, numbers and whitespace are
// normalised first, so "undefined: foo" and "undefined: bar" share a fingerprint
// while "undefined: foo" and "cannot assign to foo" do not.
func Fingerprint(kind, message string) string {
	normalised := normaliseForFingerprint(message)
	sum := sha256.Sum256([]byte(kind + "\x00" + normalised))
	return hex.EncodeToString(sum[:8])
}

// normaliseForFingerprint reduces a message to its shape: identifiers become
// placeholders, numbers become digit runs, and whitespace collapses.
func normaliseForFingerprint(message string) string {
	var sb strings.Builder
	inIdent := false
	inNumber := false
	for _, r := range message {
		switch {
		case unicode.IsLetter(r) || r == '_':
			if inNumber {
				sb.WriteString("<n>")
				inNumber = false
			}
			if !inIdent {
				sb.WriteString("<id>")
				inIdent = true
			}
		case unicode.IsDigit(r):
			if inIdent {
				sb.WriteString("")
				inIdent = false
			}
			if !inNumber {
				sb.WriteString("<n>")
				inNumber = true
			}
		case unicode.IsSpace(r):
			inIdent = false
			inNumber = false
			sb.WriteString(" ")
		default:
			inIdent = false
			inNumber = false
			sb.WriteRune(r)
		}
	}
	return strings.TrimSpace(sb.String())
}

// atoiSafe parses an int, returning zero on failure.
func atoiSafe(s string) int {
	value := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return value
		}
		value = value*10 + int(r-'0')
	}
	return value
}

// Report is what one check run produced.
type Report struct {
	// Failures are the distinct problems, in output order.
	Failures []Failure
	// New are the failures this run introduced.
	New []Failure
	// Resolved are the ones the previous run had and this one does not.
	Resolved []Failure
	// Persistent are the ones both runs have.
	Persistent []Failure
	// PreExisting are failures this run saw that predate the change under
	// test, so they are not regressions.
	PreExisting []Failure
	// Cached is true when the report came from the cache rather than a run.
	Cached bool
}

// Regression reports whether the run introduced a failure that was not present
// before. It is the only question that decides whether a change broke something.
func (r Report) Regression() bool {
	return len(r.New) > 0
}

// Compare diffs two runs.
func Compare(previous, current []Failure) (added, resolved, persistent []Failure) {
	previousByFingerprint := make(map[string]Failure, len(previous))
	for _, failure := range previous {
		previousByFingerprint[failure.Fingerprint] = failure
	}
	currentByFingerprint := make(map[string]Failure, len(current))
	for _, failure := range current {
		currentByFingerprint[failure.Fingerprint] = failure
		if _, existed := previousByFingerprint[failure.Fingerprint]; existed {
			persistent = append(persistent, failure)
			continue
		}
		added = append(added, failure)
	}
	for _, failure := range previous {
		if _, stillPresent := currentByFingerprint[failure.Fingerprint]; !stillThere(stillPresent) {
			resolved = append(resolved, failure)
		}
	}
	return added, resolved, persistent
}

// stillThere is a tiny helper so the resolved loop reads as one condition.
func stillThere(present bool) bool { return present }

// PreExistingFromBaseline splits a run's failures into the ones a baseline
// already had and the ones it introduced.
func PreExistingFromBaseline(baseline, current []Failure) (preExisting, introduced []Failure) {
	known := make(map[string]struct{}, len(baseline))
	for _, failure := range baseline {
		known[failure.Fingerprint] = struct{}{}
	}
	for _, failure := range current {
		if _, ok := known[failure.Fingerprint]; ok {
			preExisting = append(preExisting, failure)
			continue
		}
		introduced = append(introduced, failure)
	}
	return preExisting, introduced
}

// SortFailures orders failures by file then line, for stable reporting.
func SortFailures(failures []Failure) {
	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].File == failures[j].File {
			return failures[i].Line < failures[j].Line
		}
		return failures[i].File < failures[j].File
	})
}

// Command is a detected verification command.
type Command struct {
	// Run is the command to execute.
	Run string
	// Kind names the ecosystem: go, cargo, npm, pytest, gradle, dotnet, make.
	Kind string
	// Reason says which file implied it, so a wrong guess is explainable.
	Reason string
}

// Detectors maps a marker file to the check it implies. The order matters:
// the first marker found wins, and it is ordered from most specific to most
// generic so a repository with several build systems reports the primary one.
var Detectors = []struct {
	Marker string
	Kind   string
	Run    string
}{
	{"go.mod", "go", "go build ./..."},
	{"Cargo.toml", "cargo", "cargo check --all-targets"},
	{"pnpm-lock.yaml", "npm", "pnpm -s typecheck"},
	{"yarn.lock", "npm", "yarn -s tsc --noEmit"},
	{"bun.lockb", "npm", "bun run typecheck"},
	{"package.json", "npm", "npm run --silent build"},
	{"pyproject.toml", "pytest", "python -m pytest -q"},
	{"setup.py", "pytest", "python -m pytest -q"},
	{"gradlew", "gradle", "./gradlew build"},
	{"pom.xml", "maven", "mvn -q -DskipTests compile"},
	{"Makefile", "make", "make"},
	{"tsconfig.json", "tsc", "npx --no-install tsc --noEmit"},
}

// DetectCommand finds the verification command a project implies, by looking for
// its own marker files. It returns false when no marker is found, because
// inventing a command is worse than reporting that none could be detected.
func DetectCommand(listing func(name string) bool) (Command, bool) {
	if listing == nil {
		return Command{}, false
	}
	for _, detector := range Detectors {
		if !listing(detector.Marker) {
			continue
		}
		return Command{
			Run:    detector.Run,
			Kind:   detector.Kind,
			Reason: "found " + detector.Marker,
		}, true
	}
	return Command{}, false
}

// IsValidationCommand reports whether a shell command looks like a build, test or
// type check. It is used to tell a check apart from an arbitrary command, so only
// checks feed the failure memory.
func IsValidationCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, marker := range []string{
		"go build", "go test", "go vet", "cargo build", "cargo test",
		"cargo check", "npm test", "npm run build", "npm run check",
		"yarn test", "pnpm test", "pytest", "python -m pytest", "tsc",
		"golangci-lint", "eslint", "ruff", "mypy", "dotnet build",
		"dotnet test", "gradle", "gradlew", "mvn ", "make ",
		"playwright", "vitest", "jest",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
