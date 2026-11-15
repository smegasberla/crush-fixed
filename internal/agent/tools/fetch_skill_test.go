package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/stretchr/testify/require"
)

func skillFixture(name, description, instructions string, builtin bool) *skills.Skill {
	return &skills.Skill{
		Name:          name,
		Description:   description,
		Instructions:  instructions,
		SkillFilePath: skills.BuiltinPrefix + name + "/SKILL.md",
		Builtin:       builtin,
	}
}

func runFetchSkill(t *testing.T, tool fantasy.AgentTool, params FetchSkillParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c1", Name: FetchSkillToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func TestFetchSkillLoadsInstructions(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{
		skillFixture("build", "Use this when building", "## Steps\nrun make", true),
	}
	tracker := skills.NewTracker(allSkills)
	tool := NewFetchSkillTool(allSkills, tracker)

	resp := runFetchSkill(t, tool, FetchSkillParams{Name: "build"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "<loaded_skill>")
	require.Contains(t, resp.Content, "run make")
	require.Contains(t, resp.Content, "<name>build</name>")
	require.True(t, tracker.IsLoaded("build"))
}

func TestFetchSkillCaseInsensitive(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{skillFixture("Build", "d", "i", true)}
	tool := NewFetchSkillTool(allSkills, skills.NewTracker(allSkills))
	resp := runFetchSkill(t, tool, FetchSkillParams{Name: "build"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Build")
}

func TestFetchSkillUnknownName(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{skillFixture("build", "d", "i", true)}
	tool := NewFetchSkillTool(allSkills, skills.NewTracker(allSkills))
	resp := runFetchSkill(t, tool, FetchSkillParams{Name: "nope"})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no skill named")
	require.Contains(t, resp.Content, "build", "the error names what is available")
}

func TestFetchSkillList(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{
		skillFixture("build", "building things", "i1", true),
		skillFixture("test", "testing things", "i2", false),
	}
	tracker := skills.NewTracker(allSkills)
	tracker.MarkLoaded("build")
	tool := NewFetchSkillTool(allSkills, tracker)

	resp := runFetchSkill(t, tool, FetchSkillParams{Query: true})
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "build (loaded): building things")
	require.Contains(t, resp.Content, "test: testing things")
}

func TestFetchSkillEmptyNameLists(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{skillFixture("build", "d", "i", true)}
	tool := NewFetchSkillTool(allSkills, skills.NewTracker(allSkills))
	resp := runFetchSkill(t, tool, FetchSkillParams{})
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "build")
}

func TestFetchSkillRefusesModelDisabled(t *testing.T) {
	t.Parallel()

	hidden := skillFixture("secret", "d", "i", true)
	hidden.DisableModelInvocation = true
	allSkills := []*skills.Skill{hidden}
	tool := NewFetchSkillTool(allSkills, skills.NewTracker(allSkills))

	resp := runFetchSkill(t, tool, FetchSkillParams{Name: "secret"})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "cannot be loaded by the model")
}

func TestFetchSkillHiddenNotListed(t *testing.T) {
	t.Parallel()

	hidden := skillFixture("secret", "d", "i", true)
	hidden.DisableModelInvocation = true
	allSkills := []*skills.Skill{hidden, skillFixture("open", "d", "i", true)}
	tool := NewFetchSkillTool(allSkills, skills.NewTracker(allSkills))

	resp := runFetchSkill(t, tool, FetchSkillParams{Query: true})
	require.NotContains(t, resp.Content, "secret")
	require.Contains(t, resp.Content, "open")
}

func TestFetchSkillNilTracker(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{skillFixture("build", "d", "i", true)}
	tool := NewFetchSkillTool(allSkills, nil)
	resp := runFetchSkill(t, tool, FetchSkillParams{Name: "build"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "i")
}

func TestFindSkill(t *testing.T) {
	t.Parallel()

	allSkills := []*skills.Skill{
		skillFixture("Build", "d", "i", true),
		skillFixture("test", "d", "i", false),
	}
	require.NotNil(t, findSkill(allSkills, "test"))
	require.NotNil(t, findSkill(allSkills, "BUILD"), "casing is resolved")
	require.Nil(t, findSkill(allSkills, "missing"))
	require.Len(t, skillNames(allSkills), 2)
}
