package credsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The workspace's scope (WS2, R1, R5, R8, R13, R14, R17, R18, R19): one
// deployment's answer to "which member is this call, and which of its own state
// does it read" — the other side of C1's seam, over a *Workspace, the published
// node and the workspace's own store.
//
// **It is the installation's resolver with the readers moved, and nothing else
// about the service changes.** The front door, the gate, the cascade's model
// path and the refusals are one code path for both deployments (server.go,
// cascade.go); what differs is what a scope's accessors answer, and each of
// them is answered here from the member the call verified rather than from one
// configuration:
//
//   - the POLICY, the REPO AUTH record and the OUTCOMES are the MEMBER's, read
//     from the database its verified `iss` names, because that is where the
//     member's own administrator and the member's own `fugaro update` wrote
//     them (C1's rule 2);
//   - the STATE is the WORKSPACE's own, because a cooldown, a rotation
//     position and a lease for a workspace credential are facts about the
//     workspace's pool, and because the service holds no write on any member at
//     all (R8, D1299 as amended);
//   - the STOP SWITCHES are BOTH: the CALLING MEMBER's own pair, read from its
//     own database as its own service reads it, and the WORKSPACE's own switch,
//     read from the workspace's own database — which is where `fugaro budget
//     kill --workspace` writes it and how one command stops every member
//     (D1302) — because neither stop contains the other (workspaceKills);
//   - the REPOSITORY TABLE is the CALLING JOB ACCOUNT's, which is where the
//     run's verified repository is held to what the workspace owner declared
//     for that account (R6, D2992, R19);
//   - the OFFERING is the intersection of the workspace's pool with the
//     member's own policy and admission, which is R5 and is the whole of what
//     the pool adds;
//   - and the ROUTE SET and the SLIP answer are the deployment's, not the
//     caller's: a workspace serves the model routes, the forge routes a run
//     calls and the keep-warm route, and refuses everything else (R14), and a
//     call to it carries no member secret, so one that presents a slip is
//     refused (R18).
//
// **What ADMITS a caller is the node, and what VERIFIES it is the frozen
// *Workspace** (R11, R13, C1's rule 11). The table the process was built from
// is read once and never refreshed, so a member the workspace owner has removed
// since would go on being served — and drawing on the pool — until a new
// revision. The resolver therefore re-reads the published node when the copy it
// holds is older than a minute, and holds the caller to what it says: a caller
// the node does not name is refused even though the frozen table still resolves
// its token. A re-read that fails refuses the call too, as a *ScopeFault and
// never as a verdict about the caller (R17): the caller did nothing wrong, and
// a 403 is what a run's gateway reads as a missing invoke grant (R16).
//
// Two things about this file are worth a reader's attention because they are
// where C1's shape does not reach:
//
//   - **A refusal of a VERIFIED caller is answered by the gate, and this
//     resolver deliberately writes none of its own.** The credentials service's
//     own rule is that every refusal after a verification writes exactly one
//     audit line (.claude/rules/credentials-service.md), and the auditor belongs
//     to the Server — which C1's WorkspaceScopeConfig does not carry and cannot
//     (scope.go's own comment on the three answers Resolve may give). So the
//     one refusal this resolver answers about a caller it has verified is the
//     *ScopeFault, whose Caller field is exactly the channel that gets the line
//     written by the gate (refuseResolve); the run's repository is held to the
//     calling account by answering Repos with THAT account's repositories, so
//     the refusal lands at the pairing, which is where C1's rule 3 puts it and
//     where the gate writes one audited typed refusal; and the sentence that
//     names `fugaro update` as the fix is the RUN's, exactly as R16's missing
//     invoke grant is (R19's own words: "the run halts naming the workspace
//     owner's `fugaro update`, as R16 does"). What is NOT done here is a
//     callerRefusal whose answer closure would write a refusal nobody audited:
//     a refusal with no line is the one thing this package's rules forbid
//     outright.
//   - **The Bitbucket store is the NODE's, and it reads no secret** (C1's rule
//     10, and the reading C1's own comment left open). The selector a proxied
//     call is served with is built over the workspace's own declaration —
//     which token the owner's node names for the run's repository, by the name
//     the workspace's bundle holds it under — and the VALUE is read by the
//     route, out of the one snapshot it decided the call on. So the scope
//     answers a store without reading the workspace's secret at all, and
//     R-scope-forge, whose change C1 anticipated as a reader that would need
//     the bundle, turned out to need no new method: what the store needed was
//     to stop answering nil. See workspaceTokens below.
func init() {
	NewWorkspaceScopeResolver = newWorkspaceScopeResolver
}

// workspaceNodeTTL is how long a copy of the published node is served before it
// is re-read: R13's minute, which is the whole budget of a member added or an
// entry shared or unshared taking effect with no new revision. It is a constant
// and not a configuration for the reason the plan gives — the number is the
// design's, and WorkspaceScopeConfig.Now is what a test moves to reach it.
const workspaceNodeTTL = 60 * time.Second

// newWorkspaceScopeResolver builds the workspace's resolver.
//
// Every field C1 declares is required, and the refusals are this constructor's
// rather than the first call's for NewWorkspace's reason: a resolver that
// started without a front door or a node would answer every call with
// something, and none of those somethings is a call served under the
// workspace's rules.
//
// **The store a call's state is read and written through is the
// *Workspace's own** (R8), and it is the one thing this constructor does not
// ask for: C1's WorkspaceScopeConfig carries no store, and none is wanted —
// NewWorkspace already refuses a configuration without one, so every *Workspace
// this resolver can be built over has a store, and Workspace.State is what
// answers it.
func newWorkspaceScopeResolver(cfg WorkspaceScopeConfig) (ScopeResolver, error) {
	switch {
	case cfg.Workspace == nil:
		return nil, fmt.Errorf("%w: the workspace's resolver has no front door, so no caller could be verified", ErrWorkspaceConfig)
	case cfg.Node == nil:
		return nil, fmt.Errorf("%w: the workspace's resolver has no node reader, so no caller could be admitted and no pool offered", ErrWorkspaceConfig)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &workspaceResolver{w: cfg.Workspace, node: cfg.Node, meter: cfg.Meter, now: now}, nil
}

// workspaceResolver is the workspace's ScopeResolver: the deployment's three
// facts, the copy of the node it holds and the clock its freshness is measured
// against.
//
// The node is cached on the resolver and not on a scope because a scope is one
// call and the copy is the DEPLOYMENT's: R13 asks for a minute of staleness on
// the service, and a copy per call would re-read the node on every call the
// service answers — the read the minute exists to avoid — while a copy in the
// process's configuration would never be re-read at all (F26).
type workspaceResolver struct {
	w     *Workspace
	node  WorkspaceNodeReader
	meter *WorkspaceMeter
	now   func() time.Time

	// mu guards held and heldAt, and it is held ACROSS the read: two calls
	// arriving on a stale copy would otherwise both re-read the node, and the
	// node is the one read this service makes that is not a member's own.
	mu     sync.Mutex
	held   *projectpolicy.WorkspaceCredentials
	heldAt time.Time
}

// Resolve answers the scope for one call of a workspace's service.
//
// A route answered BEFORE the shared parse is resolved with no identity read at
// all, exactly as the installation's resolver answers one: those routes' callers
// hold no run identity, so RequestFromHTTP would refuse the call for the missing
// headers before a class ever read the token. Every one of them is a route
// workspace mode does not serve (R14), so the scope they get is the uncalled
// one and the gate's refusal of it is the service's own, with no audit line —
// which is what a route that was never a call to this deployment deserves.
//
// A served route is verified and then held to the node; a route the workspace
// refuses but the shared parse answers (the sign route is one) is verified and
// then answered with a scope whose Serves is false, so that its refusal is the
// audited one a verified caller's refusal is (server.go's refuseNotServed reads
// the caller for exactly that).
//
// The three answers C1 fixes are the three below: a scope, a callerRefusal
// where the caller could not be admitted, and a *ScopeFault where the scope
// could not be built for a reason that is not the caller's — which is the
// node's own failure, and a member the node no longer names.
func (res *workspaceResolver) Resolve(ctx context.Context, r *http.Request) (Scope, error) {
	if aheadOfTheSharedParse(r) {
		return unservedWorkspaceScope(Identity{}, res.meter), nil
	}
	req, err := RequestFromHTTP(r)
	if err != nil {
		// The contract's own reading of the headers, whose errors name a header
		// and never a value, and no caller has been verified at this point: the
		// refusal is the service's and writes no audit line.
		return nil, refuseCaller(err.Error(), func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
			WriteWireError(w, StatusUnverified, WireError{Error: ErrorUnverifiedRunIdentity, Message: err.Error()})
		})
	}
	call, err := res.w.Verify(ctx, req)
	if err != nil {
		// The member's own verifier refused the run identity beside the job's,
		// and the refusal is the same one the installation's front door writes
		// for the same failure (refuseIdentity): the type a run switches on, and
		// no value of the token in the sentence.
		//
		// What is NOT here is that function's cleanup of an ended run — the
		// placement and the lease it removes, and the slip it ends. Both are
		// writes into the WORKSPACE's own state, they belong to the Server
		// (refuseIdentity, forgetRun), and no field of WorkspaceScopeConfig
		// carries the server or a patcher. A run whose token is presented after
		// it ended is refused all the same; what is left behind is its placement,
		// which the next selection of the same run replaces.
		status, typ := workspaceRefusalOf(err)
		message := err.Error()
		return nil, refuseCaller(message, func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
			WriteWireError(w, status, WireError{Error: typ, Message: message})
		})
	}
	if !servesWorkspaceRoute(r.URL.Path) {
		return unservedWorkspaceScope(call.Identity, res.meter), nil
	}
	node, err := res.nodeFor(ctx)
	if err != nil {
		return nil, &ScopeFault{What: "the workspace's published node", Err: err, Caller: &call.Identity}
	}
	if !workspaceNodeAdmits(node, call) {
		// R11 and C1's rule 11: the node is what admits a caller, and this one
		// it no longer names — a member the workspace owner removed, or an
		// account dropped from a member's row.
		//
		// The answer is the fault rather than a refusal of the caller, and that
		// is this file's one departure from C1's words, taken rather than hidden
		// (the file's own comment says why): a refusal of a VERIFIED caller owes
		// one audit line, and this resolver has no auditor to write it with. A
		// fault carries the verified Caller, so the gate writes that line, the
		// refusal is 5xx rather than the 403 a run would halt on as a missing
		// invoke grant, and the run's own bounded wait is what a member whose
		// workspace no longer admits it gets — which is the truth of the
		// situation, because the fix is the workspace owner's `fugaro update` and
		// retrying changes nothing until it runs.
		return nil, &ScopeFault{
			What:   "the workspace's published node",
			Err:    fmt.Errorf("%w: the published node does not name this caller's account, so the pool is not served to it", ErrUnverifiedJob),
			Caller: &call.Identity,
		}
	}
	sc, err := res.scopeFor(call, node)
	if err != nil {
		return nil, &ScopeFault{What: "the member's own database", Err: err, Caller: &call.Identity}
	}
	return sc, nil
}

// nodeFor answers the node, re-reading it when the copy held is older than
// workspaceNodeTTL (R13) and REPORTING a re-read that failed (R17).
//
// A failed re-read drops the copy held rather than serving it: a table that
// cannot be re-read is not one that still admits what it said, and the caller
// that follows is refused by the fault Resolve builds around this error. A node
// that is ABSENT on a successful re-read is not an error here and is not an
// empty table either — the caller's admission is decided by workspaceNodeAdmits
// below, which refuses everybody, because a workspace that published no member
// serves nobody.
func (res *workspaceResolver) nodeFor(ctx context.Context) (*projectpolicy.WorkspaceCredentials, error) {
	res.mu.Lock()
	defer res.mu.Unlock()
	now := res.now()
	if res.held != nil && now.Sub(res.heldAt) < workspaceNodeTTL {
		return res.held, nil
	}
	node, found, err := res.node.WorkspaceNode(ctx)
	if err != nil {
		res.held, res.heldAt = nil, time.Time{}
		return nil, err
	}
	if !found {
		res.held, res.heldAt = nil, now
		return nil, nil
	}
	res.held, res.heldAt = node, now
	return node, nil
}

// scopeFor builds the scope of a verified member's call over the node.
//
// The member's own database, the account's repositories and the pool's
// intersection are all read here, once, and the scope carries what they
// answered: a scope is one call's, and the readers the model path asks are
// built over the one database the call resolved to — which is what keeps a
// second read of the node or the member's row from being possible below this
// line. The stop switches are the exception and are deliberately TWO readers
// behind one KillReader: the calling member's own pair, over its own database,
// and the workspace's own switch, over the workspace's (workspaceKills).
func (res *workspaceResolver) scopeFor(call MemberCall, node *projectpolicy.WorkspaceCredentials) (Scope, error) {
	db, err := res.w.database(call)
	if err != nil {
		return nil, err
	}
	repos := workspaceAccountRepos(node, call)
	return workspaceScope{
		id:    call.Identity,
		meter: res.meter,
		readers: workspaceReaders{
			policy:   RTDBPolicy{DB: db},
			kills:    workspaceKills{workspace: res.w.Kills(), member: db},
			state:    res.w.State(),
			repoAuth: &creds.RTDBStore{DB: memberReadOnly{Getter: db}},
			outcomes: RTDBOutcomes{DB: db},
			offering: workspaceOffering{w: res.w, call: call, node: node},
			forge:    workspaceForge{repos: repos, tokens: workspaceTokensOf(node)},
			repos:    repos,
		},
	}, nil
}

// workspaceKills is the stop switches a workspace-mode call is held to, and the
// choice contract-scope.md left open — "the member's own operator's, the
// workspace's, or both read together" — is BOTH, read together, because they
// are two stops an operator holds over two different things and neither
// contains the other:
//
//   - **The workspace's own switch** is `config/kill/global` of the workspace's
//     own database, which `fugaro budget kill --workspace <name>` writes
//     (D1302): it stops the runs of every member of the workspace, and it makes
//     the workspace's service refuse every shared-credential call — and that
//     refusal IS this reader, because the switch is in a database no member can
//     write and no member's operator can undo (spec.md §6, §8). A scope that
//     read the member's switches alone would serve every call of a workspace its
//     owner had stopped, and the gate's own comment would be false: "the
//     installation's own pair for an installation, and the workspace's for a
//     workspace" (server.go).
//   - **The calling member's own pair** is what that member's operators write
//     in that member's own database (`fugaro budget kill` and `--repo`), read
//     here exactly as the member's own service reads it (RTDBKills): a member's
//     stop is a narrowing of what its own runs may do, and a call drawing on the
//     workspace's pool on that member's behalf is still that member's run.
//
// **The pair is read as the service's own reader reads it and no wider**, and
// the workspace's own switch as the workspace's own: `ReadGlobalKill` on the
// workspace's database and never `ReadKills`, because a workspace's switch is
// its global one and never a repository's (the CLI's own comment on the write),
// so a repository's node of the workspace's database is not read at all. Which
// of the two stopped the call is named on the refusal the run reads — the
// workspace's word is "workspace", not "global", because "global" is the word
// the member's own operator reads against its OWN database, where the same word
// names a different node — and an unreadable switch of either is an error the
// gate refuses on rather than an off switch (budget's own fail-closed rule in
// one place: the two reads below are budget's).
type workspaceKills struct {
	// workspace is the workspace's OWN database; *rtdb.Client is one.
	workspace budget.KillGetter
	// member is the CALLING member's own database, which is also what the
	// scope's other readers read.
	member budget.KillGetter
}

// workspaceKillWord is what the gate's refusal names when the switch that
// stopped the call is the WORKSPACE's own: "the credentials service is stopped
// by the workspace kill switch". It is deliberately not budget's "global": that
// word is what a member's operator greps its own database for, and the switch
// here is one they hold no grant on and can neither read nor lift.
const workspaceKillWord = "workspace"

func (k workspaceKills) Stop(ctx context.Context, slug string) (bool, string, error) {
	// The workspace's own switch first, so the stop asks nothing of a member
	// for itself: it is one read of the workspace's own database, and a
	// stopped workspace refuses every member's call on it without a member's
	// switches being consulted at all — which is the whole of D1302's reach,
	// and it is what makes the stop one no member's operator can undo.
	killed, err := budget.ReadGlobalKill(ctx, k.workspace)
	if err != nil {
		return false, "", err
	}
	if killed.Global != nil && killed.Global.On {
		return true, workspaceKillWord, nil
	}
	return RTDBKills{DB: k.member}.Stop(ctx, slug)
}

// memberReadOnly is one member's database as creds' own reader of the
// per-repository auth record takes it: a creds.DB, which is what
// creds.ReadRepoAuth is written against, built over the read-only handle the
// deployment opens a member with (WorkspaceConfig.Open returns a Getter, and
// the grant behind it is the read-only role check 771 measures).
//
// **The two writers refuse, and nothing on this path reaches them.** The record
// is read and never written: creds.ReadRepoAuth calls GetETag and nothing else,
// and the scope hands this value out as a RepoAuthReader — an interface with
// the one read on it — so the writers exist only because the reader's own
// package declares them. A service that reached one would be a service writing
// a member, which is the one thing this deployment must not do (R8), and it
// would be answered an error rather than a write.
//
// GetETag answers no etag because a Get cannot read one and no caller here asks
// for one: an etag is the condition of a compare-and-set, and every write of
// the record is the member's own `fugaro update`'s.
type memberReadOnly struct {
	Getter
}

var errMemberReadOnly = errors.New("credsvc: a workspace's scope reads a member's database and holds no write on it, so nothing may be written through this handle (R8)")

func (m memberReadOnly) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	found, err := m.Get(ctx, path, out)
	return "", found, err
}

func (memberReadOnly) PutIfMatch(context.Context, string, string, any) error {
	return errMemberReadOnly
}

func (memberReadOnly) Patch(context.Context, string, map[string]any) error {
	return errMemberReadOnly
}

// workspaceRefusalOf maps a verification failure to the status and the type the
// run reads, exactly as refuseIdentity maps the same three failures for the
// installation: the job's own token, the run's, and a state the check could not
// be made against (which is the service's failure and not the caller's).
func workspaceRefusalOf(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnverifiedJob):
		return StatusUnverified, ErrorUnverifiedJobIdentity
	case errors.Is(err, ErrUnverified):
		return StatusUnverified, ErrorUnverifiedRunIdentity
	default:
		return StatusUnavailable, ErrorStateUnavailable
	}
}

// workspaceReaders is the set of readers a served workspace call answers with.
// They are built together because they are one call's — the member's database
// is the same object for four of them — and the scope's accessors hand them out
// one at a time (C1's shape).
type workspaceReaders struct {
	policy   PolicyReader
	kills    KillReader
	state    StateStore
	repoAuth RepoAuthReader
	outcomes OutcomeReader
	offering OfferingReader
	forge    ForgeReader
	repos    map[string]string
}

// workspaceScope is the workspace's per-call scope: the run identity the call
// proved, the route answers and the readers the member's own database serves.
type workspaceScope struct {
	id      Identity
	readers workspaceReaders
	meter   *WorkspaceMeter
	// unserved marks the scope built for a route this deployment does not serve
	// at all, and it is a field rather than a read of the path because a served
	// workspace scope and an unserved one are handed the SAME path: the build
	// half of the git proxy is answered ahead of the shared parse, so the scope
	// it gets is the unserved one, while Serves over R14's set answers true for
	// the proxy's prefix — the gate would then dispatch the call to the build
	// proxy, which reads the installation's own configuration and bundle around
	// the scope this deployment exists to put in front of it. An unserved scope
	// therefore refuses every path.
	unserved bool
}

// unservedWorkspaceScope is the scope a call gets that the workspace does not
// serve — a route answered before the shared parse, whose caller holds no run
// identity at all, or a route of the shared parse the deployment refuses. Its
// readers are the refusing ones: the gate refuses the call before a handler is
// dispatched (Serves is false for EVERY path, which is what the marker field is
// for — see workspaceScope), and a scope that answered a reader here would be a
// scope that could serve one.
//
// It still carries the caller when there is one, because the gate's refusal of
// a route it does not serve is written with the caller's identity and purpose
// where a verification happened (refuseNotServed): the operator's line has to
// name who called a route this deployment does not answer.
func unservedWorkspaceScope(id Identity, meter *WorkspaceMeter) workspaceScope {
	refuse := refusingWorkspace{}
	return workspaceScope{
		id:       id,
		meter:    meter,
		unserved: true,
		readers: workspaceReaders{
			policy:   refuse,
			kills:    refuse,
			state:    refuse,
			repoAuth: refuse,
			outcomes: refuse,
			offering: refuse,
			forge:    refuse,
		},
	}
}

func (sc workspaceScope) Caller() Identity { return sc.id }

func (sc workspaceScope) Policy() PolicyReader     { return sc.readers.policy }
func (sc workspaceScope) Kills() KillReader        { return sc.readers.kills }
func (sc workspaceScope) State() StateStore        { return sc.readers.state }
func (sc workspaceScope) RepoAuth() RepoAuthReader { return sc.readers.repoAuth }
func (sc workspaceScope) Outcomes() OutcomeReader  { return sc.readers.outcomes }
func (sc workspaceScope) Offering() OfferingReader { return sc.readers.offering }
func (sc workspaceScope) Forge() ForgeReader       { return sc.readers.forge }

// Repos answers the repositories the CALLING JOB ACCOUNT is a job of, as the
// workspace owner declared them (R6, D2992): one for a repository's own job,
// several for a shared project job.
//
// **It is where R19 lands, and it lands here rather than in a refusal of this
// resolver's own for the reason the file's comment gives**: the gate resolves
// the run identity's slug through this table and refuses a slug it does not hold
// with one audited typed refusal (server.go's pairing), which is C1's rule 3's
// own words — "a run whose repository is not in that list is refused at the
// pairing" — and the reason a run reads for it is the one R16 and R19 make the
// run name the fix for. A repository the workspace does not list is never
// resolved by guessing it from the slug's text (D1066): the table holds the
// canonical names the owner wrote, and each key is the slug task.Slug derives
// from one of them, which is the same direction the publish path keys the
// repository's own admission node with.
//
// An account the node lists with NO repository answers an empty table, which
// refuses every run of it (R19's own state: a version-1 row, or a member whose
// owner has not declared its repositories yet). It is an empty table and not a
// nil one, and neither is a wildcard.
func (sc workspaceScope) Repos() map[string]string { return sc.readers.repos }

// Meter answers the workspace's own cap, and never nil for a workspace whose
// pool has one (C1's rule 9): the meter is the resolver's, so it is answered
// whether or not the call reached a served route, and the null is a workspace
// that declared no cap rather than a call that was refused.
func (sc workspaceScope) Meter() *WorkspaceMeter { return sc.meter }

// Serves answers whether workspace mode serves the route at path: the model
// routes, the forge routes a run calls (the git proxy, a run's own token mint
// and its context tokens) and the keep-warm route. Every other route is refused
// with a typed refusal, which is what tells a member's run to keep calling its
// OWN project's service for them (R14, D2988, A2).
//
// The review routes are deliberately not served, and they are the pair a reader
// is most likely to expect here: a review call is the member's own service's
// business, and a workspace that served it would offer the pool to a class of
// caller C1's fence — the calling job account's repositories — does not
// describe (WS1's own review note). The world's route refused here is the
// member's own service's.
//
// **An unserved scope refuses every path, and the route set is asked only of a
// scope that was built for a call this deployment resolved.** The two are told
// apart by the scope and not by the path, because one path is handed to both:
// the build half of the git proxy is answered ahead of the shared parse and gets
// the unserved scope, while the proxy's own prefix is in R14's set — so a path
// predicate alone would tell the gate to dispatch that call to the build proxy,
// which reads the installation's configuration rather than the scope.
func (sc workspaceScope) Serves(path string) bool {
	if sc.unserved {
		return false
	}
	return servesWorkspaceRoute(path)
}

// AcceptsSlip answers false: a call to the workspace's service carries the
// job's identity and the run's token and NOTHING else of the member's — no slip
// id, no claim secret, no auth-mode or account header — so a call that presents
// a slip is refused rather than served (R18, F40). A call that presents none is
// unaffected: the slip-less path is what this service served before the slips
// existed.
func (sc workspaceScope) AcceptsSlip() bool { return false }

// servesWorkspaceRoute is R14's route set, and it is one function because the
// front door's gate and this file's tests have to agree about it.
func servesWorkspaceRoute(path string) bool {
	if strings.HasPrefix(path, BitbucketProxyPrefix) {
		// The git proxy, in both halves: a run's proxied traffic and the build
		// class's, which R14 serves as the one forge route a run calls for its
		// own repository's traffic.
		return true
	}
	switch path {
	case MessagesPath, CountTokensPath, MintPath, ContextTokenPath, WorkspaceKeepWarmPath:
		return true
	}
	return false
}

// workspaceOffering is the pool this call may be served from: the intersection
// of what the workspace offers this member (the node's shared entries with the
// tiers the bundle holds) with the member's own policy and the admission of the
// repository the run identity verified (R5, D1298).
//
// The intersection itself is Workspace.Shared's and is not repeated here; what
// this reader adds is the NODE the pool is read from, which the route does not
// hold. The node is the one the resolver has already admitted the caller
// against, and it is handed in rather than re-read: a pool read from a second
// copy of the node could offer a credential the node that admitted the caller no
// longer names.
//
// The bundle is the route's, handed in and never held (C1's OfferingReader): the
// values a call is finally served are read where the bundle is loaded, and a
// reader that loaded its own would be a second read of the one secret this
// service holds.
type workspaceOffering struct {
	w    *Workspace
	call MemberCall
	node *projectpolicy.WorkspaceCredentials
}

func (o workspaceOffering) Offering(ctx context.Context, b *Bundle) ([]creds.Credential, error) {
	return o.w.Shared(ctx, o.call, WorkspaceShared(b, o.node))
}

// workspaceForge is the coverage this call's forge traffic is admitted by, and
// it is the CALLING JOB ACCOUNT's repositories — the same list the pairing
// resolves through, read from the same node row, which is the one fact about a
// forge call that is per caller (C1's rule: an installation covers every
// repository its table holds, a workspace covers the repositories the owner's
// declaration names for the calling account).
//
// It is asked BESIDE the member's own policy and never instead of it (R5): a
// repository the owner declared and the member's administrator did not admit is
// refused, and so is one the member admitted and the owner did not declare.
//
// The token store is the workspace's own DECLARATION and never its bundle: the
// names the owner's node shares, chosen for the repository the call is on, and
// read for a value by the route out of the snapshot it already holds (C1's rule
// 10, workspaceTokens below).
type workspaceForge struct {
	repos  map[string]string
	tokens workspaceTokens
}

func (f workspaceForge) Bitbucket() bbtokens.Selector { return f.tokens }

func (f workspaceForge) Covers(_ context.Context, canonical string) (bool, error) {
	for _, held := range f.repos {
		if held == canonical {
			return true, nil
		}
	}
	return false, nil
}

// workspaceTokens is the Bitbucket store a workspace serves a proxied call out
// of: the tokens the owner's node declares, in the node's own order, each with
// the repositories the owner's credential is stored for (C3's `covers`).
//
// **It holds names and no values, and that is C1's rule 10 read rather than
// worked around.** The store is built where the bundle is DECIDED — the node the
// resolver has already admitted the caller against — and the value of the name
// it chooses is read by the route out of the same snapshot that admitted the
// call, so the workspace's one secret is read once per call and never here. The
// interface was shaped for this: Select is handed the Snapshot (C1, D2355), and
// snapshotValue is the one path a value is read on.
//
// **A name the bundle does not hold is skipped, and so is one that does not
// cover this repository**: the pool's own rule (WorkspaceShared's two skips),
// read on the proxy's side of the same declaration. The selection ranks nothing
// and asks no project: what a call may reach is the fence Covers and the
// member's own policy have already answered (R5), and the owner's coverage says
// which of the workspace's credentials pays for it. A read the projection cannot
// make — a token whose value the bundle holds for no name the node declares — is
// the contract's no_bitbucket_token class, which is a refusal and never a call
// forwarded without a credential (D143).
type workspaceTokens []workspaceToken

// workspaceToken is one entry of the node's shared list: the name the bundle
// holds the credential under and the concrete repositories it is stored for.
type workspaceToken struct {
	name   string
	covers []string
}

// workspaceTokensOf is the node's Bitbucket declaration, and the only reading of
// it here: a model entry (and a version-1 entry, which is one) names no
// repository this store could choose a token for, and a GitHub entry is the App
// key's, which the mint reads out of the bundle itself.
func workspaceTokensOf(node *projectpolicy.WorkspaceCredentials) workspaceTokens {
	if node == nil {
		return nil
	}
	var out workspaceTokens
	for _, s := range node.Shared {
		if s.Kind != gitprov.KindBitbucket {
			continue
		}
		out = append(out, workspaceToken{name: s.Name, covers: slices.Clone(s.Covers)})
	}
	return out
}

func (t workspaceTokens) Select(_ context.Context, snap bbtokens.Snapshot, canonical string, _ bool) (bbtokens.Choice, error) {
	for _, tok := range t {
		if !slices.Contains(tok.covers, canonical) {
			continue
		}
		if _, ok := snapshotValue(snap, bbtokens.Name(tok.name)); !ok {
			continue
		}
		return bbtokens.Choice{Name: bbtokens.Name(tok.name)}, nil
	}
	for _, tok := range t {
		if slices.Contains(tok.covers, canonical) {
			// The owner declared this repository and the workspace's bundle
			// holds no value under the name: a half-written declaration, which
			// is the store's refusal and not the coverage's (the class names the
			// store and never a token).
			return bbtokens.Choice{}, &bbtokens.Refusal{Class: bbtokens.ClassNoToken}
		}
	}
	return bbtokens.Choice{}, &bbtokens.Refusal{Class: bbtokens.ClassNotCovered, Repo: canonical}
}

// refusingWorkspace answers every reader of a scope that must not serve: the
// uncalled scope's. Every method refuses rather than answering an empty value,
// which is the same direction the stub took and for the same reason — "this
// database is not this call's" is a different statement from "this policy
// admits nothing", and a reader that answered the second for the first would be
// a reader a route could act on.
type refusingWorkspace struct{}

var errWorkspaceUnserved = fmt.Errorf("%w: this route is one workspace mode does not serve, so no reader behind it was read", ErrUnverifiedJob)

func (refusingWorkspace) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	return nil, false, errWorkspaceUnserved
}

func (refusingWorkspace) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, errWorkspaceUnserved
}

func (refusingWorkspace) Stop(context.Context, string) (bool, string, error) {
	return false, "", errWorkspaceUnserved
}

func (refusingWorkspace) Marks(context.Context) (map[string]creds.Mark, error) {
	return nil, errWorkspaceUnserved
}

func (refusingWorkspace) UpdateMark(context.Context, string, func(creds.Mark, bool) (creds.Mark, error)) (creds.Mark, error) {
	return creds.Mark{}, errWorkspaceUnserved
}

func (refusingWorkspace) Leases(context.Context, time.Time) ([]creds.Lease, error) {
	return nil, errWorkspaceUnserved
}

func (refusingWorkspace) SetPlacement(context.Context, creds.Run, string, creds.Lease) error {
	return errWorkspaceUnserved
}

func (refusingWorkspace) Current(context.Context, creds.Run) (string, bool, error) {
	return "", false, errWorkspaceUnserved
}

func (refusingWorkspace) RoundRobin(context.Context) (int64, error) { return 0, errWorkspaceUnserved }

func (refusingWorkspace) RepoAuth(context.Context, string) (creds.RepoAuth, bool, error) {
	return creds.RepoAuth{}, false, errWorkspaceUnserved
}

func (refusingWorkspace) OutcomeExists(context.Context, int64, string, string) (bool, error) {
	return false, errWorkspaceUnserved
}

func (refusingWorkspace) Offering(context.Context, *Bundle) ([]creds.Credential, error) {
	return nil, errWorkspaceUnserved
}

func (refusingWorkspace) Bitbucket() bbtokens.Selector { return nil }

func (refusingWorkspace) Covers(context.Context, string) (bool, error) {
	return false, errWorkspaceUnserved
}

// workspaceNodeAdmits reports whether the published node still names the caller
// the frozen table verified: the member's row, and the account inside it (R11,
// R13).
//
// Both halves are asked because they are both writable by the workspace owner
// and both are a membership: a row removed says the member left the workspace,
// and an account dropped from a row says one of its jobs is no longer admitted —
// while the frozen table goes on resolving either until a new revision. The
// comparison is folded the way a presented claim is (foldEmail), which is how
// the frozen table matched the account in the first place, so the two cannot
// disagree about an address that differs only in case.
func workspaceNodeAdmits(node *projectpolicy.WorkspaceCredentials, call MemberCall) bool {
	if node == nil {
		return false
	}
	account, ok := foldEmail(call.Account)
	if !ok {
		return false
	}
	for _, m := range node.Members {
		if m.Project != call.Member.Project {
			continue
		}
		for _, held := range m.JobAccounts {
			if folded, ok := foldEmail(held); ok && folded == account {
				return true
			}
		}
		return false
	}
	return false
}

// workspaceAccountRepos is the repository table of one verified member's call:
// the canonical repositories the node declares for THAT account, keyed by the
// slug a run identity carries.
//
// The account is matched inside the member's own row and not across rows: the
// node's Validate refuses one account in two rows, and this reader asks the row
// of the member the token's pins were held to, so a second member's list can
// never answer for this call (finding 6's derivation read one layer up).
//
// A name task.Slug refuses is skipped rather than fatal, for canonicalOfSlug's
// reason: the node's own writer refuses such a list, and a hand-written one
// that names a repository no repository can be is a row that neither matches nor
// stops the others from being asked.
func workspaceAccountRepos(node *projectpolicy.WorkspaceCredentials, call MemberCall) map[string]string {
	account, ok := foldEmail(call.Account)
	if !ok || node == nil {
		return map[string]string{}
	}
	for _, m := range node.Members {
		if m.Project != call.Member.Project {
			continue
		}
		out := map[string]string{}
		for _, ja := range m.JobAccountRepos {
			folded, ok := foldEmail(ja.Account)
			if !ok || folded != account {
				continue
			}
			for _, canonical := range ja.Repos {
				slug, ok := slugOfCanonical(canonical)
				if !ok {
					continue
				}
				out[slug] = canonical
			}
		}
		return out
	}
	return map[string]string{}
}

// slugOfCanonical is the slug a canonical repository name hashes to, which is
// what a run identity's `fs` carries and what the pairing is keyed by. It is
// canonicalOfSlug's direction reversed, and neither is the other's inverse in
// general — a slug cannot be turned back into a name (F18) — so the direction is
// written where it is used and the search that needs the other one is
// canonicalOfSlug's.
func slugOfCanonical(canonical string) (string, bool) {
	provider, repo, ok := strings.Cut(canonical, "/")
	if !ok || provider == "" || repo == "" {
		return "", false
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return "", false
	}
	return slug, true
}
