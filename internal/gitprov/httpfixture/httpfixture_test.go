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

// TestRecorderScrubsQueryInPath covers the round-1 review finding: a secret
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
