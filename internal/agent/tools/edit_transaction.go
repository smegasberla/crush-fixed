package tools

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/diff"
	"github.com/charmbracelet/crush/internal/editsafe"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/permission"
)

// transactionalEdit applies an addressed or multi-region edit through the
// transactional engine.
//
// The engine is used for these shapes specifically because they are the ones
// where a plain find/replace cannot protect the caller: a line number can be
// stale, and a batch can half-apply. The simple find/replace path is unchanged
// so existing behaviour and its tests still hold.
func transactionalEdit(edit editContext, params EditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for editing a file")
	}
	if params.FilePath == "" {
		return fantasy.NewTextErrorResponse("file_path is required"), nil
	}

	raw, err := os.ReadFile(params.FilePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to read file: %s", err)), nil
	}
	tx, err := editsafe.LoadBytes(params.FilePath, raw)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	for _, op := range requestedOps(params) {
		tx.Add(op)
	}

	// Validate the change before asking for permission: there is no point
	// prompting for an edit the engine will refuse, and the prompt should show
	// the diff that will actually land.
	limits := editLimits(edit.ctx)
	preview, previewErr := tx.Preview()
	if previewErr != nil {
		return fantasy.NewTextErrorResponse(previewErr.Error()), nil
	}
	if diffErr := editsafe.CheckDiff(params.FilePath, tx.Text(), preview.NewContent, limits); diffErr != nil {
		return fantasy.NewTextErrorResponse(diffErr.Error()), nil
	}
	newText := preview.NewContent
	if owner := ownershipFor(edit.ctx); owner != nil {
		if claimErr := owner.Claim(params.FilePath); claimErr != nil {
			return fantasy.NewTextErrorResponse(claimErr.Error()), nil
		}
	}

	_, additions, removals := diff.GenerateDiff(tx.Text(), newText, strings.TrimPrefix(params.FilePath, edit.workingDir))
	approved, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(params.FilePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Edit %s", params.FilePath),
			Params: EditPermissionsParams{
				FilePath:   params.FilePath,
				OldContent: tx.Text(),
				NewContent: newText,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !approved {
		resp := NewPermissionDeniedResponse()
		return fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: tx.Text(),
			NewContent: newText,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}

	before := tx.Text()
	result, err := tx.Commit()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	// Record the change so the turn can be reverted, and so the edit is
	// visible to the next prompt as something this turn did.
	recordEdit(edit.ctx, sessionID, editsafe.JournalEntry{
		Path:       params.FilePath,
		Before:     before,
		After:      result.NewContent,
		BeforeHash: editsafe.Hash([]byte(before)),
		AfterHash:  result.Hash,
		Mode:       fileModeOf(params.FilePath),
	})

	// History is a convenience: it is optional, and a failure to record a
	// version must not turn a landed edit into a reported error.
	if edit.files != nil {
		if _, err := edit.files.CreateVersion(edit.ctx, sessionID, params.FilePath, result.NewContent); err != nil {
			slog.Warn("Could not record an edit version", "error", err, "file", params.FilePath)
		}
	}

	message := fmt.Sprintf("Edited %s: %d op(s) applied, %d line(s) added, %d removed.\n%s",
		params.FilePath, result.Applied, result.Additions, result.Removals, tx.Conventions().Describe())
	if result.Has(editsafe.CodeConventionRestored) {
		message += "\nThe file's final newline was restored, because the change would have dropped it."
	}
	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(message),
		EditResponseMetadata{
			OldContent: result.OldContent,
			NewContent: result.NewContent,
			Additions:  result.Additions,
			Removals:   result.Removals,
		},
	), nil
}

// requestedOps converts the request's parameters into engine ops.
func requestedOps(params EditParams) []editsafe.Op {
	ops := make([]editsafe.Op, 0, len(params.Ops)+1)
	for _, op := range params.Ops {
		ops = append(ops, editsafe.Op{
			ID:         op.ID,
			OldText:    op.OldString,
			NewText:    op.NewString,
			Line:       op.Line,
			EndLine:    op.EndLine,
			Expect:     op.Expect,
			Delete:     op.Delete,
			ReplaceAll: op.ReplaceAll,
		})
	}
	if params.Line > 0 || (len(params.Ops) == 0 && params.OldString != "") {
		if len(params.Ops) == 0 {
			ops = append(ops, editsafe.Op{
				OldText:    params.OldString,
				NewText:    params.NewString,
				Line:       params.Line,
				EndLine:    params.EndLine,
				Expect:     params.Expect,
				Delete:     params.Delete,
				ReplaceAll: params.ReplaceAll,
			})
		}
	}
	return ops
}

// fileModeOf returns the file's permission bits, or 0 when it cannot be read.
func fileModeOf(path string) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Mode().Perm()
}

// Edit turn state is carried on the tool context so the tools can share one
// ownership tracker and one journal without the agent reaching into the tool
// package's internals.
type editTurnKey struct{}

// EditTurnState is what one turn's edits share: which files it owns and what it
// changed. It is the pair that makes the risk cap and the revert possible.
type EditTurnState struct {
	Ownership *editsafe.Ownership
	// Journal is nil when journaling is disabled.
	Journal *editsafe.Journal
	Limits  editsafe.Limits
	// InterceptShell is false when the user turned interception off.
	InterceptShell bool
}

// WithEditTurnState attaches the turn's edit state to a context.
func WithEditTurnState(ctx context.Context, state *EditTurnState) context.Context {
	return context.WithValue(ctx, editTurnKey{}, state)
}

// editTurnState returns the turn's edit state, or nil.
func editTurnState(ctx context.Context) *EditTurnState {
	state, _ := ctx.Value(editTurnKey{}).(*EditTurnState)
	return state
}

// ownershipFor returns the turn's ownership tracker, or nil.
func ownershipFor(ctx context.Context) *editsafe.Ownership {
	if state := editTurnState(ctx); state != nil {
		return state.Ownership
	}
	return nil
}

// editLimits returns the turn's configured bounds, or the defaults.
func editLimits(ctx context.Context) editsafe.Limits {
	if state := editTurnState(ctx); state != nil && state.Limits != (editsafe.Limits{}) {
		return state.Limits
	}
	return editsafe.DefaultLimits()
}

// recordEdit appends to the turn's journal when one is installed, and records
// the read so the next prompt knows the file was touched.
func recordEdit(ctx context.Context, sessionID string, entry editsafe.JournalEntry) {
	if state := editTurnState(ctx); state != nil && state.Journal != nil {
		state.Journal.Record(entry)
	}
	_ = sessionID
}

// interceptShellEdit routes a single-file in-place shell edit to the edit
// engine.
//
// It returns the message to show the model when it handled the command, and
// false when the command should run normally. The interception only applies when
// a turn state is installed, because that is what supplies the journal and the
// ownership tracker the retry needs.
func interceptShellEdit(ctx context.Context, command, workingDir string) (string, bool) {
	state := editTurnState(ctx)
	if state == nil || !state.InterceptShell {
		return "", false
	}
	write, ok := editsafe.DetectShellWrite(command)
	if !ok {
		return "", false
	}
	path := write.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	if _, err := os.Stat(path); err != nil {
		// The file does not exist: a redirect that creates one is a write,
		// not an edit, and the shell is the right tool for it.
		return "", false
	}
	if err := state.Ownership.Claim(path); err != nil {
		return err.Error(), true
	}
	return editsafe.ShellRoutingNote(path) + "\n\nNothing was run. Re-send the change with the edit tool.", true
}

// EditTurnConfig configures one turn's edit state.
type EditTurnConfig struct {
	// Limits bounds what one edit may do.
	Limits editsafe.Limits
	// ExpectedPaths are the files the request named.
	ExpectedPaths []string
	// InterceptShell routes single-file in-place shell edits to the engine.
	InterceptShell bool
	// Journal records each edit so the turn can be reverted.
	Journal bool
}

// NewEditTurnState builds the turn's edit state.
func NewEditTurnState(cfg EditTurnConfig) *EditTurnState {
	state := &EditTurnState{
		Ownership:      editsafe.NewOwnership(cfg.ExpectedPaths, cfg.Limits),
		Limits:         cfg.Limits,
		InterceptShell: cfg.InterceptShell,
	}
	if cfg.Journal {
		state.Journal = editsafe.NewJournal()
	}
	return state
}

// RevertReport is what a turn revert did, for the agent and its callers.
type RevertReport struct {
	// Restored lists files put back to their prior content.
	Restored []string
	// Removed lists files deleted because the turn created them.
	Removed []string
	// Recreated lists files recreated because the turn deleted them.
	Recreated []string
	// Skipped maps files left alone to the reason.
	Skipped map[string]string
}

// Empty reports whether the revert did nothing.
func (r RevertReport) Empty() bool {
	return len(r.Restored) == 0 && len(r.Removed) == 0 && len(r.Recreated) == 0
}
