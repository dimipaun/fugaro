package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestDocsMobileChecklistHasCheck81And82And89 pins P4.2a's half of the mobile plan
// in docs/gcp-live-checklist.md: the three rows of the summary table, the three
// sections they point at, and the register paragraph that records which task
// wrote them.
//
// The register's shape is the point of the test rather than decoration, exactly
// as it is for the website work's 69a/69b: a check that exists as a section but
// not as a row is a check nobody reads first; a row with no section is a command
// nobody can follow; and a check written as something an agent runs is a check
// that will be run by an agent -- all of 80-89 are owner-run because a live
// database's rules, a real installation and a store account are not an agent's
// to touch (the plan's Global Constraints 9, 10 and 11: store credentials are owner-run, no test may need a live cloud or a device, and `belong` is never touched by an agent; D120).
//
// Three ties hold the text to the code, and each of them is the thing the plan's
// task asks the check to read rather than remember:
//
//   - check 82's marker line is **derived** from projectpolicy here, so a bump
//     to RulesVersion moves the document this test requires instead of leaving a
//     check that reads back a generation the binary no longer deploys;
//   - check 82's rules line carries rules.Generate()'s own length, so changing
//     the template without revisiting the check fails here;
//   - the sentence check 82 quotes ("already has the rules"), the predicate
//     check 81 runs against (rules.go's userClaim) and the two act-line formats
//     it names must exist in the sources the checks name -- a check that quotes
//     a sentence or a line no longer printed is a `FACT` line nobody can record.
func TestDocsMobileChecklistHasCheck81And82And89(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// The register paragraph: P4.2a wrote 81, 82 and 89, and the rest of the
	// allocation is untouched and still Task P4.2's to write.
	for _, want := range []string{
		"Task P4.2a",
		"80 and 83–88 are still unwritten and belong to Task P4.2",
		"mobile/README.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not carry %q: the register says which task wrote which check, and which numbers are still empty (Task P4.2a writes 81, 82 and 89; 80 and 83–88 stay P4.2's)", path, want)
		}
	}

	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, n := range []string{"| 81 |", "| 82 |", "| 89 |"} {
			if strings.HasPrefix(line, n) {
				if rows[n] != "" {
					t.Errorf("%s has more than one %q row: one check, one row", path, strings.Trim(n, "| "))
				}
				rows[n] = line
			}
		}
	}
	for _, c := range []struct {
		n     string
		wants []string
	}{
		{"| 81 |", []string{"USER-RUN, NOT RUN", "**you** run it", "never `belong`", "No agent runs any step", `See "Check 81" below`}},
		{"| 82 |", []string{"USER-RUN, NOT RUN", "**you** run it", "never `belong`", "No agent applies rules", `See "Check 82" below`}},
		{"| 89 |", []string{"USER-RUN, NOT RUN", "**you** run it", "no agent runs any step of it", `See "Check 89" below`}},
	} {
		row := rows[c.n]
		if row == "" {
			// Not a Fatalf: the sections below are checked either way, so the
			// failure names both halves of what is missing at once -- the row a
			// reader sees first, and the section it points at.
			t.Errorf("the summary table has no %q row: the check is in the register's index or it is not in the register", strings.Trim(c.n, "| "))
			continue
		}
		for _, want := range c.wants {
			if !strings.Contains(row, want) {
				t.Errorf("the %q row does not carry %q: %s", strings.Trim(c.n, "| "), want, row)
			}
		}
	}

	for _, c := range []struct {
		heading string
		wants   []string
		// refuses are strings the section may NOT carry. Each is a way for an
		// owner-run live check to turn into something else: an environment-gated
		// harness an agent can be pointed at (FUGARO_LIVE_ is how the owner-run
		// live tests in internal/e2e are switched on), a legacy credential
		// (a database secret is not a check's to use), a stale generation (a
		// check that reads back generation 1 is a check of the old document) and
		// a store move the plan's C8 rules out (a promotion, or a token that
		// would let CI submit anything).
		refuses []string
	}{
		{
			heading: "## Check 81:",
			wants: []string{
				"NOT RUN",
				"never `belong`",
				"fugaro-dev",
				"fugaro doctor --project fugaro",
				"auth.token.mu",
				"auth.token.mc",
				"config/caps",
				"spend/$day",
				"config/kill",
				"TestMailboxPhoneWritesItsOwnInbox",
				"TestMailboxReadsAreTheGrantedSet",
				"TestMailboxResidualMintedTokenPassesUntilPhase6",
				"Permission denied",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
				"Restore",
			},
			refuses: []string{"FUGARO_LIVE_", "database secret"},
		},
		{
			heading: "## Check 82:",
			wants: []string{
				"NOT RUN",
				"never `belong`",
				"fugaro-dev",
				"fugaro update --firebase-project",
				"config/limits/policyRules",
				"projectpolicy.RulesVersion",
				"policy-rules-skew",
				"No agent applies rules",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
				"Restore",
			},
			refuses: []string{"FUGARO_LIVE_", "policyRules = 1"},
		},
		{
			heading: "## Check 89:",
			wants: []string{
				"NOT RUN",
				"mobile/app.json",
				"mobile/eas.json",
				"eas build",
				"eas submit",
				"distribution",
				"gitCommitHash",
				"Global Constraint 9",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
				"Restore",
			},
			refuses: []string{"FUGARO_LIVE_", "EXPO_TOKEN", "track: production", "promote"},
		},
	} {
		section := docSection(t, path, doc, c.heading)
		for _, want := range c.wants {
			if !strings.Contains(section, want) {
				t.Errorf("%s does not carry %q: a live check says who runs it, what to run and look at, when it passes and what failing looks like (plan Task P4.2a)", c.heading, want)
			}
		}
		for _, refuse := range c.refuses {
			if strings.Contains(section, refuse) {
				t.Errorf("%s carries %q, which would make an owner-run check something else: these checks are yours to run at your own terminal, on fugaro-dev, against the real database, and no credential and no public store move belongs in one (the plan's Global Constraints 9, 10 and 11; C8)", c.heading, refuse)
			}
		}
	}

	// Check 82's two numbers are the code's, not typed from memory.
	gen, err := rules.Generate()
	if err != nil {
		t.Fatalf("generating the rules: %v", err)
	}
	section82 := docSection(t, path, doc, "## Check 82:")
	for _, want := range []string{
		fmt.Sprintf("records the rules generation (%s = %d)", projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion),
		fmt.Sprintf("(%d bytes, generated)", len(gen)),
	} {
		if !strings.Contains(section82, want) {
			t.Errorf("## Check 82: does not carry %q: it reads back the marker this binary records and the document this binary generates, so the check moves with internal/projectpolicy's RulesVersion and internal/budget/rules' template rather than naming a generation of its own", want)
		}
	}

	// The sentences the checks quote, and the predicate check 81 runs against,
	// are in the sources the checks name and in the checklist, word for word.
	for _, c := range []struct {
		file, literal, why string
	}{
		{"init_firebase.go", "The database already has the rules, mark, project name and mode.",
			"check 82 step 4 quotes the line init prints for a database already in the wanted state"},
		{"../budget/rules/rules.go", "auth != null && auth.token.mu != null && auth.token.mu == auth.uid",
			"check 81 states the user predicate (rules.go's userClaim) it runs against"},
	} {
		src := readSrc(t, c.file)
		if !strings.Contains(src, c.literal) {
			t.Errorf("%s does not carry %q, which the checklist quotes: %s", c.file, c.literal, c.why)
		}
		if !strings.Contains(doc, c.literal) {
			t.Errorf("%s does not carry %q, which %s carries: a check that quotes a sentence no longer printed is a FACT line nobody can record", path, c.literal, c.file)
		}
	}

	// The two act lines are format strings in the source and rendered lines in
	// the checklist, so each is held to its own side: the format must exist in
	// DB.Plan, and the rendered forms -- the marker line and the byte count --
	// are the two strings above, derived rather than typed.
	const rulesFormat = "deploys the security rules (%d bytes, generated)"
	const markerFormat = "records the rules generation (%s = %d)"
	for _, c := range []struct{ file, format, why string }{
		{"../infra/firebasedb.go", rulesFormat,
			"check 82 step 3 quotes the rules act line"},
		{"../infra/firebasedb.go", markerFormat,
			"check 82 step 3 quotes the marker act line"},
	} {
		if !strings.Contains(readSrc(t, c.file), c.format) {
			t.Errorf("%s does not carry the format %q: %s", c.file, c.format, c.why)
		}
	}

	// And the checklist prints them in the order the plan prints them: DB.Plan
	// appends the rules action before the marker's own `d.put` call, and
	// deployDatabase prints the actions in slice order, so the rules line comes
	// first and the marker line last. That order is the check's substance, not
	// its style: the owner reads the plan before typing the project's name, and
	// the claim "a deploy that fails leaves no marker" is true because the
	// marker is what comes after the rules. The reviewer's finding: a check that
	// lists them the other way round teaches the owner that a marker-first plan
	// is what a correct deploy prints.
	dbSrc := readSrc(t, "../infra/firebasedb.go")
	rulesAt, markerAt := strings.Index(dbSrc, rulesFormat), strings.Index(dbSrc, markerFormat)
	if rulesAt < 0 || markerAt < 0 {
		t.Errorf("../infra/firebasedb.go does not carry both act-line formats, so the order check 82 asserts cannot be read from it")
	} else if rulesAt > markerAt {
		t.Errorf("DB.Plan now plans the rules marker (offset %d) before the rules deploy (offset %d): check 82 step 3 still prints the rules line first, and that order is what makes the marker's presence a statement about the document under it -- rewrite the check if the plan really changed", markerAt, rulesAt)
	}
	docRulesAt, docMarkerAt := strings.Index(section82, "rules: "+fmt.Sprintf("deploys the security rules (%d bytes, generated)", len(gen))), strings.Index(section82, fmt.Sprintf("records the rules generation (%s = %d)", projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion))
	if docRulesAt < 0 || docMarkerAt < 0 {
		// Already reported by the derived-string loop above; the order is only
		// readable once both are there.
	} else if docRulesAt > docMarkerAt {
		t.Errorf("## Check 82: prints the marker line before the rules line, which is the reverse of the plan's order (DB.Plan plans PutRules first, the marker last): the check tells the owner to record the act lines in the order printed, so it has to show them that way")
	}

	// Check 81 is the live half of P2.2's and P6.4's emulator cases, and it
	// names them. P6.4 moved the mailbox's tag and device-node cases into
	// emulator_pairing_test.go and renamed the residual, so each name carries
	// the file it has to be in: a name checked against the wrong file passes on
	// a case that is not there.
	section81 := docSection(t, path, doc, "## Check 81:")
	for _, c := range []struct{ file, name string }{
		{"emulator_mailbox_test.go", "TestMailboxPhoneWritesItsOwnInbox"},
		{"emulator_mailbox_test.go", "TestMailboxMessageShape"},
		{"emulator_mailbox_test.go", "TestMailboxPhoneAcksWhatItSpoke"},
		{"emulator_mailbox_test.go", "TestMailboxClaimMismatchIsAdmittedNowhere"},
		{"emulator_mailbox_test.go", "TestMailboxReadsAreTheGrantedSet"},
		{"emulator_mailbox_test.go", "TestMailboxResidualMintedTokenNeedsTheDeviceNode"},
		{"emulator_pairing_test.go", "TestDeviceNodeGatesEverythingUserReaches"},
		{"emulator_pairing_test.go", "TestMailboxMessagesRequireTheTag"},
	} {
		src := readSrc(t, "../budget/rules/"+c.file)
		if !strings.Contains(src, "func "+c.name+"(") {
			t.Errorf("internal/budget/rules/%s has no %s: check 81 says it is the live half of these cases, and a case that was renamed or moved leaves the claim naming nothing", c.file, c.name)
		}
		if !strings.Contains(section81, c.name) {
			t.Errorf("## Check 81: does not name %s, one of the P2.2/P6.4 cases it is the live counterpart of", c.name)
		}
	}
}

// TestDocsMobileChecklistHasChecks100To104 pins P6.11's half of the mobile plan
// in docs/gcp-live-checklist.md: the five summary-table rows, the five sections
// they point at, the register paragraph that records whose numbers they are and
// which task wrote them, and the guards, each of which has to stop for a reason
// the owner can read (the task that builds what is missing) rather than looking
// like a broken checkout.
//
// The five are the mobile functional spec v0.1's Phase 6 checks, and the
// reconciliation's §5 maps its own unverified claims onto them -- U5 is 100, U2
// is 101, U1 is 102, U4 is 103 and U3 is 104. That mapping is read out of that
// document's table here rather than restated, and every row and every section
// has to name its U: a check that quietly answers a different claim is the
// failure this half exists for.
//
// Check 100 is the one the owner amended, so it is the one this test holds
// hardest. The register has to say, in the section and not only in the row, that
// **pairing is discovery, not identity**: the code carries the project's config
// and one short-lived `p~` custom token and no key, grants no access by itself,
// and the allowlist is the device node (`rules.PathDevices`, derived here rather
// than typed) whose entry carries the **administered email**, with an unverified
// address **refused at pairing** and sign-in left at **custom tokens only**. The
// last two are the code's today for the CLI, so they are held to it:
// init_members.go's `userEmail` -- which `authenticatedUser` calls -- is what
// refuses an unverified address and a service account's, and init_firebase.go's
// `refusePublicSignUp` is what refuses a public sign-in, with the sentence the
// register quotes in its message. The three documents that carry the amendment
// (docs/mobile/README.md's line of 2026-10-20, the spec's own amendment notes and
// this register) are required to say the same thing, so a check written from the
// older reading of pairing fails here instead of reading as checked.
//
// What this test does NOT do is assert that the paths a guard stops on are
// missing: the guard is `[ -f ... ] || stop`, which is right on both sides of the
// task landing, and an assertion of absence would have to be deleted by whichever
// of P6.6, P6.7, P6.8, P6.10 or P6.12 merges next -- the task id the guard names
// is the durable half of "this stops for a known reason".
func TestDocsMobileChecklistHasChecks100To104(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	flat := strings.Join(strings.Fields(doc), " ")

	// The paragraph: the allocation, the task, and that none of the five has run.
	for _, want := range []string{
		"are written by Task P6.11 (2026-10-22)",
		"**105–109 are reserved and unused**",
		"**U5** is 100, **U2** is 101, **U1** is 102, **U4** is 103 and **U3** is 104",
		"the highest written `## Check N` section in this file was **113**",
		"**All five are owner-run and none has been run.**",
		"Each needs a **release build** of `fugaro` that carries `fugaro pair` — **0.10.0 or later**",
		"Check 103 is aimed at the sandbox repository and never at `belong`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not carry %q: the register is where a reader learns whose numbers 100–104 are, which task wrote them, when they may be run and that none has been (plan Task P6.11)", path, want)
		}
	}

	// The mapping is the reconciliation's table, read rather than restated.
	reconPath := "docs/mobile/functional-spec-reconciliation.md"
	recon := readDoc(t, reconPath)
	uRE := regexp.MustCompile(`^\| (U[1-5]) \|.*check ([0-9]{3})`)
	wantU := map[string]string{}
	for _, line := range strings.Split(recon, "\n") {
		if m := uRE.FindStringSubmatch(line); m != nil {
			wantU[m[2]] = m[1]
		}
	}
	if len(wantU) != 5 {
		t.Fatalf("%s's UNVERIFIED table names %d of the five U-numbers against a check number, want 5: this test reads the 100–104 mapping out of that table, and a table it cannot read is a mapping nothing holds the register to", reconPath, len(wantU))
	}

	for _, c := range []struct {
		n       string
		row     []string
		wants   []string
		stops   []string
		mustNot []string
	}{
		{
			n:   "100",
			row: []string{"Pairing is discovery, not identity", "no key", "device node", "administered email", "unverified email is refused", "custom tokens only", "never `belong`", "fugaro-dev", "with your own phone"},
			wants: []string{
				"NOT RUN",
				"**Pairing is discovery, not identity",
				"grants no access by itself",
				rules.PathDevices,
				"**administered email**",
				"**refuses an unverified email**",
				"`userEmail`",
				"`email_verified`",
				"`refusePublicSignUp`",
				"**custom tokens only**",
				"The web API key is public, so only custom tokens signed by Fugaro's signer may work",
				"`PAIRING`",
				"`pairing/<the p~ uid>/request`",
				"`PUBKEY`",
				"**FACT:**",
				"**Passes when:**",
				"**Fails when:**",
				"**Paste back:**",
				"**Restore.**",
				"never `belong`",
			},
			stops:   []string{"[ -f \"$ROOT/internal/cli/pair.go\" ]", "[ -d \"$ROOT/mobile/src/pairing\" ]", "Task P6.6", "Task P6.8", `*dev*`},
			mustNot: []string{"FUGARO_LIVE_", "database secret"},
		},
		{
			n:   "101",
			row: []string{"revoke", "device node", "before", "never `belong`", "fugaro-dev"},
			wants: []string{
				"NOT RUN",
				"`fugaro pair revoke`",
				"`$DEV`",
				"`$OLD`",
				"never `$UID`",
				"deletes the **node first**",
				"denied",
				rules.PathDevices,
				"**FACT:**",
				"**Passes when:**",
				"**Fails when:**",
				"**Paste back:**",
				"**Restore.**",
			},
			stops:   []string{"[ -f \"$ROOT/internal/cli/pair.go\" ]", "Task P6.6"},
			mustNot: []string{"FUGARO_LIVE_", "database secret"},
		},
		{
			n:   "102",
			row: []string{"recognition rate", "oldest", "airplane mode", "misroutes"},
			wants: []string{
				"NOT RUN",
				"**recognition rate**",
				"airplane mode",
				"**false-accept count**",
				"**recognised over W**",
				"N − W",
				"near miss",
				"Check 85",
				"**FACT:**",
				"**Passes when:**",
				"**Fails when:**",
				"**Paste back:**",
				"**Restore.**",
			},
			stops:   []string{"[ -d \"$ROOT/mobile/src/voice\" ]", "[ -f \"$ROOT/internal/cli/pair.go\" ]", "Task P6.6", "P6.8", "P6.12"},
			mustNot: []string{"FUGARO_LIVE_", "EXPO_TOKEN"},
		},
		{
			n:   "103",
			row: []string{"cancel", "merge", "edgeappinc/fugarosandbox", "never `belong`", "misheard", "replayed", "another target"},
			wants: []string{
				"NOT RUN",
				"on the sandbox repository only",
				"edgeappinc/fugarosandbox",
				"never `belong`",
				"read-back",
				"**A misheard confirm.**",
				"**A replayed confirm.**",
				"**A confirm for another target.**",
				"branch protection",
				"D169",
				"§3.1",
				"**FACT:**",
				"**Passes when:**",
				"**Fails when:**",
				"**Paste back:**",
				"**Restore.**",
			},
			stops:   []string{"[ -f \"$ROOT/internal/cli/mailbox.go\" ]", "P6.7", "REPO=${REPO:-edgeappinc/fugarosandbox}", "case \"$REPO\" in edgeappinc/fugarosandbox)", "never on belong"},
			mustNot: []string{"FUGARO_LIVE_", "database secret", "belong/"}, // `belong` is named to forbid it, never as a target
		},
		{
			n:   "104",
			row: []string{"offline", "never `belong`", "nothing is delivered late", "action record"},
			wants: []string{
				"NOT RUN",
				"airplane mode",
				"stale",
				"action record",
				"delivered late",
				"mailbox/$MB/outbox.json",
				"**FACT:**",
				"**Passes when:**",
				"**Fails when:**",
				"**Paste back:**",
				"**Restore.**",
			},
			stops:   []string{"[ -d \"$ROOT/mobile/src/mailbox\" ]", "[ -f \"$ROOT/internal/cli/mailbox.go\" ]", "P6.10", "P6.7", "Task P6.6"},
			mustNot: []string{"FUGARO_LIVE_", "database secret"},
		},
	} {
		u := wantU[c.n]
		if u == "" {
			t.Errorf("%s's UNVERIFIED table names no check %s: the register writes it, and that table is what this test reads its U-number from", reconPath, c.n)
			continue
		}

		// The row, first: it is what the owner reads before the section.
		row := ""
		for _, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(line, "| "+c.n+" |") {
				if row != "" {
					t.Errorf("%s has more than one `| %s |` row: one check, one row", path, c.n)
				}
				row = line
			}
		}
		if row == "" {
			t.Errorf("the summary table has no `| %s |` row: the check is in the register's index or it is not in the register", c.n)
		} else {
			for _, want := range append([]string{"USER-RUN, NOT RUN", "**you** run it", "no agent runs any step of it", "**" + u + "**", `See "Check ` + c.n + `" below`}, c.row...) {
				if !strings.Contains(row, want) {
					t.Errorf("the `| %s |` row does not carry %q: %s", c.n, want, row)
				}
			}
		}

		// Then the section, its guard, and the reason the guard stops.
		section := docSection(t, path, doc, "## Check "+c.n+":")
		for _, want := range append([]string{"**" + u + "**", "**Guard block"}, c.wants...) {
			if !strings.Contains(section, want) {
				t.Errorf("## Check %s: does not carry %q: a live check says whose claim it settles, what to run and look at, when it passes and what failing looks like (plan Task P6.11; the reconciliation's %s)", c.n, want, u)
			}
		}
		for _, refuse := range c.mustNot {
			if strings.Contains(section, refuse) {
				t.Errorf("## Check %s: carries %q, which would make an owner-run check something else: these five are yours to run after the release that carries `fugaro pair`, and no environment-gated harness, database secret or store credential belongs in one (the plan's Global Constraints 9, 10 and 11; C8)", c.n, refuse)
			}
		}
		guard := codeBlockAfter(t, section, "# === Check "+c.n+": setup + guard.")
		if !strings.Contains(guard, "set -u") {
			t.Errorf("## Check %s: the guard block does not open with `set -u`: an unset variable in a block the owner re-runs before every step is a command that runs against the wrong thing instead of stopping", c.n)
		}
		for _, want := range c.stops {
			if !strings.Contains(guard, want) {
				t.Errorf("## Check %s: the guard block does not carry %q: each guard stops on a path this checkout may not have and names the task that builds it, so a stop the owner reads says which task is missing rather than looking like a broken checkout (Task P6.11; P6.6, P6.7, P6.8, P6.10, P6.12)", c.n, want)
			}
		}
	}

	// The amendment check 100 is written to, in all three places it is recorded:
	// docs/mobile/README.md's status line of 2026-10-20, the spec's own amendment
	// notes, and the register's section. Each sentence is required in each
	// document, so a register that drifted back to "the QR is the identity" -- or a
	// README that dropped the amendment the register cites -- fails here.
	for _, want := range []string{
		"pairing is discovery, not identity",
		"custom tokens only",
	} {
		for _, d := range []string{"docs/mobile/README.md", "docs/mobile/functional-spec-v0.1.md"} {
			if !strings.Contains(strings.Join(strings.Fields(readDoc(t, d)), " "), want) {
				t.Errorf("%s does not carry %q: check 100 is written to the amendment of 2026-10-20, and the documents that record it are where the sentence comes from", d, want)
			}
		}
	}
	for _, want := range []string{"The entry carries an administered email, and an unverified email is refused at pairing"} {
		if !strings.Contains(strings.Join(strings.Fields(readDoc(t, "docs/mobile/functional-spec-v0.1.md")), " "), want) {
			t.Errorf("docs/mobile/functional-spec-v0.1.md does not carry %q, which check 100 step 4 records: that note is the amendment's own text (edit 7), and the check reads the refusal out of it rather than provoking it", want)
		}
	}

	// And the last two halves of that rule are the code's today for the CLI, so
	// they are held to it: userEmail (which authenticatedUser calls) refuses an
	// unverified address and a service account's, and refusePublicSignUp refuses a
	// public sign-in with the sentence the register quotes.
	members := readSrc(t, "init_members.go")
	for _, want := range []string{"func userEmail(", "userEmail(ctx, hc, userinfoURL)", `json:"email_verified"`, "is not verified", ".gserviceaccount.com"} {
		if !strings.Contains(members, want) {
			t.Errorf("internal/cli/init_members.go does not carry %q, which check 100 step 4 names as the shape the pairing refusal already has for the CLI: %s", want, "a symbol that moved leaves the check citing nothing")
		}
	}
	firebase := readSrc(t, "init_firebase.go")
	for _, want := range []string{"func refusePublicSignUp(", "The web API key is public, so only custom tokens signed by Fugaro's signer may work"} {
		if !strings.Contains(firebase, want) {
			t.Errorf("internal/cli/init_firebase.go does not carry %q, which check 100 quotes for the sign-in half of the amendment: sign-in is custom tokens only, and this refusal is the code that makes that true", want)
		}
	}
}

// TestDocsMobileChecks100To104ReadTheIdsTheySet: the three defects the review of
// this task found were all the same one -- a command block reading a variable
// nothing in the check sets. Check 101's two reads of the device node were
// written `config/admin/devices/$UID.json`, and bash defines `UID` as the
// caller's numeric user id, so both negatives (the node absent after the
// revocation, the Auth user absent after the delete) would have read node 1000
// and recorded a false PASS on a check whose whole subject is revocation; step
// 3 read `$OLD`, the ID token captured in step 2, which no block set; and check
// 104's step 1 wrote the mailbox URL with `$MB` -- under `set -u` the block
// aborted at its first use, and pasted on its own it read `mailbox//outbox.json`
// (a null read) and left steps 2-4 recording nothing. Check 81 already carries
// this pin for its own ids (TestDocsCheck81StepsNameTheIdsTheyInherit,
// docs_mobile_checks_review_test.go): a step that reads an id set by another
// step names it in its prerequisite header as "`$ID` from step N", which is
// also why re-running a guard never clears it.
//
// Read here over all five: a variable a step's block reads has to be * assigned
// in the same block, or * assigned by the check's own guard block (which is
// re-run before every step), or * one of the values check 81's guard resolves
// for the register (`$DB`, `$KEY`, `$FP` -- every one of these five says so in
// its preamble) or the shell's own (`$HOME`). Anything else fails. Neither
// `UID` nor `USER` is on that list, deliberately: they are the caller's, not the
// device's, and treating them as resolved is exactly the false PASS this pin
// exists to prevent.
func TestDocsMobileChecks100To104ReadTheIdsTheySet(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// Values the register resolves elsewhere, plus the shell's own.
	resolved := map[string]bool{
		"DB": true, "KEY": true, "FP": true, // check 81's guard block, named in each preamble
		"HOME": true, "PATH": true, "TMPDIR": true,
	}
	stepRE := regexp.MustCompile(`(?m)^(\d+)\. \*\*`)
	fenceRE := regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	assignRE := regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)=`)
	readRE := regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)
	// A comment is not a command. The blocks in these five warn against the very
	// names this test is about, in comments -- check 101's step 1 says "this, not
	// $UID, is the account in question", check 104's says "the mailbox's $mb" --
	// and a comment cannot abort under `set -u` or read an empty string, so
	// nothing after a `#` that starts a word is read here.
	stripComments := func(s string) string {
		var out []string
		for _, line := range strings.Split(s, "\n") {
			if i := strings.Index(line, "#"); i == 0 || (i > 0 && (line[i-1] == ' ' || line[i-1] == '\t')) {
				line = line[:i]
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}

	for _, n := range []string{"100", "101", "102", "103", "104"} {
		section := docSection(t, path, doc, "## Check "+n+":")
		at := stepRE.FindAllStringSubmatchIndex(section, -1)
		if len(at) < 2 {
			t.Fatalf("## Check %s: carries %d numbered steps, so this test cannot read a step's header and its block", n, len(at))
		}
		type step struct{ n, header, cmds string }
		var steps []step
		for i, loc := range at {
			end := len(section)
			if i+1 < len(at) {
				end = at[i+1][0]
			}
			text := section[loc[0]:end]
			header, _, _ := strings.Cut(text, "\n")
			var b strings.Builder
			for _, m := range fenceRE.FindAllStringSubmatch(text, -1) {
				b.WriteString(m[1])
			}
			steps = append(steps, step{n: section[loc[2]:loc[3]], header: header, cmds: stripComments(b.String())})
		}
		// The ids each step's own block assigns, and the check's guard block
		// (every fence before the first numbered step) as a step of its own: it
		// is re-run before every step, so it resolves an id for all of them.
		guard := section[:at[0][0]]
		setters := map[string]string{}
		var guardCmds strings.Builder
		for _, m := range fenceRE.FindAllStringSubmatch(guard, -1) {
			guardCmds.WriteString(stripComments(m[1]))
		}
		for _, st := range append([]step{{n: "guard", cmds: guardCmds.String()}}, steps...) {
			for _, m := range assignRE.FindAllStringSubmatch(st.cmds, -1) {
				if _, seen := setters[m[1]]; !seen {
					setters[m[1]] = st.n
				}
			}
		}
		guardSet := map[string]bool{}
		for _, m := range assignRE.FindAllStringSubmatch(guardCmds.String(), -1) {
			guardSet[m[1]] = true
		}
		if len(guardSet) == 0 {
			t.Fatalf("## Check %s: the guard block assigns nothing, so this test cannot tell a value the guard resolves from one nothing sets -- read the guard before changing the check", n)
		}
		for _, st := range steps {
			if st.cmds == "" {
				continue
			}
			own := map[string]bool{}
			for _, m := range assignRE.FindAllStringSubmatch(st.cmds, -1) {
				own[m[1]] = true
			}
			for _, m := range readRE.FindAllStringSubmatch(st.cmds, -1) {
				id := m[1]
				if own[id] || guardSet[id] || resolved[id] {
					continue
				}
				setter, ok := setters[id]
				if !ok {
					t.Errorf("## Check %s step %s: a command block reads `$%s` and nothing in this check sets it -- no step assigns it, the guard block does not, and it is not one of the values check 81's guard resolves (`$DB`, `$KEY`, `$FP`) or the shell's: under `set -u` the block aborts at its first use, and pasted on its own it reads an empty string, which is how `$UID` (the caller's numeric user id, not the device's) records a false PASS and how `$MB` leaves the steps after it with nothing to look at", n, st.n, id)
					continue
				}
				want := "`$" + id + "` from step " + setter
				if !strings.Contains(st.header, want) {
					t.Errorf("## Check %s step %s: the block reads `$%s`, which step %s sets, and the step's prerequisite header does not say %q: the header is the list an owner runs the step from, and with `set -u` on an id the block does not set the step aborts after recording the statuses above it (the model is check 81's `$MSG`/`$OUT`, held by TestDocsCheck81StepsNameTheIdsTheyInherit)", n, st.n, id, setter, want)
				}
			}
		}
	}
}

// readSrc reads a Go source file of this checkout, relative to internal/cli.
func readSrc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
