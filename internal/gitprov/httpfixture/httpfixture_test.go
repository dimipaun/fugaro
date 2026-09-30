package httpfixture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeReplaysInOrder(t *testing.T) {
	srv := Serve(t, writeFixture(t, `[
	  {"method":"GET","path":"/prs?q=branch=\"fugaro/x\" AND state=\"OPEN\"","auth":"Bearer tok","status":200,"response":{"values":[]}},
	  {"method":"POST","path":"/prs","request":{"title":"T","draft":true},"status":201,"response":{"id":1}}
	]`))
	c := &httpjson.Client{BaseURL: srv.URL, Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
	ctx := context.Background()
	var list struct{ Values []any }
	if err := c.Do(ctx, "GET", `/prs?q=branch%3D%22fugaro%2Fx%22+AND+state%3D%22OPEN%22`, nil, &list); err != nil {
		t.Fatal(err)
	}
	var pr struct{ ID int }
	if err := c.Do(ctx, "POST", "/prs", map[string]any{"draft": true, "title": "T"}, &pr); err != nil || pr.ID != 1 {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestMismatchIsReported(t *testing.T) {
	e := Exchange{Method: "POST", Path: "/prs", Request: []byte(`{"draft":true}`), Status: 201}
	r := httptest.NewRequest("POST", "/prs", nil)
	if msg := mismatch(e, r, []byte(`{"draft":false}`)); !strings.Contains(msg, "body") {
		t.Fatalf("mismatch = %q", msg)
	}
	if msg := mismatch(e, httptest.NewRequest("PUT", "/prs", nil), nil); !strings.Contains(msg, "method") {
		t.Fatalf("mismatch = %q", msg)
	}
	e.Auth = "Bearer a"
	r = httptest.NewRequest("POST", "/prs", nil)
	r.Header.Set("Authorization", "Bearer b")
	if msg := mismatch(e, r, []byte(`{"draft":true}`)); !strings.Contains(msg, "authorization") {
		t.Fatalf("mismatch = %q", msg)
	}
}

func TestRecorderScrubsAndReplays(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"ghs_live_secret","expires_at":"2026-09-27T12:00:00Z"}`))
	}))
	defer live.Close()
	rec := &Recorder{Secrets: []string{"ghs_live_secret"}}
	c := &httpjson.Client{BaseURL: live.URL, HTTP: &http.Client{Transport: rec},
		Auth: func(context.Context) (string, error) { return "Bearer jwt-secret", nil }}
	if err := c.Do(context.Background(), "POST", "/app/installations/1/access_tokens", map[string]any{"repositories": []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recorded.json")
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "ghs_live_secret") || strings.Contains(string(data), "jwt-secret") || !strings.Contains(string(data), "REDACTED") {
		t.Fatalf("recording leaks a secret:\n%s", data)
	}
	srv := Serve(t, path)
	var out struct{ Token string }
	if err := (&httpjson.Client{BaseURL: srv.URL}).Do(context.Background(), "POST", "/app/installations/1/access_tokens", map[string]any{"repositories": []string{"web"}}, &out); err != nil || out.Token != "REDACTED" {
		t.Fatalf("replay = %+v, %v", out, err)
	}
}

// TestRecorderScrubsQueryInPath: a secret
// carried in a request's query string must not reach the fixture file
// as-is, and the scrubbed path must still be replayable.
func TestRecorderScrubsQueryInPath(t *testing.T) {
	const secret = "ghs_query_secret"
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"values":[]}`))
	}))
	defer live.Close()
	rec := &Recorder{Secrets: []string{secret}}
	c := &httpjson.Client{BaseURL: live.URL, HTTP: &http.Client{Transport: rec}}
	path := "/repos?token=" + secret + "&other=kept"
	var out struct{ Values []any }
	if err := c.Do(context.Background(), "GET", path, nil, &out); err != nil {
		t.Fatal(err)
	}

	if exs := rec.Exchanges(); len(exs) != 1 || strings.Contains(exs[0].Path, secret) || !strings.Contains(exs[0].Path, "REDACTED") {
		t.Fatalf("recorded exchange = %+v", exs)
	}

	fixture := filepath.Join(t.TempDir(), "recorded.json")
	if err := rec.Save(fixture); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("recording leaks the query secret:\n%s", data)
	}
	if !strings.Contains(string(data), "REDACTED") {
		t.Fatalf("recording does not redact the query secret:\n%s", data)
	}

	// Replay: the client still sends the real secret (it's a test constant,
	// not an actual secret), but the fixture only has "REDACTED" in its
	// place. The replay server must match anyway.
	srv := Serve(t, fixture)
	var replayed struct{ Values []any }
	if err := (&httpjson.Client{BaseURL: srv.URL}).Do(context.Background(), "GET", path, nil, &replayed); err != nil {
		t.Fatalf("replay with scrubbed query: %v", err)
	}
}

// TestRecorderScrubsTokenFields covers a credential the caller could not
// list in Secrets because the API minted it during the recording (a GitHub
// installation token, "ghs_…"): every JSON "token" field in a recorded
// response is replaced with "REDACTED", at any depth, while other fields
// are kept.
func TestRecorderScrubsTokenFields(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"ghs_minted_unknown","expires_at":"2026-09-27T12:00:00Z","nested":[{"token":"ghs_nested_unknown","id":12345678901234567}]}`))
	}))
	defer live.Close()
	rec := &Recorder{}
	c := &httpjson.Client{BaseURL: live.URL, HTTP: &http.Client{Transport: rec}}
	var out struct{ Token string }
	if err := c.Do(context.Background(), "POST", "/app/installations/1/access_tokens", map[string]any{"token": "request-side"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Token != "ghs_minted_unknown" {
		t.Fatalf("the caller must still see the live response, got %q", out.Token)
	}
	path := filepath.Join(t.TempDir(), "recorded.json")
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if strings.Contains(s, "ghs_minted_unknown") || strings.Contains(s, "ghs_nested_unknown") {
		t.Fatalf("recording leaks a minted token:\n%s", s)
	}
	if !strings.Contains(s, "2026-09-27T12:00:00Z") || !strings.Contains(s, "12345678901234567") {
		t.Fatalf("recording lost non-token fields:\n%s", s)
	}
}

// TestRecorderScrubsSuffixedTokenFields covers OAuth-style credential
// fields (access_token, refresh_token, …): any key ending in "_token" is
// scrubbed like "token", while look-alikes such as "token_type" are kept.
func TestRecorderScrubsSuffixedTokenFields(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"oauth_access_unknown","refresh_token":"oauth_refresh_unknown","token_type":"bearer","scopes":{"id_token":"oidc_unknown"}}`))
	}))
	defer live.Close()
	rec := &Recorder{}
	c := &httpjson.Client{BaseURL: live.URL, HTTP: &http.Client{Transport: rec}}
	if err := c.Do(context.Background(), "POST", "/site/oauth2/access_token", nil, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recorded.json")
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	for _, leak := range []string{"oauth_access_unknown", "oauth_refresh_unknown", "oidc_unknown"} {
		if strings.Contains(s, leak) {
			t.Fatalf("recording leaks %s:\n%s", leak, s)
		}
	}
	if !strings.Contains(s, `"bearer"`) {
		t.Fatalf("recording lost token_type:\n%s", s)
	}
}

func TestServeSendsHeadersWithServerURL(t *testing.T) {
	srv := Serve(t, writeFixture(t, `[
	  {"method":"GET","path":"/items","status":200,"header":{"Link":"<{{server}}/items?page=2>; rel=\"next\""},"response":[1]},
	  {"method":"GET","path":"/items?page=2","status":200,"response":[2]}
	]`))
	c := &httpjson.Client{BaseURL: srv.URL}
	var page []int
	next, err := c.DoPage(context.Background(), "GET", "/items", nil, &page)
	if err != nil || next != "/items?page=2" {
		t.Fatalf("next = %q, err = %v", next, err)
	}
	if next, err := c.DoPage(context.Background(), "GET", next, nil, &page); err != nil || next != "" || page[0] != 2 {
		t.Fatalf("next = %q, page = %v, err = %v", next, page, err)
	}
}
