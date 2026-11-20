package credsvc

import (
	"context"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The stub scope (C1): a resolver that answers a scope which refuses every
// call. It exists so that a task can exercise the front door — "the scope
// refuses this route" is a fact about ServeHTTP and not about a workspace — and
// so that a route switched to the scope can be tested before either deployment
// implements it, without a second hand-written Scope in every test.
//
// It refuses in the only two ways a scope can: Serves is false for every path,
// so nothing is dispatched under it at all; and every reader answers an error,
// so a handler that reached one anyway is refused rather than served an empty
// policy, an empty table or an empty offering — the three answers that would be
// worse than a refusal, because a service that served them would serve a caller
// nobody admitted (fail closed).
type StubScopeResolver struct{}

// Resolve answers the stub scope, and it never refuses the call itself: what
// the front door does with a scope that serves nothing is the thing under test.
func (StubScopeResolver) Resolve(http.ResponseWriter, *http.Request) (Scope, bool) {
	return StubScope{}, true
}

// StubScope is a scope that serves no route and answers every reader with a
// refusal. It is a value: it holds nothing, so it can be a package-level
// default and shared between tests.
type StubScope struct{}

var errStubScope = errors.New("credsvc: the stub scope serves no route and answers no reader")

// Caller answers no caller: a scope that verified none is a scope whose
// refusals are the service's own and write no audit line.
func (StubScope) Caller() (Caller, bool) { return Caller{}, false }

// Serves answers false for every path, KeepWarmPath included: the stub serves
// nothing, which is what makes the front door's refusal the whole of what a
// call under it meets.
func (StubScope) Serves(string) bool { return false }

// AcceptsSlip answers false: the stub is a workspace-shaped refusal, and a
// workspace's callers present no slip (R18).
func (StubScope) AcceptsSlip() bool { return false }

func (StubScope) Policy() PolicyReader    { return stubPolicy{} }
func (StubScope) Kills() KillReader       { return stubKills{} }
func (StubScope) Outcomes() OutcomeReader { return stubOutcomes{} }
func (StubScope) Table() BindingTable     { return stubTable{} }
func (StubScope) Offering() Offering      { return stubOffering{} }
func (StubScope) Forge() ForgeReads       { return stubForge{} }

// State answers nil, and deliberately not a store that fails every method: a
// scope that serves nothing never reaches a selection, so there is no store to
// fail — and a nil store would panic rather than refuse if one ever did, which
// is the loud half of the same statement.
func (StubScope) State() creds.Store { return nil }

// Meter answers nil: the stub meters nothing, like an installation's scope.
func (StubScope) Meter() *WorkspaceMeter { return nil }

type stubPolicy struct{}

func (stubPolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	return nil, false, errStubScope
}

func (stubPolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, errStubScope
}

type stubKills struct{}

func (stubKills) Stop(context.Context, string) (bool, string, error) {
	return false, "", errStubScope
}

type stubOutcomes struct{}

func (stubOutcomes) OutcomeExists(context.Context, int64, string, string) (bool, error) {
	return false, errStubScope
}

// stubTable answers false for every lookup and no error at all: not knowing an
// account is an answer a deployment gives (Bound), and the stub's refusal is
// that it knows nobody.
type stubTable struct{}

func (stubTable) Job(string) (JobAccount, bool)          { return JobAccount{}, false }
func (stubTable) Review(string) (ReviewJobAccount, bool) { return ReviewJobAccount{}, false }
func (stubTable) Shared(string) (SharedJobAccount, bool) { return SharedJobAccount{}, false }
func (stubTable) Repo(string) (string, bool)             { return "", false }

type stubOffering struct{}

func (stubOffering) Offer(context.Context, Caller, *projectpolicy.Policy, *Bundle) (Offered, error) {
	return Offered{}, errStubScope
}

type stubForge struct{}

func (stubForge) BitbucketToken(context.Context, string) (Entry, bool, error) {
	return Entry{}, false, errStubScope
}

func (stubForge) AppKey(context.Context, string) (Entry, bool, error) {
	return Entry{}, false, errStubScope
}
