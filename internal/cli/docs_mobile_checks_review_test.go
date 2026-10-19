package cli

import (
	"encoding/json"
	"fmt"
	"os"
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

	if !strings.Contains(guard, "firebase_project") {
		t.Errorf("## Check 81: the guard block does not read budget.firebase_project out of the project's config: the Restore deletes the two Auth accounts through Identity Toolkit, whose project id is the Firebase project's, not the GCP project's")
	}
	if !strings.Contains(section, "projects/$FP/accounts:delete?key=$KEY") {
		t.Errorf("## Check 81: the Restore does not delete the two accounts under $FP (the Firebase project read from the config): Identity Toolkit's targetProjectId is the Firebase project id, and the GCP project id is not it in general")
	}
	if strings.Contains(section, "projects/$P/accounts:delete") {
		t.Errorf("## Check 81: the Restore deletes the two accounts under $P, which the guard hard-codes as the GCP project id fugaro-dev: on an installation whose Firebase project has another id the deletes answer 404 and the two accounts are left behind")
	}
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
