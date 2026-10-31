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
  web: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestKeyFilesDefaultsNodeLockfiles: a base-kind workflow without cache:
// gets the Node default, keyed by the lockfile package.json's
// packageManager picks (yarn here, although an npm lockfile is also
// present), and every lockfile the default knows is found on its own; the
// key files carry git's blob IDs.
func TestKeyFilesDefaultsNodeLockfiles(t *testing.T) {
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

// TestKeyFilesIncludeYarnAndPnpmInstallFiles: the files a Node install reads
// that a lockfile does not carry are key files, whether the workflow declares
// a cache: or takes the default, and a directory key (.yarn/patches and
// friends) contributes every file under it, since the install reads whatever
// that directory holds. A file no install reads is not one.
func TestKeyFilesIncludeYarnAndPnpmInstallFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json":                        `{"name":"web","packageManager":"yarn@4.9.2"}`,
		"yarn.lock":                           "# yarn lockfile v4\n",
		".yarnrc":                             "registry \"https://registry.example.com\"\n",
		".yarnrc.yml":                         "nodeLinker: node-modules\n",
		".pnpmfile.cjs":                       "module.exports = { hooks: {} }\n",
		".yarn/releases/yarn-4.9.2.cjs":       "#!/usr/bin/env node\n",
		".yarn/plugins/@yarnpkg/plugin-x.cjs": "module.exports = {}\n",
		".yarn/patches/left-pad.patch":        "patch\n",
		"src/app.js":                          "export {}\n",
	}
	testutil.WriteFiles(t, dir, files)
	install := []string{".pnpmfile.cjs", ".yarn/patches/left-pad.patch", ".yarn/plugins/@yarnpkg/plugin-x.cjs",
		".yarn/releases/yarn-4.9.2.cjs", ".yarnrc", ".yarnrc.yml"}
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		want []string
	}{
		{"the default cache", parse(t, webYAML), append(slices.Clone(install), "yarn.lock")},
		// The install files are the install's own, not the cache's: a
		// workflow that declares a cache: of its own still rebuilds when one
		// of them changes, and its cache key replaces only the lockfile half.
		{"an explicit cache", parse(t, strings.Replace(webYAML, "test: sh test.sh }",
			"test: sh test.sh }, cache: [{ key: ['other.lock'], paths: [~/.cache/x] }]", 1)), install},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := KeyFiles(tc.cfg, "web", Dir{Root: dir})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(slices.Sorted(maps.Keys(got)), tc.want) {
				t.Fatalf("KeyFiles = %v, want %v", slices.Sorted(maps.Keys(got)), tc.want)
			}
			if got[".yarn/patches/left-pad.patch"] != gitBlobID(files[".yarn/patches/left-pad.patch"]) {
				t.Errorf("the patch file's blob ID is not the file's: %v", got)
			}
			if _, ok := got["src/app.js"]; ok {
				t.Errorf("a source file is a key file: %v", got)
			}
		})
	}
}

// TestKeyFilesIncludeConfiguredEntries: image.key_files names paths the
// built-in list cannot know — what a patch-package postinstall applies, a
// vendored toolchain — and each one is a key file: a file as itself, a
// directory as every file under it, so a change to any of them changes the
// key exactly as a lockfile change does.
func TestKeyFilesIncludeConfiguredEntries(t *testing.T) {
	const configured = `test: sh test.sh }, image: { key_files: [patches, vendor/toolchain.toml] }`
	cfg := parse(t, strings.Replace(webYAML, "test: sh test.sh }", configured, 1))
	files := map[string]string{
		"package.json":           `{"name":"web"}`,
		"package-lock.json":      "{}",
		"patches/a.patch":        "a\n",
		"patches/nested/b.patch": "b\n",
		"vendor/toolchain.toml":  "[toolchain]\n",
		"docs/readme.md":         "# x\n",
	}
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, files)
	got, err := KeyFiles(cfg, "web", Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"package-lock.json", "patches/a.patch", "patches/nested/b.patch", "vendor/toolchain.toml"}
	if !slices.Equal(slices.Sorted(maps.Keys(got)), want) {
		t.Fatalf("KeyFiles = %v, want %v", slices.Sorted(maps.Keys(got)), want)
	}
	// One byte inside the directory changes the key, and a directory the
	// config does not name does not.
	patched := t.TempDir()
	testutil.WriteFiles(t, patched, files)
	testutil.WriteFiles(t, patched, map[string]string{"patches/a.patch": "a2\n", "docs/readme.md": "# y\n"})
	other, err := KeyFiles(cfg, "web", Dir{Root: patched})
	if err != nil {
		t.Fatal(err)
	}
	if got["patches/a.patch"] == other["patches/a.patch"] {
		t.Error("a change inside a configured directory does not change the key")
	}
	if got["docs/readme.md"] != other["docs/readme.md"] || other["docs/readme.md"] != "" {
		t.Error("a file no key names is in the key")
	}
}

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
    image: { tools: { node: "20" }, apt: [curl], setup: [echo hi] }
    commands: { build: sh build.sh, test: sh test.sh }
`
	const withDockerfile = `version: 1
project: aurora
git: { provider: github }
workflows:
  web:
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
		{"image.tools", withImage, strings.Replace(withImage, `node: "20"`, `node: "22"`, 1), "FROM x\n", true},
		{"image.key_files", withImage, strings.Replace(withImage, "setup: [echo hi]", "setup: [echo hi], key_files: [patches]", 1), "FROM x\n", true},
		{"dockerfile content", withDockerfile, withDockerfile, "FROM y\n", true},
		{"dockerfile path", withDockerfile, strings.Replace(withDockerfile, "web.Dockerfile", "other.Dockerfile", 1), "FROM x\n", true},
		{"dockerfile instead of image", withImage, withDockerfile, "FROM x\n", true},
		{"commands", withImage, strings.Replace(withImage, "sh build.sh", "make", 1), "FROM x\n", false},
		{"secrets", withImage, strings.Replace(withImage, "    commands:", "    secrets: [{ name: npm-token, env: NPM_TOKEN }]\n    commands:", 1), "FROM x\n", false},
		{"rebuild", withImage, strings.Replace(withImage, "    commands:", "    rebuild: { max_age: 3d }\n    commands:", 1), "FROM x\n", false},
		{"agent", withImage, strings.Replace(withImage, "git: { provider: github }\n", "git: { provider: github }\nagent: { model: other }\n", 1), "FROM x\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := hash(t, tc.ref, "FROM x\n")
			if got := hash(t, tc.yaml, tc.dockerfile); (got != ref) != tc.changes {
				t.Errorf("hash changed = %v, want %v", got != ref, tc.changes)
			}
		})
	}
	// base: only config.BaseKind is known today, so a different base is
	// compared on the canonical form directly.
	a, _ := json.Marshal(imageConfig{Base: config.BaseKind})
	b, _ := json.Marshal(imageConfig{Base: ""})
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
	key := Key(Resolved{BaseRef: "web", BaseDigest: baseDigest, Packages: []string{factOf("left-pad", "1.3.0", "")}, Tools: []string{"node@22.11.0"}})
	r := Record{Version: RecordVersion, Repo: "acme/webapp", Workflow: "web", KeyFiles: map[string]string{"yarn.lock": "abc"}, Key: key}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"adopted", "base_branch", "base_digest", "base_ref", "build_id", "built_at", "fugaro_version", "image", "image_config_hash",
		"image_digest", "key", "key_files", "repo", "source_commit", "source_commit_time", "template_salt", "version", "workflow"}
	if got := slices.Sorted(maps.Keys(m)); !slices.Equal(got, want) {
		t.Fatalf("fields = %v\nwant     %v", got, want)
	}
	if m["adopted"] != false {
		t.Error("adopted is not false")
	}
	if m["key"] != key {
		t.Errorf("key = %v, want %s", m["key"], key)
	}
	if RecordKey("acme-webapp-0123456789abcdef", "web") != "builds/acme-webapp-0123456789abcdef/web/image.json" {
		t.Errorf("RecordKey = %s", RecordKey("acme-webapp-0123456789abcdef", "web"))
	}
	// An unkeyed repository's record carries no key at all (§2.5): the field
	// is omitted, so a record an older fugaro wrote and one written for a
	// repository whose key files do not reduce are the same document.
	unkeyed, err := json.Marshal(Record{Version: RecordVersion})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unkeyed), `"key"`) {
		t.Errorf("an unkeyed record carries a key: %s", unkeyed)
	}
	if rec, err := ParseRecord(unkeyed); err != nil || rec.Key != "" {
		t.Errorf("a record written before the field existed: %+v, %v", rec, err)
	}
}

// TestRecordEditionRoundTrips: the edition the render step writes travels in
// the record like any other field, and a record that cannot say — an empty
// edition, which is every record written before the field existed — omits the
// key entirely. The gates read "unknown" from its absence (design
// docs/design/ee-boundary-moves.md §7), and a record an older fugaro wrote must
// not gain a key it never wrote.
func TestRecordEditionRoundTrips(t *testing.T) {
	for _, c := range []struct {
		edition string
		present bool
	}{{"", false}, {"core", true}, {"ee", true}} {
		data, err := json.Marshal(Record{Version: RecordVersion, Edition: c.edition})
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["edition"]; ok != c.present {
			t.Errorf("edition %q: written = %v, want %v (%s)", c.edition, ok, c.present, data)
		}
		rec, err := ParseRecord(data)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Edition != c.edition {
			t.Errorf("Edition = %q, want %q", rec.Edition, c.edition)
		}
	}
	rec, err := ParseRecord([]byte(`{"version": 1, "repo": "acme/webapp", "workflow": "web"}`))
	if err != nil || rec.Edition != "" {
		t.Fatalf("a record written before the field existed: %+v, %v", rec, err)
	}
}

const baseWorkflowYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: make, test: make test } }
`

// A changed root mise file or image.tools makes the image wrong: the hash
// moves. Files mise does not read at the root leave it alone.
func TestImageConfigHashCoversMiseFiles(t *testing.T) {
	hash := func(t *testing.T, yaml string, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		testutil.WriteFiles(t, dir, files)
		h, err := ImageConfigHash(parse(t, yaml), "app", Dir{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	ref := hash(t, baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	for _, tc := range []struct {
		name    string
		yaml    string
		files   map[string]string
		changes bool
	}{
		{"same", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"}, false},
		{"mise.toml", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"22\"\n"}, true},
		{"mise.lock", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "mise.lock": "x"}, true},
		{"conf.d", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", ".config/mise/conf.d/a.toml": "x"}, true},
		// mise itself ignores a dotfile fragment in conf.d (hidden files
		// aren't config), so miseFiles must skip it too: a project dropping
		// a .x.toml there (an editor swap file, say) must not rebuild.
		{"dotfile in conf.d", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", ".config/mise/conf.d/.x.toml": "x"}, false},
		{".tool-versions", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", ".tool-versions": "go 1.27\n"}, true},
		{"subdirectory", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "web/mise.toml": "x"}, false},
		{"mise.local.toml", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "mise.local.toml": "x"}, false},
		{"image.tools", strings.Replace(baseWorkflowYAML, "app: {", "app: { image: { tools: { node: \"24\" } },", 1), map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hash(t, tc.yaml, tc.files); (got != ref) != tc.changes {
				t.Errorf("changed = %v, want %v", got != ref, tc.changes)
			}
		})
	}
}

// TestImageConfigHashLegacyCanonicalForm pins the canonical JSON shape of an
// already-archived legacy record of a kind the 0.7.0 cut removed (go, web-node, java-services):
// no new keys such as tools or mise_files, so an old record read back for
// comparison still decodes the same way. It no longer
// pins a golden hash for a *recomputed* legacy workflow: base: is refused by
// Validate in 0.7.0 (TestLegacyBaseRefusedWithTheMigrationDoc), and
// Workflow.BaseKind() always returns BaseKind now, so there is no longer a
// code path that recomputes an image config hash with Base other than
// BaseKind. A repository's one rebuild on migrating off a legacy base (Task
// 22's fugaro image refresh) is the intended effect of the cut, not a bug.
func TestImageConfigHashLegacyCanonicalForm(t *testing.T) {
	c := imageConfig{Base: "web-node"} // a 0.7.0-removed kind, archived before the cut
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "tools") || strings.Contains(string(data), "mise_files") {
		t.Errorf("legacy canonical form gained keys: %s", data)
	}
}

// TestKeyFilesDefaultsBaseKind: the base kind's default cache is the Node
// package manager's (KeyFiles gives the base kind the Node default cache),
// keyed by whatever lockfile is present.
func TestKeyFilesDefaultsBaseKind(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"app","packageManager":"yarn@1.22.22"}`,
		"yarn.lock":    "# yarn lockfile v1\n",
	}
	testutil.WriteFiles(t, dir, files)
	got, err := KeyFiles(parse(t, baseWorkflowYAML), "app", Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"yarn.lock": gitBlobID(files["yarn.lock"])}
	if !maps.Equal(got, want) {
		t.Fatalf("KeyFiles = %v, want %v", got, want)
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

// TestRecordDigestMismatch: the launch's digest comparison is pure — a
// record that names no digest of its own has nothing to compare, which is
// not a mismatch, and neither is an unknown live digest. Only two digests
// that are both known and different are drift.
func TestRecordDigestMismatch(t *testing.T) {
	const (
		img    = "europe-docker.pkg.dev/p/r/app-web"
		ours   = "sha256:" + "11"
		theirs = "sha256:" + "22"
	)
	for _, tc := range []struct {
		name     string
		rec      *Record
		live     string
		mismatch bool
	}{
		{"matching", &Record{Image: img, ImageDigest: ours}, ours, false},
		{"differing", &Record{Image: img, ImageDigest: ours}, theirs, true},
		{"no digest of its own", &Record{Image: img}, ours, false},
		{"unknown live digest", &Record{Image: img, ImageDigest: ours}, "", false},
		{"render half only", &Record{}, "", false},
		{"no record", nil, ours, false},
	} {
		if got := tc.rec.DigestMismatch(tc.live); got != tc.mismatch {
			t.Errorf("%s: DigestMismatch = %v, want %v", tc.name, got, tc.mismatch)
		}
	}
}

// TestKeyFilesFollowYarnrcYml: the derived image bakes .yarnrc.yml into the
// key-file layer for a Yarn Berry checkout (internal/image's Dockerfile adds
// it), so the rebuild key carries it too — a change to it alone must rebuild,
// or the warm-up runs against a file the image was not built with.
func TestKeyFilesFollowYarnrcYml(t *testing.T) {
	files := map[string]string{
		"package.json": `{"name":"web","packageManager":"yarn@4.9.2"}`,
		"yarn.lock":    "# yarn lockfile v4\n",
		".yarnrc.yml":  "nodeLinker: node-modules\n",
	}
	before, after := t.TempDir(), t.TempDir()
	testutil.WriteFiles(t, before, files)
	files[".yarnrc.yml"] = "nodeLinker: pnp\n"
	testutil.WriteFiles(t, after, files)
	key := func(dir string) map[string]string {
		t.Helper()
		got, err := KeyFiles(parse(t, webYAML), "web", Dir{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	a, b := key(before), key(after)
	if a[".yarnrc.yml"] == "" {
		t.Fatalf(".yarnrc.yml is not a key file: %v", slices.Sorted(maps.Keys(a)))
	}
	if a[".yarnrc.yml"] == b[".yarnrc.yml"] {
		t.Errorf("a change to .yarnrc.yml left the key alone: %q", a[".yarnrc.yml"])
	}
}

// TestPnpmPatchesDropNamesTheCheckoutCannotUse: the patch paths come from
// the repository's own manifests, where validateImage's rules cannot reach
// them. A name that is not one clean repository-relative path is dropped
// rather than keyed: a glob character would be read as a pattern and check
// out files the patch does not name, and a newline would break the generated
// Dockerfile's checkout loop.
func TestPnpmPatchesDropNamesTheCheckoutCannotUse(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"package.json": `{"name":"web","pnpm":{"patchedDependencies":{` +
			`"a":"patches/left-pad.patch",` +
			`"b":"patches/*.patch",` +
			`"c":"../outside.patch",` +
			`"d":"/absolute.patch",` +
			`"e":"patches/broken\n.patch",` +
			`"f":"patches/.",` +
			`"g":".git/config",` +
			`"h":""}}}`,
		"patches/left-pad.patch": "patch\n",
	})
	got, err := PnpmPatches(Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"patches/left-pad.patch"}; !slices.Equal(got, want) {
		t.Errorf("PnpmPatches = %v, want %v", got, want)
	}
	keys, err := KeyFiles(parse(t, webYAML), "web", Dir{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["patches/left-pad.patch"]; !ok {
		t.Errorf("the usable patch is not a key file: %v", slices.Sorted(maps.Keys(keys)))
	}
}

// TestKeyFilesTolerateAManifestThatDoesNotParse: PnpmPatches reads two
// manifests that a repository need not have written for this package at all,
// and the key is computed for every workflow of the repository, Node or not,
// so a manifest that does not parse contributes no patches instead of
// failing the render. (config.DetectNodePM already refuses a package.json
// that does not parse, in defaultCache, for the workflows that ask it for a
// cache key; this is the pnpm-workspace.yaml half, and the patch reader's
// own tolerance.)
func TestKeyFilesTolerateAManifestThatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"pnpm-workspace.yaml": "patchedDependencies: [this is: not yaml\n",
		"go.mod":              "module example.com/app\n",
	})
	if got, err := PnpmPatches(Dir{Root: dir}); err != nil || len(got) != 0 {
		t.Errorf("PnpmPatches = %v, %v, want no patches and no error", got, err)
	}
	const goYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  go:
    commands: { build: go build ./..., test: go test ./... }
`
	if _, err := KeyFiles(parse(t, goYAML), "go", Dir{Root: dir}); err != nil {
		t.Errorf("a workflow with no dependencies at all was refused by a pnpm manifest it does not use: %v", err)
	}
	brokendir := t.TempDir()
	testutil.WriteFiles(t, brokendir, map[string]string{"package.json": "{not json"})
	if got, err := PnpmPatches(Dir{Root: brokendir}); err != nil || len(got) != 0 {
		t.Errorf("PnpmPatches over a package.json that does not parse = %v, %v, want no patches and no error", got, err)
	}
}
