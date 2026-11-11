// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package graph holds the console dependency graph's data and nothing else:
// the versioned document `fugaro graph --json` prints, the source-qualified key
// of a node, and the closed pattern of a scope argument. It is pure — it reads
// no file, opens no connection and has no clock of its own — so a caller can
// build, sanitise and marshal a document with no cloud, no checkout and no
// terminal.
//
// It is CG1 of docs/cli-output-console-graph/implementation-plan-graph.md: the
// document, its keys and its scope pattern. The sources that fill a document
// (the coordinator's stored view first) and the command that prints it are
// later tasks in that plan.
package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// DocumentVersion is the version of the document's own shape. A field's meaning
// never changes under it: removing or retyping a field bumps this, and a field
// whose meaning must change gets a new name (CLAUDE.md, "A field never changes
// meaning").
const DocumentVersion = 1

// Document is one snapshot of the scopes a caller asked for: what
// `fugaro graph --json` prints, and what `graph --watch --json` prints whole on
// every change (D1360). A one-shot read carries StreamID "" and Seq 0; a stream
// carries a stream_id drawn once per child and a seq that rises within it.
type Document struct {
	Version int `json:"version"`
	// StreamID names one child's stream; "" in a one-shot read.
	StreamID string `json:"stream_id"`
	// Seq rises within one StreamID and starts over with a new one; 0 in a
	// one-shot read.
	Seq uint64 `json:"seq"`
	// GeneratedAt is when the document was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// Sources is the state of every scope the document covers, so a reader can
	// tell a scope that is down from one that is empty (H3).
	Sources []SourceState `json:"sources"`
	// Releases are the project's release boxes, as the roadmap record holds them
	// (D2113, GV4.1). They are the project's and not a scope's: the record is
	// project-level (D2111), so a release is drawn whichever scope a task it
	// holds came from. Empty is the ordinary case for a project that has never
	// written one.
	Releases []Release `json:"releases"`
	// Nodes and Edges are the graph itself; a key in Edges is a Node's Key.
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Warnings are the things the read could not do, as sentences fit to
	// print; empty is the ordinary case.
	Warnings []string `json:"warnings"`
}

// Source states: a scope that is down is a state, not an exit, and a refused
// read is terminal (H3).
const (
	SourceUp         = "up"
	SourceDown       = "down"
	SourceStale      = "stale"
	SourceUnreadable = "unreadable"
)

// SourceState is one scope's own state, so a reader of the document can tell a
// scope that is down from one that is empty (H3).
type SourceState struct {
	// Source is the scope this state is about, as Scope.String writes it.
	Source string `json:"source"`
	// State is one of SourceUp, SourceDown, SourceStale, SourceUnreadable.
	State string `json:"state"`
	// AsOf is when the source's data was last known good; nil when there is no
	// such time, which is a source that has never read.
	AsOf *time.Time `json:"as_of"`
	// Reason says why, in words, when the state is not up; "" when up.
	Reason string `json:"reason"`
}

// Node kinds.
const (
	NodePullRequest = "pull_request"
	NodeRun         = "run"
	NodeTask        = "task"
)

// Node is one thing the graph draws. Key is `<source>:<id>` (NodeKey); keys are
// unique within one source, and nothing dedupes across sources (D1356).
type Node struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	// Kind is one of NodePullRequest, NodeRun, NodeTask.
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Repo    string `json:"repo"`
	State   string `json:"state"`
	Release string `json:"release"`
	// Updated is when the node last changed; nil when the source does not say.
	Updated *time.Time `json:"updated"`
	// Plan, Stream and Version are the roadmap record's own words about a task,
	// and "" for a node of any other source (GV4.1). Plan is the plan document
	// the task belongs to — the record's project-wide name, which with the task
	// id is the qualified id this node's `id` is (D2111) — and it is what marks
	// a node as the record's. Stream is the lane the task is drawn in: the
	// stream of the release it has been assigned to, or the plan's own for a
	// task nobody has placed (D2114). Version is the plan's own target, carried
	// as a display input for exactly that task — a release the record holds
	// carries a name and no version, so the two are never matched (D2114).
	Plan    string `json:"plan"`
	Stream  string `json:"stream"`
	Version string `json:"version"`
	// Dangling is D2115's mark: the record holds an assignment or an edge whose
	// task no plan document carries, and this node is that id. It is a state of
	// the record's own housekeeping rather than of the work.
	Dangling bool `json:"dangling,omitempty"`
}

// Release is one release box as the roadmap record holds it (D2113, GV4.1): the
// stream it is in, the record's own key for it, its name, the state the record
// calls it, and its position in its stream. A node's `release` is `Key` and
// nothing else, so the join from a task to its box happens in one place.
//
// There is deliberately no version: a stored release carries a name, which is
// what its box is drawn with, and a plan's own `version` is a different kind of
// value that is never matched to one (D2114).
type Release struct {
	Key    string `json:"key"`
	Stream string `json:"stream"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	// State is the record's own word for the release, `open` or `finished`
	// (coordinator.ReleaseOpen, coordinator.ReleaseFinished).
	State string `json:"state"`
	Order int    `json:"order"`
}

// Edge is one relation between two nodes, by Key. Kind is the source's own word
// for the relation; the coordinator's stored view has no edges, so the only
// edges a coordinator document carries are the roadmap record's cross-plan ones
// (D2112, GV4.1), and a document drawn from the stored view alone carries none
// (F17).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// Sanitise makes every text field of d fit to print: each one passes
// safetext.Field, and each node's Key is rebuilt from its Source and ID so the
// key and the fields it names cannot disagree. It is idempotent, and a document
// built from trusted text comes back unchanged.
//
// Every string here — a title, a repo, a branch, a note, a scope name — comes
// from a document someone else wrote, so a source that fills a document
// sanitises it before the document is printed, and no untrusted text reaches
// the console's renderer (H2).
//
// A node's Source and ID are the exception: they are its identity, not prose.
// safetext.Field is lossy — it drops controls and format characters, folds
// whitespace and cuts at FieldMax — so rewriting an identifier would let two
// distinct raw ids share one key, and keys are unique within one source
// (D1356). An identifier Field would change is therefore refused, and the
// caller reports the refusal rather than printing a document whose keys are
// wrong.
func (d *Document) Sanitise() error {
	d.StreamID = safetext.Field(d.StreamID)
	for i := range d.Sources {
		d.Sources[i].sanitise()
	}
	for i := range d.Releases {
		if err := d.Releases[i].sanitise(); err != nil {
			return err
		}
	}
	for i := range d.Nodes {
		if err := d.Nodes[i].sanitise(); err != nil {
			return err
		}
	}
	for i := range d.Edges {
		d.Edges[i].sanitise()
	}
	for i := range d.Warnings {
		d.Warnings[i] = safetext.Field(d.Warnings[i])
	}
	return nil
}

func (s *SourceState) sanitise() {
	s.Source = safetext.Field(s.Source)
	s.State = safetext.Field(s.State)
	s.Reason = safetext.Field(s.Reason)
}

func (n *Node) sanitise() error {
	if clean := safetext.Field(n.Source); clean != n.Source {
		return fmt.Errorf("graph: node source %q is not a clean identifier: sanitising it would merge two sources", clean)
	}
	if clean := safetext.Field(n.ID); clean != n.ID {
		return fmt.Errorf("graph: node %s has an id %q that is not a clean identifier: sanitising it would merge it with another node", n.Source, clean)
	}
	n.Kind = safetext.Field(n.Kind)
	n.Title = safetext.Field(n.Title)
	n.Repo = safetext.Field(n.Repo)
	n.State = safetext.Field(n.State)
	n.Release = safetext.Field(n.Release)
	n.Plan = safetext.Field(n.Plan)
	n.Stream = safetext.Field(n.Stream)
	n.Version = safetext.Field(n.Version)
	n.Key = NodeKey(n.Source, n.ID)
	return nil
}

// sanitise makes a release box fit to print. Its Key is `Release.Key`'s own
// promise — the value a task's `release` is compared with — so a key sanitising
// would rewrite would merge two boxes, and it is refused rather than rewritten
// exactly as a node's Source and ID are.
func (r *Release) sanitise() error {
	if clean := safetext.Field(r.Key); clean != r.Key {
		return fmt.Errorf("graph: release key %q is not a clean identifier: sanitising it would merge two releases", clean)
	}
	r.Stream = safetext.Field(r.Stream)
	r.ID = safetext.Field(r.ID)
	r.Name = safetext.Field(r.Name)
	r.State = safetext.Field(r.State)
	return nil
}

func (e *Edge) sanitise() {
	e.From = safetext.Field(e.From)
	e.To = safetext.Field(e.To)
	e.Kind = safetext.Field(e.Kind)
}

// MarshalJSON writes the document with every list as an array: an empty array
// is a fact ("this scope has no nodes") and a null is not, which is the
// console's own availability rule, as internal/plan's artifact already writes
// it. It does not touch the text — Sanitise is that step — so a caller
// sanitises before it prints.
func (d Document) MarshalJSON() ([]byte, error) {
	d.normalise() // d is a copy: the receiver is a value
	type plain Document
	return json.Marshal(plain(d))
}

// ParseDocument decodes one document, refusing a field this build does not know
// rather than dropping it, which is how every wire format here is read
// (CLAUDE.md). A list the document omits is read as the empty list it means,
// never carried as a null. It is the reader side of the shape: what a test, or
// a later task that reads the document back, uses to check it.
func ParseDocument(data []byte) (Document, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("graph: reading the document: %w", err)
	}
	d.normalise()
	return d, nil
}

// normalise replaces a nil list with the empty list it means, so the document
// writes and reads back arrays and never nulls.
func (d *Document) normalise() {
	if d.Sources == nil {
		d.Sources = []SourceState{}
	}
	if d.Releases == nil {
		d.Releases = []Release{}
	}
	if d.Nodes == nil {
		d.Nodes = []Node{}
	}
	if d.Edges == nil {
		d.Edges = []Edge{}
	}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
}
