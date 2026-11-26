package credsvc

// PL4's tests: the launcher's pool, resolved from the principal the run's slip
// carries (R7, D3176, D3180).
//
// What the file is about, in the contract's own words (contract-pools.md, "The
// launcher's pool"): a table's `launcher-pool` source names no pool, and which
// pool serves a run is decided per run from the verified principal on its slip
// and the pool its launch named with `--pool`. `found` false means "no pool
// designated" and is never an error — no designation, an empty pool, a named
// pool the launcher is not entitled to and a project no migration has touched
// all answer it, and the walk moves on to the chain's next source. An error
// means the store could not be read, and a caller that read it as "none" would
// serve a fallback where a pool exists.
//
// The five lookups below are driven through the WALK and not through the
// lookup's own signature wherever the claim is about what a run is served: the
// chain fixture's table names a `launcher-pool` source ahead of the
// repository's own key, so "the launcher's pool" and "no pool designated" are
// the two answers a call really gets — a pool member (tier a) or the key behind
// it (tier b). The one claim a walk cannot make is the error: walk fails the
// test on one, so "an unreadable store is an error" asks the lookup directly.
//
// The entitlement rule is one rule for both the designated and the named case
// (poolServesPrincipal), and the mutation tests say what it rests on: the pool's
// own `principal` — never its display `owner` (R7), never the run's `rb` claim
// (the requested_by a caller writes, Identity.Launcher), and never a name the
// caller sent. Everything here is hermetic (Convention 9): the designations are
// written through internal/creds' own store over internal/gcpfake's Realtime
// Database, so what the lookup reads back is the record the route really wrote,
// and every value is a NAME and never a credential (Convention 2).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// testOtherLauncherAccount and testOtherLauncherSub are a SECOND person of
	// the installation: the launcher whose pool is not the one under test. The
	// id is a Google subject's shape and not the fixture's own (auth_test.go's
	// rule), and it is one digit from it so that a subject compared by prefix
	// would still be told apart.
	testOtherLauncherAccount = "launcher-2@example.invalid"
	testOtherLauncherSub     = "104329501182774832336"

	// The fixture's pools, by name: the launcher's own, another person's, the
	// project's shared one (D3183), and one of the launcher's own that lists no
	// member (D3180's "an empty pool answers none").
	lpOwnPool    = "mine-pool"
	lpOtherPool  = "theirs-pool"
	lpSharedPool = "shared-pool"
	lpEmptyPool  = "empty-pool"
	// lpVanishedPool is a name no node of the fixture holds: a designation that
	// points at a pool the project has since stopped publishing.
	lpVanishedPool = "gone-pool"
	// lpOtherProject is another installation's project id, for the designation
	// that answers for a project this one is not.
	lpOtherProject = "another-project"
)

// lpLauncher is the fixture's launcher, as the slip records one (R7): a person
// with their own verified Google subject and never the address beside it.
func lpLauncher() routing.Principal {
	return routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}
}

// lpOtherLauncher is the second person's principal.
func lpOtherLauncher() routing.Principal {
	return routing.Principal{Kind: routing.PrincipalPerson, ID: testOtherLauncherSub}
}

// lpPoolPrincipal is a pool's principal as the config writes one.
func lpPoolPrincipal(kind routing.PrincipalKind, id string) *config.PoolPrincipal {
	return &config.PoolPrincipal{Kind: string(kind), ID: id}
}

// lpPool is one pool of the fixture's node: its name, the display owner it is
// published with (a pool must name one, D1936), its principal and its members.
//
// The owner is a parameter and not a constant because one test needs the two
// told apart: a pool whose `owner` is the launcher's own address and whose
// `principal` is somebody else serves that somebody else and nobody else (R7).
func lpPool(name, owner string, principal *config.PoolPrincipal, members ...string) config.Pool {
	return config.Pool{Name: name, Owner: owner, Principal: principal, Members: members}
}

// lpPools is the fixture's default node: the launcher's own pool, another
// person's and the project's shared one — the three answers the lookup can give
// for one launcher. The empty pool is not one of them: the reader refuses a pool
// with no members at the publish ("a pool with no members serves nothing"), so
// it is written where D3180's "an empty pool" really comes from
// (lpFixture.withEmptyPool).
func lpPools() []config.Pool {
	return []config.Pool{
		lpPool(lpOwnPool, testLauncherAccount, lpPoolPrincipal(routing.PrincipalPerson, testLauncherSub),
			testPoolCredential, testSecondPoolCredential),
		lpPool(lpOtherPool, testOtherLauncherAccount, lpPoolPrincipal(routing.PrincipalPerson, testOtherLauncherSub),
			testPoolCredential, testSecondPoolCredential),
		lpPool(lpSharedPool, testPoolOwner, lpPoolPrincipal(routing.PrincipalProject, testProject),
			testPoolCredential, testSecondPoolCredential),
	}
}

// lpPolicyDoc is the fixture's published node, in the shape `fugaro project
// policy publish` really writes: chainPolicyDoc's document — the two pool
// members the bundle holds, the admissions they are judged by and the spread
// switch — with `model_routing` (PL3, D3177) spliced on, and the pools written
// into BOTH blocks the way a publish writes them (publishedCraftPools,
// modelRoutingForPublish). chainFixture writes NO such key on purpose (an
// installation `fugaro update` has not migrated), and that state is a pool
// nothing resolves here.
//
// The two blocks are not interchangeable, and this is the whole reason the
// document is built here rather than shared: `craft_pools` is read by job
// images a release behind, so every publish strips a pool's rule, principal and
// threshold out of it, and the pool in C2's shape — the one this service
// resolves a launcher's pool from — travels under the key. A fixture that left
// the principal in `craft_pools` would be a node no publish produces, and the
// lookup would resolve a launcher's pool here while resolving nobody's pool on
// an installation anyone published.
func lpPolicyDoc(t *testing.T, pools ...config.Pool) string {
	t.Helper()
	stripped := make([]config.Pool, 0, len(pools))
	for _, p := range pools {
		q := p
		q.Rule, q.Principal, q.Threshold = "", nil, nil
		stripped = append(stripped, q)
	}
	encoded, err := json.Marshal(pools)
	if err != nil {
		t.Fatalf("the test's own pools do not marshal: %v", err)
	}
	doc := chainPolicyDoc(t, stripped...)
	return trimBrace(doc) + `,
		"model_routing": {"entries": [{"match": "*", "chain": [{"kind": "launcher-pool"}]}], "pools": ` + string(encoded) + `}}`
}

// trimBrace is doc with its closing brace and the whitespace before it removed,
// which is what lets a test add one member to a document it did not write.
func trimBrace(doc string) string {
	for i := len(doc) - 1; i >= 0; i-- {
		if doc[i] == '}' {
			return doc[:i]
		}
	}
	return doc
}

// lpTable is the fixture's routing table: one entry whose chain names the
// launcher's pool FIRST and the repository's own key behind it. The key is what
// makes every "none" answer readable — the walk moves on to it (D3180) — and
// the order is what makes every "the pool" answer readable, because a call
// served by the key was served by the second source.
func lpTable() routing.Table {
	return routing.Table{{Match: testModel, Chain: []routing.Source{
		{Kind: routing.KindLauncherPool},
		chainKey(),
	}}}
}

// lpCoverage is the grant a launcher builds from that table: one source per
// kind, the launcher's pool among them (CS14's own shape).
func lpCoverage() []creds.GrantSource { return coverage(lpTable()[0].Chain...) }

// lpBinding is the verified slip of one run: the principal the service verified
// when it wrote the slip (R7) and the sources its grant covers. It is built
// directly, as chainBinding is, because what this file tests about a slip is the
// principal it carries and not the route that wrote it (slip_chain_test.go).
func lpBinding(principal routing.Principal, sources []creds.GrantSource, named string) *slipBinding {
	return &slipBinding{
		id:    "launcher-pool-slip",
		owner: testLauncherAccount,
		slip: &creds.Slip{
			Slug:     testRepo,
			Run:      "launcher-pool-run",
			Launcher: principal,
			SlipChain: creds.SlipChain{
				Sources:   sources,
				NamedPool: named,
			},
		},
	}
}

// lpFixture is the service with a migrated installation behind it: a policy
// carrying `model_routing`, the chain fixture's bundle and admissions, the
// launcher class wired (the designation route's caller), and — this is the part
// no other fixture has — a REAL database behind Config.DB, so a designation is
// written and read back through the store the deployment uses.
type lpFixture struct {
	*svc
	db  *gcpfake.RTDB
	dbs creds.DB
}

// newLauncherPools is the fixture. storeNet false is the OTHER wiring: a
// deployment whose database implements the service's own patch half and not the
// read a designation needs (Config.DB's narrow type), which is the state "an
// error is the store could not be read" is about.
func newLauncherPools(t *testing.T, storeNet bool, pools ...config.Pool) *lpFixture {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		if storeNet {
			cfg.DB = client
		}
		auth, err := NewAuthenticator(AuthConfig{
			Project:         testProject,
			FirebaseProject: testFirebase,
			Audience:        testAudience,
			JobAccounts:     []string{testJobSA, testSiblingSA},
			Keys:            s.keys,
			JobKeys:         s.jobKeys,
			Outcomes:        RTDBOutcomes{DB: s.db},
			LauncherAccounts: []string{
				testLauncherAccount, testOtherLauncherAccount,
			},
			Now: func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's launcher class cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		s.src.docs = [][]byte{chainBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, lpPolicyDoc(t, pools...))
		s.policy.admissions[s.slug] = decodeTestChild(t, chainAdmits())
	})
	installTable(t, lpTable())
	return &lpFixture{svc: s, db: db, dbs: client}
}

// withEmptyPool is the fixture whose published node carries a pool with no
// member. It is written onto the DECODED policy and not into the document
// because the reader refuses such a pool where it is published (a pool with no
// members serves nothing): the state the contract answers "none" for is a node
// that carries one anyway, which is a hand-edited node — and an administrator
// holding the database can write any project state (F61). It goes into the
// block the lookup reads (ModelRouting.Pools) for lpPolicyDoc's reason: a pool
// spliced into `craft_pools` alone carries no principal, so it would answer
// "none" before the members were ever counted and this state would pin nothing.
func (f *lpFixture) withEmptyPool(t *testing.T) *lpFixture {
	t.Helper()
	f.policy.shared.ModelRouting.Pools = append(f.policy.shared.ModelRouting.Pools,
		lpPool(lpEmptyPool, testLauncherAccount, lpPoolPrincipal(routing.PrincipalPerson, testLauncherSub)))
	return f
}

// designates writes one principal's designation through the store the service
// reads, which is the record the route under test writes through the same
// functions: the lookup is asked of a record the service really made, not of a
// hand-built one.
func (f *lpFixture) designates(t *testing.T, principal routing.Principal, project, pool string) {
	t.Helper()
	d := routing.Designation{Principal: principal, Project: project, Pool: pool}
	if err := creds.WriteDesignation(context.Background(), f.dbs, d); err != nil {
		t.Fatalf("writing the fixture's designation: %v", err)
	}
}

// designation is one principal's designation as the store answers it: found
// false is the state every refusal below must leave behind.
func (f *lpFixture) designation(t *testing.T, principal routing.Principal) (routing.Designation, bool) {
	t.Helper()
	d, found, err := creds.ReadDesignation(context.Background(), f.dbs, principal)
	if err != nil {
		t.Fatalf("reading the fixture's designation: %v", err)
	}
	return d, found
}

// lpWalk is the fixture's chain for one call whose RUN IDENTITY the test writes,
// which chain_test's walkIn cannot do: it stamps the fixture's own identity, and
// what "a forged requested_by changes nothing" needs is exactly a run whose
// identity token claims a launcher it is not. The walk reads the SLIP for the
// launcher and never the claim, and only this signature can show that.
func (f *lpFixture) lpWalk(t *testing.T, id Identity, body string, binding *slipBinding) *chainWalk {
	t.Helper()
	ctx := context.Background()
	b, err := f.server.cfg.Bundles.Load(ctx)
	if err != nil {
		t.Fatalf("loading the fixture's bundle: %v", err)
	}
	shared := f.policy.shared
	cl := &call{body: []byte(body), header: http.Header{}}
	plan, byChain, err := f.server.chainFor(ctx, cl, id, testRepo, shared, b, binding)
	if err != nil {
		t.Fatalf("resolving the fixture's chain: %v", err)
	}
	if !byChain {
		t.Fatal("the call was not served by a chain at all, and the fixture publishes a table")
	}
	settings := settingsOf(shared)
	now := func() time.Time { return f.now }
	w := &chainWalk{
		plan:  plan,
		sel:   &creds.Selector{Store: f.state, Settings: settings, Now: now},
		pick:  creds.PoolRule{Store: f.state, Settings: settings, Now: now},
		run:   creds.Run{Slug: f.slug, ID: f.run},
		cl:    cl,
		asked: cl.body,
		now:   now,
	}
	cl.chain = w
	return w
}

// walk is lpWalk with the identity claim forged to nothing: the honest call.
func (f *lpFixture) walk(t *testing.T, body string, binding *slipBinding) *chainWalk {
	t.Helper()
	return f.forged(t, "", body, binding)
}

// forged is lpWalk with the run's own `rb` claim — the requested_by, the display
// identity a caller writes (Identity.Launcher) — set to claim, which no reader of
// this path may trust (R7).
func (f *lpFixture) forged(t *testing.T, claim, body string, binding *slipBinding) *chainWalk {
	t.Helper()
	id := Identity{Slug: f.slug, Run: f.run, Project: testProject, Launcher: claim}
	return f.lpWalk(t, id, body, binding)
}

// lpStep is the launcher-pool source of a call's plan and whether the walk kept
// it. A dropped step IS "none" — the source names no credential and so is not
// recorded as a skip (chain.go's header) — and the source behind it is then what
// served the call.
func lpStep(w *chainWalk) (chainSource, bool) {
	for _, src := range w.plan.sources {
		if src.name.Kind == routing.KindLauncherPool {
			return src, true
		}
	}
	return chainSource{}, false
}

// wantFallback holds a call to having been served by the chain's SECOND source —
// the repository's own key — which is what "the walk moves on with the step
// flagged a fallback" means (D3180) and what every "none" answer has to show.
func wantFallback(t *testing.T, w *chainWalk) {
	t.Helper()
	if _, kept := lpStep(w); kept {
		t.Errorf("the chain kept the launcher's pool: want the walk to move on to the next source")
	}
	got := w.picked(t)
	if got.Tier != creds.TierB {
		t.Errorf("the call was served by %q (tier %s), want the repository's own key (tier b): "+
			"a launcher's pool that answers none leaves the chain's next source to serve (D3180)", got.Name, got.Tier)
	}
}

// wantPool holds a call to having been served by the pool the test names, out of
// that pool's own members.
func wantPool(t *testing.T, w *chainWalk, name string) {
	t.Helper()
	step, kept := lpStep(w)
	if !kept {
		t.Fatalf("the chain dropped the launcher's pool, want it served from %q", name)
	}
	if step.name.Name != name {
		t.Errorf("the launcher's pool resolved to %q, want %q", step.name.Name, name)
	}
	if step.pool.Project != testProject || step.pool.Pool != name {
		t.Errorf("the pool reference is %+v, want the project's own spelling of %q", step.pool, name)
	}
	if step.pool.Rule == "" {
		t.Errorf("the pool reference carries no rule: the resolved rule is never empty (C2)")
	}
	got := w.picked(t)
	if got.Tier != creds.TierA {
		t.Errorf("the call was served by %q (tier %s), want a member of %q (tier a)", got.Name, got.Tier, name)
	}
	if got.Name != testPoolCredential && got.Name != testSecondPoolCredential {
		t.Errorf("the call was served by %q, which is no member of the fixture's pools", got.Name)
	}
}

// ---------------------------------------------------------------------------
// The launcher's pool is the slip owner's
// ---------------------------------------------------------------------------

// TestLauncherPoolIsTheSlipOwners is PL4's red step: a run whose slip carries a
// person's verified subject is served from the pool THAT person designated, and
// the stub this replaces answers "none" for every principal (launcherpool_stub.go).
//
// The second half is the same resolution read from the other seat: the same
// fixture, a second person's run, and the first person's designation is not that
// run's answer — which is the whole of "only to runs that person launched".
func TestLauncherPoolIsTheSlipOwners(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	s.designates(t, lpLauncher(), testProject, lpOwnPool)

	t.Run("the launcher's own run is served from its pool", func(t *testing.T) {
		w := s.walk(t, callBody("pool-own"), lpBinding(lpLauncher(), lpCoverage(), ""))
		wantPool(t, w, lpOwnPool)
	})

	t.Run("another person's run is not", func(t *testing.T) {
		w := s.walk(t, callBody("pool-other"), lpBinding(lpOtherLauncher(), lpCoverage(), ""))
		wantFallback(t, w)
	})
}

// ---------------------------------------------------------------------------
// The loop, end to end: a real slip's call
// ---------------------------------------------------------------------------

// lpGrant is the fixture's launcher asking for a slip through ServeHTTP, and the
// answer read as this contract reads it. The shape is the grant route's own: a
// bearer and no run identity at all (slip.go's slipGrant refuses one), which is
// why it is not postWith's two-identity default.
func (f *lpFixture) lpGrant(t *testing.T, body string) SlipGrantResponse {
	t.Helper()
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.launcherToken()) }
	resp := f.postWith(t, f.http.Client(), SlipPath, body, "", "", "", bearer)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the grant's answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant is %d, want 200: %s", resp.StatusCode, raw)
	}
	var ans SlipGrantResponse
	if err := json.Unmarshal(raw, &ans); err != nil {
		t.Fatalf("the grant's answer is not one this contract reads: %v", err)
	}
	return ans
}

// lpClaim is the run's claim of the slip lpGrant just answered, as the secret
// every call after it presents: the grant was the fixture's own launcher's, so
// the run identity and the job's token are the fixture's too.
func (f *lpFixture) lpClaim(t *testing.T, slipID string) string {
	t.Helper()
	resp := f.postTokens(t, SlipClaimPath, claimBody(t, ""), f.runToken(), f.jobToken(), f.job,
		func(r *http.Request) { r.Header.Set(HeaderSlip, slipID) })
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the claim's answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is %d, want 200: %s", resp.StatusCode, raw)
	}
	var claimed SlipClaimResponse
	if err := json.Unmarshal(raw, &claimed); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v", err)
	}
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret, so no call can be admitted under the slip")
	}
	return claimed.ClaimSecret
}

// TestARealSlipsCallReachesTheLauncherPool is the one test here whose binding is
// not hand-built: a launcher grants a slip covering the table's sources, its run
// claims it, and the run's model call goes through ServeHTTP and is served from
// the pool that launcher designated. It is what keeps PL4's acceptance —
// "resolves «the launcher's pool» for a run from the verified owner of its slip"
// — a claim about the service rather than about a fixture, because the binding
// it resolves from is the one verifyCallSlip really builds for a project run
// (server.go): a reader that left the record out would resolve no pool here and
// the call would land on the repository's key behind the source.
func TestARealSlipsCallReachesTheLauncherPool(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	s.designates(t, lpLauncher(), testProject, lpOwnPool)

	granted := s.lpGrant(t, chainBody(t, s.slug, s.run, testJobSA, 3600, "", lpCoverage(),
		mustSharedToken(t, testPoolCredential), mustSharedToken(t, testSecondPoolCredential)))
	if granted.Slip == "" {
		t.Fatal("the grant answered no slip")
	}
	claimSecret := s.lpClaim(t, granted.Slip)

	resp, body := s.call(t, callBody("real-slip"), func(r *http.Request) {
		r.Header.Set(HeaderSlip, granted.Slip)
		r.Header.Set(HeaderSlipClaim, claimSecret)
		r.Header.Set(HeaderStage, "coder")
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the run's call is %d, want 200: %s", resp.StatusCode, body)
	}
	var name routing.SourceName
	if err := json.Unmarshal([]byte(resp.Header.Get(HeaderSource)), &name); err != nil {
		t.Fatalf("the answer's source member %q is not a routing.SourceName: %v", resp.Header.Get(HeaderSource), err)
	}
	if name.Kind != routing.KindLauncherPool || name.Name != lpOwnPool {
		t.Errorf("the call was served from %+v, want the launcher's own pool %q: the run's slip carries the person's "+
			"verified subject and the pool is theirs (D3179-amended, R7)", name, lpOwnPool)
	}
	if got := resp.Header.Get(HeaderFallback); got != FallbackNo {
		t.Errorf("the answer says fallback=%q, want %q: the chain's first source served (C1)", got, FallbackNo)
	}
	// And the credential the upstream was sent is one of the pool's own members
	// and never the repository's key the chain names second.
	sent := s.up.Seen()
	if len(sent) != 1 {
		t.Fatalf("the upstream saw %d calls, want exactly 1", len(sent))
	}
	switch auth := sent[0].Header.Get("Authorization"); auth {
	case "Bearer " + fakeCred(t, testPoolCredential), "Bearer " + fakeCred(t, testSecondPoolCredential):
	default:
		t.Errorf("the upstream was sent the credential %q, want one of %q's members", auth, lpOwnPool)
	}
}

// TestAReleaseBehindLaunchersCallIsServedFromTheOldRecord is the other half of
// the binding above: a launcher that names no source — the grant body an older
// CLI sends, and the one a launch before the installation published its table
// sends — is a slip whose grant covers no source of the new kinds, and CS14 is
// explicit that an empty list is "what a release-behind launcher asks for — and
// never a grant that covers everything" (contract-pools.md). So the call is NOT
// served from the table: the walk sends it to the release-1 bridge, and the
// bridge answers the sources the repository's OLD record stands for
// (creds/repoauth/<slug>, which `fugaro update` keeps writing beside the table
// for release 1) — that run's own reach, and the entries D3196 leaves on the
// record among them.
//
// The bridge here is the one a deployment assigns (internal/cli's own
// assignment, NewLegacyAuth over this fixture's database), not a fake, and the
// record is written through creds' writer: what the walk reads is a record a
// release behind really left. The record's source is a pool the TABLE does not
// name at all — an `oauth` record's account is a pool of one (D3176) — so a call
// served by the table's own chain could not answer this test: the table would
// serve the launcher's pool or the repository's key, and only the record answers
// the account's pool.
//
// The call goes through ServeHTTP for the reason the test above does: what is
// under test is the binding verifyCallSlip really builds, and a reader that left
// the record out would serve this run from the table instead — the migration
// would move a run it promises not to move.
func TestAReleaseBehindLaunchersCallIsServedFromTheOldRecord(t *testing.T) {
	s := newLauncherPools(t, true, append(lpPools(),
		lpPool(testAccount, testPoolOwner, lpPoolPrincipal(routing.PrincipalProject, testProject), testPoolCredential))...)

	client := legacyClient(t, s.db)
	prev := LegacyChain
	LegacyChain = NewLegacyAuth(client, RTDBPolicy{DB: client})
	t.Cleanup(func() { LegacyChain = prev })
	legacyPolicyNode(t, s.db, "")
	legacyRecord(t, s.db, s.slug, config.AuthOAuth, testAccount)

	granted := s.lpGrant(t, legacyGrantBody(t, s.slug, s.run, testJobSA, 3600))
	if granted.Slip == "" {
		t.Fatal("the grant answered no slip")
	}
	claimSecret := s.lpClaim(t, granted.Slip)

	resp, body := s.call(t, callBody("release-behind"), func(r *http.Request) {
		r.Header.Set(HeaderSlip, granted.Slip)
		r.Header.Set(HeaderSlipClaim, claimSecret)
		r.Header.Set(HeaderStage, "coder")
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the run's call is %d, want 200: %s", resp.StatusCode, body)
	}
	var name routing.SourceName
	if err := json.Unmarshal([]byte(resp.Header.Get(HeaderSource)), &name); err != nil {
		t.Fatalf("the answer's source member %q is not a routing.SourceName: %v", resp.Header.Get(HeaderSource), err)
	}
	if name.Kind != routing.KindPool || name.Name != testAccount {
		t.Errorf("the call was served from %+v, want the record's own account %q as its pool of one: a grant that "+
			"covers no source is the release-behind launcher, and its call is answered from the old record's sources "+
			"and never the table's (CS14, MG3)", name, testAccount)
	}
	if got := resp.Header.Get(HeaderFallback); got != FallbackNo {
		t.Errorf("the answer says fallback=%q, want %q: the record names one source and it served", got, FallbackNo)
	}
	sent := s.up.Seen()
	if len(sent) != 1 {
		t.Fatalf("the upstream saw %d calls, want exactly 1", len(sent))
	}
	if auth := sent[0].Header.Get("Authorization"); auth != "Bearer "+fakeCred(t, testPoolCredential) {
		t.Errorf("the upstream was sent the credential %q, want the record's own account's pool member", auth)
	}
}

// ---------------------------------------------------------------------------
// No designation, and every state that is not one, answers "none"
// ---------------------------------------------------------------------------

// TestNoDesignationIsNone: "none" is a state and never a refusal (D3180). No
// designation, a designation that names a pool the project no longer publishes,
// one written for another project and an EMPTY pool all answer it, and in every
// case the walk moves on to the chain's next source rather than refusing the
// call.
//
// The last subtest is the other half of the contract's own sentence: an error
// means the store could not be read, and it is NOT "none" — a caller that read
// it as none would serve a fallback source where a pool exists.
func TestNoDesignationIsNone(t *testing.T) {
	t.Run("nothing is designated", func(t *testing.T) {
		s := newLauncherPools(t, true, lpPools()...)
		wantFallback(t, s.walk(t, callBody("pool-none"), lpBinding(lpLauncher(), lpCoverage(), "")))
	})

	t.Run("the designated pool is empty", func(t *testing.T) {
		s := newLauncherPools(t, true, lpPools()...).withEmptyPool(t)
		s.designates(t, lpLauncher(), testProject, lpEmptyPool)
		wantFallback(t, s.walk(t, callBody("pool-empty"), lpBinding(lpLauncher(), lpCoverage(), "")))
	})

	t.Run("the designated pool is not published", func(t *testing.T) {
		s := newLauncherPools(t, true, lpPools()...)
		s.designates(t, lpLauncher(), testProject, lpVanishedPool)
		wantFallback(t, s.walk(t, callBody("pool-gone"), lpBinding(lpLauncher(), lpCoverage(), "")))
	})

	t.Run("the designation is another project's", func(t *testing.T) {
		s := newLauncherPools(t, true, lpPools()...)
		s.designates(t, lpLauncher(), lpOtherProject, lpOwnPool)
		wantFallback(t, s.walk(t, callBody("pool-project"), lpBinding(lpLauncher(), lpCoverage(), "")))
	})

	t.Run("a store that cannot be read is an error and never none", func(t *testing.T) {
		s := newLauncherPools(t, false, lpPools()...)
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		// The record is there and this deployment cannot read it: the answer
		// must be an error, because "none" would serve the key behind the pool
		// to a run whose pool exists.
		ref, found, err := launcherPools{s: s.server}.LauncherPool(context.Background(), lpLauncher(), "")
		if err == nil {
			t.Fatalf("a deployment that cannot read a designation answered (%+v, %v, nil), want an error", ref, found)
		}
		if found || ref.Pool != "" {
			t.Errorf("the error answer carries (%+v, %v): a refusal carries no value", ref, found)
		}
	})
}

// ---------------------------------------------------------------------------
// A run with no slip has no launcher's pool
// ---------------------------------------------------------------------------

// TestARunWithNoSlipHasNoLaunchersPool is the plan's own rule: "a run with no
// slip is served the project's sources its repository is admitted to and no
// launcher's pool" (CS1). It is the orchestrator's launch, which carries no slip
// at all, and the shape is the grant's own: grantCovers admits every kind but
// the launcher's pool for a nil grant, and a call that never reached the
// launcher's pool is a call the pool cannot be resolved for — even where the
// person who designated one is the person who asked.
//
// It holds for the shape a call with no slip really has — no binding at all —
// and for a binding carrying no record, which no reader of this service
// produces (verifyCallSlip and verifyPersonCallSlip both carry the record) and
// which is held here as the gate's fail-closed direction: if a reader ever left
// the record out, its run resolves no pool rather than reading a zero
// principal.
func TestARunWithNoSlipHasNoLaunchersPool(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	s.designates(t, lpLauncher(), testProject, lpOwnPool)

	t.Run("no binding at all", func(t *testing.T) {
		w := s.walk(t, callBody("pool-noslip"), nil)
		wantFallback(t, w)
	})

	t.Run("a binding with no record", func(t *testing.T) {
		w := s.walk(t, callBody("pool-noslip"), &slipBinding{id: "none", owner: testLauncherAccount})
		wantFallback(t, w)
	})
}

// ---------------------------------------------------------------------------
// A forged requester gets nothing
// ---------------------------------------------------------------------------

// TestAForgedRequesterGetsNothing is the mutation test the acceptance names: a
// forged `requested_by` changes nothing, and the display `owner` of a pool
// decides nothing either (R7). What the resolution reads is the pool's own
// `principal` against the principal the slip carries, and neither of the two
// texts a caller or an operator writes can move a run onto another person's
// pool.
//
// Three mutations, and each is a way the same widening could be introduced:
//
//   - the pool's display owner names the launcher while its principal names
//     somebody else: the launcher's run gets NOTHING;
//   - the pool's display owner names somebody else while its principal IS the
//     launcher: the launcher's run is served, so the owner is not read as a
//     refusal either;
//   - the run's own identity token claims the launcher in its `rb` claim — the
//     requested_by, which the caller writes — while its slip carries somebody
//     else: nothing changes, and the answer is the same as the honest call's.
func TestAForgedRequesterGetsNothing(t *testing.T) {
	t.Run("a pool whose owner is the launcher but whose principal is not", func(t *testing.T) {
		pool := lpPool(lpOwnPool, testLauncherAccount,
			lpPoolPrincipal(routing.PrincipalPerson, testOtherLauncherSub),
			testPoolCredential, testSecondPoolCredential)
		s := newLauncherPools(t, true, pool)
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		wantFallback(t, s.walk(t, callBody("pool-forged-owner"), lpBinding(lpLauncher(), lpCoverage(), "")))
	})

	t.Run("a pool whose owner is another person but whose principal is the launcher", func(t *testing.T) {
		pool := lpPool(lpOwnPool, testOtherLauncherAccount,
			lpPoolPrincipal(routing.PrincipalPerson, testLauncherSub),
			testPoolCredential, testSecondPoolCredential)
		s := newLauncherPools(t, true, pool)
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		wantPool(t, s.walk(t, callBody("pool-owner-text"), lpBinding(lpLauncher(), lpCoverage(), "")), lpOwnPool)
	})

	t.Run("a run whose rb claim names the launcher", func(t *testing.T) {
		s := newLauncherPools(t, true, lpPools()...)
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		forged := s.forged(t, lpLauncher().ID, callBody("pool-forged-rb"), lpBinding(lpOtherLauncher(), lpCoverage(), ""))
		wantFallback(t, forged)
		// And the same shape with no slip: a claim is no slip either, so the
		// forged run is exactly as pool-less as the honest one (CS1).
		wantFallback(t, s.forged(t, lpLauncher().ID, callBody("pool-forged-rb"), nil))
	})
}

// ---------------------------------------------------------------------------
// A named pool must be the launcher's
// ---------------------------------------------------------------------------

// TestANamedPoolMustBeTheLaunchers is D3176's second half: `--pool` says which
// pool "the launcher's pool" resolves to for this run, and naming one never
// widens what the run may be served from. The launcher's own pool and the
// project's shared one — "a pool its launcher owns or is allowed on" — resolve;
// another person's private pool, a pool the project does not publish and the
// empty one of the launcher's own answer nothing.
//
// The last subtest is what the flag is FOR: with a designation of its own in the
// store, a launch that named the shared pool is served from the shared pool, so
// the name decides and the designation is what it falls back to.
func TestANamedPoolMustBeTheLaunchers(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...).withEmptyPool(t)
	s.designates(t, lpLauncher(), testProject, lpOwnPool)

	t.Run("the launcher's own pool", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-own"), lpBinding(lpLauncher(), lpCoverage(), lpOwnPool))
		wantPool(t, w, lpOwnPool)
	})

	t.Run("the project's shared pool", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-shared"), lpBinding(lpLauncher(), lpCoverage(), lpSharedPool))
		wantPool(t, w, lpSharedPool)
	})

	t.Run("another person's pool", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-other"), lpBinding(lpLauncher(), lpCoverage(), lpOtherPool))
		wantFallback(t, w)
	})

	t.Run("a pool the project does not publish", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-gone"), lpBinding(lpLauncher(), lpCoverage(), lpVanishedPool))
		wantFallback(t, w)
	})

	t.Run("the launcher's own pool with no members", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-empty"), lpBinding(lpLauncher(), lpCoverage(), lpEmptyPool))
		wantFallback(t, w)
	})

	t.Run("the name decides, and the designation is what it falls back to", func(t *testing.T) {
		w := s.walk(t, callBody("pool-named-wins"), lpBinding(lpLauncher(), lpCoverage(), lpSharedPool))
		wantPool(t, w, lpSharedPool)
		// The designation is untouched: naming a pool for one run is not a
		// designation written, and the record still answers the same thing.
		d, found := s.designation(t, lpLauncher())
		if !found || d.Pool != lpOwnPool {
			t.Errorf("the designation is (%+v, %v), want %q: a `--pool` flag writes no record", d, found, lpOwnPool)
		}
	})
}

// ---------------------------------------------------------------------------
// The designation route
// ---------------------------------------------------------------------------

// lpDesignate posts one designation as the launcher, with no run identity: the
// route's caller is a launcher, which holds none (designation_route.go).
func (f *lpFixture) lpDesignate(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.launcherToken()) }
	resp := f.postWith(t, f.http.Client(), PoolDesignatePath, body, "", "", "",
		append([]func(*http.Request){bearer}, mutate...)...)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, raw
}

// lpAudit is the fixture's audit lines so far, or none at all — auditLines
// fails a test on an EMPTY buffer, and the route's tests read the buffer
// between calls.
func lpAudit(t *testing.T, s *lpFixture) []map[string]any {
	t.Helper()
	if s.audit.Len() == 0 {
		return nil
	}
	return auditLines(t, s.audit)
}

// TestDesignationRouteRefusesAnothersPool: a designation is written only through
// this route, keyed on the subject the service verified (R26, F61) — so the
// route refuses a pool the caller does not own, a pool the project does not
// publish, a body that tries to name a principal, and a call that presents a run
// identity. The rule that cannot be written in the database (people reach it by
// IAM) is exactly this: the caller's subject is the key, and no field of the
// request can name another.
//
// Every refusal here is a refusal AFTER the caller was verified, so each writes
// exactly one audit line and no credential value — and the store is left exactly
// as it was, which is what the first subtest's own record is read back for.
func TestDesignationRouteRefusesAnothersPool(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)

	t.Run("the caller's own pool is written", func(t *testing.T) {
		before := len(lpAudit(t, s))
		resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpOwnPool}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the honest designation is %d, want it written: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store: the answer is the caller's own state", got)
		}
		var d routing.Designation
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatalf("the answer is not a designation: %v (%s)", err, body)
		}
		if want := (routing.Designation{Principal: lpLauncher(), Project: testProject, Pool: lpOwnPool}); d != want {
			t.Errorf("the answer = %+v, want %+v", d, want)
		}
		// The record is keyed on the caller's SUBJECT and holds the project the
		// service derived, never anything the request said.
		back, found := s.designation(t, lpLauncher())
		if !found || back != d {
			t.Errorf("the store holds (%+v, %v), want the answer %+v", back, found, d)
		}
		wantDesignationLine(t, s, before, StatusOK)
	})

	refusals := []struct {
		name string
		body string
		says string
	}{
		{"another person's pool", mustBody(t, PoolDesignateRequest{Pool: lpOtherPool}), designationNotTheCallers},
		{"a pool the project does not publish", mustBody(t, PoolDesignateRequest{Pool: lpVanishedPool}), designationNoSuchPool},
		{"a body naming a principal", `{"pool":"` + lpOwnPool + `","principal":"` + testOtherLauncherSub + `"}`,
			"the principal is the one the service verified"},
		{"a pool that is not a name", `{"pool":"a b c"}`, "the call names no pool"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := len(lpAudit(t, s))
			resp, body := s.lpDesignate(t, tc.body)
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("the call was served: %d %s", resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
			} else if !strings.Contains(e.Message, tc.says) {
				t.Errorf("the refusal %q does not say %q", e.Message, tc.says)
			}
			wantDesignationLine(t, s, before, ErrorRequestRefused)
			// Nothing was written, and the record the honest call left is
			// exactly what it was: a refused designation writes no value.
			if d, found := s.designation(t, lpLauncher()); !found || d.Pool != lpOwnPool {
				t.Errorf("the store holds (%+v, %v), want the honest call's record untouched", d, found)
			}
			if _, found := s.designation(t, lpOtherLauncher()); found {
				t.Error("a designation was written for another principal")
			}
		})
	}

	t.Run("the project's shared pool is one the caller may designate", func(t *testing.T) {
		// "a pool its launcher owns or is allowed on" (D3176, D3183): the shared
		// pool belongs to the project, and a person's designation of it is what
		// puts their own runs on it.
		before := len(lpAudit(t, s))
		resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpSharedPool}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("designating the shared pool is %d, want it written: %s", resp.StatusCode, body)
		}
		if d, found := s.designation(t, lpLauncher()); !found || d.Pool != lpSharedPool {
			t.Errorf("the store holds (%+v, %v), want the shared pool", d, found)
		}
		wantDesignationLine(t, s, before, StatusOK)
	})

	t.Run("a call presenting a run identity", func(t *testing.T) {
		before := len(lpAudit(t, s))
		resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpOwnPool}),
			func(r *http.Request) { r.Header.Set(HeaderRunIdentity, s.runToken()) })
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a call presenting a run identity was served: %d %s", resp.StatusCode, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
			t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
		} else if !strings.Contains(e.Message, designationNoRunIdentity) {
			t.Errorf("the refusal %q does not say %q", e.Message, designationNoRunIdentity)
		}
		wantDesignationLine(t, s, before, ErrorRequestRefused)
	})

	// The operator's switches, which this route reads itself because it is
	// answered ahead of the shared parse that reads them for every other route.
	// A designation is a lasting statement — a run's later calls are served from
	// what it names — so an operator who stopped the service stopped what it
	// decides and not only what it serves, and the write must not land. A switch
	// that cannot be read is the same refusal and not a serve: an unreadable
	// switch is not an off switch (budget's own rule), and its sentence is the
	// service's own, never the reader's text.
	//
	// Each is a refusal AFTER the caller was verified, so each owes exactly one
	// audit line under the route's own purpose, and the store holds what it held
	// before — a stop is not a burn.
	for _, tc := range []struct {
		name string
		stop func(*lpFixture)
	}{
		{"the global switch", func(s *lpFixture) { s.kills.global = true }},
		{"the switches that cannot be read", func(s *lpFixture) {
			s.kills.err = errors.New("the switches node could not be read")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(lpAudit(t, s))
			held, was := s.designation(t, lpLauncher())
			tc.stop(s)
			resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpOwnPool}))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("a stopped service answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
				t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorStateUnavailable)
			} else if strings.Contains(e.Message, "switches node") {
				t.Errorf("the refusal carries the reader's own text: %q", e.Message)
			}
			wantDesignationLine(t, s, before, ErrorStateUnavailable)
			if d, found := s.designation(t, lpLauncher()); found != was || d != held {
				t.Errorf("the store holds (%+v, %v), want the record before the switch (%+v, %v)", d, found, held, was)
			}

			// The switch is a stop and not a refusal of this call's shape:
			// cleared, the same launcher's own designation is written.
			s.kills.global, s.kills.err = false, nil
			if resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpOwnPool})); resp.StatusCode != http.StatusOK {
				t.Errorf("the designation after the switch cleared is %d, want it written: %s", resp.StatusCode, body)
			}
		})
	}
}

// wantDesignationLine holds ONE act of the route to having written exactly one
// line, with the verified caller and the route's own purpose, and with no
// credential, run, repository or job — the shape a credentialless purpose's line
// has (audit.go's checked writes no such field, and this is the test that reads
// it from outside). before is how many lines the fixture had already written,
// which is what lets one fixture run the route several times: each act owes
// exactly one MORE line, and never zero and never two.
func wantDesignationLine(t *testing.T, s *lpFixture, before int, status string) {
	t.Helper()
	lines := lpAudit(t, s)
	if len(lines) != before+1 {
		t.Fatalf("the route has written %d audit lines, want exactly one more than the %d before it", len(lines), before)
	}
	line := lines[before]
	if line["purpose"] != PurposePoolDesignate || line["status"] != status {
		t.Errorf("the line is %v/%v, want %q/%q", line["purpose"], line["status"], PurposePoolDesignate, status)
	}
	if line["caller"] != testLauncherAccount {
		t.Errorf("the line's caller is %v, want the verified address %q", line["caller"], testLauncherAccount)
	}
	for _, field := range []string{"credential", "tier", "run", "repository", "job"} {
		if got := line[field]; got != "" {
			t.Errorf("the line carries %s = %v, want none: a designation touches no credential and no run", field, got)
		}
	}
}
