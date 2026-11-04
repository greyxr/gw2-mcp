package gw2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func newPerformanceClient() (*Client, *performanceFixture) {
	fixture := &performanceFixture{}
	bodies := map[string]string{
		"/v2/account":           `{"id":"acc-1","name":"Benchmark"}`,
		"/v2/tokeninfo":         `{"permissions":["inventories"]}`,
		"/v2/account/wallet":    `[{"id":1,"value":20}]`,
		"/v2/account/bank":      `[{"id":1001,"count":2}]`,
		"/v2/account/materials": `[{"id":1001,"count":3}]`,
		"/v2/account/inventory": `[{"id":1001,"count":4}]`,
	}
	names := make([]string, 10)
	for i := range names {
		names[i] = fmt.Sprintf("Char%02d", i)
		bodies["/v2/characters/"+names[i]+"/inventory"] = fmt.Sprintf(`{"bags":[{"inventory":[{"id":1001,"count":%d}]}]}`, i+1)
	}
	encoded, _ := json.Marshal(names)
	bodies["/v2/characters"] = string(encoded)
	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		time.Sleep(performanceDelay)
		fixture.requests.Add(1)
		if req.Header.Get("Authorization") != "Bearer "+performanceAPIKey {
			fixture.authErrors.Add(1)
		}
		body, ok := bodies[req.URL.Path]
		if !ok {
			return response(404, "unexpected benchmark route"), nil
		}
		return response(200, body), nil
	}), performanceAPIKey)
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
		check(b, err == nil, "%v", err)
	}
	b.StopTimer()
	check(b, result.ID == "acc-1" && result.Name == "Benchmark" && len(result.Permissions) == 1 && len(result.Wallet) == 1 && result.Wallet[0].Value == 20, "incorrect overview result: %+v", result)
	check(b, fixture.requests.Load() == int64(b.N*3) && fixture.authErrors.Load() == 0, "unexpected request count or authentication")
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
		check(b, err == nil, "%v", err)
	}
	b.StopTimer()
	check(b, result.Complete && len(result.Items) == 1 && result.Items[0].ItemID == 1001 && result.Items[0].ObservedTotal == 64 && len(result.Items[0].Locations) == 13 && len(result.Sources) == 14, "incorrect inventory result: %+v", result)
	check(b, fixture.requests.Load() == int64(b.N*14) && fixture.authErrors.Load() == 0, "unexpected request count or authentication")
}
