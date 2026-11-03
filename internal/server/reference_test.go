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
	for _, raw := range []string{
		"http://wiki.guildwars2.com/wiki/Foo", "https://evil.example/wiki/Foo",
		"https://wiki.guildwars2.com:444/wiki/Foo", "https://user@wiki.guildwars2.com/wiki/Foo",
		"https://wiki.guildwars2.com/wiki/Foo#section",
		"https://wiki.guildwars2.com/api.php?action=edit",
		"https://wiki.guildwars2.com/api.php?action=query&access_token=secret",
		"https://wiki.guildwars2.com/api.php?action=query&key=secret",
		"https://wiki.guildwars2.com/api.php?action=query&action=edit",
		"https://wiki.guildwars2.com/wiki/Foo?bad=%zz",
		"https://gw2.app/reference?access_token=secret",
	} {
		if _, err := validReferenceURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{
		"https://wiki.guildwars2.com/wiki/Foo",
		"https://wiki-en.guildwars2.com/api.php?action=parse&page=Foo",
		"https://gw2.app/reference", "https://gw2efficiency.com/account/overview",
	} {
		if _, err := validReferenceURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}

func TestReferencePaginationUnicodeAndNoAuthorization(t *testing.T) {
	var authorization string
	reader := newReferenceReader()
	reader.httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
		authorization = req.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader("猫🧪ab")), Request: req}, nil
	})
	got, err := reader.get(context.Background(), "https://gw2.app/reference", 1, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "🧪a" || got.TotalChars != 4 || got.NextOffset == nil || *got.NextOffset != 3 || !got.Truncated || got.Complete {
		t.Fatalf("reference page = %+v", got)
	}
	if got.ContentType != "text/plain; charset=utf-8" || authorization != "" {
		t.Fatalf("content type=%q authorization=%q", got.ContentType, authorization)
	}
	last, err := reader.get(context.Background(), "https://gw2.app/reference", 3, 2, "")
	if err != nil || last.Text != "b" || last.NextOffset != nil || last.Complete {
		t.Fatalf("last page must terminate pagination: %+v, %v", last, err)
	}
}

func TestReferenceBodyLimitAndRedirect(t *testing.T) {
	large := strings.Repeat("x", maxReferenceBody+1)
	client := func(body string, status int, redirects *[]string) *http.Client {
		httpClient := newReferenceReader().httpClient
		httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
			*redirects = append(*redirects, req.URL.String())
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})
		return httpClient
	}
	var seen []string
	reader := referenceReader{httpClient: client(large, 200, &seen)}
	if _, err := reader.get(context.Background(), "https://gw2.app/reference", 0, 10, ""); err == nil {
		t.Fatal("oversized body accepted")
	}
	seen = nil
	reader.httpClient = client("", http.StatusFound, &seen)
	if _, err := reader.get(context.Background(), "https://gw2.app/reference", 0, 10, ""); err == nil {
		t.Fatal("redirect response accepted")
	}
	if len(seen) != 1 {
		t.Fatalf("requests after redirect = %v", seen)
	}
	reader.httpClient = client("denied", http.StatusForbidden, &seen)
	if _, err := reader.get(context.Background(), "https://gw2.app/reference", 0, 10, ""); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("forbidden response error = %v", err)
	}
	reader.httpClient = client("partial", http.StatusPartialContent, &seen)
	if _, err := reader.get(context.Background(), "https://gw2.app/reference", 0, 10, ""); err == nil || !strings.Contains(err.Error(), "HTTP 206") {
		t.Fatalf("partial response error = %v", err)
	}
}

func TestReferenceReadableHTMLAndRawFormat(t *testing.T) {
	reader := newReferenceReader()
	reader.httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept") == "" || req.Header.Get("User-Agent") == "" {
			t.Fatal("reference headers were not set")
		}
		body := `<html><style>.x{}</style><script>ignore()</script><p>Recipe <a href="/wiki/Item">item</a> <img alt="Copper ore" src="x"></p></html>`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	got, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "text")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Recipe item (/wiki/Item) Copper ore" || !got.Complete || got.Truncated {
		t.Fatalf("readable html = %+v", got)
	}
	raw, err := reader.get(context.Background(), "https://wiki.guildwars2.com/wiki/Recipe", 0, 1000, "raw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.Text, "<script>") {
		t.Fatalf("raw HTML was transformed: %q", raw.Text)
	}
}

func TestReferenceSniffsMissingContentType(t *testing.T) {
	reader := newReferenceReader()
	reader.httpClient.Transport = referenceRoundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html><p>Public recipe</p></html>")), Request: req}, nil
	})
	got, err := reader.get(context.Background(), "https://gw2.app/reference", 0, 1000, "text")
	if err != nil || got.Text != "Public recipe" {
		t.Fatalf("reference = %+v, %v", got, err)
	}
}
