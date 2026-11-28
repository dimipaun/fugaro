package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// doctor_config_version_test.go is CV2's tests (D3423): the deprecation notice
// both surfaces print for a fugaro.yaml written at a version inside its window.
//
// The table is CV1's own (config.SupportedVersions), and today it holds one
// version, which is current: no window is open, so the release prints nothing
// and no test could see a notice at all. deprecateV1 stands in the table a
// release that opens one has — v1 in its window, v2 the version this release
// reads — and both halves are checked against it: what the release does today
// (silence) and what it does the day a window opens.

// v1Window is the table the release that opens v1's deprecation window holds:
// v1 deprecated in v0.42.0, v2 the version this release reads and writes.
var v1Window = []config.Version{
	{Number: 1, DeprecatedIn: "v0.42.0"},
	{Number: 2},
}

// deprecateVersions stands a table in for the release's own and puts it back
// when the test ends: the notice reads SupportedVersions, so a test that wants
// a window has to write one.
func deprecateVersions(t *testing.T, vs ...config.Version) {
	t.Helper()
	old := config.SupportedVersions
	t.Cleanup(func() { config.SupportedVersions = old })
	config.SupportedVersions = vs
}

// TestDoctorNamesDeprecatedVersion: a repository whose default branch is
// written at a version inside its deprecation window earns one row from doctor
// naming the file and the version, with the migration as its fix — and the same
// release prints no row at all today, whose one version is current (CV1's table
// is inert, and the notice with it).
func TestDoctorNamesDeprecatedVersion(t *testing.T) {
	_ = newDoctorRig(t)
	root := repoCheckout(t, githubOrigin, refreshYAML)
	t.Chdir(root)

	// Today's table: one version, current. The window is what the notice reads,
	// so a release whose only version is current says nothing about any file —
	// this is the half that makes the check inert until a window opens.
	if c, ok := repoNoticeDoctorChecks(t, root)[repoYAMLVersionCheckID]; ok {
		t.Errorf("doctor reports %s = %+v on a release whose only version is current: %s writes version %d, and nothing is deprecated", repoYAMLVersionCheckID, c, repoNoticeYAMLFile, config.CurrentVersion)
	}

	deprecateVersions(t, v1Window...)
	checks := repoNoticeDoctorChecks(t, root)
	c, ok := checks[repoYAMLVersionCheckID]
	if !ok {
		t.Fatalf("doctor reports no %s on a checkout whose default branch is written at v1, inside its window: %+v", repoYAMLVersionCheckID, checks)
	}
	if c.OK {
		t.Fatalf("%s = %+v, want a finding: the window is open on the file's own version", repoYAMLVersionCheckID, c)
	}
	if c.Severity != "warning" || c.Class != doctorClassActionable {
		t.Errorf("%s is %q/%q, want a warning in the actionable class: the file reads today and the release after the window refuses it", repoYAMLVersionCheckID, c.Severity, c.Class)
	}
	// The one sentence the update prints for the same repository, verbatim:
	// U6a's rule, read for the notice beside its two.
	want := "acme/app: " + configVersionNotice(v1Window[0])
	if !strings.Contains(c.Problem, want) {
		t.Errorf("%s does not carry the line `fugaro update` prints, %q:\n%s", repoYAMLVersionCheckID, want, c.Problem)
	}
	for _, what := range []string{repoNoticeYAMLFile, "version 1", "since v0.42.0"} {
		if !strings.Contains(c.Problem, what) {
			t.Errorf("%s does not name %q:\n%s", repoYAMLVersionCheckID, what, c.Problem)
		}
	}
	if !strings.Contains(c.Fix, repoNoticeYAMLFile) || !strings.Contains(c.Fix, "version:") {
		t.Errorf("%s's fix does not name the file's own edit: %q", repoYAMLVersionCheckID, c.Fix)
	}
	// Nothing is written: the version line is the repository's own change
	// (spec §1), and doctor writes no working tree.
	if got := convergeRead(t, filepath.Join(root, repoNoticeYAMLFile)); got != refreshYAML {
		t.Errorf("%s after doctor:\n%s\nwant it left as the repository committed it", repoNoticeYAMLFile, got)
	}
}

// TestUpdateNamesDeprecatedVersion: the other surface, off the update's own
// step over the same views (noticeRepoValues). The line is the one doctor's row
// carries, and the run writes nothing.
func TestUpdateNamesDeprecatedVersion(t *testing.T) {
	deprecateVersions(t, v1Window...)
	root := repoCheckout(t, githubOrigin, refreshYAML)
	t.Chdir(root)
	e, out := checkoutEngine(t, nil)

	e.noticeRepoValues(theUpdateLeavesTheRemoval(t.Context()))

	seen := out.String()
	want := "acme/app: " + string(initflow.RepoNeedsYou) + ": " + configVersionNotice(v1Window[0])
	if !strings.Contains(seen, want) {
		t.Fatalf("the run printed no deprecation notice, want %q:\n%s", want, seen)
	}
	if got := convergeRead(t, filepath.Join(root, repoNoticeYAMLFile)); got != refreshYAML {
		t.Errorf("%s after the step:\n%s\nwant it left exactly as the release behind wrote it", repoNoticeYAMLFile, got)
	}
}
