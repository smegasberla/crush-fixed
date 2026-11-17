package agent

import (
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/stretchr/testify/require"
)

// reductionForTest builds a reduction state with the given settings and a
// throwaway digest directory.
func reductionForTest(t *testing.T, settings *config.ContextReductionOptions) *reductionState {
	t.Helper()
	return newReductionState(&ReductionOptions{
		Settings:  settings,
		DigestDir: t.TempDir(),
		Provider:  "test",
		Model:     "model",
	})
}

func bigToolBody(lines int) string {
	return strings.Repeat("tool output line\n", lines)
}

func toolCallMsg(id, name, input string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.ToolCallPart{ToolCallID: id, ToolName: name, Input: input},
		},
	}
}

func toolResultMsg(id, text string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{
				ToolCallID: id,
				Output:     fantasy.ToolResultOutputContentText{Text: text},
			},
		},
	}
}

func TestNewReductionStateDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, newReductionState(nil))
	require.Nil(t, newReductionState(&ReductionOptions{}), "nil settings disable the stack")

	off := false
	require.Nil(t, newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{Enabled: &off},
	}))

	state := reductionForTest(t, &config.ContextReductionOptions{})
	require.NotNil(t, state)
	require.True(t, state.enabled())
}

func TestReductionStateCeiling(t *testing.T) {
	t.Parallel()

	// A nil state hands the advertised window through untouched.
	var nilState *reductionState
	require.Equal(t, 1000, nilState.ceilingFor(1000))
	require.Equal(t, 0, nilState.ceilingFor(0))

	state := reductionForTest(t, &config.ContextReductionOptions{
		CeilingSafetyPercent: ptr(50),
	})
	require.Equal(t, 500, state.ceilingFor(1000))

	// A learned ceiling from a real overflow wins over the advertised one.
	state.learnOverflow(400, errors.New("context_length_exceeded"))
	require.Equal(t, 360, state.ceilingFor(1000))
}

func TestReductionStateDigestCaps(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{
		ToolResultDigestTokens: ptr(1000),
	})
	opts := state.digestOptions()
	require.Equal(t, 1000, opts.MinTokens)
	require.True(t, opts.ProtectErrors)
	require.Equal(t, 250, opts.ToolCaps["ls"], "cheap listings are digested earlier")
	require.Equal(t, 2000, opts.ToolCaps["bash"], "build output keeps more")
}

func TestReductionStatePointerOptions(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{
		ReadPointerStubTokens: ptr(128),
	})
	require.Equal(t, 128, state.pointerOptions().MinTokens)

	off := false
	state = reductionForTest(t, &config.ContextReductionOptions{ReadPointerStubs: &off})
	require.Greater(t, state.pointerOptions().MinTokens, 1_000_000, "disabled stubbing never fires")
}

func TestRepackNoOpWithinCeiling(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{})
	messages := []fantasy.Message{fantasy.NewUserMessage("short")}
	out, outcome := state.repack("system", messages, 200000)
	require.Equal(t, messages, out)
	require.Equal(t, outcome.Before, outcome.After)
	require.Zero(t, outcome.Digested)
	require.Equal(t, config.DefaultCompactionBudgetTokens, outcome.Ceiling,
		"the configured budget caps the ceiling even on a large advertised window")
	require.Equal(t, 4, outcome.Before)
}

func TestRepackDigestsThenDrops(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{
		ToolResultDigestTokens: ptr(10),
		CeilingSafetyPercent:   ptr(10),
	})
	body := bigToolBody(4000)
	messages := []fantasy.Message{
		toolCallMsg("c1", "bash", `{}`),
		toolResultMsg("c1", body),
		fantasy.NewUserMessage("next"),
		fantasy.NewUserMessage("next2"),
		fantasy.NewUserMessage("next3"),
	}
	out, outcome := state.repack("", messages, 100000)
	require.Positive(t, outcome.Digested, "an aged, oversized result is digested")
	require.Equal(t, 10000, outcome.Ceiling)
	require.Less(t, outcome.After, outcome.Before)
	require.Len(t, out, len(messages))
}

func TestRepackDropsOldestWhenStillOver(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{
		CeilingSafetyPercent: ptr(1),
	})
	var messages []fantasy.Message
	for range 20 {
		messages = append(messages, fantasy.NewUserMessage(strings.Repeat("y", 400)))
	}
	out, outcome := state.repack("", messages, 100000)
	require.Positive(t, outcome.DroppedMessages)
	require.Less(t, len(out), len(messages))
	require.Equal(t, messages[len(messages)-1], out[len(out)-1])
}

func TestReducePromptDisabled(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	messages := []fantasy.Message{fantasy.NewUserMessage("x")}
	out, outcome := agent.reducePrompt("", messages, 100)
	require.Equal(t, messages, out)
	require.Zero(t, outcome.Ceiling)
	require.Equal(t, 1, outcome.Messages)
}

func TestChainForTracksContent(t *testing.T) {
	t.Parallel()

	a := []fantasy.Message{fantasy.NewUserMessage("hello")}
	b := []fantasy.Message{fantasy.NewUserMessage("hello")}
	c := []fantasy.Message{fantasy.NewUserMessage("hello!")}

	require.Equal(t, chainFor("", a), chainFor("", b))
	require.NotEqual(t, chainFor("", a), chainFor("", c))
	require.NotEqual(t, chainFor("prefix", a), chainFor("", a))

	// Tool results and calls contribute to the chain.
	toolsA := []fantasy.Message{toolCallMsg("c1", "bash", `{"command":"ls"}`), toolResultMsg("c1", "a.go")}
	toolsB := []fantasy.Message{toolCallMsg("c1", "bash", `{"command":"ls"}`), toolResultMsg("c1", "b.go")}
	require.NotEqual(t, chainFor("", toolsA), chainFor("", toolsB))
}

func TestSummaryMemoRoundTrip(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{})
	chain := contextreduction.ChainHash("", "prefix")

	_, ok := state.summaryFor(chain)
	require.False(t, ok)

	state.rememberSummary(chain, 12, "## brief")
	brief, ok := state.summaryFor(chain)
	require.True(t, ok)
	require.Equal(t, "## brief", brief)

	// The memo is persisted, so a fresh state over the same directory finds it.
	fresh := newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{},
		DigestDir: state.store.Dir(),
		Provider:  "test",
		Model:     "model",
	})
	brief, ok = fresh.summaryFor(chain)
	require.True(t, ok)
	require.Equal(t, "## brief", brief)
}

func TestSummaryMemoIgnoresEmpty(t *testing.T) {
	t.Parallel()

	state := reductionForTest(t, &config.ContextReductionOptions{})
	state.rememberSummary("", 1, "brief")
	state.rememberSummary("chain", 1, "")
	_, ok := state.summaryFor("chain")
	require.False(t, ok)
}

func TestDigestDirFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/data/digests", digestDirFor(&config.ContextReductionOptions{}, "/data"))
	require.Equal(t, "", digestDirFor(&config.ContextReductionOptions{}, ""))
	require.Equal(t, "/custom", digestDirFor(&config.ContextReductionOptions{DigestCacheDir: "/custom"}, "/data"))
	require.Equal(t, "/data/digests", digestDirFor(nil, "/data"))
}

func TestPromptTokens(t *testing.T) {
	t.Parallel()

	require.Zero(t, promptTokens(nil))
	require.Equal(t, 25, promptTokens([]fantasy.Message{fantasy.NewUserMessage(strings.Repeat("x", 100))}))
}
