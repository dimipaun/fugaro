package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// Cancel statuses, as cancel --json reports them.
const (
	cancelAlreadyFinished = "already-finished"  // the execution had ended; no marker written
	cancelNotLaunched     = "not-launched"      // never launched; the marker stops a --retry
	cancelLaunching       = "launching"         // a launch is in flight; the marker stops it (launcher or runner)
	cancelFinalized       = "finalized"         // the runner finalized (the PR exists) after the marker
	cancelCancelled       = "cancelled"         // the execution was cancelled through the backend
	cancelUnfinalized     = "ended-unfinalized" // the execution ended (or vanished) without a final record
)

// defaultFinalizeReserve is the grace floor when the run's workflow is not
// known here: the default timeouts.finalize_reserve's cap (config defaults).
const defaultFinalizeReserve = 5 * time.Minute

type cancelOptions struct {
	cloud        cloudOptions
	grace        time.Duration
	finalizeWait time.Duration
	poll         time.Duration
	now, asJSON  bool
	graceSet     bool          // --grace was given explicitly
	floor        time.Duration // --grace-floor, when floorSet (tests only)
	floorSet     bool
}

// cancelResult is what cancel reports.
type cancelResult struct {
	Run    string `json:"run"`
	Status string `json:"status"`
	Marker bool   `json:"marker"`       // the cancel marker was written
	Hard   bool   `json:"hard"`         // the execution was cancelled through the backend
	PR     string `json:"pr,omitempty"` // the run's PR URL, when result.json names one
}

func newCancelCmd() *cobra.Command {
	var o cancelOptions
	cmd := &cobra.Command{
		Use:   "cancel RUN",
		Short: "Cancel a run, letting the runner open a draft PR first",
		Long: "Cancel RUN (<repo-slug>/<run-id>, or a bare run ID).\n\n" +
			"cancel writes the run's cancel marker; the runner sees it, stops the current\n" +
			"stage and finalizes a draft PR. If the execution is still in a stage after\n" +
			"--grace, cancel stops it through Cloud Run. It never stops a run that is\n" +
			"opening its PR (finalize) or writing back its cache: it waits up to 10 more\n" +
			"minutes for finalize, and counts writeback as finalized. --grace is never\n" +
			"shorter than the workflow's timeouts.finalize_reserve (5m when this checkout\n" +
			"cannot tell). --now stops the execution at once, without waiting for the\n" +
			"draft PR, but still not mid-finalize.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.grace < 0 || o.finalizeWait < 0 || o.floor < 0 || o.poll <= 0 {
				return userErr("--grace and --finalize-wait must not be negative, and --poll must be positive")
			}
			o.graceSet, o.floorSet = cmd.Flags().Changed("grace"), cmd.Flags().Changed("grace-floor")
			env, err := openCloud(cmd.Context(), o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			return cancelRun(cmd.Context(), env, &o, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.DurationVar(&o.grace, "grace", 3*time.Minute, "how long to wait for the runner to finalize before cancelling the execution (at least the workflow's finalize reserve)")
	f.BoolVar(&o.now, "now", false, "cancel the execution at once, without waiting for a draft PR")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	f.DurationVar(&o.finalizeWait, "finalize-wait", 10*time.Minute, "extra wait while the runner is in finalize")
	f.DurationVar(&o.poll, "poll", 10*time.Second, "how often to check the run")
	_ = f.MarkHidden("finalize-wait")
	f.DurationVar(&o.floor, "grace-floor", 0, "the least --grace, instead of the workflow's finalize reserve (tests)")
	_ = f.MarkHidden("poll")
	_ = f.MarkHidden("grace-floor")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// cancelRun is cancel's flow (design §4.5) against env.
func cancelRun(ctx context.Context, env *cloudEnv, o *cancelOptions, arg string, out, errOut io.Writer) error {
	emit := func(r cancelResult) error { return printCancel(out, r, o) }
	// Not locateLaunched: a never-launched run is a case cancel handles.
	slug, id, err := locateRun(ctx, env, arg)
	if err != nil {
		return err
	}
	ref := slug + "/" + id
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := ownerLaunch(ctx, env, s, id)
	if err != nil {
		return err
	}
	if l == nil || l.Execution == "" {
		claim, err := absent(s.ReadClaim(ctx))
		if err != nil {
			return remote(err)
		}
		if err := s.RequestCancel(ctx); err != nil {
			return remote(fmt.Errorf("writing the cancel marker: %w", err))
		}
		if claim == nil || time.Since(claim.At) >= runstore.ClaimTTL {
			return emit(cancelResult{Run: ref, Status: cancelNotLaunched, Marker: true})
		}
		// A launch is in flight (C-I3). Its launcher re-checks the marker
		// after claiming; if it had already passed that point, wait for its
		// launch.json and cancel the execution like any launched run.
		if l, err = awaitLaunch(ctx, env, s, id, o.poll); err != nil {
			return err
		}
		if l == nil {
			return emit(cancelResult{Run: ref, Status: cancelLaunching, Marker: true})
		}
	}
	e, err := env.be.Execution(ctx, l.Execution)
	switch {
	case errors.Is(err, backend.ErrNotFound):
		// Forgotten, or not visible yet. Agree with ls (runview.Join).
		rec, rerr := absent(s.ReadRecord(ctx))
		switch {
		case rerr != nil:
			return remote(rerr)
		case hasFinalized(rec):
			return emit(cancelResult{Run: ref, Status: cancelAlreadyFinished})
		case rec == nil && !runview.Lost(l, runTime(id), time.Now()):
			// Launched moments ago: the backend may not list it yet. The
			// marker makes its runner stop at bootstrap.
			if err := s.RequestCancel(ctx); err != nil {
				return remote(fmt.Errorf("writing the cancel marker: %w", err))
			}
			return emit(cancelResult{Run: ref, Status: cancelLaunching, Marker: true})
		}
		return emit(cancelResult{Run: ref, Status: cancelUnfinalized})
	case err != nil:
		return remote(err)
	case e.State.Terminal():
		return emit(cancelResult{Run: ref, Status: cancelAlreadyFinished})
	}

	marker := true
	if err := s.RequestCancel(ctx); err != nil {
		if !o.now {
			// Hard-cancelling would skip finalize and its draft PR.
			return remote(fmt.Errorf("writing the cancel marker (the execution is untouched; --now cancels it without a draft PR): %w", err))
		}
		marker = false
	}
	grace := o.grace
	if o.now {
		grace = 0
	} else if floor, what := graceFloor(ctx, s, o); grace < floor {
		if o.graceSet {
			_, _ = fmt.Fprintf(errOut, "note: --grace %s is shorter than %s; waiting %s so the runner can finalize\n", grace, what, floor)
		}
		grace = floor
	}
	if !o.asJSON {
		if o.now {
			_, _ = fmt.Fprintf(out, "cancel requested for %s; cancelling the execution now…\n", ref)
		} else {
			_, _ = fmt.Fprintf(out, "cancel requested for %s; waiting up to %s for the runner to finalize a draft PR…\n", ref, grace)
		}
	}

	start := time.Now()
	for {
		rec, rerr := s.ReadRecord(ctx)
		if rerr != nil && !errors.Is(rerr, runstore.ErrNotFound) {
			return remote(rerr)
		}
		// writeback runs after finalize: the PR exists.
		if hasFinalized(rec) {
			return emit(finalized(ref, marker, rec))
		}
		e, err := env.be.Execution(ctx, l.Execution)
		gone := errors.Is(err, backend.ErrNotFound) // forgotten: finished (note 5)
		if err != nil && !gone {
			return remote(err)
		}
		if gone || e.State.Terminal() {
			return emit(ended(ctx, s, ref, marker))
		}
		finalizing := rec != nil && rec.Stage == "finalize"
		limit := grace
		if finalizing {
			limit += o.finalizeWait
		}
		if time.Since(start) >= limit {
			if !finalizing {
				break
			}
			// Still finalizing after the extension: leave it to the task timeout.
			return userErr("the run is still finalizing after %s; not cancelling it mid-finalize; check fugaro diagnose %s", limit, ref)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.poll):
		}
	}
	if err := env.be.Cancel(ctx, l.Execution); err != nil {
		// Cloud Run refuses to cancel an execution that has just ended
		// (FAILED_PRECONDITION); that is the runner finishing, not a failure.
		if e, eerr := env.be.Execution(ctx, l.Execution); errors.Is(eerr, backend.ErrNotFound) || (eerr == nil && e.State.Terminal()) {
			return emit(ended(ctx, s, ref, marker))
		}
		return remote(err)
	}
	return emit(cancelResult{Run: ref, Status: cancelCancelled, Marker: marker, Hard: true})
}

// awaitLaunch polls up to claimWait for the launch of a run whose claim is
// held: nil when the claim is released (the launcher refused) or the wait
// runs out.
func awaitLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, id string, poll time.Duration) (*runstore.Launch, error) {
	deadline := time.Now().Add(claimWait)
	for {
		l, err := ownerLaunch(ctx, env, s, id)
		if err != nil || (l != nil && l.Execution != "") {
			return l, err
		}
		held, err := env.bucket.Exists(ctx, s.ClaimKey())
		if err != nil {
			return nil, remote(err)
		}
		if !held || !time.Now().Before(deadline) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// runTime is the time in run ID id, or zero.
func runTime(id string) time.Time {
	t, _ := runstore.RunTime(id)
	return t
}

// graceFloor is the least grace cancel waits, and what it is: the run's
// workflow's finalize reserve, from this checkout's fugaro.yaml when it is
// the run's repository, else defaultFinalizeReserve (plan I-4, note 8).
func graceFloor(ctx context.Context, s *runstore.Store, o *cancelOptions) (time.Duration, string) {
	if o.floorSet {
		return o.floor, "--grace-floor"
	}
	if spec, err := s.ReadTask(ctx); err == nil && spec.Workflow != "" {
		if c := checkoutConfig(ctx, spec.Repo); c != nil {
			if w, ok := c.Workflows[spec.Workflow]; ok && w.Timeouts.FinalizeReserve.Duration > 0 {
				return w.Timeouts.FinalizeReserve.Duration, "workflow " + spec.Workflow + "'s finalize reserve"
			}
		}
	}
	return defaultFinalizeReserve, "the default finalize reserve"
}

// hasFinalized reports whether rec shows the runner past opening its PR:
// a final status, or writeback, which runs after finalize.
func hasFinalized(rec *runstore.Record) bool {
	return rec != nil && (rec.Status != runstore.StatusRunning || rec.Stage == "writeback")
}

// ended is the result for an execution that has ended or that the backend
// forgot: finalized when result.json (re-read, since the runner writes it
// just before exiting) shows it, else ended-unfinalized, never finalized
// (the PartialError conservative direction).
func ended(ctx context.Context, s *runstore.Store, ref string, marker bool) cancelResult {
	rec, err := absent(s.ReadRecord(ctx))
	if err == nil && hasFinalized(rec) {
		return finalized(ref, marker, rec)
	}
	return cancelResult{Run: ref, Status: cancelUnfinalized, Marker: marker}
}

// finalized is a finalized result, with rec's PR when it names one.
func finalized(ref string, marker bool, rec *runstore.Record) cancelResult {
	r := cancelResult{Run: ref, Status: cancelFinalized, Marker: marker}
	if rec != nil && rec.PR != nil {
		r.PR = rec.PR.URL
	}
	return r
}

// printCancel prints r as JSON (--json) or as a line of text.
func printCancel(w io.Writer, r cancelResult, o *cancelOptions) error {
	if o.asJSON {
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", data)
		return err
	}
	var msg string
	r.Run, r.PR = oneLine(r.Run), oneLine(r.PR)
	switch r.Status {
	case cancelAlreadyFinished:
		msg = fmt.Sprintf("%s has already finished; nothing to cancel", r.Run)
	case cancelNotLaunched:
		msg = fmt.Sprintf("%s was never launched; marked it cancelled, so fugaro run --retry refuses it", r.Run)
	case cancelLaunching:
		msg = fmt.Sprintf("a launch of %s is in flight; marked it cancelled, so the launch is refused or its runner stops at bootstrap; check fugaro ls", r.Run)
	case cancelFinalized:
		if r.PR != "" {
			msg = fmt.Sprintf("finalized (draft PR %s)", r.PR)
		} else {
			msg = fmt.Sprintf("finalized; check fugaro diagnose %s for its PR", r.Run)
		}
	case cancelUnfinalized:
		msg = fmt.Sprintf("the run ended without finalizing; check fugaro diagnose %s", r.Run)
	default:
		lead := "grace period over; cancelled the execution."
		if o.now {
			lead = "cancelled the execution."
		}
		msg = fmt.Sprintf("%s The run may not have a draft PR; check fugaro diagnose %s", lead, r.Run)
	}
	_, err := fmt.Fprintln(w, msg)
	return err
}
