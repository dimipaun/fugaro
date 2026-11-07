// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The coordinator scope: the view the coordinator's sweep stored for each of
// the project's repositories, as graph nodes. It is CG2 of
// docs/cli-output-console-graph/implementation-plan-graph.md, and it is the
// first source of a document — `fugaro graph --json` with no `--source`, or
// with `--source coordinator`, draws exactly this (CG3).
//
// Three things it is not, each of them a fact about the data rather than a
// choice here:
//
//   - **No edges.** The view joins pull requests to the runs behind them and
//     collides records over the paths they touch; it holds no relation between
//     two records, so a document drawn from it alone carries no edge (F17).
//     Edges arrive with the orchestrator scopes, whose state has `depends_on`
//     (D1357, CG5).
//   - **A pull-request-and-run view, not a task graph.** Its nodes are the
//     project's pull requests and the in-flight runs that have opened none.
//     The plan tasks the plan scope draws are a different source with a
//     different key namespace, and D1356 keeps both.
//   - **It reads nothing.** The caller has already read the views through
//     `coordinator.Store` (`ee/coordinator/state.go`) with its own credential —
//     `fugaro coordinator status --from-store`'s read, `roles/firebasedatabase.viewer`
//     and no key of the host's — for the repositories the local config lists,
//     which is the CLI's own `coordinatorRepos` rule. This package stays what
//     its doc says it is: data, with no connection and no clock of its own.

// SweepPeriod is how often the coordinator's sweep runs: the Scheduler job
// `fugaro update --coordinator` creates triggers the aggregation job every 15
// minutes (reconciliation F16). The number is the CLI's own help text
// (`internal/cli/init.go`, `--coordinator`), and it is named here because the
// scope's staleness is measured in sweeps.
const SweepPeriod = 15 * time.Minute

// staleAfter is when the coordinator's stored data is too old to present as
// current: two sweeps. One sweep's worth of age is the ordinary case — the
// view is replaced once per tick and read between ticks — so only a
// coordinator that has missed two ticks is stale.
const staleAfter = 2 * SweepPeriod

// CoordinatorSource is the coordinator scope: the stored project view the
// caller read, and the two times the caller read it at. It holds no store, no
// client and no credential, so building it is not a read and testing it needs
// no fake.
type CoordinatorSource struct {
	// View is every repository's stored view, in the order the caller read
	// them. A repository with no stored node is the contract's `stored: false`
	// case — the runs half, which the read answers rather than refusing — and
	// it is a state of the scope rather than an error. A repository the sweep
	// could not reconcile carries its marker (`RepoView.Stale`) beside it.
	View coordinator.StoredProjectView
	// AsOf is when the sweep stored these views: the age the caller can give,
	// and never a time the read itself carries. The stored view carries no time
	// of its own — it is a function of its inputs (ee/coordinator/sweep.go),
	// which is why the Coordinator screen already claims no age for it
	// (COORDINATOR_NO_AGE_WORD) — so the time is the caller's to give, and the
	// zero time is the caller saying it has none: the scope then claims no age
	// and is never stale by age. The one age fact the read does carry is the
	// sweep's own marker, and it is not this field: see `marked`.
	AsOf time.Time
	// Now is when the caller read the views.
	Now time.Time
}

// Nodes returns the scope's nodes and the scope's own state, in the view's own
// order: every repository as the caller read it, and every record as the view
// put it (work in flight first, then by pull request number, then by run ID).
// The order is the view's rather than this function's, so the same inputs give
// the same document whichever reader produced them.
//
// A record becomes one node. A pull request is a pull request node, keyed by
// its number *and its repository* — the key is unique within one source and
// the source is the whole project, so `acme/app#12` and `acme/lib#12` are two
// nodes and not one. A record with no pull request is the in-flight run
// itself, keyed by the run's id, which is the work "somebody is on this" that
// has produced no pull request yet. Its state is the host's own word when the
// host answered for the pull request, and otherwise the newest run's status:
// runs come newest first (`coordinator.Record.Runs`), and the newest is the
// one the view itself names when a record has no pull request (`Record.Label`).
// A record with neither a pull request nor a run is the one shape the join
// never produces, and it draws nothing.
//
// The scope's state is the worst fact the read carries, in this order. A
// repository the sweep could not reconcile — `RepoView.Stale`, the marker the
// sweep leaves beside a view it could not recompute — makes the scope stale
// however fresh the caller's own time is, because that repository's records
// are the last ones a sweep could compute and a coordinator failing for hours
// is not up; the reason reads the marker's own words. Then a project none of
// whose repositories has a stored view is down. Then a caller-supplied time
// older than two sweeps is stale. Otherwise the scope is up.
//
// It refuses an identity that sanitising would rewrite — a repository or a run
// id carrying a control character, a bidi override or a run of spaces — rather
// than printing a document whose keys are wrong (Sanitise, D1356). Everything
// else on a node is prose and is sanitised.
func (s CoordinatorSource) Nodes() ([]Node, SourceState, error) {
	source := Scope{Kind: ScopeCoordinator}.String()
	state := SourceState{Source: source}
	nodes := []Node{}
	stored := 0
	for _, rv := range s.View.Repos {
		if rv.Stored {
			stored++
		}
		repo := rv.Repo
		if repo == "" {
			repo = rv.View.Repo
		}
		for _, rec := range rv.View.Records {
			n, ok := coordinatorNode(source, repo, rec)
			if !ok {
				continue
			}
			if err := n.sanitise(); err != nil {
				return nil, SourceState{}, err
			}
			nodes = append(nodes, n)
		}
	}

	marked := s.marked()
	switch {
	case len(marked) > 0:
		// The marker is the read's own statement that this repository could not
		// be reconciled, and it outranks the caller's time: it says the data is
		// known-incomplete, where the caller's time only says when it was
		// fetched. Its own words are the reason and no age is derived from them
		// — the marker's Since is when the failures began, not when the view
		// beside it was stored, and the Coordinator screen draws it exactly as
		// stored for the same reason.
		state.State = SourceStale
		state.Reason = "the sweep has not reconciled " + safetext.FieldListLine(marked)
	case len(s.View.Repos) > 0 && stored == 0:
		// Nothing is stored: the coordinator has never reconciled a repository
		// of this project, which is a state of the scope and not a failed read
		// — the read succeeded and answered with the runs half (contract §6.13).
		state.State = SourceDown
		state.Reason = "the coordinator has no stored view for any of the project's repositories"
	case !s.AsOf.IsZero() && s.Now.Sub(s.AsOf) > staleAfter:
		state.State = SourceStale
		state.Reason = fmt.Sprintf("the coordinator's stored view is more than %s old", staleAfter)
	default:
		state.State = SourceUp
	}
	if state.State != SourceDown && !s.AsOf.IsZero() {
		at := s.AsOf
		state.AsOf = &at
	}
	return nodes, state, nil
}

// marked is the repositories the sweep could not reconcile, each one as its
// marker reads: the repository, the marker's own time and the marker's own
// reason. It is the one age fact a stored view carries, and it is a statement
// rather than a measurement — the marker says since when the sweep has been
// failing, never how old the view beside it is.
//
// The marker's reason is another writer's text, so the caller prints these
// through safetext: they are the values, and FieldListLine is what bounds how
// many of them one sentence of this program's carries.
func (s CoordinatorSource) marked() []string {
	var out []string
	for _, rv := range s.View.Repos {
		if rv.Stale == nil {
			continue
		}
		repo := rv.Repo
		if repo == "" {
			repo = rv.View.Repo
		}
		out = append(out, fmt.Sprintf("%s since %s: %s", repo, rv.Stale.Since.UTC().Format(time.RFC3339), rv.Stale.Reason))
	}
	return out
}

// coordinatorNode is one record as a node, and false for a record with no
// identity at all: one with neither a pull request nor a run is nothing a
// reader could draw or key, and the join never produces one.
func coordinatorNode(source, repo string, rec coordinator.Record) (Node, bool) {
	n := Node{Source: source, Repo: repo, Release: rec.Release}
	switch {
	case rec.PR != nil && rec.PR.Number > 0:
		n.Kind = NodePullRequest
		n.ID = fmt.Sprintf("%s#%d", repo, rec.PR.Number)
		n.Title = rec.PR.Title
		n.State = rec.PR.State
	case len(rec.Runs) > 0:
		n.Kind = NodeRun
		n.ID = fmt.Sprintf("%s#%s", repo, rec.Runs[0].ID)
	default:
		return Node{}, false
	}
	if n.State == "" && len(rec.Runs) > 0 {
		n.State = rec.Runs[0].Status
	}
	n.Key = NodeKey(n.Source, n.ID)
	return n, true
}
