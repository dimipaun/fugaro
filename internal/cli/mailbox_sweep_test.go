package cli

// fugaro mailbox sweep (mobile P6.13): the owner-run sweeper's rule for stored
// prompts, tested against the task's own verification list.
//
// The separation the tests hold is the task's whole point. **The sweeper
// deletes and never answers**: expiring a prompt and handing the orchestrator
// its non-destructive answer — or a stop, when it named none — is the
// consumer's, on its own clock and from its own copy of the question, and that
// is why a sweep may run while the consumer is down. So the cases here are the
// sweeper's delete set (a stored prompt past D118's TTL, and the things that are
// not one), the store's one changed node, and the consumer **that was down for
// the sweep** still owing the answer exactly once when it comes back — the red
// step, which no consumer that read the question out of the store could pass.
//
// Every case drives the real command tree (`mailbox sweep`, and `mailbox
// --once` for the consumer's half), with the database the operator's own
// credential reaches replaced by internal/gcpfake and its clock fixed, so a
// decision here is a function of the seeds rather than of how long the test
// took.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// sweepClock fixes the database's own clock (D117), which is the clock both the
// sweeper and the consumer decide against — the header every answer of the fake
// carries. It is truncated to the second because the TTL boundary is asserted
// exactly, and a clock with nanoseconds in it would make "at the TTL" a
// statement about the test's own arithmetic rather than about the rule.
func (f *mailboxFixture) sweepClock(t *testing.T) time.Time {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	f.db.SetClock(func() time.Time { return now })
	return now
}

// mailboxSweepTTL is D118's 72 hours as a duration: the same number as
// mailboxPromptTtlMs, and the tests age a prompt against it rather than against
// a literal of their own.
func mailboxSweepTTL() time.Duration {
	return time.Duration(mailboxPromptTtlMs) * time.Millisecond
}

// client is the rtdb client over this fixture's fake, built the way the command
// builds it: the tests that need a package function taking a store (WriteReply)
// drive the same client the sweeper does.
func (f *mailboxFixture) client(t *testing.T) *rtdb.Client {
	t.Helper()
	_, db, err := openBudget(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// signer is the outbox signer for this fixture's one pairing, built from the
// fake keychain's key the way the consumer builds it (M5).
func (f *mailboxFixture) signer(t *testing.T) mailbox.ReplySigner {
	t.Helper()
	item, err := keychain.ParseItem(f.item, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.keys.Key(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	return mailbox.NewOutboxSigner(key)
}

// announce seeds one stored prompt **the way the consumer writes it**: a signed
// outbox message of kind `prompt`, through the package's own WriteReply, so what
// the sweep reads is a record a real announcement produces and not a shape the
// test invented.
func (f *mailboxFixture) announce(t *testing.T, id string, at int64) {
	t.Helper()
	run, err := mailbox.WriteReply(context.Background(), f.client(t), f.signer(t), mailbox.Reply{
		ID:     id,
		Sender: mailboxUID,
		Text:   "may I rewrite the module?",
		At:     at,
		Kind:   mailboxKindPrompt,
	})
	if err != nil {
		t.Fatalf("announcing %s: %v", id, err)
	}
	if run.Verdict != mailbox.ReplyWritten {
		t.Fatalf("announcing %s answered %q, want written", id, run.Verdict)
	}
}

// reply seeds one outbox message that is **not** a prompt — the answer the
// consumer owed a consumed delivery — so a test can hold the sweeper to its
// delete set.
func (f *mailboxFixture) reply(t *testing.T, id, forID string, at int64) {
	t.Helper()
	run, err := mailbox.WriteReply(context.Background(), f.client(t), f.signer(t), mailbox.Reply{
		ID:      id,
		Sender:  mailboxUID,
		Text:    mailboxStaleReply,
		At:      at,
		Kind:    mailboxKindAnswer,
		ReplyTo: forID,
	})
	if err != nil {
		t.Fatalf("seeding the reply %s: %v", id, err)
	}
	if run.Verdict != mailbox.ReplyWritten {
		t.Fatalf("seeding the reply %s answered %q, want written", id, run.Verdict)
	}
}

// storeOutbox is the outbox node as the store holds it, so a test can say what
// survived a sweep by reading the tree rather than by trusting the report.
func (f *mailboxFixture) storeOutbox(t *testing.T) map[string]any {
	t.Helper()
	value := f.db.Value("mailbox/" + mailboxUID + "/outbox")
	if value == nil {
		return map[string]any{}
	}
	outbox, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("the outbox node is %T, want an object", value)
	}
	return outbox
}

// storedPrompts is the number of stored prompts the outbox holds: the queue's
// storage bound, counted from the store rather than from the consumer's memory.
func (f *mailboxFixture) storedPrompts(t *testing.T) int {
	t.Helper()
	count := 0
	for _, value := range f.storeOutbox(t) {
		record, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("an outbox child is %T, want an object", value)
		}
		if kind, _ := record["kind"].(string); kind == mailboxKindPrompt {
			count++
		}
	}
	return count
}

// sweepDocument is the `--json` document read back with the wire's own field
// names rather than through this package's structs: the names are the contract a
// reader outside this binary consumes, so a rename has to fail a test here and
// not pass because both sides moved.
type sweepDocument struct {
	Project   string `json:"project"`
	At        string `json:"at"`
	DryRun    bool   `json:"dry_run"`
	Mailboxes []struct {
		Mailbox string   `json:"mailbox"`
		Deleted []string `json:"deleted"`
		Kept    int      `json:"kept"`
	} `json:"mailboxes"`
	Warnings []string `json:"warnings"`
}

func sweepJSON(t *testing.T, out string) sweepDocument {
	t.Helper()
	var doc sweepDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the sweep's document does not decode (%v): %s", err, out)
	}
	return doc
}

// localState is this machine's own state — the ledger and the prompt memory
// under $XDG_STATE_HOME — as its files are, so a test can say that a sweep left
// the consumer's half alone. The sweeper deletes store nodes and nothing else,
// so a file it touched or made would be a write the consumer's next pass reads
// as its own.
func localState(t *testing.T) map[string]string {
	t.Helper()
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		t.Fatal("the fixture isolated no state directory, so this test would read the machine's own")
	}
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[name] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading this machine's state: %v", err)
	}
	return files
}

// TestMailboxSweepDeletesTheExpiredStoredPromptAndNothingElse is P6.13's delete
// set at both sides of the boundary, held against the store rather than against
// the report: a stored prompt at the TTL is deleted, one a millisecond inside it
// survives, and everything the sweeper is not about — the consumer's own
// answers, the phone's inbox, the lease — is untouched.
func TestMailboxSweepDeletesTheExpiredStoredPromptAndNothingElse(t *testing.T) {
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	atTheTtl := now.Add(-mailboxSweepTTL())
	insideTheTtl := atTheTtl.Add(time.Millisecond)

	f.announce(t, "p-old", atTheTtl.UnixMilli())
	f.announce(t, "p-young", insideTheTtl.UnixMilli())
	f.reply(t, "r-msg-1", "msg-1", now.Add(-2*mailboxSweepTTL()).UnixMilli())
	f.writeMessage(t, "msg-1", now.Add(-2*mailboxSweepTTL()).UnixMilli())
	f.db.Set("mailbox/"+mailboxUID+"/heartbeat", map[string]any{"at": now.UnixMilli(), "boot": "boot-seeded"})

	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	doc := sweepJSON(t, out)
	if doc.Project != "aurora" || doc.DryRun {
		t.Fatalf("document = %+v, want project aurora and a sweep that really wrote", doc)
	}
	if len(doc.Mailboxes) != 1 || doc.Mailboxes[0].Mailbox != mailboxUID {
		t.Fatalf("mailboxes = %+v, want the one configured pairing %s", doc.Mailboxes, mailboxUID)
	}
	if got := doc.Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-old" {
		t.Fatalf("deleted = %v, want exactly the prompt at the TTL", got)
	}
	if got := doc.Mailboxes[0].Kept; got != 1 {
		t.Fatalf("kept = %d, want the prompt inside the TTL and nothing else counted as a prompt", got)
	}

	outbox := f.storeOutbox(t)
	if _, present := outbox["p-old"]; present {
		t.Fatal("the prompt at the TTL is still stored: the sweep reported it deleted and the store disagrees")
	}
	for _, id := range []string{"p-young", "r-msg-1"} {
		if _, present := outbox[id]; !present {
			t.Fatalf("%s is gone from the outbox: the sweep deletes stored prompts and nothing else", id)
		}
	}
	if f.db.Value("mailbox/"+mailboxUID+"/inbox/msg-1") == nil {
		t.Fatal("the phone's inbox message was deleted: it is not a stored prompt")
	}
	if f.heartbeat() == nil {
		t.Fatal("the lease was deleted: the sweep takes no lease and touches none")
	}
}

// TestMailboxSweepDryRunDeletesNothing is §4.2's `--dry-run`: the same plan,
// printed, with the store left exactly as it was.
func TestMailboxSweepDryRunDeletesNothing(t *testing.T) {
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	f.announce(t, "p-old", now.Add(-mailboxSweepTTL()-time.Second).UnixMilli())

	before := f.storeOutbox(t)
	out, _, err := execute(t, "mailbox", "sweep", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	doc := sweepJSON(t, out)
	if !doc.DryRun {
		t.Fatalf("document = %+v, want dry_run true", doc)
	}
	if got := doc.Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-old" {
		t.Fatalf("deleted = %v, want the plan to name the prompt past the TTL", got)
	}
	after := f.storeOutbox(t)
	if len(after) != len(before) {
		t.Fatalf("the outbox holds %d nodes after a dry run, want %d: nothing is written", len(after), len(before))
	}
	if _, present := after["p-old"]; !present {
		t.Fatal("a dry run deleted the prompt it only meant to name")
	}
}

// TestMailboxSweepWritesNoAnswer is P6.13's "and nothing more", asserted against
// the whole store: the one node the sweep changed is the prompt it deleted. No
// outbox answer, no heartbeat, no ledger node, and no offer to an orchestrator —
// the sweeper never opens one.
func TestMailboxSweepWritesNoAnswer(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := f.sweepClock(t)
	// A prompt the consumer is holding, so this machine's own state exists to be
	// compared: a sweep that wrote nothing to an empty directory would pass this
	// half of the test without proving anything.
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-young", At: now.UnixMilli(), Mailbox: mailboxUID, Safe: "carry on",
	}, now.UnixMilli())
	f.announce(t, "p-old", now.Add(-mailboxSweepTTL()-time.Second).UnixMilli())
	f.announce(t, "p-young", now.UnixMilli())

	before := f.db.Value("")
	beforeLocal := localState(t)
	if len(beforeLocal) == 0 {
		t.Fatal("this machine holds no state for the sweep to leave alone: the comparison below would be vacuous")
	}
	if _, _, err := execute(t, "mailbox", "sweep", "--json"); err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	after := f.db.Value("")
	afterLocal := localState(t)

	want := before.(map[string]any)
	mailboxes := want["mailbox"].(map[string]any)
	node := mailboxes[mailboxUID].(map[string]any)
	outbox := node["outbox"].(map[string]any)
	delete(outbox, "p-old")
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expected) {
		t.Fatalf("the store after the sweep is\n%s\nwant the same tree with only the expired prompt removed\n%s", got, expected)
	}
	if len(*offers) != 0 {
		t.Fatalf("offers = %+v, want none: the sweeper never answers the conversation", *offers)
	}
	if !reflect.DeepEqual(beforeLocal, afterLocal) {
		t.Fatalf("this machine's state after the sweep is\n%v\nwant the ledger and the prompt memory untouched\n%v: a sweep that wrote one would be answering the conversation it only deletes for", afterLocal, beforeLocal)
	}
}

// TestMailboxSweepConsumerDownStillExpiresFromTheLocalCopy is P6.13's red step.
//
// The orchestrator asked a question and the consumer that was up announced it and
// remembered the answer it would take; then the consumer went **down**, and the
// sweep — the only thing that ran — deleted the stored prompt. A consumer that
// answered out of the store would find nothing there and deliver nothing, which
// is what makes this a test and not a formality: the question comes back to the
// orchestrator with its safe answer, exactly once, from the local copy and the
// consumer's own clock.
func TestMailboxSweepConsumerDownStillExpiresFromTheLocalCopy(t *testing.T) {
	const safe = "carry on with the checklist"
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := f.sweepClock(t)
	at := now.Add(-mailboxSweepTTL() - time.Second)
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-1", At: at.UnixMilli(), Mailbox: mailboxUID, Safe: safe,
	}, now.UnixMilli())
	f.announce(t, "p-1", at.UnixMilli())

	// The consumer is down: the sweep is all that runs.
	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("the sweep while the consumer was down: %v", err)
	}
	if got := sweepJSON(t, out).Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-1" {
		t.Fatalf("deleted = %v, want the stored prompt deleted with the consumer down", got)
	}
	if len(*offers) != 0 {
		t.Fatalf("offers = %+v, want none: the sweep answered nothing", *offers)
	}

	// The consumer comes back, and the answer is owed from its own copy.
	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("the consumer's pass after the sweep: %v", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers = %+v, want the safe answer handed over once", *offers)
	}
	if offer := (*offers)[0]; offer.Text != safe || offer.Outcome != mailboxOfferExpired || offer.ID != "p-1" {
		t.Fatalf("offer = %+v, want %q with outcome %q", offer, safe, mailboxOfferExpired)
	}
	// And once is once: a second pass over the same state owes nothing more.
	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("the consumer's second pass: %v", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers after a second pass = %d, want 1: the expiry is recorded, not re-derived", len(*offers))
	}
}

// TestMailboxSweepNeverGrowsTheStorePastTheCap is P6.5's cap as the storage
// bound P6.13 pins: with the queue full the next question is refused
// "queue full" and is **never stored**, and a sweep of that full queue neither
// adds a node nor deletes one the owner may still answer.
func TestMailboxSweepNeverGrowsTheStorePastTheCap(t *testing.T) {
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	if mailbox.ConfirmMaxPrompts < 2 {
		t.Fatalf("ConfirmMaxPrompts = %d: this test needs a cap worth filling", mailbox.ConfirmMaxPrompts)
	}
	for i := 0; i < mailbox.ConfirmMaxPrompts; i++ {
		id := "p-" + strconv.Itoa(i)
		f.holdPrompt(t, mailbox.ConfirmPrompt{
			ID: id, At: now.UnixMilli(), Mailbox: mailboxUID, Safe: "carry on",
		}, now.UnixMilli())
		f.announce(t, id, now.UnixMilli())
	}
	prev := mailboxPromptSource
	mailboxPromptSource = func(context.Context, *mailboxConsumer, mailboxPairing) []mailboxPromptRequest {
		return []mailboxPromptRequest{{ID: "p-over", Text: "one question too many"}}
	}
	t.Cleanup(func() { mailboxPromptSource = prev })

	if _, _, err := execute(t, "mailbox", "--once", "--json"); err != nil {
		t.Fatalf("err = %v, want exit 0: a queue-full answer is a completed pass", err)
	}
	if got := f.storedPrompts(t); got != mailbox.ConfirmMaxPrompts {
		t.Fatalf("the store holds %d prompts after a full queue refused one more, want %d: the cap is the storage bound, and a refused prompt is never announced", got, mailbox.ConfirmMaxPrompts)
	}

	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("the sweep: %v", err)
	}
	doc := sweepJSON(t, out)
	if got := doc.Mailboxes[0].Deleted; len(got) != 0 {
		t.Fatalf("deleted = %v, want none: every prompt in the queue is inside its TTL", got)
	}
	if got := doc.Mailboxes[0].Kept; got != mailbox.ConfirmMaxPrompts {
		t.Fatalf("kept = %d, want the %d prompts the owner may still answer", got, mailbox.ConfirmMaxPrompts)
	}
	if got := f.storedPrompts(t); got != mailbox.ConfirmMaxPrompts {
		t.Fatalf("the store holds %d prompts after the sweep, want %d: a sweep of a full queue adds nothing and removes nothing it may not", got, mailbox.ConfirmMaxPrompts)
	}
	if _, present := f.storeOutbox(t)["p-over"]; present {
		t.Fatal("the prompt refused at the cap was stored anyway")
	}
}

// TestMailboxSweepWithoutAConfiguredMailboxIsRefused is the consumer's own
// startup row, which the sweeper owes too: a project with no mailbox block names
// no mailbox to walk, and the command says so rather than reporting a sweep of
// nothing. It is decided from the config, so the store is never read.
func TestMailboxSweepWithoutAConfiguredMailboxIsRefused(t *testing.T) {
	newMailboxFixture(t, "# no mailbox block at all\n")
	_, _, err := execute(t, "mailbox", "sweep")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no mailbox configured") {
		t.Fatalf("err = %v, code %d, want exit 1 naming the missing block", err, ExitCode(err))
	}
}
