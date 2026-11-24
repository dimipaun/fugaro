// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package cli is `fugaro orchestrator map` (CG6 of
// docs/console-fixes/implementation-plan.md): the project's active
// orchestrators, printed as the versioned document internal/orchestrator
// defines (D3221).
//
// It is a paid command and lives in ee/ for that reason: the document's
// overlap edges are the collisions the coordinator's sweep stored and the pull
// requests its stored views hold, so this is the Coordinator tab's own read. It
// is registered as a subcommand of the core's `orchestrator` family rather than
// as a command of its own — the family is the core's and this only adds to it
// (cli.RegisterSubcommand) — and ee/register registers it: this package never
// registers itself, because only that package may call a seam (§5.5, held by
// internal/eeboundary).
package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	coordcli "github.com/dimipaun/fugaro/ee/coordinator/cli"
	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// mapOptions are the command's flags.
type mapOptions struct {
	asJSON bool
	cloud  fcli.CloudFlags
}

// NewMapCmd is the `map` command, registered as `fugaro orchestrator map`.
func NewMapCmd() *cobra.Command {
	var o mapOptions
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Print the project's active orchestrators",
		Long: "Print the orchestrators that are being driven right now: one node per\n" +
			"orchestrator whose lease is fresh, the edges between them, and the\n" +
			"coordinator's heartbeat. It is the read the console's Coordinator tab draws.\n\n" +
			"An orchestrator nobody is beating is not a node with empty figures but no node\n" +
			"at all: a node is on the map because a credential is driving it, and what a\n" +
			"node carries is the principal its driver node names, how many of its items are\n" +
			"still being worked, how many runs it has going and how long ago its holder last\n" +
			"beat. Freshness, and that age, are measured against the database's own clock\n" +
			"rather than this machine's (D1230).\n\n" +
			"Two edges join two of those nodes. A dependency is one orchestrator's item\n" +
			"waiting on another's, read from its depends_on and drawn from the one that waits\n" +
			"to the one it waits on (D3208); an overlap is two orchestrators on the same\n" +
			"work, which is a collision the coordinator stored or a pull request two of them\n" +
			"have runs on, joined through the run id an item committed (D3211, D3222). Every\n" +
			"edge joins two nodes of this document: an orchestrator nobody drives is not\n" +
			"drawn, and neither is an edge to it.\n\n" +
			"With the coordinator's sweep off for this project there is no heartbeat and no\n" +
			"stored collision to read, and the health says so: the absence of an overlap edge\n" +
			"then means nobody has looked, and never that nothing overlaps (F23).\n\n" +
			"--json prints the document the console reads: its version, the project, when it\n" +
			"was assembled, the nodes, the edges and the health. Without it, a short table.\n\n" +
			"Exit codes: 1 for a project with no database, 2 for a failure of the database.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runMap(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.asJSON, "json", false, "print the map document as JSON")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// runMap is the whole path: open the project, read the map, print it.
func runMap(cmd *cobra.Command, o *mapOptions) error {
	ctx := cmd.Context()
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	// The connection both halves of the read go through: the orchestrator nodes
	// through the client, the coordinator's stored views through the store built
	// over that same client. One client, and the one credential minted for it,
	// serve the whole document — and the refusal a project with no database gets
	// is CoordinatorDB's own sentence, which is why this read goes through the
	// coordinator's door rather than a second opener worded alike.
	db, err := coordcli.CoordinatorDB(ctx, env.Config())
	if err != nil {
		return err
	}
	doc, err := eeorch.BuildMap(ctx, eeorch.MapInput{
		DB:      db,
		Store:   coordinator.NewStore(db),
		Project: env.Config().Name,
		Repos:   mapRepos(env),
		Slug:    env.RepoSlug,
		Now:     time.Now(),
	})
	if err != nil {
		// Every read the builder fails is a failure of the database or of a node
		// it holds, which is exit 2 with the project's own sentence — the
		// classification this CLI's other reads make (env.DBErr). Its three input
		// checks cannot fail from here: the project is the selected project's own
		// name, which is the one thing checkMapInput validates, and a client and a
		// clock are what this function has just built.
		return env.DBErr(err, false)
	}
	// The document is the boundary: the free text it carries is made fit to print
	// here, whichever half of the read filled it (a driver's principal, an
	// overlap's reason, the health's note).
	doc.Sanitise()
	if o.asJSON {
		return writeMapJSON(cmd.OutOrStdout(), doc)
	}
	return writeMapText(cmd.OutOrStdout(), doc, time.Now())
}

// mapRepos is the repositories the overlap edges are joined over: every one the
// project's local config lists, sorted, which is the selection the coordinator's
// own reads make (StoredProject). A project that lists none is a map with its
// nodes and dependencies and no overlap edge rather than a refusal naming a
// --repo flag this command has not got.
func mapRepos(env *fcli.Cloud) []string {
	return slices.Sorted(maps.Keys(env.Config().Repos))
}

// writeMapJSON prints the document as the console reads it, through
// EncodeMapDoc rather than a marshaller of its own: what this command publishes
// is then exactly what this release would read back — the same validation, and
// the two lists as lists rather than as null.
func writeMapJSON(w io.Writer, doc orchestrator.MapDoc) error {
	data, err := orchestrator.EncodeMapDoc(doc)
	if err != nil {
		return fcli.RemoteErr(err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// writeMapText prints the document as a short table: one line per active
// orchestrator, then the edges, then the coordinator's state.
//
// A node's driver and its lease age are the two cells a reader must be able to
// read as absent rather than as empty: "-" is the driver node absent or
// unreadable, and a lease with no stamp is said in words, because a blank where
// a figure belongs reads as a misaligned row (readings 2 and 3).
func writeMapText(w io.Writer, doc orchestrator.MapDoc, now time.Time) error {
	if len(doc.Nodes) == 0 {
		// Empty is a fact and not an error, so the table says which fact it is:
		// no orchestrator of this project is being driven (D3221).
		fmt.Fprintf(w, "%s: no orchestrator is being driven now\n", doc.Project)
	} else {
		fmt.Fprintf(w, "%s: %d orchestrators being driven\n", doc.Project, len(doc.Nodes))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, n := range doc.Nodes {
			fmt.Fprintf(tw, "  %s\t%s\t%d tasks, %d jobs, %s\n",
				n.ID, mapDriverCell(n.Driver), n.ActiveTasks, n.RunningJobs, mapLeaseCell(n.LeaseAgeSeconds))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(doc.Edges) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, e := range doc.Edges {
			fmt.Fprintf(tw, "  %s\t%s -> %s\t%s\n", e.Kind, e.From, e.To, e.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return writeMapHealth(w, doc, now)
}

// writeMapHealth is the table's last line: the coordinator's own state, which is
// what tells a reader whether the overlap edges above it are from a minute ago
// or from before the sweep was turned off.
//
// When the sweep is on, the age printed is the last read's: `ok_at` is when a
// tick last finished reading everything, and a tick still going has no `ok_at`
// and is shown from `started_at` instead. The age is measured from this
// machine's clock and not the database's — unlike every figure of a node, which
// is a claim about a lease somebody else holds — and the two clocks are the same
// installation's, so the difference is a heartbeat's own skew and never a
// statement about whose run is live.
func writeMapHealth(w io.Writer, doc orchestrator.MapDoc, now time.Time) error {
	if !doc.Health.Sweep {
		_, err := fmt.Fprintf(w, "sweep: off — %s\n", doc.Health.Note)
		return err
	}
	switch {
	case doc.Health.OkAt != nil:
		_, err := fmt.Fprintf(w, "sweep: on, last finished %s ago\n", mapAge(now.Sub(*doc.Health.OkAt)))
		return err
	case doc.Health.StartedAt != nil:
		_, err := fmt.Fprintf(w, "sweep: on, running since %s ago\n", mapAge(now.Sub(*doc.Health.StartedAt)))
		return err
	}
	_, err := fmt.Fprintln(w, "sweep: on")
	return err
}

// mapDriverCell is one node's driver as the table prints it, and "-" for the
// driver node being absent or unreadable: an empty cell is not a reading the
// contract has, and the two are not the same statement (reading 2).
func mapDriverCell(driver string) string {
	if driver == "" {
		return "-"
	}
	return driver
}

// mapLeaseCell is one node's lease as the table prints it: its age, or a word
// for a lease carrying no stamp to measure, which is never 0 (reading 3).
func mapLeaseCell(age *float64) string {
	if age == nil {
		return "lease -"
	}
	return "lease " + mapAge(time.Duration(*age*float64(time.Second)))
}

// mapAge is a duration as the table prints it: whole seconds, which is the unit
// the document's own figure is in, and never negative — a stamp from the future
// is the skew a fresh lease is allowed and reads as now (LeaseSkew).
func mapAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}
