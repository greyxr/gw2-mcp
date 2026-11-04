package server

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

func TestWorkflowPromptsListAndRender(t *testing.T) {
	ctx, cs := connectTest(t, New(gw2.NewClient("")))
	count := 0
	argumentPrompt := false
	for prompt := range cs.Prompts(ctx, nil) {
		count++
		check(t, strings.TrimSpace(prompt.Description) != "", "prompt %q has no description", prompt.Name)
		args := map[string]string{}
		for _, argument := range prompt.Arguments {
			args[argument.Name] = "TEST_GOAL"
		}
		result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: prompt.Name, Arguments: args})
		check(t, err == nil, "get prompt %q: %v", prompt.Name, err)
		check(t, len(result.Messages) == 1, "prompt %q returned %d messages", prompt.Name, len(result.Messages))
		content, ok := result.Messages[0].Content.(*mcp.TextContent)
		check(t, ok, "prompt %q content is %T", prompt.Name, result.Messages[0].Content)
		if len(prompt.Arguments) > 0 {
			argumentPrompt = true
			check(t, strings.Contains(content.Text, "TEST_GOAL"), "prompt %q did not insert its argument", prompt.Name)
		}
	}
	check(t, count == 10 && argumentPrompt, "prompt listing did not include a renderable argument prompt")
}

func TestWorkflowPromptRequiresArgument(t *testing.T) {
	ctx, cs := connectTest(t, New(gw2.NewClient("")))
	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "scenario_workbench"})
	check(t, err != nil && strings.Contains(err.Error(), "required"), "missing required prompt argument error = %v", err)
}
