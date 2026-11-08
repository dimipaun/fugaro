package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// doctor_security.go's tests (SC4, F11, D1503, D1513).
//
// What they hold. The line names the list in force by counts and by name and
// never by a value (D1513); in slice 1 the list in force is the engine's floor
// alone, so the line is a measurement and moves behind --security — and the
// one finding it can carry, a written row that matches nothing in the base, is
// the reader's to fix and is an item of the default view (D1503).

// doctorSecurityTestBase is a checkout whose origin/main holds the given
// files, committed: the base a written row is measured against (D1518 — the
// base branch's tree, never the working copy).
func doctorSecurityTestBase(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, root, "init", "-q")
	testutil.WriteFiles(t, root, files)
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-q", "-m", "base")
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	return root
}

// doctorSecurityTestWritten is the list a project layer or the repository
// wrote: one row the base matches and one it does not.
func doctorSecurityTestWritten() []sensitivity.Pattern {
	return []sensitivity.Pattern{
		{Name: "creds", Match: "internal/creds/**", Category: sensitivity.Credentials},
		{Name: "prod-secrets", Match: "config/prod/**", Category: sensitivity.Credentials},
	}
}

// doctorSecurityTestChecks runs the check over a written list and the base of
// a checkout, the way SC9 will hand it the resolution.
func doctorSecurityTestChecks(t *testing.T) []doctorCheck {
	t.Helper()
	root := doctorSecurityTestBase(t, map[string]string{
		"README.md":               "# aurora\n",
		"internal/creds/state.go": "package creds\n",
	})
	ctx := context.Background()
	ref := defaultBranchRef(ctx, root)
	if ref != "origin/main" {
		t.Fatalf("defaultBranchRef = %q, want origin/main", ref)
	}
	paths, ok := doctorSecurityBaseTree(ctx, root, ref)
	if !ok {
		t.Fatalf("no base tree for %s", ref)
	}
	return doctorSecurityListChecks(sensitivity.Floor(), doctorSecurityTestWritten(), func() (string, []string) {
		return ref, paths
	})
}

// doctorSecurityTestFinding is that run's finding, or a failure.
func doctorSecurityTestFinding(t *testing.T) doctorCheck {
	t.Helper()
	c, ok := doctorCheckByID(doctorSecurityTestChecks(t), doctorSecurityUnusedID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityUnusedID, doctorSecurityTestChecks(t))
	}
	return c
}

// The list in force is named: how many rows, and where each count comes from.
func TestDoctorNamesTheSecurityListInForce(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, doctorSecurityID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityID, o.Checks)
	}
	if !c.OK || c.Severity != "info" {
		t.Errorf("list in force = %+v, want an ok info line", c)
	}
	floor := len(sensitivity.Floor())
	for _, want := range []string{
		doctorSecurityPatternCount(floor), // the list in force
		"from the engine's floor",
		"written by this project or by this repository",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem = %q, want %q", c.Problem, want)
		}
	}
	if c.Fix == "" {
		t.Error("no fix line")
	}
}

// A list nothing was added to is a measurement: it is sound, it fails no run
// of doctor, and it costs no git read over the base.
func TestDoctorSecurityCheckIsAMeasureWhenTheListIsSound(t *testing.T) {
	read := false
	checks := doctorSecurityListChecks(sensitivity.Floor(), nil, func() (string, []string) {
		read = true
		return "origin/main", []string{"README.md"}
	})
	if read {
		t.Error("the check read the base for a list nothing was written to")
	}
	if len(checks) != 1 {
		t.Fatalf("checks = %+v, want the one line", checks)
	}
	if got := doctorClassOf(checks[0]); got != doctorClassMeasure {
		t.Errorf("class = %q, want %q", got, doctorClassMeasure)
	}
	if checksFail(checks, true) {
		t.Error("a sound list fails doctor")
	}
	if out := doctorSecurityOutput(t, checks); !strings.Contains(out, "the security list in force") {
		t.Errorf("--security does not carry the line:\n%s", out)
	}
	if out := doctorDefaultOutput(t, checks); strings.Contains(out, "the security list in force") {
		t.Errorf("the default view printed a measurement:\n%s", out)
	}
}

// D1503's second half: a written row that matches no path in the base is said
// once, and the row that does match is not.
func TestDoctorSaysWhenAPatternMatchedNothingInTheBase(t *testing.T) {
	c := doctorSecurityTestFinding(t)
	if !strings.Contains(c.Problem, "prod-secrets") || !strings.Contains(c.Problem, "config/prod/**") {
		t.Errorf("problem = %q, want the row that matched nothing", c.Problem)
	}
	if !strings.Contains(c.Problem, "origin/main") {
		t.Errorf("problem = %q, want the base it was measured against", c.Problem)
	}
	if strings.Contains(c.Problem, "creds (internal/creds/**)") {
		t.Errorf("problem = %q, names the row the base does match", c.Problem)
	}
	if c.Fix == "" {
		t.Error("no fix line")
	}
}

// That finding is an item the reader fixes: it fails --strict, it is never
// hidden by --security's sweep, and it is not a measurement.
func TestDoctorSecurityCheckIsAFindingWhenAPatternMatchedNothing(t *testing.T) {
	checks := doctorSecurityTestChecks(t)
	found := doctorSecurityTestFinding(t)
	if got := doctorClassOf(found); got != doctorClassActionable {
		t.Errorf("class = %q, want %q", got, doctorClassActionable)
	}
	if checksFail(checks, false) {
		t.Error("the plain run fails on a row that matches nothing")
	}
	if !checksFail(checks, true) {
		t.Error("--strict does not fail on a row that matches nothing")
	}
	if out := doctorDefaultOutput(t, checks); !strings.Contains(out, "prod-secrets") {
		t.Errorf("the default view hides the finding:\n%s", out)
	}
}

// A base nothing can be measured against — no ref, or a tree that cannot be
// read — says the floor alone rather than calling every written row unmatched.
func TestDoctorSecuritySaysNothingAboutABaseItCannotRead(t *testing.T) {
	root := doctorSecurityTestBase(t, map[string]string{"README.md": "# aurora\n"})
	ctx := context.Background()
	if _, ok := doctorSecurityBaseTree(ctx, root, "origin/nothing-here"); ok {
		t.Error("a ref with no tree was read as one")
	}
	if _, ok := doctorSecurityBaseTree(ctx, root, ""); ok {
		t.Error("no ref was read as a tree")
	}
	checks := doctorSecurityListChecks(sensitivity.Floor(), doctorSecurityTestWritten(), func() (string, []string) {
		return "", nil
	})
	if len(checks) != 1 {
		t.Fatalf("checks = %+v, want the floor's line alone", checks)
	}
}

// It is the security sweep's: --security carries the measurement, and the
// default view does not.
func TestDoctorSecurityCheckIsOnTheSecuritySweep(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	quiet, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	if strings.Contains(quiet, "the security list in force") {
		t.Errorf("the default view printed the sweep's measurement:\n%s", quiet)
	}
	swept, _, _ := execute(t, "doctor", "--security", "--dir", t.TempDir())
	if !strings.Contains(swept, "the security list in force") {
		t.Errorf("--security does not carry the line:\n%s", swept)
	}
}

// --json carries it whole, class and all.
func TestDoctorSecurityCheckAppearsInJSON(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, doctorSecurityID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityID, o.Checks)
	}
	if c.Class != doctorClassMeasure {
		t.Errorf("class = %q, want %q", c.Class, doctorClassMeasure)
	}
	found := doctorSecurityTestFinding(t)
	if got := classifyDoctorChecks([]doctorCheck{found})[0].Class; got != doctorClassActionable {
		t.Errorf("the finding's class = %q, want %q", got, doctorClassActionable)
	}
}
