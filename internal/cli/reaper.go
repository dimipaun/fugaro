package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
//   - It refuses to run at all where the project has no Batch configuration
//     (no batch: block in the selected project config). A sweep deletes
//     things, and the deadlines it deletes them by are the installation's own
//     task timeouts -- an installation that never provisioned Batch has no
//     such timeouts and, more to the point, no Batch jobs of its own to
//     sweep. A sweeper that guesses what it is looking at is worse than one
//     that does nothing.
//   - A pass that errored exits non-zero, with the error, even though the
//     part it did finish is still printed: a report that stops early is not a
//     clean project (Reaper.Reap says so).
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
			"deletes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReaper(cmd.Context(), cmd, o, dryRun)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be deleted, and delete nothing")
	return cmd
}

// runReaper is `fugaro reaper`: select the project config, open the reaper
// against it and print the one pass's report.
func runReaper(ctx context.Context, cmd *cobra.Command, o cloudOptions, dryRun bool) error {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	if lc.Batch == nil {
		return userErr("project %s has no Batch configuration (its project config has no batch: block), and the reaper only sweeps an installation that runs Batch: its queue deadline and run budget are derived from that installation's own task timeouts, so there is nothing here for it to judge", lc.Name)
	}
	r, err := batch.NewReaper(ctx, reaperOptions(lc, dryRun))
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
	for _, line := range rep.Lines() {
		if _, werr := fmt.Fprintln(cmd.OutOrStdout(), pluginwire.Printable(line)); werr != nil {
			return werr
		}
	}
	if err != nil {
		return remote(err)
	}
	return nil
}

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
