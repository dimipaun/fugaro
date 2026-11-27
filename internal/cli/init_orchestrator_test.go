package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The installation's tfvars are rebuilt from the spec on every path that
// re-serializes the root, and the resident orchestrator's block is one of the
// keys the spec never carries: it is the mobile orchestrator design's section
// 4.3 table — the account fugaro-orch, the launcher's role, the runs bucket's
// conditioned write and its unconditioned read, the token-minter role on the
// budget signer and the job runner's role on every job — and no writer in this
// tree composes it yet, so the one installation that has it is one whose owner
// put the block in terraform.tfvars.json by hand (mobile plan task P5.1b).
//
// Without the carry in prepare, that installation's next `fugaro update` hands
// Terraform a document with no orchestrator block at all, and the plan is the
// account's and its bindings' removal. The account is a google_service_account,
// which D1551 classifies as data-bearing — "an identity whose keys and grants
// are lost with it" — and the removal rule refuses the plan (internal/infra/tf's
// RefusesRemovalType), so the run stops and every later run stops the same way:
// the HARD RULE's failure mode, an installation that cannot be updated.
//
// Mutation (run, restore): drop the infra.CarriedOrchestrator loop from prepare
// and this test reports a document with no orchestrator in it.
func TestPrepareCarriesTheOrchestratorBlockAHandOptedInstallationHolds(t *testing.T) {
	dir := t.TempDir()
	// The block a hand-opted installation holds, written where the workdir reads
	// it from BEFORE it rebuilds the tree: prepare reads PrevVars, because
	// PrepareWorkdir has already deleted this file by the time it runs.
	const held = `{
  "project": "proj-1234",
  "orchestrator": {
    "account_id": "fugaro-orch",
    "firebase_project": "fp-2",
    "signer_account_id": "fugaro-token-signer"
  }
}`
	path := infra.WorkdirVarsPath(dir, "installation")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(held), 0o600); err != nil {
		t.Fatal(err)
	}

	wd, err := infra.PrepareWorkdir(dir, "installation")
	if err != nil {
		t.Fatal(err)
	}
	spec := infra.InstallationSpec{
		Project:     "proj-1234",
		Region:      "us-east5",
		RunsBucket:  "fugaro-runs-proj-1234",
		StateBucket: "fugaro-state-proj-1234",
	}
	if _, err := prepare(wd, spec, infra.Imports{}, nil, false); err != nil {
		t.Fatalf("prepare refused an installation that holds a resident orchestrator: %v", err)
	}
	now, err := wd.Vars()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(now, &doc); err != nil {
		t.Fatalf("the tfvars this run wrote are not readable: %v\n%s", err, now)
	}
	block, _ := doc[infra.OrchestratorKey].(map[string]any)
	if block == nil {
		t.Fatalf("the installation opted into a resident orchestrator and the document this run hands Terraform has no orchestrator block in it:\n%s\nThe account and its five bindings are state, and this document is the plan of their removal — which the D1551 removal rule refuses for a google_service_account, so this update and every update after it stops", now)
	}
	for key, want := range map[string]string{
		"account_id":        infra.OrchestratorAccountID,
		"firebase_project":  "fp-2",
		"signer_account_id": "fugaro-token-signer",
	} {
		if got, _ := block[key].(string); got != want {
			t.Errorf("the carried block's %s is %q, want %q: the block is the installation's own record and is carried whole", key, got, want)
		}
	}

	// And an installation that never opted in gets no block: a run that invented
	// one would create an account and five bindings nobody asked for, on the one
	// account whose envelope the design enumerates.
	plain := t.TempDir()
	wdPlain, err := infra.PrepareWorkdir(plain, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(wdPlain, spec, infra.Imports{}, nil, false); err != nil {
		t.Fatal(err)
	}
	nowPlain, err := wdPlain.Vars()
	if err != nil {
		t.Fatal(err)
	}
	var plainDoc map[string]any
	if err := json.Unmarshal(nowPlain, &plainDoc); err != nil {
		t.Fatal(err)
	}
	if block, _ := plainDoc[infra.OrchestratorKey].(map[string]any); block != nil {
		t.Errorf("an installation that never opted in was handed an orchestrator block: %v\n%s", block, nowPlain)
	}
}

// TestUpdateConvergesADaemonInstallationInOneCommand is the carry above at the
// door a person really runs, over the change F19 and O2 make: the account the
// credentials service announces as its caller class and binds
// roles/run.invoker for comes from the installation's own orchestrator block
// (credentials.tf and orchestrator.tf both render it from var.orchestrator), so
// an installation that opted in is one where ONE `fugaro update` is the whole
// upgrade — the block is carried into the document this run hands Terraform,
// and the apply that replaces the previous release's revision with this
// release's image is the same apply that renders the class and the invoker
// grant. Nothing else is asked of the owner, and no second command exists.
//
// Beyond converging, this pins the identity read twice, which is the whole of
// what makes the daemon admitted: the address the module announces — the
// block's account_id at the installation's project, which is exactly
// google_service_account.orchestrator[0].email — is the address the daemon mints
// its token as (orchestratorIdentityOf, orchestrator_identity.go), and the URL
// the module serves the audience from is the URL this same update publishes into
// the local config the daemon reads it out of. A pair that disagreed would be a
// token minted for an audience no revision answers to, or one the class refuses,
// with the service, the block and the daemon all looking healthy.
//
// The run in flight is the HARD RULE's other half
// (TestUpdateLeavesARunInFlightRunning): the step is named, no run is asked to
// finish first, and everything the run holds is still writable afterwards.
//
// Mutation (run, restore): drop the infra.CarriedOrchestrator loop from prepare
// and the assertion below fires — the document this run hands Terraform has no
// orchestrator block, which is the plan of the account's and its bindings'
// removal. The fixture's terraform is a fake and applies that document anyway;
// a real installation is refused there by the D1551 removal rule for a
// google_service_account, which is the HARD RULE's failure mode — an
// installation that cannot be updated at all.
func TestUpdateConvergesADaemonInstallationInOneCommand(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	// The service a previous release deployed — a revision whose environment
	// predates the class, so it answers both routes with the class's refusal —
	// and this release's image in the fake source, so the apply below really
	// replaces it.
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", installationCredentialsPlan("update", r.credentialsRef())...)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.save(t)
	holdOrchestratorBlock(t, r)
	run := startRunInFlight(t, r)

	line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"}))
	if !strings.Contains(line, "1 run in flight") || !strings.Contains(line, "waits for none") {
		t.Errorf("the update over a daemon installation does not name the run in flight and that it waits for none:\n%s", line)
	}

	doc := r.tfvars(t)
	block, _ := doc[infra.OrchestratorKey].(map[string]any)
	if block == nil {
		t.Fatalf("the installation opted into a resident orchestrator and the document this update handed Terraform has no orchestrator block in it:\n%v\nThe class FUGARO_ORCHESTRATOR_ACCOUNTS and the account's roles/run.invoker on the credentials service are rendered from this block, so the service this run deployed admits the daemon on no installation", doc)
	}
	accountID, _ := block["account_id"].(string)
	if accountID != infra.OrchestratorAccountID {
		t.Errorf("the carried block's account_id is %q, want %q: the module creates the account under that ID and `fugaro doctor` reads it back by it", accountID, infra.OrchestratorAccountID)
	}

	cfg := updatedConfig(t)
	announced := accountID + "@" + cfg.GCPProject + ".iam.gserviceaccount.com"
	if got := orchestratorIdentityOf(cfg).account; got != announced {
		t.Errorf("the daemon mints its token as %q while the service's class is rendered from the installation's block, which announces %q: the class admits an address no daemon of this installation ever presents", got, announced)
	}
	svc := credentialsServiceEntry(t, r.imagesRig)
	if cfg.CredentialsService == nil {
		t.Fatal("the update published no credentials service into the local config, so the daemon has no audience to mint for")
	}
	if got, _ := svc["url"].(string); got != cfg.CredentialsService.URL {
		t.Errorf("the service's own FUGARO_CREDENTIALS_SERVICE_URL is %q and the daemon mints for %q: the audience the class checks and the audience the daemon uses have to be one string", got, cfg.CredentialsService.URL)
	}

	// The run in flight is where the update found it: its record is the one it
	// bootstrapped with, not one this step rewrote or removed.
	rec, err := run.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatalf("the run's record could not be read after the update: %v", err)
	}
	if rec.Status != runstore.StatusRunning || rec.Stage != "implement" {
		t.Errorf("the run's record reads status %q stage %q after the update, want the running/implement it bootstrapped with: the update rewrote a record a run holds", rec.Status, rec.Stage)
	}
	run.laterWrites(t, r)
}

// holdOrchestratorBlock writes the installation's orchestrator block into its
// tfvars, which is where the one installation that runs a resident daemon holds
// it: no writer in this tree composes the block (P5.1b), so its owner put it in
// terraform.tfvars.json by hand. It goes in BEFORE the run, because that is what
// PrepareWorkdir snapshots — prepare carries the block forward from the document
// as the run found it, and a test that wrote it afterwards would be asserting on
// its own edit rather than on the carry.
//
// The block is the whole shape the module's variable requires, which is also
// what makes the assertion above a carry of the installation's own record rather
// than of one key of it.
func holdOrchestratorBlock(t *testing.T, r *convergeRig) {
	t.Helper()
	path := filepath.Join(r.imagesRig.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fixture installation holds no tfvars to opt in with: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the fixture installation's tfvars are not readable: %v\n%s", err, b)
	}
	doc[infra.OrchestratorKey] = map[string]any{
		"account_id":        infra.OrchestratorAccountID,
		"firebase_project":  fpID,
		"signer_account_id": infra.SignerAccountID,
		"service":           "fugaro-orchestrator",
		"image":             pinnedRef("base", convergeRelease),
		"rtdb_url":          r.db.URL,
		"scheduler_job":     "fugaro-orchestrator-tick",
		"scheduler_region":  "us-east5",
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
