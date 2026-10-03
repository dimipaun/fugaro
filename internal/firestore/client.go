// Package firestore is a small REST client for Cloud Firestore (native mode),
// built for the spend history (design m9-budget-and-dashboard §6.2, plan M9d).
// The Firestore SDK is not used: Fugaro needs a handful of calls, and a REST
// client is exercised end to end by the fake in internal/gcpfake.
//
// Operations: Get, Patch (an upsert whose updateMask lists every field it
// writes, so a map field is replaced whole), Delete, Query (a range on one
// field, paged), and GetDatabase/CreateDatabase for the one-time ensure step.
// Authentication is an OAuth2 token source (ADC), sent as a Bearer header; the
// token never appears in an error. Calls make one attempt, bounded by 30 s,
// with no retry: the caller owns that. Response bodies are bounded.
//
// Errors wrap a sentinel: ErrNotFound (404; ErrNoDatabase, which also is
// ErrNotFound, when the database itself is missing), ErrPermission (401, 403),
// ErrPrecondition (a currentDocument or updateTime precondition failed, or
// the document already exists) and ErrUnavailable (network, 5xx, 429, ABORTED).
// A cancelled context comes back as the context's error. Anything else
// (a decode failure, another 4xx, an argument refused locally) is none of
// those: treat it as fail-closed.
package firestore

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
	"time"

	"golang.org/x/oauth2"
)

var (
	ErrNotFound     = errors.New("firestore: not found")
	ErrNoDatabase   = fmt.Errorf("the database does not exist: %w", ErrNotFound)
	ErrPermission   = errors.New("firestore: permission denied")
	ErrPrecondition = errors.New("firestore: precondition failed")
	ErrUnavailable  = errors.New("firestore: unavailable")
)

// DefaultURL is the Firestore REST endpoint.
const DefaultURL = "https://firestore.googleapis.com"

// Error is the error of a failed call.
type Error struct {
	Op     string // "GET", "PATCH", "DELETE", "QUERY", "CREATE-DATABASE"...
	Path   string // document path, collection or database, never a query string
	Status int    // HTTP status, 0 for a network failure
	Code   string // google.rpc status, e.g. "NOT_FOUND"
	Msg    string
	kind   error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("firestore: %s %s", e.Op, e.Path)
	if e.Status != 0 {
		s += fmt.Sprintf(": %d", e.Status)
	}
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	return s
}

func (e *Error) Unwrap() error { return e.kind }

// Option configures New.
type Option func(*Client)

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithPageSize sets the documents per query page (default 300).
func WithPageSize(n int) Option { return func(c *Client) { c.pageSize = n } }

// WithPollInterval sets how often a long-running operation is polled (default 2 s).
func WithPollInterval(d time.Duration) Option { return func(c *Client) { c.poll = d } }

// WithoutQuotaProject stops sending X-Goog-User-Project. By default every call
// names the database's own project as the quota project, which user
// credentials (ADC from gcloud) need to call Google APIs.
func WithoutQuotaProject() Option { return func(c *Client) { c.quota = false } }

const (
	callTimeout = 30 * time.Second
	maxBody     = 16 << 20
	maxPages    = 10000
	maxPolls    = 300
)

// Client is safe for concurrent use. It addresses the (default) database.
type Client struct {
	base     *url.URL
	project  string
	src      oauth2.TokenSource
	hc       *http.Client
	quota    bool
	pageSize int
	poll     time.Duration
}

var projectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{3,28}[a-z0-9]$`)

// ValidateURL checks that raw is the Firestore endpoint (https, no userinfo,
// path, query or fragment) or, with loopbackOK, an http(s) loopback address.
func ValidateURL(raw string, loopbackOK bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return fmt.Errorf("%q must be just the endpoint's address, with no credentials, path, query or fragment", u.Host)
	}
	if loopbackOK && isLoopback(u.Hostname()) && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" || strings.ToLower(u.Host) != "firestore.googleapis.com" {
		return fmt.Errorf("%q must be https://firestore.googleapis.com", raw)
	}
	return nil
}

func isLoopback(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }

// New returns a client of project's (default) database at baseURL ("" is
// DefaultURL). The token is only sent over https, or to a loopback address.
func New(baseURL, project string, src oauth2.TokenSource, opts ...Option) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	if err := ValidateURL(baseURL, true); err != nil {
		return nil, fmt.Errorf("firestore: %w", err)
	}
	if !projectRE.MatchString(project) {
		return nil, fmt.Errorf("firestore: %q is not a project ID", project)
	}
	if src == nil {
		return nil, errors.New("firestore: a token source is required")
	}
	u, _ := url.Parse(baseURL)
	u.Path = ""
	c := &Client{base: u, project: project, src: src, hc: &http.Client{}, quota: true, pageSize: 300, poll: 2 * time.Second}
	for _, o := range opts {
		o(c)
	}
	if c.pageSize < 1 {
		c.pageSize = 1
	}
	return c, nil
}

// Doc is a document: its fields decoded to Go values (see Decode).
type Doc struct {
	Collection, ID string
	Fields         map[string]any
	CreateTime     string
	UpdateTime     string // RFC 3339, for IfUpdateTime
}

func (c *Client) dbPath() string { return "projects/" + c.project + "/databases/(default)" }

func (c *Client) docPath(coll, id string) (string, error) {
	if !validName(coll) || !validName(id) {
		return "", fmt.Errorf("firestore: %q/%q is not a document path", coll, id)
	}
	return c.dbPath() + "/documents/" + url.PathEscape(coll) + "/" + url.PathEscape(id), nil
}

// validName: a collection or document ID Fugaro would write: printable, no
// slash, not "." or "..", not __x__, at most 1500 bytes.
func validName(s string) bool {
	if s == "" || len(s) > 1500 || s == "." || s == ".." || (len(s) > 4 && strings.HasPrefix(s, "__") && strings.HasSuffix(s, "__")) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '/' })
}

// Get reads a document; a missing one is ErrNotFound.
func (c *Client) Get(ctx context.Context, coll, id string) (*Doc, error) {
	p, err := c.docPath(coll, id)
	if err != nil {
		return nil, err
	}
	var raw rawDoc
	if err := c.do(ctx, "GET", coll+"/"+id, http.MethodGet, "/v1/"+p, nil, nil, &raw); err != nil {
		return nil, err
	}
	return raw.decode()
}

// PatchOption refines Patch.
type PatchOption func(*patchOpts)

type patchOpts struct {
	exists     *bool
	updateTime string
	clear      []string
}

// MustExist makes the write fail (ErrNotFound) if the document is absent.
func MustExist() PatchOption { t := true; return func(o *patchOpts) { o.exists = &t } }

// MustNotExist makes the write fail (ErrPrecondition) if the document exists.
func MustNotExist() PatchOption { f := false; return func(o *patchOpts) { o.exists = &f } }

// IfUpdateTime makes the write fail (ErrPrecondition) unless the document's
// UpdateTime is still t.
func IfUpdateTime(t string) PatchOption { return func(o *patchOpts) { o.updateTime = t } }

// Clear deletes these top-level fields (they are in the mask, not in the body).
func Clear(fields ...string) PatchOption {
	return func(o *patchOpts) { o.clear = append(o.clear, fields...) }
}

// Patch writes fields into the document, creating it if absent. The
// updateMask lists every top-level field of fields (and of Clear), so each
// listed field, a map included, is replaced whole, and fields not listed are
// left alone: the result is a function of fields and the previous unlisted
// fields only. It returns the stored document.
func (c *Client) Patch(ctx context.Context, coll, id string, fields map[string]any, opts ...PatchOption) (*Doc, error) {
	p, err := c.docPath(coll, id)
	if err != nil {
		return nil, err
	}
	var o patchOpts
	for _, f := range opts {
		f(&o)
	}
	enc, err := EncodeFields(fields)
	if err != nil {
		return nil, err
	}
	var mask []string
	for k := range fields {
		mask = append(mask, k)
	}
	mask = append(mask, o.clear...)
	if len(mask) == 0 {
		return nil, errors.New("firestore: Patch with no fields writes nothing")
	}
	sort.Strings(mask)
	q := url.Values{}
	for i, k := range mask {
		if k == "" || (i > 0 && mask[i-1] == k) {
			return nil, fmt.Errorf("firestore: empty or repeated field %q", k)
		}
		q.Add("updateMask.fieldPaths", FieldPath(k))
	}
	if o.exists != nil {
		q.Set("currentDocument.exists", fmt.Sprint(*o.exists))
	}
	if o.updateTime != "" {
		q.Set("currentDocument.updateTime", o.updateTime)
	}
	var raw rawDoc
	if err := c.do(ctx, "PATCH", coll+"/"+id, http.MethodPatch, "/v1/"+p, q, map[string]any{"fields": enc}, &raw); err != nil {
		return nil, err
	}
	return raw.decode()
}

// Delete removes a document; deleting an absent one succeeds (as the
// service does) unless MustExist is given.
func (c *Client) Delete(ctx context.Context, coll, id string, opts ...PatchOption) error {
	p, err := c.docPath(coll, id)
	if err != nil {
		return err
	}
	var o patchOpts
	for _, f := range opts {
		f(&o)
	}
	q := url.Values{}
	if o.exists != nil {
		q.Set("currentDocument.exists", fmt.Sprint(*o.exists))
	}
	if o.updateTime != "" {
		q.Set("currentDocument.updateTime", o.updateTime)
	}
	return c.do(ctx, "DELETE", coll+"/"+id, http.MethodDelete, "/v1/"+p, q, nil, nil)
}

var simpleField = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*$`)

// FieldPath renders one field name as an updateMask path segment: bare when it
// is a simple identifier, else backticked with ` and \ escaped.
func FieldPath(name string) string {
	if simpleField.MatchString(name) {
		return name
	}
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`")
	return "`" + r.Replace(name) + "`"
}

// Query returns the documents of coll whose field lies in [from, to], in
// field order (ties by name). A nil bound is open. It filters on one field
// only, which needs no composite index. Pages are read with a cursor until a
// short page; a runaway is bounded.
func (c *Client) Query(ctx context.Context, coll, field string, from, to any) ([]Doc, error) {
	if !validName(coll) || field == "" {
		return nil, fmt.Errorf("firestore: bad query on %q.%q", coll, field)
	}
	var filters []any
	for _, b := range []struct {
		op string
		v  any
	}{{"GREATER_THAN_OR_EQUAL", from}, {"LESS_THAN_OR_EQUAL", to}} {
		if b.v == nil {
			continue
		}
		ev, err := Encode(b.v)
		if err != nil {
			return nil, err
		}
		filters = append(filters, map[string]any{"fieldFilter": map[string]any{
			"field": map[string]any{"fieldPath": FieldPath(field)}, "op": b.op, "value": ev}})
	}
	var out []Doc
	var cursor map[string]any
	for page := 0; page < maxPages; page++ {
		sq := map[string]any{
			"from": []any{map[string]any{"collectionId": coll}},
			"orderBy": []any{
				map[string]any{"field": map[string]any{"fieldPath": FieldPath(field)}, "direction": "ASCENDING"},
				map[string]any{"field": map[string]any{"fieldPath": "__name__"}, "direction": "ASCENDING"},
			},
			"limit": c.pageSize,
		}
		switch len(filters) {
		case 0:
		case 1:
			sq["where"] = filters[0]
		default:
			sq["where"] = map[string]any{"compositeFilter": map[string]any{"op": "AND", "filters": filters}}
		}
		if cursor != nil {
			sq["startAt"] = cursor
		}
		var rows []struct {
			Document *rawDoc `json:"document"`
		}
		if err := c.do(ctx, "QUERY", coll, http.MethodPost, "/v1/"+c.dbPath()+"/documents:runQuery", nil, map[string]any{"structuredQuery": sq}, &rows); err != nil {
			return nil, err
		}
		n := 0
		var last *rawDoc
		for _, r := range rows {
			if r.Document == nil {
				continue // the readTime-only row of an empty result
			}
			d, err := r.Document.decode()
			if err != nil {
				return nil, err
			}
			out = append(out, *d)
			last = r.Document
			n++
		}
		if n < c.pageSize {
			return out, nil
		}
		fv, ok := last.Fields[field]
		if !ok {
			return nil, &Error{Op: "QUERY", Path: coll, Msg: "a result lacks the ordered field " + field}
		}
		cursor = map[string]any{"values": []any{fv, map[string]any{"referenceValue": last.Name}}, "before": false}
	}
	return nil, &Error{Op: "QUERY", Path: coll, Msg: fmt.Sprintf("more than %d pages", maxPages)}
}

// Database is the (default) database's settings.
type Database struct {
	Name             string
	LocationID       string
	Type             string // FIRESTORE_NATIVE
	DeleteProtection string // DELETE_PROTECTION_ENABLED / _DISABLED
}

type rawDB struct {
	Name                  string `json:"name"`
	LocationID            string `json:"locationId"`
	Type                  string `json:"type"`
	DeleteProtectionState string `json:"deleteProtectionState"`
}

func (r rawDB) db() *Database {
	return &Database{Name: r.Name, LocationID: r.LocationID, Type: r.Type, DeleteProtection: r.DeleteProtectionState}
}

// GetDatabase reads the (default) database; none yet is ErrNotFound.
func (c *Client) GetDatabase(ctx context.Context) (*Database, error) {
	var raw rawDB
	if err := c.do(ctx, "GET", "database", http.MethodGet, "/v1/"+c.dbPath(), nil, nil, &raw); err != nil {
		return nil, err
	}
	return raw.db(), nil
}

// CreateDatabase creates the (default) native-mode database in location and
// waits for the operation. The location is permanent. An existing database is
// ErrPrecondition (never replaced).
func (c *Client) CreateDatabase(ctx context.Context, location string, deleteProtection bool) (*Database, error) {
	if location == "" || strings.ContainsAny(location, " /") {
		return nil, fmt.Errorf("firestore: %q is not a location", location)
	}
	dp := "DELETE_PROTECTION_DISABLED"
	if deleteProtection {
		dp = "DELETE_PROTECTION_ENABLED"
	}
	var op struct {
		Name  string `json:"name"`
		Done  bool   `json:"done"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body := map[string]any{"locationId": location, "type": "FIRESTORE_NATIVE", "deleteProtectionState": dp}
	q := url.Values{"databaseId": {"(default)"}}
	if err := c.do(ctx, "CREATE-DATABASE", "database", http.MethodPost, "/v1/projects/"+c.project+"/databases", q, body, &op); err != nil {
		return nil, err
	}
	for i := 0; !op.Done; i++ {
		if i >= maxPolls || op.Name == "" || strings.Contains(op.Name, "..") {
			return nil, &Error{Op: "CREATE-DATABASE", Path: "database", Msg: "the operation did not finish"}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("firestore: CREATE-DATABASE database: %w", ctx.Err())
		case <-time.After(c.poll):
		}
		op.Error = nil
		if err := c.do(ctx, "GET", "operation", http.MethodGet, "/v1/"+op.Name, nil, nil, &op); err != nil {
			return nil, err
		}
	}
	if op.Error != nil {
		return nil, &Error{Op: "CREATE-DATABASE", Path: "database", Msg: clip(op.Error.Message)}
	}
	return c.GetDatabase(ctx)
}

type rawDoc struct {
	Name       string         `json:"name"`
	Fields     map[string]any `json:"fields"`
	CreateTime string         `json:"createTime"`
	UpdateTime string         `json:"updateTime"`
}

func (r *rawDoc) decode() (*Doc, error) {
	i := strings.LastIndex(r.Name, "/documents/")
	if i < 0 {
		return nil, fmt.Errorf("firestore: unexpected document name %q", clip(r.Name))
	}
	parts := strings.Split(r.Name[i+len("/documents/"):], "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("firestore: unexpected document name %q", clip(r.Name))
	}
	f, err := DecodeFields(r.Fields)
	if err != nil {
		return nil, fmt.Errorf("firestore: document %s: %w", clip(r.Name), err)
	}
	coll, _ := url.PathUnescape(parts[0])
	id, _ := url.PathUnescape(parts[1])
	return &Doc{Collection: coll, ID: id, Fields: f, CreateTime: r.CreateTime, UpdateTime: r.UpdateTime}, nil
}

func (c *Client) do(parent context.Context, op, what, method, path string, q url.Values, body any, out any) error {
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	defer cancel()
	tok, err := c.src.Token()
	if err != nil {
		return tokenErr(op, what, err)
	}
	secret := tok.AccessToken
	u := *c.base
	u.Path = path
	u.RawQuery = q.Encode()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("firestore: %s %s: %w", op, what, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return fmt.Errorf("firestore: %s %s: %s", op, what, scrub(err.Error(), secret))
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if c.quota {
		req.Header.Set("X-Goog-User-Project", c.project)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.transportErr(parent, op, what, secret, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return c.transportErr(parent, op, what, secret, err)
	}
	if len(b) > maxBody {
		return &Error{Op: op, Path: what, Status: resp.StatusCode, Msg: "response too large"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr(op, what, resp.StatusCode, b, secret)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("firestore: %s %s: decoding the answer: %w", op, what, err)
		}
	}
	return nil
}

func (c *Client) transportErr(parent context.Context, op, what, secret string, err error) error {
	if perr := parent.Err(); perr != nil {
		return fmt.Errorf("firestore: %s %s: %w", op, what, perr)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return &Error{Op: op, Path: what, Msg: scrub(err.Error(), secret), kind: ErrUnavailable}
}

func tokenErr(op, what string, err error) error {
	e := &Error{Op: op, Path: what, Msg: "getting an access token: " + clip(err.Error()), kind: ErrUnavailable}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		switch re.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			e.kind = ErrPermission
		}
	}
	return e
}

func statusErr(op, what string, status int, body []byte, secret string) error {
	var env struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(body, &env) == nil && env.Error.Message != "" {
		msg = env.Error.Message
	} else {
		msg = strings.TrimSpace(string(body))
	}
	e := &Error{Op: op, Path: what, Status: status, Code: env.Error.Status, Msg: scrub(clip(msg), secret)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.kind = ErrPermission
	case status == http.StatusNotFound:
		e.kind = ErrNotFound
		if strings.Contains(msg, "database") && strings.Contains(msg, "does not exist") {
			e.kind = ErrNoDatabase
		}
	case status == http.StatusPreconditionFailed || status == http.StatusConflict && env.Error.Status == "ALREADY_EXISTS",
		env.Error.Status == "FAILED_PRECONDITION" || env.Error.Status == "ALREADY_EXISTS":
		e.kind = ErrPrecondition
	case status >= 500 || status == http.StatusTooManyRequests || env.Error.Status == "ABORTED":
		e.kind = ErrUnavailable
	}
	return e
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
