package gw2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const performanceAPIKey = "benchmark-only-key"
const performanceDelay = 20 * time.Millisecond

type performanceFixture struct {
	requests   atomic.Int64
	authErrors atomic.Int64
}

func (f *performanceFixture) roundTrip(req *http.Request) (*http.Response, error) {
	time.Sleep(performanceDelay)
	f.requests.Add(1)
	if req.Header.Get("Authorization") != "Bearer "+performanceAPIKey {
		f.authErrors.Add(1)
	}
	var body string
	switch req.URL.Path {
	case "/v2/account":
		body = `{"id":"acc-1","name":"Benchmark"}`
	case "/v2/tokeninfo":
		body = `{"permissions":["inventories"]}`
	case "/v2/account/wallet":
		body = `[{"id":1,"value":20}]`
	case "/v2/account/bank":
		body = `[{"id":1001,"count":2}]`
	case "/v2/account/materials":
		body = `[{"id":1001,"count":3}]`
	case "/v2/account/inventory":
		body = `[{"id":1001,"count":4}]`
	case "/v2/characters":
		names := make([]string, 10)
		for i := range names {
			names[i] = fmt.Sprintf("Char%02d", i)
		}
		encoded, _ := json.Marshal(names)
		body = string(encoded)
	default:
		if strings.HasPrefix(req.URL.Path, "/v2/characters/") && strings.HasSuffix(req.URL.Path, "/inventory") {
			name := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/characters/"), "/inventory")
			var count int
			if _, err := fmt.Sscanf(name, "Char%02d", &count); err != nil || count < 0 || count > 9 {
				return response(http.StatusNotFound, `{"text":"unknown benchmark character"}`), nil
			}
			body = fmt.Sprintf(`{"bags":[{"inventory":[{"id":1001,"count":%d}]}]}`, count+1)
		} else {
			return response(http.StatusNotFound, `{"text":"unexpected benchmark route"}`), nil
		}
	}
	return response(http.StatusOK, body), nil
}

func newPerformanceClient() (*Client, *performanceFixture) {
	fixture := &performanceFixture{}
	client := NewClient(performanceAPIKey)
	client.httpClient = &http.Client{Timeout: 30 * time.Second, Transport: roundTripFunc(fixture.roundTrip)}
	return client, fixture
}

func BenchmarkAccountOverview(b *testing.B) {
	client, fixture := newPerformanceClient()
	ctx := context.Background()
	var result Overview
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = client.Overview(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if result.ID != "acc-1" || result.Name != "Benchmark" || len(result.Permissions) != 1 || len(result.Wallet) != 1 || result.Wallet[0].Value != 20 {
		b.Fatalf("incorrect overview result: %+v", result)
	}
	if got, want := fixture.requests.Load(), int64(b.N*3); got != want {
		b.Fatalf("fixture received %d requests, want %d", got, want)
	}
	if got := fixture.authErrors.Load(); got != 0 {
		b.Fatalf("fixture saw %d requests without the expected bearer key", got)
	}
}

func BenchmarkFindItemsComplex(b *testing.B) {
	client, fixture := newPerformanceClient()
	ctx := context.Background()
	var result FindItemsResult
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = client.FindItems(ctx, []int{1001})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if !result.Complete || len(result.Items) != 1 || result.Items[0].ItemID != 1001 || result.Items[0].ObservedTotal != 64 || len(result.Items[0].Locations) != 13 || len(result.Sources) != 14 {
		b.Fatalf("incorrect inventory result: %+v", result)
	}
	if got, want := fixture.requests.Load(), int64(b.N*14); got != want {
		b.Fatalf("fixture received %d requests, want %d", got, want)
	}
	if got := fixture.authErrors.Load(); got != 0 {
		b.Fatalf("fixture saw %d requests without the expected bearer key", got)
	}
}
