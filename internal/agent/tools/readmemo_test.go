package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadMemoMissThenHit(t *testing.T) {
	t.Parallel()

	memo := NewReadMemo()
	hash := fileHash("body")
	_, seen := memo.Seen("s1", "/a.go", hash, 0, 200)
	require.False(t, seen, "nothing recorded yet")

	memo.Record("s1", "/a.go", hash, 100, 0, 200)
	pointer, seen := memo.Seen("s1", "/a.go", hash, 0, 200)
	require.True(t, seen)
	require.Contains(t, pointer, "/a.go")
	require.Contains(t, pointer, "lines 1-200")
	require.Contains(t, pointer, "sha256:")
	require.Contains(t, pointer, "100 tokens")
}

func TestReadMemoDistinguishesContentAndRange(t *testing.T) {
	t.Parallel()

	memo := NewReadMemo()
	memo.Record("s1", "/a.go", fileHash("first"), 10, 0, 200)

	// Same range, changed content.
	_, seen := memo.Seen("s1", "/a.go", fileHash("second"), 0, 200)
	require.False(t, seen, "a changed file must be re-sent")

	// Different range of the same content.
	_, seen = memo.Seen("s1", "/a.go", fileHash("first"), 200, 200)
	require.False(t, seen, "a range the model has not seen is never a pointer")
}

func TestReadMemoPerSession(t *testing.T) {
	t.Parallel()

	memo := NewReadMemo()
	memo.Record("s1", "/a.go", "h", 10, 0, 200)
	_, seen := memo.Seen("s2", "/a.go", "h", 0, 200)
	require.False(t, seen)
	require.Equal(t, 1, memo.Len("s1"))
	require.Zero(t, memo.Len("s2"))
}

func TestReadMemoForget(t *testing.T) {
	t.Parallel()

	memo := NewReadMemo()
	memo.Record("s1", "/a.go", "h", 10, 0, 200)
	memo.Record("s1", "/b.go", "h", 10, 0, 200)
	memo.Forget("s1", "/a.go")
	_, seen := memo.Seen("s1", "/a.go", "h", 0, 200)
	require.False(t, seen)
	_, seen = memo.Seen("s1", "/b.go", "h", 0, 200)
	require.True(t, seen)

	memo.ForgetSession("s1")
	require.Zero(t, memo.Len("s1"))
}

func TestReadMemoEvictsOldest(t *testing.T) {
	t.Parallel()

	memo := NewReadMemo()
	memo.limit = 2
	clock := time.Unix(0, 0)
	memo.SetClock(func() time.Time { return clock })

	memo.Record("s1", "/a.go", "h", 10, 0, 1)
	clock = clock.Add(time.Second)
	memo.Record("s1", "/b.go", "h", 10, 0, 1)
	clock = clock.Add(time.Second)
	memo.Record("s1", "/c.go", "h", 10, 0, 1)

	require.Equal(t, 2, memo.Len("s1"))
	_, seen := memo.Seen("s1", "/a.go", "h", 0, 1)
	require.False(t, seen, "the oldest entry is evicted")
	_, seen = memo.Seen("s1", "/c.go", "h", 0, 1)
	require.True(t, seen)
}

func TestReadMemoNilAndEmpty(t *testing.T) {
	t.Parallel()

	var nilMemo *ReadMemo
	_, seen := nilMemo.Seen("s1", "/a.go", "h", 0, 1)
	require.False(t, seen)
	nilMemo.Record("s1", "/a.go", "h", 1, 0, 1)
	nilMemo.Forget("s1", "/a.go")
	nilMemo.ForgetSession("s1")
	require.Zero(t, nilMemo.Len("s1"))

	empty := NewReadMemo()
	empty.Record("", "/a.go", "h", 1, 0, 1)
	empty.Record("s1", "", "h", 1, 0, 1)
	require.Zero(t, empty.Len("s1"))
	_, seen = empty.Seen("", "/a.go", "h", 0, 1)
	require.False(t, seen)
}

func TestFileHashIsContentIdentity(t *testing.T) {
	t.Parallel()

	require.Equal(t, fileHash("same"), fileHash("same"))
	require.NotEqual(t, fileHash("same"), fileHash("same\n"))
	require.Len(t, fileHash(strings.Repeat("x", 1000)), 16)
}
