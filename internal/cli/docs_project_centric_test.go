package cli

import (
	"os"
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
// The body assertions below are deliberately per-line: each is a fact the
// section must state (a FACT it records, a stop it can take, a step of its
// Restore, the project a command names), so deleting that sentence or that
// flag fails the test rather than passing on the section's heading alone.
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
	for _, want := range []string{
		"no agent creates a credential, no agent runs a live step",
		"no step of this check creates a token for the `edgeappinc` workspace or probes any repository that is not a scratch repository created for the probe",
		"you run every command",
		"**Never `belong`**",
		"Never run this against `belong`, never create a `edgeappinc` workspace token",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Check 52 section's body does not say %q", want)
		}
	}
	// Its row in the check list (the summary table), not only the section.
	var row bool
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 52 |") {
			row = true
			break
		}
	}
	if !row {
		t.Error("the check list has no Check 52 row")
	}
	// Check 43's guard block inside the section — the three hard stops and
	// the project pinning, not merely a ${P:?...}.
	for _, guard := range []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ] && [ -n "$L" ] \`,
		`|| { echo "Check 52 stopped: re-run the whole setup block`,
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
		`|| { echo "Check 52 stopped: this is not the fugaro-dev installation" >&2; exit 1; }`,
		`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
		`|| { echo "Check 52 stopped: this checkout's origin is not the sandbox" >&2; exit 1; }`,
	} {
		if !strings.Contains(section, guard) {
			t.Errorf("the Check 52 section is missing the Check 43 guard line %q", guard)
		}
	}
	// The two stops this check adds on top of those three: the scratch
	// Bitbucket workspaces must not be edgeappinc's, and the out-of-scope
	// project must be in the same workspace as the in-scope one. Removing
	// either stop is removing the only thing that keeps U1 off production.
	for _, stop := range []string{
		`[ -n "${PROBE_WS:?}" ] && [ "$PROBE_WS" != edgeappinc ] && [ -n "${OUT_WS:?}" ] && [ "$OUT_WS" != edgeappinc ] && [ -n "${OUT_PROJECT:?}" ] \`,
		`|| { echo "Check 52 stopped: PROBE_WS and OUT_WS must be non-empty scratch workspaces, never edgeappinc" >&2; exit 1; }`,
		`|| { echo "Check 52 stopped: OUT_WS must be the same workspace as PROBE_WS`,
	} {
		if !strings.Contains(section, stop) {
			t.Errorf("the Check 52 section is missing the stop rule %q", stop)
		}
	}
	// One FACT line per step: what the owner pastes back. Each is pinned by
	// its own sentence, so a deleted FACT line fails here.
	for _, fact := range []string{
		"**FACT:** the recorded job env (names, never values)",
		"**FACT:** whether the token reads and pushes to exactly the project's repositories",
		"**FACT:** whether the execution-level override is accepted and whether the referenced value arrives (the length, never the value)",
		"**FACT:** the two exit statuses; whether the condition is honoured",
		"**FACT:** the sandbox project's current service-account count and the quota it is counted against **as the API reports it**",
	} {
		if !strings.Contains(section, fact) {
			t.Errorf("the Check 52 section is missing the FACT line %q", fact)
		}
	}
	// The Restore: every artifact the check created is named with the command
	// that removes it (review I6). A Restore that stops after the first
	// command leaves the conditional grant, the probe account or a probe
	// secret behind.
	for _, restore := range []string{
		`gcloud run jobs describe "${SANDBOX_JOB:?}" --project="${P:?}" --region="${L:?}" --format=json > /tmp/chk52-job-after.json`,
		`diff /tmp/chk52-job-before.json /tmp/chk52-job-after.json`,
		`gcloud run jobs update "${SANDBOX_JOB:?}" --project="${P:?}" --region="${L:?}" --remove-secrets=PROBE_REF`,
		`gcloud projects remove-iam-policy-binding "${P:?}"`,
		`--condition="${PROBE_COND:?}"`,
		`gcloud iam service-accounts remove-iam-policy-binding "${PROBE_SA:?}" --project="${P:?}"`,
		`--role="roles/iam.serviceAccountTokenCreator"`,
		`gcloud secrets delete "${PROBE_IN:?}" --project="${P:?}" --quiet`,
		`gcloud secrets delete "${PROBE_OUT:?}" --project="${P:?}" --quiet`,
		`gcloud iam service-accounts delete "${PROBE_SA:?}" --project="${P:?}" --quiet`,
		`rm -rf "$SCRATCH"`,
		`unset PROBE_TOKEN; rm -f "${ASKPASS:-/nonexistent}"`,
		"the **three scratch repositories**",
		"**project access token revoked**",
	} {
		if !strings.Contains(section, restore) {
			t.Errorf("the Check 52 section's Restore is missing %q", restore)
		}
	}
	// The closing paragraph, which is what the owner reads last: its opening
	// sentence is the instruction that keeps secrets out of the pull request.
	for _, closing := range []string{
		"**Paste back:** every FACT line above, never a token, key or secret value.",
		"never point a probe at edgeserver or edgeweb**",
	} {
		if !strings.Contains(section, closing) {
			t.Errorf("the Check 52 section's closing paragraph is missing %q", closing)
		}
	}
	// Every command that acts on a project names it: `--project="${P:?}"`
	// (or, where gcloud takes the project positionally — `gcloud projects
	// ...` — or as `--consumer`, the same `${P:?}` guard). A command that
	// loses its flag stops naming fugaro-dev and fails here.
	for _, line := range gcloudCommands(section) {
		if strings.Contains(line, `--project="${P:?}"`) {
			continue
		}
		if strings.Contains(line, `"${P:?}"`) || strings.Contains(line, `projects/${P:?}`) {
			continue
		}
		t.Errorf("the Check 52 section has a gcloud command that does not name the project: %q", line)
	}
	// The access token for the two REST calls comes from a command
	// substitution and is never stored (the section is the owner's own
	// terminal, and a token in a file or a variable outlives the step).
	if n := strings.Count(section, `Bearer $(gcloud auth print-access-token)`); n < 3 {
		t.Errorf("the Check 52 section issues %d REST calls with a substituted access token, want at least 3", n)
	}
}

// gcloudCommands returns the section's `gcloud …` invocations as logical
// lines: backslash continuations are joined, and only lines whose first word
// is `gcloud` count (so a `gcloud` inside prose, a comment or a `$(…)` is not
// read as a command of its own).
func gcloudCommands(section string) []string {
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
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gcloud ") {
			out = append(out, line)
		}
	}
	return out
}
