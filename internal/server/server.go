package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type output struct {
	Data any `json:"data"`
}

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

func New(api *gw2.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gw2-mcp", Version: "0.2.0"}, nil)
	registerWorkflowPrompt(s)
	registerAPITools(s, api)
	registerRecordTools(s)
	registerReferenceTool(s)
	addRead(s, "account_overview", "Read account identity, permissions, and wallet.", func(ctx context.Context, _ struct{}) (any, error) { return api.Overview(ctx) })
	addRead(s, "list_characters", "List account character names.", func(ctx context.Context, _ struct{}) (any, error) { return api.Characters(ctx) })
	addRead(s, "get_currencies", "Get public currency metadata for 1-200 currency IDs.", func(ctx context.Context, in struct {
		CurrencyIDs []int `json:"currency_ids"`
	}) (any, error) {
		if err := validIDList(in.CurrencyIDs, "currency_ids", "currency IDs"); err != nil {
			return nil, err
		}
		return fetchData(ctx, api, "/v2/currencies?ids="+joinIDs(in.CurrencyIDs))
	})
	addRead(s, "get_items", "Get public item metadata for 1-200 item IDs.", func(ctx context.Context, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (any, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, err
		}
		return api.Items(ctx, in.ItemIDs)
	})
	addRead(s, "find_item", "Find observed account locations for an item; results may be paginated.", func(ctx context.Context, in struct {
		ItemID int `json:"item_id"`
		Offset int `json:"offset,omitempty"`
		Limit  int `json:"limit,omitempty"`
	}) (any, error) {
		if in.ItemID <= 0 || in.Offset < 0 {
			return nil, fmt.Errorf("item_id must be positive and offset nonnegative")
		}
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 200 {
			return nil, fmt.Errorf("limit must be between 1 and 200")
		}
		value, err := api.FindItem(ctx, in.ItemID)
		if err != nil {
			return nil, err
		}
		count, start := len(value.Locations), in.Offset
		if start > count {
			start = count
		}
		end := min(start+limit, count)
		value.Locations = value.Locations[start:end]
		return map[string]any{"item_id": value.ItemID, "observed_total": value.Total, "complete": value.Complete, "sources": value.Sources, "locations": value.Locations, "offset": in.Offset, "limit": limit, "location_count": count}, nil
	})
	addRead(s, "find_items", "Find observed account locations for 1-200 item IDs with shared source coverage.", func(ctx context.Context, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (any, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, err
		}
		return api.FindItems(ctx, in.ItemIDs)
	})
	addRead(s, "get_character_crafting", "Get crafting disciplines, ratings, and active status for one character.", func(ctx context.Context, in struct {
		CharacterName string `json:"character_name"`
	}) (any, error) {
		if strings.TrimSpace(in.CharacterName) == "" {
			return nil, fmt.Errorf("character_name is required")
		}
		var body struct {
			Crafting json.RawMessage `json:"crafting"`
		}
		err := api.Get(ctx, "/v2/characters/"+url.PathEscape(in.CharacterName)+"/crafting", &body)
		return body.Crafting, err
	})
	addRead(s, "get_market_prices", "Read current market highest bids and lowest asks.", marketPricesHandler(api))
	addRead(s, "search_recipes", "Find recipes by output item or ingredient item.", func(ctx context.Context, in struct {
		OutputItemID     int `json:"output_item_id,omitempty"`
		IngredientItemID int `json:"ingredient_item_id,omitempty"`
	}) (any, error) {
		if (in.OutputItemID > 0) == (in.IngredientItemID > 0) || in.OutputItemID < 0 || in.IngredientItemID < 0 {
			return nil, fmt.Errorf("provide exactly one positive output_item_id or ingredient_item_id")
		}
		query := url.Values{}
		if in.OutputItemID > 0 {
			query.Set("output", strconv.Itoa(in.OutputItemID))
		} else {
			query.Set("input", strconv.Itoa(in.IngredientItemID))
		}
		return fetchData(ctx, api, "/v2/recipes/search?"+query.Encode())
	})
	addRead(s, "get_recipes", "Get recipe details and typed ingredients for 1-200 recipe IDs.", func(ctx context.Context, in struct {
		RecipeIDs []int `json:"recipe_ids"`
	}) (any, error) {
		if err := validIDList(in.RecipeIDs, "recipe_ids", "recipe IDs"); err != nil {
			return nil, err
		}
		return fetchData(ctx, api, "/v2/recipes?ids="+joinIDs(in.RecipeIDs))
	})
	addRead(s, "get_account_recipes", "List unlocked account recipe IDs (requires the unlocks permission; optional account-specific data). Unlocked recipes are not all recipe definitions and do not guarantee craftability.", func(ctx context.Context, _ struct{}) (any, error) { return fetchData(ctx, api, "/v2/account/recipes") })
	addRead(s, "quote_trade", "Estimate market-depth trade costs or proceeds; this tool never places trades.", quoteHandler(api))
	addRead(s, "quote_trades", "Quote 1-50 independent trade scenarios using shared item metadata and market depth. Repeated item entries reuse the same depth; combine quantities for a single route. This tool never trades.", quoteTradesHandler(api))
	return s
}

func addRead[I any](s *mcp.Server, name, description string, handler func(context.Context, I) (any, error)) {
	addTool(s, &mcp.Tool{Name: name, Description: description, Annotations: readOnly}, handler)
}

func addTool[I any](s *mcp.Server, tool *mcp.Tool, handler func(context.Context, I) (any, error)) {
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, output, error) {
		value, err := handler(ctx, in)
		return nil, output{value}, err
	})
}

func marketPricesHandler(api *gw2.Client) func(context.Context, struct {
	ItemIDs []int `json:"item_ids"`
}) (any, error) {
	return func(ctx context.Context, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (any, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, err
		}
		var rows []struct {
			ID   int `json:"id"`
			Buys struct {
				UnitPrice int64 `json:"unit_price"`
				Quantity  int64 `json:"quantity"`
			} `json:"buys"`
			Sells struct {
				UnitPrice int64 `json:"unit_price"`
				Quantity  int64 `json:"quantity"`
			} `json:"sells"`
		}
		if err := api.Get(ctx, "/v2/commerce/prices?"+url.Values{"ids": {joinIDs(in.ItemIDs)}}.Encode(), &rows); err != nil {
			return nil, err
		}
		prices := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			prices = append(prices, map[string]any{"id": row.ID, "highest_bid": row.Buys, "lowest_ask": row.Sells})
		}
		return prices, nil
	}
}

func validIDs(ids []int) error { return validIDList(ids, "item_ids", "item IDs") }

func validIDList(ids []int, field, label string) error {
	if len(ids) < 1 || len(ids) > 200 {
		return fmt.Errorf("%s must contain between 1 and 200 IDs", field)
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("%s must be positive", label)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%s must be unique", label)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func fetchData(ctx context.Context, api *gw2.Client, path string) (any, error) {
	var data json.RawMessage
	err := api.Get(ctx, path, &data)
	return data, err
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
