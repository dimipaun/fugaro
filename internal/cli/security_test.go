package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// securityDocShape is D1512's document as these tests read it: the shape
// `fugaro security paths --json` prints, spelled here rather than borrowed from
// the command's own type. A test that decoded into the implementation's type
// would agree with whatever that type printed, and this shape is the design's
// (D1512) — the command is written against it, not the other way round.
type securityDocShape struct {
	Project     string `json:"project"`
	Repo        string `json:"repo"`
	Base        string `json:"base"`
	FloorSource string `json:"floor_source"`
	Error       string `json:"error"`
	Security    struct {
		Patterns []struct {
			Name     string `json:"name"`
			Match    string `json:"match"`
			Category string `json:"category"`
			Source   string `json:"source"`
		} `json:"patterns"`
		Count int `json:"count"`
	} `json:"security"`
	Checked []struct {
		Path      string `json:"path"`
		Sensitive bool   `json:"sensitive"`
		Matched   *struct {
			Name     string `json:"name"`
			Category string `json:"category"`
			Source   string `json:"source"`
		} `json:"matched"`
	} `json:"checked"`
}

// securityNoKeyYAML is the file every fixture here commits: a valid fugaro.yaml
// that names the project and the base branch and carries no security key at
// all, which is the state of most repositories. It is what makes the list in
// force the floor alone here, with this machine's layer cache isolated as every
// test in this file has it (securityCheckout).
// TestSecurityPathsOnARepositoryWithNoKeyIsTheFloor holds the "no key" half of
// that claim against the text itself rather than against this comment.
var securityNoKeyYAML = baseBranchYAML("git: { provider: github, base_branch: main }\n")

// securityCheckout makes the working directory a checkout of repo whose base
// branch main holds yaml, and returns the checkout's directory. The command
// answers for the repository the working directory is a checkout of and reads
// the list at that checkout's base branch, so every test here starts in one.
func securityCheckout(t *testing.T, repo, yaml string) string {
	t.Helper()
	// The project layer's half of the list comes from this machine's cached copy
	// of the published layer (modelsLayer), so a test that did not isolate the
	// cache would read whatever layer the machine running it happens to hold.
	isolateCache(t)
	baseBranchCheckout(t, repo, yaml, "main")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// securityDoc runs `fugaro security paths` with args and decodes its --json
// document, the read every test below is written against.
func securityDoc(t *testing.T, args ...string) securityDocShape {
	t.Helper()
	out, _, err := execute(t, append(args, "--json")...)
	if err != nil {
		t.Fatalf("fugaro %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	var doc securityDocShape
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return doc
}

// securityRow is one row of the list in force as the document prints it, so a
// test compares the command's own output against sensitivity.Floor() without
// the anonymous structs the decoder is built from.
type securityRow struct{ name, match, category, source string }

// securityRows reduces the document's list to those four strings.
func securityRows(doc securityDocShape) []securityRow {
	rows := make([]securityRow, 0, len(doc.Security.Patterns))
	for _, p := range doc.Security.Patterns {
		rows = append(rows, securityRow{p.Name, p.Match, p.Category, p.Source})
	}
	return rows
}

// securityFloorRows is sensitivity.Floor() in the same shape, with the source
// label every floor row carries — D1512's `floor`, spelled here as the design
// spells it.
func securityFloorRows() []securityRow {
	floor := sensitivity.Floor()
	rows := make([]securityRow, 0, len(floor))
	for _, p := range floor {
		rows = append(rows, securityRow{p.Name, p.Match, string(p.Category), "floor"})
	}
	return rows
}

// TestSecurityPathsPrintsTheListInForce is the read's own test: without --check
// the command prints the list in force — the floor alone here, every row with
// its source layer — and the document says which repository, which
// project and which base branch it answered for.
func TestSecurityPathsPrintsTheListInForce(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	doc := securityDoc(t, "security", "paths")

	if doc.Repo != "acme/app" {
		t.Errorf("repo = %q, want %q", doc.Repo, "acme/app")
	}
	if doc.Base != "main" {
		t.Errorf("base = %q, want %q: the checkout's fugaro.yaml writes git.base_branch: main", doc.Base, "main")
	}
	if doc.Project != "aurora" {
		t.Errorf("project = %q, want %q: the base branch's fugaro.yaml names it", doc.Project, "aurora")
	}
	if doc.Error != "" {
		t.Errorf("error = %q, want the base branch's fugaro.yaml to be readable", doc.Error)
	}
	floor := securityFloorRows()
	if doc.Security.Count != len(floor) {
		t.Fatalf("security.count = %d, want %d (the floor's own rows)", doc.Security.Count, len(floor))
	}
	got := securityRows(doc)
	if len(got) != len(floor) {
		t.Fatalf("security.patterns = %d rows, want %d", len(got), len(floor))
	}
	for i, want := range floor {
		if got[i] != want {
			t.Errorf("security.patterns[%d] = %+v, want %+v (the floor's own order)", i, got[i], want)
		}
	}

	out, _, err := execute(t, "security", "paths")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	for _, want := range []string{"acme/app", "main", "internal/creds/**", "credentials", "43"} {
		if !strings.Contains(out, want) {
			t.Errorf("the printed list does not name %q:\n%s", want, out)
		}
	}
}

// TestSecurityPathsCheckAnswersPerPath: with --check the command answers one
// path at a time — the row that answered, and whether the path is sensitive at
// all — and a path the list in force has nothing to say about is answered `no`
// with no row rather than left out. Three paths are Check 370's own: a floor
// row's path, a routine tree the floor says nothing about, and the case between
// them — a routine tree with a listed exception — which is the shape a project's
// own list will have. The fourth is the case that tells this command's rule from
// the classifier's complement: a path the reader places perfectly well, sitting
// in no routine tree and on no floor row, which a run does not escalate on and
// which is therefore answered `no`.
func TestSecurityPathsCheckAnswersPerPath(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	doc := securityDoc(t, "security", "paths",
		"--check", "internal/creds/state.go",
		"--check", "docs/recipes.md",
		"--check", "docs/pending-workflows/console-tests.yml",
		"--check", "internal/cli/registry.go")

	if len(doc.Checked) != 4 {
		t.Fatalf("checked = %d answers, want one per --check path", len(doc.Checked))
	}
	cred := doc.Checked[0]
	if cred.Path != "internal/creds/state.go" {
		t.Errorf("checked[0].path = %q, want the path as it was asked", cred.Path)
	}
	if !cred.Sensitive {
		t.Error("checked[0].sensitive = false, want true: the floor's credentials row matches internal/creds/**")
	}
	if cred.Matched == nil {
		t.Fatalf("checked[0].matched is null, want the row that decided: the floor's credentials row")
	}
	if cred.Matched.Name != "credentials" || cred.Matched.Category != "credentials" || cred.Matched.Source != "floor" {
		t.Errorf("checked[0].matched = %+v, want the floor's credentials row (credentials, floor)", *cred.Matched)
	}

	recipes := doc.Checked[1]
	if recipes.Path != "docs/recipes.md" {
		t.Errorf("checked[1].path = %q, want %q", recipes.Path, "docs/recipes.md")
	}
	if recipes.Sensitive {
		t.Error("checked[1].sensitive = true, want false: docs/recipes.md matches no row and docs/ is a routine tree")
	}
	if recipes.Matched != nil {
		t.Errorf("checked[1].matched = %+v, want null: no row of the list in force matched it", *recipes.Matched)
	}

	workflows := doc.Checked[2]
	if workflows.Path != "docs/pending-workflows/console-tests.yml" {
		t.Errorf("checked[2].path = %q, want %q", workflows.Path, "docs/pending-workflows/console-tests.yml")
	}
	if !workflows.Sensitive {
		t.Error("checked[2].sensitive = false, want true: docs/pending-workflows/** is a floor row, so a workflow staged there is listed although docs/ is routine")
	}
	if workflows.Matched == nil {
		t.Fatalf("checked[2].matched is null, want the floor's pending-workflows row")
	}
	if workflows.Matched.Name != "pending-workflows" || workflows.Matched.Category != "CI" || workflows.Matched.Source != "floor" {
		t.Errorf("checked[2].matched = %+v, want the floor's pending-workflows row (CI, floor)", *workflows.Matched)
	}

	// The discriminating case. internal/cli/registry.go is a clean
	// repository-relative path, so the reader places it; no floor row matches it,
	// so the list in force says nothing about it; and the classifier calls it
	// Sensitive, because it sits in no routine tree — which is a large part of
	// any repository, and the reason `sensitive` must not be the classifier's
	// complement. The class is asserted here, and not only the answer, so this
	// case keeps telling the two rules apart: if the tree ever becomes routine,
	// this test says so instead of quietly agreeing with both rules.
	unlisted := doc.Checked[3]
	if unlisted.Path != "internal/cli/registry.go" {
		t.Errorf("checked[3].path = %q, want %q", unlisted.Path, "internal/cli/registry.go")
	}
	if got := sensitivity.Classify([]string{"internal/cli/registry.go"}); got != sensitivity.Sensitive {
		t.Fatalf("sensitivity.Classify(internal/cli/registry.go) = %v, want %v: this case is only discriminating while the class says Sensitive and the floor's rows do not", got, sensitivity.Sensitive)
	}
	if len(sensitivity.Match([]string{"internal/cli/registry.go"}, sensitivity.Floor())) != 0 {
		t.Fatalf("the floor matches internal/cli/registry.go, want no row: this case is the placeable path the list in force says nothing about")
	}
	if unlisted.Sensitive {
		t.Error("checked[3].sensitive = true, want false: the list in force matches no row, and a run escalates on the list, not on the classifier's class")
	}
	if unlisted.Matched != nil {
		t.Errorf("checked[3].matched = %+v, want null: no row of the list in force matched it", *unlisted.Matched)
	}

	out, _, err := execute(t, "security", "paths",
		"--check", "internal/creds/state.go",
		"--check", "docs/recipes.md",
		"--check", "internal/cli/registry.go",
		"--check", "docs/pending-workflows/console-tests.yml")
	if err != nil {
		t.Fatalf("fugaro security paths --check: %v\n%s", err, out)
	}
	for _, want := range []string{"internal/creds/state.go", "docs/recipes.md", "docs/pending-workflows/console-tests.yml", "internal/cli/registry.go", "credentials", "pending-workflows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the printed answers do not name %q:\n%s", want, out)
		}
	}
}

// TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath: a path the reader
// cannot place is sensitive, and no row is named for it (F22, D1512). Every
// spelling here but one is one SC1's own rule refuses — `/etc/passwd` is
// absolute, `../SECURITY.md`, `../../etc/passwd` and a bare `..` escape the
// checkout, `docs/../website/src/x.md` is rewritten by path.Clean, and the empty
// string is no path at all — and each of them means more than one path, so a
// list can never answer `no` for one. The escaping spellings are what make this
// test the fail-closed one rather than a restatement: a rule that admitted a
// leading `..` would place them, and the `**` probe this command asks would then
// match them and answer `sensitive: false` for a path outside the repository.
// The exception is `.`: the one spelling that same rule *accepts*, because
// path.Clean leaves a bare `.` exactly as it is, and that names no path in a
// checkout either (`.` is the repository root as a directory reference). So it
// is refused where this command composes the answer (securityPlaceable) rather
// than by the matcher, and this test is what holds that boundary: it is the
// answer a reader would otherwise be told the list says nothing about, which is
// the one answer D1512 keeps out of this field.
func TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	paths := []string{"/etc/passwd", "../SECURITY.md", "../../etc/passwd", "docs/../website/src/x.md", "", ".", ".."}
	args := []string{"security", "paths"}
	for _, p := range paths {
		args = append(args, "--check", p)
	}
	doc := securityDoc(t, args...)

	if len(doc.Checked) != len(paths) {
		t.Fatalf("checked = %d answers, want %d", len(doc.Checked), len(paths))
	}
	for i, want := range paths {
		got := doc.Checked[i]
		if got.Path != want {
			t.Errorf("checked[%d].path = %q, want %q", i, got.Path, want)
		}
		if !got.Sensitive {
			t.Errorf("checked[%d].sensitive = false for %q, want true: a path the reader cannot place fails closed", i, want)
		}
		if got.Matched != nil {
			t.Errorf("checked[%d].matched = %+v for %q, want null: no row of a list written against clean repository-relative paths can name it", i, *got.Matched, want)
		}
	}
}

// TestSecurityPathsNamesTheSourceLayerOfEveryRow: every row of the list in
// force carries the layer it came from, out of D1512's three words, and the
// document names the layer the floor's rows carry once, so a reader can tell
// the engine's own rows from a project's and a repository's without scanning
// the list. The repository here writes no key and this machine holds no cached
// layer, so the list is the floor alone (D1504) and every row is `floor`; the
// two written layers are TestSecurityPathsResolvesTheRepositorysRowsAtTheBase's
// and TestSecurityPathsCarriesTheProjectLayersRows'.
func TestSecurityPathsNamesTheSourceLayerOfEveryRow(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	doc := securityDoc(t, "security", "paths")

	if doc.FloorSource != "floor" {
		t.Errorf("floor_source = %q, want %q", doc.FloorSource, "floor")
	}
	layers := []string{"floor", "project", "repo"}
	for i, row := range securityRows(doc) {
		if !slices.Contains(layers, row.source) {
			t.Errorf("security.patterns[%d].source = %q, want one of %s (D1512)", i, row.source, strings.Join(layers, ", "))
			continue
		}
		if row.source != "floor" {
			t.Errorf("security.patterns[%d] (%s) has source %q, want %q: this repository writes no security key and this machine holds no cached layer for the project", i, row.name, row.source, "floor")
		}
	}
	if doc.Security.Count == 0 {
		t.Fatal("the list in force is empty, so this check passed on no rows at all")
	}
}

// TestSecurityPathsReadsTheBaseAndNotTheWorktree is D1518's property, and the
// whole of this command's safety: the list in force is read at the base branch,
// so a change cannot carry the rule that governs its own review. The working
// tree here says a different project from the committed base — one of the things
// this reads at the ref — and the document has to carry the base's.
func TestSecurityPathsReadsTheBaseAndNotTheWorktree(t *testing.T) {
	dir := securityCheckout(t, "acme/app", securityNoKeyYAML)
	working := strings.Replace(securityNoKeyYAML, "project: aurora", "project: bramble", 1)
	if working == securityNoKeyYAML {
		t.Fatal("the fixture no longer names a project, so this test would pass on two identical files")
	}
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(working), 0o600); err != nil {
		t.Fatal(err)
	}

	doc := securityDoc(t, "security", "paths")

	if doc.Project != "aurora" {
		t.Errorf("project = %q, want %q: the document is read at origin/main, and the working tree's bramble is not committed", doc.Project, "aurora")
	}
	if doc.Base != "main" {
		t.Errorf("base = %q, want %q", doc.Base, "main")
	}
}

// TestSecurityPathsNamesItsFieldsInTheContract: the document is part of the
// console's JSON contract, and that section is read here in both directions —
// every field the command prints is named by the section, and the section names
// the command's own two flags and D1512's three source layers. A section that
// documents a member the command does not print, or a command that prints one
// its section does not name, is a contract that has drifted.
func TestSecurityPathsNamesItsFieldsInTheContract(t *testing.T) {
	const path = "console/docs/json-contract.md"
	// The section is read before the fixture moves the working directory into
	// its own checkout: readDoc resolves the path from the package's directory.
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "### 6.23 `fugaro security paths --json`")
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	out, _, err := execute(t, "security", "paths", "--check", "internal/creds/state.go", "--json")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	var printed any
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	keys := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, val := range v {
				keys[k] = true
				walk(val)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(printed)
	if len(keys) < 10 {
		t.Fatalf("the document carries %d keys (%v); this check has stopped looking", len(keys), keys)
	}
	for key := range keys {
		if !strings.Contains(section, "`"+key+"`") {
			t.Errorf("%s §6.23 does not name %q, which the command prints", path, key)
		}
	}
	for _, want := range []string{
		"fugaro security paths", "--check", "--json",
		"`floor`", "`project`", "`repo`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s §6.23 does not name %s", path, want)
		}
	}
}

// TestSecurityPathsOnARepositoryWithNoKeyIsTheFloor: a repository whose
// fugaro.yaml carries no security key resolves to the floor alone and never to
// nothing (D1504). What the test holds is the negative half — the file really
// writes none — and the positive one: with no project layer cached on this
// machine the list in force is sensitivity.Floor(), row for row.
func TestSecurityPathsOnARepositoryWithNoKeyIsTheFloor(t *testing.T) {
	if strings.Contains(securityNoKeyYAML, "security") {
		t.Fatal("the fixture writes a security key, so this test would pass on a resolution that read one")
	}
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	doc := securityDoc(t, "security", "paths")

	floor := securityFloorRows()
	got := securityRows(doc)
	if len(got) != len(floor) {
		t.Fatalf("security.patterns = %d rows, want the floor's %d", len(got), len(floor))
	}
	if doc.Security.Count != len(floor) {
		t.Errorf("security.count = %d, want %d", doc.Security.Count, len(floor))
	}
	for i, want := range floor {
		if got[i] != want {
			t.Fatalf("security.patterns[%d] = %+v, want the floor's %+v: a repository with no key resolves to the floor alone", i, got[i], want)
		}
	}
}

// TestSecurityPathsSaysSoWhenTheBaseCannotBeRead: a base branch this checkout
// cannot read the file at is a state inside the document, not a silent floor.
// The record of a read that did not answer is the `error` member (D1512), and
// the list in force is still printed — a caller that only wants to know what a
// path is treated as can still be answered — while every answer it carries is
// marked as the floor alone rather than as the repository's own list.
//
// The fixture is the repository being onboarded: main holds no fugaro.yaml yet,
// and the working tree does, which is exactly the difference this command must
// not be fooled by (D1518).
func TestSecurityPathsSaysSoWhenTheBaseCannotBeRead(t *testing.T) {
	dir := securityCheckout(t, "acme/app", securityNoKeyYAML)
	testutil.Git(t, dir, "rm", "-q", "--cached", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository before its fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")

	doc := securityDoc(t, "security", "paths", "--check", "internal/creds/state.go")

	if doc.Error == "" {
		t.Fatal("error is empty, want the read to say that origin/main holds no fugaro.yaml: a repository being onboarded is not a repository with no patterns")
	}
	if doc.Project != "" {
		t.Errorf("project = %q, want %q: the base's file does not name one", doc.Project, "")
	}
	if doc.Security.Count != len(securityFloorRows()) {
		t.Errorf("security.count = %d, want the floor's %d rows with the error beside them", doc.Security.Count, len(securityFloorRows()))
	}
	if len(doc.Checked) != 1 || !doc.Checked[0].Sensitive || doc.Checked[0].Matched == nil {
		t.Errorf("checked = %+v, want the floor's answer for internal/creds/state.go beside the error", doc.Checked)
	}

	out, errOut, err := execute(t, "security", "paths")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	if !strings.Contains(out, "internal/creds/**") {
		t.Errorf("the printed list does not carry the floor's rows:\n%s", out)
	}
	if !strings.Contains(errOut, "fugaro.yaml") {
		t.Errorf("stderr does not name what could not be read:\n%s", errOut)
	}
}

// securityAnchoredYAML is securityNoKeyYAML with the anchor a project layer is
// read for: `gcp_project:` beside `project:`. findLayer reads nothing at all
// for a file that carries neither, so a fixture without them could not tell an
// offline command from one that opens the project layer's bucket.
var securityAnchoredYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github, base_branch: main }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// TestSecurityPathsOpensNoBucket is the document's own promise held to the
// code: every read this command makes is offline (§6.23). The one place it
// could have been broken is the base branch's own name, whose layer 1 a
// launcher resolves through checkoutBaseBranch — and that read resolves the
// checkout's file over the published project layer, which opens the project
// layer's bucket (checkoutParse → findLayer) and writes the local layer cache.
//
// The fixture is exactly the shape that would have done it: an anchored
// checkout (project: and gcp_project:), which is what findLayer needs before it
// reads anything, and a selected project config whose runs_bucket is the
// default name for its GCP project, so the layer is read from
// gs://fugaro-runs-proj-1234 rather than skipped as another installation's.
//
// The two openers below are the seams TestMain's own guard uses (it panics on a
// gs:// URL), overridden here so a failure names the rule rather than the URL:
// layerBucketOpener for the project layer (checkoutParse → findLayer) and
// sharedBucketOpener for the published project config a shared selection
// fetches. The policy node is the third cloud read this could have made — the
// launcher's own layer 3 — and it needs a cloudEnv, which no path here builds.
func TestSecurityPathsOpensNoBucket(t *testing.T) {
	path := isolateProjects(t, t.TempDir())
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\n" +
		"user: someone@example.com\nruns_bucket: fugaro-runs-proj-1234\n" +
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	open, openShared := layerBucketOpener, sharedBucketOpener
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("fugaro security paths read the project layer's bucket: every read it makes is offline")
		return nil, nil
	}
	sharedBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("fugaro security paths read the runs bucket for a shared project config: every read it makes is offline")
		return nil, nil
	}
	t.Cleanup(func() { layerBucketOpener, sharedBucketOpener = open, openShared })

	securityCheckout(t, "acme/app", securityAnchoredYAML)

	doc := securityDoc(t, "security", "paths")
	if doc.Base != "main" {
		t.Errorf("base = %q, want %q: the checkout's own file names it, and reading it costs no bucket", doc.Base, "main")
	}
	if doc.Project != "aurora" || doc.Error != "" {
		t.Errorf("project = %q, error = %q, want the file at origin/main read out of the object database", doc.Project, doc.Error)
	}
	if doc.Security.Count != len(securityFloorRows()) {
		t.Errorf("security.count = %d, want the floor's %d rows", doc.Security.Count, len(securityFloorRows()))
	}
}

// securityOwnRowsYAML is a repository whose own file writes a security: block:
// the fallback's half of the resolution (D1500). The row it writes is on no
// floor row and in no routine tree this test reads, so the only list that can
// carry it is the resolution's.
const securityOwnRowsYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"security:\n  patterns:\n    - { name: payments, match: \"internal/payments/**\", category: credentials }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// TestSecurityPathsResolvesTheRepositorysRowsAtTheBase: the repository's own
// rows are part of the list in force (SC9, D1506, D1511), they carry the source
// `repo`, and they are read at the base branch and never from the working copy
// (D1518). The working tree here writes a different row from the committed base,
// so a read of the working copy would print a list that is not the run's.
func TestSecurityPathsResolvesTheRepositorysRowsAtTheBase(t *testing.T) {
	dir := securityCheckout(t, "acme/app", securityOwnRowsYAML)
	working := strings.Replace(securityOwnRowsYAML, "internal/payments/**", "internal/rogues/**", 1)
	if working == securityOwnRowsYAML {
		t.Fatal("the fixture no longer writes the row, so this test would pass on two identical files")
	}
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(working), 0o600); err != nil {
		t.Fatal(err)
	}

	doc := securityDoc(t, "security", "paths")

	rows := securityRows(doc)
	if want := len(securityFloorRows()) + 1; len(rows) != want {
		t.Fatalf("security.patterns = %d rows, want the floor's %d and the repository's one", len(rows), want)
	}
	if doc.Security.Count != len(rows) {
		t.Errorf("security.count = %d, want the list's own %d", doc.Security.Count, len(rows))
	}
	want := securityRow{"payments", "internal/payments/**", "credentials", "repo"}
	if got := rows[len(rows)-1]; got != want {
		t.Errorf("the last row = %+v, want %+v: a repository's own block is read at the base ref and carries the source repo", got, want)
	}
	for i, row := range rows[:len(rows)-1] {
		if row != securityFloorRows()[i] {
			t.Errorf("security.patterns[%d] = %+v, want the floor's %+v: a repository's rows are added beside the floor's", i, row, securityFloorRows()[i])
		}
	}
}

// securityProjectLayerYAML is a project layer whose security: block writes one
// row (D1501), in the shape the layer cache holds: the same text the published
// layer carries.
const securityProjectLayerYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\n" +
	"security:\n  patterns:\n    - { name: settlements, match: \"internal/settlements/**\", category: credentials }\n"

// TestSecurityPathsCarriesTheProjectLayersRows: the project layer's rows are the
// list's middle half (SC9, D1506, D1511) and carry the source `project`. This
// command reads no bucket (§6.23), so the layer it reads is this machine's
// cached copy of the published one — the read `fugaro models ls` makes — and a
// machine that holds it gets the project's rows with no warning beside them.
func TestSecurityPathsCarriesTheProjectLayersRows(t *testing.T) {
	// The checkout first: it isolates the cache, so the layer is written into the
	// same cache the command will read.
	securityCheckout(t, "acme/app", securityAnchoredYAML)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: securityProjectLayerYAML,
	}); err != nil {
		t.Fatal(err)
	}

	doc := securityDoc(t, "security", "paths")

	rows := securityRows(doc)
	if want := len(securityFloorRows()) + 1; len(rows) != want {
		t.Fatalf("security.patterns = %d rows, want the floor's %d and the project layer's one", len(rows), want)
	}
	want := securityRow{"settlements", "internal/settlements/**", "credentials", "project"}
	if got := rows[len(rows)-1]; got != want {
		t.Errorf("the last row = %+v, want %+v: the cached layer's row is in force and carries the source project", got, want)
	}

	out, errOut, err := execute(t, "security", "paths")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing: this machine holds the layer the list was read from", errOut)
	}
}

// TestSecurityPathsSaysSoWhenTheProjectLayerIsNotCached: a machine that holds no
// cached copy of the project's layer prints the floor's rows and the
// repository's without the project's, which the document's shape cannot say
// (every member of it is the console's contract, §6.23). The command says so on
// stderr, in both forms of the document, and the `error` member stays what it
// was — the repository's own file, which was read.
func TestSecurityPathsSaysSoWhenTheProjectLayerIsNotCached(t *testing.T) {
	securityCheckout(t, "acme/app", securityAnchoredYAML)

	out, errOut, err := execute(t, "security", "paths")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	if !strings.Contains(errOut, "aurora") || !strings.Contains(errOut, "layer") {
		t.Errorf("stderr = %q, want the project whose layer is missing and that it is the layer", errOut)
	}
	_, jsonErr, err := execute(t, "security", "paths", "--json")
	if err != nil {
		t.Fatalf("fugaro security paths --json: %v", err)
	}
	if jsonErr != errOut {
		t.Errorf("--json stderr = %q, want %q: the missing layer is not something the document can carry", jsonErr, errOut)
	}

	doc := securityDoc(t, "security", "paths")
	if doc.Project != "aurora" || doc.Error != "" {
		t.Errorf("project = %q, error = %q, want the file read and no error member: a missing cached layer is not a read that failed", doc.Project, doc.Error)
	}
	if doc.Security.Count != len(securityFloorRows()) {
		t.Errorf("security.count = %d, want the floor's %d rows: the project's rows are not on this machine to be read", doc.Security.Count, len(securityFloorRows()))
	}
}

// securityOwnRepeatYAML is a repository that writes a row under a name its
// project layer already spends: the write D1506 makes a union drop, and the one
// a person makes by narrowing a project's row in their own file.
const securityOwnRepeatYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github, base_branch: main }\n" +
	"security:\n  patterns:\n    - { name: payments, match: \"internal/payments/legacy/**\", category: artifacts }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// securityProjectPaymentsLayerYAML is a project layer writing the name the
// fixture above repeats.
const securityProjectPaymentsLayerYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\n" +
	"security:\n  patterns:\n    - { name: payments, match: \"internal/payments/**\", category: credentials }\n"

// TestSecurityPathsSaysWhenARowIsDropped: a repository's row under a name the
// project layer already spends is not in force — the project's row is — and the
// command says so on stderr rather than leaving the difference between the
// file's author's belief and the list in force unsaid (config.SecurityIgnoredRow,
// F10, D1506). The document itself carries the project's row and the source
// `project`, and the repository's narrower glob appears nowhere in it.
func TestSecurityPathsSaysWhenARowIsDropped(t *testing.T) {
	securityCheckout(t, "acme/app", securityOwnRepeatYAML)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: securityProjectPaymentsLayerYAML,
	}); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := execute(t, "security", "paths")
	if err != nil {
		t.Fatalf("fugaro security paths: %v\n%s", err, out)
	}
	if !strings.Contains(errOut, "payments") || !strings.Contains(errOut, "not in force") {
		t.Errorf("stderr = %q, want the dropped row and that it is not in force", errOut)
	}
	if strings.Contains(out, "internal/payments/legacy/**") {
		t.Errorf("the table carries the repository's dropped row:\n%s", out)
	}

	doc := securityDoc(t, "security", "paths")
	want := securityRow{"payments", "internal/payments/**", "credentials", "project"}
	if got := securityRows(doc); len(got) != len(securityFloorRows())+1 || got[len(got)-1] != want {
		t.Errorf("the list in force = %+v, want the floor's rows and %+v: the repository's row of that name does not take the project's away", got, want)
	}
}
