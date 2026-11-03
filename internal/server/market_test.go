package server

import (
	"math"
	"testing"

	"gw2mcp/internal/gw2"
)

func TestDepthQuoteOrdersAndPartialLiquidity(t *testing.T) {
	bids := []depthLevel{{Quantity: 2, UnitPrice: 80}, {Quantity: 3, UnitPrice: 100}}
	got, err := depthQuote(quote{}, bids, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gross != 380 || got.Covered != 4 || got.Unfilled != 0 {
		t.Fatalf("bid quote = %+v", got)
	}

	asks := []depthLevel{{Quantity: 2, UnitPrice: 80}, {Quantity: 3, UnitPrice: 100}}
	got, err = depthQuote(quote{}, asks, 6, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gross != 460 || got.Covered != 5 || got.Unfilled != 1 {
		t.Fatalf("ask quote = %+v", got)
	}
}

func TestSellerFeeAndSettlementEstimates(t *testing.T) {
	for _, mode := range []string{"sell_listing", "sell_instant"} {
		got, err := applySellerFees(quote{Gross: 1000, Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if got.ListingFeeEstimate != 50 || got.ExchangeFeeEstimate != 100 || got.NetEstimate != 850 || got.Upfront != 50 || got.DeliveryProceedsEstimate != 900 {
			t.Fatalf("%s settlement = %+v", mode, got)
		}
	}
}

func TestStandingQuotePriceAndOverflow(t *testing.T) {
	got, err := standingQuote(quote{}, 3, 25)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gross != 75 || got.Covered != 3 || !got.Conditional {
		t.Fatalf("standing quote = %+v", got)
	}
	if _, err := standingQuote(quote{}, math.MaxInt64, 2); err == nil {
		t.Fatal("expected standing quote overflow")
	}
}

func TestDepthQuoteOverflow(t *testing.T) {
	levels := []depthLevel{{Quantity: 2, UnitPrice: math.MaxInt64}}
	if _, err := depthQuote(quote{}, levels, 2, true); err == nil {
		t.Fatal("expected depth quote overflow")
	}
}

func TestBindingFlags(t *testing.T) {
	if !hasSaleBinding(gw2.Item{Flags: []string{"AccountBound"}}) {
		t.Fatal("account-bound flag was missed")
	}
	if !hasSaleBinding(gw2.Item{Flags: []string{"SoulbindOnAcquire"}}) {
		t.Fatal("soulbind flag was missed")
	}
	if hasSaleBinding(gw2.Item{Flags: []string{"NoSell"}}) {
		t.Fatal("unrelated flag rejected")
	}
}
