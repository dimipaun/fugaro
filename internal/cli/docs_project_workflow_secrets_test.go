package cli

// docs/project-workflow-secrets/ is the design-only folder for issue #1678:
// a workflow's own secret (`workflows.<w>.secrets`) that can be stored once
// for the project behind an operator-local allow-list. This file pins the
// folder's facts, the way docs_rigor_modes_test.go pins docs/rigor-modes/ and
// docs_backlog_test.go pins docs/backlog/ — there is no registry test over
// docs/, so a folder that wants its documents pinned writes its own file.
//
// What it pins, and why each one is worth a test rather than a promise:
//
//   - **The four documents exist and each says what it is.** The folder's
//     shape is the spec-to-pr one (a spec, a reconciliation, a plan, a
//     README), and a reader arriving from the issue must find all four.
//   - **spec.md is the issue, byte for byte.** It is the folder's only
//     document that is not a reading: the issue's words are what the design
//     is answerable to, so the headings, the three quoted sentences and the
//     checklist line are pinned as written. A paraphrase is a claim the
//     owner never made.
//   - **The decision block is exactly D1260–D1277, with D1278–D1289 held.**
//     Taken above D1189 on `main` and above the blocks #1676 and the
//     stateless-orchestrator work hold (reconciliation §5). A number written
//     twice in two work streams is the collision the block exists to avoid,
//     and a number *reused* would draw a wrong figure with no error.
//   - **Every decision costs its alternatives.** The point of the folder is
//     that the owner can answer, which needs the price of the other
//     readings. A decision with no recommendation, or with a recommendation
//     and no cost, is a decision the owner cannot take.
//   - **Every decision carries the owner's answer.** All eighteen were
//     answered on 2026-11-04 — three by name, fifteen as recommended — and
//     each section says so under its own question: a status line that read as
//     ruled while a section still asked would leave the build unsure which of
//     the two to believe.
//   - **Every finding says what the claim is and what the code does.** The
//     reconciliation's value is that it was read against the tree rather
//     than the designs: a finding that cites no code is a paragraph.
//   - **The plan names eight tasks, each with a red step and a file budget,
//     and the file budget is the brief's twelve.** A task with no red step
//     is a task whose test was written after the code.
//   - **No document in the folder carries a secret-shaped literal.** The
//     folder's subject is a credential stored once for 65 repositories, so
//     it is exactly the place a real value would be pasted "for
//     illustration".
//   - **The sentences outside the folder that the feature cannot ship
//     without.** D1272 makes five documents load-bearing — a reach a reader
//     believes and acts on — and all but one of them sit outside this folder,
//     where an operator meets the scope of a secret name, the setup flow's
//     prompts and the `set` help at the command itself: three of those W6
//     writes, and the help is W3's, pinned here because nothing else reads it.
//
// Nothing here asserts the design is *right*: the decisions are the owner's,
// and this file pins that the folder records them rather than re-argues them.
// It asserts that the folder is complete enough to be built from.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	// projectWorkflowSecretsDir is the folder, relative to this package's
	// directory (the tests run with internal/cli as the working directory).
	projectWorkflowSecretsDir = "../../docs/project-workflow-secrets"

	// The folder's four documents, by name. Every one of them is hand-written
	// and none is generated, so a missing one is a missing argument rather
	// than a stale build artifact.
	pwsReadme    = "README.md"
	pwsSpec      = "spec.md"
	pwsReconcile = "reconciliation.md"
	pwsPlan      = "implementation-plan.md"
)

// readProjectWorkflowSecrets reads one of the folder's documents, failing the
// test rather than returning an empty string: an assertion made against a
// document that could not be read would pass on nothing.
func readProjectWorkflowSecrets(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectWorkflowSecretsDir, name))
	if err != nil {
		t.Fatalf("reading %s/%s: %v", projectWorkflowSecretsDir, name, err)
	}
	return string(data)
}

// TestDocsProjectWorkflowSecretsFolderIsComplete: the four documents, none empty,
// and each carrying a status line — the fact docs/managed-agent-converge/
// carries in its README and this folder carries in each file, because a
// reader who lands on reconciliation.md directly must be able to see that
// nothing is built and nothing is approved.
func TestDocsProjectWorkflowSecretsFolderIsComplete(t *testing.T) {
	for _, name := range []string{pwsReadme, pwsSpec, pwsReconcile, pwsPlan} {
		doc := readProjectWorkflowSecrets(t, name)
		if strings.TrimSpace(doc) == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		if !strings.Contains(doc, "**Status:") {
			t.Errorf("%s carries no status line: every document in this folder states what it is", name)
		}
	}
	// The three reading documents must say the same thing about the build, in
	// the folder's own words. spec.md is the exception on purpose: it is the
	// issue, and the issue does not know what a design folder is.
	for _, name := range []string{pwsReadme, pwsReconcile, pwsPlan} {
		doc := readProjectWorkflowSecrets(t, name)
		if !strings.Contains(doc, "nothing is built") {
			t.Errorf("%s does not say that nothing is built", name)
		}
	}
	// The owner answered all eighteen on 2026-11-04, so the status line says so
	// and names what that unlocks: a reader who takes an answered design for an
	// open one does not start the build, and one who takes it for approved
	// without the slot does not know what it lands in.
	if doc := readProjectWorkflowSecrets(t, pwsReconcile); !strings.Contains(doc, "**The owner answered all eighteen on 2026-11-04, all as recommended**") ||
		!strings.Contains(doc, "the build may start") {
		t.Error("reconciliation.md does not state the owner's answers and what they start")
	}
}

// TestDocsProjectWorkflowSecretsSpecIsTheIssueVerbatim: the issue's own headings,
// the three sentences the design is answerable to, and the checklist line the
// issue ends with. The assertions are made byte for byte rather than
// whitespace-normalized, because the point of the file is that it is a copy.
func TestDocsProjectWorkflowSecretsSpecIsTheIssueVerbatim(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsSpec)
	for _, want := range []string{
		// The issue's own title line and its three section headings.
		"# Issue #1678: secrets: let a workflow secret be stored once for the project, behind an allow-list",
		"### The problem",
		"### What you would like",
		"### Help",
		// The problem, as the issue states it: the per-repository rule, its
		// source in the help text, who may be stored project-wide today, and
		// the two costs the issue counts.
		"A workflow's own secret (`workflows.<w>.secrets` in `fugaro.yaml`) is always stored per repository.",
		`"A workflow's own secret has no project-wide meaning and is stored in the repository's own container as before."`,
		"Only the reserved names (the API key, the GitHub App key, a provider's key, an allow-listed reserved secret, a named Claude account's token) can be stored once for the project.",
		"about 130 hidden prompts, and 130 copies to rotate when the password changes.",
		// The ask, including the two properties the design may not trade away:
		// the list is operator-local, and a repository's own copy still wins.
		"gated the way the project-wide Bitbucket token already is: an operator-local allow-list (like `project_secrets.<name>.allow_data_to`, wildcards within a segment)",
		"so a project copy never widens to a repository the operator did not list.",
		"A repository's own copy would still win over the project's, as it does for the reserved names.",
		// The checklist line, and the fact that it is the file's last line: a
		// reader copying the issue into a tracker gets the whole thing.
		"- [ ] I could help build, review or test this",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("spec.md does not carry the issue verbatim: %q is missing", want)
		}
	}
	// The issue states no scale figure other than the two the design quotes
	// as the issue's own; a number invented here would be the folder's, not
	// the issue's. The one figure the issue gives is the repository count.
	if !strings.Contains(doc, "about 65 Maven services") {
		t.Error("spec.md does not carry the issue's own example (about 65 Maven services)")
	}
	if !strings.HasSuffix(strings.TrimRight(doc, "\n"), "- [ ] I could help build, review or test this") {
		t.Error("spec.md does not end with the issue's checklist line: the copy has grown something the issue does not have")
	}
}

// TestDocsProjectWorkflowSecretsDecisionsAreD1260ToD1277AndNoFurther: the numbers
// the reconciliation writes, in order, with none skipped and none outside the
// block. D1278–D1289 are reserved and unused, so a decision heading there is
// a number taken twice (reconciliation §5).
func TestDocsProjectWorkflowSecretsDecisionsAreD1260ToD1277AndNoFurther(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	var got []int
	for _, m := range regexp.MustCompile(`(?m)^### D(\d+)\. `).FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("decision heading %q does not carry a number", m[0])
		}
		got = append(got, n)
	}
	if len(got) == 0 {
		t.Fatal("reconciliation.md writes no decision of the form `### D<number>. `")
	}
	for i, n := range got {
		if want := 1260 + i; n != want {
			t.Fatalf("decision %d of the block is D%d, want D%d: the block is contiguous from D1260", i+1, n, want)
		}
	}
	if last := got[len(got)-1]; last != 1277 {
		t.Errorf("the highest decision written is D%d, want D1277: D1278-D1289 are reserved and unused", last)
	}
	// The reserved half must be named as reserved, and the block's top must
	// be stated, or a reader cannot tell whether the design ran out of
	// decisions or out of block.
	for _, want := range []string{"D1278–D1289", "D1260–D1289"} {
		if !strings.Contains(doc, want) {
			t.Errorf("reconciliation.md does not name the block %s", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsNumberBlockIsReadAboveTheOpenWork: §5's facts. The
// numbers are read by grepping the trees, and the block is taken above the
// highest one claimed anywhere — so the file must name the ceiling it was
// taken above, and the two work streams that hold the numbers under it. A
// later task re-runs the commands rather than trusting the conclusion, which
// is why the commands themselves are pinned here.
func TestDocsProjectWorkflowSecretsNumberBlockIsReadAboveTheOpenWork(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	for _, want := range []string{
		"D1189",
		"#1676",
		"D1190–D1209",
		"D1219",
		"D1220–D1259",
		"b41709ea",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("reconciliation.md §5 does not carry %q: the number block must name what it was read above", want)
		}
	}
	// The README states the same block, and a reader arriving there must not
	// have to open the reconciliation to learn that the numbers are free.
	readme := readProjectWorkflowSecrets(t, pwsReadme)
	for _, want := range []string{"D1260–D1289", "D1260–D1277", "D1278–D1289", "D1189"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md does not carry %q of the number block", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsRecordsTheOwnersAnswers: the owner answered all
// eighteen on 2026-11-04 — D1260, D1266 and D1267 by name (`workflow_secrets`,
// refuse, untouched) and the other fifteen **as recommended**. A decision has to
// say which of the two it is in its own section, because a status line carrying
// the news while the section still reads as a request is an inconsistency a
// reader cannot resolve — and the fifteen have to say "as recommended" rather
// than spell a ruling out, because a ruling written here that the
// recommendation does not carry would be this file's decision and not the
// owner's.
func TestDocsProjectWorkflowSecretsRecordsTheOwnersAnswers(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	byName := map[string]string{
		"D1260.": "workflow_secrets",
		"D1266.": "refuse",
		"D1267.": "untouched",
	}
	sections := decisionSections(t, doc)
	if len(sections) == 0 {
		t.Fatal("no decision section was found")
	}
	byNameSeen, asRecommended := 0, 0
	for _, s := range sections {
		id := strings.Fields(strings.TrimPrefix(s.heading, "### "))[0]
		if !strings.Contains(s.body, "**Answered 2026-11-04") {
			t.Errorf("%s carries no answer marker, and the owner answered all eighteen on 2026-11-04", s.heading)
			continue
		}
		if ruling, ok := byName[id]; ok {
			byNameSeen++
			if !strings.Contains(s.body, ruling) {
				t.Errorf("%s does not carry the owner's ruling (%q)", s.heading, ruling)
			}
			continue
		}
		asRecommended++
		if !strings.Contains(s.body, "**Answered 2026-11-04: as recommended.**") {
			t.Errorf("%s is one of the fifteen the owner answered as recommended and does not say so", s.heading)
		}
	}
	if byNameSeen != len(byName) {
		t.Errorf("%d of the three decisions the owner answered by name were found, want %d", byNameSeen, len(byName))
	}
	if asRecommended != 15 {
		t.Errorf("%d decisions are marked as answered as recommended, want the fifteen that are", asRecommended)
	}
	// The README and the plan carry the same status, and every half of it
	// matters: how many were answered, that the answers were the
	// recommendations, and where the build lands. A task a reader believes is
	// still open does not get built, and one believed approved without a slot
	// gets built in the wrong release.
	for _, name := range []string{pwsReadme, pwsPlan} {
		other := readProjectWorkflowSecrets(t, name)
		for _, want := range []string{"all eighteen", "as recommended", "the build may start", "0.19.0"} {
			if !strings.Contains(other, want) {
				t.Errorf("%s does not carry %q: the status line has to say the block is answered, how, and where it lands", name, want)
			}
		}
		for _, id := range []string{"D1260", "D1266", "D1267"} {
			if !strings.Contains(other, id) {
				t.Errorf("%s does not name %s, which the owner answered by name", name, id)
			}
		}
	}
}

// TestDocsProjectWorkflowSecretsEveryDecisionIsAnswerable: each decision section
// must carry a recommendation and the cost of at least one alternative. The
// owner answers these; a decision that recommends nothing, or that costs
// nothing, is not one a reader can answer.
func TestDocsProjectWorkflowSecretsEveryDecisionIsAnswerable(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	sections := decisionSections(t, doc)
	if len(sections) == 0 {
		t.Fatal("no decision section was found")
	}
	for _, s := range sections {
		if !strings.Contains(s.body, "**Recommendation.**") {
			t.Errorf("%s carries no recommendation", s.heading)
		}
		if !strings.Contains(s.body, "**Cost of the alternative") {
			t.Errorf("%s does not cost its alternatives", s.heading)
		}
		// A recommendation the design cannot defend against the tree is a
		// preference. Every decision here was reached from a finding.
		if !strings.Contains(s.body, "F") && !strings.Contains(s.body, "§") {
			t.Errorf("%s cites neither a finding nor another section: it is an opinion", s.heading)
		}
	}
}

// TestDocsProjectWorkflowSecretsFindingsRunF1ToF20: the findings are contiguous
// and each states its claim and what the code does. A finding that is a
// paragraph about the design rather than a reading of the tree is what the
// reconciliation exists not to be.
func TestDocsProjectWorkflowSecretsFindingsRunF1ToF20(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	re := regexp.MustCompile(`(?m)^### (F\d+)\. `)
	ms := re.FindAllStringSubmatch(doc, -1)
	if len(ms) == 0 {
		t.Fatal("reconciliation.md writes no finding of the form `### F<number>. `")
	}
	want := make([]string, 0, len(ms))
	for i := range ms {
		want = append(want, "F"+strconv.Itoa(i+1))
	}
	for i, m := range ms {
		if m[1] != want[i] {
			t.Fatalf("finding %d is %s, want %s: the findings are contiguous from F1", i+1, m[1], want[i])
		}
	}
	if last := ms[len(ms)-1][1]; last != "F20" {
		t.Errorf("the highest finding is %s, want F20", last)
	}
	var bodies []string
	bounds := re.FindAllStringIndex(doc, -1)
	for i, b := range bounds {
		end := len(doc)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		bodies = append(bodies, doc[b[0]:end])
	}
	for i, body := range bodies {
		if !strings.Contains(body, "**What the code does.**") {
			t.Errorf("finding F%d does not say what the code does", i+1)
		}
		if !strings.Contains(body, "**Claim of the ") {
			t.Errorf("finding F%d does not state the claim it reconciles", i+1)
		}
	}
}

// TestDocsProjectWorkflowSecretsPlanNamesItsEightTasks: W0–W7, each with a red
// step, an interface or a rule, a named-test line and a file budget at or
// under the brief's twelve. The file budget is parsed rather than asserted
// by eye: a task that grew past twelve files would otherwise be a plan that
// cannot be reviewed in one sitting.
func TestDocsProjectWorkflowSecretsPlanNamesItsEightTasks(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsPlan)
	head := regexp.MustCompile(`(?m)^## (W\d+)\. `)
	ms := head.FindAllStringSubmatch(doc, -1)
	if len(ms) != 8 {
		t.Fatalf("implementation-plan.md names %d tasks, want 8 (W0-W7)", len(ms))
	}
	bounds := head.FindAllStringIndex(doc, -1)
	sec := 0
	for i, m := range ms {
		if want := "W" + strconv.Itoa(i); m[1] != want {
			t.Fatalf("task %d is %s, want %s", i+1, m[1], want)
		}
		end := len(doc)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		body := doc[bounds[i][0]:end]
		if !strings.Contains(body, "**Red step.**") {
			t.Errorf("%s has no red step", m[1])
		}
		if !strings.Contains(body, "**Files (") {
			t.Errorf("%s names no file budget", m[1])
		}
		for _, n := range regexp.MustCompile(`\*\*Files \(≤(\d+)\)`).FindAllStringSubmatch(body, -1) {
			v, err := strconv.Atoi(n[1])
			if err != nil {
				t.Fatalf("%s: unparsable file budget %q", m[1], n[1])
			}
			if v > 12 {
				t.Errorf("%s budgets %d files, and the brief's bound is 12", m[1], v)
			}
		}
		if strings.Contains(body, "**SEC**") {
			sec++
		}
	}
	// The SEC flags are the plan's own claim about where the boundaries are,
	// and the README counts them. Today six of the eight carry one; the count
	// is asserted so that a task losing its flag is a failing test rather
	// than a quieter plan.
	if sec != 6 {
		t.Errorf("%d of the eight tasks carry a SEC flag, want 6 (the README says so)", sec)
	}
	// The order and the critical path are the two facts that decide what may
	// be built in parallel, so both are pinned.
	for _, want := range []string{"Critical path", "W0 → W1 → W2 → W3 → W6 → W7", "| Level | Tasks | Needs (code) |"} {
		if !strings.Contains(doc, want) {
			t.Errorf("implementation-plan.md does not carry %q", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsPlanProtectsTheTwoPropertiesTheIssueNames: the
// issue asks for two things that are not negotiable — the list is
// operator-local, and a repository's own copy still wins. Both are decisions
// in the reconciliation (D1260's alternatives and D1264) and both must be
// visible in the plan that would build them, or a task could take either away
// without a reviewer noticing.
func TestDocsProjectWorkflowSecretsPlanProtectsTheTwoPropertiesTheIssueNames(t *testing.T) {
	rec := readProjectWorkflowSecrets(t, pwsReconcile)
	for _, want := range []string{
		// The operator-local block, and the refusal of the published variant.
		"workflow_secrets",
		"operator-local",
		// The precedence, cited to the function that holds it.
		"resolveSecretID",
		"the repository's own copy wins",
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("reconciliation.md does not carry %q", want)
		}
	}
	// The eligibility rule is the design's second guarantee — absent or empty
	// means nobody, and no sweep for a workflow secret (D1262) — and it is the
	// one a later task could reverse with a line. Both documents state it, and
	// the plan names the task that holds the test.
	for _, want := range []string{"Absent or empty means nobody", "ProjectScopeByDefault"} {
		if !strings.Contains(rec, want) {
			t.Errorf("reconciliation.md does not carry %q: D1262's rule is what keeps the entry the whole gate", want)
		}
	}
	plan := readProjectWorkflowSecrets(t, pwsPlan)
	// The plan must say that the repository's own copy winning is not this
	// work's to change, and that a shared project job may not mount one.
	for _, want := range []string{"resolveSecretID", "shared project job", "D1266", "no sweep for a workflow secret", "TestWorkflowSecretHasNoProjectScopeDefault"} {
		if !strings.Contains(plan, want) {
			t.Errorf("implementation-plan.md does not carry %q", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsReadmeLinksItsSiblings: every document the README
// describes must exist, and the folder must not have grown a fifth document
// nobody describes. A design folder whose README is stale is one whose table
// of contents lies.
func TestDocsProjectWorkflowSecretsReadmeLinksItsSiblings(t *testing.T) {
	readme := readProjectWorkflowSecrets(t, pwsReadme)
	for _, name := range []string{pwsSpec, pwsReconcile, pwsPlan} {
		if !strings.Contains(readme, "("+name+")") {
			t.Errorf("README.md does not link %s", name)
		}
	}
	entries, err := os.ReadDir(projectWorkflowSecretsDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}
	if len(files) != 4 {
		t.Errorf("%s holds %d Markdown files (%s), want the four the README describes", projectWorkflowSecretsDir, len(files), strings.Join(files, ", "))
	}
}

// TestDocsProjectWorkflowSecretsFolderCarriesNoSecretShapedLiteral: the folder's
// subject is a credential stored once for many repositories, which is exactly
// where a real value would be pasted as an illustration. The three patterns are
// the shapes a value takes rather than a list of vendors — a PEM header, and
// the two key prefixes a pasted credential usually keeps — and they are the
// three the folder's own subject could plausibly acquire.
func TestDocsProjectWorkflowSecretsFolderCarriesNoSecretShapedLiteral(t *testing.T) {
	entries, err := os.ReadDir(projectWorkflowSecretsDir)
	if err != nil {
		t.Fatal(err)
	}
	shapes := []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9-]{8,}`),
		regexp.MustCompile(`\bghp_[A-Za-z0-9]{8,}`),
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		doc := readProjectWorkflowSecrets(t, e.Name())
		for _, re := range shapes {
			if m := re.FindString(doc); m != "" {
				t.Errorf("%s carries a secret-shaped literal (%s)", e.Name(), re.String())
			}
		}
	}
}

// TestDocsProjectWorkflowSecretsSectionsAreNumberedAndOrdered: the
// reconciliation's numbered sections, in order. The threat model sits between
// what the design leaves alone and the questions it asks, so the decisions are
// §4 — a reader following a "§3" from another document has to land on the
// section they meant, and the folder's cross-references are written to it.
func TestDocsProjectWorkflowSecretsSectionsAreNumberedAndOrdered(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	want := []string{
		"Findings: what exists",
		"What this design leaves alone",
		"The threat model",
		"Decisions",
		"The number block",
		"The residual this design refuses to hide",
	}
	ms := regexp.MustCompile(`(?m)^## (\d+)\. (.+)$`).FindAllStringSubmatch(doc, -1)
	if len(ms) != len(want) {
		t.Fatalf("reconciliation.md carries %d numbered sections, want %d", len(ms), len(want))
	}
	for i, m := range ms {
		if n := strconv.Itoa(i + 1); m[1] != n {
			t.Errorf("section %d is numbered %s, want %s", i+1, m[1], n)
		}
		if got := strings.TrimSpace(m[2]); got != want[i] {
			t.Errorf("section %s is %q, want %q", m[1], got, want[i])
		}
	}
}

// TestDocsProjectWorkflowSecretsThreatModelAnswersWhoCanReadTheValue: the
// threat model §3 carries, and the three questions a security reviewer asks of
// this design before any decision is answered — what a rotation reaches, what
// the doctor says about a shadowed name, and who can read the value.
//
// Each is pinned to the code that answers it rather than to a sentence: a
// threat model whose claims cannot be re-read against the tree is the prose
// the folder exists not to be. The last of the three is an explicit statement
// that enforcement is the plan-time candidate map and **not** an independent
// Terraform check, because that is the claim a reader would otherwise supply
// from the shape of the Terraform and be wrong about.
func TestDocsProjectWorkflowSecretsThreatModelAnswersWhoCanReadTheValue(t *testing.T) {
	doc := readProjectWorkflowSecrets(t, pwsReconcile)
	model := sectionBody(t, doc, "3. The threat model")
	for _, want := range []string{
		// Rotation: one version add, resolved at container start.
		"ROTATION",
		`version = "latest"`,
		"deploy/terraform/gcp/modules/workflow/job.tf:67",
		// The doctor check, and why "no new doctor check" is not an omission.
		"secret-scope-shadowed",
		"internal/cli/doctor.go:1000",
		"no new doctor check",
		// Who can read the value, and where the one decision point is. The
		// build account is in the list because the job account is not the only
		// member: a workflow secret is a name the repository's build reads too,
		// and its grant is written from the same plan-time map.
		"secretAccessor",
		"local.secrets",
		"deploy/terraform/gcp/modules/repo/build.tf:15",
		"build account",
		"internal/infra/spec.go:1374",
		"not an independent Terraform check",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("reconciliation.md §3 does not carry %q: the threat model has to answer it against the code", want)
		}
	}
	// The section has to be a section, not three sentences folded into §2: a
	// reviewer who cannot find it has not been given a threat model.
	if !strings.Contains(model, "**Recommendation.**") && !strings.Contains(model, "**Claim.") {
		t.Error("reconciliation.md §3 states no claim of its own: a threat model reads as claims, each answerable against the tree")
	}
}

// TestDocsProjectWorkflowSecretsPlanSequencesAfterTheSecretsRmWork: the plan's
// dependency on the config-management work's G8 (`fugaro secrets rm` and the
// secret policy flags, `internal/cli/secrets_rm.go`). W3 edits
// `internal/cli/secrets.go`, which G8 edits for the same family's flags, and
// `rm` is the command that has to withdraw a project-scoped workflow secret:
// a plan that landed W3 first would leave `rm` removing the wrong block's entry
// and the design silent about it.
func TestDocsProjectWorkflowSecretsPlanSequencesAfterTheSecretsRmWork(t *testing.T) {
	plan := readProjectWorkflowSecrets(t, pwsPlan)
	order := sectionBody(t, plan, "2. Order, and what forces it")
	for _, want := range []string{"G8", "internal/cli/secrets_rm.go", "fugaro secrets rm"} {
		if !strings.Contains(order, want) {
			t.Errorf("implementation-plan.md §2 does not name %q: the dependency is what decides the order", want)
		}
	}
	w3 := taskBody(t, plan, "W3")
	for _, want := range []string{
		"G8",
		// What `rm` does for the new block, said rather than left to the task.
		"workflow_secrets.<name>",
		"secrets rm",
		// The other G8 flag the task has to rule on: one of the two readings is
		// chosen and said, rather than left for a reviewer to infer from the
		// code the task happens to write. The plan takes the reading that keeps
		// one writer per registry row, and it owes the session refusal a test.
		"--allow-data-to",
		"workflow_secrets.<name>.allow_data_to",
		"TestSecretsSetAllowDataToOnAWorkflowSecretRefusesAnAgentSession",
		// The refusal the flag already carries in a coding agent's session is
		// G8's, and it is the one a session gets rather than the widening path:
		// a task that reached the new block by a route of its own would have
		// re-opened the door that refusal closes.
		"The outright refusal is G8's",
		"TestSecretsSetPolicyRefusesAnAgentSession",
		// `proxy` is the other half of the flag, and D1275 refuses the field.
		"--proxy",
	} {
		if !strings.Contains(w3, want) {
			t.Errorf("implementation-plan.md W3 does not carry %q: the task that edits secrets.go owns the rm half too", want)
		}
	}
	// The shared-files table is what tells a task which file another task owns.
	shared := sectionBody(t, plan, "4. The shared-files table")
	for _, want := range []string{"`internal/cli/secrets_rm.go`", "`internal/cli/secrets.go`", "G8"} {
		if !strings.Contains(shared, want) {
			t.Errorf("implementation-plan.md §4 carries no %q row: a shared file no table names is the merge the table exists to prevent", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsProfileRefusalIsTheDeployersFence pins D1266's
// placement, which a security review corrected. `projectpolicy.CheckProfiles`
// takes the policy's two maps and never the operator's local config, so a
// refusal written beside the Claude OAuth token's is evaluated on machines
// where the allow-list is absent — a teammate's, or a reader of the published
// node — and lets the profile through: it fails open, which is the opposite of
// what a refusal about a credential's reach is for. The fence that fails
// closed is the deployer's, `internal/infra/projectjobs.go`'s
// `projectJobSecrets`, which refuses every profile secret `projectSecretEnv`
// cannot map. The folder has to say that where the decision, the finding and
// the task are, or a task reads the precedent as the place to write the rule.
func TestDocsProjectWorkflowSecretsProfileRefusalIsTheDeployersFence(t *testing.T) {
	rec := readProjectWorkflowSecrets(t, pwsReconcile)
	d1266 := subBody(t, rec, "D1266")
	for _, want := range []string{
		"internal/infra/projectjobs.go",
		"projectJobSecrets",
		"projectSecretEnv",
		// The reason the shape's own home is the wrong one.
		"CheckProfiles",
		"fail open",
	} {
		if !strings.Contains(d1266, want) {
			t.Errorf("reconciliation.md D1266 does not carry %q: the refusal has to be placed where it fails closed", want)
		}
	}
	f9 := subBody(t, rec, "F9")
	for _, want := range []string{"internal/infra/projectjobs.go", "projectJobSecrets", "projectSecretEnv"} {
		if !strings.Contains(f9, want) {
			t.Errorf("reconciliation.md F9 does not cite %q: the finding is a reading of the tree, not of the shape", want)
		}
	}
	plan := readProjectWorkflowSecrets(t, pwsPlan)
	w5 := taskBody(t, plan, "W5")
	for _, want := range []string{"ProjectJobs", "TestProfileRefusesAProjectWorkflowSecret", "projectSecretEnv"} {
		if !strings.Contains(w5, want) {
			t.Errorf("implementation-plan.md W5 does not carry %q: the task pins the fence the tree already has", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsProviderRuleIsWhereProvidersIsVisible pins
// D1261's split, which a security review corrected. W0's
// `ValidateWorkflowSecrets(secrets)` takes the block and nothing else — the
// signature `ValidateProjectSecrets` has — so the rules that read the
// operator's configured providers (a provider's own `secret:`, and the
// convention name `ReservedProviderSecret` derives from the provider map)
// cannot be written there: they would be evaluated with `lc.Providers`
// unavailable. They belong where it is in hand, which is `internal/localcfg`'s
// validate, in the shape `config.CheckWorkflowProviderSecrets` already has.
//
// The predicate's own half is here too: W2 consults the new block only for a
// name no other source claims, so a hand-written entry cannot widen a name the
// sweep or a provider already answers for — the predicate fails closed.
func TestDocsProjectWorkflowSecretsProviderRuleIsWhereProvidersIsVisible(t *testing.T) {
	rec := readProjectWorkflowSecrets(t, pwsReconcile)
	d1261 := subBody(t, rec, "D1261")
	for _, want := range []string{
		"ValidateWorkflowSecrets(secrets)",
		"lc.Providers",
		"internal/localcfg",
		"CheckWorkflowProviderSecrets",
	} {
		if !strings.Contains(d1261, want) {
			t.Errorf("reconciliation.md D1261 does not carry %q: the key rule is split by where each half can be computed", want)
		}
	}
	plan := readProjectWorkflowSecrets(t, pwsPlan)
	w0 := taskBody(t, plan, "W0")
	if !strings.Contains(w0, "a function of the name alone") {
		t.Error("implementation-plan.md W0 does not say what its validator can hold: the rules it cannot read belong to another task")
	}
	w1 := taskBody(t, plan, "W1")
	for _, want := range []string{"ProviderBySecret", "ReservedProviderSecret", "CheckWorkflowProviderSecrets"} {
		if !strings.Contains(w1, want) {
			t.Errorf("implementation-plan.md W1 does not carry %q: the provider half of the key rule is this task's", want)
		}
	}
	w2 := taskBody(t, plan, "W2")
	for _, want := range []string{
		"projectSecretEnv",
		"is not widened to the new block",
		"only for a name no other source claims",
	} {
		if !strings.Contains(w2, want) {
			t.Errorf("implementation-plan.md W2 does not carry %q: it is the task that could undo the fence, so it has to state what it does not touch", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsResidualNamesTheManagedExposure: §6 is the
// folder's list of what the design does not fix, and one exposure belongs there
// that the gate itself creates — a managed project admits the grant the new
// block produces, because the managed cover judges a resource's type and the
// prefix of the name it carries, never the block the entry came from. The
// section has to say that, and say that `project_secrets` is exposed the same
// way today: an exposure a reader discovers for themselves after reading a
// residual list is worse than one the list names.
func TestDocsProjectWorkflowSecretsResidualNamesTheManagedExposure(t *testing.T) {
	rec := readProjectWorkflowSecrets(t, pwsReconcile)
	residual := sectionBody(t, rec, "6. The residual this design refuses to hide")
	for _, want := range []string{
		"internal/infra/tf/managed_cover.go",
		// The type list, the reason, and the field the gate reads the name out
		// of: without all three the sentence is a claim about a gate rather
		// than a reading of it.
		"(`:68`)",
		"(`:88`)",
		"(`:155`)",
		"secret_id",
		"fugaro managed",
		// The comparison that keeps it a residual rather than a new hole.
		"`project_secrets` has exactly the same exposure today",
	} {
		if !strings.Contains(residual, want) {
			t.Errorf("reconciliation.md §6 does not carry %q: the managed exposure belongs in the residual", want)
		}
	}
	// D1270's unknown-key citation is the write path's, and it moved when
	// `fugaro settings set` grew the managed gate. The read path's own refusal
	// is a different line in a different file, and saying which is which is what
	// makes the citation re-readable.
	d1270 := subBody(t, rec, "D1270")
	for _, want := range []string{"settings_set.go:412", "settings.go:170"} {
		if !strings.Contains(d1270, want) {
			t.Errorf("reconciliation.md D1270 does not cite %q: the registry answer has to name the line it comes from", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsReachIsSaidWhereAPersonMeetsIt (W6, D1272,
// D1273): the feature's reach is invisible in a repository's own fugaro.yaml —
// nothing in that file says its `db-password` is stored once for sixty-five
// repositories — so the pages a person reads round the command are the only
// place the decision is made. Three of them said a workflow's own secret could
// not be shared at all, which was true before this design and is not true of a
// name an operator's `workflow_secrets` entry covers:
//
//   - docs/project-layer.md's "outside this table" paragraph, where the scope
//     of a secret name is decided and the layer's own non-secrets are
//     enumerated;
//   - docs/design/project-secrets.md §4's list of what `fugaro secrets set`
//     accepts, where a reader of the parent design lands on the refusal this
//     design adds a second form beside — a pointer, not a rewrite;
//   - the setup skill, whose step 9 is the other place a value is stored, and
//     whose sentence about a covered name has to hold for a workflow secret
//     too.
//
// The reach itself is elsewhere and is not re-argued here: the store-time note
// and the `set` help say it at the command (W3, secrets_test.go's own pins),
// SECURITY.md's project-scope section says it for the reader who never opens a
// terminal, and the folder states it in full. The sentences wrap in the files,
// so the assertions are made against the whitespace-normalized text.
//
// The pins are word for word, and the banned strings are the wording these
// pages carried before W6: a sentence that leaves the block out is the claim
// this task exists to replace, so a revert has to fail here rather than read
// plausibly.
func TestDocsProjectWorkflowSecretsReachIsSaidWhereAPersonMeetsIt(t *testing.T) {
	cases := []struct {
		path   string
		want   []string
		banned []string
	}{
		{"docs/project-layer.md", []string{
			// The block, its operator-locality, and the pointer: a reader who
			// stops at this paragraph must be able to reach the design that
			// decides what the entry may name and who may mount it.
			"a workflow's own secret is shared once for the project through the operator-local `workflow_secrets` block instead",
			"[project-workflow-secrets/](project-workflow-secrets/)",
		}, []string{
			// The unqualified sentence: the parenthesis that names the block
			// is what makes it true, so the clause may not stand alone again.
			"cannot be shared through a workflow's block;",
		}},
		{"docs/design/project-secrets.md", []string{
			"which has no project-wide meaning (§3; a workflow's own secret has a project-wide form of its own since 0.19.0",
			"[project-workflow-secrets/](../project-workflow-secrets/)",
		}, []string{
			"which has no project-wide meaning (§3).",
		}},
		{"plugin/skills/setup/SKILL.md", []string{
			// The skill says it in its own vocabulary — "covered" is what it
			// already calls a name the project's copy answers for — because
			// the skill carries a hard byte budget (plugin/skills_lint_test.go's
			// 24576) and the sentence has to pay for itself; the block is named
			// where the block is decided, in the two documents above.
			"a covered name is not asked for, nor is a covered workflow secret",
		}, nil},
	}
	for _, tc := range cases {
		doc := strings.Join(strings.Fields(readDoc(t, tc.path)), " ")
		for _, want := range tc.want {
			if !strings.Contains(doc, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("%s does not say %q: it is where a person decides whether storing one value for many repositories is safe", tc.path, want)
			}
		}
		for _, banned := range tc.banned {
			if strings.Contains(doc, strings.Join(strings.Fields(banned), " ")) {
				t.Errorf("%s still says %q: a workflow's own secret can be stored once for the project through the operator-local workflow_secrets block, and a page that says it cannot is a reach a reader believes and acts on wrongly", tc.path, banned)
			}
		}
	}
	// The sentences say the block is built, so they may only say it while the
	// store path reads it: the doc pins and this one fail together rather than
	// outliving the code they describe.
	for _, want := range []string{"workflow_secrets", "lc.WorkflowSecrets", "projectSecretAllowed"} {
		if !strings.Contains(readDoc(t, "internal/cli/secrets.go"), want) {
			t.Errorf("the pages say a workflow secret is stored once for the project through the new block, and internal/cli/secrets.go no longer carries %q", want)
		}
	}
}

// TestDocsProjectWorkflowSecretsSetHelpSaysTheWorkflowBlock (W6, D1272): the
// other sentence D1272 makes load-bearing is the `set` help's, which W3 wrote
// and nothing read: the generated page (website/content/cli-reference.md)
// proves the help reached the document, never that the document says what the
// store path does. It is the text a person reads at the command, before
// typing a value, so it is pinned here — the two readings it has to give are
// which block stores a workflow secret once for the project, and that an
// entry with an empty list mounts nowhere (D1274). The help wraps, so the
// assertions are made against its whitespace-normalized text.
func TestDocsProjectWorkflowSecretsSetHelpSaysTheWorkflowBlock(t *testing.T) {
	help := flattenDoc(newSecretsSetCmd().Long)
	for _, want := range []string{
		"or a secret a workflow declares that your local config's workflow_secrets block lists at least one repository for",
		"A workflow's own secret is stored once for the project only through such an entry: with no workflow_secrets entry it stays in the repository's own container as before, and an entry present with an empty allow_data_to is refused, since it would mount nowhere.",
	} {
		if !strings.Contains(help, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("`fugaro secrets set`'s help does not say %q: the store path is decided there, before the value is typed", want)
		}
	}
}

// subBody returns the text under a `### <id>. ...` heading, up to the next
// heading of the same kind, failing the test when no such heading exists.
func subBody(t *testing.T, doc, id string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^### ` + regexp.QuoteMeta(id) + `\. .*$`)
	loc := re.FindStringIndex(doc)
	if loc == nil {
		t.Fatalf("no `### %s. ` section was found", id)
	}
	rest := doc[loc[1]:]
	if next := regexp.MustCompile(`(?m)^#{2,3} `).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

// sectionBody returns the text under a `## <heading>` line, up to the next
// `## ` line, failing the test when no such heading exists.
func sectionBody(t *testing.T, doc, heading string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(heading) + `\n`)
	loc := re.FindStringIndex(doc)
	if loc == nil {
		t.Fatalf("no `## %s` section was found", heading)
	}
	rest := doc[loc[1]:]
	if next := regexp.MustCompile(`(?m)^## `).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

// taskBody returns the text under a `## <id>. ...` task heading, up to the next
// task heading, failing the test when the plan names no such task.
func taskBody(t *testing.T, doc, id string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(id) + `\. .*$`)
	loc := re.FindStringIndex(doc)
	if loc == nil {
		t.Fatalf("implementation-plan.md names no %s task", id)
	}
	rest := doc[loc[1]:]
	if next := regexp.MustCompile(`(?m)^## `).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

// decision is one decision section of the reconciliation: its heading and the
// text under it, up to the next heading of the same kind.
type decision struct{ heading, body string }

// decisionSections splits the reconciliation's `### D<number>. ` sections.
func decisionSections(t *testing.T, doc string) []decision {
	t.Helper()
	re := regexp.MustCompile(`(?m)^### D\d+\. .*$`)
	bounds := re.FindAllStringIndex(doc, -1)
	out := make([]decision, 0, len(bounds))
	for i, b := range bounds {
		end := len(doc)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		out = append(out, decision{heading: strings.TrimSpace(doc[b[0]:b[1]]), body: doc[b[0]:end]})
	}
	return out
}
