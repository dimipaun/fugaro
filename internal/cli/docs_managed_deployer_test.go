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
// safe to run: whose check it is, the twelve hard stops in the guard block (the
// pin to installation `fugaro` / project `fugaro-dev`, the belong and
// production refusals comparing PROJECT IDS and the installation's name — never
// a `fugaro doctor` line read out of the checkout — the owner-typed variable,
// the `--step` argument's own refusal, the project's number and the random
// suffix the bucket deletes need, and the config's own project, region,
// Scheduler region and runs bucket), the
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

	// 2. The guard block: the first block, with the twelve hard stops, the
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
		// records it — `fugaro update` writes it from gcp.SchedulerRegion — and
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
		`/fugaro/terraform/${P:?}/installation`, // keyed by the GCP project ID (infra.InstallationWorkdir), not the installation name
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
		"--key-id=\"${KEY_MAIN:?}\"",
		"--key-id=\"${KEY_NEG:?}\"",
		`KEY_MAIN="fmgd-chk68-key-${CHK68_RAND}"`,
		`KEY_NEG="chk68-neg-key-${CHK68_RAND}"`,
		`[ -e "$CHK68_TMP/tfvars.before.json" ]`,
		`APPLY_RC=${PIPESTATUS[0]}`,
		"Check 68 binding stop",
		"Check 68 policy stop",
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
		!strings.Contains(section, "--key-id=\"${KEY_MAIN:?}\"") {
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

// TestChecklistCheck68GuardRefusesProdPerValue: the guard's production rule
// reads THREE values — the installation `$I` the owner types, the project `$P`
// the owner types, and the project the installation's config names — and the
// three are three different places. Written as one `||` chain, the term for a
// single one of the three can be dropped and the stop still fires from another,
// which is a mutation that survives: `! prod "$P"` did, because the only case
// that carries a production id set `$P` AND the config's `gcp_project` to
// `fugaro-prod`, so the config's term refused it with the same message. The
// rule is a loop that names every refused value. This test therefore takes each
// of the three ALONE: exactly one of them is a production id, the other two are
// the pinned pair, and the guard has to stop with the belong/production refusal
// — not with the pin, which a production `$P` also trips — and to name the
// value it refused.
//
// Mutations (verified, run and restored): `prod "$CHK68_ID"` dropped from the
// loop body → all three cases fail, and the two that carry a pinned `$P` name
// `fugaro-prod` nowhere in the output; the loop narrowed to `"$I" "$P"` → the
// config case fails the same way; the loop narrowed to `"$P" "$CFG_PROJECT"` →
// the installation case fails; the refusal's message printing only `$I` and `$P`
// → every case fails on the value it refused, which is the point of naming it.
func TestChecklistCheck68GuardRefusesProdPerValue(t *testing.T) {
	guard := chk68GuardBlock(t)

	cases := []struct {
		name          string
		installation  string // CHK68_INSTALLATION
		project       string // CHK68_PROJECT
		configProject string // the config's own `gcp_project:`
		bad           string // the one value of the three that is a production id
	}{
		{"only the installation is a production id", "fugaro-prod", "fugaro-dev", "fugaro-dev", "fugaro-prod"},
		{"only the typed project is a production id", "fugaro", "fugaro-prod", "fugaro-dev", "fugaro-prod"},
		{"only the config's project is a production id", "fugaro", "fugaro-dev", "fugaro-prod", "fugaro-prod"},
		{"another production id (substring rule)", "fugaro", "acme-prod-1", "fugaro-dev", "acme-prod-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeChk68Config(t, home, tc.installation, tc.configProject, "us-east5", "us-east4", "sandbox-runs")
			code, out := runChk68Guard(t, guard, []string{
				"HOME=" + home,
				"PATH=" + os.Getenv("PATH"),
				"CHK68_OWNER_RUN=yes",
				"CHK68_PROJECT=" + tc.project,
				"CHK68_INSTALLATION=" + tc.installation,
			})
			if code == 0 {
				t.Fatalf("the guard exited 0 with %q a production id (I=%q P=%q config=%q): no step of this check may write in a production project, and this one opens a live IAM policy and creates resources there", tc.bad, tc.installation, tc.project, tc.configProject)
			}
			if stops := strings.Count(out, "Check 68 stopped:"); stops != 1 {
				t.Errorf("the guard printed %d hard stops with %q a production id (I=%q P=%q config=%q), want exactly the production one\n%s", stops, tc.bad, tc.installation, tc.project, tc.configProject, out)
			}
			if !strings.Contains(out, tc.bad+" is the belong installation") {
				t.Errorf("the guard refused I=%q P=%q config=%q without naming %q as the value it refused: the three come from three different places, so a message that names another one of them sends its reader to fix the wrong variable\n%s", tc.installation, tc.project, tc.configProject, tc.bad, out)
			}
		})
	}
}

// TestChecklistCheck68ApiKeyCreatesHideTheKeyString: `gcloud services api-keys
// create` prints the whole key object by default, keyString included, and that
// string is a live credential for every API that accepts an API key — so both of
// Check 68's creates sent a credential into the FACT, into the terminal's
// scrollback and into whatever the owner pasted back, and the section's own
// "Paste back" line forbids exactly that. A key created with no target list is
// unrestricted besides. Neither create is used against any API by any step of
// the check (steps 4 and 5 create the key and update its display name, and both
// commands that act on it name it by the `--key-id` the create chose), so
// nothing is lost by printing nothing.
//
// Mutations (verified, run and restored): `--format=none` dropped from a create
// → that create fails here; `--api-target=service=` dropped from a create →
// FAIL; the output captured instead of suppressed (`--format='value(name)'`) →
// FAIL, because a `value(...)` format on this command prints the field asked for
// and this test's rule is that nothing but the exit status is printed; the
// `--key-id` dropped → FAIL, because without it the create's own output is the
// only way to learn the name and the suppression would then be hiding something
// the check needs.
func TestChecklistCheck68ApiKeyCreatesHideTheKeyString(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")

	// The creates are the ones that write a key, wherever the section keeps
	// them: step 4's positive probe and step 5's negative one.
	creates := 0
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "gcloud services api-keys create") {
			continue
		}
		creates++
		if !strings.Contains(s, "--format=none") {
			t.Errorf("%s's Check 68 runs %q without `--format=none`: this command's default output is the key object together with its keyString, which is a live credential, and it would end up in the FACT, in the scrollback and in whatever the owner pastes back — which the section's own Paste back line forbids", path, s)
		}
		if !strings.Contains(s, "--api-target=service=") {
			t.Errorf("%s's Check 68 runs %q with no `--api-target`: an API key with no target list is unrestricted, a credential for every API that accepts one, and this check needs no API of it at all — steps 4 and 5 create the key and update its display name", path, s)
		}
		if !strings.Contains(s, "--key-id=\"${KEY_") {
			t.Errorf("%s's Check 68 runs %q without `--key-id`: the fence keys on the key's NAME, and every command that acts on the key names it by the ID this create chose — with a server-generated uid neither the check's positive action nor its cleanup can name the key", path, s)
		}
		// The three flags are on the create's own line, which is why one line is
		// read at a time here: a create whose flags were moved to a continuation
		// line is a create this test would read as missing every one of them.
		if strings.HasSuffix(s, "\\") {
			t.Errorf("%s's Check 68 splits %q over more than one line: the three flags it is held to are read off one line here, and a command whose own line carries none of them is a command whose default output and unrestricted target list nothing in this file would catch", path, s)
		}
	}
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "gcloud services api-keys update") && !strings.Contains(s, "--format=none") {
			t.Errorf("%s's Check 68 runs %q without `--format=none`", path, s)
		}
	}
	if creates != 2 {
		t.Errorf("%s's Check 68 has %d `gcloud services api-keys create` commands, want the two the positive and the negative direction each need: one per service, and the API key is one of the seven", path, creates)
	}
	// And no COMMAND in the section asks for the string. The word is in the
	// prose beside both creates, which is where the reason it is never printed
	// belongs; what is refused here is a command that reads it — a `--format`
	// naming it, or a capture of it into a variable.
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "#") || !strings.Contains(s, "keyString") {
			continue
		}
		t.Errorf("%s's Check 68 runs %q, which reads the key's own string: it is a live credential, nothing in this check uses the key against an API, and the section's Paste back line forbids it in the FACT", path, s)
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
// the production case passes (the config's term, which is the one this case
// carries — a term of the rule alone is
// TestChecklistCheck68GuardRefusesProdPerValue's); the config's project compared to nothing → the
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
		// ONE of the three values is production here, and it is the config's:
		// while `$P` was production too, this case could not tell the two terms
		// apart, and dropping `prod "$P"` from the body survived it.
		// TestChecklistCheck68GuardRefusesProdPerValue takes each term alone.
		{"the config's project is a production project", "yes", "fugaro", "fugaro-dev", "fugaro-prod", "us-east5", "us-east4", "sandbox-runs",
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
// region, and the region is not its location) makes it nine. The 2026-10-21
// review added two more, both in the guard: the project's number, which the
// bucket deletes compare a bucket's own project against (a bucket name is
// global and `gcloud storage rm` ignores --project), and the random suffix the
// check's two throwaway buckets carry, which has to be drawn rather than left
// empty — so the number was eleven. The 2026-10-21 review's second round made
// the check ONE script with `--step N`, and the argument's own refusal (a step
// that is not 1..7, or an unknown argument) is a twelfth stop in the guard block
// — so the number is twelve.
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
		"eleven": 11, "twelve": 12,
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
	// 2. The guard's own comment, which is what a reader launching the script
	// is told to expect.
	m = regexp.MustCompile(`nothing below the (\w+) hard stops runs`).FindStringSubmatch(section)
	if m == nil {
		t.Fatal("Check 68's guard comment does not say how many hard stops run before the check's steps: the guard is the first block of the script, and the count is what tells its reader how many refusals stand between a mistyped project and the first live write")
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
	if stops < 12 {
		t.Errorf("Check 68's guard carries %d `Check 68 stopped:` refusals, and this check's belong stop, its pin to the sandbox installation, the `--step` argument's own refusal, the project number its bucket deletes compare against and the random suffix its two buckets carry are among them: the guard is what keeps a live IAM check off an installation it must never write in", stops)
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

// TestManagedDeployerStepFourDenialIsPrecise: the register and the design of
// record both said a step-4 denial — a positive action the deployer was refused
// — stops Check 68 for any of the five `resource.name` services, and the design
// said it again in §5 after the register had been made precise. §8 point 3's
// ruling of 2026-10-20 draws the line, and the two texts have to draw it the
// same way: IAM service accounts and Cloud Run jobs are the feature's own use,
// so a denial there is the §5 option-2 fallback, while API keys, Cloud Scheduler
// jobs, Artifact Registry repositories (and Secret Manager secrets, for its own
// reason) are the acceptable drops — the service's type and its permissions go
// with its role (R5). A reader who takes either text as "any denial stops the
// check" drops a service that was survivable; a reader who takes it as "a denial
// is a drop" ships a feature that cannot create the accounts and jobs a managed
// plan is made of.
//
// Mutations (verified, run and restored): the design's "a step-4 denial for one
// of those five services, is STOP and redesign" and the register's "a step-4
// denial for one of those five services — STOP and redesign" put back, one at a
// time — each fails here, naming its file.
func TestManagedDeployerStepFourDenialIsPrecise(t *testing.T) {
	for _, path := range []string{
		"docs/design/managed-deployer-fence.md",
		"docs/gcp-live-checklist.md",
	} {
		doc := flattenDoc(readDoc(t, path))
		for _, want := range []string{
			"IAM service accounts and Cloud Run jobs",
			"acceptable drops",
			"point 3",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s does not say %q: §8 point 3's ruling of 2026-10-20 is the line a step-4 denial is read against — a denial for IAM service accounts or Cloud Run jobs is the fallback, and the other services are the drops", path, want)
			}
		}
		if strings.Contains(doc, "step-4 denial for one of those five services") {
			t.Errorf("%s still says a step-4 denial for any of the five `resource.name` services stops the check: the ruling is service by service, and that sentence is what a reader takes as the rule", path)
		}
	}
}

// TestChecklistCheck68MethodRunsAsAScript: Check 68's guard stops with `exit 1`
// and its Restore's stop is a `return` from the function it lives in, and both
// mean what they are written to mean only when the block is run as a script. The
// Method said "in one shell you keep for the whole check, in `bash`" and left
// which shell to the reader: sourced into the shell the owner is working in, the
// first stop closes that shell and takes its message with it, and the Restore's
// stop — the one that fires with the gate still OPEN — loses the message that is
// the only thing saying so. The sentence also carries what running it as a
// script costs: the three variables the guard reads come from the environment,
// so they have to be exported, or the script never sees them.
//
// Mutation (verified, run and restored): the sentence dropped — this fails.
func TestChecklistCheck68MethodRunsAsAScript(t *testing.T) {
	doc := flattenDoc(readDoc(t, "docs/gcp-live-checklist.md"))
	const lead = "**Method.** All of it is yours, in one shell you keep for the whole check, in `bash`"
	i := strings.Index(doc, lead)
	if i < 0 {
		t.Fatalf("Check 68's Method no longer opens with %q; update this test's marker", lead)
	}
	method := doc[i:]
	if end := strings.Index(method, "```bash"); end >= 0 {
		method = method[:end]
	}
	for _, want := range []string{
		"run it as `bash chk68.sh`, never `. chk68.sh` or `source chk68.sh`",
		"export CHK68_OWNER_RUN=yes CHK68_INSTALLATION=fugaro CHK68_PROJECT=fugaro-dev",
	} {
		if !strings.Contains(method, want) {
			t.Errorf("Check 68's Method does not say %q: the guard's stops are `exit 1`, so the block is run as a script and never sourced, and the three variables the guard reads have to be exported to reach that script", want)
		}
	}
}

// TestChecklistCheck68FailsWhenScopesTheDropsToStepFour: Check 68's "Fails when"
// paragraph applies the fix per failure, and it applied the step-4 rule to the
// wrong branch — a condition IAM refuses to store (step 2 or 3) was "the drop
// ... open only for the services §8 point 3 rules acceptable". The paragraph
// above it and the design's §5 both draw the line the other way: a refused
// condition is a STOP and the option-2 fallback whatever service it belongs to,
// and §8 point 3's drops are what a *step-4* denial gets, for the services it
// names. An owner reading the old sentence on the check's most important branch
// would drop a service the ruling says is a fallback, which is the difference
// between a smaller feature and a broken apply.
//
// Mutation (verified, run and restored): the old clause "and the drop is open
// only for the services §8 point 3 rules acceptable" put back — this fails.
func TestChecklistCheck68FailsWhenScopesTheDropsToStepFour(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := flattenDoc(readDoc(t, path))
	const lead = "**Fails when:** any of those is not what you saw."
	i := strings.Index(doc, lead)
	if i < 0 {
		t.Fatalf("%s's Check 68 no longer has its %q paragraph; update this test's marker", path, lead)
	}
	para := doc[i:]
	if end := strings.Index(para, "**Paste back:**"); end >= 0 {
		para = para[:end]
	}
	for _, want := range []string{
		"the fallback whatever service it belongs to, never a drop",
		"whether that is the fallback or one of §8 point 3's acceptable drops is the step-4 question, not this one",
		"§8 point 3 rules acceptable for API keys, Cloud Scheduler jobs, Artifact Registry repositories and Secret Manager secrets only",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("%s's Check 68 \"Fails when\" paragraph does not say %q: a condition IAM refuses to store is the fallback for every service, and the drops §8 point 3 rules acceptable are what a step-4 denial gets — the paragraph above it and the design's §5 draw that line, so this one has to draw it the same way", path, want)
		}
	}
	if strings.Contains(para, "the drop is open only for the services §8 point 3 rules acceptable") {
		t.Errorf("%s's Check 68 \"Fails when\" paragraph still applies the step-4 drop rule to a refused condition: steps 2 and 3 are the fallback whatever service they belong to, and an owner reading this on the check's main branch drops a service the ruling says is a fallback", path)
	}
}

// TestChecklistCheck68IndexRowKeepsC3AndSecretManagerApart: the index row for
// Check 68 folded Secret Manager into the set C3 covers — "for a Secret, an IAM
// service account, ... (C3, UNVERIFIED in the design)" — while the design's C3
// is the five services the reference does not list `resource.name` for, and the
// section's own item 3 says Secret Manager is "a sixth service, and a different
// fact about it" (the `extract` form, C6/C7). The row is the index a reader
// scans, so a row that puts a sixth service under C3 misstates what the check
// measures and what a C3 failure would drop.
//
// Mutations (verified, run and restored): "for a Secret, an IAM service account"
// put back in the row — this fails; and the row's "a **sixth** service measured
// for a different fact" dropped — this fails.
func TestChecklistCheck68IndexRowKeepsC3AndSecretManagerApart(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	var row string
	for _, line := range strings.Split(readDoc(t, path), "\n") {
		if strings.HasPrefix(line, "| 68 | ") {
			row = flattenDoc(line)
			break
		}
	}
	if row == "" {
		t.Fatalf("%s no longer carries an index row for Check 68; update this test's marker", path)
	}
	for _, want := range []string{
		"for an IAM service account, an API key, an Artifact Registry repository, a Cloud Run job and a Cloud Scheduler job (C3, UNVERIFIED in the design, and it covers those **five**)",
		"a **sixth** service measured for a different fact",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's Check 68 index row does not say %q: C3 is the five services the reference does not list `resource.name` for, and Secret Manager is the sixth the check measures for a different fact (the `extract` form, C6/C7)", path, want)
		}
	}
	if strings.Contains(row, "for a Secret, an IAM service account") {
		t.Errorf("%s's Check 68 index row still puts Secret Manager under C3: the design's C3 covers five services, and the section's item 3 says Secret Manager is a sixth service measured for a different fact", path)
	}
}

// TestManagedDeployerCitationsNameThePlaceNotTheLine: the redesign's documents
// point a reader at places in `deployer.tf`, `cover.go` and the design, and the
// convention this repository records (docs/plans/2026-10-19-compute-backend.md,
// the release fact: "a citation by line is a second fact that an edit anywhere
// above it falsifies, while the anchor a reader finds the place by does not
// move") is to cite them by the anchor rather than by a line number. The
// design's R5 pointed at `deployer.tf:120-123` for a paragraph at 125-128, C10
// at `:334` for the note at 339, the account clause at `:348` and `:152` for
// the local at 156, and C5 at `:69-107` for a comment running 73-116; the
// register pointed at `managed-deployer-fence.md:145` for §3's C7 row (183) and
// `:248` for Option 2's heading (287); and the managed-projects plan at
// `cover.go:434` for `accountClause` (1284 here, 1089 on the base). Every one of
// them now names the place, and this test holds both halves of that: the anchor
// is in the document that cites it, and the place it names is in the file it
// points at.
//
// Mutations (verified, run and restored): a `deployer.tf:<line>` citation put
// back in the design → FAIL; the "GATED OFF since 0.8.6" heading renamed in
// `deployer.tf` → FAIL; the §3 `| C7 |` row renamed in the design → FAIL; the
// register's "### Option 2" heading renamed → FAIL.
func TestManagedDeployerCitationsNameThePlaceNotTheLine(t *testing.T) {
	cites := []struct {
		doc, file, anchor string
	}{
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "GATED OFF since 0.8.6"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "ONE BINDING PER SERVICE"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "Two halves and not one role bound twice"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "The deployer account of D75"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "The conditions, one per service"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "What none of them holds, and why"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "deployer_condition_account"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "Binding (1)"},
		{"docs/design/managed-deployer-fence.md", "deploy/terraform/gcp/modules/installation/deployer.tf", "fugaro_deployer_scheduler_job"},
		{"docs/gcp-live-checklist.md", "docs/design/managed-deployer-fence.md", "### Option 2 — fence by project, not by name"},
		{"docs/gcp-live-checklist.md", "docs/design/managed-deployer-fence.md", "| C7 |"},
		// The two tests C7's paragraph rests on, cited by name: the line numbers
		// it used to carry (1491 and 1649) had each drifted 176 lines by the
		// time this branch's file growth was merged, and the names had not.
		{"docs/gcp-live-checklist.md", "internal/infra/tf/cover_test.go", "TestExtractSegmentModelsTheRecordedReading"},
		{"docs/gcp-live-checklist.md", "internal/infra/tf/cover_test.go", "TestDeployerConditionFencesOnlyTheManagedPrefix"},
		{"docs/plans/2026-10-16-managed-projects.md", "internal/infra/tf/cover.go", "accountClause"},
	}
	docs := map[string]string{}
	for _, c := range cites {
		if _, ok := docs[c.doc]; !ok {
			docs[c.doc] = flattenDoc(readDoc(t, c.doc))
		}
		if !strings.Contains(docs[c.doc], c.anchor) {
			t.Errorf("%s does not name the anchor %q: a citation has to be the place a reader finds it by, and the anchor is what survives an edit above it", c.doc, c.anchor)
		}
		if !strings.Contains(readDoc(t, c.file), c.anchor) {
			t.Errorf("%s no longer carries %q, the anchor %s cites: the anchor and the citation move together", c.file, c.anchor, c.doc)
		}
	}
	// And the line-number form those citations replaced is gone from the two
	// documents this branch rewrote, not merely joined by an anchor. The
	// checklist's `cover_test.go:` citations are the same rule applied to a
	// file whose paths are not `deployer.tf`: the two above had drifted 176
	// lines and nothing in the tree noticed, because the ban was written for
	// the file this branch rewrote and not for the tests it cites.
	for _, wrong := range []struct{ doc, citation string }{
		{"docs/design/managed-deployer-fence.md", "deployer.tf:"},
		{"docs/gcp-live-checklist.md", "managed-deployer-fence.md:"},
		{"docs/gcp-live-checklist.md", "cover_test.go:"},
	} {
		if m := regexp.MustCompile(regexp.QuoteMeta(wrong.citation) + `\d`).FindString(readDoc(t, wrong.doc)); m != "" {
			t.Errorf("%s still cites by line (%q): a line citation is a second fact that an edit anywhere above it falsifies, and the anchor is what a reader finds the place by", wrong.doc, m)
		}
	}
}

// TestManagedDeployerD75QuoteIsD75sWords: §7 quoted D75 as saying
// `fugaroDeployerScoped` is "bound exactly once under the name-prefix
// condition", and D75 does not say that — it says "bound only under the
// name-prefix condition" (docs/design/managed-projects.md §5), and "bound
// exactly once" is this redesign's own banner for the seven roles it writes. A
// quotation that attributes wording to a decision the owner answered is how a
// reader comes to believe D75 ruled something it did not.
//
// Mutations (verified, run and restored): the design's quote changed back to
// "bound exactly once under the name-prefix condition" → FAIL; D75's own words
// changed in docs/design/managed-projects.md → FAIL (this test's own marker).
func TestManagedDeployerD75QuoteIsD75sWords(t *testing.T) {
	const quote = "bound only under the name-prefix condition"
	d75 := flattenDoc(readDoc(t, "docs/design/managed-projects.md"))
	if !strings.Contains(d75, quote) {
		t.Fatalf("D75 no longer says %q; update this test's marker", quote)
	}
	design := flattenDoc(readDoc(t, "docs/design/managed-deployer-fence.md"))
	if !strings.Contains(design, quote) {
		t.Errorf("the design's §7 does not quote D75's own words (%q): the quotation has to be what D75 says, and \"bound exactly once\" is this redesign's banner for the seven roles it writes rather than D75's", quote)
	}
	if strings.Contains(design, "bound exactly once under the name-prefix condition") {
		t.Errorf("the design's §7 still attributes \"bound exactly once under the name-prefix condition\" to D75: D75 says \"bound only under the name-prefix condition\", and the words a quotation puts in a decision's mouth are the ones a reader takes as the ruling")
	}
}

// TestManagedDeployerFactsCountTheirServicesRight: three small facts in the
// design had drifted from the shape they describe. F3 said the one reach off the
// project "is named in F4", and it is F4a's bucket-level binding that names it —
// F4 is the prefix-is-the-only-key fact and never mentions the bucket. §4's "why
// not 1b" said "five of the seven services have a middle segment the module
// cannot know at plan time" and then named four, leaving API keys out, and §4's
// option-1b bullet put API keys among the services whose `locations/<location>`
// is a plan-time value the managed plan chooses, while an API key's location is
// the constant `global` (`internal/gcpfake/apikeys.go`'s `AddKey`). A reader
// counting the services, or looking for the bucket binding under F4, is led to
// the wrong place by each of them.
//
// Mutations (verified, run and restored): "named in F4, and F3 does not cover
// it" put back → FAIL; API keys dropped from the "why not 1b" sentence → FAIL;
// API keys put back into the option-1b location bullet → FAIL.
func TestManagedDeployerFactsCountTheirServicesRight(t *testing.T) {
	doc := flattenDoc(readDoc(t, "docs/design/managed-deployer-fence.md"))
	for _, want := range []string{
		"named in F4a, and F3 does not cover it",
		"five of the seven services take the anchor form rather than a whole-name one",
		"for API keys, the segment before `/keys/` (their location is the constant `global`",
		"- **API keys** — the location is not the unknown here",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/managed-deployer-fence.md does not say %q: F3's off-project reach is F4a's, and the five services the anchor form is for include API keys, whose location is the constant `global` and not a value the plan chooses", want)
		}
	}
	if strings.Contains(doc, "Cloud Scheduler jobs, API keys**") {
		t.Errorf("docs/design/managed-deployer-fence.md's option-1b bullet still groups API keys with the services whose location the managed plan chooses: an API key's location is the constant `global` (`internal/gcpfake/apikeys.go`'s `AddKey`), so the location is not what is unknown for it")
	}
}

// TestManagedProjectsTaskOneFiveRecordsCheck52U3AsRun: Task 1.5's condition
// paragraph said a Secret Manager name-prefix condition "is Check 52's U3 —
// which docs/gcp-live-checklist.md on main still records as NOT RUN (its row,
// "USER-RUN, NOT RUN"): its results are in the owner's records and not yet in
// the docs, so nothing on main is evidence for it". That was true when the task
// was written and is not now: the register's Check 52 index row and its section
// record U3 as RUN 2026-10-16 and honoured, and the design's own F5 table reads
// it as measured. The row the sentence quoted does not exist any more, so the
// one surviving copy of the claim has to go with the rest.
//
// Mutations (verified, run and restored): the old sentence put back → FAIL; the
// register's "U3 honoured" removed → FAIL (the two are held together).
func TestManagedProjectsTaskOneFiveRecordsCheck52U3AsRun(t *testing.T) {
	const path = "docs/plans/2026-10-16-managed-projects.md"
	plan := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		"Check 52's U3 — **which the checklist now records as RUN 2026-10-16",
		"so that half is evidence in the tree today",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s's Task 1.5 does not say %q: the register records Check 52's U3 as RUN 2026-10-16 and honoured, so the claim that nothing on main is evidence for it is stale", path, want)
		}
	}
	for _, wrong := range []string{
		"on `main` still records as NOT RUN",
		"so nothing on `main` is evidence for it",
	} {
		if strings.Contains(plan, wrong) {
			t.Errorf("%s's Task 1.5 still says %q: the register's Check 52 row and its section now record U3 as RUN 2026-10-16 (honoured), and the row the sentence quoted no longer exists", path, wrong)
		}
	}
	if checklist := flattenDoc(readDoc(t, "docs/gcp-live-checklist.md")); !strings.Contains(checklist, "U3 honoured") {
		t.Fatalf("docs/gcp-live-checklist.md no longer records Check 52's U3 as honoured; update this test's marker")
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
// in the guard block deletes the check's own resources when the RUN ends — the
// last step, a stop, or a Ctrl-C, and never between steps, because the check is
// one script whose steps act on each other's resources — so a failed probe
// leaves nothing behind, and it says that it does not close the gate. This holds the three statements of that invariant together: step 5 acts
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
		"chk68-neg-key", "chk68-neg-job", "chk68-neg-sched", "${KEY_MAIN:?}", "${KEY_NEG:?}",
	}
	negatives := []string{
		"chk68-neg-secret", "chk68-neg-sa", "chk68-neg-repo",
		"${KEY_NEG:?}", "chk68-neg-job", "chk68-neg-sched",
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
// gate opens, and on the restore a plan that destroys the gate's own addresses
// and nothing else — between one and fourteen of them, because step 2 may have
// stopped the opening apply after some, and a restore that demanded all
// fourteen refused there with the gate still open and no way forward.
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
		// A REPLACEMENT is planned as ["delete","create"], which none of the
		// three counts above sees, so the three of them together report 14/0/0
		// for a plan that destroys a live object outside the gate. The stop has
		// to carry the two filters that do see it: the address filter (nothing
		// outside the gate's fourteen may be touched) and the action filter
		// (nothing may be replaced or forgotten).
		`select(.change.actions != ["no-op"]) | .address`,
		`google_project_iam_(custom_role|member)\.fugaro_deployer_(storage|secret|account|apikey|repository|run_job|scheduler_job)\[`,
		`select(.change.actions == ["delete","create"] or .change.actions == ["create","delete"] or .change.actions == ["forget"])`,
		`[ "$ADD" = 14 ] && [ "$CHG" = 0 ] && [ "$DEL" = 0 ] && [ "$OTHER" = 0 ] && [ "$SWAP" = 0 ]`,
		"Check 68 plan stop:",
	} {
		if !strings.Contains(step2, want) {
			t.Errorf("Check 68's step 2 does not carry %q: opening the gate on a converged installation adds the gate's fourteen addresses and changes, destroys, replaces or forgets nothing, and a plan of any other shape is a reason to stop rather than apply", want)
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
		`select(.change.actions == ["delete","create"] or .change.actions == ["create","delete"] or .change.actions == ["forget"])`,
		`[ "$ADD" = 0 ] && [ "$CHG" = 0 ] && [ "$SWAP" = 0 ] && [ "$DEL" -ge 1 ] && [ "$DEL" -le 14 ] && [ "$OTHER" = 0 ]`,
		`google_project_iam_(custom_role|member)\.fugaro_deployer_(storage|secret|account|apikey|repository|run_job|scheduler_job)\[`,
		`chk68_stop "`,
	} {
		if !strings.Contains(restore, want) {
			t.Errorf("Check 68's Restore does not carry %q: closing the gate destroys the seven roles and the seven bindings and nothing else, and the plan is where that is checked", want)
		}
	}
	planned := strings.Index(restore, `terraform plan -input=false -out="$CHK68_TMP/restore.tfplan"`)
	restoreApply := strings.Index(restore, `terraform apply -input=false "$CHK68_TMP/restore.tfplan"`)
	restoreAsserted := strings.Index(restore, `chk68_stop "`)
	if planned < 0 || restoreApply < 0 {
		t.Fatal("Check 68's Restore no longer plans and applies that plan")
	}
	if restoreAsserted < 0 || restoreAsserted > restoreApply {
		t.Error("Check 68's Restore asserts the plan's shape after applying it")
	}
	// The stop here fires with the gate still OPEN, and it is the one stop in the
	// check whose message is the point: `exit 1` in the interactive shell the
	// owner is typing in closes the window and takes the message with it, so this
	// stop returns instead, and the `if chk68_restore` below is what stops the
	// commands that follow.
	for _, want := range []string{"chk68_stop() {", "return 1", "if chk68_restore; then", "THE GATE IS STILL OPEN"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q: the restore's stop fires with the gate open, so it prints and returns rather than exiting, and the `if` around the commands that follow it is what keeps them from running", want)
		}
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
// The section's stop does not end the shell (see the guard's `chk68_stop`): it
// returns from `chk68_restore()`, which is where the assertion lives. So the
// harness declares the section's own `chk68_stop` and runs the lifted lines
// inside a function of the same shape, and the function's exit status is what
// decides each case.
//
// Mutations (verified, run and restored): the filter anchored back at
// `^google_project_iam_…` → every module-prefixed case fails, in the ERE check
// and in the run; the `OTHER = 0` clause dropped from the stop → the
// extra-address cases pass; the stop's `return 1` turned into a no-op → the
// extra-address cases pass; the jq `select` on `["no-op"]` dropped → the no-op
// case fails; `set -e` added above the counts → the passing cases fail, because
// `grep -cv` exits 1 when it excludes nothing; `chk68_stop` replaced by a bare
// `echo` → the stop no longer returns non-zero and the extra-address cases pass.
func TestChecklistCheck68RestorePlanFilterIsRun(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	restore := docSection(t, path, section, "### 7. Restore")
	// The stop the assertion calls is the section's own: it is lifted from the
	// guard block rather than restated here. chk68FuncBody returns the body up to
	// the newline before the closing brace, so the brace is put back here.
	stopFn := chk68FuncBody(t, chk68GuardBlock(t), "chk68_stop") + "\n}"

	// The assertion, as the section writes it: from the first count to the line
	// carrying the stop.
	lines := strings.Split(restore, "\n")
	start, stop := -1, -1
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if start < 0 && strings.HasPrefix(s, "ADD=$(jq") {
			start = i
		}
		if start >= 0 && strings.Contains(l, `chk68_stop "`) {
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
	if !strings.Contains(assertion, "chk68_stop") {
		t.Fatal("Check 68's restore assertion stops with something other than the check's own chk68_stop")
	}
	if !strings.Contains(assertion, "return 1") {
		t.Fatal("Check 68's restore assertion does not refuse: the stop prints its message and carries on, which is not a stop")
	}
	// This is the one stop in the check whose message is the point — it fires
	// with the gate still open — and `exit 1` in the interactive shell the owner
	// is typing in closes the window and takes the message with it. So it returns
	// instead, and nothing on this path may exit the shell.
	if strings.Contains(assertion, "exit ") {
		t.Error("Check 68's restore assertion exits the shell: it fires with the gate still OPEN, so an `exit` closes the window the message is printed in and the owner never reads it. The stop returns from the function it lives in, and the `if` around the commands that follow is what stops them.")
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
	type change = chk68Change
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
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate, 0 to replace or forget"},
		{"the gate's own fourteen, unprefixed",
			append(destroys(unprefixed(gate)), own), true,
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate, 0 to replace or forget"},
		{"a no-op outside the gate",
			append(append(destroys(gate), own), change{"module.installation.google_storage_bucket.runs[0]", []string{"no-op"}}), true,
			"restore plan: 0 to add, 0 to change, 14 to destroy, 0 outside the gate, 0 to replace or forget"},
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
		// The partial set, and it is the case the restore used to refuse: step 2
		// stops on any plan shape it does not expect, so the opening apply can
		// have created six of the fourteen and been refused on the seventh, and
		// the plan that closes the gate destroys exactly those six. A restore
		// that demanded all fourteen stopped there, with the gate still open and
		// nothing the owner could do, which is the state the step exists to
		// avoid.
		{"a partial set of the gate's own, six of the fourteen",
			append(destroys(gate[:6]), own), true,
			"restore plan: 0 to add, 0 to change, 6 to destroy, 0 outside the gate, 0 to replace or forget"},
		{"one of the gate's own, the opening apply refused after the first",
			append(destroys(gate[:1]), own), true,
			"restore plan: 0 to add, 0 to change, 1 to destroy, 0 outside the gate, 0 to replace or forget"},
		// The upper end of the range, which a real plan cannot reach (the gate
		// writes fourteen addresses) and which the assertion still names: more
		// destroys than the gate has addresses is not this check's restore.
		{"more destroys than the gate has addresses",
			append(destroys(gate), change{gate[0], []string{"delete"}}), false,
			"15 to destroy"},
		{"a restore with nothing to destroy",
			[]change{own}, false,
			"0 to destroy"},
		// A REPLACEMENT OF A GATE ADDRESS, with the rest of the gate destroyed
		// beside it: the address filter passes it (it is one of the gate's own),
		// no exact count sees it, and the destroy count is inside the range — so
		// the action filter is the only thing between the restore and destroying
		// and rebuilding a conditioned role the check is about to measure. The
		// replacement alone, with nothing else in the plan, is refused by the
		// range instead: an exact ["delete"] count is zero for it.
		{"a replace of one of the gate's own addresses, beside thirteen destroys",
			append(append(destroys(gate[:13]), change{gate[13], []string{"delete", "create"}}), own), false,
			"1 to replace or forget"},
		{"a replace of one of the gate's own addresses, and nothing else",
			append([]change{change{gate[0], []string{"delete", "create"}}}, own), false,
			"0 to destroy"},
		{"a forget of one of the gate's own addresses, beside thirteen destroys",
			append(append(destroys(gate[:13]), change{gate[13], []string{"forget"}}), own), false,
			"1 to replace or forget"},
		{"a change of one of the gate's own addresses",
			append(append(destroys(gate[:6]), change{gate[6], []string{"update"}}), own), false,
			"1 to change"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "restore.json"), []byte(chk68PlanJSON(tc.changes)), 0o600); err != nil {
				t.Fatal(err)
			}
			// The assertion lives inside chk68_restore() in the section, because
			// its stop RETURNS rather than exits: `return` only leaves a function,
			// so the harness runs the lifted lines in one too, with the section's
			// own chk68_stop, and the function's exit status is what the caller
			// sees.
			script := "set -u\nCHK68_TMP='" + dir + "'\nP=fugaro-dev\n" + stopFn + "\nchk68_restore() {\n" + assertion + "\necho CHK68-ASSERTED\n}\nchk68_restore\n"
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
// read-only look and the end of the Restore — filter the seven listings with
// `$CHK68_OWNED`. The filter has to match the name inside a line rather than at
// the start of it: five of the seven listings print a FULL RESOURCE PATH
// (`projects/<P>/secrets/<name>`, `…/locations/<L>/repositories/<name>`), so an
// anchored `^fmgd-chk68-` matches no line any of them prints, and the check
// reports "no leftover" with all sixteen resources still in the project.
//
// This runs both blocks against a stubbed `gcloud` — the seven listings print a
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
// → the stub reports the unexpected call and the block fails. **The bucket
// listing is the one the review of 2026-10-21 asked for**: without it, a bucket
// delete that silently did nothing — which is what the fieldless
// `--format='value(project)'` made of every one of them — left both throwaway
// buckets in the project and the look still reported clean. Dropping the bucket
// listing → the bucket leftover is not seen and the leftover case fails on it.
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
			t.Errorf("Check 68's leftover filter is %s, anchored at the start of the line: five of the seven listings print a full resource path, so an anchored prefix matches none of the check's own names and the look reports an empty project", ere)
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
		// A bucket's line is the bucket's own name: `storage buckets list`
		// prints no path, which is the shape the delete that proves the
		// bucket's project is about.
		"fmgd-chk68-bucket-0123abcd",
		"xfmgd-chk68-bucket-0123abcd",
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
		// The installation's runs bucket: a bucket listing is in the look now,
		// and this is the name in it that must never be reported.
		"fugaro-dev-runs",
	}
	for _, l := range leftovers {
		if !re.MatchString(l) {
			t.Errorf("Check 68's leftover filter does not match %q, a line one of the seven listings prints for a resource the check created: the look would report nothing left behind while the resource was there", l)
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
		if restoreStart >= 0 && strings.Contains(l, "no leftover of this check's own in the seven listings") {
			restoreStop = i
			break
		}
	}
	if restoreStart < 0 || restoreStop < restoreStart {
		t.Fatal("Check 68's Restore no longer re-runs the filtered look over the seven listings: the check's guarantee that it leaves nothing behind is checked there, not assumed")
	}
	restoreLook := strings.Join(restoreBlock[restoreStart:restoreStop+1], "\n")

	// A stubbed gcloud, because what is run here is the section's filtering and
	// not gcloud: each of the seven listings prints its variable, and any other
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
    "storage buckets list"*)       printf '%s\n' "$STUB_BUCKETS" ;;
    *) echo "stub gcloud: unexpected call: $*" >&2; return 1 ;;
  esac
}
`
	listings := func(secret, repo, job, sched, key, account, bucket string) string {
		return "STUB_SECRETS='" + secret + "'\nSTUB_REPOSITORIES='" + repo + "'\nSTUB_JOBS='" + job +
			"'\nSTUB_SCHEDULER='" + sched + "'\nSTUB_KEYS='" + key + "'\nSTUB_ACCOUNTS='" + account +
			"'\nSTUB_BUCKETS='" + bucket + "'\n"
	}
	clean := listings(
		ownLines[0], ownLines[1], ownLines[2], ownLines[3], ownLines[4],
		ownLines[5]+"\n"+ownLines[6], ownLines[7])
	// One leftover per listing that prints a path, one service account, and one
	// bucket: every shape of line the filter has to see through.
	dirty := listings(
		ownLines[0]+"\n"+leftovers[0]+"\n"+leftovers[1],
		ownLines[1]+"\n"+leftovers[2],
		ownLines[2]+"\n"+leftovers[3],
		ownLines[3]+"\n"+leftovers[4],
		ownLines[4]+"\n"+leftovers[5],
		ownLines[5]+"\n"+ownLines[6]+"\n"+leftovers[6]+"\n"+leftovers[7],
		ownLines[7]+"\n"+leftovers[8]+"\n"+leftovers[9])

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
				if !strings.Contains(out, "no leftover of this check's own in the seven listings") {
					t.Errorf("Check 68's step-0 look did not say the project is clean:\n%s", out)
				}
				return
			}
			if strings.Contains(out, "no leftover of this check's own in the seven listings") {
				t.Errorf("Check 68's step-0 look reported no leftover while the listings carried ten of the check's own names:\n%s", out)
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
				if !strings.Contains(out, "no leftover of this check's own in the seven listings") {
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
// delete taken out of `chk68_try` → FAIL; a bucket deleted with a bare
// `gcloud storage rm` instead of `chk68_rm_bucket` → FAIL.
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
	restoreBlock := bashBlocks(docSection(t, path, section, "### 7. Restore"))[0]
	restoreNames := chk68DeleteNames(t, restoreBlock, "Check 68's Restore")

	// The check's two buckets are the only resources it deletes whose NAME is
	// global: `gcloud storage rm` takes a bucket URL rather than a project-scoped
	// resource, so `--project` does not confine it, and a bucket of the same name
	// in another project would be the one removed. Both cleanups therefore delete
	// them through chk68_rm_bucket, which asks the bucket which project it
	// belongs to first, and neither carries a bare `gcloud storage rm`.
	for _, tc := range []struct {
		label string
		block []string
	}{
		{"Check 68's cleanup_chk68 trap", strings.Split(cleanup, "\n")},
		{"Check 68's Restore", restoreBlock},
	} {
		for _, line := range tc.block {
			cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "chk68_try "))
			if strings.HasPrefix(cmd, "gcloud storage rm ") {
				t.Errorf("%s runs %q: a bucket name is global and `gcloud storage rm` ignores --project, so this line can delete a bucket of the same name that belongs to somebody else — the check's own two buckets go through chk68_rm_bucket, which proves the bucket's project first", tc.label, cmd)
			}
		}
	}

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
		// The check's two buckets are deleted through chk68_rm_bucket, which
		// proves the bucket is this project's before removing it: a bucket name
		// is global across all of GCS and `gcloud storage rm` takes a bucket URL
		// rather than a project-scoped resource, so `--project` does not confine
		// the delete. It takes exactly one name, like every other delete here.
		if strings.HasPrefix(cmd, "chk68_rm_bucket ") {
			commands++
			args := strings.Fields(strings.TrimPrefix(cmd, "chk68_rm_bucket "))
			if len(args) != 1 {
				t.Errorf("%s runs %q with %d bucket names: chk68_rm_bucket takes exactly ONE, and a line carrying two would delete neither", label, cmd, len(args))
				continue
			}
			names[strings.Trim(args[0], `"'`)]++
			continue
		}
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

// chk68Change is one entry of a plan's `resource_changes`, in the shape the
// tests that RUN Check 68's assertions feed them: an address and the action list
// terraform plans for it.
type chk68Change struct {
	address string
	actions []string
}

// chk68PlanJSON renders changes as the `terraform show -json` document the
// assertions read.
func chk68PlanJSON(changes []chk68Change) string {
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

// chk68GateAddresses returns the gate's fourteen addresses, the ones opening it
// creates and the restore destroys: seven conditioned roles and the seven
// bindings that carry them.
func chk68GateAddresses() []string {
	services := []string{"storage", "secret", "account", "apikey", "repository", "run_job", "scheduler_job"}
	kinds := []string{"google_project_iam_custom_role", "google_project_iam_member"}
	var gate []string
	for _, k := range kinds {
		for _, s := range services {
			gate = append(gate, "module.installation."+k+".fugaro_deployer_"+s+"[0]")
		}
	}
	return gate
}

// TestChecklistCheck68Step2PlanShapeIsRun executes the assertion Check 68 makes
// over the plan it is about to apply (step 2), instead of pinning its text.
//
// Step 2 is the opening half's only live write, and the assertion is what makes
// it safe: on a converged installation opening the gate adds exactly the gate's
// fourteen addresses and changes, destroys, replaces or forgets nothing. The
// three counts alone cannot see a REPLACEMENT — terraform plans one as
// `["delete","create"]`, which is neither an exact `["create"]` nor an exact
// `["delete"]` — so a plan that replaces a resource outside the gate reports
// 14/0/0 and would be applied. Nor can they see a plan that creates fourteen
// things of which one is NOT one of the gate's addresses: that is 14 adds too.
// The two filters the assertion carries are what see both, and this test feeds
// it both, plus the shapes it has to let through.
//
// Mutations (verified, run and restored): the `[ "$OTHER" = 0 ]` clause dropped
// from the stop → the case that creates fourteen addresses one of which is
// outside the gate passes; the `[ "$SWAP" = 0 ]` clause dropped → the forget of
// a gate address passes; the `select(.change.actions != ["no-op"])` filter
// narrowed to something that drops the gate's own addresses → the passing case
// is refused; the address filter anchored back at `^google_project_iam_…` → the
// passing case is refused, because a root plan's addresses carry the root's
// module name.
func TestChecklistCheck68Step2PlanShapeIsRun(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	block := chk68Block(t, section, `terraform show -json "$CHK68_TMP/chk68.tfplan"`)

	start, stop := -1, -1
	for i, l := range block {
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
		t.Fatal("Check 68's step 2 no longer counts the plan's adds, changes and destroys (there is no `ADD=$(jq` line): the apply is the opening half's only live write, and the counts are what stop it")
	}
	if stop < 0 {
		t.Fatal("Check 68's step 2 counts the plan and never stops on it: the counts have to be asserted rather than printed")
	}
	assertion := strings.Join(block[start:stop+1], "\n")
	if !strings.Contains(assertion, "exit 1") {
		t.Fatal("Check 68's step-2 assertion does not refuse: the stop prints its message and carries on, which is not a stop")
	}
	// The two filters, and the two clauses of the stop that read them. A count
	// without its clause is a count that is printed and ignored.
	for _, want := range []string{
		`select(.change.actions != ["no-op"]) | .address`,
		`select(.change.actions == ["delete","create"] or .change.actions == ["create","delete"] or .change.actions == ["forget"])`,
		`[ "$OTHER" = 0 ]`,
		`[ "$SWAP" = 0 ]`,
	} {
		if !strings.Contains(assertion, want) {
			t.Fatalf("Check 68's step-2 assertion does not carry %q: the three counts cannot see a replacement or a fourteenth add that is not one of the gate's addresses, so a plan of either shape would be applied to the pinned project", want)
		}
	}

	gate := chk68GateAddresses()
	if len(gate) != 14 {
		t.Fatalf("the gate is seven roles and seven bindings, and this test built %d addresses", len(gate))
	}
	creates := func(addrs []string) []chk68Change {
		out := make([]chk68Change, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, chk68Change{a, []string{"create"}})
		}
		return out
	}
	// A resource of the installation's own that opening the gate neither touches
	// nor creates: the plan is full of these, and they are no-ops.
	own := chk68Change{"module.installation.google_service_account.fugaro_deployer", []string{"no-op"}}

	cases := []struct {
		name    string
		changes []chk68Change
		passes  bool
		want    string
	}{
		{"the gate's own fourteen",
			append(creates(gate), own), true,
			"plan: 14 to add, 0 to change, 0 to destroy, 0 outside the gate, 0 to replace or forget"},
		{"the gate's own fourteen, unprefixed",
			append(creates(func() []string {
				out := make([]string, 0, len(gate))
				for _, a := range gate {
					out = append(out, strings.TrimPrefix(a, "module.installation."))
				}
				return out
			}()), own), true,
			"plan: 14 to add, 0 to change, 0 to destroy, 0 outside the gate, 0 to replace or forget"},
		// Fourteen adds, one of which is not one of the gate's addresses: every
		// count is what it should be, and only the address filter refuses it.
		{"fourteen adds, one of them outside the gate",
			append(creates(gate[:13]), chk68Change{"module.installation.google_storage_bucket.runs[0]", []string{"create"}}), false,
			"1 address(es) outside the gate"},
		// A replacement outside the gate: ADD, CHG and DEL all count nothing for
		// it, so the three counts report 14/0/0 and the plan would be applied.
		{"a replace outside the gate, which the three counts cannot see",
			append(creates(gate), chk68Change{"module.installation.google_service_account.runs", []string{"delete", "create"}}), false,
			"1 to replace or forget"},
		// A replacement OF a gate address: the address filter knows it (it is one
		// of the fourteen), so only the action filter refuses this one.
		{"a forget of one of the gate's own addresses",
			append(creates(gate), chk68Change{gate[0], []string{"forget"}}), false,
			"0 address(es) outside the gate's own fourteen and 1 to replace or forget"},
		{"a destroy of one of the gate's own addresses",
			append(creates(gate[:13]), chk68Change{gate[13], []string{"delete"}}), false,
			"13 to add"},
		{"an update of a resource of the installation's own",
			append(append(creates(gate), own), chk68Change{"module.installation.google_service_account.runs", []string{"update"}}), false,
			"1 to change"},
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not on PATH")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte(chk68PlanJSON(tc.changes)), 0o600); err != nil {
				t.Fatal(err)
			}
			script := "set -u\nCHK68_TMP='" + dir + "'\n" + assertion + "\necho CHK68-ASSERTED\n"
			code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
			if tc.passes {
				if code != 0 {
					t.Fatalf("Check 68's step-2 assertion refused the plan opening the gate produces (exit %d):\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
				}
				if !strings.Contains(out, "CHK68-ASSERTED") {
					t.Fatalf("the assertion did not reach the end of the block, so it stopped somewhere it should not have:\nstdout:\n%s\nstderr:\n%s", out, errOut)
				}
				if !strings.Contains(out, tc.want) {
					t.Errorf("Check 68's step-2 assertion ran but did not print %q:\n%s", tc.want, out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("Check 68's step-2 assertion passed a plan it must refuse, and the next command in step 2 applies that plan to the pinned project:\n%s", out)
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

// TestChecklistCheck68BucketDeleteProvesTheProject runs the guard's
// `chk68_rm_bucket` against a stubbed `gcloud`.
//
// The two throwaway buckets are the one pair of names in the check that is not
// confined to the project by anything gcloud does: a bucket name is global
// across all of GCS and `gcloud storage rm` takes a bucket URL rather than a
// project-scoped resource, so `--project` does not stop it deleting a bucket of
// the same name that belongs to somebody else. The guard's answer is to ask the
// bucket which project it belongs to and delete only on a match. This test
// stubs that answer three ways — this project, another project, and a bucket
// that is not there — and holds the function to deleting in exactly the first
// case.
//
// The stub is faithful to the command, not to the function's expectations:
// `gcloud storage buckets describe` STANDARDIZES the API's metadata and the
// standardized resource has no project field at all, so a stub that answered
// `$STUB_OWNER` to any `--format` would pass while the real command printed an
// empty line and exited 0 for every bucket — every delete refused as "reports
// no project", both buckets leaked, and this test the only thing that said the
// guard was fine. So the stub answers the number only to the form that has it
// (`--raw`, the resource as the API returns it, plus projectNumber) and prints
// the empty line and exit 0 the real command prints for anything else.
//
// Mutations (verified, run and restored): the `"$PROJECT_NUMBER")` match arm
// replaced by `*)` (delete whatever the bucket says it belongs to) → the
// other-project case and the empty-answer case fail; the describe's failure
// branch dropped → the absent case fails, because the function then asks the
// case what to do with an empty answer; the `chk68_try` around the `rm` dropped
// → the static assertion fails, because a delete whose failure is silent is the
// one thing the trap exists to avoid; `--raw` dropped from the describe → the
// first case fails, because the stub then answers the standardized empty line
// and nothing is deleted; the field changed back to `value(project)` → the
// static assertion fails.
func TestChecklistCheck68BucketDeleteProvesTheProject(t *testing.T) {
	guard := chk68GuardBlock(t)
	rmFn := chk68FuncBody(t, guard, "chk68_rm_bucket") + "\n}"
	// The one-liner the delete goes through, taken from the guard too: the
	// harness runs the check's own functions and not a restatement of them.
	tryFn := ""
	for _, l := range strings.Split(guard, "\n") {
		if s := strings.TrimSpace(l); strings.HasPrefix(s, "chk68_try() {") {
			tryFn = s
		}
	}
	if tryFn == "" {
		t.Fatal("Check 68's guard block no longer declares chk68_try")
	}
	if !strings.Contains(rmFn, `gcloud storage buckets describe`) {
		t.Fatal("Check 68's chk68_rm_bucket no longer asks the bucket which project it belongs to: a bucket name is global and `gcloud storage rm` ignores --project, so without that question the delete is not confined to this project at all")
	}
	// The field has to be one the command PRINTS. `gcloud storage buckets
	// describe` standardizes the API's metadata, and the standardized resource
	// has no project field (creation_time, location, name, storage_url, …): the
	// fieldless `value(project)` prints an empty line and exits 0 for every
	// bucket, so the function would refuse every delete as "reports no project"
	// and the two throwaway buckets would stay in the sandbox project. The
	// number is only in the raw resource, so both halves are required.
	if !strings.Contains(rmFn, "--raw") || !strings.Contains(rmFn, "value(projectNumber)") {
		t.Error("Check 68's chk68_rm_bucket does not read the bucket's own project with `--raw --format='value(projectNumber)'`: the standardized resource carries no project field, so any other form answers nothing and the comparison has nothing to compare")
	}
	if strings.Contains(rmFn, "value(project)'") {
		t.Error("Check 68's chk68_rm_bucket asks for `value(project)`, a field `gcloud storage buckets describe` does not print: it answers an empty line with exit 0 for every bucket, so every delete is refused as 'reports no project' and the check's two buckets leak")
	}
	if !strings.Contains(rmFn, "chk68_try gcloud storage rm -r") {
		t.Error("Check 68's chk68_rm_bucket removes the bucket outside chk68_try: a delete that failed would be silent, and the trap's own line would then read as though the bucket were gone")
	}

	const stub = `
gcloud() {
  case "$*" in
    "storage buckets describe "*)
      if [ "$STUB_STATE" = absent ]; then echo "stub: no such bucket" >&2; return 1; fi
      # The real command, faithfully: the number is in the RAW resource only.
      # Any other form asks the STANDARDIZED resource for a field it does not
      # have, and the real gcloud prints an empty line and exits 0 — which is
      # how a wrong field turns into a silent refusal to delete anything.
      case "$*" in
        *--raw*)
          case "$*" in
            *projectNumber*) printf '%s\n' "$STUB_OWNER"; return 0 ;;
          esac ;;
      esac
      echo "stub: the standardized bucket resource has no such field, so this prints an empty line and exits 0" >&2
      printf '\n' ;;
    "storage rm -r "*) echo "STUB-RM $*" ;;
    *) echo "stub gcloud: unexpected call: $*" >&2; return 1 ;;
  esac
}
`
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	const (
		bucket = "fmgd-chk68-bucket-0123abcd"
		mine   = "123456789"
		theirs = "987654321"
	)
	for _, tc := range []struct {
		name    string
		state   string
		owner   string
		deletes bool
		want    string
	}{
		{"the bucket is this project's", "there", mine, true, ""},
		{"the bucket belongs to another project", "there", theirs, false, "the bucket belongs to project " + theirs},
		{"the bucket reports no project at all", "there", "", false, "reports no project"},
		{"the bucket is not there", "absent", "", false, "is not there — nothing to delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "set -u\nP=fugaro-dev\nPROJECT_NUMBER='" + mine + "'\nSTUB_STATE='" + tc.state + "'\nSTUB_OWNER='" + tc.owner + "'\n" +
				stub + "\n" + tryFn + "\n" + rmFn + "\nchk68_rm_bucket " + bucket + "\necho CHK68-RETURNED\n"
			code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
			if code != 0 {
				t.Fatalf("chk68_rm_bucket exited %d:\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
			if strings.Contains(errOut, "stub gcloud") {
				t.Fatalf("chk68_rm_bucket calls gcloud in a way this stub does not know: %s", errOut)
			}
			if !strings.Contains(out, "CHK68-RETURNED") {
				t.Fatalf("chk68_rm_bucket did not return to its caller, so it left the shell:\nstdout:\n%s\nstderr:\n%s", out, errOut)
			}
			deleted := strings.Contains(out, "STUB-RM") && strings.Contains(out, "gs://"+bucket)
			if deleted != tc.deletes {
				t.Errorf("chk68_rm_bucket deleted the bucket: %v, and it must delete it only when the bucket reports this project's number:\nstdout:\n%s\nstderr:\n%s", deleted, out, errOut)
			}
			if !tc.deletes {
				if !strings.Contains(errOut, "REFUSED to delete gs://"+bucket) && !strings.Contains(errOut, "gs://"+bucket+" is not there") {
					t.Errorf("chk68_rm_bucket neither deleted nor said why not:\nstderr:\n%s", errOut)
				}
				if tc.want != "" && !strings.Contains(errOut, tc.want) {
					t.Errorf("chk68_rm_bucket's refusal does not say %q:\nstderr:\n%s", tc.want, errOut)
				}
			}
		})
	}
}

// TestChecklistCheck68BucketSuffixIsDrawnOnce: the two throwaway buckets carry a
// random suffix, and the guard block runs again at the start of every `--step N`
// invocation of the one script — so the suffix has to be drawn once per run and
// kept, or a repeated step would name a bucket the check never created: step 4
// would create a second bucket and the trap and the Restore would delete neither.
//
// The block is run twice in the same $CHK68_TMP, the way two invocations run it, and
// the second draw has to be the first. The two names are also held to the
// leftover filter, which is what has to see them at the end of the Restore.
//
// Mutations (verified, run and restored): the `$(cat "$CHK68_TMP/rand")` read
// dropped, so the suffix is drawn fresh every time → the second run differs;
// the `printf … > "$CHK68_TMP/rand"` write dropped → the second run differs;
// the suffix dropped from BUCKET_MISS → the two names collide with the near
// miss's own prefix.
func TestChecklistCheck68BucketSuffixIsDrawnOnce(t *testing.T) {
	guard := chk68GuardBlock(t)

	var suffix []string
	seen := false
	for _, l := range strings.Split(guard, "\n") {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "CHK68_RAND=$(cat") {
			seen = true
		}
		if !seen {
			continue
		}
		suffix = append(suffix, l)
		if strings.HasPrefix(s, "BUCKET_MISS=") {
			break
		}
	}
	if len(suffix) == 0 {
		t.Fatal("Check 68's guard block no longer draws the random suffix its two bucket names carry: the names are global, and two runs of the check would collide")
	}
	owned := ""
	for _, l := range strings.Split(guard, "\n") {
		if s := strings.TrimSpace(l); strings.HasPrefix(s, "CHK68_OWNED=") {
			owned = s
		}
	}
	if owned == "" {
		t.Fatal("Check 68's guard block no longer defines CHK68_OWNED")
	}
	re, err := regexp.Compile(strings.Trim(strings.TrimPrefix(owned, "CHK68_OWNED="), "'"))
	if err != nil {
		t.Fatalf("Check 68's leftover filter is not an ERE grep -E can run: %v", err)
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	dir := t.TempDir()
	block := strings.Join(suffix, "\n")
	script := "set -u\nCHK68_TMP='" + dir + "'\nmkdir -p \"$CHK68_TMP\"\n" + block +
		"\necho \"first ${CHK68_RAND} ${BUCKET_MAIN} ${BUCKET_MISS}\"\n" + block +
		"\necho \"second ${CHK68_RAND} ${BUCKET_MAIN} ${BUCKET_MISS}\"\nprintf 'file %s\\n' \"$(cat \"$CHK68_TMP/rand\")\"\n"
	code, out, errOut := runChk68Shell(t, script, []string{"PATH=" + os.Getenv("PATH")})
	if code != 0 {
		t.Fatalf("Check 68's suffix block exited %d:\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	fields := map[string][]string{}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 4 && (f[0] == "first" || f[0] == "second") {
			fields[f[0]] = f[1:]
		}
	}
	if len(fields["first"]) != 3 || len(fields["second"]) != 3 {
		t.Fatalf("the suffix block did not print its two runs:\n%s", out)
	}
	rand, main, miss := fields["first"][0], fields["first"][1], fields["first"][2]
	if !regexp.MustCompile(`^[a-f0-9]{8}$`).MatchString(rand) {
		t.Errorf("Check 68's bucket suffix is %q, which is not the eight hex characters the guard draws", rand)
	}
	if main != "fmgd-chk68-bucket-"+rand {
		t.Errorf("Check 68's positive bucket is %q, and it has to be fmgd-chk68-bucket-%s: the check's leftover look keys on the fmgd-chk68- prefix", main, rand)
	}
	if miss != "xfmgd-chk68-bucket-"+rand {
		t.Errorf("Check 68's near-miss bucket is %q, and it has to be xfmgd-chk68-bucket-%s: it is the near miss because it contains the prefix without starting with it, and a suffix of its own would make it a name the check never created", miss, rand)
	}
	for _, n := range []string{main, miss} {
		if !re.MatchString(n) {
			t.Errorf("Check 68's leftover filter does not match %q, one of the two buckets the check creates: the look at the end of the Restore would report a clean project with the bucket still in it", n)
		}
	}
	if fields["second"][0] != rand {
		t.Errorf("Check 68 drew a different suffix the second time the guard block ran (%s, then %s): the guard runs again at the start of every `--step N` invocation, and a fresh suffix each time names a bucket the check never created — step 4 would create a second bucket and the trap and the Restore would delete neither", rand, fields["second"][0])
	}
	if !strings.Contains(out, "file "+rand) {
		t.Errorf("Check 68's suffix is not kept in $CHK68_TMP/rand, so the next step cannot draw the same one:\n%s", out)
	}
}

// TestChecklistCheck68SchedulerRegionComesFromTheConfig: every Cloud Scheduler
// command in Check 68 took its `--location` from the config's `region`, and
// Cloud Scheduler is not offered in every region: `gcp.SchedulerRegion` maps
// this installation's `us-east5` to `us-east4`
// (internal/backend/gcp/names.go:282), the installation's own jobs run in the
// recorded `scheduler_region` (internal/localcfg, written by `fugaro update` from
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
