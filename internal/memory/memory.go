// Package memory is the project's long-term knowledge: a small SQLite store of
// lessons keyed by when they apply, retrieved before a turn and injected into the
// prompt.
//
// A memory is worth keeping only if it changes what the agent does next, so the
// store is small by design: topic, the phrase that should trigger it, and the
// lesson. Every entry carries a query trigger so retrieval is a lookup rather
// than a scan, and the injection text tells the model to honour or remove each
// one, because a stale memory is worse than no memory.
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Entry is one remembered lesson.
type Entry struct {
	// ID identifies the entry.
	ID string
	// ProjectRoot scopes the entry to one project, so a lesson learned in one
	// repository does not leak into another.
	ProjectRoot string
	// Topic names what the lesson is about, such as "build" or "migrations".
	Topic string
	// Trigger is the phrase or pattern that should surface the lesson.
	Trigger string
	// Lesson is the instruction itself, in one or two sentences.
	Lesson string
	// CreatedAt and UpdatedAt are unix seconds.
	CreatedAt int64
	UpdatedAt int64
}

// Store is a project-memory database.
type Store struct {
	db *sql.DB
}

// schema is the memory table plus its full-text index. FTS5 comes with the
// SQLite build Crush already links, and it is what makes retrieval by trigger
// cheap enough to run before every turn.
const schema = `
CREATE TABLE IF NOT EXISTS project_memories (
	id TEXT PRIMARY KEY,
	project_root TEXT NOT NULL,
	topic TEXT NOT NULL,
	trigger_pattern TEXT NOT NULL,
	lesson TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memories_root ON project_memories(project_root);
CREATE VIRTUAL TABLE IF NOT EXISTS project_memories_fts USING fts5(
	id UNINDEXED,
	project_root UNINDEXED,
	topic,
	trigger_pattern,
	lesson,
	tokenize = 'unicode61'
);
`

// Open opens (and migrates) the memory store at path. An empty path opens an
// in-memory database, which is what tests and a project without a data directory
// use.
func Open(path string) (*Store, error) {
	dsn := path
	if path == "" {
		dsn = "file::memory:?cache=shared"
	} else if !strings.Contains(path, "?") {
		dsn = path + "?_busy_timeout=5000&_journal_mode=WAL"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening memory store: %w", err)
	}
	// One connection keeps the in-memory database alive and serialises writes,
	// which is all this store needs.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating memory store: %w", err)
	}
	return &Store{db: db}, nil
}

// DefaultPath returns the store path inside a data directory.
func DefaultPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "memories.db")
}

// Close closes the store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Save inserts or updates a lesson, deduplicating on topic and text so the same
// lesson learned twice is stored once.
func (s *Store) Save(ctx context.Context, entry Entry) (Entry, error) {
	if s == nil {
		return Entry{}, fmt.Errorf("memory store is nil")
	}
	entry.ProjectRoot = cleanRoot(entry.ProjectRoot)
	if entry.ProjectRoot == "" {
		return Entry{}, fmt.Errorf("a memory needs a project root")
	}
	if strings.TrimSpace(entry.Lesson) == "" {
		return Entry{}, fmt.Errorf("a memory needs a lesson")
	}
	if entry.Topic == "" {
		entry.Topic = "general"
	}
	now := time.Now().Unix()
	if existing, ok, err := s.findDuplicate(ctx, entry); err != nil {
		return Entry{}, err
	} else if ok {
		existing.UpdatedAt = now
		existing.Trigger = entry.Trigger
		if err := s.update(ctx, existing); err != nil {
			return Entry{}, err
		}
		return existing, nil
	}
	if entry.ID == "" {
		entry.ID = newID(entry.ProjectRoot, entry.Topic, entry.Lesson, now)
	}
	entry.CreatedAt = now
	entry.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO project_memories (id, project_root, topic, trigger_pattern, lesson, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.ProjectRoot, entry.Topic, entry.Trigger, entry.Lesson, entry.CreatedAt, entry.UpdatedAt,
	); err != nil {
		return Entry{}, fmt.Errorf("saving memory: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO project_memories_fts (id, project_root, topic, trigger_pattern, lesson) VALUES (?, ?, ?, ?, ?)`,
		entry.ID, entry.ProjectRoot, entry.Topic, entry.Trigger, entry.Lesson,
	); err != nil {
		return Entry{}, fmt.Errorf("indexing memory: %w", err)
	}
	return entry, nil
}

// findDuplicate returns the entry with the same topic and lesson, if any.
func (s *Store) findDuplicate(ctx context.Context, entry Entry) (Entry, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_root, topic, trigger_pattern, lesson, created_at, updated_at
		 FROM project_memories WHERE project_root = ? AND LOWER(topic) = LOWER(?) AND lesson = ? LIMIT 1`,
		entry.ProjectRoot, entry.Topic, entry.Lesson)
	var found Entry
	err := row.Scan(&found.ID, &found.ProjectRoot, &found.Topic, &found.Trigger, &found.Lesson, &found.CreatedAt, &found.UpdatedAt)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return found, true, nil
}

// update rewrites an existing entry and reindexes it.
func (s *Store) update(ctx context.Context, entry Entry) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_memories SET trigger_pattern = ?, updated_at = ? WHERE id = ?`,
		entry.Trigger, entry.UpdatedAt, entry.ID); err != nil {
		return err
	}
	// The index is rebuilt for the row rather than updated: FTS5 has no
	// partial update, and deleting by rowid would need one more lookup.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM project_memories_fts WHERE id = ?`, entry.ID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_memories_fts (id, project_root, topic, trigger_pattern, lesson) VALUES (?, ?, ?, ?, ?)`,
		entry.ID, entry.ProjectRoot, entry.Topic, entry.Trigger, entry.Lesson)
	return err
}

// List returns every memory for a project, oldest first.
func (s *Store) List(ctx context.Context, projectRoot string) ([]Entry, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_root, topic, trigger_pattern, lesson, created_at, updated_at
		 FROM project_memories WHERE project_root = ? ORDER BY created_at`,
		cleanRoot(projectRoot))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.ProjectRoot, &entry.Topic, &entry.Trigger,
			&entry.Lesson, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// Search returns the memories whose indexed text matches the query, best first.
// A blank query returns nothing: retrieval is meant to be aimed, and a recall
// that returns everything is the same as no recall.
func (s *Store) Search(ctx context.Context, projectRoot, query string, limit int) ([]Entry, error) {
	if s == nil {
		return nil, nil
	}
	cleaned := strings.TrimSpace(query)
	if cleaned == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.project_root, m.topic, m.trigger_pattern, m.lesson, m.created_at, m.updated_at
		 FROM project_memories_fts f
		 JOIN project_memories m ON m.id = f.id
		 WHERE project_memories_fts MATCH ? AND f.project_root = ?
		 ORDER BY rank LIMIT ?`,
		ftsQuery(cleaned), cleanRoot(projectRoot), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.ProjectRoot, &entry.Topic, &entry.Trigger,
			&entry.Lesson, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// Delete removes one memory by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || id == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM project_memories WHERE id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_memories_fts WHERE id = ?`, id)
	return err
}

// DeleteProject removes every memory for a project.
func (s *Store) DeleteProject(ctx context.Context, projectRoot string) error {
	if s == nil {
		return nil
	}
	root := cleanRoot(projectRoot)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM project_memories WHERE project_root = ?`, root); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_memories_fts WHERE project_root = ?`, root)
	return err
}

// ftsQuery builds a safe MATCH expression: each word of the query becomes a
// quoted prefix term, so a query with punctuation cannot be read as FTS5 syntax.
func ftsQuery(query string) string {
	words := strings.Fields(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, query))
	if len(words) == 0 {
		return `""`
	}
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, `"`+word+`"*`)
	}
	return strings.Join(quoted, " OR ")
}

// cleanRoot normalises a project root so two spellings share one scope.
func cleanRoot(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

// newID builds a stable identifier from the entry's content and time.
func newID(root, topic, lesson string, at int64) string {
	sum := fnv64(root + "\x00" + topic + "\x00" + lesson)
	return fmt.Sprintf("mem-%x-%x", sum, at)
}

// fnv64 is a small FNV-1a, used so IDs are stable without a crypto dependency.
func fnv64(value string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	hash := uint64(offset)
	for i := range len(value) {
		hash ^= uint64(value[i])
		hash *= prime
	}
	return hash
}

// PromptBlock renders the memories for injection.
//
// The instruction is deliberately two-sided: honour each memory, and remove the
// one that is no longer true. A prompt that only asks the model to honour stale
// notes makes every future turn worse than the one before it.
func PromptBlock(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<project_memories>\n")
	sb.WriteString("PROJECT MEMORIES & LEARNED CONSTRAINTS (retrieved for this project — STRICTLY check and honor these memories at the beginning before answering the user question or executing actions. If any memory is obsolete, incorrect, or no longer necessary, remove it with the memory removal tool):\n")
	for _, entry := range entries {
		sb.WriteString("- [")
		sb.WriteString(entry.Topic)
		sb.WriteString("] ")
		if entry.Trigger != "" {
			sb.WriteString("when ")
			sb.WriteString(entry.Trigger)
			sb.WriteString(": ")
		}
		sb.WriteString(entry.Lesson)
		sb.WriteString("\n")
	}
	sb.WriteString("</project_memories>\n\n")
	return sb.String()
}
