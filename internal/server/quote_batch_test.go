package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gw2mcp/internal/gw2"
)

type batchMarketFixture struct {
	metadataCalls, reads int
	items                []gw2.Item
	books                []listingBook
	partial              bool
}

func (f *batchMarketFixture) Items(context.Context, []int) ([]gw2.Item, error) {
	f.metadataCalls++
	return f.items, nil
}
func (f *batchMarketFixture) ReadRoute(context.Context, string, bool) (gw2.APIResponse, error) {
	f.reads++
	body, _ := json.Marshal(f.books)
	return gw2.APIResponse{Data: body, ObservedAt: time.Now().UTC(), StatusCode: 206, Partial: f.partial}, nil
}

func TestBatchQuotesShareDataAndPreserveIndependentOutcomes(t *testing.T) {
	fixture := &batchMarketFixture{
		items: []gw2.Item{{ID: 1}, {ID: 3, Flags: []string{"SoulbindOnAcquire"}}},
		books: []listingBook{
			{ID: 1, Buys: &[]depthLevel{{Quantity: 1, UnitPrice: 100}, {Quantity: 2, UnitPrice: 90}}, Sells: &[]depthLevel{}},
			{ID: 2, Sells: &[]depthLevel{{Quantity: 1, UnitPrice: 80}}},
		},
		partial: true,
	}
	value, err := quoteTradesHandler(fixture)(context.Background(), quoteTradesInput{Trades: []quoteInput{
		{ItemID: 1, Quantity: 2, Mode: "sell_instant"},
		{ItemID: 1, Quantity: 2, Mode: "sell_instant"},
		{ItemID: 2, Quantity: 3, Mode: "buy_instant"},
		{ItemID: 3, Quantity: 1, Mode: "sell_listing", UnitPrice: 50},
		{ItemID: 4, Quantity: 1, Mode: "buy_instant"},
	}})
	check(t, err == nil, "%v", err)
	batch := value.(quoteBatch)
	check(t, fixture.metadataCalls == 1 && fixture.reads == 1, "batch repeated shared fetches")
	check(t, batch.Partial && batch.Independent && len(batch.Results) == 5, "batch coverage: %+v", batch)
	for _, row := range batch.Results[:2] {
		check(t, row.Quote != nil && row.Quote.Gross == 190 && row.Quote.NetEstimate == 162 && row.Quote.Covered == 2 && row.Quote.ObservedAt != "", "independent sale: %+v", row)
	}
	buy := batch.Results[2].Quote
	check(t, buy != nil && buy.Gross == 80 && buy.Covered == 1 && buy.Unfilled == 2, "partial liquidity: %+v", buy)
	for _, row := range batch.Results[3:] {
		check(t, row.Status == "error" && row.Quote == nil, "unsafe quote: %+v", row)
	}
	one := quoteInput{ItemID: 2, Quantity: 1, Mode: "buy_instant"}
	_, err = quoteTradesHandler(fixture)(context.Background(), quoteTradesInput{Trades: []quoteInput{one}})
	check(t, err == nil, "one-row batch lost partial support: %v", err)
	_, err = quoteHandler(fixture)(context.Background(), one)
	check(t, err != nil, "single quote accepted incomplete response")
}

func TestQuoteValidationAndStandingOrders(t *testing.T) {
	fixture := &batchMarketFixture{}
	run := quoteTradesHandler(fixture)
	for _, trades := range [][]quoteInput{
		nil, make([]quoteInput, 51), {{ItemID: 1, Quantity: 1, Mode: "unknown"}},
		{{ItemID: 1, Quantity: 1, Mode: "sell_listing"}},
		{{ItemID: 1, Quantity: 1, Mode: "buy_instant", UnitPrice: 5}},
		{{ItemID: 1, Quantity: 1, Mode: "buy_order", UnitPrice: 5}, {ItemID: 2, Quantity: -1, Mode: "buy_instant"}},
	} {
		_, err := run(context.Background(), quoteTradesInput{Trades: trades})
		check(t, err != nil, "invalid batch accepted: %+v", trades)
	}
	value, err := run(context.Background(), quoteTradesInput{Trades: []quoteInput{{ItemID: 1, Quantity: 2, Mode: "buy_order", UnitPrice: 25}}})
	check(t, err == nil && value.(quoteBatch).Results[0].Quote.Gross == 50, "standing quote: %v", err)
	check(t, fixture.metadataCalls == 0 && fixture.reads == 0, "invalid or standing-only estimates fetched unnecessary data")
}
