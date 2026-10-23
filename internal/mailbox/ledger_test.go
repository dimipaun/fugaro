package mailbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The ledger's tests (mobile P4.1, Task T4).
//
// Four things, and the task's verification names each: a duplicate delivery is
// not acted on twice; a process killed between the ledger commit and the action
// re-performs nothing after a restart; the file is `0o600` and written
// tmp+rename, asserted by inspecting the directory from inside a commit; and a
// full ledger is refused `dedupe-full`. Around them sit the rules the ledger
// shares with `shared/mailbox` — the two windows, the watermark, the cap and the
// per-sender quota, the `now` guards — and the file's own fail-closed reading,
// which is the part that matters most: a ledger that cannot be read is refused,
// because starting from an empty one would re-perform the action the operator's
// phone asked for once.
//
// The binding is held to the module (M9) by TestLedgerConstantsMatchTheSharedModule,
// which reads the bounds and the refusal vocabulary out of `shared/mailbox/index.js`
// — no Node, no network, no clock — so a constant that moved on either side is a
// failing test rather than a ledger the two ends disagree about. The module's
// *outcomes* for a schedule are T8's corpus; this file pins the rules the ledger
// applies on its own.
//
// **The two red steps that were run and watched fail** (T4's, and the reason the
// commit lives inside Perform rather than in its caller): move the commit after
// the `perform` call in ledger.go and TestRestartDoesNotRePerform fails with the
// action performed a second time across the restart; make commit truncate in
// place (`os.WriteFile(l.path, body, 0o600)`) and TestLedgerWriteIsAtomic fails
// with the ledger already rewritten while the temporary file is still in the
// directory. Restored; see the pull request.

const (
	// testNow is the instant every test judges at. The module reads no clock and
	// neither does this package, so the whole file is deterministic.
	testNow = int64(1_700_000_000_000)
	// testPromptTtlMs is D118's 72 hours, the number `shared/prompts` exports as
	// PROMPT_TTL_MS. T7 binds the command's own constant to the module; here it
	// is the caller's value, which is the point of LedgerOptions.PromptTtlMs.
	testPromptTtlMs = int64(259_200_000)
	// testHorizonMs is the eviction horizon, max(windowMs, promptTtlMs).
	testHorizonMs = testPromptTtlMs
)

// ledgerOptions are the production settings over a test path.
func ledgerOptions(path string) LedgerOptions {
	return LedgerOptions{Path: path, PromptTtlMs: testPromptTtlMs}
}

// openLedger opens a fresh ledger over dir, failing the test if it will not
// open: every test that wants a *refusal* asks for one explicitly.
func openLedger(t *testing.T, dir string) *Ledger {
	t.Helper()
	l, err := OpenLedger(ledgerOptions(filepath.Join(dir, "ledger.json")))
	if err != nil {
		t.Fatalf("opening a fresh ledger: %v", err)
	}
	return l
}

// ledgerMessage is a message as the gate hands one to the ledger: the fields
// the ledger reads, and a tag it never looks at (the MAC is T2's).
func ledgerMessage(id, sender, text string, at int64) *Message {
	return &Message{ID: id, Sender: sender, Text: text, At: at, MAC: strings.Repeat("ab", 32)}
}

// ledgerBytes reads the file the ledger writes.
func ledgerBytes(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the ledger file: %v", err)
	}
	return string(b)
}

// ledgerRefusalCode is the code an OpenLedger refusal carries, through
// errors.As, which is how a caller reaches it.
func ledgerRefusalCode(t *testing.T, err error) Code {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("expected a *Refusal with a code, got %v (%T)", err, err)
	}
	return refusal.Code
}

// TestLedgerConstantsMatchTheSharedModule is M9's rule for this file: every
// bound the ledger applies is `shared/mailbox`'s own number, read back out of
// the module's source, and every refusal code it speaks is one the module
// spells. Go cannot import the module, so this test is the only thing holding
// the two ends together — and it reads the module's *arithmetic* (`6 * 60 * 60 *
// 1_000`) rather than a copy of its result, so a module that moved a bound is a
// failing test here.
func TestLedgerConstantsMatchTheSharedModule(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "shared", "mailbox", "index.js"))
	if err != nil {
		t.Fatalf("reading the module: %v", err)
	}
	declared := map[string]int64{}
	for _, m := range jsMailboxBound.FindAllStringSubmatch(string(source), -1) {
		value, ok := jsMilliseconds(m[2])
		if !ok {
			t.Fatalf("reading %s out of the module: %q is not a bound this test can evaluate", m[1], m[2])
		}
		declared[m[1]] = value
	}
	for name, want := range map[string]int64{
		"DEFAULT_WINDOW_MS":   DefaultWindowMs,
		"MIN_WINDOW_MS":       MinWindowMs,
		"MAX_WINDOW_MS":       MaxWindowMs,
		"MAX_PROMPT_TTL_MS":   MaxPromptTtlMs,
		"DEFAULT_MAX_SEEN":    DefaultMaxSeen,
		"MIN_MAX_SEEN":        MinMaxSeen,
		"MAX_MAX_SEEN":        MaxMaxSeen,
		"SENDER_SHARE":        SenderShare,
		"MAX_FORWARD_JUMP_MS": MaxForwardJumpMs,
	} {
		got, ok := declared[name]
		if !ok {
			t.Errorf("the module no longer declares %s: this package's bound has nothing to be held to", name)
			continue
		}
		if got != want {
			t.Errorf("%s is %d in this package and %d in the module: the two ends disagree about a bound", name, want, got)
		}
	}
	if len(declared) != 9 {
		t.Errorf("the module declares %d bounds this reader understands and this test binds 9: a new one is a bound to bind, not to ignore", len(declared))
	}

	// Two of the module's bounds are not arithmetic in the mailbox module: it
	// aliases them to `shared/lease`'s, which is exactly what this package does.
	// MAX_TIMESTAMP is pinned to the lease module by T3's corpus, so an alias on
	// both sides is what holds the ledger's ceiling; MAX_CLOCK_SKEW is the future
	// tolerance (mac.go's FutureTolerance, which is where the gate applies it
	// first).
	for _, alias := range []struct{ line, what string }{
		{"export const TIME_MAX = MAX_TIMESTAMP;", "the ledger's `at` and `now` ceiling"},
		{"export const DEFAULT_FUTURE_TOLERANCE_MS = MAX_CLOCK_SKEW;", "the future tolerance"},
	} {
		if !strings.Contains(string(source), alias.line) {
			t.Errorf("the module no longer spells `%s`: %s is that bound and nothing here holds it", alias.line, alias.what)
		}
	}
	if TimeMax != MaxTimestamp {
		t.Errorf("TimeMax is %d and the lease's ceiling is %d: the ledger's bound is not the module's", TimeMax, MaxTimestamp)
	}
	if FutureTolerance.Milliseconds() != MaxClockSkew {
		t.Errorf("the future tolerance is %d ms and MAX_CLOCK_SKEW is %d: the ledger would admit an `at` the gate refused", FutureTolerance.Milliseconds(), MaxClockSkew)
	}

	// The refusal vocabulary. The module's REFUSAL holds codes the schema owns
	// too (this package's Codes), so the comparison is one-directional: every
	// code the ledger speaks must be one the module spells. The other direction
	// is T1's and T3's business, code set by code set.
	block := jsMailboxRefusals.FindStringSubmatch(string(source))
	if block == nil {
		t.Fatal("the module no longer declares REFUSAL as a frozen object: this package's vocabulary has nothing to be held to")
	}
	module := map[string]bool{}
	for _, m := range jsMailboxCode.FindAllStringSubmatch(block[1], -1) {
		module[m[2]] = true
	}
	if len(module) < 20 {
		t.Fatalf("read %d refusal codes out of the module: the reader, not the module, is what broke", len(module))
	}
	for _, code := range LedgerCodes {
		if !module[string(code)] {
			t.Errorf("the ledger refuses with %q, which the module does not spell: a code with nothing on the other end is a code that drifted", code)
		}
	}
	// And the one code that is this file's own is not the module's, so it cannot
	// be mistaken for a protocol code that drifted.
	if module[string(CodeInvalidLedger)] {
		t.Errorf("the module spells %q: this file's own code is no longer this file's", CodeInvalidLedger)
	}
}

var (
	jsMailboxBound = regexp.MustCompile(`(?m)^export const (DEFAULT_WINDOW_MS|MIN_WINDOW_MS|MAX_WINDOW_MS|MAX_PROMPT_TTL_MS|DEFAULT_MAX_SEEN|MIN_MAX_SEEN|MAX_MAX_SEEN|SENDER_SHARE|MAX_FORWARD_JUMP_MS) = (.+);$`)
	// The refusal block, and one code per line inside it. The module writes the
	// codes as `NAME: 'code',` with comments above them, so a line that starts
	// with a name is a code.
	jsMailboxRefusals = regexp.MustCompile(`(?s)export const REFUSAL = Object\.freeze\(\{(.*?)\n\}\);`)
	jsMailboxCode     = regexp.MustCompile(`(?m)^\s*([A-Z_]+): '([^']*)',$`)
)

// jsMilliseconds evaluates the module's spelling of a duration: a product of
// decimal integers whose digits may carry `_` separators (`1_000`).
func jsMilliseconds(expression string) (int64, bool) {
	var product int64 = 1
	for _, part := range strings.Split(expression, " * ") {
		n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(part), "_", ""), 10, 64)
		if err != nil {
			return 0, false
		}
		product *= n
	}
	return product, true
}

// TestLedgerPathNamesTheStateDirectory pins M7's location:
// `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json`, under the platform's
// state directory — and the project name is a path component, so a name that is
// not a Fugaro project name is refused rather than joined.
func TestLedgerPathNamesTheStateDirectory(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	posix := env(map[string]string{"XDG_STATE_HOME": "/state", "HOME": "/home/op"})
	got, err := ledgerPath(posix, "linux", "aurora")
	if err != nil {
		t.Fatalf("a project name and a state directory is a ledger path: %v", err)
	}
	if want := "/state/fugaro/mailbox/aurora/ledger.json"; got != want {
		t.Errorf("the ledger is at %s, and M7 says %s", got, want)
	}
	// No XDG_STATE_HOME: the platform's own rule, which is localcfg's.
	got, err = ledgerPath(env(map[string]string{"HOME": "/home/op"}), "linux", "aurora")
	if err != nil {
		t.Fatalf("a home directory is a state directory: %v", err)
	}
	if want := "/home/op/.local/state/fugaro/mailbox/aurora/ledger.json"; got != want {
		t.Errorf("the ledger is at %s, and the POSIX fallback is %s", got, want)
	}
	got, err = ledgerPath(env(map[string]string{"LOCALAPPDATA": "/localappdata", "USERPROFILE": "/users/me"}), "windows", "aurora")
	if err != nil {
		t.Fatalf("a Windows state directory: %v", err)
	}
	if want := filepath.Join("/localappdata", "fugaro", "mailbox", "aurora", "ledger.json"); got != want {
		t.Errorf("the ledger is at %s, and the Windows rule gives %s", got, want)
	}
	// A relative XDG_STATE_HOME is ignored rather than joined, which is
	// localcfg's rule and the XDG spec's (the row is the stand-in spelling the
	// platform tests use: what it proves is which variable decides).
	got, err = ledgerPath(env(map[string]string{"XDG_STATE_HOME": "rel", "HOME": "/home/op"}), "linux", "aurora")
	if err != nil {
		t.Fatalf("a relative XDG_STATE_HOME with a home directory: %v", err)
	}
	if want := "/home/op/.local/state/fugaro/mailbox/aurora/ledger.json"; got != want {
		t.Errorf("the ledger is at %s, and a relative XDG_STATE_HOME is ignored, so it is %s", got, want)
	}
	// No state directory at all is an error, not a ledger in the working
	// directory: the dedupe fence belongs where the operator's own account
	// keeps its state.
	if _, err := ledgerPath(env(nil), "linux", "aurora"); err == nil {
		t.Error("with no state directory at all the ledger path was still answered: a ledger in the working directory is a fence nobody looks at")
	}

	// The project name is a directory here as well as a name.
	for _, bad := range []string{"", "..", "../etc", "a/b", "Aurora", "aurora_2", "-aurora"} {
		if _, err := ledgerPath(posix, "linux", bad); err == nil {
			t.Errorf("%q was accepted as a ledger's project directory", bad)
		}
	}
}

// TestLedgerFileFormatIsPinned pins the document on disk: the three fields, the
// record's five, and the absence of everything else. The windows, the cap and
// the quota are deliberately not in the file (a file that could declare its own
// window could widen what the ledger admits), and the message's text is not
// either.
func TestLedgerFileFormatIsPinned(t *testing.T) {
	dir := t.TempDir()
	l := openLedger(t, dir)
	msg := ledgerMessage("m-1", "mb-a", "a secret sentence", testNow-1_000)
	if _, err := l.Perform(msg, KindCommand, testNow, nil); err != nil {
		t.Fatalf("delivering: %v", err)
	}
	want := fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":[{"sender":"mb-a","id":"m-1","at":%d,"kind":"command","status":"acted"}]}`, testNow, testNow-1_000)
	if got := ledgerBytes(t, l.Path()); got != want {
		t.Errorf("the ledger on disk is\n  %s\nand this test pins\n  %s", got, want)
	}
}

// TestLedgerFileHoldsNoText is M2's and M7's rule about the contents: the ledger
// is keyed by the message's id and holds its `at`, its kind and a status — never
// its text. A ledger that carried text would be a second copy of the mailbox.
func TestLedgerFileHoldsNoText(t *testing.T) {
	dir := t.TempDir()
	l := openLedger(t, dir)
	const text = "please cancel the run"
	if _, err := l.Perform(ledgerMessage("m-1", "mb-a", text, testNow-1_000), KindCommand, testNow, nil); err != nil {
		t.Fatalf("delivering: %v", err)
	}
	if strings.Contains(ledgerBytes(t, l.Path()), text) {
		t.Errorf("the ledger holds the message's text: it must hold ids, times and statuses, and no text at all")
	}
	for _, entry := range l.Entries() {
		if strings.Contains(entry.ID, text) || strings.Contains(entry.Sender, text) {
			t.Errorf("the ledger's record carries the message's text: %+v", entry)
		}
	}
}

// TestLedgerDuplicateIsNotActedOnTwice is the dedupe rule: the same `(sender,
// id)` at the same `at` under the same kind is answered, not acted on, and the
// action runs once for the pair however many times the phone delivers it.
func TestLedgerDuplicateIsNotActedOnTwice(t *testing.T) {
	dir := t.TempDir()
	l := openLedger(t, dir)
	msg := ledgerMessage("m-1", "mb-a", "hi", testNow-1_000)

	performed := 0
	first, err := l.Perform(msg, KindCommand, testNow, func(LedgerEntry) { performed++ })
	if err != nil {
		t.Fatalf("the first delivery: %v", err)
	}
	if first.Outcome != OutcomeActed {
		t.Fatalf("the first delivery answered %q, and a new delivery is acted on", first.Outcome)
	}
	before := ledgerBytes(t, l.Path())

	second, err := l.Perform(msg, KindCommand, testNow+1_000, func(LedgerEntry) { performed++ })
	if err != nil {
		t.Fatalf("the redelivery: %v", err)
	}
	if second.Outcome != OutcomeDuplicate {
		t.Errorf("the redelivery answered %q, and the same (sender, id) is a duplicate", second.Outcome)
	}
	if performed != 1 {
		t.Errorf("the action ran %d times for one delivery: a duplicate is answered, never acted on", performed)
	}
	if got := ledgerBytes(t, l.Path()); got != before {
		t.Errorf("the redelivery rewrote the ledger:\n  %s\n  %s", before, got)
	}
	if entries := l.Entries(); len(entries) != 1 || entries[0].Status != StatusActed {
		t.Errorf("the ledger holds %+v: a duplicate must leave it exactly as it was", entries)
	}
}

// TestLedgerIDConflictIsRefused: the pair is the message's identity, so the same
// pair with another `at` — or another kind — is an ambiguity, never a new
// delivery.
func TestLedgerIDConflictIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		at   int64
	}{
		{"another at", KindCommand, testNow - 2_000},
		{"another kind", KindPrompt, testNow - 1_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := openLedger(t, t.TempDir())
			if _, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil); err != nil {
				t.Fatalf("the first delivery: %v", err)
			}
			got, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", tc.at), tc.kind, testNow, nil)
			if err != nil {
				t.Fatalf("the second delivery: %v", err)
			}
			if got.Refusal == nil || got.Refusal.Code != CodeIDConflict {
				t.Errorf("the second delivery answered %+v, and the pair's identity makes it id-conflict", got)
			}
		})
	}
	// A different sender under the same id is another message: the key is the
	// pair, not the id.
	l := openLedger(t, t.TempDir())
	if _, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil); err != nil {
		t.Fatalf("the first delivery: %v", err)
	}
	got, err := l.Perform(ledgerMessage("m-1", "mb-b", "hi", testNow-1_000), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("the second sender's delivery: %v", err)
	}
	if got.Outcome != OutcomeActed {
		t.Errorf("the same id from another sender answered %+v, and the ledger's key is the pair", got)
	}
}

// TestRestartDoesNotRePerform is T4's headline: the record of a delivery is on
// disk before the action it asked for runs, so a consumer killed in between
// comes back to a ledger that already holds it and performs nothing.
//
// The kill is a panic out of the action, recovered here: the commit has already
// happened, which is exactly the state a killed process leaves behind. The
// mutation this catches is the one T4 names — moving the commit after the
// action — and it fails here with `performed` holding a second entry after the
// restart.
func TestRestartDoesNotRePerform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	msg := ledgerMessage("m-1", "mb-a", "cancel it", testNow-1_000)

	// The pass that dies: the delivery is taken, the ledger is committed, and
	// the process is gone before the action finishes.
	first := openLedger(t, dir)
	performed := []string{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("the action was expected to die in the middle of the pass")
			}
		}()
		if _, err := first.Perform(msg, KindCommand, testNow, func(e LedgerEntry) {
			panic("the process is killed in the action")
		}); err != nil {
			t.Fatalf("the delivery: %v", err)
		}
	}()
	if len(performed) != 0 {
		t.Fatalf("the action recorded %v before it died", performed)
	}

	// What the killed process left behind: the record, committed, as acted.
	if got := ledgerBytes(t, path); !strings.Contains(got, `"status":"acted"`) {
		t.Fatalf("the ledger does not hold the delivery as acted: %s", got)
	}

	// The restart. The same message, delivered again — and nothing performed.
	second, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("restarting: %v", err)
	}
	got, err := second.Perform(msg, KindCommand, testNow+1_000, func(e LedgerEntry) { performed = append(performed, e.ID) })
	if err != nil {
		t.Fatalf("the delivery after the restart: %v", err)
	}
	if got.Outcome != OutcomeDuplicate {
		t.Errorf("after the restart the delivery answered %q, and the ledger still holds the pair", got.Outcome)
	}
	if len(performed) != 0 {
		t.Errorf("the restart re-performed %v: the record on disk is what makes the acting at-most-once", performed)
	}

	// And the ledger is still usable: a different message is a new delivery.
	next, err := second.Perform(ledgerMessage("m-2", "mb-a", "hi", testNow-500), KindCommand, testNow+2_000, func(e LedgerEntry) { performed = append(performed, e.ID) })
	if err != nil {
		t.Fatalf("the next delivery: %v", err)
	}
	if next.Outcome != OutcomeActed || len(performed) != 1 || performed[0] != "m-2" {
		t.Errorf("after the restart a new message answered %+v and performed %v", next, performed)
	}
}

// TestLedgerRollsBackAFailedWrite is the other half of the fence: if the commit
// fails, nothing is performed and nothing is remembered. A process that could
// not write the ledger must not act, and must not come back believing it did.
func TestLedgerRollsBackAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	opts := ledgerOptions(path)
	opts.BeforeRename = func(string) error { return errors.New("the disk is full") }
	l, err := OpenLedger(opts)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	performed := 0
	if _, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), KindCommand, testNow, func(LedgerEntry) { performed++ }); err == nil {
		t.Fatal("a commit that failed was reported as a success")
	}
	if performed != 0 {
		t.Errorf("the action ran %d times over a ledger that could not be written", performed)
	}
	if entries := l.Entries(); len(entries) != 0 {
		t.Errorf("the failed write left the delivery in memory: %+v", entries)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failed write left something at the ledger's path: %v", err)
	}
	// A restart agrees: the delivery was never taken.
	restarted, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("restarting: %v", err)
	}
	got, err := restarted.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), KindCommand, testNow, func(LedgerEntry) { performed++ })
	if err != nil {
		t.Fatalf("the delivery after the failed write: %v", err)
	}
	if got.Outcome != OutcomeActed || performed != 1 {
		t.Errorf("after a failed write the delivery answered %+v and performed %d times", got, performed)
	}
}

// TestLedgerWriteIsAtomic asserts the write by looking at the directory from
// inside it: at the moment the temporary file exists and the ledger has not yet
// been replaced, the ledger still holds the *previous* bytes — so a reader (or a
// crash) never sees a half-written ledger — the temporary file is in the
// destination directory, on the same filesystem as the rename that consumes it,
// and it is `0o600`.
//
// The mutation T4 names — truncating in place — fails here: the ledger would
// already hold the new bytes while the temporary file is still on disk.
func TestLedgerWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "fugaro", "mailbox", "aurora", "ledger.json")
	l, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-2_000), KindCommand, testNow-1_000, nil); err != nil {
		t.Fatalf("the first delivery: %v", err)
	}
	before := ledgerBytes(t, path)

	var seen []string
	l.opts.BeforeRename = func(tmp string) error {
		// Inside the write: the destination still holds the old ledger, and the
		// only new file is the temporary one beside it.
		seen = append(seen, ledgerBytes(t, path))
		if filepath.Dir(tmp) != filepath.Dir(path) {
			t.Errorf("the temporary ledger is at %s, and the rename is only atomic within %s", tmp, filepath.Dir(path))
		}
		info, err := os.Stat(tmp)
		if err != nil {
			t.Fatalf("the temporary ledger: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("the temporary ledger is %v, and M7 says 0600", got)
		}
		names, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatalf("reading the ledger's directory: %v", err)
		}
		var files []string
		for _, entry := range names {
			files = append(files, entry.Name())
		}
		sort.Strings(files)
		if len(files) != 2 {
			t.Errorf("the ledger's directory holds %v during a write: a ledger and its temporary file", files)
		}
		return nil
	}
	if _, err := l.Perform(ledgerMessage("m-2", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil); err != nil {
		t.Fatalf("the second delivery: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("the commit did not reach the rename step: %d inspections", len(seen))
	}
	if seen[0] != before {
		t.Errorf("during the write the ledger already held\n  %s\nand a reader must still see\n  %s", seen[0], before)
	}

	// After it: the new ledger, the mode, and no temporary file left behind.
	after := ledgerBytes(t, path)
	if after == before || !strings.Contains(after, `"id":"m-2"`) {
		t.Errorf("after the write the ledger holds %s", after)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the ledger: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the ledger is %v, and M7 says 0600", got)
	}
	names, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reading the ledger's directory: %v", err)
	}
	if len(names) != 1 || names[0].Name() != "ledger.json" {
		t.Errorf("the ledger's directory holds %v: the temporary file is renamed, never left", names)
	}
	// The directories this package creates are the operator's own state.
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("the ledger's directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("the ledger's directory is %v, and a state directory is 0700", got)
	}
}

// TestLedgerFullIsRefused: a full ledger is refused `dedupe-full` rather than
// grown, and nothing is evicted to make room — the module's own rule, and the
// reason the cap is a bound rather than a hint. The file is filled to the
// module's default cap, so the number the module owns is the number that bites.
func TestLedgerFullIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	seen := make([]LedgerEntry, 0, DefaultMaxSeen)
	for i := 0; i < DefaultMaxSeen; i++ {
		seen = append(seen, LedgerEntry{
			Sender: "mb-a",
			ID:     fmt.Sprintf("m-%04d", i),
			At:     testNow - 1_000,
			Kind:   KindCommand,
			Status: StatusAccepted,
		})
	}
	writeLedgerFile(t, path, ledgerFile{Now: testNow - 500, Seen: seen})
	before := ledgerBytes(t, path)

	l, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("opening a full ledger: %v", err)
	}
	if len(l.Entries()) != DefaultMaxSeen {
		t.Fatalf("the ledger holds %d records and the cap is %d", len(l.Entries()), DefaultMaxSeen)
	}
	got, err := l.Perform(ledgerMessage("m-new", "mb-b", "hi", testNow-1_000), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("delivering into a full ledger: %v", err)
	}
	if got.Refusal == nil || got.Refusal.Code != CodeDedupeFull {
		t.Errorf("a full ledger answered %+v, and the module refuses it dedupe-full", got)
	}
	if after := ledgerBytes(t, path); after != before {
		t.Errorf("a refused delivery rewrote the ledger")
	}
}

// TestLedgerSenderQuotaIsRefused: one sender may hold only its share of the
// ledger, refused and never evicted, so no single sender can lock the others
// out with `dedupe-full`.
func TestLedgerSenderQuotaIsRefused(t *testing.T) {
	dir := t.TempDir()
	opts := ledgerOptions(filepath.Join(dir, "ledger.json"))
	opts.MaxSeen = 8
	opts.MaxPerSender = 2
	l, err := OpenLedger(opts)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := l.Perform(ledgerMessage(fmt.Sprintf("m-%d", i), "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil)
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
		if got.Outcome != OutcomeActed {
			t.Fatalf("delivery %d answered %+v, and the quota is two", i, got)
		}
	}
	got, err := l.Perform(ledgerMessage("m-2", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("the third delivery: %v", err)
	}
	if got.Refusal == nil || got.Refusal.Code != CodeSenderFull {
		t.Errorf("the third delivery from one sender answered %+v, and the quota is two", got)
	}
	// Another sender still has room: the quota is per sender, not a lock.
	other, err := l.Perform(ledgerMessage("m-3", "mb-b", "hi", testNow-1_000), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("another sender's delivery: %v", err)
	}
	if other.Outcome != OutcomeActed {
		t.Errorf("another sender's delivery answered %+v, and the quota is one sender's", other)
	}
}

// TestLedgerWindowsRefuseReplayAndExpiry pins the two windows and their
// boundaries, exactly: a command is a delivery up to and including the six-hour
// window and a `replay` one millisecond past it, a prompt is a delivery up to
// and including its 72-hour TTL and `expired` one millisecond past it, an `at`
// further ahead than the five-minute tolerance is `future-timestamp` and one at
// the tolerance is not, and an `at` of zero — which the schema allows — is
// `zero-timestamp`, because a zero timestamp is the shape of an unset counter.
func TestLedgerWindowsRefuseReplayAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		at   int64
		want Code
	}{
		{"a command exactly at the window", KindCommand, testNow - DefaultWindowMs, ""},
		{"a command one millisecond past it", KindCommand, testNow - DefaultWindowMs - 1, CodeReplay},
		{"a prompt exactly at its TTL", KindPrompt, testNow - testPromptTtlMs, ""},
		{"a prompt one millisecond past it", KindPrompt, testNow - testPromptTtlMs - 1, CodeExpired},
		{"an at at the future tolerance", KindCommand, testNow + FutureTolerance.Milliseconds(), ""},
		{"an at one millisecond past it", KindCommand, testNow + FutureTolerance.Milliseconds() + 1, CodeFutureTimestamp},
		{"an at of zero", KindCommand, 0, CodeZeroTimestamp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := openLedger(t, t.TempDir())
			got, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", tc.at), tc.kind, testNow, nil)
			if err != nil {
				t.Fatalf("delivering: %v", err)
			}
			switch {
			case tc.want == "" && got.Refusal != nil:
				t.Errorf("answered %v, and this delivery is inside its window", got.Refusal)
			case tc.want != "" && (got.Refusal == nil || got.Refusal.Code != tc.want):
				t.Errorf("answered %+v, and the module says %q", got, tc.want)
			}
		})
	}
	// A prompt inside its TTL but past the command window is still a delivery:
	// the two kinds have their own windows, and that is the whole reason the
	// kind is part of the ledger's key.
	l := openLedger(t, t.TempDir())
	got, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-DefaultWindowMs-1_000), KindPrompt, testNow, nil)
	if err != nil {
		t.Fatalf("delivering a prompt: %v", err)
	}
	if got.Outcome != OutcomeActed {
		t.Errorf("a prompt inside its TTL answered %+v, and a prompt is judged by its TTL", got)
	}
}

// TestLedgerStalePendingPerformsNothing: a delivery whose window has passed is
// still taken and acted on — the work is surfaced as late, never dropped — but
// the consumer performs nothing for it (§7) and answers it instead. The record
// is committed as acted either way, which is what makes the answer owed.
func TestLedgerStalePendingPerformsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	// A delivery that was accepted and never acted on — a pass that died before
	// its act — and whose window has since passed.
	writeLedgerFile(t, path, ledgerFile{
		Now:  testNow - DefaultWindowMs - 2_000,
		Seen: []LedgerEntry{{Sender: "mb-a", ID: "m-old", At: testNow - DefaultWindowMs - 1_000, Kind: KindCommand, Status: StatusAccepted}},
	})
	l, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	performed := 0
	got, err := l.Perform(ledgerMessage("m-new", "mb-a", "hi", testNow-1_000), KindCommand, testNow, func(LedgerEntry) { performed++ })
	if err != nil {
		t.Fatalf("delivering: %v", err)
	}
	if got.Outcome != OutcomeStalePending {
		t.Errorf("the pass answered %q, and the oldest un-acted record is past its window", got.Outcome)
	}
	if performed != 0 {
		t.Errorf("a stale-pending delivery performed %d actions, and §7 says it performs nothing", performed)
	}
	entries := l.Entries()
	if len(entries) != 2 {
		t.Fatalf("the ledger holds %+v", entries)
	}
	if entries[0].ID != "m-old" || entries[0].Status != StatusActed {
		t.Errorf("the late record is %+v: it is acted on exactly once, late rather than never", entries[0])
	}
	if entries[1].ID != "m-new" || entries[1].Status != StatusAccepted {
		t.Errorf("the new record is %+v: it was not the one acted on, and it waits its turn", entries[1])
	}
}

// TestLedgerEvictsOnlyAckedRecordsPastTheHorizon: the ledger is bounded by
// eviction, and eviction is allowed to forget **only** a record that is acked
// and strictly older than the horizon — max(windowMs, promptTtlMs), one horizon
// whatever the record's kind. Every eviction raises the watermark to the
// evicted record's `at`, and nothing at or before the watermark is ever admitted
// again.
func TestLedgerEvictsOnlyAckedRecordsPastTheHorizon(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	old := testNow - testHorizonMs - 1_000
	writeLedgerFile(t, path, ledgerFile{
		Now: testNow - 1_000,
		Seen: []LedgerEntry{
			{Sender: "mb-a", ID: "m-acked", At: old, Kind: KindCommand, Status: StatusAcked},
			{Sender: "mb-a", ID: "m-acted", At: old + 1, Kind: KindCommand, Status: StatusActed},
			{Sender: "mb-a", ID: "m-accepted", At: old + 2, Kind: KindCommand, Status: StatusAccepted},
		},
	})
	l, err := OpenLedger(ledgerOptions(path))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	got, err := l.Perform(ledgerMessage("m-new", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("delivering: %v", err)
	}
	// The record acted on is the oldest *accepted* one — `m-accepted`, which was
	// waiting and whose window has since passed — so the outcome is
	// stale-pending. The delivery itself is taken and waits its turn.
	if got.Outcome != OutcomeStalePending || got.Entry.ID != "m-accepted" {
		t.Fatalf("the delivery answered %+v, and the oldest accepted record is m-accepted, past its window", got)
	}
	ids := []string{}
	for _, entry := range l.Entries() {
		ids = append(ids, entry.ID)
	}
	if strings.Join(ids, ",") != "m-acted,m-accepted,m-new" {
		t.Errorf("the ledger holds %v: only the acked record past the horizon may be forgotten", ids)
	}
	// The ones that stayed are untouched: the acted one is still owed an answer,
	// the accepted one is now the acted one, and the new delivery waits.
	for i, want := range []string{StatusActed, StatusActed, StatusAccepted} {
		if got := l.Entries()[i].Status; got != want {
			t.Errorf("record %d is %q, and eviction must not change a status: it is %q", i, got, want)
		}
	}
	// The watermark moved to the evicted record's `at`, and it is on disk.
	var file ledgerFile
	if err := json.Unmarshal([]byte(ledgerBytes(t, path)), &file); err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if file.EvictedThrough != old {
		t.Errorf("the watermark is %d, and the evicted record's `at` is %d", file.EvictedThrough, old)
	}
	// A redelivery of the evicted pair is refused, not taken again.
	again, err := l.Perform(ledgerMessage("m-acked", "mb-a", "hi", old), KindCommand, testNow, nil)
	if err != nil {
		t.Fatalf("redelivering an evicted message: %v", err)
	}
	if again.Refusal == nil {
		t.Errorf("a message the ledger evicted was taken again: %+v", again)
	}
}

// TestLedgerAckRetiresARecord: acking is what makes a record evictable, and it
// is the module's `ack` step over the oldest acted record. Nothing to ack is an
// outcome, not a refusal — and a `now` that went backwards is refused rather
// than written, because the state it would write is one this package would
// refuse to read.
func TestLedgerAckRetiresARecord(t *testing.T) {
	dir := t.TempDir()
	l := openLedger(t, dir)
	if _, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), KindCommand, testNow, nil); err != nil {
		t.Fatalf("delivering: %v", err)
	}
	got, err := l.Ack(testNow + 1_000)
	if err != nil {
		t.Fatalf("acking: %v", err)
	}
	if got.Outcome != OutcomeAcked || got.Entry.ID != "m-1" {
		t.Errorf("acking answered %+v", got)
	}
	if entries := l.Entries(); entries[0].Status != StatusAcked {
		t.Errorf("after acking the record is %+v", entries[0])
	}
	none, err := l.Ack(testNow + 2_000)
	if err != nil {
		t.Fatalf("acking again: %v", err)
	}
	if none.Outcome != OutcomeNothingToAck {
		t.Errorf("a second ack answered %q, and there is nothing left to ack", none.Outcome)
	}
	before := ledgerBytes(t, l.Path())
	back, err := l.Ack(testNow)
	if err != nil {
		t.Fatalf("acking at an earlier now: %v", err)
	}
	if back.Refusal == nil || back.Refusal.Code != CodeNowWentBackwards {
		t.Errorf("an earlier `now` answered %+v, and the module refuses it now-went-backwards", back)
	}
	if after := ledgerBytes(t, l.Path()); after != before {
		t.Errorf("a refused ack rewrote the ledger")
	}
}

// TestLedgerNowGuards pins the module's two `now` guards, which are what make
// the eviction argument sound: a `now` that went backwards would let a window
// run in reverse — so "outside the window" would mean nothing — and one far
// enough ahead would refuse every later honest `now`.
func TestLedgerNowGuards(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  int64
		want Code
	}{
		{"a now earlier than the ledger's own", testNow - 1, CodeNowWentBackwards},
		{"a now at the ledger's own", testNow, ""},
		{"a now a jump ahead", testNow + MaxForwardJumpMs + 1, CodeNowJumped},
		{"a now past the module's ceiling", TimeMax + 1, CodeInvalidNow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ledger.json")
			writeLedgerFile(t, path, ledgerFile{Now: testNow, Seen: []LedgerEntry{}})
			l, err := OpenLedger(ledgerOptions(path))
			if err != nil {
				t.Fatalf("opening: %v", err)
			}
			got, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", tc.now-1_000), KindCommand, tc.now, nil)
			if err != nil {
				t.Fatalf("delivering: %v", err)
			}
			switch {
			case tc.want == "" && got.Refusal != nil:
				t.Errorf("answered %v, and this `now` is the ledger's own", got.Refusal)
			case tc.want != "" && (got.Refusal == nil || got.Refusal.Code != tc.want):
				t.Errorf("answered %+v, and the module says %q", got, tc.want)
			}
		})
	}
	// The kinds the module does not know are refused rather than judged by the
	// command window.
	l := openLedger(t, t.TempDir())
	got, err := l.Perform(ledgerMessage("m-1", "mb-a", "hi", testNow-1_000), "note", testNow, nil)
	if err != nil {
		t.Fatalf("delivering: %v", err)
	}
	if got.Refusal == nil || got.Refusal.Code != CodeUnknownKind {
		t.Errorf("an unknown kind answered %+v", got)
	}
}

// TestLedgerUnreadableFileIsRefused is the fail-closed half, and the one that
// matters most: a ledger that is present but cannot be read is refused, never
// replaced by an empty one. Starting from an empty ledger would re-accept every
// id it holds and re-perform the action the operator's phone asked for once —
// which is the whole failure this file exists to prevent.
func TestLedgerUnreadableFileIsRefused(t *testing.T) {
	good := func(seen string) string {
		return fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":[%s]}`, testNow, seen)
	}
	record := func(id string, at int64, kind, status string) string {
		return fmt.Sprintf(`{"sender":"mb-a","id":%q,"at":%d,"kind":%q,"status":%q}`, id, at, kind, status)
	}
	inside := testNow - 1_000
	for _, tc := range []struct {
		name string
		body string
		cap  int
		want Code
	}{
		{"an empty file", "", 0, CodeInvalidLedger},
		{"a truncated file", `{"now":1700000000000,"evicted`, 0, CodeInvalidLedger},
		{"not JSON at all", "the ledger", 0, CodeInvalidLedger},
		{"an array", "[]", 0, CodeInvalidLedger},
		{"a field the format does not name", fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":[],"windowMs":1}`, testNow), 0, CodeInvalidLedger},
		{"a field inside a record", fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":[{"sender":"mb-a","id":"m-1","at":%d,"kind":"command","status":"acted","text":"hi"}]}`, testNow, inside), 0, CodeInvalidLedger},
		{"two documents", good("") + good(""), 0, CodeInvalidLedger},
		{"trailing bytes", good("") + " x", 0, CodeInvalidLedger},
		{"a fractional at", fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":[{"sender":"mb-a","id":"m-1","at":1.5,"kind":"command","status":"acted"}]}`, testNow), 0, CodeInvalidLedger},
		{"no now", `{"evictedThrough":0,"seen":[]}`, 0, CodeInvalidNow},
		{"a zero now", `{"now":0,"evictedThrough":0,"seen":[]}`, 0, CodeInvalidNow},
		{"no seen", fmt.Sprintf(`{"now":%d,"evictedThrough":0}`, testNow), 0, CodeInvalidSeen},
		{"a null seen", fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":null}`, testNow), 0, CodeInvalidSeen},
		{"a seen that is not an array", fmt.Sprintf(`{"now":%d,"evictedThrough":0,"seen":{}}`, testNow), 0, CodeInvalidLedger},
		{"more records than the cap", good(record("m-1", inside, KindCommand, StatusActed) + "," + record("m-2", inside+1, KindCommand, StatusActed)), 1, CodeInvalidSeen},
		{"a record with no status", good(fmt.Sprintf(`{"sender":"mb-a","id":"m-1","at":%d,"kind":"command"}`, inside)), 0, CodeInvalidSeenEntry},
		{"a record with a zero at", good(record("m-1", 0, KindCommand, StatusActed)), 0, CodeInvalidSeenEntry},
		{"a record with an unknown kind", good(record("m-1", inside, "note", StatusActed)), 0, CodeInvalidSeenEntry},
		{"a record with an unknown status", good(record("m-1", inside, KindCommand, "done")), 0, CodeInvalidSeenEntry},
		{"an empty sender", good(fmt.Sprintf(`{"sender":"","id":"m-1","at":%d,"kind":"command","status":"acted"}`, inside)), 0, CodeInvalidSeenEntry},
		{"an empty id", good(fmt.Sprintf(`{"sender":"mb-a","id":"","at":%d,"kind":"command","status":"acted"}`, inside)), 0, CodeInvalidSeenEntry},
		{"two records for one pair", good(record("m-1", inside, KindCommand, StatusActed) + "," + record("m-1", inside+1, KindCommand, StatusAcked)), 0, CodeDuplicateSeenEntry},
		{"records out of order", good(record("m-2", inside+1, KindCommand, StatusActed) + "," + record("m-1", inside, KindCommand, StatusActed)), 0, CodeUnsortedSeen},
		{"a watermark past what the windows allow", fmt.Sprintf(`{"now":%d,"evictedThrough":%d,"seen":[]}`, testNow, testNow-testHorizonMs+1), 0, CodeInvalidEvictedThrough},
		{"a negative watermark", fmt.Sprintf(`{"now":%d,"evictedThrough":-1,"seen":[]}`, testNow), 0, CodeInvalidEvictedThrough},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ledger.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			opts := ledgerOptions(path)
			if tc.cap != 0 {
				opts.MaxSeen = tc.cap
				opts.MaxPerSender = tc.cap
			}
			l, err := OpenLedger(opts)
			if err == nil {
				t.Fatalf("a ledger this reader cannot trust was opened: %+v", l.Entries())
			}
			if got := ledgerRefusalCode(t, err); got != tc.want {
				t.Errorf("refused with %q, and this file is %q", got, tc.want)
			}
			// The refusal names the field at fault, and never the file's content.
			var refusal *Refusal
			errors.As(err, &refusal)
			if refusal.Field == "" {
				t.Errorf("the refusal names no field: %+v", refusal)
			}
			// And the file is left alone: a refusal never rewrites what it
			// could not read.
			if got := ledgerBytes(t, path); got != tc.body {
				t.Errorf("the refused file was rewritten:\n  %s\n  %s", tc.body, got)
			}
		})
	}
	// The positive control: the same shapes, valid, open.
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	writeLedgerFile(t, path, ledgerFile{Now: testNow, Seen: []LedgerEntry{{Sender: "mb-a", ID: "m-1", At: inside, Kind: KindCommand, Status: StatusAcked}}})
	if _, err := OpenLedger(ledgerOptions(path)); err != nil {
		t.Fatalf("a valid ledger was refused: %v", err)
	}
}

// writeLedgerFile writes a ledger document by hand, which is how these tests
// reach the states a running consumer cannot produce: a full ledger, a record
// left accepted by a pass that died, an acked record past the horizon.
func writeLedgerFile(t *testing.T, path string, file ledgerFile) {
	t.Helper()
	if file.Seen == nil {
		file.Seen = []LedgerEntry{}
	}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("making the fixture's directory: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}
