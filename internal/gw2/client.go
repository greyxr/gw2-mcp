package gw2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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

func (c *Client) Overview(ctx context.Context) (Overview, error) {
	var result Overview
	var account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var token struct {
		Permissions []string `json:"permissions"`
	}
	paths := []string{"v2/account", "v2/tokeninfo", "v2/account/wallet"}
	targets := []any{&account, &token, &result.Wallet}
	errors := make([]error, len(paths))
	parallel(len(paths), func(i int) { errors[i] = c.Get(ctx, paths[i], targets[i]) })
	for _, err := range errors {
		if err != nil {
			return Overview{}, err
		}
	}
	result.ID, result.Name, result.Permissions = account.ID, account.Name, token.Permissions
	if result.Wallet == nil {
		result.Wallet = []WalletEntry{}
	}
	return result, nil
}

func parallel(n int, run func(int)) {
	jobs := make(chan int, n)
	for i := range n {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(n, 4) {
		wg.Go(func() {
			for i := range jobs {
				run(i)
			}
		})
	}
	wg.Wait()
}

func getJSON[T any](ctx context.Context, c *Client, path string) (T, error) {
	var result T
	err := c.Get(ctx, path, &result)
	return result, err
}

func (c *Client) Characters(ctx context.Context) ([]string, error) {
	return getJSON[[]string](ctx, c, "v2/characters")
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
	unique := slices.Clone(ids)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	if unique[0] <= 0 {
		return nil, fmt.Errorf("item IDs must be positive")
	}

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
			if _, requested := slices.BinarySearch(unique, item.ID); requested {
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
