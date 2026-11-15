package editsafe

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Limits bounds what one edit may do to a file. A change that crosses a bound is
// refused rather than executed: the usual cause is a truncated replacement or a
// tool that "helpfully" rewrote the whole file, and both are worse than a
// refusal.
type Limits struct {
	// MaxDiffPercent is the largest share of the file's lines an edit may
	// change. Zero disables the bound.
	MaxDiffPercent int
	// MaxUnexpectedFiles is how many files one turn may touch without any of
	// them being the file the request named. Zero disables the bound.
	MaxUnexpectedFiles int
	// CollapsePercent is the share of the file's lines that must survive.
	// A result below it is treated as a truncated write. Zero disables.
	CollapsePercent int
}

// minLinesForDiffBound is the smallest file the percentage bound applies to.
// Below it the bound would describe arithmetic rather than risk.
const minLinesForDiffBound = 10

// DefaultLimits returns the bounds that catch the common accidents without
// blocking a legitimate rewrite.
func DefaultLimits() Limits {
	return Limits{
		MaxDiffPercent:     80,
		MaxUnexpectedFiles: 12,
		CollapsePercent:    20,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxDiffPercent <= 0 {
		l.MaxDiffPercent = d.MaxDiffPercent
	}
	if l.MaxUnexpectedFiles <= 0 {
		l.MaxUnexpectedFiles = d.MaxUnexpectedFiles
	}
	if l.CollapsePercent <= 0 {
		l.CollapsePercent = d.CollapsePercent
	}
	return l
}

// CheckDiff applies the size bounds to a pending change.
//
// The risk codes exist because the two failures look identical from the tool's
// side: a big edit may be exactly right, or it may be a replacement that lost
// most of the file. The engine cannot tell, so it refuses and says which bound
// tripped, and the caller either sends a smaller edit or states that the whole
// file really is being rewritten.
func CheckDiff(path string, oldText, newText string, limits Limits) error {
	limits = limits.withDefaults()
	oldLines := CountLines(oldText)
	newLines := CountLines(newText)
	additions, removals := DiffStats(oldText, newText)
	changed := additions + removals

	// A collapse is checked before the percentage bound: a replacement that
	// drops most of the file also changes most of it, and the collapse is the
	// more specific description of what went wrong.
	if oldLines >= 20 {
		retained := newLines * 100 / oldLines
		if retained < limits.CollapsePercent {
			return newError(CodeFileCollapsed, path,
				fmt.Sprintf("the result keeps %d%% of the file's %d lines (%d left)", retained, oldLines, newLines),
				"a replacement this small is usually a truncated write; send the whole file deliberately if that is the intent")
		}
	}

	// The percentage bound is meaningless on a tiny file: changing one line of
	// a two-line file is 100%, and it is also the only way to edit it at all.
	if oldLines >= minLinesForDiffBound && limits.MaxDiffPercent < 100 {
		// A line replaced in place counts as both removed and added, so the
		// raw ratio can exceed 100%. Reporting "200% of the file" would be
		// nonsense, so the ratio is capped where it saturates.
		changedPercent := changed * 100 / oldLines
		if changedPercent > 100 {
			changedPercent = 100
		}
		if changedPercent > limits.MaxDiffPercent {
			return newError(CodeDiffExplosion, path,
				fmt.Sprintf("the edit changes %d%% of the file (%d of %d lines)", changedPercent, changed, oldLines),
				fmt.Sprintf("the configured bound is %d%%; split the change, or state that the whole file is being rewritten", limits.MaxDiffPercent))
		}
	}
	return nil
}

// Ownership records which files a turn has already edited. It makes an edit
// to a file the turn does not own a deliberate act rather than an accident:
// without it, a turn can wander across a repository editing files nothing asked
// about.
type Ownership struct {
	// owned is the set of absolute paths this turn has edited.
	owned map[string]struct{}
	// unexpected counts files edited that no request named.
	unexpected int
	// expected is the set of paths the request named.
	expected map[string]struct{}
	limits   Limits
}

// NewOwnership returns a tracker for one turn.
func NewOwnership(expectedPaths []string, limits Limits) *Ownership {
	expected := make(map[string]struct{}, len(expectedPaths))
	for _, path := range expectedPaths {
		if path == "" {
			continue
		}
		expected[filepath.Clean(path)] = struct{}{}
	}
	return &Ownership{
		owned:    make(map[string]struct{}),
		expected: expected,
		limits:   limits.withDefaults(),
	}
}

// Claim records that the turn is about to edit path and reports whether the
// claim is allowed.
//
// A file the request named is always allowed. A file it did not name is allowed
// until MaxUnexpectedFiles such files have been touched, which is the point at
// which the turn has stopped doing the task and started exploring the
// repository with writes.
func (o *Ownership) Claim(path string) error {
	if o == nil {
		return nil
	}
	clean := filepath.Clean(path)
	if _, ok := o.owned[clean]; ok {
		return nil
	}
	if _, named := o.expected[clean]; !named {
		if o.limits.MaxUnexpectedFiles > 0 && o.unexpected >= o.limits.MaxUnexpectedFiles {
			return newError(CodeRiskDetected, path,
				fmt.Sprintf("this turn has already edited %d files the request did not name", o.unexpected),
				"finish the files the request named, or say plainly which additional file is needed and why")
		}
		o.unexpected++
	}
	o.owned[clean] = struct{}{}
	return nil
}

// Owned reports whether the turn has edited path.
func (o *Ownership) Owned(path string) bool {
	if o == nil {
		return false
	}
	_, ok := o.owned[filepath.Clean(path)]
	return ok
}

// Paths returns the files the turn has edited.
func (o *Ownership) Paths() []string {
	if o == nil {
		return nil
	}
	paths := make([]string, 0, len(o.owned))
	for path := range o.owned {
		paths = append(paths, path)
	}
	return paths
}

// Unexpected returns how many files the turn edited that no request named.
func (o *Ownership) Unexpected() int {
	if o == nil {
		return 0
	}
	return o.unexpected
}

// redirectPattern recognises a single-file redirect: command > file.
var redirectPattern = regexp.MustCompile(`(?:^|[;&|]\s*)[^;&|<>]*[^2>]>\s*(?:"([^"]+)"|'([^']+)'|([^\s;|&<>]+))\s*(?:$|[;&|])`)

// ShellWrite describes a shell command that edits one file in place.
type ShellWrite struct {
	// Path is the file the command edits.
	Path string
	// Command is the original command.
	Command string
	// Kind names the mechanism: "sed", "perl" or "redirect".
	Kind string
}

// DetectShellWrite reports whether a shell command edits exactly one file in
// place, which is the case where routing it to the edit engine is correct.
//
// The distinction matters because the edit engine gives the caller
// transactional semantics, an anchor check and a read-back hash, while a
// `sed -i` gives none of those and reports success even when it matched
// nothing.
func DetectShellWrite(command string) (ShellWrite, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ShellWrite{}, false
	}
	// A command that loops, globs or pipes its target is not a single-file
	// edit: rewriting it into one edit call would change what it does.
	for _, multi := range []string{" for ", " while ", " xargs ", "-exec ", "$(", "`", "find ", "&&", "||"} {
		if strings.Contains(trimmed, multi) {
			return ShellWrite{}, false
		}
	}

	if write, ok := detectInPlaceEditor(trimmed, "sed"); ok {
		return write, true
	}
	if write, ok := detectInPlaceEditor(trimmed, "perl"); ok {
		return write, true
	}
	if match := redirectPattern.FindStringSubmatch(trimmed); match != nil {
		for _, group := range match[1:] {
			if group == "" {
				continue
			}
			if strings.HasPrefix(group, "/dev/") || strings.HasPrefix(group, "-") {
				return ShellWrite{}, false
			}
			return ShellWrite{Path: group, Command: trimmed, Kind: "redirect"}, true
		}
	}
	return ShellWrite{}, false
}

// detectInPlaceEditor parses a sed or perl invocation that writes its file back
// with a -i flag. It is parsed rather than pattern-matched because the script
// argument sits between the flag and the file, and a regex that guesses which
// argument is which gets it wrong for exactly the quoting people use.
func detectInPlaceEditor(command, tool string) (ShellWrite, bool) {
	tokens := splitShellTokens(command)
	if len(tokens) < 4 || tokens[0] != tool {
		return ShellWrite{}, false
	}
	hasInPlace := false
	index := 1
	var script string
	var operands []string
	for index < len(tokens) {
		token := tokens[index]
		switch {
		case token == "-i" || token == "--in-place" || strings.HasPrefix(token, "-i.") ||
			strings.HasPrefix(token, "--in-place="):
			hasInPlace = true
			index++
			continue
		case looksLikeBundledInPlace(token):
			// perl bundles flags: -pi means -p -i.
			hasInPlace = true
			index++
			continue
		case strings.HasPrefix(token, "-"):
			// A flag. sed's -e and perl's -e take the script as the next
			// argument; every other flag is self-contained or takes an
			// argument that cannot be the file.
			if token == "-e" || token == "-E" {
				if index+1 < len(tokens) {
					script = tokens[index+1]
				}
				index += 2
				continue
			}
			// A bundled perl flag may carry the print flag and the script.
			if tool == "perl" && strings.HasPrefix(token, "-p") && len(token) > 2 {
				script = token[2:]
			}
			index++
			continue
		default:
			operands = append(operands, token)
			index++
		}
	}
	if !hasInPlace {
		return ShellWrite{}, false
	}
	if script == "" && len(operands) > 0 {
		// The first operand is the script when no -e was given.
		script = operands[0]
		operands = operands[1:]
	}
	if script == "" || len(operands) != 1 {
		// Without a script there is nothing to run, and with more than one
		// operand it is not a single-file edit.
		return ShellWrite{}, false
	}
	path := operands[0]
	if strings.HasPrefix(path, "/dev/") || strings.HasPrefix(path, "-") {
		return ShellWrite{}, false
	}
	kind := "sed"
	if tool == "perl" {
		kind = "perl"
	}
	return ShellWrite{Path: path, Command: command, Kind: kind}, true
}

// looksLikeBundledInPlace reports whether a short flag bundle contains the
// in-place flag, as in perl's -pi.
func looksLikeBundledInPlace(token string) bool {
	if !strings.HasPrefix(token, "-") || strings.HasPrefix(token, "--") || len(token) < 3 {
		return false
	}
	return strings.ContainsRune(token[1:], 'i')
}

// splitShellTokens splits a command into arguments, honouring single and double
// quotes so a sed script stays one token.
func splitShellTokens(command string) []string {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
		started bool
	)
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			if started || current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started || current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// ShellRoutingNote is what the model is told when its shell edit was wrong for
// the job. It names the tool the task actually wants and why.
func ShellRoutingNote(path string) string {
	return fmt.Sprintf("[Edit Routing] That round edited %s with a shell command. A localized change to one existing file belongs in the edit tool: it validates the change against one snapshot, keeps the file's line endings and final newline, and reports the hash of what actually landed. "+
		"Commands stay the right tool for what the edit tool does not do: a loop or find -exec over many files, a formatter or generator, a scripted pass, a build, a test. Do not re-send the same shell edit expecting a different answer, and do not re-read the file to find out what happened.", path)
}
