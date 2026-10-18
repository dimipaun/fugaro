package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// jobRunnerRole is the installation's job runner role as a binding spells
// it: the custom role ids are the installation module's (infra.RoleJobRunner)
// and a binding carries the full name.
func jobRunnerRole(project string) string {
	return "projects/" + project + "/roles/" + infra.RoleJobRunner
}

const dbLaunchers = "terraform: { launchers: [\"user:a@example.com\", \"user:o@example.com\"], operators: [\"user:o@example.com\"] }\n"

// withLaunchers gives the rig the launcher and operator lists the check
// compares each job's policy members against (the same lists dbLists gives
// the bucket check).
func (r *doctorRig) withLaunchers(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(r.cfgPath, []byte(r.cfg+dbLaunchers), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorReportsLauncherReach is plan task 3.5 step 1: the launcher role's
// permissions do not change with the project-centric model, only how many
// jobs they attach to — so doctor reports the grant count it sees, and the
// jobs a launcher can start, which is the surface the owner checks after a
// migration.
//
// The rig's launchers are user:a@example.com and user:o@example.com
// (dbLists). Three jobs are registered: a per-repository job with both
// launchers, a shared project job with one, and a job whose only binding
// names someone who is not a launcher at all. The count is the launcher
// grants alone: the third job's binding is not one, since only a launcher
// member is a launcher grant.
func TestDoctorReportsLauncherReach(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	perRepo, project := "fugaro-acme-app-web", gcp.ProjectJobName("standard", "github", "")
	for _, n := range []string{perRepo, project} {
		r.run.AddJob(n, "2", "2Gi")
	}
	r.run.AddJob("fugaro-other-app-web", "2", "2Gi")
	r.run.SetJobPolicy(perRepo, gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com", "user:o@example.com"}})
	r.run.SetJobPolicy(project, gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})
	r.run.SetJobPolicy("fugaro-other-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:someone-else@example.com"}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if !c.OK || c.Severity != "info" {
		t.Fatalf("check = %+v, want an info line that does not fail doctor", c)
	}
	// Three launcher grants: two members on the per-repository job, one on
	// the project job.
	if !strings.Contains(c.Problem, "3 grant") {
		t.Errorf("Problem does not report the grant count it saw: %s", c.Problem)
	}
	for _, want := range []string{perRepo, project} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("Problem does not name the job %s a launcher can start: %s", want, c.Problem)
		}
	}
	if strings.Contains(c.Problem, "fugaro-other-app-web") {
		t.Errorf("Problem names a job no launcher is granted on: %s", c.Problem)
	}
}

// A grant that is not a start-the-job role is a launcher grant doctor sees
// but does not call a job a launcher can start: roles/run.viewer can read a
// job and not run it, and the job-runner role is the one the migration
// attaches (modules/project/iam.tf's launcher grant).
func TestDoctorLauncherReachCountsOnlyStartableRoles(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-read", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})
	r.run.SetJobPolicy("fugaro-acme-app-read", gcpfake.Binding{Role: "roles/run.viewer",
		Members: []string{"user:a@example.com"}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if !strings.Contains(c.Problem, "2 grant") {
		t.Errorf("Problem does not report both launcher grants: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "fugaro-acme-app-web") {
		t.Errorf("Problem does not name the job a launcher can start: %s", c.Problem)
	}
	if strings.Contains(c.Problem, "fugaro-acme-app-read") {
		t.Errorf("Problem calls a read-only grant a job a launcher can start: %s", c.Problem)
	}
}

// A refused listing is a warning, never silence: the launcher surface after
// a migration is what the owner reads, and an unread surface must not look
// like a clean one.
func TestDoctorLauncherReachFailedListWarns(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.FailListWith = 500
	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found || c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "could not check") {
		t.Fatalf("check = %+v (found %v)", c, found)
	}
}

// A test configuration (no_auth with no run endpoint) reads no Cloud Run
// job, as the bucket check reads no bucket.
func TestDoctorLauncherReachSkippedWithoutARunEndpoint(t *testing.T) {
	r := newDoctorRig(t)
	cfg := strings.Replace(r.cfg+dbLaunchers, ", run: "+r.run.URL+"/", "", 1)
	if cfg == r.cfg {
		t.Fatal("the rig's config does not name the run endpoint")
	}
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs"); found {
		t.Fatal("read Cloud Run jobs under a test configuration")
	}
}
