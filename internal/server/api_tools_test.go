package server

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type apiToolsFixture struct {
	reads []string
	auth  []bool
}

func (*apiToolsFixture) GetRaw(context.Context, string) ([]byte, error) {
	return []byte("/v2/account [a]\n/v2/characters/:id/crafting [a]\n/v2/colors [l]\n/v2/items [l]\n/v2/items [a]\n/v2/recipes/search\n/v2/disabled [d]\n/v2/createsubtoken [a]\n/v2/commerce/exchange\n/v2/commerce/transactions [a]\n"), nil
}

func (f *apiToolsFixture) ReadRoute(_ context.Context, path string, auth bool) (gw2.APIResponse, error) {
	f.reads = append(f.reads, path)
	f.auth = append(f.auth, auth)
	data := json.RawMessage(`{"ok":true}`)
	if strings.HasPrefix(path, "/v2/items") {
		data = json.RawMessage(`[{"id":7,"name":"<raw>"}]`)
	}
	return gw2.APIResponse{Data: data, Endpoint: strings.SplitN(path, "?", 2)[0], ObservedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), StatusCode: 206, Partial: true, Pagination: map[string]string{"X-Result-Total": "3"}}, nil
}

func TestAPIToolsDiscoverAndQuery(t *testing.T) {
	fixture := &apiToolsFixture{}
	s := mcp.NewServer(&mcp.Implementation{Name: "api-test", Version: "1"}, nil)
	registerAPITools(s, fixture)
	ctx, session := connectTest(t, s)

	listed := callTool(t, ctx, session, "list_api_endpoints", `{}`)
	check(t, !listed.IsError, "list endpoints: %+v", listed)
	catalog := toolData[struct {
		Endpoints []apiRoute `json:"endpoints"`
	}](t, listed)
	routes := map[string]apiRoute{}
	for _, route := range catalog.Endpoints {
		routes[route.Route] = route
	}
	check(t, routes["account"].RequiresAuth && routes["colors"].LocaleAware, "discovery flags missing: %+v", routes)
	check(t, routes["items"].LocaleAware && routes["items"].RequiresAuth, "duplicate discovery flags were not merged: %+v", routes["items"])
	check(t, routes["commerce/exchange/gems"].Route != "" && routes["commerce/transactions/history/sells"].RequiresAuth, "commerce child routes missing: %+v", routes)
	check(t, routes["createsubtoken"].Route == "", "restricted credential route was exposed")

	result := callTool(t, ctx, session, "query_api", `{"route":"/v2/items","ids":["7"],"page_size":25}`)
	check(t, !result.IsError, "query items: %+v", result)
	got := toolData[struct {
		Data       json.RawMessage   `json:"data"`
		Partial    bool              `json:"partial"`
		StatusCode int               `json:"status_code"`
		Pagination map[string]string `json:"pagination"`
		Request    string            `json:"request"`
	}](t, result)
	var rows []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	check(t, got.Partial && got.StatusCode == 206 && got.Pagination["X-Result-Total"] == "3", "partial response metadata missing: %+v", got)
	check(t, json.Unmarshal(got.Data, &rows) == nil && len(rows) == 1 && rows[0].Name == "<raw>", "JSON response changed: %s", got.Data)
	request, err := url.Parse(got.Request)
	check(t, err == nil && request.Path == "/v2/items" && request.Query().Get("ids") == "7" && request.Query().Get("page_size") == "25", "request metadata = %q", got.Request)

	result = callTool(t, ctx, session, "query_api", `{"route":"account"}`)
	check(t, !result.IsError && fixture.auth[len(fixture.auth)-1], "discovered account route was not authenticated")
	result = callTool(t, ctx, session, "query_api", `{"route":"recipes/search","filters":{"input":"7"}}`)
	request, err = url.Parse(fixture.reads[len(fixture.reads)-1])
	check(t, !result.IsError && err == nil && request.Query().Get("input") == "7", "supported recipe filter was rejected: %+v", result)
}

func TestAPIToolsRejectUnsafeQueriesBeforeReading(t *testing.T) {
	fixture := &apiToolsFixture{}
	s := mcp.NewServer(&mcp.Implementation{Name: "api-test", Version: "1"}, nil)
	registerAPITools(s, fixture)
	ctx, session := connectTest(t, s)
	for _, args := range []string{
		`{"route":"https://example.com/v2/items"}`,
		`{"route":"disabled"}`,
		`{"route":"items","ids":["all"]}`,
		`{"route":"items","page_size":201}`,
		`{"route":"colors","lang":"xx"}`,
		`{"route":"characters/:id/crafting","path_params":{"id":".."}}`,
		`{"route":"items","filters":{"quantity":"2"}}`,
		`{"route":"items","filters":{"ids":"all"}}`,
		`{"route":"items","path_params":{"id":"1"}}`,
		`{"route":"items","ids":["7"],"page":0}`,
	} {
		result := callTool(t, ctx, session, "query_api", args)
		check(t, result.IsError, "unsafe query accepted: %s", args)
	}
	check(t, len(fixture.reads) == 0, "invalid queries reached the API: %v", fixture.reads)
}
