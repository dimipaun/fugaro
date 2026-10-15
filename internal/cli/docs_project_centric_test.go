package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestChecklistHasCheck52: docs/gcp-live-checklist.md must carry phase 3's
// four UNVERIFIED probes (design project-centric.md §13 and §4.5; plan
// 2026-10-14-project-centric.md Task 3.0) as one owner-run, not-yet-run,
// sandbox-only check numbered 52 — with its row in the check list and Check
// 43's guard block (the three hard stops and the project pinning) inside its
// section, so a reader cannot mistake it for something CI covers and no step
// of it can run against a project other than fugaro-dev.
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
	mustContain(t, body, "body", []string{
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
	mustContain(t, row, "row", []string{
		"two override executions",
		"fugaro-chk52-probe",
		"fugaro-probe-in",
		"fugaro-other-probe",
		// Both projects are created for the check and both go in Restore: the
		// out-of-scope one holds fugaro-probe-3.
		"two scratch Bitbucket projects",
	})

	// Check 43's guard block inside the section — the three hard stops and
	// the project pinning, not merely a ${P:?...}.
	mustContain(t, section, "section", []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] \`,
		`|| { echo "Check 52 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 52 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 52 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
	})

	// The two stops this check adds on top of those three: the scratch
	// Bitbucket workspaces must not be edgeappinc's (case-insensitively: a
	// hand-typed EdgeAppInc must not slip past), and the out-of-scope project
	// must be in the same workspace as the in-scope one. Removing either stop
	// is removing the only thing that keeps U1 off production.
	mustContain(t, section, "section", []string{
		`[ -n "${PROBE_WS:?}" ] && [ "${PROBE_WS,,}" != edgeappinc ] && [ -n "${OUT_WS:?}" ] && [ "${OUT_WS,,}" != edgeappinc ] && [ -n "${OUT_PROJECT:?}" ] \`,
		`|| { echo "Check 52 stopped: PROBE_WS and OUT_WS must be non-empty scratch workspaces, never edgeappinc (compared case-insensitively)" >&2; exit 1; }`,
		`[ "${OUT_WS,,}" = "${PROBE_WS,,}" ] \`,
		`|| { echo "Check 52 stopped: OUT_WS must be the same workspace as PROBE_WS`,
	})

	// K1: a credential helper runs before GIT_ASKPASS, and this machine already
	// stores a bitbucket.org credential. Every probe must disable the helper —
	// in the scratch repo's own config and on every git command — and the check
	// must prove, without printing anything, that no helper can answer.
	mustContain(t, section, "section", []string{
		`git config --local --unset-all credential.helper`,
		`git config --local credential.helper ''`,
		`git credential fill`,
		`| grep -q '^password='`,
		`security find-internet-password -s bitbucket.org`,
	})
	if n := strings.Count(section, `-c credential.helper= -c credential.useHttpPath=true`); n < 5 {
		t.Errorf("the Check 52 section issues %d git probe commands with the credential helper disabled, want 5 (both repositories read and pushed, and the out-of-scope read)", n)
	}

	// I-b: the prompts work in bash and zsh alike; `read -p` is bash-only.
	mustContain(t, section, "section", []string{
		`read -rs PROBE_TOKEN`,
		`read -rs THROWAWAY`,
		`: "${PROBE_TOKEN:?the probe project access token you typed}"`,
	})
	if strings.Contains(section, "read -p ") {
		t.Error("the Check 52 section prompts with read -p, which is bash-only (zsh: no coprocess)")
	}

	// I-a: the baseline is recorded once and never overwritten, or Restore's
	// diff compares the job with itself.
	mustContain(t, section, "section", []string{
		"already recorded, not overwriting",
		`[ -e "${CHK52_TMP:?}/job-before.json" ]`,
	})

	// I-c: a fresh shell may still be standing in the sandbox checkout, so the
	// probe block proves where it is and that the repo is the scratch one.
	mustContain(t, section, "section", []string{
		`[ "$(pwd -P)" = "$(cd "${SCRATCH:?}" && pwd -P)" ] && ! git remote get-url origin >/dev/null 2>&1`,
		`this block must run inside the scratch checkout, which has no origin`,
	})

	// I-d: IAM propagation, and the API the impersonated reads need.
	mustContain(t, section, "section", []string{
		`gcloud services enable iamcredentials.googleapis.com --project="${P:?}"`,
		`for i in $(seq 1 12); do`,
		`sleep 10`,
		`if [ "$INSIDE" = ok ]; then`,
	})

	// I-e: one FACT line per step, each pinned by its own sentence.
	mustContain(t, section, "section", []string{
		"**FACT:** the recorded job env (names, never values)",
		"**FACT:** whether the token reads and pushes to exactly the project's repositories",
		"**FACT:** whether the execution-level override is accepted and whether the referenced value arrives (the length, never the value)",
		"**FACT:** the two exit statuses (`inside` after the poll window, `outside` only once `inside` was `ok`)",
		"**FACT:** the sandbox project's current service-account count and the quota it is counted against **as the API reports it**",
	})

	// The Restore: every artifact the check created is named with the command
	// that removes it. A Restore that stops after the first command leaves the
	// conditional grant, the probe account, a probe secret or a mode-700
	// directory of the job's plain env values behind.
	mustContain(t, section, "section", []string{
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
		`rm -rf "$SCRATCH"`,
		`rm -rf "${CHK52_TMP:?}"`,
		`unset PROBE_TOKEN; rm -f "${ASKPASS:-/nonexistent}"`,
		"the **three scratch repositories**",
		"**project access token revoked**",
		// Restore deletes BOTH projects, not only the in-scope one: the
		// out-of-scope project holding fugaro-probe-3 was created for U1 too.
		"**both probe projects**",
		"`PROBE_PROJECT` (holding `fugaro-probe-1` and `fugaro-probe-2`) and `OUT_PROJECT` (holding `fugaro-probe-3`",
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
	mustContain(t, section, "section", []string{
		"**Then re-run the guard block and the step's variables block, in that order**",
		"the step's variables block defines `SANDBOX_JOB`, `SANDBOX_JOB_SA`, `SANDBOX_BUCKET`, `NUMBER`, `PROBE_SA`, `PROBE_IN`, `PROBE_OUT`, `PROBE_COND` and `CHK52_TMP`",
	})
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
	for _, line := range probeCommands(section) {
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
	mustContain(t, section, "section", []string{
		`jobs/${SANDBOX_JOB:?}:run`,
		`OPERATION=$(jq -r '.name // empty' "${CHK52_TMP:?}/run-a.json")`,
		`jq -r '.metadata.name // empty'`,
		`internal/backend/gcp/run.go:103-107`,
		`"value":"plain-literal"`,
	})

	// U3: the condition names the project NUMBER (a project ID never matches),
	// never the ID; the reads are impersonated and send the payload to
	// /dev/null; and extracting the accessor grant before U2 is a step.
	if strings.Contains(section, "projects/${P:?}/secrets/") {
		t.Error("the Check 52 section writes the IAM condition with the project ID; Secret Manager resources are named by project NUMBER")
	}
	mustContain(t, section, "section", []string{
		`PROBE_COND="expression=resource.name.startsWith('projects/${NUMBER:?}/secrets/fugaro-probe')`,
		`resource.type == 'secretmanager.googleapis.com/Secret'`,
		`gcloud secrets add-iam-policy-binding "${PROBE_IN:?}" --project="${P:?}"`,
		`--role="roles/secretmanager.secretAccessor"`,
	})
	if n := strings.Count(section, `--impersonate-service-account="${PROBE_SA:?}"`); n < 2 {
		t.Errorf("the Check 52 section has %d impersonated secret reads, want at least 2 (inside and outside)", n)
	}
	if n := strings.Count(section, `--out-file=/dev/null`); n < 2 {
		t.Errorf("the Check 52 section sends %d secret payloads to /dev/null, want at least 2", n)
	}

	// A Bitbucket Cloud clone URL is https://bitbucket.org/<workspace>/<slug>.git:
	// the project is metadata, never a path segment.
	mustContain(t, section, "section", []string{
		`git remote add probe1 "https://bitbucket.org/${PROBE_WS:?}/fugaro-probe-1.git"`,
		`git remote add probe2 "https://bitbucket.org/${PROBE_WS:?}/fugaro-probe-2.git"`,
		`git -c credential.helper= -c credential.useHttpPath=true ls-remote "https://bitbucket.org/${OUT_WS:?}/fugaro-probe-3.git"`,
	})
	if strings.Contains(section, "bitbucket.org/${PROBE_WS:?}/${PROBE_PROJECT:?}") ||
		strings.Contains(section, "bitbucket.org/${OUT_WS:?}/${OUT_PROJECT:?}") {
		t.Error("the Check 52 section builds a Bitbucket clone URL with the project as a path segment")
	}
	// The second repository of the in-scope project is read AND pushed: a
	// removed probe-2 push would leave the project-scoping claim half-tested.
	for _, cmd := range []string{"ls-remote probe2", "push probe2 HEAD:probe-check"} {
		if !strings.Contains(section, cmd) {
			t.Errorf("the Check 52 section is missing the probe-2 command %q", cmd)
		}
	}

	// The closing paragraph, which is what the owner reads last.
	mustContain(t, section, "section", []string{
		"**Paste back:** every FACT line above, never a token, key or secret value.",
		"never point a probe at edgeserver or edgeweb**",
	})

	// Every command that acts on a project names it, and no gcloud/curl/git
	// command interpolates an unguarded `${VAR}`: an empty variable there is
	// an empty argument gcloud resolves from its ambient configuration.
	unguarded := regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	for _, line := range probeCommands(section) {
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
	for _, line := range probeCommands(section) {
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

// mustContain fails the test for each want that is not in got, naming the part
// of the section (its body, its table row, the whole section) that should hold
// it.
func mustContain(t *testing.T, got, part string, want []string) {
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

// probeCommands returns the section's `gcloud`, `curl` and `git` invocations
// as logical lines: backslash continuations are joined, leading shell keywords
// are stripped, and only lines that then start with one of those verbs count
// (so a command inside prose, a comment or a `$(…)` is not read as a command of
// its own).
func probeCommands(section string) []string {
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
		for _, verb := range []string{"gcloud ", "curl ", "git "} {
			if strings.HasPrefix(line, verb) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}
