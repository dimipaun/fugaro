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
// state, so a dependency never crosses nodes **by a bare name**. An item's
// dependencies live in the node the item does, and the validator refuses one
// naming an item of another node for the same reason it refuses one naming
// nothing — which is why a dependency that really does cross a node is written
// as the reference D3208 adds, `<orchestrator>/<item>`, and resolved by
// searching that orchestrator's shards (ReadShards, FindItem).

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// Shard is which node of one project's orchestrator subtree a store reads and
// writes. Its value is the node's own name under `orchestrator/<id>` —
// `backlog`, and `plans/<plan>` — and the state node is the one that carries
// none here, because a store built without a shard is the node every caller of
// this package had before there were shards (ShardState, D2735).
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

// PlanNames lists the plans an orchestrator id has a node for, in order.
//
// One shallow read, not a read of the subtree, for the reason NodeIDs takes
// one: a plan node is a state document of up to MaxStateBytes, and a reader
// that only means to name them is not a reader that meant to download them.
// A key this release cannot address is left out rather than listed as a plan:
// every one of these paths is built from the name by PathPlan, so a name that
// function refuses is a node no reader of this release could have written and
// none can open.
func PlanNames(ctx context.Context, db Keys, agentID string) ([]string, error) {
	path, err := PathPlans(agentID)
	if err != nil {
		return nil, err
	}
	keys, err := db.Keys(ctx, path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		name, ok := specName(key)
		if !ok {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// ShardDB is the read half a cross-orchestrator reference is resolved through
// (D3208): one node read (Reader) and the keys of a plan list (Keys), which is
// the whole of what reading another orchestrator's shards needs. It is narrower
// than CAS on purpose, like Reader beside it: a cycle resolves a reference and
// writes nothing, and a seam that could not write is one that cannot write over
// another orchestrator's state by mistake.
type ShardDB interface {
	Reader
	Keys
}

// ReadShards reads every node of one orchestrator id: its state node, its
// standing backlog and one node per imported plan (D2735). It is the whole of
// what a reference into that orchestrator is resolved by searching (D3208), and
// it is a reader that holds nothing — no ETag and no lease — for the reason
// ReadShard is: what reads another orchestrator's items never writes its nodes.
//
// The role is the caller's, as it is for ReadShard: every node of this subtree
// belongs to the orchestrator loop, and a document another loop wrote is refused
// (ErrNotThisNode) rather than read as this one's.
func ReadShards(ctx context.Context, db ShardDB, agentID, role string) ([]State, error) {
	names, err := PlanNames(ctx, db, agentID)
	if err != nil {
		return nil, err
	}
	shards := make([]Shard, 0, len(names)+2)
	shards = append(shards, ShardState, ShardBacklog)
	for _, name := range names {
		shards = append(shards, PlanShard(name))
	}
	out := make([]State, 0, len(shards))
	for _, shard := range shards {
		s, err := ReadShard(ctx, db, agentID, role, shard)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// CrossReader reads the shards of one orchestrator id: what a reference into
// that orchestrator is resolved against (D3208). It is a seam and not the
// client because a cycle reads only the orchestrators its own items reference —
// the reader is asked for one id at a time, and nothing here lists them — which
// is what a test can hold it to.
//
// The nodes and not the items: a bare dependency of an item the reading found is
// resolved in the node that item is on and nowhere else (F44), so a reader that
// flattened an orchestrator's nodes into one list would lose the fact a cycle's
// search is held to.
type CrossReader interface {
	Shards(ctx context.Context, agentID string) ([]State, error)
}

// CrossShards is the CrossReader over the Realtime Database: every node one
// orchestrator has, read as the orchestrator loop reads its own (ReadShards).
type CrossShards struct {
	DB   ShardDB
	Role string
}

// Shards reads every shard of one orchestrator id. A node of the subtree that
// does not exist yet reads as that orchestrator's empty state, so an
// orchestrator with no item at all is no error — a reference into it simply
// resolves to nothing, which holds the item that names it.
func (r CrossShards) Shards(ctx context.Context, agentID string) ([]State, error) {
	if r.DB == nil {
		return nil, errors.New("orchestrator: a cross reference needs a store to be read through")
	}
	return ReadShards(ctx, r.DB, agentID, r.Role)
}

// FindItem finds the item a reference names among one orchestrator's items: by
// the work item's own id, or by the plan task it stands for (D3208, validItem).
// A name that is both one item's id and another's plan task is the first of them
// the shards carry, which is the order the state declares its items in.
func FindItem(items []WorkItem, name string) (WorkItem, bool) {
	for _, w := range items {
		if itemNamed(w, name) {
			return w, true
		}
	}
	return WorkItem{}, false
}

// itemNamed reports whether one item is the one a name refers to: its own id, or
// the plan task it stands for. It is the whole of what a reference's item half
// admits (validItem), and one rule for the two readers of it — the resolution of
// a reference (FindItem) and the cycle search that follows the same references.
func itemNamed(w WorkItem, name string) bool {
	return w.ID == name || (w.PlanTask != "" && w.PlanTask == name)
}

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
