package bitbucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultOAuthRoot is where Bitbucket Cloud's OAuth endpoints live. It is a
// host of its own: the REST API's root is api.bitbucket.org, and a consumer's
// own credentials are presented to bitbucket.org alone.
const DefaultOAuthRoot = "https://bitbucket.org"

// oauthTokenPath is where a consumer's client_credentials grant is exchanged
// for an access token.
const oauthTokenPath = "/site/oauth2/access_token"

// exchangeTimeout bounds the exchange when the caller's client sets no
// timeout of its own.
const exchangeTimeout = 30 * time.Second

// maxAnswerBytes bounds the token answer this reads: it is one small JSON
// object, so a longer body is not one this endpoint wrote.
const maxAnswerBytes = 64 << 10

// Consumer is a Bitbucket OAuth consumer: the key Bitbucket identifies it by,
// and the secret that authenticates it. Both are credentials, and neither
// appears in an answer, a log or an error (G5).
type Consumer struct {
	Key    string
	Secret string
}

// Minted is one token a consumer exchange minted: the token itself, the
// instant it expires, and the scopes the endpoint says it carries.
//
// Scopes is the answer's own list and empty when the answer listed none —
// which is a fact a caller reads as "the endpoint did not say what this token
// is scoped to" and never as "this token has no scopes". A holder that has to
// be sure of a token's reach cannot be sure from an empty list, and the read
// mint's rule (BB4, U11) is written to that difference.
type Minted struct {
	Token     string
	ExpiresAt time.Time
	Scopes    []string
}

// MintAccessToken exchanges a consumer's key and secret for one access token
// with the client_credentials grant, and returns the token with its expiry and
// its granted scopes. It is how a holder of the read consumer mints without
// handing the consumer's own credentials on: what the caller receives is the
// token.
//
// oauthRoot is where the exchange is made, and "" means DefaultOAuthRoot.
// A refresh token in the answer is not read, so it is neither kept nor
// forwarded, and a caller is never handed one (G5).
func MintAccessToken(ctx context.Context, c Consumer, oauthRoot string, hc *http.Client) (Minted, error) {
	if c.Key == "" || c.Secret == "" {
		return Minted{}, errors.New("bitbucket: an OAuth consumer's key and secret are both required")
	}
	if oauthRoot == "" {
		oauthRoot = DefaultOAuthRoot
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(oauthRoot, "/")+oauthTokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return Minted{}, fmt.Errorf("building the Bitbucket consumer exchange: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.Key, c.Secret)
	resp, err := exchangeClient(hc).Do(req)
	if err != nil {
		return Minted{}, fmt.Errorf("exchanging the Bitbucket consumer for an access token: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return Minted{}, fmt.Errorf("reading the Bitbucket consumer exchange: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Minted{}, refusal(resp.StatusCode, data)
	}
	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		// Scopes is what Bitbucket documents: one string of scope words,
		// separated by spaces or commas. An answer that lists filed rather than
		// words is not one this exchange reads, and it is not read as a list
		// either (scopesOf): a shape this endpoint does not write is not one
		// this package guesses at.
		Scopes string `json:"scopes"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return Minted{}, fmt.Errorf("reading the Bitbucket consumer exchange: the answer is not one this exchange wrote (%d bytes)", len(data))
	}
	if answer.AccessToken == "" {
		return Minted{}, errors.New("bitbucket: the consumer exchange returned no access token")
	}
	if answer.ExpiresIn <= 0 {
		return Minted{}, errors.New("bitbucket: the consumer exchange returned no life for its token, so it cannot be handed out as one that expires")
	}
	return Minted{
		Token:     answer.AccessToken,
		ExpiresAt: time.Now().Add(time.Duration(answer.ExpiresIn) * time.Second),
		Scopes:    scopesOf(answer.Scopes),
	}, nil
}

// scopesOf splits an answer's scopes field into the scope words it lists. An
// empty field is an empty list, which is the "the endpoint did not say" case
// Minted.Scopes documents, and every separator Bitbucket documents is read as
// one: the check that reads this (scripts/live-checks/check145.sh) splits the
// same field the same way, so the two cannot disagree about what a token lists.
func scopesOf(field string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(field, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' }) {
		out = append(out, w)
	}
	return out
}

// oauthErrorCode is the whole shape of an error code Bitbucket documents for
// a refused grant. An answer's error field that is anything else is text this
// exchange did not write, so it is dropped with the rest of the body.
var oauthErrorCode = regexp.MustCompile(`^[a-z_]{1,40}$`)

// refusal is the error of an exchange the endpoint refused. It carries the
// status and, when the answer holds one, Bitbucket's own error code -- never
// the body the code arrived in: the request carried the consumer's secret, so
// an endpoint (or anything between here and it) that prints back what it was
// given must not reach an error, a log or a caller through this (G5).
func refusal(status int, data []byte) error {
	var answer struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &answer) == nil && oauthErrorCode.MatchString(answer.Error) {
		return fmt.Errorf("bitbucket: the consumer exchange was refused: HTTP %d, error %s", status, answer.Error)
	}
	return fmt.Errorf("bitbucket: the consumer exchange was refused: HTTP %d", status)
}

// exchangeClient is the client that makes the exchange: the caller's, with a
// redirect refused, compression off, a bounded timeout and any cookie jar
// dropped, or a default one built the same way. Whatever the caller passed,
// the client this returns is a copy, so none of it changes the caller's own.
//
// The request carries the consumer's secret in its Authorization header, so
// it must not follow a redirect -- net/http would replay that header against
// a host that only answered the first request -- and it keeps no cookies
// either. A client with no timeout of its own is given one, rather than left
// to hold the exchange open against an endpoint that never answers.
func exchangeClient(c *http.Client) *http.Client {
	out := http.Client{}
	if c != nil {
		out = *c
	}
	if out.Timeout == 0 {
		out.Timeout = exchangeTimeout
	}
	out.Jar = nil
	out.Transport = exchangeTransport(out.Transport)
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// exchangeTransport returns t with compression off, cloned rather than
// changed, because the answer is one small JSON object: nothing here needs a
// compressed body, and an endpoint that decides how many bytes get read for
// the bytes it sends is not one this request hands anything to. A transport
// that is not http.Transport's -- a caller's own dialer, proxy or TLS config
// -- is returned as it is: replacing it would take away what the caller put
// there, and its compression behaviour is then the caller's own choice.
func exchangeTransport(t http.RoundTripper) http.RoundTripper {
	if t == nil {
		t = http.DefaultTransport
	}
	tr, ok := t.(*http.Transport)
	if !ok {
		return t
	}
	clone := tr.Clone()
	clone.DisableCompression = true
	return clone
}
