package rtdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// defaultTokenInfoURL is Google's token introspection endpoint: a GET with
// the access token as a query parameter (Google's own design for this one
// endpoint, not a header). The call is always https, and the token is never
// logged or wrapped into an error here.
const defaultTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

// WithTokenInfoURL points CallerEmail at another endpoint (tests).
func WithTokenInfoURL(u string) Option { return func(c *Client) { c.tokenInfoURL = u } }

// CallerEmail is the Google account email of this client's own credential:
// an OAuth2 token source only (a run's Firebase ID token names a run, not a
// person, so it has no such identity). It needs the token to carry the
// userinfo.email scope (budgetScopes does, for a person's ADC); otherwise, or
// for a service account with no verified email, it returns an error the
// caller falls back from.
func (c *Client) CallerEmail(ctx context.Context) (string, error) {
	if c.auth.Source == nil {
		return "", errors.New("rtdb: this client authenticates with a run's own ID token, which names no Google account")
	}
	tok, err := c.auth.Source.Token()
	if err != nil {
		return "", fmt.Errorf("rtdb: fetching the credential failed: %w", err)
	}
	base := c.tokenInfoURL
	if base == "" {
		base = defaultTokenInfoURL
	}
	u := base + "?access_token=" + url.QueryEscape(tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("rtdb: asking Google who the credential belongs to failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rtdb: asking Google who the credential belongs to failed: %d", resp.StatusCode)
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("rtdb: the tokeninfo answer is not readable: %w", err)
	}
	if info.Email == "" {
		return "", errors.New("rtdb: the credential names no account email (it may lack the userinfo.email scope)")
	}
	return info.Email, nil
}
