package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type wikiRoundTrip func(*http.Request) (*http.Response, error)

func (f wikiRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestWikiPagesAndSearch(t *testing.T) {
	var asked string
	askFailures := 0
	reader := wikiReader{httpClient: &http.Client{Transport: wikiRoundTrip(func(req *http.Request) (*http.Response, error) {
		check(t, req.URL.Host == "wiki.guildwars2.com" && req.Header.Get("Authorization") == "", "unexpected wiki request %s", req.URL)
		query := req.URL.Query()
		status, body := 200, `<style>hide</style><script>ignore()</script><h2>Recipe</h2><p>Uses <a href="/wiki/Item"><b>item</b></a> &amp; 猫🧪</p>`
		switch {
		case req.URL.Path == "/api.php" && query.Get("action") == "query":
			body = `{"query":{"search":[{"title":"Glob of Ectoplasm"},{"title":"Ecto [gambling]"},{"title":"Salvage"}]}}`
		case req.URL.Path == "/api.php":
			asked = query.Get("query")
			body = `{"query":{"results":{"Glob of Ectoplasm":{"printouts":{"Has game id":[19721],"Has context":["Item"]}},"Salvage":{"printouts":{"Has game id":[],"Has context":[]}}}}}`
			if askFailures > 0 {
				askFailures--
				body = `{"error":{"code":"internal_api_error_DBConnectionError"}}`
			}
		case query.Get("title") == "Missing":
			status = 404
		case query.Get("title") == "Large":
			body = strings.Repeat("x", maxWikiBody+1)
		case query.Get("action") == "raw":
			body = "{{item infobox}}"
		default:
			check(t, query.Get("title") == "Aurene's Weight" && query.Get("action") == "render", "page request %s", req.URL)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	ctx := context.Background()

	page, err := reader.page(ctx, " Aurene's Weight ", false, 0, 0)
	check(t, err == nil && page.Text == "Recipe\nUses item & 猫🧪" && page.TotalChars == 21 && page.NextOffset == 0, "page = %+v, %v", page, err)
	part, err := reader.page(ctx, "Aurene's Weight", false, 19, 1)
	check(t, err == nil && part.Text == "猫" && part.NextOffset == 20, "page slice = %+v, %v", part, err)
	source, err := reader.page(ctx, "Aurene's Weight", true, 0, 0)
	check(t, err == nil && source.Text == "{{item infobox}}", "wikitext = %+v, %v", source, err)
	for _, bad := range []struct {
		title            string
		offset, maxChars int
	}{{"", 0, 0}, {"Missing", 0, 0}, {"Large", 0, 0}, {"Aurene's Weight", 22, 0}, {"Aurene's Weight", -1, 0}, {"Aurene's Weight", 0, 100001}} {
		_, err := reader.page(ctx, bad.title, false, bad.offset, bad.maxChars)
		check(t, err != nil, "invalid page read accepted: %+v", bad)
	}

	hits, err := reader.search(ctx, "ecto", 0)
	check(t, err == nil && len(hits) == 3 && hits[0].Type == "Item" && len(hits[0].IDs) == 1 && hits[0].IDs[0] == 19721 && len(hits[2].IDs) == 0, "wiki search = %+v, %v", hits, err)
	check(t, asked == "[[:Glob of Ectoplasm||Salvage]]|?Has game id|?Has context|limit=2", "ask query = %q", asked)
	for _, bad := range []struct {
		query string
		limit int
	}{{" ", 0}, {"ecto", 16}} {
		_, err = reader.search(ctx, bad.query, bad.limit)
		check(t, err != nil, "invalid search accepted: %+v", bad)
	}
	askFailures = 1
	hits, err = reader.search(ctx, "ecto", 0)
	check(t, err == nil && len(hits[0].IDs) == 1 && askFailures == 0, "transient wiki error not retried: %+v %v", hits, err)
	askFailures = 2
	_, err = reader.search(ctx, "ecto", 0)
	check(t, err != nil, "wiki error returned results without IDs")
}
