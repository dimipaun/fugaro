package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestChecklistHasCheck52: docs/gcp-live-checklist.md must carry the UNVERIFIED
// questions phase 3 depends on (design project-centric.md §13 and §4.5; plan
// 2026-10-14-project-centric.md Task 3.0) as one owner-run, not-yet-run,
// sandbox-only check numbered 52 — with its row in the check list and Check
// 43's guard block (the three hard stops and the project pinning) inside its
// section, so a reader cannot mistake it for something CI covers and no step
// of it can run against a project other than fugaro-dev.
//
// U1 is not a probe any more: the owner ruled it on 2026-10-15 (a Bitbucket
// credential cannot be scoped to one project's repositories), so the step is a
// record of that ruling with no commands, and the assertions below fail if the
// dead probe — its token, its scratch workspace, its projects and repositories
// — comes back.
//
// The owner runs this document against real accounts, so the assertions below
// are deliberately per-line: each is a fact the section must state (a stop it
// can take, a flag a command must carry, a FACT it records, a step of its
// Restore), and each of the hand mutations the review named fails one of them
// — deleting a flag, reintroducing a removed command, or dropping a safety
// stop fails here rather than passing on the section's heading alone.
//
// Two mutations of the row's wording were verified, run and restored: the
// status marker back to "**NOT RUN**" — the claim the review found false, since
// the owner ran the three probes on 2026-10-16 → FAIL, and the U3 answer
// weakened from "**yes, the condition is honoured**" to "**yes, the condition
// is expected**" → FAIL.
func TestChecklistHasCheck52(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	// The heading itself carries the four markers the plan's Task 3.0 fixes
	// ("installation fugaro, sandbox only, never belong; run by you") and the
	// run that answered the three probes, so it is asserted as a whole rather
	// than as substrings that would also pass on the heading alone. The
	// status marker is the run and not "NOT RUN": the owner ran U2, U3 and U4
	// on 2026-10-16, and a heading that read "NOT RUN" would be the same false
	// claim this test was rewritten to catch.
	const heading = "## Check 52: the four probes phase 3 depends on (installation fugaro, sandbox only, never belong; run by you; RUN 2026-10-16: U2 accepted, U3 honoured, U4 no quota — see the result at the end of the section)"
	section := docSection(t, path, doc, heading)
	// The section's body, with the heading line stripped: the constraints must
	// live in the sentences that carry them, not only in the title.
	body := section
	if i := strings.Index(body, "\n"); i != -1 {
		body = body[i+1:]
	}
	chk52MustContain(t, body, "body", []string{
		"no agent creates a credential, no agent runs a live step",
		"no step of this check creates a token for the `edgeappinc` workspace or probes any repository that is not a scratch repository created for the probe",
		"you run every command",
		"**Never `belong`**",
		"Never run this against `belong`, never create a `edgeappinc` workspace token",
	})

	// Its row in the check list (the summary table), not only the section.
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 52 |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("the check list has no Check 52 row")
	}
	chk52MustContain(t, row, "row", []string{
		// Three override executions can run, and the row names exactly the three:
		// (a)'s reference call, (c)'s literal comparison, and the length-only run
		// when the container's command is a shell. (b) is a template-level
		// `--update-secrets` update and the bare `gcloud run jobs execute` carries
		// no override, so neither is one — "two" undercounted the three and "four"
		// counted a call that is not an override.
		"**three** override executions on the sandbox job",
		"the bare `gcloud run jobs execute` carries no override at all",
		"fugaro-chk52-probe",
		"fugaro-probe-in",
		"fugaro-other-probe",
		// U1 is answered by ruling: the row must say so, and must not promise a
		// Bitbucket credential, project or scratch repository.
		"no longer a probe",
		"nothing on the Bitbucket side",
		// And the row must carry the run that answered it, with the three
		// answers and not the question again. It said "the UNVERIFIED questions
		// phase 3 depends on, written and answered before any phase 3 code" —
		// a sentence an owner reads as a result — and this test used to pin it
		// to "**NOT RUN**", which was true when it was written and false from
		// 2026-10-16, when the owner ran U2, U3 and U4. The plan's own words
		// are "they are answered before any phase 3 code is written" — a
		// requirement — and the row now says which run answered them, with the
		// three results, in the same breath as that requirement.
		"written and **RUN 2026-10-16**",
		"before any phase 3 code was written",
		"**U2**",
		"**accepted, the reference works**",
		"**U3**",
		"**yes, the condition is honoured**",
		"**U4**",
		"**no service-account quota**",
	})
	// The section's own record of the run, which is what every other document
	// cites rather than restating the question as open: the block, its date and
	// one line per probe, each carrying that probe's answer.
	chk52MustContain(t, body, "body", []string{
		"### Result of the 2026-10-16 run",
		"**U2 — accepted.**",
		"**U3 — yes, the condition is honoured.**",
		"**U4 — no service-account quota.**",
	})
	// A statement that the check has not run is the false claim the review
	// found in four files; in this document the row and the section are where
	// it would come back, so neither may carry it.
	for what, text := range map[string]string{"row": row, "section": section} {
		if strings.Contains(text, "NOT RUN") {
			t.Errorf("docs/gcp-live-checklist.md's Check 52 %s says NOT RUN: the owner ran the three probes on 2026-10-16 (U2 accepted, U3 honoured, U4 no quota), and the result is recorded at the end of the section", what)
		}
	}
	// And the design that rests its Secret Manager half on U3 must carry the
	// same answer: it is one of the places the review found saying the check
	// had never run. The two phrases are asserted separately because the
	// design wraps the line between them.
	if design, err := os.ReadFile("../../docs/design/managed-deployer-fence.md"); err == nil {
		for _, want := range []string{"RUN 2026-10-16 and answered yes", "the condition is honoured"} {
			if !strings.Contains(string(design), want) {
				t.Errorf("docs/design/managed-deployer-fence.md no longer records Check 52's U3 as %q: the design's Secret Manager half rests on that answer, and the review found this file saying the check had never run", want)
			}
		}
	} else {
		t.Fatal(err)
	}
	for _, dead := range []string{"scratch Bitbucket", "project access token", "fugaro-probe-1"} {
		if strings.Contains(row, dead) {
			t.Errorf("the Check 52 row still describes U1 as a running probe (%q)", dead)
		}
	}
	// The row's count of override executions, both halves: the miscounts are
	// what a reader plans the check's blast radius from, and neither the
	// undercount ("two") nor the overcount ("four") names a call the section
	// actually sends.
	if strings.Contains(row, "four override executions") || strings.Contains(row, "two override executions") {
		t.Error("the Check 52 row miscounts the override executions: the section sends three (the reference call, the literal comparison and the length-only run)")
	}

	// Check 43's guard block inside the section — the three hard stops and
	// the project pinning, not merely a ${P:?...}.
	chk52MustContain(t, section, "section", []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] \`,
		`|| { echo "Check 52 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 52 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 52 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
	})

	// U1 is dead as a step: the section must record the owner's ruling, must
	// carry no command that creates or reads a credential, and must name no
	// Bitbucket repository at all. Every one of these fails if the probe comes
	// back.
	chk52MustContain(t, section, "section", []string{
		"### 2. U1 — answered by the owner's ruling; no probe",
		"**OWNER RULING, 2026-10-15 — \"probe U1: cannot be scoped\".**",
		"Bitbucket stays on the per-repository path",
		// The ruling's consequences, which the plan carries in the same words
		// (Task 3.0 step 2, the §13 map and "Accepted as deferred"): Bitbucket
		// has no project-wide credential, so a shared job has nothing to hold
		// for it, the phase's shared jobs are GitHub's, and the one step the
		// ruling lands in is Task 3.7's live check. The section used to say
		// "D5 stays exactly as the design already has it" while the design's
		// D5 row still read "First-class in phase 3, via a project-wide
		// Bitbucket access token" — the opposite of the ruling, and the reason
		// the plan's design-amendments list now owes a D5 amendment.
		"**Phase 3's shared project jobs serve GitHub repositories.**",
		"plan Tasks 3.1 to 3.7 proceed as written",
		"Check 53 runs the GitHub path and its FACT says so",
		"**D5 no longer reads \"first-class in phase 3\", and the design is amended to record that.**",
		"the plan carries the amendment that puts the ruling in both",
		"**This step has no commands, and it creates and probes nothing.**",
		"**FACT:** the ruling in the owner's own words, with its date",
		"no credential, project or repository was created for U1",
	})
	var u1 string
	if i := strings.Index(section, "### 2. U1 —"); i != -1 {
		j := strings.Index(section[i:], "### 3. U2 —")
		u1 = section[i : i+j]
	} else {
		t.Fatal("the Check 52 section has no U1 step")
	}
	for _, dead := range []string{
		"PROBE_WS", "OUT_WS", "PROBE_PROJECT", "OUT_PROJECT", // the scratch scope's variables
		"PROBE_TOKEN", "GIT_ASKPASS", "credential.helper", // the credential path
		"bitbucket.org", "git remote", "ls-remote", "security find-internet-password",
		// The claim that the design needs no amendment, which is false while
		// its D5 row reads "First-class in phase 3, via a project-wide
		// Bitbucket access token": the plan owes a D5 amendment (Task 0.5).
		"D5 stays exactly as the design already has it",
	} {
		if strings.Contains(u1, dead) {
			t.Errorf("the Check 52 U1 step still carries the dead probe (%q): it is a record of the ruling, with no commands", dead)
		}
	}
	// And no command anywhere in the section is a git command: the check reads
	// and writes no repository, so the whole credential-helper machinery
	// (K1) is gone with the probe it protected.
	for _, line := range sectionCommands(section) {
		// The guard's own origin assertion is the one git command left, and it
		// is one of the three hard stops: it reads the sandbox checkout's
		// remote, not a repository this check would touch.
		if strings.HasPrefix(line, "git ") && !strings.HasPrefix(line, "git remote get-url origin") {
			t.Errorf("the Check 52 section still runs a git command, and no step touches a repository any more: %q", line)
		}
	}
	for _, dead := range []string{
		"credential.helper", "git credential fill", "ASKPASS", "PROBE_TOKEN", "bitbucket.org",
		"scratch workspace", "project access token",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 52 section still carries the dead U1 credential machinery (%q)", dead)
		}
	}

	// I-b: the prompt works in bash and zsh alike; `read -p` is bash-only. The
	// token prompt is gone with U1 — the throwaway secret value is the only
	// thing this check ever types, and it is never echoed.
	chk52MustContain(t, section, "section", []string{
		`read -rs THROWAWAY`,
		`: "${THROWAWAY:?a throwaway value you typed}"`,
	})
	if strings.Contains(section, "read -p ") {
		t.Error("the Check 52 section prompts with read -p, which is bash-only (zsh: no coprocess)")
	}

	// Each throwaway secret is created only if it is the one that is gone
	// (review 5): the NEED test above it decides whether to *prompt*, and a
	// re-run after a partial cleanup — a failed or interrupted run leaves one
	// of the two deleted and the other in place — would stop with
	// ALREADY_EXISTS on the survivor and never reach step 1. So every create
	// is introduced by its own describe and `||`.
	chk52MustContain(t, section, "section", []string{
		`gcloud secrets describe "${PROBE_IN:?}" --project="${P:?}" >/dev/null 2>&1 \`,
		`gcloud secrets describe "${PROBE_OUT:?}" --project="${P:?}" >/dev/null 2>&1 \`,
		`|| printf '%s' "${THROWAWAY:?}" | gcloud secrets create "${PROBE_IN:?}"  --project="${P:?}" --data-file=-`,
		`|| printf '%s' "${THROWAWAY:?}" | gcloud secrets create "${PROBE_OUT:?}" --project="${P:?}" --data-file=-`,
	})
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `printf '%s' "${THROWAWAY:?}" | gcloud secrets create`) {
			t.Errorf("the Check 52 section creates a probe secret unconditionally, so a re-run after a partial cleanup stops on ALREADY_EXISTS: %q", strings.TrimSpace(line))
		}
	}

	// I-a: the baseline is recorded once and never overwritten, or Restore's
	// diff compares the job with itself.
	chk52MustContain(t, section, "section", []string{
		"already recorded, not overwriting",
		`[ -e "${CHK52_TMP:?}/job-before.json" ]`,
	})

	// I-d: IAM propagation, and the API the impersonated reads need.
	chk52MustContain(t, section, "section", []string{
		`gcloud services enable iamcredentials.googleapis.com --project="${P:?}"`,
		`for i in $(seq 1 12); do`,
		`sleep 10`,
		`if [ "$INSIDE" = ok ]; then`,
	})

	// I-e: one FACT line per step, each pinned by its own sentence.
	chk52MustContain(t, section, "section", []string{
		"**FACT:** the recorded job env (names, never values)",
		"**FACT:** the ruling in the owner's own words, with its date",
		"**FACT:** whether the execution-level override is accepted and whether the referenced value arrives (the length, never the value)",
		"If (a) is refused, the API's own error (its status, code and message) is the FACT",
		"**FACT:** the two exit statuses (`inside` after the poll window, `outside` only once `inside` was `ok`)",
		"**which condition was in place when the inside read succeeded** — `with-type` or `no-type`",
		"**FACT:** the sandbox project's current service-account count and the quota it is counted against **as the API reports it**",
	})

	// The Restore: every artifact the check created is named with the command
	// that removes it. A Restore that stops after the first command leaves the
	// conditional grant, the probe account, a probe secret or a mode-700
	// directory of the job's plain env values behind.
	chk52MustContain(t, section, "section", []string{
		`gcloud run jobs describe "${SANDBOX_JOB:?}" --project="${P:?}" --region="${L:?}" --format=json > "${CHK52_TMP:?}/job-after.json"`,
		`diff <(jq -S '.spec.template' "${CHK52_TMP:?}/job-before.json") <(jq -S '.spec.template' "${CHK52_TMP:?}/job-after.json")`,
		`gcloud run jobs update "${SANDBOX_JOB:?}" --project="${P:?}" --region="${L:?}" --remove-secrets=PROBE_REF`,
		`gcloud projects remove-iam-policy-binding "${P:?}"`,
		`--condition="${PROBE_COND:?}"`,
		`gcloud iam service-accounts remove-iam-policy-binding "${PROBE_SA:?}" --project="${P:?}"`,
		`--role="roles/iam.serviceAccountTokenCreator"`,
		`gcloud secrets delete "${PROBE_IN:?}" --project="${P:?}" --quiet`,
		`gcloud secrets delete "${PROBE_OUT:?}" --project="${P:?}" --quiet`,
		`gcloud iam service-accounts delete "${PROBE_SA:?}" --project="${P:?}" --quiet`,
		`rm -rf "${CHK52_TMP:?}"`,
		// The conditional grant is removed with whichever condition is bound:
		// step 4's fallback may have replaced the type term's.
		`--condition="${PROBE_COND_NOTYPE:?}"`,
		// Nothing to restore on the Bitbucket side, and the section must say so
		// rather than list a cleanup that never happens.
		"**And there is nothing to restore on the Bitbucket side.**",
		"there is no scratch repository to delete, no project to remove and no token to revoke",
		// The evidence is the plan-only run, not the template diff alone.
		`fugaro update --repo . --plan-only --project fugaro`,
		"**FACT:** that the second diff is empty **and** that the plan-only run reports nothing to change",
	})
	// The Restore's job check happens twice: once against the drift U2(b)
	// added, once after --remove-secrets. With only the first pair the job is
	// never compared with step 1's baseline again.
	// The diff is over the template, never the whole describe: the two `run
	// jobs update` calls bump metadata.generation and the status conditions, so
	// a whole-object diff can never come out empty and the second one — the
	// Restore's evidence — would always report drift.
	if n := strings.Count(section, `diff <(jq -S '.spec.template' "${CHK52_TMP:?}/job-before.json") <(jq -S '.spec.template' "${CHK52_TMP:?}/job-after.json")`); n != 2 {
		t.Errorf("the Check 52 section diffs the job's template against step 1's baseline %d times, want 2 (before and after --remove-secrets)", n)
	}
	if strings.Contains(section, `diff "${CHK52_TMP:?}/job-before.json" "${CHK52_TMP:?}/job-after.json"`) {
		t.Error("the Check 52 section diffs the whole job JSON, which the updates' own generation bump makes non-empty even when the template is restored")
	}
	// Restore runs from a fresh shell, and the guard block defines only P and
	// L: the step's variables block is what defines SANDBOX_JOB, PROBE_SA,
	// PROBE_IN, PROBE_OUT, PROBE_COND and CHK52_TMP. Without re-running it the
	// first command aborts on ${SANDBOX_JOB:?} and nothing is restored.
	chk52MustContain(t, section, "section", []string{
		"**First re-run the guard block and the step's variables block, in that order**",
		"the step's variables block defines `SANDBOX_JOB`, `SANDBOX_JOB_SA`, `SANDBOX_BUCKET`, `NUMBER`, `PROBE_SA`, `PROBE_IN`, `PROBE_OUT`, `PROBE_COND`, `PROBE_COND_NOTYPE` and `CHK52_TMP`",
		"**Then the job**",
	})
	// The two blocks come before the job, not after it (review 4): the job's
	// first command aborts on ${SANDBOX_JOB:?} until they have run, so a Restore
	// that sends the reader to the job first restores nothing. The old order —
	// "The job first ... Then re-run the guard block" — fails here.
	if i, j := strings.Index(section, "**First re-run the guard block and the step's variables block, in that order**"),
		strings.Index(section, "**Then the job**"); i == -1 || j == -1 || i > j {
		t.Error("the Check 52 section's Restore names the job before the guard block and the step's variables block, which the job's first command needs: the job cannot come first")
	}
	if strings.Contains(section, "**The job first**") {
		t.Error("the Check 52 section's Restore still puts the job before the blocks that define its variables")
	}
	// Restore runs from a fresh shell: OWNER is set in step 4's block only, so
	// the removal of the token-creator grant must default it here too.
	if !strings.Contains(section, `OWNER=${OWNER:-$(gcloud auth list`) {
		t.Error("the Check 52 section's Restore does not default OWNER for a fresh shell")
	}

	// U2: the execution-level override is the API's jobs.run, never a gcloud
	// flag; the execution's name comes from the run's own operation metadata,
	// never a racy --limit=1 listing; and the container override carries args
	// or clearArgs, never both. The checks are over the section's command
	// lines, since the prose says why those spellings are wrong.
	for _, line := range sectionCommands(section) {
		switch {
		case strings.Contains(line, "--overrides"):
			t.Errorf("the Check 52 section has a command using `gcloud run jobs execute --overrides`, which does not exist: %q", line)
		case strings.Contains(line, "--limit=1"):
			t.Errorf("the Check 52 section has a command naming the execution from a --limit=1 listing, which a concurrent run can race: %q", line)
		case strings.Contains(line, "clearArgs"):
			t.Errorf("the Check 52 section has a command setting clearArgs alongside args, which asks for two contradictory things: %q", line)
		}
	}
	if strings.Contains(section, `"clearArgs"`) {
		t.Error("the Check 52 section's JSON override body sets clearArgs alongside args")
	}
	chk52MustContain(t, section, "section", []string{
		`jobs/${SANDBOX_JOB:?}:run`,
		`OPERATION=$(jq -r '.name // empty' "${CHK52_TMP:?}/run-a.json")`,
		`jq -r '.metadata.name // empty'`,
		`internal/backend/gcp/run.go`,
		`"value":"plain-literal"`,
		// A refusal is an answer, not a stop: (a) branches on the API's error
		// so (b), (c), U3 and U4 still run, and nothing aborts on a missing
		// execution name.
		`if jq -e '.error' "${CHK52_TMP:?}/run-a.json" >/dev/null 2>&1; then`,
		`echo "U2(a): the API REFUSED the secret-reference override. Record this error as the FACT and go on:"`,
		`if [ -n "${EXEC:-}" ]; then`,
		// The refusal's status is inside .error (review 4): the API's error body
		// is {"error":{code,message,status}}, so a top-level `.status` prints
		// null and the FACT would record `"status": null` as the API's answer.
		`jq -c '{status: .error.status, code: .error.code, message: .error.message}' "${CHK52_TMP:?}/run-a.json"`,
		// (c)'s own response is read and printed too, not merely written: the
		// comparison's result — accepted or refused — was never shown, so the
		// FACT's "what the plain-literal comparison does" had no input.
		`if jq -e '.error' "${CHK52_TMP:?}/run-c.json" >/dev/null 2>&1; then`,
		`echo "U2(c): the same call with a plain literal value was REFUSED — record this error:"`,
		`jq -c '{status: .error.status, code: .error.code, message: .error.message}' "${CHK52_TMP:?}/run-c.json"`,
		`jq -c '{name, done}' "${CHK52_TMP:?}/run-c.json"`,
		// The executions these override calls start fail by design: the image's
		// own CMD is `fugaro exec`, and the run it executes is named by
		// FUGARO_RUN, which only the launcher's override sets. Without the
		// sentence, a reader takes the failed execution for the probe's answer.
		"An execution these override calls start is expected to end as a *failed* run, and that is not the probe's answer",
		"the base image's `CMD [\"fugaro\", \"exec\"]` (`images/base/Dockerfile:293`)",
		"the failure of these executions' own tasks recorded as the expected outcome, not as the answer",
	})

	// The section's code citations, resolved against the tree by the SYMBOL
	// they name: the response's Operation metadata is read in
	// internal/backend/gcp/run.go, and a section that pins a line instead is
	// one merge away from sending a reader to code that is not the field the
	// step reads back.
	checkCitations(t, section, "Check 52 section", chk52Citations)
	chkNoLineCites(t, section, "Check 52 section")
	if strings.Contains(section, `jq -c '{status, code:`) {
		t.Error("the Check 52 section prints the refusal's status from the response's top level, where it is null: the API's error body carries it inside .error")
	}
	if strings.Contains(section, `: "${EXEC:?`) {
		t.Error("the Check 52 section aborts when (a)'s operation reports no execution name: a refused override is the likely answer, and its error is the FACT, not a stop")
	}
	// (a)'s poll is guarded on the operation name (review 5): a response that
	// is neither an error body nor an operation — a curl that never reached the
	// API, a body jq cannot parse — leaves OPERATION empty, and `${OPERATION:?}`
	// in the poll would stop the whole shell, so (b), (c), U3 and U4 would
	// never run and the FACT would be missing most of its steps. The test used
	// to pin only the `${EXEC:?}` sibling, which is why the gap survived: what
	// has to come first is the empty test, then the poll.
	if i, j := strings.Index(section, `if [ -z "$OPERATION" ]; then`), strings.Index(section, "${OPERATION:?"); i == -1 || j == -1 || i > j {
		t.Error("the Check 52 section polls with ${OPERATION:?...} without first testing the operation name for empty: a transport or parse failure would abort the shell and (b), (c), U3 and U4 would never run")
	}
	chk52MustContain(t, section, "section", []string{
		`if [ -z "$OPERATION" ]; then`,
		`echo "U2(a): the response carried no operation name — a transport or parse failure, not the API's answer. Record what came back, and go on:"`,
		`head -c 400 "${CHK52_TMP:?}/run-a.json"; echo`,
	})

	// The three read-backs are v1 JSON paths, because `gcloud run jobs describe`
	// and `gcloud run jobs executions describe` force the v1 API
	// (version_override='v1', surface/run/jobs/describe.py:78 and
	// surface/run/jobs/executions/describe.py:80 in gcloud 588.0.0): a v1 Job
	// nests the revision at spec.template.spec.template.spec, a v1 Execution at
	// spec.template.spec with no top-level `template`. The v2-shaped spellings
	// print '' and null, so SANDBOX_JOB_SA would stay empty and the override
	// read-back would record "not visible" — the section's own FACT input.
	chk52MustContain(t, section, "section", []string{
		`--format='value(spec.template.spec.template.spec.serviceAccountName)'`,
		`--format='value(spec.template.spec.template.spec.containers[0].command)'`,
		`--format=json | jq '.spec.template.spec.containers[0].env'`,
		// And the read-back's argument is the bare execution ID (review 4):
		// `gcloud run jobs executions describe` resolves it through the
		// `run.namespaces.executions` concept spec, which parses a bare ID and
		// never the full resource path EXEC holds, so the full name stops with
		// "Cannot find execution [...]" before any request.
		`gcloud run jobs executions describe "${EXEC##*/}" \`,
	})
	for _, full := range []string{
		`executions describe "${EXEC:?`,
		`executions describe "${EXEC}"`,
		`executions describe "${EXEC:-}`,
	} {
		if strings.Contains(section, full) {
			t.Errorf("the Check 52 section describes the execution by its full resource name (%q), which the v1 `executions describe` surface cannot parse: use ${EXEC##*/}", full)
		}
	}
	for _, v2 := range []string{
		`--format='value(template.template.serviceAccountName)'`,
		`--format='value(template.template.containers[0].command)'`,
		`jq '.template.containers[0].env'`,
	} {
		if strings.Contains(section, v2) {
			t.Errorf("the Check 52 section reads a v2 JSON path (%q) off the v1 resource gcloud prints, so it prints empty or null", v2)
		}
	}

	// U3: the condition names the project NUMBER (a project ID never matches),
	// never the ID; the reads are impersonated and send the payload to
	// /dev/null; and extracting the accessor grant before U2 is a step.
	if strings.Contains(section, "projects/${P:?}/secrets/") {
		t.Error("the Check 52 section writes the IAM condition with the project ID; Secret Manager resources are named by project NUMBER")
	}
	chk52MustContain(t, section, "section", []string{
		`PROBE_COND="expression=resource.name.startsWith('projects/${NUMBER:?}/secrets/fugaro-probe')`,
		// versions.access is evaluated on a SecretVersion, so a type term that
		// accepted only .../Secret would deny the inside read and record a
		// false "not honoured".
		`(resource.type == 'secretmanager.googleapis.com/Secret' || resource.type == 'secretmanager.googleapis.com/SecretVersion')`,
		`PROBE_COND_NOTYPE="expression=resource.name.startsWith('projects/${NUMBER:?}/secrets/fugaro-probe'),title=chk52-probe-prefix-notype"`,
		`COND_USED=with-type`,
		`--condition="${PROBE_COND_NOTYPE:?}"`,
		`gcloud secrets add-iam-policy-binding "${PROBE_IN:?}" --project="${P:?}"`,
		`--role="roles/secretmanager.secretAccessor"`,
	})
	if strings.Contains(section, `resource.type == 'secretmanager.googleapis.com/Secret',title`) {
		t.Error("the Check 52 section's condition accepts only Secret, not SecretVersion: versions.access is evaluated on a SecretVersion, so the inside read would be denied")
	}
	if n := strings.Count(section, `--impersonate-service-account="${PROBE_SA:?}"`); n < 2 {
		t.Errorf("the Check 52 section has %d impersonated secret reads, want at least 2 (inside and outside)", n)
	}
	if n := strings.Count(section, `--out-file=/dev/null`); n < 2 {
		t.Errorf("the Check 52 section sends %d secret payloads to /dev/null, want at least 2", n)
	}

	// U3's fallback is only run when the inside read was denied, and it says
	// which condition was in place: a denial under the type term must never be
	// recorded as the name prefix's.
	if !strings.Contains(section, `if [ "$INSIDE" != ok ]; then`) {
		t.Error("the Check 52 section has no fallback for a denied inside read, so a type term that does not match a SecretVersion would be recorded as the prefix term failing")
	}

	// The closing paragraph, which is what the owner reads last.
	chk52MustContain(t, section, "section", []string{
		"**Paste back:** every FACT line above, never a token, key or secret value.",
		"never point a probe at edgeserver or edgeweb**",
	})

	// Every command that acts on a project names it, and no gcloud/curl/git
	// command interpolates an unguarded `${VAR}`: an empty variable there is
	// an empty argument gcloud resolves from its ambient configuration.
	unguarded := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	for _, line := range sectionCommands(section) {
		if strings.HasPrefix(line, "gcloud ") &&
			!strings.Contains(line, `--project="${P:?}"`) &&
			!strings.Contains(line, `"${P:?}"`) &&
			!strings.Contains(line, `projects/${P:?}`) {
			t.Errorf("the Check 52 section has a gcloud command that does not name the project: %q", line)
		}
		if m := unguarded.FindString(line); m != "" {
			t.Errorf("the Check 52 section has an unguarded %s in a command: %q", m, line)
		}
	}

	// U3's inside read is written `if gcloud secrets versions access …`, so it
	// must be scanned like any other command: a collector that looked only at a
	// line's first word would let that one drop its --project unnoticed.
	var sawKeywordCommand bool
	for _, line := range sectionCommands(section) {
		if strings.HasPrefix(line, "gcloud secrets versions access") {
			sawKeywordCommand = true
		}
	}
	if !sawKeywordCommand {
		t.Error("the Check 52 section's `if gcloud secrets versions access …` reads are not read as commands, so nothing checks their --project or their variables")
	}

	// The access token for the REST calls comes from a command substitution
	// and is never stored.
	if n := strings.Count(section, `Bearer $(gcloud auth print-access-token)`); n < 3 {
		t.Errorf("the Check 52 section issues %d REST calls with a substituted access token, want at least 3", n)
	}
}

// chk52MustContain fails the test for each want that is not in got, naming the part
// of the section (its body, its table row, the whole section) that should hold
// it.
func chk52MustContain(t *testing.T, got, part string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the Check 52 section's %s is missing %q", part, w)
		}
	}
}

// stripCommandPrefix removes the shell keywords and operators a command can be
// introduced by, so that `if gcloud …; then` and `… && gcloud …` are read as
// gcloud commands: U3's inside read is an `if gcloud secrets versions access
// …`, and a check that only looked at a line's first word would let that one
// drop its --project or interpolate an unguarded variable unnoticed.
func stripCommandPrefix(line string) string {
	for {
		trimmed := line
		for _, p := range []string{"if ", "elif ", "then ", "else ", "do ", "! ", "&& ", "|| ", "; ", "| "} {
			if strings.HasPrefix(line, p) {
				trimmed = strings.TrimSpace(line[len(p):])
				break
			}
		}
		if trimmed == line {
			return line
		}
		line = trimmed
	}
}

// sectionCommands returns the section's `gcloud`, `curl` and `git` invocations
// as logical lines: backslash continuations are joined, leading shell keywords
// are stripped, and only lines that then start with one of those verbs count
// (so a command inside prose, a comment or a `$(…)` is not read as a command of
// its own).
func sectionCommands(section string) []string {
	return commandLines(section, "gcloud ", "curl ", "git ")
}

// fugaroCommands returns the section's `fugaro` invocations, read the same way
// sectionCommands reads the cloud ones. Check 49 is a Fugaro check — its steps
// are mostly `fugaro` commands — so its rule that every one of them names the
// installation has to be checked over them and not over the gcloud commands
// alone.
func fugaroCommands(section string) []string {
	return commandLines(section, "fugaro ")
}

// commandLines is sectionCommands and fugaroCommands' shared reader: the
// section's logical lines that begin with one of verbs, once backslash
// continuations are joined and the shell keywords a command can be introduced
// by are stripped.
func commandLines(section string, verbs ...string) []string {
	var joined []string
	var buf string
	for _, raw := range strings.Split(section, "\n") {
		trimmed := strings.TrimRight(raw, " \t")
		if strings.HasSuffix(trimmed, "\\") {
			buf += strings.TrimSuffix(trimmed, "\\") + " "
			continue
		}
		joined = append(joined, buf+trimmed)
		buf = ""
	}
	joined = append(joined, buf)
	var out []string
	for _, line := range joined {
		line = stripCommandPrefix(strings.TrimSpace(line))
		for _, verb := range verbs {
			if strings.HasPrefix(line, verb) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// TestChecklistHasCheck49: docs/gcp-live-checklist.md must carry the phase 1
// owner-run check (design project-centric.md §5; plan
// 2026-10-14-project-centric.md Task 1.7) as one owner-run, not-yet-run,
// installation-`fugaro`-only check numbered 49 — with its row in the check
// list and Check 43's guard block (the three hard stops and the project
// pinning) inside its section, so a reader cannot mistake it for something CI
// covers and no step of it can run against a project other than fugaro-dev.
//
// The owner runs this document against the live installation, so the
// assertions are per-line facts the section must state — a stop it can take, a
// flag a command must carry, a FACT it records, a step of its Restore — and
// each of the hand mutations the review named fails one of them rather than
// passing on the heading alone. Three of them are corrections the code forces
// on the plan's own wording, and each is asserted both ways:
//
//   - the plan names `fugaro upgrade --cloud`, which does not exist (there is
//     no --cloud flag; `cloud` is one of upgrade's steps), so the section must
//     name the real command;
//   - finding 15: only `fugaro update --repo` writes a job's environment, so the
//     runner rollout is two steps and not one;
//   - step 6 cannot point "the sandbox's fugaro.yaml at a repository": no key
//     of fugaro.yaml names a repository (internal/config/config.go), so the
//     un-enrolled state is made with the allow-list itself.
//
// Phase 3 ships in 0.9.0 (the owner's ruling of 2026-10-15), so the section
// must not depend on any phase 3 code; the one thing it may say about the
// binding's live proof is that it belongs to Check 53 step 6, which is a
// pointer to a later check and not a dependency on it.
//
// The review of #421 found four things a first pass at this section got wrong
// against the code, and each is now a fact the test holds the section to:
//
//   - a `fugaro run` with no task is refused at internal/cli/run.go:220, before
//     openCloud and long before the allow-list gate, so steps 3, 5, 6(a), 7
//     and 8 would never have produced the FACTs they claim — every one of them
//     carries the setup block's `TASK`, and every `fugaro run` line is checked
//     for it;
//   - a published budget.per_run_usd is the WHOLE project's ceiling and this
//     installation also runs the live dimipaun/fugaro dogfood, so the section
//     must confirm nothing is in flight, publish the number the job's
//     environment already carries, and record that the seeded allow-list still
//     admits github/dimipaun/fugaro;
//   - the Restore must put the operator's project config back BEFORE
//     `fugaro update --repo` reads the job environment out of it, must not run
//     `fugaro budget set` (no step changes the caps node), and must check the
//     .claude/settings.json `fugaro upgrade`'s pin step rewrites;
//   - the step 3 environment read prints NAMES and value LENGTHS, and values
//     only for the three variables that are not credentials — the review found
//     the first pass printing FUGARO_FIREBASE_API_KEY's value.
//
// Each step's FACT lines are pinned INDIVIDUALLY rather than counted: a total
// count passes when one step's line is deleted and another's is added, which
// is one of the hand mutations below.
//
// Hand mutations, run and restored against this tree, each of which this test
// fails on: step 5's first FACT line deleted, or any of steps 1-4's turned
// into "Note:"; step 1's "no run in flight" FACT deleted, or a FACT line put
// before step 1; the `TASK=` line blanked in the setup block, or a `fugaro
// run` line losing its `"$TASK"`; `FUGARO_RUN` put back to a bare slug with no
// run id; `fugaro budget set --global --per-run` put back into the Restore;
// either `git diff -- .claude/settings.json` dropped; the step 3 `printable`
// set widened to the API key, or the read reverted to a `--format` that prints
// the whole env array; the "use fugaro image refresh, not fugaro upgrade"
// paragraph deleted; the `github/dimipaun/fugaro` entry no longer named; the
// "no other repository's run started" FACT deleted; and each of the stale
// citations (doctor.go:748, cloud.go:53, runner.go:797, gcp/run.go:82-98,
// upgrade_cloud.go:162, run.go:441 — the last a number that belongs to no
// citation at all, since launchPolicyGates is called at run.go:473) put back;
// and the citations the second review found drifted (run.go:507 for the
// missing-task message, run.go:449 for the allow-list gate, runner.go:863 and
// :922, runstore.go:411 and :405, policy.go:203 and :261-264, run.go:817-823,
// runner.go:992, upgrade_cloud.go:166, project.go:461, gcp/run.go:87) put
// back — chk49Citations is what holds their replacements to the tree.
func TestChecklistHasCheck49(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	const heading = "## Check 49: policy as data, end to end (installation fugaro only, never belong; run by you; NOT RUN)"
	section := docSection(t, path, doc, heading)
	body := section
	if i := strings.Index(body, "\n"); i != -1 {
		body = body[i+1:]
	}
	chk49MustContain(t, body, "body", []string{
		"**Never `belong`**",
		"you run every command",
		"no agent runs any step of it",
		"no step of this check types, prints or records a secret value or a key value",
		// The phase the check belongs to, and the release it ships in: the
		// owner ruled on 2026-10-15 that phases 3 to 6 ship in 0.9.0, so
		// nothing here may depend on phase 3 code.
		"0.9.0",
	})

	// Its row in the check list (the summary table), not only the section.
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 49 |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Error("the check list has no `| 49 |` row")
	} else {
		chk49MustContain(t, row, "row", []string{
			"USER-RUN, NOT RUN",
			"never `belong`",
			"See \"Check 49\" below",
			"sandbox repository only",
		})
	}

	// Check 43's guard block inside the section — the three hard stops and
	// the project pinning, not merely a ${P:?...}.
	chk49MustContain(t, section, "section", []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$TASK" ] && [ -n "$C" ] \`,
		// C is the operator's project config, and it is the block's and not
		// step 1's: step 8 restores it in a later shell, and a step-8 shell
		// that never ran step 1 would expand `cp "$C.before-49" "$C"` to
		// `cp ".before-49" ""` and put nothing back.
		"C=$HOME/.config/fugaro/projects/fugaro.yaml",
		`|| { echo "Check 49 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 49 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 49 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
		// The block is re-run at the start of every step, and pasted into a
		// file: an interactive zsh does not honour its `#` comments.
		"RE-RUN THIS WHOLE BLOCK at the start of every",
		"Paste the block into a file, not straight into an interactive zsh",
	})

	// The order the design fixes (design §5): the rules go first, then the
	// runner, and only then the policy — publishing before the rules are
	// deployed is a policy the runs it bounds cannot read.
	chk49MustContain(t, section, "section", []string{
		"### 2. The rules deploy",
		"### 3. The runner rollout, and the two halves it actually needs (finding 15)",
		"### 4. Publish",
		"rules deploy",
		"runner rollout",
		"`policy publish`",
		"Publishing refuses without the rules marker",
		"config/limits/policyRules",
	})

	// Finding 15, both ways: the rollout is two owner steps, `fugaro
	// update --repo` is the only one that writes a job's environment, and the
	// command the plan named does not exist.
	chk49MustContain(t, section, "section", []string{
		"finding 15",
		"the only path that writes a job's environment is `fugaro update --repo`",
		"`fugaro upgrade`",
		"`fugaro image refresh`",
		"fugaro update --repo . --project fugaro",
	})
	for _, dead := range []string{
		"fugaro upgrade --cloud", // no such flag: `cloud` is one of upgrade's steps
		"fugaro upgrade --project fugaro --cloud",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 49 section names a command that does not exist (%q): `fugaro upgrade` has no --cloud flag, and `cloud` is one of its steps", dead)
		}
	}

	// Step 6(b)'s admissionCheck citation, and it has to be pinned as the
	// PHRASE: the section spells the first number of that sentence out as
	// `internal/runner/runner.go:883` and abbreviates the second to `(:951)`,
	// so a full-path guard alone cannot hold the abbreviation. The number is
	// resolved against the tree in chk49Citations below as well, so a drift in
	// the file — not just in the sentence — fails here.
	chk49MustContain(t, chk49Step(t, section, "**(b) The run's own.**"), "step 6(b)", []string{
		"`admissionCheck` ever runs (`:951`)",
	})
	if strings.Contains(section, "`admissionCheck` ever runs (`:862`)") {
		t.Error("the Check 49 section's step 6(b) cites :862 for admissionCheck: the call is at :951 on this tree (`admissionCheck(ctx)` in the run's own start), and :862 is inside openCheckout")
	}

	// Step 6, both ways: no key of fugaro.yaml names a repository, so the
	// un-enrolled state is made with the allow-list, and the refusal it
	// produces is the launcher's; the run-side half is a direct launch.
	chk49MustContain(t, section, "section", []string{
		"has no key that names a repository",
		"fugaro project repos rm bitbucket/edgeappinc/fugarosandbox --project fugaro",
		"The launch stops before any execution exists",
		"FUGARO_RUN",
		"The run stops before any lease exists",
		"Record which layer refused",
		"Check 53 step 6",
		"live test of the binding itself belongs to Check 53 step 6",
	})
	if strings.Contains(section, "Point the sandbox's `fugaro.yaml` at a repository") {
		t.Error("the Check 49 section still points fugaro.yaml at a repository: no key of fugaro.yaml names one, so that edit cannot change which repository a run is for")
	}
	// The `credential` line the plan asked for does not exist: the run refuses
	// before the provider is opened, so there is no credential line to be
	// absent. The section says so rather than asserting a line nobody emits.
	if strings.Contains(section, "the absence of a `credential` line") {
		t.Error("the Check 49 section still asserts the absence of a `credential` line: no step of the run emits one, because the refusal is before the provider is opened")
	}

	// Every step asserts its own outcome, and each step's FACT lines are
	// pinned INDIVIDUALLY, by a phrase that occurs in that step and nowhere
	// else in the section. A total count is not enough: with only
	// `strings.Count(section, "**FACT:**") >= 8`, deleting step 5's line and
	// adding one to step 1 passes, and turning every FACT of steps 1 to 4
	// into "Note:" passes too. Each want below is one step's own FACT text,
	// so either mutation fails the step it happened in.
	chk49Facts := []struct {
		step string
		want []string
	}{
		{"### 1.", []string{
			"**FACT:** the state as recorded — `fugaro runs ls --all --project fugaro` shows **no run in flight**",
		}},
		{"### 2.", []string{
			// The two act lines are the SECOND command's, not the --plan-only
			// one's: --plan-only with --firebase-project stops after the installation
			// root and never reaches deployDatabase, so a FACT the owner reads
			// off the plan-only line above it is a FACT the owner cannot
			// produce. The stop is installRoot's plan-only return
			// (init.go:1285-1287), taken at init_firebase.go:210-213 — NOT the
			// Firebase root's applyRoot at :289, which the run never reaches.
			"**FACT:** the two act lines printed by the **second** command",
			"**FACT:** the read-back —",
		}},
		{"### 3.", []string{
			"**FACT:** the job's environment carries `FUGARO_RTDB_URL`",
			"**FACT:** one ordinary sandbox run with the policy still unpublished",
		}},
		{"### 4.", []string{
			"**FACT:** the printed diff verbatim",
			"**FACT:** the seeded allow-list names `bitbucket/edgeappinc/fugarosandbox`",
			"**FACT:** the node's `version`",
			"**FACT:** the `budget.per_run_usd` line of the printed diff",
		}},
		{"### 5.", []string{
			"**FACT:** the run's own record —",
			"**FACT:** no other repository's run started",
			"**FACT:** the run's own per-call charge",
		}},
		{"**(a) The launcher's.**", []string{
			"**FACT:** the launcher's refusal verbatim",
		}},
		{"**(b) The run's own.**", []string{
			"**FACT:** the run's own refusal text verbatim",
			"**FACT:** no lease was taken and no git token was minted",
		}},
		{"### 7.", []string{
			"**FACT:** the launcher's refusal, and the two documents read side by side",
			"**FACT:** neither document claims the launch-side check cannot be bypassed",
		}},
		{"### 8.", []string{
			"**FACT:** the restore is complete —",
			"**FACT:** the sandbox still runs",
		}},
	}
	total := 0
	for _, tc := range chk49Facts {
		step := chk49Step(t, section, tc.step)
		if n := strings.Count(step, "**FACT:**"); n != len(tc.want) {
			t.Errorf("the Check 49 section's %s step carries %d FACT lines, want exactly the %d pinned below: a step's own FACT lines are what the owner pastes back for it", tc.step, n, len(tc.want))
		}
		total += len(tc.want)
		for _, w := range tc.want {
			if !strings.Contains(step, w) {
				t.Errorf("the Check 49 section's %s step is missing its FACT line %q", tc.step, w)
			}
		}
	}
	if n := strings.Count(section, "**FACT:**"); n != total {
		t.Errorf("the Check 49 section carries %d FACT lines and the per-step pins above account for %d: every FACT line belongs to a step, and an unpinned one is a line nothing guards", n, total)
	}
	// No "**FACT:**" may sit outside the steps the pins walk: the section's
	// steps must partition it, or a FACT in the body above step 1 would be
	// counted by neither loop.
	if head := section[:strings.Index(section, "### 1.")]; strings.Contains(head, "**FACT:**") {
		t.Error("the Check 49 section has a FACT line before its first step, which no per-step pin above reaches")
	}

	// The Restore returns the installation to its recorded state, and the
	// published policy has a command to leave with, not an outline. It does
	// NOT write the caps node: nothing in the check changes it, so a
	// `fugaro budget set` there would write a node the check never touched.
	chk49MustContain(t, section, "section", []string{
		"### 8. Restore",
		"the sandbox still runs",
	})
	// The Restore's own COMMANDS, read in step 8's slice and not over the
	// whole section: the section's prose also writes `cp "$C.before-49" "$C"`
	// (the setup block's comment explains why C is the block's), and the FACT
	// names `cmp "$C.before-49" "$C"` — so a section-wide Contains passes with
	// the restore's own first line deleted, which is the line the whole
	// ordering argument rests on.
	chk49MustContain(t, chk49Step(t, section, "### 8."), "step 8", []string{
		// The order, and the file it puts back first.
		"the operator's project config: back first, because the next line reads it",
		`cp "$C.before-49" "$C"`,
		"fugaro update --repo . --project fugaro",
		"git diff -- fugaro.yaml",
		"git diff -- .claude/settings.json",
		"fugaro budget show --json --project fugaro",
		"fugaro project show --json --project fugaro",
		"fugaro project policy publish --delete --project fugaro",
	})
	// …but a substring pin over the slice cannot SEE a deletion or a swap in
	// the block itself, because step 8's own FACT prose repeats four of those
	// strings verbatim. So the restore's first fenced block is read as its own
	// lines — comments and blanks dropped, in the order the shell runs them —
	// and the two commands the step's whole ordering argument rests on are
	// checked by INDEX, not by presence. Deleting the two json-show lines, or
	// swapping `fugaro update --repo` above the `cp`, both fail here.
	restore := chk49FencedBlocks(chk49Step(t, section, "### 8."))[0]
	var restoreLines []string
	for _, l := range strings.Split(restore, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			restoreLines = append(restoreLines, l)
		}
	}
	// chk49FencedBlocks keeps the fence's own info string ("bash") as the
	// block's first line, and it is not a command.
	if len(restoreLines) > 0 && !strings.ContainsAny(restoreLines[0], " \t") {
		restoreLines = restoreLines[1:]
	}
	at := func(want string) int {
		for i, l := range restoreLines {
			if l == want {
				return i
			}
		}
		return -1
	}
	const (
		restoreConfig = `cp "$C.before-49" "$C"`
		writeEnv      = "fugaro update --repo . --project fugaro"
	)
	if i := at(restoreConfig); i != 0 {
		t.Errorf("step 8's restore block has %q at command %d of its own block, want 0: the operator's config goes back before anything reads it, and the step's FACT prose repeats the string so only the block's own order can be checked", restoreConfig, i)
	}
	if i := at(writeEnv); i != 1 {
		t.Errorf("step 8's restore block has %q at command %d, want 1 — immediately after the config restore: `update --repo` writes the job environment out of that file (`internal/infra/spec.go:753`), so running it first writes whatever the config says now into the job, which is the inversion the step calls out", writeEnv, i)
	}
	for _, want := range []string{
		"git diff -- fugaro.yaml",
		"git diff -- .claude/settings.json",
		// The revert of the one edit the check can make to the sandbox's own
		// file: step 3's answer to `update --repo`'s gcp_project: prompt is `n`,
		// and this is what handles the run that did not give it.
		"if ! git diff --quiet -- fugaro.yaml; then",
		"git checkout -- fugaro.yaml",
		"fugaro budget show --json --project fugaro",
		"fugaro project show --json --project fugaro",
	} {
		if at(want) < 0 {
			t.Errorf("step 8's restore block has no %q line of its own: the step's FACT prose writes the same string, so the substring pin above cannot see this line deleted", want)
		}
	}
	// …and the revert is guarded by step 1's own record, not by the file being
	// dirty: a checkout whose committed copy already carries gcp_project: is
	// never written to, so an unrelated working-tree change in it belongs to the
	// operator and must not be discarded here.
	chk49MustContain(t, chk49Step(t, section, "### 8."), "step 8", []string{
		"grep -q '^gcp_project:' /tmp/chk49-sandbox-yaml-before.txt",
	})
	// Read as a command line, not as a substring: the section SAYS it has no
	// `fugaro budget set`, and that sentence is the point — it is running one
	// that would write a node no step changed.
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro budget set") {
			t.Errorf("the Check 49 section runs %q: no step of this check changes the caps node, so that write restores a value the check only recorded", line)
		}
	}
	chk49MustContain(t, section, "section", []string{
		"There is no `fugaro budget set` in this restore, and that is deliberate",
	})

	// The project-wide ceiling: the installation also runs the live
	// dimipaun/fugaro dogfood, and the published per_run_usd is the whole
	// project's, so the check must say so, confirm nothing is in flight, and
	// publish the number the job environment already carries.
	chk49MustContain(t, section, "section", []string{
		"dimipaun/fugaro` dogfood pipeline",
		"a published `budget.per_run_usd` is the **whole project's** per-run ceiling",
		"no step below ever lowers it",
		"fugaro runs ls --all --project fugaro",
		"**FACT:** the state as recorded — `fugaro runs ls --all --project fugaro` shows **no run in flight**",
		"and it still names the dogfood's own repository, `github/dimipaun/fugaro`",
		"fugaro project repos add github/dimipaun/fugaro --project fugaro",
		"**FACT:** no other repository's run started",
	})

	// Every `fugaro run` carries the block's TASK. A launch with no task is
	// refused at run.go:220, before openCloud (:229) and long before the
	// allow-list gate at run.go:473 (`launchPolicyGates`; :441 is a comment
	// inside budgetPrecheck's block, which is the stale number the section is
	// checked against below), so a bare `fugaro run` in step 3, 5, 6(a), 7 or 8
	// never reaches what the step is there to produce.
	chk49MustContain(t, section, "section", []string{
		"TASK='Do not change any file and do not open a pull request. Reply with the single word ok.'",
		"Every `fugaro run` below carries a task, and it has to",
		// The runs' ref, and why it is master: the sandbox has no `main`, and
		// --ref is not cross-checked against the remote at launch.
		"Every `fugaro run` below passes `--ref master`, and it is `master` because the sandbox repository has no `main`",
		"`internal/gitops/gitops.go`",
		`fugaro run "$TASK" --workflow <the sandbox's workflow> --ref master --json --project fugaro`,
		`fugaro run "$TASK" --workflow <the sandbox's workflow> --ref master --project fugaro`,
	})
	// Every run names the sandbox's base branch, and it is `master`: the
	// sandbox repository has no `main` (deploy/sandbox/fugaro.yaml), and --ref
	// is carried into the job verbatim (run.go:589-594) and fetched by the
	// runner (gitops.go:239), so a run pointed at a branch that does not exist
	// halts on the fetch. Checked as a property of every `fugaro run` line
	// rather than as `--ref main` asserted absent, so any other wrong branch is
	// caught too.
	var runs int
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro run ") {
			runs++
			if !strings.Contains(line, `"$TASK"`) {
				t.Errorf("the Check 49 section has a `fugaro run` with no task: %q — the launcher refuses a taskless launch before the allow-list gate, so this step never reaches its own refusal", line)
			}
			if !strings.Contains(line, "--ref master") {
				t.Errorf("the Check 49 section has a `fugaro run` that does not pass `--ref master`: %q — the sandbox's base branch is master, --ref is not checked against the remote at launch, and the runner's fetch of a branch that does not exist fails the run", line)
			}
		}
	}
	if runs < 5 {
		t.Errorf("the Check 49 section has %d `fugaro run` lines, want at least 5 (steps 3, 5, 6(a), 7 and 8), so the TASK and --ref rules above are checked over all of them", runs)
	}

	// `fugaro image refresh` is the image half, not `fugaro upgrade`: upgrade
	// also rewrites the checkout's .claude/settings.json and installs the
	// plugin in Claude Code at user scope, and the check must say so rather
	// than sending the owner through two writes it then has to undo.
	chk49MustContain(t, section, "section", []string{
		"Use `fugaro image refresh` for the image half, not `fugaro upgrade`",
		"**rewrites the checkout's `.claude/settings.json`**",
		"`internal/cli/upgrade.go` is that command's own `Long` text, one paragraph per step: `pin`, `plugin`, `cloud`",
		// Both places the pin step's file is compared: step 8's block and
		// step 8's FACT. One without the other is a restore that checks the
		// file and a FACT that does not claim it.
		"git diff -- .claude/settings.json\ncmp -s /tmp/chk49-claude-settings-before.json .claude/settings.json",
		"`git diff -- .claude/settings.json` is empty with the plugin pin where step 3 found it",
	})

	// The step 3 read prints the environment's NAMES and value LENGTHS, and
	// the values only of the three that are not credentials. The set is
	// pinned exactly, so adding FUGARO_FIREBASE_API_KEY to it — the review's
	// finding: a --format that printed the env array printed the key — fails
	// here. The old read is asserted absent for the same reason.
	chk49MustContain(t, section, "section", []string{
		`printable = {"FUGARO_RTDB_URL", "FUGARO_BUDGET_MODE", "FUGARO_MAX_RUN_USD"}`,
		"# LENGTH of each value. Never a value: FUGARO_FIREBASE_API_KEY is a",
	})
	for _, dead := range []string{
		"spec.template.spec.template.spec.containers[0].env)' | tr ','",
		"FUGARO_FIREBASE_API_KEY\", \"FUGARO_MAX_RUN_USD",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 49 section's step 3 read prints the job environment's values (%q): FUGARO_FIREBASE_API_KEY is a credential and the read prints names and lengths only", dead)
		}
	}

	// Step 3 is not the job environment alone, and step 8's `git diff --
	// fugaro.yaml` FACT rests on knowing that. `fugaro update --repo`'s
	// repository stage ends by OFFERING the checkout's gcp_project: line —
	// init_repo_stage.go:163, Apply's `s.anchor(ctx, env, s.e.r.o.planOnly)`,
	// where the --plan-only run plans the same line and writes nothing (:143) —
	// through the anchor at init_fugaroyaml.go:243, the [y/N] prompt at :220
	// and the write at :230; --yes takes that prompt without asking. The check
	// runs `update --repo` in that checkout TWICE (step 3 and step 8), so the
	// section has to say which answer it wants and step 8 has to have a revert:
	// without both, "this is a confirmation that it is unchanged" is a
	// confirmation of an edit the check itself made, and the FACT above fails
	// on the operator's own checkout.
	chk49MustContain(t, chk49Step(t, section, "### 3."), "step 3", []string{
		"`update --repo` also offers the checkout's own `gcp_project:` line",
		"`internal/cli/init_repo_stage.go` → `internal/cli/init_fugaroyaml.go`",
		"Write this to <path>? [y/N]:",
		"asks `Write this to <path>? [y/N]:` (`internal/cli/init_fugaroyaml.go`)",
		"step 8 runs the same command and asks the same question a second time",
	})

	// The citations the review found drifted. Asserted ABSENT rather than
	// pinned present: a line number that moved under a legitimate change is
	// not this test's to freeze, but a number that points at something else
	// today is a citation a reader cannot follow, which is what these were.
	for _, dead := range []string{
		"internal/cli/doctor.go:748",
		"internal/cli/cloud.go:53",
		"internal/runner/runner.go:797",
		"internal/backend/gcp/run.go:82-98",
		"internal/cli/upgrade_cloud.go:162",
		"internal/cli/run.go:441",
		// upgrade's Long text: :84 is blank, `pin` is :85-89, `plugin` is
		// :90-94 and `cloud` is :95-110. A range that stops at :88 describes
		// one of the three steps and claims all three.
		"internal/cli/upgrade.go:84-88",
		// NOT listed here: `internal/cli/project_cmd.go:352` for
		// `allowSourcesOf`. It was the wrong number when it was added, then the
		// right one after the first merge with next moved the function down,
		// and wrong again — :384 — after the second. The section cites no
		// number for it now (chkNoLineCites holds that), so there is nothing
		// here to keep off the list: the numbers are what moved, not the
		// citation.
		// The Firebase root's applyRoot at init_firebase.go:289 is what
		// --plan-only never reaches: the run has already returned at :210-213
		// on installRoot's plan-only stop (init.go:1285-1287). Citing :290 as
		// the stop points at code a --plan-only run cannot execute.
		"internal/cli/init_firebase.go:290",
		// Step 6(b)'s admissionCheck. :862 was the call when the section was
		// written; the merge with next moved it twice (to :922, then to :951,
		// where `admissionCheck(ctx)` is called in the run's own start), and
		// :862 is now inside openCheckout. This entry catches the citation only
		// if it is ever written out in full; the section abbreviates it, and
		// the phrase pin above step 6 is what actually holds it.
		"internal/runner/runner.go:862",
		// The citations the second review found drifted: each pointed at
		// unrelated code at HEAD after the merge with next moved the file under
		// it. The replacements are resolved against the tree by
		// chk49Citations; these entries keep the old numbers from coming back
		// from the same reading that produced them.
		"internal/cli/run.go:507",
		"internal/cli/run.go:449",
		"internal/cli/run.go:817-823",
		"internal/runner/runner.go:863",
		"internal/runner/runner.go:922",
		"internal/runner/runner.go:992",
		"internal/runner/project.go:461",
		"internal/runner/policy.go:203",
		"internal/runner/policy.go:261-264",
		"internal/runstore/runstore.go:411",
		"internal/runstore/runstore.go:405",
		"internal/cli/upgrade_cloud.go:166",
		"internal/backend/gcp/run.go:87",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 49 section cites %q, which does not point at what it says on this tree", dead)
		}
	}

	// Step 2's plan-only claim, pinned present as well as its wrong citation
	// asserted absent: what stops a `--plan-only` run is installRoot's own
	// plan-only return, taken at init_firebase.go:210-213 — not the Firebase
	// root's applyRoot, which that run never reaches. The substantive claim
	// (the act lines are the second command's) is unchanged and pinned above.
	chk49MustContain(t, chk49Step(t, section, "### 2."), "step 2", []string{
		"`internal/cli/init.go`'s `installRoot`",
		"`internal/cli/init_firebase.go`",
		"the one whose stop this is **not**",
	})

	// Step 1's backup keeps the FIRST copy, and the guard paragraph names every
	// variable the first hard stop asserts. The unguarded `cp` was the review's
	// finding: steps are re-run block by block, so a second pass through step 1
	// overwrote the pre-check file with whatever step 8 had just put back, and
	// step 8's byte-for-byte FACT then compared the operator's config against
	// itself. The paragraph said the stop asserts `P`, `L` and `TASK` and did
	// not mention `C`, which is the variable whose emptiness does the damage.
	chk49MustContain(t, chk49Step(t, section, "### 1."), "step 1", []string{
		`[ -e "$C.before-49" ] || cp "$C" "$C.before-49"`,
		"KEEP THE FIRST COPY",
	})
	chk49MustContain(t, section, "section", []string{
		"`P` is exactly `fugaro-dev`, and `L`, `TASK` **and `C`** are non-empty",
	})

	// Every variable the section's commands read is set by the block that is
	// re-run at the start of every step, or is a step's own (the map below).
	// The review found the first pass breaking exactly this: step 1 assigned
	// `C` and step 8 — a later shell, where step 1's shell is gone — used it,
	// so `cp "$C.before-49" "$C"` expanded to `cp ".before-49" ""`, the
	// operator's config was never put back, and the `fugaro update --repo` that
	// follows read the job environment out of a file that was still edited.
	block := chk49FencedBlocks(section)[0]
	assigned := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)=`).FindAllStringSubmatch(block, -1) {
		assigned[m[1]] = true
	}
	for _, name := range []string{"P", "L", "JOB", "C", "TASK"} {
		if !assigned[name] {
			t.Errorf("the Check 49 setup block does not set %s, and every step below runs in a shell that has only what the block sets: a step that reads it reads an empty string", name)
		}
	}
	stepLocal := map[string]bool{
		"RUN": true, // step 5's, from the launch's own output
	}
	// The shell's own, which no block has to set: $HOME is the operator's
	// and is the same in every shell of the check.
	shellOwn := map[string]bool{"HOME": true}
	// Every command line in the section, not only the ones that act on a
	// project: the review's own break was in a `cp`, which no verb list
	// collects. Comment lines are skipped — they are prose, and `${VAR:?}`
	// in a comment is a sentence about the convention, not a read.
	ref := regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)`)
	for _, b := range chk49FencedBlocks(section) {
		for _, line := range strings.Split(b, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			for _, m := range ref.FindAllStringSubmatch(line, -1) {
				if assigned[m[1]] || stepLocal[m[1]] || shellOwn[m[1]] {
					continue
				}
				t.Errorf("the Check 49 section reads $%s in %q, and neither the setup block that every step re-runs nor a step's own assignment sets it: in the shell that runs this line it is empty", m[1], line)
			}
		}
	}

	// FUGARO_RUN is `<repo-slug>/<run-id>`, parsed by runstore.ParseRef, and
	// the override must name a run that does not exist — never one of
	// dimipaun/fugaro's, which would point the sandbox's job at another
	// repository's stored task.
	chk49MustContain(t, section, "section", []string{
		"**`FUGARO_RUN` is `<repo-slug>/<run-id>`, both halves required.**",
		"`internal/runstore/runstore.go`",
		"--update-env-vars FUGARO_RUN=not-this-jobs-slug-000000000000/20261015-000000-dead",
		"a run id that names no run anywhere",
	})

	// Every citation the section makes in full is resolved against the tree,
	// by the SYMBOL it names, and by nothing else: a line number drifts with
	// the next merge and leaves the owner on unrelated code, which is what
	// these citations did twice. The section's two abbreviated citations —
	// `(:229)` for openCloud and `(:951)` for admissionCheck — are held by the
	// phrase pins above.
	checkCitations(t, section, "Check 49 section", chk49Citations)
	chkNoLineCites(t, section, "Check 49 section")

	// No step handles a secret value: the check reads the rules' bytes, the
	// node's version and the job environment's key *presence*, never a value.
	chk49MustContain(t, section, "section", []string{
		"never its value",
	})
	for _, dead := range []string{
		"gcloud secrets versions access", "--data-file", "gcloud secrets create",
		"print-access-token", "FUGARO_FIREBASE_API_KEY=", "ANTHROPIC_API_KEY=",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 49 section handles a secret value (%q): this check never reads, prints or records one", dead)
		}
	}

	// The closing paragraph, which is what the owner reads last.
	chk49MustContain(t, section, "section", []string{
		"**Paste back:** every FACT line above, never a secret value and never a key value.",
		"Never run this against `belong`.",
	})

	// Every command that acts on a project names it, and no gcloud/fugaro
	// command interpolates an unguarded `${VAR}`: an empty variable there is
	// an empty argument gcloud resolves from its ambient configuration.
	unguarded := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	for _, line := range sectionCommands(section) {
		if strings.HasPrefix(line, "gcloud ") &&
			!strings.Contains(line, `--project="${P:?}"`) &&
			!strings.Contains(line, `"${P:?}"`) &&
			!strings.Contains(line, `projects/${P:?}`) {
			t.Errorf("the Check 49 section has a gcloud command that does not name the project: %q", line)
		}
		if m := unguarded.FindString(line); m != "" {
			t.Errorf("the Check 49 section has an unguarded %s in a command: %q", m, line)
		}
	}
	// `fugaro doctor --project fugaro` is the guard's own cross-check; every
	// other fugaro command acts on the installation and must name it too.
	var sawDoctor bool
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro doctor --project fugaro") {
			sawDoctor = true
			continue
		}
		if !strings.Contains(line, "--project fugaro") {
			t.Errorf("the Check 49 section has a fugaro command that does not name the installation: %q", line)
		}
		if m := unguarded.FindString(line); m != "" {
			t.Errorf("the Check 49 section has an unguarded %s in a fugaro command: %q", m, line)
		}
	}
	if !sawDoctor {
		t.Error("the Check 49 section's guard block is not read as a command, so nothing checks that it pins the installation")
	}
}

// chk49FencedBlocks returns the Check 49 section's fenced code blocks, in
// order. The first is the setup block every step re-runs, which is the one
// whose assignments a step in a later shell can rely on.
func chk49FencedBlocks(section string) []string {
	parts := strings.Split(section, "```")
	var out []string
	for i := 1; i < len(parts); i += 2 {
		out = append(out, parts[i])
	}
	return out
}

// chk49StepMarkers is the Check 49 section's steps in the order they appear,
// `### 6.` included for the boundary it draws and not pinned itself: step 6's
// own FACT lines belong to its two halves, which are what the section asserts
// separately, and its introductory paragraph asserts none.
var chk49StepMarkers = []string{
	"### 1.", "### 2.", "### 3.", "### 4.", "### 5.", "### 6.",
	"**(a) The launcher's.**", "**(b) The run's own.**", "### 7.", "### 8.",
	"**Paste back:**",
}

// chk49Step returns one step of the Check 49 section: from its marker to the
// next step's, so a FACT line is attributed to the step that carries it rather
// than to the section as a whole. A marker that is missing, or out of order,
// fails the test: the pins below are only as good as the slicing.
func chk49Step(t *testing.T, section, marker string) string {
	t.Helper()
	i := -1
	for k, m := range chk49StepMarkers {
		if m == marker {
			i = k
			break
		}
	}
	if i < 0 {
		t.Fatalf("chk49Step(%q): not one of the Check 49 section's step markers", marker)
	}
	from := strings.Index(section, marker)
	if from < 0 {
		t.Fatalf("the Check 49 section has no %q step", marker)
	}
	to := len(section)
	for _, next := range chk49StepMarkers[i+1:] {
		if j := strings.Index(section[from+len(marker):], next); j >= 0 {
			to = from + len(marker) + j
			break
		}
	}
	if to <= from {
		t.Fatalf("the Check 49 section's %q step is empty: %q must come after it", marker, chk49StepMarkers[i+1])
	}
	return section[from:to]
}

// chk49MustContain fails the test for each want that is not in got, naming the
// part of the section (its body, its table row, the whole section) that should
// hold it.
func chk49MustContain(t *testing.T, got, part string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the Check 49 section's %s is missing %q", part, w)
		}
	}
}

// TestChecklistHasCheck50: docs/gcp-live-checklist.md must carry phase 1b's
// owner-run check (design project-centric.md §6.2 and §11; plan
// 2026-10-14-project-centric.md Task 1b.4) as one owner-run, not-yet-run,
// installation-`fugaro`-only check numbered 50 — with its row in the check
// list and Check 43's guard block (the three hard stops and the project
// pinning) inside its section, so a reader cannot mistake it for something CI
// covers and no step of it can run against a project other than fugaro-dev.
//
// The owner runs this document against the live installation and it spends
// real money on both sides of the comparison (Anthropic tokens and provider
// credit), so the assertions below are per-line: each is a fact the section
// must state — a stop it can take, a flag a command must carry, a FACT it
// records, a step of its Restore — and each of the hand mutations the plan's
// task names fails one of them rather than passing on the heading alone.
//
// Three claims this section exists for are pinned in their own words, because
// each is the reason the check is written rather than assumed:
//
//   - the number it settles (step 5): the reviewer's own cache_read and
//     cache_write_5m counts for the routed run against the direct one, and the
//     two cost.model_usd figures — the sentence doctor's routing-cache-markers
//     check and docs/multi-model.md leave unmeasured until this check runs;
//   - the two halves a routed call needs, neither routing alone (step 3): the
//     route entry's explicit claim and the switch, both resolved through
//     pricing.RouteIDFor by config.RoutingClaudeFor;
//   - where a failure lands (0.8.0.x): the design's conclusion does not change
//     and the docs do, which is a patch-line change and not a re-cut.
func TestChecklistHasCheck50(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	// The heading itself carries the four markers the plan's Task 1b.4 fixes
	// ("installation fugaro only, never belong; run by you; NOT RUN"), so it is
	// asserted as a whole rather than as four substrings that would also pass
	// on the heading alone.
	const heading = "## Check 50: a Claude senior through a provider route, and whether its cache markers survive (installation fugaro only, never belong; run by you; NOT RUN)"
	section := docSection(t, path, doc, heading)
	body := section
	if i := strings.Index(body, "\n"); i != -1 {
		body = body[i+1:]
	}
	chk50MustContain(t, body, "body", []string{
		"**Never `belong`**",
		"you run every command",
		"no agent runs any step of this check",
		// The phase and its release: phase 1b ships inside 0.8.0 (R6).
		"0.8.0",
	})

	// Its row in the check list (the summary table), not only the section.
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 50 |") {
			row = line
			break
		}
	}
	if row == "" {
		// Not Fatal: the guard-block pins below must still run, so the first
		// red run reports the table row and the unguarded section together —
		// which is the state this task's red step is written against.
		t.Error("the check list has no `| 50 |` row")
	} else {
		chk50MustContain(t, row, "row", []string{
			"See \"Check 50\" below",
			"**USER-RUN, NOT RUN.**",
			"never `belong`",
			"sandbox repository only",
			// The release mapping: a step-5 failure is a docs-and-help-text change
			// on the 0.8.0 line, not a re-cut of 0.8.0 and not a 0.9.0 item.
			"0.8.0.x",
			// What the check settles, named in the row too: the number is the point.
			"cache reads",
			// The row must carry the section's own reading of the two binaries:
			// Task 1b.3 shipped with phase 1b, so *this* binary prints the
			// note that names the switch, and the record "Task 1b.3 not in
			// this binary" is for one older than that release. An owner who
			// reads only the row must neither meet the older note as this
			// check's failure nor record the message this binary does print
			// as missing.
			"**this binary prints**",
			"Task 1b.3 not in this binary",
		})
		// The row may still SAY the switch is what the message names — that
		// is what Task 1b.3's message does — but it may not say this binary
		// cannot print it, which is the phrasing below.
		for _, old := range []string{
			"cannot print",
			"refused with a message that names the switch",
		} {
			if strings.Contains(row, old) {
				t.Errorf("the Check 50 row still says %q: Task 1b.3 shipped with phase 1b, so this binary's missing-key note names the switch and the row must not deny it", old)
			}
		}
	}

	// Step 2's heading names the safeguard. It used to hedge it with "once Task
	// 1b.3 ships", which the tree the check ships against makes false: 1b.3
	// shipped with phase 1b and this binary prints the message the step asks
	// for.
	var step2Head string
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "### 2.") {
			step2Head = line
			break
		}
	}
	if !strings.Contains(step2Head, "the safeguard, which names the switch") {
		t.Errorf("the Check 50 step 2 heading does not name the switch-naming safeguard (%q): the step's second half is that message, and an owner reading the heading has to know the message is this binary's", step2Head)
	}
	if strings.Contains(step2Head, "1b.3") {
		t.Errorf("the Check 50 step 2 heading still defers the safeguard to Task 1b.3 (%q): 1b.3 shipped with phase 1b, so this binary prints the note that names the switch", step2Head)
	}

	// Check 43's guard block inside the section — the three hard stops and the
	// project pinning, not merely a ${P:?...}.
	chk50MustContain(t, section, "section", []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$JOB" ] && [ -n "$C" ] && [ -n "$TASK" ] && [ -n "$TASK50" ] && [ -n "$START" ] \`,
		// C is the operator's project config, and it is the block's and not
		// step 1's: step 7 restores it in a later shell, where step 1's shell
		// is gone.
		"C=$HOME/.config/fugaro/projects/fugaro.yaml",
		`|| { echo "Check 50 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 50 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 50 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
		// The block is re-run at the start of every step, and pasted into a
		// file: an interactive zsh does not honour its `#` comments.
		"RE-RUN THIS WHOLE BLOCK at the start of every",
		"Paste the block into a file, not straight into an interactive zsh",
	})

	// The switch the check is about, and the three values it accepts, with the
	// absent-means-direct default (D1b) and the repository's inability to write
	// it (D1a).
	chk50MustContain(t, section, "section", []string{
		"`routing.claude`",
		"`direct`",
		"`openrouter`",
		"`auto`",
		"absent",
		"a repository cannot write it",
		"allow_data_to",
		// Routing exists only with the gateway: with the budget off there is
		// nothing for the switch to act on.
		"Routing exists only with the gateway",
		// Task 1b.3 (doctor's routing-cache-markers info, validate's
		// resolution line, the missing-key message) is ordered before this
		// check's task and **has shipped with phase 1b** (#441): the section
		// says so, and says what a binary *older* than that release records
		// when its output is missing — not produced, rather than a failure.
		// Without these the owner reaches step 3 with no line to paste while
		// this test stays green.
		"Task 1b.3's, and 1b.3 has shipped",
		"prints the `routing-cache-markers` info",
		"A binary older than the release phase 1b ships in prints neither",
		"recorded as *not produced*",
		"if the `fugaro validate` you ran prints no resolution line at all, record this FACT as *not produced* and go on",
		"the line itself is Task 1b.3's, which shipped with phase 1b",
		// What "not landed" looks like is per step, and the opener says which
		// each one is: step 3's line is simply absent, step 2's note IS
		// printed and only fails to name the switch — which is why step 2
		// records its own word and not "not produced".
		`"not landed" looks like differs by the step`,
		"so step 2 records this FACT as",
	})

	// The steps, in order, and the closing paragraph.
	chk50MustContain(t, section, "section", []string{
		"### 1. Setup and guard",
		"### 2. The refusal, unclaimed",
		"### 3. ",
		"### 4. ",
		"### 5. ",
		"### 6. ",
		"### 7. Restore",
		"**Paste back:** every `FACT` line above, never a key and never a token.",
	})

	// Each step's FACT lines, pinned INDIVIDUALLY and counted: a total count
	// alone passes when one step's line is deleted and another's is added, and
	// a step whose lines were turned into "Note:" would keep the count too.
	chk50Facts := []struct {
		step string
		want []string
	}{
		{"### 1.", []string{
			"**FACT:** the state as recorded",
			// The records belong to one attempt, and the step says which two
			// checks proved it: no record was left over from an earlier attempt
			// (the block refuses on one), and the copy step 7 restores from is
			// the operator's config as it stands.
			"**FACT:** the block's own `date -r`-guarded refusal did **not** fire",
		}},
		{"### 2.", []string{
			"**FACT:** the two refusals verbatim",
			"**FACT:** that message verbatim",
		}},
		{"### 3.", []string{
			"**FACT:** the publish's printed diff",
			"**FACT:** `fugaro validate`'s resolution line verbatim",
			"**FACT:** the repository's refusal verbatim",
			"**FACT:** the job's environment now names `FUGARO_MODEL_PRICES`",
		}},
		{"### 4.", []string{
			"**FACT:** for the reviewer's own `model call` lines in each run",
			"**FACT:** `cost.model_usd` and `cost.reported_usd`",
		}},
		{"### 5.", []string{
			// ONE number, named first: the rest of the line is what it is read
			// against, and a FACT that asks for four counts and two costs leaves
			// the owner with no figure to report.
			"**FACT:** the ONE number first",
			"**FACT:** the two `cost.model_usd` figures",
		}},
		{"### 6.", []string{
			"**FACT:** which route each of the two runs took",
			"**FACT:** that the `anthropic-api-key` version",
		}},
		{"### 7.", []string{
			"**FACT:** the restore is complete",
			// The records are removed only once the restore has succeeded, and
			// the step says so: a record left behind is what the next run reads
			// as its own.
			"**FACT:** this attempt's records are gone",
			"**FACT:** the sandbox still runs",
		}},
	}
	total := 0
	for _, tc := range chk50Facts {
		step := chk50Step(t, section, tc.step)
		if n := strings.Count(step, "**FACT:**"); n != len(tc.want) {
			t.Errorf("the Check 50 section's %s step carries %d FACT lines, want exactly the %d pinned below: a step's own FACT lines are what the owner pastes back for it", tc.step, n, len(tc.want))
		}
		total += len(tc.want)
		for _, w := range tc.want {
			if !strings.Contains(step, w) {
				t.Errorf("the Check 50 section's %s step is missing its FACT line %q", tc.step, w)
			}
		}
	}
	if n := strings.Count(section, "**FACT:**"); n != total {
		t.Errorf("the Check 50 section carries %d FACT lines and the per-step pins above account for %d: every FACT line belongs to a step, and an unpinned one is a line nothing guards", n, total)
	}
	if head := section[:strings.Index(section, "### 1.")]; strings.Contains(head, "**FACT:**") {
		t.Error("the Check 50 section has a FACT line before its first step, which no per-step pin above reaches")
	}

	// The number this check exists to record: the cache fields, both ways, and
	// the cost beside them. A section that only says "compare the runs" records
	// nothing, which is the failure mode the plan's step 5 is written against.
	chk50MustContain(t, chk50Step(t, section, "### 5."), "step 5", []string{
		"`cache_read`",
		"`cache_write_5m`",
		"`cost.model_usd`",
		"full input rate",
		// A direct run with no cache field is not a baseline, and saying so is
		// what stops a zero being read as the route's doing.
		"not a baseline",
	})

	// The failure's own landing place: the docs change, the design's
	// conclusion does not, and it is a patch-line change.
	chk50MustContain(t, section, "section", []string{
		"**Where a failure lands (0.8.0.x).**",
		"the design's conclusion does not change but the docs do",
		"`docs/multi-model.md`",
		"0.8.0.x",
		// A failure does not stop the check or unship the switch.
		"A failure here is not a stop for the rest of the check",
	})

	// The two halves a routed call needs, and the resolver they share: the
	// route entry's explicit claim, the switch, pricing.RouteIDFor behind
	// config.RoutingClaudeFor — so a reader cannot think the switch alone
	// routes, or that a spelling is derived rather than read from the map.
	chk50MustContain(t, section, "section", []string{
		"neither routes on its own",
		"internal/config/routing.go",
		"internal/pricing/canonical.go",
		"`config.RoutingClaudeFor`",
		"`RouteIDFor`",
		// The measured rows the switch may name, and the refusal for every
		// other Claude ID.
		"anthropic/claude-sonnet-5.5",
		"claude-sonnet-5-5",
		"no route's own ID has been measured for it",
	})

	// Every `fugaro run` carries one of the block's two tasks and the sandbox's
	// base branch: a launch with no task is refused before any gate, and the
	// sandbox repository has no `main`. Which task is not free either — the two
	// runs step 5 compares carry the committing one, and the check's other runs
	// carry the no-change one — which the step pins below hold.
	var runs int
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro run ") {
			runs++
			if !strings.Contains(line, `"$TASK"`) && !strings.Contains(line, `"$TASK50"`) {
				t.Errorf("the Check 50 section has a `fugaro run` with no task: %q — the launcher refuses a taskless launch before any policy gate, so this step never reaches what it is there to produce", line)
			}
			if !strings.Contains(line, "--ref master") {
				t.Errorf("the Check 50 section has a `fugaro run` that does not pass `--ref master`: %q — the sandbox's base branch is master and the runner's fetch of a branch that does not exist fails the run", line)
			}
		}
	}
	if runs < 4 {
		t.Errorf("the Check 50 section has %d `fugaro run` lines, want at least 4 (the direct and routed comparison runs, the two `auto` runs, and the restore's confirmation run)", runs)
	}

	// Every command that acts on a project names it, and no gcloud/fugaro
	// command interpolates an unguarded ${VAR}: an empty variable there is an
	// empty argument gcloud resolves from its ambient configuration.
	unguarded := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	for _, line := range sectionCommands(section) {
		if strings.HasPrefix(line, "gcloud ") &&
			!strings.Contains(line, `--project="${P:?}"`) &&
			!strings.Contains(line, `"${P:?}"`) &&
			!strings.Contains(line, `projects/${P:?}`) {
			t.Errorf("the Check 50 section has a gcloud command that does not name the project: %q", line)
		}
		if m := unguarded.FindString(line); m != "" {
			t.Errorf("the Check 50 section has an unguarded %s in a command: %q", m, line)
		}
	}
	var sawDoctor bool
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro doctor --project fugaro") {
			sawDoctor = true
			continue
		}
		// The installation is named wherever the command can be told which one
		// to act on. `fugaro validate` is the exception it has to be: it has no
		// --project flag at all (it resolves the project from the checkout, the
		// way `fugaro doctor` does without the flag), so a line that carried one
		// would be a command line nobody can run — which the flag check below
		// also fails on.
		if cmd := fugaroCommandFor(line); cmd == nil || cmdHasFlag(cmd, "project") {
			if !strings.Contains(line, "--project fugaro") {
				t.Errorf("the Check 50 section has a fugaro command that does not name the installation: %q", line)
			}
		}
		if m := unguarded.FindString(line); m != "" {
			t.Errorf("the Check 50 section has an unguarded %s in a fugaro command: %q", m, line)
		}
	}
	if !sawDoctor {
		t.Error("the Check 50 section's guard block is not read as a command, so nothing checks that it pins the installation")
	}

	// Every `fugaro` command the check shows is one this binary has: the
	// subcommands resolve in the cobra tree and each --flag belongs to that
	// command or one of its ancestors (TestDocsProjectLayerCommandsExist's rule,
	// over this section). The owner runs these by hand, so a flag that does not
	// exist is a step that stops at "unknown flag" — the reason this check is
	// here is the review of this change, which found `fugaro validate --project
	// fugaro` in the section: validate declares no --project, and the rule above
	// had required one on every line.
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	for _, line := range fugaroCommands(section) {
		cmd := fugaroCommandFor(line)
		if cmd == nil {
			t.Errorf("the Check 50 section has a `fugaro` line that is not a command this binary has: %q", line)
			continue
		}
		for _, m := range flagRE.FindAllStringSubmatch(commandPart(line), -1) {
			if !cmdHasFlag(cmd, m[1]) {
				// The line as the doc writes it, and the command it resolves
				// to: the two differ only for the retired spelling, and naming
				// both is what says which door the flag was looked for on.
				t.Errorf("the Check 50 section's %q shows --%s, which %s does not declare: the owner would meet 'unknown flag' at that step", commandPart(line), m[1], cmd.CommandPath())
			}
		}
	}

	// Step 7's Restore is the one destructive block in this check: step 3
	// published the switch, so a `--delete` that runs whatever the state was
	// would delete a policy the owner had published and never asked this check
	// to touch. Step 1 therefore RECORDS the pre-check policy state and step 7
	// branches on that record, refusing when it is missing.
	step1 := chk50Step(t, section, "### 1.")
	step7 := chk50Step(t, section, "### 7.")
	stateFile := "/tmp/chk50-policy-state-before.txt"
	chk50MustContain(t, step1, "step 1", []string{stateFile})
	if !regexp.MustCompile(`(?m)^[^#\n]*>[^|\n]*` + regexp.QuoteMeta("chk50-policy-state-before.txt")).MatchString(step1) {
		t.Errorf("Check 50 step 1 does not WRITE the pre-check policy state to %s: step 7 branches on that record, and a restore whose record nothing writes is a restore that guesses", stateFile)
	}
	chk50MustContain(t, step7, "step 7", []string{
		stateFile,
		"no-policy",
		"policy-no-routing",
		"policy-routing",
		"--remove-routing",
		"exit 1",
	})
	if !strings.Contains(step7, `case "$(cat "$STATE")" in`) {
		t.Error("Check 50 step 7 does not branch on the state step 1 recorded: the three restores (delete, drop the switch, republish) are chosen by that record and by nothing else")
	}
	// Every `--delete` in the step's own blocks must sit inside the ONE arm
	// that tests the recorded state: a plain line whose only guard is a
	// trailing comment runs whatever the state was, and a destructive command
	// that has escaped its arm is the worst thing this check can ship.
	for _, f := range chk50DeleteFindings(step7) {
		t.Error(f)
	}
	// The scan's own teeth, checked as mutations of the real text: each of
	// these is a way the destructive call has escaped its arm, and each must
	// be reported. The third is why the rule is about QUOTES and not about the
	// line's first word: `echo restoring; fugaro … --delete …` is a command
	// line whose message merely starts with `echo`, and a "skip anything
	// starting with echo" rule walked straight past it — as it did past the
	// step's own `|| { echo "STOP: … --delete … not succeed" >&2; exit 1; }`
	// failure handlers, which are why the flag inside a message is skipped at
	// all.
	for _, mut := range []struct{ name, old, new string }{
		{"an unconditional --delete after the copy of the operator's config",
			`cp "$C.before-50" "$C"`, "cp \"$C.before-50\" \"$C\"\nfugaro project policy publish --delete --project fugaro"},
		{"an unconditional --delete after the case block",
			"esac\n", "esac\nfugaro project policy publish --delete --project fugaro\n"},
		{"an --delete on an echo-prefixed line before the case",
			`case "$(cat "$STATE")" in`, "echo restoring; fugaro project policy publish --delete --yes --project fugaro\ncase \"$(cat \"$STATE\")\" in"},
		{"an --delete inside the policy-no-routing arm",
			"    if fugaro project policy publish --remove-routing --project fugaro; then",
			"    fugaro project policy publish --delete --project fugaro\n    if fugaro project policy publish --remove-routing --project fugaro; then"},
	} {
		if !strings.Contains(step7, mut.old) {
			t.Errorf("the mutation %q no longer applies to Check 50 step 7 (%q is not in it): the scan below is then untested against that escape", mut.name, mut.old)
			continue
		}
		if f := chk50DeleteFindings(strings.Replace(step7, mut.old, mut.new, 1)); len(f) == 0 {
			t.Errorf("the --delete scan does not report %s: the destructive command can leave its arm and the check still passes", mut.name)
		}
	}
	// And the missing-record case stops the step instead of guessing: the test
	// of the record is followed by an `exit`, in the same branch — a guard whose
	// message says "STOP" and then runs on is not a stop.
	var guarded bool
	for _, block := range chk49FencedBlocks(step7) {
		lines := strings.Split(block, "\n")
		for i, line := range lines {
			if !strings.Contains(line, "[ ! -s ") || !strings.Contains(line, "STATE") {
				continue
			}
			for j := i + 1; j < len(lines) && j <= i+5; j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "exit ") {
					guarded = true
				}
			}
		}
	}
	if !guarded {
		t.Errorf("Check 50 step 7 does not refuse (exit) when the recorded state is missing or unreadable (no `exit` follows the `[ ! -s ... ]` test in %s): guessing the restore is the destructive case this check must never run", stateFile)
	}
	// The one destructive arm must not REPORT a deletion that did not happen.
	// `--delete` without `--yes` asks for the typed confirmation ("Type fugaro
	// to confirm") and, with stdin not a terminal — which is what the check's
	// own "keep the block in one file and run bash chk50.sh" convention gives
	// it — fails outright instead. An arm that echoes its result without
	// running the command under a status test reports the restore as done
	// while the policy this check published is still published, and this is
	// the check's only destructive moment.
	chk50MustContain(t, step7, "step 7", []string{
		// What the operator will be asked, named where the command is.
		"Type fugaro to confirm",
		// And the guard itself: the delete runs under a test of its status.
		"if fugaro project policy publish --delete --project fugaro; then",
		// The failure says the deletion did NOT happen, so no one reads this
		// arm as a completed restore.
		"STILL PUBLISHED",
	})
	for _, arm := range []string{
		"if fugaro project policy publish --remove-routing --project fugaro; then",
		"if fugaro project policy publish --project fugaro; then",
	} {
		if !strings.Contains(step7, arm) {
			t.Errorf("Check 50 step 7 does not test its command's status in one of its restore arms (%q): an arm that echoes its result without checking reports a restore that did not happen", arm)
		}
	}

	// TWO OF THE ARMS CAN BE REFUSED BY DESIGN, and a STOP that calls that an
	// outage leaves the check's own switch published. This step restores the
	// operator's config first and then asks the CLI to drop or republish the
	// routing block: buildProjectPolicy refuses `--remove-routing` when that
	// config writes a routing.claude switch of its own (`case !silent &&
	// removeRouting`) and refuses a plain republish when the config names no
	// Claude switch while the published node routes Claude
	// (`nodeRoutesClaude(old)`). Either way the arm fails — and what is still
	// published is the `routing.claude: via openrouter` step 3 published, which
	// routes every Claude senior in the project through the provider at
	// provider fees. So the step says what those two refusals are and what to
	// do about them, in the prose the owner reads before running the block and
	// in each arm's own failure message.
	chk50MustContain(t, step7, "step 7", []string{
		"internal/cli/project_cmd.go",
		// What the refusal leaves behind, named as the money it costs.
		"routes every Claude senior in this project through the provider at provider fees",
		// And the remedy, not just a STOP.
		"comment out",
	})
	if s := chk50Step(t, section, "### 7."); !strings.Contains(s, "disagree about the switch") {
		t.Error("Check 50 step 7 does not say the config it puts back and the policy it restores can disagree about the switch: both refusals above are by design, and step 1 prints both halves while nothing has been changed yet, so the prose is where the owner can still avoid the arm")
	}
	// And the step's own record of the other half: the node's routing member,
	// printed beside the config's `routing:` line, so the two can be compared
	// before a step changes anything.
	chk50MustContain(t, step1, "step 1", []string{"jq -c '.policy.routing'"})

	// STEP 7'S FACT ASKS FOR TWO THINGS NO COMMAND IN IT PRODUCED: the job's
	// environment names and the anthropic-api-key version's state. A FACT whose
	// evidence the step never reads is a line the owner cannot paste back, so
	// the step reads both (read-only, after the restore, before the records are
	// removed).
	chk50MustContain(t, step7, "step 7", []string{
		`gcloud run jobs describe "${JOB:?}"`,
		"gcloud secrets versions describe",
		`grep -E '^state:'`,
	})

	// The `route` attribute is emitted only for a call that went to a provider
	// (`cl.route = rt.provider.Name` inside `if rt.provider != nil`, and the
	// log appends the attribute only when it is non-empty), so the DIRECT
	// run's reviewer line carries no `route` at all: its absence is what says
	// the call went to Anthropic. A FACT that asks for `route` on both runs
	// names a field the baseline never prints.
	for _, tc := range []struct{ step, marker string }{
		{"### 4.", "step 4"},
		{"### 6.", "step 6"},
	} {
		if s := chk50Step(t, section, tc.step); !strings.Contains(s, "no `route` attribute") {
			t.Errorf("Check 50 %s does not say the direct run's line carries no `route` attribute: the gateway emits that attribute only for a call that went to a provider, so the FACT names a field the direct run never prints", tc.marker)
		}
	}

	// THE RECORDS BELONG TO ONE ATTEMPT, and the destructive arm is why. The
	// scenario the pins below exist for: run the check while no policy is
	// published, publish a real policy, run the check again — a `no-policy`
	// word kept from the first attempt deletes the policy published since. So
	// the guard writes a start marker, step 1 writes the records fresh and
	// REFUSES when they are already there (naming them and the command to
	// remove them), step 7 refuses to restore from a record older than the
	// marker, and step 7 removes every record once the restore has succeeded.
	chk50MustContain(t, section, "section", []string{
		"START=/tmp/chk50-run-started-at",
		`[ -e "$START" ] || : > "$START"`,
		`[ -n "$START" ]`,
	})
	chk50MustContain(t, step1, "step 1", []string{
		// A failed or unset command must stop the block, not write an empty
		// record that step 7 would then branch on.
		"set -u",
		"left from an earlier attempt",
		"rm -f ",
		"jq -e .",
		"/tmp/chk50-policy-before.json",
		"/tmp/chk50-sandbox-yaml-before.txt",
		`"$C.before-50"`,
	})
	chk50MustContain(t, step7, "step 7", []string{
		"set -u",
		"older than this attempt's start marker",
		// The copy of the operator's config is what the republish arm reads:
		// an unchecked `cp` leaves the check's own switch in place and the
		// arm then republishes THAT under a message saying it is step 1's.
		`cp "$C.before-50" "$C" ||`,
		"records: removed",
	})

	// Step 5 records ONE number, and the task both runs carry is one that
	// commits: a run whose agent commits nothing is recorded not-ready ("the
	// agent made no commits"), and whether its reviewer stage makes a model
	// call at all is UNVERIFIED — the baseline must not rest on it.
	chk50MustContain(t, chk50Step(t, section, "### 5."), "step 5", []string{
		"**The number this check records is ONE figure:**",
		"the routed run's own `cache_read`",
	})
	chk50MustContain(t, section, "section", []string{
		"TASK50=",
		"UNVERIFIED",
		"record this as *not produced*",
	})
	chk50MustContain(t, chk50Step(t, section, "### 4."), "step 4", []string{
		"internal/runner/runner.go",
	})
	for _, tc := range []struct{ step, marker string }{
		{"### 4.", "step 4"},
		{"### 6.", "step 6"},
	} {
		if s := chk50Step(t, section, tc.step); !strings.Contains(s, `fugaro run "$TASK50"`) {
			t.Errorf("Check 50 %s does not run the comparison's task ($TASK50): the guard's no-change TASK leaves the agent with nothing to commit, and a run that commits nothing is not the run this check's number is read from", tc.marker)
		}
	}

	// Task 1b.3 shipped with phase 1b, so its output is this binary's — and
	// every FACT line that names it still has a record for the binary that
	// predates the release, because a check may be run against an older
	// install. What "not landed" looks like is per step, and asking them for
	// the same word would be wrong: step 3's resolution line is simply absent
	// from such a binary, while step 2's missing-key note IS printed — it is
	// the note that names no switch (anthropicKeyNeeded, and the older
	// MissingAnthropicKey it quotes) — so step 2 records this FACT as
	// "Task 1b.3 not in this binary". A step that stops, or that reads as if
	// the older binary's form must exist, contradicts the opener and leaves
	// the owner with a check that cannot be finished.
	for _, tc := range []struct{ step, marker, want, why string }{
		{"### 2.", "step 2", "Task 1b.3 not in this binary",
			"this binary does print a missing-key note; it only does not name the switch, so a binary without 1b.3 is not silent here"},
		{"### 3.", "step 3", "not produced",
			"this binary prints no resolution line at all"},
	} {
		if s := chk50Step(t, section, tc.step); !strings.Contains(s, tc.want) {
			t.Errorf("Check 50 %s does not record Task 1b.3's absent output as %q (%s): the opener rules that a step whose output the task has not landed to print is recorded and not treated as a failure, and this step must say the same", tc.marker, tc.want, tc.why)
		}
	}
	// "the old one that names no switch" is only actionable if the step names
	// the message it means: without it the owner cannot tell 1b.3's absence
	// from a wrong answer, which the same step calls a failure.
	if s := chk50Step(t, section, "### 2."); !strings.Contains(s, "anthropicKeyNeeded") || !strings.Contains(s, "not a failure") {
		t.Error("Check 50 step 2 does not name both the message it records as \"Task 1b.3 not in this binary\" (anthropicKeyNeeded) and that the record is \"not a failure\": the old note is what tells the owner this is 1b.3's absence rather than the wrong answer the step stops on")
	}

	// No step handles a secret VALUE: the check reads a version number, a set
	// of env names, log lines and result.json. The provider key and the
	// Anthropic key are both never read, printed or recorded here.
	for _, dead := range []string{
		"gcloud secrets versions access", "gcloud secrets create", "--data-file",
		"print-access-token", "ANTHROPIC_API_KEY=", "OPENROUTER_API_KEY=",
	} {
		if strings.Contains(section, dead) {
			t.Errorf("the Check 50 section handles a secret value (%q): this check reads a version number and env names, never a value", dead)
		}
	}
	chk50MustContain(t, section, "section", []string{
		"never a value",
	})

	// The section's code citations, resolved against the tree by the SYMBOL
	// each one names. Check 50 is the section this PR writes, so its citations
	// are the ones a reader of this PR follows.
	checkCitations(t, section, "Check 50 section", chk50Citations)
	chkNoLineCites(t, section, "Check 50 section")

	// The numbering ruling names the number, so a later plan does not take it —
	// and says Task 1b.3's output is in this binary, so a reader is not sent to
	// record an output this tree does produce as missing. It said the opposite
	// ("not in this binary yet") until the three outputs shipped: doctor.go's
	// routing-cache-markers info, validate.go's resolution line, and the
	// missing-key note that names the switch.
	ruling := strings.Join(strings.Fields(doc), " ")
	if !strings.Contains(ruling, "Check 50 is phase 1b's") {
		t.Error("the Numbering ruling does not name Check 50 as phase 1b's")
	}
	if !strings.Contains(ruling, "Task 1b.3's output, which **has shipped**") {
		t.Error("the Numbering ruling does not say `fugaro doctor`'s routing-cache-markers info is Task 1b.3's and has shipped: doctor.go prints it and validate.go prints the resolution line, so a ruling that calls them missing sends the owner to record present output as absent")
	}
	if strings.Contains(ruling, "not in this binary yet") {
		t.Error("the Numbering ruling still says Task 1b.3's output is `not in this binary yet`: internal/cli/doctor.go prints the routing-cache-markers info and internal/cli/validate.go prints the resolution line, so the ruling denies an output this tree produces")
	}

	// The three claims the section carried while Task 1b.3 was unshipped, and
	// which the tree the check ships against makes false. Any of them coming
	// back means the checklist tells the owner to record present output as
	// absent, so they are refused here by name and not only through the
	// positive pins above. The section's own reading is the other way round:
	// the outputs are this binary's, and the older binary is the exception the
	// record is written for.
	for _, stale := range []string{
		"1b.3 has not shipped",
		"this binary prints no `routing-cache-markers` info",
		"this binary's `fugaro validate` prints no routing-resolution line at all",
	} {
		if strings.Contains(section, stale) {
			t.Errorf("the Check 50 section says %q: Task 1b.3 shipped with phase 1b, so this binary prints doctor's `routing-cache-markers` info and validate's resolution line, and a section that calls them missing has the owner record them as not produced", stale)
		}
	}
}

// chk50StepMarkers is the Check 50 section's steps in the order they appear,
// with the closing paragraph as the last boundary so step 7's slice ends where
// the section's FACT walk should.
var chk50StepMarkers = []string{
	"### 1.", "### 2.", "### 3.", "### 4.", "### 5.", "### 6.", "### 7.",
	"**Paste back:**",
}

// chk50Step returns one step of the Check 50 section: from its marker to the
// next step's, so a FACT line is attributed to the step that carries it rather
// than to the section as a whole. A marker that is missing, or out of order,
// fails the test: the pins below are only as good as the slicing.
func chk50Step(t *testing.T, section, marker string) string {
	t.Helper()
	i := -1
	for k, m := range chk50StepMarkers {
		if m == marker {
			i = k
			break
		}
	}
	if i < 0 {
		t.Fatalf("chk50Step(%q): not one of the Check 50 section's step markers", marker)
	}
	from := strings.Index(section, marker)
	if from < 0 {
		t.Fatalf("the Check 50 section has no %q step", marker)
	}
	to := len(section)
	for _, next := range chk50StepMarkers[i+1:] {
		if j := strings.Index(section[from+len(marker):], next); j >= 0 {
			to = from + len(marker) + j
			break
		}
	}
	if to <= from {
		t.Fatalf("the Check 50 section's %q step is empty: %q must come after it", marker, chk50StepMarkers[i+1])
	}
	return section[from:to]
}

// chk50DeleteFindings reports every `--delete` in the Check 50 step 7 blocks
// that a shell would run somewhere other than the one arm of the case that
// tests the state step 1 recorded. Each item is a failure message; an empty
// result means the destructive call is where it belongs. It is a function of
// the text so the test can also run it over mutations of the real block.
func chk50DeleteFindings(step7 string) []string {
	var out []string
	armRE := regexp.MustCompile(`^(no-policy|policy-no-routing|policy-routing)\)`)
	for _, block := range chk49FencedBlocks(step7) {
		lines := strings.Split(block, "\n")
		for i, line := range lines {
			// A `--delete` inside quotes is text in a message — the failure
			// handlers below say `echo "STOP: --delete did not succeed …"`, and
			// a `--yes` line is quoted in Check 49's prose — while an UNQUOTED
			// one is an argument to something this block runs. Any line whose
			// quotes are unclosed is held to the rule as well: a scanner that
			// cannot read the line must not wave it through.
			if !chk50Unquoted(line, "--delete") {
				continue
			}
			arm := ""
			for j := i - 1; j >= 0; j-- {
				if m := armRE.FindStringSubmatch(strings.TrimSpace(lines[j])); m != nil {
					arm = m[1]
					break
				}
			}
			if arm != "no-policy" {
				out = append(out, fmt.Sprintf("Check 50 step 7 runs %q and it is not inside the branch that tests the state step 1 recorded (it is in the %q arm, or in no arm at all): --delete may run only when step 1 recorded that no policy was published", strings.TrimSpace(line), arm))
			}
		}
	}
	return out
}

// chk50Unquoted reports whether token occurs in line outside single and double
// quotes, i.e. whether a shell would read it as an argument rather than as part
// of a message. An odd number of quotes leaves the line "open" at its end, and
// the token is then treated as unquoted — the strict reading.
func chk50Unquoted(line, token string) bool {
	// A whole-line comment is not a command either: the step's own block
	// explains the arm rule in one.
	if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
		return false
	}
	var inSingle, inDouble bool
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == '\\' && !inSingle && i+1 < len(line):
			i += 2
			continue
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble && strings.HasPrefix(line[i:], token):
			return true
		}
		i++
	}
	return false
}

// commandPart is the command half of a section line: everything before the
// first pipe, since what follows it belongs to another program (`grep`, `jq`)
// whose flags are not the fugaro command's.
func commandPart(line string) string {
	before, _, _ := strings.Cut(line, " | ")
	return before
}

// fugaroCommandFor resolves a `fugaro …` line to the command the check means
// the owner to run, or nil when the line names no command of this binary. The
// words after `fugaro` are walked down the cobra tree until one is not a
// subcommand — a flag, an argument such as a run name or a checkout path, or
// one of the `<placeholder>` forms those lines use — and the last command that
// resolved is the one whose flags the line is held to.
func fugaroCommandFor(line string) *cobra.Command {
	rest := strings.TrimPrefix(strings.TrimSpace(stripCommandPrefix(commandPart(line))), "fugaro ")
	cur := NewRootCmd()
	// The words walked are the doc's own, and no line needs a translation any
	// more: the last live document that wrote the retired spelling of the
	// repository door is docs/gcp-live-checklist.md, and W2.6 renamed it, so
	// `docCommandWords` (which mapped that word onto the front door) is gone
	// with it. A line that writes the tombstone is a line this walk resolves to
	// the tombstone, which declares no flags — and the census in
	// docs_live_init_census_test.go is the check that fails first.
	for _, f := range strings.Fields(rest) {
		if strings.HasPrefix(f, "-") {
			break
		}
		next, _, err := cur.Find([]string{f})
		if err != nil {
			// Neither a subcommand of cur nor an argument it takes: the line
			// names something this binary has no command for.
			return nil
		}
		if next == cur {
			break
		}
		cur = next
	}
	if cur == nil {
		return nil
	}
	return cur
}

// chk50MustContain fails the test for each want that is not in got, naming the
// part of the section (its body, its table row, the whole section) that should
// hold it.
func chk50MustContain(t *testing.T, got, part string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the Check 50 section's %s is missing %q", part, w)
		}
	}
}

// chk49Citations are the code citations the Check 49 section makes in full.
// Resolved against the tree by checkCitations: every one is the PATH plus the
// symbol the sentence names, with no line number, because a merge that moves
// code under a number is what made these drift in the first place — twice.
var chk49Citations = []chkCitation{
	{"internal/runner/policy.go", "func (r *run) policyCeiling"},
	{"internal/cli/project_cmd.go", "func requirePolicyRules"},
	{"internal/cli/run.go", "taskText(cmd, o.tf, args, prSet)"},
	{"internal/cli/run.go", "launchPolicyGates(ctx, env, slug, spec)"},
	{"internal/cli/run.go", "pass the task as TEXT or with --task-file (- for stdin)"},
	// The ref's own fallback moved with the ruling that gave the base branch
	// four layers: the launcher takes --ref as it is (a first run's spec is
	// buildAttemptSpec's) and resolves the ref through launchBaseBranch in
	// basebranch.go when the flag is empty. The number this sentence used to
	// carry ((:630), the shape chkNoLineCites cannot see) pointed at the
	// --plan-task gate by then, so both halves are named instead.
	{"internal/cli/run.go", "func buildAttemptSpec"},
	{"internal/cli/basebranch.go", "func launchBaseBranch"},
	{"internal/gitops/gitops.go", "\"fetch\", \"--quiet\", \"origin\", ref"},
	{"internal/runner/runner.go", "CheckoutNewBranch(ctx, spec.Ref, branch)"},
	{"internal/cli/cloud.go", "\"project\", \"\", \"the Fugaro project"},
	{"internal/cli/doctor.go", "\"project: %s (GCP %s, %s)\\n\""},
	{"internal/localcfg/select.go", "this checkout belongs to project"},
	// installRoot's own plan-only return. The bare identifier `planOnly` is all
	// over init.go, so the anchor is the line it prints: a token the file
	// carries exactly once.
	{"internal/cli/init.go", "--plan-only: nothing applied; the plan is"},
	{"internal/cli/init_firebase.go", "r.installRoot(ctx, c, t, wd, lc, spec)"},
	{"internal/cli/init_firebase.go", "r.applyRoot(ctx, c, ft, fwd, \"firebase\""},
	{"internal/cli/init_firebase.go", "already has the rules"},
	{"internal/cli/init_firebase.go", "\"The Firebase project %s:\\n\""},
	// The FACT for this citation names the two `deployDatabase` act lines
	// verbatim, so the anchors are those two acts — not `changes the mode`,
	// the same function's third act, which is what an old drifted number
	// (`firebasedb.go:222`) used to point at and which the sentence is not
	// about.
	{"internal/infra/firebasedb.go", "deploys the security rules"},
	{"internal/infra/firebasedb.go", "records the rules generation"},
	{"internal/cli/upgrade_cloud.go", "func jobConfigCopyNote"},
	{"internal/cli/image_refresh_steps.go", "func (r *initRun) refreshCloneJobs"},
	{"internal/cli/upgrade.go", "what fugaro update-skills does"},
	{"internal/cli/init_repo_stage.go", "s.anchor(ctx, env, s.e.r.o.planOnly)"},
	{"internal/cli/init_fugaroyaml.go", "\"Write this to %s? [y/N]: \""},
	{"internal/cli/init_fugaroyaml.go", "func (s *repositoryStage) anchor"},
	{"internal/projectpolicy/match.go", "func Canonical(provider, repo string)"},
	{"internal/cli/project_cmd.go", "func allowSourcesOf"},
	{"internal/runstore/runstore.go", "runRefRE"},
	{"internal/runstore/runstore.go", "func ParseRef"},
	{"internal/cli/exec.go", "os.Getenv(\"FUGARO_RUN\")"},
	{"internal/runner/runner.go", "r.d.Store.ReadTask(ctx)"},
	{"internal/backend/gcp/names.go", "func BucketCondition"},
	{"internal/runner/project.go", "The run stops before any lease exists"},
	{"internal/infra/spec.go", "env[MaxRunUSDEnv]"},
}

// chk50Citations are the code citations the Check 50 section makes in full,
// resolved against the tree by checkCitations.
var chk50Citations = []chkCitation{
	{"internal/runner/routing.go", "func (r *run) applyRouting"},
	{"internal/runner/routing.go", "r.gatewayOn()"},
	{"internal/runner/routing.go", "config.RouteAuto"},
	{"internal/config/routing.go", "func CheckRouting"},
	{"internal/config/routing.go", "func RoutingClaudeFor"},
	{"internal/config/config.go", "const MissingAnthropicKey"},
	{"internal/config/config.go", "dec.KnownFields(true)"},
	{"internal/pricing/overrides.go", "EnvName = \"FUGARO_MODEL_PRICES\""},
	{"internal/infra/spec.go", "env[ModelPricesEnv]"},
	{"internal/cli/cloud.go", "\"project\", \"\", \"the Fugaro project"},
	{"internal/cli/doctor.go", "\"project: %s (GCP %s, %s)\\n\""},
	// Step 2's "the old note that names no switch": the message a binary
	// without Task 1b.3 prints, and the reason the step records it instead of
	// stopping on it.
	{"internal/cli/validate.go", "const anthropicKeyNeeded"},
	{"internal/cli/validate.go", "const anthropicKeyNoEnforce"},
	{"internal/cli/validate.go", "msg := anthropicKeyNeeded"},
	{"internal/cli/validate.go", "\"note: \"+n.String()"},
	{"internal/cli/validate.go", "never a warning or a refusal"},
	// Step 7's `no-policy` arm: --delete without --yes asks for the typed
	// confirmation (confirmTyped) and fails when stdin is not a terminal.
	{"internal/cli/project_cmd.go", "confirmRemoval(cmd, lc, o.yes"},
	{"internal/cli/budget.go", "func confirmTyped"},
	{"internal/cli/budget.go", "\"Type %s to confirm: \""},
	// Steps 4 and 6: the `route` attribute exists only for a provider call.
	{"internal/gateway/log.go", "is the Anthropic upstream"},
	{"internal/gateway/log.go", "attrs = append(attrs, \"route\", c.route)"},
	{"internal/gateway/forward.go", "cl.route = rt.provider.Name"},
	// Step 4: why the two compared runs carry a task that COMMITS — a run with
	// nothing ahead of its base is failed with this message, so a no-change run
	// is not the run step 5's number is read from.
	{"internal/runner/runner.go", "r.fail(\"the agent made no commits\")"},
	// Step 7's two by-design refusals: `--remove-routing` beside a config that
	// writes the switch, and a republish from a config that names no Claude
	// switch while the published node routes Claude. Either one fails the arm
	// and leaves the check's own switch published, so the step names them where
	// the arms run.
	{"internal/cli/project_cmd.go", "func buildProjectPolicy"},
	{"internal/cli/project_cmd.go", "nodeRoutesClaude(old)"},
	{"internal/cli/project_cmd.go", "case !silent && removeRouting"},
	{"internal/cli/project_policy_test.go", "--remove-routing beside a config that writes the switch"},
}

// chk52Citations are the code citations the Check 52 section makes in full,
// resolved against the tree by checkCitations.
var chk52Citations = []chkCitation{
	{"internal/backend/gcp/run.go", "op.Metadata"},
	{"internal/backend/gcp/run.go", "func (b *Backend) Launch"},
	{"internal/backend/gcp/run.go", "func (b *Backend) toExecution"},
	{"internal/cli/exec.go", "os.Getenv(\"FUGARO_RUN\")"},
	{"internal/localcfg/select.go", "this checkout belongs to project"},
}

// chkCitation is one code citation a checklist section makes: the text the
// document carries, and a token — a symbol, a signature, a sentence — that has
// to be in the file it names. The citations are the way an owner reads the code
// behind a FACT, so a number that has drifted leaves them on unrelated code.
//
// The citation is preferred WITHOUT a line number: `internal/x/y.go` plus the
// symbol the section names, which is the form a code move cannot break. A
// number, or a range, is kept only where the section is about lines as such —
// and then the token has to be inside it.
//
// A pin on the NUMBER cannot hold a drift: the same reading that produces the
// number produces the next one, and until someone makes that reading the pin
// keeps passing while the sentence points somewhere else. Resolving the token
// against the tree is what makes a drift fail — and a symbol that stays put is
// what keeps it from failing on every merge.
type chkCitation struct{ cite, anchor string }

// parseCitation reads "internal/x/y.go" (the preferred form: resolve the
// anchor anywhere in the file), "internal/x/y.go:123" or
// "internal/x/y.go:123-130". numbered reports which form it read.
func parseCitation(cite string) (path string, first, last int, numbered bool) {
	i := strings.LastIndex(cite, ":")
	if i < 0 {
		return cite, 0, 0, false
	}
	p, spec := cite[:i], cite[i+1:]
	if j := strings.Index(spec, "-"); j >= 0 {
		first, last = atoiOrZero(spec[:j]), atoiOrZero(spec[j+1:])
	} else {
		first, last = atoiOrZero(spec), atoiOrZero(spec)
	}
	if first <= 0 || last < first {
		return cite, 0, 0, false
	}
	return p, first, last, true
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// checkCitations resolves each citation against the tree, from internal/cli to
// the repository root, and PREFERS THE SYMBOL: a citation without a line number
// is satisfied by the token being anywhere in the file, which is what survives
// a merge that moves code. A citation that carries a number — or a range — has
// to hold its token inside it, and when it does not, the failure says where the
// token is now, so the next drift is one edit rather than a search.
//
// Two failures it reports: the section no longer carries the citation at all
// (the doc and this table have to move together), and the citation is there but
// the token is not where it says (the number drifted off the code the sentence
// is about, or the symbol the sentence names is gone).
func checkCitations(t *testing.T, section, part string, cites []chkCitation) {
	t.Helper()
	for _, c := range cites {
		if !strings.Contains(section, c.cite) {
			t.Errorf("the %s no longer carries the citation %q: if the code moved, fix the section and this table in the same commit — the table is what resolves the citation against the tree", part, c.cite)
			continue
		}
		path, first, last, numbered := parseCitation(c.cite)
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("the %s cites %s, which cannot be read from here: %v", part, c.cite, err)
			continue
		}
		lines := strings.Split(string(data), "\n")
		if !numbered {
			if !chkCitationIn(lines, c.anchor, 1, len(lines)) {
				t.Errorf("the %s cites %s for %q, and no line of %s carries it: the symbol the sentence names is not in the file at all — fix the citation and this table in the same commit", part, c.cite, c.anchor, path)
			}
			continue
		}
		if last > len(lines) {
			t.Errorf("the %s cites %s, and %s has %d lines: the citation is past the end of the file", part, c.cite, path, len(lines))
			continue
		}
		if !chkCitationIn(lines, c.anchor, first, last) {
			t.Errorf("the %s cites %s for %q, and that is not in %s:%d-%d (which reads %q … %q): the citation points at code that is not what the sentence names%s",
				part, c.cite, c.anchor, path, first, last, strings.TrimSpace(lines[first-1]), strings.TrimSpace(lines[last-1]), chkCitationMoved(lines, c.anchor, path))
		}
	}
}

// chkCitationIn reports whether the token is on a line of lines[first-1:last].
func chkCitationIn(lines []string, anchor string, first, last int) bool {
	for n := first; n <= last && n <= len(lines); n++ {
		if strings.Contains(lines[n-1], anchor) {
			return true
		}
	}
	return false
}

// chkNoLineCites holds the rule the citation tables follow: a checklist section
// cites code by PATH AND SYMBOL, never by `path.go:NNN`. A number is true only
// until the next merge — the merge with next moved every one of the numbers
// this test used to carry, twice — while the symbol the sentence names is what
// the reader actually needs and what the tree keeps.
//
// A relative abbreviation inside a section (`(:229)`, `(:951)`) is not matched:
// it has no path, and the phrase pins in each section are what hold those.
func chkNoLineCites(t *testing.T, section, part string) {
	t.Helper()
	for _, m := range chkLineCiteFindings(section) {
		t.Errorf("the %s cites %q: cite the file and the symbol the sentence names instead (`internal/x/y.go`, the function or constant), because a line number is one merge away from pointing at unrelated code", part, m)
	}
}

// chkLineCiteFindings is the scan behind chkNoLineCites, as a plain function so
// the mutation rows below can hold it to what it must catch.
func chkLineCiteFindings(section string) []string {
	return regexp.MustCompile("[A-Za-z0-9_][A-Za-z0-9_./-]*\\.go:[0-9]+").FindAllString(section, -1)
}

// TestChecklistHasCheck50CitationsPreferSymbols holds the citation rule to
// itself, for all three sections that follow it (49, 50 and 52): the resolver
// reads both forms, and the scan that keeps the sections on the symbol form
// catches a line number however it is written and stays silent on the form the
// sections now use. Without this, a scan that quietly matched nothing would
// look exactly like three sections that follow the rule.
func TestChecklistHasCheck50CitationsPreferSymbols(t *testing.T) {
	type parseCase struct {
		cite        string
		path        string
		first, last int
		numbered    bool
	}
	for _, tc := range []parseCase{
		{"internal/x/y.go", "internal/x/y.go", 0, 0, false},
		{"internal/x/y.go:12", "internal/x/y.go", 12, 12, true},
		{"internal/x/y.go:12-30", "internal/x/y.go", 12, 30, true},
	} {
		path, first, last, numbered := parseCitation(tc.cite)
		if path != tc.path || first != tc.first || last != tc.last || numbered != tc.numbered {
			t.Errorf("parseCitation(%q) = %q %d %d %v, want %q %d %d %v", tc.cite, path, first, last, numbered, tc.path, tc.first, tc.last, tc.numbered)
		}
	}
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"`internal/cli/project_cmd.go:470`", true},
		{"internal/backend/gcp/run.go:118-129", true},
		{"the refusal lives at internal/runner/runner.go:2121", true},
		{"`internal/cli/project_cmd.go`", false},
		{"`internal/runner/runner.go`, `r.fail(\"the agent made no commits\")`", false},
		{"a bare `(:229)` abbreviation has no path and is not this scan's", false},
	} {
		if got := len(chkLineCiteFindings(tc.text)) > 0; got != tc.want {
			t.Errorf("chkLineCiteFindings(%q) found a line citation = %v, want %v", tc.text, got, tc.want)
		}
	}
	if !chkCitationIn([]string{"a", "func f()", "b"}, "func f()", 2, 2) || chkCitationIn([]string{"a", "func f()", "b"}, "func f()", 1, 1) {
		t.Error("chkCitationIn does not hold an anchor to the range it is given: a numbered citation would pass wherever the token happened to be")
	}
}

// chkCitationMoved is the tail of a failed numbered citation: where the token
// is now, if it is anywhere in the file. The fix for a drift is to write the
// citation the way the table prefers — the path and the symbol, no number.
func chkCitationMoved(lines []string, anchor, path string) string {
	var at []string
	for i, l := range lines {
		if strings.Contains(l, anchor) {
			at = append(at, fmt.Sprintf("%d", i+1))
		}
	}
	if len(at) == 0 {
		return fmt.Sprintf("; no line of %s carries %q either, so the symbol is gone and the section needs a new one", path, anchor)
	}
	return fmt.Sprintf("; %s carries it at line(s) %s — cite %s with that symbol and no line number, which the next code move cannot break", path, strings.Join(at, ", "), path)
}
