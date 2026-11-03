package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func quoteHandler(api apiClient) func(context.Context, *mcp.CallToolRequest, quoteInput) (*mcp.CallToolResult, output, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in quoteInput) (*mcp.CallToolResult, output, error) {
		if err := validateQuoteInput(in); err != nil {
			return nil, output{}, err
		}
		items := make(map[int]gw2.Item)
		if in.Mode == "sell_listing" || in.Mode == "sell_instant" {
			metadata, err := api.Items(ctx, []int{in.ItemID})
			if err != nil {
				return nil, output{}, err
			}
			for _, item := range metadata {
				items[item.ID] = item
			}
		}
		books := make(map[int]listingBook)
		if in.Mode == "sell_instant" || in.Mode == "buy_instant" {
			var book listingBook
			path := "/v2/commerce/listings/" + strconv.Itoa(in.ItemID)
			if err := api.Get(ctx, path, &book); err != nil {
				return nil, output{}, err
			}
			if book.ID != in.ItemID {
				return nil, output{}, fmt.Errorf("market listing response item ID did not match request")
			}
			books[in.ItemID] = book
		}
		result, err := quoteFromSnapshot(in, items, books, time.Now().UTC().Format(time.RFC3339), false)
		if err != nil {
			return nil, output{}, err
		}
		return nil, output{result}, nil
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

func quoteTradesHandler(api apiClient) func(context.Context, *mcp.CallToolRequest, quoteTradesInput) (*mcp.CallToolResult, output, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in quoteTradesInput) (*mcp.CallToolResult, output, error) {
		if len(in.Trades) < 1 || len(in.Trades) > 50 {
			return nil, output{}, fmt.Errorf("trades must contain between 1 and 50 entries")
		}
		needsSaleCheck := make(map[int]bool)
		needsBook := make(map[int]bool)
		for _, trade := range in.Trades {
			if err := validateQuoteInput(trade); err != nil {
				return nil, output{}, err
			}
			if trade.Mode == "sell_instant" || trade.Mode == "sell_listing" {
				needsSaleCheck[trade.ItemID] = true
			}
			if trade.Mode == "sell_instant" || trade.Mode == "buy_instant" {
				needsBook[trade.ItemID] = true
			}
		}
		observedAt := time.Now().UTC()
		items := make(map[int]gw2.Item, len(needsSaleCheck))
		if len(needsSaleCheck) > 0 {
			ids := sortedItemIDs(needsSaleCheck)
			metadata, err := api.Items(ctx, ids)
			if err != nil {
				return nil, output{}, err
			}
			for _, item := range metadata {
				items[item.ID] = item
			}
		}
		books := make(map[int]listingBook, len(needsBook))
		partial := false
		statusCode := 0
		var pagination map[string]string
		requestPath := ""
		if len(needsBook) > 0 {
			ids := sortedItemIDs(needsBook)
			params := url.Values{}
			values := make([]string, len(ids))
			for i, id := range ids {
				values[i] = strconv.Itoa(id)
			}
			params.Set("ids", strings.Join(values, ","))
			path := "/v2/commerce/listings?" + params.Encode()
			requestPath = path
			var raw json.RawMessage
			if reader, ok := api.(interface {
				Read(context.Context, string) (gw2.APIResponse, error)
			}); ok {
				response, err := reader.Read(ctx, path)
				if err != nil {
					return nil, output{}, err
				}
				raw = response.Data
				partial = response.Partial
				statusCode = response.StatusCode
				pagination = response.Pagination
				if !response.ObservedAt.IsZero() {
					observedAt = response.ObservedAt
				}
			} else if err := api.Get(ctx, path, &raw); err != nil {
				return nil, output{}, err
			}
			var rows []listingBook
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nil, output{}, fmt.Errorf("invalid listings response: %w", err)
			}
			for _, book := range rows {
				books[book.ID] = book
			}
		}

		stamp := observedAt.UTC().Format(time.RFC3339)
		results := make([]quoteTradeResult, 0, len(in.Trades))
		for index, trade := range in.Trades {
			row := quoteTradeResult{Index: index, ItemID: trade.ItemID, Quantity: trade.Quantity, Mode: trade.Mode, Status: "ok"}
			value, err := quoteFromSnapshot(trade, items, books, stamp, true)
			if err != nil {
				row.Status, row.Error = "error", err.Error()
			} else {
				row.Quote = &value
			}
			results = append(results, row)
		}
		response := map[string]any{"results": results, "observed_at": stamp, "partial": partial, "pagination": pagination, "quotes_are_independent": true}
		if requestPath != "" {
			response["request"] = requestPath
		}
		if statusCode != 0 {
			response["status_code"] = statusCode
		}
		return nil, output{response}, nil
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
	if in.Mode == "buy_order" || in.Mode == "sell_listing" {
		var err error
		result, err = standingQuote(result, in.Quantity, in.UnitPrice)
		if err != nil {
			return quote{}, err
		}
		result.ObservedAt = observedAt
	} else {
		book, found := books[in.ItemID]
		if !found {
			return quote{}, fmt.Errorf("market listing was not returned; item may be unavailable")
		}
		var levels []depthLevel
		if in.Mode == "sell_instant" {
			if book.Buys == nil {
				return quote{}, fmt.Errorf("market listing response omitted buy depth")
			}
			levels = *book.Buys
		} else {
			if book.Sells == nil {
				return quote{}, fmt.Errorf("market listing response omitted sell depth")
			}
			levels = *book.Sells
		}
		var err error
		result, err = depthQuote(result, levels, in.Quantity, in.Mode == "sell_instant")
		if err != nil {
			return quote{}, err
		}
	}
	if in.Mode == "sell_instant" || in.Mode == "sell_listing" {
		var err error
		result, err = applySellerFees(result)
		if err != nil {
			return quote{}, err
		}
	} else {
		result.NetEstimate = result.Gross
		result.Upfront = result.Gross
	}
	result.ObservedAt = observedAt
	return result, nil
}

func hasSaleBinding(item gw2.Item) bool {
	for _, flag := range item.Flags {
		if flag == "AccountBound" || flag == "SoulbindOnAcquire" {
			return true
		}
	}
	return false
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
		units := level.Quantity
		if units > remaining {
			units = remaining
		}
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
	var ok bool
	result.ListingFeeEstimate, ok = percentFloor(result.Gross, 5)
	if !ok {
		return quote{}, fmt.Errorf("fee amount overflows int64")
	}
	result.ExchangeFeeEstimate, ok = percentFloor(result.Gross, 10)
	if !ok {
		return quote{}, fmt.Errorf("fee amount overflows int64")
	}
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

func percentFloor(value, percent int64) (int64, bool) {
	if value < 0 || percent < 0 {
		return 0, false
	}
	base, ok := checkedMul(value/100, percent)
	if !ok {
		return 0, false
	}
	extra, ok := checkedMul(value%100, percent)
	if !ok {
		return 0, false
	}
	return checkedAdd(base, extra/100)
}
