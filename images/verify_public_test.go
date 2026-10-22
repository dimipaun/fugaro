package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeVerifyDocker answers verify-public.sh's docker calls. FAKE_PRIVATE=ref
// makes that reference refuse anonymous reads, FAKE_MANIFEST picks the manifest
// text, FAKE_PULL_FAIL makes a platform pull fail, and the DOCKER_CONFIG the
// script exported is recorded so the test can see no real login was in use.
const fakeVerifyDocker = `#!/bin/sh
echo "$DOCKER_CONFIG" >> "$FAKE_LOG"
echo "$*" >> "$FAKE_LOG"
case "$1 $2" in
  "manifest inspect")
    if [ "$3" = "${FAKE_PRIVATE:-}" ]; then echo "unauthorized: authentication required" >&2; exit 1; fi
    printf '%s\n' "$FAKE_MANIFEST" ;;
  "pull --quiet")
    [ -n "${FAKE_PULL_FAIL:-}" ] && { echo "no matching manifest for linux/amd64" >&2; exit 1; }
    echo ok ;;
  "buildx imagetools") echo sha256:abc123 ;;
esac
`

const indexAmd64 = `{"manifests":[{"platform":{"architecture":"arm64","os":"linux"}},{"platform":{"architecture":"amd64","os":"linux"}}]}`
const indexArmOnly = `{"manifests":[{"platform":{"architecture":"arm64","os":"linux"}}]}`
const singleManifest = `{"schemaVersion":2,"config":{"digest":"sha256:x"}}`

func runVerify(t *testing.T, manifest string, env []string, refs ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeVerifyDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	cmd := exec.Command("sh", append([]string{"verify-public.sh"}, refs...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "FAKE_MANIFEST="+manifest, "DOCKER_CONFIG=/home/me/.docker")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(out), string(logged), err
}

func TestVerifyPublicPassesForAnAnonymousAmd64Index(t *testing.T) {
	out, log, err := runVerify(t, indexAmd64, nil, "ghcr.io/o/a:1", "ghcr.io/o/b:1")
	if err != nil {
		t.Fatalf("failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "ghcr.io/o/b:1 ok (sha256:abc123)") {
		t.Errorf("output does not report the digest: %s", out)
	}
	if strings.Contains(log, "/home/me/.docker") {
		t.Errorf("docker ran with the caller's credentials: %s", log)
	}
}

func TestVerifyPublicFailsForAPrivatePackage(t *testing.T) {
	out, _, err := runVerify(t, indexAmd64, []string{"FAKE_PRIVATE=ghcr.io/o/b:1"}, "ghcr.io/o/a:1", "ghcr.io/o/b:1")
	if err == nil {
		t.Fatalf("passed with a private image: %s", out)
	}
	if !strings.Contains(out, "ghcr.io/o/b:1 cannot be read without a login") || !strings.Contains(out, "ghcr.io/o/a:1 ok") {
		t.Errorf("output should name the private image and still check the rest: %s", out)
	}
}

func TestVerifyPublicFailsWithoutAmd64(t *testing.T) {
	out, _, err := runVerify(t, indexArmOnly, nil, "ghcr.io/o/a:1")
	if err == nil || !strings.Contains(out, "no linux/amd64 entry") {
		t.Fatalf("err=%v output=%s", err, out)
	}
}

func TestVerifyPublicSingleManifestNeedsAnAmd64Pull(t *testing.T) {
	if out, _, err := runVerify(t, singleManifest, nil, "ghcr.io/o/a:1"); err != nil {
		t.Fatalf("failed: %v\n%s", err, out)
	}
	out, _, err := runVerify(t, singleManifest, []string{"FAKE_PULL_FAIL=1"}, "ghcr.io/o/a:1")
	if err == nil || !strings.Contains(out, "not pullable for linux/amd64") {
		t.Fatalf("err=%v output=%s", err, out)
	}
}

func TestVerifyPublicNeedsAReference(t *testing.T) {
	if _, _, err := runVerify(t, indexAmd64, nil); err == nil {
		t.Error("passed with no references")
	}
}

const indexAmd64Only = `{"manifests":[{"platform":{"architecture":"amd64","os":"linux"}}]}`

func TestVerifyPublicRequiresArm64ForTheBase(t *testing.T) {
	if out, _, err := runVerify(t, indexAmd64, nil, "--arm64", "ghcr.io/o/base:1"); err != nil {
		t.Fatalf("an amd64+arm64 index failed: %v\n%s", err, out)
	}
	out, _, err := runVerify(t, indexAmd64Only, nil, "--arm64", "ghcr.io/o/base:1")
	if err == nil || !strings.Contains(out, "no linux/arm64 entry") {
		t.Fatalf("an amd64-only index passed --arm64: %v\n%s", err, out)
	}
	out, _, err = runVerify(t, singleManifest, nil, "--arm64", "ghcr.io/o/base:1")
	if err == nil || !strings.Contains(out, "is not a multi-platform index") {
		t.Fatalf("a single manifest passed --arm64: %v\n%s", err, out)
	}
}

// TestImagesWorkflowPublishesAndVerifiesEveryImage: the history and
// credentials images are published, and a job with no packages permission and
// no login fails the release when any published image is not anonymously
// pullable. The base (fugaro-base) is published and verified by its own
// matrix-free publish-base/verify-public --arm64 path
// (TestImagesWorkflowPublishesAMultiArchBase), so publish's own matrix, after
// the 0.7.0 cut, holds only history; the credentials service's image is
// published by publish-credentials, which carries the signing chain its
// consumer needs (fugaro init resolves its -pinned tag and verifies the
// signature).
func TestImagesWorkflowPublishesAndVerifiesEveryImage(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Needs       any               `yaml:"needs"`
			If          string            `yaml:"if"`
			Permissions map[string]string `yaml:"permissions"`
			Strategy    struct {
				Matrix struct {
					Base []string `yaml:"base"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				If   string `yaml:"if"`
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	pub, ok := wf.Jobs["publish"]
	if !ok {
		t.Fatal("no publish job")
	}
	if got := pub.Strategy.Matrix.Base; len(got) != 1 || got[0] != "history" {
		t.Errorf("publish matrix = %v, want only history (the credentials image is published and signed by publish-credentials)", got)
	}
	credpub, ok := wf.Jobs["publish-credentials"]
	if !ok {
		t.Fatal("no publish-credentials job: the credentials image would be built and never published")
	}
	// Every image in the tree is one the kinds list can name, and every step
	// of its build is gated on that list: an image the list never reports is a
	// leg that never runs and never fails. The publish jobs carry the kinds
	// gate on the job's own if instead (asserted below for
	// publish-credentials), the way publish-base does.
	if !strings.Contains(string(data), "for k in history base credentials;") {
		t.Error("the kinds step does not report every image the workflow builds")
	}
	for _, s := range wf.Jobs["credentials"].Steps {
		if !strings.Contains(s.If, "needs.version.outputs.kinds") {
			t.Errorf("the credentials job's step %q is not gated on the images the tree builds (if: %q)", s.Name, s.If)
		}
	}
	for _, img := range []string{"history", "credentials"} {
		if !strings.Contains(string(data), "fugaro-"+img+":ci") || !strings.Contains(string(data), "images/smoke-"+img+".sh") {
			t.Errorf("the %s image is not built and smoke tested", img)
		}
	}
	if !strings.Contains(fmtNeeds(pub.Needs), "history") {
		t.Errorf("publish does not wait for the history build: %v", pub.Needs)
	}
	if !strings.Contains(fmtNeeds(credpub.Needs), "credentials") {
		t.Errorf("publish-credentials does not wait for the credentials build: %v", credpub.Needs)
	}
	if !strings.Contains(credpub.If, "credentials") || !strings.Contains(credpub.If, "publish") {
		t.Errorf("publish-credentials must publish only when publishing, only for a tree that holds the image: if=%q", credpub.If)
	}
	if !strings.Contains(string(data), "images/size-budget.sh fugaro-credentials:ci") {
		t.Error("the credentials image is not held to a size budget")
	}
	if !strings.Contains(string(data), "images/scan.sh --secrets fugaro-credentials:ci") {
		t.Error("the credentials image is not secret-scanned on every run")
	}
	if strings.Contains(string(data), "not published until") {
		t.Error("a comment still says the history image is not published")
	}
	ver, ok := wf.Jobs["verify-public"]
	if !ok {
		t.Fatal("no verify-public job")
	}
	if !strings.Contains(fmtNeeds(ver.Needs), "publish") || !strings.Contains(fmtNeeds(ver.Needs), "publish-credentials") || !strings.Contains(ver.If, "publish") {
		t.Errorf("verify-public must run after both publish jobs, only when publishing: needs=%v if=%q", ver.Needs, ver.If)
	}
	if len(ver.Permissions) != 1 || ver.Permissions["contents"] != "read" {
		t.Errorf("verify-public permissions = %v, want only contents: read", ver.Permissions)
	}
	var script string
	for _, s := range ver.Steps {
		script += s.Run + "\n"
	}
	if !strings.Contains(script, "images/verify-public.sh") {
		t.Error("verify-public does not run images/verify-public.sh")
	}
	if strings.Contains(script, "docker login") {
		t.Error("verify-public logs in; it must check as an anonymous user")
	}
	for _, img := range []string{"history", "credentials"} {
		if !strings.Contains(script, img) {
			t.Errorf("verify-public does not check %s: every image a publish job pushes has to be pulled back anonymously, or a package that is still private ships unnoticed", img)
		}
	}
	for _, img := range []string{"web-node", "go", "java-services"} { // removed in 0.7.0
		if strings.Contains(script, img) {
			t.Errorf("verify-public still checks the removed base %s", img)
		}
	}
}

func fmtNeeds(n any) string {
	switch v := n.(type) {
	case string:
		return v
	case []any:
		var s []string
		for _, x := range v {
			s = append(s, x.(string))
		}
		return strings.Join(s, " ")
	}
	return ""
}
