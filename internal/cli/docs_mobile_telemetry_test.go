package cli

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The pin for docs/plans/2026-10-19-mobile-telemetry.md, the plan for Firebase
// Crashlytics and Firebase Analytics in the Expo app (the owner's ruling of
// 2026-10-19, recorded there as D193).
//
// Nothing in the plan is built — there is no @react-native-firebase dependency,
// no mobile/app.config.js, no mobile/eas.json and no google-services.json
// anywhere in the tree — so no test can hold its behaviour. What a reader can
// check today, and what this file pins, is the four things a plan of this shape
// has to get right and a reviewer is least likely to check twice:
//
//  1. **the numbers it takes.** D193–D200 is claimed to be the next FREE block,
//     which is only true while the highest number written anywhere in docs/ is
//     D192 — so this test reads that highest number out of the tree rather than
//     trusting the plan's memory of it, and refuses a claim of a free block that
//     is not free.
//  2. **the checks it takes, and the ones it leaves.** 111–113, the first free
//     numbers after EVERY claim — 105–109 are reserved and unused, and the plan
//     applies "an unused number is not a free number" to them exactly as the
//     decision block applies it to D79–D89 — with 110 left alone (the
//     orchestrator's claim) and 114 onward free. A check is never run before its
//     text is in the register, so the plan's claim that all three are PROPOSED
//     and unwritten is checked against the register itself: a `## Check 111`
//     section appearing there while the plan still says "proposed" is a
//     disagreement, not a detail.
//  3. **the flags, spelled exactly.** The whole consent recommendation is worth
//     nothing if a flag is named approximately — a misspelled switch is a switch
//     that is not thrown to false — so each name is required verbatim.
//  4. **the two native config files are never committed.** The plan states this
//     in several places; this test requires the rule, the two paths and the word
//     EAS, and — the half that would otherwise rot — walks BOTH paths itself:
//     neither may be in the tree and neither may be tracked. It walks them from
//     its own list rather than from planPaths, because planPaths cannot see the
//     iOS one at all (see telemetryNativeConfig), and the blind spot is asserted
//     rather than assumed.
//
// The last assertion is the path walk the other plan pins use (planPaths): every
// repository path the document names must exist, so a plan that cites a file the
// tree does not have is red the moment it is written down.
//
// Mutation (verified, run and restored): change D200 to D199 in the block the
// plan claims; delete one flag name from §4.2; write a check row or a name for
// 105 back into §12; add a `## Check 111` heading to the register; commit
// `mobile/GoogleService-Info.plist` (the file the shape filter cannot see) or
// `mobile/google-services.json`; state a check number the register does not
// write as its highest section; un-date the Check 110 row's "nothing above 109"
// claim; or cite a path the tree does not have — this test fails, each of them.
const telemetryPlan = "docs/plans/2026-10-19-mobile-telemetry.md"

// telemetryBlockRE is the plan's own statement of the block it takes, in the
// several places it makes it.
var telemetryBlockRE = regexp.MustCompile(`D193[–-]D200`)

// The two allocation documents, and the shapes a block is written in there:
// `**D193–D200**` (a closed range) and `**D201 onward**` (open).
const (
	telemetryMobileReadme = "docs/mobile/README.md"
	telemetryCoordinator  = "docs/coordinator/README.md"
)

var (
	telemetryBlockRangeRE = regexp.MustCompile(`\*\*D([0-9]+)[–-]D([0-9]+)\*\*`)
	telemetryOnwardRE     = regexp.MustCompile(`\*\*D([0-9]+) onward\*\*`)
)

// telemetryBlocks is every decision block the two allocation documents claim,
// read out of them: a range is its two ends, and an "onward" row is everything
// from its start upwards (its end recorded as 0, meaning unbounded).
//
// This is the collision check, and it is derived rather than trusted. The plan
// claims D193–D200 is "the next free block", and that claim is only true while no
// other work's block reaches into it — so the blocks are read from the two
// documents that allocate them, and any overlap with this plan's block is a
// collision, by whichever work wrote second.
func telemetryBlocks(t *testing.T, doc string) [][3]int {
	t.Helper()
	var out [][3]int
	for _, m := range telemetryBlockRangeRE.FindAllStringSubmatch(doc, -1) {
		lo, errLo := strconv.Atoi(m[1])
		hi, errHi := strconv.Atoi(m[2])
		if errLo != nil || errHi != nil {
			continue
		}
		out = append(out, [3]int{lo, hi, 0})
	}
	for _, m := range telemetryOnwardRE.FindAllStringSubmatch(doc, -1) {
		lo, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, [3]int{lo, 0, 1})
	}
	return out
}

// telemetryCovers says whether a block covers n. An onward block has no upper
// end, which is the shape the row that used to read "D193 onward — free" had.
func telemetryCovers(b [3]int, n int) bool {
	if n < b[0] {
		return false
	}
	return b[2] == 1 || n <= b[1]
}

func TestTelemetryPlanTakesTheNextFreeDecisionBlock(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	// The block, stated as a block, and stated as the next free one.
	if n := len(telemetryBlockRE.FindAllString(doc, -1)); n < 3 {
		t.Errorf("%s states the block it takes (D193–D200) in %d places: it must say so where the plan is described, where the numbers are taken and in the decisions table, so a reader who lands anywhere knows which block this is", telemetryPlan, n)
	}
	section := planSection(t, doc, "## 2. The numbers this plan takes")
	for _, want := range []string{
		"D193–D200", // the block
		"free",      // that it is free
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §2 does not say %q: the block it takes must be stated as the next free one, and that is the claim this test checks against the tree", telemetryPlan, want)
		}
	}

	// The claim, checked against the two documents that allocate blocks rather
	// than against the plan's memory of them. The website redesign, the most
	// recent work to take a block, is registered in both — docs/mobile/README.md's
	// block table and the coordinator's one table of every work's block — and the
	// telemetry plan is registered the same way. A block that reaches into
	// D193–D200 in either document is a collision, and no number is taken twice.
	for _, path := range []string{telemetryMobileReadme, telemetryCoordinator} {
		blocks := telemetryBlocks(t, readDoc(t, path))
		if len(blocks) < 5 {
			t.Fatalf("%s carries %d decision blocks, which is too few for this walk to mean anything: update this test's markers rather than letting a vacuous walk pass", path, len(blocks))
		}
		mine, others := 0, 0
		for _, b := range blocks {
			into := false
			for n := 193; n <= 200; n++ {
				if telemetryCovers(b, n) {
					into = true
					break
				}
			}
			if !into {
				continue
			}
			if b[0] == 193 && b[1] == 200 && b[2] == 0 {
				mine++
				continue
			}
			others++
			t.Errorf("%s's block D%d–D%d (or D%d onward) covers a number in D193–D200, which %s takes: the block is not free, and no decision number may be taken twice", path, b[0], b[1], b[0], telemetryPlan)
		}
		if mine != 1 {
			t.Errorf("%s records the telemetry plan's block (D193–D200) %d times: a block that is taken but not registered is a number the next writer will take again", path, mine)
		}
	}

	// D193 is the owner's answered ruling and D194–D200 are not. The plan must
	// say both, because the difference is what a reader takes to the owner.
	if !strings.Contains(doc, "D193 is already answered") && !strings.Contains(doc, "**D193 is answered**") && !strings.Contains(doc, "answered 2026-10-19") {
		t.Errorf("%s does not record that D193 is answered by the owner's ruling of 2026-10-19: the whole plan is built on that ruling, and a block whose first row is answered must say so", telemetryPlan)
	}
	if !strings.Contains(doc, "D194–D200 are open") && !strings.Contains(doc, "D194–D200 open") {
		t.Errorf("%s does not record that D194–D200 are open for the owner: the plan recommends seven answers and approves nothing, and a block that reads as answered would let a task start on a recommendation", telemetryPlan)
	}
}

// telemetryReservedChecks is the block the telemetry plan must NOT take. It is
// the functional spec's, recorded in docs/mobile/README.md as "reserved and
// unused": an unused number is not a free number, so the first free number after
// it is 111 and not 105.
var telemetryReservedChecks = []string{"105", "106", "107", "108", "109"}

// telemetryTakenChecks is what the plan takes: the first free numbers after every
// claim, and nothing above them.
var telemetryTakenChecks = []string{"111", "112", "113"}

func TestTelemetryPlanTakesChecksAndLeavesTheRest(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSection(t, doc, "## 12. Checks this plan takes")

	// The three it takes, the block it takes none of, the one it does not touch
	// and the first number it leaves free.
	for _, want := range append(append([]string{}, telemetryTakenChecks...), "105", "110", "114") {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §12 does not name check %s: the plan takes 111–113, takes none of the reserved 105–109, leaves 114 onward free, and must say why it does not touch 110 (the orchestrator's claim, not this work's)", telemetryPlan, want)
		}
	}
	if !strings.Contains(section, "114 onward is free") && !strings.Contains(section, "114\nonward free") {
		t.Errorf("%s's §12 does not state which number is the first free one after the block it takes: a reader cannot tell what is still available from a list of the three numbers taken", telemetryPlan)
	}

	// And it takes none of the reserved block — the half a renumbering is most
	// likely to leave behind. A check ROW for one of them is the shape that
	// matters: the block is named in §12 for the reason it is not taken, so the
	// names themselves cannot be absent.
	for _, n := range telemetryReservedChecks {
		if strings.Contains(section, "| **"+n+"** |") {
			t.Errorf("%s's §12 carries a check row for %s: 105–109 are reserved and unused, and an unused number is not a free number — the plan takes 111–113 and must not name one of them as its own", telemetryPlan, n)
		}
	}

	// And the register agrees: 111–113 are PROPOSED, so no section and no
	// summary row for them may exist yet. This is the half that keeps the claim
	// honest — "proposed, not approved" is a statement about the register, and
	// the register is right there.
	register := readDoc(t, "docs/gcp-live-checklist.md")
	for _, n := range telemetryTakenChecks {
		if strings.Contains(register, "## Check "+n) {
			t.Errorf("docs/gcp-live-checklist.md writes a `## Check %s` section while %s says check %s is proposed and written by P7.5: a check is never run before its text is in the register, and a check that is written before the owner approves the phase is a check nobody agreed to", n, telemetryPlan, n)
		}
		row := "\n| " + n + " |"
		if strings.Contains(register, row) {
			t.Errorf("docs/gcp-live-checklist.md carries a summary row for check %s while %s says it is proposed: the row and the section are written together, by P7.5, after the owner approves Phase 7", n, telemetryPlan)
		}
	}
	// The register must also still be free of a written section above 104 at
	// all — 105–109 are reserved and 110 onward is where this plan's numbers
	// come from, so a section in or above 105 is a section somebody wrote under
	// a claim that is still open.
	highest := highestWrittenCheck(t, register)
	if highest > 104 {
		t.Errorf("docs/gcp-live-checklist.md has a written `## Check %d` section: 105–109 are reserved and the plan takes nothing out of them, so a written section above 104 is a number another claim has already spent", highest)
	}
}

// telemetryHighestRE finds a written check section's number. The register writes
// them as `## Check 81`, and `## Check 70a` for the lettered ones.
var telemetryHighestRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)[a-z]?\b`)

// highestWrittenCheck reads the highest check section a register document
// actually writes. It is derived rather than stated, because the number moves:
// PR #667 is approved to append `## Check 100`–`## Check 104`, which raises this
// reading from 89 to 104 without touching anything either plan takes.
func highestWrittenCheck(t *testing.T, register string) int {
	t.Helper()
	highest := 0
	for _, m := range telemetryHighestRE.FindAllStringSubmatch(register, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	return highest
}

func TestTelemetryPlanNamesTheFlagsExactly(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSubsection(t, doc, "### 4.2 The flags, named exactly")

	// Every switch that can gate collection, spelled as the SDKs spell it. A
	// misspelled switch is a switch that is not thrown, so the plan's whole
	// consent recommendation rests on these strings and they are pinned
	// verbatim. P7.1 confirms each against the SDK's own documentation at its
	// own merge (the plan's §14 says so), and if one is renamed there the pin
	// moves with it — which is the point of pinning it.
	for _, flag := range []string{
		"firebase_analytics_collection_enabled",    // Android, AndroidManifest meta-data
		"FIREBASE_ANALYTICS_COLLECTION_ENABLED",    // iOS, Info.plist
		"firebase_crashlytics_collection_enabled",  // Android, AndroidManifest meta-data
		"FirebaseCrashlyticsCollectionEnabled",     // iOS, Info.plist
		"google_analytics_adid_collection_enabled", // Android: the advertising id stays off
		"analytics_auto_collection_enabled",        // firebase.json, react-native block
		"crashlytics_auto_collection_enabled",      // firebase.json, react-native block
		"setAnalyticsCollectionEnabled",            // the runtime call, after consent
		"setCrashlyticsCollectionEnabled",          // the runtime call, after consent
	} {
		if !strings.Contains(section, flag) {
			t.Errorf("%s's §4.2 does not name %q: the consent gate is recommended as OFF until opt-in, and a switch named approximately is a switch that is not thrown", telemetryPlan, flag)
		}
	}

	// The recommendation itself, and the two device contexts the flags live in:
	// a reader has to be able to tell which file each one goes in.
	for _, want := range []string{"AndroidManifest.xml", "Info.plist", "firebase.json"} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §4.2 does not name %q: a flag with no file is a flag nobody can set", telemetryPlan, want)
		}
	}
	if !strings.Contains(section, "false") {
		t.Errorf("%s's §4.2 states no required value: every native switch is recommended `false`, and that is the whole of the \"nothing is collected before consent\" claim", telemetryPlan)
	}
}

// telemetryNativeConfig is the two files the plan says are never committed. They
// are walked from this list rather than from planPaths, and that is the point of
// the list: **the derivation cannot see the second one at all.** planPathRE's
// character class is `[A-Za-z0-9_./-]` — no uppercase — so
// `mobile/GoogleService-Info.plist` never enters planPaths, and the first
// version of this pin, which trusted the derivation, left the iOS file's half of
// the rule untested while claiming to require it: committing the plist left the
// suite green. The blind spot is asserted below, so the day the derivation grows
// to cover it this list is called out rather than silently doubled.
var telemetryNativeConfig = []string{
	"mobile/google-services.json",
	"mobile/GoogleService-Info.plist",
}

// telemetryTracked says whether git has the path in its index. The tracked half
// is the stronger statement — a file can be absent from the working tree and
// still be committed — and the absence half is checked beside it either way, so a
// machine without git loses one signal rather than the whole rule.
func telemetryTracked(t *testing.T, path string) bool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Logf("git is not on PATH, so whether %s is tracked is unchecked here (its absence from the tree is still asserted): %v", path, err)
		return false
	}
	return exec.Command("git", "-C", "../..", "ls-files", "--error-unmatch", "--", path).Run() == nil
}

func TestTelemetryPlanKeepsTheNativeFilesOutOfGit(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSection(t, doc, "### 4.3 The native config files are never committed")

	for _, want := range []string{
		"mobile/google-services.json",
		"mobile/GoogleService-Info.plist",
		"never",
		"EAS",
		".gitignore",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §4.3 does not say %q: the two native files name the project and carry its key, so the rule, the two paths, the ignore file and the delivery mechanism all have to be written down together", telemetryPlan, want)
		}
	}

	// The rule, checked against the tree — for BOTH files, one by one. A plan
	// that recommends keeping a file out of git while the file is in git is the
	// failure this assertion exists to catch, and it caught only half of it while
	// the iOS path was left to a derivation that cannot spell it.
	for _, p := range telemetryNativeConfig {
		if _, err := os.Stat("../../" + p); err == nil {
			t.Errorf("%s is in the tree: %s says both native config files are never committed — they name the Fugaro-operated project and carry its key — and this one has been added anyway", p, telemetryPlan)
		}
		if telemetryTracked(t, p) {
			t.Errorf("%s is tracked by git: %s says both native config files are never committed, and tracking one is the way the rule is broken while the working tree still looks clean", p, telemetryPlan)
		}
	}
	// The blind spot, asserted rather than assumed: the explicit list above is
	// only necessary while planPaths cannot derive the iOS path. If it can, this
	// test's own reason for existing has gone and the two must be reconciled.
	if slices.Contains(planPaths(doc), "mobile/GoogleService-Info.plist") {
		t.Errorf("planPaths now derives mobile/GoogleService-Info.plist from %s: telemetryNativeConfig exists because the path derivation cannot see an uppercase segment, and if it can, the derivation and the list must be reconciled rather than kept side by side", telemetryPlan)
	}

	readme := readDoc(t, "mobile/README.md")
	if strings.Contains(readme, "google-services.json") && !strings.Contains(readme, "never") {
		t.Errorf("mobile/README.md names google-services.json without the rule that keeps it out of git: the app's own README is where a developer looks for the build's file list")
	}
}

// telemetryIndexRow returns the line of docs/mobile/README.md's table that opens
// with the marker given. The two rows the block below compares are one table's,
// and a row is the unit a reader reads: the contradiction this catches is between
// two rows that disagree about the same numbers.
func telemetryIndexRow(t *testing.T, index, marker string) string {
	t.Helper()
	for _, line := range strings.Split(index, "\n") {
		if strings.HasPrefix(line, marker) {
			return line
		}
	}
	t.Fatalf("docs/mobile/README.md has no row opening %q: update this test's markers rather than letting the comparison pass vacuously", marker)
	return ""
}

func TestTelemetryPlanIsRegisteredInTheMobileDocuments(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	// The index: a row and a status sentence, the way the other mobile plans
	// (and design/push-notifications.md) are registered.
	index := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(index, "plans/2026-10-19-mobile-telemetry.md") {
		t.Errorf("docs/mobile/README.md's file table has no row for %s: an unregistered plan is a plan the next reader does not find", telemetryPlan)
	}
	for _, want := range append([]string{"D193"}, telemetryTakenChecks...) {
		if !strings.Contains(index, want) {
			t.Errorf("docs/mobile/README.md's telemetry row does not name %s: the index records what a plan takes — its decision block and its checks — or a collision is found by the writer of the next plan", want)
		}
	}
	// A reserved number is never reused: the functional spec's row keeps its
	// original words and the telemetry plan's note is dated beside them.
	if !strings.Contains(index, "105–109 reserved and unused") {
		t.Errorf("docs/mobile/README.md no longer carries \"105–109 reserved and unused\": that phrase is the anchor docs_coordinator_test.go reads the 105–109 block from, and the telemetry plan's dated note must sit beside it rather than replace it")
	}

	// **The two rows agree.** The Check 110 row (the voice design's own reading,
	// "nothing above 109 is claimed") and the Checks 100–109 row (the telemetry
	// plan's numbers) are two rows of one table making claims about one block, and
	// a review of this change found them contradicting each other: the row above
	// record 111–113 as taken while the row below said nothing above 109 was
	// claimed. Both must now say the same thing, and neither may carry the
	// undated claim.
	reservedRow := telemetryIndexRow(t, index, "| **Checks 100–109** |")
	takenRow := telemetryIndexRow(t, index, "| **Check 110** |")
	for _, row := range []struct{ name, text string }{
		{"the Checks 100–109 row", reservedRow},
		{"the Check 110 row", takenRow},
	} {
		for _, want := range append(append([]string{}, telemetryTakenChecks...), "114") {
			if !strings.Contains(row.text, want) {
				t.Errorf("docs/mobile/README.md's %s does not name %s: the telemetry plan takes 111–113 and leaves 114 onward free, and the row a reader lands on has to say so — one row recording it and the other not is the contradiction this pair of assertions exists to catch", row.name, want)
			}
		}
		if strings.Contains(row.text, "nothing above 109 is claimed") {
			t.Errorf("docs/mobile/README.md's %s says \"nothing above 109 is claimed\", and the numbers above 109 that are claimed are 110 and the telemetry plan's 111–113: the claim is a dated reading of the register on 2026-10-19 and has to say so, or the two rows of this table disagree", row.name)
		}
	}

	// The plan: Phase 7, proposed, with its tasks and their dependency rows.
	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	if !strings.Contains(plan, "## Phase 7 —") {
		t.Errorf("docs/mobile/implementation-plan.md has no Phase 7 section: %s registers its tasks there, and a task list that lives only in a plan is a task list the phase table does not count", telemetryPlan)
	}
	if !strings.Contains(plan, "plans/2026-10-19-mobile-telemetry.md") {
		t.Errorf("docs/mobile/implementation-plan.md does not cite %s: the phase's authority is the plan, and the plan is where its files, flags and checks are written out", telemetryPlan)
	}
	for n := 1; n <= 5; n++ {
		want := "### Task P7." + strconv.Itoa(n)
		if !strings.Contains(plan, want) {
			t.Errorf("docs/mobile/implementation-plan.md has no %q: Phase 7 proposes five tasks, each independently mergeable with its own verification and reviewer tier", want)
		}
	}

	// The check the brief's own instruction turns on: the block it takes is
	// stated as the next free one, and the plan says which numbers it did NOT
	// take. That sentence is what stops the next writer from taking 105 again.
	if !strings.Contains(doc, "next free") {
		t.Errorf("%s does not state that the block it takes is the next free one: the brief asks for the next free block and for the document to say so", telemetryPlan)
	}
}

// telemetryWillCreate is the paths the plan names that are deliberately not in
// the tree, with the task that will create each. Naming them here rather than
// leaving them out of the derivation is the point — each is asserted to be
// *still absent*, so this list cannot rot into a place where a real path hides,
// and each is asserted to be still named by the plan.
//
// This list excuses only paths the derivation CAN see, which is why
// `mobile/GoogleService-Info.plist` is not in it: planPathRE matches no
// uppercase segment, so the plist never reaches planPaths and an entry here
// would fail the converse walk below. The two native config files are asserted
// one by one in TestTelemetryPlanKeepsTheNativeFilesOutOfGit instead — and
// `mobile/google-services.json` is listed here as well as there, because it IS a
// path the derivation sees and the excuse has to be given somewhere.
var telemetryWillCreate = map[string]string{
	"mobile/app.config.js":                         "P7.1 (PR #675's file, not on main; the plugin entry, the guard and the gitignore)",
	"mobile/eas.json":                              "P7.1 (PR #675's file, not on main; the two env entries per profile)",
	"mobile/firebase.json":                         "P7.1 (the two react-native auto-collection switches, §4.2)",
	"mobile/google-services.json":                  "never — §4.3: delivered by an EAS file secret and kept out of git, so its absence here is the rule (checked beside `mobile/GoogleService-Info.plist` in TestTelemetryPlanKeepsTheNativeFilesOutOfGit)",
	"mobile/src/telemetry/consent.js":              "P7.2 (the app's sheet logic, as a function of its ports)",
	"mobile/src/telemetry/analytics-calls.test.js": "P7.3 (the scan of §4.2's rules with its planted positive controls)",
	"mobile/src/telemetry/events.test.js":          "P7.3 (the never-send test, over a fixture account, mailbox and token)",
	"shared/telemetry-consent":                     "P7.2 (the pure choice machine and the platform flag table)",
	"mobile/src/telemetry":                         "P7.2 (consent.js) and P7.3 (the event module, the enum sets and the breadcrumbs)",
}

func TestTelemetryPlanCitesOnlyPathsThatExist(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	paths := planPaths(doc)
	if len(paths) < 15 {
		t.Fatalf("planPaths found %d paths in %s: the derivation, not the plan, is what broke", len(paths), telemetryPlan)
	}
	named := map[string]bool{}
	for _, p := range paths {
		named[p] = true
	}
	for _, p := range paths {
		if why, ok := telemetryWillCreate[p]; ok {
			if _, err := os.Stat("../../" + p); err == nil {
				t.Errorf("%s: %s exists now (%s), so it is no longer a path this plan creates — or, for the two native config files, it is a file the plan says must never be committed: it must leave telemetryWillCreate and be checked like any other", telemetryPlan, p, why)
			}
			continue
		}
		if pathExists(p) {
			continue
		}
		t.Errorf("%s cites %s, which is not in the tree: a path the plan names and cannot be found is a later implementer's dead end", telemetryPlan, p)
	}
	for p, why := range telemetryWillCreate {
		if !named[p] {
			t.Errorf("telemetryWillCreate names %s (%s), which the plan no longer cites: the list must only excuse paths the document actually names", p, why)
		}
	}
}

// telemetryPlain strips markdown emphasis, so an assertion about a PHRASE does
// not turn on whether the document happens to bold a word inside it. It caught
// itself: "a **step 7b** in the approval gate" and "a step **7b** in the
// approval gate" say the same thing and one of them failed the search.
func telemetryPlain(s string) string {
	return strings.NewReplacer("*", "", "_", "").Replace(s)
}

// telemetryPost667 is the register as PR #667 is approved to leave it: P6.11
// writes `## Check 100`–`## Check 104` and their summary rows. The premise
// assertion below is run against the register as it is AND against this, so it
// cannot be satisfied by a reading that only holds before #667 merges — which is
// exactly how the premise broke: it pinned the literal "## Check 89 is the
// highest written section", a statement #667 falsifies.
func telemetryPost667(register string) string {
	var b strings.Builder
	b.WriteString(register)
	b.WriteString("\n")
	for n := 100; n <= 104; n++ {
		b.WriteString("\n## Check " + strconv.Itoa(n) + "\n\nP6.11's check, written by PR #667.\n")
	}
	return b.String()
}

// TestTelemetryPlanAgreesWithItsOwnRegistration pins the four agreements a
// review of this change found broken, each between two documents this change
// edits. They are the failure mode a plan plus a registration always has: the
// same fact written twice, corrected once.
//
//  1. **The phase is five tasks.** The plan, the mobile plan's Phase 7 section
//     and the index all say P7.1–P7.5, and §10.1's order diagram, its level
//     table and §12's three check rows all said "P7.6" — a task defined
//     nowhere, because running a check merges nothing and is the owner's. Every
//     reference is now named as the owner's step, and no P7.6 exists.
//  2. **The gate step is 7b.** The Phase 7 intro said "a new step 8" while the
//     step added beside it, and the plan's §13, both say "7b". One gate, one
//     label.
//  3. **The premise the block rests on holds before and after PR #667.** §2.2
//     said "## Check 70c" and that none of 80–89 had a section; the register
//     writes `## Check 81`, `## Check 82` and `## Check 89`. The premise is now
//     read from the register — and stated so that #667 appending
//     `## Check 100`–`## Check 104` does not falsify it: what must hold is that
//     the number the plan states is a section the register writes, and that
//     nothing at or above the block it takes out of nothing is written. Both are
//     checked against the register with #667's five sections appended too.
//  4. **The check numbers are the first free numbers after every claim.** The
//     plan took 105, 106 and 107 out of a block `docs/mobile/README.md` records
//     as "reserved and unused", while applying the opposite rule to the
//     decision numbers; it now takes 111–113 and takes nothing out of 100–109.
//
// Mutation (verified, run and restored): put "P7.6" back in the level table;
// change "step 7b" to "step 8"; state a check number the register does not write
// as its highest section (`## Check 88` for `## Check 89`); or take 105 back —
// this test fails, each of them.
func TestTelemetryPlanAgreesWithItsOwnRegistration(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	index := readDoc(t, telemetryMobileReadme)

	// (1) No P7.6 anywhere: the phase is five tasks, and running a check is not
	// one of them.
	for _, tc := range []struct{ path, text string }{
		{telemetryPlan, telemetryPlain(doc)},
		{"docs/mobile/implementation-plan.md", telemetryPlain(plan)},
		{telemetryMobileReadme, telemetryPlain(index)},
	} {
		if strings.Contains(tc.text, "P7.6") {
			t.Errorf("%s names P7.6: Phase 7 is five tasks (P7.1–P7.5) and no sixth is defined anywhere — running checks 111–113 is the owner's, it merges nothing, and it must not be written as a task", tc.path)
		}
	}

	// (2) One gate, one label, and it is the one the step itself carries.
	if !strings.Contains(telemetryPlain(plan), "step 7b") {
		t.Errorf("docs/mobile/implementation-plan.md does not say \"step 7b\": the gate step this phase adds is labelled 7b in the gate itself, and the Phase 7 intro has to agree with it")
	}
	if strings.Contains(telemetryPlain(plan), "step 8") || strings.Contains(telemetryPlain(doc), "step 8") {
		t.Errorf("the telemetry plan or the mobile plan says \"step 8\": the gate's seven steps were not renumbered and this phase adds one sub-step, labelled 7b — a reader told to look for a step 8 finds none")
	}
	if !strings.Contains(telemetryPlain(doc), "step 7b") {
		t.Errorf("%s does not say \"step 7b\": it tells the reader which gate step its phase enters, and the label has to be the one the gate carries", telemetryPlan)
	}

	// (3) The premise about the register's written sections, read from the
	// register and required to survive #667. The SENTENCE, not the token: the
	// plan names `## Check 89` in three other places, so a bare Contains would
	// stay green while the stated premise said something else — which is exactly
	// how the false premise survived the first pin.
	stated := regexp.MustCompile("highest check section written is `## Check ([0-9]+)[a-z]?`")
	m := stated.FindStringSubmatch(telemetryPlain(doc))
	if m == nil {
		t.Fatalf("%s no longer states the highest written check section in the shape \"the highest check section written is `## Check N`\": the premise the block rests on has to be findable, and this test cannot check a sentence it cannot locate", telemetryPlan)
	}
	statedNumber, _ := strconv.Atoi(m[1])

	// The premise is only complete with the PR that moves the reading: P6.11
	// appends 100–104, so a plan that states the reading without saying so leaves
	// the next reader to conclude that the free block is after 89.
	if !strings.Contains(telemetryPlain(doc), "#667") {
		t.Errorf("%s states the register's highest written check section without naming PR #667, which is approved to append `## Check 100`–`## Check 104` and raise that reading: the premise and the PR are one statement, and the plan also names #667 as one of the open pull requests it lands against", telemetryPlan)
	}

	register := readDoc(t, "docs/gcp-live-checklist.md")
	for _, tc := range []struct{ name, text string }{
		{"the register as it is", register},
		{"the register with #667's five sections appended", telemetryPost667(register)},
	} {
		written := map[int]bool{}
		for _, mm := range telemetryHighestRE.FindAllStringSubmatch(tc.text, -1) {
			if n, err := strconv.Atoi(mm[1]); err == nil {
				written[n] = true
			}
		}
		if len(written) == 0 {
			t.Fatalf("%s carries no written check section: this assertion would otherwise pass whatever the plan claims about the highest one", tc.name)
		}
		// The number the plan states has to be a section the register writes —
		// before and after #667, because 89 stays written either way.
		if !written[statedNumber] {
			t.Errorf("%s states the highest check section written is `## Check %d`, and %s writes no such section: a premise read from the register has to name a section the register has, and #667 appending sections above it does not remove the one it read", telemetryPlan, statedNumber, tc.name)
		}
		// And nothing at or above the reserved block is written, which is what
		// makes "the first free number is 111" true against this register.
		if h := highestWrittenCheck(t, tc.text); h > 104 {
			t.Errorf("%s states its block is the first free one and takes nothing out of 105–109, and %s writes `## Check %d`: the claim is false against this register, and 105–109 are reserved whether or not a section sits there", telemetryPlan, tc.name, h)
		}
	}
}
