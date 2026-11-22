// The orchestrator map: the one document `fugaro orchestrator map --json`
// prints, which is what the console's Coordinator tab draws (D3221).
//
// The map is a picture of the project's active orchestrators and nothing else:
// a node per orchestrator whose lease is fresh, an edge per connection between
// two of them, and the coordinator's own heartbeat beside them. It carries no
// history — a node is on it because a credential is driving it now, and an
// orchestrator nobody drives is not a node with no figures but no node at all.
// The builder is ee/orchestrator's (CG6); this file is the shape, the stub the
// command answers with until I-ORCH wires the builder in is map_stub.go, and
// the console's decoder (CG7a) reads what these two agree on.

package orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// MapDocVersion is the map document's own shape version. A field never changes
// meaning under it: a new one is additive, and a removed or retyped one is a
// new version (the rule every JSON contract in this tree follows).
const MapDocVersion = 1

// The edge kinds. A dependency is one orchestrator's item waiting on another's
// (D3208); an overlap is two orchestrators on the same work — the collision the
// coordinator stored, or two of them on one pull request (D3211, D3222).
const (
	MapEdgeDependency = "dependency"
	MapEdgeOverlap    = "overlap"
)

// MapSweepOffNote is what the health says when the coordinator's sweep is off
// for the project: with no sweep there is no heartbeat and no stored collision,
// so the map is the nodes and their dependencies alone, and a reader must not
// draw the absence of overlap edges as "nothing overlaps".
const MapSweepOffNote = "the coordinator sweep is off for this project, so the map carries no heartbeat and no overlap edge"

// MapDoc is `orchestrator map --json`: the project's active orchestrators, the
// connections between them, and the coordinator's health.
type MapDoc struct {
	Version int `json:"version"`
	// Project is the project whose orchestrators these are. Every node belongs
	// to it: one map is one project.
	Project string `json:"project"`
	// GeneratedAt is when the document was assembled, on the reader's clock. It
	// is a clock and not an as-of — how old one node is, is LeaseAgeSeconds.
	GeneratedAt time.Time `json:"generated_at"`
	// Nodes are the active orchestrators. Empty is a fact — no orchestrator of
	// this project is being driven — and never a null.
	Nodes []MapNode `json:"nodes"`
	// Edges are the connections between nodes. Every edge joins two nodes of
	// this document: an orchestrator nobody drives is not drawn, and neither is
	// an edge to it.
	Edges []MapEdge `json:"edges"`
	// Health is the coordinator's own state.
	Health MapHealth `json:"health"`
}

// MapNode is one active orchestrator: who drives it and what it is carrying.
type MapNode struct {
	// ID is the orchestrator id and Name its name half, "" for the project's
	// default orchestrator — a screen draws the project's name for that one.
	// The two cannot disagree: Name is the id's own name (ParseOrchestratorID).
	ID   string `json:"id"`
	Name string `json:"name"`
	// Driver is the principal the driver node names, "" when the node is absent
	// or unreadable. It is a person's account or a service account's address.
	Driver string `json:"driver"`
	// ActiveTasks is how many of the orchestrator's items are being worked and
	// RunningJobs how many runs it has going, both counted over its shards.
	ActiveTasks int `json:"active_tasks"`
	RunningJobs int `json:"running_jobs"`
	// LeaseAgeSeconds is how long ago the holder last beat, in seconds, and
	// null when the lease node carries no readable stamp. It is the console's
	// own reading of an age, and the figure a screen shows beside the driver.
	LeaseAgeSeconds *float64 `json:"lease_age_seconds"`
}

// MapEdge is one connection between two active orchestrators.
type MapEdge struct {
	// From and To are orchestrator ids, both nodes of the document. A
	// dependency points from the orchestrator that waits to the one it waits on.
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is MapEdgeDependency or MapEdgeOverlap.
	Kind string `json:"kind"`
	// Reason says what the edge is, in words, for an overlap — the collision
	// the coordinator stored, or the pull request two orchestrators share — and
	// is "" for a dependency, which the two ids already say.
	Reason string `json:"reason"`
}

// MapHealth is the coordinator's heartbeat as the map read it.
type MapHealth struct {
	// Sweep is whether the coordinator's sweep is on for this project. When it
	// is false the three times below are null and Note says why: a reader must
	// not draw "no sweep" as "nothing overlaps".
	Sweep bool `json:"sweep"`
	// StartedAt is when the last sweep started, OkAt when one last finished
	// reading everything, and At the server time the heartbeat was written; all
	// null when there is no heartbeat to read.
	StartedAt *time.Time `json:"started_at"`
	OkAt      *time.Time `json:"ok_at"`
	At        *time.Time `json:"at"`
	// Note says why the health is what it is, "" when there is nothing to say.
	Note string `json:"note"`
}

// EncodeMapDoc writes the document, refusing one this release would not read
// back. The two lists are filled in when they are empty, so the published shape
// is honest: a nil slice marshals as null, which the console's schema refuses.
func EncodeMapDoc(d MapDoc) ([]byte, error) {
	if err := ValidateMapDoc(d); err != nil {
		return nil, err
	}
	return json.Marshal(d.withLists())
}

// DecodeMapDoc reads the document. Strict, and checked exactly as it is on the
// way out: an unknown field is an error rather than something carried along,
// and every rule ValidateMapDoc holds is applied to what was read.
func DecodeMapDoc(raw []byte) (MapDoc, error) {
	d, err := decodeStrict[MapDoc](raw)
	if err != nil {
		return MapDoc{}, err
	}
	if err := ValidateMapDoc(d); err != nil {
		return MapDoc{}, err
	}
	return d, nil
}

// ValidateMapDoc is every rule the document must satisfy to be read. It is the
// document's own shape and not its subject matter: whether a figure is right is
// the builder's business (CG6), while a node of another project, an edge to an
// orchestrator the map does not draw and a name that disagrees with its id are
// rules a reader would otherwise have to guess at.
func ValidateMapDoc(d MapDoc) error {
	if d.Version != MapDocVersion {
		return fmt.Errorf("%w: the document says %d and this release writes %d", ErrSchemaVersion, d.Version, MapDocVersion)
	}
	if !config.ProjectNameRE.MatchString(d.Project) {
		return fmt.Errorf("%w: the project %q is not a project name", ErrInvalid, safetext.OneLine(d.Project))
	}
	seen := make(map[string]bool, len(d.Nodes))
	for _, n := range d.Nodes {
		project, name, ok := ParseOrchestratorID(n.ID)
		if !ok || project != d.Project {
			return fmt.Errorf("%w: the node %q is not an orchestrator of %s", ErrInvalid, safetext.OneLine(n.ID), safetext.OneLine(d.Project))
		}
		if n.Name != name {
			return fmt.Errorf("%w: the node %q carries the name %q, and its own name is %q", ErrInvalid, safetext.OneLine(n.ID), safetext.OneLine(n.Name), safetext.OneLine(name))
		}
		if seen[n.ID] {
			return fmt.Errorf("%w: two nodes carry the id %q", ErrInvalid, safetext.OneLine(n.ID))
		}
		seen[n.ID] = true
		if n.ActiveTasks < 0 || n.RunningJobs < 0 {
			return fmt.Errorf("%w: the node %q carries a negative count", ErrInvalid, safetext.OneLine(n.ID))
		}
		if n.LeaseAgeSeconds != nil && *n.LeaseAgeSeconds < 0 {
			return fmt.Errorf("%w: the node %q carries a negative lease age", ErrInvalid, safetext.OneLine(n.ID))
		}
	}
	for _, e := range d.Edges {
		if e.Kind != MapEdgeDependency && e.Kind != MapEdgeOverlap {
			return fmt.Errorf("%w: the edge kind %q is neither a dependency nor an overlap", ErrInvalid, safetext.OneLine(e.Kind))
		}
		if e.From == e.To {
			return fmt.Errorf("%w: the edge %q joins an orchestrator to itself", ErrInvalid, safetext.OneLine(e.From))
		}
		if !seen[e.From] || !seen[e.To] {
			return fmt.Errorf("%w: the edge %s -> %s names an orchestrator the document does not draw",
				ErrInvalid, safetext.OneLine(e.From), safetext.OneLine(e.To))
		}
	}
	if !d.Health.Sweep && (d.Health.StartedAt != nil || d.Health.OkAt != nil || d.Health.At != nil) {
		return fmt.Errorf("%w: the health says the sweep is off and carries a heartbeat", ErrInvalid)
	}
	return nil
}

// Sanitise makes the document's free text fit to print: a driver's principal,
// an edge's reason and the health's note pass safetext.Field, so no untrusted
// text reaches a renderer. The ids, the names and the kinds are identity and
// not prose — a sanitised id would name another orchestrator — so they are left
// as the builder wrote them, and ValidateMapDoc is what refuses one that is not
// an id.
func (d *MapDoc) Sanitise() {
	for i := range d.Nodes {
		d.Nodes[i].Driver = safetext.Field(d.Nodes[i].Driver)
	}
	for i := range d.Edges {
		d.Edges[i].Reason = safetext.Field(d.Edges[i].Reason)
	}
	d.Health.Note = safetext.Field(d.Health.Note)
}

// withLists is the document with its two lists present: an absent list is a
// list with nothing in it, and the JSON says so.
func (d MapDoc) withLists() MapDoc {
	if d.Nodes == nil {
		d.Nodes = []MapNode{}
	}
	if d.Edges == nil {
		d.Edges = []MapEdge{}
	}
	return d
}
