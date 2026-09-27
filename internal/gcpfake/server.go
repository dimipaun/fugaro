// Package gcpfake serves minimal, stateful httptest fakes of the Google
// Cloud REST APIs Fugaro calls: GCS (JSON API), Cloud Run Admin v2, Cloud
// Logging v2, Secret Manager v1 and Cloud Build v1. Each fake implements
// only the calls Fugaro makes, and fails the test on anything else, so a
// client change that sends a new call is noticed.
package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Request is one call a fake received.
type Request struct {
	Method, Path, Query string
	Body                []byte
}

// Server records requests and hands them to a handler.
type Server struct {
	*httptest.Server
	t    *testing.T
	mu   sync.Mutex
	reqs []Request
	// failf reports an unhandled call; it is t.Errorf except in the fake's
	// own tests, which check that the refusal happens.
	failf func(format string, args ...any)
}

func newServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *Server {
	t.Helper()
	s := &Server{t: t, failf: t.Errorf}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		s.mu.Unlock()
		h(w, r, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// Requests returns the calls received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in Google's error shape, which googleapi parses.
func writeError(w http.ResponseWriter, code int, status, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "status": status, "message": msg}})
}

// unhandled fails the test for a call the fake doesn't implement. It
// answers 400, not 501: Google clients retry every 5xx on idempotent calls,
// so a 501 would stall the test in backoff instead of failing it at once.
func (s *Server) unhandled(w http.ResponseWriter, r *http.Request) {
	s.failf("gcpfake: unhandled %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	writeError(w, http.StatusBadRequest, "UNIMPLEMENTED", "not faked")
}
