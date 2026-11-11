// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is P9a of docs/coordinator/implementation-plan.md: the roadmap
// record's shape, version 1, and its reader. Nothing here writes a node — the
// writers are P9b, the front door's `roadmap:write` tools are P9d and the
// publish command is P9c — so every refusal a write owes (a stale expect, a
// cycle, a move into a finished release) belongs to those parts and not to
// this one.
//
// The record is project-wide (D2111): a plan's repository is provenance and
// not a dependency, so the documents live under D195's `coordinator/project/`
// beside the project's other facts and never under a repository's node. Four
// kinds of document are it, each keyed by coordpath.DocumentID over what it
// describes, so the same thing described twice is one node:
//
//	coordinator/project/roadmap/plans/<id>    one published plan artifact
//	coordinator/project/roadmap/releases/<id> one release (D2113)
//	coordinator/project/roadmap/assign/<id>   where one task ships
//	coordinator/project/roadmap/edges/<id>    one cross-plan dependency (D2112)
//
// Two things are deliberately NOT documents. A stream is the `stream` field
// its releases carry, so the record's streams are read from its releases and a
// watched repository that has no release catalogue works exactly the same way
// (D2113). And a task's release before anybody moves it is nothing at all: a
// task with no assignment is unassigned, no document is written for it, and
// the plan's own `version` is a display input beside it rather than a release
// it is placed in (D2114). There is no run-derived state either — running,
// done and blocked are computed from the runs, as internal/plan's State
// already does — so the record holds no second answer that ages.

// RoadmapSchema is the shape version every roadmap document carries, and the
// only one this build reads. A field never changes meaning under a version: a
// later shape is schema 2, and a document carrying one is skipped and counted
// (Roadmap.Unreadable) rather than guessed at, because a reader that read a
// field of a shape it does not know would draw a wrong figure with no error.
const RoadmapSchema = 1

// The two states a release's `state` carries (D2113). A release the record
// calls finished refuses a move into it (P9b), and that refusal is the
// record's own state rather than a rule about versions: a release that shipped
// is not a box a task is moved into afterwards.
const (
	ReleaseOpen     = "open"
	ReleaseFinished = "finished"
)

// PathRoadmap is the roadmap record's own node, the parent of the four
// collections below. It is a named node rather than a derived ID, like
// PathIntents and PathView, because there is one record per project and
// nothing describes it.
func PathRoadmap() string { return coordpath.PathProjectDocument("roadmap") }

// PathPlans is every published plan document, and PathPlan one of them, keyed
// by PlanID over the plan's project-wide name.
func PathPlans() string           { return PathRoadmap() + "/plans" }
func PathPlan(plan string) string { return PathPlans() + "/" + PlanID(plan) }

// PathReleases is every release document, and PathRelease one of them, keyed
// by ReleaseID over the stream and the release id.
func PathReleases() string { return PathRoadmap() + "/releases" }
func PathRelease(stream, id string) string {
	return PathReleases() + "/" + ReleaseID(stream, id)
}

// PathAssigns is every assignment document, and PathAssign one of them, keyed
// by AssignID over the qualified task id — so a task has at most one
// assignment, and a move is one document rewritten rather than one deleted and
// another created.
func PathAssigns() string           { return PathRoadmap() + "/assign" }
func PathAssign(task string) string { return PathAssigns() + "/" + AssignID(task) }

// PathEdges is every cross-plan edge document, and PathEdge one of them, keyed
// by EdgeID over both endpoints.
func PathEdges() string { return PathRoadmap() + "/edges" }
func PathEdge(task, needs string) string {
	return PathEdges() + "/" + EdgeID(task, needs)
}

// PlanID is the database key of one plan document: a function of the plan id
// and of nothing else. A plan id is a project-wide name (D2111) — two
// repositories that publish a plan of the same id are publishing one plan —
// and the provenance a publish records is deliberately not part of the key: it
// is provenance, nothing reads it back, and a plan republished from another
// checkout would otherwise be a second document beside the first.
func PlanID(plan string) string { return coordpath.DocumentID("roadmap-plan", plan) }

// ReleaseID is the database key of one release document: the stream and the
// release id, so two streams may each carry a release numbered the same and
// they are two documents.
func ReleaseID(stream, id string) string {
	return coordpath.DocumentID("roadmap-release", stream, id)
}

// AssignID is the database key of one assignment: the qualified task id and
// nothing else. No clock, no counter and no random source goes in, so a move
// lands on the document the task's assignment already is.
func AssignID(task string) string { return coordpath.DocumentID("roadmap-assign", task) }

// EdgeID is the database key of one cross-plan edge: both endpoints, in order,
// so the direction is part of the key — an edge from a to b is not one from b
// to a — and one pair of tasks is one edge whatever the reason beside it says.
func EdgeID(task, needs string) string {
	return coordpath.DocumentID("roadmap-edge", task, needs)
}

// SplitQualifiedID splits a qualified task id — the record's own name for one
// task, `<plan>/<task>` — at the last slash, as internal/plan's splitPlanTask
// does: a plan id may itself hold slashes (it is a path under docs/, so
// plans/2026-10-15-desktop-console-v2 is one), while a task id is a planner's
// own name and is what an edge or an assignment refers to. A value that does
// not split into two non-empty halves is not a qualified id.
func SplitQualifiedID(qualified string) (plan, task string, ok bool) {
	i := strings.LastIndexByte(qualified, '/')
	if i <= 0 || i == len(qualified)-1 {
		return "", "", false
	}
	return qualified[:i], qualified[i+1:], true
}

// The shape checks D2120 answers for a release and a stream, anchored so
// nothing before or after them gets in. They are the write side's — P9d
// validates a tool's arguments with them before P9b writes — and they are
// exported because the check and the write sit in different files and
// different parts. The plan artifact's own `stream` is not held to
// streamNamePattern: it is the parser's closed set of three
// (internal/streams), it is a display input, and it is never matched against a
// release.
var (
	releaseIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,39}$`)
	streamNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// ReleaseNameMax is how many characters a release's name may be (D2120).
const ReleaseNameMax = 64

// ValidReleaseID reports whether id is a release id: one lower-case letter or
// digit, then up to 39 more of those, a dot or a hyphen.
func ValidReleaseID(id string) bool { return releaseIDPattern.MatchString(id) }

// ValidStreamName reports whether name is a stream name: one lower-case letter
// or digit, then up to 31 more of those or a hyphen. A dot is not one: a
// stream name is a lane a graph draws, and it is spelled the way the
// installation's other ids are.
func ValidStreamName(name string) bool { return streamNamePattern.MatchString(name) }

// ValidReleaseName reports whether name is a release's name: at most
// ReleaseNameMax characters, and not empty. The bound is D2120's; the empty
// name is refused with it because a release's name is the label its box is
// drawn with (GV4.1), and D2113's shape makes it one of the release's required
// fields rather than one it may carry nothing in.
func ValidReleaseName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= ReleaseNameMax
}

// PlanSource is where a published plan was read from: the repository, the path
// under docs/, the commit HEAD named and the graph schema the artifact was
// written in. It is provenance only (D2111): nothing reads it back, no write
// depends on it except D2123's one sentence, and it is in no key.
type PlanSource struct {
	Repo        string `json:"repo"`
	Path        string `json:"path"`
	Commit      string `json:"commit"`
	GraphSchema int    `json:"graph_schema"`
}

// Task is one task of a published plan: what the graph draws and what a
// qualified id names. A task's run and its footprint are the artifact's and
// are not here — the record holds no run-derived state — and `version` is the
// artifact's own target, carried as a display input so a task with no
// assignment can be drawn beside it marked "from the plan" (D2114).
//
// It is not named for the run's own plan task field: that one is untrusted
// display text that nothing may decide anything by, and internal/runview's
// scan fails a file that names it outside its allow-list. This is a task of a
// plan document, and the two meet only where a graph joins them.
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Kind and Size are internal/plan's Node fields, carried under the same
	// names so a schema-3 graph is trimmed into the record without renaming
	// anything: a node carrying no kind is an ordinary task, and a node with no
	// size is one the planner did not weigh.
	Kind string   `json:"kind,omitempty"`
	Size *float64 `json:"size,omitempty"`
	// Version is the target version the artifact names, null when it names
	// none.
	Version *string `json:"version"`
}

// PlanEdge is one dependency inside one plan: two task ids of that plan. The
// reason stays in the artifact, where the planner reviews it; the record keeps
// the plan's own edges so a reader can tell an edge inside a plan from a
// cross-plan one, which is the edge document beside it (D2112).
type PlanEdge struct {
	Task  string `json:"task"`
	Needs string `json:"needs"`
}

// Plan is one published plan artifact as the record holds it: the plan's
// project-wide name, its stream, where it came from, its tasks and its own
// edges. What the record does not hold — a footprint, the decoupling record,
// the change requests, a stub list — stays in the artifact beside the plan.
type Plan struct {
	// Schema is the shape version of this document (RoadmapSchema).
	Schema int `json:"schema"`
	// Plan is the plan's id, the project-wide name (D2111). The node is keyed
	// by it (PlanID) and every qualified task id starts with it.
	Plan string `json:"plan"`
	// Stream is the artifact's own stream, a display input: the plan's tasks
	// are drawn in this lane until an assignment moves them.
	Stream string `json:"stream"`
	// Source is where the artifact was read from.
	Source PlanSource `json:"source"`
	// Tasks are the plan's tasks and Edges its own dependencies, both as the
	// artifact wrote them.
	Tasks []Task     `json:"tasks"`
	Edges []PlanEdge `json:"edges"`
}

// Release is one release of one stream, as the record holds it (D2113). A
// stream is not a document of its own: the record's streams are the `stream`
// values its releases carry, so a project whose repositories carry no release
// catalogue has exactly the streams its releases name.
type Release struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Name   string `json:"name"`
	State  string `json:"state"`
	// Order is the release's position in its stream, which is the order a
	// graph draws its boxes in. It is the record's own number and not a
	// version: a stored release carries a name and no version, which is why a
	// plan's `version` cannot be matched to one without inventing a rule
	// (D2114).
	Order int       `json:"order"`
	By    string    `json:"by"`
	At    time.Time `json:"at"`
}

// Assignment is where one task ships: the qualified task, the release it is in
// and the release that one replaced. A task with no assignment document is
// unassigned (D2114) — nothing is written for it, and it is not placed in a
// release box.
type Assignment struct {
	Schema int    `json:"schema"`
	Task   string `json:"task"`
	// Release is the release document's key (ReleaseID) and not its name: the
	// record holds one node per release and this is the reference to it.
	Release string `json:"release"`
	// Prev is the release this move displaced, null when the task had none. It
	// is what an undo inverts (GV3.2), so a move records where it came from
	// rather than a reader reconstructing it.
	Prev *string   `json:"prev"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// Edge is one cross-plan or cross-stream dependency (D2112): the task and what
// it needs, each a qualified id, and why. The reason is required — an edge
// nobody can check is not a dependency — and it is the one text here that
// comes from a person rather than from a plan file.
type Edge struct {
	Schema int       `json:"schema"`
	Task   string    `json:"task"`
	Needs  string    `json:"needs"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// Roadmap is the project's roadmap record as one read: every plan, release,
// assignment and edge it holds, how many documents this build could not read,
// and which ids are dangling.
type Roadmap struct {
	Plans       []Plan       `json:"plans"`
	Releases    []Release    `json:"releases"`
	Assignments []Assignment `json:"assignments"`
	Edges       []Edge       `json:"edges"`
	// Unreadable is how many documents the record holds that this build could
	// not read: one written in another schema, or one that does not decode.
	// They are skipped rather than read as empty, and counted rather than
	// failing the read, because a screen has to tell "the record holds
	// nothing" from "the record holds something this build cannot read" (the
	// graph's `unreadable` source state, console/docs/json-contract.md §6.22).
	Unreadable int `json:"unreadable"`
	// Dangling names the qualified task ids that are dangling (D2115): an
	// assignment or an edge whose endpoint no plan document carries, or an
	// assignment naming a release the record does not hold. They are kept and
	// reported rather than dropped — a task leaves every plan when a publish
	// drops it or an unpublish removes its plan, and the prune that finally
	// deletes the leftovers is a writer's (D2125) — and the list is sorted and
	// deduplicated, so two reads of one record answer the same thing.
	Dangling []string `json:"dangling"`
}

// Streams is the set of streams the record holds, sorted and deduplicated. It
// is the `stream` of its releases and nothing else (D2113): no catalogue file
// is read for it, so a repository that carries none loses nothing.
func (r Roadmap) Streams() []string {
	out := make([]string, 0, len(r.Releases))
	for _, rel := range r.Releases {
		out = append(out, rel.Stream)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ReadRoadmap reads the project's roadmap record: the four collections under
// PathRoadmap, every document in them decoded strictly.
//
// Three rules, each a decision a caller would otherwise have to guess at:
//
//   - A document this build cannot read is skipped and counted
//     (Roadmap.Unreadable). A hand-written document, an older build's or a
//     newer schema's is one node of the record, and it must not blank a screen
//     that could draw everything else — nor be read as an empty document,
//     which would claim the record holds nothing where it holds something this
//     build does not understand.
//   - Every text a person reads — a task's title, a release's name, an edge's
//     reason, a `by` — passes through safetext.Field on the way out, as the
//     writer passes it on the way in: the document is written by whoever holds
//     the write scope and read by a screen that must not obey it.
//   - Nothing is verified against anything else. This read reports what the
//     record holds; a cycle, a stale expect, an unknown release and a finished
//     release are the writers' refusals (P9b), and a reader that refused them
//     here would be a second answer to a question already answered.
//
// An absent record is an empty one and not a failure: "no plans published yet"
// is a fact about the project, and the caller that must tell it from a read
// that failed reads the error.
func (s *Store) ReadRoadmap(ctx context.Context) (Roadmap, error) {
	out := Roadmap{
		Plans:       []Plan{},
		Releases:    []Release{},
		Assignments: []Assignment{},
		Edges:       []Edge{},
		Dangling:    []string{},
	}
	var raw json.RawMessage
	if _, found, err := s.Read(ctx, PathRoadmap(), &raw); err != nil {
		return Roadmap{}, err
	} else if !found {
		return out, nil
	}
	// The collections are decoded leniently and the documents in them
	// strictly: the four collection names are this build's, and a collection a
	// later shape adds beside them must not fail a read that can still answer
	// everything else.
	var collections struct {
		Plans    map[string]json.RawMessage `json:"plans"`
		Releases map[string]json.RawMessage `json:"releases"`
		Assign   map[string]json.RawMessage `json:"assign"`
		Edges    map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(raw, &collections); err != nil {
		return Roadmap{}, fmt.Errorf("coordinator: reading /%s: %w: %w", PathRoadmap(), ErrUnreadable, err)
	}
	for _, key := range slices.Sorted(maps.Keys(collections.Plans)) {
		var doc Plan
		if !roadmapDocument(collections.Plans[key], &doc) {
			out.Unreadable++
			continue
		}
		out.Plans = append(out.Plans, doc.sanitised())
	}
	for _, key := range slices.Sorted(maps.Keys(collections.Releases)) {
		var doc Release
		if !roadmapDocument(collections.Releases[key], &doc) {
			out.Unreadable++
			continue
		}
		out.Releases = append(out.Releases, doc.sanitised())
	}
	for _, key := range slices.Sorted(maps.Keys(collections.Assign)) {
		var doc Assignment
		if !roadmapDocument(collections.Assign[key], &doc) {
			out.Unreadable++
			continue
		}
		out.Assignments = append(out.Assignments, doc.sanitised())
	}
	for _, key := range slices.Sorted(maps.Keys(collections.Edges)) {
		var doc Edge
		if !roadmapDocument(collections.Edges[key], &doc) {
			out.Unreadable++
			continue
		}
		out.Edges = append(out.Edges, doc.sanitised())
	}
	// The collections come back in key order, which is a hash's and means
	// nothing to a reader. Each list is put in the order its own documents
	// name, so two reads of one record answer the same lists and a graph draws
	// the same boxes in the same order.
	slices.SortFunc(out.Plans, func(a, b Plan) int { return strings.Compare(a.Plan, b.Plan) })
	slices.SortFunc(out.Releases, func(a, b Release) int {
		if c := strings.Compare(a.Stream, b.Stream); c != 0 {
			return c
		}
		if c := a.Order - b.Order; c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	slices.SortFunc(out.Assignments, func(a, b Assignment) int { return strings.Compare(a.Task, b.Task) })
	slices.SortFunc(out.Edges, func(a, b Edge) int {
		if c := strings.Compare(a.Task, b.Task); c != 0 {
			return c
		}
		return strings.Compare(a.Needs, b.Needs)
	})
	out.Dangling = danglingIDs(out)
	return out, nil
}

// roadmapDocument decodes one stored document strictly and reports whether
// this build reads it at all. A document of another schema is refused before
// the strict decode, so a newer shape's document is skipped whether or not its
// fields happen to fit this one, and the decode refuses a field this build
// does not know rather than dropping it.
func roadmapDocument(raw json.RawMessage, out any) bool {
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return false
	}
	if head.Schema != RoadmapSchema {
		return false
	}
	return strictDecode(raw, out) == nil
}

// danglingIDs names the qualified task ids no plan document carries, over
// every assignment and edge of one read (D2115). A task's own assignment
// naming a release the record does not hold dangles too, and it is the task
// that is named rather than the release: the release is gone and the
// assignment is what is left pointing at it.
func danglingIDs(r Roadmap) []string {
	known := map[string]bool{}
	for _, p := range r.Plans {
		for _, task := range p.Tasks {
			known[p.Plan+"/"+task.ID] = true
		}
	}
	releases := make(map[string]bool, len(r.Releases))
	for _, rel := range r.Releases {
		releases[ReleaseID(rel.Stream, rel.ID)] = true
	}
	var out []string
	for _, a := range r.Assignments {
		if !known[a.Task] || !releases[a.Release] {
			out = append(out, a.Task)
		}
	}
	for _, e := range r.Edges {
		for _, id := range []string{e.Task, e.Needs} {
			if !known[id] {
				out = append(out, id)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// The read's half of the record's promise about its text. Every string here is
// written by whoever holds the write scope and drawn by a console, a CLI or a
// log, so it is a field on the way out exactly as it is on the way in (P9b):
// safetext.Field maps whitespace, drops controls, escape sequences, bidi and
// zero-width characters, and bounds the length. The ids and the keys are not
// fields — a title is a label and a task id is a lookup — so nothing here
// rewrites one.

func (p Plan) sanitised() Plan {
	for i := range p.Tasks {
		p.Tasks[i].Title = safetext.Field(p.Tasks[i].Title)
	}
	return p
}

func (r Release) sanitised() Release {
	r.Name = safetext.Field(r.Name)
	r.By = safetext.Field(r.By)
	return r
}

func (a Assignment) sanitised() Assignment {
	a.By = safetext.Field(a.By)
	return a
}

func (e Edge) sanitised() Edge {
	e.Reason = safetext.Field(e.Reason)
	e.By = safetext.Field(e.By)
	return e
}
