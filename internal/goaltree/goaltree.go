// Package goaltree tracks a multi-step goal as a tree of items whose completion
// must be backed by evidence.
//
// A flat todo list says "done" because the model typed done. A goal tree asks a
// different question: which evidence proves this item? An item cannot close
// without one, and a parent cannot close while a child is open, so "every step
// complete" means something checkable rather than something claimed.
package goaltree

import (
	"fmt"
	"strings"
)

// Status is an item's state.
type Status string

const (
	// StatusPending means the work has not started.
	StatusPending Status = "pending"
	// StatusActive means it is in progress.
	StatusActive Status = "in_progress"
	// StatusDone means it is complete with evidence.
	StatusDone Status = "completed"
	// StatusBlocked means something external stops it.
	StatusBlocked Status = "blocked"
	// StatusFailed means it was attempted and did not work.
	StatusFailed Status = "failed"
)

// Item is one node of the goal tree.
type Item struct {
	// ID identifies the item.
	ID string
	// Task is what must be done, in one line.
	Task string
	// Status is its state.
	Status Status
	// Evidence is what proves it: a command and its result, a file and the
	// change, a test name and its pass. An empty evidence list means the item
	// cannot be closed.
	Evidence []string
	// Children are its sub-items.
	Children []*Item
}

// open reports whether the item still needs work.
func (i *Item) open() bool {
	switch i.Status {
	case StatusDone:
		return false
	default:
		return true
	}
}

// Tree is a goal and its items.
type Tree struct {
	// Goal is the objective in the user's words.
	Goal string
	// Items are the top-level steps.
	Items []*Item
}

// New returns an empty tree for a goal.
func New(goal string) *Tree {
	return &Tree{Goal: strings.TrimSpace(goal)}
}

// Add appends a top-level item.
func (t *Tree) Add(item *Item) *Item {
	if t == nil || item == nil {
		return nil
	}
	t.Items = append(t.Items, item)
	return item
}

// AddChild appends a child under an item identified by ID.
func (t *Tree) AddChild(parentID string, child *Item) bool {
	if t == nil || child == nil {
		return false
	}
	parent := t.Find(parentID)
	if parent == nil {
		return false
	}
	parent.Children = append(parent.Children, child)
	return true
}

// Find returns the item with an ID, or nil.
func (t *Tree) Find(id string) *Item {
	if t == nil || id == "" {
		return nil
	}
	var walk func(items []*Item) *Item
	walk = func(items []*Item) *Item {
		for _, item := range items {
			if item.ID == id {
				return item
			}
			if found := walk(item.Children); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(t.Items)
}

// Close marks an item done, but only when it carries evidence and every child is
// already done.
//
// This is the whole point of the tree: a completion claim becomes a fact or it is
// refused with a reason, rather than being taken on trust.
func (t *Tree) Close(id string, evidence []string) error {
	item := t.Find(id)
	if item == nil {
		return fmt.Errorf("no item with id %q", id)
	}
	if len(evidence) == 0 {
		return fmt.Errorf("%s cannot be completed without evidence: name the command, file or test that proves it", label(item))
	}
	for _, child := range item.Children {
		if child.open() {
			return fmt.Errorf("%s has an unfinished step: %s", label(item), label(child))
		}
	}
	item.Status = StatusDone
	item.Evidence = append(item.Evidence, evidence...)
	return nil
}

// Open returns the next unfinished item, depth first, so a caller always has one
// concrete step to take.
func (t *Tree) Open() *Item {
	if t == nil {
		return nil
	}
	var walk func(items []*Item) *Item
	walk = func(items []*Item) *Item {
		for _, item := range items {
			if item.Status == StatusBlocked || item.Status == StatusFailed {
				continue
			}
			if item.open() {
				if child := walk(item.Children); child != nil {
					return child
				}
				return item
			}
		}
		return nil
	}
	return walk(t.Items)
}

// OpenCount returns how many items are unfinished.
func (t *Tree) OpenCount() int {
	if t == nil {
		return 0
	}
	count := 0
	var walk func(items []*Item)
	walk = func(items []*Item) {
		for _, item := range items {
			if item.open() {
				count++
			}
			walk(item.Children)
		}
	}
	walk(t.Items)
	return count
}

// Total returns how many items exist.
func (t *Tree) Total() int {
	if t == nil {
		return 0
	}
	count := 0
	var walk func(items []*Item)
	walk = func(items []*Item) {
		for _, item := range items {
			count++
			walk(item.Children)
		}
	}
	walk(t.Items)
	return count
}

// Finished reports whether every item is done.
func (t *Tree) Finished() bool {
	return t != nil && t.Total() > 0 && t.OpenCount() == 0
}

// UnverifiedPaths returns the items that are marked done but carry no evidence.
// It exists because a tree edited outside Close can still claim a bare
// completion, and the caller deserves to know.
func (t *Tree) UnverifiedPaths() []*Item {
	if t == nil {
		return nil
	}
	var unverified []*Item
	var walk func(items []*Item)
	walk = func(items []*Item) {
		for _, item := range items {
			if item.Status == StatusDone && len(item.Evidence) == 0 {
				unverified = append(unverified, item)
			}
			walk(item.Children)
		}
	}
	walk(t.Items)
	return unverified
}

// Render renders the tree as an indented checklist, which is what the prompt
// shows.
func (t *Tree) Render() string {
	if t == nil || len(t.Items) == 0 {
		return ""
	}
	var sb strings.Builder
	if t.Goal != "" {
		sb.WriteString("GOAL: ")
		sb.WriteString(t.Goal)
		sb.WriteString("\n")
	}
	var walk func(items []*Item, depth int)
	walk = func(items []*Item, depth int) {
		for _, item := range items {
			sb.WriteString(strings.Repeat("  ", depth))
			sb.WriteString(marker(item.Status))
			sb.WriteString(" ")
			sb.WriteString(item.Task)
			if len(item.Evidence) > 0 {
				sb.WriteString(" (evidence: ")
				sb.WriteString(strings.Join(item.Evidence, "; "))
				sb.WriteString(")")
			}
			sb.WriteString("\n")
			walk(item.Children, depth+1)
		}
	}
	walk(t.Items, 0)
	return strings.TrimRight(sb.String(), "\n")
}

// marker returns the checklist box for a status.
func marker(status Status) string {
	switch status {
	case StatusDone:
		return "[x]"
	case StatusActive:
		return "[~]"
	case StatusBlocked:
		return "[!]"
	case StatusFailed:
		return "[✗]"
	default:
		return "[ ]"
	}
}

// label names an item for a message.
func label(item *Item) string {
	if item.ID != "" {
		return fmt.Sprintf("%s (%q)", item.ID, item.Task)
	}
	return fmt.Sprintf("%q", item.Task)
}

// ResumeCandidate is what a session left unfinished, for the startup probe.
type ResumeCandidate struct {
	// SessionID is the session the work belongs to.
	SessionID string
	// Title is its title.
	Title string
	// Goal is the unfinished goal, when the tree recorded one.
	Goal string
	// OpenTasks are the steps that were not done.
	OpenTasks []string
	// UpdatedAt is when the session last changed, in unix seconds.
	UpdatedAt int64
}

// PickResume chooses the session worth resuming from the candidates: the newest
// one with unfinished work. It returns false when nothing is worth resuming, so a
// quiet startup stays quiet instead of prompting about work the user abandoned.
func PickResume(candidates []ResumeCandidate, now int64, maxAgeSeconds int64) (ResumeCandidate, bool) {
	var best ResumeCandidate
	found := false
	for _, candidate := range candidates {
		if len(candidate.OpenTasks) == 0 || candidate.SessionID == "" {
			continue
		}
		if maxAgeSeconds > 0 && now-candidate.UpdatedAt > maxAgeSeconds {
			continue
		}
		if found && candidate.UpdatedAt <= best.UpdatedAt {
			continue
		}
		best = candidate
		found = true
	}
	return best, found
}

// ResumePrompt renders the message that reopens a session. It restates the
// unfinished steps so the resumed turn starts aimed rather than re-deriving the
// goal from an empty context.
func ResumePrompt(candidate ResumeCandidate) string {
	var sb strings.Builder
	sb.WriteString("Resuming unfinished work")
	if candidate.Title != "" {
		sb.WriteString(" from ")
		sb.WriteString(fmt.Sprintf("%q", candidate.Title))
	}
	sb.WriteString(".\n")
	if candidate.Goal != "" {
		sb.WriteString("Goal: ")
		sb.WriteString(candidate.Goal)
		sb.WriteString("\n")
	}
	sb.WriteString("Still open:\n")
	for _, task := range candidate.OpenTasks {
		sb.WriteString("- ")
		sb.WriteString(task)
		sb.WriteString("\n")
	}
	sb.WriteString("Pick up the next open step. Do not repeat work that is already complete.")
	return sb.String()
}
