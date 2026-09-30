package httpjson

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoSendsAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/things" || r.Header.Get("Authorization") != "Bearer tok" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Extra") != "1" || string(body) != `{"name":"x"}` {
			t.Errorf("request = %s %s %v %s", r.Method, r.URL, r.Header, body)
		}
		w.Write([]byte(`{"id":7}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/api/", Header: http.Header{"X-Extra": {"1"}},
		Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
	var out struct{ ID int }
	if err := c.Do(context.Background(), "POST", "/things", map[string]string{"name": "x"}, &out); err != nil || out.ID != 7 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestDoStatusErrorIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("e", 5000), http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	err := (&Client{BaseURL: srv.URL}).Do(context.Background(), "GET", "/x?secret=no", nil, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 422 || len(se.Body) > 520 || se.URL != "/x" {
		t.Fatalf("err = %v", err)
	}
}

func TestDoAuthError(t *testing.T) {
	c := &Client{BaseURL: "http://unused.invalid", Auth: func(context.Context) (string, error) { return "", errors.New("no key") }}
	if err := c.Do(context.Background(), "GET", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoPageFollowsLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.RequestURI() {
		case "/api/v3/items?per_page=2":
			w.Header().Add("Link", `<`+srv.URL+`/api/v3/items?per_page=2&page=2>; rel="next", <`+srv.URL+`/api/v3/items?per_page=2&page=3>; rel="last"`)
			w.Write([]byte(`[1,2]`))
		case "/api/v3/items?per_page=2&page=2":
			w.Header().Add("Link", `<`+srv.URL+`/api/v3/items?per_page=2&page=1>; rel="prev first"`)
			w.Write([]byte(`[3]`))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/api/v3", Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
	var all []int
	path := "/items?per_page=2"
	for pages := 0; path != ""; pages++ {
		if pages > 2 {
			t.Fatal("paging did not stop")
		}
		var page []int
		next, err := c.DoPage(context.Background(), "GET", path, nil, &page)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		path = next
	}
	if len(all) != 3 || all[2] != 3 {
		t.Fatalf("all = %v", all)
	}
}

func TestDoPageRefusesForeignHost(t *testing.T) {
	var foreignHits int
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits++
		w.Write([]byte(`[]`))
	}))
	defer foreign.Close()
	for name, link := range map[string]string{
		"other host":   foreign.URL + "/items?page=2",
		"other scheme": "", // filled in below: https on the API's own host
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(nil)
			defer srv.Close()
			if link == "" {
				link = "https://" + strings.TrimPrefix(srv.URL, "http://") + "/items?page=2"
			}
			srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", `<`+link+`>; rel="next"`)
				w.Write([]byte(`[1]`))
			})
			c := &Client{BaseURL: srv.URL, Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
			var page []int
			next, err := c.DoPage(context.Background(), "GET", "/items", nil, &page)
			if err == nil || next != "" || !strings.Contains(err.Error(), "paging left the API host") {
				t.Fatalf("next = %q, err = %v", next, err)
			}
		})
	}
	if foreignHits != 0 {
		t.Fatalf("the foreign host saw %d requests", foreignHits)
	}
}

func TestDoPageNoLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[1]`)) }))
	defer srv.Close()
	var page []int
	next, err := (&Client{BaseURL: srv.URL}).DoPage(context.Background(), "GET", "/items", nil, &page)
	if err != nil || next != "" || len(page) != 1 {
		t.Fatalf("next = %q, page = %v, err = %v", next, page, err)
	}
}

func TestPagePath(t *testing.T) {
	c := &Client{BaseURL: "https://ghe.example.invalid/api/v3"}
	for next, want := range map[string]string{
		"https://ghe.example.invalid/api/v3/repos/x?page=2": "/repos/x?page=2",
		"https://GHE.example.invalid:443/api/v3/repos/x":    "/repos/x",
		"https://ghe.example.invalid/api/v4/repos/x":        "",
		"https://ghe.example.invalid.evil.invalid/api/v3/x": "",
		"http://ghe.example.invalid/api/v3/repos/x":         "",
		"https://user@ghe.example.invalid/api/v3/repos/x":   "",
		"/api/v3/repos/x": "",
		"https://ghe.example.invalid:8443/api/v3/repos/x?page=": "",
	} {
		got, err := c.PagePath(next)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("PagePath(%q) = %q, %v; want %q", next, got, err, want)
		}
	}
}
