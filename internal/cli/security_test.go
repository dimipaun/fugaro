package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
// all, which is the state of every repository in this slice (the key is SC7's
// and SC8's). TestSecurityPathsOnARepositoryWithNoKeyIsTheFloor holds the "no
// key" half of that claim against the text itself rather than against this
// comment.
var securityNoKeyYAML = baseBranchYAML("git: { provider: github, base_branch: main }\n")

// securityCheckout makes the working directory a checkout of repo whose base
// branch main holds yaml, and returns the checkout's directory. The command
// answers for the repository the working directory is a checkout of and reads
// the list at that checkout's base branch, so every test here starts in one.
func securityCheckout(t *testing.T, repo, yaml string) string {
	t.Helper()
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
// the command prints the list in force — in this slice the floor alone, every
// row with its source layer — and the document says which repository, which
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
// with no row rather than left out. The three paths are Check 370's own: a
// floor row's path, a routine tree the floor says nothing about, and the case
// between them — a routine tree with a listed exception — which is the shape a
// project's own list will have.
func TestSecurityPathsCheckAnswersPerPath(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	doc := securityDoc(t, "security", "paths",
		"--check", "internal/creds/state.go",
		"--check", "docs/recipes.md",
		"--check", "docs/pending-workflows/console-tests.yml")

	if len(doc.Checked) != 3 {
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

	out, _, err := execute(t, "security", "paths",
		"--check", "internal/creds/state.go",
		"--check", "docs/recipes.md",
		"--check", "docs/pending-workflows/console-tests.yml")
	if err != nil {
		t.Fatalf("fugaro security paths --check: %v\n%s", err, out)
	}
	for _, want := range []string{"internal/creds/state.go", "docs/recipes.md", "docs/pending-workflows/console-tests.yml", "credentials", "pending-workflows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the printed answers do not name %q:\n%s", want, out)
		}
	}
}

// TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath: a path the reader
// cannot place is sensitive, and no row is named for it (F22, D1512). The three
// spellings here are the ones SC1's own table refuses — absolute, escaping, and
// the empty string — and each of them means more than one path, so a list can
// never answer `no` for one.
func TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath(t *testing.T) {
	securityCheckout(t, "acme/app", securityNoKeyYAML)

	paths := []string{"/etc/passwd", "docs/../website/src/x.md", ""}
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
// the list. In this slice the list is the floor alone (D1504), so every row is
// `floor`.
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
			t.Errorf("security.patterns[%d] (%s) has source %q, want %q: no project key and no repository key exists in this slice", i, row.name, row.source, "floor")
		}
	}
	if doc.Security.Count == 0 {
		t.Fatal("the list in force is empty, so this check passed on no rows at all")
	}
}

// TestSecurityPathsReadsTheBaseAndNotTheWorktree is D1518's property, and the
// whole of this command's safety: the list in force is read at the base branch,
// so a change cannot carry the rule that governs its own review. The working
// tree here says a different project from the committed base — the one thing
// this slice reads at the ref — and the document has to carry the base's.
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
// nothing (D1504). This slice has no key at all, so what the test holds is the
// negative half — the file really writes none — and the positive one: the list
// in force is sensitivity.Floor(), row for row.
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
