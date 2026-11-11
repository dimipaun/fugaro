package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// doctor_security.go's tests (SC4, F11, D1503, D1513).
//
// What they hold. The line names the list in force by counts and by name and
// never by a value (D1513), counting the floor's rows, the project layer's and
// the repository's separately (SC9); a list a person added nothing to is a
// measurement and moves behind --security — and the one finding it can carry, a
// row this repository wrote that matches nothing in the base, is the
// reader's to fix and is an item of the default view (D1503). The two layers
// that are not the repository's own are counted and never measured: neither the
// floor nor a project layer is this reader's to edit.

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

// doctorSecurityTestOwn is a repository's own security: block: one row the base
// matches and one it does not.
func doctorSecurityTestOwn() *config.Security {
	return &config.Security{Patterns: []sensitivity.Pattern{
		{Name: "creds", Match: "internal/creds/**", Category: sensitivity.Credentials},
		{Name: "prod-secrets", Match: "config/prod/**", Category: sensitivity.Credentials},
	}}
}

// doctorSecurityTestChecks runs the check over the repository's own rows and the
// base of a checkout, the way the command hands it the two layers.
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
	return doctorSecurityListChecks(
		func() (*config.Security, *config.ProjectLayer) { return doctorSecurityTestOwn(), nil },
		func() (string, []string) { return ref, paths })
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
		"written by this project's layer",
		"written by this repository",
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
	checks := doctorSecurityListChecks(
		func() (*config.Security, *config.ProjectLayer) { return nil, nil },
		func() (string, []string) {
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
// read — says the list in force and no finding, rather than calling every
// written row unmatched.
func TestDoctorSecuritySaysNothingAboutABaseItCannotRead(t *testing.T) {
	root := doctorSecurityTestBase(t, map[string]string{"README.md": "# aurora\n"})
	ctx := context.Background()
	if _, ok := doctorSecurityBaseTree(ctx, root, "origin/nothing-here"); ok {
		t.Error("a ref with no tree was read as one")
	}
	if _, ok := doctorSecurityBaseTree(ctx, root, ""); ok {
		t.Error("no ref was read as a tree")
	}
	checks := doctorSecurityListChecks(
		func() (*config.Security, *config.ProjectLayer) { return doctorSecurityTestOwn(), nil },
		func() (string, []string) { return "", nil })
	if len(checks) != 1 {
		t.Fatalf("checks = %+v, want the list in force's line alone", checks)
	}
}

// TestDoctorReadsTheRepositorysOwnRowsAtTheBase: the two written layers come
// from the one resolution, and the repository's half of it is read out of its
// fugaro.yaml at the default branch's ref and never out of the working copy
// (D1518). The row the base writes is counted as the repository's own and
// measured against the base's tree; the row only the working copy writes is not
// read at all.
func TestDoctorReadsTheRepositorysOwnRowsAtTheBase(t *testing.T) {
	isolateCache(t)
	const file = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"security: { patterns: [ { name: %s, match: \"%s\", category: credentials } ] }\n" +
		"workflows: { web: { commands: { build: sh b.sh, test: sh t.sh } } }\n"
	root := doctorSecurityTestBase(t, map[string]string{
		"README.md":               "# aurora\n",
		"internal/creds/state.go": "package creds\n",
		"fugaro.yaml":             fmt.Sprintf(file, "payments", "internal/payments/**"),
	})
	testutil.WriteFiles(t, root, map[string]string{
		"fugaro.yaml": fmt.Sprintf(file, "rogues", "internal/rogues/**"),
	})

	checks := doctorSecurityChecks(context.Background(), root)

	line, ok := doctorCheckByID(checks, doctorSecurityID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityID, checks)
	}
	if !strings.Contains(line.Problem, "1 pattern written by this repository") {
		t.Errorf("problem = %q, want the base's own row counted as the repository's", line.Problem)
	}
	found, ok := doctorCheckByID(checks, doctorSecurityUnusedID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityUnusedID, checks)
	}
	if !strings.Contains(found.Problem, "payments") {
		t.Errorf("problem = %q, want the row the base's own file writes", found.Problem)
	}
	if strings.Contains(found.Problem, "rogues") {
		t.Errorf("problem = %q, names a row only the working copy writes: the base branch's file is the one read (D1518)", found.Problem)
	}
}

// TestDoctorDoesNotMeasureARowTheMatcherCannotUse: the repository's own rows are
// matched against the base's tree (D1503), so they are measured first with the
// function a run's load measures them with (sensitivity.Pattern.Validate): the
// row it refuses is not counted and not measured, and the valid row beside it is
// counted and measured, so a file that writes one unusable row is diagnosed
// rather than hung on.
//
// The base commits securityAlternationPath, which the fixture's second row would
// spend hours on if it were matched as written — so this test is the hang too.
// Mutation checked by removing the drop from securityMatchable: the line's count
// grows and both checks stop answering (`go test -run
// TestDoctorDoesNotMeasureARowTheMatcherCannotUse -timeout 30s` times out). The
// measurement was put back afterwards.
//
// What doctor does not do is name the refused row: the file that writes one is
// the `fugaro-yaml` line's subject, over the working copy's file, and a
// base-branch file that differs from the working copy's is `fugaro security
// paths`' to report — which says the row and sensitivity's own reason.
func TestDoctorDoesNotMeasureARowTheMatcherCannotUse(t *testing.T) {
	isolateCache(t)
	file := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"security: { patterns: [ { name: payments, match: \"internal/payments/**\", category: credentials }, " +
		"{ name: evil, match: \"" + securityAlternationMatch + "\", category: credentials } ] }\n" +
		"workflows: { web: { commands: { build: sh b.sh, test: sh t.sh } } }\n"
	root := doctorSecurityTestBase(t, map[string]string{
		"README.md":               "# aurora\n",
		"internal/creds/state.go": "package creds\n",
		securityAlternationPath:   "code\n",
		"fugaro.yaml":             file,
	})

	checks := doctorSecurityChecks(context.Background(), root)

	line, ok := doctorCheckByID(checks, doctorSecurityID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityID, checks)
	}
	if !strings.Contains(line.Problem, "1 pattern written by this repository") {
		t.Errorf("problem = %q, want the one row a reader may match counted and the refused row not", line.Problem)
	}
	found, ok := doctorCheckByID(checks, doctorSecurityUnusedID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityUnusedID, checks)
	}
	if !strings.Contains(found.Problem, "payments") {
		t.Errorf("problem = %q, want the valid row measured against the base's tree", found.Problem)
	}
	if strings.Contains(found.Problem, "evil") || strings.Contains(found.Problem, "{a,aa}") {
		t.Errorf("problem = %q, names a row the matcher refuses: it is not in the list in force, so it is neither counted nor measured", found.Problem)
	}
}

// doctorSecurityProjectLayerYAML is a project layer whose security: block writes
// one row this repository's base matches and one it does not (D1501). A layer's
// rows are shared by every repository of the project, so a row naming a tree
// this repository does not have is the ordinary case and not a mistake of the
// reader's.
const doctorSecurityProjectLayerYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\n" +
	"security:\n  patterns:\n" +
	"    - { name: creds, match: \"internal/creds/**\", category: credentials }\n" +
	"    - { name: settlements, match: \"internal/settlements/**\", category: credentials }\n"

// doctorSecurityLayerChecks runs the check in a checkout whose machine holds the
// layer above cached — the branch a project's rows only reach on a machine that
// has read the published layer — with the repository's own block written into
// the base's fugaro.yaml. own is that block's rows as YAML, or "" for a file
// that writes no security: key at all.
func doctorSecurityLayerChecks(t *testing.T, own string) []doctorCheck {
	t.Helper()
	isolateCache(t)
	file := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows: { web: { commands: { build: sh b.sh, test: sh t.sh } } }\n"
	if own != "" {
		file += "security: { patterns: [ " + own + " ] }\n"
	}
	root := doctorSecurityTestBase(t, map[string]string{
		"README.md":               "# aurora\n",
		"internal/creds/state.go": "package creds\n",
		"fugaro.yaml":             file,
	})
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: doctorSecurityProjectLayerYAML,
	}); err != nil {
		t.Fatal(err)
	}
	return doctorSecurityChecks(context.Background(), root)
}

// The project layer's rows are counted and never measured. A layer row that
// matches nothing in this repository's base is the ordinary case — the layer is
// shared by every repository of the project — and the fix D1503's finding asks
// for, drop the row, is one only the project's owner could apply, taking it away
// from the repository that does have that tree. A finding nobody can repair is
// worse than none, which is the rule the floor's own rows already follow.
func TestDoctorCountsTheProjectLayersRowsWithoutMeasuringThem(t *testing.T) {
	checks := doctorSecurityLayerChecks(t, "")

	line, ok := doctorCheckByID(checks, doctorSecurityID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityID, checks)
	}
	if !strings.Contains(line.Problem, "2 patterns written by this project's layer") {
		t.Errorf("problem = %q, want the cached layer's two rows counted", line.Problem)
	}
	if found, ok := doctorCheckByID(checks, doctorSecurityUnusedID); ok {
		t.Errorf("%s = %+v, want no finding: a project layer's row is not this reader's to fix", doctorSecurityUnusedID, found)
	}
	if checksFail(checks, true) {
		t.Errorf("--strict fails on a list whose only written rows are the project's: %+v", checks)
	}
}

// The repository's own rows are still measured with a layer present, and the
// finding names none of the layer's.
func TestDoctorMeasuresOnlyTheRowsThisRepositoryWrote(t *testing.T) {
	checks := doctorSecurityLayerChecks(t,
		"{ name: prod-secrets, match: \"config/prod/**\", category: credentials }")

	found, ok := doctorCheckByID(checks, doctorSecurityUnusedID)
	if !ok {
		t.Fatalf("no %s check: %+v", doctorSecurityUnusedID, checks)
	}
	if !strings.Contains(found.Problem, "prod-secrets") {
		t.Errorf("problem = %q, want the repository's own row", found.Problem)
	}
	if strings.Contains(found.Problem, "settlements") {
		t.Errorf("problem = %q, names a row of the project layer: only the rows this repository wrote are its reader's to fix", found.Problem)
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
