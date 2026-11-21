package mailbox

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The sweeper's local half and the presence verdict (mobile P4.1, Task T7),
// tested against T7's own verification list: the expiry boundary is exact and
// the constant is `shared/prompts`' number, the delete set holds nothing this
// command does not own, and the presence verdict is `shared/presence`'s rule.
//
// T7's **Red step (a)** lives here: change PromptTTLMs to six hours and
// TestPromptExpiresAt72h fails, because it is asserted against the module's own
// `PROMPT_TTL_MS` and against 259200000 written out rather than against a
// literal this file chose. **Red step (d)** is the other one: make SweepRefusals
// answer nothing for an `m~…` key and TestSweepDeletesNoUser fails.

// jsPromptsTtl reads `shared/prompts`' own arithmetic for D118's number.
var jsPromptsTtl = regexp.MustCompile(`(?m)^export const PROMPT_TTL_MS = ([\d_]+) \* ([\d_]+) \* ([\d_]+) \* ([\d_]+);$`)

// jsPresenceState, jsPresenceReason: the module's two frozen tables.
var (
	jsPresenceState  = regexp.MustCompile(`(?s)export const STATE = Object\.freeze\(\{(.*?)\n\}\);`)
	jsPresenceReason = regexp.MustCompile(`(?s)export const REASON = Object\.freeze\(\{(.*?)\n\}\);`)
	// jsPresenceConst is every `export const NAME = <expression>;` in the
	// module, so a constant written as arithmetic, as a number with separators
	// or as another constant's name is all read the same way.
	jsPresenceConst = regexp.MustCompile(`(?m)^export const ([A-Z][A-Z0-9_]*) = ([^;]+);$`)
)

// TestPromptExpiresAt72h is M3 as a test: the TTL this command enforces is
// `shared/prompts`' `PROMPT_TTL_MS` rather than a number of its own, D118's 72
// hours as amended by D168 on 2026-10-17 is 259200000 ms written out, and the
// boundary is exact on both sides of it.
func TestPromptExpiresAt72h(t *testing.T) {
	if got := jsProduct(t, readSharedModule(t, "prompts"), jsPromptsTtl, "PROMPT_TTL_MS"); got != PromptTTLMs {
		t.Errorf("PromptTTLMs is %d, shared/prompts' PROMPT_TTL_MS is %d: the TTL is the module's number, bound here rather than declared", PromptTTLMs, got)
	}
	if PromptTTLMs != 259_200_000 {
		t.Errorf("PromptTTLMs is %d, want 259200000: 72 h in ms, D118's number as amended by D168", PromptTTLMs)
	}
	if got, want := PromptRetentionMs(60_000), PromptTTLMs+60_000; got != want {
		t.Errorf("PromptRetentionMs(60000) is %d, want %d: D118's TTL plus one sweep interval", got, want)
	}

	now := testNow
	atTheBound := now - PromptTTLMs
	if !PromptExpired(atTheBound, now) || PromptLive(atTheBound, now) {
		t.Errorf("a prompt whose `at` is exactly the TTL old is live and it is expired: `now-at == ttl` is the boundary the consumer's own expiry draws, which keeps a prompt while `now-at < ttl`")
	}
	insideTheBound := atTheBound + 1
	if PromptExpired(insideTheBound, now) || !PromptLive(insideTheBound, now) {
		t.Errorf("a prompt one millisecond inside the bound is expired and it is live")
	}
	if !PromptExpired(atTheBound-1, now) {
		t.Errorf("a prompt one millisecond past the bound is live and it is expired")
	}
}

// TestPresenceConstantsMatchTheSharedModule holds this port to
// `shared/presence`'s own file: the bounds as its arithmetic, the four states,
// the five reasons, and the refusal codes as sets. Go cannot import the module,
// so every constant here is a restatement, and this is what makes each
// restatement a claim something checks.
func TestPresenceConstantsMatchTheSharedModule(t *testing.T) {
	presence := readSharedModule(t, "presence")
	constants := jsPresenceConstants(t, presence)
	for _, c := range []struct {
		name  string
		value int64
	}{
		{"DEFAULT_INTERVAL_MS", PresenceDefaultIntervalMs},
		{"MIN_INTERVAL_MS", PresenceMinIntervalMs},
		{"MAX_INTERVAL_MS", PresenceMaxIntervalMs},
		{"DEFAULT_MISSED_INTERVALS", int64(PresenceDefaultMissedIntervals)},
		{"DEFAULT_OFFLINE_INTERVALS", int64(PresenceDefaultOfflineIntervals)},
		{"MAX_BAND", int64(PresenceMaxBand)},
		{"DEFAULT_FUTURE_TOLERANCE_MS", PresenceDefaultFutureToleranceMs},
		{"MAX_BOOT_ID_LENGTH", int64(PresenceMaxBootIDLength)},
	} {
		got, ok := constants[c.name]
		if !ok {
			t.Fatalf("shared/presence no longer declares %s in the shape this test reads: this port's copy has nothing to be held to", c.name)
		}
		if got != c.value {
			t.Errorf("%s is %d, the module's is %d", c.name, c.value, got)
		}
	}
	// The module writes the future tolerance as another constant's name rather
	// than as a number, and it is the interval for a reason (ordinary jitter is
	// not a clock problem), so the Go copy has to say the same thing.
	if PresenceDefaultFutureToleranceMs != PresenceDefaultIntervalMs {
		t.Errorf("the future tolerance is %d, the interval is %d: the module defines the one as the other", PresenceDefaultFutureToleranceMs, PresenceDefaultIntervalMs)
	}

	states := jsList(t, presence, jsPresenceState, "STATE")
	mine := []string{string(PresenceLive), string(PresenceStale), string(PresenceOffline), string(PresenceClockDiffers)}
	if !sameSet(states, mine) {
		t.Errorf("the states are %v, the module's are %v", mine, states)
	}
	reasons := jsList(t, presence, jsPresenceReason, "REASON")
	mine = []string{
		PresenceReasonFresh, PresenceReasonMissedIntervals, PresenceReasonLongSilence,
		PresenceReasonFutureSkew, PresenceReasonNoHeartbeat,
	}
	if !sameSet(reasons, mine) {
		t.Errorf("the reasons are %v, the module's are %v", mine, reasons)
	}

	table := jsRefusalTable(t, presence)
	for _, code := range PresenceCodes {
		if _, ok := table[string(code)]; !ok {
			t.Errorf("this port speaks %q, which the module's REFUSAL table does not declare", code)
		}
	}
	for code := range table {
		if !hasCode(PresenceCodes, code) {
			t.Errorf("the module declares %q, which this port does not speak: a refusal a caller can meet and this side cannot name is one neither end can report", code)
		}
	}
}

// TestEvaluatePresenceAnswersTheFourStates is the rule's own table: the four
// states with the readings they were decided from, a missing heartbeat that is
// offline and never live, and the refusals that are not states.
func TestEvaluatePresenceAnswersTheFourStates(t *testing.T) {
	now := testNow
	// hb is a heartbeat written `age` milliseconds ago.
	hb := func(age int64) *PresenceHeartbeat {
		return &PresenceHeartbeat{At: now - age, BootID: "boot-1"}
	}
	for _, c := range []struct {
		name   string
		opt    PresenceOptions
		state  PresenceState
		reason string
	}{
		{"a heartbeat one millisecond old", PresenceOptions{Heartbeat: hb(1), Now: now}, PresenceLive, PresenceReasonFresh},
		{"exactly three intervals old", PresenceOptions{Heartbeat: hb(3 * PresenceDefaultIntervalMs), Now: now}, PresenceStale, PresenceReasonMissedIntervals},
		{"exactly twelve intervals old", PresenceOptions{Heartbeat: hb(12 * PresenceDefaultIntervalMs), Now: now}, PresenceOffline, PresenceReasonLongSilence},
		{"two intervals in the future", PresenceOptions{Heartbeat: hb(-2 * PresenceDefaultIntervalMs), Now: now}, PresenceClockDiffers, PresenceReasonFutureSkew},
		{"no heartbeat at all", PresenceOptions{Now: now}, PresenceOffline, PresenceReasonNoHeartbeat},
	} {
		got := EvaluatePresence(c.opt)
		if !got.OK || got.State != c.state || got.Reason != c.reason {
			t.Errorf("%s: %+v, want %s/%s", c.name, got, c.state, c.reason)
		}
	}
	// A clock merely skewed by less than the tolerance is still live: the future
	// check must not swallow ordinary jitter.
	if got := EvaluatePresence(PresenceOptions{Heartbeat: hb(-1), Now: now}); got.State != PresenceLive {
		t.Errorf("a heartbeat one millisecond ahead is %q, want live: the tolerance is the module's own interval", got.State)
	}
	// No heartbeat is not a reading: the module answers `ageMs: null`
	// (HasAge false), where a Go zero int64 would be an age of zero.
	if got := EvaluatePresence(PresenceOptions{Now: now}); got.HasAge || got.AgeMs != 0 {
		t.Errorf("a missing heartbeat carries an age (%+v), and the module answers none", got)
	}
	// The readings a caller renders come back with the verdict.
	if got := EvaluatePresence(PresenceOptions{Heartbeat: hb(1_000), Now: now}); !got.HasAge || got.AgeMs != 1_000 || got.BootID != "boot-1" || got.HeartbeatAt != now-1_000 {
		t.Errorf("a live verdict read back as %+v", got)
	}
	if got := EvaluatePresence(PresenceOptions{Now: now, IntervalMs: 2 * PresenceDefaultIntervalMs}); got.StaleAfterMs != 6*PresenceDefaultIntervalMs || got.OfflineAfterMs != 24*PresenceDefaultIntervalMs {
		t.Errorf("the bands do not follow the interval: %+v", got)
	}

	for _, c := range []struct {
		name  string
		opt   PresenceOptions
		code  Code
		field string
	}{
		{"a now that is not a time", PresenceOptions{Now: 0}, CodePresenceInvalidNow, "now"},
		{"an interval below D117's floor", PresenceOptions{Now: now, IntervalMs: PresenceMinIntervalMs - 1}, CodePresenceIntervalBelowFloor, "intervalMs"},
		{"an interval past the ceiling", PresenceOptions{Now: now, IntervalMs: PresenceMaxIntervalMs + 1}, CodePresenceIntervalAboveCeiling, "intervalMs"},
		{"bands that do not nest", PresenceOptions{Now: now, MissedIntervals: 4, OfflineIntervals: 4}, CodePresenceBandsNotIncreasing, "offlineIntervals"},
		{"a band past the arithmetic bound", PresenceOptions{Now: now, MissedIntervals: PresenceMaxBand + 1}, CodePresenceInvalidBand, "missedIntervals"},
		{"a negative future tolerance", PresenceOptions{Now: now, FutureToleranceMs: -1}, CodePresenceInvalidFutureTolerance, "futureToleranceMs"},
		{"a boot id carrying a directionality character", PresenceOptions{Now: now, Heartbeat: &PresenceHeartbeat{At: now, BootID: "boot‮1"}}, CodePresenceInvalidBootID, "heartbeat.bootId"},
		{"a boot id past its length bound", PresenceOptions{Now: now, Heartbeat: &PresenceHeartbeat{At: now, BootID: strings.Repeat("b", PresenceMaxBootIDLength+1)}}, CodePresenceInvalidBootID, "heartbeat.bootId"},
		{"a heartbeat with no boot id", PresenceOptions{Now: now, Heartbeat: &PresenceHeartbeat{At: now}}, CodePresenceHeartbeatMissingField, "heartbeat"},
		{"a heartbeat with no timestamp", PresenceOptions{Now: now, Heartbeat: &PresenceHeartbeat{BootID: "boot-1"}}, CodePresenceInvalidHeartbeatTime, "heartbeat.at"},
	} {
		got := EvaluatePresence(c.opt)
		if got.OK || got.Code != c.code || got.Field != c.field {
			t.Errorf("%s: %+v, want the refusal %s at %s", c.name, got, c.code, c.field)
		}
	}
}

// TestSweepDeletesNoUser is M8's fence as a reader, and T7's Red step (d): the
// sweeper's delete set names this machine's own state and nothing else, so a
// Firebase user (`m~…`, whose rule is P6.6's, `r~…`, whose rule is the history
// sweeper's, and `p~…`) or a node under the store's `mailbox/` subtree is
// refused and reported rather than performed.
func TestSweepDeletesNoUser(t *testing.T) {
	for _, c := range []struct {
		key    string
		reason string
	}{
		{"m~AAAAAAAAAAAAAAAAAAAAAA", "a paired device's user"},
		{"r~fugaro~20261018-125859-d3c8", "a run's user"},
		{"p~fugaro", "a pairing's user"},
		{"mailbox/m~AAAAAAAAAAAAAAAAAAAAAA/outbox/p-1", "a store node under a mailbox"},
		{"", "an empty key"},
	} {
		if findings := SweepRefusals([]string{c.key}); len(findings) != 1 {
			t.Errorf("%s (%s) answered %v, want one finding: the sweeper refuses and reports it rather than performing it", c.key, c.reason, findings)
		}
	}
	// A local state file is this sweep's, even though its directory is named
	// `mailbox`: an absolute path under the operator's own state directory is
	// not the store's namespace, and the two are told apart by shape.
	for _, path := range []string{
		"/home/op/.local/state/fugaro/mailbox/aurora/ledger.json",
		"/home/op/.local/state/fugaro/mailbox/aurora/pending.json",
	} {
		if findings := SweepRefusals([]string{path}); len(findings) != 0 {
			t.Errorf("%s was refused (%v), and it is this machine's own state", path, findings)
		}
	}
	// And so is a ledger id, which is the shape this sweep builds out of the
	// device uid: `<sender>/<id>`. It is not a Firebase user — the separator is
	// what tells the two apart — and a fence that read the prefix alone would
	// refuse this sweep's own set.
	for _, key := range []string{
		"m~AAAAAAAAAAAAAAAAAAAAAA/m-0001",
		"r~fugaro~20261018-125859-d3c8/m-0002",
	} {
		if findings := SweepRefusals([]string{key}); len(findings) != 0 {
			t.Errorf("%s was refused (%v), and it is a ledger id rather than a user root", key, findings)
		}
	}
	// And the set the sweep itself builds names none of the refused keys: that
	// is the half which makes the fence about this command and not about the
	// reader alone. Its ledger half is seeded the way production seeds one — a
	// sender that is the paired device's `m~…` uid — because a set built from a
	// sender production never writes would prove nothing about the real one.
	dir := t.TempDir()
	l := openLedger(t, dir)
	const deviceUID = "m~AAAAAAAAAAAAAAAAAAAAAA"
	if err := l.commit(ledgerState{now: testNow, seen: []LedgerEntry{
		{Sender: deviceUID, ID: "m-acked", At: testNow - PromptRetentionMs(60_000) - 1, Kind: KindCommand, Status: StatusAcked},
	}}); err != nil {
		t.Fatalf("writing a ledger state: %v", err)
	}
	l, err := OpenLedger(ledgerOptions(l.Path()))
	if err != nil {
		t.Fatalf("reopening the ledger this test wrote: %v", err)
	}
	m := openMemoryAt(t, dir, PromptRetentionMs(60_000))
	settleExpiredPrompt(t, m, "p-old", deviceUID)
	sweep, err := SweepLocal(l, m, testNow)
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if len(sweep.PromptIDs) != 1 || sweep.PromptIDs[0] != "p-old" {
		t.Fatalf("the sweep let go of %v, want the one settled prompt, so this test is not reading an empty set", sweep.PromptIDs)
	}
	if !sameStrings(sweep.LedgerIDs, []string{deviceUID + "/m-acked"}) {
		t.Fatalf("the sweep's ledger half is %v, want the acked record past the horizon: the case this guard exists for is the ledger id, and an empty one would leave it unchecked", sweep.LedgerIDs)
	}
	keys := append(append([]string{}, sweep.Paths()...), sweep.LedgerIDs...)
	keys = append(keys, sweep.PromptIDs...)
	if findings := SweepRefusals(keys); len(findings) != 0 {
		t.Errorf("the sweep's own delete set was refused: %v (%v)", findings, keys)
	}
	for _, key := range keys {
		if hasUserKeyPrefix(key) {
			t.Errorf("the delete set names %s, and a mailbox sweep deletes no user", key)
		}
		if isStorePath(key) && !isLocalStateFile(key) {
			t.Errorf("the delete set names the store path %s, and the stored-prompt deletion is P6.13's half", key)
		}
	}
}

// TestSweepLocalDropsOnlyWhatTheWindowAllows is the local delete set's rule at
// both sides: a spent id and a settled prompt past the retention window are let
// go, a prompt still to be answered is not — dropping it would lose the safe
// answer it is owed — and a ledger record that is acted but not acked is not,
// because it is work this consumer still owes an answer for.
func TestSweepLocalDropsOnlyWhatTheWindowAllows(t *testing.T) {
	const sweepIntervalMs = 60_000
	retention := PromptRetentionMs(sweepIntervalMs)
	now := testNow
	past := now - retention - 1

	dir := t.TempDir()
	l := openLedger(t, dir)
	// Two records past the horizon: one the phone has spoken for (acked, the
	// only status an eviction may take) and one it has not. The sender is the
	// paired device's own `m~…` uid, which is the sender production writes: an
	// id built from it is `<uid>/<id>`, and the fence must admit that shape.
	const deviceUID = "m~AAAAAAAAAAAAAAAAAAAAAA"
	if err := l.commit(ledgerState{now: now, seen: []LedgerEntry{
		{Sender: deviceUID, ID: "m-acked", At: past, Kind: KindCommand, Status: StatusAcked},
		{Sender: deviceUID, ID: "m-acted", At: past, Kind: KindCommand, Status: StatusActed},
	}}); err != nil {
		t.Fatalf("writing a ledger state: %v", err)
	}
	l, err := OpenLedger(ledgerOptions(l.Path()))
	if err != nil {
		t.Fatalf("reopening the ledger this test wrote: %v", err)
	}

	m := openMemoryAt(t, dir, retention)
	settleExpiredPrompt(t, m, "p-settled", deviceUID)
	if _, _, err := m.NotePrompt(ConfirmPrompt{ID: "p-waiting", At: past, Mailbox: deviceUID, Safe: "carry on"}, past); err != nil {
		t.Fatalf("holding a prompt: %v", err)
	}
	spendAt(t, m, "u-old", past)
	spendAt(t, m, "u-live", now-1_000)

	sweep, err := SweepLocal(l, m, now)
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if !sameStrings(sweep.LedgerIDs, []string{deviceUID + "/m-acked"}) {
		t.Errorf("the ledger let go of %v, want only the acked record past the horizon", sweep.LedgerIDs)
	}
	// The set the sweep built is one its own fence admits: a ledger id is
	// `<sender>/<id>`, and the sender is the device uid, so a prefix test that
	// read the whole key would refuse the sweep's own set and never sweep.
	if findings := SweepRefusals(append([]string{}, sweep.LedgerIDs...)); len(findings) != 0 {
		t.Errorf("the sweep's own ledger ids were refused: %v (%v)", findings, sweep.LedgerIDs)
	}
	for _, id := range []string{"p-settled", "u-old"} {
		if !hasString(sweep.PromptIDs, id) {
			t.Errorf("the memory kept %s, and it is past the retention window", id)
		}
	}
	for _, id := range []string{"p-waiting", "u-live"} {
		if hasString(sweep.PromptIDs, id) {
			t.Errorf("the memory let go of %s, and the window does not allow it: a waiting prompt is never dropped, and a spent id inside the window is the record of a replay", id)
		}
	}
	// What the sweep decided is on disk, so the consumer's next pass reads the
	// same state rather than the state the sweep found.
	restarted, err := OpenLedger(ledgerOptions(l.Path()))
	if err != nil {
		t.Fatalf("reopening the ledger: %v", err)
	}
	ids := []string{}
	for _, entry := range restarted.Entries() {
		ids = append(ids, entry.ID)
	}
	if !sameStrings(ids, []string{"m-acted"}) {
		t.Errorf("the ledger on disk holds %v, want only the record that is still owed", ids)
	}
	back, err := OpenConfirmMemory(ConfirmMemoryOptions{Path: m.Path(), RetentionMs: retention, MaxPrompts: ConfirmMaxPrompts})
	if err != nil {
		t.Fatalf("reopening the memory: %v", err)
	}
	if _, waiting, held := back.Prompt(deviceUID, "p-waiting"); !held || !waiting {
		t.Errorf("the waiting prompt did not survive the sweep: %+v", back.Prompts())
	}
	if _, _, held := back.Prompt(deviceUID, "p-settled"); held {
		t.Errorf("the settled prompt survived the sweep")
	}

	// A second sweep has nothing left to do and writes nothing at all: this
	// machine's own state is not this command's to touch when there is no work.
	before := ledgerBytes(t, l.Path())
	again, err := SweepLocal(l, m, now)
	if err != nil {
		t.Fatalf("sweeping again: %v", err)
	}
	if !again.Empty() {
		t.Errorf("a second sweep let go of %+v, and the first let go of all there was", again)
	}
	if ledgerBytes(t, l.Path()) != before {
		t.Errorf("a sweep with nothing to drop rewrote the ledger")
	}
}

// TestSweepLocalIsANoOpOnAnEmptyMachine: the sweeper is the owner's command and
// runs on a machine the consumer may never have run on. Nothing exists, so
// nothing is created — a file a sweep made would be state the consumer's next
// pass reads as its own.
func TestSweepLocalIsANoOpOnAnEmptyMachine(t *testing.T) {
	dir := t.TempDir()
	l := openLedger(t, dir)
	m := openMemoryAt(t, dir, PromptRetentionMs(60_000))
	sweep, err := SweepLocal(l, m, testNow)
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if !sweep.Empty() {
		t.Errorf("a sweep of an empty machine let go of %+v", sweep)
	}
	if got := ledgerBytesOrEmpty(t, l.Path()); got != "" {
		t.Errorf("the sweep wrote a ledger where there was none:\n%s", got)
	}
	if _, err := os.Stat(m.Path()); !os.IsNotExist(err) {
		t.Errorf("the sweep wrote a prompt memory where there was none: %v", err)
	}
}

// jsPresenceConstants reads every `export const NAME = …;` in `shared/presence`
// as a number, resolving a constant written as another constant's name.
func jsPresenceConstants(t *testing.T, body []byte) map[string]int64 {
	t.Helper()
	rhs := map[string]string{}
	for _, m := range jsPresenceConst.FindAllSubmatch(body, -1) {
		rhs[string(m[1])] = strings.TrimSpace(string(m[2]))
	}
	out := map[string]int64{}
	for i := 0; i < len(rhs)+1; i++ {
		for name, expression := range rhs {
			value := int64(1)
			resolved := true
			for _, factor := range strings.Split(expression, "*") {
				factor = strings.TrimSpace(factor)
				if n, err := strconv.ParseInt(strings.ReplaceAll(factor, "_", ""), 10, 64); err == nil {
					value *= n
					continue
				}
				if n, ok := out[factor]; ok {
					value *= n
					continue
				}
				resolved = false
				break
			}
			if resolved {
				out[name] = value
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("shared/presence's constants read as empty, so this test would pass over nothing")
	}
	return out
}

// sameSet compares two lists as sets.
func sameSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := map[string]bool{}
	for _, one := range left {
		seen[one] = true
	}
	for _, one := range right {
		if !seen[one] {
			return false
		}
	}
	return true
}

func hasCode(list []Code, want string) bool {
	for _, one := range list {
		if string(one) == want {
			return true
		}
	}
	return false
}

func hasString(list []string, want string) bool {
	for _, one := range list {
		if one == want {
			return true
		}
	}
	return false
}

// openMemoryAt opens a prompt memory beside this test's ledger, at the retention
// window the caller names.
func openMemoryAt(t *testing.T, dir string, retentionMs int64) *ConfirmMemory {
	t.Helper()
	m, err := OpenConfirmMemory(ConfirmMemoryOptions{
		Path: dir + "/pending.json", RetentionMs: retentionMs, MaxPrompts: ConfirmMaxPrompts,
	})
	if err != nil {
		t.Fatalf("opening a fresh memory: %v", err)
	}
	return m
}

// settleExpiredPrompt writes one prompt the window has already settled: it was
// announced before it and its answer has been handed back, which is the only
// state the memory lets go of. The settle is stamped at the announcement, so the
// prompt is settled *and* past the window when the sweep looks at it.
func settleExpiredPrompt(t *testing.T, m *ConfirmMemory, id, mailbox string) {
	t.Helper()
	at := testNow - PromptTTLMs - 60_000 - 1
	if _, refusal, err := m.NotePrompt(ConfirmPrompt{ID: id, At: at, Mailbox: mailbox, Safe: "carry on"}, at); err != nil || refusal != nil {
		t.Fatalf("holding %s: refusal=%v err=%v", id, refusal, err)
	}
	if _, ok, err := m.AnswerPrompt(mailbox, id, "carry on", at); err != nil || !ok {
		t.Fatalf("settling %s: ok=%v err=%v", id, ok, err)
	}
}

// spendAt records one id as spent at `at`, which is inside the window at that
// instant, so the memory holds it until a later `now` moves the window past it.
func spendAt(t *testing.T, m *ConfirmMemory, id string, at int64) {
	t.Helper()
	if _, err := m.Spend(ConfirmLedgerUtterance, id, at); err != nil {
		t.Fatalf("spending %s: %v", id, err)
	}
}
