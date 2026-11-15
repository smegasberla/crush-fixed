package editsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Op is one change to one file. Exactly one addressing mode is used:
//
//   - OldText/NewText replaces a unique occurrence of OldText.
//   - Line (+ optional EndLine) with Expect replaces or deletes an addressed
//     region, where Expect proves the address is not stale.
//
// Delete removes the addressed region instead of replacing it; it requires
// Expect for the reason a line address needs one: a deletion of the wrong line
// leaves nothing to notice it by.
type Op struct {
	// ID names the op, so a refusal can point at it. Two ops with the same ID
	// are refused.
	ID string
	// OldText and NewText are the find/replace pair.
	OldText string
	NewText string
	// Line is the 1-based line the op addresses.
	Line int
	// EndLine is the 1-based inclusive end of the addressed region. Zero means
	// the single addressed line.
	EndLine int
	// Expect is text that must be present at the addressed line or region.
	Expect string
	// Delete removes the addressed region.
	Delete bool
	// ReplaceAll widens a find/replace to every occurrence, explicitly.
	ReplaceAll bool
}

// validate checks one op's own shape, before it is compared with the file.
func (o Op) validate(path string, seenIDs map[string]struct{}) error {
	if o.ID != "" {
		if _, dup := seenIDs[o.ID]; dup {
			return newError(CodeOpIDConflict, path,
				fmt.Sprintf("two ops in this batch share the id %q; ids must be unique so a refusal can name one", o.ID))
		}
		seenIDs[o.ID] = struct{}{}
	}
	addressed := o.Line > 0
	textual := o.OldText != ""
	switch {
	case addressed && textual:
		return newError(CodeRejected, path,
			"an op is addressed either by text or by line, not both; split it into two ops")
	case !addressed && !textual:
		return newError(CodeNoTargetText, path,
			"an op names neither old_text nor line, so there is nothing to change")
	case addressed && o.Expect == "":
		return newError(CodeMissingAnchor, path,
			fmt.Sprintf("line %d needs an expect token: without one a stale line number silently edits the wrong line", o.Line),
			"send expect as the exact current text of that line")
	case addressed && o.EndLine > 0 && o.EndLine < o.Line:
		return newError(CodeRejected, path,
			fmt.Sprintf("end_line %d is before line %d", o.EndLine, o.Line))
	}
	if o.Delete && o.NewText != "" {
		return newError(CodeRejected, path,
			"an op resolves to a deletion that also carries replacement text; send either a delete or a replacement")
	}
	return nil
}

// region returns the 1-based inclusive line span the op addresses. It is only
// meaningful for addressed ops.
func (o Op) region() (start, end int) {
	start = o.Line
	end = o.EndLine
	if end == 0 {
		end = o.Line
	}
	return start, end
}

// Transaction is one file's pending change: a snapshot, a set of ops, and the
// result of applying them.
type Transaction struct {
	// Path is the file the transaction edits.
	Path string
	// raw is the snapshot every op is validated against, so an op cannot see
	// a half-applied file.
	raw []byte
	// conv is what the write must preserve.
	conv Conventions
	// text is the snapshot normalised to LF, which is what ops act on.
	text string
	// ops are the changes, in the order they were supplied.
	ops []Op
}

// Load reads a file into a transaction. It refuses binary and non-UTF-8 files,
// because an edit there would destroy bytes the engine cannot represent.
func Load(path string) (*Transaction, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBytes(path, raw)
}

// LoadBytes builds a transaction from bytes already in memory, for callers that
// hold the snapshot (a test, or a caller that just read the file).
func LoadBytes(path string, raw []byte) (*Transaction, error) {
	if IsBinary(raw) {
		return nil, newError(CodeBinaryFile, path,
			"the file contains binary data, so it cannot be edited as text",
			"write the whole file deliberately instead of editing it")
	}
	if !IsText(raw) {
		return nil, newError(CodeEncodingDamage, path,
			"the file is not valid UTF-8, so an edit could not round-trip its bytes",
			"convert the file to UTF-8 first, or write the whole file deliberately")
	}
	conv := DetectConventions(raw)
	body := raw[len(conv.BOM):]
	return &Transaction{
		Path: path,
		raw:  raw,
		conv: conv,
		text: ToLF(string(body)),
	}, nil
}

// Conventions returns the file's conventions.
func (t *Transaction) Conventions() Conventions { return t.conv }

// Text returns the snapshot normalised to LF.
func (t *Transaction) Text() string { return t.text }

// Add appends an op without validating it. Validation happens in Commit, so a
// batch can be built in any order.
func (t *Transaction) Add(ops ...Op) {
	t.ops = append(t.ops, ops...)
}

// Ops returns the ops in the batch.
func (t *Transaction) Ops() []Op { return t.ops }

// Resolved is one op resolved to a concrete span in the snapshot.
type resolved struct {
	op Op
	// start and end are 1-based inclusive line numbers.
	start int
	end   int
	// replacement is the text that replaces those lines (empty for a delete).
	replacement string
}

// Preview validates and applies the batch without writing anything, so a caller
// can show the real diff before asking for permission. It reports the same
// refusals Commit would.
func (t *Transaction) Preview() (Result, error) {
	if len(t.ops) == 0 {
		return Result{}, newError(CodeNoOpResolved, t.Path, "the batch contains no ops")
	}
	seenIDs := make(map[string]struct{}, len(t.ops))
	for _, op := range t.ops {
		if err := op.validate(t.Path, seenIDs); err != nil {
			return Result{}, err
		}
	}
	resolvedOps, err := t.resolve()
	if err != nil {
		return Result{}, err
	}
	newText, err := t.apply(resolvedOps)
	if err != nil {
		return Result{}, err
	}
	if newText == t.text {
		return Result{}, newError(CodeNoOpResolved, t.Path,
			"the batch would change nothing: the text is already as requested")
	}
	additions, removals := DiffStats(t.text, newText)
	return Result{
		Path:       t.Path,
		OldContent: t.text,
		NewContent: newText,
		Hash:       Hash(t.conv.Apply(newText)),
		Additions:  additions,
		Removals:   removals,
		Applied:    len(resolvedOps),
	}, nil
}

// Commit validates and applies the whole batch against the snapshot, then
// writes the file atomically and verifies the committed bytes.
//
// Nothing is written unless every op validates: a batch that fails halfway is
// worse than a batch that fails, because the caller cannot tell which half.
func (t *Transaction) Commit() (Result, error) {
	if len(t.ops) == 0 {
		return Result{}, newError(CodeNoOpResolved, t.Path, "the batch contains no ops")
	}

	seenIDs := make(map[string]struct{}, len(t.ops))
	for _, op := range t.ops {
		if err := op.validate(t.Path, seenIDs); err != nil {
			return Result{}, err
		}
	}

	resolvedOps, err := t.resolve()
	if err != nil {
		return Result{}, err
	}

	newText, err := t.apply(resolvedOps)
	if err != nil {
		return Result{}, err
	}
	if newText == t.text {
		return Result{}, newError(CodeNoOpResolved, t.Path,
			"the batch would change nothing: the text is already as requested",
			"Nothing was changed and the file is byte-identical.")
	}

	out := t.conv.Apply(newText)
	additions, removals := DiffStats(t.text, newText)
	result := Result{
		Path:       t.Path,
		OldContent: t.text,
		NewContent: newText,
		Hash:       Hash(out),
		Additions:  additions,
		Removals:   removals,
		Applied:    len(resolvedOps),
	}
	// Restoring a convention the edit would have broken is reported, not
	// silently done.
	if t.conv.FinalNewline && !strings.HasSuffix(newText, "\n") && strings.HasSuffix(t.text, "\n") {
		result.Codes = append(result.Codes, CodeConventionRestored)
	}

	if err := writeAtomic(t.Path, out); err != nil {
		return Result{}, newError(CodeNotCommitted, t.Path,
			fmt.Sprintf("the write did not land: %s", err),
			"The bytes on disk are the ones from before the edit.")
	}

	// Read the file back: the hash of what is on disk is the only proof the
	// edit landed as intended.
	committed, readErr := os.ReadFile(t.Path)
	if readErr != nil {
		return Result{}, newError(CodeNotCommitted, t.Path,
			fmt.Sprintf("the file could not be read back after the write: %s", readErr))
	}
	result.ReadBackHash = Hash(committed)
	if result.ReadBackHash != result.Hash {
		return Result{}, newError(CodeNotCommitted, t.Path,
			"the bytes on disk are NOT what this edit committed",
			fmt.Sprintf("intended sha256:%s, found sha256:%s", shortHash(result.Hash), shortHash(result.ReadBackHash)),
			"another process wrote the file during the edit; re-read it and retry")
	}
	result.Codes = append(result.Codes, CodeSuccess)
	return result, nil
}

// resolve turns every op into a concrete span, all against the same snapshot.
func (t *Transaction) resolve() ([]resolved, error) {
	lines := splitLines(t.text)
	out := make([]resolved, 0, len(t.ops))
	for _, op := range t.ops {
		var r resolved
		var err error
		if op.Line > 0 {
			r, err = t.resolveAddressed(op, lines)
		} else {
			r, err = t.resolveTextual(op, lines)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// Two ops on the same region are contradictory: whichever ran first would
	// invalidate the other's address.
	if err := checkOverlaps(t.Path, out); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveAddressed resolves a line-addressed op, verifying the expect token.
func (t *Transaction) resolveAddressed(op Op, lines []string) (resolved, error) {
	start, end := op.region()
	if start < 1 || start > len(lines) {
		return resolved{}, newError(CodeAnchorGone, t.Path,
			fmt.Sprintf("line %d is outside the file, which has %d line(s)", start, len(lines)),
			t.expectLocationsHint(lines, op.Expect))
	}
	if end > len(lines) {
		return resolved{}, newError(CodeAnchorGone, t.Path,
			fmt.Sprintf("line %d is outside the file, which has %d line(s)", end, len(lines)))
	}
	region := strings.Join(lines[start-1:end], "\n")
	if !regionContainsExpect(region, op.Expect) {
		// The address moved. Naming where the anchor actually is turns a
		// refusal into a retry that works.
		if relocated := findLineContaining(lines, op.Expect); relocated > 0 {
			return resolved{}, newError(CodeAnchorGone, t.Path,
				fmt.Sprintf("relocated: addressed line %d, expect token is line %d", start, relocated),
				fmt.Sprintf("re-send this op with line %d", relocated))
		}
		return resolved{}, newError(CodeAnchorGone, t.Path,
			fmt.Sprintf("the expect token %q does not appear at line %d", truncateText(op.Expect, 60), start),
			t.expectLocationsHint(lines, op.Expect))
	}
	return resolved{
		op:          op,
		start:       start,
		end:         end,
		replacement: op.NewText,
	}, nil
}

// resolveTextual resolves a find/replace op, requiring a unique match unless the
// op explicitly widens to all.
func (t *Transaction) resolveTextual(op Op, lines []string) (resolved, error) {
	target := ToLF(op.OldText)
	replacement := ToLF(op.NewText)
	if !strings.Contains(t.text, target) {
		return resolved{}, newError(CodeAnchorGone, t.Path,
			"old_text not found in the file; check whitespace and line breaks",
			t.nearMissHint(lines, target))
	}
	if !op.ReplaceAll {
		if count := strings.Count(t.text, target); count > 1 {
			return resolved{}, newError(CodeMultiMatch, t.Path,
				fmt.Sprintf("old_text matches %d places and one was expected", count),
				t.lineListHint(lines, target),
				"add surrounding context to make the match unique, or set replace_all when every match is intended")
		}
	}
	start := indexToLine(t.text, strings.Index(t.text, target))
	end := start + strings.Count(target, "\n")
	return resolved{
		op:          op,
		start:       start,
		end:         end,
		replacement: replacement,
	}, nil
}

// apply rewrites the snapshot with every resolved span, from the bottom up so
// earlier spans keep their line numbers.
func (t *Transaction) apply(ops []resolved) (string, error) {
	lines := splitLines(t.text)
	ordered := make([]resolved, len(ops))
	copy(ordered, ops)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start > ordered[j].start })

	// Textual ops are applied by string replacement first, because a
	// find/replace is expressed in text, not lines, and widening one to all
	// occurrences has no line span.
	var textual []resolved
	var addressed []resolved
	for _, op := range ordered {
		if op.op.OldText != "" {
			textual = append(textual, op)
			continue
		}
		addressed = append(addressed, op)
	}

	result := t.text
	for _, op := range textual {
		target := ToLF(op.op.OldText)
		if op.op.ReplaceAll {
			result = strings.ReplaceAll(result, target, op.replacement)
			continue
		}
		result = strings.Replace(result, target, op.replacement, 1)
	}

	lines = splitLines(result)
	for _, op := range addressed {
		if op.start < 1 || op.end > len(lines) {
			// A textual op shrank the file under an addressed one. This is a
			// contradictory batch, and saying so is better than writing a
			// wrong line.
			return "", newError(CodeTargetRegionChanged, t.Path,
				fmt.Sprintf("the op at line %d no longer fits: an earlier op changed the file's line count", op.start),
				"apply textual and addressed ops in separate batches")
		}
		insert := []string{}
		if op.replacement != "" {
			insert = strings.Split(op.replacement, "\n")
		}
		before := append([]string{}, lines[:op.start-1]...)
		after := append([]string{}, lines[op.end:]...)
		merged := append(before, insert...)
		merged = append(merged, after...)
		lines = merged
	}

	out := strings.Join(lines, "\n")
	if strings.HasSuffix(result, "\n") && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

// checkOverlaps refuses a batch whose ops touch the same region.
func checkOverlaps(path string, ops []resolved) error {
	sorted := make([]resolved, len(ops))
	copy(sorted, ops)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	for i := 1; i < len(sorted); i++ {
		previous, current := sorted[i-1], sorted[i]
		if previous.op.OldText != "" || current.op.OldText != "" {
			// Textual ops are matched on distinct text, so their spans are
			// informational; overlapping text matches are caught by the
			// multi-match rule.
			continue
		}
		if current.start <= previous.end {
			return newError(CodeRangesOverlap, path,
				fmt.Sprintf("ops %s and %s address overlapping lines (%d-%d and %d-%d)",
					label(previous.op), label(current.op), previous.start, previous.end, current.start, current.end),
				"merge them into one op, or send them in separate batches")
		}
	}
	return nil
}

// label names an op for a message, falling back to its address.
func label(op Op) string {
	if op.ID != "" {
		return op.ID
	}
	return fmt.Sprintf("line %d", op.Line)
}

// regionContainsExpect reports whether expect is present in the addressed
// region. A multi-line region accepts an expect spanning its lines.
func regionContainsExpect(region, expect string) bool {
	expect = strings.TrimSpace(ToLF(expect))
	if expect == "" {
		return false
	}
	return strings.Contains(ToLF(region), expect)
}

// findLineContaining returns the 1-based line that contains text, or 0.
func findLineContaining(lines []string, text string) int {
	needle := strings.TrimSpace(ToLF(text))
	if needle == "" {
		return 0
	}
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// expectLocationsHint says where the expect token actually appears, which is the
// one thing that makes a stale address fixable.
func (t *Transaction) expectLocationsHint(lines []string, expect string) string {
	needle := strings.TrimSpace(ToLF(expect))
	if needle == "" {
		return "send expect as the exact current text of the addressed line"
	}
	var found []string
	for i, line := range lines {
		if strings.Contains(line, needle) {
			found = append(found, fmt.Sprintf("%d", i+1))
			if len(found) >= 5 {
				break
			}
		}
	}
	if len(found) == 0 {
		return fmt.Sprintf("the expect token %q does not appear anywhere in the file", truncateText(needle, 60))
	}
	return "the expect token appears at line(s) " + strings.Join(found, ", ")
}

// lineListHint lists where a multiply-matched target appears.
func (t *Transaction) lineListHint(lines []string, target string) string {
	var found []string
	for i, line := range lines {
		if strings.Contains(line, strings.SplitN(target, "\n", 2)[0]) {
			found = append(found, fmt.Sprintf("%d", i+1))
			if len(found) >= 8 {
				break
			}
		}
	}
	if len(found) == 0 {
		return ""
	}
	return "the target appears on line(s) " + strings.Join(found, ", ")
}

// nearMissHint names the closest line when a target is not found at all. A
// whitespace difference is the usual cause, and showing the actual text is what
// lets a caller fix it without another read.
func (t *Transaction) nearMissHint(lines []string, target string) string {
	firstLine := strings.SplitN(strings.TrimSpace(target), "\n", 2)[0]
	compact := compactSpace(firstLine)
	if compact == "" {
		return ""
	}
	for i, line := range lines {
		if compactSpace(line) == compact {
			return fmt.Sprintf("line %d has similar text: %q", i+1, truncateText(strings.TrimSpace(line), 120))
		}
	}
	return ""
}

// compactSpace collapses runs of whitespace, so a match that differs only in
// indentation is still recognisable.
func compactSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// indexToLine converts a byte offset into a 1-based line number.
func indexToLine(text string, index int) int {
	if index <= 0 {
		return 1
	}
	return strings.Count(text[:index], "\n") + 1
}

// truncateText shortens text for a message.
func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// writeAtomic writes data to path through a temporary file in the same
// directory and renames it into place, so a crash leaves either the old file or
// the new one, never a half-written one. The temporary file is created in the
// target directory because a cross-device rename is not atomic and fails on
// Windows when the temporary directory is on another volume.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".edit-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
