package cli

import (
	"context"
	"fmt"
	"strings"

	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
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
// the job sets itself that a run does not pass to its agent (docs/image-env,
// reconciliation D1377). It is the report half of the change that an image's
// ENV reaching a run is, and it exists because the drop was silent: no command
// read a job's environment, so an operator learned about it from a failed run
// and a transcript search (F13, F14).
//
// The subtraction is runner.ImageEnvDroppedNames — the same function the
// passthrough computes the variables it hands the agent with (D1378), so a name
// this report calls dropped is a name the passthrough dropped — over the job's
// own environment minus the names Fugaro's own deployment writes into it
// (doctorFugaroWritten). What is left was set on the job by someone else, and
// those are the names an operator has to be told about; the deployment's own
// are the run's configuration, and calling them dropped would say a job that
// works reaches no run.
//
// The one thing no read here can see is an image's own ENV: Cloud Run keeps it
// in the image, and Jobs.Get carries the job's template alone, so nothing in a
// job's environment tells an image's variable from an operator's. The line says
// so rather than leaving "drops none" to read as "your image's variables all
// reach runs", and the line is a lower bound in the other direction too: every
// name it prints will not reach a run.
//
// Names only, never a value (D1375). A job with no stamp — deployed before the
// stamp, or a repository-built image — is reported as not catalog-stamped
// rather than as a list: a run of it passes none of its image's variables
// whatever they are (D1371's fail-closed), which is a different fact from
// "drops nothing".
//
// Only the jobs a run lands on are reported (doctorTaskJob): the workflow jobs
// and the project jobs. The installation's own support jobs — the reaper, the
// coordinator, the history and catalog checks, a repository's image check —
// and whatever else lives in the project run no task, so no image's variable
// could reach a run through them; reporting them would print a wrong
// "not catalog-stamped" line for each of them on every installation.
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
	jobs, err := doctorListedJobs(ctx, svc, doctorJobsParent(lc))
	if err != nil {
		return doctorImageEnvUnread(err)
	}
	var out []doctorCheck
	var unread []string
	for _, lj := range jobs {
		// A job no run lands on is not reported at all: it has no task for an
		// image's variables to reach, so the only line it could carry is the
		// false "not catalog-stamped" one.
		if !doctorTaskJob(lj.Labels) {
			continue
		}
		// One Jobs.Get per job: the listing carries no container, and this is
		// the read doctor did not make before (F14). The launcher role already
		// holds run.jobs.get, so the report needs no new grant.
		j, err := svc.Projects.Locations.Jobs.Get(lj.Name).Context(ctx).Do()
		if err != nil {
			unread = append(unread, shortJobName(lj.Name))
			continue
		}
		out = append(out, doctorImageEnvCheck(shortJobName(lj.Name), j))
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
		c.Problem = "the job's image is not catalog-stamped — no project image_catalog entry chose it (a repository-built image, a job deployed before the stamp, or a project job, which runs the installation's base image) — so a run of it passes none of its image's own variables"
		return c
	}
	// declared is the job's own FUGARO_SECRET_ENVS: no run is in hand here, and a
	// name the job itself declares reaches runs, so it is not one this line calls
	// dropped (ImageEnvDroppedNames).
	dropped := runner.ImageEnvDroppedNames(doctorJobForeignEnv(env), runner.SecretEnvNames(env))
	c.Problem = "the job's image is catalog entry " + pluginwire.Printable(entry) + ", and a run drops none of the names the job sets itself — the rest of its environment is Fugaro's own, which configures the run" + doctorImageEnvCaveat
	if len(dropped) > 0 {
		c.Problem = "the job's image is catalog entry " + pluginwire.Printable(entry) + fmt.Sprintf(", and a run drops %d name(s) the job sets itself: ", len(dropped)) +
			strings.Join(dropped, ", ") + " — Fugaro reserves them, so nothing an image sets under them can shadow the routing, the credentials or a declared secret" + doctorImageEnvCaveat
	}
	return c
}

// doctorImageEnvCaveat is the limit every stamped job's line states, in the
// words the reader needs: what an image declares is in the image, and no read
// of a job can list it. Without it, "a run drops none of the names the job sets
// itself" reads as "everything your image declares reaches a run", which is a
// claim this line cannot make either way.
const doctorImageEnvCaveat = " (an image's own ENV lives in the image and not in the job, so what that image declares is not in this line)"

// doctorTaskJob reports whether a job is one a run lands on, which is what this
// report is about: a workflow job (one per repository and workflow, labelled
// LabelWorkflow by the workflow module) or a project job (one per profile,
// provider and account, labelled LabelProfile by the project module — the job
// a clone workflow runs on).
//
// Every other job of the location runs no task, so no variable of an image
// could reach a run through it: Fugaro's own reaper, coordinator, history and
// catalog-check jobs and a repository's image check (each wearing LabelRole),
// and any job of somebody else's that shares the project. A line for one of
// them would be the false "not catalog-stamped" sentence — about six wrong
// measurements on every installation — and for a foreign job it would be a
// claim about a job Fugaro does not deploy.
func doctorTaskJob(labels map[string]string) bool {
	return labels[gcp.LabelWorkflow] != "" || labels[gcp.LabelProfile] != ""
}

// doctorFugaroEnvPrefix is the family every name Fugaro's own deployment writes
// into a job belongs to, the Vertex pair below aside. A test pins it to
// config.ReservedEnvPrefixes, which carries it as the reservation that makes
// the whole family unreachable for an image.
const doctorFugaroEnvPrefix = "FUGARO_"

// doctorFugaroWritten reports whether name is one internal/infra's spec writes
// into a job's environment: the FUGARO_ family (the platform's five, the stamp,
// the checkout and remote markers, the budget, provider and prices variables)
// and the two Vertex variables a vertex-auth job carries. Those two are the
// whole reason this is more than a prefix test, and the report would otherwise
// call them dropped on every vertex installation.
//
// The launch and platform names are not here and cannot be: the launcher writes
// FUGARO_RUN as an override at run time and Cloud Run writes its own names into
// the container, so neither is in the job's template for this read to see.
func doctorFugaroWritten(name string) bool {
	u := strings.ToUpper(name)
	return strings.HasPrefix(u, doctorFugaroEnvPrefix) || u == infra.VertexRegionEnv || u == infra.VertexProjectEnv
}

// doctorJobForeignEnv is a job's own environment minus the names the deployment
// wrote into it: what is left was set on the job by someone else. The
// subtraction the report makes runs over this, so a name Fugaro's own
// deployment wrote is never reported as one a run drops.
func doctorJobForeignEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for name, value := range env {
		if !doctorFugaroWritten(name) {
			out[name] = value
		}
	}
	return out
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
