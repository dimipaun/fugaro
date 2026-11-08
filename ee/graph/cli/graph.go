// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package cli is `fugaro graph` (CG3 of
// docs/cli-output-console-graph/implementation-plan-graph.md): the console's
// dependency graph, printed as the versioned document ee/graph defines.
//
// It is a paid command and lives in ee/ for that reason (D1362, F26): its
// `all` scope is the coordinator's stored view, which is the Coordinator tab's
// own read. ee/register registers it and this package never registers itself —
// only that package may call cli.RegisterCommand (§5.5, held by
// internal/eeboundary).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	coordcli "github.com/dimipaun/fugaro/ee/coordinator/cli"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// Three properties are this command rather than niceties around it:
//
//   - The scope is refused before anything is read. The value reaches argv from
//     the console's renderer, so it is parsed against graph's closed pattern as
//     the command's first statement (H1): a value that is not one of the four
//     shapes never reaches a path, another flag or a read. A scope this build
//     has no reader for is refused too, with a sentence rather than an empty
//     graph, because a screen must be able to tell "nothing here" from "not
//     asked" (contract §6.22).
//   - It needs no checkout. The scope that exists today is the coordinator's
//     stored view — the read `coordinator status --from-store` makes, through
//     the caller's own credential and no key of the host's — so this command
//     opens no host and paints in a console child whose cwd is nothing in
//     particular. The one fact it can take from the working directory is a
//     fallback, and it is not this command's: the provider a repository's slug
//     is keyed by comes from the project's own config, and the checkout is
//     consulted only when that entry leaves it out (repoSlug). So a project
//     whose config names its providers — every repository the CLI's own
//     onboarding records carries one — is graphed with no checkout at all, and
//     a repository the config leaves out is refused, naming that entry, rather
//     than graphed from a guess. The plan scope keeps its checkout and its
//     card; this command has neither (D1356).
//   - It prints one document. A refused read is an error with no document at
//     all, and a scope that is down is a state inside the document (H3), so a
//     reader tells the two apart by exactly that.

// graphOptions are the command's flags.
type graphOptions struct {
	source string
	asJSON bool
	watch  bool
	cloud  fcli.CloudFlags
}

// NewGraphCmd is the `graph` command.
func NewGraphCmd() *cobra.Command {
	var o graphOptions
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print the project's dependency graph",
		Long: "Print the project's dependency graph as one versioned document.\n" +
			"Every scope is read from the project's database through the caller's own\n" +
			"credential: the coordinator's stored view is what the coordinator's sweep\n" +
			"already wrote, so this command opens no host and works in a directory that\n" +
			"is not a checkout. The one fact it needs is each repository's git\n" +
			"provider, which the project config's repos entries carry and which the\n" +
			"repository's slug is keyed by: a repository whose entry leaves provider\n" +
			"out is refused with the sentence that says where to set it, rather than\n" +
			"graphed from a guess.\n\n" +
			"--source selects the scope, and the set is closed: all (the default) is\n" +
			"every scope that exists, which today is the coordinator's stored view;\n" +
			"coordinator is that view alone; orchestrator:<id> and local:<id> name scopes\n" +
			"this build has no reader for yet and are refused rather than answered with an\n" +
			"empty graph. An id is one lower-case letter or digit, then up to 39 more of\n" +
			"those or a hyphen, and nothing else: anything that does not match is refused\n" +
			"before anything is read.\n\n" +
			"The coordinator's scope is every repository the local config lists, and a\n" +
			"repository the sweep has never reconciled is read as its runs alone — the\n" +
			"half of the view that needs no credential — rather than dropped. Which\n" +
			"half it is is a fact about the repository and not about the scope: the\n" +
			"scope's state is down when the project lists repositories and the sweep\n" +
			"has stored a view for none of them, and up once it has stored one, beside\n" +
			"records that may still be the runs half.\n\n" +
			"With --json the document is printed as the console reads it: its version,\n" +
			"when it was assembled, one entry per scope in sources, the nodes, the edges\n" +
			"(the coordinator's view has none) and the warnings. Without it, a short\n" +
			"table: each scope's state, then its nodes grouped by release.\n\n" +
			"--watch --json prints a whole document per change instead of one and\n" +
			"exits, one compact object per line: the first snapshot at once, then one\n" +
			"document for every change of the scopes it covers, at most one a second,\n" +
			"and a document every 30 seconds while nothing changes. Every document\n" +
			"carries the stream's own id and a sequence that rises within it, so a\n" +
			"reader reads a new id as a whole new snapshot rather than comparing its\n" +
			"sequence with the one before. It subscribes to each scope once, and a\n" +
			"scope it cannot reach is a state in the document rather than an exit.\n" +
			"--watch needs --json, which is the only shape a reader of the stream can\n" +
			"consume.\n\n" +
			"Exit codes: 1 for a refused scope or a project with no database, 2 for a\n" +
			"failure of the database or the runs bucket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runGraph(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.source, "source", string(graph.ScopeAll), "the scope to read: all, coordinator, orchestrator:<id> or local:<id>")
	f.BoolVar(&o.asJSON, "json", false, "print the graph document as JSON")
	f.BoolVar(&o.watch, "watch", false, "print a whole document per change, one compact object per line (needs --json)")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// runGraph is the whole path: refuse the scope, read the scopes that exist,
// assemble the document, print it.
func runGraph(cmd *cobra.Command, o *graphOptions) error {
	// The renderer's value is refused before the config is selected and before
	// the cloud is opened, so a string that is not a scope reaches no path, no
	// flag and no read (H1). graph.ParseScope's sentences name the command and
	// the pattern's own rule, which is what a console shows beside its refusal.
	scope, err := graph.ParseScope(o.source)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	// A scope this build has no reader for is refused here, before the cloud is
	// opened as well: an honest "this build has no graph for that" rather than a
	// document whose sources are empty, and one the console reads as this CLI
	// having no such scope (contract §6.22, T3).
	switch scope.Kind {
	case graph.ScopeOrchestrator, graph.ScopeLocal:
		return fcli.UserErr("graph: source %s: no orchestrator is installed in this build", scope)
	}

	// The stream is the other output of the same read, and the one that needs
	// --json: a reader of a stream is a program, and a table interleaved between
	// documents is not a shape it can consume. A combination this command has no
	// shape for is refused with the scope's own rules, before the project is
	// selected and before the cloud is opened, so the pair reaches no read
	// either.
	if o.watch && !o.asJSON {
		return fcli.UserErr("graph: --watch needs --json: the stream prints one document per change, one object per line")
	}

	ctx := cmd.Context()
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	if o.watch {
		return runGraphWatch(ctx, cmd.OutOrStdout(), env)
	}

	// The read's warnings are buffered rather than written as they are made:
	// with --json they are the document's own `warnings` (a reader of the
	// document has no stderr to look at), and without it they go to stderr
	// below. Either way they are carried once.
	var warned bytes.Buffer
	now := time.Now()
	// The read goes through a store its caller holds, because the other caller
	// of the same read — the stream — holds one for its whole life. This caller
	// reads once, so it opens one here and drops it with the command; the
	// refusal a project with no database gets is CoordinatorStore's own sentence,
	// the one this read has always answered with.
	store, err := coordcli.CoordinatorStore(ctx, env.Config())
	if err != nil {
		return err
	}
	// A one-shot read has no time of its own to give for the stored view (the
	// sweep's own design), so its `as_of` is null and the scope claims no age:
	// the zero time is the caller saying it has none (contract §6.22).
	doc, err := readGraph(ctx, env, store, now, time.Time{}, &warned)
	if err != nil {
		return err
	}
	// The document is the boundary: every text field of it is made fit to print
	// here, whichever source filled it (H2).
	if err := doc.Sanitise(); err != nil {
		return fcli.RemoteErr(err)
	}
	if o.asJSON {
		return writeGraphJSON(cmd.OutOrStdout(), doc)
	}
	// The table has no field for what the read could not do, so a person
	// reading it gets those lines on stderr — the channel the coordinator's own
	// read warns on. With --json they are in the document instead.
	_, _ = cmd.ErrOrStderr().Write(warned.Bytes())
	return writeGraphText(cmd.OutOrStdout(), doc)
}

// readGraph is the read both outputs make: the coordinator's stored view as a
// document, read through the caller's own credential and holding no key of the
// host's, and the refusal that ends a read this build cannot make.
//
// `all` is the union of the scopes that exist, and today that is the
// coordinator's alone: the stored view, read exactly as `coordinator status
// --from-store --all` reads it — the same store, the same credential and the
// same runs-half fallback for a repository with nothing stored, which is what
// makes a project whose coordinator is off draw its runs rather than nothing.
//
// It is one function because two callers must not read it two ways: `graph
// --json`, which prints it once, and `graph --watch --json`, which prints it
// per change (CG7). A stream whose document differed from the one-shot read's
// for the same data would be a graph that changes shape when a screen switches
// from one to the other.
//
// asOf is when the caller's read of the views was made, and it is the caller's
// to decide because it is not a fact the stored view carries (CG2): the one-shot
// read has none to give — the zero time, and §6.22's `as_of` is null for it —
// while the stream knows its own read's time and fills it.
//
// store is the connection the read goes through, and it is the caller's to open
// for the same reason asOf is the caller's to pass: a caller that reads once
// opens one and drops it, while the stream opens one for its whole life and
// hands the same one here every time (CoordinatorStore, CG7). A store opened is
// an rtdb client built and a token source with it, so a stream that opened one
// per document would read the caller's ADC file and fetch a fresh OAuth token on
// every change, every keep-alive and every retry of a failed read.
func readGraph(ctx context.Context, env *fcli.Cloud, store *coordinator.Store, now, asOf time.Time, warned *bytes.Buffer) (graph.Document, error) {
	window, err := fcli.ParseSince(fcli.LsDefaultSince())
	if err != nil {
		return graph.Document{}, fcli.UserErr("%v", err)
	}
	project, err := coordcli.StoredProjectThrough(ctx, env, store, graphRepos(env), fcli.LsSince(window), warned)
	switch {
	case err == nil:
		src := graph.CoordinatorSource{View: project, AsOf: asOf, Now: now}
		nodes, state, err := src.Nodes()
		if err != nil {
			// The database answered with a record whose identity this build
			// cannot carry: sanitising it would rewrite the key, and a rewritten
			// key is one two things can share (D1356, Sanitise). That is the
			// same class as a node that does not decode — an answer this build
			// cannot read — and §6.22 gives it the same state rather than the
			// refusal the console would draw as "this CLI has no graph".
			return graphUnreadable(now, err, warned), nil
		}
		return graph.Document{
			Version:     graph.DocumentVersion,
			GeneratedAt: now,
			Sources:     []graph.SourceState{state},
			Nodes:       nodes,
			Edges:       []graph.Edge{},
			Warnings:    graphWarningLines(warned),
		}, nil
	case errors.Is(err, coordinator.ErrUnreadable):
		// The database answered, and what the coordinator's subtree holds is a
		// document this build cannot read — a newer sweep's field, a value of
		// another shape. §6.22's `unreadable` is a state of the scope and never
		// a refusal of the whole read, because a refusal is exactly what the
		// console reads as "this CLI has no graph" (T3), and it would say that
		// about a project whose database it can read and whose coordinator has
		// simply written something this build does not know.
		return graphUnreadable(now, err, warned), nil
	default:
		return graph.Document{}, err
	}
}

// graphUnreadable is the document of a scope that answered with something this
// build cannot read (§6.22): one source in the state `unreadable`, the read's
// own sentence as its reason, and no nodes, because a scope whose answer this
// reader cannot read has nothing it can draw — and the state beside the empty
// list is what tells a screen that from a scope with nothing to draw.
//
// Two failures reach it, and they are one class: a stored node whose decoding
// this build refuses (coordinator.ErrUnreadable), and a record whose identity
// sanitising would rewrite, which the source refuses rather than printing a key
// that could merge two things (D1356, Sanitise). Neither is a read that made no
// answer, which is what a refusal is.
func graphUnreadable(now time.Time, reason error, warned *bytes.Buffer) graph.Document {
	return graph.Document{
		Version:     graph.DocumentVersion,
		GeneratedAt: now,
		Sources: []graph.SourceState{{
			Source: graph.Scope{Kind: graph.ScopeCoordinator}.String(),
			State:  graph.SourceUnreadable,
			Reason: reason.Error(),
		}},
		Nodes:    []graph.Node{},
		Edges:    []graph.Edge{},
		Warnings: graphWarningLines(warned),
	}
}

// graphRepos is the repositories the coordinator scope covers: every one the
// project's local config lists, sorted, which is the rule `coordinator status
// --all` selects with.
//
// The graph has no --repo to name one with and no --all to ask for the rest, so
// it always reads the whole project; and a project that lists none is an empty
// document rather than a refusal, because the refusal that rule makes ("name
// one with --repo") names a flag this command does not have, and the scope has
// a word for having nothing to draw.
func graphRepos(env *fcli.Cloud) []string {
	return slices.Sorted(maps.Keys(env.Config().Repos))
}

// writeGraphJSON prints the document indented, as this tree's other --json
// documents are. One object per line is the stream's shape (CG7), not the
// one-shot read's.
func writeGraphJSON(w io.Writer, doc graph.Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// graphTitleRunes bounds the title column of the table: a title is another
// writer's text, and the table is what a person reads beside the document.
const graphTitleRunes = 60

// writeGraphText prints the document as a short table: one line per scope
// saying what its state is, then the nodes grouped by release.
//
// The grouping is F17's: the coordinator's view joins pull requests with the
// runs behind them and holds no relation between two records, so a release is
// the only grouping a reader of it has. The order inside a group is the
// document's own, which is the order --json prints, so the two outputs never
// disagree about it; the groups themselves are in the order the view first
// named them, and the nodes that name no release come last under a heading
// that says so rather than under a blank line.
func writeGraphText(w io.Writer, doc graph.Document) error {
	for _, s := range doc.Sources {
		line := s.Source + ": " + s.State
		if s.AsOf != nil {
			line += " as of " + s.AsOf.UTC().Format(time.RFC3339)
		}
		fmt.Fprintln(w, line)
		if s.Reason != "" {
			fmt.Fprintf(w, "  %s\n", s.Reason)
		}
	}
	if len(doc.Nodes) == 0 {
		fmt.Fprintln(w, "no nodes")
		return nil
	}
	var releases []string
	byRelease := map[string][]graph.Node{}
	for _, n := range doc.Nodes {
		if _, seen := byRelease[n.Release]; !seen {
			releases = append(releases, n.Release)
		}
		byRelease[n.Release] = append(byRelease[n.Release], n)
	}
	releases = slices.DeleteFunc(releases, func(r string) bool { return r == "" })
	if _, none := byRelease[""]; none {
		releases = append(releases, "")
	}
	for _, rel := range releases {
		head := rel
		if head == "" {
			head = "no release"
		}
		fmt.Fprintln(w, head)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, n := range byRelease[rel] {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", n.Kind, graphCell(n.State), n.ID, graphCell(fcli.ClipRunes(n.Title, graphTitleRunes)))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// graphCell is one cell of the table: "-" for a value the source did not give,
// so a column never collapses and a reader can tell an absent value from a
// misaligned row.
func graphCell(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// graphWarningLines is what the read warned about as the document's warnings:
// one entry per line, and no entry at all when it warned about nothing. The
// read writes them a repository at a time, in the selection's own order
// (StoredProject), and Sanitise makes each one a plain bounded line.
func graphWarningLines(warned *bytes.Buffer) []string {
	text := strings.TrimRight(warned.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
