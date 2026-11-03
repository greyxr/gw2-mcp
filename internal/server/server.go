package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gw2mcp/internal/gw2"
)

type output struct {
	Data any `json:"data"`
}

type apiClient interface {
	Overview(context.Context) (gw2.Overview, error)
	Characters(context.Context) ([]string, error)
	Currencies(context.Context, []int) ([]gw2.Currency, error)
	Items(context.Context, []int) ([]gw2.Item, error)
	FindItem(context.Context, int) (gw2.FindResult, error)
	FindItems(context.Context, []int) (gw2.FindItemsResult, error)
	CraftingForCharacter(context.Context, string) ([]gw2.CraftingDiscipline, error)
	Get(context.Context, string, any) error
	SearchRecipes(context.Context, int, int) ([]int, error)
	Recipes(context.Context, []int) ([]gw2.Recipe, error)
	AccountRecipes(context.Context) ([]int, error)
}

func New(api apiClient) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "gw2-mcp", Version: "0.2.0"}, nil)
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	registerWorkflowPrompt(server)
	registerAPITools(server, api)
	registerRecordTools(server)
	registerReferenceTool(server)
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "account_overview", Description: "Read account identity, permissions, and wallet."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, output, error) {
		value, err := api.Overview(ctx)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "list_characters", Description: "List account character names."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, output, error) {
		value, err := api.Characters(ctx)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_currencies", Description: "Get public currency metadata for 1-200 currency IDs."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		CurrencyIDs []int `json:"currency_ids"`
	}) (*mcp.CallToolResult, output, error) {
		if len(in.CurrencyIDs) < 1 || len(in.CurrencyIDs) > 200 {
			return nil, output{}, fmt.Errorf("currency_ids must contain between 1 and 200 IDs")
		}
		seen := make(map[int]struct{}, len(in.CurrencyIDs))
		for _, id := range in.CurrencyIDs {
			if id <= 0 {
				return nil, output{}, fmt.Errorf("currency IDs must be positive")
			}
			if _, exists := seen[id]; exists {
				return nil, output{}, fmt.Errorf("currency IDs must be unique")
			}
			seen[id] = struct{}{}
		}
		value, err := api.Currencies(ctx, in.CurrencyIDs)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_items", Description: "Get public item metadata for 1-200 item IDs."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (*mcp.CallToolResult, output, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, output{}, err
		}
		value, err := api.Items(ctx, in.ItemIDs)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "find_item", Description: "Find observed account locations for an item; results may be paginated."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ItemID int `json:"item_id"`
		Offset int `json:"offset,omitempty"`
		Limit  int `json:"limit,omitempty"`
	}) (*mcp.CallToolResult, output, error) {
		if in.ItemID <= 0 || in.Offset < 0 {
			return nil, output{}, fmt.Errorf("item_id must be positive and offset nonnegative")
		}
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 200 {
			return nil, output{}, fmt.Errorf("limit must be between 1 and 200")
		}
		value, err := api.FindItem(ctx, in.ItemID)
		if err != nil {
			return nil, output{}, err
		}
		locationCount := len(value.Locations)
		start := in.Offset
		if start > locationCount {
			start = locationCount
		}
		end := start + limit
		if end > locationCount {
			end = locationCount
		}
		value.Locations = value.Locations[start:end]
		return nil, output{map[string]any{
			"item_id": value.ItemID, "observed_total": value.Total, "complete": value.Complete,
			"sources": value.Sources, "locations": value.Locations, "offset": in.Offset,
			"limit": limit, "location_count": locationCount,
		}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "find_items", Description: "Find observed account locations for 1-200 item IDs with shared source coverage."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (*mcp.CallToolResult, output, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, output{}, err
		}
		value, err := api.FindItems(ctx, in.ItemIDs)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_character_crafting", Description: "Get crafting disciplines, ratings, and active status for one character."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		CharacterName string `json:"character_name"`
	}) (*mcp.CallToolResult, output, error) {
		if strings.TrimSpace(in.CharacterName) == "" {
			return nil, output{}, fmt.Errorf("character_name is required")
		}
		value, err := api.CraftingForCharacter(ctx, in.CharacterName)
		return nil, output{value}, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_market_prices", Description: "Read current market highest bids and lowest asks."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ItemIDs []int `json:"item_ids"`
	}) (*mcp.CallToolResult, output, error) {
		if err := validIDs(in.ItemIDs); err != nil {
			return nil, output{}, err
		}
		ids := make([]string, len(in.ItemIDs))
		for index, id := range in.ItemIDs {
			ids[index] = fmt.Sprint(id)
		}
		query := url.Values{}
		query.Set("ids", strings.Join(ids, ","))
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
		if err := api.Get(ctx, "/v2/commerce/prices?"+query.Encode(), &rows); err != nil {
			return nil, output{}, err
		}
		prices := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			prices = append(prices, map[string]any{"id": row.ID, "highest_bid": row.Buys, "lowest_ask": row.Sells})
		}
		return nil, output{prices}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "search_recipes", Description: "Find recipes by output item or ingredient item."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		OutputItemID     int `json:"output_item_id,omitempty"`
		IngredientItemID int `json:"ingredient_item_id,omitempty"`
	}) (*mcp.CallToolResult, output, error) {
		if (in.OutputItemID > 0) == (in.IngredientItemID > 0) || in.OutputItemID < 0 || in.IngredientItemID < 0 {
			return nil, output{}, fmt.Errorf("provide exactly one positive output_item_id or ingredient_item_id")
		}
		recipeIDs, err := api.SearchRecipes(ctx, in.OutputItemID, in.IngredientItemID)
		if err != nil {
			return nil, output{}, err
		}
		return nil, output{recipeIDs}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_recipes", Description: "Get recipe details and typed ingredients for 1-200 recipe IDs."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		RecipeIDs []int `json:"recipe_ids"`
	}) (*mcp.CallToolResult, output, error) {
		if len(in.RecipeIDs) < 1 || len(in.RecipeIDs) > 200 {
			return nil, output{}, fmt.Errorf("recipe_ids must contain between 1 and 200 IDs")
		}
		seen := make(map[int]struct{}, len(in.RecipeIDs))
		for _, id := range in.RecipeIDs {
			if id <= 0 {
				return nil, output{}, fmt.Errorf("recipe IDs must be positive")
			}
			if _, exists := seen[id]; exists {
				return nil, output{}, fmt.Errorf("recipe IDs must be unique")
			}
			seen[id] = struct{}{}
		}
		value, err := api.Recipes(ctx, in.RecipeIDs)
		if err != nil {
			return nil, output{}, err
		}
		return nil, output{value}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "get_account_recipes", Description: "List unlocked account recipe IDs (requires the unlocks permission; optional account-specific data). Unlocked recipes are not all recipe definitions and do not guarantee craftability."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, output, error) {
		recipeIDs, err := api.AccountRecipes(ctx)
		if err != nil {
			return nil, output{}, err
		}
		return nil, output{recipeIDs}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "quote_trade", Description: "Estimate market-depth trade costs or proceeds; this tool never places trades."}, quoteHandler(api))
	mcp.AddTool(server, &mcp.Tool{Annotations: readOnly, Name: "quote_trades", Description: "Quote 1-50 independent trade scenarios using shared item metadata and market depth. Repeated item entries reuse the same depth; combine quantities for a single route. This tool never trades."}, quoteTradesHandler(api))
	return server
}

func validIDs(ids []int) error {
	if len(ids) < 1 || len(ids) > 200 {
		return fmt.Errorf("item_ids must contain between 1 and 200 IDs")
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("item IDs must be positive")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("item IDs must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}
