//go:build docker

package image_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// checkout creates a remote holding files and returns a clone of it.
func checkout(t *testing.T, files map[string]string) string {
	t.Helper()
	remote := testutil.NewRemote(t, files)
	parent := t.TempDir()
	dir := filepath.Join(parent, "app")
	testutil.Git(t, parent, "clone", "--quiet", remote, dir)
	return dir
}

// buildImage runs `fugaro image build --local --json` in dir, the way the
// onboard skill does. It removes the image when the test ends, and fails the
// test unless the build and its smoke test pass.
func buildImage(t *testing.T, dir, base, tag string, env ...string) image.LocalResult {
	t.Helper()
	cmd := exec.Command(testutil.BuildFugaro(t), "image", "build", "--local", "--json",
		"--base", base, "--tag", tag, "--platform", testutil.DockerPlatform(t))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "--force", tag).Run() })
	var res image.LocalResult
	if jerr := json.Unmarshal(stdout.Bytes(), &res); jerr != nil {
		t.Fatalf("no JSON result (exit: %v): %v\nstderr:\n%s", err, jerr, testutil.Tail(stderr.String()))
	}
	if err != nil || res.Smoke == nil || !res.Smoke.Passed {
		t.Fatalf("image build: %v\nresult %+v\nstderr:\n%s", err, res, testutil.Tail(stderr.String()))
	}
	return res
}

func smokeCheck(res image.LocalResult, name string) image.Check {
	for _, c := range res.Smoke.Checks {
		if c.Name == name {
			return c
		}
	}
	return image.Check{}
}

func TestDerivedNPMSetupSudoAndSecrets(t *testing.T) {
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	maps.Copy(files, testutil.NPMFixtureFiles())
	files["fugaro.yaml"] = `version: 1
git: { provider: github }
workflows:
  app:
    base: web-node
    image:
      apt: [jq]
      setup:
        - sudo -n apt-get update -qq && sudo -n apt-get install -y -qq --no-install-recommends tree >/dev/null
        - test -n "$NPM_TOKEN" && mkdir -p ~/.cache/fugaro-fixture && echo warm > ~/.cache/fugaro-fixture/marker
    commands: { build: sh build.sh, test: sh test.sh, reports: ["build/test-results/*.xml"] }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
`
	const canary = "npm-canary-7d41c2e9"
	const tag = "fugaro-test-npm:local"
	res := buildImage(t, checkout(t, files), base, tag, "NPM_TOKEN="+canary)
	if res.Dockerfile != "generated" {
		t.Errorf("dockerfile = %q", res.Dockerfile)
	}
	// Controller ruling: the selftest's own hardening checks (no unexpected
	// setuid-root binary, no leftover sudoers rule) must have run and passed
	// too, not just the checkout-facing checks the brief names.
	for _, name := range []string{"no-sudo", "no-setuid", "sudoers", "init", "user", "git-credentials", "home-credentials", "verify-build"} {
		if c := smokeCheck(res, name); !c.OK {
			t.Errorf("smoke check %s = %+v", name, c)
		}
	}
	// apt as root, sudo in a setup step, and a per-user cache owned by fugaro.
	out := testutil.Docker(t, "run", "--rm", tag, "sh", "-c",
		"jq --version && tree --version >/dev/null && cat ~/.cache/fugaro-fixture/marker && stat -c %U ~/.cache/fugaro-fixture/marker")
	if !strings.HasPrefix(out, "jq-") || !strings.HasSuffix(out, "warm\nfugaro") {
		t.Fatalf("image contents: %q", out)
	}
	// The setup step saw the secret, so the build had it, but nothing kept it.
	if strings.Contains(testutil.Docker(t, "history", "--no-trunc", tag), canary) {
		t.Fatal("the build secret is in the image history")
	}
	if testutil.ImageContains(t, tag, canary) {
		t.Fatal("the build secret is baked into an image layer")
	}
}

func TestDerivedYarnBerryWithNodePin(t *testing.T) {
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	pkg := `{"name":"fugaro-fixture","private":true,"packageManager":"yarn@4.16.0"}` + "\n"
	rc := "nodeLinker: node-modules\n"
	// Let Yarn itself write a lockfile that --immutable accepts.
	lock := testutil.Docker(t, "run", "--rm", "-e", "PKG="+pkg, "-e", "RC="+rc, base, "sh", "-c",
		`set -e; mkdir /tmp/y && cd /tmp/y && printf '%s' "$PKG" > package.json && printf '%s' "$RC" > .yarnrc.yml && yarn install >&2 && cat yarn.lock`) + "\n"
	files := testutil.FixtureFiles(t)
	files["package.json"], files[".yarnrc.yml"], files["yarn.lock"] = pkg, rc, lock
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "    base: web-node\n", "    base: web-node\n    image: { node: \"24.19.0\" }\n", 1)
	const tag = "fugaro-test-yarn:local"
	res := buildImage(t, checkout(t, files), base, tag)
	if c := smokeCheck(res, "node"); !strings.HasPrefix(c.Detail, "v24.19.0") {
		t.Errorf("node check = %+v", c)
	}
	// Yarn was fetched by corepack during the build, so it works offline.
	if out := testutil.Docker(t, "run", "--rm", "--network", "none", tag, "sh", "-c", "node -v && yarn --version"); out != "v24.19.0\n4.16.0" {
		t.Fatalf("toolchain = %q", out)
	}
}

// TestDerivedNodePinSignedByARetiredKey pins image.node to a release whose
// SHASUMS256.txt was signed by a key from nodejs/node's "previous releases"
// list (v18.17.0, Danielle Adams's 74F12602B6F1C4E913FAA37AD3A89613643B6201),
// so the derived build must verify it against the base's baked keyring.
func TestDerivedNodePinSignedByARetiredKey(t *testing.T) {
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "    base: web-node\n", "    base: web-node\n    image: { node: \"18.17.0\" }\n", 1)
	const tag = "fugaro-test-node18:local"
	res := buildImage(t, checkout(t, files), base, tag)
	if c := smokeCheck(res, "node"); !c.OK || !strings.HasPrefix(c.Detail, "v18.17.0") {
		t.Errorf("node check = %+v", c)
	}
}

func TestRepoDockerfileEscapeHatch(t *testing.T) {
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	dir := checkout(t, files)
	fugaro := testutil.BuildFugaro(t)

	render := exec.Command(fugaro, "image", "render")
	render.Dir = dir
	generated, err := render.Output()
	if err != nil {
		t.Fatalf("fugaro image render: %v", err)
	}
	custom := string(generated) + "RUN mkdir -p /work/repo/build && echo custom > /work/repo/build/custom-marker\n"
	testutil.WriteFiles(t, dir, map[string]string{
		".fugaro/app.Dockerfile": custom,
		"fugaro.yaml":            strings.Replace(files["fugaro.yaml"], "    base: web-node\n", "    base: web-node\n    dockerfile: .fugaro/app.Dockerfile\n", 1),
	})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "Add a repository Dockerfile")

	validate := exec.Command(fugaro, "validate", "--json")
	validate.Dir = dir
	if out, err := validate.CombinedOutput(); err != nil {
		t.Fatalf("fugaro validate: %v\n%s", err, out)
	}
	const tag = "fugaro-test-dockerfile:local"
	res := buildImage(t, dir, base, tag)
	if res.Dockerfile != ".fugaro/app.Dockerfile" {
		t.Errorf("dockerfile = %q", res.Dockerfile)
	}
	if out := testutil.Docker(t, "run", "--rm", tag, "cat", "/work/repo/build/custom-marker"); out != "custom" {
		t.Fatalf("custom-marker = %q", out)
	}
}

const playwrightSmoke = `const { chromium } = require('@playwright/test');
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.setContent('<h1>fugaro</h1>');
  console.log('playwright:', await page.textContent('h1'));
  await browser.close();
})().catch((err) => { console.error(err); process.exit(1); });
`

// TestPlaywrightChromium installs Playwright's Chromium with --with-deps in a
// setup step and launches it headless from the smoke test's verify build,
// as fugaro.
func TestPlaywrightChromium(t *testing.T) {
	if os.Getenv("FUGARO_TEST_PLAYWRIGHT") == "" {
		t.Skip("set FUGARO_TEST_PLAYWRIGHT=1 to download Playwright and Chromium (several hundred MB)")
	}
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	archive := testutil.DockerOutput(t, "run", "--rm", base, "sh", "-c",
		"set -e; mkdir /tmp/p && cd /tmp/p && npm init -y >/dev/null && "+
			"npm install --package-lock-only --no-audit --no-fund @playwright/test@1.63.0 >&2 && tar -cf - package.json package-lock.json")
	files := testutil.FixtureFiles(t)
	maps.Copy(files, untar(t, archive))
	files["pw-smoke.js"] = playwrightSmoke
	files["fugaro.yaml"] = `version: 1
git: { provider: github }
workflows:
  app:
    base: web-node
    image:
      setup: ["npx playwright install --with-deps chromium"]
    commands: { build: node pw-smoke.js, test: sh test.sh, reports: ["build/test-results/*.xml"] }
`
	res := buildImage(t, checkout(t, files), base, "fugaro-test-playwright:local")
	if c := smokeCheck(res, "verify-build"); !c.OK {
		t.Fatalf("headless Chromium did not run as fugaro: %+v", c)
	}
}

func untar(t *testing.T, data []byte) map[string]string {
	t.Helper()
	files := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = string(b)
	}
}
