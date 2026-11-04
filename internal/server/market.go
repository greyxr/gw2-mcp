package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
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
	Mode      string `json:"mode"`
	UnitPrice int64  `json:"unit_price,omitempty"`
}

type quote struct {
	ItemID                   int      `json:"item_id"`
	Quantity                 int64    `json:"quantity"`
	Mode                     string   `json:"mode"`
	Gross                    int64    `json:"gross"`
	ListingFeeEstimate       int64    `json:"listing_fee_estimate"`
	ExchangeFeeEstimate      int64    `json:"exchange_fee_estimate"`
	DeliveryProceedsEstimate int64    `json:"delivery_proceeds_estimate"`
	NetEstimate              int64    `json:"net_estimate"`
	Upfront                  int64    `json:"upfront"`
	Covered                  int64    `json:"covered"`
	Unfilled                 int64    `json:"unfilled"`
	Conditional              bool     `json:"conditional_on_fill"`
	Warnings                 []string `json:"warnings"`
	ObservedAt               string   `json:"observed_at,omitempty"`
}

type quoteTradesInput struct {
	Trades []quoteInput `json:"trades"`
}

type quoteTradeResult struct {
	Index    int    `json:"index"`
	ItemID   int    `json:"item_id"`
	Quantity int64  `json:"quantity"`
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Quote    *quote `json:"quote,omitempty"`
	Error    string `json:"error,omitempty"`
}

type listingBook struct {
	ID    int           `json:"id"`
	Buys  *[]depthLevel `json:"buys"`
	Sells *[]depthLevel `json:"sells"`
}

type marketClient interface {
	Items(context.Context, []int) ([]gw2.Item, error)
	ReadRoute(context.Context, string, bool) (gw2.APIResponse, error)
}

type quoteSnapshot struct {
	items    map[int]gw2.Item
	books    map[int]listingBook
	response gw2.APIResponse
	request  string
}

func quoteHandler(api marketClient) func(context.Context, quoteInput) (any, error) {
	return func(ctx context.Context, in quoteInput) (any, error) {
		if err := validateQuoteInput(in); err != nil {
			return nil, err
		}
		snapshot, err := loadQuoteSnapshot(ctx, api, []quoteInput{in})
		if err != nil {
			return nil, err
		}
		if snapshot.response.Partial {
			return nil, fmt.Errorf("market listing response is partial")
		}
		stamp := snapshot.response.ObservedAt
		result, err := quoteFromSnapshot(in, snapshot.items, snapshot.books, stamp.Format(time.RFC3339), false)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

func validateQuoteInput(in quoteInput) error {
	if in.ItemID <= 0 || in.Quantity <= 0 {
		return fmt.Errorf("item_id and quantity must be positive")
	}
	switch in.Mode {
	case "sell_instant", "buy_instant":
		if in.UnitPrice != 0 {
			return fmt.Errorf("unit_price is only valid for standing orders")
		}
	case "buy_order", "sell_listing":
		if in.UnitPrice <= 0 {
			return fmt.Errorf("standing modes require a positive unit_price")
		}
	default:
		return fmt.Errorf("mode must be sell_instant, buy_instant, buy_order, or sell_listing")
	}
	return nil
}

func quoteTradesHandler(api marketClient) func(context.Context, quoteTradesInput) (any, error) {
	return func(ctx context.Context, in quoteTradesInput) (any, error) {
		if len(in.Trades) < 1 || len(in.Trades) > 50 {
			return nil, fmt.Errorf("trades must contain between 1 and 50 entries")
		}
		for _, trade := range in.Trades {
			if err := validateQuoteInput(trade); err != nil {
				return nil, err
			}
		}
		snapshot, err := loadQuoteSnapshot(ctx, api, in.Trades)
		if err != nil {
			return nil, err
		}
		stamp := snapshot.response.ObservedAt.UTC().Format(time.RFC3339)
		results := make([]quoteTradeResult, 0, len(in.Trades))
		for index, trade := range in.Trades {
			row := quoteTradeResult{Index: index, ItemID: trade.ItemID, Quantity: trade.Quantity, Mode: trade.Mode, Status: "ok"}
			value, err := quoteFromSnapshot(trade, snapshot.items, snapshot.books, stamp, true)
			if err != nil {
				row.Status, row.Error = "error", err.Error()
			} else {
				row.Quote = &value
			}
			results = append(results, row)
		}
		return quoteBatch{Results: results, ObservedAt: stamp, Partial: snapshot.response.Partial,
			Pagination: snapshot.response.Pagination, Independent: true, Request: snapshot.request, StatusCode: snapshot.response.StatusCode}, nil
	}
}

type quoteBatch struct {
	Results     []quoteTradeResult `json:"results"`
	ObservedAt  string             `json:"observed_at"`
	Partial     bool               `json:"partial"`
	Pagination  map[string]string  `json:"pagination"`
	Independent bool               `json:"quotes_are_independent"`
	Request     string             `json:"request,omitempty"`
	StatusCode  int                `json:"status_code,omitempty"`
}

func loadQuoteSnapshot(ctx context.Context, api marketClient, trades []quoteInput) (quoteSnapshot, error) {
	saleIDs, bookIDs := map[int]bool{}, map[int]bool{}
	for _, trade := range trades {
		if trade.Mode == "sell_instant" || trade.Mode == "sell_listing" {
			saleIDs[trade.ItemID] = true
		}
		if trade.Mode == "sell_instant" || trade.Mode == "buy_instant" {
			bookIDs[trade.ItemID] = true
		}
	}
	snapshot := quoteSnapshot{items: map[int]gw2.Item{}, books: map[int]listingBook{}, response: gw2.APIResponse{ObservedAt: time.Now().UTC()}}
	if len(saleIDs) > 0 {
		rows, err := api.Items(ctx, sortedItemIDs(saleIDs))
		if err != nil {
			return quoteSnapshot{}, err
		}
		for _, row := range rows {
			snapshot.items[row.ID] = row
		}
	}
	if len(bookIDs) == 0 {
		return snapshot, nil
	}
	ids := sortedItemIDs(bookIDs)
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.Itoa(id)
	}
	query := url.Values{"ids": {strings.Join(values, ",")}}
	snapshot.request = "/v2/commerce/listings?" + query.Encode()
	response, err := api.ReadRoute(ctx, snapshot.request, false)
	if err != nil {
		return quoteSnapshot{}, err
	}
	snapshot.response = response
	var rows []listingBook
	if err := json.Unmarshal(response.Data, &rows); err != nil {
		return quoteSnapshot{}, fmt.Errorf("invalid listings response: %w", err)
	}
	for _, row := range rows {
		snapshot.books[row.ID] = row
	}
	return snapshot, nil
}

func sortedItemIDs(ids map[int]bool) []int {
	result := make([]int, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}

func quoteFromSnapshot(in quoteInput, items map[int]gw2.Item, books map[int]listingBook, observedAt string, independent bool) (quote, error) {
	if in.Mode == "sell_instant" || in.Mode == "sell_listing" {
		item, found := items[in.ItemID]
		if !found {
			return quote{}, fmt.Errorf("item metadata was not returned; cannot check sale binding")
		}
		if hasSaleBinding(item) {
			return quote{}, fmt.Errorf("item metadata marks this item as account-bound or soulbound on acquire")
		}
	}
	result := quote{
		ItemID: in.ItemID, Quantity: in.Quantity, Mode: in.Mode, ObservedAt: observedAt,
		Warnings: []string{
			"Fee rounding, minimums, and grouping are unknown; percentages and amounts are uncalibrated estimates.",
			"Market depth is a current snapshot and does not guarantee execution; delivery proceeds require collection in game.",
		},
	}
	if independent {
		result.Warnings = append(result.Warnings, "Each batch row is estimated independently; repeated item rows do not reserve or consume depth from one another.")
	}
	var err error
	if in.Mode == "buy_order" || in.Mode == "sell_listing" {
		result, err = standingQuote(result, in.Quantity, in.UnitPrice)
	} else {
		book, found := books[in.ItemID]
		if !found {
			return quote{}, fmt.Errorf("market listing was not returned; item may be unavailable")
		}
		levels := book.Sells
		if in.Mode == "sell_instant" {
			levels = book.Buys
		}
		if levels == nil {
			return quote{}, fmt.Errorf("market listing response omitted depth")
		}
		result, err = depthQuote(result, *levels, in.Quantity, in.Mode == "sell_instant")
	}
	if err != nil {
		return quote{}, err
	}
	if in.Mode == "sell_instant" || in.Mode == "sell_listing" {
		return applySellerFees(result)
	}
	result.NetEstimate, result.Upfront = result.Gross, result.Gross
	return result, nil
}

func hasSaleBinding(item gw2.Item) bool {
	return slices.Contains(item.Flags, "AccountBound") || slices.Contains(item.Flags, "SoulbindOnAcquire")
}

func depthQuote(result quote, levels []depthLevel, quantity int64, bids bool) (quote, error) {
	sort.Slice(levels, func(i, j int) bool {
		if bids {
			return levels[i].UnitPrice > levels[j].UnitPrice
		}
		return levels[i].UnitPrice < levels[j].UnitPrice
	})
	remaining := quantity
	for _, level := range levels {
		if remaining == 0 {
			break
		}
		if level.Quantity <= 0 || level.UnitPrice <= 0 {
			continue
		}
		units := min(level.Quantity, remaining)
		amount, ok := checkedMul(units, level.UnitPrice)
		if !ok {
			return quote{}, fmt.Errorf("quote amount overflows int64")
		}
		result.Gross, ok = checkedAdd(result.Gross, amount)
		if !ok {
			return quote{}, fmt.Errorf("quote amount overflows int64")
		}
		result.Covered += units
		remaining -= units
	}
	result.Unfilled = remaining
	return result, nil
}

func standingQuote(result quote, quantity, price int64) (quote, error) {
	gross, ok := checkedMul(quantity, price)
	if !ok {
		return quote{}, fmt.Errorf("quote amount overflows int64")
	}
	result.Gross, result.Covered, result.Conditional = gross, quantity, true
	return result, nil
}

func applySellerFees(result quote) (quote, error) {
	if result.Gross < 0 {
		return quote{}, fmt.Errorf("gross amount must be nonnegative")
	}
	result.ListingFeeEstimate, result.ExchangeFeeEstimate = result.Gross/20, result.Gross/10
	result.NetEstimate = result.Gross - result.ListingFeeEstimate - result.ExchangeFeeEstimate
	result.DeliveryProceedsEstimate = result.Gross - result.ExchangeFeeEstimate
	result.Upfront = result.ListingFeeEstimate
	return result, nil
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
