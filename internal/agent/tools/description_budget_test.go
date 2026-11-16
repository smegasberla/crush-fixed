package tools

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

// These budgets are regression guards, not targets. Tool prose and parameter
// schemas are paid on every request, and both creep up one helpful sentence at
// a time. The numbers come from measuring the coder tool set after the trim
// pass that introduced them, plus about 5% headroom: a new field or a reworded
// paragraph should need a deliberate bump here.
const (
	// descriptionBudget covers the description bodies alone.
	descriptionBudget = 8200
	// definitionBudget covers names, description bodies and marshalled
	// parameter schemas together, which is what the model receives. Schemas
	// dominate it, so description-only trimming cannot hold the line here.
	definitionBudget = 18000
)

// coderTools builds the tool set a top-level coder agent gets, mirroring the
// registration in internal/agent/coordinator.go. Every dependency is nil
// because only Info() is read; nothing here runs a tool.
func coderTools() []fantasy.AgentTool {
	return []fantasy.AgentTool{
		NewBashTool(nil, "", "", 0, nil),
		NewCrushInfoTool(nil, nil, nil, nil, nil),
		NewCrushLogsTool(""),
		NewJobOutputTool(""),
		NewJobKillTool(),
		NewDownloadTool(nil, "", nil),
		NewEditTool(nil, nil, nil, nil, ""),
		NewMultiEditTool(nil, nil, nil, nil, ""),
		NewFetchTool(nil, "", nil),
		NewGlobTool("", config.ToolGlob{}),
		NewGrepTool("", config.ToolGrep{}),
		NewLsTool(nil, "", config.ToolLs{}),
		NewSourcegraphTool(nil),
		NewFetchSkillTool(nil, nil),
		NewTodosTool(nil),
		NewViewTool(nil, nil, nil, nil, ""),
		NewWriteTool(nil, nil, nil, nil, ""),
		NewQuestionTool(nil),
		NewDiagnosticsTool(nil),
		NewReferencesTool(nil),
		NewLSPRestartTool(nil),
		NewSymbolsTool(nil),
		NewDefinitionTool(nil),
		NewCallHierarchyTool(nil),
		NewRenameTool(nil, nil, nil, nil),
		NewReplaceSymbolTool(nil, nil, nil, nil),
	}
}

// toolSizes is the prompt cost of one tool: its name, its description, and the
// JSON of its parameter schema.
func toolSizes(t *testing.T, tool fantasy.AgentTool) (description, definition int) {
	t.Helper()
	info := tool.Info()
	schema, err := json.Marshal(info.Parameters)
	require.NoError(t, err)
	return len(info.Description), len(info.Name) + len(info.Description) + len(schema)
}

func TestToolDescriptionsStayWithinBudget(t *testing.T) {
	t.Parallel()

	var descriptions, definitions int
	for _, tool := range coderTools() {
		description, definition := toolSizes(t, tool)
		descriptions += description
		definitions += definition
		t.Logf("%-20s desc=%-5d total=%d", tool.Info().Name, description, definition)
	}
	t.Logf("%-20s desc=%-5d total=%d", "sum", descriptions, definitions)

	require.Lessf(t, descriptions, descriptionBudget,
		"tool descriptions cost %d bytes, over the %d budget; trim prose", descriptions, descriptionBudget)
	require.Lessf(t, definitions, definitionBudget,
		"tool definitions cost %d bytes, over the %d budget; trim prose or schema", definitions, definitionBudget)
}

func TestToolDescriptionsNeverRestateTheSchema(t *testing.T) {
	t.Parallel()

	for _, tool := range coderTools() {
		info := tool.Info()
		require.NotEmpty(t, info.Description, "%s has no description", info.Name)
		require.Lessf(t, len(info.Description), 2000,
			"%s description is %d bytes; one tool should not dominate the budget", info.Name, len(info.Description))
	}
}
