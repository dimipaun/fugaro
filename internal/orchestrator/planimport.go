package orchestrator

// A plan's graph as the work items of the plan's own node (D2704, D2735,
// D2737, D2745, D2746 of docs/orchestrator-graph-estimates).
//
// The import is the one path by which a plan reaches the daemon: the daemon has
// no checkout and cannot read a graph itself (F33), so a session in one reads
// the artifact and writes the items — and what it writes goes through the same
// typed patch and the same validator a person's patch does, so an item the
// import declares is an item like any other.
//
// Three rules shape everything here, and they are the plan's own decisions
// rather than this file's:
//
//   - an item is the plan's task (D2704): its id is the node's id, and the pair
//     the graph and the run record speak — `<plan>/<task>` — is the item's
//     `plan_task`, because a work item's id admits no slash (F37);
//   - a brief carries the plan's commit and the hash of its task's section
//     (D2737), so what a person approves is the section's content and not a
//     pointer that may move under it;
//   - a re-import keeps a node's brief, commit included, when its section's
//     hash is unchanged (D2745), so a standing approval survives the import
//     that added the plan's next task.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// PlanImportNode is one task of a plan's graph, as the import needs it: what it
// is called, what it is, and how big the planner expected it to be.
type PlanImportNode struct {
	ID    string
	Title string
	// Tier is the planner's expectation tier, and it is optional in the graph:
	// empty is a node the planner did not size, which is a different statement
	// from naming a tier nothing knows (the graph's own parser refuses that one).
	Tier string
}

// PlanImportEdge is one dependency of the graph, as the graph writes it: Task
// depends on Needs.
type PlanImportEdge struct {
	Task  string
	Needs string
}

// PlanImportInput is everything one import of a plan reads, handed in rather
// than read here: the graph, the plan's own text at the commit being pinned,
// and the three names every item this import writes carries.
type PlanImportInput struct {
	// Plan is the plan's folder under docs/, which is the node name, the items'
	// spec and the first half of their plan task.
	Plan string
	// Repo is the repository the plan is about, and Recipe is what the runs
	// launched from these items carry.
	Repo   string
	Recipe string
	// Commit is the commit the plan stands at, and PlanText is the plan
	// document at it: the two are pinned together in every brief, so a section
	// hashed here is a section that is in that commit.
	Commit   string
	PlanText string
	Nodes    []PlanImportNode
	Edges    []PlanImportEdge
}

// PlanImportPatch is G1's whole write: the patch that makes the plan's node
// hold one work item per node of the graph, and the ids of those items in node
// order.
//
// The ids are what the caller needs beside the patch — the `{id, digest}` list
// `plan_approve` takes is built from them against the state the patch leaves —
// and they name the item each node stands for, which is the node's own id
// everywhere but where the daemon has already adopted a run for that task
// (D2746): an adopted item's id is its run id, and the import links to it
// rather than writing a second item.
//
// It is a pure function of the state and the graph, and it refuses rather than
// guesses: a node id that is not an item id, a task the plan describes in no
// section, two items standing for one plan task, a graph whose patch would be
// longer than a patch may be. A refusal writes nothing, because the caller has
// nothing to write with.
func PlanImportPatch(s State, in PlanImportInput) (Patch, []string, error) {
	plan, ok := specName(in.Plan)
	if !ok {
		return Patch{}, nil, fmt.Errorf("%w: the plan %q is not a plan folder name", ErrInvalid, safetext.OneLine(in.Plan))
	}
	repo, err := task.CanonicalRepo(in.Repo)
	if err != nil {
		return Patch{}, nil, fmt.Errorf("%w: the repository %q is not owner/name", ErrInvalid, safetext.OneLine(in.Repo))
	}
	if !recipe.NameRE.MatchString(in.Recipe) {
		return Patch{}, nil, fmt.Errorf("%w: the recipe %q is not a recipe name", ErrInvalid, safetext.OneLine(in.Recipe))
	}
	if !headRE.MatchString(in.Commit) {
		return Patch{}, nil, fmt.Errorf("%w: the plan's commit %q is not a git object name", ErrInvalid, safetext.OneLine(in.Commit))
	}

	// Which of the plan's tasks already stand, and under which id. An item the
	// daemon adopted from a run in flight is keyed by its plan task exactly as
	// this import writes one, so the two meet here instead of both existing
	// (D2702, D2746).
	mine, err := planItems(s, plan)
	if err != nil {
		return Patch{}, nil, err
	}

	ids := make([]string, len(in.Nodes))
	at := make(map[string]int, len(in.Nodes))
	for i, n := range in.Nodes {
		if n.ID == "" {
			return Patch{}, nil, fmt.Errorf("%w: the graph carries a node with no id", ErrInvalid)
		}
		if _, dup := at[n.ID]; dup {
			return Patch{}, nil, fmt.Errorf("%w: two nodes of the graph share the id %q", ErrInvalid, safetext.OneLine(n.ID))
		}
		at[n.ID] = i
		if p := task.PlanTaskProblem(plan + "/" + n.ID); p != "" {
			return Patch{}, nil, fmt.Errorf("%w: the node %q is not a plan task: it %s", ErrInvalid, safetext.OneLine(n.ID), p)
		}
		id := n.ID
		if linked, adopted := mine[n.ID]; adopted {
			id = linked
		}
		if !idRE.MatchString(id) {
			// F37: the id rule admits no dot, and the plan this graph describes
			// is one whose task ids are written with them. The node has no item
			// this import may write, and folding the dot would key the item to a
			// task the plan does not have.
			return Patch{}, nil, fmt.Errorf("%w: the task id %q is not a work item id: an id is letters, digits, '-' and '_' (F37), so a plan whose tasks are numbered with dots cannot be imported until they are not", ErrInvalid, safetext.OneLine(n.ID))
		}
		ids[i] = id
	}

	needs := make(map[string][]string, len(in.Nodes))
	for _, e := range in.Edges {
		if _, ok := at[e.Task]; !ok {
			return Patch{}, nil, fmt.Errorf("%w: the edge %q -> %q names a task that is not a node of the graph", ErrInvalid, safetext.OneLine(e.Task), safetext.OneLine(e.Needs))
		}
		if _, ok := at[e.Needs]; !ok {
			return Patch{}, nil, fmt.Errorf("%w: the edge %q -> %q names a task that is not a node of the graph", ErrInvalid, safetext.OneLine(e.Task), safetext.OneLine(e.Needs))
		}
		if e.Task == e.Needs {
			return Patch{}, nil, fmt.Errorf("%w: the node %q depends on itself", ErrInvalid, safetext.OneLine(e.Task))
		}
		needs[e.Task] = append(needs[e.Task], e.Needs)
	}

	ops := make([]Op, 0, len(in.Nodes))
	added := 0
	for i, n := range in.Nodes {
		id, planTask := ids[i], plan+"/"+n.ID
		w, exists := s.Item(id)
		if exists {
			// The id is taken by an item of another plan, which is a collision
			// this import will not write over: two plans may each have a task
			// called `G1`, and the item under that id belongs to whoever wrote
			// it first — the import refuses and lets the graph be renamed,
			// rather than take an item another plan's edges point at (D2735
			// shards a plan into a node, and an id is unique inside one).
			if node, ok := planTaskNode(w.PlanTask, plan); !ok || node != n.ID {
				return Patch{}, nil, fmt.Errorf("%w: the work item %q already stands and is not this plan's: its plan task is %q and this import writes %q", ErrInvalid, safetext.OneLine(id), safetext.OneLine(w.PlanTask), safetext.OneLine(planTask))
			}
		}
		if exists && planItemLaunched(s, w) {
			// D2704: a re-import changes only the items not yet launched. What
			// the run is working from is the brief the person approved, and an
			// import that rewrote it would revoke that approval mid-run.
			continue
		}
		title := cleanLine(n.Title, MaxTitleRunes)
		if title == "" {
			return Patch{}, nil, fmt.Errorf("%w: the task %q carries no title", ErrInvalid, safetext.OneLine(n.ID))
		}
		rigor := tier.Medium
		if n.Tier != "" {
			// A node with no tier is medium, the same reading the estimate makes
			// of an unsized node (S2, D2722): every graph written before the tier
			// field existed carries none, and refusing them would leave the
			// import unusable on the plans that are waiting for it.
			if !tier.Known(tier.Tier(n.Tier)) {
				return Patch{}, nil, fmt.Errorf("%w: the task %q carries the tier %q, and a tier is one of %s", ErrInvalid, safetext.OneLine(n.ID), safetext.OneLine(n.Tier), tierList())
			}
			rigor = tier.Tier(n.Tier)
		}
		digest, ok := PlanSectionDigest(in.PlanText, n.ID)
		if !ok {
			return Patch{}, nil, fmt.Errorf("%w: the plan describes no section for the task %q, and a brief pins the section's hash: a task the plan does not describe cannot be imported (D2737)", ErrInvalid, safetext.OneLine(n.ID))
		}
		brief := PlanBrief(plan, n.ID, title, in.Commit, digest)
		if exists {
			if pinned, ok := planBriefPin(w.Brief); ok && pinned == digest {
				// D2745: the section is the same one, so the brief stands as it
				// is written — its commit included — and the approval a person
				// gave over that text is still the approval of it.
				brief = w.Brief
			}
		}
		deps := make([]string, 0, len(needs[n.ID]))
		for _, dep := range needs[n.ID] {
			// D2746: an edge naming a node the daemon adopted names the adopted
			// item, which is what the validator reads inside this one node.
			deps = append(deps, ids[at[dep]])
		}
		slices.Sort(deps)
		deps = slices.Compact(deps)

		op := WorkItemOp{ID: id}
		if !exists || w.Title != title {
			op.Title = &title
		}
		if !exists || w.Repo != repo {
			op.Repo = &repo
		}
		if !exists || w.Recipe != in.Recipe {
			op.Recipe = &in.Recipe
		}
		if !exists || w.RigorTier != string(rigor) {
			value := string(rigor)
			op.RigorTier = &value
		}
		if !exists || w.Spec != plan {
			op.Spec = &plan
		}
		if !exists || w.PlanTask != planTask {
			op.PlanTask = &planTask
		}
		if !exists || w.Brief != brief {
			op.Brief = &brief
		}
		if !slices.Equal(w.DependsOn, deps) {
			op.DependsOn = &deps
		}
		if !planOpCarries(op) {
			// Nothing about this item changed, so nothing is written: a
			// re-import that lands no op is what leaves every digest — and so
			// every standing approval — where it is (D2745).
			continue
		}
		if !exists {
			added++
		}
		ops = append(ops, Op{Op: OpWorkItemUpsert, WorkItem: &op})
	}

	// The two caps a graph can outgrow on its own. A patch carries MaxOps ops
	// (O0), and the node it lands on carries MaxWorkItems items; both are
	// refusals here rather than at Apply, because what the caller can do about
	// them — split the plan, or import it in parts — is a fact about the graph.
	if len(ops) > MaxOps {
		return Patch{}, nil, fmt.Errorf("%w: the graph carries %d tasks this import would write, and a patch carries at most %d (the node holds at most %d items)", ErrTooLarge, len(ops), MaxOps, MaxWorkItems)
	}
	if len(s.WorkItems)+added > MaxWorkItems {
		return Patch{}, nil, fmt.Errorf("%w: the node would hold %d work items, and it carries at most %d: a plan larger than that is imported in parts (D2735 shards a plan, not an item)", ErrTooLarge, len(s.WorkItems)+added, MaxWorkItems)
	}
	p := Patch{BaseRev: s.Rev, Ops: ops}
	if len(ops) > 0 {
		if err := ValidatePatch(p); err != nil {
			return Patch{}, nil, err
		}
	}
	return p, ids, nil
}

// planItemLaunched reports whether a work item has been launched, which is what
// D2704's re-import leaves alone: an item out of `queued`, one carrying a run
// id, or one an attempt has been committed for — an item back in `queued` after
// a failure has been launched, whatever its phase now says.
func planItemLaunched(s State, w WorkItem) bool {
	return w.Phase != PhaseQueued || w.RunID != "" || attemptsCommitted(s, w.ID) > 0
}

// planOpCarries reports whether an upsert would change anything. Every field of
// one is a pointer, so an op that carries none is a write of nothing: it would
// take the item's revision on for no change, and — because a fresh import
// compares against the item as it stands — an idempotent re-import must land
// none of them.
func planOpCarries(op WorkItemOp) bool {
	return op.Title != nil || op.Repo != nil || op.Recipe != nil || op.RigorTier != nil ||
		op.Spec != nil || op.PlanTask != nil || op.Brief != nil || op.DependsOn != nil
}

// planItems maps the node of a plan task to the id of the item that already
// stands for it, over the whole state.
//
// The plan half of a plan task is folded the way a spec name is (specName), so
// two spellings of one plan are one plan here as they are in the node's path
// (PathPlan): an item the daemon adopted names the plan as the launch it came
// from spelled it. Two items standing for one plan task is a node the import
// cannot place — there is no answer to which of them an edge should name, and
// an answer picked would be one of two — so it is refused rather than guessed.
func planItems(s State, plan string) (map[string]string, error) {
	mine := map[string]string{}
	for _, w := range s.WorkItems {
		node, ok := planTaskNode(w.PlanTask, plan)
		if !ok {
			continue
		}
		if other, dup := mine[node]; dup {
			return nil, fmt.Errorf("%w: %q and %q both stand for the plan task %s/%s, and one plan task is one work item (D2746)", ErrInvalid, safetext.OneLine(other), safetext.OneLine(w.ID), safetext.OneLine(plan), safetext.OneLine(node))
		}
		mine[node] = w.ID
	}
	return mine, nil
}

// planTaskNode is the node a plan task names when it names this plan. The task
// half is returned as written: it is the node id the graph spells, and the
// graph's own ids are what this import looks up by.
func planTaskNode(planTask, plan string) (string, bool) {
	name, node, ok := strings.Cut(planTask, "/")
	if !ok || node == "" || task.ASCIILower(name) != plan {
		return "", false
	}
	return node, true
}

// PlanBriefHead is the first line of every brief this import writes (D2737):
// the line a run's title comes from (task.Spec.TitleLine) when no launcher set
// one, so a run launched from an imported item is named by the plan it serves.
const PlanBriefHead = "# Task: "

// PlanBrief is the brief of a work item the import writes: the task's own line,
// and the one sentence that tells the run where its task is and which version
// of it was approved (D2737).
//
// The two pins are one statement. The commit is the plan's, and the hash is the
// task's section at it, so a run that reads the plan at that commit has the text
// the person approved, and a run that finds another section there refuses rather
// than working from what it found (F49).
func PlanBrief(plan, node, title, commit, digest string) string {
	return fmt.Sprintf("%s%s %s: %s\nRead your task from docs/%s/implementation-plan.md, section \"### %s:\", at commit %s, whose SHA-256 is %s: read the plan at that commit and refuse rather than work from a section whose hash differs.\n",
		PlanBriefHead, plan, node, title, plan, node, commit, digest)
}

// planBriefPin is the section digest a brief pins, or false for a brief that
// carries no pin at all — one a session wrote by hand, or one an earlier
// release wrote before the section was hashed. A brief with no pin is a brief
// this import cannot leave alone (D2745 keeps a brief whose section's hash is
// unchanged, and an unreadable hash is not an unchanged one).
//
// The parser is internal/plan's (BriefPin), beside the section it hashes,
// because the review stage reads the same pin out of the same text (D2932).
func planBriefPin(brief string) (string, bool) {
	pin, ok := plan.BriefPin(brief)
	if !ok {
		return "", false
	}
	return pin.Digest, true
}

// PlanSectionDigest is the SHA-256 of the section of a plan document that
// describes one task, in the spelling `Digest` uses everywhere else, or false
// when the document carries no such section.
//
// The parser is internal/plan's (SectionDigest) and not this package's: the
// review stage inside a run's job reads the same section to check a pull
// request against the task it was written for, and the job's binary does not
// link the orchestrator (D2932).
func PlanSectionDigest(doc, node string) (string, bool) {
	return plan.SectionDigest(doc, node)
}
