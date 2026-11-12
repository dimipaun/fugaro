// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// This file is GV3.1: the `move` subcommand, against a fake front door. What is
// under test is the client half — the three arguments it sends, the refusal it
// reports when the door will not queue the move, and the outcome it reports
// when the record answers later. The record's own rules (which release is
// finished, what `prev` a move writes, whether an edge points backwards) are
// the writer's and the graph's, and they are tested where they live.

// moveTask is the qualified task id every test here moves. It is the shape the
// tool's schema takes — <plan>/<task>, where the plan is the artifact path.
const moveTask = "plans/2026-01-a.md/3"

// moveArgv is the command's argv for one move, so that a test spells only what
// it is about — the two releases — and not the command and the task.
func moveArgv(flags ...string) []string {
	return append([]string{"coordinator", "roadmap", "move", moveTask}, flags...)
}

// moveRecord is the fake door's reading of the record's assignments: which
// release each task is in, or none for a task it holds no assignment for.
//
// It exists so that the door's refusal of a stale `expect` is a check rather
// than a canned sentence. The real door reads the record here (P9d's move: the
// release the request names has to be one of the record's keys, and the release
// it expected has to be the one the assignment holds now) and refuses a request
// built on a record that has moved before it queues anything — so a client that
// sent an `expect` it never read is refused by this call rather than by an
// assertion about the string it happened to put on the wire.
type moveRecord struct {
	mu   sync.Mutex
	held map[string]string
}

func newMoveRecord() *moveRecord { return &moveRecord{held: map[string]string{}} }

// hold is the record as a caller reads it: the release a task is in.
func (r *moveRecord) hold(task, release string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[task] = release
}

// release is the release the record holds for one task, `none` for a task with
// no assignment (D2114).
func (r *moveRecord) release(task string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if release, ok := r.held[task]; ok {
		return release
	}
	return coordinatorMoveNone
}

// check is the door's read of one call before it queues it: the sentence the
// tool refuses it with, or "" to queue it. A move whose `expect` is not what
// the record holds is the one refusal this door makes, and it makes it with the
// record's own sentence — the value the record holds now is what a caller needs
// to send next.
func (r *moveRecord) check(tool string, arguments map[string]any) string {
	if tool != "roadmap_move" {
		return ""
	}
	task, _ := arguments["task"].(string)
	expect, _ := arguments["expect"].(string)
	if r.release(task) != expect {
		return fmt.Sprintf("the task's stored release is not the one this request expected: the record holds %q and the request expected %q; read the record again and move from what it holds", r.release(task), expect)
	}
	return ""
}

// apply is the apply job's half: the assignment the record holds after a request
// the record applied. It is where a move changes anything — a refused request
// never reaches it — so a test asserting that a refusal moved nothing is
// asserting this record's state and not a fixture's promise.
func (r *moveRecord) apply(tool string, arguments map[string]any) {
	if tool != "roadmap_move" {
		return
	}
	task, _ := arguments["task"].(string)
	to, _ := arguments["to"].(string)
	r.mu.Lock()
	defer r.mu.Unlock()
	if to == coordinatorMoveNone {
		delete(r.held, task)
		return
	}
	r.held[task] = to
}

// moveFixture is the state every test here starts from: a project, a fake front
// door, a credential a login left in the keychain, and the door reading a
// record the test sets up.
func moveFixture(t *testing.T) (*fakeFrontDoor, *moveRecord) {
	t.Helper()
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	rec := newMoveRecord()
	door.checkWrites(rec.check)
	door.applyWrites(rec.apply)
	return door, rec
}

// TestRoadmapMoveCallsTheToolAndReadsTheOutcome is the whole of a move: one
// tools/call whose arguments are exactly the tool's three fields, the request
// id printed before anything is waited for, and the outcome read back and
// printed. The `expect` on the wire is the release the caller read, not the one
// the command was told to move the task to: a client that sent the target where
// the schema asks for the expected value would compare a record it never read.
func TestRoadmapMoveCallsTheToolAndReadsTheOutcome(t *testing.T) {
	door, rec := moveFixture(t)
	rec.hold(moveTask, "2026.1")

	out, _, err := execute(t, moveArgv("--to", "2026.2", "--expect", "2026.1")...)
	if err != nil {
		t.Fatalf("roadmap move: %v", err)
	}
	calls := door.toolCalls()
	if len(calls) != 2 {
		t.Fatalf("the door served %d calls (%v); want the write and one outcome read", len(calls), calls)
	}
	if calls[0].Name != "roadmap_move" {
		t.Fatalf("the first call was %q; want roadmap_move", calls[0].Name)
	}
	want := map[string]string{"task": moveTask, "to": "2026.2", "expect": "2026.1"}
	if len(calls[0].Arguments) != len(want) {
		t.Errorf("the CLI sent %v; the tool's schema is closed, so anything extra is a field the door would refuse", calls[0].Arguments)
	}
	for k, v := range want {
		if got, _ := calls[0].Arguments[k].(string); got != v {
			t.Errorf("the call sent %s=%q; want %q", k, got, v)
		}
	}
	if calls[1].Name != "roadmap_request" {
		t.Errorf("the second call was %q; want the outcome read", calls[1].Name)
	}
	if !strings.Contains(out, "req-1") || !strings.Contains(out, "applied") {
		t.Errorf("the command printed %q; want the request id and the outcome", out)
	}
	if got := rec.release(moveTask); got != "2026.2" {
		t.Errorf("the record holds %q for the task; want the release the move asked for", got)
	}
	scope, ok := mcpauth.ToolScope("roadmap_move")
	if !ok || scope != mcpauth.ScopeRoadmapWrite {
		t.Errorf("the catalogue maps roadmap_move to %q (%v); want %q — a tool outside that scope is one a token need not hold", scope, ok, mcpauth.ScopeRoadmapWrite)
	}
}

// TestRoadmapMoveRefusesAStaleExpectAndMovesNothing is the red step's assertion.
// The caller read the task in 2026.1 and the record holds 2026.2: the door
// refuses the move before it queues anything, and **nothing moves** — no
// request is queued, the record still holds what it held, and the sentence
// names the release the caller has to send next.
//
// The `expect` on the wire is asserted here and not only the door's refusal,
// because that is what a blind write gets wrong: a command that dropped the
// flag, defaulted it to the empty string or sent the target instead would be
// asking the record to compare nothing, and a test that read only the refusal
// would pass against the door's sentence for whatever it did send.
func TestRoadmapMoveRefusesAStaleExpectAndMovesNothing(t *testing.T) {
	door, rec := moveFixture(t)
	rec.hold(moveTask, "2026.2")

	out, _, err := execute(t, moveArgv("--to", "2026.3", "--expect", "2026.1", "--json")...)
	if err == nil {
		t.Fatal("a move the door refused before queueing exited 0")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d — nothing was queued, and the door's own sentence is what says so", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "2026.2") {
		t.Errorf("the refusal is %q; want the release the record holds now, which is what the caller sends next", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the command printed %q for a call that was never queued: there is no request and no document", out)
	}
	calls := door.toolCalls()
	if len(calls) != 1 {
		t.Fatalf("the door served %v; want the one call it refused", calls)
	}
	if got, _ := calls[0].Arguments["expect"].(string); got != "2026.1" {
		t.Errorf("the call sent expect=%q; want the release the caller read (%q) — an empty or defaulted one is the blind write the compare-and-set exists to stop", got, "2026.1")
	}
	if got := rec.release(moveTask); got != "2026.2" {
		t.Errorf("the record holds %q for the task; want %q — a refused move changes nothing", got, "2026.2")
	}
}

// TestAMoveThatLosesARaceIsRefusedNotAppliedOver is the other half of the
// compare-and-set, at the other end of the queue: the door accepted the request
// — `expect` was right when it read the record — and somebody else's move
// landed before the apply job reached it. The record refuses it there and
// answers with the release it holds now, and the move is **not** applied over
// theirs. A client that retried the refused request blind, or read a refusal as
// anything but the caller's, is what this pins.
func TestAMoveThatLosesARaceIsRefusedNotAppliedOver(t *testing.T) {
	door, rec := moveFixture(t)
	rec.hold(moveTask, "2026.1")
	// The door reads the record and queues the request — `expect` is what the
	// assignment holds at that moment — and the other writer's move lands before
	// the apply job re-reads it, which is the race and the whole reason the
	// writer compares `expect` a second time.
	door.checkWrites(func(tool string, arguments map[string]any) string {
		if sentence := rec.check(tool, arguments); sentence != "" {
			return sentence
		}
		rec.hold(moveTask, "2026.2")
		return ""
	})
	door.serve(mcpauth.RoadmapRefused,
		"the task's stored release is not the one the request expected: the record holds \"2026.2\" and the request expected \"2026.1\"",
		"2026.2")

	out, _, err := execute(t, moveArgv("--to", "2026.3", "--expect", "2026.1", "--json")...)
	if err == nil {
		t.Fatal("a move the record refused exited 0")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d — the caller re-reads and asks again", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "2026.2") {
		t.Errorf("the refusal is %q; want the release the record holds now", err)
	}
	var doc coordinatorOutcomeDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json printed %q, which is not the outcome document: %v", out, err)
	}
	if doc.Status != mcpauth.RoadmapRefused || doc.Kind != string(mcpauth.RequestMove) || doc.Current != "2026.2" {
		t.Errorf("--json printed %+v; want the refused move with the release the record holds now", doc)
	}
	if got := rec.release(moveTask); got != "2026.2" {
		t.Errorf("the record holds %q for the task; want the other move's %q — a lost race is refused, not applied over", got, "2026.2")
	}
	// The write and the one read of its outcome: a client that asked again
	// after a refusal would be re-sending a request the record has answered.
	if calls := door.toolCalls(); len(calls) != 2 {
		t.Errorf("the door served %v; want the write and one outcome read", calls)
	}
}

// TestAMoveIntoAFinishedReleaseIsRefusedByTheRecord is D2113 at the CLI: a
// release the record's own state calls finished is refused where that state is
// known — by the writer, on the far side of the queue — so the command meets it
// as an outcome and reports it as one: the record's sentence, exit 1, and no
// document pretending a request is still on its way.
func TestAMoveIntoAFinishedReleaseIsRefusedByTheRecord(t *testing.T) {
	door, rec := moveFixture(t)
	rec.hold(moveTask, "2026.1")
	door.serve(mcpauth.RoadmapRefused,
		"the release \"2026.4\" is finished: move the task into a release that is still open", "")

	out, _, err := execute(t, moveArgv("--to", "2026.4", "--expect", "2026.1", "--json")...)
	if err == nil {
		t.Fatal("a move into a finished release exited 0")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("the refusal is %q; want the record's own sentence about the release's state", err)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
	var doc coordinatorOutcomeDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json printed %q, which is not the outcome document: %v", out, err)
	}
	if doc.Status != mcpauth.RoadmapRefused || doc.Reason == "" {
		t.Errorf("--json printed %+v; want the record's refusal with its reason", doc)
	}
	// The request was queued and answered: the command does not ask again, so
	// the record's own state is what the caller is left with.
	if calls := door.toolCalls(); len(calls) != 2 {
		t.Errorf("the door served %v; want the write and one outcome read", calls)
	}
}

// TestTheInverseMoveIsTheSameMoveBack is how a mistake is undone (GV3.2's undo
// is the same argv): the same task back to the release the caller read, with
// `expect` the release it was moved into. What the record writes for it — the
// `prev` it replaces, and the `by` and `at` that say who moved it and when — is
// the writer's (P9b's TestMoveRecordsPrevSoTheInverseRestores), and the point
// here is that the CLI composes none of it: it sends the three fields and the
// assignment is back where it was.
func TestTheInverseMoveIsTheSameMoveBack(t *testing.T) {
	door, rec := moveFixture(t)
	if got := rec.release(moveTask); got != coordinatorMoveNone {
		t.Fatalf("the fixture holds %q for the task; want a task with no assignment", got)
	}

	if _, _, err := execute(t, moveArgv("--to", "2026.2", "--expect", coordinatorMoveNone)...); err != nil {
		t.Fatalf("the move: %v", err)
	}
	if got := rec.release(moveTask); got != "2026.2" {
		t.Fatalf("the record holds %q for the task after the move; want 2026.2", got)
	}
	if _, _, err := execute(t, moveArgv("--to", coordinatorMoveNone, "--expect", "2026.2")...); err != nil {
		t.Fatalf("the inverse move: %v", err)
	}
	if got := rec.release(moveTask); got != coordinatorMoveNone {
		t.Errorf("the record holds %q for the task after the inverse; want it where it was", got)
	}

	calls := door.toolCalls()
	if len(calls) != 4 {
		t.Fatalf("the door served %v; want two writes and two outcome reads", calls)
	}
	for i, want := range []map[string]string{
		{"task": moveTask, "to": "2026.2", "expect": coordinatorMoveNone},
		{"task": moveTask, "to": coordinatorMoveNone, "expect": "2026.2"},
	} {
		call := calls[2*i]
		if call.Name != "roadmap_move" {
			t.Fatalf("call %d was %q; want roadmap_move", i, call.Name)
		}
		if len(call.Arguments) != 3 {
			t.Errorf("the CLI sent %v; want the tool's three fields and nothing that stands in for the record's own `prev`, `by` or `at`", call.Arguments)
		}
		for k, v := range want {
			if got, _ := call.Arguments[k].(string); got != v {
				t.Errorf("call %d sent %s=%q; want %q", i, k, got, v)
			}
		}
	}
}

// TestAMoveThatMakesAnEdgeBackwardsIsReported is the move the graph will draw
// backwards: the task is moved into a release its dependency is not in, and the
// record applies it. Nothing in this command refuses that, and nothing in it
// could report it either — the edges are not read here, and the warning is the
// graph's (V2.6.2, GV2.3): a second copy of that rule in the CLI would be a
// refusal the record never made. What is reported is the record's own answer,
// and the assertion that goes with it is that the call carries the tool's three
// fields and no fourth one carrying a judgement of this command's.
func TestAMoveThatMakesAnEdgeBackwardsIsReported(t *testing.T) {
	door, rec := moveFixture(t)
	// The task is in 2026.3, which its dependency is not in: the move below
	// puts it in an earlier release than the work it needs, which is the order
	// the graph flags.
	rec.hold(moveTask, "2026.3")

	out, _, err := execute(t, moveArgv("--to", "2026.1", "--expect", "2026.3", "--json")...)
	if err != nil {
		t.Fatalf("the record refused a move that only the graph has an opinion about: %v", err)
	}
	var doc coordinatorOutcomeDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json printed %q, which is not the outcome document: %v", out, err)
	}
	if doc.Status != mcpauth.RoadmapApplied || doc.Kind != string(mcpauth.RequestMove) {
		t.Errorf("--json printed %+v; want the move the record applied", doc)
	}
	calls := door.toolCalls()
	if len(calls) == 0 {
		t.Fatal("the door was never called")
	}
	if len(calls[0].Arguments) != 3 {
		t.Errorf("the call sent %v; want the tool's three fields — a flag this command invented to judge the move would be a rule the record does not have", calls[0].Arguments)
	}
	if got := rec.release(moveTask); got != "2026.1" {
		t.Errorf("the record holds %q for the task; want the release the move asked for", got)
	}
}

// TestRoadmapMoveRefusesWithoutATokenAndNamesLogin is the first refusal, as the
// group's other writes have it: a person who has not signed in is told that and
// the one command that fixes it, before anything is read and before the door is
// called on their behalf.
func TestRoadmapMoveRefusesWithoutATokenAndNamesLogin(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, moveArgv("--to", "2026.2", "--expect", coordinatorMoveNone)...)
	if err == nil {
		t.Fatal("a move succeeded with no credential in the keychain")
	}
	if !strings.Contains(err.Error(), "fugaro coordinator login") {
		t.Errorf("the refusal is %q; want it to name `fugaro coordinator login`", err)
	}
	if !strings.Contains(err.Error(), item.Name()) {
		t.Errorf("the refusal is %q; want it to name the item %q a person with several projects can look for", err, item.Name())
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was reached %v; want no call without a credential", calls)
	}
}

// TestRoadmapMoveChecksWhatItCanBeforeItCalls keeps a move from reaching the
// door with nothing to compare: the expected release is the whole of the
// compare-and-set, and an omitted one cannot be defaulted — `none` is a reading
// a caller states (D2114), and the empty string is nobody's. Both refusals are
// local, so nothing is queued for the record to refuse a minute later.
func TestRoadmapMoveChecksWhatItCanBeforeItCalls(t *testing.T) {
	door, _ := moveFixture(t)

	_, _, err := execute(t, moveArgv("--to", "2026.2")...)
	if err == nil {
		t.Fatal("a move with no expected release was accepted")
	}
	if !strings.Contains(err.Error(), "--expect") || !strings.Contains(err.Error(), coordinatorMoveNone) {
		t.Errorf("the refusal is %q; want it to name the flag and the word an unassigned task is expected as", err)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}

	_, _, err = execute(t, moveArgv("--to", "", "--expect", coordinatorMoveNone)...)
	if err == nil {
		t.Fatal("a move to no release at all was accepted")
	}
	if !strings.Contains(err.Error(), "--to") || !strings.Contains(err.Error(), coordinatorMoveNone) {
		t.Errorf("the refusal is %q; want it to name the flag and the word that takes a task out of every release", err)
	}

	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was reached %v for two requests it could have refused locally", calls)
	}
}

// TestRoadmapMoveIsASubcommandOfTheGroupsOwnCommand is the registration the
// group carries: the move is a subcommand of `coordinator roadmap` and not a
// second command on `coordinator`, and its positional argument is the qualified
// task id — one argument, so a second word is refused rather than read as a
// release.
func TestRoadmapMoveIsASubcommandOfTheGroupsOwnCommand(t *testing.T) {
	group := newCoordinatorRoadmapCmd()
	var move *cobra.Command
	for _, c := range group.Commands() {
		if c.Name() == "move" {
			move = c
		}
	}
	if move == nil {
		t.Fatal("the roadmap group carries no move subcommand")
	}
	if err := move.Args(move, []string{}); err == nil {
		t.Error("a move with no task was accepted")
	}
	if err := move.Args(move, []string{moveTask, "2026.2"}); err == nil {
		t.Error("a move with a second positional argument was accepted: the release is a flag and not a word")
	}
	if err := move.Args(move, []string{moveTask}); err != nil {
		t.Errorf("a move naming one task was refused: %v", err)
	}
}
