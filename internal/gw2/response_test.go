package gw2

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReadPreservesPartialStatusAndPagination(t *testing.T) {
	c := testClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		v := response(http.StatusPartialContent, `[{"id":7,"unmodeled":{"value":9007199254740993}}]`)
		v.Header.Set("X-Result-Total", "3")
		v.Header.Set("X-Page-Total", "2")
		return v, nil
	}), "key")
	r, err := c.Read(context.Background(), "/v2/commerce/prices?ids=7,8")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Partial || r.StatusCode != http.StatusPartialContent || r.Pagination["X-Result-Total"] != "3" || r.Endpoint != "/v2/commerce/prices" || r.ObservedAt.IsZero() {
		t.Fatalf("response metadata lost: %+v", r)
	}
	if !strings.Contains(string(r.Data), "9007199254740993") {
		t.Fatal("raw JSON precision lost")
	}
	var out json.RawMessage
	if err := c.Get(context.Background(), "/v2/commerce/prices?ids=7,8", &out); err == nil {
		t.Fatal("typed Get must reject partial results")
	}
}

func TestGetRawDiscoveryAndBodyLimit(t *testing.T) {
	c := testClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, "Public API\n /v2/account [a]\n /v2/items [l]"), nil
	}), "key")
	data, err := c.GetRaw(context.Background(), "/v2")
	if err != nil || !strings.Contains(string(data), "/v2/items") {
		t.Fatalf("discovery=%s err=%v", data, err)
	}
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
	})
	if _, err := c.GetRaw(context.Background(), "/v2"); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestAdditionalPrivateEndpointAuthentication(t *testing.T) {
	paths := []string{"v2/commerce/delivery", "v2/commerce/transactions/history/buys", "v2/pvp/stats", "v2/pvp/games", "v2/pvp/standings", "v2/guild/some-guild-id/stash"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			called := false
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing authorization")
				}
				return response(http.StatusOK, `[]`), nil
			})
			var out json.RawMessage
			if err := testClient(transport, "test-key").Get(context.Background(), path, &out); err != nil {
				t.Fatal(err)
			}
			called = false
			if err := testClient(transport, "").Get(context.Background(), path, &out); err == nil || called {
				t.Fatal("missing-key request reached API")
			}
		})
	}
	for _, path := range []string{"v2/guild/permissions", "v2/guild/upgrades", "v2/guild/search?name=example", "v2/commerce/exchange/gems?quantity=100"} {
		if privatePath(strings.Split(path, "?")[0]) {
			t.Fatalf("public route treated as private: %s", path)
		}
	}
}

func TestDiscoveredAuthenticationForNewRoutes(t *testing.T) {
	c := testClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("discovered private route lost authorization")
		}
		return response(http.StatusOK, `{"future_field":true}`), nil
	}), "test-key")
	if _, err := c.ReadRoute(context.Background(), "/v2/future/private", true); err != nil {
		t.Fatal(err)
	}
}
