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
)

// Cancel statuses, as cancel --json reports them.
const (
	cancelAlreadyFinished = "already-finished" // the execution had ended; no marker written
	cancelNotLaunched     = "not-launched"     // never launched; the marker stops a --retry
	cancelFinalized       = "finalized"        // the runner finalized (the PR exists) after the marker
	cancelCancelled       = "cancelled"        // the execution was cancelled through the backend
)

type cancelOptions struct {
	cloud        cloudOptions
	grace        time.Duration
	finalizeWait time.Duration
	poll         time.Duration
	now, asJSON  bool
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
			"minutes for finalize, and counts writeback as finalized. --now stops the\n" +
			"execution at once, without waiting for the draft PR.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.grace < 0 || o.finalizeWait < 0 || o.poll <= 0 {
				return userErr("--grace and --finalize-wait must not be negative, and --poll must be positive")
			}
			env, err := openCloud(cmd.Context(), o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			return cancelRun(cmd.Context(), env, &o, args[0], cmd.OutOrStdout())
		},
	}
	f := cmd.Flags()
	f.DurationVar(&o.grace, "grace", 3*time.Minute, "how long to wait for the runner to finalize before cancelling the execution")
	f.BoolVar(&o.now, "now", false, "cancel the execution at once, without waiting for a draft PR")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	f.DurationVar(&o.finalizeWait, "finalize-wait", 10*time.Minute, "extra wait while the runner is in finalize")
	f.DurationVar(&o.poll, "poll", 10*time.Second, "how often to check the run")
	_ = f.MarkHidden("finalize-wait")
	_ = f.MarkHidden("poll")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// cancelRun is cancel's flow (design §4.5) against env.
func cancelRun(ctx context.Context, env *cloudEnv, o *cancelOptions, arg string, out io.Writer) error {
	emit := func(r cancelResult) error { return printCancel(out, r, o) }
	// Not locateLaunched: a never-launched run is a case cancel handles.
	slug, id, err := locateRun(ctx, env, arg)
	if err != nil {
		return err
	}
	ref := slug + "/" + id
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := ownerLaunch(ctx, s, id)
	if err != nil {
		return err
	}
	if l == nil || l.Execution == "" {
		if err := s.RequestCancel(ctx); err != nil {
			return remote(fmt.Errorf("writing the cancel marker: %w", err))
		}
		return emit(cancelResult{Run: ref, Status: cancelNotLaunched, Marker: true})
	}
	e, err := env.be.Execution(ctx, l.Execution)
	switch {
	case errors.Is(err, backend.ErrNotFound):
		return emit(cancelResult{Run: ref, Status: cancelAlreadyFinished})
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
	if !o.asJSON {
		if o.now {
			_, _ = fmt.Fprintf(out, "cancel requested for %s; cancelling the execution now…\n", ref)
		} else {
			_, _ = fmt.Fprintf(out, "cancel requested for %s; waiting up to %s for the runner to finalize a draft PR…\n", ref, o.grace)
		}
	}

	grace := o.grace
	if o.now {
		grace = 0
	}
	start := time.Now()
	for {
		rec, rerr := s.ReadRecord(ctx)
		if rerr != nil && !errors.Is(rerr, runstore.ErrNotFound) {
			return remote(rerr)
		}
		// writeback runs after finalize: the PR exists.
		if rec != nil && (rec.Status != runstore.StatusRunning || rec.Stage == "writeback") {
			return emit(finalized(ref, marker, rec))
		}
		e, err := env.be.Execution(ctx, l.Execution)
		if err != nil {
			return remote(err)
		}
		if e.State.Terminal() {
			return emit(finalized(ref, marker, rec))
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
		if e, eerr := env.be.Execution(ctx, l.Execution); eerr == nil && e.State.Terminal() {
			rec, _ := absent(s.ReadRecord(ctx))
			return emit(finalized(ref, marker, rec))
		}
		return remote(err)
	}
	return emit(cancelResult{Run: ref, Status: cancelCancelled, Marker: marker, Hard: true})
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
	switch r.Status {
	case cancelAlreadyFinished:
		msg = fmt.Sprintf("%s has already finished; nothing to cancel", r.Run)
	case cancelNotLaunched:
		msg = fmt.Sprintf("%s was never launched; marked it cancelled, so fugaro run --retry refuses it", r.Run)
	case cancelFinalized:
		if r.PR != "" {
			msg = fmt.Sprintf("finalized (draft PR %s)", r.PR)
		} else {
			msg = fmt.Sprintf("the execution has ended; check fugaro diagnose %s for its PR", r.Run)
		}
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
