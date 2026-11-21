package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The resident orchestrator's opt-in is the installation root's `orchestrator`
// variable (mobile plan tasks P5.1b and P5.1c; the owner's ruling of
// 2026-11-20), and these are the three properties the door owes it: that the
// block appears only when the owner asks for it, that every field in it is
// derived from what the installation already holds, and that the release's own
// pin reaches it on every later update the way it reaches the credentials
// service, the intake and the voice hop.

// orchestratorBlockIn reads the installation root's tfvars and returns the
// resident orchestrator's block, or nil when the document carries none.
func orchestratorBlockIn(t *testing.T, r *imagesRig) map[string]any {
	t.Helper()
	block, _ := r.tfvars(t)["orchestrator"].(map[string]any)
	return block
}

// seedOrchestratorBlock writes the block an installation that opted in under an
// EARLIER release holds: the account, the service, the tick and the database the
// run would derive, with the image that release's credentials service ran — the
// state a hand-written block (or one this writer wrote before this release) is
// in, and the one `fugaro update` has to carry and move.
func seedOrchestratorBlock(t *testing.T, r *imagesRig, image string) {
	t.Helper()
	path := filepath.Join(r.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["orchestrator"] = map[string]any{
		"account_id":        infra.OrchestratorAccountID,
		"firebase_project":  fpID,
		"signer_account_id": infra.SignerAccountID,
		"service":           infra.OrchestratorServiceName,
		"image":             image,
		"rtdb_url":          r.db.URL,
		"scheduler_job":     infra.OrchestratorSchedulerJob,
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

// orchestratorRig is the credentials rig as this door's own tests need it: an
// installation that has converged once (the mark the Firebase stage reads, the
// Firestore database it was set up with, and the outputs the run reads back),
// with the credentials service deployed under the release behind.
func orchestratorRig(t *testing.T) *imagesRig {
	t.Helper()
	r := credentialsRig(t)
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.fs.SetDatabase("us-east5")
	// The installation root's outputs as this path reads them: the credentials
	// service's own object (the audience fill), the intake's, and the history
	// account the Firebase stage reads back.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service":     map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
		"intake":                  intakeServiceOutput(),
		"history_service_account": historyAccount,
		"history_job":             nil,
	})}
	r.save(t)
	seedCredentialsService(t, r, oldCredentialsRef(convergePrev))
	seedIntake(t, r, oldCredentialsRef(convergePrev))
	return r
}

// seedIntake writes the secrets intake's block, which the same apply deploys
// and this release's run adds to an installation that has none. It is seeded
// because these tests are about the orchestrator and not about that addition:
// with both blocks present the installation root has nothing left to plan, and
// what the run changes is the three images alone.
func seedIntake(t *testing.T, r *imagesRig, image string) {
	t.Helper()
	path := filepath.Join(r.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := infra.SetIntake(b, infra.Intake(image))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOrchestratorBlockIsWrittenOnlyWhenOptedIn is the switch itself, in both
// directions and in the order the design's section 4.3 requires.
//
// A run that was not asked for a resident orchestrator writes no block at all:
// the account holds iam.serviceAccounts.signJwt on the budget signer, so it
// exists only where an owner asked for it, and an installation that has not
// opted in plans exactly what it planned before the variable existed.
//
// A run that WAS asked writes the block, and every field in it is the
// installation's own: the account, the service and the tick are internal/infra's
// constants, the signer is the Firebase root's own account ID, the project and
// the database are the budget block `fugaro update --firebase-project` wrote,
// the region is the config's, and the image is the pin this same run resolved
// for the credentials service — the binary whose `orchestrator serve`
// subcommand it runs.
//
// And an installation that has no credentials service is refused BY NAME rather
// than given a block: the account is that service's own caller class, so an
// installation without one would be an account whose every model call is
// refused. The refusal says which command deploys it first.
//
// Mutation (run, restore): drop the `--orchestrator` gate from
// ensureOrchestratorBlock and the first half fails with a block nobody asked
// for; drop the CredentialsServiceImageOf check and the third asks for a block
// the design's caller class cannot survive.
func TestOrchestratorBlockIsWrittenOnlyWhenOptedIn(t *testing.T) {
	r := orchestratorRig(t)

	// Nothing asked: no block, and the installation is left as it was.
	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("the run that was not asked for a resident orchestrator failed: %v\n%s%s", err, out, errOut)
	}
	if block := orchestratorBlockIn(t, r); block != nil {
		t.Errorf("a run that carries no --orchestrator wrote the block %v: the account holds iam.serviceAccounts.signJwt on the budget signer, and it may exist only where an owner asked for one\n%s%s", block, out, errOut)
	}

	// Asked: the block is written, derived field by field.
	out, errOut, err = executeStdin(t, "", "update", "--firebase-project", fpID, "--orchestrator")
	if err != nil {
		t.Fatalf("the opt-in run failed: %v\n%s%s", err, out, errOut)
	}
	block := orchestratorBlockIn(t, r)
	if block == nil {
		t.Fatalf("--orchestrator wrote no block:\n%s%s\n%s", out, errOut, r.tfvars(t))
	}
	want := map[string]any{
		"account_id":        infra.OrchestratorAccountID,
		"firebase_project":  fpID,
		"signer_account_id": infra.SignerAccountID,
		"service":           infra.OrchestratorServiceName,
		"image":             r.credentialsRef(),
		"rtdb_url":          r.db.URL,
		"scheduler_job":     infra.OrchestratorSchedulerJob,
		// The rig's installation runs in us-east5, which Cloud Scheduler does
		// not offer: the region is the fixed nearby one, resolved exactly as
		// the history job's and the coordinator's are (gcp.SchedulerRegion).
		"scheduler_region": "us-east4",
	}
	if !reflect.DeepEqual(block, want) {
		t.Errorf("the block the opt-in wrote is\n%v\nwant\n%v\n: every field is the installation's own, and one this writer invented is a resource nobody asked for", block, want)
	}
	if !strings.Contains(out, infra.OrchestratorAccountID) || !strings.Contains(out, "opted in") {
		t.Errorf("the opt-in run said nothing about what it turned on:\n%s", out)
	}

	// A later run that does not carry the flag keeps it: the block is the
	// installation's record of having opted in, and a run that dropped it would
	// plan the account's removal (a google_service_account is data-bearing under
	// D1551, so that plan is refused and the installation stops converging).
	held := marshalBlock(t, block)
	out, errOut, err = executeStdin(t, "", "update", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("the run after the opt-in failed: %v\n%s%s", err, out, errOut)
	}
	if got := marshalBlock(t, orchestratorBlockIn(t, r)); got != held {
		t.Errorf("a later run without the flag changed the block:\n%s\nwant it exactly as the opt-in left it:\n%s", got, held)
	}

	// And an installation with no credentials service is refused, by name.
	t.Run("refused without the credentials service", func(t *testing.T) {
		fakeTerminal(t)
		r := newImagesRig(t, convergeRelease)
		r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+fpID+", rtdb_url: "+r.db.URL+" }\n")
		r.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
		r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
		r.fs.SetDatabase("us-east5")
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"history_service_account": historyAccount,
			"history_job":             nil,
		})}
		r.save(t)

		out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID, "--orchestrator")
		s := out + errOut
		if err == nil {
			t.Fatalf("--orchestrator on an installation with no credentials service succeeded, want the refusal:\n%s", s)
		}
		if !strings.Contains(err.Error(), "credentials_service") || !strings.Contains(err.Error(), "--firebase-project") {
			t.Errorf("the refusal does not name what is missing and the command that deploys it: %v\n%s", err, s)
		}
		if block := orchestratorBlockIn(t, r); block != nil {
			t.Errorf("a refused opt-in wrote the block anyway: %v", block)
		}
		if stage := stoppedStage(out); stage != initflow.Installation2 {
			t.Errorf("the run stopped at %q, want %s: the refusal belongs to the stage that has the Firebase outputs\n%s", stage, initflow.Installation2, s)
		}
	})
}

// marshalBlock renders a tfvars block for comparison, so an assertion about
// "unchanged" reads the whole object rather than a member or two.
func marshalBlock(t *testing.T, block map[string]any) string {
	t.Helper()
	b, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestOrchestratorImageFollowsTheReleasePin is the HARD RULE's half of the
// opt-in: the block is state — the account, its five bindings, the pairing
// container, the service and the tick — and the ONLY thing a later release
// changes about it is the image, onto the pin that release resolved for the
// credentials service. `fugaro orchestrator serve` is the fourth subcommand of
// that one binary, so a run that moved the service, the intake and the hop and
// left the orchestrator on the release behind would run a process the state it
// just wrote may have moved past.
//
// The run carries the flag that is NOT the opt-in (`--firebase-project` alone),
// which is the whole point: the re-pin is not a second decision about the
// account, and an installation whose owner never runs `--orchestrator` again
// still converges.
//
// Mutation (run, restore): drop the SetOrchestratorImage call from
// moveCredentialsImage and this fails with the release behind's reference still
// in the block — and, one stage earlier, with the credentials-service stage
// skipping a run that had something to move.
func TestOrchestratorImageFollowsTheReleasePin(t *testing.T) {
	r := orchestratorRig(t)
	seedOrchestratorBlock(t, r, oldCredentialsRef(convergePrev))
	before := orchestratorBlockIn(t, r)
	if before["image"] != oldCredentialsRef(convergePrev) {
		t.Fatalf("the fixture's block is not the release behind's: %v", before)
	}

	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID)
	s := out + errOut
	if err != nil {
		t.Fatalf("the update of an installation holding the block failed: %v\n%s", err, s)
	}
	after := orchestratorBlockIn(t, r)
	if after == nil {
		t.Fatalf("the run dropped the block:\n%s", s)
	}
	if got := after["image"]; got != r.credentialsRef() {
		t.Errorf("the orchestrator's image after the update = %v, want this release's pin %s: `fugaro orchestrator serve` is a subcommand of the binary the credentials service runs, and a daemon left on the previous release's process reads this run's state with the reader behind it\n%s", got, r.credentialsRef(), s)
	}
	// And nothing else moved: the account, its two token-minter names, the
	// service, the tick and the database are the installation's state, and a
	// release changes none of them.
	want := map[string]any{}
	for k, v := range before {
		want[k] = v
	}
	want["image"] = r.credentialsRef()
	if !reflect.DeepEqual(after, want) {
		t.Errorf("the update changed more of the block than the image:\n%v\nwant\n%v", after, want)
	}

	// A second run has nothing to do: the image is this release's, and the
	// document it leaves is the one it found.
	held := marshalBlock(t, after)
	if out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID); err != nil {
		t.Fatalf("the second update failed: %v\n%s%s", err, out, errOut)
	}
	if got := marshalBlock(t, orchestratorBlockIn(t, r)); got != held {
		t.Errorf("a second update moved the block again:\n%s\nwant\n%s", got, held)
	}
}
