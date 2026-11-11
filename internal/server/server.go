package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

const maxInlineRecipes = 30

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

type itemIDsInput struct {
	ItemIDs []int `json:"item_ids" jsonschema:"1-200 item IDs"`
}

func New(api *gw2.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gw2-mcp", Version: "0.3.0"}, nil)
	registerWorkflowPrompt(s)
	registerAPITools(s, api)
	registerRecordTools(s)
	registerReferenceTools(s)
	addRead(s, "account_overview", "Account details, API key permissions, and wallet with currency names (coin in copper).", func(ctx context.Context, _ struct{}) (any, error) { return api.Overview(ctx) })
	addRead(s, "list_characters", "List account character names.", func(ctx context.Context, _ struct{}) (any, error) { return api.Characters(ctx) })
	addRead(s, "get_character_crafting", "Crafting disciplines, ratings, and active status for one character.", func(ctx context.Context, in struct {
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
	addRead(s, "get_currencies", "Currency names and descriptions. Omit currency_ids for all currencies.", func(ctx context.Context, in struct {
		CurrencyIDs []int `json:"currency_ids,omitempty"`
	}) (any, error) {
		all, err := api.Currencies(ctx)
		if err != nil || len(in.CurrencyIDs) == 0 {
			return all, err
		}
		return slices.DeleteFunc(slices.Clone(all), func(c gw2.Currency) bool { return !slices.Contains(in.CurrencyIDs, c.ID) }), nil
	})
	addRead(s, "get_items", "Compact item metadata (name, description, type, rarity, level, flags, vendor_value). Unknown IDs are returned in missing. For full details use query_api with route items.", func(ctx context.Context, in itemIDsInput) (any, error) {
		ids, err := gw2.UniqueIDs(in.ItemIDs, "item_ids")
		if err != nil {
			return nil, err
		}
		items, err := api.Items(ctx, ids)
		if err != nil {
			return nil, err
		}
		found := make([]int, len(items))
		for i, item := range items {
			found[i] = item.ID
		}
		return withMissing("items", items, ids, found), nil
	})
	addRead(s, "find_items", "Count items across bank, material storage, shared inventory, and character bags. Equipped gear, guild storage, and Trading Post delivery are not searched. If complete is false, totals are lower bounds.", func(ctx context.Context, in itemIDsInput) (any, error) {
		return api.FindItems(ctx, in.ItemIDs)
	})
	addRead(s, "get_market_prices", "Trading Post best prices in copper: buy is the highest buy order, sell is the lowest sell listing; buy_qty and sell_qty are total units on each side. IDs with no Trading Post entry (bound or never listed) are returned in no_listings.", func(ctx context.Context, in itemIDsInput) (any, error) {
		ids, err := gw2.UniqueIDs(in.ItemIDs, "item_ids")
		if err != nil {
			return nil, err
		}
		type side struct {
			UnitPrice int64 `json:"unit_price"`
			Quantity  int64 `json:"quantity"`
		}
		var rows []struct {
			ID    int  `json:"id"`
			Buys  side `json:"buys"`
			Sells side `json:"sells"`
		}
		if err := api.GetByIDs(ctx, "/v2/commerce/prices", ids, &rows); err != nil {
			return nil, err
		}
		type price struct {
			ID      int   `json:"id"`
			Buy     int64 `json:"buy"`
			BuyQty  int64 `json:"buy_qty"`
			Sell    int64 `json:"sell"`
			SellQty int64 `json:"sell_qty"`
		}
		prices := make([]price, len(rows))
		found := make([]int, len(rows))
		for i, row := range rows {
			prices[i] = price{row.ID, row.Buys.UnitPrice, row.Buys.Quantity, row.Sells.UnitPrice, row.Sells.Quantity}
			found[i] = row.ID
		}
		result := map[string]any{"prices": prices}
		if missing := missingIDs(ids, found); len(missing) > 0 {
			result["no_listings"] = missing
		}
		return result, nil
	})
	addRead(s, "search_recipes", fmt.Sprintf("Find crafting recipes that produce or consume an item. Returns full recipes with item names, or only recipe_ids when more than %d match. Mystic Forge and vendor recipes are not in the API; use the wiki for those.", maxInlineRecipes), func(ctx context.Context, in struct {
		OutputItemID     int `json:"output_item_id,omitempty"`
		IngredientItemID int `json:"ingredient_item_id,omitempty"`
	}) (any, error) {
		if (in.OutputItemID > 0) == (in.IngredientItemID > 0) || in.OutputItemID < 0 || in.IngredientItemID < 0 {
			return nil, fmt.Errorf("provide exactly one positive output_item_id or ingredient_item_id")
		}
		query := "output=" + strconv.Itoa(in.OutputItemID)
		if in.IngredientItemID > 0 {
			query = "input=" + strconv.Itoa(in.IngredientItemID)
		}
		var ids []int
		if err := api.Get(ctx, "/v2/recipes/search?"+query, &ids); err != nil {
			return nil, err
		}
		if len(ids) == 0 || len(ids) > maxInlineRecipes {
			return map[string]any{"count": len(ids), "recipe_ids": ids}, nil
		}
		recipes, err := fetchRecipes(ctx, api, ids)
		return map[string]any{"count": len(ids), "recipes": recipes}, err
	})
	addRead(s, "get_recipes", "Recipe details with item names for 1-200 recipe IDs. Unknown IDs are returned in missing.", func(ctx context.Context, in struct {
		RecipeIDs []int `json:"recipe_ids"`
	}) (any, error) {
		ids, err := gw2.UniqueIDs(in.RecipeIDs, "recipe_ids")
		if err != nil {
			return nil, err
		}
		recipes, err := fetchRecipes(ctx, api, ids)
		if err != nil {
			return nil, err
		}
		found := make([]int, len(recipes))
		for i, recipe := range recipes {
			found[i] = recipe.ID
		}
		return withMissing("recipes", recipes, ids, found), nil
	})
	addRead(s, "get_account_recipes", "Check which of the given recipe IDs the account has unlocked (needs the unlocks permission). AutoLearned recipes need no unlock and appear in not_unlocked.", func(ctx context.Context, in struct {
		RecipeIDs []int `json:"recipe_ids"`
	}) (any, error) {
		ids, err := gw2.UniqueIDs(in.RecipeIDs, "recipe_ids")
		if err != nil {
			return nil, err
		}
		var unlocked []int
		if err := api.Get(ctx, "/v2/account/recipes", &unlocked); err != nil {
			return nil, err
		}
		return map[string]any{
			"unlocked":     slices.DeleteFunc(slices.Clone(ids), func(id int) bool { return !slices.Contains(unlocked, id) }),
			"not_unlocked": missingIDs(ids, unlocked),
		}, nil
	})
	addRead(s, "quote_trades", "Estimate 1-50 Trading Post trades in copper using current order-book depth; never places trades. Instant modes fill against listed depth and report unfilled units; buy_order and sell_listing need unit_price and assume the order fills. Sells show listing_fee (5%, paid upfront) and exchange_fee (10%, taken on delivery); net = gross - both fees; in-game rounding may differ by a copper. Rows are independent: combine quantities of the same item into one row.", quoteTradesHandler(api))
	return s
}

func addRead[I any](s *mcp.Server, name, description string, handler func(context.Context, I) (any, error)) {
	addTool(s, &mcp.Tool{Name: name, Description: description, Annotations: readOnly}, handler)
}

func addTool[I any](s *mcp.Server, tool *mcp.Tool, handler func(context.Context, I) (any, error)) {
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
		value, err := handler(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		text, ok := value.(string)
		if !ok {
			encoded, err := encodeJSON(value)
			if err != nil {
				return nil, nil, err
			}
			text = string(encoded)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
}

func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(value)
	return bytes.TrimSpace(buffer.Bytes()), err
}

func missingIDs(requested, found []int) []int {
	return slices.DeleteFunc(slices.Clone(requested), func(id int) bool { return slices.Contains(found, id) })
}

func withMissing(key string, rows any, requested, found []int) map[string]any {
	result := map[string]any{key: rows}
	if missing := missingIDs(requested, found); len(missing) > 0 {
		result["missing"] = missing
	}
	return result
}

type ingredient struct {
	Type  string `json:"type"`
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"`
	Count int    `json:"count"`
}

type recipe struct {
	ID               int               `json:"id"`
	Type             string            `json:"type"`
	OutputItemID     int               `json:"output_item_id"`
	OutputItemName   string            `json:"output_item_name,omitempty"`
	OutputItemCount  int               `json:"output_item_count"`
	MinRating        int               `json:"min_rating"`
	Disciplines      []string          `json:"disciplines"`
	Flags            []string          `json:"flags,omitempty"`
	Ingredients      []ingredient      `json:"ingredients"`
	GuildIngredients []json.RawMessage `json:"guild_ingredients,omitempty"`
}

func fetchRecipes(ctx context.Context, api *gw2.Client, ids []int) ([]recipe, error) {
	recipes := []recipe{}
	if err := api.GetByIDs(ctx, "/v2/recipes", ids, &recipes); err != nil {
		return nil, err
	}
	var itemIDs []int
	for _, r := range recipes {
		itemIDs = append(itemIDs, r.OutputItemID)
		for _, part := range r.Ingredients {
			if part.Type == "Item" {
				itemIDs = append(itemIDs, part.ID)
			}
		}
	}
	slices.Sort(itemIDs)
	names := map[int]string{}
	for chunk := range slices.Chunk(slices.Compact(itemIDs), 200) {
		items, err := api.Items(ctx, chunk)
		if err != nil {
			break
		}
		for _, item := range items {
			names[item.ID] = item.Name
		}
	}
	for i := range recipes {
		recipes[i].OutputItemName = names[recipes[i].OutputItemID]
		for j, part := range recipes[i].Ingredients {
			if part.Type == "Item" {
				recipes[i].Ingredients[j].Name = names[part.ID]
			}
		}
	}
	return recipes, nil
}
