package tools

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/contextreduction"
)

// ReadMemo remembers, per session, what was already read this turn and what it
// hashed to. It is what lets a repeated view of an unchanged file answer with a
// compact pointer instead of the whole body: the model gets a definite answer
// ("this is the copy you already have") rather than a silently truncated one,
// and the prompt stops carrying the same file twice.
//
// A memo is bounded per session. It only needs to cover the current turn, so
// the oldest entries are evicted when the cap is reached instead of the map
// growing for the life of the process.
type ReadMemo struct {
	mu       sync.Mutex
	sessions map[string]map[string]readMemoEntry
	// limit is the per-session entry cap.
	limit int
	// now is the clock, replaceable in tests.
	now func() time.Time
}

type readMemoEntry struct {
	// hash is the content hash of the body that was returned in full.
	hash string
	// tokens is that body's estimated size.
	tokens int
	// offset and limit pin the range the entry describes. A read of a
	// different window is never answered with a pointer, because the model
	// asked for bytes it has not seen.
	offset int
	limit  int
	// at is when the entry was recorded, for eviction order.
	at time.Time
	// fullReads counts how many times the same range was served in full.
	fullReads int
}

// DefaultReadMemoLimit is how many distinct ranges are remembered per session.
const DefaultReadMemoLimit = 256

// NewReadMemo returns an empty memo.
func NewReadMemo() *ReadMemo {
	return &ReadMemo{
		sessions: make(map[string]map[string]readMemoEntry),
		limit:    DefaultReadMemoLimit,
		now:      time.Now,
	}
}

// Seen reports whether the exact same range of the file was already returned in
// full this session, and returns the pointer text when it was.
func (m *ReadMemo) Seen(sessionID, path, hash string, offset, limit int) (string, bool) {
	if m == nil || sessionID == "" || path == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.sessions[sessionID]
	if !ok {
		return "", false
	}
	entry, ok := entries[path]
	if !ok || entry.hash != hash || entry.offset != offset || entry.limit != limit {
		return "", false
	}
	entry.fullReads++
	entry.at = m.now()
	entries[path] = entry
	return fmt.Sprintf("[file unchanged: %s lines %d-%d is already in this conversation from an earlier view with identical content (%d tokens, sha256:%s). Re-read it only if it changed, or ask for a different range.]",
		path, offset+1, offset+limit, entry.tokens, hash), true
}

// Record stores a body that was returned in full.
func (m *ReadMemo) Record(sessionID, path, hash string, tokens, offset, limit int) {
	if m == nil || sessionID == "" || path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.sessions[sessionID]
	if !ok {
		entries = make(map[string]readMemoEntry)
		m.sessions[sessionID] = entries
	}
	if _, exists := entries[path]; !exists && len(entries) >= m.limit {
		evictOldest(entries)
	}
	entries[path] = readMemoEntry{
		hash:   hash,
		tokens: tokens,
		offset: offset,
		limit:  limit,
		at:     m.now(),
	}
}

// Forget drops one file, so a write can invalidate the pointer without clearing
// the whole session.
func (m *ReadMemo) Forget(sessionID, path string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if entries, ok := m.sessions[sessionID]; ok {
		delete(entries, path)
	}
}

// ForgetSession drops everything recorded for a session.
func (m *ReadMemo) ForgetSession(sessionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// Len returns the number of remembered ranges for a session, for tests and
// debug output.
func (m *ReadMemo) Len(sessionID string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions[sessionID])
}

// evictOldest removes the least recently recorded entry. Callers hold the lock.
func evictOldest(entries map[string]readMemoEntry) {
	var oldestKey string
	var oldest time.Time
	for key, entry := range entries {
		if oldestKey == "" || entry.at.Before(oldest) {
			oldestKey = key
			oldest = entry.at
		}
	}
	if oldestKey != "" {
		delete(entries, oldestKey)
	}
}

// fileHash returns a stable identity for a body. It is the hash of the bytes the
// caller actually read, so a file modified between two views is never mistaken
// for the same content.
func fileHash(content string) string {
	return contextreduction.DigestHash(content)
}

// SetClock replaces the memo clock. Tests use it to make eviction deterministic
// without sleeping.
func (m *ReadMemo) SetClock(clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = clock
}
