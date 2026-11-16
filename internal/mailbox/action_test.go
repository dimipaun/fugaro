package mailbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
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
	early, err := m.ExpirePrompts(start+ttl-1, ttl)
	if err != nil || len(early) != 0 {
		t.Fatalf("before the TTL: expired=%v err=%v", early, err)
	}
	at, err := m.ExpirePrompts(start+ttl, ttl)
	if err != nil {
		t.Fatalf("at the TTL: %v", err)
	}
	if len(at) != 1 || at[0].Stop || at[0].Reply != "yes, go ahead" {
		t.Fatalf("at the TTL: %v, want one non-stop answer", at)
	}
	// A second process reads the same file: the answer is not owed twice.
	restarted := openTestMemory(t, path, ConfirmMaxPrompts)
	again, err := restarted.ExpirePrompts(start+ttl+1, ttl)
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
	expired, err := m.ExpirePrompts(start+ttl, ttl)
	if err != nil {
		t.Fatalf("at the TTL: %v", err)
	}
	if len(expired) != 1 || !expired[0].Stop || expired[0].Reply != ConfirmExpiredReply {
		t.Fatalf("a destructive prompt expired to %v, want a stop", expired)
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
	body, err := os.ReadFile(filepath.Join("..", "..", "shared", name, "index.js"))
	if err != nil {
		t.Fatalf("reading shared/%s: %v", name, err)
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
	jsRefusalBlock             = regexp.MustCompile(`(?s)export const REFUSAL = Object\.freeze\(\{(.*?)\n\}\);`)
	jsRefusalEntry             = regexp.MustCompile(`(?m)^\s*([A-Z_0-9]+): '([^']*)',$`)

	jsPromptsMaxPending = regexp.MustCompile(`(?m)^export const DEFAULT_MAX_PENDING = (\d+);$`)
	jsPromptsQueueFull  = regexp.MustCompile(`(?m)^export const QUEUE_FULL_REPLY = '([^']*)';$`)
	jsPromptsExpired    = regexp.MustCompile(`(?m)^export const EXPIRED_REPLY = '([^']*)';$`)

	jsCommandPhrases      = regexp.MustCompile(`(?m)^export const CONFIRMATION_PHRASES = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsCommandAliases      = regexp.MustCompile(`(?m)^export const WAKE_ALIASES = Object\.freeze\(\[([^\]]*)\]\);$`)
	jsCommandMaxUtterance = regexp.MustCompile(`(?m)^export const MAX_UTTERANCE_LENGTH = (\d+);$`)

	jsLeaseMaxTimestamp = regexp.MustCompile(`(?m)^export const MAX_TIMESTAMP = (.+);$`)
)
