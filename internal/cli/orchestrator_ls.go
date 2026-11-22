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
			"the project's database — the project's own, and a named one as\n" +
			"`<project>~<name>` — with the principal driving it, the holder of its lease\n" +
			"and when that holder last beat, and every cached brief this machine holds,\n" +
			"with the time the daemon wrote it. An orchestrator this machine runs — one\n" +
			"with a cached brief — is `local`; one only the database holds is `central`.\n" +
			"A cached brief of an id nothing is beating any more is marked stale: the\n" +
			"daemon that wrote it has stopped driving the id, so no cycle is rewriting\n" +
			"it. --json prints a versioned document\n" +
			"(schemas/orchestrator-output.schema.json), whose entries carry the id's name\n" +
			"half (`name`, empty for the project's own orchestrator) and its driver\n" +
			"beside the fields the table prints — an empty driver is an id with no\n" +
			"driver node to read, which is unknown and not \"nobody drives this\".\n\n" +
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
//
// `project` carries the id as the tree keys it — `<project>` for the project's
// own orchestrator and `<project>~<name>` for a named one (C-ORCH, D3206,
// D3207) — and `name` is that id's name half, "" for the project's own. The two
// are written on every entry: the field name `project` is from when an id was
// always a project's name, and a consumer that wants the project reads the part
// before `~`. `driver` is the principal of the credential driving the id, read
// from its sibling driver node, and is written on every entry too — "" is "no
// driver node, or one this release cannot read", which the contract's own
// reading refuses to draw as "nobody drives this" (D3218, F18;
// contract-orchestrators.md, "what each side may rely on" 2), and a consumer
// needs the empty string to say "unknown" rather than to guess.
type orchestratorLsEntry struct {
	Kind        string `json:"kind"`
	Project     string `json:"project"`
	Name        string `json:"name"`
	Driver      string `json:"driver"`
	LeaseHolder string `json:"lease_holder,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	CachedAt    string `json:"cached_at,omitempty"`
	CacheStale  bool   `json:"cache_stale,omitempty"`
}

// orchestratorLsEntryOf is one entry as the id itself decides it: the id is the
// project's own orchestrator's name or `<project>~<name>`, so `name` is its name
// half — the slug for a named one, and "" for the project's own, which is what a
// screen draws as the project's own orchestrator and could not tell from an
// absent field. An id the rule does not admit has no name half and cannot arrive
// here: the ids come from NodeIDs and the cache, both of which build their
// entries with that rule.
func orchestratorLsEntryOf(id string) orchestratorLsEntry {
	_, name, _ := orchestrator.ParseOrchestratorID(id)
	return orchestratorLsEntry{Kind: orchestratorKindCentral, Project: id, Name: name}
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
		// The driver node is read beside the lease and for the same ids: it is
		// the state's, the lease's and the audit's sibling, so it belongs to an
		// id the database holds and to no id only this machine's cache names.
		driver, err := orchestrator.ReadDriverNode(ctx, db, id)
		if err != nil {
			return dbErr(lc, err, false)
		}
		e := orchestratorLsEntryOf(id)
		e.LeaseHolder = lease.Holder
		e.Driver = driver.Principal
		if !lease.At.IsZero() {
			e.UpdatedAt = lease.At.UTC().Format(time.RFC3339)
		}
		leases[id] = lease
		byID[id] = e
	}
	for _, c := range caches {
		e, ok := byID[c.ID]
		if !ok {
			// An id only this machine's cache names: it has no node in the
			// database, so it has no lease and no driver either — the loop above
			// read both for the ids the database holds and for no other — and its
			// entry is the id's own.
			e = orchestratorLsEntryOf(c.ID)
		}
		e.Kind = orchestratorKindLocal
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
	fmt.Fprintln(tw, "ORCHESTRATOR\tKIND\tDRIVER\tLEASE HOLDER\tLEASE BEAT\tCACHE")
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
		// "unknown" and not "nobody": an empty driver is an id with no driver node
		// — the node is written by whoever holds the lease (D3218, F18), so an id
		// nothing has driven since it existed has none — or one this release
		// cannot read, and the two are one field. The contract's own reading is
		// that neither is "nobody drives this", which the id's lease is the answer
		// to (contract-orchestrators.md, "what each side may rely on" 2).
		driver := e.Driver
		if driver == "" {
			driver = "unknown"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", safetext.Field(e.Project), e.Kind, safetext.Field(driver), safetext.Field(holder), beat, cache)
	}
	return tw.Flush()
}
