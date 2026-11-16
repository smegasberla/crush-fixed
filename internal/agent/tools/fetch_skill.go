package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/skills"
)

//go:embed fetch_skill.md.tpl
var fetchSkillDescriptionTmpl []byte

// FetchSkillToolName is the tool a model calls to load a skill's instructions.
const FetchSkillToolName = "fetch_skill"

// FetchSkillParams is a skill load request.
type FetchSkillParams struct {
	// Name is the skill to load, as it appears in the skills index.
	Name string `json:"name" description:"The skill to load, named as in the skills index."`
	// Query lists the skills instead of loading one, for when the index did not
	// name the skill the task needs.
	Query bool `json:"list,omitempty" description:"List every skill with its description instead of loading one."`
}

// NewFetchSkillTool builds the lazy skill loader.
//
// The system prompt carries a one-line index of skills, not their bodies: a few
// hundred installed skills would otherwise cost tens of thousands of tokens on
// every request. This tool is how a skill's actual instructions are read, and
// reading marks the skill loaded so the turn knows what it has consulted.
func NewFetchSkillTool(allSkills []*skills.Skill, tracker *skills.Tracker) fantasy.AgentTool {
	description := strings.TrimSpace(string(fetchSkillDescriptionTmpl))
	return fantasy.NewAgentTool(
		FetchSkillToolName,
		description,
		func(ctx context.Context, params FetchSkillParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Query || strings.TrimSpace(params.Name) == "" {
				return fantasy.NewTextResponse(renderSkillList(allSkills, tracker)), nil
			}
			skill := findSkill(allSkills, params.Name)
			if skill == nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"no skill named %q. Available skills: %s",
					params.Name, strings.Join(skillNames(allSkills), ", "))), nil
			}
			if skill.DisableModelInvocation {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"skill %q cannot be loaded by the model; ask the user to invoke it", params.Name)), nil
			}
			if tracker != nil {
				tracker.MarkLoaded(skill.Name)
			}
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(skill.FormatInvocation()),
				ViewResponseMetadata{
					ResourceType:        ViewResourceSkill,
					ResourceName:        skill.Name,
					ResourceDescription: skill.Description,
				},
			), nil
		},
	)
}

// findSkill resolves a name case-insensitively, because the index and the model's
// spelling can differ in casing without being ambiguous.
func findSkill(allSkills []*skills.Skill, name string) *skills.Skill {
	needle := strings.TrimSpace(name)
	for _, skill := range allSkills {
		if skill.Name == needle {
			return skill
		}
	}
	for _, skill := range allSkills {
		if strings.EqualFold(skill.Name, needle) {
			return skill
		}
	}
	return nil
}

// skillNames returns every loadable skill name.
func skillNames(allSkills []*skills.Skill) []string {
	names := make([]string, 0, len(allSkills))
	for _, skill := range allSkills {
		if skill.DisableModelInvocation {
			continue
		}
		names = append(names, skill.Name)
	}
	return names
}

// renderSkillList lists every skill with its description, which is the escape
// hatch when the compact index was not enough.
func renderSkillList(allSkills []*skills.Skill, tracker *skills.Tracker) string {
	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, skill := range allSkills {
		if skill.DisableModelInvocation {
			continue
		}
		loaded := ""
		if tracker != nil && tracker.IsLoaded(skill.Name) {
			loaded = " (loaded)"
		}
		fmt.Fprintf(&sb, "- %s%s: %s\n", skill.Name, loaded, skill.Description)
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}
