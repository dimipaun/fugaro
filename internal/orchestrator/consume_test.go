package orchestrator_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The cloud consumer's pass (mobile P5.1f, design section 4.2 and 4.9).
//
// What these tests are about, in one line each: the pass reads the inbox it
// holds the lease on and touches no other mailbox (the lease is the fence), it
// answers exactly the records whose tag and shape the gate accepted, it writes
// the reply once under an id derived from the message (create-only, so a second
// tick is a `present` and never a second answer), it writes the heartbeat it is
// bound to and nothing else, and it reads nothing at all when it cannot read
// the database's clock or hold the lease. Everything runs on
// internal/gcpfake's Realtime Database fake through the real `rtdb.Client`: no
// live cloud, no key and no secret.
//
// The two instants below are the store's clock, and the pass must use it and
// not this process's — the fixture's own Date header is set to something else
// on purpose (consumeStore.clock), so a value the pass stamped with its own
// clock would fail here.

const (
	// consumeMailboxUID is the phone's own `m~` uid — the mailbox the pairing
	// key is filed under, which is also what CONSUMER compares `$mb` against.
	consumeMailboxUID = "m~AAAAAAAAAAAAAAAAAAAAAA"
	// consumeKeyMaterial is the pairing's MAC key. It is not a secret and is
	// shaped like one only in the sense that it is key material: the tag this
	// pass writes is the whole of what holds the outbox and the heartbeat.
	consumeKeyMaterial = "not-a-secret-this-is-a-test-pairing-key"
	// consumeHolder is this consumer's name in the heartbeat, as a boot id.
	consumeHolder = "orch-1"
)

// consumeAt is the instant the fixture's database says it is: what the lease's
// `at` and every reply's `at` must be, and the clock the gate's future
// tolerance is measured against.
var consumeAt = time.Date(2026, 7, 4, 3, 2, 1, 0, time.UTC)

// consumeStore is the pass' store as a test sees it: the database fake behind
// the real client, a clock a test holds still or denies, and every path the
// pass read — because half of what the lease promises is what a pass that does
// not hold it does NOT read.
type consumeStore struct {
	*rtdb.Client
	// at is what the database says it is. A test moves it to take the next
	// tick, because the pass' instants are this value's and never the
	// process's.
	at    time.Time
	reads []string
}

func (s *consumeStore) ServerNow() (time.Time, bool) { return s.at, true }

// consumeStoreWithoutAClock answers no instant at all, which is what a store
// that has not heard from the database yet reports.
type consumeStoreWithoutAClock struct{ *consumeStore }

func (s *consumeStoreWithoutAClock) ServerNow() (time.Time, bool) { return time.Time{}, false }

// consumeWriter is the write half as these tests hand it to a pass: the same
// fake database behind the same client, writing under the mailbox the pass
// serves, which is what the claim of a real one names. These tests hold no
// consumer token and no web API key, so they cannot build what production
// builds (orchestrator.ConsumerWriter's own paragraph says why that credential
// is the point); what they build has the writer's one write and its one
// statement of the claim, and a test that gives it ANOTHER mailbox' name is a
// wiring the pass must refuse.
type consumeWriter struct {
	*consumeStore
	mailbox string
}

func (w consumeWriter) Mailbox() string { return w.mailbox }

func (s *consumeStore) Get(ctx context.Context, path string, out any) (bool, error) {
	s.reads = append(s.reads, path)
	return s.Client.Get(ctx, path, out)
}

func (s *consumeStore) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	s.reads = append(s.reads, path)
	return s.Client.GetETag(ctx, path, out)
}

// consumeFixture is a database fake with a client over it, whose Date header
// says something other than the store's clock: a pass that stamped a lease or a
// reply with the process's own clock, or with the header, would not match the
// instants these tests assert.
func consumeFixture(t *testing.T) (*gcpfake.RTDB, *consumeStore) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return consumeAt.Add(time.Hour) })
	return f, &consumeStore{Client: rtdbClient(t, f), at: consumeAt}
}

// consumeMailboxFixture is one phone as the pass is given it: the mailbox, the
// pairing's key, and the writer whose credential is minted for that mailbox.
func consumeMailboxFixture(st *consumeStore) orchestrator.ConsumerMailbox {
	return consumeMailboxOf(st, consumeMailboxUID)
}

// consumeMailboxOf is the same fixture under a mailbox the caller names, which
// is how the writer's own fence is tested: a writer minted for one mailbox
// handed to a pass serving another.
func consumeMailboxOf(st *consumeStore, mailbox string) orchestrator.ConsumerMailbox {
	return orchestrator.ConsumerMailbox{
		Mailbox: mailbox,
		Key:     keychain.NewKey([]byte(consumeKeyMaterial)),
		Writer:  consumeWriter{consumeStore: st, mailbox: mailbox},
	}
}

func consumeOptions(mailboxes ...orchestrator.ConsumerMailbox) orchestrator.ConsumerOptions {
	return orchestrator.ConsumerOptions{Holder: consumeHolder, Mailboxes: mailboxes}
}

// consumeSigned is one inbox child as the phone writes one: the five fields a
// message has, whatever else it carries, and the tag over exactly those fields
// in the inbox direction. A record signed with another key is a record that
// does not verify — which is what several cases below are about.
func consumeSigned(t *testing.T, id string, extra map[string]any, at int64, key string) map[string]any {
	t.Helper()
	record := map[string]any{"id": id, "sender": consumeMailboxUID, "text": "hello from the phone", "at": at}
	for k, v := range extra {
		record[k] = v
	}
	mac, err := mailbox.MacOf(record, []byte(key), "inbox")
	if err != nil {
		t.Fatalf("signing %s: %v", id, err)
	}
	record["mac"] = mac
	return record
}

// consumeHeartbeat is a heartbeat someone else wrote: `{at, boot, mac}` in the
// heartbeat direction, which is the value the lease reads.
func consumeHeartbeat(t *testing.T, boot string, at int64) map[string]any {
	t.Helper()
	record := map[string]any{"at": at, "boot": boot}
	mac, err := mailbox.MacOf(record, []byte(consumeKeyMaterial), "heartbeat")
	if err != nil {
		t.Fatalf("signing the heartbeat: %v", err)
	}
	record["mac"] = mac
	return record
}

func consumeInboxPath(id string) string {
	return "mailbox/" + consumeMailboxUID + "/inbox/" + id
}

// consumeOutboxID is the id an answer is written under, from the module rather
// than spelled again here: a copy of the derivation would go on passing after
// the derivation moved, and the derived id IS the create-only rule's whole
// mechanism (mailbox.ReplyID).
func consumeOutboxID(messageID string) string { return mailbox.ReplyID(messageID) }

func consumeOutboxPath(id string) string {
	return "mailbox/" + consumeMailboxUID + "/outbox/" + id
}

const consumeHeartbeatPath = "mailbox/" + consumeMailboxUID + "/heartbeat"

// consumeNode reads one node back out of the fake, as the JSON the database
// holds: absent is nil.
func consumeNode(t *testing.T, f *gcpfake.RTDB, path string) map[string]any {
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

// consumeNumber reads one whole number out of a node the fake handed back.
func consumeNumber(t *testing.T, node map[string]any, field string) int64 {
	t.Helper()
	number, ok := node[field].(json.Number)
	if !ok {
		t.Fatalf("%s = %#v, want a number", field, node[field])
	}
	got, err := number.Int64()
	if err != nil {
		t.Fatalf("%s = %v, want a whole number", field, number)
	}
	return got
}

// consumeReadNothingAt asks the store whether the pass read anything under a
// node: a pass without the lease must not, and neither must a pass with no
// clock.
func consumeReadNothingAt(t *testing.T, st *consumeStore, node string) {
	t.Helper()
	for _, path := range st.reads {
		if strings.Contains(path, node) {
			t.Errorf("the pass read %s: it holds no lease, so it reads and writes nothing", path)
		}
	}
}

// TestConsumerPassReadsVerifiesLeasesAndAnswers is the pass' whole surface in
// one case: one phone, one message with the pairing's key on it, and the two
// writes the rules grant the consumer — the heartbeat that IS the lease, and
// one create-only answer in the outbox. It pins what each of them carries, the
// instants included: the store's clock, not this process's, and the reply under
// the id the message's own id derives.
func TestConsumerPassReadsVerifiesLeasesAndAnswers(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Mailboxes != 1 || got.Leases != 1 || got.Lost != 0 {
		t.Errorf("mailboxes/leases/lost = %d/%d/%d, want 1/1/0", got.Mailboxes, got.Leases, got.Lost)
	}
	if got.Read != 1 || got.Verified != 1 || got.Refused != 0 {
		t.Errorf("read/verified/refused = %d/%d/%d, want 1/1/0", got.Read, got.Verified, got.Refused)
	}
	if got.Replied != 1 || got.Present != 0 {
		t.Errorf("replied/present = %d/%d, want 1/0", got.Replied, got.Present)
	}
	if got.Carried != 0 {
		t.Errorf("carried = %d, want 0: this pass acts on nothing until the acting half lands", got.Carried)
	}
	if len(got.Refusals) != 0 {
		t.Errorf("refusals = %v, want none", got.Refusals)
	}

	// The lease: the heartbeat the phone reads for presence, named by this
	// consumer's holder and stamped at the store's instant.
	hb := consumeNode(t, f, consumeHeartbeatPath)
	if hb == nil {
		t.Fatal("the pass answered a message without writing the heartbeat: the heartbeat write IS the lease")
	}
	if hb["boot"] != consumeHolder {
		t.Errorf("heartbeat boot = %#v, want %q", hb["boot"], consumeHolder)
	}
	if at := consumeNumber(t, hb, "at"); at != consumeAt.UnixMilli() {
		t.Errorf("heartbeat at = %d, want %d: the lease's instant is the database's clock", at, consumeAt.UnixMilli())
	}
	if ok, err := mailbox.VerifyMAC(mailbox.Record(hb), []byte(consumeKeyMaterial), "heartbeat"); err != nil || !ok {
		t.Errorf("the heartbeat's tag does not verify in the heartbeat direction (ok=%v, err=%v)", ok, err)
	}

	// The answer: one node, under the id the message's own id derives, and
	// signed in the outbox direction — a tag made in another direction is a
	// value no phone verifies and, create-only, one no later write could
	// correct.
	reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1")))
	if reply == nil {
		t.Fatalf("no answer at %s", consumeOutboxPath(consumeOutboxID("msg-1")))
	}
	if reply["id"] != consumeOutboxID("msg-1") {
		t.Errorf("reply id = %#v, want %q", reply["id"], consumeOutboxID("msg-1"))
	}
	if reply["sender"] != consumeMailboxUID {
		t.Errorf("reply sender = %#v, want the mailbox %q", reply["sender"], consumeMailboxUID)
	}
	if reply["kind"] != "answer" || reply["reply_to"] != "msg-1" {
		t.Errorf("the answer carries kind/reply_to %#v/%#v, want \"answer\"/\"msg-1\"", reply["kind"], reply["reply_to"])
	}
	if reply["text"] != orchestrator.ConsumerNotCarried {
		t.Errorf("reply text = %#v, want the pass' own sentence %q", reply["text"], orchestrator.ConsumerNotCarried)
	}
	if strings.Contains(reply["text"].(string), "hello from the phone") {
		t.Error("the answer repeats the message it answers: a message's text reaches no value this consumer writes")
	}
	if at := consumeNumber(t, reply, "at"); at != consumeAt.UnixMilli() {
		t.Errorf("the answer's at = %d, want %d", at, consumeAt.UnixMilli())
	}
	if ok, err := mailbox.VerifyMAC(mailbox.Record(reply), []byte(consumeKeyMaterial), "outbox"); err != nil || !ok {
		t.Errorf("the answer's tag does not verify in the outbox direction (ok=%v, err=%v)", ok, err)
	}
}

// TestConsumerPassTakesTheLeaseItFindsNobodyHolding covers the two instants of
// a lease a consumer takes on one tick and keeps on the next: the first pass
// takes an absent heartbeat over, the second renews its own — the common case
// for a service whose identity is stable, and the one a Take would refuse,
// because the module never re-takes a fresh claim, its holder's included.
//
// The second pass is also where create-only shows: the message is still in the
// inbox, so the pass owes the same answer again, finds it there and writes
// nothing — not the outbox value, which keeps the first pass' instant.
func TestConsumerPassTakesTheLeaseItFindsNobodyHolding(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))

	first, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	if first.Leases != 1 || first.Replied != 1 {
		t.Fatalf("the first pass leased %d and replied %d, want 1 and 1", first.Leases, first.Replied)
	}
	before := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1")))

	// The next tick: the same holder, a fresh heartbeat it wrote itself.
	later := consumeAt.Add(time.Minute)
	st.at = later
	second, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if second.Leases != 1 || second.Lost != 0 {
		t.Errorf("the second pass leased %d and lost %d, want 1 and 0: its own fresh heartbeat is renewed", second.Leases, second.Lost)
	}
	if second.Present != 1 || second.Replied != 0 {
		t.Errorf("the second pass answered present/replied %d/%d, want 1/0", second.Present, second.Replied)
	}

	hb := consumeNode(t, f, consumeHeartbeatPath)
	if at := consumeNumber(t, hb, "at"); at != later.UnixMilli() {
		t.Errorf("heartbeat at = %d after the renewal, want %d: the phone reads this value for presence", at, later.UnixMilli())
	}
	after := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1")))
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the answer changed on the second pass:\n before %#v\n after  %#v", before, after)
	}
}

// TestConsumerPassWritesNothingUnderAnotherConsumersFreshClaim is section 4.9's
// second fence: a phone's heartbeat holding someone else's fresh claim is not
// this consumer's mailbox this tick, so it reads nothing — not the inbox, not
// the outbox — and leaves the other holder's claim exactly where it found it.
// The two consumers here are the resident one and a laptop woken beside it.
func TestConsumerPassWritesNothingUnderAnotherConsumersFreshClaim(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeHeartbeatPath, consumeHeartbeat(t, "laptop-2", consumeAt.UnixMilli()))
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Lost != 1 || got.Leases != 0 {
		t.Errorf("lost/leases = %d/%d, want 1/0: another consumer holds this mailbox", got.Lost, got.Leases)
	}
	if got.Read != 0 || got.Verified != 0 || got.Replied != 0 || got.Present != 0 {
		t.Errorf("the pass read %d, verified %d, replied %d and found %d present, want zeros", got.Read, got.Verified, got.Replied, got.Present)
	}
	consumeReadNothingAt(t, st, "/inbox")
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply != nil {
		t.Errorf("the pass answered a message it holds no lease on: %#v", reply)
	}
	hb := consumeNode(t, f, consumeHeartbeatPath)
	if hb["boot"] != "laptop-2" {
		t.Errorf("the heartbeat now names %#v, want the other consumer's claim left alone", hb["boot"])
	}
	if at := consumeNumber(t, hb, "at"); at != consumeAt.UnixMilli() {
		t.Errorf("the other consumer's claim was rewritten at %d", at)
	}
}

// TestConsumerPassAnswersOnlyWhatTheGateAccepted: a record the tag or the shape
// refuses is counted and never answered — a reply to a record nothing accepted
// would be a write an attacker asked for by writing an inbox node. The lease is
// still held and renewed either way, because the refusal is about the message
// and not about the mailbox.
func TestConsumerPassAnswersOnlyWhatTheGateAccepted(t *testing.T) {
	for _, tc := range []struct {
		what  string
		child any
	}{
		{"a record signed with another key", consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), "another-pairing-key")},
		{"a record with no tag at all", map[string]any{"id": "msg-1", "sender": consumeMailboxUID, "text": "hello", "at": consumeAt.UnixMilli()}},
		{"a child that is not an object", "hello from a phone this build cannot read"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ctx := context.Background()
			f, st := consumeFixture(t)
			f.Set(consumeInboxPath("msg-1"), tc.child)

			got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
			if err != nil {
				t.Fatalf("Consume: %v", err)
			}
			if got.Leases != 1 || got.Read != 1 {
				t.Errorf("leases/read = %d/%d, want 1/1: the refusal is about the record, not the mailbox", got.Leases, got.Read)
			}
			if got.Verified != 0 || got.Refused != 1 {
				t.Errorf("verified/refused = %d/%d, want 0/1", got.Verified, got.Refused)
			}
			if got.Replied != 0 || got.Present != 0 {
				t.Errorf("replied/present = %d/%d, want 0/0", got.Replied, got.Present)
			}
			if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply != nil {
				t.Errorf("a refused record was answered: %#v", reply)
			}
			if hb := consumeNode(t, f, consumeHeartbeatPath); hb == nil {
				t.Error("the pass took no lease over a refused record: the lease is the mailbox's, not the message's")
			}
		})
	}
}

// TestConsumerPassAnswersNoPromptAndNoUnknownKind: a prompt travels host to
// phone — a consumer that answered one out of its own inbox would be answering
// its own question — so a verified prompt is counted as refused and written
// nowhere. The same is true of any kind this pass does not carry.
func TestConsumerPassAnswersNoPromptAndNoUnknownKind(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("p-1"), consumeSigned(t, "p-1", map[string]any{"kind": "prompt"}, consumeAt.UnixMilli(), consumeKeyMaterial))
	f.Set(consumeInboxPath("m-1"), consumeSigned(t, "m-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Read != 2 || got.Verified != 2 {
		t.Errorf("read/verified = %d/%d, want 2/2: a prompt is a record this build reads and verifies", got.Read, got.Verified)
	}
	if got.Refused != 1 || got.Replied != 1 {
		t.Errorf("refused/replied = %d/%d, want 1/1", got.Refused, got.Replied)
	}
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("p-1"))); reply != nil {
		t.Errorf("the pass answered a prompt: %#v", reply)
	}
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("m-1"))); reply == nil {
		t.Error("the pass left an ordinary message unanswered because a prompt was in the same inbox")
	}
}

// TestConsumerPassAnswersAStopRequest: the phone's stop request (mobile plan
// task P5.1g, the owner's answer Q10(c)) is a kind a phone sends, so this pass
// carries it — it is answered, and the answer says what actually happened:
// nothing was stopped from here, because there is nothing here to stop it with.
//
// The two halves both matter. A record this pass refused outright would never
// be answered, and the phone's own at-least-once rule keeps an unanswered
// record owing (M2), so the human's stop request would sit in the app as a
// pending utterance with nothing to say about it; and an answer that claimed
// something had been stopped would be the lie Q10's answer (c) exists to avoid
// — the phone cannot write the enforcement side, and this pass holds no grant
// for it either.
func TestConsumerPassAnswersAStopRequest(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("s-1"), consumeSigned(t, "s-1", map[string]any{
		"kind": "stop", "text": "stop everything",
	}, consumeAt.UnixMilli(), consumeKeyMaterial))

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Read != 1 || got.Verified != 1 || got.Refused != 0 || got.Replied != 1 {
		t.Errorf("read/verified/refused/replied = %d/%d/%d/%d, want 1/1/0/1: a stop request is a record this pass reads, verifies and answers",
			got.Read, got.Verified, got.Refused, got.Replied)
	}
	reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("s-1")))
	if reply == nil {
		t.Fatal("the pass left a stop request unanswered: an unanswered record is one the phone keeps owing")
	}
	if reply["kind"] != "answer" {
		t.Errorf("the reply's kind = %v, want answer: the outbox answer is the kind a phone reads, and a stop request raises no prompt of this pass' own", reply["kind"])
	}
	text, _ := reply["text"].(string)
	for _, want := range []string{"nothing was stopped", "no phone-writable kill switch", "revoke this device's pairing"} {
		if !strings.Contains(text, want) {
			t.Errorf("the reply to a stop request does not carry %q: %q", want, text)
		}
	}
}

// TestConsumerPassReportsARefusedWriteAndOwesTheAnswer: the two rules that
// fence the outbox — CONSUMER's write grant and the create-only clause — are
// enforced where the write happens, so a rules denial is a refusal this pass
// reports and never a reply it counts. Nothing was written, so the answer is
// owed again on the next tick.
func TestConsumerPassReportsARefusedWriteAndOwesTheAnswer(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))
	f.Deny("mailbox/" + consumeMailboxUID + "/outbox")

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Leases != 1 || got.Verified != 1 {
		t.Errorf("leases/verified = %d/%d, want 1/1", got.Leases, got.Verified)
	}
	if got.Replied != 0 || got.Present != 0 {
		t.Errorf("replied/present = %d/%d, want 0/0: a denied write is not an answer", got.Replied, got.Present)
	}
	if len(got.Refusals) != 1 {
		t.Errorf("refusals = %v, want the one refused write recorded", got.Refusals)
	}
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply != nil {
		t.Errorf("a denied outbox write landed anyway: %#v", reply)
	}
}

// TestConsumerPassTakesNoLeaseWhenTheHeartbeatWriteIsDenied: the heartbeat
// write IS the lease, so a store that refuses it leaves this consumer holding
// nothing — and a consumer holding nothing reads nothing. It is the fail-closed
// half of M1, and the reason a wiring mistake in the consumer's grants shows up
// as silence rather than as a second consumer acting on the phone's messages.
func TestConsumerPassTakesNoLeaseWhenTheHeartbeatWriteIsDenied(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))
	f.Deny("mailbox/" + consumeMailboxUID + "/heartbeat")

	got, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Leases != 0 || got.Lost != 1 {
		t.Errorf("leases/lost = %d/%d, want 0/1", got.Leases, got.Lost)
	}
	if len(got.Refusals) != 1 {
		t.Errorf("refusals = %v, want the one refused heartbeat recorded", got.Refusals)
	}
	consumeReadNothingAt(t, st, "/inbox")
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply != nil {
		t.Errorf("a consumer with no lease answered a message: %#v", reply)
	}
}

// TestConsumerPassKeepsOnePhonesFailureFromStoppingAnother: a mailbox this pass
// cannot even build a path from is that mailbox's refusal, collected in the
// outcome — the next phone is a different key and a different inbox, and a
// deployment that lost one pairing's key must not lose the others.
func TestConsumerPassKeepsOnePhonesFailureFromStoppingAnother(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))
	broken := consumeMailboxOf(st, "a mailbox/with a separator")

	got, err := orchestrator.Consume(ctx, st, consumeOptions(broken, consumeMailboxFixture(st)))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Mailboxes != 2 || got.Leases != 1 || got.Lost != 1 {
		t.Errorf("mailboxes/leases/lost = %d/%d/%d, want 2/1/1: every mailbox is accounted for", got.Mailboxes, got.Leases, got.Lost)
	}
	if len(got.Refusals) != 1 {
		t.Errorf("refusals = %v, want the one broken mailbox recorded", got.Refusals)
	}
	if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply == nil {
		t.Error("the second phone's message went unanswered because the first mailbox was broken")
	}
}

// TestConsumerPassRefusesAWriterItCannotWriteWith is the fence on the write
// credential, which is the one thing about it a caller can be held to in code
// (ConsumerWriter's own paragraph): the `mc` claim names ONE mailbox, so a pass
// handed this phone's mailbox with a writer minted for another one would read
// this phone's inbox and have every write refused by the rules — CONSUMER is
// `auth.token.mc == $mb` — and a pass with no writer at all has nothing to
// write the two nodes with. Both are the same verdict: refused before the lease
// and before the inbox, recorded, counted lost, and nothing attempted.
func TestConsumerPassRefusesAWriterItCannotWriteWith(t *testing.T) {
	const other = "m~BBBBBBBBBBBBBBBBBBBBBB"
	for _, tc := range []struct {
		name     string
		writerOf func(st *consumeStore) orchestrator.ConsumerWriter
	}{
		{"a writer minted for another mailbox", func(st *consumeStore) orchestrator.ConsumerWriter {
			return consumeWriter{consumeStore: st, mailbox: other}
		}},
		{"no writer at all", func(*consumeStore) orchestrator.ConsumerWriter { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, st := consumeFixture(t)
			f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))
			m := consumeMailboxFixture(st)
			m.Writer = tc.writerOf(st)

			got, err := orchestrator.Consume(ctx, st, consumeOptions(m))
			if err != nil {
				t.Fatalf("Consume: %v", err)
			}
			if got.Mailboxes != 1 || got.Leases != 0 || got.Lost != 1 || len(got.Refusals) != 1 {
				t.Errorf("mailboxes/leases/lost/refusals = %d/%d/%d/%d, want 1/0/1/1: a mailbox this pass cannot write with is a mailbox it could not consume", got.Mailboxes, got.Leases, got.Lost, len(got.Refusals))
			}
			if len(st.reads) != 0 {
				t.Errorf("the pass read %v, and a mailbox it cannot write with is read in no way at all: the inbox it read would be this phone's and the answer another credential's", st.reads)
			}
			if hb := consumeNode(t, f, consumeHeartbeatPath); hb != nil {
				t.Errorf("the pass took a lease it cannot write with: %#v", hb)
			}
			if reply := consumeNode(t, f, consumeOutboxPath(consumeOutboxID("msg-1"))); reply != nil {
				t.Errorf("the pass answered with a writer that is not this mailbox' own: %#v", reply)
			}
		})
	}
}

// TestConsumerPassReadsNothingWithoutTheDatabasesClock: every instant this pass
// judges — the lease's, the reply's, the gate's tolerance — is one
// authoritative clock's (D117), so a store that cannot answer one is the pass'
// one error and nothing behind it is attempted.
func TestConsumerPassReadsNothingWithoutTheDatabasesClock(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))

	got, err := orchestrator.Consume(ctx, &consumeStoreWithoutAClock{st}, consumeOptions(consumeMailboxFixture(st)))
	if err == nil {
		t.Fatal("Consume judged messages against a clock its store never gave it")
	}
	if got.Mailboxes != 0 || got.Leases != 0 || got.Lost != 0 || got.Read != 0 || got.Replied != 0 || len(got.Refusals) != 0 {
		t.Errorf("outcome = %#v, want the zero one: nothing was attempted", got)
	}
	if len(st.reads) != 0 {
		t.Errorf("the pass read %v with no clock to judge the records against", st.reads)
	}
	if hb := consumeNode(t, f, consumeHeartbeatPath); hb != nil {
		t.Errorf("the pass wrote a heartbeat with no clock: %#v", hb)
	}
}

// TestConsumerPassWritesOnlyTheOutboxAndTheHeartbeat is U6's surface, counted:
// over one pass the only nodes that changed are the two CONSUMER is granted,
// and every other node in the tree is exactly what it was. It is what
// makes the claim "the outbox and the heartbeat and nothing else" a test rather
// than a sentence.
func TestConsumerPassWritesOnlyTheOutboxAndTheHeartbeat(t *testing.T) {
	ctx := context.Background()
	f, st := consumeFixture(t)
	f.Set(consumeInboxPath("msg-1"), consumeSigned(t, "msg-1", nil, consumeAt.UnixMilli(), consumeKeyMaterial))
	f.Set("mailbox/"+consumeMailboxUID+"/outbox/other", map[string]any{"id": "other", "text": "an answer from an earlier pass"})
	f.Set("mailbox/"+consumeMailboxUID+"/sent/1", map[string]any{"id": "1"})
	inbox := consumeNode(t, f, consumeInboxPath("msg-1"))
	other := consumeNode(t, f, consumeOutboxPath("other"))
	sent := consumeNode(t, f, "mailbox/"+consumeMailboxUID+"/sent/1")

	if _, err := orchestrator.Consume(ctx, st, consumeOptions(consumeMailboxFixture(st))); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if now := consumeNode(t, f, consumeInboxPath("msg-1")); !reflect.DeepEqual(inbox, now) {
		t.Errorf("the pass changed the phone's inbox:\n before %#v\n after  %#v", inbox, now)
	}
	if now := consumeNode(t, f, consumeOutboxPath("other")); !reflect.DeepEqual(other, now) {
		t.Errorf("the pass changed an answer it does not owe:\n before %#v\n after  %#v", other, now)
	}
	if now := consumeNode(t, f, "mailbox/"+consumeMailboxUID+"/sent/1"); !reflect.DeepEqual(sent, now) {
		t.Errorf("the pass wrote outside the outbox and the heartbeat:\n before %#v\n after  %#v", sent, now)
	}
}
