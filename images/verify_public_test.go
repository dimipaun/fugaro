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

// TestImagesWorkflowPublishesAndVerifiesEveryImage: the history image is
// published beside the bases, and a job with no packages permission and no
// login fails the release when any published image is not anonymously
// pullable.
func TestImagesWorkflowPublishesAndVerifiesEveryImage(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
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
				Run string `yaml:"run"`
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
	has := func(l []string, s string) bool {
		for _, x := range l {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, img := range []string{"web-node", "go", "java-services", "history"} {
		if !has(pub.Strategy.Matrix.Base, img) {
			t.Errorf("publish matrix lacks %s: %v", img, pub.Strategy.Matrix.Base)
		}
	}
	if !strings.Contains(fmtNeeds(pub.Needs), "history") {
		t.Errorf("publish does not wait for the history build: %v", pub.Needs)
	}
	if !strings.Contains(string(data), "fugaro-history:ci") || !strings.Contains(string(data), "images/smoke-history.sh") {
		t.Error("the history image is not built and smoke tested")
	}
	if strings.Contains(string(data), "not published until") {
		t.Error("a comment still says the history image is not published")
	}
	ver, ok := wf.Jobs["verify-public"]
	if !ok {
		t.Fatal("no verify-public job")
	}
	if !strings.Contains(fmtNeeds(ver.Needs), "publish") || !strings.Contains(ver.If, "publish") {
		t.Errorf("verify-public must run after publish, only when publishing: needs=%v if=%q", ver.Needs, ver.If)
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
	for _, img := range []string{"web-node", "go", "java-services", "history"} {
		if !strings.Contains(script, img) {
			t.Errorf("verify-public does not check %s", img)
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
