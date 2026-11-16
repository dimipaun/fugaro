package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
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

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
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
	if !strings.Contains(out, "✔ installation No changes") {
		t.Errorf("the second update did not leave the installation root with nothing to change:\n%s", out)
	}
	if second := licenceBlock(t, r); second != first {
		t.Errorf("the second update wrote a different licence block:\n  was %s\n  now %s", first, second)
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
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if c, ok := r.tfvars(t)["coordinator"].(map[string]any); !ok || c["deploy_job"] == true {
		t.Fatalf("the fixture deployed the coordinator's own job: %v, so it is not the installation this test is about", r.tfvars(t)["coordinator"])
	}
	if v, ok := r.tfvars(t)["licence"]; ok {
		t.Errorf("an installation whose coordinator job is not deployed planned a licence job: %v\nthe licence job's image is the coordinator's, so it is created under that job's own condition and nowhere else (TN5)", v)
	}
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
