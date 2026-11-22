package credsvc

import (
	"context"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is C1: the per-call scope, which is the seam that lets this one
// service be deployed twice — as an installation's credentials service, over
// its one database and its one repository table, and as a workspace's, over
// the database of the member the caller turned out to be (R1). The prose half
// is docs/workspace-service/contract-scope.md, which is the document a side
// building the other half of this contract reads.
//
// The seam is a value and not a branch. Every reader a route needs is reached
// through the Scope a call resolved to, and the scope is what says WHICH
// database, WHICH repository table and WHICH pool the call is served out of —
// so the routes are written once and the two deployments differ in the resolver
// they are built with, not in the code that serves them. That is R1's whole
// requirement, and the reason the two halves must not be two spellings of one
// route: a second implementation of a route is a rule that can drift from the
// rule beside it, and the rules here are the ones that decide whether a
// credential is issued at all.
//
// The scope is per CALL and not per process. A workspace's service answers
// calls from the jobs of every member it was declared for, and the member a
// call belongs to is a fact only the call carries: it is what its run identity
// verified (Workspace.Verify), and it decides which database the policy, the
// state and the outcomes are read from, which repository table the pairing
// resolves through and which pool the call may be served from. A process-wide
// reader would have to re-derive the member at every route, which is the second
// derivation this shape exists to remove.
//
// Nothing here carries a credential value. Every reader answers names, marks,
// tiers and node bytes: the values live in the bundle, which the route loads
// itself and which is never a field of a scope.

// Scope is everything one call needs, resolved for the caller that made it.
//
// It is an interface with two implementations and no third: the installation's
// (scope_installation.go, WS1) answers every accessor out of today's Config —
// the installation's one database, its one repository table and its one
// verifier — and the workspace's (scope_workspace.go, WS2) answers them out of
// the member the call's run identity resolved to. A deployment is built with
// one resolver or the other and the routes below it are the same code.
//
// **The zero answer of every accessor is the fail-closed one**, and that is a
// rule of the shape rather than a property of the two implementations: a scope
// that names no caller, serves no route, accepts no slip, holds no repository
// table and answers refusing readers is a scope a route can be handed without
// anything being served by accident. WS1's
// TestARouteTheScopeDoesNotServeIsRefused is the case that holds the gate to it:
// a scope that serves nothing reaches no handler and no reader.
//
// **Meter is the one exception, and it is named here rather than left to be
// inferred.** Every other accessor's zero answer refuses a call; Meter's nil
// means "this deployment has no workspace cap", and a route that pays for a
// call serves it UNRESERVED where the meter is nil (D1301 as amended) — so a
// resolver that holds a cap and answers nil has not refused the call, it has
// removed the cap. Nothing in the shape can tell the two apart at the point the
// route asks, so the requirement sits on the resolver that has the cap: WS2
// holds, with a test of its own, that a workspace with a cap configured never
// answers a nil meter (and WS8, which wires the reservation, holds the other
// side of it).
type Scope interface {
	// Caller is the run identity this call's token proved, and the zero
	// Identity on a call that carries none — every route this service answers
	// ahead of RequestFromHTTP (VerifyPath, the read mints, the merge mint, the
	// label and issue routes, the slip grant, the build routes and a build's
	// proxy traffic), whose caller is a person, a daemon, a launcher or a build
	// and holds no run identity at all.
	//
	// It is the value the ROUTE acts on and not a header: it is what the
	// resolver's verification returned, so a route that reads the repository,
	// the job account or the claims of the caller reads them here rather than
	// from the request (C1.10).
	Caller() Identity
	// Policy is the published policy this caller's repository is held to: the
	// project's shared node and a repository's own admission node. The
	// installation's is its own one node; a workspace's is the member's own
	// published policy, read from the member's own database, because that is
	// the policy its administrator wrote (R1, R5).
	Policy() PolicyReader
	// Kills is the operator's stop switches the call is held to: the reader is
	// read before the bundle on every route, and a call whose switch is on is
	// refused before any credential is touched. The installation's is its own
	// database's (RTDBKills).
	//
	// **Whose switches a workspace-mode call honours is not settled by this
	// contract**, and the workspace's resolver is where the answer goes: the
	// caller's own operator's, read from the member's database, is what makes
	// "stop my runs" mean what it means today even for a call the member's own
	// service no longer serves — and the WORKSPACE's own switch, which
	// workspace_cap.go's comment already names as one of the three refusals the
	// workspace's service answers on its own account, is the one that stops
	// everything the workspace's pool pays for. Both readings are defensible and
	// nothing in R1 to R19 picks one; a resolver that wants both reads both,
	// because the interface answers one reader and a reader may hold two
	// switches. What the shape fixes is only that the answer is chosen for the
	// caller and asked before the bundle.
	Kills() KillReader
	// State is the cascade's own state — the marks, the leases, a run's
	// placement and the tie-breaker — as THIS caller's deployment keeps it. The
	// installation's is its own database's; a workspace's is the workspace's OWN
	// database and never a member's, because a cooldown, a rotation position and
	// a spend figure for a workspace credential are facts about the workspace's
	// pool and the service holds no write on any member (R8, D1299 as amended).
	//
	// **The per-repository auth record is NOT here.** It is written by the
	// MEMBER's own `fugaro update` into the member's database, so it is read
	// through RepoAuth below and never through the state store a workspace owns:
	// a workspace that read it here would find nothing, and narrowToRepoAuth
	// reads an absent record as the cascade unchanged (cascade.go, D2070 (a)),
	// which would serve a member's run the whole of the workspace's pool. The
	// type carries that and not only the comment — StateStore is
	// creds.CascadeState, creds.Store without the method and the very interface
	// creds.Selector.Store is typed as, so a route hands this straight to a
	// selection and cannot reach the wrong database by habit.
	State() StateStore
	// RepoAuth is the record of what ONE repository's runs go out on
	// (creds/repoauth/<slug>, D2070 (a), R7), read from the CALLER's database:
	// the installation's own, and for a workspace the MEMBER's, because the
	// member's own `fugaro update` is what wrote it
	// (internal/creds/repoauth.go). It is the reader the admission is narrowed
	// by last (narrowToRepoAuth) and it is deliberately not part of State: the
	// marks, the leases and the counters are the workspace's, and this record is
	// the member's.
	//
	// found=false is a repository no update has recorded — an installation this
	// release has not written, or a member whose own update predates the record
	// — and it is not an error: the caller decides what an absent record means,
	// which for the cascade is "unchanged" (D2070 (a)).
	RepoAuth() RepoAuthReader
	// Outcomes is the reader that answers whether a run of this caller's
	// repository has already ended. It is the one the resolver's verification
	// asked (an ended run's token is not honoured again, W2) and it is carried
	// here because the database that holds the outcomes is the caller's: a
	// run's outcome node is written in its own project's database, so an
	// installation reads its own and a workspace reads the member's the call
	// resolved to — the same database that member's policy is read from, and
	// not the workspace's own.
	//
	// No route reads it today; it is declared because R1 names it and because
	// the scope is the only place that can name the right database.
	Outcomes() OutcomeReader
	// Repos is the repository table this call's pairing resolves through: the
	// slug a run identity carries (task.Slug) to the canonical repository name
	// the published policy speaks (projectpolicy.Canonical). A slug the table
	// does not hold is refused at the pairing and never guessed from the slug's
	// own text (D1066).
	//
	// **The table is the CALLER's, and that is a narrowing a workspace needs.**
	// An installation's table is every repository it serves (RepositoryTableFor
	// over its job accounts); a workspace's holds the repositories of the
	// CALLING job account and no other, which is how a member's run is held to
	// the repositories the workspace owner declared for that account — one for
	// a repository's own job, several for a shared project job (R6, D2992) —
	// and how a repository outside the list is refused with the typed reason
	// that names the workspace owner's `fugaro update` (R19, F28).
	Repos() map[string]string
	// Offering is the pool this call may be served from. See OfferingReader.
	Offering() OfferingReader
	// Forge is the forge credentials and the coverage this call's git traffic
	// and its own token mints are served under. See ForgeReader.
	Forge() ForgeReader
	// Meter is the workspace's own cap, taken where a call is paid for, and nil
	// on a deployment that has none — which is every installation, whose spend
	// is the project's own budget rather than a workspace's (D1301 as amended).
	// A route that pays for a call reserves against a non-nil meter and serves
	// unreserved where it is nil, which is today's behaviour unchanged; the
	// wiring itself is WS8's.
	//
	// **It is the one accessor whose zero answer does not refuse** (see the
	// Scope comment above), so the answer is the resolver's to get right: a
	// workspace whose pool has a cap must answer that cap here and never nil,
	// and WS2 holds that with a test of its own rather than leaving the shape to
	// be trusted. A resolver that answered nil for a capped workspace would not
	// refuse a call — it would pay for every one of them.
	Meter() *WorkspaceMeter
	// Serves reports whether this deployment serves the route at path at all.
	//
	// It is the ROUTE half of the scope and not a reader: the two deployments
	// do not serve the same routes. An installation serves every route this
	// service has, and a workspace serves the model routes, the forge routes a
	// run calls (the git proxy, a run's own token mint and its context tokens)
	// and the keep-warm route, and refuses every other one with a typed refusal
	// — review signing and verification, slips, workflow secrets, build tokens,
	// the read mints, the merge, label and issue routes — because a member's
	// run keeps calling its OWN project's service for them (R14, D2988, A2).
	//
	// The refusal is written where the gate is — in ServeHTTP, which asks this
	// question before it dispatches anything (WS1) — and this method answers the
	// question only: a scope that answers false for a route it has no reader
	// for is what keeps a route from being served with nothing behind it.
	Serves(path string) bool
	// AcceptsSlip reports whether a call that presents a slip is served under
	// it. An installation's is true — a slip is the grant its launcher wrote
	// and its run claims (DS1) — and a workspace's is false, because a call to
	// the workspace's service carries the job's identity and the run's token
	// and NOTHING else of the member's: no slip id, no claim secret, no
	// auth-mode or account header, and the workspace's scope refuses a call
	// that presents one (R18, F40).
	//
	// A call that presents no slip is unaffected by the answer, in both
	// deployments: the slip-less path is what this service served before the
	// slips existed and what it goes on serving (the slips plan's R9, which is
	// server.go's own rule and not a resolution of this one).
	AcceptsSlip() bool
}

// StateStore is the cascade state a Scope answers: creds.CascadeState, which is
// creds.Store without the per-repository auth record, because that record is a
// fact about a MEMBER's repository and not about the workspace's pool, so it is
// read from the caller's database through RepoAuthReader instead
// (Scope.RepoAuth).
//
// **It is creds.CascadeState by alias, and that is the whole of why it works.**
// A route does not only read the state — it hands it to the cascade's one
// consumer, `creds.Selector.Store`, which is typed as CascadeState for the same
// reason: the six methods a selection uses are the six a selection may reach.
// Because the two are the same type, `&creds.Selector{Store: scope.State()}`
// compiles as it stands. A split that needed an adapter — or a cast back to
// creds.Store — would be undone by the first route that wrote one, and a
// workspace adapter answering RepoAuth out of the workspace's own database would
// read as "no record, the cascade unchanged" (D2070 (a)) and serve a member's
// run the whole of the workspace's pool. There is nothing to adapt: the store a
// side already holds satisfies this and RepoAuthReader both.
type StateStore = creds.CascadeState

// RepoAuthReader reads one repository's auth record — what its own fugaro.yaml
// says its runs go out on (creds/repoauth/<slug>, D2070 (a), R7) — out of the
// database the repository's own `fugaro update` wrote it in: the installation's
// for an installation, and the MEMBER's for a workspace.
//
// creds.Store satisfies it, so an installation answers it with the store it
// already holds; what the interface fixes is that the answer comes from the
// CALLER's database and never from the workspace's own state store.
type RepoAuthReader interface {
	RepoAuth(ctx context.Context, slug string) (creds.RepoAuth, bool, error)
}

// The split held by the compiler rather than by the two comments above: a side
// answers both with the one store it already holds, and a creds.Store that
// stopped satisfying either would fail to build here rather than at WS1.
var (
	_ StateStore     = (creds.Store)(nil)
	_ RepoAuthReader = (creds.Store)(nil)
)

// ScopeResolver answers the scope of one request: which caller made it, and
// therefore which readers serve it.
//
// It is the whole of what a deployment is parameterised by. The installation's
// resolver is built from today's Config and answers every route (WS1); the
// workspace's is built from a credsvc.Workspace, the published node and the
// meter (WS2), through the constructor below.
type ScopeResolver interface {
	// Resolve verifies the caller the request carries and answers the scope it
	// is served under. It is handed the *http.Request and not a parsed
	// credsvc.Request because the routes answered ahead of RequestFromHTTP
	// carry no run identity to parse (VerifyPath, the read mints, the merge
	// mint, the label and issue routes, the slip grant, the build routes) and
	// because WHICH verification a route needs is a property of the route: the
	// resolver decides it, so no route can reach a reader it was not admitted
	// to.
	//
	// It answers one of three things, and the three are not interchangeable:
	//
	//   - the scope the call is served under. A route the deployment does not
	//     serve is NOT an error: the scope's Serves is false, so that a route
	//     can be told apart from a caller, and so that the one gate that
	//     refuses an unserved route stays in one place;
	//   - a REFUSAL OF THE CALLER — the verifier's own, which names the field
	//     that failed and never its value — which ServeHTTP answers exactly as
	//     it answers the verification it makes itself today;
	//   - a *ScopeFault, where the SCOPE could not be built for a reason that
	//     is not the caller's at all. It is a channel of its own because the
	//     two answers are read differently by the run that gets them and by the
	//     operator who reads the line (the type's own comment says how), and
	//     because a fault answered as a refusal of the caller is a verdict
	//     about the caller that was never made.
	Resolve(ctx context.Context, r *http.Request) (Scope, error)
}

// ScopeFault is a resolver's failure to build a scope for a reason that is NOT
// the caller's: the workspace's published node could not be re-read (R17), a
// member's database could not be reached, a table could not be decoded.
//
// **It is a channel of its own, and a caller refusal is not it.** The two
// answers differ for the run that reads them and for the operator who reads the
// line. A refusal of the caller is StatusUnverified — a 403 — and a run's
// gateway reads a platform 403 as a missing invoke grant and halts the run on
// it (R16): the halt would name a grant when what happened is a node that could
// not be read. A fault is answered as the service's OWN failure instead:
// StatusUnavailable with ErrorStateUnavailable, the refusal refuseIdentity's
// default arm already gives a state that could not be read and refuseUnavailable
// writes for the proxy — a refusal the run halts on rather than a fallback
// (D143). It says the service could not answer, which is what happened.
//
// **The audit rule is not suspended for it, and Caller is what keeps it.** Once
// the caller's identity is verified every refusal writes exactly one line, and
// what carries none is the refusals answered before there is a verified
// identity to record (audit.go, SECURITY.md). A resolver that had verified a
// caller when its fault happened puts that caller here, and WS1 writes that one
// line — the run, the repository, the job and ErrorStateUnavailable — while a
// resolver that had verified none leaves this nil and the call is answered with
// no line, exactly as today's pre-identity refusals are. The third reading is
// the one this type exists to prevent: a fault written as a caller refusal
// whose line says the run identity failed to verify — a verification that did
// not happen.
type ScopeFault struct {
	// What names the step that failed, for the operator — "the workspace's
	// node", a reader's own name. A field name and never a value.
	What string
	// Err is the cause, the reader's own error. The refusal's message carries
	// it, as refuseIdentity's default arm does today, and a reader's error is
	// held by its own package never to name a credential, a token or a value of
	// any table.
	Err error
	// Caller is the identity the resolver had verified when the fault happened,
	// and nil where it had verified none — which is the whole of the difference
	// between a line and no line (see above).
	Caller *Identity
}

func (e *ScopeFault) Error() string {
	msg := "credsvc: the scope could not be built"
	if e.What != "" {
		msg += ": " + e.What
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap lets WS1's log name the reader's own error beside the fault.
func (e *ScopeFault) Unwrap() error { return e.Err }

// The channel is an error whatever else changes about it: WS1 branches on it
// with errors.As, as it branches on RunEnded today.
var _ error = (*ScopeFault)(nil)

// OfferingReader answers the credentials one call may be served from: the pool
// the cascade walks, before any credential is chosen.
//
// **It is a narrowing and never a widening.** Whatever it leaves out is never
// served — a credential it does not name cannot reach the cascade — and what it
// answers is still held to the legs the route applies itself (the repository's
// own admission, the crafts a node publishes and the repository's own auth
// record), so a reader that answered more than it should cannot widen a call;
// it can only be narrowed further. The direction is the safe one on purpose:
// the two deployments narrow at different points, and a contract that demanded
// the same point of both would be a contract the workspace could not meet
// without a second admission rule.
//
// The installation answers the bundle's own credentials, which the crafts and
// the admissions then narrow. A workspace answers the intersection of the
// workspace's pool with the MEMBER's own policy and the repository's admission
// in the member's own database (Workspace.Shared over WorkspaceShared of the
// bundle and the published node) — the same intersection R5 fixes, and the
// reason a member's run cannot be served a credential its own administrator
// did not admit.
//
// The bundle is passed in rather than held: the route loads it once for the
// credential VALUES, which a scope never carries, and a reader that loaded its
// own would be a second read of the one secret the service holds.
type OfferingReader interface {
	Offering(ctx context.Context, b *Bundle) ([]creds.Credential, error)
}

// ForgeReader is the forge half of the scope: the Bitbucket credential a
// proxied call is served with, and the coverage a forge call is admitted by.
//
// It is the readers the forge routes take from the seam (WS16, WS17), and it is
// deliberately the whole of what they take: the forge routes read the App key
// and the proxy's own configuration out of the bundle and the Config as they do
// today, and only what is per CALLER comes from here.
//
// **This is the surface C1 is least sure of, and the plan anticipates the
// change** (R-scope-forge, raised by WS16 or WS17 and reaching WS1 and WS2): if
// the forge routes turn out to need a reader this interface does not carry, the
// change is a contract change — a new method here, added to both
// implementations in the same sweep — and never a second reader reached around
// the scope.
type ForgeReader interface {
	// Bitbucket is the token store a proxied call is served with, and nil where
	// this deployment holds none — in which case the proxy REFUSES the call
	// rather than serving it with a token nothing chose (T8, D2355).
	//
	// **nil means refuse in BOTH modes, and an installation that wired no store
	// of its own answers the legacy selector explicitly** (bitbucketproxy.go's
	// legacySelector, which serves the bundle's legacy entry and the contract's
	// no_bitbucket_token class where the bundle holds none). Today the route
	// reads nil as "the legacy entry" itself (chooseBitbucketToken's own
	// fallback), and that fallback is what WS1 replaces with the scope's answer
	// when it takes the selector from here: a single-token installation is then
	// served exactly as it is now — which is why no existing test changes — and
	// a workspace scope that answers nil refuses, instead of being handed the
	// workspace bundle's legacy token by a fallback nobody chose. The two modes'
	// answers differ in kind here, so it is written down rather than left to the
	// route's reading of a nil.
	//
	// An installation's is its own store, over its own database. A workspace's
	// is the workspace's, and the tokens it holds are the workspace owner's
	// coverage for the repositories it declares (D2978: one cascade for every
	// kind, so a forge credential is served to a member's run the same way a
	// model one is).
	Bitbucket() bbtokens.Selector
	// Covers reports whether this caller's forge calls may reach the canonical
	// repository at all: an installation covers every repository its table
	// holds, and a workspace covers the repositories the workspace owner's own
	// declaration names for the calling job account (C3's forge entry, WS15).
	//
	// It is asked BESIDE the member's policy and never instead of it: a
	// repository the workspace owner declared and the member's administrator
	// did not admit is refused, and so is one the member admitted and the
	// owner did not declare — the two are different fences and a call has to
	// pass both (R5, D1298, WS16's tests). An error is a refusal: a node that
	// could not be read is not a node that covers nothing.
	Covers(ctx context.Context, canonical string) (bool, error)
}

// WorkspaceNodeReader reads the workspace's own published credentials node: the
// member table, the pool's entries and their forge coverage (C3's version 2,
// projectpolicy.PathWorkspaceCredentials).
//
// It is the one thing the workspace's resolver reads that is not a database of
// a member's: the node is the workspace's own, published by the workspace
// owner's `fugaro update`, and it is what says which members there are and what
// the pool offers. **It is therefore what ADMITS a caller, and the frozen
// *Workspace the resolver is built beside is not** (WorkspaceScopeConfig's own
// comment): the workspace's process serves the table it read at startup and no
// other, so a member removed from the node is refused from the next read of it
// (R11) while the table still resolves the identity. The scope re-reads it when
// the copy it holds is older than 60 seconds, conditionally, so a member added
// or an entry shared or unshared takes effect within a minute with no new
// revision (R13) — and a re-read that FAILS refuses the call rather than serving
// from the stale copy, because a table that cannot be re-read is not a table
// that still admits what it said (R17). That refusal is a *ScopeFault and not a
// refusal of the caller: the caller did nothing wrong and is owed the truth
// that the service could not answer it (R16, ScopeFault's own comment).
type WorkspaceNodeReader interface {
	// WorkspaceNode answers the node, false where none is published, and an
	// error where the node could not be read or decoded. Absent is not an
	// error and not an empty node: a workspace that published none has no
	// member, and a service that read it as an empty table would refuse every
	// caller with the wrong sentence.
	WorkspaceNode(ctx context.Context) (*projectpolicy.WorkspaceCredentials, bool, error)
}

// WorkspaceScopeConfig is what the workspace's resolver is built from.
//
// Everything in it is required except Meter, and the constructor refuses a
// configuration that is missing one, for NewWorkspace's reason: a resolver that
// starts without a front door or a node is a resolver that answers every call
// with something, and none of those somethings is what its caller must be told.
type WorkspaceScopeConfig struct {
	// Workspace is the workspace's verified front door: Caller resolves which
	// member a job account belongs to, and Verify resolves the run identity
	// beside it and answers the MemberCall the routes that act for a member
	// take. It is the same value the process serves with.
	//
	// **It is the verifier and not the membership, and the difference is a
	// fence.** A *Workspace is built once from WorkspaceConfig.Members
	// (NewWorkspace's readMembers) and has no refresh of its own, so the table
	// it resolves against still holds a member the workspace owner has since
	// removed — F26 is exactly that, and R13 is its consequence. Who the scope
	// ADMITS is therefore the node's and only the node's: a caller the
	// re-read node no longer lists is refused from the service's next read of
	// it (R11), whatever the frozen table still resolves, and a resolver that
	// took its members from here instead would keep a removed member served —
	// and drawing on the workspace's pool — until a new revision. What this
	// field is for is what only the verifier holds: which member a job
	// account's identity belongs to, that member's own outcome reader, and the
	// audience the run's token was minted for.
	Workspace *Workspace
	// Node is the workspace's own published credentials node, re-read as
	// WorkspaceNodeReader describes.
	Node WorkspaceNodeReader
	// Meter is the workspace's own cap, taken where a call is paid for. It is
	// the only optional field: a workspace that declared no cap still serves
	// its pool, and its Scope.Meter answers nil — the same answer an
	// installation's does (D1301 as amended, R15).
	Meter *WorkspaceMeter
	// Now is the clock the node's freshness is measured against; nil is
	// time.Now.
	Now func() time.Time
}

// WorkspaceScopeResolverFunc is the signature of the workspace resolver's
// constructor: the whole of what C1 produces for WS5, which builds the process
// with it, and for WS2, which supplies the body.
type WorkspaceScopeResolverFunc func(cfg WorkspaceScopeConfig) (ScopeResolver, error)

// NewWorkspaceScopeResolver builds the workspace's resolver.
//
// **It is a variable and not a function, and that is the contract's own
// shape.** WS5 built a workspace-mode process through this constructor while
// WS2 — which owns the implementation, in scope_workspace.go — was still being
// written, so the signature had to exist before its body did; and Go has no way
// to declare a function in one file of a package and define it in another,
// because two definitions of one name are a compile error whichever file they
// are in. The two sides assigned it from init while the scaffold was up, and
// I1 deleted the stub's assignment rather than editing it, which is what leaves
// the name a variable with one writer.
//
// Nothing outside the package may assign this: a service whose resolver was
// replaced after it was built would be a deployment served under a scope its
// configuration never named. scope_workspace.go's init is the only assignment,
// so a caller does not have to consider any of it: it calls the constructor and
// gets the workspace's resolver.
var NewWorkspaceScopeResolver WorkspaceScopeResolverFunc

// WorkspaceKeepWarmPath is the keep-warm route's path: where a run that routed
// a kind to the workspace posts its beat, at the slip beat's interval, so that
// the service is not scaled to zero between the calls it pays for (R14, D2985).
//
// The route takes the job's identity and carries no secret — no slip id and no
// claim secret — because the beat the run already has posts both, and R18
// refuses a call to the workspace's service that presents either (F33). It is
// the one route of the workspace mode that answers nothing about a credential
// and the one that must therefore exist before the service serves anything: a
// beat posted to a route the deployment does not serve is a 404, which a run
// reads as the service being unreachable.
//
// The path is C1's to name and is read from here by both sides — the service
// that serves it (WS2) and the run that posts it (WS13, which reads it from
// C1). It sits under /v1/ with the rest of this service's contract and names
// the workspace, because it is the workspace mode's route and not an
// installation's: an installation's scope answers Serves false for it and the
// run posts no beat there, because a run that routed nothing to the workspace
// has nothing to keep warm.
const WorkspaceKeepWarmPath = "/v1/workspace/keep-warm"
