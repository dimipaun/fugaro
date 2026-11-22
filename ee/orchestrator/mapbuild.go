// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The map's builder (CG6 of docs/console-fixes): the read behind
// `fugaro orchestrator map --json`, which is what the console's Coordinator tab
// draws (D3221). internal/orchestrator/mapdoc.go is the shape, this is the
// subject matter, and the command is ee/orchestrator/cli's.
//
// The one thing it answers is **who is driving something right now**: a node is
// an orchestrator whose lease is fresh, and an orchestrator nobody drives is not
// a node with no figures but no node at all. So the read starts with the ids
// (`orchestrator.NodeIDs`, one shallow read of the subtree — the same read
// `fugaro orchestrator ls` makes, D1358) and a lease read per id, and only the
// fresh ones are read further: a state document is up to MaxStateBytes and a
// plan's is another, so an installation with forty orchestrators of which two
// are driven pays for two.
//
// The same read is why the map carries no history and no age of its own beyond
// the lease's: what a node is worth now is a fact somebody else is writing, and
// this read does not archive it.
//
// **Where it refuses.** A node the lease, a shard or a stored view cannot be
// read from fails the whole read rather than dropping that orchestrator from the
// document. The map document has no per-node failure state (unlike the graph's
// sources, §6.22), so the choice is between a document that is missing an
// orchestrator somebody is driving — which reads as "nobody drives it", the one
// thing a map must never say wrongly — and a read that says it could not be
// made. The second is the honest one, and the console draws the refusal's own
// sentence (D3221's reading 7). The driver node is the one exception, because
// the contract gives its failure a value: `driver: ""` is "the node is absent or
// unreadable" and never "nobody drives this" (reading 2).

// MapInput is everything the map is read from: the project's database, the
// coordinator's store, and the repositories whose stored views the overlap edges
// are joined over.
//
// Store is what the coordinator's own half of the map is read through, and a nil
// one is a project whose coordinator is not read at all: the map then says the
// sweep is off, which is the same document a project with no heartbeat gets
// (CG5, D3212). The CLI always passes one; a test that is about the nodes alone
// need not.
type MapInput struct {
	DB    *rtdb.Client
	Store *coordinator.Store
	// Project is the project whose map this is. The subtree holds every
	// orchestrator the installation's database knows, and an id of another
	// project is not a node of this document (ValidateMapDoc).
	Project string
	// Repos are the project's repositories, and Slug is how each one's node in
	// the coordinator's subtree is keyed — the same resolution the coordinator's
	// own read makes, so a repository whose provider cannot be resolved is
	// refused with the sentence that names the entry to set (repoSlug).
	Repos []string
	Slug  func(ctx context.Context, repo string) (string, error)
	// Now is the reader's clock: it stamps generated_at and nothing else. Every
	// age and every freshness question is measured against the database's own
	// clock (D1230), which the builder takes from the client after its first
	// read — a lease beat and the lease's TTL are both the server's, so a laptop
	// whose clock is off would read a live orchestrator as dead.
	Now time.Time
}

// BuildMap is the map: one read of the project's orchestrators, the edges
// between the ones that are being driven, and the coordinator's health.
//
// The document is validated before it is returned, so a caller that prints it
// prints one this release also reads back (`EncodeMapDoc` applies the same
// rule); the free text it carries is the caller's to sanitise, which the command
// does at the boundary (`Sanitise`).
func BuildMap(ctx context.Context, in MapInput) (orchestrator.MapDoc, error) {
	if err := checkMapInput(in); err != nil {
		return orchestrator.MapDoc{}, err
	}
	doc := orchestrator.MapDoc{
		Version:     orchestrator.MapDocVersion,
		Project:     in.Project,
		GeneratedAt: in.Now.UTC(),
		Nodes:       []orchestrator.MapNode{},
		Edges:       []orchestrator.MapEdge{},
	}
	health, err := readHealth(ctx, in)
	if err != nil {
		return orchestrator.MapDoc{}, err
	}
	doc.Health = health

	active, err := readActive(ctx, in)
	if err != nil {
		return orchestrator.MapDoc{}, err
	}
	for _, a := range active {
		doc.Nodes = append(doc.Nodes, orchestrator.MapNode{
			ID:              a.id,
			Name:            a.name,
			Driver:          a.driver,
			ActiveTasks:     orchestrator.ActiveTasks(a.states),
			RunningJobs:     orchestrator.CountsOfAll(a.states, a.serverNow).Running,
			LeaseAgeSeconds: leaseAgeSeconds(a.lease, a.serverNow),
		})
	}
	doc.Edges = append(doc.Edges, dependencyEdges(active)...)
	if doc.Health.Sweep {
		// The overlap edges are the sweep's own: they are joined out of the
		// collisions a tick stored and the pull requests its stored views hold,
		// and with no sweep nobody has looked (D3211, D3222, reading 6). The map
		// says so in the health rather than drawing the absence of a dashed edge
		// as "nothing overlaps".
		overlaps, err := overlapEdges(ctx, in, active)
		if err != nil {
			return orchestrator.MapDoc{}, err
		}
		doc.Edges = append(doc.Edges, overlaps...)
	}
	doc.Edges = dedupeEdges(doc.Edges)
	if err := orchestrator.ValidateMapDoc(doc); err != nil {
		return orchestrator.MapDoc{}, err
	}
	return doc, nil
}

// checkMapInput is what a map cannot be built without: a database to read, a
// project to read it for, and a clock to stamp it with. Each is refused here
// rather than at the first request, because a map of no project would be a
// document ValidateMapDoc refuses after every read it cost.
func checkMapInput(in MapInput) error {
	if in.DB == nil {
		return errors.New("ee/orchestrator: a map is read from the project's database, and none was given")
	}
	if !orchestrator.ValidOrchestratorID(in.Project) {
		return fmt.Errorf("ee/orchestrator: %q is not a project, so it names no map", safetext.OneLine(in.Project))
	}
	if _, err := orchestrator.DefaultOrchestratorID(in.Project); err != nil {
		return fmt.Errorf("ee/orchestrator: %q names a named orchestrator and not a project, and a map is one project's", safetext.OneLine(in.Project))
	}
	if in.Now.IsZero() {
		return errors.New("ee/orchestrator: a map carries the time it was assembled, so a clock is needed")
	}
	return nil
}

// readHealth is the coordinator's own state: its heartbeat, or the sweep being
// off.
//
// An absent heartbeat is the ordinary shape of an installation whose coordinator
// was never scheduled — the job is off by default (F23) — and it is a fact
// rather than a failure: the document says the sweep is off and carries no time
// at all, which is what a reader must have to tell "nobody looked" from "nothing
// overlaps" (reading 6, MapSweepOffNote).
func readHealth(ctx context.Context, in MapInput) (orchestrator.MapHealth, error) {
	if in.Store == nil {
		return orchestrator.MapHealth{Note: orchestrator.MapSweepOffNote}, nil
	}
	hb, found, err := in.Store.ReadHeartbeat(ctx)
	if err != nil {
		return orchestrator.MapHealth{}, fmt.Errorf("reading the coordinator's heartbeat: %w", err)
	}
	if !found {
		return orchestrator.MapHealth{Note: orchestrator.MapSweepOffNote}, nil
	}
	// `at` is the server time the heartbeat was written, and the heartbeat has
	// no stamp of its own: a tick writes started_at as it begins and ok_at when
	// it finishes without failing (CG5), so the newer of the two is when the
	// document was last written and the older is what the tick was doing.
	newest := hb.StartedAt
	if hb.OKAt.After(newest) {
		newest = hb.OKAt
	}
	return orchestrator.MapHealth{
		Sweep:     true,
		StartedAt: mapStamp(hb.StartedAt),
		OkAt:      mapStamp(hb.OKAt),
		At:        mapStamp(newest),
	}, nil
}

// mapStamp is one time as the document carries it: null for a stamp the heartbeat
// does not carry, which is a fact about that heartbeat and never "now".
func mapStamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// activeOrchestrator is one orchestrator that is being driven: the id, its lease
// claim, the principal its driver node names, and the states of every shard it
// keeps.
type activeOrchestrator struct {
	id        string
	name      string
	lease     orchestrator.LeaseClaim
	driver    string
	states    []orchestrator.State
	items     map[string]bool
	serverNow time.Time
}

// readActive is the read behind every node: the ids under `orchestrator/`, one
// lease read per id, and the shards and the driver of the ids whose lease is
// fresh.
//
// An id that is not an orchestrator of this project is left out rather than
// refused — the subtree holds every project the installation's database serves,
// and one map is one project — and so is an id this release cannot address,
// which NodeIDs already drops for the same reason.
func readActive(ctx context.Context, in MapInput) ([]activeOrchestrator, error) {
	ids, err := orchestrator.NodeIDs(ctx, in.DB)
	if err != nil {
		return nil, fmt.Errorf("listing the project's orchestrators: %w", err)
	}
	// The database's own clock, read after the first request because that is
	// where it comes from: a reader that measured a lease with this machine's
	// clock would answer "nobody is driving" for a machine whose clock is behind
	// (D1230).
	serverNow, ok := in.DB.ServerNow()
	if !ok {
		return nil, errors.New("the database answered without its own clock, and whether an orchestrator is driven is measured against the server's time rather than this machine's (D1230)")
	}
	slices.Sort(ids)
	active := []activeOrchestrator{}
	for _, id := range ids {
		project, name, ok := orchestrator.ParseOrchestratorID(id)
		if !ok || project != in.Project {
			continue
		}
		lease, err := orchestrator.ReadLeaseNode(ctx, in.DB, id)
		if err != nil {
			return nil, fmt.Errorf("reading the lease of %s: %w", safetext.OneLine(id), err)
		}
		if !lease.Fresh(serverNow) {
			continue
		}
		states, err := readShards(ctx, in, id)
		if err != nil {
			return nil, err
		}
		items := map[string]bool{}
		for _, s := range states {
			for _, w := range s.WorkItems {
				items[w.ID] = true
			}
		}
		active = append(active, activeOrchestrator{
			id:        id,
			name:      name,
			lease:     lease,
			driver:    readDriver(ctx, in, id),
			states:    states,
			items:     items,
			serverNow: serverNow,
		})
	}
	return active, nil
}

// readShards reads every node of one orchestrator: the state node, the standing
// backlog and one per imported plan (D2735). The states together are the
// orchestrator, which is what the map counts over and what a cross reference
// resolves against.
//
// A shard this release cannot read refuses the whole map rather than being
// counted as empty, which is the direction that never reports an orchestrator as
// carrying less work than it does.
func readShards(ctx context.Context, in MapInput, id string) ([]orchestrator.State, error) {
	shards := []orchestrator.Shard{orchestrator.ShardState, orchestrator.ShardBacklog}
	plans, err := orchestrator.PlanNames(ctx, in.DB, id)
	if err != nil {
		return nil, fmt.Errorf("listing the plans of %s: %w", safetext.OneLine(id), err)
	}
	for _, plan := range plans {
		shards = append(shards, orchestrator.PlanShard(plan))
	}
	states := make([]orchestrator.State, 0, len(shards))
	for _, shard := range shards {
		s, err := orchestrator.ReadShard(ctx, in.DB, id, orchestrator.RoleOrchestrator, shard)
		if err != nil {
			return nil, fmt.Errorf("reading the node %s of %s: %w", safetext.OneLine(shardName(shard)), safetext.OneLine(id), err)
		}
		states = append(states, s)
	}
	return states, nil
}

// shardName is one shard as a sentence names it: the state node, the backlog, or
// the plan whose node it is.
func shardName(shard orchestrator.Shard) string {
	if plan, ok := strings.CutPrefix(string(shard), orchestrator.PlansNode+"/"); ok {
		return "plan " + plan
	}
	if shard == orchestrator.ShardBacklog {
		return "backlog"
	}
	return "state"
}

// readDriver is the principal driving one orchestrator, and "" for a driver node
// that is absent or that this release cannot read — the contract's own reading
// of the field (reading 2), which is why the failure is carried rather than
// thrown: the node is on the map because a credential holds its lease, so an
// empty driver is a read that failed and never "nobody drives this".
func readDriver(ctx context.Context, in MapInput, id string) string {
	path, err := orchestrator.PathDriver(id)
	if err != nil {
		return ""
	}
	var raw json.RawMessage
	found, err := in.DB.Get(ctx, path, &raw)
	if err != nil || !found {
		return ""
	}
	d, err := orchestrator.DecodeDriver(raw)
	if err != nil {
		return ""
	}
	return d.Principal
}

// leaseAgeSeconds is how long ago the holder of one lease last beat, and nil for
// a lease that carries no stamp it can be measured from — never 0, which would
// be a claim about the beat that this reader does not have (reading 3). A stamp
// ahead of the server's clock is the skew a fresh lease is allowed (LeaseSkew)
// and reads as 0 rather than as a negative age, which the document refuses.
func leaseAgeSeconds(claim orchestrator.LeaseClaim, now time.Time) *float64 {
	if claim.At.IsZero() {
		return nil
	}
	age := now.Sub(claim.At).Seconds()
	if age < 0 {
		age = 0
	}
	return &age
}

// dependencyEdges is the cross references between the active orchestrators: one
// edge from the orchestrator whose item waits to the one it waits on (D3208).
//
// A reference resolves against the peer's own shards, so an edge is drawn only
// when the peer is on the map and holds the item — reading 5's rule that an edge
// never names an orchestrator the document does not draw. A dependency on the
// waiting orchestrator's own item is not an edge but the inside of one node
// (F44), and a bare item id is not a reference at all (ParseItemRef).
func dependencyEdges(active []activeOrchestrator) []orchestrator.MapEdge {
	byID := make(map[string]*activeOrchestrator, len(active))
	for i := range active {
		byID[active[i].id] = &active[i]
	}
	edges := []orchestrator.MapEdge{}
	for _, a := range active {
		for _, s := range a.states {
			for _, w := range s.WorkItems {
				for _, dep := range w.DependsOn {
					ref, err := orchestrator.ParseItemRef(dep)
					if err != nil || ref.Orchestrator == a.id {
						continue
					}
					peer, ok := byID[ref.Orchestrator]
					if !ok || !peer.items[ref.Item] {
						continue
					}
					edges = append(edges, orchestrator.MapEdge{
						From: a.id,
						To:   peer.id,
						Kind: orchestrator.MapEdgeDependency,
					})
				}
			}
		}
	}
	return edges
}

// overlapEdges is where two active orchestrators are on the same work, joined
// out of the coordinator's stored views (D3211, D3222). Two facts make an
// overlap, and both come from the view:
//
//   - a **stored collision** between two records, which is the coordinator's own
//     answer about two pull requests changing one file or one service — the two
//     records are named by label, so the edge's ends are the orchestrators whose
//     runs are on them;
//   - a **pull request two orchestrators carry runs on**, which the view holds
//     as the runs behind one record and which needs no collision to be true.
//
// The join runs through `WorkItem.run_id` and nothing else: a stored record
// carries no orchestrator id (F21), and the run id an orchestrator committed
// against its item is what says whose run it is.
//
// A view that holds nothing is no overlap and not a failure: the repository has
// never been reconciled, which is the ordinary shape of a project whose
// coordinator is young.
func overlapEdges(ctx context.Context, in MapInput, active []activeOrchestrator) ([]orchestrator.MapEdge, error) {
	runOrch := map[string]string{}
	for _, a := range active {
		for _, s := range a.states {
			for _, w := range s.WorkItems {
				if w.RunID != "" {
					runOrch[w.RunID] = a.id
				}
			}
		}
	}
	// Nothing a driven orchestrator has run is nothing the stored views can be
	// joined to, and that is worth knowing before a read per repository.
	if len(runOrch) == 0 {
		return nil, nil
	}
	edges := []orchestrator.MapEdge{}
	for _, repo := range in.Repos {
		slug, err := in.Slug(ctx, repo)
		if err != nil {
			return nil, err
		}
		view, err := in.Store.ReadRepoView(ctx, slug, repo)
		if err != nil {
			return nil, fmt.Errorf("reading the coordinator's view of %s: %w", safetext.OneLine(repo), err)
		}
		if !view.Stored {
			continue
		}
		byLabel := orchestratorsByLabel(view.View, runOrch)
		for _, c := range view.View.Collisions {
			for _, from := range byLabel[c.A] {
				for _, to := range byLabel[c.B] {
					if from == to {
						continue
					}
					edges = append(edges, orchestrator.MapEdge{
						From:   from,
						To:     to,
						Kind:   orchestrator.MapEdgeOverlap,
						Reason: collisionReason(c, repo),
					})
				}
			}
		}
		for _, rec := range view.View.Records {
			if rec.PR == nil || rec.PR.Number <= 0 {
				continue
			}
			orchs := orchestratorsOf(rec.Runs, runOrch)
			if len(orchs) < 2 {
				continue
			}
			for i := range orchs {
				for _, to := range orchs[i+1:] {
					edges = append(edges, orchestrator.MapEdge{
						From:   orchs[i],
						To:     to,
						Kind:   orchestrator.MapEdgeOverlap,
						Reason: fmt.Sprintf("both are on pull request #%d of %s", rec.PR.Number, safetext.OneLine(repo)),
					})
				}
			}
		}
	}
	return edges, nil
}

// orchestratorsByLabel is the active orchestrators behind each record of one
// stored view, keyed by the record's own label (Record.Label): a stored
// collision names its two records that way and nothing else, so this is the join
// from a collision to the orchestrators whose runs are on it.
//
// A record no active orchestrator has a run on is left out, and a label it does
// not carry is an empty list: both are collisions the map draws nothing for,
// because an edge's two ends are nodes of the document and a run nobody is
// driving is not a node.
func orchestratorsByLabel(view coordinator.View, runOrch map[string]string) map[string][]string {
	out := map[string][]string{}
	for _, rec := range view.Records {
		label := rec.Label()
		if label == "-" {
			continue
		}
		if orchs := orchestratorsOf(rec.Runs, runOrch); len(orchs) > 0 {
			out[label] = orchs
		}
	}
	return out
}

// orchestratorsOf is the active orchestrators behind one record's runs, each
// once, in id order.
func orchestratorsOf(runs []coordinator.Run, runOrch map[string]string) []string {
	seen := map[string]bool{}
	for _, run := range runs {
		if id, ok := runOrch[run.ID]; ok {
			seen[id] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// collisionReason says what one stored collision is, in the words a screen shows
// beside the dashed edge: the two records the coordinator stored it between, the
// repository it was found in, and what they share.
func collisionReason(c coordinator.Collision, repo string) string {
	shares := make([]string, 0, 2)
	if n := len(c.Files); n > 0 {
		shares = append(shares, counted(n, "shared file"))
	}
	if n := len(c.Services); n > 0 {
		shares = append(shares, counted(n, "shared service"))
	}
	return fmt.Sprintf("the coordinator stored a collision between %s and %s in %s: %s",
		c.A, c.B, safetext.OneLine(repo), strings.Join(shares, " and "))
}

// counted is one count and its noun, so a sentence about one file does not say
// "1 shared files".
func counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// dedupeEdges is the document's edges, each fact once and in one order: two
// references to one peer are one dependency, a collision stored for two
// repositories is one edge per repository, and a reader that drew the document
// twice must not see it move. The order is the edges' own — from, to, kind,
// reason — and never the order the reads happened to finish in.
func dedupeEdges(edges []orchestrator.MapEdge) []orchestrator.MapEdge {
	seen := make(map[orchestrator.MapEdge]bool, len(edges))
	out := make([]orchestrator.MapEdge, 0, len(edges))
	for _, e := range edges {
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}
