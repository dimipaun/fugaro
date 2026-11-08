// Package plan reads the plan-side graph artifact: the document a planner
// writes beside the plan it describes, and the shape a read-only command prints
// it in.
//
// The console v2 design's D25 puts the graph on the plan's side rather than in
// a database (finding F6/F33), and this package is the Go half of that: the
// document type, the parser and the validation. The console never opens the
// file — it reads the document the way it reads every other document, as the
// `--json` output of a spawned `fugaro` command (design D1, §3.4 rule 7), which
// is why the schema lives here and is echoed to the console rather than being
// the console's own reading of a path it was handed.
//
// Two properties are the point of the package, and both are refusals rather
// than best-effort readings:
//
//   - a graph this build does not understand is refused, not rendered — a
//     document from a future schema version, a malformed one, one naming a node
//     twice or an edge to a node that is not there;
//   - a graph with a cycle is refused, not rendered. A cycle is not a drawing
//     problem: every "ready" and "blocked" answer is a function of the graph, so
//     a cyclic graph has no answers to give, and a view that rendered one would
//     be inventing them.
package plan

import "github.com/dimipaun/fugaro/internal/streams"

// SchemaVersion is the graph schema this build writes, and the newest it reads.
// It is 3 since the plan's graph learned the contract-first decoupling pass
// (D1811): a node's `kind`, a node's relative `size`, the stubs a contract names,
// the record of each broken edge and the change requests raised against a
// merged contract. The field is on the document rather than in this package's
// head because the console has to be able to tell "this graph is from a newer
// CLI" from "this graph is broken", and it can only do that if the number is in
// the document it was handed.
const SchemaVersion = 3

// FirstSchemaVersion is the graph schema this build reads beside the current
// one. A v1 artifact names no stream and spells a node's target version in the
// planner's own words, and it stays a document this build renders: the second
// schema added fields rather than changing one, so a plan written before it is
// not invalidated by it.
const FirstSchemaVersion = 1

// readsSchemaVersion reports whether this build reads a document written in
// schema version v. Three are read rather than one because each of the newer
// ones only added fields: a v1 or v2 artifact is a graph this build draws, not
// one it refuses.
func readsSchemaVersion(v int) bool { return v >= FirstSchemaVersion && v <= SchemaVersion }

// The four kinds of node a schema-3 graph names. A node carrying none is a
// task, which is what every node of a v1 or v2 graph is, so `kind` is absent
// from those documents rather than written out as the default.
const (
	// KindTask is ordinary work: a node of the plan that is not part of the
	// decoupling pass.
	KindTask = "task"
	// KindContract is a contract task: the interface the pass wrote down
	// before breaking an edge through it, and the stubs it leaves behind.
	KindContract = "contract"
	// KindIntegration is the integration task: it depends on both endpoints of
	// the edge the contract broke, deletes the contract's stubs and runs the
	// end-to-end checks.
	KindIntegration = "integration"
	// KindContractChange is a change request against a contract that has
	// already merged (D1812), which the planner records rather than edits.
	KindContractChange = "contract_change"
)

// nodeKinds is the closed set `kind` is held to, in the order a refusal names
// them.
var nodeKinds = []string{KindTask, KindContract, KindIntegration, KindContractChange}

// kindOf is the kind a node is, with the default spelled out: a node carrying
// no kind is a task.
func kindOf(n Node) string {
	if n.Kind == "" {
		return KindTask
	}
	return n.Kind
}

// validStream reports whether name is one of the release streams a plan may
// name (GD7). The list is internal/streams' own, the one the streams file is
// held to, so a plan cannot name a stream the catalogue does not carry; a value
// outside it is refused by name rather than drawn as a column whose heading
// nothing can fill.
func validStream(name string) bool { return streams.Known(name) }

// ArtifactSuffix is what a graph artifact is called beside its plan: the plan
// `docs/plans/2026-10-15-desktop-console-v2.md` is described by
// `docs/plans/2026-10-15-desktop-console-v2.graph.json`. A plan-side artifact
// rather than a database (D25), and beside the plan rather than in a directory
// of its own so that moving the plan moves its graph.
const ArtifactSuffix = ".graph.json"

// Document is the graph artifact: the plan it came from, the tasks as nodes,
// the dependencies as edges, and the schema version all of it is written in.
//
// Every slice is emitted as an array even when it is empty — an empty array is
// a fact ("this plan has no edges yet") and a null is not, which is the
// console's own availability rule, which its JSON contract states (§1).
type Document struct {
	SchemaVersion int    `json:"schema_version"`
	Plan          string `json:"plan"`
	// Stream is the release stream this plan ships in — one of cli, console or
	// mobile — and it is optional: absent, or null, is a plan that does not say,
	// which is a different statement from naming a stream nothing knows (that
	// one is refused). It is a display input, so it is written with omitempty
	// and a v1 artifact, which cannot carry one, prints no such key at all. What
	// a task actually ships in comes from the project record (ruling (d)).
	Stream *string `json:"stream,omitempty"`
	Nodes  []Node  `json:"nodes"`
	Edges  []Edge  `json:"edges"`
	// Decoupling is the record of every edge the decoupling pass broke: which
	// contract broke it, the edge it was, the integration node that covers it
	// and why breaking it was right (D1811, D1817). It is a schema-3 field and
	// it is omitted when the planner broke nothing, so a v3 graph of a plan
	// that did not run the pass prints no such key.
	Decoupling []Decoupling `json:"decoupling,omitempty"`
	// ContractChanges is the change requests raised against contracts that have
	// already merged (D1814). The document is where they are recorded because
	// it is git-tracked and reviewed with the plan, and because the read that
	// prints the graph is the read that shows them.
	ContractChanges []ContractChange `json:"contract_changes,omitempty"`
}

// Node is one task of the plan. ID is the planner's own name for it and is what
// the edges refer to; Title is what a person reads.
type Node struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Run is the run this task belongs to, null when it belongs to none: a
	// task that has not been launched has no run, and that is a fact rather
	// than an empty string.
	Run *string `json:"run"`
	// Version is the target version this task ships in, null when the plan
	// does not say. A schema-2 artifact spells it MAJOR.MINOR, optionally with
	// a patch — 0.10, or the patch box 0.10.1 a release such as 0.10.1 is — so
	// a release box can be drawn from it; a v1 artifact's version is free text
	// and is read as written. It is the version roadmap's source of truth until
	// the project record carries one (D29).
	Version *string `json:"version"`
	// Footprint is the planner's expected set of paths, advisory and always
	// labelled a prediction (D28, footprint.go).
	Footprint Footprint `json:"footprint"`
	// Kind is what this node is: one of the four above, and absent when it is
	// an ordinary task, which is every node of a v1 or v2 graph. It is a
	// schema-3 field, so the documents those schemas describe print without it.
	Kind string `json:"kind,omitempty"`
	// Size is the task's weight relative to the others, and it is how a
	// before/after path is compared: a contract task and an integration task
	// are small against the work they unblock, and a plan that could only count
	// tasks would report a loss for the simplest break (F5, D1816). It is
	// absent when the planner did not say, which means one — and a size of zero
	// or less is refused rather than read as "free work".
	Size *float64 `json:"size,omitempty"`
	// Stubs is the whole files a contract node leaves for its dependents to
	// compile against, which the integration task deletes (D1815, F9). It is
	// set on a contract node and nowhere else, each entry is a literal
	// repository-relative path rather than a glob, and each must appear in that
	// node's own footprint by name — a stub is a file the planner named, not
	// one a pattern happened to cover.
	Stubs []string `json:"stubs,omitempty"`
}

// Decoupling is one edge the pass broke, and it is the record CD3 computes the
// gain from: the CLI restores `was` to build the before graph rather than
// trusting any figure the planner wrote next to it.
//
// It is one entry per broken edge, and the entries naming one contract share
// one integration node (D1817): an integration task covers every edge its
// contract broke, not one edge each.
type Decoupling struct {
	// Contract is the node of kind contract the edge was broken through.
	Contract string `json:"contract"`
	// Was is the edge as it stood before the pass, which must not still be an
	// edge of this graph: an edge that is both broken and present is a
	// document disagreeing with itself.
	Was BrokenEdge `json:"was"`
	// Integration is the node of kind integration that depends on both
	// endpoints of `was`, and every entry naming the same contract names the
	// same one.
	Integration string `json:"integration"`
	// Reason is why the break is right — the same rule as an edge's own
	// reason: a break nobody can check is not a decoupling.
	Reason string `json:"reason"`
}

// BrokenEdge is a dependency as it stood before the pass broke it: the task,
// and the task it needed. It is an edge without a reason, because the reason
// belongs to the record of the break and not to the edge that no longer exists.
type BrokenEdge struct {
	Task  string `json:"task"`
	Needs string `json:"needs"`
}

// ContractChange is one request to change a contract that has already merged
// (D1812, D1814). It is a plan node rather than a comment on a pull request:
// an unlaunched dependent waits on it through an edge, and a launched one is
// reached by a follow-up run whose instructions name the contract file.
type ContractChange struct {
	// Change is the node of kind contract_change that carries the work.
	Change string `json:"change"`
	// Contract is the node of kind contract the change is against.
	Contract string `json:"contract"`
	// RaisedBy is the node that found the change — a dependent that cannot be
	// built against the contract as it merged.
	RaisedBy string `json:"raised_by"`
	// Reason is what has to change and why.
	Reason string `json:"reason"`
	// Affects names the tasks the change reaches; each one is a node of this
	// graph.
	Affects []string `json:"affects"`
}

// Edge is one dependency: Task depends on Needs, and Reason is why — the field
// is required rather than decorative, because an edge with no reason is an edge
// nobody can check. The direction is named in the fields rather than implied by
// their order: `{"task": "b", "needs": "a"}` reads the same way whichever way
// round a renderer walks it.
type Edge struct {
	Task   string `json:"task"`
	Needs  string `json:"needs"`
	Reason string `json:"reason"`
}
