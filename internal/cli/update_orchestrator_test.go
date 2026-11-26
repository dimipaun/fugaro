package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The resident orchestrator's block is the installation root's `orchestrator`
// variable (mobile plan tasks P5.1b and P5.1c; the owner's ruling of
// 2026-11-20), and these are the properties the door owes it: that `fugaro
// update` alone writes it, for an installation that can carry one and for no
// other, that every field in it is derived from what the installation already
// holds, that a run that never applies it leaves none behind, and that the
// release's own pin reaches it on every later update the way it reaches the
// credentials service, the intake and the voice hop.

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

// TestUpdateWritesTheOrchestratorBlockWhereItCanCarryOne is the writer itself:
// a plain `fugaro update` of an installation that has the credentials service
// and the Firebase backend writes the block, every field in it the
// installation's own — the account, the service and the tick are
// internal/infra's constants, the signer is the Firebase root's own account ID,
// the project and the database are the budget block `fugaro update
// --firebase-project` wrote, the region is the config's, and the image is the
// pin the credentials service now runs, the binary whose `orchestrator serve`
// subcommand it is. It names the signJwt grant in the line it prints.
//
// A later run keeps it untouched: the block is the installation's record, and a
// run that dropped it would plan the account's removal (a google_service_account
// is data-bearing under D1551, so that plan is refused and the installation
// stops converging).
//
// An installation that cannot carry one — no Firebase backend recorded — is not
// an error: the update succeeds, writes no block, and says in one line why.
//
// Mutation (run, restore): make orchestratorVerdict answer false and the first
// half fails with no block; drop the budget check from it and the last asks for
// a block whose database the config does not name.
func TestUpdateWritesTheOrchestratorBlockWhereItCanCarryOne(t *testing.T) {
	r := orchestratorRig(t)

	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("the update failed: %v\n%s%s", err, out, errOut)
	}
	block := orchestratorBlockIn(t, r)
	if block == nil {
		t.Fatalf("a plain update wrote no orchestrator block:\n%s%s\n%s", out, errOut, r.tfvars(t))
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
		t.Errorf("the block the update wrote is\n%v\nwant\n%v\n: every field is the installation's own, and one this writer invented is a resource nobody asked for", block, want)
	}
	if s := out + errOut; !strings.Contains(s, infra.OrchestratorAccountID+"@") || !strings.Contains(s, "signJwt") {
		t.Errorf("the update said nothing about the account it adds or the grant that makes it the widest:\n%s", s)
	}
	if strings.Contains(out+errOut, "--orchestrator") {
		t.Errorf("the update names a flag that no longer exists:\n%s%s", out, errOut)
	}

	held := marshalBlock(t, block)
	out, errOut, err = executeStdin(t, "", "update", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("the run after the first failed: %v\n%s%s", err, out, errOut)
	}
	if got := marshalBlock(t, orchestratorBlockIn(t, r)); got != held {
		t.Errorf("a later run changed the block:\n%s\nwant it exactly as the first left it:\n%s", got, held)
	}

	t.Run("left off, with one line, where the installation records no Firebase backend", func(t *testing.T) {
		r := orchestratorRig(t)
		r.dropBudgetFromConfig(t)

		out, errOut, err := executeStdin(t, "", "update")
		s := out + errOut
		if err != nil {
			t.Fatalf("an installation that cannot carry the orchestrator failed its update: %v\n%s", err, s)
		}
		if block := orchestratorBlockIn(t, r); block != nil {
			t.Errorf("the update wrote a block for an installation with no database to point it at: %v", block)
		}
		if n := strings.Count(s, "resident orchestrator is left off"); n != 1 || !strings.Contains(s, "Firebase budget backend") {
			t.Errorf("the update names why the orchestrator is left off %d time(s), want one line naming the missing backend:\n%s", n, s)
		}
	})
}

// TestOrchestratorBlockIsTakenBackWhenTheRunNeverAppliesIt is the window
// between the write and the apply.
//
// ensureOrchestratorBlock writes the block into the workdir BEFORE the plan
// that carries it, because the plan reads the document on disk — and a run can
// end inside that window with nothing created: a plan that cannot be read, a
// plan the guard refuses, the confirmation declined, --plan-only's stop. The
// block is then not the record of an apply, it is the apply, taken by a run that
// never got to it: a later update that asks nobody (a managed project's, or
// --yes) would bring the account, its five bindings, the pairing container and
// the service up on a document the failed run left. The writer takes it back
// out, says so, and the next update writes it again and applies it.
//
// The failure is aimed at the installation root's plan in the
// credentials-service stage, the one the block was written for: the plans
// before it (the first apply, the audience fill) carry no block.
func TestOrchestratorBlockIsTakenBackWhenTheRunNeverAppliesIt(t *testing.T) {
	r := orchestratorRig(t)
	r.script["plan@installation"] = []map[string]any{
		{"exit": 2},
		{"exit": 2},
		{"exit": 2},
		{"exit": 1, "stderr": "Error: Error building changes: the installation root's plan failed"},
		{"exit": 2},
	}
	r.save(t)

	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID)
	if err == nil {
		t.Fatalf("the installation root's plan failed, yet the update succeeded:\n%s%s", out, errOut)
	}
	if stage := stoppedStage(out); stage != initflow.CredentialsService {
		t.Fatalf("the run stopped at %q, want %s: the failure has to land in the window between the write and the apply, or this test proves nothing\n%s%s", stage, initflow.CredentialsService, out, errOut)
	}
	if !strings.Contains(out+errOut, "was not applied") {
		t.Errorf("the run did not say the block it wrote was taken back out:\n%s%s", out, errOut)
	}
	if block := orchestratorBlockIn(t, r); block != nil {
		t.Errorf("a run that applied nothing left the block in the workdir:\n%v", block)
	}

	out, errOut, err = executeStdin(t, "", "update", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("the update after the failed one failed: %v\n%s%s", err, out, errOut)
	}
	if block := orchestratorBlockIn(t, r); block == nil {
		t.Errorf("the update after a run that applied nothing wrote no block: the installation never converges on the orchestrator\n%s%s", out, errOut)
	}
}

// orchestratorConvergeRig is the installation one release behind as this
// door's own convergence test needs it: the converge rig — a checkout, a
// published node and a plugin pin from convergePrev — with the credentials
// service deployed under the previous release's image, and the resident
// orchestrator's block written by hand exactly as an operator without a writer
// wrote it (the task's own before-state). withBlock is the two fixtures the
// HARD RULE names: an installation that opted in under the release behind, and
// one that never did.
//
// The installation root's plan holds the two images moving, because a plan
// that reports no changes is never applied (TF.Plan) and the whole point here
// is the document Terraform is handed. The outputs are the ones that apply
// reads back, and they carry the credentials service this run's tfvars name
// (the installation stage refuses the half-carried state).
func orchestratorConvergeRig(t *testing.T, withBlock bool) *convergeRig {
	t.Helper()
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	if withBlock {
		seedOrchestratorBlock(t, r.imagesRig, oldCredentialsRef(convergePrev))
	}
	// The rig's document answers the state reads under "show -json" while the
	// plan answers under "show@installation"; without the first, the fallback
	// the fake documents (a script naming only "show" answers the state too)
	// would let this stage's plan be read as the installation's state, which
	// manages nothing.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	changes := installationCredentialsPlan("update", r.credentialsRef())
	if withBlock {
		changes = append(changes, change("module.installation.google_cloud_run_v2_service.orchestrator", "update"))
	} else {
		changes = append(changes,
			change("module.installation.google_service_account.orchestrator[0]", "create"),
			change("module.installation.google_cloud_run_v2_service_iam_member.orchestrator_credentials_invoker[0]", "create"))
	}
	r.setPlanAt(t, "installation", changes...)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.save(t)
	return r
}

// TestUpdateConvergesAnInstallationAtThePreviousVersionWithAndWithoutTheBlock
// is the HARD RULE's own fixture, in both directions: `fugaro update` alone
// converges an installation at the release behind whether or not it holds the
// resident orchestrator's block (plan tasks P5.1b and P5.1c).
//
// Holding the block is the direction that can destroy something. The account
// and its five bindings are STATE, and the spec this run rebuilds the root's
// document from never carries the key (Orchestrator is the one writer that
// echoes the installation's own opt-in). A run whose carry dropped it would
// hand Terraform a document without var.orchestrator, which is the plan of the
// account's removal — a google_service_account is data-bearing under D1551 and
// the removal rule REFUSES that plan rather than applying it (tf/cover.go's
// RefusesRemovalType), so the installation would not merely lose its daemon,
// it would stop converging: every later update repeats the refusal. What the
// run must leave is therefore the block it found, with one member moved.
//
// The moved member is the image, onto the pin this release resolved for the
// credentials service — `fugaro orchestrator serve` is a subcommand of that
// same binary — and NOTHING else about the block moves: the account, the two
// names of its token-minter row, the service, the tick and the database are
// the installation's record, and a run that recomposed them would be
// re-deciding grants the design's section 4.3 fixes.
//
// And the second update plans what the first left: the document handed to
// Terraform is the one the first run wrote, byte for byte, which is what "the
// installation has converged" means for the one member a release moves.
//
// The empty direction is the switch's own promise, checked where it matters —
// on an installation that is genuinely at the release behind and converging
// for other reasons. No run invents the block: not the one that writes the
// others (the credentials service's, the intake's), and not one that would
// have to derive every field of it anyway, since the credentials service is
// there and the config's budget block is not empty.
func TestUpdateConvergesAnInstallationAtThePreviousVersionWithAndWithoutTheBlock(t *testing.T) {
	t.Run("holding a block written by hand", func(t *testing.T) {
		r := orchestratorConvergeRig(t, true)
		before := orchestratorBlockIn(t, r.imagesRig)
		if before == nil || before["image"] != oldCredentialsRef(convergePrev) {
			t.Fatalf("the fixture's block is not the release behind's: %v", before)
		}

		convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

		after := orchestratorBlockIn(t, r.imagesRig)
		if after == nil {
			t.Fatalf("after fugaro update, the installation's %s carries no orchestrator block. The account and its bindings are state, and the document this run handed Terraform is the plan of their destruction — which the removal rule refuses (a google_service_account is data-bearing under D1551), so the installation stops converging rather than losing only its daemon", infra.VarsFile)
		}
		if got := after["image"]; got != r.credentialsRefAt(convergeRelease) {
			t.Errorf("the orchestrator's image after the update = %v, want this release's pin %s: `fugaro orchestrator serve` is a subcommand of the binary the credentials service runs, so a daemon left on the release behind's process reads this run's state with the reader behind it", got, r.credentialsRefAt(convergeRelease))
		}
		want := map[string]any{}
		for k, v := range before {
			want[k] = v
		}
		want["image"] = r.credentialsRefAt(convergeRelease)
		if !reflect.DeepEqual(after, want) {
			t.Errorf("the update changed more of the block than the image:\n%v\nwant\n%v\n: the account, its grants, the service, the tick and the database are the installation's own record of what it opted into", after, want)
		}

		// And the installation converges: the daemon's identity is armed (the
		// block is the record the account exists, orchestratorIdentityOf), and
		// a second update hands Terraform the document the first left.
		if !orchestratorBlockHeld(initProject) {
			t.Errorf("after fugaro update, the installation's %s holds no block the daemon's identity can read (orchestratorIdentityOf): a block the run carried but the reader cannot read is an identity that mints nothing", infra.VarsFile)
		}
		// The document above is the one a second update plans from, and that is
		// the whole of "a second update plans nothing" as this rig can hold it:
		// the door is not run twice here, for the reason TestUpdateIsIdempotent
		// gives — this rig's first run onboards the repository, which leaves the
		// checkout different from the default branch, and the second run's
		// repository stage stops for the user over exactly that (its own,
		// correct refusal). What a second run does to the block is pinned where
		// the rig can run the door twice, on the opt-in's own rig
		// (TestOrchestratorImageFollowsTheReleasePin's second run: the same
		// document, byte for byte, and no apply at all).
	})

	t.Run("holding no block", func(t *testing.T) {
		r := orchestratorConvergeRig(t, false)
		if orchestratorBlockIn(t, r.imagesRig) != nil {
			t.Fatalf("the fixture already carries a block: %v", orchestratorBlockIn(t, r.imagesRig))
		}
		if orchestratorBlockHeld(initProject) {
			t.Fatal("the fixture's daemon identity is armed before the update: the account does not exist on an installation at the release behind")
		}

		run := startRunInFlight(t, r)

		line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"}))
		if !strings.Contains(line, "1 run in flight") || !strings.Contains(line, "waits for none") || !strings.Contains(line, "resident orchestrator") {
			t.Errorf("the update does not say, beside the run in flight, that the orchestrator's revision reaches the credentials service it calls:\n%s", line)
		}
		rec, err := run.store.ReadRecord(context.Background())
		if err != nil {
			t.Fatalf("the run's record could not be read after the update: %v", err)
		}
		if rec.Status != runstore.StatusRunning || rec.Stage != "implement" {
			t.Errorf("the run's record reads status %q stage %q after the update, want the running/implement it bootstrapped with", rec.Status, rec.Stage)
		}
		run.laterWrites(t, r)

		// The document Terraform was handed is the whole of what the update
		// decides: the block is var.orchestrator, and the module's own tests
		// (modules/installation/orchestrator_test.go, and
		// internal/infra's TestOrchestratorDaemonIsTheCredentialsServicesOwnCallerClass
		// under the terraform tag) read from that variable the account
		// fugaro-orch, roles/run.invoker on the credentials service for it and
		// FUGARO_ORCHESTRATOR_ACCOUNTS on the service's revision.
		block := orchestratorBlockIn(t, r.imagesRig)
		if block == nil {
			t.Fatalf("fugaro update gave the installation no resident orchestrator: the installation's %s carries no block, so the account fugaro-orch, its invoker grant on the credentials service and the service's orchestrator class are all still absent", infra.VarsFile)
		}
		if block["account_id"] != infra.OrchestratorAccountID || block["image"] != r.credentialsRefAt(convergeRelease) {
			t.Errorf("the block the update wrote is %v: want the account %s on this release's pin %s", block, infra.OrchestratorAccountID, r.credentialsRefAt(convergeRelease))
		}
		if !orchestratorBlockHeld(initProject) {
			t.Error("after fugaro update, the daemon's identity is not armed: the block is the record the account exists (orchestratorIdentityOf), and Check 640 stops at F19 until it is")
		}
	})
}

// dropBudgetFromConfig takes the budget block the credentials rig appended out
// of the project's config: the installation then records no Firebase backend, so
// the block has no database to be pointed at.
func (r *imagesRig) dropBudgetFromConfig(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "budget:") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
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
	// And with the installation converged there is nothing left to apply: the
	// images stage's stale test reads the block's image (servicesImageStale), so
	// a block it left behind would send the credentials-service stage through
	// the move and the apply a second time, for ever — an upgrade nobody
	// finishes.
	appliesBefore := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update", "--firebase-project", fpID)
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update failed: %v\n%s", err, s)
	}
	if got := marshalBlock(t, orchestratorBlockIn(t, r)); got != held {
		t.Errorf("a second update moved the block again:\n%s\nwant\n%s", got, held)
	}
	if after := len(r.ran(t, "apply")); after != appliesBefore {
		t.Errorf("the second update applied %d time(s): a run after a converged update has nothing left to do\n%s", after-appliesBefore, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
}
