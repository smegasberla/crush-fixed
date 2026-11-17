package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListFromSummary(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	ids := make([]string, 0, 5)
	for range 5 {
		msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}

	t.Run("empty summary returns the whole session", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromSummary(t.Context(), sessionID, "")
		require.NoError(t, err)
		require.Len(t, got, len(ids))
	})

	t.Run("returns exactly the summary message and what follows", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromSummary(t.Context(), sessionID, ids[2])
		require.NoError(t, err)

		// Every message here was written in the same second, so a
		// timestamp-based cut would return all five. The window marker has to
		// be an exact position, or a compaction silently changes nothing.
		require.Len(t, got, 3, "the window must start exactly at the marker")
		require.Equal(t, ids[2], got[0].ID)
		require.Equal(t, ids[4], got[2].ID)
	})

	// A missing summary must not silently erase the history.
	t.Run("dangling summary falls back to the whole session", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromSummary(t.Context(), sessionID, "does-not-exist")
		require.NoError(t, err)
		require.Len(t, got, len(ids))
	})
}
