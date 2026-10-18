package cli

import (
	"fmt"
	"os"
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
// to touch (the plan's Global Constraints 1, 8 and 9; D120).
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
				"fugaro init --firebase",
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
				t.Errorf("%s carries %q, which would make an owner-run check something else: these checks are yours to run at your own terminal, on fugaro-dev, against the real database, and no credential and no public store move belongs in one (the plan's Global Constraints 1, 8 and 9; C8)", c.heading, refuse)
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
	for _, c := range []struct{ file, format, why string }{
		{"../infra/firebasedb.go", "deploys the security rules (%d bytes, generated)",
			"check 82 step 3 quotes the rules act line"},
		{"../infra/firebasedb.go", "records the rules generation (%s = %d)",
			"check 82 step 3 quotes the marker act line"},
	} {
		if !strings.Contains(readSrc(t, c.file), c.format) {
			t.Errorf("%s does not carry the format %q: %s", c.file, c.format, c.why)
		}
	}

	// Check 81 is the live half of P2.2's emulator cases, and it names them.
	emulator := readSrc(t, "../budget/rules/emulator_mailbox_test.go")
	section81 := docSection(t, path, doc, "## Check 81:")
	for _, name := range []string{
		"TestMailboxPhoneWritesItsOwnInbox",
		"TestMailboxMessageShape",
		"TestMailboxPhoneAcksWhatItSpoke",
		"TestMailboxClaimMismatchIsAdmittedNowhere",
		"TestMailboxReadsAreTheGrantedSet",
		"TestMailboxResidualMintedTokenPassesUntilPhase6",
	} {
		if !strings.Contains(emulator, "func "+name+"(") {
			t.Errorf("internal/budget/rules/emulator_mailbox_test.go has no %s: check 81 says it is the live half of these cases, and a case that was renamed or removed leaves the claim naming nothing", name)
		}
		if !strings.Contains(section81, name) {
			t.Errorf("## Check 81: does not name %s, one of the P2.2 cases it is the live counterpart of", name)
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
