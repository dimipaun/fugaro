// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The identity service's assertion route (mobile identity plan task A1): one
// POST carrying a Firebase ID token and the two names an assertion is bound to,
// answered with the assertion or with a refusal a client can branch on.
//
// **What a refusal says and what it does not.** Every refusal is one JSON
// object with a stable `code` and a sentence for a person, in the shape this
// repository's other services answer in. None of them echoes a value out of the
// request: the ID token is a stranger's bearer, an audience is a project's name
// and a challenge is a nonce, and a refusal reaches a log line, a person's phone
// and whatever is watching the service — so the codes are the vocabulary and the
// values stay out of it (F10).
//
// **Which refusal is which status.** A body that is not this route's request is
// the caller's mistake and answers 400; a token this project did not mint is the
// person's and answers 401; a fault of this service's own — a key set it cannot
// read, a signing call that failed — answers 503, so an operator looks at the
// deployment rather than at somebody's sign-in. The distinction is the one
// Sign's errors already carry, and this file maps it rather than deciding it.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

const (
	// RouteAssertion is the route the phones and the projects' apps call. It is
	// a path and not a versioned one: this service has one document to hand out
	// and the assertion inside it carries its own version through its claim set.
	RouteAssertion = "/assertion"

	// maxRequestBytes bounds a request body. The body is two short names.
	maxRequestBytes = 8 << 10
)

// The codes a client branches on. They are `lower_snake_case` and stable: an
// app switches on them, so renaming one is changing a wire format (CLAUDE.md).
const (
	codeInvalidRequest    = "invalid_request"
	codeUnverifiedIDToken = "unverified_id_token"
	codeUnavailable       = "assertion_unavailable"
	codeMethodNotAllowed  = "method_not_allowed"
)

// AssertionRequest is the route's request: the ONE project the assertion is for,
// and the challenge it is bound to. Both are required, and neither is defaulted
// — an assertion for an audience this service picked would be one its holder
// could replay at a project that never asked for it.
type AssertionRequest struct {
	// Audience is the project the assertion is for, as that project's own app
	// names itself.
	Audience string `json:"audience"`
	// Challenge is the QR nonce of a pairing or a service-issued restore
	// challenge (D2792).
	Challenge string `json:"challenge"`
}

// AssertionAnswer is the route's answer: the signed assertion, as the compact
// JWS the project's verifier reads.
type AssertionAnswer struct {
	Assertion string `json:"assertion"`
}

// WireRefusal is every refusal, under the one key a client reads it from.
type WireRefusal struct {
	Error WireFault `json:"error"`
}

// WireFault is the code a client branches on and the sentence a person reads.
type WireFault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Handler serves the assertion route over one signer.
type Handler struct {
	signer *Signer
	logf   func(format string, args ...any)
}

// HandlerOption configures the handler.
type HandlerOption func(*Handler)

// WithLogger sets where the refusals are written (tests). A deployment leaves
// it alone: the lines go to the platform's log.
func WithLogger(logf func(format string, args ...any)) HandlerOption {
	return func(h *Handler) {
		if logf != nil {
			h.logf = logf
		}
	}
}

// NewHandler returns the handler over one signer.
func NewHandler(signer *Signer, opts ...HandlerOption) *Handler {
	h := &Handler{signer: signer, logf: log.Printf}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Mux is the service's routes. The assertion route is the whole of it in A1;
// the JWKS publication A2 adds mounts here beside it.
func (h *Handler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(RouteAssertion, h)
	return mux
}

// ServeHTTP answers one call to the assertion route.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.refuse(w, nil, http.StatusMethodNotAllowed, codeMethodNotAllowed, "this route answers POST only")
		return
	}
	// A signer this handler was built without is a deployment mistake, and it is
	// answered as this service's own fault rather than as a person's sign-in:
	// the person's token is not what is wrong.
	if h.signer == nil {
		h.refuse(w, nil, http.StatusServiceUnavailable, codeUnavailable, "this service has no signer configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var req AssertionRequest
	if err := decodeRequest(r, &req); err != nil {
		h.refuse(w, err, http.StatusBadRequest, codeInvalidRequest, "the body is not this route's request")
		return
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		h.refuse(w, nil, http.StatusUnauthorized, codeUnverifiedIDToken, "the request carries no bearer ID token")
		return
	}
	assertion, err := h.signer.Sign(r.Context(), token, req.Audience, req.Challenge)
	switch {
	case err == nil:
	case errors.Is(err, ErrNoAudience), errors.Is(err, ErrNoChallenge):
		h.refuse(w, err, http.StatusBadRequest, codeInvalidRequest, "the request does not name the audience and the challenge an assertion is bound to")
		return
	case errors.Is(err, ErrUnverifiedIDToken):
		h.refuse(w, err, http.StatusUnauthorized, codeUnverifiedIDToken, "the identity project did not mint the ID token this request carries")
		return
	case errors.Is(err, ErrKeyNotServed):
		h.refuse(w, err, http.StatusServiceUnavailable, codeUnavailable, "this service cannot sign at the moment")
		return
	default:
		h.refuse(w, err, http.StatusServiceUnavailable, codeUnavailable, "this service cannot sign at the moment")
		return
	}
	writeJSON(w, http.StatusOK, AssertionAnswer{Assertion: assertion})
}

// refuse answers one refusal and logs one line for it.
//
// The line carries the code and the error and no value out of the request: the
// error is one of this package's own refusals, which are written to name a rule
// rather than a value, and the two names a caller sent never reach it. What an
// operator gets is which rule refused and how often, which is what a log is for
// here.
func (h *Handler) refuse(w http.ResponseWriter, err error, status int, code, message string) {
	if err != nil {
		h.logf("identitysvc: refused an assertion: code=%s: %v", code, err)
	} else {
		h.logf("identitysvc: refused an assertion: code=%s", code)
	}
	writeJSON(w, status, WireRefusal{Error: WireFault{Code: code, Message: message}})
}

// writeJSON writes one answer. The status and the body are written together
// because a refusal and an answer are the same shape here, and an encoding that
// fails after the status is written cannot be answered at all.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeRequest reads the one JSON object this route accepts. Unknown fields are
// refused rather than ignored: a client sending a field this service does not
// know is one whose author expected it to do something, and reading the body
// anyway would answer as though it had.
func decodeRequest(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("the body carries more than one JSON value")
	}
	return nil
}

// bearerToken reads the token out of an Authorization header, answering false
// for anything that is not exactly one bearer credential.
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
