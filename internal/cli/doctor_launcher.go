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
// line, and only a read that fails is a warning.
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
	launchers := lc.Terraform.Launchers
	parent := fmt.Sprintf("projects/%s/locations/%s", lc.GCPProject, lc.Region)
	var (
		jobs      []string // every job in the location, by name
		grants    int      // (job, launcher) bindings
		startable []string
		unread    []string
	)
	page := ""
	for {
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
		}
		page = resp.NextPageToken
		if page == "" {
			break
		}
	}
	slices.Sort(startable)
	problem := fmt.Sprintf("launcher grants on Cloud Run jobs: %d grant(s) over %d job(s), and a launcher can start %d of them",
		grants, len(jobs), len(startable))
	if len(startable) > 0 {
		problem += ": " + strings.Join(startable, ", ")
	} else {
		problem += " (no launcher is granted on any job, so fugaro run cannot start one)"
	}
	out := []doctorCheck{{ID: "launcher-jobs", OK: true, Severity: "info", Problem: problem,
		Fix: "the launcher role's permissions are unchanged by the project-centric model (run.jobs.run, run.jobs.runWithOverrides and the secret metadata list): what changes is how many jobs they attach to, so a grant count that falls while runs still work is a migration doing what it says, and a job a launcher is not granted on is a job fugaro run cannot start. Project-level run roles bypass the jobs' own policies and are not counted here"}}
	if len(unread) > 0 {
		out = append(out, doctorCheck{ID: "launcher-jobs-unread", Severity: "warning",
			Problem: "could not read the IAM policy of " + fmt.Sprintf("%d Cloud Run job(s): ", len(unread)) + strings.Join(unread, ", ") +
				": whether a launcher can start them is unknown, not no",
			Fix: "rerun fugaro doctor with access to the project, or ask an operator who can read each job (gcloud run jobs get-iam-policy JOB --region " + quoteWord(lc.Region) + ")"})
	}
	return out
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
	case "roles/owner", "roles/editor", "roles/run.admin", "roles/run.developer":
		return true
	}
	return role == "projects/"+project+"/roles/"+infra.RoleJobRunner
}

// shortJobName is a job's resource name as the last segment: what a person
// types, and what a message should carry.
func shortJobName(name string) string {
	return pluginwire.Printable(name[strings.LastIndex(name, "/")+1:])
}
