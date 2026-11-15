// Package editsafe is a transactional edit engine: it validates every change
// against one snapshot of the file, refuses anything ambiguous, preserves the
// file's own conventions (BOM, line endings, final newline), and verifies the
// committed bytes by reading them back.
//
// The point of the engine is not to make edits cleverer but to make failures
// impossible to misread. Every refusal carries a code that names what went
// wrong, and every success carries the hash of what actually landed on disk, so
// a caller never has to guess whether a write happened.
package editsafe

import (
	"fmt"
	"strings"
)

// Code identifies why an edit was refused or how it ended. The set mirrors the
// failure taxonomy: each code names a distinct way an edit can go wrong, which
// is what makes a refusal actionable instead of a generic error.
type Code string

const (
	// CodeSuccess means the transaction applied and the read-back matched.
	CodeSuccess Code = "EDIT_SUCCESS"
	// CodeRejected is the generic refusal.
	CodeRejected Code = "EDIT_REJECTED"
	// CodeNotApplied means nothing was written.
	CodeNotApplied Code = "EDIT_NOT_APPLIED"
	// CodeNotCommitted means the write was attempted but did not land.
	CodeNotCommitted Code = "EDIT_NOT_COMMITTED"
	// CodePartiallyApplied means some ops applied before a later one failed.
	// The engine never produces it: a failing op aborts the whole batch, and
	// the code exists so a caller can tell that guarantee from a partial
	// write by an external tool.
	CodePartiallyApplied Code = "EDIT_PARTIALLY_APPLIED"
	// CodeRiskDetected means the change is larger than the configured bound.
	CodeRiskDetected Code = "EDIT_RISK_DETECTED"
	// CodeConventionRestored means the engine had to restore a convention the
	// edit would have broken (usually the final newline).
	CodeConventionRestored Code = "EDIT_CONVENTION_RESTORED"

	// CodeAnchorGone means the addressed text is no longer at the address.
	CodeAnchorGone Code = "anchor_gone"
	// CodeMultiMatch means a find/replace matched more than once.
	CodeMultiMatch Code = "multi_match"
	// CodeMissingAnchor means an addressed edit had no expect token.
	CodeMissingAnchor Code = "missing_anchor"
	// CodeNoTargetText means a replacement had no text to find.
	CodeNoTargetText Code = "no_target_text"
	// CodeRangesOverlap means two ops touched the same region.
	CodeRangesOverlap Code = "ranges_overlap"
	// CodeNoOpResolved means the edit would have changed nothing.
	CodeNoOpResolved Code = "no_op_resolved"
	// CodeDiffExplosion means the change is disproportionate to the file.
	CodeDiffExplosion Code = "diff_explosion"
	// CodeBinaryFile means the file is not text.
	CodeBinaryFile Code = "binary_file"
	// CodeEncodingDamage means the bytes are not valid UTF-8 and would not
	// survive a round trip.
	CodeEncodingDamage Code = "encoding_damage"
	// CodeTempMismatch means the staged copy differed from the intended
	// result, so nothing was committed.
	CodeTempMismatch Code = "temp_mismatch"
	// CodeOpIDConflict means two ops claimed the same identifier.
	CodeOpIDConflict Code = "op_id_conflict"
	// CodeTargetRegionChanged means the region changed between the read and
	// the write.
	CodeTargetRegionChanged Code = "target_region_changed"
	// CodeProtectedFile means the path is on the engine's protected list.
	CodeProtectedFile Code = "protected_file"
	// CodeFileCollapsed means the result lost most of the file's lines, which
	// is almost always a truncated replacement rather than an intent.
	CodeFileCollapsed Code = "file_collapsed"
	// CodeUnrelatedDeclaration means a structural check found that the edit
	// touched a declaration it was not asked to touch.
	CodeUnrelatedDeclaration Code = "unrelated_declaration"
)

// Error is a refusal. It carries the code, a message the model or operator can
// act on, and any hints derived from the actual file.
type Error struct {
	Code Code
	// Message explains the refusal in one or two sentences.
	Message string
	// Hints are concrete next steps, such as the line the anchor moved to.
	Hints []string
	// Path is the file the refusal is about.
	Path string
}

func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString(string(e.Code))
	if e.Path != "" {
		sb.WriteString(": ")
		sb.WriteString(e.Path)
	}
	if e.Message != "" {
		sb.WriteString(" — ")
		sb.WriteString(e.Message)
	}
	for _, hint := range e.Hints {
		sb.WriteString("\n")
		sb.WriteString(hint)
	}
	return sb.String()
}

// newError builds a refusal.
func newError(code Code, path, message string, hints ...string) *Error {
	return &Error{Code: code, Path: path, Message: message, Hints: hints}
}

// IsCode reports whether err is a refusal with the given code.
func IsCode(err error, code Code) bool {
	var editErr *Error
	if !asError(err, &editErr) {
		return false
	}
	return editErr.Code == code
}

// asError is errors.As, specialised for *Error so the helper stays allocation
// free on the common path.
func asError(err error, target **Error) bool {
	for err != nil {
		if editErr, ok := err.(*Error); ok {
			*target = editErr
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// Result reports a successful transaction.
type Result struct {
	// Path is the file that changed.
	Path string
	// OldContent and NewContent are the bytes before and after, as text.
	OldContent string
	NewContent string
	// Hash is the hex SHA-256 of NewContent, and ReadBackHash is the hash of
	// what the engine read from disk after committing. They must match; a
	// mismatch is reported as an error, not as a result.
	Hash         string
	ReadBackHash string
	// Codes are the status codes this transaction raised, always including
	// CodeSuccess.
	Codes []Code
	// Additions and Removals count changed lines, for reporting.
	Additions int
	Removals  int
	// Applied is how many ops were applied.
	Applied int
}

// Has reports whether the result raised a code.
func (r Result) Has(code Code) bool {
	for _, c := range r.Codes {
		if c == code {
			return true
		}
	}
	return false
}

// String renders a one-line summary, for logs.
func (r Result) String() string {
	return fmt.Sprintf("%s: %d op(s), +%d/-%d, sha256:%s",
		r.Path, r.Applied, r.Additions, r.Removals, shortHash(r.Hash))
}

// shortHash truncates a hash for display.
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
