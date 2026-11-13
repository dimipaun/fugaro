// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The API mount's own tests (portal plan P1.0): the table the areas register
// into, the one refusal shape, the cap on a route's body, and the caller's own
// address behind Hosting's rewrite.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The probe is one area's registration, written the way an area writes it: an
// area file registers its routes from its init (api.go), and this test file is
// the only file P1.0 has that can. A route registered here is answered although
// no line of the service names it, which is what "no later task edits
// service.go" means, and it reaches seams a deployment may be missing, so New's
// refusal about a mounted route's seam is a case a test can build.
const (
	apiProbePath  = "/api/probe/echo"
	apiProbeBytes = 1 << 10
)

// probe is the test's own route: what it was handed, so a test reads that a body
// under the cap reached the handler and one over it did not.
type probe struct {
	mu     sync.Mutex
	bodies [][]byte
}

var apiProbe = &probe{}

func (p *probe) serve(_ *Service, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	p.mu.Lock()
	p.bodies = append(p.bodies, body)
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// read is every body the probe was handed, in order.
func (p *probe) read() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.bodies...)
}

func init() {
	registerAPI(http.MethodPost, apiProbePath, apiProbe.serve,
		withBody(apiProbeBytes), reaching(needDocs, needSignIn))
}

// TestAPIRoutesComeFromTheRegistry: Handler answers what the table holds and
// nothing else, and the table refuses a route the rewrite cannot reach.
func TestAPIRoutesComeFromTheRegistry(t *testing.T) {
	f := newFixture(t)

	// A route registered from an init and named by no line of Handler is served,
	// which is the whole of what an area's file has to do.
	if got := f.api(http.MethodPost, apiProbePath, "hello"); got.Code != http.StatusNoContent {
		t.Fatalf("a route registered from an init answered %d, want %d: an area's door is the table's and not Handler's", got.Code, http.StatusNoContent)
	}

	// The intake's own API route is in the table too, and it answers at the
	// address Hosting's rewrite puts it at (F16).
	if _, known := apiRoutes.lookup(http.MethodPost, RouteWebhook); !known {
		t.Errorf("the merchant's route is not in the API's table, want it registered beside every area's")
	}
	if got := f.deliver(Event{}); got.Code != http.StatusOK {
		t.Errorf("the merchant's route answered %d, want 200: it is mounted from the table", got.Code)
	}

	// A path under the rewrite that no area registered is the subtree's own
	// refusal and not some other route's answer.
	unknown := f.api(http.MethodPost, "/api/nothing/here", "{}")
	if unknown.Code != http.StatusNotFound {
		t.Errorf("a path no area registered answered %d, want %d", unknown.Code, http.StatusNotFound)
	}

	// A route the rewrite cannot reach is refused where it is registered: the
	// subtree is Hosting's contract with this service (F21), and a route outside
	// it is one the SPA's own origin never reaches. A second registration of one
	// method and path is refused the same way, because one of the two would
	// never answer.
	for _, tc := range []struct {
		name string
		take func()
	}{
		{"a route outside the rewrite's subtree", func() { registerAPI(http.MethodGet, "/elsewhere", apiProbe.serve) }},
		{"a route at the subtree itself", func() { registerAPI(http.MethodGet, apiPrefix, apiProbe.serve) }},
		{"a route registered twice", func() { registerAPI(http.MethodPost, apiProbePath, apiProbe.serve) }},
		{"a route with no method", func() { registerAPI("", "/api/nothing", apiProbe.serve) }},
		{"a route with no handler", func() { registerAPI(http.MethodGet, "/api/nothing", nil) }},
		{"a route with no body cap", func() { registerAPI(http.MethodGet, "/api/nothing", apiProbe.serve, withBody(0)) }},
	} {
		if !panics(t, tc.take) {
			t.Errorf("%s was registered, want the table to refuse it: a table an area registers into at init has nobody to return an error to", tc.name)
		}
	}
}

// TestAPIErrorsAreUniform: every refusal the API itself makes is one shape — one
// content type, one object, a code and a sentence — because the SPA reads one
// thing and an operator greps one thing. None of them carries a CORS header,
// which is what "no CORS" is: no route here sets one, and Hosting serves the SPA
// from the same origin it rewrites /api/** from (F21).
func TestAPIErrorsAreUniform(t *testing.T) {
	f := newFixture(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{name: "a path no area registered", method: http.MethodPost, path: "/api/nothing/here", body: "{}", status: http.StatusNotFound, code: "no_route"},
		{name: "a method the route does not answer", method: http.MethodGet, path: RouteWebhook, status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
		{name: "a body over the route's cap", method: http.MethodPost, path: RouteWebhook, body: strings.Repeat("x", maxEventBytes+1), status: http.StatusRequestEntityTooLarge, code: "body_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.api(tc.method, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("the answer is %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != apiJSONType {
				t.Errorf("the refusal's content type is %q, want %q", got, apiJSONType)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("the refusal admits the origin %q, want no CORS header at all", got)
			}
			var held map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &held); err != nil {
				t.Fatalf("the refusal is not JSON: %v: %s", err, rec.Body.String())
			}
			if len(held) != 1 || held["error"] == nil {
				t.Fatalf("the refusal holds %v, want one `error` object", held)
			}
			var fault apiFault
			if err := json.Unmarshal(held["error"], &fault); err != nil {
				t.Fatalf("the refusal's error object does not decode: %v", err)
			}
			if fault.Code != tc.code {
				t.Errorf("the refusal's code is %q, want %q", fault.Code, tc.code)
			}
			if fault.Message == "" {
				t.Error("the refusal says nothing about the request, and a client with a code alone cannot tell a person what to do")
			}
		})
	}

	// The method refusal also says what the route does answer, which is the one
	// thing about a refusal a client can act on by itself.
	rec := f.api(http.MethodGet, RouteWebhook, "")
	if got := rec.Header().Get("Allow"); !strings.Contains(got, http.MethodPost) {
		t.Errorf("a method refusal allows %q, want it to name %s", got, http.MethodPost)
	}
}

// TestAPIBodyIsCapped: the cap is the route's own. A body exactly at it is read
// whole; one byte over it is refused before the handler is reached, and a second
// route with a larger cap still reads what the first refuses.
func TestAPIBodyIsCapped(t *testing.T) {
	f := newFixture(t)

	before := apiProbe.read()
	over := f.api(http.MethodPost, apiProbePath, strings.Repeat("x", apiProbeBytes+1))
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body one byte over the route's cap answered %d, want %d", over.Code, http.StatusRequestEntityTooLarge)
	}
	if got := apiProbe.read(); len(got) != len(before) {
		t.Errorf("the route over its cap was reached %d time(s), want none: the cap is the mount's and not the handler's", len(got)-len(before))
	}

	at := f.api(http.MethodPost, apiProbePath, strings.Repeat("y", apiProbeBytes))
	if at.Code != http.StatusNoContent {
		t.Fatalf("a body exactly at the route's cap answered %d, want %d", at.Code, http.StatusNoContent)
	}
	got := apiProbe.read()
	if len(got) != len(before)+1 || len(got[len(got)-1]) != apiProbeBytes {
		t.Errorf("the route at its cap read %d more body(s), want the one of %d byte(s)", len(got)-len(before), apiProbeBytes)
	}

	// The merchant's route reads a body orders of magnitude larger, so the same
	// bytes are over the probe's cap and well inside its own: the cap belongs to
	// the route and not to the mount.
	large := f.api(http.MethodPost, RouteWebhook, strings.Repeat("x", maxEventBytes))
	if large.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("the merchant's route refused a body of %d byte(s), want its own cap to read it", maxEventBytes)
	}
}

// TestNewRefusesASeamOnlyAMountedRouteReaches: the seams the API's routes reach
// are refused when the service starts, and only for the routes this deployment
// mounts: a deployment that serves no door of an area is not that area's seam's
// business (plan P1.0).
func TestNewRefusesASeamOnlyAMountedRouteReaches(t *testing.T) {
	f := newFixture(t)
	base := *f.cfg

	for _, tc := range []struct {
		name string
		miss func(*Deps)
	}{
		{name: "no document store", miss: func(d *Deps) { d.Docs = nil }},
		{name: "no sign-in verifier", miss: func(d *Deps) { d.SignIn = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := *f.deps
			tc.miss(&deps)
			_, err := New(base, deps)
			if err == nil {
				t.Fatalf("the service started with %s, want a refusal: a mounted route would answer every request with a failure of its own", tc.name)
			}
			if !strings.Contains(err.Error(), apiProbePath) {
				t.Errorf("the refusal is %q, want it to name the route that reaches the seam", err)
			}
		})
	}

	// The seams no mounted route reaches are not this deployment's: a service
	// that serves none of their doors starts without them.
	deps := *f.deps
	deps.Sealer, deps.Paddle, deps.Scheduler = nil, nil, nil
	if _, err := New(base, deps); err != nil {
		t.Errorf("a deployment holding no seam a mounted route reaches did not start: %v", err)
	}
}

// TestClientAddressBehindHostingIsTheCallers: F34 — the address a route counts a
// caller's rate by is the **last** entry of X-Forwarded-For, the one the
// platform's own front end appended, and never an entry a caller wrote. That is
// the intake's rule (service.go's clientIP) and the API adds no second one: the
// service answers everyone at its own address as well as through Hosting's
// rewrite (F21), so a request that arrives directly carries the caller's forged
// entries in front of the platform's append, and a rule that read the entry
// before the last would hand that caller a fresh bucket per forged line — the
// bypass a rate exists to refuse (§7.2).
//
// Which entry the platform appends behind Hosting — the caller, or the edge
// every caller shares — is F34 and stays unverified here: it is a property of
// the deployed chain, not of this code, and the reconciliation's open list
// carries it. No route may be built on an answer this test cannot give.
func TestClientAddressBehindHostingIsTheCallers(t *testing.T) {
	const (
		caller = "198.51.100.9"
		forged = "203.0.113.5"
		peer   = "10.0.0.2:443"
	)

	for _, tc := range []struct {
		name      string
		forwarded []string
		want      string
	}{
		{
			name:      "the one entry the platform appended",
			forwarded: []string{caller},
			want:      caller,
		},
		{
			name:      "an entry a caller wrote itself does not become the address a rate is counted by",
			forwarded: []string{forged + ", " + caller},
			want:      caller,
		},
		{
			name:      "however many entries a caller writes, across however many lines",
			forwarded: []string{forged, "192.0.2.7, 2001:db8::1", caller},
			want:      caller,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, intakeOrigin+apiProbePath, nil)
			for _, header := range tc.forwarded {
				req.Header.Add("X-Forwarded-For", header)
			}
			req.RemoteAddr = peer
			if got := clientIP(req); got != tc.want {
				t.Errorf("the address the request is counted by is %q, want %q", got, tc.want)
			}
		})
	}

	// No header at all — a local run, a test — and the peer is the only address
	// there is.
	bare := httptest.NewRequest(http.MethodPost, intakeOrigin+apiProbePath, nil)
	bare.RemoteAddr = peer
	if got, want := clientIP(bare), "10.0.0.2"; got != want {
		t.Errorf("a request with no forwarded address is %q, want the peer %q", got, want)
	}
}

// panics answers whether one registration panics, which is how the table's own
// invariants are read: an init has nobody to return an error to, and a table
// holding a route the service cannot serve is worse than a service that does not
// start.
func panics(t *testing.T, take func()) (panicked bool) {
	t.Helper()
	defer func() { panicked = recover() != nil }()
	take()
	return false
}
