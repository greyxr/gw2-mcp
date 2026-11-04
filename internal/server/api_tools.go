package server

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gw2mcp/internal/gw2"
)

type apiRoute struct {
	Route             string `json:"route"`
	RequiresAuth      bool   `json:"requires_auth,omitempty"`
	LocaleAware       bool   `json:"locale_aware,omitempty"`
	CurrentlyDisabled bool   `json:"currently_disabled,omitempty"`
}

type apiQueryInput struct {
	Route      string            `json:"route"`
	PathParams map[string]string `json:"path_params,omitempty"`
	IDs        []string          `json:"ids,omitempty"`
	Page       *int              `json:"page,omitempty"`
	PageSize   *int              `json:"page_size,omitempty"`
	Lang       string            `json:"lang,omitempty"`
	Filters    map[string]string `json:"filters,omitempty"`
}

type apiReader interface {
	GetRaw(context.Context, string) ([]byte, error)
	ReadRoute(context.Context, string, bool) (gw2.APIResponse, error)
}

func registerAPITools(server *mcp.Server, api apiReader) {
	addRead(server, "list_api_endpoints", "List routes exposed by the official Guild Wars 2 API discovery endpoint.", func(ctx context.Context, _ struct{}) (any, error) {
		routes, err := discoverRoutes(ctx, api)
		return map[string]any{"endpoints": routes}, err
	})
	addRead(server, "query_api", "Read JSON from a discovered GW2 API route. Substitute declared path parameters and use bounded IDs, pagination, language, and route-specific filters. Arbitrary URLs and query parameters are not accepted.", func(ctx context.Context, in apiQueryInput) (any, error) {
		routes, err := discoverRoutes(ctx, api)
		if err != nil {
			return nil, err
		}
		route := normalizeAPIRoute(in.Route)
		var selected *apiRoute
		for i := range routes {
			if routes[i].Route == route {
				selected = &routes[i]
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("route must exactly match a route returned by list_api_endpoints")
		}
		if selected.CurrentlyDisabled {
			return nil, fmt.Errorf("route is currently marked disabled by GW2 API discovery")
		}
		path, err := substituteRoute(route, in.PathParams)
		if err != nil {
			return nil, err
		}
		query, err := validateAPIQuery(*selected, in)
		if err != nil {
			return nil, err
		}
		path = "/v2/" + path
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
		response, err := api.ReadRoute(ctx, path, selected.RequiresAuth)
		if err != nil {
			return nil, err
		}
		result := map[string]any{
			"endpoint": response.Endpoint, "observed_at": response.ObservedAt.Format(time.RFC3339),
			"request": path, "data": response.Data, "partial": response.Partial,
		}
		if response.StatusCode != 0 {
			result["status_code"] = response.StatusCode
		}
		if response.Pagination != nil {
			result["pagination"] = response.Pagination
		}
		return result, nil
	})
}

func discoverRoutes(ctx context.Context, api apiReader) ([]apiRoute, error) {
	catalog, err := api.GetRaw(ctx, "/v2")
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(catalog))
	if text == "" || text == "null" {
		return nil, fmt.Errorf("GW2 API discovery response contained no routes")
	}
	routes := make(map[string]apiRoute)
	add := func(route apiRoute) {
		current := routes[route.Route]
		current.Route = route.Route
		current.RequiresAuth = current.RequiresAuth || route.RequiresAuth
		current.LocaleAware = current.LocaleAware || route.LocaleAware
		current.CurrentlyDisabled = current.CurrentlyDisabled || route.CurrentlyDisabled
		routes[route.Route] = current
	}
	for _, line := range strings.Split(text, "\n") {
		if route, ok := parseDiscoveryLine(line); ok && route.Route != "createsubtoken" {
			add(route)
		}
	}
	for parent, children := range map[string][]apiRoute{
		"commerce/exchange": {{Route: "coins"}, {Route: "gems"}},
		"commerce/transactions": {
			{Route: "current/buys", RequiresAuth: true}, {Route: "current/sells", RequiresAuth: true},
			{Route: "history/buys", RequiresAuth: true}, {Route: "history/sells", RequiresAuth: true},
		},
	} {
		if _, exists := routes[parent]; exists {
			for _, child := range children {
				add(apiRoute{Route: parent + "/" + child.Route, RequiresAuth: child.RequiresAuth})
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
	route = strings.TrimPrefix(route, "/v2/")
	route = strings.TrimPrefix(route, "/")
	return route
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
				entry.CurrentlyDisabled = true
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
			if id == "" || len(id) > 128 || id != strings.TrimSpace(id) || strings.EqualFold(id, "all") || strings.ContainsAny(id, ",/?#\\%") {
				return nil, fmt.Errorf("ids must be nonempty individual IDs; ids=all and comma-separated values are not allowed")
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
	case key == "since" && (strings.HasPrefix(route, "commerce/transactions/history/") || route == "guild/:id/log"):
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
