package mailbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// TestConfirmConstantsMatchTheSharedModule holds this package's copy of
// `shared/confirm`'s numbers and words to the module's own, so the two ends
// cannot drift apart quietly. Go cannot import the module, so every constant
// here is a restatement; this is the test that makes each restatement a claim
// something checks.
func TestConfirmConstantsMatchTheSharedModule(t *testing.T) {
	confirm := readSharedModule(t, "confirm")
	prompts := readSharedModule(t, "prompts")
	command := readSharedModule(t, "command")

	for _, c := range []struct {
		name  string
		expr  *regexp.Regexp
		value int64
	}{
		{"DEFAULT_CONFIRM_TIMEOUT_MS", jsConfirmDefaultTimeout, ConfirmDefaultTimeoutMs},
		{"MIN_CONFIRM_TIMEOUT_MS", jsConfirmMinTimeout, ConfirmMinTimeoutMs},
		{"MAX_CONFIRM_TIMEOUT_MS", jsConfirmMaxTimeout, ConfirmMaxTimeoutMs},
		{"DEFAULT_MAX_PENDING", jsConfirmDefaultMaxPending, ConfirmDefaultMaxPending},
		{"MIN_MAX_PENDING", jsConfirmMinMaxPending, ConfirmMinMaxPending},
		{"MAX_MAX_PENDING", jsConfirmMaxMaxPending, ConfirmMaxMaxPending},
		{"DEFAULT_MAX_LEDGER", jsConfirmDefaultMaxLedger, ConfirmDefaultMaxLedger},
		{"MIN_MAX_LEDGER", jsConfirmMinMaxLedger, ConfirmMinMaxLedger},
		{"MAX_MAX_LEDGER", jsConfirmMaxMaxLedger, ConfirmMaxMaxLedger},
		{"PR_MAX", jsConfirmPRMax, ConfirmPRMax},
		{"MAX_FORWARD_JUMP_MS", jsConfirmForwardJump, ConfirmForwardJumpMs},
	} {
		got := jsProduct(t, confirm, c.expr, c.name)
		if got != c.value {
			t.Errorf("%s is %d, the module's is %d", c.name, c.value, got)
		}
	}
	if got := jsOneString(t, confirm, jsConfirmCue, "CONFIRM_CUE"); got != ConfirmCue {
		t.Errorf("ConfirmCue is %q, the module's is %q", ConfirmCue, got)
	}

	// The module's REFUSAL table, as a set, against every code `internal/mailbox`
	// speaks. The table is the module's whole vocabulary for every input it
	// reads -- messages, actions, targets, events and states -- so what it is
	// compared with is the package's codes as a whole, not this file's alone.
	table := jsRefusalTable(t, confirm)
	for _, code := range ConfirmCodes {
		if _, ok := table[string(code)]; !ok {
			t.Errorf("this package speaks %q, which the module's REFUSAL table does not declare", code)
		}
	}
	mine := map[string]bool{}
	for _, code := range Codes {
		mine[string(code)] = true
	}
	for _, code := range LedgerCodes {
		mine[string(code)] = true
	}
	mine[string(CodeFutureTimestamp)] = true
	for _, code := range ConfirmCodes {
		mine[string(code)] = true
	}
	for _, code := range jsOnlyCodes {
		mine[code] = true
	}
	for code := range table {
		if !mine[code] {
			t.Errorf("the module refuses with %q, which this package does not speak", code)
		}
	}

	// D2786's session policy, which `shared/confirm/policy.js` owns: the two
	// values the ruling reads, their defaults (P6's `mobile.costly_modes` and
	// `mobile.biometric_on_costly`) and C2's bounds for them. Each is read out of
	// the module, so a default moved on one side fails here.
	policy := readSharedFile(t, "confirm", "policy.js")
	if got := jsList(t, policy, jsPolicyCostlyModes, "DEFAULT_COSTLY_MODES"); !sameStrings(got, ConfirmDefaultCostlyModes) {
		t.Errorf("ConfirmDefaultCostlyModes is %v, the module's DEFAULT_COSTLY_MODES is %v", ConfirmDefaultCostlyModes, got)
	}
	if m := jsPolicyBiometric.FindSubmatch(policy); m == nil {
		t.Fatal("shared/confirm/policy.js no longer declares DEFAULT_BIOMETRIC_ON_COSTLY in a shape this test reads")
	} else if got := string(m[1]) == "true"; got != ConfirmDefaultBiometricOnCostly {
		t.Errorf("ConfirmDefaultBiometricOnCostly is %v, the module's is %v", ConfirmDefaultBiometricOnCostly, got)
	}
	if got := jsProduct(t, policy, jsPolicyMaxCostlyModes, "MAX_COSTLY_MODES"); got != ConfirmMaxCostlyModes {
		t.Errorf("ConfirmMaxCostlyModes is %d, the module's is %d", ConfirmMaxCostlyModes, got)
	}
	if got := jsProduct(t, policy, jsPolicyMaxModeLength, "MAX_MODE_LENGTH"); got != ConfirmMaxModeLen {
		t.Errorf("ConfirmMaxModeLen is %d, the module's is %d", ConfirmMaxModeLen, got)
	}
	// A pending read-back may hold the destructive verbs and D2786's costly
	// start, which is the module's own composition of the two.
	if !jsConfirmPendingVerbs.Match(confirm) {
		t.Fatal("shared/confirm/index.js no longer composes PENDING_VERBS out of DESTRUCTIVE_VERBS and VERB.LAUNCH")
	}
	pending := append(append([]string{}, jsList(t, confirm, jsConfirmDestructiveVerbs, "DESTRUCTIVE_VERBS")...), ConfirmVerbLaunch)
	if !sameStrings(pending, ConfirmPendingVerbs) {
		t.Errorf("ConfirmPendingVerbs is %v, the module's PENDING_VERBS is %v", ConfirmPendingVerbs, pending)
	}
	kindBlock := jsConfirmTargetKindBlock.FindSubmatch(confirm)
	if kindBlock == nil {
		t.Fatal("shared/confirm no longer declares a TARGET_KIND table this test can read")
	}
	kinds := map[string]string{}
	for _, m := range jsRefusalEntry.FindAllSubmatch(kindBlock[1], -1) {
		kinds[string(m[1])] = string(m[2])
	}
	if got := kinds["MODE"]; got != ConfirmTargetMode {
		t.Errorf("ConfirmTargetMode is %q, the module's TARGET_KIND.MODE is %q", ConfirmTargetMode, got)
	}

	// P6.5's prompt vocabulary: the cap and the two replies. (The TTL itself is
	// internal/cli's: TestMailboxPromptTtlIsTheModulesNumber holds it.)
	if got := jsProduct(t, prompts, jsPromptsMaxPending, "DEFAULT_MAX_PENDING"); got != int64(ConfirmMaxPrompts) {
		t.Errorf("ConfirmMaxPrompts is %d, shared/prompts' DEFAULT_MAX_PENDING is %d", ConfirmMaxPrompts, got)
	}
	if got := jsOneString(t, prompts, jsPromptsQueueFull, "QUEUE_FULL_REPLY"); got != ConfirmQueueFullReply {
		t.Errorf("ConfirmQueueFullReply is %q, shared/prompts' is %q", ConfirmQueueFullReply, got)
	}
	if got := jsOneString(t, prompts, jsPromptsExpired, "EXPIRED_REPLY"); got != ConfirmExpiredReply {
		t.Errorf("ConfirmExpiredReply is %q, shared/prompts' is %q", ConfirmExpiredReply, got)
	}

	// P6.1's vocabulary, which is what "the fixed phrase" means.
	if got := jsList(t, command, jsCommandPhrases, "CONFIRMATION_PHRASES"); len(got) != 1 || got[0] != ConfirmPhrase {
		t.Errorf("ConfirmPhrase is %q, shared/command's CONFIRMATION_PHRASES is %v", ConfirmPhrase, got)
	}
	if got := jsList(t, command, jsCommandAliases, "WAKE_ALIASES"); !sameStrings(got, confirmWakeAliases) {
		t.Errorf("the wake aliases are %v, shared/command's are %v", confirmWakeAliases, got)
	}
	if got := jsProduct(t, command, jsCommandMaxUtterance, "MAX_UTTERANCE_LENGTH"); got != ConfirmMaxUtteranceLen {
		t.Errorf("ConfirmMaxUtteranceLen is %d, shared/command's is %d", ConfirmMaxUtteranceLen, got)
	}

	// The refusal the consumer's own window uses, which P1.1 owns.
	lease := readSharedModule(t, "lease")
	if m := jsLeaseMaxTimestamp.FindSubmatch(lease); m == nil {
		t.Fatal("shared/lease no longer declares MAX_TIMESTAMP in a shape this test reads")
	} else if got, ok := jsBoundValue(string(m[1])); !ok || got != TimeMax {
		t.Errorf("TimeMax is %d, shared/lease's MAX_TIMESTAMP is %d", TimeMax, got)
	}
}

// jsOnlyCodes are the module's refusals with no Go counterpart, and there are
// exactly two: `internal-error` is a thrown Proxy trap answered as a refusal, and
// `accessor-property` is a field read through a getter. Neither can happen to a
// value a Go decoder produced, so this package has no code for either -- and
// naming them here is what keeps the set comparison above exact rather than
// almost exact.
var jsOnlyCodes = []string{"accessor-property", "internal-error"}

// TestConfirmCodesAreTheModulesSet is the list on its own: no code declared
// twice, and every code this file speaks one the module's REFUSAL table declares
// under the same string. The other direction -- a module code this package does
// not speak -- is the whole-table comparison in the constants test above, which
// is where the package's codes as a whole are gathered.
func TestConfirmCodesAreTheModulesSet(t *testing.T) {
	table := jsRefusalTable(t, readSharedModule(t, "confirm"))
	mine := map[string]bool{}
	for _, code := range ConfirmCodes {
		if mine[string(code)] {
			t.Errorf("%q is declared twice in ConfirmCodes", code)
		}
		mine[string(code)] = true
	}
	for code := range mine {
		if _, ok := table[code]; !ok {
			t.Errorf("ConfirmCodes names %q, which the module's REFUSAL table does not declare", code)
		}
	}
}

// TestConfirmMemorySurvivesARestart is the point of the file: an id spent before
// a restart is spent after it, so a record re-sent under a new message id -- or
// the same one -- is refused by the second process too.
func TestConfirmMemorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	now := int64(1_700_000_000_000)
	first := openTestMemory(t, path, ConfirmMaxPrompts)

	spent, err := first.Spend(ConfirmLedgerAction, "act_1", now)
	if err != nil || !spent {
		t.Fatalf("spending a fresh action id: spent=%v err=%v", spent, err)
	}
	again, err := first.Spend(ConfirmLedgerAction, "act_1", now)
	if err != nil || again {
		t.Fatalf("spending the same action id twice: spent=%v err=%v", again, err)
	}

	second := openTestMemory(t, path, ConfirmMaxPrompts)
	if !second.Spent(ConfirmLedgerAction, "act_1") {
		t.Error("the action id spent before the restart is not remembered after it")
	}
	if second.Spent(ConfirmLedgerAction, "act_2") {
		t.Error("an id that was never spent is remembered as spent")
	}
	if got := second.Prompts(); len(got) != 0 {
		t.Errorf("a memory with no prompts answers %v", got)
	}
}

// TestConfirmMemoryPrunesOnlyWhatTheWindowAllows: a spent id is kept for the
// retention window (D118's TTL plus one sweep interval) and dropped after it,
// and a prompt still to be answered is kept however old it is -- dropping it
// would lose the safe answer it is owed.
func TestConfirmMemoryPrunesOnlyWhatTheWindowAllows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	start := int64(1_700_000_000_000)
	retention := int64(ConfirmWindowMs)
	m := openTestMemory(t, path, ConfirmMaxPrompts)

	if _, err := m.Spend(ConfirmLedgerUtterance, "utt_old", start); err != nil {
		t.Fatalf("spending: %v", err)
	}
	if _, _, err := m.NotePrompt(ConfirmPrompt{ID: "pr_pending", At: start, Mailbox: "m~a", Safe: "yes"}, start); err != nil {
		t.Fatalf("noting a prompt: %v", err)
	}
	// One millisecond inside the window the id is still spent.
	if _, err := m.Spend(ConfirmLedgerAction, "act_in", start+retention); err != nil {
		t.Fatalf("spending: %v", err)
	}
	if !m.Spent(ConfirmLedgerUtterance, "utt_old") {
		t.Error("an id inside the retention window was forgotten")
	}
	// Beyond it, it goes.
	if _, err := m.Spend(ConfirmLedgerAction, "act_out", start+retention+2); err != nil {
		t.Fatalf("spending: %v", err)
	}
	if m.Spent(ConfirmLedgerUtterance, "utt_old") {
		t.Error("an id older than the retention window is still remembered")
	}
	if !m.Spent(ConfirmLedgerAction, "act_in") {
		t.Error("an id just inside the window was forgotten when a later write pruned")
	}
	// The pending prompt is still there, and still owed its answer.
	if len(m.Prompts()) != 1 {
		t.Fatalf("a pending prompt was pruned: %v", m.Prompts())
	}
}

// TestConfirmMemoryQueueFullAtTheBoundary: the cap is a cap, and the prompt that
// would exceed it is refused with P6.5's own word.
func TestConfirmMemoryQueueFullAtTheBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	m := openTestMemory(t, path, 2)
	now := int64(1_700_000_000_000)

	for i, id := range []string{"pr_1", "pr_2"} {
		added, refusal, err := m.NotePrompt(ConfirmPrompt{ID: id, At: now, Mailbox: "m~a", Safe: "yes"}, now)
		if err != nil || refusal != nil || !added {
			t.Fatalf("prompt %d: added=%v refusal=%v err=%v", i+1, added, refusal, err)
		}
	}
	added, refusal, err := m.NotePrompt(ConfirmPrompt{ID: "pr_3", At: now, Mailbox: "m~a", Safe: "yes"}, now)
	if err != nil {
		t.Fatalf("the third prompt: %v", err)
	}
	if added || refusal == nil || refusal.Code != CodeConfirmQueueFull {
		t.Fatalf("the third prompt past a cap of two: added=%v refusal=%v", added, refusal)
	}
	// The same prompt again is not a third prompt.
	added, refusal, err = m.NotePrompt(ConfirmPrompt{ID: "pr_1", At: now, Mailbox: "m~a", Safe: "yes"}, now)
	if err != nil || refusal != nil || added {
		t.Fatalf("a prompt whose id is already known: added=%v refusal=%v err=%v", added, refusal, err)
	}
}

// TestConfirmMemorySettledPromptsDoNotFillTheQueuesCap: the cap bounds the
// prompts still to be **answered**, and a settled prompt stays in the memory
// until the retention window ends, because it is how an answer already handed
// back is known to have been handed back. So a memory that has settled a cap's
// worth of prompts still opens, and the next prompt is still admitted: a reader
// that counted every entry against the cap would refuse the file the writer had
// just written, and no later pass could repair it -- the parse gate fails before
// anything that could prune.
func TestConfirmMemorySettledPromptsDoNotFillTheQueuesCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	start := int64(1_700_000_000_000)
	capped := 2
	m := openTestMemory(t, path, capped)
	// Three prompts, each settled in turn, so the memory holds one more entry
	// than the cap while nothing is waiting.
	for i, id := range []string{"pr_1", "pr_2", "pr_3"} {
		if _, _, err := m.NotePrompt(ConfirmPrompt{ID: id, At: start + int64(i), Mailbox: "m~a", Safe: "yes"}, start+int64(i)); err != nil {
			t.Fatalf("noting %s: %v", id, err)
		}
		if _, settled, err := m.AnswerPrompt("m~a", id, "yes", start+int64(i)+1); err != nil || !settled {
			t.Fatalf("settling %s: settled=%v err=%v", id, settled, err)
		}
	}

	// A restart reads the file the writer wrote: three settled prompts, and a
	// queue of none.
	restarted := openTestMemory(t, path, capped)
	added, refusal, err := restarted.NotePrompt(ConfirmPrompt{ID: "pr_4", At: start + 10, Mailbox: "m~a", Safe: "yes"}, start+10)
	if err != nil || refusal != nil || !added {
		t.Fatalf("a prompt after a cap's worth was settled: added=%v refusal=%v err=%v", added, refusal, err)
	}
	// And the cap is still a cap on what is live: the second live prompt is
	// admitted, the third is refused.
	if _, _, err := restarted.NotePrompt(ConfirmPrompt{ID: "pr_5", At: start + 11, Mailbox: "m~a", Safe: "yes"}, start+11); err != nil {
		t.Fatalf("the second live prompt: %v", err)
	}
	added, refusal, err = restarted.NotePrompt(ConfirmPrompt{ID: "pr_6", At: start + 12, Mailbox: "m~a", Safe: "yes"}, start+12)
	if err != nil {
		t.Fatalf("the third live prompt: %v", err)
	}
	if added || refusal == nil || refusal.Code != CodeConfirmQueueFull {
		t.Fatalf("a third live prompt past a cap of two: added=%v refusal=%v", added, refusal)
	}
}

// TestConfirmMemoryExpiresAPromptExactlyOnce: the safe answer is handed back at
// the TTL, and a second pass over the same memory -- a restart, or the next
// sweep -- hands back nothing.
func TestConfirmMemoryExpiresAPromptExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	start := int64(1_700_000_000_000)
	ttl := testPromptTtlMs
	m := openTestMemory(t, path, ConfirmMaxPrompts)
	if _, _, err := m.NotePrompt(ConfirmPrompt{ID: "pr_1", At: start, Mailbox: "m~a", Safe: "yes, go ahead"}, start); err != nil {
		t.Fatalf("noting a prompt: %v", err)
	}

	// One millisecond before the TTL there is nothing to hand back.
	early, err := m.ExpirePrompts("", start+ttl-1, ttl)
	if err != nil || len(early) != 0 {
		t.Fatalf("before the TTL: expired=%v err=%v", early, err)
	}
	at, err := m.ExpirePrompts("", start+ttl, ttl)
	if err != nil {
		t.Fatalf("at the TTL: %v", err)
	}
	if len(at) != 1 || at[0].Stop || at[0].Reply != "yes, go ahead" {
		t.Fatalf("at the TTL: %v, want one non-stop answer", at)
	}
	// A second process reads the same file: the answer is not owed twice.
	restarted := openTestMemory(t, path, ConfirmMaxPrompts)
	again, err := restarted.ExpirePrompts("", start+ttl+1, ttl)
	if err != nil || len(again) != 0 {
		t.Fatalf("after a restart: expired=%v err=%v", again, err)
	}
}

// TestConfirmMemoryStopsWhenThereIsNoSafeAnswer: a destructive prompt hands back
// a stop rather than a destructive answer.
func TestConfirmMemoryStopsWhenThereIsNoSafeAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	start := int64(1_700_000_000_000)
	ttl := testPromptTtlMs
	m := openTestMemory(t, path, ConfirmMaxPrompts)
	if _, _, err := m.NotePrompt(ConfirmPrompt{ID: "pr_1", At: start, Mailbox: "m~a", Destructive: true}, start); err != nil {
		t.Fatalf("noting a prompt: %v", err)
	}
	expired, err := m.ExpirePrompts("", start+ttl, ttl)
	if err != nil {
		t.Fatalf("at the TTL: %v", err)
	}
	if len(expired) != 1 || !expired[0].Stop || expired[0].Reply != ConfirmExpiredReply {
		t.Fatalf("a destructive prompt expired to %v, want a stop", expired)
	}
}

// TestConfirmMemoryHandsBackTheSafeAnswerOfADestructivePrompt: what expiry hands
// the orchestrator is the prompt's **non-destructive** answer, and a destructive
// prompt that named one gets it too (P6.5's `expiryOf`: the stop is for the
// prompt with no safe answer, never for the one whose question was destructive).
// The alternative -- stopping every destructive prompt -- would turn a refusal
// the owner never gave into a stop the orchestrator reads as one.
func TestConfirmMemoryHandsBackTheSafeAnswerOfADestructivePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	start := int64(1_700_000_000_000)
	ttl := testPromptTtlMs
	m := openTestMemory(t, path, ConfirmMaxPrompts)
	prompt := ConfirmPrompt{ID: "pr_1", At: start, Mailbox: "m~a", Destructive: true, Safe: "leave it alone"}
	if _, _, err := m.NotePrompt(prompt, start); err != nil {
		t.Fatalf("noting a prompt: %v", err)
	}
	expired, err := m.ExpirePrompts("", start+ttl, ttl)
	if err != nil {
		t.Fatalf("at the TTL: %v", err)
	}
	if len(expired) != 1 || expired[0].Stop || expired[0].Reply != "leave it alone" {
		t.Fatalf("a destructive prompt with a non-destructive answer expired to %v, want that answer", expired)
	}
}

// TestConfirmMemoryAnswersAPromptOnce: the owner's own answer settles the prompt
// exactly once, and the second answer -- or a restart's second delivery of the
// first -- answers nothing.
func TestConfirmMemoryAnswersAPromptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	now := int64(1_700_000_000_000)
	m := openTestMemory(t, path, ConfirmMaxPrompts)
	if _, _, err := m.NotePrompt(ConfirmPrompt{ID: "pr_1", At: now, Mailbox: "m~a", Safe: "yes"}, now); err != nil {
		t.Fatalf("noting a prompt: %v", err)
	}
	settled, ok, err := m.AnswerPrompt("m~a", "pr_1", "no, not yet", now+1)
	if err != nil || !ok || settled.Reply != "no, not yet" || settled.Stop {
		t.Fatalf("answering: settled=%v ok=%v err=%v", settled, ok, err)
	}
	restarted := openTestMemory(t, path, ConfirmMaxPrompts)
	if _, ok, err := restarted.AnswerPrompt("m~a", "pr_1", "no, not yet", now+2); err != nil || ok {
		t.Fatalf("the same answer after a restart: ok=%v err=%v, want nothing to hand over", ok, err)
	}
	// An id this pairing never held is not a prompt, and neither is one of
	// another pairing's.
	if _, ok, err := restarted.AnswerPrompt("m~a", "pr_absent", "hi", now+3); err != nil || ok {
		t.Fatalf("an unknown prompt: ok=%v err=%v", ok, err)
	}
	if _, ok, err := restarted.AnswerPrompt("m~b", "pr_1", "hi", now+4); err != nil || ok {
		t.Fatalf("another pairing's prompt: ok=%v err=%v", ok, err)
	}
	// And the settled prompt is never expired a second time either.
	if expired, err := restarted.ExpirePrompts("", now+testPromptTtlMs, testPromptTtlMs); err != nil || len(expired) != 0 {
		t.Fatalf("a settled prompt expired to %v (err=%v), want nothing", expired, err)
	}
}

// TestConfirmMemoryDropsOnePairingsPrompts: `fugaro pair revoke` leaves the
// consumer to answer that pairing's prompts once and forget them, and to leave
// every other pairing's alone.
func TestConfirmMemoryDropsOnePairingsPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	now := int64(1_700_000_000_000)
	m := openTestMemory(t, path, ConfirmMaxPrompts)
	for _, p := range []ConfirmPrompt{
		{ID: "pr_a", At: now, Mailbox: "m~a", Safe: "yes"},
		{ID: "pr_b", At: now, Mailbox: "m~b", Safe: "yes"},
	} {
		if _, _, err := m.NotePrompt(p, now); err != nil {
			t.Fatalf("noting %s: %v", p.ID, err)
		}
	}
	dropped, err := m.DropPrompts("m~a", now)
	if err != nil {
		t.Fatalf("dropping: %v", err)
	}
	if len(dropped) != 1 || dropped[0].Prompt.ID != "pr_a" || dropped[0].Reply != "yes" {
		t.Fatalf("dropped %v, want pr_a's safe answer once", dropped)
	}
	left := m.Prompts()
	if len(left) != 1 || left[0].ID != "pr_b" {
		t.Fatalf("after the drop the queue is %v, want only pr_b", left)
	}
	// A second revoke owes nothing: the answer was handed back once.
	again, err := m.DropPrompts("m~a", now)
	if err != nil || len(again) != 0 {
		t.Fatalf("dropping twice: %v err=%v", again, err)
	}
}

// TestConfirmMemoryRefusesAFileItCannotTrust: forgetting what has been spent
// re-opens a replay, so a memory that is present but not one this consumer will
// read is refused rather than replaced.
func TestConfirmMemoryRefusesAFileItCannotTrust(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"not json", "{"},
		{"an unknown field", `{"now":1700000000000,"spent":[],"prompts":[],"extra":1}`},
		{"a spent id of an unknown kind", `{"now":1700000000000,"spent":[{"kind":"prompt","id":"x","at":1700000000000}],"prompts":[]}`},
		{"a duplicate spent id", `{"now":1700000000000,"spent":[{"kind":"action","id":"x","at":1700000000000},{"kind":"action","id":"x","at":1700000000000}],"prompts":[]}`},
		{"a prompt with no pairing", `{"now":1700000000000,"spent":[],"prompts":[{"id":"p","at":1700000000000}]}`},
		// The phone's own key names for its queue entry are not this file's: a
		// record that re-used `prompt`/`announcedAt` is a shape this consumer
		// does not read, and refusing it is what keeps the two from being taken
		// for one another.
		{"the queue entry's phone names", `{"now":1700000000000,"spent":[],"prompts":[{"prompt":"p","announcedAt":1700000000000,"mailbox":"m~a"}]}`},
		{"a now that is not a time", `{"now":0,"spent":[],"prompts":[]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pending.json")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			m, err := OpenConfirmMemory(ConfirmMemoryOptions{Path: path, RetentionMs: ConfirmWindowMs})
			if err == nil {
				t.Fatalf("the memory was opened as %v, want a refusal", m)
			}
		})
	}
}

// TestConfirmMemoryNeedsARetentionWindow: the caller binds D118's TTL plus one
// sweep interval, so a caller that has not is refused rather than given a
// memory that forgets on a window nobody chose.
func TestConfirmMemoryNeedsARetentionWindow(t *testing.T) {
	if _, err := OpenConfirmMemory(ConfirmMemoryOptions{Path: filepath.Join(t.TempDir(), "pending.json")}); err == nil {
		t.Fatal("a memory with no retention window was opened")
	}
}

// TestConfirmMemoryIsBesideTheLedger: the memory's path is the ledger's
// directory and the same project rule, so one project's consumer keeps its state
// in one place.
func TestConfirmMemoryIsBesideTheLedger(t *testing.T) {
	getenv := func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	}
	ledger, err := ledgerPath(getenv, "linux", "fugaro")
	if err != nil {
		t.Fatalf("the ledger path: %v", err)
	}
	memory, err := confirmMemoryPath(getenv, "linux", "fugaro")
	if err != nil {
		t.Fatalf("the memory path: %v", err)
	}
	if filepath.Dir(ledger) != filepath.Dir(memory) {
		t.Errorf("the ledger is at %s and the memory at %s, want one directory", ledger, memory)
	}
	if filepath.Base(memory) != "pending.json" {
		t.Errorf("the memory is at %s, want the consumer's own state file beside the ledger", memory)
	}
	if _, err := confirmMemoryPath(getenv, "linux", "../escape"); err == nil {
		t.Error("a project name that is a path step was accepted")
	}
}

// TestConfirmValidateActionRefusesARecordOutsideTheCommandWindow: the six-hour
// window is measured from the confirmation's own time (D173), so a record whose
// message is fresh and whose confirmation is old authorises nothing.
func TestConfirmValidateActionRefusesARecordOutsideTheCommandWindow(t *testing.T) {
	now := int64(1_700_000_000_000)
	action := func(at int64) *Action {
		return &Action{
			ID: "act_1", Verb: ConfirmVerbCancel,
			Target:       Target{Run: "20261018-125859-d3c8"},
			Confirmation: Confirmation{ID: "conf_1", At: at},
		}
	}
	if _, refusal := ConfirmValidateAction(action(now-ConfirmWindowMs), ConfirmActionOptions{Now: now}); refusal != nil {
		t.Errorf("a confirmation exactly at the window's edge was refused: %s", refusal)
	}
	if _, refusal := ConfirmValidateAction(action(now-ConfirmWindowMs-1), ConfirmActionOptions{Now: now}); refusal == nil {
		t.Error("a confirmation one millisecond past the window was honoured")
	}
	if _, refusal := ConfirmValidateAction(action(now+FutureTolerance.Milliseconds()), ConfirmActionOptions{Now: now}); refusal != nil {
		t.Errorf("a confirmation exactly at the future tolerance was refused: %s", refusal)
	}
	if _, refusal := ConfirmValidateAction(action(now+FutureTolerance.Milliseconds()+1), ConfirmActionOptions{Now: now}); refusal == nil {
		t.Error("a confirmation past the future tolerance was honoured")
	}
}

// TestConfirmValidateActionRefusesARecordWithoutAConfirmation: the record is the
// only thing that authorises a destructive verb, so one without a confirmation
// -- or with a target the verb does not take -- is refused.
func TestConfirmValidateActionRefusesARecordWithoutAConfirmation(t *testing.T) {
	now := int64(1_700_000_000_000)
	base := func() *Action {
		return &Action{
			ID: "act_1", Verb: ConfirmVerbCancel,
			Target:       Target{Run: "20261018-125859-d3c8"},
			Confirmation: Confirmation{ID: "conf_1", At: now},
		}
	}
	for _, c := range []struct {
		name   string
		change func(*Action)
		code   Code
		field  string
	}{
		{"no confirmation id", func(a *Action) { a.Confirmation.ID = "" }, CodeMissingField, "action.confirmation.id"},
		{"no confirmation time", func(a *Action) { a.Confirmation.At = 0 }, CodeBadTimestamp, "action.confirmation.at"},
		{"no action id", func(a *Action) { a.ID = "" }, CodeMissingField, "action.id"},
		// A launch is a mailbox action now (J5a, O10) and a destructive verb
		// this allow-list does not authorise: O10's own gate --
		// ConfirmValidateLaunch -- is what reads a start, so a record that
		// carries one here authorises nothing.
		{"a launch", func(a *Action) { a.Verb = ConfirmVerbLaunch }, CodeConfirmRefusedVerb, "action.verb"},
		{"the kill switch", func(a *Action) { a.Verb = "kill-switch" }, CodeConfirmRefusedVerb, "action.verb"},
		{"a verb outside the set", func(a *Action) { a.Verb = "explode" }, CodeUnknownVerb, "action.verb"},
		{"a run id that would be a flag", func(a *Action) { a.Target.Run = "-rf" }, CodeBadKey, "target.runId"},
		{"a merge's target on a cancel", func(a *Action) {
			a.Target = Target{Repo: "dimipaun/fugaro", PR: 568, IsRepo: true}
		}, CodeConfirmVerbTargetMismatch, "target.kind"},
		{"a head sha on a cancel", func(a *Action) {
			a.HeadSHA, a.HasHeadSHA = strings.Repeat("a", 40), true
		}, CodeFieldNotAllowed, "action.head_sha"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := base()
			c.change(a)
			if _, refusal := ConfirmValidateAction(a, ConfirmActionOptions{Now: now}); refusal == nil {
				t.Fatal("the record was honoured")
			} else if refusal.Code != c.code || refusal.Field != c.field {
				t.Errorf("refused %s/%s, want %s/%s", refusal.Code, refusal.Field, c.code, c.field)
			}
		})
	}
	if _, refusal := ConfirmValidateAction(nil, ConfirmActionOptions{Now: now}); refusal == nil {
		t.Error("a nil record was honoured")
	}
}

// TestConfirmValidateActionBindsTheHeadReadBack: a merge's record carries the
// head the read-back named, and that is the head the consumer compares against
// the PR's own before it merges (D169).
func TestConfirmValidateActionBindsTheHeadReadBack(t *testing.T) {
	now := int64(1_700_000_000_000)
	head := strings.Repeat("a", 40)
	a := &Action{
		ID: "act_1", Verb: ConfirmVerbMerge,
		Target:       Target{Repo: "dimipaun/fugaro", PR: 568, IsRepo: true},
		HeadSHA:      head,
		HasHeadSHA:   true,
		Confirmation: Confirmation{ID: "conf_1", At: now},
	}
	record, refusal := ConfirmValidateAction(a, ConfirmActionOptions{Now: now})
	if refusal != nil {
		t.Fatalf("a well-formed merge record was refused: %s", refusal)
	}
	if record.Target.HeadSHA != head {
		t.Errorf("the record's head is %q, want %q", record.Target.HeadSHA, head)
	}
	if record.Target.Kind != ConfirmTargetMerge || record.Target.PR != 568 || record.Target.Repo != "dimipaun/fugaro" {
		t.Errorf("the record's target is %+v", record.Target)
	}
	// A head that is not a SHA is refused rather than carried.
	a.HeadSHA = strings.Repeat("z", 40)
	if _, refusal := ConfirmValidateAction(a, ConfirmActionOptions{Now: now}); refusal == nil || refusal.Code != CodeConfirmBadHeadSHA {
		t.Errorf("a head that is not hex: %v", refusal)
	}
	// And the record in the message binding's own vocabulary names the same
	// three things, which is what the runner reads.
	back := record.Action()
	if !back.Target.IsRepo || back.Target.Repo != "dimipaun/fugaro" || back.Target.PR != 568 || !back.HasHeadSHA || back.HeadSHA != head {
		t.Errorf("the record as an action is %+v", back)
	}
}

// TestActionLaunchGateRefusesACostlyStartWithNoConfirmation is O10's red step:
// the gate this task owns refuses a start in a mode the session calls costly
// when the record carries no confirmation. A modified client -- one that
// skipped the read-back, or one that was told to -- is the case the gate exists
// for, and the modes it judges by are the session's own, so a mode the session
// does not name passes through unread.
func TestActionLaunchGateRefusesACostlyStartWithNoConfirmation(t *testing.T) {
	start := func(mode string) *Action {
		return &Action{ID: "act_1", Verb: ConfirmVerbLaunch, Mode: mode, Target: Target{Repo: "dimipaun/fugaro", IsRepo: true}}
	}
	// The default policy is P6's: `pro` and `critical` are costly, and every
	// other mode is D162's pass-through.
	for _, mode := range ConfirmDefaultCostlyModes {
		if _, refusal := ConfirmValidateLaunch(start(mode), ConfirmLaunchOptions{}); refusal == nil {
			t.Errorf("a start in %q with no confirmation was honoured", mode)
		} else if refusal.Code != CodeMissingField || refusal.Field != "action.confirmation" {
			t.Errorf("a start in %q was refused %s/%s, want %s/action.confirmation", mode, refusal.Code, refusal.Field, CodeMissingField)
		}
	}
	for _, mode := range []string{"default", "fast", "cheap"} {
		if _, refusal := ConfirmValidateLaunch(start(mode), ConfirmLaunchOptions{}); refusal != nil {
			t.Errorf("a start in %q was refused: %s (D162: only a costly mode is read back)", mode, refusal)
		}
	}
	// The session's own list decides, not the default: a mode the session added
	// is costly, and a default mode the session dropped is not.
	if _, refusal := ConfirmValidateLaunch(start("turbo"), ConfirmLaunchOptions{CostlyModes: []string{"turbo"}}); refusal == nil {
		t.Error("a session-declared costly mode was started with no confirmation")
	}
	if _, refusal := ConfirmValidateLaunch(start("pro"), ConfirmLaunchOptions{CostlyModes: []string{"turbo"}}); refusal != nil {
		t.Errorf("a mode the session does not call costly was refused: %s", refusal)
	}
	// A session that names no costly mode is the empty list, which is read as
	// what it says -- the same reading ConfirmInitialState gives it.
	if _, refusal := ConfirmValidateLaunch(start("pro"), ConfirmLaunchOptions{CostlyModes: []string{}}); refusal != nil {
		t.Errorf("an empty costly list was read as a refusal: %s", refusal)
	}
}

// TestActionLaunchGateBindsTheModeAndSpendsTheConfirmation: what the gate hands
// back is the record a consumer starts from, and the two things it has to carry
// are the mode the record named -- bound as J3's machine binds a costly start's
// target, so a caller reads one shape whatever produced it -- and the
// confirmation the phone wrote, for the caller to spend against its own memory
// before anything runs.
func TestActionLaunchGateBindsTheModeAndSpendsTheConfirmation(t *testing.T) {
	a := &Action{
		ID: "act_1", Verb: ConfirmVerbLaunch, Mode: "pro",
		Target:          Target{Repo: "dimipaun/fugaro", IsRepo: true},
		Confirmation:    Confirmation{ID: "conf_1", At: testNow},
		HasConfirmation: true,
	}
	record, refusal := ConfirmValidateLaunch(a, ConfirmLaunchOptions{})
	if refusal != nil {
		t.Fatalf("a read-back costly start was refused: %s", refusal)
	}
	if record.Verb != ConfirmVerbLaunch || record.ID != "act_1" {
		t.Errorf("the record is %+v", record)
	}
	if record.Target.Kind != ConfirmTargetMode || record.Target.Mode != "pro" {
		t.Errorf("the record's target is %+v, want the mode %q bound", record.Target, "pro")
	}
	if record.Confirmation.ID != "conf_1" || record.Confirmation.At != testNow {
		t.Errorf("the record's confirmation is %+v", record.Confirmation)
	}
	// A plain start's record carries no confirmation at all: there is nothing to
	// spend, and a zero Confirmation would be one the caller might try to.
	a.Mode, a.HasConfirmation = "default", false
	record, refusal = ConfirmValidateLaunch(a, ConfirmLaunchOptions{})
	if refusal != nil {
		t.Fatalf("a plain start was refused: %s", refusal)
	}
	if record.Confirmation != (ConfirmConfirmation{}) {
		t.Errorf("a plain start's record carries the confirmation %+v", record.Confirmation)
	}
	if record.Target.Mode != "default" {
		t.Errorf("a plain start's record binds %q", record.Target.Mode)
	}
}

// TestActionLaunchGateRefusesWhatIsNotALaunch: the gate answers one verb, and
// the destructive records are ConfirmValidateAction's. Each refuses the other's
// verbs, so a caller that hands a record to the wrong one is refused rather
// than answered with a record that authorises nothing.
func TestActionLaunchGateRefusesWhatIsNotALaunch(t *testing.T) {
	for _, verb := range []string{ConfirmVerbCancel, ConfirmVerbMerge, ConfirmVerbConfirm} {
		a := &Action{
			ID: "act_1", Verb: verb, Mode: "pro",
			Target:          Target{Run: "20261018-125859-d3c8"},
			Confirmation:    Confirmation{ID: "conf_1", At: testNow},
			HasConfirmation: true,
		}
		if _, refusal := ConfirmValidateLaunch(a, ConfirmLaunchOptions{}); refusal == nil {
			t.Errorf("a %s record was honoured by the launch gate", verb)
		} else if refusal.Code != CodeConfirmRefusedVerb || refusal.Field != "action.verb" {
			t.Errorf("a %s record was refused %s/%s, want %s/action.verb", verb, refusal.Code, refusal.Field, CodeConfirmRefusedVerb)
		}
	}
	if _, refusal := ConfirmValidateLaunch(nil, ConfirmLaunchOptions{}); refusal == nil {
		t.Error("a nil record was honoured")
	}
}

// TestActionLaunchGateRefusesAPolicyItCannotRead: the costly list is the one
// input the gate takes from outside, and a list it cannot read is refused
// rather than read as "no mode is costly". The lenient reading of a defect is
// exactly the reading that lets a costly start through with no read-back, so
// each of the module's own refusals for the list is pinned here.
func TestActionLaunchGateRefusesAPolicyItCannotRead(t *testing.T) {
	a := &Action{ID: "act_1", Verb: ConfirmVerbLaunch, Mode: "pro", Target: Target{Repo: "dimipaun/fugaro", IsRepo: true}}
	for _, c := range []struct {
		name  string
		modes []string
		code  Code
		field string
	}{
		{"a mode carrying a control character", []string{"pro\u0000"}, CodeBadCharacter, "costlyModes"},
		{"a mode past the length bound", []string{strings.Repeat("x", ConfirmMaxModeLen+1)}, CodeTooLong, "costlyModes"},
		{"an empty mode", []string{""}, CodeMissingField, "costlyModes"},
		{
			"more modes than a session may name",
			func() []string {
				modes := make([]string, ConfirmMaxCostlyModes+1)
				for i := range modes {
					modes[i] = fmt.Sprintf("m-%d", i)
				}
				return modes
			}(),
			CodeTooLong, "costlyModes",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, refusal := ConfirmValidateLaunch(a, ConfirmLaunchOptions{CostlyModes: c.modes}); refusal == nil {
				t.Fatal("an unreadable costly list was read as a policy")
			} else if refusal.Code != c.code || refusal.Field != c.field {
				t.Errorf("refused %s/%s, want %s/%s", refusal.Code, refusal.Field, c.code, c.field)
			}
		})
	}
	// The list at its own bounds is a policy like any other, and a costly mode
	// in it still needs its confirmation.
	modes := make([]string, ConfirmMaxCostlyModes)
	for i := range modes {
		modes[i] = fmt.Sprintf("m-%d", i)
	}
	if _, refusal := ConfirmValidateLaunch(a, ConfirmLaunchOptions{CostlyModes: modes}); refusal != nil {
		t.Errorf("a list at the count bound was refused: %s", refusal)
	}
	// A record whose confirmation is present but not one is refused rather than
	// handed back for a caller to spend: the gate re-reads the shape of what it
	// answers with.
	broken := &Action{
		ID: "act_1", Verb: ConfirmVerbLaunch, Mode: "pro",
		Target:          Target{Repo: "dimipaun/fugaro", IsRepo: true},
		Confirmation:    Confirmation{ID: "", At: testNow},
		HasConfirmation: true,
	}
	if _, refusal := ConfirmValidateLaunch(broken, ConfirmLaunchOptions{}); refusal == nil || refusal.Field != "action.confirmation.id" {
		t.Errorf("a confirmation with no id: %v", refusal)
	}
	broken.Confirmation = Confirmation{ID: "conf_1"}
	if _, refusal := ConfirmValidateLaunch(broken, ConfirmLaunchOptions{}); refusal == nil || refusal.Code != CodeBadTimestamp {
		t.Errorf("a confirmation with no time: %v", refusal)
	}
}

// TestConfirmRefusesAStateThatDoesNotSayWhatIsCostly: the module refuses a
// state whose `costlyModes` is missing (`missing-field/costlyModes`), and a port
// that read a nil list as "no mode is costly" would fail open on the worst
// answer: a costly start would pass through with no read-back while the phone
// asked for one. A state that *names* no costly mode is the empty list, which is
// read as what it says.
func TestConfirmRefusesAStateThatDoesNotSayWhatIsCostly(t *testing.T) {
	state, refusal := ConfirmInitialState(ConfirmOptions{Now: testNow})
	if refusal != nil {
		t.Fatalf("the empty machine was refused: %s", refusal.Code)
	}
	launch := ConfirmEvent{
		Type: ConfirmEventCommand, Now: testNow + 1000,
		UtteranceID: "utt-1", Text: "kick off the docs job in pro mode",
		Verb: ConfirmVerbLaunch, Mode: "pro", HasMode: true,
		ActionID: "act-1", HasActionID: true,
	}

	state.CostlyModes = nil
	if answer := ConfirmStep(state, launch); answer.OK || answer.Code != CodeMissingField || answer.Field != "costlyModes" {
		t.Errorf("a state with no costly mode list was answered: ok=%v code=%s field=%s, want %s/%s",
			answer.OK, answer.Code, answer.Field, CodeMissingField, "costlyModes")
	}

	// A start that is not costly is a plain start, which carries no action id:
	// it is the same event the machine passes through with no read-back.
	plain := launch
	plain.ActionID, plain.HasActionID = "", false
	state.CostlyModes = []string{}
	if answer := ConfirmStep(state, plain); !answer.OK || answer.Outcome != ConfirmOutcomePassThrough {
		t.Errorf("a session that calls no mode costly did not pass the start through: ok=%v outcome=%s code=%s",
			answer.OK, answer.Outcome, answer.Code)
	}
}

func openTestMemory(t *testing.T, path string, cap int) *ConfirmMemory {
	t.Helper()
	m, err := OpenConfirmMemory(ConfirmMemoryOptions{
		Path: path, RetentionMs: ConfirmWindowMs, MaxPrompts: cap,
	})
	if err != nil {
		t.Fatalf("opening the memory at %s: %v", path, err)
	}
	return m
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func readSharedModule(t *testing.T, name string) []byte {
	t.Helper()
	return readSharedFile(t, name, "index.js")
}

// readSharedFile reads one file of one shared module, for a value the module
// keeps in a file beside its entry point.
func readSharedFile(t *testing.T, module, file string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "shared", module, file))
	if err != nil {
		t.Fatalf("reading shared/%s/%s: %v", module, file, err)
	}
	return body
}

// jsProduct reads `export const NAME = a * b * c;` and answers the product, so a
// restated millisecond constant is held to the module's own arithmetic rather
// than to a number typed twice.
func jsProduct(t *testing.T, body []byte, pattern *regexp.Regexp, name string) int64 {
	t.Helper()
	m := pattern.FindSubmatch(body)
	if m == nil {
		t.Fatalf("shared module no longer declares %s in the shape this test reads: this package's copy has nothing to be held to", name)
	}
	product := int64(1)
	for _, factor := range m[1:] {
		n, err := strconv.ParseInt(strings.ReplaceAll(string(factor), "_", ""), 10, 64)
		if err != nil {
			t.Fatalf("reading %s from the module: %v", name, err)
		}
		product *= n
	}
	return product
}

// jsString reads `export const NAME = 'value';`.
func jsOneString(t *testing.T, body []byte, pattern *regexp.Regexp, name string) string {
	t.Helper()
	m := pattern.FindSubmatch(body)
	if m == nil {
		t.Fatalf("shared module no longer declares %s in the shape this test reads: this package's copy has nothing to be held to", name)
	}
	return string(m[1])
}

// jsList reads a one-element-per-line list of quoted strings.
func jsList(t *testing.T, body []byte, pattern *regexp.Regexp, name string) []string {
	t.Helper()
	block := pattern.FindSubmatch(body)
	if block == nil {
		t.Fatalf("shared module no longer declares %s in the shape this test reads: this package's copy has nothing to be held to", name)
	}
	out := []string{}
	for _, m := range jsString.FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	return out
}

// jsRefusalTable reads the module's REFUSAL object into a set of code -> name.
func jsRefusalTable(t *testing.T, body []byte) map[string]string {
	t.Helper()
	block := jsRefusalBlock.FindSubmatch(body)
	if block == nil {
		t.Fatal("shared/confirm no longer declares a REFUSAL table this test can read")
	}
	table := map[string]string{}
	for _, m := range jsRefusalEntry.FindAllSubmatch(block[1], -1) {
		table[string(m[2])] = string(m[1])
	}
	if len(table) == 0 {
		t.Fatal("the module's REFUSAL table read as empty, so this test would pass over nothing")
	}
	return table
}

var (
	jsConfirmDefaultTimeout    = regexp.MustCompile(`(?m)^export const DEFAULT_CONFIRM_TIMEOUT_MS = (\d+) \* (\d+);$`)
	jsConfirmMinTimeout        = regexp.MustCompile(`(?m)^export const MIN_CONFIRM_TIMEOUT_MS = (\d+) \* (\d+);$`)
	jsConfirmMaxTimeout        = regexp.MustCompile(`(?m)^export const MAX_CONFIRM_TIMEOUT_MS = (\d+) \* (\d+) \* (\d+);$`)
	jsConfirmDefaultMaxPending = regexp.MustCompile(`(?m)^export const DEFAULT_MAX_PENDING = (\d+);$`)
	jsConfirmMinMaxPending     = regexp.MustCompile(`(?m)^export const MIN_MAX_PENDING = (\d+);$`)
	jsConfirmMaxMaxPending     = regexp.MustCompile(`(?m)^export const MAX_MAX_PENDING = (\d+);$`)
	jsConfirmDefaultMaxLedger  = regexp.MustCompile(`(?m)^export const DEFAULT_MAX_LEDGER = (\d+);$`)
	jsConfirmMinMaxLedger      = regexp.MustCompile(`(?m)^export const MIN_MAX_LEDGER = (\d+);$`)
	jsConfirmMaxMaxLedger      = regexp.MustCompile(`(?m)^export const MAX_MAX_LEDGER = (\d+);$`)
	jsConfirmPRMax             = regexp.MustCompile(`(?m)^export const PR_MAX = (\d+);$`)
	jsConfirmForwardJump       = regexp.MustCompile(`(?m)^export const MAX_FORWARD_JUMP_MS = (\d+) \* (\d+) \* (\d+) \* (\d+) \* (\d+);$`)
	jsConfirmCue               = regexp.MustCompile(`(?m)^export const CONFIRM_CUE = '([^']*)';$`)
	// D2786's policy half lives in shared/confirm/policy.js, which
	// shared/confirm/index.js re-exports as its only entry point.
	jsConfirmPendingVerbs  = regexp.MustCompile(`(?m)^const PENDING_VERBS = Object\.freeze\(\[\.\.\.DESTRUCTIVE_VERBS, VERB\.LAUNCH\]\);$`)
	jsPolicyCostlyModes    = regexp.MustCompile(`(?m)^export const DEFAULT_COSTLY_MODES = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsPolicyBiometric      = regexp.MustCompile(`(?m)^export const DEFAULT_BIOMETRIC_ON_COSTLY = (true|false);$`)
	jsPolicyMaxCostlyModes = regexp.MustCompile(`(?m)^export const MAX_COSTLY_MODES = (\d+);$`)
	jsPolicyMaxModeLength  = regexp.MustCompile(`(?m)^export const MAX_MODE_LENGTH = (\d+);$`)
	jsRefusalBlock         = regexp.MustCompile(`(?s)export const REFUSAL = Object\.freeze\(\{(.*?)\n\}\);`)
	jsRefusalEntry         = regexp.MustCompile(`(?m)^\s*([A-Z_0-9]+): '([^']*)',$`)

	jsConfirmVerbBlock        = regexp.MustCompile(`(?s)export const VERB = Object\.freeze\(\{(.*?)\n\}\);`)
	jsConfirmTargetKindBlock  = regexp.MustCompile(`(?s)export const TARGET_KIND = Object\.freeze\(\{(.*?)\n\}\);`)
	jsConfirmDestructiveVerbs = regexp.MustCompile(`(?m)^export const DESTRUCTIVE_VERBS = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsConfirmPassThroughVerbs = regexp.MustCompile(`(?m)^export const PASS_THROUGH_VERBS = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsConfirmRefusedVerbs     = regexp.MustCompile(`(?ms)^export const REFUSED_VERBS = Object\.freeze\(\[([^\]]*)\]\);$`)

	jsPromptsMaxPending = regexp.MustCompile(`(?m)^export const DEFAULT_MAX_PENDING = (\d+);$`)
	jsPromptsQueueFull  = regexp.MustCompile(`(?m)^export const QUEUE_FULL_REPLY = '([^']*)';$`)
	jsPromptsExpired    = regexp.MustCompile(`(?m)^export const EXPIRED_REPLY = '([^']*)';$`)

	jsCommandPhrases      = regexp.MustCompile(`(?m)^export const CONFIRMATION_PHRASES = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsCommandAliases      = regexp.MustCompile(`(?m)^export const WAKE_ALIASES = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsCommandMaxUtterance = regexp.MustCompile(`(?m)^export const MAX_UTTERANCE_LENGTH = (\d+);$`)

	jsLeaseMaxTimestamp = regexp.MustCompile(`(?m)^export const MAX_TIMESTAMP = (.+);$`)
)

// TestDestructiveIsRefused is T6's headline test and the one its first Red step
// names: the three tables are the module's own, as sets, and every verb the
// module refuses is refused by the machine — so a verb added to the ruling and
// not to this package fails here, and removing `cancel` from the three makes it
// fail as the Red step says it should.
//
// The tables are read out of `shared/confirm/index.js` rather than restated,
// because "the refusal list is a value with a test" is the plan's own answer to
// the risk of a list that is right today and wrong later. The behavioural half
// then drives the machine: a refused verb is `refused-verb`, a verb in no table
// at all is `unknown-verb` — the closed set is why a new destructive verb is
// never silently unprotected — a pass-through verb goes through, and the three
// destructive verbs are read back rather than refused. Two of those four are the
// same statement from two sides: what is *not* in the tables is refused by
// default.
func TestDestructiveIsRefused(t *testing.T) {
	confirm := readSharedModule(t, "confirm")
	destructive := jsList(t, confirm, jsConfirmDestructiveVerbs, "DESTRUCTIVE_VERBS")
	passThrough := jsList(t, confirm, jsConfirmPassThroughVerbs, "PASS_THROUGH_VERBS")
	refused := jsList(t, confirm, jsConfirmRefusedVerbs, "REFUSED_VERBS")
	if len(destructive)+len(passThrough)+len(refused) < 15 {
		t.Fatalf("the module's three verb tables read as %d verbs in total, which is fewer than they declare: the lists are not being read", len(destructive)+len(passThrough)+len(refused))
	}

	for _, c := range []struct {
		name  string
		js    []string
		gover []string
	}{
		{"DESTRUCTIVE_VERBS", destructive, ConfirmDestructiveVerbs},
		{"PASS_THROUGH_VERBS", passThrough, ConfirmPassThroughVerbs},
		{"REFUSED_VERBS", refused, ConfirmRefusedVerbs},
	} {
		if !sameStrings(c.js, c.gover) {
			t.Errorf("%s is %v here and %v in shared/confirm: the refusal table is the ruling's, and a package that restates it at a different width is a fence with a hole in it", c.name, c.gover, c.js)
		}
	}

	// Every verb the module names is classified exactly once. A verb in two
	// tables has two answers and no stated tie-break; a verb in none is one this
	// machine would refuse as unknown, which is the safe half but not what the
	// module says about it.
	block := jsConfirmVerbBlock.FindSubmatch(confirm)
	if block == nil {
		t.Fatal("shared/confirm no longer declares a VERB table this test can read")
	}
	verbs := map[string]string{}
	for _, m := range jsRefusalEntry.FindAllSubmatch(block[1], -1) {
		verbs[string(m[2])] = string(m[1])
	}
	if len(verbs) == 0 {
		t.Fatal("the module's VERB table read as empty, so this half of the test would pass over nothing")
	}
	classified := map[string]int{}
	for _, list := range [][]string{destructive, passThrough, refused} {
		for _, verb := range list {
			classified[verb]++
		}
	}
	for verb, name := range verbs {
		switch classified[verb] {
		case 1:
		case 0:
			t.Errorf("the module declares the verb %s (%s) and no table of this package classifies it: a new destructive verb must not be silently unprotected", verb, name)
		default:
			t.Errorf("the verb %s (%s) is in %d tables: a verb with two answers has none", verb, name, classified[verb])
		}
	}

	state, refusal := ConfirmInitialState(ConfirmOptions{Now: testNow})
	if refusal != nil {
		t.Fatalf("the empty machine was refused: %s", refusal.Code)
	}
	run := ConfirmTarget{Kind: ConfirmTargetRun, RunID: "20261018-125859-d3c8"}
	merge := ConfirmTarget{Kind: ConfirmTargetMerge, Repo: "owner/name", PR: 7, HeadSHA: strings.Repeat("a", 40)}

	for _, verb := range ConfirmRefusedVerbs {
		answer := ConfirmStep(state, commandEvent(verb, &merge))
		if answer.OK || answer.Code != CodeConfirmRefusedVerb {
			t.Errorf("the verb %q is not refused: ok=%v code=%s: every destructive verb the ruling names is refused whatever the target says", verb, answer.OK, answer.Code)
		}
	}
	for _, verb := range []string{"teleport", "Cancel", "cancel "} {
		answer := ConfirmStep(state, commandEvent(verb, &run))
		if answer.OK || answer.Code != CodeUnknownVerb {
			t.Errorf("the verb %q is not refused as an unknown verb: ok=%v code=%s: the table is the closed set, which is what a new destructive verb meets", verb, answer.OK, answer.Code)
		}
	}
	for _, verb := range ConfirmPassThroughVerbs {
		answer := ConfirmStep(state, commandEvent(verb, nil))
		if !answer.OK || answer.Outcome != ConfirmOutcomePassThrough {
			t.Errorf("the verb %q does not pass through: ok=%v outcome=%s code=%s", verb, answer.OK, answer.Outcome, answer.Code)
		}
	}
	for _, c := range []struct {
		verb   string
		target ConfirmTarget
	}{
		{ConfirmVerbCancel, run},
		{ConfirmVerbConfirm, run},
		{ConfirmVerbMerge, merge},
	} {
		answer := ConfirmStep(state, commandEvent(c.verb, &c.target))
		if !answer.OK || answer.Outcome != ConfirmOutcomeReadBack || answer.Phase != ConfirmPhasePending {
			t.Errorf("the destructive verb %q is not read back: ok=%v outcome=%s phase=%s code=%s: it needs a confirmation and is admitted on the way to one", c.verb, answer.OK, answer.Outcome, answer.Phase, answer.Code)
		}
	}
}

// commandEvent is one command utterance carrying a verb and a target, with the
// ids the machine needs. A synthetic one and not a record: what is under test is
// the verb table, and a record would drag the confirmation and the head clock
// into a test that is not about them.
//
// The text is deliberately wordless about the verb. A transcript carrying the
// confirming phrase is refused `command-and-confirm` before the verb is read --
// D47's rule, so one misrecognition cannot satisfy both halves -- and a text
// built out of the verb would make the `confirm` row a test of that rule
// instead of a test of the table.
func commandEvent(verb string, target *ConfirmTarget) ConfirmEvent {
	ev := ConfirmEvent{
		Type: ConfirmEventCommand, Now: testNow,
		UtteranceID: "utt-1", Text: "do the thing", Verb: verb,
	}
	if target != nil {
		ev.Target, ev.HasTarget = target, true
	}
	// A destructive verb carries the action id the confirmation will name. A
	// launch's is D2786's: a *costly* start carries one too, which a test that
	// means it sets itself, beside the mode that made it costly.
	if confirmContains(ConfirmDestructiveVerbs, verb) {
		ev.ActionID, ev.HasActionID = "act-1", true
	}
	return ev
}

// writeRecorder is the store one pass writes through: it records every path a
// conditional write asked for, which is what "the write set is exactly the
// outbox and the heartbeat" is a fact about. It satisfies OutboxStore and
// HeartbeatStore at once, because those are the same two methods on two nodes.
type writeRecorder struct {
	inner OutboxStore
	paths []string
}

func (s *writeRecorder) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	return s.inner.GetETag(ctx, path, out)
}

func (s *writeRecorder) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	s.paths = append(s.paths, path)
	return s.inner.PutIfMatch(ctx, path, etag, v)
}

// TestTheWriteSetIsTheOutboxAndTheHeartbeat is T6's write-set test, and it is
// C0's sentence as code: the consumer writes the outbox and the heartbeat with
// the operator's own credential and nothing else, so the write set is a rule in
// this command's code rather than a fence the database enforces.
//
// One pass here is the two writes a consumer makes -- one take and one reply --
// through a recorder that sees every conditional write asked of the store, so
// what is asserted is what reached the store and not what a caller intended.
// Each of the two values is then read back and verified in its own direction
// (M4b): a write set that were the right two nodes and unsigned would be a fence
// around the wrong thing.
func TestTheWriteSetIsTheOutboxAndTheHeartbeat(t *testing.T) {
	key := []byte(vectorKey(t))
	f := gcpfake.NewRTDB(t)
	store := &writeRecorder{inner: leaseClient(t, f)}
	ctx := context.Background()

	opts := leaseOptions(leaseHolder, leaseAt)
	opts.Sign = NewHeartbeatSigner(keychain.NewKey(key))
	if _, err := Take(ctx, store, opts); err != nil {
		t.Fatalf("taking the lease: %v", err)
	}
	reply := testReply(t, "r-m-0001", "m-0001")
	run, err := WriteReply(ctx, store, NewOutboxSigner(keychain.NewKey(key)), reply)
	if err != nil {
		t.Fatalf("writing the reply: %v", err)
	}
	if run.Verdict != ReplyWritten {
		t.Fatalf("the reply was not written: %s, %v", run.Verdict, run.Refusal)
	}

	heartbeat, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}
	outbox, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		t.Fatalf("building the outbox path: %v", err)
	}
	want := map[string]string{heartbeat: directionHeartbeat, outbox: directionOutbox}
	if len(store.paths) != len(want) {
		t.Fatalf("the pass wrote %v: one take and one reply are two writes, to %v and only those", store.paths, want)
	}
	for _, path := range store.paths {
		if _, ok := want[path]; !ok {
			t.Errorf("the pass wrote %s, which is neither the outbox nor the heartbeat: the write set is a rule in this package's code (C0), and a third node is a rule broken", path)
		}
	}
	for path, direction := range want {
		stored := storedRecord(t, f, path)
		if stored == nil {
			t.Fatalf("nothing is in the store at %s", path)
		}
		if mac, _ := stored["mac"].(string); mac == "" {
			t.Errorf("the write to %s carries no mac: every outbound value is signed before it is written (M4)", path)
		}
		if ok, err := VerifyMAC(stored, key, direction); err != nil || !ok {
			t.Errorf("the value at %s does not verify in the %s direction: %v, %v", path, direction, ok, err)
		}
	}
}
