package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/goaltree"
	"github.com/charmbracelet/crush/internal/memory"
	"github.com/charmbracelet/crush/internal/promptlayer"
	"github.com/charmbracelet/crush/internal/replyhygiene"
	"github.com/charmbracelet/crush/internal/validate"
)

// LeverageOptions carries the long-session settings. Nil disables the layer.
type LeverageOptions struct {
	Settings *config.LeverageOptions
	// DataDir is where the memory and journal stores live.
	DataDir string
	// ProjectRoot scopes memories to one project.
	ProjectRoot string
}

// leverageState is the per-agent runtime for the long-session layer: one memory
// store, one verification memory, and the prompt-prefix digest.
type leverageState struct {
	settings    *config.LeverageOptions
	dataDir     string
	projectRoot string
	memories    *memory.Store
	// lastPrefixDigest is the previous round's cacheable prefix hash, so a
	// prefix that changes every round is visible in the log.
	lastPrefixDigest string
	// lastKnownGood tracks the last clean validation for a bisect hint.
	lastKnownGood *validate.LastKnownGood
}

// newLeverageState builds the runtime, or nil when the layer is disabled. A
// failure to open the memory store disables memory only: the rest of the layer
// still works, because a missing database must not stop a turn.
func newLeverageState(opts *LeverageOptions) *leverageState {
	if opts == nil || opts.Settings == nil {
		return nil
	}
	if !opts.Settings.LeverageEnabled() {
		return nil
	}
	state := &leverageState{
		settings:      opts.Settings,
		dataDir:       opts.DataDir,
		projectRoot:   opts.ProjectRoot,
		lastKnownGood: validate.NewLastKnownGood(),
	}
	if opts.Settings.GetProjectMemory() {
		store, err := memory.Open(memory.DefaultPath(opts.DataDir))
		if err != nil {
			slog.Warn("Project memory is unavailable", "error", err)
		} else {
			state.memories = store
		}
	}
	return state
}

// enabled reports whether the layer should run.
func (l *leverageState) enabled() bool { return l != nil && l.settings != nil }

// Close releases the memory store.
func (l *leverageState) Close() {
	if l == nil || l.memories == nil {
		return
	}
	if err := l.memories.Close(); err != nil {
		slog.Warn("Closing the project memory store failed", "error", err)
	}
}

// memoryBlock returns the memory prompt block for a query, honouring the limit.
func (l *leverageState) memoryBlock(ctx context.Context, query string) string {
	if !l.enabled() || l.memories == nil || !l.settings.GetProjectMemory() {
		return ""
	}
	entries, err := l.memories.Search(ctx, l.projectRoot, query, l.settings.GetMemoryLimit())
	if err != nil {
		slog.Warn("Project memory lookup failed", "error", err)
		return ""
	}
	return memory.PromptBlock(entries)
}

// rememberLesson stores a lesson for this project.
func (l *leverageState) rememberLesson(ctx context.Context, topic, trigger, lesson string) error {
	if !l.enabled() || l.memories == nil {
		return nil
	}
	_, err := l.memories.Save(ctx, memory.Entry{
		ProjectRoot: l.projectRoot,
		Topic:       topic,
		Trigger:     trigger,
		Lesson:      lesson,
	})
	return err
}

// lessons returns every remembered lesson for this project.
func (l *leverageState) lessons(ctx context.Context) []memory.Entry {
	if !l.enabled() || l.memories == nil {
		return nil
	}
	entries, err := l.memories.List(ctx, l.projectRoot)
	if err != nil {
		slog.Warn("Listing project memories failed", "error", err)
		return nil
	}
	return entries
}

// forgetLesson removes a lesson by ID.
func (l *leverageState) forgetLesson(ctx context.Context, id string) error {
	if !l.enabled() || l.memories == nil {
		return nil
	}
	return l.memories.Delete(ctx, id)
}

// layeredPrompt assembles the prompt with a stable prefix and logs a prefix that
// changed, which is the only visible symptom of a cache that never hits.
func (l *leverageState) layeredPrompt(sessionID, core, projectContext, contract string, notes ...string) string {
	if !l.enabled() || !l.settings.GetPromptLayering() {
		return promptlayer.Layered(core, projectContext, contract, notes...)
	}
	prompt := promptlayer.New()
	prompt.Add(promptlayer.LayerCore, core)
	prompt.Add(promptlayer.LayerContext, projectContext)
	prompt.Add(promptlayer.LayerContract, contract)
	prompt.AddAll(promptlayer.LayerNotes, notes...)

	digest := prompt.CoreDigest()
	if prompt.StablePrefixChanged(l.lastPrefixDigest) {
		slog.Debug("Prompt prefix changed between rounds", "session_id", sessionID, "digest", digest)
	}
	l.lastPrefixDigest = digest
	return prompt.Render()
}

// cleanReply runs the reply through the hygiene stack and reports leaks that
// survived it.
func (l *leverageState) cleanReply(reply string) string {
	if !l.enabled() || !l.settings.GetReplyHygiene() {
		return reply
	}
	result := replyhygiene.Clean(reply)
	if leaks := replyhygiene.AuditLeaks(result.Text); len(leaks) > 0 {
		slog.Warn("Reply still contains template tokens after cleaning", "tokens", leaks)
	}
	if result.Changed() {
		slog.Debug("Reply hygiene cleaned the answer", "stages", result.Applied, "removed_rounds", result.RemovedRunes)
	}
	return result.Text
}

// goalContract builds the task-contract layer for a request, using the project's
// detected verification command as the required validation when there is one.
func goalContract(goal string, files []string) string {
	actions := []string{"identify the cause or the exact locus of the change"}
	if len(files) > 0 {
		actions = append(actions, "modify the relevant code")
		actions = append(actions, "validate the change (build/tests/diagnostics)")
	}
	return promptlayer.Contract(goal, actions, files)
}

// resumeCandidate builds a resume candidate from a session's open todos. It
// returns false when the session has nothing open.
func resumeCandidate(sessionID, title string, updatedAt int64, todos []sessionTodoLite) (goaltree.ResumeCandidate, bool) {
	var open []string
	for _, todo := range todos {
		switch todo.Status {
		case "completed", "cancelled":
			continue
		}
		open = append(open, todo.Content)
	}
	if len(open) == 0 {
		return goaltree.ResumeCandidate{}, false
	}
	return goaltree.ResumeCandidate{
		SessionID: sessionID,
		Title:     title,
		OpenTasks: open,
		UpdatedAt: updatedAt,
	}, true
}

// sessionTodoLite is the subset of a todo a resume needs.
type sessionTodoLite struct {
	Content string
	Status  string
}

// leverageConfigFor builds the leverage options from the loaded config.
func (c *coordinator) leverageConfigFor() *LeverageOptions {
	cfg := c.cfg.Config()
	settings := cfg.Options.GetLeverage()
	if !settings.LeverageEnabled() {
		return nil
	}
	root := c.cfg.WorkingDir()
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &LeverageOptions{
		Settings:    settings,
		DataDir:     cfg.Options.DataDirectory,
		ProjectRoot: root,
	}
}

// resumeMaxAge returns the resume window as a duration.
func (l *leverageState) resumeMaxAge() time.Duration {
	if !l.enabled() {
		return 0
	}
	return time.Duration(l.settings.GetResumeMaxAgeHours()) * time.Hour
}

// resumeProbeEnabled reports whether unfinished work is offered at startup.
func (l *leverageState) resumeProbeEnabled() bool {
	return l.enabled() && l.settings.GetResumeProbe()
}

// goalTreeEnabled reports whether completed steps need evidence.
func (l *leverageState) goalTreeEnabled() bool {
	return l.enabled() && l.settings.GetGoalTree()
}

// verificationMemoryEnabled reports whether failures are remembered.
func (l *leverageState) verificationMemoryEnabled() bool {
	return l.enabled() && l.settings.GetVerificationMemory()
}

// noteEditForBisect records an edit for the last-known-good ledger.
func (l *leverageState) noteEditForBisect(path string) {
	if !l.enabled() {
		return
	}
	l.lastKnownGood.NoteEdit(path)
}

// bisectHint returns where to look first when a check fails.
func (l *leverageState) bisectHint() string {
	if !l.enabled() {
		return ""
	}
	return l.lastKnownGood.BisectHint()
}
