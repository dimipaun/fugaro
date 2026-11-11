// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package cli is `fugaro graph` (CG3 and CG5 of
// docs/cli-output-console-graph/implementation-plan-graph.md): the console's
// dependency graph, printed as the versioned document ee/graph defines.
//
// It is a paid command and lives in ee/ for that reason (D1362, F26): its
// `all` scope is the coordinator's stored view, which is the Coordinator tab's
// own read, and one scope per orchestrator the installation has. ee/register
// registers it and this package never registers itself — only that package may
// call cli.RegisterCommand (§5.5, held by internal/eeboundary).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	coordcli "github.com/dimipaun/fugaro/ee/coordinator/cli"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Three properties are this command rather than niceties around it:
//
//   - The scope is refused before anything is read. The value reaches argv from
//     the console's renderer, so it is parsed against graph's closed pattern as
//     the command's first statement (H1): a value that is not one of the four
//     shapes never reaches a path, another flag or a read. A scope that names
//     nothing — an orchestrator id neither the database nor this machine's cache
//     knows — is refused too, with a sentence rather than an empty graph,
//     because a screen must be able to tell "nothing here" from "not asked"
//     (contract §6.22). That refusal is the one read this command makes *in
//     order to* refuse: an id this installation does not have can only be told
//     apart from one that is merely down by looking.
//   - It needs no checkout. The coordinator scope is the stored view — the read
//     `coordinator status --from-store` makes, through the caller's own
//     credential and no key of the host's — and the orchestrator scopes are the
//     nodes under `orchestrator/`, read through the same credential (D1357,
//     T2), so this command opens no host and paints in a console child whose
//     cwd is nothing in particular. The one fact it can take from the working
//     directory is a fallback, and it is not this command's: the provider a
//     repository's slug is keyed by comes from the project's own config, and the
//     checkout is consulted only when that entry leaves it out (repoSlug). So a
//     project whose config names its providers — every repository the CLI's own
//     onboarding records carries one — is graphed with no checkout at all, and a
//     repository the config leaves out is refused, naming that entry, rather
//     than graphed from a guess. The plan scope keeps its checkout and its card;
//     this command has neither (D1356).
//   - It prints one document. A refused read is an error with no document at
//     all, and a scope that is down is a state inside the document (H3), so a
//     reader tells the two apart by exactly that. A read that merely failed —
//     an unreachable database, a node this release cannot decode — is the second
//     of those and never the first: a console draws a scope it cannot reach and
//     must not be told that this CLI has no graph at all.

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
			"--source selects the scope, and the set is closed. all (the default) is\n" +
			"every scope that exists: the coordinator's stored view and one scope per\n" +
			"orchestrator this installation has. coordinator is that view alone;\n" +
			"orchestrator:<id> is one orchestrator's node under /orchestrator, its work\n" +
			"items as nodes and their depends_on as edges; local:<id> is the same node\n" +
			"read as one this machine's daemon drives, falling back to the daemon's\n" +
			"cached brief when the node cannot be read. An id is one lower-case letter or\n" +
			"digit, then up to 39 more of those or a hyphen, and nothing else: anything\n" +
			"that does not match is refused before anything is read, and so is an id the\n" +
			"project's database does not hold, with a sentence naming it and pointing at\n" +
			"fugaro orchestrator ls. Which ids exist is what that listing asks — a key\n" +
			"under /orchestrator — and not whether a state node was written, so an id it\n" +
			"lists is a scope even with no state yet: a daemon that has beaten its lease\n" +
			"and saved none is down rather than refused. A local:<id> is refused when this\n" +
			"machine holds no cached brief for it and there is no node to read, because\n" +
			"the cache stands in for the local scope alone: an orchestrator:<id> the\n" +
			"database does not hold is refused even on a machine holding a brief for it.\n\n" +
			"The coordinator's scope is every repository the local config lists, and a\n" +
			"repository the sweep has never reconciled is read as its runs alone — the\n" +
			"half of the view that needs no credential — rather than dropped. Which\n" +
			"half it is is a fact about the repository and not about the scope: the\n" +
			"scope's state is down when the project lists repositories and the sweep\n" +
			"has stored a view for none of them, and up once it has stored one, beside\n" +
			"records that may still be the runs half.\n\n" +
			"With --json the document is printed as the console reads it: its version,\n" +
			"when it was assembled, one entry per scope in sources, the nodes, the edges\n" +
			"(the coordinator's view has none; an orchestrator's are its work items'\n" +
			"depends_on) and the warnings. Without it, a short table: each scope's state,\n" +
			"then its nodes grouped by release, then the edges.\n\n" +
			"--watch --json prints a whole document per change instead of one and\n" +
			"exits, one compact object per line: the first snapshot at once, then one\n" +
			"document for every change of the scopes it covers, at most one a second,\n" +
			"and a document every 30 seconds while nothing changes. Every document\n" +
			"carries the stream's own id and a sequence that rises within it, so a\n" +
			"reader reads a new id as a whole new snapshot rather than comparing its\n" +
			"sequence with the one before. It subscribes once per scope subtree — the\n" +
			"coordinator's and the orchestrator's for all, or the one node a named\n" +
			"orchestrator scope reads — and a scope it cannot reach is a state in the\n" +
			"document rather than an exit. A local scope falls back to the daemon's\n" +
			"cached brief, which the stream re-reads on every document it writes.\n" +
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

	// The connection every scope of this read goes through: the coordinator's
	// view through the store over it, and an orchestrator's node through the
	// client itself. One client and one credential serve both — the refusal a
	// project with no database gets is CoordinatorDB's own sentence — because a
	// second one would mint a second credential for the same read.
	db, err := coordcli.CoordinatorDB(ctx, env.Config())
	if err != nil {
		return err
	}
	if o.watch {
		return runGraphWatch(ctx, cmd.OutOrStdout(), env, db, scope)
	}

	// The read's warnings are buffered rather than written as they are made:
	// with --json they are the document's own `warnings` (a reader of the
	// document has no stderr to look at), and without it they go to stderr
	// below. Either way they are carried once.
	var warned bytes.Buffer
	now := time.Now()
	// A one-shot read has no time of its own to give for the stored view (the
	// sweep's own design), so its `as_of` is null and the scope claims no age:
	// the zero time is the caller saying it has none (contract §6.22).
	doc, err := readGraph(ctx, env, coordinator.NewStore(db), db, scope, now, time.Time{}, &warned)
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

// readGraph is the read both outputs make: every scope the caller asked for, as
// one document, read through the caller's own credential and holding no key of
// the host's, and the refusal that ends a read this build cannot make.
//
// `all` is the union of the scopes that exist — the coordinator's stored view
// and one scope per orchestrator the installation has (D1356) — each under its
// own name, which is the source half of every key it produces and a value a
// console may send straight back as --source. `coordinator`,
// `orchestrator:<id>` and `local:<id>` are each one of them alone.
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
// while the stream knows its own read's time and fills it. It is the
// coordinator's alone: an orchestrator's node carries no time either, so an
// orchestrator scope's `as_of` is the cached brief's stamp when there is one and
// null otherwise.
//
// store and db are the connection the read goes through, and they are the
// caller's to open for the same reason asOf is the caller's to pass: a caller
// that reads once opens one and drops it, while the stream opens one for its
// whole life and hands the same one here every time (CG7). A store opened is an
// rtdb client built and a token source with it, so a stream that opened one per
// document would read the caller's ADC file and fetch a fresh OAuth token on
// every change, every keep-alive and every retry of a failed read. db is that
// same client, which the orchestrator scopes are read through: one connection,
// and the one credential minted for it, serve every scope of the document.
func readGraph(ctx context.Context, env *fcli.Cloud, store *coordinator.Store, db *rtdb.Client, scope graph.Scope, now, asOf time.Time, warned *bytes.Buffer) (graph.Document, error) {
	sources := []graph.SourceState{}
	nodes := []graph.Node{}
	releases := []graph.Release{}
	edges := []graph.Edge{}

	covers, orchestrators, err := graphSources(ctx, env, db, scope, warned)
	if err != nil {
		return graph.Document{}, err
	}
	if covers {
		state, ns, rs, es, err := coordinatorScope(ctx, env, store, now, asOf, warned)
		if err != nil {
			return graph.Document{}, err
		}
		sources = append(sources, state)
		nodes = append(nodes, ns...)
		releases = append(releases, rs...)
		edges = append(edges, es...)
	}
	for _, src := range orchestrators {
		ns, es, state, err := src.Nodes()
		if err != nil {
			// Under `all`, every id this read enumerates has an answer of its
			// own: the database's listing names it, or this machine's cache
			// does. A source with none is therefore one the enumeration lost
			// between itself and this read — a scope that is gone, and not a
			// reason to refuse the rest of the document, which the console
			// draws as "this CLI has no graph" (H3, T3). It is left out with
			// the sentence saying so. A scope named on the command line is the
			// other case: it is refused, because a reader that asked for one
			// scope and got a document without it could not tell that from an
			// empty one (§6.22).
			if scope.Kind == graph.ScopeAll && errors.Is(err, graph.ErrNoOrchestrator) {
				_, _ = fmt.Fprintf(warned, "one orchestrator scope is left out of this graph: %s\n", safetext.OneLine(err.Error()))
				continue
			}
			return graph.Document{}, fcli.UserErr("%v", err)
		}
		sources = append(sources, state)
		nodes = append(nodes, ns...)
		edges = append(edges, es...)
	}
	return graph.Document{
		Version:     graph.DocumentVersion,
		GeneratedAt: now,
		Sources:     sources,
		Releases:    releases,
		Nodes:       nodes,
		Edges:       edges,
		Warnings:    graphWarningLines(warned),
	}, nil
}

// graphSources is the scopes a read covers: whether the coordinator's stored
// view is one of them, and one source per orchestrator id the scope names.
//
// The coordinator's subtree is read for `all` and `coordinator`, and for those
// two only: the stored view is a project-wide read of every repository the
// config lists, and a caller that asked for one orchestrator did not ask for it.
//
// An orchestrator id is read through the caller's own credential (D1357, T2) —
// the same client the store is built on — and the id's own node is
// `orchestrator/<id>/state`, the path the writer builds (orchestrator.PathState)
// and never a second spelling of it.
//
// Which of the two kinds an id is comes from CG4's rule and from nowhere else
// (D1358): `local` is an orchestrator **this machine** runs, which is exactly
// what a cached brief under this machine's cache directory says, and `central`
// — here, a plain `orchestrator:` scope — is one only the project's database
// holds. That is why `ls` and this reader agree about the kind of an id without
// asking each other: they read the same two things.
//
// Whether the database holds an id *at all* is the third read, and it is `ls`'s
// own: the keys under `orchestrator/` with no values (orchestrator.NodeIDs). An
// id it names is an orchestrator this installation has, whatever its state node
// says — `orchestrator run` writes the lease node before the cycle that saves a
// state — so one whose state is not written yet is a scope that is down and not
// a scope that does not exist (H3, §6.22). `all` makes that read once, by
// enumerating, and carries the answer into every source it builds; an
// `orchestrator:<id>` named on the command line makes it only when its state
// node is not there, because a node that was read has answered already
// (databaseHolds).
//
// The read here is a failed read and not a refusal in the ordinary case: a
// database that will not answer reaches the source as `ReadErr` and comes back
// as a scope that is down (H3), while a read this CLI is *refused* is returned
// as it is — terminal, exit 1, the one failure the console draws as "this CLI
// has no graph" (T3).
//
// A cache this release cannot read is neither of those under `all`: it takes
// away one of the two ways an id is named and not the scopes the database
// answers for, so the read warns about it and carries on. Only a scope *named*
// on the command line is refused over it, because `local:<id>` is a request for
// that very file (cachedBriefs).
func graphSources(ctx context.Context, env *fcli.Cloud, db *rtdb.Client, scope graph.Scope, warned *bytes.Buffer) (bool, []graph.OrchestratorSource, error) {
	switch scope.Kind {
	case graph.ScopeCoordinator:
		return true, nil, nil
	}
	if scope.Kind == graph.ScopeOrchestrator || scope.Kind == graph.ScopeLocal {
		src, err := orchestratorSource(ctx, env, db, scope.ID, scope.Kind == graph.ScopeLocal, nil)
		if err != nil {
			return false, nil, err
		}
		// The one question this read cannot answer by itself. A scope named on
		// the command line is asked whether the database holds the id at all
		// when its state node is not there, and only then: a node that was read
		// has already answered, and the question costs a shallow read of the ids
		// under `orchestrator/` — the one `fugaro orchestrator ls` lists them
		// with. It decides between a scope that is down — an orchestrator this
		// installation has whose state is not written yet — and the refusal for
		// an id nothing holds (§6.22).
		//
		// The local kind is asked nothing: `local:<id>` is a request for this
		// machine's daemon, a cached brief is what says this machine runs one,
		// and the cache stands in for that scope alone — so an id with no brief
		// here is refused whatever the database holds (§6.22, D1358).
		if src.Absent() && scope.Kind == graph.ScopeOrchestrator {
			held, err := databaseHolds(ctx, env, db, scope.ID)
			if err != nil {
				return false, nil, err
			}
			src.Listed = held
		}
		return false, []graph.OrchestratorSource{src}, nil
	}
	// `all`: the ids the database holds and the ids this machine has a cached
	// brief for, which is the union `fugaro orchestrator ls` lists.
	caches, err := cachedBriefs()
	if err != nil {
		// A file on this machine this release cannot read is one way of naming
		// the orchestrators a machine runs, and the database is the other: the
		// ids it holds are still read, as the central scopes they are, and the
		// ones only the cache knew about are left out with the warning saying
		// so. `all` is the console's default read, and a console told "this CLI
		// has no graph" over one stale cache file would draw nothing at all
		// (H3, T3).
		_, _ = fmt.Fprintf(warned, "the orchestrators only this machine runs are left out of this graph: %s\n", safetext.OneLine(err.Error()))
		caches = nil
	}
	ids, err := orchestrator.NodeIDs(ctx, db)
	if err != nil {
		return false, nil, env.DBErr(err, false)
	}
	held := make(map[string]bool, len(ids))
	kind := make(map[string]bool, len(ids)+len(caches))
	for _, id := range ids {
		held[id] = true
		kind[id] = false
	}
	for id := range caches {
		kind[id] = true
	}
	sorted := slices.Sorted(maps.Keys(kind))
	out := make([]graph.OrchestratorSource, 0, len(sorted))
	for _, id := range sorted {
		src, err := orchestratorSource(ctx, env, db, id, kind[id], caches)
		if err != nil {
			return false, nil, err
		}
		// The enumeration has just answered this for every id: a key under
		// `orchestrator/<id>` is there, whatever its state node says. It is
		// what makes an id whose state is not written yet a scope that is down
		// rather than a scope left out of the document.
		src.Listed = held[id]
		out = append(out, src)
	}
	return true, out, nil
}

// databaseHolds reports whether the project's database holds an id at all: one
// shallow read of the ids under `orchestrator/`, which is the read `fugaro
// orchestrator ls` lists them with (orchestrator.NodeIDs, D1358). An
// `orchestrator:<id>` named on the command line is its only caller, and it asks
// only when its state node is not there (graphSources); a read this CLI is
// refused is classified the way the enumeration's own failure is, because it is
// the same read (H3, T3).
func databaseHolds(ctx context.Context, env *fcli.Cloud, db *rtdb.Client, id string) (bool, error) {
	ids, err := orchestrator.NodeIDs(ctx, db)
	if err != nil {
		return false, env.DBErr(err, false)
	}
	return slices.Contains(ids, id), nil
}

// orchestratorSource reads one id's node and builds its source: the state
// document, or why it could not be read, and the cached brief this machine has
// for it when it has one.
//
// caches is what cachedBriefs returned, and it is passed in rather than read
// again because `all` builds one source per id and one directory listing serves
// them all. An id named on the command line passes nil, having read the caches
// only when the kind it asked for needs them (`local:`), and reads its own
// stamp below.
func orchestratorSource(ctx context.Context, env *fcli.Cloud, db *rtdb.Client, id string, local bool, caches map[string]time.Time) (graph.OrchestratorSource, error) {
	src := graph.OrchestratorSource{ID: id, Local: local}
	if local {
		if caches == nil {
			each, err := cachedBriefs()
			if err != nil {
				return graph.OrchestratorSource{}, err
			}
			caches = each
		}
		src.CachedAt = caches[id]
	}
	node, err := orchestratorNode(ctx, db, id)
	if err != nil {
		// A refusal is the one failure this command does not carry as a state:
		// the caller may not read this subtree at all, which is not a scope
		// that is down but a read this CLI cannot make (H3, T3).
		classified := env.DBErr(err, false)
		if fcli.ExitCode(classified) == fcli.ExitUserError {
			return graph.OrchestratorSource{}, classified
		}
		src.ReadErr = classified
		return src, nil
	}
	src.Node = node
	return src, nil
}

// orchestratorNode reads one id's state node, and nil when the database holds
// none: a not-found node is a scope this read has an answer about, not a
// failure. The decode is the source's (graph.OrchestratorSource): what this
// reader does here is the request and its path.
func orchestratorNode(ctx context.Context, db *rtdb.Client, id string) (json.RawMessage, error) {
	path, err := orchestrator.PathState(id)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return raw, nil
}

// cachedBriefs is the daemon's cached briefs on this machine, keyed by
// orchestrator id. It reads the environment and the platform because that is
// how the cache's directory is placed (orchestrator.CachedBriefs, D1243) — the
// reader is internal/orchestrator's and this is not a second one.
//
// An error here is what a caller makes of it, and the two callers differ: a
// scope named `local:<id>` is a request for this file, so it refuses with the
// sentence `fugaro orchestrator ls` refuses it with (CG4) — reporting the id as
// merely down would hide a file a person can see and fix — while `all` warns
// and reads the scopes the database answers for (graphSources), because one
// file this release cannot read is not every scope's failure.
func cachedBriefs() (map[string]time.Time, error) {
	briefs, err := orchestrator.CachedBriefs(os.Getenv, runtime.GOOS)
	if err != nil {
		return nil, fcli.UserErr("the daemon's cached briefs cannot be listed: %v", err)
	}
	out := make(map[string]time.Time, len(briefs))
	for _, b := range briefs {
		out[b.ID] = b.At
	}
	return out, nil
}

// coordinatorScope is the coordinator's own part of a document: the stored view
// as its state and its nodes, and the project's roadmap record as the task
// nodes, release boxes and cross-plan edges beside them (GV4.1). The view is
// read exactly as `coordinator status --from-store --all` reads it — the same
// store, the same credential and the same runs-half fallback for a repository
// with nothing stored, which is what makes a project whose coordinator is off
// draw its runs rather than nothing — and the record is read from the same store
// with the same credential (D2117, `coordinator.Store.ReadRoadmap`).
//
// Two answers this build cannot read become the scope's state and never a
// refusal: a stored node the decoding refuses (coordinator.ErrUnreadable), and
// a record whose identity sanitising would rewrite, which the source refuses
// rather than printing a key that could merge two things (D1356, Sanitise).
// Both are `unreadable` (§6.22), because a refusal is exactly what the console
// reads as "this CLI has no graph" (T3) and it would say that about a project
// whose database this CLI can read and whose coordinator has simply written
// something this build does not know.
//
// **A record this build could not read is a warning and not a state**: the
// stored view above it was read, so the scope is not unreadable, and the record
// is the newer half of the same database. A record that answers with documents
// this build cannot decode (`Roadmap.Unreadable`) and one whose read failed are
// both said in a sentence beside the figures the scope does have — the same door
// the rest of the read's sentences take (`graphWarningLines`), and the reason
// the count exists is exactly this: "the record holds nothing" and "the record
// holds something this build cannot read" must not look alike (§6.22).
func coordinatorScope(ctx context.Context, env *fcli.Cloud, store *coordinator.Store, now, asOf time.Time, warned *bytes.Buffer) (graph.SourceState, []graph.Node, []graph.Release, []graph.Edge, error) {
	source := graph.Scope{Kind: graph.ScopeCoordinator}.String()
	window, err := fcli.ParseSince(fcli.LsDefaultSince())
	if err != nil {
		return graph.SourceState{}, nil, nil, nil, fcli.UserErr("%v", err)
	}
	project, err := coordcli.StoredProjectThrough(ctx, env, store, graphRepos(env), fcli.LsSince(window), warned)
	switch {
	case err == nil:
		src := graph.CoordinatorSource{View: project, AsOf: asOf, Now: now}
		nodes, state, err := src.Nodes()
		if err != nil {
			return unreadableScope(source, err), nil, nil, nil, nil
		}
		record := recordScope(ctx, store, warned)
		return state, append(nodes, record.Nodes...), record.Releases, record.Edges, nil
	case errors.Is(err, coordinator.ErrUnreadable):
		return unreadableScope(source, err), nil, nil, nil, nil
	default:
		return graph.SourceState{}, nil, nil, nil, err
	}
}

// recordScope is the project's roadmap record as the graph draws it, and the
// sentences its read owes a reader.
//
// **A read that failed is not the whole read's failure**: the document's other
// scopes answered, and the coordinator's stored view beside it did, so the
// failure is a part of the picture that is missing and it is said rather than
// refused. That is the same rule the orchestrator scopes follow for an id the
// enumeration lost (`readGraph`), and the one difference is that this half's
// absence is always worth a sentence, because a project with published plans
// draws them here and nowhere else.
func recordScope(ctx context.Context, store *coordinator.Store, warned *bytes.Buffer) graph.Record {
	empty := graph.Record{Nodes: []graph.Node{}, Releases: []graph.Release{}, Edges: []graph.Edge{}}
	record, err := store.ReadRoadmap(ctx)
	if err != nil {
		// safetext.OneLine for the same reason every other sentence here uses
		// it: the text is another program's and the console draws it as one line.
		_, _ = fmt.Fprintf(warned, "the roadmap record is left out of this graph: %s\n", safetext.OneLine(err.Error()))
		return empty
	}
	if record.Unreadable > 0 {
		_, _ = fmt.Fprintf(warned, "this build cannot read %d of the roadmap record's documents\n", record.Unreadable)
	}
	drawn, err := graph.CoordinatorRecordSource{Record: record}.Draw()
	if err != nil {
		_, _ = fmt.Fprintf(warned, "the roadmap record is left out of this graph: %s\n", safetext.OneLine(err.Error()))
		return empty
	}
	return drawn
}

// unreadableScope is one scope that answered with something this build cannot
// read (§6.22): the state, the read's own sentence as its reason, and no nodes,
// because a scope whose answer this reader cannot read has nothing it can draw —
// and the state beside the empty list is what tells a screen that from a scope
// with nothing to draw.
func unreadableScope(source string, reason error) graph.SourceState {
	return graph.SourceState{Source: source, State: graph.SourceUnreadable, Reason: reason.Error()}
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
//
// The edges are the last thing, and only when there are any: a document of the
// coordinator's scope alone has none (F17), and a section headed "edges" with
// nothing under it would say the opposite of what it means.
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
		return writeGraphEdges(w, doc)
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
	return writeGraphEdges(w, doc)
}

// writeGraphEdges prints the document's edges, one line each, under the nodes
// they relate: an edge is a relation between two keys, and the keys are what
// identifies the two ends to a reader who has just read the table above.
//
// One line per edge and no grouping: an orchestrator's edges are its work
// items' `depends_on` (CG5), which is one relation kind, and a reader that
// wanted it grouped by kind would be reading the document rather than the
// table.
func writeGraphEdges(w io.Writer, doc graph.Document) error {
	if len(doc.Edges) == 0 {
		return nil
	}
	fmt.Fprintln(w, "edges")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, e := range doc.Edges {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", graphCell(e.Kind), graphCell(e.From), graphCell(e.To))
	}
	return tw.Flush()
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
