//go:build firebase

package rules

// The emulator cases for /creds and for the credentials children the cascade
// writes (credentials-service plan, tasks C0.6 and C1.3).
//
// C0.6 is the groundwork the owner allowed before the seal: the template is not
// changed by it, and what it pins is that today's rules already deny a run
// token every read and every write under /creds — the state the credentials
// service alone writes, as its own account, with the rules bypassed (D136, O1).
// C1.3 adds the explicit `creds` node to the template and the two registry
// children and the by-tier counters beside it, and these cases grow with it:
// the denial is re-proven against the deployed document, and the new children
// are proven to accept their own run's well-formed writes and to refuse
// everything else.
//
// C0.6's own plan text says its red step is impossible ("the tests pass on
// today's template by design") and that a reviewer must check each would fail
// if a grant were added. That was true of C0.6 alone. This file now also
// carries C1.3's cases, whose red step is real (the template did not generate
// the children), and the denial cases here are mutation-proven the same way as
// every other case in the suite: add the grant, watch them fail, remove it.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// credsMark is a seeded mark under /creds: the shape the service writes (a
// state, an instant and a backoff count), so a read the rules refused for want
// of data is not mistaken for a refusal.
var credsMark = map[string]any{"state": "exhausted", "until": int64(4_000_000_000_000), "since": int64(3_000_000_000_000), "backoff": 2}

// credsSeed is what the admin writes before each denial case: a mark, a lease,
// the run's sticky credential and the round-robin counter, so every location
// under /creds a reader of the plan would name holds something.
func (h *harness) credsSeed() {
	h.set("creds/marks/a1", credsMark)
	h.set("creds/marks/a2", credsMark)
	h.set("creds/leases/r1", map[string]any{"credential": "a1", "expires": int64(4_000_000_000_000)})
	h.set("creds/current/aurora/r1", "a1")
	h.set("creds/roundrobin", map[string]any{"a1": 3, "a2": 1})
}

// credsPaths are the locations the cases walk: every node /creds is expected to
// hold, at the depth the service reads and writes them (C1.9).
func credsPaths() []string {
	return []string{
		"creds/marks/a1",
		"creds/marks/a2",
		"creds/leases/r1",
		"creds/current/aurora/r1",
		"creds/roundrobin",
	}
}

// TestCredsRunCannotReadAnything: a run's token reads nothing under /creds, not
// even its own repository's stickiness. The node is seeded as the admin writes
// it, so a read that came back empty because there was nothing to read would
// not pass for a denial — the assertion is a permission error.
func TestCredsRunCannotReadAnything(t *testing.T) {
	h := newHarness(t)
	h.credsSeed()
	c := h.run("aurora", "r1")
	for _, p := range credsPaths() {
		h.mustReadDenied(c, p)
	}
	h.mustReadDenied(c, "creds/marks")
	h.mustReadDenied(c, "creds")
}

// TestCredsRunCannotWriteAnything: a run's token writes nothing under /creds,
// at any shape — a mark (the write a false exhaustion would need, the cost
// shift review finding W3 is about), a lease, a stickiness record or the
// round-robin counter. Each case also asserts the database did not change,
// which is what rules' mustDeny does.
func TestCredsRunCannotWriteAnything(t *testing.T) {
	h := newHarness(t)
	h.credsSeed()
	c := h.run("aurora", "r1")
	for _, u := range []map[string]any{
		{"creds/marks/a1": credsMark},
		{"creds/marks/a1/state": "exhausted"},
		{"creds/marks/a3": credsMark},
		{"creds/leases/r1": map[string]any{"credential": "a2", "expires": int64(4_000_000_000_000)}},
		{"creds/leases/r2": map[string]any{"credential": "a1"}},
		{"creds/current/aurora/r1": "a2"},
		{"creds/roundrobin/a1": 99},
	} {
		h.mustDeny(c, "a run's write under /creds", u)
	}
}

// TestCredsRunCannotDelete: deleting a mark is the other half of the write
// grant a run must never have — a delete is not validated, so a `!data.exists()`
// or a `.validate` would not stand in the way of one. It is driven both through
// the multi-path client and as a raw DELETE, which is the request that carries
// no body for the rules to validate.
func TestCredsRunCannotDelete(t *testing.T) {
	h := newHarness(t)
	h.credsSeed()
	c := h.run("aurora", "r1")
	for _, p := range credsPaths() {
		h.mustDeny(c, "a run deleting "+p, map[string]any{p: nil})
	}
	h.rawDenied(claims{slug: "aurora", run: "r1"}, "DELETE", "creds/marks/a1", "")
	h.rawDenied(claims{slug: "aurora", run: "r1"}, "DELETE", "creds/roundrobin", "")
}

// TestCredsTokenOfAnotherRepositoryDenied: the denial is the node's, not the
// run's — another repository's run, a launcher-shaped token of another fs, and
// the three identities the rules read (a phone's user claim, a consumer's and a
// pairing session's) are refused exactly as this run is. The last two matter
// for a different reason: they are the only claims admitted anywhere outside a
// run's own families, and /creds must not be one of the families they reach.
func TestCredsTokenOfAnotherRepositoryDenied(t *testing.T) {
	h := newHarness(t)
	h.credsSeed()
	others := map[string]*rtdb.Client{
		"another repository's run":       h.run("other", "r1"),
		"another run of this repository": h.run("aurora", "rB"),
		"the phone":                      h.phone("mb-1"),
		"the consumer":                   h.consumer("mb-1"),
		"the pairing session":            h.pairing("p~abc"),
	}
	for name, c := range others {
		for _, p := range credsPaths() {
			h.mustReadDenied(c, p)
		}
		h.mustDeny(c, name+" writing a mark", map[string]any{"creds/marks/a1": credsMark})
	}
}

// TestRunTokenCannotWriteTheCredsRulesMarkers: neither generation marker is a
// run's to write. `credsRules` is what a cascade launch, the init stage and
// doctor read before a run may start (C1.19-C1.21) and `policyRules` is what
// every reader of a published policy reads: a token that could write either
// could make a run pass a gate the installation has not met. Both are written
// by the owner's `fugaro init --firebase`, over IAM, which bypasses the rules.
//
// `policyRules` had no such pin; this adds it beside the new one.
func TestRunTokenCannotWriteTheCredsRulesMarkers(t *testing.T) {
	h := newHarness(t)
	h.set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	h.set(PathCredsRulesMarker(), CredsRulesVersion)
	c := h.run("aurora", "r1")
	for _, p := range []string{PathCredsRulesMarker(), projectpolicy.PathRulesMarker()} {
		// The read is a run token's: a cascade launch reads the marker with the
		// run's own identity, so the grant has to be there.
		h.mustRead(c, p)
		h.mustDeny(c, "a run writing "+p, map[string]any{p: CredsRulesVersion})
		h.mustDeny(c, "a run deleting "+p, map[string]any{p: nil})
		h.rawDenied(claims{slug: "aurora", run: "r1"}, "PUT", p, "1")
	}
}

// agentSeed creates the registry entry of the run, which the children below sit
// on: `/agents/$slug/$run`'s own .validate requires `repo` and `requestedBy`,
// and every field of the entry is a validation-only child of it.
func (h *harness) agentSeed(slug, run string) *rtdb.Client {
	h.t.Helper()
	c := h.run(slug, run)
	h.mustAllow(c, "create the registry entry", map[string]any{budget.PathAgent(slug, run): map[string]any{
		"repo": "aurora/web", "auth": "cascade", "startedAt": time.Now().UnixMilli(), "requestedBy": "alice@example.invalid",
	}})
	return c
}

// TestAgentEntryCredentialNameValidated: the run records the name of the
// credential it is using (C1.3 rule 2, D140): a bounded string that ties to the
// launcher's rb claim like every other field of the entry, and nothing else —
// a number, an over-long string or another launcher's rb is refused.
//
// The rules cannot tell a name from a value: what makes this a name is that the
// service answers with one and the runner writes what it was given. The bound
// is what the rules' half can be, and the `$other` guard on the entry is what
// keeps a credential-shaped field the schema never named from being written
// beside it.
func TestAgentEntryCredentialNameValidated(t *testing.T) {
	h := newHarness(t)
	w := stdWorld()
	h.seed(w)
	c := h.agentSeed("aurora", "r1")
	entry := budget.PathAgent("aurora", "r1")
	h.mustAllow(c, "a credential's name", map[string]any{entry + "/credential": "a1"})
	h.wantValue(entry+"/credential", `"a1"`)
	h.mustAllow(c, "a longer name", map[string]any{entry + "/credential": "claude-shared-account-2"})

	for name, u := range map[string]map[string]any{
		"a number":                {entry + "/credential": 7},
		"a boolean":               {entry + "/credential": true},
		"a nested object":         {entry + "/credential": map[string]any{"name": "a1"}},
		"a string past the bound": {entry + "/credential": longString(MaxString + 1)},
	} {
		h.mustDeny(c, name, u)
	}
	// RBOK: the field is tied to the launcher's rb claim, as the entry's other
	// fields are. A token without rb cannot write it.
	noRb := h.as(claims{slug: "aurora", run: "r1", rb: "-"})
	h.mustDeny(noRb, "a token with no rb claim", map[string]any{entry + "/credential": "a1"})
	// And the entry is still a closed allowlist: a field the schema does not
	// name is refused, so no second credential-shaped node can be bolted on.
	h.mustDeny(c, "a field the entry does not name", map[string]any{entry + "/credential_value": "sk-not-a-name"})
}

// TestAgentEntryWaitingUntilValidated: `waiting_until` is the instant the run
// expects to resume at, for the `oauth-only` "waiting, expected resume time"
// view (D152). It is a timestamp — a non-negative whole number of epoch
// milliseconds, bounded at MaxTimestamp — and not an amount: the amount bound
// (MaxAmount, $10M) would refuse a resume time in the year 2286 and accept a
// negative one, which is the direction that matters.
func TestAgentEntryWaitingUntilValidated(t *testing.T) {
	h := newHarness(t)
	w := stdWorld()
	h.seed(w)
	c := h.agentSeed("aurora", "r1")
	entry := budget.PathAgent("aurora", "r1")
	at := time.Now().Add(15 * time.Minute).UnixMilli()
	h.mustAllow(c, "the instant the run resumes at", map[string]any{entry + "/waiting_until": at})
	h.wantValue(entry+"/waiting_until", json.Number(int64String(at)).String())

	for name, u := range map[string]map[string]any{
		"a negative instant":         {entry + "/waiting_until": -1},
		"a fractional instant":       {entry + "/waiting_until": 1.5},
		"a string":                   {entry + "/waiting_until": "2026-10-19T12:00:00Z"},
		"past the exact-double line": {entry + "/waiting_until": int64(MaxTimestamp) + 1},
	} {
		h.mustDeny(c, name, u)
	}
	// The bound itself is accepted: a clock reading at the line is not refused
	// for being at it.
	h.mustAllow(c, "the timestamp bound itself", map[string]any{entry + "/waiting_until": MaxTimestamp})
}

// int64String is n as decimal text, for comparing against the emulator's JSON.
func int64String(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// longString is n "x" characters, for the over-the-bound cases.
func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// byTierRun and byTierDay are the two by-tier leaves of the run's own counters:
// its lifetime ledger and its share of the day (C1.3 rule 3). The repository's
// and the project's day counters beside them are written the same way.
func byTierRun(tier string) string { return "runs/aurora/r1/byTier/" + tier + "/spent" }
func byTierDay(tier string) string {
	return budget.PathSpendRun(today(), "aurora", "r1") + "/byTier/" + tier + "/spent"
}
func byTierRepo(tier string) string {
	return budget.PathSpendRepo(today(), "aurora") + "/byTier/" + tier + "/spent"
}
func byTierGlobal(tier string) string {
	return budget.PathSpendGlobal(today()) + "/byTier/" + tier + "/spent"
}

// TestByTierCountersOwnRunOnly: the by-tier split is real spend, so it is
// written under exactly the grants the `spent` leaf beside it carries — the
// run's own ledger and day share for its own token, the repository's and the
// project's day counters for the same identity the totals use. Another run's or
// another repository's split is refused, and so is a tier key that is not `b` or
// `c`: tier A is notional and never real, so it has no node here to write.
func TestByTierCountersOwnRunOnly(t *testing.T) {
	h := newHarness(t)
	w := leased(3 * usd)
	h.seed(w)
	c := w.client(h)

	h.mustAllow(c, "the run's tier-B lifetime counter", map[string]any{byTierRun("b"): int64(usd)})
	h.mustAllow(c, "the run's tier-C lifetime counter", map[string]any{byTierRun("c"): int64(usd)})
	h.mustAllow(c, "the run's tier-B share of the day", map[string]any{byTierDay("b"): int64(usd)})
	h.mustAllow(c, "this repository's tier-B day counter", map[string]any{byTierRepo("b"): int64(usd)})
	h.mustAllow(c, "the project's tier-C day counter", map[string]any{byTierGlobal("c"): int64(usd)})

	h.mustDeny(c, "another run's lifetime counter", map[string]any{"runs/aurora/rB/byTier/b/spent": int64(usd)})
	h.mustDeny(c, "another repository's lifetime counter", map[string]any{"runs/other/r1/byTier/b/spent": int64(usd)})
	h.mustDeny(c, "another run's share of the day", map[string]any{budget.PathSpendRun(today(), "aurora", "rB") + "/byTier/b/spent": int64(usd)})
	h.mustDeny(c, "another repository's day counter", map[string]any{budget.PathSpendRepo(today(), "other") + "/byTier/b/spent": int64(usd)})
	h.mustDeny(c, "another day's counter", map[string]any{budget.PathSpendGlobal(today()-2) + "/byTier/b/spent": int64(usd)})

	// Tier A has no real-spend node: it is accounted as notional on the nodes
	// that already exist.
	for _, p := range []string{byTierRun("a"), byTierRun("A"), byTierRun(""), byTierDay("a"), byTierRepo("a"), byTierGlobal("a")} {
		h.mustDeny(c, "an unknown tier at "+p, map[string]any{p: int64(usd)})
	}
	// And the whole split node is not writable as one value: every leaf carries
	// its own .write, so a write that names the parent is refused.
	h.mustDeny(c, "a whole by-tier node", map[string]any{"runs/aurora/r1/byTier": map[string]any{"b": map[string]any{"spent": int64(usd)}}})
}

// TestByTierCountersMonotonic: each by-tier leaf is validated exactly like the
// `spent` leaf beside it — increase-only, whole micro-dollars, bounded per
// write and (for the run's own nodes) still inside what it reserved. A split
// counter that could fall, take a fraction or exceed the reservation would be a
// counter a run could use to hide spend, which is the whole quantity it exists
// to show.
func TestByTierCountersMonotonic(t *testing.T) {
	h := newHarness(t)
	w := leased(3 * usd)
	h.seed(w)
	h.set(byTierRun("b"), int64(2*usd))
	h.set(byTierGlobal("b"), int64(2*usd))
	w.st.Run.Spent = 2 * usd
	c := w.client(h)

	h.mustAllow(c, "an increase", map[string]any{byTierRun("b"): int64(3 * usd)})
	h.mustDeny(c, "a decrease", map[string]any{byTierRun("b"): int64(usd)})
	h.mustDeny(c, "a delete", map[string]any{byTierRun("b"): nil})
	h.mustDeny(c, "a negative amount", map[string]any{byTierRun("b"): int64(-1)})
	h.mustDeny(c, "a fractional amount", map[string]any{byTierRun("b"): 1.5})
	h.mustDeny(c, "a string", map[string]any{byTierRun("b"): "1000000"})
	// The run's own leaf is bounded by what it reserved: the $3 lifetime
	// reservation with nothing released holds $3, and not $4.
	h.mustDeny(c, "more than the run reserved", map[string]any{byTierRun("b"): int64(4 * usd)})
	// The shared day counter is bounded per write by the largest lease.
	h.mustDeny(c, "a write past maxReserve", map[string]any{byTierGlobal("b"): int64(8 * usd)})
}
