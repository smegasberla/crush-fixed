package editsafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// persistedJournal is the on-disk form of a journal. It exists so a revert
// survives the process: the edits happened, and the ability to undo them should
// not depend on the agent that made them still being alive.
type persistedJournal struct {
	// SessionID scopes the journal to one session.
	SessionID string `json:"sessionId"`
	// Entries is the recorded changes, oldest first.
	Entries []persistedEntry `json:"entries"`
	// UpdatedAt is when the journal was last written.
	UpdatedAt string `json:"updatedAt"`
}

// persistedEntry is on-disk form of one change.
type persistedEntry struct {
	Path       string `json:"path"`
	Before     string `json:"before"`
	After      string `json:"after"`
	BeforeHash string `json:"beforeHash"`
	AfterHash  string `json:"afterHash"`
	Created    bool   `json:"created,omitempty"`
	Removed    bool   `json:"removed,omitempty"`
	At         string `json:"at"`
	Mode       uint32 `json:"mode,omitempty"`
}

// JournalStore persists a journal under a directory, one file per session.
//
// The write is atomic (temporary file in the same directory, then a rename), so
// a crash mid-write leaves the previous journal intact rather than a truncated
// one that would revert half a turn.
type JournalStore struct {
	dir string
}

// NewJournalStore returns a store rooted at dir. An empty dir disables
// persistence: the journal still works in memory.
func NewJournalStore(dir string) *JournalStore {
	return &JournalStore{dir: dir}
}

// Enabled reports whether the store persists anything.
func (s *JournalStore) Enabled() bool {
	return s != nil && s.dir != ""
}

func (s *JournalStore) path(sessionID string) string {
	return filepath.Join(s.dir, "journal-"+safeName(sessionID)+".json")
}

// safeName makes a session ID usable as a file name on every platform. Session
// IDs are UUIDs today, but a file name built from an arbitrary string must not
// be able to escape the directory or trip a Windows reserved name.
func safeName(sessionID string) string {
	out := make([]rune, 0, len(sessionID))
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "session"
	}
	return string(out)
}

// Save writes a journal for a session.
func (s *JournalStore) Save(sessionID string, entries []JournalEntry) error {
	if !s.Enabled() {
		return nil
	}
	if sessionID == "" {
		return errors.New("journal needs a session id")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating journal store: %w", err)
	}
	persisted := persistedJournal{
		SessionID: sessionID,
		Entries:   make([]persistedEntry, 0, len(entries)),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range entries {
		persisted.Entries = append(persisted.Entries, persistedEntry{
			Path:       entry.Path,
			Before:     entry.Before,
			After:      entry.After,
			BeforeHash: entry.BeforeHash,
			AfterHash:  entry.AfterHash,
			Created:    entry.Created,
			Removed:    entry.Removed,
			At:         entry.At.UTC().Format(time.RFC3339Nano),
			Mode:       uint32(entry.Mode),
		})
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("encoding journal: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("creating journal: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing journal: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing journal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing journal: %w", err)
	}
	if err := os.Rename(tmpName, s.path(sessionID)); err != nil {
		return fmt.Errorf("installing journal: %w", err)
	}
	return nil
}

// Load reads a session's journal. A missing or corrupt file is an empty journal,
// not an error: the caller's next step is the same either way.
func (s *JournalStore) Load(sessionID string) []JournalEntry {
	if !s.Enabled() || sessionID == "" {
		return nil
	}
	data, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		return nil
	}
	var persisted persistedJournal
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil
	}
	entries := make([]JournalEntry, 0, len(persisted.Entries))
	for _, entry := range persisted.Entries {
		at, _ := time.Parse(time.RFC3339Nano, entry.At)
		entries = append(entries, JournalEntry{
			Path:       entry.Path,
			Before:     entry.Before,
			After:      entry.After,
			BeforeHash: entry.BeforeHash,
			AfterHash:  entry.AfterHash,
			Created:    entry.Created,
			Removed:    entry.Removed,
			At:         at,
			Mode:       os.FileMode(entry.Mode),
		})
	}
	return entries
}

// Delete removes a session's journal.
func (s *JournalStore) Delete(sessionID string) error {
	if !s.Enabled() || sessionID == "" {
		return nil
	}
	if err := os.Remove(s.path(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// List returns the sessions with a persisted journal.
func (s *JournalStore) List() ([]string, error) {
	if !s.Enabled() {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) < len("journal-.json") {
			continue
		}
		if name[:len("journal-")] != "journal-" || name[len(name)-len(".json"):] != ".json" {
			continue
		}
		sessions = append(sessions, name[len("journal-"):len(name)-len(".json")])
	}
	return sessions, nil
}

// NewJournalFromEntries rebuilds an in-memory journal from persisted entries, so
// a revert can run against a journal loaded from disk.
func NewJournalFromEntries(entries []JournalEntry) *Journal {
	journal := NewJournal()
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.entries = append(journal.entries, entries...)
	return journal
}
