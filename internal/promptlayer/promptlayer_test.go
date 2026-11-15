package promptlayer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLayerOrderIsFixed(t *testing.T) {
	t.Parallel()

	prompt := New()
	// Add out of order: the render order must not depend on insertion order.
	prompt.Add(LayerNotes, "volatile note")
	prompt.Add(LayerContract, "the contract")
	prompt.Add(LayerContext, "project context")
	prompt.Add(LayerCore, "the core")

	rendered := prompt.Render()
	core := strings.Index(rendered, "the core")
	context := strings.Index(rendered, "project context")
	contract := strings.Index(rendered, "the contract")
	notes := strings.Index(rendered, "volatile note")

	require.True(t, core < context, "core comes before context")
	require.True(t, context < contract, "context comes before the contract")
	require.True(t, contract < notes, "the contract comes before the notes")
}

func TestEmptySectionsAreIgnored(t *testing.T) {
	t.Parallel()

	prompt := New()
	prompt.Add(LayerCore, "core")
	prompt.Add(LayerContext, "")
	prompt.Add(LayerNotes, "   ")

	require.Equal(t, "core", prompt.Render())
}

func TestNilPromptIsSafe(t *testing.T) {
	t.Parallel()

	var prompt *Prompt
	prompt.Add(LayerCore, "x")
	prompt.AddAll(LayerCore, "x")
	require.Empty(t, prompt.Render())
	require.Empty(t, prompt.CoreDigest())
	require.False(t, prompt.StablePrefixChanged("abc"))
}

func TestCoreDigestIsStable(t *testing.T) {
	t.Parallel()

	first := New()
	first.Add(LayerCore, "core")
	first.Add(LayerContext, "ctx")

	second := New()
	second.Add(LayerCore, "core")
	second.Add(LayerContext, "ctx")

	require.Equal(t, first.CoreDigest(), second.CoreDigest())
	require.Len(t, first.CoreDigest(), 16)

	// The volatile layer does not affect the digest: that is the whole point.
	second.Add(LayerNotes, "a note that changes every round")
	require.Equal(t, first.CoreDigest(), second.CoreDigest())
	require.False(t, second.StablePrefixChanged(first.CoreDigest()))
}

func TestStablePrefixChanged(t *testing.T) {
	t.Parallel()

	prompt := New()
	prompt.Add(LayerCore, "core")
	digest := prompt.CoreDigest()

	changed := New()
	changed.Add(LayerCore, "different core")
	require.True(t, changed.StablePrefixChanged(digest))

	// No previous digest means nothing to compare.
	require.False(t, changed.StablePrefixChanged(""))
}

func TestLayered(t *testing.T) {
	t.Parallel()

	rendered := Layered("core", "ctx", "contract", "note one", "note two")
	require.Contains(t, rendered, "core")
	require.Contains(t, rendered, "ctx")
	require.Contains(t, rendered, "contract")
	require.Contains(t, rendered, "note one")
	require.Contains(t, rendered, "note two")
	require.True(t, strings.Index(rendered, "core") < strings.Index(rendered, "note one"))
}

func TestContract(t *testing.T) {
	t.Parallel()

	contract := Contract("fix the retry loop", []string{"read the code", "make the change"}, []string{"agent.go"})
	require.Contains(t, contract, "<task_contract>")
	require.Contains(t, contract, "GOAL: fix the retry loop")
	require.Contains(t, contract, "- read the code")
	require.Contains(t, contract, "FILES THIS TASK IS EXPECTED TO TOUCH: agent.go")
	require.Contains(t, contract, "</task_contract>")
}

func TestContractEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, Contract("", nil, nil))
	// Whitespace-only input produces no contract at all, not an empty one.
	require.Empty(t, Contract("  ", nil, nil))

	onlyFiles := Contract("", nil, []string{"a.go"})
	require.Contains(t, onlyFiles, "a.go")
	require.NotContains(t, onlyFiles, "GOAL")
}

func TestContractCollapsesWhitespace(t *testing.T) {
	t.Parallel()

	contract := Contract("fix\n\nthe   retry loop", nil, nil)
	require.Contains(t, contract, "GOAL: fix the retry loop")

	long := Contract(strings.Repeat("word ", 200), nil, nil)
	require.Contains(t, long, "…")
}

func TestLayerString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "core", LayerCore.String())
	require.Equal(t, "context", LayerContext.String())
	require.Equal(t, "contract", LayerContract.String())
	require.Equal(t, "notes", LayerNotes.String())
}
