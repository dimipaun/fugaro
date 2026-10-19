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
			// The three refusals the plan names, and D162's contrast — which is the
			// confirmation machine's pass-through and not a §3.4 command: "kick off …"
			// is not a row in the router's grammar, so the step has to say so.
			"a misheard confirm",
			"replayed confirm",
			"different target",
			"kick off",
			"not a row in the router's grammar",
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
// The paragraph's claim about what was already written is pinned by its own test
// below (TestDocsMobileChecksNumberingParagraphAgreesWhatIsWritten), because this
// one held the false version of it for a round: "the highest written `## Check N`
// section in this file was 70c" is not the register's state — 81, 82 and 89 are
// written, so the highest before this block is 89.
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
		"a check is never run before its text is in the register",
	} {
		if !strings.Contains(mobileFlat(doc), mobileFlat(want)) {
			t.Errorf("the register's numbering paragraph does not carry %q: the paragraph is how a later work reads what is taken, and a claim that is gone is a number taken twice", want)
		}
	}
}

// mobileSpecCommandPhrases is the phrase the register's script has to use for each
// of the router's five §3.4 commands, keyed by the router's own command id. The keys
// are `shared/command`'s, so a command the module stops carrying — or renames — fails
// the test below rather than passing quietly with a register that names a set the app
// does not have.
var mobileSpecCommandPhrases = map[string][]string{
	// "Fugaro, switch to ⟨project⟩" — §3.4's first command.
	"switch-project": {"switch to"},
	// "Fugaro, talk to ⟨agent⟩". There is no "switch agent": addressing an agent is
	// what this command is for, and the register named a command that does not exist.
	"talk-to-agent": {"talk to"},
	// "Fugaro, what project am I on?" — the one the register's memory dropped.
	"where-am-i": {"what project am I on"},
	// "Fugaro, list projects" / "list agents" — one command id, two rows.
	"list": {"list projects", "list agents"},
	// "Fugaro, what's pending?"
	"whats-pending": {"what's pending"},
}

// TestDocsMobileCheck102ListsTheRoutersOwnCommands: Check 102 fixes a script before the
// first utterance and speaks `<alias>, <command>` in it, and its own Method says a list
// typed from memory measures a set the app may not have. Step 2 typed it from memory
// anyway: it named "switch agent", which appears nowhere in the tree, and "kick off",
// which is D162's launch — not a row in the router's grammar at all — while leaving out
// "what project am I on", one of the five. A script written that way would exercise a
// command the app does not have, skip one it does, and record a fail-closed pass for a
// `<alias>, kick off …` utterance it had mislabelled as a command.
//
// The five are read out of the tree here as they are in the guard block, so the register
// and `shared/command/index.js` cannot drift apart silently.
//
// Mutation (verified, run and restored): putting "switch agent" back into step 2 fails
// here; dropping "what project am I on" fails here; removing the guard block's `CMDS`
// read fails here.
func TestDocsMobileCheck102ListsTheRoutersOwnCommands(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	src := readDoc(t, "shared/command/index.js")

	// `shared/command`'s own two lists, read from the module rather than restated: the
	// command ids, and which five of them the spec's §3.4 names.
	const commandBlock = "export const COMMAND = Object.freeze({"
	const specBlock = "export const SPEC_COMMANDS = Object.freeze(["
	ids := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^  ([A-Z_]+): '([a-z-]+)',$`).FindAllStringSubmatch(mobileBlock(src, commandBlock), -1) {
		ids[m[1]] = m[2]
	}
	var spec []string
	for _, m := range regexp.MustCompile(`COMMAND\.([A-Z_]+)`).FindAllStringSubmatch(mobileBlock(src, specBlock), -1) {
		id, ok := ids[m[1]]
		if !ok {
			t.Fatalf("shared/command's SPEC_COMMANDS names COMMAND.%s, which the module's COMMAND object does not declare: this pin reads the five from there, and a name it cannot resolve is a list it cannot check", m[1])
		}
		spec = append(spec, id)
	}
	if len(spec) != 5 {
		t.Fatalf("shared/command's SPEC_COMMANDS carries %d commands, want the spec's five: the register's script speaks exactly these, and a module that changes how many there are has to be read against the check again", len(spec))
	}

	section := docSection(t, path, doc, "## Check 102:")
	m := regexp.MustCompile(`(?ms)^2\. \*\*(.*?)^3\. \*\*`).FindStringSubmatch(section)
	if m == nil {
		t.Fatal("Check 102 carries no numbered step 2 this pin can read: the script is what the check speaks, and it is fixed there")
	}
	step2 := mobileFlat(m[1])

	for _, id := range spec {
		phrases, ok := mobileSpecCommandPhrases[id]
		if !ok {
			t.Fatalf("shared/command carries the §3.4 command %q, and this pin has no phrase for it: the register's step 2 and this table are updated together, or the check goes on measuring a set the app has stopped having", id)
		}
		for _, want := range phrases {
			if !strings.Contains(step2, want) {
				t.Errorf("Check 102's step 2 does not name %q, so the script does not speak `%s`, one of the spec's §3.4 commands: the five are the router's own, read out of shared/command, not typed from memory", want, id)
			}
		}
	}
	// The two things the memory put in that the grammar does not have.
	if strings.Contains(step2, "switch agent") {
		t.Error("Check 102's step 2 names \"switch agent\", and no such command exists: addressing an agent is \"talk to ⟨agent⟩\", and a script that speaks a command the app does not have measures the router's refusal, not its recognition rate")
	}
	if !strings.Contains(step2, "is not a row in the router's grammar") {
		t.Error("Check 102's step 2 does not say where a launch stands: \"kick off …\" is D162's launch and not a §3.4 command, so a script that counted it as one would record a fail-closed pass for an utterance it had mislabelled")
	}

	// And the guard block reads that list, which is what makes the pin above a check on
	// the register rather than a second memory.
	blocks := bashBlocks(section)
	if len(blocks) == 0 {
		t.Fatal("Check 102 carries no bash block: its guard block is where the alias set and the command set are read out of the tree")
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{"[ -n \"$CMDS\" ]", "Check 102: the command kinds as the tree writes them"} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 102's guard block does not carry %q: the command set is data, and the block reads it out of shared/command/index.js for the same reason it reads the alias set there", want)
		}
	}
}

// mobileBlock returns the source from the line that opens a frozen constant to the line
// that closes it — `});` for an object, `]);` for an array — or the empty string when the
// opening line is not there. The module declares several objects and arrays at the same
// indent, so a pin that reads one of them has to be scoped to it: an unscoped read takes
// `LIST_TARGET`'s and `ROUTE`'s lines for the command set, and an array closed with the
// object's delimiter runs on into the constant declared below it.
func mobileBlock(src, open string) string {
	start := strings.Index(src, open)
	if start < 0 {
		return ""
	}
	rest := src[start:]
	end := len(rest)
	for _, close := range []string{"\n});", "\n]);"} {
		if i := strings.Index(rest, close); i >= 0 && i < end {
			end = i
		}
	}
	return rest[:end]
}

// TestDocsMobileChecksNumberingParagraphAgreesWhatIsWritten: the register's paragraph for
// the 100–104 block tells a later work which numbers are taken, and it claimed two things
// about this register that are false — that "the highest written `## Check N` section in
// this file was 70c", and that the mobile work's own first block, 80–89, "has written" no
// row and no section here. Checks 81, 82 and 89 are written (Task P4.2a), with a row and a
// section each, and both `docs/mobile/implementation-plan.md` and the register's own table
// carry them; the older claim was written while this branch was behind the commit that
// added them. The plan is what this pin reads the written set from, because it is the
// document that records which of 80–89 are written and which are still P4.2's.
//
// Mutation (verified, run and restored): restoring "was 70c" fails here; so does restoring
// the claim that the mobile block has written no row or section.
func TestDocsMobileChecksNumberingParagraphAgreesWhatIsWritten(t *testing.T) {
	plan := mobileFlat(readDoc(t, "docs/mobile/implementation-plan.md"))
	for _, want := range []string{
		"Checks 81, 82 and 89 are written into the register by P4.2a",
		"checks 80 and 83–88 by P4.2",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("docs/mobile/implementation-plan.md no longer says %q: this pin reads the written set from there, and without it the register's paragraph would be checked against nothing", want)
		}
	}

	para := mobileCheck100To104Paragraph(t, readDoc(t, "docs/gcp-live-checklist.md"))
	for _, want := range []string{
		"the highest written `## Check N` section in this file was 89",
		"wrote **81**, **82** and **89** into this register",
		"**80** and **83–88**",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("the register's 100–104 paragraph does not say %q: the mobile work's first block is not unwritten, and a paragraph that says it is contradicts the plan, the table below it and the sections in this file", want)
		}
	}
	for _, bad := range []string{
		"was 70c",
		"has written a row or a section into this file",
	} {
		if strings.Contains(para, bad) {
			t.Errorf("the register's 100–104 paragraph carries %q: that is the claim this pin exists for — 81, 82 and 89 are written into this register, and a paragraph saying otherwise is a reader's reason to think the numbering is free where it is not", bad)
		}
	}
}

// mobileCheck100To104Paragraph returns the register's 100–104 numbering paragraph,
// flattened: from the phrase that opens it to the first blank line after it. The
// paragraph is one block of prose in a file of them, so a claim about the whole file
// (`was 70c` is nowhere else, either) is not the same claim as one about this paragraph.
func mobileCheck100To104Paragraph(t *testing.T, doc string) string {
	t.Helper()
	const open = "**Checks 100–104 are the mobile functional spec v0.1's"
	start := strings.Index(doc, open)
	if start < 0 {
		t.Fatalf("the register carries no %q paragraph: the block's allocation is how a later work reads which numbers are taken", open)
	}
	rest := doc[start:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return mobileFlat(rest)
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
// revocation is step 3; **Checks 103 and 104 pair nothing and revoke nothing** — they
// borrow Check 100's pairing, so 104 re-runs Check 100's guard block unchanged
// (`N=chk100`) and 103's own guard declares no device at all, and a name of its own in
// either would be a pairing that never happens and a fresh-device rule that can never
// fire. 103 is here because it is the same defect 104 had, found in the next round of
// review: it declared `N=chk103` while its Method said the phone was Check 100's, and
// `$N` reached nothing but the assertion on itself.
//
// Mutation (verified, run and restored): setting Check 104's re-run back to
// `N=chk104` fails here, and so does deleting the sentence that says 104 pairs and
// revokes no device; setting Check 101's re-run to `N=chk100` fails here; giving
// Check 103 an `N=chk103` device line again fails here.
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
	// The Method is where the reader is told, so the sentence has to be in the
	// prose and not only in a bash comment: a comment is something they paste,
	// and the contradiction this pin exists for was between the Method and the
	// declaration.
	if !strings.Contains(mobileFlat(mobileProse(docSection(t, path, doc, "## Check 104:"))), "pairs no device and revokes none") {
		t.Error("Check 104 does not say it pairs and revokes nothing: its Restore revokes no device, and a check whose Method is silent about the pairing it borrowed is a reader's reason to revoke somebody else's device")
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

	// And Check 103, which borrows the same pairing and declares no device: the
	// defect 104 had, one round later and in the section that never had a Method
	// sentence to contradict the declaration.
	section = mobileFlat(docSection(t, path, doc, "## Check 103:"))
	if strings.Contains(section, "N=chk") {
		t.Error("Check 103 declares a device name of its own (`N=chk…`): it pairs no device and revokes none — its phone is Check 100's pairing — and no command in the section takes a device name, so the declaration reaches nothing but an assertion on itself and the fresh-device rule it suggests can never fire")
	}
	if !strings.Contains(mobileFlat(mobileProse(docSection(t, path, doc, "## Check 103:"))), "pairs no device and revokes none") {
		t.Error("Check 103 does not say it pairs and revokes nothing: its phone is Check 100's pairing, borrowed, and a check whose Method does not say so is a reader's reason to pair and revoke a device that is somebody else's")
	}
	if !strings.Contains(section, "chk100") {
		t.Error("Check 103 does not name `chk100`, the pairing it borrows: the phone it speaks through is Check 100's, and a check that does not name it leaves the reader to guess which device node admits it")
	}
}

// mobileProse returns a section with every fenced block removed, so an assertion
// about what a check *tells its reader* cannot be satisfied by a comment inside a
// block the reader pastes and runs. The contradiction this pin exists for was
// between a check's Method and its guard block's declaration, and a bash comment is
// on the other side of it.
func mobileProse(section string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// TestDocsMobileChecksGuardBlocksNameEveryStep: a guard block that says which steps
// re-run it is a list the reader follows, and a list one step short sends them into
// the last step with the setup block's variables gone. Check 102's said "(1, 2, 3, 4,
// 5)" over a section with six steps. A list may close with `Restore` in place of the
// number of a last step that *is* the Restore — that is how Checks 100 and 103 write
// theirs — but every other step has to be named.
//
// Mutation (verified, run and restored): dropping the 6 from Check 102's list fails
// here, and so does dropping the 8 from Check 103's.
func TestDocsMobileChecksGuardBlocksNameEveryStep(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	stepsRE := regexp.MustCompile(`(?m)^([0-9]+)\. \*\*([^*]*)`)
	for _, num := range []string{"100", "102", "103"} {
		section := docSection(t, path, doc, "## Check "+num+":")

		var steps []string
		for _, m := range stepsRE.FindAllStringSubmatch(section, -1) {
			steps = append(steps, m[2])
		}
		if len(steps) == 0 {
			t.Errorf("Check %s carries no numbered step this pin can read: the list would otherwise be checked against nothing", num)
			continue
		}

		// The list lives in a bash comment, so the phrase is split across `#`
		// lines in the raw text and the regexp steps over the marker.
		m := mobileStepListRE.FindStringSubmatch(section)
		if m == nil {
			t.Errorf("Check %s's guard block does not say which steps re-run it: the block runs in later shells where its variables are gone, and the list is what tells the reader when", num)
			continue
		}
		listed := map[int]bool{}
		closesWithRestore := false
		for _, tok := range strings.Split(m[1], ",") {
			tok = strings.TrimSpace(tok)
			if tok == "Restore" {
				closesWithRestore = true
				continue
			}
			n, err := strconv.Atoi(tok)
			if err != nil {
				t.Errorf("Check %s's re-run list carries %q, which is neither a step number nor `Restore`", num, tok)
				continue
			}
			listed[n] = true
		}

		want := len(steps)
		if closesWithRestore {
			if !strings.HasPrefix(steps[want-1], "Restore.") {
				t.Errorf("Check %s's re-run list closes with `Restore`, and the section's last step is %q, which is not the Restore", num, steps[want-1])
				continue
			}
			want--
		}
		for n := 1; n <= want; n++ {
			if !listed[n] {
				t.Errorf("Check %s's re-run list does not name step %d: the section has %d steps, and a reader who follows the list reaches that step with the setup block's variables unset", num, n, len(steps))
			}
		}
		for n := range listed {
			if n > len(steps) {
				t.Errorf("Check %s's re-run list names step %d, and the section has %d steps", num, n, len(steps))
			}
		}
	}
}

// mobileStepListRE reads a guard block's re-run list — "at the start of every step
// (1, 2, 3, …, Restore)" — out of the raw section. The phrase is written in a bash
// comment and wraps, so a line marker may sit between "every" and "step".
var mobileStepListRE = regexp.MustCompile(`at the start of every\s+(?:#\s*)?step \(([^)]*)\)`)

// TestDocsMobileCheck100RowAgreesTheStopIsInThePairingStep: the row's one-line
// account of the guard is what a reader takes away from the table, and it said the
// re-run setup block hard-stops on "the device name this check uses is free". That
// stopped being true when the stop moved into the pairing step, and it contradicts
// the Method and TestDocsMobileCheck100RefusesASecondDeviceAtThePairingStep on the
// one safety property the stop exists for.
//
// Mutation (verified, run and restored): putting "and the device name this check uses
// is free" back into the row fails here.
func TestDocsMobileCheck100RowAgreesTheStopIsInThePairingStep(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	row := mobileFlat(mobileChecklistRow(readDoc(t, path), "100"))
	if row == "" {
		t.Fatal("the checklist has no `| 100 |` row")
	}
	if !strings.Contains(row, "hard-stops unless the installation is `fugaro-dev`") {
		t.Error("row 100 does not say the setup block hard-stops on the installation: that is the stop the block does carry, and the row is where a reader learns it")
	}
	if strings.Contains(row, "hard-stops unless the installation is `fugaro-dev` and the device name") {
		t.Error("row 100 says the re-run setup block hard-stops on the device name being free, and the stop is in the pairing step: the guard block reports that state instead, because it is re-run after this check has paired its own device")
	}
	if !strings.Contains(row, "the one pairing step carries the stop") {
		t.Error("row 100 does not say where the device-name stop is: the row and the Method have to agree on the one safety property the stop exists for")
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
