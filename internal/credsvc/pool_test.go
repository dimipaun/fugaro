package credsvc

// The credentials service's pools (docs/model-crafts/spec.md §2, reconciliation
// F7, F10, F21, F22, D154, D1622, D1626, D1627, D1634; plan tasks MC6 and MC19):
// which of the bundle's model credentials a call may be offered once the
// project's crafts go out on a pool, and what a call is answered when they
// leave it nothing to be served by.
//
// MC19 is the end-to-end half of the same subject and it needs no production
// change: 0.24.0 already reads the published node, so what it adds is the pin
// that what serves a call is the DOCUMENT the publish writes (MC18) — the
// node's crafts, its craft_credentials and its craft_pools' listed members —
// and, in one place, what each fact of that document decides
// (TestPublishedPoolOrderReachesTheService, and the seam
// TestRunnerPathMemberIsNotOfferedToTheService closes). The writing side of the
// same seam is internal/cli's TestPublishWritesTheCraftsFromTheConfig, which is
// where the document's shape is held.
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the policy is a decoded document,
// the state is a map, and nothing in this file is, or looks like, a credential
// (Convention 2) — every value is built at run time by fakeCred.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// testPoolAccount is the third Claude account the pool fixture's bundle
	// holds, beside the default one and testAccount's. It exists so a test can
	// tell "the member the pool names served this call" from "a bundle
	// credential the pool does not name served it": with three subscriptions
	// the pool's own member is never the one the round robin would have reached
	// for by accident.
	testPoolAccount = "bob"
	// testRouterCredential is the fixture's OpenRouter credential: a member of
	// a type the runner path serves and this service does not (D1634). It
	// carries no value here — a provider's key is mounted in the job that
	// spends it, never in this service's bundle — so the name is all a pool
	// needs to list it, and all this file has to spell.
	testRouterCredential = "acme-router"
)

// poolBundleDoc is the installation's bundle with testPoolAccount's
// subscription on it: the default account's token, alice's, bob's and the
// project's paid key.
func poolBundleDoc(t *testing.T, extra ...string) []byte {
	t.Helper()
	doc := config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n" +
		config.ClaudeOAuthTokenPrefix + testAccount + ": " + fakeCred(t, testAccount) + "\n" +
		config.ClaudeOAuthTokenPrefix + testPoolAccount + ": " + fakeCred(t, testPoolAccount) + "\n"
	// extra are further named accounts, for a test whose pool reaches past the
	// two this fixture's pool lists: a rotation is read off the members it
	// visits (roundrobin_test.go).
	for _, name := range extra {
		doc += config.ClaudeOAuthTokenPrefix + name + ": " + fakeCred(t, name) + "\n"
	}
	return []byte(doc + config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
}

// poolNode is the fixture's project node with the test's own keys written into
// it. block is everything the node holds beside the budget, the allow-list and
// the accounts: the budget's credentials block, the craft credentials, the
// pools, the crafts and the subscription switch.
//
// The pools every block here writes are legacy_open, the migration's own
// marker for a pool that existed before pools carried an owner (D1942): what
// these tests read is which of a pool's members serves a call, and such a pool
// serves everyone, where an owner and a roster would answer a question this
// file does not ask (the grant is slip_pool_test's).
//
// The three accounts are offered to both of the fixture's repositories, as
// testPolicyDoc's are, because a pool member's own admission is the same leg
// (O4) the cascade's list is narrowed by and a member this repository is not
// admitted to is a member it is not served by.
func poolNode(block string, extra ...string) string {
	// extra are further accounts the node defines, and they are defined the way
	// the three above are: a member's admission is the intersection of the
	// child's list and this block (admittedAccounts), so an account absent here
	// is an account no pool member can be served from.
	accounts := []string{config.DefaultClaudeAccount, testAccount, testPoolAccount}
	accounts = append(accounts, extra...)
	entries := make([]string, 0, len(accounts))
	for _, name := range accounts {
		entries = append(entries, `"`+name+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`)
	}
	return poolNodeAccounts(strings.Join(entries, ",\n\t\t\t"), block)
}

// poolNodeAccounts is poolNode with the accounts block written by the test, in
// the order the test wrote it. The admission's second leg is each account's own
// allow_repos (`accounts.<name>.allow_repos`, admittedAccounts), so a test whose
// subject is that leg has to be able to give one account a list of its own —
// which poolNode, whose three accounts are all offered both fixture
// repositories, cannot do.
func poolNodeAccounts(accounts, block string) string {
	return `{
		"version": ` + strconv.Itoa(projectpolicy.Version) + `,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			` + accounts + `
		},
		` + block + `
	}`
}

// poolSvc is the cascade fixture whose project node is the test's own, with the
// bundle that holds the third subscription and the repository's own node
// admitting all three accounts. The repository node is replaced rather than
// added to because the fixture's is the one the pool's members are judged
// against: a member the child does not name is not admitted, whatever the
// project's node says.
func poolSvc(t *testing.T, block string) *svc {
	t.Helper()
	return poolSvcAdmitted(t, block, config.DefaultClaudeAccount, testAccount, testPoolAccount)
}

// poolSvcAdmitted is poolSvc with the repository's own node naming the accounts
// the test gives and no other. That list is the admission's first leg — the
// repository's child under the node (projectpolicy.PathRepo, and
// RepoPolicy.Credentials.Accounts) — and a test about a member the repository
// may not be served by leaves that member out of it (F10, D1627): the pool
// still lists him, the project's node still defines him, and the child is what
// says this repository may not be served by him.
func poolSvcAdmitted(t *testing.T, block string, accounts ...string) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, poolNode(block))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(accounts...))
	})
}

// poolSvcHolding is poolSvc whose bundle holds further model credentials, for a
// test whose subject is a name the bundle holds and the node points at another
// path: Bundle.byName answers an account's name to every
// claude-oauth-token-<name> key, so a member the node calls an OpenRouter
// credential can also be a name this service could select. The three fixture
// accounts stay admitted, as poolSvc admits them.
func poolSvcHolding(t *testing.T, block string, bundle ...string) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t, bundle...)}
		s.policy.shared = decodeTestPolicy(t, poolNode(block))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount))
	})
}

// poolCraftBlock is one pool and the craft whose access names it, as a test
// writes it into a node: the credentials the members are of, the pool itself
// and the coder craft that selects it. The review craft inherits the coder, so
// one craft key is enough to put the pool under both of a coding run's
// first-craft stages.
func poolCraftBlock(credentials string, members ...string) string {
	return poolCraftBlockOf("", credentials, members...)
}

// poolCraftBlockOf is poolCraftBlock with the pool's strategy written: "" is
// the key's absence, which is D1632's failover and the shape every pool written
// before the strategy existed has, and a strategy is the owner's word for how
// the members are shared out (roundrobin_test.go).
func poolCraftBlockOf(strategy, credentials string, members ...string) string {
	return `"craft_credentials": [` + credentials + `],
		"craft_pools": [` + poolDoc(testPoolName, strategy, members...) + `],
		"crafts": {"coder": {"access": "` + testPoolName + `"}}`
}

// poolDoc is ONE pool as the node writes it — the name, the owner a published
// pool must name (D1936), the strategy when one is asked for and `members` in
// the order the owner listed them — so that a fixture whose project goes out on
// two pools of different strategies writes two of these rather than a second
// copy of the shape (roundrobin_test.go's mixture).
func poolDoc(name, strategy string, members ...string) string {
	quoted := make([]string, 0, len(members))
	for _, m := range members {
		quoted = append(quoted, `"`+m+`"`)
	}
	written := ""
	if strategy != "" {
		written = `, "strategy": "` + strategy + `"`
	}
	return `{"name": "` + name + `", "owner": "` + testPoolOwner + `"` + written + `, "members": [` + strings.Join(quoted, ", ") + `]}`
}

// testPoolName is the fixture's pool, by the name its crafts' accesses spell.
const testPoolName = "acme-pool"

// testPoolOwner is the owner a published pool must name (D1936): the node is
// the document that rule bites on, so a fixture that publishes a pool names one
// the way `fugaro accounts pool add` does.
const testPoolOwner = "owner@example.com"

// TestPoolMembersAreServedByTierOrder is MC6's red step: a pool that lists the
// API key first is still served by its OAuth member, because the TIER decides
// and the listed order only breaks a tie (F7).
//
// Both halves of the rule are read off the one call. bob serves it although the
// key is listed first (tier A before tier B), and alice — a bundle credential
// the pool does not name — never serves it at all: before this task the
// candidate list was the whole bundle, and the round robin over the two
// never-exhausted subscriptions would have answered alice, which is the answer
// this test is written against.
func TestPoolMembersAreServedByTierOrder(t *testing.T) {
	s := poolSvc(t, `"credentials": {"fallback": "auto"},
		`+poolCraftBlock(
		`{"name": "`+config.AnthropicSecret+`", "type": "anthropic"},
			{"name": "`+testPoolAccount+`", "type": "claude-oauth"}`,
		config.AnthropicSecret, testPoolAccount)+`,
		"subscription_spread": true`)

	resp, body := s.call(t, callBody("pool-tier-order"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the pool's member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
		t.Errorf("%s = %q, want %q: the pool lists the key first and its OAuth member still serves the call, and a credential the pool does not name is never offered", HeaderCredential, got, testPoolAccount)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q: the tier decides and the listed order only breaks a tie (F7)", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the list is narrowed, not walked", n)
	}
}

// TestOpenRouterAndVertexMembersNeverReachCredsvc is D1634's half of F7: an
// OpenRouter and the built-in vertex member are the runner path's — the job
// that holds their key is the one that spends it — so this service serves
// neither of them, and a pool whose members are all of that other path is
// refused rather than served by something the pool does not name.
//
// A pool that listed them BESIDE a subscription is no longer a node this
// service can be handed at all: one pool is served by one half of a run from
// MC20 (D1668), so the crossing is refused at the node's read — by this
// service's own reader, before any member is chosen — and the seam this test
// was written for is closed by that refusal rather than by poolOfferingOf
// skipping the member.
func TestOpenRouterAndVertexMembersNeverReachCredsvc(t *testing.T) {
	t.Run("a pool naming both halves is refused before this service reads it", func(t *testing.T) {
		block := `"credentials": {"fallback": "auto"},
			` + poolCraftBlock(
			`{"name": "`+testRouterCredential+`", "type": "openrouter"},
				{"name": "`+testAccount+`", "type": "claude-oauth"}`,
			testRouterCredential, config.CredentialVertex, testAccount) + `,
			"subscription_spread": true`

		p, found, err := projectpolicy.Decode([]byte(poolNode(block)))
		if err == nil || p != nil {
			t.Fatalf("the node's read = (%+v, %v), want a refusal: this service holds no OpenRouter key, and a pool's listed order can span only one half of a run", p, err)
		}
		if !found {
			t.Error("the node exists and cannot be used, so found must be true (fail closed)")
		}
		for _, want := range []string{testRouterCredential, testAccount, "name two pools, one per half"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not name %q: the operator has to be told which two members cross and what the fix is", err, want)
			}
		}
	})

	t.Run("a pool this service cannot serve is refused", func(t *testing.T) {
		s := poolSvc(t, `"credentials": {"fallback": "auto"},
			`+poolCraftBlock(
			`{"name": "`+testRouterCredential+`", "type": "openrouter"}`,
			testRouterCredential))

		resp, body := s.call(t, callBody("pool-runner-only"))
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a pool of OpenRouter members served the call: %d %s", resp.StatusCode, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if !strings.Contains(e.Message, testPoolName) || !strings.Contains(e.Message, testRouterCredential) {
			t.Errorf("the refusal %q names neither the pool nor the member this path cannot serve", e.Message)
		}
		if line := auditLines(t, s.audit); len(line) != 1 {
			t.Errorf("a refusal wrote %d audit lines, want exactly one", len(line))
		}
	})
}

// TestExhaustedMemberBackedOffAndProbed is C1.12 walked per member: a pool
// member whose back-off has run out is probed before it is used again, and the
// probe's success puts it back in the pool. MC6 changes which credentials a
// call is offered and nothing about what a mark means, so this pins that the
// back-off and the probe are the cascade's own over a pool member.
func TestExhaustedMemberBackedOffAndProbed(t *testing.T) {
	s := poolSvc(t, `"credentials": {"fallback": "auto", "stop_after_exhaustion_per_window": false, "concurrency_per_token": 0},
		`+poolCraftBlock(
		`{"name": "`+config.AnthropicSecret+`", "type": "anthropic"},
			{"name": "`+testPoolAccount+`", "type": "claude-oauth"}`,
		config.AnthropicSecret, testPoolAccount)+`,
		"subscription_spread": true`)
	s.backedOff(testPoolAccount, s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		// The probe's answer, and one no client may ever see.
		{Status: http.StatusOK, Body: `{"type":"message","probe_answer":"probe-answer"}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("pool-probe"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the probed member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
		t.Errorf("%s = %q, want %q: the member the probe cleared is the pool's tier-A member", HeaderCredential, got, testPoolAccount)
	}
	if strings.Contains(string(body), "probe-answer") {
		t.Errorf("the run was given the probe's answer: %s", body)
	}
	if m, ok := s.state.mark(testPoolAccount); !ok || !m.Available() {
		t.Errorf("a successful probe left the member's mark at %+v, want it cleared", m)
	}
	if n := s.up.Count(); n != 2 {
		t.Errorf("the upstream saw %d requests, want the probe and the run's own call", n)
	}
}

// TestReviewCraftPoolOnlyReviewClass is F21: a review caller's list is the ONE
// credential its pairing resolved, and never an entry the cascade could fall to
// — whatever pools the project's crafts name. The node here lists a review
// pool holding the pairing's own account and another subscription, so a service
// that read the review crafts' pools into the caller's list would serve the
// second call from bob, and the review class would have gained the fallback
// F13 exists to refuse it.
func TestReviewCraftPoolOnlyReviewClass(t *testing.T) {
	s := newReviewSvcSpelled(t, testReviewSA, testReviewSA, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, poolNode(`"credentials": {"fallback": "auto"},
			"craft_credentials": [
				{"name": "`+testAccount+`", "type": "claude-oauth"},
				{"name": "`+testPoolAccount+`", "type": "claude-oauth"}
			],
			"craft_pools": [{"name": "`+testPoolName+`", "owner": "`+testPoolOwner+`", "members": ["`+testAccount+`", "`+testPoolAccount+`"]}],
			"crafts": {"senior-review": {"access": "`+testPoolName+`"}, "security-review": {"access": "`+testPoolName+`"}},
			"subscription_spread": true`))
		s.policy.admissions[s.slug] = decodeTestChild(t,
			testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount))
	})

	resp, body := s.callTokens(t, callBody("review-class-pool"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review call is %d, want the credential its pairing resolved: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testAccount {
		t.Errorf("%s = %q, want %q: the review class is served by the pairing and never by a craft's pool", HeaderCredential, got, testAccount)
	}

	// The pairing's credential held, with the pool's other member free: the
	// review call waits for it and is never handed the pool member instead.
	s.hold(testAccount, s.now, s.now.Add(time.Hour))
	resp, body = s.callTokens(t, callBody("review-class-held"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("the review call was served with its pairing's credential held: %d %s", resp.StatusCode, body)
	}
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the answer is %d, want the typed wait (%d): a review caller paired with one credential has nothing to fall to: %s",
			resp.StatusCode, StatusExhaustedUntil, body)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want only the first call's: a held review credential is waited for and never stepped over", n)
	}
	for _, tr := range refusal(t, resp.StatusCode, body).Tried {
		if tr.Reason != ReasonNotAdmitted {
			t.Errorf("the credential %q is reported as %q: the cascade never fell to another credential, so none was %s or %s",
				tr.Name, tr.Reason, ReasonExhausted, ReasonBroken)
		}
	}
}

// TestSubscriptionPoolInertInCredsvc is F22's pin on this path: a pool of two
// Claude OAuth subscriptions serves nothing while the project's
// subscription_spread switch is off (D1622), so a call it is the only access
// for is refused — with the switch named as the way out — and the same node
// with the switch on serves the pool's members.
//
// The pool lists its members in the reverse of their name order, and the
// member it names first is the one asserted to serve: with the switch on, the
// two subscriptions are the same tier and neither has ever been exhausted, so
// the tie is broken by the order the pool lists them in and by nothing else
// (F7, and the sort internal/creds orders a tied set with).
func TestSubscriptionPoolInertInCredsvc(t *testing.T) {
	block := func(spread string) string {
		return `"credentials": {"fallback": "auto"},
			"craft_credentials": [
				{"name": "` + testAccount + `", "type": "claude-oauth"},
				{"name": "` + testPoolAccount + `", "type": "claude-oauth"}
			],
			"craft_pools": [{"name": "` + testPoolName + `", "owner": "` + testPoolOwner + `", "members": ["` + testPoolAccount + `", "` + testAccount + `"]}],
			"crafts": {"coder": {"access": "` + testPoolName + `"}}` + spread
	}

	s := poolSvc(t, block(""))
	resp, body := s.call(t, callBody("pool-inert"))
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a pool of two subscriptions served a call with subscription_spread off: %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, testPoolName) || !strings.Contains(e.Message, "subscription_spread") {
		t.Errorf("the refusal %q names neither the pool nor the switch that would let it serve", e.Message)
	}
	if lines := auditLines(t, s.audit); len(lines) != 1 {
		t.Errorf("a refusal wrote %d audit lines, want exactly one", len(lines))
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: an inert pool spends nothing", n)
	}

	on := poolSvc(t, block(`,
		"subscription_spread": true`))
	resp, body = on.call(t, callBody("pool-spread"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the same pool with the switch on is %d, want a served call: %s", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get(HeaderCredential), testPoolAccount; got != want {
		t.Errorf("%s = %q, want %q: the switch on makes the pool's first member serve, and a tie is broken by the order the pool lists them in", HeaderCredential, got, want)
	}
}

// migratedBlock is the marker C2's contract fixes for a migrated installation:
// the one new top-level policy key `fugaro update` publishes with the table,
// its admissions and the pools in their new shape (contract-table.md, RT4). Its
// PRESENCE is the whole of what this release reads — no table, no admission and
// no pool inside it decides anything here — which is why a test can write the
// smallest node that is a migrated one and still be writing the real shape: one
// entry whose chain names the fixture's pool, and the pools beside it.
const migratedBlock = `"model_routing": {"entries": [{"match": "*", "chain": [{"kind": "pool", "pool": "` + testPoolName + `"}]}], "pools": []}`

// poolOfTwoSubscriptions is the node's block of a project whose one craft goes
// out on a pool of two Claude OAuth subscriptions, in the reverse of their name
// order, with the spread switch written by the caller ("" is its absence).
func poolOfTwoSubscriptions(spread string) string {
	return `"credentials": {"fallback": "auto"},
		"craft_credentials": [
			{"name": "` + testAccount + `", "type": "claude-oauth"},
			{"name": "` + testPoolAccount + `", "type": "claude-oauth"}
		],
		"craft_pools": [{"name": "` + testPoolName + `", "owner": "` + testPoolOwner + `", "members": ["` + testPoolAccount + `", "` + testAccount + `"]}],
		"crafts": {"coder": {"access": "` + testPoolName + `"}}` + spread
}

// TestAPoolServesWithoutTheSpreadSwitch is D3177's pin on this path (plan task
// PL3), and its red step: once the published node carries `model_routing` — the
// marker that says the installation has been migrated — a pool of several
// Claude OAuth subscriptions serves ALL of them with the spread switch off,
// which is the one thing such a pool does not do today.
//
// Both members are read, in the pool's own order, from two runs of the project:
// the first listed member serves the first, the second serves the next, so what
// the switch's retirement buys is a pool of tokens and not one token answered
// twice. The rules that decide which of them a run goes to are internal/creds'
// own (a pool that names no rule fails over, and the counter breaks the tie the
// two never-exhausted members make) — what this test reads is that both of them
// are OFFERED at all.
func TestAPoolServesWithoutTheSpreadSwitch(t *testing.T) {
	s := poolSvc(t, poolOfTwoSubscriptions("")+`,
		`+migratedBlock)

	served := rrCalls(t, s, 2)
	if served[0] != testPoolAccount || served[1] != testAccount {
		t.Errorf("two runs of a migrated project were served by %v, want [%s %s]: the pool's members are all served, in the order the pool lists them", served, testPoolAccount, testAccount)
	}
	if n := s.up.Count(); n != 2 {
		t.Errorf("the upstream saw %d requests, want 2: one per call", n)
	}

	t.Run("a mixed pool keeps no skip either", func(t *testing.T) {
		// D3177 retires the switch for a mixed pool the same way: the
		// subscription member is no longer stepped over beside the key, so the
		// tier rule serves it — which is the answer the same node gets with the
		// switch on and the marker absent (TestMixedPoolSkipsItsSubscription…).
		s := poolSvc(t, `"credentials": {"fallback": "auto"},
			`+poolCraftBlock(
			`{"name": "`+testPoolAccount+`", "type": "claude-oauth"},
				{"name": "`+config.AnthropicSecret+`", "type": "anthropic"}`,
			testPoolAccount, config.AnthropicSecret)+`,
			`+migratedBlock)

		resp, body := s.call(t, callBody("pool-migrated-mixed"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want the pool's subscription member to serve it: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
			t.Errorf("%s = %q, want %q: a migrated project's pool spreads its subscription, and tier A serves before the key beside it", HeaderCredential, got, testPoolAccount)
		}
	})
}

// TestAnUnmigratedInstallationKeepsItsInertPool is the other half of the same
// rule, and the one that keeps this task from moving anything on its own: the
// very same node WITHOUT `model_routing` is a project `fugaro update` has not
// migrated, so its pool of several subscription tokens is inert exactly as it
// is today — refused, naming the pool and the switch — and the switch is still
// the way out of it, because the member keeps being decoded and keeps deciding
// for a node no migration has touched (PL1, R21).
//
// A project can therefore be in three states and this test reads them side by
// side: migrated (the test above, which serves), unmigrated with the switch off
// (refused with the fix named), and unmigrated with the switch on (served, as
// it always was).
func TestAnUnmigratedInstallationKeepsItsInertPool(t *testing.T) {
	block := poolOfTwoSubscriptions("")

	s := poolSvc(t, block)
	resp, body := s.call(t, callBody("pool-unmigrated"))
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a pool of two subscriptions served a call of an unmigrated project: %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	for _, want := range []string{testPoolName, "subscription_spread"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the refusal %q does not name %q: an unmigrated project is told which pool is inert and which switch would let it serve", e.Message, want)
		}
	}
	if lines := auditLines(t, s.audit); len(lines) != 1 {
		t.Errorf("a refusal wrote %d audit lines, want exactly one", len(lines))
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: an inert pool spends nothing", n)
	}

	on := poolSvc(t, poolOfTwoSubscriptions(`,
		"subscription_spread": true`))
	if served := rrCalls(t, on, 1); served[0] != testPoolAccount {
		t.Errorf("the same node with the switch on was served by %v, want %s: an unmigrated project's switch keeps deciding exactly as it did", served, testPoolAccount)
	}
}

// TestMixedPoolSkipsItsSubscriptionWhileTheSwitchIsOff is D1626 on this path: a
// pool holding ONE subscription beside another kind is not inert — it is the
// API-key fallback shape the owner asked for — and while the switch is off its
// subscription member is stepped over, so the key beside it serves.
//
// The pool lists the subscription FIRST, which is what makes the call's answer
// evidence: with the member offered, the tier rule would serve it (tier A before
// tier B, TestPoolMembersAreServedByTierOrder's own pool with the switch on),
// and the key can serve only because the member above it was skipped. The
// runner makes the same skip from its side (poolchoice.poolMember), or a mixed
// pool would be spread by whichever path answered it.
func TestMixedPoolSkipsItsSubscriptionWhileTheSwitchIsOff(t *testing.T) {
	s := poolSvc(t, `"credentials": {"fallback": "auto"},
		`+poolCraftBlock(
		`{"name": "`+testPoolAccount+`", "type": "claude-oauth"},
			{"name": "`+config.AnthropicSecret+`", "type": "anthropic"}`,
		testPoolAccount, config.AnthropicSecret))

	resp, body := s.call(t, callBody("pool-mixed-switch-off"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the pool's key member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
		t.Errorf("%s = %q, want %q: the subscription member is skipped while the switch is off, so the key beside it serves (D1626)", HeaderCredential, got, config.AnthropicSecret)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: the credential that served is the paid key", HeaderTier, got, TierB)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the skipped member is not tried", n)
	}

	t.Run("a pool whose only member is a subscription is refused, and the skip says why", func(t *testing.T) {
		s := poolSvc(t, `"credentials": {"fallback": "auto"},
			`+poolCraftBlock(
			`{"name": "`+testPoolAccount+`", "type": "claude-oauth"}`,
			testPoolAccount))

		resp, body := s.call(t, callBody("pool-one-subscription"))
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a pool of one skipped subscription served the call: %d %s", resp.StatusCode, body)
		}
		e := refusal(t, resp.StatusCode, body)
		for _, want := range []string{testPoolName, testPoolAccount, poolSkipSubscription} {
			if !strings.Contains(e.Message, want) {
				t.Errorf("the refusal %q does not name %q: the operator is told which member was skipped and that the switch would let it serve", e.Message, want)
			}
		}
		if lines := auditLines(t, s.audit); len(lines) != 1 {
			t.Errorf("a refusal wrote %d audit lines, want exactly one", len(lines))
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none: every member was skipped", n)
		}
	})
}

// TestProviderKeyIsAnsweredAsHeldAndNeverSelected is AK6b's entry against this
// file: the bundle's provider key is a credential of tier C, so a craft that
// names it is answered with what this service HOLDS it as — one it does not
// select for a craft's calls — and never with "the bundle does not hold it",
// which would send an operator to `fugaro creds set` for a credential the
// bundle is carrying (the tier is read by bundleTiers beside Credentials, which
// never offers it).
//
// Both shapes of the name are walked, because both answer it: a craft's access,
// and a pool that lists it as a member. Neither is served by it — a tier-C
// candidate would refuse the caller's whole call (checkCandidates, Check 50) —
// and the value is never rendered, which is the last arm.
func TestProviderKeyIsAnsweredAsHeldAndNeverSelected(t *testing.T) {
	providerBundle := func(t *testing.T) [][]byte {
		t.Helper()
		return [][]byte{append(poolBundleDoc(t), providerKeyDoc(t, testProviderBase, providerAuthBearer)...)}
	}
	admissions := func(s *svc) {
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount))
	}

	t.Run("a craft's access", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.src.docs = providerBundle(t)
			s.policy.shared = decodeTestPolicy(t, poolNode(`"credentials": {"fallback": "auto"},
				"craft_credentials": [{"name": "`+config.ProviderKeySecret+`", "type": "anthropic"}],
				"crafts": {"coder": {"access": "`+config.ProviderKeySecret+`"}}`))
			admissions(s)
		})

		resp, body := s.call(t, callBody("provider-key-access"))
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a craft naming the provider key served the call with it: %d %s", resp.StatusCode, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if !strings.Contains(e.Message, config.ProviderKeySecret) || !strings.Contains(e.Message, providerNotSelected) {
			t.Errorf("the refusal %q does not name %q and %q: the bundle holds the name, and this path does not select it", e.Message, config.ProviderKeySecret, providerNotSelected)
		}
		if strings.Contains(e.Message, "does not hold") {
			t.Errorf("the refusal %q says the bundle does not hold a credential the fixture's bundle carries", e.Message)
		}
		if strings.Contains(string(body), fakeCred(t, "provider-key")) {
			t.Errorf("the refusal rendered the credential's value: %s", body)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none: the provider key is never a candidate for a coding caller", n)
		}
	})

	t.Run("a pool's member", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.src.docs = providerBundle(t)
			s.policy.shared = decodeTestPolicy(t, poolNode(`"credentials": {"fallback": "auto"},
				`+poolCraftBlock(
				`{"name": "`+config.ProviderKeySecret+`", "type": "anthropic"},
					{"name": "`+config.AnthropicSecret+`", "type": "anthropic"}`,
				config.ProviderKeySecret, config.AnthropicSecret)))
			admissions(s)
		})

		resp, body := s.call(t, callBody("provider-key-member"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want the pool's other member to serve it: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
			t.Errorf("%s = %q, want %q: the probe member is listed first and is never offered (its tier is not one this path selects)", HeaderCredential, got, config.AnthropicSecret)
		}
		if got := resp.Header.Get(HeaderTier); got != TierB {
			t.Errorf("%s = %q, want %q", HeaderTier, got, TierB)
		}

		only := newSvcWith(t, func(s *svc, _ *Config) {
			s.src.docs = providerBundle(t)
			s.policy.shared = decodeTestPolicy(t, poolNode(`"credentials": {"fallback": "auto"},
				`+poolCraftBlock(
				`{"name": "`+config.ProviderKeySecret+`", "type": "anthropic"}`,
				config.ProviderKeySecret)))
			admissions(s)
		})

		resp, body = only.call(t, callBody("provider-key-only-member"))
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a pool served the call from the provider key: %d %s", resp.StatusCode, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if !strings.Contains(e.Message, providerNotSelected) {
			t.Errorf("the refusal %q does not say why the member cannot serve: this service holds it and does not select it (%q)", e.Message, providerNotSelected)
		}
		if n := only.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})
}

// TestPublishedPoolOrderReachesTheService is MC19's own pin, and the sentence
// it holds in one place: what serves a call is the pool the owner published.
// The document below is the one the publish writes (MC18's `crafts`,
// `craft_credentials` and `craft_pools`), and the two facts read off it are the
// whole of how a pool reaches this service — its members are the candidate list,
// in the order the owner listed them, and that order breaks a tie inside a tier
// and decides nothing else (F7, D154), because the tier is creds.Selector's and
// it serves tier A before tier B.
//
// The three assertions are one walk of that document. The offering carries the
// node's members in the node's own order with the bundle's tier for each name,
// which is the order creds.Select breaks a tie inside a tier with — so a reader
// that reordered them, by name say, would be re-ordering a pool its owner
// ordered deliberately. The call is served by the pool's first tier-A member
// although the paid key is listed before both of them, so the listed order does
// not outrank the tier. And with both subscriptions held the key serves, which
// is the same rule from the other end: the node's order never decides a call on
// its own.
func TestPublishedPoolOrderReachesTheService(t *testing.T) {
	s := poolSvc(t, `"credentials": {"fallback": "auto"},
		`+poolCraftBlock(
		`{"name": "`+config.AnthropicSecret+`", "type": "anthropic"},
			{"name": "`+testPoolAccount+`", "type": "claude-oauth"},
			{"name": "`+testAccount+`", "type": "claude-oauth"}`,
		config.AnthropicSecret, testPoolAccount, testAccount)+`,
		"subscription_spread": true`)
	// Two replies: this test reads the same pool twice, once with both
	// subscriptions free and once with both held.
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	b, err := ParseBundle(poolBundleDoc(t))
	if err != nil {
		t.Fatalf("the fixture's bundle is not one ParseBundle accepts: %v", err)
	}
	offer, strategy, narrowed, err := poolOffering(s.policy.shared, b)
	if err != nil {
		t.Fatalf("the published pool leaves this call nothing: %v", err)
	}
	if !narrowed {
		t.Fatal("the node's crafts did not narrow the call's candidates")
	}
	if strategy != creds.StrategyFailover {
		t.Errorf("the pool is served under %q, want the cascade's own rule: the node publishes no strategy (D1632)", strategy)
	}
	want := []creds.Credential{
		{Name: config.AnthropicSecret, Tier: creds.TierB},
		{Name: testPoolAccount, Tier: creds.TierA},
		{Name: testAccount, Tier: creds.TierA},
	}
	if len(offer) != len(want) {
		t.Fatalf("the offering holds %d candidates (%v), want the pool's three members", len(offer), offer)
	}
	for i, w := range want {
		if offer[i] != w {
			t.Errorf("candidate %d is %+v, want %+v: the order the node lists its members in is the order this service holds", i, offer[i], w)
		}
	}

	// The node lists the key first, and the pool's own first OAuth member serves:
	// the tier decides the call and the listed order only breaks a tie inside it
	// (F7). alice is listed after bob and is the name a reader that sorted its
	// candidates would have reached instead.
	resp, body := s.call(t, callBody("published-pool-order"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the pool's tier-A member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
		t.Errorf("%s = %q, want %q: the pool lists the paid key before both subscriptions and this one before the other", HeaderCredential, got, testPoolAccount)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q: the tier decides and the listed order breaks a tie inside it (F7)", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the list is narrowed to the pool, not walked", n)
	}

	// Both subscriptions held for longer than the wait threshold: the cascade
	// moves on to the paid tier and the key serves. It is the pool's last-listed
	// member, and it serves because every tier-A member is unavailable now —
	// which is the one thing a pool's listed order may never do on its own.
	s.hold(testPoolAccount, s.now, s.now.Add(time.Hour))
	s.hold(testAccount, s.now, s.now.Add(time.Hour))
	resp, body = s.call(t, callBody("published-pool-paid"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the pool's paid member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
		t.Errorf("%s = %q, want %q: with both subscriptions held the pool's remaining member serves", HeaderCredential, got, config.AnthropicSecret)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierB)
	}
}

// TestRunnerPathMemberIsNotOfferedToTheService is MC19's seam, and the reason a
// crossing pool is harmless on this path: a node may list a member of the path
// this service does not serve — an OpenRouter key, whose value is mounted in the
// job that spends it (D1634). The reader refuses such a pool now: the refusal
// that splits a crossing pool is the write door's (config.ValidatePoolPaths) and
// a published node's read's (ValidatePublishedPools, MC20), and converging a
// node an earlier release published is MC21's migration (the activation plan's
// row 43). So the crossing this test offers is rebuilt below from a node the
// reader accepts. This service is where the crossing is answered for a call, and
// it answers it by not offering the member at all: the pool's own service-path
// member serves, and the runner path's is not in the candidate list.
// internal/projectpolicy's TestTheNodeRefusesACrossPathPool is the reader's
// half, and TestOpenRouterAndVertexMembersNeverReachCredsvc is what this service
// ANSWERS such a pool (MC6).
//
// The fixture's bundle holds a model credential NAMED after the member, so that
// the member's path is the only thing that can keep it out of the offering: a
// bundle that held no such name would drop it on the bundle check instead, and
// the guard this test names would be pinned by nothing (a review round found
// exactly that of the first cut — deleting the guard left the package green).
// The name is one an account may take, which is what makes the two documents
// meet: the node points the name at the runner's path and the bundle could
// serve a call by it.
func TestRunnerPathMemberIsNotOfferedToTheService(t *testing.T) {
	if config.CredentialServingPath(config.CredentialTypeOpenRouter) != config.CredentialPathRunner {
		t.Fatalf("the fixture's OpenRouter credential is served by %q, and this test is written against it being the runner path's",
			config.CredentialServingPath(config.CredentialTypeOpenRouter))
	}
	// The reader now refuses a pool naming both halves (MC21), so the crossing
	// the old reader accepted is rebuilt from a node it accepts: both members
	// are written as subscriptions, and the router's type is changed after the
	// decode, which leaves the offering the same document the earlier release
	// published would have produced.
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t, testRouterCredential)}
		p := decodeTestPolicy(t, poolNode(`"credentials": {"fallback": "auto"},
		`+poolCraftBlock(
			`{"name": "`+testRouterCredential+`", "type": "claude-oauth"},
			{"name": "`+testPoolAccount+`", "type": "claude-oauth"}`,
			testRouterCredential, testPoolAccount)+`,
		"subscription_spread": true`))
		for i := range p.CraftCredentials {
			if p.CraftCredentials[i].Name == testRouterCredential {
				p.CraftCredentials[i].Type = config.CredentialTypeOpenRouter
			}
		}
		s.policy.shared = p
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount))
	})

	// The bundle the offering is read off is the one the service holds, the
	// member's name included: a document without it would drop the member on the
	// bundle check and this test would be pinning that instead of the path.
	b, err := ParseBundle(poolBundleDoc(t, testRouterCredential))
	if err != nil {
		t.Fatalf("the fixture's bundle is not one ParseBundle accepts: %v", err)
	}
	offer, _, _, err := poolOffering(s.policy.shared, b)
	if err != nil {
		t.Fatalf("the pool's own member serves, so the call is not refused: %v", err)
	}
	if len(offer) != 1 || offer[0].Name != testPoolAccount {
		t.Fatalf("the offering is %v, want the pool's subscription member alone: this path never offers the runner's member", offer)
	}

	resp, body := s.call(t, callBody("published-runner-member"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d, want the pool's own member to serve it: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
		t.Errorf("%s = %q, want %q", HeaderCredential, got, testPoolAccount)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the member of the other path is not tried", n)
	}
}

// providerNotSelected is the tail both of this file's clauses for a credential
// the bundle HOLDS and a craft's call may not go out on share: the craft's own
// clause ("which this service holds as a credential …") and a pool member's
// ("this service holds it as a credential …"). One rule, so one sentence, and a
// test that reads the answer off the two shapes needs the part they agree on.
const providerNotSelected = "does not select for a craft's calls"
