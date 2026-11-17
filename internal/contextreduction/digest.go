package contextreduction

import (
	"fmt"
	"strings"

	"charm.land/fantasy"
)

// Default stub geometry. A digested body keeps enough of both ends that the
// model can recognise what it was and whether it still matters, and drops the
// middle, which is where the bulk lives.
const (
	// Stubs keep a slice, not the whole body: enough to recognise the result
	// and decide whether to ask for the rest, which is what a model needs to
	// act. The body stays recoverable through the tool that produced it.
	//
	// defaultStubHeadTokens is how much of a digested body survives at its
	// start.
	defaultStubHeadTokens = 160
	// defaultStubTailTokens is how much of a digested body survives at its
	// end. Errors and failures usually appear there.
	defaultStubTailTokens = 80
	// defaultKeepRecentMessages is how many trailing messages are never
	// digested. The live round must stay intact or the model cannot edit what
	// it just read.
	defaultKeepRecentMessages = 2
)

// DigestOptions configures tool-result digesting.
type DigestOptions struct {
	// MinTokens is the size below which a result is left alone. A stub that
	// costs more than the body it replaces is pure loss.
	MinTokens int
	// HeadTokens and TailTokens bound what a digested body keeps.
	HeadTokens int
	TailTokens int
	// KeepRecentMessages is the number of trailing messages that are never
	// digested, so the live round is always complete.
	KeepRecentMessages int
	// ProtectErrors keeps failed results verbatim.
	ProtectErrors bool
	// ToolCaps raises (or lowers) MinTokens for individual tools, keyed by
	// tool name. A listing tool deserves a smaller cap than a build log:
	// its output is structurally cheap to re-derive, while build output is
	// the evidence a fix is based on.
	ToolCaps map[string]int
}

// minTokensFor returns the size threshold that applies to one tool.
func (o DigestOptions) minTokensFor(toolName string) int {
	if cap, ok := o.ToolCaps[toolName]; ok && cap > 0 {
		return cap
	}
	return o.MinTokens
}

// WithDefaults fills unset fields.
func (o DigestOptions) WithDefaults() DigestOptions {
	if o.MinTokens <= 0 {
		o.MinTokens = 1024
	}
	if o.HeadTokens <= 0 {
		o.HeadTokens = defaultStubHeadTokens
	}
	if o.TailTokens <= 0 {
		o.TailTokens = defaultStubTailTokens
	}
	if o.KeepRecentMessages <= 0 {
		o.KeepRecentMessages = defaultKeepRecentMessages
	}
	return o
}

// DigestStats reports what digesting did, so the stage is verifiable from a
// log line instead of from a vibe.
type DigestStats struct {
	// Digested is how many tool results were replaced by an aged stub.
	Digested int
	// Deduplicated is how many were replaced because the exact same body was
	// still present later in the conversation.
	Deduplicated int
	// Skipped counts results left alone, keyed by reason.
	Skipped map[string]int
	// TokensBefore and TokensAfter are tool-result totals across every
	// message, in estimated tokens.
	TokensBefore int
	TokensAfter  int
}

// Saved returns the tokens digesting removed.
func (s DigestStats) Saved() int { return s.TokensBefore - s.TokensAfter }

// resultRef locates one tool result inside the message slice and records what
// the decision needs to know about it.
type resultRef struct {
	msg     int
	callID  string
	text    string
	hash    string
	tokens  int
	isError bool
	// replaceable is false for results whose payload is not text (media) or
	// whose payload is an error, which must keep its exact wording. A
	// non-replaceable result is never counted as digested, so the stats can
	// never claim a saving that did not happen.
	replaceable bool
}

// DigestToolResults replaces aged tool-result bodies with addressable stubs.
// It never mutates its input: a new slice is returned and messages that need
// no change are copied by value.
//
// Two things are digested. A body that is merely old keeps its head and tail
// plus the paths it mentioned, so the model can tell what it was and re-read
// the part it needs. A body whose exact text is still present in a *later*
// message is replaced entirely by a pointer, because the newer copy is already
// in the prompt.
func DigestToolResults(messages []fantasy.Message, opts DigestOptions) ([]fantasy.Message, DigestStats) {
	opts = opts.WithDefaults()
	stats := DigestStats{Skipped: make(map[string]int)}

	toolNames := toolNamesByCallID(messages)
	refs := collectResults(messages)
	for _, ref := range refs {
		stats.TokensBefore += ref.tokens
	}

	// A body is duplicated when the identical text is still present later in
	// the conversation. The newest occurrence is the one that stays.
	newestIndex := make(map[string]int, len(refs))
	for _, ref := range refs {
		if later, ok := newestIndex[ref.hash]; !ok || ref.msg > later {
			newestIndex[ref.hash] = ref.msg
		}
	}

	agedBefore := len(messages) - opts.KeepRecentMessages
	replacements := make(map[string]string, len(refs))
	for _, ref := range refs {
		name := toolNames[ref.callID]
		switch {
		case !ref.replaceable:
			stats.Skipped["not text"]++
			stats.TokensAfter += ref.tokens
			continue
		case ref.isError && opts.ProtectErrors:
			stats.Skipped["protected error"]++
			stats.TokensAfter += ref.tokens
			continue
		case ref.tokens < opts.minTokensFor(name):
			stats.Skipped["below minimum"]++
			stats.TokensAfter += ref.tokens
			continue
		case ref.msg >= agedBefore:
			stats.Skipped["recent"]++
			stats.TokensAfter += ref.tokens
			continue
		}
		if later := newestIndex[ref.hash]; later > ref.msg {
			stub := duplicateStub(name, ref.tokens, ref.hash)
			replacements[ref.callID] = stub
			stats.Deduplicated++
			stats.TokensAfter += EstimateTokens(stub)
			continue
		}
		stub := digestedStub(name, ref.text, ref.hash, opts)
		replacements[ref.callID] = stub
		stats.Digested++
		stats.TokensAfter += EstimateTokens(stub)
	}

	if len(replacements) == 0 {
		return messages, stats
	}
	out := make([]fantasy.Message, len(messages))
	copy(out, messages)
	for i := range out {
		var rewritten []fantasy.MessagePart
		for _, part := range out[i].Content {
			tr, ok := asToolResult(part)
			if !ok {
				continue
			}
			stub, replace := replacements[tr.ToolCallID]
			if !replace {
				continue
			}
			if rewritten == nil {
				rewritten = make([]fantasy.MessagePart, len(out[i].Content))
				copy(rewritten, out[i].Content)
			}
			for j, existing := range rewritten {
				if e, ok := asToolResult(existing); ok && e.ToolCallID == tr.ToolCallID {
					rewritten[j] = withToolResultText(e, stub)
					break
				}
			}
		}
		if rewritten != nil {
			out[i].Content = rewritten
		}
	}
	return out, stats
}

// collectResults gathers every textual tool result in order.
func collectResults(messages []fantasy.Message) []resultRef {
	var refs []resultRef
	for i, msg := range messages {
		for _, part := range msg.Content {
			tr, ok := asToolResult(part)
			if !ok {
				continue
			}
			text, isError := toolResultText(tr)
			tokens := EstimateTokens(text)
			refs = append(refs, resultRef{
				msg:         i,
				callID:      tr.ToolCallID,
				text:        text,
				hash:        DigestHash(text),
				tokens:      tokens,
				isError:     isError,
				replaceable: isTextResult(tr),
			})
		}
	}
	return refs
}

// toolNamesByCallID maps a tool call ID to the tool's name using the
// assistant messages that requested it.
func toolNamesByCallID(messages []fantasy.Message) map[string]string {
	names := make(map[string]string)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.ToolCallPart:
				if p.ToolCallID != "" && p.ToolName != "" {
					names[p.ToolCallID] = p.ToolName
				}
			case *fantasy.ToolCallPart:
				if p != nil && p.ToolCallID != "" && p.ToolName != "" {
					names[p.ToolCallID] = p.ToolName
				}
			}
		}
	}
	return names
}

func asToolResult(part fantasy.MessagePart) (fantasy.ToolResultPart, bool) {
	switch p := part.(type) {
	case fantasy.ToolResultPart:
		return p, true
	case *fantasy.ToolResultPart:
		if p == nil {
			return fantasy.ToolResultPart{}, false
		}
		return *p, true
	default:
		return fantasy.ToolResultPart{}, false
	}
}

// toolResultText returns the textual payload of a result and whether it is an
// error.
func toolResultText(part fantasy.ToolResultPart) (string, bool) {
	switch out := part.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		return out.Text, false
	case *fantasy.ToolResultOutputContentText:
		if out == nil {
			return "", false
		}
		return out.Text, false
	case fantasy.ToolResultOutputContentError:
		if out.Error == nil {
			return "", true
		}
		return out.Error.Error(), true
	case *fantasy.ToolResultOutputContentError:
		if out == nil || out.Error == nil {
			return "", true
		}
		return out.Error.Error(), true
	default:
		return "", false
	}
}

// isTextResult reports whether a result's payload can be replaced by a stub.
// Media and error results cannot: one is bytes, the other keeps its exact
// wording.
func isTextResult(part fantasy.ToolResultPart) bool {
	switch part.Output.(type) {
	case fantasy.ToolResultOutputContentText, *fantasy.ToolResultOutputContentText:
		return true
	default:
		return false
	}
}

// withToolResultText returns a copy of part whose text output is replaced.
// Errors and media are returned unchanged: an error body must keep its exact
// wording and a media payload cannot be expressed as text.
func withToolResultText(part fantasy.ToolResultPart, text string) fantasy.ToolResultPart {
	switch part.Output.(type) {
	case fantasy.ToolResultOutputContentText, *fantasy.ToolResultOutputContentText:
		part.Output = fantasy.ToolResultOutputContentText{Text: text}
	}
	return part
}

// digestedStub renders the replacement for an aged body: a header naming the
// tool, its size and hash, the paths it mentioned, then the head and tail.
func digestedStub(toolName, body, hash string, opts DigestOptions) string {
	// Fold back-to-back repeats first: a body that is mostly one line
	// repeated becomes a few lines and a count before the head/tail slice is
	// taken, so the slice covers distinct information rather than the same
	// line many times.
	if folded, ok := CollapseRepeatedLines(body, 3); ok {
		foldedTokens := EstimateTokens(folded)
		if foldedTokens < EstimateTokens(body) {
			body = folded
		}
	}
	tokens := EstimateTokens(body)
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s result digested to fit the context window: %d tokens, sha256:%s]", toolLabel(toolName), tokens, hash)
	if paths := PathsIn(body, 6); len(paths) > 0 {
		fmt.Fprintf(&sb, "\nMentions: %s", strings.Join(paths, ", "))
	}
	sb.WriteString("\n\n")
	sb.WriteString(HeadTail(body, opts.HeadTokens, opts.TailTokens))
	return sb.String()
}

// duplicateStub renders the replacement for a body whose identical text is
// still present later in the conversation.
func duplicateStub(toolName string, tokens int, hash string) string {
	return fmt.Sprintf("[%s result repeated: identical output is still in this conversation later (%d tokens, sha256:%s). Use that copy instead of re-running the call.]", toolLabel(toolName), tokens, hash)
}

func toolLabel(name string) string {
	if name == "" {
		return "tool"
	}
	return name
}
