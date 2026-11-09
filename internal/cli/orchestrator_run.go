package cli

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro orchestrator run: the foreground daemon (O8).
//
// It takes the orchestrator id's lease, ticks, writes the session-start cache
// and waits: every N seconds, or sooner when the node's own stream says
// something moved (F8: the Firebase Realtime Database's SSE stream is what
// wakes a local daemon; there is no Eventarc in any Terraform module and none
// is wanted). Nothing here converges anything: a daemon that finds the state
// node at a schema version this release does not read reports that an update
// is due and starts nothing else (G8), and no step of this command ever passes
// a `--yes` to a confirmation — the gated commands are the person's, and this
// file does not import them.
//
// The command is foreground on purpose: what keeps the daemon alive is the
// login unit (`fugaro setup` writes it, O9), and what restarts it after a
// package manager replaces the binary is that unit seeing this process exit 0
// (the check in orchestrator_version.go).

// orchestratorRunOptions are `fugaro orchestrator run`'s flags.
type orchestratorRunOptions struct {
	cloudOptions
	// binary is the stable path the unit names, and the path whose version is
	// checked each tick. Empty is a foreground run with no unit behind it:
	// nothing restarts it, so nothing is checked.
	binary string
	// interval is how long a cycle waits when the node says nothing moved.
	// The stream is the other half of the wait, so this is the ceiling on how
	// stale a tick can be, not the usual delay.
	interval time.Duration
	// once is one cycle and exit. It is what a person (or a harness that holds
	// every command to returning with its stdin closed) can run: the lease is
	// taken for that cycle and released with it, so a single pass never keeps
	// the id from the daemon that drives it.
	once bool
}

func newOrchestratorRunCmd() *cobra.Command {
	var o orchestratorRunOptions
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the project's orchestrator daemon in the foreground",
		Long: "Run the project's orchestrator daemon: take the id's lease, tick every\n" +
			"interval — and sooner when the database's own stream says the node moved —\n" +
			"start the runs the state's plan calls for over the same launch path fugaro\n" +
			"run takes, and rewrite the session-start cache after every cycle. It stops\n" +
			"when the lease goes to another machine, and it exits cleanly (releasing the\n" +
			"lease) when the binary the unit names has moved on, so the login unit starts\n" +
			"the new one.\n\n" +
			"The command is the person's own credential and the daemon's own loop: it\n" +
			"never converges, publishes or installs anything, and a state node this\n" +
			"release does not read is reported as an update being due rather than\n" +
			"touched. Run it in the foreground to watch it; the login unit is what keeps\n" +
			"it running (fugaro setup installs it).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorRun(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.binary, "binary", "", "the stable path of the fugaro binary the login unit names: the daemon exits cleanly when the version there differs from this process")
	f.DurationVar(&o.interval, "interval", 30*time.Second, "how long to wait between cycles when the node says nothing moved")
	f.BoolVar(&o.once, "once", false, "run one cycle and exit, releasing the lease, instead of driving the id until it is stopped")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

func runOrchestratorRun(cmd *cobra.Command, o *orchestratorRunOptions) error {
	ctx := cmd.Context()
	if o.interval <= 0 {
		return userErr("--interval takes a positive duration such as 30s, and %s is not one", o.interval)
	}
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	id, err := orchestratorID(lc)
	if err != nil {
		return err
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	holder, err := task.NewRunID(time.Now(), crand.Reader)
	if err != nil {
		return userErr("naming this daemon: %v", err)
	}
	lease, err := orchestrator.NewLease(db, id, holder, db.ServerNow)
	if err != nil {
		return userErr("%v", err)
	}
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator, Lease: lease})
	if err != nil {
		return userErr("%v", err)
	}
	d := &orchestratorDaemon{
		lc: lc, id: id, db: db, env: env, node: node, lease: lease, holder: holder,
		interval: o.interval, binary: o.binary, running: Version, once: o.once,
		out: cmd.OutOrStdout(), warn: cmd.ErrOrStderr(),
		launched: &launchInFlight{},
	}
	d.cache = &orchestrator.BriefCacheWriter{Getenv: os.Getenv, GOOS: runtime.GOOS, ID: id, Log: func(err error) {
		fmt.Fprintf(d.warn, "orchestrator %s: the session-start cache was not written: %s\n", d.id, safetext.OneLine(err.Error()))
	}}
	return d.start(ctx)
}

// orchestratorDaemon is one running daemon: the id it drives, the client it
// reads and writes through, the lease it holds and everything a tick acts
// with.
type orchestratorDaemon struct {
	lc     *localcfg.Config
	id     string
	db     *rtdb.Client
	env    *cloudEnv
	node   *orchestrator.Node
	lease  *orchestrator.Lease
	holder string
	// interval and binary are the two flags, and running is the version of
	// this process, which the version check compares the unit's binary with.
	interval time.Duration
	binary   string
	running  string
	// once is --once: one cycle, then the clean exit.
	once     bool
	tick     *orchestrator.Tick
	cache    *orchestrator.BriefCacheWriter
	launched *launchInFlight
	out      io.Writer
	warn     io.Writer
	// wake is where a cycle waits for the next one. Production waits on the
	// node's own stream and the interval; a test replaces it with one that
	// ends the loop, which is the only way to run this loop deterministically.
	wake func(context.Context) error
}

// start is the whole life of the daemon: converge nothing, learn whether an
// update is due, take the lease, and then tick until something stops it.
func (d *orchestratorDaemon) start(ctx context.Context) error {
	if due, err := d.updateDue(ctx); err != nil {
		return err
	} else if due {
		return d.holdForUpdate(ctx)
	}
	state, err := d.lease.Acquire(ctx)
	if err != nil {
		return dbErr(d.lc, err, false)
	}
	if !state.Held {
		return userErr("orchestrator %s is driven by another daemon right now (holder %s, last beat %s): stop it, or wait for its lease to expire, before running this one",
			safetext.Field(d.id), safetext.Field(state.Holder), state.At.UTC().Format(time.RFC3339))
	}
	if err := d.migrate(ctx); err != nil {
		return err
	}
	tick, err := d.newTick(ctx)
	if err != nil {
		return err
	}
	d.tick = tick
	fmt.Fprintf(d.out, "orchestrator %s: driving it as %s, one cycle every %s\n", safetext.Field(d.id), safetext.Field(d.holder), d.interval)
	return d.loop(ctx)
}

// stateVersion is the schema_version the state node carries, and whether the
// node exists at all. It is a read and decides nothing: the two things the
// daemon's start-up asks about a node are asked of the same reading.
func (d *orchestratorDaemon) stateVersion(ctx context.Context) (int, bool, error) {
	var raw json.RawMessage
	_, found, err := d.db.GetETag(ctx, d.node.Path(), &raw)
	if err != nil {
		return 0, false, dbErr(d.lc, err, false)
	}
	if !found {
		return 0, false, nil
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, false, userErr("the state node at %s is not a document this release reads: %v", d.node.Path(), err)
	}
	return doc.SchemaVersion, true, nil
}

// updateDue reports whether the state node was written by a newer release, and
// says so when it was: the daemon then drives nothing, claims nothing and
// writes nothing (G8). It is asked before the lease is taken, because a daemon
// that is not going to drive the id must not claim it even for a moment: a
// state node this release does not read is the person's cue to run `fugaro
// update`, and nothing here touches it.
func (d *orchestratorDaemon) updateDue(ctx context.Context) (bool, error) {
	v, found, err := d.stateVersion(ctx)
	if err != nil || !found {
		return false, err
	}
	if v <= orchestrator.SchemaVersion {
		return false, nil
	}
	// What the report promises next depends on who would start this process
	// again: a unit is written to start a daemon after any exit, so there the
	// report is not the end of anything, and holdForUpdate says what happens
	// instead.
	ending := "This daemon is stopping; the running one was not touched."
	if d.binary != "" && !d.once {
		ending = "This daemon is holding instead of exiting, because the unit starts it again after any exit: it claims nothing and drives nothing, and it exits only once the binary the unit names has moved on."
	}
	fmt.Fprintf(d.out, "orchestrator %s: the state node is at schema_version %d and this binary reads %d, so an update is due: it was written by a newer release, and nothing is started until this machine runs `fugaro update`. %s\n",
		safetext.Field(d.id), v, orchestrator.SchemaVersion, ending)
	return true, nil
}

// holdForUpdate is what a daemon does with an update due while a unit runs it:
// nothing at all, for as long as it takes.
//
// It must not exit. The unit is written with KeepAlive on launchd and
// Restart=always on systemd (O9), so a clean exit does not stop anything: it is
// the same old binary started again, printing the same line, for ever — and
// the report that announces a stop would be a lie. What ends the hold is the
// one thing that can help: the binary at the path the unit names moving on,
// which is what `fugaro update` does. This process then exits cleanly, so the
// unit starts the new binary, which can read the node.
func (d *orchestratorDaemon) holdForUpdate(ctx context.Context) error {
	// A foreground run with no --binary is nobody's unit — there is nothing to
	// start it again, so the report is the end of it — and --once is one cycle
	// by its own contract, wherever it is asked for.
	if d.binary == "" || d.once {
		return nil
	}
	for {
		if err := d.wait(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		moved, err := d.handOver(ctx)
		if err != nil {
			fmt.Fprintf(d.warn, "orchestrator %s: the binary at %s could not be read: %s\n", safetext.Field(d.id), safetext.Field(d.binary), safetext.OneLine(err.Error()))
			continue
		}
		if moved {
			return d.stop(ctx)
		}
	}
}

// migrate is the daemon's own start-up convergence of its state node (O0, Q2's
// answer as T7's ruling of 2026-11-05 keeps it): the node is read through the
// store, whose Load applies Migrate, and a node left at an older
// schema_version is written back at this release's by the daemon itself.
// Nothing else converges anything — `fugaro update` no longer touches a node
// under orchestrator/ — and a node at a newer version never gets here: it is
// reported as an update being due and the daemon stops before taking the lease.
//
// The write is fenced by the lease, so this runs after the lease is taken and
// never before it: a migration save by a daemon that holds no lease is refused,
// and that refusal is what keeps two machines from migrating one node at once.
func (d *orchestratorDaemon) migrate(ctx context.Context) error {
	v, found, err := d.stateVersion(ctx)
	if err != nil || !found || v == orchestrator.SchemaVersion {
		return err
	}
	s, etag, err := d.node.Load(ctx)
	if err != nil {
		return dbErr(d.lc, err, false)
	}
	if err := d.node.Save(ctx, s, etag); err != nil {
		return dbErr(d.lc, err, false)
	}
	fmt.Fprintf(d.out, "orchestrator %s: the state node was at schema_version %d and is now written at %d\n",
		safetext.Field(d.id), v, s.SchemaVersion)
	return nil
}

// newTick builds the one tick this daemon runs every cycle: the node (with the
// lease that fences its writes), the runs the state recorded, the launcher
// that starts attempts over `fugaro run`'s path, and the guardrails the
// project's layers set.
func (d *orchestratorDaemon) newTick(ctx context.Context) (*orchestrator.Tick, error) {
	caps, err := d.caps(ctx)
	if err != nil {
		return nil, err
	}
	return &orchestrator.Tick{
		Store:    d.node,
		Runs:     &orchestratorRunView{bucket: d.env.bucket.Bucket},
		Launcher: &orchestratorLauncher{env: d.env, stderr: d.warn, started: d.launched},
		Mint:     func(now time.Time) (string, error) { return task.NewRunID(now, crand.Reader) },
		Caps:     caps,
		Breaker:  &orchestrator.Breaker{},
		Now:      d.db.ServerNow,
	}, nil
}

// caps are the daemon's guardrails: the project's orchestrator block with the
// values Fugaro ships filled in, and the kill switch. The switch is read here
// rather than per launch because it is what stops the daemon launching
// anything at all, and a switch that cannot be read is ON: a daemon that
// cannot tell is a daemon that starts nothing.
func (d *orchestratorDaemon) caps(ctx context.Context) (orchestrator.Caps, error) {
	published, _, err := projectPolicyRead(ctx, d.lc)
	if err != nil {
		return orchestrator.Caps{}, err
	}
	var pub *policy.Orchestrator
	if published != nil {
		pub = published.Orchestrator
	}
	blk := policy.Orchestrator{}
	if eff := d.lc.OrchestratorEffective(pub); eff != nil {
		blk = *eff
	}
	blk = blk.WithDefaults(int64(d.lc.MaxParallel))
	on := true
	var kg budget.Kill
	found, err := d.db.Get(ctx, budget.PathKillGlobal, &kg)
	if err == nil {
		on = found && kg.On
	}
	return orchestrator.Caps{
		MaxRunning:     int(blk.MaxRunning),
		MaxPerCycle:    int(blk.BatchSize),
		SpawnPerMinute: int(blk.SpawnPerMinute),
		SpawnPerHour:   int(blk.SpawnPerHour),
		SpawnPerDay:    int(blk.SpawnPerDay),
		KillSwitch:     on,
	}, nil
}

// loop is the daemon's life after start-up: beat, tick, write the cache, check
// the unit's binary, wait.
//
// Every failure is the cycle's and not the daemon's, with two exceptions: a
// lease that went to another machine stops it (Beat, and the tick's own write
// behind Node.Save), and a handover stops it cleanly.
func (d *orchestratorDaemon) loop(ctx context.Context) error {
	for {
		if err := d.lease.Beat(ctx); err != nil {
			if errors.Is(err, orchestrator.ErrLeaseLost) {
				fmt.Fprintf(d.out, "orchestrator %s: the lease went to another daemon, so this one is stopping without touching the node\n", safetext.Field(d.id))
				return nil
			}
			return dbErr(d.lc, err, false)
		}
		rep, err := orchestrator.StepWithCache(ctx, d.tick, d.cache)
		d.report(rep, err)
		d.rearmBreaker(ctx)
		if d.once {
			return d.oneCycle(ctx)
		}
		if err != nil {
			if errors.Is(err, orchestrator.ErrLeaseLost) {
				fmt.Fprintf(d.out, "orchestrator %s: the lease went to another daemon while a cycle was writing, so this one is stopping\n", safetext.Field(d.id))
				return nil
			}
			if !errors.Is(err, orchestrator.ErrStateMoved) {
				fmt.Fprintf(d.warn, "orchestrator %s: a cycle failed: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
			}
		}
		if moved, err := d.handOver(ctx); err != nil {
			fmt.Fprintf(d.warn, "orchestrator %s: the binary at %s could not be read: %s\n", safetext.Field(d.id), safetext.Field(d.binary), safetext.OneLine(err.Error()))
		} else if moved {
			return d.stop(ctx)
		}
		if err := d.wait(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// handOver reports whether the binary the unit names has moved on from this
// process, which is what makes the daemon exit 0 with the lease released so
// the service manager starts the new one.
//
// A run with no --binary has no unit behind it and nothing that would restart
// it, so it checks nothing. A Launch in progress skips the check and never a
// run merely in flight: a tick that handed over mid-launch would abandon a
// start whose intent is already on the node.
func (d *orchestratorDaemon) handOver(ctx context.Context) (bool, error) {
	if d.binary == "" || d.launched.busy() {
		return false, nil
	}
	return binaryMoved(ctx, d.binary, d.running)
}

// rearmBreaker is the loop's half of the breaker (D1228): the count is this
// process's and the question the trip raised is the node's, so a streak a
// person has answered has to start again here. A breaker that is never
// re-armed is silent from the trip on — a fourth failure in a row reports the
// question the streak already reached, so without this the daemon could never
// ask a second person anything, and the next streak would pause it with
// nothing for anybody to look at.
//
// The node is read only while the streak stands at the threshold, which is the
// one state the answer can change: any other cycle has already settled the
// count by its own outcome. A read that fails is logged and leaves the streak
// where it is; the next cycle tries again.
func (d *orchestratorDaemon) rearmBreaker(ctx context.Context) {
	if d.tick == nil || d.tick.Breaker == nil || !d.tick.Breaker.Tripped() {
		return
	}
	s, _, err := d.node.Load(ctx)
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the breaker could not be re-armed: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		return
	}
	if !orchestrator.BreakerOpen(s) {
		d.tick.Breaker.Reset()
	}
}

// oneCycle ends a --once run. The lease is released with the cycle it covered:
// one pass held at a terminal must not keep the orchestrator id from the daemon
// that drives it for the whole TTL.
func (d *orchestratorDaemon) oneCycle(ctx context.Context) error {
	fmt.Fprintf(d.out, "orchestrator %s: one cycle as asked (--once); the lease is released\n", safetext.Field(d.id))
	if _, err := d.lease.Release(ctx); err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the lease could not be released: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
	}
	return nil
}

// stop is the clean exit: the lease is released, so the next daemon starts at
// once rather than waiting out the TTL, and the process ends with status 0.
func (d *orchestratorDaemon) stop(ctx context.Context) error {
	fmt.Fprintf(d.out, "orchestrator %s: the binary at %s is no longer the one this process runs (%s), so this daemon is exiting cleanly for the unit to start the one at that path\n",
		safetext.Field(d.id), safetext.Field(d.binary), safetext.Field(d.running))
	if _, err := d.lease.Release(ctx); err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the lease could not be released: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
	}
	return nil
}

// report prints one cycle's outcome.
func (d *orchestratorDaemon) report(rep orchestrator.Report, err error) {
	if err != nil {
		return
	}
	fmt.Fprintf(d.out, "orchestrator %s: %s (%d folded, %d carried)\n", safetext.Field(d.id), safetext.OneLine(string(rep.Outcome)), rep.Folded, rep.Carried)
}

// wait is the daemon's wait between cycles: the node's own stream — the
// database's SSE, whose first event is a full put of the node as it stands, so
// a change made while the stream was down is never lost — and the interval as
// the ceiling. A stream that ends falls back to the interval alone rather than
// stopping the daemon.
//
// The connection's first event is passed over, because it is not news: the
// cycle that just ran read that same node, and the snapshot is what every
// connection opens with rather than a change. Returning on it would end every
// wait in microseconds and spin the loop at the speed of the interval it never
// honours — and the writes that follow it in the same connection are the
// changes a wake-up is for, so nothing is lost by sitting out the first one.
//
// The stream is cancelled on the way out. It holds a connection, a goroutine
// and a 64KiB scanner buffer for as long as its context lives, and the stream
// of a wait that left its context behind would still be connected to the node
// a day later: one per cycle is a leak with no bound at all.
func (d *orchestratorDaemon) wait(ctx context.Context) error {
	if d.wake != nil {
		return d.wake(ctx)
	}
	timer := time.NewTimer(d.interval)
	defer timer.Stop()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := d.db.Stream(sctx, d.node.Path())
	connected := true
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			snapshot := connected
			connected = false
			switch ev.Type {
			case "put", "patch":
				if snapshot {
					continue
				}
				return nil
			case "keep-alive":
				continue
			}
		case <-timer.C:
			return nil
		}
	}
}

// launchInFlight is the daemon's answer to "is a launch running right now",
// which is the one moment the version check is skipped. It is a counter rather
// than a bool so that two launches of one cycle both have to end before the
// daemon hands over.
type launchInFlight struct {
	mu sync.Mutex
	n  int
}

func (f *launchInFlight) begin() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
}

func (f *launchInFlight) done() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n > 0 {
		f.n--
	}
}

func (f *launchInFlight) busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n > 0
}

// orchestratorRunView is the daemon's read-only view of the runs the state
// recorded (RunViewReader, O7): the run ids the intents carry, looked up in the
// runs bucket, and nothing else.
//
// A run the bucket does not know is reported as one that never started, which
// is what recovery acts on; a run whose task is stored but whose launch never
// happened is the same answer, because launch.json is what says an execution
// was started. A run that started and has not written result.json carries no
// outcome and leaves its item running, which is a cycle that is still going and
// not an item nobody can move again.
type orchestratorRunView struct {
	bucket *blob.Bucket
}

func (v *orchestratorRunView) Runs(ctx context.Context, runIDs []string) ([]orchestrator.RunReport, error) {
	reports := make([]orchestrator.RunReport, 0, len(runIDs))
	for _, id := range runIDs {
		slug, run, err := runstore.Locate(ctx, v.bucket, id)
		if err != nil {
			if errors.Is(err, runstore.ErrNotFound) {
				reports = append(reports, orchestrator.RunReport{RunID: id})
				continue
			}
			return nil, err
		}
		s := runstore.Open(v.bucket, slug, run)
		rep := orchestrator.RunReport{RunID: id}
		if _, err := s.ReadLaunch(ctx); err != nil {
			if !errors.Is(err, runstore.ErrNotFound) {
				return nil, err
			}
		} else {
			rep.Started = true
		}
		if rec, err := s.ReadRecord(ctx); err != nil {
			if !errors.Is(err, runstore.ErrNotFound) {
				return nil, err
			}
		} else {
			rep.Finished, rep.Outcome = true, orchestratorOutcome(rec)
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

// orchestratorOutcome is what one finished run means to the state: the run
// record's status in the state's own vocabulary.
//
// A record this release cannot place — a status the runner has not written, or
// one from a newer release — is a failure, which is the safe direction: the
// attempt is over and the item does not stay running for ever. It is not the
// direction that costs anything either: a failed attempt is the one a person
// looks at.
func orchestratorOutcome(rec *runstore.Record) orchestrator.Outcome {
	switch rec.Status {
	case runstore.StatusSucceeded:
		return orchestrator.OutcomeSucceeded
	case runstore.StatusCancelled:
		return orchestrator.OutcomeCancelled
	}
	return orchestrator.OutcomeFailed
}
