package credsvc

// CS1's tests: the service walks the chain.
//
// The walk is exercised at the seam it is written to — chainFor resolves a
// table's entry for the model a call's body names and answers the chain's
// sources, and the chainWalk offers them in the chain's own order — with the
// service's own fixtures for everything else (the bundle, the published node,
// the state store, the admissions). The two tests that are about the ANSWER
// (#5, #6) go through the real HTTP path, because what they assert is what a
// caller reads.
//
// A slip is built directly here rather than granted and claimed, and that is
// deliberate: what this file is testing about a grant is which SOURCES it
// covers, which is a fact about the record and not about the route that wrote
// it — and that route has its own tests (slip_chain_test.go, CS14). Building
// the record directly is also what lets a test state a grant a launcher could
// hold without writing an HTTP fixture for every case.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// ---------------------------------------------------------------------------
// The fixture: a table installed, a bundle that holds what the chain names
// ---------------------------------------------------------------------------

// chainUnheld is a credential the fixture's document defines and its bundle
// does not hold: a pool listing it is a pool this service has no member for, so
// the walk meets D3180's "nothing usable" without inventing a pool no
// administrator could publish (ValidatePools refuses an empty member list).
const chainUnheld = "unheld-token"

// chainBundleDoc is the installation's bundle with the two pool members the
// fixtures' pools list, beside the default account and the repository's own
// paid key. The default document (testBundleDoc) holds neither pool member, so
// a pool would resolve to nothing and every pool test would pass for the wrong
// reason; and it holds the PROJECT's Anthropic key rather than a repository's,
// while the chain reads the repository's own (ownNamedEntry, D3008).
func chainBundleDoc(t *testing.T) []byte {
	t.Helper()
	doc := []byte(config.ClaudeOAuthToken + ": " + fakeCred(t, config.DefaultClaudeAccount) + "\n" +
		config.ClaudeOAuthTokenPrefix + testPoolCredential + ": " + fakeCred(t, testPoolCredential) + "\n" +
		config.ClaudeOAuthTokenPrefix + testSecondPoolCredential + ": " + fakeCred(t, testSecondPoolCredential) + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
	own := repoEntryKeyFor(t, "github", testRepoPath, config.AnthropicKeyAccount)
	return append(doc, repoEntryDoc(t, own, fakeCred(t, "repo-key"), testProviderBase, providerAuthXAPIKey)...)
}

// chainPolicyDoc is the fixture's policy with the pools a test publishes: the
// document poolPolicyDoc writes, with the two pool members this file's bundle
// holds and the repository's own key's account among the accounts a member's
// admission is intersected with (admittedAccounts).
//
// subscription_spread is written TRUE and not left absent, which is what the
// switch means for this file: a pool of two subscription tokens is inert while
// it is off (D1622), so a fixture that left it out would have every pool serve
// nothing and every pool test pass for the wrong reason. It is the switch a
// migrated installation retires (D3177, RT4) and the one this release still
// reads, so writing it is what an operator publishes TODAY.
func chainPolicyDoc(t *testing.T, pools ...config.Pool) string {
	t.Helper()
	encoded, err := json.Marshal(pools)
	if err != nil {
		t.Fatalf("the test's own pools do not marshal: %v", err)
	}
	credentials, err := json.Marshal([]config.Credential{
		{Name: testPoolCredential, Type: config.CredentialTypeClaudeOAuth},
		{Name: testSecondPoolCredential, Type: config.CredentialTypeClaudeOAuth},
		{Name: chainUnheld, Type: config.CredentialTypeClaudeOAuth},
	})
	if err != nil {
		t.Fatalf("the test's own credentials do not marshal: %v", err)
	}
	accounts := `"` + config.DefaultClaudeAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testPoolCredential + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testSecondPoolCredential + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}`
	return `{
		"version": 9,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			` + accounts + `
		},
		"credentials": {"fallback": "auto"},
		"subscription_spread": true,
		"craft_credentials": ` + string(credentials) + `,
		"craft_pools": ` + string(encoded) + `
	}`
}

// chainAdmits is the repository's own node as this file's fixture writes it: the
// accounts a pool's member is admitted by (R20). It is the four names the
// document above defines, so a member's admission is the pool that named it and
// never the absent account.
func chainAdmits() string {
	return testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolCredential, testSecondPoolCredential)
}

// installTable puts a routing table behind the seam RT4 will fill, for the
// duration of one test. It is a seam and not a member of the fixture because
// that is what the service reads: an installation that has not been migrated
// publishes no `model_routing` key at all, and PublishedTable's own default is
// that answer.
func installTable(t *testing.T, table routing.Table) {
	t.Helper()
	prev := PublishedTable
	PublishedTable = func(*projectpolicy.Policy) (routing.Table, bool) { return table, true }
	t.Cleanup(func() { PublishedTable = prev })
}

// chainFixture is the service with a table installed, a bundle that holds the
// chains' credentials, and the pools a test publishes.
func chainFixture(t *testing.T, table routing.Table, pools ...config.Pool) *svc {
	t.Helper()
	s := newSvc(t)
	installTable(t, table)
	s.src.docs = [][]byte{chainBundleDoc(t)}
	s.policy.shared = decodeTestPolicy(t, chainPolicyDoc(t, pools...))
	s.policy.admissions[s.slug] = decodeTestChild(t, chainAdmits())
	return s
}

// teamPool is the pool the fixtures publish: two subscription accounts, in the
// order its owner listed them.
func teamPool(strategy string) config.Pool {
	return config.Pool{
		Name:     slipTestPoolName,
		Strategy: strategy,
		Owner:    testLauncherAccount,
		Members:  []string{testPoolCredential, testSecondPoolCredential},
	}
}

// chainKey is the chain's single-credential source of the repository's own paid
// key, admitted to the fixture's repository (D3182's admit list).
func chainKey() routing.Source {
	return routing.Source{Kind: routing.KindAnthropic, Admit: []string{testRepo}}
}

// chainBinding is the verified slip of a run whose grant covers these sources,
// built directly: what this file tests about a grant is the SET of sources it
// covers, and the route that writes the record has its own tests.
func chainBinding(sources []creds.GrantSource, named string) *slipBinding {
	return &slipBinding{
		id:    "chain-slip",
		owner: testLauncherAccount,
		slip: &creds.Slip{
			Slug:     testRepo,
			Run:      "chain-run",
			Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub},
			SlipChain: creds.SlipChain{
				Sources:   sources,
				NamedPool: named,
			},
		},
	}
}

// coverage is the grant a launcher builds from a chain: one GrantSource per
// source, which is what CS14's route writes (the pool on the kinds that name
// one, the kind alone on the rest).
func coverage(sources ...routing.Source) []creds.GrantSource {
	out := make([]creds.GrantSource, 0, len(sources))
	for _, src := range sources {
		out = append(out, creds.GrantSource{Kind: string(src.Kind), Pool: src.Pool})
	}
	return out
}

// walk is the fixture's chain for one call: the plan chainFor resolved, and the
// walk built over it exactly as server.go builds it.
func (s *svc) walk(t *testing.T, body string, binding *slipBinding) *chainWalk {
	t.Helper()
	return s.walkIn(t, context.Background(), body, binding)
}

// walkIn is walk under a call's own context, which is what a test needs when the
// call's SCOPE is not the installation's: the offering a chain is narrowed by is
// read out of the call's scope (C1's OfferingReader), so a member's own chain is
// only reachable with the scope on the context (withScope).
func (s *svc) walkIn(t *testing.T, ctx context.Context, body string, binding *slipBinding) *chainWalk {
	t.Helper()
	b, err := s.server.cfg.Bundles.Load(ctx)
	if err != nil {
		t.Fatalf("loading the fixture's bundle: %v", err)
	}
	shared := s.policy.shared
	cl := &call{body: []byte(body), header: http.Header{}}
	id := Identity{Slug: s.slug, Run: s.run, Project: testProject}
	plan, byChain, err := s.server.chainFor(ctx, cl, id, testRepo, shared, b, binding)
	if err != nil {
		t.Fatalf("resolving the fixture's chain: %v", err)
	}
	if !byChain {
		t.Fatal("the call was not served by a chain at all, and the fixture publishes a table")
	}
	settings := settingsOf(shared)
	now := func() time.Time { return s.now }
	w := &chainWalk{
		plan:  plan,
		sel:   &creds.Selector{Store: s.state, Settings: settings, Now: now},
		pick:  creds.PoolRule{Store: s.state, Settings: settings, Now: now},
		run:   creds.Run{Slug: s.slug, ID: s.run},
		cl:    cl,
		asked: cl.body,
		now:   now,
	}
	// The call carries its walk, as server.go's own does before forward runs:
	// what reads it is the probe (probeCall), which asks the model the CALLER
	// named rather than the one the walk left in the body.
	cl.chain = w
	return w
}

// served is what the walk answers for a fresh call.
func (w *chainWalk) picked(t *testing.T) creds.Credential {
	t.Helper()
	c, _, err := w.next(context.Background(), map[string]bool{})
	if err != nil {
		t.Fatalf("the walk answered %v, want a credential", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// 1. The chain's own order replaces the fixed tier order
// ---------------------------------------------------------------------------

// TestChainOrderIsStrict is the plan's red step: a key listed before a pool is
// still served after it.
//
// The fixed order this replaces walked tier A (the donated subscription
// accounts) before tier B (the project's own paid key) whatever the project
// wrote, so a chain that lists the key first would be served from the pool by
// every call — which is exactly the behaviour R4 replaces (D3170). The test is
// the two orders of one chain, and the two answers must differ: the FIRST
// source of the chain is the source that serves, whatever tier it is.
func TestChainOrderIsStrict(t *testing.T) {
	pool := teamPool("")

	keyFirst := routing.Table{{Match: testModel, Chain: []routing.Source{
		chainKey(), {Kind: routing.KindPool, Pool: pool.Name},
	}}}
	s := chainFixture(t, keyFirst, pool)
	got := s.walk(t, callBody("order"), chainBinding(coverage(keyFirst[0].Chain...), "")).picked(t)
	if got.Tier != creds.TierB {
		t.Errorf("the chain lists the repository's key first and the call was served by %q (tier %s), want the key "+
			"(tier b): the chain's own order is what replaces the fixed tier order (R4)", got.Name, got.Tier)
	}

	poolFirst := routing.Table{{Match: testModel, Chain: []routing.Source{
		{Kind: routing.KindPool, Pool: pool.Name}, chainKey(),
	}}}
	s = chainFixture(t, poolFirst, pool)
	got = s.walk(t, callBody("order"), chainBinding(coverage(poolFirst[0].Chain...), "")).picked(t)
	if got.Tier != creds.TierA {
		t.Errorf("the chain lists the pool first and the call was served by %q (tier %s), want a pool member (tier a): "+
			"a pool listed first is not overtaken by the paid key behind it", got.Name, got.Tier)
	}
}

// ---------------------------------------------------------------------------
// 2. A source the repository is not admitted to is skipped
// ---------------------------------------------------------------------------

// TestChainSkipsAnUnadmittedSource: a key, a provider key or Vertex names the
// repositories it may serve (D3182), and a repository that is not in the list is
// skipped — the chain moves on to its next source, and the credential that was
// dropped is named rather than vanishing.
//
// An EMPTY admit list is the fail-closed direction and admits nobody, so it is
// the same answer as a list naming another repository: a project can publish
// the shape of a table before it decides its admissions.
func TestChainSkipsAnUnadmittedSource(t *testing.T) {
	pool := teamPool("")
	for _, tc := range []struct {
		name string
		key  routing.Source
	}{
		{"a list naming another repository", routing.Source{Kind: routing.KindAnthropic, Admit: []string{testOtherRepo}}},
		{"no list at all", routing.Source{Kind: routing.KindAnthropic}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := routing.Table{{Match: testModel, Chain: []routing.Source{
				tc.key, {Kind: routing.KindPool, Pool: pool.Name},
			}}}
			s := chainFixture(t, table, pool)
			w := s.walk(t, callBody("unadmitted"), chainBinding(coverage(table[0].Chain...), ""))
			got := w.picked(t)
			if got.Tier != creds.TierA {
				t.Errorf("the key is not admitted to %s and the call was served by %q (tier %s), want a pool member: "+
					"an unadmitted source is skipped (D3182)", testRepo, got.Name, got.Tier)
			}
			wantChainSkipped(t, w.plan, ReasonNotAdmitted)
		})
	}
}

// wantSkipped holds a plan to having dropped exactly one credential, with this
// reason, and to having named it.
func wantChainSkipped(t *testing.T, plan *chainPlan, reason string) {
	t.Helper()
	if len(plan.skipped) != 1 {
		t.Fatalf("the chain dropped %d credentials, want one: %+v", len(plan.skipped), plan.skipped)
	}
	tr := plan.skipped[0]
	if tr.Reason != reason {
		t.Errorf("the dropped credential %q is recorded as %q, want %q: the reasons are the contract's own closed set",
			tr.Name, tr.Reason, reason)
	}
	if tr.Name == "" || !ValidReason(tr.Reason) {
		t.Errorf("the dropped credential is %+v, want a name and one of the contract's four reasons", tr)
	}
}

// ---------------------------------------------------------------------------
// 3. A pool member the repository is not admitted to is skipped
// ---------------------------------------------------------------------------

// TestChainSkipsAMemberThatDoesNotAdmitTheRepository: a pool carries no admit
// list of its own (C1's Source says so) — its admission is its MEMBERS' own
// (R20, D3182). Two repositories admitted to two different accounts can share
// one pool and neither moves, and a member the repository is not admitted to is
// skipped for it exactly as a key is.
func TestChainSkipsAMemberThatDoesNotAdmitTheRepository(t *testing.T) {
	pool := teamPool("")
	table := routing.Table{{Match: testModel, Chain: []routing.Source{{Kind: routing.KindPool, Pool: pool.Name}}}}
	s := chainFixture(t, table, pool)
	// The repository's own node names ONE account, so one of the pool's two
	// members is admitted to it and the other is not.
	s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(testPoolCredential))

	w := s.walk(t, callBody("member"), chainBinding(coverage(table[0].Chain...), ""))
	got := w.picked(t)
	if got.Name != testPoolCredential {
		t.Errorf("the call was served by %q, want %q: the pool's other member is not admitted to %s (R20, D3182)",
			got.Name, testPoolCredential, testRepo)
	}
	wantChainSkipped(t, w.plan, ReasonNotAdmitted)
}

// ---------------------------------------------------------------------------
// 4. The chain is walked again at each step
// ---------------------------------------------------------------------------

// TestChainIsWalkedAgainEachStep: a placement is sticky for one step and the
// chain is walked from its FIRST source at the next (R16, which amends D154). A
// run moved onto the chain's second source by an exhaustion does not stay there
// once the first source can serve again.
//
// The test is the whole arc: the first call goes to the chain's first source;
// that credential exhausted, the second call goes to the chain's second source;
// the exhaustion cleared, the third call is on the FIRST source again — which is
// the fact a run-level stickiness would not give, because the placement the
// second call wrote names the pool's member by then.
func TestChainIsWalkedAgainEachStep(t *testing.T) {
	pool := teamPool("")
	table := routing.Table{{Match: testModel, Chain: []routing.Source{
		chainKey(), {Kind: routing.KindPool, Pool: pool.Name},
	}}}
	s := chainFixture(t, table, pool)
	slip := chainBinding(coverage(table[0].Chain...), "")

	first := s.walk(t, callBody("step"), slip).picked(t)
	if first.Tier != creds.TierB {
		t.Fatalf("the first call was served by %q (tier %s), want the chain's first source, the repository's own key",
			first.Name, first.Tier)
	}

	// The first source is exhausted well past its reset, so the next call is
	// the chain's second source and not a wait on the first.
	mark := func(state creds.State, until time.Time) {
		t.Helper()
		if _, err := s.state.UpdateMark(context.Background(), first.Name,
			func(creds.Mark, bool) (creds.Mark, error) {
				return creds.Mark{State: state, Until: until}, nil
			}); err != nil {
			t.Fatalf("writing the first source's mark: %v", err)
		}
	}
	mark(creds.StateExhausted, s.now.Add(time.Hour))

	second := s.walk(t, callBody("step"), slip).picked(t)
	if second.Tier != creds.TierA {
		t.Fatalf("the second call was served by %q (tier %s), want a pool member: the chain's first source is exhausted",
			second.Name, second.Tier)
	}

	// The exhaustion lifted, the chain is walked from its first source again,
	// whatever placement the call before it wrote (R16).
	mark("", time.Time{})
	if got, _ := s.state.placed(s.credsRun()); got != second.Name {
		t.Fatalf("the fixture's placement is %q, want %q: this test needs the run to be ON the second source for the "+
			"third call to prove anything", got, second.Name)
	}
	third := s.walk(t, callBody("step"), slip).picked(t)
	if third.Name != first.Name {
		t.Errorf("the third call was served by %q, want %q: the chain is walked from its first source at every step, "+
			"and it is not held on the placement the call before it wrote (R16)", third.Name, first.Name)
	}
}

// ---------------------------------------------------------------------------
// 4b. A retry answers the reset of the source it has spent
// ---------------------------------------------------------------------------

// TestARetryAnswersTheResetTheSpentSourceHolds: a retry after a 401 or a usage
// limit is a fresh walk that may not send again to a credential this call has
// already used (forward's sent set) — and a chain whose every source is spent is
// NOT a chain that serves nothing. The spent sources are judged once more, so
// the reset a mark holds is the call's answer.
//
// This is the difference between a run that waits at a usage limit and one that
// halts at every limit: a walk that answered "nothing usable" here is refused as
// state_unavailable, "none becomes usable by waiting", where the mark holds an
// instant the run could wait out. It is retryPool's rule on the cascade's own
// path, and it must hold for both kinds of source, because the two are judged by
// two different rules: a single-credential source by the selector's judgement, a
// pool by the rule its reference carries.
func TestARetryAnswersTheResetTheSpentSourceHolds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chain []routing.Source
		pools []config.Pool
	}{
		{"a key", []routing.Source{chainKey()}, nil},
		{"a pool", []routing.Source{{Kind: routing.KindPool, Pool: slipTestPoolName}},
			[]config.Pool{{Name: slipTestPoolName, Owner: testLauncherAccount, Members: []string{testPoolCredential}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			table := routing.Table{{Match: testModel, Chain: tc.chain}}
			s := chainFixture(t, table, tc.pools...)
			slip := chainBinding(coverage(tc.chain...), "")

			served := s.walk(t, callBody("retry"), slip).picked(t)
			mark := func(state creds.State, until time.Time) {
				t.Helper()
				if _, err := s.state.UpdateMark(ctx, served.Name,
					func(creds.Mark, bool) (creds.Mark, error) {
						return creds.Mark{State: state, Until: until}, nil
					}); err != nil {
					t.Fatalf("writing the served credential's mark: %v", err)
				}
			}
			reset := s.now.Add(time.Hour)
			mark(creds.StateExhausted, reset)

			// The retry. The only credential this chain could serve with has
			// been used and its mark now holds a reset, so the answer is that
			// reset — and never a second attempt at the credential.
			spent := map[string]bool{served.Name: true}
			c, wait, err := s.walk(t, callBody("retry"), slip).next(ctx, spent)
			var until *creds.ExhaustedUntil
			if !errors.As(err, &until) {
				t.Fatalf("the retry answered %q / %v, want the reset the spent source holds: a chain this call has "+
					"spent is not a chain that serves nothing, and a run at a usage limit waits the reset out "+
					"instead of halting", c.Name, err)
			}
			if !until.At.Equal(reset) || wait != time.Hour {
				t.Errorf("the retry answers the reset %v (wait %v), want %v (%v)", until.At, wait, reset, time.Hour)
			}

			// The same source usable again and still this call's own: it is
			// never served a second time, so the chain is spent with nothing to
			// wait for rather than a credential handed back.
			mark("", time.Time{})
			c, _, err = s.walk(t, callBody("retry"), slip).next(ctx, spent)
			if !errors.Is(err, creds.ErrNoCredential) {
				t.Errorf("the retry answered %q / %v, want a selection refusal: this call has already used the only "+
					"credential the chain can serve with, and a credential is never sent to twice", c.Name, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. The answer names the source and whether it fell back
// ---------------------------------------------------------------------------

// TestAnswerNamesTheSourceAndFallback: the answer carries which source served,
// whether it was the chain's first, and the model that served (C1's three
// members) — and only to a caller that sent the stage header.
func TestAnswerNamesTheSourceAndFallback(t *testing.T) {
	stage := func(r *http.Request) { r.Header.Set(HeaderStage, "coder") }

	t.Run("a source that fell back to", func(t *testing.T) {
		// The chain's first source is a pool whose only listed member the
		// bundle does not hold, so the walk has nothing to serve it with and
		// moves on (D3180) — the shape a run meets when its launcher's pool has
		// nothing usable in it.
		pool := config.Pool{Name: slipTestPoolName, Owner: testLauncherAccount, Members: []string{chainUnheld}}
		table := routing.Table{{Match: testModel, Chain: []routing.Source{
			{Kind: routing.KindPool, Pool: pool.Name}, chainKey(),
		}}}
		s := chainFixture(t, table, pool)

		resp, body := s.call(t, callBody("answer"), stage)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want 200: %s", resp.StatusCode, body)
		}
		var name routing.SourceName
		if err := json.Unmarshal([]byte(resp.Header.Get(HeaderSource)), &name); err != nil {
			t.Fatalf("the answer's source member %q is not a routing.SourceName: %v", resp.Header.Get(HeaderSource), err)
		}
		if name.Kind != routing.KindAnthropic {
			t.Errorf("the answer names the source %+v, want the repository's key: the chain's first source has no member "+
				"this service holds and is skipped", name)
		}
		if got := resp.Header.Get(HeaderFallback); got != FallbackYes {
			t.Errorf("the answer says fallback=%q, want %q: the call was served by the chain's SECOND source",
				got, FallbackYes)
		}
		if got := resp.Header.Get(HeaderModel); got != testModel {
			t.Errorf("the answer names the model %q, want %q", got, testModel)
		}
	})

	t.Run("the chain's first source", func(t *testing.T) {
		table := routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}}
		s := chainFixture(t, table)
		resp, body := s.call(t, callBody("first"), stage)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderFallback); got != FallbackNo {
			t.Errorf("the answer says fallback=%q, want %q: the call was served by the chain's FIRST source", got, FallbackNo)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. A body that names no model is refused
// ---------------------------------------------------------------------------

// TestACallWithNoModelInTheBodyIsRefused: the model a call is routed by is the
// one in its BODY, and no header is trusted for it (C1's second rule). A body
// that names none is a request refusal — nothing was read and no credential was
// chosen — and a model in a header does not save it.
func TestACallWithNoModelInTheBodyIsRefused(t *testing.T) {
	// The entry's pattern is the empty model itself, and that is the model a
	// body that names none resolves to — so this entry would SERVE such a call,
	// and only the walk's own refusal keeps it from being served. A table of
	// real model IDs would refuse the same call by the resolver's own "no
	// entry" answer (R22), and the walk's rule would never be the thing that
	// answered it.
	table := routing.Table{{Match: "", Chain: []routing.Source{chainKey()}}}
	// Both paths the walk serves, and the token count is the one that holds this
	// rule to the walk's own door: the gateway's request check refuses a
	// Messages body with no model before the chain is read at all
	// (server.go), so on that path this refusal is not the walk's and the walk's
	// own would be untested. The token count's body the gateway does not hold to
	// that check, so it reaches the walk and only the walk refuses it.
	for _, path := range []string{MessagesPath, CountTokensPath} {
		t.Run(path, func(t *testing.T) {
			s := chainFixture(t, table)
			body := `{"max_tokens":16,"messages":[{"role":"user","content":"no model"}]}`
			resp, answer := s.callPath(t, path, body, func(r *http.Request) {
				r.Header.Set(HeaderModel, testModel)
				r.Header.Set(HeaderStage, "coder")
			})
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the call is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
			}
			if e := refusal(t, resp.StatusCode, answer); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorRequestRefused)
			}
			if sent := s.up.Seen(); len(sent) != 0 {
				t.Errorf("the upstream saw %d requests, want none: a call that names no model is refused before "+
					"anything is sent", len(sent))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. A source the grant does not cover is never served
// ---------------------------------------------------------------------------

// TestASourceTheGrantDoesNotCoverIsNeverServed: a grant records the sources one
// run may be served from (CS14), and a source outside it is never served — not
// skipped-and-then-fallen-back-to, never served at all. It is the fence around a
// launcher's own reading of the table: the table may be wider than this run was
// granted.
func TestASourceTheGrantDoesNotCoverIsNeverServed(t *testing.T) {
	pool := teamPool("")
	key := chainKey()
	table := routing.Table{{Match: testModel, Chain: []routing.Source{{Kind: routing.KindPool, Pool: pool.Name}, key}}}
	s := chainFixture(t, table, pool)
	// The grant covers the KEY alone: the pool's members are not this run's,
	// and the pool is listed FIRST — so a walk that fell back onto it would
	// answer the pool's member and this test would catch it.
	got := s.walk(t, callBody("grant"), chainBinding(coverage(key), "")).picked(t)
	if got.Tier != creds.TierB {
		t.Errorf("the call was served by %q (tier %s), want the repository's key: the pool is not a source this run's "+
			"grant covers, and an uncovered source is never served (CS14)", got.Name, got.Tier)
	}
}

// ---------------------------------------------------------------------------
// 8. A release-behind gateway meets the answer it meets today
// ---------------------------------------------------------------------------

// TestAReleaseBehindGatewayMeetsTheOldAnswer: a gateway built before this
// contract sends no stage header, and it decodes an answer it does not know
// strictly — so the three members this contract adds are written only to a
// caller that sent the stage header (R24, F57), and a caller that sent none
// meets exactly the answer it meets today.
func TestAReleaseBehindGatewayMeetsTheOldAnswer(t *testing.T) {
	served := &chainServed{source: routing.SourceName{Kind: routing.KindAnthropic}, model: testModel}

	old := &call{body: []byte(callBody("legacy")), header: http.Header{}}
	hdr := http.Header{}
	chainAnswer(hdr, old, served)
	for _, h := range []string{HeaderSource, HeaderFallback, HeaderModel} {
		if got := hdr.Get(h); got != "" {
			t.Errorf("a caller that sent no stage header reads %s=%q, want nothing: a release-behind gateway must meet "+
				"exactly the answer it meets today", h, got)
		}
	}

	// And the same members ARE written to a caller that did send it, which is
	// what keeps the rule above from being "write nothing at all". The flag is
	// what readCall keeps of the stage header — it strips every X-Fugaro-
	// member before the call travels, so the header itself is never here.
	stage := &call{body: []byte(callBody("legacy")), header: http.Header{}, stage: true}
	hdr = http.Header{}
	chainAnswer(hdr, stage, served)
	if hdr.Get(HeaderSource) == "" || hdr.Get(HeaderFallback) == "" || hdr.Get(HeaderModel) == "" {
		t.Errorf("a caller that sent the stage header reads %v, want the source, the fallback flag and the model", hdr)
	}

	// And a served source the answer cannot name a model for writes NOTHING
	// rather than the two members it has: the reader takes all three as the
	// source, the flag and the model, none as the pre-contract shape and PART
	// of them as an error it halts the run on (the gateway's
	// chainAnswerHeaders), so half an answer is worse than no answer.
	hdr = http.Header{}
	chainAnswer(hdr, stage, &chainServed{source: routing.SourceName{Kind: routing.KindAnthropic}})
	for _, h := range []string{HeaderSource, HeaderFallback, HeaderModel} {
		if got := hdr.Get(h); got != "" {
			t.Errorf("a served source with no model writes %s=%q, want nothing: a caller that can read part of this "+
				"contract's answer halts the run on it", h, got)
		}
	}

	// And end to end: a project that publishes no table is untouched by this
	// contract, header or no header.
	noTable := newSvc(t)
	resp, body := noTable.call(t, callBody("legacy"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a project with no table answers %d, want 200: %s", resp.StatusCode, body)
	}
	for _, h := range []string{HeaderSource, HeaderFallback, HeaderModel} {
		if got := resp.Header.Get(h); got != "" {
			t.Errorf("a project that publishes no table answers %s=%q, want nothing", h, got)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. An older launcher's grant is not walked by the table
// ---------------------------------------------------------------------------

// TestAnOlderLaunchersGrantIsNotWalkedByTheTable: a grant that covers no source
// of the new kinds is what a launcher built before the table sends (CS14's
// `sources` is empty there, and an empty list covers NOTHING — never
// everything). Such a run is the release-1 bridge's business and not the walk's,
// and with the bridge this release ships answering "no record" for everything it
// is refused with the fix rather than served by whatever a record happened to
// hold.
func TestAnOlderLaunchersGrantIsNotWalkedByTheTable(t *testing.T) {
	table := routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}}
	s := chainFixture(t, table)
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("loading the fixture's bundle: %v", err)
	}
	cl := &call{body: []byte(callBody("legacy-grant")), header: http.Header{}}
	id := Identity{Slug: s.slug, Run: s.run, Project: testProject}
	_, byChain, err := s.server.chainFor(context.Background(), cl, id, testRepo, s.policy.shared, b, chainBinding(nil, ""))
	if !byChain {
		t.Fatal("an older launcher's grant was not decided by the chain path at all: it covers no source of the table's kinds")
	}
	var cr *chainRefusal
	if !errors.As(err, &cr) {
		t.Fatalf("the call answered %v, want a refusal naming the fix: an empty grant is not a grant that covers everything", err)
	}
	if cr.status != StatusUnavailable || cr.typ != ErrorStateUnavailable {
		t.Errorf("the refusal is %d/%s, want %d/%s", cr.status, cr.typ, StatusUnavailable, ErrorStateUnavailable)
	}
}

// ---------------------------------------------------------------------------
// 10. The run the bridge answers for is served by the bridge's record
// ---------------------------------------------------------------------------

// fakeLegacyBridge is the release-1 bridge with a record behind it, which the
// stub this release ships never answers (chain_stub.go): MG3 owns the real one,
// and what this test is about is the walk's reading of an answer, not the read.
type fakeLegacyBridge struct {
	sources []routing.Source
	found   bool
}

func (f fakeLegacyBridge) OldSources(string) ([]routing.Source, bool) { return f.sources, f.found }

// TestABridgedRecordIsServed: a run whose grant covers nothing of the table's
// kinds — a launcher built before the table — is served by the bridge's own
// record, and NOT refused. The grant is what the bridge stands in for here, so
// reading the bridge's sources against it would drop every source the bridge
// just named and refuse a call the record says how to serve; the release-1
// record IS that release's answer about what the repository's runs go out on.
//
// The launcher's pool is the one kind a bridged record does not carry: its
// whole content is the designation THIS run's grant holds (C2), which a record
// read for a run it did not launch cannot answer, so it stays excluded.
func TestABridgedRecordIsServed(t *testing.T) {
	table := routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}}
	s := chainFixture(t, table)

	bridge := func(t *testing.T, sources ...routing.Source) {
		t.Helper()
		prev := LegacyChain
		LegacyChain = fakeLegacyBridge{sources: sources, found: true}
		t.Cleanup(func() { LegacyChain = prev })
	}

	t.Run("a bridged key is served", func(t *testing.T) {
		bridge(t, chainKey())
		w := s.walk(t, callBody("bridged"), chainBinding(nil, ""))
		if len(w.plan.sources) != 1 {
			t.Fatalf("the bridge's record names one key and the plan holds %d sources: a grant that covers nothing "+
				"of the table's kinds is the REASON the bridge is read, and it must not filter the bridge's own answer",
				len(w.plan.sources))
		}
		got := w.picked(t)
		if got.Tier != creds.TierB {
			t.Errorf("the bridged call was served by %q (tier %s), want the repository's own key (tier b)",
				got.Name, got.Tier)
		}
		// The model is read from the body on this path too, so the answer can
		// name all three of its members: a bridged call with no model in its
		// plan is a call whose answer carries two of the three, which the
		// gateway halts the run on rather than reading.
		if w.plan.model != testModel {
			t.Errorf("the bridged plan carries the model %q, want %q: the model is read from the call's body whichever "+
				"record its sources come from", w.plan.model, testModel)
		}
		hdr := http.Header{}
		chainAnswer(hdr, &call{body: []byte(callBody("bridged")), header: http.Header{}, stage: true}, w.served)
		for _, h := range []string{HeaderSource, HeaderFallback, HeaderModel} {
			if hdr.Get(h) == "" {
				t.Errorf("a bridged call's answer carries %s=%q, want all three members: a partial answer is a halt at "+
					"the gateway", h, hdr.Get(h))
			}
		}
	})

	t.Run("a bridged launcher's pool is not", func(t *testing.T) {
		bridge(t, routing.Source{Kind: routing.KindLauncherPool, Pool: slipTestPoolName})
		w := s.walk(t, callBody("bridged-pool"), chainBinding(nil, ""))
		if len(w.plan.sources) != 0 {
			t.Errorf("the bridged record names a launcher's pool and the plan holds %d sources, want none: the "+
				"launcher's pool is the run's own designation (C2) and is never read out of an old record",
				len(w.plan.sources))
		}
	})
}

// ---------------------------------------------------------------------------
// 11. A probe on a chain-served call asks the model the probed source serves
// ---------------------------------------------------------------------------

// TestAProbeAsksTheModelTheProbedSourceServes: the walk rewrites the call's
// `model` member to each source's spelling on its way out and leaves the SERVED
// source's there (chainWalk.next), so a body read on the retry after a 401 or a
// usage limit carries the LAST source's model. A probe is made on ONE credential
// of the chain (probeDue), and the model it asks about is the one THAT
// credential's source would serve this call with (probeModel): reading the body
// as it stands asks this credential about another source's model — a route ID or
// a Vertex date this one may not serve at all — and reading the model the run
// named asks it about an id its own route may not take. Either way the answer
// would be recorded against THIS credential: a 400 from a route handed an id it
// does not know doubles its wait and leaves it backed off.
//
// The fixture's two sources serve two different written models, which is what
// makes the readings differ at all.
func TestAProbeAsksTheModelTheProbedSourceServes(t *testing.T) {
	const (
		first  = "claude-haiku-4-5"
		second = "claude-opus-5"
	)
	key := chainKey()
	key.Model = first
	pool := routing.Source{Kind: routing.KindPool, Pool: slipTestPoolName, Model: second}
	s := chainFixture(t, routing.Table{{Match: testModel, Chain: []routing.Source{key, pool}}}, teamPool(""))

	w := s.walk(t, callBody("probe"), chainBinding(coverage(key, pool), ""))
	w.cl.path = MessagesPath
	w.picked(t)
	if left, _ := callModel(w.cl.body); left != first {
		t.Fatalf("the walk left the call's body naming %q, want the serving source's model %q: a probe is owed on the "+
			"retry AFTER a source was served, and the body it finds there is the one this test is about", left, first)
	}
	if len(w.plan.sources) != 2 {
		t.Fatalf("the fixture's chain holds %d sources, want the key and the pool", len(w.plan.sources))
	}

	for _, tc := range []struct {
		name string
		who  creds.Credential
		want string
	}{
		{"the key that served", w.plan.sources[0].members[0], first},
		{"the pool's member", w.plan.sources[1].members[0], second},
	} {
		probe, ok := probeCall(w.cl, tc.who)
		if !ok {
			t.Fatalf("%s: a call on the Messages path is a call a probe is made on", tc.name)
		}
		model, ok := callModel(probe.body)
		if !ok {
			t.Fatalf("%s: the probe's own body names no model: %s", tc.name, probe.body)
		}
		if model != tc.want {
			t.Errorf("%s: the probe asks for the model %q, want %q: a probe asks the model the credential's OWN source "+
				"serves this call with, never the model the last source left in the body", tc.name, model, tc.want)
		}
	}
}

// TestAChainProbeMayAskAnySourceItServes: which credentials a probe may be made
// on is the ORDER the call goes out on (probeCandidatesFor), so a chain-served
// call's list is the plan's own — every member of it, whatever tier it holds —
// where a call the cascade serves is filtered by the policy (probeCandidates).
//
// The difference is a dead end and not a preference. The table REPLACES the
// fixed tier order (R4, D3170), so the walk serves the project's paid key under
// `oauth-only` when the chain names it, and a probe is the only thing that clears
// a backed-off mark (creds.free, Mark.NeedsProbe): a member the policy's gate
// dropped from the probe's list would be a source this call can be served by and
// can never come back — the walk serves nothing of the chain that is left, the
// mark never clears, and every call of the step is answered the same wait.
func TestAChainProbeMayAskAnySourceItServes(t *testing.T) {
	src := chainKey()
	s := chainFixture(t, routing.Table{{Match: testModel, Chain: []routing.Source{src}}})
	w := s.walk(t, callBody("probe"), chainBinding(coverage(src), ""))
	// The policy does not spend, which is what withdraws the paid tier from the
	// cascade's own probe list. It is the fixture's own policy changed for this
	// test's two readings rather than a second document, because what the test
	// is about is a chain-served call's list differing from the policy's.
	w.sel.Settings = creds.Settings{Fallback: creds.OAuthOnly}

	paid := creds.Credential{Name: "paid", Tier: creds.TierB}
	provider := creds.Credential{Name: "provider-key", Tier: creds.TierC, Provider: "openrouter"}
	cands := []creds.Credential{{Name: "default", Tier: creds.TierA}, paid, provider}

	if got := probeCandidates(cands, w.sel.Settings, w.sel.Model, w.sel.Provider); len(got) != 1 {
		t.Fatalf("the policy's own list holds %d candidates, want the subscription one alone: this test says nothing "+
			"about the chain's list unless the two differ", len(got))
	}
	if got := probeCandidatesFor(w.cl, cands, w.sel); len(got) != len(cands) {
		t.Errorf("a chain-served call may probe %d of its %d members, want every one of them: the chain's own order is "+
			"what says which sources it serves, and a member no probe may ask about is a source that can never come back",
			len(got), len(cands))
	}
	if got := probeCandidatesFor(&call{}, cands, w.sel); len(got) != 1 {
		t.Errorf("a call the cascade serves may probe %d candidates, want the policy's own answer (%d)",
			len(got), 1)
	}
}

// ---------------------------------------------------------------------------
// 12. A chain is served out of the scope's offering and nothing wider
// ---------------------------------------------------------------------------

// TestAChainIsNarrowedByTheScopesOffering: a pool source is served from the pool
// the SCOPE offers this call (C1's OfferingReader), and out of nothing wider.
//
// For an installation that is a no-op BY CONSTRUCTION — the offering is
// b.Credentials() and a pool's members are read out of that same list
// (poolOfferingOf) — so the case this pins is the one where it is not: a
// WORKSPACE MEMBER, whose bundle is the member's own node and whose offering is
// what the workspace's administrator shared with it. A member's published table
// must never serve it a credential its own administrator did not share (R5, R8),
// and the narrowing is the scope's and not a second rule of the walk's.
//
// The pool lists two members and the scope offers ONE, so the two readings differ
// at the credential that serves: the walk must serve the second, and the first
// must be recorded as a credential this call may not be served by rather than
// vanishing.
//
// Each half builds its OWN fixture, and that is not tidiness: a serve places the
// credential it chose for the run (R16), so a second call over one fixture would
// be answered by the pool's next member whatever the offering said and both
// halves would hold for the wrong reason.
func TestAChainIsNarrowedByTheScopesOffering(t *testing.T) {
	// The member's own shared node: the pool's SECOND member alone. The fixture's
	// pool lists it second, so a walk that served the pool's first member would
	// be serving a credential this scope never offered.
	offered := poolOfferingWithout{{Name: testSecondPoolCredential, Tier: creds.TierA}}

	t.Run("the walk serves what the scope offers", func(t *testing.T) {
		pool := teamPool("")
		table := routing.Table{{Match: testModel, Chain: []routing.Source{{Kind: routing.KindPool, Pool: pool.Name}}}}
		s := chainFixture(t, table, pool)
		sc := scopeWithout{Scope: installationScope{s: s.server}, offering: offered}
		w := s.walkIn(t, withScope(context.Background(), sc), callBody("scope-offer"),
			chainBinding(coverage(table[0].Chain...), ""))
		if got := w.picked(t); got.Name != testSecondPoolCredential {
			t.Errorf("the call was served by %q, want %q: the pool lists %q first, and this scope offers the second "+
				"member alone (C1's OfferingReader)", got.Name, testSecondPoolCredential, testPoolCredential)
		}
		wantChainSkipped(t, w.plan, ReasonNotAdmitted)
	})

	t.Run("the answer's own line names what was served", func(t *testing.T) {
		// The same narrowing end to end, through the front door: what a caller
		// reads is the credential the answer's audit line names, and nothing of
		// this is visible at the seam alone.
		pool := teamPool("")
		table := routing.Table{{Match: testModel, Chain: []routing.Source{{Kind: routing.KindPool, Pool: pool.Name}}}}
		s := chainFixture(t, table, pool)
		s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, offering: offered}
		resp, body := s.call(t, callBody("scope-offer"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want 200: %s", resp.StatusCode, body)
		}
		if got := auditField(s.onlyAuditLine(t), "credential"); got != testSecondPoolCredential {
			t.Errorf("the answer's line names the credential %q, want %q: the scope's offering is the pool this call "+
				"may be served from at all", got, testSecondPoolCredential)
		}
	})
}

// ---------------------------------------------------------------------------
// 13. A review call is never served by the chain
// ---------------------------------------------------------------------------

// TestAReviewCallIsNeverServedByTheChain: the review class's candidate list is
// not this service's to narrow — the pairing has already resolved the ONE
// credential the call may be served by, and the class buys no other
// (reviewCandidates) — so a chain, which is a list this service chooses from,
// must never serve it. Those two answers cannot both be honoured: the chain's
// own order replaces the fixed tier order (R4), so a class the table served would
// be served by the chain's first usable source whatever the pairing resolved.
//
// The call is therefore answered by the path it is answered by today, and the
// table's answer about the class is CS13's to make: with the table's entry naming
// the repository's own paid key, a review call served by the chain would go out
// on that key, and it goes out on the pairing's account instead.
func TestAReviewCallIsNeverServedByTheChain(t *testing.T) {
	// The bundle holds both: the repository's own key (which the table's entry
	// names) and the account the pairing resolves.
	docs := append([]byte(config.ClaudeOAuthTokenPrefix+testAccount+": "+fakeCred(t, testAccount)+"\n"), chainBundleDoc(t)...)
	s := newReviewSvcSpelled(t, testReviewSA, testReviewSA, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{docs}
	})
	installTable(t, routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}})

	byChain := func(t *testing.T, class JobClass) (*chainPlan, bool) {
		t.Helper()
		ctx := context.Background()
		b, err := s.server.cfg.Bundles.Load(ctx)
		if err != nil {
			t.Fatalf("loading the fixture's bundle: %v", err)
		}
		cl := &call{body: []byte(callBody("review-chain")), header: http.Header{}}
		id := Identity{Slug: s.slug, Run: s.run, Project: testProject, JobAccount: testReviewSA, JobClass: class}
		plan, served, err := s.server.chainFor(ctx, cl, id, testRepo, s.policy.shared, b, nil)
		if err != nil {
			t.Fatalf("resolving the fixture's chain as %q: %v", class, err)
		}
		return plan, served
	}

	t.Run("the walk answers not-by-chain for the class", func(t *testing.T) {
		if plan, served := byChain(t, ClassReview); served || plan != nil {
			t.Errorf("a review call is served by the chain (%v, plan %+v), want the path it is served by today: the "+
				"class's candidate list is the pairing's one credential and a chain is a list this service chooses from",
				served, plan)
		}
		// And the same identity, one class over, IS served by the chain — so the
		// answer above is the class's and not the fixture's table being unreadable.
		plan, served := byChain(t, ClassRepository)
		if !served || plan == nil || len(plan.sources) != 1 {
			t.Fatalf("a repository call is served by %v sources (byChain %v), want the table's one: the fixture must "+
				"publish a table this call would be served by", len(plan.sources), served)
		}
	})

	t.Run("the call goes out on the pairing's account", func(t *testing.T) {
		resp, body := s.callTokens(t, callBody("review-chain"), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the review call is %d, want 200: %s", resp.StatusCode, body)
		}
		got := auditField(s.onlyAuditLine(t), "credential")
		if got != testReviewAccount {
			t.Errorf("the review call was served by %q, want the pairing's account %q: the chain's entry names the "+
				"repository's own key, and the class is never served by it", got, testReviewAccount)
		}
		if repoKey := repoEntryKeyFor(t, "github", testRepoPath, config.AnthropicKeyAccount); got == repoKey {
			t.Errorf("the review call was served by the repository's own entry %q", repoKey)
		}
	})
}

// ---------------------------------------------------------------------------
// 14. A run with no slip is served the project's sources and no launcher's pool
// ---------------------------------------------------------------------------

// fakeLauncherPools is a launcher's pool lookup that designates one pool for
// every principal and every name. PL4 lands the real one, which resolves from the
// principal the run's slip carries and the pool a launch named with `--pool`
// (CS12); the stub this release ships designates nothing at all (D3180), so a
// KindLauncherPool source never serves without a fake like this one.
type fakeLauncherPools struct{ pool string }

func (f fakeLauncherPools) LauncherPool(context.Context, routing.Principal, string) (routing.PoolRef, bool, error) {
	return routing.PoolRef{Pool: f.pool}, true, nil
}

// designateLauncherPool installs that fake for the duration of one test.
func designateLauncherPool(t *testing.T, pool string) {
	t.Helper()
	prev := LauncherPools
	LauncherPools = fakeLauncherPools{pool: pool}
	t.Cleanup(func() { LauncherPools = prev })
}

// TestARunWithNoSlipIsServedTheProjectsSources: a run nothing granted — an
// orchestrator's launch, or a call of a scope that never wrote a slip — is
// served the project's sources its repository is admitted to and NO launcher's
// pool, because the launcher's pool's whole content is a per-run designation
// such a run does not have (grantCovers).
//
// The chain lists the launcher's pool FIRST and the repository's own key behind
// it, and both halves are asserted: the run is served the key, and the plan it
// was served from holds no launcher's pool. The with-slip control is what makes
// that a fact about the grant rather than about the fixture's table: the same
// chain, under a slip that covers both sources, IS served the launcher's pool.
func TestARunWithNoSlipIsServedTheProjectsSources(t *testing.T) {
	pool := teamPool("")
	table := routing.Table{{Match: testModel, Chain: []routing.Source{
		{Kind: routing.KindLauncherPool}, chainKey(),
	}}}
	s := chainFixture(t, table, pool)
	designateLauncherPool(t, pool.Name)

	t.Run("no slip: the project's key serves and no launcher's pool does", func(t *testing.T) {
		w := s.walk(t, callBody("no-slip"), nil)
		if len(w.plan.sources) != 1 {
			t.Fatalf("a run with no slip is served by %d sources, want the chain's key alone: a launcher's pool is the "+
				"designation THIS run's grant holds (C2), and a run with no slip holds none", len(w.plan.sources))
		}
		if got := w.picked(t); got.Tier != creds.TierB {
			t.Errorf("the call was served by %q (tier %s), want the repository's key (tier b): the launcher's pool is "+
				"listed first and is not this run's to be served from", got.Name, got.Tier)
		}
	})

	t.Run("a slip covering both is served the launcher's pool", func(t *testing.T) {
		w := s.walk(t, callBody("no-slip"), chainBinding(coverage(table[0].Chain...), pool.Name))
		if len(w.plan.sources) != 2 {
			t.Fatalf("the same chain under a slip that covers both sources is served by %d sources, want both: this is "+
				"the control that makes the answer above the grant's", len(w.plan.sources))
		}
		if got := w.picked(t); got.Tier != creds.TierA {
			t.Errorf("the call was served by %q (tier %s), want a pool member (tier a)", got.Name, got.Tier)
		}
	})

	// The rule itself, at the seam the walk cannot reach: a nil binding is ALSO
	// caught by the walk's own "no designation" check (planSource), so a
	// grantCovers that covered the launcher's pool for a nil grant would pass
	// every assertion above and still be wrong — the bridge's own reading of a
	// record passes a nil grant on purpose (chainFor), and it is this function
	// that keeps a launcher's pool out of it.
	t.Run("a nil grant covers every kind but the launcher's pool", func(t *testing.T) {
		for _, tc := range []struct {
			kind routing.Kind
			want bool
		}{
			{routing.KindLauncherPool, false},
			{routing.KindPool, true},
			{routing.KindAnthropic, true},
			{routing.KindProvider, true},
			{routing.KindVertex, true},
		} {
			if got := grantCovers(nil, routing.Source{Kind: tc.kind}); got != tc.want {
				t.Errorf("grantCovers(nil, %s) = %v, want %v: a run with no slip is served the project's sources its "+
					"repository is admitted to, and the launcher's pool is the one kind it does not have", tc.kind, got, tc.want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 15. Every chain refusal writes one audit line and no value
// ---------------------------------------------------------------------------

// TestEveryChainRefusalWritesOneLineAndNoValue: a refusal of the chain path is
// audited by the one path that audits refusals — exactly one line, never zero and
// never two — and no line, no log line and no sentence of the refusal carries a
// credential's value.
//
// The four refusals this path can write before a credential is touched are the
// four here, and each is driven through the front door rather than at the seam,
// because a line is what a call writes and not what a function returns: a body
// naming no model, a table with no entry for the model the body names (R22), a
// release-behind grant whose bridge holds no record, and a table the resolver
// refuses to read. Each names the fields of the contract with no credential and
// no tier, because none was read.
func TestEveryChainRefusalWritesOneLineAndNoValue(t *testing.T) {
	const noModel = `{"max_tokens":16,"messages":[{"role":"user","content":"no model"}]}`

	for _, tc := range []struct {
		name   string
		status int
		typ    string
		// want is a phrase the refusal's own sentence must carry, for the row
		// whose status and type another refusal of the same path shares: the
		// bridge's refusal is a 503 like the forward path's, so without this the
		// row would hold for the wrong one.
		want string
		// call makes the call and answers the fixture, the answer, and how many
		// audit lines the fixture had already written when the call was made (a
		// grant and a claim write their own).
		call func(t *testing.T) (*svc, *http.Response, []byte, int)
	}{
		{
			name:   "the body names no model",
			status: StatusRequestRefused,
			typ:    ErrorRequestRefused,
			want:   "names none",
			call: func(t *testing.T) (*svc, *http.Response, []byte, int) {
				s := chainFixture(t, routing.Table{{Match: "", Chain: []routing.Source{chainKey()}}})
				resp, answer := s.callPath(t, CountTokensPath, noModel)
				return s, resp, answer, 0
			},
		},
		{
			name:   "the table has no entry for the model",
			status: StatusRequestRefused,
			typ:    ErrorRequestRefused,
			want:   "holds no entry for the model",
			call: func(t *testing.T) (*svc, *http.Response, []byte, int) {
				s := chainFixture(t, routing.Table{{Match: "claude-nothing-*", Chain: []routing.Source{chainKey()}}})
				resp, answer := s.call(t, callBody("no-entry"))
				return s, resp, answer, 0
			},
		},
		{
			// The bridge is reached by a call whose binding CARRIES the grant
			// record, and on the installation's own path it does not: only the
			// workspace's reading hands the record to the binding
			// (verifyPersonCallSlip, server.go), because the personal reader is
			// the one reader that is handed it (slipBinding.slip). A record
			// with no sources is therefore driven here through the workspace's
			// own slip — the grant shape that carries none at all (C1's
			// PersonGrantRequest) — and the installation's slip-less call is
			// the case the walk is served by the project's sources instead
			// (TestARunWithNoSlipIsServedTheProjectsSources).
			name:   "a release-behind grant whose bridge holds no record",
			status: StatusUnavailable,
			typ:    ErrorStateUnavailable,
			want:   "predates the table",
			call: func(t *testing.T) (*svc, *http.Response, []byte, int) {
				p := newWSPerson(t)
				personalSwitch(t, true, nil)
				tbl := routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}}
				s := p.service(t, func(*svc, *Config) { installTable(t, tbl) })
				granted := p.granted(t, s, p.grantBody(t, wsAccountA, creds.PersonTokenName))
				secret := p.claimed(t, s, granted)
				before := len(s.auditLines(t))
				resp, answer := p.personCall(t, s, granted, secret, callBody("bridged-no-record"))
				return s, resp, answer, before
			},
		},
		{
			name:   "the table cannot be read",
			status: StatusUnavailable,
			typ:    ErrorStateUnavailable,
			want:   "the call's routing table could not be read",
			call: func(t *testing.T) (*svc, *http.Response, []byte, int) {
				// The winning pattern written twice: the resolver refuses to pick
				// between two entries a project wrote for one pattern (D3172).
				entry := routing.Table{{Match: testModel, Chain: []routing.Source{chainKey()}}}
				s := chainFixture(t, append(entry, entry...))
				resp, answer := s.call(t, callBody("two-entries"))
				return s, resp, answer, 0
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, resp, answer, before := tc.call(t)
			if resp.StatusCode != tc.status {
				t.Fatalf("the call is %d, want %d: %s", resp.StatusCode, tc.status, answer)
			}
			if e := refusal(t, resp.StatusCode, answer); e.Error != tc.typ {
				t.Errorf("the refusal is %q, want %q", e.Error, tc.typ)
			}
			if tc.want != "" && !strings.Contains(string(answer), tc.want) {
				t.Errorf("the refusal's sentence does not carry %q, so this is another refusal of the same class: %s",
					tc.want, answer)
			}
			if sent := s.up.Seen(); len(sent) != 0 {
				t.Errorf("the upstream saw %d requests, want none: every one of these is refused before a credential "+
					"is touched", len(sent))
			}

			lines := s.auditLines(t)
			if len(lines) != before+1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one (the fixture had written %d before the "+
					"call): %v", len(lines)-before, before, lines)
			}
			line := lines[len(lines)-1]
			checkAuditShape(t, line)
			for _, f := range []string{"credential", "tier"} {
				if got := auditField(line, f); got != "" {
					t.Errorf("the refusal's line carries %s=%q, want nothing: no credential was read", f, got)
				}
			}

			// And no credential VALUE is anywhere the refusal reached, which is
			// what the line's credential field being empty does not yet say: the
			// sentence, the service's own log and the line itself are all read
			// here, and the fixture's bundle is what the values are.
			for _, what := range []string{config.DefaultClaudeAccount, testPoolCredential, testSecondPoolCredential, "paid"} {
				value := fakeCred(t, what)
				for name, text := range map[string]string{
					"the refusal's sentence": string(answer),
					"the service's log":      s.logs.String(),
					"the audit line":         lineJSON(t, line),
				} {
					if strings.Contains(text, value) {
						t.Errorf("%s carries the value of %q: a refusal names the field and never its content", name, what)
					}
				}
			}
		})
	}
}

// lineJSON is one audit line as the JSON the service wrote: a value that reached
// a line is in it whichever field carried it, and the parsed line's own fields
// are what the checks above read.
func lineJSON(t *testing.T, line map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("the audit line does not marshal back: %v", err)
	}
	return string(raw)
}
