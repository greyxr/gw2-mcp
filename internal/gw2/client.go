package gw2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.guildwars2.com"
const schemaVersion = "2025-08-29T01:00:00.000Z"
const maxResponseBytes = 4 << 20

type Pagination struct {
	PageSize    int `json:"page_size,omitempty"`
	PageTotal   int `json:"page_total,omitempty"`
	ResultCount int `json:"result_count,omitempty"`
	ResultTotal int `json:"result_total,omitempty"`
}

type APIResponse struct {
	Data       json.RawMessage
	ObservedAt time.Time
	Partial    bool
	Pagination *Pagination
}

type Client struct {
	key        string
	baseURL    string
	httpClient *http.Client
	retryDelay time.Duration
	mu         sync.Mutex
	items      map[int]Item
	currencies []Currency
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
		retryDelay: time.Second,
		items:      make(map[int]Item),
	}
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	response, err := c.request(ctx, path, false)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return c.safe(err)
	}
	return nil
}

func (c *Client) GetByIDs(ctx context.Context, route string, ids []int, out any) error {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	err := c.Get(ctx, route+"?ids="+strings.Join(parts, ","), out)
	if IsNoValidIDs(err) {
		return nil
	}
	return err
}

func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, error) {
	response, err := c.request(ctx, path, false)
	return response.Data, err
}

func (c *Client) ReadRoute(ctx context.Context, path string, authenticated bool) (APIResponse, error) {
	response, err := c.request(ctx, path, authenticated)
	if err == nil && !json.Valid(response.Data) {
		return APIResponse{}, fmt.Errorf("GW2 API returned invalid JSON")
	}
	return response, err
}

type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GW2 API returned HTTP %d: %s", e.Code, e.Message)
}

func IsNoValidIDs(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Code == http.StatusNotFound && strings.Contains(status.Message, "all ids provided are invalid")
}

func (c *Client) request(ctx context.Context, path string, authenticated bool) (APIResponse, error) {
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
	useKey := authenticated || privatePath(parsedPath.Path)
	if useKey && strings.TrimSpace(c.key) == "" {
		return APIResponse{}, fmt.Errorf("GW2 API key is required for this endpoint")
	}
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return APIResponse{}, c.safe(err)
		}
		req.Header.Set("Accept", "application/json")
		if useKey {
			req.Header.Set("Authorization", "Bearer "+c.key)
		}
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return APIResponse{}, c.safe(err)
		}
		if attempt > 0 || !transient(resp.StatusCode) {
			break
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return APIResponse{}, c.safe(ctx.Err())
		case <-time.After(c.retryDelay):
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var payload struct {
			Text string `json:"text"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &payload) == nil && payload.Text != "" {
			msg = payload.Text
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return APIResponse{}, &StatusError{Code: resp.StatusCode, Message: c.redact(msg)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return APIResponse{}, c.safe(err)
	}
	if len(body) > maxResponseBytes {
		return APIResponse{}, fmt.Errorf("GW2 API response exceeds %d bytes; use smaller pages", maxResponseBytes)
	}
	result := APIResponse{Data: body, ObservedAt: time.Now().UTC(), Partial: resp.StatusCode == http.StatusPartialContent}
	page := Pagination{
		PageSize: headerInt(resp, "X-Page-Size"), PageTotal: headerInt(resp, "X-Page-Total"),
		ResultCount: headerInt(resp, "X-Result-Count"), ResultTotal: headerInt(resp, "X-Result-Total"),
	}
	if page != (Pagination{}) {
		result.Pagination = &page
	}
	return result, nil
}

func transient(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func headerInt(resp *http.Response, name string) int {
	value, _ := strconv.Atoi(resp.Header.Get(name))
	return value
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
	Account     json.RawMessage `json:"account"`
	Permissions []string        `json:"permissions"`
	Wallet      []WalletEntry   `json:"wallet"`
}
type WalletEntry struct {
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"`
	Value int64  `json:"value"`
}
type Currency struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (c *Client) Overview(ctx context.Context) (Overview, error) {
	var result Overview
	var token struct {
		Permissions []string `json:"permissions"`
	}
	var currencies []Currency
	paths := []string{"v2/account", "v2/tokeninfo", "v2/account/wallet"}
	targets := []any{&result.Account, &token, &result.Wallet}
	errs := make([]error, len(paths)+1)
	Parallel(len(errs), func(i int) {
		if i == len(paths) {
			currencies, errs[i] = c.Currencies(ctx)
			return
		}
		errs[i] = c.Get(ctx, paths[i], targets[i])
	})
	for _, err := range errs[:len(paths)] {
		if err != nil {
			return Overview{}, err
		}
	}
	names := make(map[int]string, len(currencies))
	for _, currency := range currencies {
		names[currency.ID] = currency.Name
	}
	for i := range result.Wallet {
		result.Wallet[i].Name = names[result.Wallet[i].ID]
	}
	result.Permissions = token.Permissions
	if result.Wallet == nil {
		result.Wallet = []WalletEntry{}
	}
	return result, nil
}

func (c *Client) Currencies(ctx context.Context) ([]Currency, error) {
	c.mu.Lock()
	cached := c.currencies
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	var fetched []Currency
	if err := c.Get(ctx, "v2/currencies?ids=all", &fetched); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.currencies = fetched
	c.mu.Unlock()
	return fetched, nil
}

func Parallel(n int, run func(int)) {
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
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Rarity      string   `json:"rarity"`
	Level       int      `json:"level,omitempty"`
	VendorValue int64    `json:"vendor_value,omitempty"`
	Flags       []string `json:"flags,omitempty"`
}

func UniqueIDs(ids []int, field string) ([]int, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, fmt.Errorf("%s must contain 1 to 200 IDs", field)
	}
	unique := slices.Clone(ids)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	if unique[0] <= 0 {
		return nil, fmt.Errorf("%s must be positive", field)
	}
	return unique, nil
}

func (c *Client) Items(ctx context.Context, ids []int) ([]Item, error) {
	unique, err := UniqueIDs(ids, "item_ids")
	if err != nil {
		return nil, err
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
		var fetched []Item
		if err := c.GetByIDs(ctx, "v2/items", missing, &fetched); err != nil {
			return nil, err
		}
		c.mu.Lock()
		for _, item := range fetched {
			c.items[item.ID] = item
		}
		c.mu.Unlock()
	}
	out := make([]Item, 0, len(unique))
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range unique {
		if item, ok := c.items[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}
