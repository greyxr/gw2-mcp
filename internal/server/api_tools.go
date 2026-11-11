package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type apiRoute struct {
	Route        string
	RequiresAuth bool
	LocaleAware  bool
	Disabled     bool
}

type apiQueryInput struct {
	Route      string            `json:"route" jsonschema:"route from list_api_endpoints, e.g. characters/:id/equipment"`
	PathParams map[string]string `json:"path_params,omitempty" jsonschema:"values for :name route segments"`
	IDs        []string          `json:"ids,omitempty" jsonschema:"up to 200 IDs, or [\"all\"] where the endpoint allows it"`
	Page       *int              `json:"page,omitempty"`
	PageSize   *int              `json:"page_size,omitempty" jsonschema:"1-200"`
	Lang       string            `json:"lang,omitempty" jsonschema:"en, de, es, fr, or zh"`
	Filters    map[string]string `json:"filters,omitempty" jsonschema:"route-specific query parameters"`
	Fields     []string          `json:"fields,omitempty" jsonschema:"keep only these top-level keys of each returned object"`
	Offset     int               `json:"offset,omitempty" jsonschema:"skip this many elements of the returned array (next_offset of a cut result); unlike page, it slices one API response"`
}

type apiQueryResult struct {
	Request    string          `json:"request"`
	ObservedAt string          `json:"observed_at"`
	Data       any             `json:"data"`
	Partial    bool            `json:"partial,omitempty"`
	Pagination *gw2.Pagination `json:"pagination,omitempty"`
	Total      int             `json:"total,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type apiReader interface {
	GetRaw(context.Context, string) ([]byte, error)
	ReadRoute(context.Context, string, bool) (gw2.APIResponse, error)
}

const maxDataChars = 24000
const maxObjectChars = 4 * maxDataChars
const catalogTTL = time.Hour

type apiCatalog struct {
	api     apiReader
	mu      sync.Mutex
	routes  []apiRoute
	fetched time.Time
}

func (c *apiCatalog) load(ctx context.Context) ([]apiRoute, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routes != nil && time.Since(c.fetched) < catalogTTL {
		return c.routes, nil
	}
	routes, err := discoverRoutes(ctx, c.api)
	if err != nil {
		return nil, err
	}
	c.routes, c.fetched = routes, time.Now()
	return routes, nil
}

func registerAPITools(server *mcp.Server, api apiReader) {
	catalog := &apiCatalog{api: api}
	addRead(server, "list_api_endpoints", "List official GW2 API v2 routes for query_api, one per line. [a] needs the API key, [l] accepts lang. Pass prefix (e.g. account, commerce, wvw) to narrow the list.", func(ctx context.Context, in struct {
		Prefix string `json:"prefix,omitempty"`
	}) (any, error) {
		routes, err := catalog.load(ctx)
		if err != nil {
			return nil, err
		}
		prefix := normalizeAPIRoute(in.Prefix)
		var lines []string
		for _, route := range routes {
			if route.Disabled || !strings.HasPrefix(route.Route, prefix) {
				continue
			}
			line := route.Route
			if route.RequiresAuth {
				line += " [a]"
			}
			if route.LocaleAware {
				line += " [l]"
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return nil, fmt.Errorf("no enabled routes match prefix %q", in.Prefix)
		}
		return strings.Join(lines, "\n"), nil
	})
	addRead(server, "query_api", fmt.Sprintf("Read any GW2 API v2 route from list_api_endpoints. A route without ids or page usually returns its ID list. Use fields to keep only needed keys. Arrays are cut near %d characters and continue from next_offset; partial means some requested ids do not exist. Filters: quantity (commerce/exchange/*), input or output (recipes/search), name (guild/search), world (wvw/matches*), since (guild/:id/log).", maxDataChars), func(ctx context.Context, in apiQueryInput) (any, error) {
		routes, err := catalog.load(ctx)
		if err != nil {
			return nil, err
		}
		route := normalizeAPIRoute(in.Route)
		index := slices.IndexFunc(routes, func(r apiRoute) bool { return r.Route == route })
		if index < 0 {
			return nil, fmt.Errorf("route must exactly match a route returned by list_api_endpoints")
		}
		selected := routes[index]
		if selected.Disabled {
			return nil, fmt.Errorf("route is currently marked disabled by GW2 API discovery")
		}
		if in.Offset < 0 {
			return nil, fmt.Errorf("offset must be nonnegative")
		}
		path, err := substituteRoute(route, in.PathParams)
		if err != nil {
			return nil, err
		}
		query, err := validateAPIQuery(selected, in)
		if err != nil {
			return nil, err
		}
		path = "/v2/" + path
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
		response, err := api.ReadRoute(ctx, path, selected.RequiresAuth)
		if gw2.IsNoValidIDs(err) && len(in.IDs) > 0 {
			response, err = gw2.APIResponse{Data: json.RawMessage("[]"), ObservedAt: time.Now().UTC(), Partial: true}, nil
		}
		if err != nil {
			return nil, err
		}
		result := apiQueryResult{Request: path, ObservedAt: response.ObservedAt.Format(time.RFC3339), Partial: response.Partial}
		if len(in.IDs) == 0 {
			result.Pagination = response.Pagination
		}
		result.Data, result.Total, result.NextOffset, err = shapeData(response.Data, in.Fields, in.Offset)
		return result, err
	})
}

func shapeData(data json.RawMessage, fields []string, offset int) (any, int, int, error) {
	var rows []json.RawMessage
	if json.Unmarshal(data, &rows) != nil {
		data = selectFields(data, fields)
		if len(data) > maxObjectChars {
			return nil, 0, 0, fmt.Errorf("response is %d characters; request fewer fields or a narrower route", len(data))
		}
		return data, 0, 0, nil
	}
	if offset > len(rows) {
		return nil, 0, 0, fmt.Errorf("offset exceeds the %d returned elements", len(rows))
	}
	kept, size := []json.RawMessage{}, 0
	for _, row := range rows[offset:] {
		var compact bytes.Buffer
		if err := json.Compact(&compact, selectFields(row, fields)); err != nil {
			return nil, 0, 0, err
		}
		if compact.Len() > maxObjectChars {
			return nil, 0, 0, fmt.Errorf("one element is %d characters; request fewer fields or a narrower route", compact.Len())
		}
		if size += compact.Len() + 1; size > maxDataChars && len(kept) > 0 {
			return kept, len(rows), offset + len(kept), nil
		}
		kept = append(kept, compact.Bytes())
	}
	if offset > 0 {
		return kept, len(rows), 0, nil
	}
	return kept, 0, 0, nil
}

func selectFields(data json.RawMessage, fields []string) json.RawMessage {
	var object map[string]json.RawMessage
	if len(fields) == 0 || json.Unmarshal(data, &object) != nil || object == nil {
		return data
	}
	maps.DeleteFunc(object, func(key string, _ json.RawMessage) bool { return !slices.Contains(fields, key) })
	selected, _ := encodeJSON(object)
	return selected
}

func discoverRoutes(ctx context.Context, api apiReader) ([]apiRoute, error) {
	catalog, err := api.GetRaw(ctx, "/v2")
	if err != nil {
		return nil, err
	}
	routes := make(map[string]apiRoute)
	add := func(route apiRoute) {
		current := routes[route.Route]
		current.Route = route.Route
		current.RequiresAuth = current.RequiresAuth || route.RequiresAuth
		current.LocaleAware = current.LocaleAware || route.LocaleAware
		current.Disabled = current.Disabled || route.Disabled
		routes[route.Route] = current
	}
	for _, line := range strings.Split(string(catalog), "\n") {
		if route, ok := parseDiscoveryLine(line); ok && route.Route != "createsubtoken" {
			add(route)
		}
	}
	for parent, children := range map[string][]string{
		"commerce/exchange":     {"coins", "gems"},
		"commerce/transactions": {"current/buys", "current/sells", "history/buys", "history/sells"},
		"emblem":                {"foregrounds", "backgrounds"},
	} {
		if route, exists := routes[parent]; exists {
			for _, child := range children {
				add(apiRoute{Route: parent + "/" + child, RequiresAuth: route.RequiresAuth})
			}
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("GW2 API discovery response contained no valid routes")
	}
	result := make([]apiRoute, 0, len(routes))
	for _, route := range routes {
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Route < result[j].Route })
	return result, nil
}

func normalizeAPIRoute(route string) string {
	route = strings.TrimSpace(route)
	route = strings.TrimPrefix(route, "/")
	return strings.TrimPrefix(route, "v2/")
}

var discoveryRoutePattern = regexp.MustCompile(`^[A-Za-z0-9_:-]+(?:/[A-Za-z0-9_:-]+)*$`)

func parseDiscoveryLine(line string) (apiRoute, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/v2/") {
		return apiRoute{}, false
	}
	route := strings.TrimPrefix(fields[0], "/v2/")
	if !discoveryRoutePattern.MatchString(route) {
		return apiRoute{}, false
	}
	entry := apiRoute{Route: route}
	for _, flag := range fields[1:] {
		for _, tag := range strings.Split(strings.Trim(flag, "[]"), ",") {
			switch tag {
			case "a":
				entry.RequiresAuth = true
			case "l":
				entry.LocaleAware = true
			case "d":
				entry.Disabled = true
			}
		}
	}
	return entry, true
}

func substituteRoute(route string, params map[string]string) (string, error) {
	segments := strings.Split(route, "/")
	used := make(map[string]bool, len(params))
	for i, segment := range segments {
		if !strings.HasPrefix(segment, ":") {
			continue
		}
		name := strings.TrimPrefix(segment, ":")
		value, ok := params[name]
		if !ok || strings.TrimSpace(value) == "" || len(value) > 128 || value == "." || value == ".." || strings.ContainsAny(value, "/?#\\%") {
			return "", fmt.Errorf("path_params.%s is required and must be one path segment", name)
		}
		segments[i] = url.PathEscape(value)
		used[name] = true
	}
	for name := range params {
		if !used[name] {
			return "", fmt.Errorf("path_params contains unsupported parameter %q", name)
		}
	}
	return strings.Join(segments, "/"), nil
}

func validateAPIQuery(route apiRoute, in apiQueryInput) (url.Values, error) {
	query := url.Values{}
	if len(in.IDs) > 200 {
		return nil, fmt.Errorf("ids must contain at most 200 values")
	}
	if len(in.IDs) > 0 {
		seen := make(map[string]struct{}, len(in.IDs))
		for _, id := range in.IDs {
			if id == "" || len(id) > 128 || id != strings.TrimSpace(id) || strings.ContainsAny(id, ",/?#\\%") {
				return nil, fmt.Errorf("ids must be nonempty individual IDs, one per array element")
			}
			if _, exists := seen[id]; exists {
				return nil, fmt.Errorf("ids must be unique")
			}
			seen[id] = struct{}{}
		}
		query.Set("ids", strings.Join(in.IDs, ","))
	}
	if in.Page != nil {
		if *in.Page < 0 || *in.Page > 100000 {
			return nil, fmt.Errorf("page must be between 0 and 100000")
		}
		query.Set("page", fmt.Sprint(*in.Page))
	}
	if in.PageSize != nil {
		if *in.PageSize < 1 || *in.PageSize > 200 {
			return nil, fmt.Errorf("page_size must be between 1 and 200")
		}
		query.Set("page_size", fmt.Sprint(*in.PageSize))
	}
	if len(in.IDs) > 0 && in.Page != nil {
		return nil, fmt.Errorf("ids and page cannot be used together")
	}
	if in.Lang != "" {
		if !route.LocaleAware {
			return nil, fmt.Errorf("lang is only valid on locale-aware routes")
		}
		switch in.Lang {
		case "en", "de", "es", "fr", "zh":
		default:
			return nil, fmt.Errorf("lang must be en, de, es, fr, or zh")
		}
		query.Set("lang", in.Lang)
	}
	for key, value := range in.Filters {
		if err := validateFilter(route.Route, key, value); err != nil {
			return nil, err
		}
		query.Set(key, value)
	}
	if route.Route == "guild/search" && query.Get("name") == "" {
		return nil, fmt.Errorf("guild/search requires filters.name")
	}
	if route.Route == "recipes/search" && (query.Get("input") == "") == (query.Get("output") == "") {
		return nil, fmt.Errorf("recipes/search requires exactly one of filters.input or filters.output")
	}
	if route.Route == "commerce/exchange/coins" || route.Route == "commerce/exchange/gems" {
		if query.Get("quantity") == "" {
			return nil, fmt.Errorf("exchange routes require filters.quantity")
		}
	}
	return query, nil
}

func validateFilter(route, key, value string) error {
	if value == "" || len(value) > 256 {
		return fmt.Errorf("filter %q must be between 1 and 256 characters", key)
	}
	if key == "name" && route == "guild/search" {
		return nil
	}
	var min, max int64
	switch {
	case key == "world" && (route == "wvw/matches" || strings.HasPrefix(route, "wvw/matches/")):
		min, max = 1, int64(^uint64(0)>>1)
	case key == "quantity" && (route == "commerce/exchange/coins" || route == "commerce/exchange/gems"):
		min, max = 1, 1_000_000_000_000
	case (key == "input" || key == "output") && route == "recipes/search":
		min, max = 1, int64(^uint64(0)>>1)
	case key == "since" && route == "guild/:id/log":
		min, max = 0, int64(^uint64(0)>>1)
	default:
		return fmt.Errorf("filter %q is not supported for route %q", key, route)
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < min || number > max {
		return fmt.Errorf("filter %q must be a numeric value between %d and %d", key, min, max)
	}
	return nil
}
