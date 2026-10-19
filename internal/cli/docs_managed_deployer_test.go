package cli

import (
	"os"
	"strings"
	"testing"
)

// TestChecklistHasCheck68 pins Check 68 of docs/gcp-live-checklist.md: the live
// check of the managed deployer's redesigned fence (docs/design/
// managed-deployer-fence.md; deploy/terraform/gcp/modules/installation/
// deployer.tf). The section is the only place the shape's open questions are
// measured, so what this test holds is what makes it runnable by the owner and
// safe to run: whose check it is, the four hard stops in the guard block, the
// two directions every service is measured in, the Secret Manager version read
// and the section's own statement that it does not settle the design's C7 (a
// name of a conditioned type that misses its `extract` template cannot be
// constructed, so the register must not read as measuring one), the restore
// that closes the gate again, and the fact that Check 67 — the transitive-reach
// half — is not this section.
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
		// the section says outright that it is NOT settled by this check.
		// Without this the register would read as measuring a question the
		// check cannot construct a probe for (see the section's step 6).
		"C7",
		"What this check does not settle",
		"stays UNVERIFIED",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not say %q", want)
		}
	}

	// 2. The guard block: the first block, with the four hard stops and the
	// project set by the owner rather than read from gcloud.
	blocks := bashBlocks(section)
	if len(blocks) < 6 {
		t.Fatalf("Check 68 has %d bash blocks, want the guard, the read-only setup and the steps", len(blocks))
	}
	guard := strings.Join(blocks[0], "\n")
	for _, want := range []string{
		"CHK68_PROJECT:?",
		"grep -qx belong",
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

	// 4. The fail-closed question: named, and disowned. The section has to say
	// in as many words that this check does not settle C7, and why — a
	// SecretVersion is not the secret clause's type, so the read is denied for
	// want of the permission and the condition is never evaluated.
	for _, want := range []string{
		"secretmanager.versions.*",
		"What this check does not settle",
		"stays UNVERIFIED",
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
