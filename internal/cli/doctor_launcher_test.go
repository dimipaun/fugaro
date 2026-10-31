package cli

import (
	"bytes"
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
	if c.OK || c.Severity != "info" {
		t.Fatalf("check = %+v, want an un-ok info line that does not fail doctor", c)
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

// The line a person reads: the launcher reach is an *info* line and --all
// prints it (it is an audit line, so the default view leaves it out).
// printDoctorChecks prints every check that is not OK, and an OK one
// too when it carries a Severity; what it prints nothing for is an OK check
// with no Severity, which is doctor's own bookkeeping (`if c.OK &&
// c.Severity == "" { continue }`, doctor.go). So this check is *not ok* and
// Severity "info": it prints for the owner's eyes, and checksFail's "info" arm
// keeps it out of the exit code. An earlier shape of this check (OK with the
// Problem) was skipped by the printer as it stood then, and that is why the
// check looks the way it does — but the printer's rule has moved, and an OK
// check carrying a Severity would print today.
//
// It also has to leave doctor's summary line alone: an info check reports what
// doctor *measured*, and counting it as a failure (printDoctorChecks' failed
// flag) printed the reach while saying nothing at all about everything else —
// "everything checked is ok" never came out on a healthy installation.
//
// Mutation (run, restore): drop the Severity from doctorLauncherReach (leaving
// it not ok) — the line still prints, but Severity "" is the arm checksFail
// fails on, so doctor exits non-zero and the ExitCode assertion below is what
// catches it. Setting OK: true alone no longer changes what is printed. The
// other half, the check not holding back doctor's summary line, is
// TestDoctorInfoChecksDoNotHoldBackTheSummary's subject.
func TestDoctorPrintsTheLauncherReach(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})

	// --all: the launcher reach is an audit line, so the default view leaves it
	// out and this test names the view that shows it.
	out, errOut, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	line := "launcher-jobs: launcher grants on Cloud Run jobs: 1 grant(s) over 1 job(s), and a launcher can start 1 of them: fugaro-acme-app-web"
	if !strings.Contains(out, line) {
		t.Errorf("doctor did not print the launcher reach:\n%s", out)
	}
	// And it is not a failure: severity "info" is what keeps an un-ok check out
	// of the exit code (checksFail), so printing it costs the caller nothing.
	if ExitCode(err) != ExitOK {
		t.Errorf("an info line failed doctor: exit %d (%v)", ExitCode(err), err)
	}
}

// The installation grants the job runner role to launchers AND operators
// (internal/infra's launchersWithOperators: operators get everything launchers
// get, and they run, watch and cancel runs too), so a job whose only grant
// names an operator is a job Fugaro can start. Counting the launcher list
// alone read it as no grant at all.
//
// The operator below (user:o@example.com) is on the operator list and on no
// launcher list, which is the whole point: a member both lists name proves
// nothing about which list the check compared against (dbLaunchers holds just
// that pair, so this test writes its own config).
//
// Mutation (run, restore): compare against lc.Terraform.Launchers again
// (doctorLauncherReach) — the operator-only job is reported as no grant, and
// "0 grant(s)" stands beside a job the operator can start.
func TestDoctorLauncherReachCountsOperators(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.WriteFile(r.cfgPath, []byte(r.cfg+"terraform: { launchers: [\"user:a@example.com\"], operators: [\"user:o@example.com\"] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:o@example.com"}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if !strings.Contains(c.Problem, "1 grant") {
		t.Errorf("an operator's grant on a job is not counted: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "can start 1 of them: fugaro-acme-app-web") {
		t.Errorf("a job only an operator is granted is not reported as one Fugaro can start: %s", c.Problem)
	}
}

// The default arm's message has to be true in the case it fires: a launcher IS
// named on these jobs' policies, on a role that cannot start one (a read-only
// grant — counted, and never a job a launcher can start). "No launcher is
// granted on any job" would be false about the lines printed beside it, and
// the reader's next move — an operator checking the grant — starts from a
// premise the same output contradicts.
//
// Mutation (run, restore): drop the `case grants > 0` arm — the default's "no
// launcher is granted on any job" comes back over two grants.
func TestDoctorLauncherReachSaysWhenAGrantCannotStart(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-read", "2", "2Gi")
	for _, n := range []string{"fugaro-acme-app-web", "fugaro-acme-app-read"} {
		r.run.SetJobPolicy(n, gcpfake.Binding{Role: "roles/run.viewer", Members: []string{"user:a@example.com"}})
	}

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if !strings.Contains(c.Problem, "can start 0 of them") || !strings.Contains(c.Problem, "2 grant") {
		t.Fatalf("the read-only grants are not reported as grants: %s", c.Problem)
	}
	if strings.Contains(c.Problem, "no launcher is granted") {
		t.Errorf("claims no launcher is granted over %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "roles that cannot start a job") {
		t.Errorf("does not say why the grants start nothing: %s", c.Problem)
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
	if !found || c.OK || c.Severity != "info" {
		t.Fatalf("check = %+v (found %v), want an info line", c, found)
	}
	if strings.Contains(c.Problem, "cannot start") {
		t.Errorf("claims no launcher can start a job from policies it could not read: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "you can start 1 of them: "+project) {
		t.Errorf("does not report the job the caller can start (with overrides, as fugaro run launches): %s", c.Problem)
	}
	// And it is an unread state, not a measurement: every policy was refused,
	// so the grants were never read, and a measurement here would print beside
	// "everything checked is ok" on an installation nobody looked at.
	if c.Class != doctorClassAudit {
		t.Errorf("launcher-jobs on a surface nobody could read is classed %q, want %q", c.Class, doctorClassAudit)
	}
}

// When not one job's policy could be read — every read failed, so not even a
// job's own permissions could be asked — the summary must not answer "no
// launcher is granted on any job": that is a claim about policies nobody read,
// and it would sit beside the launcher-jobs-unread warning that says the
// answer for those jobs is unknown, not no. What such a run can say is that it
// has no count to give.
func TestDoctorLauncherReachWithNoPolicyRead(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	project := gcp.ProjectJobName("standard", "github", "")
	r.run.AddJob(project, "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	// 500, not 403: a refused read falls back to testIamPermissions, this one
	// does not, so both jobs land in unread.
	r.run.FailPolicyOf = map[string]int{project: 500, "fugaro-acme-app-web": 500}

	checks := r.runDoctorJSON(t).Checks
	if c, found := doctorCheckByID(checks, "launcher-jobs-unread"); !found || !strings.Contains(c.Problem, project) {
		t.Fatalf("the unread warning does not name the jobs: %+v (found %v)", c, found)
	}
	c, found := doctorCheckByID(checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if strings.Contains(c.Problem, "cannot start") || strings.Contains(c.Problem, "no launcher is granted") {
		t.Errorf("claims no launcher can start a job whose policy it could not read: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "could not read the IAM policy of 2 of the location's 2 Cloud Run job(s)") {
		t.Errorf("does not say it has no count to give: %s", c.Problem)
	}
}

// A count over the policies that could be read says how many of the
// location's jobs it leaves out, whichever way the others were lost: "1
// grant(s) over 1 job(s)" must not read as a count over the location, and the
// job it leaves out is not one no launcher can start.
func TestDoctorLauncherReachSaysWhatItsCountsLeaveOut(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.AddJob("fugaro-acme-app-hid", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})
	r.run.FailPolicyOf = map[string]int{"fugaro-acme-app-hid": 500}

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "launcher-jobs")
	if !found {
		t.Fatal("doctor reports no launcher reach")
	}
	if !strings.Contains(c.Problem, "can start 1 of them: fugaro-acme-app-web") {
		t.Errorf("the readable job is not reported: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, "1 of the location's 2 job(s) are not in that count: 1 whose read failed") {
		t.Errorf("the count does not say which job it leaves out: %s", c.Problem)
	}
}

// The two informational checks doctor builds — the launcher reach over the
// Cloud Run jobs, and the project's Claude account jobs with the repositories
// each token may be used by — are OK checks with a Severity, and the printer
// skipped every OK check: both lines existed only in --json, so an operator
// running plain `fugaro doctor` never read the reach the check had just
// computed. A line that carries a Severity is one doctor means to show.
//
// Mutation caught: skipping OK checks whatever their Severity (both lines
// vanish from the printed output while --json still carries them).
func TestDoctorPrintsItsInformationLines(t *testing.T) {
	t.Run("launcher reach", func(t *testing.T) {
		r := newDoctorRig(t)
		r.withLaunchers(t)
		r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
		r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
			Members: []string{"user:a@example.com"}})
		out, _, _ := execute(t, "doctor", "--all")
		if !strings.Contains(out, "info launcher-jobs: ") || !strings.Contains(out, "can start 1 of them: fugaro-acme-app-web") {
			t.Fatalf("the launcher reach is not in the printed output:\n%s", out)
		}
	})
	t.Run("project accounts", func(t *testing.T) {
		r := projectAccountRig(t)
		putProjectSecret(t, r.sm, "aurora", "claude-oauth-token-acme-personal", []byte("fake-token-0123456789"))
		putProjectSecret(t, r.sm, "aurora", "claude-oauth-token", []byte("fake-token-9876543210"))
		out, _, _ := execute(t, "doctor", "--all")
		if !strings.Contains(out, "info project-account-stored: ") ||
			!strings.Contains(out, "repositories acme/app in claude_accounts.acme-personal.allow_data_to") {
			t.Fatalf("the project accounts' reach is not in the printed output:\n%s", out)
		}
	})
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

// TestDoctorInfoChecksDoNotHoldBackTheSummary: an "info" check is what doctor
// measured rather than what it found wrong — the launcher reach the owner
// reads after a migration, the price catalog's age, whether a policy node could
// be read — and it must cost the reader neither the line nor the sentence.
//
// printDoctorChecks sets its failed flag from the checks it prints, and every
// un-ok check used to set it: on a healthy installation whose only lines were
// info ones, doctor printed them and then said nothing about the rest, which
// is the one thing the summary line is for ("fugaro doctor: everything checked
// is ok" is the answer to "did anything fail?"). checksFail already refuses to
// fail doctor on an info check, with or without --strict, so the two readings
// of the same check had to agree.
//
// Mutation (run, restore): take the `c.Severity != "info"` guard off
// printDoctorChecks' failed flag — the summary line goes missing below, and
// the exit code stays right, which is what made the old reading invisible.
func TestDoctorInfoChecksDoNotHoldBackTheSummary(t *testing.T) {
	var out bytes.Buffer
	printDoctorChecks(&out, []doctorCheck{
		{ID: "launcher-jobs", Severity: "info", Problem: "launcher grants on Cloud Run jobs: 0 grant(s) over 1 job(s), and a launcher can start 0 of them"},
		{ID: "everything-else", OK: true},
	})
	got := out.String()
	if !strings.Contains(got, "launcher-jobs: launcher grants") {
		t.Errorf("doctor did not print the info check:\n%s", got)
	}
	if !strings.Contains(got, "fugaro doctor: everything checked is ok") {
		t.Errorf("an info check held back doctor's summary line: it is a measurement, never a failure (checksFail):\n%s", got)
	}
	if checksFail([]doctorCheck{{ID: "launcher-jobs", Severity: "info"}}, true) {
		t.Errorf("an info check fails doctor under --strict, so the exit code and the summary disagreed")
	}
}
