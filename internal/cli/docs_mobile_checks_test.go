package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The mobile functional spec's checks 100–104, written into the register by
// Task P6.11 of docs/mobile/implementation-plan.md (Phase 6, approved by the
// owner on 2026-10-18), from decisions D126, D161–D172 and
// docs/mobile/functional-spec-reconciliation.md.
//
// What these tests are for, and what they are not. The five checks are
// owner-run: no test below runs one, and none of them could — a phone, a
// camera, a keychain and a spoken confirmation are what they need. What is
// testable is that the register carries them at all, in the shape the
// register's own convention requires (a summary row and a section each, the
// installation named, the owner-run status stated, `belong` refused), that
// 105–109 stay reserved and unused, and that the two places where a check can
// do damage are pinned structurally rather than by prose: Check 100's guard
// block hard-stops on the project (and reports, rather than stops on, a device
// name it has already paired — the stop itself belongs to the pairing step,
// because the guard block is re-run), and Check 103's merge cannot reach a
// repository or a pull request that is not its own.
//
// One pin is outside the register: the coordinator's numbers row called 100–104
// unwritten, and writing the sections made that false, so the row and the
// register are held together here too.
//
// The mutations these tests were proved against are recorded on each
// assertion's own comment below.
//
// The names begin with TestDocsMobile for the same reason the mobile plan's own pin does
// (TestDocsMobilePlanPhase6IsApproved, docs_mobile_plan_test.go): Task P6.11's verification is
// `go test ./internal/cli -run 'Docs|Lint'`, and a pin that command does not select verifies
// nothing. The register's other check pins are named TestChecklistHas..., which that filter does
// not reach; these carry both halves in one name.

// mobileFlat collapses every run of whitespace in s to one space. The
// register's prose is wrapped to a column, so a phrase it carries can be split
// across two lines by a later edit that changes nothing else. Every prose
// assertion below matches against the flattened text for that reason: a pin
// that fails because a paragraph was rewrapped is a pin that says nothing about
// what the check claims.
func mobileFlat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// mobileCheck is one of the five checks, as the register writes it: the number
// as the table's first cell, the heading docSection is anchored to, and the
// phrases its row and its section must carry.
type mobileCheck struct {
	num     string
	heading string
	// rowWants are the phrases the summary row must carry: the owner-run
	// status, the pointer at the section, and the installation.
	rowWants []string
	// sectionWants are the phrases the section must carry beyond the four
	// every section carries (see mobileCheckSectionCommon).
	sectionWants []string
	// sectionRefuses are phrases that must not appear in the section.
	sectionRefuses []string
}

// mobileCheckSectionCommon is what every one of the five sections carries,
// whatever it checks: the register's own words for an owner-run check, and
// the refusal of `belong` that every check in this file makes.
var mobileCheckSectionCommon = []string{
	"NOT RUN",
	"run by you",
	"never `belong`",
	"No agent runs any step",
}

var mobileChecks100To104 = []mobileCheck{
	{
		num:     "100",
		heading: "## Check 100:",
		rowWants: []string{
			"USER-RUN, NOT RUN",
			`See "Check 100" below`,
			"never `belong`",
			"**you** run it",
		},
		sectionWants: []string{
			// The two tokens and the key's channel (D165, D171).
			"`p~`",
			"`m~`",
			"`r~`",
			"and **no key**",
			"at most 5",
			"cannot decrypt",
			// The one-shot request node, which is what makes a photograph of
			// the QR useless.
			"pairing/<uid>/request",
			"!data.exists()`",
			// D111's two items, and the node that admits the device (D166).
			"exactly **two** items",
			"config/admin/devices/$uid",
			// The two refusals the plan names for this check.
			"a wrong typed code",
			"a photograph of the QR",
		},
		sectionRefuses: []string{
			// A guard that is not hard-coded, or one that reads the project
			// from the ambient gcloud configuration.
			"P=$(gcloud",
		},
	},
	{
		num:     "101",
		heading: "## Check 101:",
		rowWants: []string{
			"USER-RUN, NOT RUN",
			`See "Check 101" below`,
			"never `belong`",
			"**you** run it",
		},
		sectionWants: []string{
			"fugaro pair revoke",
			"config/admin/devices/",
			// U2: the deleted user's token outliving its deletion, measured
			// rather than asserted.
			"U2",
			"still unexpired",
			"seconds between the revoke and the denial",
			"**immediate**",
			// The sweeper's m~ rule, and the pairing session that has no time
			// limit of its own.
			"m~",
			"no time limit",
		},
		sectionRefuses: []string{
			// The revoked key's live half is P6.7's over the simulator, and
			// the check has to say so rather than pretend it produced one.
			"a live fixture was built and",
		},
	},
	{
		num:     "102",
		heading: "## Check 102:",
		rowWants: []string{
			"USER-RUN, NOT RUN",
			`See "Check 102" below`,
			"never `belong`",
			"**you** run it",
		},
		sectionWants: []string{
			// The alias set is data, read out of the router (D164).
			"WAKE_ALIASES",
			"shared/command/index.js",
			// The three rates, and the direction D164 answered fail-closed.
			"false accepts",
			"I didn't catch that",
			"never forwarded",
			// The script is fixed before the first utterance, or the rate is
			// not a rate.
			"before the first utterance",
			"the oldest supported device",
		},
		sectionRefuses: []string{
			// The check must not type the alias set from memory: the guard
			// block reads it, and a hard-coded copy here would be the very
			// thing the block refuses.
			"ALIASES=fugaro figaro",
		},
	},
	{
		num:     "103",
		heading: "## Check 103:",
		rowWants: []string{
			"USER-RUN, NOT RUN",
			`See "Check 103" below`,
			"never `belong`",
			"**you** run it",
			// The one check of the five that touches a repository says which.
			"sandbox repository only",
			"edgeappinc/fugarosandbox",
		},
		sectionWants: []string{
			"edgeappinc/fugarosandbox",
			"git remote get-url origin",
			"gh pr merge",
			"operator's own `gh` login",
			"head SHA",
			"resolved run id",
			// The three refusals the plan names, and D162's contrast.
			"a misheard confirm",
			"replayed confirm",
			"different target",
			"kick off",
			"no** read-back",
		},
		sectionRefuses: []string{
			// The merge stop exists so that no merge command is written with a
			// repository or a pull request typed by eye.
			`--repo edgeappinc/fugarosandbox`,
			`--repo "$R"`,
		},
	},
	{
		num:     "104",
		heading: "## Check 104:",
		rowWants: []string{
			"USER-RUN, NOT RUN",
			`See "Check 104" below`,
			"never `belong`",
			"**you** run it",
		},
		sectionWants: []string{
			"airplane mode",
			"refuses to send",
			"unsent text",
			// D167's sentence, and U3's live half.
			"never queued",
			"the mailbox read back **before and after**",
			"the SDK's own retry window",
			"the text screen",
		},
		sectionRefuses: []string{
			// A check about not sending must not tell the reader to send.
			"queue it and let the SDK",
		},
	},
}

// TestDocsMobileChecks100To104AreWritten pins the five checks the mobile functional
// spec's Phase 6 writes into the register: a summary row and a section each, in
// the register's own shape, with the installation named, the owner-run status
// stated and `belong` refused.
//
// Mutation (verified, run and restored): dropping "USER-RUN, NOT RUN" from row
// 101 fails on the row; dropping "No agent runs any step" from Check 102's
// section fails on the section; dropping "sandbox repository only" from row
// 103 fails on the row; and taking "never `belong`" out of Check 104's heading
// fails on the section. Each was planted, seen to fail, and removed.
func TestDocsMobileChecks100To104AreWritten(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	for _, c := range mobileChecks100To104 {
		row := mobileChecklistRow(doc, c.num)
		if row == "" {
			// Not Fatalf: a missing row must not hide the section's own
			// failures, which are the other half of what this test is for.
			t.Errorf("the checklist has no `| %s |` table row: checks 100–104 are written into the register as a row and a section each, and a row that is gone is a check an owner reading the table will not find", c.num)
		} else {
			flat := mobileFlat(row)
			for _, want := range c.rowWants {
				if !strings.Contains(flat, mobileFlat(want)) {
					t.Errorf("the `| %s |` row does not carry %q: the row is where an owner learns whose check this is and which installation it runs against", c.num, want)
				}
			}
		}

		flat := mobileFlat(docSection(t, path, doc, c.heading))
		for _, want := range append(append([]string{}, mobileCheckSectionCommon...), c.sectionWants...) {
			if !strings.Contains(flat, mobileFlat(want)) {
				t.Errorf("Check %s does not say %q", c.num, want)
			}
		}
		for _, bad := range c.sectionRefuses {
			if strings.Contains(flat, mobileFlat(bad)) {
				t.Errorf("Check %s carries %q, which is a shape this check must not have", c.num, bad)
			}
		}
	}
}

// TestDocsMobileChecks100To104GuardBlocksHardStop: every check that runs a command
// opens with a guard block whose stops are hard, and every `fugaro` command in
// those blocks names its installation rather than resolving one from the
// checkout or from the ambient configuration. A guard that prints instead of
// stopping is the failure mode this pins: the checks below pair a device, write
// a device node and merge a pull request, and each of those on the wrong
// installation is worse than not running at all.
//
// Mutation (verified, run and restored): changing Check 100's
// `[ "$P" = fugaro-dev ]` stop into an `echo` fails here, and so does dropping
// `--project fugaro` from its `fugaro doctor` line.
func TestDocsMobileChecks100To104GuardBlocksHardStop(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	for _, c := range mobileChecks100To104 {
		section := docSection(t, path, doc, c.heading)
		blocks := bashBlocks(section)
		// Check 100 carries the setup + guard block; 101 and 104 re-run that
		// one block rather than a copy of it, and 102 and 103 carry their own
		// (the alias set it reads out of the tree, and the sandbox
		// repository's stops). A copy of a reviewed block that drifted is the
		// failure this shape avoids, which is why the reference is asserted
		// rather than a second block being tolerated here.
		switch c.num {
		case "101", "104":
			if !strings.Contains(mobileFlat(section), "Check 100's setup + guard block is re-run") {
				t.Errorf("Check %s does not re-run Check 100's setup + guard block: the block is one block and not four copies, and a copy that drifted is a check whose stops are not the ones that were reviewed", c.num)
			}
			if len(blocks) > 0 {
				t.Errorf("Check %s carries a bash block of its own, and it must re-run Check 100's guard block instead: two copies of one guard are two guards to keep in step", c.num)
			}
			continue
		}
		if len(blocks) == 0 {
			t.Errorf("Check %s carries no bash block: its guard block is where the hard stops are, and the check cannot be run from the prose alone", c.num)
			continue
		}
		for i, block := range blocks {
			text := strings.Join(block, "\n")
			if !strings.Contains(text, "exit 1") {
				t.Errorf("Check %s's bash block %d stops nowhere: every guard in the register ends a failure with `exit 1`, and a block that only prints is a block that keeps going", c.num, i+1)
			}
			for _, line := range block {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "fugaro ") && !strings.HasPrefix(trimmed, "if fugaro ") {
					continue
				}
				if !strings.Contains(trimmed, "--project fugaro") {
					t.Errorf("Check %s's bash block %d runs %q without `--project fugaro`: a `fugaro` command that resolves its installation from the checkout or from the ambient configuration is the one that lands on the wrong installation", c.num, i+1, trimmed)
				}
			}
		}
	}
}

// TestDocsMobileCheck100RefusesASecondDeviceAtThePairingStep, and not in the guard
// block. The check pairs one device under one name and revokes it in its Restore, so
// a device already carrying that name is an earlier attempt that did not finish:
// pairing beside it would leave two device nodes, two `m~` users and two keychain
// items behind, and the Restore would revoke one of them. That is why the stop
// exists — and why it cannot live in the guard block, which the check re-runs at the
// start of **every** step. From step 3 on this check's own device is paired, so a
// stop there would put steps 4–7 and the Restore out of reach, and its own message
// ("revoke it, or run the Restore first") would be the one thing standing between
// the reader and the Restore it tells them to run. The guard therefore **reports**
// the state; the pairing step, which is run once and not re-run afterwards, is what
// refuses.
//
// Mutation (verified, run and restored): putting the `exit 1` back into the guard
// block's fresh-device branch fails here on the guard; taking it out of the pairing
// step's branch fails here on the pairing step; and dropping the guard's note fails
// on the note.
func TestDocsMobileCheck100RefusesASecondDeviceAtThePairingStep(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 100:")

	blocks := bashBlocks(section)
	if len(blocks) < 2 {
		t.Fatalf("Check 100 carries %d bash blocks, want its guard block and the pairing step's own stop: the guard is re-run and cannot hold the stop, so the stop has to be a block of its own", len(blocks))
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{
		// The project is hard-coded and never read from gcloud, and the
		// block's own run is asserted before anything else uses it.
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ]`,
		// The installation's own line, read locally (no cloud call).
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
		// An empty variable is a hard error, never an empty argument.
		`${N:?}`,
		"exit 1",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 100's guard block does not carry %q:\n%s", want, guard)
		}
	}
	// The state is reported, not stopped on: the note names the device and says
	// which of the two readings applies.
	if !strings.Contains(guard, "Check 100 note: a device named $N is already paired") {
		t.Error("Check 100's guard does not report a device already carrying $N: the reader re-running the block has to be told which state they are in, or a leftover from an earlier attempt looks like this check's own device")
	}
	guardBranch := mobileBashBranch(guard, `if fugaro pair ls --project fugaro`)
	if guardBranch == "" {
		t.Fatal("Check 100's guard has no fresh-device branch to read: the block is re-run, so its report of an already-paired device is the half that has to be there")
	}
	if strings.Contains(guardBranch, "exit 1") {
		t.Error("Check 100's guard block stops on a device it has already paired: this block is re-run at the start of every step, and from step 3 on this check's own device is paired, so the stop puts steps 4-7 and the Restore out of reach and its own message (\"revoke it, or run the Restore first\") is circular")
	}

	// And the stop itself, in the one step that pairs.
	stop := strings.Join(blocks[1], "\n")
	stopBranch := mobileBashBranch(stop, `if fugaro pair ls --project fugaro`)
	if stopBranch == "" {
		t.Fatal("Check 100's pairing step carries no fresh-device stop: the guard reports and cannot stop, so this branch is the only place a second device under this check's name is refused")
	}
	if !strings.Contains(stopBranch, "exit 1") {
		t.Error("Check 100's pairing step warns instead of stopping: a message on stderr leaves the check running with a leftover device already paired, which is the state the Restore then revokes half of")
	}
	if !strings.Contains(stop, "Check 100 stopped: a device named $N is already paired") {
		t.Error("Check 100's pairing step does not name the leftover device as the reason it stopped, or does not say what to do about it: the reader has to be able to carry on at the later steps or revoke and start again")
	}
}

// mobileBashBranch returns the body of the shell branch opened by open — the text
// from that line to the next `fi` at column 0 — or the empty string when the block
// carries no such branch. A guard whose stop is asserted has to be read on its own:
// an `exit 1` elsewhere in the block is not this branch's, which is exactly the
// reading that let a warning pass for a stop here once already.
func mobileBashBranch(block, open string) string {
	start := strings.Index(block, open)
	if start < 0 {
		return ""
	}
	rest := block[start:]
	if end := strings.Index(rest, "\nfi"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestDocsMobileCheck103MergesOnlyItsOwnPullRequest: Check 103 is the one of
// the five that touches a repository, and this is the structural half of its
// safety. The sandbox repository is hard-coded, the checkout's own origin is a
// stop, `gh` must resolve that repository to itself under the operator's login,
// and the merge is reached only through a fourth stop that refuses any pull
// request that is not this check's own open, non-draft one on the branch it
// pushed. The `--repo` assertions are what keep a repository or a number from
// being typed by eye into `gh pr merge`.
//
// Mutation (verified, run and restored): replacing the merge stop's
// `--arg b "${B:?}"` filter with `true` fails here; so does writing
// `--repo edgeappinc/fugarosandbox` into the merge command.
func TestDocsMobileCheck103MergesOnlyItsOwnPullRequest(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 103:")

	blocks := bashBlocks(section)
	if len(blocks) < 2 {
		t.Fatalf("Check 103 carries %d bash blocks, want its guard block and the merge step's own stop", len(blocks))
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{
		"R=edgeappinc/fugarosandbox",
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`gh repo view "${R:?}" --json nameWithOwner`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 103's guard block does not carry %q:\n%s", want, guard)
		}
	}

	mergeStop := strings.Join(blocks[1], "\n")
	for _, want := range []string{
		`gh pr view "${PR:?}" --repo "${R:?}"`,
		`--arg b "${B:?}"`,
		`startswith($b)`,
		`.state=="OPEN"`,
		`(.isDraft|not)`,
		"nothing is merged",
	} {
		if !strings.Contains(mergeStop, want) {
			t.Errorf("Check 103's merge stop does not carry %q:\n%s", want, mergeStop)
		}
	}

	// Every `--repo` in the whole section takes the sandbox repository from a
	// checked variable, and none is written out.
	for i, block := range blocks {
		for _, line := range block {
			if !strings.Contains(line, "--repo") {
				continue
			}
			if !strings.Contains(line, `"${R:?}"`) {
				t.Errorf("Check 103's bash block %d carries %q, and its `--repo` is not `${R:?}`: the sandbox repository is the only one this check may reach, and a repository written into the command is one the guard above cannot refuse", i+1, strings.TrimSpace(line))
			}
		}
	}
	// And the check's own pull request is named by the number its step recorded,
	// never by a literal.
	if strings.Contains(mobileFlat(section), "--repo edgeappinc/fugarosandbox") {
		t.Error("Check 103 names the sandbox repository literally in a `gh` command: the merge stop refuses a pull request that is not this check's own, and a literal repository is the shape that gets past it by hand")
	}
}

// TestDocsMobileChecks105To109StayReserved: the mobile functional spec holds 100–109
// and Phase 6 proposes 100–104 of it, so 105–109 stay reserved and unused — and
// the register has to say so and leave them unwritten. A reserved number is
// never reused and never renumbered, which is the register's own ruling.
//
// Mutation (verified, run and restored): adding a `## Check 105` section (or a
// `| 105 |` row) fails here.
func TestDocsMobileChecks105To109StayReserved(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	for n := 105; n <= 109; n++ {
		heading := fmt.Sprintf("## Check %d", n)
		if strings.Contains(doc, heading) {
			t.Errorf("the register writes %q: 105–109 are reserved and unused, and the mobile functional spec's Phase 6 proposes 100–104 of its block and nothing above them", heading)
		}
		if row := mobileChecklistRow(doc, fmt.Sprint(n)); row != "" {
			t.Errorf("the checklist has a `| %d |` table row: 105–109 are reserved and unused", n)
		}
	}

	// The allocation paragraph is what a later work reads before taking a
	// number, so its two claims are pinned where they are written.
	for _, want := range []string{
		"Checks 100–104 are the mobile functional spec v0.1's",
		"105–109 stay reserved and unused",
		"Check 110 is not this block's",
		"the highest written `## Check N` section in this file was 70c",
		"a check is never run before its text is in the register",
	} {
		if !strings.Contains(mobileFlat(doc), mobileFlat(want)) {
			t.Errorf("the register's numbering paragraph does not carry %q: the paragraph is how a later work reads what is taken, and a claim that is gone is a number taken twice", want)
		}
	}
}

// TestDocsMobileChecks100To104AreTheLastSections: the five are appended, so the
// file's last section is Check 104 and no section after them belongs to another
// work. It is the shape every block in this file arrived in, and it is what
// makes "the highest written section" a fact a reader can check by scrolling.
//
// Mutation (verified, run and restored): appending another work's `## Check 99`
// section after the five fails here.
func TestDocsMobileChecks100To104AreTheLastSections(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	last := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## Check ") {
			last = line
		}
	}
	if !strings.HasPrefix(last, "## Check 104:") {
		t.Errorf("the last `## Check` section in the register is %q, want Check 104: checks 100–104 are written by Task P6.11 and appended to the file", last)
	}
}

// TestDocsMobileChecksDeviceBookkeepingMatchesTheirSteps: each check's device name
// has to be one its own steps actually pair and revoke, or the guard's fresh-device
// rule is vacuous and the row promises bookkeeping the section does not do. Check 100
// pairs `chk100` and revokes it in its Restore; Check 101 pairs `chk101` and its
// revocation is step 3; **Check 104 pairs nothing and revokes nothing** — it borrows
// Check 100's pairing, so it re-runs Check 100's guard block unchanged (`N=chk100`)
// and a name of its own would be a pairing that never happens.
//
// Mutation (verified, run and restored): setting Check 104's re-run back to
// `N=chk104` fails here, and so does deleting the sentence that says 104 pairs and
// revokes no device; setting Check 101's re-run to `N=chk100` fails here.
func TestDocsMobileChecksDeviceBookkeepingMatchesTheirSteps(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// The two checks that pair: their own name, and a revocation of it.
	for _, c := range []struct{ num, name string }{{"100", "chk100"}, {"101", "chk101"}} {
		section := mobileFlat(docSection(t, path, doc, "## Check "+c.num+":"))
		if !strings.Contains(section, "N="+c.name) {
			t.Errorf("Check %s does not re-run the guard block with N=%s: its device name is which device node, which `m~` user and which keychain item this check owns", c.num, c.name)
		}
		if !strings.Contains(section, `fugaro pair revoke "$N"`) && !strings.Contains(section, "fugaro pair revoke") {
			t.Errorf("Check %s never revokes $N: the register's owner-run checks leave nothing paired behind, and a name whose revocation is not in the check is a device node left on the installation", c.num)
		}
	}

	// And the one that borrows: no name of its own, and it says so.
	section := mobileFlat(docSection(t, path, doc, "## Check 104:"))
	if strings.Contains(section, "chk104") {
		t.Error("Check 104 names a device of its own (chk104): it pairs no device and revokes none, so that name is a pairing no step of this check performs — and the guard's fresh-device rule, which the row says it re-runs, would never fire for it")
	}
	if !strings.Contains(section, "N=chk100") {
		t.Error("Check 104 does not say it re-runs Check 100's guard block with N=chk100: it borrows Check 100's pairing, so the block is re-run unchanged and the device it reports is this check's precondition, not a leftover")
	}
	if !strings.Contains(section, "pairs no device and revokes none") {
		t.Error("Check 104 does not say it pairs and revokes nothing: its Restore revokes no device, and a check whose Restore is silent about the pairing it borrowed is a reader's reason to revoke somebody else's device")
	}
	// The row and the section have to name the same device, which is the drift
	// this pin exists for: the row said "this check's device name" while the
	// Method said the phone was Check 100's pairing.
	row := mobileFlat(mobileChecklistRow(doc, "104"))
	if row == "" {
		t.Fatal("the checklist has no `| 104 |` row")
	}
	if !strings.Contains(row, "chk100") {
		t.Error("row 104 does not name `chk100`: the row is where a reader learns which device the check uses, and it has to name the same one the section does")
	}
	if strings.Contains(row, "with this check's device name") {
		t.Error("row 104 says it re-runs the guard block \"with this check's device name\", and Check 104 has no device of its own")
	}
}

// TestDocsMobileCoordinatorRowAgreesTheRegisterReaches100To104. The coordinator's
// numbers row is the one place that lists every work's block, so a cell that denies
// the register reaches a range is a reader's reason not to look for it there. Writing
// 100–104 made two of the row's phrases false — "the two the checklist's register
// does not reach" and "for 90–99 and 100–109, which the checklist's register does not
// reach" — and the coordinator's own pin does not catch it, because its trigger for
// "this range is not written" is `unused|not taken|free` and the words here were
// "does not reach".
//
// The rule pinned is the one the row has to keep: a claim that the register does not
// reach a range must carry that range in the same cell or sentence, and the range
// must not contain a check the register has written. "the two the checklist's
// register does not reach" carries none — the ranges were named a comma and a half
// earlier — which is how one clause came to deny ten numbers of which five are
// written.
//
// Mutation (verified, run and restored): restoring either false phrase fails here;
// so does deleting the row's "written into the register" claim.
func TestDocsMobileCoordinatorRowAgreesTheRegisterReaches100To104(t *testing.T) {
	const path = "docs/coordinator/README.md"
	flat := mobileFlat(readDoc(t, path))

	// The register is the judge, exactly as the coordinator's own pin has it: the
	// numbers it carries a `## Check N` section for are the written ones. The colon
	// is required, so a sub-lettered section (`## Check 70a`) does not put its plain
	// number in the set: a claim about the range 70–79 is a claim about the bare
	// numbers, and the register's own reading of that range is that no check is
	// written in it.
	written := map[int]bool{}
	for _, m := range regexp.MustCompile(`(?m)^## Check ([0-9]+):`).FindAllStringSubmatch(readDoc(t, "docs/gcp-live-checklist.md"), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			written[n] = true
		}
	}
	if len(written) == 0 {
		t.Fatal("the register carries no written check section: this pin would otherwise pass whatever the row claims about a range")
	}
	for _, want := range []string{"written into the register", "**100–104** are **approved 2026-10-18**"} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/coordinator/README.md no longer says %q: the row is how a reader learns these five checks exist, and a row that does not say they were written sends them looking for a number that is taken", want)
		}
	}

	units := 0
	for _, unit := range mobileDenialUnits(flat) {
		if !mobileUnreachedRE.MatchString(unit) {
			continue
		}
		units++
		for _, m := range mobileNumberRangeRE.FindAllStringSubmatch(unit, -1) {
			lo, errLo := strconv.Atoi(m[1])
			hi, errHi := strconv.Atoi(m[2])
			if errLo != nil || errHi != nil {
				continue
			}
			for n := lo; n <= hi; n++ {
				if written[n] {
					t.Errorf("docs/coordinator/README.md says the register does not account for %s, and the register writes a `## Check %d` section inside it: the row and the register disagree, and the register is the one that allocates\nin: %s", m[0], n, unit)
				}
			}
		}
	}
	if units == 0 {
		t.Fatal("docs/coordinator/README.md carries no claim that a range is unwritten: this pin's trigger is gone, and a rule with no trigger passes whatever the row says")
	}
}

// mobileUnreachedRE is the vocabulary a row uses to deny a range: the register's own
// words for a number it does not write, plus the coordinator row's "does not reach".
var mobileUnreachedRE = regexp.MustCompile(`does not reach|unwritten|not written|unused|not taken`)

// mobileNumberRangeRE finds a check range written with an en dash or a hyphen. It
// deliberately does not match a decision block's range: `D79–D89` and `D180–D192`
// carry the letter on both sides, so the digits are never adjacent to the dash.
var mobileNumberRangeRE = regexp.MustCompile(`\b([0-9]{1,3})[–-]([0-9]{1,3})\b`)

// mobileDenialUnits splits the coordinator README into the units a denial is stated
// in: a table cell, and a sentence. Both, because the row states one in a cell and
// the note below the table states the other in a sentence, and a denial that reaches
// across a cell boundary or a full stop is not a claim about anything.
func mobileDenialUnits(flat string) []string {
	var units []string
	for _, cell := range strings.Split(flat, "|") {
		units = append(units, strings.Split(cell, ". ")...)
	}
	return units
}

// mobileChecklistRow returns the checklist's summary row for a check number, or
// the empty string when there is none. The table's first cell is the number,
// which is why the match is on the whole cell and not on a prefix: `| 100 |`
// must not be found by a row for `| 1000 |`.
func mobileChecklistRow(doc, num string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| "+num+" |") {
			return line
		}
	}
	return ""
}
