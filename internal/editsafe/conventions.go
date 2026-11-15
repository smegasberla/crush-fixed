package editsafe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// LineEnding names how a file terminates its lines.
type LineEnding string

const (
	// LineEndingLF is the Unix convention.
	LineEndingLF LineEnding = "lf"
	// LineEndingCRLF is the Windows convention.
	LineEndingCRLF LineEnding = "crlf"
	// LineEndingMixed means the file uses both, which is preserved as-is
	// rather than normalised: rewriting every line of a file the user did not
	// ask to reformat is a bigger change than any edit.
	LineEndingMixed LineEnding = "mixed"
	// LineEndingNone means the file has no line terminators.
	LineEndingNone LineEnding = "none"
)

// Conventions is everything about a file that an edit must not disturb.
type Conventions struct {
	// LineEnding is the dominant terminator.
	LineEnding LineEnding
	// BOM is the byte-order mark the file starts with, if any.
	BOM []byte
	// FinalNewline is whether the file ended with a terminator.
	FinalNewline bool
	// CRLFCount and LFCount are the terminator counts the classification is
	// based on.
	CRLFCount int
	LFCount   int
}

// DetectConventions inspects raw bytes and reports the conventions an edit must
// preserve.
func DetectConventions(raw []byte) Conventions {
	var conv Conventions
	if bom, ok := detectBOM(raw); ok {
		conv.BOM = bom
	}
	body := raw[len(conv.BOM):]
	for i, b := range body {
		if b != '\n' {
			continue
		}
		conv.LFCount++
		if i > 0 && body[i-1] == '\r' {
			conv.CRLFCount++
		}
	}
	switch {
	case conv.LFCount == 0:
		conv.LineEnding = LineEndingNone
	case conv.CRLFCount == 0:
		conv.LineEnding = LineEndingLF
	case conv.CRLFCount == conv.LFCount:
		conv.LineEnding = LineEndingCRLF
	default:
		conv.LineEnding = LineEndingMixed
	}
	conv.FinalNewline = len(body) > 0 && body[len(body)-1] == '\n'
	return conv
}

// detectBOM returns the byte-order mark raw starts with. Only the marks an
// editor can legitimately emit are recognised; anything else is content.
func detectBOM(raw []byte) ([]byte, bool) {
	marks := [][]byte{
		{0xEF, 0xBB, 0xBF}, // UTF-8
		{0xFF, 0xFE},       // UTF-16 LE
		{0xFE, 0xFF},       // UTF-16 BE
	}
	for _, mark := range marks {
		if bytes.HasPrefix(raw, mark) {
			return mark, true
		}
	}
	return nil, false
}

// Terminator returns the string this file uses between lines.
func (c Conventions) Terminator() string {
	switch c.LineEnding {
	case LineEndingCRLF:
		return "\r\n"
	case LineEndingMixed:
		// A mixed file is edited with LF, which is what every tool that
		// wrote the mixed file already does; the mixed regions survive
		// because only the edited span is rewritten.
		return "\n"
	default:
		return "\n"
	}
}

// Describe renders the conventions for a log line or a tool response.
func (c Conventions) Describe() string {
	var sb strings.Builder
	sb.WriteString("line_endings: " + string(c.LineEnding))
	if len(c.BOM) > 0 {
		sb.WriteString(", bom: yes")
	}
	if c.FinalNewline {
		sb.WriteString(", final_newline: yes")
	} else {
		sb.WriteString(", final_newline: no")
	}
	return sb.String()
}

// ToLF normalises CRLF to LF so the engine can work in one convention and
// restore the file's own when writing.
func ToLF(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// ApplyConventions turns engine-internal text (LF) back into the byte sequence
// this file should have: its BOM, its terminators, and its final-newline
// convention.
func (c Conventions) Apply(text string) []byte {
	normalised := ToLF(text)
	if c.LineEnding == LineEndingCRLF {
		normalised = strings.ReplaceAll(normalised, "\n", "\r\n")
	}
	// Restore the final newline convention. An editor that dropped it would
	// make every later diff include the last line.
	if c.FinalNewline && !strings.HasSuffix(normalised, "\n") {
		normalised += c.Terminator()
	}
	if !c.FinalNewline {
		normalised = strings.TrimSuffix(normalised, "\n")
		normalised = strings.TrimSuffix(normalised, "\r")
	}
	out := make([]byte, 0, len(c.BOM)+len(normalised))
	out = append(out, c.BOM...)
	out = append(out, normalised...)
	return out
}

// Hash returns the hex SHA-256 of raw.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// IsBinary reports whether raw looks like a binary file rather than text. A NUL
// byte in the first 8 KiB is the standard test: no text format produces one,
// and every binary one does.
func IsBinary(raw []byte) bool {
	probe := raw
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// IsText reports whether raw is valid UTF-8 and not binary. A file that fails
// this is refused rather than edited: rewriting it would destroy bytes the
// engine cannot see.
func IsText(raw []byte) bool {
	if IsBinary(raw) {
		return false
	}
	body := raw
	if _, ok := detectBOM(raw); ok {
		body = raw[3:]
	}
	return utf8.Valid(body)
}

// CountLines returns how many lines text has, counting a final unterminated
// line.
func CountLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

// DiffStats counts the added and removed lines between two texts. It compares
// line by line, which is what a reader of the summary expects; it is not a
// minimal diff.
func DiffStats(oldText, newText string) (additions, removals int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return 0, 0
	}
	inCommon := longestCommonPrefix(oldLines, newLines)
	suffix := longestCommonSuffix(oldLines[inCommon:], newLines[inCommon:])
	removals = len(oldLines) - inCommon - suffix
	additions = len(newLines) - inCommon - suffix
	if removals < 0 {
		removals = 0
	}
	if additions < 0 {
		additions = 0
	}
	return additions, removals
}

// splitLines splits text into lines without their terminators.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func longestCommonPrefix(a, b []string) int {
	limit := min(len(a), len(b))
	for i := range limit {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func longestCommonSuffix(a, b []string) int {
	limit := min(len(a), len(b))
	for i := range limit {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return limit
}
