package cli

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// followUpLookback is how far back fugaro run --pr looks for a pull
// request's runs: the runs/ lifecycle age (design §3.3), so every run the
// bucket still holds is seen.
const followUpLookback = 90 * 24 * time.Hour

// prChain is what the runs bucket says about a Fugaro pull request.
type prChain struct {
	Branch   string
	Previous runview.Row   // the newest run on the PR (by started_at) that pushed
	Runs     []runview.Row // every run on the PR, newest first
	// Ref and Workflow are the previous run's: a follow-up keeps the base
	// branch and workflow of the runs before it.
	Ref, Workflow string
}

// resolvePR finds pull request pr of slug through the runs bucket, and
// refuses a follow-up whose context can't be trusted. exclude is a run ID
// to leave out (a retry, or a repeated --run-id). Every refusal is a
// userErr naming the run and what to do; a failed read is a remote error.
// The runner checks the PR itself with the provider when it starts.
func resolvePR(ctx context.Context, env *cloudEnv, slug string, pr int, exclude string, warn io.Writer) (*prChain, error) {
	if warn == nil {
		warn = io.Discard
	}
	now := time.Now()
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), pr: pr, warn: warn}, now)
	if err != nil {
		return nil, err
	}
	// A run is on the PR when its task continues it, or when its record
	// names it and it is on a Fugaro branch.
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return r.RunID == exclude || (r.TaskPR != pr && !task.BranchRE.MatchString(r.Branch))
	})
	if len(rows) == 0 {
		return nil, userErr("PR #%d is not a Fugaro PR in this repository, or its runs are older than %d days; start a new run instead",
			pr, int(followUpLookback/(24*time.Hour)))
	}
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := startOf(b).Compare(startOf(a)); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})

	c := &prChain{Runs: rows}
	var runs []runview.Row // the launched runs
	for _, r := range rows {
		switch r.Status {
		case runview.StatusError:
			return nil, userErr("run %s on PR #%d can't be read (%s), so the PR's state is unknown; see fugaro diagnose %s", r.Run, pr, oneLine(r.Reason), r.Run)
		case runview.StatusLaunching, runview.StatusPending, string(runstore.StatusRunning):
			return nil, userErr("run %s on PR #%d is still %s; wait for it to finish (fugaro ls --pr %d) or cancel it", r.Run, pr, r.Status, pr)
		case runview.StatusUnlaunched:
			fmt.Fprintf(warn, "warning: run %s on PR #%d never launched; leaving it out\n", oneLine(r.Run), pr)
			continue
		}
		runs = append(runs, r)
	}
	for _, r := range rows {
		switch {
		case r.Branch == "":
		case c.Branch == "":
			c.Branch = r.Branch
		case r.Branch != c.Branch:
			return nil, userErr("the runs on PR #%d disagree on its branch (%s, %s); follow it up by hand", pr, oneLine(c.Branch), oneLine(r.Branch))
		}
	}
	root, ok := task.BranchRunID(c.Branch)
	if !ok {
		return nil, userErr("PR #%d's branch %q is not a Fugaro branch; start a new run instead", pr, oneLine(c.Branch))
	}
	if err := checkRoot(ctx, env, slug, root, c.Branch, pr); err != nil {
		return nil, err
	}
	i := slices.IndexFunc(runs, func(r runview.Row) bool { return r.Pushed })
	if i < 0 {
		return nil, userErr("no run on PR #%d has pushed to %s, so there is nothing to follow up; start a new run instead", pr, c.Branch)
	}
	c.Previous = runs[i]
	if err := checkBranchLock(ctx, env, slug, c.Branch, now); err != nil {
		return nil, err
	}
	prev, err := readTask(ctx, env, slug, c.Previous.RunID)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, userErr("run %s, the last to update PR #%d, has no task.json, so its base branch is unknown; start a new run instead", c.Previous.Run, pr)
	case err != nil:
		return nil, err
	}
	c.Ref, c.Workflow = prev.Ref, prev.Workflow
	if c.Workflow == "" {
		c.Workflow = c.Previous.Workflow
	}
	return c, nil
}

// readTask reads run id's task.json: runstore.ErrNotFound when there is
// none, a userErr when it is unreadable, a remote error when the read fails.
func readTask(ctx context.Context, env *cloudEnv, slug, id string) (*task.Spec, error) {
	raw, err := runstore.Open(env.bucket.Bucket, slug, id).ReadFile(ctx, "task.json")
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, err
	case corruptObject(err):
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, id, err)
	case err != nil:
		return nil, remote(err)
	}
	spec, err := task.Parse(raw)
	if err != nil {
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, id, err)
	}
	return spec, nil
}

// startOf is when a row's run started: its record's started_at, else its
// run ID's time. A run ID can be chosen with --run-id, so its time is used
// only when nothing better is known.
func startOf(r runview.Row) time.Time {
	if r.StartedAt != nil && !r.StartedAt.IsZero() {
		return *r.StartedAt
	}
	return r.Created
}

// checkRoot refuses a branch whose root run (the run it names) still has a
// record that isn't on that branch: the branch would then not be the one
// the root run opened.
func checkRoot(ctx context.Context, env *cloudEnv, slug, root, branch string, pr int) error {
	rec, err := runstore.Open(env.bucket.Bucket, slug, root).ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil // expired: the runs that remain agree on the branch
	case corruptObject(err):
		return userErr("run %s/%s, which PR #%d's branch names, has an unreadable result.json (%v); see fugaro diagnose %s/%s", slug, root, pr, err, slug, root)
	case err != nil:
		return remote(err)
	}
	if rec.Branch != branch {
		return userErr("run %s/%s, which PR #%d's branch %s names, records branch %q; the PR is not the one it opened, so follow it up by hand",
			slug, root, pr, branch, oneLine(rec.Branch))
	}
	return nil
}

// checkBranchLock refuses a branch whose lock is live. An absent, expired or
// unreadable lock is fine: the runner takes it over.
func checkBranchLock(ctx context.Context, env *cloudEnv, slug, branch string, now time.Time) error {
	data, _, err := env.bucket.Read(ctx, lock.Key(slug, branch))
	switch {
	case errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrTooLarge):
		return nil
	case err != nil:
		return remote(err)
	}
	var h lock.Holder
	if json.Unmarshal(data, &h) != nil || !now.Before(h.ExpiresAt) {
		return nil
	}
	who := "another run"
	if h.RunID != "" {
		who = "run " + oneLine(h.RunID)
	}
	return userErr("branch busy: %s holds it until %s; wait for it, or cancel it", who, h.ExpiresAt.UTC().Format(time.RFC3339))
}

// prSpec builds the spec of fugaro run --pr: a stored follow-up that a
// repeated --run-id names (reused true), else a new one after resolvePR.
func prSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string, total time.Duration, warn io.Writer) (slug string, spec *task.Spec, reused bool, err error) {
	repo := o.repo
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return "", nil, false, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", nil, false, userErr("--repo: %v", err)
	}
	if slug, err = env.repoSlug(repo, checkoutOf(ctx, repo)); err != nil {
		return "", nil, false, err
	}
	if o.runID != "" {
		if spec, err = storedFollowUp(ctx, env, slug, o, text, total); err != nil || spec != nil {
			return slug, spec, spec != nil, err
		}
	}
	c, err := resolvePR(ctx, env, slug, o.pr, o.runID, warn)
	if err != nil {
		return "", nil, false, err
	}
	spec, err = newFollowUpSpec(ctx, env, o, repo, c, text, total)
	return slug, spec, false, err
}

// storedFollowUp is the task a repeated --run-id already stored, when it is
// this call's follow-up (the same PR, text, batch and overrides): it is
// reused as it is, its previous_run and requested_by included. Any other
// stored task is refused; nil means there is none.
func storedFollowUp(ctx context.Context, env *cloudEnv, slug string, o *runOptions, text string, total time.Duration) (*task.Spec, error) {
	have, err := readTask(ctx, env, slug, o.runID)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var want task.Overrides
	if total > 0 {
		want.TotalTimeout = total.String()
	}
	a, err1 := json.Marshal(have.Overrides)
	b, err2 := json.Marshal(want)
	if err := errors.Join(err1, err2); err != nil {
		return nil, err
	}
	if have.PR != o.pr || have.Task != text || have.Batch != o.batch || string(a) != string(b) {
		return nil, userErr("run ID %s already holds a different task", o.runID)
	}
	return have, nil
}

// newFollowUpSpec is the task of a new follow-up of chain c: its branch and
// PR, the previous run it continues, and that run's base branch and
// workflow; nothing else is inherited.
func newFollowUpSpec(ctx context.Context, env *cloudEnv, o *runOptions, repo string, c *prChain, text string, total time.Duration) (*task.Spec, error) {
	workflow := c.Workflow
	checkout := checkoutOf(ctx, repo)
	if workflow == "" {
		var err error
		if workflow, err = resolveWorkflow(env, repo, "", checkout); err != nil {
			return nil, err
		}
	}
	if total > 0 {
		if cfg := checkout(); cfg != nil {
			if w, ok := cfg.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	runID := o.runID
	if runID == "" {
		if runID, err = task.NewRunID(time.Now(), crand.Reader); err != nil {
			return nil, err
		}
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: c.Ref, Workflow: workflow, Task: text,
		Branch: c.Branch, PR: o.pr, PreviousRun: c.Previous.RunID, RequestedBy: me, Batch: o.batch}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if err := spec.Validate(); err != nil {
		return nil, userErr("%v", err)
	}
	return spec, nil
}

// recheckFollowUp runs the PR's checks again for a stored follow-up about
// to launch (--retry, or a repeated --run-id of one that never launched),
// with the run itself left out, and refuses it when another run has
// updated the PR since it was made: it would act on stale context.
func recheckFollowUp(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, warn io.Writer) error {
	c, err := resolvePR(ctx, env, slug, spec.PR, spec.RunID, warn)
	if err != nil {
		return err
	}
	if c.Branch != spec.Branch {
		return userErr("run %s/%s continues %s, but PR #%d is on %s; start a new follow-up: fugaro run --pr %d", slug, spec.RunID, spec.Branch, spec.PR, c.Branch, spec.PR)
	}
	if c.Previous.RunID != spec.PreviousRun {
		return userErr("run %s/%s follows %s, but run %s has updated PR #%d since; start a new follow-up: fugaro run --pr %d",
			slug, spec.RunID, spec.PreviousRun, c.Previous.Run, spec.PR, spec.PR)
	}
	return nil
}
