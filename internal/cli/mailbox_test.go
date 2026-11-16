package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// fugaro mailbox (mobile P4.1, Task T5): the exit contract of
// docs/plans/2026-10-18-mobile-mailbox-consumer.md §4.4, tested on the
// command's own RunE, on both sides of every row — plus §4.3's document, which
// must name the project and must never carry a message's text or a key.
//
// The pass is one pass over one mailbox, so every test here drives it through
// the real command tree (`execute`), with the database the operator's own
// credential reaches replaced by internal/gcpfake and the OS keychain replaced
// by keychain.Fake (M5: no test in this repository touches a real keychain).

// mailboxKeyMaterial is what the fake keychain holds for one pairing. It is a
// phrase and not a key-shaped literal: nothing here is a secret and nothing
// here should look like one.
const mailboxKeyMaterial = "not-a-secret-this-is-a-test-pairing-key"

// mailboxUID is the pairing's own key — the `m~` device uid `fugaro pair`
// writes, which is the node every mailbox path is built under. 22 base64url
// characters behind the prefix, which is the shape internal/localcfg checks.
const mailboxUID = "m~AAAAAAAAAAAAAAAAAAAAAA"

type mailboxFixture struct {
	*budgetFixture
	keys *keychain.Fake
	item string
}

// newMailboxFixture is newBudgetFixture plus a mailbox block naming one
// pairing, an isolated state directory (the ledger is per project, under
// $XDG_STATE_HOME) and a fake keychain holding that pairing's key.
//
// mailboxYAML replaces the block when it is non-empty, so a test can configure
// no mailbox at all or a name of the wrong shape.
func newMailboxFixture(t *testing.T, mailboxYAML string) *mailboxFixture {
	t.Helper()
	f := &mailboxFixture{budgetFixture: newBudgetFixture(t, "")}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f.item = "fugaro-mailbox-aurora-a1"
	f.keys = &keychain.Fake{Keys: map[string]string{f.item: mailboxKeyMaterial}}
	prev := mailboxKeychain
	mailboxKeychain = func() keychain.Store { return f.keys }
	t.Cleanup(func() { mailboxKeychain = prev })
	if mailboxYAML == "" {
		mailboxYAML = "mailbox:\n  keychain_item:\n    \"" + mailboxUID + "\": " + f.item + "\n"
	}
	f.appendConfig(t, mailboxYAML)
	return f
}

// writeRecord seeds one inbox child: the five fields a message has, whatever
// else it carries, and its tag — computed with key, so a record written with
// another key is a message that does not verify.
func (f *mailboxFixture) writeRecord(t *testing.T, id string, extra map[string]any, at int64, key string) {
	t.Helper()
	f.db.Set("mailbox/"+mailboxUID+"/inbox/"+id, f.signed(t, id, extra, at, key))
}

// signed is the record itself, shared by the two fixtures that need one: the
// one seeded into the inbox, and the one seeded into the ledger. Signing is a
// function of these arguments, so two calls with the same ones are the same
// record.
func (f *mailboxFixture) signed(t *testing.T, id string, extra map[string]any, at int64, key string) map[string]any {
	t.Helper()
	record := map[string]any{"id": id, "sender": mailboxUID, "text": "hello from the phone", "at": at}
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

// writeMessage is one plain message, correctly signed by the pairing's key.
func (f *mailboxFixture) writeMessage(t *testing.T, id string, at int64) {
	t.Helper()
	f.writeRecord(t, id, nil, at, mailboxKeyMaterial)
}

// writeAction is a message D128 is about: an action record asking to cancel a
// run, signed by the pairing's key, so it reaches §9's fence and nothing else
// refuses it first.
func (f *mailboxFixture) writeAction(t *testing.T, id string, at int64) {
	t.Helper()
	f.writeRecord(t, id, map[string]any{
		"kind": "action",
		"action": map[string]any{
			"id":           "act-1",
			"verb":         "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "conf-1", "at": at},
		},
	}, at, mailboxKeyMaterial)
}

// seedConsumed records a delivery as this machine's ledger already consumed it,
// which is the state a restart leaves behind: the record is in the inbox and in
// the ledger's `seen` list, so the next pass over it has nothing new to do.
//
// A duplicate is the **same** message and not merely the same id: the ledger
// refuses a known pair whose `at` differs as `id-conflict`, since the id is the
// message's identity and a second message under it is an ambiguity. So the
// record seeded here is the inbox's own, the same arguments through the same
// signer, read back exactly as the pass reads it — marshalled, and through
// mailbox.Decode.
//
// The ledger is stamped with the **database's** clock rather than this
// machine's: that is the instant the command's own pass decides at (D117), and
// a ledger whose watermark is ahead of it refuses the whole pass
// `now-went-backwards`.
func (f *mailboxFixture) seedConsumed(t *testing.T, id string, at int64) {
	t.Helper()
	ctx := context.Background()
	item, err := keychain.ParseItem(f.item, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.keys.Key(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(f.signed(t, id, nil, at, mailboxKeyMaterial))
	if err != nil {
		t.Fatal(err)
	}
	record, err := mailbox.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	message, refusal := mailbox.NewGate(key, mailbox.GateOptions{}).Verify(record)
	if refusal != nil {
		t.Fatalf("the fixture's own record does not verify (%s)", refusal.Code)
	}
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: "aurora", PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ledger.Perform(message, mailbox.KindOf(message), f.serverNowMs(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != mailbox.OutcomeActed {
		t.Fatalf("seeding the ledger answered %q, want the delivery to be new to it", result.Outcome)
	}
}

// serverNowMs is the database's own clock, which is the one the pass decides
// at: the client reads it from the last response's Date header.
func (f *mailboxFixture) serverNowMs(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	_, db, err := openBudget(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var project any
	if _, err := db.Get(ctx, "fugaro/project", &project); err != nil {
		t.Fatal(err)
	}
	now, ok := db.ServerNow()
	if !ok {
		t.Fatal("the database answered no clock, so the seeded ledger has none to be stamped with")
	}
	return now.UnixMilli()
}

// heartbeat is the node the lease lives in, as the fixture's database holds it.
func (f *mailboxFixture) heartbeat() any {
	return f.db.Value("mailbox/" + mailboxUID + "/heartbeat")
}

func mailboxNowMs() int64 { return time.Now().UTC().UnixMilli() }

// TestMailboxIntervalBelowOneSecondIsRefused is §4.4's bad-flag row, and it is
// decided before anything is opened: the store is never reached.
func TestMailboxIntervalBelowOneSecondIsRefused(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	_, _, err := execute(t, "mailbox", "--once", "--interval", "500ms")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, ExitCode(err))
	}
	if f.heartbeat() != nil {
		t.Fatal("a refused --interval still wrote a heartbeat: the flag is refused before the store is opened")
	}
}

// TestMailboxWithoutAConfiguredMailboxIsRefused: a project with no
// mailbox.keychain_item block has no pairing whose key could be read, and the
// command says so rather than looping over nothing (§4.4).
func TestMailboxWithoutAConfiguredMailboxIsRefused(t *testing.T) {
	newMailboxFixture(t, "# no mailbox block at all\n")
	_, _, err := execute(t, "mailbox", "--once")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no mailbox configured") {
		t.Fatalf("err = %v, code %d, want exit 1 naming the missing block", err, ExitCode(err))
	}
}

// TestMailboxMalformedItemNameIsRefused is §4.4's other config row: an item
// name that is not M5's shape is refused. internal/localcfg refuses it while
// the config is read, which is the same answer one step earlier, and this test
// holds the command to it.
func TestMailboxMalformedItemNameIsRefused(t *testing.T) {
	newMailboxFixture(t, "mailbox:\n  keychain_item:\n    \""+mailboxUID+"\": \"an item name with spaces\"\n")
	_, _, err := execute(t, "mailbox", "--once")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, ExitCode(err))
	}
}

// TestMailboxNoPairingKeyStopsAtStartup is §8.3's availability row and M4's
// first: the item is named and empty, so the command refuses at startup — exit
// 1, no pass run and nothing written — rather than looping over a mailbox it
// could do nothing with.
func TestMailboxNoPairingKeyStopsAtStartup(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.keys.Keys = map[string]string{}
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), f.item) {
		t.Fatalf("err = %v, want the item's name in it (the name is not a secret, M5)", err)
	}
	if out != "" {
		t.Fatalf("out = %q, want no pass reported: nothing runs after the availability check", out)
	}
	if f.heartbeat() != nil {
		t.Fatal("a heartbeat was written by a command that refused at startup")
	}
}

// TestMailboxEmptyInboxExitsZero is M12's clause 1 from the other side: a pass
// that held its lease and read nothing is a completed pass.
func TestMailboxEmptyInboxExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	out, _, err := execute(t, "mailbox", "--once")
	if err != nil {
		t.Fatalf("err = %v, want exit 0 over an empty inbox", err)
	}
	if !strings.Contains(out, "read 0") {
		t.Fatalf("out = %q, want the pass reported with nothing read", out)
	}
	if f.heartbeat() == nil {
		t.Fatal("no heartbeat was written: the empty-inbox pass still takes the lease (§7)")
	}
}

// TestMailboxWithoutAHeartbeatExitsZero: --no-heartbeat takes no lease, so the
// pass writes nothing, reads nothing and acts on nothing — and a pass that read
// nothing is clause 1 failing, which is exit 0 (§7, §4.4).
func TestMailboxWithoutAHeartbeatExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	out, _, err := execute(t, "mailbox", "--once", "--no-heartbeat")
	if err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	if !strings.Contains(out, "lease none") || !strings.Contains(out, "read 0") {
		t.Fatalf("out = %q, want a none lease and nothing read", out)
	}
	if f.heartbeat() != nil {
		t.Fatal("--no-heartbeat wrote a heartbeat")
	}
}

// TestMailboxDuplicatePassExitsZero is M12's clause 4: a `--once` pass that
// read a delivery it had already consumed refused everything it read and
// performed nothing — and is still 0, because none of those refusals was an
// action refusal. The ledger is what makes it a duplicate, so the fixture seeds
// it rather than running a first pass: a second consumer's take is refused the
// same mailbox's lease (TestMailboxASecondConsumerReadsNothing), and a pass with
// no lease reads nothing at all.
func TestMailboxDuplicatePassExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	at := mailboxNowMs()
	f.writeMessage(t, "msg-1", at)
	f.seedConsumed(t, "msg-1", at)
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a duplicate is an outcome of a completed pass", err)
	}
	doc := mailboxJSON(t, out)
	if got := mailboxCount(t, doc, "duplicates"); got != 1 {
		t.Fatalf("duplicates = %d, want 1: the ledger is what stops a restart from acting on a delivery twice", got)
	}
	if code := mailboxRefusedCode(t, doc, 0); code != "duplicate" {
		t.Fatalf("refused[0].code = %q, want duplicate", code)
	}
	if got := mailboxCount(t, doc, "delivered"); got != 0 {
		t.Fatalf("delivered = %d, want 0: a delivery the ledger has already consumed is not performed again", got)
	}
}

// TestMailboxASecondConsumerReadsNothing is §4.4's no-lease row in the shape a
// second run has: the first `--once` took the mailbox's lease and its claim is
// fresh for ten minutes, so a second consumer — a restart, a cron, another
// machine — is answered lost, reads nothing and exits 0. §7 is explicit that
// nothing is written and nothing is acted on, and the claim is what keeps two
// consumers from acting on one mailbox.
func TestMailboxASecondConsumerReadsNothing(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	if _, _, err := execute(t, "mailbox", "--once"); err == nil {
		t.Fatal("the first pass acted on nothing, so it must be M12's exit 1")
	}
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a pass that holds no lease is a completed pass", err)
	}
	doc := mailboxJSON(t, out)
	if lease, _ := doc["lease"].(map[string]any); lease["state"] != string(mailbox.LeaseLost) {
		t.Fatalf("lease = %v, want lost: the first consumer's claim is fresh", doc["lease"])
	}
	if got := mailboxCount(t, doc, "read"); got != 0 {
		t.Fatalf("read = %d, want 0: a consumer with no lease does not read the inbox", got)
	}
}

// TestMailboxHoldsTheLeaseItTook: the loop takes a mailbox's lease **once** and
// holds it, which is the module's rule — a fresh claim is refused a second take
// "whatever its holder string", so a pass that re-took the lease on every
// iteration would act on nothing until the claim went stale ten minutes later.
// Two passes of one consumer therefore both hold it, and the second one refuses
// the delivery it already consumed as a duplicate.
func TestMailboxHoldsTheLeaseItTook(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeMessage(t, "msg-1", mailboxNowMs())
	ctx := context.Background()
	lc, db, err := openBudget(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pairings, _, err := mailboxKeyring(ctx, lc)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: lc.Name, PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		t.Fatal(err)
	}
	out := &strings.Builder{}
	c := &mailboxConsumer{
		lc: lc, db: db, ledger: ledger, pairings: pairings,
		held: map[string]mailbox.LeaseRun{}, holder: "boot-hold-test", out: out,
	}
	for _, want := range []struct {
		state      mailbox.LeaseVerdict
		duplicates int
	}{{mailbox.LeaseHeld, 0}, {mailbox.LeaseHeld, 1}} {
		pass, err := c.pass(ctx, pairings[0])
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		if pass.lease.State != string(want.state) {
			t.Fatalf("pass lease = %q, want %q: a consumer holds the lease it took", pass.lease.State, want.state)
		}
		if pass.verdict.duplicates != want.duplicates {
			t.Fatalf("duplicates = %d, want %d", pass.verdict.duplicates, want.duplicates)
		}
	}
}

// TestRefusedMessageStillExitsZero is §4.4's first row, and the red step T5
// names: a per-message refusal is a **recorded outcome of a completed pass**,
// never a failure to run. The message is signed with a key that is not the
// pairing's, so it does not verify (M4a) — and clause 4 wants an *action*
// refusal, which this is not.
func TestRefusedMessageStillExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeRecord(t, "msg-1", nil, mailboxNowMs(), "a-different-key-entirely")
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a message that does not verify is an outcome, not a failure to run", err)
	}
	doc := mailboxJSON(t, out)
	if code := mailboxRefusedCode(t, doc, 0); code != "bad-mac" {
		t.Fatalf("refused[0].code = %q, want bad-mac", code)
	}
	if got := mailboxCount(t, doc, "read"); got != 1 {
		t.Fatalf("read = %d, want the refused message counted", got)
	}
}

// TestMailboxRefusedActionAloneExitsOne is §4.4's second exit-1 row and M12's
// one pass-level predicate, all four clauses at once: one message read, no
// action performed, every message read refused, and one of those refusals
// D128's. §9's fence refuses every message until the confirmation record's
// consumer lands, so this is what the operator gets for asking.
func TestMailboxRefusedActionAloneExitsOne(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, ExitCode(err))
	}
	doc := mailboxJSON(t, out)
	if code := mailboxRefusedCode(t, doc, 0); code != string(mailboxCodeActionRefused) {
		t.Fatalf("refused[0].code = %q, want %s", code, mailboxCodeActionRefused)
	}
	if got := mailboxCount(t, doc, "delivered"); got != 0 {
		t.Fatalf("delivered = %d, want 0: nothing is performed while §9's fence refuses everything", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "mailbox", "aurora", "ledger.json")); err != nil {
		t.Fatalf("ledger: %v — the record the fence refused a delivery for is still written", err)
	}
}

// TestMailboxRefusedActionWithADeliveredMessageExitsZero is the other side of
// clause 2: the same refused action, in a pass that also performed something,
// is 0 — the operator asked for one thing and the pass did another. It is T6's
// to reach with a real action; here the seam answers the delivery so the
// predicate is exercised on both sides of its second clause.
func TestMailboxRefusedActionWithADeliveredMessageExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	prev := mailboxPerform
	mailboxPerform = func(context.Context, mailboxAction) *mailbox.Refusal { return nil }
	t.Cleanup(func() { mailboxPerform = prev })

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: the pass did something else", err)
	}
	if got := mailboxCount(t, mailboxJSON(t, out), "delivered"); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
}

// TestMailboxSignerRefusedWriteExitsZero is M4's third row: the key is loaded
// and the signer errors on this value, so nothing is written for it and the
// pass continues — an outcome of a completed pass, exit 0, and not §8.3's
// startup refusal. The heartbeat write is the lease, so a heartbeat that cannot
// be signed is a lease this consumer does not take.
func TestMailboxSignerRefusedWriteExitsZero(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	prev := mailboxHeartbeatSigner
	mailboxHeartbeatSigner = func(keychain.Key) mailbox.LeaseSigner {
		return func(mailbox.Heartbeat) (string, error) {
			return "", fmt.Errorf("%w: injected for this test", mailbox.ErrUnsignable)
		}
	}
	t.Cleanup(func() { mailboxHeartbeatSigner = prev })

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a value the signer refuses is an outcome, not a failure to run", err)
	}
	doc := mailboxJSON(t, out)
	if got := mailboxCount(t, doc, "read"); got != 0 {
		t.Fatalf("read = %d, want 0: no lease, so nothing is read", got)
	}
	if lease, _ := doc["lease"].(map[string]any); lease["state"] != string(mailbox.LeaseLost) {
		t.Fatalf("lease = %v, want lost", doc["lease"])
	}
	if f.heartbeat() != nil {
		t.Fatal("an unsigned heartbeat was written: M4b says the write does not happen")
	}
	if outbox := f.db.Value("mailbox/" + mailboxUID + "/outbox"); outbox != nil {
		t.Fatal("the outbox was written by a pass that wrote nothing")
	}
}

// TestMailboxPermissionRefusalIsExitOne is §4.4's `dbErr` row: a refused write
// is adminDenied, which is a user error.
func TestMailboxPermissionRefusalIsExitOne(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.db.DenyNext(1)
	_, _, err := execute(t, "mailbox", "--once")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1", err, ExitCode(err))
	}
}

// TestMailboxUnreachableDatabaseIsExitTwo is §4.4's other row: an outage is a
// remote failure, and it is the only thing that is.
func TestMailboxUnreachableDatabaseIsExitTwo(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	_, _, err := execute(t, "mailbox", "--once")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, code %d, want exit 2", err, ExitCode(err))
	}
}

// TestMailboxJSONIsTheDocumentAndCarriesNoText: §4.3's document, and the rule
// for every field in it — the project, the lease, the counters, the ids and the
// codes. A message's text never appears, and neither does the key it was
// verified with.
func TestMailboxJSONIsTheDocumentAndCarriesNoText(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeAction(t, "msg-1", mailboxNowMs())
	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	doc := mailboxJSON(t, out)
	for _, field := range []string{"project", "mailbox", "at", "lease", "pass", "acted", "replied", "refused", "warnings"} {
		if _, ok := doc[field]; !ok {
			t.Fatalf("the document has no %q: %v", field, doc)
		}
	}
	if doc["project"] != "aurora" {
		t.Fatalf("project = %v, want aurora", doc["project"])
	}
	if doc["mailbox"] != mailboxUID {
		t.Fatalf("mailbox = %v, want the pairing's own key", doc["mailbox"])
	}
	if lease, _ := doc["lease"].(map[string]any); lease["state"] != string(mailbox.LeaseHeld) || lease["holder"] == "" {
		t.Fatalf("lease = %v, want a held lease naming its holder", doc["lease"])
	}
	if _, ok := doc["warnings"].([]any); !ok {
		t.Fatalf("warnings = %v, want a present and empty list", doc["warnings"])
	}
	for _, secret := range []string{"hello from the phone", mailboxKeyMaterial, mailboxStaleReply} {
		if strings.Contains(out, secret) {
			t.Fatalf("the document carries %q: a message's text, a key and an outbox body never appear in what this command prints", secret)
		}
	}
}

// TestMailboxPromptTtlIsTheModulesNumber is M3: the 72-hour TTL is
// shared/prompts' number, bound here rather than declared here. A change on
// either side is a failing test rather than a ledger horizon the two ends
// disagree about.
func TestMailboxPromptTtlIsTheModulesNumber(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "shared", "prompts", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	// The module writes the number as a product (`72 * 60 * 60 * 1_000`), so the
	// whole right-hand side is read and multiplied rather than its first factor.
	m := regexp.MustCompile(`PROMPT_TTL_MS\s*=\s*([0-9_]+(?:\s*\*\s*[0-9_]+)*)`).FindSubmatch(data)
	if m == nil {
		t.Fatal("shared/prompts/index.js no longer declares PROMPT_TTL_MS")
	}
	product := 1
	for _, factor := range strings.Split(string(m[1]), "*") {
		n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(factor), "_", ""))
		if err != nil {
			t.Fatalf("PROMPT_TTL_MS has a factor this test cannot read (%q): %v", factor, err)
		}
		product *= n
	}
	if got := fmt.Sprint(mailboxPromptTtlMs); got != strconv.Itoa(product) {
		t.Fatalf("mailboxPromptTtlMs = %s, want shared/prompts' %d (M3: the number is the module's)", got, product)
	}
}

// TestMailboxIsRegisteredWithItsOwnSurface: the one `root.AddCommand` line
// (plan §5) — the command is in the tree an operator runs, with §4.1's flags
// and their defaults, and it takes no arguments.
func TestMailboxIsRegisteredWithItsOwnSurface(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"mailbox"})
	if err != nil || cmd == nil || cmd.Name() != "mailbox" {
		t.Fatalf("fugaro mailbox is not in the tree: %v", err)
	}
	for _, want := range []struct {
		name string
		def  string
	}{
		{"once", "false"},
		{"interval", "5s"},
		{"json", "false"},
		{"no-heartbeat", "false"},
		{"project", ""},
		{"gcp-project", ""},
	} {
		flag := cmd.Flags().Lookup(want.name)
		if flag == nil {
			t.Fatalf("--%s is not a flag of `fugaro mailbox`", want.name)
		}
		if flag.DefValue != want.def {
			t.Errorf("--%s default = %q, want %q", want.name, flag.DefValue, want.def)
		}
	}
	if _, _, err := execute(t, "mailbox", "nonsense"); err == nil {
		t.Fatal("`fugaro mailbox nonsense` was accepted: Args is cobra.NoArgs")
	}
}

// mailboxJSON is the one document a --once --json pass prints.
func mailboxJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the pass printed no JSON document (%v): %q", err, out)
	}
	return doc
}

// mailboxCount reads one counter out of the document's pass block.
func mailboxCount(t *testing.T, doc map[string]any, field string) int {
	t.Helper()
	pass, ok := doc["pass"].(map[string]any)
	if !ok {
		t.Fatalf("the document has no pass block: %v", doc)
	}
	n, ok := pass[field].(float64)
	if !ok {
		t.Fatalf("pass.%s = %v, want a number", field, pass[field])
	}
	return int(n)
}

// mailboxRefusedCode reads the code of the i-th refusal in the document.
func mailboxRefusedCode(t *testing.T, doc map[string]any, i int) string {
	t.Helper()
	refused, ok := doc["refused"].([]any)
	if !ok || len(refused) <= i {
		t.Fatalf("the document has no refusal %d: %v", i, doc["refused"])
	}
	entry, _ := refused[i].(map[string]any)
	code, _ := entry["code"].(string)
	return code
}
