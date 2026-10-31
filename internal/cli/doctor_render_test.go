package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// doctorOwnerCase is the state the owner pasted on 2026-10-30: "fugaro doctor
// is a dog's breakfast ... Who reads all that nonsense?!?" — seven warnings and
// about fifteen informational lines, several of them sentences of six hundred
// characters. The IDs, severities and Problems here are the ones the checks
// themselves emit for that state, so a rendering change is measured against
// what a person actually saw.
//
// The sweep's lines are built by the checks that emit them — selfCheck and
// tokenGrantUnchecked for the bucket, doctorTokenSigners for the signers —
// rather than written out here: a fixture that invents the text decides the
// class too, and a check whose unread or over-granted state is filed as a
// measurement is a finding nobody counts.
func doctorOwnerCase(t *testing.T) []doctorCheck {
	t.Helper()
	signerSummary, signerOwners := doctorOwnerSigners(t)
	deployers := doctorOwnerDeployers(t)
	caveat, pluginNotice := doctorOwnerPluginSettings(t)
	routing, markers := doctorOwnerRouting(t)
	xdg := tempXDGChecks(func(v string) string {
		if v == "XDG_CONFIG_HOME" {
			return "/tmp/fugaro-config"
		}
		return ""
	})[0]
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
			Fix:     "run `fugaro update`: its policy stage rewrites the node in place at this Fugaro's version 4, keeping the members the node carries, so nothing has to be deleted and published again"},
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
		routing,
		markers,
		{ID: "shared-config", Severity: "info",
			Problem: "the project's config is the shared file published to the runs bucket (generation 7, checked 3 days ago)",
			Fix:     "fugaro setup creates your own local config"},
		caveat,
		pluginNotice,
		xdg,
		{ID: "launcher-jobs", Severity: "info",
			Problem: "launcher grants on Cloud Run jobs: 2 grant(s) over 1 job(s), and a launcher can start 1 of them: fugaro-owner-repo-a-web"},
		signerSummary,
		signerOwners,
		tokenGrantUnchecked(doctorOwnerBucket),
		selfCheck(doctorOwnerBucket, true, "someone@example.com", nil, nil),
		{ID: "secret-scope", Severity: "info",
			Problem: "secrets this checkout's jobs mount, by the copy Fugaro resolves for each: app-api-key — repository copy, which wins over the project's; claude-oauth-token — project scope"},
		{ID: "provider-open-to-every-repository", Severity: "info",
			Problem: "provider provider-a is open to every repository",
			Fix:     "to narrow it, list the repositories in that provider's allow_data_to and run fugaro update --repo in each one"},
		{ID: "provider-secret-name", Severity: "info",
			Problem: "provider provider-a: its key is stored as anthropic-key, not provider-a-api-key"},
		deployers,
		{ID: "project-account-stored", OK: true, Severity: "info",
			Problem: "the project's Claude account job(s), with the repositories each one's token may be used by: fugaro-aurora-personal (profile personal, account personal, token claude-oauth-token-personal): repositories owner/repo-a"},
		{ID: "everything-else", OK: true},
	}
}

// doctorOwnerBucket is the rig's runs bucket, the one the bucket checks name.
const doctorOwnerBucket = "fugaro-runs-proj-1234"

// doctorOwnerPluginSettings are the two lines the wiring's own emitter writes
// for a settings file that carries a hook: the per-notice finding and the caveat
// beside it.
func doctorOwnerPluginSettings(t *testing.T) (caveat, notice doctorCheck) {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"make lint"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := pluginSettingsChecks(p)
	if len(checks) != 2 || checks[0].ID != "plugin-settings-1" || checks[0].Problem == "" {
		t.Fatalf("the settings emitter did not report the hook: %+v", checks)
	}
	return checks[1], checks[0]
}

// doctorOwnerRouting are the two routing lines for a machine whose config names
// no repository: the emitter's own text, since "unresolved here" is a sentence
// with a why in it that a fixture must not invent.
func doctorOwnerRouting(t *testing.T) (routing, markers doctorCheck) {
	t.Helper()
	checks := doctorRoutingChecks(&localcfg.Config{}, &projectpolicy.Policy{}, "", doctorRoutingLayer{})
	if len(checks) != 2 {
		t.Fatalf("the routing emitter returned %d checks", len(checks))
	}
	return checks[0], checks[1]
}

// doctorOwnerDeployers is the credentials-service line as its own emitter
// writes it, on a rig whose fakes serve every policy it reads: the fixture may
// not invent a sentence, and an invented one decides the class too.
func doctorOwnerDeployers(t *testing.T) doctorCheck {
	t.Helper()
	r := newSecretReadersRig(t)
	r.withCredentialsService(t)
	r.svcs.AddService(infra.CredentialsServiceName)
	r.iam.AddServiceAccount("proj-1234", credentialsSA, "credentials service")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/run.developer", Members: []string{"user:person-2@example.com"}})
	o, _ := r.doctor(t)
	c, ok := doctorCheckByID(o.Checks, "credentials-service-deployers")
	if !ok {
		t.Fatal("the credentials-service check emitted no line: the fixture would be inventing one")
	}
	if c.unread {
		t.Fatal("the credentials-service line reports an unread policy, and the rig's fakes serve every one")
	}
	return c
}

// doctorOwnerSigners are the owner's two signer lines, read from the check
// that emits them (doctorTokenSigners) on a rig whose project policy names an
// owner and whose IAM fake resolves roles/owner as the real role does. Both
// are the check's own text: the partial-coverage summary doctor always says,
// and the owners line, which is information and offered nothing to run.
func doctorOwnerSigners(t *testing.T) (summary, owners doctorCheck) {
	t.Helper()
	r := newSignerDoctorRig(t)
	r.withClassRoles()
	r.project()
	o, _ := r.doctor(t)
	var ok bool
	if summary, ok = checkByID(o, "token-signers"); !ok {
		t.Fatal("the signer check emitted no summary line")
	}
	if owners, ok = checkByID(o, "token-signers-owners"); !ok {
		t.Fatal("the signer check emitted no owners line: the fixture would be inventing one")
	}
	return summary, owners
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

// doctorSecurityOutput is the same screen through --security: the default view
// plus the sweep it asked for, printed whole.
func doctorSecurityOutput(t *testing.T, checks []doctorCheck) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	_ = emitDoctor(cmd, doctorOutput{Checks: checks}, false, doctorView{security: true})
	return buf.String()
}

// doctorAllOutput is --all: one line per check, the informational and audit
// lines included, with every check's own text whole.
func doctorAllOutput(t *testing.T, checks []doctorCheck) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	_ = emitDoctor(cmd, doctorOutput{Checks: checks}, false, doctorView{all: true})
	return buf.String()
}

// TestDoctorDefaultIsOneScreen: the default view is a verdict and then only
// what the reader has to do — one sentence and one fix per item — because the
// owner pasted a full `fugaro doctor` (seven warnings and about fifteen
// informational lines, several sentences of six hundred characters) and asked
// who is meant to read it. The informational lines and the security audit are
// still there, behind --all and --security; what the default view may not do is
// print them line by line.
//
// The owner's correction of 2026-10-31 adds one thing the screen owes the
// reader: what it left out is counted at the end, so an audit nobody reads
// about is not an audit nobody knows exists. That is one line each — and this
// state's sweep holds two real findings, the caller's own write access on the
// runs bucket and a token grant the bucket policy could not be read for, both
// counted rather than printed. The limits the brief fixes stand unchanged: at
// most 25 lines, and no line longer than 160 characters.
func TestDoctorDefaultIsOneScreen(t *testing.T) {
	got := doctorDefaultOutput(t, doctorOwnerCase(t))
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	if len(lines) > 25 {
		t.Errorf("the default view is %d lines; it must fit one screen (at most 25):\n%s", len(lines), got)
	}
	for i, l := range lines {
		if n := len([]rune(l)); n > 160 {
			t.Errorf("line %d is %d characters; the default view may not print more than 160:\n%s", i+1, n, l)
		}
	}
	if strings.Contains(got, "\u2026") {
		t.Errorf("the default view clips a sentence instead of saying it, and --all prints the whole text:\n%s", got)
	}
	if !regexp.MustCompile(`^fugaro doctor: \d+ things to do, \d+ to decide$`).MatchString(lines[0]) {
		t.Errorf("the first line is not the verdict %q: %q", "fugaro doctor: N things to do, M to decide", lines[0])
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
	// Three findings: the wiring's own notice in the audit half, and the sweep's
	// two — access the caller cannot be placed for, and a read that failed. All
	// three are counted, none is printed. The standing provider-zdr-unverified
	// note is not one of them: it is a measurement (what doctor cannot read there
	// is the provider account's privacy setting, not this installation), so it
	// earns no pointer — a pointer over a screen with nothing wrong in it is
	// what a reader learns to skip.
	if !strings.Contains(got, "1 more finding: `fugaro doctor --all`") {
		t.Errorf("the default view hides its audit findings without saying so:\n%s", got)
	}
	if !strings.Contains(got, "2 security findings: `fugaro doctor --security`") {
		t.Errorf("the default view does not count the sweep's own findings:\n%s", got)
	}
	// The pointers are the last thing on the screen: a reader who reads to the
	// bottom of a short output learns that there is more of it.
	if last := lines[len(lines)-1]; !strings.Contains(last, "`fugaro doctor --all`") {
		t.Errorf("the default view does not end with the audit pointer: %q", last)
	}
	// The choices are the owner's and are counted as choices: a reader is not
	// handed a decision as a task.
	if !strings.Contains(got, "yours to decide:\n") {
		t.Errorf("the default view does not separate the decisions from the work:\n%s", got)
	}
}

// TestDoctorAllFineIsOneLine: a state with nothing to do and no audit finding
// is one line, and the line the docs pin ("everything checked is ok") is the
// one a reader gets. A measurement doctor made and found nothing in does not
// earn a pointer: the audit class is only a finding when it is not ok.
func TestDoctorAllFineIsOneLine(t *testing.T) {
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "project-layer", Severity: "info", OK: true, Problem: "project layer aurora generation 12 applies"},
		{ID: "token-signers", Severity: "info", OK: true, Problem: "who can sign sign-in tokens: 1 service account"},
		{ID: "runs-bucket-iam", OK: true},
		{ID: "everything-else", OK: true},
	})
	if got != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a state with nothing to do printed:\n%s", got)
	}
}

// TestDoctorNoAuditFindingsNoPointer: the pointer lines are for findings. An
// installation whose audit checks all pass — the sweep measured the IAM and it
// is the installation's own accounts, the layer is the published one — gets the
// verdict and nothing else: a pointer to an empty --all is noise, and noise on
// every clean run is how a reader learns to skip the line.
func TestDoctorNoAuditFindingsNoPointer(t *testing.T) {
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "provider-price-catalog", Severity: "info", OK: true, Problem: "the price catalog was checked 3 days ago"},
		{ID: "project-layer", Severity: "info", OK: true, Problem: "project layer aurora generation 12 applies"},
		{ID: "policy-published", Severity: "info", OK: true, Problem: "a policy is published at config/policy/shared"},
		{ID: "launcher-jobs", Severity: "info", OK: true, Problem: "launcher grants on Cloud Run jobs: 2 grants over 1 job"},
		{ID: "token-signers", Severity: "info", OK: true, Problem: "who can sign sign-in tokens: 1 service account"},
		{ID: "plugin-settings-1", Severity: "info", OK: true, Problem: "the wiring blesses every rule in that file"},
	})
	for _, unwanted := range []string{"--security", "--all", "finding"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a clean audit still prints %q:\n%s", unwanted, got)
		}
	}
}

// TestDoctorDefaultEndsWithAuditPointer: an audit finding the default view does
// not print is an audit finding nobody hears about (the owner's correction,
// 2026-10-31). The screen ends with one pointer line per kind — the security
// sweep's and the rest of the audit class — each naming the flag that prints
// them, and each counted, not listed.
//
// The two counted here are findings, not measurements: runs-bucket-iam-1 is one
// instance's grant in the sweep, and plugin-settings-1 is the wiring's own
// notice. The summaries those sweeps also emit are what doctor measured and
// found nothing in — runs-bucket-iam's own OK summary, built by the check — and
// the verdict says so by not counting them. The standing provider-zdr-unverified
// note is in the fixture for the same reason: it is a measurement, so it is
// counted nowhere, not even under --all's own pointer.
func TestDoctorDefaultEndsWithAuditPointer(t *testing.T) {
	measured := findingChecks(nil)[0]
	if measured.ID != "runs-bucket-iam" || !measured.OK {
		t.Fatalf("the bucket sweep's measurement is no longer an OK summary: %+v", measured)
	}
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "installation", OK: true},
		{ID: "provider-zdr-unverified", Severity: "info", Problem: "whether provider-a holds the traffic under zero data retention is unverified"},
		{ID: "runs-bucket-iam-1", Severity: "info", Problem: "user:x@example.com can write anywhere in gs://fugaro-runs-proj-1234 (roles/storage.objectAdmin): expected only for an operator"},
		{ID: "plugin-settings-1", Severity: "info", Problem: "the wiring in .claude/settings.json runs a command Fugaro does not bless"},
		{ID: "routing-resolution", Severity: "info", Problem: "no repository this machine's config names resolves the switch"},
		{ID: "token-signers", Severity: "info", Problem: "who can sign sign-in tokens for the budget database: 1 service account"},
		measured,
		{ID: "project-layer", Severity: "info", OK: true, Problem: "project layer aurora generation 12 applies"},
	})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the screen is %d lines, want the verdict and the two pointers:\n%s", len(lines), got)
	}
	// The verdict may never contradict the pointers: "everything checked is ok"
	// above "2 more findings" is the reading the owner ruled out, so a screen
	// that points at a finding says the weaker thing.
	if lines[0] != "fugaro doctor: nothing to do" {
		t.Errorf("the verdict is not the first line: %q", lines[0])
	}
	if strings.Contains(got, "everything checked is ok") {
		t.Errorf("the verdict says everything is ok above a pointer to a finding:\n%s", got)
	}
	if lines[1] != "1 security finding: `fugaro doctor --security`" {
		t.Errorf("the security pointer is not the second line: %q", lines[1])
	}
	if lines[2] != "1 more finding: `fugaro doctor --all`" {
		t.Errorf("the audit pointer is not the last line: %q", lines[2])
	}

	// --security prints the sweep's own, and its pointer goes: a flag asks for
	// detail, it never changes what the reader is told exists.
	shown, _, _ := execute(t, "doctor", "--security", "--dir", t.TempDir())
	if strings.Contains(shown, "security finding") {
		t.Errorf("--security still points at the sweep it just printed:\n%s", shown)
	}
	// --all prints everything, and so has nothing to point at. (The directory
	// has no installation and doctor exits 1: the view is on the writer either
	// way, and the exit code is not what this test is about.)
	all, _, _ := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if strings.Contains(all, "more findings:") || strings.Contains(all, "security findings:") {
		t.Errorf("--all still points at what it just printed:\n%s", all)
	}
}

// TestDoctorDefaultIsAddressedToTheReader: each line names the check, says in
// one sentence what is wrong, and names the one fix — a command or one action.
func TestDoctorDefaultIsAddressedToTheReader(t *testing.T) {
	got := doctorDefaultOutput(t, doctorOwnerCase(t))
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

// TestDoctorSecurityIsBehindItsFlag: the IAM and security sweep leaves the
// default view — the owner's complaint was that it crowded out the items — and
// the default view says how many it left out, so a short output is never read
// as a clean one. --security prints them.
//
// The sweep's per-instance findings are what the pointer counts; its summary
// lines are measurements (see the second half, and TestDoctorMeasurementIsNotAFinding).
func TestDoctorSecurityIsBehindItsFlag(t *testing.T) {
	g := gcpfake.NewGCS(t)
	r := newDoctorRig(t)
	// A write grant on the runs bucket and no launcher or operator named: the
	// finding is info severity, and it is a finding all the same — somebody
	// this installation cannot place holds a write role (infra.RunsBucketFindings).
	r.withRunsBucket(t, g, "", gcpfake.Binding{Role: "roles/storage.objectAdmin",
		Members: []string{"user:x@example.com"}})

	quiet, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	if strings.Contains(quiet, "runs-bucket-iam") {
		t.Errorf("the default view prints the bucket sweep:\n%s", quiet)
	}
	if !strings.Contains(quiet, "1 security finding: `fugaro doctor --security`") {
		t.Errorf("the default view hides a security finding without saying so:\n%s", quiet)
	}

	shown, _, _ := execute(t, "doctor", "--security", "--dir", t.TempDir())
	if !strings.Contains(shown, "runs-bucket-iam-1") {
		t.Errorf("--security does not print the bucket sweep:\n%s", shown)
	}
	// Whole, not clipped: a reader who asked for the sweep gets the answer,
	// and the sweep's answers are longer than one screen line by nature.
	if !strings.Contains(shown, "expected only for an operator") {
		t.Errorf("--security prints a clipped bucket sweep:\n%s", shown)
	}
	if strings.Contains(shown, "security findings: `fugaro doctor --security`") {
		t.Errorf("--security still says findings are hidden:\n%s", shown)
	}
}

// TestDoctorSecurityVerdictAccountsForWhatItPrints: --security prints the
// sweep under the verdict, so the verdict may not be the ok line — "everything
// checked is ok" directly above a finding of that same sweep is the reading
// the owner ruled out, and the flag is what made it visible. A measurement the
// sweep prints is not a finding: nothing is wrong in it and the reader has
// nothing to do, so the ok line stands beside it.
func TestDoctorSecurityVerdictAccountsForWhatItPrints(t *testing.T) {
	got := doctorSecurityOutput(t, []doctorCheck{selfCheck(doctorOwnerBucket, true, "someone@example.com", nil, nil)})
	if strings.Contains(got, "everything checked is ok") {
		t.Errorf("--security says everything is ok above a finding it printed:\n%s", got)
	}
	if !strings.Contains(got, "fugaro doctor: nothing to do") {
		t.Errorf("--security does not give the weaker verdict:\n%s", got)
	}
	if !strings.Contains(got, "info runs-bucket-iam: ") {
		t.Errorf("--security did not print the finding the verdict is about:\n%s", got)
	}

	// The same sweep, measured and found clean: the ok line is the answer.
	clean := doctorSecurityOutput(t, []doctorCheck{
		{ID: "token-signers", Severity: "info", OK: true, Problem: "who can sign sign-in tokens for the budget database: 1 service account"},
	})
	if !strings.Contains(clean, "fugaro doctor: ok — everything checked is ok") {
		t.Errorf("--security lost the ok line over a measurement it printed:\n%s", clean)
	}
}

// TestDoctorMeasurementIsNotAFinding is the owner's rule of 2026-10-31: an
// installation whose sweep measured its IAM and found nothing to act on gets
// the one ok line, and no pointer at all. The same lines are still in --all
// and in --security: only the default view's verdict leaves them out.
func TestDoctorMeasurementIsNotAFinding(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")
	r.run.SetJobPolicy("fugaro-acme-app-web", gcpfake.Binding{Role: jobRunnerRole("proj-1234"),
		Members: []string{"user:a@example.com"}})

	quiet, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	if strings.Contains(quiet, "launcher-jobs") {
		t.Errorf("the default view prints the launcher sweep:\n%s", quiet)
	}
	if strings.Contains(quiet, "finding") || strings.Contains(quiet, "--security") {
		t.Errorf("a measurement is counted as a finding:\n%s", quiet)
	}

	// A reader who asks for the sweep gets it whole, measurement and all.
	shown, _, _ := execute(t, "doctor", "--security", "--dir", t.TempDir())
	if !strings.Contains(shown, "launcher-jobs") {
		t.Errorf("--security does not print the launcher sweep:\n%s", shown)
	}
	if !strings.Contains(shown, "migration doing what it says") {
		t.Errorf("--security prints a clipped launcher sweep:\n%s", shown)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if !strings.Contains(all, "launcher-jobs") {
		t.Errorf("--all does not print the launcher sweep:\n%s", all)
	}
}

// TestDoctorDefaultShowsAWarningSecurityFinding: a security check that is a
// warning is a finding, not a measurement, and hiding it would hide a live
// grant from the default view — the owner's correction of 2026-10-31 is
// exactly this: what needs a person is shown, and only the measurements move
// behind a flag.
func TestDoctorDefaultShowsAWarningSecurityFinding(t *testing.T) {
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "secret fugaro-owner-repo-a-api-key grants a role that reads every version to user:x@example.com",
			Fix:     "review each member and remove the ones Fugaro did not create"},
		{ID: "runs-bucket-iam-1", Severity: "warning",
			Problem: "bucket fugaro-runs-proj-1234 grants roles/storage.admin to allUsers",
			Fix:     "remove the public grant: gcloud storage buckets remove-iam-policy-binding"},
		{ID: "token-signers-2", Severity: "info", Problem: "the signer list also names a service account"},
	})
	for _, want := range []string{"warning secret-readers-unexpected: ", "warning runs-bucket-iam-1: "} {
		if !strings.Contains(got, want) {
			t.Errorf("a warning security finding is hidden: %q missing from\n%s", want, got)
		}
	}
	if !strings.Contains(got, "1 security finding: ") {
		t.Errorf("the one hidden info finding is not counted:\n%s", got)
	}
}

// TestDoctorSweepWarningsAreOneLine: the security sweep's warnings are printed
// in the default view — a warning-severity finding is never hidden — and their
// own sentence names a member, a role and a container in one breath, which on
// the installations the sweep exists for is more than a screen line. The
// fallback was firstSentence, and for these the first sentence runs to the end
// of a paragraph, so the clip cut the line exactly where the names are (the
// review's finding). Every one of them now has a short form whose sentence fits
// the head budget whole, and the names stay in --all and --json.
func TestDoctorSweepWarningsAreOneLine(t *testing.T) {
	// The sentence alone has to fit: printDoctorItem clips the head when the
	// head and its fix do not fit together, so a sentence that fits the budget
	// is the one thing that can never be cut.
	fits := func(t *testing.T, c doctorCheck) {
		t.Helper()
		head := doctorSeverityWord(c) + " " + c.ID + ": " + doctorShortProblem(c)
		if len([]rune(head)) > doctorLineLimit {
			t.Errorf("%s's sentence does not fit one screen line: %d runes, limit %d: %q",
				c.ID, len([]rune(head)), doctorLineLimit, head)
		}
	}
	// On the check's own worst text: what a short form is for is not clipping
	// whatever the check itself says.
	long := "a problem in the check's own words, long enough that a clipped head would cut it where the names are: " +
		strings.Repeat("member@example.com (roles/example.role) on gs://a-bucket, ", 4)
	for id := range doctorShorts {
		if id == "secret-scope-shadowed" {
			// The one entry that is not a sentence of its own: a decision has to
			// name the container it is about, so doctorShortProblem keeps the
			// check's own first words and adds the clause behind them, and the
			// head is as long as the name (doctorShortProblem's special case).
			continue
		}
		fits(t, doctorCheck{ID: id, Severity: "warning", Problem: long, Fix: long})
	}
	for _, p := range doctorShortsByPrefix {
		fits(t, doctorCheck{ID: p.prefix + "2", Severity: "warning", Problem: long, Fix: long})
	}

	// And on a real emission: a bucket grant the sweep cannot place, read from
	// the rig's own policy. Its line is one sentence now, and the member, the
	// role and the bucket are what --all carries.
	g := gcpfake.NewGCS(t)
	r := newDoctorRig(t)
	odd := gcpfake.Binding{Role: "projects/example/roles/customWriter", Members: []string{"group:platform@example.com"}}
	r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), odd)...)
	got, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	const head = "warning runs-bucket-iam-1: "
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, head) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the default view does not print the sweep's warning finding:\n%s", got)
	}
	if want := head + "a grant on the runs bucket that Fugaro does not write, or one it could not read."; line != want {
		t.Errorf("the sweep's finding is not its short form: %q, want %q", line, want)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if !strings.Contains(all, "group:platform@example.com") {
		t.Errorf("--all does not carry the member the default line leaves out:\n%s", all)
	}
}

// TestDoctorAllPrintsEveryCheck: --all is the long-standing output — one line
// per check, informational and audit lines included — so nothing an operator
// could read before is gone.
func TestDoctorAllPrintsEveryCheck(t *testing.T) {
	fixture := doctorOwnerCase(t)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	_ = emitDoctor(cmd, doctorOutput{Checks: fixture}, false, doctorView{all: true})
	got := buf.String()
	for _, c := range fixture {
		if c.OK && c.Severity == "" {
			continue
		}
		if !strings.Contains(got, c.ID+": ") {
			t.Errorf("--all does not print %s:\n%s", c.ID, got)
		}
	}
	if !strings.Contains(got, "info launcher-jobs: ") {
		t.Errorf("--all does not print the security sweep:\n%s", got)
	}
}

// TestDoctorUpdateItemsAreOneCommand: an item `fugaro update` puts right says
// so and says nothing else, however long the check's own text is (CLAUDE.md's
// hard rule: that one command is the whole upgrade). The check's text is not
// lost: --all and --json still carry it.
func TestDoctorUpdateItemsAreOneCommand(t *testing.T) {
	updateIDs := []string{
		"cli-version", "plugin-pin", "base-image", "image-config-web",
		"policy-rules-skew", "policy-vs-jobs", "policy-older-than-binary",
	}
	for _, id := range updateIDs {
		c := doctorCheck{ID: id, Severity: "warning",
			Problem: "a long problem sentence that says what is wrong here, in the check's own words",
			Fix:     "a long fix that names every step of a manual migration, which the default view must not print"}
		if got := doctorClassOf(c); got != doctorClassUpdate {
			t.Errorf("%s is %q, want %q", id, got, doctorClassUpdate)
		}
		line := doctorItemLine(t, c)
		// The line names `fugaro update` and what makes it do the work: the
		// upgrade before it (cli-version), or the repository the checkout
		// runs in (plugin-pin). A check here that carries no line of its own
		// gets the class's one command, whole.
		if !strings.Contains(line, "`fugaro update") {
			t.Errorf("%s's line does not name the fugaro update command: %q", id, line)
		}
		if strings.Contains(line, "manual migration") {
			t.Errorf("%s's line prints the check's own long fix: %q", id, line)
		}
		// And the item is gone once it passes: nothing prints it, and the
		// verdict is the ok one (UC4's convergence).
		ok := c
		// A check that passes carries no severity at all: that is what the
		// plugins states and the layer checks set when there is nothing to say.
		ok.OK, ok.Severity, ok.Problem, ok.Fix = true, "", "", ""
		if got := doctorDefaultOutput(t, []doctorCheck{ok}); !strings.Contains(got, "fugaro doctor: ok") {
			t.Errorf("%s still prints when it passes:\n%s", id, got)
		}
	}

	// policy-vs-jobs is the one update item whose command may not be narrowed:
	// the check is about jobs still on a runner image that predates this
	// Fugaro, and `fugaro update --repo .` runs the checkout's repository
	// stage and nothing else — it writes a job's environment, and the jobs stay
	// on that image. Plain `fugaro update` is the whole converge, images stage
	// first (init_stages.go), and it is what the line names.
	narrowed := doctorCheck{ID: "policy-vs-jobs", Severity: "warning",
		Problem: "the jobs are deployed from the runner image ghcr.io/example/runner:1, which predates this Fugaro",
		Fix:     "fugaro image refresh moves the jobs to this release's runner image (then fugaro update --repo . in your own terminal)"}
	if line := doctorItemLine(t, narrowed); !strings.HasSuffix(line, "run `fugaro update`") {
		t.Errorf("policy-vs-jobs' line does not name the whole update: %q", line)
	}

	// policy-rules-skew's line, for every state `fugaro update` really repairs,
	// is that same plain command and nothing else: it is one ID with ten
	// states (rulesSkewKind), and the states the update does not put right are
	// the two that carry their own line, below. The check's own long text is
	// what the class's line exists to replace — the state's detail is in the
	// next line's own words.
	skew := doctorCheck{ID: "policy-rules-skew", Severity: "warning",
		Problem: "the deployed security rules are generation 1, which predates the project policy: this Fugaro deploys generation 5",
		Fix:     "re-run 'fugaro update' with fugaro >= 0.8.0 in your own terminal: it deploys the rules a policy is read under and records generation 5 in the marker"}
	if line := doctorItemLine(t, skew); !strings.HasSuffix(line, "run `fugaro update`") {
		t.Errorf("policy-rules-skew's line, for a state the update repairs, is not the plain command: %q", line)
	}

	// And the two states it does not repair print their own fix (ownFix), in
	// full, because it is the command that does the work: a marker no deploy
	// writes is repaired by a deploy from a Fugaro that reads generations, and
	// a marker above this binary's is repaired by the newer binary. The class
	// is the ID's and stays what it was (--json is unchanged): what differs is
	// which line the default view prints.
	for id, want := range map[string]string{
		"the hand-edited marker":    "re-run `fugaro update --firebase-project proj-1234` with fugaro >= 0.8.0 in your own terminal.",
		"the marker above this one": "upgrade Fugaro (`brew upgrade fugaro`), then `fugaro update` with the newer binary.",
	} {
		c := doctorCheck{ID: "policy-rules-skew", Severity: "warning", ownFix: true,
			Problem: "the deployed rules are not this Fugaro's to deploy over",
			Fix:     want + " The rest of the check's own text stays where it is."}
		if got := doctorClassOf(c); got != doctorClassUpdate {
			t.Errorf("%s: the class is %q, want %q: ownFix changes the line, not the class", id, got, doctorClassUpdate)
		}
		if line := doctorItemLine(t, c); !strings.Contains(line, want) || strings.HasSuffix(line, "run `fugaro update`") {
			t.Errorf("%s's line does not print the state's own fix (%q): %q", id, want, line)
		}
	}

	// policy-older-than-binary joined the class with the policy stage: the node
	// is migrated in place by the same run (init_policy_migrate.go), so its line
	// is the plain command like the rest — and the check's own long text, which
	// the class's line exists to replace, is what --all still prints.
	older := doctorCheck{ID: "policy-older-than-binary", Severity: "warning",
		Problem: "config/policy/shared holds a policy of version 3, and this Fugaro writes version 4: no run applies it, and publishing over it is refused",
		Fix:     "run `fugaro update`: its policy stage rewrites the node in place at this Fugaro's version 4, keeping the members the node carries, so nothing has to be deleted and published again"}
	if line := doctorItemLine(t, older); !strings.HasSuffix(line, "run `fugaro update`") {
		t.Errorf("policy-older-than-binary's line, for the state the update migrates, is not the plain command: %q", line)
	}
	if all := doctorAllOutput(t, []doctorCheck{older}); !strings.Contains(all, "rewrites the node in place") {
		t.Errorf("--all does not carry the check's own text:\n%s", all)
	}

	// A check `fugaro update` does not put right is not an update item, and its
	// line names the command that does the work: a line that sends the reader
	// to a command which leaves the warning standing is worse than a longer one
	// that works (the review's finding: what differs for plugin-install is the
	// installed plugin, and the converge's plugin stage writes the pin rather
	// than installing it).
	for id, want := range map[string]string{
		"plugin-install": "`fugaro upgrade --local`",
	} {
		c := doctorCheck{ID: id, Severity: "warning", Problem: "what is wrong here",
			Fix: "a fix long enough that the default view prints the short form instead: " + strings.Repeat("and its own words ", 10)}
		if got := doctorClassOf(c); got == doctorClassUpdate {
			t.Errorf("%s is an update item, and fugaro update does not do this: %q", id, got)
		}
		line := doctorItemLine(t, c)
		if !strings.Contains(line, want) {
			t.Errorf("%s's line does not name %s: %q", id, want, line)
		}
	}
}

// TestDoctorNeverHidesAFinding is the invariant the classification exists
// under: checksFail fails on any check that is not ok and whose severity is
// not "info", so a check that can fail doctor may never be hidden — not by an
// audit ID (provider-price-catalog is an age to read and a stale catalog,
// project-layer-drift is a layer that moved on and a run that applied none),
// not by anything else. A hidden check plus a failing exit code prints
// "everything checked is ok" beside exit 1.
func TestDoctorNeverHidesAFinding(t *testing.T) {
	ids := []string{
		"provider-price-catalog", "provider-zdr-unverified", "routing-resolution", "routing-cache-markers",
		"project-layer", "project-layer-drift", "shared-config", "shared-config-differs",
		"xdg-temp-dir", "policy-published", "plugin-settings-caveat", "plugin-settings-1",
		"token-signers-owners", "runs-bucket-iam-1", "launcher-jobs-public", "job-token-grant",
		"project-account-stored",
	}
	for _, id := range ids {
		for _, sev := range []string{"", "warning"} {
			c := doctorCheck{ID: id, Severity: sev, Problem: "something is wrong", Fix: "run `fugaro update`"}
			if doctorClassOf(c) != doctorClassActionable {
				t.Errorf("%s at severity %q is not an item of the default view: %q", id, sev, doctorClassOf(c))
			}
			if !strings.Contains(doctorDefaultOutput(t, []doctorCheck{c}), id) {
				t.Errorf("%s at severity %q fails doctor and the default view does not print it", id, sev)
			}
		}
	}

	// The audit half: an info line the default view counts rather than prints.
	// A measurement is never counted, so an ID that is not on the allow-list
	// stays a finding here — that is the ruling's default, and it is what keeps
	// a check added later from going unmentioned.
	// plugin-settings-<n> is here for the reason it is not on the allow-list
	// below: a settings file that adds a hook, bypassPermissions, an
	// apiKeyHelper or enableAllProjectMcpServers printed in the default view
	// before doctor had classes, and a notice about what the wiring carries is
	// not a measurement of this checkout's state. The fixed caveat line beside
	// it (plugin-settings-caveat) is one, and stays one.
	for _, id := range []string{
		"runs-bucket-iam-1", "job-token-grant-2", "launcher-jobs-public",
		"project-account-stored", "a-check-added-later", "plugin-settings-1",
	} {
		info := doctorCheck{ID: id, Severity: "info", Problem: "what doctor measured"}
		if got := doctorClassOf(info); got != doctorClassAudit {
			t.Errorf("%s at an info severity is %q, want %q", id, got, doctorClassAudit)
		}
		if !strings.Contains(doctorDefaultOutput(t, []doctorCheck{info}), "1 more finding") &&
			!strings.Contains(doctorDefaultOutput(t, []doctorCheck{info}), "1 security finding") {
			t.Errorf("%s at an info severity is not counted anywhere in the default view:\n%s", id, doctorDefaultOutput(t, []doctorCheck{info}))
		}
	}

	// The measurement allow-list, the other way: an ID on it at an info severity
	// is measured, counted by no view and printed by --all. Nothing on this list
	// may be a check that fails doctor (the loop above holds that end: the same
	// IDs at "" and at "warning" are items), and nothing whose info line is a
	// read that failed belongs on it at all. provider-zdr-unverified is in both
	// loops for that reason and not in spite of it: its warning is the state
	// itself, and its info line is what doctor could not read about the
	// *provider account* — a fact outside this installation, not a read of this
	// installation that did not answer, which is a finding whatever it says
	// (TestDoctorReadFailureIsCounted).
	for _, id := range []string{
		"cli-version", "plugin-install", "cosign", "provider-price-catalog",
		"provider-zdr-unverified",
		"routing-resolution", "routing-cache-markers", "project-layer", "project-layer-drift",
		"shared-config", "shared-config-differs", "xdg-temp-dir", "policy-published",
		"launcher-jobs", "token-signers", "token-signers-owners",
		"secret-readers-unexpected", "credentials-service-deployers",
		"batch-apis", "secret-scope",
		"plugin-settings-caveat",
	} {
		c := doctorCheck{ID: id, Severity: "info", Problem: "what doctor measured"}
		if got := doctorClassOf(c); got != doctorClassMeasure {
			t.Errorf("%s at an info severity is %q, want %q", id, got, doctorClassMeasure)
		}
		if checksFail([]doctorCheck{c}, true) {
			t.Errorf("%s is a measurement and fails doctor: %+v", id, c)
		}
	}

	// The per-instance IDs of those same sweeps are findings: runs-bucket-iam-1
	// is one grant doctor could not place, and its summary line is the
	// measurement. A prefix in the allow-list would swallow it, which is why the
	// list holds whole IDs.
	for _, id := range []string{"runs-bucket-iam-1", "job-token-grant-2", "token-signers-3", "launcher-jobs-public"} {
		c := doctorCheck{ID: id, Severity: "info", Problem: "one instance's own finding"}
		if got := doctorClassOf(c); got != doctorClassAudit {
			t.Errorf("%s is %q, want %q: the per-instance IDs are findings, not measurements", id, got, doctorClassAudit)
		}
	}

	// secret-scope is a listing, not a choice: the check offers one command
	// that lists the same names again, so it belongs to no decision block and
	// no view prints it (the owner's ruling of 2026-10-31, second half).
	scope := doctorCheck{ID: "secret-scope", Severity: "info",
		Problem: "secrets this checkout's jobs mount, by the copy Fugaro resolves for each: api-key — project scope"}
	if got := doctorClassOf(scope); got != doctorClassMeasure {
		t.Errorf("secret-scope is %q, want %q: it is a listing nobody decides on", got, doctorClassMeasure)
	}
	if out := doctorDefaultOutput(t, []doctorCheck{scope}); strings.Contains(out, "secret-scope") || strings.Contains(out, "to decide") || strings.Contains(out, "finding") {
		t.Errorf("a chosen-nothing state still prints the secret listing:\n%s", out)
	}

	// The other direction: whatever a view hides, doctor does not fail on.
	for _, c := range doctorOwnerCase(t) {
		hidden := doctorClassOf(c) == "" || doctorClassOf(c) == doctorClassAudit
		if hidden && checksFail([]doctorCheck{c}, true) {
			t.Errorf("%s is hidden from the default view and fails doctor: %+v", c.ID, c)
		}
	}
}

// TestDoctorReadFailureIsCounted is the other half of the ruling of
// 2026-10-31: a check that says "could not be read, so it is unknown, not no"
// is never a measurement. A read that failed is not a fact doctor found — it is
// the absence of one — and filing it as a measurement is a silent pass: the ok
// line goes up while the thing it is about was never read. Each of these is a
// counted finding, and the pointer is where its reader hears of it; the check
// is not printed in the default view, which is exactly why the count may not
// lose it. One case per ID, each on the check's own emission.
func TestDoctorReadFailureIsCounted(t *testing.T) {
	cascade := func(p *projectpolicy.Policy) {
		p.Routing = &projectpolicy.Routing{Claude: &projectpolicy.ClaudeRoute{Via: projectpolicy.RouteCascade}}
	}
	for _, tc := range []struct {
		id   string
		emit func(*testing.T) doctorCheck
		flag string
	}{
		{"policy-rules-skew", func(t *testing.T) doctorCheck {
			_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
			db.DenyRead(".settings/rules")
			return doctorCheckEmitted(t, "policy-rules-skew")
		}, "`fugaro doctor --all`"},
		{"creds-broken", func(t *testing.T) doctorCheck {
			_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
			db.DenyRead("creds")
			return doctorCheckEmitted(t, "creds-broken")
		}, "`fugaro doctor --all`"},
		{"creds-rules", func(t *testing.T) doctorCheck {
			_, db := doctorPolicyRig(t, doctorPolicy(cascade), "")
			db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
			db.DenyRead(rules.PathCredsRulesMarker())
			return doctorCheckEmitted(t, "creds-rules")
		}, "`fugaro doctor --all`"},
		// The bucket policy a launcher's credentials may not read: the same
		// unread state, emitted by the check itself.
		{"job-token-grant", func(t *testing.T) doctorCheck {
			return tokenGrantUnchecked(doctorOwnerBucket)
		}, "`fugaro doctor --security`"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			c := tc.emit(t)
			if c.OK || c.Severity != "info" {
				t.Fatalf("%s = %+v, want the info note of a read that failed", tc.id, c)
			}
			if got := doctorClassOf(c); got != doctorClassAudit {
				t.Errorf("%s is %q, want %q: a failed read is never a measurement", tc.id, got, doctorClassAudit)
			}
			out := doctorDefaultOutput(t, []doctorCheck{c})
			if strings.Contains(out, tc.id) {
				t.Errorf("the default view prints the unread line instead of counting it:\n%s", out)
			}
			if !strings.Contains(out, "1 more finding") && !strings.Contains(out, "1 security finding") {
				t.Errorf("%s is not counted in the default view:\n%s", tc.id, out)
			}
			if !strings.Contains(out, tc.flag) {
				t.Errorf("the pointer does not name the flag that prints %s:\n%s", tc.id, out)
			}
		})
	}
}

// TestDoctorUnreadCheckThatSaysOKIsCounted is the guard beside the ruling of
// 2026-10-31: the unread marker is what a check that could not read something
// sets, and it is a finding whatever the check's own result field says. No
// emitter writes this shape today (each unread check also carries a severity,
// and its line says the read did not answer), which is why it is a guard: the
// two fields are written by different branches, and a check that set both would
// otherwise be doctor's own bookkeeping — an "ok" check with no severity is
// skipped by every view, so an unknown would be dropped whole, counted by
// nothing and printed nowhere. Both the class and the counting have to hold it.
func TestDoctorUnreadCheckThatSaysOKIsCounted(t *testing.T) {
	for _, tc := range []struct {
		id, severity string
	}{
		// The two shapes an OK check can come in: with a severity (the
		// measurement shape, which --all prints) and without one (doctor's
		// bookkeeping, which nothing prints).
		{"everything-else", ""},
		{"everything-else", "info"},
	} {
		t.Run("severity "+tc.severity, func(t *testing.T) {
			c := doctorCheck{ID: tc.id, OK: true, Severity: tc.severity, unread: true,
				Problem: "whether the thing was read could not be checked, so it is unknown here"}
			if got := doctorClassOf(c); got != doctorClassAudit {
				t.Errorf("an unread check that says ok is %q, want %q", got, doctorClassAudit)
			}
			if !doctorIsFinding(c) {
				t.Fatal("an unread check that says ok is not a finding")
			}
			out := doctorDefaultOutput(t, []doctorCheck{c})
			if strings.Contains(out, "everything checked is ok") {
				t.Errorf("the ok line stands above an unread check:\n%s", out)
			}
			if !strings.Contains(out, "1 more finding: `fugaro doctor --all`") {
				t.Errorf("an unread check is dropped from the default view:\n%s", out)
			}
		})
	}

	// The verdict --security prints above the sweep is held by the same rule: a
	// check the sweep holds is printed below it, so an ok one that carries the
	// marker may not be answered with the ok line the reader would read first.
	sweep := doctorCheck{ID: "model-key-in-agent", OK: true, unread: true, Severity: "info",
		Problem: "whether a model credential reaches the agent could not be checked"}
	shown := doctorSecurityOutput(t, []doctorCheck{sweep})
	if strings.Contains(shown, "everything checked is ok") {
		t.Errorf("--security says everything is ok above its own unread finding:\n%s", shown)
	}
	if !strings.Contains(shown, "fugaro doctor: nothing to do") {
		t.Errorf("--security does not give the weaker verdict:\n%s", shown)
	}
}

// doctorCheckEmitted is one check of a real `fugaro doctor --json` run on the
// rig the test just configured.
func doctorCheckEmitted(t *testing.T, id string) doctorCheck {
	t.Helper()
	c, ok := doctorChecksByID(t)[id]
	if !ok {
		t.Fatalf("doctor emitted no %s check for the state the test set up", id)
	}
	return c
}

// doctorCleanRig is the installation the owner's rule of 2026-10-31 is about:
// every check doctor runs on it passes or is a measurement doctor made. It is
// built from the real checks — the rig's fakes, a recorded base image, the
// verifier a base pin needs, and the plugin wiring an installation writes — so
// "a clean installation prints one line" is a fact about what doctor emits, not
// about a fixture assembled to make the view look right.
//
// configure runs on the rig before the clean state is completed, for a test
// that needs one real finding in it.
func doctorCleanRig(t *testing.T, configure func(*doctorRig)) (*doctorRig, string) {
	t.Helper()
	r := newDoctorRig(t)
	if configure != nil {
		configure(r)
	}
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	return doctorCleanState(t, r, root), root
}

// doctorCleanState is the clean half on a rig and a checkout of the caller's
// choosing: a test whose clean installation has a checkout of its own — one
// whose jobs mount a secret — completes the same state here, so "clean" means
// the same thing in both.
func doctorCleanState(t *testing.T, r *doctorRig, root string) *doctorRig {
	t.Helper()
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfgPath, append(data, []byte("base_images: {base: ghcr.io/example/base:1}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(r.dir, "cosign")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", r.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No installed plugin of this machine's own: what Claude Code installed is
	// read from a directory the test owns.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return r
}

// secretCleanRig is the same clean installation in a checkout whose jobs mount
// a stored secret: the state secret-scope exists for. Its listing is a
// measurement and not a decision — one line, no choice to make, nobody to send
// anywhere — so the clean screen stays the one line the owner asked for.
func secretCleanRig(t *testing.T) (*doctorRig, string) {
	t.Helper()
	r, root := secretScopeCheckout(t)
	// An empty layer bucket of the test's own: the clean state reads no gs://.
	addLocalLines(t, r, "bucket_url: "+layerBucketWith(t, "")+"\n")
	addLocalLines(t, r, projectSecretsConfig)
	// Both copies the listing resolves: the project's, and this repository's
	// own. Values are never printed, and doctor must not print them here.
	putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	slug := mustSlug("github", "acme/app")
	r.sm.Seed(gcp.SecretID(slug, "db-password"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: "db-password"}, []byte("repo-value"))
	// The checkout's own project line: without it the rig has an item (a
	// checkout that cannot say which project it belongs to), and this test is
	// about the secret listing, not that one.
	manifest := filepath.Join(root, "fugaro.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(data), "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(wiredAt("v0.2.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	return doctorCleanState(t, r, root), root
}

// TestDoctorSecretScopeIsNotAChoice: secret-scope is a listing of the secrets
// this checkout's jobs mount and the copy Fugaro resolves for each. It offers
// no choice — its own fix names the command that lists them again — so it is a
// measurement: the clean installation's screen stays one line, with no pointer
// and nothing under "yours to decide". The listing itself is not lost: --all
// carries it, and so does --json.
func TestDoctorSecretScopeIsNotAChoice(t *testing.T) {
	_, root := secretCleanRig(t)
	out, _, err := execute(t, "doctor", "--dir", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a clean installation with a stored secret printed:\n%s", out)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", root)
	if !strings.Contains(all, "secret-scope") || !strings.Contains(all, "db-password") {
		t.Errorf("--all does not carry the secret listing:\n%s", all)
	}
	if strings.Contains(all, "repo-value") || strings.Contains(all, "project-value") {
		t.Errorf("the listing printed a secret's value:\n%s", all)
	}
}

// TestDoctorCleanInstallationIsOneLine is the owner's rule, on the real
// emissions: an installation with nothing wrong prints one line and no pointer,
// whatever the sweep measured on the way. The measurements are not lost — --all
// and --json carry every one of them.
func TestDoctorCleanInstallationIsOneLine(t *testing.T) {
	_, root := doctorCleanRig(t, nil)
	out, _, err := execute(t, "doctor", "--dir", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a clean installation printed:\n%s", out)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", root)
	for _, want := range []string{"launcher-jobs", "policy-published", "provider-price-catalog", "secret-readers-unexpected"} {
		if !strings.Contains(all, want) {
			t.Errorf("--all does not print the measurement %s:\n%s", want, all)
		}
	}
}

// TestDoctorRealFindingPrintsItAndThePointer: the same rig with one real item
// and one real finding the default view holds. The item prints, the finding is
// counted rather than printed, and the pointer is the last line.
func TestDoctorRealFindingPrintsItAndThePointer(t *testing.T) {
	_, root := doctorCleanRig(t, func(r *doctorRig) {
		g := gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, "", gcpfake.Binding{Role: "roles/storage.objectAdmin",
			Members: []string{"user:x@example.com"}})
	})
	// The plugin is pinned behind this binary: a warning the reader fixes.
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(wiredAt("v0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := execute(t, "doctor", "--dir", root)
	if !strings.Contains(out, "warning plugin-pin: ") {
		t.Errorf("the default view does not print the real item:\n%s", out)
	}
	if !strings.Contains(out, "1 security finding: `fugaro doctor --security`") {
		t.Errorf("the default view does not count the real finding:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "`fugaro doctor --security`") {
		t.Errorf("the pointer is not the last line: %q\n%s", last, out)
	}
}

// TestDoctorDoesNotClipAFixCommand: a fix is a command, and a command cut at
// the edge of a terminal is a command somebody pastes and gets wrong. The
// item's own sentence is bounded to the line; the fix goes on its own line, in
// full, however long it is.
func TestDoctorDoesNotClipAFixCommand(t *testing.T) {
	long := "gcloud storage buckets remove-iam-policy-binding gs://fugaro-runs-proj-1234 --member=user:a-very-long-member-name@example.com --role=roles/storage.objectAdmin"
	got := doctorDefaultOutput(t, []doctorCheck{
		{ID: "runs-bucket-iam-1", Severity: "warning", Problem: "a member holds a write role on the bucket", Fix: long},
	})
	if !strings.Contains(got, long) {
		t.Errorf("the fix command is not printed in full:\n%s", got)
	}
	if strings.Contains(got, "\u2026") {
		t.Errorf("the default view clipped the item:\n%s", got)
	}
	// A decision's choices are commands too: each one is printed whole.
	decision := "store the value you mean with `fugaro secrets set app-api-key --repo owner/repo-a`, or disable the repository copy's versions with gcloud secrets versions disable app-api-key --project proj-1234"
	got = doctorDefaultOutput(t, []doctorCheck{
		{ID: "secret-scope-shadowed", Severity: "warning", Problem: "app-api-key: both copies exist", Fix: decision},
	})
	for _, want := range []string{"`fugaro secrets set app-api-key --repo owner/repo-a`", "gcloud secrets versions disable app-api-key --project proj-1234"} {
		if !strings.Contains(got, want) {
			t.Errorf("a decision choice is clipped (%q missing):\n%s", want, got)
		}
	}
}

// doctorItemLine is one check's default-view line, joined when the fix went to
// its own line.
func doctorItemLine(t *testing.T, c doctorCheck) string {
	t.Helper()
	var buf bytes.Buffer
	printDoctorItem(&buf, c)
	return strings.Join(strings.Fields(buf.String()), " ")
}

// doctorHumanItem is one item of a rendered default view, looked up by its
// head: the head line, and — for an item whose fix did not fit beside it — the
// line the fix went to (printDoctorItem indents it by two spaces). Reading the
// human view this way is how a test sees the line a reader gets rather than the
// check's fields, which is the difference ownFix makes.
func doctorHumanItem(t *testing.T, out, head string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, head) {
			continue
		}
		item := l
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
			item += " " + strings.TrimSpace(lines[i+1])
		}
		return item
	}
	t.Fatalf("the default view prints no item whose head is %q:\n%s", head, out)
	return ""
}

// TestDoctorJSONKeepsEveryCheckAndClassifiesIt: --json is the complete and
// stable surface the console and the mobile app read: every check is there with
// the same fields whatever the view flags say, and the class is the one new
// field — added, never a field whose meaning changed.
func TestDoctorJSONKeepsEveryCheckAndClassifiesIt(t *testing.T) {
	r := newDoctorRig(t)
	r.withLaunchers(t)
	r.run.AddJob("fugaro-acme-app-web", "2", "2Gi")

	plain := r.runDoctorJSON(t)
	all, _, err := execute(t, "doctor", "--json", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("doctor --json --all: %v", err)
	}
	var full doctorOutput
	if err := json.Unmarshal([]byte(all), &full); err != nil {
		t.Fatalf("json: %v\n%s", err, all)
	}
	if len(plain.Checks) != len(full.Checks) {
		t.Fatalf("--all changed what --json carries: %d checks against %d", len(full.Checks), len(plain.Checks))
	}
	classes := map[string]bool{
		doctorClassActionable: true, doctorClassUpdate: true, doctorClassAudit: true,
		doctorClassDecision: true, doctorClassMeasure: true,
	}
	for _, c := range full.Checks {
		if c.ID == "" {
			t.Fatalf("a check has no id: %+v", c)
		}
		if c.Class != "" && !classes[c.Class] {
			t.Errorf("%s carries the class %q, which is not one of the classes the views use: %+v", c.ID, c.Class, c)
		}
		if !c.OK && c.Problem == "" {
			t.Errorf("%s is not ok and carries no problem in --json: %+v", c.ID, c)
		}
	}
	// The full text the default view shortens is still in --json.
	c, ok := doctorCheckByID(full.Checks, "launcher-jobs")
	if !ok || !strings.Contains(c.Problem, "launcher grants on Cloud Run jobs") {
		t.Errorf("launcher-jobs lost its text in --json: %+v", c)
	}
}

// TestDoctorDefaultDoesNotOpenWithTheTagLine: the release line is "this fugaro
// is vX, built from commit Y; a tag can be moved, so to check it still names
// that commit run: ..." — a sentence of three hundred characters, and the
// first thing a person saw. It is an audit line: --all prints it.
func TestDoctorDefaultDoesNotOpenWithTheTagLine(t *testing.T) {
	releaseBuild(t, "0.2.0")
	if releaseLine() == "" {
		t.Skip("no release embedded in this build")
	}
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	_ = emitDoctor(cmd, doctorOutput{Checks: []doctorCheck{{ID: "runs-bucket-iam", OK: true}}}, false, doctorView{})
	if got := buf.String(); got != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("the default view opens with something other than the verdict:\n%s", got)
	}
	var all bytes.Buffer
	cmd = &cobra.Command{}
	cmd.SetOut(&all)
	_ = emitDoctor(cmd, doctorOutput{Checks: []doctorCheck{{ID: "runs-bucket-iam", OK: true}}}, false, doctorView{all: true})
	if !strings.Contains(all.String(), "a tag can be moved") {
		t.Errorf("--all does not print the release line:\n%s", all.String())
	}
}

// TestDoctorUnreadPolicyStatesAreCounted: the states a real installation
// reaches when the policy node cannot be read — a read the database refused,
// and a budget.rtdb_url pointing at another project's database. Before the
// unread marker each of them printed "ok — everything checked is ok", exit 0,
// with no pointer, because policy-published and the two routing checks share
// one ID between the measurement and the unread state. They are counted
// findings now: the default view names the flag that prints them, and none of
// their IDs is printed as informational.
func TestDoctorUnreadPolicyStatesAreCounted(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, db *gcpfake.RTDB)
	}{
		{"the policy node could not be read", func(t *testing.T, db *gcpfake.RTDB) {
			db.DenyRead(projectpolicy.PathShared())
		}},
		{"the database belongs to another project", func(t *testing.T, db *gcpfake.RTDB) {
			// The check every reader makes: doctor reads everything as this
			// project's, and this database is not it.
			db.Set("fugaro/project", "borealis")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
			tc.set(t, db)

			// Every state the unread node reaches: the node itself, and the
			// two routing lines that resolve a switch out of it.
			for _, id := range []string{"policy-published", "routing-resolution", "routing-cache-markers"} {
				c := doctorCheckEmitted(t, id)
				if c.OK || c.Severity != "info" {
					t.Fatalf("%s = %+v, want the info line of an unread state", id, c)
				}
				if c.Class != doctorClassAudit {
					t.Errorf("%s is classed %q, want %q: an unread state is never a measurement", id, c.Class, doctorClassAudit)
				}
			}

			// The human view: not the ok line, and not the unread lines
			// printed as if they had been measured.
			out, _, err := execute(t, "doctor")
			if err != nil {
				t.Fatalf("doctor: %v\n%s", err, out)
			}
			if strings.Contains(out, "everything checked is ok") {
				t.Errorf("an installation whose policy could not be read printed the ok line:\n%s", out)
			}
			if !strings.Contains(out, "`fugaro doctor --all`") {
				t.Errorf("the default view hides the unread state without saying so:\n%s", out)
			}
			for _, id := range []string{"policy-published", "routing-resolution"} {
				if strings.Contains(out, "info "+id) {
					t.Errorf("the default view prints the unread %s:\n%s", id, out)
				}
			}
			// --all still carries every one of them, whole.
			all, _, _ := execute(t, "doctor", "--all")
			for _, id := range []string{"policy-published", "routing-resolution", "routing-cache-markers"} {
				if !strings.Contains(all, id) {
					t.Errorf("--all does not print %s:\n%s", id, all)
				}
			}
		})
	}
}

// TestDoctorPluginSettingsNoticeIsCounted: a settings file that adds a hook,
// bypassPermissions, an apiKeyHelper or enableAllProjectMcpServers is what the
// wiring's notice lines are about, and they printed in the default view before
// doctor had classes. A notice is a counted audit finding — the pointer names
// `--all` — while the fixed caveat beside it stays a measurement.
func TestDoctorPluginSettingsNoticeIsCounted(t *testing.T) {
	_, root := doctorCleanRig(t, nil)
	// The wiring an installation has, plus a hook: the notice the per-notice
	// line is for, written through the check's own emitter below.
	settings := filepath.Join(root, ".claude", "settings.json")
	withHook := strings.TrimSuffix(wiredAt("v0.2.0"), "}") + `,"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"make lint"}]}]}}`
	if err := os.WriteFile(settings, []byte(withHook), 0o644); err != nil {
		t.Fatal(err)
	}
	if checks := pluginSettingsChecks(settings); len(checks) != 2 || checks[0].ID != "plugin-settings-1" {
		t.Fatalf("the settings emitter did not report the hook: %+v", checks)
	}

	out, _, err := execute(t, "doctor", "--dir", root)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if strings.Contains(out, "everything checked is ok") {
		t.Errorf("a clean installation whose settings carry a hook printed the ok line:\n%s", out)
	}
	if !strings.Contains(out, "1 more finding: `fugaro doctor --all`") {
		t.Errorf("the default view does not count the settings notice:\n%s", out)
	}
	if strings.Contains(out, "plugin-settings-1") {
		t.Errorf("the default view prints the settings notice:\n%s", out)
	}
	all, _, _ := execute(t, "doctor", "--all", "--dir", root)
	if !strings.Contains(all, "plugin-settings-1") || !strings.Contains(all, "the file defines hooks") {
		t.Errorf("--all does not print the settings notice:\n%s", all)
	}
}
