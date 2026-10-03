package imagecheck

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func parse(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("fugaro.yaml: %v", problems)
	}
	return cfg
}

// gitBlobID is git's SHA-1 object ID of a blob holding data.
func gitBlobID(data string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(data), data))))
}

const webYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  web: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }
`

// TestKeyFilesDefaultsWebNode: a web-node workflow without cache: gets the
// per-base default, keyed by the lockfile package.json's packageManager
// picks (yarn here, although an npm lockfile is also present), and the
// key files carry git's blob IDs.
func TestKeyFilesDefaultsWebNode(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json":      `{"name":"web","packageManager":"yarn@1.22.22"}`,
		"yarn.lock":         "# yarn lockfile v1\n",
		"package-lock.json": `{"lockfileVersion":3}`,
		"src/index.ts":      "export {}\n",
	}
	testutil.WriteFiles(t, dir, files)
	got, err := KeyFiles(parse(t, webYAML), "web", Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"yarn.lock": gitBlobID(files["yarn.lock"])}
	if !maps.Equal(got, want) {
		t.Fatalf("KeyFiles = %v, want %v", got, want)
	}

	// Without packageManager, detection order picks yarn.lock too; every
	// lockfile the default knows is found on its own.
	for _, lock := range []string{"pnpm-lock.yaml", "yarn.lock", "package-lock.json", "npm-shrinkwrap.json"} {
		d := t.TempDir()
		testutil.WriteFiles(t, d, map[string]string{"package.json": `{"name":"web"}`, lock: "x"})
		got, err := KeyFiles(parse(t, webYAML), "web", Dir{Root: d})
		if err != nil || !slices.Equal(slices.Collect(maps.Keys(got)), []string{lock}) {
			t.Errorf("%s alone: KeyFiles = %v, %v", lock, got, err)
		}
	}
}

// TestKeyFilesExplicitCache: an explicit cache: replaces the default, and
// its globs match files in subdirectories.
func TestKeyFilesExplicitCache(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"package.json":          `{"name":"web"}`,
		"package-lock.json":     "{}",
		"packages/a/deps.lock":  "a",
		"packages/b/deps.lock":  "b",
		"packages/b/other.json": "{}",
	})
	cfg := parse(t, strings.Replace(webYAML, "test: sh test.sh }", "test: sh test.sh }, cache: [{ key: ['packages/**/deps.lock'], paths: [~/.cache/x] }]", 1))
	got, err := KeyFiles(cfg, "web", Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"packages/a/deps.lock", "packages/b/deps.lock"}; !slices.Equal(slices.Sorted(maps.Keys(got)), want) || got["packages/a/deps.lock"] != gitBlobID("a") {
		t.Fatalf("KeyFiles = %v", got)
	}
}

// TestDirBlobIDMatchesGit: the directory tree's blob IDs are the ones git
// records, so a build's record compares with the check's git tree.
func TestDirBlobIDMatchesGit(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", dir)
	testutil.WriteFiles(t, dir, map[string]string{"yarn.lock": "lock\ncontent\n"})
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "x")
	got, err := Dir{Root: dir}.BlobID("yarn.lock")
	if want := testutil.Git(t, dir, "rev-parse", "HEAD:yarn.lock"); err != nil || got != want {
		t.Fatalf("BlobID = %q, %v; git says %q", got, err, want)
	}
	if _, err := (Dir{Root: dir}).BlobID("missing"); err == nil {
		t.Error("BlobID of a missing file succeeded")
	}
	if _, err := (Dir{Root: dir}).BlobID("../outside"); err == nil {
		t.Error("BlobID outside the tree succeeded")
	}
}

// TestImageConfigHashChanges: what makes the image wrong (its base,
// image: settings, the repository Dockerfile's name or content) changes
// the hash; the commands, cache and secrets don't.
func TestImageConfigHashChanges(t *testing.T) {
	// image: and dockerfile: are exclusive, so each is varied from its own
	// reference.
	const withImage = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    base: web-node
    image: { node: "20", apt: [curl], setup: [echo hi] }
    commands: { build: sh build.sh, test: sh test.sh }
`
	const withDockerfile = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
    base: web-node
    dockerfile: .fugaro/web.Dockerfile
    commands: { build: sh build.sh, test: sh test.sh }
`
	tree := func(t *testing.T, dockerfile string) Dir {
		dir := t.TempDir()
		testutil.WriteFiles(t, dir, map[string]string{".fugaro/web.Dockerfile": dockerfile, ".fugaro/other.Dockerfile": dockerfile})
		return Dir{Root: dir}
	}
	hash := func(t *testing.T, yaml, dockerfile string) string {
		t.Helper()
		h, err := ImageConfigHash(parse(t, yaml), "web", tree(t, dockerfile))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	for _, ref := range []string{withImage, withDockerfile} {
		if h := hash(t, ref, "FROM x\n"); len(h) != 64 {
			t.Fatalf("hash = %q, want 64 hex", h)
		}
	}
	for _, tc := range []struct {
		name, ref, yaml, dockerfile string
		changes                     bool
	}{
		{"same image", withImage, withImage, "FROM x\n", false},
		{"same dockerfile", withDockerfile, withDockerfile, "FROM x\n", false},
		{"image.apt", withImage, strings.Replace(withImage, "apt: [curl]", "apt: [curl, jq]", 1), "FROM x\n", true},
		{"image.setup", withImage, strings.Replace(withImage, "echo hi", "echo bye", 1), "FROM x\n", true},
		{"image.node", withImage, strings.Replace(withImage, `node: "20"`, `node: "22"`, 1), "FROM x\n", true},
		{"dockerfile content", withDockerfile, withDockerfile, "FROM y\n", true},
		{"dockerfile path", withDockerfile, strings.Replace(withDockerfile, "web.Dockerfile", "other.Dockerfile", 1), "FROM x\n", true},
		{"dockerfile instead of image", withImage, withDockerfile, "FROM x\n", true},
		{"commands", withImage, strings.Replace(withImage, "sh build.sh", "make", 1), "FROM x\n", false},
		{"secrets", withImage, strings.Replace(withImage, "    base: web-node\n", "    base: web-node\n    secrets: [{ name: npm-token, env: NPM_TOKEN }]\n", 1), "FROM x\n", false},
		{"rebuild", withImage, strings.Replace(withImage, "    base: web-node\n", "    base: web-node\n    rebuild: { max_age: 3d }\n", 1), "FROM x\n", false},
		{"agent", withImage, strings.Replace(withImage, "git: { provider: github }\n", "git: { provider: github }\nagent: { model: other }\n", 1), "FROM x\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := hash(t, tc.ref, "FROM x\n")
			if got := hash(t, tc.yaml, tc.dockerfile); (got != ref) != tc.changes {
				t.Errorf("hash changed = %v, want %v", got != ref, tc.changes)
			}
		})
	}
	// base: only web-node is known today, so a different base is compared
	// on the canonical form directly.
	a, _ := json.Marshal(imageConfig{Base: "web-node"})
	b, _ := json.Marshal(imageConfig{Base: "java-services"})
	if hashOf(a) == hashOf(b) {
		t.Error("base does not change the hash")
	}
	if _, err := ImageConfigHash(parse(t, strings.Replace(withDockerfile, "web.Dockerfile", "missing.Dockerfile", 1)), "web", tree(t, "x")); err == nil {
		t.Error("a missing repository Dockerfile gave a hash")
	}
}

// TestRecordJSON pins the record's field names, which the check and
// fugaro image status read.
func TestRecordJSON(t *testing.T) {
	r := Record{Version: RecordVersion, Repo: "acme/webapp", Workflow: "web", KeyFiles: map[string]string{"yarn.lock": "abc"}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"adopted", "base_branch", "base_digest", "base_ref", "build_id", "built_at", "fugaro_version", "image", "image_config_hash",
		"image_digest", "key_files", "repo", "source_commit", "source_commit_time", "template_salt", "version", "workflow"}
	if got := slices.Sorted(maps.Keys(m)); !slices.Equal(got, want) {
		t.Fatalf("fields = %v\nwant     %v", got, want)
	}
	if m["adopted"] != false {
		t.Error("adopted is not false")
	}
	if RecordKey("acme-webapp-0123456789abcdef", "web") != "builds/acme-webapp-0123456789abcdef/web/image.json" {
		t.Errorf("RecordKey = %s", RecordKey("acme-webapp-0123456789abcdef", "web"))
	}
}

// TestRecordNewerThan: newer compares the source commits' committer
// times, then, for one commit, the build times.
func TestRecordNewerThan(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	rec := func(commit string, commitAt, builtAt time.Time) *Record {
		return &Record{SourceCommit: commit, SourceCommitTime: commitAt, BuiltAt: builtAt}
	}
	ours := rec("aaa", t0, t0.Add(time.Hour))
	for _, tc := range []struct {
		name  string
		cur   *Record
		newer bool
	}{
		{"later commit", rec("bbb", t0.Add(time.Minute), t0), true},
		{"earlier commit built later", rec("ccc", t0.Add(-time.Minute), t0.Add(2*time.Hour)), false},
		{"same commit built later", rec("aaa", t0, t0.Add(2*time.Hour)), true},
		{"same commit built earlier", rec("aaa", t0, t0), false},
		{"same commit same build time", rec("aaa", t0, t0.Add(time.Hour)), false},
		{"other commit same time", rec("ddd", t0, t0.Add(2*time.Hour)), false},
	} {
		if got := tc.cur.NewerThan(ours); got != tc.newer {
			t.Errorf("%s: NewerThan = %v, want %v", tc.name, got, tc.newer)
		}
	}
}
