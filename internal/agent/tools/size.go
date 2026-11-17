package tools

import (
	"encoding/json"

	"charm.land/fantasy"
)

// DefinitionBytes returns the prompt cost of a tool set: every tool's name,
// description and marshalled parameter schema. It is what the model receives
// for the tools on each request, so callers that budget a prompt have to count
// it or they size a request that is larger than they think.
func DefinitionBytes(tools []fantasy.AgentTool) int {
	total := 0
	for _, tool := range tools {
		info := tool.Info()
		schema, err := json.Marshal(info.Parameters)
		if err != nil {
			schema = nil
		}
		total += len(info.Name) + len(info.Description) + len(schema)
	}
	return total
}
