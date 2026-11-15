package governor

import (
	"fmt"
	"strings"
)

// SteeringNote renders the note for a report. The wording is the escalation
// ladder itself: each level says something the previous one did not, and every
// level keeps the door open for genuinely new work so a note never becomes a
// refusal to do the task.
func SteeringNote(report Report) string {
	switch report.Level {
	case LevelSoft:
		return "[Agent Governor] Recent lookups have produced diminishing returns. Aim the next search or read at a specific open question instead of a general sweep."
	case LevelModerate:
		return "[Agent Governor] Your recent lookups have produced little new information, so stop re-asking what you have already answered — and keep working: aim the next lookup at a specific unresolved requirement of the request."
	case LevelStrong:
		return "[Agent Governor] This turn has re-issued the same exploration repeatedly without gaining information. Redundant repetitions of an already-answered search/read will now be blocked; a genuinely new query or a different region of a file still runs."
	case LevelHard:
		return "[Agent Governor] This turn has re-issued the same exploration repeatedly without gaining information. Redundant repetitions of an already-answered search/read will now be blocked; a genuinely new query or a different region of a file still runs. Act on what you have, or answer the user."
	default:
		return ""
	}
}

// GatheredNote is the variant delivered once the turn has collected
// substantial context: at that point the useful advice is to aim, not to stop.
func GatheredNote() string {
	return "[Agent Governor] You have gathered substantial context. Aim each further lookup at a specific open question — an unread file or region is still worth reading when the remaining work needs it."
}

// StopRequestedNote tells the model the user asked to stop.
func StopRequestedNote() string {
	return "[Agent Governor] The user asked to stop. End the turn now: write the summary of what you have already established and stop calling tools. Do not start new work."
}

// QuestionNote tells the model to answer first, then continue.
func QuestionNote() string {
	return "[Agent Governor] The user asked a question. Answer it in your next reply first — then continue the tool work the task still needs. Do not answer by calling another tool."
}

// ScopeAddedNote tells the model to fold new scope into the plan.
func ScopeAddedNote() string {
	return "[Agent Governor] The user added scope. Keep the work already done, fold the addition into your plan (a todo list, if you have one), and cover it before concluding."
}

// RejectedApproachNote tells the model to re-derive after a rejection.
func RejectedApproachNote() string {
	return "[Agent Governor] The user rejected the current approach. Discard the plan you were following and the conclusions it rested on; re-derive from the request with fresh reads or a different route. Do not defend the discarded plan."
}

// RequirementChangedNote tells the model which words are authoritative.
func RequirementChangedNote() string {
	return "[Agent Governor] The requirement changed mid-turn. The newest message is authoritative, not the original ask; anything you concluded about the earlier version is no longer authoritative. Re-orient to the current contract before your next call."
}

// SettledStateNote is delivered when the turn re-deliberates facts it already
// established.
func SettledStateNote(standing string) string {
	standing = strings.TrimSpace(standing)
	if standing == "" {
		return "[Settled state] — you are re-deliberating facts this turn already settled."
	}
	return "[Settled state] — you are re-deliberating facts this turn already settled. This is what still stands: " + standing
}

// AlreadyRefutedNote names a theory the turn's own evidence killed.
func AlreadyRefutedNote(theory string) string {
	return fmt.Sprintf("Already ruled out this turn by your own change and its result: %s. Do not re-derive it — the evidence that refuted it is still in this conversation.", theory)
}
