package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorJobMountID is the ID of the line a job that mounts a workflow secret
// gets, and doctorJobMountUnreadID the one a job whose template could not be
// read is carried by: the second is a warning, because "could not be read" is
// not the same fact as "mounts none" (the rule doctorMeasureIDs states).
const (
	doctorJobMountID       = "workflow-secret-mount"
	doctorJobMountUnreadID = "workflow-secret-mount-unread"
)

// doctorJobMountChecks is doctor's "workflow-secret-mount" report (IS10,
// D3331): every job a run lands on is read, and each one whose template mounts
// a secret under a variable that is not one Fugaro reserves for itself is
// listed by the job and the variable name — never a value.
//
// The variable name is what tells the classes apart, and it is the only thing
// that can, from a deployed template: every mount of another class is a
// variable the platform reserves (config.ReservedEnv). The git credential is
// FUGARO_BITBUCKET_TOKEN or FUGARO_GITHUB_APP_PRIVATE_KEY, a model credential
// is CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY, and a provider's key is
// FUGARO_PROVIDER_KEY_<NAME> — the FUGARO_, CLAUDE_CODE_ and ANTHROPIC_
// families the reservation holds. A workflow secret's variable may not be one
// of them (config.Validate refuses one, and the job spec's own mount collision
// guard refuses it too), so a secret-backed variable outside the reserved
// family is exactly T3's class: the one IS6's flip takes away from every run
// job, and the one this report is the live reading of. Nothing is read but the
// container's env entries and the secret reference's own name, which the API
// answers with no value at all.
//
// It is a measurement, not a finding: before the flip a vertex repository (G7,
// D3326 — its model mounts stay, and until IS9 it is served nothing) and a
// repository the flip does not serve keep their workflow-secret mounts by
// design, so a line here is the list of what the flip has left to take and not
// something the reader has to repair. It prints under --all and --json, is
// counted by no view, and fails nothing, --strict included. A job with no such
// mount gets no line at all: the report is of what there is to see.
//
// It reads the same listing doctor's image-env report reads (doctorListedJobs)
// and the same per-job get (run.jobs.get, a permission the launcher role
// already holds), and it is deliberately the whole location's jobs filtered by
// doctorTaskJob rather than the jobs a local config computes: a job outlives
// the fugaro.yaml entry that made it, and the point of the line is to show what
// is actually deployed.
func doctorJobMountChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		// Fakes only, and no fake for Cloud Run: an empty endpoint would send
		// the call to Google. The same guard the other job reads behind doctor
		// make.
		return nil
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return doctorJobMountUnread(err)
	}
	jobs, err := doctorListedJobs(ctx, svc, doctorJobsParent(lc))
	if err != nil {
		return doctorJobMountUnread(err)
	}
	var out []doctorCheck
	var unread []string
	for _, lj := range jobs {
		if !doctorTaskJob(lj.Labels) {
			continue
		}
		j, err := svc.Projects.Locations.Jobs.Get(lj.Name).Context(ctx).Do()
		if err != nil {
			unread = append(unread, shortJobName(lj.Name))
			continue
		}
		if c := doctorJobMountCheck(shortJobName(lj.Name), j); c != nil {
			out = append(out, *c)
		}
	}
	if len(unread) > 0 {
		out = append(out, doctorCheck{ID: doctorJobMountUnreadID, Severity: "warning",
			Problem: fmt.Sprintf("could not read the template of %d Cloud Run job(s), so whether they mount a workflow secret is unknown, not none: ", len(unread)) + strings.Join(unread, ", "),
			Fix:     "rerun fugaro doctor with access to the project, or ask an operator who can read each job (gcloud run jobs describe JOB --region " + quoteWord(lc.Region) + ")"})
	}
	return out
}

// doctorJobMountCheck is one job's line: the workflow-secret variables its
// template mounts, in order, or nil when it mounts none.
//
// The variables are named and never the containers they mount: this report says
// which job still reads a workflow secret, which is the fact the flip changes,
// and a container ID tells the reader nothing it can act on. Nothing here
// prints a value — a value_source entry is the API's whole answer, and it holds
// no value.
func doctorJobMountCheck(job string, j *runapi.GoogleCloudRunV2Job) *doctorCheck {
	mounts := doctorWorkflowSecretMounts(j)
	if len(mounts) == 0 {
		return nil
	}
	// OK with a Severity is what files a check as a measurement
	// (doctorClassOf): before the flip there is nothing wrong here, it is what
	// doctor measured.
	return &doctorCheck{ID: doctorJobMountID, Severity: "info", OK: true,
		Problem: fmt.Sprintf("job %s mounts %s: the template names a secret container for each of those variables, and none of them is a variable Fugaro reserves for the git, the model or a provider credential, so they are the job's workflow secrets (T3's class)",
			job, strings.Join(mounts, ", ")),
		Fix: "nothing to run for this: it is the reading of what a deployed job still mounts. A vertex repository and one the flip does not serve keep these mounts by design; once the flip has taken them away, a job still carrying one was applied before it, and re-applying its repository (" + selfCommand() + " update --repo OWNER/NAME, in that repository's checkout) deploys its jobs without them"}
}

// doctorWorkflowSecretMounts is the variables j mounts a secret as that are not
// one of the reserved classes, sorted and deduplicated. It is the class read
// the header describes, over one job's containers.
func doctorWorkflowSecretMounts(j *runapi.GoogleCloudRunV2Job) []string {
	var out []string
	if j == nil || j.Template == nil || j.Template.Template == nil {
		return nil
	}
	for _, c := range j.Template.Template.Containers {
		if c == nil {
			continue
		}
		for _, e := range c.Env {
			if e == nil || e.ValueSource == nil || e.ValueSource.SecretKeyRef == nil {
				continue
			}
			if config.ReservedEnv(e.Name) {
				continue
			}
			out = append(out, pluginwire.Printable(e.Name))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// doctorJobMountUnread is the report a read that did not answer leaves: whether
// a job mounts a workflow secret is then unknown, which is not the same fact as
// none.
func doctorJobMountUnread(err error) []doctorCheck {
	return []doctorCheck{{ID: doctorJobMountUnreadID, Severity: "warning",
		Problem: "could not read the Cloud Run jobs' templates to list the workflow secrets they mount: " + oneLineCLI(err.Error()),
		Fix:     "rerun fugaro doctor with access to the project, or as an operator"}}
}
