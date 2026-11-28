package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_licence_test.go is K4.2's own test of the update door: `fugaro update`
// creates the licence renewal job, its account, its empty container and its
// schedule on an installation whose coordinator's condition holds, and it does
// both of the things CLAUDE.md requires of every change that reaches an
// installation — it converges one that is a release behind in the same one
// command, and it does it while a run is in flight.
//
// The fixture is the update door's own (update_in_flight_test.go's rig), with
// the coordinator opted in: the job's image is the coordinator's, so an
// installation that has not opted in has no tagged image to run and the job is
// not for it (TN5, and the case asserted at the bottom of this file).

// TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning: an installation one
// release behind — it has a coordinator and no licence job, because the job did
// not exist when it applied — updated while a run of its own repository is in
// flight. One `fugaro update` ends with the job in the installation's tfvars,
// the run is named once and not asked about, and every write the run makes
// afterwards still lands.
//
// The step touches nothing a run holds and that is the claim: a new job, a new
// account, a new container and a new schedule, none of which any run reads,
// writes or is named in. docs/design/update-in-flight.md carries the row.
func TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning(t *testing.T) {
	r := convergeRigWithTheCoordinator(t)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// The job exists, at this release's names, in the document the apply read.
	vars := r.tfvars(t)
	l, ok := vars["licence"].(map[string]any)
	if !ok {
		t.Fatalf("the installation's tfvars carry no licence job after fugaro update: %v\nan installation that deploys the coordinator's own job gets the renewal job on every update (D524 — the core never reads a key, so update cannot know whether one was bought and must not ask)", vars["licence"])
	}
	lc := r.localConfig(t)
	for _, want := range []struct {
		what, got string
		key       string
	}{
		{"the account", "fugaro-licence", "account_id"},
		{"the job", "fugarolicence", "job"},
		{"the scheduler job", "fugaro-licence-renew", "scheduler_job"},
	} {
		if got, _ := l[want.key].(string); got != want.got {
			t.Errorf("the licence job's %s is %q, want %q", want.what, got, want.got)
		}
	}
	if got, _ := l["renewal_secret"].(string); got != infra.LicenceRenewalSecretID(lc.Name) {
		t.Errorf("the licence job's renewal container is %q, want the ID derived from %s (%s): `fugaro licence set` writes into the container that ID names and refuses before it exists with one line naming `fugaro update`", got, lc.Name, infra.LicenceRenewalSecretID(lc.Name))
	}
	read := conditionOf(t, l, "read_condition")
	write := conditionOf(t, l, "write_condition")
	for _, want := range []string{gcp.LicenceMarkerObject, gcp.LicenceConfigObject, gcp.LicenceKeyObject} {
		if !strings.Contains(read, want) {
			t.Errorf("the job's read condition does not name %s:\n%s\nthe job runs in a container with no checkout and no config file, so the marker and the published config are how it finds the installation whose licence it renews", want, read)
		}
	}
	if strings.Contains(write, gcp.LicenceMarkerObject) || !strings.Contains(write, gcp.LicenceKeyObject) {
		t.Errorf("the job's write condition is\n%s\nwant the licence object alone: the renewal replaces one object, and a write condition naming the marker or the config is a grant no module writes", write)
	}
	env, _ := l["env"].(map[string]any)
	for _, key := range []string{"FUGARO_PROJECT", "FUGARO_GCP_PROJECT"} {
		if v, _ := env[key].(string); v == "" {
			t.Errorf("the job's environment carries no %s: the container has no checkout, so those two names are how it finds the installation it renews for", key)
		}
	}

	// The run: named once, not asked about, and every write it makes after the
	// update still lands — the same three claims
	// TestUpdateLeavesARunInFlightRunning makes, over the update that also
	// created the job.
	line := inFlightLine(t, out)
	if !strings.Contains(line, "1 run in flight") || !strings.Contains(line, "waits for none") {
		t.Errorf("the update that created the licence job did not print the in-flight line: %q", line)
	}
	ctx := context.Background()
	rec, err := run.store.ReadRecord(ctx)
	if err != nil {
		t.Fatalf("the run's record could not be read after the update: %v", err)
	}
	if rec.Stage != "implement" {
		t.Errorf("the run's record reads stage %q after the update, want the implement it bootstrapped with", rec.Stage)
	}
	run.laterWrites(t, r)
}

// TestASecondUpdateLeavesTheLicenceJobAlone: the job is created once and kept.
// A second `fugaro update` of the same installation writes the same block, byte
// for byte, and the update door's own check is that nothing of it changed —
// which is what makes the first update's job a fact of the installation rather
// than something every run re-plans.
//
// It is also the convergence CLAUDE.md requires, read from the other side: an
// installation that has just been updated is at this release, so the update
// that follows it has nothing to do about the licence job at all.
func TestASecondUpdateLeavesTheLicenceJobAlone(t *testing.T) {
	r := convergeRigWithTheCoordinator(t)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	first := licenceBlock(t, r)

	// The second run is allowed to end non-zero, and the tolerance is the
	// fixture's rather than the update's: the first converge left this
	// checkout's fugaro.yaml differing from origin/main (the anchor it writes),
	// and the repository stage refuses a checkout that is not the default
	// branch's from then on — a fact of one temp clone nobody re-onboards, not
	// of the licence job. The installation stage runs BEFORE it, which is the
	// stage this test is about, and what it wrote is what is asserted.
	fakeTerminal(t)
	out, _, _ := executeStdin(t, "", "update")
	if !strings.Contains(out, markDone+" "+initflow.DisplayName(initflow.Installation)+" No changes") {
		t.Errorf("the second update did not leave the installation root with nothing to change:\n%s", out)
	}
	if second := licenceBlock(t, r); second != first {
		t.Errorf("the second update wrote a different licence block:\n  was %s\n  now %s", first, second)
	}
}

// TestAnUpdateKeepsTheLicenceJobTheInstallationHolds is the other installation
// the drop above is about: one that HOLDS the renewal job — an earlier run
// deployed the coordinator's job, so the renewal job, its account and its empty
// container are this installation's — and whose coordinator's readiness turns
// false afterwards. The block must survive that run in the installation's own
// document, or the record of a job the installation still holds is lost to the
// next document the root is written from.
//
// The readiness is recomputed on every run from three facts (init_firebase.go's
// coordinatorJob): the Firebase outputs the config records, the repositories it
// records, and the history image's tag. Here it is the third that goes — the tag
// leaves the fake registry between the two runs — and the run then plans no
// licence job at all, because the module's own condition is the coordinator's
// (licence.tf's local.licence_on) and not the block's presence: what licenceJob
// composes and what the module creates are two different questions.
//
// So this pins the DOCUMENT and not the plan: the block is carried forward from
// the document the run before wrote (infra.CarriedLicence, prepare's last
// carry), so a run whose readiness is false leaves the installation's record
// whole. The plan in that state is unchanged — the module creates nothing either
// way — and whether a readiness recomputed false should take back a job the
// installation already holds (the container carries prevent_destroy, so the
// plan that would is the one Terraform refuses) is a decision about that
// condition, not about this carry.
func TestAnUpdateKeepsTheLicenceJobTheInstallationHolds(t *testing.T) {
	r := convergeRigWithTheCoordinator(t)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	if first := licenceBlock(t, r); first == "null" {
		t.Fatalf("the first update wrote no licence block, so this is not the installation this test is about: %s", first)
	}

	// The history image's tag goes, which is the fact the renewal job hangs off:
	// one image serves the sweep, the front door and this job (infra.Licence's
	// spec takes HistoryImage), and a plain `fugaro update` mirrors no image, so
	// nothing of the second run puts the tag back.
	r.ar.RemoveTag(initProject, "us-east5", infra.BaseRegistry, infra.HistoryImagePackage, "latest")

	fakeTerminal(t)
	// The second run is allowed to end non-zero, and the tolerance is the
	// fixture's rather than the update's: the first converge left this
	// checkout's fugaro.yaml differing from origin/main (the anchor it writes),
	// and the repository stage refuses a checkout that is not the default
	// branch's from then on — a fact of one temp clone nobody re-onboards, not
	// of the licence job. The installation stage runs BEFORE it, which is the
	// stage this test is about, and what it wrote is what is asserted below.
	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Logf("the second update ended with %v", err)
	}
	vars := r.tfvars(t)
	// The fixture's own precondition: the run really did recompute the
	// coordinator's deployment to false, which is what stops licenceJob
	// composing the block from the spec.
	if c, ok := vars["coordinator"].(map[string]any); !ok || c["deploy_job"] == true {
		t.Fatalf("the second run still deployed the coordinator's job, so this is not the state the test is about:\n%s\n%s", vars["coordinator"], out)
	}
	l, ok := vars["licence"].(map[string]any)
	if !ok {
		t.Fatalf("the update dropped the licence block of an installation that holds the job (%v):\nthe container the block names carries prevent_destroy, so a document that forgets the key is a plan Terraform refuses rather than applies, and the installation's own record of the job has to outlive a run whose readiness is false (infra.CarriedLicence, prepare's last carry)", vars["licence"])
	}
	for _, want := range []struct {
		what, got string
		key       string
	}{
		{"the account", "fugaro-licence", "account_id"},
		{"the job", "fugarolicence", "job"},
	} {
		if got, _ := l[want.key].(string); got != want.got {
			t.Errorf("the kept licence block's %s is %q, want %q", want.what, got, want.got)
		}
	}
}

// TestNoCoordinatorMeansNoLicenceJob: an installation that has not opted into
// the coordinator gets no licence job, no account and no container — and no
// variable, so its plan is byte for byte the one the release before this one
// planned (CLAUDE.md's convergence rule, and the reason internal/cli's licenceJob
// drops the block rather than letting the module ignore it).
//
// It is a real installation on the update door, not a unit test of one
// function: the config below is this rig's own minus the coordinator's opt-in.
func TestNoCoordinatorMeansNoLicenceJob(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if v, ok := r.tfvars(t)["licence"]; ok {
		t.Errorf("an installation with no coordinator planned a licence job: %v\nthe job's image is the coordinator's, so an installation that never opted in has no tagged image to run and `fugaro licence status` says renewal is by hand there (TN5)", v)
	}
	if c, ok := r.tfvars(t)["coordinator"]; ok {
		t.Errorf("the fixture opted into the coordinator after all: %v", c)
	}
}

// TestNoCoordinatorJobMeansNoLicenceJob: the OTHER half of the condition, and
// the one that is not about the owner's flag. An installation whose coordinator
// is opted in but whose job is not deployed — the state of one whose history
// image is not tagged yet, which is what internal/cli's coordinatorJob reads —
// gets no licence job either: the job's image is the coordinator's, so a
// licence job there would be a Cloud Run job whose container image does not
// exist, failing once a day with nothing to renew.
//
// This is the pair the module's local.deploy_coordinator and internal/cli's
// licenceJob have to agree on, and the reason neither is a flag of its own: a
// second switch over one fact drifts the first time a run sets one and not the
// other.
func TestNoCoordinatorJobMeansNoLicenceJob(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	// The coordinator is opted in and its account is planned, and no image is
	// tagged, so its own job is not.
	r.appendConfig(t, "terraform: { budget_backend: true, coordinator: true }\n")
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if c, ok := r.tfvars(t)["coordinator"].(map[string]any); !ok || c["deploy_job"] == true {
		t.Fatalf("the fixture deployed the coordinator's own job: %v, so it is not the installation this test is about", r.tfvars(t)["coordinator"])
	}
	if v, ok := r.tfvars(t)["licence"]; ok {
		t.Errorf("an installation whose coordinator job is not deployed planned a licence job: %v\nthe licence job's image is the coordinator's, so it is created under that job's own condition and nowhere else (TN5)", v)
	}
}

// TestTheReviewCoversTheRenewalBindingTheUpdatePlans is the plan review's half
// of the licence renewal job (plan K4.2), on the door that plans it: the update
// that creates the job also plans its secret binding, and the run's own review
// has to admit that binding rather than report the step as one it did not
// announce.
//
// What it pins: the review reads the licence block off the RUN, where the
// completion recorded it (initRun.licence, licenceJob), because that is the
// block the plan being judged is written from. installRoot completes a COPY of
// the spec — it takes the spec by value — so the run's own record, and not the
// spec the engine holds, is what says what the stages put in this plan:
// licenceJob makes two decisions there (fill, and drop for an installation that
// deploys no job) and only the first of them is invisible, since
// infra.Installation already hands the door a block whenever the coordinator's
// opt-in is on.
//
// The plan is scripted, because the fake terraform runs no module: one change,
// the binding the module writes, whose container is the one the run's own
// licence spec derived (infra.LicenceRenewalSecretID — the same function
// TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning reads the tfvars
// back with, so the fixture cannot drift from the plan).
//
// This fixture's own state is the one where the door's spec and the completed
// one agree: an installation whose coordinator's job is deployed is handed a
// licence block by infra.Installation and keeps it through licenceJob, so
// reading the engine's spec here would name the same container. What this test
// pins is that the binding the run itself planned is admitted at all — it fails
// the moment the cover carries another container, or none
// (TestTheReviewRefusesABindingWithNoJobToHoldIt is the half that fails when
// the cover reads the engine's spec instead of the completed one).
func TestTheReviewCoversTheRenewalBindingTheUpdatePlans(t *testing.T) {
	r := convergeRigWithTheCoordinator(t)
	// The runs bucket's mark names this project, so the run's one confirmation
	// is the review's own (owner(), runCovers): without it every step asks its
	// own banner and a plan the review did not announce is asked about rather
	// than noted, which is the case the assertion below is not about.
	r.markedRuns(t, initProjectName, initProject)
	// The rig's own document answers the state reads under "show -json" while
	// the plan answers under "show@installation": without the first, the
	// fallback the fake documents (a script naming only "show" answers the
	// state too) would let this stage's plan be read as the installation's
	// state, which manages nothing (update_converge_test.go's own fixture).
	// The plan is read at all only because the installation root reports
	// changes: a plan that reports none is never read, and a review is never
	// asked about it.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", renewalBindingChange())
	r.save(t)
	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	// The review admits the binding, or refuses it by the one reason
	// licenceSecretGrant exists to give (tf.Cover): its target is not the
	// container this installation's licence renewal job writes into. The
	// refusal is a CONTENT finding (the grant table is D1545's record-and-warn
	// half), so the run would print it as a note and apply the plan anyway —
	// which is why this test reads the transcript rather than an exit code: a
	// binding the run itself planned must draw no such note.
	if i := strings.Index(out, "not this installation's renewal container"); i >= 0 {
		line := out[i:]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		t.Errorf("the review refused the renewal job's own secret binding:\n%s", line)
	}
	// The fixture's own check: the review really was taken, so the assertion
	// above is about a step the review judged rather than about a run that
	// asked each step separately.
	// A managed project's covered step reads the run's own sentence rather
	// than the review's: the run's one confirmation is the review's, and the
	// gate takes that verdict on the plan and records it (U3, D1016), so
	// GateOrdinary's line is what stands where the review's own question stood
	// in an unmanaged project.
	if !strings.Contains(out, initflow.GateOrdinary()) {
		t.Errorf("no step of this update read as covered by the run's one confirmation, so this fixture is not the review's own case:\n%s", out)
	}
}

// TestTheReviewRefusesABindingWithNoJobToHoldIt is the half of the review's
// licence block that a cover read off the ENGINE's spec gets wrong, and it is
// the reason the run records licenceJob's answer rather than leaving the review
// to read the spec: the completion's two decisions are not the same decision.
//
// The installation here is one whose coordinator is opted in and whose job is
// not deployed — the state TestNoCoordinatorJobMeansNoLicenceJob pins from the
// plan's side: no tagged history image, so internal/cli's coordinatorJob says
// the job is not to be deployed and its licenceJob DROPS the block
// (infra.Installation had handed the door one, the opt-in being on). The plan
// this run writes therefore carries no licence binding, and a plan that carries
// one anyway — a state Terraform wrote under an earlier release, or a plan
// somebody edited — is a binding this installation has no job to hold: the
// cover has no container to match it against, and licenceSecretGrant says so
// ("a cover with no container in it ... every binding to this account is
// refused"). It is a content finding (the grant table is D1545's record-and-
// warn half), so the run says it in one note and applies the plan anyway at an
// ordinary terminal; what the note's presence is the evidence of is that the
// finding was made at all.
//
// A cover read off e.spec.Licence answers the other way, and that is the bug
// this test exists for: the engine's spec still carries the block
// infra.Installation put there, so the container matches, the binding is
// admitted, no note is printed, and the run announces as its own a grant to the
// renewal account that nothing in this installation renews.
func TestTheReviewRefusesABindingWithNoJobToHoldIt(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	// The coordinator's opt-in without its image: this is the state the drop
	// branch of licenceJob is about, and TestNoCoordinatorJobMeansNoLicenceJob
	// is the same rig read from the plan's side.
	r.appendConfig(t, "terraform: { budget_backend: true, coordinator: true }\n")
	registerInstallation(t, r.initRig)
	r.markedRuns(t, initProjectName, initProject)
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", renewalBindingChange())
	r.save(t)
	currentRelease(t)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	whole := out + errOut
	// The refusal is a CONTENT finding, not an integrity one: the grant table
	// is what D1545 records and warns about, so the run carries on and applies
	// the plan at an ordinary terminal — what must not happen is the finding
	// never being made. The empty container in the refusal is the whole point:
	// the cover has NOTHING to match the binding against, which is the block
	// the completion dropped, where the engine's spec still has one to offer.
	if !strings.Contains(whole, "not this installation's renewal container ()") {
		t.Errorf("the review admitted a binding to the renewal container on an installation that deploys no licence job (exit %d, and the step's own confirmation printed above it):\nthe binding is one this installation's plan does not write, so the run has to record it as a step the review did not announce, by a cover holding no container — one read off the engine's spec would still carry the block infra.Installation put there and admit the grant:\n%s", ExitCode(err), whole)
	}
}

// renewalBindingChange is the renewal job's secret binding as the installation
// root's plan carries it: the module's own resource, the container its tfvars
// derived from the Fugaro project's name, the custom role written as the
// literal the review needs (a computed role is refused, so the plan must name
// it), and the job's own account.
func renewalBindingChange() planChange {
	return created("module.installation.google_secret_manager_secret_iam_member.licence_renewal[0]",
		"google_secret_manager_secret_iam_member", map[string]any{
			"project":   initProject,
			"secret_id": infra.LicenceRenewalSecretID(initProjectName),
			"role":      "projects/" + initProject + "/roles/" + tf.LicenceRenewalRole,
			"member":    "serviceAccount:" + tf.LicenceAccountID + "@" + initProject + ".iam.gserviceaccount.com",
		})
}

// convergeRigWithTheCoordinator is the update door's rig with the coordinator
// opted in: the installation is one release behind and holds the coordinator's
// own job, which is exactly the installation the licence job is for.
//
// The opt-in is the local config's own field (localcfg.Terraform.Coordinator),
// which is what `fugaro update --firebase-project --coordinator` records and
// what every later run reads, so this is the state of an installation whose
// owner opted in a release ago rather than a flag passed to the door here.
func convergeRigWithTheCoordinator(t *testing.T) *convergeRig {
	t.Helper()
	r := newConvergeRig(t, v3Node(nil))
	// The image the coordinator's job runs, which the licence job's own spec
	// takes: without it coordinatorJob plans the account alone and there is no
	// job for the licence to hang off.
	r.historyImage()
	r.appendConfig(t, "terraform: { budget_backend: true, coordinator: true }\n")
	registerInstallation(t, r.initRig)
	return r
}

// licenceBlock is the installation's licence variable as one string, so two
// updates can be compared without a field-by-field walk.
func licenceBlock(t *testing.T, r *convergeRig) string {
	t.Helper()
	b, err := json.Marshal(r.tfvars(t)["licence"])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// conditionOf reads one of the licence block's two conditions out of the tfvars
// as {title, expression}, and fails the test when the block carries neither.
func conditionOf(t *testing.T, l map[string]any, key string) string {
	t.Helper()
	c, ok := l[key].(map[string]any)
	if !ok {
		t.Fatalf("the licence block carries no %s: %v", key, l[key])
	}
	title, _ := c["title"].(string)
	expr, _ := c["expression"].(string)
	if title == "" || expr == "" {
		t.Fatalf("the licence block's %s is %v, want a title and an expression: the plan review reads both to say which grant a binding is", key, c)
	}
	return expr
}

// localConfig is the installation's own config as this run left it.
func (r *convergeRig) localConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("reading the local config after the update: %v", err)
	}
	return lc
}
