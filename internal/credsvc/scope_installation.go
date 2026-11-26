package credsvc

import (
	"context"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/identitywire"
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
// Nothing here decides anything new. The two identities and the pairing are the
// reads ServeHTTP made before it dispatched anything, moved into the resolver
// because that is where the reads that come before a route belong (C1: the
// resolver owns them and answers the refusal itself), and the readers below are
// the Config fields the model path used to read directly.
type installationResolver struct {
	s *Server
}

// Resolve answers the scope for one call of an installation's own service: the
// verified caller and the deployment's readers.
//
// A route answered BEFORE the shared parse is resolved with no identity read at
// all, and that is not an optimisation: those routes' callers hold no run
// identity — a verifier, a launcher, a build, the orchestrator daemon — so
// RequestFromHTTP would refuse the call for the missing headers before its own
// class ever read anything (server.go's own account of each). What the scope
// answers for them is the uncalled one: Serves is still asked, so a deployment
// that does not serve such a route refuses it before it reaches a handler, and
// the refusal is the service's own and writes no audit line (Caller, C1).
//
// The three answers C1 fixes are the three below: a scope, a callerRefusal
// where the caller could not be admitted, and — for a scope that could not be
// built at all — a *ScopeFault, which nothing in this resolver answers because
// an installation's readers are fields of a configuration that was already
// validated when the process started (NewServer).
func (res installationResolver) Resolve(ctx context.Context, r *http.Request) (Scope, error) {
	s := res.s
	if aheadOfTheSharedParse(r) {
		return installationScope{s: s}, nil
	}
	req, err := RequestFromHTTP(r)
	if err != nil {
		// The contract's own reading of the headers, never this server's
		// (review finding m7). Its errors name a header and never a value.
		return nil, refuseCaller(err.Error(), func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
			s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
		})
	}
	id, err := s.cfg.Auth.Verify(ctx, req)
	if err != nil {
		// refuseIdentity is the one answer a Verify refusal has, and it does the
		// work a refusal can require (an ended run's placement and slip) — which
		// is why the refusal travels as a closure and not as a status and a
		// sentence: what the front door writes is that function, unchanged.
		return nil, refuseCaller(err.Error(), func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			s.refuseIdentity(ctx, w, r, err)
		})
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
	switch id.JobClass {
	case ClassReview:
		if _, err := s.pairReview(ctx, id); err != nil {
			return nil, refuseCaller(err.Error(), func(ctx context.Context, w http.ResponseWriter, _ *http.Request) {
				s.refusePairing(ctx, w, id, purpose, credential, err)
			})
		}
	case ClassShared:
		// The shared class's pairing (G21, F40): the repository is the run
		// identity's own slug and the job's row says which profile's
		// repositories that may be, so the comparison the per-repository class
		// makes against its one repository is made here against the profile's
		// list.
		if _, err := s.pairShared(ctx, id); err != nil {
			return nil, refuseCaller(err.Error(), func(ctx context.Context, w http.ResponseWriter, _ *http.Request) {
				s.refusePairing(ctx, w, id, purpose, credential, err)
			})
		}
	default:
		ja, bound := s.jobs[id.JobAccount]
		if !bound || ja.Slug != id.Slug {
			// The refusal is recorded like the ones below it: the identity
			// Verify returned is whole, so the line names the run, the
			// repository the run belongs to, the job and the account — and the
			// route's credential and no other, which for a model call is none
			// (the bundle has not been read) and for a mint is the App key's
			// name.
			const message = "the run identity is not a run of the calling job's repository"
			return nil, refuseCaller(message, func(ctx context.Context, w http.ResponseWriter, _ *http.Request) {
				s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnverified, ErrorUnverifiedRunIdentity, message)
			})
		}
	}
	return installationScope{s: s, id: id}, nil
}

// callerRefusal is the resolver's answer when the CALLER could not be admitted,
// and it is a channel of its own beside C1's *ScopeFault: a fault is the
// SCOPE's failure and this is a verdict about the caller, which the front door
// answers exactly as it answered the read before the resolver owned it.
//
// **Why the answer and not the facts of it.** C1 gives Resolve no
// http.ResponseWriter — the seam is a value and not a branch, and a resolver
// that wrote a refusal would be a second place this service's answers are
// spelled — so the refusal travels as the one thing that cannot drift from what
// it was: the call itself. A status, a type and a sentence carried as fields
// would leave the cleanup an ended run's refusal performs (refuseIdentity), the
// status a pairing refusal chooses from its error (refusePairing) and the
// purpose and credential the audit line is written with to be re-derived in the
// gate, which is exactly how two spellings of one rule appear. The message is
// kept beside it because an error has to say something: it is the sentence the
// refusal carries, and the gate logs it if it ever meets one it cannot write.
type callerRefusal struct {
	message string
	answer  func(ctx context.Context, w http.ResponseWriter, r *http.Request)
}

func (e *callerRefusal) Error() string { return e.message }

// refuseCaller is a resolver's refusal of the caller, with the answer the front
// door writes for it.
func refuseCaller(message string, answer func(ctx context.Context, w http.ResponseWriter, r *http.Request)) error {
	return &callerRefusal{message: message, answer: answer}
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
	case VerifyPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, BuildTokenPath, BuildCredentialsPath, IssuePath, SlipPath, PoolDesignatePath:
		return true
	}
	if _, isIdentity := identitywire.RouteFor(r.URL.Path); isIdentity {
		// The five identity routes (C1, R2): the slip's grant reason and not a
		// new one — a ticket is asked for before the run exists and a claim is
		// the run's own first call, so their caller holds no run identity
		// either, and each route reads its own class and its own body.
		return true
	}
	return false
}

// installationScope is an installation's per-call scope. It holds the server it
// reads the deployment from and the caller the resolver verified; a scope
// resolved ahead of the shared parse holds the zero Identity, which is C1's own
// spelling of a route that carries no caller (Caller).
type installationScope struct {
	s  *Server
	id Identity
}

// Caller answers the run identity the call's token proved, and the zero
// Identity on a route resolved ahead of the shared parse.
func (sc installationScope) Caller() Identity { return sc.id }

// Serves answers true for every path: an installation serves every route of
// this service, and WS1 changes nothing about which ones those are. The scope
// that serves SOME of them is a workspace's (WS2, R14).
func (sc installationScope) Serves(string) bool { return true }

// AcceptsSlip answers true: the slips are this installation's own (DS1), and a
// run of this installation claims one from the service it is calling.
func (sc installationScope) AcceptsSlip() bool { return true }

// Repos answers the installation's own repository table: every repository it
// serves, as the deployment derived it from its job accounts
// (RepositoryTableFor). The call's slug is resolved through it and never
// guessed from its own text (D1066).
//
// The configuration's table answers it wherever the deployer set one, which
// every deployment of this service does: it is the map the read mints, the
// label and issue routes and the review and shared pairings already read. Where
// it set none, the table is derived from the job rows it did give — and that is
// not a second spelling of the deployer's rule, because Repos IS
// RepositoryTableFor over the same job rows JobAccountsFor produced the
// configured accounts from, so the two agree by construction. What the
// derivation buys is that a service built without the field still resolves the
// repositories its own jobs are bound to, which is what the pairing answered
// for this class before C1 gave the table its name. An installation whose
// deployment carries an EMPTY table keeps it: empty says this installation
// serves no repository, which is a different answer from "none was given" and
// one this accessor must not overrule.
func (sc installationScope) Repos() map[string]string { return installationRepos(sc.s) }

// installationRepos is that table, derived where the deployer set none, and it
// is a function rather than a method body because the scope answers it twice:
// this accessor answers the call's slug through it and the forge reader answers
// the coverage from it (installationForge.Covers), and the two have to be the
// same table. A coverage read off the CONFIGURED field alone would refuse a
// repository the same deployment had just resolved and admitted — an
// installation whose configuration carries no table resolves its repositories
// from its job rows, and a coverage that did not follow would refuse every call
// of such a deployment (WS16's gate is the first thing that asks).
func installationRepos(s *Server) map[string]string {
	if s.cfg.Repos != nil {
		return s.cfg.Repos
	}
	out := make(map[string]string, len(s.cfg.JobAccounts))
	for _, ja := range s.cfg.JobAccounts {
		out[ja.Slug] = ja.Repo
	}
	return out
}

func (sc installationScope) Policy() PolicyReader { return sc.s.cfg.Policy }

func (sc installationScope) Kills() KillReader { return sc.s.cfg.Kills }

// State is the installation's own cascade state, and the whole of it: the
// configuration's store is a creds.Store, which StateStore (creds.CascadeState)
// and RepoAuthReader both take, so the installation answers the split's two
// readers with the one value it has always held.
func (sc installationScope) State() StateStore { return sc.s.cfg.State }

// RepoAuth answers the record one repository's own fugaro.yaml wrote out of the
// installation's database, which is where its own `fugaro update` wrote every
// record. It is the caller's reader and never the workspace's (C1's rule 2):
// a workspace answers it from the MEMBER's database, because that is whose
// update wrote it.
func (sc installationScope) RepoAuth() RepoAuthReader { return sc.s.cfg.State }

// Outcomes is the installation's write-once outcomes node, derived from the
// database the service was configured with rather than from a field of its own:
// the reader the verifier holds (AuthConfig.Outcomes) is built from that same
// client by the deployment (internal/cli/credentials_serve.go wires one
// *rtdb.Client into both), so a second configuration field would be a second
// answer to one question. A database this process cannot read as a Getter —
// which no deployment of this service is — answers nil.
func (sc installationScope) Outcomes() OutcomeReader {
	if db, ok := sc.s.cfg.DB.(Getter); ok {
		return RTDBOutcomes{DB: db}
	}
	return nil
}

// Offering answers the credentials this installation may serve a call out of:
// the bundle's own, which the crafts and the admissions then narrow on the model
// path (candidates, poolOffering). The narrowing is deliberately not here: C1
// fixes the reader as the POOL a call may be served from, before any credential
// is chosen, and an installation's pool is its bundle — the intersection over a
// workspace's own pool is WS2's answer and is the only one the two deployments
// differ in.
func (sc installationScope) Offering() OfferingReader { return installationOffering{} }

// Forge is the installation's forge reader: its own token store, and the
// coverage its table answers. The forge handlers still read the bundle and the
// configuration directly and switch to this reader in WS16 and WS17 — WS1
// declares the reader and changes nothing about the routes.
func (sc installationScope) Forge() ForgeReader { return installationForge{sc.s} }

// Meter answers nil: an installation's calls are metered by the installation's
// own budget, taken where it always was, and R15's meter is the workspace's.
// nil is the one accessor's zero answer that is not a refusal (C1, D1301 as
// amended): it means "this deployment has no workspace cap", which is what an
// installation is.
func (sc installationScope) Meter() *WorkspaceMeter { return nil }

// installationOffering is what an installation offers a caller: the bundle's
// own credentials, and nothing else.
//
// It is b.Credentials() and never b.Entries(): the bundle holds the GitHub App
// key (C2.1), the upstream Bitbucket token (D506), the read consumer (F21), the
// review-signing keys (D1069) and the provider key (AK6b) as well, and none of
// them is a candidate for a caller's Anthropic calls. A cascade that offered
// one would attach an App private key, the caller's own git token or a signing
// key to an Anthropic call as its credential, and the provider key would refuse
// the whole call (a tier-C candidate is refused by checkCandidates rather than
// skipped) — so the filter is the accessor's rather than this reader's.
//
// The bundle is the one the ROUTE loaded, handed in and never held here (C1's
// OfferingReader): a reader that loaded its own would be a second read of the
// one secret this service holds, and the values a call is finally served are
// the route's to read.
type installationOffering struct{}

func (installationOffering) Offering(_ context.Context, b *Bundle) ([]creds.Credential, error) {
	return b.Credentials(), nil
}

// installationForge is the installation's forge reader: the token store its
// configuration wired, and the coverage its own table answers.
type installationForge struct {
	s *Server
}

// Bitbucket answers the installation's own token store, and the legacy selector
// EXPLICITLY where the configuration wired none (C1's rule 10): an installation
// that stored only the bundle's legacy entry is served exactly as it was before
// a store existed, and nil stays what a workspace's scope answers to mean
// "refuse", so the proxy's own fallback (chooseBitbucketToken) is never the
// thing that decides which token a call is served with.
func (f installationForge) Bitbucket() bbtokens.Selector {
	if f.s.cfg.Selector != nil {
		return f.s.cfg.Selector
	}
	return legacySelector{}
}

// Covers reports whether this call's forge traffic may reach the canonical
// repository at all: an installation covers every repository its table holds,
// which is every repository it serves.
//
// It reads the table through installationRepos, which is the very table this
// scope's Repos accessor answers and not the configuration field beside it: a
// deployment that set no table resolves its repositories from the job rows it
// did give, and a coverage that read the field alone would refuse every one of
// them — including the call whose canonical repository the pairing had just
// resolved out of that same table. The two answers agree by construction (WS16).
func (f installationForge) Covers(_ context.Context, canonical string) (bool, error) {
	for _, held := range installationRepos(f.s) {
		if held == canonical {
			return true, nil
		}
	}
	return false, nil
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

// scopeCtxKey is the context key the scope travels under. It is unexported and
// of its own type so that no other package can put a value under it and no
// handler can be handed another package's scope.
type scopeCtxKey struct{}

// withScope hands the scope to every handler dispatched under it, which is what
// lets a handler read its caller and its readers without a signature change:
// the routes of this service are dispatched by path, and a handler that took a
// scope argument would be a handler whose caller had to know which deployment
// it was serving.
//
// It is written in the installation's own file because the FRONT DOOR is
// WS1's: the gate that resolves a scope and hands it down is one code path for
// both deployments (server.go), and what a workspace's deployment differs in is
// the resolver it is built with.
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
