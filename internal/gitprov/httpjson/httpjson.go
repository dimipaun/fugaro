// Package httpjson is the small JSON-over-HTTP client the provider adapters share.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(data)
	}
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimSuffix(c.BaseURL, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
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
			return fmt.Errorf("authenticating %s %s: %w", method, path, err)
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
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("%s %s: reading response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		excerpt := strings.TrimSpace(string(data))
		if len(excerpt) > maxErrorBody {
			excerpt = strings.ToValidUTF8(excerpt[:maxErrorBody], "") + "…"
		}
		return &StatusError{Method: method, URL: req.URL.Path, Status: resp.StatusCode, Body: excerpt}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
		}
	}
	return nil
}
