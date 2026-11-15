package contextreduction

import (
	"charm.land/fantasy"
)

// Accounting is the token breakdown of a single message.
type Accounting struct {
	// Text is the tokens of the message's prose (the user question or the
	// assistant's answer).
	Text int
	// Reasoning is the tokens of thinking blocks. Older reasoning is the
	// first thing that should disappear: it is not needed to continue.
	Reasoning int
	// ToolInput is the tokens of tool-call arguments.
	ToolInput int
	// ToolResult is the tokens of tool results, split by call ID so a
	// result can be digested individually.
	ToolResult int
	// ToolResults holds the per-call accounting for tool messages.
	ToolResults map[string]int
	// Files is the tokens of attached media parts.
	Files int
}

// Total is the whole message's estimated size.
func (a Accounting) Total() int {
	return a.Text + a.Reasoning + a.ToolInput + a.ToolResult + a.Files
}

// Budget is the whole prompt's token breakdown. It is what the planner
// decides on: the numbers are recomputed from the compacted message list, so
// the summary is always consistent with what would actually be sent.
type Budget struct {
	// System is the size of the system prompt plus context files.
	System int
	// Messages is the size of everything after the system prompt.
	Messages int
	// Reasoning is the part of Messages spent on thinking blocks.
	Reasoning int
	// ToolResults is the part of Messages spent on tool output.
	ToolResults int
	// ToolInputs is the part of Messages spent on tool arguments.
	ToolInputs int
	// Files is the part of Messages spent on attachments.
	Files int
	// PerMessage holds the accounting of each counted message, in order.
	PerMessage []MessageAccounting
	// Counted is true when the prompt was inspected message by message; it
	// is false for a system-only prompt.
	Counted bool
}

// Total is the whole prompt's estimated size.
func (b Budget) Total() int {
	return b.System + b.Messages
}

// MessageAccounting pairs a message's parts with its size.
type MessageAccounting struct {
	// ReasoningChars is the amount of reasoning text in the message. It is
	// used to prefer dropping reasoning over content.
	ReasoningChars int
	// ToolResultChars is the total tool-result text in the message.
	ToolResultChars int
	// Tokens is the message's estimated size.
	Tokens int
}

// AccountMessage estimates one message.
func AccountMessage(msg fantasy.Message) Accounting {
	var acct Accounting
	if msg.Content == nil {
		return acct
	}
	for _, part := range msg.Content {
		switch p := part.(type) {
		case fantasy.TextPart:
			acct.Text += EstimateTokens(p.Text)
		case *fantasy.TextPart:
			acct.Text += EstimateTokens(p.Text)
		case fantasy.ReasoningPart:
			acct.Reasoning += EstimateTokens(p.Text)
		case *fantasy.ReasoningPart:
			acct.Reasoning += EstimateTokens(p.Text)
		case fantasy.ToolCallPart:
			acct.ToolInput += EstimateTokens(p.ToolName) + EstimateTokens(p.Input)
		case *fantasy.ToolCallPart:
			acct.ToolInput += EstimateTokens(p.ToolName) + EstimateTokens(p.Input)
		case fantasy.ToolResultPart:
			n := AccountToolResult(p)
			acct.ToolResult += n
			if acct.ToolResults == nil {
				acct.ToolResults = make(map[string]int)
			}
			acct.ToolResults[p.ToolCallID] += n
		case *fantasy.ToolResultPart:
			n := AccountToolResult(*p)
			acct.ToolResult += n
			if acct.ToolResults == nil {
				acct.ToolResults = make(map[string]int)
			}
			acct.ToolResults[p.ToolCallID] += n
		case fantasy.FilePart:
			acct.Files += estimateFileTokens(p)
		case *fantasy.FilePart:
			acct.Files += estimateFileTokens(*p)
		}
	}
	return acct
}

// AccountToolResult returns the estimated size of a tool result's output.
func AccountToolResult(part fantasy.ToolResultPart) int {
	switch out := part.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		return EstimateTokens(out.Text)
	case *fantasy.ToolResultOutputContentText:
		return EstimateTokens(out.Text)
	case fantasy.ToolResultOutputContentError:
		if out.Error == nil {
			return 0
		}
		return EstimateTokens(out.Error.Error())
	case *fantasy.ToolResultOutputContentError:
		if out.Error == nil {
			return 0
		}
		return EstimateTokens(out.Error.Error())
	case fantasy.ToolResultOutputContentMedia:
		return EstimateTokens(out.Text) + estimateBase64Tokens(out.Data)
	case *fantasy.ToolResultOutputContentMedia:
		return EstimateTokens(out.Text) + estimateBase64Tokens(out.Data)
	case nil:
		return 0
	default:
		return 0
	}
}

// estimateFileTokens returns a flat cost for attached media. Media is counted
// by part, not by byte, because providers price an image as an image.
func estimateFileTokens(part fantasy.FilePart) int {
	return 1500
}

// estimateBase64Tokens returns the token cost of inline media data. Base64 is
// roughly one token per 3 bytes after decoding, and providers bill per media
// item, so this is a flat-ish estimate with a size floor.
func estimateBase64Tokens(data string) int {
	if data == "" {
		return 0
	}
	return 1500
}

// Summarize computes a prompt budget from a system prompt and the messages
// that follow it.
func Summarize(system string, messages []fantasy.Message) Budget {
	b := Budget{
		System: EstimateTokens(system),
	}
	for _, msg := range messages {
		acct := AccountMessage(msg)
		total := acct.Total()
		if total == 0 {
			continue
		}
		b.Messages += total
		b.Reasoning += acct.Reasoning
		b.ToolResults += acct.ToolResult
		b.ToolInputs += acct.ToolInput
		b.Files += acct.Files
		b.PerMessage = append(b.PerMessage, MessageAccounting{
			Tokens:          total,
			ReasoningChars:  acct.Reasoning * BytesPerToken,
			ToolResultChars: acct.ToolResult * BytesPerToken,
		})
	}
	b.Counted = len(messages) > 0
	return b
}
