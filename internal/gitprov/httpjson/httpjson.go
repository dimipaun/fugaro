// Package httpjson is the small JSON-over-HTTP client the provider adapters share.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds one request when Client.HTTP is nil.
const DefaultTimeout = 30 * time.Second

// maxErrorBody is how much of an error response StatusError keeps.
const maxErrorBody = 512

var defaultHTTP = &http.Client{Timeout: DefaultTimeout}

// Client sends JSON requests to one REST API.
type Client struct {
	BaseURL string       // prefixed to request paths that are not absolute URLs
	HTTP    *http.Client // nil means a client with DefaultTimeout
	// Auth returns the Authorization header value for a request; nil or ""
	// sends none.
	Auth   func(ctx context.Context) (string, error)
	Header http.Header // added to every request
}

// StatusError is a response outside 2xx.
type StatusError struct {
	Method string
	URL    string
	Status int
	Body   string // at most 512 bytes of the response body
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// Do sends in, JSON-encoded unless nil, with method to path, and decodes a
// 2xx response body into out unless out is nil or the body is empty.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	_, err := c.do(ctx, method, path, in, out)
	return err
}

// DoPage is Do for one page of a listing. It returns the Link header's
// rel="next" URL as a path relative to BaseURL, or "" on the last page.
// A next URL on another scheme or host than BaseURL's, or outside its
// path, is an error: following it would send the Authorization header
// somewhere else.
func (c *Client) DoPage(ctx context.Context, method, path string, in, out any) (string, error) {
	h, err := c.do(ctx, method, path, in, out)
	if err != nil {
		return "", err
	}
	next := nextLink(h.Values("Link"))
	if next == "" {
		return "", nil
	}
	rel, err := c.PagePath(next)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", method, path, err)
	}
	return rel, nil
}

// PagePath turns a next-page URL an API returned into a path relative to
// BaseURL, refusing one on another scheme or host (or outside BaseURL's
// path), so a listing's bearer token never leaves the API.
func (c *Client) PagePath(next string) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parsing the API root: %w", err)
	}
	u, err := url.Parse(next)
	if err != nil || !u.IsAbs() {
		return "", fmt.Errorf("paging left the API host: next page %q is not an absolute URL", redactQuery(next))
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || hostPort(u) != hostPort(base) || u.User != nil {
		return "", fmt.Errorf("paging left the API host: next page is on %s://%s, not %s://%s", u.Scheme, u.Host, base.Scheme, base.Host)
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/")
	p := u.EscapedPath()
	if prefix != "" && p != prefix && !strings.HasPrefix(p, prefix+"/") {
		return "", fmt.Errorf("paging left the API host: next page %s is outside %s", p, prefix)
	}
	rel := strings.TrimPrefix(p, prefix)
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	if u.RawQuery != "" {
		rel += "?" + u.RawQuery
	}
	return rel, nil
}

// hostPort is u's lower-cased host with its scheme's default port made explicit.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// redactQuery drops the query from a URL for an error message.
func redactQuery(s string) string {
	s, _, _ = strings.Cut(s, "?")
	return s
}

// nextLink returns the rel="next" target of RFC 8288 Link header values,
// or "".
func nextLink(values []string) string {
	for _, v := range values {
		for _, link := range strings.Split(v, ",") {
			target, params, ok := strings.Cut(strings.TrimSpace(link), ">")
			if !ok || !strings.HasPrefix(target, "<") {
				continue
			}
			for _, param := range strings.Split(params, ";") {
				k, val, ok := strings.Cut(strings.TrimSpace(param), "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(k), "rel") {
					continue
				}
				for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(val), `"`)) {
					if strings.EqualFold(rel, "next") {
						return strings.TrimPrefix(target, "<")
					}
				}
			}
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("encoding %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(data)
	}
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimSuffix(c.BaseURL, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range c.Header {
		req.Header[k] = append([]string(nil), vs...)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Auth != nil {
		v, err := c.Auth(ctx)
		if err != nil {
			return nil, fmt.Errorf("authenticating %s %s: %w", method, path, err)
		}
		if v != "" {
			req.Header.Set("Authorization", v)
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: reading response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		excerpt := strings.TrimSpace(string(data))
		if len(excerpt) > maxErrorBody {
			const ellipsis = "…"
			excerpt = strings.ToValidUTF8(excerpt[:maxErrorBody-len(ellipsis)], "") + ellipsis
		}
		return nil, &StatusError{Method: method, URL: req.URL.Path, Status: resp.StatusCode, Body: excerpt}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("%s %s: decoding response: %w", method, path, err)
		}
	}
	return resp.Header, nil
}
