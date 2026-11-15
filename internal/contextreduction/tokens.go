// Package contextreduction shrinks what Crush re-sends to the model every
// turn. It is the engine behind the context-reduction options: a token
// estimator shared by every decision, per-message accounting, tool-result
// digesting with addressable stubs, read-pointer stubs for unchanged files,
// conversation compaction (summary plus a recent tail), provably lossless
// prose de-duplication, and a content-addressed digest memo.
//
// The package is deliberately free of I/O beyond the digest directory and of
// provider knowledge: callers hand it messages and a ceiling, and it returns
// what should be sent. Every stage is a no-op on work that does not need it,
// so enabling it cannot reflow a prompt that already fits.
package contextreduction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// BytesPerToken is the divisor used by the cheap estimator. It matches the
// provider fallback accounting already used for usage reporting, so the
// numbers in logs and the numbers decisions are made on agree.
const BytesPerToken = 4

// EstimateTokens returns the approximate token count of s. It is deliberately
// cheap: exact tokenizers differ per provider, and a shared, consistent
// estimate is what matters for budgeting.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + BytesPerToken - 1) / BytesPerToken
}

// EstimateTokensRunes estimates tokens from a rune count, for callers that do
// not have the string.
func EstimateTokensRunes(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + BytesPerToken - 1) / BytesPerToken
}

// TruncateToTokens cuts s so that it estimates at most maxTokens, without
// splitting a UTF-8 sequence. It reports whether anything was cut.
func TruncateToTokens(s string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		return "", s != ""
	}
	maxBytes := maxTokens * BytesPerToken
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// HeadTail returns up to headTokens from the start and tailTokens from the
// end of s, joined by a marker that says what was elided. It is used for
// bodies that must stay recognisable (a diff, a log) while losing their
// middle.
func HeadTail(s string, headTokens, tailTokens int) string {
	total := EstimateTokens(s)
	if total <= headTokens+tailTokens {
		return s
	}
	headBytes := headTokens * BytesPerToken
	tailBytes := tailTokens * BytesPerToken
	if headBytes < 0 {
		headBytes = 0
	}
	if tailBytes < 0 {
		tailBytes = 0
	}
	if headBytes+tailBytes >= len(s) {
		return s
	}
	head := s[:headBytes]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tailStart := len(s) - tailBytes
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	tail := s[tailStart:]
	elided := total - headTokens - tailTokens
	if elided < 1 {
		elided = 1
	}
	return fmt.Sprintf("%s\n\n… [%d tokens elided] …\n\n%s", head, elided, tail)
}

// DigestHash returns a short content hash used in stubs and memo keys. The
// hash is over the exact bytes, so a body that changed by one byte changes
// every stub that refers to it.
func DigestHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// ChainHash combines a previous chain hash with new material, so a digest
// memo can be keyed on the whole absorbed prefix instead of a single message.
// An empty previous hash starts a chain.
func ChainHash(previous, material string) string {
	h := sha256.New()
	h.Write([]byte(previous))
	h.Write([]byte{0})
	h.Write([]byte(material))
	return hex.EncodeToString(h.Sum(nil))
}

// OneLine collapses whitespace so a multi-line body can be named in a stub
// without breaking the surrounding prompt structure.
func OneLine(s string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	if maxRunes <= 0 || utf8.RuneCountInString(collapsed) <= maxRunes {
		return collapsed
	}
	runes := []rune(collapsed)
	return string(runes[:maxRunes]) + "…"
}

// PathsIn returns the whitespace-delimited tokens of s that look like file
// paths. It is used to name what a digested result was about, so the model
// knows which file it must re-read if it needs the body.
func PathsIn(s string, max int) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, field := range strings.Fields(s) {
		token := strings.Trim(field, "\"'`()[]{}<>,;:")
		if !looksLikePath(token) {
			continue
		}
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// isFilename reports whether token is shaped like a file name: only the
// characters a name may carry, at least one letter, and no spaces.
func isFilename(token string) bool {
	hasLetter := false
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return hasLetter
}

// looksLikePath reports whether token is plausibly a path or a filename. It is
// intentionally conservative: bare words, flags and URLs are rejected so a stub
// never claims a file that was never mentioned.
func looksLikePath(token string) bool {
	if len(token) < 3 || len(token) > 400 {
		return false
	}
	if strings.Contains(token, "://") || strings.HasPrefix(token, "-") {
		return false
	}
	if strings.HasPrefix(token, ".") && !strings.Contains(token, "/") {
		// A dotfile is a path; the dot is what marks it as one.
		return isFilename(token)
	}
	return strings.Contains(token, "/") || strings.Contains(token, ".")
}
