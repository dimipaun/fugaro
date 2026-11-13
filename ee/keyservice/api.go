// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's API mount (portal plan P1.0): the door Firebase Hosting rewrites
// /api/** to (F21), the table every area registers its routes into, the one
// refusal shape, the cap on a route's body, the caller's address a rate is
// counted by, and the seams the areas' routes reach.
//
// The table is a package-level registry rather than a list in Handler because
// the portal is built by one task per area (the plan's slices): an area file
// registers its own routes from its init, so adding a door edits no file another
// area holds. service.go has two writers in the plan and no area may be a third.
//
// A deployment that mounts no route is one this file does nothing for: P1.0 is
// inert alone, and every seam below is a shape a later area fills.
//
// The caller's address is the intake's own rule (service.go's clientIP) and
// there is deliberately no second reader here. F34 — whether the entry the
// platform appends behind Hosting is the caller's or the Hosting edge's — is not
// settled by any test in this repository: it is a property of the deployed
// chain, and the reconciliation's open list still carries it. What is settled is
// the half a rate rests on, which is that the entry read is the platform's own
// append and never one a caller wrote: the service answers everyone at its own
// address too (F21's rewrite is not the only way in), and a caller that reached
// it directly with one entry of its own in front of the platform's would name
// its own bucket under any rule that read further left.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// apiPrefix is everything Firebase Hosting rewrites to this service (F21).
	// A route outside it is one the SPA's own origin never reaches, which is why
	// the table refuses one where it is registered.
	apiPrefix = "/api/"

	// apiJSONType is what the API writes, refusals included, so a client parses
	// one thing.
	apiJSONType = "application/json"

	// maxAPIJSONBytes is the body one of the API's own routes reads unless it
	// names its own. Every route has a cap, this one or a stated one, because a
	// door a stranger can post to is a door whose body the service has to hold.
	maxAPIJSONBytes = 64 << 10
)

// apiHandler is one route's handler: a method on Service, so an area registers
// (*Service).itsDoor and serves the service Handler belongs to.
type apiHandler func(*Service, http.ResponseWriter, *http.Request)

// apiNeed names one seam a route reaches.
//
// A route states what it reaches so a deployment that does not hold the seam is
// refused when it starts rather than answering a failure at every request: a
// door that reads the merchant of record's client and holds none is a door
// nothing can pass. What a route does not state is not its business, and that is
// the point — a deployment mounting no door of an area needs none of that area's
// seams.
type apiNeed int

const (
	needDocs apiNeed = iota
	needSignIn
	needPaddle
	needScheduler
	needSealer
)

// String is the seam's own name, for the refusal's sentence.
func (n apiNeed) String() string {
	switch n {
	case needDocs:
		return "the document store"
	case needSignIn:
		return "the sign-in verifier"
	case needPaddle:
		return "the merchant of record's client"
	case needScheduler:
		return "the scheduler's token verifier"
	case needSealer:
		return "the handover's sealer"
	}
	return "a seam this build does not know"
}

// held answers whether this deployment holds the seam. A need this build does
// not know is not held, which refuses the start rather than mounting a route
// whose seam nothing checked.
func (n apiNeed) held(deps Deps) bool {
	switch n {
	case needDocs:
		return deps.Docs != nil
	case needSignIn:
		return deps.SignIn != nil
	case needPaddle:
		return deps.Paddle != nil
	case needScheduler:
		return deps.Scheduler != nil
	case needSealer:
		return deps.Sealer != nil
	}
	return false
}

// apiRoute is one mounted door: what it answers, how much of a body it reads,
// what it reaches, and the handler.
type apiRoute struct {
	method   string
	path     string
	maxBytes int64
	needs    []apiNeed
	serve    apiHandler
}

// apiOption is what a route states about itself beside its method, its path and
// its handler.
type apiOption func(*apiRoute)

// withBody is the cap on one route's body. It is the route's own and not the
// mount's because the bodies differ by orders of magnitude: a request the SPA
// makes is kilobytes and a merchant's event is hundreds.
func withBody(maxBytes int64) apiOption {
	return func(route *apiRoute) { route.maxBytes = maxBytes }
}

// reaching names the seams a route reaches, which New refuses the deployment
// over when it holds none of them.
func reaching(needs ...apiNeed) apiOption {
	return func(route *apiRoute) { route.needs = append(route.needs, needs...) }
}

// apiRegistry is the API's table: every route an area registered, in the order
// its init ran, and the index a dispatch reads.
type apiRegistry struct {
	mu     sync.Mutex
	routes []apiRoute
	byPath map[string][]int
}

// apiRoutes is the one table Handler mounts and New checks.
var apiRoutes = &apiRegistry{byPath: map[string][]int{}}

// registerAPI mounts one route of the API. An area file calls it from its init,
// which is why a mistake here is a panic: an init has nobody to return an error
// to, and a table holding a route the service cannot dispatch, cannot tell from
// another, or cannot serve behind the rewrite is worse than a service that does
// not start at all.
func registerAPI(method, path string, h apiHandler, opts ...apiOption) {
	route := apiRoute{method: method, path: path, maxBytes: maxAPIJSONBytes, serve: h}
	for _, opt := range opts {
		opt(&route)
	}
	apiRoutes.add(route)
}

func (rg *apiRegistry) add(route apiRoute) {
	switch {
	case route.method == "":
		panic("keyservice: the API route " + route.path + " names no method")
	case route.serve == nil:
		panic("keyservice: the API route " + route.method + " " + route.path + " has no handler")
	case !strings.HasPrefix(route.path, apiPrefix):
		panic("keyservice: the API route " + route.method + " " + route.path + " is not under " + apiPrefix + ", the subtree Hosting rewrites to this service")
	case route.path == apiPrefix:
		panic("keyservice: the API route " + route.method + " " + apiPrefix + " is the subtree itself, which Handler mounts for every path no area registered: the two patterns are one, and the mux refuses the pair when it is built")
	case route.maxBytes < 1:
		panic("keyservice: the API route " + route.method + " " + route.path + " has no body cap, and a route a stranger can post to is one whose body the service has to hold")
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, held := range rg.routes {
		if held.method == route.method && held.path == route.path {
			panic("keyservice: the API route " + route.method + " " + route.path + " is registered twice, and one of the two would never answer")
		}
	}
	rg.byPath[route.path] = append(rg.byPath[route.path], len(rg.routes))
	rg.routes = append(rg.routes, route)
}

// lookup answers the route one method and path name. The path is the pattern the
// request matched and not the request's own path, so a pattern's wildcards are
// the registering area's business and not this table's.
func (rg *apiRegistry) lookup(method, path string) (apiRoute, bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, i := range rg.byPath[path] {
		if rg.routes[i].method == method {
			return rg.routes[i], true
		}
	}
	return apiRoute{}, false
}

// methods is what one path answers, in the order the areas registered them.
func (rg *apiRegistry) methods(path string) []string {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	methods := make([]string, 0, len(rg.byPath[path]))
	for _, i := range rg.byPath[path] {
		methods = append(methods, rg.routes[i].method)
	}
	return methods
}

// paths is every registered path, sorted, so a Handler built twice mounts the
// same table in the same order.
func (rg *apiRegistry) paths() []string {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	paths := make([]string, 0, len(rg.byPath))
	for path := range rg.byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// missing refuses a deployment that holds no seam one of the mounted routes
// reaches. It is asked at New, and only about the routes this deployment mounts.
//
// Every route reaching the seam is named and not the first one found: a
// deployment that holds none holds it for all of them, and an operator who
// supplied the seam one name asked for would meet the next name at the start
// after it.
func (rg *apiRegistry) missing(deps Deps) error {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, route := range rg.routes {
		for _, need := range route.needs {
			if need.held(deps) {
				continue
			}
			var mounts []string
			for _, other := range rg.routes {
				if slices.Contains(other.needs, need) {
					mounts = append(mounts, other.method+" "+other.path)
				}
			}
			return fmt.Errorf("the API mounts %s, which reach %s, and this deployment has none: each of them would answer every request with a failure of its own (plan P1.0)", strings.Join(mounts, ", "), need)
		}
	}
	return nil
}

// mount adds the table to the mux: one pattern per registered path, dispatching
// the method the route names, and the subtree's own answer for a path no area
// registered.
//
// No route here sets a CORS header, and that is the whole of "no CORS": Hosting
// serves the SPA from the origin it rewrites /api/** from (F21), so the browser
// calls this same-origin, and a header admitting another origin would admit it
// to an API whose answers are a customer's own records.
func (rg *apiRegistry) mount(mux *http.ServeMux, s *Service) {
	for _, path := range rg.paths() {
		mux.HandleFunc(path, s.apiDispatch(path))
	}
	mux.HandleFunc(apiPrefix, unknownAPIRoute)
}

// unknownAPIRoute answers a path under the rewrite's subtree that no area
// registered: the door the SPA asked for is not one this deployment has.
func unknownAPIRoute(w http.ResponseWriter, _ *http.Request) {
	refuseAPI(w, http.StatusNotFound, "no_route", "This address is not one of this API's.")
}

// apiDispatch answers one registered path: the route the request's method names,
// or the refusal that fits — the path answers other methods, or the body is over
// what the route reads.
func (s *Service) apiDispatch(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		route, known := apiRoutes.lookup(r.Method, path)
		if !known {
			methods := apiRoutes.methods(path)
			w.Header().Set("Allow", strings.Join(methods, ", "))
			refuseAPI(w, http.StatusMethodNotAllowed, "method_not_allowed",
				"This route answers "+strings.Join(methods, " and ")+".")
			return
		}
		if err := capAPI(r, route.maxBytes); err != nil {
			if errors.Is(err, errAPIOverCap) {
				refuseAPI(w, http.StatusRequestEntityTooLarge, "body_too_large", "This request's body is larger than this route reads.")
				return
			}
			refuseAPI(w, http.StatusBadRequest, "unreadable_body", "This request's body could not be read.")
			return
		}
		route.serve(s, w, r)
	}
}

// errAPIOverCap is a body over the route's own cap.
var errAPIOverCap = errors.New("keyservice: the request's body is over the route's cap")

// capAPI reads the route's body, or refuses it. It reads one byte past the cap,
// which is what tells a body exactly at the cap from one over it, and hands the
// handler what it read as the request's body: the service then holds a body
// whose size it named rather than one that arrives in pieces (§3.4's rule, at
// the API's door).
//
// The cap is enforced here and not in each handler because a handler that
// forgets it is a handler a stranger can make the service hold a body for, and
// because a refusal the mount makes is the one shape every refusal shares.
func capAPI(r *http.Request, maxBytes int64) error {
	if r.Body == nil {
		return nil
	}
	held, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(held)) > maxBytes {
		return errAPIOverCap
	}
	r.Body = io.NopCloser(bytes.NewReader(held))
	r.ContentLength = int64(len(held))
	return nil
}

// apiError is the whole of a refusal's body: one object under `error`.
type apiError struct {
	Error apiFault `json:"error"`
}

// apiFault is the code a client branches on and the sentence a person reads.
type apiFault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// refuseAPI writes the one refusal shape: a code and a sentence about the
// request, and never anything about the service's inside (§7.2's rule, read at
// the API's door).
func refuseAPI(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", apiJSONType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: apiFault{Code: code, Message: message}})
}

// Docs is the portal's own document store (plan P1.2): named collections of
// documents, read and written by their id, listed by one field's value, and
// changed inside a transaction the store runs again when another writer got
// there first.
//
// It is a seam here and an implementation there because the API's areas ask
// about records — organisations, workspaces, entitlements, keys, sales events —
// and never about Firestore, which is the deploy's choice and P1.2's to make.
type Docs interface {
	Get(ctx context.Context, collection, id string) (Doc, error)
	Put(ctx context.Context, collection, id string, doc Doc) error
	Query(ctx context.Context, collection string, where Where) ([]Doc, error)
	Update(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Doc is one document: a JSON object, which is what the store holds and what
// the API's own wire shapes are (plan P1.1).
type Doc map[string]any

// Where selects the documents of one collection whose field holds one value. An
// empty Field selects the collection's documents, which is what a list of them
// is.
type Where struct {
	Field string
	Value any
}

// Tx is the reads and the writes one transaction makes together (plan P1.2):
// what it writes is committed with what it read, or the store runs it again.
type Tx interface {
	Get(ctx context.Context, collection, id string) (Doc, error)
	Put(ctx context.Context, collection, id string, doc Doc) error
	Delete(ctx context.Context, collection, id string) error
}

// ErrNoDocument is what a read of a document that is not there answers, so a
// caller tells an absent record from a store that failed.
var ErrNoDocument = errors.New("keyservice: the document is not there")

// Caller is who a verified request is: the account, the address its provider
// wrote, how it signed in and when, and the tenant when the provider is one that
// has them (F31, D2207).
type Caller struct {
	UID      string
	Email    string
	Provider string
	AuthTime time.Time
	TenantID string
}

// SignIn verifies one sign-in (plan P1.3): it turns the ID token the SPA sends
// into the caller that token names, having checked the token's audience, its
// issuer, its signature and that the address is verified, first. What an area
// then asks of a caller — the provider rule, a fresh sign-in, a pinned tenant —
// is P1.3's, and it is asked through this seam for the same reason the
// verification is: a rule about a sign-in belongs to whatever holds the
// sign-in.
type SignIn interface {
	Verify(ctx context.Context, idToken string) (Caller, error)
	// RequireSSO refuses a caller whose sign-in the rule does not admit (D2207,
	// F10). The rule is the caller's to build from the organisation's own record
	// (P1.3's SSORule) and it follows the membership a request USES and not the
	// organisation the request is about, so an area passes the membership's own
	// organisation.
	RequireSSO(caller Caller, rule SSORule) error
}

// Paddle is the merchant of record as the portal reaches it. Every method is one
// the plan's slices name: the checkout's transaction, created server-side so the
// browser never holds the key and never names custom_data (P4.1, F15); the
// customer portal's session the billing screens link out to, and the
// subscription's own seat count (P4.3); and the cancellation an organisation's
// deletion makes (P2.4).
type Paddle interface {
	CreateTransaction(ctx context.Context, checkout Checkout) (Transaction, error)
	PortalSession(ctx context.Context, customerID string) (string, error)
	SetSeats(ctx context.Context, subscriptionID string, seats int) error
	Cancel(ctx context.Context, subscriptionID string) error
}

// Checkout is one purchase the API starts: what the overlay is opened for and
// the custom_data its transaction carries, which is what a webhook maps back to
// the organisation and the workspace (F15).
type Checkout struct {
	PriceID      string
	Organisation string
	Workspace    string
	Seats        int
}

// Transaction is what the merchant answers a checkout with: the id the overlay
// is handed — the only thing the browser ever sees — and the customer it will
// belong to.
type Transaction struct {
	ID         string
	CustomerID string
}

// Scheduler verifies Cloud Scheduler's own sign-ins (F33): the jobs that remind
// and accrue call the API as a service account, and the token they carry has to
// name both that account and this service as its audience. It answers the
// account, which is what an audit line names.
type Scheduler interface {
	Verify(ctx context.Context, bearer string) (account string, err error)
}

// Sealer is the handover's encryption (F3): the bundle a reveal leaves for the
// installation is stored sealed, under a key only the API's account can open, so
// a leaked record is not a live key. P1.7 builds it on Cloud KMS.
type Sealer interface {
	Seal(ctx context.Context, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, sealed []byte) ([]byte, error)
}
