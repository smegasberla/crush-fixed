package contextreduction

import (
	"charm.land/fantasy"
)

// protectedTail is the number of recent messages a planner keeps verbatim.
const protectedTail = 2

// FitTailToBudget bounds the newest messages that a compaction or a ceiling
// drop must keep regardless of size. A transcript whose last two messages are
// large tool results can be 170K even under a 90K budget: no amount of
// dropping older messages fixes that. Only tool results are reduced, and only
// above a per-result cap; text and tool calls are left verbatim.
func FitTailToBudget(messages []fantasy.Message, budget int) ([]fantasy.Message, bool) {
	if budget <= 0 || len(messages) == 0 {
		return messages, false
	}

	start := len(messages) - protectedTail
	if start < 0 {
		start = 0
	}
	perResult := max(512, budget/protectedTail)

	names := toolNamesByCallID(messages)
	refs := collectResults(messages[start:])
	replacements := make(map[string]string, len(refs))
	for _, ref := range refs {
		if !ref.replaceable || ref.tokens <= perResult {
			continue
		}
		replacements[ref.callID] = digestedStub(names[ref.callID], ref.text, ref.hash,
			DigestOptions{MinTokens: perResult}.WithDefaults())
	}
	if len(replacements) == 0 {
		return messages, false
	}

	out := make([]fantasy.Message, len(messages))
	copy(out, messages)
	for i := start; i < len(out); i++ {
		rewritten := false
		for j, part := range out[i].Content {
			tr, ok := asToolResult(part)
			if !ok {
				continue
			}
			stub, replace := replacements[tr.ToolCallID]
			if !replace {
				continue
			}
			if !rewritten {
				out[i].Content = append([]fantasy.MessagePart(nil), out[i].Content...)
				rewritten = true
			}
			out[i].Content[j] = withToolResultText(tr, stub)
		}
	}
	return out, true
}
