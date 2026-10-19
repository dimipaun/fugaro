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

// A public member that can start a job is a finding, not a count: anyone on
// the internet (allUsers) or with any Google account (allAuthenticatedUsers)
// holding a start role on a job could launch it — and with runWithOverrides,
// set its environment. The launcher count never includes them, so without
// this the info line would read clean over a public job.
func TestDoctorLauncherReachWarnsOnAPublicStartGrant(t *testing.T) {
	for _, member := range []string{"allUsers", "allAuthenticatedUsers"} {
		t.Run(member, func(t *testing.T) {
			r := newDoctorRig(t)
			r.withLaunchers(t)
			r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
			r.run.AddJob("fugaro-acme-app-read", "2", "2Gi")
			r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
				Members: []string{"user:a@example.com", member}})
			r.run.SetJobPolicy("fugaro-acme-app-read", gcpfake.Binding{Role: "roles/run.viewer",
				Members: []string{member}})
			c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs-public")
			if !found || c.OK || c.Severity != "warning" {
				t.Fatalf("check = %+v (found %v), want a warning", c, found)
			}
			if !strings.Contains(c.Problem, "fugaro-acme-app-web") || strings.Contains(c.Problem, "fugaro-acme-app-read") {
				t.Errorf("Problem must name the job the public member can start, and only it: %s", c.Problem)
			}
		})
	}
}

// With no launcher in the local config (a launcher's own shared config may
// carry none), a zero count is not evidence that no launcher is granted: the
// line says what it could not compare against rather than that fugaro run
// cannot start a job.
func TestDoctorLauncherReachWithoutALauncherList(t *testing.T) {
	r := newDoctorRig(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})
	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if strings.Contains(c.Problem, "cannot start") {
		t.Errorf("claims no launcher can start a job from an empty launcher list: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "no launcher") || !strings.Contains(c.Problem, "local config") {
		t.Errorf("does not say the local config lists no launcher: %s", c.Problem)
	}
}

// The predefined role that carries run.jobs.runWithOverrides is a start role
// too: a launcher granted it on a job can start the job as fugaro run does.
func TestDoctorLauncherReachKnowsTheOverridesExecutorRole(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: "roles/run.jobsExecutorWithOverrides",
		Members: []string{"user:a@example.com"}})
	c, _ := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !strings.Contains(c.Problem, "can start 1 of them: fugaro-acme-app-web") {
		t.Errorf("does not count the overrides executor role as a start role: %s", c.Problem)
	}
}

// One job whose policy cannot be read (a failure, not a refusal) is named in a warning, not counted as a
// job no launcher can start: an unread policy must not look like an empty one.
func TestDoctorLauncherReachUnreadPolicyWarns(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-hid", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})
	r.run.FailPolicyOf = map[string]int{"fugaro-acme-app-hid": 500}
	checks := r.runDoctorJSON(t).Checks
	c, found := doctorCheckByID(checks, "launcher-jobs-unread")
	if !found || c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "fugaro-acme-app-hid") {
		t.Fatalf("check = %+v (found %v), want a warning naming the unread job", c, found)
	}
	if info, _ := doctorCheckByID(checks, "launcher-jobs"); !strings.Contains(info.Problem, "can start 1 of them: fugaro-acme-app-web") {
		t.Errorf("the readable job is not reported beside the unread one: %s", info.Problem)
	}
}

// A plain launcher holds run.jobs.list and not run.jobs.getIamPolicy (the
// installation's fugaroLauncher role), so every policy read is refused. That
// is the ordinary launcher's doctor, not a fault: it must not warn about every
// job, nor claim that no launcher can start one. It reports instead which jobs
// the caller can start, from jobs:testIamPermissions, as the bucket check
// falls back to the caller's own permissions.
func TestDoctorLauncherReachAsAPlainLauncher(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	project := gcp.ProjectJobName("standard", "github", "")
	r.run.AddJob(project, "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.FailPolicyOf = map[string]int{project: 403, "fugaro-acme-app-web": 403}
	r.run.HeldOn = map[string][]string{project: {"run.jobs.run", "run.jobs.runWithOverrides"},
		"fugaro-acme-app-web": {"run.jobs.run"}}
	checks := r.runDoctorJSON(t).Checks
	if c, found := doctorCheckByID(checks, "launcher-jobs-unread"); found {
		t.Errorf("a refused policy read warned: %+v", c)
	}
	c, found := doctorCheckByID(checks, "launcher-jobs")
	if !found || !c.OK || c.Severity != "info" {
		t.Fatalf("check = %+v (found %v), want an info line", c, found)
	}
	if strings.Contains(c.Problem, "cannot start") {
		t.Errorf("claims no launcher can start a job from policies it could not read: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "you can start 1 of them: "+project) {
		t.Errorf("does not report the job the caller can start (with overrides, as fugaro run launches): %s", c.Problem)
	}
}

// TestDoctorLauncherReachEndsOnARepeatedPageToken: the jobs listing is the
// SDK's own page loop, which follows whatever next-page token the server
// answers with and never notices one it has already followed. A server that
// answers the same token on every page would have doctor walk forever; the
// bound ends the walk and the check reports it as the failed read every other
// failure of this read is reported as, never as a shorter list of jobs (a
// listing cut short reads as jobs a launcher cannot start).
//
// Mutation (verified, run and restored): delete the `seen[next]` guard from
// doctorLauncherReach -- this test fails on the page cap's message instead
// ("runs past 200 pages"), which is the guard's own answer missing. On its
// own the walk still ends, which is the point of keeping both guards: the
// repeat guard is what names the real cause and keeps a listing of a few
// pages from being walked to the bound. Its companion below covers the page
// cap by never repeating a token.
func TestDoctorLauncherReachEndsOnARepeatedPageToken(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.LoopListToken = true

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v, want the warning a failed read is", c)
	}
	if !strings.Contains(c.Problem, "repeated page token") {
		t.Errorf("Problem does not say the server repeated a page token: %s", c.Problem)
	}
}

// TestDoctorLauncherReachEndsOnAnEndlessListing is the same bound's other
// half: a server whose token is never empty and never a repeat ends no walk
// that only remembers tokens, so the page cap is what ends this one. A walk
// that ran past it would hold a job list nobody bounded.
//
// Mutation (verified, run and restored): delete the `pages >= doctorMaxJobPages`
// guard -- this test hangs until the go test timeout kills it.
func TestDoctorLauncherReachEndsOnAnEndlessListing(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.EndlessListToken = true

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v, want the warning a failed read is", c)
	}
	if !strings.Contains(c.Problem, "runs past 200 pages") {
		t.Errorf("Problem does not say the listing ran past the bound: %s", c.Problem)
	}
}
