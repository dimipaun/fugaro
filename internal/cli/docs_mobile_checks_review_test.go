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
	// The columns' own counts are held by
	// TestDocsCheck81Step6CountsItsReadColumns, which reads the two loops out of
	// the same step and compares the numbers to them: this test's subject is the
	// golden rules, and a copy of the count here would be a second place for it
	// to rot.
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
	if len(found) != 6 {
		t.Fatalf("check 81 step 8 carries %d `grep -o '<pattern>' ... # <n>:` lines, want the six the step is about (mailbox, the USER predicate, $other's guard, D166's device allowlist, D165's pairing subtree, and the mac the messages carry): the test reads the patterns and their counts from the step, and a line it cannot parse is a line it does not hold to the golden", len(found))
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

	// The summary also promised "at least 1 for each in any document that
	// carries the mailbox", and that is false of the one older document this
	// check exists to catch: a generation-2 document carries the mailbox and
	// answers 0 for the last three patterns, which generation 3 added -- the
	// paragraph's own next sentence says so. A threshold the older document
	// fails would have the owner read a correct generation-2 deployment as a
	// fingerprint the check never asked for (the review of PR #580's finding).
	if strings.Contains(step, "at least 1 for each") {
		t.Errorf("## Check 81 step 8: the counts are promised as \"at least 1 for each in any document that carries the mailbox\", and a generation-2 document -- which carries the mailbox and is the older document this check is written to catch -- has **0** for each of the last three patterns (`config/admin/devices`, `\"pairing\"` and `\"mac\"`, all three of them generation 3's own): the threshold makes a correct older deployment read as a fingerprint the check never asked for")
	}
	if !strings.Contains(step, "**0** for each of the last three") {
		t.Errorf("## Check 81 step 8: the counts paragraph does not say a generation-2 document answers **0** for the last three patterns: the sentence after it already says the last three are generation 3's own, so a FACT line that promises them in any document with a mailbox contradicts the check's own text")
	}

	// The step prints the six counts a second time, as one list ("The six
	// counts: **1, 12, 13, 12, 1 and 3**"), and the list is the same six
	// numbers in the same order. Nothing held the two together, so a template
	// change could move the lines and leave the summary counting the old
	// document.
	nums := make([]string, 0, len(found))
	for _, m := range found {
		nums = append(nums, m[2])
	}
	want := fmt.Sprintf("The six counts: **%s**", strings.Join(nums[:len(nums)-1], ", ")+" and "+nums[len(nums)-1])
	if !strings.Contains(step, want) {
		t.Errorf("check 81 step 8's summary of its counts is not %q: `grep -o` lines above are the same six numbers in that order, and the summary is what the owner pastes back -- a list that is not theirs makes the deployed document's fingerprint a number the check itself no longer prints", want)
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

// TestDocsCheck82PlanCitesTheConstructsThatPlanTheActLines: check 82 step 3
// tells the owner which of the two act lines `DB.Plan` plans first, and it used
// to do that with two line numbers into internal/infra/firebasedb.go -- the
// rules action at `:251` and "the marker's `PutIfMatch`" at `:276`. Both halves
// were the wrong kind of claim about a file that is read rather than
// remembered: `:251` was a bare line number, right the day it was written and
// stale the first time anything above it moved, and `:276` was not the
// construct it named at all -- it is the marker act line's own format string,
// while the `PutIfMatch` call is the shared helper inside `func (d *DB) put`,
// below the marker's plan.
// The check now names the constructs instead, and this test holds each named
// construct to the file, so a rename fails here rather than leaving the check
// citing a call that is no longer in the plan.
func TestDocsCheck82PlanCitesTheConstructsThatPlanTheActLines(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	src := readSrc(t, "../infra/firebasedb.go")
	for _, c := range []struct{ construct, what string }{
		{`Path: "rules"`, "the rules action"},
		{"d.put(projectpolicy.PathRulesMarker()", "the marker's plan call"},
	} {
		if !strings.Contains(section, c.construct) {
			t.Errorf("## Check 82: does not name %s (`%s`) among the constructs of `DB.Plan` that plan the two act lines: the check tells the owner which of the two comes first, and a bare line number into internal/infra/firebasedb.go is a claim about a file that moves under it -- cite the construct", c.what, c.construct)
		}
		if !strings.Contains(src, c.construct) {
			t.Errorf("internal/infra/firebasedb.go does not carry `%s`, which check 82 names as %s: a check that cites a construct the plan no longer has sends the owner looking for something that is not there", c.construct, c.what)
		}
	}
	if m := regexp.MustCompile("`:[0-9]+`").FindString(section); m != "" {
		t.Errorf("## Check 82: cites %s, a bare line number into a source file: the line a construct sits on moves with every edit above it, and the citation this replaces was wrong about the construct as well as fragile about the position -- cite the symbol or the construct, which this test holds to the file", m)
	}
}

// TestDocsCheck82RulesDeployComesBeforeTheMarker: check 82's preamble said the
// generated document is put on the database "as the last write of its database
// step", and that is the reverse of what the step does. `DB.Plan` appends the
// rules action and then plans the marker with its `d.put(projectpolicy.PathRulesMarker(),
// ...)` call (internal/infra/firebasedb.go),
// and `Apply` writes them in that order -- which is what makes the marker's
// presence a statement about the document underneath it, the property step 3
// and the "what a step failed before the deploy leaves behind" paragraph are
// both written against. The review of PR #580 found the preamble teaching the
// reverse. The order is read out of the file here, so a plan that changed it
// fails this test until the sentence is rewritten with it.
func TestDocsCheck82RulesDeployComesBeforeTheMarker(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	if strings.Contains(section, "last write of its database step") {
		t.Errorf("## Check 82: the preamble still says the rules deploy is the last write of the database step: the marker is, and the sentence would have the owner read a missing marker as a deploy that landed")
	}
	for _, want := range []string{
		"the write **immediately before the generation marker**",
		"the marker is written **after** the rules",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("## Check 82: the preamble does not say %q: `DB.Plan` appends the rules action and then plans the marker with its `d.put(projectpolicy.PathRulesMarker(), ...)` call, and the marker being last is what makes its presence a statement about the deployed document", want)
		}
	}

	// The support the sentence cites: the parenthesis after "never before".
	// docs/gcp-setup.md's "Deploying the rules is an owner step" states the
	// step's order the other way round -- it called the rules the last write of
	// the database step -- so it cannot be the support for this one; the
	// review of PR #580 found the preamble citing it for exactly that, and an
	// owner who followed the citation landed on the inverted order. The
	// paragraph now names the code for the order and warns about the page in
	// its own words. The page itself is corrected in this round and pinned by
	// TestGcpSetupDatabaseStepOrderIsTheCodes, so this clause is now about the
	// citation rather than about a page that disagrees: the order belongs in
	// the check, next to the steps it governs.
	_, cited, ok := strings.Cut(section, "the marker is written **after** the rules, never before (")
	if !ok {
		t.Fatalf("## Check 82: the preamble no longer carries the sentence stating that the marker is written after the rules: this test reads the support it cites from it, and the check needs the sentence")
	}
	support, _, ok := strings.Cut(cited, ")")
	if !ok {
		t.Fatalf("## Check 82: the sentence stating the order has no closing parenthesis after it, so the support it cites cannot be read")
	}
	if strings.Contains(support, "docs/gcp-setup.md") {
		t.Errorf("## Check 82: the order sentence's support cites docs/gcp-setup.md:\n%s\nits \"Deploying the rules is an owner step\" paragraph calls the rules the database step's last write, where the marker is, so it supports the opposite of the claim it is cited for and the owner who follows it lands on the inverted order", support)
	}

	// The order the sentence describes, read out of the file it names.
	src := readSrc(t, "../infra/firebasedb.go")
	rules := strings.Index(src, `Path: "rules"`)
	marker := strings.Index(src, "d.put(projectpolicy.PathRulesMarker()")
	if rules < 0 || marker < 0 {
		t.Fatalf("internal/infra/firebasedb.go no longer plans the rules action and the marker (got %d and %d): check 82's account of the deploy order is written against them", rules, marker)
	}
	if rules > marker {
		t.Errorf("DB.Plan plans the marker write (%d) before the rules action (%d): check 82's preamble says the marker is written after the rules, and this test holds the sentence to the code -- if the order really changed, rewrite the sentence with it", marker, rules)
	}
}

// TestDocsCheck82SkewStatesAreTheCodes: check 82 step 1 said which
// `policy-rules-skew` line doctor prints "is a function of the marker and of
// nothing else", enumerated "five states", and told the owner to record and
// **stop** for any line that was "neither of the two above and not the 'newer
// Fugaro' warning", on the ground that `DB.Plan` refuses it. `doctorRulesSkew`
// has a sixth state, and the marker does not decide it: with a marker equal to
// `RulesVersion` doctor reads the deployed document, compares it with the one
// this binary generates through `infra.SameJSON`, and prints no line when they
// match, `rulesSkewText` when they differ, and an info note when either read
// fails. `rulesSkewText` is what a console hand-edit of `/.settings/rules.json`
// leaves behind; `DB.Plan` does not refuse it, and its remedy is the deploy this
// check exists to run -- an owner told to stop would abandon the check on the
// very state it is written for. The states are read out of the source here, and
// the section has to carry the sixth and to tell the two stop-states apart.
func TestDocsCheck82SkewStatesAreTheCodes(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	// The five states the marker alone decides, as doctorRulesSkew decides them.
	src := readSrc(t, "doctor.go")
	for _, want := range []string{
		"if err != nil || !found {",
		"MarkerHandEdited(gen)",
		"gen > projectpolicy.RulesVersion",
		"rulesMarkerPrePolicyText(gen)",
		"rulesMarkerOlderText(gen)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("internal/cli/doctor.go's doctorRulesSkew no longer carries `%s`: check 82 step 1's account of the marker-decided states is written against these branches, so read the function again before changing the check", want)
		}
	}
	// The sixth: the marker at this generation, decided by the deployed document.
	if !strings.Contains(src, "infra.SameJSON(live, want)") || !strings.Contains(src, "rulesSkewText()") {
		t.Fatalf("internal/cli/doctor.go's doctorRulesSkew no longer compares the deployed document with the generated one (`infra.SameJSON(live, want)`) and prints `rulesSkewText()` when they differ: that pair is check 82 step 1's sixth state, and the check's account of it is written against them")
	}

	for _, want := range []string{"sixth state", "rulesSkewText", "step 1's marker tells them apart", "**confirm step 3**"} {
		if !strings.Contains(section, want) {
			t.Errorf("## Check 82: does not carry %q: a marker at this release's generation is the one state the marker does not decide (doctorRulesSkew compares the deployed document with the generated one and prints rulesSkewText for a difference), and that state's remedy is step 3's deploy -- the follow-up has to send the owner to the deploy, not to a stop, for the line that names it", want)
		}
	}
	if strings.Contains(section, "is a function of the marker and of nothing else") {
		t.Errorf("## Check 82: step 1 still says which `policy-rules-skew` line doctor prints \"is a function of the marker and of nothing else\": with a marker of 3 the line is decided by the deployed document -- no line when it is `infra.SameJSON`-equal to what this binary generates, `rulesSkewText` when it is not -- so the marker alone does not decide it")
	}
}

// TestDocsMarkerOrderIsNotAFailClosedClaim: checks 82 and docs/gcp-setup.md's
// rules-deploy bullet both said a rules deploy that fails "leaves no marker and a
// runner reading it fails closed", and check 82 step 7 said that while a
// hand-written marker above this binary's generation stands "every runner that
// reads the marker fails closed". Neither is what the code does. An absent marker
// is generation 0, the legacy path: `projectpolicy`'s `rulesGeneration` answers
// "not found" (internal/projectpolicy/read.go), the runner keeps the ceiling its
// job environment was deployed with, and the launcher's allow-list and
// release-skew gates return no refusal for it (internal/cli/run_allowlist.go). A
// marker *above* `RulesVersion` reads as deployed, so the policy published under
// it is read and enforced -- internal/cli/doctor.go's own words. What stops a run
// is a policy node that exists and cannot be read or decoded:
// internal/runner/projectpolicy.go's `unreadablePolicy`, for a denied read, a node
// that is not this Fugaro's policy, or a hand-edited marker. Both sentences are
// read here, and the code facts they are written against with them.
func TestDocsMarkerOrderIsNotAFailClosedClaim(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	// The code first: the legacy path for an absent marker, the marker-above
	// reading, and the refusal that is fail-closed.
	for _, c := range []struct{ file, want, why string }{
		{"../projectpolicy/read.go", "is generation 0: the pre-policy rules",
			"projectpolicy's rulesGeneration reads an absent marker as the pre-policy rules, under which no policy node is read and nothing is refused"},
		{"doctor.go", "a marker at or above this Fugaro's generation reads as deployed",
			"doctor's own words for the state check 82 step 7 called fail-closed: a marker above this generation reads the policy under it"},
		{"../runner/projectpolicy.go", "func (r *run) unreadablePolicy(err error) error",
			"the runner's fail-closed path is a policy node that exists and cannot be read or decoded, not a marker that is not there"},
	} {
		if !strings.Contains(readSrc(t, c.file), c.want) {
			t.Fatalf("%s no longer carries %q: %s", c.file, c.want, c.why)
		}
	}

	for _, c := range []struct{ text, where string }{
		{section, "## Check 82:"},
		{gcpSetupRulesBullet(t), "docs/gcp-setup.md's rules-deploy bullet"},
	} {
		if strings.Contains(c.text, "fails closed") || strings.Contains(c.text, "fail closed") {
			t.Errorf("%s claims a runner fails closed on a marker that is absent or above this binary's generation: an absent marker is generation 0, the legacy path (the policy nodes are simply not read, and nothing is refused), and a marker above this generation reads as deployed, so the policy under it is read and enforced -- what fails closed is a policy node that exists and cannot be read or decoded (`internal/runner/projectpolicy.go`'s `unreadablePolicy`)", c.where)
		}
	}
}

// gcpSetupRulesBullet is the "Deploying the rules is an owner step" bullet of
// docs/gcp-setup.md, the page check 82's preamble is held to.
func gcpSetupRulesBullet(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Deploying the rules is an owner step") {
			return line
		}
	}
	t.Fatalf("docs/gcp-setup.md has no line saying \"Deploying the rules is an owner step\": this test holds that bullet's account of the marker to the code")
	return ""
}

// TestDocsCheck81StepsNameTheIdsTheyInherit: check 81's steps mint ids into
// shell variables of their own (`MSG` in step 2, `OUT` in step 4, `OUT2` in step
// 5) and later steps read them. Every step that reads an id it does not set has
// to name it in its prerequisite header -- the parenthesis that lists what the
// step needs -- because the header is what an owner runs the step from, and with
// `set -u` on a block that reads an unset variable aborts at its first use, after
// the statuses recorded above it. Step 5 names "`$OUT` from step 4" and step 2
// names "tokens from step 1"; step 4 read `$MSG`, the message step 2 wrote, and
// named only "tokens" -- the second review of PR #580's finding. The ids and the
// step that sets each are read out of the blocks here.
func TestDocsCheck81StepsNameTheIdsTheyInherit(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	re := regexp.MustCompile(`(?m)^(\d+)\. \*\*`)
	at := re.FindAllStringSubmatchIndex(section, -1)
	if len(at) < 2 {
		t.Fatalf("## Check 81: carries %d numbered steps, so this test cannot read a step's header and block", len(at))
	}
	type step struct{ n, header, text string }
	var steps []step
	for i, loc := range at {
		end := len(section)
		if i+1 < len(at) {
			end = at[i+1][0]
		}
		text := section[loc[0]:end]
		header, _, _ := strings.Cut(text, "\n")
		steps = append(steps, step{n: section[loc[2]:loc[3]], header: header, text: text})
	}
	// Only the fenced blocks are commands the owner runs: a step's FACT
	// paragraph names the ids of earlier steps in prose ("step 2's `$MSG` is
	// taken"), and prose is not a block that can abort on an unset variable.
	fence := regexp.MustCompile("(?s)```[a-z]*\\n(.*?)```")
	cmds := map[string]string{}
	for _, st := range steps {
		var b strings.Builder
		for _, m := range fence.FindAllStringSubmatch(st.text, -1) {
			b.WriteString(m[1])
		}
		cmds[st.n] = b.String()
	}
	setters := map[string]string{}
	for _, st := range steps {
		for _, id := range []string{"MSG", "OUT", "OUT2"} {
			if strings.Contains(cmds[st.n], id+"=") && setters[id] == "" {
				setters[id] = st.n
			}
		}
	}
	if len(setters) == 0 {
		t.Fatalf("## Check 81: no step's block assigns an id this test knows (`MSG=`, `OUT=` or `OUT2=`): the cross-step ids are what the headers have to name, so read the steps again before changing the check")
	}
	for _, st := range steps {
		for id, setter := range setters {
			if setter == st.n || !strings.Contains(cmds[st.n], "$"+id) {
				continue
			}
			want := "`$" + id + "` from step " + setter
			if !strings.Contains(st.header, want) {
				t.Errorf("## Check 81 step %s: the block reads `$%s`, which step %s sets, and the step's prerequisite header does not say %q: the header is the list the step is run from, and with `set -u` on an id the block does not set aborts it at the first use, after the statuses it has already recorded", st.n, id, setter, want)
			}
		}
	}
}

// TestGcpSetupDatabaseStepOrderIsTheCodes: the third finding of the PR #580
// review, on the page check 82's preamble had cited for the deploy order.
// docs/gcp-setup.md's "Deploying the rules is an owner step" bullet called the
// rules the database step's **last write**, where the marker is, and said a
// database whose rules already match "reports `No changes`" -- a line
// `applyRoot` prints for an empty Terraform root, not this step's sentence.
// Both claims were the reverse of the code, and an owner who read the page
// before running the step expected a plan without a marker write and a settled
// database that prints a line it never prints.
//
// The bullet is now written from the code, and this test holds it there: the
// order it states is read against `DB.Plan` in internal/infra/firebasedb.go,
// the two lines it distinguishes against internal/cli/init_firebase.go's
// `deployDatabase` (the settled line and its `len(acts) == 0 && !idp.missing &&
// !fs.Pending()` guard) and `applyRoot`'s "No changes", and the act line it
// quotes against the format `DB.Plan` prints.
func TestGcpSetupDatabaseStepOrderIsTheCodes(t *testing.T) {
	const path = "../../docs/gcp-setup.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bullet string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Deploying the rules is an owner step") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("%s has no line saying \"Deploying the rules is an owner step\": this test holds that bullet's account of the database step to the code, so it cannot check a page that no longer carries it", path)
	}

	// The order, as the plan makes it.
	src := readSrc(t, "../infra/firebasedb.go")
	rules := strings.Index(src, `Path: "rules"`)
	marker := strings.Index(src, "d.put(projectpolicy.PathRulesMarker()")
	if rules < 0 || marker < 0 {
		t.Fatalf("internal/infra/firebasedb.go no longer plans the rules action and the marker (got %d and %d): the page's account of the order is written against them", rules, marker)
	}
	if rules > marker {
		t.Errorf("DB.Plan plans the marker write (%d) before the rules action (%d): docs/gcp-setup.md says the rules go first and the marker after, and this test holds the sentence to the code -- if the order really changed, rewrite the page with it", marker, rules)
	}
	for _, want := range []string{
		"then the rules, and **then the generation marker**",
		"the marker is written **after** the rules and never before",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("%s: the \"Deploying the rules is an owner step\" bullet does not say %q: `DB.Plan` appends the rules action and then plans the marker with its `d.put(projectpolicy.PathRulesMarker(), ...)` call, and the marker being last is what makes its presence a statement about the document under it -- the page an owner reads before the step has to say the same thing", path, want)
		}
	}
	if strings.Contains(bullet, "last write") {
		t.Errorf("%s: the bullet calls a write the database step's \"last write\": the marker is the last one, and a page that names the rules there has the owner read a missing marker as a deploy that landed", path)
	}

	// The two lines the bullet distinguishes: the stage's "No changes" and the
	// database step's settled sentence.
	const settled = "The database already has the rules, mark, project name and mode."
	if !strings.Contains(readSrc(t, "init_firebase.go"), settled) {
		t.Errorf("internal/cli/init_firebase.go no longer prints %q: the page quotes it as what a database step with nothing pending prints", settled)
	}
	if !strings.Contains(bullet, settled) {
		t.Errorf("%s: the bullet does not quote %q, the line a database step with nothing pending prints: it is the line the reader is told to expect, and the page's account of the step is what they compare their run against", path, settled)
	}
	if !strings.Contains(bullet, "`No changes` is `fugaro setup`'s line for a stage with nothing to do, not this step's") {
		t.Errorf("%s: the bullet does not say that `No changes` is a stage's line rather than the database step's (internal/cli's `init_firebase.go` prints it for a Terraform root with an empty plan, and the database step's own no-op sentence is %q): the sentence it replaced said a database whose rules match \"reports `No changes`\", which no run of the step prints", path, settled)
	}
	if !strings.Contains(bullet, "**no rules line**") || !strings.Contains(bullet, "the **marker** write") {
		t.Errorf("%s: the bullet does not say that a database whose rules already match prints no rules line but still plans the marker write when the marker is absent or holds another generation (`DB.Plan` plans the marker whenever `!markerFound || markerGen != RulesVersion`): that run is the one an owner of a pre-generation-3 installation is about to see, and the page has to describe it", path)
	}

	// The act line the bullet tells the reader to look for, as the plan prints
	// it.
	if !strings.Contains(src, "deploys the security rules (%d bytes, generated)") {
		t.Errorf("internal/infra/firebasedb.go no longer prints the rules act line as \"deploys the security rules (%%d bytes, generated)\": docs/gcp-setup.md tells the owner to confirm the step when it lists that line")
	}
	if !strings.Contains(bullet, "deploys the security rules (… bytes, generated)") {
		t.Errorf("%s: the bullet does not name the act line the reader confirms the step on (\"deploys the security rules (… bytes, generated)\", `DB.Plan`'s own wording with the byte count elided): the page tells them what to look for, so it has to be the line the CLI prints", path)
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
// because P3.1 had not landed when the check was written. Both files have
// landed -- P3.1's `mobile/app.json` and P4.4's `mobile/eas.json` and
// `mobile/app.config.js` are all in this checkout, so the guard's file tests
// pass and what it stops on today is the store identity itself: an app.json
// that does not carry it, and the two variables that are not in the shell yet
// -- the ids a store build cannot be submitted without. The old guard read
// `d["ios"]["bundleIdentifier"]` and would have died with a Python traceback
// instead of saying so.
//
// Two more ways to die with a traceback are pinned here for the same reason,
// because the guard's contract is a sentence and not a stack: it reads the
// identity by running `app.config.js` through `node`, so it tests for the
// interpreter (its preamble names it) the way it tests for `eas`; and it parses
// what node printed, so stdout that is not the config is a stop with a reason.
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
		t.Errorf("## Check 89: the guard does not test for mobile/eas.json, the file the build and submit profiles are read from (P4.4's, and in this checkout): without the test, a checkout that predates it would build against no profiles at all, and the stop would come from eas-cli's own error rather than from this guard")
	}
	if strings.Contains(guard, `d["ios"]["bundleIdentifier"]`) {
		t.Errorf("## Check 89: the guard reads ios.bundleIdentifier straight out of app.json, and the file has no such key yet: the guard dies with a Python traceback instead of stopping with the reason a store build cannot be submitted")
	}
	for _, want := range []string{"bundleIdentifier", "android", "sys.exit("} {
		if !strings.Contains(guard, want) {
			t.Errorf("## Check 89: the guard does not read/carry %q: it must name the store identity it needs and stop with a sentence when the app does not have it", want)
		}
	}

	// The guard runs the app's dynamic config through `node`, and the preamble
	// says so ("It needs `node` on `PATH` for that last read"): so it has to
	// test for the interpreter the way it tests for `eas`, or a shell without
	// node gets a Python FileNotFoundError -- a traceback out of the block whose
	// whole job is to stop with a sentence. It is the same defect as the
	// `d["ios"]["bundleIdentifier"]` read above, one layer out. Mutation: drop
	// the `command -v node` line and this fails.
	if !strings.Contains(section, "It needs `node` on `PATH`") {
		t.Errorf("## Check 89: the preamble no longer names `node` as a requirement, so this test's marker for it is stale: the guard reads the store identity with node, and the preamble is where the owner is told")
	}
	if !strings.Contains(guard, "command -v node") {
		t.Errorf("## Check 89: the guard tests that `eas` is on PATH but not `node`, although its own preamble says it needs both: with node missing, the `subprocess.run([\"node\", ...])` read of the store identity raises FileNotFoundError and the block ends in a Python traceback instead of the sentence it promises")
	}

	// ... and what node printed is parsed with a guard on it: a node that put a
	// warning line on stdout, printed an empty document, or printed `null` is a
	// stop with a sentence rather than an uncaught JSONDecodeError or an
	// AttributeError out of `e.get(...)`. Mutation: drop the `try`/`except
	// ValueError` (or the `isinstance` check) and this fails.
	for _, want := range []string{"except ValueError", `if not isinstance(e, dict):`, `isinstance(e.get("ios"), dict)`} {
		if !strings.Contains(guard, want) {
			t.Errorf("## Check 89: the guard parses node's stdout with no %q: whatever node printed that is not the config (a warning line, an empty document, `null`) reaches the owner as a Python traceback instead of the sentence this guard promises", want)
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
// JSON, **formatting ignored**. The checks once said the read-back was 42687
// bytes, and that is `rules.Generate()`'s own size -- the number it produced
// then, and nothing the database owes them: a document the API
// stored with other whitespace is the same document at another byte count, and
// a check that calls it a failure is a check that fails a correct deployment.
// The generator's number is right and stays -- it is what the plan's rules line
// prints and what `testdata/rules.golden.json` holds -- so this test holds the
// two apart instead of dropping it, and it reads the size it must not find
// attached to the read-back out of the golden rather than writing it down, so
// the guard below catches the claim whatever the template grows to.
func TestDocsReadBackIsComparedAsJSONNotBytes(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	if !strings.Contains(readSrc(t, "../infra/firebasedb.go"), "if !SameJSON(live, gen) {") {
		t.Fatalf("internal/infra/firebasedb.go no longer decides the rules action with `if !SameJSON(live, gen) {`: the two checks' account of what a correct deployment is is written against that comparison, so read the code again before changing them")
	}

	// The number a clause about the read-back may not name: the generated
	// document's own size, read from the golden the checks quote and formatted
	// the way they print it. A hard-coded 42687 here would stop catching the
	// claim the moment the template grew, which is exactly what generation 3 did.
	golden, err := os.ReadFile("../budget/rules/testdata/rules.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	genSize := fmt.Sprint(len(golden))

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
				if strings.Contains(clause, genSize) && mentionsReadBack(clause) {
					t.Errorf("%s: the same sentence claims a size for the document read back and names %s:\n%s\n%s is `rules.Generate()`'s own size (internal/budget/rules/testdata/rules.golden.json, %d bytes), not the read-back document's -- DB.Plan calls the two the same document when SameJSON says so, formatting ignored, so the byte count of what the API stored is the API's to choose and a check that requires this number of it fails a correct deployment", heading, genSize, clause, genSize, len(golden))
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
	if strings.Contains(mobileBlockCommandLines(restore), "?key=$KEY") {
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
// read through TOK as well, and no form of the helper discards a body. The
// same review found step 2's positive write -- a bare `curl -sS -X PUT` whose
// FACT line asks for "the response body ... and the status, 200" -- so step 2
// goes through TOK and the test holds every `?auth=` call in the section to
// it, not only the steps whose FACT lines it reads.
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
		{"2. **The fence's positive half", "3. **The fence's negative half"},
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
	// Every rules-facing call in the section goes through TOK, not only the
	// steps whose FACT lines were read above: a call carrying `?auth=` is
	// answered by the deployed rules, and the check reads each of them as a
	// status **with** the body it answered with. Step 2's first write was a
	// bare `curl -sS -X PUT` -- the body, no status -- while its FACT line
	// asks for "the response body ... and the status, 200"; the review of
	// PR #580 found it, and the loop above could not see it, because step 2's
	// later lines are TOK calls and the step therefore did call TOK. The
	// helper block's own comment states the rule ("Every call this check
	// makes with `?auth=` ... goes through it").
	for _, st := range steps {
		fenced := false
		for _, line := range strings.Split(stepText(t, section, st.from, st.to), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fenced = !fenced
				continue
			}
			if !fenced || !strings.Contains(line, "?auth=$") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "TOK ") || strings.Contains(line, "$(TOK ") {
				continue
			}
			t.Errorf("check 81 %s: this rules-facing call does not go through `TOK`:\n%s\nits status is not printed, and the step's FACT lines read every one of these calls as a status with the body it answered with", st.from, strings.TrimSpace(line))
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

	// The paragraph above the guard says where each credential lives, and it has
	// to match the block below it: the key and the ID tokens are shell
	// variables, and only the minted custom tokens reach $W. It said the key
	// and every ID token "live in /tmp/chk81", which is where neither is -- an
	// owner who believes it goes looking in $W for a key that was never written
	// there, and reads the mode 700 as protecting it.
	para := ""
	for _, p := range paragraphsOf(section) {
		if strings.Contains(p, "is the project's restricted web API key") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatalf("## Check 81: no paragraph says what $KEY is and where the credentials live: this test holds that account to the guard's own $W, and the guard is what writes the files")
	}
	if strings.Contains(para, "live in `/tmp/chk81`") {
		t.Errorf("## Check 81: the paragraph says the web API key and the ID tokens live in `/tmp/chk81`, and nothing writes either there: $KEY is read out of the project's config and the ID tokens are the output of `signin`, so both are shell variables, and only the minted custom tokens go to $W/jwt-$uid.txt -- the sentence is what tells the owner what the 700 directory is protecting")
	}
	for _, want := range []string{"shell's variables", "$W/jwt-$uid.txt"} {
		if !strings.Contains(para, want) {
			t.Errorf("## Check 81: the paragraph does not say %q: it is the section's account of where the credentials are, and the guard below it writes the minted tokens to $W and nothing else to a file", want)
		}
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

// TestDocsCheck81Step5CountsItsTokLines: steps 3, 4, 5 and 7 state how many
// statuses their blocks produce, and the counts are the blocks' own `TOK`
// lines. Step 5's FACT line also points at two of its lines by position ("the
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

	// Step 3: fourteen refusals, counted by `TOK` line. Its FACT line said
	// "these eleven refusals" over a block of ten, and nothing read it --
	// steps 4, 5 and 7 were counted here and step 3 was not. The four lines
	// generation 3's device conjunct added (the allowlist write and the three
	// `$BO` refusals) are `TOK` lines like the rest, so the count moved with
	// them.
	step3 := stepText(t, section, "3. **The fence's negative half", "4. **The user reads")
	toks3 := tokLines(step3, "ID2=$(printf 'chk81-%s'")
	if len(toks3) != 14 {
		t.Fatalf("check 81 step 3's block has %d `TOK` lines, and this test counts the refusals its FACT line states by them: the count cannot be checked against a block it does not read", len(toks3))
	}
	for _, phrase := range []string{"the fourteen statuses", "these fourteen refusals"} {
		if !strings.Contains(step3, phrase) {
			t.Errorf("check 81 step 3's block is %d `TOK` lines and its FACT line does not say %q: the review of PR #580 found it saying \"these eleven refusals\" over a block of ten, so both the count at the top of the FACT line and the one in the read-back sentence are the block's own", len(toks3), phrase)
		}
	}

	// Step 6 points back at step 3 by position, the way step 5 points at its own
	// lines: it says the control read of `config/caps/global` with `$BO` is the
	// line that would be a 401 without Bob's device node. The sentence used to
	// call it "step 3's third-from-last line", which is the `curl` read-back of
	// `config/limits/maxReserveMicros` -- the device-conjunct line is the last
	// `TOK` line of the block, and the last of its three `$BO` lines. Nothing
	// read the claim, so both the position and the line it names are checked
	// here.
	step6 := stepText(t, section, "6. **The granted read nodes", "7. **A token whose")
	if !strings.Contains(step6, "step 3's last `TOK` line") {
		t.Errorf("check 81 step 6 does not point at \"step 3's last `TOK` line\" for the 401 that Bob's device node turns into a 200: it is the last of step 3's three `$BO` lines, and a position that is not named cannot be read against the block")
	}
	if last := strings.TrimSpace(toks3[len(toks3)-1]); !strings.Contains(last, "config/caps/global.json?auth=$BO") {
		t.Errorf("check 81 step 6 names step 3's last `TOK` line as the one that would be a 401 without Bob's device node, and that line is\n%s\nwhich does not read `config/caps/global.json?auth=$BO`: the position the sentence names and the line it describes have to be the same line", last)
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
	// The other half of the same sentence: it says how many `curl` lines the
	// block has, to say that none of them is one of the five statuses. The
	// device node generation 3 added is the block's fourth `curl` -- the `$OUT2`
	// IAM seed, the device-node IAM write, and the two read-backs -- and the
	// sentence kept the older count for a round.
	block5 := codeBlockAfter(t, step5, "AT=$(gcloud auth print-access-token)")
	curls := 0
	for _, line := range strings.Split(block5, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "curl ") {
			curls++
		}
	}
	words := []string{"no", "one", "two", "three", "four", "five"}
	if curls >= len(words) {
		t.Fatalf("check 81 step 5's block has %d `curl` lines and this test has no word for that count: write one", curls)
	}
	if want := fmt.Sprintf("the block's %s `curl` lines", words[curls]); !strings.Contains(step5, want) {
		t.Errorf("check 81 step 5's block has %d `curl` lines (the two IAM writes and the two read-backs) and its FACT line does not say %q: the sentence is what tells the owner that those lines are not among the statuses the step records", curls, want)
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
		{12, `"TOKEN || USER"`}, {13, `"TOKEN || USER"`},
		{23, `"TOKEN || USER"`}, {97, `"TOKEN || USER"`},
		{14, `(TOKEN && auth.token.fs == $slug) || USER`},
		{24, `(TOKEN && auth.token.fs == $slug) || USER`},
		{126, `(TOKEN && auth.token.fs == $slug) || USER`},
		{182, `"RUN || USER"`},
		{53, `"RUN"`}, {212, `"RUN"`},
		{296, `"MAILBOX"`}, {340, `"MAILBOX"`},
		{45, `"PAIRING"`},
	} {
		if want.n < 1 || want.n > len(lines) {
			t.Fatalf("rules.json.tmpl has %d lines and the paragraph cites line %d", len(lines), want.n)
		}
		if got := lines[want.n-1]; !strings.Contains(got, want.pred) {
			t.Errorf("rules.json.tmpl line %d is %s and the paragraph cites it as %s: the paragraph hands the owner line numbers to open, so a number that does not hold the predicate claimed is evidence for something else", want.n, strings.TrimSpace(got), want.pred)
		}
	}
	if !strings.Contains(para, "`RUN || USER`") {
		t.Errorf("## Check 81: the paragraph listing D112's grants does not name `RUN || USER` for the day's per-run node (rules.json.tmpl line 182): a mu token reads it because USER is in that disjunction, and a citation that says `TOKEN || USER` there is not what the template says")
	}
	if !strings.Contains(para, "`RUN`") {
		t.Errorf("## Check 81: the paragraph does not file runs/** and agents/** under `RUN`: the template gates both with it (lines 53 and 212), and calling them `TOKEN` names a predicate a mu token fails for another reason than the one the sentence is making")
	}
}

// TestDocsCheck81CountsMatchTheBlocks: check 81 tells the owner how much of
// the project it writes and how many credentials it handles, in the section
// and in its register row -- the summary read *before* the section. Two
// sentences of the section count the IAM seeds ("N nodes under Alice's mailbox
// are seeded with IAM", "the N nodes it reads and acks ... are **seeded with
// the operator's IAM credentials**") and both said two; the register row
// counted them twice too ("two nodes are seeded with your own IAM
// credentials", "two seeded nodes"), and the review of PR #580 found the row
// still at two after the section had been corrected -- the row sits outside
// `## Check 81:`, so nothing read it. The blocks seed three: step 4's outbox
// message and heartbeat, and step 5's second outbox message, which the value
// guard needs because a `false` written to the message step 5 already acked
// would be refused by `!data.exists()` and prove nothing about the boolean.
// The row's other count is read here too: it said two minted custom tokens
// where the section mints four (step 1's two, step 7's mismatch token and its
// residual's). Every count is derived from the blocks, so an edit that changes
// what the check does fails this test until the prose counts it.
//
// The seeds are of two kinds, though, and the second review of PR #580 found
// this test holding both sentences to their *total*: `word` was the five IAM
// seeds while the first sentence counts the three under Alice's mailbox, so the
// assertion passed on the unrelated "**Five nodes in all**" in the same
// paragraph -- changing "Three nodes under Alice's mailbox are seeded with IAM"
// to "Two" left the test green. Each sentence is now held to its own subject,
// and the paragraph's device-node and total statements to their own counts.
func TestDocsCheck81CountsMatchTheBlocks(t *testing.T) {
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
	// The seeds are of two kinds, and the sentences count them separately: the
	// mailbox nodes (step 4's outbox message and heartbeat, and step 5's second
	// outbox message) and the device nodes (step 1's Alice, step 5's Bob). The
	// two paragraphs name both -- "the three nodes it reads and acks ... are
	// seeded with the operator's IAM credentials" and "the two device nodes
	// steps 1 and 5 write are seeded the same way, five nodes in all" -- so a
	// sentence is held to its own subject and to the totals the paragraph
	// states. Holding both sentences to the *total* is what let the section say
	// "Three nodes under Alice's mailbox" and still pass, on the unrelated
	// "**Five nodes in all**" in the same paragraph (the review of PR #580's
	// finding on this test).
	var mailboxSeeds, deviceSeeds, unclassified int
	for _, line := range strings.Split(steps, "\n") {
		if !strings.Contains(line, "?access_token=$AT") {
			continue
		}
		switch {
		case strings.Contains(line, "/mailbox/"):
			mailboxSeeds++
		case strings.Contains(line, "/config/admin/devices/"):
			deviceSeeds++
		default:
			unclassified++
		}
	}
	if unclassified > 0 {
		t.Fatalf("check 81's steps make %d IAM writes (`?access_token=$AT`) this test cannot file under a mailbox node or a device node: the sentences below count one kind each, so every seed has to be one of the two", unclassified)
	}
	if mailboxSeeds+deviceSeeds == 0 {
		t.Fatalf("## Check 81: no step writes with `?access_token=$AT`: the seeding this test counts is gone from the blocks, so the sentences below cannot be checked against them")
	}
	word := func(n int) string {
		w, ok := map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}[n]
		if !ok {
			t.Fatalf("check 81 counts %d of something and this test has no word for that count: write one", n)
		}
		return w
	}
	seeds := mailboxSeeds + deviceSeeds
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
		lower := strings.ToLower(para)
		for _, c := range []struct {
			want  string
			what  string
			count int
		}{
			{word(mailboxSeeds) + " nodes", "the mailbox nodes the sentence itself counts", mailboxSeeds},
			{word(deviceSeeds) + " device nodes", "the device nodes the paragraph adds", deviceSeeds},
			{word(seeds) + " nodes in all", "the total the paragraph states", seeds},
		} {
			if !strings.Contains(lower, strings.ToLower(c.want)) {
				t.Errorf("## Check 81: the paragraph saying %q does not say %q, and the blocks make %d IAM writes (`?access_token=$AT` in the steps): %d under a mailbox and %d under `config/admin/devices`. %s -- an owner reading a smaller number looks for a seed the check never made, and one reading a larger number for a node the Restore does not remove", stem, c.want, seeds, mailboxSeeds, deviceSeeds, c.what)
			}
		}
	}

	// The register row is the summary the owner reads *before* the section, and
	// it counts the seeds twice ("three nodes are seeded with your own IAM
	// credentials ... three seeded nodes"). It sits outside the section, so the
	// loop above never sees it -- the review of PR #580 found both counts still
	// at two there after the section had been corrected. The row is read the
	// same way TestDocsCheck89RegisterRowDoesNotClaimTheGuardReadsTheTrack
	// reads check 89's.
	row := registerRow(t, doc, "81")
	for _, phrase := range []string{word(seeds) + " nodes are seeded", word(seeds) + " seeded nodes"} {
		if !strings.Contains(strings.ToLower(row), strings.ToLower(phrase)) {
			t.Errorf("## Check 81: the register row does not say %q, and the blocks make %d IAM writes (`?access_token=$AT` in the steps): the row is the summary the owner reads before the section, so a smaller count there sends them looking for a seeded node that is not in the blocks -- and a larger one for a node the Restore does not remove", phrase, seeds)
		}
	}

	// The credentials: every `signin "$uid"` in the section is one custom token
	// minted and exchanged for an ID token -- step 1's two, step 7's mismatch
	// token and the residual's, which the prose names.
	tokens := strings.Count(section, `signin "$`)
	tokWord, ok := map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}[tokens]
	if !ok {
		t.Fatalf("check 81 mints %d custom tokens and this test has no word for that count: write one", tokens)
	}
	if !strings.Contains(row, tokWord+" minted custom tokens") {
		t.Errorf("## Check 81: the register row does not say %q, and the section mints %d custom tokens (`signin \"$uid\"`, step 1's two, step 7's mismatch token and the residual's): the row is what the owner reads before the section, and the tokens are the credentials the check handles", tokWord+" minted custom tokens", tokens)
	}
}

// TestDocsCheck82IdentityPlatformFailureComesAfterTheDeploy: check 82's "What
// failing looks like" used to say a project whose Identity Platform is not
// initialized "fails the database step **before** the rules". The code does the
// opposite: `deployDatabase` runs `db.Apply` -- the rules' `PutRules`, the
// marker and the other database writes -- and only then `idp.initialize`
// (`internal/cli/init_firebase.go`), which is why `internal/cli`'s
// TestIdentityPlatformInitializedWhenMissing sees the run succeed and the mark
// written with Identity Platform missing. The review of PR #580 found the
// bullet inverted, and an owner debugging by it would believe generation 2 was
// never deployed while it is live. The order is read out of the function here,
// so a reordering fails this test until the bullet is rewritten with it.
func TestDocsCheck82IdentityPlatformFailureComesAfterTheDeploy(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	var para string
	for _, p := range paragraphsOf(section) {
		if strings.Contains(p, "Identity Platform is not initialized") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatalf("## Check 82: no paragraph says a project's Identity Platform may not be initialized: this test holds that sentence to the code's order, so it cannot check a sentence that is not there")
	}
	if !strings.Contains(para, "**after** the rules") {
		t.Errorf("## Check 82: the \"What failing looks like\" paragraph does not say an uninitialized Identity Platform fails the step **after** the rules: `deployDatabase` applies the database (rules, marker, mark, project name, mode) and initializes Identity Platform afterwards, so the bullet has to send the owner to a live generation 2, not to a deploy that never happened")
	}
	if strings.Contains(para, "**before** the rules") {
		t.Errorf("## Check 82: the paragraph still says a failure at the Identity Platform step comes **before** the rules: that is the inverted claim the review of PR #580 found, and it would have the owner debugging a rules deploy that has already landed")
	}

	// The order the bullet describes, read out of the function it names.
	body := deployDatabaseBody(t)
	apply, idp := strings.Index(body, "db.Apply(ctx)"), strings.Index(body, "idp.initialize(ctx)")
	if apply < 0 || idp < 0 {
		t.Fatalf("deployDatabase no longer calls db.Apply and idp.initialize: got %d and %d, and check 82's bullet is about their order", apply, idp)
	}
	if apply > idp {
		t.Errorf("deployDatabase calls idp.initialize (%d) before db.Apply (%d): check 82's \"What failing looks like\" says an uninitialized Identity Platform fails the step **after** the rules, and this test holds the sentence to the code -- if the order really changed, rewrite the bullet with it", idp, apply)
	}
}

// TestDocsCheck82SettledLineNeedsNothingPending: check 82's cost paragraph
// attached the settled line -- "The database already has the rules, mark,
// project name and mode." -- to "an installation whose rules already match
// this binary's document". The sentence the same bullet goes on to describe,
// the run that still writes the marker, is exactly a run with an act line to
// show and a confirmation to ask for, and `deployDatabase` prints the settled
// line only under `len(acts) == 0 && !idp.missing && !fs.Pending()`
// (internal/cli/init_firebase.go) -- which is what this check's own step 4
// says ("the \"already has\" sentence is printed only when nothing at all is
// pending"). The review of PR #580 found the bullet attaching it to the rules'
// state. The trigger is read out of the code here, so a guard that changed
// fails this test until the paragraph is rewritten with it.
func TestDocsCheck82SettledLineNeedsNothingPending(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	const line = "The database already has the rules, mark, project name and mode."
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")

	var cost string
	for _, para := range paragraphsOf(section) {
		if strings.Contains(para, "**What changes in the project, and its cost.**") {
			cost = para
			break
		}
	}
	if cost == "" {
		t.Fatalf("## Check 82: no paragraph says what changes in the project and what it costs: this test holds its account of the settled line to the code")
	}
	if !strings.Contains(cost, line) {
		t.Fatalf("## Check 82: the cost paragraph no longer names the settled line %q: this test holds the trigger it is attached to, so it cannot check a paragraph that does not carry it", line)
	}
	if !strings.Contains(cost, "nothing at all is pending") || !strings.Contains(cost, "len(acts) == 0 && !idp.missing && !fs.Pending()") {
		t.Errorf("## Check 82: the cost paragraph does not give the settled line's trigger as nothing at all being pending (`deployDatabase`: `len(acts) == 0 && !idp.missing && !fs.Pending()`): an owner who reads the rules' state as the trigger expects that line from a run that is about to write the marker, and that run prints its act line and asks for the confirmation instead")
	}
	if strings.Contains(cost, "it plans **no** rules write and prints `"+line+"`") {
		t.Errorf("## Check 82: the cost paragraph still attaches the settled line to the rules matching: the same bullet's next clause is the run that writes the marker, and a run with a pending action never prints it")
	}

	// The guard, read out of the function the paragraph names.
	body := deployDatabaseBody(t)
	guard := strings.Index(body, "if len(acts) == 0 && !idp.missing && !fs.Pending() {")
	printed := strings.Index(body, `"`+line+`"`)
	if guard < 0 || printed < 0 {
		t.Fatalf("deployDatabase no longer guards the settled line with `len(acts) == 0 && !idp.missing && !fs.Pending()` (guard %d, line %d): check 82's account of what the line means is written against that guard", guard, printed)
	}
	if guard > printed || strings.Contains(body[guard:printed], "\n\t}") {
		t.Errorf("deployDatabase prints the settled line outside its `len(acts) == 0 && !idp.missing && !fs.Pending()` guard: the line is the step's for a database with nothing pending, not its line for one whose rules match, and check 82's cost paragraph and step 4 both say so")
	}
}

// TestDocsCheck82NamesTheTestsThatCoverTheRefusal: step 7's FACT asks the owner
// to record "the sentence above, quoted from the source, and the two test
// names", and the step described them instead of naming them ("internal/infra's
// plan test for the refusal", "internal/cli's doctorRulesSkew tests") -- a FACT
// line the owner cannot fill in, because no function name in the section can be
// looked up.
//
// The names are held to the packages that define them: each has to be a
// `func <name>(t *testing.T)` in that package's tests, so renaming or deleting
// the test fails here instead of leaving the checklist sending the owner to a
// name that no longer exists.
func TestDocsCheck82NamesTheTestsThatCoverTheRefusal(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 82:")
	step := stepText(t, section, "7. **The marker above this binary's generation", "**Passes when:**")

	if !strings.Contains(step, "the two test names") {
		t.Errorf("check 82 step 7's FACT no longer asks for \"the two test names\": this test holds the names the step gives to that request, so it cannot check a step that asks for something else")
	}
	want := map[string]string{
		"TestDBRefusesToDeployOverNewerRules": "internal/infra",
		"TestDoctorPolicyChecks":              "internal/cli",
	}
	named := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(step, -1)
	if len(named) != len(want) {
		t.Fatalf("check 82 step 7 names %d test functions (%v) and its FACT asks the owner for \"the two test names\": the sentence that covers the refusal offline has to name the tests that cover it, one for the plan refusal and one for the reporting", len(named), named)
	}
	for _, name := range named {
		pkg, ok := want[name]
		if !ok {
			t.Errorf("check 82 step 7 names %s, and this test does not know it: the two names are the offline coverage of the marker-above refusal, and a name that is not one of them is either a typo or a test the step should not be claiming", name)
			continue
		}
		files, err := filepath.Glob(filepath.Join("..", "..", pkg, "*_test.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no tests in %s (%v): check 82 step 7 names %s as living there", pkg, err, name)
		}
		found := false
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "func "+name+"(t *testing.T) {") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("check 82 step 7 names %s as the test for the marker-above refusal, and %s has no `func %s(t *testing.T)`: the FACT line asks the owner to record the name, so a renamed or deleted test leaves the checklist sending them to a name that does not exist -- the same defect as the sentence that named no test at all", name, pkg, name)
		}
	}
}

// TestDocsCheck81StatedCountsMatchTheirBlocks: the rest of check 81's stated
// counts -- step 2's six wrong shapes, step 4's two 200s, step 6's three
// reads by the second user, step 7's two tokens and the Restore's four
// deletes. Each counts something in the step's own block, so each is read from
// the block here: the review of PR #580 found step 3 stating "these eleven
// refusals" over a block of ten, and that is the shape a count nothing derives
// has wherever it sits -- a number in a FACT line that no command in the step
// produces, which is what the owner is asked to record.
func TestDocsCheck81StatedCountsMatchTheirBlocks(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	// A `TOK` call, in either form the check uses: a line of its own (`TOK -X
	// PUT ...`) or inside a `$(...)` in a `printf` (`$(TOK "..." )`, which is
	// how step 6's two columns are built).
	toks := func(step, carrying string) int {
		n := 0
		for _, line := range strings.Split(step, "\n") {
			if strings.Contains(line, "TOK ") && strings.Contains(line, carrying) {
				n++
			}
		}
		return n
	}

	// Step 2: the wrong shapes, each on an id of its own, all 401.
	step2 := stepText(t, section, "2. **The fence's positive half", "3. **The fence's negative half")
	if n := toks(step2, "inbox/$BAD.json"); n != 6 {
		t.Errorf("check 81 step 2's block makes %d refused writes of the wrong shapes (`TOK` lines writing `inbox/$BAD.json`) and the step counts them as six (\"All six are 401\", \"the six wrong shapes' status (401)\"): the count is the block's, and a step that adds a shape without the count reads as a check that made one more refusal than it did", n)
	}
	for _, want := range []string{"All six are 401", "the six wrong shapes"} {
		if !strings.Contains(step2, want) {
			t.Errorf("check 81 step 2 does not say %q: it is one of the two places the step counts its six wrong shapes, and the count is read against the block", want)
		}
	}

	// Step 4: the two 200s, the grants that hold content.
	step4 := stepText(t, section, "4. **The user reads", "5. **The user's second write")
	if n := toks(step4, "# 200"); n != 2 {
		t.Errorf("check 81 step 4's block has %d `TOK` reads marked 200 and its FACT line says \"the six statuses, and -- for the two 200s -- the bodies\": the two 200s are the outbox and the heartbeat, and a third 200 (or one fewer) is what the sentence about the seeded content is about", n)
	}
	// The step says it twice (the FACT line's "for the two 200s" and its
	// closing "the seeding here is what makes the two 200s hold content"), so
	// every count of 200s in it is read, not just the first.
	for _, m := range regexp.MustCompile(`(\w+) 200s\b`).FindAllStringSubmatch(step4, -1) {
		if m[1] != "two" {
			t.Errorf("check 81 step 4 says \"%s 200s\" and its block has two `TOK` reads marked 200 (the outbox and the heartbeat): the two are the nodes the IAM seeding fills, so the count is the block's", m[1])
		}
	}

	// Step 6: the second user's three reads, which close the binding.
	step6 := stepText(t, section, "6. **The granted read nodes", "7. **A token whose")
	if n := toks(step6, "?auth=$BO"); n != 3 {
		t.Errorf("check 81 step 6's block has %d reads made with the second user's token (`TOK` lines carrying `?auth=$BO`) and the step counts them as three (\"closes the binding with three more\", \"**FACT:** those three\"): the three are the binding's other half -- the granted node is 200 for BOB and ALICE's mailbox nodes are not -- so a count that drifts from the block is a claim about a read the check does not make", n)
	}
	for _, want := range []string{"closes the binding with three more", "**FACT:** those three"} {
		if !strings.Contains(step6, want) {
			t.Errorf("check 81 step 6 does not say %q: it counts the second user's reads, and the count is the block's", want)
		}
	}

	// Step 7: two tokens, minted and exchanged only there.
	step7 := stepText(t, section, "7. **A token whose", "8. **The fence is the deployed")
	if n := strings.Count(step7, `signin "$`); n != 2 {
		t.Errorf("check 81 step 7's block mints %d tokens (`signin \"$...\"` calls) and the step says \"Two tokens, minted and exchanged only here\": the second is the residual P2.2 accepted, which needs a signer of its own, and the count of what the step mints is the block's", n)
	}
	if !strings.Contains(step7, "Two tokens, minted and exchanged only here") {
		t.Errorf("check 81 step 7 does not say \"Two tokens, minted and exchanged only here\": its block makes two `signin` calls, and the sentence is what tells the owner how many custom tokens this step handles")
	}

	// The Restore: four RTDB deletes, one per node the check wrote -- the two
	// mailboxes and the two device nodes generation 3 needs before an account
	// can read anything at all.
	restore := section[strings.Index(section, "**Restore.**"):]
	loops := forInLoops(restore)
	if len(loops) != 1 || len(loops[0]) != 4 {
		t.Fatalf("check 81's Restore has %d `for p in` loops of %v paths, and its FACT line says \"the four RTDB deletes' statuses (200 each)\": the deletes are the two mailboxes and the two device nodes, so the loop is what the count is read from", len(loops), loops)
	}
	if !strings.Contains(restore, "the four RTDB deletes") {
		t.Errorf("check 81's Restore does not say \"the four RTDB deletes\": its loop deletes four paths (`%s`), and the FACT line is what the owner pastes back", strings.Join(loops[0], " "))
	}
}

// TestDocsGeneratorSizeAndMailboxCountAreTheGoldens: check 81 step 8 and check
// 82 step 5 both state the generated document's size and check 82 states the
// mailbox subtree's count (1). The three `grep -o` counts of check 81 step 8
// are held to the golden document; these two numbers, which the owner pastes
// back as the deployment's fingerprint, were not. Both are read out of the
// file the checks name -- `rules.Generate()`'s own document, which
// `internal/budget/rules/testdata/rules.golden.json` holds byte for byte -- so
// a template change moves them together. Neither number is written down here:
// the size was 42687 bytes under the generation-2 template, and what the test
// uses now is `len(golden)`.
func TestDocsGeneratorSizeAndMailboxCountAreTheGoldens(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	golden, err := os.ReadFile("../budget/rules/testdata/rules.golden.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		heading string
		re      string
		what    string
		want    int
	}{
		{"## Check 81:", `generator's own \*\*(\d+)\*\* bytes`, "the generated document's size", len(golden)},
		{"## Check 82:", `\*\*(\d+) is the generator's own size\*\*`, "the generated document's size", len(golden)},
		{"## Check 82:", "its `mailbox` count \\(\\*\\*(\\d+)\\*\\*", "the mailbox subtree's count", strings.Count(string(golden), "mailbox")},
	} {
		section := docSection(t, path, doc, c.heading)
		m := regexp.MustCompile(c.re).FindStringSubmatch(section)
		if m == nil {
			t.Fatalf("%s: does not carry %s in the form this test reads (`%s`): the number is the check's own fingerprint of the deployed document, so the sentence that states it is what the test holds to the golden", c.heading, c.what, c.re)
		}
		if m[1] != fmt.Sprint(c.want) {
			t.Errorf("%s: states %s as %s, and `rules.Generate()`'s own document (internal/budget/rules/testdata/rules.golden.json) gives %d: the number is a FACT line the owner pastes back, so a template change has to move it here too", c.heading, c.what, m[1], c.want)
		}
	}

	// The two places the document's size is rounded to kilobytes rather than
	// printed: check 82's cost paragraph ("one rules deploy of about N KB") and
	// the register row, which the owner reads to decide what the run costs.
	// Generation 3 grew the document past the round number generation 2 had, and
	// the paragraph was left saying "about 42 KB" while the row had already been
	// moved to the new one: the two are the same number, read from the same file,
	// so both are held to it here -- and the number itself is not written down,
	// because it is `len(golden)` rounded.
	kb := fmt.Sprint((len(golden) + 512) / 1024)
	for _, c := range []struct{ what, text string }{
		{"## Check 82's cost paragraph", docSection(t, path, doc, "## Check 82:")},
		{"the register row for check 82", registerRow(t, doc, "82")},
	} {
		m := regexp.MustCompile(`about (\d+) KB`).FindStringSubmatch(c.text)
		if m == nil {
			t.Fatalf("%s: no longer says \"about <N> KB\", which this test holds to the golden's size (%d bytes, about %s KB): the sentence is what tells the owner what the run costs", c.what, len(golden), kb)
		}
		if m[1] != kb {
			t.Errorf("%s: says \"about %s KB\" and `rules.Generate()`'s document is %d bytes, about %s KB: the number is the same one the check prints in full elsewhere, so a template change has to move both", c.what, m[1], len(golden), kb)
		}
	}
}

// TestDocsCheck89StatedCountsMatchItsCommands: check 89's counts are counts of
// its own commands and of the app files the scan allows -- the two submissions
// (one per platform), the two lists the credential scan reads, and the app's
// own four JSON files. Nothing derived them, and the same defect sits in all
// three: a number in a FACT line that no command in the step produces.
func TestDocsCheck89StatedCountsMatchItsCommands(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 89:")

	// Step 3: one submission per platform.
	step3 := stepText(t, section, "3. **The internal track", "4. **The build installs")
	if n := strings.Count(step3, "eas submit --platform"); n != 2 {
		t.Errorf("check 89 step 3 names %d `eas submit --platform` commands and its FACT line asks for \"the two submission ids, the two track names\": the submissions are the two platforms, so the count of what the owner records is the count of the commands", n)
	}
	for _, want := range []string{"the two submission ids", "the two track names"} {
		if !strings.Contains(step3, want) {
			t.Errorf("check 89 step 3 does not say %q: it names two `eas submit --platform` commands, and the FACT line is the list the owner pastes back", want)
		}
	}

	// Step 5: two lists, and the four files the JSON scan allows.
	step5 := stepText(t, section, "5. **No store credential", "\n\n**Passes when:**")
	if n := strings.Count(step5, "git -C .. ls-files mobile"); n != 2 {
		t.Errorf("check 89 step 5 runs `git -C .. ls-files mobile` %d times and its FACT line asks for \"the two file lists\": one list is the credential-shaped files and the other the JSON beyond the app's own, so the count is the block's", n)
	}
	if !strings.Contains(step5, "the two file lists") {
		t.Errorf("check 89 step 5 does not say \"the two file lists\": its block runs the scan twice, and the FACT line is what the owner pastes back")
	}
	m := regexp.MustCompile(`\^mobile/\(([^)]+)\)\\\.json\$`).FindStringSubmatch(step5)
	if m == nil {
		t.Fatalf("check 89 step 5's JSON scan no longer carries a `^mobile/(...)\\.json$` allow-list: this test reads the app's own files out of that pattern")
	}
	allowed := strings.Split(m[1], "|")
	if len(allowed) != 4 {
		t.Errorf("check 89 step 5's JSON scan allows %d app files (`%s`) and the step says \"the app's own four files\": the pattern is the list the scan exempts, so a file added to one without the other is a scan that reports a file the step says is allowed (or the reverse)", len(allowed), m[1])
	}
	for _, name := range allowed {
		if !strings.Contains(step5, name+".json") {
			t.Errorf("check 89 step 5's JSON scan allows %s.json and the step does not name it among \"the app's own four files\": the pattern and the list have to be the same files", name)
		}
	}
}

// TestDocsCheck81GuardPreambleCountsTheValuesItReads: the paragraph above check
// 81's guard block says the guard "reads the four backend values out of the
// project's own config rather than from memory, and refuses to start without
// any of them". It said three, and three was right while the loop read DB, KEY
// and SA; FP joined the loop later (the Firebase project id, which step 1's
// sign-in and the Restore's batchDelete address) and the sentence stayed at
// three -- the same stale count the review of PR #580 found in step 3, in a
// sentence no test read. The count is taken from the loop here, so a value
// added to or dropped from the guard fails this test until the sentence says
// the new number.
func TestDocsCheck81GuardPreambleCountsTheValuesItReads(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	guard := codeBlockAfter(t, section, "# === Check 81: setup + guard")
	m := regexp.MustCompile(`for v in ([A-Z][A-Z ]*); do`).FindStringSubmatch(guard)
	if m == nil {
		t.Fatalf("## Check 81: the guard block has no `for v in ...; do` loop: that loop is the emptiness check whose values the paragraph counts")
	}
	vals := strings.Fields(m[1])
	word, ok := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six"}[len(vals)]
	if !ok {
		t.Fatalf("check 81's guard reads %d values (%s) and this test has no word for that count: write one", len(vals), strings.Join(vals, " "))
	}

	var para string
	for _, p := range paragraphsOf(section) {
		if !strings.Contains(p, "backend values") {
			continue
		}
		if para != "" {
			t.Fatalf("## Check 81: more than one paragraph says \"backend values\": this test holds the guard paragraph's count to the loop, so it cannot pick the sentence")
		}
		para = p
	}
	if para == "" {
		t.Fatalf("## Check 81: no paragraph says \"backend values\": this test holds that sentence's count to the guard's loop")
	}
	if !strings.Contains(para, "the "+word+" backend values") {
		t.Errorf("## Check 81: the guard paragraph does not count the values as %q, and the guard's loop reads %d (`for v in %s; do`): the paragraph is how the owner knows what the guard refuses to start without, so a count below the loop's hides a value the guard stops for and one above it names a read the block does not make", "the "+word+" backend values", len(vals), m[1])
	}
}

// TestDocsCheck81PredicateCountNamesTheTemplatesCases: check 81's "What the
// deployed rules are" says "Three predicates decide everything
// (`internal/budget/rules/rules.go`): `USER` ..., `MAILBOX` ...,
// `CONSUMER` ...". The three are the mailbox's whole vocabulary, and the
// sentence is the owner's index into the rules the check reads back, so the
// names are held to the switch that implements them (`wordMacro`,
// internal/budget/rules/rules.go) and the count to the names.
func TestDocsCheck81PredicateCountNamesTheTemplatesCases(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	var para string
	for _, p := range paragraphsOf(section) {
		if strings.Contains(p, "predicates decide everything") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatalf("## Check 81: no paragraph says \"predicates decide everything\": this test holds its names and its count to the switch that implements them")
	}

	// The sentence's end is the first "). " after it: each predicate is
	// backticked and followed by its definition in parentheses, and the last
	// definition is what closes the sentence.
	sentence := para[strings.Index(para, "predicates decide everything"):]
	j := strings.Index(sentence, "). ")
	if j < 0 {
		t.Fatalf("## Check 81: the sentence naming the predicates has no \"). \" ending it: this test reads the predicate list out of that sentence")
	}
	names := regexp.MustCompile("`([A-Z]+)` \\(").FindAllStringSubmatch(sentence[:j], -1)
	word, ok := map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}[len(names)]
	if !ok {
		t.Fatalf("check 81 names %d predicates and this test has no word for that count: write one", len(names))
	}
	if !strings.Contains(para, word+" predicates decide everything") {
		t.Errorf("## Check 81: the sentence names %d predicates and does not open with %q: the count is what tells the owner the sentence is the whole vocabulary the mailbox is written in", len(names), word+" predicates decide everything")
	}
	src := readSrc(t, "../budget/rules/rules.go")
	for _, n := range names {
		if !strings.Contains(src, "case \""+n[1]+"\":") {
			t.Errorf("## Check 81: the paragraph names `%s` as one of the predicates that decide the mailbox, and internal/budget/rules/rules.go has no `case \"%s\":`: the paragraph is the owner's index into the rules the check reads back, so a name the switch does not have points at a predicate that does not exist", n[1], n[1])
		}
	}
}

// TestDocsCheck81TemplateCountsMatchTheTemplate: the paragraph that lists what
// a mu token may read counts two things out of the rules template -- the
// mailbox's own "two nodes" (the outbox and the heartbeat, the two `.read`
// lines the `MAILBOX` predicate gates) and the grant's "three `repos/$slug`
// entries" (the `(TOKEN && auth.token.fs == $slug) || USER` reads under caps,
// kill and the day). Both counts are the template's own, so a grant added to
// or removed from it fails this test until the paragraph counts the new one.
func TestDocsCheck81TemplateCountsMatchTheTemplate(t *testing.T) {
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
		t.Fatalf("## Check 81: no paragraph says \"D112's grant sits at\": this test counts the template's grants against that paragraph")
	}

	tmpl := readSrc(t, "../budget/rules/rules.json.tmpl")
	for _, c := range []struct {
		counts int
		phrase string
		what   string
	}{
		{strings.Count(tmpl, `".read": "MAILBOX"`), "its own mailbox's %s nodes", "the mailbox's own reads (`\".read\": \"MAILBOX\"` in rules.json.tmpl: the outbox and the heartbeat)"},
		{strings.Count(tmpl, `".read": "(TOKEN && auth.token.fs == $slug) || USER"`), "their %s `repos/$slug` entries", "the `repos/$slug` reads that carry the fs claim (`(TOKEN && auth.token.fs == $slug) || USER`: caps, kill and the day)"},
	} {
		word, ok := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six"}[c.counts]
		if !ok {
			t.Fatalf("rules.json.tmpl carries %d of %s and this test has no word for that count: write one", c.counts, c.what)
		}
		if want := fmt.Sprintf(c.phrase, word); !strings.Contains(para, want) {
			t.Errorf("## Check 81: the paragraph listing D112's grant does not say %q, and rules.json.tmpl carries %d of %s: the paragraph is the owner's map of what a mu token may read, so a count that is not the template's sends them looking for a grant the template does not make -- or hides one it does", want, c.counts, c.what)
		}
	}
}

// TestDocsCheck81Step6CountsItsReadColumns: step 6's FACT line counts its two
// columns -- "the first thirteen are **200** ... the second twenty-six are
// **401**" -- and the Passes paragraph above the section repeats both numbers.
// The counts are the step's own two `for p in ...; do` loops, and nothing
// derived them, so a path added to one column would leave the prose counting
// the old one: the owner would record a column of the wrong size and a
// refusal the check never made would read as one it did.
func TestDocsCheck81Step6CountsItsReadColumns(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	step6 := stepText(t, section, "6. **The granted read nodes", "7. **A token whose `mu` is not its own uid")
	loops := forInLoops(step6)
	if len(loops) != 2 {
		t.Fatalf("check 81 step 6 has %d `for p in ...; do` loops and its FACT line counts two columns: this test reads the granted column and the denied one out of those loops", len(loops))
	}
	granted, denied := len(loops[0]), len(loops[1])
	words := map[int]string{13: "thirteen", 26: "twenty-six"}
	gw, ok := words[granted]
	if !ok {
		t.Fatalf("check 81 step 6's first loop reads %d paths and this test has no word for that count: write one", granted)
	}
	dw, ok := words[denied]
	if !ok {
		t.Fatalf("check 81 step 6's second loop reads %d paths and this test has no word for that count: write one", denied)
	}

	i := strings.Index(section, "**Passes when:**")
	if i < 0 {
		t.Fatalf("## Check 81: has no \"**Passes when:**\" paragraph: it repeats step 6's two counts, and this test holds it to them")
	}
	passes := section[i:]
	if j := strings.Index(passes, "\n\n"); j >= 0 {
		passes = passes[:j]
	}

	for _, c := range []struct{ text, want, where string }{
		{step6, fmt.Sprintf("The first %s are **200**", gw), "step 6's FACT line"},
		{step6, fmt.Sprintf("the second %s are **401**", dw), "step 6's FACT line"},
		{passes, fmt.Sprintf("step 6's first %s reads answer 200", gw), "the Passes paragraph"},
		{passes, fmt.Sprintf("its second %s 401", dw), "the Passes paragraph"},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("## Check 81: %s does not say %q, and step 6's loops read %d granted paths and %d denied ones: the loops are the columns, so a count that is not theirs makes the owner record a column of the wrong size", c.where, c.want, granted, denied)
		}
	}
}

// TestDocsCheck81AccountCountIsTheRestores: check 81's cost paragraph says
// "Two Firebase Auth accounts (`chk81-alice`, the user, and `chk81-bob`, a
// second user to prove the binding)", added by hand before step 1 (D114) and
// removed by the Restore. The Restore's `accounts:batchDelete` is the one
// place the check names the accounts, so the paragraph's count is read out of
// that list: an account added to the check without the paragraph (or the
// reverse) leaves the owner adding a user nothing signs in as, or leaving one
// behind in the project.
func TestDocsCheck81AccountCountIsTheRestores(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	i := strings.Index(section, "**Restore.**")
	if i < 0 {
		t.Fatalf("## Check 81: has no \"**Restore.**\" paragraph: this test reads the accounts out of its `accounts:batchDelete`")
	}
	m := regexp.MustCompile(`\\"localIds\\":\[([^\]]*)\]`).FindStringSubmatch(section[i:])
	if m == nil {
		t.Fatalf("## Check 81: the Restore has no `\"localIds\":[...]` in its `accounts:batchDelete`: this test counts the accounts the check used from that list")
	}
	uids := strings.Split(m[1], ",")
	word, ok := map[int]string{1: "One", 2: "Two", 3: "Three"}[len(uids)]
	if !ok {
		t.Fatalf("check 81's Restore deletes %d Auth accounts and this test has no word for that count: write one", len(uids))
	}
	if !strings.Contains(section, word+" Firebase Auth accounts") {
		t.Errorf("## Check 81: the cost paragraph does not say %q, and the Restore's `accounts:batchDelete` deletes %d (`%s`): the paragraph is how the owner knows how many accounts to add by hand before step 1, and the Restore is what removes them", word+" Firebase Auth accounts", len(uids), m[1])
	}
}

// TestDocsCheck82PlansOneRulesDeploy: check 82's cost paragraph and its
// register row both say one rules deploy ("**one rules deploy** of about <N>
// KB", "One rules deploy of about <N> KB and at most the marker write beside
// it"), at the size `TestDocsGeneratorSizeAndMailboxCountAreTheGoldens` reads
// from the golden. One is what the plan holds: `DB.Plan` appends a single
// `Path: "rules"` action and then the marker's write. Nothing derived it, so a
// second rules action would leave both sentences promising a run that writes
// the document once.
func TestDocsCheck82PlansOneRulesDeploy(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	src := readSrc(t, "../infra/firebasedb.go")
	n := strings.Count(src, `Path: "rules"`)
	if n == 0 {
		t.Fatalf("internal/infra/firebasedb.go has no `Path: \"rules\"` action: the rules deploy this test counts is gone from the plan, so the sentences cannot be checked against it")
	}
	word, ok := map[int]string{1: "one", 2: "two", 3: "three"}[n]
	if !ok {
		t.Fatalf("DB.Plan appends %d rules actions and this test has no word for that count: write one", n)
	}
	cap := strings.ToUpper(word[:1]) + word[1:]

	for _, c := range []struct{ text, want, where string }{
		{docSection(t, path, doc, "## Check 82:"), "**" + word + " rules deploy**", "the section's cost paragraph"},
		{registerRow(t, doc, "82"), cap + " rules deploy", "the register row"},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("## Check 82: %s does not say %q, and `DB.Plan` appends %d `Path: \"rules\"` actions (internal/infra/firebasedb.go): the two sentences are what the owner reads to know how much the run writes", c.where, c.want, n)
		}
	}
}

// TestDocsRegisterPrefaceNamesEveryRunPhase: the paragraph above the register
// table says which of the ten checks are still unwritten and which phases run
// them, and it said "81 and 82 at P2.3, 89 at P4.4" -- one phase for check 81,
// where the plan's own table (docs/mobile/implementation-plan.md, the row the
// paragraph cites) says "run at P2.3 and P4.2", and the register row this same
// diff adds says "P2.3 and P4.2 **run** it". The preface is the paragraph whose
// job is to say what has and has not been run, so a phase it drops is a check
// the owner does not run at the task that runs it.
//
// The phases are held to the plan's table rather than to a copy of the
// sentence: the plan is what the paragraph cites, and it is where a phase
// changes first.
func TestDocsRegisterPrefaceNamesEveryRunPhase(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	var para string
	for _, p := range paragraphsOf(doc) {
		if strings.Contains(p, "All ten are owner-run and none has been run") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatalf("%s: no paragraph says all ten checks are owner-run and none has been run: this test reads the phases out of that sentence", path)
	}
	_, said, ok := strings.Cut(para, "none has been run**")
	if !ok {
		t.Fatalf("the register preface does not carry the sentence this test reads:\n%s", para)
	}
	said, _, ok = strings.Cut(said, "Until")
	if !ok {
		t.Fatalf("the register preface's sentence about the run phases no longer ends before \"Until\":\n%s", said)
	}
	said = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(said), ":")), ".")
	said = strings.Join(strings.Fields(said), " ") // the paragraph wraps; the sentence does not

	got := map[string]string{}
	for _, seg := range strings.Split(said, ",") {
		n, phases, ok := strings.Cut(strings.TrimSpace(seg), " at ")
		if !ok {
			t.Fatalf("the register preface's sentence is %q, and the segment %q does not name a phase: the phases are what tells the owner when to run each check", said, strings.TrimSpace(seg))
		}
		n = strings.TrimSpace(n)
		if strings.ContainsAny(n, " ") {
			t.Errorf("the register preface names %q for a phase, and each phase has to name its check on its own: check 81 is run twice (P2.3 and P4.2), so a segment that groups two checks under one phase states one run for both -- and 82's is P2.3 alone", n)
			continue
		}
		got[n] = strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(phases), " and ", " ")), " ")
	}
	if len(got) == 0 {
		t.Fatalf("the register preface's sentence names no phase: %q", said)
	}

	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	for _, n := range []string{"81", "82", "89"} {
		row := ""
		for _, line := range strings.Split(plan, "\n") {
			if strings.HasPrefix(line, "| **"+n+"** |") {
				row = line
				break
			}
		}
		if row == "" {
			t.Fatalf("docs/mobile/implementation-plan.md has no row for check %s: the register preface's phases are read against that table", n)
		}
		_, run, ok := strings.Cut(row, "run at ")
		if !ok {
			t.Fatalf("the plan's row for check %s does not say when it is run:\n%s", n, row)
		}
		run, _, _ = strings.Cut(run, " |")
		want := strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(run), " and ", " ")), " ")
		if got[n] != want {
			t.Errorf("the register preface says check %s is run at %q, and the plan's row says %q:\n%s\nthe preface is the paragraph whose job is to say which checks are still unrun and at which task each is run, and the plan it cites is where a phase changes", n, got[n], want, strings.TrimSpace(row))
		}
	}
}

// TestDocsFactsAskingForAReadHaveTheRead: two of check 81's FACT lines ask the
// owner to record a value that only a read produces -- step 3's "that
// config/mode, config/limits/maxReserveMicros and fugaro/mark are unchanged",
// read back with the operator's credentials, and step 5's "read the message
// back ... that it now carries acked: true and its text is unchanged" -- while
// both blocks were writes only. The owner cannot record a value the step never
// printed, and steps 2 and 4 of the same check put the read-back in the block
// next to the FACT line that asks for it.
func TestDocsFactsAskingForAReadHaveTheRead(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 81:")

	for _, c := range []struct {
		step, from, to, marker string
		reads                  []string
	}{
		{
			step: "step 3", from: "3. **The fence's negative half", to: "4. **The user reads",
			marker: "ID2=$(printf 'chk81-%s'",
			reads:  []string{"config/mode.json", "config/limits/maxReserveMicros.json", "fugaro/mark.json"},
		},
		{
			step: "step 5", from: "5. **The user's second write", to: "6. **The granted read nodes",
			marker: "AT=$(gcloud auth print-access-token)",
			reads:  []string{"outbox/$OUT.json"},
		},
	} {
		step := stepText(t, section, c.from, c.to)
		block := codeBlockAfter(t, step, c.marker)
		for _, want := range c.reads {
			found := false
			for _, line := range strings.Split(block, "\n") {
				if strings.Contains(line, "Authorization: Bearer $AT") && strings.Contains(line, want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("check 81 %s: the FACT line asks the owner to record %s, and the step's block never reads it with the operator's credentials (no `curl ... -H \"Authorization: Bearer $AT\" ... %s` line): a FACT line that asks for a value no command produces is one the owner cannot record, and the step's own refusal writes cannot show it -- the nodes are `TOKEN`-only, so a `mu` token cannot read them either", c.step, want, want)
			}
		}
	}
}

// TestDocsReadyLinesDoNotDenyWhatTheyPrint: every check that opens with a guard
// echoes the values it read on its "ready" line, and check 81's printed the
// Firebase project id while the same line said "DB, KEY, SA and FP are read and
// never printed" -- the sentence contradicted the echo on its own line, in the
// place the owner reads to know what is safe to paste back.
//
// The rule is the general one: for whatever a ready line echoes (`$VAR` before
// the dash), the sentence after the dash may not name it as never printed. The
// echoed values are what makes a guard stop a finding the owner can act on, so
// the sentence is what gives way, not the echo.
func TestDocsReadyLinesDoNotDenyWhatTheyPrint(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	seen := 0
	for _, check := range []string{"Check 81", "Check 82", "Check 89"} {
		section := docSection(t, path, doc, "## "+check+":")
		ready := ""
		for _, line := range strings.Split(section, "\n") {
			if strings.Contains(line, `echo "`+check+` ready:`) {
				ready = line
			}
		}
		if ready == "" {
			t.Fatalf("## %s: no line echoes a ready message, so this test cannot see what the guard prints: the check's steps are re-run after a stop, and the ready line is what tells the owner which installation they are on", check)
		}
		seen++
		values, said, ok := strings.Cut(ready, "—")
		if !ok {
			continue // a ready line that says nothing about what it read
		}
		claim, _, ok := strings.Cut(said, "never printed")
		if !ok {
			continue // a ready line that claims nothing is held back
		}
		for _, m := range regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(values, -1) {
			v := m[1]
			if regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(v) + `([^A-Za-z0-9_]|$)`).MatchString(claim) {
				t.Errorf("## %s: the ready line prints $%s and the same line says %s is never printed:\n%s\n\tthe values before the dash are what the owner reads to know which installation the check is about; only the sentence after the dash may claim something is held back", check, v, v, ready)
			}
		}
		if check == "Check 81" && !strings.Contains(claim, "KEY") {
			t.Errorf("## %s: the ready line no longer says the web API key is the value it holds back:\n%s\n\t$KEY is the project's restricted web API key, and the line is where the owner reads that the key is not printed", check, ready)
		}
	}
	if seen != 3 {
		t.Fatalf("this test read %d ready lines, want the three guards of checks 81, 82 and 89: a guard that stops echoing what it read is the same defect in the other direction", seen)
	}

	// The register row for check 81 says the same thing about the same block,
	// and the row is what the owner reads before the section.
	if row := registerRow(t, doc, "81"); strings.Contains(row, "never prints it") {
		t.Errorf("the register row for check 81 says the setup block reads the backend out of the project's own config and \"never prints it\", and the block echoes the Firebase project id on its ready line: the row is what the owner reads first, and it has to say what the block does")
	}
}

// deployDatabaseBody returns the body of internal/cli/init_firebase.go's
// `deployDatabase`, up to the next top-level function: check 82's account of
// the database step (the order of the rules deploy and the marker, and the
// guard on the settled line) is written against it, and two tests read it.
func deployDatabaseBody(t *testing.T) string {
	t.Helper()
	src := readSrc(t, "init_firebase.go")
	i := strings.Index(src, "func (r *initRun) deployDatabase(")
	if i < 0 {
		t.Fatalf("internal/cli/init_firebase.go has no deployDatabase: the check's account of the database step is written against that function")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	return body
}

// mobileBlockCommandLines returns a block's command lines without its comments: a comment
// inside a block quotes the broken form to warn about it (`?key=$KEY here
// answers 401`), and a claim about what the block does is about its commands.
func mobileBlockCommandLines(block string) string {
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
