// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
)

// This file is the roadmap group's own tests: the seven tools over a queue and
// a reader a test supplies, the scope they map to, the identity that scope is
// served to (D2121), and the two properties the dormancy rests on — that the
// front door holds no database write, and that its answers name no node of the
// record (D2118, spec.md §9).
//
// **The queue is a fake and there is no apply job to reach**, which is the
// point of P9d rather than a gap in these tests: what is tested here is the
// front door's half, and the half that writes is P9e's — a different process,
// running as the coordinator's own account, which is why the interface this
// file drives has no method that could write anything.

// testRoadmapRelease is the one release the fixture's record holds, and the key
// a move into it names.
func testRoadmapRelease() coordinator.Release {
	return coordinator.Release{
		Schema: coordinator.RoadmapSchema, ID: "r1", Stream: "v1",
		Name: "1.0", State: coordinator.ReleaseOpen, Order: 0,
	}
}

// testPlanDocument is a plan document of the shape the record holds, with the
// plan id a caller names and one task in it.
func testPlanDocument(plan string) coordinator.Plan {
	return coordinator.Plan{
		Schema: coordinator.RoadmapSchema,
		Plan:   plan,
		Stream: "v1",
		Source: coordinator.PlanSource{
			Repo: "acme/app", Path: "docs/plans/" + plan + ".md", Commit: "4f2a1c9d", GraphSchema: 3,
		},
		Tasks: []coordinator.Task{{ID: "T1", Title: "publish the record"}},
		Edges: []coordinator.PlanEdge{},
	}
}

// fakeQueue is the queue as a test supplies it: the requests it holds, one
// outcome per id, the two failures a queue can be made to produce, and the log
// of what it was asked. The log is what tells a refusal that happened before
// the queue was reached from one that came out of it.
type fakeQueue struct {
	submitted  []RoadmapRequest
	held       map[string]RoadmapOutcome
	submitErr  error
	outcomeErr error
	asked      []string
}

func newFakeQueue() *fakeQueue { return &fakeQueue{held: map[string]RoadmapOutcome{}} }

func (q *fakeQueue) Submit(_ context.Context, req RoadmapRequest) error {
	q.asked = append(q.asked, "submit:"+string(req.Kind))
	if q.submitErr != nil {
		return q.submitErr
	}
	q.submitted = append(q.submitted, req)
	return nil
}

func (q *fakeQueue) Outcome(_ context.Context, id string) (RoadmapOutcome, error) {
	q.asked = append(q.asked, "outcome:"+id)
	if q.outcomeErr != nil {
		return RoadmapOutcome{}, q.outcomeErr
	}
	out, held := q.held[id]
	if !held {
		return RoadmapOutcome{}, ErrRoadmapRequestUnknown
	}
	return out, nil
}

// fakeReader is the record as a test supplies it: the documents one read
// answers, the version one document is at, and the two failures a read can be
// made to produce.
type fakeReader struct {
	record     coordinator.Roadmap
	versions   map[string]string
	readErr    error
	versionErr error
	asked      []string
}

func (r *fakeReader) ReadRoadmap(context.Context) (coordinator.Roadmap, error) {
	r.asked = append(r.asked, "read")
	if r.readErr != nil {
		return coordinator.Roadmap{}, r.readErr
	}
	return r.record, nil
}

func (r *fakeReader) Version(_ context.Context, path string) (string, error) {
	r.asked = append(r.asked, "version:"+path)
	if r.versionErr != nil {
		return "", r.versionErr
	}
	return r.versions[path], nil
}

// The versions the fixture's record holds: one published plan and one release,
// and nothing under the plan a publish names, which is what "the record holds
// no such document" is.
const (
	testPlanVersion    = "version-of-the-published-plan"
	testReleaseVersion = "version-of-the-release"
)

func testReader() *fakeReader {
	return &fakeReader{
		record: coordinator.Roadmap{
			Plans:       []coordinator.Plan{testPlanDocument("plans/x")},
			Releases:    []coordinator.Release{testRoadmapRelease()},
			Assignments: []coordinator.Assignment{{Schema: coordinator.RoadmapSchema, Task: "plans/x/T1", Release: coordinator.ReleaseID("v1", "r1")}},
			Edges:       []coordinator.Edge{},
		},
		versions: map[string]string{
			coordinator.PathPlan("plans/x"):     testPlanVersion,
			coordinator.PathRelease("v1", "r1"): testReleaseVersion,
			coordinator.PathRelease("v1", "r9"): testReleaseVersion,
		},
	}
}

// roadmapFixture is the group as the tests drive it: the registry the seven
// tools are registered on, the queue and the reader behind them, and the role
// the call acts for — an operator by default, because the scope these tools map
// to is one only an operator may hold (D2121) and a test that is not about that
// rule should not be able to fail for it.
type roadmapFixture struct {
	reg    *Registry
	queue  *fakeQueue
	reader *fakeReader
	role   Role
}

func newRoadmapFixture(t *testing.T) *roadmapFixture {
	t.Helper()
	queue, reader := newFakeQueue(), testReader()
	// The read-back tool's own id, queued by this same caller: the fixture
	// answers every one of the seven, so a test about a scope or about the queue
	// never has to build a queue first.
	queue.held["a-request-id-a-caller-read-back"] = RoadmapOutcome{
		Request: "a-request-id-a-caller-read-back", Kind: RequestMove, Status: RoadmapApplied, Actor: testIdentity, At: testNow,
	}
	reg := NewRegistry()
	if err := RegisterRoadmapTools(reg, queue, reader); err != nil {
		t.Fatalf("RegisterRoadmapTools: %v", err)
	}
	return &roadmapFixture{reg: reg, queue: queue, reader: reader, role: Role{Launcher: true, Operator: true}}
}

// call runs one of the roadmap tools through the registry, which is the one
// path a door reaches a handler by, and answers its document as text. The scope
// defaults to the one the whole group maps to.
func (f *roadmapFixture) call(t *testing.T, tool, args string, scopes ...string) (string, error) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{ScopeRoadmapWrite}
	}
	out, err := f.reg.Call(context.Background(), Call{
		Tool:      tool,
		Arguments: json.RawMessage(args),
		Scopes:    scopes,
		Identity:  testIdentity,
		Role:      f.role,
		ClientID:  "client-that-queued-it",
	})
	if err != nil {
		return "", err
	}
	text, ok := out.(string)
	if !ok {
		t.Fatalf("%s answered %T: a tool's answer is JSON text, which is what lets the whole document be passed over before the door writes it", tool, out)
	}
	return text, nil
}

// answer runs one tool that must answer.
func (f *roadmapFixture) answer(t *testing.T, tool, args string) string {
	t.Helper()
	text, err := f.call(t, tool, args)
	if err != nil {
		t.Fatalf("%s(%s): %v", tool, args, err)
	}
	return text
}

// roadmapName is the arguments object for a call naming a plan.
func roadmapPlanArgs(plan string) string { return fmt.Sprintf(`{"plan":%q}`, plan) }

// The seven names, in the order RegisterRoadmapTools registers them, with
// arguments the fixture answers: one call per tool, all of them valid, so a
// test about a scope or about the queue is one call per name and never a test
// that also has to build a document.
func roadmapCalls() []struct{ tool, args string } {
	rel := coordinator.ReleaseID("v1", "r1")
	return []struct{ tool, args string }{
		{"roadmap_publish_plan", fmt.Sprintf(`{"plan":%s,"expect":""}`, testPlanDocumentJSON("plans/new"))},
		{"roadmap_unpublish_plan", fmt.Sprintf(`{"plan":"plans/x","expect":%q}`, testPlanVersion)},
		{"roadmap_move", fmt.Sprintf(`{"task":"plans/x/T1","to":%q,"expect":%q}`, rel, rel)},
		{"roadmap_need", `{"task":"plans/x/T1","needs":"plans/y/T2","reason":"the shared library lands first"}`},
		{"roadmap_unneed", `{"task":"plans/x/T1","needs":"plans/y/T2"}`},
		{"roadmap_release_set", fmt.Sprintf(`{"id":"r9","stream":"v1","name":"9.0","state":"open","order":3,"expect":%q}`, testReleaseVersion)},
		{"roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`},
	}
}

// testPlanDocumentJSON is the plan document above as the arguments a caller
// sends: the document is marshalled from the same value the record's own shape
// check is written over, so a test cannot pass here with an argument object the
// tool refuses.
func testPlanDocumentJSON(plan string) string {
	raw, err := json.Marshal(testPlanDocument(plan))
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// TestRoadmapToolsMapToRoadmapWrite is D2121 as a property of the catalogue:
// the seven roadmap names map to roadmap:write, that scope covers those seven
// and no other name, and the group's tools are the seven RegisterRoadmapTools
// serves.
//
// The second half is the one a later task cannot quietly change: a name added
// to the roadmap scope, or a roadmap name moved to another scope, fails here
// rather than being served to whoever holds that other scope — and the scope is
// the operator's (OperatorOnlyScope), so what is really pinned is that the
// tools which rewrite the project's roadmap are reached by holders of one scope
// and nothing else.
func TestRoadmapToolsMapToRoadmapWrite(t *testing.T) {
	want := make([]string, 0, len(roadmapCalls()))
	for _, c := range roadmapCalls() {
		want = append(want, c.tool)
		scope, ok := ToolScope(c.tool)
		if !ok || scope != ScopeRoadmapWrite {
			t.Errorf("the catalogue maps %q to %q, %v; want %q: every roadmap tool is a write to the record and maps to the one scope that may make one (D2121)", c.tool, scope, ok, ScopeRoadmapWrite)
		}
	}

	onTheScope := make([]string, 0, len(want))
	for _, spec := range toolCatalogue {
		if spec.scope == ScopeRoadmapWrite {
			onTheScope = append(onTheScope, spec.name)
		}
	}
	if !slices.Equal(onTheScope, want) {
		t.Errorf("the catalogue maps %v to %q; want %v: the scope is the roadmap group's own, and a name on it is a name the operator rule serves", onTheScope, ScopeRoadmapWrite, want)
	}

	f := newRoadmapFixture(t)
	listed := make([]string, 0, len(want))
	for _, d := range f.reg.List() {
		listed = append(listed, d.Name)
	}
	if !slices.Equal(listed, want) {
		t.Errorf("RegisterRoadmapTools served %v; want %v", listed, want)
	}
	// Every one of the seven is reached by the scope it maps to, which is what
	// makes the mapping more than a table: a name registered under a handler of
	// its own still maps through the catalogue.
	for _, c := range roadmapCalls() {
		if _, err := f.call(t, c.tool, c.args, ScopeRoadmapWrite); err != nil {
			t.Errorf("%s with %q = %v; want it served: the scope the catalogue maps it to is the one the registry serves it to", c.tool, ScopeRoadmapWrite, err)
		}
	}
}

// roadmapIsUnreachable runs one call per roadmap tool with the scopes a test
// names and checks that none of them ran a handler: the refusal is the scope's,
// the queue was asked nothing, and the answer named no document.
func roadmapIsUnreachable(t *testing.T, f *roadmapFixture, scopes []string, what string) {
	t.Helper()
	for _, c := range roadmapCalls() {
		text, err := f.call(t, c.tool, c.args, scopes...)
		if !errors.Is(err, ErrScopeNotHeld) {
			t.Fatalf("%s with %s answered %q with %v; want ErrScopeNotHeld: %s must not silently become a roadmap writer", c.tool, what, text, err, what)
		}
		if text != "" {
			t.Errorf("%s answered %q beside its refusal", c.tool, text)
		}
	}
	if len(f.queue.asked) != 0 {
		t.Errorf("the queue was asked %v by calls that do not hold %q: the scope is checked before a handler runs", f.queue.asked, ScopeRoadmapWrite)
	}
	if len(f.reader.asked) != 0 {
		t.Errorf("the record was read (%v) for calls that do not hold %q: a refusal composed after reading the record would be the least useful half of the rule", f.reader.asked, ScopeRoadmapWrite)
	}
}

// TestATaskScopeTokenCannotReachTheRoadmapTools is D2121's reason in the
// direction a task token points: a token that may start a run, read a run or
// stop one reaches no roadmap tool, so the token a launcher's client holds
// cannot rewrite the project's roadmap.
//
// The control is a tool the task pair does reach, registered on the same
// registry: without it the refusals below would pass for a registry that serves
// nothing at all.
func TestATaskScopeTokenCannotReachTheRoadmapTools(t *testing.T) {
	f := newRoadmapFixture(t)
	if err := f.reg.Register(Tool{Name: "get_task_status", InputSchema: testSchema, Handler: func(context.Context, Call) (any, error) {
		return "a run", nil
	}}); err != nil {
		t.Fatalf("registering the control tool: %v", err)
	}
	roadmapIsUnreachable(t, f, []string{ScopeTasksRead, ScopeTasksWrite}, "a token holding the task pair")

	if _, err := f.reg.Call(context.Background(), Call{
		Tool: "get_task_status", Arguments: json.RawMessage(`{}`), Scopes: []string{ScopeTasksRead},
	}); err != nil {
		t.Fatalf("get_task_status with %q = %v; want the answer: the refusals above must be about the roadmap scope and not about a registry that serves nothing", ScopeTasksRead, err)
	}
}

// TestAStateScopeTokenCannotReachTheRoadmapTools is the same rule from the
// other group's side (the state group's own test makes the mirror of it): a
// token that may read or patch an orchestrator's state is not one that may
// rewrite the project's roadmap, and the two scopes are separate on purpose —
// one is the orchestrator's own node and one is the record every client reads.
func TestAStateScopeTokenCannotReachTheRoadmapTools(t *testing.T) {
	f := newRoadmapFixture(t)
	roadmapIsUnreachable(t, f, []string{ScopeStateRead, ScopeStateWrite}, "a token holding the state scopes")
}

// TestRoadmapToolsRefuseAStaleExpectBeforeQueueing is the front door's half of
// D2118's compare-and-set: a request built on a value the record has moved past
// is refused where it was queued, and nothing reaches the queue.
//
// It is the half a person feels. The writer checks again when it applies — P9b's
// compare-and-set is what makes the write safe — but a request that is already
// stale when it is sent has a caller who is still waiting for it, and the
// outcome it would read back an hour later is a refusal whose reason it could
// have been told at once. The queue's log is the assertion that matters: the
// record was read (or not) and the queue was never asked.
func TestRoadmapToolsRefuseAStaleExpectBeforeQueueing(t *testing.T) {
	stored := coordinator.ReleaseID("v1", "r1")
	for _, tc := range []struct {
		what     string
		tool     string
		args     string
		wantErr  string
		readOnly bool
	}{
		{
			what: "an unpublish from a version the plan has moved past",
			tool: "roadmap_unpublish_plan",
			args: fmt.Sprintf(`{"plan":"plans/x","expect":"a-version-the-record-has-moved-past"}`),
			// The current value is the record's own version, which is what the
			// caller reads again.
			wantErr: testPlanVersion,
		},
		{
			what: "a publish of a plan the record already holds, sent as a new one",
			tool: "roadmap_publish_plan",
			args: fmt.Sprintf(`{"plan":%s,"expect":%q}`, testPlanDocumentJSON("plans/x"), "a-version-the-record-has-moved-past"),
			// The record holds this plan, so the empty expect a first publish
			// carries is not what it holds now.
			wantErr: testPlanVersion,
		},
		{
			what:    "a release write from a version the release has moved past",
			tool:    "roadmap_release_set",
			args:    `{"id":"r9","stream":"v1","name":"9.0","state":"open","order":3,"expect":"a-version-the-record-has-moved-past"}`,
			wantErr: testReleaseVersion,
		},
		{
			what: "a move whose expect names no release where the task's assignment holds one",
			tool: "roadmap_move",
			args: fmt.Sprintf(`{"task":"plans/x/T1","to":%q,"expect":"none"}`, stored),
			// The stored release is what the caller sends back, and it is a key
			// of the record rather than a version: an assignment is a document
			// whose value is the release it points at.
			wantErr: stored,
		},
		{
			what:    "a move naming a release the record does not hold",
			tool:    "roadmap_move",
			args:    fmt.Sprintf(`{"task":"plans/x/T1","to":%q,"expect":%q}`, coordinator.ReleaseID("v1", "no-such-release"), stored),
			wantErr: "no release of this record",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newRoadmapFixture(t)
			text, err := f.call(t, tc.tool, tc.args)
			if err == nil {
				t.Fatalf("%s answered %q; want the stale request refused: a request the record has moved past is not one a person should wait on an outcome for", tc.tool, text)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the refusal is %q; want it to name what the record holds now (%q), which is the only thing the caller can act on", err, tc.wantErr)
			}
			if len(f.queue.asked) != 0 {
				t.Errorf("the queue was asked %v by a request that was refused for a stale value: the check is the front door's, and it comes before the write is queued", f.queue.asked)
			}
		})
	}

	// The control: the same tools with the values the record holds are queued,
	// which is what makes the refusals above about the version and not about
	// the tools refusing everything.
	f := newRoadmapFixture(t)
	for _, c := range roadmapCalls()[:6] {
		f.answer(t, c.tool, c.args)
	}
	if len(f.queue.submitted) != 6 {
		t.Fatalf("the queue holds %d requests; want one per write tool: %v", len(f.queue.submitted), f.queue.asked)
	}
	for _, req := range f.queue.submitted {
		if req.Actor != testIdentity {
			t.Errorf("a queued %s request carries the actor %q; want %q: the identity a write is recorded as is the one the call resolved to and never anything the caller sent", req.Kind, req.Actor, testIdentity)
		}
		if req.At.IsZero() {
			t.Errorf("a queued %s request carries no time", req.Kind)
		}
		if req.ID == "" {
			t.Errorf("a queued %s request carries no id: the id is the only handle the caller has on what it asked for", req.Kind)
		}
	}

	// A store that cannot be read is the installation's fault and not the
	// request's, and it is refused rather than queued: a request queued over a
	// record nobody read would be applied against whatever the record holds
	// when the job reaches it.
	broken := newRoadmapFixture(t)
	broken.reader.versionErr = errors.New("the record could not be read")
	broken.reader.readErr = broken.reader.versionErr
	for _, c := range roadmapCalls()[:3] {
		if _, err := broken.call(t, c.tool, c.args); !errors.Is(err, ErrStoreUnreadable) {
			t.Errorf("%s over a record that cannot be read = %v; want ErrStoreUnreadable: the answer is the installation's fault and the door says so", c.tool, err)
		}
	}
	if len(broken.queue.asked) != 0 {
		t.Errorf("the queue was asked %v over a record that could not be read", broken.queue.asked)
	}
}

// TestRoadmapToolsRefuseBadArgumentsBeforeQueueing is the door's reading of the
// record's own shape rules and D2116's bounds (P9a's checks, read early): a
// document, a task id, a reason or a release the record would refuse is refused
// here, so that nobody waits for an outcome that can only be a refusal.
//
// It is not the writer's check and does not replace it: P9b checks the whole
// rule again where the write happens — the room the record has, an endpoint no
// plan carries, a cycle, a finished release — and P9e's apply job is what turns
// those into an outcome. What is here is the half a caller can act on without
// an outcome at all.
func TestRoadmapToolsRefuseBadArgumentsBeforeQueueing(t *testing.T) {
	oversized := testPlanDocument("plans/new")
	oversized.Tasks = []coordinator.Task{{ID: "T1", Title: strings.Repeat("t", coordinator.MaxTaskTitle+1)}}

	for _, tc := range []struct{ what, tool, args string }{
		{"a plan document with no plan id", "roadmap_publish_plan", `{"plan":{"plan":"","tasks":[]},"expect":""}`},
		{"a task id carrying a slash", "roadmap_publish_plan", `{"plan":{"plan":"plans/new","tasks":[{"id":"a/T1","title":"t"}]},"expect":""}`},
		{"two tasks sharing one id", "roadmap_publish_plan", `{"plan":{"plan":"plans/new","tasks":[{"id":"T1","title":"t"},{"id":"T1","title":"again"}]},"expect":""}`},
		{"a title past D2116's bound", "roadmap_publish_plan", fmt.Sprintf(`{"plan":%s,"expect":""}`, mustJSON(oversized))},
		{"a task id that is not qualified", "roadmap_move", `{"task":"T1","to":"none","expect":"none"}`},
		{"an unpublish naming no plan", "roadmap_unpublish_plan", `{"plan":"  ","expect":""}`},
		{"a dependency with no reason", "roadmap_need", `{"task":"plans/x/T1","needs":"plans/y/T2","reason":"  "}`},
		{"a reason past D2116's bound", "roadmap_need", fmt.Sprintf(`{"task":"plans/x/T1","needs":"plans/y/T2","reason":%q}`, strings.Repeat("r", coordinator.MaxEdgeReason+1))},
		{"a dependency of a task on itself", "roadmap_need", `{"task":"plans/x/T1","needs":"plans/x/T1","reason":"why"}`},
		{"an endpoint that is not qualified", "roadmap_unneed", `{"task":"plans/x/T1","needs":"T2"}`},
		{"a stream name the record does not take", "roadmap_release_set", `{"id":"r9","stream":"V 1","name":"9.0","state":"open","order":3,"expect":""}`},
		{"a release state that is not one of the two", "roadmap_release_set", `{"id":"r9","stream":"v1","name":"9.0","state":"shipped","order":3,"expect":""}`},
		{"a release with no name", "roadmap_release_set", `{"id":"r9","stream":"v1","name":"","state":"open","order":3,"expect":""}`},
		{"a negative order", "roadmap_release_set", `{"id":"r9","stream":"v1","name":"9.0","state":"open","order":-1,"expect":""}`},
		{"an argument the tool does not take", "roadmap_move", `{"task":"plans/x/T1","to":"none","expect":"none","force":true}`},
		{"a request id that is empty", "roadmap_request", `{"id":"  "}`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newRoadmapFixture(t)
			if _, err := f.call(t, tc.tool, tc.args); err == nil {
				t.Fatalf("%s accepted %s: the record's shape rules and D2116's bounds are read before a request is queued", tc.tool, tc.what)
			}
			if len(f.queue.asked) != 0 {
				t.Errorf("the queue was asked %v by %s", f.queue.asked, tc.what)
			}
		})
	}
}

// mustJSON marshals a fixture value into the arguments a caller sends.
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// TestRoadmapQueueHasNoDatabaseWriteMethod is D2118 held as a property of the
// type rather than of the seven handlers written over it: the queue has exactly
// two methods, one that submits a request and one that reads an outcome back,
// and neither can write a node.
//
// The interface is what makes "the front door holds no database write" (D922/H3)
// more than a statement about the code in this file: a handler written over this
// queue could not write the record even if a later task added one, and a third
// method here would be a new power the front door has over the database — which
// is the decision D2124 answers by applying a request in another process, with
// the coordinator's own account, from a job nothing can reach.
func TestRoadmapQueueHasNoDatabaseWriteMethod(t *testing.T) {
	rt := reflect.TypeOf((*RoadmapQueue)(nil)).Elem()
	want := map[string]string{
		"Submit":  "func(context.Context, mcpauth.RoadmapRequest) error",
		"Outcome": "func(context.Context, string) (mcpauth.RoadmapOutcome, error)",
	}
	got := make([]string, 0, rt.NumMethod())
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		got = append(got, m.Name)
		sig, known := want[m.Name]
		if !known {
			t.Errorf("RoadmapQueue has a method %s: the queue submits a request and reads one outcome back, and holds no other — a Put, a Delete or a query over the record here is the database write the front door must not have", m.Name)
			continue
		}
		if m.Type.String() != sig {
			t.Errorf("RoadmapQueue.%s is %s; want %s", m.Name, m.Type, sig)
		}
	}
	slices.Sort(got)
	if names := []string{"Outcome", "Submit"}; !slices.Equal(got, names) {
		t.Errorf("RoadmapQueue's methods are %v; want %v", got, names)
	}
	// The tests' own queue is the only implementation this package has, so it is
	// the one that would have to grow such a method and the one held to the same
	// shape.
	if !reflect.TypeOf((*fakeQueue)(nil)).Implements(rt) {
		t.Error("the tests' own queue does not implement RoadmapQueue: the seven tools are tested over a queue they could not be written over")
	}
	if !reflect.TypeOf((*fakeReader)(nil)).Implements(reflect.TypeOf((*RoadmapReader)(nil)).Elem()) {
		t.Error("the tests' own reader does not implement RoadmapReader")
	}
}

// TestARequestOutcomeIsReadOnlyByItsCaller is roadmap_request's own rule: an
// outcome is read back by the person who queued it, and an id that is another
// caller's — like one nobody queued — is answered ErrRoadmapRequestUnknown.
//
// The two are one answer on purpose. A caller that could tell "no such request"
// from "not yours" holds an oracle over other people's writes: it could walk
// the id space and learn which requests exist, and a refusal that said "not
// yours" would confirm a request it guessed. The id is what a caller presents
// and the actor on the outcome is what decides, and the answer is the same
// sentence either way.
func TestARequestOutcomeIsReadOnlyByItsCaller(t *testing.T) {
	f := newRoadmapFixture(t)
	f.queue.held["a-request-id-a-caller-read-back"] = RoadmapOutcome{
		Request: "a-request-id-a-caller-read-back", Kind: RequestMove, Status: RoadmapApplied,
		Actor: "user:someone-else@example.com", At: testNow,
	}

	_, err := f.call(t, "roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`)
	if !errors.Is(err, ErrRoadmapRequestUnknown) {
		t.Fatalf("reading back another caller's request = %v; want ErrRoadmapRequestUnknown", err)
	}
	_, unknown := f.call(t, "roadmap_request", `{"id":"a-request-id-nobody-ever-queued"}`)
	if !errors.Is(unknown, ErrRoadmapRequestUnknown) {
		t.Fatalf("reading back an id nobody queued = %v; want ErrRoadmapRequestUnknown", unknown)
	}
	if err.Error() != unknown.Error() {
		t.Errorf("the two answers differ (%q and %q): a caller that could tell them apart could ask which requests exist and whose they are", err, unknown)
	}

	// The same request, read back by the person who queued it: the outcome and
	// nothing about the record it was applied to.
	f.queue.held["a-request-id-a-caller-read-back"] = RoadmapOutcome{
		Request: "a-request-id-a-caller-read-back", Kind: RequestMove, Status: RoadmapRefused,
		Reason: "the release this task was moved into is finished", Current: coordinator.ReleaseID("v1", "r1"),
		Actor: testIdentity, At: testNow,
	}
	doc := decodeRoadmapAnswer(t, f.answer(t, "roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`))
	if doc["status"] != RoadmapRefused || doc["kind"] != string(RequestMove) {
		t.Errorf("the outcome answered %v; want the refused move it read back", doc)
	}
	if doc["reason"] != "the release this task was moved into is finished" || doc["current"] != coordinator.ReleaseID("v1", "r1") {
		t.Errorf("the outcome answered %v; want the writer's reason and the value the record holds now beside it: that pair is what lets a caller decide what to ask next without reading the record again", doc)
	}

	// A request the apply job has not reached yet is a state and not a fault:
	// the front door queues and a job applies, so "nothing to read yet" is what
	// an immediate read back is answered.
	f.queue.held["a-request-id-a-caller-read-back"] = RoadmapOutcome{
		Request: "a-request-id-a-caller-read-back", Kind: RequestNeed, Actor: testIdentity,
	}
	doc = decodeRoadmapAnswer(t, f.answer(t, "roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`))
	if doc["status"] != RoadmapPending {
		t.Errorf("the outcome of a request the job has not reached is %v; want %q", doc["status"], RoadmapPending)
	}

	// A queue that cannot answer is the installation's fault, and it is said as
	// one: a caller told "no such request" would stop asking about a request
	// that exists.
	broken := newRoadmapFixture(t)
	broken.queue.outcomeErr = errors.New("the queue could not be read")
	if _, err := broken.call(t, "roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`); !errors.Is(err, ErrStoreUnreadable) {
		t.Errorf("reading back a request over a queue that cannot be read = %v; want ErrStoreUnreadable", err)
	}
}

// decodeRoadmapAnswer decodes one answer document into the members it carries,
// which is how the tests below read a shape rather than a sentence.
func decodeRoadmapAnswer(t *testing.T, text string) map[string]string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("the answer is not a JSON object: %v (%s)", err, text)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			out[k] = string(v)
			continue
		}
		out[k] = s
	}
	return out
}

// TestARoadmapAnswerNamesNoDatabasePath is spec.md §9 held as a property of the
// two documents this group answers: what a caller reads back is its own request
// and what the record did with it, and never a node, a key or a subtree of the
// record's layout.
//
// The record's layout is this installation's own and is not a client's to
// learn. What a caller does receive is the opaque value the record holds now
// beside a refusal — a version, or a release's key — because that is exactly
// what it has to send back to ask again, and it names a document without saying
// where the document lives. The node paths a writer's error names (P9b's
// messages carry them) are deliberately not passed through: where a read fails,
// the tool says the record was not read and nothing about where it is.
func TestARoadmapAnswerNamesNoDatabasePath(t *testing.T) {
	f := newRoadmapFixture(t)
	queued := decodeRoadmapAnswer(t, f.answer(t, "roadmap_move", roadmapCalls()[2].args))
	if want := []string{"asked", "kind", "request", "version"}; !slices.Equal(sortedKeys(queued), want) {
		t.Errorf("the answer to a queued request carries %v; want exactly %v", sortedKeys(queued), want)
	}
	if queued["kind"] != string(RequestMove) {
		t.Errorf("the answer names the kind %q; want %q", queued["kind"], RequestMove)
	}
	if queued["request"] == "" {
		t.Error("the answer names no request: the id is the caller's only handle on what it queued")
	}
	if !strings.Contains(queued["asked"], "plans/x/T1") {
		t.Errorf("the answer echoes %q; want the arguments the caller sent back: what a caller asked for is the caller's own text and not this package's description of it", queued["asked"])
	}

	f.queue.held[queued["request"]] = RoadmapOutcome{
		Request: queued["request"], Kind: RequestMove, Status: RoadmapRefused,
		Reason: "the task is not carried by any plan document", Current: coordinator.ReleaseID("v1", "r1"),
		Actor: testIdentity, At: testNow,
	}
	outcome := decodeRoadmapAnswer(t, f.answer(t, "roadmap_request", fmt.Sprintf(`{"id":%q}`, queued["request"])))
	if want := []string{"current", "kind", "reason", "request", "status", "version"}; !slices.Equal(sortedKeys(outcome), want) {
		t.Errorf("the outcome answer carries %v; want exactly %v", sortedKeys(outcome), want)
	}

	for _, text := range []string{mustJSON(queued), mustJSON(outcome)} {
		for _, path := range []string{coordinator.PathRoadmap(), coordinator.PathPlans(), coordinator.PathReleases(), coordinator.PathAssigns(), coordinator.PathEdges()} {
			if strings.Contains(text, path) {
				t.Errorf("the answer %s names the record's node %q: the layout stays this installation's own (spec.md §9)", text, path)
			}
		}
	}
	// The refused outcome carries the value the caller sends back and no
	// document key it could not have read itself: the record's own keys are
	// what a caller receives from a read of the record, not from this door.
	if _, carriesKey := outcome["plan_key"]; carriesKey {
		t.Errorf("the outcome answer carries a document key member: %v", outcome)
	}
}

// sortedKeys answers the members one answer document carries, sorted, which is
// how a shape is compared without depending on a map's order.
func sortedKeys(doc map[string]string) []string {
	out := make([]string, 0, len(doc))
	for k := range doc {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestATokenMintedBeforeTheRoadmapScopeStillServes is the HARD RULE's converge
// shape for this change: a token record the store holds today — the task pair
// and nothing else — is served after it, and gains no scope it did not hold.
//
// The change is additive and this is what that means for a record already at
// rest: the vocabulary grew, the record did not move, CheckScopes reads the
// same pair the same way, and the door serves the token exactly as it did. What
// is new is one refusal — the roadmap group's tools, which this token does not
// hold the scope for — and a refusal is not a migration. Nothing to converge
// means nothing for `fugaro update` to do: no release writes the switch that
// turns this door on (D934), so no installed version holds a door with an older
// vocabulary than this one.
func TestATokenMintedBeforeTheRoadmapScopeStillServes(t *testing.T) {
	ts := newResourceServer(t)
	const minted = "token-marker-minted-before-the-roadmap-scope"
	ts.seedAccessToken(t, minted, Token{
		Identity:  testIdentity,
		ClientID:  "client-that-minted-it",
		Limits:    Limits{Scopes: []string{ScopeTasksRead, ScopeTasksWrite}},
		ExpiresAt: testNow.Add(AccessTokenTTL),
	})

	rec := ts.rpc(t, minted, toolsListBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("a token holding the task pair was answered %d (%s); want 200: the vocabulary grew additively, so a record minted before the roadmap scope is served unchanged", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	record, err := ts.store.LookupByHash(context.Background(), TokenHash(minted))
	if err != nil {
		t.Fatalf("reading the token's record: %v", err)
	}
	if got := strings.Join(record.Token.Limits.Scopes, " "); got != ScopeTasksRead+" "+ScopeTasksWrite {
		t.Errorf("the record carries the scopes %q; want the task pair it was minted with: nothing rewrites a stored record, and a scope a build did not have it hold is a widening", got)
	}

	// The one thing that changed for it is a refusal: the roadmap group's tool
	// is not one this token holds the scope for, and the door refuses the call
	// before a handler runs.
	queue := newFakeQueue()
	if err := RegisterRoadmapTools(ts.tools, queue, testReader()); err != nil {
		t.Fatalf("RegisterRoadmapTools: %v", err)
	}
	rec = ts.rpc(t, minted, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"roadmap_request","arguments":{"id":"a-request-id"}}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the roadmap tool answered %d (%s) for a token holding only the task pair; want 403: the scope it maps to is one this token does not hold", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := refusalOf(t, rec).Error; got != "insufficient_scope" {
		t.Errorf("the refusal is %q; want insufficient_scope: a token that is alive and holds another scope is refused for the scope it needs and not for being unusable", got)
	}
	if len(queue.asked) != 0 {
		t.Errorf("the queue was asked %v through the door for a token that does not hold %q: the door refuses the call before a handler runs", queue.asked, ScopeRoadmapWrite)
	}
}

// TestALauncherTokenWithRoadmapWriteIsRefused is D2121 where it is enforced:
// a token record that says it holds roadmap:write and acts for somebody on the
// launcher list is refused the group's tools, whatever the installation grants
// and whatever the record says.
//
// The record is the one place a scope is written down, and this is the test
// that says the record is not the last word on it: the identity's own list is,
// which is what makes an operator removed from the installation lose the
// roadmap tools at the next call rather than at the next mint. The verification
// is the queue: the refusal happens before a handler runs, so nothing is
// queued. It is also one of the two tests the plan's mutation names — drop the
// operator check from Registry.Call and this and TestALauncherCannotObtainRoadmapWrite
// are the two that fail.
func TestALauncherTokenWithRoadmapWriteIsRefused(t *testing.T) {
	ts := newResourceServerGrantingTo(t, Members{Launchers: []string{testIdentity}}, ScopeRoadmapWrite)
	const launcherToken = "token-marker-holding-the-roadmap-scope"
	ts.seedAccessToken(t, launcherToken, Token{
		Identity:  testIdentity,
		ClientID:  "client-that-asked-for-it",
		Limits:    Limits{Scopes: []string{ScopeRoadmapWrite}},
		ExpiresAt: testNow.Add(AccessTokenTTL),
	})

	queue := newFakeQueue()
	if err := RegisterRoadmapTools(ts.tools, queue, testReader()); err != nil {
		t.Fatalf("RegisterRoadmapTools: %v", err)
	}
	// The task is one no assignment of the fixture's record names, so the call
	// is a valid one for the tool as well as for the door: what refuses it below
	// is the identity and nothing about the request.
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"roadmap_move","arguments":{"task":"plans/x/T9","to":"none","expect":"none"}}}`
	rec := ts.rpc(t, launcherToken, call)
	if rec.Code != http.StatusOK {
		t.Fatalf("the call answered %d (%s); want the tool's own refusal inside a successful result: the token holds the scope, and what refuses it is the identity's own rule", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	text, isError := wantToolText(t, wantResult(t, rec))
	if !isError {
		t.Fatalf("a launcher's token holding %q was served the roadmap tool with %q; want a refusal: the scope is one only an operator may hold (D2121)", ScopeRoadmapWrite, text)
	}
	if !strings.Contains(text, "operators") {
		t.Errorf("the refusal is %q; want it to name the rule — the scope is served to this installation's operators and to nobody else (D2121)", text)
	}
	if len(queue.asked) != 0 {
		t.Errorf("the queue was asked %v: the identity's rule is read before a handler runs, so a launcher's token cannot queue a write it may not make", queue.asked)
	}

	// The control, on the same installation and with the same record: the very
	// same call is served once the identity is an operator's, so the refusal is
	// about the list and not about the tool or the token.
	operator := newResourceServerGrantingTo(t, Members{Launchers: []string{testIdentity}, Operators: []string{testIdentity}}, ScopeRoadmapWrite)
	operator.seedAccessToken(t, launcherToken, Token{
		Identity:  testIdentity,
		ClientID:  "client-that-asked-for-it",
		Limits:    Limits{Scopes: []string{ScopeRoadmapWrite}},
		ExpiresAt: testNow.Add(AccessTokenTTL),
	})
	served := newFakeQueue()
	if err := RegisterRoadmapTools(operator.tools, served, testReader()); err != nil {
		t.Fatalf("RegisterRoadmapTools: %v", err)
	}
	rec = operator.rpc(t, launcherToken, call)
	text, isError = wantToolText(t, wantResult(t, rec))
	if isError {
		t.Fatalf("an operator's call was refused with %q; want it served: without this half the refusal above would pass for a group nothing can reach", text)
	}
	if len(served.submitted) != 1 {
		t.Errorf("an operator's move queued %d requests; want the one it asked for", len(served.submitted))
	}
}

// TestARunTokenOrAProjectTokenIsNotAFrontDoorToken is D923's vocabulary read at
// the door: the identity a token acts for has to be one person written as
// `user:<email>`, and a database custom token of the shape this installation
// mints for a run or for the project itself is not one — whatever scopes its
// record carries, and roadmap:write included.
//
// It is the direction that matters for a write scope: those two tokens are
// minted for processes and hold no member list of their own, so a door that
// read them as callers would serve the whole roadmap to anything that could
// present one. The refusal comes before any tool is chosen, so nothing is
// queued, and the two shapes are refused for the identity and never for the
// scope they carry.
func TestARunTokenOrAProjectTokenIsNotAFrontDoorToken(t *testing.T) {
	for _, tc := range []struct{ what, identity string }{
		{"a run's own token", "run:4f2a1c9d-marker-not-a-credential"},
		{"the project's own token", "token:project-marker-not-a-credential"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts := newResourceServerGrantingTo(t, Members{Launchers: []string{testIdentity}}, ScopeRoadmapWrite)
			const value = "token-marker-of-a-database-custom-token"
			ts.seedAccessToken(t, value, Token{
				Identity:  tc.identity,
				ClientID:  "a-database-client",
				Limits:    Limits{Scopes: []string{ScopeTasksRead, ScopeRoadmapWrite}},
				ExpiresAt: testNow.Add(AccessTokenTTL),
			})

			queue := newFakeQueue()
			if err := RegisterRoadmapTools(ts.tools, queue, testReader()); err != nil {
				t.Fatalf("RegisterRoadmapTools: %v", err)
			}
			rec := ts.rpc(t, value, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"roadmap_move","arguments":{"task":"plans/x/T1","to":"none","expect":"none"}}}`)
			wantBearerRefusal(t, rec, http.StatusForbidden, "insufficient_scope")
			body := strings.TrimSpace(rec.Body.String())
			if !strings.Contains(body, "holds no role") {
				t.Errorf("the refusal of %s is %q; want it to say the identity holds no role at this installation: the token is refused for the caller it acts for, and the scopes its record carries are not read as a permission", tc.what, body)
			}
			if strings.Contains(body, tc.identity) {
				t.Errorf("the refusal repeats the identity %q: a refusal is a line a log keeps", tc.identity)
			}
			if len(queue.asked) != 0 {
				t.Errorf("the queue was asked %v by %s", queue.asked, tc.what)
			}
		})
	}
}

// TestARoadmapToolOverASubmittingQueueThatFailsSaysSo is the queue's own two
// failures at the seven tools: a request the queue could not record is not a
// request the caller may believe was queued, and the answer says the
// installation's store failed rather than that the write was refused — a caller
// that read a refusal would change its request, and the one whose request was
// dropped would never ask again.
func TestARoadmapToolOverASubmittingQueueThatFailsSaysSo(t *testing.T) {
	f := newRoadmapFixture(t)
	f.queue.submitErr = errors.New("the queue could not be written")
	if _, err := f.call(t, "roadmap_unneed", `{"task":"plans/x/T1","needs":"plans/y/T2"}`); !errors.Is(err, ErrStoreUnreadable) {
		t.Errorf("a request over a queue that cannot be written = %v; want ErrStoreUnreadable", err)
	}

	// The record the tool read before the queue is not a write of anything:
	// what a failing queue leaves behind is nothing at all.
	if len(f.queue.submitted) != 0 {
		t.Errorf("a queue that failed recorded %v", f.queue.submitted)
	}
}

// TestRoadmapToolsNeedTheirThreeCollaborators: the group is registered over a
// registry, a queue and a reader, and a registration missing one of them is
// refused where it is written rather than served as a tool that panics on the
// first call — a nil queue here would be a write scope whose every call dies
// inside the handler.
func TestRoadmapToolsNeedTheirThreeCollaborators(t *testing.T) {
	if err := RegisterRoadmapTools(nil, newFakeQueue(), testReader()); err == nil {
		t.Error("RegisterRoadmapTools(nil, queue, reader) = nil; want a refusal: a tool registered on no registry is a name nothing serves")
	}
	if err := RegisterRoadmapTools(NewRegistry(), nil, testReader()); err == nil {
		t.Error("RegisterRoadmapTools(reg, nil, reader) = nil; want a refusal: a group registered over no queue would fail on every call")
	}
	if err := RegisterRoadmapTools(NewRegistry(), newFakeQueue(), nil); err == nil {
		t.Error("RegisterRoadmapTools(reg, queue, nil) = nil; want a refusal: a group registered over no reader could check nothing before it queued")
	}
	// And the group is registered once: a second call is a second registration
	// of seven names the registry already serves.
	f := newRoadmapFixture(t)
	if err := RegisterRoadmapTools(f.reg, f.queue, f.reader); !errors.Is(err, ErrToolNameTaken) {
		t.Errorf("a second registration = %v; want ErrToolNameTaken: one name is one tool", err)
	}
	// What the group queues is an interface a test satisfies with a value, and
	// the registry was handed exactly it: the seven tools answer the fake.
	if len(f.queue.asked) != 0 {
		t.Errorf("registering asked the queue %v", f.queue.asked)
	}
}

// TestARoadmapRequestIsQueuedWithItsKindAndNotTheToolsName: what a queue holds
// is what the write is, not what the tool was called. The kinds are the
// vocabulary P9e's apply job reads, and the two names that are one act with
// opposite arguments (need and unneed) are what a tool name could not express.
func TestARoadmapRequestIsQueuedWithItsKindAndNotTheToolsName(t *testing.T) {
	f := newRoadmapFixture(t)
	for _, c := range roadmapCalls() {
		if c.tool == "roadmap_request" {
			continue
		}
		f.answer(t, c.tool, c.args)
	}
	want := []RoadmapRequestKind{RequestPublishPlan, RequestUnpublishPlan, RequestMove, RequestNeed, RequestUnneed, RequestReleaseSet}
	got := make([]RoadmapRequestKind, 0, len(f.queue.submitted))
	for _, req := range f.queue.submitted {
		got = append(got, req.Kind)
		if !slices.Contains(want, req.Kind) {
			t.Errorf("a queued request carries the kind %q; want one of %v: the kind is what the write is and not the name the tool was called by", req.Kind, want)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("the tools queued %v; want %v", got, want)
	}
	// The move carries the two endpoints of the record's own vocabulary and the
	// release key, and the release write carries a document with its shape
	// version on it — the job applies these with P9b's writers and reads nothing
	// else.
	move := f.queue.submitted[2]
	if move.Task != "plans/x/T1" || move.To != coordinator.ReleaseID("v1", "r1") || move.Expect != coordinator.ReleaseID("v1", "r1") {
		t.Errorf("the queued move is %+v; want the task, the release the caller named and the release it read", move)
	}
	rel := f.queue.submitted[5]
	if rel.Release == nil || rel.Release.Schema != coordinator.RoadmapSchema || rel.Release.ID != "r9" {
		t.Errorf("the queued release write is %+v; want the document with the shape version the record reads", rel.Release)
	}
	pub := f.queue.submitted[0]
	if pub.Document == nil || pub.Document.Schema != coordinator.RoadmapSchema || pub.Source == nil || pub.Source.Repo != "acme/app" {
		t.Errorf("the queued publish is %+v; want the trimmed document and its provenance (D2111)", pub)
	}
}

// TestAnAppliedOutcomeCarriesNoTimeAndNoActor is the answer's own shape at the
// two members a caller must not be handed: the identity the request acted for
// and the moment the apply job wrote it. Both are the record's facts and the
// audit's, and the caller reads the status — a client shown the actor would be
// shown a name it did not need to see, and one shown the time would be reading
// this installation's clock as a document's own field.
func TestAnAppliedOutcomeCarriesNoTimeAndNoActor(t *testing.T) {
	f := newRoadmapFixture(t)
	doc := decodeRoadmapAnswer(t, f.answer(t, "roadmap_request", `{"id":"a-request-id-a-caller-read-back"}`))
	if doc["status"] != RoadmapApplied {
		t.Errorf("the outcome of an applied request is %v; want %q", doc["status"], RoadmapApplied)
	}
	for _, member := range []string{"at", "actor"} {
		if _, carried := doc[member]; carried {
			t.Errorf("the answer carries %q: %v — the caller reads the status, and the outcome's own actor and time are this installation's", member, doc)
		}
	}
}
