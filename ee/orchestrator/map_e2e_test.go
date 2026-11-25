// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

// The map, end to end (I-ORCH of docs/console-fixes/implementation-plan.md): two
// named orchestrators of one project, driven at the same moment, with a cross
// dependency between them and an overlapping collision the coordinator stored,
// read through the builder the command calls and printed as the document the
// console decodes.
//
// It is the one case that stands on every side of every edge C-ORCH broke: the
// named id rule (CG1a) that makes `<project>~<name>` addressable, the cross
// reference the validator accepts (CG3) and the builder that draws both kinds of
// edge (CG6), with the map's own decoder reading the wire back. The stub this
// file's task deleted answered an empty document, so a builder that drew nothing
// — or one the command never called — would fail every assertion below.
//
// Nothing here contacts Firebase and nothing reads this machine's cache: the
// fixture is internal/gcpfake's Realtime Database with its clock set, and the
// coordinator's stored view is written through its own store.

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestOrchestratorMapEndToEnd: the map a person's console draws, built from the
// database the way the command builds it.
//
// Two orchestrators of one project are being driven — `aurora~night` and
// `aurora~day` — and the document carries one edge of each kind: a **solid**
// dependency, night's item waiting on day's, and a **dashed** overlap, the
// collision the coordinator stored between the two pull requests their runs are
// on (D3208, D3211, D3222). The two edges are the whole document's edge list, so
// a builder that drew only one kind, or an edge to an orchestrator nobody drives,
// is the failure.
//
// Mutation: reading every id instead of the fresh ones, drawing a dependency on
// the waiting orchestrator's own item, joining the collision by anything but the
// run id an item committed, or answering with the stub's empty document — each
// leaves the edge list without one of the two edges below.
func TestOrchestratorMapEndToEnd(t *testing.T) {
	const night, day = "aurora~night", "aurora~day"
	if !orchestrator.NamedIDsAreAddressable() {
		t.Fatal("this release cannot address a named orchestrator, so two of one project cannot be written")
	}
	const nightRun, dayRun = "20261122-080000-aaaa", "20261122-080000-bbbb"

	f, db, store, ctx := mapFixture(t)
	beat := mapNow.Add(-5 * time.Second)

	// night is driving two items: one of its own, and one waiting on day's item
	// — the cross reference CG3's validator admits.
	mapState(t, f, night, orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{
			mapItem("wi-1", orchestrator.PhaseRunning, nightRun),
			mapItem("wi-2", orchestrator.PhaseQueued, "", day+"/wi-1"),
		},
	})
	mapLease(t, f, night, nightRun, &beat)
	mapDriver(t, f, night, "night@acme.test")

	// day is driving one item, which night's second item waits on.
	mapState(t, f, day, orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseRunning, dayRun)},
	})
	mapLease(t, f, day, dayRun, &beat)
	mapDriver(t, f, day, "day@acme.test")

	// The coordinator's stored view: one open pull request per orchestrator, each
	// with the run that orchestrator committed against its item, and a collision
	// between the two records — which is the overlap edge and the only thing that
	// can produce one, joined through `WorkItem.run_id` (D3211, D3222).
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
	// A heartbeat, so the sweep is on and the overlap half of the read runs
	// (CG5, D3212): with the sweep off the map carries no overlap edge at all.
	f.Set(coordpath.PathHeartbeat, map[string]any{"started_at": mapNow.Add(-time.Minute).Format(time.RFC3339)})

	doc, err := BuildMap(ctx, mapInput(db, store, mapNow, "acme/app"))
	if err != nil {
		t.Fatalf("building the map: %v", err)
	}

	// The document the console reads is read back here by the same door a reader
	// outside this binary uses — strict, and validated — so a field this build
	// writes that this build would not read is the failure (D3221).
	wire, err := orchestrator.EncodeMapDoc(doc)
	if err != nil {
		t.Fatalf("encoding the map the command prints: %v", err)
	}
	read, err := orchestrator.DecodeMapDoc(wire)
	if err != nil {
		t.Fatalf("the console's own decoder refuses the document this read printed: %v\n%s", err, wire)
	}

	if len(read.Nodes) != 2 || read.Nodes[0].ID != day || read.Nodes[1].ID != night {
		t.Fatalf("the map drew %+v, want both driven orchestrators of the project, in id order", read.Nodes)
	}
	if !read.Health.Sweep {
		t.Fatalf("the health is %+v, want the sweep on over the heartbeat the fixture wrote", read.Health)
	}

	// One edge of each kind, and no third: the solid dependency and the dashed
	// overlap are the whole picture two orchestrators of one project can draw.
	solid := []orchestrator.MapEdge{}
	dashed := []orchestrator.MapEdge{}
	for _, e := range read.Edges {
		switch e.Kind {
		case orchestrator.MapEdgeDependency:
			solid = append(solid, e)
		case orchestrator.MapEdgeOverlap:
			dashed = append(dashed, e)
		}
	}
	if len(solid) != 1 {
		t.Errorf("the map carries %d dependency edges, want the one solid edge: %+v", len(solid), read.Edges)
	}
	if len(dashed) != 1 {
		t.Errorf("the map carries %d overlap edges, want the one dashed edge: %+v", len(dashed), read.Edges)
	}
	if len(read.Edges) != 2 {
		t.Fatalf("the map carries %+v, want exactly the two edges", read.Edges)
	}
	wantSolid := orchestrator.MapEdge{From: night, To: day, Kind: orchestrator.MapEdgeDependency}
	if !slices.Contains(read.Edges, wantSolid) {
		t.Errorf("the map's edges %+v carry no %+v: night's item waits on day's", read.Edges, wantSolid)
	}
	wantDashed := orchestrator.MapEdge{From: night, To: day, Kind: orchestrator.MapEdgeOverlap,
		Reason: "the coordinator stored a collision between #12 and #13 in acme/app: 1 shared file"}
	if !slices.Contains(read.Edges, wantDashed) {
		t.Errorf("the map's edges %+v carry no %+v: the coordinator stored that collision", read.Edges, wantDashed)
	}

	// The overlap's reason is free text the console draws beside the dashed edge,
	// so the document that reaches a renderer carries it through safetext.Field
	// and never a raw one (Sanitise at the command's boundary).
	doc.Sanitise()
	for _, e := range doc.Edges {
		if e.Kind == orchestrator.MapEdgeOverlap && e.Reason == "" {
			t.Errorf("the overlap edge %+v carries no reason, and a dashed edge is drawn from its own sentence", e)
		}
	}

	// The shape the console's schema ranges over: both lists are lists and never
	// null, and the two ends of every edge are nodes of this document.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(wire, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nodes", "edges", "health", "generated_at"} {
		if string(raw[key]) == "null" {
			t.Errorf("the document's %s is null, want a value: %s", key, wire)
		}
	}
}

// TestOrchestratorMapEndToEndReadsNoStub: the command's own builder is the one
// this package exports, and there is no second `BuildMap` behind it.
//
// The stub C-ORCH shipped answered the empty document from the core package, and
// I-ORCH deleted it: a core `BuildMap` that came back would be a command
// answering a document no orchestrator produced. The assertion is the read
// itself — the builder here is `ee/orchestrator`'s and the map it draws carries
// the orchestrator this fixture wrote — so a command wired back to a stub is the
// failure.
func TestOrchestratorMapEndToEndReadsNoStub(t *testing.T) {
	f, db, store, ctx := mapFixture(t)
	beat := mapNow.Add(-time.Second)
	mapState(t, f, "aurora", orchestrator.ShardState, orchestrator.State{
		WorkItems: []orchestrator.WorkItem{mapItem("wi-1", orchestrator.PhaseQueued, "")},
	})
	mapLease(t, f, "aurora", "20261122-090000-aaaa", &beat)

	doc, err := BuildMap(ctx, mapInput(db, store, mapNow))
	if err != nil {
		t.Fatalf("building the map: %v", err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].ID != "aurora" {
		t.Fatalf("the map drew %+v, want the one driven orchestrator of the project rather than the stub's empty document", doc.Nodes)
	}
}
