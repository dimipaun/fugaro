// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

// The map's builder (CG6 of docs/console-fixes): the read behind `fugaro
// orchestrator map --json`, which is what the console's Coordinator tab draws
// (D3221).
//
// The map is a picture of what is being driven right now, so a case here is
// about one of the three things that decide it: whether a lease is fresh (a
// heartbeat of the installation's own daemon, judged by the database's clock),
// what the fresh orchestrators say about each other — one's item waiting on
// another's — and what the coordinator's stored views say about the work they
// share. The first runs against internal/gcpfake's Realtime Database with its
// clock set, so that "fresh" is a case a test states rather than a race with the
// machine it runs on; the second and third build the active set the builder's
// own read produces, because two orchestrators of one project cannot be written
// to a database until the id rule that makes `<project>~<name>` addressable has
// landed (the last test in this file is that case, and waits for it).
//
// Nothing here contacts Firebase, and nothing reads this machine's cache: a map
// is read from the project's database and the coordinator's stored views.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// mapNow is the fixture's clock: a case reads the map at this moment, and every
// beat it writes is written relative to it.
var mapNow = time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

// mapFixture is one project's database, the client over it and the store the
// coordinator's half is read through.
func mapFixture(t *testing.T) (*gcpfake.RTDB, *rtdb.Client, *coordinator.Store, context.Context) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return mapNow })
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	return f, db, coordinator.NewStore(db), context.Background()
}

// mapInput is the read a case makes: one project's map over the fixture, at the
// case's clock and for the repositories it names.
func mapInput(db *rtdb.Client, store *coordinator.Store, now time.Time, repos ...string) MapInput {
	return MapInput{
		DB:      db,
		Store:   store,
		Project: "aurora",
		Repos:   repos,
		Slug:    func(_ context.Context, repo string) (string, error) { return repo, nil },
		Now:     now,
	}
}

// mapState writes one node of an orchestrator the way a writer of this release
// writes it, so that the builder reads back through its own decoder a document
// the tree really produces.
func mapState(t *testing.T, f *gcpfake.RTDB, id string, shard orchestrator.Shard, s orchestrator.State) {
	t.Helper()
	path, err := mapShardPath(id, shard)
	if err != nil {
		t.Fatal(err)
	}
	s.SchemaVersion, s.AgentID, s.Role = orchestrator.SchemaVersion, id, orchestrator.RoleOrchestrator
	s.Goal = "keep the project's pull requests moving without a person watching"
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the fixture state of %s is not one this release writes: %v", id, err)
	}
	f.Set(path, json.RawMessage(raw))
}

func mapShardPath(id string, shard orchestrator.Shard) (string, error) {
	switch shard {
	case orchestrator.ShardState:
		return orchestrator.PathState(id)
	case orchestrator.ShardBacklog:
		return orchestrator.PathBacklog(id)
	}
	return orchestrator.PathPlan(id, strings.TrimPrefix(string(shard), orchestrator.PlansNode+"/"))
}

// mapLease writes an id's lease node: the holder and its last beat, which is the
// only thing that puts an orchestrator on the map. A nil at writes no lease node
// at all, which is the other shape a stale lease takes.
func mapLease(t *testing.T, f *gcpfake.RTDB, id, holder string, at *time.Time) {
	t.Helper()
	path, err := orchestrator.PathLease(id)
	if err != nil {
		t.Fatal(err)
	}
	if at == nil {
		f.Set(path, nil)
		return
	}
	f.Set(path, map[string]any{"holder": holder, "at": at.UTC().Format(time.RFC3339)})
}

// mapDriver writes the driver node beside the lease: who is driving (D3218, F18).
func mapDriver(t *testing.T, f *gcpfake.RTDB, id, principal string) {
	t.Helper()
	path, err := orchestrator.PathDriver(id)
	if err != nil {
		t.Fatal(err)
	}
	f.Set(path, map[string]any{"principal": principal, "at": mapNow.Add(-30 * time.Second).UTC().Format(time.RFC3339)})
}

// mapItem is one work item of a fixture state: an item in a phase, optionally
// waiting on something and optionally with a run committed against it.
func mapItem(id string, phase orchestrator.Phase, runID string, deps ...string) orchestrator.WorkItem {
	w := orchestrator.WorkItem{
		ID:        id,
		Title:     "the work of " + id,
		Repo:      "acme/app",
		Recipe:    "standard",
		RigorTier: "medium",
		Brief:     "The brief of " + id + ".",
		Phase:     phase,
		DependsOn: deps,
		RunID:     runID,
	}
	if runID != "" {
		w.Attempts = 1
	}
	return w
}

// mapIntent is the run committed against one item, which is what a job going
// counts (D1231).
func mapIntent(t *testing.T, w orchestrator.WorkItem, runID string) orchestrator.Intent {
	t.Helper()
	return orchestrator.Intent{
		ID:         orchestrator.IntentID(w.ID, 1),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: mapNow.Add(-time.Hour),
		At:         mapNow.Add(-time.Hour),
	}
}

// active builds one orchestrator the way the read builds it: the id, its name
// half, the principal its driver node names, its nodes and the items they carry.
// It is the input a case about the edges between two orchestrators is written
// from, and the only way to write two of one project before named ids are
// addressable.
func active(id, driver string, states ...orchestrator.State) activeOrchestrator {
	_, name, _ := orchestrator.ParseOrchestratorID(id)
	items := map[string]bool{}
	for _, s := range states {
		for _, w := range s.WorkItems {
			items[w.ID] = true
		}
	}
	return activeOrchestrator{
		id:        id,
		name:      name,
		driver:    driver,
		states:    states,
		items:     items,
		serverNow: mapNow,
	}
}

// TestMapReadsActiveOnly: the map is the orchestrators being driven — one
// shallow read of the ids, one lease read each, and a full read of only those
// whose lease is fresh — and each node carries the figures a screen shows: the
// driver, the items still being worked, the jobs going and the lease's age
// (D3221, and CG4's own liveness rule).
//
// An orchestrator nobody is beating is not a node with empty figures but no node
// at all, whether its last beat is past the lease's TTL or there is no lease node
// for it, which are the two shapes Fresh answers false for; and an id of another
// project is not this map's, because one map is one project.
//
// Hand mutation this fails on: reading every id instead of the fresh ones (the
// idle id becomes a node), counting the whole state as active work (the done and
// the cancelled item are counted), stamping the lease's age from the reader's
// clock rather than the database's, and dropping the project's own name half.
func TestMapReadsActiveOnly(t *testing.T) {
	f, db, store, ctx := mapFixture(t)
	beat := mapNow.Add(-30 * time.Second)

	running := mapItem("wi-1", orchestrator.PhaseRunning, "20261122-083000-aaaa")
	queued := mapItem("wi-2", orchestrator.PhaseQueued, "")
	done := mapItem("wi-3", orchestrator.PhaseDone, "")
	cancelled := mapItem("wi-4", orchestrator.PhaseCancelled, "")
	mapState(t, f, "aurora", orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{running, queued, done, cancelled},
		Intents:   []orchestrator.Intent{mapIntent(t, running, "20261122-083000-aaaa")},
	})
	mapLease(t, f, "aurora", "20261122-083000-aaaa", &beat)
	mapDriver(t, f, "aurora", "ops@acme.test")

	// Another project of the same installation, driven right now: not this map's.
	mapState(t, f, "borealis", orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseQueued, "")},
	})
	mapLease(t, f, "borealis", "20261122-085500-bbbb", &beat)

	doc, err := BuildMap(ctx, mapInput(db, store, mapNow))
	if err != nil {
		t.Fatalf("building the map: %v", err)
	}
	if doc.Project != "aurora" || doc.Version != orchestrator.MapDocVersion {
		t.Fatalf("the map is %q at version %d, want aurora at %d", doc.Project, doc.Version, orchestrator.MapDocVersion)
	}
	if !doc.GeneratedAt.Equal(mapNow) {
		t.Errorf("generated_at = %s, want the reader's clock %s", doc.GeneratedAt, mapNow)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("the map drew %d nodes, want the one orchestrator of this project: %+v", len(doc.Nodes), doc.Nodes)
	}
	n := doc.Nodes[0]
	if n.ID != "aurora" || n.Name != "" {
		t.Errorf("the node is %q named %q, want the project's own orchestrator, whose name half is empty", n.ID, n.Name)
	}
	if n.Driver != "ops@acme.test" {
		t.Errorf("the driver is %q, want the principal the driver node names", n.Driver)
	}
	// Running and queued are still being worked; done and cancelled are not.
	if n.ActiveTasks != 2 {
		t.Errorf("active_tasks = %d, want 2 (the running and the queued item)", n.ActiveTasks)
	}
	if n.RunningJobs != 1 {
		t.Errorf("running_jobs = %d, want 1 (the one intent with no outcome)", n.RunningJobs)
	}
	if n.LeaseAgeSeconds == nil {
		t.Fatal("lease_age_seconds is null, want the age of a beat that carried a stamp")
	}
	if age := *n.LeaseAgeSeconds; age < 30 || age > 31 {
		t.Errorf("lease_age_seconds = %v, want the 30 seconds the beat is old by", age)
	}
	if len(doc.Edges) != 0 {
		t.Errorf("the map drew %d edges, want none: one node has nothing to join", len(doc.Edges))
	}

	// The same read past the lease's TTL: the orchestrator is not being driven any
	// more, so the document carries no node for it even though every other
	// document under the id is still there.
	later := mapNow.Add(orchestrator.LeaseTTL + time.Minute)
	f.SetClock(func() time.Time { return later })
	doc, err = BuildMap(ctx, mapInput(db, store, later))
	if err != nil {
		t.Fatalf("building the map after the lease lapsed: %v", err)
	}
	if len(doc.Nodes) != 0 {
		t.Errorf("the map drew %+v, want no node: nobody has beaten the lease for longer than its TTL", doc.Nodes)
	}
	// The lists a reader ranges over are lists whatever the map holds: an absent
	// list is the shape a decoder has to special-case (reading 4).
	if doc.Nodes == nil || doc.Edges == nil {
		t.Errorf("the map's lists are nil, want lists a reader can range over: %+v", doc)
	}

	// And with no lease node at all, which is the other shape of the same answer:
	// an id the database has documents for and no holder.
	f.SetClock(func() time.Time { return mapNow })
	mapLease(t, f, "aurora", "", nil)
	doc, err = BuildMap(ctx, mapInput(db, store, mapNow))
	if err != nil {
		t.Fatalf("building the map with no lease node: %v", err)
	}
	if len(doc.Nodes) != 0 {
		t.Errorf("the map drew %+v, want no node: the id has no lease node", doc.Nodes)
	}
}

// TestMapDependencyEdge: one orchestrator's item waiting on another's is an edge
// from the one that waits to the one it waits on (D3208). It is the map's one
// relation that is a fact about the work rather than about the coordinator's
// stored views, and this is the test the task's red step names: with the
// reference read as a bare item id, the map carries no edge at all.
//
// The four ways a reference draws nothing are as much of the rule as the edge
// is: a reference to an item of the waiting orchestrator's own (the inside of one
// node, F44), one to an orchestrator that is not on the map, one to an item the
// peer does not hold, and a string that is not a reference at all. The map draws
// no edge it cannot end, which is reading 5's rule and the reason an edge joins
// two nodes of one document.
//
// Hand mutation this fails on: reading every reference as a bare item id (no
// edge), drawing one for a peer that is not a node, skipping the check that the
// peer holds the item, drawing a self-edge for a reference that names the waiting
// orchestrator, and giving the edge the wrong end first.
func TestMapDependencyEdge(t *testing.T) {
	night := active("aurora~night", "night@acme.test", orchestrator.State{
		WorkItems: []orchestrator.WorkItem{
			mapItem("wi-1", orchestrator.PhaseQueued, ""),
			mapItem("wi-2", orchestrator.PhaseQueued, "",
				"wi-1",
				"aurora~night/wi-1",
				"aurora~day/wi-2",
				"aurora~gone/wi-1",
				"aurora~day/wi-99",
				"Docs/wi-1"),
		},
	})
	day := active("aurora~day", "day@acme.test", orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-2", orchestrator.PhaseRunning, "")},
	})

	want := orchestrator.MapEdge{From: "aurora~night", To: "aurora~day", Kind: orchestrator.MapEdgeDependency}
	edges := dependencyEdges([]activeOrchestrator{night, day})
	if len(edges) != 1 || edges[0] != want {
		t.Fatalf("the map's edges are %+v, want the one %+v: night's item waits on day's", edges, want)
	}
	// A dependency says which two orchestrators are joined and nothing else: the
	// two ids are the whole reason, so the field is empty, as an overlap's own
	// reason is not.
	if edges[0].Reason != "" {
		t.Errorf("the dependency carries the reason %q, want none", edges[0].Reason)
	}
	if edges := dependencyEdges([]activeOrchestrator{night}); len(edges) != 0 {
		t.Errorf("the map drew %+v with day absent, want nothing: an edge never names a node the map does not draw", edges)
	}
}

// TestMapOverlapFromCollisions: two orchestrators on the same work are an overlap
// edge, and the map joins them out of what the coordinator already wrote — the
// collisions a sweep stored, keyed by the records' own labels, and the pull
// requests its stored view holds — through `WorkItem.run_id`, which is the only
// thing that says whose run a record's run is (F21, D3211, D3222).
//
// Both facts are drawn, and each carries the reason a screen shows beside the
// dashed edge: the collision's own shares, and the pull request two orchestrators
// are on. What draws nothing is a collision between records no driven
// orchestrator has a run on, a repository the store holds no view of, and a map
// whose orchestrators have committed no run at all — the last of which is worth
// knowing before a read per repository.
//
// Hand mutation this fails on: joining collisions without going through run_id
// (no edge at all), drawing the shared pull request's edge from a record's runs
// rather than from the orchestrators of those runs, reading a repository the
// store has no view of, and reading the views when nothing is running.
func TestMapOverlapFromCollisions(t *testing.T) {
	_, db, store, ctx := mapFixture(t)

	const nightRun, nightRun2 = "20261122-080000-aaaa", "20261122-080100-aaaa"
	const dayRun, dayRun2, strangerRun = "20261122-080000-bbbb", "20261122-080100-bbbb", "20261122-080000-cccc"
	night := active("aurora~night", "night@acme.test", orchestrator.State{
		WorkItems: []orchestrator.WorkItem{
			mapItem("wi-1", orchestrator.PhaseRunning, nightRun),
			mapItem("wi-2", orchestrator.PhaseRunning, nightRun2),
		},
	})
	day := active("aurora~day", "day@acme.test", orchestrator.State{
		WorkItems: []orchestrator.WorkItem{
			mapItem("wi-1", orchestrator.PhaseRunning, dayRun),
			mapItem("wi-2", orchestrator.PhaseRunning, dayRun2),
		},
	})

	// The stored view a sweep wrote: one record per pull request, the runs behind
	// it and the collisions it found. The records no driven orchestrator has a run
	// on are the negative half of the case.
	if err := store.Write(ctx, coordinator.PathView("acme/app"), coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{
			{PR: &coordinator.PullRequest{Number: 12, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: nightRun, PR: 12, Status: "running"}}},
			{PR: &coordinator.PullRequest{Number: 13, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: dayRun, PR: 13, Status: "running"}}},
			// A pull request both orchestrators have a run on: D3222's own fact,
			// with no collision behind it.
			{PR: &coordinator.PullRequest{Number: 14, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: nightRun2, PR: 14}, {ID: dayRun2, PR: 14}}},
			// A record neither of them is on.
			{PR: &coordinator.PullRequest{Number: 15, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: strangerRun, PR: 15}}},
		},
		Collisions: []coordinator.Collision{
			{A: "#12", B: "#13", Files: []string{"internal/cli/run.go"}, Services: []string{"internal/cli"}},
			{A: "#15", B: "#99", Files: []string{"website/content/cli-reference.md"}},
		},
	}); err != nil {
		t.Fatalf("storing the coordinator's view: %v", err)
	}

	edges, err := overlapEdges(ctx, mapInput(db, store, mapNow, "acme/app"), []activeOrchestrator{night, day})
	if err != nil {
		t.Fatalf("reading the overlap: %v", err)
	}
	want := []orchestrator.MapEdge{
		// The collision is stored between #12 and #13, so the edge runs from the
		// orchestrator whose run is on #12 to the one whose run is on #13.
		{From: "aurora~night", To: "aurora~day", Kind: orchestrator.MapEdgeOverlap,
			Reason: "the coordinator stored a collision between #12 and #13 in acme/app: 1 shared file and 1 shared service"},
		{From: "aurora~day", To: "aurora~night", Kind: orchestrator.MapEdgeOverlap,
			Reason: "both are on pull request #14 of acme/app"},
	}
	if len(edges) != len(want) {
		t.Fatalf("the overlap is %+v, want the stored collision and the shared pull request: %+v", edges, want)
	}
	for _, w := range want {
		if !slices.Contains(edges, w) {
			t.Errorf("the overlap %+v carries no %+v", edges, w)
		}
	}

	// A repository whose view is not stored, and a project whose repositories are
	// not listed at all: nothing overlaps where nobody has looked.
	other := mapInput(db, store, mapNow, "acme/other")
	if edges, err := overlapEdges(ctx, other, []activeOrchestrator{night, day}); err != nil || len(edges) != 0 {
		t.Errorf("an unreconciled repository drew %+v (%v), want nothing", edges, err)
	}
	if edges, err := overlapEdges(ctx, mapInput(db, store, mapNow), []activeOrchestrator{night, day}); err != nil || len(edges) != 0 {
		t.Errorf("a project with no repositories drew %+v (%v), want nothing", edges, err)
	}

	// Nothing running is nothing to join: the read stops before a repository's
	// view is read at all. The refused read is what holds that — no view of this
	// project is readable over the second database, and the map answers anyway.
	quiet := active("aurora~night", "night@acme.test", orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseQueued, "")},
	})
	denied := gcpfake.NewRTDB(t)
	denied.DenyRead(coordinator.PathView("acme/app"))
	deniedDB, err := rtdb.New(denied.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	quietIn := mapInput(deniedDB, coordinator.NewStore(deniedDB), mapNow, "acme/app")
	if edges, err := overlapEdges(ctx, quietIn, []activeOrchestrator{quiet}); err != nil || len(edges) != 0 {
		t.Errorf("a map with nothing running drew %+v (%v), want nothing: the views are not read at all", edges, err)
	}
}

// TestMapSweepOff: with the coordinator's sweep off there is no heartbeat to
// read, and the map says so rather than letting a reader take the absence of an
// overlap edge for "nothing overlaps" (F23, reading 6).
//
// What the map still carries is the half that needs no sweep — the nodes — and it
// carries no heartbeat time at all, because a document of zeroes would read as a
// sweep that ran at the beginning of time. A stored collision is not read either,
// however recent it is: nobody has looked since the sweep was turned off. The
// other half of the same test is the heartbeat's own reading: a beat that is there
// turns the sweep on and stamps the three times, the newest of them being when the
// document was last written.
//
// Hand mutation this fails on: reading the stored collisions with no heartbeat (an
// overlap edge beside a health that says the sweep is off), and filling the
// health's times from the reader's clock.
func TestMapSweepOff(t *testing.T) {
	f, db, store, ctx := mapFixture(t)
	beat := mapNow.Add(-5 * time.Second)

	const nightRun, dayRun = "20261122-080000-aaaa", "20261122-080000-bbbb"
	mapState(t, f, "aurora", orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseRunning, nightRun)},
	})
	mapLease(t, f, "aurora", nightRun, &beat)

	// A stored view with a collision in it, which is exactly what a map with no
	// sweep must not draw: nobody has looked since the sweep was turned off.
	if err := store.Write(ctx, coordinator.PathView("acme/app"), coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{
			{PR: &coordinator.PullRequest{Number: 12}, InFlight: true, Runs: []coordinator.Run{{ID: nightRun, PR: 12}}},
			{PR: &coordinator.PullRequest{Number: 13}, InFlight: true, Runs: []coordinator.Run{{ID: dayRun, PR: 13}}},
		},
		Collisions: []coordinator.Collision{{A: "#12", B: "#13", Files: []string{"internal/cli/run.go"}}},
	}); err != nil {
		t.Fatalf("storing the coordinator's view: %v", err)
	}

	doc, err := BuildMap(ctx, mapInput(db, store, mapNow, "acme/app"))
	if err != nil {
		t.Fatalf("building the map: %v", err)
	}
	if doc.Health.Sweep {
		t.Errorf("the health says the sweep is on with no heartbeat to read: %+v", doc.Health)
	}
	if doc.Health.StartedAt != nil || doc.Health.OkAt != nil || doc.Health.At != nil {
		t.Errorf("the health carries the times %+v with the sweep off, want null: there is no heartbeat", doc.Health)
	}
	if doc.Health.Note != orchestrator.MapSweepOffNote {
		t.Errorf("health.note = %q, want the sentence that says why (%q)", doc.Health.Note, orchestrator.MapSweepOffNote)
	}
	if len(doc.Edges) != 0 {
		t.Errorf("the map drew %+v, want no edge: a stored collision is not read with the sweep off", doc.Edges)
	}
	if len(doc.Nodes) != 1 {
		t.Errorf("the map drew %+v, want the node the sweep has nothing to do with", doc.Nodes)
	}

	// The same read with the coordinator's heartbeat: the sweep is on, the times
	// are the tick's own, and the stored collision is still no edge — one node has
	// nothing to overlap with.
	f.Set(coordpath.PathHeartbeat, map[string]any{
		"started_at": mapNow.Add(-2 * time.Minute).Format(time.RFC3339),
		"ok_at":      mapNow.Add(-time.Minute).Format(time.RFC3339),
	})
	doc, err = BuildMap(ctx, mapInput(db, store, mapNow, "acme/app"))
	if err != nil {
		t.Fatalf("building the map with a heartbeat: %v", err)
	}
	if !doc.Health.Sweep || doc.Health.Note != "" {
		t.Errorf("the health is %+v, want the sweep on and nothing to say", doc.Health)
	}
	if doc.Health.StartedAt == nil || !doc.Health.StartedAt.Equal(mapNow.Add(-2*time.Minute)) {
		t.Errorf("health.started_at = %v, want when the tick began", doc.Health.StartedAt)
	}
	if doc.Health.OkAt == nil || !doc.Health.OkAt.Equal(mapNow.Add(-time.Minute)) {
		t.Errorf("health.ok_at = %v, want when a tick last finished", doc.Health.OkAt)
	}
	if doc.Health.At == nil || !doc.Health.At.Equal(mapNow.Add(-time.Minute)) {
		t.Errorf("health.at = %v, want the newer of the two: when the document was last written", doc.Health.At)
	}
	if len(doc.Edges) != 0 {
		t.Errorf("the map drew %+v, want no edge: the collision's two records are one orchestrator's work", doc.Edges)
	}
}

// TestMapReadsTwoOrchestratorsOfOneProject is the whole read at once, over the
// database: two orchestrators of one project, one waiting on the other's item and
// both on one stored collision, drawn as two nodes with a dependency edge and an
// overlap edge between them.
//
// It is here and it skips, because two orchestrators of one project are two named
// ids and the store cannot address one yet: the id rule for `<project>~<name>`
// (D3207) is CG1a's own task and is not in this tree, so `NodeIDs` lists no such
// id and `PathState` refuses it. The reference between the two needs a second
// unlanded half, the validator that accepts `<orchestrator>/<item>` in an item's
// `depends_on` (CG3). The cases above cover both halves against the builder's own
// input; this one is the join between them and the database, and it starts
// answering by itself the moment those two have landed. I-ORCH's end-to-end test
// is the same statement at the console's end of the read.
func TestMapReadsTwoOrchestratorsOfOneProject(t *testing.T) {
	const night, day = "aurora~night", "aurora~day"
	if _, err := orchestrator.PathState(night); err != nil {
		t.Skipf("the store does not address a named orchestrator yet, so two orchestrators of one project cannot be written: %v", err)
	}
	// The other half of the same wait, stated as the tree's own reader sees it: a
	// state carrying a cross reference is not a document this release reads until
	// CG3's validator accepts it.
	probe := `{"schema_version":1,"agent_id":"` + night + `","role":"orchestrator","goal":"g","work_items":[{"id":"wi-1","title":"t","repo":"acme/app","recipe":"standard","rigor_tier":"medium","brief":"b","phase":"queued","depends_on":["` + day + `/wi-1"]}]}`
	if _, err := orchestrator.DecodeState([]byte(probe)); err != nil {
		t.Skipf("a dependency on another orchestrator's item is not a document this release reads, so the edge cannot be written: %v", err)
	}

	f, db, store, ctx := mapFixture(t)
	beat := mapNow.Add(-5 * time.Second)
	const nightRun, dayRun = "20261122-080000-aaaa", "20261122-080000-bbbb"
	mapState(t, f, night, orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{
			mapItem("wi-1", orchestrator.PhaseRunning, nightRun),
			mapItem("wi-2", orchestrator.PhaseQueued, "", day+"/wi-1"),
		},
	})
	mapLease(t, f, night, nightRun, &beat)
	mapState(t, f, day, orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseRunning, dayRun)},
	})
	mapLease(t, f, day, dayRun, &beat)
	if err := store.Write(ctx, coordinator.PathView("acme/app"), coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{
			{PR: &coordinator.PullRequest{Number: 12, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: nightRun, PR: 12}}},
			{PR: &coordinator.PullRequest{Number: 13, State: coordinator.StateOpen}, InFlight: true,
				Runs: []coordinator.Run{{ID: dayRun, PR: 13}}},
		},
		Collisions: []coordinator.Collision{{A: "#12", B: "#13", Files: []string{"internal/cli/run.go"}}},
	}); err != nil {
		t.Fatalf("storing the coordinator's view: %v", err)
	}
	f.Set(coordpath.PathHeartbeat, map[string]any{"started_at": mapNow.Add(-time.Minute).Format(time.RFC3339)})

	doc, err := BuildMap(ctx, mapInput(db, store, mapNow, "acme/app"))
	if err != nil {
		t.Fatalf("building the map: %v", err)
	}
	if len(doc.Nodes) != 2 || doc.Nodes[0].ID != day || doc.Nodes[1].ID != night {
		t.Fatalf("the map drew %+v, want both driven orchestrators of the project, in id order", doc.Nodes)
	}
	want := []orchestrator.MapEdge{
		{From: night, To: day, Kind: orchestrator.MapEdgeDependency},
		{From: night, To: day, Kind: orchestrator.MapEdgeOverlap,
			Reason: "the coordinator stored a collision between #12 and #13 in acme/app: 1 shared file"},
	}
	for _, w := range want {
		if !slices.Contains(doc.Edges, w) {
			t.Errorf("the map's edges %+v carry no %+v", doc.Edges, w)
		}
	}
}
