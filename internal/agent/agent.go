// Package agent is the core orchestration layer for Crush AI agents.
//
// It provides session-based AI agent functionality for managing
// conversations, tool execution, and message handling. It coordinates
// interactions between language models, messages, sessions, and tools while
// handling features like automatic summarization, queuing, and token
// management.
package agent

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"charm.land/lipgloss/v2"
	"charm.land/tone"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/contextreduction"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/editsafe"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/nudge"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/stringext"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/charmbracelet/x/ansi"
)

const (
	DefaultSessionName = "Untitled Session"

	// Constants for auto-summarization thresholds
	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.2
)

var userAgent = fmt.Sprintf("Charm-Crush/%s (https://charm.land/crush)", version.Version)

// providerStoppedTurnMessage is the banner title recorded when a provider
// ends the response on its own terms (fantasy's error/other finish
// reasons) instead of completing it. Details are filled in per turn.
const providerStoppedTurnMessage = "Provider stopped the response"

//go:embed templates/title.md
var titlePrompt []byte

// summaryPrompt is the background digest's instructions for writing a
// continuation brief. Compaction does not use it: compaction is local.
//
//go:embed templates/summary.md
var summaryPrompt []byte

// Used to remove <think> tags from generated titles.
var (
	thinkTagRegex       = regexp.MustCompile(`(?s)<think>.*?</think>`)
	orphanThinkTagRegex = regexp.MustCompile(`</?think>`)
)

type SessionAgentCall struct {
	SessionID string
	// RunID, when non-empty, is the caller-supplied correlator that
	// gets echoed back on the notify.RunComplete event emitted for
	// this turn. It is preserved when the call is enqueued behind a
	// busy session so the queued turn's terminal event is still
	// recognisable to the original caller. Callers that need a
	// reliable completion contract (e.g. `crush run` against a
	// session that may be busy) MUST set it; SessionID alone is
	// ambiguous when concurrent turns share the same session.
	RunID             string
	Channel           string
	HiddenUserMessage bool
	// QueueOnly delivers the call only into an active turn; on an idle
	// session Run returns ErrSessionIdle instead of starting one.
	QueueOnly bool
	Prompt    string
	// Continuation marks a turn re-queued because a compaction cut the
	// previous one short. Its prompt is already in the transcript, so it must
	// not be stored again: a stored copy is history the model re-sends on
	// every later request, and repeating the user's words back at them reads
	// as a second message they never wrote.
	Continuation bool
	// Report is a subagent report this call delivers. It is written to
	// the transcript only when delivered, so it lands where the model
	// reads it. Prompt is still what the model reads for it.
	Report           *message.SubAgentReport
	ProviderOptions  fantasy.ProviderOptions
	Attachments      []message.Attachment
	MaxOutputTokens  int64
	Temperature      *float64
	TopP             *float64
	TopK             *int64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	NonInteractive   bool

	// HostContext is what the host application had in view when the
	// prompt was sent. The model sees it with this prompt only; it is
	// not stored with the message.
	HostContext proto.HostContext
	// OnComplete, when non-nil, replaces the default RunComplete
	// publish path: the inner Run hands the terminal payload to this
	// callback instead of emitting it on the RunComplete broker. The
	// coordinator uses this hook to coalesce the unauthorized →
	// re-auth → retry chain into a single user-visible terminal
	// event, so non-interactive clients (e.g. `crush run`) don't
	// exit on a stale failed-attempt RunComplete before the
	// successful retry. It is intentionally stripped when queueing
	// a busy-session call (see Run): the originating
	// coordinator.Run has long returned by the time the queued
	// recursion drains, so falling back to the default broker
	// publish keeps the event visible to subscribers.
	OnComplete func(notify.RunComplete)
	// Accepted, when non-nil, is the accept reservation taken by
	// BeginAccepted before the call was dispatched onto a goroutine
	// (the client/server fire-and-forget path). Run consumes it under
	// dispatchMu[SessionID] once the accepted -> (cancel-on-entry |
	// queued | active) transition has been chosen. When nil
	// (in-process / local callers like AppWorkspace), behavior is
	// unchanged and no accept tracking applies.
	Accepted *AcceptedRun
	// acceptSeq carries the accept sequence of the handle that produced
	// this call after it has been enqueued and its Accepted handle
	// stripped. The queue-drain paths compare it against a session's
	// cancel mark so a follow-up queued before a cancel is dropped while
	// one queued after the cancel survives. 0 means untracked (an
	// in-process enqueue with no accept reservation), which the drain
	// paths treat as covered by any present mark, preserving the
	// pre-sequence behavior.
	acceptSeq uint64
	// OnAuthRefresh, when non-nil, is called by fantasy when a stream
	// fails with an authentication error (HTTP 401). The callback should
	// refresh credentials and return nil on success, in which case
	// fantasy retries the stream transparently. Returning an error
	// surfaces the original auth error without retry.
	OnAuthRefresh func(ctx context.Context, err *fantasy.ProviderError) error
	// channelMeta holds the attributes of the <channel> element a
	// channel-originated turn was started with, parsed once at Run
	// entry. It survives the auto-summarize continuation, whose Prompt
	// is rewritten and no longer carries the element, so the reply
	// target is not lost when a long channel turn is summarized.
	channelMeta map[string]string
}

// filterToolsForChannel scopes the tool list for a turn. A channel-originated
// turn (channel != "") sees only the originating channel server's tools plus
// all non-channel tools — the model's reach is restricted to the channel it
// is replying through, so it cannot accidentally send via a different
// messaging backend. A local turn (channel == "") keeps every tool,
// including channel server tools, so a user in the TUI can still ask the
// agent to send a message through Signal or any other enabled channel.
func filterToolsForChannel(agentTools []fantasy.AgentTool, channel string, states map[string]mcp.ClientInfo) []fantasy.AgentTool {
	if channel == "" {
		return agentTools
	}
	filtered := make([]fantasy.AgentTool, 0, len(agentTools))
	for _, agentTool := range agentTools {
		mcpTool, ok := agentTool.(interface{ MCP() string })
		if !ok {
			filtered = append(filtered, agentTool)
			continue
		}
		state, found := states[mcpTool.MCP()]
		if !found || !state.Channel || channel == mcpTool.MCP() {
			filtered = append(filtered, agentTool)
		}
	}
	return filtered
}

// ErrSessionIdle is returned for a QueueOnly call when no turn is active.
var ErrSessionIdle = errors.New("session is idle")

type SessionAgent interface {
	Run(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)
	BeginAccepted(sessionID string) *AcceptedRun
	SetTools(tools []fantasy.AgentTool)
	SetSystemPrompt(systemPrompt string)
	Cancel(sessionID string)
	CancelAll()
	HasActiveTurn(sessionID string) bool
	HasPendingWork(sessionID string) bool
	IsBusy() bool
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []string
	ClearQueue(sessionID string)
	AddNote(sessionID, text string)
	Summarize(context.Context, string, fantasy.ProviderOptions, func(context.Context, *fantasy.ProviderError) error) error
	Model() Model
	GenerateTitle(ctx context.Context, sessionID, userPrompt string)
}

type Model struct {
	Model      fantasy.LanguageModel
	CatwalkCfg catwalk.Model
	ModelCfg   config.SelectedModel
	FlatRate   bool
	// OAuth is true when the provider signs in by OAuth, so a rejected
	// credential calls for a new sign-in rather than a config change.
	OAuth bool
	// ProviderName is the provider's display name, such as "GitHub Copilot".
	ProviderName string
}

// activeCancel wraps a context.CancelFunc with a unique pointer identity.
// The pointer is used for compare-and-delete in the dispatch completion path:
// when a finishing run's deferred cleanup fires, it must only remove its own
// entry — not a newer run's entry that was installed in the window between
// the explicit Del and the function return.
type activeCancel struct {
	cancel context.CancelFunc
}

type sessionAgent struct {
	models             func() ModelSet
	systemPromptPrefix *csync.Value[string]
	systemPrompt       *csync.Value[string]
	tools              *csync.Slice[fantasy.AgentTool]

	isSubAgent bool
	sessions   session.Service
	messages   message.Service
	// cfg backs channel reply routing (config lookup + MCP tool
	// invocation). Nil in tests and subagents that never see channel
	// turns; sendChannelReply treats nil as "routing disabled".
	cfg                  *config.ConfigStore
	disableAutoSummarize bool
	// permissions answers whether the user is being asked about tool
	// calls at all. Nil in tests and anywhere that never needs to know.
	permissions permission.Service
	notify      pubsub.Publisher[notify.Notification]
	runComplete pubsub.Publisher[notify.RunComplete]
	hooks       *hooks.Runner

	messageQueue   *csync.Map[string, []SessionAgentCall]
	activeRequests *csync.Map[string, *activeCancel]
	// notes are what the host told the agent since its last turn, by
	// session, to add to the next prompt. They are not chat messages.
	notes *csync.Map[string, []string]
	// titles tracks in-flight title generation so CancelAll can drain it.
	titles *titleTracker

	// dispatchMu holds a per-session mutex that serializes the
	// accepted -> (cancel-on-entry | queued | active) transition in
	// Run against a concurrent Cancel. The lock is held only during
	// the brief handoff (no DB or LLM I/O under the lock).
	dispatchMu *csync.Map[string, *sync.Mutex]
	// acceptedRuns counts dispatched-but-not-yet-active runs per
	// session. A counter > 0 means a dispatched prompt is in flight
	// and has not yet completed the dispatch handoff in Run. Only
	// BeginAccepted increments it; only AcceptedRun.Close decrements
	// it.
	acceptedRuns *csync.Map[string, int]
	// cancelMark records, per session, a high-water accept sequence: an
	// accepted handle is canceled by it iff the handle's sequence is at
	// or below the mark. Cancel raises the mark to the latest sequence
	// assigned at cancel time, so a single Cancel covers every prompt
	// accepted-but-not-yet-active then, while a prompt accepted later
	// (higher sequence) is never poisoned. Absent or 0 means no pending
	// cancel. It is only raised by Cancel when acceptedRuns > 0, so an
	// idle Escape never records a mark.
	cancelMark *csync.Map[string, uint64]
	// dispatchMuCreate guards lazy creation of per-session entries in
	// dispatchMu so two goroutines can't race to lock different mutex
	// instances for the same session.
	dispatchMuCreate sync.Mutex
	// acceptedMu serializes increments/decrements of acceptedRuns and
	// the assignment of accept sequence numbers from acceptSeqGen. It
	// is separate from dispatchMu so AcceptedRun.Close (which may run
	// while Run holds dispatchMu for the same session) does not
	// deadlock by re-entering the dispatch lock.
	acceptedMu sync.Mutex
	// acceptSeqGen is the monotonic source of accept sequence numbers.
	// Each BeginAccepted increments it under acceptedMu and stamps the
	// returned handle, so sequences strictly increase in accept order
	// across the agent. Cancel uses its current value as the per-session
	// high-water mark.
	acceptSeqGen uint64
	// reduction holds the context-reduction settings and the learned
	// context ceiling. Nil disables the stack entirely, which is what the
	// tests that predate it rely on.
	reduction *reductionState
	// governance is the turn-governance layer: exploration pressure, nudge
	// policy and the completion gate. Nil disables it.
	governance *governanceState
	// editSafety holds the edit bounds. Nil uses the defaults.
	editSafety *config.EditSafetyOptions
	// editMu guards lastJournal: a turn installs its journal here once, and a
	// revert reads it from another goroutine.
	editMu sync.Mutex
	// lastJournal is the most recent turn's edit journal, so the turn can be
	// reverted.
	lastJournal *editsafe.Journal
	// speed is the request-latency layer. Nil disables it.
	speed *speedState
	// leverage is the long-session layer: project memory, prompt layering,
	// verification memory and reply hygiene. Nil disables it.
	leverage *leverageState
	// workingDir is the root an expected edit path is resolved against.
	workingDir string
	// compactions records the most recent compaction per session, so the UI
	// can report what it folded and what it saved.
	compactions compactionLog
}

// compactionLog keeps the last compaction of each session.
type compactionLog struct {
	mu      sync.Mutex
	records map[string]compactionRecord
}

// record stores a session's latest compaction.
func (l *compactionLog) record(rec compactionRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.records == nil {
		l.records = make(map[string]compactionRecord)
	}
	l.records[rec.SessionID] = rec
}

// get returns a session's latest compaction, if it has been compacted.
func (l *compactionLog) get(sessionID string) (compactionRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[sessionID]
	return rec, ok
}

type SessionAgentOptions struct {
	// Models hands out the agent's current models on every call, so a
	// credential refresh reaches requests already in flight.
	Models               func() ModelSet
	SystemPromptPrefix   string
	SystemPrompt         string
	IsSubAgent           bool
	DisableAutoSummarize bool
	Sessions             session.Service
	Messages             message.Service
	Permissions          permission.Service
	Cfg                  *config.ConfigStore
	Tools                []fantasy.AgentTool
	Notify               pubsub.Publisher[notify.Notification]
	RunComplete          pubsub.Publisher[notify.RunComplete]
	// Hooks runs lifecycle hooks for turns and compaction. Nil runs none.
	Hooks *hooks.Runner
	// titles is shared by a coordinator's agents so one drain covers all.
	titles *titleTracker
	// Reduction carries the context-reduction settings and digest store.
	// Nil disables the stack.
	Reduction *ReductionOptions
	// Governance carries the turn-governance settings. Nil disables it.
	Governance *GovernanceOptions
	// EditSafety carries the edit bounds. Nil uses the defaults.
	EditSafety *config.EditSafetyOptions
	// WorkingDir is the project root, used to resolve the paths a request
	// names into absolute ones.
	WorkingDir string
	// Speed carries the request-latency settings. Nil disables the layer.
	Speed *SpeedOptions
	// Leverage carries the long-session settings. Nil disables the layer.
	Leverage *LeverageOptions
}

func NewSessionAgent(
	opts SessionAgentOptions,
) SessionAgent {
	return &sessionAgent{
		models:               opts.Models,
		systemPromptPrefix:   csync.NewValue(opts.SystemPromptPrefix),
		systemPrompt:         csync.NewValue(opts.SystemPrompt),
		isSubAgent:           opts.IsSubAgent,
		sessions:             opts.Sessions,
		messages:             opts.Messages,
		cfg:                  opts.Cfg,
		disableAutoSummarize: opts.DisableAutoSummarize,
		permissions:          opts.Permissions,
		tools:                csync.NewSliceFrom(opts.Tools),
		notify:               opts.Notify,
		runComplete:          opts.RunComplete,
		hooks:                opts.Hooks,
		messageQueue:         csync.NewMap[string, []SessionAgentCall](),
		notes:                csync.NewMap[string, []string](),
		activeRequests:       csync.NewMap[string, *activeCancel](),
		titles:               cmp.Or(opts.titles, new(titleTracker)),
		reduction:            newReductionState(opts.Reduction),
		governance:           newGovernanceState(opts.Governance),
		editSafety:           opts.EditSafety,
		speed:                newSpeedState(opts.Speed),
		leverage:             newLeverageState(opts.Leverage),
		workingDir:           opts.WorkingDir,
		dispatchMu:           csync.NewMap[string, *sync.Mutex](),
		acceptedRuns:         csync.NewMap[string, int](),
		cancelMark:           csync.NewMap[string, uint64](),
	}
}

// AcceptedRun owns exactly one accept reservation taken by
// BeginAccepted. It is the only carrier of accept-state across the
// backend.runAgent / Coordinator.Run / sessionAgent.Run layers: a
// counter > 0 means a dispatched prompt is in flight and has not yet
// completed the dispatch handoff in Run. Close is the only way to
// release the reservation and is idempotent.
type AcceptedRun struct {
	agent     *sessionAgent
	sessionID string
	// seq is the monotonic accept sequence stamped by BeginAccepted. A
	// cancel covers this handle iff seq is at or below the session's
	// cancel mark, so a handle accepted after a cancel (higher seq) is
	// never poisoned by it.
	seq  uint64
	done atomic.Bool
}

// Close decrements the accept counter for this reservation. It is safe
// to call multiple times; only the first call has effect.
func (r *AcceptedRun) Close() {
	if r == nil {
		return
	}
	if !r.done.CompareAndSwap(false, true) {
		return
	}
	r.agent.endAccepted(r.sessionID)
}

// SessionID exposes the session this reservation is for so the run path
// can use it without an extra parameter.
func (r *AcceptedRun) SessionID() string {
	if r == nil {
		return ""
	}
	return r.sessionID
}

// BeginAccepted increments the accept counter for sessionID and returns
// a handle whose Close is the only way to decrement it. It is the only
// entry point that mutates acceptedRuns.
func (a *sessionAgent) BeginAccepted(sessionID string) *AcceptedRun {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, _ := a.acceptedRuns.Get(sessionID)
	a.acceptedRuns.Set(sessionID, count+1)
	a.acceptSeqGen++
	return &AcceptedRun{agent: a, sessionID: sessionID, seq: a.acceptSeqGen}
}

// endAccepted decrements the accept counter for sessionID. It is only
// called via AcceptedRun.Close. It uses a dedicated lock (not the
// per-session dispatch mutex) so it can run while Run holds dispatchMu
// for the same session without deadlocking.
//
// When the count reaches zero the session's cancel mark is dropped: no
// accepted handle remains for it to cover, and any handle accepted later
// gets a strictly higher sequence that the mark would not match anyway.
// Handles canceled on entry never reach RunComplete, so this is the only
// place that clears the mark for an all-canceled batch. Sibling handles
// covered by the same mark are serialized on the per-session dispatch
// mutex and read the mark before they Close, so this never clears it out
// from under a covered handle still waiting to enter Run.
func (a *sessionAgent) endAccepted(sessionID string) {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, ok := a.acceptedRuns.Get(sessionID)
	if !ok || count <= 1 {
		a.acceptedRuns.Del(sessionID)
		a.cancelMark.Del(sessionID)
		return
	}
	a.acceptedRuns.Set(sessionID, count-1)
}

// sessionMu returns the per-session dispatch mutex, creating it on first
// use. Creation is guarded so concurrent callers always observe the same
// mutex instance for a given session.
func (a *sessionAgent) sessionMu(sessionID string) *sync.Mutex {
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	a.dispatchMuCreate.Lock()
	defer a.dispatchMuCreate.Unlock()
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	mu := &sync.Mutex{}
	a.dispatchMu.Set(sessionID, mu)
	return mu
}

// enqueueCall appends call to the session's message queue. The
// OnComplete hook is stripped: the caller that supplied it (typically
// coordinator.Run) has its own retry/coalesce scope that ends when it
// returns, so by the time the queue drains nobody is left to consume the
// buffered terminal event. The recursive Run falls back to the default
// broker publish, which is what existing subscribers expect for queued
// turns.
func (a *sessionAgent) enqueueCall(call SessionAgentCall) {
	existing, ok := a.messageQueue.Get(call.SessionID)
	if !ok {
		existing = []SessionAgentCall{}
	}
	queued := call
	if call.Accepted != nil {
		// Preserve the accept sequence after the handle is stripped so
		// the queue-drain paths can tell a follow-up queued before a
		// cancel (covered by the mark) from one queued after it.
		queued.acceptSeq = call.Accepted.seq
	}
	queued.OnComplete = nil
	queued.Accepted = nil
	// Once queued the call is accepted: if no step folds it in, it runs as
	// its own turn rather than being refused as idle.
	queued.QueueOnly = false
	a.messageQueue.Set(call.SessionID, insertQueued(existing, queued))
}

// insertQueued appends call, except that the user's own prompts go ahead of
// queued subagent reports: delivered together they read as one message, and
// a short line trailing a long report gets missed.
func insertQueued(queue []SessionAgentCall, call SessionAgentCall) []SessionAgentCall {
	at := len(queue)
	if call.Report == nil {
		for at > 0 && queue[at-1].Report != nil {
			at--
		}
	}
	return slices.Insert(queue, at, call)
}

// drainQueueForStep partitions the session's queued calls for the current
// streaming step under the per-session dispatch mutex so the filtering is
// atomic against a concurrent Cancel: canceledBySeq requires the caller to
// hold that mutex, and evaluating it here (rather than after unlocking)
// prevents a cancel recorded between the drain and the check from being
// observed inconsistently.
//
// Calls covered by a pending cancel are dropped and returned in canceled
// for the caller to settle (see settleDroppedCalls). Uncanceled calls without
// a RunID are returned in fold to be folded into the active turn,
// preserving the existing follow-up behavior. Uncanceled calls that carry
// a RunID are left in the queue so each runs as its own turn via the
// recursive run path and publishes its own RunComplete, giving every
// RunID-bearing prompt an explicit lifecycle instead of being silently
// absorbed into another turn. fold is processed by the caller without the
// lock held.
func (a *sessionAgent) drainQueueForStep(sessionID string) (fold, canceled []SessionAgentCall) {
	dispatchLock := a.sessionMu(sessionID)
	dispatchLock.Lock()
	defer dispatchLock.Unlock()
	queuedCalls, _ := a.messageQueue.Get(sessionID)
	var keep []SessionAgentCall
	for _, queued := range queuedCalls {
		if a.canceledBySeq(sessionID, queued.acceptSeq) {
			canceled = append(canceled, queued)
			continue
		}
		if queued.RunID != "" {
			keep = append(keep, queued)
			continue
		}
		fold = append(fold, queued)
	}
	if len(keep) == 0 {
		a.messageQueue.Del(sessionID)
	} else {
		a.messageQueue.Set(sessionID, keep)
	}
	return fold, canceled
}

// settleDroppedCalls settles queued calls removed without running. A
// subagent report is written to the transcript, where the model reads it
// next turn, so cancelling a turn never loses one. Each call that carries a
// RunID gets a terminal cancelled RunComplete. A queued prompt removed
// from the queue without ever running — covered by a pending cancel, or
// cleared by Cancel/ClearQueue — would otherwise leave a caller blocked on
// that RunID: `crush run` ignores live message events and exits only on a
// RunComplete whose RunID matches. Calls without a RunID had no such waiter
// and are dropped silently as before. A detached, bounded context keeps the
// must-deliver publish alive even when the run context that triggered the
// drop is already canceled.
func (a *sessionAgent) settleDroppedCalls(drops []SessionAgentCall) {
	if len(drops) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, d := range drops {
		if d.Report != nil {
			if _, err := a.createUserMessage(ctx, d, ""); err != nil {
				slog.Error("Failed to record a dropped subagent report", "session_id", d.SessionID, "error", err)
			}
		}
		if d.RunID == "" {
			continue
		}
		a.publishRunComplete(ctx, d, notify.RunComplete{
			SessionID: d.SessionID,
			RunID:     d.RunID,
			Cancelled: true,
		})
	}
}

// clearQueueAndNotify removes all queued prompts for the session and
// publishes a terminal cancelled RunComplete for any that carried a RunID,
// so callers waiting on those RunIDs (e.g. `crush run`) are not left
// hanging when their queued prompt is discarded without running.
func (a *sessionAgent) clearQueueAndNotify(sessionID string) {
	queued, ok := a.messageQueue.Get(sessionID)
	a.messageQueue.Del(sessionID)
	if !ok {
		return
	}
	a.settleDroppedCalls(queued)
}

// clearPendingCancel removes any pending-cancel mark for sessionID. It
// takes the per-session dispatch lock so it is ordered against Cancel
// and the dispatch handoff.
func (a *sessionAgent) clearPendingCancel(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	a.cancelMark.Del(sessionID)
}

// canceledBySeq reports whether an accepted handle or queued call with
// the given accept sequence is covered by a pending cancel for the
// session. Callers must hold the session's dispatch mutex. A tracked
// sequence (seq > 0) is covered only when it is at or below the cancel
// high-water mark, so a prompt accepted after the cancel (higher seq) is
// never poisoned. An untracked sequence (seq == 0, an in-process enqueue
// with no accept reservation) is covered whenever any mark is present,
// preserving the pre-sequence behavior. The mark is not consumed: it
// stays so every sibling handle it covers observes the same cancel, and
// a later handle (higher seq) ignores it regardless.
func (a *sessionAgent) canceledBySeq(sessionID string, seq uint64) bool {
	mark, ok := a.cancelMark.Get(sessionID)
	if !ok || mark == 0 {
		return false
	}
	return seq == 0 || seq <= mark
}

// persistCanceledTurn writes the user/assistant records for a turn that
// was canceled before (or just as) streaming would have produced them.
// It creates the user message only when it was not already created by an
// earlier createUserMessage call (userMsgCreated), then writes an
// assistant message with FinishReasonCanceled. Both writes use
// context.WithoutCancel(ctx) so workspace shutdown (which cancels the run
// context) can't drop them.
func (a *sessionAgent) persistCanceledTurn(ctx context.Context, call SessionAgentCall, userMsgCreated bool) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if !userMsgCreated {
		if _, err := a.createUserMessage(writeCtx, call, ""); err != nil {
			return err
		}
	}
	largeModel := a.models().Large
	assistant, err := a.messages.Create(writeCtx, call.SessionID, message.CreateMessageParams{
		Role:      message.Assistant,
		Parts:     []message.ContentPart{},
		Model:     largeModel.ModelCfg.Model,
		ModelName: a.modelDisplayName(largeModel.ModelCfg),
		Provider:  largeModel.ModelCfg.Provider,
	})
	if err != nil {
		return err
	}
	assistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
	return a.messages.Update(writeCtx, assistant)
}

// publishRunComplete emits the authoritative terminal event for a turn.
// It honors the per-call OnComplete hook when set (so the coordinator can
// coalesce retries) and otherwise falls back to the RunComplete broker.
// ctx is used only for the bounded-blocking must-deliver publish; the
// terminal payload is supplied by the caller. This is the single emit path
// shared by the streaming defer and the cancel-on-entry early return so a
// caller waiting on RunComplete (e.g. `crush run` with a RunID) always
// observes exactly one terminal event regardless of which Run branch ends
// the turn.
func (a *sessionAgent) publishRunComplete(ctx context.Context, call SessionAgentCall, complete notify.RunComplete) {
	if call.OnComplete != nil {
		call.OnComplete(complete)
		return
	}
	if a.runComplete == nil {
		return
	}
	a.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, complete)
}

// ValidateCall performs the cheap structural validation that
// sessionAgent.Run requires before a call can be dispatched: a call must
// carry either a non-empty prompt or a text attachment, and it must name a
// session. It is exported so callers that accept a run before dispatching it
// (e.g. backend.SendMessage) can apply the same checks and keep the error
// contract consistent.
func ValidateCall(call SessionAgentCall) error {
	if call.Prompt == "" && !message.ContainsTextAttachment(call.Attachments) {
		return ErrEmptyPrompt
	}
	if call.SessionID == "" {
		return ErrSessionMissing
	}
	return nil
}

// turnFinish maps the upstream finish reason of a step onto what the
// assistant message records: the reason itself plus the banner copy the
// agent owns for the stops it has to explain (empty copy means the TUI
// supplies its own). Abnormal stops are logged because they otherwise
// look exactly like a turn that completed normally — the response just
// stops short.
func turnFinish(largeModel Model, sessionID string, reason fantasy.FinishReason) (message.FinishReason, string, string) {
	switch reason {
	case fantasy.FinishReasonStop:
		return message.FinishReasonEndTurn, "", ""
	case fantasy.FinishReasonToolCalls:
		return message.FinishReasonToolUse, "", ""
	case fantasy.FinishReasonLength:
		// The turn looks like a normal, completed answer that simply
		// stops mid-sentence, so leave a trace in the log even though
		// the TUI owns the display copy.
		slog.Warn(
			"Model response truncated at the output token limit",
			"session_id", sessionID,
			"provider", largeModel.ModelCfg.Provider,
			"model", largeModel.ModelCfg.Model,
			"finish_reason", string(reason),
		)
		return message.FinishReasonMaxTokens, "", ""
	case fantasy.FinishReasonContentFilter:
		// Provider safety classifier stopped the model
		// (Anthropic stop_reason=refusal, OpenAI content_filter).
		// The TUI owns the display copy; we only persist the
		// reason so the UI can show a REFUSED banner.
		slog.Warn(
			"Provider content filter stopped the model",
			"session_id", sessionID,
			"finish_reason", string(reason),
		)
		return message.FinishReasonContentFilter, "", ""
	case fantasy.FinishReasonError, fantasy.FinishReasonOther:
		// The provider gave up on the response from its own side
		// (e.g. insufficient_system_resource) but the stream still
		// completed, so no error reaches the run's error path. Record
		// it as an error finish, with copy generic enough for any
		// provider reason, or the turn renders as a normal completed
		// answer that stops short.
		slog.Warn(
			"Provider stopped the model",
			"session_id", sessionID,
			"provider", largeModel.ModelCfg.Provider,
			"model", largeModel.ModelCfg.Model,
			"finish_reason", string(reason),
		)
		return message.FinishReasonError, providerStoppedTurnMessage, fmt.Sprintf(
			"The provider ended this response before it was complete (finish_reason=%s). Try again, or use a different model.",
			string(reason),
		)
	}
	return message.FinishReasonUnknown, "", ""
}

func (a *sessionAgent) Run(ctx context.Context, call SessionAgentCall) (result *fantasy.AgentResult, retErr error) {
	if err := ValidateCall(call); err != nil {
		return nil, err
	}

	if call.Channel != "" && call.channelMeta == nil {
		call.channelMeta, _ = parseChannelMeta(call.Prompt)
	}

	// genCtx/cancel are the run context and its cancel func, created under
	// the per-session dispatch mutex below so a concurrent Cancel can observe
	// the activeRequests entry before the assistant message exists.
	var (
		genCtx         context.Context
		cancel         context.CancelFunc
		userMsgCreated bool
	)

	// Serialize the dispatch decision (cancel-on-entry | queued | active)
	// against a concurrent Cancel. Cancel takes the same per-session lock, so
	// every cancel observes at least one of: a cancel mark, an activeRequests
	// entry, or a messageQueue entry it then clears. Holding the lock across
	// the busy check and the active registration also makes them atomic, so
	// two concurrent in-process callers — a burst of channel events, or a
	// channel event racing a typed prompt — cannot both pass the busy check
	// and start two runs on the same session.
	sessMu := a.sessionMu(call.SessionID)
	sessMu.Lock()

	if call.Accepted != nil && a.canceledBySeq(call.SessionID, call.Accepted.seq) {
		// Cancel-on-entry: a cancel arrived while this accepted run was
		// dispatched but not yet active, and this handle's accept sequence
		// is at or below the session's cancel mark. The mark is left in
		// place so sibling handles it also covers observe the same cancel;
		// release the accept reservation, drop the lock, and persist a
		// canceled turn without entering Stream.
		//
		// This path returns before the streaming defer that publishes
		// RunComplete is installed, so emit the terminal event explicitly.
		// Without it, a caller waiting on RunComplete for this RunID (e.g.
		// `crush run`, which ignores message events and blocks on
		// RunComplete) would hang on an immediately-canceled accepted run.
		call.Accepted.Close()
		sessMu.Unlock()
		complete := notify.RunComplete{
			SessionID: call.SessionID,
			RunID:     call.RunID,
			Cancelled: true,
		}
		if err := a.persistCanceledTurn(ctx, call, false); err != nil {
			complete.Error = err.Error()
			a.publishRunComplete(ctx, call, complete)
			return nil, err
		}
		a.publishRunComplete(ctx, call, complete)
		return nil, nil
	}

	if a.HasActiveTurn(call.SessionID) {
		// Busy: an earlier prompt is active. Queue this call so it is
		// folded into (or sequenced after) the active turn, and release any
		// accept reservation. A Cancel arriving after this point sees the
		// active entry and clears the queue.
		//
		// enqueueCall strips OnComplete: the caller that supplied the hook
		// (typically coordinator.Run) has its own retry/coalesce scope that
		// ends when it returns, so by the time the queue drains nobody is
		// left to consume the buffered terminal event. The queued turn falls
		// back to the default broker publish, which is what existing
		// subscribers expect.
		a.enqueueCall(call)
		if call.Accepted != nil {
			call.Accepted.Close()
		}
		sessMu.Unlock()
		return nil, nil
	}

	if call.QueueOnly {
		if call.Accepted != nil {
			call.Accepted.Close()
		}
		sessMu.Unlock()
		return nil, ErrSessionIdle
	}

	// Idle: become the active run. Register the cancel func before dropping
	// the lock so a Cancel that arrives between here and assistant creation
	// is not lost.
	runCtx := context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)
	runCtx = withRefusalRetries(runCtx, refusalRetriesPerTurn)
	genCtx, cancel = context.WithCancel(runCtx)
	ac := &activeCancel{cancel: cancel}
	a.activeRequests.Set(call.SessionID, ac)
	if call.Accepted != nil {
		call.Accepted.Close()
	}
	sessMu.Unlock()

	defer cancel()
	// Conditional cleanup: only remove our entry if it hasn't been replaced
	// by a newer run. Without this guard, the deferred Del fires after a
	// concurrent run registers in the completion window, silently wiping
	// the new run's cancel and breaking cancellation.
	defer a.activeRequests.CompareAndDelete(call.SessionID, ac)

	// skipRunComplete is set just before the queued-recursion path so
	// the outer Run doesn't publish a RunComplete that would race
	// with — and be superseded by — the recursive call's own
	// RunComplete (each queued user prompt is its own turn and
	// publishes exactly one terminal event).
	var skipRunComplete bool
	// currentAssistant is declared here so the deferred RunComplete
	// publish below can capture the pointer that PrepareStep will
	// later (re)assign for each streaming step. The final assistant
	// message of the turn is the value reachable through this
	// pointer when the defer runs.
	var currentAssistant *message.Message
	// Drain any debounced message updates before returning. message.Service
	// already flushes synchronously on terminal updates, but a defer here
	// guarantees the contract at every Run exit (success, error, panic
	// recovery upstream) without callers needing to know.
	//
	// After the flush completes — meaning all per-message
	// Publish(UpdatedEvent) calls have fired and been buffered into
	// every subscriber's channel — publish the authoritative
	// RunComplete event for this turn. The flush-then-publish order
	// gives well-behaved clients the best chance of seeing the final
	// message event before RunComplete; the embedded Text field
	// reconciles for clients that observe the events out of order
	// (the pubsub broker fan-in does not serialize publishes from
	// different upstream brokers).
	defer func() {
		// Use a context detached from the run context: workspace
		// shutdown cancels ctx before this goroutine returns, but the
		// buffered streaming deltas must still land before the DB is
		// closed. A short timeout bounds the flush.
		flushCtx, flushCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer flushCancel()
		if flushErr := a.messages.FlushAll(flushCtx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after run", "error", flushErr)
		}
		if skipRunComplete {
			return
		}
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if retErr != nil {
			complete.Error = retErr.Error()
			complete.Cancelled = errors.Is(retErr, context.Canceled)
		} else if ctx.Err() != nil {
			complete.Cancelled = true
		}
		// Prefer the per-call hook when supplied so the coordinator
		// can coalesce retries (e.g. unauthorized → re-auth → retry)
		// into a single user-visible terminal event. The fallback
		// must-deliver publish applies bounded-blocking semantics to
		// the authoritative terminal event so a momentarily-full
		// subscriber channel can't silently drop it and hang
		// non-interactive clients waiting on RunComplete.
		a.publishRunComplete(ctx, call, complete)
	}()

	// Copy mutable fields under lock to avoid races with SetTools.
	agentTools := filterToolsForChannel(a.tools.Copy(), call.Channel, mcp.GetStates())
	largeModel := a.models().Large
	systemPrompt := a.systemPrompt.Get()
	promptPrefix := a.systemPromptPrefix.Get()
	var instructions strings.Builder

	for _, server := range mcp.GetStates() {
		if server.State != mcp.StateConnected {
			continue
		}
		if s := server.Client.InitializeResult().Instructions; s != "" {
			instructions.WriteString(s)
			instructions.WriteString("\n\n")
		}
	}

	if s := instructions.String(); s != "" {
		systemPrompt += "\n\n<mcp-instructions>\n" + s + "\n</mcp-instructions>"
	}

	if len(agentTools) > 0 {
		// Add Anthropic caching to the last tool.
		agentTools[len(agentTools)-1].SetProviderOptions(a.getCacheControlOptions())
	}

	agent := fantasy.NewAgent(
		largeModel.Model,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithTools(agentTools...),
		fantasy.WithUserAgent(userAgent),
	)

	sessionLock := sync.Mutex{}
	currentSession, err := a.sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}
	// The first real prompt of a session also carries the repository
	// snapshot, which is volatile and so cannot live in the cached system
	// prompt.
	firstTurn := !hasUserTextMessage(msgs)
	if firstTurn {
		titleCtx := context.WithoutCancel(ctx)
		a.titles.begin()
		go func() {
			defer a.titles.end()
			a.generateTitle(titleCtx, call.SessionID, call.Prompt)
		}()
	}

	// Add the user message to the session. The context sent ahead of the
	// prompt is stored with it so later requests replay the same text.
	// Project memory is retrieved before the turn so a lesson learned earlier
	// can change what this turn does, not just what it says afterwards.
	memoryBlock := a.leverage.memoryBlock(ctx, call.Prompt)
	modelContext := hostContextPrompt(call.HostContext) + notesPrompt(a.takeNotes(call.SessionID)) + memoryBlock
	if firstTurn {
		modelContext += a.gitStatusContext(ctx)
	}
	if !call.Continuation {
		if _, err = a.createUserMessage(ctx, call, modelContext); err != nil {
			return nil, err
		}
	}
	userMsgCreated = true

	// Add the session to the context. The run context (genCtx) and its
	// cancel func were already created and registered under the dispatch
	// mutex above for both the accepted and in-process paths.
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)

	// The turn's edit state: what this turn owns, what it changed, and the
	// bounds a change must respect. An edit to a file the request did not
	// name is allowed until the cap, and every change is journalled so the
	// turn can be reverted.
	editTurn := tools.NewEditTurnState(tools.EditTurnConfig{
		Limits:         a.editLimits(),
		ExpectedPaths:  a.expectedEditPaths(msgs, call),
		InterceptShell: a.interceptShellEdits(),
		Journal:        a.journalEdits(),
	})
	ctx = tools.WithEditTurnState(ctx, editTurn)
	if editTurn.Journal != nil {
		a.editMu.Lock()
		a.lastJournal = editTurn.Journal
		a.editMu.Unlock()
	}

	history, files := a.preparePrompt(msgs, largeModel.CatwalkCfg.Capabilities.Vision, largeModel.CatwalkCfg.Capabilities.Audio, call.Attachments...)

	// Shrink the prompt before it is sent. The stack learns its ceiling from
	// real provider overflows, so it only bites on sessions that actually
	// grew too large.
	history, reduction := a.reducePrompt(systemPrompt, history, int(largeModel.CatwalkCfg.ContextWindow))
	if reduction.Before > 0 {
		slog.Debug("Context reduction applied",
			"session_id", call.SessionID,
			"ceiling", reduction.Ceiling,
			"tokens_before", reduction.Before,
			"tokens_after", reduction.After,
			"pointer_stubs", reduction.PointerStubs,
			"digested", reduction.Digested,
			"deduplicated", reduction.Deduplicated,
			"dropped_messages", reduction.DroppedMessages)
	}

	startTime := time.Now()
	a.eventPromptSent(call.SessionID)
	a.fireTurnHook(genCtx, call, hooks.Event{Name: hooks.EventTurnStart})

	var stepMessages []fantasy.Message
	var shouldSummarize bool
	// Written from OnToolCall and read from OnToolResult, which fantasy
	// invokes concurrently for parallel tool calls.
	sanitizedToolCalls := csync.NewMap[string, bool]()
	// Full names of tool calls that completed without error this turn.
	// Written from OnToolResult, which runs concurrently for parallel
	// tool calls, and read after Stream returns, where sendChannelReply
	// uses it to tell whether the model already replied on the
	// originating channel.
	completedToolCalls := csync.NewMap[string, struct{}]()
	// Calls that got a reply this turn; the rest are settled when it ends.
	answeredToolCalls := csync.NewMap[string, struct{}]()
	// Don't send MaxOutputTokens if 0 — some providers (e.g. LM Studio) reject it
	var maxOutputTokens *int64
	if call.MaxOutputTokens > 0 {
		maxOutputTokens = &call.MaxOutputTokens
	}
	// Turn governance. The governor scores exploration and the nudge engine
	// names the failure; both report into the same per-turn tracker so a note
	// can be injected into the next round's prompt.
	turnGov := a.governorFor()
	rounds := &roundTracker{}
	truth := newReasoningTruth()
	turnSpeed := a.speedFor()
	result, err = agent.Stream(genCtx, fantasy.AgentStreamCall{
		Prompt:           modelContext + message.PromptWithTextAttachments(strings.TrimSpace(call.Prompt), call.Attachments),
		Files:            files,
		Messages:         history,
		Headers:          sessionHeaders(call.SessionID),
		ProviderOptions:  call.ProviderOptions,
		MaxOutputTokens:  maxOutputTokens,
		TopP:             call.TopP,
		Temperature:      call.Temperature,
		PresencePenalty:  call.PresencePenalty,
		TopK:             call.TopK,
		FrequencyPenalty: call.FrequencyPenalty,
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = options.Messages
			for i := range prepared.Messages {
				prepared.Messages[i].ProviderOptions = nil
			}
			turnGov.beginRound()

			// Use latest tools (updated by SetTools when MCP tools
			// change), filtered for the session's channel and minus MCP
			// servers disabled for this repository.
			prepared.Tools = wrapToolsWithResultCache(
				a.filterDisabledMCPTools(
					callContext,
					filterToolsForChannel(a.tools.Copy(), call.Channel, mcp.GetStates()),
				),
				turnSpeed,
			)

			// Drain queued follow-up prompts for this step. Calls covered
			// by a cancel recorded while they sat in the queue are dropped:
			// a cancel that arrived after a prompt was queued must not let
			// it run as part of this step. Coverage is per-call by accept
			// sequence so a follow-up queued after the cancel (higher seq)
			// is not dropped. A dropped prompt carrying a RunID still gets
			// its terminal cancelled RunComplete so a caller waiting on it
			// does not hang. Uncanceled prompts without a RunID are folded
			// into this turn; uncanceled prompts with a RunID are left
			// queued so each runs as its own turn (with its own
			// RunComplete) via the recursive run path below.
			fold, canceled := a.drainQueueForStep(call.SessionID)
			a.settleDroppedCalls(canceled)
			for _, queued := range fold {
				userMessage, createErr := a.createUserMessage(callContext, queued, "")
				if createErr != nil {
					return callContext, prepared, createErr
				}
				prepared.Messages = append(prepared.Messages, userMessage.ToAIMessage()...)
			}

			prepared.Messages = dropEmptyToolMedia(prepared.Messages)
			prepared.Messages = a.workaroundProviderMediaLimitations(prepared.Messages, largeModel)

			lastSystemRoleInx := 0
			systemMessageUpdated := false
			for i, msg := range prepared.Messages {
				// Only add cache control to the last message.
				if msg.Role == fantasy.MessageRoleSystem {
					lastSystemRoleInx = i
				} else if !systemMessageUpdated {
					prepared.Messages[lastSystemRoleInx].ProviderOptions = a.getCacheControlOptions()
					systemMessageUpdated = true
				}
				// Than add cache control to the last 2 messages.
				if i > len(prepared.Messages)-3 {
					prepared.Messages[i].ProviderOptions = a.getCacheControlOptions()
				}
			}

			if promptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(promptPrefix)}, prepared.Messages...)
			}

			// Steering notes are the whole point of the governor: they only
			// work if the model reads them, and the one place it always reads
			// is the prompt it is about to receive.
			//
			// They are appended, never prepended. The provider caches the
			// longest identical prefix of a request, so inserting anything
			// before the system prompt changes the first byte and makes the
			// whole prompt uncached, which costs more time than the note can
			// save. Appending leaves every earlier byte untouched.
			if notes := turnGov.takeNotes(); len(notes) > 0 {
				prepared.Messages = append(prepared.Messages, fantasy.NewUserMessage(governanceNotesText(notes)))
			}

			sessionLock.Lock()
			stepMessages = cloneFantasyMessages(prepared.Messages)
			sessionLock.Unlock()

			var assistantMsg message.Message
			assistantMsg, err = a.messages.Create(callContext, call.SessionID, message.CreateMessageParams{
				Role:      message.Assistant,
				Parts:     []message.ContentPart{},
				Model:     largeModel.ModelCfg.Model,
				ModelName: a.modelDisplayName(largeModel.ModelCfg),
				Provider:  largeModel.ModelCfg.Provider,
			})
			if err != nil {
				return callContext, prepared, err
			}
			callContext = context.WithValue(callContext, tools.MessageIDContextKey, assistantMsg.ID)
			callContext = context.WithValue(callContext, tools.ChannelContextKey, call.Channel)
			callContext = context.WithValue(callContext, tools.SupportsImagesContextKey, largeModel.CatwalkCfg.Capabilities.Vision)
			callContext = context.WithValue(callContext, tools.ModelNameContextKey, largeModel.CatwalkCfg.Name)
			currentAssistant = &assistantMsg
			return callContext, prepared, err
		},
		OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
			currentAssistant.AppendReasoningContent(reasoning.Text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningDelta: func(id string, text string) error {
			turnGov.observeReasoning(text)
			currentAssistant.AppendReasoningContent(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// handle anthropic signature
			if anthropicData, ok := reasoning.ProviderMetadata[anthropic.Name]; ok {
				if reasoning, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok {
					currentAssistant.AppendReasoningSignature(reasoning.Signature)
				}
			}
			if googleData, ok := reasoning.ProviderMetadata[google.Name]; ok {
				if reasoning, ok := googleData.(*google.ReasoningMetadata); ok {
					currentAssistant.AppendThoughtSignature(reasoning.Signature, reasoning.ToolID)
				}
			}
			if openaiData, ok := reasoning.ProviderMetadata[openai.Name]; ok {
				if reasoning, ok := openaiData.(*openai.ResponsesReasoningMetadata); ok {
					currentAssistant.SetReasoningResponsesData(reasoning)
				}
			}
			currentAssistant.FinishThinking()
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnTextDelta: func(id string, text string) error {
			// Strip leading newline from initial text content. This is is
			// particularly important in non-interactive mode where leading
			// newlines are very visible.
			if len(currentAssistant.Parts) == 0 {
				text = strings.TrimPrefix(text, "\n")
			}

			currentAssistant.AppendContent(text)
			turnGov.observeProse(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolInputStart: func(id string, toolName string) error {
			toolCall := message.ToolCall{
				ID:               id,
				Name:             toolName,
				ProviderExecuted: false,
				Finished:         false,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnRetry: func(err *fantasy.ProviderError, delay time.Duration) {
			slog.Warn("Provider request failed, retrying", providerRetryLogFields(err, delay)...)
			// Reset streamed content so the retried response doesn't
			// concatenate with partial content from the failed attempt.
			// On the final attempt (no more retries), any partial content
			// stays in the message as useful context beneath the error.
			currentAssistant.ResetStreamedContent()
			if updateErr := a.messages.Update(genCtx, *currentAssistant); updateErr != nil {
				slog.Error("Failed to reset message on retry", "error", updateErr)
			}
		},
		OnAuthRefresh: call.OnAuthRefresh,
		ModelProvider: func() fantasy.LanguageModel {
			slog.Info("ModelProvider called",
				"provider", largeModel.ModelCfg.Provider,
				"model", largeModel.ModelCfg.Model)
			return a.currentBuildOf(largeModel)
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			input, wasSanitized := sanitizeToolInput(tc.ToolName, tc.ToolCallID, tc.Input)
			if wasSanitized {
				sanitizedToolCalls.Set(tc.ToolCallID, true)
			}
			// A repeat the governor has already answered is refused rather
			// than run again: the result cannot differ, and the round that
			// would have carried it is spent for nothing.
			if turnGov.observeToolCall(tc.ToolName, input) {
				sanitizedToolCalls.Set(tc.ToolCallID, true)
			}
			toolCall := message.ToolCall{
				ID:               tc.ToolCallID,
				Name:             tc.ToolName,
				Input:            input,
				ProviderExecuted: false,
				Finished:         true,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			toolResult := a.convertToToolResult(result)
			if _, ok := sanitizedToolCalls.Get(result.ToolCallID); ok {
				toolResult.Content = "Tool call failed: arguments were not valid JSON. Please check your tool call format and try again."
				toolResult.IsError = true
			} else if toolResult.IsError && genCtx.Err() != nil {
				// The turn was cancelled out from under the tool.
				toolResult.Content = interruptedToolCallResult
			}
			if !toolResult.IsError && toolResult.Name != "" {
				completedToolCalls.Set(toolResult.Name, struct{}{})
			}
			turnGov.observeToolResult(toolResult.Name, toolCallInputByID(toolResult.ToolCallID, currentAssistant), toolResult.IsError, toolResult.Content)
			// A mutation invalidates every cached read: the workspace the
			// cached results describe no longer exists.
			if !toolResult.IsError && newFilesTouchedByName(toolResult.Name) {
				turnSpeed.invalidate()
			}
			// Use parent ctx instead of genCtx to ensure the message is created
			// even if the request is canceled mid-stream
			_, createMsgErr := a.messages.Create(ctx, currentAssistant.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					toolResult,
				},
			})
			if createMsgErr == nil {
				answeredToolCalls.Set(result.ToolCallID, struct{}{})
			}
			return createMsgErr
		},
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			for _, w := range stepResult.Warnings {
				slog.Warn("Provider warning", "type", w.Type, "message", w.Message)
			}
			finishReason, finishMessage, finishDetails := turnFinish(largeModel, call.SessionID, stepResult.FinishReason)
			// If a tool result halted the turn (e.g. a hook halt or a
			// permission denial), the step ends on FinishReasonToolCalls but
			// the model will not be called again. Treat it as the end of the
			// turn so the UI can render the assistant footer.
			if finishReason == message.FinishReasonToolUse {
				for _, tr := range stepResult.Content.ToolResults() {
					if tr.StopTurn {
						finishReason = message.FinishReasonEndTurn
						break
					}
				}
			}
			currentAssistant.AddFinish(finishReason, finishMessage, finishDetails)
			currentAssistant.PrismModelID, currentAssistant.PrismModelName = extractPrismModel(stepResult.ProviderMetadata)
			currentAssistant.PrismHypercreditSavings, currentAssistant.PrismDollarSavings = extractPrismSavings(stepResult.ProviderMetadata)
			// Did the provider actually honour the reasoning parameters this
			// turn sent? A provider that accepts and ignores them makes every
			// effort knob meaningless, so the fact is recorded rather than
			// assumed.
			sentReasoning := call.ProviderOptions != nil
			gotReasoning := false
			for _, part := range stepResult.Content {
				if _, ok := part.(fantasy.ReasoningContent); ok {
					gotReasoning = true
					break
				}
			}
			truth.observeResponse(sentReasoning, gotReasoning)

			sessionLock.Lock()
			defer sessionLock.Unlock()

			updatedSession, getSessionErr := a.sessions.Get(ctx, call.SessionID)
			if getSessionErr != nil {
				return getSessionErr
			}
			usage, estimated := fallbackStepUsage(stepMessages, stepResult)
			if cached := cacheReadTokens(usage); cached > 0 {
				slog.Debug("Provider served part of the prompt from cache",
					"session_id", call.SessionID, "cache_read_tokens", cached, "input_tokens", usage.InputTokens)
			}
			pricingModel := a.modelForStepCost(largeModel, stepResult.ProviderMetadata)
			a.updateSessionUsage(pricingModel, &updatedSession, usage, a.openrouterCost(stepResult.ProviderMetadata), estimated)
			_, sessionErr := a.sessions.Save(ctx, updatedSession)
			if sessionErr != nil {
				return sessionErr
			}
			currentSession = updatedSession

			// Evaluate governance for the round that just finished. The
			// facts come from what the round actually did, not from what the
			// model said about it.
			if turnGov != nil {
				calls := len(stepResult.Content.ToolCalls())
				mutated := newFilesTouched(stepResult.Content)
				rounds.observe(calls, mutated, calls > 0 && onlyJobChecks(stepResult.Content))
				if mutated {
					rounds.edits++
				}
				planning := len(currentSession.Todos) > 0
				turnGov.recordReasoning(
					postDecisionTail(stepResult.Content),
					mutated,
					reasoningRestarts(stepResult.Content),
				)
				turnGov.endRound(rounds.outcome(
					sessionTodos(currentSession.Todos),
					planning,
					nudge.LooksLikeRecap(currentAssistant.Content().Text) && calls == 0,
					repeatedProseCount(currentAssistant.Content().Text),
				))
			}

			return a.messages.Update(genCtx, *currentAssistant)
		},
		StopWhen: []fantasy.StopCondition{
			func(_ []fantasy.StepResult) bool {
				if a.disableAutoSummarize {
					return false
				}
				cw := int64(largeModel.CatwalkCfg.ContextWindow)
				// Only the prompt occupies the window the next request has
				// to fit into. Adding the answer's tokens counted output
				// that is already part of that prompt on the next round, so
				// the trigger fired early and at an unpredictable size.
				tokens := currentSession.PromptTokens

				// The configured trigger wins when it is set, because it is
				// the number the user chose for cost, not just for survival.
				// Waiting until the window is nearly full means every request
				// carries the whole transcript, which is what makes a long
				// session expensive rather than slow.
				if trigger := a.compactionTriggerTokens(); trigger > 0 {
					if tokens >= trigger && (!a.reduction.enabled() || a.reduction.canCompact()) {
						shouldSummarize = true
						return true
					}
					return false
				}

				// If context window is unknown (0), skip auto-summarize
				// to avoid immediately truncating custom/local models.
				if cw == 0 {
					return false
				}
				remaining := cw - tokens
				var threshold int64
				if cw > largeContextWindowThreshold {
					threshold = largeContextWindowBuffer
				} else {
					threshold = int64(float64(cw) * smallContextWindowRatio)
				}
				if remaining <= threshold {
					shouldSummarize = true
					return true
				}
				return false
			},
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
			func(steps []fantasy.StepResult) bool {
				// The round cap is off by default: the governor steers, it
				// does not cut. When a user turns it on, it ends the turn
				// rather than looping forever.
				limit := 0
				if a.governance != nil {
					limit = a.governance.maxRounds()
				}
				return limit > 0 && len(steps) >= limit
			},
		},
	})

	a.eventPromptResponded(call.SessionID, time.Since(startTime).Truncate(time.Second))

	// Final writes use a context detached from the run: workspace shutdown
	// cancels ctx before agent goroutines finish, and the turn's ending
	// must still land.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if currentAssistant != nil {
		answered := func(id string) bool {
			_, ok := answeredToolCalls.Get(id)
			return ok
		}
		if settleErr := a.settleTurn(cleanupCtx, currentAssistant, answered, err); settleErr != nil {
			slog.Error("Failed to settle unanswered tool calls",
				"session_id", call.SessionID, "error", settleErr)
		}
	}

	if err != nil {
		isCancelErr := errors.Is(err, context.Canceled)
		// A provider that rejects the prompt for being too long proves the
		// ceiling was lower than advertised. Recording it here means the
		// next attempt (or the next turn) is sized to fit instead of failing
		// the same way forever.
		if !isCancelErr && contextreduction.IsContextOverflow(err) {
			if a.reduction != nil {
				a.reduction.learnOverflow(promptTokens(history), err)
			}
		}
		// A provider that refuses a reasoning parameter must not leave the
		// turn behaving as if the parameter took effect. Recording the drop
		// and telling the model keeps the change from being read as a change
		// in the task.
		if !isCancelErr {
			if parameter, dropped := dropsReasoningParameter(err); dropped {
				truth.noteDropped(parameter)
				a.AddNote(call.SessionID, reasoningDroppedNote(parameter))
				slog.Info("Provider refused a reasoning parameter",
					"session_id", call.SessionID, "parameter", parameter)
			}
		}
		slog.Info("Agent stream returned error",
			"error", err.Error(),
			"error_type", fmt.Sprintf("%T", err),
			"oauth", largeModel.OAuth,
			"is_cancel", isCancelErr)
		// Fire once the turn's final state is written, whichever return
		// below is taken, and outlive the cancelled run context.
		defer a.fireTurnHook(context.WithoutCancel(ctx), call, turnEndEvent(err, currentAssistant))
		if currentAssistant == nil {
			// Cancel-before-assistant-creation window: the run was
			// canceled after activeRequests.Set but before PrepareStep
			// created the assistant message. Without this, the turn
			// would return with no FinishReasonCanceled marker and no
			// user-visible record. The user message was already created
			// above, so persistCanceledTurn only writes the assistant
			// record.
			if isCancelErr {
				if persistErr := a.persistCanceledTurn(ctx, call, userMsgCreated); persistErr != nil {
					return nil, persistErr
				}
			}
			return result, err
		}
		// Ensure we finish thinking on error to close the reasoning state.
		currentAssistant.FinishThinking()
		var fantasyErr *fantasy.Error
		var providerErr *fantasy.ProviderError
		var requestTimedOutErr *requestTimeoutError
		const defaultTitle = "Provider Error"
		linkStyle := lipgloss.NewStyle().Foreground(tone.Guac).Underline(true)
		if isCancelErr {
			currentAssistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
		} else if errors.Is(err, errRefused) {
			// Every retry was refused; keep the refusal banner rather than
			// reporting a generic provider error.
			currentAssistant.AddFinish(message.FinishReasonContentFilter, "", "")
		} else if errors.As(err, &requestTimedOutErr) {
			// Checked before the provider branches so a deadline our own
			// request timeout imposed is never reported as a provider error.
			currentAssistant.AddFinish(message.FinishReasonError, "Request timed out", requestTimedOutErr.userMessage())
		} else if largeModel.OAuth && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized {
			currentAssistant.AddFinish(message.FinishReasonError, "Unauthorized", signInAgainMessage(largeModel))
		} else if errors.As(err, &providerErr) {
			if providerErr.Message == "The requested model is not supported." {
				url := "https://github.com/settings/copilot/features"
				link := linkStyle.Hyperlink(url, "id=copilot").Render(url)
				currentAssistant.AddFinish(
					message.FinishReasonError,
					"Copilot model not enabled",
					fmt.Sprintf("%q is not enabled in Copilot. Go to the following page to enable it. Then, wait 5 minutes before trying again. %s", largeModel.CatwalkCfg.Name, link),
				)
			} else {
				currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(providerErr.Title), defaultTitle), providerErr.Message)
			}
		} else if errors.As(err, &fantasyErr) {
			currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(fantasyErr.Title), defaultTitle), fantasyErr.Message)
		} else if fantasy.IsTransportError(err) {
			wrapped := fantasy.NewTransportError(err)
			currentAssistant.AddFinish(message.FinishReasonError, stringext.Capitalize(wrapped.Title), wrapped.Message)
		} else {
			currentAssistant.AddFinish(message.FinishReasonError, defaultTitle, err.Error())
		}
		updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
		if updateErr != nil {
			return nil, updateErr
		}
		// A channel-originated turn has no caller watching the error, so
		// tell the channel side something went wrong instead of leaving
		// the sender hanging. sendChannelReply detaches from the run
		// context so the notice survives a provider error that tore it down.
		if channelErrorReplyWanted(call.Channel, err) {
			a.sendChannelReply(ctx, call,
				"Something went wrong while handling your message. Please try again.",
				completedToolCalls.Copy())
		}
		return nil, err
	}

	// A turn cut short for compaction with tool calls still pending is
	// continued by a queued follow-up, which replies and fires TurnEnd
	// when it actually finishes.
	continued := false
	if shouldSummarize {
		a.activeRequests.Del(call.SessionID)
		summarizeErr := a.summarize(genCtx, call.SessionID, compactTriggerAuto, call.ProviderOptions, call.OnAuthRefresh)
		switch {
		case errors.Is(summarizeErr, ErrCompactionBlocked):
			// Uncompacted, the next step would only hit the limit
			// again, so the turn ends here.
			slog.Info("PreCompact hook blocked compaction", "session_id", call.SessionID)
		case summarizeErr != nil:
			return nil, summarizeErr
		case len(currentAssistant.ToolCalls()) > 0:
			existing, _ := a.messageQueue.Get(call.SessionID)
			// The prompt is re-queued unchanged. It used to be rewritten to
			// announce the interruption, but that text is a transient
			// instruction, not something the user said: stored as a message it
			// stayed in history and was re-sent on every request afterwards,
			// with a new copy each time compaction fired. The compaction note
			// already explains the fold, so the wrapper is redundant.
			//
			// The queued turn is not the report, so it must not re-deliver one.
			call.Report = nil
			// Its prompt is already in the transcript. Storing it again would
			// show the user their own words a second time and pay for the copy
			// on every later request.
			call.Continuation = true
			a.messageQueue.Set(call.SessionID, append(existing, call))
			continued = true
		}
	}
	finished := currentAssistant != nil && !continued

	// The completion gate: a finished reply that claims work this turn's tool
	// results do not support is refused once, with a follow-up asking for the
	// work. Without the follow-up the claim is the last thing the user sees;
	// with it, the next round can actually do the job.
	if finished && a.governance.completionGateEnabled() {
		verdict := evaluateCompletion(completionEvidence{
			ToolCalls:          len(currentAssistant.ToolCalls()),
			Edits:              rounds.edits,
			Runs:               turnGov.runCount(),
			Prose:              currentAssistant.Content().Text,
			ClaimsEdit:         ClaimsEdit(currentAssistant.Content().Text),
			ClaimsVerification: ClaimsVerification(currentAssistant.Content().Text),
		})
		if verdict.Blocked {
			if a.governance.gateBlocksUsed(call.SessionID, completionGateMaxBlocks) < completionGateMaxBlocks {
				slog.Info("Completion gate refused an unsupported finish",
					"session_id", call.SessionID, "reason", verdict.Reason)
				followUp := call
				followUp.Prompt = completionGateFollowUp(verdict.Reason)
				followUp.Report = nil
				existing, _ := a.messageQueue.Get(call.SessionID)
				a.messageQueue.Set(call.SessionID, append(existing, followUp))
				continued = true
				finished = false
			} else {
				slog.Warn("Completion gate reached its retry limit; ending the turn",
					"session_id", call.SessionID, "reason", verdict.Reason)
			}
		}
	}

	if finished {
		turnSpeed.logStats(call.SessionID)
	}

	// Clean template debris out of the answer before the user sees it. This is
	// presentation only: the reasoning behind the answer is untouched.
	if finished && currentAssistant != nil {
		if cleaned := a.leverage.cleanReply(currentAssistant.Content().Text); cleaned != currentAssistant.Content().Text {
			currentAssistant.SetContent(cleaned)
			_ = a.messages.Update(ctx, *currentAssistant)
		}
	}

	// Record the turn's reasoning behaviour. This is the measured half of the
	// anti-overthinking story: tail length and restart density are what tell
	// an operator the model is spinning, so tuning is driven by numbers.
	if finished && turnGov != nil {
		if score := turnGov.scoreSnapshot(); score.Rounds > 0 && a.governance.reasoningMetricsEnabled() {
			slog.Info("Turn reasoning metrics",
				"session_id", call.SessionID,
				"rounds", score.Rounds,
				"thinking_rounds", score.ThinkingRounds,
				"progress_rounds", score.ProgressRounds,
				"reason_chars", score.ReasonChars,
				"restarts", score.Restarts,
				"restart_density", score.RestartDensity(),
				"worst_tail_chars", score.WorstTailChars,
				"tail_ratio", score.TailRatio,
				"needs_recovery", score.NeedsRecovery())
		}
		if honored, observed := truth.honorsReasoning(); observed {
			slog.Info("Reasoning parameters",
				"session_id", call.SessionID,
				"honored", honored)
		}
	}

	// Route the finished turn's response back to the channel it came
	// from. Runs before the busy state is released so a follow-up push
	// queued behind this turn cannot overtake its reply. The reply text
	// carries a closing note when the response stopped short, because a
	// channel recipient sees only the text, never the TUI banner.
	if finished {
		a.sendChannelReply(ctx, call, channelReplyText(currentAssistant), completedToolCalls.Copy())
	}

	// Release active request before publishing the notification.
	// TUI handlers poll IsSessionBusy() and only re-evaluate when a
	// tea.Msg arrives, so the cleanup must precede the notify or
	// subscribers see stale busy state at the moment of receipt.
	a.activeRequests.Del(call.SessionID)
	cancel()

	if finished {
		a.fireTurnHook(ctx, call, turnEndEvent(nil, currentAssistant))
	}

	// Send notification that agent has finished its turn (skip for
	// nested/non-interactive sessions).
	if !call.NonInteractive && a.notify != nil {
		a.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID:    call.SessionID,
			SessionTitle: currentSession.Title,
			Type:         notify.TypeAgentFinished,
		})
	}

	// Hand off to the next queued prompt (if any) under dispatchMu so
	// the transition from this finished run to the queued run is atomic
	// against a concurrent Cancel. activeRequests for this session was
	// just deleted above, so without the lock there is a window in
	// which the session looks idle and a cancel becomes a no-op that
	// fails to stop the queued prompt. Holding the lock lets us observe
	// a pending cancel recorded against the session and drop the queue
	// instead of running it, and (for the recursion) hand a fresh
	// accept reservation to the dequeued call so acceptedRuns stays > 0
	// across the recursive Run's own dispatch handoff — keeping the
	// session observable to Cancel for the entire transition and
	// closing the dequeue -> re-register window.
	mu := a.sessionMu(call.SessionID)
	mu.Lock()
	queuedMessages, _ := a.messageQueue.Get(call.SessionID)
	if mark, ok := a.cancelMark.Get(call.SessionID); ok && mark > 0 && len(queuedMessages) > 0 {
		// A cancel was recorded for this session (e.g. it arrived while
		// this run was active and follow-ups had been queued). Drop the
		// queued prompts it covers (accept sequence at or below the
		// mark, or untracked); keep any queued after the cancel (higher
		// sequence) so they still run.
		var kept, dropped []SessionAgentCall
		for _, q := range queuedMessages {
			if q.acceptSeq == 0 || q.acceptSeq <= mark {
				dropped = append(dropped, q)
				continue
			}
			kept = append(kept, q)
		}
		queuedMessages = kept
		a.messageQueue.Set(call.SessionID, kept)
		a.settleDroppedCalls(dropped)
	}
	if len(queuedMessages) == 0 {
		// No queued work. Clear the cancel mark only when no accepted
		// run remains in flight that it might still cover; otherwise a
		// sibling prompt (sequence at or below the mark) waiting to
		// enter Run would lose its cancellation. When accepted runs are
		// gone, this also clears a stale mark so it can't catch a
		// future run.
		a.messageQueue.Del(call.SessionID)
		a.acceptedMu.Lock()
		inFlight, _ := a.acceptedRuns.Get(call.SessionID)
		a.acceptedMu.Unlock()
		if inFlight == 0 {
			a.cancelMark.Del(call.SessionID)
		}
		mu.Unlock()
		return result, err
	}
	// There are queued messages, restart the loop. Suppress the outer
	// defer's emit: it would otherwise observe the recursive Run's retErr
	// (named-return clobbering through the return below) against this
	// turn's MessageID/Text and publish a mixed, racing event.
	skipRunComplete = true
	// Decide whether this turn still owes its own terminal RunComplete.
	// Each submitted prompt with a RunID has its own lifecycle, so a turn
	// that is finished and handing off to a *different* queued prompt must
	// publish its own RunComplete here — leaving it to the recursive turn
	// (which carries a different RunID) would hang a caller waiting on
	// this turn's RunID. The exception is the summarize-continuation path,
	// which re-queues this same call (same RunID) to resume after a
	// summary; in that case the eventual terminal turn for this RunID
	// publishes, so publishing now would double-emit.
	outerOwesRunComplete := call.RunID != ""
	if outerOwesRunComplete {
		for _, q := range queuedMessages {
			if q.RunID == call.RunID {
				outerOwesRunComplete = false
				break
			}
		}
	}
	firstQueuedMessage := queuedMessages[0]
	a.messageQueue.Set(call.SessionID, queuedMessages[1:])
	// Reserve a fresh accept for the dequeued prompt before dropping the
	// lock so acceptedRuns > 0 across the handoff into the recursive
	// Run. This closes the window between this dequeue and the recursive
	// Run registering its activeRequests entry: a cancel arriving in
	// that window now records a pending cancel (acceptedRuns > 0) that
	// the recursive Run's accepted path observes as cancel-on-entry.
	firstQueuedMessage.Accepted = a.BeginAccepted(call.SessionID)
	mu.Unlock()
	if outerOwesRunComplete {
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if ctx.Err() != nil {
			complete.Cancelled = true
		}
		a.publishRunComplete(ctx, call, complete)
	}
	return a.Run(ctx, firstQueuedMessage)
}

// Summarize compacts the session on the user's request. It returns
// ErrCompactionBlocked when a PreCompact hook blocks it.
func (a *sessionAgent) Summarize(ctx context.Context, sessionID string, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error) error {
	if a.HasPendingWork(sessionID) {
		return ErrSessionBusy
	}
	return a.summarize(ctx, sessionID, compactTriggerManual, opts, onAuthRefresh)
}

// summarize compacts the session so the next request fits. It is deliberately
// local: measuring the transcript, choosing a recent tail and stamping a note
// costs microseconds, while asking a model to write the summary costs a full
// round-trip over the very transcript that is too large to send.
//
// The transcript is not modified. A compaction records where the visible
// history now starts, so every message stays in the database and the fold is
// reversible; the UI can still scroll back through what was folded.
//
// A continuation brief is reused when a previous fold already wrote one for
// this prefix, but it is never generated here. Producing one is a background
// job: paying for it inside a turn would reintroduce exactly the stall this
// path exists to avoid.
func (a *sessionAgent) summarize(ctx context.Context, sessionID, trigger string, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error) error {
	_ = opts
	_ = onAuthRefresh

	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	if pre := a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPreCompact, SessionID: sessionID, Trigger: trigger}); pre.Blocked() {
		return fmt.Errorf("%w: %s", ErrCompactionBlocked, pre.Reason)
	}

	// The tail a compaction keeps is measured on the same message shape the
	// next request sends, so the size the planner compares against the window
	// is the size the provider will see.
	aiMsgs, _ := a.preparePrompt(msgs, false, false)

	brief := ""
	if a.reduction.enabled() {
		// A brief for this exact prefix may already exist from an earlier
		// fold. Reusing it keeps the invariants the earliest turns
		// established; generating one waits for the background digest.
		brief = a.reduction.briefFor(aiMsgs)
	}

	// A request is the system prompt, the tool definitions and the
	// conversation. The first two are paid on every turn, so the budget has to
	// count them; sizing only the messages made a 25K budget arrive as 30K.
	overhead := contextreduction.EstimateTokens(a.systemPrompt.Get()) +
		contextreduction.EstimateTokens(a.systemPromptPrefix.Get()) +
		tools.DefinitionBytes(a.tools.Copy())/contextreduction.BytesPerToken
	compactOpts := a.reduction.compactOptionsWithOverhead(overhead)
	var plan contextreduction.Plan
	if a.reduction.enabled() {
		_, plan = contextreduction.CompactLocally("", aiMsgs, brief, compactOpts)
	} else {
		plan = contextreduction.Plan{Reason: "context reduction disabled"}
	}
	if !plan.Compact {
		// Nothing to fold, but the compaction still ran: the hooks fire so a
		// script that relies on them sees the attempt, as it always has.
		slog.Info("Compaction found nothing to fold",
			"session_id", sessionID, "reason", plan.Reason, "tokens", plan.Total)
		a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPostCompact, SessionID: sessionID, Trigger: trigger})
		return nil
	}

	// plan indexes the prompt shape, which prepends a reminder message, so
	// the kept count is what maps back to the stored transcript. Taking it
	// from the end is also what keeps the newest work: a tail is the recent
	// conversation, never an older slice of it.
	kept := min(plan.KeptMessages, len(msgs))
	if kept <= 0 || kept >= len(msgs) {
		return nil
	}

	// The marker is the first kept message, and the note that explains the
	// fold is memoized against it. Nothing is deleted: the window moved, the
	// history did not.
	marker := msgs[len(msgs)-kept]
	note := contextreduction.SummaryNote(brief, len(msgs)-kept, kept)
	if a.reduction.enabled() {
		a.reduction.rememberNote(marker.ID, note)
	}

	builtTokens := contextreduction.Summarize("", aiMsgs[plan.TailStart:]).Total()
	a.compactions.record(compactionRecord{
		SessionID:       sessionID,
		Trigger:         trigger,
		OriginalTokens:  plan.Total,
		BuiltTokens:     builtTokens,
		BudgetTokens:    plan.Budget,
		ReserveTokens:   plan.Reserve,
		KeptMessages:    kept,
		DroppedMessages: len(msgs) - kept,
		ReusedBrief:     brief != "",
	})

	slog.Info("Compacted the session locally",
		"session_id", sessionID,
		"trigger", trigger,
		"tokens_before", plan.Total,
		"tokens_after", builtTokens,
		"dropped_messages", len(msgs)-kept,
		"kept_messages", kept,
		"budget", plan.Budget,
		"reused_brief", brief != "")

	currentSession.SummaryMessageID = marker.ID
	currentSession.PromptTokens = 0
	currentSession.CompletionTokens = 0
	currentSession.EstimatedUsage = true
	if _, err := a.sessions.Save(ctx, currentSession); err != nil {
		return err
	}
	a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPostCompact, SessionID: sessionID, Trigger: trigger})

	// Release the active request before processing queued messages so that
	// Run() does not see the session as busy.
	a.activeRequests.Del(sessionID)

	queuedMessages, ok := a.messageQueue.Get(sessionID)
	if !ok || len(queuedMessages) == 0 {
		return nil
	}
	firstQueuedMessage := queuedMessages[0]
	a.messageQueue.Set(sessionID, queuedMessages[1:])
	_, qErr := a.Run(ctx, firstQueuedMessage)
	return qErr
}

func (a *sessionAgent) getCacheControlOptions() fantasy.ProviderOptions {
	if t, _ := strconv.ParseBool(os.Getenv("CRUSH_DISABLE_ANTHROPIC_CACHE")); t {
		return fantasy.ProviderOptions{}
	}
	return fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		bedrock.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		vercel.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
}

// sessionHeaders returns the HTTP headers we use for cache affinity on
// every LLM request for a given session.
//
// We use the session hash is used instead of the raw UUID so the header
// value is deterministic and opaque.
func sessionHeaders(sessionID string) map[string]string {
	hash := session.HashID(sessionID)
	return map[string]string{
		"x-session-id":       hash,
		"x-session-affinity": hash,
	}
}

func (a *sessionAgent) createUserMessage(ctx context.Context, call SessionAgentCall, modelContext string) (message.Message, error) {
	if call.Report != nil {
		return a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{*call.Report},
		})
	}
	parts := []message.ContentPart{message.TextContent{Text: call.Prompt, Hidden: call.HiddenUserMessage, Context: modelContext}}
	var attachmentParts []message.ContentPart
	for _, attachment := range call.Attachments {
		attachmentParts = append(attachmentParts, message.BinaryContent{Path: attachment.FilePath, MIMEType: attachment.MimeType, Data: attachment.Content})
	}
	parts = append(parts, attachmentParts...)
	msg, err := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: parts,
	})
	if err != nil {
		return message.Message{}, fmt.Errorf("failed to create user message: %w", err)
	}
	return msg, nil
}

func (a *sessionAgent) preparePrompt(msgs []message.Message, supportsImages, supportsAudio bool, attachments ...message.Attachment) ([]fantasy.Message, []fantasy.FilePart) {
	var history []fantasy.Message
	if !a.isSubAgent {
		history = append(history, fantasy.NewUserMessage(
			fmt.Sprintf(
				"<system_reminder>%s</system_reminder>",
				`This is a reminder that your todo list is currently empty. DO NOT mention this to the user explicitly because they are already aware.
If you are working on tasks that would benefit from a todo list please use the "todos" tool to create one.
If not, please feel free to ignore. Again do not mention this message to the user.`,
			),
		))
	}
	// Collect all tool call IDs present in assistant messages, then index
	// every tool result by its call ID. Tool results are re-emitted right
	// after the assistant message that requested them instead of at their
	// stored position: messages can be written to a session concurrently
	// (e.g. resuming while a tool is still running), which interleaves
	// user messages between a tool call and its result. LLM APIs require
	// every tool call to be followed by its results before any other
	// message, and strict-adjacency providers (e.g. Kimi, DeepSeek) reject
	// the request otherwise, permanently locking the session.
	knownToolCallIDs := make(map[string]struct{})
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			knownToolCallIDs[tc.ID] = struct{}{}
		}
	}
	toolResultsByCall := make(map[string]fantasy.ToolResultPart)
	for _, m := range msgs {
		if m.Role != message.Tool {
			continue
		}
		for _, aiMsg := range m.ToAIMessage() {
			for _, part := range aiMsg.Content {
				tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
				if !ok {
					// Tool-role ToAIMessage only emits ToolResultParts today;
					// log so unexpected parts do not vanish silently.
					slog.Warn(
						"Dropping unexpected non-tool-result part from tool message",
						"part_type", fmt.Sprintf("%T", part),
					)
					continue
				}
				if _, known := knownToolCallIDs[tr.ToolCallID]; !known {
					slog.Warn(
						"Dropping orphaned tool result with no matching tool call",
						"tool_call_id", tr.ToolCallID,
					)
					continue
				}
				// One result per call: a real result beats an error, since the
				// usual duplicate is a premature "interrupted" then the output.
				if kept, seen := toolResultsByCall[tr.ToolCallID]; seen {
					_, keptIsError := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](kept.Output)
					slog.Warn("Dropping duplicate tool result", "session_id", m.SessionID,
						"tool_call_id", tr.ToolCallID, "kept_later", keptIsError)
					if !keptIsError {
						continue
					}
				}
				toolResultsByCall[tr.ToolCallID] = tr
			}
		}
	}

	for _, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		// Assistant message without content or tool calls (cancelled before it returned anything).
		if m.Role == message.Assistant && len(m.ToolCalls()) == 0 && m.Content().Text == "" && m.ReasoningContent().String() == "" {
			continue
		}
		// Tool results are emitted right after their assistant message.
		if m.Role == message.Tool {
			continue
		}
		aiMsgs := m.ToAIMessage()
		if !supportsImages {
			for i := range aiMsgs {
				if aiMsgs[i].Role == fantasy.MessageRoleUser {
					aiMsgs[i].Content = filterFileParts(aiMsgs[i].Content)
				}
			}
		}
		history = append(history, aiMsgs...)

		if m.Role == message.Assistant && len(m.ToolCalls()) > 0 {
			history = append(history, toolResultsForCalls(m, toolResultsByCall))
		}
	}

	var files []fantasy.FilePart
	for _, attachment := range attachments {
		if attachment.IsText() {
			continue
		}
		if attachment.IsAudio() {
			if !supportsAudio {
				continue
			}
		} else if !supportsImages {
			continue
		}
		files = append(files, fantasy.FilePart{
			Filename:  attachment.FileName,
			Data:      attachment.Content,
			MediaType: attachment.MimeType,
		})
	}

	return history, files
}

// filterDisabledMCPTools removes tools from MCP servers disabled via the
// "Toggle MCPs" dialog. The override set is repository-scoped and shared
// by every session in the repository, including subagent sessions.
// Connections are process-global and left untouched; only the tool list
// changes.
func (a *sessionAgent) filterDisabledMCPTools(ctx context.Context, toolList []fantasy.AgentTool) []fantasy.AgentTool {
	disabledServers, err := a.sessions.MCPDisabledServers(ctx)
	if err != nil {
		slog.Error("Failed to list disabled MCP servers", "error", err)
		return toolList
	}
	if len(disabledServers) == 0 {
		return toolList
	}
	disabled := make(map[string]struct{}, len(disabledServers))
	for _, name := range disabledServers {
		disabled[name] = struct{}{}
	}
	filtered := make([]fantasy.AgentTool, 0, len(toolList))
	for _, t := range toolList {
		if mcpTool, ok := t.(*tools.Tool); ok {
			if _, off := disabled[mcpTool.MCP()]; off {
				continue
			}
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// filterFileParts removes fantasy.FilePart entries from a slice of message
// parts. Used to strip image attachments from historical user messages when
// the current model does not support them.
func filterFileParts(parts []fantasy.MessagePart) []fantasy.MessagePart {
	filtered := make([]fantasy.MessagePart, 0, len(parts))
	for _, part := range parts {
		if _, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func (a *sessionAgent) getSessionMessages(ctx context.Context, session session.Session) ([]message.Message, error) {
	// Read only the tail a compacted session actually sends. The full
	// transcript can be tens of megabytes on the single shared connection.
	msgs, err := a.messages.ListFromSummary(ctx, session.ID, session.SummaryMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}

	if session.SummaryMessageID != "" {
		// The marker is the first message of the kept tail; the note that
		// explains the fold is prepended after slicing (see compactionNote).
		summaryMsgIndex := -1
		for i, msg := range msgs {
			if msg.ID == session.SummaryMessageID {
				summaryMsgIndex = i
				break
			}
		}
		if summaryMsgIndex != -1 {
			msgs = msgs[summaryMsgIndex:]
		}
		// The note that opens the window is prepended here rather than stored:
		// the transcript is read in time order, so a stored note would sort
		// after the tail it has to precede. This is the shape a compacted
		// request builds, and it is assembled fresh on every read.
		if note := a.compactionNote(session.ID, session.SummaryMessageID); note != "" {
			msgs = append([]message.Message{{
				ID:               compactionNoteID,
				SessionID:        session.ID,
				Role:             message.User,
				CreatedAt:        msgs[0].CreatedAt,
				IsSummaryMessage: true,
				Parts:            []message.ContentPart{message.TextContent{Text: note}},
			}}, msgs...)
		}
	}

	// Sessions written before the resume wrapper was removed still carry it
	// as a stored user message, one copy per compaction. It is not something
	// the user said, so it is stripped on read: the transcript on disk is
	// history and is left alone, but nothing sends the text again.
	msgs = stripResumeWrappers(msgs)
	return msgs, nil
}

// stripResumeWrappers removes the old interruption announcement from a
// transcript. It returns msg with any such prompt collapsed to what the user
// actually wrote.
func stripResumeWrappers(msgs []message.Message) []message.Message {
	for i := range msgs {
		if msgs[i].Role != message.User {
			continue
		}
		if !hasResumeWrapper(msgs[i].Parts) {
			continue
		}
		msgs[i].Parts = replaceResumeWrapper(msgs[i].Parts)
	}
	return msgs
}

// hasResumeWrapper reports whether any text part carries the announcement.
// The check is a substring scan over user messages only, which is cheap next
// to the prompt assembly it sits in.
func hasResumeWrapper(parts []message.ContentPart) bool {
	for _, part := range parts {
		if tc, ok := part.(message.TextContent); ok && strings.Contains(tc.Text, resumeWrapperMarker) {
			return true
		}
	}
	return false
}

// resumeWrapperMarker is the distinctive text of the removed announcement.
const resumeWrapperMarker = "The previous session was interrupted because it got too long"

// replaceResumeWrapper rebuilds a user message's parts with the wrapper
// stripped from every text part.
func replaceResumeWrapper(parts []message.ContentPart) []message.ContentPart {
	out := make([]message.ContentPart, 0, len(parts))
	for _, part := range parts {
		tc, ok := part.(message.TextContent)
		if !ok {
			out = append(out, part)
			continue
		}
		stripped := stripOneWrapper(tc.Text)
		if stripped == "" {
			// The whole message was the wrapper, so there is nothing left to
			// send. Keep the part only when it carried context.
			if tc.Context == "" {
				continue
			}
			tc.Text = ""
			out = append(out, tc)
			continue
		}
		tc.Text = stripped
		out = append(out, tc)
	}
	return out
}

// stripOneWrapper unwraps nested interruption announcements, innermost first,
// so a prompt that was wrapped several times collapses to the original.
func stripOneWrapper(text string) string {
	for {
		i := strings.Index(text, resumeWrapperMarker)
		if i < 0 {
			return strings.TrimSpace(text)
		}
		// The wrapper is "<prefix><original>`"; keep everything after the
		// opening backtick of the innermost copy.
		rest := text[i:]
		open := strings.Index(rest, "`")
		if open < 0 {
			return strings.TrimSpace(text)
		}
		body := rest[open+1:]
		// Drop the closing backtick that the wrapper appended.
		if j := strings.LastIndex(body, "`"); j >= 0 {
			body = body[:j] + body[j+1:]
		}
		text = text[:i] + body
	}
}

// interruptedPromptPrefix opens a prompt replayed after a compaction cut a
// turn short. It is a constant so a second compaction can recognise a prompt it
// already wrapped and leave it alone.
const interruptedPromptPrefix = "The previous session was interrupted because it got too long, the initial user request was: `"

// compactionNoteID is the synthetic id of the note that opens a compacted
// window. It is never written to the database.
const compactionNoteID = "compaction-note"

// compactionNote returns the note that opens a compacted window, or "" when the
// session was never compacted. It is the summary half of the payload a
// compacted request builds: system prompt, this note, then the kept tail.
func (a *sessionAgent) compactionNote(sessionID, summaryMessageID string) string {
	if summaryMessageID == "" {
		return ""
	}
	if note, ok := a.reduction.noteFor(summaryMessageID); ok {
		return note
	}
	return ""
}

// hasUserTextMessage reports whether any user message in msgs contains
// text content (as opposed to only shell commands or other non-text parts).
func hasUserTextMessage(msgs []message.Message) bool {
	for _, msg := range msgs {
		if msg.Role != message.User {
			continue
		}
		for _, part := range msg.Parts {
			if tc, ok := part.(message.TextContent); ok && tc.Text != "" {
				return true
			}
		}
	}
	return false
}

// GenerateTitle generates a session title based on the initial prompt.
func (a *sessionAgent) GenerateTitle(ctx context.Context, sessionID string, userPrompt string) {
	a.titles.begin()
	defer a.titles.end()
	a.generateTitle(ctx, sessionID, userPrompt)
}

// generateTitle is GenerateTitle without tracking, for callers that
// register with a.titles themselves.
func (a *sessionAgent) generateTitle(ctx context.Context, sessionID string, userPrompt string) {
	if userPrompt == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, titleTimeout)
	defer cancel()

	// Ensure the session always gets a title even if every path below
	// fails or the context is cancelled before we finish.
	var titleSaved bool
	defer func() {
		if !titleSaved {
			// WithoutCancel also drops the title timeout's deadline.
			fallbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := a.sessions.Rename(fallbackCtx, sessionID, DefaultSessionName); err != nil {
				slog.Error("Failed to save fallback session title", "error", err)
			}
		}
	}()

	models := a.models()
	smallModel, largeModel := models.Small, models.Large
	systemPromptPrefix := a.systemPromptPrefix.Get()

	newAgent := func(m fantasy.LanguageModel, p []byte, tok int64) fantasy.Agent {
		return fantasy.NewAgent(
			m,
			fantasy.WithSystemPrompt(string(p)+"\n /no_think"),
			fantasy.WithMaxOutputTokens(tok),
			fantasy.WithUserAgent(userAgent),
		)
	}

	streamCall := fantasy.AgentStreamCall{
		Prompt:  fmt.Sprintf("Generate a concise title for the following content:\n\n%s\n <think>\n\n</think>", userPrompt),
		Headers: sessionHeaders(sessionID),
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = opts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{
					fantasy.NewSystemMessage(systemPromptPrefix),
				}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	}

	type modelAttempt struct {
		name  string
		model Model
	}
	attempts := []modelAttempt{
		{"small", smallModel},
		{"large", largeModel},
	}

	var resp *fantasy.AgentResult
	var err error
	var model Model
	var success bool
	for _, attempt := range attempts {
		tok := int64(40)
		if attempt.model.CatwalkCfg.CanReason() {
			tok = attempt.model.CatwalkCfg.DefaultMaxTokens
		}
		agent := newAgent(attempt.model.Model, titlePrompt, tok)
		resp, err = agent.Stream(ctx, streamCall)
		if err == nil && resp.Response.FinishReason != fantasy.FinishReasonLength {
			model = attempt.model
			slog.Debug("Generated title with " + attempt.name + " model")
			success = true
			break
		}
		if err != nil {
			slog.Error("Error generating title with "+attempt.name+" model; trying next", "err", err)
		} else {
			slog.Error("Title generation hit token limit with " + attempt.name + " model; trying next")
		}
	}
	if !success {
		// The deferred fallback will save the default session name.
		return
	}

	// Clean up title.
	var title string
	title = strings.ReplaceAll(resp.Response.Content.Text(), "\n", " ")

	// Remove thinking tags if present.
	title = thinkTagRegex.ReplaceAllString(title, "")
	title = orphanThinkTagRegex.ReplaceAllString(title, "")

	title = strings.TrimSpace(title)
	if title == "" {
		// LLM returned empty content. Use the prompt itself as a
		// fallback title, truncated to 50 chars, before resorting to
		// the generic default.
		fallback := strings.ReplaceAll(userPrompt, "\n", " ")
		fallback = strings.TrimSpace(fallback)
		if len(fallback) > 50 {
			fallback = ansi.Truncate(fallback, 50, "…")
		}
		title = cmp.Or(fallback, DefaultSessionName)
	}

	// Calculate usage and cost.
	var overrideCost *float64
	for _, step := range resp.Steps {
		stepCost := a.openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if overrideCost != nil {
				newCost += *overrideCost
			}
			overrideCost = &newCost
		}
	}
	if hyperCost := a.hyperStepsCost(model, resp.Steps); hyperCost != nil {
		overrideCost = hyperCost
	}

	cost := usageCost(model.CatwalkCfg.Pricing, resp.TotalUsage)

	// Use override cost if available (e.g., from OpenRouter or Hyper Prism).
	if overrideCost != nil {
		cost = *overrideCost
	}

	// Skip cost accumulation
	if model.FlatRate {
		cost = 0
	}

	promptTokens := contextTokens(resp.TotalUsage)
	completionTokens := resp.TotalUsage.OutputTokens

	// Atomically update only title and usage fields to avoid overriding other
	// concurrent session updates.
	saveErr := a.sessions.UpdateTitleAndUsage(ctx, sessionID, title, promptTokens, completionTokens, cost)
	if saveErr != nil {
		slog.Error("Failed to save session title and usage", "error", saveErr)
		return
	}
	titleSaved = true
}

func (a *sessionAgent) openrouterCost(metadata fantasy.ProviderMetadata) *float64 {
	openrouterMetadata, ok := metadata[openrouter.Name]
	if !ok {
		return nil
	}

	opts, ok := openrouterMetadata.(*openrouter.ProviderMetadata)
	if !ok {
		return nil
	}
	return &opts.Usage.Cost
}

// extractPrismModel returns the ID and name of the model that actually
// served the turn, as reported by the Hyper Prism model router headers,
// or empty strings when the turn was not routed through a Prism model.
func extractPrismModel(metadata fantasy.ProviderMetadata) (modelID, modelName string) {
	openaiMeta, ok := metadata[openai.Name]
	if !ok {
		return "", ""
	}
	pm, ok := openaiMeta.(*openai.ProviderMetadata)
	if !ok {
		return "", ""
	}
	_ = pm.ExtraField(hyper.PrismModelIDField, &modelID)
	_ = pm.ExtraField(hyper.PrismModelNameField, &modelName)
	return modelID, modelName
}

// modelDisplayName resolves the display name of a model from the
// current config, falling back to the model ID when no entry exists or
// the entry has no name. The result is persisted on assistant messages
// so the UI keeps showing a meaningful name after the model is renamed
// or removed from the config.
func (a *sessionAgent) modelDisplayName(model config.SelectedModel) string {
	if a.cfg != nil {
		if cfg := a.cfg.Config(); cfg != nil {
			if m := cfg.GetModel(model.Provider, model.Model); m != nil && m.Name != "" {
				return m.Name
			}
		}
	}
	return model.Model
}

func (a *sessionAgent) modelForStepCost(model Model, metadata fantasy.ProviderMetadata) Model {
	if model.ModelCfg.Provider != hyper.Name || a.cfg == nil {
		return model
	}
	routedModelID, _ := extractPrismModel(metadata)
	if routedModelID == "" {
		return model
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return model
	}
	routedModel := cfg.GetModel(hyper.Name, routedModelID)
	if routedModel == nil {
		return model
	}
	model.CatwalkCfg = *routedModel
	return model
}

func (a *sessionAgent) hyperStepsCost(model Model, steps []fantasy.StepResult) *float64 {
	if model.ModelCfg.Provider != hyper.Name || len(steps) == 0 {
		return nil
	}
	var cost float64
	for _, step := range steps {
		pricingModel := a.modelForStepCost(model, step.ProviderMetadata)
		cost += usageCost(pricingModel.CatwalkCfg.Pricing, step.Usage)
	}
	return &cost
}

// extractPrismSavings returns the hypercredit and dollar savings from
// routing the turn through the Hyper Prism model router, as reported by
// its savings trailers, or nil when not reported or malformed.
func extractPrismSavings(metadata fantasy.ProviderMetadata) (hypercredits, dollars *float64) {
	openaiMeta, ok := metadata[openai.Name]
	if !ok {
		return nil, nil
	}
	pm, ok := openaiMeta.(*openai.ProviderMetadata)
	if !ok {
		return nil, nil
	}
	return extraFieldFloat(pm, hyper.PrismHypercreditSavingsField), extraFieldFloat(pm, hyper.PrismDollarSavingsField)
}

func extraFieldFloat(pm *openai.ProviderMetadata, key string) *float64 {
	var value string
	if !pm.ExtraField(key, &value) {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		slog.Warn("Could not parse Prism savings", "key", key, "value", value, "error", err)
		return nil
	}
	return &parsed
}

func usageCost(pricing catwalk.Pricing, usage fantasy.Usage) float64 {
	return pricing.CacheCreate/1e6*float64(usage.CacheCreationTokens) +
		pricing.CacheHit/1e6*float64(usage.CacheReadTokens) +
		pricing.Input/1e6*float64(usage.InputTokens) +
		pricing.Output/1e6*float64(usage.OutputTokens)
}

func (a *sessionAgent) updateSessionUsage(model Model, session *session.Session, usage fantasy.Usage, overrideCost *float64, estimated bool) {
	if !usageIsZero(usage) {
		session.EstimatedUsage = estimated
	}

	cost := usageCost(model.CatwalkCfg.Pricing, usage)

	if !estimated {
		a.eventTokensUsed(session.ID, model, usage, cost)
	}

	if estimated {
		cost = 0
	} else {
		// Use override cost if available (e.g., from OpenRouter).
		if overrideCost != nil {
			cost = *overrideCost
		}

		// Skip cost accumulation
		if model.FlatRate {
			cost = 0
		}
	}

	session.Cost += cost
	updateSessionTokenCounters(session, usage)
}

// contextTokens returns the size of the prompt the provider processed for
// a step. Providers with prompt caching (Anthropic, Bedrock, Vercel) report
// the prompt as three disjoint buckets: tokens served from cache, tokens
// newly written to the cache, and the uncached remainder. All three occupy
// the context window, so all three count. Providers without cache writes
// leave CacheCreationTokens at zero, and the OpenAI-style providers already
// subtract cached tokens from InputTokens, so nothing is counted twice.
func contextTokens(usage fantasy.Usage) int64 {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
}

func updateSessionTokenCounters(session *session.Session, usage fantasy.Usage) {
	if usage.OutputTokens != 0 {
		session.CompletionTokens = usage.OutputTokens
	}
	if promptTokens := contextTokens(usage); promptTokens != 0 {
		session.PromptTokens = promptTokens
	}
}

func summaryCompletionTokens(usage fantasy.Usage, summaryMessage message.Message) int64 {
	if usage.OutputTokens != 0 {
		return usage.OutputTokens
	}
	return approxTokenCount(summaryMessage.Content().Text) + approxTokenCount(summaryMessage.ReasoningContent().String())
}

func (a *sessionAgent) Cancel(sessionID string) {
	// Serialize against the dispatch handoff in Run so the accepted ->
	// (cancel-on-entry | queued | active) transition is atomic against
	// this cancel. Every cancel observes at least one of: an active
	// request, an accepted run (recorded as a pending cancel), or a
	// queue entry it then clears. If none of those hold, an idle Escape
	// is a true no-op and must not poison the next prompt.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	// Cancel regular requests. Don't use Take() here - we need the entry to
	// remain in activeRequests so IsBusy() returns true until the goroutine
	// fully completes (including error handling that may access the DB).
	// The defer in processRequest will clean up the entry.
	if ac, ok := a.activeRequests.Get(sessionID); ok && ac != nil {
		slog.Debug("Request cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Also check for summarize requests.
	if ac, ok := a.activeRequests.Get(sessionID + "-summarize"); ok && ac != nil {
		slog.Debug("Summarize cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Record a pending cancel only when a dispatched-but-not-yet-active
	// run exists. This catches runs still in the goroutine scheduler or
	// about to enter Run's busy-queue branch, while leaving an idle
	// session untouched. Active and accepted are not mutually exclusive:
	// when a run is active and a follow-up has been accepted, both the
	// cancel above and this pending record fire.
	//
	// Raise the session's cancel mark to the latest accept sequence
	// assigned so far. Every prompt currently accepted-but-not-yet-
	// active has a sequence at or below that value, so one cancel covers
	// all of them; a prompt accepted after this cancel gets a strictly
	// higher sequence and is never poisoned. Using max keeps repeated
	// cancels idempotent while the same prompts are in flight and lets a
	// later cancel extend coverage to prompts accepted since.
	a.acceptedMu.Lock()
	count, ok := a.acceptedRuns.Get(sessionID)
	mark := a.acceptSeqGen
	a.acceptedMu.Unlock()
	if ok && count > 0 {
		slog.Debug("Recording cancel mark for accepted runs", "session_id", sessionID, "count", count, "mark", mark)
		existing, _ := a.cancelMark.Get(sessionID)
		a.cancelMark.Set(sessionID, max(existing, mark))
	}

	if a.QueuedPrompts(sessionID) > 0 {
		slog.Debug("Clearing queued prompts", "session_id", sessionID)
		a.clearQueueAndNotify(sessionID)
	}
}

func (a *sessionAgent) ClearQueue(sessionID string) {
	if a.QueuedPrompts(sessionID) > 0 {
		slog.Debug("Clearing queued prompts", "session_id", sessionID)
		a.clearQueueAndNotify(sessionID)
	}
}

// AddNote keeps text for the session's next prompt, which the agent reads
// with it. Nothing runs and nothing is shown in the chat.
func (a *sessionAgent) AddNote(sessionID, text string) {
	if text == "" {
		return
	}
	existing, _ := a.notes.Get(sessionID)
	a.notes.Set(sessionID, append(existing, text))
}

// takeNotes returns and forgets the session's notes, for the prompt about to run.
func (a *sessionAgent) takeNotes(sessionID string) []string {
	notes, _ := a.notes.Get(sessionID)
	a.notes.Del(sessionID)
	return notes
}

// CancelAll cancels every run and waits, up to 5s in total, for the runs
// and any in-flight title generation to finish writing.
func (a *sessionAgent) CancelAll() {
	deadline := time.Now().Add(5 * time.Second)
	if a.IsBusy() {
		for key := range a.activeRequests.Seq2() {
			a.Cancel(key) // key is sessionID
		}
		for key := range a.acceptedRuns.Seq2() {
			a.Cancel(key)
		}
		for a.IsBusy() {
			if time.Now().After(deadline) {
				slog.Warn("Gave up waiting for agent runs to cancel; their final state may not be recorded")
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !a.titles.wait(time.Until(deadline)) {
		slog.Warn("Gave up waiting for session title generation to finish")
	}
}

// IsBusy reports whether any session has an active or accepted run.
func (a *sessionAgent) IsBusy() bool {
	for ac := range a.activeRequests.Seq() {
		if ac != nil {
			return true
		}
	}
	return a.acceptedRuns.Len() > 0
}

// HasActiveTurn ignores accepts so Run, holding its own, doesn't queue behind itself.
func (a *sessionAgent) HasActiveTurn(sessionID string) bool {
	_, busy := a.activeRequests.Get(sessionID)
	return busy
}

// HasPendingWork reports whether sessionID has an active or accepted run.
func (a *sessionAgent) HasPendingWork(sessionID string) bool {
	if a.HasActiveTurn(sessionID) {
		return true
	}
	accepted, _ := a.acceptedRuns.Get(sessionID)
	return accepted > 0
}

func (a *sessionAgent) QueuedPrompts(sessionID string) int {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return 0
	}
	return len(l)
}

func (a *sessionAgent) QueuedPromptsList(sessionID string) []string {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return nil
	}
	prompts := make([]string, len(l))
	for i, call := range l {
		prompts[i] = call.Prompt
	}
	return prompts
}

func (a *sessionAgent) SetTools(tools []fantasy.AgentTool) {
	a.tools.SetSlice(tools)
}

func (a *sessionAgent) SetSystemPrompt(systemPrompt string) {
	a.systemPrompt.Set(systemPrompt)
}

func (a *sessionAgent) Model() Model {
	return a.models().Large
}

// convertToToolResult converts a fantasy tool result to a message tool result.
func (a *sessionAgent) convertToToolResult(result fantasy.ToolResultContent) message.ToolResult {
	baseResult := message.ToolResult{
		ToolCallID: result.ToolCallID,
		Name:       result.ToolName,
		Metadata:   result.ClientMetadata,
	}

	switch result.Result.GetType() {
	case fantasy.ToolResultContentTypeText:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Result); ok {
			baseResult.Content = r.Text
		}
	case fantasy.ToolResultContentTypeError:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result.Result); ok {
			baseResult.Content = r.Error.Error()
			baseResult.IsError = true
		}
	case fantasy.ToolResultContentTypeMedia:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Result); ok {
			switch {
			case r.Data == "":
				slog.Warn("Tool returned media with no data, storing a note instead",
					"tool", result.ToolName, "tool_call_id", result.ToolCallID)
				baseResult.Content = emptyMediaText(r.Text)
			case !stringext.IsValidBase64(r.Data):
				slog.Warn(
					"Tool returned media with invalid base64 data, discarding image",
					"tool", result.ToolName,
					"tool_call_id", result.ToolCallID,
				)
				baseResult.Content = "Tool returned image data with invalid encoding"
				baseResult.IsError = true
			default:
				content := r.Text
				if content == "" {
					content = fmt.Sprintf("Loaded %s content", r.MediaType)
				}
				baseResult.Content = content
				baseResult.Data = r.Data
				baseResult.MIMEType = r.MediaType
			}
		}
	}

	return baseResult
}

// workaroundProviderMediaLimitations converts media content in tool results to
// user messages for providers that don't natively support images in tool results.
//
// Problem: OpenAI, Google, OpenRouter, and other OpenAI-compatible providers
// don't support sending images/media in tool result messages - they only accept
// text in tool results. However, they DO support images in user messages.
//
// If we send media in tool results to these providers, the API returns an error.
//
// Solution: For these providers, we:
//  1. Replace the media in the tool result with a text placeholder
//  2. Inject a user message immediately after with the image as a file attachment
//  3. This maintains the tool execution flow while working around API limitations
//
// Anthropic and Bedrock support images natively in tool results, so we skip
// this workaround for them.
//
// Example transformation:
//
//	BEFORE: [tool result: image data]
//	AFTER:  [tool result: "Image loaded - see attached"], [user: image attachment]
func (a *sessionAgent) workaroundProviderMediaLimitations(messages []fantasy.Message, largeModel Model) []fantasy.Message {
	providerSupportsMedia := largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderAnthropic) ||
		largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderBedrock) ||
		largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderBedrockEurope)

	if providerSupportsMedia {
		return messages
	}

	supportsImages := largeModel.CatwalkCfg.Capabilities.Vision

	convertedMessages := make([]fantasy.Message, 0, len(messages))

	for _, msg := range messages {
		if msg.Role != fantasy.MessageRoleTool {
			convertedMessages = append(convertedMessages, msg)
			continue
		}

		textParts := make([]fantasy.MessagePart, 0, len(msg.Content))
		var mediaFiles []fantasy.FilePart

		for _, part := range msg.Content {
			toolResult, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				textParts = append(textParts, part)
				continue
			}

			if media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](toolResult.Output); ok {
				if !supportsImages {
					// Model cannot process images. Replace with a text
					// placeholder and skip creating a synthetic user
					// message with FilePart, which would brick the
					// session on text-only models.
					textParts = append(textParts, fantasy.ToolResultPart{
						ToolCallID: toolResult.ToolCallID,
						Output: fantasy.ToolResultOutputContentText{
							Text: "[Image/media content not supported by this model]",
						},
						ProviderOptions: toolResult.ProviderOptions,
					})
					continue
				}

				decoded, err := base64.StdEncoding.DecodeString(media.Data)
				if err != nil {
					slog.Warn("Failed to decode media data", "error", err)
					textParts = append(textParts, part)
					continue
				}

				mediaFiles = append(mediaFiles, fantasy.FilePart{
					Data:      decoded,
					MediaType: media.MediaType,
					Filename:  fmt.Sprintf("tool-result-%s", toolResult.ToolCallID),
				})

				textParts = append(textParts, fantasy.ToolResultPart{
					ToolCallID: toolResult.ToolCallID,
					Output: fantasy.ToolResultOutputContentText{
						Text: "[Image/media content loaded - see attached file]",
					},
					ProviderOptions: toolResult.ProviderOptions,
				})
			} else {
				textParts = append(textParts, part)
			}
		}

		convertedMessages = append(convertedMessages, fantasy.Message{
			Role:    fantasy.MessageRoleTool,
			Content: textParts,
		})

		if len(mediaFiles) > 0 {
			convertedMessages = append(convertedMessages, fantasy.NewUserMessage(
				"Here is the media content from the tool result:",
				mediaFiles...,
			))
		}
	}

	return convertedMessages
}

// gitStatusContext returns the repository state to carry on the first user
// turn. It used to live in the system prompt, where anything that changes the
// working tree rewrote the cached prefix and made every round a cache miss.
// Only the first turn carries it: later turns of the same session have already
// been told, and repeating it would grow the transcript for nothing.
func (a *sessionAgent) gitStatusContext(ctx context.Context) string {
	if a.workingDir == "" {
		return ""
	}
	block := prompt.GitStatusBlock(ctx, a.workingDir)
	if block == "" {
		return ""
	}
	return "\n\n<git_status>\n" + block + "\n</git_status>"
}

// buildSummaryPrompt constructs the prompt the background digest uses to write
// a continuation brief. It is not on the compaction path: compaction is local
// and never asks a model to write anything.
func buildSummaryPrompt(todos []session.Todo) string {
	return buildContinuationPrompt(todos, "")
}

// buildContinuationPrompt asks for the brief, and folds in what the previous
// brief established.
//
// The previous brief is carried forward rather than regenerated: the earliest
// turns established constraints that a summary written from the tail alone would
// lose, and re-deriving them is exactly what a long session cannot afford.
func buildContinuationPrompt(todos []session.Todo, previousBrief string) string {
	var sb strings.Builder
	sb.WriteString("Write the continuation brief for the conversation above.")
	if previousBrief != "" {
		sb.WriteString("\n\nA brief from earlier in this session already exists. Keep every fact and constraint in it that is still true, and add what this part of the conversation established. Do not repeat what it already says in different words.\n\n")
		sb.WriteString("<previous_brief>\n")
		sb.WriteString(previousBrief)
		sb.WriteString("\n</previous_brief>")
	}
	if len(todos) > 0 {
		sb.WriteString("\n\n<current_plan>\n")
		for _, t := range todos {
			fmt.Fprintf(&sb, "- [%s] %s\n", t.Status, t.Content)
		}
		sb.WriteString("</current_plan>\n\nCarry these steps into the brief with their statuses, so the next assistant continues the plan instead of restating it.")
	}
	return sb.String()
}

func providerRetryLogFields(err *fantasy.ProviderError, delay time.Duration) []any {
	fields := []any{
		"retry_delay", delay.String(),
	}
	if err == nil {
		return fields
	}
	fields = append(fields, "status_code", err.StatusCode)
	if err.Title != "" {
		fields = append(fields, "title", err.Title)
	}
	if err.Message != "" {
		fields = append(fields, "message", err.Message)
	}
	return fields
}

// sanitizeToolInput validates tool call JSON from the provider.
// Malformed input is replaced with an empty object to prevent
// stuck conversations from truncated or malformed model output.
// The second return value indicates whether sanitization occurred.
func sanitizeToolInput(toolName, toolCallID, input string) (string, bool) {
	if !json.Valid([]byte(input)) {
		slog.Warn(
			"Malformed tool call JSON from provider, replacing with empty object",
			"tool", toolName,
			"id", toolCallID,
			"input_len", len(input),
		)
		return "{}", true
	}
	return input, false
}

// signInAgainMessage tells the user how to replace a credential the provider
// refused after a refresh.
func signInAgainMessage(m Model) string {
	name := cmp.Or(m.ProviderName, m.ModelCfg.Provider)
	return fmt.Sprintf("Please sign in to %s again. You can also run \"crush login %s --force\".",
		name, m.ModelCfg.Provider)
}
