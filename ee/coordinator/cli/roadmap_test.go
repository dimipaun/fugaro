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

// TestReleaseSetChecksWhatItCanBeforeItCalls keeps two mistakes from costing a
// request the record then refuses a minute later: a state outside the two a
// release is written in, and a release written with no position — the one flag
// whose default is a real value, so "forgotten" and "first" would otherwise
// read the same.
func TestReleaseSetChecksWhatItCanBeforeItCalls(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	base := []string{"coordinator", "roadmap", "releases", "set",
		"--id", "2026.1", "--stream", "ga", "--name", "Winter", "--order", "0", "--expect", ""}
	_, _, err := execute(t, append(append([]string{}, base...), "--state", "done")...)
	if err == nil {
		t.Fatal("a release state outside the two was accepted")
	}
	if !strings.Contains(err.Error(), "open") || !strings.Contains(err.Error(), "finished") {
		t.Errorf("the refusal is %q; want it to name the two states", err)
	}
	forgotten := []string{"coordinator", "roadmap", "releases", "set",
		"--id", "2026.1", "--stream", "ga", "--name", "Winter", "--state", "open", "--expect", ""}
	if _, _, err := execute(t, forgotten...); err == nil {
		t.Error("a release was written with no position")
	}
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was reached %v for two requests it could have refused locally", calls)
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
	if doc.Version != mcpauth.RoadmapAnswerVersion || doc.Status != mcpauth.RoadmapRefused ||
		doc.Current != "v7" || doc.Request == "" || doc.Kind != string(mcpauth.RequestReleaseSet) {
		t.Errorf("--json printed %+v; want the refused outcome with the current version", doc)
	}
}

// TestAWriteRefusedBeforeItIsQueuedIsTheDoorsOwnSentence is the refusal a
// write meets when there is nothing to wait for: the door answered HTTP 200
// with a successful result and `isError` set, which is how MCP carries a
// tool's own refusal and how a stale `expect` and an argument the schema does
// not hold both arrive. Reading that text as the tool's data reported this
// build's guess at the shape ("named no request id") and dropped the door's
// sentence — the reason, and the version the record holds, which is the one
// value that tells a person what to send next.
func TestAWriteRefusedBeforeItIsQueuedIsTheDoorsOwnSentence(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	door.refuseWrite("the record has moved since this request was built: it holds the version v7 and the request expected v3; read the record again and send what it holds")

	out, _, err := execute(t, "coordinator", "roadmap", "releases", "set",
		"--id", "2026.1", "--stream", "ga", "--name", "Winter", "--state", "open",
		"--order", "3", "--expect", "v3", "--json")
	if err == nil {
		t.Fatal("a write the door refused before queueing exited 0")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d — nothing was queued, and the door's own sentence is what says so", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "holds the version v7") || !strings.Contains(err.Error(), "expected v3") {
		t.Errorf("the refusal is %q; want the door's sentence with the version the record holds", err)
	}
	if strings.Contains(err.Error(), "named no request id") || strings.Contains(err.Error(), "not an outcome document") {
		t.Errorf("the refusal is %q; want the door's sentence and not this build's guess at the shape it could not parse", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("--json printed %q for a call that was never queued: there is no request to report", out)
	}
}

// TestAFaultOfTheFrontDoorIsTheInstallations is the other half of the exit
// code the group's own sentence pins: a JSON-RPC error is the front door
// failing to answer a call this build made — -32603 a store it could not read
// or write, -32602 a call it would not read, -32601 a method it does not serve
// — and it is the installation's, exit 2. Only a tool's own refusal (`isError`)
// and a credential the door will not take (401, 403) are the caller's, and
// mapping this one to exit 1 told a person to look again at a call the record
// never saw.
func TestAFaultOfTheFrontDoorIsTheInstallations(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	door.failMCP(-32603, "the tool could not be answered: this installation's store could not be read or written, which is a fault of the installation and not of this call")

	_, _, err := execute(t, "coordinator", "roadmap", "request", "req-1")
	if err == nil {
		t.Fatal("a fault of the installation exited 0")
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Errorf("the fault exited %d; want %d — the record never saw the call", fcli.ExitCode(err), fcli.ExitRemoteError)
	}
	if !strings.Contains(err.Error(), "-32603") || !strings.Contains(err.Error(), "fault of the installation") {
		t.Errorf("the refusal is %q; want the code and the door's own sentence", err)
	}
}

// TestADocumentInAVersionThisBuildDoesNotReadIsRefused is the gate the version
// is for, at both ends of a write: the number is the front door's own
// (mcpauth.RoadmapAnswerVersion — one constant, so there is no second number
// to drift), and a document carrying another one is refused by its number
// rather than read field by field. Reading it anyway is the failure the number
// exists to prevent: a field that means something new in a shape this build
// does not know is a wrong answer with no error.
func TestADocumentInAVersionThisBuildDoesNotReadIsRefused(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	future := strconv.Itoa(mcpauth.RoadmapAnswerVersion + 1)
	door.answerInVersion(mcpauth.RoadmapAnswerVersion + 1)
	_, _, err := execute(t, needArgs()...)
	if err == nil {
		t.Fatal("a queued answer in a version this build does not read was read field by field")
	}
	if !strings.Contains(err.Error(), future) || !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("the refusal is %q; want it to name the version the door answered in and the command that brings the two into step", err)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal exited %d; want %d — the machine out of step is this one", fcli.ExitCode(err), fcli.ExitUserError)
	}

	// The other end: the queued answer is one this build reads, and the
	// outcome document it reads back is not.
	door.answerInVersion(mcpauth.RoadmapAnswerVersion)
	if _, _, err := execute(t, needArgs()...); err != nil {
		t.Fatalf("roadmap need: %v", err)
	}
	door.answerInVersion(mcpauth.RoadmapAnswerVersion + 1)
	_, _, err = execute(t, "coordinator", "roadmap", "request", "req-1")
	if err == nil {
		t.Fatal("an outcome document in a version this build does not read was read field by field")
	}
	if strings.Contains(err.Error(), "not an outcome document") {
		t.Errorf("the refusal is %q; want the version's own refusal and not this build's guess at the shape", err)
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
	} else if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("an unknown id exited %d; want %d — it is the caller's, and the door said so", fcli.ExitCode(err), fcli.ExitUserError)
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

// TestTwoCommandsAtOnceDoNotSpendTheSameRefreshToken is the credential lock's
// own test (D930). The refresh token is spent by the exchange that reads it, so
// two commands of one project started at once must not both present the token
// the keychain holds: the door reads the second presentation as the reuse D930
// answers by revoking the family, which signs the person out, sends them to a
// browser, and records a theft they did not commit.
//
// The two commands are held at the barrier until both have read the credential,
// which is the state the lock exists for. Without the lock both present the
// same token and the second is refused; with it the command that waited reads
// the successor the first one stored, and the keychain is left holding the
// token the door will accept next.
func TestTwoCommandsAtOnceDoNotSpendTheSameRefreshToken(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	withTwoCommandsAtOnce(t)

	answers, errs := executeConcurrently(t, 2, needArgs()...)
	for i := range answers {
		if errs[i] != nil {
			t.Errorf("command %d was refused: %v\n%s", i+1, errs[i], answers[i])
		}
	}
	presented := door.refreshPresentations()
	if len(presented) != 2 {
		t.Fatalf("the door was handed %d refresh tokens (%v); want one per command", len(presented), presented)
	}
	if presented[0] == presented[1] {
		t.Errorf("both commands presented the refresh token %q: the second use of it is the reuse the door revokes the family over", presented[0])
	}
	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := coordinatorReadCredential(context.Background(), store, item)
	if err != nil {
		t.Fatalf("reading the credential back: %v", err)
	}
	if cred.RefreshToken != door.refreshToken() {
		t.Errorf("the keychain holds %q; want %q, the successor both commands left it holding", cred.RefreshToken, door.refreshToken())
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
	// credentiallock.go is on the same path — it is what the credential is
	// swapped under — so it is held to the same property rather than left as
	// the one file of the group nobody checks. roadmap_publish.go, which reads
	// a checkout, is held to it by TestPublishNeverWritesTheDatabaseItself,
	// beside the code that file holds.
	for _, name := range []string{"roadmap.go", "roadmap_move.go", "login.go", "credentiallock.go"} {
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
	// endpoints and the catalogue's own tool names. The two files are read
	// together, because the group's writes are split across them — the
	// transport and the group in roadmap.go, the move in its own file — and a
	// name check against one of the two would pass while the other reached the
	// record another way.
	source := ""
	for _, name := range []string{"roadmap.go", "roadmap_move.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		source += string(data)
	}
	for _, want := range []string{"mcpauth.MCPPath", "roadmap_need", "roadmap_unneed", "roadmap_move", "roadmap_release_set", "roadmap_request"} {
		if !strings.Contains(source, want) {
			t.Errorf("the roadmap commands do not name %s, so this test is asserting nothing about the route it is meant to pin", want)
		}
	}
}
