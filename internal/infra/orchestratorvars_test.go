package infra

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The resident orchestrator's block is the installation root's section 4.3
// table — the account fugaro-orch, the launcher's role, the runs bucket's two
// grants, the token-minter role on the budget signer and the job runner's role
// on every job — and CarriedOrchestrator is how every path that re-serializes
// the root's tfvars keeps it. It is the same shape of carry as
// CarriedIntake and CarriedManagedDeployerScoped: a read-modify-write of ONE
// key, taken from the documents the workdir holds, so a document rebuilt from a
// spec — which never carries the key, no writer in this tree composing one —
// does not plan the account's destruction. That plan is refused (the removal of
// a google_service_account is data-bearing under D1551, RefusesRemovalType), and
// the refusal repeats on every later update, which is what makes the carry the
// difference between an installation that opted in converging and one that
// stopped.
//
// Mutation (run, restore): drop the `doc[OrchestratorKey] = block` write and the
// first half of this test reports a document with no orchestrator in it.
func TestCarriedOrchestratorCarriesTheBlockAHandOptedInstallationHolds(t *testing.T) {
	const held = `{"project": "proj-1234", "orchestrator": {"account_id": "fugaro-orch", "firebase_project": "fp-2", "signer_account_id": "fugaro-token-signer"}}`
	const fresh = `{"project": "proj-1234", "region": "us-east5"}`

	got, err := CarriedOrchestrator([]byte(fresh), []byte(held))
	if err != nil {
		t.Fatal(err)
	}
	var doc, previous map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(held), &previous); err != nil {
		t.Fatal(err)
	}
	block, _ := doc[OrchestratorKey].(map[string]any)
	if block == nil {
		t.Fatalf("the block the installation holds is not in the document this run hands Terraform:\n%s. The account and its bindings are STATE, and a document without the key is the plan of their removal — a google_service_account is data-bearing under D1551 and the removal rule refuses it, so every later update of this installation is refused too", got)
	}
	if !reflect.DeepEqual(block, previous[OrchestratorKey]) {
		t.Errorf("the carried block is %v, want it whole as the installation held it (%v): the block is the installation's own record of what it opted into, and a run that recomposed it would be re-deciding the account's grants", block, previous[OrchestratorKey])
	}

	// And nothing else moved: every other entry of the fresh document is still
	// there, and no key beside the one was added.
	var want map[string]any
	if err := json.Unmarshal([]byte(fresh), &want); err != nil {
		t.Fatal(err)
	}
	delete(doc, OrchestratorKey)
	if !mapsEqual(want, doc) {
		t.Errorf("the carried document changed beyond the one key:\nwant %v\ngot  %v", want, doc)
	}

	// A document that carries the key of its own stands: that is the release
	// that adds the writer, whose spec field is the caller's own answer, and
	// this carry never overwrites it.
	own := []byte(`{"project": "proj-1234", "orchestrator": {"account_id": "fugaro-orch", "firebase_project": "fp-9", "signer_account_id": "fugaro-token-signer"}}`)
	if got2, err := CarriedOrchestrator(own, []byte(held)); err != nil || string(got2) != string(own) {
		t.Errorf("a document with its own block = %s, %v, want it unchanged", got2, err)
	}

	// No key to carry — an installation that never opted in, and a workdir with
	// no tfvars at all (nil: a first run) — invents nothing: a block appearing
	// here would be an account and five bindings nobody asked for.
	for name, prev := range map[string][]byte{"no key in the previous document": []byte(fresh), "no previous document": nil} {
		got2, err := CarriedOrchestrator([]byte(fresh), prev)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got2) != fresh {
			t.Errorf("%s: the document changed: %s", name, got2)
		}
	}
}

// A previous document this program cannot read is an error and not "nothing to
// carry": the direction that fails closed is the one that refuses to write a
// document dropping the account.
func TestCarriedOrchestratorRefusesAnUnreadablePreviousDocument(t *testing.T) {
	const fresh = `{"project": "proj-1234"}`
	if _, err := CarriedOrchestrator([]byte(fresh), []byte("{not json")); err == nil {
		t.Error("an unreadable previous document carried nothing forward without an error: a run that cannot read the installation's tfvars plans the account's removal, and it has to stop instead")
	}
}
