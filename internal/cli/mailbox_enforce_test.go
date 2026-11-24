package cli

// fugaro mailbox (mobile P6.7): the consumer's enforcement of the D128
// amendment, tested against the task's own verification list. §9's refusal table
// and M6's default are what these tests hold: what a phone signs is re-validated
// against `shared/confirm`'s rules (internal/mailbox's own vectors test holds the
// port to the module), what is spent is spent once, and what is refused is
// refused before anything runs.
//
// Every case here drives the real command tree — `mailbox --once` — because the
// **order** of the checks is the property under test: the MAC first, then the
// record's own validity, then the ids, and only then the run. `f.offers` and
// `f.cancels` record what the orchestrator was handed and what the CLI's own
// cancel path was asked to cancel, so "nothing runs" is a claim about a recorder
// rather than about a log line.
//
// The exit codes are M12's, and the two classes of refusal are what they turn
// on. A refusal the **gate** or the **ledger** answers — a message that does not
// verify, a record the schema refuses, a delivery outside its window — is a
// per-message refusal: the pass read something and did nothing with it, and
// clause 4 leaves the pass at 0. A refusal the **action seam** answers — a
// confirmation replayed, a target that moved, a prompt answered in free text —
// is an action refusal, and a pass whose only reading was one of those exits 1,
// because the operator asked for one thing and nothing happened.

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// mailboxOtherKey is a key that is not the pairing's: a record signed with it is
// a record this consumer cannot attribute (M4a). It is a phrase and not a
// key-shaped literal, for the same reason mailboxKeyMaterial is.
const mailboxOtherKey = "not-a-secret-either-this-is-a-key-nobody-paired"

// mailboxHeadSHA is a well-formed 40-character object name: Git's SHA-1 shape,
// which the schema accepts beside the 64-character one.
const mailboxHeadSHA = "0123456789abcdef0123456789abcdef01234567"

// mailboxItem is the keychain item the served pairing names. The fixture's own
// default mailbox block names the same string; a test that writes its own block
// spells it here.
const mailboxItem = "fugaro-mailbox-aurora-a1"

// mailboxActionRecord is one action record's `action` object, so a test varies
// only the part it is about: everything outside it — the message's id, its
// sender, its tag — is the fixture's.
func mailboxActionRecord(verb string, target map[string]any, actionID, confirmationID string, confirmationAt int64) map[string]any {
	return map[string]any{
		"id":           actionID,
		"verb":         verb,
		"target":       target,
		"confirmation": map[string]any{"id": confirmationID, "at": confirmationAt},
	}
}

// mailboxCancelAction is the record D169's cancel is: a run target, an action id
// and a confirmation a minute behind the message.
func mailboxCancelAction(actionID, confirmationID string, at int64) map[string]any {
	return mailboxActionRecord("cancel", map[string]any{"run": mailboxRunID}, actionID, confirmationID, at-mailboxConfirmationLag.Milliseconds())
}

// mailboxMergeAction is the same for a merge: a repository, a pull request and
// the head SHA read back at confirmation (D169's read-back, which the consumer
// re-reads before it merges).
func mailboxMergeAction(actionID, confirmationID string, at int64, repo string, pr int64, head string) map[string]any {
	action := mailboxActionRecord("merge", map[string]any{"repo": repo, "pr": pr}, actionID, confirmationID, at-mailboxConfirmationLag.Milliseconds())
	action["head_sha"] = head
	return action
}

// mailboxActionExtra is an action record's `extra` for the fixture's writer: the
// kind, and the record itself.
func mailboxActionExtra(action map[string]any) map[string]any {
	return map[string]any{"kind": "action", "action": action}
}

// writeRaw seeds one inbox child verbatim, tag and all: the case a test needs a
// record the fixture would not have signed — an unsigned one, or one whose
// covered fields were edited after it was signed.
func (f *mailboxFixture) writeRaw(t *testing.T, id string, record map[string]any) {
	t.Helper()
	f.db.Set("mailbox/"+mailboxUID+"/inbox/"+id, record)
}

// writeAnswer is a plain `answer` message: the kind that settles a prompt, and
// the shape D128's widening refuses as an answer to a destructive one.
func (f *mailboxFixture) writeAnswer(t *testing.T, id, replyTo, text string, at int64) {
	t.Helper()
	f.writeRecord(t, id, map[string]any{"kind": "answer", "reply_to": replyTo, "text": text}, at, mailboxKeyMaterial)
}

// holdPrompt takes one prompt into this machine's memory, the way a pass that
// announced it would, so a test can drive the answer and expiry cases without a
// producer.
func (f *mailboxFixture) holdPrompt(t *testing.T, prompt mailbox.ConfirmPrompt, at int64) {
	t.Helper()
	taken, refusal, err := f.promptMemory(t).NotePrompt(prompt, at)
	if err != nil {
		t.Fatalf("holding the prompt: %v", err)
	}
	if refusal != nil {
		t.Fatalf("holding the prompt was refused (%s)", refusal.Code)
	}
	if !taken {
		t.Fatal("the prompt was already held: the fixture's ids are not unique")
	}
}

// seedSpent records one id as already spent, which is the state a restart
// leaves: the memory's own file carries it, and the pass the command runs reads
// that file.
func (f *mailboxFixture) seedSpent(t *testing.T, kind, id string, at int64) {
	t.Helper()
	if _, err := f.promptMemory(t).Spend(kind, id, at); err != nil {
		t.Fatalf("seeding the spent %s %s: %v", kind, id, err)
	}
}

// mailboxDocs is the documents one `--json` run prints: one per pairing, in the
// config's sorted order, which is what a two-pairing run's output is.
func mailboxDocs(t *testing.T, out string) []map[string]any {
	t.Helper()
	docs := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("a pass printed no JSON document (%v): %q", err, line)
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		t.Fatalf("no pass document was printed: %q", out)
	}
	return docs
}

// mailboxDocFor is one pairing's document out of a run's output.
func mailboxDocFor(t *testing.T, docs []map[string]any, uid string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		if doc["mailbox"] == uid {
			return doc
		}
	}
	t.Fatalf("no pass document for %s: %v", uid, docs)
	return nil
}

// mailboxActedOutcome reads the outcome of the i-th entry of a document's acted
// list.
func mailboxActedOutcome(t *testing.T, doc map[string]any, i int) string {
	t.Helper()
	acted, ok := doc["acted"].([]any)
	if !ok || len(acted) <= i {
		t.Fatalf("the document has no acted entry %d: %v", i, doc["acted"])
	}
	entry, _ := acted[i].(map[string]any)
	outcome, _ := entry["outcome"].(string)
	return outcome
}

// mailboxWarnings is a document's warnings list.
func mailboxWarnings(t *testing.T, doc map[string]any) []string {
	t.Helper()
	raw, ok := doc["warnings"].([]any)
	if !ok {
		t.Fatalf("the document has no warnings list: %v", doc)
	}
	notes := make([]string, 0, len(raw))
	for _, note := range raw {
		s, ok := note.(string)
		if !ok {
			t.Fatalf("a warning is %T, want a string", note)
		}
		notes = append(notes, s)
	}
	return notes
}

// readCalls is the argv a fake command recorded, one line per invocation.
func readCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no fake command ran: %v", err)
	}
	return string(data)
}

// mailboxNothingRan is M12's exit-1 side, asserted where a test wants it: the
// pass read only refusals and one of them was an action that did not run.
func mailboxNothingRan(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want exit 1: the pass read only refusals, and one of them was an action that did not run")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, code)
	}
}

// ---------------------------------------------------------------------------
// The confirmation record: what is refused, and that nothing runs
// ---------------------------------------------------------------------------

// TestMailboxActionWithoutAConfirmationIsRefused is the task's first case: a
// record that asks for a cancel and carries no confirmation at all is refused by
// the schema's own required-field rule — every part of an action record is
// required because every part is executed — and the CLI's cancel path is never
// reached.
func TestMailboxActionWithoutAConfirmationIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	offers := f.offers(t)
	f.writeRecord(t, "msg-1", mailboxActionExtra(map[string]any{
		"id": "act-1", "verb": "cancel", "target": map[string]any{"run": mailboxRunID},
	}), mailboxNowMs(), mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a record the schema refuses is a per-message refusal", err)
	}
	doc := mailboxJSON(t, out)
	if code := mailboxRefusedCode(t, doc, 0); code != string(mailbox.CodeMissingField) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeMissingField)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("the cancel path was asked to cancel %v: a record with no confirmation runs nothing", *cancelled)
	}
	if len(*offers) != 0 || mailboxCount(t, doc, "delivered") != 0 {
		t.Fatalf("delivered %d, offers %v: a refused action record is not a delivery", mailboxCount(t, doc, "delivered"), *offers)
	}
}

// TestMailboxForgedActionRecordIsRefused is M4a and the task's forged-record
// case: an unsigned record and a record signed with another key are each
// refused, and **both are well formed in every other way** — a run target that
// resolves, an action id, a confirmation id and a confirmation time inside the
// window. That shape is D169's residual risk (the launcher's own `m~` device
// could address the host's inbox): the shape an impostor gets right, and the tag
// it cannot.
func TestMailboxForgedActionRecordIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		// key is what the record is signed with; an empty key writes a record
		// with no tag at all.
		key  string
		code string
	}{
		{name: "unsigned", code: string(mailbox.CodeMissingField)},
		{name: "signed with a key that is not the pairing's", key: mailboxOtherKey, code: string(mailbox.CodeBadMAC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMailboxFixture(t, "")
			cancelled := f.cancels(t)
			offers := f.offers(t)
			at := mailboxNowMs()
			extra := mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", at))
			var record map[string]any
			if tc.key == "" {
				record = map[string]any{"id": "msg-1", "sender": mailboxUID, "text": "hello from the phone", "at": at}
				for k, v := range extra {
					record[k] = v
				}
			} else {
				record = f.signed(t, "msg-1", extra, at, tc.key)
			}
			f.writeRaw(t, "msg-1", record)

			out, _, err := execute(t, "mailbox", "--once", "--json")
			if err != nil {
				t.Fatalf("err = %v, want exit 0: a record that does not verify is a per-message refusal", err)
			}
			if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != tc.code {
				t.Fatalf("refused[0].code = %q, want %q", code, tc.code)
			}
			if len(*cancelled) != 0 || len(*offers) != 0 {
				t.Fatalf("a forged record was acted on: cancelled %v, offers %v", *cancelled, *offers)
			}
		})
	}
}

// TestMailboxUnsignedMessageAndAnswerAreRefused is the gate's other half: a
// plain `message` and an `answer` that do not verify are refused before anything
// is done with them, so neither reaches the orchestrator as a delivery and
// neither settles a prompt.
func TestMailboxUnsignedMessageAndAnswerAreRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{ID: "p-1", At: now, Mailbox: mailboxUID}, now)
	f.writeRaw(t, "msg-1", map[string]any{"id": "msg-1", "sender": mailboxUID, "text": "hello from the phone", "at": now})
	f.writeRaw(t, "msg-2", map[string]any{
		"id": "msg-2", "sender": mailboxUID, "text": "yes please", "at": now,
		"kind": "answer", "reply_to": "p-1",
	})

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a message that does not verify is a per-message refusal", err)
	}
	doc := mailboxJSON(t, out)
	for i := 0; i < 2; i++ {
		if code := mailboxRefusedCode(t, doc, i); code != string(mailbox.CodeMissingField) {
			t.Fatalf("refused[%d].code = %q, want %s", i, code, mailbox.CodeMissingField)
		}
	}
	if got := mailboxCount(t, doc, "delivered"); got != 0 {
		t.Fatalf("delivered = %d, want 0", got)
	}
	if len(*offers) != 0 {
		t.Fatalf("the orchestrator was handed %v: an unsigned message is refused before anything is handed over", *offers)
	}
	if _, waiting, held := f.promptMemory(t).Prompt(mailboxUID, "p-1"); !held || !waiting {
		t.Fatal("the prompt was settled by an answer that did not verify")
	}
}

// TestMailboxPlainTextAnswerToADestructivePromptIsRefused is D128's widening
// after review round 2: an answer to a **destructive** prompt is accepted only
// as a MAC-verified action record with a confirmation, so a plain `answer` is
// refused with `not-a-confirmation`, the prompt is left waiting, and the pass —
// which read that one message and did nothing else — is M12's exit 1. An
// impostor's "yes" merges nothing.
func TestMailboxPlainTextAnswerToADestructivePromptIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{ID: "p-1", At: now, Mailbox: mailboxUID, Destructive: true}, now)
	f.writeAnswer(t, "msg-1", "p-1", "yes, go ahead", now)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeConfirmNotAConfirmation) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeConfirmNotAConfirmation)
	}
	if len(*offers) != 0 {
		t.Fatalf("a destructive prompt was answered from free text: %v", *offers)
	}
	if _, waiting, _ := f.promptMemory(t).Prompt(mailboxUID, "p-1"); !waiting {
		t.Fatal("the plain-text answer settled a destructive prompt: the refusal is what stops it, and the question is owed an answer still")
	}
}

// TestMailboxTamperedRecordIsRefused: the tag covers every field the record
// carries, so a record whose **target** was edited after it was signed does not
// verify — an impostor who reads a legitimate confirmation cannot point it at
// another run. Where the wrong-key case is the tag of a shape that was never
// right, this is the tag of a shape that is right and a record that is not: a
// reader of the record cannot see it, and the gate can.
func TestMailboxTamperedRecordIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	offers := f.offers(t)
	at := mailboxNowMs()
	record := f.signed(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", at)), at, mailboxKeyMaterial)
	action, _ := record["action"].(map[string]any)
	action["target"] = map[string]any{"run": "20260101-000000-aaaa"}
	f.writeRaw(t, "msg-1", record)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a tampered record is refused by the gate", err)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeBadMAC) {
		t.Fatalf("refused[0].code = %q, want %s: the target is covered by the tag", code, mailbox.CodeBadMAC)
	}
	if len(*cancelled) != 0 || len(*offers) != 0 {
		t.Fatalf("a tampered record was acted on: cancelled %v, offers %v", *cancelled, *offers)
	}
}

// TestMailboxMalformedHeadSHAIsRefused: a merge record's head SHA is a 40- or
// 64-character object name, and a value that is not one is refused by the schema
// — a merge against a head nobody read back never reaches `gh` at all.
func TestMailboxMalformedHeadSHAIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	calls := testutil.FakeCommand(t, "gh", "echo '{\"headRefOid\":\"x\"}'\n")
	at := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(
		mailboxMergeAction("act-1", "conf-1", at, "acme/app", 41, "not-a-sha")), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a head the schema refuses is a per-message refusal", err)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeBadSHA) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeBadSHA)
	}
	if data, err := os.ReadFile(calls); err == nil {
		t.Fatalf("gh ran for a head SHA the schema refuses: %q", data)
	}
	if len(*offers) != 0 {
		t.Fatalf("a refused merge was handed to the orchestrator: %v", *offers)
	}
}

// TestMailboxTamperedHeadSHAIsRefused is the head SHA's half of the same pinned
// edit as TestMailboxTamperedRecordIsRefused's target: the SHA a merge is
// confirmed against is covered by the tag, so a record whose head was changed
// after the phone signed it is refused by the gate and `gh` is never run — not
// even to read the head it would have compared against.
func TestMailboxTamperedHeadSHAIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	calls := testutil.FakeCommand(t, "gh", "echo '{\"headRefOid\":\""+mailboxHeadSHA+"\"}'\n")
	at := mailboxNowMs()
	record := f.signed(t, "msg-1", mailboxActionExtra(
		mailboxMergeAction("act-1", "conf-1", at, "acme/app", 41, mailboxHeadSHA)), at, mailboxKeyMaterial)
	action, _ := record["action"].(map[string]any)
	action["head_sha"] = strings.Repeat("f", 40)
	f.writeRaw(t, "msg-1", record)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a tampered head is refused by the gate", err)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeBadMAC) {
		t.Fatalf("refused[0].code = %q, want %s: the head SHA is covered by the tag", code, mailbox.CodeBadMAC)
	}
	if data, err := os.ReadFile(calls); err == nil {
		t.Fatalf("gh ran for a record whose head was edited after signing: %q", data)
	}
	if len(*offers) != 0 {
		t.Fatalf("a tampered merge was handed to the orchestrator: %v", *offers)
	}
}

// ---------------------------------------------------------------------------
// The ids: spent once, across a restart
// ---------------------------------------------------------------------------

// TestMailboxReplayedConfirmationIsRefused: a confirmation is spent before
// anything runs, so a second record carrying it is refused — even one whose own
// action id is new. The confirmation's id is the one that authorises *whatever*
// record carries it, so spending it first is the fail-closed half of the pair.
func TestMailboxReplayedConfirmationIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := mailboxNowMs()
	f.seedSpent(t, mailbox.ConfirmLedgerConfirmation, "conf-1", at)
	f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", at)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeConfirmReplayedConfirmation) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeConfirmReplayedConfirmation)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("a replayed confirmation reached the cancel path: %v", *cancelled)
	}
}

// TestMailboxReplayedActionIsRefused is the same record re-sent **under a new
// message id**: the delivery ledger has never seen that message — its dedupe is
// keyed by `(sender, id)` — so what stops the copy is the action id this
// machine's memory has already spent. Without that spend the second copy would
// cancel the same run twice, which is the case the ledger cannot see and the
// reason the memory exists.
func TestMailboxReplayedActionIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := mailboxNowMs()
	f.seedSpent(t, mailbox.ConfirmLedgerAction, "act-1", at)
	f.writeRecord(t, "msg-2", mailboxActionExtra(mailboxCancelAction("act-1", "conf-2", at)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeConfirmReplayedAction) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeConfirmReplayedAction)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("an action a restart had already run ran again: %v", *cancelled)
	}
}

// TestMailboxConfirmedCancelRunsTheCancelPathOnce is the accepting side of the
// fence: a validly signed, confirmed cancel is run through **this CLI's own
// cancel path**, exactly once, and the pass records which action it was — the
// document says `cancelled`, not "something happened".
func TestMailboxConfirmedCancelRunsTheCancelPathOnce(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", at)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a confirmed cancel is a completed pass", err)
	}
	if len(*cancelled) != 1 || (*cancelled)[0] != mailboxRunID {
		t.Fatalf("the cancel path was asked for %v, want exactly [%s]", *cancelled, mailboxRunID)
	}
	if got := mailboxActedOutcome(t, mailboxJSON(t, out), 0); got != mailboxOutcomeCancelled {
		t.Fatalf("acted[0].outcome = %q, want %q", got, mailboxOutcomeCancelled)
	}
	memory := f.promptMemory(t)
	if !memory.Spent(mailbox.ConfirmLedgerAction, "act-1") || !memory.Spent(mailbox.ConfirmLedgerConfirmation, "conf-1") {
		t.Fatal("the action's and the confirmation's ids are not both spent: an action that ran and was not recorded as run is one that runs again")
	}
}

// TestMailboxReplayedRecordAfterARestartIsRefused runs the whole command twice.
// The first pass cancels; the second — a fresh process over the same state,
// which is what a restart is — reads the same record again and refuses it as a
// duplicate of the delivery, and the cancel path is asked exactly once over the
// two. The memory's own answer is never needed for the second, which is the point
// of the ledger being the first fence: the record is one it has already taken.
//
// The database's clock moves past the claim's own TTL between the two, which is
// what makes the second run a restart rather than a second reader: a claim taken
// from a live process is not one this consumer may read under, so a second pass
// at the same instant is a pass that reads nothing at all (§7).
func TestMailboxReplayedRecordAfterARestartIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := time.Now().UTC().Truncate(time.Second)
	f.db.SetClock(func() time.Time { return at })
	msgAt := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", msgAt)), msgAt, mailboxKeyMaterial)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	at = at.Add(mailbox.ClaimTTL*time.Millisecond + time.Second)
	f.db.SetClock(func() time.Time { return at })
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a duplicate is a per-message refusal", err)
	}
	if len(*cancelled) != 1 {
		t.Fatalf("the cancel path was asked %v over two passes, want once: the ledger's record of the delivery is what stops the second", *cancelled)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeDuplicate) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeDuplicate)
	}
}

// ---------------------------------------------------------------------------
// The windows: D173's six hours for a command, D118's TTL for a prompt
// ---------------------------------------------------------------------------

// TestMailboxExpiredConfirmationIsRefused is D173: the six-hour window is
// measured **from the confirmation's own time**, so a confirmation the owner
// gave seven hours ago authorises nothing — while the message carrying it is
// fresh, which is the case a window measured from the message would admit.
func TestMailboxExpiredConfirmationIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := mailboxNowMs()
	old := at - 7*60*60*1_000
	f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord("cancel",
		map[string]any{"run": mailboxRunID}, "act-1", "conf-1", old)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeReplay) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeReplay)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("a confirmation from seven hours ago cancelled a run: %v", *cancelled)
	}
}

// TestMailboxTimestampsAheadOfTheClockAreRefused is the other end of both
// windows: a message whose `at` is more than the gate's five minutes ahead of
// this machine's clock is refused (`future-timestamp`, a per-message refusal),
// and so is a confirmation whose own time is — the two tolerances are the
// module's, and neither is this file's to widen.
func TestMailboxTimestampsAheadOfTheClockAreRefused(t *testing.T) {
	t.Run("the message", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		f.offers(t)
		now := mailboxNowMs()
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", now)),
			now+6*60*1_000, mailboxKeyMaterial)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		if err != nil {
			t.Fatalf("err = %v, want exit 0: a message dated ahead is a per-message refusal", err)
		}
		if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeFutureTimestamp) {
			t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeFutureTimestamp)
		}
		if len(*cancelled) != 0 {
			t.Fatal("a message dated six minutes ahead was acted on")
		}
	})

	t.Run("the confirmation", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		f.offers(t)
		at := mailboxNowMs()
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord("cancel",
			map[string]any{"run": mailboxRunID}, "act-1", "conf-1", at+6*60*1_000)), at, mailboxKeyMaterial)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		mailboxNothingRan(t, err)
		if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeFutureTimestamp) {
			t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeFutureTimestamp)
		}
		if len(*cancelled) != 0 {
			t.Fatal("a confirmation dated six minutes ahead authorised a cancel")
		}
	})

	t.Run("this machine's clock, not the store's", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		f.offers(t)
		// D117: the store's clock is the one the windows are measured with, and
		// a machine whose own clock is a few minutes slow reads the same `at` as
		// further ahead than the store does. The store here runs three minutes
		// ahead of this machine's clock, so the message is four minutes ahead of
		// the store — inside the tolerance, and a delivery to act on — and seven
		// ahead of this machine, which is the reading a gate on `time.Now` would
		// refuse it for.
		at := time.Now().UTC().Truncate(time.Second).Add(3 * time.Minute)
		f.db.SetClock(func() time.Time { return at })
		msgAt := at.UnixMilli() + 4*60*1_000
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", msgAt)),
			msgAt, mailboxKeyMaterial)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		if err != nil {
			t.Fatalf("err = %v, want exit 0: a message inside the store's tolerance is a completed pass", err)
		}
		if len(*cancelled) != 1 || (*cancelled)[0] != mailboxRunID {
			t.Fatalf("the cancel path was asked for %v, want exactly [%s]", *cancelled, mailboxRunID)
		}
		if got := mailboxActedOutcome(t, mailboxJSON(t, out), 0); got != mailboxOutcomeCancelled {
			t.Fatalf("acted[0].outcome = %q, want %q", got, mailboxOutcomeCancelled)
		}
	})
}

// TestMailboxPromptWindowIsTheTtlAndTheCommandWindowIsSixHours is D173's split,
// on one clock: an `answer` seven hours old is inside the prompt's TTL and is
// **accepted** — it settles the prompt it names — while a plain command of the
// same age is refused `replay`, because a command is judged by the six-hour
// window. A consumer that judged both by the command window would refuse the
// owner's answer to a question asked this morning.
func TestMailboxPromptWindowIsTheTtlAndTheCommandWindowIsSixHours(t *testing.T) {
	now := mailboxNowMs()
	seven := now - 7*60*60*1_000

	t.Run("a prompt's answer inside the TTL", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		offers := f.offers(t)
		f.holdPrompt(t, mailbox.ConfirmPrompt{ID: "p-1", At: now, Mailbox: mailboxUID}, now)
		f.writeAnswer(t, "msg-1", "p-1", "carry on with the plan", seven)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		if err != nil {
			t.Fatalf("err = %v, want exit 0: an answer inside the prompt's TTL is a completed pass", err)
		}
		if len(*offers) != 1 || (*offers)[0].Outcome != mailboxOfferAnswered {
			t.Fatalf("offers = %+v, want the prompt settled by the seven-hour-old answer", *offers)
		}
		if got := mailboxActedOutcome(t, mailboxJSON(t, out), 0); got != mailboxOfferAnswered {
			t.Fatalf("acted[0].outcome = %q, want %q", got, mailboxOfferAnswered)
		}
	})

	t.Run("a command outside the six-hour window", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		offers := f.offers(t)
		f.writeMessage(t, "msg-1", seven)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		if err != nil {
			t.Fatalf("err = %v, want exit 0: a command outside its window is a per-message refusal", err)
		}
		if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeReplay) {
			t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeReplay)
		}
		if len(*offers) != 0 {
			t.Fatal("a command older than its window was delivered")
		}
	})
}

// ---------------------------------------------------------------------------
// Prompts: the TTL on this consumer's own clock, and the safe answer once
// ---------------------------------------------------------------------------

// TestMailboxExpiredPromptHandsOverItsSafeAnswer is D168 as amended: the
// consumer expires the prompt on its own clock, and what the orchestrator gets
// is the prompt's **non-destructive** answer — never a destructive one, and
// never the question's own text. The pass document carries the id and the
// outcome, and §4.3's rule holds: the answer itself is never printed.
func TestMailboxExpiredPromptHandsOverItsSafeAnswer(t *testing.T) {
	const safe = "the plan's first step is enough for now"
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-1", At: now - mailboxPromptTtlMs - 1_000, Mailbox: mailboxUID,
		Destructive: true, Safe: safe,
	}, now)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: an expiry is a completed pass", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers = %+v, want exactly one", *offers)
	}
	offer := (*offers)[0]
	if offer.Outcome != mailboxOfferExpired || offer.Kind != mailboxKindPrompt || offer.Stop {
		t.Fatalf("offer = %+v, want an expired prompt carrying an answer rather than a stop", offer)
	}
	if offer.Text != safe {
		t.Fatalf("offer text = %q, want the prompt's own non-destructive answer %q", offer.Text, safe)
	}
	if strings.Contains(out, safe) {
		t.Fatal("the answer's text was printed: §4.3 says an id and an outcome appear, never a message's text")
	}
	if got := mailboxActedOutcome(t, mailboxJSON(t, out), 0); got != mailboxOfferExpired {
		t.Fatalf("acted[0].outcome = %q, want %q", got, mailboxOfferExpired)
	}
}

// TestMailboxExpiredPromptWithNoSafeAnswerTellsTheOrchestratorToStop is the
// other side of D168: a prompt that named no non-destructive answer leaves
// nothing to carry on with, so what is handed over is a stop rather than an
// answer nobody gave.
func TestMailboxExpiredPromptWithNoSafeAnswerTellsTheOrchestratorToStop(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-1", At: now - mailboxPromptTtlMs - 1_000, Mailbox: mailboxUID, Destructive: true,
	}, now)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0: an expiry is a completed pass", err)
	}
	if len(*offers) != 1 || !(*offers)[0].Stop {
		t.Fatalf("offers = %+v, want the orchestrator told to stop", *offers)
	}
	if (*offers)[0].Text != mailbox.ConfirmExpiredReply {
		t.Fatalf("offer text = %q, want %q", (*offers)[0].Text, mailbox.ConfirmExpiredReply)
	}
}

// TestMailboxExpiredPromptIsNotDeliveredAgainAfterARestart: the memory is
// committed **before** the answer is handed over, so a consumer killed in
// between — and the new process that reads the same file — does not hand the
// same answer over twice. The second run is a fresh command over the same state.
func TestMailboxExpiredPromptIsNotDeliveredAgainAfterARestart(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-1", At: now - mailboxPromptTtlMs - 1_000, Mailbox: mailboxUID, Safe: "stop after the next step",
	}, now)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers after the first pass = %d, want 1", len(*offers))
	}
	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers after a restart = %d, want 1: the expiry is owed exactly once, and the memory is what records it", len(*offers))
	}
}

// TestMailboxSweeperDeletedPromptIsStillExpiredFromTheLocalCopy is the reason
// the prompt is persisted locally at all: the owner-run sweeper (P6.13) deletes
// the **stored** prompt, and the consumer still expires the question from its own
// copy, with the answer it named. The pass here decides nothing from the store —
// the outbox node is gone before it runs.
func TestMailboxSweeperDeletedPromptIsStillExpiredFromTheLocalCopy(t *testing.T) {
	const safe = "keep going with what you have"
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	f.holdPrompt(t, mailbox.ConfirmPrompt{
		ID: "p-1", At: now - mailboxPromptTtlMs - 1_000, Mailbox: mailboxUID, Safe: safe,
	}, now)

	// The prompt as the store held it, and then deleted the way the sweeper
	// deletes a stored prompt. The node is seeded rather than announced with
	// mailbox.WriteReply because this command reads no outbox at all: what the
	// pass decides from is the local copy, and the node's whole part here is that
	// it is gone before the pass runs.
	f.db.Set("mailbox/"+mailboxUID+"/outbox/p-1", map[string]any{
		"id": "p-1", "sender": mailboxUID, "text": "may I rewrite the module?", "at": now,
		"kind": mailboxKindPrompt,
	})
	if f.db.Value("mailbox/"+mailboxUID+"/outbox/p-1") == nil {
		t.Fatal("the fixture's own announcement is not in the outbox")
	}
	f.db.Set("mailbox/"+mailboxUID+"/outbox/p-1", nil)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0: the expiry is this consumer's own", err)
	}
	if len(*offers) != 1 || (*offers)[0].Text != safe || (*offers)[0].Outcome != mailboxOfferExpired {
		t.Fatalf("offers = %+v, want the safe answer out of the local copy", *offers)
	}
}

// TestMailboxPendingPromptCapRefusesTheNextPrompt is P6.5's cap at its boundary:
// with the queue full, the next question is answered "queue full" — refused and
// reported rather than evicting a prompt the owner may still answer — and the
// queue keeps the prompts it holds.
func TestMailboxPendingPromptCapRefusesTheNextPrompt(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	now := mailboxNowMs()
	for i := 0; i < mailbox.ConfirmMaxPrompts; i++ {
		f.holdPrompt(t, mailbox.ConfirmPrompt{
			ID: "held-" + string(rune('a'+i)), At: now, Mailbox: mailboxUID,
		}, now)
	}
	prev := mailboxPromptSource
	mailboxPromptSource = func(context.Context, *mailboxConsumer, mailboxPairing) []mailboxPromptRequest {
		return []mailboxPromptRequest{{ID: "p-next", Text: "one question too many", Destructive: true}}
	}
	t.Cleanup(func() { mailboxPromptSource = prev })

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0: a queue-full answer is a completed pass", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers = %+v, want the one queue-full answer", *offers)
	}
	if offer := (*offers)[0]; offer.Outcome != mailboxOfferQueueFull || offer.Text != mailbox.ConfirmQueueFullReply {
		t.Fatalf("offer = %+v, want %q with outcome %q", offer, mailbox.ConfirmQueueFullReply, mailboxOfferQueueFull)
	}
	if got := len(f.promptMemory(t).Prompts()); got != mailbox.ConfirmMaxPrompts {
		t.Fatalf("the queue holds %d prompts, want %d: a refused prompt never evicts one the owner may still answer", got, mailbox.ConfirmMaxPrompts)
	}
}

// TestMailboxRevokedPairingDropsItsPromptsAndRefusesItsKey is `fugaro pair
// revoke` as the consumer meets it, and it is the one case with two pairings in
// the config. The revoke leaves the config entry behind and takes the key out of
// the keychain, so that pairing's pass is the one that asks for no lease and
// reads nothing — there is no key to verify a message with — and hands over the
// safe answer each of its pending prompts is owed, once. A message signed with
// the key it used to hold **is refused**: verification is per pairing, so the
// mailbox of the pairing that still has a key cannot attribute a record signed
// with the one that is gone, and the revoked pairing's own inbox is not read at
// all.
func TestMailboxRevokedPairingDropsItsPromptsAndRefusesItsKey(t *testing.T) {
	const gone = "fugaro-mailbox-aurora-gone"
	const revokedUID = "m~BBBBBBBBBBBBBBBBBBBBBB"
	yaml := "mailbox:\n  keychain_item:\n    \"" + mailboxUID + "\": \"" + mailboxItem + "\"\n    \"" + revokedUID + "\": \"" + gone + "\"\n"
	f := newMailboxFixture(t, yaml)
	f.keys.Keys = map[string]string{f.item: mailboxKeyMaterial, gone: mailboxOtherKey}
	offers := f.offers(t)
	cancelled := f.cancels(t)
	now := mailboxNowMs()
	taken, refusal, err := f.promptMemory(t).NotePrompt(mailbox.ConfirmPrompt{
		ID: "p-1", At: now, Mailbox: revokedUID, Safe: "carry on without me",
	}, now)
	if err != nil || refusal != nil || !taken {
		t.Fatalf("holding the revoked pairing's prompt: taken %v, refusal %v, err %v", taken, refusal, err)
	}
	// The key the revoked pairing used to hold, presented to both mailboxes: the
	// served one is where a replay would be attributed, and it cannot be.
	record := f.signed(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", now)), now, mailboxOtherKey)
	f.db.Set("mailbox/"+mailboxUID+"/inbox/msg-1", record)
	f.db.Set("mailbox/"+revokedUID+"/inbox/msg-1", record)

	// The revoke itself: the key is gone from this machine.
	delete(f.keys.Keys, gone)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a refusal and a dropped prompt are both completed passes", err)
	}
	docs := mailboxDocs(t, out)
	if len(docs) != 2 {
		t.Fatalf("the run printed %d pass documents, want one per pairing", len(docs))
	}
	served := mailboxDocFor(t, docs, mailboxUID)
	revokedDoc := mailboxDocFor(t, docs, revokedUID)
	if code := mailboxRefusedCode(t, served, 0); code != string(mailbox.CodeBadMAC) {
		t.Fatalf("the served pairing answered %q, want %s: a revoked pairing's key is not one this consumer attributes a message to", code, mailbox.CodeBadMAC)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("a message signed with a revoked pairing's key reached the cancel path: %v", *cancelled)
	}
	if got := mailboxCount(t, revokedDoc, "read"); got != 0 {
		t.Fatalf("read = %d on the revoked pairing, want 0: a pairing with no key reads nothing", got)
	}
	if notes := strings.Join(mailboxWarnings(t, revokedDoc), "\n"); !strings.Contains(notes, "no key at keychain item "+gone) {
		t.Fatalf("the revoked pairing's pass does not report the missing item: %v", notes)
	}
	if len(*offers) != 1 || (*offers)[0].Outcome != mailboxOfferRevoked || (*offers)[0].Text != "carry on without me" {
		t.Fatalf("offers = %+v, want the revoked pairing's prompt handed over once with its safe answer", *offers)
	}
	if _, _, held := f.promptMemory(t).Prompt(revokedUID, "p-1"); held {
		t.Fatal("the revoked pairing's prompt is still held: nobody can answer it any more")
	}
}

// ---------------------------------------------------------------------------
// Merge: D169's read-back
// ---------------------------------------------------------------------------

// mailboxFakeGH is the fake `gh` the merge tests put on PATH: it answers the head
// read with `head`, and it refuses a merge the way the real one does. `gh pr
// merge` requires one of `--merge`, `--squash` and `--rebase` when it is not
// running interactively — and this consumer's `gh` never is (M11: a buffer for
// stdout, nothing on stdin) — so a fake that accepted any argv would let a merge
// with no method pass every test here and then fail on the operator's machine,
// with the record's confirmation already spent.
func mailboxFakeGH(t *testing.T, head string) (calls string) {
	t.Helper()
	return testutil.FakeCommand(t, "gh",
		"case \"$*\" in\n"+
			"*\"pr merge\"*)\n"+
			"  case \"$*\" in\n"+
			"  *--merge*|*--squash*|*--rebase*) ;;\n"+
			"  *) echo \"--merge, --rebase, or --squash required when not running interactively\" >&2; exit 1 ;;\n"+
			"  esac\n"+
			"  ;;\n"+
			"esac\n"+
			"echo '{\"headRefOid\":\""+head+"\"}'\n")
}

// TestMailboxMergeRefusesAHeadThatMoved is D169's read-back: the consumer
// re-reads the pull request's head and refuses the merge if it differs from the
// head the confirmation named. `gh pr merge` is never reached — the recorded
// argv shows the read and nothing else — and the code is this file's own
// `head-moved` rather than a generic failure, because the operator's answer to
// it is to confirm again against a head that has not moved.
func TestMailboxMergeRefusesAHeadThatMoved(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.offers(t)
	calls := mailboxFakeGH(t, "fedcba9876543210fedcba9876543210fedcba98")
	at := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(
		mailboxMergeAction("act-1", "conf-1", at, "acme/app", 41, mailboxHeadSHA)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeHeadMoved) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeHeadMoved)
	}
	argv := readCalls(t, calls)
	if strings.Contains(argv, "pr merge") {
		t.Fatalf("the merge ran against a head that moved: %q", argv)
	}
	if !strings.Contains(argv, "pr view 41 --repo acme/app --json headRefOid") {
		t.Fatalf("the head was not read back as D169 says: %q", argv)
	}
}

// TestMailboxMergeRunsGHAgainstTheConfirmedHead is the accepting side: the head
// the confirmation named is the head the pull request has, so the merge runs —
// `gh pr merge <n> --repo <owner/name> --squash --match-head-commit <head>`, as
// D169 says, with the number, the repository and the flags as separate argv
// elements and no shell.
//
// The two flags are asserted, not assumed. Without a merge method `gh` refuses
// when it is not on a terminal, which is always here, and the record's
// confirmation would already have been spent by then; without
// `--match-head-commit` the read-back above and the merge are two calls, and a
// push between them is merged anyway — the exact case the read-back exists to
// refuse, left open by the gap between the two.
func TestMailboxMergeRunsGHAgainstTheConfirmedHead(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.offers(t)
	calls := mailboxFakeGH(t, mailboxHeadSHA)
	at := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(
		mailboxMergeAction("act-1", "conf-1", at, "acme/app", 41, mailboxHeadSHA)), at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a confirmed merge is a completed pass", err)
	}
	if got := mailboxActedOutcome(t, mailboxJSON(t, out), 0); got != mailboxOutcomeMerged {
		t.Fatalf("acted[0].outcome = %q, want %q", got, mailboxOutcomeMerged)
	}
	want := "pr merge 41 --repo acme/app --squash --match-head-commit " + mailboxHeadSHA
	if argv := readCalls(t, calls); !strings.Contains(argv, want) {
		t.Fatalf("the merge was run as %q, want `gh %s`", argv, want)
	}
}

// ---------------------------------------------------------------------------
// The verbs: what a confirmation cannot authorise, and what needs none
// ---------------------------------------------------------------------------

// TestMailboxLaunchNeedsNoConfirmationRecord is D162: "kick off …" is a launch,
// it travels as a plain `message`, and it is **delivered** — not refused for
// lacking a confirmation record it was never going to have. The default is
// refusal for what this consumer does not recognise (M6); a launch is what D128's
// amendment names as allowed, so free text that is not a destructive command is
// the conversation channel.
func TestMailboxLaunchNeedsNoConfirmationRecord(t *testing.T) {
	const launch = "kick off the migration on a fresh branch"
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	f.writeFreeText(t, "msg-1", launch, mailboxNowMs())

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0: a launch is delivered rather than refused", err)
	}
	if len(*offers) != 1 {
		t.Fatalf("offers = %+v, want the launch handed over", *offers)
	}
	offer := (*offers)[0]
	if offer.Text != launch || offer.Outcome != mailboxOutcomePerformed || offer.Kind != mailbox.KindMessage {
		t.Fatalf("offer = %+v, want the launch verbatim as a delivered message", offer)
	}
}

// TestMailboxDestructiveFreeTextIsRefused is §9's table asked of the
// conversation channel: the three verbs D128's amendment allows **only** with a
// typed confirmation, the verbs it refuses outright, and the word for approving
// a pull request are all refused when they arrive as text. The record that
// authorises a cancel is typed; a sentence cannot be one, so a pass whose only
// reading was one of these is M12's exit 1.
//
// The recogniser's cost is stated rather than hidden: the words are compared
// whole, so a sentence that merely *mentions* one is refused too, and the
// operator's answer is to say it again without the word. A false refusal costs a
// second sentence; a false admission merges a pull request.
//
// The forms a spoken sentence actually uses are the reason the fold exists: the
// `-ing` shape of an e-final verb drops the `e` ("merging", "deleting") and a
// short verb doubles its last consonant ("cancelling"), so a recogniser that
// only stripped the ending would read "I am merging acme/app#41" as a request it
// may pass on.
func TestMailboxDestructiveFreeTextIsRefused(t *testing.T) {
	for _, text := range []string{
		"fugaro cancel " + mailboxRunID,
		"merge acme/app#41",
		"please confirm that for me",
		"approve the pull request",
		"delete the branch and revoke the token",
		"it is merging acme/app#41",
		"I am approving the pull request",
		"it is deleting the branch",
		"we are cancelling the run",
		"it has been revoked",
	} {
		t.Run(text, func(t *testing.T) {
			f := newMailboxFixture(t, "")
			offers := f.offers(t)
			f.cancels(t)
			f.writeFreeText(t, "msg-1", text, mailboxNowMs())

			out, _, err := execute(t, "mailbox", "--once", "--json")
			mailboxNothingRan(t, err)
			if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeActionRefused) {
				t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeActionRefused)
			}
			if len(*offers) != 0 {
				t.Fatalf("%q was delivered: destructive free text is refused, never handed on", text)
			}
		})
	}
}

// TestMailboxDestructiveFreeTextInAnAnswerIsRefused is the same fence asked of
// the other kind that carries free text. An `answer` is a sentence by another
// name, and it is the path to the orchestrator that the message kind's check does
// not cover — so without this, "yes, merge acme/app#41 and delete the branch"
// reaches the orchestrator as the answer to any harmless question, which is D128's
// fence walked around. The refusal is an action refusal (M12's exit 1 when it is
// all the pass read), and the prompt is **left waiting**: what was refused is the
// sentence, not the question, so an answer that is an answer still settles it.
func TestMailboxDestructiveFreeTextInAnAnswerIsRefused(t *testing.T) {
	for _, text := range []string{
		"yes, merge acme/app#41 and delete the branch",
		"go ahead and cancel " + mailboxRunID,
	} {
		t.Run(text, func(t *testing.T) {
			f := newMailboxFixture(t, "")
			offers := f.offers(t)
			cancelled := f.cancels(t)
			now := mailboxNowMs()
			f.holdPrompt(t, mailbox.ConfirmPrompt{ID: "p-1", At: now, Mailbox: mailboxUID, Safe: "carry on"}, now)
			f.writeAnswer(t, "msg-1", "p-1", text, now)

			out, _, err := execute(t, "mailbox", "--once", "--json")
			mailboxNothingRan(t, err)
			if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeActionRefused) {
				t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeActionRefused)
			}
			if len(*offers) != 0 {
				t.Fatalf("%q reached the orchestrator as an answer: destructive free text is refused, never handed on", text)
			}
			if len(*cancelled) != 0 {
				t.Fatalf("an answer ran an action: %v", *cancelled)
			}
			if _, waiting, held := f.promptMemory(t).Prompt(mailboxUID, "p-1"); !held || !waiting {
				t.Fatal("a refused answer settled the prompt: the question is owed an answer still")
			}
		})
	}
}

// TestMailboxOtherDestructiveVerbsAreRefusedEvenConfirmed is the closed verb
// set: the kill switch and a budget write are not in `shared/mailbox-schema`'s
// ACTIONS, so a record asking for one is refused **before** any confirmation is
// considered — there is no confirmation that could authorise them. The code is
// the schema's `unknown-verb` and not one of this file's, which is what makes
// the refusal the two ends' agreement rather than this consumer's opinion, and
// it is a per-message refusal: nothing the operator asked for was even a verb.
func TestMailboxOtherDestructiveVerbsAreRefusedEvenConfirmed(t *testing.T) {
	for _, verb := range []string{"kill", "budget", "revoke", "delete"} {
		t.Run(verb, func(t *testing.T) {
			f := newMailboxFixture(t, "")
			cancelled := f.cancels(t)
			f.offers(t)
			at := mailboxNowMs()
			f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord(verb,
				map[string]any{"run": mailboxRunID}, "act-1", "conf-1", at-mailboxConfirmationLag.Milliseconds())),
				at, mailboxKeyMaterial)

			out, _, err := execute(t, "mailbox", "--once", "--json")
			if err != nil {
				t.Fatalf("err = %v, want exit 0: a verb the schema does not name is a per-message refusal", err)
			}
			if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeUnknownVerb) {
				t.Fatalf("refused[0].code = %q, want %s", code, mailbox.CodeUnknownVerb)
			}
			if len(*cancelled) != 0 {
				t.Fatalf("a %s record reached the cancel path: %v", verb, *cancelled)
			}
		})
	}
}

// TestMailboxBareConfirmationIsNotExecutable: a `confirm` record validates — it
// is one of the three verbs the schema's ACTIONS names — and this consumer runs
// nothing for it, because the action it confirms is the orchestrator's own. It
// is refused as `not-executable` rather than handed on as a sentence nobody can
// check, and it is an action refusal: the operator asked for something and
// nothing ran.
func TestMailboxBareConfirmationIsNotExecutable(t *testing.T) {
	f := newMailboxFixture(t, "")
	cancelled := f.cancels(t)
	f.offers(t)
	at := mailboxNowMs()
	f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord("confirm",
		map[string]any{"run": mailboxRunID}, "act-1", "conf-1", at-mailboxConfirmationLag.Milliseconds())),
		at, mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	mailboxNothingRan(t, err)
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeNotExecutable) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeNotExecutable)
	}
	if len(*cancelled) != 0 {
		t.Fatalf("a bare confirmation ran something: %v", *cancelled)
	}
}

// TestMailboxKindThatDoesNotTravelIsRefused: a `prompt` read out of the host's
// own inbox is a message written for the other direction, and an orchestrator
// handed one would take it for a question to answer. This consumer refuses it
// with its own code, as a per-message refusal — nothing the operator asked for
// was an action, so the exit stays 0.
func TestMailboxKindThatDoesNotTravelIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	f.writeRecord(t, "msg-1", map[string]any{"kind": "prompt", "options": []any{"yes", "no"}}, mailboxNowMs(), mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: the kind is a fact about a message, not an action refused", err)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailboxCodeUnexpectedKind) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeUnexpectedKind)
	}
	if len(*offers) != 0 {
		t.Fatalf("a host-written kind was handed to the orchestrator: %v", *offers)
	}
}

// TestMailboxStopRequestIsAnswered is the phone's stop request through this
// consumer (mobile plan task P5.1g, the owner's answer Q10(c)): the kind is one
// a phone sends, so the run switch **answers** it rather than refusing it — a
// refusal is an answer the phone never hears, and an unanswered record is one
// its own at-least-once rule keeps owing (M2), so the human's stop request would
// sit in the app as a pending utterance with nothing to say about it — and what
// the answer says is what actually happened: nothing was stopped from here,
// because there is nothing here to stop it with.
//
// The other half is what the answer does **not** claim, and it is the half Q10's
// answer (c) exists for: no run is touched, nothing is handed to the
// orchestrator, and `config/kill/**` — the enforcement side, which no client
// writes — is not written. The sentence names where the stop can be made, which
// is the prompt the schema says a stop request raises on the console or the
// laptop, and the one thing the phone can always do by itself, which is to
// revoke its own pairing. The resident orchestrator answers one it reads out of
// the same inbox with the same two halves (its consumerCarries and
// consumerStopRequest); this command's sentence is its own, as every sentence
// this file writes is.
func TestMailboxStopRequestIsAnswered(t *testing.T) {
	f := newMailboxFixture(t, "")
	offers := f.offers(t)
	f.writeRecord(t, "msg-1", map[string]any{"kind": "stop"}, mailboxNowMs(), mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a stop request is a record this consumer reads and answers, not a failure to run", err)
	}
	doc := mailboxJSON(t, out)
	if got := mailboxCount(t, doc, "read"); got != 1 {
		t.Fatalf("read = %d, want 1: a stop request is read like any other message", got)
	}
	if refused, _ := doc["refused"].([]any); len(refused) != 0 {
		t.Fatalf("refused = %v, want none: a stop request is answered rather than refused, so the phone is not left owing it", refused)
	}
	if len(*offers) != 0 {
		t.Fatalf("a stop request was handed to the orchestrator: %v: nothing this consumer can do stops a run, and an offer would hand an agent a sentence it was never meant to receive", *offers)
	}
	replied, _ := doc["replied"].([]any)
	if len(replied) != 1 {
		t.Fatalf("replied = %v, want the one answer a stop request is owed", doc["replied"])
	}
	entry, _ := replied[0].(map[string]any)
	if entry["id"] != "r-msg-1" || entry["for"] != "msg-1" {
		t.Fatalf("replied[0] = %v, want the answer to msg-1 under mailbox.ReplyID's own id", entry)
	}
	value := f.db.Value("mailbox/" + mailboxUID + "/outbox/r-msg-1")
	node, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("the outbox holds %T at r-msg-1, want the answer this consumer wrote", value)
	}
	if node["kind"] != "answer" || node["reply_to"] != "msg-1" {
		t.Fatalf("the answer = %v, want kind answer with reply_to msg-1: the outbox is where a phone reads what became of what it sent", node)
	}
	if node["mac"] == nil {
		t.Fatal("the answer carries no tag: M4b signs every outbound value, and an unsigned one is one no phone can verify")
	}
	text, _ := node["text"].(string)
	for _, want := range []string{"nothing was stopped", "no phone-writable kill switch", "revoke this device's pairing"} {
		if !strings.Contains(text, want) {
			t.Errorf("the answer to a stop request does not carry %q: %q", want, text)
		}
	}
	if strings.Contains(out, text) {
		t.Error("the document printed a message's text: §4.3 says an id may appear in this command's output and a message's text never does")
	}
}

// TestMailboxInboxKindsAndVerbsAreTheSchemas holds this file's own kind names
// and the verbs it runs to `shared/mailbox-schema`'s: the kind constants are
// bound here rather than imported because the module declares none for them, and
// a kind or a verb the schema renames would otherwise be a branch nothing
// reaches — or, for a verb, a record this consumer would try to run.
func TestMailboxInboxKindsAndVerbsAreTheSchemas(t *testing.T) {
	for _, kind := range []string{mailboxKindAction, mailboxKindAnswer, mailboxKindPrompt, mailboxKindRegistration, mailboxKindStop} {
		if !slices.Contains(mailbox.Kinds, kind) {
			t.Errorf("this file reads %q out of an inbox, and shared/mailbox-schema does not name it: %v", kind, mailbox.Kinds)
		}
	}
	for _, verb := range []string{mailbox.ConfirmVerbCancel, mailbox.ConfirmVerbMerge, mailbox.ConfirmVerbConfirm} {
		if !slices.Contains(mailbox.Actions, verb) {
			t.Errorf("this file runs %q, and shared/mailbox-schema does not name it: %v", verb, mailbox.Actions)
		}
	}
}

// ---------------------------------------------------------------------------
// The heartbeat: the agents this consumer runs (D161)
// ---------------------------------------------------------------------------

// TestMailboxHeartbeatAdvertisesItsAgents: the heartbeat carries the agents this
// consumer runs, which is what the phone reads out to list them and to default a
// bare "talk to <agent>" to (D161, D163). The field is inside the record the
// signer covers, so a list is signed with the same tag as the instant beside it.
func TestMailboxHeartbeatAdvertisesItsAgents(t *testing.T) {
	prev := mailboxAgents
	mailboxAgents = func(context.Context, *mailboxConsumer, mailboxPairing) []mailbox.Agent {
		return []mailbox.Agent{{ID: "a-1", Name: "cody"}}
	}
	t.Cleanup(func() { mailboxAgents = prev })

	f := newMailboxFixture(t, "")
	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	record, ok := f.heartbeat().(map[string]any)
	if !ok {
		t.Fatalf("the heartbeat is %T, want the object the consumer wrote", f.heartbeat())
	}
	if _, refusal := mailbox.ValidateHeartbeat(mailbox.Record(record)); refusal != nil {
		t.Fatalf("the heartbeat this consumer wrote is not one the schema accepts (%s): a signed list it would refuse is a lease no phone can read", refusal.Code)
	}
	// Compared as the wire carries it rather than as Go holds it: the list is a
	// value a phone reads, and the order inside it is part of what the tag covers.
	want, err := json.Marshal(mailbox.AgentList([]mailbox.Agent{{ID: "a-1", Name: "cody"}}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(record["agents"])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("the heartbeat's agents = %s, want %s", got, want)
	}
}

// TestMailboxHeartbeatWithoutAgentsCarriesNoField is the other side: nothing in
// this tree supplies an agent list yet, and a heartbeat with none is the shape it
// had before D161's field — not an empty list a phone would read as "this
// consumer runs no agents".
func TestMailboxHeartbeatWithoutAgentsCarriesNoField(t *testing.T) {
	f := newMailboxFixture(t, "")
	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	record, ok := f.heartbeat().(map[string]any)
	if !ok {
		t.Fatalf("the heartbeat is %T, want the object the consumer wrote", f.heartbeat())
	}
	if _, present := record["agents"]; present {
		t.Fatalf("an empty agent list was written: %v", record)
	}
}

// TestRefusalPrecedesTheSpawn is T6's second named test and its second Red step:
// the fence is evaluated **before** anything is spawned, so a refused action
// never reaches the runner.
//
// The spawn recorder is the seam the cancel path goes through, and the test
// holds both sides of it, because "never called" over a seam no pass could fill
// would pass for the wrong reason: two refused records leave it empty — one the
// schema's own closed verb set refuses before a record can even name it, and one
// that decodes, names a run and carries an action id, and is refused by the
// fence the runner sits behind — and the admitted cancel fills it exactly once.
//
// The second is the case the Red step names: everything about that record is
// what a spawn needs, so a spawn moved in front of the fence would be seen here.
func TestRefusalPrecedesTheSpawn(t *testing.T) {
	t.Run("a verb the ruling refuses is not a record at all", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		offers := f.offers(t)
		at := mailboxNowMs()
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord(
			"kill-switch", map[string]any{"run": mailboxRunID}, "act-1", "conf-1", at-mailboxConfirmationLag.Milliseconds(),
		)), at, mailboxKeyMaterial)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		if err != nil {
			t.Fatalf("err = %v, want exit 0: a record the schema refuses is a per-message refusal", err)
		}
		if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeUnknownVerb) {
			t.Fatalf("refused[0].code = %q, want %q: an action record's verb set is the ruling's three, so a refused verb cannot be written as one", code, mailbox.CodeUnknownVerb)
		}
		if len(*cancelled) != 0 {
			t.Fatalf("the runner was spawned for %v: a refused verb never reaches it", *cancelled)
		}
		if len(*offers) != 0 {
			t.Fatalf("offers = %v: a refused action record is not a delivery either", *offers)
		}
	})

	t.Run("a refused confirmation is refused before the spawn", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		offers := f.offers(t)
		at := mailboxNowMs()
		old := at - 7*60*60*1_000
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxActionRecord("cancel",
			map[string]any{"run": mailboxRunID}, "act-1", "conf-1", old)), at, mailboxKeyMaterial)

		out, _, err := execute(t, "mailbox", "--once", "--json")
		mailboxNothingRan(t, err)
		if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeReplay) {
			t.Fatalf("refused[0].code = %q, want %q", code, mailbox.CodeReplay)
		}
		if len(*cancelled) != 0 {
			t.Fatalf("the runner was spawned for %v: this record decodes, names a run and carries an action id, so what stopped it is the fence — and it must be evaluated before the spawn", *cancelled)
		}
		if len(*offers) != 0 {
			t.Fatalf("offers = %v: a refused action record is not a delivery either", *offers)
		}
	})

	t.Run("an admitted record does reach the spawn", func(t *testing.T) {
		f := newMailboxFixture(t, "")
		cancelled := f.cancels(t)
		f.offers(t)
		at := mailboxNowMs()
		f.writeRecord(t, "msg-1", mailboxActionExtra(mailboxCancelAction("act-1", "conf-1", at)), at, mailboxKeyMaterial)

		if _, _, err := execute(t, "mailbox", "--once", "--json"); err != nil {
			t.Fatalf("err = %v, want exit 0: a confirmed cancel is a completed pass", err)
		}
		if len(*cancelled) != 1 || (*cancelled)[0] != mailboxRunID {
			t.Fatalf("the runner was spawned for %v, want exactly [%s]: the recorder sees a spawn when the fence admits one", *cancelled, mailboxRunID)
		}
	})
}
