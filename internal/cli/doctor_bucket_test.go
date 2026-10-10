package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
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
