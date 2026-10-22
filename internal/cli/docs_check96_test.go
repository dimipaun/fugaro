package cli

import (
	"strings"
	"testing"
)

// Check 96 — GitHub tokens through the service (Task C2.4, D145, F34) — as the
// register writes it.
//
// Check 96 is the live half of the change that moves the GitHub App's private
// key out of the job: C2.1 mints at the credentials service, C2.2 makes the
// runner ask there, C2.3 takes the job's accessor and mount away, and Check 96
// is what reads the three results on a real installation. It is owner-run and
// NOT RUN, and the point of this test is that the three claims the check is a
// statement about are read out of the code rather than trusted to the prose,
// and that the two things which are NOT true on this tree are stated in the
// check itself:
//
//   - C2.2's release has not landed: internal/config/providers.go's
//     ServiceHeldSecretNames is still empty, so the job keeps its accessor and
//     its container keeps the mount. A check that claimed the drop had happened
//     would be false, and one that stayed silent about it would let a reader
//     take the drop as done.
//   - The service cannot be configured from its own deployment: the
//     installation module puts three names on its container and
//     credentialsServiceConfig refuses by name, so no mint can be made at all
//     on this tree. The check has to say so, and it has to keep saying so for
//     exactly as long as internal/cli/credentials_serve.go still refuses.
//
// Mutations (verified, run and restored): "**No step of it has been run, and
// nothing here is a claim that it works.**" deleted → FAIL (a check whose steps
// read as carried out); "**on this tree that list is empty**" rewritten to
// "**this list is populated**" while ServiceHeldSecretNames is still empty →
// FAIL (the check would claim the job holds no key); "**The service cannot be
// configured from its own deployment.**" flattened while
// credentialsServiceConfig still refuses → FAIL (the check would let a reader
// take steps 2 to 5 as runnable); a repository field added to
// credsvc.MintRequest → FAIL (the check's strongest claim is that the
// repository is the service's to decide, and that is exactly this absent
// field); the pairing clause in credsvc's admission changed from
// `if !bound || ja.Slug != id.Slug` to `if !bound` → FAIL (the check states the
// pairing refusal as the reason a run identity for X cannot mint for Y, and
// that clause is the mechanism); the `# 0` read-back on the Restore's scratch
// line deleted → FAIL (the check's final assertion is that nothing survives
// it).
func TestDocsCheck96IsWrittenAndSaysWhatIsNotYetTrue(t *testing.T) {
	check := check96Section(t)

	// The section exists as a section, and its register row exists as a row: the
	// two are one check, and a section without its row is a document no reader
	// finds from the register.
	whole := readDoc(t, "docs/gcp-live-checklist.md")
	if !strings.Contains(whole, "| **96** |") && !strings.Contains(whole, "| 96 |") {
		t.Errorf("docs/gcp-live-checklist.md's register has no row 96: Check 96 is written below it and the register is where a reader finds it")
	}

	// The claims of the plan's row 79, as the check has to state them.
	for _, want := range []string{
		"the job holds no App key",
		"a run identity for repository X cannot mint for Y",
		"**NOT RUN.**",
		"**No step of it has been run, and nothing here is a claim that it works.**",
		"never `belong`",
		// The mint is the service's, for the calling account's repository, with the
		// run's permissions — the three mechanisms behind the second claim.
		"`credsvc.MintPath` = `POST /v1/github/installation-token`",
		"there is no repository field to fill in",
		"`github.RunTokenPermissions`",
		"`JobAccount.Repo`",
		// The pairing refusal, which is what the third claim is.
		"`ja, bound := s.jobs[id.JobAccount]; if !bound || ja.Slug != id.Slug`",
		"the run identity is not a run of the calling job's repository",
		"`unverified_job_identity`",
		// Secret hygiene: a check that mints credentials has to say where they go.
		"greps its own text for `ghs_`",
		"**Never paste a token of any kind",
		// And the Restore, whose final assertion is that nothing survives it.
		"**Restore.**",
		"the read-back count (**`0`**",
	} {
		if !strings.Contains(check, want) {
			t.Errorf("docs/gcp-live-checklist.md's Check 96 does not say %q: the check is the live statement of D145's change, and this is a clause a reader takes the check's behaviour from", want)
		}
	}
	if !strings.Contains(check, "# 0") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer reads its own scratch directory back to 0: the Restore's final assertion is that no file the check wrote survives it")
	}

	// The code each claim is read out of, so that a rename or a widening on
	// either side is a failing test and not a check that cites nothing.
	mint := readDoc(t, "internal/credsvc/github.go")
	if !strings.Contains(mint, "MintPath = ") || !strings.Contains(mint, "/v1/github/installation-token") {
		t.Errorf("Check 96 nails the mint to `credsvc.MintPath` and `POST /v1/github/installation-token`, and internal/credsvc/github.go no longer has them")
	}
	req := cenMintRequestStruct(t, mint)
	if strings.Contains(req, "epository") {
		t.Errorf("internal/credsvc/github.go's MintRequest now has a repository field (%q): Check 96 tells a reader the repository is the service's to decide and never the caller's to name — the reason \"a run identity for repository X cannot mint for Y\" is structural and not a rule — and that is exactly this absent field", strings.TrimSpace(req))
	}
	apptoken := readDoc(t, "internal/gitprov/github/apptoken.go")
	if !strings.Contains(apptoken, "func RunTokenPermissions(") {
		t.Errorf("Check 96 says the minted token's reach is the run's own set because the service signs with `github.RunTokenPermissions`, and internal/gitprov/github/apptoken.go no longer spells that set out")
	}
	server := readDoc(t, "internal/credsvc/server.go")
	if !strings.Contains(server, "ja.Slug != id.Slug") {
		t.Errorf("internal/credsvc/server.go no longer pairs the run identity with the calling job's account (`ja.Slug != id.Slug`): Check 96 states that pairing as the reason a run identity for X cannot mint for Y, and the check's step 4 is written against it")
	}
	if !strings.Contains(server, "the run identity is not a run of the calling job's repository") {
		t.Errorf("internal/credsvc/server.go no longer answers `the run identity is not a run of the calling job's repository`: Check 96 records that sentence as the refusal's own body")
	}

	// The first thing that is not true on this tree, held to the code: while the
	// held-secrets list is empty the job keeps the key, and the check has to say
	// so; the day the list names it, the same sentence is the one that has to go.
	const notLanded = "**on this tree that list is empty**"
	const stillHolds = "would be answered **yes, it still does**"
	if heldSecretsListNamesTheAppKey(t) {
		if strings.Contains(check, notLanded) || strings.Contains(check, stillHolds) {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames now names the App key — C2.3's drop landed — and Check 96 still says the job holds it: the release landed and this check did not")
		}
	} else {
		if !strings.Contains(check, notLanded) || !strings.Contains(check, stillHolds) {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames is still empty — the job keeps its accessor and its mount — and Check 96 does not say so plainly: this is the one claim a reader would take as done today, and step 1's answer is the release's first fact")
		}
	}

	// The second, likewise held to the code: the command refuses to configure a
	// service, so no mint can be made, and the check records that as the blocker
	// it is rather than pretending a step below could run.
	const blocker = "**The service cannot be configured from its own deployment.**"
	if strings.Contains(readDoc(t, "internal/cli/credentials_serve.go"), "cannot be configured from this deployment") {
		if !strings.Contains(check, blocker) {
			t.Errorf("internal/cli/credentials_serve.go still refuses to configure the credentials service (`credentialsServiceConfig`), so no mint can be made on this tree, and Check 96 does not say so: steps 2 to 5 cannot run until that changes, and the check is written to be run AFTER the release the owner runs it with")
		}
	} else if strings.Contains(check, blocker) {
		t.Errorf("internal/cli/credentials_serve.go no longer refuses to configure the credentials service — the service can be configured from its deployment — and Check 96 still calls that the blocker: the gap was closed and this check did not notice")
	}
}

// check96Section returns the register's `## Check 96` section, from its heading
// to the next `## ` heading or the end of the file. The check is the register's
// last section, but the slice is bounded by the heading rather than by the end
// so that a section added after it does not silently widen what this test reads.
func check96Section(t *testing.T) string {
	t.Helper()
	register := readDoc(t, "docs/gcp-live-checklist.md")
	const head = "\n## Check 96:"
	_, rest, ok := strings.Cut(register, head)
	if !ok {
		t.Fatalf("docs/gcp-live-checklist.md has no `## Check 96:` section: Task C2.4 writes it, and this test's marker has moved")
	}
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	if len(rest) < 2000 {
		t.Fatalf("docs/gcp-live-checklist.md's Check 96 section is %d bytes: this test's markers have moved, and a section this short is not the check", len(rest))
	}
	return rest
}
