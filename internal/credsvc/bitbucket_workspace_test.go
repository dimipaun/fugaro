package credsvc

// WS16: the Bitbucket git proxy on the scope, served as a WORKSPACE serves it.
//
// The route's own tests (bitbucketproxy_test.go and the three files beside it)
// run an INSTALLATION and are deliberately not touched by this task: what they
// pin is unchanged, and "an installation's behaviour is unchanged" is the claim
// that they still pass. What is asked here is the other deployment — the same
// route under a workspace's scope (C1, R1) — and the fences a call under one has
// to pass: the workspace owner's COVERAGE of the repository, the MEMBER's own
// published policy, and the calling job account's own repository table. Each
// test below leaves one of them unsatisfied:
//
//   - a repository outside the owner's coverage is refused (the red step: before
//     WS16 the route read the deployment's own table, asked no coverage at all,
//     and served it);
//   - a repository the MEMBER's policy admits and the owner never declared is
//     refused — the coverage is the owner's list and never the member's (C3.6);
//   - a repository the owner declares for ANOTHER job account is refused even
//     though the coverage names it, because the table the call is resolved and
//     bound through is the CALLER's (D2992, G3);
//   - a coverage nobody could read is a refusal and never a repository nothing
//     covers (C1's ForgeReader: an error is a refusal);
//   - and an admitted call is relayed as the installation's is: streamed in both
//     directions, with nothing held (B1.3, G2).
//
// The deployment is a TEST DOUBLE and not WS2's resolver: the scope here is
// C1's shape with the readers this route asks answered by the test, and a
// resolver that answers the scope registered for the run token a call
// presents. It verifies nothing, and it does not pretend to — what the seam
// means for this route is WHICH store, WHICH coverage and WHICH table a call is
// served out of, and that is what a double can pin. WS2's resolver is what a
// deployment holds and its tests ask the real readers; what this double answers
// is the route's own half.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitproxy"
)

// The workspace these tests serve: three repositories of ONE Bitbucket
// workspace, so that a canonical name is never the reason a call is refused. The
// difference between them is only ever which fence names them.
const (
	// wsMemberRepo is the repository the member's run is most often a run of: the
	// owner's credential covers it and the member's own policy admits it.
	wsMemberRepo = testWorkspace + "/ws-member"
	// wsMemberOtherRepo is the same member's second repository: the member's own
	// policy admits it and the owner's coverage does not name it.
	wsMemberOtherRepo = testWorkspace + "/ws-member-two"
	// wsOtherMemberRepo is another member's repository, which the owner's
	// coverage DOES name — one credential covers the workspace's repositories —
	// and which the calling job account's table does not hold.
	wsOtherMemberRepo = testWorkspace + "/ws-theirs"
)

// wsTokenName is the workspace's own Bitbucket token, by the name its bundle key
// spells (config.BitbucketTokenKey): the credential the workspace owner stores
// and the scope's forge reader answers. It is a NAME and not a value — the value
// is the bundle's and is minted by the fixture (fakeCred), so no test here writes
// one down.
const wsTokenName = "workspace-a"

// wsFetchAnswer is what the fake bitbucket.org answers an advertisement with: the
// bytes git expects, so that a served call's answer is recognisable.
const wsFetchAnswer = "001e# service=git-upload-pack\n0000"

// ---------------------------------------------------------------------------
// The workspace's scope, as C1 declares it
// ---------------------------------------------------------------------------

// wsForge is the workspace's forge reader (ForgeReader, C1): the token store the
// workspace's own bundle is served from, and the coverage the workspace owner's
// declaration names.
//
// It is deliberately neither the installation's reader nor the route's own
// configuration. A store that answers a NAMED token (wsTokenName) is one the
// route can only have taken from here: the installation's fallback answers the
// bundle's legacy entry and nothing else, so a route still reading its own
// configuration would refuse this bundle rather than serve a call out of it.
type wsForge struct {
	store  bbtokens.Selector
	covers []string
	// err is what the coverage read fails with, for the test that asks what a
	// coverage nobody could read does (C1: an error is a refusal).
	err error
}

func (f wsForge) Bitbucket() bbtokens.Selector { return f.store }

func (f wsForge) Covers(_ context.Context, canonical string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for _, held := range f.covers {
		if held == canonical {
			return true, nil
		}
	}
	return false, nil
}

// wsTokenStore is the workspace's own token store as this route reads it: one
// token, the workspace's, for every request, and the record of what it was asked.
//
// It is a stand-in and not the real store: that one's ranking, its marks and its
// project lookup are T8's and T9's tests, and the chain end to end is
// bitbucketproxy_tokens_e2e_test.go's. What this file asks is WHICH store the
// route asks at all.
type wsTokenStore struct {
	name bbtokens.Name
	mu   sync.Mutex
	asks []wsTokenAsk
}

// wsTokenAsk is one selection: the canonical repository it was made for and
// whether the call was a write (bbtokens.Selector's own two arguments).
type wsTokenAsk struct {
	canonical string
	write     bool
}

func (st *wsTokenStore) Select(_ context.Context, _ bbtokens.Snapshot, canonical string, write bool) (bbtokens.Choice, error) {
	st.mu.Lock()
	st.asks = append(st.asks, wsTokenAsk{canonical: canonical, write: write})
	st.mu.Unlock()
	return bbtokens.Choice{Name: st.name}, nil
}

// asked is what the store was asked, in order.
func (st *wsTokenStore) asked() []wsTokenAsk {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]wsTokenAsk(nil), st.asks...)
}

// wsProxyScope is a workspace's scope, cut down to what the proxy route asks of one:
// the caller, the member's policy, the switches, the calling job account's
// repository table and the forge reader.
//
// The readers this route never asks are here because Scope declares them, and
// they answer nil: a route of this deployment that reached for the workspace's
// own state, or for the member's auth record, would fail here rather than read a
// test's stand-in and pass — the honest answer, because which database each of
// those is read from is WS2's decision and not this file's.
type wsProxyScope struct {
	caller Identity
	table  map[string]string
	policy PolicyReader
	kills  KillReader
	forge  ForgeReader
}

func (sc wsProxyScope) Caller() Identity { return sc.caller }

func (sc wsProxyScope) Policy() PolicyReader { return sc.policy }

func (sc wsProxyScope) Kills() KillReader { return sc.kills }

func (sc wsProxyScope) State() StateStore { return nil }

func (sc wsProxyScope) RepoAuth() RepoAuthReader { return nil }

func (sc wsProxyScope) Outcomes() OutcomeReader { return nil }

// Repos is the CALLING job account's own table (C1, D2992): the repositories the
// workspace owner declared for the account the call was verified from, and never
// the workspace's whole coverage.
func (sc wsProxyScope) Repos() map[string]string { return sc.table }

func (sc wsProxyScope) Offering() OfferingReader { return nil }

func (sc wsProxyScope) Forge() ForgeReader { return sc.forge }

// Meter answers nil: this deployment declares no workspace cap, and the git
// proxy pays for nothing either way — no model call is made on this route.
func (sc wsProxyScope) Meter() *WorkspaceMeter { return nil }

// Serves is the workspace's own route set (R14, D2988), as the spec fixes it: the
// model routes; the forge routes a run calls — the git proxy under this file's
// prefix, a run's own token mint and its context tokens; and the keep-warm route.
// Every other route is refused: the read mints, the slips, the workflow secrets,
// the build tokens, the review signing and verification, and the merge, label and
// issue routes stay the installation's, and a member's run keeps calling its own
// project's service for them.
func (sc wsProxyScope) Serves(path string) bool {
	if strings.HasPrefix(path, BitbucketProxyPrefix) {
		return true
	}
	switch path {
	case MessagesPath, CountTokensPath, MintPath, ContextTokenPath, WorkspaceKeepWarmPath:
		return true
	}
	return false
}

// AcceptsSlip answers false: a call to the workspace's service carries the job's
// identity and the run's token and nothing else of the member's (R18).
func (sc wsProxyScope) AcceptsSlip() bool { return false }

// wsResolver is the deployment's resolver as a workspace's answers one, cut down
// to what this file needs.
//
// It verifies nothing: the verification is WS2's and WS11's, and no test here
// asks about it. What it answers is the scope REGISTERED for the run token the
// call presents, and a run token nothing registered is refused rather than
// served under another caller's scope — the one property the double must have,
// standing where the service's own verification stands.
type wsResolver struct {
	scopes map[string]wsProxyScope
}

func (r wsResolver) Resolve(_ context.Context, req *http.Request) (Scope, error) {
	sc, ok := r.scopes[req.Header.Get(HeaderRunIdentity)]
	if !ok {
		return nil, refuseCaller("this test registered no identity for the run token this call presents",
			func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(StatusUnverified)
			})
	}
	return sc, nil
}

// ---------------------------------------------------------------------------
// The workspace under test
// ---------------------------------------------------------------------------

// wsConfig is what one test's workspace differs in. Every list is a set of
// repositories in the canonical names the route speaks, and each is a fence of
// its own: accountRepos is the calling job account's table (C1's Repos), covered
// is the workspace owner's declaration for the credential (C3's covers), and
// admitted is what the MEMBER's own published policy admits (the allow-list the
// rules decide against, gitproxy.Decide).
type wsConfig struct {
	// repo is the repository the member's run is a run of: the canonical
	// repository a call is served FOR, resolved out of accountRepos by the run
	// identity's own slug.
	repo string
	// accountRepos are the repositories the calling job account is bound to,
	// which is the table the scope answers.
	accountRepos []string
	// covered is the workspace owner's coverage for the credential that account
	// is served from.
	covered []string
	// admitted is what the member's own policy admits, with the proxy switch on.
	admitted []string
	// coverErr, when set, is what the coverage read fails with.
	coverErr error
	// noStore is the workspace's forge reader answering NO token store at all: a
	// nil selector, which the route refuses rather than escapes into a fallback
	// (D143, D2355).
	noStore bool
	// legacyBundle writes the deployment's legacy `bitbucket-token` entry into
	// the workspace's bundle beside the named one, which is what a test of that
	// refusal needs: a route that restored the legacy fallback would be served
	// the legacy value and answer the call, so the bundle has to hold one for
	// the refusal to be this route's own doing rather than the bundle's.
	legacyBundle bool
}

// wsWorld is a workspace-mode service, the fake bitbucket.org its route forwards
// to, and what the workspace's own bundle holds.
type wsWorld struct {
	t      *testing.T
	s      *svc
	up     *proxyUpstream
	store  *wsTokenStore
	base   wsProxyScope
	scopes map[string]wsProxyScope
	// token is the workspace bundle's own value for wsTokenName, so that a test
	// can hold what bitbucket.org was shown to the bundle without either being
	// written down.
	token string
}

// newWSWorld builds the workspace: a service whose resolver is this file's, whose
// scope answers the configuration, whose bundle holds the workspace's own named
// token, and whose bitbucket.org is the fixture's fake.
//
// Config.Repos is deliberately left nil. The installation's own table has no
// bearing on a call served under a workspace's scope, and a route that still
// resolved its repositories through the configuration would fail every test that
// follows rather than one of them.
func newWSWorld(t *testing.T, cfg wsConfig, mutate ...func(*svc, *Config, *wsProxyScope)) *wsWorld {
	t.Helper()
	up := newProxyUpstream(t)
	doc, values := proxyTokenBundle(t, cfg.legacyBundle, wsTokenName)
	store := &wsTokenStore{name: bbtokens.Name(config.BitbucketTokenKey(wsTokenName))}
	w := &wsWorld{
		t:      t,
		up:     up,
		store:  store,
		scopes: map[string]wsProxyScope{},
		token:  values[config.BitbucketTokenKey(wsTokenName)],
	}
	table := map[string]string{}
	for _, repo := range cfg.accountRepos {
		table[bitbucketSlug(t, repo)] = testCanonical(t, repo)
	}
	covered := make([]string, 0, len(cfg.covered))
	for _, repo := range cfg.covered {
		covered = append(covered, testCanonical(t, repo))
	}
	sc := &wsProxyScope{
		table: table,
		policy: &fakePolicy{
			shared:      decodeTestPolicy(t, wsMemberPolicyDoc(t, cfg.admitted...)),
			sharedFound: true,
		},
		kills: &fakeKills{repos: map[string]bool{}},
		forge: wsForge{store: store, covers: covered, err: cfg.coverErr},
	}
	if cfg.noStore {
		sc.forge = wsForge{covers: covered, err: cfg.coverErr}
	}
	s := newSvcWith(t, func(s *svc, c *Config) {
		// The run identity the scope answers for a run of the repository the
		// test named: the claims are the fixture's own (its run id, its job name,
		// the branch a run pushes), and the repository is a fact about the CALLER
		// and never about the request (G3).
		sc.caller = Identity{
			Slug: bitbucketSlug(t, cfg.repo), Run: s.run, Project: testProject,
			FX: testNow().Add(2 * time.Hour), Launcher: "operator@example.invalid",
			Branch: "fugaro/" + s.run, Job: s.job, JobAccount: testJobSA,
			JobClass: ClassRepository,
		}
		c.Resolver = wsResolver{scopes: w.scopes}
		c.Client = up.client()
		s.src.docs = [][]byte{doc}
		for _, m := range mutate {
			m(s, c, sc)
		}
		w.base = *sc
	})
	w.s = s
	return w
}

// callAs sends one call of the route as a run of `repo`, which has to be one of
// the repositories the calling job account's table holds: the identity the
// workspace's scope resolves for it.
//
// A call names the repository it is FOR through its run identity and never
// through its path (G3), so the two arguments of this helper are the test's whole
// statement about a call: `repo` is the run's own repository, and `path` is the
// git path the caller asked for — the ones the tests below differ in.
func (w *wsWorld) callAs(t *testing.T, method, path string, body []byte, repo string) (*http.Response, []byte) {
	t.Helper()
	tok := w.runTokenOf(t, repo)
	return w.s.proxyCallAs(t, method, path, body, tok, w.s.jobToken(), w.s.job)
}

// runTokenOf is the fixture's run token for a run of `repo`, registered with the
// resolver so that the scope answers the caller it is: the claims are the
// fixture's and the slug claim (fs) is the repository's own, which is the identity
// a run token carries and the fact the pairing resolves the call's canonical
// repository through.
func (w *wsWorld) runTokenOf(t *testing.T, repo string) string {
	t.Helper()
	slug := bitbucketSlug(t, repo)
	tok := w.s.runToken(append(proxyClaims(w.s.proxyBranch()), func(c map[string]any) {
		c["fs"] = slug
		c["sub"] = "r~" + slug + "~" + w.s.run
		c["user_id"] = "r~" + slug + "~" + w.s.run
	})...)
	if _, ok := w.scopes[tok]; !ok {
		sc := w.base
		sc.caller.Slug = slug
		w.scopes[tok] = sc
	}
	return tok
}

// wsGitPath is one git path of a repository, as a caller spells it: the
// repository and the action, and no query.
func wsGitPath(repo, action string) string { return repo + ".git/" + action }

// wsMemberPolicyDoc is the MEMBER's own published policy as the workspace's scope
// answers it (C1: a workspace's Policy is the member's, read from the member's
// database): the block that admits each repository named, with the proxy switch
// on — which is what the rules decide a proxied call against (gitproxy.Decide,
// G9).
func wsMemberPolicyDoc(t *testing.T, repos ...string) string {
	t.Helper()
	quoted := make([]string, 0, len(repos))
	for _, repo := range repos {
		quoted = append(quoted, `"`+testCanonical(t, repo)+`"`)
	}
	list := `[` + strings.Join(quoted, ", ") + `]`
	return `{
		"version": 9,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ` + list + `,
		"accounts": {"default": {"allow_repos": ` + list + `}},
		"credentials": {"fallback": "auto"},
		"bitbucket_token": {"allow_repos": ` + list + `, "proxy": true}
	}`
}

// wsFetchPath is the one call the admitted tests make: the advertisement a git
// fetch opens with, which the rules allow for the run's own repository and which
// the route forwards to bitbucket.org.
func wsFetchPath(repo string) string {
	return proxyPath(wsGitPath(repo, "info/refs")) + "?service=git-upload-pack"
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestWorkspaceProxyServesOnlyACoveredRepository is this task's red step. The
// member's job account is bound to two of its own repositories and the member's
// own policy admits both, so the two calls below pass every fence the route had
// before this task — the binding, the block and the allow-list — and differ in
// one thing only: the workspace owner's credential covers one of them. Before
// WS16 the route read the deployment's own table, asked no coverage at all, and
// served the other one.
func TestWorkspaceProxyServesOnlyACoveredRepository(t *testing.T) {
	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo, wsMemberOtherRepo},
		covered:      []string{wsMemberRepo},
		admitted:     []string{wsMemberRepo, wsMemberOtherRepo},
	})
	w.up.answerWith(http.StatusOK, wsFetchAnswer)

	// The covered repository is served, and with the WORKSPACE's own token: the
	// named entry a store that answered the legacy fallback could not have
	// chosen, so the credential the call went out with is the scope's answer and
	// not a fallback of the route's.
	resp, ans := w.callAs(t, http.MethodGet, wsFetchPath(wsMemberRepo), nil, wsMemberRepo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch of the covered repository answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	if string(ans) != wsFetchAnswer {
		t.Errorf("the answer = %q, want the upstream's own bytes relayed unchanged (%q)", ans, wsFetchAnswer)
	}
	got := w.up.only(t)
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(bitbucket.GitUsername+":"+w.token))
	if got.auth != wantAuth {
		t.Errorf("bitbucket.org was shown %q, want the workspace bundle's own token as git's x-token-auth Basic", got.auth)
	}
	if asks := w.store.asked(); len(asks) != 1 || asks[0].canonical != testCanonical(t, wsMemberRepo) || asks[0].write {
		t.Errorf("the workspace's store was asked %+v, want one selection for the covered repository as a read", asks)
	}
	line := w.s.onlyAuditLine(t)
	if line["status"] != StatusOK || auditField(line, "credential") != config.BitbucketTokenKey(wsTokenName) {
		t.Errorf("the audit line = %v, want a served call recorded with the workspace token's document key", line)
	}

	// The repository the owner's credential does not cover is refused, and
	// BEFORE bitbucket.org is reached: the coverage is a fact about the
	// deployment, so no byte of the call goes out and no pack comes back.
	resp, ans = w.callAs(t, http.MethodGet, wsFetchPath(wsMemberOtherRepo), nil, wsMemberOtherRepo)
	proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, refusalNotDeclared)
	if !strings.Contains(string(ans), "fugaro update") {
		t.Errorf("the refusal = %q, and R19 wants it to name the workspace owner's own command", ans)
	}
	if n := w.up.count(); n != 1 {
		t.Fatalf("bitbucket.org saw %d calls, want the covered fetch alone: %+v", n, w.up.calls())
	}
	if asks := w.store.asked(); len(asks) != 1 {
		t.Errorf("the workspace's store was asked %d times, want the covered fetch alone: no credential is chosen for a repository nothing covers", len(asks))
	}
	lines := w.s.auditLines(t)
	if len(lines) != 2 {
		t.Fatalf("the two calls wrote %d audit lines, want one each", len(lines))
	}
	refused := lines[1]
	if refused["status"] != ErrorRequestRefused || refused["run"] != w.s.run ||
		refused["repository"] != bitbucketSlug(t, wsMemberOtherRepo) ||
		auditField(refused, "purpose") != PurposeGit {
		t.Errorf("the refusal's audit line = %v, want the refused run of the repository the call was served for, under git's own purpose", refused)
	}
	if !strings.Contains(w.s.logs.String(), "class="+refusalNotDeclared) {
		t.Errorf("the operator's line does not name the class %q: %s", refusalNotDeclared, w.s.logs.String())
	}
}

// TestARepositoryNamedOnlyByTheMemberIsRefused: the coverage is the WORKSPACE
// OWNER's list and never the member's (C3.6, R5). The member's own policy names
// its own repository and admits it with the proxy switch on, so every fence the
// route had before this task passes and the rules would allow the call — and it
// is refused anyway, because the workspace holds no credential the owner declared
// for it.
func TestARepositoryNamedOnlyByTheMemberIsRefused(t *testing.T) {
	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo},
		// The member's own published policy: the repository, admitted, proxied.
		admitted: []string{wsMemberRepo},
		// The owner's declaration, which names nothing at all: the workspace
		// holds its own credential and has not declared this repository for it.
		covered: nil,
	})
	w.up.answerWith(http.StatusOK, wsFetchAnswer)

	resp, ans := w.callAs(t, http.MethodGet, wsFetchPath(wsMemberRepo), nil, wsMemberRepo)
	// The class is the coverage's own and not the rules': not_admitted and
	// proxy_off are the member's two fences and both are satisfied here, so a
	// refusal under either of them would mean this fence was never asked.
	proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, refusalNotDeclared)
	if n := w.up.count(); n != 0 {
		t.Fatalf("bitbucket.org saw %d calls, want none: a repository the owner declared nothing for is served by nothing", n)
	}
	if asks := w.store.asked(); len(asks) != 0 {
		t.Errorf("the workspace's store was asked %+v, want nothing: no token is chosen for a repository nothing covers", asks)
	}
	line := w.s.onlyAuditLine(t)
	if line["status"] != ErrorRequestRefused || auditField(line, "credential") != config.BitbucketSecret {
		t.Errorf("the audit line = %v, want the refusal recorded under the credential a line names before any token is chosen", line)
	}
}

// TestAnotherMembersRepositoryIsRefused: one member's job never reaches a
// repository covered for another, and the two calls below ask it the two ways a
// call can. The owner's coverage names BOTH members' repositories — one
// credential covers the workspace's repositories — and the member's own policy
// admits both, which is what makes the coverage and the policy alone unable to
// answer this: what refuses both calls is the CALLING JOB ACCOUNT's own table,
// read by the pairing before the route is reached and applied by the rules'
// binding inside it (D2992, G3).
func TestAnotherMembersRepositoryIsRefused(t *testing.T) {
	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo},
		covered:      []string{wsMemberRepo, wsOtherMemberRepo},
		admitted:     []string{wsMemberRepo, wsOtherMemberRepo},
	})
	w.up.answerWith(http.StatusOK, wsFetchAnswer)

	// A run identity of the other member's repository: the repository is not one
	// the calling account's table holds, so the call is refused where every run
	// call is bound to its job account and never reaches the route.
	resp, ans := w.callAs(t, http.MethodGet, wsFetchPath(wsOtherMemberRepo), nil, wsOtherMemberRepo)
	proxyRefused(t, resp, ans, StatusUnverified, ErrorUnverifiedRunIdentity,
		"the run identity is not a run of the calling job's repository")

	// The calling account's own run, asking for the other member's repository by
	// its PATH: the pairing resolves the caller's own repository, the rules bind
	// the request to it, and the repository the call names — covered by the
	// workspace's credential and admitted by the member's policy — is refused
	// because it is not the repository this run is served for.
	resp, ans = w.callAs(t, http.MethodGet, wsFetchPath(wsOtherMemberRepo), nil, wsMemberRepo)
	proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, gitproxy.ReasonOtherRepository)
	if !strings.Contains(string(ans), testCanonical(t, wsMemberRepo)) {
		t.Errorf("the refusal = %q, and a refusal of another repository names the one this run reaches", ans)
	}
	if n := w.up.count(); n != 0 {
		t.Fatalf("bitbucket.org saw %d calls, want none: neither call may reach the other member's repository", n)
	}
	lines := w.s.auditLines(t)
	if len(lines) != 2 {
		t.Fatalf("the two calls wrote %d audit lines, want one each", len(lines))
	}
	// The line the ROUTE wrote records the calling account's own repository and
	// not the one the request named: an audit line records the act, and the
	// other member's repository is not what this run is (S2's rule for this
	// route, and the reason the sentence is built apart from the line).
	//
	// The first line is the pairing's, written before any route: what it records
	// is the repository the run identity CLAIMED and that the calling account's
	// table does not hold, which is the claim the refusal is about. That is the
	// pairing's own rule and not this route's, and nothing here changes it.
	if lines[0]["status"] != ErrorUnverifiedRunIdentity || lines[0]["run"] != w.s.run {
		t.Errorf("the pairing's line = %v, want the refused run's own unverified identity", lines[0])
	}
	if lines[1]["repository"] != bitbucketSlug(t, wsMemberRepo) {
		t.Errorf("the route's line = %v, want the calling account's own repository", lines[1])
	}
	if strings.Contains(w.s.logs.String(), testCanonical(t, wsOtherMemberRepo)) {
		t.Errorf("the operator's own lines carry the other member's repository: %s", w.s.logs.String())
	}
}

// TestWorkspaceProxyRefusesACoverageItCannotRead: an error is a refusal and never
// a repository nothing covers (C1's ForgeReader). A deployment that cannot read
// what its credential covers serves no call out of it: the alternative — reading
// a failed read as "covers nothing" — would be the right refusal for the wrong
// reason, and one that answered the other way would serve a repository the owner
// never declared.
func TestWorkspaceProxyRefusesACoverageItCannotRead(t *testing.T) {
	var wsUnreadableCoverage = errors.New("credsvc: the test's own workspace node could not be read")
	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo},
		covered:      []string{wsMemberRepo},
		admitted:     []string{wsMemberRepo},
		coverErr:     wsUnreadableCoverage,
	})
	w.up.answerWith(http.StatusOK, wsFetchAnswer)

	resp, ans := w.callAs(t, http.MethodGet, wsFetchPath(wsMemberRepo), nil, wsMemberRepo)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d, want %d (%s)", resp.StatusCode, StatusUnavailable, ans)
	}
	e := refusal(t, resp.StatusCode, ans)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal's type = %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, "coverage") {
		t.Errorf("the refusal = %q, want it to name the read that failed", e.Message)
	}
	if n := w.up.count(); n != 0 {
		t.Fatalf("bitbucket.org saw %d calls, want none: a coverage nobody could read serves nothing", n)
	}
	if line := w.s.onlyAuditLine(t); line["status"] != ErrorStateUnavailable {
		t.Errorf("the audit line = %v, want the service's own failure recorded", line)
	}
}

// TestWorkspaceProxyRefusesAStoreTheScopeDoesNotHold: the store a call is served
// from is the scope's own answer (ForgeReader.Bitbucket), and a scope that
// answers none is REFUSED rather than served out of the deployment's legacy
// entry (D143, D2355). Everything else about the call is admitted — the account
// is bound to the repository, the owner's credential covers it and the member's
// policy admits it with the proxy switch on — so what refuses it is the missing
// store and nothing else.
//
// The workspace's bundle holds the legacy `bitbucket-token` as well as the
// named one (legacyBundle), which is the whole of the fence: a route that kept
// the fallback this task removes would choose that entry and serve the call, so
// the refusal below is that fallback's absence and not the bundle's.
func TestWorkspaceProxyRefusesAStoreTheScopeDoesNotHold(t *testing.T) {
	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo},
		covered:      []string{wsMemberRepo},
		admitted:     []string{wsMemberRepo},
		noStore:      true,
		legacyBundle: true,
	})
	w.up.answerWith(http.StatusOK, wsFetchAnswer)

	resp, ans := w.callAs(t, http.MethodGet, wsFetchPath(wsMemberRepo), nil, wsMemberRepo)
	// The token store's own class and not one of the rules': not_admitted,
	// proxy_off and the coverage's own refusal are all passed here, so a refusal
	// under any of them would mean this fence was never asked.
	proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, string(bbtokens.ClassNoToken))
	if n := w.up.count(); n != 0 {
		t.Fatalf("bitbucket.org saw %d calls, want none: a call whose credential nothing chose is not made", n)
	}
	line := w.s.onlyAuditLine(t)
	if line["status"] != ErrorRequestRefused || line["repository"] != bitbucketSlug(t, wsMemberRepo) ||
		auditField(line, "credential") != config.BitbucketSecret || auditField(line, "purpose") != PurposeGit {
		t.Errorf("the audit line = %v, want one refusal of the run's own repository, under git's purpose and the credential a line names before any token was chosen", line)
	}
	if !strings.Contains(w.s.logs.String(), "class="+string(bbtokens.ClassNoToken)) {
		t.Errorf("the operator's line does not name the class %q: %s", string(bbtokens.ClassNoToken), w.s.logs.String())
	}
}

// TestWorkspaceProxyStreamsAndHoldsNothing: an admitted call under a workspace's
// scope is relayed exactly as the installation relays one (B1.3, G2) — both
// directions streamed, nothing held, and no byte rewritten.
//
// The answer's first half is sixty-four kilobytes, and the size is the point:
// net/http buffers a handler's writes before they reach the caller — it copies
// the first 512 bytes of a streamed answer before it stops sniffing, holds two
// kilobytes before it starts chunking, and buffers four more on the connection —
// so a first half of a few bytes would be sitting in one of those buffers either
// way and would say nothing about the route. Sixty-four kilobytes is well past
// every one of them, and a proxy that read the answer whole before relaying it
// could not hand over half of it while the upstream was still holding the rest:
// this test would time out reading the first half rather than fail on a
// comparison. The request's body is four times the largest body this route reads
// whole — the push's update request, the LFS batch and the pull-request write are
// one mebibyte each — and a fetch's negotiation is forwarded unread, so a route
// that held or clipped it would show it here.
func TestWorkspaceProxyStreamsAndHoldsNothing(t *testing.T) {
	head := bytes.Repeat([]byte("a"), 64<<10)
	tail := []byte("0000")
	up := newWSStreamUpstream(t, head, tail)
	big := bytes.Repeat([]byte("w"), 4<<20)

	w := newWSWorld(t, wsConfig{
		repo:         wsMemberRepo,
		accountRepos: []string{wsMemberRepo},
		covered:      []string{wsMemberRepo},
		admitted:     []string{wsMemberRepo},
	}, func(_ *svc, c *Config, _ *wsProxyScope) { c.Client = up.client() })

	req, err := http.NewRequest(http.MethodPost, w.s.http.URL+proxyPath(wsGitPath(wsMemberRepo, "git-upload-pack")), bytes.NewReader(big))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.s.jobToken())
	req.Header.Set(HeaderRunIdentity, w.runTokenOf(t, wsMemberRepo))
	req.Header.Set(HeaderRunJob, w.s.job)
	resp, err := w.s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d, want 200", resp.StatusCode)
	}

	// The request reached bitbucket.org whole: four mebibytes of it, unchanged.
	select {
	case sent := <-up.body:
		if !bytes.Equal(sent, big) {
			t.Errorf("the upstream was sent %d bytes of the %d this call carried: the request is clipped or rewritten on the way", len(sent), len(big))
		}
	case <-time.After(wsStreamWait):
		t.Fatal("the upstream never received the request's body")
	}

	// The first half of the answer is the caller's while the upstream still holds
	// the rest: the read is bounded, and its failing is the failure this test
	// exists for.
	got := make([]byte, len(head)/2)
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, got)
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("reading the answer's first half: %v", err)
		}
	case <-time.After(wsStreamWait):
		t.Fatalf("the answer's first %d bytes never reached the caller while the upstream held the rest: the proxy is holding the answer whole", len(got))
	}
	if !bytes.Equal(got, head[:len(got)]) {
		t.Errorf("the answer's first half = %d bytes, want the upstream's own %d unchanged", len(got), len(got))
	}
	// The upstream's flush is waited for rather than read non-blockingly: the
	// caller's half cannot have arrived before the upstream put it on the wire, so
	// the channel is already closed by now and this never waits — but a `default:`
	// arm here reads the scheduler's luck instead, and the whole tree's run (three
	// concurrent `go test` invocations on four cores) failed it with data the
	// upstream had demonstrably sent.
	select {
	case <-up.flushed:
	case <-time.After(wsStreamWait):
		t.Error("the first half reached the caller before the upstream had put it on the wire")
	}

	// What the upstream held then arrives, so what the caller ends up holding is
	// the whole answer and not the first half of it.
	close(up.release)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer's second half: %v", err)
	}
	if want := append(append([]byte{}, head[len(got):]...), tail...); !bytes.Equal(rest, want) {
		t.Errorf("the answer's second half = %d bytes, want the head's own remainder and the tail (%d)", len(rest), len(want))
	}
	if line := w.s.onlyAuditLine(t); line["status"] != StatusOK {
		t.Errorf("the audit line = %v, want the forwarded call recorded as served", line)
	}
}

// wsStreamWait bounds the waits of the streaming test, so that a proxy holding an
// answer fails the test rather than hanging it. It is generous for a loopback hop
// and short enough to read as a bound.
const wsStreamWait = 10 * time.Second

// wsStreamUpstream is a fake bitbucket.org that answers one call in TWO halves
// and holds the second until the test releases it: the shape that tells a proxy
// which streams an answer from one that reads it whole and hands it over
// afterwards. It records the request's body as it arrived, which is what the
// request direction is held to.
type wsStreamUpstream struct {
	srv     *httptest.Server
	head    []byte
	tail    []byte
	flushed chan struct{}
	release chan struct{}
	body    chan []byte
}

func newWSStreamUpstream(t *testing.T, head, tail []byte) *wsStreamUpstream {
	t.Helper()
	u := &wsStreamUpstream{
		head: head, tail: tail,
		flushed: make(chan struct{}),
		release: make(chan struct{}),
		body:    make(chan []byte, 1),
	}
	var once sync.Once
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "the body could not be read", http.StatusInternalServerError)
			return
		}
		u.body <- sent
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(u.head); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		once.Do(func() { close(u.flushed) })
		select {
		case <-u.release:
		case <-r.Context().Done():
			return
		case <-time.After(wsStreamWait * 3):
			return
		}
		_, _ = w.Write(u.tail)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// client is the client the service under test makes its upstream call with: the
// two hosts a proxied call may reach, and no other, with the request handed over
// as it is — the body is NOT read here, so what the upstream sees is what the
// route streamed to it (proxyUpstream's own transport reads the body, which is
// what that fake needs and what this test cannot have).
func (u *wsStreamUpstream) client() *http.Client {
	base, err := url.Parse(u.srv.URL)
	if err != nil {
		panic("the streaming fake's URL is not a URL: " + err.Error())
	}
	return &http.Client{Transport: wsStreamTransport{base: base}}
}

type wsStreamTransport struct {
	base *url.URL
}

func (h wsStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Host {
	case "bitbucket.org", "api.bitbucket.org":
	default:
		return nil, fmt.Errorf("the proxy addressed %s://%s, and a proxied call reaches bitbucket.org or api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host, c.Host = h.base.Scheme, h.base.Host, h.base.Host
	return http.DefaultTransport.RoundTrip(c)
}
