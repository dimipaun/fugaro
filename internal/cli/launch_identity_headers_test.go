package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// TestTicketAskPresentsThePersonsTokenInBothHeaders: behind Cloud Run's front
// end Authorization is the one the front end's invoker check reads, and the
// service reads the person's own token from X-Fugaro-Person-Identity. An ask
// that sent Authorization alone was refused caller_not_admitted for a person on
// the roster, so no launch could start.
func TestTicketAskPresentsThePersonsTokenInBothHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"caller_not_admitted","message":"x"}`))
	}))
	defer srv.Close()
	i := &launchIssuer{
		service:  srv.URL,
		now:      time.Now(),
		identity: func(context.Context, string) (string, error) { return "the-token", nil },
	}
	_, _ = i.ask(context.Background(), identitywire.TicketRequest{})
	if v := got.Get("Authorization"); v != "Bearer the-token" {
		t.Errorf("Authorization = %q, want the bearer", v)
	}
	if v := got.Get(credsvc.HeaderPersonIdentity); v != "the-token" {
		t.Errorf("%s = %q, want the person's token", credsvc.HeaderPersonIdentity, v)
	}
}
