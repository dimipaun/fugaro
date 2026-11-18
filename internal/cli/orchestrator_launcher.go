package cli

import (
	"context"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The resident orchestrator's launcher (mobile plan task P5.1d): the same
// launch, started from a container that has no checkout and no local config.
//
// WHY IT IS A SECOND TYPE OVER THE SAME STEPS, AND NOT A SECOND LAUNCH PATH.
// The daemon's launcher above builds the launch it starts: resolveAttemptPlan
// and buildAttemptSpec read the item's repository, its ref and its account out
// of a local config and a checkout, and createTask writes the task the run
// reads. The resident service has neither of those — its container is told six
// names and holds no project file (orchestrator_serve.go) — so it cannot build
// a spec, and it does not have to: the only action it is handed is a re-issue,
// an attempt whose run id is already on the node and whose task a launcher
// before it stored. What is left is the half that can be reused, and it is
// reused whole: retrySpec is `fugaro run --retry`'s own reader of a stored task,
// and launchRun is the command's claim, mint and job start — the three steps
// the design names for this function (always-on-orchestrator.md section 4.2,
// "takes the launch claim, mints the budget token, and starts the job
// execution. That is fugaro run's existing path with a service account behind
// it"). Nothing here starts a run the node does not carry, and nothing here
// writes a task: createTask is the command's and the daemon's, and an attempt
// whose task the runs bucket does not hold is refused below rather than
// rebuilt.
//
// WHAT IT DELIBERATELY DOES NOT RUN IS THE GATE BLOCK, and the trade is worth
// stating because freshLaunchGates is what the other two launch shapes pass.
// Those gates judge two things this launcher has no hand in: the spec they
// would build (the workflow's kind, the account, the levels, the layer, the
// fields a flag asked for) and the task they would write (the drop a skew gate
// makes lands in what createTask stores, and the image gate judges the task the
// run will read). Here both halves are somebody else's and already done: the
// task was gated by the launch that stored it, and this process neither rebuilds
// it nor writes it. What a skip can cost is bounded and is not a widening: a
// task whose deployed image has since moved under it starts and dies at
// bootstrap on a field its runner cannot read — a spent attempt of a run whose
// execution the launch record then reports — and never a run on a job the gates
// would have refused for a reason about this launch, because the job, the
// variant, the marker and the sealed token are decided by launchRun itself,
// which is the same code the command runs. The caps are the plan's:
// ResidentRequests hands this launcher only re-issues, which planNode's own
// reason is that they spend nothing a cap is there to bound (an attempt that
// was committed and counted before this process woke). A release that wants the
// gate block here too is a change of behaviour over this one, and it belongs to
// whoever owns the deployment that asks for it.
//
// THE ONE QUESTION A CONTAINER CANNOT ANSWER, AND WHY THE ANSWER IS A REFUSAL.
// retrySpec asks the command's own questions of the stored task, and one of them
// is a question about the OPERATOR'S LOCAL CONFIG: the account a recorded task
// names is judged against `claude_accounts` (claudeAccountFor, with
// retrySpec's own nameRecorded origin). A resident container is told six names
// and holds no project file (orchestrator_serve.go), so that list is empty and
// the walk answers `default` for every repository: a task naming no account at
// all is admitted here exactly as it is on a machine whose config lists no
// `claude_accounts`, and a task naming a NAMED Claude account is refused with
// the command's own sentence every tick ("not one <repo> may use (allowed:
// default)"). That sentence sends a reader to a key this deployment does not
// have, so the fix it means is the person's, and it is the same fix the empty
// requested_by refusal below names: `fugaro run --retry <slug>/<run>` from a
// machine whose config knows the account. What the refusal deliberately is NOT
// is a guess: the credential a run mounts is the account its stored task names
// (the job variant is derived from it), so a launcher that resolved another
// account here — the published policy's per-repository axis is what would answer
// this, and this revision reads no policy node — would start a run whose review
// stages are refused for a token nothing mounted. Fail-closed, loud, once per
// tick, and TestResidentLauncherRefusesANamedAccountThisDeploymentCannotSee
// pins it.
type residentLauncher struct {
	// open is the environment each launch starts through, and it is opened
	// per launch rather than once: the config it carries is written with the
	// identity the stored task names (below), and one config shared by the
	// passes of a service that Cloud Run may run two of at once would be two
	// launches attributing each other's run. The opener copies the
	// deployment's config for the same reason, so what a launch writes is
	// never the next launch's.
	open func(ctx context.Context) (*cloudEnv, error)
}

// Launch starts the run the node recorded, over the command's own launch.
//
// The request it reads is the run's stored record and not the spec it is
// handed: the spec is the item's declaration (orchestrator.SpecOf), and what
// launchRun starts is the task the earlier launcher stored — which is what
// makes this a re-issue of one attempt rather than a launch of an item. The
// two are held to each other all the same: a task whose repository is not the
// item's is refused, so the run this returns is the run the node's own record
// asks for and not merely the one whose id it happens to name.
func (l *residentLauncher) Launch(ctx context.Context, spec orchestrator.Spec, runID string) (string, error) {
	// A container has no checkout, and this launch must not read one if a
	// deployment ever gives it one: every reader below that could reach a
	// working tree — the document, the account, the job decision — is a
	// reader of the repository's own bytes, and the resident service reads
	// them from the bucket or not at all (the daemon's Launch installs the
	// same switch for the same reason).
	ctx = withoutCheckout(ctx)
	// One read of the repository's document at the forge for this whole
	// launch, installed before any reader of it: the same cache finishLaunch
	// and the daemon's launch install, and for the same reason.
	ctx = withServiceReadCache(ctx)
	env, err := l.open(ctx)
	if err != nil {
		return "", err
	}
	// retrySpec is the command's --retry reader: the run is located in the
	// runs bucket by the id the node recorded (a bare id, which is the
	// whole-bucket listing the launcher's own binding exists for, so a
	// repository this deployment cannot name is still reachable), its task is
	// read, and the refusals a stored task meets — an A/B arm, a report run,
	// an account the repository may no longer use — are the command's own,
	// in the command's words.
	slug, ts, err := retrySpec(ctx, env, runID)
	if err != nil {
		return "", err
	}
	if err := residentRunIsTheItems(spec, slug, ts); err != nil {
		return "", err
	}
	// The repository's document is deliberately NOT installed here, and the
	// call the command and the daemon make is absent because it would be a
	// no-op that reads as an effect: withLaunchDocument answers a launch
	// whose working tree cannot give a fugaro.yaml out of the checkout's own
	// OBJECTS, and a container has no checkout — with withoutCheckout above,
	// checkoutRoot is "" for every repository, and that function's own rule
	// leaves such a launch alone (its doc says the same for the daemon, whose
	// launches are this shape). What reads a document below is therefore the
	// job decision, and it reads the repository's at the ref over the
	// installation's credentials service (launchProjectJobFor ->
	// launchConfigWhereTheTreeGivesNone -> repoConfigAtTheRef, spec.md's
	// source 3, D902) — a service this account never calls (Q13, section
	// 4.3). A project that published no profile decides the per-repository
	// job from that same function's (nil, nil) without a document at all,
	// which is the case a launch of this deployment can serve; a repository
	// whose document has to be read at the ref is refused, every tick, with
	// the command's own refusal (orchestrator_serve.go's header is where the
	// gap is named).
	// The identity this launch mints the run's budget token under, and the
	// one name a container cannot answer for itself. `Me` would reach for git
	// config in a container that has neither a person nor a checkout, and a
	// resident launch is not nobody's: the task says who asked for the run
	// (requested_by, written by the launch that stored it), the launch record
	// says the same name (launched_by), and the token's RB claim — its own
	// self-asserted attribution, which nothing in the rules reads — says it
	// too rather than a service account's. So the config this launch's env
	// carries is given that name here, on a config the opener made for this
	// launch alone.
	if ts.RequestedBy == "" {
		return "", userErr("run %s/%s names nobody who asked for it, and a resident launch gives the run the budget identity of the person its own record carries: the task's requested_by is what the launch record's launched_by says beside it, and this service will not mint a run's identity under a name it invented. Launch it from a machine of your own (fugaro run --retry %s/%s), or patch the item's attempt so that it names a run whose task does", slug, runID, slug, runID)
	}
	env.lc.User = ts.RequestedBy
	res, err := launchRun(ctx, env, slug, ts, time.Now())
	if err != nil {
		return "", err
	}
	return res.RunID, nil
}

// residentRunIsTheItems refuses a run whose stored task is not the item's own
// repository: the node records the attempt's run id, and this service locates
// that run in the bucket by the id alone (retrySpec, a bare run id), so a node
// whose item names one repository and whose run id belongs to another would
// otherwise start the other repository's run. It is the same fence the starter
// keeps on the id: what is started is the attempt the record names, and the
// record here is the item beside the run id as well as the id itself.
func residentRunIsTheItems(spec orchestrator.Spec, slug string, ts *task.Spec) error {
	want, err := task.CanonicalRepo(spec.Repo)
	if err != nil {
		return userErr("the item of run %s/%s names no repository this release can read (%v), and a resident launch starts what the item and the run's own task agree on", slug, ts.RunID, err)
	}
	got, err := task.CanonicalRepo(ts.Repo)
	if err != nil {
		return userErr("run %s/%s holds the task of %s, whose repository this release cannot read (%v)", slug, ts.RunID, oneLine(ts.Repo), err)
	}
	if got != want {
		return userErr("the item of run %s/%s is %s's, and the run's stored task is %s's: a resident launch starts the attempt the node recorded, and the record it acts on is the item and the run id together — nothing was started", slug, ts.RunID, oneLine(spec.Repo), oneLine(ts.Repo))
	}
	return nil
}

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
	o := &runOptions{repo: spec.Repo, recipe: spec.Recipe, tier: spec.RigorTier, runID: runID, title: spec.Title, planTask: spec.PlanTask}
	plan, err := resolveAttemptPlan(ctx, l.env, o, 0, l.stderr, false)
	if err != nil {
		return "", nil, err
	}
	slug, ts, err := buildAttemptSpec(plan, o, spec.Brief)
	if err != nil {
		return "", nil, err
	}
	// The item's external id, which no flag carries and only this launch
	// writes: it is the ticket the item came from, and the pull request of a
	// run for it names that ticket so a merge closes it (B5, D2727). It is
	// dropped rather than refused against a job image whose runner cannot read
	// it (externalSkew), because what the drop costs is one `Fixes` line and
	// what a refusal costs is the approved attempt — so it is set here, before
	// every gate that judges the task and before createTask writes it, and the
	// task the runner reads is the one this launch decided on.
	//
	// The plan task above is the other half of the same pair and is passed
	// through runOptions instead, because it is a field `fugaro run` also
	// writes (--plan-task) and its gate is therefore the command's own. It
	// lands in task.json and in launch.json — the two records `plan status`
	// joins a daemon's run to its node by, and the ones the --plan-task
	// duplicate refusal reads (F38, B7). An item's plan task is never dropped:
	// it is part of what a person approved, so an image whose runner cannot
	// read it is refused by that gate in freshLaunchGates (planTaskSince),
	// exactly as `fugaro run --plan-task` is, and the approved attempt is not
	// spent on a run that dies at bootstrap.
	ts.External = spec.External
	externalSkew(ctx, l.env, slug, ts, nil, l.stderr)
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
