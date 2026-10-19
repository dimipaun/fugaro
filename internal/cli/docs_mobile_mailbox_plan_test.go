package cli

import (
	"encoding/json"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The pin for docs/plans/2026-10-18-mobile-mailbox-consumer.md, the CLI plan
// console D19 requires before mobile task P4.1 (`fugaro mailbox`) may be
// written. Nothing in the plan is built, so no test can hold its behaviour; the
// two things a reader can check today are its citations and the one rule it
// states in three places.
//
// The path half of the citations is derived from the plan rather than listed
// here (planPaths, below), because a hand-kept list quietly stops covering the
// plan the moment the plan names a new file: a review of this pin found
// internal/cli/docs_website_cliref_test.go — named three times in the plan and
// load-bearing for T5's reference regeneration — absent from the list while the
// header claimed "every path and symbol the plan names is in the tree".
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

// planPathRE matches a backticked repository path: the plan writes them as
// `internal/cli/root.go`, `shared/` (with its trailing slash) and
// `internal/localcfg` (a package directory without one).
var planPathRE = regexp.MustCompile("`((?:internal|shared|mobile|docs|website|deploy|scripts)/[A-Za-z0-9_./-]*)`")

// planLinkRE matches a markdown link target. The plan cites its neighbours that
// way (../design/voice.md) as well as in backticks, and a path is cited however
// it is written.
var planLinkRE = regexp.MustCompile(`\]\(([^)]+)\)`)

// planPaths is every repository path the plan names — in backticks and as a
// link — sorted and deduplicated. It is the list the existence check runs on,
// read out of the document at test time so that a path the plan adds is checked
// the moment it is written down. A trailing slash (a directory written as
// `shared/`) is stripped, so the two spellings of one directory are one entry;
// a link is resolved against docs/plans/, which is where the document lives.
func planPaths(doc string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSuffix(p, "/")
		if !planPathPrefix(p) || !isPlanPath(p) || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, m := range planPathRE.FindAllStringSubmatch(doc, -1) {
		add(m[1])
	}
	for _, m := range planLinkRE.FindAllStringSubmatch(doc, -1) {
		add(path.Join("docs/plans", m[1]))
	}
	sort.Strings(out)
	return out
}

// planPathPrefix is the brief's rule for what counts as a repository path here:
// the top-level directories this repository's tree uses for code, docs and
// deployables. A token that starts with anything else (a flag, a key, a
// `$XDG_STATE_HOME` path) is not a path this test can stat.
func planPathPrefix(p string) bool {
	for _, root := range []string{"internal/", "shared/", "mobile/", "docs/", "website/", "deploy/", "scripts/"} {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}

// isPlanPath keeps package-qualified symbols out of the path list: a path ends
// in a directory (no dot at all — `internal/clidoc`, `shared/presence`) or in a
// file whose extension this repository uses. `internal/procgroup.Run` and
// `mobile/implementation-plan.md`'s sibling symbol spellings are references, not
// paths, and stat'ing them would be a test failing on its own mistake.
func isPlanPath(p string) bool {
	last := p[strings.LastIndex(p, "/")+1:]
	if !strings.Contains(last, ".") {
		return true
	}
	switch path.Ext(last) {
	case ".go", ".js", ".json", ".md", ".tmpl", ".yml", ".yaml", ".ts":
		return true
	}
	return false
}

// planWillCreate is the paths the plan names that are deliberately not in the
// tree yet: T1–T8 create them. Naming them here rather than leaving them out of
// the derivation is the point — the test asserts each one is *still* absent, so
// this list cannot rot into a place where a real path hides, and each one is
// still named by the plan.
var planWillCreate = map[string]string{
	"internal/cli/mailbox.go":                 "T5's command surface",
	"internal/cli/mailbox_test.go":            "T5, T6",
	"internal/cli/mailbox_sweep_test.go":      "T7",
	"internal/mailbox":                        "T1–T4, T2a, T6, T7, T8 — the package itself",
	"internal/mailbox/keychain":               "T2's keychain item-name reader",
	"internal/mailbox/mac.go":                 "T2 (verify) and T2a (sign)",
	"internal/mailbox/message.go":             "T1",
	"internal/mailbox/message_test.go":        "T1",
	"internal/mailbox/lease.go":               "T3",
	"internal/mailbox/ledger.go":              "T4",
	"internal/mailbox/expiry.go":              "T3, T4",
	"internal/mailbox/action.go":              "T6",
	"internal/mailbox/parity_test.go":         "T8 (replays shared/mailbox/parity.test.js's corpus)",
	"internal/mailbox/mac_sign_test.go":       "T2a",
	"internal/mailbox/testdata/parity.json":   "T1 generates it; the Go side only reads it",
	"docs/mobile/mailbox-review-checklist.md": "T8",
	// The parity corpora the shared modules' own test files will write; the
	// merged modules' index.test.js files exist today, these do not (§13's
	// "no merged module changes" — test files only).
	"shared/mailbox/parity.test.js":     "T1",
	"shared/lease/parity.test.js":       "T3",
	"shared/mailbox-sim/parity.test.js": "T8",
}

// TestMailboxPlanEveryCitedPathExists is the path half of the citations pin,
// derived from the plan rather than kept by hand. Every backticked `internal/`,
// `shared/`, `mobile/`, `docs/`, `website/`, `deploy/` or `scripts/` path the
// plan names must be in the tree — or be one of the handful T1–T8 create, each
// of which must still be absent and still be named by the plan.
//
// Mutation (verified, run and restored): rename a cited path in the plan
// (`internal/cli/docs_website_cliref_test.go` -> `…_test2.go`) and this test
// fails naming it; add a path to planWillCreate that exists (or one the plan
// does not name) and it fails on that entry.
func TestMailboxPlanEveryCitedPathExists(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	paths := planPaths(doc)
	// A regex that stopped matching would make every assertion below vacuous.
	if len(paths) < 40 {
		t.Fatalf("planPaths found %d paths in %s: the derivation, not the plan, is what broke", len(paths), mailboxPlan)
	}
	named := map[string]bool{}
	for _, p := range paths {
		named[p] = true
	}
	for _, p := range paths {
		if why, ok := planWillCreate[p]; ok {
			if _, err := os.Stat("../../" + p); err == nil {
				t.Errorf("%s: %s exists now (%s), so it is no longer a path T1–T8 will create: it must leave planWillCreate and be checked like any other", mailboxPlan, p, why)
			}
			continue
		}
		if pathExists(p) {
			continue
		}
		t.Errorf("%s cites %s, which is not in the tree: a path the plan names and cannot be found is a later implementer's dead end", mailboxPlan, p)
	}
	for p, why := range planWillCreate {
		if !named[p] {
			t.Errorf("planWillCreate names %s (%s), which the plan no longer cites: the list must only excuse paths the document actually names", p, why)
		}
	}
}

// pathExists resolves one of the plan's path spellings against the repository.
// docs/plans/'s own links to its neighbours are relative (`../mobile/…`), and
// the plan also writes that neighbour as `mobile/…`, so a path that is not at
// the root is tried under docs/ as well. Only the branch's own tree is
// consulted; nothing is fetched.
func pathExists(p string) bool {
	for _, root := range []string{"../../", "../../docs/"} {
		if _, err := os.Stat(root + p); err == nil {
			return true
		}
	}
	return false
}

// TestMailboxPlanCitationsExist: the symbols the plan cites really are in the
// files it names them in, and the plan still names them. The plan is the
// authority for a command that does not exist yet, so a citation it gets wrong
// is a later implementer's dead end — and the review that produced this pin
// found one, an exit row that attributed to dbErr a decision checkDBProject
// makes. The paths are the other half, and they are derived from the document
// itself in TestMailboxPlanEveryCitedPathExists rather than listed here.
//
// Mutation (verified, run and restored): rename a cited symbol in the plan
// (every dbErr -> dbFailure) and this test fails naming the symbol; rename a
// cited path out of the plan and the derivation test fails naming the path.
func TestMailboxPlanCitationsExist(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	// Anchor citations: paths named in the plan's prose that must stay named —
	// the ones whose loss would leave a claim in this plan unsupported. The
	// completeness half is TestMailboxPlanEveryCitedPathExists, which derives its
	// list from the document; this list is deliberately not "all of them", and
	// nothing here claims to be.
	for _, cited := range []string{
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
		"internal/cli/docs_website_cliref_test.go",
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
		if !planCited(doc, cited) {
			t.Errorf("%s no longer cites %s: this list is the plan's own citations, so one that has left the text must leave this test", mailboxPlan, cited)
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
	// Section 5 item 4's "no test reads this document" was false of this pin,
	// which is the one test that does. It now names the gap precisely.
	if strings.Contains(doc, "no test in this repository reads this document") {
		t.Errorf("%s claims no test reads it again: this pin does, by readDoc, and section 13 says so", mailboxPlan)
	}
	if !strings.Contains(doc, "is the only test that reads") {
		t.Errorf("%s no longer says which test reads it, so the IAM gap in section 5 item 4 is unnamed again", mailboxPlan)
	}
}

// The plan's section 2 is an argument about GRANTS, so its claims about who may
// read and write what are checkable against the merged rules file itself rather
// than against a reader's memory of it. TestMailboxPlanMatchesTheMergedGrants
// parses internal/budget/rules/rules.json.tmpl and holds the plan to what it
// says — the review that produced this test found the plan's M1 taking a lease
// by an ETag read of mailbox/$mb/heartbeat while section 2's own grant table
// named no credential that may read it: the heartbeat's only .read grant is
// MAILBOX (the phone), and CONSUMER "may read no mailbox at all".
//
// Mutation (verified, run and restored): change the heartbeat's ".read" in the
// template to "CONSUMER" and this test fails naming the path; delete the
// heartbeat's ".read" and it fails on the missing grant; add a ".read" under
// the inbox and it fails on the read the plan says does not exist.
func TestMailboxPlanMatchesTheMergedGrants(t *testing.T) {
	doc := readDoc(t, mailboxPlan)
	grants := mailboxGrants(t)

	// What the plan says the merged rules decide (section 2's quotes of the
	// template), asserted against the template itself.
	want := map[string]string{
		"mailbox/$mb/outbox":    "MAILBOX",
		"mailbox/$mb/heartbeat": "MAILBOX",
	}
	for path, expr := range want {
		if got := grants.reads[path]; got != expr {
			t.Errorf("%s says %s has %q as its .read; the merged template says %q", mailboxPlan, path, expr, got)
		}
	}
	if len(grants.reads) != len(want) {
		t.Errorf("the merged mailbox subtree grants %d reads (%v); the plan's section 2 lists %d (%v)", len(grants.reads), sortedKeys(grants.reads), len(want), sortedKeys(want))
	}
	for path, expr := range grants.reads {
		if strings.Contains(expr, "CONSUMER") {
			t.Errorf("%s (via section 2) says CONSUMER may read no mailbox; the merged template grants %q at %s", mailboxPlan, expr, path)
		}
	}
	for path := range grants.reads {
		if strings.HasPrefix(path, "mailbox/$mb/inbox") {
			t.Errorf("%s's C1 says the inbox has no .read grant at all; the merged template grants one at %s", mailboxPlan, path)
		}
	}
	// C3: the heartbeat is the consumer's only replaceable node. The two
	// CONSUMER writes are the create-only outbox message and the heartbeat,
	// and the heartbeat's is the one without the create-only clause.
	if len(grants.consumerWrites) != 2 {
		t.Errorf("the merged mailbox subtree has %d CONSUMER writes (%v); the plan's C3 rests on exactly two — the outbox and the heartbeat", len(grants.consumerWrites), sortedKeys(grants.consumerWrites))
	}
	for path, expr := range grants.consumerWrites {
		switch path {
		case "mailbox/$mb/outbox/$msg":
			if !strings.Contains(expr, "!data.exists()") {
				t.Errorf("%s says the outbox write is create-only (C2, section 6); the merged template's %s is %q", mailboxPlan, path, expr)
			}
		case "mailbox/$mb/heartbeat":
			if strings.Contains(expr, "!data.exists()") {
				t.Errorf("%s's C3 rests on the heartbeat being the replaceable node; the merged template's %s is create-only (%q)", mailboxPlan, path, expr)
			}
		default:
			t.Errorf("the merged template grants CONSUMER a write at %s (%q); the plan's section 2 knows only the outbox and the heartbeat", path, expr)
		}
	}
	if got := grants.writes["mailbox/$mb/inbox/$msg"]; !strings.HasPrefix(got, "MAILBOX") {
		t.Errorf("%s says the phone creates inbox messages; the merged template's inbox write is %q", mailboxPlan, got)
	}

	// The plan-side half: the read half of M1's CAS has a credential named, and
	// it is the one the rules leave (no grant admits the consumer to a read).
	m1 := planSection(t, doc, "### M1 —")
	s2 := planSection(t, doc, "## 2. What the merged rules")
	s6 := planSection(t, doc, "## 6. Where state lives")
	for _, section := range []struct{ name, text string }{{"M1", m1}, {"2", s2}, {"6", s6}} {
		if !strings.Contains(section.text, "ADC") {
			t.Errorf("%s no longer names the operator's ADC as the reader of the heartbeat: no merged grant admits the consumer to that read, so the CAS cannot run under its own credential", section.name)
		}
	}
	for _, want := range []string{"GetETag", "if-match"} {
		if !strings.Contains(m1, want) {
			t.Errorf("%s's M1 no longer says %q: the arbitration is the store's precondition, which holds whatever credential performs it", mailboxPlan, want)
		}
	}

	// M8's sweeper deletes the consumer's own state, never a store user, and
	// the plan says so in this one place rather than contradicting section 6.
	m8 := planSection(t, doc, "### M8 —")
	if strings.Contains(doc, "its user keys are `m~") {
		t.Errorf("%s says the mailbox sweeper's user keys are m~ again: that rule is P6.6's and is not approved (section 6, section 16), and P6.13's sweeper deletes stored prompts and nothing more", mailboxPlan)
	}
	for _, want := range []string{"deletes no Firebase user", "refuse and report"} {
		if !strings.Contains(m8, want) {
			t.Errorf("%s's M8 no longer says %q: what the P4.1 sweeper may delete, and what it must refuse, is the finding a review of this plan raised", mailboxPlan, want)
		}
	}
}

// grantsByPath is the .read and .write expressions under the rules' mailbox
// subtree, keyed by their path in the tree. Only the mailbox subtree is walked:
// the plan is an argument about mailbox grants.
type grantsByPath struct {
	reads, writes, consumerWrites map[string]string
}

// tmplPlaceholderRE matches the template's {{.MaxKeyLength}}-style parameters,
// which make rules.json.tmpl not-quite-JSON. Replacing them with a number is
// what lets this test parse the file rather than pattern-match it.
var tmplPlaceholderRE = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// mailboxGrants parses internal/budget/rules/rules.json.tmpl as it is on this
// branch and returns the grants under `rules.mailbox`, macros unexpanded — which
// is the form the plan quotes ("MAILBOX", "CONSUMER") and the form whose names
// are meaningful: Generate() expands them into the same expressions the emulator
// evaluates, and would hide which claim a grant names.
func mailboxGrants(t *testing.T) grantsByPath {
	t.Helper()
	raw, err := os.ReadFile("../../internal/budget/rules/rules.json.tmpl")
	if err != nil {
		t.Fatalf("reading the merged rules template: %v", err)
	}
	var doc struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(tmplPlaceholderRE.ReplaceAll(raw, []byte("0")), &doc); err != nil {
		t.Fatalf("parsing the merged rules template: %v", err)
	}
	mb, ok := doc.Rules["mailbox"].(map[string]any)
	if !ok {
		t.Fatal("the merged rules template has no rules.mailbox subtree")
	}
	g := grantsByPath{reads: map[string]string{}, writes: map[string]string{}, consumerWrites: map[string]string{}}
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		for key, v := range node {
			switch {
			case key == ".read" || key == ".write":
				expr, _ := v.(string)
				if key == ".read" {
					g.reads[path] = expr
				} else {
					g.writes[path] = expr
					if strings.Contains(expr, "CONSUMER") {
						g.consumerWrites[path] = expr
					}
				}
			case strings.HasPrefix(key, "."):
				// .validate and friends: values are expressions, not nodes.
			default:
				if child, ok := v.(map[string]any); ok {
					walk(child, path+"/"+key)
				}
			}
		}
	}
	walk(mb, "mailbox")
	if len(g.reads) == 0 {
		t.Fatal("no .read grant found under rules.mailbox: the walk, not the rules, is what broke")
	}
	return g
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
