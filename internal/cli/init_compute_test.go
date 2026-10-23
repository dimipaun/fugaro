package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The compute stage (design compute-backend §8.1 task 6). It is the owner's
// step, and the only step --yes may not reach that is neither a project, a
// secret nor a checkout's own file: what it provisions is money (a NAT's
// hours, a billable image build) and permanent, so its confirmation is the
// project's name typed at a real terminal.
//
// The rig is the whole converge with --provision-batch: the run accounts come
// from the project (a service account carrying a Fugaro job display name), the
// installation's outputs come from the fake terraform, and the Scheduler fake
// records the preload rebuild.

// computePin is the base image the installation pins: through the remote
// repository, the shape 57a measured on 2026-10-15 (§7.2). It is the image
// the preload pulls onto the boot image, so the pin must be there for the
// root to have anything to build from.
const computePin = "us-east5-docker.pkg.dev/proj-1234/" + infra.GhcrRegistry + "/acme/fugaro-base:1.2.3"

// computeSocketGIDLabel is the label name the stage reads back through the
// root's outputs (infra.ComputeOutputs.PreloadSocketGIDLabel). It is spelled
// here rather than imported because it is the module's string and not this
// package's: the module publishes it and
// TestComputeModuleRecordsTheSocketGID holds the three files together.
const computeSocketGIDLabel = "socket-gid"

// computeRig is the ordinary init rig with a state bucket, a base pin, a run
// account and the preload rebuild's job, ready for --provision-batch.
func computeRig(t *testing.T) *initRig {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	// The installation's pin, recorded by the run that pinned it.
	r.appendConfig(t, "base_images:\n  "+config.BaseKind+": "+computePin+"\n")
	// The run-class account a repository root would have created: the only
	// kind of account the compute root accepts as a run account.
	r.iam.AddServiceAccount(initProject, "fugaro-acme-web@proj-1234.iam.gserviceaccount.com", "Fugaro job acme/sandbox web")
	// The rebuild job the compute root creates; the stage starts it once the
	// apply is done. It lives in the SCHEDULER region and not in the
	// installation's, which is the whole point of that input: Cloud Scheduler
	// does not serve us-east5 (the rig's region), so the module creates the
	// job in us-east4 -- the location gcp.SchedulerRegion names for it -- and
	// a stage that started it in the installation's region would be asking
	// for a job that is not there.
	r.sched.SetJob(initProject, "us-east4", infra.PreloadSchedulerJob,
		"https://cloudbuild.googleapis.com/v1/projects/proj-1234/builds", "fugaro-preload@proj-1234.iam.gserviceaccount.com")
	// The compute root's own outputs. They are scripted for this root alone:
	// the installation's answer is a different one (a root's name is the
	// script key's suffix).
	r.script["output@compute"] = map[string]any{"stdout": computeOutputsJSON(t)}
	r.save(t)
	return r
}

// computeOutputsJSON is what the compute root's `terraform output -json`
// answers: the image the preload built, and the names the stage reports.
func computeOutputsJSON(t *testing.T) string {
	t.Helper()
	vals := map[string]string{
		"preload_image": infra.PreloadedImage(computePin),
		// The label each generation records its socket gid under. A name and
		// not a value: the gid is read off the builder VM by the build and
		// written onto the image it makes (preload.sh), so no root output can
		// carry it and this root's answer is the key the read-back needs.
		"preload_socket_gid_label": computeSocketGIDLabel,
		"reaper_account":           infra.ReaperAccountID + "@" + initProject + ".iam.gserviceaccount.com",
		"reaper_role":              "projects/" + initProject + "/roles/" + infra.ReaperRoleID,
		"preload_account":          infra.PreloadAccountID + "@" + initProject + ".iam.gserviceaccount.com",
		"preload_scheduler_job":    infra.PreloadSchedulerJob,
		"network":                  infra.DefaultNetwork(initProject),
		"subnetwork":               infra.DefaultSubnetwork(initProject, "us-east5"),
	}
	out := map[string]any{}
	for k, v := range vals {
		out[k] = map[string]any{"value": v, "type": "string", "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// computeCalls are the terraform subcommands run in the compute root's own
// workdir, in order. The rig's calls() reads the args alone, and the workdir
// is what tells this root's calls from the installation's.
func computeCalls(t *testing.T, r *initRig) [][]string {
	t.Helper()
	dir, err := infra.ComputeWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	// Both sides resolved: faketerraform records os.Getwd(), which answers a
	// path with every symlink followed, and the workdir is built from
	// $XDG_STATE_HOME (under $TMPDIR in the rig). On macOS those differ --
	// /var is a symlink to /private/var -- so an unresolved comparison finds
	// none of this root's calls.
	dir = resolvedPath(dir)
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var c struct {
			Args []string `json:"args"`
			Dir  string   `json:"dir"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if d := resolvedPath(c.Dir); d == dir || strings.HasPrefix(d, dir+string(filepath.Separator)) {
			out = append(out, c.Args)
		}
	}
	return out
}

// resolvedPath is path with the symlinks in it followed. A path that does not
// exist is returned as it is: only the comparison needs it resolved, not the
// caller.
func resolvedPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// subcommands are the subcommands of calls, in order.
func subcommands(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c[0])
	}
	return out
}

// The whole stage: a run that asks for the provisioning and types the
// project's name plans the root, shows the summary, applies it in the root's
// own workdir and state prefix, and starts the preload rebuild at once.
func TestComputeStageProvisionsAfterATypedConfirmation(t *testing.T) {
	typedThroughYes(t) // about the provisioning, not about who may confirm it
	r := computeRig(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := computeCalls(t, r)
	if got := subcommands(calls); !slices.Equal(got, []string{"version", "init", "plan", "show", "apply", "output"}) {
		t.Fatalf("terraform calls in the compute workdir = %q\n%s", got, out)
	}
	init := calls[1]
	if !slices.Contains(init, "-backend-config=prefix="+infra.StatePrefixCompute) {
		t.Errorf("the compute root does not have a state of its own: %q", init)
	}
	if runs := r.sched.Runs(); len(runs) != 1 || !strings.Contains(runs[0], infra.PreloadSchedulerJob) {
		t.Errorf("the preload rebuild was started %v, want once", runs)
	}
	if !strings.Contains(out, "provisions the Batch compute backend") {
		t.Errorf("the confirmation did not say what it provisions:\n%s", out)
	}
	// The parenthetical is the plan's own counts sentence and nothing after
	// it. counts.String() already ends in "updates", so a literal suffix left
	// in the format string renders "…(0 imports, 14 creates, 0 updates to
	// create)" on the one screen that authorizes billable resources.
	if !regexp.MustCompile(`scheduled Cloud Build \(\d+ imports, \d+ creates, \d+ updates(, \d+ deletes, \d+ replaces)?\)`).MatchString(out) {
		t.Errorf("the confirmation's counts are not the plan's own sentence:\n%s", out)
	}
	if !strings.Contains(out, infra.PreloadedImage(computePin)) {
		t.Errorf("the preloaded image's name was not reported:\n%s", out)
	}
	// The socket gid's label travels with the image's name, and the stage says
	// so on the screen that reports the provisioning: a run's `--group-add`
	// needs the gid the image records under that label, and a stage that
	// dropped the key would leave the read-back (Check 57b, `doctor`) with the
	// family alone. The value itself is the build's, never the stage's.
	if !strings.Contains(out, computeSocketGIDLabel) {
		t.Errorf("the socket gid's label was not reported:\n%s", out)
	}
	// The tfvars are the installation's own readings, not the defaults: the
	// run accounts come from the project, the repository and the roles from
	// the installation's outputs, and the COS family from Google's project.
	vars := computeTfvars(t, r)
	for k, want := range map[string]any{
		"project":              initProject,
		"region":               "us-east5",
		"run_accounts":         []any{"serviceAccount:fugaro-acme-web@proj-1234.iam.gserviceaccount.com"},
		"registry_repository":  infra.GhcrRegistry,
		"base_image":           computePin,
		"base_project":         "batch-custom-image",
		"base_family":          "batch-cos-stable-official",
		"build_submitter_role": "projects/" + initProject + "/roles/fugaroBuildSubmitter",
		// The window the scheduled rebuild replaces a stale image inside.
		"preload_refresh_days": infra.PreloadRefreshDaysDefault,
		"network":              "projects/" + initProject + "/global/networks/default",
		"subnetwork":           "projects/" + initProject + "/regions/us-east5/subnetworks/default",
		// The scheduler region, resolved and not defaulted: the config names
		// none, and Cloud Scheduler does not serve the installation's us-east5,
		// so the root's Scheduler jobs are created in the nearest location it
		// does serve (gcp.SchedulerRegion). The installation's region is NOT
		// this value, and a tfvars that wrote it there is an apply that fails
		// at the reaper's sweep.
		"scheduler_region": "us-east4",
	} {
		if got := vars[k]; !equalJSON(got, want) {
			t.Errorf("tfvars[%q] = %v, want %v", k, got, want)
		}
	}
}

// The installation's own scheduler_region wins over the nearest location Cloud
// Scheduler serves, and the stage starts the rebuild in the region it named:
// that is the reading the history job and the daily image check already make
// of the config's field, and it is what lets an installation keep every
// schedule in one place it chose.
func TestComputeStageTakesTheConfigsSchedulerRegion(t *testing.T) {
	typedThroughYes(t)
	r := computeRig(t)
	r.appendConfig(t, "scheduler_region: us-central1\n")
	// The rebuild the stage starts lives where the config says, not where the
	// rig's own region would have put it.
	r.sched.SetJob(initProject, "us-central1", infra.PreloadSchedulerJob,
		"https://cloudbuild.googleapis.com/v1/projects/proj-1234/builds", "fugaro-preload@proj-1234.iam.gserviceaccount.com")
	r.save(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := computeTfvars(t, r)["scheduler_region"]; !equalJSON(got, "us-central1") {
		t.Errorf("tfvars[scheduler_region] = %v, want the config's own us-central1", got)
	}
	if runs := r.sched.Runs(); len(runs) != 1 || !strings.Contains(runs[0], "/locations/us-central1/") {
		t.Errorf("the rebuild was started %v, want once in us-central1: the job lives where the config put it", runs)
	}
}

// The workdir rig logs is the one os.Getwd() answers, and that path has every
// symlink in it followed, while the workdir this test computes is built from
// $XDG_STATE_HOME. On macOS those differ -- $TMPDIR is /var/..., which is a
// symlink to /private/var -- so an unresolved comparison finds none of this
// root's calls and the stage's own calls read as the installation's.
func TestComputeStageFindsItsWorkdirThroughASymlink(t *testing.T) {
	typedThroughYes(t)
	r := computeRig(t)
	// The same case on any platform: the rig's own state home, reached
	// through a link. mkdir through it creates the real directory, and the
	// path the stage computes keeps the link while the rig records what
	// os.Getwd() answers.
	link := filepath.Join(t.TempDir(), "state-root")
	if err := os.Symlink(r.dir, link); err != nil {
		t.Skipf("this host makes no symlink: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(link, "xdg_state_home"))
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := computeCalls(t, r)
	if got := subcommands(calls); !slices.Contains(got, "apply") {
		t.Errorf("the compute root's calls are not found through a symlinked state home: %q\n%s", got, out)
	}
}

// computeTfvars is the compute root's terraform.tfvars.json.
func computeTfvars(t *testing.T, r *initRig) map[string]any {
	t.Helper()
	dir, err := infra.ComputeWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "gcp", "roots", "compute", "terraform.tfvars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// --yes never covers it: it is not a stage --yes is allowed to reach, so a
// run that passes --yes and types nothing leaves it for the owner instead of
// applying.
func TestComputeStageNeverTakesYes(t *testing.T) {
	r := computeRig(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup", "--yes", "--provision-batch")
	if err == nil || !strings.Contains(err.Error(), "left for you") {
		t.Fatalf("err = %v, want the step left for the owner\n%s", err, out)
	}
	if calls := subcommands(computeCalls(t, r)); slices.Contains(calls, "apply") {
		t.Fatalf("the compute root was applied without the typed name: %q", calls)
	}
	if len(r.sched.Runs()) != 0 {
		t.Error("the preload rebuild was started by a run that applied nothing")
	}
	if !strings.Contains(out, "left for you (compute, prompt)") {
		t.Errorf("the step was not reported as left for the owner:\n%s", out)
	}
	if !strings.Contains(out, "provisions the Batch compute backend") {
		t.Errorf("what the step provisions was not shown before it was left:\n%s", out)
	}
}

// A run that cannot ask does not even enter the stage: no terminal and --yes
// leaves the compute stage where it stands, and NOTHING of the compute root
// runs -- not its tfvars, not its init, not its plan.
//
// This is what the stage's noYes slot in initflow's order buys, and it is not
// the same guarantee as the stage's own typed prompt: askTyped refuses a --yes
// run too, but only once Apply is under way, which is after plan() has written
// the root's tfvars and planned it. Losing noYes would not apply anything --
// the prompt still stops that -- so nothing else here would fail, which is why
// this test exists.
func TestComputeStageIsLeftUnenteredWithoutATerminal(t *testing.T) {
	r := computeRig(t)
	out, _, err := executeStdin(t, "", "setup", "--yes", "--provision-batch")
	if err == nil || !strings.Contains(err.Error(), "left for you") {
		t.Fatalf("err = %v, want the step left for the owner\n%s", err, out)
	}
	if calls := computeCalls(t, r); len(calls) != 0 {
		t.Fatalf("a run that cannot confirm reached the compute root: %q\n%s", calls, out)
	}
	if len(r.sched.Runs()) != 0 {
		t.Error("the preload rebuild was started by a run that applied nothing")
	}
}

// A plan that would delete something is refused unless --allow-delete names
// it: the stage never deletes what it did not create. Both guards are held to
// this — the one in Plan (the plan view) and the one in Apply (a real run) —
// because they are separate calls and a mutation that drops either one leaves
// the other's path untested.
//
// The delete is put in the COMPUTE root's plan ALONE (setPlanAt). Scripting it
// for every root, as this test first did, is answered by the installation
// stage's own guard before the compute stage ever plans: the test then passes
// for a reason it does not name, and deleting guard(...) from both of the
// compute stage's methods leaves it green (a review's mutation).
func TestComputeStageGuardsDeletes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"the plan view", []string{"setup", "--yes", "--plan-only", "--provision-batch"}},
		{"the apply", []string{"setup", "--yes", "--provision-batch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := computeRig(t)
			// The installation plans cleanly, so the run reaches the compute
			// stage: the refusal below can only be that stage's.
			r.setPlanAt(t, "installation")
			r.setPlanAt(t, "compute", change("module.compute.google_compute_router_nat.this", "delete", "create"))
			fakeTerminal(t)
			out, _, err := executeStdin(t, initProjectName+"\n", tc.args...)
			if err == nil || !strings.Contains(err.Error(), "--allow-delete") {
				t.Fatalf("err = %v, want the compute stage's guard refusal:\n%s", err, out)
			}
			if calls := subcommands(computeCalls(t, r)); slices.Contains(calls, "apply") {
				t.Fatalf("a plan that deletes was applied: %q", calls)
			}
		})
	}
}

// Nothing to do is "No changes", not an apply: a second run of the stage is a
// no-op, which is what makes it safe to rerun after a partial failure.
func TestComputeStageNoChanges(t *testing.T) {
	r := computeRig(t)
	r.script["plan"] = map[string]any{"exit": 0} // an empty plan, everywhere
	r.save(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if calls := subcommands(computeCalls(t, r)); slices.Contains(calls, "apply") {
		t.Fatalf("an empty plan was applied: %q", calls)
	}
	if !strings.Contains(out, "No changes: the compute backend's provisioning matches the plan.") {
		t.Errorf("the empty plan was not reported:\n%s", out)
	}
}

// --plan-only applies nothing, --provision-batch or not: not the compute
// root's apply, and not the preload rebuild Apply starts once it has applied.
//
// The work is scripted into the compute root alone so that this stage is the
// one the run plans, and the run's own account is then read to hold that: see
// the guard below.
func TestComputeStagePlanOnlyAppliesNothing(t *testing.T) {
	r := computeRig(t)
	r.setPlanAt(t, "installation")
	r.setPlanAt(t, "compute", change("module.compute.google_cloud_scheduler_job.preload_rebuild", "create"))
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup", "--yes", "--plan-only", "--provision-batch", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The guard that keeps the two assertions below honest, and the reason the
	// work is scripted into the compute root alone: initflow's preview plans
	// only the FIRST stage with something to do, so a change left in the
	// installation root (the rig's default) is what gets planned, and this
	// stage is reported as "will-do ... (after installation)" — a stage a
	// plan-only run never reaches. Both assertions below are green against a
	// Plan that starts the build in that case, which is exactly the mutation
	// they are here to catch; so the run has to be shown to have planned THIS
	// stage, with work in it, before either of them means anything.
	var res struct {
		Stages []struct {
			Name, State, Detail, After string
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	compute := -1
	for i, s := range res.Stages {
		if s.Name == "compute" {
			compute = i
		}
	}
	if compute < 0 {
		t.Fatalf("the run has no compute stage:\n%s", out)
	}
	if s := res.Stages[compute]; s.State != "will-do" || s.After != "" || !strings.Contains(s.Detail, "creates") {
		t.Fatalf("the compute stage %+v is not the stage this plan-only run planned: it is the stage with work, and it is neither done nor waiting for another", s)
	}
	if calls := subcommands(computeCalls(t, r)); slices.Contains(calls, "apply") {
		t.Fatalf("--plan-only applied: %q", calls)
	}
	// And it starts nothing: the preload rebuild is Apply's last action, and
	// a plan is not the confirmation that earns it (the stage's own comment
	// puts the run after the apply). Asserted rather than assumed, because a
	// Plan that started the build would spend the image build a --plan-only
	// run was asked to cost nothing of.
	if runs := r.sched.Runs(); len(runs) != 0 {
		t.Fatalf("--plan-only ran the preload rebuild: %q", runs)
	}
}

// The stage is not registered unless the flag is given: an ordinary run never
// plans or touches the compute root, whatever the installation is. This is
// the INERT guarantee the design's rollout rests on.
func TestComputeStageIsBehindItsFlag(t *testing.T) {
	r := computeRig(t)
	fakeTerminal(t)
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
		t.Fatal(err)
	}
	if calls := computeCalls(t, r); len(calls) != 0 {
		t.Fatalf("an ordinary run reached the compute root: %q", calls)
	}
}

// The flag is refused where it could only mislead: with --repo (another
// root's run) and with every flag that only reads or only stops
// (--forget, --print-vars, --config-only, --publish-config, --anchor), so a
// run that named --provision-batch always means the apply it names. The
// check is asked
// directly: a --repo run resolves the checkout before it validates anything,
// and what is under test is the refusal, not the order of the checks.
func TestProvisionBatchFlagIsRefusedWhereItCannotApply(t *testing.T) {
	for _, o := range []*initOptions{{provisionBatch: true, repo: true}, {provisionBatch: true, forget: true},
		{provisionBatch: true, printVars: true}, {provisionBatch: true, configOnly: true},
		{provisionBatch: true, publishConfig: true}, {provisionBatch: true, anchor: true}} {
		err := o.check()
		if err == nil || !strings.Contains(err.Error(), "--provision-batch") {
			t.Errorf("%+v: err = %v, want it refused", o, err)
		}
	}
}

// The --repo run's own check, and the one place a --repo run validates: it
// never reaches check(), which is why the pair has to be refused here too.
// Left unrefused, `fugaro update --repo --provision-batch` onboarded the
// repository and dropped the flag silently -- the user asked for an apply that
// costs money and got no apply and no word about it. The flag's own help says
// it excludes --repo, so the refusal names both.
func TestProvisionBatchIsRefusedOnTheRepoPath(t *testing.T) {
	o := &initOptions{provisionBatch: true, repo: true}
	err := o.checkRepo()
	if err == nil {
		t.Fatal("fugaro update --repo --provision-batch was accepted: the flag excludes --repo, and a flag that does nothing here must be refused, not ignored")
	}
	if !strings.Contains(err.Error(), "--provision-batch") || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want a refusal naming --provision-batch and --repo", err)
	}
}

// The registry repository the root is given is the one the installation's pin
// names, never the fugaro-base constant (§7.2): a grant aimed at the wrong
// repository leaves every agent unable to pull.
func TestRegistryRepositoryFollowsThePin(t *testing.T) {
	host := "us-east5-docker.pkg.dev/" + initProject
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5", RegistryHost: host}
	outs := infra.InstallationOutputs{BaseRegistry: infra.BaseRegistry, RegistryHost: host}
	if got := registryRepository(lc, outs); got != infra.BaseRegistry {
		t.Errorf("no pin: %q, want %q", got, infra.BaseRegistry)
	}
	lc.BaseImages = map[string]string{"base": host + "/" + infra.GhcrRegistry + "/acme/fugaro-base:1.2.3"}
	lc.ImageSource = "ghcr.io/acme"
	if got := registryRepository(lc, outs); got != infra.GhcrRegistry {
		t.Errorf("a pin through the remote: %q, want %q", got, infra.GhcrRegistry)
	}
	// A pin that does not go through the remote repository (an installation
	// that mirrors its base elsewhere) reads the installation's own answer.
	lc.BaseImages = map[string]string{"base": "eu.gcr.io/other/fugaro-base:1.2.3"}
	if got := registryRepository(lc, outs); got != infra.BaseRegistry {
		t.Errorf("a foreign pin: %q, want %q", got, infra.BaseRegistry)
	}
	// The host the pin is read against comes from the installation's outputs
	// when the local config does not carry it yet (a first run that also
	// provisions): reading it against an empty host would aim the grant at
	// the shared repository the pin does not go through (§7.2).
	lc.RegistryHost = ""
	lc.BaseImages = map[string]string{"base": host + "/" + infra.GhcrRegistry + "/acme/fugaro-base:1.2.3"}
	if got := registryRepository(lc, outs); got != infra.GhcrRegistry {
		t.Errorf("a first run's pin: %q, want %q", got, infra.GhcrRegistry)
	}
}

// jobsEditorChange is the launchers' Batch Job Editor grant as the compute
// root's plan would carry it, with conds as its condition blocks.
func jobsEditorChange(member string, conds []any) planChange {
	return planChange{"address": `module.compute.google_project_iam_member.launcher_jobs_editor["x"]`, "type": "google_project_iam_member",
		"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{
			"project": initProject, "role": "roles/batch.jobsEditor", "member": member, "condition": conds}}}
}

// The compute root's grants are held to the compute module's rules
// (infra.ComputeCover's GrantsNotCovered): an unconditioned Batch Job Editor
// -- every Batch job in the project, with any image and any account -- is
// refused before anything is confirmed or applied, in the plan view and in
// the apply alike. The change is the COMPUTE root's alone, so the refusal can
// only be this stage's: deleting the check from either method fails here.
func TestComputeStageHoldsItsGrantsToTheCover(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"the plan view", []string{"setup", "--yes", "--plan-only", "--provision-batch"}},
		{"the apply", []string{"setup", "--yes", "--provision-batch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typedThroughYes(t)
			r := computeRig(t)
			r.setPlanAt(t, "installation")
			r.setPlanAt(t, "compute", jobsEditorChange("serviceAccount:"+infra.ReaperAccountID+"@"+initProject+".iam.gserviceaccount.com", []any{}))
			fakeTerminal(t)
			out, _, err := executeStdin(t, initProjectName+"\n", tc.args...)
			if err == nil || !strings.Contains(err.Error(), "the compute root's plan grants what the compute module does not") ||
				!strings.Contains(err.Error(), "launcher_jobs_editor") {
				t.Fatalf("err = %v, want the compute stage's grant refusal:\n%s", err, out)
			}
			if calls := subcommands(computeCalls(t, r)); slices.Contains(calls, "apply") {
				t.Fatalf("a plan with an uncovered grant was applied: %q", calls)
			}
		})
	}
}

// propagationStderr is what an apply says when a binding names a service
// account the same apply created a moment ago and IAM does not see yet
// (design compute-backend §7.2 finding 5).
const propagationStderr = `Error: Error applying IAM policy for project "proj-1234": Error setting IAM policy for project "proj-1234": googleapi: Error 400: Service account fugaro-reaper@proj-1234.iam.gserviceaccount.com does not exist., badRequest`

// A fresh account's bindings are retried, not given up on: an apply that fails
// because IAM does not see an account yet waits, re-plans, holds the new plan
// to the guard, the grant rules and the plan that was confirmed, and applies
// what is left -- without asking again, since what is left is part of what
// was confirmed.
func TestComputeStageRetriesIAMPropagation(t *testing.T) {
	noComputeWaits(t)
	typedThroughYes(t)
	r := computeRig(t)
	r.script["apply@compute"] = []any{map[string]any{"exit": 1, "stderr": propagationStderr}, map[string]any{"exit": 0}}
	r.save(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := subcommands(computeCalls(t, r)); !slices.Equal(got, []string{"version", "init", "plan", "show", "apply", "plan", "show", "apply", "output"}) {
		t.Fatalf("terraform calls in the compute workdir = %q, want one re-plan and one more apply\n%s", got, out)
	}
	if !strings.Contains(out, "not visible to IAM yet") {
		t.Errorf("the retry was not reported:\n%s", out)
	}
	if runs := r.sched.Runs(); len(runs) != 1 {
		t.Errorf("the preload rebuild was started %d times, want once after the retried apply", len(runs))
	}
}

// What the retry does not do: retry anything that is not a propagation gap,
// retry for ever, or apply a re-plan that holds more than was confirmed.
func TestComputeStageRetriesOnlyPropagation(t *testing.T) {
	t.Run("another failure", func(t *testing.T) {
		noComputeWaits(t)
		typedThroughYes(t)
		r := computeRig(t)
		r.script["apply@compute"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded"}
		r.save(t)
		fakeTerminal(t)
		_, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
		if err == nil {
			t.Fatal("a failed apply succeeded")
		}
		if n := strings.Count(strings.Join(subcommands(computeCalls(t, r)), " "), "apply"); n != 1 {
			t.Errorf("applied %d times, want 1: only a propagation gap is retried", n)
		}
	})
	t.Run("a gap that never closes", func(t *testing.T) {
		noComputeWaits(t)
		typedThroughYes(t)
		r := computeRig(t)
		r.script["apply@compute"] = map[string]any{"exit": 1, "stderr": propagationStderr}
		r.save(t)
		fakeTerminal(t)
		_, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("err = %v, want the last propagation error", err)
		}
		if n := strings.Count(strings.Join(subcommands(computeCalls(t, r)), " "), "apply"); n != len(computeApplyWaits)+1 {
			t.Errorf("applied %d times, want %d", n, len(computeApplyWaits)+1)
		}
	})
	t.Run("a re-plan with something new", func(t *testing.T) {
		noComputeWaits(t)
		typedThroughYes(t)
		r := computeRig(t)
		first := planJSON(t, change("module.compute.google_compute_router.this", "create"))
		second := planJSON(t, change("module.compute.google_compute_router.this", "create"), change("module.compute.google_compute_firewall.extra", "create"))
		r.script["show@compute"] = []any{map[string]any{"stdout": first}, map[string]any{"stdout": second}}
		r.script["apply@compute"] = []any{map[string]any{"exit": 1, "stderr": propagationStderr}, map[string]any{"exit": 0}}
		r.save(t)
		fakeTerminal(t)
		_, _, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-batch")
		if err == nil || !strings.Contains(err.Error(), "google_compute_firewall.extra") {
			t.Fatalf("err = %v, want the retry refused for the change nobody confirmed", err)
		}
		if n := strings.Count(strings.Join(subcommands(computeCalls(t, r)), " "), "apply"); n != 1 {
			t.Errorf("applied %d times, want 1", n)
		}
	})
}

// noComputeWaits makes the propagation retry's waits instant.
func noComputeWaits(t *testing.T) {
	t.Helper()
	old := computeApplyWaits
	computeApplyWaits = make([]time.Duration, len(old))
	t.Cleanup(func() { computeApplyWaits = old })
}
