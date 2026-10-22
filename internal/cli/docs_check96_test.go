package cli

import (
	"regexp"
	"strings"
	"testing"
)

// pipeIntoPythonHeredocRE matches a pipe into `python3 -` followed by a heredoc
// opener: the one shape whose stdin the heredoc wins.
var pipeIntoPythonHeredocRE = regexp.MustCompile(`\|\s*python3?\s+(-\s+)?<<`)

// Check 96 — GitHub tokens through the service (Task C2.4, D145, F34) — as the
// register writes it.
//
// Check 96 is the live half of the change that moves the GitHub App's private
// key out of the job: C2.1 mints at the credentials service, C2.2 makes the
// runner ask there, C2.3 takes the job's accessor and mount away, and Check 96
// is what reads the three results on a real installation. It is owner-run and
// NOT RUN, and the point of this test is that the three claims the check is a
// statement about are read out of the code rather than trusted to the prose,
// and that the things which are NOT true on this tree are stated in the check
// itself:
//
//   - C2.2's release has not landed: internal/config/providers.go's
//     ServiceHeldSecretNames is still empty, so the job keeps its accessor and
//     its container keeps the mount. A check that claimed the drop had happened
//     would be false, and one that stayed silent about it would let a reader
//     take the drop as done.
//   - The keyless path is switched on by the job's environment naming the
//     service, not by the opt-in: internal/gitprov/providers/providers.go opens
//     the keyless provider only when FUGARO_CREDENTIALS_SERVICE_URL is set, and
//     nothing on this tree puts that name on a job (no field of internal/infra,
//     no module of deploy/terraform/gcp), so an opted-in runner still reads the
//     App key and mints in the job today. A check that read "the mint is the
//     service's" as the state of this tree would be false, and this is the check
//     the owner runs after that release.
//   - The service cannot be configured from its own deployment: the
//     installation module puts three names on its container and
//     credentialsServiceConfig refuses by name, so no mint can be made at all
//     on this tree. The check has to say so, and it has to keep saying so for
//     exactly as long as internal/cli/credentials_serve.go still refuses.
//
// One more thing is held to the code rather than to the prose: step 4's pass
// condition. The pairing refusal is made in ServeHTTP, before the mint handler
// is reached, and internal/credsvc/server.go's refuse writes the answer and no
// audit line — so the check must expect NO audit line for it. It first expected
// one ("always audited"), which a correct release would have failed.
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
// it); the known-limit bullet on the URL gate deleted whole → FAIL (the check
// would leave a reader taking the keyless path as the state of this tree);
// step 4's "**no audit line** for it" put back to "always audited" → FAIL (the
// check would fail a correct release); and, on the code's side, an audit call
// added to internal/credsvc/server.go's `refuse` → FAIL (the expectation is
// held to `refuse`'s body, so the day it starts recording admission refusals
// this check says so instead of passing).
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
		// The job's spec is read from the file the guard wrote, by path: a heredoc takes
		// stdin, so the `printf … | python3 - <<'PY'` this step first used handed python the
		// script and an empty stdin, and the step printed none of its facts.
		"python3 - \"$W/job.json\" <<'PY'",
		// The known limit of this release, which the check has to carry: the keyless path is
		// switched on by the job's environment naming the service, and nothing on this tree
		// puts that name there, so an opted-in runner still reads the key and mints in-job.
		"the expected answer today is `false`",
		"**What to expect today is therefore `FUGARO_CREDENTIALS_SERVICE_URL` absent from the job's environment and the key still read in-job**",
		// And the corrected audit expectation of step 4: the pairing refusal is made before
		// the mint is reached, so it is not audited, and a check that expected a line there
		// would fail a correct release.
		"**a refusal that never reaches the mint is not audited**",
		"**no audit line** for it: the refusal is made before the mint is reached",
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
	// Step 4's audit expectation, held to the code on the other side: the refusal
	// is answered by `refuse`, which writes the wire error and no audit line, so
	// the check must expect no line for it. If `refuse` ever grows an audit call,
	// the check's expectation is the thing that has to change with it.
	if !strings.Contains(server, "func (s *Server) refuse(w http.ResponseWriter") {
		t.Errorf("internal/credsvc/server.go no longer has `refuse`: Check 96's step 4 is written on its behaviour — it answers the pairing refusal and writes no audit line")
	}
	if body := check96FuncBody(t, server, "func (s *Server) refuse(w http.ResponseWriter"); strings.Contains(body, "Audit.Write") || strings.Contains(body, "auditMint") {
		t.Errorf("internal/credsvc/server.go's `refuse` now writes an audit line (%q): Check 96's step 4 expects NO audit line for the pairing refusal — an admission refusal is not a mint's act — so the check and the code have moved apart", strings.TrimSpace(body))
	}

	// The second thing that is not true on this tree, held to the code as well:
	// the keyless provider is opened only when the job's environment names the
	// service, and the check has to carry that limit rather than read the mint as
	// the service's on this tree.
	providers := readDoc(t, "internal/gitprov/providers/providers.go")
	if !strings.Contains(providers, "if vars[EnvCredentialsServiceURL] != \"\"") {
		t.Errorf("internal/gitprov/providers/providers.go no longer gates the keyless github provider on `EnvCredentialsServiceURL`: Check 96 states that gate as the reason an opted-in runner still reads the App key and mints in the job on this tree, and that is the switch it names")
	}
	if !strings.Contains(check, "**What to expect today is therefore `FUGARO_CREDENTIALS_SERVICE_URL` absent from the job's environment and the key still read in-job**") {
		t.Errorf("Check 96 does not state what to expect today about the URL gate: the keyless path needs FUGARO_CREDENTIALS_SERVICE_URL and nothing on this tree sets it, so the check the owner runs must say the variable is expected absent and the key still read in-job")
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

// check96FuncBody returns one function's body out of a source file the test
// already read: from the signature to the next top-level `func `, so a call
// added below it does not leak into what the test reads.
func check96FuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("%s not found: this test's marker has moved", sig)
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	return body
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

// TestDocsChecklistHasNoPipeIntoPythonHeredoc is the register-wide rule behind the
// Check 96 defect this test file was extended for: bash gives a heredoc
// precedence over a pipe, so `printf '%s' "$JSON" | python3 - <<'PY'` runs the
// script with the pipe attached to nothing — python's stdin is the heredoc, and
// `json.load(sys.stdin)` reads EOF and dies. `bash -n` parses it happily (the
// block is valid shell), and the guard block sets only `set -u`, so the step
// would have printed none of the facts it exists for and the run would have
// continued past the one read that decides whether the release landed.
//
// It is named `TestDocsChecklist…` and not `TestChecklist…` on purpose: the
// `TestChecklist…` family is the per-check tests, enumerated by name and count
// in docs/plans/2026-10-19-compute-backend.md's open item 9 and held to the tree
// by TestDocsComputeBackendNamesEveryChecklistTest. This is a rule over the
// register's shell, not a check, so it does not join that family and does not
// make a foreign plan's enumeration wrong.
//
// The rule is stated over the whole register rather than over Check 96, because
// the shape is invisible: any step that pipes a document into a python heredoc
// reads nothing. A heredoc that takes its input as an argv path — the form every
// other heredoc in this file uses — is what to write instead.
//
// Mutation (run and restored): Check 96's step 1 put back to
// `printf '%s' "$JOB_JSON" | python3 - <<'PY'` → FAIL.
func TestDocsChecklistHasNoPipeIntoPythonHeredoc(t *testing.T) {
	register := readDoc(t, "docs/gcp-live-checklist.md")
	if m := pipeIntoPythonHeredocRE.FindString(register); m != "" {
		t.Errorf("docs/gcp-live-checklist.md pipes a document into a python heredoc (%q): bash gives the heredoc stdin, so the script reads EOF instead of the document and the step prints none of its facts. Pass the document as a file or an argv argument (the form the register's other heredocs use), or use `python3 -c`.", m)
	}
}
