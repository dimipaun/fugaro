// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
)

// loadStoredView reads the fixture the coordinator scope's tests share: a
// stored project view, the document `fugaro coordinator status --from-store
// --all --json` prints (json-contract.md §6.13), with one repository the sweep
// has reconciled and one it has never touched. It is decoded strictly, the way
// every wire format here is read, so a fixture that drifted from
// `coordinator.RepoView` fails here rather than in a test that then asserts
// nothing.
//
// The fixture says it is the document that command prints, and §6.13 pins two
// things about that document's records: their order (work still in flight
// first, then by pull request number, then by run ID) and their `in_flight`.
// Both are `coordinator.Join`'s own output, so each view here is rebuilt from
// its own pull requests and runs and must come back identical. A fixture whose
// records sit in an order the join cannot produce — or whose `in_flight`
// contradicts it — would otherwise be pinned by the tests below as "the view's
// own order", and a reader would take a document the command never prints for
// the shape it prints.
func loadStoredView(t *testing.T) coordinator.StoredProjectView {
	t.Helper()
	data, err := os.ReadFile("testdata/view.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v coordinator.StoredProjectView
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("testdata/view.golden.json is not a stored project view: %v", err)
	}
	for _, rv := range v.Repos {
		var prs []coordinator.PullRequest
		var runs []coordinator.Run
		for _, rec := range rv.View.Records {
			if rec.PR != nil {
				prs = append(prs, *rec.PR)
			}
			runs = append(runs, rec.Runs...)
		}
		if want := coordinator.Join(rv.Repo, prs, runs); !reflect.DeepEqual(want, rv.View) {
			t.Fatalf("testdata/view.golden.json's %s view is not what coordinator.Join makes of its own pull requests and runs, so it is not the document the command prints (json-contract.md §6.13):\n fixture %+v\n command %+v", rv.Repo, rv.View, want)
		}
	}
	return v
}

// coordinatorAt is the clock the coordinator scope's tests read at, so every
// expected time in this file is a literal rather than a call to time.Now.
var coordinatorAt = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// TestCoordinatorNodesPerRepoAndRelease: the coordinator scope draws the stored
// view as nodes — a pull request with its joined runs is one node, and work
// with no pull request yet is the in-flight run itself — carrying the
// repository it belongs to and the release its record declares. A pull request
// the host did not answer for takes the state of the newest run behind it, and
// the same pull request number in two repositories is two nodes, because the
// key is unique within the source and the source is the whole project.
//
// The nodes come out in the view's own order, which is the fixture's: the
// fixture is a document the command printed, so its records are already what
// sortRecords produces — the orphan run first (it has no number to sort by),
// then the numbered ones, then the record the join found out of flight. This
// test is where that order is pinned: a source that re-sorted, or that walked
// the records some other way, draws the same five nodes in another sequence.
//
// Hand mutation this fails on: keying a node by its pull request number alone,
// which makes acme/app#12 and acme/lib#12 one node; dropping the record's
// release, which empties the 0.11.0 expectation; reading the state off the
// pull request alone, which leaves the host-silent node with no state; and
// drawing the records in any order but the view's.
func TestCoordinatorNodesPerRepoAndRelease(t *testing.T) {
	now := coordinatorAt
	nodes, state, err := CoordinatorSource{
		View: loadStoredView(t),
		AsOf: now.Add(-time.Minute),
		Now:  now,
	}.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if state.State != SourceUp {
		t.Errorf("state = %q, want %q (the views were stored a minute ago)", state.State, SourceUp)
	}
	if state.Source != string(ScopeCoordinator) {
		t.Errorf("state.Source = %q, want %q", state.Source, ScopeCoordinator)
	}

	want := []Node{
		{
			Key: "coordinator:acme/app#20261013-140000-eeee", Source: "coordinator", Kind: NodeRun,
			ID: "acme/app#20261013-140000-eeee", Repo: "acme/app", State: "running",
		},
		{
			Key: "coordinator:acme/app#12", Source: "coordinator", Kind: NodePullRequest,
			ID: "acme/app#12", Title: "Add search to the catalogue", Repo: "acme/app", State: "open",
		},
		{
			Key: "coordinator:acme/app#15", Source: "coordinator", Kind: NodePullRequest,
			ID: "acme/app#15", Title: "Release 0.11.0", Repo: "acme/app", State: "open",
			Release: "0.11.0",
		},
		{
			Key: "coordinator:acme/app#17", Source: "coordinator", Kind: NodePullRequest,
			ID: "acme/app#17", Title: "Hand-written change no run opened", Repo: "acme/app",
			State: "succeeded",
		},
		{
			Key: "coordinator:acme/lib#12", Source: "coordinator", Kind: NodePullRequest,
			ID: "acme/lib#12", Title: "Index the catalogue nightly", Repo: "acme/lib", State: "open",
		},
	}
	if len(nodes) != len(want) {
		t.Fatalf("Nodes returned %d nodes, want %d: %+v", len(nodes), len(want), nodes)
	}
	for i, w := range want {
		got := nodes[i]
		if got.Key != w.Key || got.Source != w.Source || got.Kind != w.Kind || got.ID != w.ID ||
			got.Title != w.Title || got.Repo != w.Repo || got.State != w.State || got.Release != w.Release {
			t.Errorf("node %d =\n %+v\nwant\n %+v", i, got, w)
		}
		if got.Updated != nil {
			t.Errorf("node %d carries an update time %v: the stored view has none (contract §6.13)", i, got.Updated)
		}
	}
}

// TestCoordinatorSourceStateIsStaleAfterTwoSweeps: the stored view carries no
// time of its own (contract §6.13), so the caller supplies the sweep's stored
// time, and the scope is stale once that time is older than two sweeps — the
// coordinator has missed two ticks. A caller with no such time claims no age,
// which is the rule the Coordinator screen already keeps: a view with no time
// is not a view that is stale.
//
// Hand mutation this fails on: dropping the age comparison, which makes the
// two-sweeps case read up; and inverting the boundary to >=, which makes the
// exact-two-sweeps case stale.
func TestCoordinatorSourceStateIsStaleAfterTwoSweeps(t *testing.T) {
	view := loadStoredView(t)
	now := coordinatorAt
	cases := []struct {
		name string
		asOf time.Time
		want string
	}{
		{"one sweep old", now.Add(-SweepPeriod), SourceUp},
		{"exactly two sweeps old", now.Add(-2 * SweepPeriod), SourceUp},
		{"a second past two sweeps", now.Add(-2*SweepPeriod - time.Second), SourceStale},
		{"no stored time to claim", time.Time{}, SourceUp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, state, err := CoordinatorSource{View: view, AsOf: tc.asOf, Now: now}.Nodes()
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			if state.State != tc.want {
				t.Fatalf("state = %q, want %q (as of %v, read at %v)", state.State, tc.want, tc.asOf, now)
			}
			switch tc.want {
			case SourceStale:
				if state.Reason == "" {
					t.Error("a stale scope carries no reason")
				}
				if state.AsOf == nil || !state.AsOf.Equal(tc.asOf) {
					t.Errorf("state.AsOf = %v, want %v (the sweep's stored time)", state.AsOf, tc.asOf)
				}
			default:
				if state.Reason != "" {
					t.Errorf("an up scope carries the reason %q", state.Reason)
				}
				if !tc.asOf.IsZero() && (state.AsOf == nil || !state.AsOf.Equal(tc.asOf)) {
					t.Errorf("state.AsOf = %v, want %v", state.AsOf, tc.asOf)
				}
				if tc.asOf.IsZero() && state.AsOf != nil {
					t.Errorf("state.AsOf = %v, want nil: the caller claimed no time", state.AsOf)
				}
			}
		})
	}
}

// TestCoordinatorUnstoredRepoIsAStateNotAnError: a repository the coordinator
// has never reconciled is the contract's `stored: false` case, and the CLI
// answers it rather than refusing — the runs half, with no stored view. The
// scope says so as a state, so a reader can tell a coordinator that has never
// run from one that is running, and the nodes the read did produce are still
// drawn: a scope that is down is a state, not an exit (H3).
//
// Hand mutation this fails on: returning an error for a repository with no
// stored view, which is what the command's own refusal path would do.
func TestCoordinatorUnstoredRepoIsAStateNotAnError(t *testing.T) {
	now := coordinatorAt
	view := coordinator.StoredProjectView{Repos: []coordinator.RepoView{{
		Repo:   "acme/app",
		Stored: false,
		View: coordinator.View{Repo: "acme/app", Records: []coordinator.Record{{
			PR:       &coordinator.PullRequest{Number: 12, Title: "Index the catalogue nightly", State: "open"},
			Runs:     []coordinator.Run{},
			Files:    []string{},
			Services: []string{},
		}}, Collisions: []coordinator.Collision{}},
	}}}
	nodes, state, err := CoordinatorSource{View: view, AsOf: now, Now: now}.Nodes()
	if err != nil {
		t.Fatalf("Nodes refused a repository with no stored view: %v", err)
	}
	if state.State != SourceDown {
		t.Errorf("state = %q, want %q: no repository has a stored view", state.State, SourceDown)
	}
	if state.Reason == "" {
		t.Error("a down scope carries no reason")
	}
	if state.AsOf != nil {
		t.Errorf("state.AsOf = %v, want nil: there is no stored view to date", state.AsOf)
	}
	if len(nodes) != 1 || nodes[0].Key != "coordinator:acme/app#12" {
		t.Errorf("the runs half was not drawn: %+v", nodes)
	}
}

// TestCoordinatorTextIsSanitised: every string the scope puts on a node — a
// pull request's title and state, a record's release — is text someone else
// wrote, so the source sanitises it before the document is printed (H2). A
// repository name is not prose: it is part of the node's identity, so one that
// safetext.Field would rewrite is refused rather than sanitised, because two
// distinct repositories would otherwise share one key (D1356).
//
// Hand mutation this fails on: dropping the sanitise call, which lets the
// escape sequence through into the title, the state and the release.
func TestCoordinatorTextIsSanitised(t *testing.T) {
	const esc = "\x1b[31m"
	now := coordinatorAt
	view := coordinator.StoredProjectView{Repos: []coordinator.RepoView{{
		Repo:   "acme/app",
		Stored: true,
		View: coordinator.View{Repo: "acme/app", Records: []coordinator.Record{
			{
				PR:       &coordinator.PullRequest{Number: 12, Title: esc + "Fix the thing", State: "open" + esc},
				Runs:     []coordinator.Run{},
				Files:    []string{},
				Services: []string{},
			},
			{
				PR:       &coordinator.PullRequest{Number: 13, Title: "Ship it", State: "open"},
				Release:  "0.113" + esc,
				Runs:     []coordinator.Run{},
				Files:    []string{},
				Services: []string{},
			},
		}, Collisions: []coordinator.Collision{}},
	}}}
	nodes, _, err := CoordinatorSource{View: view, AsOf: now, Now: now}.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("Nodes returned %d nodes, want 2", len(nodes))
	}
	for _, c := range []struct{ what, got, want string }{
		{"Nodes[0].Title", nodes[0].Title, "Fix the thing"},
		{"Nodes[0].State", nodes[0].State, "open"},
		{"Nodes[1].Release", nodes[1].Release, "0.113"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
		}
		assertClean(t, c.what, c.got)
	}

	// The repository is the node's identity, so a name Field would rewrite is a
	// refusal and not a rewrite: two repositories must not share one key.
	bad := view
	bad.Repos[0].Repo = "acme/app\x07"
	if _, _, err := (CoordinatorSource{View: bad, AsOf: now, Now: now}).Nodes(); err == nil {
		t.Error("Nodes accepted a repository whose name safetext.Field would change")
	}
}
