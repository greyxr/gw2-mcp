package gw2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func testClient(rt http.RoundTripper, key string) *Client {
	c := NewClient(key)
	c.httpClient = &http.Client{Transport: rt}
	return c
}

func TestAuthenticationAndPathBoundaries(t *testing.T) {
	var auth string
	calls := 0
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		auth = r.Header.Get("Authorization")
		return response(200, `[]`), nil
	})
	for _, path := range []string{"v2/account", "v2/characters/Hero/inventory", "v2/tokeninfo", "v2/commerce/delivery", "v2/commerce/transactions/history/buys", "v2/pvp/stats", "v2/pvp/games", "v2/pvp/standings", "v2/guild/id/stash"} {
		var out json.RawMessage
		check(t, testClient(rt, "key").Get(context.Background(), path, &out) == nil && auth == "Bearer key", "private authentication: %s", path)
		before := calls
		check(t, testClient(rt, "").Get(context.Background(), path, &out) != nil && calls == before, "missing key reached API: %s", path)
	}
	for _, path := range []string{"v2/items/1", "v2/guild/permissions", "v2/guild/upgrades", "v2/guild/search?name=example", "v2/commerce/exchange/gems?quantity=100"} {
		var out json.RawMessage
		check(t, testClient(rt, "key").Get(context.Background(), path, &out) == nil && auth == "", "public request leaked credentials: %s", path)
	}
	before := calls
	for _, path := range []string{"v2/items/../account", "v2/items/%2e%2e/account", "https://example.com", "v2/items?access_token=secret"} {
		var out json.RawMessage
		check(t, testClient(rt, "key").Get(context.Background(), path, &out) != nil && calls == before, "unsafe path: %s", path)
	}
	_, err := testClient(rt, "key").ReadRoute(context.Background(), "v2/future/private", true)
	check(t, err == nil && auth == "Bearer key", "new discovered authentication lost")
}

func TestResponseCoverageAndLimits(t *testing.T) {
	c := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := response(206, `[{"id":7,"unmodeled":9007199254740993}]`)
		r.Header.Set("X-Result-Total", "3")
		r.Header.Set("Link", "</v2/items?page=1>; rel=next")
		return r, nil
	}), "secret")
	r, err := c.ReadRoute(context.Background(), "/v2/commerce/prices?ids=7,8", false)
	check(t, err == nil && r.Partial && *r.Pagination == Pagination{ResultTotal: 3} && !r.ObservedAt.IsZero() && strings.Contains(string(r.Data), "9007199254740993"), "response coverage lost: %+v %v", r, err)
	var rows []struct{ ID int }
	check(t, c.GetByIDs(context.Background(), "/v2/commerce/prices", []int{7, 8}, &rows) == nil && len(rows) == 1, "typed batch rejected partial data")
	for _, body := range []string{"Public API\n /v2/items [l]", strings.Repeat("x", maxResponseBytes+1)} {
		c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		raw, err := c.GetRaw(context.Background(), "/v2")
		check(t, (err == nil) == (len(body) <= maxResponseBytes), "response size enforcement: %v", err)
		if err == nil {
			check(t, string(raw) == body, "plaintext discovery changed")
		}
	}
	c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(500, "secret"), nil })
	_, err = c.GetRaw(context.Background(), "/v2")
	check(t, err != nil && !strings.Contains(err.Error(), "secret"), "error exposed key: %v", err)
}

func TestInvalidIDsAndTransientRetry(t *testing.T) {
	calls := 0
	c := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(404, `{"text":"all ids provided are invalid"}`), nil
	}), "")
	c.retryDelay = 0
	rows := []struct{ ID int }{}
	check(t, c.GetByIDs(context.Background(), "/v2/items", []int{1}, &rows) == nil && len(rows) == 0 && calls == 1, "all-invalid batch was an error")
	items, err := c.Items(context.Background(), []int{1, 1})
	check(t, err == nil && len(items) == 0, "unknown items: %+v %v", items, err)
	err = c.Get(context.Background(), "/v2/items/1", &rows)
	check(t, err != nil && err.Error() == "GW2 API returned HTTP 404: all ids provided are invalid", "error text: %v", err)

	statuses := []int{503, 200, 429, 502, 400}
	c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		status := statuses[0]
		statuses = statuses[1:]
		return response(status, `[]`), nil
	})
	check(t, c.Get(context.Background(), "/v2/items", &rows) == nil, "transient failure was not retried")
	check(t, c.Get(context.Background(), "/v2/items", &rows) != nil && len(statuses) == 1, "retried more than once")
	check(t, c.Get(context.Background(), "/v2/items", &rows) != nil && len(statuses) == 0, "retried a client error")
}

func TestAccountAggregationCacheAndIncompleteInventory(t *testing.T) {
	bodies := map[string]string{
		"/v2/account": `{"id":"abc","name":"N"}`, "/v2/tokeninfo": `{"permissions":["inventories"]}`,
		"/v2/account/wallet": `[{"id":1,"value":99},{"id":9,"value":1}]`, "/v2/items": `[{"id":7,"name":"Seven"}]`,
		"/v2/currencies": `[{"id":1,"name":"Coin"}]`,
		"/v2/characters": `["Zed"]`, "/v2/account/bank": `[{"id":5,"count":2}]`,
		"/v2/account/inventory": `[{"id":5,"count":3}]`, "/v2/characters/Zed/inventory": `{"bags":[{"inventory":[{"id":5,"count":4}]}]}`,
	}
	var mu sync.Mutex
	calls := map[string]int{}
	c := testClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		body, ok := bodies[r.URL.Path]
		if !ok {
			return response(500, "unavailable"), nil
		}
		return response(200, body), nil
	}), "key")
	for range 2 {
		overview, err := c.Overview(context.Background())
		check(t, err == nil && string(overview.Account) == bodies["/v2/account"] && len(overview.Permissions) == 1, "account aggregation: %+v %v", overview, err)
		check(t, len(overview.Wallet) == 2 && overview.Wallet[0] == WalletEntry{1, "Coin", 99} && overview.Wallet[1] == WalletEntry{9, "", 1}, "wallet: %+v", overview.Wallet)
		items, err := c.Items(context.Background(), []int{7})
		check(t, err == nil && len(items) == 1 && items[0].Name == "Seven", "item metadata: %+v %v", items, err)
	}
	check(t, calls["/v2/items"] == 1 && calls["/v2/currencies"] == 1, "stable metadata was refetched: %v", calls)
	result, err := c.FindItems(context.Background(), []int{5, 6, 5})
	check(t, err == nil && !result.Complete && result.SourcesChecked == 4 && len(result.Items) == 2, "partial inventory: %+v %v", result, err)
	check(t, result.Items[0].Total == 9 && len(result.Items[0].Locations) == 3 && result.Items[0].Locations[2] == Location{"character:Zed", 4} && result.Items[1].Total == 0, "locations: %+v", result.Items)
	check(t, len(result.FailedSources) == 1 && result.FailedSources[0].Name == "materials", "missing source not reported: %+v", result.FailedSources)

	delete(bodies, "/v2/characters")
	result, err = c.FindItems(context.Background(), []int{5})
	check(t, err == nil && !result.Complete && result.Items[0].Total == 5 && result.SourcesChecked == 4 && len(result.FailedSources) == 2 && result.FailedSources[1].Name == "characters", "character list failure: %+v %v", result, err)
}
