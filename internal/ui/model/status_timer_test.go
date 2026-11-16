package model

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestStatusDrawsElapsedTimeOnTheRight checks the turn timer renders on the
// hint row, hard against the right edge and well clear of the shortcuts.
func TestStatusDrawsElapsedTimeOnTheRight(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	st := u.status
	st.helpKm = u
	st.SetWidth(100)

	common.StartTurn("s1")
	t.Cleanup(func() { common.StopTurn("s1") })
	st.SetSession("s1")

	lines := drawStatusLines(t, st, 100, 3)
	first := strings.TrimRight(lines[0], " ")
	require.Contains(t, first, "worked for", "the timer label must be on the hint row: %q", first)

	// The timer is pinned to the right edge of the status area.
	require.Equal(t, 100, lipgloss.Width(first), "the timer must end at the right edge: %q", first)

	// The hints start on the left, so the two are separated by a clear gap
	// rather than running together.
	require.Contains(t, first, "worked for", "unexpected timer text: %q", first)
	require.Greater(t, strings.Count(first, "  "), 1, "the shortcuts and the timer must be separated: %q", first)
	require.Contains(t, first, "tab focus chat", "the hints must keep their shortcuts: %q", first)
}

// TestStatusHidesElapsedTimeWhenIdle checks the timer disappears once no turn
// is running, rather than showing a stale duration.
func TestStatusHidesElapsedTimeWhenIdle(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	st := u.status
	st.helpKm = u
	st.SetWidth(100)
	st.SetSession("idle-session")

	lines := drawStatusLines(t, st, 100, 3)
	require.NotContains(t, lines[0], "worked for")
}

// TestStatusTimerDoesNotTruncateShortcuts checks the hints keep their room
// when the timer is on screen, instead of being pushed under it.
func TestStatusTimerDoesNotTruncateShortcuts(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	st := u.status
	st.helpKm = u
	st.SetWidth(120)

	common.StartTurn("s2")
	t.Cleanup(func() { common.StopTurn("s2") })
	st.SetSession("s2")

	lines := drawStatusLines(t, st, 120, 3)
	require.Contains(t, lines[0], "worked for")

	// Whatever the help view renders first must still be there in full.
	hints := strings.TrimSpace(u.ShortHelp()[0].Help().Key)
	require.Contains(t, ansi.Strip(lines[0]), strings.TrimRight(hints, " "),
		"the first shortcut must survive the timer's reservation")
}

// TestStatusTimerRepaintScheduling checks the timer clock arms while a turn
// runs, repaints, and retires when the turn ends.
func TestStatusTimerRepaintScheduling(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	st := u.status
	st.SetSession("s3")

	require.Nil(t, st.UpdateTimer(), "no turn: no clock")

	common.StartTurn("s3")
	t.Cleanup(func() { common.StopTurn("s3") })

	require.NotNil(t, st.UpdateTimer(), "a running turn must arm the clock")
	require.Nil(t, st.UpdateTimer(), "the clock must not be double-armed")

	require.True(t, st.HandleTimerTick(statusTimerTickMsg{gen: st.timerGen}), "a live tick repaints")
	require.True(t, st.timerRunning, "the tick must reschedule while the turn runs")

	require.False(t, st.HandleTimerTick(statusTimerTickMsg{gen: st.timerGen - 1}),
		"a tick from a retired clock must be ignored")

	common.StopTurn("s3")
	st.timerRunning = false
	require.False(t, st.HandleTimerTick(statusTimerTickMsg{gen: st.timerGen}),
		"a tick with no running turn must not repaint")
}

// TestTimerWiringPassesTheSession checks the update tail hands the status bar
// the session whose turn it should be timing. Draw only calls SetSession too,
// but a timer that only learns its session at paint time would show the wrong
// clock between a session switch and the first repaint.
func TestTimerWiringPassesTheSession(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	u.session = &session.Session{ID: "wired"}

	u.beginFrameUpdate()
	u.Update(struct{}{})

	require.Equal(t, "wired", u.status.sessionID)
}

// TestTimerTickInvalidatesTheFrame checks a elapsed-second repaint discards the
// memoized frame; otherwise a scroll-only frame could be served with a frozen
// timer for up to the frame TTL.
func TestTimerTickInvalidatesTheFrame(t *testing.T) {
	t.Parallel()

	u := newFrameTestUI(t)
	u.session = &session.Session{ID: "s4"}
	common.StartTurn("s4")
	t.Cleanup(func() { common.StopTurn("s4") })

	u.status.SetSession("s4")
	u.status.timerRunning = true
	require.True(t, u.status.HandleTimerTick(statusTimerTickMsg{gen: u.status.timerGen}))

	u.beginFrameUpdate()
	u.Update(statusTimerTickMsg{gen: u.status.timerGen})
	require.True(t, u.frameDirty, "a repaint must drop the memoized frame")
}
