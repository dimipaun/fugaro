// The hop's shell: the one route, the order of the checks, and the log line.
package voicesvc

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config is what the service needs to run. Every member is required except the
// logger: a server missing its verifier, its device reader or its enforcer
// could not answer anything, and a server that started anyway would answer
// every call wrongly.
type Config struct {
	// Auth verifies the device's ID token (auth.go).
	Auth *Authenticator
	// Devices answers whether a verified device is still paired (status.go).
	Devices DeviceReader
	// Enforcer answers whether one project admits one call (policy.go).
	Enforcer *Enforcer
	// Keys is where a provider's credential comes from, for the engines that
	// present one (provider.go).
	Keys ProviderKeys
	// Tokens mints this service's own access token, for the engine that is
	// called as the installation's identity rather than with a key.
	Tokens TokenSource
	// Client makes the provider calls. It is required and not defaulted: the
	// TLS configuration, the timeouts and the transport of a call that carries
	// the installation's provider key are decisions the process that starts
	// this server has to make in the open (internal/cli/voice_serve.go).
	Client *http.Client
	// Log is where the one line per call goes. A nil logger discards.
	Log *slog.Logger
	// Now is the clock for the log's elapsed figure, replaced only by tests.
	Now func() time.Time
}

// Server is the hop's HTTP handler. It is safe for concurrent use.
type Server struct {
	auth     *Authenticator
	devices  DeviceReader
	enforcer *Enforcer
	// register is the engines this hop holds, parsed from the document in this
	// binary at start-up (register.go): a hop that started with an unreadable
	// register would refuse every call for a reason it could not name.
	register *Register
	keys     ProviderKeys
	tokens   TokenSource
	client   *http.Client
	log      *slog.Logger
	now      func() time.Time
}

// NewServer returns the handler.
func NewServer(cfg Config) (*Server, error) {
	switch {
	case cfg.Auth == nil:
		return nil, errors.New("voicesvc: the server needs the device verifier")
	case cfg.Devices == nil:
		return nil, errors.New("voicesvc: the server needs the device reader")
	case cfg.Enforcer == nil:
		return nil, errors.New("voicesvc: the server needs the policy enforcer")
	case cfg.Keys == nil:
		return nil, errors.New("voicesvc: the server needs a provider key source: without one, no call could be made to an engine that presents a key")
	case cfg.Tokens == nil:
		return nil, errors.New("voicesvc: the server needs its own token source: without one, no call could be made to an engine reached as the installation's identity")
	case cfg.Client == nil:
		return nil, errors.New("voicesvc: the server needs the HTTP client its provider calls are made with")
	}
	register, err := LoadRegister()
	if err != nil {
		return nil, err
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		auth: cfg.Auth, devices: cfg.Devices, enforcer: cfg.Enforcer,
		register: register, keys: cfg.Keys, tokens: cfg.Tokens, client: cfg.Client,
		log: log, now: now,
	}, nil
}

// ServeHTTP routes. This service has three routes — the project-status query
// and the two forwarding ones — and answers everything else with not_found,
// including the paths a provider API spells: what this service reaches is a
// constant endpoint of its own (provider.go, §8) and never a path a caller
// names.
//
// The status route is matched on the exact path and not on a prefix: a service
// whose surface is one path should not have a second one appear because a
// caller appended a slash. The two forwarding routes ARE matched by prefix,
// because the engine's name is the rest of the path and the name's own pattern
// (registerNameRE) is what bounds it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	switch {
	case r.URL.Path == StatusPath:
		if r.Method != http.MethodPost {
			s.refuseWith(w, start, callLog{act: "voice status"}, http.StatusMethodNotAllowed, ReasonBadRequest,
				"the project-status query is a POST carrying the project in its body")
			return
		}
		s.status(w, r, start)
	case strings.HasPrefix(r.URL.Path, STTPathPrefix), strings.HasPrefix(r.URL.Path, TTSPathPrefix):
		dir := STT
		if strings.HasPrefix(r.URL.Path, TTSPathPrefix) {
			dir = TTS
		}
		if r.Method != http.MethodPost {
			s.refuseWith(w, start, callLog{act: "voice forward", direction: dir.String()}, http.StatusMethodNotAllowed, ReasonBadRequest,
				"a forwarding call is a POST: the body is the audio, or the text to speak, and the project travels in "+HeaderProject)
			return
		}
		s.forward(w, r, start, dir)
	default:
		s.refuseWith(w, start, callLog{act: "voice status"}, http.StatusNotFound, ReasonNotFound,
			"this service answers three paths: the project-status query at "+StatusPath+", and the forwarding calls at "+STTPathPrefix+"<engine> and "+TTSPathPrefix+"<engine>")
	}
}

// status is the whole of the project-status query: who is calling, whether that
// device is still paired, and what the policy says about the project it named.
//
// The order is the order of the trust: the token first, because nothing a
// caller sent is a fact before it; the device node second, because a revoked
// device must be refused whatever it asked; and the request's own body last,
// because a caller that will be refused for its identity should not have this
// service read its payload at all.
func (s *Server) status(w http.ResponseWriter, r *http.Request, start time.Time) {
	cl := callLog{act: "voice status"}
	tok, ok := BearerToken(r.Header.Get(HeaderAuth))
	if !ok {
		s.refuseWith(w, start, cl, http.StatusUnauthorized, ReasonTokenUnverified,
			"present the device's Firebase ID token as "+HeaderAuth+": "+authScheme+"<token>")
		return
	}
	id, err := s.auth.VerifyDevice(r.Context(), tok)
	if err != nil {
		s.refuseErr(w, start, cl, err)
		return
	}
	cl.device = id.UID
	if _, err := s.devices.DeviceExists(r.Context(), id.UID); err != nil {
		s.refuseErr(w, start, cl, err)
		return
	}
	req, err := ReadStatusRequest(r)
	if err != nil {
		s.refuseErr(w, start, cl, refuse(ReasonBadRequest, err))
		return
	}
	cl.project = req.Project
	ans, err := s.enforcer.Answer(r.Context(), req.Project)
	if err != nil {
		s.refuseErr(w, start, cl, err)
		return
	}
	WriteAnswer(w, ans)
	cl.status, cl.remote, cl.reason, cl.elapsed = http.StatusOK, ans.Remote, ans.Reason, s.now().Sub(start)
	s.logCall(cl)
}

// refuseWith answers a refusal this file built, with the status this file
// chose. It is for the two the request's SHAPE decides — a path this service
// does not serve and a method it does not serve — where the reason alone would
// not carry the difference: `not_found` and `bad_request` are one reason each,
// while "this path is not mine at all" and "this path is mine but you knocked
// on it wrong" are two statuses.
//
// The status is an argument and not a lookup for that reason, and it is passed
// at the call rather than defaulted, so a reader sees which the answer is.
func (s *Server) refuseWith(w http.ResponseWriter, start time.Time, cl callLog, status int, reason, message string) {
	WriteWireError(w, status, WireError{Error: reason, Message: message})
	cl.status, cl.reason, cl.elapsed = status, reason, s.now().Sub(start)
	s.logCall(cl)
}

// refuseErr answers an error carrying a wire reason. The status is derived from
// the reason and not from the message: a handler that read a sentence to decide
// an HTTP status would change its behaviour when a sentence is edited, and half
// these sentences are another writer's text (internal/projectpolicy's refusal).
func (s *Server) refuseErr(w http.ResponseWriter, start time.Time, cl callLog, err error) {
	reason := ReasonOf(err)
	status := statusFor(reason)
	// The message is the error's own, bounded and made one line on the way out
	// (WriteWireError). It is NOT the log's: the log line carries the reason
	// and the error's type, so a sentence that quotes a hand-edited policy node
	// never reaches a log file (callLog).
	WriteWireError(w, status, WireError{Error: reason, Message: err.Error()})
	cl.status, cl.reason, cl.errorType, cl.elapsed = status, reason, errorType(err), s.now().Sub(start)
	s.logCall(cl)
}

// statusFor maps a reason to the HTTP status the app sees.
//
// The split that matters is 4xx against 5xx: everything the CALLER can fix is a
// 4xx (its token, its device's standing, its request), and everything this
// SERVICE could not do is a 5xx. A 5xx is the app's cue that this is not about
// the phone — the same distinction auth.go draws between a bad token and an
// unreadable key set, one level up.
func statusFor(reason string) int {
	switch reason {
	case ReasonTokenUnverified:
		return http.StatusUnauthorized
	case ReasonNotADevice, ReasonDeviceUnknown:
		return http.StatusForbidden
	case ReasonBadRequest, ReasonUnknownEngine:
		return http.StatusBadRequest
	case ReasonNotFound:
		return http.StatusNotFound
	case ReasonProviderFailed:
		return http.StatusBadGateway
	default:
		// state_unavailable, policy_unreadable and anything a later release
		// adds without a status: the service could not answer, and the app
		// falls back to local either way (§4.5).
		return http.StatusServiceUnavailable
	}
}

// callLog is one call's log line, in internal/gateway's callLog shape: the act,
// the names the service already knows, and the outcome — and no byte of any
// body.
//
// What is deliberately absent is the whole point of the type. There is no
// field for the request's body, the response's body, the ID token, the
// Authorization header or the refusal's message, so there is no way for a later
// edit to log one by accident: a body byte would have to be added as a field
// here first, in a diff a reviewer reads. §4.4 point 6 is the requirement, and
// this is how it is kept rather than promised.
//
// `device` and `project` are the two caller-supplied strings that DO appear,
// and each is safe for a reason of its own: a device uid is a key the operator
// prints (`fugaro pair ls`) and is shape-checked before it gets here, and the
// project is shape-checked to the slug internal/task.Slug produces — lowercase,
// digits and dashes. Neither is content, and neither is a credential.
type callLog struct {
	// act is which handler answered. It is a constant, not the request's path:
	// a caller-chosen path in a log line is a caller writing lines.
	act string
	// device is the verified uid, and "" when no token verified.
	device string
	// project is the slug the request named, and "" when the request was
	// refused before its body was read.
	project string
	// provider is the engine a forwarding call named, and "" for the status
	// query. It is the register's own name and is shape-checked to that
	// pattern before it gets here, so it is a name of this tree's and never a
	// caller's text.
	provider string
	// direction is "stt" or "tts" for a forwarding call, and "" for the status
	// query, which names no direction.
	direction string
	// providerStatus is what the PROVIDER answered with, on a forwarding call
	// that reached one. It is an integer, and it is the one figure an operator
	// reads a failed provider call from (a 401 is the installation's key, a 429
	// is its quota).
	providerStatus int
	// bytes is how many bytes of the answer the caller was sent, and it is a
	// COUNT and never content: no payload of this service is logged, and a
	// figure that says how long the answer was is not a byte of it.
	bytes int64
	// cut says the copy of the answer did not reach the provider's own end of
	// the body, and which end stopped it (the closed set below). It is a fact
	// about the call and not a refusal: a 200 and some of the answer were
	// already written when it happened.
	cut string
	// status is what the caller was answered with.
	status int
	// remote is the project-status answer's own field, logged only when there
	// is an answer.
	remote bool
	// reason is the wire reason, and "" when the call succeeded with a remote
	// answer.
	reason string
	// errorType is the Go type of a service-side failure, for an operator
	// reading a log: it names the reader that failed and never what it said.
	errorType string
	// elapsed is how long the call took.
	elapsed time.Duration
}

// logCall writes the one line. It is the only place this package logs.
func (s *Server) logCall(c callLog) {
	attrs := []any{
		"act", c.act,
		"device", c.device,
		"project", c.project,
		"status", c.status,
		"remote", c.remote,
		"elapsed_ms", c.elapsed.Milliseconds(),
	}
	if c.provider != "" {
		attrs = append(attrs, "provider", c.provider, "direction", c.direction)
	}
	if c.providerStatus != 0 {
		attrs = append(attrs, "provider_status", c.providerStatus, "bytes", c.bytes)
	}
	if c.cut != "" {
		attrs = append(attrs, "cut", c.cut)
	}
	if c.reason != "" {
		attrs = append(attrs, "reason", c.reason)
	}
	if c.errorType != "" {
		attrs = append(attrs, "error_type", c.errorType)
	}
	s.log.Info("voice call", attrs...)
}

// The two ends that can stop a copy of a provider's answer before the provider's
// own end of the body, from a closed set: which end is a fact an operator reads
// a truncated call from, and neither is a word any peer chose.
const (
	// cutProvider is the provider's body failing mid-answer.
	cutProvider = "provider"
	// cutClient is the write to the caller failing: the phone went away.
	cutClient = "client"
)

// maxErrorType bounds the type name in a log line. A Go type name is this
// tree's own text and is short; the bound is here so that nothing a wrapped
// error's type could be is unbounded in a log.
const maxErrorType = 64

// errorType names the kind of a service-side failure for the log, and "" for a
// refusal of the caller: a caller's own refusal needs no type, because its
// reason already says what was wrong, and a type here would be a second
// spelling of the same fact.
//
// It is the INNERMOST error's type and not the wrapper's: every error this
// package returns is a *RefusalError or a fmt wrap of one, so the wrapper's own
// name would say nothing, while the innermost is what tells an operator whether
// the database, the key set or the decoder failed.
func errorType(err error) string {
	var r *RefusalError
	if errors.As(err, &r) {
		switch r.Reason {
		case ReasonStateUnavailable, ReasonPolicyUnreadable:
		default:
			return ""
		}
		if r.Err != nil {
			err = r.Err
		}
	}
	for {
		next := errors.Unwrap(err)
		if next == nil {
			break
		}
		err = next
	}
	t := fmt.Sprintf("%T", err)
	if len(t) > maxErrorType {
		t = t[:maxErrorType]
	}
	return t
}
