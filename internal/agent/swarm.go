package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

//go:embed templates/swarm_tool.md
var swarmToolDescription string

const (
	SwarmToolName = "swarm"

	// swarmMaxItems bounds the fan-out. A swarm is a cost multiplier, so the
	// cap is deliberately low: a few targeted workers beat a large crowd of
	// agents all re-reading the same files.
	swarmMaxItems = 8
	// swarmFindingChars bounds one worker's answer before it enters the
	// parent's context. Findings are pointers, not transcripts.
	swarmFindingChars = 1200
	// swarmWorkerTimeout bounds one worker's whole run. Without it a worker
	// that keeps exploring spends the budget a swarm is meant to save.
	swarmWorkerTimeout = 90 * time.Second
)

// SwarmParams is one fan-out request. Items are plain strings, not objects:
// a nested schema costs tokens on every parent request and gives the model
// more ways to get the call wrong.
type SwarmParams struct {
	Question string   `json:"question" description:"The shared question every worker answers"`
	Items    []string `json:"items" description:"One short, standalone question per worker; 2 to 8 items"`
}

// SwarmResult is one worker's finding. It is kept small so a swarm's output
// is a set of pointers the parent can act on, not another transcript.
type SwarmResult struct {
	Item   string `json:"item"`
	Result string `json:"result"`
}

// swarmWorkerPrompt builds the whole prompt a worker sees. It is intentionally
// tiny: no role instructions, no rules restated from the parent, no history.
// The worker's own system prompt already carries what it needs.
func swarmWorkerPrompt(question, item string) string {
	var b strings.Builder
	if strings.TrimSpace(question) != "" {
		fmt.Fprintf(&b, "Question: %s\n", strings.TrimSpace(question))
	}
	fmt.Fprintf(&b, "Check: %s\n\nAnswer with findings only. Give file paths and what you found. At most %d characters; no introduction or conclusion.",
		strings.TrimSpace(item), swarmFindingChars)
	return b.String()
}

// validateSwarmParams rejects calls that would fan out too widely or send a
// worker without enough to do. Returning a message rather than an error makes
// the correction visible to the model so it can retry.
func validateSwarmParams(params SwarmParams) string {
	if strings.TrimSpace(params.Question) == "" && len(params.Items) == 0 {
		return "question or items is required"
	}
	if len(params.Items) < 2 {
		return "swarm needs at least 2 items; use one agent for a single task"
	}
	if len(params.Items) > swarmMaxItems {
		return fmt.Sprintf("too many items (%d); the cap is %d", len(params.Items), swarmMaxItems)
	}
	for i, item := range params.Items {
		if strings.TrimSpace(item) == "" {
			return fmt.Sprintf("item %d is empty", i+1)
		}
	}
	return ""
}

// swarmTool builds the fan-out tool. It reuses the read-only task agent, so
// every fix in SessionAgent (local compaction, stable prefixes, bounded tool
// output) applies to workers too, and workers cannot edit or delegate.
func (c *coordinator) swarmTool(ctx context.Context, parentAgentID string) (fantasy.AgentTool, error) {
	// Swarm workers run on the small model. Anthropic's own guidance is to
	// route narrow lookups to the cheaper model; building one shared worker
	// means one prompt, one tool set and one model per swarm, not per item.
	agentCfg, canWrite := c.taskAgentConfig(accessRead)
	_ = canWrite
	var ready errgroup.Group
	worker, err := c.buildTaskAgent(ctx, &ready, agentCfg, false, func() ModelSet {
		return c.smallOnlyModels()
	})
	if err != nil {
		return nil, err
	}
	if err := ready.Wait(); err != nil {
		return nil, err
	}

	return fantasy.NewAgentTool(
		SwarmToolName,
		swarmToolDescription,
		func(ctx context.Context, params SwarmParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if msg := validateSwarmParams(params); msg != "" {
				return fantasy.NewTextErrorResponse(msg), nil
			}
			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}
			messageID := tools.GetMessageFromContext(ctx)
			if messageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			results := make([]SwarmResult, len(params.Items))
			var wg sync.WaitGroup
			for i, item := range params.Items {
				wg.Add(1)
				go func(i int, item string) {
					defer wg.Done()
					workerCtx, cancel := context.WithTimeout(ctx, swarmWorkerTimeout)
					defer cancel()
					prompt := swarmWorkerPrompt(params.Question, item)
					resp, runErr := c.runSubAgent(workerCtx, subAgentParams{
						Agent:          worker,
						SessionID:      sessionID,
						AgentMessageID: messageID,
						ToolCallID:     fmt.Sprintf("%s-swarm-%d", call.ID, i),
						Prompt:         prompt,
						SessionTitle:   "Swarm Worker",
						SkipCostRollup: true,
					})
					finding := ""
					if runErr != nil {
						finding = fmt.Sprintf("worker failed: %v", runErr)
					} else if resp.Content != "" {
						finding = resp.Content
					} else {
						finding = "no findings"
					}
					results[i] = SwarmResult{Item: item, Result: clampSwarmFinding(finding)}
				}(i, item)
			}
			wg.Wait()

			// The report is ordered by the caller's items, never by finish
			// time: an unordered report makes the parent re-read it to work
			// out which finding belongs where.
			return fantasy.NewTextResponse(renderSwarmReport(results)), nil
		},
	), nil
}

// clampSwarmFinding keeps one worker's output within the parent's budget.
func clampSwarmFinding(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= swarmFindingChars {
		return text
	}
	return text[:swarmFindingChars] + "… [finding truncated]"
}

// renderSwarmReport emits a compact, ordered report.
func renderSwarmReport(results []SwarmResult) string {
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n%s\n", i+1, r.Item, r.Result)
	}
	return strings.TrimSpace(b.String())
}

// coderToolNames returns the names of the tools in a set, for tests that need
// to assert a swarm worker's reduced tool surface.
func coderToolNames(tools []fantasy.AgentTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		info := tool.Info()
		names = append(names, info.Name)
	}
	slices.Sort(names)
	return names
}
