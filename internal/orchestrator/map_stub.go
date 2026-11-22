// The map builder's stub: this release carries the document's shape and no
// builder (C-ORCH). The builder that reads the project's leases, its shards and
// the coordinator's heartbeat lives in ee/ (CG6), and I-ORCH deletes this file
// and points the command at it.

package orchestrator

import "time"

// BuildMap is what `fugaro orchestrator map --json` answers with until I-ORCH
// wires the real builder in: the empty document. No node, no edge, and a health
// that says the sweep is off — which is what a build with no builder can
// honestly say, and what a screen draws as nothing to show rather than as a
// project with no orchestrator running.
func BuildMap(project string, now time.Time) (MapDoc, error) {
	d := MapDoc{
		Version:     MapDocVersion,
		Project:     project,
		GeneratedAt: now.UTC(),
		Nodes:       []MapNode{},
		Edges:       []MapEdge{},
		Health:      MapHealth{Sweep: false, Note: MapSweepOffNote},
	}
	if err := ValidateMapDoc(d); err != nil {
		return MapDoc{}, err
	}
	return d, nil
}
