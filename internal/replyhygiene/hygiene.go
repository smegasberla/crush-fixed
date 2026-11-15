// Package replyhygiene cleans what a model actually emits before it reaches the
// user. It never changes the reasoning behind an answer; it removes the debris
// that low-bit and heavily-prompted models wrap around it: chat-template
// envelopes, half-written fences, stray parameter tags, repeated prose, and
// imitated tool syntax.
//
// Every stage is independent and idempotent, and every stage has a guard that
// keeps it from deleting real content. The guards matter more than the cleaning:
// a repair that eats the answer is worse than the debris it removed.
package replyhygiene

import (
	"regexp"
	"strings"
)

// Result is the cleaned reply.
type Result struct {
	// Text is the cleaned reply.
	Text string
	// Applied lists the stages that changed something, in order.
	Applied []string
	// RemovedRunes is how much was dropped.
	RemovedRunes int
}

// Changed reports whether any stage did anything.
func (r Result) Changed() bool { return len(r.Applied) > 0 }

// envelopePatterns strip a chat-template wrapper the model echoed back. The
// wrappers are matched only when they enclose the whole reply, so documentation
// that *mentions* a template is left alone.
var envelopePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)^\s*<\|?assistant\|?>\s*\n?(.*?)\n?\s*<\|?end\|?>\s*$`),
	regexp.MustCompile(`(?s)^\s*<\|?start_header_id\|?>\s*assistant\s*<\|?end_header_id\|?>\s*\n?(.*?)\s*$`),
	regexp.MustCompile(`(?s)^\s*\[/?INST\]\s*\n?(.*?)\s*$`),
	regexp.MustCompile(`(?s)^\s*<\|?im_start\|?>\s*assistant\s*\n(.*?)\n?\s*<\|?im_end\|?>\s*$`),
}

// Clean runs every stage in order.
func Clean(text string) Result {
	result := &Result{Text: text}
	result.Text = stripEnvelope(result, result.Text)
	result.Text = unwrapFences(result, result.Text)
	result.Text = stripParamDebris(result, result.Text)
	result.Text = collapseRepeats(result, result.Text)
	result.Text = stripImitatedToolCards(result, result.Text)
	result.Text = trimTrailingDebris(result, result.Text)
	return *result
}

// record notes that a stage changed the text.
func (r *Result) record(stage string, before, after string) {
	if before == after {
		return
	}
	r.Applied = append(r.Applied, stage)
	if len(before) > len(after) {
		r.RemovedRunes += len(before) - len(after)
	}
}

// stripEnvelope removes a template wrapper around the whole reply.
func stripEnvelope(result *Result, text string) string {
	for _, pattern := range envelopePatterns {
		match := pattern.FindStringSubmatch(text)
		if match == nil || len(match) < 2 {
			continue
		}
		inner := strings.TrimSpace(match[1])
		if inner == "" {
			continue
		}
		result.record("envelope", text, inner)
		return inner
	}
	return text
}

// fenceMarker matches a fenced code block opener or closer.
var fenceMarker = regexp.MustCompile("^\\s*(```+|~~~+)")

// unwrapFences removes a fence that wraps the entire reply, which a model emits
// when it treats its answer as a quoted block. A fence around *part* of the reply
// is content and is never touched.
func unwrapFences(result *Result, text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return text
	}
	first := fenceMarker.FindStringSubmatch(lines[0])
	if first == nil {
		return text
	}
	last := -1
	for i := len(lines) - 1; i > 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if fenceMarker.MatchString(lines[i]) {
			last = i
		}
		break
	}
	if last <= 1 {
		return text
	}
	inner := strings.Join(lines[1:last], "\n")
	if strings.TrimSpace(inner) == "" {
		return text
	}
	result.record("fence", text, inner)
	return inner
}

// paramDebrisPatterns match the scaffolding a model emits when it imitates a
// tool call's XML form instead of its native one.
var paramDebrisPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)</?(?:parameter|param)[^>]*>`),
	regexp.MustCompile(`(?s)<\|?tool_call\|?>`),
	regexp.MustCompile(`(?m)^\s*<\|?tool\|?>\s*$`),
	regexp.MustCompile(`(?s)<environment_details>.*?</environment_details>`),
	regexp.MustCompile(`(?m)^\s*\|\s*\{\s*"tool"\s*:`),
}

// stripParamDebris removes tool-call scaffolding that is not part of the answer.
func stripParamDebris(result *Result, text string) string {
	out := text
	for _, pattern := range paramDebrisPatterns {
		out = pattern.ReplaceAllString(out, "")
	}
	out = strings.TrimSpace(out)
	result.record("param_debris", text, out)
	return out
}

// sentencePattern matches a sentence long enough that an exact repeat is not
// emphasis. It stops at a blank line, so a repeat is only collapsed inside one
// paragraph where it really is a stutter.
var sentencePattern = regexp.MustCompile(`[^.!?\n]{40,}[.!?]`)

// collapseRepeats removes an immediately repeated identical sentence. Only
// exact repeats collapse: a paraphrase is the model's choice of words, not
// debris.
func collapseRepeats(result *Result, text string) string {
	out := text
	// Collapse until stable, so three copies become one rather than two.
	for {
		next := collapseRepeatsOnce(out)
		if next == out {
			break
		}
		out = next
	}
	if out != text {
		result.record("repeat", text, out)
	}
	return out
}

// collapseRepeatsOnce removes one immediate repeat, or returns the text
// unchanged.
func collapseRepeatsOnce(text string) string {
	matches := sentencePattern.FindAllStringIndex(text, -1)
	for i := 1; i < len(matches); i++ {
		previous := strings.TrimSpace(text[matches[i-1][0]:matches[i-1][1]])
		current := strings.TrimSpace(text[matches[i][0]:matches[i][1]])
		if previous != current {
			continue
		}
		if strings.TrimSpace(text[matches[i-1][1]:matches[i][0]]) != "" {
			continue
		}
		return strings.TrimSpace(text[:matches[i][0]] + text[matches[i][1]:])
	}
	return text
}

// imitatedToolCard matches a pseudo tool call the model wrote as prose.
var imitatedToolCard = regexp.MustCompile(`(?m)^\s*(?:›|>)\s*[a-z_]+\s*\{.*\}\s*$`)

// stripImitatedToolCards removes lines that imitate the app's own tool-card
// rendering. The real cards are rendered by the app, not by the model, so a line
// the model wrote in that shape is noise. The check requires the card shape at
// the start of a line, so prose that mentions a tool name is kept.
func stripImitatedToolCards(result *Result, text string) string {
	out := imitatedToolCard.ReplaceAllString(text, "")
	out = strings.TrimSpace(out)
	if out == "" {
		// The reply was nothing but cards: keep the original rather than
		// returning an empty answer.
		return text
	}
	result.record("imitated_cards", text, out)
	return out
}

// trailingDebris matches a line of punctuation or a bare fence at the very end.
var trailingDebris = regexp.MustCompile(`(?s)\n\s*(?:\*{2,}|-{3,}|_{3,}|` + "```" + `)\s*$`)

// trimTrailingDebris removes a closing rule or a bare fence left at the end.
func trimTrailingDebris(result *Result, text string) string {
	out := trailingDebris.ReplaceAllString(text, "")
	out = strings.TrimRight(out, " \t\n")
	out = strings.TrimRight(out, "`~")
	out = strings.TrimRight(out, " \t\n")
	if strings.TrimSpace(out) == "" {
		return text
	}
	result.record("trailing_debris", text, out)
	return out
}

// AuditLeaks reports whether the reply still contains a template token. A leak
// that survives cleaning is a real problem: the user will see it.
func AuditLeaks(text string) []string {
	tokens := []string{
		"<|im_end|>", "<|im_start|>", "<|endoftext|>", "<|eot_id|>",
		"</s>", "[INST]", "[/INST]", "<|start_header_id|>",
		"<|end▁of▁sentence|>",
	}
	var found []string
	for _, token := range tokens {
		if strings.Contains(text, token) {
			found = append(found, token)
		}
	}
	return found
}

// StopSequences are the hard stops every provider should be given. They are
// exported so a caller can install them rather than rediscover them.
var StopSequences = []string{
	"<|im_end|>",
	"<|eot_id|>",
	"<|endoftext|>",
	"</s>",
	"<|end▁of▁sentence|>",
	"<|start_header_id|>",
	"[INST]",
	"<｜end▁of▁thinking｜>",
	"</reasoning>",
}
