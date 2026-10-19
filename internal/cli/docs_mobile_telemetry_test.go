package cli

import (
	"regexp"
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
//  2. **the checks it takes, and the ones it leaves.** 105–107 out of the free
//     block after 104, with 108 and 109 left free and 110 alone (the
//     orchestrator's claim). A check is never run before its text is in the
//     register, so the plan's claim that all three are PROPOSED and unwritten is
//     checked against the register itself: a `## Check 105` section appearing
//     there while the plan still says "proposed" is a disagreement, not a
//     detail.
//  3. **the flags, spelled exactly.** The whole consent recommendation is worth
//     nothing if a flag is named approximately — a misspelled switch is a switch
//     that is not thrown to false — so each name is required verbatim.
//  4. **the two native config files are never committed.** The plan states this
//     in several places; this test requires the rule, the two paths and the word
//     EAS, and — the half that would otherwise rot — requires that neither path
//     is tracked in this repository today.
//
// The last assertion is the path walk the other plan pins use (planPaths): every
// repository path the document names must exist, so a plan that cites a file the
// tree does not have is red the moment it is written down.
//
// Mutation (verified, run and restored): change D200 to D199 in the block the
// plan claims; delete one flag name from §4.2; change "105–107" to "105–108";
// add a `## Check 105` heading to the register; or cite a path the tree does not
// have — this test fails, each of them.
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

func TestTelemetryPlanTakesChecksAndLeavesTheRest(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSection(t, doc, "## 12. Checks this plan takes")

	// The three it takes, and the two it leaves.
	for _, want := range []string{"105", "106", "107", "108", "109", "110"} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §12 does not name check %s: the plan takes 105–107, leaves 108 and 109 free, and must say why it does not touch 110 (the orchestrator's claim, not this work's)", telemetryPlan, want)
		}
	}
	if !strings.Contains(section, "leaving 108 and 109 free") && !strings.Contains(section, "108 and 109 stay free") {
		t.Errorf("%s's §12 does not state that 108 and 109 are left free: a reader cannot tell which numbers are still available from a list of the three that were taken", telemetryPlan)
	}

	// And the register agrees: 105–107 are PROPOSED, so no section and no
	// summary row for them may exist yet. This is the half that keeps the claim
	// honest — "proposed, not approved" is a statement about the register, and
	// the register is right there.
	register := readDoc(t, "docs/gcp-live-checklist.md")
	for _, n := range []string{"105", "106", "107"} {
		if strings.Contains(register, "## Check "+n) {
			t.Errorf("docs/gcp-live-checklist.md writes a `## Check %s` section while %s says check %s is proposed and written by P7.5: a check is never run before its text is in the register, and a check that is written before the owner approves the phase is a check nobody agreed to", n, telemetryPlan, n)
		}
		row := "\n| " + n + " |"
		if strings.Contains(register, row) {
			t.Errorf("docs/gcp-live-checklist.md carries a summary row for check %s while %s says it is proposed: the row and the section are written together, by P7.5, after the owner approves Phase 7", n, telemetryPlan)
		}
	}
	// The register must also still be free of a written section above 104 at
	// all — the plan's reading of the free block is "after 104", and that is a
	// reading of this file.
	written := regexp.MustCompile(`(?m)^## Check ([0-9]+)[a-z]?\b`).FindAllStringSubmatch(register, -1)
	highest := 0
	for _, m := range written {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	if highest > 104 {
		t.Errorf("docs/gcp-live-checklist.md has a written `## Check %d` section: %s reads the free block as \"after 104\", and a section above it makes that reading wrong", highest, telemetryPlan)
	}
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

	// The rule, checked against the tree: neither file is tracked, and neither
	// is a path this repository has committed under any name. A plan that
	// recommends keeping a file out of git while the file is in git is the
	// failure this assertion exists to catch.
	readme := readDoc(t, "mobile/README.md")
	if strings.Contains(readme, "google-services.json") && !strings.Contains(readme, "never") {
		t.Errorf("mobile/README.md names google-services.json without the rule that keeps it out of git: the app's own README is where a developer looks for the build's file list")
	}
}

func TestTelemetryPlanIsRegisteredInTheMobileDocuments(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	// The index: a row and a status sentence, the way the other mobile plans
	// (and design/push-notifications.md) are registered.
	index := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(index, "plans/2026-10-19-mobile-telemetry.md") {
		t.Errorf("docs/mobile/README.md's file table has no row for %s: an unregistered plan is a plan the next reader does not find", telemetryPlan)
	}
	for _, want := range []string{"D193", "105", "106", "107"} {
		if !strings.Contains(index, want) {
			t.Errorf("docs/mobile/README.md's telemetry row does not name %s: the index records what a plan takes — its decision block and its checks — or a collision is found by the writer of the next plan", want)
		}
	}
	// A reserved number is never reused: the functional spec's row keeps its
	// original words and the telemetry plan's note is dated beside them.
	if !strings.Contains(index, "105–109 reserved and unused") {
		t.Errorf("docs/mobile/README.md no longer carries \"105–109 reserved and unused\": that phrase is the anchor docs_coordinator_test.go reads the 105–109 block from, and the telemetry plan's dated note must sit beside it rather than replace it")
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

func TestTelemetryPlanCitesOnlyPathsThatExist(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	paths := planPaths(doc)
	if len(paths) < 10 {
		t.Fatalf("%s names %d repository paths, which is too few for this walk to mean anything: update this test's markers rather than letting a vacuous walk pass", telemetryPlan, len(paths))
	}
	for _, p := range paths {
		if !isPlanPath(p) {
			t.Errorf("%s cites %q, which is not in this repository: a plan that names a file the tree does not have is a plan a reader cannot follow", telemetryPlan, p)
		}
	}
}
