// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is the roadmap group's half of D2110's answer: the tools that put a
// change to the project's roadmap record into a queue, and the one tool that
// reads a queued request's outcome back (D2118). The record's shape and its
// reader are P9a's (ee/coordinator), its writers are P9b's, and the queue
// itself — the node a request is written to and the job that applies it as the
// coordinator's own account — is P9e's, which is why the two interfaces below
// are interfaces and not a client.
//
// **The front door holds no database write** (D2118, D922/H3), and that is a
// property of the types rather than of each handler: RoadmapQueue has one
// method that submits a request and one that reads an outcome, and no method
// that could write a node — not the roadmap's, not anything else's. What
// actually changes the record is a job that is not this process, holds no
// route of this front door and runs as the coordinator's account (P9e, D2124).
//
// **Every one of the tools maps to roadmap:write and to nothing else**
// (tools.go), and that scope is an operator's (D2121): the registry refuses
// every call here to a token whose holder is not on the installation's
// operator list, so nothing in this file has to ask who is calling — except
// the outcome read, which is the caller's own request and not the operator's
// writ (RoadmapOutcome.Actor).
//
// **The answers are data and carry no database path** (spec.md §9): a tool
// answers the request's own id and what it asked for, and the outcome it reads
// back, and never a node, a key or a subtree of the record. The record's layout
// is this installation's own, and the paths a writer's error names (P9b's
// messages carry them) are deliberately not what a caller is answered with:
// where a reader fails, the tool says the record was not read and nothing about
// where it lives.
//
// **Nothing here is served.** RegisterRoadmapTools is called by nothing in this
// repository: the front door is off on every installation (D934) and this
// group waits on P9e, which waits on M12 (the front door mounted on a binary)
// and, through it, on M12b's delivery to a paired phone (D2126). What this file
// fixes is the shape the served door will have, and the catalogue rows in
// tools.go are what make the scope of each name decided rather than chosen by
// whichever task registers it first.

// RoadmapQueue is where a roadmap write goes and where its outcome is read
// back (D2118, D2119).
//
// **It has no database write method, and that is the interface's own rule**
// (D2118, D922/H3, T2 — the shape StateReach has for the state half): the
// front door is a resource server that a stranger's client reaches, so the one
// thing it must not hold is a way to write the subtree the rules keep every
// client out of. A handler written over this queue therefore cannot change the
// record even if a later task writes one, and the process that does change it —
// P9e's apply job, running with the coordinator's own account — is a different
// process by construction and not by convention.
//
// The implementations this package's tests use are fakes; the one that writes
// is P9e's.
type RoadmapQueue interface {
	// Submit records one validated request, under the id it carries, and
	// answers nothing. A request this installation cannot record is an error,
	// and the caller is told the request was not queued (ErrStoreUnreadable):
	// a request that was dropped silently is a person waiting for an outcome
	// nothing will ever write.
	Submit(ctx context.Context, req RoadmapRequest) error
	// Outcome answers one request's outcome: applied, refused with the reason
	// and the value the record holds now, failed, or pending while the apply
	// job has not reached it. An id the queue holds no request for is
	// ErrRoadmapRequestUnknown.
	Outcome(ctx context.Context, id string) (RoadmapOutcome, error)
}

// RoadmapReader reads the project's roadmap record, and the version of one of
// its documents (D2118's "the tool checks what it can before it queues": a
// request built on a record that has since moved is refused here rather than
// applied over the newer one).
//
// Version answers the version the record holds one document under, and the
// empty string when it holds none — the value a request's `expect` is compared
// with, exactly as P9b's writers compare theirs. The path is built by the
// coordinator package's own Path* functions (PathPlan, PathRelease) and never
// out of the text a caller sent, which is what keeps this interface from being
// a way into a node of somebody else's choosing.
type RoadmapReader interface {
	ReadRoadmap(ctx context.Context) (coordinator.Roadmap, error)
	Version(ctx context.Context, path string) (string, error)
}

// RoadmapRequestKind is the writer one queued request asks for. It is the
// request's own discriminator rather than the tool's name, because two names
// are the same act with opposite arguments (need and unneed) and a queue that
// stored a tool name would be storing a name this package is free to change.
type RoadmapRequestKind string

const (
	// RequestPublishPlan writes one plan document into the record (P9b's
	// PublishPlan).
	RequestPublishPlan RoadmapRequestKind = "publish_plan"
	// RequestUnpublishPlan removes one published plan (P9b's UnpublishPlan).
	RequestUnpublishPlan RoadmapRequestKind = "unpublish_plan"
	// RequestMove puts one task's assignment in a release, or takes it out
	// (P9b's Move).
	RequestMove RoadmapRequestKind = "move"
	// RequestNeed records one dependency (P9b's AddEdge).
	RequestNeed RoadmapRequestKind = "need"
	// RequestUnneed removes one (P9b's RemoveEdge).
	RequestUnneed RoadmapRequestKind = "unneed"
	// RequestReleaseSet writes one release document (P9b's SetRelease).
	RequestReleaseSet RoadmapRequestKind = "release_set"
)

// RoadmapRequest is one roadmap write the front door has validated and queued
// (D2118). It is what the apply job reads and applies with P9b's writer of the
// kind it names.
//
// The fields are the union of the six kinds' arguments and a kind reads only
// its own: a request carries what the caller asked for, already checked against
// the record's shape rules and bounds, so the job applies it rather than
// re-deciding what a caller meant. Nothing here is a database path — Plan,
// Task, To and Needs are ids and names of the record's own vocabulary
// (ee/coordinator), and the job builds the nodes.
type RoadmapRequest struct {
	// ID is the request's id, minted by the front door and the name the queue
	// writes it under. It is the only handle the caller has on what it asked
	// for, and the one it presents to roadmap_request.
	ID string `json:"id"`
	// Kind is the writer this request asks for.
	Kind RoadmapRequestKind `json:"kind"`
	// Actor is the identity the call acted for, in the `user:<email>` entry
	// form the installation's lists are written in (D923). It is what the
	// apply job records as the write's `by`, and what makes an outcome
	// readable back by the person who queued it and by nobody else.
	Actor string `json:"actor"`
	// At is when the front door accepted the request, which is the order the
	// queue's own readers show them in.
	At time.Time `json:"at"`
	// Plan is the plan id an unpublish names; publish carries its document in
	// Document instead, because the document names its own plan.
	Plan string `json:"plan,omitempty"`
	// Document is the plan document a publish carries, trimmed to P9a's shape
	// and carrying its provenance in Source (D2111).
	Document *coordinator.Plan `json:"document,omitempty"`
	// Source is where the document was read from (D2111's provenance: D2123's
	// one sentence reads it, and nothing else does).
	Source *coordinator.PlanSource `json:"source,omitempty"`
	// Expect is the version the request was built on: the document's own for a
	// publish, an unpublish or a release write, and the release the record
	// holds for a move (NoneRelease for a task with no assignment, D2114).
	Expect string `json:"expect,omitempty"`
	// Task is the qualified task id an assignment or an edge names.
	Task string `json:"task,omitempty"`
	// To is the release a move puts the task in, as its key (ReleaseID) or
	// NoneRelease to take it out of one.
	To string `json:"to,omitempty"`
	// Needs is the second endpoint of a dependency.
	Needs string `json:"needs,omitempty"`
	// Reason is why a dependency exists (D2112: an edge nobody can check is
	// not one).
	Reason string `json:"reason,omitempty"`
	// Release is the release document a release write carries.
	Release *coordinator.Release `json:"release,omitempty"`
}

// The statuses RoadmapOutcome carries, in the vocabulary a caller reads: the
// three the plan names and the one a request that is still queued is in.
const (
	// RoadmapApplied is a request the apply job has carried out.
	RoadmapApplied = "applied"
	// RoadmapRefused is a request the record refused: the reason and the value
	// the record holds now are beside it, so a caller can decide what to ask
	// next without reading the record again.
	RoadmapRefused = "refused"
	// RoadmapFailed is a request the installation could not carry out at all:
	// a writer that could not reach the record, which is a fault of this
	// installation and not of the request.
	RoadmapFailed = "failed"
	// RoadmapPending is a request the queue holds and the apply job has not
	// reached: a queued request is applied after it is accepted (P9e), so
	// "there is nothing to read yet" is a state of the request and not a
	// failure of the read.
	RoadmapPending = "pending"
)

// RoadmapOutcome is what one queued request came to.
//
// It is written by the apply job and read back by the person who queued it:
// the reason and the current value are the writer's own words (P9b's
// refusals), and the job composes them for a caller rather than passing a
// database error through — the node layout stays this installation's (spec.md
// §9).
type RoadmapOutcome struct {
	// Request is the request's id, which is the one the caller presented.
	Request string `json:"request"`
	// Kind is what that request asked for.
	Kind RoadmapRequestKind `json:"kind"`
	// Status is one of the four above.
	Status string `json:"status"`
	// Reason is why a refused or failed request did not happen, in words a
	// person can act on.
	Reason string `json:"reason,omitempty"`
	// Current is the value the record holds now, beside a refusal that is a
	// stale `expect`: the version, the release, or the release key.
	Current string `json:"current,omitempty"`
	// Actor is the identity that queued the request. The door reads it to
	// answer the request to its own caller only, and it is not part of what a
	// caller is shown.
	Actor string `json:"actor"`
	// At is when the apply job wrote the outcome, zero while the request is
	// pending.
	At time.Time `json:"at,omitempty"`
}

// ErrRoadmapRequestUnknown refuses a roadmap request id this door has nothing
// to answer for: one the queue never held, and one that belongs to somebody
// else. The two are one answer on purpose — a caller that could tell them
// apart could ask which requests exist and whose they are, so the id is not an
// oracle about other people's writes.
var ErrRoadmapRequestUnknown = errors.New("mcpauth: no roadmap request of that id was queued for you")

// roadmapAnswerVersion is the version of the two documents the roadmap tools
// answer with, so that a later shape is a version and not a field that means
// something new (the console and P9f's command read these documents).
const roadmapAnswerVersion = 1

// The argument schemas, one per tool. Every one of them is closed
// (`additionalProperties: false`), which is the rule the decoders behind them
// enforce: a client that misspells an argument and is answered with a queued
// request it did not mean has been answered by a tool that did not read what
// it sent.
var (
	publishPlanArgsSchema = json.RawMessage(`{"type":"object","properties":{"plan":{"type":"object","description":"The plan document as the record holds it: its plan id, its stream, where it was read from, its tasks and its own edges"},"expect":{"type":"string","description":"The document version the publisher last read, empty for a plan the record does not hold"}},"required":["plan"],"additionalProperties":false}`)

	unpublishPlanArgsSchema = json.RawMessage(`{"type":"object","properties":{"plan":{"type":"string","description":"The plan id to remove from the record"},"expect":{"type":"string","description":"The document version the caller last read, empty for a plan the record does not hold"}},"required":["plan"],"additionalProperties":false}`)

	moveArgsSchema = json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"The qualified task id: <plan>/<task>"},"to":{"type":"string","description":"The release to put the task in, as the key the record holds, or none to take it out of one"},"expect":{"type":"string","description":"The release the record holds for this task now, or none for a task with no assignment"}},"required":["task","to","expect"],"additionalProperties":false}`)

	needArgsSchema = json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"The qualified task id that needs another: <plan>/<task>"},"needs":{"type":"string","description":"The qualified task id it needs: <plan>/<task>"},"reason":{"type":"string","description":"Why the dependency exists, which a reviewer reads"}},"required":["task","needs","reason"],"additionalProperties":false}`)

	unneedArgsSchema = json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"The qualified task id the dependency starts at: <plan>/<task>"},"needs":{"type":"string","description":"The qualified task id it pointed at: <plan>/<task>"}},"required":["task","needs"],"additionalProperties":false}`)

	releaseSetArgsSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"The release id within its stream"},"stream":{"type":"string","description":"The stream the release belongs to"},"name":{"type":"string","description":"The release's name, which is the label its box is drawn with"},"state":{"type":"string","description":"open or finished"},"order":{"type":"integer","description":"The release's position in its stream"},"expect":{"type":"string","description":"The document version the caller last read, empty for a release the record does not hold"}},"required":["id","stream","name","state","order","expect"],"additionalProperties":false}`)

	roadmapRequestArgsSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"The id of a roadmap request this client queued"}},"required":["id"],"additionalProperties":false}`)
)

// The arguments of the seven tools, each one the fields the table above names.
type (
	publishPlanArgs struct {
		Plan   coordinator.Plan `json:"plan"`
		Expect string           `json:"expect"`
	}
	unpublishPlanArgs struct {
		Plan   string `json:"plan"`
		Expect string `json:"expect"`
	}
	moveArgs struct {
		Task   string `json:"task"`
		To     string `json:"to"`
		Expect string `json:"expect"`
	}
	needArgs struct {
		Task   string `json:"task"`
		Needs  string `json:"needs"`
		Reason string `json:"reason"`
	}
	unneedArgs struct {
		Task  string `json:"task"`
		Needs string `json:"needs"`
	}
	releaseSetArgs struct {
		ID     string `json:"id"`
		Stream string `json:"stream"`
		Name   string `json:"name"`
		State  string `json:"state"`
		Order  int    `json:"order"`
		Expect string `json:"expect"`
	}
	requestArgs struct {
		ID string `json:"id"`
	}
)

// RegisterRoadmapTools registers the roadmap group on reg, over queue and
// reader: the six tools that queue a change and the one that reads an outcome
// back.
//
// It is called by nothing in this repository yet (P9e is the task that serves
// the group, and it waits on the front door being mounted, M12): what exists
// here is the shape, and the catalogue rows in tools.go are the part of it that
// is decided from today — a name the catalogue maps to roadmap:write, which
// only an operator may hold (D2121).
func RegisterRoadmapTools(reg *Registry, queue RoadmapQueue, reader RoadmapReader) error {
	if reg == nil || queue == nil || reader == nil {
		return errors.New("mcpauth: the roadmap tools need a registry, a queue and a reader of the record")
	}
	rt := roadmapTools{queue: queue, reader: reader}
	for _, t := range []Tool{
		{Name: "roadmap_publish_plan", InputSchema: publishPlanArgsSchema, Handler: rt.publishPlan},
		{Name: "roadmap_unpublish_plan", InputSchema: unpublishPlanArgsSchema, Handler: rt.unpublishPlan},
		{Name: "roadmap_move", InputSchema: moveArgsSchema, Handler: rt.move},
		{Name: "roadmap_need", InputSchema: needArgsSchema, Handler: rt.need},
		{Name: "roadmap_unneed", InputSchema: unneedArgsSchema, Handler: rt.unneed},
		{Name: "roadmap_release_set", InputSchema: releaseSetArgsSchema, Handler: rt.releaseSet},
		{Name: "roadmap_request", InputSchema: roadmapRequestArgsSchema, Handler: rt.request},
	} {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// roadmapTools is the seven handlers over one queue and one reader. The
// registry reads the scope a call needs out of the catalogue and the identity
// rule that comes with it (D2121), so no handler here states either.
type roadmapTools struct {
	queue  RoadmapQueue
	reader RoadmapReader
}

// roadmapQueued is what the six write tools answer: the request's own id, what
// kind of write it is, and what the caller asked for.
//
// It is the arguments the caller sent, echoed rather than described: a caller
// that asked for a move is shown the plan, the task, the release and the
// version it named and nothing this package composed about them — and the
// document is passed over by answerJSON like every other answer, so what a
// caller wrote reaches it as data (M-g).
type roadmapQueued struct {
	Version int                `json:"version"`
	Request string             `json:"request"`
	Kind    RoadmapRequestKind `json:"kind"`
	Asked   json.RawMessage    `json:"asked"`
}

// roadmapOutcomeDoc is what roadmap_request answers: the request's id, what it
// asked for, and the one thing the record did with it.
type roadmapOutcomeDoc struct {
	Version int                `json:"version"`
	Request string             `json:"request"`
	Kind    RoadmapRequestKind `json:"kind,omitempty"`
	Status  string             `json:"status"`
	Reason  string             `json:"reason,omitempty"`
	Current string             `json:"current,omitempty"`
}

// submit queues one validated request under a fresh id and answers the caller
// what it asked for.
//
// The id is the front door's and not the queue's, so that the answer the caller
// receives names the request whatever the queue does with it, and the actor is
// the identity the call resolved to (D923) rather than anything the caller
// sent: a request that named its own actor would be a queued write by whoever
// asked for it, which is the one thing the audit record and the write's `by`
// both have to be able to trust.
func (t roadmapTools) submit(ctx context.Context, call Call, req RoadmapRequest) (any, error) {
	id, err := NewID()
	if err != nil {
		return nil, fmt.Errorf("minting the request's id: %w", err)
	}
	req.ID, req.Actor, req.At = id, call.Identity, time.Now().UTC()
	if err := t.queue.Submit(ctx, req); err != nil {
		return nil, fmt.Errorf("%w: the roadmap request was not queued", ErrStoreUnreadable)
	}
	asked := call.Arguments
	if len(strings.TrimSpace(string(asked))) == 0 {
		asked = json.RawMessage(`{}`)
	}
	return answerJSON(roadmapQueued{
		Version: roadmapAnswerVersion, Request: id, Kind: req.Kind, Asked: asked,
	})
}

// publishPlan is roadmap_publish_plan: one plan artifact into the record.
//
// The document is the one P9c trimmed out of a checkout and its provenance is
// inside it (Plan.Source), which is where P9a's shape keeps it: the publisher
// is not one of the record's facts (P9b's PublishPlan asks for no actor), so a
// separate provenance argument would be a second place the same value is
// written.
func (t roadmapTools) publishPlan(ctx context.Context, call Call) (any, error) {
	var a publishPlanArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	doc := a.Plan
	if err := checkPlanDocument(doc); err != nil {
		return nil, err
	}
	if err := t.expectDocument(ctx, coordinator.PathPlan(doc.Plan), a.Expect); err != nil {
		return nil, err
	}
	doc.Schema = coordinator.RoadmapSchema
	source := doc.Source
	return t.submit(ctx, call, RoadmapRequest{
		Kind: RequestPublishPlan, Document: &doc, Source: &source, Expect: a.Expect,
	})
}

// unpublishPlan is roadmap_unpublish_plan: one plan document out of the record,
// leaving the tasks it carried dangling until the writer's prune (D2115).
//
// The `expect` is checked here and nowhere else: P9b's UnpublishPlan is
// idempotent and takes no version, so a caller that read a record holding no
// plan and asks to remove it is answered the queue rather than a refusal —
// which is what an unpublish of a plan that is already gone is.
func (t roadmapTools) unpublishPlan(ctx context.Context, call Call) (any, error) {
	var a unpublishPlanArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Plan) == "" {
		return nil, errors.New("a plan id is needed: an unpublish removes the plan document this id names")
	}
	if err := t.expectDocument(ctx, coordinator.PathPlan(a.Plan), a.Expect); err != nil {
		return nil, err
	}
	return t.submit(ctx, call, RoadmapRequest{Kind: RequestUnpublishPlan, Plan: a.Plan, Expect: a.Expect})
}

// move is roadmap_move: one task's assignment put in a release, or taken out of
// one (D2114's `none`).
//
// The record is read once, and it answers both halves of the check: the release
// the request names has to be one of the record's own keys, and the release the
// request expected has to be the one the task's assignment holds now. The keys
// are the record's (P9a's ReleaseID), so a caller can only name a release by
// having read the record, and its `expect` can only be right by having read the
// same version of it.
//
// What it does not decide is whether the move is a good one: a release the
// record calls finished, a task no plan document carries and a cycle are the
// writer's refusals (P9b's Move and AddEdge, applied by P9e), and a door that
// answered them here would be a second place the record's rules live.
func (t roadmapTools) move(ctx context.Context, call Call) (any, error) {
	var a moveArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	if _, _, ok := coordinator.SplitQualifiedID(a.Task); !ok {
		return nil, fmt.Errorf("%s is not a qualified task id: a move names one task as <plan>/<task>", safetext.Field(a.Task))
	}
	rm, err := t.readRecord(ctx)
	if err != nil {
		return nil, err
	}
	if a.To != coordinator.NoneRelease {
		if _, held := releaseOf(rm, a.To); !held {
			return nil, fmt.Errorf("no release of this record has the key %s: read the record and move the task into a release it holds, or none", safetext.Field(a.To))
		}
	}
	stored := coordinator.NoneRelease
	for _, assignment := range rm.Assignments {
		if assignment.Task == a.Task {
			stored = assignment.Release
		}
	}
	if stored != a.Expect {
		return nil, fmt.Errorf("the task's stored release is not the one this request expected: the record holds %s and the request expected %s; read the record again and move from what it holds", safetext.Field(stored), safetext.Field(a.Expect))
	}
	return t.submit(ctx, call, RoadmapRequest{Kind: RequestMove, Task: a.Task, To: a.To, Expect: a.Expect})
}

// need is roadmap_need: one dependency across plans or streams, with its reason
// (D2112).
//
// The reason is required and bounded (D2116): an edge nobody can check is not a
// dependency, and a reason is the one text here a person wrote rather than a
// plan artifact. What the record decides — that both endpoints are carried by a
// plan document, that an edge inside one plan belongs in the artifact, that the
// edge closes no cycle and that the record has room for it — is the writer's
// (P9b's AddEdge), asked by the job that applies this request.
func (t roadmapTools) need(ctx context.Context, call Call) (any, error) {
	var a needArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	task, needs, err := edgeEndpoints(a.Task, a.Needs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Reason) == "" {
		return nil, errors.New("a reason is needed: an edge nobody can check is not a dependency (D2112)")
	}
	if utf8.RuneCountInString(a.Reason) > coordinator.MaxEdgeReason {
		return nil, fmt.Errorf("a dependency's reason is at most %d characters, and this one is %d", coordinator.MaxEdgeReason, utf8.RuneCountInString(a.Reason))
	}
	return t.submit(ctx, call, RoadmapRequest{Kind: RequestNeed, Task: task, Needs: needs, Reason: a.Reason})
}

// unneed is roadmap_unneed: the removal of one recorded dependency, named by
// its two endpoints, which are the edge's key (P9a's EdgeID) and the whole of
// what identifies it — the reason beside an edge is not part of what it is.
func (t roadmapTools) unneed(ctx context.Context, call Call) (any, error) {
	var a unneedArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	task, needs, err := edgeEndpoints(a.Task, a.Needs)
	if err != nil {
		return nil, err
	}
	return t.submit(ctx, call, RoadmapRequest{Kind: RequestUnneed, Task: task, Needs: needs})
}

// releaseSet is roadmap_release_set: one release document of one stream
// (D2113, D2120).
//
// The shape checks are P9a's own, exported for exactly this (roadmap.go's
// note on the two patterns), and the `expect` is the document's version: a
// release is created with the empty version and changed only from the version
// the caller read.
func (t roadmapTools) releaseSet(ctx context.Context, call Call) (any, error) {
	var a releaseSetArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	rel := coordinator.Release{ID: a.ID, Stream: a.Stream, Name: a.Name, State: a.State, Order: a.Order}
	if err := checkReleaseArgument(rel); err != nil {
		return nil, err
	}
	if err := t.expectDocument(ctx, coordinator.PathRelease(rel.Stream, rel.ID), a.Expect); err != nil {
		return nil, err
	}
	rel.Schema = coordinator.RoadmapSchema
	return t.submit(ctx, call, RoadmapRequest{Kind: RequestReleaseSet, Release: &rel, Expect: a.Expect})
}

// request is roadmap_request: the outcome of one request this caller queued.
//
// It is the only tool of the group that is not a write, and it is the one place
// an identity decides something here: the outcome names the actor that queued
// the request, and a caller that is not that actor is answered exactly what a
// caller naming an id nobody queued is answered (ErrRoadmapRequestUnknown) —
// one answer for both, because a request id is not a caller's to probe with.
//
// An outcome is read back for a request that is still queued as `pending`,
// which is a state and not a fault: the front door queues and the apply job
// applies, so a caller that asks immediately has nothing to read yet.
func (t roadmapTools) request(ctx context.Context, call Call) (any, error) {
	var a requestArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.ID) == "" {
		return nil, errors.New("a request id is needed: this tool reads back one roadmap request this client queued")
	}
	out, err := t.queue.Outcome(ctx, a.ID)
	switch {
	case errors.Is(err, ErrRoadmapRequestUnknown):
		return nil, ErrRoadmapRequestUnknown
	case err != nil:
		return nil, fmt.Errorf("%w: the roadmap request's outcome was not read", ErrStoreUnreadable)
	}
	if out.Actor != call.Identity {
		return nil, ErrRoadmapRequestUnknown
	}
	status := out.Status
	if status == "" {
		status = RoadmapPending
	}
	return answerJSON(roadmapOutcomeDoc{
		Version: roadmapAnswerVersion, Request: a.ID, Kind: out.Kind,
		Status: status, Reason: out.Reason, Current: out.Current,
	})
}

// expectDocument refuses a request built on a version the record has moved past
// (the front door's half of P9b's compare-and-set: the writer checks again when
// it applies, and the two are not a duplication — this one answers a caller
// that is still waiting, and that one is what makes the write safe).
//
// A reader that cannot answer is the installation's fault and not the
// request's, so it is reported as the store being unreadable: a caller told its
// version was stale would read the record again against a record nobody can
// read.
func (t roadmapTools) expectDocument(ctx context.Context, path, expect string) error {
	current, err := t.reader.Version(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: the roadmap record was not read", ErrStoreUnreadable)
	}
	if current != expect {
		return fmt.Errorf("the record has moved since this request was built: it holds the version %q and the request expected %q; read the record again and send what it holds", current, expect)
	}
	return nil
}

// readRecord reads the whole record, for the tools whose arguments only the
// record can check: a release key, and the release a task's assignment holds.
func (t roadmapTools) readRecord(ctx context.Context) (coordinator.Roadmap, error) {
	rm, err := t.reader.ReadRoadmap(ctx)
	if err != nil {
		return coordinator.Roadmap{}, fmt.Errorf("%w: the roadmap record was not read", ErrStoreUnreadable)
	}
	return rm, nil
}

// releaseOf answers the release one of the record's keys names.
func releaseOf(rm coordinator.Roadmap, key string) (coordinator.Release, bool) {
	for _, rel := range rm.Releases {
		if coordinator.ReleaseID(rel.Stream, rel.ID) == key {
			return rel, true
		}
	}
	return coordinator.Release{}, false
}

// checkPlanDocument is the door's reading of P9a's shape rules and D2116's
// bounds over a plan document, before it is queued.
//
// It is not the writer's check (P9b's checkPlan is unexported and stays where
// the write is): it is the same rules read early, so that a document the record
// would refuse is never a request somebody waits on an outcome for. The
// refusals it can make are the ones a caller can act on from here — the plan's
// own id, a task's id and title, how many tasks one plan carries and how many
// bytes it is.
func checkPlanDocument(doc coordinator.Plan) error {
	if doc.Plan == "" {
		return errors.New("a plan document names its plan id, and this one has none")
	}
	if len(doc.Tasks) > coordinator.MaxPlanTasks {
		return fmt.Errorf("a plan carries at most %d tasks, and this one carries %d", coordinator.MaxPlanTasks, len(doc.Tasks))
	}
	seen := make(map[string]bool, len(doc.Tasks))
	for _, task := range doc.Tasks {
		if task.ID == "" {
			return fmt.Errorf("a task of %s has no id", safetext.Field(doc.Plan))
		}
		if strings.ContainsRune(task.ID, '/') {
			return fmt.Errorf("the task id %s of %s holds a slash: a task id is a name inside its plan, and the record's qualified id is <plan>/<task>", safetext.Field(task.ID), safetext.Field(doc.Plan))
		}
		if seen[task.ID] {
			return fmt.Errorf("%s names the task %s twice: one name is one task", safetext.Field(doc.Plan), safetext.Field(task.ID))
		}
		seen[task.ID] = true
		if utf8.RuneCountInString(task.Title) > coordinator.MaxTaskTitle {
			return fmt.Errorf("a title is at most %d characters, and %s's is %d", coordinator.MaxTaskTitle, safetext.Field(task.ID), utf8.RuneCountInString(task.Title))
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("the plan document is not one this record could hold: %v", err)
	}
	if len(raw) > coordinator.MaxPlanBytes {
		return fmt.Errorf("a plan document is at most %d bytes, and this one is %d", coordinator.MaxPlanBytes, len(raw))
	}
	return nil
}

// checkReleaseArgument is P9a's shape check over a release a caller named: a
// stream name, a release id and a name of the shapes D2120 fixes, one of the
// two states, and an order that is a position rather than a negative.
func checkReleaseArgument(rel coordinator.Release) error {
	if !coordinator.ValidStreamName(rel.Stream) {
		return fmt.Errorf("%s is not a stream name: a stream is lower-case letters, digits and hyphens, starting with a letter or a digit", safetext.Field(rel.Stream))
	}
	if !coordinator.ValidReleaseID(rel.ID) {
		return fmt.Errorf("%s is not a release id: a release id is lower-case letters, digits, dots and hyphens, starting with a letter or a digit", safetext.Field(rel.ID))
	}
	if !coordinator.ValidReleaseName(rel.Name) {
		return fmt.Errorf("a release's name is 1 to %d characters, and this one is %d", coordinator.ReleaseNameMax, utf8.RuneCountInString(rel.Name))
	}
	if rel.State != coordinator.ReleaseOpen && rel.State != coordinator.ReleaseFinished {
		return fmt.Errorf("%s is not a release state: a release is %s or %s", safetext.Field(rel.State), coordinator.ReleaseOpen, coordinator.ReleaseFinished)
	}
	if rel.Order < 0 {
		return fmt.Errorf("a release's order is a position in its stream, and %d is not one", rel.Order)
	}
	return nil
}

// edgeEndpoints reads the two endpoints of a dependency, each a qualified task
// id, and refuses a task that needs itself: a self-edge is refused by the
// writer too (P9b's AddEdge), and the one thing a caller can be told here is
// that its own two arguments are the same value.
func edgeEndpoints(task, needs string) (string, string, error) {
	if _, _, ok := coordinator.SplitQualifiedID(task); !ok {
		return "", "", fmt.Errorf("%s is not a qualified task id: a dependency names its endpoints as <plan>/<task>", safetext.Field(task))
	}
	if _, _, ok := coordinator.SplitQualifiedID(needs); !ok {
		return "", "", fmt.Errorf("%s is not a qualified task id: a dependency names its endpoints as <plan>/<task>", safetext.Field(needs))
	}
	if task == needs {
		return "", "", fmt.Errorf("%s cannot need itself", safetext.Field(task))
	}
	return task, needs, nil
}
