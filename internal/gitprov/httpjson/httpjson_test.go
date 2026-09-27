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
