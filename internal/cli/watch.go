package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/watch"
)

// fugaro watch (design §7, plan M9c): a live view of one project's budget
// backend. This file is the command: flags, project selection, and the
// non-interactive outputs (plain text frames and JSON documents). The
// interactive screen plugs in through runTUI.

const (
	// watchPlainInterval is the least time between two plain frames.
	watchPlainInterval = 10 * time.Second
	// watchJSONGap is the least time between two JSON documents.
	watchJSONGap = time.Second
	// watchFirstFrame is how long watch waits for all four streams before it
	// prints what it has (an offline frame, usually).
	watchFirstFrame = 5 * time.Second
	// degradedNotice is what the project without a budget backend is told.
	degradedNotice = "no budget backend: showing run records only"
	// degradedSince is how far back the degraded view reads runs.
	degradedSince = 24 * time.Hour
)

// WatchDeps is everything the interactive screen needs; the command has
// already selected the project, opened and checked its database, and started
// the supervisor.
type WatchDeps struct {
	In       io.Reader
	Out, Err io.Writer
	LC       *localcfg.Config
	DB       *rtdb.Client
	Sup      *watch.Supervisor
	Config   watch.Config
	// RepoKey is the wire key of --repo's repository, "" for all of them.
	RepoKey string
	Repo    string // --repo as given
	ASCII   bool
	NoColor bool
}

// watchStdoutTTY says whether w is a terminal; tests replace it.
var watchStdoutTTY = isTTY

// runTUI runs the interactive screen (internal/watch's Bubble Tea model). A
// variable so tests can replace it.
var runTUI = func(ctx context.Context, d *WatchDeps) error {
	by, err := d.LC.Me(ctx) // who a kill is recorded as, before the screen takes the terminal
	if err != nil {
		return userErr("%v", err)
	}
	return watch.RunTUI(ctx, watch.TUIOptions{
		In: d.In, Out: d.Out, Project: d.LC.Name, Updates: d.Sup.Updates(), Config: d.Config,
		RepoKey: d.RepoKey, Repo: d.Repo,
		ASCII:   watch.UseASCII(d.ASCII, os.Getenv),
		NoColor: watch.UseNoColor(d.NoColor, os.Getenv),
		Exec: func(ctx context.Context, req watch.Request) watch.Outcome {
			return watch.Execute(ctx, d.DB, req, by)
		},
		AltScreen: true,
	})
}

type watchOptions struct {
	cloud                                 cloudOptions
	repo                                  string
	json, once, poll, plain, ascii, noClr bool
	interval                              time.Duration
}

func newWatchCmd() *cobra.Command {
	var o watchOptions
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch the project's spend, repositories and running agents live",
		Long: "Watch the project's budget backend live: the project's total against its caps,\n" +
			"one block per repository (spend against its cap, burn rate, kill state) and one\n" +
			"row per running agent (stage, round, age, spend, silent, lost and\n" +
			"near-deadline flags). It reads the project's Firebase Realtime Database as you\n" +
			"(the Viewer role), like fugaro budget show.\n\n" +
			"At a terminal watch opens the interactive screen. With --plain, --json or\n" +
			"--once, or when stdin or stdout is not a terminal, it prints text frames (at\n" +
			"most one per --interval, 10s) or, with --json, one JSON document per change\n" +
			"(at most one a second); --once prints one snapshot and exits. Those modes have\n" +
			"no kill keys: fugaro budget kill and resume are the script path.\n\n" +
			"A project with no budget backend shows its run records only (as fugaro ls\n" +
			"does, for the last 24 hours) and says so; fugaro ls --watch stays for the\n" +
			"question \"did my runs finish\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runWatch(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.json, "json", false, "print one JSON document per change instead of a screen")
	f.BoolVar(&o.once, "once", false, "print one snapshot and exit (with --json or plain text)")
	f.BoolVar(&o.plain, "plain", false, "print text frames instead of the interactive screen")
	f.BoolVar(&o.poll, "poll", false, "read by polling instead of streaming (for proxies that buffer)")
	f.DurationVar(&o.interval, "interval", 0, "the least time between plain frames (default 10s) and the polling period (default 5s)")
	f.BoolVar(&o.ascii, "ascii", false, "use ASCII instead of ⚠ ▓░ ▸")
	f.BoolVar(&o.noClr, "no-color", false, "no colour (NO_COLOR and TERM=dumb do the same)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runWatch(cmd *cobra.Command, o *watchOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if o.interval < 0 {
		return userErr("--interval must be positive")
	}
	if o.json && o.plain {
		return userErr("--json and --plain don't go together")
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o.cloud)
	if err != nil {
		return err
	}
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return runWatchDegraded(cmd, o, lc)
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	var repoKey string
	if o.repo != "" {
		slug, err := repoSlugOf(ctx, lc, o.repo)
		if err != nil {
			return err
		}
		repoKey = budget.Key(slug)
	}
	d := &WatchDeps{In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), LC: lc, DB: db,
		Config: watch.Config{BurnAlertPerHour: lc.BurnAlert()}, RepoKey: repoKey, Repo: o.repo, ASCII: o.ascii, NoColor: o.noClr}

	if o.once {
		return watchOnce(ctx, d, o)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sup := watch.Start(ctx, db, watch.Options{Poll: o.poll, Interval: o.interval})
	d.Sup = sup
	if !o.json && !o.plain && stdinIsTerminal(d.In) && watchStdoutTTY(d.Out) {
		err := runTUI(ctx, d)
		cancel()
		for range sup.Updates() { // the supervisor stops cleanly
		}
		return err
	}
	return watchStream(ctx, d, o)
}

// emit writes one frame or document of v.
func (d *WatchDeps) emit(o *watchOptions, v watch.View, header bool) error {
	if d.RepoKey != "" {
		v = watch.FilterRepo(v, d.RepoKey)
	}
	if o.json {
		return json.NewEncoder(d.Out).Encode(watch.BuildJSON(d.LC.Name, v))
	}
	if header {
		fmt.Fprintf(d.Out, "--- %s ---\n", v.Now.UTC().Format(time.RFC3339))
	}
	if err := watch.RenderPlain(d.Out, d.LC.Name, v, watch.PlainOptions{ASCII: o.ascii, Repo: d.Repo}); err != nil {
		return err
	}
	if header {
		fmt.Fprintln(d.Out)
	}
	return nil
}

// watchOnce prints one snapshot, read by plain GETs of the four paths the
// stream would follow.
func watchOnce(ctx context.Context, d *WatchDeps, o *watchOptions) error {
	now := budgetNow().UTC()
	st := watch.NewState()
	read := func(src watch.Source, path string) error {
		var raw json.RawMessage
		found, err := d.DB.Get(ctx, path, &raw)
		if err != nil {
			return dbErr(d.LC, err, false)
		}
		if !found {
			raw = json.RawMessage("null")
		}
		if sn, ok := d.DB.ServerNow(); ok {
			now = sn
		}
		return st.Apply(src, rtdb.Event{Type: "put", Path: "/", Data: raw}, now)
	}
	// The day comes from the database's clock once a request has seen it.
	if _, err := d.DB.Get(ctx, budget.PathProject, new(json.RawMessage)); err != nil {
		return dbErr(d.LC, err, false)
	}
	if sn, ok := d.DB.ServerNow(); ok {
		now = sn
	}
	day := budget.Day(now)
	st.SetDay(day)
	for _, p := range []struct {
		src  watch.Source
		path string
	}{
		{watch.SrcConfig, "config"},
		{watch.SrcGlobal, budget.PathSpendGlobal(day)},
		{watch.SrcRepos, "spend/" + budget.DayKey(day) + "/repos"},
		{watch.SrcAgents, "agents"},
	} {
		if err := read(p.src, p.path); err != nil {
			return err
		}
	}
	return d.emit(o, watch.Build(st, now, d.Config), false)
}

// watchStream prints a frame or document for every change the supervisor
// reports, no faster than the mode allows, until ctx ends.
func watchStream(ctx context.Context, d *WatchDeps, o *watchOptions) error {
	gap := watchPlainInterval
	if o.interval > 0 {
		gap = o.interval
	}
	if o.json {
		gap = watchJSONGap
	}
	st := watch.NewState()
	var seen [4]bool
	started := time.Now()
	var last time.Time
	var lastConn watch.ConnKind
	dirty := false
	for {
		var u watch.Update
		var ok bool
		select {
		case <-ctx.Done():
			return nil
		case u, ok = <-d.Sup.Updates():
			if !ok {
				return nil
			}
		}
		_ = st.Handle(u) // a bad event leaves the tree as it was
		switch {
		case u.Kind == watch.UpdEvent && (u.Ev.Type == "put" || u.Ev.Type == "patch"):
			dirty = true
			if u.Ev.Type == "put" && u.Ev.Path == "/" {
				seen[u.Src] = true
			}
		case u.Kind == watch.UpdDay || u.Kind == watch.UpdPolling:
			dirty = true
		}
		v := watch.Build(st, u.Now, d.Config)
		if !last.IsZero() && v.Conn.Kind != lastConn {
			dirty = true
		}
		refused := v.Conn.Kind == watch.ConnRefused
		ready := seen == [4]bool{true, true, true, true} || time.Since(started) >= watchFirstFrame
		if refused || (ready && dirty && (last.IsZero() || time.Since(last) >= gap)) {
			if err := d.emit(o, v, !o.json); err != nil {
				return err
			}
			last, lastConn, dirty = time.Now(), v.Conn.Kind, false
		}
		if refused {
			return dbErr(d.LC, rtdb.ErrPermission, false)
		}
	}
}

// ------------------------------------------------------------- degraded

// watchDegradedDoc is the --json document of a project without a budget
// backend: ls's document, flagged.
type watchDegradedDoc struct {
	lsDoc
	Degraded bool   `json:"degraded"`
	Notice   string `json:"notice"`
}

// runWatchDegraded shows run records only, read as ls reads them.
func runWatchDegraded(cmd *cobra.Command, o *watchOptions, lc *localcfg.Config) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	errw := cmd.ErrOrStderr()
	f := lsFilter{warn: errw}
	if f.slugs, err = lsSlugs(ctx, env, &lsOptions{repo: o.repo}, errw); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	interval := watchPlainInterval
	if o.interval > 0 {
		interval = o.interval
	}
	clear := !o.once && !o.json && !o.plain && isTTY(out)
	for {
		now := time.Now()
		f.since = now.Add(-degradedSince)
		rows, err := loadRows(ctx, env, f, now)
		if err != nil {
			return err
		}
		if clear {
			fmt.Fprint(out, "\x1b[H\x1b[2J")
		}
		if o.json {
			err = json.NewEncoder(out).Encode(watchDegradedDoc{
				lsDoc:    lsDoc{Project: lc.Name, Runs: rows, Totals: runview.Sum(rows), Warnings: []string{}},
				Degraded: true, Notice: degradedNotice})
		} else {
			fmt.Fprintf(out, "%s\n", degradedNotice)
			err = printRows(out, lc.Name, rows, nil, now, false)
			fmt.Fprintln(out)
		}
		if err != nil || o.once {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
