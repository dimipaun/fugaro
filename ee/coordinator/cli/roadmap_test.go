// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// This file is P9f's roadmap half. Every test starts where a person who has
// signed in is — a credential in the fake keychain — and asserts what the
// command put on the wire and what it made of the answer.
//
// The one property that is not a behaviour is TestRoadmapNeverWritesTheDatabaseItself:
// the whole point of routing these commands through the front door is that
// they hold no way to write the record, and that is a property of the files
// rather than of any one run of them.

// needArgs are the three arguments a need is called with, in one place so that
// the tests that only need a write to happen do not each spell them.
func needArgs() []string {
	return []string{"coordinator", "roadmap", "need",
		"--task", "plans/2026-01-a.md/3", "--needs", "plans/2026-01-b.md/1",
		"--reason", "the schema has to land first"}
}

// TestRoadmapNeedCallsTheToolAndReadsTheOutcome is the whole of a write: one
// tools/call whose arguments are exactly the tool's schema, the request id
// printed before anything is waited for, and the outcome read back and
// printed.
func TestRoadmapNeedCallsTheToolAndReadsTheOutcome(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	out, _, err := execute(t, needArgs()...)
	if err != nil {
		t.Fatalf("roadmap need: %v", err)
	}
	calls := door.toolCalls()
	if len(calls) != 2 {
		t.Fatalf("the door served %d calls (%v); want the write and one outcome read", len(calls), calls)
	}
	if calls[0].Name != "roadmap_need" {
		t.Errorf("the first call was %q; want roadmap_need", calls[0].Name)
	}
	want := map[string]string{
		"task":   "plans/2026-01-a.md/3",
		"needs":  "plans/2026-01-b.md/1",
		"reason": "the schema has to land first",
	}
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
	if !strings.Contains(out, "req-1") {
		t.Errorf("the command printed %q; want the request id", out)
	}
	if !strings.Contains(out, "applied") {
		t.Errorf("the command printed %q; want the outcome", out)
	}
}

// TestRoadmapUnneedAndReleaseSetAreCataloguedTools keeps the three names the
// CLI calls in step with the door's catalogue: a name the catalogue does not
// hold is a call the door answers -32601, and a scope the catalogue does not
// map to roadmap:write would be a tool served to a token that may not.
func TestRoadmapUnneedAndReleaseSetAreCataloguedTools(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	for _, tc := range []struct {
		name string
		args []string
		want map[string]string
	}{
		{"roadmap_unneed", []string{"coordinator", "roadmap", "unneed", "--task", "a/1", "--needs", "b/1"},
			map[string]string{"task": "a/1", "needs": "b/1"}},
		{"roadmap_release_set", []string{"coordinator", "roadmap", "releases", "set",
			"--id", "2026.1", "--stream", "ga", "--name", "Winter", "--state", "open", "--order", "3", "--expect", "v2"},
			map[string]string{"id": "2026.1", "stream": "ga", "name": "Winter", "state": "open", "expect": "v2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			door.mu.Lock()
			before := len(door.calls)
			door.mu.Unlock()
			if _, _, err := execute(t, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			calls := door.toolCalls()
			call := calls[before]
			if call.Name != tc.name {
				t.Fatalf("the call was %q; want %q", call.Name, tc.name)
			}
			for k, v := range tc.want {
				if got, _ := call.Arguments[k].(string); got != v {
					t.Errorf("%s sent %s=%q; want %q", tc.name, k, got, v)
				}
			}
			if tc.name == "roadmap_release_set" {
				if got, _ := call.Arguments["order"].(float64); got != 3 {
					t.Errorf("the release's order was sent as %v; want the number 3", call.Arguments["order"])
				}
			}
			scope, ok := mcpauth.ToolScope(tc.name)
			if !ok {
				t.Fatalf("the catalogue holds no row for %s", tc.name)
			}
			if scope != mcpauth.ScopeRoadmapWrite {
				t.Errorf("%s maps to %q; want %q", tc.name, scope, mcpauth.ScopeRoadmapWrite)
			}
		})
	}
}

// TestRoadmapRefusesWithoutATokenAndNamesLogin is the first refusal, and it is
// checked before anything else happens: a person who has not signed in is told
// that and the one command that fixes it, and no Terraform run and no call to
// the door is made on their behalf.
func TestRoadmapRefusesWithoutATokenAndNamesLogin(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)

	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		needArgs(),
		{"coordinator", "roadmap", "unneed", "--task", "a/1", "--needs", "b/1"},
		{"coordinator", "roadmap", "releases", "set", "--id", "1", "--stream", "ga", "--name", "One", "--state", "open", "--order", "0", "--expect", ""},
		{"coordinator", "roadmap", "request", "req-1"},
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v succeeded with no credential in the keychain", args)
		}
		if !strings.Contains(err.Error(), "fugaro coordinator login") {
			t.Errorf("%v refused with %q; want it to name `fugaro coordinator login`", args, err)
		}
		if !strings.Contains(err.Error(), item.Name()) {
			t.Errorf("%v refused with %q; want it to name the item %q a person with several projects can look for", args, err, item.Name())
		}
		if fcli.ExitCode(err) != fcli.ExitUserError {
			t.Errorf("%v exited %d; want %d", args, fcli.ExitCode(err), fcli.ExitUserError)
		}
	}
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was reached %v; want no call without a credential", calls)
	}
}

// TestRoadmapRefusesWhenTheFrontDoorIsOff is the second refusal: D934's switch
// is off, the installation's outputs publish no URL, and the command says so
// rather than calling something it guessed.
func TestRoadmapRefusesWhenTheFrontDoorIsOff(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	prev := coordinatorFrontDoorURL
	t.Cleanup(func() { coordinatorFrontDoorURL = prev })
	coordinatorFrontDoorURL = func(context.Context, *localcfg.Config) (string, error) { return "", errNoFrontDoor() }

	_, _, err := execute(t, "coordinator", "roadmap", "request", "req-1")
	if err == nil {
		t.Fatal("the command reached a front door the installation does not publish")
	}
	if !strings.Contains(err.Error(), "front door is off") {
		t.Errorf("the refusal is %q; want it to say the front door is off", err)
	}
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was reached %v with the switch off", calls)
	}
}

// TestReleasesSetRefusesAChangedIDOrStream is the record refusing a write
// built on a version it no longer holds: the reason and the value the record
// holds now are the two things the caller needs to ask again, and both are
// surfaced — in the sentence a person reads and in the --json document the
// console reads.
func TestReleasesSetRefusesAChangedIDOrStream(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	door.serve(mcpauth.RoadmapRefused,
		`the release "2026.1" in stream "ga" is at version v7, and this request was built on v3`,
		"v7")

	out, _, err := execute(t, "coordinator", "roadmap", "releases", "set",
		"--id", "2026.1", "--stream", "ga", "--name", "Winter", "--state", "open",
		"--order", "3", "--expect", "v3", "--json")
	if err == nil {
		t.Fatal("a refused write exited 0")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("a refused write exited %d; want %d — the person has to look again", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "v7") {
		t.Errorf("the refusal is %q; want the value the record holds now", err)
	}
	var doc coordinatorOutcomeDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json printed %q, which is not the outcome document: %v", out, err)
	}
	if doc.Version != coordinatorOutcomeVersion || doc.Status != mcpauth.RoadmapRefused ||
		doc.Current != "v7" || doc.Request == "" || doc.Kind != string(mcpauth.RequestReleaseSet) {
		t.Errorf("--json printed %+v; want the refused outcome with the current version", doc)
	}
}

// TestARoadmapWriteThatIsStillQueuedPrintsPending is the other end of the
// wait: the apply job has not reached the request, the command says so, and
// the id it printed is how the person reads it later.
func TestARoadmapWriteThatIsStillQueuedPrintsPending(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	door.serve(mcpauth.RoadmapPending, "", "")
	prevWait, prevPoll := coordinatorRoadmapWait, coordinatorRoadmapPoll
	t.Cleanup(func() { coordinatorRoadmapWait, coordinatorRoadmapPoll = prevWait, prevPoll })
	coordinatorRoadmapWait, coordinatorRoadmapPoll = 40*time.Millisecond, time.Millisecond

	out, _, err := execute(t, "coordinator", "roadmap", "unneed", "--task", "a/1", "--needs", "b/1")
	if err != nil {
		t.Fatalf("a pending request is not a failure, and was answered %v", err)
	}
	if !strings.Contains(out, "req-1") || !strings.Contains(out, "pending") {
		t.Errorf("the command printed %q; want the request id and that it is still pending", out)
	}
	if !strings.Contains(out, "roadmap request req-1") {
		t.Errorf("the command printed %q; want it to name how the request is read later", out)
	}
	if calls := door.toolCalls(); len(calls) < 3 {
		t.Errorf("the door served %d calls; want the command to have waited and asked again", len(calls))
	}
}

// TestRoadmapRequestReadsAnOutcomeLater is the second half of the pair: an id
// a write printed is read back by a command of its own, which reads once and
// does not wait — a pending request is a state, not a failure.
func TestRoadmapRequestReadsAnOutcomeLater(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	if _, _, err := execute(t, needArgs()...); err != nil {
		t.Fatalf("roadmap need: %v", err)
	}
	door.mu.Lock()
	before := len(door.calls)
	door.mu.Unlock()
	out, _, err := execute(t, "coordinator", "roadmap", "request", "req-1")
	if err != nil {
		t.Fatalf("roadmap request: %v", err)
	}
	if !strings.Contains(out, "req-1") || !strings.Contains(out, "applied") {
		t.Errorf("the command printed %q; want the outcome", out)
	}
	if _, _, err := execute(t, "coordinator", "roadmap", "request", "req-9"); err == nil {
		t.Error("an id the door holds nothing for was answered as if it existed")
	} else if !strings.Contains(err.Error(), "queued for you") {
		t.Errorf("the refusal is %q; want the door's own sentence for an unknown id", err)
	}
	door.mu.Lock()
	after := len(door.calls)
	door.mu.Unlock()
	if after-before != 2 {
		t.Errorf("the two reads served %d calls; want one each and no waiting", after-before)
	}
}

// TestTheRotatedRefreshTokenIsStoredBack is D930 in the CLI: the token the
// door spends is spent, and a command that kept it would find its family
// revoked the next time it ran. The credential in the keychain has to be the
// successor the door minted, and the second command is what proves it.
func TestTheRotatedRefreshTokenIsStoredBack(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, needArgs()...); err != nil {
		t.Fatalf("the first write: %v", err)
	}
	cred, err := coordinatorReadCredential(context.Background(), store, item)
	if err != nil {
		t.Fatalf("reading the credential back: %v", err)
	}
	if cred.RefreshToken == "refresh-1" {
		t.Fatal("the keychain still holds the refresh token the door spent")
	}
	if cred.RefreshToken != door.refreshToken() {
		t.Fatalf("the keychain holds %q; want the successor the door minted", cred.RefreshToken)
	}
	// The second write is what a kept token would fail: the door refuses a
	// refresh token that was already spent.
	if _, _, err := execute(t, needArgs()...); err != nil {
		t.Fatalf("the second write: %v", err)
	}
}

// TestACredentialTheDoorWillNotRenewSaysToSignInAgain is the refusal a person
// meets when the family was revoked, or when the installation's front door was
// recreated somewhere else: the door's own sentence, and the one command that
// fixes it.
func TestACredentialTheDoorWillNotRenewSaysToSignInAgain(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinatorStoreCredential(context.Background(), store, item, frontDoorCredential{
		Issuer: door.URL(), ClientID: door.clientID(), RefreshToken: "refresh-0",
		Scopes: []string{mcpauth.ScopeRoadmapWrite},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, needArgs()...)
	if err == nil {
		t.Fatal("a spent refresh token was renewed")
	}
	if !strings.Contains(err.Error(), "fugaro coordinator login") {
		t.Errorf("the refusal is %q; want it to name the command that signs in again", err)
	}
	if !strings.Contains(err.Error(), "already spent") {
		t.Errorf("the refusal is %q; want the door's own sentence", err)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit code %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
}

// TestATokenTheDoorNoLongerAcceptsSaysToSignInAgain is the same refusal at the
// other end of the call: the access token was minted and then withdrawn, and
// the door's 401 is answered with the command that mints a new one.
func TestATokenTheDoorNoLongerAcceptsSaysToSignInAgain(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	door.mu.Lock()
	door.refuseStatus = http.StatusUnauthorized
	door.mu.Unlock()

	_, _, err := execute(t, "coordinator", "roadmap", "request", "req-1")
	if err == nil {
		t.Fatal("a withdrawn access token was accepted")
	}
	if !strings.Contains(err.Error(), "fugaro coordinator login") {
		t.Errorf("the refusal is %q; want it to name the command that signs in again", err)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit code %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
}

// TestRoadmapNeverWritesTheDatabaseItself is the property the whole route
// exists for (D2118, D922/H3): the commands that change the record hold no way
// to change it. The front door queues, and a job this process never reaches
// applies as the coordinator's own account.
//
// **This is the red step's assertion.** A command that reached P9b's writer
// with the person's credential — the shape of the mistake the design is
// guarding against — has to import the record's package or a database client
// to do it, and the first of those two checks is what refuses it. The second
// catches the same mistake made through a helper this package already has.
func TestRoadmapNeverWritesTheDatabaseItself(t *testing.T) {
	forbidden := []string{
		"github.com/dimipaun/fugaro/ee/coordinator",
		"github.com/dimipaun/fugaro/internal/rtdb",
		"github.com/dimipaun/fugaro/internal/runstore",
		"github.com/dimipaun/fugaro/internal/budget",
		"github.com/dimipaun/fugaro/internal/blobx",
	}
	// The names a write of the record would be made through, whichever way the
	// import arrived: P9b's writers are reached from the record's package and
	// from the store the coordinator's other commands open.
	writers := []string{"openCoordinatorStore", "CoordinatorDB", "coordinator.Store", "rtdb.", "runstore."}
	for _, name := range []string{"roadmap.go", "login.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %s; the roadmap commands reach the record through the front door's tools and hold no writer of their own", name, bad)
				}
			}
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, bad := range writers {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s mentions %s; a roadmap command that can write the record has been routed around the front door", name, bad)
			}
		}
	}
	// And the positive half: the calls it does make are the door's own
	// endpoints and the catalogue's own tool names.
	source, err := os.ReadFile("roadmap.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mcpauth.MCPPath", "roadmap_need", "roadmap_unneed", "roadmap_release_set", "roadmap_request"} {
		if !strings.Contains(string(source), want) {
			t.Errorf("roadmap.go does not name %s, so this test is asserting nothing about the route it is meant to pin", want)
		}
	}
}
