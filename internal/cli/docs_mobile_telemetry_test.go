package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The pin for docs/plans/2026-10-19-mobile-telemetry.md, the plan for Firebase
// Crashlytics and Firebase Analytics in the Expo app. It is the rebuilt
// replacement for the draft on PR #693, and what it had to change is what this
// pin is mostly about: the numbers.
//
// The draft asked for D193-D200 and checks 111-113. By the time it was rebuilt
// on main, D193-D197 had been answered by the coordinator (2026-10-21) and the
// register carried written ## Check 111 (the setup wizard's), ## Check 112 and
// ## Check 113 (the website zoom-diagrams plan's). So the plan takes D253-D260
// and checks 120, 121 and 122 instead, and the one property worth pinning is
// that the numbers it takes are the numbers that were free when it took them.
//
// That property cannot be held by a list of numbers written here: a list is a
// copy of the plan's own claim, and it stays green after another work writes a
// decision into the same block. It is held instead by reading the block out of
// every document under docs/ and refusing a number in the plan's range that a
// document other than the plan and its two registrations wrote - which is the
// reading the plan itself performed, and the reason its block moved.
//
// What this pin deliberately does not assert: anything about an app. No task of
// Phase 7 is built, so there is no behaviour to hold. The claims here are about
// documents, about two files that must not be in the tree, and about the app's
// dependency list still not naming the SDKs.
const (
	telemetryPlan         = "docs/plans/2026-10-19-mobile-telemetry.md"
	telemetryMobileReadme = "docs/mobile/README.md"
	telemetryImplPlan     = "docs/mobile/implementation-plan.md"
	telemetryRegister     = "docs/gcp-live-checklist.md"
)

// telemetryDecisionFirst and telemetryDecisionLast are the block the plan
// takes. They are the reading of 2026-10-22, and the walk below is what keeps
// them honest: the numbers are checked against every other document rather
// than trusted from this file.
const (
	telemetryDecisionFirst = 253
	telemetryDecisionLast  = 260
)

// telemetryEditedTableRE reads a row of the plan's section 13 "Edited" table,
// keeping the first cell: the path the change edits. The rest of the row is
// prose and names other documents, which is why only the first cell is read.
var telemetryEditedTableRE = regexp.MustCompile(`(?m)^\| ` + "`" + `(docs/[A-Za-z0-9_./-]+)` + "`" + ` \|`)

// telemetryCiteable is the set of documents allowed to name a number in the
// plan's block: the plan itself, and the documents its own section 13 says this
// change edits. It is derived from that table rather than listed here, so that
// a document the plan forgets to declare is a failure rather than a silence -
// and so that the set cannot quietly grow past what the plan admits to
// touching.
//
// The distinction the walk makes is between *taking* a number and *citing* one.
// The four documents that carry D111's amendment cite D259 - the row that
// amended the list they quote - and citing a row is what a dated note does.
// Taking one is what a second work in the same block does, and that is what
// the walk refuses.
func telemetryCiteable(t *testing.T, plan string) map[string]bool {
	t.Helper()
	i := strings.Index(plan, "**Edited:**")
	if i < 0 {
		t.Fatalf("%s has no **Edited:** table; update this test's markers", telemetryPlan)
	}
	j := strings.Index(plan[i:], "**Not touched by this change:**")
	if j < 0 {
		t.Fatalf("%s's edited table is not followed by its \"not touched\" paragraph; update this test's markers", telemetryPlan)
	}
	rows := telemetryEditedTableRE.FindAllStringSubmatch(plan[i:i+j], -1)
	if len(rows) < 6 {
		t.Fatalf("%s's edited table names %d documents; it named six when this pin was written, so the table has moved rather than shrunk", telemetryPlan, len(rows))
	}
	out := map[string]bool{telemetryPlan: true}
	for _, row := range rows {
		out[row[1]] = true
	}
	return out
}

// telemetryDNumberRE reads a decision number as these documents write it:
// `D253`, and `D253-D260` as the two numbers it is. The three-digit form with a
// boundary on both sides is what keeps `D113b` and `D113c` out of the walk -
// they are the mobile work's sub-numbers, and a pattern that read `D113` out of
// `D113b` would put the walk's count three rows out for no reason.
var telemetryDNumberRE = regexp.MustCompile(`\bD([0-9]{3})\b`)

// telemetryDocs walks docs/ and returns every markdown file in it, as a
// repository-relative path. The walk is the assertion's control as well as its
// input: a walk that read nothing would pass the block check silently, so the
// caller asserts the plan and the register are in the result.
func telemetryDocs(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("../../docs", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, strings.TrimPrefix(filepath.ToSlash(path), "../../"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// TestTelemetryPlanTakesTheNumbersThatWereFree is the headline pin: the block
// the plan takes is free, the checks it takes are unwritten, and the document
// says so where a reader looks for it.
//
// The block half is a walk rather than a list, and it is the half that would
// have caught the draft this replaces. Mutations (each run and restored):
// change telemetryDecisionFirst to 252 - the block then covers D252, which the
// website zoom-diagrams work wrote, and the walk fails on
// docs/website/zoom-diagrams/reconciliation.md; delete the
// `docs/mobile/design/push-notifications.md` row from the plan's own section 13
// table - the derivation of the citeable set loses a document, and the guard on
// the table's size fails before the walk can pass on a set that is one document
// too small. Both were verified by hand.
//
// The second mutation is the one that matters for the walk's honesty: it is
// what proves the allowed set is read out of the plan rather than copied into
// this file, because a copy would not have moved.
func TestTelemetryPlanTakesTheNumbersThatWereFree(t *testing.T) {
	plan := readDoc(t, telemetryPlan)

	for _, want := range []string{
		"### 2.1 Decisions: **D253–D260**, and why those",
		"**D253–D260 is the next free block, and this document is where it is taken.**",
		"**D252 is the highest number written anywhere in `docs/`**",
		"### 2.2 Checks: **120, 121 and 122**, and why those",
		"**So the first free number is 120**",
		"**This plan takes 120, 121 and 122**",
		"## 12. Checks this plan takes (120, 121, 122)",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not say %q: the block and the checks it takes are the claim this pin exists for", telemetryPlan, want)
		}
	}

	// The register half: the numbers the plan takes are not written yet. A
	// written `## Check 120` section would mean P7.5 had run, and a `## Check
	// 111` section that had gone missing would mean the reading the plan took
	// its block after had moved.
	register := readDoc(t, telemetryRegister)
	for _, section := range []string{"## Check 111", "## Check 112", "## Check 113"} {
		if !strings.Contains(register, section) {
			t.Errorf("%s has no %q section: the telemetry plan read the register as carrying 111-113 and took 120-122 after them, so this reading is what chose the block", telemetryRegister, section)
		}
	}
	for _, section := range []string{"## Check 120", "## Check 121", "## Check 122"} {
		if strings.Contains(register, section) {
			t.Errorf("%s carries a %q section: checks 120-122 are written by P7.5, after the phase is built, and a written section means this plan's own text and the register disagree", telemetryRegister, section)
		}
	}

	files := telemetryDocs(t)
	for _, control := range []string{telemetryPlan, telemetryRegister, telemetryMobileReadme} {
		if !slices.Contains(files, control) {
			t.Fatalf("the walk over docs/ did not read %s; the scan is not the tree", control)
		}
	}
	citeable := telemetryCiteable(t, plan)
	for _, file := range files {
		if citeable[file] {
			continue
		}
		for _, m := range telemetryDNumberRE.FindAllStringSubmatch(readDoc(t, file), -1) {
			var n int
			if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
				t.Fatal(err)
			}
			if n >= telemetryDecisionFirst && n <= telemetryDecisionLast {
				t.Errorf("%s names D%d, which is inside the block %s takes (D%d–D%d): the block was read as free, and a document outside the plan and the files its section 13 declares is a second work in the same block - the collision this plan moved its numbers to avoid", file, n, telemetryPlan, telemetryDecisionFirst, telemetryDecisionLast)
			}
		}
	}
}

// TestTelemetryPlanIsRegisteredInTheMobileDocuments: the plan is indexed where
// this work's numbers are read from, and both registrations name the same
// block. The numbers are what a reader checks a plan against, so a plan
// registered in one document and not the other is a plan whose block is read
// from two places.
func TestTelemetryPlanIsRegisteredInTheMobileDocuments(t *testing.T) {
	readme := readDoc(t, telemetryMobileReadme)
	impl := readDoc(t, telemetryImplPlan)

	for _, want := range []string{
		"| [../plans/2026-10-19-mobile-telemetry.md](../plans/2026-10-19-mobile-telemetry.md) |",
		"| **D253–D260** | **the telemetry plan**",
		"| **Checks 120–122** | **the telemetry plan**",
		"**Status (2026-10-22): the telemetry plan is merged and the owner has passed its gate.**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not carry %q: the plan is registered in this file's own numbers table", telemetryMobileReadme, want)
		}
	}

	for _, want := range []string{
		"| **7 — APPROVED 2026-10-22 (telemetry, on merge of its plan)** |",
		"## Phase 7 — APPROVED by the owner on 2026-10-22 (telemetry, on merge of its plan)",
		"**D253–D260** — the next free decision block, read from the tree",
		"**Checks 120, 121 and 122** — the first free numbers after every claim",
	} {
		if !strings.Contains(impl, want) {
			t.Errorf("%s does not carry %q: this is Phase 7's registration", telemetryImplPlan, want)
		}
	}
	for n := 1; n <= 5; n++ {
		row := fmt.Sprintf("| P7.%d *(approved 2026-10-22)*", n)
		if !strings.Contains(impl, row) {
			t.Errorf("%s has no %q: P7.%d's dependency row does not record the approval, so a reader checking that one task is told the phase is unapproved", telemetryImplPlan, row, n)
		}
	}
}

// TestTelemetryPlanGateIsPassedAndTheTwoDocumentsAgree: the owner passed the
// gate on 2026-10-22, on the condition that the plan is merged first, and both
// documents carry that date. The date is stated twice - once in the plan, once
// in the phase it registers - and a gate passed in one document and not the
// other is the failure this pins: a reader who opens only the mobile plan
// cannot start, and a reader who opens only the plan cannot tell whether they
// may.
//
// The plan's own "PROPOSED, not approved" text is gone with it, and the ban is
// checked where it was written rather than by a phrase this test invents: the
// mobile plan's Phase 6 pin already refuses `NOT APPROVED` anywhere in that
// document, and this test refuses the phase heading this change replaced.
func TestTelemetryPlanGateIsPassedAndTheTwoDocumentsAgree(t *testing.T) {
	plan := readDoc(t, telemetryPlan)
	impl := readDoc(t, telemetryImplPlan)

	for _, want := range []string{
		"**The owner passed the gate on 2026-10-22, and the tasks may start once this plan is merged.**",
		"**Status: a plan, and the owner has passed the approval gate for it.**",
		"Phase 7 heading\nnow reads **APPROVED 2026-10-22 (on merge of this plan)**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not say %q: the gate's date and its condition are the plan's status", telemetryPlan, want)
		}
	}

	for _, gone := range []string{
		"## Phase 7 — PROPOSED 2026-10-19",
		"Phase 7 is still **PROPOSED, not approved**",
	} {
		if strings.Contains(impl, gone) {
			t.Errorf("%s still carries %q: the owner approved Phase 7 on 2026-10-22, on merge of the plan", telemetryImplPlan, gone)
		}
	}
	if !strings.Contains(impl, "**7b. The telemetry plan's block (D253–D260), and the phase it registers — added 2026-10-19, PASSED 2026-10-22, for Phase 7 only.**") {
		t.Errorf("%s's approval gate has no step 7b recording the date the telemetry phase was passed", telemetryImplPlan)
	}
	if !strings.Contains(impl, "On that condition **Phase 7 is APPROVED — tasks P7.1–P7.5 may start**") {
		t.Errorf("%s's step 7b does not state the condition the approval carries (on merge of the plan)", telemetryImplPlan)
	}
}

// TestTelemetryPlanNeverCommitsTheNativeConfigFiles: the two files that name
// the Firebase project are never *committed*, and the plan says how a build
// gets them instead. This is the one claim in the plan that is about a file the
// repository must not hold, so it is checked against git rather than by
// searching the plan for the sentence - the sentence is the plan's, and the
// committed set is the tree's.
//
// The rule is the plan's own (§4.3, part 2): P7.1's JS test "fails if the file
// is tracked by git, and fails if it is not ignored". This test holds the
// tracked half, because that is the half this repository can hold today: the
// ignore half is P7.1's to add (`mobile/.gitignore` carries no entry for either
// path yet, and the plan says so). Reading mere *existence* instead - which
// this test did first - is stricter than the rule and false-fails the workflow
// the plan mandates: D260 and P7.1's `npx expo prebuild` writes a local
// `mobile/android/app/google-services.json` from the EAS file secret, and a
// developer running the development build has one on disk. A local copy is not
// a commit, and the plan's rule is about commits.
//
// Mutation (run and restored): `git add -f mobile/google-services.json` on an
// empty file -> fails on the listing, and the walk over the working tree that
// this test used to do would NOT have distinguished that from the legitimate
// untracked copy. `node_modules` is not special-cased any more, because the
// listing is git's and a vendored copy is not tracked either.
func TestTelemetryPlanNeverCommitsTheNativeConfigFiles(t *testing.T) {
	plan := readDoc(t, telemetryPlan)
	for _, want := range []string{
		"`google-services.json`", "`GoogleService-Info.plist`",
		"**never committed**", "**EAS file secrets**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not say %q: the two native config files are never committed and reach a build as EAS file secrets", telemetryPlan, want)
		}
	}

	names := []string{"google-services.json", "GoogleService-Info.plist"}
	out, err := exec.Command("git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v; this test reads the committed set, so it cannot run without it", err)
	}
	tracked := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(tracked) < 100 {
		t.Fatalf("git ls-files listed %d files; the repository has thousands, so the listing is not the tree and this test would pass on anything", len(tracked))
	}
	for _, path := range tracked {
		if slices.Contains(names, filepath.Base(path)) {
			t.Errorf("%s is committed at %s: the plan requires both native config files to be delivered as EAS file secrets and never committed, because they name the Firebase project", filepath.Base(path), path)
		}
	}
}

// TestTelemetryPlanLinksResolve: every relative link in the documents this
// change edits resolves from the document's own directory. This repository's
// convention is to resolve the target on disk rather than to look for the
// path as a substring - docs_mobile_push_test.go does exactly this for the
// P5.2 entry, and for exactly this reason: a link whose text is right and
// whose target is wrong reads as correct and is not.
//
// It is here because this change got five of them wrong, in the four documents
// below and in no others: `docs/mobile/README.md` and
// `docs/mobile/implementation-plan.md` pointed at `../voice-session/`, which
// lands on the nonexistent `docs/voice-session/` (the file is at
// `docs/mobile/voice-session/`), the plan pointed at `voice-session/` from
// `docs/plans/`, and `docs/mobile/design/voice-evaluation-switch.md` pointed at
// `../plans/`, which lands on `docs/mobile/plans/`. The substring checks above
// cannot see any of that, because the link's *text* is the right path in all
// five.
//
// Mutation (run and restored): point one of the plan's links back at
// `voice-session/reconciliation.md` -> fails with "does not resolve from
// docs/plans/", which is the failure the five real links would have raised.
func TestTelemetryPlanLinksResolve(t *testing.T) {
	for _, doc := range []struct{ path, dir string }{
		{telemetryPlan, "docs/plans"},
		{telemetryMobileReadme, "docs/mobile"},
		{telemetryImplPlan, "docs/mobile"},
		{"docs/mobile/design/voice-evaluation-switch.md", "docs/mobile/design"},
	} {
		targets := markdownLinkTargets(readDoc(t, doc.path))
		if len(targets) == 0 {
			t.Errorf("%s carries no markdown link at all; the documents this change edits cite each other", doc.path)
			continue
		}
		for _, target := range targets {
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
				continue
			}
			if _, err := os.Stat(filepath.Join("../../", doc.dir, target)); err != nil {
				t.Errorf("%s links %q, which does not resolve from %s/: a reader who follows it lands on nothing", doc.path, target, doc.dir)
			}
		}
	}
}

// TestTelemetryPlanNamesTheFlagsItDependsOn: the plan states the exact native
// switch names, and the app's dependency list still does not name the SDKs.
// Both halves are the plan's "nothing here is built" claim, from the two sides
// a reader can check: the names an implementer has to write down, and the
// dependency that would have to exist for any of them to matter.
func TestTelemetryPlanNamesTheFlagsItDependsOn(t *testing.T) {
	plan := readDoc(t, telemetryPlan)
	for _, want := range []string{
		"`firebase_analytics_collection_enabled`",
		"`FIREBASE_ANALYTICS_COLLECTION_ENABLED`",
		"`firebase_crashlytics_collection_enabled`",
		"`FirebaseCrashlyticsCollectionEnabled`",
		"`google_analytics_adid_collection_enabled`",
		"`google_analytics_default_allow_ad_personalization_signals`",
		"`com.google.android.gms.permission.AD_ID`",
		"`analytics_auto_collection_enabled`",
		"`crashlytics_auto_collection_enabled`",
		"`setAnalyticsCollectionEnabled(…)`",
		"`setCrashlyticsCollectionEnabled(…)`",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not name %q: section 4.2 is the list of names the plan commits to, and P7.1 confirms each against the SDK's own documentation", telemetryPlan, want)
		}
	}

	pkg := readDoc(t, "mobile/package.json")
	if strings.Contains(pkg, "@react-native-firebase") {
		t.Errorf("mobile/package.json names @react-native-firebase: the plan is a plan and P7.1 is unbuilt, so the dependency is not the app's yet")
	}
	if _, err := os.Stat("../../mobile/src/telemetry"); err == nil {
		t.Errorf("mobile/src/telemetry exists: the plan's telemetry module is P7.2's and P7.3's, and neither is built")
	}
}

// TestTelemetryPlanRecordsTheSecretItemsDebtAsStillOpen: the plan says D259's
// D259's four-item list was superseded by the owner's D232 ruling, because
// `SECRET_ITEMS` is an exhaustive allow-list of D111's two items that no task of
// Phase 7 widens (§4.4, §11). That is a claim about the tree, and it is the kind
// that goes quietly wrong: the day someone widens the list, the plan's own
// paragraph becomes false, and the two documents it points at
// (`secret.test.js`'s "knows the list and nothing else" and
// `website/test/privacy.mjs`'s `idToken,macKey` assertion) would be the only
// places that noticed.
//
// So the debt is pinned from both ends: the fence is still shut, and the plan
// still says who owes opening it. **When the widening lands, this test fails and
// the plan's §4.4 and §11 must be rewritten in the same change** - which is the
// point, because that change is the one that also has to move the privacy page's
// paragraph. Nothing here asserts the app's behaviour: the two files are read as
// text, and the plan is read for the two sentences that state the debt.
func TestTelemetryPlanRecordsTheSecretItemsDebtAsStillOpen(t *testing.T) {
	secret := readDoc(t, "mobile/src/accounts/secret.js")
	if !strings.Contains(secret, "Object.freeze(['idToken', 'macKey'])") {
		t.Errorf("mobile/src/accounts/secret.js no longer freezes SECRET_ITEMS to ['idToken', 'macKey']: the plan's §4.4 and §11 say D232 stands and the list is not widened - if a widening has landed, both sections are now wrong and must be rewritten with it")
	}
	store := readDoc(t, "mobile/src/accounts/secure-store.js")
	if !strings.Contains(store, "UNKNOWN_ITEM") {
		t.Errorf("mobile/src/accounts/secure-store.js no longer refuses an unknown secret item: the plan's §4.4 names that refusal as half of the fence D232's standing keeps shut")
	}
	privacy := readDoc(t, "website/test/privacy.mjs")
	if !strings.Contains(privacy, "'idToken,macKey'") {
		t.Errorf("website/test/privacy.mjs no longer asserts SECRET_ITEMS is exactly idToken,macKey: the plan's §4.4 names that assertion as the third place the two-item list is written down, and as the one the widening owes a change to alongside the page")
	}

	// The plan's paragraphs are wrapped, so both sides are flattened: the claim
	// is about the sentence, not about where the line broke.
	plan := flattenDoc(readDoc(t, telemetryPlan))
	for _, want := range []string{
		"**The owner ruled on 2026-10-29 that D232 stands, so `SECRET_ITEMS` is not widened**",
		"**Where the pairing-delivered configuration is stored is task P6.8's choice, and no Phase 7 task writes a secure-storage item.**",
		"**The code allows only the two `SECRET_ITEMS` (`idToken`, `macKey`), and `website/test/privacy.mjs` pins exactly that.**",
	} {
		if !strings.Contains(plan, flattenDoc(want)) {
			t.Errorf("%s does not say %q: the owner's D232 ruling keeps D111 at two items and the fence that keeps it so must be named where the ruling is recorded", telemetryPlan, want)
		}
	}
}
