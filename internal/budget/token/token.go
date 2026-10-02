// Package token gives a run its Firebase identity (design m9 §6.4, R5).
//
// The launcher mints a per-run custom token with IAM Credentials signJwt,
// signing as a service account that holds no roles (Mint), and leaves it in
// the runs bucket (PutObject). The runner takes it, which deletes it
// (TakeObject), and trades it for an ID token and a refresh token
// (Exchange). The Session keeps the ID token fresh through Secure Token,
// which keeps the uid and the claims, and hands the live ID token to
// internal/rtdb.
//
// Security posture. A token acts as one run: its uid is r~<slug>~<run> and
// the database rules read fs, fr, fx, fp and rb from it. So:
//   - nothing here ever puts a token or the API key in an error, a log line
//     or a String(); errors carry a fixed text, a status and, at most, the
//     server's upper-case error code;
//   - every secret is handed to Config.Register (the runner's redactor)
//     before the first network call that carries it, and again for each ID
//     token a refresh issues;
//   - a token is only used for the run its object path names, and the ID
//     token the server returns must carry the same uid and claims as the
//     custom token, otherwise it is refused;
//   - the credentials of the launcher (the signJwt caller) are an oauth2
//     token source supplied by the caller; this package never looks for
//     credentials of its own.
package token

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrUnavailable is a network failure, a 5xx or a 429 from Google: the
	// caller's grace clock (R6) runs, nothing was decided.
	ErrUnavailable = errors.New("budget token: backend unavailable")
	// ErrTokenExpired means the custom token is past its exp (it was minted
	// more than an hour ago): the run halts budget_token_expired.
	ErrTokenExpired = errors.New("budget token: expired")
	// ErrTokenInvalid means Google refused the custom token or the API key
	// (not a transient failure) or the token is not a well formed JWT.
	ErrTokenInvalid = errors.New("budget token: invalid")
	// ErrTokenMismatch means a token or an answer names another run or
	// carries other claims than the ones expected. It is never retried.
	ErrTokenMismatch = errors.New("budget token: names another run or carries other claims")
	// ErrRevoked means a refresh was refused permanently (the user was
	// deleted or disabled, the refresh token is no longer valid).
	ErrRevoked = errors.New("budget token: identity revoked")
	// ErrNoToken means the token object is not there (already taken, or
	// never written).
	ErrNoToken = errors.New("budget token: no token object")
	// ErrObjectExists means PutObject found a token object already there.
	ErrObjectExists = errors.New("budget token: token object already exists")
	// ErrNotMinter means the launcher may not sign as the signer (403): it
	// lacks the fugaroTokenMinter role on it.
	ErrNotMinter = errors.New("budget token: not allowed to sign as the token signer")
)

const (
	// Audience is the aud a custom token must carry.
	Audience = "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit"
	// Lifetime is how long a custom token is valid (Firebase's maximum).
	Lifetime = time.Hour

	maxUIDBytes  = 128
	maxFieldLen  = 200
	maxBodyBytes = 1 << 20
	httpTimeout  = 30 * time.Second
)

// Claims are the facts a run's token proves (R5).
type Claims struct {
	Slug string    // fs: the repository slug
	Run  string    // fr: the run id
	FX   time.Time // fx: when the identity stops being honoured by the rules
	FP   string    // fp: the Fugaro project (must equal /fugaro/project)
	RB   string    // rb: requested_by, the launcher's own identity
}

// UID is the Firebase uid of the run: r~<slug>~<run>.
func (c Claims) UID() string { return UID(c.Slug, c.Run) }

// UID returns the Firebase uid of a run.
func UID(slug, run string) string { return "r~" + slug + "~" + run }

// validSegment reports whether s may be a slug or run id inside a uid and an
// object path: non-empty, no '~' (the uid separator: slug "a~b" with run "c"
// must not collide with slug "a" and run "b~c"), no '/', no control or space.
func validSegment(s string) bool {
	if s == "" || len(s) > maxFieldLen || s == "." || s == ".." || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '~' || r == '/' || r == '\\' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func (c Claims) validate(now time.Time) error {
	switch {
	case !utf8.ValidString(c.Slug) || !utf8.ValidString(c.Run) || !utf8.ValidString(c.FP) || !utf8.ValidString(c.RB):
		return errors.New("budget token: a claim is not valid UTF-8")
	case !validSegment(c.Slug):
		return errors.New("budget token: invalid slug")
	case !validSegment(c.Run):
		return errors.New("budget token: invalid run id")
	case len(c.UID()) > maxUIDBytes:
		return errors.New("budget token: the uid is longer than 128 bytes")
	case c.FP == "" || len(c.FP) > maxFieldLen || strings.ContainsFunc(c.FP, unicode.IsControl):
		return errors.New("budget token: invalid project claim")
	case c.RB == "" || len(c.RB) > maxFieldLen || strings.ContainsFunc(c.RB, unicode.IsControl):
		return errors.New("budget token: invalid requested_by claim")
	case !c.FX.After(now):
		return errors.New("budget token: fx must be in the future")
	case c.FX.After(now.Add(30 * 24 * time.Hour)):
		return errors.New("budget token: fx is more than 30 days away")
	}
	return nil
}

// parseJWT decodes the payload of a JWT WITHOUT verifying it (Google does);
// it is for reading claims we minted or were issued.
func parseJWT(tok string) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("%w: not a JWT", ErrTokenInvalid)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("%w: bad payload encoding", ErrTokenInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: bad payload", ErrTokenInvalid)
	}
	return out, nil
}

func claimString(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func claimInt(m map[string]any, k string) (int64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Int64()
	return v, err == nil
}

// sameRun reports whether got (the payload of a token) names the run and
// carries the claims of want, which is the payload of the custom token.
func sameIdentity(got, want map[string]any) bool {
	uid := claimString(want, "uid")
	if uid == "" {
		return false
	}
	// Whichever of user_id and sub the token carries must be the uid, and at
	// least one must be there.
	uidGot, subGot := claimString(got, "user_id"), claimString(got, "sub")
	if (uidGot == "" && subGot == "") || (uidGot != "" && uidGot != uid) || (subGot != "" && subGot != uid) {
		return false
	}
	extra, _ := want["claims"].(map[string]any)
	if len(extra) == 0 {
		return false
	}
	for k, v := range extra {
		if fmt.Sprint(got[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

// httpDo performs one request and returns the status and a bounded body. A
// transport failure is ErrUnavailable with no URL (it can hold the API key)
// and no cause text; a cancelled context is returned as itself.
func httpDo(ctx context.Context, c *http.Client, method, u, contentType string, body []byte, hdr http.Header) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("budget token: building the request failed")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		var ne net.Error
		why := "request failed"
		if errors.As(err, &ne) && ne.Timeout() {
			why = "timed out"
		}
		return 0, nil, fmt.Errorf("%w: %s", ErrUnavailable, why)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, fmt.Errorf("%w: reading the answer failed", ErrUnavailable)
	}
	return resp.StatusCode, data, nil
}

// newHTTPClient returns c, or a default client; either way it never follows a
// redirect (a custom token must not be resent to a Location).
func newHTTPClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	}
	if out.Timeout == 0 {
		out.Timeout = httpTimeout
	}
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// errorCode extracts the server's upper-case error code from a Google error
// body ("INVALID_CUSTOM_TOKEN : detail" gives INVALID_CUSTOM_TOKEN) and
// nothing else, so an answer that echoes a credential cannot reach an error.
func errorCode(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	msg := e.Error.Message
	if i := strings.IndexAny(msg, " :"); i >= 0 {
		msg = msg[:i]
	}
	for _, r := range msg {
		if !(r >= 'A' && r <= 'Z' || r == '_' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return msg
}

// statusError classifies a non-2xx answer: 429 and 5xx are ErrUnavailable,
// anything else wraps sentinel (when not nil) with the status and code.
func statusError(op string, status int, body []byte, sentinel error) error {
	code := errorCode(body)
	suffix := fmt.Sprintf("%s: http %d", op, status)
	if code != "" {
		suffix += " " + code
	}
	if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status == 425 || status >= 500 {
		return fmt.Errorf("%w: %s", ErrUnavailable, suffix)
	}
	if sentinel == nil {
		return errors.New("budget token: " + suffix)
	}
	return fmt.Errorf("%w: %s", sentinel, suffix)
}

func keyURL(base, path, key string) string {
	return strings.TrimRight(base, "/") + path + "?key=" + url.QueryEscape(key)
}

// expiredAnswer reports whether a Google error body says a token expired
// (only the fact is used; the text is never copied anywhere).
func expiredAnswer(body []byte) bool {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &e) == nil && strings.Contains(strings.ToLower(e.Error.Message), "expired")
}

// checkURL requires a URL that will carry credentials to be https, or plain
// http to a loopback host (test fakes), with no userinfo, query or fragment.
func checkURL(what, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("budget token: %s is not a valid URL", what)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") || strings.Contains(raw, "?") {
		return fmt.Errorf("budget token: %s must not carry userinfo, a query or a fragment", what)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" {
			return nil
		}
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("budget token: %s must be https (http is allowed only to a loopback host)", what)
}
