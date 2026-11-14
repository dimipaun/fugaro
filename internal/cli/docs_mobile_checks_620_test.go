package cli

// Checks 620 to 627 — the mobile-auth work's eight owner-run live checks — as
// the register and that work's plan write them.
//
// The allocation of 2026-11-14 gave the eight checks a number each
// (`docs/gcp-live-checklist.md`'s paragraph above the table, the eight summary
// rows and the eight `## Check N:` sections). A check is registered by two
// things — a row in the summary table and a section below it — and either half
// alone is a number a reader cannot use; every other block in the register
// carries its own assertion for its own numbers (docs_check600_test.go for
// 600a to 600d, docs_checks_520_521_test.go for 520 and 521, and
// docs_dynamic_delivery_test.go for 510 and 511), because the pairing
// register-wide backstop is deliberately absent. This file is that assertion
// for 620 to 627.
//
// Two documents are one statement here. The numbers come from the plan's §9,
// whose table is where each check was proposed by name, and the register is
// where each is written; a number the plan's table carries and the register has
// no section for is a check nobody wrote, and a section whose number the plan
// does not carry is a number nobody sent. So the plan's table, its heading and
// its §10 are read against the register's own halves, and the two **runner**
// rows of §9 are held to their side: the app's own CI job and the resolver's
// rule on a real declaration are not checks the owner runs, and they take no
// number — a number written for one of them would be the register's rule for a
// work that reserved none applied to something that is not an owner-run check.
//
// Nothing here runs a check or reads a cloud project: every assertion is about
// the text of two documents, and no live step of any of the eight is run by an
// agent. What the tests say about a check's own readings — the paths a paired
// phone may read, what `fugaro pair revoke` deletes, the D231 gap Check 623
// reports — is pinned against the sentences the register carries, so a section
// that stopped saying one of them fails here rather than quietly telling its
// owner less than it did.

import (
	"strings"
	"testing"
)

// mobileAuth620Plan is where the eight were proposed, by name, before the
// allocation gave them their numbers.
const mobileAuth620Plan = "docs/mobile-auth/implementation-plan.md"

// mobileAuth620Numbers are the eight, in the order the register writes them.
var mobileAuth620Numbers = []string{"620", "621", "622", "623", "624", "625", "626", "627"}

// mobileAuth620Subjects is the name each check carries in **both** documents:
// the register's section heading is `## Check N: <subject> (...)` and the plan's
// §9 table names it in its second column. The map is the one place the two are
// held together, and a subject that moved in one document and not the other is
// what fails below.
var mobileAuth620Subjects = map[string]string{
	"620": "the pairing that refuses an address it cannot attribute",
	"621": "the paired phone cannot read its own entry",
	"622": "a revoked pairing stops on the next request",
	"623": "a publish never re-adds a revoked entry",
	"624": "a declared entry that is not on the node admits nobody",
	"625": "the QR carries no identity",
	"626": "the first screen with no account",
	"627": "the refusal of a public sign-up still holds with a provider on",
}

// TestDocsMobileChecks620AreRegistered: each of the eight has both halves of
// its registration, both places say it is the owner's and has not been run, and
// the section points at no installation but a sandbox one.
func TestDocsMobileChecks620AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range mobileAuth620Numbers {
		row := checkRow(register, number)
		if row == "" {
			t.Fatalf("%s has no `| %s |` row: Check %s's section is written below it, and the table is where a reader finds it", liveChecksDoc, number, number)
		}
		if !strings.Contains(row, "OWNER-RUN, NOT RUN.") {
			t.Errorf("Check %s's row does not read `OWNER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", number)
		}
		if !strings.Contains(row, `See "Check `+number+`" below`) {
			t.Errorf("Check %s's row does not point at its own section (`See \"Check %s\" below`): the row and the section are one registration, and a row pointing elsewhere sends its reader to another check's steps", number, number)
		}
		if !strings.Contains(row, "**you** run it") {
			t.Errorf("Check %s's row does not say `**you** run it`: every check of this block is owner-run, and the register's own form names who", number)
		}
		if !strings.Contains(row, "never `belong`") {
			t.Errorf("Check %s's row does not refuse `belong`: the register's rule for an owner-run check is that it names the installation it may be run on, and that installation is never the owner's own", number)
		}
		if !strings.Contains(row, "no agent runs any step of it") {
			t.Errorf("Check %s's row does not say `no agent runs any step of it`: the register's own rule for an owner-run check, and a row that leaves it out is one an agent may take for a test", number)
		}

		section := checkSection(t, register, number)
		head, _, _ := strings.Cut(section, "\n")
		flat := strings.ToLower(flattenDoc(section))
		if want := strings.ToLower(mobileAuth620Subjects[number]); !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not name the check %q: the heading is the name the plan's §9 table and the row both carry, and a section that dropped it is a check the reader cannot match to its row", number, mobileAuth620Subjects[number])
		}
		if !strings.Contains(head, "run by you; NOT RUN)") {
			t.Errorf("Check %s's heading is %q: every section of this block is written in the register's own heading form, `(<target>; run by you; NOT RUN)`", number, head)
		}
		for _, want := range []string{
			// Both halves, flattened and folded: the register's prose wraps, and
			// a pin that missed a phrase across a line break would be a pin on
			// the line breaks.
			"not run",
			"no agent runs any step of it",
			"never `belong`",
			// The number is the work's own, so the section says where it came
			// from: the register's paragraph above the table and the plan's §9.
			"allocated on 2026-11-14",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles, on what, and how; a pin that has moved is a sentence the owner no longer reads", number, want)
			}
		}
	}
}

// planTable returns the table a plan carries, from its header to the blank line
// that ends it, so a row counted here is that table's own and not a row of the
// next one.
func planTable(t *testing.T, plan, header string) string {
	t.Helper()
	i := strings.Index(plan, header)
	if i < 0 {
		t.Fatalf("the plan carries no %q table: the rows above are read out of it", header)
	}
	table := plan[i:]
	if end := strings.Index(table, "\n\n"); end >= 0 {
		table = table[:end]
	}
	return table
}

// TestDocsMobileChecks620PlanAndRegisterAgree: the plan proposed the eight by
// name and the register gave them their numbers, so the two documents have to
// say the same thing about each — a number in one and not the other is a check
// nobody allocated, and a runner row carrying a number is the allocation's rule
// applied to something that is not an owner-run check.
func TestDocsMobileChecks620PlanAndRegisterAgree(t *testing.T) {
	plan := readDoc(t, mobileAuth620Plan)
	flat := flattenDoc(plan)

	if !strings.Contains(flat, "the eight owner-run checks below are") {
		t.Errorf("%s's §9 no longer says the eight owner-run checks of the block are the register's: the plan is where the allocation is recorded, and the table below it is the eight rows", mobileAuth620Plan)
	}
	for _, number := range mobileAuth620Numbers {
		if want := "| **" + number + "** | **" + mobileAuth620Subjects[number] + "** |"; !strings.Contains(flat, want) {
			t.Errorf("%s's §9 table has no row %q: the register's Check %s section names the same check, and the two are one statement", mobileAuth620Plan, "| **"+number+"** | **"+mobileAuth620Subjects[number]+"** |", number)
		}
	}
	if !strings.Contains(flat, "**Checks 620 to 627**") {
		t.Errorf("%s's §9 no longer holds the number block as `**Checks 620 to 627**`: the allocation's extent is what makes the eight a block rather than eight single numbers", mobileAuth620Plan)
	}

	// The two runner rows hold no number, read out of the block's own table
	// rather than out of the whole plan: the table's first column is `—` for
	// them, and the eight owner-run rows carry their numbers. A number written
	// on a row that is not owner-run is the allocation's rule applied to
	// something no owner runs, and a row with no number that is not one of the
	// two runner rows is an owner-run check the register has no place for.
	// Raw rather than flattened: the rows are counted by their own lines, and
	// flattenDoc collapses a table's line breaks along with its prose.
	table := planTable(t, plan, "| Check | Proposed check |")
	numbered, unnumbered := 0, 0
	for _, line := range strings.Split(table, "\n") {
		switch {
		case strings.HasPrefix(line, "| **—** | **"):
			unnumbered++
		case strings.HasPrefix(line, "| **6") && strings.Contains(line, "** |"):
			numbered++
		}
	}
	if numbered != len(mobileAuth620Numbers) {
		t.Errorf("%s's §9 table carries %d numbered rows, and the block is %d: the register writes one section per number, and a row whose number the register does not carry is a check nobody allocated", mobileAuth620Plan, numbered, len(mobileAuth620Numbers))
	}
	if unnumbered != 2 {
		t.Errorf("%s's §9 table carries %d rows with no number, and this block's non-owner rows are exactly two — the app's own CI job and the resolver's rule on a real declaration: a third is an owner-run check written without a number", mobileAuth620Plan, unnumbered)
	}

	// And the plan's own numbers section says which numbers the block holds.
	if !strings.Contains(flat, "**Check numbers: 620–627**") {
		t.Errorf("%s's §10 no longer records `**Check numbers: 620–627**`: the block's numbers are the plan's own reading of the register, and a plan that stopped naming them sends its reader to the register to guess", mobileAuth620Plan)
	}
}

// mobileAuth620Set is the eight as a set, so "is this heading one of the
// block's" is asked of the numbers rather than of their spelling.
func mobileAuth620Set() map[string]bool {
	set := make(map[string]bool, len(mobileAuth620Numbers))
	for _, n := range mobileAuth620Numbers {
		set[n] = true
	}
	return set
}

// TestDocsMobileChecks620AreAppended: the register appends a work's sections
// after every section older than them, and in ascending order: a section
// inserted among older ones, or a number written out of order, is how a reader
// loses track of which numbers are live. The block is not pinned as the file's
// **highest** -- a later work appends its own sections below these, and this
// test would then fail for a reason that is not about this block -- so what is
// asserted is that the block's own 620 comes after every heading that is not
// one of the eight, and that 627 is written.
func TestDocsMobileChecks620AreAppended(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	block := mobileAuth620Set()

	prev := 0
	for _, number := range mobileAuth620Numbers {
		at := strings.Index(register, "\n## Check "+number+":")
		if at < 0 {
			t.Fatalf("%s has no `## Check %s:` section: the row above it points at a section that has to be there", liveChecksDoc, number)
		}
		if at < prev {
			t.Errorf("%s writes Check %s out of the block's ascending order: the eight are one appended block, and a reader takes a number written above an older one for a live one", liveChecksDoc, number)
		}
		prev = at
	}

	first := strings.Index(register, "\n## Check 620:")
	lastOlder := -1
	for _, m := range check600NumberRE.FindAllStringSubmatchIndex(register, -1) {
		if m[0] < first && !block[register[m[2]:m[3]]] {
			lastOlder = m[0]
		}
	}
	if lastOlder < 0 {
		t.Fatalf("%s carries no `## Check` section above the mobile-auth block: this assertion would otherwise pass whatever the register holds", liveChecksDoc)
	}
	if first < lastOlder {
		t.Errorf("%s writes Check 620 before the sections that were already written: the block is appended where the register's written sections end, not inserted among them", liveChecksDoc)
	}
}

// TestDocsMobileChecks620NameWhatTheCodeDoes: three of the eight sections state
// something about the tree that a reader takes without checking -- which nodes
// a paired phone may read, what a revoke deletes, and what a publish does with
// a revoked entry -- and each of them was wrong once, in a review of this
// block. A section that drifts back to a path, a node or a rule the code does
// not have is an owner sent to look at the wrong thing, so the sentences are
// pinned against the code's own names here.
func TestDocsMobileChecks620NameWhatTheCodeDoes(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	// Check 621: the phone's reads are its mailbox and the one delivery node,
	// not a subtree under `config/` -- the rules grant it `mailbox/<mu>` (the
	// inbox and the heartbeat) and `pairing/<uid>/delivery`, and
	// rules.PathDevices is `.read: false, .write: false`.
	phone := strings.ToLower(flattenDoc(checkSection(t, register, "621")))
	for _, want := range []string{"mailbox/<uid>", "heartbeat", "pairing/<uid>/delivery"} {
		if !strings.Contains(phone, want) {
			t.Errorf("Check 621's section no longer names %q as a node a paired phone reads: the rules grant the phone its mailbox and the delivery node, and a section that named another path would send its owner to a node that does not exist", want)
		}
	}
	if strings.Contains(phone, "config/mobile/") {
		t.Errorf("Check 621's section names `config/mobile/`: no such node is in the tree, and the rules grant the phone `mailbox/<mu>` and `pairing/<uid>/delivery`")
	}
	// And its restore is the allowlist: `fugaro pair revoke` deletes the device
	// node and the keychain item and leaves the pairing node, delivery among it,
	// to the history sweep -- which is what Check 622 says of the same command.
	for _, want := range []string{"keychain item", "history sweep"} {
		if !strings.Contains(phone, want) {
			t.Errorf("Check 621's section no longer says %q of its restore: a revoke deletes the device node and the laptop's keychain item, and the pairing node is the history sweep's", want)
		}
	}
	if !strings.Contains(phone, "as long as that token's id token lives") {
		t.Errorf("Check 621's section no longer records the residual a revoke leaves: the sealed payload stays readable to the pairing token until its ID token expires, which is the state `internal/cli/pair.go` names when the pairing node cannot be deleted")
	}

	// Check 623: the gap is D231's, and the section has to name the three things
	// that make it -- the node's absence as the seed's gate, the database's
	// pruning of an emptied node, and `GetETag` reading the null body as absent.
	publish := strings.ToLower(flattenDoc(checkSection(t, register, "623")))
	for _, want := range []string{"d231", "getetag", "prunes", "last child", "declared"} {
		if !strings.Contains(publish, want) {
			t.Errorf("Check 623's section no longer says %q: the check reads P5's open D231 gap -- a publish re-seeds every declared entry once the last device's revocation has emptied the node -- and a section that dropped a part of it would report the gap as narrower than it is", want)
		}
	}
}

// TestDocsMobileChecks620MovesTheCensusRow: this file is one more
// `internal/cli/docs_*_test.go` pin, so it moved the running total of them in
// `docs/rigor-modes/reconciliation.md`, whose own rule is that the folder which
// writes one moves the row in the same commit and names itself in the sentence
// beside it -- a row that moved without a sentence is the merge that row calls
// the one that did not.
func TestDocsMobileChecks620MovesTheCensusRow(t *testing.T) {
	const recon = "docs/rigor-modes/reconciliation.md"
	census := flattenDoc(readDoc(t, recon))
	for _, want := range []string{
		"| Docs folders that ship their own `internal/cli/docs_*_test.go` pin | 140 |",
		"Amended 2026-11-14: `internal/cli/docs_mobile_checks_620_test.go`, the pin over the mobile-auth work's owner Checks 620 to 627 in the register",
	} {
		if !strings.Contains(census, want) {
			t.Errorf("%s does not carry %q: the row is a running total of other folders' guards, and the folder that adds one is the folder that moves it", recon, want)
		}
	}
}
