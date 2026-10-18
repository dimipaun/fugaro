//go:build firebase

package rules

// creds/ is the credential cascade's shared state (docs/credentials-service,
// D136 and the owner's O1 ruling of 2026-10-17): the credentials service writes
// it as its own service account, which is an IAM admin and bypasses these
// rules, and **no run token may read or write any part of it** — not its own
// run's node, not another repository's, not jointly.
//
// Today's template has no `creds` node at all and the root's `.read`/`.write`
// are false, so every token of the project is denied everything under it. This
// file pins that against the deployed rules, so the later rules change that
// adds the cascade's own children (the plan's C1.3) cannot open creds/ by
// accident.
//
// It changes no rule and adds no grant: the template is untouched, and these
// tests pass on today's generated rules by construction. Their worth is that
// each one goes red the moment a grant appears over any of the paths below —
// checked by adding one to rules.json.tmpl locally and watching every test
// here fail (the plan's own review step; the mutation was never committed).
//
// The seeded state is invented and holds nothing a credential could be: names,
// states, a counter and a lease expiry. See Convention 2 of the plan.

import (
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// credsState is the cascade's shared state as the service writes it: two
// accounts' marks, the round-robin counter, a run's sticky choice and one
// lease. A later task (C1.3, C1.9) may add to this shape; nothing here depends
// on it beyond "these are the leaves of creds/".
func credsState() map[string]any {
	return map[string]any{
		"creds/accounts/alice/state":      "available",
		"creds/accounts/alice/until":      int64(0),
		"creds/accounts/bob/state":        "exhausted",
		"creds/accounts/bob/until":        int64(1_800_000_000_000),
		"creds/accounts/bob/backoff":      int64(900_000),
		"creds/counters/roundRobin":       int64(3),
		"creds/runs/aurora/r1/credential": "alice",
		"creds/leases/alice/aurora-r1":    int64(1_800_000_000_000),
	}
}

// credsPaths is every path a token might plausibly be granted: the subtree
// root, each parent, and the leaves the cascade actually uses. A read grant on
// any ancestor cascades to its children in RTDB, which is why the parents are
// here too.
func credsPaths() []string {
	return []string{
		"creds",
		"creds/accounts",
		"creds/accounts/alice",
		"creds/accounts/alice/state",
		"creds/accounts/alice/until",
		"creds/accounts/bob",
		"creds/accounts/bob/state",
		"creds/accounts/bob/until",
		"creds/accounts/bob/backoff",
		"creds/counters",
		"creds/counters/roundRobin",
		"creds/runs",
		"creds/runs/aurora",
		"creds/runs/aurora/r1",
		"creds/runs/aurora/r1/credential",
		"creds/leases",
		"creds/leases/alice",
		"creds/leases/alice/aurora-r1",
	}
}

// TestCredsRunCannotReadAnything: a run's token reads none of creds/, not even
// its own run's sticky choice — the runner learns the current credential from
// the service's answer and records it on its own agent entry, never from here.
func TestCredsRunCannotReadAnything(t *testing.T) {
	h := newHarness(t)
	h.patch(credsState())
	c := h.run("aurora", "r1")
	// The state is really there, and readable as admin: a denial that passed
	// because nothing had been seeded would prove nothing.
	h.wantValue("creds/accounts/bob/state", `"exhausted"`)
	h.wantValue("creds/runs/aurora/r1/credential", `"alice"`)
	// ... and the run reads none of it, leaf or parent.
	for _, p := range credsPaths() {
		h.mustReadDenied(c, p)
	}
}

// TestCredsRunCannotWriteAnything: every write the cascade would make, made
// with the run's own token, is refused — an exhaustion mark, the counter, the
// run's sticky choice, a lease, and the creation of a new account's node.
func TestCredsRunCannotWriteAnything(t *testing.T) {
	h := newHarness(t)
	h.patch(credsState())
	c := h.run("aurora", "r1")
	for _, u := range []map[string]any{
		{"creds/accounts/alice/state": "exhausted"},
		{"creds/accounts/alice/until": int64(1_800_000_000_000)},
		{"creds/accounts/alice/backoff": int64(1_800_000)},
		{"creds/accounts/carol/state": "available"},
		{"creds/counters/roundRobin": int64(4)},
		{"creds/runs/aurora/r1/credential": "bob"},
		{"creds/leases/alice/aurora-r1": int64(1_800_000_000_000)},
		{"creds/accounts/alice": map[string]any{"state": "broken", "until": int64(0)}},
	} {
		h.mustDeny(c, "a write under creds/", u)
	}
	// A whole-node write, which a multi-path update cannot express (the REST
	// client has no root PUT): refused outright, by the rules and not by
	// validation, since no write grant exists anywhere on the path.
	h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodPut, "creds/accounts/alice", `{"state":"available"}`)
	h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodPatch, "creds/accounts/alice", `{"state":"available"}`)
}

// TestCredsRunCannotDelete: deleting a mark or a lease is the cheapest way to
// un-exhaust a token, and a delete is not validated at all — so the denial has
// to come from the write rule, on every level of the path.
func TestCredsRunCannotDelete(t *testing.T) {
	h := newHarness(t)
	h.patch(credsState())
	c := h.run("aurora", "r1")
	for _, u := range []map[string]any{
		{"creds/accounts/bob/state": nil},
		{"creds/accounts/bob/until": nil},
		{"creds/accounts/bob": nil},
		{"creds/accounts": nil},
		{"creds": nil},
		{"creds/counters/roundRobin": nil},
		{"creds/runs/aurora/r1/credential": nil},
		{"creds/leases/alice": nil},
	} {
		h.mustDeny(c, "a delete under creds/", u)
	}
	h.rawDenied(claims{slug: "aurora", run: "r1"}, http.MethodDelete, "creds/accounts/bob", "")
	// The exhausted mark the service wrote is exactly as it left it.
	h.wantValue("creds/accounts/bob/state", `"exhausted"`)
	h.wantValue("creds/accounts/bob/until", "1800000000000")
}

// TestCredsTokenOfAnotherRepositoryDenied: creds/ is not scoped per repository
// — it is closed to every run token. A token would otherwise read the shared
// marks of every admitted account, and could mark them.
func TestCredsTokenOfAnotherRepositoryDenied(t *testing.T) {
	h := newHarness(t)
	h.patch(credsState())
	other := h.run("vega", "r7")
	for _, p := range credsPaths() {
		h.mustReadDenied(other, p)
	}
	h.mustDeny(other, "a mark write from another repository's token", map[string]any{"creds/accounts/alice/state": "exhausted"})
	h.mustDeny(other, "its own repository's sticky choice", map[string]any{"creds/runs/vega/r7/credential": "alice"})
	h.mustDeny(other, "a delete from another repository's token", map[string]any{"creds/accounts/alice": nil})
	// The token of the repository the state belongs to is denied too: this is
	// not a slug mismatch, it is a closed subtree.
	h.mustDeny(h.run("aurora", "r1"), "the owning repository's own token", map[string]any{"creds/accounts/alice/state": "exhausted"})
	h.mustReadDenied(h.run("aurora", "r1"), "creds/accounts/alice/state")
}

// TestCredsMailboxIdentitiesDenied: the project's other token kinds are denied
// too — the phone's signed-in owner and the mailbox consumer (D112, D113b).
// Neither has any business under creds/, and neither can reach it today. The
// case is here because creds/ is closed to *every* non-admin token and not
// only to a run's, so this makes the sentence in this file's header a checked
// claim: C1.3 adds children under creds/, and a child granted to a signed-in
// identity would pass the run-token cases above while failing this one.
func TestCredsMailboxIdentitiesDenied(t *testing.T) {
	h := newHarness(t)
	h.patch(credsState())
	for _, tc := range []struct {
		who string
		c   *rtdb.Client
	}{
		{"the phone", h.phone("mb")},
		{"the consumer", h.consumer("mb")},
	} {
		for _, p := range credsPaths() {
			h.mustReadDenied(tc.c, p)
		}
		h.mustDeny(tc.c, tc.who+" writing a mark", map[string]any{"creds/accounts/alice/state": "exhausted"})
		h.mustDeny(tc.c, tc.who+" writing the counter", map[string]any{"creds/counters/roundRobin": int64(4)})
		h.mustDeny(tc.c, tc.who+" deleting a mark", map[string]any{"creds/accounts/bob/state": nil})
		// The state is untouched by every refusal above.
		h.wantValue("creds/accounts/bob/state", `"exhausted"`)
		h.wantValue("creds/counters/roundRobin", "3")
	}
}
