package mailbox

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The consumer's own signatures on what it writes (mobile-identity-pairing J5c,
// O12): a reply and a heartbeat for a phone paired the new way are signed with
// the installation's key in Cloud KMS, and the phone verifies them with the
// public key it pinned. What the plan's verification names, and what each test
// below proves: the reply the outbox carries has `sig` and no `mac`, and it
// verifies under the public half of the key the key ring signed with
// (TestOutboxSignatureIsWrittenAndVerifies); the heartbeat the lease writes is
// the same, over `{at, boot, agents?}` (TestHeartbeatSignatureIsWrittenAndVerifies,
// TestHeartbeatSignatureCoversTheAdvertisedAgents); a signature a phone cannot
// verify is refused as the field it would have landed in
// (TestOutboxSignatureRefusedWriteNamesTheSignatureField); and a signer built for
// one node cannot sign for the other (TestKMSSignerCannotSignForTheOtherNode).
//
// Everything runs on `internal/gcpfake`'s Realtime Database fake and a key
// generated in this process (fakeKeyRing, kmssigner_test.go): no live cloud, no
// credential, no key file. The MAC's half of the same write paths is T2a's and
// is unchanged — mac_sign_test.go still sweeps it — which is the point of the
// scheme living in the signer rather than in the writer.

// kmsReplySigner is the outbox signer a phone paired the new way is served by,
// built the way the consumer builds it: the deployment's key version, the
// outbox's node (J5c, internal/cli/mailbox.go's mailboxConsumerSigners).
func kmsReplySigner(t *testing.T, ring AsymmetricSigner, project, region string) *KMSSigner {
	t.Helper()
	signer, err := NewKMSOutboxSigner(ring, ConsumerCryptoKeyVersionPath(project, region))
	if err != nil {
		t.Fatalf("building the outbox's KMS signer: %v", err)
	}
	return signer
}

func TestOutboxSignatureIsWrittenAndVerifies(t *testing.T) {
	ring := newFakeKeyRing(t)
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	reply := testReply(t, "r-k-0001", "m-0001")
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}

	signer := kmsReplySigner(t, ring, "proj-1234", "us-central1")
	if got := signer.Field(); got != tagFieldSig {
		t.Fatalf("the KMS signer writes %q, and O6's signature is the %s field (J5r)", got, tagFieldSig)
	}
	run, err := WriteReply(ctx, store, signer, reply)
	if err != nil || run.Verdict != ReplyWritten {
		t.Fatalf("a reply signed with the consumer's own key answered %s, %v, %v", run.Verdict, run.Refusal, err)
	}
	if run.Value == nil {
		t.Fatal("a written reply handed back no value")
	}
	stored := storedRecord(t, f, path)
	if stored == nil {
		t.Fatal("the reply is not in the store")
	}
	// The field is the signer's: `sig` and no `mac`. A value carrying both is
	// refused by the schema (J5r), and a value carrying `mac` would be one a
	// phone paired the new way has no key to check — the gate's own downgrade
	// fence, on the writing side.
	if _, has := stored["mac"]; has {
		t.Fatalf("the reply carries a `mac` as well as a signature: %v", stored)
	}
	sig, has := stored["sig"].(string)
	if !has || sig == "" {
		t.Fatalf("the reply carries no signature: %v", stored)
	}
	if len(sig) != SigChars {
		t.Fatalf("the signature is %d characters, and a signature is %d (J5r)", len(sig), SigChars)
	}
	// What the phone does with it: the signature verifies over the value's own
	// canonical bytes in the node it read, under the public half of the key the
	// key ring signed with.
	if ok, err := VerifySignature(stored, ring.pub, directionOutbox); err != nil || !ok {
		t.Fatalf("the reply in the outbox does not carry its own signature in the outbox direction: %v, %v", ok, err)
	}
	// And it does not verify in another node, which is the direction being
	// inside the signed bytes.
	if ok, err := VerifySignature(stored, ring.pub, directionHeartbeat); err != nil || ok {
		t.Errorf("the reply verifies as a heartbeat: %v, %v", ok, err)
	}
	// The tag covers the value and not itself: editing a field after signing
	// breaks it, so what the phone reads is what this consumer wrote.
	edited := cloneRecord(t, stored)
	edited["text"] = "something else"
	if ok, err := VerifySignature(edited, ring.pub, directionOutbox); err != nil || ok {
		t.Errorf("a reply whose text was changed after signing still verifies: %v, %v", ok, err)
	}
	// The key version travels to the call as the deployment names it, and no
	// MAC key was needed to write this: the value is signed and nothing else.
	if len(ring.versions) != 1 || ring.versions[0] != ConsumerCryptoKeyVersionPath("proj-1234", "us-central1") {
		t.Fatalf("the signing call named %v, want the one key version the deployment publishes", ring.versions)
	}
	if strings.ContainsAny(sig, "+/=") {
		t.Errorf("the signature %q is not the unpadded base64url the phone decodes", sig)
	}
}

func TestOutboxSignatureRefusedWriteNamesTheSignatureField(t *testing.T) {
	// A signing call that fails is M4's third row, and the refusal names the
	// field the tag would have landed in: `bad-sig` at `sig` (J5r). A refusal
	// that said `bad-mac` at `mac` would point the operator at a field this
	// value never had — and a value written with no signature is one no phone
	// paired the new way can read, so nothing is written at all.
	ring := newFakeKeyRing(t)
	ring.answer = func([]byte) ([]byte, error) { return nil, errors.New("the key ring is unreachable") }
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	reply := testReply(t, "r-k-0002", "m-0002")
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}

	run, err := WriteReply(ctx, store, kmsReplySigner(t, ring, "proj-1234", "us-central1"), reply)
	if err != nil {
		t.Fatalf("a reply the signer could not sign ended as an error (%v): M4b says it is a per-write refusal", err)
	}
	if run.Verdict != ReplyRefused || run.Refusal == nil {
		t.Fatalf("the write answered %s, %v, want a refusal", run.Verdict, run.Refusal)
	}
	if run.Refusal.Code != CodeBadSig || run.Refusal.Field != tagFieldSig {
		t.Errorf("the refusal is %s at %s, want %s at %s", run.Refusal.Code, run.Refusal.Field, CodeBadSig, tagFieldSig)
	}
	if run.Value != nil || store.writes != 0 || f.Value(path) != nil {
		t.Errorf("an unsigned reply was written (%v) after %d writes", f.Value(path), store.writes)
	}
}

func TestHeartbeatSignatureIsWrittenAndVerifies(t *testing.T) {
	ring := newFakeKeyRing(t)
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := leaseClient(t, f)
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}

	signer, err := NewKMSHeartbeatSigner(ring, ConsumerCryptoKeyVersionPath("proj-1234", "us-central1"))
	if err != nil {
		t.Fatalf("building the heartbeat's KMS signer: %v", err)
	}
	opts := LeaseOptions{Mailbox: leaseMailbox, Holder: leaseHolder, At: leaseAt, Sign: signer}
	run, err := Take(ctx, store, opts)
	if err != nil {
		t.Fatalf("taking the lease with the consumer's own key: %v", err)
	}
	if run.Verdict != LeaseHeld {
		t.Fatalf("the take answered %s, want held: the heartbeat write IS the lease", run.Verdict)
	}
	stored := storedRecord(t, f, path)
	if stored == nil {
		t.Fatal("the heartbeat is not in the store")
	}
	if _, has := stored["mac"]; has {
		t.Fatalf("the heartbeat carries a `mac`: %v", stored)
	}
	if ok, err := VerifySignature(stored, ring.pub, directionHeartbeat); err != nil || !ok {
		t.Fatalf("the heartbeat does not carry its own signature in the heartbeat direction: %v, %v", ok, err)
	}
	// The fields the merged rules allow on the heartbeat and no fourth: `at`,
	// `boot` and the one tag.
	if len(stored) != 3 {
		t.Fatalf("the heartbeat carries %d fields (%v), and the rules allow `at`, `boot` and one tag", len(stored), sortedKeys(stored))
	}
	// A renewal signs again, over the same fields, and the lease stays this
	// consumer's: the signature is not a one-off a long-lived consumer loses.
	opts.At = leaseAt + ClaimTTL/2
	renewed, err := Renew(ctx, store, opts)
	if err != nil || renewed.Verdict != LeaseHeld {
		t.Fatalf("renewing with the consumer's own key answered %s, %v", renewed.Verdict, err)
	}
	after := storedRecord(t, f, path)
	if got, ok := intValue(after["at"]); !ok || got != opts.At {
		t.Fatalf("the renewed heartbeat is at %v, want %d", after["at"], opts.At)
	}
	if ok, err := VerifySignature(after, ring.pub, directionHeartbeat); err != nil || !ok {
		t.Errorf("the renewed heartbeat does not carry its own signature: %v, %v", ok, err)
	}
}

func TestHeartbeatSignatureCoversTheAdvertisedAgents(t *testing.T) {
	// P6.7's half of the heartbeat's field set (D161), on the signature scheme:
	// the advertised list is inside the signed bytes, so a list changed in the
	// store after the take is a heartbeat that verifies nowhere. A signer handed
	// the list and a writer that added it afterwards would produce exactly that
	// — which is why both build the record with one function (heartbeatValue).
	ring := newFakeKeyRing(t)
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := leaseClient(t, f)
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}
	signer, err := NewKMSHeartbeatSigner(ring, ConsumerKeyVersionName)
	if err != nil {
		t.Fatalf("building the heartbeat's KMS signer: %v", err)
	}
	agents := []Agent{{ID: "orchestrator", Name: "Orchestrator"}, {ID: "scout", Name: "Scout"}}
	opts := LeaseOptions{Mailbox: leaseMailbox, Holder: leaseHolder, At: leaseAt, Sign: signer, Agents: agents}
	if _, err := Take(ctx, store, opts); err != nil {
		t.Fatalf("taking the lease with agents advertised: %v", err)
	}
	stored := storedRecord(t, f, path)
	if stored == nil || stored["agents"] == nil {
		t.Fatalf("the heartbeat does not carry the advertised list: %v", stored)
	}
	if ok, err := VerifySignature(stored, ring.pub, directionHeartbeat); err != nil || !ok {
		t.Fatalf("the heartbeat advertising its agents does not verify: %v, %v", ok, err)
	}
	edited := cloneRecord(t, stored)
	edited["agents"] = AgentList([]Agent{{ID: "orchestrator", Name: "Orchestrator"}, {ID: "impostor", Name: "Scout"}})
	if ok, err := VerifySignature(edited, ring.pub, directionHeartbeat); err != nil || ok {
		t.Errorf("a heartbeat whose advertised list was changed after signing still verifies: %v, %v", ok, err)
	}
}

func TestKMSSignerCannotSignForTheOtherNode(t *testing.T) {
	// The two outbound families are signed in two directions (§8.2.3), and the
	// node is a property of the signer: a heartbeat signer handed to a reply
	// write is refused before it is called, because the value it produced would
	// be one no phone verifies and, create-only, one no later write could
	// correct (M10, C0). The refusal names the signature's field, which is the
	// scheme this signer writes.
	ring := newFakeKeyRing(t)
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	store := &replyRecorder{inner: leaseClient(t, f)}
	reply := testReply(t, "r-k-0003", "m-0003")
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}

	heartbeat, err := NewKMSHeartbeatSigner(ring, ConsumerKeyVersionName)
	if err != nil {
		t.Fatalf("building the heartbeat's KMS signer: %v", err)
	}
	run, err := WriteReply(ctx, store, heartbeat, reply)
	if err != nil {
		t.Fatalf("a reply signed with the heartbeat's signer ended as an error (%v)", err)
	}
	if run.Verdict != ReplyRefused || run.Refusal == nil || run.Refusal.Code != CodeBadSig || run.Refusal.Field != tagFieldSig {
		t.Fatalf("a reply signed in the %s direction answered %s, %v", heartbeat.Direction(), run.Verdict, run.Refusal)
	}
	if run.Value != nil || store.writes != 0 || f.Value(path) != nil {
		t.Errorf("a reply signed in the %s direction was written (%v) after %d writes", heartbeat.Direction(), f.Value(path), store.writes)
	}
	// And the heartbeat's own signer refuses a heartbeat it is not built for:
	// the outbox signer's Heartbeat is a refusal, not a signature.
	outbox := kmsReplySigner(t, ring, "proj-1234", "us-central1")
	if sig, err := outbox.Heartbeat(ctx, Heartbeat{At: leaseAt, Boot: leaseHolder}); !errors.Is(err, ErrUnsignable) || sig != "" {
		t.Errorf("an outbox signer answered (%q, %v) for a heartbeat, want ErrUnsignable", sig, err)
	}
	if len(ring.payloads) != 0 {
		t.Errorf("the key ring was called %d time(s) for a value the signer refuses: nothing is signed for it", len(ring.payloads))
	}
}

func TestKMSSignerSignatureIsTheSignerField(t *testing.T) {
	// The field is what a writer reads to place the tag, and it is the scheme
	// the signer was built for: `sig` for the KMS signer, `mac` for the
	// keychain's (J5r). A signer that answered neither would put a tag in a
	// field the schema refuses, which is a refusal and not a write.
	ring := newFakeKeyRing(t)
	outbox := kmsReplySigner(t, ring, "proj-1234", "us-central1")
	if got := outbox.Field(); got != tagFieldSig {
		t.Fatalf("the KMS signer names the field %q, want %q", got, tagFieldSig)
	}
	if got := NewOutboxSigner(keychain.NewKey([]byte("a-placeholder-mac-key"))).Field(); got != tagFieldMAC {
		t.Fatalf("the MAC signer names the field %q, want %q", got, tagFieldMAC)
	}
	var nilKMS *KMSSigner
	if got := nilKMS.Field(); got != "" {
		t.Errorf("a nil KMS signer names the field %q, and a nil signer names nothing", got)
	}
	var nilMAC *Signer
	if got := nilMAC.Field(); got != "" {
		t.Errorf("a nil MAC signer names the field %q, and a nil signer names nothing", got)
	}
	// The vocabulary the two names come from is the module's, and it is the
	// closed set J5r's rules are written against: a third field would be a tag
	// the rules refuse.
	if strings.Join(TagFields, ",") != "mac,sig" {
		t.Fatalf("TagFields = %v, and a record carries exactly `mac` or `sig`", TagFields)
	}
	// The signed bytes are the MAC's: one covered field set for both schemes,
	// so the phone's verifier and this signer cannot drift (M9).
	value := consumerValue()
	payload, err := CanonicalBytes(value, directionOutbox)
	if err != nil {
		t.Fatalf("the value's canonical bytes: %v", err)
	}
	sig, err := outbox.Tag(context.Background(), value)
	if err != nil {
		t.Fatalf("signing a reply: %v", err)
	}
	raw, err := consumerKeyEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("the signature is not unpadded base64url: %v", err)
	}
	if !ed25519.Verify(ring.pub, payload, raw) {
		t.Error("the signature does not verify over the value's canonical bytes")
	}
}
