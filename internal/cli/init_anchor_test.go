package cli

// The shared rig the preview, image-refresh and anchor-check tests read a
// checkout through: a fake terraform on PATH, a checkout of acme/app as the
// working directory, a fake runs bucket for build records and a file:// bucket
// the shared config would be published to. `fugaro update --anchor` itself is
// gone (U9b, R14) — the gcp_project line is `fugaro project repos add`'s — so
// what is left here is the rig and the check-text tests, and the flag tests
// themselves live in project_enroll_anchor_test.go.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// anchorModeRig is the init rig's local config (a fake terraform on PATH,
// whose calls are logged) with acme/app onboarded, a checkout of acme/app as
// the working directory, a fake runs bucket for the build records and a
// file:// bucket the shared config would be published to.
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

func anchorModeYAML() string { return checkoutYAML("github", "oauth", "aurora", "") }

func withRigAnchor(yaml string) string {
	return strings.Replace(yaml, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
}

// releaseBase is a release base image a current build ran FROM.
const releaseBase = "ghcr.io/dimipaun/fugaro-base:0.4.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// putWorkflowRecordFrom is a build record of acme/app's workflow wf submitted
// by a fugaro of version, built FROM base ("" for a record that does not say).
func putWorkflowRecordFrom(wf, version, base string) {
	slug, _ := task.Slug("github", "acme/app")
	records[imagecheck.RecordKey(slug, wf)] = `{"version":1,"fugaro_version":"` + version + `","base_ref":"` + base + `"}`
}

// check asserts the checkout's fugaro.yaml, the terraform log, the registry
// and the shared-config bucket: the run touches none of them unless it writes
// the line, which want says.
func (r *anchorModeRig) check(t *testing.T, want string) {
	t.Helper()
	if got := readYAML(t, r.dir); got != want {
		t.Errorf("fugaro.yaml:\n%s\nwant:\n%s", got, want)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the run ran terraform: %q", calls)
	}
	if reqs := r.ar.Requests(); len(reqs) != 0 {
		t.Errorf("the run called Artifact Registry: %+v", reqs)
	}
	if ents, err := os.ReadDir(r.published); err != nil || len(ents) != 0 {
		t.Errorf("the run published to the runs bucket: %v %v", ents, err)
	}
}

// doctor says the same, as information, in a listed repository's checkout.
func TestDoctorNotesTheAnchor(t *testing.T) {
	noProjectLayerBucket(t)
	for _, c := range []struct {
		yaml   string
		listed bool
		want   bool
	}{
		{"version: 1\nproject: aurora\n", true, true},
		{"version: 1\nproject: aurora\ngcp_project: proj-1234\n", true, false},
		{"version: 1\nproject: aurora\n", false, false},
	} {
		r := newDoctorRig(t)
		if c.listed {
			if err := os.WriteFile(r.cfgPath, []byte(r.cfg+"repos:\n  acme/app: { provider: github, workflows: [app] }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		dir := gitCheckout(t, filepath.Join(r.dir, "app"), c.yaml)
		testutil.Git(t, dir, "remote", "add", "origin", githubOrigin)
		t.Chdir(dir)
		o := doctorJSON(t)
		ch, ok := doctorCheckByID(o.Checks, "gcp-project-line")
		if ok != c.want || (ok && (ch.Severity != "info" || ch.Problem != anchorHintText)) {
			t.Errorf("%q listed %v: check %+v (%v)", c.yaml, c.listed, ch, ok)
		}
	}
}

// A registry host that cannot be worked out is said as such, not taken for
// a custom base image.
func TestGCPProjectProblemsRegistryHostError(t *testing.T) {
	lc := anchorLC()
	lc.RegistryHost = "us-east5-docker.pkg.dev/another-project"
	lc.BaseImages = map[string]string{"base": "us-east5-docker.pkg.dev/fugaro-aurora/fugaro-base/fugaro-base:0.4.0"}
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	putRecordData(t, `{"version":1,"fugaro_version":"0.4.0","base_ref":"`+releaseBase+`"}`)
	ps := gcpProjectProblems(t.Context(), lc, "", "github", "acme/app", map[string]string{"app": "base"})
	if len(ps) != 1 || !strings.Contains(ps[0].text, "the registry host is unknown") || strings.Contains(ps[0].text, "remove base_images") {
		t.Fatalf("%+v", ps)
	}
}

// A local-config path with a space is quoted in the fix.
func TestAnchorProblemTextQuotesThePath(t *testing.T) {
	got := anchorProblemText("acme/app", "app", "go", "/home/me/my configs/aurora.yaml", []string{"x"}, true)
	if !strings.Contains(got, "remove base_images.go from '/home/me/my configs/aurora.yaml' (keep a backup)") {
		t.Errorf("%s", got)
	}
}

// With no base kind known, fugaro image refresh cannot be named (it needs a
// kind), so the fix is fugaro image build for the workflow, then repos add.
func TestAnchorProblemTextKindUnknownNamesImageBuild(t *testing.T) {
	got := anchorProblemText("acme/app", "app", "", "", []string{"x"}, false)
	want := "In order: (1) fugaro image build --repo acme/app --workflow app, (2) fugaro project repos add acme/app in the repository's checkout, before you merge"
	if !strings.Contains(got, want) || strings.Contains(got, "image refresh") {
		t.Errorf("%s\nwant it to contain %q and no image refresh", got, want)
	}
}

// The base a build ran FROM is a release image of the workflow's own kind,
// on any host, at or after the field; anything else is refused.
func TestBaseRefReason(t *testing.T) {
	hex := strings.Repeat("a", 64)
	for _, c := range []struct {
		ref, kind string
		ok        bool
	}{
		{"ghcr.io/dimipaun/fugaro-base:0.4.0", "base", true},
		{"ghcr.io/dimipaun/fugaro-base:0.4.0@sha256:" + hex, "base", true},
		{"localhost:5000/x/fugaro-base:0.4.0", "base", true},
		{"ghcr.io/dimipaun/fugaro-base:0.10.0", "base", true},
		{"ghcr.io/dimipaun/fugaro-base:v0.4.0", "base", true},
		{"us-east5-docker.pkg.dev/p/fugaro-base/fugaro-base:0.4.0", "base", true},
		// This release's pin through the remote repository: the version tag
		// is kept as a marker beside the digest (design §3.6), and the
		// release regex reads that shape already, so a build FROM the pin is
		// judged on the release it names — the digest is not a refusal.
		{"us-east5-docker.pkg.dev/p/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:" + hex, "base", true},
		{"us-east5-docker.pkg.dev/p/fugaro-ghcr/acme/fugaro-base:v0.7.5@sha256:" + hex, "base", true},
		// Without the version marker it is not a release of a kind.
		{"us-east5-docker.pkg.dev/p/fugaro-ghcr/dimipaun/fugaro-base@sha256:" + hex, "base", false},
		{"us-east5-docker.pkg.dev/p/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:abc", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.4.0", "", true},       // the workflow's kind not known
		{"ghcr.io/dimipaun/fugaro-base:0.4.0", "other", false}, // a kind the ref does not match
		{"ghcr.io/dimipaun/fugaro-base:0.4.0-rc1", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.3.9", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:latest", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.4", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.4.0RC", "base", false},
		{"ghcr.io/dimipaun/FUGARO-BASE:0.4.0", "base", false},
		{"ghcr.io/dimipaun/fugaro-base@sha256:" + hex, "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.4.0@sha256:" + strings.Repeat("A", 64), "base", false},
		{"ghcr.io/dimipaun/evilfugaro-base:0.4.0", "base", false},
		{"ghcr.io/dimipaun/fugaro-base/evil:0.4.0", "base", false},
		{"fugaro-base:0.4.0", "base", false},
		{"ubuntu:24.04", "base", false},
		{"", "base", false},
		{"ghcr.io/dimipaun/fugaro-base:0.4.0\n", "base", false},
		{"ubuntu:22.04\n/fugaro-base:0.4.0", "base", false},
		{"ubuntu:24.04 /fugaro-base:0.4.0", "base", false},
		{"x/fugaro-base-evil:0.4.0", "base", false},
		{"x/fugaro-x:0.4.0", "", false},
		{"us-east5-docker.pkg.dev/p/fugaro-base/fugaro-base-cuda:1.2.0", "", false},
	} {
		if got := baseRefReason(c.ref, c.kind); (got == "") != c.ok {
			t.Errorf("baseRefReason(%q, %q) = %q, want ok %v", c.ref, c.kind, got, c.ok)
		}
	}
}

// repoReady is an applied installation of aurora with a base image for
// base, and a repository plan with nothing to change: update --repo of the
// listed acme/sandbox runs to its end.
func (r *initRig) repoReady(t *testing.T) {
	t.Helper()
	r.installationState(t)
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.4.0}\n")
	r.setPlan(t)
	r.script["plan"] = map[string]any{"exit": 0}
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.save(t)
}

// The pinned remote path is a release base image everywhere the release
// regexp is read — recipes_skew's skew gate, the layered-config gate
// (image_layer.go) and imagePredates. A narrowed edit here would make every
// migrated repository look like it predates the release, silently.
func TestBaseRefReleaseREAcceptsThePinnedRemotePath(t *testing.T) {
	hex := strings.Repeat("a", 64)
	pin := "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:" + hex
	m := baseRefReleaseRE.FindStringSubmatch(pin)
	if m == nil || m[1] != "base" || m[2] != "0.7.5" {
		t.Fatalf("baseRefReleaseRE(%q) = %q", pin, m)
	}
	if got := baseRefReason(pin, "base"); got != "" {
		t.Errorf("baseRefReason(%q, base) = %q", pin, got)
	}
	// The version marker is what is read: a digest-only reference is not a
	// release, and neither is one whose digest is malformed.
	for _, ref := range []string{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base@sha256:" + hex,
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:" + strings.Repeat("A", 64),
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:0.7.5@sha256:abc",
	} {
		if baseRefReleaseRE.FindStringSubmatch(ref) != nil {
			t.Errorf("baseRefReleaseRE(%q) matched", ref)
		}
	}
}
