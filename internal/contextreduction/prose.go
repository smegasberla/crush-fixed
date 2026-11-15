package contextreduction

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CollapseResult reports what prose de-duplication did.
type CollapseResult struct {
	// Text is the cleaned prose.
	Text string
	// RemovedSentences is how many sentences were dropped.
	RemovedSentences int
	// RemovedRunes is how many characters were dropped.
	RemovedRunes int
}

// minSentenceRunes is the shortest sentence worth collapsing. Below it a
// repeated sentence is emphasis ("No. No.") rather than a ramble.
const minSentenceRunes = 12

// CollapseRepeatedProse removes a sentence only when it adds no information
// over the one before it: every content word it carries is already in its
// predecessor. That test is the whole point. A paraphrase that introduces a new
// word is left alone, because choosing between synonyms is not a text-cleaning
// operation and would silently change what the model said.
//
// The function is idempotent and a no-op on prose with no such sentence.
func CollapseRepeatedProse(text string) CollapseResult {
	result := CollapseResult{Text: text}
	if text == "" {
		return result
	}
	sentences := splitSentences(text)
	if len(sentences) < 2 {
		return result
	}

	var kept []string
	for _, sentence := range sentences {
		if len(kept) > 0 && isLosslessRepeat(kept[len(kept)-1], sentence) {
			result.RemovedSentences++
			result.RemovedRunes += utf8.RuneCountInString(sentence)
			continue
		}
		kept = append(kept, sentence)
	}
	if result.RemovedSentences == 0 {
		return result
	}
	result.Text = joinSentences(kept)
	return result
}

// isLosslessRepeat reports whether candidate repeats previous without adding a
// content word.
func isLosslessRepeat(previous, candidate string) bool {
	prevWords := contentWords(previous)
	candWords := contentWords(candidate)
	if len(candWords) == 0 {
		return false
	}
	if utf8.RuneCountInString(strings.TrimSpace(candidate)) < minSentenceRunes {
		return false
	}
	for word := range candWords {
		if _, ok := prevWords[word]; !ok {
			return false
		}
	}
	return true
}

// contentWords returns the lowercased words of a sentence with stop words and
// pure punctuation removed.
func contentWords(sentence string) map[string]struct{} {
	words := make(map[string]struct{})
	for _, field := range strings.FieldsFunc(sentence, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		lower := strings.ToLower(field)
		if len(lower) < 3 || stopWords[lower] {
			continue
		}
		words[lower] = struct{}{}
	}
	return words
}

// stopWords are the words that carry no information, so a sentence repeating
// only those adds nothing.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "any": true, "can": true,
	"had": true, "her": true, "was": true, "one": true, "our": true,
	"out": true, "his": true, "has": true, "him": true, "how": true,
	"its": true, "may": true, "new": true, "now": true, "old": true,
	"see": true, "two": true, "way": true, "who": true, "boy": true,
	"did": true, "she": true, "use": true, "this": true,
	"that": true, "these": true, "those": true, "with": true, "from": true,
	"there": true, "here": true, "then": true, "than": true, "them": true,
	"they": true, "will": true, "would": true, "could": true, "should": true,
	"have": true, "been": true, "were": true, "what": true, "when": true,
	"which": true, "into": true, "only": true, "also": true, "just": true,
	"some": true, "such": true, "very": true, "more": true, "most": true,
	"much": true, "many": true, "like": true, "does": true, "doing": true,
}

// splitSentences splits prose at sentence terminators while keeping the
// terminator with its sentence. Abbreviations are not split: a period followed
// by a lowercase letter continues the same sentence.
func splitSentences(text string) []string {
	var sentences []string
	start := 0
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if !isSentenceEnd(runes[i]) {
			continue
		}
		// Consume a run of terminators and closing quotes.
		j := i + 1
		for j < len(runes) && (isSentenceEnd(runes[j]) || isClosing(runes[j])) {
			j++
		}
		// An initialism keeps its periods ("U.S. dollar", "e.g. this"): the
		// word before the period is a single letter, or the whole token is a
		// known abbreviation.
		if isInitialism(runes, i) {
			i = j - 1
			continue
		}
		// A lowercase next word means the period ended an abbreviation.
		if k := nextNonSpace(runes, j); k < len(runes) && unicode.IsLower(runes[k]) {
			i = j - 1
			continue
		}
		sentences = append(sentences, string(runes[start:j]))
		start = j
		i = j - 1
	}
	if start < len(runes) {
		sentences = append(sentences, string(runes[start:]))
	}
	return sentences
}

// isInitialism reports whether the word that ends at runes[end] is an
// initialism or a common abbreviation, so the period there does not end a
// sentence.
func isInitialism(runes []rune, end int) bool {
	start := end
	for start > 0 && (unicode.IsLetter(runes[start-1]) || runes[start-1] == '.') {
		start--
	}
	word := strings.ToLower(string(runes[start:end]))
	if word == "" {
		return false
	}
	if len([]rune(word)) == 1 {
		return true
	}
	switch word {
	case "e.g", "i.e", "u.s", "a.m", "p.m", "vs", "etc", "dr", "mr", "mrs", "ms",
		"prof", "sr", "jr", "st", "no", "fig", "approx", "dept", "est", "inc",
		"ltd", "co", "cf", "al":
		return true
	}
	return false
}

// nextNonSpace returns the index of the first rune that is not whitespace at or
// after i, or len(runes) when there is none.
func nextNonSpace(runes []rune, i int) int {
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	return i
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	}
	return false
}

func isClosing(r rune) bool {
	switch r {
	case '"', '\'', ')', ']', '»', '”', '’':
		return true
	}
	return false
}

// joinSentences reassembles sentences preserving a single separating space
// where the original had one.
func joinSentences(sentences []string) string {
	var sb strings.Builder
	for i, sentence := range sentences {
		if i > 0 {
			prev := sentences[i-1]
			if !strings.HasSuffix(prev, " ") && !strings.HasPrefix(sentence, " ") && needsSpace(prev, sentence) {
				sb.WriteString(" ")
			}
		}
		sb.WriteString(sentence)
	}
	return sb.String()
}

// needsSpace reports whether two adjacent sentences need a space between them.
func needsSpace(prev, next string) bool {
	prevRunes := []rune(prev)
	nextRunes := []rune(next)
	if len(prevRunes) == 0 || len(nextRunes) == 0 {
		return false
	}
	return !unicode.IsSpace(prevRunes[len(prevRunes)-1]) && !unicode.IsSpace(nextRunes[0])
}
