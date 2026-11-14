package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type apiToolsFixture struct {
	discoveries int
	reads       []string
	auth        []bool
	body        string
}

func (f *apiToolsFixture) GetRaw(context.Context, string) ([]byte, error) {
	f.discoveries++
	return []byte("/v2/account [a]\n/v2/characters/:id/crafting [a]\n/v2/colors [l]\n/v2/items [l]\n/v2/items [a]\n/v2/recipes/search\n/v2/disabled [d]\n/v2/createsubtoken [a]\n/v2/commerce/exchange\n/v2/commerce/transactions [a]\n/v2/emblem\n/v2/guild/:id/log [a]\n"), nil
}

func (f *apiToolsFixture) ReadRoute(_ context.Context, path string, auth bool) (gw2.APIResponse, error) {
	f.reads = append(f.reads, path)
	f.auth = append(f.auth, auth)
	body := f.body
	if body == "" {
		body = `{"ok":true}`
	}
	return gw2.APIResponse{Data: json.RawMessage(body), ObservedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), Partial: true, Pagination: &gw2.Pagination{ResultTotal: 3}}, nil
}

func apiToolsSession(t *testing.T, fixture *apiToolsFixture) (context.Context, *mcp.ClientSession) {
	s := mcp.NewServer(&mcp.Implementation{Name: "api-test", Version: "1"}, nil)
	registerAPITools(s, fixture)
	return connectTest(t, s)
}

func TestAPIToolsDiscoverAndQuery(t *testing.T) {
	fixture := &apiToolsFixture{}
	ctx, session := apiToolsSession(t, fixture)

	catalog := "\n" + toolText(t, callTool(t, ctx, session, "list_api_endpoints", `{}`)) + "\n"
	check(t, toolText(t, callTool(t, ctx, session, "list_api_endpoints", `{"prefix":"v2/col"}`)) == "colors [l]", "prefix normalization")
	for _, line := range []string{"account [a]", "colors [l]", "items [a] [l]", "commerce/exchange/gems", "commerce/transactions/history/sells [a]", "emblem/foregrounds"} {
		check(t, strings.Contains(catalog, "\n"+line+"\n"), "catalog lacks %q:%s", line, catalog)
	}
	check(t, !strings.Contains(catalog, "createsubtoken") && !strings.Contains(catalog, "disabled"), "restricted or disabled route listed:%s", catalog)

	fixture.body = `[{"id":7,"name":"<raw>","icon":"x"}]`
	result := callTool(t, ctx, session, "query_api", `{"route":"/v2/items","ids":["7"],"page_size":25,"fields":["id","name"]}`)
	got := toolData[apiQueryResult](t, result)
	check(t, strings.Contains(toolText(t, result), `"data":[{"id":7,"name":"<raw>"}]`) && got.Partial && got.Pagination == nil && got.ObservedAt == "2026-11-03T12:00:00Z", "query result: %s", toolText(t, result))
	request, err := url.Parse(got.Request)
	check(t, err == nil && request.Path == "/v2/items" && request.Query().Get("ids") == "7" && request.Query().Get("page_size") == "25", "request metadata = %q", got.Request)

	fixture.body = ""
	result = callTool(t, ctx, session, "query_api", `{"route":"account","fields":["ok"]}`)
	check(t, !result.IsError && fixture.auth[len(fixture.auth)-1] && strings.Contains(toolText(t, result), `"data":{"ok":true}`), "account query: %+v", result)
	for args, want := range map[string]string{
		`{"route":"recipes/search","filters":{"input":"7"}}`:                           "input=7",
		`{"route":"items","ids":["all"]}`:                                              "ids=all",
		`{"route":"guild/:id/log","path_params":{"id":"A B"},"filters":{"since":"5"}}`: "guild/A%20B/log?since=5",
	} {
		result = callTool(t, ctx, session, "query_api", args)
		check(t, !result.IsError && strings.Contains(fixture.reads[len(fixture.reads)-1], want), "query %s read %v", args, fixture.reads)
	}
	check(t, fixture.discoveries == 1, "discovery was fetched %d times", fixture.discoveries)
}

func TestAPIQueryBoundsLargeResponses(t *testing.T) {
	rows := make([]string, 3000)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":%d,"pad":"%s"}`, i, strings.Repeat("x", 20))
	}
	fixture := &apiToolsFixture{body: "[" + strings.Join(rows, ",\n ") + "]"}
	ctx, session := apiToolsSession(t, fixture)
	seen, offset := 0, 0
	for {
		result := callTool(t, ctx, session, "query_api", fmt.Sprintf(`{"route":"items","offset":%d}`, offset))
		check(t, len(toolText(t, result)) < maxDataChars+500, "page is %d characters", len(toolText(t, result)))
		page := toolData[struct {
			Data       []struct{ ID int }
			Total      int
			NextOffset int `json:"next_offset"`
		}](t, result)
		check(t, len(page.Data) > 0 && page.Data[0].ID == seen && page.Total == 3000, "page at %d: %d rows, total %d", offset, len(page.Data), page.Total)
		seen += len(page.Data)
		if offset = page.NextOffset; offset == 0 {
			break
		}
	}
	check(t, seen == 3000, "paged %d of 3000 rows", seen)
	trimmed := toolData[struct{ Data []map[string]any }](t, callTool(t, ctx, session, "query_api", `{"route":"items","fields":["id"]}`))
	check(t, len(trimmed.Data) > 2000 && len(trimmed.Data[0]) == 1, "field selection kept %d rows", len(trimmed.Data))

	fixture.body = `[{"big":"` + strings.Repeat("x", maxObjectChars) + `","small":1}]`
	check(t, callTool(t, ctx, session, "query_api", `{"route":"items"}`).IsError, "oversized element returned")
	check(t, !callTool(t, ctx, session, "query_api", `{"route":"items","fields":["small"]}`).IsError, "field selection did not shrink element")
	fixture.body = `{"big":"` + strings.Repeat("x", maxObjectChars) + `","small":1}`
	check(t, callTool(t, ctx, session, "query_api", `{"route":"account"}`).IsError, "oversized object returned")
	check(t, !callTool(t, ctx, session, "query_api", `{"route":"account","fields":["small"]}`).IsError, "field selection did not shrink object")
}

func TestAPIToolsRejectUnsafeQueriesBeforeReading(t *testing.T) {
	fixture := &apiToolsFixture{}
	ctx, session := apiToolsSession(t, fixture)
	for _, args := range []string{
		`{"route":"https://example.com/v2/items"}`,
		`{"route":"disabled"}`,
		`{"route":"createsubtoken"}`,
		`{"route":"items","ids":["1,2"]}`,
		`{"route":"items","page_size":201}`,
		`{"route":"items","offset":-1}`,
		`{"route":"colors","lang":"xx"}`,
		`{"route":"characters/:id/crafting","path_params":{"id":".."}}`,
		`{"route":"items","filters":{"quantity":"2"}}`,
		`{"route":"items","filters":{"ids":"all"}}`,
		`{"route":"commerce/transactions/history/buys","filters":{"since":"1"}}`,
		`{"route":"items","path_params":{"id":"1"}}`,
		`{"route":"items","ids":["7"],"page":0}`,
	} {
		result := callTool(t, ctx, session, "query_api", args)
		check(t, result.IsError, "unsafe query accepted: %s", args)
	}
	check(t, len(fixture.reads) == 0, "invalid queries reached the API: %v", fixture.reads)
}
