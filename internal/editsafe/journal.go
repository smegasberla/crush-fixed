package editsafe

import (
	"os"
	"sort"
	"sync"
	"time"
)

// JournalEntry records one file's change in a turn, with enough of the prior
// state to put it back.
type JournalEntry struct {
	// Path is the absolute file path.
	Path string
	// Before and After are the file's text before and after the edit.
	Before string
	// After is the committed text. It is kept so a revert can refuse when the
	// file changed again after this edit: reverting over a later change would
	// silently discard it.
	After string
	// BeforeHash and AfterHash are the SHA-256 of the bytes.
	BeforeHash string
	AfterHash  string
	// Created is true when the file did not exist before the edit, so a
	// revert removes it.
	Created bool
	// Removed is true when the edit deleted the file, so a revert recreates
	// it.
	Removed bool
	// At is when the change landed.
	At time.Time
	// Mode is the file's permission bits before the edit.
	Mode os.FileMode
}

// Journal is one turn's changes, newest last. It is what makes a revert
// possible and honest: the engine knows exactly what it wrote, so a revert can
// verify the file is still as it left it before undoing anything.
type Journal struct {
	mu      sync.Mutex
	entries []JournalEntry
}

// NewJournal returns an empty journal.
func NewJournal() *Journal { return &Journal{} }

// Record appends an entry.
func (j *Journal) Record(entry JournalEntry) {
	if j == nil || entry.Path == "" {
		return
	}
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, entry)
}

// Entries returns a copy of the journal, oldest first.
func (j *Journal) Entries() []JournalEntry {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JournalEntry, len(j.entries))
	copy(out, j.entries)
	return out
}

// Len returns how many changes the journal holds.
func (j *Journal) Len() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

// Paths returns the distinct files the journal touched, sorted.
func (j *Journal) Paths() []string {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	seen := make(map[string]struct{}, len(j.entries))
	paths := make([]string, 0, len(j.entries))
	for _, entry := range j.entries {
		if _, ok := seen[entry.Path]; ok {
			continue
		}
		seen[entry.Path] = struct{}{}
		paths = append(paths, entry.Path)
	}
	sort.Strings(paths)
	return paths
}

// RevertResult reports what a revert did.
type RevertResult struct {
	// Restored lists the files put back to their prior content.
	Restored []string
	// Removed lists the files deleted because the turn created them.
	Removed []string
	// Recreated lists the files recreated because the turn deleted them.
	Recreated []string
	// Skipped lists the files left alone, with the reason.
	Skipped map[string]string
}

// Revert undoes the journal, newest change first, so the file returns to the
// state it had before the turn.
//
// A file that changed after the edit is skipped rather than overwritten:
// discarding a change the engine did not make would be a bigger loss than an
// incomplete revert, and the reason is reported so the caller can decide.
func (j *Journal) Revert() (RevertResult, error) {
	result := RevertResult{Skipped: make(map[string]string)}
	if j == nil {
		return result, nil
	}
	entries := j.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		current, readErr := os.ReadFile(entry.Path)
		exists := readErr == nil

		if entry.Created {
			if !exists {
				result.Skipped[entry.Path] = "already absent"
				continue
			}
			if Hash(current) != entry.AfterHash {
				result.Skipped[entry.Path] = "changed after this turn; not removing"
				continue
			}
			if err := os.Remove(entry.Path); err != nil {
				result.Skipped[entry.Path] = "could not remove: " + err.Error()
				continue
			}
			result.Removed = append(result.Removed, entry.Path)
			continue
		}

		if exists && Hash(current) != entry.AfterHash {
			result.Skipped[entry.Path] = "changed after this turn; not overwriting"
			continue
		}
		mode := entry.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := writeAtomic(entry.Path, []byte(entry.Before)); err != nil {
			result.Skipped[entry.Path] = "could not restore: " + err.Error()
			continue
		}
		_ = os.Chmod(entry.Path, mode)
		if entry.Removed {
			result.Recreated = append(result.Recreated, entry.Path)
			continue
		}
		result.Restored = append(result.Restored, entry.Path)
	}
	return result, nil
}
