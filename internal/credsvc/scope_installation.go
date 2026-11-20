package credsvc

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The installation's scope (WS1, R1): the deployment this service has always
// been, read through C1's seam.
//
// It is the other side of the contract, and it is deliberately boring: one
// bundle, one database, one table, every route, and every reader answered by
// the configuration NewServer was built from. What it buys is that the model
// path and the front door are written against a scope rather than against
// Config, so a workspace's scope can be served by the same code without a
// second copy of the cascade — which is the whole of R1.
//
// Nothing here decides anything new. The pairing, the refusals and the audit
// lines are the code that was in ServeHTTP, moved into the resolver because
// that is where the reads it makes belong (C1: the resolver owns the reads that
// come before any route is dispatched), and the readers below are the Config
// fields the handlers used to read directly.
type installationResolver struct {
	s *Server
}

// Resolve answers the scope for one call of an installation's own service: the
// verified caller, the canonical repository the pairing resolved, and the
// deployment's readers.
//
// A route answered BEFORE the shared parse is resolved with no identity read at
// all, and that is not an optimisation: those routes' callers hold no run
// identity — a verifier, a launcher, a build, the orchestrator daemon — so
// RequestFromHTTP would refuse the call for the missing headers before its own
// class ever read anything (server.go's own account of each). What the scope
// answers for them is the uncalled scope: Serves is still asked, so a
// deployment that does not serve such a route refuses it before it reaches a
// handler, and the refusal is the service's own and writes no audit line
// (Caller, C1).
func (res installationResolver) Resolve(w http.ResponseWriter, r *http.Request) (Scope, bool) {
	s := res.s
	if aheadOfTheSharedParse(r) {
		return installationScope{s: s}, true
	}
	ctx := r.Context()
	req, err := RequestFromHTTP(r)
	if err != nil {
		// The contract's own reading of the headers, never this server's
		// (review finding m7). Its errors name a header and never a value.
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
		return nil, false
	}
	id, err := s.cfg.Auth.Verify(ctx, req)
	if err != nil {
		s.refuseIdentity(ctx, w, r, err)
		return nil, false
	}
	// The purpose the call asks for and the credential a line for it names, and
	// what every refusal below is recorded as (C1.13): the route decides both
	// (routeAct, the one rule), and they are read here because every refusal
	// that follows — the binding, the stop switches and an unreadable switch —
	// is answered before the route is branched on, and because every refusal
	// below the mint's branch is recorded with THIS purpose as well: a line that
	// hard-coded one route's purpose would be a line an operator reads as
	// another call (a mint refused by the kill switch recorded as a proxied
	// model call was exactly that, before the purpose was read off the route).
	// A mint's line names the App key for the same reason (routeAct): the
	// credential the endpoint issues from is the endpoint's own name for it, not
	// a value read out of a bundle, which is why the mint itself (auditMint,
	// refuseMint) takes both from the same rule.
	purpose, credential := routeAct(r.URL.Path)
	// The pairing, which is where the two classes differ (spec §3.2), and the
	// canonical repository every check below is made against.
	//
	// A per-repository job's account names a repository, so the pairing is one
	// comparison: a run identity is honoured only by the job whose repository it
	// belongs to, which is what stops an admitted job account from presenting a
	// run token of another repository (review finding N1). A review job's
	// account names none, so that comparison cannot be made and must not be
	// replaced by "trust the run token": the repository is the run identity's
	// own slug, resolved through the installation's table and admitted to by the
	// calling account (pairReview). Either way the call reads the repository
	// from the pairing and not from the job account, because a review caller has
	// no job account to read it from.
	call := Caller{Identity: id, Request: req}
	switch id.JobClass {
	case ClassReview:
		repo, err := s.pairReview(ctx, id)
		if err != nil {
			s.refusePairing(ctx, w, id, purpose, credential, err)
			return nil, false
		}
		call.Canonical = repo
		// What the mint is handed in place of a job account: the same account,
		// the run identity's slug and the repository the pairing resolved. The
		// mint's own shape is unchanged — C3.9b is the task that chooses its
		// permission set from the class — and what it is scoped to is already
		// the pairing's answer here.
		call.Job = JobAccount{Email: id.JobAccount, Slug: id.Slug, Repo: repo}
	case ClassShared:
		// The shared class's pairing (G21, F40): the repository is the run
		// identity's own slug and the job's row says which profile's
		// repositories that may be, so the comparison the per-repository class
		// makes against its one repository is made here against the profile's
		// list. What is handed to the mint is spelled the way a review caller's
		// is, and for the same reason: the account names no repository, so the
		// pairing's answer is the repository.
		repo, err := s.pairShared(ctx, id)
		if err != nil {
			s.refusePairing(ctx, w, id, purpose, credential, err)
			return nil, false
		}
		call.Canonical = repo
		call.Job = JobAccount{Email: id.JobAccount, Slug: id.Slug, Repo: repo}
	default:
		ja, bound := s.jobs[id.JobAccount]
		if !bound || ja.Slug != id.Slug {
			// The refusal is recorded like the ones below it: the identity
			// Verify returned is whole, so the line names the run, the
			// repository the run belongs to, the job and the account — and the
			// route's credential and no other, which for a model call is none
			// (the bundle has not been read) and for a mint is the App key's
			// name.
			s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnverified, ErrorUnverifiedRunIdentity,
				"the run identity is not a run of the calling job's repository")
			return nil, false
		}
		call.Canonical = ja.Repo
		call.Job = ja
	}
	return installationScope{s: s, call: call, called: true}, true
}

// aheadOfTheSharedParse is the routes ServeHTTP answers before the two
// identities are read: the one predicate the resolver needs to know whether a
// call has a caller at all.
//
// It is the list ServeHTTP's own branches dispatch on, and the two have to
// agree — a path this predicate names that ServeHTTP then parses would verify
// nobody (a caller holding no run identity is refused by RequestFromHTTP), and
// a path it does not name that ServeHTTP answers early would be refused for a
// missing identity it never needed. TestInstallationScopeIsTodaysBehaviour
// holds the two to each other by walking the list.
func aheadOfTheSharedParse(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, BitbucketProxyPrefix) {
		// The proxy's BUILD half alone: a run's proxy traffic carries no
		// HeaderBuild and goes through the shared parse like every other run
		// call (B1.6).
		return r.Header.Get(HeaderBuild) != ""
	}
	switch r.URL.Path {
	case VerifyPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, BuildTokenPath, BuildCredentialsPath, IssuePath, SlipPath:
		return true
	}
	return false
}

// installationScope is an installation's per-call scope. It holds the server it
// reads the deployment from and the caller the resolver verified; a scope
// resolved ahead of the shared parse holds no caller at all (called is false).
type installationScope struct {
	s      *Server
	call   Caller
	called bool
}

func (sc installationScope) Caller() (Caller, bool) { return sc.call, sc.called }

// Serves answers true for every path: an installation serves every route of
// this service, and WS1 changes nothing about which ones those are. The scope
// that serves SOME of them is a workspace's (WS2, R14).
func (sc installationScope) Serves(string) bool { return true }

// AcceptsSlip answers true: the slips are this installation's own (DS1), and a
// run of this installation claims one from the service it is calling.
func (sc installationScope) AcceptsSlip() bool { return true }

func (sc installationScope) Policy() PolicyReader { return sc.s.cfg.Policy }

func (sc installationScope) Kills() KillReader { return sc.s.cfg.Kills }

func (sc installationScope) State() creds.Store { return sc.s.cfg.State }

// Outcomes is the installation's write-once outcomes node, derived from the
// database the service was configured with rather than from a field of its own:
// the reader the verifier holds (AuthConfig.Outcomes) is built from that same
// client by the deployment (internal/cli/credentials_serve.go wires one
// *rtdb.Client into both), so a second configuration field would be a second
// answer to one question. A database this process cannot read as a Getter —
// which no deployment of this service is — answers nil, and the scope asks no
// outcome question itself: Outcomes is what the scope hands a caller that
// verifies something (WS2's scope over a member's database).
func (sc installationScope) Outcomes() OutcomeReader {
	if db, ok := sc.s.cfg.DB.(Getter); ok {
		return RTDBOutcomes{DB: db}
	}
	return nil
}

func (sc installationScope) Table() BindingTable { return installationTable{sc.s} }

func (sc installationScope) Offering() Offering { return installationOffering{sc.s} }

func (sc installationScope) Forge() ForgeReads { return installationForge{sc.s} }

// Meter answers nil: an installation's calls are metered by the installation's
// own budget, taken where it always was, and R15's meter is the workspace's.
func (sc installationScope) Meter() *WorkspaceMeter { return nil }

// installationTable is the installation's binding table: the maps NewServer
// built, behind the lookups the pairing and the front door make.
type installationTable struct {
	s *Server
}

func (t installationTable) Job(email string) (JobAccount, bool) {
	ja, ok := t.s.jobs[email]
	return ja, ok
}

func (t installationTable) Review(email string) (ReviewJobAccount, bool) {
	return t.s.reviewAccount(email)
}

func (t installationTable) Shared(email string) (SharedJobAccount, bool) {
	return t.s.sharedAccount(email)
}

func (t installationTable) Repo(slug string) (string, bool) {
	repo, ok := t.s.cfg.Repos[slug]
	return repo, ok
}

// installationOffering is what an installation offers a caller: the bundle, as
// candidates has always read it.
type installationOffering struct {
	s *Server
}

// Offer answers the caller's list out of the bundle. It is candidates' own
// head, moved: b.Credentials() and never b.Entries(), because the bundle holds
// the GitHub App key (C2.1), the upstream Bitbucket token (D506), the read
// consumer (F21), the review-signing keys (D1069) and the provider key (AK6b)
// as well, and none of them is a candidate for a caller's Anthropic calls. A
// cascade that offered one would attach an App private key, the caller's own
// git token or a signing key to an Anthropic call as its credential, and the
// provider key would refuse the whole call (a tier-C candidate is refused by
// checkCandidates rather than skipped) — so the filter is the accessor's rather
// than this loop's.
//
// A review caller's list is its own (reviewOfferings, reviewCandidates) and it
// is final: that class's credentials are decided before the cascade's own
// narrowing, and what it is served is the account's read-only set rather than
// the pool.
func (o installationOffering) Offer(ctx context.Context, c Caller, shared *projectpolicy.Policy, b *Bundle) (Offered, error) {
	if c.Identity.JobClass == ClassReview {
		// The lookup is guarded although the pairing has already refused an
		// account this table does not hold (pairReview): the class is the
		// verifier's answer and the two lists are configured separately, so a
		// caller admitted as a review one whose email this table does not carry
		// is a deployment disagreeing with itself — and a service that panicked
		// on a state it cannot name would be the worse answer to it.
		ra, ok := o.s.reviewAccount(c.Identity.JobAccount)
		if !ok {
			return Offered{}, fmt.Errorf("%w: the calling account is not one of this service's review job accounts", ErrUnavailable)
		}
		out, skipped := reviewCandidates(reviewOfferings(b), ra.Account)
		return Offered{Credentials: out, Strategy: creds.StrategyFailover, NotAdmitted: skipped, Final: true}, nil
	}
	offer, strategy, narrowed, err := poolOffering(shared, b)
	if err != nil {
		// The crafts leave this call nothing at all (poolRefusal): the refusal's
		// own sentence is the answer, and it travels as this error rather than
		// as an empty list, which the selector would answer with a sentence
		// about the bundle.
		return Offered{}, err
	}
	// A node that publishes no crafts narrows nothing, and every list that is
	// not the crafts' is answered with the cascade's own rule (MC7): a list no
	// strategy was written for has always been served by it.
	offered := Offered{Credentials: b.Credentials(), Strategy: creds.StrategyFailover}
	if narrowed {
		offered = Offered{Credentials: offer, Strategy: strategy}
		// The credentials the crafts leave out are ones this call may not be
		// served by, which is what not_admitted means for a caller (the rule a
		// review caller's list is built by). They are reported rather than
		// dropped, so the run's halt message reads the same list either way:
		// what was considered, and why each one was skipped. A fifth reason
		// would be a value this contract's closed set does not carry.
		for _, cred := range b.Credentials() {
			if !named(offer, cred.Name) {
				offered.NotAdmitted = append(offered.NotAdmitted, Tried{Name: cred.Name, Tier: wireTier(cred.Tier), Reason: ReasonNotAdmitted})
			}
		}
	}
	return offered, nil
}

// installationForge is the installation's forge-credential reader: the two
// entries of its own bundle that the forge routes read today (bundle.go's
// BitbucketToken and AppKey).
//
// The repository is not a narrowing here and that is the installation's own
// answer rather than a gap: an installation's bundle is one credential for its
// whole deployment, so every repository it serves is served by the same token
// and the same App key. Which repository a call is for decides nothing until a
// workspace's node says which of its shared entries covers it (WS15, WS16), and
// the NAMED Bitbucket tokens a selection chooses among by status stay the
// proxy's own read (bbtokens.Selector, T8) until WS16 moves the whole of that
// read behind the scope — this reader is what those handlers switch to, not a
// second selection written beside them.
type installationForge struct {
	s *Server
}

func (f installationForge) BitbucketToken(ctx context.Context, _ string) (Entry, bool, error) {
	b, err := f.s.cfg.Bundles.Load(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := b.BitbucketToken()
	return e, ok, nil
}

func (f installationForge) AppKey(ctx context.Context, _ string) (Entry, bool, error) {
	b, err := f.s.cfg.Bundles.Load(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := b.AppKey()
	return e, ok, nil
}

// scopeOfCall is the scope of a call that reached the cascade or one of the
// model path's other readers, and it is the installation's own when the context
// carries none.
//
// The front door always hands one (ServeHTTP's withScope) and no handler can be
// dispatched without it, so the fallback is reached by a direct call inside this
// package — this package's own tests ask the cascade its question without
// running a request — and it is the installation's scope because that is the
// behaviour those calls had before the seam existed. It is stated here rather
// than left to a nil dereference: a scope that answered nothing would turn a
// test's question into a crash.
func scopeOfCall(ctx context.Context, s *Server) Scope {
	if sc, ok := scopeOf(ctx); ok {
		return sc
	}
	return installationScope{s: s}
}
