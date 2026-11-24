package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorBriefCacheVersion is the version of the cached brief's own
// format, written beside the document it carries so that a reader tells an
// older file from a current one rather than reading a shape that moved.
//
// It stays at 1 while the brief's text changes (S5), and it moves with the
// writer's constant (internal/orchestrator.BriefCacheVersion) when the file's
// shape does, because the two are one contract: a bump here alone, or one for a
// text whose document did not move, refuses every cache the release behind wrote.
// The daemon an update left running keeps executing the old binary and keeps
// writing the old number, and a bump would leave deleting the file by hand as
// the only way out of it.
const orchestratorBriefCacheVersion = 1

// orchestratorStateOptions are `fugaro orchestrator state`'s flags.
type orchestratorStateOptions struct {
	cloudOptions
	// name is --name: which of the project's orchestrators is read (D3206,
	// D3207). Empty is the project's own.
	name    string
	brief   bool
	offline bool
	json    bool
}

func newOrchestratorStateCmd() *cobra.Command {
	var o orchestratorStateOptions
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Print the orchestrator's state: the generated summary of its live node",
		Long: "Print the state of the project's orchestrator as the generated summary of its\n" +
			"live node, with everything it prints out of the document fenced as data. --brief\n" +
			"prints the summary alone, which is the form the daemon caches for the\n" +
			"session-start print. --brief --offline prints that cache instead and reaches no\n" +
			"network at all, showing the stamp the daemon wrote it with; with no cache yet it\n" +
			"says so rather than reading the node. --json prints a versioned document\n" +
			"(schemas/orchestrator-output.schema.json).\n\n" +
			"--name reads one of the project's named orchestrators instead of the project's\n" +
			"own: `--name night` reads `<project>~night`, its node and its cached brief.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorState(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.BoolVar(&o.brief, "brief", false, "print only the generated summary")
	f.BoolVar(&o.offline, "offline", false, "print the brief the daemon cached at session start, reaching no network (goes with --brief)")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// orchestratorStateDoc is the document `state --json` prints: the summary the
// command generated (or the daemon cached), where it came from, and the
// revision a patch built from it would carry.
type orchestratorStateDoc struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Source  string `json:"source"`
	// Rev is a pointer because zero is a revision this document has to write: a
	// node nobody has written yet is at revision 0, and that 0 is the base_rev a
	// patch built from this read carries. Absent means the source is the cache,
	// which has no revision at all.
	Rev           *int64 `json:"rev,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	Brief         string `json:"brief"`
	// Items is every work item of the live node, so a reader never parses the
	// brief's prose, which is capped. Absent for the cache, which keeps the
	// brief alone.
	Items    []orchestratorStateItem `json:"items,omitempty"`
	CachedAt string                  `json:"cached_at,omitempty"`
}

// orchestratorStateItem is one work item as `state --json` lists it.
type orchestratorStateItem struct {
	ID       string `json:"id"`
	Phase    string `json:"phase"`
	RunID    string `json:"run_id,omitempty"`
	Attempts int    `json:"attempts"`
	Repo     string `json:"repo"`
	Spec     string `json:"spec,omitempty"`
}

func orchestratorStateItems(s orchestrator.State) []orchestratorStateItem {
	items := make([]orchestratorStateItem, 0, len(s.WorkItems))
	for _, w := range s.WorkItems {
		items = append(items, orchestratorStateItem{
			ID: w.ID, Phase: string(w.Phase), RunID: w.RunID, Attempts: w.Attempts, Repo: w.Repo, Spec: w.Spec,
		})
	}
	return items
}

func runOrchestratorState(cmd *cobra.Command, o *orchestratorStateOptions) error {
	ctx := cmd.Context()
	if o.offline && !o.brief {
		return userErr("--offline prints the brief the daemon cached, so it is used with --brief: fugaro orchestrator state --brief --offline")
	}
	if o.offline {
		_, id, err := selectOrchestratorProject(ctx, o.cloudOptions, o.name)
		if err != nil {
			return err
		}
		return printCachedOrchestratorBrief(cmd, o, id)
	}
	t, err := openOrchestrator(ctx, o.cloudOptions, o.name)
	if err != nil {
		return err
	}
	s, _, err := t.node.Load(ctx)
	if err != nil {
		return dbErr(t.lc, err, false)
	}
	brief, err := orchestratorBrief(ctx, t.lc, t.db, s)
	if err != nil {
		return err
	}
	text := brief.String()
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorStateDoc{
			Version: orchestratorOutputVersion, Kind: "state", ID: t.id, Source: "node",
			Rev: &s.Rev, SchemaVersion: s.SchemaVersion, Brief: text, Items: orchestratorStateItems(s),
		})
	}
	// The revision is what a patch is built from, so a reader who is about to
	// write one is told it; --brief is the summary alone, because that is the
	// form the daemon caches and the session-start print shows.
	if !o.brief {
		fmt.Fprintf(w, "orchestrator %s: rev %d, schema_version %d\n", safetext.Field(t.id), s.Rev, s.SchemaVersion)
	}
	fmt.Fprint(w, text)
	return nil
}

// orchestratorBrief is what `state --brief` prints: the state node, the nodes
// beside it and the history its estimates are drawn from.
//
// The two inputs the command supplies rather than reads are the estimate's own:
// how many tasks are assumed to run at once is the running cap this project's
// own layers state — `orchestrator.max_running` where they set one, the person's
// `max_parallel` otherwise, which is what that key defaults to (A6, D1239) — and
// the mode is the default one a run resolves to when no layer and no request
// sets another (D19). The brief is a print and not a run, so it has no `--rigor`
// of its own to be told otherwise.
//
// The cap is read from the local config and never from the published policy: the
// published block is a network read the brief must not make (D1243), so a
// project whose cap is published rather than set here is estimated at the local
// number, and the range does not say which number it used.
//
// The table is the file the estimate's history is cached in, never the runs
// bucket (D1243): the brief is a print a session makes before it does anything,
// so the whole of it is a read of the database's orchestrator subtree and two
// local files, and a machine that has no cache yet estimates from the seeds.
func orchestratorBrief(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, s orchestrator.State) (orchestrator.Brief, error) {
	// Load refused a document another id or role wrote, so the state's own id is
	// this project's orchestrator.
	nodes, err := orchestratorBriefNodes(ctx, db, s.AgentID)
	if err != nil {
		return orchestrator.Brief{}, dbErr(lc, err, false)
	}
	table, err := orchestratorBriefTable()
	if err != nil {
		return orchestrator.Brief{}, err
	}
	return orchestrator.Brief{
		State:      s,
		Nodes:      nodes,
		Table:      table,
		MaxRunning: orchestratorBriefMaxRunning(lc),
		Mode:       rigor.Default,
		Now:        time.Now(),
	}, nil
}

// orchestratorBriefMaxRunning is how many tasks the estimate assumes run at
// once: the project's `orchestrator.max_running` where this checkout's own
// config sets one, and the person's `max_parallel` otherwise, which is the
// value that key falls back to (A6, D1239). It is the same arithmetic the
// daemon plans its cycles under, minus the published layer, which the brief
// cannot read (D1243).
func orchestratorBriefMaxRunning(lc *localcfg.Config) int {
	blk := policy.Orchestrator{}
	if eff := lc.OrchestratorEffective(nil); eff != nil {
		blk = *eff
	}
	return int(blk.WithDefaults(int64(lc.MaxParallel)).MaxRunning)
}

// orchestratorBriefNodes reads the nodes beside the state node: one per imported
// plan and the backlog's (D2735, D2714).
//
// The plans are listed with a shallow read and then read one by one — the read
// NodeIDs makes for the ids of an installation, and for its reason: a plan node
// is a state document of up to MaxStateBytes, so a project with twenty plans is
// not a read of all of them at once.
//
// A project that has imported no plan and holds nothing standing has neither
// node, and both read as this orchestrator's empty state rather than being
// refused: the brief of a project nobody has planned in is the brief it always
// was.
func orchestratorBriefNodes(ctx context.Context, db *rtdb.Client, id string) ([]orchestrator.NodeBrief, error) {
	names, err := orchestrator.PlanNames(ctx, db, id)
	if err != nil {
		return nil, err
	}
	nodes := make([]orchestrator.NodeBrief, 0, len(names)+1)
	for _, name := range names {
		s, err := orchestrator.ReadShard(ctx, db, id, orchestrator.RoleOrchestrator, orchestrator.PlanShard(name))
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, orchestrator.NodeBrief{Name: name, State: s})
	}
	s, err := orchestrator.ReadShard(ctx, db, id, orchestrator.RoleOrchestrator, orchestrator.ShardBacklog)
	if err != nil {
		return nil, err
	}
	return append(nodes, orchestrator.NodeBrief{Name: orchestrator.BacklogNode, State: s}), nil
}

// orchestratorBriefTable is the run history the estimates are drawn from: the
// table cache this machine holds, which the command that reads the runs bucket
// writes with the history it read.
//
// The brief reaches no runs bucket itself (D1243), so this file is the whole of
// what it knows about how long work has taken, and a machine without one is the
// tier seeds and nothing else (D2712). A file that is there and cannot be read is
// refused rather than passed over: a table this release does not read would
// otherwise be a figure drawn from something else, and what the refusal names is
// the file to remove.
func orchestratorBriefTable() (*estimate.Table, error) {
	path, err := estimate.CachePath(os.Getenv, runtime.GOOS)
	if err != nil {
		return nil, userErr("%v", err)
	}
	table, err := estimate.ReadCache(path)
	switch {
	case err == nil:
		return table, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	default:
		return nil, userErr("%v; delete that file to estimate from the tier seeds again", err)
	}
}

// orchestratorBriefCache is the file the daemon writes for the session-start
// print (D1243): the generated brief, the server time it was written and the
// cache format's own version. O8 owns the writer — internal/orchestrator's
// cache.go — and this is the reader's half of that one contract, here rather
// than there because a reader shares the file and never the source. The brief
// it carries is the fenced summary (G3), so the print shows it as the daemon
// wrote it and adds nothing of its own to it.
type orchestratorBriefCache struct {
	Version   int       `json:"schema_version"`
	Brief     string    `json:"brief"`
	WrittenAt time.Time `json:"written_at"`
}

// orchestratorCachePath is the daemon's cached brief for one orchestrator id:
// $XDG_CACHE_HOME/fugaro/orchestrator/<id>.json. The place is the writer's own
// — internal/orchestrator.BriefCachePath, which the daemon writes through — so
// the two ends of the file cannot drift into reading one path and writing
// another.
func orchestratorCachePath(getenv func(string) string, id string) (string, error) {
	path, err := orchestrator.BriefCachePath(getenv, runtime.GOOS, id)
	if err != nil {
		return "", userErr("the daemon's cached brief cannot be placed: %v", err)
	}
	return path, nil
}

// readOrchestratorBriefCache reads the cached brief, with the path it read so
// a refusal can name it. Any trouble — no file, a document this release does
// not read, a version from another release, a stamp that is missing or in the
// future — is that trouble and never a fallback to the node: a print that read
// the node when the cache was missing would break the one rule D1243 keeps.
func readOrchestratorBriefCache(getenv func(string) string, id string) (orchestratorBriefCache, string, error) {
	path, err := orchestratorCachePath(getenv, id)
	if err != nil {
		return orchestratorBriefCache{}, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return orchestratorBriefCache{}, path, userErr("the daemon's cached brief at %s cannot be read: %v", path, err)
		}
		return orchestratorBriefCache{}, path, userErr("no cached brief at %s yet: the daemon writes it after its first tick, and fugaro orchestrator state --brief reads the node instead", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c orchestratorBriefCache
	if err := dec.Decode(&c); err != nil {
		return orchestratorBriefCache{}, path, userErr("the cached brief at %s is not a document this release reads: %v; fugaro orchestrator state --brief reads the node", path, err)
	}
	switch {
	case c.Version != orchestratorBriefCacheVersion:
		return orchestratorBriefCache{}, path, userErr("the cached brief at %s carries schema_version %d, and this release reads %d: fugaro orchestrator state --brief reads the node", path, c.Version, orchestratorBriefCacheVersion)
	case c.WrittenAt.IsZero():
		return orchestratorBriefCache{}, path, userErr("the cached brief at %s carries no time it was written, so how stale it is cannot be shown: fugaro orchestrator state --brief reads the node", path)
	}
	return c, path, nil
}

// printCachedOrchestratorBrief shows the daemon's cached brief and the stamp
// it carries, so a stale one is visible rather than silent (D1243).
func printCachedOrchestratorBrief(cmd *cobra.Command, o *orchestratorStateOptions, id string) error {
	c, _, err := readOrchestratorBriefCache(os.Getenv, id)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorStateDoc{
			Version: orchestratorOutputVersion, Kind: "state", ID: id, Source: "cache",
			Brief: c.Brief, CachedAt: c.WrittenAt.UTC().Format(time.RFC3339),
		})
	}
	fmt.Fprint(w, c.Brief)
	if !strings.HasSuffix(c.Brief, "\n") {
		fmt.Fprintln(w)
	}
	// The stamp is the server's, so a machine whose own clock is behind the
	// database's sees it in the future; that is said rather than turned into a
	// refusal, because how stale the brief is is what a reader is here for.
	var age string
	if d := time.Since(c.WrittenAt); d > 0 {
		age = d.Truncate(time.Second).String() + " ago"
	} else {
		age = "stamped ahead of this machine's clock, so it is at least current"
	}
	fmt.Fprintf(w, "note: this is the brief the daemon cached at %s (%s), not the live node: fugaro orchestrator state --brief reads it\n",
		c.WrittenAt.UTC().Format(time.RFC3339), age)
	return nil
}

// orchestratorEventsOptions are `fugaro orchestrator events`'s flags.
type orchestratorEventsOptions struct {
	cloudOptions
	// name is --name: whose audit log is read (D3206, D3207). Empty is the
	// project's own orchestrator's.
	name  string
	since string
	json  bool
}

func newOrchestratorEventsCmd() *cobra.Command {
	var o orchestratorEventsOptions
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Read the orchestrator's audit log",
		Long: "Read the project's orchestrator audit log in the order the server stamped it,\n" +
			"oldest first. --since takes a duration (1h, 30m) and keeps the entries the\n" +
			"database's own clock puts inside it. --json prints a versioned document\n" +
			"(schemas/orchestrator-output.schema.json).\n\n" +
			"--name reads one of the project's named orchestrators instead of the project's\n" +
			"own: `--name night` reads `<project>~night`'s log, which is the log its own\n" +
			"daemon writes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorEvents(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.StringVar(&o.since, "since", "", "only entries newer than this duration, such as 1h or 30m")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// orchestratorEventsDoc is the document `events --json` prints.
type orchestratorEventsDoc struct {
	Version int                  `json:"version"`
	Kind    string               `json:"kind"`
	ID      string               `json:"id"`
	Since   string               `json:"since,omitempty"`
	Events  []orchestrator.Entry `json:"events"`
}

func runOrchestratorEvents(cmd *cobra.Command, o *orchestratorEventsOptions) error {
	ctx := cmd.Context()
	var since time.Duration
	if o.since != "" {
		d, err := time.ParseDuration(o.since)
		if err != nil || d <= 0 {
			return userErr("--since takes a duration such as 1h or 30m, and %q is not one", safetext.OneLine(o.since))
		}
		since = d
	}
	t, err := openOrchestrator(ctx, o.cloudOptions, o.name)
	if err != nil {
		return err
	}
	// The read is internal/orchestrator's, which is the front door's get_events
	// too: the order is the server's stamp's (D1230) and --since is cut on the
	// server's clock, and one function reads the log for both surfaces so they
	// cannot come to answer differently.
	read, err := orchestrator.ReadAudit(ctx, t.db, t.id, since)
	if err != nil {
		if errors.Is(err, orchestrator.ErrNoServerTime) {
			return userErr("the database answered without its own clock, and --since cuts the log at the server's time rather than this machine's (D1230)")
		}
		return dbErr(t.lc, err, false)
	}
	doc := orchestratorEventsDoc{Version: orchestratorOutputVersion, Kind: "events", ID: t.id, Events: read.Entries}
	if !read.Since.IsZero() {
		doc.Since = read.Since.Format(time.RFC3339)
	}
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, doc)
	}
	if len(doc.Events) == 0 {
		fmt.Fprintln(w, "no events")
		return nil
	}
	for _, e := range doc.Events {
		fmt.Fprintf(w, "%s %s %s", e.At.UTC().Format(time.RFC3339), safetext.Field(e.Subject), safetext.Field(e.Transition))
		if e.Text != "" {
			fmt.Fprintf(w, ": %s", safetext.OneLine(e.Text))
		}
		fmt.Fprintln(w)
	}
	return nil
}
