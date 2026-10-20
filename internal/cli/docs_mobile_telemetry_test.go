package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
// no mobile/firebase.json and no google-services.json anywhere in the tree — so
// no test can hold its behaviour. The two store-path files it EDITS do exist
// (mobile/app.config.js and mobile/eas.json, merged by PR #675), which is why
// they are not in telemetryWillCreate and why
// TestTelemetryPlanModifiesTheFilesPR675Merged holds the plan's sentences about
// them. What a reader can check today, and what this file pins, is the things a
// plan of this shape has to get right and a reviewer is least likely to check
// twice:
//
//  1. **the numbers it takes.** D193–D200 is claimed to be the next FREE block,
//     which is a claim about the tree, so it is read from the tree and not from
//     the plan's memory of it. Two halves, and both are over documents rather
//     than over a phrase: the blocks the two allocation documents record are
//     walked, so a block reaching into D193–D200 is a collision; and **every
//     markdown file under docs/ is walked** for a range statement that reaches
//     into the block, so a number taken in a third document — which the two-
//     document walk cannot see — is caught by the same test. Each place the plan
//     states its block must state it as D193–D200 and as the next free one.
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
//     its own list rather than from planPaths, because planPaths drops the iOS
//     one (isPlanPath does not accept a `.plist`; see telemetryNativeConfig), and
//     the blind spot is asserted rather than assumed.
//  5. **the app carries no advertising identifier.** The website's privacy page
//     promises it, so it is a requirement rather than a hope: the two Android
//     switches `false`, the `AD_ID` permission removed from the merged manifest,
//     the iOS Analytics variant that links no `AdSupport`, each read per row and
//     each backed by the build-time test of §7.4 — which is what
//     TestTelemetryPlanRequiresNoAdvertisingIdentifier holds. The two names that
//     cannot be stated from memory are marked VERIFY with the way to verify them
//     named, and the test requires that marking rather than a guessed option.
//
// The last assertion is the path walk the other plan pins use (planPaths): every
// repository path the document names must exist, so a plan that cites a file the
// tree does not have is red the moment it is written down.
//
// Mutation (verified, run and restored): change D200 to D199 in the block the
// plan claims; write a check row or a name for 105 back into §12; add a
// `## Check 111` heading to the register; delete one flag name from §4.2; commit
// `mobile/GoogleService-Info.plist` (the file the extension allow-list drops) or
// `mobile/google-services.json`; state a check number the register does not
// write as its highest section; un-date the Check 110 row's "nothing above 109"
// claim; record 105, 106 and 107 as taken in the coordinator's checks row; take a
// number in D193–D200 from a third document under docs/; or cite a path the tree
// does not have — this test fails, each of them. And, for the corrections the
// rounds since then forced: let `mobile/app.config.js` back into
// telemetryWillCreate, say the plan's two store-path files are not on `main`, put
// "D192's rule" back on P7.4's word ceilings, let either README row claim 110 for
// its own design, flip a §4.2 ad-ID row to `true`, drop the iOS row's VERIFY, or
// empty the ad-ID requirement out of §7.4 or out of P7.1 — each of those fails
// too, run and restored like the rest. And for round 5's two corrections: put a
// `file.md:653` line citation back into §1, drop §1's "cited by symbol" sentence,
// name a base commit this branch is not built on, name no base at all, narrow
// planPathRE's class so it stops matching the plist, or widen isPlanPath's
// extension list so it starts — each of those fails, run and restored. And for
// round 11's four findings: restore the pre-D195 "two groups … the four switches
// that decide *what*" framing in §4.2's preamble, delete that preamble's
// "`true` six times … `false` twice" (the count below the table still stands), put
// the closing `**` back onto "including the option that must switch ad-ID
// collection off" in the mobile plan's open item 9, drop `expo` from §1's
// `mobile/index.js` row, put "and nothing else" back in that row, restore step
// 7b's sub-range of the block, or write any sub-range of D193–D200 into another
// document under docs/ — including one in a paragraph that mentions telemetry —
// each of those fails, run and restored.
const telemetryPlan = "docs/plans/2026-10-19-mobile-telemetry.md"

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

// telemetryStatedBlockRE finds a block statement the plan makes that starts at
// D193. Every one of them has to end at D200, and that is asserted per statement
// rather than by counting occurrences: a count anywhere in the document stays
// green while one of the places says D193–D199, which is the drift this pin is
// for.
var telemetryStatedBlockRE = regexp.MustCompile(`D193[–-]D([0-9]+)`)

// telemetryBlockPlaces are the places the plan states its block, each of which a
// reader can land on without reading the others. The names are the readers, not
// the line numbers: §2 is where the numbers are taken, §3 is the decisions table
// a reader takes to the owner, and the closing paragraph is what the document
// says it is when somebody quotes it.
var telemetryBlockPlaces = []struct{ name, heading string }{
	{"§2, The numbers this plan takes", "## 2. The numbers this plan takes"},
	{"§3, Owner decisions", "## 3. Owner decisions"},
	{"the closing status paragraph", "*Status of this document"},
}

func TestTelemetryPlanTakesTheNextFreeDecisionBlock(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	// The block, stated as a block — and every statement of it ending where the
	// block ends. The first version of this pin counted occurrences anywhere in
	// the document (`len(FindAllString) < 3`), which the plan's own §2.1 table
	// demonstrated the weakness of: it could say D193–D199 while the sentence
	// below it, the decisions heading and the closing paragraph all still said
	// D193–D200, and the suite stayed green.
	stated := telemetryStatedBlockRE.FindAllStringSubmatch(doc, -1)
	if len(stated) < 3 {
		t.Fatalf("%s states a block starting at D193 in %d places: it must say so where the numbers are taken, in the decisions table and in the closing paragraph, so a reader who lands anywhere knows which block this is", telemetryPlan, len(stated))
	}
	for _, m := range stated {
		if m[1] != "200" {
			t.Errorf("%s states the block it takes as D193–D%s: the block is D193–D200, and a block statement that ends anywhere else is a different block — the decisions table and the sentence beside it then disagree about which rows the owner is being asked for", telemetryPlan, m[1])
		}
	}
	for _, place := range telemetryBlockPlaces {
		start := strings.Index(doc, place.heading)
		if start < 0 {
			t.Fatalf("%s has no %q: update this test's markers rather than letting a place go unchecked", telemetryPlan, place.heading)
		}
		section := doc[start:]
		if j := strings.Index(section[len(place.heading):], "\n## "); j >= 0 && place.heading != "*Status of this document" {
			section = section[:len(place.heading)+j]
		}
		// The block, in the place itself, and — where the place is the one that
		// takes the numbers — that it is the next free one.
		if !strings.Contains(section, "D193–D200") {
			t.Errorf("%s's %s does not state the block it takes (D193–D200): a place a reader lands on without reading the others has to say which block this is", telemetryPlan, place.name)
		}
		if place.heading == "## 2. The numbers this plan takes" && !strings.Contains(section, "free") {
			t.Errorf("%s's §2 does not say the block is free: the brief asks for the next free block, and this is the place the plan takes it", telemetryPlan)
		}
	}
	// The closing paragraph is what the document says it is when somebody quotes
	// it, and it is a paragraph rather than a section — the sentence above takes
	// it to the end of the file, which is where it is.
	if closing := doc[strings.LastIndex(doc, "*Status of this document"):]; !strings.Contains(closing, "D193–D200") || !strings.Contains(closing, "free") {
		t.Errorf("%s's closing status paragraph does not state the block it takes (D193–D200) as the next free one: that paragraph is the document's own summary, and it is the one a reader quotes", telemetryPlan)
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

	// The block is answered in full as of 2026-10-19, D199 last. The plan has to
	// say so in the words the block's own list of decisions uses, because that
	// list is what a reader takes to the owner: an answered row is a ruling a
	// task can be built on, and D199 is the row that governed P7.1.
	if !strings.Contains(doc, "D193 is already answered") && !strings.Contains(doc, "**D193 is answered**") && !strings.Contains(doc, "answered 2026-10-19") {
		t.Errorf("%s does not record that D193 is answered by the owner's ruling of 2026-10-19: the whole plan is built on that ruling, and a block whose first row is answered must say so", telemetryPlan)
	}
	if !strings.Contains(doc, "D199: accepted, owner 2026-10-19") {
		t.Errorf("%s does not carry the line \"D199: accepted, owner 2026-10-19\" in its list of decisions: D199 is the row that decides which project the native config files name and where an installation's own connection comes from, the owner accepted it on 2026-10-19, and a list that still reads as seven-answered-one-open sends a reader to the gate for an answer that is already in §3", telemetryPlan)
	}
	if strings.Contains(doc, "D199 is the one row still open") {
		t.Errorf("%s still says D199 is the one row still open: the owner accepted it on 2026-10-19, and the sentence is the one a reader would take to the owner as a reason not to start P7.1", telemetryPlan)
	}
}

// telemetryAnswerCell returns the last cell of a markdown table row: the
// "Owner's answer" column every decision row in §3 ends with. It is read from
// the row itself rather than from a phrase beside it, because the row is the
// unit a reader reads — a paragraph saying the answer arrived while the row a
// reader lands on still says *(empty — owner)* is exactly the contradiction this
// file exists to catch.
func telemetryAnswerCell(t *testing.T, row string) string {
	t.Helper()
	cells := strings.Split(strings.TrimSpace(row), "|")
	if len(cells) < 4 {
		t.Fatalf("the row %q has %d cells: a decision row is five columns, and the answer is the last", row, len(cells))
	}
	return cells[len(cells)-2]
}

// TestTelemetryPlanKeepsNoAnsweredRowOpen is the pin the owner's answers of
// 2026-10-19 need. The owner answered all eight rows of the block — D193, D194,
// D195 (always on, AGAINST the plan's opt-in recommendation), D196, D197, D198,
// D200, and D199 ACCEPTED last, the row that asks whether the Firebase config is
// built into the app. Two failures are possible and this test catches both: an
// answered row left reading as open (which sends the owner back to answer a
// question they have answered, and hides a ruling the plan is now built on), and
// the block described in prose as still open while the rows say otherwise. Two
// things stay visible and are checked here too: the recommendation D195's answer
// overruled, and the question D199's answer was first given in place of — since
// superseding a recommendation, or answering a question, is not deleting
// either.
func TestTelemetryPlanKeepsNoAnsweredRowOpen(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	decisions := planSection(t, doc, "## 3. Owner decisions")

	// The rows the owner answered, and the phrase each answer carries. D194's
	// and D195's are the two that are more than "as recommended": the project's
	// own name, and the answer that supersedes the recommendation.
	answered := []struct{ row, want string }{
		{"D193", "answered"},
		{"D194", "fugaro-prod"},
		{"D195", "ALWAYS ON"},
		{"D196", "as recommended"},
		{"D197", "as recommended"},
		{"D198", "SECOND build"},
		{"D199", "ACCEPTED"},
		{"D200", "as recommended"},
	}
	for _, tc := range answered {
		cell := telemetryAnswerCell(t, telemetryRowWith(t, decisions, "| **"+tc.row+"** |"))
		if !strings.Contains(cell, "Owner answered 2026-10-19") {
			t.Errorf("%s's %s row does not record the owner's answer of 2026-10-19 in its own answer column: the answer is what a task is built on, and it has to be in the row a reader lands on", telemetryPlan, tc.row)
		}
		if !strings.Contains(cell, tc.want) {
			t.Errorf("%s's %s row records an answer that does not say %q: the row has to carry what the owner actually ruled, not that a ruling arrived", telemetryPlan, tc.row, tc.want)
		}
		for _, open := range []string{"NOT ANSWERED", "**(empty — owner)**", "*(empty — owner)*"} {
			if strings.Contains(cell, open) {
				t.Errorf("%s's %s row is answered (2026-10-19) and still carries %q: an answered row left reading as open sends the owner back to a question they have answered, and hides a ruling this plan is now built on", telemetryPlan, tc.row, open)
			}
		}
	}

	// D199's answer was first given as a question, and the question stays in the
	// row (D129's habit): the owner asked "firebase config should be built into
	// the app, no?" and then accepted the recommendation. What must NOT survive
	// is the row reading as an open question, which the loop above already
	// catches; what must survive is the question itself, so that the answer is
	// legible against what it answered.
	d199 := telemetryAnswerCell(t, telemetryRowWith(t, decisions, "| **D199** |"))
	for _, want := range []string{
		"firebase config should be built into the app, no?",
		"secure storage",
	} {
		if !strings.Contains(d199, want) {
			t.Errorf("%s's D199 row does not carry %q: the owner answered the question by accepting the recommendation, and the question and the secure-storage consequence it names have to stay legible in the row a reader lands on", telemetryPlan, want)
		}
	}

	// And no prose in the plan may describe the block as open when every one of
	// its rows is answered. Each string below was true before 2026-10-19 and is
	// false now; leaving one in sends a reader to the gate for answers that are
	// already in §3.
	for _, stale := range []string{
		"D194–D200 are open",
		"D194–D200 open",
		"D194–D200 are open for the owner",
		"D194–D200 open for the owner",
		"each with a recommendation",
		"D199 is the one row still open",
		"seven of the eight rows are answered",
		"Seven of the eight rows are answered",
		"seven of the eight are answered",
	} {
		if strings.Contains(telemetryPlain(doc), telemetryPlain(stale)) {
			t.Errorf("%s still says %q: the owner answered all eight rows on 2026-10-19, D199's acceptance last, and a sentence that still describes the whole block as open is a sentence that hides the rulings this plan is built on", telemetryPlan, stale)
		}
	}
}

// TestTelemetryPlanRecordsWhatIsBuiltInAndWhatThePairingPayloadCarries is the
// pin D199's acceptance needs. The row was answered with a question — "firebase
// config should be built into the app, no?" — and the answer is a SPLIT, not a
// yes or a no: the telemetry project's two native config files are the same for
// every installation and so are compiled in, while each installation's own
// Firebase connection differs per installation and is unknown at build time, so
// it cannot be. A plan that records only half of that split is wrong in one of
// two directions: "the config is built in" alone would have a build carry one
// installation's database URL and web API key, and "it is not built in" alone
// would leave the SDKs with no project at all. §4.4 is where the split is
// written, so every assertion here is scoped to §4.4 rather than to the
// document: the same words appear in §3's row and in §4.1, and a document-wide
// Contains would stay green with §4.4's table deleted.
//
// Two further failures are caught here. The section must name the mechanism on
// each side of the split (EAS file secrets for what is compiled in; the
// encrypted pairing payload, the CLI's own record and G18 for what is not),
// because a split with no mechanism is an intention. And no line of the plan
// may carry a secret-shaped literal: the whole point of D199's answer is that
// the installation's web API key lives in secure storage and in the pairing
// payload, so the plan names the value's KIND and never a value.
//
// Each assertion is read from the row, sentence or line it is about, not from
// the document: the first draft read them all from §4.4, and deleting
// "Realtime Database URL" from the pairing-payload row proved nothing because
// the secure-storage row names the same two values.
//
// Mutations (verified, run and restored): delete the "Inside the pairing
// payload" row; delete "EAS file secrets" from the built-in row; delete
// "Realtime Database URL" from the pairing-payload row; delete
// "init_firebase.go" from it; change "Four items per account" to "Two items per
// account" in the secure-storage row; delete "SECRET_SHAPES" from that row;
// delete "Realtime Database URL" from that row; delete the dated amendment from
// docs/mobile/reconciliation.md's D111 row, or "four items per account" from
// it; delete the dated amendment from Global Constraint 4; or write a
// 39-character `AIza…` value into §4.4 — this test fails, each of them.
func TestTelemetryPlanRecordsWhatIsBuiltInAndWhatThePairingPayloadCarries(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	s44 := planSubsection(t, doc, "### 4.4 What is built in, what arrives in the pairing payload, and what is in secure storage (D199, accepted)")

	// The three groups, named as the row's answer names them. Each is read from
	// the ROW that carries it, not from the section: the same phrases appear in
	// the paragraphs below the table, so a section-wide Contains would stay green
	// with a row deleted — which is how this test's first draft proved nothing.
	builtIn := telemetryRowWith(t, s44, "| **Built into the build** |")
	payload := telemetryRowWith(t, s44, "| **Inside the pairing payload** |")
	storage := telemetryRowWith(t, s44, "| **In the phone's secure storage** |")

	// What is compiled in: the telemetry project's two native files, delivered
	// as EAS file secrets and naming the project the owner answered for.
	for _, want := range []string{
		"google-services.json",
		"GoogleService-Info.plist",
		"EAS file secrets",
		"fugaro-prod",
	} {
		if !strings.Contains(builtIn, want) {
			t.Errorf("%s §4.4's \"built into the build\" row does not name %q: the same-for-every-installation half of D199's answer is the telemetry project's two native config files, delivered as EAS file secrets and naming the project the owner answered for (D194)", telemetryPlan, want)
		}
	}

	// What arrives in the pairing payload: the installation's own connection,
	// which no build can carry, and the CLI side that already records it.
	for _, want := range []string{
		"Realtime Database URL",
		"web API key",
		"not known at build time**",
		"encrypted pairing payload",
		"init_firebase.go",
		"G18",
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("%s §4.4's pairing-payload row does not name %q: the installation's own Firebase connection differs per installation and is unknown at build time, so it is delivered inside the encrypted pairing payload the user's own installation hands over — and the CLI already records both values, which G18 reads for the QR", telemetryPlan, want)
		}
	}

	// What is in secure storage: four items, and the settings store's fence is
	// NOT relaxed to hold them. SECRET_SHAPES is matched against the raw row, not
	// telemetryPlain's flattening, because the underscores are what a reader
	// greps for.
	if !strings.Contains(storage, "SECRET_SHAPES") {
		t.Errorf("%s §4.4 does not name SECRET_SHAPES: the two new items must NOT be held in the settings entry, and the store's refusal of an API-key-shaped value is the reason D199's answer puts them in secure storage instead of relaxing that fence", telemetryPlan)
	}
	if !strings.Contains(strings.ToLower(telemetryPlain(storage)), "four items per account") {
		t.Errorf("%s §4.4 does not say the app holds four items per account: D199's acceptance is what amends D111's record from two to four, and the row that states the new record is the row that has to state its size", telemetryPlan)
	}
	for _, want := range []string{
		"ID token",
		"per-pairing MAC key",
		"Realtime Database URL",
		"web API key",
		"Keychain / Keystore",
	} {
		if !strings.Contains(storage, want) {
			t.Errorf("%s §4.4 does not name %q among the four items in secure storage: D54b's precedent is to list every item held and not only the obvious one, and a list that stops at D111's two hides the two D199 adds", telemetryPlan, want)
		}
	}

	// No secret-shaped literal, anywhere in the plan. The plan names the two
	// values' KINDS — a Realtime Database URL and a web API key — and the
	// repository's own rule is that no `AIza…` value appears anywhere it ships.
	if m := telemetryAPIKeyRE.FindString(doc); m != "" {
		t.Errorf("%s carries the secret-shaped literal %q: the plan names the installation's web API key as a KIND and never as a value, because the whole of D199's answer is that the value lives in the pairing payload and in secure storage and nowhere in this repository", telemetryPlan, m)
	}

	// And the two documents that repeat D111's record carry the amendment, read
	// from the sentence that repeats it rather than from the document — both name
	// 2026-10-20 elsewhere, so a document-wide Contains proves nothing.
	recon := readDoc(t, "docs/mobile/reconciliation.md")
	d111 := telemetryRowWith(t, recon, "| **D111** |")
	if !strings.Contains(strings.ToLower(telemetryPlain(d111)), "four items per account") {
		t.Errorf("docs/mobile/reconciliation.md's D111 row does not record that the list is now four items per account: D199's acceptance amends a record that row states, and an amendment kept in one file while the row a task is built on still reads \"Exactly two items, per account\" is a record that disagrees with itself")
	}
	if !strings.Contains(d111, "Amended 2026-10-20") {
		t.Errorf("docs/mobile/reconciliation.md's D111 row carries no dated amendment for D199's acceptance: the repository's habit is to keep the superseded text visible under a dated note (D129), and an undated change to a constraint a task is built on cannot be told from the text it replaced")
	}

	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	constraint := ""
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "4. **The app holds exactly two items per account") {
			constraint = line
			break
		}
	}
	if constraint == "" {
		t.Fatalf("docs/mobile/implementation-plan.md has no Global Constraint 4 line beginning \"4. **The app holds exactly two items per account\": this test cannot check an amendment to a constraint it cannot locate, and the constraint is the record every mobile task is built on")
	}
	if !strings.Contains(strings.ToLower(telemetryPlain(constraint)), "four items per account") {
		t.Errorf("docs/mobile/implementation-plan.md's Global Constraint 4 does not record that the list is now four items per account: D199's acceptance amends the constraint itself, and Global Constraint 12 is the precedent for amending one in place with a dated note rather than leaving it to be reconciled by hand")
	}
	if !strings.Contains(constraint, "Amended 2026-10-20") {
		t.Errorf("docs/mobile/implementation-plan.md's Global Constraint 4 carries no dated amendment for D199's acceptance: the amendment has to be dated, or the text it replaced cannot be told from the text that replaced it")
	}
}

// TestTelemetryPlanDoesNotClaimWorkAlreadyDone pins the four findings of
// round 10, and all four are the same mistake in different clothes: a sentence
// that described work as still to be done when the tree, or a decision the owner
// had already given, said otherwise.
//
//  1. **The Firebase project is not created.** D194 answered `fugaro-prod`, the
//     project the website is already deployed into, and §5 step 1 states
//     "Nothing is created for telemetry: no new project". §11 and the mobile
//     plan's Phase 7 registration nevertheless said the owner's live action was
//     "creating the Firebase project". The live action is **registering the two
//     Firebase apps in the project that exists**, and a plan that tells the
//     owner to create `fugaro-prod` tells them to create a project that is
//     already serving the website.
//  2. **The retention setting is not recorded anywhere.** §5 step 4 said
//     "D194's answer records which" retention is required, and §14's item 8
//     said the opposite: "D194 is answered and the retention setting is still
//     not recorded". D194's row answers which project and says nothing about
//     retention, so step 4 is where the owner notes it — which is what §14 said
//     and what the step has to say too.
//  3. **Neither native path is ignored yet, and the root `.gitignore` is not
//     where the rule goes.** §4.3's item 1 said both paths "are ignored at the
//     repository root's `.gitignore` and at `mobile/.gitignore`"; no entry for
//     either exists in either file, and no task names the root `.gitignore` —
//     P7.1, §10.1's level table and §13's edited table all name the app's own
//     file. Both paths are under `mobile/`, where the app's file governs them,
//     so the root is not touched and the plan says so instead of implying an
//     entry that is not there.
//
// Mutations (verified, run and restored): put "the owner creating the Firebase
// project" back in §11; delete "registering the two Firebase apps" from §11;
// restore "D194's answer records which" in §5's step 4; delete "no decision
// records it today" from it; restore "ignored at the repository root" in §4.3;
// delete "Neither path is ignored anywhere today" from it; or put
// "at both `.gitignore`s" back in §4.4 — this test fails, each of them.
func TestTelemetryPlanDoesNotClaimWorkAlreadyDone(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	flat := telemetryPlain(strings.Join(strings.Fields(doc), " "))
	register := telemetryPlain(strings.Join(strings.Fields(readDoc(t, "docs/mobile/implementation-plan.md")), " "))

	// 1. Nothing creates the project. Read as a phrase over both documents, and
	// the §11 requirement beside it reads the plan's own section: a ban with no
	// positive requirement beside it is satisfied by deleting the sentence.
	for _, tc := range []struct{ path, text string }{
		{telemetryPlan, flat},
		{"docs/mobile/implementation-plan.md", register},
	} {
		for _, stale := range []string{
			"the owner creating the Firebase project",
			"creating the Firebase project and uploading",
		} {
			if strings.Contains(tc.text, telemetryPlain(stale)) {
				t.Errorf("%s still says %q: D194 answered `fugaro-prod`, the project the website is already deployed into, and §5 step 1 states that nothing is created for telemetry — the owner's live action is REGISTERING the two Firebase apps in the project that exists, and a reader told to create it would create a second project beside the one serving the site", tc.path, stale)
			}
		}
	}
	s11 := telemetryPlain(strings.Join(strings.Fields(planSection(t, doc, "## 11. What this plan does not change")), " "))
	if !strings.Contains(s11, "registering the two Firebase apps") {
		t.Errorf("%s's §11 does not say the owner's live action is \"registering the two Firebase apps\": D194 answered a project that already exists, so the only thing the phase asks of the owner there is the two app registrations — and a section that lists the phase's live actions has to name the one it has", telemetryPlan)
	}

	// 2. The retention setting is the owner's to note, and no decision records
	// it. This is §14's item 8's own statement, and step 4 has to agree with it.
	for _, stale := range []string{"D194's answer records which", "D194's answer records the retention"} {
		if strings.Contains(flat, telemetryPlain(stale)) {
			t.Errorf("%s still says %q: D194's row answers which project the native files name and says nothing about retention, while §14's item 8 says the retention setting is still not recorded — one of the two sentences was false, and this one is it", telemetryPlan, stale)
		}
	}
	s5 := telemetryPlain(strings.Join(strings.Fields(planSection(t, doc, "## 5. The Fugaro-operated project")), " "))
	if !strings.Contains(s5, "no decision records it today") {
		t.Errorf("%s's §5 does not say that no decision records the retention setting today: step 4 tells the owner to note it and states the plan's own requirement (the shortest the console offers), and the section has to say that the number is theirs to record — otherwise a reader looks for a decision that does not exist", telemetryPlan)
	}

	// 3. One ignore file, the app's own, and neither path is ignored yet.
	if strings.Contains(flat, telemetryPlain("ignored at the repository root")) {
		t.Errorf("%s still says the two native paths are \"ignored at the repository root\": neither has an entry in the root `.gitignore` or in `mobile/.gitignore` today, and no task in any phase names the root — both paths are under `mobile/`, where the app's own file governs them, so the plan must not imply an entry nothing writes", telemetryPlan)
	}
	if strings.Contains(flat, "at both `.gitignore`s") {
		t.Errorf("%s still says the paths are ignored at \"both `.gitignore`s\": the root's is not touched and does not need to be — a rule written in two files is a rule corrected in one", telemetryPlan)
	}
	s43 := telemetryPlain(strings.Join(strings.Fields(planSection(t, doc, "### 4.3 The native config files are never committed")), " "))
	for _, want := range []string{
		"The repository root's `.gitignore` is not touched",
		"Neither path is ignored anywhere today",
	} {
		if !strings.Contains(s43, want) {
			t.Errorf("%s's §4.3 does not say %q: the rule lives in the app's own ignore file, the root's is deliberately left alone, and both entries are P7.1's to add — a reader who cannot tell what is in the tree from what the task will add takes an unwritten rule for a written one", telemetryPlan, want)
		}
	}
}

// TestTelemetryPlanBalancesTheEmphasisInItsReadsOfThePhaseSevenPlan reads a
// paragraph of the MOBILE plan, and it is here because the paragraph is about
// this plan's own requirements. Open item 9 says which of Phase 7's requirements
// were read from memory of the SDKs' documentation rather than from a page read
// that day — a warning to the owner, and the reason this plan's §14 carries the
// UNVERIFIED list. Its emphasis was unbalanced: the `**` opened before "read" was
// closed by the marker meant to OPEN "including the option that must switch ad-ID
// collection off on each platform (P7.1, and the plan's §14)", so from there to
// the end of the item bold and plain text are inverted — in the one paragraph
// whose whole point is which half was actually read.
//
// Mutation (verified, run and restored): move the closing `**` back off "today"
// and onto "including" — the text as it was — and this test fails.
func TestTelemetryPlanBalancesTheEmphasisInItsReadsOfThePhaseSevenPlan(t *testing.T) {
	const path = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, path)

	// The item as a paragraph: it wraps over a dozen lines, so a line-by-line
	// read would see markers balanced within no line at all.
	item := ""
	for _, para := range strings.Split(doc, "\n\n") {
		if strings.Contains(para, "9. **Phase 7 (telemetry) is proposed and unbuilt") {
			item = strings.Join(strings.Fields(para), " ")
		}
	}
	if item == "" {
		t.Fatalf("%s's open item 9 is gone or renumbered: this test reads the paragraph that states which Phase 7 requirements were read from memory, and it cannot judge a paragraph it cannot find", path)
	}
	if n := strings.Count(item, "**"); n%2 != 0 {
		t.Errorf("%s's open item 9 carries %d `**` markers, an odd number: one span is left open, which inverts bold and plain text from there to the end of the item — in the item whose point is which half of Phase 7 was actually read", path, n)
	}
	if !strings.Contains(item, "from a page read today**") {
		t.Errorf("%s's open item 9 does not close its emphasis after \"a page read today\": the bold span is \"read from memory of the SDKs' documentation and not from a page read today\", and the ad-ID option named next is part of the list that was NOT read from a page — closing the span anywhere else inverts the two halves of the item", path)
	}
}

// telemetryImportRE reads an import specifier out of mobile/index.js, the app's
// entry: the default form, the named form and the side-effect form alike.
var telemetryImportRE = regexp.MustCompile(`(?m)^import\s+(?:[^'"]*?\sfrom\s+)?['"]([^'"]+)['"]`)

// TestTelemetryPlanReadsTheEntrysImports is §1's row on `mobile/index.js` checked
// against the file it cites. The row is the plan's evidence that the app wires no
// Firebase SDK, and a column that says what was read has to be what the file
// holds: the row named `expo-async-storage`, which is not a package at all — it is
// the app's OWN port module, `mobile/src/accounts/expo-async-storage.js`, reached
// through a `./src/` path — and it left out `expo` (the `registerRootComponent`
// import), while finishing with "and nothing else". The conclusion the row draws —
// no Firebase SDK is wired — survives all three; the column exists to record
// exactly what was read from the tree.
//
// Mutation (verified, run and restored): drop `expo` from the row's list of
// imports — a real import of the file, and the one the row was missing — and this
// test fails. Dropping the port module's path, or restoring "and nothing else",
// fails it too.
func TestTelemetryPlanReadsTheEntrysImports(t *testing.T) {
	const entry = "mobile/index.js"
	src := readDoc(t, entry)
	s1 := planSection(t, readDoc(t, telemetryPlan), "## 1. What is in the tree today, and what this plan therefore builds on")
	row := telemetryRowWith(t, s1, "`"+entry+"`")

	// Every BARE package the entry imports is named in the row, backticked: a
	// substring test would be satisfied by `expo-secure-store` for `expo`, which
	// is the one the row was missing. The relative specifiers are this app's own
	// modules; the row names them as a group and the async-storage port module by
	// path, which is the name the row got wrong.
	specs, bare := 0, 0
	for _, m := range telemetryImportRE.FindAllStringSubmatch(src, -1) {
		spec := m[1]
		specs++
		if strings.HasPrefix(spec, ".") {
			continue
		}
		bare++
		if !strings.Contains(row, "`"+spec+"`") {
			t.Errorf("%s's §1 row on %s does not name %q, which the file imports: the row records what was read from the tree, so a package the file imports and the row does not list is a column a reader cannot trust", telemetryPlan, entry, spec)
		}
	}
	if specs < 9 || bare < 5 {
		t.Fatalf("%s yielded %d import specifiers (%d of them bare packages), which is too few for it to be the app's entry: update this test's regexp rather than letting the comparison above pass vacuously", entry, specs, bare)
	}

	// The port module is named as what it is — by the path the file imports it
	// from — and the absence of Firebase as what it is. `expo-async-storage` reads
	// like a package and is not one; the third-party storage package is
	// `@react-native-async-storage/async-storage`.
	port := ""
	for _, m := range telemetryImportRE.FindAllStringSubmatch(src, -1) {
		if strings.HasPrefix(m[1], ".") && strings.Contains(m[1], "expo-async-storage") {
			port = m[1]
		}
	}
	if port == "" {
		t.Fatalf("%s imports no expo-async-storage port: the row's correction is about that module, and this test cannot hold a name the file no longer imports", entry)
	}
	for _, want := range []string{"@react-native-async-storage/async-storage", "`" + port + "`", "no Firebase import"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §1 row on %s does not say %q: the row is the plan's evidence that no Firebase SDK is wired, and it has to record the imports it actually read — the storage package's real name, this app's own port module by the path the file imports it from, and the absence of any Firebase import", telemetryPlan, entry, want)
		}
	}

	// And nothing in the row claims its list is complete by fiat. "and nothing
	// else" was false as written — the file imports `expo` too — and a
	// completeness claim is what makes the rest of the row unfalsifiable.
	if strings.Contains(row, "and nothing else") {
		t.Errorf("%s's §1 row on %s says \"and nothing else\": the imports are listed above it, and a completeness claim beside a list that was missing one of them is a claim a reader takes for a reading — the row states what was read, and does not certify that the list is exhaustive", telemetryPlan, entry)
	}
}

// telemetryAPIKeyRE matches a Google API key VALUE, not the shape's name: 39
// characters starting `AIza`, which is `SECRET_SHAPES`'s own `google-api-key`
// pattern. The plan writes the ellipsis form (`AIza…`) when it names the shape,
// which is not a value and does not match.
var telemetryAPIKeyRE = regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)

// telemetryScreenEnumRE reads the backticked value list of §7.2's
// `screen_shown` row. A regexp rather than a cell split: the enum is written
// with markdown-escaped pipes (`\|`), so splitting the row on "|" cuts it into
// a dozen pieces and the cell the test wants is not one of them.
var telemetryScreenEnumRE = regexp.MustCompile("`screen: ([^`]*)`")

// telemetryRangeRE finds a decision-number range as the documents write it:
// "D1–D20", "D180–D192", "**D193–D200**", "D39–D54b". It needs the dash, so an
// "onward" row ("D201 onward") and a bare mention ("D193 is already answered")
// are not ranges and are not read as one.
var telemetryRangeRE = regexp.MustCompile(`D([0-9]+)[a-z]?[–-]D?([0-9]+)`)

// TestTelemetryPlanTakesTheOnlyBlockReachingD193 is the claim "D193–D200 is the
// next free block", checked against **every markdown file under docs/** rather
// than against the two allocation documents' summaries. The pin's own header said
// it read the highest D-number written anywhere in docs/ out of the tree while
// the code walked two files, so a number taken in a third document — a new design
// claiming D193–D199, say — was not caught by the very test that pins "next
// free". It is caught now, and the walk is over the tree.
//
// The one statement allowed to reach into D193–D200 is the block itself, D193–D200:
// a document that states it in full is stating what this plan took
// (docs/mobile/README.md's row and the phase in the plan register it that way),
// and **any other range that touches the block is a second claim on a number that
// is taken — including a sub-range, and including one written in a paragraph that
// mentions telemetry.** That last clause is the point: the walk used to excuse a
// range whenever its paragraph named "telemetry", "Phase 7", "P7." or "next free
// decision block", which is a topic and not a claim of ownership, so another
// document could write "**D193–D199**" beside the word "telemetry" and the pin
// passed — the exact failure mode this test was rewritten to catch. A sub-range
// is never evidence that a paragraph is describing this plan's block; stating the
// block in full is, and that is the whole exemption.
//
// Mutation (verified, run and restored): append "**D193–D199** the widgets plan's"
// to any document under docs/ other than this plan — docs/mobile/spec.md was
// used — and this test fails, naming the file and the line. The sharper mutation,
// and the one the exemption above was removed for: append "**D193–D199** the
// widgets plan's, which also covers telemetry" — a sub-range in a paragraph that
// names telemetry — and it fails too.
func TestTelemetryPlanTakesTheOnlyBlockReachingD193(t *testing.T) {
	var files []string
	err := filepath.WalkDir("../../docs", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, strings.TrimPrefix(path, "../../"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The walk has to have read the tree: a regexp that stopped matching would
	// otherwise pass by finding nothing.
	if len(files) < 20 {
		t.Fatalf("the walk over docs/ read %d markdown files, which is too few for it to be the tree: update this test's markers rather than letting a vacuous walk pass", len(files))
	}
	ranges := 0
	for _, file := range files {
		if file == telemetryPlan {
			continue // this plan's own statements are the ones the pin is about
		}
		// **A paragraph, not a line**, because these documents wrap: the phase that
		// registers this plan writes its block on one line and what the block is on
		// the next, and reading them apart turns a sentence about this plan into a
		// claim by somebody else. What is excused is the block written in full,
		// never a sub-range and never a paragraph's topic — see this test's header.
		line := 1
		for _, para := range strings.Split(readDoc(t, file), "\n\n") {
			flat := strings.Join(strings.Fields(para), " ")
			for _, m := range telemetryRangeRE.FindAllStringSubmatch(flat, -1) {
				lo, errLo := strconv.Atoi(m[1])
				hi, errHi := strconv.Atoi(m[2])
				if errLo != nil || errHi != nil || hi < lo {
					continue
				}
				ranges++
				if hi < 193 || lo > 200 {
					continue
				}
				if lo == 193 && hi == 200 {
					continue // this plan's block, and only ever as the whole block
				}
				t.Errorf("%s:%d names the block D%d–D%d, which reaches into D193–D200: %s takes that block and no decision number is taken twice, so this is a statement made about a block that is not free", file, line, lo, hi, telemetryPlan)
			}
			line += strings.Count(para, "\n") + 2
		}
	}
	if ranges < 20 {
		t.Fatalf("the walk found %d decision-number ranges under docs/: too few for the reading above to mean anything, and a range matcher that stopped working would otherwise pass by finding no collision", ranges)
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
	// And what the register must be free of is THE NUMBERS THIS PLAN TAKES — not
	// a ceiling on the register. This assertion used to require
	// `highestWrittenCheck(register) <= 104`, which was wrong twice over: it
	// fails the moment ANY later number is written (PR #632's check 110, which
	// this plan names as an open claim it deliberately does not touch, or the
	// reserved 105–109 when their holder writes them), and §2.2 states the
	// opposite principle — "the highest check section written is a reading", and
	// a reading moves without the block moving. The register is read twice, once
	// as it is and once with a later claim's section appended, so the ceiling
	// cannot creep back in: what is checked is what this plan takes.
	for _, tc := range []struct{ name, text string }{
		{"the register as it is", register},
		{"the register with a later claim's section appended", register + "\n## Check 110\n\nPR #632's claim, written by whoever holds it.\n"},
	} {
		for _, n := range telemetryTakenChecks {
			if strings.Contains(tc.text, "## Check "+n) {
				t.Errorf("docs/gcp-live-checklist.md writes a `## Check %s` section (%s) while %s says check %s is proposed and written by P7.5: a check is never run before its text is in the register, and the numbers this plan takes are the only ones whose written presence says anything about this plan — a number above them belongs to whichever claim holds it", n, tc.name, telemetryPlan, n)
			}
		}
	}
}

// telemetryHighestRE finds a written check section's number. The register writes
// them as `## Check 81`, and `## Check 70a` for the lettered ones.
var telemetryHighestRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)[a-z]?\b`)

func TestTelemetryPlanNamesTheFlagsExactly(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSubsection(t, doc, "### 4.2 The flags, named exactly")

	// Every switch that can gate collection, spelled as the SDKs spell it. A
	// misspelled switch is a switch that is not thrown, so the whole of D195's
	// answer rests on these strings and they are pinned verbatim. P7.1 confirms
	// each against the SDK's own documentation at its own merge (the plan's §14
	// says so), and if one is renamed there the pin moves with it — which is the
	// point of pinning it.
	for _, flag := range []string{
		"firebase_analytics_collection_enabled",                     // Android, AndroidManifest meta-data
		"FIREBASE_ANALYTICS_COLLECTION_ENABLED",                     // iOS, Info.plist
		"firebase_crashlytics_collection_enabled",                   // Android, AndroidManifest meta-data
		"FirebaseCrashlyticsCollectionEnabled",                      // iOS, Info.plist
		"google_analytics_adid_collection_enabled",                  // Android: the advertising id stays off
		"google_analytics_default_allow_ad_personalization_signals", // Android: and is not used to personalise
		"AdSupport", // iOS: the framework that must NOT be linked
		"com.google.android.gms.permission.AD_ID", // Android: the permission that must NOT be declared
		"analytics_auto_collection_enabled",       // firebase.json, react-native block
		"crashlytics_auto_collection_enabled",     // firebase.json, react-native block
		"setAnalyticsCollectionEnabled",           // the runtime call D195's answer forbids
		"setCrashlyticsCollectionEnabled",         // the runtime call D195's answer forbids
	} {
		if !strings.Contains(section, flag) {
			t.Errorf("%s's §4.2 does not name %q: the app's collection state is set by these switches and by nothing else, and a switch named approximately is a switch that is not thrown", telemetryPlan, flag)
		}
	}

	// The values, read from each switch's OWN row. D195's answer is what they
	// are: the six switches that decide whether collection happens at all are
	// `true` — there is no consent to wait for, so the build that carries the
	// SDK collects from the first launch — and the two that decide what may be
	// collected are `false`, the advertising identifier staying uncollected
	// under always-on exactly as it did under opt-in. Per row, because a value
	// named in prose beside a row saying the opposite is not a requirement.
	for _, sw := range []string{
		"firebase_analytics_collection_enabled",
		"FIREBASE_ANALYTICS_COLLECTION_ENABLED",
		"firebase_crashlytics_collection_enabled",
		"FirebaseCrashlyticsCollectionEnabled",
		"analytics_auto_collection_enabled",
		"crashlytics_auto_collection_enabled",
	} {
		if row := telemetryRowWith(t, section, sw); !strings.Contains(row, "`true`") {
			t.Errorf("%s's §4.2 row for %q does not require `true`: D195 is answered always-on, so the collection switches ARE the collection, and a row that reads `false` describes the opt-in recommendation the owner did not take", telemetryPlan, sw)
		}
	}
	for _, sw := range []string{
		"google_analytics_adid_collection_enabled",
		"google_analytics_default_allow_ad_personalization_signals",
	} {
		if row := telemetryRowWith(t, section, sw); !strings.Contains(row, "`false`") {
			t.Errorf("%s's §4.2 row for %q does not require `false`: the advertising identifier is not collected under D195's always-on answer any more than it was to be collected under the opt-in recommendation the owner overruled", telemetryPlan, sw)
		}
	}
	// And the two runtime calls are refused rather than used: a call that can set
	// collection state IS the switch D195 says the app does not have.
	for _, call := range []string{"setAnalyticsCollectionEnabled", "setCrashlyticsCollectionEnabled"} {
		if row := telemetryRowWith(t, section, call); !strings.Contains(row, "never called") {
			t.Errorf("%s's §4.2 row for %q does not say it is never called: D195 is answered always-on and the app has no switch, so a runtime call that turns collection on or off is the switch the answer rules out", telemetryPlan, call)
		}
	}

	// The arithmetic of the table, pinned because it is the sentence a reader
	// checks the table against at a glance — and because the earlier draft of
	// it counted four and four, which was the opt-in table's shape and not this
	// one's: six switches are thrown `true` (the four native collection ones and
	// the two firebase.json auto-collection ones) and two `false` (the ad-ID pair).
	for _, want := range []string{"`true` six times", "`false` twice"} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's §4.2 does not say %q: the table it introduces throws six switches `true` and two `false`, and a summary that counts them differently is a summary a reader trusts instead of the table", telemetryPlan, want)
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
		t.Errorf("%s's §4.2 states no required value: every switch in the table is thrown, one way or the other, and a table that names a switch without a value is a table an implementer reads as optional", telemetryPlan)
	}
}

// TestTelemetryPlanDescribesItsFlagTableAsTheTableItIs is §4.2's preamble read
// against the table fifteen lines below it. The preamble described "two groups" —
// "the four switches that decide *what*" against "the two switches that decide
// *whether*" — which is the shape of the opt-in table this plan recommended and
// the owner did not take, and not this table's shape at all: this table's four
// native collection switches are `true` under D195's always-on answer, and the
// two switches that preamble called "what" are the ad-ID pair, which is not among
// those four. An implementer who builds what it described builds four collection
// switches `false` — the table §7.4's build-time test refuses — and the sentence
// that contradicts it ("`true` six times … `false` twice") sat below the table,
// pinned, while the preamble above it shipped green. So the preamble is pinned to
// the table's own arithmetic, and the two stale framings are banned from it.
//
// Mutation (verified, run and restored): put the old preamble's opening back —
// "There are **two groups** and they answer different questions: the **four
// switches that decide *what*** the SDKs may collect" — and this test fails.
func TestTelemetryPlanDescribesItsFlagTableAsTheTableItIs(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	section := planSubsection(t, doc, "### 4.2 The flags, named exactly")

	// The preamble is everything before the table's header row, read as a slice
	// rather than as the section: a count or a framing stated BELOW the table
	// cannot satisfy an assertion about the paragraph that introduces it, which
	// is the whole reason this pin exists.
	at := strings.Index(section, "| What it gates |")
	if at < 0 {
		t.Fatalf("%s's §4.2 has no flag table: this test reads the paragraph that introduces it, and a section whose table is gone cannot be judged against it", telemetryPlan)
	}
	preamble := telemetryPlain(section[:at])
	if fields := len(strings.Fields(preamble)); fields < 40 {
		t.Fatalf("%s's §4.2 has %d words before its flag table, which is too few to be the paragraph that introduces it: update this test's marker rather than letting the assertions below pass against the heading", telemetryPlan, fields)
	}

	// The table's arithmetic, in the paragraph a reader meets the table through.
	for _, want := range []string{"`true` six times", "`false` twice"} {
		if !strings.Contains(preamble, want) {
			t.Errorf("%s's §4.2 preamble does not say %q: the table it introduces throws six switches `true` and two `false`, and a count stated only under the table is a count the reader who stops at the preamble never sees", telemetryPlan, want)
		}
	}

	// What the four native collection switches are, and that the ad-ID pair is
	// not among them: the two halves the old preamble assigned to wrong groups.
	for _, want := range []string{"four native collection switches", "ad-ID"} {
		if !strings.Contains(preamble, want) {
			t.Errorf("%s's §4.2 preamble does not name %q: the preamble is where a reader learns which switches D195's always-on answer turns on and which stay `false`, and one that does not say it leaves the table to be guessed at", telemetryPlan, want)
		}
	}

	// And the two framings that describe the pre-D195 table. Both were in the
	// preamble as written, and both have an implementer turning the four native
	// collection switches off.
	for _, banned := range []string{"two groups", "switches that decide"} {
		if strings.Contains(preamble, banned) {
			t.Errorf("%s's §4.2 preamble says %q: that is the opt-in table's grouping — \"what\" against \"whether\" — while this table's four native collection switches are `true` under D195's always-on answer, so the preamble as written describes a table §7.4's build-time test refuses", telemetryPlan, banned)
		}
	}
}

// TestTelemetryPlanNamesTheSurfaceAndTheHostItCannotLinkYet pins the three
// corrections a review of round 6 forced, each of them a sentence that was
// true of nothing:
//
//  1. **There is no settings surface.** The plan put the always-on statement in
//     "the app's own settings surface" and check 111 asked a reader to look at
//     it, but `mobile/src/app/root.js` builds a shell out of a title and one
//     body Text and no phase of the mobile plan builds a settings screen. The
//     statement therefore goes in the shell, as a third child, and
//     `mobile/src/app/root.test.js` — which pins the shell's children to
//     exactly `[SHELL.TITLE, SHELL.BODY]` — has to move with it, or P7.2's own
//     `cd mobile && npm test` fails. **The claim, not the sentence, is what is
//     banned** (round 9): §1's tree row made the same claim in different words
//     ("lives in its settings surface instead"), and a ban on two exact
//     sentences passed it. The ban is now on "settings surface" as a phrase,
//     and §7.2's `screen_shown` enum — which said it "is the whole screen set"
//     while offering a value for that screen — is checked against the enum
//     itself, read out of its own row.
//  2. **The link cannot be built yet.** `shared/url`'s `DEFAULT_HOST_ALLOW_LIST`
//     ships empty and D124's answer names no hosts, so `/privacy/` is refused
//     with `allow-list-empty` and renders as text. That is an owner step, and
//     the plan has to name it rather than let a reader find it on a device.
//  3. **The project id is already committed.** §5 said no id of the project is
//     written into this repository; `fugaro-prod` is D91's project and is named
//     in the website's deploy script, its preview workflow and its promote doc.
//
// Each assertion is about a phrase the document has to carry, and each was
// mutated: dropping any one of them fails this test.
func TestTelemetryPlanNamesTheSurfaceAndTheHostItCannotLinkYet(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	flat := telemetryPlain(strings.Join(strings.Fields(doc), " "))

	// 1. The surface: §6 is where the statement is placed, and it is read
	// section-scoped rather than document-wide — the phrase "the app's shell"
	// and the path of the shell's test both appear in more than one place, and
	// an assertion satisfied by §10's task list would stay green while §6, the
	// section a reader lands on, still sent them to a screen that does not
	// exist.
	section6 := telemetryPlain(strings.Join(strings.Fields(planSection(t, doc, "## 6. Consent and opt-out (D195): there is none")), " "))
	for _, want := range []string{
		"The app has no settings screen",
		"SHELL.NOTICE",
		"mobile/src/app/root.test.js",
		// 2. The host list: the refusal, its code, and the owner step it needs.
		"allow-list-empty",
		"fugaro.dev",
	} {
		if !strings.Contains(section6, want) {
			t.Errorf("%s's §6 does not say %q: the app's shell is a title and one body Text, no task in any phase builds a settings screen, and shared/url ships an empty allow-list that D124's answer does not fill — so the statement has to be placed in the shell, the test that pins the shell's children has to move with it, and the owner has to be asked for the host list before the link can exist", telemetryPlan, want)
		}
	}
	if !strings.Contains(planSection(t, doc, "## 6. Consent and opt-out (D195): there is none"), "DEFAULT_HOST_ALLOW_LIST") {
		t.Errorf("%s's §6 does not name DEFAULT_HOST_ALLOW_LIST: it is the export that refuses every host today, and the section that requires a link has to say which empty list refuses it (read from the raw text, since telemetryPlain strips underscores)", telemetryPlan)
	}
	// And the phrasing that assumed a surface that does not exist is gone, in
	// EVERY sentence that used it — not only the two §6 used to carry. The ban is
	// on the phrase "settings surface" as a whole: §1's tree row said the
	// statement "lives in its settings surface instead", which is the same claim
	// in different words, and a ban on two exact sentences passed it.
	if strings.Contains(flat, "settings surface") {
		t.Errorf("%s still says \"settings surface\": no such screen exists in the app or in any task of any phase (§6), so the statement, the link and check 111's walk have to name the surface the shell actually has — and a reader sent to a screen the app does not have is a reader sent nowhere", telemetryPlan)
	}

	// And the screen enum carries no value for the surface that does not exist.
	// §7.2 says the enum "is the whole screen set", so a value for a screen no
	// phase builds is a report that can never be sent — the same claim as the
	// ban above, in the one place where it would be read as a requirement. The
	// parameters CELL is what is checked, not the whole row: the row's own
	// explanation names `'settings'` in order to say it is absent.
	s72 := planSubsection(t, doc, "### 7.2 Sent — the closed allow-list")
	shown := telemetryRowWith(t, s72, "| `screen_shown` |")
	m := telemetryScreenEnumRE.FindStringSubmatch(shown)
	if m == nil {
		t.Fatalf("the §7.2 screen_shown row %q carries no backticked `screen: …` enum: this test cannot check a value list it cannot locate, and the enum is the whole screen set the app reports", shown)
	}
	enum := m[1]
	for _, want := range []string{"'overview'", "'text'"} {
		if !strings.Contains(enum, want) {
			t.Errorf("%s's §7.2 screen_shown enum does not carry %q: the enum is the whole screen set the app reports, and a check that reads it has to see the whole of it", telemetryPlan, want)
		}
	}
	if strings.Contains(enum, "settings") {
		t.Errorf("%s's §7.2 screen_shown enum still offers a value for a settings screen: no phase builds one (§6), so the value names a screen a device can never reach and a report that can never be sent — while the row says the enum is the whole screen set", telemetryPlan)
	}

	// 3. And the task that does the work carries both: P7.2 is where the shell
	// gains its third child and where the link is built, and a requirement that
	// lives only in §6 is a requirement the task's own reader never sees.
	p72 := telemetryPlain(strings.Join(strings.Fields(planSubsection(t, doc, "### Task P7.2:")), " "))
	for _, want := range []string{
		"mobile/src/app/root.test.js",
		"SHELL.NOTICE",
		"allow-list-empty",
	} {
		if !strings.Contains(p72, want) {
			t.Errorf("%s's P7.2 does not name %q: the shell's test pins its children to exactly [SHELL.TITLE, SHELL.BODY] and fails on the statement's first line, and the link is refused until the owner's host list names the host — a task that does not say either ships a broken npm test or a link that cannot open", telemetryPlan, want)
		}
	}

	// 3. §5's step 6: the id is committed already, and the step says which
	// half of the answer is the part that must not be written down.
	section5 := telemetryPlain(strings.Join(strings.Fields(planSection(t, doc, "## 5. The Fugaro-operated project (D194), as owner steps")), " "))
	if strings.Contains(section5, "No id of this project is written into this repository") {
		t.Errorf("%s's §5 still says no id of the project is written into this repository: `fugaro-prod` is D91's project and it is already named in scripts/deploy-website.sh, .github/workflows/website-preview.yml and docs/website/preview-promote.md — the step has to say what is genuinely new (the property id and the two app ids), not that no id is here at all", telemetryPlan)
	}
	if !strings.Contains(section5, "D91") {
		t.Errorf("%s's §5 does not name D91: the project it uses is the website's own, already committed and already documented, and the step that claims nothing of it is written down has to say so", telemetryPlan)
	}

	// 4. The one leftover that described collection as gated, three sections
	// below the row rewritten to say it is not.
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "collects nothing before consent") {
			t.Errorf("%s still says collection waits for consent (%q): D195 is answered always-on, so there is no consent state for a task to verify and no sentence may describe one", telemetryPlan, strings.TrimSpace(line))
		}
	}
}

// telemetryRowWith returns the line of a markdown table that carries the marker.
// Per-row, because that is the unit a reader reads and the unit this file has
// already been caught not checking (the file-table row that could lose its
// numbers while another line still named them): a flag named in prose beside a
// row that says `true` is not a requirement.
func telemetryRowWith(t *testing.T, table, marker string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no table row carries %q: update this test's marker rather than letting the check pass vacuously", marker)
	return ""
}

// TestTelemetryPlanRequiresNoAdvertisingIdentifier pins the addition the
// website's privacy page forced. The page promises that **the app carries no
// advertising identifier**; on its own that is a sentence about a build nobody
// has checked, so the plan has to make it a requirement with something holding
// it: the two Android switches thrown to false, the Android AD_ID permission
// removed from the merged manifest, the iOS Analytics variant that links no
// AdSupport, and a build-time test that fails if any of the three moves. The
// parts that cannot be stated from memory are marked VERIFY with the way to
// verify named, and this test requires that marking rather than a guessed name.
func TestTelemetryPlanRequiresNoAdvertisingIdentifier(t *testing.T) {
	doc := readDoc(t, telemetryPlan)
	s42 := planSubsection(t, doc, "### 4.2 The flags, named exactly")

	// Each switch, in its own row, with the value the row requires.
	for _, tc := range []struct{ flag, want string }{
		{"google_analytics_adid_collection_enabled", "false"},
		{"google_analytics_default_allow_ad_personalization_signals", "false"},
		{"com.google.android.gms.permission.AD_ID", "remove"},
	} {
		row := telemetryRowWith(t, s42, tc.flag)
		if !strings.Contains(row, tc.want) {
			t.Errorf("%s's §4.2 row for %q does not require %q: the row is what an implementer reads, and the advertising-identifier promise needs the switch thrown and the permission gone, not merely named. Row: %s", telemetryPlan, tc.flag, tc.want, strings.TrimSpace(row))
		}
	}
	// Android's is a switch; iOS's is a variant of the SDK that is linked, and
	// the plan says which framework must be absent from the binary. Read per ROW:
	// a VERIFY somewhere else in §4.2 is not this item's VERIFY, which is how the
	// first version of this check passed while the iOS row's marking was removed.
	iosRow := telemetryRowWith(t, s42, "AdSupport")
	for _, want := range []string{"AdSupport", "VERIFY"} {
		if !strings.Contains(iosRow, want) {
			t.Errorf("%s's §4.2 iOS row for the ad-ID-free Analytics variant does not carry %q: on iOS the promise is not a switch but the variant that is linked, and where the option's name cannot be read from a page today the plan says VERIFY. Row: %s", telemetryPlan, want, strings.TrimSpace(iosRow))
		}
	}
	// The way to verify it is named, in §4.2 and in the UNVERIFIED list: the
	// pinned package's own podspec and config plugin, and the generated files.
	s14 := planSection(t, doc, "## 14. UNVERIFIED")
	for _, tc := range []struct{ name, text, want string }{
		{"§4.2", s42, "podspec"},
		{"§4.2", s42, "Podfile.lock"},
		{"§14", s14, "podspec"},
		{"§14", s14, "Podfile.lock"},
		{"§14", s14, "AndroidManifest.xml"},
	} {
		if !strings.Contains(tc.text, tc.want) {
			t.Errorf("%s's %s does not name %q: a VERIFY with no way to verify it is a hope, and this one is named so the implementer knows what to read (the pinned @react-native-firebase package's source and the generated native project)", telemetryPlan, tc.name, tc.want)
		}
	}

	// The build-time test, in the section that lists the tests, naming the
	// generated files it reads and the planted control that proves it can fail.
	s74 := planSubsection(t, doc, "### 7.4 The test that holds it")
	for _, want := range []string{"ad-ID test", "AndroidManifest.xml", "Podfile.lock", "planted"} {
		if !strings.Contains(s74, want) {
			t.Errorf("%s's §7.4 does not name %q: the advertising-identifier promise is the one entry in the ban list held by the BUILD rather than by the app's code, so it needs a test over the generated native config with a control that proves the test can fail", telemetryPlan, want)
		}
	}

	// And the integration task itself has to carry it: §7.4 says whose test it is,
	// and P7.1 is where the switches, the manifest edit and the pod variant are
	// actually done, so the task's own section names all three.
	p71 := planSubsection(t, doc, "### Task P7.1:")
	for _, want := range []string{"ad-ID", "AD_ID", "AdSupport", "false"} {
		if !strings.Contains(p71, want) {
			t.Errorf("%s's P7.1 does not name %q: the advertising-identifier promise is kept in the build the integration task produces, and a requirement that lives only in §4.2's table is a requirement the task's own reader never sees", telemetryPlan, want)
		}
	}

	// The D195 row says what the plan recommended, what the owner answered, and
	// that this one fact is not contingent on which of the two won. The
	// recommendation stays visible: the owner overruled it, and superseding a
	// recommendation is not deleting it — a reader has to be able to see both.
	decisions := planSection(t, doc, "## 3. Owner decisions")
	d195 := telemetryRowWith(t, decisions, "| **D195** |")
	if !strings.Contains(d195, "recommended: opt-in") {
		t.Errorf("%s's D195 row no longer says \"recommended: opt-in\": the owner answered always-on AGAINST that recommendation, so the row has to keep the recommendation visible while it records the answer — a row that drops the recommendation loses the fact that the plan did not propose what was ruled", telemetryPlan)
	}
	if !strings.Contains(d195, "ALWAYS ON") {
		t.Errorf("%s's D195 row does not record the owner's answer of 2026-10-19 (always on): the answer is what §6, P7.2 and every dependent sentence are rewritten to, and it is what turns the recommendation in the same row into history", telemetryPlan)
	}
	if !strings.Contains(d195, "advertising identifier") {
		t.Errorf("%s's D195 row does not say that the advertising identifier is collected under neither option: the consent answer is the owner's, and the ad-ID promise is not contingent on it", telemetryPlan)
	}

	// The two cross-references the promise leans on: the website's own statement
	// (PR #689, whose text this plan lands against) and Play's form.
	if !strings.Contains(readDoc(t, telemetryPlan), "#689") {
		t.Errorf("%s does not name PR #689: the sentence the app's build is now required to keep true is the privacy page's, and that page's text is what #689 carries", telemetryPlan)
	}
	if !strings.Contains(strings.Join(strings.Fields(planSubsection(t, doc, "### 8.1 Play")), " "), "no Advertising ID") {
		t.Errorf("%s's §8.1 does not state that Play's Data safety form carries no Advertising ID type: that answer is true only while the build keeps the id out, and it is the same fact the website states in prose", telemetryPlan)
	}
}

// TestTelemetryPlanAttributesTheWordCeilingsToD188 pins a correction this
// document needed twice over. The website's word ceilings -- a page's lead at
// most 40 words, a page at most 250 -- are **D188's**; **D192** is the budget's
// byte ceilings. P7.4's "Files" line attributed the word ceilings to "D192's
// rule" while the same document's §8.3 said "The page's word ceiling is D188's"
// and `docs/mobile/implementation-plan.md`'s P7.4 said D188. Two numbers that
// both bound a page's length are exactly the pair a reader will not check twice,
// so all three places are read here, one by one.
func TestTelemetryPlanAttributesTheWordCeilingsToD188(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	s83 := planSubsection(t, doc, "### 8.3 ")
	if !strings.Contains(telemetryPlain(s83), "The page's word ceiling is D188's") {
		t.Errorf("%s's §8.3 no longer states that the page's word ceiling is D188's: that sentence is what P7.4's attribution has to agree with", telemetryPlan)
	}

	p74 := planSubsection(t, doc, "### Task P7.4:")
	plain := telemetryPlain(p74)
	if !strings.Contains(plain, "D188's rule") {
		t.Errorf("%s's P7.4 does not attribute the website's word ceilings to D188's rule: the design decision that sets the word ceilings is D188, and the task that moves a page's word count is the task that has to name it", telemetryPlan)
	}
	if strings.Contains(plain, "D192's rule") {
		t.Errorf("%s's P7.4 attributes the word ceilings to D192's rule: D192 is the byte budgets and D188 is the word ceilings, §8.3 of the same document says so, and a task sent to the wrong decision changes the wrong gate", telemetryPlan)
	}

	// The plan this one registers into says D188 too, and the two must not drift.
	ip74 := planSubsection(t, readDoc(t, coordinatorMobilePlan), "### Task P7.4:")
	if !strings.Contains(telemetryPlain(ip74), "D188") {
		t.Errorf("%s's P7.4 does not name D188: the mobile plan and the telemetry plan answer the same question about the same page's gate, and the pin requires both", coordinatorMobilePlan)
	}
	if strings.Contains(telemetryPlain(ip74), "D192") {
		t.Errorf("%s's P7.4 names D192 where the telemetry plan names D188 for the same word ceilings: the two documents register the same task and cannot attribute its gate to two different decisions", coordinatorMobilePlan)
	}
}

// telemetryNativeConfig is the two files the plan says are never committed. They
// are walked from this list rather than from planPaths, and that is the point of
// the list: **the derivation cannot see the second one at all.** planPathRE
// matches it — its class `[A-Za-z0-9_./-]` covers every character in
// `mobile/GoogleService-Info.plist` — but planPaths keeps a candidate only if
// isPlanPath accepts it, and isPlanPath accepts a directory or one of this
// repository's extensions (`.go .js .json .md .tmpl .yml .yaml .ts`). `.plist`
// is not among them, so the iOS file is dropped and never enters planPaths. The
// first version of this pin trusted the derivation and left the iOS file's half
// of the rule untested while claiming to require it: committing the plist left
// the suite green. The cause is asserted below, both halves of it, so the day
// either half changes this list is called out rather than silently doubled.
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
	// the iOS path was left to a derivation that drops it on its extension.
	for _, p := range telemetryNativeConfig {
		if _, err := os.Stat("../../" + p); err == nil {
			t.Errorf("%s is in the tree: %s says both native config files are never committed — they name the Fugaro-operated project and carry its key — and this one has been added anyway", p, telemetryPlan)
		}
		if telemetryTracked(t, p) {
			t.Errorf("%s is tracked by git: %s says both native config files are never committed, and tracking one is the way the rule is broken while the working tree still looks clean", p, telemetryPlan)
		}
	}
	// The blind spot, asserted rather than assumed — and asserted as the two
	// facts it actually is, because the first version of this comment got the
	// cause wrong and a wrong cause is a wrong fix the day the derivation
	// changes. planPathRE does match the plist (its class has A-Z); isPlanPath
	// is what drops it, on the `.plist` extension. If either half moves, this
	// list stops being necessary and the two must be reconciled rather than kept
	// side by side.
	if !planPathRE.MatchString("`mobile/GoogleService-Info.plist`") {
		t.Errorf("planPathRE no longer matches `mobile/GoogleService-Info.plist`: telemetryNativeConfig exists because the regexp DOES match this path and isPlanPath drops it, so the comment above and this test both rest on a cause that has changed — rewrite them against what planPaths does now")
	}
	if isPlanPath("mobile/GoogleService-Info.plist") {
		t.Errorf("isPlanPath now accepts mobile/GoogleService-Info.plist: it used to reject it because `.plist` is not one of the extensions the path derivation walks, which is the whole reason telemetryNativeConfig has to name the iOS file itself — if that has changed, planPaths derives it and the two must be reconciled rather than kept side by side")
	}
	if slices.Contains(planPaths(doc), "mobile/GoogleService-Info.plist") {
		t.Errorf("planPaths now derives mobile/GoogleService-Info.plist from %s: the derivation used to drop it on its `.plist` extension, and if it no longer does, telemetryNativeConfig's reason for existing has gone and the derivation and the list must be reconciled rather than kept side by side", telemetryPlan)
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
	// (and design/push-notifications.md) are registered. **The row is the unit
	// checked, not the document**: these claims used to be searched for over the
	// whole of docs/mobile/README.md, so the telemetry file-table row could lose
	// its block or its check numbers while the numbers table below still named
	// them, and the suite stayed green — a reader who followed the file table
	// would have found no record of what the plan takes.
	index := readDoc(t, "docs/mobile/README.md")
	fileRow := telemetryIndexRow(t, index, "| [../plans/2026-10-19-mobile-telemetry.md]")
	for _, want := range append([]string{"D193–D200"}, telemetryTakenChecks...) {
		if !strings.Contains(fileRow, want) {
			t.Errorf("docs/mobile/README.md's telemetry file-table row does not name %s: the row is what a reader checking this plan lands on, and it is the row that has to record what the plan takes — its decision block and its checks — or a collision is found by the writer of the next plan", want)
		}
	}
	// A reserved number is never reused: the functional spec's row keeps its
	// original words and the telemetry plan's note is dated beside them.
	reservedRow := telemetryIndexRow(t, index, "| **Checks 100–109** |")
	if !strings.Contains(reservedRow, "105–109 reserved and unused") {
		t.Errorf("docs/mobile/README.md's Checks 100–109 row no longer carries \"105–109 reserved and unused\": that phrase is the anchor docs_coordinator_test.go reads the 105–109 block from, and the telemetry plan's dated note must sit beside it in that row rather than replace it")
	}

	// **The two rows agree.** The Check 110 row (the voice design's own reading,
	// "nothing above 109 is claimed") and the Checks 100–109 row (the telemetry
	// plan's numbers) are two rows of one table making claims about one block, and
	// a review of this change found them contradicting each other: the row above
	// record 111–113 as taken while the row below said nothing above 109 was
	// claimed. Both must now say the same thing, and neither may carry the
	// undated claim.
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

	// **110 has one owner in both rows, and it is not either row's design.** The
	// pair disagreed about 110 exactly as they had about 105–109: the Checks
	// 100–109 row called it "the always-on orchestrator's" while the Check 110
	// row's own dated note called it "this design's" — i.e. the voice design's,
	// which is the row's subject and is not where the claim lives. The claim is
	// PR #632's, which is what docs/coordinator/README.md's "Check 110" row
	// records and what the plan and the mobile plan both say. Both rows must name
	// the orchestrator's claim, and neither may attribute 110 to itself.
	for _, row := range []struct{ name, text string }{
		{"the Checks 100–109 row", reservedRow},
		{"the Check 110 row", takenRow},
	} {
		if !strings.Contains(row.text, "always-on orchestrator") {
			t.Errorf("docs/mobile/README.md's %s does not name 110 as the always-on orchestrator design's claim: 110 is PR #632's claim (docs/coordinator/README.md's \"Check 110\" row) and the two rows of this table have to agree about whose it is, not only about 111–113", row.name)
		}
		if strings.Contains(row.text, "this design's") {
			t.Errorf("docs/mobile/README.md's %s attributes a number above 109 to \"this design\" (the row's own document): the claimants are the always-on orchestrator design (110) and the telemetry plan (111–113), and a row claiming a number for its own subject is how the same number gets two owners", row.name)
		}
	}
	// The other two documents that record the number say the same thing, and this
	// is the assertion that keeps the three from drifting apart again.
	for _, tc := range []struct{ path, want string }{
		{telemetryPlan, "110 is claimed"},
		{"docs/mobile/implementation-plan.md", "110 is claimed by the always-on orchestrator design"},
	} {
		if !strings.Contains(telemetryPlain(readDoc(t, tc.path)), telemetryPlain(tc.want)) {
			t.Errorf("%s does not carry %q: the telemetry plan takes 111–113 because 110 is claimed by another open work, and that reason has to be in the plan and in the plan it registers into", tc.path, tc.want)
		}
	}

	// **The coordinator's table says the same thing.** It is the one table in this
	// repository that lists every work's block and every work's checks, and the
	// next writer reads the free block out of it — so a cell still saying the
	// telemetry plan took 105, 106 and 107 is how the reserved block gets taken a
	// second time. This assertion is here because the drift was real: with the
	// cell put back to its old words, the whole package stayed green.
	coordinatorCell := telemetryIndexRow(t, readDoc(t, telemetryCoordinator), "| **Checks 1–109** |")
	if !strings.Contains(coordinatorCell, "111, 112 and 113") {
		t.Errorf("docs/coordinator/README.md's checks row does not name 111, 112 and 113: %s takes them and this table is where the next writer reads what is free, so a cell that does not record them is a cell that invites 105 to be taken again", telemetryPlan)
	}
	if strings.Contains(coordinatorCell, "105, 106 and 107") {
		t.Errorf("docs/coordinator/README.md's checks row still records 105, 106 and 107 as taken by the telemetry plan: 105–109 are reserved and unused, the plan takes none of them, and this table is what the next writer reads")
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
// `mobile/GoogleService-Info.plist` is not in it: planPathRE matches it, but
// isPlanPath drops it on the `.plist` extension, so it never reaches planPaths
// and an entry here would fail the converse walk below. The two native config files are asserted
// one by one in TestTelemetryPlanKeepsTheNativeFilesOutOfGit instead — and
// `mobile/google-services.json` is listed here as well as there, because it IS a
// path the derivation sees and the excuse has to be given somewhere.
//
// `mobile/app.config.js` and `mobile/eas.json` used to be here, excused as PR
// #675's files held off main. #675 merged, so both are in the tree and the
// excuse became false — the first entry to rot exactly as the paragraph above
// warns. They are gone from the map, which is what makes the derivation check
// them like any other cited path, and TestTelemetryPlanModifiesTheFilesPR675Merged
// holds the other half of that correction: the plan must say they are on main
// and that P7.1 MODIFIES them, not create them.
var telemetryWillCreate = map[string]string{
	"mobile/firebase.json":                         "P7.1 (the react-native auto-collection switches and the two ad-ID switches, §4.2)",
	"mobile/google-services.json":                  "never — §4.3: delivered by an EAS file secret and kept out of git, so its absence here is the rule (checked beside `mobile/GoogleService-Info.plist` in TestTelemetryPlanKeepsTheNativeFilesOutOfGit)",
	"mobile/src/telemetry/consent.js":              "never — D195's answer is always-on, so there is no consent gate and no stored choice; §6 names it as a path the opt-in recommendation would have created and the answer takes out of the plan",
	"mobile/src/telemetry/notice.js":               "P7.2 (the always-on statement the shell renders as SHELL.NOTICE — D195's answer, in place of the sheet; there is no settings screen for it to live in)",
	"mobile/src/telemetry/notice.test.js":          "P7.2 (the statement's text, the link, and the absence of a switch and of any runtime collection call)",
	"mobile/src/telemetry/analytics-calls.test.js": "P7.3 (the scan of §4.2's rules with its planted positive controls)",
	"mobile/src/telemetry/events.test.js":          "P7.3 (the never-send test, over a fixture account, mailbox and token)",
	"shared/telemetry-consent":                     "never — D195's answer is always-on, so there is no choice machine and no platform flag table to put in `shared/`; §6 names it as a path the opt-in recommendation would have created and the answer takes out of the plan",
	"mobile/src/telemetry":                         "P7.2 (notice.js and notice.test.js) and P7.3 (the event module, the enum sets and the breadcrumbs)",
}

// TestTelemetryPlanModifiesTheFilesPR675Merged pins the correction #675's merge
// forced. The plan was written while `mobile/app.config.js` and `mobile/eas.json`
// were PR #675's and off main; it said so in eight places, and every one of them
// became false the moment #675 merged. This test holds all three halves:
// both files are in the tree, the plan says they are on main, and the plan's task
// P7.1 names them as files it MODIFIES. It also bans the stale sentences, because
// a plan that says "not on `main`" about a file the tree has sends its next
// implementer to create a file that already exists.
func TestTelemetryPlanModifiesTheFilesPR675Merged(t *testing.T) {
	for _, p := range []string{"mobile/app.config.js", "mobile/eas.json"} {
		if _, err := os.Stat("../../" + p); err != nil {
			t.Fatalf("%s is not in the tree: PR #675 merged both store-path files, and the plan's sentences are written against that fact", p)
		}
	}
	doc := telemetryPlain(readDoc(t, telemetryPlan))
	for _, want := range []string{
		"`mobile/app.config.js` and `mobile/eas.json` are on `main`",
		"`mobile/app.config.js` (**modified**",
		"`mobile/eas.json` (**modified**",
	} {
		if !strings.Contains(doc, telemetryPlain(want)) {
			t.Errorf("%s does not say %q: PR #675 merged both files, so the plan must name them as existing files it modifies rather than files it waits on", telemetryPlan, want)
		}
	}
	for _, stale := range []string{
		"no `mobile/app.config.js`",
		"the file arrives from PR #675",
		"and PR #675 (for",
		"PR #675's `app.config.js`",
		"depends on #675 merging first",
	} {
		if strings.Contains(doc, telemetryPlain(stale)) {
			t.Errorf("%s still carries %q: that reading was true while PR #675 was open and is false now that it has merged", telemetryPlan, stale)
		}
	}
	if n := strings.Count(doc, "not on `main`"); n != 0 {
		t.Errorf("%s says \"not on `main`\" %d time(s): every such sentence was about PR #675's two files, and #675 is merged", telemetryPlan, n)
	}
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

// telemetryLineCiteRE matches a citation of the form `path/to/file.ext:123` (or
// `:123-145`). The plan states its own convention in §1 — a file it does not own
// is cited by symbol or by phrase, never by line — and the round-5 review found
// the convention stated and then broken eight times over, against a merge base
// the document had also mis-declared. A line number into a document other work
// edits is stale the moment that work lands and nothing about the sentence it
// was written for looks wrong, which is why the convention exists.
var telemetryLineCiteRE = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:go|js|mjs|ts|json|md|yaml|yml|astro|tmpl):[0-9]+(?:-[0-9]+)?`)

// telemetryBaseRE is the commit §1 declares this branch's base to be, written as
// a bare hex SHA between bold markers. The base is the premise every "read on
// this branch" fact rests on, so the document has to name one.
var telemetryBaseRE = regexp.MustCompile(`\*\*` + "`" + `([0-9a-f]{7,40})` + "`" + `\*\*`)

func TestTelemetryPlanCitesBySymbolAndDeclaresItsBase(t *testing.T) {
	doc := readDoc(t, telemetryPlan)

	// The convention, stated — and then kept, everywhere in the document.
	section := planSection(t, doc, "## 1. What is in the tree today, and what this plan therefore builds on")
	for _, want := range []string{"merge base", "cited by symbol"} {
		if !strings.Contains(telemetryPlain(section), want) {
			t.Errorf("%s's §1 does not state %q: the base it read its facts against and the citation convention those facts are written in both have to be on the page, or a reader cannot tell a stale fact from a current one", telemetryPlan, want)
		}
	}
	if cites := telemetryLineCiteRE.FindAllString(doc, -1); len(cites) != 0 {
		t.Errorf("%s cites %v by line: §1 states that a file this plan does not own is cited by symbol or by phrase, and every file it cites is one it does not own — a line number into a document another change edits goes stale silently, so replace the number with the symbol, the heading or the phrase it points at", telemetryPlan, cites)
	}

	// The base, checked against the repository rather than trusted: it has to be
	// a commit this repository actually has, and one this branch is built on.
	m := telemetryBaseRE.FindStringSubmatch(section)
	if m == nil {
		t.Fatalf("%s's §1 names no base commit: the facts in the table below are facts about a tree, and which tree is the one thing a reader cannot recover from the text", telemetryPlan)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Logf("git is not on PATH, so the base %s is unchecked here (it is still required to be named): %v", m[1], err)
		return
	}
	if err := exec.Command("git", "-C", "../..", "cat-file", "-e", m[1]+"^{commit}").Run(); err != nil {
		t.Logf("the base %s is not in this clone (a shallow or exported tree), so its ancestry is unchecked here: %v", m[1], err)
		return
	}
	if err := exec.Command("git", "-C", "../..", "merge-base", "--is-ancestor", m[1], "HEAD").Run(); err != nil {
		t.Errorf("%s's §1 declares its base to be %s, which is not an ancestor of this branch's HEAD: the base is where the document's facts were read, so a base this branch is not built on makes every \"read on this branch\" sentence in §1 a claim about a tree nobody is looking at", telemetryPlan, m[1])
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
		// And none of the numbers the plan takes is written, which is what makes
		// "the first free number is 111" true against this register. NOT a
		// ceiling on the register: the numbers above the block belong to other
		// claims (110 is the orchestrator's, and 105–109 are reserved for their
		// holder to write whenever they like), so a `highest <= 104` assertion
		// would fail on a change that says nothing about this plan — and §2.2
		// states the reading as a reading precisely so that it can move.
		for _, n := range telemetryTakenChecks {
			if strings.Contains(tc.text, "## Check "+n) {
				t.Errorf("%s states its block is the first free one, and %s writes `## Check %s`: the claim is false against this register, and the numbers the plan takes are the ones whose written presence falsifies it", telemetryPlan, tc.name, n)
			}
		}
	}
}
