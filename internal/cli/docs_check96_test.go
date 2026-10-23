package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// pipeIntoPythonHeredocRE matches a pipe into `python3` on a line that opens a
// heredoc further along it: the one shape whose stdin the heredoc wins.
//
// The first form of this regex required `<<` immediately after `python3` or
// `python3 -`, which is only one member of the family: the shape that got past
// it was `printf '%s' "$ANSWER" | python3 - "$CHK96_SANDBOX_REPO" <<'PY'` — a
// heredoc with an argv argument in front of it — which reads exactly as dead as
// `… | python3 - <<'PY'` and passed the guard with the defect in the register.
// A pipe into python on a line that opens a heredoc anywhere after it is the
// rule, so a document cannot slip past by putting a word between the two.
//
// (The `|` is part of the pattern and not of the prose: a comment that names the
// shape without the pipe — “a pipe into `python3 - <<'PY'`” — is exactly what the
// fixed form's explanation says, and it must not be flagged as the defect.)
var pipeIntoPythonHeredocRE = regexp.MustCompile(`\|\s*python3?\s[^\n]*<<`)

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
//     no job's environment on this tree carries that name — the one place the
//     name is written at all is the credentials service's own container, as that
//     service's audience (deploy/terraform/gcp/modules/installation/
//     credentials.tf, from the installation field credentials_service.url) — so
//     an opted-in runner still reads the App key and mints in the job today. A
//     check that read "the mint is the service's" as the state of this tree
//     would be false, and this is the check the owner runs after that release.
//   - The service refuses every call on this tree: its deployment configures it
//     now — the seven names credentials.tf sets are what C1.14's builder reads,
//     so a revision starts and serves — and its account holds exactly one grant,
//     the bundle's accessor (Q6), and no role on the installation's database, so
//     the first read a call makes (the identity check's read of the run's
//     write-once outcome, which ServeHTTP runs before the stop flags) is denied
//     and the call is answered `state_unavailable` rather than served. The check has
//     to say so, and it has to keep saying so for exactly as long as
//     credentials.tf grants that account no `firebasedatabase` role.
//
// One more thing is held to the code rather than to the prose: step 4's pass
// condition. The pairing refusal is made in ServeHTTP after Auth.Verify returned
// a whole identity, so it is answered by refuseCall (internal/credsvc/cascade.go),
// which writes ONE audit line through auditCall before the wire error — purpose
// `mint`, credential `github-app-key`, no tier, status `unverified_run_identity`,
// the RUN's own repository. So the check must expect exactly one line for it, and
// the line it expects is the route's and not the branch's. Only the refusals
// answered before there is a verified identity to record take `refuse`, which
// writes the answer and nothing else (server.go's refuseIdentity is the one on
// this path). The check first expected NO line for the pairing refusal, on the
// reading that `refuse` answers it; that was false, and a check that kept it
// would have failed a correct release on the day it ran.
//
// Mutations (verified, run and restored): "**No step of it has been run, and
// nothing here is a claim that it works.**" deleted → FAIL (a check whose steps
// read as carried out); "**on this tree that list is empty**" rewritten to
// "**this list is populated**" while ServiceHeldSecretNames is still empty →
// FAIL (the check would claim the job holds no key); "**The service is
// configured from its own deployment now, and refuses every call.**" flattened
// while credentials.tf grants the service's account no database role → FAIL
// (the check would leave a reader taking step 3's mint as one this tree can
// make); a `roles/firebasedatabase.*` grant added to credentials.tf while the
// check still says every call is refused → FAIL in the other direction (the
// service can serve and the check did not notice); a repository field added to
// credsvc.MintRequest → FAIL (the check's strongest claim is that the
// repository is the service's to decide, and that is exactly this absent
// field); the pairing clause in credsvc's admission changed from
// `if !bound || ja.Slug != id.Slug` to `if !bound` → FAIL (the check states the
// pairing refusal as the reason a run identity for X cannot mint for Y, and
// that clause is the mechanism); the `# 0` read-back on the Restore's scratch
// line deleted → FAIL (the check's final assertion is that nothing survives
// it); the known-limit bullet on the URL gate deleted whole → FAIL (the check
// would leave a reader taking the keyless path as the state of this tree);
// step 4's "**exactly `1` audit line** for the pairing refusal's run id" put
// back to "**`0` audit lines**" → FAIL (the check would fail a correct release,
// whose refusal is recorded by refuseCall); the pairing refusal's line moved
// out of server.go's refuseCall and into refuse → FAIL in the other direction
// (the check's expectation is held to refuseCall's body and to auditCall's
// being written before the answer); and a mint's own refusals moved off
// refuseMint/auditMint → FAIL (the check states those as audited too).
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
		// And the audit expectation of step 4, which is the truth of the answer path: the
		// pairing refusal is made after the identity was verified, so refuseCall records it —
		// exactly one line, the route's purpose and credential, no tier, the refusal's own
		// status — and a check that expected none would fail a correct release.
		"**exactly `1` audit line for the pairing refusal's run id**",
		"which writes one line through `auditCall` and only then the typed refusal",
		"**A `0` here would be the finding**",
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
	// Step 4's audit expectation, held to the code on the other side: the pairing
	// refusal is answered by `refuseCall` (cascade.go), which writes one line
	// through `auditCall` and only then the wire error, so the check must expect
	// exactly one line for it — the route's purpose and credential, no tier, the
	// refusal's own status. `refuse` is the wire writer alone, and it is the path
	// the refusals answered before there is a verified identity take
	// (`refuseIdentity`), which is what the check says about (b). If either half of
	// that pair moves, the check's expectation is the thing that has to move with
	// it.
	if !strings.Contains(server, "s.refuseCall(ctx, w, id, purpose, credential, \"\", StatusUnverified, ErrorUnverifiedRunIdentity,") {
		t.Errorf("internal/credsvc/server.go no longer answers the pairing refusal through `refuseCall`: Check 96's step 4 expects exactly one audit line for it — an answer through `refuse` would leave a refusal this service made unrecorded — so the check and the code have moved apart")
	}
	if !strings.Contains(server, "func (s *Server) refuseIdentity(ctx context.Context, w http.ResponseWriter") {
		t.Errorf("internal/credsvc/server.go no longer has `refuseIdentity`: Check 96 states the identity refusal as the one refusal on this path that is answered before an identity is verified, and so the one that writes no audit line")
	}
	cascade := readDoc(t, "internal/credsvc/cascade.go")
	if !strings.Contains(cascade, "func (s *Server) refuseCall(ctx context.Context, w http.ResponseWriter") {
		t.Errorf("internal/credsvc/cascade.go no longer has `refuseCall`: Check 96's step 4 is written on its behaviour — it writes the refusal's one audit line and then the typed refusal")
	}
	if body := check96FuncBody(t, cascade, "func (s *Server) refuseCall(ctx context.Context, w http.ResponseWriter"); !strings.Contains(body, "auditCall") || !strings.Contains(body, "s.refuse(") {
		t.Errorf("internal/credsvc/cascade.go's `refuseCall` no longer writes the audit line before the answer (%q): Check 96's step 4 expects exactly one line for the pairing refusal, written before the answer goes out", strings.TrimSpace(body))
	}
	if !strings.Contains(server, "func (s *Server) refuse(w http.ResponseWriter") {
		t.Errorf("internal/credsvc/server.go no longer has `refuse`: Check 96's step 4 is written on its behaviour — it is the wire writer the refusals answered before an identity is verified take, and it writes no audit line")
	}
	if body := check96FuncBody(t, server, "func (s *Server) refuse(w http.ResponseWriter"); strings.Contains(body, "Audit.Write") || strings.Contains(body, "auditMint") || strings.Contains(body, "auditCall") {
		t.Errorf("internal/credsvc/server.go's `refuse` now writes an audit line (%q): Check 96 states it as the wire writer alone — the refusals answered before there is a verified identity take it — so the check and the code have moved apart", strings.TrimSpace(body))
	}
	mintGo := readDoc(t, "internal/credsvc/github.go")
	if !strings.Contains(mintGo, "func (s *Server) refuseMint(ctx context.Context, w http.ResponseWriter") {
		t.Errorf("internal/credsvc/github.go no longer has `refuseMint`: Check 96 states a mint's own refusals as audited too, and that is the path they take")
	}
	if body := check96FuncBody(t, mintGo, "func (s *Server) refuseMint(ctx context.Context, w http.ResponseWriter"); !strings.Contains(body, "auditMint") {
		t.Errorf("internal/credsvc/github.go's `refuseMint` no longer audits the mint it refuses (%q): Check 96 states that a refused mint is recorded like an issued one", strings.TrimSpace(body))
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

	// The third, likewise held to the code, and in the direction the fact moved:
	// the deployment DOES configure the service now — the seven names
	// credentials.tf sets are exactly what C1.14's builder reads, and
	// TestCredentialsServiceEnvIsTheDeploymentsOwn is what holds that pair — and
	// what still stops it serving is that its account holds no role on the
	// installation's database. The first read a call makes is the run's
	// write-once outcome — the identity check's, before the stop switches — so
	// the call is refused `state_unavailable` rather than served, and the check
	// has to say that instead of the refusal that was lifted.
	//
	// The claim is read against the module's own role lines, so the day the
	// credentials account is granted a database role the sentence is the one that
	// has to go. It reads credentials.tf, the file that creates the account and
	// its one grant; a database role added to the account from another module
	// would not be seen by this line, and that is a review question rather than
	// something a string match can answer.
	// Since 0.10.1 the role is the credentials service's OWN custom role
	// (fugaroCredentialsDatabase, in the Firebase project, the two data
	// permissions), so the sentence that has to be there is the one that says
	// what the service can read, and the one that has to go is the refusal.
	// Both directions are read against the module, so neither sentence can be
	// left behind by a change to the other side.
	const refusedEveryCall = "**The service is configured from its own deployment now, and refuses every call.**"
	const readsTheDatabase = "**The service is configured from its own deployment now, and reads the installation's database.**"
	hasRole := credentialsDatabaseRoleRE.MatchString(
		stripLineComments(readDoc(t, "deploy/terraform/gcp/modules/installation/credentials.tf")))
	if hasRole {
		if strings.Contains(check, refusedEveryCall) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account a database role (%s) — the service reads the database and can serve — and Check 96 still says every call is refused: the gap was closed and this check did not notice", tf.CredentialsDatabaseRole)
		}
		if !strings.Contains(check, readsTheDatabase) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account its database role (%s, the two data permissions) now, so every database read a call makes — the run's outcome first among them — is allowed and step 3's mint can be made, and Check 96 does not say so", tf.CredentialsDatabaseRole)
		}
	} else {
		if !strings.Contains(check, refusedEveryCall) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account no role on the installation's database, so the first read a call makes — the run's write-once outcome — is denied and the call is refused `state_unavailable` rather than served, and Check 96 does not say so: that is what decides whether the mint of step 3 can be made at all")
		}
		if strings.Contains(check, readsTheDatabase) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account no database role and Check 96 says it reads the installation's database: a reader would take step 3's mint as one this tree can make")
		}
	}
	// And the half of the same sentence that names the configuration: the check
	// states it as the deployment's own seven names, so the list it names has to
	// be there.
	if !strings.Contains(readDoc(t, "internal/cli/credentials_serve.go"), "credentialsServiceEnvNames") {
		t.Errorf("Check 96 states the service's configuration as the seven names its deployment sets (`credentialsServiceEnvNames`), and internal/cli/credentials_serve.go no longer has that list")
	}
}

// credentialsDatabaseRoleRE matches the credentials service's database role in
// credentials.tf: the shape the guard above reads. Two spellings, because
// there are two ways to give that account a database role.
//
//   - the custom role 0.10.1 adds: role_id = "fugaroCredentialsDatabase" (the
//     ID is read from internal/infra/tf's own constant, so a rename cannot
//     leave this guard watching a name nothing writes any more), declared in
//     the installation module and bound to the service's own account;
//   - a predefined database role granted directly (role = "roles/firebasedatabase…"),
//     which is what the guard saw FIRST and all it saw: that line alone is the
//     narrower role this release deliberately did not take (viewer cannot
//     write; admin adds the instance lifecycle), and it would still mean the
//     account holds a database reach.
//
// The old regex was the second spelling alone, and its own comment said a
// database role added to the account from another shape "would not be seen by
// this line, and that is a review question". 0.10.1 answered it: the custom
// role is what the tree writes now, so the guard reads it.
//
// The match is made on the file with its comments stripped
// (stripLineComments, this package's own), and not on the raw text: every
// spelling this regexp knows is one the file's own prose discusses at length —
// the header names `fugaroCredentialsDatabase` and the long comment above the
// role names both predefined RTDB roles and why they were refused — so a match
// over the raw bytes would flip this guard to "the service reads the database"
// with no grant in the file at all. That is the vacuous pass the stripped read
// removes, and the same shape the other guards in this package avoid.
// Mutation (run, restore): delete the two resources from credentials.tf and
// leave their comments, and this guard fails on the missing grant rather than
// passing on the prose.
var credentialsDatabaseRoleRE = regexp.MustCompile(`role_id\s*=\s*"` + regexp.QuoteMeta(tf.CredentialsDatabaseRole) + `"|role\s+= "roles/firebasedatabase`)

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
// Mutation (run and restored): Check 96's step 3 put back to
// `printf '%s' "$ANSWER" | python3 - "$CHK96_SANDBOX_REPO" <<'PY'` — the argv
// form the first regex missed → FAIL, which is the widening this test's regex
// was extended for.
func TestDocsChecklistHasNoPipeIntoPythonHeredoc(t *testing.T) {
	register := readDoc(t, "docs/gcp-live-checklist.md")
	if m := pipeIntoPythonHeredocRE.FindString(register); m != "" {
		t.Errorf("docs/gcp-live-checklist.md pipes a document into a python heredoc (%q): bash gives the heredoc stdin, so the script reads EOF instead of the document and the step prints none of its facts. Pass the document as a file or an argv argument (the form the register's other heredocs use), or use `python3 -c`.", m)
	}
}

// shellHeredocOpenerRE matches a `<<`/`<<'PY'` heredoc opener and captures the
// delimiter, the way bash reads it: `<<`, an optional `-`, optional blanks, and
// the word, quoted or not.
var shellHeredocOpenerRE = regexp.MustCompile(`<<-?\s*(?:'([A-Za-z_][A-Za-z0-9_]*)'|([A-Za-z_][A-Za-z0-9_]*))`)

// apostropheInWordRE matches an apostrophe inside a word — a letter, an
// apostrophe and a letter — which outside a heredoc can only be an English
// apostrophe and never shell quoting: the closing quote of a single-quoted
// program is followed by a blank, a bracket, a semicolon or the end of the line,
// and an opening one is preceded by a blank, `=`, `(` or `-c`.
var apostropheInWordRE = regexp.MustCompile(`[A-Za-z]'[A-Za-z]`)

// TestDocsCheck96ShellBlocksRunAndCloseTheirHeredocs is the guard the review of
// Check 96 asked for, over the register's own reading of the section.
//
// The register's shell is an *artifact that runs*: internal/cli/
// docs_artifact_runs_test.go's bashBlocks, which several tests here run a
// section's blocks through, keeps each block's lines with their indentation, so
// what a step looks like in the file is what bash is handed. Three defects of
// exactly that kind were found in Check 96 by hand and are held here:
//
//   - steps 1, 3 and 4 opened `<<'PY'` and closed it with an indented `   PY`.
//     `bash -n` does NOT fail on that — bash only ends a heredoc at a delimiter
//     at column 0, warns “here-document … delimited by end-of-file”, and exits
//     0 — and every command after the opener is consumed as heredoc input and
//     never runs. In step 1 that includes the `gcloud secrets get-iam-policy`
//     read the step's FACT is entirely about, and in step 4 the audit read.
//   - the same blocks' bodies carried the step's own list indentation, so the
//     first python statement was indented and python died with “IndentationError:
//     unexpected indent” even once the delimiter was at column 0. That is why
//     the rule below is about the delimiter AND the body's first line.
//   - step 5's read-back carried an apostrophe inside a single-quoted
//     `python3 -c '…'` program (“the check's two run ids”). The quote closes
//     early, the words after it become python's argv, python exits on its own
//     SyntaxError — and, because the mangled words were consumed as arguments,
//     the `ls -A "$W"; rm -rf "$W"` and the `# 0` read-back that follow it never
//     ran and the block ended quietly. `bash -n` does catch this one where bash
//     is installed, and the apostrophe-in-a-word rule below states the shape
//     itself, for the machines with no bash on PATH.
//
// The rules are scoped to Check 96 rather than stated over the register: the
// indented-delimiter shape pre-exists in merged Check 92 (Task C1.5's, at
// docs/gcp-live-checklist.md's Check 92 section), which is another work's check
// and not this one's to edit, and a register-wide rule would fail on it. The
// pipe rule above is register-wide because that shape is invisible and reads as
// working; this one is a class the register has not been swept for.
//
// Mutations (run and restored): step 1's `PY` delimiter indented again → FAIL;
// step 1's first body line re-indented → FAIL; step 5's apostrophe put back into
// the `python3 -c` program → FAIL.
func TestDocsCheck96ShellBlocksRunAndCloseTheirHeredocs(t *testing.T) {
	section := check96Section(t)
	blocks := bashBlocks(section)
	if len(blocks) < 6 {
		t.Fatalf("Check 96 has %d ```bash blocks; the section is the register's shell and it does not have fewer than six", len(blocks))
	}

	for i, block := range blocks {
		// 1. Every heredoc a block opens is closed by a delimiter at column 0, and
		//    its body starts at column 0. The scan walks the block line by line and
		//    steps over a heredoc's body once it has been read, so a `<<` written
		//    inside one body is not read as another opener; a comment is skipped
		//    for the same reason — a comment cannot open a heredoc, and this
		//    section explains the shape it bans in one.
		for ln := 0; ln < len(block); ln++ {
			line := block[ln]
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			m := shellHeredocOpenerRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			delim := m[1]
			if delim == "" {
				delim = m[2]
			}
			// The terminator is the first line that would end the heredoc if it
			// stood at column 0 — not the next line that happens to be at column
			// 0 with that word on it, which is how a block with two heredocs
			// would hide the first one's indented delimiter behind the second
			// one's.
			end := -1
			indented := ""
			for j := ln + 1; j < len(block); j++ {
				if strings.TrimSpace(block[j]) != delim {
					continue
				}
				if block[j] != delim {
					indented = block[j]
					break
				}
				end = j
				break
			}
			if indented != "" {
				t.Errorf("Check 96's bash block %d opens a `<<%s` heredoc on its line %d and closes it with %q — an indented delimiter: bash ends a heredoc only at a delimiter at column 0, so it reads on to the end of the block and every command after the opener is swallowed as heredoc input and never runs (and `bash -n` exits 0 on it)", i+1, delim, ln+1, indented)
				continue
			}
			if end < 0 {
				t.Errorf("Check 96's bash block %d opens a `<<%s` heredoc on its line %d which no line closes: every command after the opener is swallowed as heredoc input and never runs", i+1, delim, ln+1)
				continue
			}
			for j := ln + 1; j < end; j++ {
				if strings.TrimSpace(block[j]) == "" {
					continue
				}
				if block[j] != strings.TrimLeft(block[j], " \t") {
					t.Errorf("Check 96's bash block %d opens a `<<%s` heredoc on its line %d whose body starts indented (%q): the body is handed to the interpreter verbatim, so a list indentation carried into it is a syntax error (`IndentationError: unexpected indent` for python) and the step does nothing", i+1, delim, ln+1, block[j])
				}
				break
			}
			ln = end
		}

		// 2. No apostrophe inside a word outside a heredoc body. In shell this is
		//    the defect step 5 carried: `python3 -c '… the check's two run ids …'`
		//    closes the program's quote at the apostrophe, re-splits the rest of
		//    the line into python's argv, and python exits on its own SyntaxError —
		//    after which nothing on the line or below it runs, so the step's own
		//    restore and its `# 0` read-back were never reached. `bash -n` catches
		//    that one (rule 3), but only where bash is installed; this rule names
		//    the shape itself, in the section's own words. Heredoc bodies are
		//    exempt: an apostrophe inside a python string there is harmless, and
		//    this check's own prose prints some.
		body := make([]bool, len(block))
		for ln := 0; ln < len(block); ln++ {
			line := block[ln]
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			m := shellHeredocOpenerRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			delim := m[1]
			if delim == "" {
				delim = m[2]
			}
			for j := ln + 1; j < len(block) && block[j] != delim; j++ {
				body[j] = true
			}
		}
		// The rule reads only the inside of a `python3 -c '…'` program: the
		// program may run over several lines (this section's FPROJ does), so the
		// scan carries “still inside the program” across lines — a marker opens
		// it, and each line's quote parity closes it again — and a program's own
		// `#` comments are left alone, where an apostrophe is python's business
		// and not bash's.
		inProgram := false
		for ln, line := range block {
			if body[ln] {
				inProgram = false
				continue
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !inProgram && !strings.Contains(line, "python3 -c '") {
				continue
			}
			if m := apostropheInWordRE.FindString(line); m != "" {
				t.Errorf("Check 96's bash block %d line %d carries an apostrophe inside a word (%q) inside a `python3 -c '…'` program: that quote ends the program, the words after it become python's argv, python exits on a SyntaxError, and the commands after the line — a step's own restore among them — never run. Keep apostrophes out of the program, or pass values in as argv", i+1, ln+1, trimmed)
			}
			// A program is open after this line when the line's quotes do not
			// pair up. The quotes before the marker are a balanced pair in
			// every program this section writes (`printf '%s' …`), so counting
			// the whole line is counting the program.
			inProgram = strings.Count(line, "'")%2 == 1
		}

		// 3. And the block parses as bash. This does not catch an unterminated
		//    heredoc (bash -n exits 0 on one), which is what rule 1 is for; it
		//    catches the quoting class.
		bash, err := exec.LookPath("bash")
		if err != nil {
			continue
		}
		f := filepath.Join(t.TempDir(), fmt.Sprintf("check96-%d.sh", i))
		if err := os.WriteFile(f, []byte(strings.Join(block, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
			t.Errorf("Check 96's bash block %d does not parse as bash (%v): %s", i+1, err, out)
		}
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Log("bash is not on PATH: the parse half of this test did not run")
	}
}
