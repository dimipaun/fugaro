package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

const dbBucket = "fugaro-runs-proj-1234"

// withRunsBucket gives the doctor rig a storage endpoint holding the runs
// bucket with policy, and the local config's lists and user.
func (r *doctorRig) withRunsBucket(t *testing.T, g *gcpfake.GCS, extra string, policy ...gcpfake.Binding) {
	t.Helper()
	g.AddBucket(dbBucket, 123456789012, map[string]string{"fugaro": "managed"})
	g.SetBucketPolicy(dbBucket, policy)
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { storage: "+g.URL+"/storage/v1/, ", 1) + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hardenedPolicy() []gcpfake.Binding {
	return []gcpfake.Binding{
		{Role: "roles/storage.objectAdmin", Members: []string{"user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:a@example.com"}},
		{Role: "roles/storage.objectUser", Members: []string{"user:a@example.com"},
			Condition: &gcpfake.IAMCondition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(dbBucket)}},
	}
}

const dbLists = "terraform: { launchers: [\"user:a@example.com\", \"user:o@example.com\"], operators: [\"user:o@example.com\"] }\n"

// runDoctorJSON runs fugaro doctor --json against the rig's project.
func (r *doctorRig) runDoctorJSON(t *testing.T) doctorOutput {
	t.Helper()
	return doctorJSON(t)
}

func TestDoctorRunsBucketIAMHardenedIsOK(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	o := r.runDoctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || !c.OK {
		t.Fatalf("check = %+v (%v)", c, ok)
	}
}

func TestDoctorRunsBucketIAMOldGrantWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, gcpfake.Binding{Role: "roles/storage.objectAdmin", Members: []string{"user:a@example.com", "user:o@example.com"}})
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" && strings.Contains(c.Fix, "fugaro init") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning: %+v", o.Checks)
	}
	if !o.OK {
		t.Fatal("a warning failed doctor without --strict")
	}
	// The summary check itself (ID exactly "runs-bucket-iam", not one of the
	// runs-bucket-iam-N findings) must also carry the warning: that is what
	// --strict acts on, and a findingChecks that forgets to aggregate would
	// leave it OK here while still emitting the per-finding warnings above.
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK || sum.Severity != "warning" {
		t.Fatalf("summary check = %+v (%v), want a warning", sum, ok)
	}
}

func TestDoctorRunsBucketIAMSelfTest(t *testing.T) {
	for _, tc := range []struct {
		name, user string
		held       []string
		sev        string
		ok         bool
	}{
		{"launcher, hardened", "a@example.com", nil, "", true},
		{"operator holds writes", "o@example.com", []string{"storage.objects.create", "storage.objects.delete"}, "", true},
		{"launcher holds writes", "a@example.com", []string{"storage.objects.create"}, "warning", false},
		{"unknown caller holds writes", "", []string{"storage.objects.create"}, "info", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, g := newDoctorRig(t), gcpfake.NewGCS(t)
			cfg := strings.Replace(r.cfg, "user: someone@example.com\n", "", 1)
			if tc.user != "" {
				cfg += "user: " + tc.user + "\n"
			}
			r.cfg = cfg
			r.withRunsBucket(t, g, dbLists)
			g.RefuseBucketPolicy(dbBucket)
			g.SetHeldPermissions(dbBucket, tc.held...)
			o := r.runDoctorJSON(t)
			c, found := doctorCheckByID(o.Checks, "runs-bucket-iam")
			if !found || c.OK != tc.ok || c.Severity != tc.sev {
				t.Fatalf("check = %+v (found %v)", c, found)
			}
		})
	}
}

// A test configuration (no_auth with no storage endpoint) reads no bucket.
func TestDoctorRunsBucketIAMSkippedWithoutAStorageEndpoint(t *testing.T) {
	r := newDoctorRig(t)
	o := r.runDoctorJSON(t)
	if _, found := doctorCheckByID(o.Checks, "runs-bucket-iam"); found {
		t.Fatal("checked a bucket under a test configuration")
	}
}

// A transient read failure (500, not a 403 refusal) must still surface a
// warning, never silence: the default: branch of doctorRunsBucketIAM's
// switch, which TestDoctorRunsBucketIAMSelfTest's RefuseBucketPolicy (403)
// cases never reach.
func TestDoctorRunsBucketIAMFailedPolicyReadWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	g.FailBucketPolicy(dbBucket)
	o := r.runDoctorJSON(t)
	c, found := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !found || c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "could not check who can write") {
		t.Fatalf("check = %+v (found %v)", c, found)
	}
}

// A launcher's project-level role (roles/editor, say) writes every bucket's
// objects whatever the bucket's own policy says: doctor must report it
// (infra.ProjectStorageWriters), not just the hardened bucket policy.
func TestDoctorRunsBucketIAMProjectEditorWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/editor", Members: []string{"user:a@example.com"}})
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" &&
			strings.Contains(c.Problem, "roles/editor") && strings.Contains(c.Fix, "fugaro init --operator") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no project-editor warning: %+v", o.Checks)
	}
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK {
		t.Fatalf("summary = %+v (%v), want a warning", sum, ok)
	}
}

// When the project's own policy can't be read, a launcher who bypasses the
// bucket's policy entirely through a project-level role would otherwise go
// undetected: that must be a warning, not a silent OK.
func TestDoctorRunsBucketIAMProjectPolicyUncheckedWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	r.crm.DenyPolicy("proj-1234")
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" && strings.Contains(c.Problem, "could not check which launchers hold project-level storage roles") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no unchecked-project warning: %+v", o.Checks)
	}
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK {
		t.Fatalf("summary = %+v (%v), want a warning", sum, ok)
	}
}

// findingChecks must redact a finding's Fix the same way it redacts Problem:
// Fix carries the raw IAM member (so the printed gcloud command is actually
// correct to paste), but the doctorCheck it becomes is printed to a
// terminal, so control characters and ANSI escapes in a member name must
// never reach it unescaped.
func TestFindingChecksRedactsControlCharactersInFix(t *testing.T) {
	const raw = "user:x\x1b[31m@example.com"
	checks := findingChecks([]infra.BucketFinding{{Severity: "warning", Problem: "p", Fix: "gcloud ... --member=" + raw}})
	if len(checks) != 2 {
		t.Fatalf("checks = %+v", checks)
	}
	fix := checks[1].Fix
	if strings.Contains(fix, "\x1b") {
		t.Fatalf("Fix leaked a raw escape byte: %q", fix)
	}
	if !strings.Contains(fix, `\u001b`) {
		t.Fatalf("Fix was not escaped: %q", fix)
	}
}
