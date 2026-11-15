package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestContinuationPromptWithoutHistory(t *testing.T) {
	t.Parallel()

	prompt := buildContinuationPrompt(nil, "")
	require.Contains(t, prompt, "continuation brief")
	require.NotContains(t, prompt, "<previous_brief>")
	require.NotContains(t, prompt, "<current_plan>")
}

func TestContinuationPromptCarriesThePreviousBrief(t *testing.T) {
	t.Parallel()

	prompt := buildContinuationPrompt(nil, "## Established facts\n- the build needs BUILD_DIR")
	require.Contains(t, prompt, "<previous_brief>")
	require.Contains(t, prompt, "BUILD_DIR")
	require.Contains(t, prompt, "Keep every fact and constraint in it that is still true")
	require.Contains(t, prompt, "Do not repeat what it already says")
}

func TestContinuationPromptCarriesThePlan(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{
		{Content: "read the code", Status: session.TodoStatusCompleted},
		{Content: "change the loop", Status: session.TodoStatusPending},
	}
	prompt := buildContinuationPrompt(todos, "")
	require.Contains(t, prompt, "<current_plan>")
	require.Contains(t, prompt, "- [completed] read the code")
	require.Contains(t, prompt, "- [pending] change the loop")
	require.Contains(t, prompt, "continues the plan instead of restating it")
}

func TestSummaryPromptIsTheBrief(t *testing.T) {
	t.Parallel()

	// buildSummaryPrompt is what the summarize path calls; it must ask for the
	// brief, not for a generic summary.
	prompt := buildSummaryPrompt(nil)
	require.Contains(t, prompt, "continuation brief")
}

func TestBriefMemoIsReusedAcrossIdenticalPrefixes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{},
		DigestDir: dir,
		Provider:  "p",
		Model:     "m",
	})
	history := []fantasy.Message{fantasy.NewUserMessage("first"), fantasy.NewUserMessage("second")}

	chain := chainFor("", history)
	_, ok := state.summaryFor(chain)
	require.False(t, ok, "nothing has been written yet")

	state.rememberSummary(chain, len(history), "## Established facts\n- the cache is disabled in tests")
	brief, ok := state.summaryFor(chain)
	require.True(t, ok)
	require.Contains(t, brief, "cache is disabled")

	// A different prefix is a different brief.
	other := chainFor("", append(history, fantasy.NewUserMessage("third")))
	_, ok = state.summaryFor(other)
	require.False(t, ok)
}

func TestBriefMemoSurvivesARestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settings := &config.ContextReductionOptions{}
	first := newReductionState(&ReductionOptions{Settings: settings, DigestDir: dir, Provider: "p", Model: "m"})
	history := []fantasy.Message{fantasy.NewUserMessage("hello")}
	chain := chainFor("", history)
	first.rememberSummary(chain, 1, "## Goal\nship it")

	// A new process, the same digest directory.
	second := newReductionState(&ReductionOptions{Settings: settings, DigestDir: dir, Provider: "p", Model: "m"})
	brief, ok := second.summaryFor(chain)
	require.True(t, ok)
	require.Contains(t, brief, "ship it")
	_ = filepath.Join(dir, "digests")
}

func TestChainForCoversEveryPart(t *testing.T) {
	t.Parallel()

	base := []fantasy.Message{fantasy.NewUserMessage("hi")}
	withReasoning := []fantasy.Message{{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "hi"},
			fantasy.ReasoningPart{Text: "thinking"},
		},
	}}
	require.NotEqual(t, chainFor("", base), chainFor("", withReasoning))

	// Tool results are part of the absorbed prefix: a brief that ignored them
	// would be reused for a transcript it never saw.
	withResult := []fantasy.Message{{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "c1", Output: fantasy.ToolResultOutputContentText{Text: "output"}},
		},
	}}
	require.NotEqual(t, chainFor("", base), chainFor("", withResult))
}

func TestBriefPromptMentionsPointersNotBodies(t *testing.T) {
	t.Parallel()

	text := string(summaryPrompt)
	require.Contains(t, text, "Pointers, not bodies")
	require.Contains(t, text, "Already ruled out")
	require.Contains(t, text, "Session rules")
	require.Contains(t, text, "Never mention that this is a summary")
	require.NotContains(t, strings.ToLower(text), "be thorough",
		"the old prompt asked for length, which is the cost this brief exists to avoid")
}

func TestSummaryMemoNilStateIsSafe(t *testing.T) {
	t.Parallel()

	var state *reductionState
	_, ok := state.summaryFor("chain")
	require.False(t, ok)
	state.rememberSummary("chain", 1, "brief")

	agent := &sessionAgent{}
	require.Nil(t, agent.reduction)
	_ = context.Background()
}
