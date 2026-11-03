package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type apiToolsFixture struct {
	surfaceFixture
	paths     []string
	discovery []byte
}

func (f *apiToolsFixture) GetRaw(_ context.Context, path string) ([]byte, error) {
	f.paths = append(f.paths, path)
	if f.discovery != nil {
		return f.discovery, nil
	}
	return []byte("** Public API v2 **\n\nThe following paths are exposed by this API:\n  /v2/account [a]\n  /v2/characters/:id/crafting [a]\n  /v2/items [l]\n  /v2/tokeninfo [a]\n  /v2/createsubtoken [a]\n  /v2/commerce/exchange\n  /v2/commerce/transactions [a]\n  /v2/homestead\n  /v2/homestead\n"), nil
}

func (f *apiToolsFixture) Get(_ context.Context, path string, out any) error {
	f.paths = append(f.paths, path)
	var data string
	switch {
	case strings.HasPrefix(path, "/v2/items"):
		data = `[{"id":7,"name":"<raw>"}]`
	default:
		data = `{"ok":true}`
	}
	return json.Unmarshal([]byte(data), out)
}

func (f *apiToolsFixture) Read(_ context.Context, path string) (gw2.APIResponse, error) {
	f.paths = append(f.paths, path)
	data := json.RawMessage(`{"ok":true}`)
	if strings.HasPrefix(path, "/v2/items") {
		data = json.RawMessage(`[{"id":7,"name":"<raw>"}]`)
	}
	return gw2.APIResponse{
		Data: data, Endpoint: strings.SplitN(path, "?", 2)[0], ObservedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		StatusCode: 206, Partial: true, Pagination: map[string]string{"X-Result-Total": "3"},
	}, nil
}

func TestDiscoverRoutesAndQueryPreservesJSON(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := &apiToolsFixture{}
	server := New(fixture)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	listed, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "list_api_endpoints", Arguments: json.RawMessage(`{}`)})
	if err != nil || listed.IsError {
		t.Fatalf("list_api_endpoints: result=%+v err=%v", listed, err)
	}
	var listing struct {
		Data struct {
			Endpoints []apiRoute `json:"endpoints"`
		} `json:"data"`
	}
	encoded, _ := json.Marshal(listed.StructuredContent)
	if err := json.Unmarshal(encoded, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Data.Endpoints) < 10 || listing.Data.Endpoints[0].Route != "account" {
		t.Fatalf("discovery listing = %s", encoded)
	}
	seenRoutes := make(map[string]bool, len(listing.Data.Endpoints))
	for _, route := range listing.Data.Endpoints {
		if seenRoutes[route.Route] {
			t.Fatalf("duplicate route %q in discovery", route.Route)
		}
		seenRoutes[route.Route] = true
		if route.Route == "createsubtoken" {
			t.Fatal("credential-creating route must not be exposed")
		}
	}
	if !seenRoutes["commerce/exchange/gems"] || !seenRoutes["commerce/transactions/history/sells"] {
		t.Fatalf("expected nested routes were not expanded: %s", encoded)
	}
	for _, route := range listing.Data.Endpoints {
		if route.Route == "account" && !route.RequiresAuth {
			t.Fatal("combined discovery auth flags were not parsed")
		}
	}

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query_api",
		Arguments: json.RawMessage(`{"route":"/v2/items","ids":["7"],"page_size":25}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("query_api: result=%+v err=%v", result, err)
	}
	var payload map[string]json.RawMessage
	encoded, _ = json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Partial    bool              `json:"partial"`
		StatusCode int               `json:"status_code"`
		Pagination map[string]string `json:"pagination"`
	}
	if err := json.Unmarshal(payload["data"], &metadata); err != nil {
		t.Fatal(err)
	}
	if !metadata.Partial || metadata.StatusCode != 206 || metadata.Pagination["X-Result-Total"] != "3" {
		t.Fatalf("response metadata missing: %+v", metadata)
	}
	var requestMetadata struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal(payload["data"], &requestMetadata); err != nil || requestMetadata.Request != "/v2/items?ids=7&page_size=25" {
		t.Fatalf("request metadata missing: %+v err=%v", requestMetadata, err)
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		Data     []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload["data"], &body); err != nil {
		t.Fatal(err)
	}
	if body.Endpoint != "/v2/items" || len(body.Data) != 1 || body.Data[0].Name != "<raw>" {
		t.Fatalf("API response changed: %+v", body)
	}
	if got := fixture.paths[len(fixture.paths)-1]; got != "/v2/items?ids=7&page_size=25" {
		t.Fatalf("query path = %q", got)
	}

	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query_api",
		Arguments: json.RawMessage(`{"route":"characters/:id/crafting","path_params":{"id":"My Hero"}}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("query_api with a path parameter: result=%+v err=%v", result, err)
	}
	if got := fixture.paths[len(fixture.paths)-1]; got != "/v2/characters/My%20Hero/crafting" {
		t.Fatalf("path parameter was not escaped: %q", got)
	}
}

func TestParsePlaintextDiscoveryFlagsAndTemplates(t *testing.T) {
	entry, ok := parseDiscoveryLine("  /v2/characters/:id/crafting [a]")
	if !ok || entry.Route != "characters/:id/crafting" || !entry.RequiresAuth {
		t.Fatalf("parsed route = %+v, %v", entry, ok)
	}
	entry, ok = parseDiscoveryLine("/v2/colors [l]")
	if !ok || !entry.LocaleAware {
		t.Fatalf("parsed locale route = %+v, %v", entry, ok)
	}
	entry, ok = parseDiscoveryLine("/v2/characters/:id/dungeons [d,a]")
	if !ok || !entry.RequiresAuth || !entry.CurrentlyDisabled {
		t.Fatalf("combined flags were not parsed: %+v, %v", entry, ok)
	}
	if _, ok := parseDiscoveryLine("/v2/createsubtoken [a]"); !ok {
		t.Fatal("discovery parser should parse route; filtering is handled by catalog builder")
	}
}

func TestDiscoveryDoesNotInventRoutesWithoutTheirParent(t *testing.T) {
	api := &apiToolsFixture{discovery: []byte("/v2/items [l]\n")}
	routes, err := discoverRoutes(context.Background(), api)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Route != "items" {
		t.Fatalf("unexpected supplemental routes: %+v", routes)
	}
}

func TestDiscoveryRejectsSpeculativeJSONMapCatalog(t *testing.T) {
	api := &apiToolsFixture{discovery: []byte(`{"account":["/v2/account"]}`)}
	if _, err := discoverRoutes(context.Background(), api); err == nil || !strings.Contains(err.Error(), "plaintext route listing") {
		t.Fatalf("JSON map discovery should fail closed, got %v", err)
	}
}

func TestAPIQueryValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route string
		in    apiQueryInput
	}{
		{name: "ids all", in: apiQueryInput{IDs: []string{"all"}}},
		{name: "id list too large", in: apiQueryInput{IDs: makeIDs(201)}},
		{name: "bad page size", in: apiQueryInput{PageSize: intPointer(201)}},
		{name: "bad lang", in: apiQueryInput{Lang: "xx"}},
		{name: "unsupported filter", in: apiQueryInput{Filters: map[string]string{"quantity": "2"}}},
		{name: "invalid exchange quantity", in: apiQueryInput{Filters: map[string]string{"quantity": "0"}}},
		{name: "nonpositive world", route: "wvw/matches/overview", in: apiQueryInput{Filters: map[string]string{"world": "0"}}},
		{name: "nonpositive recipe item", route: "recipes/search", in: apiQueryInput{Filters: map[string]string{"output": "0"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route := apiRoute{Route: tc.route, LocaleAware: true}
			if route.Route == "" {
				route.Route = "items"
			}
			if tc.name == "invalid exchange quantity" {
				route.Route = "commerce/exchange/gems"
			}
			if _, err := validateAPIQuery(route, tc.in); err == nil {
				t.Fatal("expected invalid query to be rejected")
			}
		})
	}
	if _, err := substituteRoute("characters/:id/crafting", map[string]string{"id": ".."}); err == nil {
		t.Fatal("dot segment path parameter accepted")
	}
	if _, err := substituteRoute("characters/:id/crafting", map[string]string{"id": strings.Repeat("x", 129)}); err == nil {
		t.Fatal("oversized path parameter accepted")
	}
	got, err := substituteRoute("a/:id/:name", map[string]string{"id": ":name", "name": "zz"})
	if err != nil || got != "a/:name/zz" {
		t.Fatalf("parameter substitution changed a value that looks like another token: %q err=%v", got, err)
	}
	if _, err := validateAPIQuery(apiRoute{Route: "commerce/exchange/gems"}, apiQueryInput{Filters: map[string]string{"quantity": "100"}}); err != nil {
		t.Fatalf("valid exchange quantity rejected: %v", err)
	}
	if _, err := validateAPIQuery(apiRoute{Route: "guild/:id/log"}, apiQueryInput{Filters: map[string]string{"since": "0"}}); err != nil {
		t.Fatalf("valid guild log since filter rejected: %v", err)
	}
	if _, err := validateAPIQuery(apiRoute{Route: "guild/:id/log"}, apiQueryInput{Filters: map[string]string{"since": "-1"}}); err == nil {
		t.Fatal("negative guild log since filter accepted")
	}
}

func makeIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = string(rune('a'+i%26)) + string(rune('A'+i/26))
	}
	return ids
}

func intPointer(value int) *int { return &value }
