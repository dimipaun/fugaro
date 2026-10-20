package cli

import (
	"os"
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
// misses an `extract` template fails closed — is settled by the documented
// semantics of `extract` rather than measured here (no live name of a
// conditioned type can be made to miss its own template, so the register must
// not read as measuring one), the restore that closes the gate again, and the
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
		// the section says outright that this check does not settle it —
		// the documented semantics of `extract` do, and no live probe can
		// construct the mismatch (see the section's step 6). Without this the
		// register would read as measuring a question the check cannot
		// construct a probe for.
		"C7",
		"the documented semantics settle it",
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
		"BELONG=edge-devel-dimi",
		`[ "$P" != "$BELONG" ]`,
		`[ "$I" != belong ]`,
		`sed -n 's/^gcp_project: *//p' "$C"`,
		`$HOME/.config/fugaro/projects/${I:?}.yaml`,
		"exit 1",
		"--project=\"${P:?}\"",
		"mkdir -p \"$CHK68_TMP\"; chmod 700 \"$CHK68_TMP\"",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 68's guard block does not carry %q", want)
		}
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
	// The workdir and its backend, which is where the gate variable lives and
	// where the plan is made. The checkout's root has neither a tfvars file nor a
	// backend block, so a check that plans there cannot run at all.
	for _, want := range []string{
		`XDG_STATE_HOME:-$HOME/.local/state`,
		`/fugaro/terraform/${I:?}/installation`,
		`TF_DATA_DIR="$W/.terraform" TF_CLI_CONFIG_FILE="$W/terraformrc"`,
		"-backend-config=bucket=fugaro-tfstate-${P:?}",
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
		"xfmgd-chk68-bucket", "xfmgd-chk68-secret",
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

	// 4. The fail-closed question: named, disowned, and answered elsewhere. The
	// section has to say in as many words that this check does not settle C7 and
	// why — a SecretVersion is not the secret clause's type, so the read is
	// denied for want of the permission and the condition is never evaluated —
	// and that the documented semantics of `extract` are what settle it, because
	// the register must not read as measuring a question nothing live can.
	for _, want := range []string{
		"secretmanager.versions.*",
		"What this step does not settle",
		"C7 is not this check's question",
		"the documented semantics settle it",
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
		"rm -rf \"$CHK68_TMP\"",
		"**Passes when:**",
		"**Fails when:**",
		"**Paste back:**",
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
