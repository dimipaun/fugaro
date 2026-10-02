package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// QueueAllowance and Slack pad fx beyond the job's own timeout: a run may
// queue for an hour before it starts, and the clocks may differ a little.
const (
	QueueAllowance = time.Hour
	Slack          = 5 * time.Minute
)

// ExpiryFX is the fx claim for a run launched at launch whose job may run for
// jobTimeout: launch + job timeout + 1 h queueing + 5 m (R5). The rules stop
// honouring the identity then, whatever the tokens' own lifetimes.
func ExpiryFX(launch time.Time, jobTimeout time.Duration) time.Time {
	return launch.Add(jobTimeout + QueueAllowance + Slack)
}

// Signer signs a JWT claim set as one service account, which holds no roles
// itself: it exists only as the key (design §11).
type Signer interface {
	// Email is the signing account; it becomes iss and sub.
	Email() string
	// SignJWT returns the compact JWT for the JSON claim set payload.
	SignJWT(ctx context.Context, payload []byte) (string, error)
}

// Mint returns the Firebase custom token for the run c names, signed by
// signer through IAM Credentials signJwt. It never asks for an access token
// as the signer, and the launcher needs only the signJwt permission on it.
func Mint(ctx context.Context, signer Signer, c Claims) (string, error) {
	return MintAt(ctx, signer, c, time.Now())
}

// MintAt is Mint with the clock given: iat is now and exp is now + Lifetime
// (a retry mints a fresh token, with its own iat and exp).
func MintAt(ctx context.Context, signer Signer, c Claims, now time.Time) (string, error) {
	if err := c.validate(now); err != nil {
		return "", err
	}
	email := signer.Email()
	if email == "" {
		return "", errors.New("budget token: no signer")
	}
	payload, err := json.Marshal(map[string]any{
		"iss": email, "sub": email, "aud": Audience,
		"iat": now.Unix(), "exp": now.Add(Lifetime).Unix(),
		"uid": c.UID(),
		"claims": map[string]any{
			"fs": c.Slug, "fr": c.Run, "fx": c.FX.UnixMilli(), "fp": c.FP, "rb": c.RB,
		},
	})
	if err != nil {
		return "", err
	}
	tok, err := signer.SignJWT(ctx, payload)
	if err != nil {
		return "", err
	}
	// The signer is trusted, but a token that does not name the run it was
	// minted for must never leave this function.
	got, err := parseJWT(tok)
	if err != nil {
		return "", errors.New("budget token: the signer returned something that is not a JWT")
	}
	var want map[string]any
	if json.Unmarshal(payload, &want) != nil || claimString(got, "uid") != c.UID() {
		return "", fmt.Errorf("%w: the signer returned a token for another uid", ErrTokenMismatch)
	}
	return tok, nil
}

// IAMSigner is a Signer that calls IAM Credentials signJwt with the
// launcher's own credential.
type IAMSigner struct {
	email    string
	ts       oauth2.TokenSource
	endpoint string
	hc       *http.Client
}

// IAMOption configures NewIAMSigner.
type IAMOption func(*IAMSigner)

// WithIAMEndpoint points the signer at another base URL (tests).
func WithIAMEndpoint(u string) IAMOption { return func(s *IAMSigner) { s.endpoint = u } }

// WithIAMHTTPClient sets the HTTP client (tests); it must not add credentials.
func WithIAMHTTPClient(c *http.Client) IAMOption { return func(s *IAMSigner) { s.hc = c } }

var signerEmailRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*@[a-z0-9][a-z0-9.-]*\.iam\.gserviceaccount\.com$`)

// NewIAMSigner returns the signer for the service account email, called with
// the credential ts yields (the launcher's ADC). It refuses an email that is
// not a service account address.
func NewIAMSigner(email string, ts oauth2.TokenSource, opts ...IAMOption) (*IAMSigner, error) {
	if !signerEmailRE.MatchString(email) {
		return nil, errors.New("budget token: the signer must be a service account email (…@<project>.iam.gserviceaccount.com)")
	}
	if ts == nil {
		return nil, errors.New("budget token: no credential to call signJwt with")
	}
	s := &IAMSigner{email: email, ts: ts, endpoint: "https://iamcredentials.googleapis.com"}
	for _, o := range opts {
		o(s)
	}
	s.hc = newHTTPClient(s.hc)
	return s, nil
}

// Email implements Signer.
func (s *IAMSigner) Email() string { return s.email }

// SignJWT implements Signer. The service account is named in the path with
// the project wildcard "-", as the API documents.
func (s *IAMSigner) SignJWT(ctx context.Context, payload []byte) (string, error) {
	tok, err := s.ts.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode >= 400 && re.Response.StatusCode < 500 {
			return "", errors.New("budget token: the launcher's credential was refused (run gcloud auth application-default login)")
		}
		return "", fmt.Errorf("%w: fetching the launcher's credential failed", ErrUnavailable)
	}
	body, _ := json.Marshal(map[string]string{"payload": string(payload)})
	u := strings.TrimRight(s.endpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(s.email) + ":signJwt"
	hdr := http.Header{"Authorization": {"Bearer " + tok.AccessToken}}
	status, resp, err := httpDo(ctx, s.hc, http.MethodPost, u, "application/json", body, hdr)
	if err != nil {
		return "", err
	}
	if status == http.StatusForbidden {
		return "", fmt.Errorf("%w (%s): the launcher needs the fugaroTokenMinter role on it", ErrNotMinter, s.email)
	}
	if status/100 != 2 {
		return "", statusError("signJwt", status, resp, nil)
	}
	var out struct {
		SignedJWT string `json:"signedJwt"`
	}
	if json.Unmarshal(resp, &out) != nil || out.SignedJWT == "" {
		return "", errors.New("budget token: signJwt answered without a token")
	}
	return out.SignedJWT, nil
}
