package credsvc

import (
	"context"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The per-call scope (C1, R1, R14, R18).
//
// A route of this service does not read the deployment's configuration to learn
// who is calling it and what that caller may be served: ServeHTTP resolves ONE
// scope for the request (ScopeResolver), refuses the call where the scope does
// not serve the route, and hands the scope to every handler it dispatches
// through the request's context (withScope). Every reader the model path needs
// — the verified caller, the published policy, the operator's stop switches,
// the cascade's state, the outcomes node, the deployment's binding table, the
// offering, the forge credentials and the workspace's cap meter — is asked of
// that scope.
//
// The seam exists because this service has two deployments and one code path
// (spec.md §1). An installation's scope is the deployment itself: one database,
// one bundle, one table, every route (scope_installation.go). A workspace's
// scope is a call by one of its members: that member's database, the
// intersection of the workspace's shared node with the member's own policy, and
// the eight routes R14 names and no others (WS2, scope_workspace.go). What the
// two share is this interface and nothing else, so a route written against the
// scope serves both without knowing which one it is in — and a handler that
// read Config directly would be a handler that serves an installation's own
// database to a workspace's caller.
//
// Three things about the shape are the design and not an accident:
//
//   - the scope is per CALL and not per deployment. The caller a scope answers
//     is the identity this request was verified as, so no scope can be cached
//     across requests and no reader can be swapped under a call in flight.
//   - a scope is not a credential. Every reader here answers names, records and
//     credentials-as-entries; a bundle's VALUES are reached only by the code
//     that attaches one to a call (Bundle.Entry.Value), which is what keeps a
//     scope safe to hand to a handler and to name in an audit line.
//   - the readers fail closed. A reader that cannot answer returns an error and
//     never an empty answer, because an empty policy, an empty table and an
//     unreadable one are three different facts and a service that served the
//     first two for the third would serve a caller nobody admitted.
type Scope interface {
	// Caller is the caller this scope resolved for, and ok is false for a
	// scope resolved for a route answered BEFORE the shared parse (ServeHTTP):
	// the routes whose callers hold no run identity — the verifier, the two
	// read mints, the build routes, the launcher's grant — are resolved with no
	// identity read at all, and a refusal there is the service's own and writes
	// no audit line (audit.go's rule is about a verified caller).
	Caller() (Caller, bool)
	// Serves is whether this scope serves a route (R14). An installation serves
	// every route of the service; a workspace serves the model routes, a run's
	// forge routes and its keep-warm route, and refuses every other route with
	// a typed refusal. ServeHTTP asks it once per request, before any handler
	// is dispatched, so a route a scope does not serve reaches no handler at
	// all — which is what keeps R14 a property of the front door rather than
	// eight checks inside eight handlers.
	Serves(path string) bool
	// AcceptsSlip is whether a call to this scope may present a slip (R18). An
	// installation's callers do (the slips are its own, DS1); a workspace's
	// never do — a member's run presents the job's identity and the run's token
	// and nothing else of the member's — so a call that presents one to a
	// workspace's service is refused rather than served slip-less.
	AcceptsSlip() bool
	// Policy is the published policy this scope's callers are held to.
	Policy() PolicyReader
	// Kills is the operator's stop switches this scope's callers are held to:
	// the installation's own for an installation, the workspace's for a
	// workspace (R8's neighbour, and D1299's rule that a member's own stop
	// switches are the member's own service's business).
	Kills() KillReader
	// State is the cascade's shared state, read and written: cooldowns, the
	// round-robin position, the run's placement and the repository's recorded
	// auth. A workspace's state is the workspace's own database and never a
	// member's (R8).
	State() creds.Store
	// Outcomes is the write-once outcomes node the verifier reads (auth.go):
	// the same reader Config.Auth is built with, answered here for the scope
	// that verified the caller. nil is a deployment whose database this process
	// cannot read as a Getter, which is a test's own configuration.
	Outcomes() OutcomeReader
	// Table is the binding table the pairing is made against: the job accounts,
	// the review and shared tables, and the slug-to-repository table (R1's
	// "verified caller plus table").
	Table() BindingTable
	// Offering is what this scope offers the caller out of the bundle, before
	// the admission and the repository's recorded auth narrow it.
	Offering() Offering
	// Forge is the git credentials this scope serves a run's forge calls with
	// (R14): the token a proxied Bitbucket request is served by and the App key
	// a run's mint issues from. It is read by the forge routes alone (WS16,
	// WS17), and it is a reader of its own rather than a field of Offering
	// because a forge credential is not offered at a tier and never reaches the
	// cascade.
	Forge() ForgeReads
	// Meter is the workspace's cap meter, or nil for a scope whose calls are
	// not metered by a workspace (R15, workspace_cap.go). It is the one reader
	// here that takes room rather than answers a question, and it is the
	// workspace's own: an installation's cap is the installation's budget,
	// taken where it always was.
	Meter() *WorkspaceMeter
}

// ScopeResolver is the front door's one question: the scope this request is
// served under (R1). It is the seam between the two deployments — an
// installation's resolver is built from today's Config by NewServer
// (scope_installation.go), a workspace's from its own table (WS2) — and the
// server asks it once per request and never asks Config for a caller again.
//
// Resolve answers ok=false when it refused the call itself: it owns the reads
// that come before any route is dispatched — RequestFromHTTP, the two
// identities' verification, and the pairing — and a failure of any of them is
// the refusal the route would have written, written here because the resolver
// is what made the read. A resolver that refused has already written its
// answer (and, where an identity was verified, its one audit line).
//
// It must answer a scope for a route answered before the shared parse
// (aheadOfTheSharedParse) without reading an identity: those routes' callers
// hold none, and what tells them apart is that their scope's Serves refuses
// them where the deployment does not serve them (a workspace refuses eight of
// them, R14).
type ScopeResolver interface {
	Resolve(w http.ResponseWriter, r *http.Request) (Scope, bool)
}

// KeepWarmPath is the keep-warm route's path (R14): the one route of workspace
// mode whose call carries no secret, so that a member's run has something to
// ask while its model calls are refused (reconciliation F33, D2985's beat).
// The path is spelled once, here, because three things have to agree on it: the
// workspace's scope, which serves it; the run's beat, which calls it
// (WS13); and the route record, which does not name it (C2 — the beat's
// address is the workspace's own).
const KeepWarmPath = "/v1/keep_warm"

// Caller is the verified caller of one call: the run identity the service
// verified, the canonical repository the pairing resolved it to, the job
// account it paired with, and the parsed request those came from.
//
// It carries names and no credential value: no field of it is a secret, and the
// tokens of Request are the caller's own — they are named here because the
// routes that scrub an answer, mint with the job's token or read the run's
// claims need them, never because they are served anywhere.
type Caller struct {
	// Identity is the verified run: the slug, the run id and the job account
	// the verifier answered (auth.go).
	Identity Identity
	// Canonical is the canonical repository the pairing resolved (review.go's
	// pairReview and pairShared): the run's own slug resolved through the
	// scope's table and admitted by the calling account. Every check after the
	// pairing — the admission, the policy's allow-lists, the proxy's rules — is
	// made against this name and never against the caller's claim.
	Canonical string
	// Job is the job account row the call paired with. For a review caller and
	// for a shared project job it is the identity's own account with the
	// canonical repository filled in, which is what the mint and the
	// workflow-secrets route are scoped by.
	Job JobAccount
	// Request is the parsed call (RequestFromHTTP): the two identity tokens and
	// the claims beside them, which the claim route and the sign route read.
	Request Request
}

// BindingTable is the scope's binding table as the front door, the pairing and
// the cascade read it: one lookup per question, each answering whether the
// table knows the value at all.
//
// A lookup that does not know a value answers false and never an error: an
// account this deployment does not bind is a caller it does not admit, which is
// an answer and not a failed read (the refusals of an unreadable node are the
// readers' own, Policy and Kills above).
type BindingTable interface {
	// Job is the per-repository job account of one service account, by the
	// spelling the verifier answered (the configured one).
	Job(email string) (JobAccount, bool)
	// Review is a review job's row (review.go's ReviewJobAccount).
	Review(email string) (ReviewJobAccount, bool)
	// Shared is a shared project job's row (server.go's SharedJobAccount),
	// looked up by the folded account like the deployment's own map (server.go
	// NewServer).
	Shared(email string) (SharedJobAccount, bool)
	// Repo is the canonical repository of one run-identity slug (R1's table):
	// the row a review or shared caller's pairing resolves its repository
	// through, because neither class's account names one.
	Repo(slug string) (string, bool)
}

// Offered is one call's offering: what the scope offers this caller out of the
// bundle, before the tier-A admission, the repository's recorded auth and the
// slip's fence narrow it (candidates).
type Offered struct {
	// Credentials are the credentials offered, in the order they are to be
	// tried, and Strategy is the rule the choice among them is made by
	// (creds.Strategy, roundrobin.go).
	Credentials []creds.Credential
	Strategy    creds.Strategy
	// NotAdmitted are the credentials left out of the list with the reason each
	// was left out, which the wire reports to the run so that an operator can
	// read why a call was served what it was (Tried).
	NotAdmitted []Tried
	// Final says the list is the caller's whole admission, so nothing narrows
	// it further. It is true for a review caller alone: that class's offering
	// is decided before the cascade's own narrowing (reviewOfferings), and a
	// scope that answered a repository caller Final would be a scope serving
	// the whole shared pool to a repository the member's own node never
	// admitted.
	Final bool
}

// Offering answers what one caller is offered out of the bundle. It is the
// scope's reader and not the cascade's own because the pool is the scope's: an
// installation offers its bundle, and a workspace offers the intersection of
// its shared node with the member's own policy over the same bundle
// (workspace.go's Shared, D1298, D2980).
type Offering interface {
	// Offer answers the credentials the caller may be offered, the strategy
	// they are served under, the ones left out with why, and whether the list
	// is final (Offered). shared is the published policy the offering is
	// decided against — read once by the caller, because the cascade's own
	// narrowing below it is made against the same snapshot.
	Offer(ctx context.Context, c Caller, shared *projectpolicy.Policy, b *Bundle) (Offered, error)
}

// ForgeReads is the forge half of the model path's readers (R14): the git
// credentials a run's forge calls are served with. Both answer an Entry — the
// bundle's own value holder, whose Value() is the only way to the secret — and
// both answer ok=false when this scope serves that repository no such
// credential, which is a refusal and not an empty value.
//
// A workspace's forge reads come from the workspace owner's node over the
// member's bundle (WS15, WS16, WS17), which is why they are the scope's: which
// repository a call is served for is the scope's answer (Caller.Canonical) and
// the credential it is served with has to be the one that scope's owner
// declared.
type ForgeReads interface {
	// BitbucketToken is the token one proxied call for canonical is served
	// with.
	BitbucketToken(ctx context.Context, canonical string) (Entry, bool, error)
	// AppKey is the GitHub App key a run's mint for canonical issues from.
	AppKey(ctx context.Context, canonical string) (Entry, bool, error)
}

// scopeCtxKey is the context key the scope travels under. It is unexported and
// of its own type so that no other package can put a value under it and no
// handler can be handed another package's scope.
type scopeCtxKey struct{}

// withScope hands the scope to every handler dispatched under it, which is what
// lets a handler read its caller and its readers without a signature change:
// the routes of this service are dispatched by path, and a handler that took a
// scope argument would be a handler whose caller had to know which deployment
// it was serving.
func withScope(ctx context.Context, sc Scope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, sc)
}

// scopeOf is the scope ServeHTTP resolved for this call. ok is false for a call
// that reached a handler without one — a test calling a handler directly, which
// is why it is an answer rather than a panic.
func scopeOf(ctx context.Context) (Scope, bool) {
	sc, ok := ctx.Value(scopeCtxKey{}).(Scope)
	return sc, ok
}

// WorkspaceScopeConfig is what the workspace's resolver is built from (C1;
// WS2 implements the scope behind it, WS5 is its caller).
//
// The three fields are the three things a workspace's scope reads and an
// installation's does not: the member table the caller resolves to, the one
// bundle whose values the shared pool holds, and the meter that takes the
// workspace's room. Everything else a scope answers — the member's policy, its
// stop switches, its state, its outcomes and the node that names what is shared
// — is read through the workspace's own databases, which Workspace holds: a
// member's scope reads the member's database and never another member's, and no
// field of this configuration can point one member's call at another member's
// state (finding 6).
type WorkspaceScopeConfig struct {
	// Workspace is the deployment the resolver serves: its member table, the
	// per-member verifiers, and the database each member's state is read from
	// (workspace.go's NewWorkspace).
	Workspace *Workspace
	// Bundles loads the workspace's own bundle, which is where the values of
	// the shared pool are (bundle_workspace.go).
	Bundles *BundleLoader
	// Meter is the workspace's cap meter (workspace_cap.go): the scope answers
	// it, and WS8 is the task that puts it on the paid-call path. nil is a
	// workspace whose calls are metered by nothing yet, which is the state of
	// every deployment before that wiring lands.
	Meter *WorkspaceMeter
}

// NewWorkspaceResolver is the workspace resolver's constructor: the signature
// C1 fixes, because two tasks are written against it at once — WS5 builds the
// workspace's service from it (internal/cli/credentials_serve.go) and WS2 lands
// the scope behind it — and a signature fixed in the contract is what keeps the
// two from drifting.
//
// WS2 REPLACES THIS BODY. Until it lands, the constructor answers the refusal
// that says so rather than a scope: a workspace's deployment that could not
// build its resolver must not start, because the scope it would otherwise be
// given is an installation's — one database, one table, every route — and that
// is precisely the service R1 exists to make impossible. A refusal at start is
// read by the operator who deployed it; a wrong scope would be read by nobody.
func NewWorkspaceResolver(cfg WorkspaceScopeConfig) (ScopeResolver, error) {
	if cfg.Workspace == nil {
		return nil, errors.New("credsvc: a workspace resolver needs the workspace whose members it serves")
	}
	return nil, errors.New("credsvc: the workspace's scope is not implemented in this build (WS2 of docs/workspace-service/implementation-plan.md)")
}
