package cli

import (
	"context"
	"fmt"
	"strings"

	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runner"
)

// doctorImageEnvUnreadID is the one check a report doctor could not make is
// carried by. The per-job report is a measurement, and the rule measurements
// answer to is that a read which FAILED is not one: an unknown filed as a
// measurement prints beside "everything checked is ok" as if it had been read
// (doctorMeasureIDs). This one is a warning instead, so a job whose environment
// doctor could not read is a line the reader sees.
const doctorImageEnvUnreadID = "image-env-unread"

// doctorImageEnvChecks is option 3 of issue #1755: per Cloud Run job, the names
// in that job's own environment that reach no agent (docs/image-env,
// reconciliation D1377). It is the report half of the change that an image's
// ENV reaching a run is, and it exists because the drop was silent: no command
// read a job's environment, so an operator learned about it from a failed run
// and a transcript search (F13, F14).
//
// The subtraction is runner.ImageEnvDroppedNames — the same function the
// passthrough computes the variables it hands the agent with (D1378), so a name
// this report calls dropped is a name the passthrough dropped. What it names is
// the job's own environment, which is all Jobs.Get carries: the variables an
// image declares for itself are in no API doctor (or the deploy) can read, so
// the line is a lower bound — every name it prints will not reach a run, and a
// name it does not print either passes or was never visible here.
//
// Names only, never a value (D1375). A job with no stamp — deployed before the
// stamp, or a repository-built image — is reported as not catalog-stamped
// rather than as a list: a run of it passes none of its image's variables
// whatever they are (D1371's fail-closed), which is a different fact from
// "drops nothing".
//
// The default view never prints any of this: the report is a measurement, and
// --all (and --json) is where measurements print (F13).
func doctorImageEnvChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return nil
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return doctorImageEnvUnread(err)
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", lc.GCPProject, lc.Region)
	jobs, err := doctorJobNames(ctx, svc, parent)
	if err != nil {
		return doctorImageEnvUnread(err)
	}
	var out []doctorCheck
	var unread []string
	for _, name := range jobs {
		// One Jobs.Get per job: the listing carries no container, and this is
		// the read doctor did not make before (F14). The launcher role already
		// holds run.jobs.get, so the report needs no new grant.
		j, err := svc.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
		if err != nil {
			unread = append(unread, shortJobName(name))
			continue
		}
		out = append(out, doctorImageEnvCheck(shortJobName(name), j))
	}
	if len(unread) > 0 {
		out = append(out, doctorCheck{ID: doctorImageEnvUnreadID, Severity: "warning",
			Problem: fmt.Sprintf("could not read the environment of %d Cloud Run job(s), so which of their variables a run drops is unknown, not none: ", len(unread)) + strings.Join(unread, ", "),
			Fix:     "rerun fugaro doctor with access to the project, or ask an operator who can read each job (gcloud run jobs describe JOB --region " + quoteWord(lc.Region) + ")"})
	}
	return out
}

// doctorImageEnvCheck is one job's line: the catalog entry its image came
// from, and the names of its own environment that a run drops.
func doctorImageEnvCheck(job string, j *runapi.GoogleCloudRunV2Job) doctorCheck {
	env := doctorJobEnv(j)
	// The stamp is written on EVERY workflow job (internal/infra's
	// repoCtx.workflow), empty where the catalog chose no image, and the runner
	// reads an empty one as no entry at all (D1371). Empty is therefore not "no
	// variables": it is "the passthrough is off for this job".
	entry := env[runner.ImageEntryEnv]
	// OK with a Severity is what files a check as a measurement (doctorClassOf):
	// nothing here is wrong, it is what doctor measured (F13).
	c := doctorCheck{ID: "image-env-" + job, Severity: "info", OK: true}
	if entry == "" {
		c.Problem = "the job's image is not catalog-stamped — no project image_catalog entry chose it (a repository-built image, or a job deployed before the stamp) — so a run of it passes none of its image's own variables"
		return c
	}
	// declared is the job's own FUGARO_SECRET_ENVS: no run is in hand here, and a
	// name the job itself declares reaches runs, so it is not one this line calls
	// dropped (ImageEnvDroppedNames).
	dropped := runner.ImageEnvDroppedNames(env, runner.SecretEnvNames(env))
	c.Problem = "the job's image is catalog entry " + pluginwire.Printable(entry) + ", and a run drops none of the names the job's environment carries"
	if len(dropped) > 0 {
		c.Problem = "the job's image is catalog entry " + pluginwire.Printable(entry) + fmt.Sprintf(", and a run drops %d name(s) the job's environment carries: ", len(dropped)) +
			strings.Join(dropped, ", ") + " — Fugaro reserves them, so nothing an image sets under them can shadow the routing, the credentials or a declared secret"
	}
	return c
}

// doctorJobEnv is a job's own container environment as Jobs.Get reports it:
// the same read internal/backend/gcp's JobFullEnv makes for the converge, with
// the same rule for secret-backed entries — their value is a reference the API
// answers empty, and the entry's own name is what this report is about. Every
// value stays in here: the report prints names only (D1375).
func doctorJobEnv(j *runapi.GoogleCloudRunV2Job) map[string]string {
	env := map[string]string{}
	if j == nil || j.Template == nil || j.Template.Template == nil {
		return env
	}
	for _, c := range j.Template.Template.Containers {
		if c == nil {
			continue
		}
		for _, e := range c.Env {
			if e == nil || e.ValueSource != nil {
				continue
			}
			env[e.Name] = e.Value
		}
	}
	return env
}

// doctorImageEnvUnread is the report a read that did not answer leaves: the
// names a run drops are then unknown, which is not the same fact as none.
func doctorImageEnvUnread(err error) []doctorCheck {
	return []doctorCheck{{ID: doctorImageEnvUnreadID, Severity: "warning",
		Problem: "could not read the Cloud Run jobs' environments to see which of their variables a run drops: " + oneLineCLI(err.Error()),
		Fix:     "rerun fugaro doctor with access to the project, or as an operator"}}
}
