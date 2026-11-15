package editsafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectConventions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		ending   LineEnding
		finalNew bool
		bom      bool
	}{
		{name: "lf with final newline", raw: "a\nb\n", ending: LineEndingLF, finalNew: true},
		{name: "lf without final newline", raw: "a\nb", ending: LineEndingLF},
		{name: "crlf", raw: "a\r\nb\r\n", ending: LineEndingCRLF, finalNew: true},
		{name: "mixed", raw: "a\r\nb\n", ending: LineEndingMixed, finalNew: true},
		{name: "no terminators", raw: "single", ending: LineEndingNone},
		{name: "empty", raw: "", ending: LineEndingNone},
		{name: "utf8 bom", raw: "\xef\xbb\xbfa\n", ending: LineEndingLF, finalNew: true, bom: true},
		{name: "utf16le bom", raw: "\xff\xfea\n", ending: LineEndingLF, finalNew: true, bom: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conv := DetectConventions([]byte(tt.raw))
			require.Equal(t, tt.ending, conv.LineEnding)
			require.Equal(t, tt.finalNew, conv.FinalNewline)
			require.Equal(t, tt.bom, len(conv.BOM) > 0)
		})
	}
}

func TestConventionsDescribe(t *testing.T) {
	t.Parallel()

	conv := DetectConventions([]byte("\xef\xbb\xbfa\r\nb\r\n"))
	description := conv.Describe()
	require.Contains(t, description, "line_endings: crlf")
	require.Contains(t, description, "bom: yes")
	require.Contains(t, description, "final_newline: yes")

	plain := DetectConventions([]byte("a\nb"))
	require.Contains(t, plain.Describe(), "final_newline: no")
}

func TestConventionsTerminator(t *testing.T) {
	t.Parallel()

	require.Equal(t, "\r\n", Conventions{LineEnding: LineEndingCRLF}.Terminator())
	require.Equal(t, "\n", Conventions{LineEnding: LineEndingLF}.Terminator())
	require.Equal(t, "\n", Conventions{LineEnding: LineEndingMixed}.Terminator())
	require.Equal(t, "\n", Conventions{LineEnding: LineEndingNone}.Terminator())
}

func TestConventionsApply(t *testing.T) {
	t.Parallel()

	crlf := DetectConventions([]byte("a\r\nb\r\n"))
	require.Equal(t, "a\r\nb\r\n", string(crlf.Apply("a\nb\n")))

	// A BOM is restored.
	withBOM := DetectConventions([]byte("\xef\xbb\xbfa\n"))
	require.Equal(t, "\xef\xbb\xbfa\n", string(withBOM.Apply("a\n")))

	// A missing final newline is restored.
	noFinal := DetectConventions([]byte("a\nb"))
	require.Equal(t, "a\nb", string(noFinal.Apply("a\nb\n")))
	require.Equal(t, "a\nb", string(noFinal.Apply("a\nb")))

	// A file that ends with a newline keeps one.
	withFinal := DetectConventions([]byte("a\nb\n"))
	require.Equal(t, "a\nb\n", string(withFinal.Apply("a\nb")))
}

func TestToLF(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a\nb", ToLF("a\r\nb"))
	require.Equal(t, "a\nb\nc", ToLF("a\r\nb\r\nc"))
	require.Equal(t, "", ToLF(""))
}

func TestHash(t *testing.T) {
	t.Parallel()

	require.Equal(t, Hash([]byte("a")), Hash([]byte("a")))
	require.NotEqual(t, Hash([]byte("a")), Hash([]byte("b")))
	require.Len(t, Hash(nil), 64)
}

func TestIsBinary(t *testing.T) {
	t.Parallel()

	require.False(t, IsBinary([]byte("plain text\n")))
	require.True(t, IsBinary([]byte("text\x00more")))
	require.False(t, IsBinary(nil))

	// A NUL past the probe window is not detected: the test reads a prefix,
	// which is the standard trade-off.
	long := append([]byte(strings.Repeat("a", 9000)), 0)
	require.False(t, IsBinary(long))
}

func TestIsText(t *testing.T) {
	t.Parallel()

	require.True(t, IsText([]byte("hello\n")))
	require.True(t, IsText([]byte("\xef\xbb\xbfhello\n")))
	require.False(t, IsText([]byte("bad\xff\xfe")))
	require.False(t, IsText([]byte("binary\x00")))
	require.True(t, IsText(nil))
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	require.Zero(t, CountLines(""))
	require.Equal(t, 1, CountLines("one"))
	require.Equal(t, 1, CountLines("one\n"))
	require.Equal(t, 2, CountLines("one\ntwo"))
	require.Equal(t, 3, CountLines("one\ntwo\nthree\n"))
}

func TestDiffStats(t *testing.T) {
	t.Parallel()

	add, rem := DiffStats("", "")
	require.Zero(t, add)
	require.Zero(t, rem)

	add, rem = DiffStats("a\nb\nc\n", "a\nB\nc\n")
	require.Equal(t, 1, add)
	require.Equal(t, 1, rem)

	add, rem = DiffStats("a\n", "a\nb\n")
	require.Equal(t, 1, add)
	require.Zero(t, rem)

	add, rem = DiffStats("a\r\nb\r\n", "a\nb\n")
	require.Zero(t, add, "line endings alone are not a content change")
	require.Zero(t, rem)

	add, rem = DiffStats("a\nb\nc\n", "")
	require.Zero(t, add)
	require.Equal(t, 3, rem)
}

func TestSplitLines(t *testing.T) {
	t.Parallel()

	require.Nil(t, splitLines(""))
	require.Equal(t, []string{"a", "b"}, splitLines("a\nb"))
	require.Equal(t, []string{"a", "b"}, splitLines("a\nb\n"))
	require.Equal(t, []string{"a", "b"}, splitLines("a\r\nb\r\n"))
	require.Equal(t, []string{"a", ""}, splitLines("a\n\n"))
}
