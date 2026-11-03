package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type surfaceFixture struct{}

func (surfaceFixture) Overview(context.Context) (gw2.Overview, error) {
	return gw2.Overview{ID: "account", Name: "Test"}, nil
}
func (surfaceFixture) Characters(context.Context) ([]string, error) { return []string{"Hero"}, nil }
func (surfaceFixture) Currencies(context.Context, []int) ([]gw2.Currency, error) {
	return []gw2.Currency{{ID: 1, Name: "Coin"}}, nil
}
func (surfaceFixture) Items(_ context.Context, ids []int) ([]gw2.Item, error) {
	items := make([]gw2.Item, len(ids))
	for i, id := range ids {
		items[i] = gw2.Item{ID: id, Name: "Test item"}
	}
	return items, nil
}
func (surfaceFixture) FindItem(_ context.Context, id int) (gw2.FindResult, error) {
	return gw2.FindResult{ItemID: id, Total: 2, Locations: []gw2.Location{{Location: "bank", Count: 2}}, Complete: true}, nil
}
func (surfaceFixture) FindItems(_ context.Context, ids []int) (gw2.FindItemsResult, error) {
	items := make([]gw2.ItemLocations, len(ids))
	for i, id := range ids {
		items[i] = gw2.ItemLocations{ItemID: id, ObservedTotal: 2}
	}
	return gw2.FindItemsResult{Items: items, Complete: true}, nil
}
func (surfaceFixture) CraftingForCharacter(context.Context, string) ([]gw2.CraftingDiscipline, error) {
	return []gw2.CraftingDiscipline{{Discipline: "Artificer", Rating: 400, Active: true}}, nil
}
func (surfaceFixture) SearchRecipes(context.Context, int, int) ([]int, error) { return []int{42}, nil }
func (surfaceFixture) Recipes(context.Context, []int) ([]gw2.Recipe, error) {
	return []gw2.Recipe{{ID: 42, OutputItemID: 7, OutputItemCount: 1}}, nil
}
func (surfaceFixture) AccountRecipes(context.Context) ([]int, error) { return []int{42}, nil }
func (surfaceFixture) Get(_ context.Context, path string, out any) error {
	data := `{"id":7,"buys":[{"quantity":3,"unit_price":100}],"sells":[{"quantity":3,"unit_price":120}]}`
	if len(path) >= len("/v2/commerce/prices") && path[:len("/v2/commerce/prices")] == "/v2/commerce/prices" {
		data = `[{"id":7,"buys":{"unit_price":100,"quantity":3},"sells":{"unit_price":120,"quantity":3}}]`
	}
	if err := json.Unmarshal([]byte(data), out); err != nil {
		return fmt.Errorf("fixture %s: %w", path, err)
	}
	return nil
}

func TestMCPToolSurfaceAndQuoteModes(t *testing.T) {
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

	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"account_overview": true, "list_characters": true, "get_items": true, "get_currencies": true,
		"find_item": true, "find_items": true, "get_character_crafting": true, "get_market_prices": true,
		"quote_trade": true, "quote_trades": true, "search_recipes": true, "get_recipes": true, "get_account_recipes": true,
		"list_api_endpoints": true, "query_api": true, "get_reference": true,
		"put_record": true, "get_record": true, "list_records": true, "delete_record": true,
	}
	got := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		got[tool.Name] = tool
	}
	for name := range want {
		if got[name] == nil {
			t.Errorf("missing tool %q", name)
		}
	}

	calls := []struct {
		name string
		args string
	}{
		{"account_overview", `{}`}, {"list_characters", `{}`}, {"get_items", `{"item_ids":[7]}`},
		{"get_currencies", `{"currency_ids":[1]}`}, {"find_item", `{"item_id":7}`},
		{"find_items", `{"item_ids":[7,8]}`}, {"get_character_crafting", `{"character_name":"Hero"}`},
		{"get_market_prices", `{"item_ids":[7]}`}, {"search_recipes", `{"output_item_id":7}`},
		{"get_recipes", `{"recipe_ids":[42]}`}, {"get_account_recipes", `{}`},
		{"quote_trades", `{"trades":[{"item_id":7,"quantity":2,"mode":"buy_order","unit_price":110}]}`},
	}
	for _, tc := range calls {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: json.RawMessage(tc.args)})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if res.IsError {
			t.Errorf("%s returned MCP error: %+v", tc.name, res)
		}
		if res.StructuredContent == nil {
			t.Errorf("%s had no structured output", tc.name)
		} else {
			var payload map[string]any
			encoded, _ := json.Marshal(res.StructuredContent)
			if err := json.Unmarshal(encoded, &payload); err != nil || payload["data"] == nil {
				t.Errorf("%s structured output missing data: %s", tc.name, encoded)
			}
		}
	}
	wantQuotes := map[string]map[string]int64{
		"buy_instant":  {"gross": 240, "covered": 2, "unfilled": 0, "net_estimate": 240, "upfront": 240},
		"buy_order":    {"gross": 220, "covered": 2, "unfilled": 0, "net_estimate": 220, "upfront": 220},
		"sell_instant": {"gross": 200, "covered": 2, "unfilled": 0, "listing_fee_estimate": 10, "exchange_fee_estimate": 20, "delivery_proceeds_estimate": 180, "net_estimate": 170, "upfront": 10},
		"sell_listing": {"gross": 220, "covered": 2, "unfilled": 0, "listing_fee_estimate": 11, "exchange_fee_estimate": 22, "delivery_proceeds_estimate": 198, "net_estimate": 187, "upfront": 11},
	}
	for mode, expected := range wantQuotes {
		args := fmt.Sprintf(`{"item_id":7,"quantity":2,"mode":%q`, mode)
		if mode == "buy_order" || mode == "sell_listing" {
			args += `,"unit_price":110`
		}
		args += `}`
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "quote_trade", Arguments: json.RawMessage(args)})
		if err != nil {
			t.Errorf("quote_trade %s: %v", mode, err)
			continue
		}
		if res.IsError || res.StructuredContent == nil {
			t.Errorf("quote_trade %s failed: %+v", mode, res)
			continue
		}
		var wrapped struct {
			Data map[string]any `json:"data"`
		}
		encoded, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(encoded, &wrapped); err != nil {
			t.Errorf("quote_trade %s output decode: %v", mode, err)
			continue
		}
		for key, value := range expected {
			got, ok := wrapped.Data[key].(float64)
			if !ok || int64(got) != value {
				t.Errorf("quote_trade %s %s = %v, want %d", mode, key, wrapped.Data[key], value)
			}
		}
		conditional, _ := wrapped.Data["conditional_on_fill"].(bool)
		if conditional != (mode == "buy_order" || mode == "sell_listing") {
			t.Errorf("quote_trade %s conditional_on_fill = %v", mode, conditional)
		}
	}
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "quote_trade", Arguments: json.RawMessage(`{"item_id":7,"quantity":1,"mode":"unknown"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("invalid quote mode did not return an MCP tool error")
	}
}

func TestRecordToolAnnotationsAndArbitraryJSON(t *testing.T) {
	t.Setenv("GW2_MCP_STATE_DIR", filepath.Join(t.TempDir(), "records"))
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
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "put_record" {
			schemaJSON, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(schemaJSON, &schema); err != nil {
				t.Fatal(err)
			}
			if string(schema.Properties["data"]) != `{}` {
				t.Fatalf("put_record data schema = %s, want an empty schema", schema.Properties["data"])
			}
		}
		if tool.Name == "get_record" || tool.Name == "list_records" || tool.Name == "get_reference" {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s is not marked read-only", tool.Name)
			}
		}
		if tool.Name == "put_record" || tool.Name == "delete_record" {
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
				t.Errorf("%s has incorrect read-only annotation", tool.Name)
			}
		}
	}
	call := func(name, args string) *mcp.CallToolResult {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			t.Fatalf("%s failed: %+v", name, result)
		}
		return result
	}
	call("put_record", `{"name":"daily","kind":"runs","data":{"durations":[12,15],"note":"test"}}`)
	result := call("get_record", `{"name":"daily"}`)
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil || got.Data.Data["note"] != "test" {
		t.Fatalf("record payload = %s, err = %v", encoded, err)
	}
	call("list_records", `{"kind":"runs"}`)
	call("delete_record", `{"name":"daily"}`)
}
