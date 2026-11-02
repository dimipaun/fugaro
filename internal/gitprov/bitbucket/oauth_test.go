package bitbucket

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The consumer and the tokens below are made up. Nothing here is a real
// Bitbucket credential, and no test may carry one.
const (
	testConsumerKey    = "fake-consumer-key"
	testConsumerSecret = "fake-consumer-secret"
	testAccessToken    = "fake-access-token"
)

// tokenAnswer is an endpoint's answer to a client_credentials grant, as the
// exchange reads it. extra is spliced in for the shapes a case needs.
func tokenAnswer(extra string) string {
	return fmt.Sprintf(`{"access_token":%q,"scopes":"repository pullrequest","expires_in":7200,"token_type":"bearer"%s}`,
		testAccessToken, extra)
}

func TestClientCredentialsExchange(t *testing.T) {
	type request struct{ method, path, body, user, pass string }
	var seen []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		user, pass, _ := r.BasicAuth()
		seen = append(seen, request{method: r.Method, path: r.URL.Path, body: string(body), user: user, pass: pass})
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, tokenAnswer(""))
	}))
	defer srv.Close()

	tok, exp, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if tok != testAccessToken {
		t.Fatalf("token = %q, want the answer's access_token", tok)
	}
	if len(seen) != 1 {
		t.Fatalf("the exchange made %d requests, want one", len(seen))
	}
	got := seen[0]
	if got.method != http.MethodPost || got.path != oauthTokenPath {
		t.Errorf("the exchange sent %s %s, want POST %s", got.method, got.path, oauthTokenPath)
	}
	if got.body != "grant_type=client_credentials" {
		t.Errorf("the exchange sent the body %q, want the grant and nothing else", got.body)
	}
	if got.user != testConsumerKey || got.pass != testConsumerSecret {
		t.Errorf("the exchange authenticated as %q/%q, want the consumer's key and secret", got.user, got.pass)
	}
	if want := time.Now().Add(7200 * time.Second); exp.Sub(want) > time.Minute || exp.Sub(want) < -time.Minute {
		t.Errorf("expiry = %s, want about two hours from now, the answer's own expires_in", exp)
	}

	t.Run("an answer with no access token is refused", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"expires_in":7200,"token_type":"bearer"}`)
		}))
		defer s.Close()
		if _, _, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, s.URL, s.Client()); err == nil {
			t.Fatal("an answer with no access token was accepted")
		}
	})

	t.Run("an answer with no life is refused", func(t *testing.T) {
		// A token whose life the endpoint does not state cannot be handed
		// out as one that expires: a caller would take a zero expiry for
		// "never", and a two-hour token would outlive the read it was
		// minted for.
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"access_token":"`+testAccessToken+`","token_type":"bearer"}`)
		}))
		defer s.Close()
		if _, _, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, s.URL, s.Client()); err == nil {
			t.Fatal("an answer with no expires_in was accepted")
		}
	})

	t.Run("a redirect is refused, and the credentials go nowhere else", func(t *testing.T) {
		var landed int
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			landed++
			io.WriteString(w, tokenAnswer(""))
		}))
		defer elsewhere.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+oauthTokenPath, http.StatusFound)
		}))
		defer redirect.Close()

		if _, _, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, redirect.URL, redirect.Client()); err == nil {
			t.Fatal("a redirected exchange was followed")
		}
		if landed != 0 {
			t.Fatalf("the exchange sent the consumer's credentials to the redirect's target %d times", landed)
		}
	})
}

// TestExchangeErrorCarriesNoSecret: the request carries the consumer's secret,
// so a refusal must not relay an answer that echoes it. The endpoint here is
// the case that makes that concrete -- it prints back everything it was given.
func TestExchangeErrorCarriesNoSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"invalid_client","error_description":"client %s (secret %s) is unknown; sent %s"}`,
			testConsumerKey, testConsumerSecret, body)
	}))
	defer srv.Close()

	_, _, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, srv.URL, srv.Client())
	if err == nil {
		t.Fatal("an exchange the endpoint refused returned no error")
	}
	for _, credential := range []string{testConsumerSecret, testConsumerKey} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("the refusal carries a credential: %v", err)
		}
	}
}

// TestExchangeDropsRefreshToken: Bitbucket may answer a grant with a refresh
// token beside the access token. A holder of the read consumer keeps that
// refresh token; the exchange hands the caller the access token alone, so no
// caller is ever in a position to forward one.
func TestExchangeDropsRefreshToken(t *testing.T) {
	const refresh = "fake-refresh-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, tokenAnswer(fmt.Sprintf(`,"refresh_token":%q`, refresh)))
	}))
	defer srv.Close()

	tok, exp, err := MintAccessToken(ctx, Consumer{Key: testConsumerKey, Secret: testConsumerSecret}, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if tok != testAccessToken || exp.IsZero() {
		t.Fatalf("token = %q, expiry = %s, want the answer's access token with its life", tok, exp)
	}
	if got := tok + exp.String(); strings.Contains(got, refresh) {
		t.Fatalf("what the exchange returned carries the refresh token: %q", got)
	}
}
