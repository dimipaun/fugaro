package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runner"
)

// envJob is a workflow job of the rig's installation carrying env, as jobs get
// reports it: one a run lands on, labelled the way the workflow module labels
// the job it deploys (deploy/terraform/gcp/modules/workflow/job.tf), which is
// what makes the report consider it at all.
func (r *doctorRig) envJob(t *testing.T, name string, env map[string]string) {
	t.Helper()
	r.labelledEnvJob(t, name, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: "acme-app", gcp.LabelWorkflow: "web",
	}, env)
}

// labelledEnvJob is a job of the rig's installation with labels of the caller's
// choosing, for the tests about which jobs the report is made of.
func (r *doctorRig) labelledEnvJob(t *testing.T, name string, labels, env map[string]string) {
	t.Helper()
	r.run.SetJob(name, labels, "img:1")
	r.run.SetJobEnv(name, env)
}

// The report of what a run drops, per job: the names the job sets itself that
// reach no agent, computed by the same function the passthrough hands the
// agent's variables with (docs/image-env, reconciliation D1377, D1378). A name
// the operator's image sets for itself — issue #1755's NEXUS_BASEURL — is not
// one of them, a reserved name an operator set on the job is, and neither is a
// name the job itself declares as a secret (that reaches runs through the
// declared secret path) nor any of the names Fugaro's own deployment writes
// (that is the next test's subject).
//
// Mutation (verified, run and restored): drop the doctorImageEnvChecks call
// from runDoctor — the report is absent from --all and --json, which is the
// state issue #1755 was opened in (F14: no command read a job's environment).
func TestDoctorAllNamesAJobsDroppedImageVariables(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
		"FUGARO_SECRET_ENVS": "NPM_TOKEN",
		"JAVA_TOOL_OPTIONS":  "-Dorg.gradle.daemon=false",
		"NPM_TOKEN":          "placeholder",
		"NEXUS_BASEURL":      "https://example.invalid/repository/maven-public/",
	})

	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out, "image-env-fugaro-acme-app-web: ") || !strings.Contains(out, "acme-base") {
		t.Fatalf("--all does not carry the job's report:\n%s", out)
	}
	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "image-env-fugaro-acme-app-web")
	if !found {
		t.Fatal("--json carries no report for the job")
	}
	if !strings.Contains(c.Problem, "JAVA_TOOL_OPTIONS") {
		t.Errorf("the job's line does not name the dropped JAVA_TOOL_OPTIONS: %s", c.Problem)
	}
	for _, name := range []string{"FUGARO_IMAGE_ENTRY", "NEXUS_BASEURL", "NPM_TOKEN"} {
		if strings.Contains(c.Problem, name) {
			t.Errorf("the job's line calls %s dropped: it is Fugaro's own, reaches a run as an image's own name, or arrives through the declared secret path: %s", name, c.Problem)
		}
	}
}

// The report is made of the jobs a run lands on and of no others. A real
// installation's location holds Fugaro's own support jobs — the reaper, the
// coordinator, the history and catalog checks, a repository's image check — and
// whatever else shares the project; none of them runs a task, so no variable of
// an image could reach a run through one, and a line calling it not
// catalog-stamped is a wrong measurement on every installation. The two kinds a
// run does land on — a workflow job and a shared project job — are reported.
//
// Mutation (verified, run and restored): drop the doctorTaskJob guard, so every
// listed job is reported — this test fails on the reaper's and the foreign
// job's line.
func TestDoctorImageEnvReportsOnlyTheJobsARunLandsOn(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{"FUGARO_IMAGE_ENTRY": "acme-base"})
	// The reaper as the compute module deploys it, and a job of another team's
	// in the same project and region.
	r.labelledEnvJob(t, "fugaro-reaper", map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: "reaper",
	}, map[string]string{"NEXUS_BASEURL": "https://example.invalid/repository/maven-public/"})
	r.labelledEnvJob(t, "someone-elses", map[string]string{"team": "other"}, nil)
	// The shared project job of a (profile, provider, account) triple, with the
	// project module's labels and no stamp: its image is the installation's
	// base image, which no catalog entry chose.
	r.labelledEnvJob(t, "fugaro-p-team-github", map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "team", "fugaro_provider": "github", "fugaro_account": "",
	}, map[string]string{"FUGARO_BUCKET": "gs://fugaro-runs-proj-1234"})

	o := r.runDoctorJSON(t)
	for _, job := range []string{"fugaro-reaper", "someone-elses"} {
		if c, found := doctorCheckByID(o.Checks, "image-env-"+job); found {
			t.Errorf("%s is not a job a run lands on, and the report carries a line for it anyway: %s", job, c.Problem)
		}
	}
	for _, job := range []string{"fugaro-acme-app-web", "fugaro-p-team-github"} {
		if _, found := doctorCheckByID(o.Checks, "image-env-"+job); !found {
			t.Errorf("--json carries no report for %s, which is a job a run lands on", job)
		}
	}
	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, job := range []string{"fugaro-reaper", "someone-elses"} {
		if strings.Contains(out, "image-env-"+job+": ") {
			t.Errorf("--all prints a report for %s:\n%s", job, out)
		}
	}
}

// doctorTaskJob is the line between the jobs this report is about and the rest
// of the location, and both halves are pinned here: the labels a job a run
// lands on wears (the workflow module's, and the project module's) are reported,
// and Fugaro's own support jobs, a foreign job and a job with no labels at all
// are not.
func TestDoctorTaskJobIsAJobARunLandsOn(t *testing.T) {
	landed := []map[string]string{
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: "acme-app", gcp.LabelWorkflow: "web"},
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "team", "fugaro_provider": "github", "fugaro_account": ""},
	}
	for _, labels := range landed {
		if !doctorTaskJob(labels) {
			t.Errorf("%v is how a job a run lands on is labelled, and the report leaves it out", labels)
		}
	}
	skipped := []map[string]string{
		// Fugaro's own support jobs, as deploy/terraform/gcp labels them: the
		// reaper, the coordinator, the history and catalog checks, and a
		// repository's image check.
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: "reaper"},
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: "coordinator"},
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: "history"},
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: "catalog-check"},
		{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: "acme-app", gcp.LabelRole: gcp.RoleCheck},
		// A job of somebody else's, and a job the API listed with no labels.
		{"team": "other"},
		nil,
	}
	for _, labels := range skipped {
		if doctorTaskJob(labels) {
			t.Errorf("%v is not a job a run lands on, and the report considers it", labels)
		}
	}
}

// The job's own environment on a real installation is what Fugaro's deployment
// writes into it (internal/infra's spec: the FUGARO_ family, and CLOUD_ML_REGION
// and ANTHROPIC_VERTEX_PROJECT_ID on a vertex job). None of that is an image's
// variable, all of it is what the run reads to find its bucket, its project, its
// budget and its credentials, and all of it the job that deployed it keeps —
// so a line listing them as "names a run drops" tells an operator their working
// job is broken and buries the names this report is for.
//
// Mutation (verified, run and restored): drop the doctorJobForeignEnv call, so
// the subtraction runs over the job's whole environment — the deployment's own
// names are reported as dropped and this test fails on each of them.
func TestDoctorAllLeavesTheDeploymentsOwnNamesOut(t *testing.T) {
	r := newDoctorRig(t)
	deployed := map[string]string{
		"FUGARO_BUCKET":               "gs://fugaro-runs-proj-1234",
		"FUGARO_BACKEND":              "cloudrun",
		"FUGARO_GCP_PROJECT":          "proj-1234",
		"FUGARO_PROJECT":              "aurora",
		"FUGARO_REGION":               "us-east5",
		"FUGARO_IMAGE_ENTRY":          "acme-base",
		"FUGARO_SECRET_ENVS":          "",
		"CLOUD_ML_REGION":             "us-east5",
		"ANTHROPIC_VERTEX_PROJECT_ID": "proj-1234",
	}
	r.envJob(t, "fugaro-acme-app-web", deployed)

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "image-env-fugaro-acme-app-web")
	if !found {
		t.Fatal("--json carries no report for the job")
	}
	if !strings.Contains(c.Problem, "acme-base") {
		t.Errorf("the job's line does not name the catalog entry its image came from: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "drops none") {
		t.Errorf("a job whose environment is only the deployment's own does not read as dropping none of it: %s", c.Problem)
	}
	for name := range deployed {
		if strings.Contains(c.Problem, name) {
			t.Errorf("the job's line calls %s — a name Fugaro's own deployment writes — dropped: %s", name, c.Problem)
		}
	}
}

// doctorFugaroWritten is the line between the names this report leaves out and
// the names it is about, and both halves are pinned here: every name the
// deployment writes into a job (internal/infra's spec, whose constants are what
// a rename would move) is Fugaro's own whatever it is spelled, and a name an
// operator or an image can set is not. The prefix is also pinned to
// config.ReservedEnvPrefixes, which is what makes the family unreachable for an
// image in the first place: a report may only leave out names an image could
// not have set.
func TestDoctorFugaroWrittenIsTheDeploymentsOwnNames(t *testing.T) {
	own := []string{
		infra.BucketEnv, infra.BackendEnv, infra.GCPProjectEnv, infra.ProjectEnv, infra.RegionEnv,
		infra.VertexRegionEnv, infra.VertexProjectEnv,
		runner.ImageEntryEnv, runner.SecretEnvsVar, runner.CheckoutEnv, runner.RemoteEnv,
		runner.ModelProvidersEnv, runner.ModelPricesEnv, runner.MaxRunTokensEnv, runner.AllowedModelsEnv,
		runner.MaxRunUSDEnv, runner.RTDBURLEnv, runner.FirebaseAPIKeyEnv, runner.BudgetModeEnv,
		providers.EnvGitHubAppID,
		// Case, as an environment name is read: the reservation is
		// case-insensitive, so this line is too.
		strings.ToLower(infra.BucketEnv),
	}
	for _, name := range own {
		if !doctorFugaroWritten(name) {
			t.Errorf("%s is a name the deployment writes, and the report does not leave it out", name)
		}
	}
	if !slices.Contains(config.ReservedEnvPrefixes, doctorFugaroEnvPrefix) {
		t.Errorf("%s is not a reserved prefix: the report would leave out a name an image may set", doctorFugaroEnvPrefix)
	}
	for _, name := range []string{"JAVA_TOOL_OPTIONS", "PATH", "NEXUS_BASEURL", "NPM_TOKEN", "CLOUD_RUN_JOB", "LD_PRELOAD"} {
		if doctorFugaroWritten(name) {
			t.Errorf("%s is not a name the deployment writes, and the report leaves it out", name)
		}
	}
}

// The report is names only, never a value (D1375): a value an image sets is the
// operator's own configuration, the agent reads it in its environment, and
// nothing of it belongs in doctor's output or in a --json a machine reads.
//
// The value below is recognisable on purpose: the assertion is that the name is
// printed and the value appears nowhere in either view.
//
// Mutation (verified, run and restored): print the values beside the names
// (join the job's environment as NAME=value) — this test fails on the value
// found in the output, and so does the report's own promise to the reviewer.
func TestDoctorAllPrintsNoImageValues(t *testing.T) {
	const marker = "https://example.invalid/nexus-marker-9f1c/"
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
		"NEXUS_BASEURL":      marker,
		"JAVA_TOOL_OPTIONS":  "-Dmarker=" + marker,
	})

	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out, "JAVA_TOOL_OPTIONS") {
		t.Fatalf("--all does not name the variable the run drops:\n%s", out)
	}
	o := r.runDoctorJSON(t)
	c, found := doctorCheckByID(o.Checks, "image-env-fugaro-acme-app-web")
	if !found {
		t.Fatal("--json carries no report for the job")
	}
	if !strings.Contains(out, "JAVA_TOOL_OPTIONS") || !strings.Contains(c.Problem, "JAVA_TOOL_OPTIONS") {
		t.Errorf("the report does not name the dropped variable: %s", c.Problem)
	}
	if strings.Contains(out, "nexus-marker-9f1c") {
		t.Errorf("--all prints a value of the job's environment:\n%s", out)
	}
	raw, _, err := execute(t, "doctor", "--json", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(raw, "nexus-marker-9f1c") {
		t.Errorf("--json prints a value of the job's environment:\n%s", raw)
	}
}

// A job with no stamp — no catalog entry chose its image, so a run of it passes
// none of the image's own variables whatever they are — is reported as not
// catalog-stamped rather than as an empty list of dropped names: the two are
// different facts, and "drops nothing" would be the false one (D1371's
// fail-closed).
//
// Both shapes of "no stamp" are covered: the key absent (a job deployed before
// the stamp existed) and the key present and empty (what the deploy writes for
// a repository-built image).
func TestDoctorAllMarksAnUnstampedJob(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "",
		"NEXUS_BASEURL":      "https://example.invalid/repository/maven-public/",
	})
	r.envJob(t, "fugaro-acme-app-old", map[string]string{
		"NEXUS_BASEURL": "https://example.invalid/repository/maven-public/",
	})

	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, job := range []string{"fugaro-acme-app-web", "fugaro-acme-app-old"} {
		c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "image-env-"+job)
		if !found {
			t.Fatalf("--json carries no report for %s", job)
		}
		if !strings.Contains(c.Problem, "not catalog-stamped") {
			t.Errorf("%s is not reported as not catalog-stamped: %s", job, c.Problem)
		}
		if strings.Contains(c.Problem, "drops none") || strings.Contains(c.Problem, "drops 0") {
			t.Errorf("%s reads as a job that drops nothing, which is the fact this is not: %s", job, c.Problem)
		}
		if !strings.Contains(out, "image-env-"+job+": ") {
			t.Errorf("--all does not carry the report of %s:\n%s", job, out)
		}
	}
}

// The report is a measurement, not a finding: nothing is wrong in it (the image
// is the operator's, the rule is Fugaro's — F13), so the default screen is
// untouched and one installation with nothing wrong still prints one line and
// no pointer. --all and --json are where it prints.
func TestDoctorDefaultIsStillOneScreen(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
		"JAVA_TOOL_OPTIONS":  "-Dorg.gradle.daemon=false",
	})

	out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	if strings.Contains(out, "image-env") {
		t.Errorf("the default view prints the report:\n%s", out)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if !strings.Contains(all, "image-env-fugaro-acme-app-web: ") {
		t.Errorf("--all does not print the report:\n%s", all)
	}
}

// --json carries the report whole: the per-job ID, the catalog entry, and the
// names a run drops, classified as what it is — a measurement, so a reader of
// the JSON is not told something is wrong with the installation.
func TestDoctorJSONCarriesTheDroppedNames(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
		"JAVA_TOOL_OPTIONS":  "-Dorg.gradle.daemon=false",
	})
	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "image-env-fugaro-acme-app-web")
	if !found {
		t.Fatal("--json carries no report for the job")
	}
	if !c.OK || c.Severity != "info" || c.Class != doctorClassMeasure {
		t.Fatalf("check = %+v, want an ok info measurement that fails nothing", c)
	}
	if !strings.Contains(c.Problem, "acme-base") || !strings.Contains(c.Problem, "JAVA_TOOL_OPTIONS") {
		t.Errorf("the report does not carry the entry and the dropped name: %s", c.Problem)
	}
}

// A read that did not answer is a warning, never a silent measurement: the
// names a run drops are then unknown, which is not the same fact as none, and
// an unknown filed as a measurement prints beside "everything checked is ok"
// (doctorMeasureIDs). The launcher reach is read from the same listing and
// warns on its own line; this one is the report's own.
func TestDoctorImageEnvUnreadWarns(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
	})
	r.run.FailListWith = 500

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, doctorImageEnvUnreadID)
	if !found || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (found %v), want the warning a failed read is", c, found)
	}
	if !strings.Contains(c.Problem, "could not read") {
		t.Errorf("the warning does not say the read failed: %s", c.Problem)
	}
}

// The other half of the same failure: the listing answers and a job's own read
// does not. A job nobody could read is named in the warning and carries no
// per-job line of its own — a report for it would have to invent the names it
// drops, and the empty list that inventing produces is "drops nothing", which
// is the one answer this line must never give for a read that failed.
//
// Mutation (verified, run and restored): file the unread state as a measurement
// (OK, Severity "info") instead of a warning — the check classifies as a
// measurement and prints beside "everything checked is ok", and this test and
// TestDoctorImageEnvUnreadWarns both fail on it.
func TestDoctorImageEnvUnreadNamesTheJobItCouldNotRead(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{
		"FUGARO_IMAGE_ENTRY": "acme-base",
		"JAVA_TOOL_OPTIONS":  "-Dorg.gradle.daemon=false",
	})
	r.run.SetJobFaults(gcpfake.JobFaults{GetStatus: 500})

	o := r.runDoctorJSON(t)
	c, found := doctorCheckByID(o.Checks, doctorImageEnvUnreadID)
	if !found || c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v (found %v), want the warning a job nobody could read is", c, found)
	}
	if !strings.Contains(c.Problem, "fugaro-acme-app-web") {
		t.Errorf("the warning does not name the job it could not read: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "unknown, not none") {
		t.Errorf("the warning does not say the answer is unknown rather than none: %s", c.Problem)
	}
	if _, found := doctorCheckByID(o.Checks, "image-env-fugaro-acme-app-web"); found {
		t.Error("a job whose read failed carries a per-job report anyway: it would have to invent the names it drops")
	}
}

// A test configuration (no_auth with no run endpoint) reads no Cloud Run job's
// environment either, as it reads no bucket and no IAM policy: without the skip
// doctor would send this listing to the real https://run.googleapis.com from a
// configuration whose whole point is that every read a test makes lands on a
// fake, and the answer no rig gives would surface as a warning about a read
// that failed — a network call dressed as a measurement.
//
// Mutation (verified, run and restored): remove the guard, so
// doctorImageEnvChecks runs under this configuration — this test fails on the
// check the unguarded read leaves.
func TestDoctorImageEnvSkippedWithoutARunEndpoint(t *testing.T) {
	r := newDoctorRig(t)
	r.envJob(t, "fugaro-acme-app-web", map[string]string{"FUGARO_IMAGE_ENTRY": "acme-base"})
	cfg := strings.Replace(r.cfg, ", run: "+r.run.URL+"/", "", 1)
	if cfg == r.cfg {
		t.Fatal("the rig's config does not name the run endpoint")
	}
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.runDoctorJSON(t).Checks {
		if strings.HasPrefix(c.ID, "image-env") {
			t.Fatalf("read a Cloud Run job's environment under a test configuration: %+v", c)
		}
	}
}
