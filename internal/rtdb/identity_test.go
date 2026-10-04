package rtdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

func TestCallerEmail(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("access_token")
		json.NewEncoder(w).Encode(map[string]string{"email": "person@example.com"})
	}))
	defer srv.Close()

	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "the-access-token"})
	c, err := rtdb.New("https://db.example.com", rtdb.Auth{Source: src}, rtdb.WithTokenInfoURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	email, err := c.CallerEmail(context.Background())
	if err != nil || email != "person@example.com" {
		t.Fatalf("email = %q, err = %v", email, err)
	}
	if gotToken != "the-access-token" {
		t.Fatalf("the client's own token was not sent: %q", gotToken)
	}
}

func TestCallerEmailErrors(t *testing.T) {
	// An ID-token client (a run) has no Google account to ask about.
	c, err := rtdb.New("https://db.example.com", idAuth("idtok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallerEmail(context.Background()); err == nil {
		t.Fatal("an ID-token client must refuse: it names no account")
	}

	// No email in the answer (a service account with none verified, or a
	// token missing the userinfo.email scope): an error, not an empty string.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer srv.Close()
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"})
	c2, err := rtdb.New("https://db.example.com", rtdb.Auth{Source: src}, rtdb.WithTokenInfoURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.CallerEmail(context.Background()); err == nil {
		t.Fatal("no email in the answer must be an error")
	}

	// A refused introspection (an expired or revoked token).
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv2.Close()
	c3, err := rtdb.New("https://db.example.com", rtdb.Auth{Source: src}, rtdb.WithTokenInfoURL(srv2.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c3.CallerEmail(context.Background()); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want it to name the status", err)
	}
}
