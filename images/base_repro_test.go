package images_test

import (
	"os"
	"strings"
	"testing"
)

// The reproducibility of the base build (issue #1745) is a property of two
// files that only the build can exercise: images/base/byproducts.sh, which
// normalizes the byproducts of a package install, and the flags
// images/build-base.sh passes to docker. The docker-tagged tests build the
// image and compare two builds of it (images/base_repro_docker_test.go); these
// are the pins that hold when no docker is available, and the two halves are
// deliberately the same strings in both files, so a change to one that forgets
// the other is a failure rather than a nightly that quietly republishes.

// runBlocks returns every RUN instruction of a Dockerfile as one string, its
// continuation lines included: what has to be checked is which commands share
// a layer, and a command list split across lines is one instruction.
func runBlocks(t *testing.T, df string) []string {
	t.Helper()
	var blocks []string
	lines := strings.Split(df, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "RUN") {
			continue
		}
		block := lines[i]
		for strings.HasSuffix(block, "\\") && i+1 < len(lines) {
			i++
			block += "\n" + lines[i]
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// runBlockWith returns the one RUN instruction holding marker, and fails when
// no single instruction holds it.
func runBlockWith(t *testing.T, df, marker string) string {
	t.Helper()
	var found []string
	for _, b := range runBlocks(t, df) {
		if strings.Contains(b, marker) {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d RUN instructions hold %q, want one", len(found), marker)
	}
	return found[0]
}

// TestBaseByproductsAreNormalizedInTheirOwnLayer: the normalizer has to run in
// the same RUN that installed the packages, not a later one. A later RUN that
// rewrites a file leaves the layer that first wrote it with the bytes of that
// build's clock, so the two images still differ by that layer's digest, which
// is what the catalog check reads. The base is therefore not just "normalized
// somewhere": every RUN that leaves a byproduct runs the script, and the
// script is bind-mounted (like fetch.sh), never copied in.
func TestBaseByproductsAreNormalizedInTheirOwnLayer(t *testing.T) {
	df := baseDockerfile(t)
	for _, m := range []struct{ marker, what string }{
		{"apt-get install -y --no-install-recommends", "the apt layer (useradd, git-lfs's postinst, dpkg's logs)"},
		{"npm ci --ignore-scripts", "the harness layer (npm writes /tmp/node-compile-cache)"},
	} {
		block := runBlockWith(t, df, m.marker)
		if !strings.Contains(block, "--mount=type=bind,source=images/base/byproducts.sh,target=/tmp/byproducts.sh") {
			t.Errorf("%s does not bind-mount images/base/byproducts.sh", m.what)
		}
		if !strings.Contains(block, "sh /tmp/byproducts.sh") {
			t.Errorf("%s does not run byproducts.sh, so what it wrote from the clock stays in that layer", m.what)
		}
		// The script has to be the last thing that RUN does: anything after it
		// that touches /etc, /var or /tmp would be a byproduct again.
		const run = "sh /tmp/byproducts.sh"
		if tail := strings.TrimSpace(block[strings.LastIndex(block, run)+len(run):]); tail != "" {
			t.Errorf("%s keeps writing after byproducts.sh, so that write is a byproduct of its own:\n%s", m.what, tail)
		}
	}
	// No default on the ARG: a default of 0 would date the build 1970, and
	// `chage -d 1970-01-01` writes the last-change day PAM reads as a required
	// password change. The date is the caller's to pass or the build's to
	// refuse, never this line's to invent.
	if !strings.Contains(df, "\nARG SOURCE_DATE_EPOCH\n") {
		t.Error("the Dockerfile does not declare SOURCE_DATE_EPOCH, so the date byproducts.sh reads is not a build argument BuildKit's frontend also uses")
	}
	if strings.Contains(df, "ARG SOURCE_DATE_EPOCH=") {
		t.Error("the Dockerfile defaults SOURCE_DATE_EPOCH, and a default of 0 dates the image 1970, whose last-change day PAM reads as a required password change")
	}
}

// TestByproductsScriptNormalizesTheBuildClock pins what the script does, one
// line per source that the comparison of the two published 0.18.0 images
// named: /etc/gitconfig (git-lfs's four filter keys, whose order git decides),
// /etc/shadow's last-change day and the /etc/shadow- copy the account tools
// leave beside it, /var/log's files (dpkg.log, apt's history and term.log,
// alternatives.log), ldconfig's aux-cache, and the Node compile cache.
//
// /var/cache is narrowed on purpose: the same comparison found aux-cache to be
// the only file under it that two builds disagree on, and debconf's database
// there is state a derived build's install reads, so removing it would trade a
// nondeterminism that does not exist for a behaviour change that does.
func TestByproductsScriptNormalizesTheBuildClock(t *testing.T) {
	b, err := os.ReadFile("base/byproducts.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`epoch=${SOURCE_DATE_EPOCH:-}`,
		`[ -n "$epoch" ] && [ "$epoch" != 0 ] || {`,
		`echo "byproducts: SOURCE_DATE_EPOCH is ${epoch:-empty}, and 0 dates the image 1970,`,
		`chage -d "$(date -u -d "@$epoch" +%Y-%m-%d)" fugaro`,
		"cp -f /etc/shadow /etc/shadow-",
		"rm -f /var/cache/ldconfig/aux-cache",
		"find /var/log -mindepth 1 ! -type d -delete",
		"rm -rf /tmp/node-compile-cache",
		`for key in clean smudge process required; do`,
		`echo "byproducts: /etc/gitconfig has no filter.lfs.$key" >&2; exit 1`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("byproducts.sh lacks %q", want)
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// The comment above says why /var/cache is narrowed; this is the
		// command that has to stay narrowed, since debconf's database there is
		// state a derived build's install reads and no comparison showed it to
		// differ between two builds.
		if strings.Contains(line, "/var/cache") && !strings.Contains(line, "/var/cache/ldconfig/aux-cache") {
			t.Errorf("byproducts.sh touches more of /var/cache than ldconfig's aux-cache: %s", line)
		}
	}
	// The order is the point of the gitconfig rewrite: the four keys are
	// written back in the order the loop names them, whatever order git
	// happened to write them in. A section that only re-printed what it read
	// would be a no-op.
	if i, j := strings.Index(s, "clean smudge process required"), strings.Index(s, "printf '\\tclean = %s\\n\\tsmudge = %s\\n\\tprocess = %s\\n\\trequired = %s\\n'"); i == -1 || j == -1 || i > j {
		t.Error("byproducts.sh does not re-write /etc/gitconfig's filter keys from the values it read, in a fixed order")
	}
}

// TestBaseGraphifyBytecodeIsHashInvalidated: `uv pip install
// --compile-bytecode` writes timestamp-mode .pyc, whose header carries the
// source's extraction mtime, so two builds of the same tree disagree on every
// one of graphify's bytecode files. The recompile has to be in the same RUN as
// the install (the files it rewrites must never be in a layer of their own)
// and it has to cover both roots: the venv graphify runs from and the managed
// interpreter's own lib.
func TestBaseGraphifyBytecodeIsHashInvalidated(t *testing.T) {
	df := baseDockerfile(t)
	block := runBlockWith(t, df, "uv pip install --python /opt/fugaro/graphify/venv/bin/python")
	if !strings.Contains(block, "-m compileall -q -f --invalidation-mode unchecked-hash") {
		t.Error("the graphify layer does not re-compile its bytecode with unchecked-hash invalidation")
	}
	if !strings.Contains(block, "/opt/fugaro/graphify/python/cpython-*/lib") || !strings.Contains(block, "/opt/fugaro/graphify/venv/lib") {
		t.Error("the recompile does not cover both the venv and the managed interpreter's lib")
	}
	if !strings.Contains(block, "--compile-bytecode") {
		t.Error("the install no longer asks uv for bytecode at all, so the recompile is the only reason the venv has any")
	}
}

// TestBuildBasePinsTheDateAndTheExporter: the two flags the whole scheme rests
// on are in the one script CI and the docker tests build through, and the date
// they carry is the tree's own (the last commit), never the wall clock: a
// build that stamps the moment it ran would make every nightly a new image.
func TestBuildBasePinsTheDateAndTheExporter(t *testing.T) {
	b, err := os.ReadFile("build-base.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`epoch=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct 2>/dev/null || echo 0)}`,
		`--build-arg "SOURCE_DATE_EPOCH=$epoch"`,
		`if [ -z "$epoch" ] || [ "$epoch" = 0 ]; then`,
		`echo "build-base.sh: SOURCE_DATE_EPOCH is ${epoch:-empty} and this checkout has no commit date to take:`,
		`--output "type=oci,name=$first,dest=$tarball,rewrite-timestamp=true"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("build-base.sh lacks %q", want)
		}
	}
	if strings.Contains(s, "date +%s") || strings.Contains(s, "$(date ") {
		t.Error("build-base.sh reads the wall clock for the date it stamps the build with")
	}
	// The docker test builds its fixture with the same two flags, because a
	// fixture that passed different ones would pass while the real build
	// regressed. Both files are read here rather than trusted.
	docker, err := os.ReadFile("base_repro_docker_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--build-arg", "SOURCE_DATE_EPOCH=", "type=oci", "rewrite-timestamp=true"} {
		if !strings.Contains(string(docker), want) {
			t.Errorf("the fixture build in base_repro_docker_test.go lacks %q, so it does not build the way build-base.sh does", want)
		}
	}
}
