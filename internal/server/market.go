package server

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"gw2mcp/internal/gw2"
)

type depthLevel struct {
	Quantity  int64 `json:"quantity"`
	UnitPrice int64 `json:"unit_price"`
}

type quoteInput struct {
	ItemID    int    `json:"item_id"`
	Quantity  int64  `json:"quantity"`
	Mode      string `json:"mode" jsonschema:"buy_instant, sell_instant, buy_order, or sell_listing"`
	UnitPrice int64  `json:"unit_price,omitempty" jsonschema:"copper per unit; required for buy_order and sell_listing"`
}

type quote struct {
	ItemID      int    `json:"item_id"`
	Quantity    int64  `json:"quantity"`
	Mode        string `json:"mode"`
	Gross       int64  `json:"gross"`
	Filled      int64  `json:"filled"`
	Unfilled    int64  `json:"unfilled,omitempty"`
	ListingFee  int64  `json:"listing_fee,omitempty"`
	ExchangeFee int64  `json:"exchange_fee,omitempty"`
	Net         int64  `json:"net"`
}

type quoteError struct {
	ItemID   int    `json:"item_id"`
	Quantity int64  `json:"quantity"`
	Mode     string `json:"mode"`
	Error    string `json:"error"`
}

const listingChunk = 5

type quoteTradesInput struct {
	Trades []quoteInput `json:"trades"`
}

type quoteBatch struct {
	ObservedAt string `json:"observed_at"`
	Quotes     []any  `json:"quotes"`
}

type listingBook struct {
	ID    int          `json:"id"`
	Buys  []depthLevel `json:"buys"`
	Sells []depthLevel `json:"sells"`
}

type marketClient interface {
	Items(context.Context, []int) ([]gw2.Item, error)
	GetByIDs(context.Context, string, []int, any) error
}

func validateQuoteInput(in quoteInput) error {
	if in.ItemID <= 0 || in.Quantity <= 0 {
		return fmt.Errorf("item_id and quantity must be positive")
	}
	switch in.Mode {
	case "sell_instant", "buy_instant":
		if in.UnitPrice != 0 {
			return fmt.Errorf("unit_price is only valid for buy_order and sell_listing")
		}
	case "buy_order", "sell_listing":
		if in.UnitPrice <= 0 {
			return fmt.Errorf("%s requires a positive unit_price", in.Mode)
		}
	default:
		return fmt.Errorf("mode must be sell_instant, buy_instant, buy_order, or sell_listing")
	}
	return nil
}

func isSale(mode string) bool    { return mode == "sell_instant" || mode == "sell_listing" }
func isInstant(mode string) bool { return mode == "sell_instant" || mode == "buy_instant" }

func quoteTradesHandler(api marketClient) func(context.Context, quoteTradesInput) (any, error) {
	return func(ctx context.Context, in quoteTradesInput) (any, error) {
		if len(in.Trades) < 1 || len(in.Trades) > 50 {
			return nil, fmt.Errorf("trades must contain between 1 and 50 entries")
		}
		saleIDs, bookIDs := map[int]bool{}, map[int]bool{}
		for _, trade := range in.Trades {
			if err := validateQuoteInput(trade); err != nil {
				return nil, err
			}
			if isSale(trade.Mode) {
				saleIDs[trade.ItemID] = true
			}
			if isInstant(trade.Mode) {
				bookIDs[trade.ItemID] = true
			}
		}
		items, books := map[int]gw2.Item{}, map[int]listingBook{}
		if len(saleIDs) > 0 {
			rows, err := api.Items(ctx, sortedItemIDs(saleIDs))
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				items[row.ID] = row
			}
		}
		chunks := slices.Collect(slices.Chunk(sortedItemIDs(bookIDs), listingChunk))
		rows, errs := make([][]listingBook, len(chunks)), make([]error, len(chunks))
		gw2.Parallel(len(chunks), func(i int) {
			errs[i] = api.GetByIDs(ctx, "/v2/commerce/listings", chunks[i], &rows[i])
		})
		for i, err := range errs {
			if err != nil {
				return nil, err
			}
			for _, row := range rows[i] {
				books[row.ID] = row
			}
		}
		batch := quoteBatch{ObservedAt: time.Now().UTC().Format(time.RFC3339), Quotes: make([]any, len(in.Trades))}
		for i, trade := range in.Trades {
			row, err := quoteFromSnapshot(trade, items, books)
			if err != nil {
				batch.Quotes[i] = quoteError{trade.ItemID, trade.Quantity, trade.Mode, err.Error()}
			} else {
				batch.Quotes[i] = row
			}
		}
		return batch, nil
	}
}

func sortedItemIDs(ids map[int]bool) []int {
	result := make([]int, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}

func quoteFromSnapshot(in quoteInput, items map[int]gw2.Item, books map[int]listingBook) (quote, error) {
	if isSale(in.Mode) {
		item, found := items[in.ItemID]
		if !found {
			return quote{}, fmt.Errorf("unknown item")
		}
		if slices.Contains(item.Flags, "AccountBound") || slices.Contains(item.Flags, "SoulbindOnAcquire") {
			return quote{}, fmt.Errorf("item is account-bound or soulbound and cannot be sold")
		}
	}
	result := quote{ItemID: in.ItemID, Quantity: in.Quantity, Mode: in.Mode}
	if isInstant(in.Mode) {
		book, found := books[in.ItemID]
		if !found {
			return quote{}, fmt.Errorf("item has no Trading Post listings")
		}
		levels := book.Sells
		if in.Mode == "sell_instant" {
			levels = book.Buys
		}
		if err := fillDepth(&result, levels); err != nil {
			return quote{}, err
		}
	} else {
		gross, ok := checkedMul(in.Quantity, in.UnitPrice)
		if !ok {
			return quote{}, fmt.Errorf("quote amount overflows int64")
		}
		result.Gross, result.Filled = gross, in.Quantity
	}
	result.Net = result.Gross
	if isSale(in.Mode) && result.Gross > 0 {
		result.ListingFee, result.ExchangeFee = max(1, result.Gross/20), max(1, result.Gross/10)
		result.Net = result.Gross - result.ListingFee - result.ExchangeFee
	}
	return result, nil
}

func fillDepth(result *quote, levels []depthLevel) error {
	levels = slices.Clone(levels)
	sort.Slice(levels, func(i, j int) bool {
		if result.Mode == "sell_instant" {
			return levels[i].UnitPrice > levels[j].UnitPrice
		}
		return levels[i].UnitPrice < levels[j].UnitPrice
	})
	remaining := result.Quantity
	for _, level := range levels {
		if remaining == 0 {
			break
		}
		if level.Quantity <= 0 || level.UnitPrice <= 0 {
			continue
		}
		units := min(level.Quantity, remaining)
		amount, ok := checkedMul(units, level.UnitPrice)
		if ok {
			result.Gross, ok = checkedAdd(result.Gross, amount)
		}
		if !ok {
			return fmt.Errorf("quote amount overflows int64")
		}
		result.Filled += units
		remaining -= units
	}
	result.Unfilled = remaining
	return nil
}

func checkedMul(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || (a != 0 && b > math.MaxInt64/a) {
		return 0, false
	}
	return a * b, true
}

func checkedAdd(a, b int64) (int64, bool) {
	if b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
