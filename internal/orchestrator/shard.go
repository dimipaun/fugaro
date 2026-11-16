// A plan's own node and the standing backlog's: the two nodes D2735 puts
// beside the state node (F34).
//
// One state node carries MaxWorkItems items, MaxFacts facts and MaxStateBytes
// of JSON, and OverCap makes the daemon plan no spawn at all — so a plan of a
// hundred tasks, every open issue and the folded backlog cannot share it, and
// an issue author could stop every launch by filling it. D2735's answer splits
// the work across nodes: each imported plan has one, and the standing backlog
// has one. Every node keeps today's caps, holds a State through and through,
// and is read and saved by its own ETag under the one project lease, so
// nothing about the state node, its readers or its caps moves — and no
// migration is needed, the state node's own document being untouched.
//
// What the split costs is what F44 settles: validate.go resolves an item's
// `depends_on` inside the one state it validates and dependenciesDone reads one
// state, so a dependency never crosses nodes. An item's dependencies live in
// the node the item does, and the validator refuses one naming an item of
// another node for the same reason it refuses one naming nothing.

package orchestrator

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// Shard is which node of one project's orchestrator subtree a store reads and
// writes. Its values are the node's own name under `orchestrator/<id>`, so a
// shard named by a patch's `node` and a shard a daemon iterates are the same
// value and not two spellings of it.
type Shard string

const (
	// ShardState is the state node, `orchestrator/<id>/state` (StateNode). It
	// is the zero Shard, which is what keeps D2735's other half true: a store
	// built without one — every caller of this package from before there were
	// shards — is the node it always was.
	ShardState Shard = ""
	// ShardBacklog is the standing backlog's node,
	// `orchestrator/<id>/backlog`: the project's work items with no spec, which
	// are the ones `fugaro backlog add` writes and the issue ingest finds
	// (D2715).
	ShardBacklog Shard = BacklogNode
)

// PlanShard is one imported plan's node,
// `orchestrator/<id>/plans/<plan>`, where `<plan>` is the plan's folder under
// docs/ — the same name the import sets on the items it writes, because the
// two name the same plan (D2704). The name is checked where the path is built
// and not here, so a store is refused its own construction rather than a tick
// (NewNode), which is the rule the agent id and the role already follow.
func PlanShard(plan string) Shard { return Shard(PlansNode + "/" + plan) }

// PathPlan is one imported plan's node and PathBacklog is the standing
// backlog's. Each is a sibling of the state node, so the one write neither a
// daemon nor a person makes is a PUT of their parent (PathNode), which would
// wipe them all at once.
//
// A plan's folder is folded the way a work item's spec is (specName), because
// the two name the same plan: an item's spec is the folder the import read, and
// a plan whose two spellings differed would be a plan in two nodes — one the
// import writes, the other the tick reads.
//
// The check is the transport's too. The plan name is a property name in the
// tree, and the Realtime Database refuses `. $ # [ ] /` and control characters
// anywhere in one (validate.go, TestRTDBFakeRejectsKeysRealRTDBRejects), so a
// name admitted here and refused there would be a plan whose node no tick can
// ever create — the orchestrator id wedged, since every tick derives the same
// state again (D1231).
func PathPlan(agentID, plan string) (string, error) {
	name, ok := specName(plan)
	if !ok {
		return "", fmt.Errorf("%w: the plan %q is not a plan folder name", ErrInvalid, safetext.OneLine(plan))
	}
	parent, err := PathNode(agentID)
	if err != nil {
		return "", err
	}
	return parent + "/" + PlansNode + "/" + name, nil
}

func PathBacklog(agentID string) (string, error) { return pathOf(agentID, BacklogNode) }

// PathPlans is the parent of the plan nodes, `orchestrator/<id>/plans`: what a
// reader lists to find every plan the project has imported. It carries no
// document of its own, a plan node being a child of it.
func PathPlans(agentID string) (string, error) { return pathOf(agentID, PlansNode) }

// shardPath is the node a shard names under one orchestrator id, and the whole
// check on a shard: a name that is not one of the three is refused here, at the
// store's construction, rather than turned into a node nobody grants.
func shardPath(agentID string, shard Shard) (string, error) {
	switch shard {
	case ShardState:
		return PathState(agentID)
	case ShardBacklog:
		return PathBacklog(agentID)
	}
	plan, ok := strings.CutPrefix(string(shard), PlansNode+"/")
	if !ok {
		return "", fmt.Errorf("%w: %q is not a node of the orchestrator subtree", ErrInvalid, safetext.OneLine(string(shard)))
	}
	return PathPlan(agentID, plan)
}
