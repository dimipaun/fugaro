package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// envJob is a Cloud Run job of the rig's installation carrying env, as
// jobs get reports it.
func (r *doctorRig) envJob(t *testing.T, name string, env map[string]string) {
	t.Helper()
	r.run.AddJob(name, "2", "2Gi")
	r.run.SetJobEnv(name, env)
}

// The report of what a run drops, per job: the names in the job's own
// environment that reach no agent, computed by the same function the
// passthrough hands the agent's variables with (docs/image-env, reconciliation
// D1377, D1378). A name the operator's image sets for itself — issue #1755's
// NEXUS_BASEURL — is not one of them, a reserved name is, and neither is a name
// the job itself declares as a secret: that reaches runs through the declared
// secret path, which is not this one.
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
	for _, name := range []string{"JAVA_TOOL_OPTIONS", "FUGARO_IMAGE_ENTRY"} {
		if !strings.Contains(c.Problem, name) {
			t.Errorf("the job's line does not name the dropped %s: %s", name, c.Problem)
		}
	}
	for _, name := range []string{"NEXUS_BASEURL", "NPM_TOKEN"} {
		if strings.Contains(c.Problem, name) {
			t.Errorf("the job's line calls %s dropped: it reaches a run (an image's own name) or arrives through the declared secret path: %s", name, c.Problem)
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
