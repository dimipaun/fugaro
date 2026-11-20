package credsvc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is the scaffolding of C1, and it is the one part of this package
// that goes away: I1 wires the sides and deletes this file and its test
// (scope_stub_test.go), which is why every stub lives here and nothing the
// sides keep does.
//
// What it is for is the two sides of the contract being written at once. WS5
// builds a workspace-mode process through NewWorkspaceScopeResolver while WS2 —
// which owns that constructor's body, in scope_workspace.go — is still being
// written, so the constructor has to answer before WS2 exists; and the answer
// it must give in the meantime is the one that can never serve a call by
// accident. So the stub resolver answers a scope whose every reader refuses and
// whose every question about a route is answered "no", and it verifies no
// caller at all.
//
// **A stub returns the shape it is named for and decides nothing**, which is
// the same rule the other scaffolding in this tree follows (internal/cli's
// after_write_stub.go, internal/mobilesession's stub.go): it reads no policy,
// no kill switch, no state, no repository's auth record, no outcome and no
// node, holds no repository table, offers no credential, selects no token,
// takes no room and keeps no figure.
// A side written against C1 compiles and runs its own tests against a signature
// that exists, and no side can pass on a decision nothing made — because
// nothing here makes one.
//
// The one thing the stub does decide is the fail-closed direction of every
// answer, and TestStubScopeRefusesEveryCall holds it to that: the caller is the
// zero Identity, the repository table is empty (a slug it does not hold is
// refused at the pairing, D1066), Serves and AcceptsSlip are false, the meter
// is nil, the Bitbucket selector is nil, and every reader answers an error. An
// empty table and a nil selector are refusals here and not "nothing to do":
// both are the state a deployment that holds neither is in, and both leave the
// call refused rather than served by default.

// errStubScope is what every reader of the stub scope refuses with. It is one
// value rather than a sentence per method because the stub never runs in
// production — I1 deletes it — and a reader of this error is a developer
// looking at a test, not an operator looking at a refusal: what the sentence
// has to say is which of the two it is, and nothing about the call.
var errStubScope = errors.New("credsvc: this is the stub scope C1 ships until WS2 lands, and it decides nothing: it verified no caller and reads no policy, kill switch, state, outcome, node, table or pool")

// StubScopeResolver is the shape of a resolver and nothing else: it answers a
// scope that refuses every call, for every request, whatever the request is.
// Nothing about the request reaches the answer — no header is read, no token is
// verified, no route is looked at — so two calls with two different identities
// get the same refusing scope, which is what makes it a stub and not a policy.
func StubScopeResolver() ScopeResolver { return stubScopeResolver{} }

type stubScopeResolver struct{}

func (stubScopeResolver) Resolve(context.Context, *http.Request) (Scope, error) {
	return stubScope{}, nil
}

// stubScope is the scope that refuses every call: it names no caller, serves no
// route, accepts no slip and answers a refusing reader for every reader the
// contract declares.
type stubScope struct{}

// Caller answers the zero Identity: no token was verified, so no run identity
// stands behind this scope and every route that acts for a run is refused by
// the pairing rather than served.
func (stubScope) Caller() Identity { return Identity{} }

func (stubScope) Policy() PolicyReader     { return stubPolicy{} }
func (stubScope) Kills() KillReader        { return stubKills{} }
func (stubScope) State() StateStore        { return stubState{} }
func (stubScope) RepoAuth() RepoAuthReader { return stubRepoAuth{} }
func (stubScope) Outcomes() OutcomeReader  { return stubOutcomes{} }
func (stubScope) Offering() OfferingReader { return stubOffering{} }
func (stubScope) Forge() ForgeReader       { return stubForge{} }

// Repos answers no table at all: the pairing refuses a slug no table holds
// rather than resolving it (D1066), so an empty one is the answer that serves
// nothing — and it is not the same answer as a table that happens to be empty,
// which is a deployment that published no repository.
func (stubScope) Repos() map[string]string { return nil }

// Meter answers nil, which is a deployment with no workspace cap — the state
// every installation is in and the state a workspace that declared none is in
// (D1301 as amended). It is the ONE accessor of this scope whose zero answer is
// not a refusal (Scope's own rule): a route serves unreserved where the meter
// is nil, so what keeps the stub from paying for anything is that Serves is
// false for every route and the offering holds nothing — never the nil itself.
func (stubScope) Meter() *WorkspaceMeter { return nil }

// Serves answers false for every path, including the paths this service really
// serves and the keep-warm route C1 declares: a scope that served a route would
// be a route dispatched to readers that refuse, and the refusal belongs where
// the route is (R14).
func (stubScope) Serves(string) bool { return false }

// AcceptsSlip answers false: the stub holds no slip store, and a call admitted
// by a slip is a call admitted by a grant nothing verified (R18).
func (stubScope) AcceptsSlip() bool { return false }

// stubPolicy reads no policy: neither the project's shared node nor a
// repository's own admission node is read, and both refuse. found=false with no
// error would be the answer "this node does not exist", which is a decision
// about the published policy and not a stub's to make.
type stubPolicy struct{}

func (stubPolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	return nil, false, errStubScope
}

func (stubPolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, errStubScope
}

// stubKills reads no stop switch. It refuses rather than answering "off",
// which is this service's own rule read the other way round: an unreadable
// switch is ON, and a stub that answered a readable off switch would serve
// through a stop nobody read.
type stubKills struct{}

func (stubKills) Stop(context.Context, string) (bool, string, error) {
	return false, "", errStubScope
}

// stubState reads and writes nothing: no mark, no lease, no placement and no
// tie-breaker. Every method refuses, including the writes — a mark a stub wrote
// would outlive the stub, and a placement it wrote would be stickiness nothing
// selected. The repository's auth record is not here at all: it is the member's
// and not the state's (Scope.State), and stubRepoAuth below is its reader.
type stubState struct{}

func (stubState) Marks(context.Context) (map[string]creds.Mark, error) {
	return nil, errStubScope
}

func (stubState) UpdateMark(context.Context, string, func(creds.Mark, bool) (creds.Mark, error)) (creds.Mark, error) {
	return creds.Mark{}, errStubScope
}

func (stubState) Leases(context.Context, time.Time) ([]creds.Lease, error) {
	return nil, errStubScope
}

func (stubState) SetPlacement(context.Context, creds.Run, string, creds.Lease) error {
	return errStubScope
}

func (stubState) Current(context.Context, creds.Run) (string, bool, error) {
	return "", false, errStubScope
}

func (stubState) RoundRobin(context.Context) (int64, error) { return 0, errStubScope }

// stubRepoAuth answers no repository's auth record: the record is what narrows
// a repository's reach to the auth its own configuration names, and found=false
// with no error would be the answer "this repository has no record", which
// narrowToRepoAuth reads as the cascade unchanged (D2070 (a)) — a decision
// about a member's configuration and not a stub's to make.
type stubRepoAuth struct{}

func (stubRepoAuth) RepoAuth(context.Context, string) (creds.RepoAuth, bool, error) {
	return creds.RepoAuth{}, false, errStubScope
}

// stubOutcomes answers no outcome: whether a run has ended is evidence the stub
// cannot have, and the false it would otherwise answer is the answer that
// honours a replayed token (W2).
type stubOutcomes struct{}

func (stubOutcomes) OutcomeExists(context.Context, int64, string, string) (bool, error) {
	return false, errStubScope
}

// stubOffering offers no credential: an empty list with no error would be the
// answer "this deployment has nothing to offer", which a route reads as a
// bundle that holds no candidate and not as a stub that read nothing.
type stubOffering struct{}

func (stubOffering) Offering(context.Context, *Bundle) ([]creds.Credential, error) {
	return nil, errStubScope
}

// stubForge holds no Bitbucket token and covers no repository: a nil selector
// is what makes the proxy refuse the call rather than serve it with a token
// nothing chose (T8, and Scope.Forge's own rule that nil refuses in both modes
// — the route's legacy fallback is WS1's to replace with this answer), and
// coverage refuses rather than answering true, which would serve a repository
// no workspace owner declared.
type stubForge struct{}

func (stubForge) Bitbucket() bbtokens.Selector { return nil }

func (stubForge) Covers(context.Context, string) (bool, error) { return false, errStubScope }

// stubWorkspaceScopeResolver is the constructor's body until WS2 supplies the
// real one. It refuses no configuration, and that is deliberate: a
// configuration error is the real constructor's to name, and this function
// exists so that a process built through the constructor refuses its CALLS
// rather than failing to start — the failure to start would be a worse answer
// to a deployment whose workspace half is not written yet.
func stubWorkspaceScopeResolver(WorkspaceScopeConfig) (ScopeResolver, error) {
	return StubScopeResolver(), nil
}

// The assignment is the whole of how the constructor answers before WS2 exists,
// and WS2's own file replaces it: the toolchain presents a package's files in
// name order, so scope_stub.go is initialized before scope_workspace.go and the
// later assignment is the one that stands. Deleting this file (I1) is therefore
// a deletion and not an edit.
func init() {
	NewWorkspaceScopeResolver = stubWorkspaceScopeResolver
}
