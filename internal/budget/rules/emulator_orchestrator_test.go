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
// CG6 of the console dependency graph plan (docs/cli-output-console-graph/
// implementation-plan-graph.md, H7, T2(a) ruled 2026-11-06) adds the rest of
// the identities a token can carry to both tests below: the graph reads this
// subtree through the person's IAM credential, so the subtree is granted
// nothing, and the mailbox consumer's `mc` claim and a pairing session's `pu`
// claim join the run's and the minter's in being refused it. orchestrator_test.go
// reads the same denial out of the generated document, in the job with no
// emulator.
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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
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

// mustOrchPath and mustPlanPath are this package's node paths for orchID, built
// through internal/orchestrator rather than written out here: a path a test
// spells itself is a path no writer builds, so a wrong one is a node nobody
// grants — which is exactly the accident the tests below are about.
func mustOrchPath(t *testing.T, pathOf func(string) (string, error)) string {
	t.Helper()
	p, err := pathOf(orchID)
	if err != nil {
		t.Fatalf("building the node path: %v", err)
	}
	return p
}

func mustPlanPath(t *testing.T, plan string) string {
	t.Helper()
	p, err := orchestrator.PathPlan(orchID, plan)
	if err != nil {
		t.Fatalf("building the plan's node path: %v", err)
	}
	return p
}

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
// state has) and by nobody the rules evaluate as a client token — E12's
// reports-job read (F27) is the one grant under it, it is on the escalation
// block alone, and the loop below requires it to be exactly that.
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
	// Nothing beneath it may carry a grant of its own except E12's, which is
	// named and required here: the subtree is one node whose read cascades, and
	// a child with a rule is the shape a widening hides in. The reports job's
	// read of the escalation block (F27) is the one grant under it — on exactly
	// one node, for the reports job's claim alone — so a second grant, the same
	// grant moved one node up, and the same grant widened to another identity
	// each fail here.
	for path, attrs := range generatedPaths(t) {
		if !strings.HasPrefix(path, PathOrchestrator+"/") {
			continue
		}
		if path == escalationLedgerPath {
			if len(attrs) != 1 || attrs[".read"] != reportsJobClaim {
				t.Errorf("generated /%s = %v, want exactly the reports job's read (%q): E12's grant is the one rule under this subtree", path, attrs, reportsJobClaim)
			}
			continue
		}
		for _, attr := range []string{".read", ".write"} {
			if v, has := attrs[attr]; has {
				t.Errorf("generated /%s carries %s = %v, under a subtree whose only grants are its own .read and E12's escalation read", path, attr, v)
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
	// Nor are the other two identities a token can carry (CG6 of the console
	// graph plan, H7): the mailbox consumer's `mc` claim and a pairing session's
	// `pu` claim. Each reaches a family the rules do grant it first -- the
	// consumer its mailbox's heartbeat, the session its own pairing request --
	// so these are refusals of this subtree and not of a token refused
	// everywhere.
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.pairing(pairUid), "its own pairing request", put(request(pairUid), aRequest()))
	for _, p := range []string{PathOrchestrator, orchState, orchLease, orchAudit} {
		h.mustReadDenied(h.consumer(mbAlice), p)
		h.mustReadDenied(h.pairing(pairUid), p)
	}
}

// TestOrchestratorReportsJobReadsEscalationOnly: E12's grant (F27), proved
// against the rules engine rather than against the document alone — the admit
// case and the refuse cases both, on the path the reports job really reads.
//
// The read is granted to a custom-token claim, and the claim's null guard is
// the difference between the escalation block's read and the whole subtree's:
// where /fugaro/project is absent, the equality alone compares null to null and
// admits any signed-in client, which is why the last case deletes the node and
// demands the refusal of a token that carries no rj claim.
//
// The block the job reads is the document the daemon writes, and the admit case
// decodes it as this build's ledger with the two names the loss of the item
// above it made necessary (repo, run_id). What the job does with them — the
// digest's item and the chain behind it — is TestEscalationItemFromTheReportsJobRead,
// which needs no emulator.
// TestLaunchEnvelopeGrantsTheDeviceItsOwnEntry: the launch envelope against a
// real rules engine (mobile plan task P5.1g, approved by the owner on
// 2026-11-14; the design's section 4.5 E1 and section 4.8, the owner's answers
// Q9 and Q11). The document's shape — the two grants, the two parents that carry
// none, and the `.write: false` on each child — is rules_test.go's
// TestLaunchEnvelopeNodesAreReadPerChild, which runs in the job with no
// emulator; what this test adds is the engine's own verdict on each claim, each
// beside the positive control that makes its refusal a refusal of the rule and
// not of an empty tree:
//
//   - a paired device reads the installation's named safe recipe and **its own**
//     entry, and is refused every other device's entry;
//   - an account whose device node was never written reads neither, and
//     deleting a live device's node takes its entry away with no ID token
//     expiring in between (D166's conjunct, which is what ties the envelope to
//     the pairing);
//   - no other identity reaches it — a run's own token, the launcher's minter,
//     the mailbox consumer's `mc` claim, a pairing session's `pu` claim and the
//     reports job's `rj` claim are each refused, and each is shown reaching a
//     family the rules do grant it first;
//   - nothing writes it: not the phone it is granted to, not a run, not an
//     unpaired account, and not with a whole-node replace or a single child —
//     the allow-list, the rate and the ceiling are the owner's numbers, and a
//     phone that could move one would bound nothing (E1's own residual risk).
func TestLaunchEnvelopeGrantsTheDeviceItsOwnEntry(t *testing.T) {
	h := newHarness(t)
	h.seed(leased(2 * usd))
	// The operator publishes the envelope over IAM, which is the only writer and
	// bypasses the rules: the values are really there, so every refusal below is
	// the rule's and not an absent node's.
	recipePath := PathLaunchEnvelope + "/recipe"
	aliceEntry := PathLaunchEnvelope + "/devices/" + mbAlice
	bobEntry := PathLaunchEnvelope + "/devices/" + mbBob
	h.set(recipePath, map[string]any{"repo": "owner/safe", "task": "safe-recipe"})
	h.set(aliceEntry, map[string]any{"repos": []any{"owner/one"}, "per_hour": 2, "daily_usd": 5})
	h.set(bobEntry, map[string]any{"repos": []any{"owner/two"}, "per_hour": 1, "daily_usd": 2})

	alice, bob := h.phone(mbAlice), h.phone(mbBob)
	// The positive control: each phone reaches a family USER is granted, so the
	// refusals below are this node's rule and not an empty tree's.
	h.mustRead(alice, budget.PathCapsGlobal)
	h.mustRead(bob, budget.PathCapsGlobal)

	// Each device reads the installation's recipe and its own entry, and is
	// refused the other's — which is the other's own token's to read.
	h.mustRead(alice, recipePath)
	h.mustRead(alice, aliceEntry)
	h.mustReadDenied(alice, bobEntry)
	h.mustRead(bob, recipePath)
	h.mustRead(bob, bobEntry)
	h.mustReadDenied(bob, aliceEntry)
	// The two parents and the node above them are refused: the grants are per
	// child, and a read on any of them would hand the recipe and every device's
	// entry to every paired phone.
	for _, p := range []string{"config/policy", PathLaunchEnvelope, PathLaunchEnvelope + "/devices"} {
		h.mustReadDenied(alice, p)
	}

	// A device whose node was never written reads neither the recipe nor its own
	// entry, and the caps family every paired device reads is refused to it too:
	// D166's conjunct is what ties the envelope to the pairing, so an account
	// that was never paired is refused everywhere at once. The account is one
	// nothing paired — alice's and bob's own nodes are written above, which is
	// what makes their reads pass — and its entry is really there, so the
	// refusal is the missing device node and not an absent path.
	const neverPairedUid = "never-paired-uid"
	neverPairedEntry := PathLaunchEnvelope + "/devices/" + neverPairedUid
	h.set(neverPairedEntry, map[string]any{"repos": []any{"owner/one"}, "per_hour": 2, "daily_usd": 5})
	neverPaired := h.unpaired(neverPairedUid)
	h.mustReadDenied(neverPaired, budget.PathCapsGlobal)
	h.mustReadDenied(neverPaired, recipePath)
	h.mustReadDenied(neverPaired, neverPairedEntry)
	// And deleting a live device's node takes its entry away with it, with no
	// token expiring in between — which is `fugaro pair revoke`'s reach.
	h.unpairDevice(mbAlice)
	h.mustReadDenied(alice, recipePath)
	h.mustReadDenied(alice, aliceEntry)
	h.pairDevice(mbAlice)

	// No other identity reaches the envelope, and each reaches a family the
	// rules do grant it in the same database.
	run := h.run("aurora", "r1")
	minted := h.minter("svc-minter-1")
	consumer := h.consumer(mbAlice)
	job := h.reportsJob(projectID)
	h.mustRead(run, budget.PathCapsGlobal)
	h.mustRead(minted, budget.PathCapsGlobal)
	h.mustAllow(consumer, "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.pairing(pairUid), "its own pairing request", put(request(pairUid), aRequest()))
	for _, c := range []*rtdb.Client{run, minted, consumer, h.pairing(pairUid), job} {
		h.mustReadDenied(c, recipePath)
		h.mustReadDenied(c, aliceEntry)
	}

	// Nothing writes it: not the phone it is granted to, not a run, not an
	// unpaired account, and not with a whole-node replace or a single child.
	doc := map[string]any{"repos": []any{"owner/one", "owner/other"}, "per_hour": 100, "daily_usd": 1000}
	h.mustDeny(alice, "the phone widening its own repository allow-list", put(aliceEntry, doc))
	h.mustDeny(alice, "the phone raising its own cost ceiling", put(aliceEntry+"/daily_usd", 1000))
	h.mustDeny(alice, "the phone writing another device's entry", put(bobEntry, doc))
	h.mustDeny(alice, "the phone writing the named safe recipe", put(recipePath, map[string]any{"repo": "owner/other"}))
	h.mustDeny(run, "a run writing a device's envelope entry", put(aliceEntry, doc))
	h.mustDeny(neverPaired, "an unpaired account writing an entry", put(aliceEntry, doc))

	// And the denials changed nothing: the device still reads its own values.
	h.mustRead(alice, aliceEntry)
	h.wantValue(aliceEntry+"/daily_usd", "5")
}

func TestOrchestratorReportsJobReadsEscalationOnly(t *testing.T) {
	const (
		orchItem       = orchState + "/work_items/w1"
		orchEscalation = orchItem + "/escalation"
		ledgerRepo     = "owner/repo"
		ledgerRun      = "20261116-090000-abcd"
	)
	h := newHarness(t)
	h.seed(leased(2 * usd))
	h.set(orchState, map[string]any{"schemaVersion": 1, "phase": "idle"})
	h.set(orchItem, map[string]any{"state": "escalated"})
	h.set(orchEscalation, map[string]any{
		"repo": ledgerRepo, "run_id": ledgerRun,
		"rung": "R3", "entered_at": "2026-11-16T09:00:00Z", "outer_attempts": 2,
		"transitions": []map[string]any{
			{"to": "R0", "reason": "rebase", "at": "2026-11-16T08:00:00Z"},
			{"from": "R0", "to": "R3", "reason": "age", "at": "2026-11-16T09:00:00Z"},
		},
	})

	// The read the design grants, and the node it is granted on: the ledger's
	// escalation block of a work item, which is where the ladder's record of the
	// rungs it climbed lives.
	job := h.reportsJob(projectID)
	h.mustRead(job, orchEscalation)

	// And the block the read returns is a ledger this build reads, with the two
	// names a reader that cannot reach the item beside it needs: the repository
	// its per-repository report is about, and the run its chain is found by.
	var raw json.RawMessage
	if _, err := job.Get(context.Background(), orchEscalation, &raw); err != nil {
		t.Fatalf("read /%s: %v", orchEscalation, err)
	}
	var block orchestrator.Escalation
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&block); err != nil {
		t.Fatalf("the block the reports job read is not a ledger this build reads: %v\n%s", err, raw)
	}
	if block.Repo != ledgerRepo || block.RunID != ledgerRun {
		t.Fatalf("the block the reports job read is on %q and names the run %q, want %q and %q", block.Repo, block.RunID, ledgerRepo, ledgerRun)
	}
	if block.Rung != escalation.Rung("R3") || len(block.Transitions) != 2 {
		t.Fatalf("the block came back as rung %q with %d transitions", block.Rung, len(block.Transitions))
	}

	// And nothing else of the database. The work items and the state node above
	// the block are the widening this test exists for — a read there reaches
	// every work item's every field — and the caps family is a positive control
	// of the other kind: a node that holds data and that the identities the
	// rules do admit read, refused to this one.
	for _, p := range []string{
		PathOrchestrator,
		PathOrchestrator + "/" + orchID,
		orchState,
		orchState + "/work_items",
		orchItem,
		budget.PathCapsGlobal,
	} {
		h.mustReadDenied(job, p)
	}

	// The claim is this installation's: a token minted for another one is
	// refused, and so is a client that carries no rj claim at all — this is the
	// state a token is in before the job's mint exists (E12 ships the grant and
	// not the mint), and it is refused for the guard's reason and not the
	// equality's.
	h.mustReadDenied(h.reportsJob("another-installation"), orchEscalation)
	h.mustReadDenied(h.as(claims{uid: "no-claims", fp: "-"}), orchEscalation)

	// And the guard itself, at the one state where it is the only thing between
	// a signed-in client and the block: an installation whose /fugaro/project
	// was never written (the generation-0 state check 82 documents), where a
	// claim-less equality is null == null. Both a client with no rj and the
	// reports job's own token are refused there.
	h.adminDo(http.MethodDelete, "fugaro/project", nil)
	h.mustReadDenied(h.as(claims{uid: "no-claims", fp: "-"}), orchEscalation)
	h.mustReadDenied(h.reportsJob(projectID), orchEscalation)
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

	// The two token identities the cases below add hold a write the rules do
	// grant them elsewhere -- the consumer writes its mailbox's heartbeat, the
	// pairing session writes its own request -- so their refusals below are this
	// node's rule and not claims the rules ignore wherever they appear.
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.pairing(pairUid), "its own pairing request", put(request(pairUid), aRequest()))

	for _, c := range []struct {
		who    string
		client *rtdb.Client
		write  func() map[string]any
	}{
		{"a run's token", run, func() map[string]any { return put(orchState, map[string]any{"phase": "running"}) }},
		{"a run's token", run, func() map[string]any { return put(orchSibling, 1) }},
		{"a run's token", run, func() map[string]any { return put(orchState+"/phase", "running") }},
		{"a run's token", run, func() map[string]any { return map[string]any{orchState: nil} }},
		{"a minter-minted token", minted, func() map[string]any { return put(orchState+"/phase", "running") }},
		{"a minter-minted token", minted, func() map[string]any { return put(orchLease, map[string]any{"until": 1}) }},
		// CG6 of the console graph plan (H7): the rest of the identities the
		// rules evaluate are refused the write too, an account with no device
		// node among them.
		{"a mailbox consumer's token", h.consumer(mbAlice), func() map[string]any { return put(orchState+"/phase", "running") }},
		{"a pairing session's token", h.pairing(pairUid), func() map[string]any { return put(orchLease, map[string]any{"until": 1}) }},
		{"a token of an account with no device node", h.unpaired(mbBob), func() map[string]any { return put(orchState+"/phase", "running") }},
	} {
		h.mustDeny(c.client, c.who+" writing the orchestrator state", c.write())
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

// TestRunTokenCannotWritePlanOrBacklogNode is X0's half of that rule (D2735,
// F34): the node per imported plan and the node for the standing backlog sit
// under the same `orchestrator` subtree, so the one rule that closes the
// subtree is the whole of what closes them. No rule names either node — the
// plan's own boundary makes any RTDB rules change an ask-first, and the
// reconciliation's answer is that these two need none — so what this test adds
// to TestOrchestratorRefusesEveryTokenWriter is the two nodes by name: the
// person's own credential writes them, over IAM, and reads them back, and every
// identity the rules evaluate is refused the write.
//
// The positive control at the top is what keeps the refusals from passing for
// the wrong reason: the root is already `".write": false`, so a node the
// template never mentions is refused everything by a rule nobody reading the
// file can see. The person's write proves these two hold data, and the run's
// and the minter's reads of the caps family prove the refusals are this
// subtree's rule and not an empty database's.
func TestRunTokenCannotWritePlanOrBacklogNode(t *testing.T) {
	const planFolder = "orchestrator-graph-estimates"
	plan := mustPlanPath(t, planFolder)
	backlog := mustOrchPath(t, orchestrator.PathBacklog)
	plans := mustOrchPath(t, orchestrator.PathPlans)
	planItem := plan + "/work_items/wi-x0"
	backlogItem := backlog + "/work_items/wi-b1"

	h := newHarness(t)
	h.seed(leased(2 * usd))
	alice, run := h.phone(mbAlice), h.run("aurora", "r1")
	minted := h.minter("svc-minter-1")
	// Each of the three reaches a family the rules do grant it, so the refusals
	// below are refusals of these nodes rather than of a database that denies
	// everything.
	h.mustRead(alice, budget.PathCapsGlobal)
	h.mustRead(run, budget.PathCapsGlobal)
	h.mustRead(minted, budget.PathCapsGlobal)

	// The person's own credential, over IAM: the harness's admin client is that
	// path, and it is the only writer these two nodes have. What it leaves is
	// what the readers below read and the writers below are refused.
	h.set(plan, map[string]any{"schema_version": 4, "agent_id": orchID, "role": "orchestrator", "work_items": []any{}})
	h.set(backlog, map[string]any{"schema_version": 4, "agent_id": orchID, "role": "orchestrator", "work_items": []any{}})
	h.set(planItem, map[string]any{"id": "wi-x0", "phase": "queued"})
	h.set(backlogItem, map[string]any{"id": "wi-b1", "phase": "queued"})
	h.wantValue(planItem+"/phase", `"queued"`)
	h.wantValue(backlogItem+"/phase", `"queued"`)

	// The signed-in person reads both nodes, their parent and the items in
	// them: the grant is at the subtree, so a read of a plan's node or the
	// backlog's is admitted exactly as a read of the state node is.
	for _, p := range []string{plans, plan, planItem, backlog, backlogItem} {
		h.mustRead(alice, p)
	}
	// A run's token and a token the launcher's minter made are not readers of
	// them: these are the daemon's state, not a family a run may read.
	for _, p := range []string{plans, plan, planItem, backlog, backlogItem} {
		h.mustReadDenied(run, p)
		h.mustReadDenied(minted, p)
	}

	// The phone whose own `mu` claim is the person's, and the same claim on a
	// token the minter made: the first is the identity a paired phone carries
	// and the second is the shape a forged "write own" claim would take, and
	// both are readers of this subtree and neither is a writer of it.
	forged := h.as(claims{mu: mbAlice, fp: "-"})
	h.mustRead(forged, planItem)

	for _, c := range []struct {
		who    string
		client *rtdb.Client
		write  func() map[string]any
	}{
		{"a run's token", run, func() map[string]any { return put(plan, map[string]any{"schema_version": 4}) }},
		{"a run's token", run, func() map[string]any { return put(planItem+"/phase", "running") }},
		{"a run's token", run, func() map[string]any { return put(backlog, map[string]any{"schema_version": 4}) }},
		{"a run's token", run, func() map[string]any { return put(backlogItem+"/phase", "running") }},
		{"a run's token", run, func() map[string]any { return map[string]any{planItem: nil} }},
		{"a run's token", run, func() map[string]any { return map[string]any{backlogItem: nil} }},
		{"a minter-minted token", minted, func() map[string]any { return put(planItem+"/phase", "running") }},
		{"a minter-minted token", minted, func() map[string]any { return put(backlogItem+"/phase", "running") }},
		{"a mailbox consumer's token", h.consumer(mbAlice), func() map[string]any { return put(planItem+"/phase", "running") }},
		{"a pairing session's token", h.pairing(pairUid), func() map[string]any { return put(backlog, map[string]any{"schema_version": 4}) }},
		{"a token of an account with no device node", h.unpaired(mbBob), func() map[string]any { return put(backlogItem+"/phase", "running") }},
		{"the phone's own token", alice, func() map[string]any { return put(planItem+"/phase", "running") }},
		{"a minted token carrying the person's own mu claim", forged, func() map[string]any { return put(backlogItem+"/phase", "running") }},
	} {
		h.mustDeny(c.client, c.who+" writing the plan or the backlog node", c.write())
	}
	// And nothing the person's write left behind moved.
	h.wantValue(planItem+"/phase", `"queued"`)
	h.wantValue(backlogItem+"/phase", `"queued"`)
}
