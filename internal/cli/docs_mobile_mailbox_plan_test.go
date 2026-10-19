package cli

import (
	"os"
	"strings"
	"testing"
)

// The pin for docs/plans/2026-10-18-mobile-mailbox-consumer.md, the CLI plan
// console D19 requires before mobile task P4.1 (`fugaro mailbox`) may be
// written. Nothing in the plan is built, so no test can hold its behaviour; the
// two things a reader can check today are its citations and the one rule it
// states in three places.
//
// It exists because a review of the plan found the exit contract contradicting
// itself: M12 and 4.4 both stated the rule as "the pass performed no action and
// refused every message it read" -> 1, else 0, which is vacuously true of a
// pass that read nothing — a pass that held no lease never reaches the inbox,
// and an empty mailbox has nothing to read — so every quiet pass would have
// exited 1 while the 0 row promised the opposite. The rule is now four clauses
// (at least one message read, no action, every message read refused, at least
// one of those refusals an action refusal), and both places must carry all of
// them.
const mailboxPlan = "docs/plans/2026-10-18-mobile-mailbox-consumer.md"

// planCited says whether the plan cites path, in its own prose or as the
// relative link the document uses for its neighbours (docs/plans/'s
// ../../SECURITY.md and ../design/v1.md forms, whose surviving suffix is what
// is matched here).
func planCited(doc, path string) bool {
	if strings.Contains(doc, path) {
		return true
	}
	if i := strings.Index(path, "docs/"); i >= 0 {
		return strings.Contains(doc, path[i+len("docs/"):])
	}
	return false
}

// planSection is the plan's text from heading to the next "## " heading at the
// same or a higher level. The plan's subsections are "### ", which does not
// match "## " here, so a subsection runs to the end of its parent section —
// which is what these assertions want: M12 is 3's last ruling and 4.4 is 4's
// last subsection.
func planSection(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("%s has no %q heading", mailboxPlan, heading)
	}
	rest := doc[i:]
	if j := strings.Index(rest[len(heading):], "\n## "); j >= 0 {
		return rest[:len(heading)+j]
	}
	return rest
}

// TestMailboxPlanCitationsExist: every path and symbol the plan names is in the
// tree, and every symbol really is in the file the plan names it in. The plan is
// the authority for a command that does not exist yet, so a citation it gets
// wrong is a later implementer's dead end — and the review that produced this
// pin found one, an exit row that attributed to dbErr a decision checkDBProject
// makes. Only the paths that exist today are listed: the files the plan *will*
// create (internal/mailbox/, internal/cli/mailbox.go and the corpus) are checked
// by the tasks that create them, not here.
//
// Mutation (verified, run and restored): rename a cited symbol in the plan
// (every dbErr -> dbFailure) and this test fails naming the symbol; rename a
// cited path out of the plan and it fails naming the path.
func TestMailboxPlanCitationsExist(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	for _, path := range []string{
		// The CLI the command joins, and the classification its exit rows use.
		"internal/cli/root.go",
		"internal/cli/budget.go",
		"internal/cli/cloud.go",
		"internal/cli/project.go",
		"internal/cli/cancel.go",
		"internal/cli/history.go",
		// The store, the fence on the heartbeat, the rules.
		"internal/rtdb/client.go",
		"internal/budget/kill.go",
		"internal/budget/sweep.go",
		"internal/budget/rules/rules.go",
		"internal/budget/rules/rules.json.tmpl",
		"internal/budget/rules/emulator_mailbox_test.go",
		// The precedents the plan borrows from.
		"internal/watch/acks.go",
		"internal/watch/prstate.go",
		"internal/runstore/launch.go",
		"internal/clidoc/cliref.go",
		"internal/clidoc/cliref_test.go",
		"internal/localcfg",
		"internal/gcpfake",
		"internal/safetext",
		"website/content/cli-reference.md",
		// The merged substrate, which this plan reuses and never forks.
		"shared/mailbox/index.js",
		"shared/lease/index.js",
		"shared/presence/index.js",
		"shared/digest/index.js",
		"shared/mailbox-sim/index.js",
		"shared/mailbox-schema/index.js",
		// The plan's authorities.
		"docs/design/v1.md",
		"docs/design/voice.md",
		"docs/mobile/implementation-plan.md",
		"docs/mobile/functional-spec-reconciliation.md",
	} {
		if !planCited(doc, path) {
			t.Errorf("%s no longer cites %s: this list is the plan's own citations, so one that has left the text must leave this test", mailboxPlan, path)
			continue
		}
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", mailboxPlan, path, err)
		}
	}

	for path, symbols := range map[string][]string{
		"internal/cli/budget.go":         {"func dbErr(", "func checkDBProject(", "func adminDenied("},
		"internal/cli/root.go":           {"ExitOK", "ExitUserError", "ExitRemoteError", "func ExitCode(", "func NewRootCmd("},
		"internal/cli/cancel.go":         {"func cancelRun("},
		"internal/cli/history.go":        {"func newBudgetHistoryCmd("},
		"internal/rtdb/client.go":        {"func (c *Client) GetETag(", "func (c *Client) PutIfMatch(", "ErrPrecondition"},
		"internal/budget/kill.go":        {"func SetKill(", "SetAttempts"},
		"internal/budget/sweep.go":       {"type Sweeper", "func RunUID("},
		"internal/budget/rules/rules.go": {"\"MAILBOX\":", "\"CONSUMER\":"},
		"internal/watch/acks.go":         {"func AckPath("},
		"internal/watch/prstate.go":      {"func (p *PRState) ask("},
		"internal/runstore/launch.go":    {"func (s *Store) Claim(", "ClaimTTL"},
		"internal/clidoc/cliref.go":      {"defaultPath"},
		"internal/clidoc/cliref_test.go": {"func TestCliReferenceIsCurrent("},
		"shared/mailbox/index.js":        {"DEFAULT_WINDOW_MS", "MIN_WINDOW_MS", "MAX_WINDOW_MS", "MAX_PROMPT_TTL_MS", "DEFAULT_MAX_SEEN", "defaultMaxPerSender", "export function step(", "export function initialState("},
		"shared/lease/index.js":          {"CLAIM_TTL", "MAX_CLOCK_SKEW", "export function takeLease(", "export function readLease(", "export function releaseLease(", "export function validateLease("},
		"shared/presence/index.js":       {"export function evaluatePresence(", "MAX_BOOT_ID_LENGTH"},
		"shared/digest/index.js":         {"export function digest(", "REDACTED", "redact"},
		"shared/mailbox-sim/index.js":    {"export function createSimulator(", "export function scheduleFromSeed(", "export function faultFor(", "export function scheduleText(", "export function parseScheduleText(", "export function replyId("},
		"shared/mailbox-schema/index.js": {"FIELDS", "REQUIRED_FIELDS", "validateMessage", "REFUSALS"},
	} {
		data, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		for _, symbol := range symbols {
			if !strings.Contains(string(data), symbol) {
				t.Errorf("%s cites %s's %s, which is no longer there: a citation the plan names must still exist", mailboxPlan, path, symbol)
			}
		}
	}

	// ... and the other direction: the plan must still name them. A symbol the
	// plan has silently renamed is the same dead end as one that left the tree,
	// and it is the failure this half is written for — the review's exit row
	// named dbErr for a decision checkDBProject makes.
	for _, symbol := range []string{
		"dbErr", "checkDBProject", "adminDenied", "cancelRun", "cancel.go",
		"GetETag", "PutIfMatch", "ErrPrecondition", "ErrPermission", "SetKill", "SetAttempts",
		"AckPath", "PRState", "RunUID", "Sweeper", "newBudgetHistoryCmd",
		"selectProject", "budgetDBFor",
		"ExitOK", "ExitUserError", "ExitRemoteError", "ExitCode", "NewRootCmd",
		"defaultPath", "TestCliReferenceIsCurrent", "TestParityCorpusIsCurrent",
		"CLAIM_TTL", "MAX_CLOCK_SKEW", "takeLease", "readLease", "releaseLease",
		"validateLease", "evaluatePresence", "MAX_BOOT_ID_LENGTH",
		"DEFAULT_WINDOW_MS", "MIN_WINDOW_MS", "MAX_WINDOW_MS", "MAX_PROMPT_TTL_MS",
		"DEFAULT_MAX_SEEN", "defaultMaxPerSender", "FIELDS", "REQUIRED_FIELDS",
		"validateMessage", "redact", "digest", "createSimulator", "scheduleFromSeed",
		"faultFor", "scheduleText", "parseScheduleText", "replyId",
		"MAILBOX", "CONSUMER",
	} {
		if !strings.Contains(doc, symbol) {
			t.Errorf("%s no longer names %s: a citation the plan drops is a reader who cannot find what it points at", mailboxPlan, symbol)
		}
	}
}

// TestMailboxPlanExitContractIsOneRule: the plan states its exit contract in
// three places and they must agree. M12 (the ruling) and 4.4 (the command
// surface) each carry the four clauses and the classification each row rests
// on; 9 (D128's fence) points at the same rule; T5 (the task that tests it)
// names both vacuous passes and both refusal kinds. The clauses and the two
// function names are the pin: a revision that drops "read nothing" from either
// statement is the contradiction this pin was written for.
//
// Mutation (verified, run and restored): remove the vacuity statement from the
// plan (every "read nothing" -> "nothing read") and this test fails naming M12,
// 4.4 and 9 — the three places that must all say it.
func TestMailboxPlanExitContractIsOneRule(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	m12 := planSection(t, doc, "### M12 —")
	s44 := planSection(t, doc, "### 4.4 Exit codes")
	s9 := planSection(t, doc, "## 9. D128")

	for _, section := range []struct {
		name string
		text string
	}{{"M12", m12}, {"4.4", s44}} {
		for _, clause := range []string{
			"at least one message", // clause 1: a pass that read nothing must not qualify
			"performed no action",  // clause 2
			"every message",        // clause 3
			"action refusal",       // clause 4: a duplicates-only pass is still 0
			"read nothing",         // the vacuous cases, stated, not left to the reader
			"`dbErr`",              // the classification each exit row rests on
			"`checkDBProject`",     // and the function that decides foreignness
			"`remote(err)`",        // what is left for exit 2
		} {
			if !strings.Contains(section.text, clause) {
				t.Errorf("%s's exit contract no longer states %q: M12 and 4.4 must say the same four-clause rule and attribute each row to the function that decides it", section.name, clause)
			}
		}
	}

	// The refused write is adminDenied's sentence, and 4.4's row names it.
	if !strings.Contains(s44, "adminDenied") {
		t.Errorf("%s's 4.4 no longer names adminDenied for a refused write", mailboxPlan)
	}
	// D128's fence points at the one rule rather than restating it differently.
	for _, want := range []string{"M12's predicate", "read nothing at all"} {
		if !strings.Contains(s9, want) {
			t.Errorf("%s's D128 section no longer says %q", mailboxPlan, want)
		}
	}
	// T5 tests both vacuous passes and both refusal kinds.
	for _, want := range []string{
		"a pass that held no lease",
		"a pass over an empty inbox",
		"read only a refused action",
		"per-message",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s's T5 no longer tests %q: the exit contract is tested on both sides of every row", mailboxPlan, want)
		}
	}
}

// TestMailboxPlanNamesTheMacTriggersAndTheCosts pins the three things a review
// of this plan found stated twice with incompatible answers.
//
//  1. "cannot verify or sign" was one phrase for two states: M4b, section 7's
//     loop and T2a described a per-write refusal (write nothing, report a code,
//     owe the reply again) while 8.3 and T5 described a startup exit, and
//     nothing separated them. They are now M4's three-row table — unavailable
//     (startup, exit 1, no pass), a message that does not verify (per-message,
//     inside a pass), and a value the signer rejects (per-write, inside a pass,
//     reached only by an injected failing signer).
//  2. T7 was said to "run the docs guards" for a guard whose globs never reach
//     docs/plans/, and its own `-run 'Sweep|Expiry'` filter excludes them anyway.
//  3. An unverifiable inbox message can never be retired — nothing records it
//     and the create-only inbox grant refuses a delete — so every pass re-reads
//     and re-refuses it. The cost is now stated where the design decision is
//     (section 7), where the rule is (M4a) and in section 16, rather than left
//     to be discovered.
//
// Mutation (verified, run and restored): delete the "injected failing signer"
// mention from M4 and this test fails naming the section; put "T7 runs the docs
// guards" back and it fails on the withdrawn claim.
func TestMailboxPlanNamesTheMacTriggersAndTheCosts(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	m4 := planSection(t, doc, "### M4 —")
	s83 := planSection(t, doc, "## 8. The MAC, precisely")
	t2a := planSection(t, doc, "### Task T2a:")
	s7 := planSection(t, doc, "## 7. The loop")

	for _, section := range []struct{ name, text string }{
		{"M4", m4}, {"8.3", s83}, {"T2a", t2a},
	} {
		for _, want := range []string{"injected failing signer"} {
			if !strings.Contains(section.text, want) {
				t.Errorf("%s no longer names %q: the per-write signing failure is unreachable without it, and without it M4b's refusal and 8.3's startup exit read as the same behaviour", section.name, want)
			}
		}
	}
	// M4's table is the separation itself: the three states, and the fact that
	// the first is the only startup one.
	for _, want := range []string{
		"cannot be computed **at all**", // the availability trigger
		"exit 1, no pass run",           // what the startup state does
		"A message that does not verify",
		"A write that cannot be signed",
		"only the first is",
	} {
		if !strings.Contains(m4, want) {
			t.Errorf("%s's M4 no longer states %q: the two per-value states and the startup state must stay told apart", mailboxPlan, want)
		}
	}
	// The retired claim: no guard reaches docs/plans/, and T7 does not run them.
	for _, gone := range []string{"T7 runs the docs guards", "T7's docs guard"} {
		if strings.Contains(doc, gone) {
			t.Errorf("%s says %q again: no docs guard's globs reach docs/plans/, so a guard could not prove anything about this document", mailboxPlan, gone)
		}
	}
	if !strings.Contains(doc, "never reach `docs/plans/*.md`") {
		t.Errorf("%s no longer states that the docs guards' globs stop at docs/", mailboxPlan)
	}
	// The parked-message cost, in the three places it has to be.
	for _, section := range []struct{ name, text string }{{"M4", m4}, {"7", s7}} {
		for _, want := range []string{"retire"} {
			if !strings.Contains(section.text, want) {
				t.Errorf("%s no longer states that an unverifiable message cannot be retired and is re-read every pass", section.name)
			}
		}
	}
	if !strings.Contains(doc, "An unverifiable inbox message is never retired") {
		t.Errorf("%s's unverified list no longer carries the parked-message cost", mailboxPlan)
	}
}
