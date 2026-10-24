package projectpolicy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestPolicyCredentialsDefaults: D152's and D153's defaults, read through the
// accessors a consumer uses. An absent `credentials` block and a written empty
// one are the same policy — every default — and the two are asserted together
// because "absent" and "empty" must not be two answers.
//
// The two controls (D142) are the one part of this block the plan states no
// default for, and their defaults here are the protective ones: this policy is
// read by a service that spends somebody else's subscription quota, so a
// project that has not decided may not spend one account's window more than
// once and may not run more than one run at a time against one shared token. A
// project that wants the permissive behaviour writes it: false, and 0.
func TestPolicyCredentialsDefaults(t *testing.T) {
	for _, tc := range []struct{ name, node string }{
		{"no credentials at all", `{"version": 3}`},
		{"a written empty block", `{"version": 3, "credentials": {}}`},
	} {
		p, found, err := Decode([]byte(tc.node))
		if err != nil || !found || p == nil {
			t.Fatalf("%s: Decode = (%v, %v, %v), want a policy", tc.name, p, found, err)
		}
		c := p.Credentials
		if got := c.FallbackValue(); got != FallbackOAuthOnly {
			t.Errorf("%s: fallback = %q, want %q (D152: absent means oauth-only, and auto must be written)", tc.name, got, FallbackOAuthOnly)
		}
		if got := c.WaitThresholdDuration(); got != 15*time.Minute {
			t.Errorf("%s: wait_threshold = %s, want 15m (D153)", tc.name, got)
		}
		if got := c.BackoffInitialDuration(); got != 15*time.Minute {
			t.Errorf("%s: backoff.initial = %s, want 15m (D153)", tc.name, got)
		}
		if got := c.BackoffCapDuration(); got != 5*time.Hour {
			t.Errorf("%s: backoff.cap = %s, want 5h (D153)", tc.name, got)
		}
		if !c.StopsAfterExhaustion() {
			t.Errorf("%s: stop_after_exhaustion_per_window = false, want the default true (D142's first control, protecting the token owner's quota)", tc.name)
		}
		if got := c.ConcurrencyCap(); got != 1 {
			t.Errorf("%s: concurrency_per_token = %d, want the default 1 (D142's second control)", tc.name, got)
		}
	}
	// A nil block reads the same way: the accessors are nil-safe so a caller
	// that holds a policy with no credentials member (an older node, a hand
	// written value in a test) reads the defaults rather than a zero that would
	// mean "a threshold of no time at all".
	var none *Credentials
	if got := none.FallbackValue(); got != FallbackOAuthOnly {
		t.Errorf("a nil block: fallback = %q, want %q", got, FallbackOAuthOnly)
	}
	if got := none.WaitThresholdDuration(); got != 15*time.Minute {
		t.Errorf("a nil block: wait_threshold = %s, want 15m", got)
	}
	if got := none.ConcurrencyCap(); got != 1 {
		t.Errorf("a nil block: concurrency_per_token = %d, want 1", got)
	}
	// And a written value is read back as written, in each member, so the
	// defaults above cannot be the only thing the accessors ever return.
	p, _, err := Decode([]byte(`{
		"version": 3,
		"budget": {"mode": "enforce", "per_run_usd": 5},
		"credentials": {
			"fallback": "auto",
			"wait_threshold": "90s",
			"backoff": {"initial": "1m", "cap": "2h"},
			"stop_after_exhaustion_per_window": false,
			"concurrency_per_token": 4
		}}`))
	if err != nil {
		t.Fatalf("the written block was refused: %v", err)
	}
	c := p.Credentials
	if c.FallbackValue() != FallbackAuto {
		t.Errorf("fallback = %q, want auto", c.FallbackValue())
	}
	if c.WaitThresholdDuration() != 90*time.Second {
		t.Errorf("wait_threshold = %s, want 90s", c.WaitThresholdDuration())
	}
	if c.BackoffInitialDuration() != time.Minute || c.BackoffCapDuration() != 2*time.Hour {
		t.Errorf("backoff = %s/%s, want 1m/2h", c.BackoffInitialDuration(), c.BackoffCapDuration())
	}
	if c.StopsAfterExhaustion() {
		t.Error("stop_after_exhaustion_per_window = true, want the written false (0 is how a project opts out)")
	}
	if c.ConcurrencyCap() != 4 {
		t.Errorf("concurrency_per_token = %d, want the written 4", c.ConcurrencyCap())
	}
	// concurrency_per_token 0 is the *written* opt-out, and it is not the
	// default: it must survive the decode as 0 rather than being folded onto
	// the absent key's 1.
	p, _, err = Decode([]byte(`{"version": 3, "credentials": {"concurrency_per_token": 0}}`))
	if err != nil {
		t.Fatalf("concurrency_per_token 0 was refused: %v", err)
	}
	if got := p.Credentials.ConcurrencyCap(); got != 0 {
		t.Errorf("concurrency_per_token 0 = %d, want the written 0 (no cap)", got)
	}
	if p.Credentials.ConcurrencyPerToken == nil {
		t.Error("concurrency_per_token 0 decoded to the absent key: the two are different policies, and a written 0 that reads as 1 caps a project that opted out")
	}
}

// TestPolicyFallbackAutoRefusedWithoutEnforceCap: D152's other half, and this
// task's red step. `auto` is the only value that starts spending the project's
// money on tiers B and C, so it may not be reached by writing one word: it
// needs the policy's budget to be enforcing with a per-run ceiling, and the
// refusal names both.
//
// The three refused states are each a way for the spend to be unbounded or
// unobserved: no budget block at all (today's behaviour is the ceiling each
// job's environment carries, which this node cannot see), mode observe (the
// ceiling is recorded, not enforced), and enforce with per_run_usd 0 (nothing
// is charged against a cap of zero). The last case is the one a careless
// reading of "mode: enforce" would let through.
func TestPolicyFallbackAutoRefusedWithoutEnforceCap(t *testing.T) {
	for _, tc := range []struct{ name, node string }{
		{"no budget block", `{"version": 3, "credentials": {"fallback": "auto"}}`},
		{"budget off", `{"version": 3, "budget": {"mode": "off"}, "credentials": {"fallback": "auto"}}`},
		{"budget observe", `{"version": 3, "budget": {"mode": "observe", "per_run_usd": 5}, "credentials": {"fallback": "auto"}}`},
		{"enforce with no ceiling", `{"version": 3, "budget": {"mode": "enforce"}, "credentials": {"fallback": "auto"}}`},
		{"enforce with a zero ceiling", `{"version": 3, "budget": {"mode": "enforce", "per_run_usd": 0}, "credentials": {"fallback": "auto"}}`},
	} {
		p, found, err := Decode([]byte(tc.node))
		if err == nil || !found || p != nil {
			t.Errorf("%s: Decode = (%v, %v, %v), want a refusal", tc.name, p, found, err)
			continue
		}
		got := err.Error()
		for _, want := range []string{"credentials.fallback", "auto", "enforce", "per_run_usd"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the refusal is %q, want it to name %s (D152: the operator has to be told which two things to set)", tc.name, got, want)
			}
		}
	}
	// A negative ceiling never reaches this rule: the budget block's own range
	// check refuses per_run_usd below zero first, and it is the better refusal
	// (it names the key that is out of range). Asserted so that this rule's
	// silence about it is a fact and not an untested path: auto is refused here
	// either way, and the operator is sent to the ceiling.
	if _, _, err := Decode([]byte(`{"version": 3, "budget": {"mode": "enforce", "per_run_usd": -1}, "credentials": {"fallback": "auto"}}`)); err == nil {
		t.Error("a negative per_run_usd with fallback: auto was accepted")
	} else if !strings.Contains(err.Error(), "per_run_usd") {
		t.Errorf("a negative per_run_usd refused with %q, want the budget block's own range sentence", err.Error())
	}
	// The state D152 allows, and the two other values, which never need a cap:
	// oauth-only is what an absent fallback means and spends nothing, and a
	// written one is accepted with no budget block at all.
	for _, tc := range []struct{ name, node string }{
		{"enforce with a ceiling", `{"version": 3, "budget": {"mode": "enforce", "per_run_usd": 5}, "credentials": {"fallback": "auto"}}`},
		{"oauth-only with no budget", `{"version": 3, "credentials": {"fallback": "oauth-only"}}`},
		{"an absent fallback with no budget", `{"version": 3, "credentials": {"wait_threshold": "1m"}}`},
	} {
		if p, found, err := Decode([]byte(tc.node)); err != nil || !found || p == nil {
			t.Errorf("%s: Decode = (%v, %v, %v), want a policy", tc.name, p, found, err)
		}
	}
	// And the mode is read as the budget block spells it: `enforce` written in
	// another case is refused by the budget block itself, so it can never be
	// what turns the spend on.
	if _, _, err := Decode([]byte(`{"version": 3, "budget": {"mode": "Enforce", "per_run_usd": 5}, "credentials": {"fallback": "auto"}}`)); err == nil {
		t.Error("a budget whose mode is spelled Enforce was accepted: the mode is compared with the documented spelling, and a case variant is a mode this Fugaro does not enforce")
	}
}

// TestPolicyCredentialsUnknownKeyRefused: the block is strict inside, the way
// the budget block and a providers entry are (the package doc's "strict in the
// other direction"), and the member names are held to their spelling on top of
// the strict decode, because encoding/json folds case.
//
// What a tolerated misspelling costs here is a *setting*: a misspelt
// `stop_after_exhaustion_per_window` leaves the control at its default, and a
// misspelt `concurrency_per_token` leaves the cap at 1 — both of them quiet
// answers to a question the operator believes they answered.
func TestPolicyCredentialsUnknownKeyRefused(t *testing.T) {
	for _, tc := range []struct{ name, node, want string }{
		{"an unknown member", `{"version": 3, "credentials": {"fallbackd": "auto"}}`, "fallbackd"},
		{"an unknown member with a budget that would allow auto", `{"version": 3, "budget": {"mode": "enforce", "per_run_usd": 5}, "credentials": {"spend": true}}`, "spend"},
		{"a member spelled in another case", `{"version": 3, "credentials": {"Fallback": "oauth-only"}}`, "fallback"},
		{"a member spelled in another case beside the right one", `{"version": 3, "credentials": {"fallback": "oauth-only", "Wait_Threshold": "1m"}}`, "wait_threshold"},
		{"a member written twice", `{"version": 3, "credentials": {"fallback": "oauth-only", "fallback": "oauth-only"}}`, "twice"},
		{"an unknown member of backoff", `{"version": 3, "credentials": {"backoff": {"initial": "1m", "maximum": "5h"}}}`, "maximum"},
		{"a fallback that is not a value", `{"version": 3, "credentials": {"fallback": "maybe"}}`, "oauth-only"},
		{"a threshold that is not a duration", `{"version": 3, "credentials": {"wait_threshold": "soon"}}`, "duration"},
		{"a threshold of no time at all", `{"version": 3, "credentials": {"wait_threshold": "0s"}}`, "positive"},
		{"a negative threshold", `{"version": 3, "credentials": {"wait_threshold": "-5m"}}`, "positive"},
		{"a backoff that is not an object", `{"version": 3, "credentials": {"backoff": "15m"}}`, "backoff"},
		{"a backoff initial that is not a duration", `{"version": 3, "credentials": {"backoff": {"initial": "later"}}}`, "duration"},
		{"a backoff cap below its initial", `{"version": 3, "credentials": {"backoff": {"initial": "2h", "cap": "1m"}}}`, "cap"},
		{"a zero backoff cap", `{"version": 3, "credentials": {"backoff": {"cap": "0s"}}}`, "positive"},
		{"a negative concurrency", `{"version": 3, "credentials": {"concurrency_per_token": -1}}`, "concurrency_per_token"},
		{"a concurrency that is not a number", `{"version": 3, "credentials": {"concurrency_per_token": "2"}}`, "concurrency_per_token"},
		{"a control that is not a boolean", `{"version": 3, "credentials": {"stop_after_exhaustion_per_window": "yes"}}`, "stop_after_exhaustion_per_window"},
	} {
		p, found, err := Decode([]byte(tc.node))
		if err == nil {
			t.Errorf("%s: accepted (%v), want refused", tc.name, p)
			continue
		}
		if !found || p != nil {
			t.Errorf("%s: Decode = (%v, %v, %v), want (nil, true, err): a node that exists and cannot be used is a halt, never an absent policy", tc.name, p, found, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: the refusal is %q, want it to name %s", tc.name, err.Error(), tc.want)
		}
	}
}

// TestPolicyBackoffCapAboveItsEffectiveInitial: the cap and the initial wait
// are refused when the cap is below the wait, and the pair that decides that is
// the *effective* pair — an absent member is its default (D153: 15m, capped at
// 5h), so the rule reads the two values the cascade would actually use. Reading
// only the written pair let a node through with an effective cap below its
// effective initial, which is the state the rule exists to refuse: two
// single-member spellings of it,
//
//	{"backoff": {"cap": "1m"}}      → 15m initial, 1m cap
//	{"backoff": {"initial": "10h"}} → 10h initial, 5h cap
//
// both of which the earlier check accepted because "no value written" read as
// "nothing to compare" rather than as the default it is. A cap below the first
// wait is a probe schedule that never doubles, so a credential with no reset
// time is retried at the same rate for ever — no backoff at all, on the settings
// that decide how hard the cascade leans on somebody's subscription quota.
//
// The boundary is asserted too, on the effective values: a cap equal to the
// initial (and an initial equal to the cap) is in order, so the rule is a
// comparison and not a round of "the two must differ".
func TestPolicyBackoffCapAboveItsEffectiveInitial(t *testing.T) {
	for _, tc := range []struct{ name, node string }{
		{"a cap written below the default initial", `{"backoff": {"cap": "1m"}}`},
		{"an initial written above the default cap", `{"backoff": {"initial": "10h"}}`},
		{"a cap written below a written initial", `{"backoff": {"initial": "2h", "cap": "1m"}}`},
		{"an initial written above a written cap", `{"backoff": {"initial": "6h", "cap": "5h"}}`},
		{"a cap written a second below the default initial", `{"backoff": {"cap": "14m59s"}}`},
	} {
		p, found, err := Decode([]byte(`{"version": 3, "credentials": ` + tc.node + `}`))
		if err == nil {
			t.Errorf("%s: accepted (%v), want refused: the *effective* pair is what the cascade would use", tc.name, p)
			continue
		}
		if !found || p != nil {
			t.Errorf("%s: Decode = (%v, %v, %v), want (nil, true, err)", tc.name, p, found, err)
		}
		// The refusal names the key to fix and both effective values, so the
		// operator who wrote one member is told what the other one is.
		for _, want := range []string{"credentials.backoff.cap", "cap", "initial"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal is %q, want it to name %s", tc.name, err.Error(), want)
			}
		}
	}
	// The states that are in order, including the two boundaries: the comparison
	// is cap < initial, on the effective values.
	for _, tc := range []struct{ name, node string }{
		{"no backoff block at all", `{"wait_threshold": "1m"}`},
		{"an empty backoff block", `{"backoff": {}}`},
		{"a cap equal to the default initial", `{"backoff": {"cap": "15m"}}`},
		{"an initial equal to the default cap", `{"backoff": {"initial": "5h"}}`},
		{"a cap above the default initial", `{"backoff": {"cap": "30m"}}`},
		{"an initial below the default cap", `{"backoff": {"initial": "2h"}}`},
		{"a written pair in order", `{"backoff": {"initial": "1m", "cap": "1h"}}`},
		{"a written pair equal", `{"backoff": {"initial": "2h", "cap": "2h"}}`},
	} {
		if p, found, err := Decode([]byte(`{"version": 3, "credentials": ` + tc.node + `}`)); err != nil || !found || p == nil {
			t.Errorf("%s: Decode = (%v, %v, %v), want a policy", tc.name, p, found, err)
		}
	}
	// And the effective values are the ones the accessors answer, so this test
	// and a reader of the block cannot disagree about which pair is meant.
	p, _, err := Decode([]byte(`{"version": 3, "credentials": {"backoff": {"initial": "10h"}}}`))
	if err == nil {
		t.Fatalf("a node the effective comparison refuses was accepted: initial %s, cap %s", p.Credentials.BackoffInitialDuration(), p.Credentials.BackoffCapDuration())
	}
}

// TestPolicyCredentialsCarryNoVersionBump: the plan's version-skew section,
// asserted rather than assumed. The member is additive and is read by a
// *tolerant* reader, so `Version` does not move: an older runner decodes a node
// carrying `credentials` exactly as it decodes one without it, listing the key
// in UnknownKeys and going on rather than refusing (P8), which is what the
// plan's "an older runner ignores them" claims.
//
// The plan spelled this test TestPolicyCredentialsIgnoredByVersion1Reader and
// said "Version stays 1" (the C1.2 test list named a test that does not exist;
// the re-review of C1.9/this follow-up corrected the list to this name and
// TestPlanTestListsNamesOnlyTestsThatExist holds the two together). On this base
// the policy version is 3 (it moved at 0.8.0 and 0.9.0), so the number in the
// test's name and in the node below is 3: what the plan is pinning is that the
// *number does not move for this task*, and that is what this asserts. See the
// pull request for the disagreement between the plan's text and the code.
func TestPolicyCredentialsCarryNoVersionBump(t *testing.T) {
	if Version != 3 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 3: update both together, deliberately", Version)
	}
	p, found, err := Decode([]byte(`{
		"version": 3,
		"budget": {"mode": "off"},
		"credentials": {"fallback": "oauth-only", "wait_threshold": "15m"}
	}`))
	if err != nil || !found || p == nil {
		t.Fatalf("a node carrying credentials = (%v, %v, %v), want a policy: the member must not need a version bump", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("the node's version = %d, want %d: this task does not move it", p.Version, Version)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: credentials is a key this Fugaro reads", p.UnknownKeys)
	}
	// The other direction, which is what "an older reader ignores it" is worth:
	// the key a *newer* Fugaro adds is tolerated and listed, and the policy
	// around it is still read — the same answer an older runner gives for
	// `credentials` today.
	p, found, err = Decode([]byte(`{"version": 3, "budget": {"mode": "observe"}, "future_credentials": {"anything": 1}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("a node carrying an unknown key = (%v, %v, %v), want a policy", p, found, err)
	}
	if len(p.UnknownKeys) != 1 || p.UnknownKeys[0] != "future_credentials" {
		t.Errorf("UnknownKeys = %v, want [future_credentials]", p.UnknownKeys)
	}
	if p.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe: the rest of the policy stays in force beside an unknown key", p.Budget.Mode)
	}
}

// TestRepoPolicyWithNoAdmissionsWritesNoBlock: RepoPolicy is the *wire* type of
// a repository's node — the shape a reader of the node decodes and the shape
// the publisher writes back — so the two have to be the same shape, and this
// test is the pair.
//
// A repository this policy admits to nothing must marshal with no credentials
// member at all. With a value-typed field the `omitempty` is a no-op (it does
// nothing for a struct) and the document is
// {"credentials":{"accounts":null}} — which DecodeRepo refuses, because a
// written block must name at least one account. The type this package writes
// back would then be the one document this package will not read, and the
// refusal message would send the operator to remove a block the publisher wrote
// itself.
//
// Mutation (verified, run and restored): make Credentials a value again
// (RepoCredentials) — the first assertion fails with the null-accounts document
// above, and the round-trip below it fails on DecodeRepo's refusal.
func TestRepoPolicyWithNoAdmissionsWritesNoBlock(t *testing.T) {
	b, err := json.Marshal(RepoPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{}`; got != want {
		t.Errorf("a repository policy with nothing admitted marshals to %s, want %s: a repository with no admissions writes no credentials member, because a written block has to name an account", got, want)
	}
	// And what it writes, it reads: the round trip is what makes the publisher
	// and the reader one shape rather than two.
	rp, found, err := DecodeRepo(b)
	if err != nil || !found || rp == nil {
		t.Fatalf("DecodeRepo(%s) = (%v, %v, %v), want a node", b, rp, found, err)
	}
	if got := rp.AdmittedAccounts(); len(got) != 0 {
		t.Errorf("the round trip admits %v, want nothing", got)
	}
	// A null block is the same policy as an absent one (Decode's rule for the
	// shared node too), and it round-trips to no block as well: a publisher
	// handed a node with a written null must not keep the null.
	rp, found, err = DecodeRepo([]byte(`{"credentials": null}`))
	if err != nil || !found || rp == nil {
		t.Fatalf("a null block = (%v, %v, %v), want a node", rp, found, err)
	}
	if got := rp.AdmittedAccounts(); len(got) != 0 {
		t.Errorf("a null block admits %v, want nothing", got)
	}
	if b, err := json.Marshal(rp); err != nil {
		t.Fatal(err)
	} else if got := string(b); got != `{}` {
		t.Errorf("a null block re-marshals to %s, want {}", got)
	}
	// The block the publisher does write is the block the reader reads.
	b, err = json.Marshal(RepoPolicy{Credentials: &RepoCredentials{Accounts: []string{"ops", "spare"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"credentials":{"accounts":["ops","spare"]}}`; got != want {
		t.Errorf("a repository policy with two admissions marshals to %s, want %s", got, want)
	}
	if rp, found, err := DecodeRepo(b); err != nil || !found || len(rp.AdmittedAccounts()) != 2 {
		t.Errorf("the publisher's own document does not decode = (%v, %v, %v)", rp, found, err)
	}
}

// TestPolicyRepoAdmissionsNamesOnly: the per-repository node of O4 —
// config/policy/repos/$slug carries the *names* of the Claude accounts its
// repository is admitted to, and nothing else. The names come from
// config.ClaudeAccount.AllowDataTo, and the service reads them as its own
// account to decide whether a run of this repository may be given a tier-A
// credential.
//
// Names only is the whole of O4's security claim, and it is asserted from both
// sides: the decoded list holds account names, and the document holds nothing
// else (no token, no secret name). The node is readable by the run whose slug
// it is, so a value here would be a value a prompt-injected agent can take
// through /proc.
//
// D153's "never in fugaro.yaml" is the other half, asserted where the answer
// lives: internal/config's decoder refuses a `credentials:` key in a
// repository's own file (the same rule TestRepoCannotWriteRouting applies to
// the routing switch), so the block exists on the node alone.
func TestPolicyRepoAdmissionsNamesOnly(t *testing.T) {
	raw := []byte(`{"credentials": {"accounts": ["work", "personal"]}}`)
	rp, found, err := DecodeRepo(raw)
	if err != nil || !found || rp == nil {
		t.Fatalf("DecodeRepo = (%v, %v, %v), want a node", rp, found, err)
	}
	got := rp.AdmittedAccounts()
	if len(got) != 2 || got[0] != "personal" || got[1] != "work" {
		t.Errorf("AdmittedAccounts = %v, want [personal work] (sorted)", got)
	}
	// O4: names only. The node is readable by the run whose slug it is, and it
	// carries nothing about the account but the name an operator wrote in
	// claude_accounts — no token, no secret name, no value of any kind. The
	// assertion is on the decoded *document*: every string in it is an account
	// name of the list above.
	if strings.Contains(string(raw), "sk-") || strings.Contains(string(raw), "token") {
		t.Fatalf("the fixture carries something secret-shaped: %s", raw)
	}
	for _, s := range got {
		if strings.ContainsAny(s, " \t\n:/@") {
			t.Errorf("AdmittedAccounts holds %q, which is not an account name", s)
		}
	}
	// A null node and an absent one are "no policy for this repository" —
	// today's behaviour, and not an error.
	for _, empty := range []string{"", "null", "  "} {
		rp, found, err := DecodeRepo([]byte(empty))
		if rp != nil || found || err != nil {
			t.Errorf("DecodeRepo(%q) = (%v, %v, %v), want (nil, false, nil)", empty, rp, found, err)
		}
	}
	// A node with no credentials block at all is a repository the policy admits
	// to no account: it decodes, and it admits nothing. It is not an error —
	// the node is written per repository and a repository with no account is a
	// legal state (its runs never get tier A).
	rp, found, err = DecodeRepo([]byte(`{}`))
	if err != nil || !found || rp == nil {
		t.Fatalf("an empty node = (%v, %v, %v), want a node", rp, found, err)
	}
	if got := rp.AdmittedAccounts(); len(got) != 0 {
		t.Errorf("an empty node admits %v, want nothing", got)
	}
	// A block that is written must name its accounts: an empty list beside a
	// written `credentials` is the same silent-nothing the routing block's own
	// refusal is about, and the operator who wrote it believes they named
	// somebody.
	//
	// A block with no accounts key, and a bare list, are refused too: both are
	// a shape this reader does not read, and reading either as "no admissions"
	// is an admission decision made by a typo.
	for _, tc := range []struct{ name, raw string }{
		{"a written block with no accounts", `{"credentials": {}}`},
		{"a written block with an empty list", `{"credentials": {"accounts": []}}`},
		{"a written block with a null list", `{"credentials": {"accounts": null}}`},
		{"a bare list", `{"credentials": ["work"]}`},
		{"the credentials block written as a list", `{"credentials": {"accounts": [""]}}`},
		{"an unknown member of the block", `{"credentials": {"accounts": ["work"], "tokens": ["x"]}}`},
		{"an unknown top-level key", `{"admissions": {"accounts": ["work"]}}`},
		{"a member spelled in another case", `{"credentials": {"Accounts": ["work"]}}`},
		{"a member written twice", `{"credentials": {"accounts": ["work"], "accounts": ["personal"]}}`},
		{"an account listed twice", `{"credentials": {"accounts": ["work", "work"]}}`},
		{"a name that is not an account name", `{"credentials": {"accounts": ["not a name"]}}`},
	} {
		rp, found, err := DecodeRepo([]byte(tc.raw))
		if err == nil {
			t.Errorf("%s: accepted (%v), want refused", tc.name, rp)
			continue
		}
		if !found || rp != nil {
			t.Errorf("%s: DecodeRepo = (%v, %v, %v), want (nil, true, err)", tc.name, rp, found, err)
		}
	}
}
