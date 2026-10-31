package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// doctorOwnerCase is the state the owner pasted on 2026-10-30: "fugaro doctor
// is a dog's breakfast ... Who reads all that nonsense?!?" — seven warnings and
// about fifteen informational lines, several of them sentences of six hundred
// characters. The IDs, severities and Problems here are the ones the checks
// themselves emit for that state, so a rendering change is measured against
// what a person actually saw.
func doctorOwnerCase() []doctorCheck {
	return []doctorCheck{
		{ID: "cli-version", Severity: "warning",
			Problem: "fugaro 0.14.1 is available; this is 0.14.0",
			Fix:     "brew upgrade fugaro"},
		{ID: "base-image", Severity: "warning",
			Problem: "the installation has no Fugaro base image recorded, so every derived image build starts from whatever the repository's Dockerfile names: `fugaro image refresh` records the release's base and rebuilds the job images from it, and until it runs the base the jobs were built on is whatever the last build happened to resolve",
			Fix:     "fugaro image refresh (in your own terminal: it rebuilds the job images, which takes cloud calls)"},
		{ID: "plugin-pin", Severity: "warning",
			Problem: "the plugin is pinned at fugaro@0.13.0, and this Fugaro is 0.14.0",
			Fix:     "run fugaro update to repin the plugin"},
		{ID: "policy-older-than-binary", Severity: "warning",
			Problem: "config/policy/shared holds a policy of version 2, and this Fugaro writes version 4: no run applies it, and publishing over it is refused",
			Fix:     "fugaro project policy publish --delete removes it, then 'fugaro project policy publish' writes this Fugaro's version 4"},
		{ID: "project-layer-copy", Severity: "warning",
			Problem: "the repository's copy of the project layer (sha256 1a2b3c4d) is not the published one (5e6f7a8b): its daily image check resolves against the old one, so a workflow whose image settings the layer changed is checked against settings it no longer carries and the drift stays invisible until a run fails on an image the layer no longer describes",
			Fix:     "run fugaro config publish again, or fugaro image build in this checkout"},
		{ID: "secret-scope-shadowed", Severity: "warning",
			Problem: "app-api-key: stored in owner/repo-a's own container and project-wide at once, and a repository copy with an enabled version wins over the project copy, so the project's value never reaches this repository",
			Fix: "store the value you mean with fugaro secrets set app-api-key --repo owner/repo-a to keep this repository's own copy up to date, " +
				"or disable the repository copy's versions (gcloud secrets versions disable) if the project's value is the one this repository should mount"},
		{ID: "project-secret-extra-accessors", Severity: "warning",
			Problem: "project-wide secret claude-oauth-token grants a reader to a job of a repository the account no longer lists: a repository removed from an account's allow_data_to keeps its live grant until its own apply runs again, and every reader of a project-wide container can read every version of it",
			Fix:     "run fugaro update --repo owner/repo-b in that repository's checkout to reapply it"},
		{ID: "project-layer", Severity: "info",
			Problem: "project layer aurora generation 12 (sha256 9f8e7d6c) applies; fugaro config show prints where each value comes from"},
		{ID: "policy-published", Severity: "info",
			Problem: "a policy is published at config/policy/shared (version 4, 3 allow-list entries)",
			Fix:     "fugaro project policy show prints it"},
		{ID: "provider-price-catalog", Severity: "info",
			Problem: "the shipped price catalog is 24 days old; estimates use it until a release carries a newer one"},
		{ID: "provider-zdr-unverified", Severity: "info",
			Problem: "whether provider-a holds the traffic of a routed call under zero data retention is unverified: the provider's own console is the only place that answers it"},
		{ID: "routing-resolution", Severity: "info",
			Problem: "no repository this machine's config names resolves the switch, so which Claude ID is routed through a provider is unknown here"},
		{ID: "routing-cache-markers", Severity: "info", Problem: routingCacheMarkersNothingRouted},
		{ID: "shared-config", Severity: "info",
			Problem: "the project's config is the shared file published to the runs bucket (generation 7, checked 3 days ago)",
			Fix:     "fugaro setup creates your own local config"},
		{ID: "plugin-settings-caveat", Severity: "info", Problem: "the wiring in .claude/settings.json blesses every rule in that file"},
		{ID: "plugin-settings-1", Severity: "info",
			Problem: ".claude/settings.json carries a hook that runs a command from the repository"},
		{ID: "xdg-temp-dir", Severity: "info",
			Problem: "XDG_CONFIG_HOME is under /tmp, which a reboot empties: the project config lives there"},
		{ID: "launcher-jobs", Severity: "info",
			Problem: "launcher grants on Cloud Run jobs: 2 grant(s) over 1 job(s), and a launcher can start 1 of them: fugaro-owner-repo-a-web"},
		{ID: "token-signers", Severity: "info",
			Problem: "who can sign sign-in tokens for the budget database: 1 service account"},
		{ID: "token-signers-owners", Severity: "info",
			Problem: "Project Owners can sign sign-in tokens for the budget database whatever the signer list says"},
		{ID: "job-token-grant", Severity: "info",
			Problem: "the jobs' own token grants over the budget database: 1 job account"},
		{ID: "runs-bucket-iam", Severity: "info",
			Problem: "the runs bucket's IAM: the installation's own accounts only"},
		{ID: "secret-scope", Severity: "info",
			Problem: "secrets this checkout's jobs mount, by the copy Fugaro resolves for each: app-api-key — repository copy, which wins over the project's; claude-oauth-token — project scope"},
		{ID: "provider-open-to-every-repository", Severity: "info",
			Problem: "provider provider-a is open to every repository",
			Fix:     "to narrow it, list the repositories in that provider's allow_data_to and run fugaro update --repo in each one"},
		{ID: "provider-secret-name", Severity: "info",
			Problem: "provider provider-a: its key is stored as anthropic-key, not provider-a-api-key"},
		{ID: "credentials-service-deployers", Severity: "info",
			Problem: "the credentials service is deployed and its deployers are: 1 service account"},
		{ID: "project-account-stored", OK: true, Severity: "info",
			Problem: "the project's Claude account job(s), with the repositories each one's token may be used by: fugaro-aurora-personal (profile personal, account personal, token claude-oauth-token-personal): repositories owner/repo-a"},
		{ID: "everything-else", OK: true},
	}
}

// doctorDefaultOutput is the default (no flag) human view of one check list,
// through the command's own emitter: what `fugaro doctor` prints on a terminal.
func doctorDefaultOutput(t *testing.T, checks []doctorCheck) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	// The ExitError is the exit code, not a rendering failure: the view is on
	// the writer either way.
	_ = emitDoctor(cmd, doctorOutput{Checks: checks}, false, doctorView{})
	return buf.String()
}

// TestDoctorDefaultIsOneScreen: the default view is a verdict and then only
// what the reader has to do — one sentence and one fix per item — because the
// owner pasted a full `fugaro doctor` (seven warnings and about fifteen
// informational lines, several sentences of six hundred characters) and asked
// who is meant to read it. The informational lines and the security audit are
// still there, behind --all and --security; what the default view may not do is
// print them.
//
// The limits are the ones the brief fixes: at most 25 lines on this state, and
// no line longer than 160 characters.
func TestDoctorDefaultIsOneScreen(t *testing.T) {
	got := doctorDefaultOutput(t, doctorOwnerCase())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	if len(lines) > 25 {
		t.Errorf("the default view is %d lines; it must fit one screen (at most 25):\n%s", len(lines), got)
	}
	for i, l := range lines {
		if n := len([]rune(l)); n > 160 {
			t.Errorf("line %d is %d characters; the default view may not print more than 160:\n%s", i+1, n, l)
		}
	}
	if !regexp.MustCompile(`^fugaro doctor: \d+ things to do$`).MatchString(lines[0]) {
		t.Errorf("the first line is not the verdict %q: %q", "fugaro doctor: N things to do", lines[0])
	}
	if len(lines) < 2 || !strings.Contains(lines[1], "warning") || !strings.Contains(lines[1], "error") {
		t.Errorf("the severity is not explained once, on the second line: %q", strings.Join(lines[min(2, len(lines)):], "\n"))
	}
	// The audit and the security findings are what the owner asked to be rid
	// of: none of them may be in the default view.
	for _, hidden := range []string{
		"project-layer:", "policy-published", "provider-price-catalog", "provider-zdr-unverified",
		"routing-resolution", "routing-cache-markers", "shared-config", "plugin-settings-caveat",
		"xdg-temp-dir", "launcher-jobs", "token-signers", "job-token-grant", "runs-bucket-iam",
		"project-account-stored", "credentials-service-deployers",
	} {
		if strings.Contains(got, hidden) {
			t.Errorf("the default view prints the audit finding %q:\n%s", hidden, got)
		}
	}
	// And it says where they went, so nobody reads a short output as a clean one.
	if !strings.Contains(got, "security findings: `fugaro doctor --security`") {
		t.Errorf("the default view hides security findings without saying so:\n%s", got)
	}
}

// TestDoctorDefaultVerdictIsOKWhenThereIsNothingToDo: a clean state is one
// line, and the line the docs pin ("everything checked is ok") is the one a
// reader gets.
func TestDoctorDefaultVerdictIsOKWhenThereIsNothingToDo(t *testing.T) {
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "project-layer", Severity: "info", Problem: "project layer aurora generation 12 applies"},
		{ID: "routing-resolution", Severity: "info", Problem: "no switch resolves here"},
		{ID: "runs-bucket-iam", OK: true},
	})
	if got != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a state with nothing to do printed:\n%s", got)
	}
}

// TestDoctorDefaultIsAddressedToTheReader: each line names the check, says in
// one sentence what is wrong, and names the one fix — a command or one action.
func TestDoctorDefaultIsAddressedToTheReader(t *testing.T) {
	got := doctorDefaultOutput(t, doctorOwnerCase())
	for _, want := range []string{
		"run `fugaro update`",
		"secret-scope-shadowed: ",
		// A decision prints the command for each choice, never one of them.
		"fugaro secrets set app-api-key --repo owner/repo-a",
		"gcloud secrets versions disable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the default view does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Fix:") || strings.Contains(got, "(run fugaro config publish") {
		t.Errorf("the default view still prints the raw fix parenthesis:\n%s", got)
	}
}
