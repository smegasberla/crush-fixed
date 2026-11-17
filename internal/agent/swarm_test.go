package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/stretchr/testify/require"
)

func TestSwarmWorkerPromptIsTiny(t *testing.T) {
	t.Parallel()

	prompt := swarmWorkerPrompt("Where is the retry loop?", "inspect internal/agent/retry.go")
	require.Less(t, contextreduction.EstimateTokens(prompt), 100,
		"a worker's prompt must be a few dozen tokens, not a paragraph")
	require.Contains(t, prompt, "inspect internal/agent/retry.go")
	require.NotContains(t, prompt, "<critical_rules>",
		"worker prompts must not restate the parent's rules")
}

func TestSwarmValidationBoundsFanOut(t *testing.T) {
	t.Parallel()

	require.Empty(t, validateSwarmParams(SwarmParams{
		Question: "where is X", Items: []string{"a", "b"},
	}))
	require.NotEmpty(t, validateSwarmParams(SwarmParams{Items: []string{"only one"}}),
		"a single item should use the cheaper single-agent path")

	tooMany := make([]string, swarmMaxItems+1)
	for i := range tooMany {
		tooMany[i] = "item"
	}
	require.NotEmpty(t, validateSwarmParams(SwarmParams{Items: tooMany}))
	require.NotEmpty(t, validateSwarmParams(SwarmParams{Items: []string{"a", " "}}))
}

func TestSwarmReportIsOrderedAndClamped(t *testing.T) {
	t.Parallel()

	results := []SwarmResult{
		{Item: "auth", Result: "found a bug"},
		{Item: "db", Result: clampSwarmFinding(strings.Repeat("x", swarmFindingChars+500))},
	}
	report := renderSwarmReport(results)
	require.Less(t, strings.Index(report, "1. auth"), strings.Index(report, "2. db"),
		"results follow the caller's items, not completion order")
	require.LessOrEqual(t, len(results[1].Result), swarmFindingChars+len("… [finding truncated]"))
	require.Contains(t, report, "finding truncated")
}

func TestSwarmDescriptionIsShort(t *testing.T) {
	t.Parallel()

	require.Less(t, contextreduction.EstimateTokens(swarmToolDescription), 300,
		"the swarm description is paid on every parent request")
}
