//go:build firebase

package rules

// O2 of the stateless orchestrator plan (docs/stateless-orchestrator/
// implementation-plan.md; D1220, D1241 answered on 2026-11-04): the state, the
// lease and the audit live in a top-level `orchestrator/<id>/` subtree, and
// these rules are the only place that subtree's refusal of every token writer
// is written down.
//
// The name is half the assertion, and it is the half the emulator cannot make:
// the root is already `".read": false` and `".write": false`, so a subtree the
// template never mentions is refused everything by a rule nobody reading the
// file can see -- and the next person who adds a grant to this document has to
// meet it. So the template and the generated document are read for the node by
// name first, and the emulator then proves the deployed document does what
// those two say: the signed-in person (USER, D1241's reader) reads it, and the
// identities the rules evaluate and refuse -- a run's own token, a token the
// launcher's `signJwt` minter made, and a phone whose device node was never
// written -- are refused, each after reaching a family the rules do grant it in
// the same database, so a refusal here is this node's rule and not an empty
// tree's.
//
// What these tests deliberately do not assert is a write-own guarantee. People
// reach the database with an OAuth Bearer, and IAM bypasses `.write` and
// `.validate` (reconciliation.md, "What the rules do and do not bind"), so the
// person's write is the harness's admin client -- the IAM path -- and no rule
// in this file binds it. The subtree is written as a refusal of every identity
// the rules *do* evaluate, not as a claim that the writer is the person: a
// claim-based "write own" rule is forgeable by any launcher that can mint a
// custom token through `signJwt` (iam.tf's `minter` grant), which is why the
// minted-person-shaped case below is refused as a writer and is not asserted to
// be refused as a reader.
//
// Hand mutation each fails on: dropping the node from the template (the
// generated document carries no read grant and the emulator's person read is
// refused), giving it a `.write` anywhere (a run's token writes the state node
// the daemon's `PutIfMatch` is supposed to own), turning the `.read` into
// `false` (the signed-in person the design has reading the node is refused it),
// giving it `mcpauth`'s `.read: false` (the person's own read fails), and a
// `$child` under it carrying a grant (a rule hidden one level down, which the
// name check reads out of the generated paths).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The siblings under `orchestrator/<id>` are separate nodes on purpose (D1232,
// G9): a PUT of the parent would wipe the children and every heartbeat would
// move a shared ETag. The tests below therefore reach the state through a path
// two levels down, which is also the path the daemon writes.
const (
	orchID      = "o1"
	orchState   = PathOrchestrator + "/" + orchID + "/state"
	orchLease   = PathOrchestrator + "/" + orchID + "/lease"
	orchAudit   = PathOrchestrator + "/" + orchID + "/audit"
	orchSibling = PathOrchestrator + "/" + orchID + "/lease/until"
)

// minter is a token the launcher's `signJwt` minter made (iam.tf's `minter`
// grant; reconciliation.md, "What the rules do and do not bind"): a custom
// token whose claims the minter picked, so it carries the project's `fp` -- it
// satisfies the rules' own `TOKEN` macro, and it reaches the families a run
// reaches -- for an identity that is no run's and no person's. That is the
// second writer O2's answer names: the subtree is closed to it for the same
// reason it is closed to a run's own token, and not because a shape check
// happens to reject the claims it carries.
func (h *harness) minter(uid string) *rtdb.Client { return h.as(claims{uid: uid}) }

// orchestratorInTemplate reads the embedded template -- the key a person edits
// -- and returns the /orchestrator node, or fails saying what the design puts
// there.
func orchestratorInTemplate(t *testing.T) map[string]any {
	t.Helper()
	var tmpl map[string]any
	if err := json.Unmarshal([]byte(source), &tmpl); err != nil {
		t.Fatalf("the rules template is not JSON: %v", err)
	}
	rules, ok := tmpl["rules"].(map[string]any)
	if !ok {
		t.Fatalf("the rules template carries no \"rules\" object")
	}
	node, ok := rules[PathOrchestrator].(map[string]any)
	if !ok {
		t.Fatalf("the template carries no top-level /%s subtree: D1220's answer of 2026-11-04 puts the orchestrator's state, its lease and its audit there, and a subtree the rules never name is one no reader of this file can see", PathOrchestrator)
	}
	return node
}

// TestOrchestratorSubtreeIsReadableOnlyBySignedInPeople: the subtree is read by
// the signed-in person (the `USER` macro, the same reader the coordinator's
// state has) and by nobody the rules evaluate as a client token.
func TestOrchestratorSubtreeIsReadableOnlyBySignedInPeople(t *testing.T) {
	node := orchestratorInTemplate(t)
	// The read is the explicit `USER` grant, not an inherited one: D1241's
	// answer has the person read the node, and an inherited grant is one a
	// later edit can widen without meeting this check.
	if v, has := node[".read"]; !has || v != "USER" {
		t.Errorf("the template's /%s carries .read = %v, want the explicit USER grant the signed-in person reads it through", PathOrchestrator, v)
	}
	if v, has := node[".write"]; !has || v != false {
		t.Errorf("the template's /%s carries .write = %v, want the explicit false every identity the rules evaluate is refused by: the person's own credential writes this node over IAM, which bypasses the rules, and no client token does", PathOrchestrator, v)
	}

	// And the generated document, which is what a deployment carries.
	generated, ok := generatedPaths(t)[PathOrchestrator]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node", PathOrchestrator)
	}
	if cond, _ := generated[".read"].(string); !strings.Contains(cond, "auth.token.mu") {
		t.Errorf("generated /%s .read = %v, want the expanded USER predicate (the paired phone's mu claim, D166)", PathOrchestrator, generated[".read"])
	}
	if v, has := generated[".write"]; !has || v != false {
		t.Errorf("generated /%s .write = %v, want false", PathOrchestrator, v)
	}
	// Nothing beneath it may carry a grant of its own: the subtree is one node
	// whose read cascades, and a child with a rule is the shape a widening hides
	// in.
	for path, attrs := range generatedPaths(t) {
		if !strings.HasPrefix(path, PathOrchestrator+"/") {
			continue
		}
		for _, attr := range []string{".read", ".write"} {
			if v, has := attrs[attr]; has {
				t.Errorf("generated /%s carries %s = %v, under a subtree whose single grant is its own .read", path, attr, v)
			}
		}
	}

	h := newHarness(t)
	h.seed(leased(2 * usd))
	alice, run := h.phone(mbAlice), h.run("aurora", "r1")
	minted := h.minter("svc-minter-1")
	// The three identities each reach a family the rules do grant them, so the
	// refusals below are refusals of this node rather than of an empty database.
	h.mustRead(alice, budget.PathCapsGlobal)
	h.mustRead(run, budget.PathCapsGlobal)
	h.mustRead(minted, budget.PathCapsGlobal)

	// The person's own credential, over IAM: the harness's admin client is that
	// path (no rule here binds it), and the record it leaves is what the reader
	// below reads and what the writers below are refused.
	h.set(orchState, map[string]any{"schemaVersion": 1, "phase": "idle"})
	h.wantValue(orchState+"/phase", `"idle"`)

	// The signed-in person reads the node, its parent and its siblings: the
	// grant is at the subtree, so a read of any path under it is admitted.
	for _, p := range []string{PathOrchestrator, PathOrchestrator + "/" + orchID, orchState, orchLease, orchAudit} {
		h.mustRead(alice, p)
	}
	// The reader has to be a person the rules recognise as one: an account whose
	// device node was never written is admitted nowhere (D166), and this node is
	// no exception.
	h.mustReadDenied(h.unpaired(mbBob), orchState)
	// A run's token and a token the launcher's minter made are not readers: the
	// subtree is the daemon's state, not a family a run may read.
	for _, p := range []string{PathOrchestrator, orchState, orchLease, orchAudit} {
		h.mustReadDenied(run, p)
		h.mustReadDenied(minted, p)
	}
}

// TestOrchestratorRefusesEveryTokenWriter: the write is `false` for every
// identity the rules evaluate -- a run-scoped job token, a minter-minted
// non-person token and the phone's `mu` claim alike -- and the person's own
// write, which the rules do not bind, is proved to succeed first, so the
// refusals are this node's rule and not a vacuous deny-by-default root.
//
// The oversize and malformed cases are O0's client-side test
// (internal/orchestrator), not this one: the rules do not validate shape or
// size at all, and a value that O0's validator would reject is refused here for
// the same reason a well-formed one is.
func TestOrchestratorRefusesEveryTokenWriter(t *testing.T) {
	node := orchestratorInTemplate(t)
	if v, has := node[".write"]; !has || v != false {
		t.Errorf("the template's /%s carries .write = %v, want the explicit false", PathOrchestrator, v)
	}
	generated, ok := generatedPaths(t)[PathOrchestrator]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node", PathOrchestrator)
	}
	if v, has := generated[".write"]; !has || v != false {
		t.Errorf("generated /%s .write = %v, want false", PathOrchestrator, v)
	}

	h := newHarness(t)
	h.seed(leased(2 * usd))
	alice, run := h.phone(mbAlice), h.run("aurora", "r1")
	minted := h.minter("svc-minter-1")
	h.mustRead(alice, budget.PathCapsGlobal)
	h.mustRead(run, budget.PathCapsGlobal)
	h.mustRead(minted, budget.PathCapsGlobal)

	// The person's own credential writes the node, over IAM -- D1241's answer.
	// The rules do not bind this write and this task claims no guarantee about
	// it; it is here as the positive control, so that every refusal below is a
	// refusal of a node that holds data and is written by someone.
	h.set(orchState, map[string]any{"schemaVersion": 1, "phase": "idle"})
	h.wantValue(orchState+"/phase", `"idle"`)

	for _, c := range []struct {
		who   string
		write func() map[string]any
	}{
		{"a run's token", func() map[string]any { return put(orchState, map[string]any{"phase": "running"}) }},
		{"a run's token", func() map[string]any { return put(orchSibling, 1) }},
		{"a run's token", func() map[string]any { return put(orchState+"/phase", "running") }},
		{"a run's token", func() map[string]any { return map[string]any{orchState: nil} }},
		{"a minter-minted token", func() map[string]any { return put(orchState+"/phase", "running") }},
		{"a minter-minted token", func() map[string]any { return put(orchLease, map[string]any{"until": 1}) }},
	} {
		client := run
		if c.who == "a minter-minted token" {
			client = minted
		}
		h.mustDeny(client, c.who+" writing the orchestrator state", c.write())
	}
	// The phone's own `mu` claim is refused like the rest: a token the rules
	// admit as a reader is not a writer of this node, and the reconciliation's
	// answer is explicit that the `.write` is false for all of them. This is
	// also the strongest case of the three, because it is the shape a forged
	// "write own" claim would take: any launcher can mint it through `signJwt`.
	forged := h.as(claims{mu: mbAlice, fp: "-"})
	h.mustRead(forged, orchState)
	h.mustDeny(forged, "a minted token carrying the person's own mu claim", put(orchState+"/phase", "running"))
	// And nothing the person's write left behind moved.
	h.wantValue(orchState+"/phase", `"idle"`)
}
