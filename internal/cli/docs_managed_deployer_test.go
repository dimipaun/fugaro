package cli

import (
	"bytes"
	"fmt"
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
// safe to run: whose check it is, the nine hard stops in the guard block (the
// pin to installation `fugaro` / project `fugaro-dev`, the belong and
// production refusals comparing PROJECT IDS and the installation's name — never
// a `fugaro doctor` line read out of the checkout — the owner-typed variable,
// and the config's own project, region, Scheduler region and runs bucket), the
// workdir and backend
// configuration every terraform command below it needs (the checkout's
// `deploy/terraform/gcp/roots/installation` has no tfvars and no backend), the
// two directions every service is measured in, the Secret Manager version read
// and the section's own statement that the design's C7 — whether a name that
// misses an `extract` template fails closed — is UNVERIFIED rather than
// measured here, and cannot be measured here: no live name of a
// conditioned type can be made to miss its own template, and the grammar is the
// repository's reading of a reference no source in the tree quotes, so the
// register must not read as either measuring it or inheriting an answer for it),
// the clause-for-clause policy comparison of step 3 — keyed on BOTH sides by the
// role a clause belongs to, every clause of a multi-clause role included, so a
// clause that moved to another service's condition cannot diff clean — the
// restore that closes the gate again, and the
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
		// The throwaway resources, which are SIXTEEN and not the plan's
		// fourteen addresses: step 4 creates seven (one per service), step 5
		// three near misses (the bucket, the secret and the job) and step 5's
		// negative direction six more (`chk68-neg-*`, one per service — the
		// runs bucket is the installation's own and is read, not created) —
		// and the trap and the Restore delete all sixteen. The fourteen are
		// the plan's addresses — seven roles and seven bindings — and a row
		// that conflates the two counts the resources an owner has to find and
		// delete wrongly.
		"sixteen throwaway resources",
		"the seven per-service probes of step 4, the three near misses and the six negative probes of step 5",
		"chk68-neg-*",
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

	// 2. The guard block: the first block, with the eight hard stops, the
	// project typed by the owner and pinned by name rather than read from
	// gcloud, and the belong stop
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
		// The owner-typed variable: "no agent runs this" is prose, and this is
		// the stop that makes it a refusal. TestChecklistCheck68GuardPinsTheInstallation
		// runs the block to prove a value other than `yes` refuses.
		"CHK68_OWNER_RUN:?",
		// The pin: this check runs on installation `fugaro` in project
		// `fugaro-dev` and nowhere else (docs/dogfooding.md:3,11). Both names
		// are compared against, so a project or an installation that is not
		// belong and not production still cannot be pointed at.
		"PIN_INSTALLATION=fugaro",
		"PIN_PROJECT=fugaro-dev",
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
		// ... and production, by the same substring rule: the site's own
		// Firebase project is `fugaro-prod` and an owner running this check has
		// a *-prod project in their ambient configuration.
		"prod()",
		"*prod*",
		`sed -n 's/^gcp_project: *//p' "$C"`,
		`$HOME/.config/fugaro/projects/${I:?}.yaml`,
		"exit 1",
		"--project=\"${P:?}\"",
		"mkdir -p \"$CHK68_TMP\"; chmod 700 \"$CHK68_TMP\"",
		// The region and the runs bucket, read out of the installation's own
		// config: an environment variable is what a *different* installation's
		// value gets typed into, and step 5 reads the runs bucket.
		`sed -n 's/^region: *//p' "$C"`,
		`sed -n 's/^runs_bucket: *//p' "$C"`,
		// The Scheduler region is a value of its own and not the region: Cloud
		// Scheduler is not offered everywhere (this installation's us-east5
		// gives us-east4), so a Scheduler command given the region fails on the
		// pinned installation and its condition goes unmeasured. The config
		// records it — `fugaro init` writes it from gcp.SchedulerRegion — and
		// the guard refuses to run without it.
		`sed -n 's/^scheduler_region: *//p' "$C"`,
		"SREGION",
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
	if stops < 9 {
		t.Errorf("Check 68's guard carries %d hard stops, and the check documents nine", stops)
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
	// operator, and each of the seven services is acted on both ways. The
	// negative direction acts on resources the check creates itself
	// (`chk68-neg-*`), never on the sandbox installation's own: a probe that
	// fails must change nothing anyone else depends on, and a name the check
	// chose measures the same clause, because the conditions test
	// `resource.name`.
	for _, want := range []string{
		`--impersonate-service-account="${DEPLOYER:?}"`,
		"fmgd-chk68-bucket", "fmgd-chk68-secret", "fmgd-chk68-sa", "fmgd-chk68-repo",
		"fmgd-chk68-key", "fmgd-chk68-job", "fmgd-chk68-sched",
		"xfmgd-chk68-bucket", "xfmgd-chk68-secret", "xfmgd-chk68-job",
		"chk68-neg-secret", "chk68-neg-sa", "chk68-neg-repo",
		"chk68-neg-key", "chk68-neg-job", "chk68-neg-sched",
		"${RUNS_BUCKET:?}",
		"permission",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}
	if strings.Contains(section, "${SANDBOX_") {
		t.Error("Check 68 still reads a ${SANDBOX_*:?} name: the negative direction acted on the sandbox installation's own resources, and a fence that fails changes them with no undo")
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
	//
	// And the diff is keyed by the role each clause belongs to, on both sides:
	// a bare clause list diffs clean when a clause has moved from one service's
	// condition to another's, and which role fences which service is the design.
	// The policy side keys EVERY clause of a role, including the second clause
	// of a role that has two (storage has two), so it splits the expression into
	// clauses instead of substituting a newline into the string: an
	// interpolation over a multi-line string prefixes only its first line, and
	// the rest would diff unprefixed — a false alarm on a correct policy.
	for _, want := range []string{
		"--key-id=fmgd-chk68-key",
		`split(" || ")`,
		`(.condition.expression | split(" || "))[]`,
		`s/\\"/"/g`,
		`\${var.project}`,
		"policy-clauses.txt",
		"Every line of that diff carries the ROLE its clause belongs to",
		"deployer_condition_<service>",
		`/^  deployer_condition_[a-z]+ = join/`,
		`svc = $1`,
		`sub(/^deployer_condition_/, "", svc)`,
		`print svc "\t" line`,
		`sub(".*/fugaroDeployer"; "") | ascii_downcase`,
		"select(.condition.expression)",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q: a probe built any other way cannot produce a pass", want)
		}
	}
	if strings.Contains(section, `gsub(" \\|\\| "; "\n")`) {
		t.Error("Check 68 splits the policy's expression with gsub into a multi-line string: the role key lands on the first clause only, so a role with two clauses (storage) diffs a correct policy as a mismatch")
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
		// The Restore plans and reads the plan instead of applying with
		// -auto-approve: the restore is the one apply nobody reads, and it is
		// where a hand-edited resource would otherwise be applied unread.
		"terraform apply -input=false \"$CHK68_TMP/restore.tfplan\"",
		// The Restore deletes everything the check created, the near miss
		// included: a probe left behind is a resource the next run's plan and
		// the next reader both have to account for. One command per resource —
		// the line that used to carry `fmgd-chk68-job xfmgd-chk68-job` in one
		// `gcloud run jobs delete` deleted neither, because the subcommand takes
		// a single positional name.
		"gcloud run jobs delete fmgd-chk68-job --project=",
		"gcloud run jobs delete xfmgd-chk68-job --project=",
		"gcloud run jobs delete chk68-neg-job --project=",
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
// chk68GuardBlock is Check 68's setup + guard block, the first bash block of
// the section: the block every step re-runs, and the only thing that keeps a
// live IAM check off an installation it must never write in.
func chk68GuardBlock(t *testing.T) string {
	t.Helper()
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	blocks := bashBlocks(section)
	if len(blocks) == 0 {
		t.Fatal("Check 68 has no bash blocks")
	}
	return strings.Join(blocks[0], "\n")
}

// runChk68Guard writes the guard block to a script and runs it with the
// environment a case gives it, in the temporary HOME that case set up.
func runChk68Guard(t *testing.T, guard string, env []string) (int, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	script := filepath.Join(t.TempDir(), "chk68-guard.sh")
	if err := os.WriteFile(script, []byte(guard+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, script)
	cmd.Env = env
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
	return code, stderr.String()
}

// writeChk68Config writes an installation's local config where the guard reads
// it: $HOME/.config/fugaro/projects/<installation>.yaml, the file the guard now
// takes the project, the region, the Scheduler region and the runs bucket from.
// An empty value writes the key with nothing after it, which is what a config
// that carries no such value looks like to the guard's `sed`.
func writeChk68Config(t *testing.T, home, installation, project, region, schedulerRegion, runsBucket string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fugaro", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: " + installation + "\ngcp_project: " + project + "\nregion: " + region +
		"\nscheduler_region: " + schedulerRegion + "\nruns_bucket: " + runsBucket + "\n"
	if err := os.WriteFile(filepath.Join(dir, installation+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChecklistCheck68GuardRefusesBelong(t *testing.T) {
	guard := chk68GuardBlock(t)

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
		// The control: the pinned installation itself must get past the belong
		// stop. It stops at the next hard stop, because this test has no workdir
		// and no gcloud — and that is the point: the belong stop is not what
		// stopped it.
		{"the pinned installation", "fugaro-dev", "fugaro", "fugaro-dev", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.configProject != "" {
				writeChk68Config(t, home, tc.installation, tc.configProject, "us-east5", "us-east4", "sandbox-runs")
			}
			code, out := runChk68Guard(t, guard, []string{
				"HOME=" + home,
				"PATH=" + os.Getenv("PATH"),
				"CHK68_OWNER_RUN=yes",
				"CHK68_PROJECT=" + tc.project,
				"CHK68_INSTALLATION=" + tc.installation,
			})
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
}

// TestChecklistCheck68GuardPinsTheInstallation: Check 68's project and
// installation were free environment variables — `${CHK68_PROJECT:?}` and
// `${CHK68_INSTALLATION:?}` with nothing comparing them to each other, to the
// installation's own config, or to any name — so `CHK68_PROJECT=fugaro-prod`,
// or any project that is not belong, passed the belong stop and went on to edit
// a workdir's tfvars and apply a live IAM policy somewhere this check was never
// meant to run. The guard now pins the pair the tree records for this check —
// installation `fugaro` in project `fugaro-dev` (docs/dogfooding.md:3,11) —
// refuses belong and production by name, requires the installation's config to
// exist and to name `$P`, reads `L`, `SREGION` and `RUNS_BUCKET` out of that
// config rather than out of the environment, and refuses to run at all unless
// the owner has typed an explicit variable.
//
// Mutations (verified, run and restored): the pin comparison dropped → the
// wrong-project and wrong-installation cases pass; the `prod` refusal dropped →
// the production case passes; the config's project compared to nothing → the
// wrong-config case passes; `L=${L:?...}` put back → every case fails, because
// this test's environment sets no L and the config is the only place the region
// is written; the owner check dropped → the "not typed by the owner" case
// passes; a `${VAR:?}` dropped → the "every variable is required" case for that
// variable passes; the Scheduler region's stop dropped → the "config carries no
// scheduler region" case passes, and the check then runs every Scheduler
// command against the region on an installation where Scheduler is not offered
// there.
func TestChecklistCheck68GuardPinsTheInstallation(t *testing.T) {
	guard := chk68GuardBlock(t)

	cases := []struct {
		name            string
		ownerRun        string // CHK68_OWNER_RUN
		installation    string // CHK68_INSTALLATION
		project         string // CHK68_PROJECT
		configProject   string // the config's `gcp_project:`; "" writes no config
		configRegion    string // the config's `region:`; "" writes the file's own default
		configScheduler string // the config's `scheduler_region:`
		configBucket    string // the config's `runs_bucket:`
		want            string // the phrase the refusal must carry
	}{
		// The pin: one installation, by name. Nothing else runs this check.
		{"a project that is not fugaro-dev", "yes", "fugaro", "fugaro-sandbox-1", "fugaro-sandbox-1", "us-east5", "us-east4", "sandbox-runs",
			"runs on installation fugaro"},
		{"an installation that is not fugaro", "yes", "sandbox", "fugaro-dev", "fugaro-dev", "us-east5", "us-east4", "sandbox-runs",
			"runs on installation fugaro"},
		// Production, by name: the same rule the belong stop applies, for the
		// same reason — the ambient project of an owner running this is often a
		// *-prod one, and `fugaro-prod` is the site's own Firebase project.
		{"a production project", "yes", "fugaro", "fugaro-prod", "fugaro-prod", "us-east5", "us-east4", "sandbox-runs",
			"production project"},
		// The config is what names the installation's project, and the check
		// edits that installation's workdir and applies a policy in that project:
		// a config naming a different project is a refusal, not a warning.
		{"the config names another project", "yes", "fugaro", "fugaro-dev", "fugaro-other", "us-east5", "us-east4", "sandbox-runs",
			"has to exist and name gcp_project"},
		{"no config at all", "yes", "fugaro", "fugaro-dev", "", "", "", "",
			"has to exist and name gcp_project"},
		// L and RUNS_BUCKET come from the config now, so a config that carries
		// neither is a stop rather than a step that acts on an empty name.
		{"the config carries no region", "yes", "fugaro", "fugaro-dev", "fugaro-dev", "", "us-east4", "sandbox-runs",
			"region=''"},
		{"the config carries no runs bucket", "yes", "fugaro", "fugaro-dev", "fugaro-dev", "us-east5", "us-east4", "",
			"runs_bucket=''"},
		// And the Scheduler region, which is not the region: Cloud Scheduler is
		// not offered in every region, so a Scheduler command given `L` fails on
		// the pinned installation and the one condition this check would then
		// never measure is the scheduler job's. The stop is what keeps that from
		// being a silent skip.
		{"the config carries no scheduler region", "yes", "fugaro", "fugaro-dev", "fugaro-dev", "us-east5", "", "sandbox-runs",
			"no scheduler_region"},
		// And the owner-typed variable: prose that says "no agent runs this" is
		// not a stop, and this is the one that is.
		{"the owner did not type the run variable", "maybe", "fugaro", "fugaro-dev", "fugaro-dev", "us-east5", "us-east4", "sandbox-runs",
			"CHK68_OWNER_RUN"},
		// The control: the pinned installation with a config that names it, and
		// nothing else in place, reaches the workdir stop — no earlier stop is
		// what refused it.
		{"the pinned installation", "yes", "fugaro", "fugaro-dev", "fugaro-dev", "us-east5", "us-east4", "sandbox-runs",
			"does not exist"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.configProject != "" {
				writeChk68Config(t, home, tc.installation, tc.configProject, tc.configRegion, tc.configScheduler, tc.configBucket)
			}
			code, out := runChk68Guard(t, guard, []string{
				"HOME=" + home,
				"PATH=" + os.Getenv("PATH"),
				"CHK68_OWNER_RUN=" + tc.ownerRun,
				"CHK68_PROJECT=" + tc.project,
				"CHK68_INSTALLATION=" + tc.installation,
			})
			if code == 0 {
				t.Fatalf("the guard exited 0 for %s (I=%q P=%q config=%q): the refusal did not fire", tc.name, tc.installation, tc.project, tc.configProject)
			}
			if stops := strings.Count(out, "Check 68 stopped:"); stops != 1 {
				t.Errorf("the guard printed %d hard stops for %s, want exactly one:\n%s", stops, tc.name, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the guard refused %s without saying %q:\n%s", tc.name, tc.want, out)
			}
			if strings.Contains(out, "gcloud config get-value project") {
				t.Errorf("the guard read the project from gcloud's ambient configuration:\n%s", out)
			}
		})
	}

	// Every variable the guard needs is required, one at a time: an unset
	// variable is a hard error naming itself, never an empty argument that
	// gcloud resolves from its ambient configuration or that the config read
	// turns into an empty name.
	t.Run("every variable is required", func(t *testing.T) {
		for _, missing := range []string{"CHK68_OWNER_RUN", "CHK68_PROJECT", "CHK68_INSTALLATION"} {
			t.Run(missing, func(t *testing.T) {
				home := t.TempDir()
				writeChk68Config(t, home, "fugaro", "fugaro-dev", "us-east5", "us-east4", "sandbox-runs")
				env := []string{
					"HOME=" + home,
					"PATH=" + os.Getenv("PATH"),
					"CHK68_OWNER_RUN=yes",
					"CHK68_PROJECT=fugaro-dev",
					"CHK68_INSTALLATION=fugaro",
				}
				var kept []string
				for _, e := range env {
					if strings.HasPrefix(e, missing+"=") {
						continue
					}
					kept = append(kept, e)
				}
				code, out := runChk68Guard(t, guard, kept)
				if code == 0 {
					t.Fatalf("the guard ran with %s unset: it is one of the variables that must never default", missing)
				}
				if !strings.Contains(out, missing) {
					t.Errorf("the guard stopped with %s unset without naming it:\n%s", missing, out)
				}
			})
		}
	})

	// And with nothing set at all, the guard refuses rather than running with
	// an empty project: every variable is ${VAR:?}-guarded, and an empty one is
	// a hard error rather than an argument gcloud resolves from its ambient
	// configuration. Which variable the shell names first is the declaration
	// order's business and not a claim here; that each of the three is required
	// on its own is the subtest above.
	t.Run("nothing set at all", func(t *testing.T) {
		code, out := runChk68Guard(t, guard, []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")})
		if code == 0 {
			t.Fatal("the guard ran with nothing set: every variable it acts on must never default")
		}
		if !strings.Contains(out, "CHK68_") {
			t.Errorf("the guard stopped without naming a CHK68_ variable: %s", out)
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
// section prose; the row kept the number it had when the guard had four. The
// independent review of the redesigned runbook then added three more — the pin
// to installation `fugaro` / project `fugaro-dev`, the owner-typed variable, and
// the config's own project, region and runs bucket — so the number was eight,
// and the Scheduler region's own stop (Cloud Scheduler is not offered in every
// region, and the region is not its location) makes it nine.
// Three places state the same fact, so this reads all three and requires one
// number — a guard whose count is stated in prose is exactly the kind of claim
// that survives a silent edit.
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
	if stops < 9 {
		t.Errorf("Check 68's guard carries %d `Check 68 stopped:` refusals, and this check's belong stop and its pin to the sandbox installation are among them: the guard is what keeps a live IAM check off an installation it must never write in", stops)
	}
}

// TestGcpSetupSaysWhyTheManagedGateIsClosed: the operator guide's disabled-mode
// note carried the pre-redesign reason — the deployer's scoped role "is not an
// IAM condition GCP accepts yet", with the redesign still ahead ("after the
// fence is redesigned") — while the paragraph two above it, docs/release.md's
// bullet and docs/design/managed-projects.md's 0.10.0 note all state the reason
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
		"0.10.0",
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

// TestChecklistCheck68NegativeDirectionNamesAgree: the negative direction acted
// on the sandbox installation's own resources — its secret, its service account,
// its repository, its API key, its Cloud Run job, its scheduler job, and a write
// into its runs bucket — so a fence that FAILED changed all seven with no undo:
// a paused scheduler job, a job's environment, a display name, a description,
// labels, and a file left in the runs bucket. The failure this check exists to
// find was the one case in which it damaged the installation it was measuring.
//
// Every negative probe now acts on a resource the check creates itself and the
// Restore deletes (`chk68-neg-*`, a name no condition admits — deliberately
// without the `fmgd-` prefix, and not a near miss of it either, since a near miss
// contains the prefix), except the runs bucket, which is read and not written:
// `storage.objects.list` is in the conditioned storage role alone, so the read is
// a real test of a condition, and it changes nothing if the fence fails. A trap
// in the guard block deletes the check's own resources when the shell exits, so a
// failed probe leaves nothing behind, and it says that it does not close the
// gate. This holds the three statements of that invariant together: step 5 acts
// only on the check's own names, the trap and the Restore both name every one of
// them, and no `${SANDBOX_*}` name survives anywhere in the section.
//
// Mutations (verified, run and restored): a negative probe put back on the
// sandbox's own resource → FAIL; the runs-bucket read turned back into a write
// (`storage cp`) → FAIL; a created resource dropped from the trap → FAIL; a
// created resource dropped from the Restore → FAIL; the trap moved above the
// hard stops → FAIL; the trap's "it does not close the gate" line dropped →
// FAIL; the trap removed → FAIL.
func TestChecklistCheck68NegativeDirectionNamesAgree(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")

	// The check's own names, as step 4 and step 5 create them. The negative
	// direction's six, plus the runs bucket, which is read and never written.
	created := []string{
		"fmgd-chk68-bucket", "fmgd-chk68-secret", "fmgd-chk68-sa",
		"fmgd-chk68-repo", "fmgd-chk68-key", "fmgd-chk68-job", "fmgd-chk68-sched",
		"xfmgd-chk68-bucket", "xfmgd-chk68-secret", "xfmgd-chk68-job",
		"chk68-neg-secret", "chk68-neg-sa", "chk68-neg-repo",
		"chk68-neg-key", "chk68-neg-job", "chk68-neg-sched",
	}
	negatives := []string{
		"chk68-neg-secret", "chk68-neg-sa", "chk68-neg-repo",
		"chk68-neg-key", "chk68-neg-job", "chk68-neg-sched",
	}

	// 1. No sandbox name anywhere: the section used to carry six ${SANDBOX_*:?}
	// guards, and the whole point of the rewrite is that they are gone.
	if i := strings.Index(section, "${SANDBOX_"); i >= 0 {
		t.Errorf("%s's Check 68 still reads a ${SANDBOX_*:?} name (%q): the negative direction must act on a resource the check creates itself, because a probe that acts on the installation's own changes it when the fence fails, and nothing undoes that", path, section[i:i+20])
	}

	// 2. Step 5's first block: seven actions, one per service, each on a name the
	// check owns — and the one action on the installation's own resource is a
	// read.
	step5 := docSection(t, path, section, "### 5. The negative direction")
	if end := strings.Index(step5, "**Then the near miss"); end >= 0 {
		step5 = step5[:end]
	}
	var acts, creates, asDeployer []string
	for _, line := range strings.Split(step5, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "gcloud ") {
			continue
		}
		acts = append(acts, s)
		if strings.Contains(s, " create ") {
			creates = append(creates, s)
		}
		if strings.Contains(s, `--impersonate-service-account="${DEPLOYER:?}"`) {
			asDeployer = append(asDeployer, s)
		}
	}
	// The seven actions are the ones taken as the deployer; the six creates are
	// the owner's own identity, and they are what makes the probes the check's.
	if len(asDeployer) != 7 {
		t.Fatalf("%s's Check 68 step 5 takes %d actions as the deployer, want one per service (seven): %v", path, len(asDeployer), asDeployer)
	}
	if len(creates) != 6 {
		t.Errorf("%s's Check 68 step 5 creates %d resources, want the six the negative probes act on (the runs bucket is the installation's own and is read): %v", path, len(creates), creates)
	}
	for _, a := range creates {
		if !strings.Contains(a, "chk68-neg-") {
			t.Errorf("%s's Check 68 step 5 creates %q: every resource the negative direction creates is named chk68-neg-*, a name no condition admits; a create under the fmgd- prefix would be a positive probe, and a create under a near miss of it would test the prefix rather than the probe", path, a)
		}
	}
	for _, a := range acts {
		own := false
		for _, name := range created {
			if strings.Contains(a, name) {
				own = true
			}
		}
		if strings.Contains(a, "${RUNS_BUCKET:?}") {
			if !strings.HasPrefix(a, "gcloud storage ls ") {
				t.Errorf("%s's Check 68 step 5 acts on the runs bucket with %q: the bucket is the installation's own, and the one probe against it has to be a read — `gcloud storage ls` — which changes nothing if the fence fails and still tests the conditioned storage role, whose storage.objects.list is in no other deployer role", path, a)
			}
			own = true
		}
		if !own {
			t.Errorf("%s's Check 68 step 5 acts on %q, which is neither a resource the check creates (fmgd-chk68-*, xfmgd-chk68-*, chk68-neg-*) nor the runs bucket: every negative probe acts on the check's own, because a fence that fails must change nothing anyone else depends on", path, a)
		}
	}
	// The seven services, one action each, as the deployer.
	for _, want := range []string{"secrets update", "service-accounts update", "repositories update", "api-keys update", "run jobs update", "scheduler jobs pause", "storage ls"} {
		found := false
		for _, a := range acts {
			if strings.Contains(a, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s's Check 68 step 5 has no %q action: the negative direction is one action per service, and the seven are the design's seven roles", path, want)
		}
	}

	// 3. The trap in the guard block: it deletes every resource the check
	// created, it is set after the hard stops, and it says what it does not do.
	guard := chk68GuardBlock(t)
	if !strings.Contains(guard, "trap cleanup_chk68 EXIT") {
		t.Error("Check 68's guard block has no `trap cleanup_chk68 EXIT`: a probe that fails, or a step that stops the check, must not leave the check's own bucket, secret, job or service account behind for the next reader to account for")
	}
	for _, name := range created {
		if !strings.Contains(guard, name) {
			t.Errorf("Check 68's cleanup_chk68 trap does not delete %q: every resource the check creates has to be one the trap removes, or a failed run leaves it behind", name)
		}
	}
	if !strings.Contains(guard, "run step 7 (Restore)") {
		t.Error("Check 68's cleanup_chk68 trap does not say that it leaves the gate open and the seven bindings in place: a trap that fired is a reason to run the Restore, not a reason to think the project is as it was")
	}
	lastStop := strings.LastIndex(guard, "Check 68 stopped:")
	trap := strings.Index(guard, "trap cleanup_chk68 EXIT")
	if lastStop < 0 || trap < lastStop {
		t.Error("Check 68's cleanup_chk68 trap is installed before the guard's hard stops: a stop that fires before anything is created has nothing to clean up, and a trap armed above them would run the cleanup for a check that never started")
	}

	// 4. The Restore deletes them too, so the check does not depend on the trap
	// having fired — and it says the same thing about the six negative probes.
	restore := docSection(t, path, section, "### 7. Restore")
	for _, name := range negatives {
		if !strings.Contains(restore, name) {
			t.Errorf("Check 68's Restore does not delete %q: the negative direction's resources are the check's own, and the Restore is what deletes them", name)
		}
	}
}

// TestChecklistCheck68AssertsThePlanShape: step 2 applied whatever plan step 1
// had written and step 7 restored with `-auto-approve` and no plan at all, so a
// plan carrying anything other than the gate's own fourteen addresses — a
// resource the owner had edited by hand, a provider that wanted to replace
// something, a state bucket that is not the one the installation lives under —
// was applied unread, in a live project, twice. The check now reads the shape
// before each apply: fourteen to add and nothing to change or destroy when the
// gate opens, and on the restore a plan that destroys those fourteen addresses
// and nothing else.
//
// Mutations (verified, run and restored): step 2's shape assertion removed →
// FAIL; the restore's plan-then-apply reverted to `-auto-approve` → FAIL; the
// restore's "nothing outside the gate" clause dropped → FAIL; the assertion
// moved after its apply → FAIL.
func TestChecklistCheck68AssertsThePlanShape(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := func(name, next string) string {
		i := strings.Index(section, name)
		if i < 0 {
			t.Fatalf("Check 68 has no %q step: the plan's shape is asserted there", name)
		}
		rest := section[i:]
		if j := strings.Index(rest, next); j >= 0 {
			return rest[:j]
		}
		return rest
	}

	// Step 2, the one live write of the opening half.
	step2 := step("### 2. Apply it", "### 3.")
	if !strings.Contains(step2, `terraform show -json "$CHK68_TMP/chk68.tfplan"`) {
		t.Error("Check 68's step 2 does not read the plan it is about to apply: the apply is a live write in the pinned project, and the plan file is the last chance to see what it carries")
	}
	for _, want := range []string{
		`select(.change.actions == ["create"])`,
		`select(.change.actions == ["update"])`,
		`select(.change.actions == ["delete"])`,
		`[ "$ADD" = 14 ] && [ "$CHG" = 0 ] && [ "$DEL" = 0 ]`,
		"Check 68 plan stop:",
	} {
		if !strings.Contains(step2, want) {
			t.Errorf("Check 68's step 2 does not carry %q: opening the gate on a converged installation adds the gate's fourteen addresses and changes or destroys nothing, and a plan of any other shape is a reason to stop rather than apply", want)
		}
	}
	apply := strings.Index(step2, `terraform apply -input=false "$CHK68_TMP/chk68.tfplan"`)
	asserted := strings.Index(step2, "Check 68 plan stop:")
	if apply < 0 {
		t.Fatal("Check 68's step 2 no longer applies the plan step 1 wrote")
	}
	if asserted < 0 || asserted > apply {
		t.Error("Check 68's step 2 asserts the plan's shape after the apply it is meant to gate: the assertion is what makes the apply safe, so it runs first")
	}

	// Step 7, the restore.
	restore := step("### 7. Restore", "**FACT (on `${P:?}`):** the binding count")
	for _, line := range strings.Split(restore, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "terraform apply") && strings.Contains(line, "-auto-approve") {
			t.Errorf("Check 68's Restore applies with -auto-approve (%q): the restore is the one apply nobody reads, and it has to be planned and its plan asserted before it runs", line)
		}
	}
	for _, want := range []string{
		`terraform plan -input=false -out="$CHK68_TMP/restore.tfplan"`,
		`terraform show -json "$CHK68_TMP/restore.tfplan"`,
		`terraform apply -input=false "$CHK68_TMP/restore.tfplan"`,
		`[ "$ADD" = 0 ] && [ "$CHG" = 0 ] && [ "$DEL" = 14 ] && [ "$OTHER" = 0 ]`,
		`google_project_iam_(custom_role|member)\.fugaro_deployer_(storage|secret|account|apikey|repository|run_job|scheduler_job)\[`,
		"Check 68 plan stop:",
	} {
		if !strings.Contains(restore, want) {
			t.Errorf("Check 68's Restore does not carry %q: closing the gate destroys the seven roles and the seven bindings and nothing else, and the plan is where that is checked", want)
		}
	}
	planned := strings.Index(restore, `terraform plan -input=false -out="$CHK68_TMP/restore.tfplan"`)
	restoreApply := strings.Index(restore, `terraform apply -input=false "$CHK68_TMP/restore.tfplan"`)
	restoreAsserted := strings.Index(restore, "Check 68 plan stop:")
	if planned < 0 || restoreApply < 0 {
		t.Fatal("Check 68's Restore no longer plans and applies that plan")
	}
	if restoreAsserted < 0 || restoreAsserted > restoreApply {
		t.Error("Check 68's Restore asserts the plan's shape after applying it")
	}
}

// chk68Block returns the first bash block of section that carries marker. The
// section's blocks are addressed by what they contain and not by index: a step
// that grows a second block would otherwise move every assertion onto its
// neighbour's text.
func chk68Block(t *testing.T, section, marker string) []string {
	t.Helper()
	for _, b := range bashBlocks(section) {
		if strings.Contains(strings.Join(b, "\n"), marker) {
			return b
		}
	}
	t.Fatalf("Check 68 has no bash block carrying %q", marker)
	return nil
}

// chk68FuncBody returns the body of the shell function name declared in the
// guard block, from its `name() {` line to the `}` at column 0 that closes it.
func chk68FuncBody(t *testing.T, guard, name string) string {
	t.Helper()
	i := strings.Index(guard, "\n"+name+"() {")
	if i < 0 {
		t.Fatalf("Check 68's guard block does not declare %s()", name)
	}
	rest := guard[i+1:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j+1]
	}
	t.Fatalf("Check 68's %s() is never closed by a `}` at column 0", name)
	return ""
}

// runChk68Shell writes script to a file and runs it under bash, returning the
// exit code, stdout and stderr. It skips when bash is not on PATH, the way
// runChk68Guard does: what the tests below run is Check 68's own lines, and bash
// is what they are written in.
func runChk68Shell(t *testing.T, script string, env []string) (int, string, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	file := filepath.Join(t.TempDir(), "chk68.sh")
	if err := os.WriteFile(file, []byte(script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, file)
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()
	code := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if runErr != nil {
		t.Fatalf("running the script: %v", runErr)
	}
	return code, out.String(), errOut.String()
}

// TestChecklistCheck68RestorePlanFilterIsRun executes Check 68's restore
// assertion instead of pinning its text.
//
// The assertion's job is to let the restore apply only a plan that destroys the
// gate's own fourteen addresses and nothing else, and as it was first written it
// could not do that job at all: the address filter was anchored at
// `^google_project_iam_…`, and a root plan's addresses carry the root's module —
// `module.installation.google_project_iam_custom_role.fugaro_deployer_storage[0]`
// (deploy/terraform/gcp/roots/installation/main.tf declares `module
// "installation"`). So OTHER counted the fourteen gate addresses themselves, the
// stop fired on a CORRECT plan, and the restore never ran: the check would have
// ended with the gate open and seven conditioned roles and bindings live in
// `fugaro-dev`, which is the one state the section forbids. A test that read the
// filter as a string could not see it — the string was what the review asked
// for.
//
// So this test runs the block. The four counts, the `grep -cvE` filter and the
// stop are lifted out of the section exactly as they are written, a synthetic
// `restore.json` is written for each case, and the block is run under bash with
// `CHK68_TMP` pointing at it. The filter's own ERE is compiled and applied
// in-process as well, so the case that matters — a module-prefixed gate address
// — is checked even where jq is not installed.
//
// Mutations (verified, run and restored): the filter anchored back at
// `^google_project_iam_…` → every module-prefixed case fails, in the ERE check
// and in the run; the `OTHER = 0` clause dropped from the stop → the
// extra-address cases pass; the stop's `exit 1` turned into a no-op → the
// extra-address cases pass; the jq `select` on `["no-op"]` dropped → the no-op
// case fails; `set -e` added above the counts → the passing cases fail, because
// `grep -cv` exits 1 when it excludes nothing.
func TestChecklistCheck68RestorePlanFilterIsRun(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	restore := docSection(t, path, section, "### 7. Restore")

	// The assertion, as the section writes it: from the first count to the line
	// carrying the stop.
	lines := strings.Split(restore, "\n")
	start, stop := -1, -1
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if start < 0 && strings.HasPrefix(s, "ADD=$(jq") {
			start = i
		}
		if start >= 0 && strings.Contains(l, "Check 68 plan stop:") {
			stop = i
			break
		}
	}
	if start < 0 {
		t.Fatal("Check 68's Restore no longer counts the plan's adds, changes and destroys (there is no `ADD=$(jq` line): the restore is the one apply nobody reads, and the counts are what stop it")
	}
	if stop < 0 {
		t.Fatal("Check 68's Restore counts the plan and never stops on it: the counts have to be asserted rather than printed")
	}
	assertion := strings.Join(lines[start:stop+1], "\n")
	if !strings.Contains(assertion, "exit 1") {
		t.Fatal("Check 68's restore assertion does not refuse: the stop prints its message and carries on, which is not a stop")
	}
	// The stop leaves the gate open — the roles and bindings the restore was
	// about to destroy are still live — and the one state the section forbids is
	// exactly that. A stop that does not say so reads as "the restore is done".
	if !strings.Contains(assertion, "THE GATE IS STILL OPEN") {
		t.Error("Check 68's restore stop does not say that the gate is still open when it fires: hitting it leaves the seven conditioned roles and their bindings live in the project, and a reader who does not know that stops there")
	}
	// `grep -cv` exits 1 when it excludes nothing, and excluding nothing is the
	// PASSING case: `set -e` anywhere in the restore would abort it exactly when
	// the plan is right, and the gate would never be closed.
	if strings.Contains(restore, "set -e") {
		t.Error("Check 68's Restore turns on `set -e`: the `grep -cv` in the OTHER count exits 1 when it excludes nothing, which is the case the restore has to pass, so `set -e` would abort it there and leave the gate open")
	}

	// The filter's own ERE, compiled here: this half runs without jq.
	ere := ""
	for _, l := range lines[start : stop+1] {
		if i := strings.Index(l, "grep -cvE '"); i >= 0 {
			ere = strings.TrimSuffix(l[i+len("grep -cvE '"):], "')")
		}
	}
	if ere == "" {
		t.Fatal("Check 68's restore filter no longer runs `grep -cvE '…'` over the plan's addresses")
	}
	re, err := regexp.Compile(ere)
	if err != nil {
		t.Fatalf("Check 68's restore filter is not an ERE grep -E can run: %v", err)
	}

	services := []string{"storage", "secret", "account", "apikey", "repository", "run_job", "scheduler_job"}
	kinds := []string{"google_project_iam_custom_role", "google_project_iam_member"}
	var gate []string
	for _, k := range kinds {
		for _, s := range services {
			gate = append(gate, "module.installation."+k+".fugaro_deployer_"+s+"[0]")
		}
	}
	if len(gate) != 14 {
		t.Fatalf("the gate is seven roles and seven bindings, and this test built %d addresses", len(gate))
	}
	// What the gate does not write, and what the restore must therefore refuse to
	// destroy: the unconditioned role, a name one service away from a gate
	// address, the unconditioned binding, and a resource of another type.
	outside := []string{
		"module.installation.google_project_iam_custom_role.fugaro_deployer[0]",
		"module.installation.google_project_iam_custom_role.fugaro_deployer_extra[0]",
		"module.installation.google_project_iam_member.fugaro_deployer",
		"module.installation.google_storage_bucket.runs[0]",
		"module.installation.google_service_account.fugaro_deployer[0]",
	}
	for _, a := range gate {
		if !re.MatchString(a) {
			t.Errorf("Check 68's restore filter does not exclude %q, which is one of the gate's own fourteen addresses: a root plan's addresses carry the root's module name, so OTHER counts them, the stop fires on a correct plan, and the restore that closes the gate never runs", a)
		}
		// The same address without the module prefix: the other form a plan's
		// addresses take, and what the filter's optional group is for.
		if bare := strings.TrimPrefix(a, "module.installation."); !re.MatchString(bare) {
			t.Errorf("Check 68's restore filter does not exclude %q: the module prefix is optional in the addresses a root plan carries, and a filter that only knows the prefixed form counts the gate's own addresses as outside it", bare)
		}
	}
	for _, a := range outside {
		if re.MatchString(a) {
			t.Errorf("Check 68's restore filter excludes %q, which is not one of the gate's fourteen addresses: the restore may destroy the seven roles and the seven bindings and nothing else", a)
		}
	}

	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not on PATH: the filter's own ERE is checked above")
	}
	type change struct {
		address string
		actions []string
	}
	planJSON := func(changes []change) string {
		parts := make([]string, 0, len(changes))
		for _, c := range changes {
			quoted := make([]string, 0, len(c.actions))
			for _, a := range c.actions {
				quoted = append(quoted, `"`+a+`"`)
			}
			parts = append(parts, fmt.Sprintf(`{"address":%q,"change":{"actions":[%s]}}`, c.address, strings.Join(quoted, ",")))
		}
		return `{"resource_changes":[` + strings.Join(parts, ",") + `]}`
	}
	destroys := func(addrs []string) []change {
		out := make([]change, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, change{a, []string{"delete"}})
		}
		return out
	}
	unprefixed := func(addrs []string) []string {
		out := make([]string, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, strings.TrimPrefix(a, "module.installation."))
		}
		return out
	}
	// A resource of the installation's own that the gate neither creates nor
	// destroys: the restore's plan is full of these, and they are no-ops.
	own := change{"module.installation.google_service_account.fugaro_deployer", []string{"no-op"}}

	cases := []struct {
		name    string
		changes []change
		passes  bool
		want    string
	}{
		{"the gate's own fourteen, module-prefixed",
			append(destroys(gate), own), true,
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate"},
		{"the gate's own fourteen, unprefixed",
			append(destroys(unprefixed(gate)), own), true,
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate"},
		{"a no-op outside the gate",
			append(append(destroys(gate), own), change{"module.installation.google_storage_bucket.runs[0]", []string{"no-op"}}), true,
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate"},
		{"a fifteenth destroy, of another resource",
			append(destroys(gate), change{"module.installation.google_storage_bucket.runs[0]", []string{"delete"}}), false,
			"1 address(es) outside the gate"},
		{"a destroy of a gate-shaped name the gate does not write",
			append(destroys(gate), change{"module.installation.google_project_iam_custom_role.fugaro_deployer_extra[0]", []string{"delete"}}), false,
			"1 address(es) outside the gate"},
		{"a destroy of the unconditioned binding",
			append(destroys(gate), change{"module.installation.google_project_iam_member.fugaro_deployer", []string{"delete"}}), false,
			"1 address(es) outside the gate"},
		// A REPLACEMENT, and it is the case the three counts cannot see: the
		// plan's actions are ["delete", "create"], so ADD, CHG and DEL all count
		// nothing for it, and only the address filter stands between the restore
		// and a resource of the installation's own being destroyed and rebuilt.
		{"a replace outside the gate, which the three counts cannot see",
			append(destroys(gate), change{"module.installation.google_service_account.runs", []string{"delete", "create"}}), false,
			"1 address(es) outside the gate"},
		{"an add the restore did not make",
			append(append(destroys(gate), own), change{"module.installation.google_service_account.runs", []string{"create"}}), false,
			"1 to add"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "restore.json"), []byte(planJSON(tc.changes)), 0o600); err != nil {
				t.Fatal(err)
			}
			script := "set -u\nCHK68_TMP='" + dir + "'\nP=fugaro-dev\n" + assertion + "\necho CHK68-ASSERTED\n"
			code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
			if tc.passes {
				if code != 0 {
					t.Fatalf("Check 68's restore assertion refused a plan of the shape the restore is for (exit %d):\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
				}
				if !strings.Contains(out, "CHK68-ASSERTED") {
					t.Fatalf("the assertion did not reach the end of the block, so it stopped somewhere it should not have:\nstdout:\n%s\nstderr:\n%s", out, errOut)
				}
				if !strings.Contains(out, tc.want) {
					t.Errorf("Check 68's restore assertion ran but did not print %q:\n%s", tc.want, out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("Check 68's restore assertion passed a plan the restore must refuse:\n%s", out)
			}
			if !strings.Contains(errOut, "Check 68 plan stop:") {
				t.Errorf("the assertion refused the plan without a `Check 68 plan stop:` line:\nstderr:\n%s", errOut)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("the assertion's stop does not carry %q:\nstderr:\n%s", tc.want, errOut)
			}
		})
	}
}

// TestChecklistCheck68LeftoverFilterIsRun: Check 68's own guarantee is that it
// leaves nothing behind, and the two places that check the guarantee — step 0's
// read-only look and the end of the Restore — filter the six listings with
// `$CHK68_OWNED`. The filter has to match the name inside a line rather than at
// the start of it: five of the six listings print a FULL RESOURCE PATH
// (`projects/<P>/secrets/<name>`, `…/locations/<L>/repositories/<name>`), so an
// anchored `^fmgd-chk68-` matches no line any of them prints, and the check
// reports "no leftover" with all sixteen resources still in the project.
//
// This runs both blocks against a stubbed `gcloud` — the six listings print a
// synthetic project, once with a leftover of the check's own and once without —
// so what decides is the section's own filter, stop and messages. The filter's
// ERE is also applied in-process, so the anchoring is checked even where bash is
// absent.
//
// Mutations (verified, run and restored): `CHK68_OWNED` anchored at `^fmgd-…`
// (the form the prose was written for) → the leftover cases report no leftover
// and fail; the `[ -z "$CHK68_LEFT" ]` stop dropped from the Restore → the
// leftover case exits 0; step 0's filter dropped → the leftover case prints
// nothing of the check's own; a listing dropped from either look → the leftover
// that listing carries is not seen, and the case fails on the line it is missing
// (the scheduler listing, for the one leftover only it prints); a listing added
// → the stub reports the unexpected call and the block fails.
func TestChecklistCheck68LeftoverFilterIsRun(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	guard := chk68GuardBlock(t)

	owned := ""
	for _, l := range strings.Split(guard, "\n") {
		if s := strings.TrimSpace(l); strings.HasPrefix(s, "CHK68_OWNED=") {
			owned = s
		}
	}
	if owned == "" {
		t.Fatal("Check 68's guard block no longer defines CHK68_OWNED: both looks for the check's own leftovers filter on it")
	}
	ere := strings.Trim(strings.TrimPrefix(owned, "CHK68_OWNED="), "'")
	for _, anchor := range []string{"^fmgd-", "^xfmgd-", "^chk68-"} {
		if strings.HasPrefix(ere, anchor) {
			t.Errorf("Check 68's leftover filter is %s, anchored at the start of the line: five of the six listings print a full resource path, so an anchored prefix matches none of the check's own names and the look reports an empty project", ere)
		}
	}
	re, err := regexp.Compile(ere)
	if err != nil {
		t.Fatalf("Check 68's leftover filter is not an ERE grep -E can run: %v", err)
	}
	// The names as the listings print them, with the prefix inside the line.
	leftovers := []string{
		"projects/fugaro-dev/secrets/chk68-neg-secret",
		"projects/fugaro-dev/secrets/fmgd-chk68-secret",
		"projects/fugaro-dev/locations/us-east5/repositories/fmgd-chk68-repo",
		"projects/fugaro-dev/locations/us-east5/jobs/fmgd-chk68-job",
		"projects/fugaro-dev/locations/us-east4/jobs/xfmgd-chk68-sched",
		"projects/fugaro-dev/locations/global/keys/fmgd-chk68-key",
		"fmgd-chk68-sa@fugaro-dev.iam.gserviceaccount.com",
		"chk68-neg-sa@fugaro-dev.iam.gserviceaccount.com",
	}
	// The installation's own, which the filter must leave alone: a look that
	// reports these is a look that sends the operator deleting the sandbox.
	ownLines := []string{
		"projects/fugaro-dev/secrets/fugaro-runs",
		"projects/fugaro-dev/locations/us-east5/repositories/fugaro-base",
		"projects/fugaro-dev/locations/us-east5/jobs/fugaro-daily",
		"projects/fugaro-dev/locations/us-east4/jobs/fugaro-image-check",
		"projects/fugaro-dev/locations/global/keys/abc123",
		"fugaro-deployer@fugaro-dev.iam.gserviceaccount.com",
		"fugaro-history@fugaro-dev.iam.gserviceaccount.com",
	}
	for _, l := range leftovers {
		if !re.MatchString(l) {
			t.Errorf("Check 68's leftover filter does not match %q, a line one of the six listings prints for a resource the check created: the look would report nothing left behind while the resource was there", l)
		}
	}
	for _, l := range ownLines {
		if re.MatchString(l) {
			t.Errorf("Check 68's leftover filter matches %q, which is the installation's own: a look that prints it sends the operator to delete a resource the check never created", l)
		}
	}

	// The two blocks, as the section writes them.
	lookBlock := chk68Block(t, section, "CHK68_LOOK=$( {")
	restoreBlock := bashBlocks(docSection(t, path, section, "### 7. Restore"))[0]
	restoreStart, restoreStop := -1, -1
	for i, l := range restoreBlock {
		s := strings.TrimSpace(l)
		if restoreStart < 0 && strings.HasPrefix(s, "CHK68_LEFT=$(") {
			restoreStart = i
		}
		if restoreStart >= 0 && strings.Contains(l, "Check 68 stop: these are still in") {
			restoreStop = i
		}
		if restoreStart >= 0 && strings.Contains(l, "no leftover of this check's own in the six listings") {
			restoreStop = i
			break
		}
	}
	if restoreStart < 0 || restoreStop < restoreStart {
		t.Fatal("Check 68's Restore no longer re-runs the filtered look over the six listings: the check's guarantee that it leaves nothing behind is checked there, not assumed")
	}
	restoreLook := strings.Join(restoreBlock[restoreStart:restoreStop+1], "\n")

	// A stubbed gcloud, because what is run here is the section's filtering and
	// not gcloud: each of the six listings prints its variable, and any other
	// call is reported so a listing added to the block cannot pass unnoticed.
	const stub = `
gcloud() {
  case "$*" in
    "secrets list"*)               printf '%s\n' "$STUB_SECRETS" ;;
    "artifacts repositories list"*) printf '%s\n' "$STUB_REPOSITORIES" ;;
    "run jobs list"*)              printf '%s\n' "$STUB_JOBS" ;;
    "scheduler jobs list"*)        printf '%s\n' "$STUB_SCHEDULER" ;;
    "services api-keys list"*)     printf '%s\n' "$STUB_KEYS" ;;
    "iam service-accounts list"*)  printf '%s\n' "$STUB_ACCOUNTS" ;;
    *) echo "stub gcloud: unexpected call: $*" >&2; return 1 ;;
  esac
}
`
	listings := func(secret, repo, job, sched, key, account string) string {
		return "STUB_SECRETS='" + secret + "'\nSTUB_REPOSITORIES='" + repo + "'\nSTUB_JOBS='" + job +
			"'\nSTUB_SCHEDULER='" + sched + "'\nSTUB_KEYS='" + key + "'\nSTUB_ACCOUNTS='" + account + "'\n"
	}
	clean := listings(
		ownLines[0], ownLines[1], ownLines[2], ownLines[3], ownLines[4], ownLines[5]+"\n"+ownLines[6])
	// One leftover per listing that prints a path, and one service account:
	// every shape of line the filter has to see through.
	dirty := listings(
		ownLines[0]+"\n"+leftovers[0]+"\n"+leftovers[1],
		ownLines[1]+"\n"+leftovers[2],
		ownLines[2]+"\n"+leftovers[3],
		ownLines[3]+"\n"+leftovers[4],
		ownLines[4]+"\n"+leftovers[5],
		ownLines[5]+"\n"+ownLines[6]+"\n"+leftovers[6]+"\n"+leftovers[7])

	prefix := "set -u\n" + owned + "\nP=fugaro-dev\nL=us-east5\nSREGION=us-east4\n" + stub
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH: the filter's own ERE is checked above")
	}

	// Step 0's look: a leftover is printed, and a clean project says so.
	for _, tc := range []struct {
		name     string
		listings string
		left     bool
	}{
		{"step 0, a clean project", clean, false},
		{"step 0, a leftover of the check's own", dirty, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := prefix + tc.listings + strings.Join(lookBlock, "\n") + "\n"
			code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
			if code != 0 {
				t.Fatalf("Check 68's step-0 look exited %d:\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
			if strings.Contains(errOut, "stub gcloud") {
				t.Fatalf("Check 68's step-0 look calls gcloud in a way this stub does not know: %s", errOut)
			}
			if !tc.left {
				if !strings.Contains(out, "no leftover of this check's own in the six listings") {
					t.Errorf("Check 68's step-0 look did not say the project is clean:\n%s", out)
				}
				return
			}
			if strings.Contains(out, "no leftover of this check's own in the six listings") {
				t.Errorf("Check 68's step-0 look reported no leftover while the listings carried eight of the check's own names:\n%s", out)
			}
			for _, l := range leftovers {
				if !strings.Contains(out, l) {
					t.Errorf("Check 68's step-0 look did not print %q, which a listing carries: the filter misses the name inside the line", l)
				}
			}
		})
	}

	// The Restore's look: the same filter, and this time a leftover is a stop.
	for _, tc := range []struct {
		name     string
		listings string
		left     bool
	}{
		{"the Restore, a clean project", clean, false},
		{"the Restore, a leftover of the check's own", dirty, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := prefix + tc.listings + restoreLook + "\necho CHK68-ASSERTED\n"
			code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
			if strings.Contains(errOut, "stub gcloud") {
				t.Fatalf("Check 68's Restore look calls gcloud in a way this stub does not know: %s", errOut)
			}
			if !tc.left {
				if code != 0 {
					t.Fatalf("Check 68's Restore stopped on a project with nothing of the check's own left in it (exit %d):\nstderr:\n%s", code, errOut)
				}
				if !strings.Contains(out, "no leftover of this check's own in the six listings") {
					t.Errorf("Check 68's Restore did not say the project is clean:\n%s", out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("Check 68's Restore passed while the listings still carried the check's own resources:\n%s", out)
			}
			if !strings.Contains(errOut, "Check 68 stop: these are still in fugaro-dev after the restore:") {
				t.Errorf("Check 68's Restore stopped without naming what is left:\nstderr:\n%s", errOut)
			}
			for _, l := range leftovers {
				if !strings.Contains(errOut, l) {
					t.Errorf("Check 68's Restore stop does not name %q:\n%s", l, errOut)
				}
			}
		})
	}
}

// TestChecklistCheck68DeletesOneResourcePerCommand: the trap and the Restore
// deleted the check's throwaway resources several to a line —
// `gcloud run jobs delete fmgd-chk68-job xfmgd-chk68-job` and the like — and
// every one of those subcommands takes a single positional name, so gcloud
// refused the extra positional and deleted NOTHING on that line. Only
// `gcloud storage rm -r` takes several URLs. With the trap also sending its
// errors to /dev/null, a check that stopped halfway looked as though it had
// cleaned up while about eight of its sixteen resources were still in the
// project, which is the opposite of what the section promises.
//
// So each delete is held to one positional name, the trap is held to
// `chk68_try` (which prints a failure instead of hiding it), and the trap and
// the Restore are held to the same set of names: a resource one of them deletes
// and the other does not is one a stopped check leaves behind.
//
// Mutations (verified, run and restored): two names put back on one
// `gcloud run jobs delete` line → FAIL; a name dropped from the trap → FAIL; a
// name dropped from the Restore → FAIL; `2>/dev/null` put on a delete → FAIL; a
// delete taken out of `chk68_try` → FAIL.
func TestChecklistCheck68DeletesOneResourcePerCommand(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	guard := chk68GuardBlock(t)
	cleanup := chk68FuncBody(t, guard, "cleanup_chk68")

	if !strings.Contains(guard, `chk68_try() { "$@" || echo "Check 68 cleanup:`) {
		t.Error("Check 68's guard block no longer declares chk68_try, which is what makes a delete that failed say so: a trap whose failures are hidden is a trap that reports a clean exit over the check's own resources")
	}
	for _, line := range strings.Split(cleanup, "\n") {
		cmd := strings.TrimSpace(line)
		if strings.HasPrefix(cmd, "#") || !strings.Contains(cmd, "gcloud ") {
			continue
		}
		if !strings.HasPrefix(cmd, "chk68_try ") {
			t.Errorf("Check 68's cleanup_chk68 runs %q without chk68_try: a delete that failed there is silent, and the trap's own line then says the resources are gone", cmd)
		}
	}

	trapNames := chk68DeleteNames(t, strings.Split(cleanup, "\n"), "Check 68's cleanup_chk68 trap")
	restoreNames := chk68DeleteNames(t, bashBlocks(docSection(t, path, section, "### 7. Restore"))[0], "Check 68's Restore")

	// The check creates sixteen throwaway resources (step 4's seven, step 5's
	// three near misses and its six negative probes), and the two cleanups are
	// what make the section's "leaves nothing behind" true.
	if len(trapNames) != 16 {
		t.Errorf("Check 68's cleanup_chk68 trap deletes %d distinct resources, and the check creates sixteen: %v", len(trapNames), trapNames)
	}
	if len(restoreNames) != 16 {
		t.Errorf("Check 68's Restore deletes %d distinct resources, and the check creates sixteen: %v", len(restoreNames), restoreNames)
	}
	for n := range trapNames {
		if _, ok := restoreNames[n]; !ok {
			t.Errorf("Check 68's Restore does not delete %q, which the trap does: the Restore is what runs when the trap has not (a step that stopped the check before the trap was armed), so the two lists have to be the same one", n)
		}
	}
	for n := range restoreNames {
		if _, ok := trapNames[n]; !ok {
			t.Errorf("Check 68's cleanup_chk68 trap does not delete %q, which the Restore does: a probe that fails leaves that resource behind until the owner runs the Restore, which is the case the trap exists for", n)
		}
	}
}

// chk68DeleteNames parses every gcloud delete in block and returns the
// positional resource names it carries, failing the test for the one thing that
// makes such a line delete nothing: more than one positional name on a
// subcommand that takes a single one.
func chk68DeleteNames(t *testing.T, block []string, label string) map[string]int {
	t.Helper()
	names := map[string]int{}
	commands := 0
	for _, line := range block {
		cmd := strings.TrimSpace(line)
		cmd = strings.TrimPrefix(cmd, "chk68_try ")
		if !strings.HasPrefix(cmd, "gcloud ") {
			continue
		}
		if !strings.Contains(cmd, " delete ") && !strings.Contains(cmd, " rm ") {
			continue
		}
		commands++
		if strings.Contains(cmd, "2>/dev/null") {
			t.Errorf("%s runs %q with its error output thrown away: a delete that failed would look exactly like one that worked, which is how a stopped check leaves most of its resources behind while the cleanup reads as done", label, cmd)
		}
		if !strings.Contains(cmd, "--project=") {
			t.Errorf("%s runs %q without naming the project: every gcloud command in this check carries --project", label, cmd)
		}
		toks := strings.Fields(cmd)
		verb := -1
		for i, tk := range toks {
			if tk == "delete" || tk == "rm" {
				verb = i
				break
			}
		}
		if verb < 0 {
			t.Errorf("%s runs %q, which is neither a delete nor a `storage rm`", label, cmd)
			continue
		}
		var positionals []string
		for _, tk := range toks[verb+1:] {
			if strings.HasPrefix(tk, "-") {
				continue
			}
			positionals = append(positionals, strings.Trim(tk, `"'`))
		}
		// `gcloud storage rm -r` is the one delete that takes several URLs.
		if strings.Contains(cmd, "gcloud storage rm ") {
			if len(positionals) == 0 {
				t.Errorf("%s runs %q with no URL to delete", label, cmd)
			}
			for _, n := range positionals {
				names[strings.TrimPrefix(n, "gs://")]++
			}
			continue
		}
		if len(positionals) != 1 {
			t.Errorf("%s runs %q with %d positional resource names: every gcloud delete but `gcloud storage rm -r` takes exactly ONE, so a line carrying several deletes none of them — gcloud refuses the extra positional — and a check that stopped halfway leaves most of its sixteen resources behind while the cleanup looks as though it ran", label, cmd, len(positionals))
			continue
		}
		names[positionals[0]]++
	}
	if commands == 0 {
		t.Fatalf("%s has no gcloud delete at all: the check's resources are deleted there", label)
	}
	return names
}

// TestChecklistCheck68SchedulerRegionComesFromTheConfig: every Cloud Scheduler
// command in Check 68 took its `--location` from the config's `region`, and
// Cloud Scheduler is not offered in every region: `gcp.SchedulerRegion` maps
// this installation's `us-east5` to `us-east4`
// (internal/backend/gcp/names.go:282), the installation's own jobs run in the
// recorded `scheduler_region` (internal/localcfg, written by `fugaro init` from
// the region unless `--scheduler-region` overrides it), and
// `gcloud scheduler jobs … --location=us-east5` fails on the pinned
// installation. Every one of those commands would have failed — the listing,
// both probes and both cleanups — and the
// `cloudscheduler.googleapis.com/Job` condition would never have been measured:
// the one service the check would have skipped in silence, with the check
// reporting a pass.
//
// Mutations (verified, run and restored): the Scheduler commands put back on
// `${L:?}` → FAIL; the guard's `SREGION` read dropped → FAIL; the Scheduler
// region's stop dropped → the guard test's "config carries no scheduler region"
// case passes; the prose that says Scheduler is not offered everywhere dropped
// → FAIL.
func TestChecklistCheck68SchedulerRegionComesFromTheConfig(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	guard := chk68GuardBlock(t)

	if !strings.Contains(guard, `SREGION=$(sed -n 's/^scheduler_region: *//p' "$C")`) {
		t.Error("Check 68's guard block does not read the Scheduler region out of the installation's config: the region is not the Scheduler location, and the two differ on this installation")
	}
	if !strings.Contains(guard, "no scheduler_region") {
		t.Error("Check 68's guard block does not stop when the config carries no Scheduler region: every Scheduler command below would then run against a location that is not one, and the scheduler job's condition would go unmeasured")
	}
	// Every Scheduler command in the section. The guard's own comment names the
	// wrong form (`--location=\"${L:?}\"`) as the thing this rule exists for, so
	// comments are not commands and are skipped.
	scheduler := 0
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || !strings.Contains(s, "gcloud scheduler ") {
			continue
		}
		scheduler++
		if !strings.Contains(s, `--location="${SREGION:?}"`) {
			t.Errorf("Check 68 runs %q: every Scheduler command needs the Scheduler LOCATION, which is the config's `scheduler_region` and not its `region` — this installation's us-east5 gives us-east4, so a Scheduler command given the region fails and its condition is never measured", s)
		}
		if strings.Contains(s, `--location="${L:?}"`) {
			t.Errorf("Check 68 runs %q with the region where the Scheduler location belongs: Cloud Scheduler is not offered in every region, and this installation is one of the pairs that proves it", s)
		}
	}
	if scheduler < 9 {
		t.Errorf("Check 68 has %d Scheduler commands, want the listing, the positive probe, the negative probe, the near-miss job and both cleanups: a command that went missing takes its service's measurement with it", scheduler)
	}
	// And why the region is not the answer, in the section's own words: the
	// reader who finds a Scheduler command failing has to be able to see that the
	// region was never its location.
	for _, want := range []string{
		"Cloud Scheduler is NOT offered in every region",
		"gcp.SchedulerRegion",
		"us-east5",
		"us-east4",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not say %q: the Scheduler region is a value of its own, and a reader who does not know that types the region", want)
		}
	}
}
