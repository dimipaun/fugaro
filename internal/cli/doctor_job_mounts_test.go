package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// mountJob is a workflow job of the rig's installation whose template mounts
// the secrets refs maps variable -> container, the way modules/workflow/job.tf
// deploys the mounts before the flip: one a run lands on, labelled the way the
// workflow module labels the job it deploys, which is what makes the report
// consider it at all.
func (r *doctorRig) mountJob(t *testing.T, job string, refs map[string]string) {
	t.Helper()
	r.run.SetJob(job, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: "acme-app", gcp.LabelWorkflow: "web",
	}, "img:1")
	r.run.SetJobSecretRefs(job, refs)
}

// TestDoctorJobMountsFlagsAWorkflowSecret is the red step: a deployed job whose
// template mounts a secret under a variable no other class claims is a job
// still mounting one of the workflow's own secrets, and doctor lists it by job
// and variable name (T3's class, IS10, D3331).
func TestDoctorJobMountsFlagsAWorkflowSecret(t *testing.T) {
	r := newDoctorRig(t)
	const job = "fugaro-acme-app-web"
	r.mountJob(t, job, map[string]string{
		"NPM_TOKEN": gcp.SecretID("acme-app-ab12cd34", "npm-token"),
	})

	o := r.runDoctorJSON(t)
	c, found := doctorCheckByID(o.Checks, doctorJobMountID)
	if !found {
		t.Fatalf("--json carries no %s line for a job mounting a workflow secret: %+v", doctorJobMountID, o.Checks)
	}
	if !strings.Contains(c.Problem, job) || !strings.Contains(c.Problem, "NPM_TOKEN") {
		t.Fatalf("the line does not name the job and the variable: %q", c.Problem)
	}
	if c.Class != "measure" || !c.OK {
		t.Fatalf("the line is not a measurement (OK %v, class %q): before the flip these mounts are the vertex and unserved repositories' own, so it fails nothing", c.OK, c.Class)
	}

	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out, doctorJobMountID+": ") || !strings.Contains(out, "NPM_TOKEN") {
		t.Fatalf("--all does not carry the line:\n%s", out)
	}
}

// TestDoctorJobMountsIgnoresOtherClasses: the report is of T3's class alone. A
// git credential, a model credential, a provider's key and any plain-value
// variable are other classes (every one of their variables is one Fugaro
// reserves), so a job mounting only those gets no line at all — the mounts are
// in the body doctor reads, which the control below pins, so a filter that
// stopped applying would put them on the report.
func TestDoctorJobMountsIgnoresOtherClasses(t *testing.T) {
	r := newDoctorRig(t)
	const job = "fugaro-acme-app-web"
	r.mountJob(t, job, map[string]string{
		config.ReservedSecrets[config.BitbucketSecret]:  gcp.SecretID("acme-app-ab12cd34", config.BitbucketSecret),
		config.ReservedSecrets[config.ClaudeOAuthToken]: gcp.SecretID("acme-app-ab12cd34", "claude-oauth-token"),
		config.ReservedSecrets[config.AnthropicSecret]:  gcp.SecretID("acme-app-ab12cd34", config.AnthropicSecret),
		config.ProviderKeyEnv("openrouter-api-key"):     gcp.SecretID("acme-app-ab12cd34", "openrouter-api-key"),
	})
	// Plain-value variables stand in the same env list as the mounts, which is
	// what a real job's container carries. NODE_ENV is no name Fugaro reserves,
	// so only the check that an entry has to be a value_source.secret_key_ref to
	// be a mount keeps it off this report — without it every plain variable of
	// every job would be listed as a mounted workflow secret. FUGARO_IMAGE_ENTRY
	// is the stamp the control below reads from that same body; it is reserved,
	// so it never would have told that check anything.
	r.run.SetJobEnv(job, map[string]string{"FUGARO_IMAGE_ENTRY": "acme-base", "NODE_ENV": "production"})

	o := r.runDoctorJSON(t)
	// The other report's line about this job is the control: it is built from
	// the same jobs get answer the mounts were set on, so its stamp proves the
	// body doctor read really carried the reserved mounts this test is about —
	// without it the test would pass on a job that mounts nothing at all.
	env, found := doctorCheckByID(o.Checks, "image-env-"+job)
	if !found || !strings.Contains(env.Problem, "acme-base") {
		t.Fatalf("the read carried no environment for %s, so this test read no mounts either: %+v", job, o.Checks)
	}
	for _, c := range o.Checks {
		if c.ID == doctorJobMountID || c.ID == doctorJobMountUnreadID {
			t.Fatalf("a job mounting only other classes got a line: %+v", c)
		}
	}
	out, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(out, doctorJobMountID+": ") {
		t.Fatalf("--all carries a line for a job that mounts no workflow secret:\n%s", out)
	}
}

// TestDoctorJobMountsJSON: the rows --json carries are the ones the human view
// prints, one per job, by job and variable name, and no line carries a value.
func TestDoctorJobMountsJSON(t *testing.T) {
	r := newDoctorRig(t)
	r.mountJob(t, "fugaro-acme-app-web", map[string]string{
		"FIREBASE_SA_KEY": "projects/proj-1234/secrets/" + gcp.SecretID("acme-app-ab12cd34", "firebase-sa-key"),
	})
	r.mountJob(t, "fugaro-acme-app-nightly", map[string]string{
		"NPM_TOKEN": gcp.SecretID("acme-app-ab12cd34", "npm-token"),
	})

	o := r.runDoctorJSON(t)
	rows := checksByID(o, doctorJobMountID)
	if len(rows) != 2 {
		t.Fatalf("%s emitted %d row(s), want one per job: %+v", doctorJobMountID, len(rows), rows)
	}
	var problems []string
	for _, c := range rows {
		if !c.OK || c.Severity != "info" || c.Class != "measure" {
			t.Errorf("row = %+v, want an ok measurement at info severity", c)
		}
		problems = append(problems, c.Problem)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"fugaro-acme-app-web", "FIREBASE_SA_KEY", "fugaro-acme-app-nightly", "NPM_TOKEN"} {
		if !strings.Contains(joined, want) {
			t.Errorf("--json rows do not name %s:\n%s", want, joined)
		}
	}
	// The container IDs are what the reference names and are never printed: the
	// line is about the variable a job still reads, and the report never echoes
	// anything but names.
	for _, id := range []string{"fugaro-acme-app-ab12cd34-npm-token", gcp.SecretID("acme-app-ab12cd34", "firebase-sa-key")} {
		if strings.Contains(joined, id) {
			t.Errorf("--json carries the container ID %s:\n%s", id, joined)
		}
	}
}
