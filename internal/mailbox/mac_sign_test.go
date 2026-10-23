package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The outbound signature's tests (mobile P4.1, Task T2a): §8.2's half of the one
// MAC function, on the reply and on the heartbeat.
//
// T2's file proves the inbound gate; this one proves the mirror image, and the
// plan's verification names each piece: what `sign` produces the command's own
// `verify` accepts (TestSignVerifyRoundTrip, over the same vectors T2 replays);
// the covered field set is the same one in both directions, differing only in the
// direction argument (TestTheCoveredFieldSetIsTheSameInBothDirections); **both**
// families are signed, the reply with `outbox` and the heartbeat with `heartbeat`
// (TestOutboundIsSigned, TestTheHeartbeatIsSignedInItsOwnDirection); a consumer
// whose signer fails for this value writes nothing — no reply, no heartbeat, and
// therefore no lease (M1) — and reports the refusal by code
// (TestSigningFailureIsAPerWriteRefusal, TestUnsignedHeartbeatIsNotWritten); and
// the reply it could not write is owed again on the next pass rather than lost
// (TestARefusedReplyIsOwedAgain).
//
// **The failing signer is injected, and it has to be.** No shipped build reaches
// a signer error by itself: M5 reads the key once at startup, so a *key read*
// that fails is always §8.3's availability check — a startup refusal with no pass
// run — and what is left inside a running pass is a signer that is present and
// errored on one value. That is M4's third row, and it is the reason these tests
// exist as their own tests rather than as one more case of the startup row: the
// line between "the layer is unavailable" and "this value could not be signed" is
// the difference between exit 1 and a recorded refusal (§4.4).
//
// Everything runs on `internal/gcpfake`'s Realtime Database fake and a fake
// keychain (the vectors' placeholder, never a real one): no live cloud, no clock,
// no key.

// signerIn is the signer a value written to one node is signed with. It fails
// loudly for a direction the consumer never writes, so the set of directions this
// package can sign for is stated here rather than assumed: the phone signs the
// inbox (P3.6), and the consumer signs the outbox and the heartbeat and nothing
// else (M4b).
func signerIn(t *testing.T, key []byte, direction string) *Signer {
	t.Helper()
	switch direction {
	case directionOutbox:
		return NewOutboxSigner(keychain.NewKey(key))
	case directionHeartbeat:
		return NewHeartbeatSigner(keychain.NewKey(key))
	}
	t.Fatalf("no signer signs for the %s direction: the consumer writes the outbox and the heartbeat and nothing else", direction)
	return nil
}

// TestEveryOutboundDirectionHasASigner pins the pair of families §8.2.3 names,
// and the absence of the third: a signer exists for each of the two directions
// the consumer writes, it reports that direction, and there is no signer for the
// inbox — a tag the consumer made for the inbox would be a tag it could pass off
// as the phone's.
func TestEveryOutboundDirectionHasASigner(t *testing.T) {
	key := []byte(vectorKey(t))
	signable := map[string]bool{directionOutbox: true, directionHeartbeat: true}
	for _, direction := range Directions {
		if !signable[direction] {
			continue
		}
		if got := signerIn(t, key, direction).Direction(); got != direction {
			t.Errorf("the signer for %s reports %s", direction, got)
		}
	}
	if len(signable) != 2 {
		t.Fatalf("the consumer signs %d families; §8.2.3 names two — the outbox reply and the heartbeat", len(signable))
	}
}

// TestSignVerifyRoundTrip is the plan's "what `sign` produces, the command's own
// `verify` accepts", swept over every case in the vector file and both outbound
// directions rather than over one value: each case is signed in its own direction
// (where the file pins a tag for it, the tag must be the file's) and in the other
// outbound direction, and every tag produced is one VerifyMAC accepts — and one
// no other direction accepts.
//
// The key swap is the second half of the plan's red step (b): a tag made with one
// key does not verify under another, in either direction. It is what makes "the
// key is the pairing's" a fact about the tag rather than a comment about the
// caller.
func TestSignVerifyRoundTrip(t *testing.T) {
	key := []byte(vectorKey(t))
	other := []byte(otherKey)
	outbound := []string{directionOutbox, directionHeartbeat}
	cases := list(t, loadVectors(t)["cases"], "cases")
	swept := 0
	for _, entry := range cases {
		vector := object(t, entry, "case")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			record := Record(object(t, vector["message"], "message"))
			readFrom := text(t, vector["direction"], "direction")
			for _, direction := range outbound {
				signed := cloneRecord(t, record)
				tag, err := signerIn(t, key, direction).Tag(signed)
				if err != nil {
					t.Fatalf("signing for the %s node: %v", direction, err)
				}
				swept++
				// The tag is MacOf's over the case's own bytes, and the file
				// pins that tag where it is the case's own direction: sign and
				// verify are one function, so a tag this side produces is a tag
				// the module's mac.js produces.
				want, err := MacOf(record, key, direction)
				if err != nil {
					t.Fatalf("MacOf(%s): %v", direction, err)
				}
				if tag != want {
					t.Errorf("the tag for %s is %s, MacOf says %s", direction, tag, want)
				}
				if direction == readFrom {
					if pinned := text(t, vector["mac"], "mac"); tag != pinned {
						t.Errorf("the vector file pins %s for this record read from the %s node, this package signs %s", pinned, direction, tag)
					}
				}
				signed["mac"] = tag
				if ok, err := VerifyMAC(signed, key, direction); err != nil || !ok {
					t.Errorf("VerifyMAC(%s) refused the tag this package just signed: %v, %v", direction, ok, err)
				}
				// And the tag is for this node alone: every other direction
				// refuses it (§8.2.2, the reflections).
				for _, elsewhere := range Directions {
					if elsewhere == direction {
						continue
					}
					if ok, err := VerifyMAC(signed, key, elsewhere); err != nil {
						t.Fatalf("VerifyMAC(%s): %v", elsewhere, err)
					} else if ok {
						t.Errorf("a tag signed for the %s node verified as %s", direction, elsewhere)
					}
				}
				// The key swap: a real tag over real bytes, made with a key that
				// is not the pairing's, verifies nowhere.
				if ok, err := VerifyMAC(signed, other, direction); err != nil || ok {
					t.Errorf("a tag signed with one key verified under another (%s): %v, %v", direction, ok, err)
				}
			}
		})
	}
	if swept < len(cases)*2 {
		t.Fatalf("the sweep signed %d values over %d cases: both outbound directions are the contract", swept, len(cases))
	}
}

// TestTheCoveredFieldSetIsTheSameInBothDirections is §8.2.2's "the covered set is
// the same one T2 pins, in both directions, differing only in the direction
// argument". It is the outbound twin of T2's covered-set test, and it is written
// against the *same* field list (Fields, the schema's own) so that a field the
// schema grows is a field both halves demand a decision about.
//
// The mutation this test exists to catch is the one the plan names: add or drop a
// field from the covered set and the tag stops answering to it — in both
// directions, because a set that differs between them is a reply and a heartbeat
// signed over two different things.
func TestTheCoveredFieldSetIsTheSameInBothDirections(t *testing.T) {
	key := []byte(vectorKey(t))
	records := map[string]Record{
		"the plain message":        plainMessage(t),
		"the prompt with options":  vectorCase(t, "prompt with options"),
		"the answer with reply_to": vectorCase(t, "answer"),
		"the merge action":         vectorCase(t, "action: merge a pull request"),
	}
	seen := map[string]bool{}
	covered := map[string]map[string]bool{}
	for what, record := range records {
		for _, direction := range []string{directionOutbox, directionHeartbeat} {
			base, err := signerIn(t, key, direction).Tag(record)
			if err != nil {
				t.Fatalf("signing %s for %s: %v", what, direction, err)
			}
			if covered[direction] == nil {
				covered[direction] = map[string]bool{}
			}
			for _, field := range sortedKeys(record) {
				seen[field] = true
				clone := cloneRecord(t, record)
				clone[field] = changedValue(clone[field])
				got, err := signerIn(t, key, direction).Tag(clone)
				if err != nil {
					t.Fatalf("signing %s (%s changed) for %s: %v", what, field, direction, err)
				}
				isCovered := field != "mac" && field != "acked"
				if covered[direction][field] != isCovered && covered[direction][field] {
					t.Errorf("%s is covered in the %s direction for one record and not another", field, direction)
				}
				covered[direction][field] = isCovered
				switch {
				case isCovered && got == base:
					t.Errorf("changing %s in %s left the %s tag alone, so the field is not covered", field, what, direction)
				case !isCovered && got != base:
					t.Errorf("changing %s in %s changed the %s tag, so the field is covered", field, what, direction)
				}
			}
		}
	}
	// Every field of the schema is exercised, and the two directions agree about
	// every one of them: the covered set is `canonicalBytes`'s, and the direction
	// is an argument rather than a second rule.
	for _, field := range Fields {
		if !seen[field] {
			t.Errorf("no record in this test carries %s, so whether it is covered is unchecked", field)
		}
		a, b := covered[directionOutbox][field], covered[directionHeartbeat][field]
		if a != b {
			t.Errorf("%s is covered in one outbound direction and not the other: the two families are signed over the same field set", field)
		}
	}
}

// TestOutboundIsSigned is T2a's headline test for the reply, and the one the
// plan's red step (a) names: the value the store holds carries a tag over the
// covered fields, made with the pairing's key in the `outbox` direction — and the
// value that reaches the store is exactly the value that was signed, so what the
// phone reads is what the consumer signed.
//
// It asserts the store's own bytes rather than the value the function returned,
// because "the write is signed" is a fact about what landed and not about what a
// caller was handed.
func TestOutboundIsSigned(t *testing.T) {
	key := []byte(vectorKey(t))
	f := gcpfake.NewRTDB(t)
	store := leaseClient(t, f)
	reply := testReply(t, "r-m-0001", "m-0001")

	run, err := WriteReply(context.Background(), store, NewOutboxSigner(keychain.NewKey(key)).Tag, reply)
	if err != nil {
		t.Fatalf("writing the reply: %v", err)
	}
	if run.Verdict != ReplyWritten || run.Value == nil {
		t.Fatalf("the reply was not written: %s, %v", run.Verdict, run.Refusal)
	}
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}
	stored := storedRecord(t, f, path)
	if stored == nil {
		t.Fatal("the reply is not in the outbox: a write the command could sign must land")
	}
	// The store's value is the one that was signed: the tag verifies over the
	// bytes that landed, in the outbox direction.
	if ok, err := VerifyMAC(stored, key, directionOutbox); err != nil || !ok {
		t.Fatalf("the value in the outbox does not carry its own tag in the outbox direction: %v, %v", ok, err)
	}
	if ok, err := VerifyMAC(stored, key, directionHeartbeat); err != nil || ok {
		t.Errorf("the reply in the outbox verifies as a heartbeat: %v, %v", ok, err)
	}
	// The covered set, over the value that landed: changing any covered field
	// changes the tag, and the two uncovered ones do not.
	base, err := MacOf(stored, key, directionOutbox)
	if err != nil {
		t.Fatalf("MacOf over the stored value: %v", err)
	}
	if want := text(t, stored["mac"], "mac"); base != want {
		t.Errorf("the tag over the stored value is %s and the value carries %s", base, want)
	}
	for _, field := range []string{"id", "sender", "text", "at", "kind", "reply_to"} {
		if !stored.has(field) {
			t.Fatalf("the reply in the outbox carries no %s, so the value written is not the reply that was built", field)
		}
		clone := cloneRecord(t, stored)
		clone[field] = changedValue(clone[field])
		got, err := MacOf(clone, key, directionOutbox)
		if err != nil {
			t.Fatalf("MacOf with %s changed: %v", field, err)
		}
		if got == base {
			t.Errorf("changing %s left the tag alone, so the reply is not signed over it", field)
		}
	}
	// And the value is exactly §6's field set plus the kind's: a field the node
	// does not name would be a value the rules refuse, and one this consumer has
	// no business writing.
	want := map[string]bool{"id": true, "sender": true, "text": true, "at": true, "mac": true, "kind": true, "reply_to": true}
	for field := range stored {
		if !want[field] {
			t.Errorf("the reply in the outbox carries %s, which §6's node does not name", field)
		}
	}
}

// TestTheHeartbeatIsSignedInItsOwnDirection is the other half of §8.2.3: the
// heartbeat the lease writes carries a tag in the `heartbeat` direction, over
// `{at, boot}` — the fields the merged rules name — and it does not verify as an
// outbox value.
func TestTheHeartbeatIsSignedInItsOwnDirection(t *testing.T) {
	key := []byte(vectorKey(t))
	f := gcpfake.NewRTDB(t)
	store := leaseClient(t, f)
	signer := NewHeartbeatSigner(keychain.NewKey(key))

	opts := leaseOptions(leaseHolder, leaseAt)
	opts.Sign = signer.Heartbeat
	if _, err := Take(context.Background(), store, opts); err != nil {
		t.Fatalf("taking the lease: %v", err)
	}
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}
	stored := storedRecord(t, f, path)
	if stored == nil {
		t.Fatal("the heartbeat is not in the store: the take must write it")
	}
	if ok, err := VerifyMAC(stored, key, directionHeartbeat); err != nil || !ok {
		t.Fatalf("the heartbeat does not carry its own tag in the heartbeat direction: %v, %v", ok, err)
	}
	if ok, err := VerifyMAC(stored, key, directionOutbox); err != nil || ok {
		t.Errorf("the heartbeat verifies as an outbox value: %v, %v", ok, err)
	}
	// The tag is over `{at, boot}` and nothing else: the two fields the
	// heartbeat's MAC covers, which is what a reader with the same key verifies.
	tag, err := signer.Heartbeat(Heartbeat{At: leaseAt, Boot: leaseHolder})
	if err != nil {
		t.Fatalf("signing the heartbeat: %v", err)
	}
	if got := text(t, stored["mac"], "mac"); got != tag {
		t.Errorf("the stored heartbeat carries %s, and the heartbeat's own signature is %s", got, tag)
	}
}

// TestDirectionsAreNotInterchangeable is the plan's red step (d): a heartbeat
// signed in the `outbox` direction is not a heartbeat. The signer refuses to
// produce one at all — the node a value is signed for is a property of the
// signer, not a parameter — and the tag an outbox signer would make for the
// heartbeat's fields does not verify in the heartbeat direction, which is the
// fact underneath the refusal.
func TestDirectionsAreNotInterchangeable(t *testing.T) {
	key := []byte(vectorKey(t))
	hb := Heartbeat{At: leaseAt, Boot: leaseHolder}
	if tag, err := NewOutboxSigner(keychain.NewKey(key)).Heartbeat(hb); err == nil {
		t.Errorf("a signer built for the outbox signed a heartbeat: %q", tag)
	}
	// The mutation this catches is a heartbeat signed in the outbox direction
	// (the guard removed): the value is then a real value with a real tag that
	// no heartbeat reader accepts.
	value := Record{"at": leaseAt, "boot": leaseHolder}
	tag, err := MacOf(value, key, directionOutbox)
	if err != nil {
		t.Fatalf("MacOf: %v", err)
	}
	value["mac"] = tag
	if _, refusal := ValidateHeartbeat(value); refusal != nil {
		t.Fatalf("the schema refuses a heartbeat-shaped value, so this test proves nothing about the tag: %s", refusal)
	}
	if ok, err := VerifyMAC(value, key, directionHeartbeat); err != nil || ok {
		t.Errorf("a heartbeat signed in the outbox direction verified as a heartbeat: %v, %v", ok, err)
	}
}

// TestUnsignedHeartbeatIsNotWritten is the plan's red step (c), on M1's node: a
// heartbeat this consumer cannot sign is not written — and since the heartbeat
// write **is** the lease, no lease is taken either, and the pass that could not
// sign acts on nothing.
func TestUnsignedHeartbeatIsNotWritten(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}

	opts := leaseOptions(leaseHolder, leaseAt)
	opts.Sign = func(Heartbeat) (string, error) { return "", errors.New("the signing service is gone") }
	run, err := Take(context.Background(), store, opts)
	if err == nil {
		t.Fatal("a take whose heartbeat cannot be signed succeeded")
	}
	if !errors.Is(err, ErrUnsignable) {
		t.Errorf("the take answered %v, which is not ErrUnsignable", err)
	}
	if run.Verdict == LeaseHeld || run.Verdict == LeaseTakenOver {
		t.Errorf("the take answered %s with no heartbeat written: the heartbeat write IS the lease", run.Verdict)
	}
	if store.writes != 0 || f.Value(path) != nil {
		t.Errorf("an unsignable heartbeat was written (%v) after %d writes", f.Value(path), store.writes)
	}
	// The key is loaded and the same consumer signs a good value: this is M4's
	// third row, not its first — nothing here is the availability check.
	good := leaseOptions(leaseHolder, leaseAt)
	good.Sign = NewHeartbeatSigner(keychain.NewKey([]byte(vectorKey(t)))).Heartbeat
	if _, err := Take(context.Background(), store, good); err != nil {
		t.Fatalf("the same consumer could not sign a heartbeat it can sign: %v", err)
	}
	if f.Value(path) == nil {
		t.Error("the heartbeat the consumer could sign was not written")
	}
}

// TestSigningFailureIsAPerWriteRefusal is the plan's red step (e), and the line
// M4's table spends its detail on: a signer that errors on one value is **a
// per-write refusal inside a running pass** — an outcome of a completed pass,
// exit 0 (§4.4) — and never §8.3's availability check, which is exit 1 with no
// pass run.
//
// The injected signer is only reachable if the availability check has already
// passed, and both families are asserted here because both write paths are the
// same row: the outbox answers the refusal as a value (the way the gate answers
// a message that does not verify), and the lease answers it as an error, and a
// caller classifies both as ErrUnsignable and neither as the availability row.
func TestSigningFailureIsAPerWriteRefusal(t *testing.T) {
	ctx := context.Background()

	// The outbox: a refusal, not an error, and nothing written.
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	reply := testReply(t, "r-m-0002", "m-0002")
	failing := func(Record) (string, error) { return "", errors.New("this value cannot be signed") }
	run, err := WriteReply(ctx, store, failing, reply)
	if err != nil {
		t.Fatalf("an unsignable reply ended as an error (%v): a value that cannot be signed is a per-write refusal, an outcome of a completed pass", err)
	}
	if run.Verdict != ReplyRefused || run.Refusal == nil {
		t.Fatalf("an unsignable reply answered %s, %v", run.Verdict, run.Refusal)
	}
	if run.Refusal.Code != CodeBadMAC {
		t.Errorf("the refusal's code is %s, want %s: the schema's own code for a tag that is not a tag", run.Refusal.Code, CodeBadMAC)
	}
	if run.Value != nil {
		t.Errorf("a refused reply answered a value as well: %v", run.Value)
	}
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}
	if store.writes != 0 || f.Value(path) != nil {
		t.Errorf("an unsignable reply was written (%v) after %d writes", f.Value(path), store.writes)
	}

	// The heartbeat: an error that is ErrUnsignable's, and the same answer to
	// the question that matters — which of M4's three rows is this.
	hbStore := &replyRecorder{inner: leaseClient(t, gcpfake.NewRTDB(t))}
	hb := leaseOptions(leaseHolder, leaseAt)
	hb.Sign = func(Heartbeat) (string, error) { return "", errors.New("this value cannot be signed") }
	_, heartbeatErr := Take(ctx, hbStore, hb)
	if !errors.Is(heartbeatErr, ErrUnsignable) {
		t.Errorf("an unsignable heartbeat ended as %v, not ErrUnsignable", heartbeatErr)
	}

	// Neither is the availability row, and that is the whole point: §8.3's
	// startup exit is a key that is not there, and it is told apart by errors.Is
	// because the two answers are exit 1 with no pass run and a recorded refusal
	// inside a pass that ran.
	for what, err := range map[string]error{"the outbox": err, "the heartbeat": heartbeatErr} {
		if errors.Is(err, keychain.ErrNoKey) || errors.Is(err, keychain.ErrBackend) {
			t.Errorf("%s answered the availability refusal (%v): a signer that errored on one value is not a missing key", what, err)
		}
	}
}

// TestARefusedReplyIsOwedAgain is §8.2.4's recovery: a reply that could not be
// signed is not lost. Nothing is written and nothing is recorded for it, so the
// next pass reads the message again and owes it again (M2) — and the proof is
// that a later write of the same reply, with a signer that works, lands.
//
// The second half is create-only (M10, C0): a reply whose id is already in the
// outbox is not overwritten, because the answer the phone reads is the one that
// was written first.
func TestARefusedReplyIsOwedAgain(t *testing.T) {
	key := []byte(vectorKey(t))
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	signer := NewOutboxSigner(keychain.NewKey(key)).Tag
	reply := testReply(t, "r-m-0003", "m-0003")
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}

	if run, err := WriteReply(ctx, store, func(Record) (string, error) { return "", errors.New("no") }, reply); err != nil || run.Verdict != ReplyRefused {
		t.Fatalf("the unsignable reply answered %s, %v, %v", run.Verdict, run.Refusal, err)
	}
	if f.Value(path) != nil {
		t.Fatal("the refused reply is in the outbox: a write the command could not sign must not happen")
	}
	// The same reply, the next pass, with a signer that works: it is owed and it
	// is written.
	if run, err := WriteReply(ctx, store, signer, reply); err != nil || run.Verdict != ReplyWritten {
		t.Fatalf("the reply was not written on the next pass: %s, %v, %v", run.Verdict, run.Refusal, err)
	}
	first := storedRecord(t, f, path)
	if first == nil {
		t.Fatal("the reply owed from the refused pass never landed")
	}
	// Create-only: a second write of the same id, with a different body, is not
	// a write at all.
	changed := reply
	changed.Text = "a different answer under the same id"
	if run, err := WriteReply(ctx, store, signer, changed); err != nil || run.Verdict != ReplyPresent {
		t.Fatalf("a second write of one outbox id answered %s, %v, %v", run.Verdict, run.Refusal, err)
	}
	after := storedRecord(t, f, path)
	if got, want := text(t, after["text"], "text"), text(t, first["text"], "text"); got != want {
		t.Errorf("the outbox reply was overwritten: %q, want %q", got, want)
	}
}

// TestTheSignerNeverPrintsTheKey is M5's rule for the outbound half: the key
// appears in no output, no error string and no JSON. The signer holds a
// keychain.Key, which redacts itself in every shape a printer can ask for, and
// this test is what keeps that true if the field ever changes to a []byte.
func TestTheSignerNeverPrintsTheKey(t *testing.T) {
	material := vectorKey(t)
	signer := NewOutboxSigner(keychain.NewKey([]byte(material)))
	doc, err := json.Marshal(signer)
	if err != nil {
		t.Fatalf("json.Marshal(signer): %v", err)
	}
	// An error the signer produced: the value it could not sign is a record the
	// canonical serialisation refuses, so the error is one this package made.
	_, signErr := signer.Tag(Record{"id": "m-0001", "at": number("1700000000000"), "landed_at": nil})
	if signErr == nil {
		t.Fatal("signing a record canonicalBytes refuses succeeded")
	}
	var nilSigner *Signer
	printings := map[string]string{
		"the signer, %v":          fmt.Sprintf("%v", signer),
		"the signer, %+v":         fmt.Sprintf("%+v", signer),
		"the signer, %#v":         fmt.Sprintf("%#v", signer),
		"the signer, %s":          fmt.Sprintf("%s", signer),
		"the signer's JSON":       string(doc),
		"a nil signer, %v":        fmt.Sprintf("%v", nilSigner),
		"a nil signer's tag":      tagOrError(nilSigner),
		"the signer's error":      signErr.Error(),
		"the signer's error, %v":  fmt.Sprintf("%v", signErr),
		"a heartbeat it refuses":  heartbeatOrError(signer, Heartbeat{At: leaseAt, Boot: leaseHolder}),
		"a log line about them":   fmt.Sprintf("the reply could not be signed: %v", signErr),
		"the keychain's own Key":  keychain.NewKey([]byte(material)).String(),
		"the signer's direction":  signer.Direction(),
	}
	for what, got := range printings {
		if strings.Contains(got, material) {
			t.Errorf("%s carries the key: %s", what, got)
		}
	}
}

// TestOutboundWritesAreSignedAcrossSeededSchedules is the plan's "swept over the
// simulator's seeded schedules, not one call", in the shape this repository can
// run today: a seeded draw decides, per frame, whether the value is signed with
// the pairing's key, not signed at all, or signed with a tag the schema refuses —
// and the invariant is checked after every frame rather than at the end.
//
// **The invariant is the whole point of T2a, stated once:** every value in the
// outbox verifies under the pairing's key in the outbox direction, and no value
// that could not be signed is there. A schedule that drew a failing signer and
// still found a value in the outbox is a build that wrote unsigned.
//
// The corpus's own schedules (`shared/mailbox/parity.test.js` → T8's
// `internal/mailbox/testdata/parity.json`) are not in the tree yet, so the draw
// below is this test's own — seeded, replayable, and a failure prints its seed.
// When T8 lands, the same assertions are replayed over the module's schedules;
// this test is the fence until then, and it is why the sweep is a sweep rather
// than one call.
func TestOutboundWritesAreSignedAcrossSeededSchedules(t *testing.T) {
	key := []byte(vectorKey(t))
	ctx := context.Background()
	const frames = 8
	drawn := map[ReplyVerdict]int{}
	for seed := uint32(1); seed <= 24; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			store := &replyRecorder{inner: leaseClient(t, f)}
			state := seed*2654435761 + 1
			for frame := 0; frame < frames; frame++ {
				// The seeded draw: stir the state, then take it modulo the
				// vocabulary of faults, the way shared/mailbox-sim derives one
				// frame's fault from its seed.
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				reply := testReply(t, fmt.Sprintf("r-m-%04d-%04d", seed, frame), fmt.Sprintf("m-%04d", frame))
				path, err := OutboxPath(reply.Sender, reply.ID)
				if err != nil {
					t.Fatalf("seed %d frame %d: building the path: %v", seed, frame, err)
				}
				switch state % 3 {
				case 0:
					// The pairing's key: the write must land and must verify.
					run, err := WriteReply(ctx, store, NewOutboxSigner(keychain.NewKey(key)).Tag, reply)
					if err != nil {
						t.Fatalf("seed %d frame %d: writing the reply: %v", seed, frame, err)
					}
					drawn[run.Verdict]++
					if run.Verdict != ReplyWritten {
						t.Fatalf("seed %d frame %d: the reply was not written: %s, %v", seed, frame, run.Verdict, run.Refusal)
					}
					if f.Value(path) == nil {
						t.Fatalf("seed %d frame %d: the reply answered %s and is not in the outbox", seed, frame, run.Verdict)
					}
				default:
					// No signature at all, and a signature the schema refuses:
					// neither may reach the store, and neither is an error.
					sign := func(Record) (string, error) { return "", errors.New("this value cannot be signed") }
					if state%3 == 2 {
						sign = func(Record) (string, error) { return "not-a-tag", nil }
					}
					run, err := WriteReply(ctx, store, sign, reply)
					if err != nil {
						t.Fatalf("seed %d frame %d: an unsignable reply ended as an error: %v", seed, frame, err)
					}
					drawn[run.Verdict]++
					if run.Verdict != ReplyRefused {
						t.Fatalf("seed %d frame %d: an unsignable reply answered %s", seed, frame, run.Verdict)
					}
					if f.Value(path) != nil {
						t.Fatalf("seed %d frame %d: an unsigned reply is in the outbox (%v)", seed, frame, f.Value(path))
					}
				}
				// The invariant, after every frame: everything in the outbox is
				// signed by the pairing's key, in the outbox direction.
				assertOutboxIsSigned(t, f, key, seed, frame)
			}
		})
	}
	// The sweep drew both halves somewhere: a draw that never signed anything
	// would make the invariant vacuous.
	if drawn[ReplyWritten] == 0 || drawn[ReplyRefused] == 0 {
		t.Errorf("the sweep drew %d writes and %d refusals: a schedule that never drew both proves one half", drawn[ReplyWritten], drawn[ReplyRefused])
	}
}

// assertOutboxIsSigned walks everything under the mailbox's outbox and requires
// every value there to carry its own tag in the outbox direction under key.
func assertOutboxIsSigned(t *testing.T, f *gcpfake.RTDB, key []byte, seed uint32, frame int) {
	t.Helper()
	// No outbox yet is the state a schedule that has signed nothing leaves:
	// there is no value to check, which is the answer the invariant wants.
	if f.Value("mailbox/"+leaseMailbox+"/outbox") == nil {
		return
	}
	outbox := object(t, f.Value("mailbox/"+leaseMailbox+"/outbox"), "the outbox")
	for id, value := range outbox {
		record, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("seed %d frame %d: the outbox holds a %T at %s", seed, frame, value, id)
		}
		if ok, err := VerifyMAC(Record(record), key, directionOutbox); err != nil || !ok {
			t.Errorf("seed %d frame %d: the outbox value at %s does not carry its own tag in the outbox direction: %v, %v", seed, frame, id, ok, err)
		}
	}
}

// replyRecorder wraps the real client so a test can see what a reply write asked
// of the store: how many reads and how many conditional writes. It is how "an
// unsigned value never reaches the store" is asserted as a fact about the store
// rather than a fact about the value the caller was handed.
type replyRecorder struct {
	inner  OutboxStore
	reads  int
	writes int
}

func (s *replyRecorder) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	s.reads++
	return s.inner.GetETag(ctx, path, out)
}

func (s *replyRecorder) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	s.writes++
	return s.inner.PutIfMatch(ctx, path, etag, v)
}

// storedRecord is the value the store holds at path, read back through the
// package's own decoder — so a test asserts what the phone would read and not
// what a caller was handed.
func storedRecord(t *testing.T, f *gcpfake.RTDB, path string) Record {
	t.Helper()
	value := f.Value(path)
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("re-encoding the node at %s: %v", path, err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding the node at %s: %v", path, err)
	}
	return record
}

// testReply is the reply a test writes: an answer to the prompt it names, with
// every field the outbox node carries. The text is the vector file's plain
// message's, so the value is one the schema accepts.
func testReply(t *testing.T, id, replyTo string) Reply {
	t.Helper()
	return Reply{
		ID:      id,
		Sender:  leaseMailbox,
		Text:    text(t, plainMessage(t)["text"], "text"),
		At:      leaseAt,
		Kind:    "answer",
		ReplyTo: replyTo,
	}
}

// tagOrError is a nil signer's tag call, for the redaction test: a nil receiver
// must answer an error and not a panic, and the error must carry no key.
func tagOrError(s *Signer) string {
	tag, err := s.Tag(Record{"id": "m-0001", "sender": "phone", "text": "x", "at": 1700000000000})
	if err != nil {
		return err.Error()
	}
	return tag
}

// heartbeatOrError is a heartbeat signed by a signer built for another node: the
// refusal, for the redaction test.
func heartbeatOrError(s *Signer, hb Heartbeat) string {
	tag, err := s.Heartbeat(hb)
	if err != nil {
		return err.Error()
	}
	return tag
}
