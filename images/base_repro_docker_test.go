//go:build docker

package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// reproFixtureEpoch is the SOURCE_DATE_EPOCH the fixture is built with. It is
// a fixed date, not the checkout's: what is under test is that the build is
// the same twice, and a date the test also computes could only make a failure
// harder to read.
const reproFixtureEpoch = "1600000000"

// requireBuildx skips when the docker CLI has no buildx plugin. The fixture
// needs `--output` (rewrite-timestamp is an exporter option, and there is no
// flag for it), which the plain builder does not have; the docker tests'
// other builds do not, so this is only a skipped test here, never a broken
// one.
func requireBuildx(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "buildx", "version").Run(); err != nil {
		t.Skipf("docker has no buildx plugin, so no --output/rewrite-timestamp: %v", err)
	}
}

// TestBaseReproducibleFixtureBuild builds the same tree twice and requires the
// same image, which is what the catalog check reads and what the nightly
// republish has to stop doing when nothing changed (issue #1745).
//
// The base itself is too heavy to build twice here: the docker tests build it
// once and share it (testutil.BaseImageOf). So the fixture is built on top of
// that image and makes, deliberately, every byproduct the real build gets for
// free -- /var/log's and apt's logs, ldconfig's aux-cache, a Node compile
// cache, a scrambled /etc/gitconfig (git-lfs's four keys in another order),
// and a file whose mtime is the build's own clock -- then runs
// images/base/byproducts.sh, the same script the base's RUNs run, and builds
// it with the same flags images/build-base.sh passes. Two builds that disagree
// on any of those bytes, or on the timestamps the exporter rewrites, come out
// with different image IDs.
func TestBaseReproducibleFixtureBuild(t *testing.T) {
	requireBuildx(t)
	base := testutil.BaseImageOf(t, "base")
	dir := t.TempDir()
	script, err := os.ReadFile(filepath.Join("base", "byproducts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "byproducts.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	df := "FROM " + base + "\n" +
		"ARG SOURCE_DATE_EPOCH\n" +
		// The base ends as fugaro; the byproducts are root's to normalize, as
		// they are in the real build.
		"USER root\n" +
		"RUN --mount=type=bind,source=byproducts.sh,target=/tmp/byproducts.sh \\\n" +
		"    set -eu; \\\n" +
		"    mkdir -p /var/log/apt /var/cache/ldconfig /tmp/node-compile-cache/v0; \\\n" +
		"    for f in /var/log/dpkg.log /var/log/alternatives.log /var/log/apt/term.log /var/log/apt/history.log /var/log/apt/eipp.log.xz /var/cache/ldconfig/aux-cache /tmp/node-compile-cache/v0/cache; do date -u +%s%N > \"$f\"; done; \\\n" +
		"    git config --system --remove-section filter.lfs; \\\n" +
		"    git config --system filter.lfs.required true; \\\n" +
		"    git config --system filter.lfs.process 'git-lfs filter-process'; \\\n" +
		"    git config --system filter.lfs.smudge 'git-lfs smudge -- %f'; \\\n" +
		"    git config --system filter.lfs.clean 'git-lfs clean -- %f'; \\\n" +
		"    echo fixture >/tmp/build-clock; \\\n" +
		"    sh /tmp/byproducts.sh\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
	tags := []string{"fugaro-repro-fixture:a", "fugaro-repro-fixture:b"}
	t.Cleanup(func() {
		for _, tag := range tags {
			_ = exec.Command("docker", "image", "rm", "--force", tag).Run()
		}
	})
	var ids []string
	for _, tag := range tags {
		build := exec.Command("docker", "buildx", "build", "--progress", "plain",
			"--platform", testutil.DockerPlatform(t),
			"--build-arg", "SOURCE_DATE_EPOCH="+reproFixtureEpoch,
			"--output", "type=docker,rewrite-timestamp=true",
			"--tag", tag, "--file", filepath.Join(dir, "Dockerfile"), dir)
		build.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("docker buildx build: %v\n%s", err, testutil.Tail(string(out)))
		}
		ids = append(ids, testutil.Docker(t, "image", "inspect", "--format", "{{.Id}}", tag))
	}
	if ids[0] == ids[1] {
		return
	}
	// Which layer, then which files: a bare pair of digests sends the next
	// reader to the whole image, and the answer is always a handful of files.
	// An image ID is its config's digest, which carries the layer digests, so
	// a difference here is a difference in the layers' bytes -- mtimes
	// included, which is what the exporter's rewrite-timestamp is for.
	layers := make([]string, 2)
	for i, tag := range tags {
		layers[i] = testutil.Docker(t, "image", "inspect", "--format", "{{json .RootFS.Layers}}", tag)
	}
	t.Errorf("two builds of the same fixture are different images:\n  %s\n  %s\nlayers:\n  %s\n  %s",
		ids[0], ids[1], layers[0], layers[1])
	a, b := fixtureHashes(t, tags[0]), fixtureHashes(t, tags[1])
	for name, ha := range a {
		if hb, ok := b[name]; !ok {
			t.Errorf("only the first build has %s", name)
		} else if ha != hb {
			t.Errorf("%s differs: %s vs %s", name, ha, hb)
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			t.Errorf("only the second build has %s", name)
		}
	}
}

// fixtureHashes is every file of a fixture image by path, with the hash of its
// contents, so a failure names the byproduct that survived.
func fixtureHashes(t *testing.T, image string) map[string]string {
	t.Helper()
	out := testutil.Docker(t, "run", "--rm", "--user", "0", "--entrypoint", "sh", image, "-c",
		`find / -xdev -type f -exec sha256sum {} + | sort`)
	hashes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "  "); i == 64 {
			hashes[line[i+2:]] = line[:64]
		}
	}
	return hashes
}

// TestBaseImageShipsNoBuildByproducts checks the built base image itself for
// the three byproducts the fixture can only imitate: no Node compile cache,
// no .pyc left in timestamp invalidation mode (flags 0: the mode uv's
// --compile-bytecode writes, whose header is the source's mtime), and an
// /etc/gitconfig whose filter keys are in byproducts.sh's order whichever
// order git-lfs's postinst wrote them in.
func TestBaseImageShipsNoBuildByproducts(t *testing.T) {
	img := testutil.BaseImageOf(t, "base")
	if out := testutil.Docker(t, "run", "--rm", "--user", "0", img, "sh", "-c", "test -e /tmp/node-compile-cache && echo present || echo absent"); out != "absent" {
		t.Errorf("/tmp/node-compile-cache is %s in the base image, and nothing in it reads that cache", out)
	}
	// Every .pyc under the graphify venv and the managed CPython, by the first
	// four bytes of its header after the magic: flags 1 is unchecked-hash, 0
	// is timestamp.
	script := `python=/opt/fugaro/graphify/venv/bin/python
$python - <<'PY'
import pathlib, struct, sys
bad = []
n = 0
for root in ("/opt/fugaro/graphify/venv/lib", ) + tuple(str(p) for p in pathlib.Path("/opt/fugaro/graphify/python").glob("cpython-*/lib")):
    for pyc in pathlib.Path(root).rglob("*.pyc"):
        n += 1
        flags = struct.unpack("<I", pyc.read_bytes()[4:8])[0]
        if not flags & 1:
            bad.append(str(pyc))
print("pyc", n, "timestamp-mode", len(bad))
for p in bad[:5]:
    print(p)
sys.exit(1 if bad else 0)
PY`
	out := testutil.Docker(t, "run", "--rm", "--user", "0", "--entrypoint", "sh", img, "-c", script)
	if !strings.HasPrefix(out, "pyc ") || !strings.Contains(out, "timestamp-mode 0") {
		t.Errorf("graphify's bytecode still carries the build's clock:\n%s", out)
	}
	got := testutil.Docker(t, "run", "--rm", "--user", "0", img, "git", "config", "--system", "--list")
	want := "filter.lfs.clean=git-lfs clean -- %f\nfilter.lfs.smudge=git-lfs smudge -- %f\nfilter.lfs.process=git-lfs filter-process\nfilter.lfs.required=true"
	if got != want {
		t.Errorf("/etc/gitconfig is\n%s\nwant\n%s", got, want)
	}
}
