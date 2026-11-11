package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type referenceRoundTrip func(*http.Request) (*http.Response, error)

func (f referenceRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestReferenceURLPolicy(t *testing.T) {
	for raw, wantValid := range map[string]bool{
		"https://wiki.guildwars2.com/wiki/Foo": true, "https://wiki-en.guildwars2.com/api.php?action=parse&page=Foo": true,
		"https://gw2.app/reference": true, "https://gw2efficiency.com/account/overview": true,
		"http://wiki.guildwars2.com/wiki/Foo": false, "https://evil.example/wiki/Foo": false,
		"https://wiki.guildwars2.com:444/wiki/Foo": false, "https://user@wiki.guildwars2.com/wiki/Foo": false,
		"https://wiki.guildwars2.com/wiki/Foo#section": false, "https://wiki.guildwars2.com/api.php?action=edit": false,
		"https://wiki.guildwars2.com/api.php?action=query&action=edit":         false,
		"https://wiki.guildwars2.com/api.php?action=query&access_token=secret": false,
		"https://wiki.guildwars2.com/api.php?action=query&key=secret":          false,
		"https://gw2.app/reference?apiKey=secret":                              false, "https://gw2.app/reference?authorization=secret": false,
		"https://gw2.app/reference?password=secret": false, "https://gw2.app/reference?secret=secret": false,
		"https://gw2.app/reference?credentials=secret": false,
		"https://gw2.app/reference?user_token=secret":  false, "https://gw2.app/reference?special_token=secret": false,
		"https://gw2.app/reference?my_key=secret":     false,
		"https://gw2.app/reference?secretary=example": true, "https://gw2.app/reference?keymaster=example": true,
		"https://gw2.app/reference?access_token=secret": false, "https://wiki.guildwars2.com/wiki/Foo?bad=%zz": false,
	} {
		_, err := validReferenceURL(raw)
		check(t, (err == nil) == wantValid, "reference URL %q: want valid=%t, err=%v", raw, wantValid, err)
	}
}

func TestReferenceReadModesAndBounds(t *testing.T) {
	reader := newReferenceReader()
	var unauthorized, sawRedirect, followedRedirect bool
	var asked, requested string
	askFailures := 0
	reader.httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
		unauthorized = unauthorized || req.Header.Get("Authorization") != ""
		followedRedirect = followedRedirect || req.URL.Host == "evil.example"
		requested = req.URL.Path
		status, contentType, body := 200, "text/plain; charset=utf-8", "猫🧪ab"
		switch req.URL.Path {
		case "/wiki/Recipe":
			contentType = "text/html; charset=utf-8"
			body = `<html><style>hide</style><script>ignore()</script><div id="nav">Navigation</div><div id="mw-content-text"><h2>Recipe<span class="mw-editsection"><span>[</span><a href="/edit">edit</a><span>]</span></span></h2><p>Uses <a href="/wiki/Item"><b>item</b></a> <img alt="Copper ore" src="x"></p><div class="printfooter">Retrieved from</div></div></html>`
		case "/api.php":
			contentType = "application/json"
			body = `{"query":{"search":[{"title":"Glob of Ectoplasm"},{"title":"Ecto [gambling]"},{"title":"Salvage"}]}}`
			if req.URL.Query().Get("action") == "ask" {
				asked = req.URL.Query().Get("query")
				if askFailures > 0 {
					askFailures--
					body = `{"error":{"code":"internal_api_error_DBConnectionError"}}`
					break
				}
				body = `{"query":{"results":{"Glob of Ectoplasm":{"printouts":{"Has game id":[19721],"Has context":["Item"]}},"Salvage":{"printouts":{"Has game id":[],"Has context":[]}}}}}`
			}
		case "/large":
			body = strings.Repeat("x", maxReferenceBody+1)
		case "/invalid":
			body = string([]byte{0xff})
		case "/redirect":
			status = http.StatusFound
		}
		header := http.Header{"Content-Type": {contentType}}
		if req.URL.Path == "/redirect" {
			header.Set("Location", "https://evil.example/redirect")
			sawRedirect = true
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})

	page, err := reader.get(context.Background(), "https://gw2.app/reference", 1, 2, "", false)
	check(t, err == nil && page.Text == "🧪a" && page.TotalChars == 4 && page.NextOffset == 3, "unicode page = %+v, %v", page, err)
	last, err := reader.get(context.Background(), "https://gw2.app/reference", 3, 2, "", false)
	check(t, err == nil && last.Text == "b" && last.NextOffset == 0, "last unicode page = %+v, %v", last, err)
	text, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "text", false)
	check(t, err == nil && text.Text == "Recipe\nUses item Copper ore", "article text = %q, %v", text.Text, err)
	linked, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "text", true)
	check(t, err == nil && linked.Text == "Recipe\nUses item (/wiki/Item) Copper ore", "linked text = %q, %v", linked.Text, err)
	raw, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "raw", false)
	check(t, err == nil && strings.Contains(raw.Text, "<script>"), "raw HTML = %+v, %v", raw, err)
	for _, path := range []string{"/large", "/invalid", "/redirect"} {
		_, err := reader.get(context.Background(), "https://gw2.app"+path, 0, 10, "", false)
		check(t, err != nil, "unsafe response %s was accepted", path)
	}
	check(t, sawRedirect && !followedRedirect && !unauthorized, "redirect=%t followed=%t authorization=%t", sawRedirect, followedRedirect, unauthorized)

	hits, err := reader.search(context.Background(), "ecto", 0)
	check(t, err == nil && len(hits) == 3 && hits[0].Type == "Item" && len(hits[0].IDs) == 1 && hits[0].IDs[0] == 19721 && len(hits[2].IDs) == 0, "wiki search = %+v, %v", hits, err)
	check(t, asked == "[[:Glob of Ectoplasm||Salvage]]|?Has game id|?Has context|limit=2", "ask query = %q", asked)
	_, err = reader.search(context.Background(), " ", 0)
	check(t, err != nil, "empty search accepted")
	_, err = reader.search(context.Background(), "ecto", 16)
	check(t, err != nil, "oversized ask accepted")
	askFailures = 1
	hits, err = reader.search(context.Background(), "ecto", 0)
	check(t, err == nil && len(hits[0].IDs) == 1 && askFailures == 0, "transient wiki error not retried: %+v %v", hits, err)
	askFailures = 2
	_, err = reader.search(context.Background(), "ecto", 0)
	check(t, err != nil, "wiki error returned results without IDs")
	spaced, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Aurene's Weight", 0, 10, "", false)
	check(t, err == nil && spaced.URL == "https://wiki.guildwars2.com/wiki/Aurene%27s_Weight" && requested == "/wiki/Aurene's_Weight", "wiki title URL = %q %q %v", spaced.URL, requested, err)
}
