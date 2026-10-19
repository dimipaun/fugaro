package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestDocsBudgetTokenChannelDesignIsWritten (plan task 3b.1,
// SECURITY-SENSITIVE):
// the design that moves the run's budget token out of reach of the other runs
// of a shared job exists, prices the four candidates the plan names, states a
// recommendation, says plainly that nothing follows it until the owner
// approves, and keeps the interim ruling D13's own words say the profile
// remains the isolation boundary — for secrets only, since every project job
// account holds the same whole bucket prefixes (Task 3.2's review), which
// leaves D13's "separate profiles" mitigation open for the owner. Its red state is this test: the document
// did not exist.
func TestDocsBudgetTokenChannelDesignIsWritten(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The subject and where it sits.
		"Phase 3b",
		"Task 3b.1",
		// The four candidates the plan lists, each priced.
		"signed URL",
		"Secret Manager",
		"tokens/",
		"execution identity",
		// The constraints it must not break.
		"launcher-overridable",
		"fail closed",
		"profile is the isolation boundary for secrets only",
		"open for the owner to",
		// The verdict and its gate.
		"capability, not a boundary",
		"no code follows this document until the owner approves it",
		// The probe that gates one candidate, and the check that has not run.
		"Check 52",
		// The owner's decisions and the live check the implementation needs.
		"## The owner's open decisions",
		"## The check outline",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsBudgetTokenChannelSection1IsTheSealedChannel: §1 is "the channel as
// it is", and the tree it describes is the sealed one — the object under
// `tokens/` (the §7.2 move), the take that never deletes, and the marker whose
// value is the launch's key rather than the "1" bit. §1 went unread while the
// seal and the move landed: it still said the object was at
// `runs/<slug>/<run>/budget-token`, that `TakeObject` "reads the object, deletes
// it", that the mark was `lspec.Env = ... + "=1"`, and that "The take is the
// consume. TakeObject deletes as it reads" — so an engineer reasoning about the
// channel's threat model from §1 was told the wrong mechanism and pointed at the
// wrong prefix (the 2026-10-19 review's medium finding). The shipped claims are
// required here and the superseded ones refused, both on §1's own text, so §7.1
// or §7.2 cannot stand in for it.
func TestDocsBudgetTokenChannelSection1IsTheSealedChannel(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "## 1. The channel as it is")
	j := strings.Index(doc, "## 2. What phase 3 changes about it")
	if i < 0 || j < i {
		t.Fatalf("%s has no §1 followed by §2: the section that states the shipped channel is gone", path)
	}
	// The section is wrapped, so a phrase can straddle a line break: the
	// assertions below are on prose, not on layout.
	sec := strings.Join(strings.Fields(doc[i:j]), " ")
	for _, want := range []string{
		// The object the launcher writes and the run reads is under tokens/,
		// and it is sealed: §7.1's and §7.2's shipped form.
		"`tokens/<slug>/<run>/budget-token`",
		"**sealed** under a key the launcher draws for this launch",
		// The take unseals and never deletes.
		"**unseals it under the launch's key**",
		"**never deletes it**",
		// The mark carries the launch's key, as a secret override member.
		"lspec.SecretEnv = []string{sealMarker(sealKey)}",
		// And the property that costs the most if it is read backwards: what
		// makes a token usable once is the key, not a delete.
		"since the seal it is *not* the delete that makes it one",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s §1 does not say %q: the section describing the channel as it is has to describe the shipped one — the sealed object under tokens/, the take that unseals and never deletes, and the marker that carries the launch's key", path, want)
		}
	}
	for _, refused := range []string{
		"`runs/<slug>/<run>/budget-token`",
		"reads the object, **deletes it**",
		`lspec.Env = []string{runner.BudgetTokenEnv + "=1"}`,
		"`TakeObject` deletes as it reads",
		"The override carries one bit",
	} {
		if strings.Contains(sec, refused) {
			t.Errorf("%s §1 still says %q, which is the pre-seal channel: the seal and the move to tokens/ are shipped, so §1 must state them (or point at §7.1/§7.2 for them) rather than describe the tree they replaced", path, refused)
		}
	}
}

// TestDocsBudgetTokenChannelAnswersTheBatchSeal (compute backend plan task 14,
// SECURITY-SENSITIVE): the Batch half of the seal's carrier used to be an open
// owner question — the design's own words were "**Open for the owner:** whether
// Batch should ever run a budget-on run, and through which carrier — this
// document names none that satisfies §3's constraints" — and the code refused a
// sealed launch with a message that said "yet", which reads as a mechanism
// somebody owes. The owner answered on 2026-10-19 (the plan's P1): keep the
// refusal and document it as a known limit, because a Batch job's variables are
// the job's definition and the only alternative carrier this design allows is
// the rejected C2. This test holds the three places that answer now live in —
// the design's own paragraph, the compute backend design's §1.6 and its C10
// row, and the refusal `internal/backend/batch/run.go` emits — to the same
// sentence, so one of them cannot be rewritten back into a question without the
// others failing here. It is the docs half of
// TestLaunchRefusesASecretEnv, which holds the message's own shape.
func TestDocsBudgetTokenChannelAnswersTheBatchSeal(t *testing.T) {
	const design = "docs/design/budget-token-channel.md"
	doc := readDoc(t, design)
	i := strings.Index(doc, "**The Batch backend refuses a sealed launch")
	j := strings.Index(doc, "Every symbol below was read on")
	if i < 0 || j < i {
		t.Fatalf("%s has no \"The Batch backend refuses a sealed launch\" paragraph followed by the symbol note: the paragraph that states the seal's Batch answer is gone", design)
	}
	// The paragraph is wrapped, so a phrase can straddle a line break: the
	// assertions below are on prose, not on layout.
	sec := strings.Join(strings.Fields(doc[i:j]), " ")
	for _, want := range []string{
		// The answer, in the operator's words: Batch runs no sealed budget
		// run, and — the correction this test now holds — a minting
		// installation can launch NO run on that backend, because minting is
		// the installation's and an installation has one backend. "Its
		// budget-on path runs on Cloud Run" was the earlier, misleading
		// wording: it reads as though budget-off runs could still go to Batch.
		"Batch runs no sealed budget run",
		"cannot launch any run on Batch",
		"has to stay on `backend: cloud-run`",
		// The full minting condition, which the earlier text left short: the
		// code mints for a budget database with the mode on OR a published
		// policy (internal/cli/launch_budget.go's budgetMintOn).
		"a budget database with budget mode on, or a published policy",
		// The reason, which is what makes the answer a limit rather than a
		// missing mechanism: the key would live in the job's definition.
		"the job's definition",
		// And the reason as the threat model states it: what the carrier adds
		// is a standing copy in the resource and the reaper as a reader, not a
		// boundary that the launchers were behind.
		"a standing copy in the job resource",
		"widen its audience to the reaper",
		"documented limit",
		// The refusal is the code's, and the paragraph says what it is.
		"ErrRejected",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s's \"refuses a sealed launch\" paragraph does not say %q: the owner answered this question (the compute backend plan's P1, 2026-10-19) and the answer is what an operator reads", design, want)
		}
	}
	// And the question is gone, along with the two wordings this test's own
	// review corrected: an "Open for the owner" clause sends a reader looking
	// for a mechanism that is not coming, "budget-on path" promises a run kind
	// that no installation on this backend can have, and "the set the seal
	// exists to exclude" contradicts the seal's own threat model (the
	// launchers are already in the override's audience).
	for _, refused := range []string{
		"Open for the owner:** whether Batch should ever run a budget-on run",
		"an open owner question",
		"a sealed budget run cannot run on Batch yet",
		"runs its budget-on path on Cloud Run",
		"budget-on path",
		"the set the seal exists to exclude",
		"a budget database, a published policy and a signer",
	} {
		if strings.Contains(sec, refused) {
			t.Errorf("%s's \"refuses a sealed launch\" paragraph still says %q: the question it asked is answered (keep the refusal, documented as a known limit), and the wording it was answered in has to survive a reader\n\nparagraph: %s", design, refused, sec)
		}
	}

	// The compute backend design carries the same answer, in §1.6 and in its
	// C10 row, so a reader of the design that owns the backend finds it there
	// too rather than only in the seal's own document.
	const cbDesign = "docs/design/compute-backend.md"
	cb := readDoc(t, cbDesign)
	k := strings.Index(cb, "### 1.6 Secrets")
	l := strings.Index(cb, "### 1.7 Networking")
	if k < 0 || l < k {
		t.Fatalf("%s has no §1.6 followed by §1.7: the section that says what Batch does with secrets is gone", cbDesign)
	}
	sec16 := strings.Join(strings.Fields(cb[k:l]), " ")
	for _, want := range []string{
		"Batch runs no sealed budget run",
		"cannot launch any run on Batch",
		"a budget database with budget mode on, or a published policy",
	} {
		if !strings.Contains(sec16, want) {
			t.Errorf("%s §1.6 does not say %q: §1.6 is where a reader learns what this backend does with a secret, and the seal's answer belongs beside the secretVariables paragraph", cbDesign, want)
		}
	}
	if strings.Contains(sec16, "budget-on path") || strings.Contains(sec16, "budget-on run at all") {
		t.Errorf("%s §1.6 still says \"budget-on path\"/\"budget-on run\": minting is the installation's and an installation has one backend, so there is no run kind an operator can still send to Batch\n\nsection: %s", cbDesign, sec16)
	}
	var c10 string
	for _, line := range strings.Split(cb, "\n") {
		if strings.HasPrefix(line, "| C10 |") {
			c10 = line
			break
		}
	}
	if c10 == "" {
		t.Fatalf("%s has no C10 row: the decision table the answer has to reach is gone", cbDesign)
	}
	for _, want := range []string{"refuse", "sealed launch", "cannot launch any run on Batch"} {
		if !strings.Contains(c10, want) {
			t.Errorf("%s's C10 row does not say %q: §1.6's answer has to reach the decision table too, or the table reads as though the backend carried every secret the design lists\n\nrow: %s", cbDesign, want, c10)
		}
	}

	// And the refusal the code emits says the same sentence — the same three
	// phrases, read out of the source rather than typed twice. The slice is
	// the refusal *message* and not the file: run.go cites this document in
	// its comments too, and an assertion over the whole file cannot see the
	// message's own citation move (which is exactly what the mutation run for
	// this test found, when the citation was rewritten in the message alone
	// and this test still passed).
	run, err := os.ReadFile("../../internal/backend/batch/run.go")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Join(strings.Fields(string(run)), " ")
	const opens = `fmt.Errorf("launching %s: the launch carries`
	start := strings.Index(code, opens)
	if start < 0 {
		t.Fatalf("internal/backend/batch/run.go no longer builds the seal's refusal with %s: this test reads that message", opens)
	}
	end := strings.Index(code[start:], "full, kv.Name)")
	if end < 0 {
		t.Fatalf("internal/backend/batch/run.go's seal refusal no longer ends at `full, kv.Name)`: this test cannot find the message's end")
	}
	message := code[start : start+end]
	for _, want := range []string{
		"the job's definition",
		"docs/design/budget-token-channel.md",
		// The answer, in the words the documents use: the installation, not a
		// run, is what cannot use this backend, and it says what the operator
		// has to do instead. This is the phrase the review of this branch
		// found missing from the refusal.
		"cannot launch any run on Batch",
		"a budget database with budget mode on, or a published policy",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("internal/backend/batch/run.go's refusal does not carry %q, which %s's answer does: the document and the message an operator actually sees have to say the same thing\n\nmessage: %s", want, design, message)
		}
	}
	// The refusal is not allowed to promise a run kind that can still go to
	// Batch, which "runs its budget-on path on Cloud Run" did.
	if strings.Contains(message, "budget-on path") {
		t.Errorf("internal/backend/batch/run.go's refusal still says \"budget-on path\": an installation has one backend, so the refusal has to say that no run of a minting installation can go to Batch\n\nmessage: %s", message)
	}
	// The message is refused rather than carried, and the value is never in
	// it: the entry's name is the most an operator may be shown, which is
	// checked on the real error in
	// internal/backend/batch's TestLaunchRefusesASecretEnv too.
	if strings.Contains(message, "kv.Value") {
		t.Errorf("internal/backend/batch/run.go's refusal formats the secret's value into its text: the value must never appear in a refusal\n\nmessage: %s", message)
	}
}

// TestDocsBudgetTokenChannelNamesTheMintingCondition: the condition under which
// a launch is sealed is the code's, held to it rather than typed twice. The
// document's earlier text said "a budget database, a published policy and a
// signer", which is short of the code in a way that misleads an operator: a
// configuration with the budget mode on and **no** published policy mints too
// (the review of this branch found it).
//
// The pin is two-sided, because either side can drift alone: the document has
// to state both disjuncts, and internal/cli/launch_budget.go has to still have
// them — a code change that drops the mode branch, or makes an absent policy
// mint, fails here and sends whoever made it back to the paragraph.
func TestDocsBudgetTokenChannelNamesTheMintingCondition(t *testing.T) {
	// The document's half: both disjuncts, in the words the message and this
	// test use, and the signer that both branches need.
	const design = "docs/design/budget-token-channel.md"
	doc := readDoc(t, design)
	i := strings.Index(doc, "**The owner's answer: Batch runs no sealed budget run")
	j := strings.Index(doc, "Every symbol below was read on")
	if i < 0 || j < i {
		t.Fatalf("%s has no owner's-answer paragraph followed by the symbol note: the paragraph that states the minting condition is gone", design)
	}
	sec := strings.Join(strings.Fields(doc[i:j]), " ")
	for _, want := range []string{
		"a budget database with budget mode on, or a published policy",
		"a signer is required in both cases",
		"budgetMintOn",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s's answer paragraph does not state %q: an operator has to be able to tell whether their installation mints without reading the code", design, want)
		}
	}
	// The §1.6 half, which is the design that owns the backend.
	cb := readDoc(t, "docs/design/compute-backend.md")
	k := strings.Index(cb, "### 1.6 Secrets")
	l := strings.Index(cb, "### 1.7 Networking")
	if k < 0 || l < k {
		t.Fatalf("docs/design/compute-backend.md has no §1.6 followed by §1.7: the section that states the condition is gone")
	}
	if sec16 := strings.Join(strings.Fields(cb[k:l]), " "); !strings.Contains(sec16, "a budget database with budget mode on, or a published policy") {
		t.Errorf("docs/design/compute-backend.md §1.6 does not state the full minting condition (\"a budget database with budget mode on, or a published policy\"): a project whose budget mode is on with no published policy still gets every launch refused, and §1.6 is where that project's operator reads why")
	}

	// The code's half: the two disjuncts, read out of the functions that
	// decide them. A rewrite that removes either one changes what "minting"
	// means, and this assertion is what makes that a red test rather than a
	// paragraph that quietly stops being true.
	src, err := os.ReadFile("../../internal/cli/launch_budget.go")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Join(strings.Fields(string(src)), " ")
	// budgetBackendOn: the mode branch, and the database it needs.
	bs := strings.Index(code, "func budgetBackendOn(lc *localcfg.Config) (bool, error) {")
	if bs < 0 {
		t.Fatalf("internal/cli/launch_budget.go no longer has budgetBackendOn, which %s's minting condition is written from", design)
	}
	be := strings.Index(code[bs:], "func budgetSignerOn(")
	if be < 0 {
		t.Fatalf("internal/cli/launch_budget.go no longer has budgetSignerOn after budgetBackendOn: this test cannot find the end of the condition it reads")
	}
	backendOn := code[bs : bs+be]
	// budgetMintOn: the policy branch.
	ms := strings.Index(code, "func (e *cloudEnv) budgetMintOn(ctx context.Context) (bool, error) {")
	if ms < 0 {
		t.Fatalf("internal/cli/launch_budget.go no longer has budgetMintOn, which %s's minting condition names", design)
	}
	me := strings.Index(code[ms:], "// projectPolicyRead is the CLI's read")
	if me < 0 {
		t.Fatalf("internal/cli/launch_budget.go no longer has the comment that ends budgetMintOn: this test cannot find the end of the function it reads")
	}
	mintOn := code[ms : ms+me]
	for _, want := range []string{
		// "budget mode on": the mode is what the first branch turns on.
		"lc.BudgetMode() == localcfg.BudgetOff",
		// "a budget database": both branches need the address.
		"lc.Budget.RTDBURL == \"\"",
		// "a published policy": the second branch's own read.
		"p, found, err := e.policy.read(ctx, e.lc)",
		// still mode-or-policy and not mode-and-policy.
		"if on, err := budgetBackendOn(e.lc); err != nil || on {",
	} {
		if !strings.Contains(backendOn, want) && !strings.Contains(mintOn, want) {
			t.Errorf("internal/cli/launch_budget.go no longer has %q, which %s's minting condition (\"a budget database with budget mode on, or a published policy\") is written from: the code and the document have to change together", want, design)
		}
	}
}

// TestDocsBudgetTokenChannelCitationsExist: the design's load-bearing citations
// are paths a later implementer has to find — the module that grants the prefix,
// the package the seal will live in, the runner that parses the marker, the
// register the check number is read from. A substring assertion cannot see one of
// them move, so each is stat'ed here as well as required in the text.
//
// Every path below was read on `next` on 2026-10-15 and is in this branch's tree.
// The two documents the check-number paragraph cites from `main` —
// docs/design/artifact-runs.md and docs/plans/2026-10-15-desktop-console.md — are
// deliberately not stat'ed: they are `main`'s files (#393 and #401 merged there),
// this branch is `next`'s, and the paragraph says so in its own words.
func TestDocsBudgetTokenChannelCitationsExist(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The grants the move changes, and the modules that carry them.
		"deploy/terraform/gcp/modules/installation/iam.tf",
		"deploy/terraform/gcp/modules/installation/bucket.tf",
		"deploy/terraform/gcp/modules/workflow/job.tf",
		"deploy/terraform/gcp/modules/workflow/sa.tf",
		// The channel itself: the object, the mint, the claims, the exchange.
		"internal/budget/token/object.go",
		"internal/budget/token/mint.go",
		"internal/budget/token/token.go",
		"internal/budget/token/exchange.go",
		// The two ends: the runner that takes and parses, the launcher that mints.
		"internal/runner/budget_session.go",
		"internal/runner/projectpolicy.go",
		"internal/runner/runner.go",
		"internal/cli/launch_budget.go",
		"internal/cli/run.go",
		"internal/cli/exec.go",
		// What the design leans on for its ordering and its check.
		"internal/cli/init_fugaroyaml.go",
		"internal/cli/init.go",
		"internal/imagecheck/record.go",
		"internal/infra/tf/cover.go",
		"internal/infra/tfvars.go",
		"internal/infra/hardening.go",
		"internal/backend/gcp/names.go",
		"internal/budget/rules/rules.go",
		// The documents it is read against.
		"SECURITY.md",
		"docs/gcp-live-checklist.md",
		"docs/plans/2026-10-14-project-centric.md",
		"docs/design/compute-backend.md",
		"docs/design/m9-budget-and-dashboard.md",
		"docs/design/bucket-iam.md",
		"docs/design/project-centric.md",
	} {
		// The document links its neighbours relatively (../plans/…,
		// ../../SECURITY.md), so a citation counts in either form.
		rel, err := filepath.Rel("docs/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", path, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", path, cited, err)
		}
	}

	// The number the outline proposes is now written, by the seal's own
	// launcher half: the checklist's Check 60 is this design's, owner-run,
	// sandbox only, and never the belong installation.
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	i := strings.Index(checklist, "## Check 60: the sealed budget token")
	if i < 0 || !strings.Contains(checklist, "| 60 | The sealed budget token") {
		t.Fatalf("docs/gcp-live-checklist.md does not carry Check 60 for %s (its section and its summary row)", path)
	}
	section := checklist[i:]
	for _, want := range []string{"never `belong`", "run by you", "NOT RUN", "**No agent runs any step of this check**", "P=fugaro-dev", "never a token, a key"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 60 does not say %q", want)
		}
	}
	// A step's commands have to be commands: `fugaro status` never existed
	// (internal/cli/root.go adds no such command), and the one run's own record
	// is `fugaro diagnose RUN --json`, whose row carries the execution
	// (internal/cli/diagnose.go's Diagnosis.Row).
	for _, want := range []string{"fugaro diagnose", "fugaro runs ls", ".row.execution"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 60 does not read a run through a command that exists: it does not say %q", want)
		}
	}
	if strings.Contains(section, "fugaro status") {
		t.Errorf("Check 60 reads a run with `fugaro status`, which no command implements: the one-run read is `fugaro diagnose RUN --json` (.row.execution), the list is `fugaro runs ls`")
	}
}

// TestCheck60LearnsTheJobBeforeAnyStepNamesIt: Check 60's steps are run in
// order, and the job every step from 3 on executes — and step 4 filters the
// audit log by — is not known until step 3 reads it off the run's own launch
// record (it is the repository's own job or the shared project job). The setup
// block is re-run at the start of every step, so a `${SANDBOX_JOB:?}` in it
// stops step 1 and step 2 on a variable the owner cannot know yet, and the only
// way past is to guess a job name — which is what the 2026-10-18 review found
// the block did. What holds the order instead: the setup block leaves the
// variable optional and reads `$CHK60/vars.env` back (step 2 writes `SLUG` and
// `RUN2` there, and step 3 the job), and the first `${SANDBOX_JOB:?}` — the
// hard stop at the point of use — comes after step 3 has learnt it.
func TestCheck60LearnsTheJobBeforeAnyStepNamesIt(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "## Check 60: the sealed budget token")
	j := strings.Index(doc, "## Check 69a:")
	if i < 0 || j < i {
		t.Fatalf("%s has no Check 60 section followed by Check 69a: the section this test reads is gone", path)
	}
	sec := doc[i:j]
	setup := section(t, sec, "### 1. Setup and guard", "### 2. ")
	step2 := section(t, sec, "### 2. ", "### 3. ")
	step3 := section(t, sec, "### 3. ", "### 4. ")
	// The blocks carry comments, and a comment that names a guard is not a
	// guard: every check below is on the lines that run.
	setup, step2, step3 = codeLines(setup), codeLines(step2), codeLines(step3)

	// Step 1: the job is not a hard stop here, and the file the later steps
	// keep their variables in is read back rather than assumed empty.
	if strings.Contains(setup, "SANDBOX_JOB=${SANDBOX_JOB:?") {
		t.Errorf("Check 60's setup block hard-stops on SANDBOX_JOB, which step 3 is what learns: re-run at the start of step 1 or 2 it stops on a job name the owner has no way to fill in, so the only way past is to guess one")
	}
	if !strings.Contains(setup, "SANDBOX_JOB=${SANDBOX_JOB:-}") {
		t.Errorf("Check 60's setup block does not leave SANDBOX_JOB optional (`${SANDBOX_JOB:-}`): step 1 and step 2 run before step 3 learns it")
	}
	if !strings.Contains(setup, `"$CHK60/vars.env"`) {
		t.Errorf("Check 60's setup block does not read $CHK60/vars.env back: it is re-run in every step, and a later shell has lost what step 2 and step 3 wrote there")
	}
	// Steps 1 and 2 must not use it: the only reads before step 3 are the
	// read-only lists and step 2's own run.
	for _, bad := range []string{"${SANDBOX_JOB:?", "${SANDBOX_JOB}"} {
		if strings.Contains(setup, bad) || strings.Contains(step2, bad) {
			t.Errorf("Check 60 uses %s before step 3 learns the job", bad)
		}
	}

	// Step 3: learnt off the run's launch record, and kept for every step
	// after it.
	const learn = `SANDBOX_JOB=$(gcloud storage cat`
	const keep = `echo "SANDBOX_JOB=${SANDBOX_JOB:?}" >> "$CHK60/vars.env"`
	if !strings.Contains(step3, learn) {
		t.Errorf("Check 60's step 3 does not assign SANDBOX_JOB from the run's own launch record (`%s`): the job every later step names is the one this step reads", learn)
	}
	if !strings.Contains(step3, keep) {
		t.Errorf("Check 60's step 3 does not keep the job in vars.env (`%s`): every step after it re-runs the setup block in a shell that has never seen it", keep)
	}
	// Both offsets are into the section's own code lines. An offset into step
	// 3's slice is a different coordinate: step 3 starts partway through the
	// section, so an index into the section is always the larger of the two and
	// the comparison could never fail — the 2026-10-19 review's first finding.
	secCode := codeLines(sec)
	if a, b := strings.Index(secCode, learn), strings.Index(secCode, "${SANDBOX_JOB:?"); a < 0 || b < 0 || b < a {
		t.Errorf("Check 60's first `${SANDBOX_JOB:?}` is not after the step that learns the job (the learn at %d, the first hard stop at %d, both offsets into the check's code lines): the guard at the point of use is what has to catch an empty job, not the setup block", a, b)
	}
}

// TestCheck60ReadsFailOnAMissingField: step 3 turns two reads into a job name
// and an execution name, and both were bare `jq -r` reads — which print the
// word `null` when the field is missing and exit 0. `null` is a non-empty
// string, so the `${SANDBOX_JOB:?}` guard after the first one lets it through
// and the step goes on to describe a job called `null` and record a FACT about
// nothing. `jq -e` is what makes the read fail instead — with `-e` jq exits 1
// when the last value is null, or when there is no result at all — but `-e`
// alone is not enough, because jq still prints `null` and `$(...)` captures
// stdout whatever the exit status is: the step has to consume the non-zero exit
// (`|| { echo "Check 60 stopped" …; exit 1; }`) or the guard is handed `null`.
// The read failing is the whole point — not the variable being empty. This is
// the 2026-10-19 independent review's first leftover.
func TestCheck60ReadsFailOnAMissingField(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "## Check 60: the sealed budget token")
	j := strings.Index(doc, "## Check 69a:")
	if i < 0 || j < i {
		t.Fatalf("%s has no Check 60 section followed by Check 69a: the section this test reads is gone", path)
	}
	step3 := codeLines(section(t, doc[i:j], "### 3. ", "### 4. "))
	for _, want := range []string{"jq -er .job", "jq -er .row.execution"} {
		if !strings.Contains(step3, want) {
			t.Errorf("Check 60's step 3 does not read %q: a bare `jq -r` on a missing field prints `null` and exits 0, so a `null` job or a `null` execution name is carried on as if it were read", want)
		}
	}
	if strings.Contains(step3, "jq -r .") {
		t.Errorf("Check 60's step 3 still reads a field with a bare `jq -r`: `null` is what a missing field prints, it exits 0, and `${SANDBOX_JOB:?}` accepts it — use `jq -er`, which exits non-zero and leaves the variable empty")
	}
	// `-e` changes only jq's exit status: it still prints `null` for a missing
	// field, and `$(...)` captures stdout whatever that status is — so the
	// read has to stop the step itself, or the word `null` goes on to be read
	// as a name. That is what makes the guard after it meaningful.
	for _, read := range []string{"jq -er .job", "jq -er .row.execution"} {
		failsClosed := regexp.MustCompile(regexp.QuoteMeta(read) + `\)\s*(\\\n\s*)?\|\| \{ echo "Check 60 stopped`)
		if !failsClosed.MatchString(step3) {
			t.Errorf("Check 60's `%s` does not consume jq's non-zero exit (`|| { echo \"Check 60 stopped\" …; exit 1; }` on the same command): jq prints `null` for a missing field whatever its status, so without this the step goes on with a job or an execution named null", read)
		}
	}
	// The guard the block already had still catches an empty one.
	const guard = `echo "SANDBOX_JOB=${SANDBOX_JOB:?}"`
	if a, b := strings.Index(step3, "jq -er .job"), strings.Index(step3, guard); a < 0 || b < a {
		t.Errorf("Check 60's step 3 does not hand the job read to the ${SANDBOX_JOB:?} guard (the read at %d, the guard at %d): an empty job is what the guard is for", a, b)
	}
}

// TestCheck60SaysHowToStartOver: the setup block reads $CHK60/vars.env back,
// which is what makes the steps runnable from separate shells — and what makes
// a /tmp/chk60 left over from an earlier attempt actively harmful. The block
// would take that attempt's SLUG, RUN2 and SANDBOX_JOB as this attempt's, every
// step would read the earlier run's objects and record them as the new run's,
// and the baseline would be the earlier attempt's policy file. The Method has
// to say how to start clean. This is the 2026-10-19 independent review's second
// leftover.
func TestCheck60SaysHowToStartOver(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "## Check 60: the sealed budget token")
	j := strings.Index(doc, "## Check 69a:")
	if i < 0 || j < i {
		t.Fatalf("%s has no Check 60 section followed by Check 69a: the section this test reads is gone", path)
	}
	method := section(t, doc[i:j], "**Method.**", "### 1. Setup")
	for _, want := range []string{"earlier attempt", "rm -rf /tmp/chk60", "vars.env"} {
		if !strings.Contains(method, want) {
			t.Errorf("Check 60's Method does not say %q: it is where the owner is told the variables live, and a leftover /tmp/chk60 from an earlier attempt makes every step read that attempt's variables", want)
		}
	}
}

// TestCodeLinesKeepsAHashThatIsNotAComment: codeLines feeds every Check 60
// assertion, so what it removes has to be comments and nothing else. It cut
// each fenced line at its first `#`, which read the check's own
// `"${EXE##*/}"` as `"${EXE` — a `#` inside a parameter expansion is the
// length operator, not a comment, and a `#` inside quotes is a string. Cutting
// there hands the assertions a line the shell never runs, which is the shape
// of the 2026-10-19 review's second finding. A comment is a `#` that starts a
// line or follows whitespace, outside quotes and outside `${…}`; the `##` case
// the review named is in the table.
func TestCodeLinesKeepsAHashThatIsNotAComment(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// The review's case: `##` is the length operator inside `${…}`.
		{`gcloud run jobs executions describe "${EXE##*/}" --region="${L:?}"`, `gcloud run jobs executions describe "${EXE##*/}" --region="${L:?}"`},
		// A comment after code, and one on a line of its own.
		{`grep -c 'eyJ' "$f"   # both 0`, `grep -c 'eyJ' "$f"   `},
		{`   # a whole-line comment`, `   `},
		// `#` inside quotes is text, in both quotings.
		{`echo 'a # b'`, `echo 'a # b'`},
		{`echo "a # b" # out here`, `echo "a # b" `},
		// `#` inside an expansion is text even after a space — `${…}` is not
		// parsed for comments — and the `#` after it is one.
		{`X=${Y:?no # comment here} # and this goes`, `X=${Y:?no # comment here} `},
		// `#` that follows no whitespace is part of the word, as in a URL.
		{`curl "https://host/p#frag"`, `curl "https://host/p#frag"`},
		// An escaped `#` is a literal `#`, not a comment.
		{`echo \# not a comment`, `echo \# not a comment`},
	} {
		got := codeLines("```bash\n" + c.in + "\n```")
		if got != c.want {
			t.Errorf("codeLines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// codeLines is what s runs: the lines inside its fenced blocks, with each
// line's `#` comment removed. Prose is not read, so a sentence that names a
// guard is not a guard — the check is on the commands the owner pastes.
func codeLines(s string) string {
	var out []string
	fenced := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			continue
		}
		out = append(out, stripComment(line))
	}
	return strings.Join(out, "\n")
}

// stripComment removes one line's shell comment: the `#` that starts it, or
// that follows whitespace. A `#` inside a quote or inside a `${…}` expansion
// is code — `${EXE##*/}` is the length operator, and `'a # b'` is a string —
// so it is kept, to the end of the line if there is no comment at all.
//
// Nesting is tracked for quotes and for `${…}` only: a `#` inside a `$( … )`
// or a backtick could be a comment (the shell parses their insides), and this
// would keep it, which is the safe direction for tests that assert on what the
// owner pastes. Nothing in Check 60's blocks has one.
func stripComment(line string) string {
	var quote byte // '\'' or '"' while inside one
	depth := 0     // `$ {` nesting, so a `#` in `${…}` is not a comment
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			switch {
			case c == '\\':
				i++ // the escaped byte is text, whatever it is
			case c == '"':
				quote = 0
			case c == '$' && i+1 < len(line) && line[i+1] == '{':
				depth++
				i++
			case c == '}' && depth > 0:
				depth--
			}
		case c == '\\':
			i++ // ditto, outside quotes
		case c == '\'' || c == '"':
			quote = c
		case c == '$' && i+1 < len(line) && line[i+1] == '{':
			depth++
			i++
		case c == '}' && depth > 0:
			depth--
		case c == '#' && depth == 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// TestCheck60LabelsWhatIsPerJobAndWhatIsNot: the check has three scopes and the
// text has to say which step is which, because the owner re-runs it and reads
// the FACTs against a live project: step 2 is per-run, steps 3 to 7 and 9 name
// one job (`SANDBOX_JOB`) and are about that job, and step 8 reads the bucket's
// whole policy — the launchers' binding and every job's `tokens/` grant, which
// is installation-wide. The 2026-10-18 review found only step 8 saying so.
//
// It holds the commands' own rule too (Check 43's "Every `fugaro` command that
// takes `--project` gets `--project fugaro`"): the two `fugaro` reads this check
// makes are the ones that would otherwise resolve their installation from
// whatever checkout the owner is standing in.
func TestCheck60LabelsWhatIsPerJobAndWhatIsNot(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "## Check 60: the sealed budget token")
	j := strings.Index(doc, "## Check 69a:")
	if i < 0 || j < i {
		t.Fatalf("%s has no Check 60 section followed by Check 69a: the section this test reads is gone", path)
	}
	sec := doc[i:j]

	// Every step that runs something against one job names it, guarded.
	for _, h := range []struct{ from, to string }{
		{"### 3. ", "### 4. "}, {"### 4. ", "### 5. "}, {"### 5. ", "### 6. "},
		{"### 6. ", "### 7. "}, {"### 7. ", "### 8. "}, {"### 9. ", "### Restore"},
	} {
		step := section(t, sec, h.from, h.to)
		if !strings.Contains(codeLines(step), "${SANDBOX_JOB:?") {
			t.Errorf("Check 60's %s does not name ${SANDBOX_JOB:?}: every step from 3 on runs against the one job step 3 learnt, and a step that named the repository's own job instead would record a false FACT on a project whose workflow launches on the shared one", strings.TrimSpace(h.from))
		}
	}

	// Step 8 is the installation-wide one, and it says so, with the two grant
	// titles the live policy must show — spelled by the code that writes them.
	step8 := section(t, sec, "### 8. ", "### 9. ")
	if !strings.Contains(step8, "installation-wide") {
		t.Errorf("Check 60's step 8 does not say it is the installation-wide step: it reads the whole bucket policy, while every other step from 3 on is about one job")
	}
	tokenTitle := strings.TrimSuffix(gcp.TokenBucketConditionTitle("acct"), "acct")
	for _, want := range []string{tokenTitle, gcp.ProjectTokenBucketConditionTitlePrefix} {
		if !strings.Contains(step8, want) {
			t.Errorf("Check 60's step 8 does not name the grant title %q: its FACT is what the owner compares the live policy against, so both shapes have to be spelled", want)
		}
	}

	// The two fugaro reads carry --project, or they resolve their installation
	// from the checkout the owner happens to be in.
	step2 := section(t, sec, "### 2. ", "### 3. ")
	step3 := section(t, sec, "### 3. ", "### 4. ")
	for _, c := range []struct{ step, what string }{
		{step2, "fugaro run"}, {step3, "fugaro diagnose"},
	} {
		for _, line := range strings.Split(codeLines(c.step), "\n") {
			if !strings.Contains(line, c.what) {
				continue
			}
			if !strings.Contains(line, "--project fugaro") {
				t.Errorf("Check 60's `%s` does not carry --project fugaro: %s", c.what, strings.TrimSpace(line))
			}
		}
	}
}

// section is the part of doc between the from and to markers, failing the test
// when either is missing or out of order.
func section(t *testing.T, doc, from, to string) string {
	t.Helper()
	i := strings.Index(doc, from)
	j := strings.Index(doc, to)
	if i < 0 || j < i {
		t.Fatalf("the document has no %q followed by %q: this test reads the text between them", from, to)
	}
	return doc[i:j]
}

// TestDocsBudgetTokenChannelSection72IsTheMergedTree: Check 60 sends the owner
// to §7.2 for the moved object's grant, its lifecycle rule and the shape of its
// take, so that section has to describe the tree the owner is standing in
// rather than the two-step plan D19 priced — the 2026-10-18 review found it
// three versions behind (title `fugaro-launchers-runs` and a runs/-only
// expression, `matches_prefix = ["runs/"]`, and a take with "two shapes and one
// switch" shipping a runs/-keyed fatal delete). Each fact is asserted against
// the merged code's own name for it, so the section cannot drift from
// `gcp.LauncherBucketConditionTitle` or from `token.TakeObject`'s contract
// without this failing.
func TestDocsBudgetTokenChannelSection72IsTheMergedTree(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "### 7.2 ")
	j := strings.Index(doc, "### 7.3 ")
	if i < 0 || j < i {
		t.Fatalf("%s has no §7.2 followed by §7.3: the section the checklist points at is gone", path)
	}
	sec := doc[i:j]
	for _, want := range []string{
		// The launchers' widened condition and its new title, in Terraform
		// and in gcp.LauncherBucketConditionTitle (iam.tf's precondition
		// pins both).
		gcp.LauncherBucketConditionTitle,
		// The one-day rule that moved with the object.
		`matches_prefix = ["runs/", "tokens/"]`,
		// The one take shape, the read-only one.
		"never calls delete",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s §7.2 does not say %q: the checklist sends the owner here for the moved token's grant, its rule and its take", path, want)
		}
	}
	if strings.Contains(sec, "two shapes and one switch") {
		t.Errorf("%s §7.2 still describes the take as \"two shapes and one switch\": the merged tree has one shape, the read-only one that never deletes (token.TakeObject)", path)
	}
}

// TestDocsBudgetTokenChannelC3PricesThePreSealGrant: the rejected alternative C3
// quotes the launchers' condition as it stood when the alternative was priced —
// `runs/` alone. §7.2 is now the merged tree's account of that grant (widened to
// `runs/ || tokens/`, under a new title), so citing §7.2 for the runs/-only shape
// contradicts the section this branch corrected; the 2026-10-18 review found the
// C3 bullet still doing exactly that. The two assertions hold the corrected
// sentence: the pre-seal shape is named as pre-seal, and the pointer to §7.2
// names the widening rather than the old expression.
func TestDocsBudgetTokenChannelC3PricesThePreSealGrant(t *testing.T) {
	const path = "docs/design/budget-token-channel.md"
	doc := readDoc(t, path)
	i := strings.Index(doc, "### C3. ")
	if i < 0 {
		t.Fatalf("%s has no C3 alternative: the section this branch corrected its cross-reference in is gone", path)
	}
	sec := doc[i:]
	if j := strings.Index(sec, "### C4. "); j > 0 {
		sec = sec[:j]
	}
	// The document is wrapped, so a phrase can straddle a line break: the
	// assertions below are on prose, not on layout.
	flowed := strings.Join(strings.Fields(sec), " ")
	for _, want := range []string{
		"conditioned to `runs/` alone as this alternative was priced",
		"widened it to `runs/ || tokens/`",
	} {
		if !strings.Contains(flowed, want) {
			t.Errorf("%s's C3 does not say %q: it prices the launchers' grant, which the merge has since widened, so the shape it prices has to be marked as the one it was priced at", path, want)
		}
	}
	if strings.Contains(sec, "`objectUser` on `runs/` only, §7.2") {
		t.Errorf("%s's C3 cites §7.2 for a runs/-only launcher condition, which is not what §7.2 says any more: the section is the merged tree's widened member", path)
	}
}

// TestDocsSecurityMdPointsAtTheTokenChannelDesign: the trust model's reader can
// find the design that sealed the token's delivery channel, from the D13
// paragraph that the seal narrows, and the paragraph says what the seal is —
// built, both halves, and a capability rather than a boundary — rather than
// overstating it. The pointer is asserted on the D13 paragraph's own line, so
// wording elsewhere in the file cannot stand in for it.
func TestDocsSecurityMdPointsAtTheTokenChannelDesign(t *testing.T) {
	const design = "docs/design/budget-token-channel.md"
	sec := readDoc(t, "SECURITY.md")
	var pointer string
	for _, line := range strings.Split(sec, "\n") {
		if strings.Contains(line, "D13") && strings.Contains(line, design) {
			pointer = line
			break
		}
	}
	if pointer == "" {
		t.Fatalf("SECURITY.md's D13 paragraph does not point at %s", design)
	}
	for _, want := range []string{"both halves built", "capability, not a boundary", "read-only", "sealed"} {
		if !strings.Contains(pointer, want) {
			t.Errorf("SECURITY.md's D13 paragraph does not say %q about the seal\n\nline: %s", want, pointer)
		}
	}
	// The marker's sentence and the launcher paragraph are rewritten
	// together (D15): the token is a bearer credential that does not travel
	// as an override, the key is not a credential and its audience is named.
	for _, want := range []string{"**bearer credential**", "**seal key**", "is not a credential", "Its audience is the override's", "not** a peer run"} {
		if !strings.Contains(sec, want) {
			t.Errorf("SECURITY.md does not carry %q: the marker's sentence and the launcher paragraph must say what the key is (D15)", want)
		}
	}
	if _, err := os.Stat("../../" + design); err != nil {
		t.Fatalf("SECURITY.md points at %s, which is not there: %v", design, err)
	}
}
