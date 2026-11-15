package contextreduction

import (
	"fmt"

	"charm.land/fantasy"
)

// PointerOptions configures read-pointer stubs.
type PointerOptions struct {
	// MinTokens is the smallest body worth replacing with a pointer. A short
	// file costs less to re-send than the pointer does to explain.
	MinTokens int
	// KeepRecentMessages protects the live round, exactly like digesting.
	KeepRecentMessages int
}

// WithDefaults fills unset fields.
func (o PointerOptions) WithDefaults() PointerOptions {
	if o.MinTokens <= 0 {
		o.MinTokens = 256
	}
	if o.KeepRecentMessages <= 0 {
		o.KeepRecentMessages = defaultKeepRecentMessages
	}
	return o
}

// PointerStats reports what stubbing did.
type PointerStats struct {
	// Stubbed is how many tool results were replaced by a pointer.
	Stubbed int
	// Skipped counts results left alone, keyed by reason.
	Skipped map[string]int
	// TokensBefore and TokensAfter are tool-result totals across every
	// message, in estimated tokens.
	TokensBefore int
	TokensAfter  int
}

// Saved returns the tokens stubbing removed.
func (s PointerStats) Saved() int { return s.TokensBefore - s.TokensAfter }

// SupersededReadStubs replaces every earlier copy of a file body that is read
// again later in the conversation with a pointer to the newest copy.
//
// The newest reading is the only one any later edit can be based on, so the
// older copies are pure duplication: keeping them costs prompt space and, worse,
// invites the model to edit a version that no longer exists. This mirrors the
// "superseded by <path> at round N" behaviour of long-running assistants.
func SupersededReadStubs(messages []fantasy.Message, opts PointerOptions) ([]fantasy.Message, PointerStats) {
	opts = opts.WithDefaults()
	stats := PointerStats{Skipped: make(map[string]int)}

	refs := collectResults(messages)
	for _, ref := range refs {
		stats.TokensBefore += ref.tokens
	}
	if len(refs) == 0 {
		return messages, stats
	}

	// A read result is named after the path it mentions. Two results that
	// mention the same single path are two readings of the same file.
	// The tool behind each call is looked up once, not per comparison.
	names := toolNamesByCallID(messages)
	newest := make(map[string]int, len(refs))
	for _, ref := range refs {
		name := readTarget(ref, names)
		if name == "" {
			continue
		}
		if later, ok := newest[name]; !ok || ref.msg > later {
			newest[name] = ref.msg
		}
	}

	agedBefore := len(messages) - opts.KeepRecentMessages
	replacements := make(map[string]string, len(refs))
	for _, ref := range refs {
		target := readTarget(ref, names)
		switch {
		case target == "":
			stats.Skipped["not a read"]++
			stats.TokensAfter += ref.tokens
			continue
		case ref.tokens < opts.MinTokens:
			stats.Skipped["below minimum"]++
			stats.TokensAfter += ref.tokens
			continue
		case ref.msg >= agedBefore:
			stats.Skipped["recent"]++
			stats.TokensAfter += ref.tokens
			continue
		case newest[target] == ref.msg:
			stats.Skipped["newest reading"]++
			stats.TokensAfter += ref.tokens
			continue
		}
		stub := supersededReadStub(target, ref.tokens, ref.hash)
		replacements[ref.callID] = stub
		stats.Stubbed++
		stats.TokensAfter += EstimateTokens(stub)
	}

	if len(replacements) == 0 {
		return messages, stats
	}
	return rewriteResults(messages, replacements), stats
}

// readTarget returns the path a read result was about, or "" when the result
// did not come from a file-reading tool.
func readTarget(ref resultRef, toolNames map[string]string) string {
	if ref.isError {
		return ""
	}
	if !isReadTool(toolNames[ref.callID]) {
		return ""
	}
	paths := PathsIn(ref.text, 4)
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

// isReadTool reports whether a tool only reads a file. Anything else may
// produce output that is not derived from the file's bytes, and must never be
// stubbed as a superseded reading.
func isReadTool(name string) bool {
	switch name {
	case "view", "read", "cat", "head", "tail", "read_file":
		return true
	default:
		return false
	}
}

func supersededReadStub(target string, tokens int, hash string) string {
	return fmt.Sprintf("[Earlier view of %s superseded: a newer reading of this file is later in this conversation (%d tokens, sha256:%s). Work from the newer copy; this one is only the reading still in context.]", target, tokens, hash)
}

// rewriteResults returns a copy of messages with the named tool results
// replaced. Messages without a replacement are shared by value.
func rewriteResults(messages []fantasy.Message, replacements map[string]string) []fantasy.Message {
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
	return out
}
