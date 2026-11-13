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

// check96Script is Check 96's own script: the register tells the owner to run
// it, and the block it prints is what the section below explains.
const check96Script = "scripts/live-checks/check96.sh"

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
//   - C2.3's drop is HELD (the owner's ruling: the flip waits on this check's
//     own live result, #907): internal/config/providers.go's
//     ServiceHeldSecretNames is still empty, so the job keeps its accessor and
//     its container keeps the mount. A check that claimed the drop had happened
//     would be false, and one that stayed silent about it would let a reader
//     take the drop as done.
//   - The keyless path is switched on by the job's environment naming the
//     service, not by the switch: internal/gitprov/providers/providers.go opens
//     the keyless provider only when FUGARO_CREDENTIALS_SERVICE_URL is set, and
//     **the launch is what sets it now** (0.10.1) —
//     internal/cli/run.go's credentialsServiceLaunchEnv puts it on a switched-on
//     repository's per-repository execution and on no other, from the URL the
//     credentials stage recorded in the project's config. So the check has to
//     say what to expect per job: the variable present on a repository job's
//     execution and the key not read in-job, absent on a shared project job and
//     on every job of a repository whose root has not been re-applied with the
//     switch on. The sentence it
//     used to carry ("nothing on this tree sets it", the variable expected
//     absent, the key still read in-job) is now false and is banned below.
//   - The service reads the installation's database on this tree: its
//     deployment configures it — the seven names credentials.tf sets are what
//     C1.14's builder reads, so a revision starts — and since 0.10.1 its account
//     holds its OWN database role, the custom role fugaroCredentialsDatabase
//     (firebasedatabase.instances.get and .update, on the Firebase project the
//     database lives in, and no instance lifecycle), bound to that account and
//     no other. Every database read a call makes is therefore admitted, and the
//     first of them is NOT the operator's stop switches: it is the identity
//     verification's own read of the run's write-once outcome (auth.go's
//     checkLive, reached from Auth.Verify), which the stop switches, the policy
//     and the bundle all come after. So step 4's pairing refusal (a) IS
//     observable — the pairing question is asked only after Auth.Verify returned
//     a whole identity, and that verification now completes — and so is step 4's
//     other half, (b), a caller that is no job account: verifyJob refuses that
//     caller out of the account table the container was configured with, before
//     any database read, so the service answers `403` `unverified_job_identity`
//     there, exactly as it does on the release the check is written for. What
//     the check has to keep saying for exactly as long as credentials.tf grants
//     that account a `firebasedatabase` role is that the reads are allowed — and,
//     with the launch setting FUGARO_CREDENTIALS_SERVICE_URL on a repository
//     job (the bullet above), that a mint reaches the bundle, which no
//     Terraform writes: an unfilled bundle is the refusal the check now meets.
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
// configured from its own deployment now, and reads the installation's
// database.**" flattened while credentials.tf grants the service's account its
// database role → FAIL (the check would leave a reader taking the service's
// database reach as a gap that is still open); the two resources of that role
// deleted from credentials.tf while the check still says the reads are admitted
// → FAIL in the other direction (the service cannot read the database and the
// check did not notice); a repository field added to
// credsvc.MintRequest → FAIL (the check's strongest claim is that the
// repository is the service's to decide, and that is exactly this absent
// field); the pairing clause in credsvc's admission changed from
// `if !bound || ja.Slug != id.Slug` to `if !bound` → FAIL (the check states the
// pairing refusal as the reason a run identity for X cannot mint for Y, and
// that clause is the mechanism); the `# 0` read-back on the Restore's scratch
// line deleted → FAIL (the check's final assertion is that nothing survives
// it); the per-job URL paragraph rewritten back to "nothing on this tree sets
// it", the variable expected absent and the key still read in-job → FAIL, on
// the ban and on the pin both, because internal/cli/run.go's
// credentialsServiceLaunchEnv sets the variable on a repository job and
// internal/cli/run.go is read for the helper's existence; step 4's
// "**exactly `1` audit line** for the pairing refusal's run id" put
// back to "**`0` audit lines**" → FAIL (the check would fail a correct release,
// whose refusal is recorded by refuseCall); the pairing refusal's line moved
// out of server.go's refuseCall and into refuse → FAIL in the other direction
// (the check's expectation is held to refuseCall's body and to auditCall's
// being written before the answer); a mint's own refusals moved off
// refuseMint/auditMint → FAIL (the check states those as audited too); the
// check's "the first read a call makes — **not** the stop switches, but the
// identity verification's own read of the run's write-once outcome" put back to
// the draft's "the first read a call makes, the operator's stop switches, is
// denied" → FAIL (that order is not the code's: auth.go's checkLive is reached
// from Auth.Verify, and Kills.Stop is asked only after it — and the check's
// account of `state_unavailable`, and of why step 4's pairing refusal cannot
// be reached on this tree, rest on that order); that paragraph's "**nor can
// step 4's pairing refusal (a) be observed**" deleted → FAIL (the check would
// claim a deployed service answers the pairing refusal on a tree where the
// denied database read is the verification's own); and its "(b) is observable"
// — or the `verifyJob` sentence beside it — deleted, or the two halves put back
// together as "answers both (a) and (b) `state_unavailable`" → FAIL (verifyJob
// is the first thing Auth.Verify does and it reads the account table out of the
// configuration, so a caller that is no job account is refused 403
// unverified_job_identity here, before any database read); the check's "that refusal **is**
// audited" put back to the draft's "a refusal taken before the mint is reached
// is NOT audited on this tree" → FAIL (the pairing refusal is made after
// Auth.Verify returned a whole identity, so refuseCall records it — a check
// that said otherwise would fail a correct release and contradict its own step
// 4); and the register's row 99's "**The service's issuance audit line on the
// answer path IS wired** (#902)" put back to "nothing on the forward path calls
// `audit.Write`" → FAIL (cascade.go's answer writes that line, so the row would
// send a reader looking for a missing line that is not missing).
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
		// The check is ONE command now: the register tells the owner to run the
		// script rather than to paste five guard-block steps by hand, and the
		// script is named as the artifact the block comes from.
		"**Run it:** `bash scripts/live-checks/check96.sh`",
		"scripts/live-checks/check96.sh",
		// The wiring, which the check has to state per job: the launch sets the
		// variable on a switched-on repository's per-repository job and on no
		// other, so the expected answer is true there and false on a shared
		// project job and on every job of a repository whose root has not been
		// re-applied with the switch on.
		"the expected answer on a switched-on repository's repository job is `true`",
		"On a **shared project job** the expected answer is `false`",
		"**What to expect on a switched-on repository is therefore `FUGARO_CREDENTIALS_SERVICE_URL` present on a per-repository job's execution and the App key not read in-job",
		"**The launch is what puts it there, as of 0.10.1**",
		"credentialsServiceLaunchEnv",
		"held until the live result of this check proves the deployed service mints",
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
	if !strings.Contains(check, "no scratch directory and no token in any file") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer reads its own scratch directory back: the Restore's final assertion is that no file the check wrote survives it, and the script's own `restore` row is where that is read now")
	}
	if !strings.Contains(check, "one `FACT <key>: <verdict> — <detail>` line per key") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says what the block it asks the owner to paste back is made of: the owner diffs a pasted block against the section's own list of keys")
	}

	// The register's row 96 carries two of the same claims, and two reviews found
	// both of them false the same way the section's were: the pairing refusal is
	// recorded (it is made after Auth.Verify returned a whole identity), and the
	// first database read of a call is the identity verification's own read of
	// the run's outcome, not the operator's stop switches. The row is where a
	// reader meets the check first, so it is held to the same two facts.
	row := registerRow(t, whole, "96")
	for _, want := range []string{
		"that refusal **is** audited",
		"the first read a call makes — **not** the stop switches, but the identity verification's own read of the run's write-once outcome",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("docs/gcp-live-checklist.md's row 96 does not say %q: the row is where the check's three claims are read first, and this is one of the two a review found it stating falsely", want)
		}
	}
	for _, notWant := range []string{
		"a refusal taken before the mint is reached is NOT audited",
		"the first read a call makes, the operator's stop switches, is denied",
		"nothing on the forward path calls `audit.Write`",
	} {
		if strings.Contains(row, notWant) {
			t.Errorf("docs/gcp-live-checklist.md's row 96 still states %q: that reading is not the code's — auth.go's checkLive is the first database read, the pairing refusal is audited through refuseCall, and cascade.go's answer writes the issuance line — and a row that keeps it contradicts the section below it", notWant)
		}
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
	// The three calls are one path and the check's expectation is about the
	// path: refuseCall delegates to refuseCallCause, which writes the audit line
	// through `auditCall` and only then the typed refusal through `s.refuse`. A
	// reading held to `refuseCall`'s own body alone would be a reading of where
	// the code happens to sit, and it went red when the cause-carrying half
	// moved one level down (the refusal's own server-side line, refusallog.go) —
	// so both halves are pinned: the entry point that every refusal after the
	// pairing takes, and the body that audits before it answers.
	if body := check96FuncBody(t, cascade, "func (s *Server) refuseCall(ctx context.Context, w http.ResponseWriter"); !strings.Contains(body, "refuseCallCause") {
		t.Errorf("internal/credsvc/cascade.go's `refuseCall` no longer answers through the audited path `refuseCallCause` (%q): Check 96's step 4 expects exactly one line for the pairing refusal", strings.TrimSpace(body))
	}
	if body := check96FuncBody(t, cascade, "func (s *Server) refuseCallCause(ctx context.Context, w http.ResponseWriter"); !strings.Contains(body, "auditCall") || !strings.Contains(body, "s.refuse(") {
		t.Errorf("internal/credsvc/cascade.go's `refuseCallCause` no longer writes the audit line before the answer (%q): Check 96's step 4 expects exactly one line for the pairing refusal, written before the answer goes out", strings.TrimSpace(body))
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

	// Step 2's front-end row is the one shape where the status and the count fail
	// to tell two different services apart: a service deployed WITHOUT Cloud Run
	// IAM lets the call reach the container, where `refuseIdentity` answers the
	// same `403` `unverified_job_identity` and writes no line either — there is
	// no verified identity to record — so a row that read the status and the
	// unmoved line count alone would pass a service anyone can call. The body is
	// what separates them (the platform's own non-JSON refusal against the
	// service's typed one), and both halves of the pair are pinned: the section
	// says the row reads the body, and the script's `step_front_end` does read it.
	// Mutation (run and restored): the script's front-end step put back to the
	// status-and-count test alone → FAIL; the section's sentence dropped → FAIL.
	if !strings.Contains(check, "so this row reads the **body** the call came back with") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says the `front-end` row reads the body the unauthenticated call came back with: the status and the unmoved audit-line count are the same for Cloud Run's own refusal and for a service deployed without IAM, whose `refuseIdentity` answers the same `403` and writes no line either, so without the body the row would pass a service anyone can call")
	}
	// And the counts it compares have to be counts it read. The row's two audit
	// reads are compared for equality, and a `gcloud logging read` that exits 0
	// with a document this check cannot parse leaves both empty — equal to each
	// other — which is a PASS asserting "the service wrote no line for it" from a
	// log that was never read. The section says the read that cannot be counted
	// stops the run instead, and the script's `audit_lines` is held to checking
	// the reader's status.
	// Mutation (run and restored): `audit_lines` put back to
	// `field_of lines "$(reader count <…)"` → FAIL.
	if !strings.Contains(check, "it compares only two counts it actually read") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says the `front-end` row compares two counts it actually read: the equality test over two counts that were never taken is true, so the row would record a PASS about the service's log from a document it could not parse")
	}
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "audit_lines() {"); !strings.Contains(body, `ans=$(reader count <"$SCRATCH/audit-count.json") || return 1`) || !strings.Contains(body, "[ -n \"$lines\" ] || return 1") {
		t.Errorf("scripts/live-checks/check96.sh's `audit_lines` no longer checks the count it hands out (%q): the counts this row compares are taken through `field_of`, whose pipeline succeeds on empty input, so an unread log gives two empty counts that compare equal and the row PASSes on a log it never read", strings.TrimSpace(body))
	}
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "step_front_end() {"); !strings.Contains(body, `reader refusal <"$SCRATCH/noident.json"`) || !strings.Contains(body, "BY THE SERVICE") {
		t.Errorf("scripts/live-checks/check96.sh's `step_front_end` no longer tells the two `403`s apart by the body (%q): the section states that discrimination as this row's mechanism, and a step that read the status and the line count alone would pass a service deployed without Cloud Run IAM", strings.TrimSpace(body))
	}

	// A row that reads the service's log has to wait for it. Cloud Logging is
	// eventually consistent — the line the service wrote a moment ago is often
	// not readable at once — and the `audit` and `pairing` rows read an empty
	// document as the one finding they exist to report, *"the service answered
	// this call and left it unrecorded"*. A row that read once would therefore
	// report that finding, on every run, against a service that is behaving: a
	// false live result, and the one the App-key drop (#907) is waiting on. The
	// section says both rows re-read until the line is there and fails only a
	// line that never arrives; the script's `wait_for_audit` is that read, with
	// the bound the section names — so the document's "about 90 seconds" and the
	// script's `CHECK96_LOG_TRIES` × `CHECK96_LOG_POLL` cannot drift apart — and
	// all three rows that read a run's lines go through it rather than through
	// `audit_for`, which reports the empty first read.
	// Mutation (run and restored): `wait_for_audit`'s loop cut to a single
	// `audit_for` call → FAIL, on the case whose first read is empty.
	if !strings.Contains(check, "Both rows therefore re-read the run's lines until the line the call should have written is there") || !strings.Contains(check, "about 90 seconds") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says the `audit` and `pairing` rows re-read the service's log until the line is there, within a bound: an empty document is what those rows read as \"the service answered and left it unrecorded\", so a reader who is not told about the wait takes an ordinary ingestion delay for the finding")
	}
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "wait_for_audit() {"); !strings.Contains(body, `local every=${CHECK96_LOG_POLL:-5} limit=${CHECK96_LOG_TRIES:-18}`) || !strings.Contains(body, `[ "$lines" -ge "$want" ] && break`) {
		t.Errorf("scripts/live-checks/check96.sh's `wait_for_audit` no longer re-reads until the run's lines are there, with the bound the section names (%q): a read taken once reports the empty document Cloud Logging serves while a just-written line is still being ingested, and the `audit` and `pairing` rows read that as the finding they exist to report", strings.TrimSpace(body))
	}
	if script := readRepoFile(t, check96Script); strings.Count(script, `wait_for_audit "`) != 3 {
		t.Errorf("scripts/live-checks/check96.sh has %d `wait_for_audit` call(s) where the section names three log reads that wait — the `audit` row's, the `pairing` row's and the `outsider` row's second read of the mint's run: a row left reading through `audit_for` reports a service whose line has not been ingested yet as one that left the call unrecorded", strings.Count(script, `wait_for_audit "`))
	}

	// Which repository the check mints for is the owner's to name. With more than
	// one repository holding state and none named, the reader used to take the
	// first one — and what the check does with that repository is mint a REAL
	// GitHub installation token for it and read its reach at GitHub, so a pick the
	// owner did not make is a token spent on a repository that may not be the
	// sandbox at all. It refuses now, and the refusal lists the candidates: the
	// section says the run stops, and the reader is held to the refusal.
	// Mutation (run and restored): the first-of-several pick put back →
	// test_a_derivation_among_several_repositories_is_refused FAILs on
	// "a call before the refusal".
	if !strings.Contains(check, "stops (exit `2`) and lists them rather than picking one") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says an unnamed run stops when more than one repository has state: Check 96 mints a real GitHub installation token for whatever repository it is handed, so the section has to say the repository is named rather than picked")
	}
	if body := check96PythonFunc(t, readRepoFile(t, check96Script), "def cmd_budget(want):"); !strings.Contains(body, "cannot tell which one is the sandbox") {
		t.Errorf("scripts/live-checks/check96.sh's `cmd_budget` no longer refuses to derive one repository out of several (%q): the check mints a real installation token for the repository it names and reads that repository at GitHub, so a derivation the owner did not make spends a token on a repository that may not be the sandbox", strings.TrimSpace(body))
	}

	// And the config's `repos` mapping is read at the indentation fugaro writes
	// that file with. `localcfg.Config.Marshal` is `yaml.Marshal` (yaml.v3, four
	// spaces), so a reader that only accepts a two-space entry finds no repository
	// at all in a config fugaro wrote itself: `credentials_service_ready` is
	// `unset` for every repository, `step_job_env` reaches its final PASS branch,
	// and the FAIL for a switched-on repository's job that has lost the service's
	// address can never fire. The section says where the record is read.
	// Mutation (run and restored): `len(pad) == 2` put back with the fixture at
	// four spaces → test_a_job_that_has_no_service_url_while_the_config_records_one_is_a_fail
	// FAILs with the check PASSing a job that has no URL.
	if !strings.Contains(check, "at the indentation fugaro writes that file with") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says the repository's record is read out of the config at the indentation fugaro writes it with: yaml.v3 writes four spaces, and a `job-env` row that could not find the record would report `unset` where the repository's own answer is the thing being read")
	}
	if body := check96PythonFunc(t, readRepoFile(t, check96Script), "def cmd_config(path, repo):"); !strings.Contains(body, "entry_pad is None") || !strings.Contains(body, "if len(pad) <= entry_pad:") {
		t.Errorf("scripts/live-checks/check96.sh's `cmd_config` no longer takes the `repos` mapping's indentation from the block's first key (%q): localcfg.Config.Marshal is yaml.Marshal at yaml.v3's four-space indent, and a reader that assumed two finds no repository in the installation's own config", strings.TrimSpace(body))
	}

	// The installation is the sandbox one by NAME, not by its project alone. The
	// project is what the calls go to, but the Firebase project, the token signer
	// and the restricted web API key this check mints its run identities with are
	// read out of whichever installation the config in force describes — so a
	// second installation on fugaro-dev would be read as this one and minted
	// against, while the register's row says the check runs on installation
	// `fugaro` and no other. The section says the guard is by name and why, and
	// `step_installation` is held to the name it accepts.
	// Mutation (run and restored): the NAME case neutered (`*) ;;`) →
	// test_another_installation_on_fugaro_dev_stops FAILs with every row PASS
	// under a `scratch-install` doctor, which is the review's own probe.
	if !strings.Contains(check, "the installation is read by **name**, not by its project alone") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says the installation is read by name rather than by its project alone: the register's row promises the check runs on installation `fugaro` and no other, and the Firebase project, the signer and the web API key all come out of that one config")
	}
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "step_installation() {"); !strings.Contains(body, `case "$NAME" in`) || !strings.Contains(body, `"$NAME_WANTED") ;;`) {
		t.Errorf("scripts/live-checks/check96.sh's `step_installation` no longer refuses an installation that is not `$NAME_WANTED` (%q): a guard on the GCP project alone passes a second installation on fugaro-dev, and everything this check mints is minted with that installation's Firebase project, token signer and web API key", strings.TrimSpace(body))
	}

	// And `job` falls back to the shared project job for NO job of the
	// repository's own — never for several. A repository whose fugaro.yaml has
	// more than one workflow has one job per workflow, and the job a run lands on
	// is a question the launch itself refuses to answer without `--workflow`
	// (internal/cli/run.go's resolveWorkflow), so reading one of them (or the
	// shared project job) would judge `job-env`, `mint` and `pairing` against an
	// account and an environment that are not the repository's. The section says
	// which of the two it is, the shell stops on several, and the reader reports
	// the count it found rather than collapsing a several into a none.
	// Mutation (run and restored): the several-jobs stop folded back into the
	// `0` branch → test_a_repository_with_several_jobs_of_its_own_is_not_read_as_none
	// FAILs with the false `the sandbox repository acme/sandbox has NO job of its own`.
	if !strings.Contains(check, "A repository with **several** jobs of its own") {
		t.Errorf("docs/gcp-live-checklist.md's Check 96 no longer says a repository with several jobs of its own is neither read nor fallen back from: one job per workflow, and the row that reports one of them would report an account, an environment and a mint that are not the repository's")
	}
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "step_job() {"); !strings.Contains(body, `0)`) || !strings.Contains(body, `answer=$(reader projectjob`) || !strings.Contains(body, `stop "the sandbox repository $REPO has $found jobs of its own in $P/$REGION`) {
		t.Errorf("scripts/live-checks/check96.sh's `step_job` no longer tells no job of the repository's own from several (%q): the fallback to the installation's shared project job is for none, and a repository with several jobs has one per workflow, so falling back there reads the wrong account, the wrong environment and the wrong mint", strings.TrimSpace(body))
	}
	if body := check96PythonFunc(t, readRepoFile(t, check96Script), "def cmd_jobs():"); !strings.Contains(body, `print("found=%d" % len(found))`) {
		t.Errorf("scripts/live-checks/check96.sh's `cmd_jobs` no longer reports how many jobs of its own the repository has (%q): without the count the shell cannot tell a repository with none from one with several, and the two are the fallback and the stop", strings.TrimSpace(body))
	}

	// The URL gate is held in BOTH directions, which is what the section's
	// expectation below says: the variable is what opens the github provider
	// keyless, so a repository job that carries it while the config in force
	// records the repository as NOT switched on — or names no credentials
	// service at all — is the job and the record having moved apart. A row that
	// tested only the switched-on direction PASSed that state while its own
	// sentence said the job "mints in-job exactly as before", which is false of
	// a job that carries the variable.
	// Mutation (run and restored): either FAIL folded back into its PASS branch
	// (`if [ "$url" = NEVERMATCHES ]`, which leaves both sentences in the
	// function and only makes them unreachable) → this test FAILs on the pin,
	// and the two cases below FAIL with the row PASSing a job that mints at the
	// service.
	if body := check96ShellFunc(t, readRepoFile(t, check96Script), "step_job_env() {"); !strings.Contains(body, "present ]; then\n    fact job-env FAIL \"the config in force records $REPO's root as NOT") || !strings.Contains(body, "present ]; then\n      fact job-env FAIL \"the config in force records no credentials service") || !strings.Contains(body, "does not carry FUGARO_CREDENTIALS_SERVICE_URL") {
		t.Errorf("scripts/live-checks/check96.sh's `step_job_env` no longer holds the URL to the config in both directions (%q): the section's expectation is the variable present on a switched-on repository's own job and absent on every other, and a row that only tested the switched-on direction would PASS a job that mints at the service while the config in force records the repository as not switched on", strings.TrimSpace(body))
	}

	// The wiring, held to the code as well: the keyless provider is opened only
	// when the job's environment names the service, and the launch is what puts
	// that name on a repository job — so the check's expected answers are read
	// from the gate and from the launcher both.
	providers := readDoc(t, "internal/gitprov/providers/providers.go")
	if !strings.Contains(providers, "if vars[EnvCredentialsServiceURL] != \"\"") {
		t.Errorf("internal/gitprov/providers/providers.go no longer gates the keyless github provider on `EnvCredentialsServiceURL`: Check 96 states that gate as the switch the launch sets, and that is the name it carries")
	}
	run := readDoc(t, "internal/cli/run.go")
	if !strings.Contains(run, "func credentialsServiceLaunchEnv(") {
		t.Errorf("internal/cli/run.go no longer has `credentialsServiceLaunchEnv`: Check 96 states the launch as what puts `FUGARO_CREDENTIALS_SERVICE_URL` on a repository job's execution, and that helper is the mechanism")
	}
	if !strings.Contains(check, "**What to expect on a switched-on repository is therefore `FUGARO_CREDENTIALS_SERVICE_URL` present on a per-repository job's execution and the App key not read in-job; on a shared project job, or on any job of a repository whose root has not been re-applied with the switch on, the variable is absent**") {
		t.Errorf("Check 96 does not state what to expect about the URL gate: the keyless path needs FUGARO_CREDENTIALS_SERVICE_URL, the launch sets it on a repository job, and the check the owner runs must say so per job — present there, absent on a shared project job and on a repository whose root has not been re-applied with the switch on")
	}
	// And the retired half of that sentence, which said no job ever carried the
	// name: banned rather than merely unpinned.
	for _, stale := range []string{
		"Nothing on this tree puts that variable in a job's environment",
		"the expected answer today is `false`",
		"the wiring that puts the name on a job is the release after this one",
	} {
		if strings.Contains(check, stale) {
			t.Errorf("Check 96 still says %q: internal/cli/run.go's credentialsServiceLaunchEnv sets `FUGARO_CREDENTIALS_SERVICE_URL` on a switched-on repository's job as of this release, so the variable is expected present there and the check would mislead the owner about the state it is reading", stale)
		}
	}

	// The first thing that is not true on this tree, held to the code: while the
	// held-secrets list is empty the job keeps the key, and the check has to say
	// so; the day the list names it, the same sentence is the one that has to go.
	const notLanded = "**on this tree that list is empty**"
	const stillHolds = "is answered **yes, it still does**"
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
	// TestCredentialsServiceEnvIsTheDeploymentsOwn is what holds that pair — and,
	// since 0.10.1, its account holds ITS OWN database role: the custom role
	// fugaroCredentialsDatabase, the database's two data permissions, bound on
	// the Firebase project the database lives in. So every database read a call
	// makes is admitted — the first of them the identity verification's own read
	// of the run's write-once outcome (auth.go's checkLive, reached from
	// Auth.Verify), not the operator's stop switches — and the check has to say
	// that instead of the refusal that was lifted.
	//
	// The claim is read against the module's own role lines, in both directions:
	// the day the credentials account is granted a database role the refusal
	// sentence is the one that has to go, and the day the role is removed the
	// sentence that says the reads are admitted is the one that has to. It reads
	// credentials.tf, the file that creates the account and its grants; a
	// database role added to the account from another module would not be seen
	// by this line, and that is a review question rather than something a string
	// match can answer.
	const refusedEveryCall = "**The service is configured from its own deployment now, and refuses every call.**"
	const readsTheDatabase = "**The service is configured from its own deployment now, and reads the installation's database.**"
	hasRole := credentialsDatabaseRoleRE.MatchString(
		stripLineComments(readDoc(t, "deploy/terraform/gcp/modules/installation/credentials.tf")))
	if hasRole {
		if strings.Contains(check, refusedEveryCall) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account a database role (%s) — every database read a call makes is admitted — and Check 96 still says every call is refused: the gap was closed and this check did not notice", tf.CredentialsDatabaseRole)
		}
		if !strings.Contains(check, readsTheDatabase) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account its own database role (%s, the two data permissions) now, so every database read a call makes — the run's outcome first among them — is admitted and step 4's pairing refusal (a) is observable, and Check 96 does not say so", tf.CredentialsDatabaseRole)
		}
	} else {
		if !strings.Contains(check, refusedEveryCall) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account no role on the installation's database, so the first read a call makes — the identity verification's own read of the run's outcome (auth.go's checkLive) — is denied and the call is refused `state_unavailable` rather than served, and Check 96 does not say so: that is what decides whether the mint of step 3 can be made at all")
		}
		if strings.Contains(check, readsTheDatabase) {
			t.Errorf("deploy/terraform/gcp/modules/installation/credentials.tf grants the credentials service's account no database role and Check 96 says it reads the installation's database: a reader would take step 3's mint — and step 4's pairing refusal — as calls this tree can make")
		}
	}

	// And the order that sentence rests on, in both directions: the check has to
	// name the identity verification's read as the first database read of a call
	// and must not put the stop switches there. Three reviews of this section
	// found the stop switches named as the first read; the code has never read it
	// that way — auth.go's checkLive is what Auth.Verify reaches, and
	// server.go's Kills.Stop is asked after ServeHTTP has a whole identity in
	// hand — so a check that put them first would have reported a mechanism no
	// release has.
	if !strings.Contains(check, "the first one is **not** the stop switches but the identity verification's own read of the run's write-once outcome") {
		t.Errorf("Check 96 no longer states the first database read of a call as the identity verification's own read of the run's outcome: that order is what the check's account of `state_unavailable` rests on, and it is read out of internal/credsvc/auth.go's checkLive")
	}
	// Step 4's two halves do not stand or fall together, and the check has to
	// say which is which. (a) is the pairing refusal: it is asked only after
	// Auth.Verify returned a whole identity, and on this tree that verification
	// is stopped by the denied database read, so a deployed service never
	// reaches it. (b) is the caller that is no job account: auth.go's verifyJob
	// refuses it out of the account table the container was configured with,
	// before any database read, so this tree answers it 403
	// unverified_job_identity exactly as the target release does. The section
	// said both were `state_unavailable`; with the account holding its own
	// database role, the identity verification completes and it says both are
	// observable — which half is refused where is still the point, so the
	// sentence that names the mechanism stays pinned either way.
	if !strings.Contains(check, "**(a) and (b) are both observable**") {
		t.Errorf("Check 96 no longer says step 4's two halves are both observable on this tree: the pairing question is asked only after Auth.Verify returned a whole identity, and credentials.tf grants the service's account its own database role, so that verification completes and the pairing refusal is answered rather than stopped by a denied database read")
	}
	if !strings.Contains(check, "verifyJob` refuses a caller that is no job account out of the account table") {
		t.Errorf("Check 96 no longer says why step 4's (b) is observable, which is what the code does: verifyJob reads the account table out of the configuration (internal/credsvc/auth.go, the first thing Auth.Verify does) and refuses a caller that is no job account before any database read, so the refusal the check's step 4 (b) looks for is produced here")
	}
	for _, notWant := range []string{
		"step 4's two refusals be observed",
		"answers both (a) and (b) `state_unavailable`",
		"step 4's two refusals are unreachable",
		"nor can step 4's pairing refusal (a) be observed",
		"and of step 4's two refusals",
	} {
		if strings.Contains(check, notWant) {
			t.Errorf("Check 96 still carries %q: the two halves of step 4 do not fail together — (a) needs a verified identity, and this tree's service verifies one now that its account holds a database role, while (b) is refused out of the configured account table before any read — so a sentence that lumps them or denies (a) is false about one of them", notWant)
		}
	}
	if body := check96FuncBody(t, readDoc(t, "internal/credsvc/server.go"), "func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)"); !strings.Contains(body, "id, err := s.cfg.Auth.Verify(ctx, req)") || !strings.Contains(body, "s.cfg.Kills.Stop(ctx, id.Slug)") {
		t.Errorf("internal/credsvc/server.go's ServeHTTP no longer verifies the identities before it reads the stop switches (%q): Check 96 states that order — the identity verification's own database read comes first — and that is what it is read out of", strings.TrimSpace(body))
	} else if strings.Index(body, "s.cfg.Auth.Verify(ctx, req)") > strings.Index(body, "s.cfg.Kills.Stop(ctx, id.Slug)") {
		t.Errorf("internal/credsvc/server.go's ServeHTTP now reads the stop switches before it verifies the identities: Check 96 states the opposite order (the identity verification's database read is the first read of a call), so the check and the code have moved apart")
	}
	// The same order inside the verifier: checkLive's outcome read is what
	// Auth.Verify reaches, and it is a database read (RTDBOutcomes.OutcomeExists).
	authSrc := readDoc(t, "internal/credsvc/auth.go")
	if !strings.Contains(authSrc, "if err := a.checkLive(ctx, slug, run, now); err != nil {") {
		t.Errorf("internal/credsvc/auth.go's verifyRun no longer calls checkLive: Check 96 states the identity verification's own database read as the first read of a call, and that call is the read")
	}
	if !strings.Contains(authSrc, "a.cfg.Outcomes.OutcomeExists(ctx, day, slug, run)") {
		t.Errorf("internal/credsvc/auth.go's checkLive no longer reads the run's outcome through Outcomes.OutcomeExists: Check 96 states that read as the first database read a call makes")
	}
	// And the half of the same sentence that names the configuration: the check
	// states it as the deployment's own seven names, so the list it names has to
	// be there.
	if !strings.Contains(readDoc(t, "internal/cli/credentials_serve.go"), "credentialsServiceEnvNames") {
		t.Errorf("Check 96 states the service's configuration as the seven names its deployment sets (`credentialsServiceEnvNames`), and internal/cli/credentials_serve.go no longer has that list")
	}
}

// credentialsDatabaseRoleRE matches the credentials service's database role in
// credentials.tf: the shape the guard above reads. Two spellings, because there
// are two ways to give that account a database role.
//
//   - the custom role 0.10.1 adds: role_id = "fugaroCredentialsDatabase" (the
//     ID is read from internal/infra/tf's own constant, so a rename cannot
//     leave this guard watching a name nothing writes any more), declared in
//     the installation module and bound to the service's own account;
//   - a predefined database role granted directly (role = "roles/firebasedatabase..."),
//     which is what this guard saw first and all it saw: that line alone is the
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

// check96ShellFunc returns one shell function's body out of scripts/live-checks/
// check96.sh: from its signature to the `}` that closes it at column 0.
//
// check96FuncBody below splits on a top-level `\nfunc `, which is the Go shape
// and not this file's: pointed at the script it returned everything below the
// signature, so a pin meant to hold one step in place was satisfied anywhere in
// the rest of the file, and a refactor that moved the body read out of
// `step_front_end` would have kept passing.
func check96ShellFunc(t *testing.T, script, sig string) string {
	t.Helper()
	i := strings.Index(script, sig)
	if i < 0 {
		t.Fatalf("%s: %s not found — this test's marker has moved", check96Script, sig)
	}
	rest := script[i+len(sig):]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("%s: %s is not closed by a `}` at column 0 — this test's marker has moved", check96Script, sig)
	}
	return rest[:j]
}

// check96PythonFunc returns one function's body out of the reader embedded in
// scripts/live-checks/check96.sh: from its `def` line to the next `def ` at
// column 0. The reader is Python, so check96ShellFunc's `\n}\n` is not in it and
// check96FuncBody's `\nfunc ` is not either.
func check96PythonFunc(t *testing.T, script, sig string) string {
	t.Helper()
	i := strings.Index(script, sig)
	if i < 0 {
		t.Fatalf("%s: %s not found — this test's marker has moved", check96Script, sig)
	}
	rest := script[i+len(sig):]
	if j := strings.Index(rest, "\ndef "); j >= 0 {
		return rest[:j]
	}
	return rest
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
// Mutation (run and restored): Check 96's step 3 put back to
// `printf '%s' "$ANSWER" | python3 - "$CHK96_SANDBOX_REPO" <<'PY'` — the argv
// form the first regex missed → FAIL, which is the widening this test's regex
// was extended for.
// TestCheck96RegisterNamesEveryBlockKey: the register's "Reading the `CHECK 96
// RESULT` block:" paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the
// script's own BLOCK_KEYS rather than a paraphrase of it. The paragraph, and
// not the whole section, is the check: several of these keys are named again in
// the prose above (`mint`, `pairing` and `app-key` are the claims themselves),
// and a key the paragraph leaves out reads as a key the block was not meant to
// print — `restore`, `survivors` and `xtrace` are the three whose absence would
// leave a reader taking a run that left a user behind, or one whose shell was
// tracing, as a complete result.
//
// The script is also held to the harness that runs it: a script whose test is
// not wired into scripts/live_checks_test.go is a script nothing checks, and
// Check 96's test is the only thing that runs a step of it.
//
// Mutations (verified, run and restored): take `secret-scan` out of the
// paragraph's parenthetical — the key a run that leaked would print, and the
// one a reader has to be able to place; take `xtrace` out of the key list, and
// the paragraph no longer names a key BLOCK_KEYS prints; delete the
// `{"check96.sh", "check96_test.sh"}` line from scripts/live_checks_test.go —
// red on the wiring.
func TestCheck96RegisterNamesEveryBlockKey(t *testing.T) {
	script := readRepoFile(t, check96Script)
	keys := blockKeys(t, script)
	// A parse that reads nothing (or reads the comment above the array) would
	// make this test vacuous.
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check96Script, len(keys), keys)
	}
	sec := check96Section(t)
	const heading = "**Reading the `CHECK 96 RESULT` block:**"
	i := strings.Index(sec, heading)
	if i < 0 {
		t.Fatalf("docs/gcp-live-checklist.md: the Check 96 section has no %q paragraph", heading)
	}
	para := sec[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("docs/gcp-live-checklist.md's Check 96 %q paragraph does not name the block key `%s`, which check96.sh's BLOCK_KEYS prints", heading, k)
		}
	}
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check96.sh", "check96_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run %s's own test: the owner-run check's only automated proof is that suite", check96Script)
	}
}

func TestDocsChecklistHasNoPipeIntoPythonHeredoc(t *testing.T) {
	register := readDoc(t, "docs/gcp-live-checklist.md")
	if m := pipeIntoPythonHeredocRE.FindString(register); m != "" {
		t.Errorf("docs/gcp-live-checklist.md pipes a document into a python heredoc (%q): bash gives the heredoc stdin, so the script reads EOF instead of the document and the step prints none of its facts. Pass the document as a file or an argv argument (the form the register's other heredocs use), or use `python3 -c`.", m)
	}
	// The same rule over Check 96's shell, which is where its blocks live now:
	// the defect this test was extended for was in the register's step text and
	// the step text moved into scripts/live-checks/check96.sh, so a rule that
	// still read only the register would be watching the document the defect
	// left rather than the script it landed in.
	if m := pipeIntoPythonHeredocRE.FindString(readRepoFile(t, check96Script)); m != "" {
		t.Errorf("%s pipes a document into a python heredoc (%q): bash gives the heredoc stdin, so the program reads EOF instead of the document. Check 96 reads every document through its own reader with the document on the program's stdin (`python3 \"$SCRATCH/reader.py\" <file`), which is the form that works.", check96Script, m)
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
	// The section is prose plus the one command now: the five step blocks and
	// their guard block were replaced by `**Run it:**`, and the shell that used
	// to be in the register is the script. What is left in the section is the
	// command itself, and the rules below run over it as before.
	if len(blocks) < 1 {
		t.Fatalf("Check 96 has %d ```bash blocks; the section has to carry at least the one command it tells the owner to run", len(blocks))
	}
	// The script's own shell is held to the same three rules: it is the artifact
	// that runs now, so its heredocs are the ones that decide whether a read
	// happens at all. The whole file is one block for that purpose.
	script := readRepoFile(t, check96Script)
	if !strings.Contains(script, "<<'READER_EOF'") {
		t.Errorf("%s no longer carries a `<<'READER_EOF'` heredoc: Check 96's shell is held here for the heredoc rules, and a script whose reads moved to another file would pass them vacuously", check96Script)
	}
	blocks = append(blocks, strings.Split(strings.TrimRight(script, "\n"), "\n"))

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
