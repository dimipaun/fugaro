//go:build docker

package image_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
// setup skill does. It removes the image when the test ends, and fails the
// test unless the build and its smoke test pass.
func buildImage(t *testing.T, dir, base, tag string, env ...string) image.LocalResult {
	t.Helper()
	res, stderr, err := buildImageResult(t, dir, base, tag, env...)
	if err != nil || res.Smoke == nil || !res.Smoke.Passed {
		t.Fatalf("image build: %v\nresult %+v\nstderr:\n%s", err, res, testutil.Tail(stderr))
	}
	return res
}

// buildImageResult is buildImage without the pass requirement: it returns
// the JSON result, the command's stderr and its error, and fails the test
// only when there is no JSON result at all.
func buildImageResult(t *testing.T, dir, base, tag string, env ...string) (image.LocalResult, string, error) {
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
	return res, stderr.String(), err
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
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	maps.Copy(files, testutil.NPMFixtureFiles())
	files["mise.toml"] = "[tools]\nnode = \"24\"\n"
	files["fugaro.yaml"] = `version: 1
project: aurora
git: { provider: github }
workflows:
  app:
    image:
      apt: [jq]
      setup:
        - sudo -n apt-get update -qq && sudo -n apt-get install -y -qq --no-install-recommends tree >/dev/null
        - test -n "$NPM_TOKEN" && mkdir -p ~/.cache/fugaro-fixture && echo warm > ~/.cache/fugaro-fixture/marker
        - printf %s "$NPM_TOKEN" | sha256sum > ~/.cache/fugaro-fixture/token-sha && test -z "${OTHER_TOKEN+set}"
    commands: { build: sh build.sh, test: sh test.sh, reports: ["build/test-results/*.xml"] }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
      - { name: other-token, env: OTHER_TOKEN }
`
	const canary = "npm-canary-7d41c2e9"
	const tag = "fugaro-test-npm:local"
	res := buildImage(t, checkout(t, files), base, tag, "NPM_TOKEN="+canary)
	if res.Dockerfile != "generated" {
		t.Errorf("dockerfile = %q", res.Dockerfile)
	}
	// The selftest's own hardening checks (no unexpected setuid-root binary,
	// no leftover sudoers rule) must have run and passed too, not just the
	// checkout-facing checks: a base image that lost them is not hardened.
	for _, name := range []string{"no-sudo", "no-setuid", "no-setgid", "no-file-caps", "sudoers", "init", "user", "git-credentials", "home-credentials", "verify-build"} {
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
	// The setup step saw the secret's exact value (through the file mount and
	// the RUN's export), and the unset OTHER_TOKEN stayed unset.
	sum := sha256.Sum256([]byte(canary))
	if got := testutil.Docker(t, "run", "--rm", tag, "cat", "/home/fugaro/.cache/fugaro-fixture/token-sha"); !strings.HasPrefix(got, hex.EncodeToString(sum[:])+" ") {
		t.Fatalf("the setup step saw NPM_TOKEN hashing to %q, want %x", got, sum)
	}
	// The setup step saw the secret, so the build had it, but nothing kept it.
	if strings.Contains(testutil.Docker(t, "history", "--no-trunc", tag), canary) {
		t.Fatal("the build secret is in the image history")
	}
	if testutil.ImageContains(t, tag, canary) {
		t.Fatal("the build secret is baked into an image layer")
	}
}

// TestSelftestFlagsSetgidAndFileCaps: a setup step with sudo that leaves a
// setgid binary or grants a file capability must fail the smoke test, the
// way a leftover setuid binary does. The setgid binary sits in a 0711
// directory fugaro can't list, which only the root scan can see into.
func TestSelftestFlagsSetgidAndFileCaps(t *testing.T) {
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "  app:\n", `  app:
    image:
      setup:
        - sudo -n install -d -m 0711 /opt/hidden && sudo -n install -m 2755 /usr/bin/true /opt/hidden/sgid-true
        - sudo -n install -m 0755 /usr/bin/true /usr/local/bin/capped-true && sudo -n setcap cap_net_raw+ep /usr/local/bin/capped-true
`, 1)
	res, stderr, err := buildImageResult(t, checkout(t, files), base, "fugaro-test-hardening:local")
	if err == nil || res.Smoke == nil || res.Smoke.Passed {
		t.Fatalf("want the smoke test to fail, got err=%v result %+v\nstderr:\n%s", err, res, testutil.Tail(stderr))
	}
	for name, want := range map[string]string{"no-setgid": "opt/hidden/sgid-true", "no-file-caps": "usr/local/bin/capped-true"} {
		if c := smokeCheck(res, name); c.OK || !strings.Contains(c.Detail, want) {
			t.Errorf("smoke check %s = %+v, want a failure naming %s", name, c, want)
		}
	}
	if c := smokeCheck(res, "no-setuid"); !c.OK {
		t.Errorf("no-setuid = %+v, want it to pass", c)
	}
}

// TestDerivedYarnBerryWithNodePin and TestDerivedNodePinSignedByARetiredKey
// (image.node pinned to 24.19.0 and the retired-key release 18.17.0) are
// deleted in the base-image cut, not migrated to image.tools: both pinned
// image.node specifically to exercise install-node's own download and
// GPG-keyring verification of a Node release, a mechanism this repository no
// longer has — Node now comes from `mise install` (design base-image.md
// section 4), which manages its own download integrity outside this
// repository's control. A `mise install` step also needs the Fugaro base
// image (mise is baked there, not in images/web-node, which these tests
// otherwise build FROM to get a throwaway Node for generating a yarn.lock/
// package-lock.json fixture), so migrating either test would mean building
// its lockfile fixture from one base image and its derived image from
// another — a new, unverified-without-Docker test shape, not a mechanical
// rename. TestSampleProjectNodePythonOnTheBase below is the design's
// intended coverage for "a project's pinned runtime version is honored on
// the Fugaro base" (mise.toml, not image.node).

// TestDerivedSkipBuildScripts: with image.skip_build_scripts, the warm-up
// installs the dependencies but runs neither the repository's nor a
// dependency's postinstall script. The same package.json run through a plain
// npm install shows both scripts do run otherwise.
func TestDerivedSkipBuildScripts(t *testing.T) {
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	pkg := `{"name":"fugaro-fixture","version":"0.0.0","private":true,"dependencies":{"dep":"file:dep"},"scripts":{"postinstall":"touch \"$HOME/root-postinstall-ran\""}}` + "\n"
	dep := `{"name":"dep","version":"1.0.0","scripts":{"postinstall":"touch \"$HOME/dep-postinstall-ran\""}}` + "\n"
	// Let npm write the lockfile, and check the scripts run without the
	// setting. The bare base has no node on PATH (design base-image.md
	// section 4: runtimes come from mise), so this throwaway run installs it
	// through mise first.
	lock := testutil.Docker(t, "run", "--rm", "-e", "PKG="+pkg, "-e", "DEP="+dep, base, "sh", "-c",
		`set -e; mise install node@24 >&2 && mkdir -p /tmp/p/dep && cd /tmp/p && printf '%s' "$PKG" > package.json && printf '%s' "$DEP" > dep/package.json && mise exec node@24 -- npm install --no-audit --no-fund >&2 && test -e ~/root-postinstall-ran && test -e ~/dep-postinstall-ran && cat package-lock.json`) + "\n"
	files := testutil.FixtureFiles(t)
	files["mise.toml"] = "[tools]\nnode = \"24\"\n"
	files["package.json"], files["dep/package.json"], files["package-lock.json"] = pkg, dep, lock
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "  app:\n", "  app:\n    image: { skip_build_scripts: true }\n", 1)
	dir := checkout(t, files)
	render := exec.Command(testutil.BuildFugaro(t), "image", "render")
	render.Dir = dir
	if out, err := render.Output(); err != nil || !strings.Contains(string(out), "; npm ci --ignore-scripts\n") {
		t.Fatalf("fugaro image render: %v\n%s", err, out)
	}
	const tag = "fugaro-test-skip-build:local"
	buildImage(t, dir, base, tag)
	if out := testutil.Docker(t, "run", "--rm", tag, "sh", "-c",
		`test -f node_modules/dep/package.json && echo installed; ls ~/root-postinstall-ran ~/dep-postinstall-ran 2>/dev/null; true`); out != "installed" {
		t.Fatalf("want dep installed and no postinstall marker, got %q", out)
	}
}

func TestRepoDockerfileEscapeHatch(t *testing.T) {
	base := testutil.BaseImageOf(t, "base")
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
		"fugaro.yaml":            strings.Replace(files["fugaro.yaml"], "  app:\n", "  app:\n    dockerfile: .fugaro/app.Dockerfile\n", 1),
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
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	archive := testutil.DockerOutput(t, "run", "--rm", base, "sh", "-c",
		"set -e; mise install node@24 >&2 && mkdir /tmp/p && cd /tmp/p && mise exec node@24 -- npm init -y >/dev/null && "+
			"mise exec node@24 -- npm install --package-lock-only --no-audit --no-fund @playwright/test@1.63.0 >&2 && tar -cf - package.json package-lock.json")
	files := testutil.FixtureFiles(t)
	maps.Copy(files, untar(t, archive))
	files["mise.toml"] = "[tools]\nnode = \"24\"\n"
	files["pw-smoke.js"] = playwrightSmoke
	files["fugaro.yaml"] = `version: 1
project: aurora
git: { provider: github }
workflows:
  app:
    image:
      setup: ["npx playwright install --with-deps chromium"]
    commands: { build: node pw-smoke.js, test: sh test.sh, reports: ["build/test-results/*.xml"] }
`
	res := buildImage(t, checkout(t, files), base, "fugaro-test-playwright:local")
	if c := smokeCheck(res, "verify-build"); !c.OK {
		t.Fatalf("headless Chromium did not run as fugaro: %+v", c)
	}
}

// The acceptance criterion (design base-image.md): a project with Node and
// Python in mise.toml builds on the base, its image passes the selftest,
// mise-tools included, and its tests run on the declared versions.
func TestSampleProjectNodePythonOnTheBase(t *testing.T) {
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	files := map[string]string{
		"mise.toml":         "[tools]\nnode = \"24.21.0\"\npython = \"3.12\"\n",
		"package.json":      `{"name":"sample","version":"1.0.0","private":true,"scripts":{"test":"node test.js"}}`,
		"package-lock.json": `{"name":"sample","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"sample","version":"1.0.0"}}}`,
		"test.js":           `if (!process.version.startsWith("v24.21.")) { console.error("node " + process.version); process.exit(1) }` + "\n",
		"test_sample.py":    "import sys\nassert sys.version_info[:2] == (3, 12), sys.version\n",
		"fugaro.yaml": `version: 1
project: aurora
git: { provider: github }
workflows:
  app:
    commands: { build: node --version && python3 --version, test: npm test && python3 test_sample.py }
`,
	}
	res := buildImage(t, checkout(t, files), base, "fugaro-test-sample:local")
	for _, name := range []string{"mise-tools", "tool:mise", "tool:claude", "verify-build"} {
		if c := smokeCheck(res, name); !c.OK {
			t.Errorf("%s: %+v", name, c)
		}
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
