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

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/decide"
	"github.com/dimipaun/fugaro/internal/orchestrator/endpoint"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/versionstamp"
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
	// name is --name: which of the project's orchestrators this daemon drives
	// (D3206, D3207). Empty is the project's own, which is the id the login unit
	// `fugaro setup` and `fugaro update` write starts, and a name is one the unit
	// `fugaro orchestrator install --name` wrote starts: the two halves of the id
	// are the selection here and the flag there.
	name string
	// binary is the stable path the unit names, and the path whose version is
	// checked each tick. Empty is a foreground run with no unit behind it:
	// nothing restarts it, so nothing is checked.
	binary string
	// interval is how long a cycle waits when the node says nothing moved.
	// The stream is the other half of the wait, so this is the ceiling on how
	// stale a tick can be, not the usual delay. A wait beats the lease while it
	// runs, so the interval is not what keeps the claim fresh; it is still
	// capped at the claim's own life (orchestrator.LeaseTTL), because a daemon
	// that holds an id and beats it between cycles further apart than the claim
	// is worth is not a cadence this command offers.
	interval time.Duration
	// once is one cycle and exit. It is what a person (or a harness that holds
	// every command to returning with its stdin closed) can run: the lease is
	// taken for that cycle and released with it, so a single pass never keeps
	// the id from the daemon that drives it.
	once bool
	// decide is --decide: whether this daemon wakes the decision agent on the
	// judgement cases the spec's table names (O13). It is off by default,
	// because a judgement is a fresh child on the person's own Claude login
	// (D1228) and a daemon nobody asked to judge anything makes no model call.
	decide bool
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
			"it running (fugaro setup installs it).\n\n" +
			"One process drives one orchestrator: the project's own unless --name\n" +
			"names one of its named ones, whose id is `<project>~<name>`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorRun(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.StringVar(&o.binary, "binary", "", "the stable path of the fugaro binary the login unit names: the daemon exits cleanly when the version there differs from this process")
	f.DurationVar(&o.interval, "interval", 30*time.Second, "how long to wait between cycles when the node says nothing moved")
	f.BoolVar(&o.once, "once", false, "run one cycle and exit, releasing the lease, instead of driving the id until it is stopped")
	f.BoolVar(&o.decide, "decide", false, "wake the decision agent on the judgement cases code cannot decide (a run out of retries, an attempt that never started, a result no phase reads): each judgement is a model call on your own Claude login")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

func runOrchestratorRun(cmd *cobra.Command, o *orchestratorRunOptions) error {
	ctx := cmd.Context()
	if o.interval <= 0 {
		return userErr("--interval takes a positive duration such as 30s, and %s is not one", o.interval)
	}
	// The wait beats the lease while it runs, on the lease's own cadence and
	// not on this flag, so the claim is never left to lapse between cycles.
	// What the bound refuses is the cadence itself: a daemon that holds an
	// orchestrator id and drives it with cycles further apart than the claim is
	// worth is not a shape this command offers, and a value it cannot serve is
	// refused rather than quietly shortened.
	if o.interval > orchestrator.LeaseTTL {
		return userErr("--interval takes a wait no longer than the lease this daemon holds (%s), and %s is longer", orchestrator.LeaseTTL, o.interval)
	}
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	id, err := orchestratorIDFor(lc, o.name)
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
	// Who drives the id is the person whose credential takes this lease (D3218,
	// F18): the driver node names a principal, and the console's Coordinator tab
	// draws it. A machine that cannot say who it is — no user in the local
	// config and no git identity — writes no driver node rather than a wrong
	// one, which the map reads as unknown, exactly as it is.
	driver, meErr := lc.Me(ctx)
	if meErr != nil {
		driver = ""
	}
	lease, err := orchestrator.NewLease(db, id, holder, driver, db.ServerNow)
	if err != nil {
		return userErr("%v", err)
	}
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator, Lease: lease})
	if err != nil {
		return userErr("%v", err)
	}
	d := &orchestratorDaemon{
		lc: lc, id: id, db: db, env: env, node: node, lease: lease, holder: holder,
		interval: o.interval, beat: orchestrator.LeaseBeat,
		binary: o.binary, running: Version, once: o.once, decide: o.decide,
		// The required-CLI stamp is read through the CLI's one seam, over the
		// project's own database, exactly as the root fence and `update`'s door
		// read it (T1, T6): the daemon is exempt from the root refusal and holds
		// here instead, because a unit starts it again after any exit (R7).
		stamp:    newStampSource(versionstamp.Options{Open: stampOpen, Getenv: os.Getenv}),
		out:      cmd.OutOrStdout(),
		warn:     cmd.ErrOrStderr(),
		launched: &launchInFlight{},
		// The endpoint's registry goes under the user's state directory, read
		// from the process's own environment the way the cached brief is.
		registryGetenv: os.Getenv, registryGOOS: runtime.GOOS,
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
	// beat is how often the wait refreshes the claim while it runs. It is the
	// lease's own cadence (orchestrator.LeaseBeat) and never a flag: a value
	// below it is a claim that lapses between beats, and the person's own knob
	// is the interval.
	beat time.Duration
	// stamp is the reader of the project's required CLI (T1), which the daemon
	// reads at start and at every tick (R7): this command is exempt from the
	// root fence, and holds below the stamp instead of refusing. A nil one is a
	// daemon with no fence at all, which is what a test that is not about the
	// stamp builds — the same nil-tolerance d.wake, d.endpoint and
	// d.registryGetenv have.
	stamp versionstamp.Source
	// once is --once: one cycle, then the clean exit, and decide is --decide:
	// whether the tick this daemon builds reaches the decision agent at all.
	once     bool
	decide   bool
	tick     *orchestrator.Tick
	cache    *orchestrator.BriefCacheWriter
	launched *launchInFlight
	// launcher is the launcher the tick starts runs through, kept here because
	// the list of nodes it finds an item in moves with the plans (nodes,
	// refreshNodes): the tick is built once and the launcher inside it must be
	// handed the node a plan imported after that.
	launcher *orchestratorLauncher
	// nodes are the nodes beside the state node this daemon works: the standing
	// backlog's, and one per plan the project has imported (D2735). They are
	// read and written by the cycle (Tick.Nodes) and given to the launcher, and
	// they are rebuilt every cycle because a plan the project has just imported
	// is a plan this daemon has to work on its next wake rather than at its
	// next start (refreshNodes).
	nodes []orchestrator.Store
	// orchestratorOff is the project's own switch as this daemon last read it
	// (orchestrator_enabled, D2701, D2739): true while the project has turned
	// its orchestrator off. It is kept across a caps read that failed — the caps
	// themselves are the caller's to keep (refreshCaps), and a reading that
	// survived the failure is what makes a failed read keep the last value read
	// instead of turning the project's orchestrator back on — and it is what the
	// once-per-off line is said from (noteOrchestratorSwitch).
	orchestratorOff bool
	out             io.Writer
	warn            io.Writer
	// outputLock is what keeps the two a single stream once the local endpoint
	// is up: see sealOutput.
	outputLock *sync.Mutex
	// endpoint is the local endpoint this daemon serves and the registry file
	// that tells a reader on this machine where it is, nil while none is
	// running: the daemon is the process that starts and stops it (CG13b).
	endpoint *servedEndpoint
	// feed is the last tick the daemon left, which is what the endpoint serves
	// (see endpointFeed).
	feed endpointFeed
	// registryGetenv and registryGOOS are where the endpoint's registry file is
	// placed: the user's state directory through localcfg.UserDir, and the
	// platform its fallback is chosen for. They are fields rather than a read
	// of the environment inside the endpoint for the reason the cache writer's
	// are: a test passes its own, so no write here can reach a developer's own
	// state directory. A nil getenv is a daemon that serves no endpoint at all
	// -- runOrchestratorRun always sets one, and a test that is not about the
	// endpoint does not.
	registryGetenv func(string) string
	registryGOOS   string
	// wake is where a cycle waits for the next one. Production waits on the
	// node's own stream and the interval; a test replaces it with one that
	// ends the loop, which is the only way to run this loop deterministically.
	wake func(context.Context) error
	// debounce is how long that wait holds a run-side write for the rest of
	// the burst it came in. Zero is orchestrator.WakeDebounce; a test shortens
	// it, because two seconds of real time per assertion is not a test's to
	// spend.
	debounce time.Duration
}

// sealOutput gives the daemon's two streams one lock, once, for the daemon's
// life.
//
// From the moment the local endpoint is served the warning stream has a second
// writer: the endpoint reports a request it refused from that connection's own
// goroutine, while this daemon writes its lines from its loop (CG13b). An
// io.Writer promises nothing about two goroutines at once, and a test's own
// buffer promises even less. The streams are wrapped here rather than where the
// endpoint is started because the run view and the launcher capture these two
// writers when the first tick is built, before any endpoint exists, and a line
// they write must be serialised against the endpoint's as well.
func (d *orchestratorDaemon) sealOutput() {
	if d.outputLock != nil {
		return
	}
	d.outputLock = new(sync.Mutex)
	d.out = syncWriter{mu: d.outputLock, w: d.out}
	d.warn = syncWriter{mu: d.outputLock, w: d.warn}
}

// syncWriter serialises writes to w, so the daemon's loop and the endpoint's
// connection goroutines can share one stream.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// start is the whole life of the daemon: converge nothing, learn whether an
// update is due, take the lease, and then tick until something stops it.
func (d *orchestratorDaemon) start(ctx context.Context) error {
	d.sealOutput()
	if due, err := d.updateDue(ctx); err != nil {
		return err
	} else if due {
		return d.holdForUpdate(ctx)
	}
	// The stamp is asked before the lease is taken, for the reason updateDue is:
	// a daemon that is going to claim nothing must not hold the id even for a
	// moment (R7). Under a unit this is a hold and not an exit, because the unit
	// starts the same old binary again after any exit; under --once or with no
	// --binary, holdForUpdate returns nil and this daemon exits 0.
	if d.belowRequiredCLI(ctx, false) {
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
		return d.abandon(ctx, err)
	}
	// The nodes beside the state node are built once before the first tick, so
	// that a run this daemon adopts into a plan's node lands in a node the very
	// first cycle reads.
	if err := d.refreshNodes(ctx); err != nil {
		return d.abandon(ctx, err)
	}
	d.adopt(ctx)
	tick, err := d.newTick(ctx)
	if err != nil {
		return d.abandon(ctx, err)
	}
	d.tick = tick
	fmt.Fprintf(d.out, "orchestrator %s: driving it as %s, one cycle every %s\n", safetext.Field(d.id), safetext.Field(d.holder), d.interval)
	d.serve(ctx)
	defer d.unserve(ctx)
	return d.loop(ctx)
}

// endpointStopGrace bounds the endpoint's shutdown, which is a listener and the
// streams on it rather than the daemon's own exit: a reader that has to notice
// a closed connection must not hold the daemon open past this.
const endpointStopGrace = 5 * time.Second

// servedEndpoint is one running endpoint with the registry file that names it.
type servedEndpoint struct {
	srv    *endpoint.Server
	holder *endpoint.Holder
}

// serve starts the local endpoint this daemon answers on and writes its
// registry file, once, at start-up (CG13b).
//
// Nothing here fails the daemon. The endpoint is what a reader on this machine
// prefers, and the node and the session-start cache are what it reads without
// one (T5): a listener that cannot bind, or a state directory that cannot be
// written, is a line in the daemon's output and never a start-up that stops.
// That is also the whole of what `fugaro update` owes this file (F28): the
// daemon creates its own local file, so there is nothing to migrate and no
// operator step, and a machine whose home is read-only drives its id as before.
func (d *orchestratorDaemon) serve(ctx context.Context) {
	if d.registryGetenv == nil {
		return
	}
	srv, err := endpoint.Start(ctx, endpoint.Options{ID: d.id, Snapshot: d.feed.snapshot, Log: func(err error) {
		fmt.Fprintf(d.warn, "orchestrator %s: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
	}})
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the local endpoint was not started: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		return
	}
	holder, err := endpoint.Hold(d.registryGetenv, d.registryGOOS, endpoint.Registry{
		ID: d.id, PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: srv.Nonce(), Started: time.Now().UTC(),
	})
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the local endpoint was not started: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		closeEndpoint(srv, d.warn)
		return
	}
	d.endpoint = &servedEndpoint{srv: srv, holder: holder}
	fmt.Fprintf(d.out, "orchestrator %s: the local endpoint answers on %s for a reader on this machine\n", safetext.Field(d.id), srv.Addr())
}

// unserve is the clean shutdown the daemon already performs, with the
// endpoint's half of it: the registry file goes first, so no reader is told
// about an endpoint that is stopping, and the listener closes after it. A
// failure here is a warning and never an exit code: the process is leaving
// either way, and a file left behind is one the next reader finds dead.
func (d *orchestratorDaemon) unserve(ctx context.Context) {
	e := d.endpoint
	if e == nil {
		return
	}
	d.endpoint = nil
	if err := e.holder.Remove(); err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the local endpoint's registry file was not removed: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
	}
	closeEndpoint(e.srv, d.warn)
}

// closeEndpoint stops a listener that is already serving, with a grace of its
// own rather than the caller's context: one of the exits that reach here is
// that context ending -- Ctrl-C at a terminal, the unit's SIGTERM -- and a
// shutdown the cancellation aborted would leave the port bound by a process
// that is going.
func closeEndpoint(srv *endpoint.Server, warn io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), endpointStopGrace)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		fmt.Fprintf(warn, "the local endpoint did not stop cleanly: %s\n", safetext.OneLine(err.Error()))
	}
}

// endpointFeed is the last tick the daemon left: the state, the server time it
// was read at, and whether a cycle has finished at all. It is what the local
// endpoint serves.
//
// It is held in memory rather than read from the node per request because the
// endpoint exists to save the reader a cloud read: a request that read the
// database would be the cache file with a listener in front of it.
type endpointFeed struct {
	mu     sync.Mutex
	state  orchestrator.State
	at     time.Time
	ticked bool
}

// snapshot is what the endpoint's Options.Snapshot is: the state of the last
// cycle and the server time it was read at, or false until a cycle has finished
// (H3, the endpoint's own 503).
func (f *endpointFeed) snapshot() (orchestrator.State, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.at, f.ticked
}

// publish hands the endpoint the state the cycle just left, so a reader on this
// machine sees the tick the node sees without reading the database itself. It
// is skipped while no endpoint is running, and a read that fails leaves the
// endpoint on the tick before: the next cycle tries again.
func (d *orchestratorDaemon) publish(ctx context.Context) {
	if d.endpoint == nil {
		return
	}
	s, _, err := d.node.Load(ctx)
	if err != nil {
		return
	}
	now, ok := d.db.ServerNow()
	if !ok {
		return
	}
	d.feed.mu.Lock()
	defer d.feed.mu.Unlock()
	d.feed.state, d.feed.at, d.feed.ticked = s, now, true
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

// belowRequiredCLI reports whether the project's required CLI is above this
// binary, and says so when it is: the daemon is exempt from the root refusal
// (R7), so this is where the one line the person reads comes from, and the
// caller is what does the holding.
//
// The read goes through versionstamp.Source, so the cache rule is the CLI's one
// rule — the stamp at start and at every tick, and at most one database read
// per quarter hour (D3263) — and the fail-open rule is the CLI's too: a
// database that cannot be read is one line and a daemon that drives on
// (D3264), and only a stamp that is really above this binary stops it.
//
// A stamp that is present and malformed is the one case a read fails closed
// (T1): the sentence names the node and the one thing that repairs it, and the
// daemon stops on it exactly as it stops on a stamp above it, because a project
// whose fence this binary cannot read is not one to drive.
//
// midRun says which stop this is, so the line the person reads matches it: at
// start nothing is claimed yet, and mid-run the id is going back (the caller
// releases it).
func (d *orchestratorDaemon) belowRequiredCLI(ctx context.Context, midRun bool) bool {
	if d.stamp == nil {
		return false
	}
	res, err := d.stamp.Required(ctx, versionstamp.Query{URL: d.lc.Budget.RTDBURL, NoAuth: d.lc.Endpoints.NoAuth})
	if err != nil {
		d.reportRequiredCLI(safetext.OneLine(err.Error()), midRun)
		return true
	}
	if res.Origin == versionstamp.OriginNone {
		fmt.Fprintf(d.warn, "orchestrator %s: the project's required CLI could not be read, so this daemon drives on: %s\n",
			safetext.Field(d.id), safetext.OneLine(res.Reason))
		return false
	}
	if !res.Found || !versionstamp.Below(res.Stamp, d.running) {
		return false
	}
	d.reportRequiredCLI((&versionstamp.BelowError{Stamp: res.Stamp, Version: d.running}).Error(), midRun)
	return true
}

// reportRequiredCLI is that one line: the refusal sentence — which names the
// required release, the date the installation set it and the brew command, and
// comes from internal/versionstamp and nowhere else — and then what this daemon
// does about it. The ending is updateDue's own choice of words: a unit starts
// this binary again after any exit, so there the report is not the end of
// anything and the daemon says so.
func (d *orchestratorDaemon) reportRequiredCLI(sentence string, midRun bool) {
	ending := "This daemon is stopping, and it claimed nothing."
	switch {
	case d.binary != "" && !d.once:
		ending = "This daemon is holding instead of exiting, because the unit starts it again after any exit: it claims nothing and drives nothing, and it exits only once the binary the unit names has moved on."
	case midRun:
		ending = "This daemon is stopping; the lease is released."
	}
	fmt.Fprintf(d.out, "orchestrator %s: %s %s\n", safetext.Field(d.id), sentence, ending)
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

// adopt is R4's step, run once at start-up (D2702, D2721): the runs already in
// flight are taken into the state as items this daemon folds and counts, and
// approved by nobody.
//
// It is called after the lease is taken and before the first tick, which is the
// order its own contract asks for: it writes only under the lease, and what the
// first cycle reads is a node that already carries the work.
//
// A failure is one line and this daemon starts anyway. Nothing is half-written
// for it to repair — a lister whose read failed leaves adoption writing no item
// at all (Adopt's own rule) — and what it did not adopt is still in flight, so
// the next start adopts it. A start that stopped here would stop the project's
// orchestrator over the one step whose whole effect is to make work it does not
// know about visible.
func (d *orchestratorDaemon) adopt(ctx context.Context) {
	n, err := orchestrator.Adopt(ctx, &orchestratorRunLister{lc: d.lc, env: d.env, warn: d.warn}, d.node)
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the runs already in flight were not adopted (%s); they are still in flight and the next start adopts them\n",
			safetext.Field(d.id), safetext.OneLine(err.Error()))
		return
	}
	if n > 0 {
		fmt.Fprintf(d.out, "orchestrator %s: %d run(s) already in flight were adopted as items of this state\n", safetext.Field(d.id), n)
	}
}

// refreshNodes rebuilds the list of nodes this daemon works beside the state
// node: the standing backlog's and one per plan the project has imported
// (D2735).
//
// It is called once before the first tick and again every cycle, because the
// tick is built once and a plan is imported long after it: a session that
// imports a plan while this daemon runs — which is the whole point of the
// import's own command — writes a node this daemon has to work from its next
// wake and not from its next start. The listing is one shallow read of the
// plans parent (PlanNames), and a name no path of this release can be built for
// is one that read has already left out.
//
// A plan node that was emptied or written by an older release is not skipped
// for being unreadable: the cycle loads every node in this list, and a node
// that cannot be read ends the cycle (tick's load), so a corrupted plan stops
// this daemon with a line naming it rather than being quietly dropped — the
// same answer the state node gets, and the one this daemon's caps can be
// counted over (D2743).
func (d *orchestratorDaemon) refreshNodes(ctx context.Context) error {
	names, err := orchestrator.PlanNames(ctx, d.db, d.id)
	if err != nil {
		return dbErr(d.lc, err, false)
	}
	nodes := make([]orchestrator.Store, 0, 1+len(names))
	backlog, err := d.shardNode(orchestrator.ShardBacklog)
	if err != nil {
		return err
	}
	nodes = append(nodes, backlog)
	for _, name := range names {
		n, err := d.shardNode(orchestrator.PlanShard(name))
		if err != nil {
			return err
		}
		nodes = append(nodes, n)
	}
	d.nodes = nodes
	if d.tick != nil {
		d.tick.Nodes = nodes
	}
	if d.launcher != nil {
		// The launcher reads these for one write only — putting an item whose
		// issue was edited back to queued (requeueBodyChanged) — and the item
		// it was asked to start may stand in the state node as well as in one
		// of these, so the state node is the first of them.
		d.launcher.nodes = append([]orchestrator.Store{d.node}, nodes...)
	}
	return nil
}

// shardNode builds one of the nodes beside the state node, under the lease this
// daemon holds and through the client it holds it through: which node it is, is
// the shard and nothing else.
func (d *orchestratorDaemon) shardNode(shard orchestrator.Shard) (*orchestrator.Node, error) {
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{
		DB: d.db, AgentID: d.id, Role: orchestrator.RoleOrchestrator, Lease: d.lease, Shard: shard,
	})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return node, nil
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
	judge, err := d.decideSeams(ctx)
	if err != nil {
		return nil, err
	}
	log, err := orchestrator.NewAudit(d.db, d.id, d.db.ServerNow)
	if err != nil {
		return nil, err
	}
	issues, err := d.orchestratorIssueSeams(ctx)
	if err != nil {
		return nil, err
	}
	d.launcher = &orchestratorLauncher{
		env: d.env, stderr: d.warn, started: d.launched,
		// The ticket behind an item is read through the installation's
		// credentials service (B9, D2720), and the one write a launch may
		// make under the state — putting an item whose issue was edited back
		// to queued — goes to the node this daemon works, stamped with the
		// server's own clock. A daemon with no node here is one nothing
		// launches from, and the launcher refuses that write rather than
		// making it nowhere.
		//
		// The list is the state node and the nodes beside it (refreshNodes),
		// because the launcher has to find the item it was asked to start in
		// the node it is starting it from.
		issues: issueReadSeam(d),
		nodes:  append([]orchestrator.Store{d.node}, d.nodes...),
		now:    d.db.ServerNow,
	}
	return &orchestrator.Tick{
		Store: d.node,
		// The nodes beside the state node: the standing backlog's and one per
		// imported plan (D2735), which refreshNodes has built before this tick
		// was and rebuilds after it.
		Nodes:    d.nodes,
		Runs:     &orchestratorRunView{bucket: d.env.bucket.Bucket, env: d.env, warn: d.warn},
		Launcher: d.launcher,
		Mint:     func(now time.Time) (string, error) { return task.NewRunID(now, crand.Reader) },
		Caps:     caps,
		Breaker:  &orchestrator.Breaker{},
		// A tick has four things to say — a run it folded ended, it committed
		// a question (O7), it merged under a ceiling that tells somebody
		// (D2030), and it blocked an item on a cycle across orchestrators
		// (D3220) — and the log sink is the channel slice 1 ships, one cleaned
		// line on this daemon's own output, which is where the person running it
		// in the foreground reads what it did (and where the login unit collects
		// it). Left unset, every say() in a cycle returns at its nil check: the
		// daemon would stop a run nobody hears about, pause on a spend question
		// nobody sees, land a merge nobody is told to read, and block an item on
		// a deadlock nobody can break.
		Notifier: orchestrator.LogSink{W: d.out},
		Triage:   orchestratorTriageSeams(d),
		// The labels of the pull requests the runs it launched opened (F25, T7):
		// read back from the runs bucket by run id and written through the
		// installation's credentials service, which is the one holder of the App
		// key (D2439). Empty where this installation records no credentials
		// service, and where it holds no orchestrator block to have a daemon
		// account from (orchestratorLabelSeams); a daemon that has both and
		// cannot mint says so through the client's own refusal rather than
		// labelling nothing quietly.
		Labels: orchestratorLabelSeams(d),
		// The merge read (G3, D2705): the state of the pull request each item
		// waiting on a merge left, read through the same credentials service the
		// labels are (orchestratorMergeSeams), which is what moves such an item
		// to done once the pull request merged and to failed once it closed
		// without merging. Empty where this installation has no service to read
		// through; an item whose pull request is then never read stays in
		// review, where its dependents wait, which is the direction this fails
		// in.
		Merge: orchestratorMergeSeams(d),
		// The issue sync (B4, D2718): the open issues of the repositories the
		// project's `issue_sync` names, ingested into the standing backlog's
		// node through the installation's credentials service, which is the one
		// holder of the App key (D2439) and the only thing here that reads or
		// opens an issue. Empty where the project publishes no block that
		// decides anything, and where this installation records no credentials
		// service or no daemon account to mint a token as
		// (orchestratorIssueSeams); a daemon that has both and cannot read one
		// repository says so through the client's own refusal, once per cadence,
		// rather than ingesting nothing quietly.
		Issues: issues,
		// The cross-orchestrator references (D3208): the items of the other
		// orchestrators this node's own items name, read from the same database
		// the node is and only for the ids a reference names. It is wired
		// always, because the reading is what makes such a reference mean
		// anything: a daemon that resolved none would hold every item that
		// names one, and the reference would be a dependency nothing can ever
		// satisfy.
		Cross: orchestrator.CrossShards{DB: d.db, Role: orchestrator.RoleOrchestrator},
		// The audit log the trip writes its refusal line to (O19): the daemon's
		// own node, under the id the entry derives from the run, which is the
		// same log the triage and the decision agent append to. It is wired
		// here rather than through a feature's seams because the trip is the
		// breaker's and a daemon triages nothing at all when it has no host.
		Audit: log,
		// The decision agent is the one step a daemon makes that a person has
		// to ask for: the seam is empty unless --decide was given, and a tick
		// with none judges nothing however many cases its node carries (O13).
		Decide: judge,
		Now:    d.db.ServerNow,
	}, nil
}

// decideSeams is the decision agent this daemon reaches when it was started
// with --decide (O13), and no seam at all when it was not.
//
// The runner is O12's, whole: the bounds (six calls an hour, forty a day, a
// three-turn call), the brief, the output schema, the deny rules a coding
// agent's session has, and the class-to-model rule are all its own, and nothing
// about them is spelled again here. What this function hands it is the three
// things only the daemon has — the merged policy's models, the audit log the
// reasoning goes to and the fence is read from, and the bucket the evidence
// beside a case is read out of — and the server clock every stamp in a decision
// comes from (D1230).
func (d *orchestratorDaemon) decideSeams(ctx context.Context) (orchestrator.DecideSeams, error) {
	if !d.decide {
		return orchestrator.DecideSeams{}, nil
	}
	blk, err := d.orchestratorBlock(ctx)
	if err != nil {
		return orchestrator.DecideSeams{}, err
	}
	log, err := orchestrator.NewAudit(d.db, d.id, d.db.ServerNow)
	if err != nil {
		return orchestrator.DecideSeams{}, err
	}
	return orchestrator.DecideSeams{
		Agent: &orchestratorDecider{
			runner: &decide.Runner{
				Agent:  decide.Claude{},
				Models: blk.DecisionModelPerClass,
				Audit:  log,
				Now:    d.db.ServerNow,
			},
			bucket: d.env.bucket.Bucket,
			warn:   d.warn,
		},
		Log: &orchestratorDecideLog{db: d.db, id: d.id},
	}, nil
}

// orchestratorDecideLog is the fence O13 reads and O12 writes to: O3's audit
// log, read by the derived id of one case and appended to under the same id.
//
// It is the same node the triage appends to, and the same log: what makes a
// judgement once-per-attempt is the id its subject and transition derive, so a
// repeated cycle reads a line rather than making a second call, and a retry of
// the append writes the same key rather than a second line.
type orchestratorDecideLog struct {
	db *rtdb.Client
	id string
}

// Judged reports whether a case's line is already in the log (DecideLog).
func (l *orchestratorDecideLog) Judged(ctx context.Context, id string) (bool, error) {
	path, err := orchestrator.PathAudit(l.id)
	if err != nil {
		return false, err
	}
	var e orchestrator.Entry
	found, err := l.db.Get(ctx, path+"/"+id, &e)
	if err != nil {
		return false, err
	}
	return found, nil
}

// Append writes one judgement (DecideLog) under the id its subject and its
// transition derive.
func (l *orchestratorDecideLog) Append(ctx context.Context, e orchestrator.Entry) error {
	log, err := orchestrator.NewAudit(l.db, l.id, l.db.ServerNow)
	if err != nil {
		return err
	}
	_, err = log.Append(ctx, e)
	return err
}

// orchestratorDecider is one judgement (Decider, O13): O12's runner, handed the
// case's item, the evidence the run behind it left and the state the cycle
// read.
//
// The evidence is the run's own record — the changed paths O1's classifier
// reads and the reason the run ended with — read here rather than carried on
// the case, because the tick's run view is asked about the attempts that have
// not reported and a judgement is about one that has. Neither half is a
// decision: the paths pick the model by code (D1234) and the reason is text the
// brief fences as untrusted (O12).
type orchestratorDecider struct {
	runner *decide.Runner
	bucket *blob.Bucket
	warn   io.Writer
}

// Decide makes one judgement about one case (Decider).
//
// A refusal is not a failure and is not handed back as one: O12 refuses a call
// the caps do not allow before anything is spent (ErrCallCap), and a daemon
// held back by its own bound is not a daemon that is going wrong (its own
// reading, D1228). Everything else — a class with no model, a child that
// failed, an answer this release cannot read — is the call's own error, which
// the tick reports on the cycle and never raises out of it.
func (a *orchestratorDecider) Decide(ctx context.Context, s orchestrator.State, c orchestrator.DecideCase) (orchestrator.DecideResult, error) {
	paths, failure := a.evidence(ctx, c.Attempt.RunID)
	dec, err := a.runner.Decide(ctx, decide.Request{State: s, ItemID: c.ItemID, Paths: paths, Failure: failure})
	res := orchestrator.DecideResult{Next: dec.Next, Outcome: dec.Outcome, Text: decisionLine(dec)}
	// An answer was read — whatever it asked for, an answer that asks for no
	// change included — and a call that was refused before it was made, or one
	// whose answer this release could not read, is left unfenced so the caps
	// and the breaker count the retries (G10).
	res.Answered = res.Called() && (dec.Outcome == orchestrator.CycleProgress || dec.Outcome == orchestrator.CycleEmpty)
	if errors.Is(err, decide.ErrCallCap) {
		return res, nil
	}
	return res, err
}

// decisionLine is what the audit log carries for one judgement: the class and
// the model it ran as, and what the model reasoned. The log clips it to its own
// bound, and the reasoning is a model's own words about one item — the state,
// not this line, is where the effect of a judgement lives.
func decisionLine(dec decide.Decision) string {
	line := string(dec.Class) + " (" + dec.Model + ")"
	if dec.Reasoning == "" {
		return line
	}
	return line + ": " + dec.Reasoning
}

// evidence is what the run behind one attempt left for a judgement: the changed
// paths O1's classifier reads and the reason its record carries for how the run
// ended.
//
// A path set the record marks truncated is not handed to the classifier at all.
// The class picks the model, and a set cut short is one the classifier can only
// under-read; its own rule is that a change whose paths it could not read is
// Sensitive, which is the direction a judgement fails in (D1234, O1's
// allowlist). A run the bucket does not know, or one whose record cannot be
// read, is the same answer and never a failure: the case is about the item, and
// the agent judges it with what there is.
func (a *orchestratorDecider) evidence(ctx context.Context, runID string) ([]string, string) {
	if runID == "" {
		return nil, ""
	}
	short := task.ShortRun(oneLine(runID))
	slug, run, err := runstore.Locate(ctx, a.bucket, runID)
	if err != nil {
		if !errors.Is(err, runstore.ErrNotFound) {
			fmt.Fprintf(a.warn, "run %s: what a judgement would read of it could not be found: %s\n", short, safetext.OneLine(err.Error()))
		}
		return nil, ""
	}
	rec, err := runstore.Open(a.bucket, slug, run).ReadRecord(ctx)
	if err != nil {
		if !errors.Is(err, runstore.ErrNotFound) {
			fmt.Fprintf(a.warn, "run %s: what a judgement would read of it could not be read: %s\n", short, safetext.OneLine(err.Error()))
		}
		return nil, ""
	}
	if rec == nil {
		return nil, ""
	}
	if files := rec.ChangedFiles.Bounded(); files != nil && !files.Truncated {
		return files.Paths, rec.Reason
	}
	return nil, rec.Reason
}

// orchestratorBlock is O4's orchestrator block as this machine reads it: the
// project's published block, with Fugaro's ceiling over it and this machine's
// own config under that, and every key it still leaves unset filled to its own
// floor (localcfg.OrchestratorEffective, policy.WithDefaults).
//
// The caps and the decision models are two halves of this one block, so one
// function reads it: the daemon's guardrails and the model each class of
// judgement runs on are the same policy, and a second reader spelling the same
// merge is a second place a layer could be left out of.
func (d *orchestratorDaemon) orchestratorBlock(ctx context.Context) (policy.Orchestrator, error) {
	published, _, err := projectPolicyRead(ctx, d.lc)
	if err != nil {
		return policy.Orchestrator{}, err
	}
	return d.orchestratorBlockOf(published), nil
}

// orchestratorBlockOf is that merge over a policy node the caller has already
// read, so that a caller which wants something else out of the same document —
// the caps, which read the project's own switch beside the block (D2701,
// D2739) — makes one read of the node rather than two.
//
// A machine with no published policy has the block its own config states and
// no more: localcfg.OrchestratorEffective merges nothing over nil, and the
// floors below are what a project that never published one runs on.
func (d *orchestratorDaemon) orchestratorBlockOf(published *projectpolicy.Policy) policy.Orchestrator {
	var pub *policy.Orchestrator
	if published != nil {
		pub = published.Orchestrator
	}
	blk := policy.Orchestrator{}
	if eff := d.lc.OrchestratorEffective(pub); eff != nil {
		blk = *eff
	}
	return blk.WithDefaults(int64(d.lc.MaxParallel))
}

// caps are the daemon's guardrails: the project's orchestrator block with the
// values Fugaro ships filled in, and the two switches that stop every start at
// once — the budget's global kill switch and the project's own orchestrator
// switch (D2701, D2739), which the caps carry as one flag (orchestrator.CapsOf).
//
// A switch that cannot be read is ON: a budget node that cannot be read, or one
// that carries no switch, hands true, and a daemon that cannot tell is a daemon
// that starts nothing. The project's switch is read in the same read as the
// block it sits beside, so a policy node that cannot be read fails this whole
// reading, and what the caller does with that is the caller's (refreshCaps
// keeps the caps it had and turns the switch on).
func (d *orchestratorDaemon) caps(ctx context.Context) (orchestrator.Caps, error) {
	published, _, err := projectPolicyRead(ctx, d.lc)
	if err != nil {
		return orchestrator.Caps{}, err
	}
	killOn := true
	var kg budget.Kill
	if found, err := d.db.Get(ctx, budget.PathKillGlobal, &kg); err == nil {
		killOn = found && kg.On
	}
	on := published.OrchestratorOn()
	d.noteOrchestratorSwitch(on)
	return orchestrator.CapsOf(d.orchestratorBlockOf(published), killOn, on), nil
}

// noteOrchestratorSwitch keeps the project's own switch as this daemon last
// read it, and says so the once it goes off.
//
// It is kept and not only folded into the caps because the caps a failed read
// leaves behind have the switch on whatever the project wrote: this reading is
// what survives that, so a failed read never turns the project's orchestrator
// back on — the daemon goes on holding the answer it last got.
//
// The line is said on the transition and not every cycle. A daemon under the
// login unit writes to a log nobody watches, and one sentence repeated every
// interval is a log of one sentence; a daemon a person started reads it as the
// reason its cycles report nothing launched. Coming back on is not announced —
// the cycles' own reports are what say the daemon is launching again — and the
// switch is read every cycle either way.
func (d *orchestratorDaemon) noteOrchestratorSwitch(on bool) {
	off := !on
	was := d.orchestratorOff
	d.orchestratorOff = off
	if !off || was {
		return
	}
	fmt.Fprintf(d.out, "orchestrator %s: the project turned its orchestrator off (orchestrator_enabled: false), so this daemon launches nothing while it is off and goes on folding the runs already going\n", safetext.Field(d.id))
}

// refreshCaps re-reads the daemon's guardrails before every cycle.
//
// They decide whether a cycle plans anything at all, and the kill switch is a
// person's: one turned on while this daemon is running has to stop it, and a
// daemon that read the switch once at start-up would keep committing intents
// and starting runs for the rest of its life with nothing in the way — the
// person's stop would be a stop of the runs already going and of none of the
// ones to come. The project's caps move for the same reason: a limit tightened
// on the policy server is a limit the next cycle is held to.
//
// A guardrail that cannot be read is not a guardrail that is off. The caps the
// daemon already has are kept, with the switch on: what a failed read costs is
// one cycle's launches, and the next cycle reads again.
func (d *orchestratorDaemon) refreshCaps(ctx context.Context) {
	caps, err := d.caps(ctx)
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the guardrails could not be read, so this cycle starts nothing: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		caps = d.tick.Caps
		caps.KillSwitch = true
	}
	d.tick.Caps = caps
}

// loop is the daemon's life after start-up: beat, tick, write the cache, check
// the unit's binary, wait.
//
// A failure inside a cycle is the cycle's and not the daemon's: it is reported,
// the wait runs, and the next cycle is the next try. The daemon's own beat is
// the one place that is not true, because the beat is the lease and a lease
// failure has one answer: a claim that went to another machine stops this one
// without touching the node (and so does the tick's own write behind Node.Save),
// and a beat that failed for any other reason — a database that answered an
// error — is the process's failure, the unit's restart being the answer to it. A
// handover stops it cleanly; so does the context ending — Ctrl-C or the unit's
// SIGTERM. Each of those exits releases the lease on the way out, and so does
// the failed beat above, rather than leaving the id claimed by a process that
// is gone; the one exit that does not is the lost lease, where the claim is
// somebody else's.
func (d *orchestratorDaemon) loop(ctx context.Context) error {
	for {
		if err := d.lease.Beat(ctx); err != nil {
			if ctx.Err() != nil {
				return d.cancelled(ctx)
			}
			if d.once {
				// One pass by its own contract, and a pass that could not take
				// its lease ran nothing: exit 0 would tell the caller it did.
				return d.oneCycle(ctx, err)
			}
			if errors.Is(err, orchestrator.ErrLeaseLost) {
				fmt.Fprintf(d.out, "orchestrator %s: the lease went to another daemon, so this one is stopping without touching the node\n", safetext.Field(d.id))
				return nil
			}
			// The id goes back before the error does. The process is leaving
			// with a claim of its own on the node, and the unit restarts it at
			// once: a fresh claim refuses that restart's own Acquire ("driven
			// by another daemon") for the whole TTL, so a restart loop faster
			// than the TTL — systemd's RestartSec is 100ms — is a unit that
			// gives up for good over one refused write.
			d.release(ctx)
			return dbErr(d.lc, err, false)
		}
		// The stamp is read again every cycle, through the cache (R7): an admin's
		// `fugaro update` raises it under a running daemon, and this one stops
		// claiming and driving rather than act on an installation that has moved
		// past it. The id goes back the way abandon gives it back, and what this
		// daemon does next is what it does below the stamp at start: it holds
		// under a unit and exits 0 under --once or with no --binary.
		//
		// The local endpoint stops before the hold and not with the process: a
		// hold under a unit does not end — it lasts until the binary the unit
		// names moves on — and a reader on this machine would go on being
		// answered from a feed no cycle refreshes, which is the one thing a
		// daemon that has stopped driving owes nobody (R7).
		if d.belowRequiredCLI(ctx, true) {
			d.release(ctx)
			d.unserve(ctx)
			return d.holdForUpdate(ctx)
		}
		d.refreshCaps(ctx)
		// The plans are listed again every cycle: a plan imported while this
		// daemon runs is a node it has to work from this wake (refreshNodes).
		// A listing that failed leaves the nodes as the last one read them —
		// the tick works what it has — and says so, because a daemon quietly
		// working a stale set of plans is one nobody can see is not working the
		// project.
		if err := d.refreshNodes(ctx); err != nil {
			fmt.Fprintf(d.warn, "orchestrator %s: the plans could not be listed, so this cycle works the nodes it worked last: %s\n",
				safetext.Field(d.id), safetext.OneLine(err.Error()))
		}
		rep, err := orchestrator.StepWithCache(ctx, d.tick, d.cache)
		d.report(rep, err)
		d.publish(ctx)
		d.rearmBreaker(ctx)
		if d.once {
			return d.oneCycle(ctx, err)
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
				return d.cancelled(ctx)
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
//
// The read is the pause's own (orchestrator.BreakerOpen) and so it is made
// against the server's clock: a question a workspace's refusal raised lifts
// itself after its window (O19), and a streak that stands at the threshold
// while such a question has lapsed has to be re-armed like any other — without
// that, the one pause a cycle cannot lift would leave this process silent for
// the rest of its life. A clock that cannot be read leaves the streak where it
// is, which is the direction that keeps the guard.
func (d *orchestratorDaemon) rearmBreaker(ctx context.Context) {
	if d.tick == nil || d.tick.Breaker == nil || !d.tick.Breaker.Tripped() {
		return
	}
	now, ok := d.tick.Now()
	if !ok {
		return
	}
	s, _, err := d.node.Load(ctx)
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the breaker could not be re-armed: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		return
	}
	if !orchestrator.BreakerOpen(s, now) {
		d.tick.Breaker.Reset()
	}
}

// abandon is the daemon leaving after it has taken the lease and before it has
// anything to drive with. The id goes back with the failure: a lease left
// behind by a start-up that failed is not a daemon driving anything, and it
// keeps every other daemon — the unit's next attempt, a person's `--once` —
// out for the whole TTL.
func (d *orchestratorDaemon) abandon(ctx context.Context, err error) error {
	d.release(ctx)
	return err
}

// orchestratorReleaseGrace is how long a release waits for its write. It is
// short on purpose: there is nothing left to do with a lease whose release is
// still hanging, and a person pressing Ctrl-C twice must not have to wait for
// the second press.
const orchestratorReleaseGrace = 5 * time.Second

// release gives the id back, and a lease that could not be released is a
// warning and not a failure: the TTL ends it, and the exits that call this (a
// clean stop, a cancelled run, a --once pass) have already decided what they
// are.
//
// The write gets a grace of its own rather than the caller's context, because
// two of those exits arrive as that context ending — Ctrl-C at a terminal, the
// unit's SIGTERM — and a release the cancellation aborted would leave a process
// that is gone holding the id for the whole TTL.
func (d *orchestratorDaemon) release(ctx context.Context) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orchestratorReleaseGrace)
	defer cancel()
	if _, err := d.lease.Release(rctx); err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the lease could not be released: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
	}
}

// oneCycle ends a --once run. The lease is released with the cycle it covered:
// one pass held at a terminal must not keep the orchestrator id from the daemon
// that drives it for the whole TTL.
//
// A pass that failed is the process's failure. `--once` is the one cycle a
// person or a harness holds the daemon to, and its exit code is what says
// whether that cycle ran: a budget refusal, a lease that went to another
// machine or an unreadable node that exited 0 would read as one cycle done.
// The lease is still released first, and the line still only prints for a
// cycle that ran.
func (d *orchestratorDaemon) oneCycle(ctx context.Context, cycleErr error) error {
	if cycleErr == nil {
		fmt.Fprintf(d.out, "orchestrator %s: one cycle as asked (--once); the lease is released\n", safetext.Field(d.id))
	}
	d.release(ctx)
	return cycleErr
}

// cancelled is the daemon stopped from outside: Ctrl-C at a terminal, the
// unit's SIGTERM at a shutdown. It is a clean exit like the handover's, and the
// lease goes back with it — a process that exited and left its claim behind
// keeps the next daemon, the unit's own restart and a person's `--once`, out
// for the whole TTL with a holder that is not there.
//
// The release itself is made with a grace rather than with the context that
// just ended (release), so the line below is the truth even here.
func (d *orchestratorDaemon) cancelled(ctx context.Context) error {
	fmt.Fprintf(d.out, "orchestrator %s: stopping where the context ended; the lease is released\n", safetext.Field(d.id))
	d.release(ctx)
	return nil
}

// stop is the clean exit: the lease is released, so the next daemon starts at
// once rather than waiting out the TTL, and the process ends with status 0.
func (d *orchestratorDaemon) stop(ctx context.Context) error {
	fmt.Fprintf(d.out, "orchestrator %s: the binary at %s is no longer the one this process runs (%s), so this daemon is exiting cleanly for the unit to start the one at that path\n",
		safetext.Field(d.id), safetext.Field(d.binary), safetext.Field(d.running))
	d.release(ctx)
	return nil
}

// report prints one cycle's outcome.
func (d *orchestratorDaemon) report(rep orchestrator.Report, err error) {
	if err != nil {
		return
	}
	fmt.Fprintf(d.out, "orchestrator %s: %s (%d folded, %d carried, %d judged)\n", safetext.Field(d.id), safetext.OneLine(string(rep.Outcome)), rep.Folded, rep.Carried, rep.Decided)
	// A judgement that failed is said and not raised: the cycle it was part of
	// still folded its runs and still carries its items, and what a person
	// reads here is that one case went unanswered — the fence was not written,
	// so the next cycle offers the same case to the agent again.
	if rep.DecideErr != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: a judgement could not be made: %s\n", safetext.Field(d.id), safetext.OneLine(rep.DecideErr.Error()))
	}
}

// wait is the daemon's wait between cycles: the streams of every node a run
// writes outside the orchestrator subtree — `agents/<slug>` for each
// repository the state names and the day's `outcomes` node (E1, D2708) — plus
// the daemon's own state node, and the interval as the ceiling.
//
// A run's end is what the run-side streams are for. Without them the daemon
// learned of one at its next cycle, and the interval is a person's flag rather
// than the delay a run's end deserves: a run that finishes a second after a
// cycle waited the whole of it. What wakes and what does not — an entry's
// creation or deletion under `agents/` and never the heartbeat every run in
// flight writes, any write under `outcomes/`, and the two-second debounce that
// makes one run's end one cycle — is the waker's own (orchestrator.Wakes,
// internal/orchestrator/wake.go), which is also where the runs whose registry
// entry vanished with no outcome are named.
//
// Every stream is cancelled on the way out. Each holds a connection, a
// goroutine and a 64KiB scanner buffer for as long as its context lives, and a
// wait that left its context behind would still be connected to the node a day
// later: one set per cycle is a leak with no bound at all.
//
// The ceiling is the interval, and it is what a stream that fails falls back
// to rather than stopping the daemon: the database's client reconnects with a
// fresh snapshot of the node, and the cycle the ceiling then runs reads what
// the outage missed. Nothing wakes on a snapshot — not the first connection's
// and not a reconnect's — because it is the node as it already stands and not a
// change; the waker passes over each one it reads.
//
// The wait beats the claim while it runs, on the lease's own cadence. The
// interval is a person's value and the claim expires at LeaseTTL, so a wait
// that honoured the flag alone would leave the id un-beaten for a whole
// interval: an interval a little under the TTL is a claim that lapses under the
// daemon holding it, and a second daemon takes that id while this one is still
// driving it. A beat that failed is not answered here — it ends the wait, and
// the loop's own Beat at the top of the cycle decides it, because a lease
// failure has one answer and it is not this function's (a claim that went to
// another machine stops the daemon, a database error is the process's).
func (d *orchestratorDaemon) wait(ctx context.Context) error {
	if d.wake != nil {
		return d.wake(ctx)
	}
	timer := time.NewTimer(d.interval)
	defer timer.Stop()
	// A wait no longer than the lease's own beat needs no ticker: the loop's
	// Beat at the top of the next cycle is that beat, which is also what keeps
	// the default interval — the beat itself — from beating twice a cycle.
	var beats <-chan time.Time
	if d.beat > 0 && d.beat < d.interval {
		ticker := time.NewTicker(d.beat)
		defer ticker.Stop()
		beats = ticker.C
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wakes := orchestrator.NewWakes(d.wakeOptions(ctx)).Run(sctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case w, ok := <-wakes:
			// A waker that closed its channel heard nothing more for good:
			// the wait keeps its ceiling, which is the cycle that catches up.
			if !ok {
				wakes = nil
				continue
			}
			d.noteWake(w)
			return nil
		case <-beats:
			if err := d.lease.Beat(ctx); err != nil {
				return nil
			}
		case <-timer.C:
			return nil
		}
	}
}

// wakeOptions is the run-side half of this wait: the daemon's own node, one
// `agents/<slug>` per repository the state names, and the day's outcomes node.
//
// The repositories come from the state the cycle just read, and a state that
// cannot be read costs wakes rather than the wait: without its slugs the daemon
// still hears every run's end on the outcomes node, and what it would miss is
// the entry that vanished without one.
//
// The day is the database's own clock where it has one, which is the clock
// every stamp under this subtree comes from (D1230), and the local clock only
// until the first response has been read.
func (d *orchestratorDaemon) wakeOptions(ctx context.Context) orchestrator.WakeOptions {
	// The daemon's own nodes are the state node and the nodes beside it
	// (D2735): a plan imported while this daemon runs, and an item a person adds
	// to the standing backlog, are writes to one of these, and this stream is
	// what brings either to the next cycle instead of the next interval. Both
	// paths are built from the id the state node's own path was built from and
	// cannot fail here; a path that somehow did is left out rather than streamed
	// as the empty string, which would be the whole database.
	own := []string{d.node.Path()}
	if p, err := orchestrator.PathPlans(d.id); err == nil {
		own = append(own, p)
	}
	if p, err := orchestrator.PathBacklog(d.id); err == nil {
		own = append(own, p)
	}
	o := orchestrator.WakeOptions{
		DB:  d.db,
		Own: own,
		Now: func() time.Time {
			if t, ok := d.db.ServerNow(); ok {
				return t
			}
			return time.Now()
		},
		Log: func(err error) {
			fmt.Fprintf(d.warn, "orchestrator %s: %s\n", safetext.Field(d.id), safetext.OneLine(err.Error()))
		},
		Debounce: d.debounce,
	}
	slugs, err := d.stateSlugs(ctx)
	if err != nil {
		fmt.Fprintf(d.warn, "orchestrator %s: the repositories the state names could not be read, so this wait hears a run's end on the outcomes node alone: %s\n",
			safetext.Field(d.id), safetext.OneLine(err.Error()))
		return o
	}
	o.Agents = slugs
	return o
}

// stateSlugs is the slug of every repository the state's items name: the
// repositories whose runs write their registry entries where this daemon has to
// listen for them.
//
// The slug is resolved the way the launch resolves it — under the checkoutless
// switch, and from the local config where the working directory cannot answer
// (repoSlug, checkoutOf) — because the node a run's entry lands on is named by
// the launch's slug and not by a second rule kept here. A repository whose
// provider nothing names has no slug and no stream: its runs' ends are still on
// the outcomes node, which is the half that says a run is over.
func (d *orchestratorDaemon) stateSlugs(ctx context.Context) ([]string, error) {
	s, _, err := d.node.Load(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var slugs []string
	for _, item := range s.WorkItems {
		if item.Repo == "" || seen[item.Repo] {
			continue
		}
		seen[item.Repo] = true
		slug, err := d.env.repoSlug(item.Repo, checkoutOf(withoutCheckout(ctx), item.Repo))
		if err != nil {
			continue
		}
		slugs = append(slugs, slug)
	}
	return slugs, nil
}

// noteWake says what the wake means, and nothing has to be handed on for the
// cycle that follows to act on it: a run whose entry vanished wrote no outcome,
// so its intent is still open, and the cycle — which the tick derives from the
// state it then reads — asks the platform about the execution of every run that
// has not reported (orchestratorRunView.executionOver, D2725). A run whose
// execution the platform has ended is folded over on the state it reported, and
// the run that is still spending is one this cycle leaves alone, exactly as it
// treats every other run still in flight.
func (d *orchestratorDaemon) noteWake(w orchestrator.Wake) {
	for _, ref := range w.Vanished {
		fmt.Fprintf(d.warn, "run %s/%s: its registry entry vanished with no outcome, so this cycle checks its execution\n",
			safetext.Field(ref.Slug), task.ShortRun(oneLine(ref.Run)))
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
// runs bucket, and — for a run that has not reported — the platform's own word
// on the execution behind it.
//
// A run the bucket does not know is reported as one that never started, which
// is what recovery acts on; a run whose task is stored but whose launch never
// happened is the same answer, because launch.json is what says an execution
// was started. A run that started and has not written result.json — its record
// still says running — carries no outcome and leaves its item running, which is
// a cycle that is still going and not an item nobody can move again, as long as
// the platform agrees the run is still going: a run the platform has already
// ended is one that died without writing anything, which executionOver reads.
type orchestratorRunView struct {
	bucket *blob.Bucket
	env    *cloudEnv
	warn   io.Writer
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
		launch, err := s.ReadLaunch(ctx)
		switch {
		case err == nil:
			rep.Started = true
		case !errors.Is(err, runstore.ErrNotFound):
			return nil, err
		}
		rec, err := s.ReadRecord(ctx)
		switch {
		case errors.Is(err, runstore.ErrNotFound):
			rec = nil
		case err != nil:
			return nil, err
		}
		if rec != nil {
			// What the run reported about the graph travels whole and unjudged:
			// the blocks its own agent discovered and the conflict paths it met
			// are what the fold acts on (E3), and the fold bounds both again on
			// the way in (runstore.Discovered.Bounded, BoundedConflictPaths) —
			// a view that trimmed or filtered them here would be the second
			// place that rule lives, and a view that dropped them leaves the
			// fold with nothing to fold in an installation whose tests all
			// pass.
			rep.Discovered = rec.Discovered
			rep.ConflictPaths = rec.ConflictPaths
		}
		if rec != nil && rec.PR != nil && rec.PR.Number > 0 {
			// The pull request the run left, which is the record's own answer
			// (F25) and the whole of what tells a fold of a succeeded run that
			// the item waits on a merge rather than being done (D2705). It is
			// read here and never a second time: the merge read is handed the
			// same number off this same report, because one attempt leaves one
			// pull request and the item it left it for is the item the report
			// is folded into.
			rep.PRNumber = rec.PR.Number
		}
		if rec != nil && rec.Halt != nil {
			// The halt travels whole, and the marker with it: the fold asks the
			// build's seam whether this is a refusal a workspace's credentials
			// service answered, and that rule holds the marker against the halt
			// beside it (O19). Nothing is decided here — a reader that judged
			// the record would be the second place the rule lives.
			rep.Halt = orchestrator.Halt{
				Reason: string(rec.Halt.Reason),
				Scope:  rec.Halt.Scope,
				Detail: rec.Halt.Detail,
			}
			rep.WorkspaceRefusal = rec.Halt.WorkspaceRefusal
		}
		if done, outcome := orchestratorOutcome(rec); done {
			rep.Finished, rep.Outcome = true, outcome
		} else if state, over := v.executionOver(ctx, slug, run, launch); rep.Started && over {
			// The record said the run was still going when it was read, and
			// the platform says the execution is over. The runner may have
			// written its last record in between: result.json is the runner's
			// last act and the execution ends with it, so a run that succeeded
			// a moment after this read is a run whose record says so, and
			// folding the ended execution over the stale read would close a
			// successful attempt as failed — and the same cycle would start a
			// retry under the standing approval, a second execution and a
			// second pull request for work that is already done. So the record
			// is read again, and only a record that still says running (or one
			// that is gone) is the run that died without reporting.
			fresh, err := s.ReadRecord(ctx)
			switch {
			case errors.Is(err, runstore.ErrNotFound):
				fresh = nil
			case err != nil:
				return nil, err
			}
			// The state the platform reported, which is what a run whose
			// record still says running leaves as its only word: a
			// cancellation is a person's own act and never retried, a
			// failure is what a retry is for (executionOver's comment is
			// the why).
			outcome := orchestrator.OutcomeFailed
			if state == backend.StateCancelled {
				outcome = orchestrator.OutcomeCancelled
			}
			if done, o := orchestratorOutcome(fresh); done {
				outcome = o
			}
			rep.Finished, rep.Outcome = true, outcome
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

// executionOver reports whether the platform has ended the run's execution,
// which is the one thing that can tell a run still saying it is going that it
// is not.
//
// A container the platform OOM-killed, one that hit the job's timeout, one
// whose start-up failed: the runner inside it never got to write another record
// (result.json is the runner's last act, and a runner that was killed never
// makes it), so the record says running for ever. Left at that, the attempt
// stays open for good: recovery skips a run that started, the plan passes over
// an item with an attempt that has not reported, the breaker reads the whole
// node as in flight — so the daemon does nothing but wait on a run that is not
// there, and a person cannot close the intent for it (an intent is the daemon's
// to write, and a person's upsert carrying one is refused). A run the platform
// has ended is over, and an attempt that ended without reporting is folded on
// the state the platform reported for it.
//
// Only an execution the platform reports as ended counts. One it no longer has
// (Cloud Run keeps them 30 days) and one whose status cannot be read are both
// "not known here", and both leave the run open: waiting a cycle longer costs
// nothing next to folding a run as failed while it is still spending, which is
// the double spend this view exists to avoid. The read is skipped when the
// bucket says the run never started, and then there is no execution to ask
// about.
//
// An ended execution is not by itself a failed run: the runner ends the
// execution by writing its record and returning, so the record can land between
// the view's own read of it and this question, and the caller reads it again
// before it folds anything.
//
// So what it returns is the state the platform reported and whether that state
// ends the execution, and not just the second: the two states that end one are
// not the same thing to the caller. A run the platform killed because a person
// cancelled it wrote no final record either, and its execution is the only
// place that says so; folded as a failure it would be a failure a standing
// approval still covers, and the next cycle would start the second execution
// for the run the person just stopped.
func (v *orchestratorRunView) executionOver(ctx context.Context, slug, runID string, l *runstore.Launch) (backend.State, bool) {
	if l == nil || l.Execution == "" {
		return "", false
	}
	x, err := v.env.checkExecution(ctx, nil, l.Execution, slug, runID)
	if err != nil {
		// The ending, not the whole ID: this line is one a person reads in the
		// daemon's output, and every surface names a run the way a lookup takes
		// it (spec.md §1, D1705).
		fmt.Fprintf(v.warn, "run %s/%s: the state of its execution could not be read: %s\n",
			slug, task.ShortRun(oneLine(runID)), safetext.OneLine(err.Error()))
		return "", false
	}
	if x == nil {
		return "", false
	}
	return x.State, x.State.Terminal()
}

// orchestratorOutcome is what one run means to the state, and whether the run
// is over at all.
//
// A record the runner is still writing — status running, which the runner
// writes as the run begins and replaces as it ends — is not a finished run:
// folding it as anything would close the attempt that is still going, take the
// item off it, and have the next cycle start a second run for the same approval
// while the first one is still spending. A run with no record at all is the
// same answer: the runner has not written its first one yet.
//
// A status this release cannot place, including one from a newer release, is a
// finished failure: the attempt is over and the item must not stay running for
// ever, and a failed attempt is the one a person looks at.
//
// A halted run is the exception, and it is the one case where the two outcomes
// differ in what happens next: a spend cap, the kill switch or a governance
// tripwire stopped a run that started, which is a refusal and not a failure
// (G4), and a refusal is never retried. Folded as failed it would be: the
// attempt would be one a person's standing approval still covers, so the very
// next cycle would relaunch work that was refused, and it would only stop when
// it ran out of allowance.
func orchestratorOutcome(rec *runstore.Record) (bool, orchestrator.Outcome) {
	if rec == nil {
		return false, ""
	}
	switch rec.Status {
	case runstore.StatusRunning:
		return false, ""
	case runstore.StatusSucceeded:
		return true, orchestrator.OutcomeSucceeded
	case runstore.StatusCancelled:
		return true, orchestrator.OutcomeCancelled
	case runstore.StatusHalted:
		return true, orchestrator.OutcomeRefused
	}
	return true, orchestrator.OutcomeFailed
}
