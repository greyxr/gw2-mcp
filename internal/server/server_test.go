package server

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gw2mcp/internal/gw2"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func apiFixture(t *testing.T) *gw2.Client {
	t.Helper()
	bodies := map[string]string{
		"/v2":                           "Public API\n /v2/items [l]\n /v2/account [a]\n /v2/events [l,d]",
		"/v2/account":                   `{"id":"account","name":"Test","world":1001}`,
		"/v2/tokeninfo":                 `{"permissions":["inventories"]}`,
		"/v2/account/wallet":            `[{"id":1,"value":99}]`,
		"/v2/characters":                `["Hero"]`,
		"/v2/account/bank":              `[null,{"id":7,"count":2}]`,
		"/v2/account/materials":         `[]`,
		"/v2/account/inventory":         `[]`,
		"/v2/characters/Hero/inventory": `{"bags":[null,{"inventory":[null,{"id":7,"count":1}]}]}`,
		"/v2/characters/Hero/crafting":  `{"crafting":[{"discipline":"Artificer","rating":400,"active":true}]}`,
		"/v2/items":                     `[{"id":7,"name":"Test & item","flags":[]}]`,
		"/v2/currencies":                `[{"id":1,"name":"Coin","icon":"x"},{"id":2,"name":"Karma"}]`,
		"/v2/recipes/search":            `[42]`,
		"/v2/recipes":                   `[{"id":42,"output_item_id":7,"output_item_count":1,"ingredients":[{"type":"Item","id":7,"count":2},{"type":"Currency","id":1,"count":5}],"chat_link":"x"}]`,
		"/v2/account/recipes":           `[42]`,
		"/v2/commerce/prices":           `[{"id":7,"buys":{"unit_price":100,"quantity":3},"sells":{"unit_price":120,"quantity":3}}]`,
		"/v2/commerce/listings":         `[{"id":7,"buys":[{"quantity":3,"unit_price":100}],"sells":[{"quantity":3,"unit_price":120}]}]`,
	}
	partial := map[string]bool{"/v2/items": true, "/v2/recipes": true, "/v2/commerce/prices": true, "/v2/commerce/listings": true}
	var authErrors atomic.Int64
	original := http.DefaultTransport
	http.DefaultTransport = testTransport(func(r *http.Request) (*http.Response, error) {
		private := r.URL.Path == "/v2/tokeninfo" || strings.HasPrefix(r.URL.Path, "/v2/account") || strings.HasPrefix(r.URL.Path, "/v2/characters")
		if (r.Header.Get("Authorization") == "Bearer fixture-key") != private {
			authErrors.Add(1)
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			return nil, fmt.Errorf("unexpected fixture route %s", r.URL.Path)
		}
		status := 200
		if partial[r.URL.Path] && strings.Contains(r.URL.Query().Get("ids"), ",") {
			status = 206
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = original
		check(t, authErrors.Load() == 0, "account authentication escaped its scope")
	})
	return gw2.NewClient("fixture-key")
}

func TestMCPTools(t *testing.T) {
	ctx, session := connectTest(t, New(apiFixture(t)))
	listed, err := session.ListTools(ctx, nil)
	check(t, err == nil && len(listed.Tools) == 19, "tool catalog: %v", err)
	for _, tool := range listed.Tools {
		check(t, tool.Annotations != nil && tool.Annotations.ReadOnlyHint == (tool.Name != "put_record" && tool.Name != "delete_record"), "incorrect access annotation: %s", tool.Name)
		check(t, tool.OutputSchema == nil, "%s advertises an output schema", tool.Name)
	}
	for _, call := range []struct{ name, args, want string }{
		{"account_overview", `{}`, `{"account":{"id":"account","name":"Test","world":1001},"permissions":["inventories"],"wallet":[{"id":1,"name":"Coin","value":99}]}`},
		{"list_characters", `{}`, `["Hero"]`},
		{"get_items", `{"item_ids":[7,8,8,7]}`, `{"items":[{"id":7,"name":"Test & item","type":"","rarity":""}],"missing":[8]}`},
		{"get_currencies", `{"currency_ids":[2]}`, `[{"id":2,"name":"Karma"}]`},
		{"get_currencies", `{}`, `[{"id":1,"name":"Coin"},{"id":2,"name":"Karma"}]`},
		{"find_items", `{"item_ids":[8,7]}`, `{"items":[{"item_id":7,"total":3,"locations":[{"location":"bank","count":2},{"location":"character:Hero","count":1}]},{"item_id":8,"total":0}],"complete":true,"sources_checked":4}`},
		{"get_character_crafting", `{"character_name":"Hero"}`, `[{"discipline":"Artificer","rating":400,"active":true}]`},
		{"get_market_prices", `{"item_ids":[7,8]}`, `{"no_listings":[8],"prices":[{"id":7,"buy":100,"buy_qty":3,"sell":120,"sell_qty":3}]}`},
		{"search_recipes", `{"output_item_id":7}`, `{"count":1,"recipes":[{"id":42,"type":"","output_item_id":7,"output_item_name":"Test & item","output_item_count":1,"min_rating":0,"disciplines":null,"ingredients":[{"type":"Item","id":7,"name":"Test & item","count":2},{"type":"Currency","id":1,"count":5}]}]}`},
		{"get_account_recipes", `{"recipe_ids":[42,43]}`, `{"not_unlocked":[43],"unlocked":[42]}`},
		{"list_api_endpoints", `{}`, "account [a]\nitems [l]"},
		{"list_api_endpoints", `{"prefix":"/v2/acc"}`, "account [a]"},
		{"quote_trades", `{"trades":[{"item_id":7,"quantity":2,"mode":"buy_instant"},{"item_id":7,"quantity":4,"mode":"sell_instant"},{"item_id":7,"quantity":2,"mode":"buy_order","unit_price":110},{"item_id":7,"quantity":2,"mode":"sell_listing","unit_price":110},{"item_id":8,"quantity":1,"mode":"buy_instant"}]}`, `"quotes":[` +
			`{"item_id":7,"quantity":2,"mode":"buy_instant","gross":240,"filled":2,"net":240},` +
			`{"item_id":7,"quantity":4,"mode":"sell_instant","gross":300,"filled":3,"unfilled":1,"listing_fee":15,"exchange_fee":30,"net":255},` +
			`{"item_id":7,"quantity":2,"mode":"buy_order","gross":220,"filled":2,"net":220},` +
			`{"item_id":7,"quantity":2,"mode":"sell_listing","gross":220,"filled":2,"listing_fee":11,"exchange_fee":22,"net":187},` +
			`{"item_id":8,"quantity":1,"mode":"buy_instant","error":"item has no Trading Post listings"}]`},
	} {
		result := callTool(t, ctx, session, call.name, call.args)
		check(t, !result.IsError, "%s failed: %+v", call.name, result.Content[0])
		got := toolText(t, result)
		check(t, got == call.want || call.name == "quote_trades" && strings.HasSuffix(got, call.want+"}"), "%s %s\n got %s\nwant %s", call.name, call.args, got, call.want)
	}
	got := toolData[struct{ Recipes []recipe }](t, callTool(t, ctx, session, "get_recipes", `{"recipe_ids":[42]}`))
	check(t, len(got.Recipes) == 1 && got.Recipes[0].Ingredients[0].Name == "Test & item", "recipes: %+v", got)
	for _, call := range []struct{ name, args string }{
		{"quote_trades", `{"trades":[{"item_id":7,"quantity":1,"mode":"unknown"}]}`},
		{"get_items", `{"item_ids":[]}`}, {"get_items", `{"item_ids":[0]}`},
		{"search_recipes", `{}`}, {"list_api_endpoints", `{"prefix":"events"}`},
	} {
		check(t, callTool(t, ctx, session, call.name, call.args).IsError, "invalid %s accepted: %s", call.name, call.args)
	}
}

func TestRecordToolSerialization(t *testing.T) {
	t.Setenv("GW2_MCP_STATE_DIR", filepath.Join(t.TempDir(), "records"))
	ctx, session := connectTest(t, New(gw2.NewClient("")))
	for _, call := range []struct{ name, args string }{
		{"put_record", `{"name":"daily","kind":"runs","data":{"durations":[12,15],"note":"test"}}`},
		{"get_record", `{"name":"daily"}`}, {"list_records", `{"kind":"runs"}`}, {"delete_record", `{"name":"daily"}`},
	} {
		result := callTool(t, ctx, session, call.name, call.args)
		check(t, !result.IsError, "%s failed: %+v", call.name, result)
		if call.name == "get_record" {
			got := toolData[localRecord](t, result)
			check(t, strings.Contains(string(got.Data), `"note":"test"`), "arbitrary JSON lost: %s", got.Data)
		}
	}
}
