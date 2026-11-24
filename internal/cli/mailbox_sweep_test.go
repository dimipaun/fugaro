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
	Project string `json:"project"`
	At      string `json:"at"`
	DryRun  bool   `json:"dry_run"`
	// Local is T7's half of the sweep: this machine's own state, and the records
	// the retention window let go of in it (§6).
	Local struct {
		Ledger    string   `json:"ledger"`
		Prompts   string   `json:"prompts"`
		LedgerIDs []string `json:"ledger_ids"`
		PromptIDs []string `json:"prompt_ids"`
		Wrote     bool     `json:"wrote"`
		// Refused is why this machine's half did not happen, when another writer
		// holds its state: the same fields and `wrote: false` alone cannot tell
		// "nothing to let go of" from "not allowed to look".
		Refused string `json:"refused"`
	} `json:"local"`
	Mailboxes []struct {
		Mailbox string `json:"mailbox"`
		// Presence is the verdict the sweep computed for the stored heartbeat and
		// wrote nothing for (§4.2). AgeMs is a pointer because the module answers
		// `null` for a mailbox with no heartbeat, which is not an age of zero.
		Presence struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
			AgeMs  *int64 `json:"age_ms"`
			BootID string `json:"boot_id"`
			Code   string `json:"code"`
		} `json:"presence"`
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
//
// The state lock is skipped, and it is the one file that is not the consumer's
// state: it is the arbitration between the two writers that may touch the other
// two (`internal/mailbox`'s statelock.go), its contents are a courtesy for a
// refusal's wording, and no reader decides anything from it. A sweep that leaves
// nothing to let go of still takes it — a lock taken is not a state rewritten.
func localState(t *testing.T) map[string]string {
	t.Helper()
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		t.Fatal("the fixture isolated no state directory, so this test would read the machine's own")
	}
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == mailbox.StateLockFile {
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

// TestMailboxSweepIsHidden: the sweeper is the owner's command and not a
// reader's, so it is out of both places a reader meets the command tree — the
// generated reference and `mailbox --help`. It is the `newBudgetHistoryCmd`
// precedent (`TestHistoryIsHidden`) applied here: the command is in the tree and
// absent from what documents it, and a revision that unhid it fails this.
//
// Red step (b): drop `Hidden: true` from the sweeper's command and this fails,
// naming the reference the generator would then have to carry a section for.
func TestMailboxSweepIsHidden(t *testing.T) {
	out, _, err := execute(t, "mailbox", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sweep") {
		t.Fatalf("mailbox --help lists the sweeper:\n%s", out)
	}
	ref, err := os.ReadFile("../../website/content/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ref), "`fugaro mailbox sweep`") {
		t.Fatal("website/content/cli-reference.md documents fugaro mailbox sweep: the reference carries only the commands a reader may be told about, so a hidden command in it means the command is no longer hidden")
	}
	// And the hiding is the command's own field rather than an accident of the
	// tree's shape: whoever regenerates the reference must be able to see that
	// the sweeper is out on purpose.
	sweep := findSub(t, findSub(t, NewRootCmd(), "mailbox"), "sweep")
	if !sweep.Hidden {
		t.Fatal("the sweeper sits in `mailbox` and is not hidden: it is the owner's command, and every reader of the reference would be told to run it")
	}
}

// TestMailboxSweepReportsThePresenceVerdict is T7's second reading (§4.2): what
// the mailbox's stored heartbeat says about the consumer that wrote it, decided
// against the store's own clock. The verdict is a reading and not a write, and a
// mailbox with no heartbeat is the module's own `offline`/`no-heartbeat` — a
// fact about the account rather than a refusal.
func TestMailboxSweepReportsThePresenceVerdict(t *testing.T) {
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)

	// Five minutes is past the third missed interval (3 × 60 s) and short of the
	// twelfth (12 × 60 s), so the answer is the middle state rather than the
	// boundary of either.
	f.db.Set("mailbox/"+mailboxUID+"/heartbeat", map[string]any{
		"at":   now.Add(-5 * time.Minute).UnixMilli(),
		"boot": "boot-1",
	})
	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	presence := sweepJSON(t, out).Mailboxes[0].Presence
	if presence.State != "stale" || presence.Reason != "missed-intervals" {
		t.Fatalf("presence = %+v, want stale/missed-intervals: five minutes is five intervals past the default one-minute heartbeat", presence)
	}
	if presence.AgeMs == nil || *presence.AgeMs != 5*60_000 {
		t.Fatalf("age = %v, want 300000 ms read from the store's own clock", presence.AgeMs)
	}
	if presence.BootID != "boot-1" {
		t.Fatalf("boot id = %q, want the `boot` the heartbeat carries (D117's name, and the module's own)", presence.BootID)
	}

	// The same mailbox with the node gone: offline, no heartbeat, and no age —
	// which is not the same answer as an age of zero.
	f.db.Set("mailbox/"+mailboxUID+"/heartbeat", nil)
	out, _, err = execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	presence = sweepJSON(t, out).Mailboxes[0].Presence
	if presence.State != "offline" || presence.Reason != "no-heartbeat" {
		t.Fatalf("presence = %+v, want offline/no-heartbeat for a mailbox with no heartbeat", presence)
	}
	if presence.AgeMs != nil {
		t.Fatalf("age = %v, want null: a missing heartbeat has no age, and zero would read as a fresh one", *presence.AgeMs)
	}
}

// TestSweepWritesNoVerdict is §4.2's own rule, held against the whole store: a
// sweep whose only work is a verdict writes nothing at all. The mailbox has a
// heartbeat and nothing past the TTL, so the one thing the command did was
// decide what that heartbeat means — and no node, no answer and no lease
// changed.
//
// Red step (c): have the verdict written back (a `mailbox/<uid>/presence` node,
// say) and this fails on the diff.
func TestSweepWritesNoVerdict(t *testing.T) {
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	f.announce(t, "p-young", now.UnixMilli())
	f.db.Set("mailbox/"+mailboxUID+"/heartbeat", map[string]any{
		"at": now.UnixMilli(), "boot": "boot-1",
	})

	before, err := json.Marshal(f.db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	doc := sweepJSON(t, out)
	if doc.Mailboxes[0].Presence.State != "live" {
		t.Fatalf("presence = %+v, want live: this test is about a sweep that decided something", doc.Mailboxes[0].Presence)
	}
	if len(doc.Mailboxes[0].Deleted) != 0 {
		t.Fatalf("deleted = %v, want none: the verdict is the only work this sweep had", doc.Mailboxes[0].Deleted)
	}
	after, err := json.Marshal(f.db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the store after a sweep whose only work was a verdict is\n%s\nwant it exactly as it was\n%s: the verdict is reported and nothing is written for it", after, before)
	}
}

// TestSweepDeletesNoUser is M8 held against the store and against the set the
// sweep reports: a mailbox sweep deletes an expired stored prompt and this
// machine's own expired state, and it deletes no Firebase user — the `m~…` rule
// is the history sweeper's — and no node it did not name.
//
// gcpfake's IdentityToolkit keeps no record of deletions a test could read, so
// the claim is held where it can be: the user-shaped nodes are still in the
// tree, the set the command reports names no user key and no store path, and the
// fence in internal/mailbox refuses that set the moment it would name one.
//
// Red step (d): put one `m~…` user in the delete set — have the sweeper walk
// `mailbox/` looking for users, say — and this fails on the surviving nodes and
// on the reported set.
func TestSweepDeletesNoUser(t *testing.T) {
	const runUser = "r~fugaro~20261018-125859-d3c8"
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	f.announce(t, "p-old", now.Add(-mailboxSweepTTL()-time.Second).UnixMilli())
	// The users' own nodes in the tree the sweeper walks: a device's
	// configuration, a run's subtree, and a mailbox keyed by a device uid.
	f.db.Set("config/admin/devices/"+mailboxUID, map[string]any{"token": "sealed"})
	f.db.Set("fugaro/"+runUser+"/state", "running")
	f.db.Set("mailbox/"+mailboxUID+"/inbox/m-1", map[string]any{"kind": "message"})

	// A second user's mailbox, keyed by a run uid: the sweeper walks the
	// pairing it was configured with and this node is not under it.
	f.db.Set("mailbox/"+runUser+"/outbox/p-other", map[string]any{"kind": "prompt"})

	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	doc := sweepJSON(t, out)
	if got := doc.Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-old" {
		t.Fatalf("deleted = %v, want exactly the stored prompt past the TTL", got)
	}
	for _, node := range []string{
		"config/admin/devices/" + mailboxUID,
		"fugaro/" + runUser + "/state",
		"mailbox/" + mailboxUID + "/inbox/m-1",
		"mailbox/" + runUser + "/outbox/p-other",
	} {
		if f.db.Value(node) == nil {
			t.Fatalf("%s is gone from the store: a mailbox sweep deletes stored prompts and this machine's own state, never a Firebase user's nodes (M8)", node)
		}
	}
	// The set the sweep reported, read as the delete set it is.
	keys := append([]string{doc.Local.Ledger, doc.Local.Prompts}, doc.Local.LedgerIDs...)
	keys = append(keys, doc.Local.PromptIDs...)
	for _, key := range keys {
		if key == "" {
			t.Fatal("the sweep reported an empty key in its local delete set")
		}
	}
	if findings := mailbox.SweepRefusals(keys); len(findings) != 0 {
		t.Fatalf("the local delete set the command reports is one its own fence refuses: %v", findings)
	}
	// And the fence refuses the set a user-walking sweeper would have had, which
	// is what keeps the tree above intact: the rule is enforced, not merely
	// unexercised.
	for _, user := range []string{mailboxUID, runUser, "p~fugaro~20261018-125859-d3c8"} {
		if findings := mailbox.SweepRefusals([]string{user}); len(findings) == 0 {
			t.Fatalf("the fence admits the user key %s: a sweeper that reached for one would delete a Firebase user (M8)", user)
		}
	}
}

// TestSweeperNeverAnswers is T7's own statement of P6.13's rule, asserted where
// it can be: the sweeper deletes, and it never answers. It removes the expired
// stored prompt and writes nothing beside it — no outbox child, and no `acked`
// ledger record, which is what an answer is — and it hands nothing to an
// orchestrator.
func TestSweeperNeverAnswers(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := f.sweepClock(t)
	at := now.Add(-mailboxSweepTTL() - time.Second)
	f.announce(t, "p-1", at.UnixMilli())
	// An answer already in the outbox for another delivery, so the test can say
	// the sweep wrote nothing *new* rather than that the outbox is empty. That
	// delivery is consumed at the store's current time: a message old enough to
	// have expired is outside the ledger's command window too, and one it refuses
	// is no record to hold the sweep against.
	f.reply(t, "r-existing", "m-1", at.UnixMilli())
	f.seedConsumed(t, "m-1", now.Add(-time.Minute).UnixMilli())
	before := map[string]any{}
	for id, value := range f.storeOutbox(t) {
		before[id] = value
	}

	out, _, err := execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	if got := sweepJSON(t, out).Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-1" {
		t.Fatalf("deleted = %v, want the stored prompt deleted", got)
	}
	// The outbox after the sweep is the outbox before it, minus the expired
	// prompt: the sweeper answers nothing, so no child was added — and an answer
	// is the one write that would make it a second consumer.
	delete(before, "p-1")
	outbox := f.storeOutbox(t)
	if len(outbox) != len(before) {
		t.Fatalf("the outbox holds %v after the sweep, want %v: the sweeper deleted the prompt and wrote nothing beside it", outbox, before)
	}
	for id, value := range before {
		after, ok := outbox[id]
		if !ok {
			t.Fatalf("the outbox child %s is gone: the sweep's delete set is the expired stored prompt and nothing else", id)
		}
		want, _ := json.Marshal(value)
		got, _ := json.Marshal(after)
		if string(want) != string(got) {
			t.Fatalf("the outbox child %s is\n%s\nwant it untouched\n%s", id, got, want)
		}
	}
	for _, entry := range mailboxLedgerEntries(t) {
		if entry.Status == mailbox.StatusAcked {
			t.Fatalf("the ledger holds the acked record %+v after a sweep: an answer is what the phone acks, and this command answers nothing", entry)
		}
	}
	if len(*offers) != 0 {
		t.Fatalf("offers = %+v, want none: the sweeper hands nothing to an orchestrator", *offers)
	}
}

// TestMailboxSweepLetsGoOfTheConsumersExpiredState is T7's local half through
// the command: §6's delete set is the consumer's own expired state — a settled
// prompt and a spent id past the retention window — and the sweep lets go of it
// through the same two rules the consumer applies to itself, so a waiting prompt
// survives and a `--dry-run` writes nothing.
//
// The window is D118's TTL plus one sweep interval, and it is the command's own
// number rather than this test's: the state is aged against
// `mailboxPromptRetentionMs()`.
func TestMailboxSweepLetsGoOfTheConsumersExpiredState(t *testing.T) {
	const safe = "carry on"
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	past := now.UnixMilli() - mailboxPromptRetentionMs() - 1_000

	// One prompt this machine is still waiting on, one it has settled and is past
	// the window, and one id it spent long ago. The order matters: every one of
	// these writes prunes at its own `now`, so the waiting prompt goes in first
	// and the aged state is written with an aged clock.
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-waiting", At: now.UnixMilli(), Mailbox: mailboxUID, Safe: safe,
	}, now.UnixMilli())
	memory := f.promptMemory(t)
	if _, refusal, err := memory.NotePrompt(mailbox.ConfirmPrompt{
		ID: "p-settled", At: past, Mailbox: mailboxUID, Safe: safe,
	}, past); err != nil || refusal != nil {
		t.Fatalf("holding the settled prompt: refusal=%v err=%v", refusal, err)
	}
	if _, ok, err := memory.AnswerPrompt(mailboxUID, "p-settled", safe, past); err != nil || !ok {
		t.Fatalf("settling the prompt: ok=%v err=%v", ok, err)
	}
	f.seedSpent(t, mailbox.ConfirmLedgerUtterance, "u-old", past)

	// A dry run first: the same plan, and this machine's state untouched.
	beforeLocal := localState(t)
	out, _, err := execute(t, "mailbox", "sweep", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("the dry run: %v", err)
	}
	doc := sweepJSON(t, out)
	if !holdsID(doc.Local.PromptIDs, "p-settled") || !holdsID(doc.Local.PromptIDs, "u-old") {
		t.Fatalf("the dry run's plan is %v, want the settled prompt and the spent id past the window", doc.Local.PromptIDs)
	}
	if holdsID(doc.Local.PromptIDs, "p-waiting") {
		t.Fatalf("the plan names the waiting prompt (%v): dropping it would lose the safe answer it is owed", doc.Local.PromptIDs)
	}
	if doc.Local.Wrote {
		t.Fatal("the dry run reports that it wrote")
	}
	if after := localState(t); !reflect.DeepEqual(beforeLocal, after) {
		t.Fatalf("a dry run rewrote this machine's state:\n%v\nwant\n%v", after, beforeLocal)
	}

	// Then the sweep itself, and the state is what the report said.
	out, _, err = execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("the sweep: %v", err)
	}
	doc = sweepJSON(t, out)
	if !doc.Local.Wrote {
		t.Fatalf("the sweep let go of %v and reports that it wrote nothing", doc.Local.PromptIDs)
	}
	if doc.Local.Ledger == "" || doc.Local.Prompts == "" {
		t.Fatalf("the sweep named no files: %+v, want the two this machine keeps", doc.Local)
	}
	back := f.promptMemory(t)
	if _, _, held := back.Prompt(mailboxUID, "p-settled"); held {
		t.Fatal("the settled prompt survived the sweep")
	}
	if _, waiting, held := back.Prompt(mailboxUID, "p-waiting"); !held || !waiting {
		t.Fatalf("the waiting prompt did not survive the sweep: %+v", back.Prompts())
	}
	keys := append([]string{doc.Local.Ledger, doc.Local.Prompts}, doc.Local.LedgerIDs...)
	keys = append(keys, doc.Local.PromptIDs...)
	for _, key := range keys {
		if strings.HasPrefix(key, "mailbox/") {
			t.Fatalf("the local delete set names the store path %s", key)
		}
	}
	if findings := mailbox.SweepRefusals(keys); len(findings) != 0 {
		t.Fatalf("the local delete set is one the sweep's own fence refuses: %v", findings)
	}
}

// TestMailboxSweepRefusesTheConsumersStateWhileTheConsumerRuns is the review's
// finding as a case: the sweep is **not a second writer** of the ledger and the
// prompt memory.
//
// §6 names the consumer as their writer, and the consumer holds what it read in
// memory for the life of its process — so a sweep that committed a set read
// before a delivery the consumer accepted in between would drop that delivery
// from disk, and a restart would re-read the never-deleted inbox node and run
// the action a second time. Here the consumer is running (its shared lock is
// held, which is the state it puts this machine in), the state holds exactly
// what a sweep would otherwise let go of, and the sweep must therefore refuse
// its own half — reporting the process to stop — while the store half, which
// touches no file of the consumer's, still runs.
//
// The control is the last step: with the lock given back, the same state is
// swept, so the refusal above is a refusal and not an empty state passing
// quietly.
func TestMailboxSweepRefusesTheConsumersStateWhileTheConsumerRuns(t *testing.T) {
	const safe = "carry on"
	const deviceUID = "m~AAAAAAAAAAAAAAAAAAAAAA"
	f := newMailboxFixture(t, "")
	now := f.sweepClock(t)
	past := now.UnixMilli() - mailboxPromptRetentionMs() - 1_000

	// This machine's state, the way the consumer leaves it: a settled prompt and
	// a spent id past the retention window, which a sweep would let go of.
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-settled", At: past, Mailbox: mailboxUID, Safe: safe,
	}, now.UnixMilli())
	memory := f.promptMemory(t)
	if _, ok, err := memory.AnswerPrompt(mailboxUID, "p-settled", safe, past); err != nil || !ok {
		t.Fatalf("settling the prompt: ok=%v err=%v", ok, err)
	}
	f.seedSpent(t, mailbox.ConfirmLedgerUtterance, "u-old", past)
	// And one stored prompt past D118's TTL, so the store half has work to do
	// whether or not the consumer is up.
	f.announce(t, "p-old", now.Add(-mailboxSweepTTL()-time.Second).UnixMilli())

	// The consumer is running: the lock it holds for the life of its process.
	held, err := mailbox.LockState(mailbox.StateLockOptions{Project: "aurora", Holder: "fugaro mailbox", Shared: true})
	if err != nil {
		t.Fatalf("taking the consumer's lock: %v", err)
	}
	beforeLocal := localState(t)

	// A dry run is a reader: it writes nothing, it takes no lock, and it says
	// the sweep would be refused rather than showing a set it will not perform.
	out, _, err := execute(t, "mailbox", "sweep", "--dry-run", "--json")
	if err == nil {
		t.Fatal("the dry run beside a running consumer exited 0, want the refusal (exit 1)")
	}
	doc := sweepJSON(t, out)
	if doc.Local.Refused == "" || !strings.Contains(doc.Local.Refused, "another fugaro mailbox is running") {
		t.Fatalf("the dry run's local half reads %+v, want the refusal naming the consumer", doc.Local)
	}
	if len(doc.Local.LedgerIDs) != 0 || len(doc.Local.PromptIDs) != 0 || doc.Local.Wrote {
		t.Fatalf("a refused local half reported a delete set: %+v", doc.Local)
	}

	// The sweep itself: its own half refused, the store's half performed.
	out, _, err = execute(t, "mailbox", "sweep", "--json")
	if err == nil {
		t.Fatal("the sweep beside a running consumer exited 0, want the refusal (exit 1)")
	}
	doc = sweepJSON(t, out)
	if doc.Local.Refused == "" {
		t.Fatalf("the refused sweep reports no refusal: %+v", doc.Local)
	}
	if len(doc.Local.LedgerIDs) != 0 || len(doc.Local.PromptIDs) != 0 || doc.Local.Wrote {
		t.Fatalf("the refused sweep reported a delete set it must not have written: %+v", doc.Local)
	}
	if doc.Local.Ledger == "" || doc.Local.Prompts == "" {
		t.Fatalf("the refused sweep named no files: %+v, want the two it left alone", doc.Local)
	}
	if after := localState(t); !reflect.DeepEqual(beforeLocal, after) {
		t.Fatalf("the refused sweep rewrote this machine's state:\n%v\nwant\n%v", after, beforeLocal)
	}
	if got := doc.Mailboxes[0].Deleted; len(got) != 1 || got[0] != "p-old" {
		t.Fatalf("deleted = %v, want the stored prompt deleted: the store half touches no file of the consumer's, so a running consumer does not fence it", got)
	}

	// The control: with the consumer stopped, the same state is let go of.
	if err := held.Release(); err != nil {
		t.Fatalf("releasing the consumer's lock: %v", err)
	}
	out, _, err = execute(t, "mailbox", "sweep", "--json")
	if err != nil {
		t.Fatalf("the sweep after the consumer stopped: %v", err)
	}
	doc = sweepJSON(t, out)
	if doc.Local.Refused != "" {
		t.Fatalf("the sweep after the consumer stopped is still refused: %+v", doc.Local)
	}
	if !holdsID(doc.Local.PromptIDs, "p-settled") || !holdsID(doc.Local.PromptIDs, "u-old") {
		t.Fatalf("the sweep after the consumer stopped let go of %v, want the settled prompt and the spent id", doc.Local.PromptIDs)
	}
	if _, _, held := f.promptMemory(t).Prompt(mailboxUID, "p-settled"); held {
		t.Fatal("the settled prompt survived the sweep that was allowed to run")
	}
}

// TestMailboxConsumerRefusesToStartUnderASweep is the other direction of the
// same lock: the consumer holds it shared for the life of its process, and a
// sweep holds it exclusively across its local half — so a consumer that starts
// inside that half is refused rather than let through to read a state the sweep
// is about to commit over.
func TestMailboxConsumerRefusesToStartUnderASweep(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	sweepLock, err := mailbox.LockState(mailbox.StateLockOptions{Project: "aurora", Holder: "fugaro mailbox sweep"})
	if err != nil {
		t.Fatalf("taking the sweep's lock: %v", err)
	}
	defer func() { _ = sweepLock.Release() }()

	if _, _, err := execute(t, "mailbox", "--once"); err == nil {
		t.Fatal("the consumer started while a sweep held this machine's state, want the refusal")
	} else if !strings.Contains(err.Error(), "another fugaro mailbox sweep is running") {
		t.Fatalf("the consumer's refusal reads %q, want the sweep it met", err.Error())
	}
	if len(*offers) != 0 {
		t.Fatalf("the refused consumer acted on %d deliveries, want none", len(*offers))
	}
}

// holdsID reports whether a reported id list holds one id.
func holdsID(list []string, want string) bool {
	for _, one := range list {
		if one == want {
			return true
		}
	}
	return false
}
