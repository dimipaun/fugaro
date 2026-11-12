package credsvc

// The credentials service's pools (docs/model-crafts/spec.md §2, reconciliation
// F7, F10, F21, F22, D154, D1622, D1626, D1627, D1634; plan task MC6): which of
// the bundle's model credentials a call may be offered once the project's
// crafts go out on a pool, and what a call is answered when they leave it
// nothing to be served by.
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
	return `{
		"version": ` + strconv.Itoa(projectpolicy.Version) + `,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			` + strings.Join(entries, ",\n\t\t\t") + `
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
	return newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, poolNode(block))
		s.policy.admissions[s.slug] = decodeTestChild(t,
			testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount))
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
// neither of them, a pool that lists them beside a subscription is served by
// the subscription, and a pool whose members are all of that other path is
// refused rather than served by something the pool does not name.
func TestOpenRouterAndVertexMembersNeverReachCredsvc(t *testing.T) {
	t.Run("a mixed pool is served by its own member", func(t *testing.T) {
		s := poolSvc(t, `"credentials": {"fallback": "auto"},
			`+poolCraftBlock(
			`{"name": "`+testRouterCredential+`", "type": "openrouter"},
				{"name": "`+testAccount+`", "type": "claude-oauth"}`,
			testRouterCredential, config.CredentialVertex, testAccount)+`,
			"subscription_spread": true`)

		resp, body := s.call(t, callBody("pool-runner-members"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is %d, want the pool's subscription member to serve it: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != testAccount {
			t.Errorf("%s = %q, want %q: the OpenRouter and vertex members are the runner path's and this service holds neither", HeaderCredential, got, testAccount)
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

// providerNotSelected is the tail both of this file's clauses for a credential
// the bundle HOLDS and a craft's call may not go out on share: the craft's own
// clause ("which this service holds as a credential …") and a pool member's
// ("this service holds it as a credential …"). One rule, so one sentence, and a
// test that reads the answer off the two shapes needs the part they agree on.
const providerNotSelected = "does not select for a craft's calls"
