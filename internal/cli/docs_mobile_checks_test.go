package cli

import (
	"fmt"
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
// block hard-stops on the project and on a device name it has already paired,
// and Check 103's merge cannot reach a repository or a pull request that is
// not its own.
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

// TestDocsMobileCheck100GuardRefusesADeviceItAlreadyPaired: Check 100's own
// fourth stop, and the reason it needs one. The check pairs one device under
// one name and revokes it in its Restore, so a device already carrying that
// name is a previous attempt that did not finish — pairing beside it would
// leave two device nodes, two `m~` users and two keychain items behind, and the
// Restore would revoke one of them.
//
// Mutation (verified, run and restored): removing the `grep -q "${N:?}"` stop
// fails here, and so does turning its `exit 1` into a warning.
func TestDocsMobileCheck100GuardRefusesADeviceItAlreadyPaired(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 100:")

	blocks := bashBlocks(section)
	if len(blocks) == 0 {
		t.Fatal("Check 100 carries no bash block, so its guard cannot be read")
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{
		// The project is hard-coded and never read from gcloud, and the
		// block's own run is asserted before anything else uses it.
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ]`,
		// The installation's own line, read locally (no cloud call).
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
		// The fresh-device rule.
		`fugaro pair ls --project fugaro`,
		`grep -q "${N:?}"`,
		"exit 1",
		// An empty variable is a hard error, never an empty argument.
		`${N:?}`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 100's guard block does not carry %q:\n%s", want, guard)
		}
	}
	// The message is not the stop. `exit 1` elsewhere in the block is not
	// this stop's either, so the fresh-device branch is read on its own, from
	// its `if` to its `fi`, and it has to end the block itself.
	if !strings.Contains(guard, "Check 100 stopped: a device named $N is already paired") {
		t.Error("Check 100's guard does not name the leftover device as the reason it stopped: a stop whose message does not say what to do is a stop an owner works around")
	}
	start := strings.Index(guard, `if fugaro pair ls --project fugaro`)
	if start < 0 {
		t.Fatal("Check 100's guard has no fresh-device branch to read: the stop is what keeps a second device from being paired beside a leftover one")
	}
	rest := guard[start:]
	end := strings.Index(rest, "\nfi\n")
	if end < 0 {
		t.Error("Check 100's fresh-device branch is not closed by an `fi` at column 0: the branch's own status cannot be read, so whether it stops or warns cannot either")
	} else if !strings.Contains(rest[:end], "exit 1") {
		t.Error("Check 100's fresh-device branch warns instead of stopping: a message on stderr leaves the check running with a leftover device already paired, which is the state the Restore then revokes half of")
	}
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
