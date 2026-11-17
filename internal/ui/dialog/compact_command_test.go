package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestCompactCommandItem checks the command palette's Compact item is a
// manual compaction action, not the old Summarize action. Typing "/" opens
// the palette, so this is what a user sees when asking to compact.
func TestCompactCommandItem(t *testing.T) {
	t.Parallel()

	st := styles.CharmtonePantera()
	item := NewCommandItem(&st, "compact", "Compact Session", "",
		ActionCompact{SessionID: "session-id"}).
		WithAliases("compact", "summarize").
		WithDescription("Fold older messages now, keeping recent work verbatim")

	require.Equal(t, "compact", item.ID())
	require.Contains(t, item.Filter(), "Compact Session")
	require.Contains(t, item.Filter(), "compact")
	require.IsType(t, ActionCompact{}, item.Action())
}
