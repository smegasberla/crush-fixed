package goaltree

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddAndFind(t *testing.T) {
	t.Parallel()

	tree := New("ship the fix")
	parent := tree.Add(&Item{ID: "a", Task: "change the code"})
	require.True(t, tree.AddChild("a", &Item{ID: "a1", Task: "edit the retry loop"}))
	require.False(t, tree.AddChild("missing", &Item{ID: "x"}))

	require.Equal(t, parent, tree.Find("a"))
	require.NotNil(t, tree.Find("a1"))
	require.Nil(t, tree.Find("nope"))
	require.Nil(t, tree.Find(""))
	require.Equal(t, 2, tree.Total())
}

func TestCloseRequiresEvidence(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	tree.Add(&Item{ID: "a", Task: "do the thing"})

	err := tree.Close("a", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be completed without evidence")

	require.NoError(t, tree.Close("a", []string{"go test ./internal/agent/: ok"}))
	require.Equal(t, StatusDone, tree.Find("a").Status)
	require.Equal(t, []string{"go test ./internal/agent/: ok"}, tree.Find("a").Evidence)
}

func TestCloseRefusesWithOpenChildren(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	tree.Add(&Item{ID: "a", Task: "parent"})
	tree.AddChild("a", &Item{ID: "a1", Task: "child"})

	err := tree.Close("a", []string{"looks done"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "has an unfinished step")

	require.NoError(t, tree.Close("a1", []string{"proof"}))
	require.NoError(t, tree.Close("a", []string{"proof"}))
	require.True(t, tree.Finished())
}

func TestCloseUnknownItem(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	require.Error(t, tree.Close("missing", []string{"proof"}))
}

func TestOpenReturnsNextStep(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	tree.Add(&Item{ID: "a", Task: "first", Status: StatusDone})
	tree.Add(&Item{ID: "b", Task: "second"})
	tree.AddChild("b", &Item{ID: "b1", Task: "second-child"})

	next := tree.Open()
	require.NotNil(t, next)
	require.Equal(t, "b1", next.ID, "the deepest open step comes first")

	tree.Find("b1").Status = StatusDone
	require.Equal(t, "b", tree.Open().ID)
}

func TestOpenSkipsBlockedAndFailed(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	tree.Add(&Item{ID: "a", Task: "blocked one", Status: StatusBlocked})
	tree.Add(&Item{ID: "b", Task: "failed one", Status: StatusFailed})
	tree.Add(&Item{ID: "c", Task: "fine"})

	require.Equal(t, "c", tree.Open().ID)
}

func TestOpenCountAndFinished(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	require.True(t, tree.Finished() == false, "an empty tree is not finished work")

	tree.Add(&Item{ID: "a", Task: "one", Status: StatusDone})
	require.Equal(t, 0, tree.OpenCount())
	require.True(t, tree.Finished())

	tree.Add(&Item{ID: "b", Task: "two"})
	require.Equal(t, 1, tree.OpenCount())
	require.False(t, tree.Finished())
}

func TestUnverifiedPaths(t *testing.T) {
	t.Parallel()

	tree := New("goal")
	tree.Add(&Item{ID: "a", Task: "claimed done", Status: StatusDone})
	tree.Add(&Item{ID: "b", Task: "properly done", Status: StatusDone, Evidence: []string{"test passed"}})

	unverified := tree.UnverifiedPaths()
	require.Len(t, unverified, 1)
	require.Equal(t, "a", unverified[0].ID)
}

func TestRender(t *testing.T) {
	t.Parallel()

	tree := New("ship the fix")
	tree.Add(&Item{ID: "a", Task: "edit the code", Status: StatusDone, Evidence: []string{"tests pass"}})
	tree.Add(&Item{ID: "b", Task: "verify"})
	tree.AddChild("b", &Item{ID: "b1", Task: "run the build", Status: StatusActive})

	rendered := tree.Render()
	require.Contains(t, rendered, "GOAL: ship the fix")
	require.Contains(t, rendered, "[x] edit the code (evidence: tests pass)")
	require.Contains(t, rendered, "[ ] verify")
	require.Contains(t, rendered, "  [~] run the build")

	markers := map[Status]string{
		StatusDone:    "[x]",
		StatusActive:  "[~]",
		StatusBlocked: "[!]",
		StatusFailed:  "[✗]",
		StatusPending: "[ ]",
	}
	for status, want := range markers {
		require.Equal(t, want, marker(status))
	}
}

func TestRenderEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, New("goal").Render())
	require.Empty(t, (&Tree{}).Render())

	var nilTree *Tree
	require.Empty(t, nilTree.Render())
	require.Nil(t, nilTree.Open())
	require.Zero(t, nilTree.OpenCount())
	require.Zero(t, nilTree.Total())
	require.False(t, nilTree.Finished())
	require.Nil(t, nilTree.UnverifiedPaths())
	require.Nil(t, nilTree.Find("x"))
	require.Nil(t, nilTree.Add(&Item{ID: "a"}))
	require.False(t, nilTree.AddChild("a", &Item{}))
}

func TestPickResume(t *testing.T) {
	t.Parallel()

	candidates := []ResumeCandidate{
		{SessionID: "old", UpdatedAt: 100, OpenTasks: []string{"a"}},
		{SessionID: "new", UpdatedAt: 500, OpenTasks: []string{"b"}},
		{SessionID: "finished", UpdatedAt: 900},
	}
	picked, ok := PickResume(candidates, 1000, 3600)
	require.True(t, ok)
	require.Equal(t, "new", picked.SessionID)
}

func TestPickResumeSkipsStale(t *testing.T) {
	t.Parallel()

	candidates := []ResumeCandidate{{SessionID: "old", UpdatedAt: 100, OpenTasks: []string{"a"}}}
	_, ok := PickResume(candidates, 10000, 3600)
	require.False(t, ok, "work abandoned long ago is not worth prompting about")

	_, ok = PickResume(nil, 0, 0)
	require.False(t, ok)
	_, ok = PickResume([]ResumeCandidate{{SessionID: ""}}, 0, 0)
	require.False(t, ok)
}

func TestResumePrompt(t *testing.T) {
	t.Parallel()

	prompt := ResumePrompt(ResumeCandidate{
		Title:     "Fix the retry loop",
		Goal:      "the build must pass",
		OpenTasks: []string{"edit the loop", "run the tests"},
	})
	require.Contains(t, prompt, `"Fix the retry loop"`)
	require.Contains(t, prompt, "Goal: the build must pass")
	require.Contains(t, prompt, "- edit the loop")
	require.Contains(t, prompt, "- run the tests")
	require.Contains(t, prompt, "Do not repeat work that is already complete.")

	bare := ResumePrompt(ResumeCandidate{})
	require.True(t, strings.HasPrefix(bare, "Resuming unfinished work"))
}
