package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/charmbracelet/crush/internal/message"
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

func TestAbsorbedPrefixStopsAtTheBrief(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{ID: "old-1"},
		{ID: "brief"},
		{ID: "after-1"},
		{ID: "after-2"},
	}
	require.Equal(t, 2, absorbedPrefix(msgs, "brief"),
		"the brief itself is absorbed, so the delta starts after it")
	require.Equal(t, 0, absorbedPrefix(msgs, ""),
		"a session that was never compacted has nothing absorbed")
	require.Equal(t, 0, absorbedPrefix(msgs, "missing"),
		"an unknown summary id must not silently skip the transcript")
}

// TestCompactionNeverCallsAModel is the whole point of the local design: a
// compaction is a measurement plus a pointer move, so it must not spend a
// round-trip over the very transcript that is too large to send.
func TestCompactionNeverCallsAModel(t *testing.T) {
	t.Parallel()

	model := &recordingStreamModel{text: "unused"}
	env := testEnv(t)
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{CompactionTriggerTokens: intPtr(1), CompactionBudgetTokens: intPtr(1)},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	_, err = sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "folded earlier"})
	require.NoError(t, err)
	_, runErr := sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "work after"})
	require.NoError(t, runErr)

	model.reset()
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))
	require.Empty(t, model.calls(), "compaction must not ask a model to write anything")
}

// TestCompactionMovesTheHistoryMarker covers what a compaction actually does:
// it records where the visible history starts and leaves every message in the
// database, so the fold is a view rather than a deletion.
func TestCompactionMovesTheHistoryMarker(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, &recordingStreamModel{text: "unused"}, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{CompactionTriggerTokens: intPtr(1), CompactionBudgetTokens: intPtr(1)},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	_, err = sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "folded earlier"})
	require.NoError(t, err)
	_, runErr := sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "work after"})
	require.NoError(t, runErr)

	before, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, before)

	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))

	after, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, after, len(before), "compaction must not delete history")

	updated := mustSession(t, env, sess.ID)
	require.NotEmpty(t, updated.SummaryMessageID, "the visible history now starts at a later message")

	// The kept tail still contains the newest prompt.
	visible, err := sa.getSessionMessages(ctx, updated)
	require.NoError(t, err)
	var joined strings.Builder
	for _, msg := range visible {
		joined.WriteString(msg.Content().Text)
	}
	require.Contains(t, joined.String(), "work after", "the recent work survives the fold")
}

// TestCompactionRecordsWhatItSaved covers the telemetry: a compaction reports
// the sizes it measured, so the saving is auditable rather than assumed.
func TestCompactionRecordsWhatItSaved(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, &recordingStreamModel{text: "unused"}, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	// A trigger above what the transcript costs and a budget below it, so the
	// planner folds the older part and keeps the tail.
	sa.reduction = newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{
			CompactionTriggerTokens: intPtr(1),
			CompactionBudgetTokens:  intPtr(10),
			ResponseReserveTokens:   intPtr(1),
		},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	for _, prompt := range []string{"first prompt", "second prompt", "third prompt"} {
		_, runErr := sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: prompt})
		require.NoError(t, runErr)
	}

	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))
	rec, ok := sa.compactions.get(sess.ID)
	require.True(t, ok, "a compaction must be recorded")
	require.Positive(t, rec.OriginalTokens)
	require.Positive(t, rec.DroppedMessages)
	require.NotEmpty(t, rec.Trigger)
}

// recordingStreamModel is a minimal LanguageModel that records the messages it
// was asked to read, so a test can assert what the summarizer actually saw.
type recordingStreamModel struct {
	finishStreamModel
	mu      sync.Mutex
	prompts []fantasy.Prompt
}

func (m *recordingStreamModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.prompts = append(m.prompts, call.Prompt)
	m.mu.Unlock()
	return m.finishStreamModel.Stream(ctx, call)
}

func (m *recordingStreamModel) reset() {
	m.mu.Lock()
	m.prompts = nil
	m.mu.Unlock()
}

func (m *recordingStreamModel) calls() []fantasy.Prompt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]fantasy.Prompt(nil), m.prompts...)
}

func (m *recordingStreamModel) lastCall() fantasy.Prompt {
	calls := m.calls()
	if len(calls) == 0 {
		return nil
	}
	return calls[len(calls)-1]
}

// promptText flattens every text part of a recorded prompt.
func promptText(prompt fantasy.Prompt) string {
	var sb strings.Builder
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				sb.WriteString(text.Text)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

func mustSession(t *testing.T, env fakeEnv, sessionID string) session.Session {
	t.Helper()
	sess, err := env.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	return sess
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

// intPtr is a helper for the optional int settings.
func intPtr(v int) *int { return &v }

// TestCompactionIsInstantAndLocal pins the properties Chaser's telemetry
// established for its compaction path: it makes no provider call, and it takes
// milliseconds rather than the tens of seconds a model-written summary costs.
func TestCompactionIsInstantAndLocal(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &recordingStreamModel{text: "unused"}
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{
			CompactionTriggerTokens: intPtr(1),
			CompactionBudgetTokens:  intPtr(10),
			ResponseReserveTokens:   intPtr(1),
		},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	for _, prompt := range []string{"one", "two", "three", "four"} {
		_, runErr := sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: prompt})
		require.NoError(t, runErr)
	}

	model.reset()
	start := time.Now()
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))
	elapsed := time.Since(start)

	require.Empty(t, model.calls(), "compaction must not call a model")
	require.Less(t, elapsed, 500*time.Millisecond,
		"compaction is a measurement and a pointer move, not a round-trip")
}

// TestCompactionWindowIsNoteThenTail checks the payload a compacted request
// builds: the note that explains the fold comes first, then the kept tail.
func TestCompactionWindowIsNoteThenTail(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, &recordingStreamModel{text: "unused"}, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{
			CompactionTriggerTokens: intPtr(1),
			CompactionBudgetTokens:  intPtr(10),
			ResponseReserveTokens:   intPtr(1),
		},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	for _, prompt := range []string{"oldest", "middle", "newest"} {
		_, runErr := sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: prompt})
		require.NoError(t, runErr)
	}
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))

	updated := mustSession(t, env, sess.ID)
	window, err := sa.getSessionMessages(ctx, updated)
	require.NoError(t, err)
	require.NotEmpty(t, window)

	first := window[0].Content().Text
	require.Contains(t, first, "earlier messages were compacted",
		"the window opens with the note that explains the fold")
	require.Contains(t, first, "messages kept verbatim",
		"the note reports how much survived")

	// The newest work is in the window, not something the fold dropped.
	var tail strings.Builder
	for _, msg := range window {
		tail.WriteString(msg.Content().Text)
	}
	require.Contains(t, tail.String(), "newest", "the fold keeps the recent conversation")
	require.NotContains(t, tail.String(), "oldest", "the fold drops the oldest work")
}

// TestCompactionActuallyShrinksTheNextPrompt is the end-to-end guard for the
// bug that made every compaction a no-op: the window marker was resolved by a
// timestamp, every message was written in the same second, so the whole
// transcript came back and the fold changed nothing while reporting success.
func TestCompactionActuallyShrinksTheNextPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, &recordingStreamModel{text: ""}, &finishStreamModel{text: "t"}, "sys").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{
			CompactionTriggerTokens: intPtr(1_000_000),
			CompactionBudgetTokens:  intPtr(1_000_000),
		},
		DigestDir: t.TempDir(),
		Provider:  "p",
		Model:     "m",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)

	// Fat messages written directly, so no agent turn compacts them first.
	blob := strings.Repeat("x", 4000)
	for i := range 40 {
		_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: fmt.Sprintf("%d %s", i, blob)}},
		})
		require.NoError(t, err)
	}

	windowBefore, err := sa.getSessionMessages(ctx, mustSession(t, env, sess.ID))
	require.NoError(t, err)
	before := contextreduction.Summarize("", aiOf(t, sa, windowBefore)).Total()

	// Now compact with a trigger the transcript exceeds.
	sa.reduction.settings = &config.ContextReductionOptions{
		CompactionTriggerTokens: intPtr(5000),
		CompactionBudgetTokens:  intPtr(5000),
		ResponseReserveTokens:   intPtr(100),
	}
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))

	windowAfter, err := sa.getSessionMessages(ctx, mustSession(t, env, sess.ID))
	require.NoError(t, err)
	after := contextreduction.Summarize("", aiOf(t, sa, windowAfter)).Total()

	require.Less(t, len(windowAfter), len(windowBefore), "the window must actually shrink")
	require.Less(t, after, before, "the next prompt must be smaller than the one before it")
	require.Greater(t, float64(before-after)/float64(before), 0.5,
		"a compaction over a fat transcript must save most of it, got %d -> %d tokens", before, after)
}

// aiOf converts stored messages to the prompt shape, for size comparisons.
func aiOf(t *testing.T, sa *sessionAgent, msgs []message.Message) []fantasy.Message {
	t.Helper()
	ai, _ := sa.preparePrompt(msgs, false, false)
	return ai
}
