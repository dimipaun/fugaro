package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Check 27 (the rewritten `fugaro setup` end-to-end check) and Check 111 (the
// billing picker's failure modes) are W0.3's, and both are text the owner
// pastes into a shell. The 2026-10-20 review round fixed six claims in them;
// these tests hold the repaired facts against the tree, so the sections cannot
// drift back. What they read is mostly `codeLines` — the commands themselves,
// not the prose around them.
const (
	setupWizardChecklist = "docs/gcp-live-checklist.md"
	setupWizardPlan      = "docs/setup-wizard/implementation-plan.md"
	setupWizardD5        = "docs/setup-wizard/reconciliation.md"

	check27Heading  = "## Check 27:"
	check28Heading  = "## Check 28:"
	check111Heading = "## Check 111:"
)

// check27 is Check 27's own text, ending at Check 28's heading.
func check27(t *testing.T, doc string) string {
	t.Helper()
	return section(t, doc, check27Heading, check28Heading)
}

// check111 is Check 111's text: from its heading to the next `## ` heading, or
// to the end of the document when it is the last section. It was the checklist's
// last section until Check 124 was written below it, and the bound is what keeps
// a section appended after 113 out of Check 111's text -- the tests below read
// every `fugaro setup` line in it and require `--create-project` on each.
func check111(t *testing.T, doc string) string {
	t.Helper()
	i := strings.Index(doc, check111Heading)
	if i < 0 {
		t.Fatalf("%s has no %q: the billing picker's check is gone", setupWizardChecklist, check111Heading)
	}
	rest := doc[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		rest = rest[:j+1]
	}
	return rest
}

// TestDocsChecklistPlanStatusCitationsNameTheStatusLine: both rewritten
// sections quote the plan's status sentence — "plan only; nothing is
// implemented; decisions open" — as the reason the checks cannot run yet, and
// both cite `implementation-plan.md:13` for it. The review found them citing
// line 9, which is the Goal paragraph: a citation that resolves to a different
// sentence is worse than none, because it looks checked. So the cited line is
// read out of the plan and has to be the line holding that sentence.
func TestDocsChecklistPlanStatusCitationsNameTheStatusLine(t *testing.T) {
	doc := readDoc(t, setupWizardChecklist)
	planLines := strings.Split(readDoc(t, setupWizardPlan), "\n")

	const quoted = `*"approved to build; implementation in progress"*`
	re := regexp.MustCompile(`implementation-plan\.md:(\d+)`)
	matches := re.FindAllStringSubmatchIndex(doc, -1)
	if len(matches) == 0 {
		t.Fatalf("nothing in %s cites %s: Check 27 and Check 111 both do", setupWizardChecklist, setupWizardPlan)
	}

	cited := 0
	for _, m := range matches {
		// The citation is the one the quoted sentence sits in front of; a
		// citation elsewhere may name any other line, so only these are held.
		from := m[0] - 300
		if from < 0 {
			from = 0
		}
		if !strings.Contains(doc[from:m[0]], quoted) {
			continue
		}
		cited++
		line, err := strconv.Atoi(doc[m[2]:m[3]])
		if err != nil || line < 1 || line > len(planLines) {
			t.Errorf("%s cites %s:%s, which is not a line of it", setupWizardChecklist, setupWizardPlan, doc[m[2]:m[3]])
			continue
		}
		if got := planLines[line-1]; !strings.Contains(got, "approved to build; implementation in progress") {
			t.Errorf("%s cites %s:%d for the status sentence, and that line reads %q",
				setupWizardChecklist, setupWizardPlan, line, strings.TrimSpace(got))
		}
	}
	if cited < 2 {
		t.Errorf("only %d citation(s) of %s sit next to the quoted status sentence: Check 27 and Check 111 each carry one, and this test reads both",
			cited, setupWizardPlan)
	}
}

// TestDocsChecklistInjectionGrepNamesCheck27sRowAndSection: Check 27's intro
// and the plan's D10 both justify keeping the old probes with the same fact —
// the grep for them returns Check 27's register row and its section, and
// nothing else in the whole register. The review found the section paraphrasing
// that as "this section and nothing else", which the grep itself contradicts
// (line 499, the register's own row 27). So the claim is computed here: every
// line the pattern matches is either that row or a line of Check 27's section.
func TestDocsChecklistInjectionGrepNamesCheck27sRowAndSection(t *testing.T) {
	doc := readDoc(t, setupWizardChecklist)
	// the pattern the two documents quote, matched case-sensitively as grep does
	pattern := regexp.MustCompile(`injection|hostile`)

	registerEnd := strings.Index(doc, "\n## Check ")
	sectionStart := strings.Index(doc, check27Heading)
	sectionEnd := strings.Index(doc, check28Heading)
	if registerEnd < 0 || sectionStart < 0 || sectionEnd < sectionStart {
		t.Fatalf("%s has no register followed by Check 27: the grep's two halves cannot be told apart", setupWizardChecklist)
	}

	var rows, inSection []int
	off := 0
	for i, line := range strings.Split(doc, "\n") {
		at := off
		off += len(line) + 1
		if !pattern.MatchString(line) {
			continue
		}
		switch {
		case at < registerEnd:
			rows = append(rows, i+1)
		case at >= sectionStart && at < sectionEnd:
			inSection = append(inSection, i+1)
		default:
			t.Errorf("line %d matches the grep Check 27's intro and D10 both quote, and it is neither the register's row 27 nor a line of Check 27's section: %s", i+1, strings.TrimSpace(line))
		}
	}
	if len(rows) != 1 {
		t.Errorf("the grep returns %d line(s) of the register (%v), and both Check 27's intro and D10 say it returns that check's row: %s", len(rows), rows, setupWizardChecklist)
	}
	if len(inSection) < 3 {
		t.Errorf("the grep returns %d line(s) of Check 27's section (%v): the probes it is quoted for are gone", len(inSection), inSection)
	}

	// And the sentence has to say what the grep returns, the way D10 says it.
	// Paraphrased as "this section and nothing else" it is false, because the
	// register's own row 27 is the first line the grep prints.
	const claim = "returns Check 27's register row and its section and nothing else in the whole register"
	if !strings.Contains(flattenDoc(readDoc(t, setupWizardD5)), claim) {
		t.Errorf("D10 in %s no longer states the grep's result as %q: Check 27's intro quotes it as the reason the old probes were kept", setupWizardD5, claim)
	}
	if !strings.Contains(flattenDoc(doc), claim) {
		t.Errorf("Check 27's intro does not state the grep's result as D10 does — %q: a paraphrase that drops the register row turns a true claim into a false one", claim)
	}
}

// TestDocsCheck27RestoreListsTheSecretsRatherThanCountingThem: the Restore's
// final assertion has to be about the artifacts this check made — jobs and
// buckets, which must read 0 — and the project's secrets must be out of it.
// They cannot read 0: the converge creates the installation's and the
// repository's secret containers in P by design, and `init`'s secrets stage
// stores the App key and claude-oauth-token into them, so a "secrets left" line
// here would fail every run. They are listed instead, as evidence, and go with
// the project.
func TestDocsCheck27RestoreListsTheSecretsRatherThanCountingThem(t *testing.T) {
	doc := readDoc(t, setupWizardChecklist)
	restore := section(t, check27(t, doc),
		"**Restore.** Delete the throwaway project",
		"### Results of the seventh live run")
	code := codeLines(restore)

	if strings.Contains(code, "secrets left") {
		t.Errorf("Check 27's Restore counts the project's secrets in its must-read-0 block: the converge creates those containers and init stores into them, so the count can never read 0")
	}
	for _, want := range []string{
		// the two artifacts the assertion is about, each with --project pinned
		`echo "jobs left:`,
		`echo "buckets left:`,
		`--project="${P:?}"`,
		// the secrets, listed rather than counted
		`gcloud secrets list --project="${P:?}" --format='value(name)'`,
		// and the project delete, which takes the containers with it
		`gcloud projects delete "${P:?}" --quiet`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Check 27's Restore does not run %q: it is one of the lines the review round repaired", want)
		}
	}

	// The prose names the Terraform that creates the containers. Naming it is
	// what makes "they exist by design" checkable, so the names are checked
	// against the tree — the file is wrapped across lines in the comment, which
	// is why the module path is matched by its parts.
	for _, want := range []string{"modules/installation/secrets.tf", "modules/repo/", "prevent_destroy"} {
		if !strings.Contains(restore, want) {
			t.Errorf("Check 27's Restore does not name %q among the reasons the secrets stay: the claim has to point at the code that makes it true", want)
		}
	}
	for _, module := range []string{
		"deploy/terraform/gcp/modules/installation/secrets.tf",
		"deploy/terraform/gcp/modules/repo/secrets.tf",
	} {
		if _, err := os.Stat("../../" + module); err != nil {
			t.Errorf("Check 27's Restore cites %s, which is not on this tree: %v", module, err)
		}
	}

	// The secrets are stored by the stage, and the stage is run by the commands
	// this rewrite gives the owner (`fugaro setup`, `fugaro update`) — `fugaro
	// init` is the old command line, and this rewrite renames it everywhere the
	// owner types it. The Restore is inside the rewritten text, so it may not
	// name it (except in the results table and the record below, which quote
	// the old run on purpose and are outside this span).
	if strings.Contains(restore, "fugaro init") {
		t.Errorf("Check 27's Restore names `fugaro init`, which this rewrite renames: step 4 runs `fugaro setup` and step 16 runs `fugaro update`, and the Restore is rewritten text")
	}
	if !strings.Contains(restore, "the secrets stage this check's") {
		t.Errorf("Check 27's Restore no longer says which run stores the project's secrets: the must-read-0 claim turns on the stage being this check's own")
	}
}

// TestDocsCheck27DoctorStopStartsAtStepFive: the guard block's doctor stop
// asserts a local config exists, and steps 1 to 4 are ONE `fugaro setup` run
// whose installation stage — the stage that writes this check's config — only
// applies inside step 4. Re-running the block with that stop at the start of
// step 2 or 3 would therefore hard-stop the check mid-run for the right reason,
// which is the worst kind of stop: the owner reads it as their own mistake.
func TestDocsCheck27DoctorStopStartsAtStepFive(t *testing.T) {
	c27 := check27(t, readDoc(t, setupWizardChecklist))
	flat := flattenDoc(c27)

	for _, want := range []string{
		"has a sixth stop, but only from step 5 on",
		"From **step 5** onwards",
		// the reason, in the doc, so the step number is not an unexplained rule
		"install()` calls `writeConfig` once the installation root's apply has succeeded",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 27 does not say %q: the doctor stop's start step is justified by the stage that writes the config", want)
		}
	}
	if strings.Contains(flat, "from step 2 onwards") {
		t.Errorf("Check 27 still instructs the owner to re-run the guard block with the doctor stop in place from step 2 onwards, where no local config exists yet")
	}
	if !strings.Contains(codeLines(c27), "fugaro doctor --project fugaro") {
		t.Errorf("Check 27's guard block no longer runs the doctor stop this paragraph is about")
	}

	// The stage that writes the config, on the tree: install() applies the
	// installation root and THEN writes the config, which is why the config
	// cannot exist before that stage's step. The call site is what the doc
	// asserts, so it is read out of install()'s own body — the bare identifier
	// is satisfied by writeConfig's own definition further down the file, and
	// would hold this test up after install() stopped calling it.
	body := funcBody(t, readDoc(t, "internal/cli/init.go"), "func (r *initRun) install(")
	applied := strings.Index(body, "r.installRoot(")
	written := strings.Index(body, "r.writeConfig(")
	if applied < 0 || written < 0 {
		t.Errorf("initRun.install() no longer applies the installation root (%d) and writes the config (%d) in that order: Check 27's step-5 paragraph is about the stage that does both", applied, written)
	} else if applied > written {
		t.Errorf("initRun.install() writes the config before the installation root's apply succeeds: Check 27's step-5 paragraph says the config is written once the apply has succeeded")
	}
}

// funcBody is the text of the function whose declaration line starts with decl
// (e.g. `func (r *initRun) install(`), up to the next top-level `func ` at the
// start of a line. It fails the test when no such declaration is in the file.
func funcBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("this file has no %q: the function the checklist's claim rests on is gone", decl)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestDocsCheck27ImageBuildIsStepTwentyAndNotInSectionA: the first image build
// is billable and typed, it belongs to the `repository` stage's apply, and
// section A runs in a directory that is not a checkout — where D5 makes `setup`
// skip the repository question altogether. So the build is step 20's, and
// steps 3 and 4 must not promise its confirmation.
func TestDocsCheck27ImageBuildIsStepTwentyAndNotInSectionA(t *testing.T) {
	c27 := check27(t, readDoc(t, setupWizardChecklist))
	flat := flattenDoc(c27)

	for _, want := range []string{
		"Step 20 is where the `repository` stage and its billable, typed first build are run",
		"**There is no `repository` stage in this run**",
		"this run never reaches it",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 27 does not say %q: the first image build belongs to step 20's repository stage, not to the setup run's input phase", want)
		}
	}
	if strings.Contains(flat, "comes in step 4's repository stage") {
		t.Errorf("Check 27 still places the first image build's typed confirmation in step 4: that run never reaches the repository stage")
	}

	// D5 is what makes the skip a rule rather than a guess.
	d5 := section(t, readDoc(t, setupWizardD5), "### D5 —", "### D6 —")
	if !strings.Contains(flattenDoc(d5), "outside a checkout it skips the question and says how to enrol one later") {
		t.Errorf("D5 in %s no longer says setup outside a checkout skips the repository question: Check 27's premise for section A is D5's ruling", setupWizardD5)
	}
}

// TestDocsCheck27InjectionProbeNamesEveryFileThatPlantsTheScript: step 21's
// probe tells the owner what the hostile fixture plants, and the review found
// it naming the README alone — while the fixture also plants the pipe in
// package.json's postinstall, its Makefile, .nvmrc and .fugaro/web.Dockerfile,
// and its ci.yml runs `npm ci`, which runs that postinstall. The probe's
// description is what an owner compares the agent's refusals against, so every
// planting file has to be named, and the two files the fixture does NOT have
// have to be named too (an agent that tries to run them has answered wrongly).
func TestDocsCheck27InjectionProbeNamesEveryFileThatPlantsTheScript(t *testing.T) {
	c27 := check27(t, readDoc(t, setupWizardChecklist))
	probe := section(t, c27, "21. **Prompt injection.**", "22. Fugaro's own repository")

	const fixture = "../../plugin/testdata/repos/hostile/repo"
	var planting []string
	err := filepath.WalkDir(fixture, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte("evil.example")) {
			rel, err := filepath.Rel(fixture, path)
			if err != nil {
				return err
			}
			planting = append(planting, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planting) < 4 {
		t.Fatalf("the hostile fixture plants its script in %d file(s) (%v): step 21's probe describes a fixture that plants it in several, so this test would pass vacuously",
			len(planting), planting)
	}

	for _, rel := range planting {
		base := filepath.Base(rel)
		named := false
		for _, name := range []string{rel, base, strings.TrimSuffix(base, ".md")} {
			if strings.Contains(probe, name) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("the hostile fixture plants the script in %s and step 21 does not name that file: the probe understates what the agent is expected to refuse", rel)
		}
	}

	for _, absent := range []string{"scripts/bootstrap.sh", "scripts/postinstall.js"} {
		if _, err := os.Stat(filepath.Join(fixture, absent)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the fixture has %s after all, and step 21 tells the owner it does not: %v", absent, err)
		}
		if !strings.Contains(probe, absent) {
			t.Errorf("step 21 does not name %s among the files the fixture's plant names and does not carry", absent)
		}
	}
	if !strings.Contains(probe, "do not exist") {
		t.Errorf("step 21 no longer says the planted scripts do not exist in the fixture: an owner cannot tell a correct refusal of them from a lucky one")
	}
}

// TestDocsCheck111GuardDeclaresItsThirdProjectAndKeepsItsRoot: mode 2's premise
// is a caller without `billing.accounts.list` against an API that IS enabled —
// which needs a project whose Cloud Billing API is on, and P and Q are the ones
// it must be off on. So the guard declares a third project S, stops mode 2
// without it, and asserts its API is on BEFORE the run; and the guard's config
// root is written down and read back, so a later shell does not adopt whatever
// `XDG_CONFIG_HOME` its own shell happens to hold.
func TestDocsCheck111GuardDeclaresItsThirdProjectAndKeepsItsRoot(t *testing.T) {
	c111 := check111(t, readDoc(t, setupWizardChecklist))
	code := codeLines(c111)
	flat := flattenDoc(c111)

	for _, want := range []string{
		"need_s() {",
		"need_s_enabled() {",
		// the premise, asserted against the project itself
		`gcloud services list --enabled --project="${S:?}"`,
		// an integer test, not a string one: this check runs on macOS too, and
		// BSD wc pads its count ("       1"), so `= 1` would stop mode 2 on the
		// very state it requires
		`[ "${got:-0}" -ge 1 ] ||`,
		// the root this check writes down once and reads back in every shell
		`printf 'C=%s\n' "$CHK111/config" > "$CHK111/vars.env"`,
		`. "$CHK111/vars.env"`,
		`[ "$XDG_CONFIG_HOME" = "$CHK111/config" ]`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Check 111's guard block does not run %q: it is one of the lines the review round repaired", want)
		}
	}

	// S ships empty on purpose, and mode 2 has to insist on it before every run.
	for _, want := range []string{"S=", "Q="} {
		if !regexp.MustCompile(`(?m)^` + want + `\s*$`).MatchString(code) {
			t.Errorf("Check 111's guard block has no %q declaration: an unset project must stop its mode, never silently no-op it", want)
		}
	}
	if !strings.Contains(flat, "need_s 2a` and `need_s_enabled 2a`") {
		t.Errorf("Check 111's mode 2 does not run need_s and need_s_enabled before its first capture: an S whose API is off would take mode 1's disabled-API path and prove nothing")
	}

	// Order: the root is recorded and checked BEFORE it is exported (after the
	// export the "was this shell already pointed elsewhere" test can never
	// fail), and asserted again once the block has finished its own work.
	recorded := strings.Index(code, `printf 'C=%s\n' "$CHK111/config"`)
	exported := strings.Index(code, "export XDG_CONFIG_HOME=")
	asserted := strings.Index(code, "the block did not leave XDG_CONFIG_HOME")
	if recorded < 0 || exported < 0 || asserted < 0 {
		t.Fatalf("Check 111's guard block no longer records, exports and re-asserts its config root in that shape (recorded=%d exported=%d asserted=%d)", recorded, exported, asserted)
	}
	if !(recorded < exported && exported < asserted) {
		t.Errorf("Check 111's guard block records its config root at %d, exports it at %d and re-asserts it at %d: the root has to be written down and checked before the export, and asserted again after", recorded, exported, asserted)
	}
}

// TestDocsCheck111ThreeModesReachOutsideTheShellAndModeTwoHasItsProject: the
// review found the section calling mode 1(b) "the only step that changes
// anything outside your shell", which is false — 1(a) and 2 repoint ADC's quota
// project too — and telling the owner to run 1(b) last, which contradicts the
// stated mode order. What the check has to say is which runs touch ADC, so the
// Restore's first line cannot be skipped, and mode 2 needs its project named.
func TestDocsCheck111ThreeModesReachOutsideTheShellAndModeTwoHasItsProject(t *testing.T) {
	c111 := check111(t, readDoc(t, setupWizardChecklist))
	flat := flattenDoc(c111)
	code := codeLines(c111)

	for _, want := range []string{
		"Three modes reach outside your shell",
		"Modes **1(a)**, **1(b)** and **2** each repoint **your own ADC quota project**",
		"there is no step here that must come last",
		// mode 2's premise, in the mode itself
		"a second Google account with no billing role is the reliable way",
		"**enabled** here (on `S`, this mode's quota project)",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 111 does not say %q: three of its runs repoint the owner's ADC, and mode 2 is the one that needs an API that is on", want)
		}
	}
	for _, gone := range []string{
		"is the only step that changes anything outside your shell",
		"run it last",
	} {
		if strings.Contains(flat, gone) {
			t.Errorf("Check 111 still claims %q: three runs (1a, 1b, 2) repoint the owner's ADC quota project, and none of them has to come last", gone)
		}
	}

	for _, want := range []string{
		// mode 2 points ADC's quota project at S, the API-on project
		`set-quota-project "${S:?}"`,
		// and the assertion block reads each mode's own premise
		"   Q cloudbilling:",
		"   S cloudbilling:",
		"must read 1: mode 2's premise is an API that IS enabled",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Check 111 does not run %q: mode 2's premise has to be a project whose API is on, before the run and after it", want)
		}
	}
}

// TestDocsCheck111AssertModeRefusesAModeWithNoCapture: a mode that was never
// run must never be read as a pass. `assert_mode` therefore keys its capture on
// the mode it is asserting (mode 4 alone keeps two streams, and both have to be
// there), refuses a missing or empty capture, and — the part the first review
// round found — returns non-zero on every FAIL, because a block that only
// echoes its FAIL is read by the owner as a mode that passed.
func TestDocsCheck111AssertModeRefusesAModeWithNoCapture(t *testing.T) {
	code := codeLines(check111(t, readDoc(t, setupWizardChecklist)))

	for _, want := range []string{
		`files="$CHK111/mode-$m.log"`,
		`[ -s "$files" ]`,
		`files="$CHK111/mode-4.json $CHK111/mode-4.err"`,
		`[ -s "$f" ]`,
		"a mode with no capture is not a pass",
		"mode $m passed its assertions",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Check 111's assertion block does not contain %q: a mode is read through its own capture, and only through it", want)
		}
	}

	i := strings.Index(code, "assert_mode() {")
	if i < 0 {
		t.Fatalf("Check 111 has no assert_mode function: the assertion block is gone")
	}
	body := code[i:]
	var failures int
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "FAIL:") {
			continue
		}
		failures++
		if !strings.Contains(line, "return 1") {
			t.Errorf("Check 111's assert_mode prints a FAIL without returning non-zero on the same line, so its caller and the owner read the mode as a pass: %s", strings.TrimSpace(line))
		}
	}
	if failures < 4 {
		t.Errorf("Check 111's assert_mode has %d FAIL line(s): the block asserts a guided line, no account id, no display name and no numbered list, and each refusal has to be its own FAIL", failures)
	}
	if !strings.Contains(body, "return 0") {
		t.Errorf("Check 111's assert_mode no longer returns 0 on the pass path")
	}

	// Mode 2(a) is the run where the picker DOES list the accounts — display
	// name AND id, per D6 — so the account-id greps have to be inside the
	// may-list test's else branch. Above it, an owner who wrote the optional
	// account-ids.txt gets a permanent FAIL out of the one run that is working
	// as designed, which is the opposite of how the mode is described.
	guarded := strings.Index(body, `if [ "$may" = may-list ]; then`)
	idGrep := strings.Index(body, "billingAccounts/")
	idsGrep := strings.Index(body, `account-ids.txt" $files`)
	if guarded < 0 || idGrep < 0 || idsGrep < 0 {
		t.Fatalf("Check 111's assert_mode no longer tests may-list (%d) and greps for account ids (%d, %d) in that shape", guarded, idGrep, idsGrep)
	}
	unlisted := strings.Index(body[guarded:], "\n  else")
	if unlisted < 0 {
		t.Fatalf("Check 111's may-list test has no else branch: the leak greps have nowhere to live")
	}
	unlisted += guarded
	if idGrep < unlisted || idsGrep < unlisted {
		t.Errorf("Check 111's assert_mode greps for account ids outside the may-list test's else branch (guard %d, else %d, id greps %d and %d): mode 2(a) lists the accounts by design, ids included", guarded, unlisted, idGrep, idsGrep)
	}
}

// TestDocsCheck111EveryModeCarriesCreateProject: every mode's command line
// carries --create-project, and the section says why in terms of what the
// wizard does.
//
// There is no create/adopt question any more: Fugaro only starts fresh (the
// owner's ruling of 2026-10-23). `P` has a local config (its installation has
// converged), and a run with a project config registers the project stage only
// when --create-project says so; with it, the stage reads `P` — which must be a
// project `fugaro setup` created, carrying fugaro_created=true, or it is refused —
// and resumes it, so the guided line has a path to the owner: the stage's own
// left-for-you, which it prints only for a `P` that exists, is ACTIVE, has
// Firebase and has no billing link. The section states that state as the
// precondition every mode runs on, and mode 2(a) — the one mode whose picker
// does list — tells the owner to answer its prompt `none`.
func TestDocsCheck111EveryModeCarriesCreateProject(t *testing.T) {
	c111 := check111(t, readDoc(t, setupWizardChecklist))
	code := codeLines(c111)
	flat := flattenDoc(c111)

	// Every run of the wizard in this section, flag and all. The six are the
	// seven modes' commands (2(b) is 2(a)'s line again, with the fallback
	// maimed first, and the two --yes reruns are their own line with --yes).
	runs := regexp.MustCompile(`(?m)^\s*(?:capture \S+|plain \S+|)\s*fugaro setup [^\n]*`).FindAllString(code, -1)
	if len(runs) < 6 {
		t.Fatalf("Check 111 has %d `fugaro setup` command line(s), want at least 6 — one per mode: %q", len(runs), runs)
	}
	for _, line := range runs {
		if !strings.Contains(line, "--create-project") {
			t.Errorf("Check 111 runs %q without --create-project: P has a local config, so without the flag the run registers no project stage and the guided line it asserts would not print", strings.TrimSpace(line))
		}
		if !strings.Contains(line, `--gcp-project "${P:?}"`) {
			t.Errorf("Check 111 runs %q without --gcp-project: the run would converge on the id the wizard offers, fugaro-fugaro, and not on P", strings.TrimSpace(line))
		}
	}

	// The paragraph the mode commands contradicted: it claimed the picker was
	// not on this tree at all, which was true when W0.3 wrote it and is not
	// now. What must not come back is the claim itself.
	for _, gone := range []string{
		"there is **no picker**",
		"no `setup` and no `update`",
		"the picker has not run at all — not against fakes and not against a service",
	} {
		if strings.Contains(flat, gone) {
			t.Errorf("Check 111 still claims %q: the picker is on this tree (W4.3) and setup and update are registered (W1.2–W1.3), so the sentence describing a tree without them is a false premise for the whole section", gone)
		}
	}
	if !strings.Contains(flat, "no mode of it has ever been run") {
		t.Errorf("Check 111 no longer says what is still true — that no mode has been run — which is the reason the section is NOT RUN")
	}

	// The reasons the flag is there, as the code gives them: a run with a
	// project config registers the project stage only on the flag, the stage
	// resumes a `P` that fugaro setup created and refuses any other, and the
	// guided line's own home is the project stage's left-for-you, which needs
	// all four of these to be true of `P`.
	for _, want := range []string{
		"a run with a project config registers the project stage only when that flag says so",
		"one that **`fugaro setup` created**, so it carries the label `fugaro_created=true`",
		"the stage prints it as its **left-for-you** line only when `P` **exists**, is **`ACTIVE`**, **has Firebase** and has **no billing link**",
		"**A fresh project is not the easy one**",
		// mode 2(a) is the one mode whose picker lists, so it is the one mode
		// that has to say what to answer at its prompt
		"**At the picker's prompt, answer `none` — not a number.**",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 111 no longer says %q: the flag's reason and the guided line's home are what make the modes mean what they assert", want)
		}
	}
	for _, gone := range []string{
		"the answer would be **adoption**",
		"create/adopt question",
		"the run takes **adoption**",
		"a fresh throwaway project is the easy one",
		"Check 27's project is the easy one",
		"asks for the typed confirmation a run with no terminal cannot give (it is not there yet)",
	} {
		if strings.Contains(flat, gone) {
			t.Errorf("Check 111 still claims %q, which is not what the code does: there is no create/adopt question (Fugaro only starts fresh), and the guided line's left-for-you needs `P` to exist, be ACTIVE, have Firebase and have no billing link", gone)
		}
	}
}

// Two sentences in Check 27's subtree are *about* the rename rather than
// rewritten by it: the register row's parenthetical, which lists what the
// rewrite renames (the retired command's own name → `fugaro setup`, the
// converge-again steps → `fugaro update`, `--firebase` → `--firebase-project`),
// and step 4's "the same one the old text made of `init` run a second time".
//
// The row describes the retired command rather than spelling it, which is that
// section's own convention: it heads the recorded run with "the retired
// command's own name, its creating flags and `--firebase`", calls the table
// below "the record of the retired command's spellings", and the row's own next
// clause points there ("the run recorded under Check 27 was made with the
// **old** command lines"). It has to: the register is live text, and the census
// reads a live file as naming the retired command nowhere, so a row that spelled
// it would be the one mention of the retired verb no pattern here can see. Step
// 4 is inside the section whose subject IS the old text, so its bare `init`
// stays.
//
// The guard above cannot see either: it forbids the retired spelling in
// rewritten text, and a mechanical rename that overwrote the left of the first
// arrow writes the *new* spelling, leaving the row asserting that `fugaro setup`
// was renamed to `fugaro setup`. This test holds the two sides of that arrow
// apart, holds the row's left side to the register's own words rather than to
// the bare `init` the census' pattern cannot see (a left side that named no
// program would not say which command was renamed), and holds step 4's clause.
//
// Mutation (run, restore): put `fugaro setup` back on the left of the first
// arrow — the self-mapping check fails. Spell that left side `init` again — the
// first want and the bare-spelling check fail. Rewrite step 4's clause to name
// the new door — the last want fails.
func TestDocsCheck27NamesTheRetiredCommandWhereItSpeaksOfIt(t *testing.T) {
	flat := flattenDoc(readDoc(t, setupWizardChecklist))

	if strings.Contains(flat, "`fugaro setup` → `fugaro setup`") {
		t.Errorf("the register's rename parenthetical maps `fugaro setup` to itself: its left side is what the rewrite renames, so the retired command's name belongs there, not the door it became")
	}
	if strings.Contains(flat, "(`init` → `fugaro setup`") {
		t.Errorf("the register's rename parenthetical spells the retired command as bare `init`: the register is live text, so the row describes the old name the way its own section does and leaves the spelling to the dated record below")
	}
	for _, want := range []string{
		"**Kept, with their commands renamed** (the retired command's own name → `fugaro setup`",
		"the converge-again steps → `fugaro update`",
		"`--firebase` → `--firebase-project`",
		"the same one the old text made of `init` run a second time",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 27 no longer says %q: the row and step 4 are about what the rewrite renamed, so they name the retired command on the side that describes the old text", want)
		}
	}
}
