package contextreduction

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DigestMemo is a continuation brief stored on disk. It is keyed by the chain
// hash of everything it absorbed, so re-digesting the same prefix is free and a
// new brief extends the previous one instead of re-summarising the transcript.
type DigestMemo struct {
	// Chain is the chain hash of the absorbed prefix. It is the memo key.
	Chain string `json:"chain"`
	// Absorbed is how many messages the brief covers.
	Absorbed int `json:"absorbed"`
	// Brief is the rendered continuation brief.
	Brief string `json:"brief"`
	// Misses counts how often a lookup for this chain missed, for diagnostics.
	Misses int `json:"misses"`
	// UpdatedAt is when the memo was last written, in RFC 3339.
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// DigestStore memoizes continuation briefs under a directory.
//
// A digest costs a model call, so the result is cached by content: the chain
// hash changes only when the absorbed prefix changes, which makes a repeated
// build free and lets a new brief extend an existing one. Writes go through a
// temporary file in the same directory and a rename, so the swap is atomic on
// every supported platform (a cross-volume rename is not, and fails on Windows),
// and a corrupt memo is treated as a miss rather than an error.
type DigestStore struct {
	dir string
}

// NewDigestStore returns a store rooted at dir. An empty dir disables
// persistence: the store still reports misses, it just never writes.
func NewDigestStore(dir string) *DigestStore {
	return &DigestStore{dir: dir}
}

// Dir returns the store's directory.
func (s *DigestStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Enabled reports whether the store persists anything.
func (s *DigestStore) Enabled() bool {
	return s != nil && s.dir != ""
}

func (s *DigestStore) path(chain string) string {
	return filepath.Join(s.dir, chain+".json")
}

// Load returns the memo for a chain, or false when it is absent or unreadable.
// An unreadable memo is a miss, never an error: the caller regenerates it.
func (s *DigestStore) Load(chain string) (DigestMemo, bool) {
	if !s.Enabled() || chain == "" {
		return DigestMemo{}, false
	}
	data, err := os.ReadFile(s.path(chain))
	if err != nil {
		return DigestMemo{}, false
	}
	var memo DigestMemo
	if err := json.Unmarshal(data, &memo); err != nil {
		return DigestMemo{}, false
	}
	if memo.Brief == "" {
		return DigestMemo{}, false
	}
	return memo, true
}

// Store writes a memo atomically.
func (s *DigestStore) Store(memo DigestMemo) error {
	if !s.Enabled() {
		return nil
	}
	if memo.Chain == "" {
		return errors.New("digest memo needs a chain hash")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating digest store: %w", err)
	}
	memo.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(memo)
	if err != nil {
		return fmt.Errorf("encoding digest memo: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".memo-*.tmp")
	if err != nil {
		return fmt.Errorf("creating digest memo: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing digest memo: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing digest memo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing digest memo: %w", err)
	}
	if err := os.Rename(tmpName, s.path(memo.Chain)); err != nil {
		return fmt.Errorf("installing digest memo: %w", err)
	}
	return nil
}

// Delete removes a memo. A missing memo is not an error.
func (s *DigestStore) Delete(chain string) error {
	if !s.Enabled() || chain == "" {
		return nil
	}
	if err := os.Remove(s.path(chain)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Chains lists the memo keys present in the store, for debug output and for
// pruning. A missing directory yields no keys and no error.
func (s *DigestStore) Chains() ([]string, error) {
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
	chains := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		chains = append(chains, strings.TrimSuffix(name, ".json"))
	}
	return chains, nil
}

// MergeBrief composes a brief from a previous brief and a new delta. Keeping
// the older section verbatim is the point: a brief that is regenerated from
// scratch every time loses the invariants the earliest turns established.
func MergeBrief(previous, delta string) string {
	previous = strings.TrimSpace(previous)
	delta = strings.TrimSpace(delta)
	switch {
	case previous == "":
		return delta
	case delta == "":
		return previous
	default:
		return previous + "\n\n### Update\n" + delta
	}
}
