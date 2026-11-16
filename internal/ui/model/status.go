package model

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DefaultStatusTTL is the default time-to-live for status messages.
const DefaultStatusTTL = 5 * time.Second

// badgeLeftInset is the number of cells between the status bar's left edge
// and the mode badge.
const badgeLeftInset = 1

// statusTimerGap is the number of blank cells kept between the shortcut
// hints and the elapsed timer, so the two read as separate things.
const statusTimerGap = 2

// statusTimerInterval is how often the elapsed timer repaints. The value only
// changes once a second, and the shared animation clock stops as soon as no
// spinner is visible, so the timer needs a clock of its own at that rate.
const statusTimerInterval = time.Second

// statusTimerTickMsg asks for one elapsed-timer repaint.
type statusTimerTickMsg struct {
	// gen retires ticks from a clock that was replaced, so stopping and
	// restarting the timer cannot leave two clocks running.
	gen uint64
}

// Status is the status bar and help model.
type Status struct {
	com      *common.Common
	hideHelp bool
	help     help.Model
	helpKm   help.KeyMap
	msg      util.InfoMsg

	// msgSeq identifies the message on screen; expiry timers carry it.
	msgSeq uint64

	// inputMode and permMode drive the mode badge shown before the help
	// hints.
	inputMode uiInputMode
	permMode  permission.PermissionMode

	// sessionID selects the turn clock the elapsed timer reads.
	sessionID string

	// timerRunning is true while a repaint is scheduled, and timerGen
	// identifies that clock so a stale tick cannot extend it.
	timerRunning bool
	timerGen     uint64
}

// NewStatus creates a new status bar and help model.
func NewStatus(com *common.Common, km help.KeyMap) *Status {
	s := new(Status)
	s.com = com
	s.help = help.New()
	s.help.Styles = com.Styles.Help
	s.helpKm = km
	return s
}

// Show puts msg on screen and returns the command that retires it once its
// TTL, or [DefaultStatusTTL] if unset, has passed.
func (s *Status) Show(msg util.InfoMsg) tea.Cmd {
	ttl := msg.TTL
	if ttl <= 0 {
		ttl = DefaultStatusTTL
	}
	s.msg = msg
	s.msgSeq++
	seq := s.msgSeq
	return tea.Tick(ttl, func(time.Time) tea.Msg {
		return util.ClearStatusMsg{Seq: seq}
	})
}

// ClearInfoMsg clears the message on screen if seq still identifies it.
func (s *Status) ClearInfoMsg(seq uint64) {
	if seq == s.msgSeq {
		s.msg = util.InfoMsg{}
	}
}

// SetMode sets the input mode and permission mode used for the mode badge.
func (s *Status) SetMode(mode uiInputMode, permMode permission.PermissionMode) {
	s.inputMode = mode
	s.permMode = permMode
}

// SetSession sets the session whose turn clock the elapsed timer reads.
func (s *Status) SetSession(sessionID string) {
	s.sessionID = sessionID
}

// elapsedView renders the running turn's elapsed time, or an empty string
// when no turn is running in this session. The duration itself is bolded and
// colored so it is readable at a glance from the right edge of the row.
func (s *Status) elapsedView() string {
	elapsed := common.Elapsed(s.sessionID)
	if elapsed == "" {
		return ""
	}
	t := s.com.Styles
	return t.Status.TimerLabel.Render("worked for ") + t.Status.Timer.Render(elapsed)
}

// UpdateTimer arms the elapsed timer's repaint clock, or retires it when no
// turn is running in this session. It is the only place the clock is armed,
// so it is cheap to call from the update tail on every message.
func (s *Status) UpdateTimer() tea.Cmd {
	if common.Elapsed(s.sessionID) == "" {
		s.timerRunning = false
		return nil
	}
	return s.armTimer()
}

// armTimer schedules the next repaint.
func (s *Status) armTimer() tea.Cmd {
	if s.timerRunning {
		return nil
	}
	s.timerRunning = true
	s.timerGen++
	gen := s.timerGen
	return tea.Tick(statusTimerInterval, func(time.Time) tea.Msg {
		return statusTimerTickMsg{gen: gen}
	})
}

// HandleTimerTick consumes one repaint tick and reschedules while a turn is
// still running. It reports whether the timer is what changed, which tells
// the caller to repaint instead of reusing the memoized frame.
func (s *Status) HandleTimerTick(msg statusTimerTickMsg) bool {
	if msg.gen != s.timerGen {
		return false
	}
	s.timerRunning = false
	if common.Elapsed(s.sessionID) == "" {
		return false
	}
	s.armTimer()
	return true
}

// modeBadge renders the badge for the current mode, or an empty string in
// the default coding mode.
func (s *Status) modeBadge() string {
	t := s.com.Styles
	// Mirror the editor prompt precedence: planning wins over YOLO, which
	// can be carried into plan mode.
	if s.inputMode == uiInputModePlan {
		return t.Status.ModeBadgePlan.String()
	}
	switch s.permMode {
	case permission.PermissionModeSysadmin:
		return t.Status.ModeBadgeSysadmin.String()
	case permission.PermissionModeYolo:
		return t.Status.ModeBadgeYolo.String()
	}
	return ""
}

// SetWidth sets the width of the status bar and help view.
func (s *Status) SetWidth(width int) {
	helpStyle := s.com.Styles.Status.Help
	horizontalPadding := helpStyle.GetPaddingLeft() + helpStyle.GetPaddingRight()
	s.help.SetWidth(width - horizontalPadding)
}

// ShowingAll returns whether the full help view is shown.
func (s *Status) ShowingAll() bool {
	return s.help.ShowAll
}

// ToggleHelp toggles the full help view.
func (s *Status) ToggleHelp() {
	s.help.ShowAll = !s.help.ShowAll
}

// SetHideHelp sets whether the app is on the onboarding flow.
func (s *Status) SetHideHelp(hideHelp bool) {
	s.hideHelp = hideHelp
}

// Draw draws the status bar onto the screen.
func (s *Status) Draw(scr uv.Screen, area uv.Rectangle) {
	// The elapsed timer sits on the hint row, hard against the right edge
	// and far from the shortcuts, so it never reads as one of them. It is
	// reserved out of the hint width below.
	elapsed := s.elapsedView()
	elapsedWidth := 0
	if elapsed != "" {
		elapsedWidth = lipgloss.Width(elapsed)
	}

	if !s.hideHelp {
		helpStyle := s.com.Styles.Status.Help
		helpWidth := area.Dx() - helpStyle.GetPaddingLeft() - helpStyle.GetPaddingRight()
		badge := s.modeBadge()
		if badge != "" {
			// Shrink the hints so the badge does not push them past the
			// status area.
			helpWidth -= lipgloss.Width(badge) + 1 + badgeLeftInset
		}
		if elapsedWidth > 0 {
			// Keep the hints clear of the timer rather than letting the
			// help view truncate them into it.
			helpWidth -= elapsedWidth + statusTimerGap
		}
		s.help.SetWidth(max(0, helpWidth))
		helpView := helpStyle.Render(s.help.View(s.helpKm))
		if badge != "" {
			// Indent the rows after the first so the expanded help lines up
			// with the hints on the badge row.
			indent := strings.Repeat(" ", badgeLeftInset+lipgloss.Width(badge)+1)
			helpView = strings.ReplaceAll(helpView, "\n", "\n"+indent)
			helpView = strings.Repeat(" ", badgeLeftInset) + badge + " " + helpView
		}
		uv.NewStyledString(helpView).Draw(scr, area)
	}

	if elapsedWidth > 0 {
		// The first row of the status area is the one directly under the
		// editor, which is where the shortcuts are.
		x := max(area.Min.X, area.Max.X-elapsedWidth)
		uv.NewStyledString(elapsed).Draw(scr, uv.Rect(x, area.Min.Y, elapsedWidth, 1))
	}

	// Render notifications
	if s.msg.IsEmpty() {
		return
	}

	var indStyle lipgloss.Style
	var msgStyle lipgloss.Style
	// Mode banners show the same badge that sits next to the help hints, so
	// they honor the same left inset to keep the indicator from jumping when
	// the banner appears or expires.
	indInset := 0
	switch s.msg.Type {
	case util.InfoTypePlan:
		indStyle = s.com.Styles.Status.ModeBannerPlanBadge
		msgStyle = s.com.Styles.Status.ModeBannerPlan
		indInset = badgeLeftInset
	case util.InfoTypeYolo:
		indStyle = s.com.Styles.Status.ModeBannerYoloBadge
		msgStyle = s.com.Styles.Status.ModeBannerYolo
		indInset = badgeLeftInset
	case util.InfoTypeSysadmin:
		indStyle = s.com.Styles.Status.ModeBannerSysadminBadge
		msgStyle = s.com.Styles.Status.ModeBannerSysadmin
		indInset = badgeLeftInset
	case util.InfoTypeError:
		indStyle = s.com.Styles.Status.ErrorIndicator
		msgStyle = s.com.Styles.Status.ErrorMessage
	case util.InfoTypeWarn:
		indStyle = s.com.Styles.Status.WarnIndicator
		msgStyle = s.com.Styles.Status.WarnMessage
	case util.InfoTypeUpdate:
		indStyle = s.com.Styles.Status.UpdateIndicator
		msgStyle = s.com.Styles.Status.UpdateMessage
	case util.InfoTypeInfo:
		indStyle = s.com.Styles.Status.InfoIndicator
		msgStyle = s.com.Styles.Status.InfoMessage
	case util.InfoTypeSuccess:
		indStyle = s.com.Styles.Status.SuccessIndicator
		msgStyle = s.com.Styles.Status.SuccessMessage
	}

	ind := indStyle.String()
	indWidth := lipgloss.Width(ind)
	msgPad := msgStyle.GetPaddingLeft() + msgStyle.GetPaddingRight()
	avail := max(0, area.Dx()-indWidth-msgPad-indInset)
	msg := strings.Join(strings.Split(s.msg.Msg, "\n"), " ")
	msg = ansi.Truncate(msg, avail, "…")
	if w := lipgloss.Width(msg); w < avail {
		msg += strings.Repeat(" ", avail-w)
	}
	info := msgStyle.Render(msg)

	// Draw the info message over the help view
	uv.NewStyledString(strings.Repeat(" ", indInset)+ind+info).Draw(scr, area)
}
