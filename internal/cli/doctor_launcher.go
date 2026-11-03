package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorMaxJobPages is the most pages the launcher-jobs check follows while
// listing a location's Cloud Run jobs: at the 100 a page it asks for, room for
// 20,000 jobs, which is far past any installation's job set (one per
// (profile, provider, account) plus one per per-repository workflow). It is a
// bound on a walk that has gone wrong rather than a capacity: the SDK follows
// whatever next-page token the server answers with, and doctor must end
// whatever the server does (internal/managed's inventoryMaxPages is the same
// guard on its own listing).
const doctorMaxJobPages = 200

// doctorLauncherReach is the launcher-jobs check (plan task 3.5 step 1): the
// grant count doctor sees on the project's Cloud Run jobs, and the jobs a
// launcher can start.
//
// The project-centric model changes no permission a launcher holds — the
// launcher role is `run.jobs.run`, `run.jobs.runWithOverrides` and the
// secret metadata list, exactly as before — and it changes *how many* jobs
// those permissions attach to: one shared job per (profile, provider,
// account) instead of one job per (repository, workflow). A migration
// therefore shows up here as a falling grant count over fewer jobs, and this
// is the surface the owner reads to see it, rather than the count of jobs in
// a plan.
//
// What it counts is per-job bindings only, exactly as the bucket check
// counts the bucket's own policy beside the project-level roles that bypass
// it: a launcher who holds a project-level run role (roles/run.admin,
// roles/owner, roles/editor) can start every job in the project whatever the
// jobs' policies say, and the jobs' policies cannot show that. The launcher
// role's own permissions are a fact of the installation module, not of any
// job, so this check reports grants and does not judge them: it is an info
// line — not a failure under --strict, and still printed, which is what makes
// the surface readable at all (the check carries a Problem and is not OK, with
// Severity "info": printDoctorChecks prints every check that is not OK, and an
// OK one only when it carries a Severity, so an un-ok check is the shape that
// prints either way; and an info one neither fails doctor nor holds back the
// "everything checked is ok" line). Only a read that fails is a warning.
//
// Read-only, and metadata only: job names and their IAM policies, never an
// execution, an override or an env var. A test configuration (no_auth with
// no run endpoint) is skipped, so no real Cloud Run job is read from one.
func doctorLauncherReach(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return nil
	}
	unchecked := func(err error) []doctorCheck {
		return []doctorCheck{{ID: "launcher-jobs", Severity: "warning",
			Problem: "could not check which Cloud Run jobs a launcher can start: " + oneLineCLI(err.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"}}
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return unchecked(err)
	}
	launchers := launcherMembers(lc.Terraform.Launchers, lc.Terraform.Operators)
	parent := fmt.Sprintf("projects/%s/locations/%s", lc.GCPProject, lc.Region)
	var (
		jobs      []string // every job in the location, by name
		grants    int      // (job, launcher) bindings
		startable []string
		unread    []string
		public    []string // jobs a public member can start
		withheld  int      // jobs whose policy the caller may not read
		selfCan   []string // of those, the jobs the caller can start
	)
	seen := map[string]bool{}
	page := ""
	for pages := 1; ; pages++ {
		resp, err := svc.Projects.Locations.Jobs.List(parent).PageSize(100).PageToken(page).Context(ctx).Do()
		if err != nil {
			return unchecked(err)
		}
		for _, j := range resp.Jobs {
			if j.Name == "" {
				continue
			}
			jobs = append(jobs, j.Name)
			p, err := svc.Projects.Locations.Jobs.GetIamPolicy(j.Name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
			if err != nil && isAccessDenied(err) {
				// A plain launcher's doctor: the fugaroLauncher role lists
				// jobs and does not read their policies. What the caller
				// can still learn is its own reach, as the bucket check
				// falls back to TestIamPermissions — both of fugaro run's
				// permissions, since a launch carries overrides.
				r, terr := svc.Projects.Locations.Jobs.TestIamPermissions(j.Name,
					&runapi.GoogleIamV1TestIamPermissionsRequest{Permissions: launchPermissions}).Context(ctx).Do()
				if terr != nil {
					unread = append(unread, shortJobName(j.Name))
					continue
				}
				withheld++
				if r != nil && len(r.Permissions) == len(launchPermissions) {
					selfCan = append(selfCan, shortJobName(j.Name))
				}
				continue
			}
			if err != nil {
				// One job's policy unreadable: the job is named in a warning
				// below rather than silently counted as a job no launcher can
				// start, which is what an unreadable policy would look like.
				unread = append(unread, shortJobName(j.Name))
				continue
			}
			n, can := launcherGrantsOn(p, launchers, lc.GCPProject)
			grants += n
			if can {
				startable = append(startable, shortJobName(j.Name))
			}
			if publicCanStart(p, lc.GCPProject) {
				public = append(public, shortJobName(j.Name))
			}
		}
		next := resp.NextPageToken
		if next == "" {
			break
		}
		// The SDK follows whatever next-page token the server answers with
		// and never notices one it has already followed, so a server that
		// repeats a token would have doctor walk this listing forever -- the
		// same two guards internal/managed's inventory read carries, for the
		// same reason. A walk past either bound is a read that failed, and
		// this check has one answer for a failed read (the warning above),
		// never a shorter list of jobs: a listing cut short would read as
		// jobs a launcher cannot start.
		if seen[next] {
			return unchecked(fmt.Errorf("the server repeated page token %q after %d pages of the location's jobs", oneLineCLI(next), pages))
		}
		if pages >= doctorMaxJobPages {
			return unchecked(fmt.Errorf("the listing of %s's jobs runs past %d pages", parent, doctorMaxJobPages))
		}
		seen[next] = true
		page = next
	}
	slices.Sort(startable)
	slices.Sort(selfCan)
	read := len(jobs) - withheld - len(unread)
	// Every count below is over the policies this doctor could read: a job
	// whose policy the caller may not read (withheld) or whose read failed
	// (unread) is in none of them. What that leaves out is said beside them
	// below, so a count over a partial read never reads as a count over the
	// location — in either direction: a job nobody read is neither one whose
	// grants were counted nor one no launcher can start.
	problem := fmt.Sprintf("launcher grants on Cloud Run jobs: %d grant(s) over %d job(s), and a launcher can start %d of them",
		grants, read, len(startable))
	switch {
	case withheld > 0 && read == 0:
		// Every policy was refused: that is the ordinary launcher's view
		// (fugaroLauncher holds run.jobs.list and not run.jobs.getIamPolicy),
		// not a fault. Nothing was read, so there is no count to give: only
		// the caller's own reach, from testIamPermissions.
		problem = fmt.Sprintf("your credentials may not read the Cloud Run jobs' IAM policies (the launchers' grants are an operator's check); of %d job(s), you can start %d of them", withheld, len(selfCan))
		if len(selfCan) > 0 {
			problem += ": " + strings.Join(selfCan, ", ")
		}
		if len(unread) > 0 {
			problem += fmt.Sprintf(" (%d job(s) could not be asked even that: %s)", len(unread), strings.Join(unread, ", "))
		}
	case len(jobs) > 0 && read == 0:
		// Not one policy was read, and not by refusal either: a read that
		// failed leaves no grant count and no reach of the caller's own, and
		// "no launcher is granted on any job" would be a claim about policies
		// nobody read — beside the launcher-jobs-unread warning that names
		// those jobs and says the answer for them is unknown, not no.
		problem = fmt.Sprintf("could not read the IAM policy of %d of the location's %d Cloud Run job(s): this line has no grant count to give, and launcher-jobs-unread names them", len(unread), len(jobs))
	case len(launchers) == 0:
		// Nothing to compare the policies' members against: a zero here is
		// the local config's, not the jobs'.
		problem += " (the local config lists no launcher and no operator, so no grant could be attributed to one; run doctor with the operator's config to see the launchers' reach)"
	case len(startable) > 0:
		problem += ": " + strings.Join(startable, ", ")
	case grants > 0:
		// A launcher IS named on a job's policy, on a role that cannot start
		// one (roles/run.viewer, say — counted as a grant and never as a job a
		// launcher can start). Saying "no launcher is granted on any job" here
		// would be false about the very lines this one is read beside.
		problem += fmt.Sprintf(" (the %d grant(s) are on roles that cannot start a job, so fugaro run cannot start one of them)", grants)
	default:
		problem += " (no launcher or operator is named in the policy of any job whose policy could be read, so fugaro run cannot start one of them)"
	}
	// What the counts above leave out, and — for the jobs the caller was
	// refused a policy for but could still ask about — its own reach over
	// them. A job whose read failed is in neither half: not even its own
	// permissions could be asked.
	if left := withheld + len(unread); left > 0 && read > 0 {
		var which []string
		if withheld > 0 {
			s := fmt.Sprintf("%d whose policy your credentials may not read", withheld)
			if len(selfCan) > 0 {
				s += ", of which you can start " + strings.Join(selfCan, ", ")
			}
			which = append(which, s)
		}
		if len(unread) > 0 {
			which = append(which, fmt.Sprintf("%d whose read failed", len(unread)))
		}
		problem += fmt.Sprintf(" (%d of the location's %d job(s) are not in that count: %s)",
			left, len(jobs), strings.Join(which, " and "))
	}
	// Not OK, and Severity "info": printDoctorChecks prints every check that is
	// not OK (and an OK one only when it carries a Severity), and an "info" one
	// neither fails doctor, with or without --strict (checksFail), nor
	// suppresses the "everything checked is ok" line beside it. Being un-ok is
	// what makes the print independent of the printer's OK rule as well: a line
	// the owner must be able to read is the shape that prints either way.
	//
	// Any withheld policy sets the unread marker, not only a listing where every
	// one was: the counts above are over the policies this doctor read, so a
	// partial read is a partial answer — "2 grants over 1 job" says nothing about
	// the job whose policy was refused, and a measurement nobody hears about is
	// how a grant that was never read passes for a count that is fine.
	out := []doctorCheck{{ID: "launcher-jobs", Severity: "info", unread: withheld > 0 || len(unread) > 0, Problem: problem,
		Fix: "the launcher role's permissions are unchanged by the project-centric model (run.jobs.run, run.jobs.runWithOverrides and the secret metadata list): what changes is how many jobs they attach to, so a grant count that falls while runs still work is a migration doing what it says, and a job a launcher is not granted on is a job fugaro run cannot start. Project-level run roles bypass the jobs' own policies and are not counted here"}}
	if len(public) > 0 {
		slices.Sort(public)
		out = append(out, doctorCheck{ID: "launcher-jobs-public", Severity: "warning",
			Problem: fmt.Sprintf("%d Cloud Run job(s) can be started by allUsers or allAuthenticatedUsers: ", len(public)) + strings.Join(public, ", ") +
				": anyone the member names can launch the job, and with run.jobs.runWithOverrides set its environment",
			Fix: "remove the public member from each job's IAM policy (gcloud run jobs remove-iam-policy-binding JOB --region " + quoteWord(lc.Region) + " --member allUsers --role ROLE); Fugaro grants only the launchers listed in its config"})
	}
	if len(unread) > 0 {
		out = append(out, doctorCheck{ID: "launcher-jobs-unread", Severity: "warning",
			Problem: "could not read the IAM policy of " + fmt.Sprintf("%d Cloud Run job(s): ", len(unread)) + strings.Join(unread, ", ") +
				": whether a launcher can start them is unknown, not no",
			Fix: "rerun fugaro doctor with access to the project, or ask an operator who can read each job (gcloud run jobs get-iam-policy JOB --region " + quoteWord(lc.Region) + ")"})
	}
	return out
}

// launchPermissions are what fugaro run needs on a job: it always launches
// with overrides (the run's FUGARO_RUN), so run.jobs.run alone does not start
// a Fugaro run.
var launchPermissions = []string{"run.jobs.run", "run.jobs.runWithOverrides"}

// launcherMembers is the members a job's policies are compared against: the
// local config's launchers AND its operators, each once. The installation
// grants the job runner role to both — internal/infra's launchersWithOperators
// is what the roots render, "operators get everything launchers get" (they
// run, watch and cancel runs too) — so a binding naming an operator is a grant
// the installation made, and a job only an operator can start is still a job
// Fugaro can start. Comparing against lc.Terraform.Launchers alone read every
// operator-only binding as no grant at all.
func launcherMembers(launchers, operators []string) []string {
	out := slices.Concat(launchers, operators)
	slices.Sort(out)
	return slices.Compact(out)
}

// launcherGrantsOn is the (job, launcher) grants job's own policy carries and
// whether a launcher can start it. Members are compared as IAM spells them
// (the local config's launcher list is already `user:…`/`group:…`), groups
// are not expanded, and only a role that can actually start the job makes it
// one a launcher can start: a launcher who can read a job but not run it is
// a grant doctor sees and does not misreport.
func launcherGrantsOn(p *runapi.GoogleIamV1Policy, launchers []string, project string) (n int, startable bool) {
	if p == nil {
		return 0, false
	}
	for _, b := range p.Bindings {
		if b == nil {
			continue
		}
		for _, m := range b.Members {
			if !slices.Contains(launchers, m) {
				continue
			}
			n++
			if launcherRoleStartsJobs(b.Role, project) {
				startable = true
			}
		}
	}
	return n, startable
}

// launcherRoleStartsJobs reports whether role can start a Cloud Run job in
// project. It recognizes the installation's own job runner role — the one
// `deploy/terraform/gcp/modules/project/iam.tf` grants each job to each
// launcher, whose permissions are run.jobs.run and run.jobs.runWithOverrides
// — and the built-in project roles that carry them. A custom role of another
// installation, or one the operator created with another id, is not
// recognized: it is still counted as a grant, and not claimed as a job a
// launcher can start, since nothing here may expand a role's permissions.
func launcherRoleStartsJobs(role, project string) bool {
	switch role {
	case "roles/owner", "roles/editor", "roles/run.admin", "roles/run.developer", "roles/run.jobsExecutorWithOverrides":
		return true
	}
	return role == "projects/"+project+"/roles/"+infra.RoleJobRunner
}

// publicCanStart reports whether p lets allUsers or allAuthenticatedUsers
// start the job: a binding of a start role (launcherRoleStartsJobs) whose
// members include either. A conditional binding is still reported, since
// nothing here evaluates a condition and a public member under any condition
// is not one Fugaro ever grants.
func publicCanStart(p *runapi.GoogleIamV1Policy, project string) bool {
	if p == nil {
		return false
	}
	for _, b := range p.Bindings {
		if b == nil || !launcherRoleStartsJobs(b.Role, project) {
			continue
		}
		if slices.Contains(b.Members, "allUsers") || slices.Contains(b.Members, "allAuthenticatedUsers") {
			return true
		}
	}
	return false
}

// shortJobName is a job's resource name as the last segment: what a person
// types, and what a message should carry.
func shortJobName(name string) string {
	return pluginwire.Printable(name[strings.LastIndex(name, "/")+1:])
}
