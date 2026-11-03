package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type batchMarketFixture struct {
	surfaceFixture
	metadataCalls [][]int
	readPaths     []string
	items         []gw2.Item
	books         []listingBook
	partial       bool
}

func (f *batchMarketFixture) Items(_ context.Context, ids []int) ([]gw2.Item, error) {
	f.metadataCalls = append(f.metadataCalls, append([]int(nil), ids...))
	return f.items, nil
}

func (f *batchMarketFixture) Read(_ context.Context, path string) (gw2.APIResponse, error) {
	f.readPaths = append(f.readPaths, path)
	body, err := json.Marshal(f.books)
	if err != nil {
		return gw2.APIResponse{}, err
	}
	return gw2.APIResponse{
		Data: body, Endpoint: strings.SplitN(path, "?", 2)[0],
		ObservedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		StatusCode: 206, Partial: f.partial,
		Pagination: map[string]string{"X-Result-Count": fmt.Sprint(len(f.books))},
	}, nil
}

func TestQuoteTradesBatchesUniqueMarketReadsAndKeepsItemsIndependent(t *testing.T) {
	fixture := &batchMarketFixture{
		items: []gw2.Item{{ID: 1}, {ID: 3, Flags: []string{"SoulbindOnAcquire"}}},
		books: []listingBook{
			{ID: 1, Buys: &[]depthLevel{{Quantity: 1, UnitPrice: 100}, {Quantity: 2, UnitPrice: 90}}, Sells: &[]depthLevel{{Quantity: 1, UnitPrice: 120}, {Quantity: 2, UnitPrice: 130}}},
			{ID: 2, Buys: &[]depthLevel{}, Sells: &[]depthLevel{}},
		},
		partial: true,
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "quote-batch-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "quote_trades", Description: "test"}, quoteTradesHandler(fixture))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	args := json.RawMessage(`{"trades":[{"item_id":1,"quantity":2,"mode":"sell_instant"},{"item_id":1,"quantity":2,"mode":"buy_instant"},{"item_id":2,"quantity":3,"mode":"buy_order","unit_price":10},{"item_id":3,"quantity":1,"mode":"sell_listing","unit_price":50},{"item_id":4,"quantity":1,"mode":"buy_instant"}]}`)
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "quote_trades", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("quote_trades: result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(fixture.metadataCalls, [][]int{{1, 3}}) {
		t.Fatalf("metadata calls = %v", fixture.metadataCalls)
	}
	if len(fixture.readPaths) != 1 {
		t.Fatalf("market snapshot calls = %v", fixture.readPaths)
	}
	query, err := url.ParseQuery(strings.SplitN(fixture.readPaths[0], "?", 2)[1])
	if err != nil || query.Get("ids") != "1,4" {
		t.Fatalf("market snapshot request = %q (query=%v err=%v)", fixture.readPaths[0], query, err)
	}
	var envelope struct {
		Data struct {
			Partial    bool               `json:"partial"`
			StatusCode int                `json:"status_code"`
			Request    string             `json:"request"`
			Results    []quoteTradeResult `json:"results"`
		} `json:"data"`
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.Partial || envelope.Data.StatusCode != 206 || envelope.Data.Request != fixture.readPaths[0] || len(envelope.Data.Results) != 5 {
		t.Fatalf("batch response = %s", encoded)
	}
	if got := envelope.Data.Results[0].Quote; got == nil || got.Gross != 190 || got.Covered != 2 || got.NetEstimate != 162 {
		t.Fatalf("sell instant quote = %+v", got)
	}
	if !strings.Contains(strings.Join(envelope.Data.Results[0].Quote.Warnings, " "), "estimated independently") {
		t.Fatal("batch quote omitted the independent-estimate warning")
	}
	if got := envelope.Data.Results[1].Quote; got == nil || got.Gross != 250 || got.Covered != 2 {
		t.Fatalf("buy instant quote = %+v", got)
	}
	if got := envelope.Data.Results[2].Quote; got == nil || got.Gross != 30 || !got.Conditional {
		t.Fatalf("standing order quote = %+v", got)
	}
	if row := envelope.Data.Results[3]; row.Status != "error" || !strings.Contains(row.Error, "bound") || row.Quote != nil {
		t.Fatalf("bound sale row = %+v", row)
	}
	if row := envelope.Data.Results[4]; row.Status != "error" || row.Quote != nil {
		t.Fatalf("missing listing row = %+v", row)
	}
	for _, row := range envelope.Data.Results {
		if row.Status == "ok" && (row.Quote == nil || row.Quote.ObservedAt == "") {
			t.Errorf("successful row missing quote timestamp: %+v", row)
		}
	}
}

func TestQuoteTradesValidatesEverythingBeforeFetching(t *testing.T) {
	fixture := &batchMarketFixture{}
	handler := quoteTradesHandler(fixture)
	_, _, err := handler(context.Background(), nil, quoteTradesInput{Trades: []quoteInput{
		{ItemID: 1, Quantity: 1, Mode: "buy_order", UnitPrice: 2},
		{ItemID: 2, Quantity: 1, Mode: "bad"},
	}})
	if err == nil {
		t.Fatal("invalid second trade was accepted")
	}
	if len(fixture.metadataCalls) != 0 || len(fixture.readPaths) != 0 {
		t.Fatalf("API was called before all inputs were validated: metadata=%v reads=%v", fixture.metadataCalls, fixture.readPaths)
	}
	for _, trades := range [][]quoteInput{nil, make([]quoteInput, 51)} {
		if _, _, err := handler(context.Background(), nil, quoteTradesInput{Trades: trades}); err == nil {
			t.Fatalf("batch length %d was accepted", len(trades))
		}
	}
}

func TestQuoteTradesStandingOnlyDoesNotFetchListings(t *testing.T) {
	fixture := &batchMarketFixture{}
	handler := quoteTradesHandler(fixture)
	_, result, err := handler(context.Background(), nil, quoteTradesInput{Trades: []quoteInput{
		{ItemID: 1, Quantity: 2, Mode: "buy_order", UnitPrice: 25},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.metadataCalls) != 0 || len(fixture.readPaths) != 0 {
		t.Fatalf("buy-order estimate made unnecessary API calls: metadata=%v reads=%v", fixture.metadataCalls, fixture.readPaths)
	}
	encoded, _ := json.Marshal(result.Data)
	if !strings.Contains(string(encoded), `"gross":50`) {
		t.Fatalf("standing quote missing from result: %s", encoded)
	}
}

func TestQuoteTradesBoundMetadataFetchedOnceForSaleModes(t *testing.T) {
	fixture := &batchMarketFixture{
		items: []gw2.Item{{ID: 8}},
		books: []listingBook{{ID: 8, Buys: &[]depthLevel{{Quantity: 1, UnitPrice: 20}}, Sells: &[]depthLevel{}}},
	}
	handler := quoteTradesHandler(fixture)
	_, _, err := handler(context.Background(), nil, quoteTradesInput{Trades: []quoteInput{
		{ItemID: 8, Quantity: 1, Mode: "sell_listing", UnitPrice: 30},
		{ItemID: 8, Quantity: 1, Mode: "sell_instant"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.metadataCalls) != 1 || !reflect.DeepEqual(fixture.metadataCalls[0], []int{8}) {
		t.Fatalf("sale metadata calls = %v", fixture.metadataCalls)
	}
	if len(fixture.readPaths) != 1 {
		t.Fatalf("listing calls = %v", fixture.readPaths)
	}
	parsed, err := url.Parse(fixture.readPaths[0])
	if err != nil || parsed.Query().Get("ids") != "8" {
		t.Fatalf("listing path = %q err=%v", fixture.readPaths[0], err)
	}
	if !sort.IntsAreSorted(fixture.metadataCalls[0]) {
		t.Fatal("metadata IDs are not deterministic")
	}
}

func TestSingleSaleQuoteRejectsMissingItemMetadata(t *testing.T) {
	fixture := &batchMarketFixture{}
	handler := quoteHandler(fixture)
	_, _, err := handler(context.Background(), nil, quoteInput{ItemID: 9, Quantity: 1, Mode: "sell_listing", UnitPrice: 10})
	if err == nil || !strings.Contains(err.Error(), "metadata was not returned") {
		t.Fatalf("missing metadata error = %v", err)
	}
	if len(fixture.metadataCalls) != 1 || len(fixture.readPaths) != 0 {
		t.Fatalf("unexpected API calls: metadata=%v listings=%v", fixture.metadataCalls, fixture.readPaths)
	}
}
