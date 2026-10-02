// Package rtdb is a small REST client for a Firebase Realtime Database,
// built for the budget backend (design m9-budget-and-dashboard §6.3). The
// Firebase Admin SDK cannot be used: its Go client authenticates as an
// admin, but a run holds only its own ID token.
//
// The client does GET (with ETag), PUT with if-match, atomic multi-path
// PATCH and Server-Sent-Event streams. Paths are node paths relative to the
// database root, already key-escaped (see budget.Key); they are URL-encoded
// here exactly once. Credentials never appear in an error or log line.
//
// Non-stream calls make one attempt (bounded by 30 s) with no retry or
// backoff: the caller owns retry, jitter and its outage clock. Update keys and
// paths must be built from budget.Path* (escaped keys); the client refuses
// empty, dotted or otherwise unescaped segments before sending. A database
// Date header has one-second resolution, so ServerNow can lag the true day by
// a moment near midnight: a lease refused as not_today is a retry with the
// fresh day, not a refusal.
//
// Errors wrap one of three sentinels, so a caller can tell a refusal from a
// stale write from an outage: ErrPermission (401, 403: a rule denied the
// write, or the credential is bad), ErrPrecondition (412: the ETag moved) and
// ErrUnavailable (the network, a 5xx or a 429). A cancelled context is
// returned as the context's own error. Any other error (a decode failure, a
// 4xx other than 401/403/412, an argument refused locally) is not an outage
// and not a refusal: callers must treat it as fail-closed.
package rtdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
)

var (
	// ErrPermission is a 401 or 403 answer.
	ErrPermission = errors.New("rtdb: permission denied")
	// ErrPrecondition is a 412 answer: the ETag no longer matches.
	ErrPrecondition = errors.New("rtdb: precondition failed")
	// ErrUnavailable is a network failure, a 5xx or a 429.
	ErrUnavailable = errors.New("rtdb: unavailable")
)

// Error is the error of a failed call; it unwraps to a sentinel above unless
// the status is another 4xx.
type Error struct {
	Op     string // "GET", "PUT", "PATCH", "STREAM"
	Path   string
	Status int // 0 for a network failure
	Msg    string
	kind   error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("rtdb: %s /%s", e.Op, e.Path)
	if e.Status != 0 {
		s += fmt.Sprintf(": %d", e.Status)
	}
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	return s
}

func (e *Error) Unwrap() error { return e.kind }

// Auth is how the client authenticates; exactly one field is set.
type Auth struct {
	// IDToken returns a run's Firebase ID token (sent as ?auth=). It is
	// called for every request, so a refreshed token is picked up.
	IDToken func() string
	// Source is an OAuth2 token source for people and the history job (sent
	// as a Bearer header).
	Source oauth2.TokenSource
}

// Option configures New.
type Option func(*Client)

// WithHTTPClient sets the HTTP client (default: a client with no timeout; each
// non-stream call is bounded by a 30 s context instead).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithStreamBackoff sets the reconnect delay: it starts at min, doubles on
// each consecutive failure up to max, and resets after a connection that
// delivered an event. Defaults 1 s and 30 s.
func WithStreamBackoff(min, max time.Duration) Option {
	return func(c *Client) { c.backMin, c.backMax = min, max }
}

// WithStreamIdleTimeout sets how long a stream may be silent before it is
// treated as dead and reconnected. The server sends a keep-alive about every
// 30 s; the default is 90 s.
func WithStreamIdleTimeout(d time.Duration) Option { return func(c *Client) { c.idle = d } }

const callTimeout = 30 * time.Second

// Client is safe for concurrent use.
type Client struct {
	base    *url.URL
	auth    Auth
	hc      *http.Client
	backMin time.Duration
	backMax time.Duration
	idle    time.Duration
	clock   atomic.Pointer[serverClock]
}

type serverClock struct{ server, local time.Time }

// New returns a client of the database at baseURL (e.g.
// "https://aurora-fp-default-rtdb.firebaseio.com").
func New(baseURL string, auth Auth, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("rtdb: %q is not an http(s) database URL", baseURL)
	}
	if (auth.IDToken == nil) == (auth.Source == nil) {
		return nil, errors.New("rtdb: set exactly one of Auth.IDToken and Auth.Source")
	}
	// A person's OAuth token must never travel in cleartext to a remote
	// host: with a token source, plain http is for loopback fakes only.
	if auth.Source != nil && u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("rtdb: %q: an OAuth token is only sent over https (or to a loopback address)", u.Host)
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = strings.TrimRight(u.Path, "/"), "", "", ""
	c := &Client{base: u, auth: auth, hc: &http.Client{}, backMin: time.Second, backMax: 30 * time.Second, idle: 90 * time.Second}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// ServerNow is the database server's current time, from the Date header of
// the latest response advanced by the local clock since. ok is false before
// any response. Resolution is one second.
func (c *Client) ServerNow() (time.Time, bool) {
	sc := c.clock.Load()
	if sc == nil {
		return time.Time{}, false
	}
	return sc.server.Add(time.Since(sc.local)).UTC(), true
}

// Get reads the node at path into out (a pointer; json.RawMessage works). A
// null node reports found=false and leaves out untouched.
func (c *Client) Get(ctx context.Context, path string, out any) (found bool, err error) {
	_, found, err = c.get(ctx, path, out, false)
	return found, err
}

// GetETag is Get that also returns the node's ETag ("null_etag" when the node
// is absent), for PutIfMatch.
func (c *Client) GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error) {
	return c.get(ctx, path, out, true)
}

func (c *Client) get(ctx context.Context, path string, out any, wantETag bool) (string, bool, error) {
	if err := validPath(path); err != nil {
		return "", false, err
	}
	hdr := http.Header{}
	if wantETag {
		hdr.Set("X-Firebase-ETag", "true")
	}
	res, err := c.do(ctx, http.MethodGet, path, nil, nil, hdr)
	if err != nil {
		return "", false, err
	}
	body := bytes.TrimSpace(res.body)
	if len(body) == 0 || string(body) == "null" {
		return res.etag, false, nil
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return "", false, fmt.Errorf("rtdb: GET /%s: decoding the node: %w", path, err)
		}
	}
	return res.etag, true, nil
}

// PutIfMatch writes v (nil deletes) at path only if the node's ETag is still
// etag; otherwise it returns ErrPrecondition. ETags work on one location: this
// is for single-node admin writes.
func (c *Client) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	if etag == "" {
		return fmt.Errorf("rtdb: PUT /%s: an empty etag is no precondition; read the node with GetETag first", path)
	}
	if err := validPath(path); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("rtdb: PUT /%s: %w", path, err)
	}
	hdr := http.Header{}
	hdr.Set("If-Match", etag)
	_, err = c.do(ctx, http.MethodPut, path, url.Values{"print": {"silent"}}, b, hdr)
	return err
}

// Patch updates several locations under root in one atomic request: every
// key of updates is a path relative to root (without a leading slash) and its
// value the new node (nil deletes). Either all of them are written or, if any
// is denied, none (ErrPermission). An empty update sends nothing.
func (c *Client) Patch(ctx context.Context, root string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	if err := validPath(root); err != nil {
		return err
	}
	for k := range updates {
		if k == "" || strings.HasPrefix(k, "/") || strings.HasSuffix(k, "/") {
			return fmt.Errorf("rtdb: PATCH /%s: update path %q must be relative and non-empty", root, k)
		}
		if err := validPath(k); err != nil {
			return err
		}
	}
	b, err := json.Marshal(updates)
	if err != nil {
		return fmt.Errorf("rtdb: PATCH /%s: %w", root, err)
	}
	_, err = c.do(ctx, http.MethodPatch, root, url.Values{"print": {"silent"}}, b, nil)
	return err
}

type result struct {
	body []byte
	etag string
}

// request builds a request for path with credentials; secret is the token to
// scrub from any error text.
func (c *Client) request(ctx context.Context, method, path string, q url.Values, body []byte, hdr http.Header) (req *http.Request, secret string, err error) {
	u := *c.base
	u.Path = u.Path + "/" + strings.Trim(path, "/") + ".json"
	if strings.Trim(path, "/") == "" {
		u.Path = c.base.Path + "/.json"
	}
	q = cloneValues(q)
	hd := http.Header{}
	switch {
	case c.auth.IDToken != nil:
		secret = c.auth.IDToken()
		q.Set("auth", secret)
	default:
		tok, terr := c.auth.Source.Token()
		if terr != nil {
			return nil, "", tokenErr(method, path, terr)
		}
		secret = tok.AccessToken
		hd.Set("Authorization", "Bearer "+tok.AccessToken)
	}
	u.RawQuery = q.Encode()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err = http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, secret, fmt.Errorf("rtdb: %s /%s: %v", method, path, scrub(err.Error(), secret))
	}
	for k, vs := range hd {
		req.Header[k] = vs
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, secret, nil
}

// tokenErr classifies a failed Source.Token(): the token endpoint refusing the
// credential (400, 401, 403: a revoked or expired grant) is ErrPermission;
// anything else (network, 5xx) is ErrUnavailable.
func tokenErr(method, path string, err error) error {
	e := &Error{Op: method, Path: path, Msg: "getting an access token: " + clip(err.Error()), kind: ErrUnavailable}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		e.Status = 0
		switch re.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			e.kind = ErrPermission
		}
	}
	return e
}

// validPath checks a node path: segments non-empty and free of the characters
// RTDB forbids in keys (. $ # [ ] and control characters; / separates). One
// leading or trailing slash is tolerated and ignored; the empty path is the
// root.
func validPath(path string) error {
	p := strings.Trim(path, "/")
	if p == "" {
		return nil
	}
	for _, sg := range strings.Split(p, "/") {
		if sg == "" || len(sg) > 768 || strings.ContainsFunc(sg, func(r rune) bool {
			return r < 0x20 || r == 0x7f || strings.ContainsRune(".$#[]", r)
		}) {
			return fmt.Errorf("rtdb: path %q has an empty or unescaped key (build it with budget.Path*)", path)
		}
	}
	return nil
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (c *Client) do(parent context.Context, method, path string, q url.Values, body []byte, hdr http.Header) (result, error) {
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	defer cancel()
	req, secret, err := c.request(ctx, method, path, q, body, hdr)
	if err != nil {
		return result{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return result{}, c.transportErr(parent, method, path, secret, err)
	}
	defer resp.Body.Close()
	c.observe(resp)
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return result{}, c.transportErr(parent, method, path, secret, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return result{body: b, etag: resp.Header.Get("ETag")}, nil
	}
	return result{}, statusErr(method, path, resp.StatusCode, b, secret)
}

// transportErr classifies a failure to talk to the server.
func (c *Client) transportErr(parent context.Context, op, path, secret string, err error) error {
	if perr := parent.Err(); perr != nil {
		return fmt.Errorf("rtdb: %s /%s: %w", op, path, perr)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // the URL carries the auth parameter
	}
	return &Error{Op: op, Path: path, Msg: scrub(err.Error(), secret), kind: ErrUnavailable}
}

func statusErr(op, path string, status int, body []byte, secret string) error {
	e := &Error{Op: op, Path: path, Status: status, Msg: scrub(serverMessage(body), secret)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.kind = ErrPermission
	case status == http.StatusPreconditionFailed:
		e.kind = ErrPrecondition
	case status >= 500 || status == http.StatusTooManyRequests:
		e.kind = ErrUnavailable
	}
	return e
}

// serverMessage is the {"error": "..."} text of an answer, or a clipped body.
func serverMessage(b []byte) string {
	var m struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &m) == nil && len(m.Error) > 0 {
		var s string
		if json.Unmarshal(m.Error, &s) == nil {
			return clip(s)
		}
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(m.Error, &o) == nil && o.Message != "" {
			return clip(o.Message)
		}
	}
	return clip(strings.TrimSpace(string(b)))
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func scrub(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// observe records the server's clock from a response.
func (c *Client) observe(resp *http.Response) {
	if d := resp.Header.Get("Date"); d != "" {
		if t, err := http.ParseTime(d); err == nil {
			c.clock.Store(&serverClock{server: t, local: time.Now()})
		}
	}
}

var (
	hostLegacy = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.firebaseio\.com$`)
	hostRegion = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.[a-z0-9-]+\.firebasedatabase\.app$`)
)

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ValidateURL checks that raw names a Firebase Realtime Database
// (https://<db>.firebaseio.com or https://<db>.<region>.firebasedatabase.app),
// with no userinfo, port, query, fragment or path. loopbackOK also admits
// http(s) to a loopback address, for fakes and emulators.
func ValidateURL(raw string, loopbackOK bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return fmt.Errorf("%q must be just the database's address, with no credentials, path, query or fragment", u.Host)
	}
	if loopbackOK && isLoopback(u.Hostname()) && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	host := strings.ToLower(u.Host)
	if u.Scheme != "https" || (!hostLegacy.MatchString(host) && !hostRegion.MatchString(host)) {
		return fmt.Errorf("%q must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app", raw)
	}
	return nil
}

// RootKeys lists the keys at the database root without downloading its data
// (a shallow read). An empty database has none.
func (c *Client) RootKeys(ctx context.Context) ([]string, error) {
	res, err := c.do(ctx, http.MethodGet, "", url.Values{"shallow": {"true"}}, nil, nil)
	if err != nil {
		return nil, err
	}
	body := bytes.TrimSpace(res.body)
	if len(body) == 0 || string(body) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("rtdb: GET /: decoding the root's keys: %w", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// settingsRules is where a database's security rules live.
const settingsRules = ".settings/rules"

// Rules reads the deployed security rules document.
func (c *Client) Rules(ctx context.Context) ([]byte, error) {
	res, err := c.do(ctx, http.MethodGet, settingsRules, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return res.body, nil
}

// PutRules deploys rules (a {"rules": {...}} document), replacing the
// database's security rules. It needs an admin credential.
func (c *Client) PutRules(ctx context.Context, rules []byte) error {
	if !json.Valid(rules) {
		return errors.New("rtdb: PUT /.settings/rules: the rules are not JSON")
	}
	_, err := c.do(ctx, http.MethodPut, settingsRules, url.Values{"print": {"silent"}}, rules, nil)
	return err
}
