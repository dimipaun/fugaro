package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tests in this file are the fix round of 2026-10-19 on checks 81, 82 and
// 89 (PR #580). Each one holds a claim of the checklist to the thing the claim
// is about -- the generated rules, the code that plans the deploy, the files
// that exist in this checkout -- rather than to a copy of the claim, because
// every defect this round found was a sentence that had been true of an older
// tree: a read the rules never grant, a confirmation the code does take, a
// fingerprint the document cannot contain, an app the guard said was not here.

// TestDocsCheck81ReadColumnsAreWhatTheRulesGrant holds check 81 step 6's two
// columns to the deployed document itself: every path in the granted column
// must carry a read grant at that node or above it in
// internal/budget/rules/testdata/rules.golden.json, and no path in the refused
// column may. The rule the columns state is RTDB's: a read grant sits at the
// location it is written on and cascades **down** only, so `spend/$day/global`
// is readable and `spend/$day` is not.
//
// The review of PR #580 found the granted column listing `spend/$DAY`, which
// has no grant anywhere above it: an operator following it would record a
// failure of a correct deployment. This test reads the columns out of the
// checklist and walks the golden rules with them, so the same mistake cannot
// come back in either direction -- a granted path the rules refuse, or a
// refused path the rules grant.
func TestDocsCheck81ReadColumnsAreWhatTheRulesGrant(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	step := stepText(t, section,
		"6. **The granted read nodes", "7. **A token whose")

	loops := forInLoops(step)
	if len(loops) != 2 {
		t.Fatalf("check 81 step 6 has %d `for p in` loops, want the granted column and the refused column: these are the paths the test walks the golden rules with, so it cannot guess which is which", len(loops))
	}
	granted, denied := loops[0], loops[1]
	if len(granted) == 0 || len(denied) == 0 {
		t.Fatalf("check 81 step 6's columns are %d and %d paths long; a column that empty is a loop the test read wrongly", len(granted), len(denied))
	}
	if len(granted) != 13 || len(denied) != 21 {
		t.Errorf("check 81 step 6 lists %d granted and %d refused paths, and its FACT line says \"The first thirteen are **200**\" and \"the second twenty-one are **401**\": a list and its count move together", len(granted), len(denied))
	}
	for _, want := range []string{"The first thirteen are **200**", "the second twenty-one are **401**"} {
		if !strings.Contains(step, want) {
			t.Errorf("check 81 step 6 does not carry %q: the FACT line is what the owner pastes back, so the counts in it are part of the columns", want)
		}
	}

	root := goldenRules(t)
	for _, p := range granted {
		if !readableInGolden(root, p) {
			t.Errorf("check 81 step 6 lists %s in the granted (200) column, and the golden rules grant no read at it or above it: an RTDB read grant is on the location it is written at and cascades down only, so this read answers 401 -- move it to the refused column (this is the finding of the PR #580 review: spend/$DAY was granted there, and the grant is at spend/$day/global, /repos/$slug and /runs/$slug/$run)", p)
		}
	}
	for _, p := range denied {
		if readableInGolden(root, p) {
			t.Errorf("check 81 step 6 lists %s in the refused (401) column, and the golden rules grant a read at it or above it: the check would record a failure of a deployment that is doing exactly what D112 says", p)
		}
	}
}

// TestDocsCheck81FingerprintsMatchTheGoldenRules: check 81 step 8 hands the
// owner three `grep -o` patterns and a count for each, over the document read
// back from the database. A pattern the generated document can never contain,
// or a count that is not the document's, is a FACT line that reads as a failed
// deployment on a perfect one: the review of PR #580 found
// `grep -o '"mu != null"'`, which matches nothing because the rules render
// `auth.token.mu != null` with no quote before `mu`.
//
// The patterns are read out of the checklist and matched against the golden
// document here, so both halves of each line -- the pattern and its number --
// are the generated rules' own.
func TestDocsCheck81FingerprintsMatchTheGoldenRules(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	step := stepText(t, section,
		"8. **The fence is the deployed document's", "\n\n**Passes when:**")

	golden, err := os.ReadFile("../budget/rules/testdata/rules.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`grep -o '([^']+)' [^\n]*# (\d+):`)
	found := re.FindAllStringSubmatch(step, -1)
	if len(found) != 3 {
		t.Fatalf("check 81 step 8 carries %d `grep -o '<pattern>' ... # <n>:` lines, want the three the step is about (mailbox, the USER predicate, $other's guard): the test reads the patterns and their counts from the step, and a line it cannot parse is a line it does not hold to the golden", len(found))
	}
	for _, m := range found {
		pattern, want := m[1], m[2]
		// The shell pattern is a grep BRE, and the only metacharacter any of
		// these uses is `\.` for a literal dot -- which Go's regexp reads the
		// same way, so it compiles as written.
		rx, err := regexp.Compile(pattern)
		if err != nil {
			t.Errorf("check 81 step 8's pattern %q does not compile: %v", pattern, err)
			continue
		}
		got := len(rx.FindAllString(string(golden), -1))
		if got == 0 {
			t.Errorf("check 81 step 8 asks for `grep -o '%s'`, and the generated document contains no match: the FACT line would read 0 on a correct deployment (the review of PR #580 found this with `\"mu != null\"`, where the rules render `auth.token.mu != null`)", pattern)
			continue
		}
		if fmt.Sprint(got) != want {
			t.Errorf("check 81 step 8 says `%s` appears %s time(s) in the deployed document, and rules.Generate()'s document contains it %d time(s): the count is the document's, so a change to the template moves both", pattern, want, got)
		}
	}
}

// TestDocsCheck82ConfirmationIsTheOrdinaryOne: the database step's
// confirmation is `confirmOrdinary` -> `askOrdinary` -> `ask`, and `ask` takes
// `--yes` (`confirmed by --yes`) for every step that is not typed and not
// under --plan-only. Check 82 said the confirmation "is not `--yes`", which is
// the one thing the code does not say; what is true, and what the check needs,
// is that a coding agent's session never confirms it (initflow.AgentError) and
// that this check does not take the --yes shortcut.
func TestDocsCheck82ConfirmationIsTheOrdinaryOne(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	src := readSrc(t, "init.go")
	if !strings.Contains(src, "if r.o.yes && !r.o.planOnly {") {
		t.Errorf("internal/cli/init.go no longer shows `ask` taking --yes for an ordinary step: the checklist's text about the database step's confirmation is written against this code, so read it again before changing the check")
	}
	if !strings.Contains(src, "confirmed by --yes") {
		t.Errorf("internal/cli/init.go no longer prints %q: check 82 quotes it as what the owner sees instead of the typed confirmation", "confirmed by --yes")
	}
	if strings.Contains(section, "is not `--yes`") {
		t.Errorf("## Check 82: says the database step's confirmation is not --yes, and internal/cli/init.go's ask takes --yes for an ordinary step (the database step is confirmOrdinary, not confirmLocation's typed one): an owner who passed --yes would read the check as saying the CLI refused something it accepted")
	}
	if !strings.Contains(section, "confirmed by --yes") {
		t.Errorf("## Check 82: does not name the `confirmed by --yes` line: the check's point is that it does not take that shortcut, so it has to name the shortcut")
	}
}

// TestDocsCheck82PlanLineNumbersPointAtTheActLines: check 82 step 3 names the
// two lines of internal/infra/firebasedb.go that plan the two act lines, and
// the numbers are what tells the owner which of them comes first. The test
// reads the numbers out of the checklist and checks the lines they name, so a
// file that moved under the claim fails here instead of printing a line
// number that points at something else (the merge of 2026-10-18 moved the
// marker write from :274 to :276).
func TestDocsCheck82PlanLineNumbersPointAtTheActLines(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	re := regexp.MustCompile("appends the PutRules action at `:(\\d+)` and the marker's `PutIfMatch` after it at `:(\\d+)`")
	m := re.FindStringSubmatch(section)
	if m == nil {
		t.Fatalf("## Check 82: does not carry the sentence naming the two lines of DB.Plan that plan the rules action and the marker write: the test reads the line numbers from it, and the check needs them to say which of the two act lines is planned first")
	}
	lines := strings.Split(readSrc(t, "../infra/firebasedb.go"), "\n")
	for _, c := range []struct {
		n    int
		want string
		what string
	}{
		{atoi(t, m[1]), "deploys the security rules (%d bytes, generated)", "the rules action"},
		{atoi(t, m[2]), "records the rules generation (%s = %d)", "the marker's PutIfMatch"},
	} {
		if c.n < 1 || c.n > len(lines) {
			t.Errorf("check 82 names internal/infra/firebasedb.go:%d, and the file has %d lines", c.n, len(lines))
			continue
		}
		if !strings.Contains(lines[c.n-1], c.want) {
			t.Errorf("check 82 names internal/infra/firebasedb.go:%d as %s, and that line is %q: the number is the check's own claim about which act line is planned first, so it is updated with the file", c.n, c.what, strings.TrimSpace(lines[c.n-1]))
		}
	}
}

// TestDocsCheck82PrintVarsKeys: check 82 step 2 tells the owner to read the
// resolved Firebase project and the region out of `init --firebase
// --print-vars`. That output is one JSON object, `{"installation": ...,
// "firebase": ...}` (`init_firebase.go`'s firebaseVars), where the project is
// `firebase.project` and the region `installation.region`; the name
// `firebase_project` is the local config's key and the Firebase root's output,
// not a key of this document. The step names the two keys it means.
func TestDocsCheck82PrintVarsKeys(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	src := readSrc(t, "init_firebase.go")
	if !strings.Contains(src, `"installation": inst, "firebase": fb`) {
		t.Errorf("internal/cli/init_firebase.go no longer prints --print-vars as {\"installation\": ..., \"firebase\": ...}: check 82 tells the owner which keys to read, so the shape it names has to be this one")
	}
	for _, want := range []string{"`firebase.project`", "`installation.region`"} {
		if !strings.Contains(section, want) {
			t.Errorf("## Check 82: does not name %s, the key --print-vars actually prints the value under: a FACT line that sends the owner looking for a key that is not in the output is a step that reads as a broken CLI", want)
		}
	}

	// The database step's no-op line is its own sentence, not a Terraform
	// root's "No changes" (which is applyRoot's, printed for a root whose plan
	// is empty).
	if !strings.Contains(readSrc(t, "init_firebase.go"), "The database already has the rules, mark, project name and mode.") {
		t.Errorf("internal/cli/init_firebase.go no longer prints the database step's already-settled line: check 82 quotes it as what a settled installation's second run prints")
	}
	if strings.Contains(section, "reports `No changes` for the step") {
		t.Errorf("## Check 82: says the database step reports `No changes`, which is applyRoot's line for an empty Terraform plan: the database step prints %q instead, and it is that sentence the check must quote", "The database already has the rules, mark, project name and mode.")
	}
}

// TestDocsCheck89GuardMatchesWhatExists: check 89's guard is written to
// hard-stop on a checkout without the app, and it stopped on `mobile/app.json`
// because P3.1 had not landed when the check was written. It has: the file is
// in this checkout, so the guard stops on `mobile/eas.json` (P4.4 makes it)
// and on an app.json that does not carry the store identity yet -- the ids a
// store build cannot be submitted without. The old guard read
// `d["ios"]["bundleIdentifier"]` and would have died with a Python traceback
// instead of saying so.
func TestDocsCheck89GuardMatchesWhatExists(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 89:")
	guard := codeBlockAfter(t, section, "# === Check 89: setup + guard")

	if _, err := os.Stat("../../mobile/app.json"); err == nil {
		// The claim lives in two places -- the check's own preamble and the
		// register row at the top of the document -- and both are what the owner
		// reads to decide whether a guard stop is a finding or the state of the
		// tree, so both are held to the checkout.
		for _, where := range []struct{ what, text string }{
			{"## Check 89", section},
			{"the register row for check 89", registerRow(t, doc, "89")},
		} {
			for _, stale := range []string{
				"there is no `mobile/app.json`",
				"hard-stops** on a checkout without `mobile/app.json`",
			} {
				if strings.Contains(where.text, stale) {
					t.Errorf("%s: carries %q, and mobile/app.json is in this checkout (P3.1 landed): the check's own account of what its guard stops on is what tells the owner whether a stop is a finding or the state of the tree", where.what, stale)
				}
			}
		}
	}
	if !strings.Contains(guard, "mobile/eas.json") {
		t.Errorf("## Check 89: the guard does not stop on mobile/eas.json, which is what is absent today (P4.4 writes it): the guard has to name the file whose absence is the honest current state")
	}
	if strings.Contains(guard, `d["ios"]["bundleIdentifier"]`) {
		t.Errorf("## Check 89: the guard reads ios.bundleIdentifier straight out of app.json, and the file has no such key yet: the guard dies with a Python traceback instead of stopping with the reason a store build cannot be submitted")
	}
	for _, want := range []string{"bundleIdentifier", "android", "sys.exit("} {
		if !strings.Contains(guard, want) {
			t.Errorf("## Check 89: the guard does not read/carry %q: it must name the store identity it needs and stop with a sentence when the app does not have it", want)
		}
	}

	// The stop has to be a stop. The store identity is read inside a command
	// substitution (`APP=$(python3 ...)`), and a `sys.exit` there only sets a
	// status: without the guard acting on it, the block prints "Check 89
	// stopped: ..." and then goes on to "Check 89 ready", and the owner builds
	// the very artifact the sentence said could not be submitted.
	if !stoppedOnCommandSubstitution(guard) {
		t.Errorf("## Check 89: the guard reads the store identity in a command substitution and does not act on its status (`) || exit 1` after the python block): `sys.exit` inside `$(...)` only sets the substitution's status, so the guard would print \"Check 89 stopped: ...\" and then print \"Check 89 ready\" and the owner would build anyway -- while the preamble and the register row both say it hard-stops")
	}

	// And it has to be re-runnable, which is what the block's own "re-run this
	// before every step" says: the file tests have to be resolved from a known
	// directory, not from wherever the previous run left the shell (the block
	// ends in mobile/, so a bare `[ -f mobile/app.json ]` fails on the second
	// run with "there is no mobile/app.json in this checkout" -- a false stop).
	root := strings.Contains(guard, "$ROOT/mobile/app.json") && strings.Contains(guard, "$ROOT/mobile/eas.json")
	if !root {
		t.Errorf("## Check 89: the guard's file tests are not resolved from the repository root (`$ROOT/mobile/app.json`): the block ends in `mobile/`, so a re-run from there tests `mobile/mobile/app.json` and stops with the false message \"there is no mobile/app.json in this checkout\"")
	}
	for _, line := range strings.Split(guard, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[ -f ") {
			continue
		}
		rel := strings.TrimPrefix(line, "[ -f ")
		if i := strings.IndexAny(rel, " \t"); i >= 0 {
			rel = rel[:i]
		}
		rel = strings.Trim(rel, `"`)
		if !strings.HasPrefix(rel, "$ROOT/") {
			t.Errorf("## Check 89: the guard tests %s, a path relative to whatever directory the shell happens to be in: the block ends in mobile/, so every file test has to be resolved from $ROOT (the repository root) for the block to be re-runnable as it says", rel)
		}
	}
}

// stoppedOnCommandSubstitution reports whether the guard acts on the status of
// the `$(python3 ...)` read of the store identity: the closing fence of the
// substitution (`)`) has to carry `|| exit 1`.
func stoppedOnCommandSubstitution(guard string) bool {
	end := strings.Index(guard, "\nPY\n")
	if end < 0 {
		return false
	}
	rest := guard[end+len("\nPY\n"):]
	line, _, _ := strings.Cut(rest, "\n")
	return strings.HasPrefix(strings.TrimSpace(line), ")") && strings.Contains(line, "|| exit 1")
}

// TestDocsReadBackIsComparedAsJSONNotBytes: checks 81 and 82 read the deployed
// document back from `/.settings/rules.json` and compare it with what this
// binary generates -- and `internal/infra`'s DB.Plan makes that comparison with
// `SameJSON`, which unmarshals both documents and re-marshals them: the same
// JSON, **formatting ignored**. The checks said the read-back was 42687 bytes,
// which is `rules.Generate()`'s own size and nothing the database owes them:
// a document the API stored with other whitespace is the same document at
// another byte count, and a check that calls it a failure is a check that fails
// a correct deployment. The generator's number is right and stays -- it is what
// the plan's rules line prints and what `testdata/rules.golden.json` holds --
// so this test holds the two apart instead of dropping it.
func TestDocsReadBackIsComparedAsJSONNotBytes(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	if !strings.Contains(readSrc(t, "../infra/firebasedb.go"), "if !SameJSON(live, gen) {") {
		t.Fatalf("internal/infra/firebasedb.go no longer decides the rules action with `if !SameJSON(live, gen) {`: the two checks' account of what a correct deployment is is written against that comparison, so read the code again before changing them")
	}

	for _, heading := range []string{"## Check 81:", "## Check 82:"} {
		section := docSection(t, path, doc, heading)
		if !strings.Contains(section, "SameJSON") ||
			(!strings.Contains(section, "ignoring formatting") && !strings.Contains(section, "formatting ignored")) {
			t.Errorf("%s does not say that the read-back is compared with the generated document by `infra.SameJSON`, the same JSON with the formatting ignored: it reads a live document back and compares it with the generated one, and what the CLI compares is the JSON -- a check that does not say so reads a document the API stored with other whitespace as a failed deploy", heading)
		}
		for _, para := range paragraphsOf(section) {
			if !mentionsReadBack(para) {
				continue
			}
			for _, clause := range clausesOf(para) {
				if strings.Contains(clause, "42687") && mentionsReadBack(clause) {
					t.Errorf("%s: the same sentence claims a size for the document read back and names 42687:\n%s\n42687 is `rules.Generate()`'s own size, not the read-back document's -- DB.Plan calls the two the same document when SameJSON says so, formatting ignored, so the byte count of what the API stored is the API's to choose and a check that requires this number of it fails a correct deployment", heading, clause)
				}
			}
			if byteIdenticalClaim(para) {
				t.Errorf("%s: a paragraph about the read-back claims it is byte-identical to the generated document:\n%s\nthe comparison is `infra.SameJSON` -- the same JSON, formatting ignored -- so a read-back that differs in whitespace is the same document and not a failure", heading, para)
			}
		}
	}
}

// mentionsReadBack reports whether a paragraph is about the **document** read
// back from the database, by any of the names the checks use for it. The marker
// node read back from `config/limits/policyRules` is a different thing and is
// not one of them.
func mentionsReadBack(para string) bool {
	for _, marker := range []string{
		"/.settings/rules.json", "deployed.json", "after.json", "before.json",
		"deployed document", "read-back document", "read-back's", "read back the",
	} {
		if strings.Contains(para, marker) {
			return true
		}
	}
	return false
}

// clausesOf splits a paragraph into sentences and their semicolon-joined
// clauses, which is the unit a claim is made in: "the read-back's byte count is
// X" is a claim about the read-back even when the paragraph also says what the
// generator produces, and it is the clause that has to be right.
func clausesOf(para string) []string {
	return regexp.MustCompile(`[.;]\s+`).Split(para, -1)
}

func byteIdenticalClaim(para string) bool {
	for _, phrase := range []string{"byte-identical", "byte for byte", "byte-for-byte", "identical bytes", "the same bytes"} {
		if strings.Contains(strings.ToLower(para), phrase) {
			return true
		}
	}
	return false
}

// paragraphsOf returns the text paragraphs of a section, skipping fenced code
// blocks: a code block is a command to run, not a claim.
func paragraphsOf(section string) []string {
	var out []string
	var cur []string
	fenced := false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			flush()
			continue
		}
		if fenced {
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// TestDocsEachCheckHasOnePasteBackParagraph: a check's "**Paste back:**"
// paragraph is the register of what the owner reports, so a check carries one
// and it starts a paragraph of its own. Merge 5965a75 left a second one -- a
// stale copy of check 69b's, without its cookie clause -- glued to the end of
// check 70c's, where it read as a step of a check about copy controls; no test
// looked at the document's paragraphs at all, and this one does.
func TestDocsEachCheckHasOnePasteBackParagraph(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	parts := regexp.MustCompile(`(?m)^## `).Split(doc, -1)
	if len(parts) < 2 {
		t.Fatalf("%s has no `## ` sections", path)
	}
	for _, sec := range parts[1:] {
		head, _, _ := strings.Cut(sec, "\n")
		if !strings.HasPrefix(head, "Check ") {
			continue
		}
		name := strings.SplitN(head, ":", 2)[0]
		if n := strings.Count(sec, "**Paste back:**"); n > 1 {
			t.Errorf("## %s: carries %d \"**Paste back:**\" paragraphs, want one: a check reports back once, and a second one is another check's paragraph that the document's own reading no longer tells apart from this check's", name, n)
		}
	}

	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "**Paste back:**") {
			continue
		}
		if i > 0 && strings.TrimSpace(lines[i-1]) != "" {
			t.Errorf("%s:%d: a \"**Paste back:**\" paragraph starts on a line whose predecessor is non-blank (%q): glued like that it reads as the tail of the paragraph above it, which is how a paragraph from another check sat inside check 70c after merge 5965a75", path, i+1, strings.TrimSpace(lines[i-1]))
		}
	}
}

// TestDocsCheck89CredentialScanSkipsTheAppFiles: check 89 step 5 scans the
// tracked files under mobile/ for anything credential-shaped, and its pattern
// listed `json` beside `p12|mobileprovision|keystore|jks` in one alternation,
// so it matched mobile/app.json, mobile/package.json and
// mobile/package-lock.json every time: the `|| echo "nothing credential-shaped
// ..."` half was dead and the FACT line's "empty list" could never be true. The
// scan is held to the checkout's own file names here: the app's four files must
// survive it, because a scan that always fires is a finding nobody reads.
func TestDocsCheck89CredentialScanSkipsTheAppFiles(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 89:")
	step := stepText(t, section, "5. **No store credential", "\n\n**Passes when:**")

	known := []string{"mobile/app.json", "mobile/package.json", "mobile/package-lock.json", "mobile/eas.json"}
	detect := regexp.MustCompile(`grep -E '([^']+)'`)
	exclude := regexp.MustCompile(`grep -Ev '([^']+)'`)

	lines := 0
	for _, line := range strings.Split(step, "\n") {
		if !strings.Contains(line, "ls-files mobile") {
			continue
		}
		lines++
		var detects, excludes []*regexp.Regexp
		for _, m := range detect.FindAllStringSubmatch(line, -1) {
			rx, err := regexp.Compile(m[1])
			if err != nil {
				t.Errorf("check 89 step 5's pattern %q does not compile: %v", m[1], err)
				continue
			}
			detects = append(detects, rx)
		}
		for _, m := range exclude.FindAllStringSubmatch(line, -1) {
			rx, err := regexp.Compile(m[1])
			if err != nil {
				t.Errorf("check 89 step 5's pattern %q does not compile: %v", m[1], err)
				continue
			}
			excludes = append(excludes, rx)
		}
		for _, name := range known {
			flagged := false
			for _, rx := range detects {
				if rx.MatchString(name) {
					flagged = true
				}
			}
			for _, rx := range excludes {
				if rx.MatchString(name) {
					flagged = false
				}
			}
			if flagged {
				t.Errorf("check 89 step 5's scan flags %s, a file of the app's own that is in this checkout: the scan's `|| echo \"nothing credential-shaped ...\"` half would be dead and its FACT line's empty list could never be true -- a scan that always fires is a finding nobody reads (list the credential types, and handle JSON by name)", name)
			}
		}
		if !strings.Contains(line, "||") && !strings.Contains(line, "grep -Ev") {
			t.Errorf("check 89 step 5's scan line %q has no `|| echo` for its empty list: the step's FACT line says an empty list is the pass, so the line that prints it has to be reachable", line)
		}
	}
	if lines != 2 {
		t.Errorf("check 89 step 5 has %d `ls-files mobile` scan lines, want 2 (the credential types, and the JSON that is not one of the app's own files)", lines)
	}
}

// TestDocsCheck82DoctorCallsNameTheProject: every `fugaro doctor` in check 82
// names the installation, as the guard's own call does. A bare `fugaro doctor`
// reads whichever project the machine's config resolves, so check 82 -- which
// exists to report whether a deploy landed on `fugaro-dev` -- would record
// another installation's `policy-rules-skew` state.
func TestDocsCheck82DoctorCallsNameTheProject(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	calls := 0
	fenced := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		// Only the commands, not the prose that quotes doctor's own sentences.
		if !fenced || !strings.Contains(line, "fugaro doctor") {
			continue
		}
		calls++
		if !strings.Contains(line, "--project fugaro") {
			t.Errorf("## Check 82: a `fugaro doctor` call does not name the installation:\n%s\nwithout `--project fugaro` it reads whichever project the machine's config resolves, and this check is about the `fugaro-dev` installation's marker", line)
		}
	}
	if calls < 4 {
		t.Errorf("## Check 82: has %d `fugaro doctor` calls, want the guard's, step 1's and step 6's two: the test only holds the calls it finds", calls)
	}
}

// TestDocsCheck81AckFalseIsOnAFreshMessage: check 81 step 5's `-d 'false'` ack
// was written to the same node the line above had just acked, so it was refused
// by the write-once `!data.exists()` and never reached the `newData.val() ==
// true` guard the comment and the FACT line named (P2.2's `c2` tries that guard
// on a fresh message). The false ack has to be on its own message.
func TestDocsCheck81AckFalseIsOnAFreshMessage(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	step := stepText(t, section, "5. **The user's second write", "6. **The granted read nodes")

	var ackedOnce, booleanAt string
	for _, line := range strings.Split(step, "\n") {
		i := strings.Index(line, "acked.json?auth=")
		if i < 0 {
			continue
		}
		url := line[strings.LastIndex(line[:i], `"`)+1 : i]
		if strings.Contains(line, "# 200") {
			ackedOnce = url
		}
		if strings.Contains(line, "-d 'false'") {
			booleanAt = url
		}
	}
	if ackedOnce == "" || booleanAt == "" {
		t.Fatalf("check 81 step 5 does not carry both the 200 ack and the `-d 'false'` ack (%q, %q): the two are what tells the boolean guard from the write-once one", ackedOnce, booleanAt)
	}
	if ackedOnce == booleanAt {
		t.Errorf("check 81 step 5 writes `-d 'false'` to %s, the node the 200 ack already wrote: the false ack is refused by the write-once `!data.exists()` before `newData.val() == true` is ever reached, so the line and its FACT sentence claim a guard nothing exercised -- the false ack needs a message of its own (seeded by IAM, as step 4 seeds the first)", booleanAt)
	}
	if !strings.Contains(step, "access_token=$AT") {
		t.Errorf("check 81 step 5 has no IAM seeding of the second message: the fresh message the `-d 'false'` line needs is not there, and the boolean guard is again tried on a node that already has an ack")
	}
}

// TestDocsCheck81MintSignsInTheFirebaseProject: `mint` calls
// `gcloud iam service-accounts sign-jwt --iam-account="$SA"`, and `$SA` is the
// installation's `token_signer` -- `fugaro-token-signer@<fp>.iam.gserviceaccount.com`,
// an account of the **Firebase** project (internal/localcfg requires exactly
// that shape). `--project` has to be that project ($FP), not the GCP project id
// ($P) the guard hard-codes, or the call names an account of one project while
// telling gcloud to look in another.
func TestDocsCheck81MintSignsInTheFirebaseProject(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	if !strings.Contains(section, `--iam-account="$SA" --project="$FP"`) {
		t.Errorf("## Check 81: `mint` does not sign with `--iam-account=\"$SA\" --project=\"$FP\"`: the signer is an account of the Firebase project ($FP), so `--project` has to be its project -- on an installation whose Firebase project is not its GCP project, naming $P is the same $P/$FP confusion the Restore had")
	}
	if strings.Contains(section, `--iam-account="$SA" --project="$P"`) {
		t.Errorf("## Check 81: `mint` still passes `--project=\"$P\"` (the GCP project id) to sign-jwt while signing as $SA (an account of the Firebase project): the two are the same only on a same-project installation")
	}
}

// TestDocsCheck81RestoreUsesTheFirebaseProject: the accounts this check adds
// are deleted through Identity Toolkit's
// `projects/{targetProjectId}/accounts:delete`, and targetProjectId is the
// **Firebase** project id -- the installation's `budget.firebase_project`,
// which the Firebase root's outputs write into the local config. The guard
// read only rtdb_url, firebase_api_key and token_signer and the Restore used
// the GCP project id, so on an installation whose Firebase project is not its
// GCP project the delete would 404 on an account the check had just created.
func TestDocsCheck81RestoreUsesTheFirebaseProject(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	guard := codeBlockAfter(t, section, "# === Check 81: setup + guard")
	restore := codeBlockAfter(t, section, "# === Check 81: Restore")

	if !strings.Contains(guard, "firebase_project") {
		t.Errorf("## Check 81: the guard block does not read budget.firebase_project out of the project's config: the Restore deletes the two Auth accounts through Identity Toolkit, whose project id is the Firebase project's, not the GCP project's")
	}
	if !strings.Contains(restore, "projects/$FP/accounts:batchDelete") {
		t.Errorf("## Check 81: the Restore does not delete the two accounts under $FP (the Firebase project read from the config) through the admin API: Identity Toolkit's targetProjectId is the Firebase project id, and the GCP project id is not it in general")
	}
	if strings.Contains(restore, "projects/$P/accounts:") {
		t.Errorf("## Check 81: the Restore deletes the two accounts under $P, which the guard hard-codes as the GCP project id fugaro-dev: on an installation whose Firebase project has another id the delete answers 404 and the two accounts are left behind")
	}

	// The call has to be authorised. `accounts:delete` and `accounts:batchDelete`
	// are admin endpoints: the web API key ($KEY) authenticates the **client**
	// endpoints only (accounts:signInWithCustomToken, step 1), so `?key=$KEY` on
	// one of them answers 401 and leaves both accounts behind -- the Restore's
	// FACT line would claim a 200 that never came, and the sweeper would not
	// collect them either (internal/budget/sweep.go deletes only `r~<slug>~<run>`
	// uids). The call this repository makes is internal/budget's
	// AuthAdmin.DeleteUsers: accounts:batchDelete, a Bearer credential, and
	// `{"localIds": [...], "force": true}`.
	if strings.Contains(codeLines(restore), "?key=$KEY") {
		t.Errorf("## Check 81: the Restore authenticates an Identity Toolkit admin call with the web API key (`?key=$KEY`): the key signs the client endpoints only, so this answers 401, the two accounts stay behind and the Restore's FACT line is false -- the call needs the owner's own credential as a Bearer token")
	}
	if !strings.Contains(restore, `-H "Authorization: Bearer $AT"`) {
		t.Errorf("## Check 81: the Restore's account delete carries no `Authorization: Bearer $AT`: it is the Identity Toolkit admin API, which is an OAuth2 API -- the web API key is not a credential for it")
	}
	if !strings.Contains(restore, `\"localIds\"`) || !strings.Contains(restore, `\"force\":true`) {
		t.Errorf("## Check 81: the Restore's account delete does not carry the body the repository's own admin call uses (`{\"localIds\": [...], \"force\": true}`): read internal/budget/authadmin.go's DeleteUsers and write the call it makes")
	}
	src := readSrc(t, "../budget/authadmin.go")
	for _, want := range []string{`"localIds": batch, "force": true`, `"/v1/projects/" + a.Project + "/accounts:" + op`} {
		if !strings.Contains(src, want) {
			t.Errorf("internal/budget/authadmin.go no longer carries %q: the Restore's admin call is written against that code, so read it again before changing the Restore", want)
		}
	}
}

// TestDocsCheck81TokKeepsTheBody: the `TOK` helper was
// `curl -sS -o /dev/null -w '%{http_code}'` -- the status alone -- while the
// FACT lines of steps 3, 4, 5 and 7 ask for each status **with** its body
// ("each status with its refused body `Permission denied`", "the five 401s and
// their bodies"). An owner following the check could not record what those
// lines demand. TOK now keeps the body and puts the status on a line of its
// own. Step 6 was then moved to a second, status-only `CODE` helper, and the
// review of PR #580 found that its FACT line asks for bodies too ("the first
// thirteen are 200, with the body being whatever that node holds ... and the
// second twenty-one are 401 with `Permission denied`"): the two columns are
// read through TOK as well, and no form of the helper discards a body.
func TestDocsCheck81TokKeepsTheBody(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	helpers := codeBlockAfter(t, section, "# === Check 81: the helpers")

	var tok string
	for _, line := range strings.Split(helpers, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "TOK()") {
			tok = line
		}
	}
	if tok == "" {
		t.Fatalf("## Check 81: the helpers define no `TOK()`, so this test cannot see whether it keeps the body: read the helper block again")
	}
	if strings.Contains(tok, "/dev/null") {
		t.Errorf("## Check 81: `TOK` still discards the body:\n%s\nthe FACT lines of steps 3, 4, 5, 6 and 7 ask for each status with its body, and a status-only helper cannot produce them", strings.TrimSpace(tok))
	}
	if !strings.Contains(tok, "%{http_code}") {
		t.Errorf("## Check 81: `TOK` does not print the status:\n%s\nit is the helper every line in steps 3, 4, 5, 6 and 7 goes through", strings.TrimSpace(tok))
	}

	steps := []struct{ from, to string }{
		{"3. **The fence's negative half", "4. **The user reads"},
		{"4. **The user reads", "5. **The user's second write"},
		{"5. **The user's second write", "6. **The granted read nodes"},
		{"6. **The granted read nodes", "7. **A token whose `mu`"},
		{"7. **A token whose `mu`", "8. **The fence is the deployed"},
	}
	for _, st := range steps {
		step := stepText(t, section, st.from, st.to)
		// The premise is the step's own FACT lines, not the word anywhere in
		// it: step 6's prose mentions a `null` body in passing, so a check
		// over the whole step would stay green after its FACT line stopped
		// asking for one.
		asks := false
		for _, para := range paragraphsOf(step) {
			if !strings.HasPrefix(strings.TrimSpace(para), "**FACT:**") {
				continue
			}
			if strings.Contains(para, "body") || strings.Contains(para, "bodies") {
				asks = true
			}
		}
		if !asks {
			t.Fatalf("check 81 %s: no FACT paragraph mentions a body, so this test's premise (a body is asked for here) is gone", st.from)
		}
		if !strings.Contains(step, "TOK ") {
			t.Errorf("check 81 %s: its FACT lines ask for the bodies and the step calls no `TOK`: the bodies cannot be recorded", st.from)
		}
	}
	// No form of the helper may throw a body away: step 6's FACT asks for the
	// bodies its two columns answer with (the node's value, or `null`), so a
	// status-only helper here is a FACT line the owner cannot fill in.
	if strings.Contains(helpers, "-o /dev/null") {
		t.Errorf("## Check 81: the helper block defines a form that discards the body (`-o /dev/null`): every step whose FACT line asks for a body has to be able to record it, step 6's columns included")
	}
}

// TestDocsRunnableBlocksNameFilesThatExist: a runnable block of checks 81 and
// 82 is run from the checkout root, so every path it names has to be a path
// that checkout has. The review of PR #580 found both checks comparing the
// document read back from the database against `testdata/rules.golden.json`,
// which does not exist from the root -- the golden is
// internal/budget/rules/testdata/rules.golden.json, where this repository's own
// rules tests read it. The step would have failed on the owner's machine with a
// traceback, in a check whose whole subject is the deployed document.
func TestDocsRunnableBlocksNameFilesThatExist(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// A file the block names: a repo-relative path with a known extension. A
	// `$VAR/...` path is the run's own scratch space, and a URL is not a file.
	fileish := regexp.MustCompile(`\.(json|tmpl|yaml|yml|go|md)$`)
	for _, heading := range []string{"## Check 81:", "## Check 82:"} {
		section := docSection(t, path, doc, heading)
		fenced, found := false, 0
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fenced = !fenced
				continue
			}
			if !fenced {
				continue
			}
			for _, field := range strings.Fields(line) {
				tok := strings.Trim(field, "\"'`()<>|,")
				if !strings.Contains(tok, "/") || strings.ContainsAny(tok, "$*?") ||
					strings.HasPrefix(tok, "http") || strings.HasPrefix(tok, "/") || !fileish.MatchString(tok) {
					continue
				}
				found++
				if _, err := os.Stat(filepath.Join("..", "..", tok)); err != nil {
					t.Errorf("%s: a runnable block names %s, and this checkout has no such file: the blocks run from the repository root, so a path that is not the checkout's fails on the owner's machine -- the golden is internal/budget/rules/testdata/rules.golden.json", heading, tok)
				}
			}
		}
		if found == 0 {
			t.Errorf("%s: no runnable block names a repository file, and check 81's step 8 and check 82's step 5 both read the golden: this test is no longer looking at the paths it exists for", heading)
		}
	}
}

// TestDocsCheck81UrlsCarryTheScheme: the guard reads the database's address out
// of the project's config and strips the scheme from it (`sed -e
// 's|^https://||'`), so every URL built from it has to put `https://` back.
// Check 81 stripped the scheme and then wrote 33 bare `"$DB/..."` URLs -- plain
// HTTP on port 80, which cannot answer the 200/401 the whole check is made of --
// while only its step 8 re-added the scheme, and check 82 (the same guard, the
// same strip, and docs/gcp-setup.md's block) re-adds it in every URL it writes.
// No test looked at the scheme, which is how 33 lines of the check were unrunnable
// under a green `go test ./internal/cli`.
func TestDocsCheck81UrlsCarryTheScheme(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	for _, heading := range []string{"## Check 81:", "## Check 82:", "## Check 89:"} {
		section := docSection(t, path, doc, heading)
		if !strings.Contains(section, "$DB") {
			continue // check 89 builds no URL from $DB (its guard is eas/profiles)
		}
		if !strings.Contains(section, "s|^https://||") {
			t.Errorf("%s: reads $DB out of the config without the `s|^https://||` strip this test's rule is about, so the rule may not be the section's any more: read the guard before trusting the scheme in $DB", heading)
		}
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // the guard's own comment quotes the broken form to warn about it
			}
			for i := 0; ; {
				j := strings.Index(line[i:], `"$DB/`)
				if j < 0 {
					break
				}
				t.Errorf("%s: a URL is built as %s\n$DB is the host alone (the guard strips the scheme), so this URL is plain HTTP on port 80 and the request cannot answer the status the check requires: every URL from $DB is written \"https://$DB/...\", as check 82 and docs/gcp-setup.md write theirs", heading, strings.TrimSpace(line[i+j:]))
				i += j + len(`"$DB/`)
			}
		}
	}
}

// TestDocsCheck81TokenDirIsOwnerOnly: check 81 mints custom tokens into
// `/tmp/chk81` -- `$W/jwt-$uid.txt`, `$W/payload-$uid.json` -- and a custom
// token is a bearer credential for whoever holds it until its `exp` (the helper
// sets `now+3600`). `mkdir -p "$W"` under the default umask leaves that
// directory 0755 with 0644 files in it, readable by any other local user for the
// length of the run, in a check whose own text says the tokens are "never in a
// file that outlives the run". The repository already writes credential-bearing
// files this way (docs/gcp-live-checklist.md:1159: `(umask 077; terraform state
// pull > …)   # holds the API key string: mode 600`).
func TestDocsCheck81TokenDirIsOwnerOnly(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")
	guard := codeBlockAfter(t, section, "# === Check 81: setup + guard")

	for _, want := range []string{"$W/jwt-$uid.txt", "$W/payload-$uid.json"} {
		if !strings.Contains(section, want) {
			t.Fatalf("## Check 81: the helpers no longer write %s, so the premise of this test (a minted credential lives in $W) is gone: read the mint helper again before trusting this guard", want)
		}
	}
	// The creation itself has to carry the umask: a `mkdir -p "$W"` on a line of
	// its own, with a `umask 077` somewhere above it, is a directory made at 0755.
	mkdir := ""
	for _, line := range strings.Split(guard, "\n") {
		if strings.Contains(line, "mkdir -p") && strings.Contains(line, "$W") {
			mkdir = line
		}
	}
	if mkdir == "" {
		t.Fatalf("## Check 81: the guard does not create $W with `mkdir -p` any more, so this test cannot see the mode it is made at: read the guard again")
	}
	if !strings.Contains(mkdir, "umask 077") {
		t.Errorf("## Check 81: the guard creates $W here:\n%s\nwith no `umask 077` on the same line: a 0755 /tmp/chk81 holding the minted custom tokens and their payloads is readable by every other local user for the length of the run, and a custom token is a bearer credential for the account until its exp", strings.TrimSpace(mkdir))
	}
	if !strings.Contains(guard, "chmod 700") {
		t.Errorf("## Check 81: the guard does not `chmod 700` $W: `umask 077` only shapes a directory this run creates, and a directory a previous run (or a previous copy of this block) left at 0755 keeps its mode -- the tokens go into the same path either way")
	}
}

// TestDocsCheck89RegisterRowDoesNotClaimTheGuardReadsTheTrack: the register row
// and the Fails-when paragraph both said the guard "stops on any profile whose
// submit track is not internal". Nothing in the guard reads the track: it tests
// that `mobile/eas.json` exists and that `app.json` carries the store identity.
// The track is read by step 1's hand-run `jq`, and the stop is the owner's own
// judgement -- which is what the section's own preamble says the guard does, so
// the row contradicted the section it points at, and the row is what the owner
// reads first.
func TestDocsCheck89RegisterRowDoesNotClaimTheGuardReadsTheTrack(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 89:")
	guard := codeBlockAfter(t, section, "# === Check 89: setup + guard")
	row := registerRow(t, doc, "89")

	// The premise: the guard really does not read the track.
	for _, unread := range []string{"android.track", ".submit", "jq"} {
		if strings.Contains(guard, unread) {
			t.Fatalf("## Check 89: the guard now reads %q, so it does read the submit track and this test's premise (the track is step 1's read, not the guard's) is gone: read the guard again before changing the row", unread)
		}
	}

	for _, where := range []struct{ what, text string }{
		{"the register row for check 89", row},
		{"## Check 89's \"Fails when\" paragraph", stepText(t, section, "**Fails when:**", "**Paste back:**")},
	} {
		for _, stale := range []string{"guard stops on any profile", "the guard stops on the profile", "guard stops on a profile"} {
			if strings.Contains(where.text, stale) {
				t.Errorf("%s: carries %q, and no line of the guard block reads a submit track -- it stops on the app's own files (`mobile/eas.json` today, and the store identity in `app.json`), so a stop on a track is a read the guard does not make and an owner looking for it reads the row as a promise the section does not keep", where.what, stale)
			}
		}
	}
	if !strings.Contains(row, "submit.internal.android.track") && !strings.Contains(row, "submit track is read") {
		t.Errorf("the register row for check 89 no longer says the submit track is read before anything is built: the row has to account for the `internal` track the check is made of, and the read is step 1's `jq` with the owner's stop on it")
	}
}

// TestDocsGuardPreamblesNameAConstructTheyUse: check 81's guard preamble said
// "the `${VAR:?}` forms below are bash's" while no guard block among 81, 82 and
// 89 contains a single `${VAR:?}` -- the bash-only construct check 81's block
// actually has is `${!v}` (indirect expansion) in its `for v in DB KEY SA FP`
// loop. The sentence is check 58's (its guard does use `${VAR:?}`), so it was
// right for the wrong reason: an owner who greps for the named construct finds
// nothing, and the reason to `source` the block instead of pasting it into zsh
// is the construct that is really there.
func TestDocsGuardPreamblesNameAConstructTheyUse(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	for _, c := range []struct{ heading, marker string }{
		{"## Check 81:", "# === Check 81: setup + guard"},
		{"## Check 82:", "# === Check 82: setup + guard"},
		{"## Check 89:", "# === Check 89: setup + guard"},
	} {
		section := docSection(t, path, doc, c.heading)
		guard := codeBlockAfter(t, section, c.marker)

		var preamble string
		for _, para := range paragraphsOf(section) {
			if strings.Contains(para, "**Guard block") {
				preamble = para
				break
			}
		}
		if preamble == "" {
			t.Fatalf("%s: has no \"**Guard block\" paragraph, so this test cannot tell which prose describes the block it reads", c.heading)
		}
		if strings.Contains(preamble, "${VAR:?}") && !strings.Contains(guard, "${VAR:?}") {
			t.Errorf("%s: the guard preamble names `${VAR:?}` and the block uses it nowhere: the sentence is what tells the owner why to `source` the block rather than paste it into an interactive zsh, and a construct that is not in the block sends them looking for the wrong thing", c.heading)
		}
		if strings.Contains(guard, "${!v}") && strings.Contains(preamble, "interactive zsh") && !strings.Contains(preamble, "${!v}") {
			t.Errorf("%s: the guard block is bash through `${!v}` (indirect expansion -- `${(P)v}` in zsh) and its preamble does not name it: the sentence about pasting into an interactive zsh has to name the construct that is actually there", c.heading)
		}
	}
}

// TestDocsCheck81Step5CountsItsTokLines: step 5's FACT line says how many
// statuses its block produces and points at two of them by position ("the
// third line ... the value guard", "the fifth line ... its own parent"). The
// block is one IAM `curl` (the seed) and five `TOK` lines, so the count and
// both positions have to be counted the same way -- and step 4, whose block is
// two seeds and six `TOK` lines, counts by `TOK` line too. The review of
// PR #580 found the count and the second position bumped to six and fifth
// while "the third line" was left, so the sentence contradicted itself under
// either numbering; a position is only worth naming if it is the line the
// FACT line describes, which is what this test holds it to.
func TestDocsCheck81Step5CountsItsTokLines(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	tokLines := func(step, marker string) []string {
		t.Helper()
		block := codeBlockAfter(t, step, marker)
		var out []string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "TOK ") {
				out = append(out, line)
			}
		}
		return out
	}

	step4 := stepText(t, section, "4. **The user reads", "5. **The user's second write")
	toks4 := tokLines(step4, "AT=$(gcloud auth print-access-token)")
	if len(toks4) != 6 || !strings.Contains(step4, "the six statuses") {
		t.Errorf("check 81 step 4's block has %d `TOK` lines and its FACT line does not say `the six statuses`: the two steps count their statuses the same way -- by `TOK` line, not by the IAM seeds -- and step 5's count is read against this one", len(toks4))
	}

	step5 := stepText(t, section, "5. **The user's second write", "6. **The granted read nodes")
	toks5 := tokLines(step5, "AT=$(gcloud auth print-access-token)")
	if len(toks5) != 5 {
		t.Fatalf("check 81 step 5's block has %d `TOK` lines, and this test counts the block's statuses by them (the IAM seed is a `curl`, and step 4 counts its own six the same way): the positions its FACT line names cannot be checked against the block", len(toks5))
	}
	if !strings.Contains(step5, "the five `TOK` statuses") {
		t.Errorf("check 81 step 5's FACT line does not say `the five `TOK` statuses`: its block is %d `TOK` lines and one IAM seed, and step 4 counts its own six `TOK` lines the same way, so a count of six is the seed counted in or the TOK lines miscounted", len(toks5))
	}
	for _, want := range []struct {
		ord string
		n   int
		cmd string
	}{
		{"The third `TOK` line", 3, `'false'`},
		{"The fourth `TOK` line", 4, "chk81-absent"},
	} {
		if !strings.Contains(step5, want.ord) {
			t.Errorf("check 81 step 5's FACT line does not say %q: it points at two of the block's lines by position, and a position that is not named cannot be read against the block", want.ord)
		}
		if !strings.Contains(toks5[want.n-1], want.cmd) {
			t.Errorf("check 81 step 5's %s is\n%s\nwhich does not carry %s: the position the FACT line names and the command it describes have to be the same line", want.ord, strings.TrimSpace(toks5[want.n-1]), want.cmd)
		}
	}

	// Step 7 counts the same way, over a block of five `TOK` lines and no
	// seed: "the five 401s and their bodies". It is the third count of this
	// kind in the section, so it is read against the block here too -- an
	// undercount is the finding this test exists for (PR #580's rounds 6
	// and 7).
	step7 := stepText(t, section, "7. **A token whose `mu`", "8. **The fence is the deployed")
	toks7 := tokLines(step7, `MI=$(signin "$BOB" "{\"mu\":\"$ALICE\"}")`)
	if len(toks7) != 5 || !strings.Contains(step7, "the five 401s") {
		t.Errorf("check 81 step 7's block has %d `TOK` lines and its FACT line does not say `the five 401s`: every line in it is a refusal, so the count in the FACT line is the block's own", len(toks7))
	}
}

// TestDocsCheck81GrantListNamesTheTemplatesOwnPredicates: the paragraph that
// states what a `mu` token may read cites rules.json.tmpl by line number, and
// invites the owner to open those lines. The review of PR #580 found line 132
// -- spend/$day/runs/$slug/$run -- lumped into a `TOKEN || USER` list when the
// template gates it with `RUN || USER`, and the sentence after it filing
// runs/** and agents/** under `TOKEN` when the template gates them with `RUN`.
// The effect asserted (USER is what admits a mu token) was right; the evidence
// named for it, at numbers the owner is told to open, was not.
func TestDocsCheck81GrantListNamesTheTemplatesOwnPredicates(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	var para string
	for _, p := range paragraphsOf(section) {
		if strings.Contains(p, "D112's grant sits at") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatalf("## Check 81: no paragraph names the locations D112's grant sits at: this test reads the paragraph that lists them, so it cannot check what it says")
	}

	// The template's own predicates, at the lines the paragraph cites.
	lines := strings.Split(readSrc(t, "../budget/rules/rules.json.tmpl"), "\n")
	for _, want := range []struct {
		n    int
		pred string
	}{
		{11, `"TOKEN || USER"`}, {12, `"TOKEN || USER"`},
		{22, `"TOKEN || USER"`}, {65, `"TOKEN || USER"`},
		{132, `"RUN || USER"`},
		{30, `"RUN"`}, {153, `"RUN"`},
		{207, `"MAILBOX"`}, {223, `"MAILBOX"`},
	} {
		if want.n < 1 || want.n > len(lines) {
			t.Fatalf("rules.json.tmpl has %d lines and the paragraph cites line %d", len(lines), want.n)
		}
		if got := lines[want.n-1]; !strings.Contains(got, want.pred) {
			t.Errorf("rules.json.tmpl line %d is %s and the paragraph cites it as %s: the paragraph hands the owner line numbers to open, so a number that does not hold the predicate claimed is evidence for something else", want.n, strings.TrimSpace(got), want.pred)
		}
	}
	if !strings.Contains(para, "`RUN || USER`") {
		t.Errorf("## Check 81: the paragraph listing D112's grants does not name `RUN || USER` for the day's per-run node (rules.json.tmpl line 132): a mu token reads it because USER is in that disjunction, and a citation that says `TOKEN || USER` there is not what the template says")
	}
	if !strings.Contains(para, "`RUN`") {
		t.Errorf("## Check 81: the paragraph does not file runs/** and agents/** under `RUN`: the template gates both with it (lines 30 and 153), and calling them `TOKEN` names a predicate a mu token fails for another reason than the one the sentence is making")
	}
}

// TestDocsCheck81SeedCountMatchesTheBlocks: check 81 tells the owner how much
// of the project it writes by hand, in two sentences -- "N nodes under Alice's
// mailbox are seeded with IAM" (what changes in the project) and "the N nodes
// it reads and acks ... are **seeded with the operator's IAM credentials**"
// (what the check does not cover). Both said two, and the blocks seed three:
// step 4's outbox message and heartbeat, and step 5's second outbox message,
// which the value guard needs because a `false` written to the message step 5
// already acked would be refused by `!data.exists()` and prove nothing about
// the boolean. An owner reading "two" would look for one seed fewer than the
// check writes into the installation. The count is derived from the blocks
// here, so a fourth seed fails this test until the prose counts it too.
func TestDocsCheck81SeedCountMatchesTheBlocks(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	// The seeds are the steps' own IAM writes: `?access_token=$AT`, which only
	// the seeding and the Restore use, so the Restore is cut off first.
	i := strings.Index(section, "**Restore.**")
	if i < 0 {
		t.Fatalf("## Check 81: has no \"**Restore.**\" paragraph, so this test cannot tell the steps' seeds from the Restore's deletes")
	}
	steps := section[:i]
	seeds := strings.Count(steps, "?access_token=$AT")
	if seeds == 0 {
		t.Fatalf("## Check 81: no step writes with `?access_token=$AT`: the seeding this test counts is gone from the blocks, so the sentences below cannot be checked against them")
	}
	word, ok := map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}[seeds]
	if !ok {
		t.Fatalf("check 81's steps make %d IAM seeds and this test has no word for that count: write one", seeds)
	}
	for _, stem := range []string{
		"nodes under Alice's mailbox are seeded with IAM",
		"are **seeded with the operator's IAM credentials**",
	} {
		var para string
		for _, p := range paragraphsOf(section) {
			if strings.Contains(p, stem) {
				para = p
				break
			}
		}
		if para == "" {
			t.Fatalf("## Check 81: no paragraph says %q: this test holds that sentence to the blocks' seed count, so it cannot check a sentence that is not there", stem)
		}
		if !strings.Contains(strings.ToLower(para), strings.ToLower(word)+" nodes") {
			t.Errorf("## Check 81: the paragraph saying %q does not count the seeds as %q, and the blocks make %d IAM writes (`?access_token=$AT` in the steps): an owner reading a smaller number looks for a seed the check never made, and one reading a larger number for a node the Restore does not remove", stem, word+" nodes", seeds)
		}
	}
}

// codeLines returns a block's command lines without its comments: a comment
// inside a block quotes the broken form to warn about it (`?key=$KEY here
// answers 401`), and a claim about what the block does is about its commands.
func codeLines(block string) string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// --- helpers ---------------------------------------------------------------

// stepText returns one numbered step of a section: from is the step's opening
// words, to the words of the step after it (or "" for the rest of the
// section).
func stepText(t *testing.T, section, from, to string) string {
	t.Helper()
	i := strings.Index(section, from)
	if i < 0 {
		t.Fatalf("the section has no step starting %q", from)
	}
	rest := section[i:]
	if to != "" {
		j := strings.Index(rest, to)
		if j < 0 {
			t.Fatalf("the section has no %q after %q", to, from)
		}
		rest = rest[:j]
	}
	return rest
}

// codeBlockAfter returns the rest of the fenced block whose own first comment
// line is marker: the marker is inside the block, so the block runs from it to
// the closing fence.
func codeBlockAfter(t *testing.T, section, marker string) string {
	t.Helper()
	i := strings.Index(section, marker)
	if i < 0 {
		t.Fatalf("the section has no %q", marker)
	}
	rest := section[i:]
	if end := strings.Index(rest, "\n```"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// registerRow returns the summary-table row of one check: the line of the
// document's register that starts "| <n> |". The register is where the owner
// reads a check's one-line account of itself, so a claim it repeats has to move
// with the section it points at.
func registerRow(t *testing.T, doc, n string) string {
	t.Helper()
	prefix := "| " + n + " |"
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("the register has no row for check %s", n)
	return ""
}

// forInLoops returns the quoted paths of every `for p in "..." ; do` loop of s,
// in order.
func forInLoops(s string) [][]string {
	var out [][]string
	for {
		i := strings.Index(s, "for p in ")
		if i < 0 {
			return out
		}
		s = s[i+len("for p in "):]
		j := strings.Index(s, "; do")
		if j < 0 {
			return out
		}
		out = append(out, quotedWords(s[:j]))
		s = s[j:]
	}
}

func quotedWords(s string) []string {
	var out []string
	for {
		i := strings.Index(s, `"`)
		if i < 0 {
			return out
		}
		s = s[i+1:]
		j := strings.Index(s, `"`)
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+1:]
	}
}

// goldenRules is the generated document as a tree.
func goldenRules(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../budget/rules/testdata/rules.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the golden rules: %v", err)
	}
	if doc.Rules == nil {
		t.Fatalf("the golden rules have no top-level \"rules\" node: this test walks the database's rules tree, which is what that node is")
	}
	return doc.Rules
}

// readableInGolden reports whether the golden rules grant a read at path or at
// any node above it **to a token carrying the `mu` claim**, which is what
// check 81's columns are about. A `.read` counts only when it is a condition
// string that reaches the `mu` predicate: TOKEN-only nodes (`config/limits`),
// the boolean false denials and `auth.token.fs`-bound run grants are all
// refused to the app's user, and the columns say so.
//
// A path segment of the checklist is a concrete value ($DAY, $NODE, $ALICE,
// $((DAY-1)) ...), so it is matched against a literal node first and against
// the rules' `$wildcard` children after -- which is the match RTDB itself
// makes.
func readableInGolden(root map[string]any, path string) bool {
	node := root
	if grantedAt(node) {
		return true
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		next, ok := node[seg]
		if !ok {
			for k, v := range node {
				if strings.HasPrefix(k, "$") {
					next, ok = v, true
					break
				}
			}
			if !ok {
				return false
			}
		}
		m, ok := next.(map[string]any)
		if !ok {
			return false
		}
		node = m
		if grantedAt(node) {
			return true
		}
	}
	return false
}

func grantedAt(node map[string]any) bool {
	cond, ok := node[".read"].(string)
	return ok && strings.Contains(cond, "auth.token.mu")
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("reading %q as a number: %v", s, err)
	}
	return n
}
