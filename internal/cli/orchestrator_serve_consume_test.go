package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
)

// The mailbox half of a tick (mobile P5.1f), as THIS file's wiring serves it.
//
// What internal/orchestrator's own tests pin is the pass itself: the lease, the
// tag, the derived reply id, the two writes and nothing else, and every refusal
// the store can answer. What is pinned here is the tick around it — that a
// deployment wired with a consumer really runs one (the pass is reachable from
// the route a scheduler calls and not only from a Go test), that its counts ride
// the pass an operator reads, that the node's half is served whatever the
// mailbox half came to, and that a deployment with no consumer says so in one
// line instead of reporting an empty inbox.
//
// Everything runs on internal/gcpfake's Realtime Database fake through the real
// `rtdb.Client`, which is what makes the writes real: the node tests beside this
// file serve with a reader-only store, and a store that refuses every write is
// exactly what the mailbox half is made of.

const (
	// orchestratorConsumeUID is the phone's own `m~` uid: the mailbox this pass
	// holds the lease on, and the name the pairing key is filed under.
	orchestratorConsumeUID = "m~AAAAAAAAAAAAAAAAAAAAAA"
	// orchestratorConsumeKey is the pairing's MAC key. It is key material and
	// not a secret, and it is the whole of what holds the heartbeat and the
	// outbox.
	orchestratorConsumeKey = "not-a-secret-this-is-a-test-pairing-key"
	// orchestratorConsumeText is what the phone said, and no answer this file
	// reads may quote it: the consumer carries nothing, so its answer is the
	// same sentence for every message.
	orchestratorConsumeText = "hello from the phone"
)

// orchestratorConsumeAt is the instant the fixture's database says it is, which
// is what every instant the pass writes must be: the heartbeat's `at` and the
// reply's are the database's clock, never this container's (D1230).
var orchestratorConsumeAt = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

func orchestratorConsumeInboxPath(id string) string {
	return "mailbox/" + orchestratorConsumeUID + "/inbox/" + id
}

func orchestratorConsumeOutboxPath(id string) string {
	return "mailbox/" + orchestratorConsumeUID + "/outbox/" + id
}

const orchestratorConsumeHeartbeatPath = "mailbox/" + orchestratorConsumeUID + "/heartbeat"

// orchestratorConsumeFixture is a database fake with a real client over it. The
// client is the same one the service is built with in production and it is the
// store BOTH halves are served with — the node's reads and the mailbox's writes
// are one database, which is the deployment's own shape.
func orchestratorConsumeFixture(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return orchestratorConsumeAt })
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("opening the database fake: %v", err)
	}
	return f, db
}

// orchestratorConsumerOn is the consumer the tick is wired with: this
// installation's one paired phone, under the holder production uses.
func orchestratorConsumerOn(db orchestrator.ConsumerStore) *orchestratorConsumer {
	return &orchestratorConsumer{
		holder: orchestratorConsumerHolder,
		db:     db,
		mailboxes: []orchestrator.ConsumerMailbox{{
			Mailbox: orchestratorConsumeUID,
			Key:     keychain.NewKey([]byte(orchestratorConsumeKey)),
		}},
	}
}

// orchestratorConsumeSigned is one inbox child as the phone wrote it: the five
// fields a message has and the pairing's tag over them, in the inbox direction.
func orchestratorConsumeSigned(t *testing.T, id string) map[string]any {
	t.Helper()
	record := map[string]any{
		"id": id, "sender": orchestratorConsumeUID,
		"text": orchestratorConsumeText, "at": orchestratorConsumeAt.UnixMilli(),
	}
	mac, err := mailbox.MacOf(record, []byte(orchestratorConsumeKey), "inbox")
	if err != nil {
		t.Fatalf("signing %s: %v", id, err)
	}
	record["mac"] = mac
	return record
}

// orchestratorConsumeTick serves one tick through the route a scheduler calls —
// never through tick directly, because the route is part of what is pinned —
// and returns the recorder and what the tick logged.
func orchestratorConsumeTick(t *testing.T, db *rtdb.Client, consumer *orchestratorConsumer) (*httptest.ResponseRecorder, string, orchestratorPass) {
	t.Helper()
	var log bytes.Buffer
	srv, err := newResidentOrchestrator(orchestratorConfig{
		id: "aurora", db: db, launch: &fakeResidentLauncher{},
		consumer: consumer, log: runner.NewLogger(&log),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, orchestratorTickPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s), and both halves of it are this installation's: %s", rec.Code, rec.Body.String(), log.String())
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("the tick's answer is not the pass it read: %v (%s)", err, rec.Body.String())
	}
	return rec, log.String(), pass
}

// orchestratorConsumeNode reads one node back out of the fake: absent is nil.
func orchestratorConsumeNode(t *testing.T, f *gcpfake.RTDB, path string) map[string]any {
	t.Helper()
	value := f.Value(path)
	if value == nil {
		return nil
	}
	node, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s holds %#v, want an object", path, value)
	}
	return node
}

// TestOrchestratorTickConsumesTheMailboxItIsWiredWith is the whole of P5.1f as
// a deployment meets it: one tick, one paired phone with one signed message in
// its inbox, and the two writes the rules grant the consumer — the heartbeat
// that IS the lease, and one answer in the outbox. The two halves are read off
// ONE response, because they are one tick: the node's counts and the mailbox's
// ride the same pass.
func TestOrchestratorTickConsumesTheMailboxItIsWiredWith(t *testing.T) {
	f, db := orchestratorConsumeFixture(t)
	f.Set(orchestratorConsumeInboxPath("msg-1"), orchestratorConsumeSigned(t, "msg-1"))

	_, logged, pass := orchestratorConsumeTick(t, db, orchestratorConsumerOn(db))
	if pass.Mailbox == nil {
		t.Fatalf("the tick reported no mailbox half, and it was wired with a consumer: %+v", pass)
	}
	got := *pass.Mailbox
	want := orchestratorMailboxPass{Mailboxes: 1, Leases: 1, Read: 1, Verified: 1, Answered: 1}
	if got != want {
		t.Errorf("the mailbox pass read %+v, and one phone with one message is %+v: a message the pairing's tag accepted is answered once, and nothing else is written or read", got, want)
	}
	if !strings.Contains(logged, orchestratorPassLine) {
		t.Errorf("the pass logged %q, and one line per tick is %q", logged, orchestratorPassLine)
	}
	// The counts ride the same line as the node's, which is the half an operator
	// reads a tick by: a mailbox pass whose numbers were only in the body would
	// be invisible in Cloud Logging.
	for _, want := range []string{`"mailboxes":1`, `"mail_answered":1`} {
		if !strings.Contains(logged, want) {
			t.Errorf("the pass line does not carry %s: %s", want, logged)
		}
	}

	// The heartbeat is the lease this pass holds: signed in the heartbeat
	// direction, stamped with the DATABASE's clock, and held under the holder
	// production uses — not a per-instance boot id, which is the whole reason
	// the holder is a constant (orchestratorConsumerHolder).
	beat := orchestratorConsumeNode(t, f, orchestratorConsumeHeartbeatPath)
	if beat == nil {
		t.Fatalf("the tick took no lease: %s holds nothing", orchestratorConsumeHeartbeatPath)
	}
	if boot := beat["boot"]; boot != orchestratorConsumerHolder {
		t.Errorf("the heartbeat's holder is %#v, and this pass takes the lease under %q", boot, orchestratorConsumerHolder)
	}
	if fmt.Sprint(beat["at"]) != fmt.Sprint(orchestratorConsumeAt.UnixMilli()) {
		t.Errorf("the heartbeat is stamped %#v, and the database's clock is %d: the lease's instants are the database's and never this container's", beat["at"], orchestratorConsumeAt.UnixMilli())
	}
	if ok, err := mailbox.VerifyMAC(beat, []byte(orchestratorConsumeKey), "heartbeat"); err != nil || !ok {
		t.Errorf("the heartbeat does not verify with the pairing's key (ok=%v): %v", ok, err)
	}

	// The answer is one create-only reply in the outbox, under the id the
	// message's own id derives. What it SAYS is pinned where it is written
	// (internal/orchestrator's consume_test holds the sentence against the
	// constant): what matters here is that the phone's text is nowhere in it —
	// the consumer carries nothing, so no answer may read like a result.
	out := orchestratorConsumeNode(t, f, orchestratorConsumeOutboxPath(mailbox.ReplyID("msg-1")))
	if out == nil {
		t.Fatalf("the tick answered nothing: %s holds nothing", orchestratorConsumeOutboxPath(mailbox.ReplyID("msg-1")))
	}
	if out["reply_to"] != "msg-1" || out["sender"] != orchestratorConsumeUID {
		t.Errorf("the answer reads %#v, and it is this phone's reply to msg-1", out)
	}
	if text, _ := out["text"].(string); text == "" || strings.Contains(text, orchestratorConsumeText) {
		t.Errorf("the answer's text is %q, and the consumer carries nothing: it answers every accepted message with its own sentence and never with what the message said", text)
	}
	if ok, err := mailbox.VerifyMAC(out, []byte(orchestratorConsumeKey), "outbox"); err != nil || !ok {
		t.Errorf("the answer does not verify with the pairing's key (ok=%v): %v", ok, err)
	}
}

// TestOrchestratorTickSaysItHasNoConsumer is the deployment this release can
// actually make: the six names the service is given are none of them a pairing
// key, a web API key or anything a database read could be taken with, so the
// builder wires no consumer at all (orchestratorServiceConfig). The tick then
// says so IN ONE LINE rather than reporting the numbers an empty inbox reports,
// and it still serves the node's half: the absent consumer is a fact about the
// mailbox and not a broken service.
func TestOrchestratorTickSaysItHasNoConsumer(t *testing.T) {
	store := &fakeOrchestratorStore{docs: map[string]string{}, at: orchestratorConsumeAt}
	rec, logged := tickForTest(t, store, "aurora", &fakeResidentLauncher{})
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Mailbox != nil {
		t.Errorf("the pass carries a mailbox half (%+v) on a deployment with no consumer: those numbers would be a consumer's that no consumer reported", pass.Mailbox)
	}
	if !strings.Contains(logged, orchestratorNoConsumer) {
		t.Errorf("the tick logged %q, and a deployment with no consumer says so in one line: %q", logged, orchestratorNoConsumer)
	}
	if !strings.Contains(logged, orchestratorPassLine) {
		t.Errorf("the tick logged %q without its pass line: the node's half is served whatever the mailbox's came to", logged)
	}
}

// TestOrchestratorTickServesTheNodeWhenAMailboxIsRefused is the other half of
// the same promise: a mailbox the pass could not consume — here the store
// refuses the heartbeat, which is how a real deployment meets an IAM denial, a
// quota or a database that is down — is a warning line and a count, and the
// tick still reads the node and answers with it. One phone's failure must not
// cost the installation its orchestrator.
func TestOrchestratorTickServesTheNodeWhenAMailboxIsRefused(t *testing.T) {
	f, db := orchestratorConsumeFixture(t)
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal([]byte(residentStateDoc), &doc); err != nil {
		t.Fatal(err)
	}
	f.Set(statePath, doc)
	f.Deny(orchestratorConsumeHeartbeatPath)

	_, logged, pass := orchestratorConsumeTick(t, db, orchestratorConsumerOn(db))
	if pass.Rev != 7 || pass.Items != 1 {
		t.Errorf("the pass read %+v, and the node holds rev 7 with one item: the mailbox's failure stopped the node's half", pass)
	}
	if pass.Mailbox == nil || pass.Mailbox.Mailboxes != 1 || pass.Mailbox.Leases != 0 || pass.Mailbox.Lost != 1 || pass.Mailbox.Refusals != 1 {
		t.Errorf("the mailbox half read %+v, and a mailbox whose heartbeat was refused is one this pass holds no lease on, counted as lost and named in a line", pass.Mailbox)
	}
	if !strings.Contains(logged, "the tick could not consume a mailbox") {
		t.Errorf("the tick logged %q, and a mailbox it could not consume is a line a person has to see", logged)
	}
	if f.Value(orchestratorConsumeHeartbeatPath) != nil {
		t.Error("the refused heartbeat was written anyway")
	}
	if f.Value(orchestratorConsumeOutboxPath(mailbox.ReplyID("msg-1"))) != nil {
		t.Error("the pass answered a mailbox it holds no lease on: a reply is written under the lease and nowhere else")
	}
}

// TestOrchestratorServeRefusesAHalfBuiltConsumer is the configuration fence: no
// consumer at all is a deployment (the test above), and a consumer with no
// holder or no store is not — every one of its mailbox passes would fail on a
// nil the deployment could have told apart from an absent consumer.
func TestOrchestratorServeRefusesAHalfBuiltConsumer(t *testing.T) {
	store := &fakeOrchestratorStore{docs: map[string]string{}, at: orchestratorConsumeAt}
	for _, tc := range []struct {
		name     string
		consumer *orchestratorConsumer
	}{
		{"no store", &orchestratorConsumer{holder: orchestratorConsumerHolder}},
		{"no holder", &orchestratorConsumer{db: store}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := orchestratorConfig{id: "aurora", db: store, launch: &fakeResidentLauncher{},
				consumer: tc.consumer, log: runner.NewLogger(&bytes.Buffer{})}
			if _, err := newResidentOrchestrator(cfg); err == nil {
				t.Fatal("a half-built consumer was served: a mailbox pass behind it would fail on a nil every tick")
			}
		})
	}
}
