// Package promptlayer assembles a prompt so its cacheable prefix stays
// byte-stable across rounds.
//
// A provider caches the longest identical prefix of a request. Any change near
// the front invalidates everything after it, and the agent re-pays for the whole
// prompt. The fix is ordering, not compression: stable content first, then the
// task, then the volatile notes, because only the tail changes between rounds.
package promptlayer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Layer is one part of the prompt, with a fixed position.
type Layer int

const (
	// LayerCore is the system prompt: identity, rules, tool guidance. It must
	// not change between rounds.
	LayerCore Layer = iota
	// LayerContext is project context such as AGENTS.md. It changes only when
	// the files themselves change.
	LayerContext
	// LayerContract is the task contract for this turn: the goal, the
	// required actions and the named files.
	LayerContract
	// LayerNotes is the volatile part: steering notes, memory retrieval and
	// turn-specific reminders.
	LayerNotes
)

// String names the layer for logs.
func (l Layer) String() string {
	switch l {
	case LayerContext:
		return "context"
	case LayerContract:
		return "contract"
	case LayerNotes:
		return "notes"
	default:
		return "core"
	}
}

// Prompt is a layered prompt under construction.
type Prompt struct {
	layers map[Layer][]string
}

// New returns an empty prompt.
func New() *Prompt {
	return &Prompt{layers: make(map[Layer][]string)}
}

// Add appends text to a layer. Empty text is ignored so an absent section never
// becomes a blank line that shifts the prefix.
func (p *Prompt) Add(layer Layer, text string) {
	if p == nil || strings.TrimSpace(text) == "" {
		return
	}
	p.layers[layer] = append(p.layers[layer], text)
}

// AddAll appends several sections to one layer.
func (p *Prompt) AddAll(layer Layer, texts ...string) {
	for _, text := range texts {
		p.Add(layer, text)
	}
}

// Render joins the layers in their fixed order.
func (p *Prompt) Render() string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, layer := range []Layer{LayerCore, LayerContext, LayerContract, LayerNotes} {
		sections := p.layers[layer]
		if len(sections) == 0 {
			continue
		}
		for _, section := range sections {
			sb.WriteString(strings.TrimRight(section, "\n"))
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// CoreDigest returns a short hash of the stable part of the prompt: everything
// up to and including the contract. It is what a caller compares between rounds
// to confirm the cacheable prefix did not change.
func (p *Prompt) CoreDigest() string {
	if p == nil {
		return ""
	}
	h := sha256.New()
	for _, layer := range []Layer{LayerCore, LayerContext, LayerContract} {
		for _, section := range p.layers[layer] {
			h.Write([]byte(section))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// StablePrefixChanged reports whether the cacheable prefix differs from the
// digest of the previous round. A caller logs this: a prefix that changes every
// round means the prompt is misordered, and every request pays full price.
func (p *Prompt) StablePrefixChanged(previousDigest string) bool {
	if previousDigest == "" || p == nil {
		return false
	}
	return p.CoreDigest() != previousDigest
}

// Layered assembles the whole prompt from its parts. It is the convenience form
// for callers that already have the five strings.
func Layered(core, context, contract string, notes ...string) string {
	prompt := New()
	prompt.Add(LayerCore, core)
	prompt.Add(LayerContext, context)
	prompt.Add(LayerContract, contract)
	prompt.AddAll(LayerNotes, notes...)
	return prompt.Render()
}

// Contract builds the task-contract layer from a request.
//
// The contract restates the goal in the user's own words plus the facts the turn
// must honour, which is what keeps a long turn aimed: the model can re-read it
// without any of the conversation that produced it.
func Contract(goal string, requiredActions, targetFiles []string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" && len(requiredActions) == 0 && len(targetFiles) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<task_contract>\n")
	if goal != "" {
		sb.WriteString("GOAL: ")
		sb.WriteString(oneLine(goal, 400))
		sb.WriteString("\n")
	}
	if len(requiredActions) > 0 {
		sb.WriteString("REQUIRED ACTIONS:\n")
		for _, action := range requiredActions {
			sb.WriteString("- ")
			sb.WriteString(action)
			sb.WriteString("\n")
		}
	}
	if len(targetFiles) > 0 {
		sb.WriteString("FILES THIS TASK IS EXPECTED TO TOUCH: ")
		sb.WriteString(strings.Join(targetFiles, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("</task_contract>")
	return sb.String()
}

// oneLine collapses whitespace and truncates, so a multi-paragraph request stays
// one contract line.
func oneLine(text string, limit int) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	runes := []rune(collapsed)
	if len(runes) <= limit {
		return collapsed
	}
	return string(runes[:limit]) + "…"
}
