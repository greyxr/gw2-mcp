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
	reader.httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
		unauthorized = unauthorized || req.Header.Get("Authorization") != ""
		followedRedirect = followedRedirect || req.URL.Host == "evil.example"
		status, contentType, body := 200, "text/plain; charset=utf-8", "猫🧪ab"
		switch req.URL.Path {
		case "/wiki/Recipe":
			contentType = "text/html; charset=utf-8"
			body = `<html><style>hide</style><script>ignore()</script><p>Recipe <a href="/wiki/Item"><b>item</b></a> <img alt="Copper ore" src="x"></p></html>`
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

	page, err := reader.get(context.Background(), "https://gw2.app/reference", 1, 2, "")
	check(t, err == nil && page.Text == "🧪a" && page.TotalChars == 4 && page.NextOffset != nil && *page.NextOffset == 3, "unicode page = %+v, %v", page, err)
	last, err := reader.get(context.Background(), "https://gw2.app/reference", 3, 2, "")
	check(t, err == nil && last.Text == "b" && last.NextOffset == nil && !last.Complete, "last unicode page = %+v, %v", last, err)
	text, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "text")
	check(t, err == nil && strings.Contains(text.Text, "Recipe item (/wiki/Item) Copper ore") && !strings.Contains(text.Text, "ignore()"), "readable HTML = %+v, %v", text, err)
	raw, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "raw")
	check(t, err == nil && strings.Contains(raw.Text, "<script>"), "raw HTML = %+v, %v", raw, err)
	for _, path := range []string{"/large", "/invalid", "/redirect"} {
		_, err := reader.get(context.Background(), "https://gw2.app"+path, 0, 10, "")
		check(t, err != nil, "unsafe response %s was accepted", path)
	}
	check(t, sawRedirect && !followedRedirect && !unauthorized, "redirect=%t followed=%t authorization=%t", sawRedirect, followedRedirect, unauthorized)
}
