package cli

import (
	"os"
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
func TestChecklistHasCheck52(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	// The heading itself carries the four markers the plan's Task 3.0 fixes
	// ("installation fugaro, sandbox only, never belong; run by you; NOT
	// RUN"), so it is asserted as a whole rather than as four substrings that
	// would also pass on the heading alone.
	const heading = "## Check 52: the four probes phase 3 depends on (installation fugaro, sandbox only, never belong; run by you; NOT RUN)"
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
	})
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
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
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
		`fugaro init --repo . --plan-only --project fugaro`,
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
		`internal/backend/gcp/run.go:103-107`,
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
//   - finding 15: only `fugaro init --repo` writes a job's environment, so the
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
//     `fugaro init --repo` reads the job environment out of it, must not run
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
// citation at all, since launchPolicyGates is called at run.go:449) put back.
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
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
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

	// Finding 15, both ways: the rollout is two owner steps, `fugaro init
	// --repo` is the only one that writes a job's environment, and the
	// command the plan named does not exist.
	chk49MustContain(t, section, "section", []string{
		"finding 15",
		"the only path that writes a job's environment is `fugaro init --repo`",
		"`fugaro upgrade`",
		"`fugaro image refresh`",
		"fugaro init --repo . --project fugaro",
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
	// `internal/runner/runner.go:863` and abbreviates the second to `(:922)`,
	// so the full-path entry in the absent list below cannot catch it — the
	// string `internal/runner/runner.go:862` occurs nowhere in the section.
	// Verified by mutation: rewriting the citation back to `(:862)` leaves a
	// full-path-only guard passing, and fails this one.
	chk49MustContain(t, chk49Step(t, section, "**(b) The run's own.**"), "step 6(b)", []string{
		"`admissionCheck` ever runs (`:922`)",
	})
	if strings.Contains(section, "`admissionCheck` ever runs (`:862`)") {
		t.Error("the Check 49 section's step 6(b) cites :862 for admissionCheck: the merge with next moved the call to :922, and :862 is now `func (r *run) bootstrap(...)`")
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
			// one's: --plan-only with --firebase stops after the installation
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
		"fugaro init --repo . --project fugaro",
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
	// swapping `fugaro init --repo` above the `cp`, both fail here.
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
		writeEnv      = "fugaro init --repo . --project fugaro"
	)
	if i := at(restoreConfig); i != 0 {
		t.Errorf("step 8's restore block has %q at command %d of its own block, want 0: the operator's config goes back before anything reads it, and the step's FACT prose repeats the string so only the block's own order can be checked", restoreConfig, i)
	}
	if i := at(writeEnv); i != 1 {
		t.Errorf("step 8's restore block has %q at command %d, want 1 — immediately after the config restore: `init --repo` writes the job environment out of that file (`internal/infra/spec.go:753`), so running it first writes whatever the config says now into the job, which is the inversion the step calls out", writeEnv, i)
	}
	for _, want := range []string{
		"git diff -- fugaro.yaml",
		"git diff -- .claude/settings.json",
		// The revert of the one edit the check can make to the sandbox's own
		// file: step 3's answer to `init --repo`'s gcp_project: prompt is `n`,
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
	// refused at run.go:220, before openCloud and long before the allow-list
	// gate at run.go:449 (`launchPolicyGates`; :441 is a comment inside
	// budgetPrecheck's block, which is the stale number the section is checked
	// against below), so a bare `fugaro run` in step 3, 5, 6(a), 7 or 8 never
	// reaches what the step is there to produce.
	chk49MustContain(t, section, "section", []string{
		"TASK='Do not change any file and do not open a pull request. Reply with the single word ok.'",
		"Every `fugaro run` below carries a task, and it has to",
		// The runs' ref, and why it is master: the sandbox has no `main`, and
		// --ref is not cross-checked against the remote at launch.
		"Every `fugaro run` below passes `--ref master`, and it is `master` because the sandbox repository has no `main`",
		"`internal/gitops/gitops.go:239`",
		`fugaro run "$TASK" --workflow <the sandbox's workflow> --ref master --json --project fugaro`,
		`fugaro run "$TASK" --workflow <the sandbox's workflow> --ref master --project fugaro`,
	})
	// Every run names the sandbox's base branch, and it is `master`: the
	// sandbox repository has no `main` (deploy/sandbox/fugaro.yaml), and --ref
	// is carried into the job verbatim (run.go:817-823) and fetched by the
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
		"`internal/cli/upgrade.go:85-110` is that command's own `Long` text, one paragraph per step: `pin` at `:85-89`, `plugin` at `:90-94`, `cloud` at `:95-110`",
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
	// fugaro.yaml` FACT rests on knowing that. `fugaro init --repo`'s
	// repository stage ends by OFFERING the checkout's gcp_project: line —
	// init_repo_stage.go:163, Apply's `s.anchor(ctx, env, s.e.r.o.planOnly)`,
	// where the --plan-only run plans the same line and writes nothing (:143) —
	// through the anchor at init_fugaroyaml.go:243, the [y/N] prompt at :220
	// and the write at :230; --yes takes that prompt without asking. The check
	// runs `init --repo` in that checkout TWICE (step 3 and step 8), so the
	// section has to say which answer it wants and step 8 has to have a revert:
	// without both, "this is a confirmation that it is unchanged" is a
	// confirmation of an edit the check itself made, and the FACT above fails
	// on the operator's own checkout.
	chk49MustContain(t, chk49Step(t, section, "### 3."), "step 3", []string{
		"`init --repo` also offers the checkout's own `gcp_project:` line",
		"internal/cli/init_repo_stage.go:163",
		"internal/cli/init_fugaroyaml.go:243",
		"Write this to <path>? [y/N]:",
		"`internal/cli/init_fugaroyaml.go:220`",
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
		// NOT listed here: `internal/cli/project_cmd.go:352`. It was the wrong
		// number when it was added — :345 was the declaration then — but the
		// merge with next moved the whole function down and :352 is now where
		// `allowSourcesOf` is declared, which is what the section cites. A line
		// number in this list is only true until the next merge; it is kept off
		// so the test does not fail on a citation that is now correct.
		// The Firebase root's applyRoot at init_firebase.go:289 is what
		// --plan-only never reaches: the run has already returned at :210-213
		// on installRoot's plan-only stop (init.go:1285-1287). Citing :290 as
		// the stop points at code a --plan-only run cannot execute.
		"internal/cli/init_firebase.go:290",
		// Step 6(b)'s admissionCheck. :862 was the call when the section was
		// written and :922 is where the merge with next left it — the file grew
		// above it, and :862 is now `func (r *run) bootstrap(...)`, which is the
		// reader the same sentence cites at :863. The two numbers came from one
		// reading of the file and only one of them was updated. This entry
		// catches the citation only if it is ever written out in full; the
		// section abbreviates it, and the phrase pin above step 6 is what
		// actually holds it.
		"internal/runner/runner.go:862",
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
		"`internal/cli/init.go:1285-1287`, inside `installRoot` (declared at `:1171`)",
		"`internal/cli/init_firebase.go:210-213` (`if err != nil || stop { return err }`)",
		"`internal/cli/init_firebase.go:289`, the one whose stop this is **not**",
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
	// operator's config was never put back, and the `fugaro init --repo` that
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
		"`internal/runstore/runstore.go:411`",
		"--update-env-vars FUGARO_RUN=not-this-jobs-slug-000000000000/20261015-000000-dead",
		"a run id that names no run anywhere",
	})

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
		})
	}

	// Check 43's guard block inside the section — the three hard stops and the
	// project pinning, not merely a ${P:?...}.
	chk50MustContain(t, section, "section", []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$JOB" ] && [ -n "$C" ] && [ -n "$TASK" ] \`,
		// C is the operator's project config, and it is the block's and not
		// step 1's: step 7 restores it in a later shell, where step 1's shell
		// is gone.
		"C=$HOME/.config/fugaro/projects/fugaro.yaml",
		`|| { echo "Check 50 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
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
			"**FACT:** both token counts side by side",
			"**FACT:** the two `cost.model_usd` figures",
		}},
		{"### 6.", []string{
			"**FACT:** which route each of the two runs took",
			"**FACT:** that the `anthropic-api-key` version",
		}},
		{"### 7.", []string{
			"**FACT:** the restore is complete",
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

	// Every `fugaro run` carries the block's TASK and the sandbox's base
	// branch: a launch with no task is refused before any gate, and the sandbox
	// repository has no `main`.
	var runs int
	for _, line := range fugaroCommands(section) {
		if strings.HasPrefix(line, "fugaro run ") {
			runs++
			if !strings.Contains(line, `"$TASK"`) {
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
				t.Errorf("the Check 50 section's %q shows --%s, which that command does not declare: the owner would meet 'unknown flag' at that step", cmd.CommandPath(), m[1])
			}
		}
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

	// The numbering ruling names the number, so a later plan does not take it.
	ruling := strings.Join(strings.Fields(doc), " ")
	if !strings.Contains(ruling, "Check 50 is phase 1b's") {
		t.Error("the Numbering ruling does not name Check 50 as phase 1b's")
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
