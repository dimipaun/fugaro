// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
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
// Both are `coordinator.Join`'s own output, so each view here is rebuilt the
// way the command builds it and must come back identical: a repository with a
// stored view is the join of that view's own pull requests and runs, and one
// with none is the runs half — `coordinator.Join(repo, nil, runs)`, which is
// the only shape the fallback produces, so a record there can carry no title,
// no state and no branch, and its pull request is the number a run names. A
// fixture that drifts from either — records in an order the join cannot
// produce, an `in_flight` that contradicts it, a pull request field the runs
// half never fills — would otherwise be pinned by the tests below as "the
// view's own order", and a reader would take a document the command never
// prints for the shape it prints.
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
		if rv.Stored {
			for _, rec := range rv.View.Records {
				if rec.PR != nil {
					prs = append(prs, *rec.PR)
				}
			}
		}
		var runs []coordinator.Run
		for _, rec := range rv.View.Records {
			runs = append(runs, rec.Runs...)
		}
		if want := coordinator.Join(rv.Repo, prs, runs); !reflect.DeepEqual(want, rv.View) {
			t.Fatalf("testdata/view.golden.json's %s view (stored: %v) is not what the command makes of its own pull requests and runs, so it is not the document the command prints (json-contract.md §6.13):\n fixture %+v\n command %+v", rv.Repo, rv.Stored, rv.View, want)
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
			ID: "acme/lib#12", Repo: "acme/lib", State: "succeeded",
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
// The one age fact the read does carry is the sweep's own marker
// (`coordinator.RepoView.Stale`), and it outranks the caller's time: a
// repository the sweep could not reconcile holds the records of the last sweep
// that could compute them, so the scope is stale however recently it was read
// — a coordinator that has been failing for hours is not up. The reason reads
// the marker's own words, and no age is derived from them: the marker says
// since when the failures began, not how old the view beside it is.
//
// Hand mutation this fails on: dropping the age comparison, which makes the
// two-sweeps case read up; inverting the boundary to >=, which makes the
// exact-two-sweeps case stale; and ignoring the marker, which reports a
// repository the sweep has been failing on as up.
func TestCoordinatorSourceStateIsStaleAfterTwoSweeps(t *testing.T) {
	now := coordinatorAt
	marker := &coordinator.Stale{Since: now.Add(-6 * time.Hour), Reason: coordinator.StalePullRequests}
	cases := []struct {
		name string
		asOf time.Time
		mark *coordinator.Stale
		want string
	}{
		{"one sweep old", now.Add(-SweepPeriod), nil, SourceUp},
		{"exactly two sweeps old", now.Add(-2 * SweepPeriod), nil, SourceUp},
		{"a second past two sweeps", now.Add(-2*SweepPeriod - time.Second), nil, SourceStale},
		{"no stored time to claim", time.Time{}, nil, SourceUp},
		{"a repository the sweep could not reconcile", now.Add(-time.Minute), marker, SourceStale},
		{"a repository the sweep could not reconcile, with no age claimed", time.Time{}, marker, SourceStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := loadStoredView(t)
			view.Repos[0].Stale = tc.mark
			_, state, err := CoordinatorSource{View: view, AsOf: tc.asOf, Now: now}.Nodes()
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			if state.State != tc.want {
				t.Fatalf("state = %q, want %q (as of %v, read at %v, marked %v)", state.State, tc.want, tc.asOf, now, tc.mark != nil)
			}
			if tc.want == SourceStale {
				if state.Reason == "" {
					t.Error("a stale scope carries no reason")
				}
			} else if state.Reason != "" {
				t.Errorf("an up scope carries the reason %q", state.Reason)
			}
			if tc.asOf.IsZero() {
				if state.AsOf != nil {
					t.Errorf("state.AsOf = %v, want nil: the caller claimed no time", state.AsOf)
				}
			} else if state.AsOf == nil || !state.AsOf.Equal(tc.asOf) {
				t.Errorf("state.AsOf = %v, want %v (the caller's time, which the marker does not replace)", state.AsOf, tc.asOf)
			}
			if tc.mark != nil {
				for _, want := range []string{"acme/app", tc.mark.Reason, tc.mark.Since.UTC().Format(time.RFC3339)} {
					if !strings.Contains(state.Reason, want) {
						t.Errorf("the reason %q does not name %q: the marker is the age fact the read carries", state.Reason, want)
					}
				}
			}
			assertClean(t, "state.Reason", state.Reason)
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
// The record here is the one the runs half can hold: a run that names no pull
// request, since the fallback joins the runs with no pull requests at all
// (`coordinator.Join(repo, nil, runs)`), so nothing in it carries the host's
// answer.
//
// Hand mutation this fails on: returning an error for a repository with no
// stored view, which is what the command's own refusal path would do.
func TestCoordinatorUnstoredRepoIsAStateNotAnError(t *testing.T) {
	now := coordinatorAt
	view := coordinator.StoredProjectView{Repos: []coordinator.RepoView{{
		Repo:   "acme/app",
		Stored: false,
		View: coordinator.View{Repo: "acme/app", Records: []coordinator.Record{{
			Runs:     []coordinator.Run{{ID: "20261106-140000-cccc", Status: "running"}},
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
	if len(nodes) != 1 || nodes[0].Key != "coordinator:acme/app#20261106-140000-cccc" {
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

// recordFixture is the roadmap record the GV4.1 tests draw: two plans in two
// streams, one task placed in a release, one nobody has placed, one release the
// record calls finished, a cross-plan dependency between the two plans, and one
// assignment naming a task no plan document carries (D2115). It is built here
// rather than read from a file because every field it carries is one a writer
// computes — an assignment holds a release *key* (ReleaseID) and not a name —
// and a fixture spelling one by hand would pin a shape the writer never writes.
func recordFixture() coordinator.Roadmap {
	cli := coordinator.Release{ID: "v1.5.0", Stream: "cli", Name: "CLI 1.5", State: coordinator.ReleaseOpen, Order: 2}
	style := coordinator.Release{ID: "v1.4.0", Stream: "style", Name: "Style 1.4", State: coordinator.ReleaseFinished, Order: 1}
	target := "1.6.0"
	return coordinator.Roadmap{
		Plans: []coordinator.Plan{
			{
				Schema: coordinator.RoadmapSchema,
				Plan:   "plans/cli",
				Stream: "cli",
				Tasks: []coordinator.Task{
					{ID: "place", Title: "the box a task is placed in", Version: &target},
					{ID: "free", Title: "a task nobody has placed", Version: &target},
					{ID: "orphan", Title: "a task in a release the record has lost"},
				},
			},
			{
				Schema: coordinator.RoadmapSchema,
				Plan:   "plans/style",
				Stream: "style",
				Tasks: []coordinator.Task{
					{ID: "paint", Title: "a task in the other stream"},
				},
			},
		},
		Releases: []coordinator.Release{cli, style},
		Assignments: []coordinator.Assignment{
			{Schema: coordinator.RoadmapSchema, Task: "plans/cli/place", Release: coordinator.ReleaseID(cli.Stream, cli.ID)},
			{Schema: coordinator.RoadmapSchema, Task: "plans/cli/orphan", Release: coordinator.ReleaseID(cli.Stream, "v1.0.0")},
			{Schema: coordinator.RoadmapSchema, Task: "plans/gone/left", Release: coordinator.ReleaseID(cli.Stream, cli.ID)},
		},
		Edges: []coordinator.Edge{
			{Schema: coordinator.RoadmapSchema, Task: "plans/style/paint", Needs: "plans/cli/place", Reason: "the palette"},
			{Schema: coordinator.RoadmapSchema, Task: "plans/style/paint", Needs: "plans/gone/left", Reason: "the palette"},
		},
		// The reader's own answer (D2115): the two ids no plan document carries
		// — one an assignment names and one an edge names.
		Dangling: []string{"plans/cli/orphan", "plans/gone/left"},
	}
}

func drawnRecord(t *testing.T) Record {
	t.Helper()
	out, err := CoordinatorRecordSource{Record: recordFixture()}.Draw()
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	return out
}

func recordNodeAt(t *testing.T, out Record, id string) Node {
	t.Helper()
	for _, n := range out.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node for %q in %v", id, out.Nodes)
	return Node{}
}

// TestCoordinatorRecordDrawsTasksBoxesAndEdges is GV4.1's own assertion: a task
// of a published plan is a node of the coordinator scope, its release is a box
// the document carries, and a cross-plan dependency is an edge between the two
// tasks — the edge a document drawn from the stored view alone has none of
// (F17).
func TestCoordinatorRecordDrawsTasksBoxesAndEdges(t *testing.T) {
	out := drawnRecord(t)
	source := Scope{Kind: ScopeCoordinator}.String()

	if len(out.Releases) != 2 {
		t.Fatalf("releases = %v, want 2", out.Releases)
	}
	first := out.Releases[0]
	if first.Key != coordinator.ReleaseID("cli", "v1.5.0") || first.Stream != "cli" || first.Name != "CLI 1.5" ||
		first.State != coordinator.ReleaseOpen || first.Order != 2 {
		t.Fatalf("release[0] = %+v", first)
	}

	placed := recordNodeAt(t, out, "plans/cli/place")
	if placed.Key != NodeKey(source, "plans/cli/place") || placed.Kind != NodeTask {
		t.Fatalf("placed = %+v", placed)
	}
	if placed.Plan != "plans/cli" || placed.Stream != "cli" || placed.Release != first.Key {
		t.Fatalf("placed = %+v, want the record's plan, stream and release key", placed)
	}
	if placed.Dangling {
		t.Fatalf("placed is dangling: %+v", placed)
	}

	if len(out.Edges) != 2 {
		t.Fatalf("edges = %v, want 2", out.Edges)
	}
	want := Edge{
		From: NodeKey(source, "plans/style/paint"),
		To:   NodeKey(source, "plans/cli/place"),
		Kind: DependsOnEdge,
	}
	if out.Edges[0] != want {
		t.Fatalf("edge = %+v, want %+v", out.Edges[0], want)
	}
	// Every endpoint of every edge is a node of the document, dangling ones
	// included (D2115): an edge drawn to an endpoint no node carries is a
	// dependency a reader cannot follow.
	for _, edge := range out.Edges {
		for _, key := range []string{edge.From, edge.To} {
			if !slices.ContainsFunc(out.Nodes, func(n Node) bool { return n.Key == key }) {
				t.Fatalf("edge %+v names %q and no node carries it", edge, key)
			}
		}
	}
}

// TestCoordinatorRecordUnassignedTaskIsInItsPlansStream holds D2114: a task
// nobody has placed is drawn in its plan's own stream box-less, with the plan's
// version beside it, and it is never matched to a release.
func TestCoordinatorRecordUnassignedTaskIsInItsPlansStream(t *testing.T) {
	out := drawnRecord(t)
	free := recordNodeAt(t, out, "plans/cli/free")
	if free.Release != "" {
		t.Fatalf("an unassigned task names a release: %+v", free)
	}
	if free.Stream != "cli" {
		t.Fatalf("stream = %q, want the plan's own (D2114)", free.Stream)
	}
	if free.Version != "1.6.0" {
		t.Fatalf("version = %q, want the plan's own target", free.Version)
	}
}

// TestCoordinatorRecordDanglingIsDrawn holds D2115's two dangling shapes: an id
// no plan document carries is a node of its own — so an edge naming it has two
// endpoints in the document — and a task whose assignment names a release the
// record does not hold is drawn in its plan's own lane rather than in a box that
// is not there.
func TestCoordinatorRecordDanglingIsDrawn(t *testing.T) {
	out := drawnRecord(t)
	gone := recordNodeAt(t, out, "plans/gone/left")
	if !gone.Dangling {
		t.Fatalf("a task no plan carries is not marked: %+v", gone)
	}
	if gone.Plan != "plans/gone" {
		t.Fatalf("plan = %q, want the qualified id's own plan", gone.Plan)
	}
	if gone.Release != coordinator.ReleaseID("cli", "v1.5.0") || gone.Stream != "cli" {
		t.Fatalf("an assignment to a release that exists is not drawn in its box: %+v", gone)
	}

	orphan := recordNodeAt(t, out, "plans/cli/orphan")
	if !orphan.Dangling {
		t.Fatalf("a task whose release is gone is not marked: %+v", orphan)
	}
	if orphan.Release != "" {
		t.Fatalf("a task whose release the record does not hold names one: %+v", orphan)
	}
	if orphan.Stream != "cli" {
		t.Fatalf("stream = %q, want the plan's own lane", orphan.Stream)
	}
}

// TestCoordinatorRecordTextIsSanitised is the scope's own rule (H2): every text
// of the record is another writer's, so it is sanitised rather than trusted,
// and the fields this build joins on — the qualified id — are refused instead of
// rewritten (Sanitise, D1356).
func TestCoordinatorRecordTextIsSanitised(t *testing.T) {
	record := recordFixture()
	record.Plans[0].Tasks[0].Title = "a title\x07with a bell"
	record.Releases[0].Name = "a name\x07with a bell"
	record.Plans[0].Tasks[1].Title = "a title​with a zero-width space"
	out, err := CoordinatorRecordSource{Record: record}.Draw()
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if strings.ContainsRune(recordNodeAt(t, out, "plans/cli/place").Title, '\x07') {
		t.Fatalf("a title kept a control character")
	}
	for _, rel := range out.Releases {
		if strings.ContainsRune(rel.Name, '\x07') {
			t.Fatalf("release name %q kept a control character", rel.Name)
		}
	}
	if out.Releases[0].Name != "a namewith a bell" {
		t.Fatalf("release name = %q", out.Releases[0].Name)
	}

	record = recordFixture()
	record.Plans[0].Tasks[0].ID = "pla\x07ce"
	if _, err := (CoordinatorRecordSource{Record: record}).Draw(); err == nil {
		t.Fatal("an id sanitising would rewrite was accepted")
	}
}

// TestCoordinatorRecordEmptyIsEmpty is the absent-record case: a project that
// has never published a plan draws no node, no box and no edge, and never a
// null the console would have to tell from an empty list.
func TestCoordinatorRecordEmptyIsEmpty(t *testing.T) {
	out, err := CoordinatorRecordSource{}.Draw()
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if len(out.Nodes) != 0 || len(out.Releases) != 0 || len(out.Edges) != 0 {
		t.Fatalf("an empty record drew %+v", out)
	}
	if out.Nodes == nil || out.Releases == nil || out.Edges == nil {
		t.Fatalf("an empty record drew a nil list: %+v", out)
	}
}
