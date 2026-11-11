package cli

import (
	"context"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The daemon's launcher: one launch path, shared with `fugaro run` (F11, D931,
// T2).
//
// The daemon starts runs of its own, and the one thing it must never do is
// grow a second launch path: a launch that skips the budget pre-check, or that
// can start the same run id twice, is a hole `fugaro run` does not have. So
// this implements orchestrator.Launcher over the same steps the command takes
// — the spec is built by the command's own resolveAttemptPlan and
// buildAttemptSpec, the task is stored once by createTask, and the run is
// started by launchRun, whose claim is what makes it at most once — and over
// the command's own gate block, which the daemon's launch shape passes whole
// (freshLaunchGates). What it deliberately does not reach is the flags no work
// item carries: `--attach` above all, whose upload is the command's and whose
// spec fields are empty here, so the daemon starts the plain launch these steps
// make and nothing else.
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
	// The launch reads no checkout, and never the one the daemon happens to run
	// in: a daemon's working directory is whatever its unit was started from,
	// and a checkout of the item's repository there would decide the launch.
	//
	// Two of its reads are why. The ref (newRunRef): the branch a person left
	// checked out is not the one the approval's digest covers, it moves under
	// the daemon, and switching it between a crash and the re-issue rebuilds a
	// spec whose task.json differs from the stored one — createTask then
	// refuses the attempt every tick. And the workflow's own kind, which decides
	// the job: from a checkout with a `checkout: clone` workflow this launch
	// would take the shared project job of the workflow's profile while the gates
	// over it judged a repository with no checkout — the clone-side gates among
	// them (the base image a clone workflow runs on, the per-field gates, the
	// seal, the proxy pair) decided against a kind the launch itself ignored.
	//
	// The switch therefore covers the whole launch: the plan and every gate
	// (prepare) and the start itself (launchRun), whose job resolution, docker
	// tag rule and credentials-service pair are reads of the working directory
	// too. What that costs is the reads a checkout is needed for: they cannot be
	// answered at all, and the launch refuses rather than guesses (project_job.go:
	// a project that published profiles cannot tell a clone workflow's shared job
	// from a baked workflow's own).
	ctx = withoutCheckout(ctx)
	// The one read this launch makes of the repository's document at the forge
	// (project_job.go) is shared by its two readers — the gates (prepare,
	// freshLaunchGates) and the job decision (launchRun) — so it is installed
	// over the whole launch, before either: the two would otherwise cost two
	// mints where spec.md §6 allows one (D904). `fugaro run` installs one in
	// the same place and for the same reason (finishLaunch).
	ctx = withServiceReadCache(ctx)
	slug, ts, err := l.prepare(ctx, spec, runID)
	if err != nil {
		return "", err
	}
	res, err := launchRun(ctx, l.env, slug, ts, time.Now())
	if err != nil {
		return "", err
	}
	return res.RunID, nil
}

// prepare is everything a launch does before it starts anything, and it is a
// step of its own because that is where a daemon can die: the task is the
// stored record of the attempt, and a crash between storing it and starting
// the run is the one the next tick has to finish rather than start over. What
// it returns is what launchRun needs and nothing else.
//
// It resolves under the context Launch marked as checkoutless, and it does not
// mark one itself: the launch is one resolution, and a part of it that installed
// the switch on its own is how the launch ends up judging the repository with no
// checkout and starting it on the one the daemon's directory holds.
func (l *orchestratorLauncher) prepare(ctx context.Context, spec orchestrator.Spec, runID string) (string, *task.Spec, error) {
	// Everything here resolves under the launch's checkoutless context, which
	// Launch installs over the whole launch: the plan, the project layer the
	// task carries and every gate are one resolution, and the start that
	// follows them is judged by the same one.
	//
	// The item's rigor_tier is the expectation tier (trivial to large) and not
	// the rigor mode: a daemon declares no paths and asks for no mode, so the
	// run resolves its rigor from the configuration alone, which is what a
	// launch nobody typed can honestly say.
	o := &runOptions{repo: spec.Repo, recipe: spec.Recipe, tier: spec.RigorTier, runID: runID, title: spec.Title}
	plan, err := resolveAttemptPlan(ctx, l.env, o, 0, l.stderr, false)
	if err != nil {
		return "", nil, err
	}
	slug, ts, err := buildAttemptSpec(plan, o, spec.Brief)
	if err != nil {
		return "", nil, err
	}
	// The project layer goes into the task's own copy, the way `fugaro run`
	// puts it there: the runner resolves the run's configuration from the
	// task and nowhere else, so a launch that skipped this would start a run
	// without the project's shared defaults — the daemon's own project
	// selection is not something a run can read back.
	if _, err := embedProjectLayer(ctx, l.env, ts, l.stderr); err != nil {
		return "", nil, err
	}
	// The gates `fugaro run` passes before it writes anything of a launch, in
	// its own order and from its own code (freshLaunchGates): the budget
	// pre-check, the workflow's image and per-field gates, the author, the
	// seal, the proxy, the parallel cap, the artifact caps and the policy.
	// The daemon's launch is that same shape, so it is that same block — a
	// launch path that ran only the pre-check and the policy started runs the
	// command refuses, onto a runner that cannot read the sealed token or a
	// field the task carries, and the run then died at bootstrap with the
	// approved attempt spent. Before the task is stored and before the claim,
	// like every gate there: a refusal leaves no task, no token, no claim and
	// no execution behind.
	compression, _ := compressionRequested(ctx, l.env, ts)
	s := runstore.Open(l.env.bucket.Bucket, slug, ts.RunID)
	// A re-issued intent whose earlier attempt stored a task but died before
	// the start: the runner reads the stored task and its layer, whatever this
	// tick just re-resolved (createTask keeps it), so the gates below must
	// judge that layer rather than a project layer republished in between. The
	// alternative is a document the run never reads deciding the launch: a
	// spurious refusal that leaves the attempt stuck with a failing cycle
	// every tick, or a gate that passes for a task carrying fields the
	// deployed image cannot parse. `fugaro run` adopts the stored layer in
	// this same place and for this same case (finishLaunch).
	if err := adoptStoredProjectLayer(ctx, s, ts); err != nil {
		return "", nil, err
	}
	// The launch that already went, asked before the gates rather than only
	// where launchRun asks it, because a gate judging a re-issue judges it as
	// a first launch. The parallel cap is the one that bites: a crash between
	// jobs.run and launch.json — the execution started, the process died
	// before the record of it was written — leaves the run's own execution in
	// flight under a node whose launch is exactly what the gates then read as
	// a fresh run, so under a full max_parallel the run is refused by itself
	// ("1 runs active; max_parallel is 1") and keeps being refused every tick
	// for the rest of its life, the attempt failing on a run that is going
	// fine until the breaker trips on it. `fugaro run` passes the same block
	// in the same place and for the same reason (finishLaunch): a run id that
	// already started is not judged again, and what is left of its launch is
	// the report that it went, which launchRun makes below.
	prior, err := existingLaunch(ctx, l.env, s, ts)
	if err != nil {
		return "", nil, err
	}
	if prior == nil {
		if _, err := freshLaunchGates(ctx, l.env, o, slug, s, ts, compression || ts.CompressionOff, l.stderr, false); err != nil {
			return "", nil, err
		}
	}
	if err := createTask(ctx, s, ts); err != nil {
		return "", nil, err
	}
	return slug, ts, nil
}
