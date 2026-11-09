package cli

import (
	"context"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The daemon's launcher: one launch path, shared with `fugaro run` (F11, D931,
// T2).
//
// The daemon starts runs of its own, and the one thing it must never do is
// grow a second launch path: a launch that skips the budget pre-check, or that
// can start the same run id twice, is a hole `fugaro run` does not have. So
// this implements orchestrator.Launcher over the same steps the command takes
// — the spec is built by the command's own resolveAttemptPlan and
// buildAttemptSpec, the task is stored once by createTask, the budget
// pre-check runs before anything is claimed, and the run is started by
// launchRun, whose claim is what makes it at most once. What it deliberately
// does not reach is the command's own flags: `--attach`, the image and
// compression gates, the seal a person's launch asks for. Those are a person's
// asks, and a work item carries none of them; a run started by the daemon is
// the plain launch these steps make.
//
// The run id is the state's: the starter mints it, commits the intent that
// records it and only then calls Launch, so the id here is one the node
// already carries, and it is never re-minted (D1231).

// orchestratorLauncher starts the runs of one daemon. The environment is
// opened once, at start-up, and every launch reuses it: a daemon that opened
// its backend per attempt would pay for the connection on every tick.
type orchestratorLauncher struct {
	env    *cloudEnv
	stderr io.Writer
	// started is the daemon's answer to "is a launch in progress", which is
	// the one moment the binary check is skipped: a daemon that handed over
	// mid-launch would abandon a start whose intent is already on the node.
	started *launchInFlight
}

// Launch starts the run recorded as runID for one attempt, over the path
// `fugaro run` takes.
//
// It returns the run id it started, which is the one it was handed: the task
// it stores is named by that id, and launchRun's answer for a run that already
// went is that same id, so a re-issued intent after a crash reports the run
// that exists rather than starting a second one.
func (l *orchestratorLauncher) Launch(ctx context.Context, spec orchestrator.Spec, runID string) (string, error) {
	l.started.begin()
	defer l.started.done()
	o := &runOptions{repo: spec.Repo, recipe: spec.Recipe, rigor: spec.RigorTier, runID: runID, title: spec.Title}
	plan, err := resolveAttemptPlan(ctx, l.env, o, 0, l.stderr, false)
	if err != nil {
		return "", err
	}
	slug, ts, err := buildAttemptSpec(plan, o, spec.Brief)
	if err != nil {
		return "", err
	}
	// The budget pre-check, before the task is stored and before the claim:
	// a launch the budget refuses costs no claim to release, which is the
	// order `fugaro run` refuses in too. It fails closed, so an unreachable
	// database stops the launch rather than starting a run nothing counts.
	if err := l.env.budgetPrecheck(ctx, slug, ts, l.stderr); err != nil {
		return "", err
	}
	s := runstore.Open(l.env.bucket.Bucket, slug, ts.RunID)
	if err := createTask(ctx, s, ts); err != nil {
		return "", err
	}
	res, err := launchRun(ctx, l.env, slug, ts, time.Now())
	if err != nil {
		return "", err
	}
	return res.RunID, nil
}
