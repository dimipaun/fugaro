package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestChecklistHasCheck68 pins Check 68 of docs/gcp-live-checklist.md: the live
// check of the managed deployer's redesigned fence (docs/design/
// managed-deployer-fence.md; deploy/terraform/gcp/modules/installation/
// deployer.tf). The section is the only place the shape's open questions are
// measured, so what this test holds is what makes it runnable by the owner and
// safe to run: whose check it is, the five hard stops in the guard block (the
// belong one comparing PROJECT IDS and the installation's name, never a
// `fugaro doctor` line read out of the checkout), the workdir and backend
// configuration every terraform command below it needs (the checkout's
// `deploy/terraform/gcp/roots/installation` has no tfvars and no backend), the
// two directions every service is measured in, the Secret Manager version read
// and the section's own statement that the design's C7 — whether a name that
// misses an `extract` template fails closed — is UNVERIFIED rather than
// measured here, and cannot be measured here: no live name of a
// conditioned type can be made to miss its own template, and the grammar is the
// repository's reading of a reference no source in the tree quotes, so the
// register must not read as either measuring it or inheriting an answer for it),
// the restore that closes the gate again, and the
// fact that Check 67 — the transitive-reach half — is not this section.
//
// The row and the heading carry the same facts as prose: a live check that does
// not say it is the owner's, on a sandbox project, and never against `belong`
// is one an agent could read as a step to run.
func TestChecklistHasCheck68(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	section := docSection(t, path, doc, "## Check 68:")

	// 1. Whose check it is. The row is the checklist's own index of it.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 68 |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 68 |` table row")
	}
	for _, want := range []string{
		"USER-RUN, NOT RUN",
		"never `belong`",
		"sandbox project only",
		"Check 67",
		"managed_deployer_scoped = true",
		// The operator count the check expects, counted as the repository
		// counts it (`&&` and `||` alike: the 0.8.6 expression is recorded as
		// 17). A row that says "one operator at the most" states a number no
		// test produces and no IAM refusal is about.
		"three logical operators at the most",
		// The throwaway resources, which are TEN and not the plan's fourteen
		// addresses: step 4 creates seven (one per service) and step 5 three
		// near misses (the bucket, the secret and the job), and the Restore
		// deletes those ten. The fourteen are the plan's addresses — seven
		// roles and seven bindings — and a row that conflates the two counts
		// the resources an owner has to find and delete wrongly.
		"ten throwaway resources",
		"the seven per-service probes of step 4 and the three near misses of step 5",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 68 |` row does not carry %q", want)
		}
	}
	for _, want := range []string{
		"NOT RUN",
		"run by you",
		"never `belong`",
		"No agent runs any step of it",
		"agentMarker",
		"Check 67",
		// The three questions the section settles, by their design labels: a
		// check that stops naming them stops being the measurement of the
		// design's UNVERIFIED rows.
		"F1",
		"C1",
		"C3",
		"UNVERIFIED",
		// And the one it names so the reader is not misled: C7 is named, and
		// the section says outright that this check cannot settle it — no live
		// step can construct the mismatch, and the design carries the reading as
		// UNVERIFIED rather than as a citation (see the section's step 6).
		// Without this the register would read as measuring, or as answering, a
		// question the check cannot construct a probe for.
		"C7",
		"no step of this check can answer it",
		"TestExtractSegmentModelsTheRecordedReading",
		"empty string",
		"extractSegment",
		// C1's expected number, in the repository's own counting: the C1 step
		// has to expect what TestDeployerOperatorCountsAreTheDocumentedNumbers
		// counts (three for Cloud Storage, one for each of the other six), or
		// the check measures an expression the tree does not write.
		"at most **three** logical operators",
		"TestDeployerOperatorCountsAreTheDocumentedNumbers",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not say %q", want)
		}
	}
	// The count as it was first written, which is what the redesign's own tests
	// contradict: a condition's clause joins its type term with an `&&`, and
	// counting that as "no operator" makes the number the check expects one IAM
	// would never refuse on.
	for _, wrong := range []string{
		"one logical operator at the most",
		"one logical operator at most",
	} {
		if strings.Contains(section, wrong) {
			t.Errorf("Check 68 states the operator count as %q, which is not how this repository counts operators (`&&` and `||` alike)", wrong)
		}
	}

	// 2. The guard block: the first block, with the five hard stops, the project
	// set by the owner rather than read from gcloud, and the belong stop
	// comparing project ids and the installation's name — a `fugaro doctor` line
	// says which installation the checkout is, which is no evidence at all about
	// the project the check writes in.
	blocks := bashBlocks(section)
	if len(blocks) < 6 {
		t.Fatalf("Check 68 has %d bash blocks, want the guard, the read-only setup and the steps", len(blocks))
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{
		"CHK68_PROJECT:?",
		"CHK68_INSTALLATION:?",
		// The belong stop, as the round-4 re-review required it: belong's OLD
		// project id (the one the fifth live run read out of its project.json,
		// kept as a refusal because that project is still live), the project it
		// moved to on 2026-10-06 (`fugaro-belong`), and any id that merely
		// contains `belong` — which is what a third move would be called. The
		// installation's name and the project its config names are refused the
		// same way, and TestChecklistCheck68GuardRefusesBelong runs the block to
		// prove each one refuses.
		"fugaro-belong",
		"edge-devel-dimi",
		"*belong*",
		"belongs()",
		`[ "$I" != belong ]`,
		`sed -n 's/^gcp_project: *//p' "$C"`,
		`$HOME/.config/fugaro/projects/${I:?}.yaml`,
		"exit 1",
		"--project=\"${P:?}\"",
		"mkdir -p \"$CHK68_TMP\"; chmod 700 \"$CHK68_TMP\"",
		// The state bucket, read from the installation's config instead of
		// assumed: `fugaro-tfstate-<gcp-project>` is only the default, and this
		// check has to init the backend the sandbox's own state lives under.
		"STATE_BUCKET",
		// The read itself, not just the variable: the bucket is the config's
		// when the config names one, and a guard that assigned the default
		// instead would plan against a bucket the sandbox's state may not be in.
		`/^terraform:/,/^[^[:space:]#]/p`,
		`s/^[[:space:]]*state_bucket:[[:space:]]*//p`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 68's guard block does not carry %q", want)
		}
	}
	// Every hard stop refuses: the message and its `exit 1` are one line, and a
	// stop whose exit is turned into a no-op prints the same text and carries
	// on. That mutation (D4 in the round-4 report) survived this test while it
	// only read the text, so the pin is structural here and executed in
	// TestChecklistCheck68GuardRefusesBelong below.
	stops := 0
	for _, line := range strings.Split(guard, "\n") {
		if !strings.Contains(line, "Check 68 stopped:") {
			continue
		}
		stops++
		if !strings.Contains(line, "exit 1") {
			t.Errorf("Check 68's stop does not refuse: %q", line)
		}
	}
	if stops < 5 {
		t.Errorf("Check 68's guard carries %d hard stops, and the check documents five", stops)
	}
	if strings.Contains(guard, "gcloud config get-value project") {
		t.Error("Check 68's guard reads the project from gcloud's ambient configuration, which is the trap the check names")
	}
	// The commands of the block, without its comments: what the guard runs is
	// what matters, and the comment above the belong stop names the thing it no
	// longer reads.
	guardCmds := ""
	for _, line := range blocks[0] {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		guardCmds += s + "\n"
	}
	if strings.Contains(guardCmds, "fugaro doctor") {
		t.Error("Check 68's guard reads a doctor line out of the checkout: that says which installation the checkout is, and proves nothing about $P")
	}
	// The guard's citation for BELONG, which is what lets the operator check the
	// hard stop rather than take it on trust: it has to name a section the
	// checklist actually carries, and that section is the one holding the
	// project.json read. "Check 27 above" is not it — Check 27's section
	// contains no project.json line.
	const belongEvidence = "Results of the fifth live run (M9a)"
	// The guard's prose with its comment markers and line wrapping removed: a
	// citation that wraps across two comment lines still names the section.
	guardProse := strings.Join(strings.Fields(strings.ReplaceAll(guard, "#", " ")), " ")
	if !strings.Contains(guardProse, belongEvidence) {
		t.Errorf("Check 68's belong stop does not cite %q, where belong's project.json read is recorded", belongEvidence)
	}
	if strings.Contains(guardProse, "Check 27") {
		t.Error("Check 68's belong stop cites Check 27 for belong's project id, and Check 27's section carries no project.json read")
	}
	// The evidence itself, so the citation is to something: the M9a results are
	// where `{"gcp_project":"edge-devel-dimi","name":"belong",...}` is recorded.
	if strings.Contains(doc, "## "+belongEvidence) {
		if m9a := docSection(t, path, doc, "## "+belongEvidence); !strings.Contains(m9a, `"gcp_project":"edge-devel-dimi"`) {
			t.Errorf("the section Check 68 cites for belong's project id (%q) does not carry the project.json read", belongEvidence)
		}
	} else {
		t.Errorf("Check 68 cites %q, and the checklist has no such section", belongEvidence)
	}
	// The other half of the citation, and it is what makes the new refusal
	// evidence rather than a guess: belong moved on 2026-10-06, and the design
	// that records the move names the project it moved to. A guard citing a
	// document that does not say that is a guard citing nothing.
	const moveEvidence = "docs/design/adopt-firebase-root.md"
	if !strings.Contains(guardProse, moveEvidence) {
		t.Errorf("Check 68's belong stop does not cite %q, where the 2026-10-06 move to fugaro-belong is recorded", moveEvidence)
	}
	if b, err := os.ReadFile("../../" + moveEvidence); err != nil {
		t.Errorf("Check 68 cites %q, which does not read: %v", moveEvidence, err)
	} else if moved := string(b); !strings.Contains(moved, "fugaro-belong") || !strings.Contains(moved, "2026-10-06") {
		t.Errorf("Check 68 cites %q for belong's move to fugaro-belong, and that document records no such move", moveEvidence)
	}
	// The workdir and its backend, which is where the gate variable lives and
	// where the plan is made. The checkout's root has neither a tfvars file nor a
	// backend block, so a check that plans there cannot run at all.
	for _, want := range []string{
		`XDG_STATE_HOME:-$HOME/.local/state`,
		`/fugaro/terraform/${I:?}/installation`,
		`TF_DATA_DIR="$W/.terraform" TF_CLI_CONFIG_FILE="$W/terraformrc"`,
		`-backend-config=bucket="${STATE_BUCKET:?}"`,
		"-backend-config=prefix=fugaro/installation",
		`$W/gcp/roots/installation/terraform.tfvars.json`,
		"managed_deployer_scoped: true",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q: the gate is set in the workdir's tfvars and the root is planned from the workdir", want)
		}
	}
	if strings.Contains(section, "fugaro config path") {
		t.Error("Check 68 uses `fugaro config path`, which does not exist: the local config is $HOME/.config/fugaro/projects/<name>.yaml, as the other checks name it")
	}
	// Every gcloud command in the section names its project, in every block.
	commands := 0
	for i, block := range blocks {
		for _, line := range block {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "echo ") {
				continue
			}
			if !strings.HasPrefix(s, "gcloud ") {
				continue
			}
			commands++
			// The project is named either as the flag or as the argument
			// (`gcloud projects describe "${P:?}"`), and never left to
			// gcloud's ambient configuration.
			if !strings.Contains(s, "--project=") && !strings.Contains(s, `"${P:?}"`) {
				t.Errorf("block %d: %q is a gcloud command that names no project", i, s)
			}
		}
	}
	if commands < 20 {
		t.Errorf("Check 68 has %d gcloud commands, want the positive direction, the negative direction, the near misses and the restore", commands)
	}

	// 3. The two directions, per service, as the deployer: the impersonation
	// flag is what makes a denial a fact about the fence rather than about the
	// operator, and each of the seven services is acted on both ways.
	for _, want := range []string{
		`--impersonate-service-account="${DEPLOYER:?}"`,
		"fmgd-chk68-bucket", "fmgd-chk68-secret", "fmgd-chk68-sa", "fmgd-chk68-repo",
		"fmgd-chk68-key", "fmgd-chk68-job", "fmgd-chk68-sched",
		"xfmgd-chk68-bucket", "xfmgd-chk68-secret", "xfmgd-chk68-job",
		"${RUNS_BUCKET:?}",
		"${SANDBOX_SECRET:?", "${SANDBOX_SA:?", "${SANDBOX_REPO:?",
		"${SANDBOX_KEY:?", "${SANDBOX_JOB:?", "${SANDBOX_SCHED:?",
		"permission",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}
	if n := strings.Count(section, "--impersonate-service-account=\"${DEPLOYER:?}\""); n < 14 {
		t.Errorf("Check 68 impersonates the deployer %d times, want the seven positive actions, the seven negative ones and the near misses", n)
	}

	// 3b. How the two probes that could never pass are built. The API key's ID
	// is chosen by the check, because a server-generated uid never starts with
	// fmgd- and the key's positive action would be denied for a reason the
	// fence is not responsible for; and the module's side of the policy
	// comparison is unescaped (the HCL file spells its quotes `\"`) and has its
	// plan-time project template filled in, or the diff reports differences
	// that are artefacts of the file's own syntax.
	for _, want := range []string{
		"--key-id=fmgd-chk68-key",
		`gsub(" \\|\\| "; "\n")`,
		`s/\\"/"/g`,
		`\${var.project}`,
		"policy-clauses.txt",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q: a probe built any other way cannot produce a pass", want)
		}
	}
	if strings.Contains(section, "--display-name=\"fmgd-chk68-key\"") &&
		!strings.Contains(section, "--key-id=fmgd-chk68-key") {
		t.Error("Check 68 creates the API key without --key-id, so its ID is server-generated and its positive action can never succeed")
	}
	// 3c. The Cloud Run job's runtime account, which is the third way a probe
	// can be unsatisfiable whatever the fence does: updating a Cloud Run job
	// needs `iam.serviceAccounts.actAs` on the job's RUNTIME account, so a job
	// created without --service-account runs as the project's default compute
	// account and the deployer's update is denied for a reason that is not this
	// service's condition. Both the positive job and the near miss are created
	// as the throwaway account this step creates (fmgd-chk68-sa), which the
	// account clause's condition admits — so the actAs the update needs is the
	// fence's own grant, and what the update measures is the run job condition.
	const runJobAccount = `--service-account="fmgd-chk68-sa@${P:?}.iam.gserviceaccount.com"`
	if n := strings.Count(section, runJobAccount); n < 2 {
		t.Errorf("Check 68 creates %d Cloud Run jobs as the throwaway account, want the positive job and the near miss: a job left on the default compute account makes its probe's denial ambiguous", n)
	}
	// And the denial is read, not assumed: a denial naming actAs is recorded as
	// not being this service's answer rather than as the condition being too
	// narrow.
	for _, want := range []string{"iam.serviceAccounts.actAs", "not this service's answer"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q: a Cloud Run denial has two possible causes and the FACT has to name which one it saw", want)
		}
	}

	// 4. The fail-closed question: named, disowned, and answered nowhere. The
	// section has to say in as many words that this check does not settle C7 and
	// why — a SecretVersion is not the secret clause's type, so the read is
	// denied for want of the permission and the condition is never evaluated —
	// and that the design holds the reading UNVERIFIED rather than citing a
	// reference, because the register must neither read as measuring a question
	// nothing live can nor inherit a settled answer from the design.
	for _, want := range []string{
		"secretmanager.versions.*",
		"What this step does not settle",
		"C7 is not this check's question",
		"cannot be settled here either",
		"UNVERIFIED",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}

	// 5. The version read and the restore. The step records a denial that is a
	// property of the roles' permission lists, the restore has to close the
	// gate — a check that leaves it open leaves a fence in place that nothing
	// reads.
	for _, want := range []string{
		"gcloud secrets versions access",
		"fail closed",
		"managed_deployer_scoped",
		"terraform apply -input=false -auto-approve",
		// The Restore deletes everything the check created, the near miss
		// included: a probe left behind is a resource the next run's plan and
		// the next reader both have to account for.
		"gcloud run jobs delete fmgd-chk68-job xfmgd-chk68-job",
		"rm -rf \"$CHK68_TMP\"",
		"**Passes when:**",
		"**Fails when:**",
		"**Paste back:**",
		// The three near misses, not two: step 5 creates one for the bucket,
		// one for the secret and one for the job, its own FACT counts three and
		// the Restore deletes three. A Paste-back line that asks for two asks
		// for a result the check does not produce.
		"the seven negative results and all three near misses with their verbatim denials",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}
	// The restore's own assertion: the gate closed leaves ONE binding on the
	// deployer account (the unconditioned one), and the section says so.
	if !strings.Contains(section, "**one**") {
		t.Error("Check 68's restore does not state the binding count it must leave behind")
	}
}

// TestManagedDeployerDocsCountTemplatePlaceholdersRight holds the design's own
// arithmetic about the first cut's templates to the templates it writes out.
//
// The C6 row and the §6 paragraph argue that the multi-placeholder template is
// not the documented form, and they do it by counting: the shape of the
// argument is "this template carries N placeholders, and the documented form
// carries one". The count has been wrong — the secret template
// `projects/{project}/secrets/{secret}` carries two, and the three-placeholder
// one is the locations form the other four services used — which does not
// change the argument but does make a reader check the sentence instead of
// trusting it.
//
// The rule here is the one the prose already follows: wherever these docs put a
// count next to a template, that count is the template's own number of
// placeholders. A sentence that counts a template the docs do not write out is
// outside this test, and so is a count expressed any other way ("carries
// three").
func TestManagedDeployerDocsCountTemplatePlaceholdersRight(t *testing.T) {
	numbers := map[string]int{"one": 1, "two": 2, "three": 3, "four": 4}
	countRE := regexp.MustCompile(`\b(one|two|three|four)[- ]placeholders?\b`)
	checked := 0
	for _, path := range []string{
		"../../docs/design/managed-deployer-fence.md",
		"../../docs/design/managed-projects.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		for _, template := range []string{
			"projects/{project}/secrets/{secret}",
			"projects/{project}/locations/{location}/keys/{key}",
		} {
			want := strings.Count(template, "{")
			for i := 0; ; {
				j := strings.Index(doc[i:], template)
				if j < 0 {
					break
				}
				i += j + len(template)
				// The count phrase that follows the template, near enough to
				// be about it: the sentences these docs argue in are one line
				// long.
				window := doc[i:]
				if len(window) > 160 {
					window = window[:160]
				}
				m := countRE.FindStringSubmatch(window)
				if m == nil {
					continue
				}
				checked++
				if got := numbers[m[1]]; got != want {
					t.Errorf("%s: the sentence after %s says %q, and the template carries %d placeholders", path, template, m[0], want)
				}
			}
		}
	}
	if checked < 3 {
		t.Errorf("only %d template counts were read, want the C6 row's two and the §6 paragraph's and the managed-projects paragraph's: a doc that stops counting its templates stops being checked here", checked)
	}
}

// TestChecklistCheck68GuardRefusesBelong runs Check 68's guard block rather
// than reading it.
//
// The guard is the only thing between a live IAM check and the `belong`
// installation, and the round-4 re-review killed two mutants of it that the
// pins above could not see. One was the ID: the stop compared $P with belong's
// OLD project (`edge-devel-dimi`, what the fifth live run read out of its
// project.json) while belong moved to `fugaro-belong` on 2026-10-06
// (docs/design/adopt-firebase-root.md:7), so CHK68_PROJECT=fugaro-belong passed
// the guard and every write the check makes would have landed in belong's live
// project. The other was the refusal itself: turning the stop's `exit 1` into
// `true` printed the same message and carried on, and no test noticed, because
// every pin read the block's text.
//
// So this test executes it. The block is written to a file and run under bash
// with the environment the check documents — bash is what the check is written
// in, and the guard is meant to be pasted into a shell — once per id the guard
// has to refuse, once for the installation's name, once for the project the
// installation's config names, and once for a sandbox that must not be refused
// on belong's account.
//
// What proves a refusal here is WHICH stop fired: nothing past the belong stop
// can run in this test (the workdir does not exist, and gcloud is not
// installed), so a refused case must print exactly one `Check 68 stopped:` line
// and it must be the belong one, and the safe case must stop at the next stop
// with no belong line at all. A guard whose belong stop is turned into a no-op
// prints the belong line and then the next one, and fails on the count.
func TestChecklistCheck68GuardRefusesBelong(t *testing.T) {
	// readDoc resolves the path against the repository root, so this is the
	// repository-relative one.
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	blocks := bashBlocks(section)
	if len(blocks) == 0 {
		t.Fatal("Check 68 has no bash blocks")
	}
	guard := strings.Join(blocks[0], "\n")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}

	cases := []struct {
		name          string
		project       string // CHK68_PROJECT
		installation  string // CHK68_INSTALLATION
		configProject string // the `gcp_project:` line of the installation's config; "" writes no config
		refused       bool   // the belong stop is the one that must fire
	}{
		// belong's project as of the move, and its project before it: both are
		// live projects this check may never write in, and the second is the
		// one the fifth live run recorded.
		{"belong's current project", "fugaro-belong", "sandbox", "", true},
		{"belong's project before the 2026-10-06 move", "edge-devel-dimi", "sandbox", "", true},
		// A third move, or a sandbox someone named after belong: the substring
		// rule is what catches a project ID this check has never heard of.
		{"a project id containing belong", "belong-sandbox-2026", "sandbox", "", true},
		{"belong's old project with belong's name in it", "fugaro-belong-run", "sandbox", "", true},
		// The installation's name, and the project its config names: the check
		// edits the workdir tfvars of $I and the config is what names $I's
		// project, so both are writes in belong's territory.
		{"the installation is belong", "fugaro-sandbox-1", "belong", "", true},
		{"the config names belong's project", "fugaro-sandbox-1", "sandbox", "fugaro-belong", true},
		{"the config names belong's old project", "fugaro-sandbox-1", "sandbox", "edge-devel-dimi", true},
		{"the config names a project containing belong", "fugaro-sandbox-1", "sandbox", "belong-sandbox-2026", true},
		// The control: a sandbox that is not belong must get past the belong
		// stop. It stops at the next hard stop, because this test has no workdir
		// and no gcloud — and that is the point: the belong stop is not what
		// stopped it.
		{"a sandbox installation", "fugaro-sandbox-1", "sandbox", "fugaro-sandbox-1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.configProject != "" {
				dir := filepath.Join(home, ".config", "fugaro", "projects")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				body := "name: " + tc.installation + "\ngcp_project: " + tc.configProject + "\nregion: us-east5\nruns_bucket: sandbox-runs\n"
				if err := os.WriteFile(filepath.Join(dir, tc.installation+".yaml"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			script := filepath.Join(t.TempDir(), "chk68-guard.sh")
			if err := os.WriteFile(script, []byte(guard+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, script)
			cmd.Env = []string{
				"HOME=" + home,
				"PATH=" + os.Getenv("PATH"),
				"CHK68_PROJECT=" + tc.project,
				"CHK68_INSTALLATION=" + tc.installation,
				"L=us-east5",
				"RUNS_BUCKET=sandbox-runs",
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			cmd.Stdout = io.Discard
			runErr := cmd.Run()
			code := 0
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if runErr != nil {
				t.Fatalf("running the guard: %v", runErr)
			}
			out := stderr.String()
			stops := strings.Count(out, "Check 68 stopped:")
			if tc.refused {
				if code == 0 {
					t.Errorf("the guard exited 0 for %s/%s (config %q): the belong stop did not refuse", tc.project, tc.installation, tc.configProject)
				}
				if stops != 1 {
					t.Errorf("the guard printed %d hard stops for %s/%s (config %q), want exactly the belong one: a stop that prints its message and carries on is not a stop\n%s", stops, tc.project, tc.installation, tc.configProject, out)
				}
				if !strings.Contains(out, "is the belong installation") {
					t.Errorf("the guard refused %s/%s (config %q) for some other reason: %s", tc.project, tc.installation, tc.configProject, out)
				}
				return
			}
			if strings.Contains(out, "is the belong installation") {
				t.Errorf("the guard refused the sandbox %s/%s as belong: %s", tc.project, tc.installation, out)
			}
			if stops != 1 || !strings.Contains(out, "does not exist") {
				t.Errorf("the guard did not reach the workdir stop for the sandbox %s/%s (stops: %d): %s", tc.project, tc.installation, stops, out)
			}
		})
	}

	// And with nothing set at all, the guard refuses rather than running with
	// an empty project: every variable is ${VAR:?}-guarded, and an empty one is
	// a hard error rather than an argument gcloud resolves from its ambient
	// configuration.
	t.Run("no project set", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "chk68-guard.sh")
		if err := os.WriteFile(script, []byte(guard+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, script)
		cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		cmd.Stdout = io.Discard
		runErr := cmd.Run()
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("the guard ran with CHK68_PROJECT unset (%v): the project is the one variable that must never default", runErr)
		}
		if out := stderr.String(); !strings.Contains(out, "CHK68_PROJECT") {
			t.Errorf("the guard stopped without naming CHK68_PROJECT: %s", out)
		}
	})
}

// TestReleaseManagedDeployerBulletStatesTheCurrentReason: the Before-you-tag
// bullet for `managedDeployerShipped` used to give the gate's reason as the
// 0.8.6 fence itself — "that binding's `fmgd-` fence is not an IAM condition GCP
// accepts as written (17 logical operators where IAM allows 12;
// `resource.name.contains()` is not a supported function; `resource.name` is not
// supported for most of the types it fences)" — and described one scoped role
// with one conditioned binding. Both halves are now wrong about this tree: the
// redesign replaced that shape with seven roles, seven conditioned bindings, at
// most three logical operators per expression and no `contains(` anywhere, and
// the reason the gate is still closed is that the fence has not been measured
// live (the design's §9, Check 68 — USER-RUN, NOT RUN). A release checklist read
// against the old text sends a release hunting a defect that is no longer there
// and names a shape the module does not write.
//
// Mutation (verified, run and restored): put the old reason sentence back into
// the bullet — this fails naming it.
func TestReleaseManagedDeployerBulletStatesTheCurrentReason(t *testing.T) {
	doc := readDoc(t, "docs/release.md")
	start := strings.Index(doc, "Verify `managedDeployerShipped`")
	if start < 0 {
		t.Fatal("docs/release.md's Before-you-tag list has no `managedDeployerShipped` bullet: it is where a release verifies the managed gate")
	}
	bullet := doc[start:]
	if end := strings.Index(bullet[1:], "\n- **"); end >= 0 {
		bullet = bullet[:end+1]
	}
	// The current reason and the current shape, so a release reads what the tree
	// gates off and why.
	for _, want := range []string{
		"seven conditioned bindings",
		"has not been measured live",
		"Check 68",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("the `managedDeployerShipped` bullet does not say %q: the gate is closed because the redesigned fence has not been measured live, and the bullet has to say so rather than describe a defect the redesign removed", want)
		}
	}
	// The pre-redesign shape, which is history and not the reason: the bullet may
	// record it as what the 2026-10-18 ruling responded to, and it may not state
	// it as what the gate is closed for.
	for _, wrong := range []string{
		"so no installation creates the deployer's scoped role or its conditioned binding",
		"is not an IAM condition GCP accepts as written",
	} {
		if strings.Contains(bullet, wrong) {
			t.Errorf("the `managedDeployerShipped` bullet states %q: this tree writes seven conditioned roles with at most three logical operators each and no `resource.name.contains()`, so the old single-binding defect is the history behind the 2026-10-18 ruling and not the reason the gate is closed", wrong)
		}
	}
}

// TestChecklistCheck68HardStopCountsAgree: the index row counted "four hard
// stops" while the guard block carries five `Check 68 stopped:` refusals and the
// section's own setup comment says "nothing below the five hard stops runs".
// The belong refusal (commit c98da729) added the fifth stop and updated the
// section prose; the row kept the number it had when the guard had four. Three
// places state the same fact, so this reads all three and requires one number —
// a guard whose count is stated in prose is exactly the kind of claim that
// survives a silent edit.
//
// Mutation (verified, run and restored): put "four hard stops" back into the row
// — this fails naming the two numbers.
func TestChecklistCheck68HardStopCountsAgree(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	numbers := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	// 1. The index row, the checklist's own summary of the check.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 68 |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 68 |` table row")
	}
	m := regexp.MustCompile(`\b(\w+) hard stops\b`).FindStringSubmatch(row)
	if m == nil {
		t.Fatal("Check 68's index row does not state its number of hard stops: the guard's refusals are what the row is describing")
	}
	rowN, ok := numbers[m[1]]
	if !ok {
		t.Fatalf("Check 68's index row states its hard stops as %q, which is not a count this test can read", m[1])
	}
	section := docSection(t, path, doc, "## Check 68:")
	// 2. The guard's own comment, which is what a reader re-running the block
	// is told to expect.
	m = regexp.MustCompile(`nothing below the (\w+) hard stops runs`).FindStringSubmatch(section)
	if m == nil {
		t.Fatal("Check 68's guard comment does not say how many hard stops run before the check's steps: the owner re-runs that block at every step")
	}
	sectionN, ok := numbers[m[1]]
	if !ok {
		t.Fatalf("Check 68's guard comment states its hard stops as %q, which is not a count this test can read", m[1])
	}
	// 3. The refusals themselves.
	stops := 0
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(line, "Check 68 stopped:") {
			stops++
		}
	}
	if rowN != sectionN || rowN != stops {
		t.Errorf("Check 68's hard stops are counted three ways and disagree: the index row says %d, the guard comment says %d, and the guard carries %d `Check 68 stopped:` refusals", rowN, sectionN, stops)
	}
	if stops < 5 {
		t.Errorf("Check 68's guard carries %d `Check 68 stopped:` refusals, and this check's belong stop is one of them: the guard is what keeps a live IAM check off the belong installation", stops)
	}
}

// TestGcpSetupSaysWhyTheManagedGateIsClosed: the operator guide's disabled-mode
// note carried the pre-redesign reason — the deployer's scoped role "is not an
// IAM condition GCP accepts yet", with the redesign still ahead ("after the
// fence is redesigned") — while the paragraph two above it, docs/release.md's
// bullet and docs/design/managed-projects.md's 0.9.6 note all state the reason
// this tree actually has: the redesign is written and in the tree, and what is
// missing is the live measurement (Check 68, written and NOT RUN). An operator
// reading the note is deciding whether the gate can be opened, so a reason the
// branch itself removed sends them looking for a defect that is no longer there.
//
// Mutations (verified, run and restored): the old reason sentence put back into
// the note, and "after the fence is redesigned" put back — both fail here.
func TestGcpSetupSaysWhyTheManagedGateIsClosed(t *testing.T) {
	const path = "docs/gcp-setup.md"
	doc := readDoc(t, path)
	const lead = "**Disabled since 0.8.6, and still off in 0.9.0.**"
	i := strings.Index(doc, lead)
	if i < 0 {
		t.Fatalf("%s no longer has the managed-mode disabled note (%q): it is where the guide says why the gate is closed", path, lead)
	}
	note := doc[i:]
	if end := strings.Index(note, "\n\n"); end >= 0 {
		note = note[:end]
	}
	for _, want := range []string{
		"measured live",
		"Check 68",
		"NOT RUN",
		"0.9.6",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("%s's disabled note does not say %q: the gate is closed because the redesigned fence has not been measured live (Check 68 is written and NOT RUN), and the note is where an operator reads that", path, want)
		}
	}
	for _, wrong := range []string{
		"is not an IAM condition GCP accepts",
		"after the fence is redesigned",
	} {
		if strings.Contains(note, wrong) {
			t.Errorf("%s's disabled note still states %q: this branch lands the redesign (seven roles, one per service, one binding each, at most three logical operators), so the fence's old defect is history and the reason the gate is closed is the missing live measurement", path, wrong)
		}
	}
}
