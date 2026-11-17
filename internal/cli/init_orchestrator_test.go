package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
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
