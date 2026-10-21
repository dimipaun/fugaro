package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

const (
	// envReaperJob is the marker that says a `fugaro reaper` is running where
	// it is meant to: inside the Cloud Run job the compute module creates, with
	// no shell and no operator. reaper.tf sets it, and nothing else does --
	// deliberately, because the two variables that name the pass's project and
	// region (envGCPProject, envRegion) are the HISTORY job's, and any process
	// with those two in its environment would otherwise reach the environment
	// path and sweep a project for real from a shell. Requiring a variable only
	// this job sets is what tells the two apart: the job is the only thing an
	// automated, deleting pass may be.
	envReaperJob = "FUGARO_REAPER_JOB"
	// reaperJobMarker is the value envReaperJob must have. It is a marker and
	// not a credential: a marker is checked for the value the job sets and
	// nothing else, and "1" is what Terraform sets. An empty variable is not
	// the marker (an environment a shell happened to export empty is not a
	// reaper job), and neither is any other value.
	reaperJobMarker = "1"
)

// newReaperCmd is `fugaro reaper`: one pass of internal/backend/batch's
// Reaper, the component that deletes the Batch jobs and Compute Engine
// instances a run has leaked (design compute-backend.md §3.4, §8.1 Task 11).
//
// It is the library's one production caller: the reaper is a scheduled sweep
// of an installation's own project, run by the Cloud Run job the compute
// module's reaper.tf creates and started by its Scheduler job every
// ReapMargin, and it exists as a command so that the thing a schedule runs is
// the thing an operator can run by hand -- with --dry-run, which is what an
// operator reaches for first: the report names every job and every VM the
// pass would delete and why, and deletes nothing.
//
// Two properties are the command's own, not the library's:
//
//   - It refuses to run at all where the project has no Batch configuration.
//     A sweep deletes things, and the deadlines it deletes them by are the
//     installation's own task timeouts -- an installation that never
//     provisioned Batch has no such timeouts and, more to the point, no Batch
//     jobs of its own to sweep. A sweeper that guesses what it is looking at
//     is worse than one that does nothing. What counts as a Batch
//     configuration is either of the two sources below: a project config's
//     batch: block, or the reaper job's own environment, which only the
//     installation's compute module provisions.
//   - A pass that errored exits non-zero, with the error, even though the
//     part it did finish is still printed: a report that stops early is not a
//     clean project (Reaper.Reap says so).
//
// Where the pass's configuration comes from is the one thing this command has
// that others do not: the Cloud Run job runs `fugaro reaper` with no config
// file, no checkout and no project name, so the job's own environment (the
// project and the region Terraform sets on it, reaper.tf) stands in for the
// project config, exactly as it does for the history job (history.go). A
// command run by hand selects a project config as every cloud command does,
// and the environment is reached for only where nothing named a config at all
// (reaperPass).
//
// Nothing here makes `backend: cloud-batch` selectable. The reaper's project
// config carries a batch: block, which a config may carry while its own
// `backend:` still names Cloud Run -- Task 8's allowlist said so -- and
// internal/localcfg refuses `backend: cloud-batch` until the task that turns
// it on. The reaper never opens the seam's backend at all: it reads the
// installation's facts out of the config and talks to the Batch and Compute
// APIs itself.
func newReaperCmd() *cobra.Command {
	var o cloudOptions
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reaper",
		Short: "Sweep the Batch jobs and VMs a run has leaked",
		Long: "One pass of the reaper (docs/design/compute-backend.md §3.4): delete the Batch jobs\n" +
			"that are stuck or queued past their deadline, and the Compute Engine instances of runs\n" +
			"no Batch job accounts for, in this project and region.\n\n" +
			"The reaper holds five permissions and creates nothing (SECURITY.md). Every deletion\n" +
			"rests on a label check of its own, and anything it cannot explain it refuses to touch --\n" +
			"naming it in the report, which is what this command prints and what its own Cloud Run\n" +
			"job logs.\n\n" +
			"Run it as the installation's reaper account. --dry-run prints the same report and\n" +
			"deletes nothing.\n\n" +
			"Where no project config is selected the pass reads the reaper job's own environment\n" +
			"(FUGARO_GCP_PROJECT and FUGARO_REGION, which Terraform sets on the job), and deletes\n" +
			"only inside the Cloud Run job that also sets FUGARO_REAPER_JOB: anywhere else that\n" +
			"environment is a dry run, so an operator's shell never sweeps their project.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReaper(cmd.Context(), cmd, o, dryRun)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be deleted, and delete nothing")
	return cmd
}

// runReaper is `fugaro reaper`: resolve what the pass runs with, open the
// reaper against it and print the one pass's report.
func runReaper(ctx context.Context, cmd *cobra.Command, o cloudOptions, dryRun bool) error {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	opts, note, err := reaperPass(ctx, o, os.Getenv, dryRun)
	if err != nil {
		return err
	}
	if note != "" {
		// The note is what tells an operator who ran this by hand that the
		// pass they are watching is a dry run: it is the difference between a
		// report and a deletion, so it goes to stderr before the report.
		fmt.Fprintln(cmd.ErrOrStderr(), note)
	}
	r, err := batch.NewReaper(ctx, opts)
	if err != nil {
		// Everything NewReaper refuses is a fact of the selected config --
		// the project, the region, the deadlines derived from its task
		// timeouts -- or the credentials to reach Google with; both are the
		// caller's side of the call, not a failure of the API it then reads.
		return userErr("%v", err)
	}
	rep, err := r.Reap(ctx)
	// The report is printed whether or not the pass finished (Reap returns
	// what it did before an error), and the error follows it: a caller that
	// reads only the exit code still gets the lines in the job's log.
	// The report is the audit trail -- the Cloud Run job's stdout is the only
	// place it lives -- and a refusal's reason is the whole of what a reader
	// needs from it, so the lines are printed WHOLE: oneLine escapes what must
	// not reach a terminal (control and bidi characters, a newline) without
	// bounding the length, which is what the sibling scheduled command does
	// (history.go) and what internal/pluginwire's Printable does not -- its
	// 200-rune cap cuts the tail off the longest lines, and the tail is the
	// reason. A line this command prints is the engine's own sentence about
	// this installation's project, region and job id; it is bounded by what
	// those are, not by a wire format.
	for _, line := range rep.Lines() {
		if _, werr := fmt.Fprintln(cmd.OutOrStdout(), oneLine(line)); werr != nil {
			return werr
		}
	}
	if err != nil {
		return remote(err)
	}
	return nil
}

// reaperPass is what the one pass runs with: the selected project config, or
// -- where nothing was named that could select one -- the reaper job's own
// environment. The second return value is a note for the operator ("" when
// there is none), which today is the environment path's "this is a dry run".
//
// The order is the whole of it. A config that selects and carries a batch:
// block is the pass's configuration, which is `fugaro reaper --dry-run` on an
// operator's machine. A config that selects and carries no batch: block is
// refused: that installation has no Batch jobs for a reaper to judge, and its
// deadlines would have to be invented. Only where the selection found and
// named nothing at all -- no --config, no --project, no FUGARO_CONFIG, no
// FUGARO_PROJECT, which is the Cloud Run job, with no file and no checkout --
// does the environment answer instead; a config someone did name, and that
// then failed to select or selected without a batch: block, is never quietly
// passed over. Where the environment cannot answer either, the selection's own
// error is the one returned: it is the one that says what an operator can do
// about it.
//
// --gcp-project and --region are refused here rather than ignored. They only
// ever modify a SELECTED config (selectProject's announce, through
// localcfg.Config.Override), so on the environment path they would name a
// project the pass then swept the environment's instead of -- the operator's
// flag dropped without a word, and a report about a project nobody asked
// about. Neither answer is "honour it": the environment path has no config to
// apply the flag to, and its own project and region are Terraform's. So the
// combination is refused, with the two ways out named.
func reaperPass(ctx context.Context, o cloudOptions, getenv func(string) string, dryRun bool) (batch.ReaperOptions, string, error) {
	_, lc, err := selectProject(ctx, o)
	if err == nil {
		if lc.Batch == nil {
			return batch.ReaperOptions{}, "", userErr("project %s has no Batch configuration (its project config has no batch: block), and the reaper only sweeps an installation that runs Batch: its queue deadline and run budget are derived from that installation's own task timeouts, so there is nothing here for it to judge", lc.Name)
		}
		return reaperOptions(lc, dryRun), "", nil
	}
	if o.config != "" || o.project != "" || getenv("FUGARO_CONFIG") != "" || getenv(envProject) != "" {
		return batch.ReaperOptions{}, "", err
	}
	if o.gcpProject != "" || o.region != "" {
		return batch.ReaperOptions{}, "", userErr("%s cannot be honoured here: nothing selected a project config to apply it to (--gcp-project and --region override the selected config's own gcp_project and region), and on the reaper's job-environment path the project and the region come from %s and %s, which Terraform sets on the Cloud Run job. Name a project config -- --config, FUGARO_CONFIG, a checkout's fugaro.yaml, or the one project config -- to sweep a project, or drop the flag and use --dry-run to see what this job's own environment would sweep", strings.Join(reaperFlagNames(o), " and "), envGCPProject, envRegion)
	}
	opts, forced, envErr := reaperEnvOptions(getenv, dryRun)
	if envErr != nil {
		return batch.ReaperOptions{}, "", err
	}
	if forced {
		return opts, fmt.Sprintf("fugaro: note: %s is not %s, so this is not the reaper's own job and the pass is a DRY RUN: it prints what it would delete and deletes nothing. The Cloud Run job sets %s=%s (the compute module's reaper.tf); add --dry-run to say so yourself.", envReaperJob, reaperJobMarker, envReaperJob, reaperJobMarker), nil
	}
	return opts, "", nil
}

// reaperFlagNames names the endpoint flags an operator actually passed, for
// the refusal above: "it is refused" says nothing about which flag to drop.
func reaperFlagNames(o cloudOptions) []string {
	var names []string
	if o.gcpProject != "" {
		names = append(names, "--gcp-project "+o.gcpProject)
	}
	if o.region != "" {
		names = append(names, "--region "+o.region)
	}
	return names
}

// reaperEnvOptions is the pass of a reaper that runs from its own job's
// environment, the way the history job runs (history.go): no project config
// file, no checkout, nothing but the environment Terraform gives the job
// (reaper.tf sets the compute module's own project and region) and the
// account the job runs as, which is the reaper account -- the credentials
// are the identity's, the same as the history job's.
//
// Everything else is derived, and the endpoints are the real APIs: the job
// reaches Cloud Batch and Compute Engine at their own addresses, which is
// what "" means to internal/backend/batch (the endpoint overrides exist for
// installations whose tests or emulators point elsewhere).
//
// It refuses when either variable is missing, and the caller then returns the
// selection's error rather than this one: an environment that names no project
// and no region is not a Batch configuration, and both messages would be true,
// but the selection's says what the operator can do.
//
// The project and the region are the HISTORY job's variables, and that is a
// problem for a pass that deletes: any shell with FUGARO_GCP_PROJECT and
// FUGARO_REGION exported -- an operator's own, following the history job's
// documentation -- would take this path and sweep a live project for real. So
// this path deletes only where the marker the Cloud Run job sets is present
// (envReaperJob): without it the pass is forced to a dry run, and says so. The
// second return value reports that the forcing happened, so the caller can
// tell the operator; --dry-run on the command line is never touched by any of
// this (a dry run stays a dry run, marker or not).
func reaperEnvOptions(getenv func(string) string, dryRun bool) (batch.ReaperOptions, bool, error) {
	gcpProject, region := getenv(envGCPProject), getenv(envRegion)
	if gcpProject == "" || region == "" {
		return batch.ReaperOptions{}, false, userErr("the reaper job needs %s and %s in its environment (Terraform sets both on the job), and there is no project config here to read them from", envGCPProject, envRegion)
	}
	forced := false
	if !dryRun && getenv(envReaperJob) != reaperJobMarker {
		dryRun, forced = true, true
	}
	w := reaperEnvWiring{}
	if reaperEnvTest != nil {
		w = *reaperEnvTest
	}
	return batch.ReaperOptions{
		GCPProject: gcpProject,
		Region:     region,
		Timeouts:   reaperEnvTimeouts{},
		DryRun:     dryRun,
		Endpoints: batch.Endpoints{
			Batch:   w.batch,
			Compute: reaperComputeRoot,
			NoAuth:  w.noAuth,
		},
	}, forced, nil
}

// reaperEnvTimeouts is the task-timeout source of a pass that runs from the
// reaper job's environment. That environment carries no timeouts -- they live
// in the installation's project config, which the job does not have -- so the
// bound is backend.MaxRunTime: a run's own ceiling, the widest total a launch
// may ask for (BackendOptions.MaxRunTime), and therefore the widest a live run
// can be.
//
// It can only be the safe direction. The reaper's queue deadline is
// max(longest task timeout, backend.MaxRunTime) plus the launcher's slack, so
// an installation whose own bound is the ceiling -- the usual case, and the
// one an installation with no batch.task_timeout has -- gets the same deadline
// from this source as from its config, and one whose bound is shorter is swept
// by a deadline no run can outlive. A configured bound can never be LONGER:
// localcfg refuses a batch.task_timeout above backend.MaxRunTime
// (validateBatch), because a run can never be given more than the ceiling
// (--total-timeout is capped there) and the two sources disagreeing about that
// is exactly the direction that ends a live run. A pass never deletes
// something a live run could still be using; it can only be less patient than
// the config would be.
type reaperEnvTimeouts struct{}

func (reaperEnvTimeouts) TaskTimeout(string, string) (time.Duration, error) {
	return backend.MaxRunTime, nil
}

func (reaperEnvTimeouts) LongestTaskTimeout() (time.Duration, error) {
	return backend.MaxRunTime, nil
}

// reaperEnvWiring points the job's-environment path at fakes; tests only, nil
// in production -- history.go's historyTest is the same shape. Unlike the
// project config path, this one has no config to carry an endpoint: the
// production job reaches Cloud Batch at its own address as the account it runs
// as, and the only thing a test changes here is where it reaches it. The
// environment path is the one the Cloud Run job takes, so it is the one a test
// most needs to drive end to end, against internal/gcpfake.
type reaperEnvWiring struct {
	batch  string // the Cloud Batch API root; "" is the API's own
	noAuth bool   // send no credentials
}

var reaperEnvTest *reaperEnvWiring

// reaperComputeRoot is the Compute Engine API root the reaper's instance pass
// reads and deletes through, unless the project config overrides it. The
// project config has no endpoint for it on purpose: localcfg.Endpoints says
// the reaper's Compute Engine root is not a CLI client's endpoint but the
// reaper job's own, so there is nothing an installation configures here. This
// is a variable because the reaper's instance pass is tested against
// internal/gcpfake's Compute fake, and because a test can point the two passes
// of one command invocation at two different fakes.
var reaperComputeRoot = "https://compute.googleapis.com/compute/v1/"

// reaperOptions is the ReaperOptions one pass runs with, from the selected
// project config: the installation's identity, its task timeouts (which is
// where both of the reaper's deadlines come from) and the endpoints its two
// passes read.
//
// It is a separate function from the command because the two halves are
// separately checked: this is a pure derivation a test can read without a
// command tree, and the command above is then only selection, printing and
// exit codes.
func reaperOptions(lc *localcfg.Config, dryRun bool) batch.ReaperOptions {
	return batch.ReaperOptions{
		GCPProject: lc.GCPProject,
		Region:     lc.Region,
		// The same source the launched runs' timeouts come from
		// (batchOptions): the installation's own bound, not a number this
		// command invents, so an installation that lengthens a workflow
		// lengthens the reaper's patience with it.
		Timeouts: batchTimeouts{lc: lc},
		DryRun:   dryRun,
		Endpoints: batch.Endpoints{
			Batch:   lc.Endpoints.Batch,
			Compute: reaperComputeRoot,
			Logging: lc.Endpoints.Logging,
			NoAuth:  lc.Endpoints.NoAuth,
		},
	}
}
