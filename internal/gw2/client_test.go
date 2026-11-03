package gw2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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

func TestGetScopesBearerAndRejectsPartial(t *testing.T) {
	var publicAuth string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/v2/account") {
			if r.Header.Get("Authorization") != "Bearer secret-token" {
				t.Errorf("private authorization missing")
			}
			return response(http.StatusPartialContent, ""), nil
		}
		publicAuth = r.Header.Get("Authorization")
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	c := testClient(rt, "secret-token")
	var out map[string]bool
	if err := c.Get(context.Background(), "v2/items/1", &out); err != nil {
		t.Fatal(err)
	}
	if publicAuth != "" {
		t.Fatalf("public request carried authorization")
	}
	err := c.Get(context.Background(), "v2/account", &out)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe partial/error result: %v", err)
	}
}

func TestOverviewAndItemCache(t *testing.T) {
	calls := map[string]int{}
	var callsMu sync.Mutex
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		callsMu.Lock()
		calls[r.URL.Path]++
		callsMu.Unlock()
		switch r.URL.Path {
		case "/v2/account":
			return response(http.StatusOK, `{"id":"abc","name":"N"}`), nil
		case "/v2/tokeninfo":
			return response(http.StatusOK, `{"permissions":["inventories"]}`), nil
		case "/v2/account/wallet":
			return response(http.StatusOK, `[ {"id":1,"value":99} ]`), nil
		case "/v2/items":
			return response(http.StatusOK, `[ {"id":7,"name":"Seven","type":"Armor","rarity":"Fine","flags":[]} ]`), nil
		default:
			return response(http.StatusNotFound, "not found"), nil
		}
	})
	c := testClient(rt, "key")
	o, err := c.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "abc" || len(o.Permissions) != 1 || len(o.Wallet) != 1 || o.Wallet[0].Value != 99 {
		t.Fatalf("bad overview: %+v", o)
	}
	for i := 0; i < 2; i++ {
		items, err := c.Items(context.Background(), []int{7})
		if err != nil || len(items) != 1 || items[0].Name != "Seven" {
			t.Fatalf("items=%v err=%v", items, err)
		}
	}
	callsMu.Lock()
	itemCalls := calls["/v2/items"]
	callsMu.Unlock()
	if itemCalls != 1 {
		t.Fatalf("cache did not limit fetches: %v", calls)
	}
}

func TestFindItemPartialSourcesAndCounts(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/account/bank":
			return response(http.StatusOK, `[{"id":5,"count":2}]`), nil
		case "/v2/account/materials":
			return response(http.StatusServiceUnavailable, "unavailable"), nil
		case "/v2/account/inventory":
			return response(http.StatusOK, `[{"id":5,"count":3}]`), nil
		case "/v2/characters":
			return response(http.StatusOK, `["Zed"]`), nil
		case "/v2/characters/Zed/inventory":
			return response(http.StatusOK, `{"bags":[{"inventory":[{"id":5,"count":4}]}]}`), nil
		default:
			return response(http.StatusNotFound, "not found"), nil
		}
	})
	res, err := testClient(rt, "key").FindItem(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete || res.Total != 9 || len(res.Locations) != 3 || len(res.Sources) != 5 {
		t.Fatalf("incomplete inventory accounting: %+v", res)
	}
	wantSources := []Source{
		{Name: "bank", Status: "ok"},
		{Name: "materials", Status: "error"},
		{Name: "shared_inventory", Status: "ok"},
		{Name: "characters", Status: "ok"},
		{Name: "character:Zed", Status: "ok"},
	}
	for i, source := range res.Sources {
		if source.Name != wantSources[i].Name || source.Status != wantSources[i].Status {
			t.Fatalf("source order/result = %+v, want %+v", res.Sources, wantSources)
		}
	}
	counts := make(map[string]int64, len(res.Locations))
	for _, location := range res.Locations {
		counts[location.Location] = location.Count
	}
	if counts["bank"] != 2 || counts["shared_inventory"] != 3 || counts["character:Zed:bags"] != 4 {
		t.Fatalf("unexpected location counts: %v", counts)
	}
}

func TestPrivateMissingKeyDoesNotRequest(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})
	c := testClient(rt, "")
	var out json.RawMessage
	if err := c.Get(context.Background(), "v2/account", &out); err == nil {
		t.Fatal("expected missing key error")
	}
}

func TestGetValidatesPathSegments(t *testing.T) {
	var paths []string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.EscapedPath())
		return response(http.StatusOK, `[]`), nil
	})
	c := testClient(rt, "key")
	var out []string
	validPath := "v2/characters/" + url.PathEscape("A..B") + "/crafting"
	if err := c.Get(context.Background(), validPath, &out); err != nil {
		t.Fatalf("valid character name rejected: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("valid path was not requested: %v", paths)
	}
	for _, path := range []string{"v2/items/../account", "v2/items/%2e%2e/account"} {
		if err := c.Get(context.Background(), path, &out); err == nil {
			t.Errorf("traversal path %q was accepted", path)
		}
	}
	if len(paths) != 1 {
		t.Fatalf("invalid paths reached server: %v", paths)
	}
}
