package cli

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The listing end of the orchestrator family (CG4 of
// docs/cli-output-console-graph, D1358, D1361): which orchestrators an
// installation has.
//
// The registry is not a file of its own. D1358 refuses a registry of PIDs and a
// local HTTP endpoint in favour of what the daemon already writes — one node
// per orchestrator under `orchestrator/`, in the project's database, and a
// cached brief per id under this machine's cache directory — so this command
// reads those two and nothing else. It is also why the names D1361 picked are
// `fugaro orchestrator ls` and not the spec's top-level plural: one spelling
// per noun, inside the group the stateless plan registers.

// orchestratorLsOptions are `fugaro orchestrator ls`'s flags.
type orchestratorLsOptions struct {
	cloudOptions
	json bool
}

func newOrchestratorLsCmd() *cobra.Command {
	var o orchestratorLsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the installation's orchestrators",
		Long: "List the orchestrators this installation has: every id whose node exists in\n" +
			"the project's database, with the holder of its lease and when that holder last\n" +
			"beat, and every cached brief this machine holds, with the time the daemon wrote\n" +
			"it. An orchestrator this machine runs — one with a cached brief — is `local`;\n" +
			"one only the database holds is `central`. A cached brief of an id nothing is\n" +
			"beating any more is marked stale: the daemon that wrote it has stopped driving\n" +
			"the id, so no cycle is rewriting it. --json prints a versioned document\n" +
			"(schemas/orchestrator-output.schema.json).\n\n" +
			"There is no registry of processes and no port: the node and the cache file are\n" +
			"the two things the daemon writes, and a run or a token may not read either.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorLs(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// The two kinds of the listing. `local` is an orchestrator this machine runs —
// the daemon here wrote its cached brief — and `central` is one only the
// project's database holds (D1358: the registry is the node and the cache file,
// so which of the two a machine has is what tells them apart).
const (
	orchestratorKindLocal   = "local"
	orchestratorKindCentral = "central"
)

// orchestratorLsDoc is the document `ls --json` prints.
type orchestratorLsDoc struct {
	Version       int                   `json:"version"`
	Kind          string                `json:"kind"`
	Orchestrators []orchestratorLsEntry `json:"orchestrators"`
}

// orchestratorLsEntry is one orchestrator of the listing.
//
// `kind` is where the orchestrator is kept: `local` is one this machine runs —
// the daemon here wrote the cached brief — and `central` is one only the
// database holds. The lease fields are what the id's lease node says and are
// absent for an id nobody holds; the cache fields are what this machine's
// cached brief says and are absent for an orchestrator it does not run.
// `cache_stale` answers "is anything still rewriting that brief" from the lease
// and not from the brief's own age: nobody is beating the id.
type orchestratorLsEntry struct {
	Kind        string `json:"kind"`
	Project     string `json:"project"`
	LeaseHolder string `json:"lease_holder,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	CachedAt    string `json:"cached_at,omitempty"`
	CacheStale  bool   `json:"cache_stale,omitempty"`
}

func runOrchestratorLs(cmd *cobra.Command, o *orchestratorLsOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	// The database is read first because it is the read that needs the network
	// and the project's IAM, and its refusal names the project; the cache
	// directory is local and cheap beside it.
	ids, err := orchestrator.NodeIDs(ctx, db)
	if err != nil {
		return dbErr(lc, err, false)
	}
	caches, err := orchestrator.CachedBriefs(os.Getenv, runtime.GOOS)
	if err != nil {
		return userErr("the daemon's cached briefs cannot be listed: %v", err)
	}
	// Freshness is measured against the database's own clock and never this
	// machine's: a lease beat and a cached brief are both stamped with the
	// server's time (D1243, D1230), so a laptop whose clock is off would read a
	// live daemon as dead, which is the whole question this listing answers.
	now, ok := db.ServerNow()
	if !ok {
		return userErr("the database answered without its own clock, and whether a daemon is beating is measured against the server's time rather than this machine's (D1230)")
	}

	leases := make(map[string]orchestrator.LeaseClaim, len(ids))
	byID := make(map[string]orchestratorLsEntry, len(ids)+len(caches))
	for _, id := range ids {
		// The lease is read for the ids the database holds and for no other:
		// an id only this machine's cache names has no node, so it has no
		// lease either, and reading one would be a request per unknown name.
		lease, err := orchestrator.ReadLeaseNode(ctx, db, id)
		if err != nil {
			return dbErr(lc, err, false)
		}
		e := orchestratorLsEntry{Kind: orchestratorKindCentral, Project: id, LeaseHolder: lease.Holder}
		if !lease.At.IsZero() {
			e.UpdatedAt = lease.At.UTC().Format(time.RFC3339)
		}
		leases[id] = lease
		byID[id] = e
	}
	for _, c := range caches {
		e := byID[c.ID]
		e.Kind = orchestratorKindLocal
		e.Project = c.ID
		e.CachedAt = c.At.UTC().Format(time.RFC3339)
		// Whether the cached brief is being kept current is the lease's
		// question and not the cache's own age: a daemon beats its lease every
		// LeaseBeat whatever its cycles do, and the cache is rewritten by a
		// successful cycle only. The id nothing beats — no node at all, or a
		// last beat past the TTL — is the id whose cache is frozen.
		e.CacheStale = !leases[c.ID].Fresh(now)
		byID[c.ID] = e
	}
	doc := orchestratorLsDoc{Version: orchestratorOutputVersion, Kind: "ls", Orchestrators: make([]orchestratorLsEntry, 0, len(byID))}
	for _, e := range byID {
		doc.Orchestrators = append(doc.Orchestrators, e)
	}
	slices.SortFunc(doc.Orchestrators, func(a, b orchestratorLsEntry) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})

	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, doc)
	}
	if len(doc.Orchestrators) == 0 {
		fmt.Fprintln(w, "no orchestrators: nothing is installed on this machine and none is in the project's database")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ORCHESTRATOR\tKIND\tLEASE HOLDER\tLEASE BEAT\tCACHE")
	for _, e := range doc.Orchestrators {
		cache := "not on this machine"
		if e.CachedAt != "" {
			cache = e.CachedAt
			if e.CacheStale {
				cache += " (stale)"
			}
		}
		holder := e.LeaseHolder
		if holder == "" {
			holder = "nobody"
		}
		beat := e.UpdatedAt
		if beat == "" {
			beat = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", safetext.Field(e.Project), e.Kind, safetext.Field(holder), beat, cache)
	}
	return tw.Flush()
}
