package gw2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.guildwars2.com"
const schemaVersion = "2025-08-29T01:00:00.000Z"
const maxResponseBytes = 4 << 20

type APIResponse struct {
	Data       json.RawMessage   `json:"data"`
	Endpoint   string            `json:"endpoint"`
	ObservedAt time.Time         `json:"observed_at"`
	StatusCode int               `json:"status_code"`
	Partial    bool              `json:"partial"`
	Pagination map[string]string `json:"pagination,omitempty"`
}

type Client struct {
	key        string
	baseURL    string
	httpClient *http.Client
	mu         sync.Mutex
	items      map[int]Item
}

func NewClient(key string) *Client {
	return &Client{
		key:     key,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		items: make(map[int]Item),
	}
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	response, err := c.request(ctx, path, false, false)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return c.safe(err)
	}
	return nil
}

func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, error) {
	response, err := c.request(ctx, path, false, false)
	return response.Data, err
}

func (c *Client) Read(ctx context.Context, path string) (APIResponse, error) {
	return c.ReadRoute(ctx, path, false)
}

func (c *Client) ReadRoute(ctx context.Context, path string, authenticated bool) (APIResponse, error) {
	response, err := c.request(ctx, path, true, authenticated)
	if err == nil && !json.Valid(response.Data) {
		return APIResponse{}, fmt.Errorf("GW2 API returned invalid JSON")
	}
	return response, err
}

func (c *Client) request(ctx context.Context, path string, allowPartial, authenticated bool) (APIResponse, error) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return APIResponse{}, fmt.Errorf("invalid API path")
	}
	parsedPath, err := url.Parse(path)
	if err != nil || parsedPath.IsAbs() || parsedPath.Host != "" {
		return APIResponse{}, fmt.Errorf("invalid API path")
	}
	if parsedPath.Fragment != "" || parsedPath.User != nil || parsedPath.Query().Has("access_token") {
		return APIResponse{}, fmt.Errorf("invalid API path")
	}
	for _, segment := range strings.Split(parsedPath.Path, "/") {
		if segment == "." || segment == ".." {
			return APIResponse{}, fmt.Errorf("invalid API path")
		}
	}
	if parsedPath.Query().Get("v") == "" {
		query := parsedPath.Query()
		query.Set("v", schemaVersion)
		parsedPath.RawQuery = query.Encode()
	}
	u, err := url.Parse(c.baseURL + "/" + parsedPath.String())
	if err != nil || u.Host == "" {
		return APIResponse{}, fmt.Errorf("invalid API path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return APIResponse{}, c.safe(err)
	}
	req.Header.Set("Accept", "application/json")
	if authenticated || privatePath(parsedPath.Path) {
		if strings.TrimSpace(c.key) == "" {
			return APIResponse{}, fmt.Errorf("GW2 API key is required for this endpoint")
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return APIResponse{}, c.safe(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(allowPartial && resp.StatusCode == http.StatusPartialContent) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return APIResponse{}, fmt.Errorf("GW2 API returned HTTP %d: %s", resp.StatusCode, c.redact(msg))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return APIResponse{}, c.safe(err)
	}
	if len(body) > maxResponseBytes {
		return APIResponse{}, fmt.Errorf("GW2 API response exceeds %d bytes; use smaller pages", maxResponseBytes)
	}
	result := APIResponse{Data: body, Endpoint: "/" + parsedPath.EscapedPath(), ObservedAt: time.Now().UTC(), StatusCode: resp.StatusCode, Partial: resp.StatusCode == http.StatusPartialContent}
	for _, name := range []string{"X-Page-Size", "X-Page-Total", "X-Result-Count", "X-Result-Total", "Link"} {
		if value := resp.Header.Get(name); value != "" {
			if result.Pagination == nil {
				result.Pagination = make(map[string]string)
			}
			result.Pagination[name] = value
		}
	}
	return result, nil
}

func privatePath(path string) bool {
	path = strings.TrimPrefix(path, "/")
	for _, prefix := range []string{"v2/account", "v2/characters", "v2/tokeninfo", "v2/commerce/delivery", "v2/commerce/transactions", "v2/pvp/games", "v2/pvp/standings", "v2/pvp/stats"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	if strings.HasPrefix(path, "v2/guild/") {
		part := strings.Split(strings.TrimPrefix(path, "v2/guild/"), "/")[0]
		return part != "permissions" && part != "search" && part != "upgrades"
	}
	return false
}
func (c *Client) redact(s string) string {
	if c.key != "" {
		s = strings.ReplaceAll(s, c.key, "[REDACTED]")
	}
	return s
}
func (c *Client) safe(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("GW2 API request failed: %s", c.redact(err.Error()))
}

type Overview struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Permissions []string      `json:"permissions"`
	Wallet      []WalletEntry `json:"wallet"`
}
type WalletEntry struct {
	ID    int   `json:"id"`
	Value int64 `json:"value"`
}
type accountResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type walletResponse struct {
	ID    int   `json:"id"`
	Value int64 `json:"value"`
}
type tokenInfoResponse struct {
	Permissions []string `json:"permissions"`
}

func (c *Client) Overview(ctx context.Context) (Overview, error) {
	var a accountResponse
	var tokenInfo tokenInfoResponse
	var wallet []walletResponse
	var accountErr, tokenInfoErr, walletErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		accountErr = c.Get(ctx, "v2/account", &a)
	}()
	go func() {
		defer wg.Done()
		tokenInfoErr = c.Get(ctx, "v2/tokeninfo", &tokenInfo)
	}()
	go func() {
		defer wg.Done()
		walletErr = c.Get(ctx, "v2/account/wallet", &wallet)
	}()
	wg.Wait()
	if accountErr != nil {
		return Overview{}, accountErr
	}
	if tokenInfoErr != nil {
		return Overview{}, tokenInfoErr
	}
	if walletErr != nil {
		return Overview{}, walletErr
	}
	o := Overview{ID: a.ID, Name: a.Name, Permissions: tokenInfo.Permissions, Wallet: make([]WalletEntry, len(wallet))}
	for i, v := range wallet {
		o.Wallet[i] = WalletEntry{ID: v.ID, Value: v.Value}
	}
	return o, nil
}
func (c *Client) Characters(ctx context.Context) ([]string, error) {
	var names []string
	if err := c.Get(ctx, "v2/characters", &names); err != nil {
		return nil, err
	}
	return names, nil
}

type CraftingDiscipline struct {
	Discipline string `json:"discipline"`
	Rating     int    `json:"rating"`
	Active     bool   `json:"active"`
}

func (c *Client) CraftingForCharacter(ctx context.Context, name string) ([]CraftingDiscipline, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("character_name is required")
	}
	var response struct {
		Crafting []CraftingDiscipline `json:"crafting"`
	}
	path := "v2/characters/" + url.PathEscape(name) + "/crafting"
	if err := c.Get(ctx, path, &response); err != nil {
		return nil, err
	}
	return response.Crafting, nil
}

type Item struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Rarity      string   `json:"rarity"`
	Flags       []string `json:"flags"`
}

func (c *Client) Items(ctx context.Context, ids []int) ([]Item, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, fmt.Errorf("item_ids must contain 1 to 200 IDs")
	}
	unique := make([]int, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("item IDs must be positive")
		}
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	sort.Ints(unique)

	c.mu.Lock()
	missing := make([]int, 0, len(unique))
	for _, id := range unique {
		if _, ok := c.items[id]; !ok {
			missing = append(missing, id)
		}
	}
	c.mu.Unlock()
	if len(missing) > 0 {
		parts := make([]string, len(missing))
		for i, id := range missing {
			parts[i] = fmt.Sprint(id)
		}
		var fetched []Item
		if err := c.Get(ctx, "v2/items?ids="+strings.Join(parts, ","), &fetched); err != nil {
			return nil, err
		}
		c.mu.Lock()
		for _, item := range fetched {
			if seen[item.ID] {
				c.items[item.ID] = item
			}
		}
		c.mu.Unlock()
	}

	out := make([]Item, 0, len(unique))
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range unique {
		item, ok := c.items[id]
		if !ok {
			return nil, fmt.Errorf("item %d was not returned by the API", id)
		}
		out = append(out, item)
	}
	return out, nil
}
func (c *Client) SearchRecipes(ctx context.Context, outputItemID, ingredientItemID int) ([]int, error) {
	query := url.Values{}
	if outputItemID > 0 {
		query.Set("output", fmt.Sprint(outputItemID))
	} else {
		query.Set("input", fmt.Sprint(ingredientItemID))
	}
	var recipeIDs []int
	if err := c.Get(ctx, "v2/recipes/search?"+query.Encode(), &recipeIDs); err != nil {
		return nil, err
	}
	return recipeIDs, nil
}

type RecipeIngredient struct {
	Type  string `json:"type"`
	ID    int    `json:"id"`
	Count int    `json:"count"`
}

type Recipe struct {
	ID              int                `json:"id"`
	Type            string             `json:"type"`
	OutputItemID    int                `json:"output_item_id"`
	OutputItemCount int                `json:"output_item_count"`
	Disciplines     []string           `json:"disciplines"`
	MinRating       int                `json:"min_rating"`
	Flags           []string           `json:"flags"`
	Ingredients     []RecipeIngredient `json:"ingredients"`
}

func (c *Client) Recipes(ctx context.Context, ids []int) ([]Recipe, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, fmt.Errorf("recipe_ids must contain 1 to 200 IDs")
	}
	parts := make([]string, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("recipe IDs must be positive")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("recipe IDs must be unique")
		}
		seen[id] = struct{}{}
		parts[i] = fmt.Sprint(id)
	}
	var recipes []Recipe
	if err := c.Get(ctx, "v2/recipes?ids="+strings.Join(parts, ","), &recipes); err != nil {
		return nil, err
	}
	return recipes, nil
}

type Currency struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

func (c *Client) Currencies(ctx context.Context, ids []int) ([]Currency, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, fmt.Errorf("currency_ids must contain 1 to 200 IDs")
	}
	parts := make([]string, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("currency IDs must be positive")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("currency IDs must be unique")
		}
		seen[id] = struct{}{}
		parts[i] = fmt.Sprint(id)
	}
	var currencies []Currency
	if err := c.Get(ctx, "v2/currencies?ids="+strings.Join(parts, ","), &currencies); err != nil {
		return nil, err
	}
	return currencies, nil
}
func (c *Client) AccountRecipes(ctx context.Context) ([]int, error) {
	var recipeIDs []int
	if err := c.Get(ctx, "v2/account/recipes", &recipeIDs); err != nil {
		return nil, err
	}
	return recipeIDs, nil
}
