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
		"/v2":                           "Public API\n /v2/items [l]\n /v2/account [a]",
		"/v2/account":                   `{"id":"account","name":"Test"}`,
		"/v2/tokeninfo":                 `{"permissions":["inventories"]}`,
		"/v2/account/wallet":            `[{"id":1,"value":99}]`,
		"/v2/characters":                `["Hero"]`,
		"/v2/account/bank":              `[{"id":7,"count":2}]`,
		"/v2/account/materials":         `[]`,
		"/v2/account/inventory":         `[]`,
		"/v2/characters/Hero/inventory": `{"bags":[]}`,
		"/v2/characters/Hero/crafting":  `{"crafting":[{"discipline":"Artificer","rating":400,"active":true}]}`,
		"/v2/items":                     `[{"id":7,"name":"Test item","flags":[]}]`,
		"/v2/currencies":                `[{"id":1,"name":"Coin"}]`,
		"/v2/recipes/search":            `[42]`,
		"/v2/recipes":                   `[{"id":42,"output_item_id":7,"output_item_count":1}]`,
		"/v2/account/recipes":           `[42]`,
		"/v2/commerce/prices":           `[{"id":7,"buys":{"unit_price":100,"quantity":3},"sells":{"unit_price":120,"quantity":3}}]`,
		"/v2/commerce/listings":         `[{"id":7,"buys":[{"quantity":3,"unit_price":100}],"sells":[{"quantity":3,"unit_price":120}]}]`,
		"/wiki/Test":                    "public guide",
	}
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
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
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
	check(t, err == nil && len(listed.Tools) == 20, "tool catalog: %v", err)
	for _, tool := range listed.Tools {
		check(t, tool.Annotations != nil && tool.Annotations.ReadOnlyHint == (tool.Name != "put_record" && tool.Name != "delete_record"), "incorrect access annotation: %s", tool.Name)
	}
	for _, call := range []struct{ name, args string }{
		{"account_overview", `{}`}, {"list_characters", `{}`},
		{"get_items", `{"item_ids":[7]}`}, {"get_currencies", `{"currency_ids":[1]}`},
		{"find_item", `{"item_id":7}`}, {"find_items", `{"item_ids":[7,8]}`},
		{"get_character_crafting", `{"character_name":"Hero"}`}, {"get_market_prices", `{"item_ids":[7]}`},
		{"search_recipes", `{"output_item_id":7}`}, {"get_recipes", `{"recipe_ids":[42]}`}, {"get_account_recipes", `{}`},
		{"list_api_endpoints", `{}`}, {"query_api", `{"route":"items","ids":["7"]}`},
		{"get_reference", `{"url":"https://wiki.guildwars2.com/wiki/Test"}`},
		{"quote_trades", `{"trades":[{"item_id":7,"quantity":2,"mode":"buy_order","unit_price":110}]}`},
	} {
		result := callTool(t, ctx, session, call.name, call.args)
		check(t, !result.IsError && toolData[any](t, result) != nil, "%s failed: %+v", call.name, result)
	}
	for _, want := range []struct {
		mode                                             string
		gross, listing, exchange, delivery, net, upfront int64
		conditional                                      bool
	}{
		{"buy_instant", 240, 0, 0, 0, 240, 240, false},
		{"buy_order", 220, 0, 0, 0, 220, 220, true},
		{"sell_instant", 200, 10, 20, 180, 170, 10, false},
		{"sell_listing", 220, 11, 22, 198, 187, 11, true},
	} {
		price := ""
		if want.conditional {
			price = `,"unit_price":110`
		}
		args := fmt.Sprintf(`{"item_id":7,"quantity":2,"mode":%q%s}`, want.mode, price)
		got := toolData[quote](t, callTool(t, ctx, session, "quote_trade", args))
		check(t, got.Gross == want.gross && got.Covered == 2 && got.Unfilled == 0 && got.Conditional == want.conditional, "%s fill: %+v", want.mode, got)
		check(t, got.ListingFeeEstimate == want.listing && got.ExchangeFeeEstimate == want.exchange && got.DeliveryProceedsEstimate == want.delivery && got.NetEstimate == want.net && got.Upfront == want.upfront, "%s settlement: %+v", want.mode, got)
	}
	bad := callTool(t, ctx, session, "quote_trade", `{"item_id":7,"quantity":1,"mode":"unknown"}`)
	check(t, bad.IsError, "invalid quote accepted")
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
