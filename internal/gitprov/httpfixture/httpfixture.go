// Package httpfixture replays recorded HTTP exchanges for the provider
// adapter tests, and records new ones from a live API (design §13).
//
// A fixture file is a JSON array of exchanges, served strictly in order.
// Paths may be written unescaped: they are compared after parsing, so
// ?q=source.branch.name="fugaro/x" matches its escaped form on the wire.
//
// A recorded path's query may itself have been scrubbed (see Recorder): a
// query parameter whose fixture value is "REDACTED" matches any value the
// replayed request sends for that parameter, so a secret that a Recorder
// removed from a path stays replayable without the original secret.
package httpfixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Exchange is one HTTP request and its response.
type Exchange struct {
	Method string `json:"method"`
	Path   string `json:"path"` // path and query
	// Auth, when set, is the Authorization header the request must carry.
	Auth string `json:"auth,omitempty"`
	// Request, when set, is the JSON body the request must carry,
	// compared as JSON values rather than as text.
	Request json.RawMessage `json:"request,omitempty"`
	Status  int             `json:"status"`
	// Header, when set, holds response headers, such as a Link to the
	// next page. "{{server}}" in a value stands for the replaying
	// server's URL, which a fixture can't know in advance.
	Header   map[string]string `json:"header,omitempty"`
	Response json.RawMessage   `json:"response,omitempty"`
}

// Server replays one fixture file.
type Server struct {
	*httptest.Server
	t         *testing.T
	mu        sync.Mutex
	exchanges []Exchange
	next      int
}

// Serve starts a server replaying the exchanges in file. The test fails if
// a request does not match the next exchange, or if any exchange is unused
// when the test ends.
func Serve(t *testing.T, file string) *Server {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t}
	if err := json.Unmarshal(data, &s.exchanges); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(func() {
		s.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.next < len(s.exchanges) {
			e := s.exchanges[s.next]
			t.Errorf("%s: %d exchanges unused, starting with %s %s", file, len(s.exchanges)-s.next, e.Method, e.Path)
		}
	})
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.exchanges) {
		s.t.Errorf("unexpected request %s %s (all %d exchanges used)", r.Method, r.URL.RequestURI(), len(s.exchanges))
		http.Error(w, "no more exchanges", http.StatusInternalServerError)
		return
	}
	e := s.exchanges[s.next]
	s.next++
	if msg := mismatch(e, r, body); msg != "" {
		s.t.Errorf("exchange %d (%s %s): %s", s.next, e.Method, e.Path, msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	for k, v := range e.Header {
		w.Header().Set(k, strings.ReplaceAll(v, "{{server}}", s.URL))
	}
	if len(e.Response) > 0 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(e.Status)
	w.Write(e.Response)
}

func mismatch(e Exchange, r *http.Request, body []byte) string {
	if r.Method != e.Method {
		return "method " + r.Method
	}
	want, err := url.Parse(e.Path)
	if err != nil {
		return "bad fixture path: " + err.Error()
	}
	wantQ, _ := url.ParseQuery(want.RawQuery)
	if r.URL.Path != want.Path || !queryMatches(wantQ, r.URL.Query()) {
		return "path " + r.URL.RequestURI()
	}
	if e.Auth != "" && r.Header.Get("Authorization") != e.Auth {
		return fmt.Sprintf("authorization %q", r.Header.Get("Authorization"))
	}
	if len(e.Request) > 0 {
		var got, exp any
		if err := json.Unmarshal(body, &got); err != nil {
			return "body is not JSON: " + string(body)
		}
		if err := json.Unmarshal(e.Request, &exp); err != nil {
			return "bad fixture request: " + err.Error()
		}
		if !reflect.DeepEqual(got, exp) {
			return "body " + string(body)
		}
	}
	return ""
}

// queryMatches reports whether got, the replayed request's query, satisfies
// want, the fixture's query. They must name the same parameters with the
// same number of values each, except that a "REDACTED" value in want (left
// there by a Recorder scrubbing a secret) matches any value in got.
func queryMatches(want, got url.Values) bool {
	if len(want) != len(got) {
		return false
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok || len(wv) != len(gv) {
			return false
		}
		for i, v := range wv {
			if v == "REDACTED" {
				continue
			}
			if v != gv[i] {
				return false
			}
		}
	}
	return true
}

// Recorder is an http.RoundTripper that forwards requests to Next and
// records each exchange, for turning a live run into fixture files. It
// never records the Authorization header, and replaces every value in
// Secrets with "REDACTED" wherever it appears — in the request and
// response bodies, and in the recorded path, including its query string.
// It also replaces every JSON "token" or "*_token" field in a response with "REDACTED"
// (scrubTokenFields), for credentials minted during the recording. Review
// a recording before committing it all the same: a secret in some other
// field, or one not listed in Secrets, is kept.
// A secret redacted from a query value replays via Server's REDACTED
// wildcard match (see queryMatches), without needing the original secret.
type Recorder struct {
	Next    http.RoundTripper // nil means http.DefaultTransport
	Secrets []string
	mu      sync.Mutex
	list    []Exchange
}

// RoundTrip implements http.RoundTripper.
func (rec *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		var err error
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	next := rec.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	rec.mu.Lock()
	rec.list = append(rec.list, Exchange{
		Method: req.Method, Path: rec.scrubString(req.URL.RequestURI()), Status: resp.StatusCode,
		Request: rec.scrub(reqBody), Response: scrubTokenFields(rec.scrub(respBody)),
	})
	rec.mu.Unlock()
	return resp, nil
}

// scrubString replaces every value in Secrets with "REDACTED" in s.
func (rec *Recorder) scrubString(s string) string {
	for _, secret := range rec.Secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "REDACTED")
		}
	}
	return s
}

func (rec *Recorder) scrub(body []byte) json.RawMessage {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	s := rec.scrubString(string(body))
	if !json.Valid([]byte(s)) {
		quoted, _ := json.Marshal(s) // keep a non-JSON body as a JSON string
		return quoted
	}
	return json.RawMessage(s)
}

// scrubTokenFields replaces the value of every JSON object field named
// "token" or ending in "_token" (access_token, refresh_token, id_token, …;
// see isTokenKey), at any depth, with "REDACTED". It covers credentials an API
// mints during a recording (a GitHub installation token), which the caller
// could not have listed in Secrets beforehand. Only responses are
// scrubbed this way: a request body must stay as sent to replay. A body
// with no such field is returned byte for byte.
func scrubTokenFields(body json.RawMessage) json.RawMessage {
	if len(body) == 0 || !bytes.Contains(bytes.ToLower(body), []byte(`token"`)) {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // keep large IDs exact
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	if !redactTokens(v) {
		return body
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}

// isTokenKey reports whether a JSON field named k holds a token: "token"
// itself, or any key ending in "_token". A key merely starting with it,
// such as "token_type", does not.
func isTokenKey(k string) bool {
	k = strings.ToLower(k)
	return k == "token" || strings.HasSuffix(k, "_token")
}

// redactTokens rewrites v in place and reports whether it changed anything.
func redactTokens(v any) bool {
	changed := false
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if _, isString := x.(string); isTokenKey(k) && isString {
				v[k] = "REDACTED"
				changed = true
			} else if redactTokens(x) {
				changed = true
			}
		}
	case []any:
		for _, x := range v {
			if redactTokens(x) {
				changed = true
			}
		}
	}
	return changed
}

// Exchanges returns what has been recorded so far.
func (rec *Recorder) Exchanges() []Exchange {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]Exchange(nil), rec.list...)
}

// Save writes the recorded exchanges to path as a fixture file.
func (rec *Recorder) Save(path string) error {
	data, err := json.MarshalIndent(rec.Exchanges(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
