package cli

import (
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
	"github.com/dimipaun/fugaro/internal/task"
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

func putWorkflowRecord(wf, version string) {
	slug, _ := task.Slug("github", "acme/app")
	records[imagecheck.RecordKey(slug, wf)] = `{"version":1,"fugaro_version":"` + version + `"}`
}

// untouched asserts the checkout's fugaro.yaml, the terraform log and the
// shared-config bucket: --anchor touches none of them unless it writes the
// line, which want says.
func (r *anchorModeRig) check(t *testing.T, want string) {
	t.Helper()
	if got := readYAML(t, r.dir); got != want {
		t.Errorf("fugaro.yaml:\n%s\nwant:\n%s", got, want)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("--anchor ran terraform: %q", calls)
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

// (b) An image built by an older release: the exact commands, nothing written.
func TestInitAnchorOldRecordFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	putWorkflowRecord("app", "0.3.1")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	msg := oldImageWarning("acme/app", "app", "0.3.1")
	if !strings.Contains(msg, "run fugaro init (it copies the current release base image), then fugaro image build --repo acme/app --workflow app") ||
		!strings.Contains(msg, "fugaro 0.3.1") {
		t.Errorf("message: %s", msg)
	}
	if out != failText(msg) {
		t.Errorf("output:\n%s\nwant:\n%s", out, failText(msg))
	}
	r.check(t, r.yaml)
}

// (c) No build record: the same commands.
func TestInitAnchorMissingRecordFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != failText(oldImageWarning("acme/app", "app", "")) {
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
		if ExitCode(err) != ExitUserError || out != failText(devImageWarning("acme/app", "app", v)) || !strings.Contains(out, "built by a development CLI") {
			t.Fatalf("%s: exit %d, err %v\n%s", v, ExitCode(err), err, out)
		}
		r.check(t, r.yaml)
	}
}

// (e) A dev base image the local config pins fails, even with a current
// record: the next build would bake its binary in again.
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
	msg := customBaseWarning(ref, "go", r.cfg)
	for _, s := range []string{"the base image " + ref + " for kind go is not a release >= 0.4.0", "is never replaced by fugaro init --base", "remove base_images.go from " + r.cfg + " (keep a backup)", "fugaro init --base go from outside the checkout"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message lacks %q: %s", s, msg)
		}
	}
	if out != failText(msg) {
		t.Errorf("output:\n%s\nwant:\n%s", out, failText(msg))
	}
	r.check(t, yaml)
}

// (f) A managed release base image older than the field fails too.
func TestInitAnchorOldManagedBaseFails(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	const ref = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.3.1"
	r.appendConfig(t, "base_images:\n  web-node: "+ref+"\n")
	putWorkflowRecord("app", "0.4.0")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != failText(oldBaseWarning(ref, "web-node", "0.3.1")) {
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

// (g) Two workflows, one stale: the stale one is named, alone.
func TestInitAnchorTwoWorkflowsOneStale(t *testing.T) {
	yaml := anchorModeYAML() + "  api: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newAnchorModeRig(t, yaml, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [api, app] }\n")
	putWorkflowRecord("app", "0.4.0")
	putWorkflowRecord("api", "0.3.1")
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitUserError || out != failText(oldImageWarning("acme/app", "api", "0.3.1")) {
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
	if ExitCode(err) != ExitUserError || out != r.path+" already has "+anchorRigLine+"; nothing to write\n"+failText(oldImageWarning("acme/app", "app", "0.3.1")) {
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
		len(res.Anchor.Problems) != 1 || res.Anchor.Problems[0] != oldImageWarning("acme/app", "app", "0.3.1") || res.Error == "" {
		t.Fatalf("%v %+v\n%s", err, res.Anchor, out)
	}
	r.check(t, r.yaml)
}

// A record read that fails in the cloud is exit 2, and nothing is written.
func TestInitAnchorRecordReadFailureIsRemote(t *testing.T) {
	r := newAnchorModeRig(t, anchorModeYAML(), true)
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { return nil, errors.New("boom") }
	out, _, err := anchorExec(t, "", "init", "--anchor", "--yes")
	if ExitCode(err) != ExitRemoteError || out != failText(unknownImageWarning("acme/app", "app", "boom")) {
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
