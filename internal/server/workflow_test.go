package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkflowPromptsListAndRender(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := New(surfaceFixture{})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	want := map[string][]string{
		"aurene_fang_or_weight":      {"Every route must end with one usable Aurene's Weight", "craft Fang, sell Fang, then craft Weight", "Do not invent or imply that Fang is an input to the Weight recipe", "craft and sell Fang, then buy a finished Weight", "recipe flags including automatic/learned status", "Check item binding and sale eligibility", "stage-aware material ledger", "Nontradeable shortages", "buy_instant", "buy_order", "sell_instant", "sell_listing", "four scenarios total", "maximum cash deficit", "net liquidation value", "Report quote observed_at as the host's observation time", "quote_trades", "combine all purchases or sales of a given item into one total quantity", "either use delivery proceeds minus the separately paid listing fee", "Do not declare a definitive cheapest route", "Never invent IDs"},
		"account_audit":              {"account question", "lower-bound totals", "equipped upgrades", "source coverage"},
		"unlock_collection_review":   {"achievement progress", "mounts", "masteries", "wardrobe/skin unlocks"},
		"trading_post_review":        {"current orders", "delivery box", "historical fill price", "Never create/cancel orders"},
		"crafting_or_profit_plan":    {"recipe outputs", "typed ingredients", "batch surplus", "opportunity value"},
		"earning_route_review":       {"Wizard's Vault", "fixed rewards", "random drops", "daily reset"},
		"activity_earnings_review":   {"caller-captured records", "historical completion", "do not infer a completion timestamp", "not proceeds from this activity", "current liquidation value", "five-second target"},
		"currency_conversion_review": {"currency IDs", "vendor exchange", "account-bound currencies", "arbitrary gold value"},
		"account_value_and_history":  {"rankings", "timestamped saved records", "historical data before records were captured"},
		"scenario_workbench":         {"smallest required facts", "bounded read plan", "supported partial answer"},
	}

	listed := map[string]*mcp.Prompt{}
	for prompt, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[prompt.Name] = prompt
	}
	for name := range want {
		if _, ok := listed[name]; !ok {
			t.Errorf("required prompt %q is missing from list", name)
		}
	}
	for name, prompt := range listed {
		if strings.TrimSpace(prompt.Description) == "" {
			t.Errorf("prompt %q has no description", name)
		}
		args := map[string]string{}
		for _, argument := range prompt.Arguments {
			args[argument.Name] = "TEST_GOAL"
		}
		result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			t.Errorf("get prompt %q: %v", name, err)
			continue
		}
		if len(result.Messages) != 1 {
			t.Errorf("prompt %q returned %d messages, want 1", name, len(result.Messages))
			continue
		}
		content, ok := result.Messages[0].Content.(*mcp.TextContent)
		if !ok {
			t.Errorf("prompt %q content is %T, want text", name, result.Messages[0].Content)
			continue
		}
		for _, phrase := range want[name] {
			if !strings.Contains(content.Text, phrase) {
				t.Errorf("prompt %q is missing guidance %q", name, phrase)
			}
		}
		if name != "aurene_fang_or_weight" && !strings.Contains(content.Text, "TEST_GOAL") {
			t.Errorf("prompt %q did not insert its required argument", name)
		}
		if strings.Contains(content.Text, "use Fang as an input") || strings.Contains(content.Text, "acquiring any Fang required") {
			t.Errorf("prompt %q contains the unsupported Fang-as-Weight-input route", name)
		}
	}
}

func TestWorkflowPromptRequiresArgument(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := New(surfaceFixture{})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "scenario_workbench"})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing required prompt argument error = %v", err)
	}
}
