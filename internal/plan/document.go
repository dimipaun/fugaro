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
// It is 2 since the plan's graph learned the stream its plan ships in and the
// shape a node's target version has (GV1.3). The field is on the document rather
// than in this package's head because the console has to be able to tell "this
// graph is from a newer CLI" from "this graph is broken", and it can only do
// that if the number is in the document it was handed.
const SchemaVersion = 2

// FirstSchemaVersion is the graph schema this build reads beside the current
// one. A v1 artifact names no stream and spells a node's target version in the
// planner's own words, and it stays a document this build renders: the second
// schema added fields rather than changing one, so a plan written before it is
// not invalidated by it.
const FirstSchemaVersion = 1

// readsSchemaVersion reports whether this build reads a document written in
// schema version v. Two are read rather than one because the second only added
// fields: a v1 artifact is a graph this build draws, not one it refuses.
func readsSchemaVersion(v int) bool { return v >= FirstSchemaVersion && v <= SchemaVersion }

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
