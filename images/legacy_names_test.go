package images_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// After the 0.7.0 cut, no file presents a removed base as current. History
// (plans, old release notes, the design docs' history) and the migration
// guide may name them.
func TestNoLegacyBaseOutsideHistory(t *testing.T) {
	legacy := regexp.MustCompile(`web-node|java-services|fugaro-go\b|base: go\b`)
	// A line that says the bases were removed (a refusal, a test of one) is
	// about the cut, not a current use; "Verified live DATE (...)" is this
	// checklist's own convention for a dated record of what a past live
	// check actually tested, which must name the image it really used, not
	// the one in use today.
	about := func(line string) bool {
		return strings.Contains(line, "0.7.0") || strings.Contains(line, "MigrationDoc") || strings.Contains(line, "Verified live")
	}
	allowed := []string{
		"docs/plans/", "docs/releases/", "docs/design/", "docs/base-image-migration.md", "docs/base-image.md",
		"images/legacy_names_test.go", "internal/config/legacy_test.go", "internal/localcfg/localcfg.go", "internal/config/dockerfile.go",
		"internal/localcfg/localcfg_test.go", "internal/cli/sharedcfg_validate_test.go", "internal/infra/checkjob_test.go",
		"testdata/config/invalid/legacy-",
		".git/", ".worktrees/", "internal/cli/docs_base_test.go",

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
	root := ".."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		for _, a := range allowed {
			if strings.HasPrefix(rel, a) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() || strings.HasSuffix(rel, ".png") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if legacy.MatchString(line) && !about(line) {
				t.Errorf("%s:%d names a removed base: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
