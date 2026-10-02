// Package gcpfake serves minimal, stateful httptest fakes of the Google
// Cloud REST APIs Fugaro calls: GCS (JSON API), Cloud Run Admin v2, Cloud
// Logging v2, Secret Manager v1, Cloud Build v1 and a Firebase Realtime
// Database (RTDB, without security rules). Each fake implements
// only the calls Fugaro makes, and fails the test on anything else, so a
// client change that sends a new call is noticed.
package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// refusal, when set, answers every call instead of the handler (see
	// Refuse).
	refusal *apiError
	// disabled, when set, names the API the fake serves if a ServiceUsage
	// fake has it disabled; such a call is answered SERVICE_DISABLED.
	disabled func() (service, consumer string, off bool)
}

// apiError is an error answer in Google's shape.
type apiError struct {
	code           int
	status, reason string
	msg            string
}

func newServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *Server {
	t.Helper()
	s := &Server{t: t, failf: t.Errorf}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// The client went away mid-request (a cancelled context, say), so
			// the body is truncated: there is nothing to answer, and running
			// the handler on half a document would report a failure of the
			// fake's own making.
			return
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		refusal, disabled := s.refusal, s.disabled
		s.mu.Unlock()
		if refusal != nil {
			writeErrorInfo(w, refusal.code, refusal.status, refusal.msg, refusal.reason, nil)
			return
		}
		if disabled != nil {
			if service, consumer, off := disabled(); off {
				writeServiceDisabled(w, service, consumer)
				return
			}
		}
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

// Refuse makes the fake answer every call with code, status and msg, as
// Google does: with a google.rpc.ErrorInfo detail carrying reason when
// reason is not empty (a 403 for a missing permission carries
// IAM_PERMISSION_DENIED, say). A code of 0 lifts it.
func (s *Server) Refuse(code int, status, reason, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == 0 {
		s.refusal = nil
		return
	}
	s.refusal = &apiError{code: code, status: status, reason: reason, msg: msg}
}

// writeErrorInfo answers in Google's error shape with an ErrorInfo detail
// (domain googleapis.com) carrying reason and metadata, when reason is not
// empty.
func writeErrorInfo(w http.ResponseWriter, code int, status, msg, reason string, metadata map[string]string) {
	e := map[string]any{"code": code, "status": status, "message": msg}
	if reason != "" {
		info := map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "googleapis.com"}
		if metadata != nil {
			info["metadata"] = metadata
		}
		e["details"] = []any{info}
	}
	writeJSON(w, code, map[string]any{"error": e})
}

// writeServiceDisabled answers as Google does a call to an API that is
// disabled in the consumer project: 403 PERMISSION_DENIED, the legacy
// accessNotConfigured reason, and an ErrorInfo whose reason is
// SERVICE_DISABLED and whose metadata names the service.
func writeServiceDisabled(w http.ResponseWriter, service, consumer string) {
	num := strings.TrimPrefix(consumer, "projects/")
	msg := service + " has not been used in project " + num + " before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/" +
		service + "/overview?project=" + num + " then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry."
	writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{
		"code": http.StatusForbidden, "status": "PERMISSION_DENIED", "message": msg,
		"errors": []any{map[string]any{"message": msg, "domain": "usageLimits", "reason": "accessNotConfigured", "extendedHelp": "https://console.developers.google.com"}},
		"details": []any{
			map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "SERVICE_DISABLED", "domain": "googleapis.com",
				"metadata": map[string]string{"service": service, "consumer": consumer, "containerInfo": num,
					"activationUrl": "https://console.developers.google.com/apis/api/" + service + "/overview?project=" + num}},
			map[string]any{"@type": "type.googleapis.com/google.rpc.LocalizedMessage", "locale": "en-US", "message": msg},
			map[string]any{"@type": "type.googleapis.com/google.rpc.Help", "links": []any{map[string]any{
				"description": "Google developers console API activation", "url": "https://console.developers.google.com/apis/api/" + service + "/overview?project=" + num}}},
		},
	}})
}

// unhandled fails the test for a call the fake doesn't implement. It
// answers 400, not 501: Google clients retry every 5xx on idempotent calls,
// so a 501 would stall the test in backoff instead of failing it at once.
func (s *Server) unhandled(w http.ResponseWriter, r *http.Request) {
	s.failf("gcpfake: unhandled %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	writeError(w, http.StatusBadRequest, "UNIMPLEMENTED", "not faked")
}
