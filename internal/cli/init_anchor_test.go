package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The init rig's project: aurora in GCP project proj-1234, the default runs
// bucket. Its line in fugaro.yaml:
const anchorRigLine = "gcp_project: proj-1234"

// anchorModeRig is fugaro init --anchor's rig: the init rig's local config
// (a fake terraform on PATH, whose calls are logged) with acme/app onboarded,
// a checkout of acme/app as the working directory, a fake runs bucket for the
// build records and a file:// bucket the shared config would be published to.
type anchorModeRig struct {
	*initRig
	dir, root, path, yaml, published string
}

func newAnchorModeRig(t *testing.T, yaml string, onboarded bool) *anchorModeRig {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	r := &anchorModeRig{initRig: newInitRig(t), yaml: yaml}
	if onboarded {
		r.appendConfig(t, "  acme/app: { provider: github, workflows: [app] }\n")
	}
	r.published = sharedRuns(t)
	r.dir = repoCheckout(t, githubOrigin, yaml)
	t.Chdir(r.dir)
	out, err := exec.Command("git", "-C", r.dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	r.root = strings.TrimSpace(string(out))
	r.path = filepath.Join(r.root, "fugaro.yaml")
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	return r
}

// anchorExec runs fugaro init with stdin, without the rig's own warning that
// its XDG directories are temporary.
func anchorExec(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	out, errOut, err := executeStdin(t, stdin, args...)
	var kept []string
	for _, l := range strings.SplitAfter(out, "\n") {
		if !strings.HasPrefix(l, "warning: XDG_") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, ""), errOut, err
}

func anchorModeYAML() string { return checkoutYAML("github", "oauth", "aurora", "") }

func withRigAnchor(yaml string) string {
	return strings.Replace(yaml, "project: aurora\n", "project: aurora\n"+anchorRigLine+"\n", 1)
}

// releaseBase is a release base image a current build ran FROM.
const releaseBase = "ghcr.io/dimipaun/fugaro-web-node:0.4.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// putWorkflowRecord is a build record of acme/app's workflow wf submitted by
// a fugaro of version, built FROM a 0.4.0 release base image.
func putWorkflowRecord(wf, version string) { putWorkflowRecordFrom(wf, version, releaseBase) }

// putWorkflowRecordFrom is putWorkflowRecord built FROM base ("" for a
// record that does not say).
func putWorkflowRecordFrom(wf, version, base string) {
	slug, _ := task.Slug("github", "acme/app")
	records[imagecheck.RecordKey(slug, wf)] = `{"version":1,"fugaro_version":"` + version + `","base_ref":"` + base + `"}`
}

// wantFail is the one FAIL line of acme/app's workflow wf (base kind kind),
// with its reasons and its ordered fix.
func wantFail(wf, kind, lcPath string, customBase bool, reasons ...string) string {
	return failText(anchorProblemText("acme/app", wf, kind, lcPath, reasons, customBase))
}

// untouched asserts the checkout's fugaro.yaml, the terraform log, the
// registry and the shared-config bucket: --anchor touches none of them
// unless it writes the line, which want says.
func (r *anchorModeRig) check(t *testing.T, want string) {
	t.Helper()
	if got := readYAML(t, r.dir); got != want {
		t.Errorf("fugaro.yaml:\n%s\nwant:\n%s", got, want)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("--anchor ran terraform: %q", calls)
	}
	if reqs := r.ar.Requests(); len(reqs) != 0 {
		t.Errorf("--anchor called Artifact Registry: %+v", reqs)
	}
	if ents, err := os.ReadDir(r.published); err != nil || len(ents) != 0 {
		t.Errorf("--anchor published to the runs bucket: %v %v", ents, err)
	}
}

func rigDiff(path string) string { return path + "\n  + " + anchorRigLine + "\n" }
func rigAddLine(path string) string {
	return "to let teammates use this installation without setup, add this line to " + path + ": " + anchorRigLine + " (every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before the line is merged: older versions refuse the key as unknown)\n"
}
func rigUpdated(path string) string {
	return "Updated " + path + " with gcp_project. Review it with git diff and commit it like any change. " + cliCaution + "\n"
}

// (a) Everything is ready: the diff, the question, the write; no Terraform,
// no publish.
func TestInitAnchorWritesAfterTheChecks(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.4.0")
	fakeTerminal(t)
	out, _, err := anchorExec(t, "y\n", "init", "--anchor")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := rigDiff(r.path) + "Write this to " + r.path + "? [y/N]: " + rigUpdated(r.path); out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	r.check(t, withRigAnchor(r.yaml))
}

func failText(msg string) string { return "FAIL: " + msg + "\n" }

// (b) An image submitted by an older release: the reason and the ordered
// fix, nothing written.
func TestInitAnchorOldRecordFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.3.1")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	want := wantFail("app", "web-node", r.cfg, false, reasonRecordVersionOld("0.3.1"))
	for _, s := range []string{"fugaro 0.3.1", "In order: (1) run fugaro init --base web-node from outside the checkout (it copies this release's base image), (2) fugaro image build --repo acme/app --workflow app, (3) fugaro init --anchor"} {
		if !strings.Contains(want, s) {
			t.Errorf("message lacks %q: %s", s, want)
		}
	}
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	r.check(t, r.yaml)
}

// (c) No build record: the same fix.
func TestInitAnchorMissingRecordFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonNoRecord()) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "has no build record") {
		t.Errorf("output:\n%s", out)
	}
	r.check(t, r.yaml)
}

// (d) A development CLI's image: said as such.
func TestInitAnchorDevRecordFails(t *testing.T) {
	for _, v := range []string{"dev", "0.4.0-3-gabcdef"} {
		r := newAnchorModeRig(t, anchorModeYAML(), true)
		putWorkflowRecord("app", v)
		out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
		if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonDevCLI(v)) || !strings.Contains(out, "built by a development CLI") {
			t.Fatalf("%s: exit %d, err %v\n%s", v, ExitCode(err), err, out)
		}
		r.check(t, r.yaml)
	}
}

// The record's fugaro_version is the submitting CLI's: what the image holds
// is the base it was built FROM. A 0.4.0 CLI that built FROM a 0.3.1 base,
// with the local config since moved on to 0.4.0, is refused.
func TestInitAnchorRecordBuiltFromAnOldBaseFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	r.appendConfig(t, "base_images:\n  web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.4.0\n")
	const from = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.3.1"
	putWorkflowRecordFrom("app", "0.4.0", from+"@sha256:"+strings.Repeat("a", 64))
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonBaseRefOld(from+"@sha256:"+strings.Repeat("a", 64), "0.3.1")) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "built from the base image "+from) {
		t.Errorf("output:\n%s", out)
	}
	r.check(t, r.yaml)
}

// A build FROM a dev base, whose base_images entry was removed afterwards,
// is refused: the image still holds the dev binary.
func TestInitAnchorRecordBuiltFromADevBaseFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	const from = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-8fe2647"
	putWorkflowRecordFrom("app", "0.4.0", from)
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonBaseRefDev(from)) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, r.yaml)
}

// A record that does not say which base it was built FROM (an older one)
// is refused, saying so and to rebuild.
func TestInitAnchorRecordWithoutBaseRefFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecordFrom("app", "0.4.0", "")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonNoBaseRef()) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "does not say which base image it was built from") || !strings.Contains(out, "fugaro image build --repo acme/app --workflow app") {
		t.Errorf("output:\n%s", out)
	}
	r.check(t, r.yaml)
}

// A release base at or after the field, on any host (an unset base is the
// release's own in ghcr.io; --image-source names another), passes.
func TestInitAnchorRecordBuiltFromAReleaseBasePasses(t *testing.T) {
	for _, from := range []string{"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.4.0", "ghcr.io/acme/fugaro-web-node:v0.5.1", releaseBase} {
		r := newAnchorModeRig(t, anchorModeYAML(), true)
		putWorkflowRecordFrom("app", "0.4.0", from)
		if out, _, err := anchorExec(t, "", "init", "--anchor", "--yes"); err != nil {
			t.Fatalf("%s: %v\n%s", from, err, out)
		}
		r.check(t, withRigAnchor(r.yaml))
	}
}

// (e) A dev base image the local config pins fails, even with a current
// record: the next build would bake its binary in again. Its fix comes
// first in the one ordered list.
func TestInitAnchorCustomBaseFails(t *testing.T) {
	yaml := strings.Replace(anchorModeYAML(), "base: web-node", "base: go", 1)
	r := newAnchorModeRig(t, yaml, true)
	const ref = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:dev-8fe2647"
	r.appendConfig(t, "base_images:\n  go: "+ref+"\n")
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	want := wantFail("app", "go", r.cfg, true, reasonConfigBaseCustom(ref, "go"))
	for _, s := range []string{"the local config's base image " + ref + " for kind go is not a release >= 0.4.0", "is never replaced by fugaro init --base",
		"(1) remove base_images.go from " + quoteWord(r.cfg) + " (keep a backup), (2) run fugaro init --base go from outside the checkout", "(3) fugaro image build --repo acme/app --workflow app, (4) fugaro init --anchor"} {
		if !strings.Contains(want, s) {
			t.Errorf("message lacks %q: %s", s, want)
		}
	}
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	r.check(t, yaml)
}

// A local-config path with a space is quoted in the fix.
func TestAnchorProblemTextQuotesThePath(t *testing.T) {
	got := anchorProblemText("acme/app", "app", "go", "/home/me/my configs/aurora.yaml", []string{"x"}, true)
	if !strings.Contains(got, "remove base_images.go from '/home/me/my configs/aurora.yaml' (keep a backup)") {
		t.Errorf("%s", got)
	}
}

// (f) A managed release base image older than the field fails too.
func TestInitAnchorOldManagedBaseFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	const ref = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.3.1"
	r.appendConfig(t, "base_images:\n  web-node: "+ref+"\n")
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("app", "web-node", r.cfg, false, reasonConfigBaseOld(ref, "web-node", "0.3.1")) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, r.yaml)
	// A managed 0.4.0 base passes.
	r = newAnchorModeRig(t, anchorModeYAML(), true)
	r.appendConfig(t, "base_images:\n  web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.4.0\n")
	putWorkflowRecord("app", "0.4.0")
	if out, _, err := anchorExec(t, "", "init", "--anchor", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r.check(t, withRigAnchor(r.yaml))
}

// A registry host that cannot be worked out is said as such, not taken for
// a custom base image.
func TestGCPProjectProblemsRegistryHostError(t *testing.T) {
	lc := anchorLC()
	lc.RegistryHost = "us-east5-docker.pkg.dev/another-project"
	lc.BaseImages = map[string]string{"web-node": "us-east5-docker.pkg.dev/fugaro-aurora/fugaro-base/fugaro-web-node:0.4.0"}
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	putRecordData(t, `{"version":1,"fugaro_version":"0.4.0","base_ref":"`+releaseBase+`"}`)
	ps := gcpProjectProblems(t.Context(), lc, "", "github", "acme/app", map[string]string{"app": "web-node"})
	if len(ps) != 1 || !strings.Contains(ps[0].text, "the registry host is unknown") || strings.Contains(ps[0].text, "remove base_images") {
		t.Fatalf("%+v", ps)
	}
}

// (g) Two workflows, one stale: the stale one is named, alone.
func TestInitAnchorTwoWorkflowsOneStale(t *testing.T) {
	yaml := anchorModeYAML() + "  api: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newAnchorModeRig(t, yaml, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [api, app] }\n")
	putWorkflowRecord("app", "0.4.0")
	putWorkflowRecord("api", "0.3.1")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != wantFail("api", "web-node", r.cfg, false, reasonRecordVersionOld("0.3.1")) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, yaml)
}

// (h) The line already there: exit 0 with a fresh image, exit 1 (the
// dangerous state) with a stale one.
func TestInitAnchorAlreadyPresent(t *testing.T) {
	yaml := withRigAnchor(anchorModeYAML())
	r := newAnchorModeRig(t, yaml, true)
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor")
	if err != nil || out != r.path+" already has "+anchorRigLine+"; nothing to write\n" {
		t.Fatalf("err %v\n%s", err, out)
	}
	r.check(t, yaml)

	r = newAnchorModeRig(t, yaml, true)
	putWorkflowRecord("app", "0.3.1")
	out, _, err = anchorExec(t, "", "init", "--anchor")
	if ExitCode(err) != ExitUserError || out != r.path+" already has "+anchorRigLine+"; nothing to write\n"+wantFail("app", "web-node", r.cfg, false, reasonRecordVersionOld("0.3.1")) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(err.Error(), "already has gcp_project") {
		t.Errorf("err = %v", err)
	}
	r.check(t, yaml)
}

// (i) A custom runs bucket: the note, nothing written.
func TestInitAnchorCustomBucket(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(cfg), "runs_bucket: fugaro-runs-proj-1234", "runs_bucket: my-own-bucket", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if want := "note: gcp_project is not written to fugaro.yaml: shared config needs the default runs-bucket name fugaro-runs-proj-1234 (this installation's is \"my-own-bucket\")\n"; out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	r.check(t, r.yaml)
}

// (j) Outside a checkout, and in a checkout of a repository the project does
// not list: clear refusals.
func TestInitAnchorRefusals(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), false)
	_, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme/app is not one of project aurora's repositories") || !strings.Contains(err.Error(), "run fugaro init in this checkout to onboard it") {
		t.Errorf("not onboarded: exit %d, err %v", ExitCode(err), err)
	}
	r.check(t, r.yaml)

	newAnchorModeRig(t, anchorModeYAML(), true)
	t.Chdir(t.TempDir())
	_, _, err = anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not in a git checkout") {
		t.Errorf("no checkout: exit %d, err %v", ExitCode(err), err)
	}
}

// (k) The confirmation matrix: --yes writes without asking; no terminal and
// no --yes prints the line and exits 1; a terminal's "n" leaves the bytes.
func TestInitAnchorConfirmation(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if err != nil || out != rigDiff(r.path)+"  confirmed by --yes\n"+rigUpdated(r.path) {
		t.Fatalf("--yes: err %v\n%s", err, out)
	}
	r.check(t, withRigAnchor(r.yaml))

	r = newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.4.0")
	out, _, err = anchorExec(t, "", "init", "--anchor")
	if ExitCode(err) != ExitUserError || out != rigDiff(r.path)+rigAddLine(r.path) {
		t.Fatalf("no terminal: exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, r.yaml)

	r = newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.4.0")
	fakeTerminal(t)
	out, _, err = anchorExec(t, "n\n", "init", "--anchor")
	if ExitCode(err) != ExitUserError || out != rigDiff(r.path)+"Write this to "+r.path+"? [y/N]: "+rigAddLine(r.path) {
		t.Fatalf("n: exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, r.yaml)
}

// (l) --anchor is a mode of its own.
func TestInitAnchorExcludesTheOtherModes(t *testing.T) {
	newAnchorModeRig(t, anchorModeYAML(), true)
	for _, other := range [][]string{{"--plan-only"}, {"--print-vars"}, {"--config-only"}, {"--forget"}, {"--publish-config"}, {"--repo"},
		{"--firebase", "fugaro-fb-1234"}, {"--base", "go"}, {"--image-source", "ghcr.io/acme"}, {"--runs-bucket", "x-runs"},
		{"--onboard-repo", "acme/app"}, {"--github-app-id", "123"}, {"--budget", "5", "--budget-currency", "USD", "--billing-account", "0123AB-4567CD-89EF01"}} {
		_, _, err := anchorExec(t, "", append([]string{"init", "--anchor"}, other...)...)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--anchor") || !strings.Contains(err.Error(), other[0]) {
			t.Errorf("%s: exit %d, err %v", other, ExitCode(err), err)
		}
	}
}

// (m) --json prints one object, on success and on a failed precondition.
func TestInitAnchorJSON(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res initResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Anchor == nil || res.Anchor.State != "written" || res.Anchor.Path != r.path || res.Anchor.Line != anchorRigLine || res.Project != "aurora" {
		t.Fatalf("%v %+v\n%s", err, res.Anchor, out)
	}
	r.check(t, withRigAnchor(r.yaml))

	r = newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.3.1")
	out, _, err = anchorExec(t, "", "init", "--anchor", "--yes", "--json")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
	res = initResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Anchor == nil || res.Anchor.State != "not_written" ||
		len(res.Anchor.Problems) != 1 || failText(res.Anchor.Problems[0]) != wantFail("app", "web-node", r.cfg, false, reasonRecordVersionOld("0.3.1")) || res.Error == "" {
		t.Fatalf("%v %+v\n%s", err, res.Anchor, out)
	}
	r.check(t, r.yaml)
}

// A record read that fails in the cloud is exit 2, and nothing is written.
func TestInitAnchorRecordReadFailureIsRemote(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { return nil, errors.New("boom") }
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitRemoteError || out != wantFail("app", "web-node", r.cfg, false, reasonUnreadable("boom")) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, r.yaml)
}

// An unsafe file shape prints the line to add by hand and writes nothing.
func TestInitAnchorUnsafeShape(t *testing.T) {
	yaml := strings.Replace(anchorModeYAML(), "project: aurora\n", "\"project\": aurora\n", 1)
	r := newAnchorModeRig(t, yaml, true)
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	want := "note: the project: line is not a simple one-line value; fugaro.yaml is not edited: add this line to it by hand, at the top level: " + anchorRigLine + "\n"
	if ExitCode(err) != ExitUserError || out != want {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	r.check(t, yaml)
}

// init --repo ends with one note pointing at --anchor while the checkout's
// fugaro.yaml lacks the line of a convention-bucket installation; never
// otherwise, and never twice.
func TestInitRepoNotesTheAnchor(t *testing.T) {
	for _, c := range []struct {
		name, yaml, bucket string
		want               bool
	}{
		{"no line", anchorModeYAML(), "", true},
		{"line present", withRigAnchor(anchorModeYAML()), "", false},
		{"custom bucket", anchorModeYAML(), "my-own-bucket", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			testutil.IsolateGit(t)
			dir := repoCheckout(t, githubOrigin, c.yaml)
			lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", RunsBucket: cmp.Or(c.bucket, "fugaro-runs-proj-1234")}
			var buf strings.Builder
			r := &initRun{w: &buf}
			r.noteAnchor(t.Context(), dir, lc)
			r.noteAnchor(t.Context(), dir, lc)
			want := ""
			if c.want {
				want = "note: " + anchorHintText + "\n"
			}
			if buf.String() != want {
				t.Errorf("output %q, want %q", buf.String(), want)
			}
		})
	}
}

// doctor says the same, as information, in such a checkout.
func TestDoctorNotesTheAnchor(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want bool
	}{{"version: 1\nproject: aurora\n", true}, {"version: 1\nproject: aurora\ngcp_project: proj-1234\n", false}} {
		r := newDoctorRig(t)
		t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), c.yaml))
		o := doctorJSON(t)
		ch, ok := doctorCheckByID(o.Checks, "gcp-project-line")
		if ok != c.want || (ok && (ch.Severity != "info" || ch.Problem != anchorHintText)) {
			t.Errorf("%q: check %+v (%v)", c.yaml, ch, ok)
		}
	}
}
