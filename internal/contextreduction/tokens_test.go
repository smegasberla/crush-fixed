package contextreduction

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	require.Zero(t, EstimateTokens(""))
	require.Equal(t, 1, EstimateTokens("a"))
	require.Equal(t, 1, EstimateTokens("abcd"))
	require.Equal(t, 2, EstimateTokens("abcde"))
	require.Equal(t, 25, EstimateTokens(strings.Repeat("x", 100)))
	require.Zero(t, EstimateTokensRunes(0))
	require.Equal(t, 1, EstimateTokensRunes(3))
}

func TestTruncateToTokens(t *testing.T) {
	t.Parallel()

	short := "hello"
	out, cut := TruncateToTokens(short, 10)
	require.Equal(t, "hello", out)
	require.False(t, cut)

	long := strings.Repeat("a", 100)
	out, cut = TruncateToTokens(long, 5)
	require.True(t, cut)
	require.Len(t, out, 20)

	// A multi-byte rune is never split.
	multibyte := strings.Repeat("è", 40)
	out, cut = TruncateToTokens(multibyte, 3)
	require.True(t, cut)
	require.True(t, utf8.ValidString(out))
	require.NotContains(t, out, "\xff")

	out, cut = TruncateToTokens("something", 0)
	require.Empty(t, out)
	require.True(t, cut)
}

func TestHeadTail(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("A", 400) + "MIDDLE" + strings.Repeat("B", 400)
	out := HeadTail(body, 10, 10)
	require.Contains(t, out, "elided")
	require.True(t, strings.HasPrefix(out, strings.Repeat("A", 40)))
	require.True(t, strings.HasSuffix(out, strings.Repeat("B", 40)))
	require.NotContains(t, out, "MIDDLE")
	require.Less(t, len(out), len(body))

	// A body that fits is returned untouched.
	require.Equal(t, "short", HeadTail("short", 100, 100))
}

func TestDigestHashAndChain(t *testing.T) {
	t.Parallel()

	require.Equal(t, DigestHash("a"), DigestHash("a"))
	require.NotEqual(t, DigestHash("a"), DigestHash("b"))
	require.Len(t, DigestHash("a"), 16)

	first := ChainHash("", "a")
	require.Equal(t, first, ChainHash("", "a"))
	require.NotEqual(t, first, ChainHash("", "b"))
	require.NotEqual(t, first, ChainHash(first, "a"))
	// The separator prevents a previous hash from merging with material.
	require.NotEqual(t, ChainHash("ab", "c"), ChainHash("a", "bc"))
}

func TestOneLine(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a b c", OneLine("a\n  b\tc\n", 0))
	require.Equal(t, "aaaa…", OneLine("aaaa bbbb", 4))
	require.Equal(t, "", OneLine("   ", 10))
}

func TestPathsIn(t *testing.T) {
	t.Parallel()

	body := `edited internal/agent/agent.go and ./internal/config/config.go
plus "Makefile" and https://example.com/a/b.go and --flag
and src/main.rs and .gitignore and "quoted/path.go"`
	paths := PathsIn(body, 8)

	require.Contains(t, paths, "internal/agent/agent.go")
	require.Contains(t, paths, "./internal/config/config.go")
	require.NotContains(t, paths, "Makefile", "a bare word with no extension is not a path")
	require.Contains(t, paths, "src/main.rs")
	require.Contains(t, paths, ".gitignore")
	require.NotContains(t, paths, "--flag")
	for _, p := range paths {
		require.NotContains(t, p, "example.com")
	}

	// The cap is honoured and results are unique.
	limited := PathsIn("a/b.go a/b.go c/d.go e/f.go", 2)
	require.Len(t, limited, 2)
}
