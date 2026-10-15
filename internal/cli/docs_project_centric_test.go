package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
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
	for _, line := range chk52ProbeCommands(section) {
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
	for _, line := range chk52ProbeCommands(section) {
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
	for _, line := range chk52ProbeCommands(section) {
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
	for _, line := range chk52ProbeCommands(section) {
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

// chk52StripCommandPrefix removes the shell keywords and operators a command can be
// introduced by, so that `if gcloud …; then` and `… && gcloud …` are read as
// gcloud commands: U3's inside read is an `if gcloud secrets versions access
// …`, and a check that only looked at a line's first word would let that one
// drop its --project or interpolate an unguarded variable unnoticed.
func chk52StripCommandPrefix(line string) string {
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

// chk52ProbeCommands returns the section's `gcloud`, `curl` and `git` invocations
// as logical lines: backslash continuations are joined, leading shell keywords
// are stripped, and only lines that then start with one of those verbs count
// (so a command inside prose, a comment or a `$(…)` is not read as a command of
// its own).
func chk52ProbeCommands(section string) []string {
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
		line = chk52StripCommandPrefix(strings.TrimSpace(line))
		for _, verb := range []string{"gcloud ", "curl ", "git "} {
			if strings.HasPrefix(line, verb) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}
