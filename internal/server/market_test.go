package server

import (
	"math"
	"math/big"
	"testing"

	"gw2mcp/internal/gw2"
)

func TestQuoteDepthBindingAndOverflow(t *testing.T) {
	levels := []depthLevel{{Quantity: 2, UnitPrice: 80}, {Quantity: 3, UnitPrice: 100}}
	books := map[int]listingBook{1: {ID: 1, Buys: &levels, Sells: &levels}}
	items := map[int]gw2.Item{1: {ID: 1}}
	for _, want := range []struct {
		mode                               string
		quantity, gross, covered, unfilled int64
	}{
		{"sell_instant", 4, 380, 4, 0}, {"buy_instant", 6, 460, 5, 1},
	} {
		got, err := quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: want.quantity, Mode: want.mode}, items, books, "observed", false)
		check(t, err == nil && got.Gross == want.gross && got.Covered == want.covered && got.Unfilled == want.unfilled, "depth quote: %+v %v", got, err)
	}
	for _, flag := range []string{"AccountBound", "SoulbindOnAcquire"} {
		items[1] = gw2.Item{ID: 1, Flags: []string{flag}}
		_, err := quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: 1, Mode: "sell_instant"}, items, books, "observed", false)
		check(t, err != nil, "sale allowed for %s", flag)
	}
	_, err := standingQuote(quote{}, math.MaxInt64, 2)
	check(t, err != nil, "standing amount overflowed")
	_, err = depthQuote(quote{}, []depthLevel{{Quantity: 2, UnitPrice: math.MaxInt64}}, 2, true)
	check(t, err != nil, "depth amount overflowed")
}

func TestSaleSettlementRoundingAndLargeAmounts(t *testing.T) {
	for _, gross := range []int64{1, 19, 20, 99, 100, 1000, math.MaxInt64} {
		got, err := quoteFromSnapshot(quoteInput{ItemID: 1, Quantity: 1, UnitPrice: gross, Mode: "sell_listing"}, map[int]gw2.Item{1: {ID: 1}}, nil, "observed", false)
		check(t, err == nil, "%v", err)
		fee := func(percent int64) int64 {
			amount := new(big.Int).Mul(big.NewInt(gross), big.NewInt(percent))
			return amount.Div(amount, big.NewInt(100)).Int64()
		}
		listing, exchange := fee(5), fee(10)
		check(t, got.ListingFeeEstimate == listing && got.ExchangeFeeEstimate == exchange && got.NetEstimate == gross-listing-exchange && got.Upfront == listing && got.DeliveryProceedsEstimate == gross-exchange, "settlement for %d: %+v", gross, got)
	}
}
