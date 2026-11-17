package contextreduction

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/fantasy"
)

// ContinuityNote describes what the folded part of a conversation did, using
// only facts already present in the messages. It is assembled locally at
// compaction: asking a model to write it would cost the same round-trip the
// local compaction exists to avoid.
//
// The point is pointers, not bodies. A model that sees this note should know
// which files were read and changed and which errors were already diagnosed,
// so it continues from that state instead of re-reading the same files. The
// recent tail is still sent verbatim; this only covers what it cannot see.
type ContinuityNote struct {
	Paths      []string
	Edits      []string
	Commands   []string
	Errors     []string
	LastPrompt string
}

// BuildContinuityNote reads the folded messages and records the smallest set of
// facts that stop the next assistant repeating work. Paths are deduplicated
// and sorted so the note is stable for a given transcript, which also makes it
// cache-friendly.
func BuildContinuityNote(messages []fantasy.Message) ContinuityNote {
	var note ContinuityNote
	paths := make(map[string]struct{})
	edits := make(map[string]struct{})
	commands := make(map[string]struct{})
	errs := make(map[string]struct{})

	names := toolNamesByCallID(messages)
	for i, msg := range messages {
		if msg.Role == fantasy.MessageRoleUser {
			// The first user turn of the folded part is the request the
			// assistant was working on. Later user turns are usually tool
			// reports or a "continue", which say nothing about the goal.
			if text := lastText(msg); text != "" && note.LastPrompt == "" {
				note.LastPrompt = text
			}
		}
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.ToolCallPart:
				recordCall(names[p.ToolCallID], p.ToolName, p.Input, edits, commands)
			case *fantasy.ToolCallPart:
				if p != nil {
					recordCall(names[p.ToolCallID], p.ToolName, p.Input, edits, commands)
				}
			case fantasy.TextPart:
				for _, path := range PathsIn(p.Text, 8) {
					paths[path] = struct{}{}
				}
				if msg.Role == fantasy.MessageRoleUser {
					for _, path := range PathsIn(p.Text, 8) {
						paths[path] = struct{}{}
					}
				}
			}
			tr, ok := asToolResult(part)
			if !ok {
				continue
			}
			text, isError := toolResultText(tr)
			for _, path := range PathsIn(text, 8) {
				paths[path] = struct{}{}
			}
			if isError {
				first := firstLine(text)
				if first != "" {
					errs[first] = struct{}{}
				}
			}
		}
		_ = i
	}

	note.Paths = sortedKeys(paths)
	note.Edits = sortedKeys(edits)
	note.Commands = sortedKeys(commands)
	note.Errors = sortedKeys(errs)
	return note
}

// recordCall pulls the addressable target out of a tool call without keeping
// the call's full input. An edit names a file; a shell command is kept only as
// its first line so a test run is recognisable.
func recordCall(displayName, name, input string, edits, commands map[string]struct{}) {
	path := jsonStringField(input, "file_path")
	if path == "" {
		path = jsonStringField(input, "path")
	}
	switch name {
	case "edit", "multiedit", "write":
		if path != "" {
			edits[path] = struct{}{}
		}
	case "bash":
		cmd := jsonStringField(input, "command")
		if cmd != "" {
			commands[firstLine(cmd)] = struct{}{}
		}
	}
}

// Render writes the note in a fixed, compact shape. Sections with nothing in
// them are omitted so an ordinary fold costs almost nothing.
func (n ContinuityNote) Render() string {
	var sb strings.Builder
	if n.LastPrompt != "" {
		fmt.Fprintf(&sb, "Request in progress: %s\n", truncateLine(firstLine(n.LastPrompt), 200))
	}
	if len(n.Edits) > 0 {
		fmt.Fprintf(&sb, "Files changed: %s\n", strings.Join(n.Edits, ", "))
	}
	if len(n.Paths) > 0 {
		fmt.Fprintf(&sb, "Files already read: %s\n", strings.Join(n.Paths, ", "))
	}
	if len(n.Commands) > 0 {
		fmt.Fprintf(&sb, "Commands already run: %s\n", strings.Join(n.Commands, ", "))
	}
	if len(n.Errors) > 0 {
		fmt.Fprintf(&sb, "Errors already seen: %s\n", strings.Join(n.Errors, ", "))
	}
	return strings.TrimSpace(sb.String())
}

// Empty reports whether there is nothing worth sending.
func (n ContinuityNote) Empty() bool { return n.Render() == "" }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func lastText(msg fantasy.Message) string {
	for i := len(msg.Content) - 1; i >= 0; i-- {
		if p, ok := msg.Content[i].(fantasy.TextPart); ok && p.Text != "" {
			return p.Text
		}
	}
	return ""
}

func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(strings.TrimSpace(s), '\n'); i >= 0 {
		return strings.TrimSpace(s)[:i]
	}
	return strings.TrimSpace(s)
}

// jsonStringField reads a top-level string field from a JSON object without
// the noise of a full struct decode. Tool inputs are small and well-formed by
// the time they reach here.
func jsonStringField(input, field string) string {
	needle := `"` + field + `"`
	i := strings.Index(input, needle)
	if i < 0 {
		return ""
	}
	i += len(needle)
	for i < len(input) && (input[i] == ' ' || input[i] == '\t' || input[i] == ':') {
		i++
	}
	if i >= len(input) || input[i] != '"' {
		return ""
	}
	i++
	var b strings.Builder
	for i < len(input) {
		c := input[i]
		if c == '\\' && i+1 < len(input) {
			i++
			b.WriteByte(input[i])
			i++
			continue
		}
		if c == '"' {
			break
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
