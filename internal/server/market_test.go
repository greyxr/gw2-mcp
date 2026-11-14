package server

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"

	"gw2mcp/internal/gw2"
)

type marketFixture struct {
	mu                   sync.Mutex
	metadataCalls, reads int
	largest              int
	items                []gw2.Item
	books                []listingBook
}

func (f *marketFixture) Items(context.Context, []int) ([]gw2.Item, error) {
	f.metadataCalls++
	return f.items, nil
}

func (f *marketFixture) GetByIDs(_ context.Context, _ string, ids []int, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.largest = max(f.largest, len(ids))
	body, _ := json.Marshal(f.books)
	return json.Unmarshal(body, out)
}

func TestBatchQuotesShareDataAndStayIndependent(t *testing.T) {
	fixture := &marketFixture{
		items: []gw2.Item{{ID: 1}, {ID: 3, Flags: []string{"SoulbindOnAcquire"}}, {ID: 5, Flags: []string{"AccountBound"}}},
		books: []listingBook{
			{ID: 1, Buys: []depthLevel{{Quantity: 2, UnitPrice: 90}, {Quantity: 1, UnitPrice: 100}}},
			{ID: 2, Sells: []depthLevel{{Quantity: 1, UnitPrice: 80}}},
		},
	}
	value, err := quoteTradesHandler(fixture)(context.Background(), quoteTradesInput{Trades: []quoteInput{
		{ItemID: 1, Quantity: 2, Mode: "sell_instant"},
		{ItemID: 1, Quantity: 2, Mode: "sell_instant"},
		{ItemID: 2, Quantity: 3, Mode: "buy_instant"},
		{ItemID: 3, Quantity: 1, Mode: "sell_listing", UnitPrice: 50},
		{ItemID: 4, Quantity: 1, Mode: "buy_instant"},
		{ItemID: 5, Quantity: 1, Mode: "sell_instant"},
		{ItemID: 6, Quantity: 1, Mode: "sell_listing", UnitPrice: 50},
	}})
	check(t, err == nil, "%v", err)
	batch := value.(quoteBatch)
	check(t, fixture.metadataCalls == 1 && fixture.reads == 1 && batch.ObservedAt != "" && len(batch.Quotes) == 7, "batch: %+v", batch)
	for _, value := range batch.Quotes[:2] {
		row := value.(quote)
		check(t, row.Gross == 190 && row.ListingFee == 9 && row.ExchangeFee == 19 && row.Net == 162 && row.Filled == 2 && row.Unfilled == 0, "independent sale: %+v", row)
	}
	buy := batch.Quotes[2].(quote)
	check(t, buy.Gross == 80 && buy.Net == 80 && buy.Filled == 1 && buy.Unfilled == 2 && buy.ListingFee == 0, "partial liquidity: %+v", buy)
	for _, value := range batch.Quotes[3:] {
		row, failed := value.(quoteError)
		check(t, failed && row.Error != "" && row.ItemID != 0, "unsafe quote: %+v", value)
	}

	var trades []quoteInput
	for id := 1; id <= 50; id++ {
		trades = append(trades, quoteInput{ItemID: id, Quantity: 1, Mode: "buy_instant"})
	}
	chunked := &marketFixture{}
	_, err = quoteTradesHandler(chunked)(context.Background(), quoteTradesInput{Trades: trades})
	check(t, err == nil && chunked.reads == 10 && chunked.largest == listingChunk, "books fetched in %d reads of up to %d: %v", chunked.reads, chunked.largest, err)
}

func TestQuoteValidationAndStandingOrders(t *testing.T) {
	fixture := &marketFixture{}
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
	check(t, err == nil && value.(quoteBatch).Quotes[0].(quote).Gross == 50, "standing quote: %v", err)
	check(t, fixture.metadataCalls == 0 && fixture.reads == 0, "invalid or standing-only estimates fetched unnecessary data")
}

func TestSellerFeesAndOverflow(t *testing.T) {
	items := map[int]gw2.Item{1: {ID: 1}}
	for _, want := range []struct{ gross, listing, exchange int64 }{
		{1, 1, 1}, {19, 1, 1}, {20, 1, 2}, {99, 4, 9}, {1000, 50, 100}, {math.MaxInt64, math.MaxInt64 / 20, math.MaxInt64 / 10},
	} {
		got, err := quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: 1, UnitPrice: want.gross, Mode: "sell_listing"}, items, nil)
		check(t, err == nil && got.ListingFee == want.listing && got.ExchangeFee == want.exchange && got.Net == want.gross-want.listing-want.exchange, "fees for %d: %+v %v", want.gross, got, err)
	}
	_, err := quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: math.MaxInt64, UnitPrice: 2, Mode: "buy_order"}, items, nil)
	check(t, err != nil, "standing amount overflowed")
	levels := []depthLevel{{Quantity: 2, UnitPrice: math.MaxInt64}}
	_, err = quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: 2, Mode: "buy_instant"}, items, map[int]listingBook{1: {ID: 1, Sells: levels}})
	check(t, err != nil, "depth amount overflowed")
}
