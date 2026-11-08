package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorBriefCacheVersion is the version of the cached brief's own
// format, written beside the document it carries so that a reader tells an
// older file from a current one rather than reading a shape that moved.
const orchestratorBriefCacheVersion = 1

// orchestratorStateOptions are `fugaro orchestrator state`'s flags.
type orchestratorStateOptions struct {
	cloudOptions
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
			"(schemas/orchestrator-output.schema.json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorState(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.brief, "brief", false, "print only the generated summary")
	f.BoolVar(&o.offline, "offline", false, "print the brief the daemon cached at session start, reaching no network (goes with --brief)")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// orchestratorStateDoc is the document `state --json` prints: the summary the
// command generated (or the daemon cached), where it came from, and the
// revision a patch built from it would carry.
// Rev is a pointer because zero is a revision this document has to write: a
// node nobody has written yet is at revision 0, and that 0 is the base_rev a
// patch built from this read carries. Absent means the source is the cache,
// which has no revision at all.
type orchestratorStateDoc struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Source        string `json:"source"`
	Rev           *int64 `json:"rev,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	Brief         string `json:"brief"`
	CachedAt      string `json:"cached_at,omitempty"`
}

func runOrchestratorState(cmd *cobra.Command, o *orchestratorStateOptions) error {
	ctx := cmd.Context()
	if o.offline && !o.brief {
		return userErr("--offline prints the brief the daemon cached, so it is used with --brief: fugaro orchestrator state --brief --offline")
	}
	if o.offline {
		lc, err := selectOrchestratorProject(ctx, o.cloudOptions)
		if err != nil {
			return err
		}
		id, err := orchestratorID(lc)
		if err != nil {
			return err
		}
		return printCachedOrchestratorBrief(cmd, o, id)
	}
	lc, node, _, err := openOrchestrator(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	s, _, err := node.Load(ctx)
	if err != nil {
		return dbErr(lc, err, false)
	}
	brief := orchestrator.Summarize(s)
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorStateDoc{
			Version: orchestratorOutputVersion, Kind: "state", ID: lc.Name, Source: "node",
			Rev: &s.Rev, SchemaVersion: s.SchemaVersion, Brief: brief,
		})
	}
	// The revision is what a patch is built from, so a reader who is about to
	// write one is told it; --brief is the summary alone, because that is the
	// form the daemon caches and the session-start print shows.
	if !o.brief {
		fmt.Fprintf(w, "orchestrator %s: rev %d, schema_version %d\n", safetext.Field(lc.Name), s.Rev, s.SchemaVersion)
	}
	fmt.Fprint(w, brief)
	return nil
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
// $XDG_CACHE_HOME/fugaro/orchestrator/<id>.json, placed through
// localcfg.UserDir the way the name check's cache is.
func orchestratorCachePath(getenv func(string) string, id string) (string, error) {
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return "", userErr("the daemon's cached brief cannot be placed: %v", err)
	}
	return filepath.Join(dir, "fugaro", "orchestrator", id+".json"), nil
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
			"(schemas/orchestrator-output.schema.json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorEvents(cmd, &o) },
	}
	f := cmd.Flags()
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
	lc, _, db, err := openOrchestrator(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	path, err := orchestrator.PathAudit(lc.Name)
	if err != nil {
		return userErr("%v", err)
	}
	var raw map[string]orchestrator.Entry
	_, err = db.Get(ctx, path, &raw)
	if err != nil {
		return dbErr(lc, err, false)
	}
	// The log is a map keyed by the entry's derived id, so the order is read
	// off the stamp the server gave each entry (D1230) and never off the keys.
	entries := make([]orchestrator.Entry, 0, len(raw))
	for _, e := range raw {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b orchestrator.Entry) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	doc := orchestratorEventsDoc{Version: orchestratorOutputVersion, Kind: "events", ID: lc.Name, Events: entries}
	if since > 0 {
		now, ok := db.ServerNow()
		if !ok {
			return userErr("the database answered without its own clock, and --since cuts the log at the server's time rather than this machine's (D1230)")
		}
		cut := now.Add(-since)
		doc.Since = cut.UTC().Format(time.RFC3339)
		kept := entries[:0]
		for _, e := range entries {
			if !e.At.Before(cut) {
				kept = append(kept, e)
			}
		}
		doc.Events = kept
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
