package infra

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The resident orchestrator's block is the installation root's section 4.3
// table — the account fugaro-orch, the launcher's role, the runs bucket's two
// grants, the token-minter role on the budget signer and the job runner's role
// on every job — and CarriedOrchestrator is how every path that re-serializes
// the root's tfvars keeps it. It is the same shape of carry as
// CarriedIntake and CarriedManagedDeployerScoped: a read-modify-write of ONE
// key, taken from the documents the workdir holds, so a document rebuilt from a
// spec — which never carries the key, Orchestrator composing the one write the
// installation's own opt-in asks for —
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

	// The document being carried FROM wins over one vars already carry, which
	// is what makes the carry order-based rather than presence-based: prepare
	// runs this over the snapshot first and over the tfvars the workdir holds
	// NOW second (init.go), so the run's own write — the opt-in's block, and
	// the image the credentials-service stage re-pinned inside it — is the one
	// that survives, and the older document cannot revert it.
	//
	// The other rule would be the one that broke the re-pin: with the older
	// document winning, an installation that opted in would keep the release
	// behind's image in its block for ever, and every update would rewrite the
	// pin the stage just moved.
	own := []byte(`{"project": "proj-1234", "orchestrator": {"account_id": "fugaro-orch", "firebase_project": "fp-2", "signer_account_id": "fugaro-token-signer"}}`)
	older := []byte(`{"project": "proj-1234", "orchestrator": {"account_id": "fugaro-orch", "firebase_project": "fp-9", "signer_account_id": "fugaro-token-signer"}}`)
	got2, err := CarriedOrchestrator(own, older)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]any
	if err := json.Unmarshal(got2, &carried); err != nil {
		t.Fatal(err)
	}
	if block, _ := carried[OrchestratorKey].(map[string]any); block == nil || block["firebase_project"] != "fp-9" {
		t.Errorf("the carried block is %v, want the one the document being carried from holds (fp-9): the pair is read oldest first, so the tfvars the run itself wrote is the answer", block)
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

// The block's presence is the installation's own statement that it opted in,
// and it is the answer everything that reads the account's existence reads: the
// module creates the account, its five bindings, the credentials service's
// caller class and the invoker grant from this one variable and nulls them all
// without it, so a reader that took the block's absence for a block would be
// reading an installation into having an identity it does not.
//
// Mutation (run, restore): answer `ok` alone where the two are compared and the
// null row reports an installation that wrote the key to say no as one that
// opted in — the address a daemon would then mint as belongs to no project.
func TestOrchestratorRecordedReadsTheInstallationsOwnSwitch(t *testing.T) {
	for name, tc := range map[string]struct {
		vars []byte
		want bool
	}{
		"the block":                 {[]byte(`{"project": "proj-1234", "orchestrator": {"account_id": "fugaro-orch"}}`), true},
		"the key and nothing in it": {[]byte(`{"orchestrator": {}}`), true},
		"no key":                    {[]byte(`{"project": "proj-1234", "region": "us-east5"}`), false},
		"the key is null":           {[]byte(`{"project": "proj-1234", "orchestrator": null}`), false},
		"no document at all":        {nil, false},
		"a document with nothing":   {[]byte(`{}`), false},
	} {
		got, err := OrchestratorRecorded(tc.vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: OrchestratorRecorded(%s) = %v, want %v", name, tc.vars, got, tc.want)
		}
	}
	for _, vars := range [][]byte{[]byte("not json"), []byte(""), []byte("[1,2]"), []byte(`{"orchestrator": {`)} {
		got, err := OrchestratorRecorded(vars)
		if err == nil {
			t.Errorf("OrchestratorRecorded(%q) = %v, nil error, want a refusal: a document this program cannot read is not one that has not opted in", vars, got)
			continue
		}
		if !strings.Contains(err.Error(), VarsFile) {
			t.Errorf("OrchestratorRecorded(%q) refused with %q, want the file named (%s)", vars, err, VarsFile)
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

// orchestratorPin is the released pin a `fugaro update` resolves once and
// hands to every service it moves: the credentials service's registry, the
// GitHub owner the release is published under, and a digest. It is the same
// shape internal/cli's converge fixtures use, because the image Orchestrator
// admits is exactly the image the credentials stage already pinned.
func orchestratorPin(t *testing.T, version string) string {
	t.Helper()
	return "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:" + version + "@sha256:" + strings.Repeat("ab", 32)
}

// TestOrchestratorSpecIsDerivedFromTheInstallation is the writer's whole
// premise: every one of the block's eight fields comes from what the
// installation already holds, and none of them is a value an operator types.
// The account, the service and the tick are internal/infra's own constants
// (the module's literals, which TestInstallationVarsMatchModule and the plan
// tests read back); the two names of the token-minter row are the Firebase
// root's signer (SignerAccountID, the ID internal/infra hands that root) and
// the budget block `fugaro update --firebase-project` wrote; the database is
// that same block's URL; the scheduler region is the config's, resolved the
// way the history job's and the coordinator's are; and the image is the
// credentials service's resolved pin, handed in, because one released binary
// serves all four (VoiceServiceSpec's "one release, one pin, four services").
//
// The two refusals are the ones the brief names: an installation with no
// budget block gets `fugaro update --firebase-project` as the fix rather than
// a document the module's validations would refuse, and a tag is refused
// before it reaches a tfvars at all — the module holds the same rule, and the
// operator holds write access to the registry the image comes from.
func TestOrchestratorSpecIsDerivedFromTheInstallation(t *testing.T) {
	lc := credentialsLC(t)
	pin := orchestratorPin(t, "1.2.3")
	wantRegion, err := gcp.SchedulerRegion(lc.Region)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Orchestrator(lc, pin)
	if err != nil {
		t.Fatal(err)
	}
	want := OrchestratorSpec{
		AccountID:       OrchestratorAccountID,
		FirebaseProject: credentialsFP,
		SignerAccountID: SignerAccountID,
		Service:         OrchestratorServiceName,
		Image:           pin,
		SchedulerJob:    OrchestratorSchedulerJob,
		RTDBURL:         "https://" + credentialsFP + "-default-rtdb.firebaseio.com",
		SchedulerRegion: wantRegion,
	}
	if got != want {
		t.Errorf("Orchestrator = %+v, want %+v: every field is the installation's own, and a field this composer invented is one the plan would create without the owner asking", got, want)
	}

	// The config's own scheduler region wins over the nearest served one, the
	// rule history.tf's and coordinator.tf's blocks follow.
	lc.SchedulerRegion = "us-central1"
	got, err = Orchestrator(lc, pin)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchedulerRegion != "us-central1" {
		t.Errorf("scheduler region = %q, want the config's own us-central1: Cloud Scheduler is not offered in every Cloud Run region, so the config is where an installation that needed another one said so", got.SchedulerRegion)
	}
	lc.SchedulerRegion = ""

	// A tag, and a digest this program cannot read: refused here rather than
	// written into a document the plan refuses.
	for _, image := range []string{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3",
		"",
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3@sha256:" + strings.Repeat("AB", 32),
	} {
		_, err := Orchestrator(lc, image)
		if err == nil || !strings.Contains(err.Error(), "@sha256:") {
			t.Errorf("Orchestrator(%q) = %v, want a refusal naming the digest rule: the module holds the same rule, and a tag is mutable by whoever holds the registry", image, err)
		}
	}

	// A database the module's own validation would refuse is refused here, by
	// name, with the command that writes a real one.
	lc.Budget.RTDBURL = credentialsFP + "-default-rtdb.firebaseio.com"
	if _, err := Orchestrator(lc, pin); err == nil || !strings.Contains(err.Error(), "budget.rtdb_url") {
		t.Errorf("Orchestrator with a database URL that is not one = %v, want a refusal naming budget.rtdb_url", err)
	}
	lc.Budget.RTDBURL = "https://" + credentialsFP + "-default-rtdb.firebaseio.com"

	// No Firebase backend at all: the installation never ran the Firebase
	// stage, and this composer says which door does that rather than writing
	// an account whose process has no node to read.
	lc.Budget = nil
	if _, err := Orchestrator(lc, pin); err == nil || !strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("Orchestrator with no budget block = %v, want a refusal naming fugaro update --firebase-project", err)
	}
}

// TestSetOrchestratorWritesTheOneKeyAndLeavesTheRest is the write's own
// contract, and it is the reason the writer is a read-modify-write and not a
// document rebuilt from a spec: the tfvars on disk carries the run's discovery
// (the live project secrets it found, its legacy registry, its imports), and a
// document rebuilt without them plans their destruction. SetOrchestrator is
// SetIntake's shape on the key that turns the account on.
func TestSetOrchestratorWritesTheOneKeyAndLeavesTheRest(t *testing.T) {
	lc := credentialsLC(t)
	spec, err := Orchestrator(lc, orchestratorPin(t, "1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	const held = `{"project": "proj-1234", "region": "us-east5", "project_secrets": {"app": "aurora-app-ab12"}}`

	got, err := SetOrchestrator([]byte(held), spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the written document is not a tfvars document: %v\n%s", err, got)
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(held), &before); err != nil {
		t.Fatal(err)
	}
	block, _ := doc[OrchestratorKey].(map[string]any)
	if block == nil {
		t.Fatalf("no orchestrator block in the document the writer handed Terraform:\n%s", got)
	}
	if block["account_id"] != OrchestratorAccountID || block["scheduler_job"] != OrchestratorSchedulerJob {
		t.Errorf("the written block is %v, want the account and the tick's names", block)
	}
	delete(doc, OrchestratorKey)
	if !mapsEqual(before, doc) {
		t.Errorf("the write changed more than the one key:\nwant %v\ngot  %v", before, doc)
	}

	// And it reads back as the block the installation holds: the reader the
	// carry and the re-pin both ask answers true, whole.
	back, ok, err := OrchestratorOf(got)
	if err != nil || !ok {
		t.Fatalf("OrchestratorOf(the written document) = %+v, %v, %v, want the block", back, ok, err)
	}
	if back != spec {
		t.Errorf("the block read back is %+v, want %+v", back, spec)
	}
}

// TestSetOrchestratorImageMovesOnlyTheImage is the update's convergence half
// at the unit level: the block is state — the account, its bindings, the
// pairing container and the service — and the ONLY field a later release moves
// is the image, onto the pin that release resolved for the credentials service
// it shares the binary with. Writing the block here instead of re-pinning it
// is refused: that would turn the resident orchestrator on for an installation
// whose owner never asked.
func TestSetOrchestratorImageMovesOnlyTheImage(t *testing.T) {
	lc := credentialsLC(t)
	spec, err := Orchestrator(lc, orchestratorPin(t, "1.2.2"))
	if err != nil {
		t.Fatal(err)
	}
	held, err := SetOrchestrator([]byte(`{"project": "proj-1234", "project_secrets": {"app": "aurora-app-ab12"}}`), spec)
	if err != nil {
		t.Fatal(err)
	}

	next := orchestratorPin(t, "1.2.3")
	moved, err := SetOrchestratorImage(held, next)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := OrchestratorOf(moved)
	if err != nil || !ok {
		t.Fatalf("the re-pinned document has no readable block: %+v, %v, %v", got, ok, err)
	}
	if got.Image != next {
		t.Errorf("the orchestrator's image is %q, want the release's pin %q: one binary serves the credentials service, the intake, the voice hop and the orchestrator, and a run that moved three of them and left this one runs the release behind's process against the state it wrote", got.Image, next)
	}
	var doc, was map[string]any
	if err := json.Unmarshal(moved, &doc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(held, &was); err != nil {
		t.Fatal(err)
	}
	delete(doc, OrchestratorKey)
	delete(was, OrchestratorKey)
	if !mapsEqual(was, doc) {
		t.Errorf("the re-pin changed something besides the orchestrator block:\nwant %v\ngot  %v", was, doc)
	}
	if spec.Image == next {
		t.Fatal("the fixture's two pins are the same: this test proves nothing")
	}

	// A document with no block is refused, and a tag beside it too.
	if _, err := SetOrchestratorImage([]byte(`{"project": "proj-1234"}`), next); err == nil || !strings.Contains(err.Error(), "no orchestrator") {
		t.Errorf("re-pinning an installation with no orchestrator block = %v, want a refusal: a block written here turns the account and its bindings on for an owner who never asked", err)
	}
	if _, err := SetOrchestratorImage(held, "fugaro-credentials:1.2.3"); err == nil || !strings.Contains(err.Error(), "@sha256:") {
		t.Errorf("re-pinning to a tag = %v, want a refusal naming the digest rule", err)
	}
}
