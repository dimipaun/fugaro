package images_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var legacy = regexp.MustCompile(`web-node|java-services|fugaro-go\b|base: go\b`)

// about reports whether a line says the bases were removed (a refusal, a test
// of one) — about the cut, not a current use. "Verified live DATE (...)" is
// this checklist's own convention for a dated record of what a past live check
// actually tested, which must name the image it really used, not the one in
// use today.
func about(line string) bool {
	return strings.Contains(line, "0.7.0") || strings.Contains(line, "MigrationDoc") || strings.Contains(line, "Verified live")
}

// untrackedDirs hold other branches' copies of this repository: git worktrees
// an agent run or a developer left behind, and tool state alongside them.
// Nothing here is tracked, CI never has them, and a finding inside one belongs
// to whatever branch that tree is on — see repoFiles.
var untrackedDirs = []string{".claude/worktrees", ".worktrees", ".superpowers"}

// repoFiles lists the files this scan reads, as slash paths relative to root:
// what git tracks when root is a git checkout, otherwise every file under root
// outside untrackedDirs. Walking the whole checkout unconditionally reported
// false failures from the untracked trees above on a developer machine (CI has
// none of them) — findings a developer could not reproduce in CI, and half of
// which are not in this checkout at all.
func repoFiles(root string) ([]string, error) {
	if files, err := gitFiles(root); err == nil && len(files) > 0 {
		return files, nil
	}
	return walkFiles(root)
}

// gitFiles is git's own listing of the checkout's content: `git ls-files`
// fails (so the caller falls back to the walk) when root is not a git checkout
// or git is not installed.
func gitFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	// -z splits paths on NUL, so a path with a newline or a quote in it
	// survives unquoted.
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, filepath.ToSlash(p))
		}
	}
	return files, nil
}

// walkFiles is the fallback: a walk of root that never enters untrackedDirs
// (or .git, whose objects are not this repository's content either).
func walkFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, filepath.ToSlash(root)+"/"))
		if d.IsDir() {
			if skippedPath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !skippedPath(rel) {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// skippedPath reports whether rel is under a directory this scan never reads.
// root itself (whose relative path is unchanged by the TrimPrefix above) is a
// directory the walk visits, not a path to skip.
func skippedPath(rel string) bool {
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	for _, dir := range untrackedDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// legacyOffenders returns one message per line of the named files under root
// that names a removed base and is not about the cut. A file git lists but
// that is not on disk (deleted, not staged) is not a file this checkout has.
func legacyOffenders(root string, files []string) ([]string, error) {
	var hits []string
	for _, rel := range files {
		if allowedPath(rel) || strings.HasSuffix(rel, ".png") {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			// A submodule's gitlink: ls-files names the directory, not a file.
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if legacy.MatchString(line) && !about(line) {
				hits = append(hits, fmt.Sprintf("%s:%d names a removed base: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
	}
	return hits, nil
}

// allowedPath reports whether rel is one of the files (or under one of the
// directories) below.
func allowedPath(rel string) bool {
	for _, a := range allowed {
		if strings.HasPrefix(rel, a) {
			return true
		}
	}
	return false
}

var allowed = []string{
	"docs/plans/", "docs/releases/", "docs/design/", "docs/base-image-migration.md", "docs/base-image.md",
	"images/legacy_names_test.go", "internal/config/legacy_test.go", "internal/localcfg/localcfg.go", "internal/config/dockerfile.go",
	"internal/localcfg/localcfg_test.go", "internal/cli/sharedcfg_validate_test.go", "internal/infra/checkjob_test.go",
	"testdata/config/invalid/legacy-",
	// .git and the untracked trees (.worktrees, .superpowers,
	// .claude/worktrees) used to be listed here; repoFiles now keeps them
	// out of the scan entirely, so an entry would only hide a regression
	// there.
	"internal/cli/docs_base_test.go",

	// Below: real hits of TestNoLegacyBaseOutsideHistory, triaged when
	// this test was added (base image task 20) and re-triaged after a
	// review found it too broad. None of them presents a removed base
	// as a thing to use today; each comment says why. A fixed line
	// moves to a line-level "0.7.0"/"MigrationDoc" marker (see about(),
	// above) instead of a file-wide allow whenever that keeps this test
	// meaningful for the rest of the file; only a file genuinely out of
	// this task's scope, or wholly historical/staged content, gets a
	// path entry here.

	// Fugaro cannot push a change under .github/workflows/ from an agent
	// run (GitHub rejects it): the real file still runs the pre-cut jobs
	// (it still builds, smoke-tests and publishes fugaro-web-node,
	// fugaro-go and fugaro-java-services) until a human copies in
	// docs/pending-workflows/images.yml, task 20's own pending
	// replacement, which has no legacy job. See that file's README.
	".github/workflows/images.yml",
	// Stages workflow diffs (and the rationale for them) for a human to
	// copy in by hand; narrating a diff that removes the legacy jobs
	// necessarily names them, the same way docs/releases/ and
	// docs/design/ may.
	"docs/pending-workflows/",
	// Describes the still-active images.yml pipeline (above) until the
	// pending copy lands: accurate to current behavior (the real,
	// unedited workflow really does still build and publish the three
	// legacy images), not a stale reference. docs/gcp-setup.md,
	// docs/gcp-live-checklist.md and docs/project-layer.md, which used
	// to need the same allowance, are fixed and removed from this list
	// (a review found the blanket allow was hiding their staleness).
	"docs/release.md",
	// Explains that this repository's own fugaro.yaml deliberately keeps
	// base: go until Task 22 (after fugaro-base is released and `fugaro
	// image refresh` has run here); flipping it is that task's job, not
	// this one's. fugaro.yaml itself and TestRepositoryFugaroYAML's own
	// assertion message carry an inline "0.7.0" marker instead (see
	// about()), so only this doc needs the file-wide allow.
	"docs/dogfooding.md",
	// A fixture for the legacy per-profile base refusal, named the same
	// way as the already-allowed testdata/config/invalid/legacy- ones.
	"testdata/project-layer/invalid/legacy-",
	// TestCheckJobBackfillsFromAPreCutLegacySpec deliberately simulates a
	// check job whose installed spec predates the cut (the comment above
	// it in that file explains why: the backfill is load-bearing for
	// that migration window, not dead code to delete).
	"internal/cli/imagecheck_test.go",
	// java-services here is only ever used as an arbitrary second,
	// non-"base" kind to exercise imagesStage.kinds()'s set/dedup logic
	// below the CLI flag-validation layer (--base itself already refuses
	// a legacy kind elsewhere, internal/cli/init_test.go).
	"internal/cli/init_images_test.go",
	// Comments explaining a corpus rename and using "base: go"/"base:
	// java-17" as generic, syntactically-valid filler in refusal tests
	// of unrelated rules (anchors, merge keys, credentials, profile
	// names), not of the base-kind cut itself.
	"internal/config/image_test.go", "internal/config/layer_test.go",
	// An arbitrary sample Artifact Registry path for a digest-fetch
	// test, unrelated to base-kind validity.
	"internal/imagecheck/registry_test.go",
	// Deliberately sets BaseImages to a non-"base" kind to prove the
	// Inputs validation path refuses a workflow whose kind has no entry.
	"internal/infra/spec_test.go",
	// Real terraform plan goldens (scripts/gen-golden-plans.sh); known
	// leftover from task 19's review, unregenerated because doing so
	// needs a cached Google provider (~/.cache/fugaro/terraform-plugins)
	// and no sandbox this series has run in has had one or network
	// access to fetch it. Cover.NotCovered reads base_images by key, not
	// by this string, so the tests that use these goldens pass either
	// way; this is wording-only. Exactly these two named files: the
	// other five goldens in this directory never named a legacy kind,
	// so they are not allowed here and would fail this test if one
	// crept in.
	"internal/infra/tf/testdata/golden/repo.plan.json",
	"internal/infra/tf/testdata/golden/repo-adopt.plan.json",
	// fugaro-go/fugaro-history are arbitrary example image names for the
	// mirror's generic reference-parsing and size-limit logic, unrelated
	// to base kinds.
	"internal/mirror/",
	// An arbitrary sample base-image string in a cache-key warning test.
	"internal/runner/lockcache_test.go",
	// The two per-language fixture directories that used to be named
	// web-node/java-services are now node/server (a fix-round review
	// pointed out the rename was cheap, and it drops setup_fixtures_test.go
	// and the rest of testdata/repos/ off this list entirely). The one
	// fixture that still legitimately names a removed base is
	// existing-fugaro-yaml's repo/fugaro.yaml: a hand-written 0.6 file
	// (`base: web-node`) the skill's expected/ output corrects, kept as
	// repository data to fix, not usage to normalize away.
	"plugin/testdata/repos/existing-fugaro-yaml/repo/fugaro.yaml",
	// TestCIWorkflowSkipsImagesTheTagTreeDoesNotHold simulates OLD,
	// pre-cut release trees to prove the version job's "kinds" step
	// still detects their legacy directories (that step is deliberately
	// unchanged by this task: a backport build off an old release line
	// needs it). Naming the legacy kinds is the point of that test, not
	// a stale leftover.
	"images/cloudbuild_test.go",
}

// After the 0.7.0 cut, no file this checkout holds presents a removed base as
// current. History (plans, old release notes, the design docs' history) and
// the migration guide may name them, and the scan is scoped to what git tracks
// (repoFiles) so a developer's untracked worktrees cannot report here.
func TestNoLegacyBaseOutsideHistory(t *testing.T) {
	root := ".."
	files, err := repoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	// The scan is only as good as its listing: a failed or empty one would
	// make this test pass by reading nothing.
	if len(files) < 100 {
		t.Fatalf("listed only %d files: the scan would not see the checkout", len(files))
	}
	hits, err := legacyOffenders(root, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		t.Error(hit)
	}
}

// writeFile writes a file (and its parents) under root.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyScanFindsATrackedFile: the mutation this test exists to catch.
// Add `base: go` to a tracked file and TestNoLegacyBaseOutsideHistory fails;
// the git-based listing above must keep that true, not turn the scan into a
// walk of an empty (or filtered-to-nothing) file list.
func TestLegacyScanFindsATrackedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/thing.go", "package thing\n\n// base: go\n")
	writeFile(t, root, "pkg/clean.go", "package thing\n\n// base: node\n")
	files, err := repoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want both written files", files)
	}
	hits, err := legacyOffenders(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "pkg/thing.go:3") {
		t.Fatalf("hits = %v, want the one line in pkg/thing.go", hits)
	}
}

// TestRepoFilesIgnoresUntrackedDirs: `.worktrees`, `.claude/worktrees` and
// `.superpowers` hold other branches' trees on a developer machine (and
// agent runs). CI has none of them, so a scan that walks them reports
// failures a developer cannot reproduce in CI and half of which belong to
// work that is not in this checkout at all.
func TestLegacyScanIgnoresUntrackedDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/clean.go", "package thing\n")
	for _, dir := range untrackedDirs {
		writeFile(t, root, filepath.Join(dir, "stale.go"), "package stale\n\n// base: go\n")
	}
	// The fallback path (git unavailable, or root not a checkout): the walk.
	files, err := walkFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "pkg/clean.go" {
		t.Fatalf("files = %v, want only pkg/clean.go", files)
	}
	hits, err := legacyOffenders(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v, want none: the stale trees must not be scanned", hits)
	}
}

// TestRepoFilesListsWhatGitTracks: when root is a git checkout the listing is
// git's (git ls-files), so an untracked file is out of scope even outside the
// directories above — that is what keeps the scan the same in CI and on a
// developer machine.
func TestLegacyScanListsWhatGitTracks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	writeFile(t, root, "pkg/tracked.go", "package thing\n")
	writeFile(t, root, "pkg/untracked.go", "package thing\n\n// base: go\n")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "pkg/tracked.go"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	files, err := repoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "pkg/tracked.go" {
		t.Fatalf("files = %v, want only the tracked pkg/tracked.go", files)
	}
}
