package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// projectSoloCatalogName is a project override of the catalog's solo (so
// publishing it shows the "replaces the catalog's" note), with a pinned
// max_rounds to tell it apart from the catalog's own text.
const projectSoloCatalogName = "version: 1\nname: solo\nroles:\n  reviewer: coder\nsteps:\n  - review: { max_rounds: 2 }\n"

func writeRecipeFile(t *testing.T, dir, rel, text string) string {
	t.Helper()
	testutil.WriteFiles(t, dir, map[string]string{rel: text})
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// TestRecipesValidateAllowsReviewMode: mode: review parses (the format,
// Task 8) and the runner now executes it (Task 12), so fugaro recipes
// validate must call it valid, not refuse it as not-yet-runnable.
//
// Mutation (restore the ModeReview branch of recipeFileProblems): this
// test fails with "cannot be launched yet".
func TestRecipesValidateAllowsReviewMode(t *testing.T) {
	dir := t.TempDir()
	path := writeRecipeFile(t, dir, "reviewonly.yaml", "version: 1\nname: reviewonly\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n")
	out, _, err := execute(t, "recipes", "validate", path)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// TestRecipesValidateAllowsCheckStep: a check step recipe is launchable now
// (Task 11's runner gates the run on it), so fugaro recipes validate must
// call it valid, not refuse it as not-yet-runnable.
//
// Mutation (restore the StepCheck branch of recipe.NotYetRunnable): this
// test fails with "cannot be launched yet".
func TestRecipesValidateAllowsCheckStep(t *testing.T) {
	dir := t.TempDir()
	path := writeRecipeFile(t, dir, "gated.yaml", "version: 1\nname: gated\nsteps:\n  - check: { command: test }\n  - review: {}\n")
	out, _, err := execute(t, "recipes", "validate", path)
	if err != nil || strings.Contains(out, "cannot be launched yet") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// TestRecipesPublishAllowsReviewMode: the same used to be refused for
// publishing a project recipe; mode: review is launchable now (Task 12), so
// it reaches the bucket like any other valid recipe.
//
// Mutation (restore the ModeReview branch of recipeFileProblems): this
// test fails with "cannot be launched yet", and nothing is published.
func TestRecipesPublishAllowsReviewMode(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "reviewonly.yaml", "version: 1\nname: reviewonly\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n")
	if _, _, err := execute(t, "recipes", "publish", path); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("reviewonly")))); err != nil {
		t.Fatalf("reviewonly.yaml was not published: %v", err)
	}
}

// TestRecipesPublishAllowsCheckStep: a check step recipe is launchable now
// (Task 11's runner gates the run on it), so fugaro recipes publish must
// let it reach the bucket.
//
// Mutation (restore the StepCheck branch of recipe.NotYetRunnable): this
// test fails with "cannot be launched yet", and nothing is published.
func TestRecipesPublishAllowsCheckStep(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "gated.yaml", "version: 1\nname: gated\nsteps:\n  - check: { command: test }\n  - review: {}\n")
	if _, _, err := execute(t, "recipes", "publish", path); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("gated")))); err != nil {
		t.Fatalf("gated.yaml was not published: %v", err)
	}
}

func TestRecipesValidate(t *testing.T) {
	dir := t.TempDir()
	good := writeRecipeFile(t, dir, "team.yaml", projectTeam)
	out, _, err := execute(t, "recipes", "validate", good)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good: %s, %v", out, err)
	}
	bad := writeRecipeFile(t, dir, "bad.yaml", "version: 1\nname: bad\nmodel: claude-opus-5\nsteps:\n  - review: {}\n")
	out, _, err = execute(t, "recipes", "validate", "--json", bad)
	var doc struct {
		Valid    bool             `json:"valid"`
		Problems []recipe.Problem `json:"problems"`
	}
	if ExitCode(err) != ExitUserError || json.Unmarshal([]byte(out), &doc) != nil || doc.Valid || len(doc.Problems) != 1 ||
		!strings.Contains(doc.Problems[0].Message, "never names a model") {
		t.Fatalf("bad: %s, %v", out, err)
	}
}

// Review Focus 4.
func TestRecipesValidateNameMismatch(t *testing.T) {
	path := writeRecipeFile(t, t.TempDir(), ".fugaro/recipes/mine.yaml", "version: 1\nname: claude-solo\nsteps:\n  - review: {}\n")
	_, _, err := execute(t, "recipes", "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	out, _, _ := execute(t, "recipes", "validate", path)
	if !strings.Contains(out, "the file name and the name must agree") {
		t.Fatalf("out = %s", out)
	}
}

func TestRecipesPublishRefusedInAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team")))); err == nil {
		t.Fatal("published from an agent session")
	}
}

func TestRecipesPublishWritesObject(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "solo.yaml", projectSoloCatalogName)
	out, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(out, "published solo to gs://fugaro-runs-proj-1234/fugaro/recipes/solo.yaml") ||
		!strings.Contains(errOut, "replaces the catalog's solo") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
	got, err := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("solo"))))
	if err != nil || string(got) != projectSoloCatalogName {
		t.Fatalf("object = %q, %v", got, err)
	}
}

func TestRecipesPublishShowsWhatItReplaces(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(errOut, "replacing the project's team") ||
		!strings.Contains(errOut, "- ") || !strings.Contains(errOut, "+ ") || !strings.Contains(errOut, "max_rounds: 3") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

func TestRecipesPublishNeedsFileNamedAfterRecipe(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "mine.yaml", projectSolo)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("claude-solo")))); err == nil {
		t.Fatal("published under a mismatched file name")
	}
}

func TestRecipesPublishRefusesInvalidAndCustomBucket(t *testing.T) {
	f := newCloudFixture(t) // custom bucket name
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	if _, _, err := execute(t, "recipes", "publish", path); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("custom bucket: %v", err)
	}
	projectRecipesFixture(t, f, nil)
	bad := writeRecipeFile(t, t.TempDir(), "bad.yaml", "version: 1\nname: bad\nsteps: []\n")
	if _, _, err := execute(t, "recipes", "publish", bad); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("invalid: %v", err)
	}
}

func TestRecipesLsLayers(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo, "team": projectTeam})
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/team.yaml", repoTeam)
	t.Chdir(dir)
	out, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []recipeRow `json:"recipes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	want := []recipeRow{
		{Name: "team", Source: "repo"},
		{Name: "claude-solo", Source: "project"},
		{Name: "team", Source: "project", ShadowedBy: "repo"},
		{Name: "default", Source: "catalog"},
		{Name: "lint-fix", Source: "catalog"},
		{Name: "pick", Source: "catalog"},
		{Name: "premium", Source: "catalog"},
		{Name: "review-only", Source: "catalog"},
		{Name: "run-digest", Source: "catalog"},
		{Name: "solo", Source: "catalog"},
		{Name: "standard", Source: "catalog"},
		{Name: "test-and-fix", Source: "catalog"},
	}
	if len(doc.Recipes) != len(want) {
		t.Fatalf("rows = %+v", doc.Recipes)
	}
	for i, w := range want {
		g := doc.Recipes[i]
		if g.Name != w.Name || g.Source != w.Source || g.ShadowedBy != w.ShadowedBy {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

// TestRecipesLsVerbose: --verbose prints each recipe's use_when and steps on
// stdout, and --json always carries them (unlike plain ls, Task 14 step 1).
func TestRecipesLsVerbose(t *testing.T) {
	newCloudFixture(t)
	out, _, err := execute(t, "recipes", "ls", "--verbose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "use when: Typical feature or bug-fix work that needs a real review.") ||
		!strings.Contains(out, "steps: first_line, review (bounce: first_line)") {
		t.Fatalf("out = %q", out)
	}
	// Plain ls (no --verbose) prints neither line.
	plain, _, err := execute(t, "recipes", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "use when:") || strings.Contains(plain, "steps:") {
		t.Fatalf("plain ls = %q", plain)
	}
	// --json always carries use_when and steps, with or without --verbose.
	jsonOut, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []recipeRow `json:"recipes"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	var standard recipeRow
	for _, r := range doc.Recipes {
		if r.Name == "standard" {
			standard = r
		}
	}
	if standard.UseWhen == "" || len(standard.Steps) != 2 || standard.Steps[0].Kind != "first_line" ||
		standard.Steps[1].Kind != "review" || !standard.Steps[1].Bounce {
		t.Fatalf("standard row = %+v", standard)
	}
	if !standard.Launchable {
		t.Fatalf("standard launchable = %+v", standard)
	}
}

// TestWriteRecipesTableEscapesInjection: name, description and use_when are
// untrusted (a project recipe is anyone-with-bucket-write's text), and
// recipe.Parse's own refusal of control, bidi and invisible characters is
// one layer, not the only one this relies on (a pre-existing stored object,
// or any future caller that does not route through Parse, must still be
// safe to print). ESC, an OSC 8 terminal hyperlink, an 8-bit C1 CSI, a bidi
// override and a newline must each come out escaped, never as the raw byte.
//
// Mutation (run, restore): drop the pluginwire.Printable call on desc and
// r.UseWhen in writeRecipesTable (oneLine alone), and this test fails: the
// escaped \uXXXX markers this asserts on are gone (oneLine drops or masks
// the same bytes a different way, which is still safe but not what this
// pins).
func TestWriteRecipesTableEscapesInjection(t *testing.T) {
	const (
		esc  = "\x1b"
		osc8 = esc + "]8;;http://evil.example\x07click\x1b]8;;\x07"
	)
	rows := []recipeRow{{
		Name:        "x",
		Source:      "catalog",
		Description: "d" + esc + "[31mred",
		UseWhen:     "use" + osc8 + "when\u009bCSI\u202ebidi\nline",
		Steps:       []stepView{{Kind: "review"}},
	}}
	var buf bytes.Buffer
	if err := writeRecipesTable(&buf, rows, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07\u009b\u202e") {
		t.Fatalf("a raw dangerous byte reached the table: %q", out)
	}
	for _, want := range []string{"\\u001b", "\\u0007", "\\u009b", "\\u202e"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the escaped marker %q in %q", want, out)
		}
	}
}

// TestWriteRecipesTableKeepsColumnsAligned: a --verbose use-when/steps
// block has no tabs of its own (fugaro.yaml review's finding #4), which
// breaks the tabwriter's column block at that point unless those lines are
// tab-terminated like every other row: the SOURCE column of the row after
// a verbose block must still line up with the row before it.
func TestWriteRecipesTableKeepsColumnsAligned(t *testing.T) {
	rows := []recipeRow{
		{Name: "aaa", Source: "catalog", Description: "short desc", UseWhen: "when", Steps: []stepView{{Kind: "review"}}},
		{Name: "bb", Source: "catalog", Description: "d2", Steps: []stepView{{Kind: "review"}}},
	}
	var buf bytes.Buffer
	if err := writeRecipesTable(&buf, rows, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("lines = %q", lines)
	}
	col := func(line string) int { return strings.Index(line, "catalog") }
	first, second := col(lines[1]), col(lines[4])
	if first < 0 || second < 0 || first != second {
		t.Fatalf("SOURCE column misaligned: row 1 at %d, row 2 at %d:\n%s", first, second, buf.String())
	}
}

// TestRecipesLsVerboseDoesNotTruncateUseWhen: a use_when at its own 300
// byte parser limit must print in full under --verbose, not cut at
// pluginwire.MaxPrintable's unrelated 200-rune default.
//
// Mutation (run, restore): change writeRecipesTable's PrintableN(r.UseWhen,
// recipe.MaxUseWhen) back to Printable(r.UseWhen), and this test fails: the
// 300-byte use_when is cut at 200 runes.
func TestRecipesLsVerboseDoesNotTruncateUseWhen(t *testing.T) {
	f := newCloudFixture(t)
	long := strings.Repeat("x", recipe.MaxUseWhen)
	projectRecipesFixture(t, f, map[string]string{"longuw": "version: 1\nname: longuw\nuse_when: \"" + long + "\"\nsteps:\n  - review: {}\n"})
	out, _, err := execute(t, "recipes", "ls", "--verbose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "use when: "+long) {
		t.Fatalf("use_when was cut: %q", out)
	}
	if strings.Contains(out, "(truncated)") {
		t.Fatalf("use_when (at its own 300-byte limit) was truncated: %q", out)
	}
}

// TestRecipesLsNotesMissingUseWhen: design generic-tool.md §3.2 ("A custom
// recipe without use_when gets a recipes ls note"). A catalog recipe
// (default) is exempt: it is not custom, and every other catalog entry is
// required to have one (TestCatalogIsTheStarterSet).
func TestRecipesLsNotesMissingUseWhen(t *testing.T) {
	newCloudFixture(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/no-use-when.yaml", "version: 1\nname: no-use-when\nsteps:\n  - review: {}\n")
	t.Chdir(dir)
	_, errOut, err := execute(t, "recipes", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "repo recipe no-use-when has no use_when; the routing skill matches on use_when, add one") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(errOut, "default has no use_when") {
		t.Fatalf("the catalog's own default was wrongly noted: %q", errOut)
	}
}

// TestRecipesLsJSONEveryCatalogRecipeLaunchable: the routing skill reads
// fugaro recipes ls --json (design generic-tool.md §3.2); a recipe it
// could pick and then have fugaro run refuse would be a bug (G14, G15).
// Task 11 (check steps) and Task 12 (mode: review) close the last two such
// gaps: every catalog recipe, review-only and pick included, is launchable
// now.
//
// Mutation (restore `r.Launchable = r.Problem == "" && recipe.NotYetRunnable(rcp) == ""`
// with mode: review treated as a gap again): review-only and pick come
// back launchable=false, failing the loop below.
func TestRecipesLsJSONEveryCatalogRecipeLaunchable(t *testing.T) {
	newCloudFixture(t)
	out, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []recipeRow `json:"recipes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	byName := map[string]recipeRow{}
	for _, r := range doc.Recipes {
		byName[r.Name] = r
	}
	for _, name := range recipe.CatalogNames() {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("no row for %s", name)
		}
		if !r.Launchable {
			t.Errorf("%s: launchable=%v, want true", name, r.Launchable)
		}
	}
}

func TestRecipesShowSource(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo})
	out, _, err := execute(t, "recipes", "show", "claude-solo")
	if err != nil || !strings.HasPrefix(out, "recipe claude-solo from project (gs://fugaro-runs-proj-1234/fugaro/recipes/claude-solo.yaml)\nsha256 "+recipe.Sum([]byte(projectSolo))+"\n\n") ||
		!strings.HasSuffix(out, projectSolo) {
		t.Fatalf("out = %q, %v", out, err)
	}
}

func TestRecipesShowPrintsNote(t *testing.T) {
	newCloudFixture(t) // custom runs bucket: project recipes are not read
	out, errOut, err := execute(t, "recipes", "show", "default")
	if err != nil || !strings.HasPrefix(out, "recipe default from catalog") || !strings.Contains(errOut, "note: project recipes need the default runs bucket name") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

// The agent check comes before any cloud access: with no project at all the
// refusal is still the agent's, not "no project".
func TestRecipesPublishAgentCheckComesFirst(t *testing.T) {
	isolateProjects(t, t.TempDir())
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
}

// What a replacement shows is exactly the lines that change.
func TestRecipesPublishDiffIsExact(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil {
		t.Fatal(err)
	}
	head := "replacing the project's team (sha256 " + recipe.Sum([]byte(projectTeam)) + " -> " + recipe.Sum([]byte(repoTeam)) + "):\n"
	_, body, ok := strings.Cut(errOut, head)
	if !ok || body != lineDiff(projectTeam, repoTeam) || body != "  +   - review: { max_rounds: 3 }\n  -   - review: {}\n" {
		t.Fatalf("stderr %q", errOut)
	}
}

// A publisher that wins the race between the read and the write is not
// overwritten: the second one is refused and the first one's content stays.
func TestRecipesPublishRefusesAConcurrentPublisher(t *testing.T) {
	const theirs = "version: 1\nname: team\nsteps:\n  - review: { max_rounds: 9 }\n"
	for _, existing := range []bool{true, false} {
		f := newCloudFixture(t)
		recipes := map[string]string{}
		if existing {
			recipes["team"] = projectTeam
		}
		projectRecipesFixture(t, f, recipes)
		noAgentSession(t)
		old := recipePublishRace
		recipePublishRace = func(ctx context.Context, b *blobx.Bucket, key string) {
			if err := b.Bucket.WriteAll(ctx, key, []byte(theirs), nil); err != nil {
				t.Error(err)
			}
		}
		path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
		_, _, err := execute(t, "recipes", "publish", path)
		recipePublishRace = old
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") || !strings.Contains(err.Error(), "another publisher") {
			t.Fatalf("existing=%v: err = %v", existing, err)
		}
		got, rerr := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team"))))
		if rerr != nil || string(got) != theirs {
			t.Fatalf("existing=%v: object = %q, %v", existing, got, rerr)
		}
	}
}

func TestRecipesPublishRefusesAnOversizedObject(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": strings.Repeat("# x\n", 6000)})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "delete it by hand") {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team"))))
	if len(got) != 24000 {
		t.Fatalf("the oversized object was replaced: %d bytes", len(got))
	}
}

func TestRecipesPublishAndValidateRefuseAFifo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "team.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	noAgentSession(t)
	if _, _, err := execute(t, "recipes", "validate", fifo); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("validate: %v", err)
	}
	newCloudFixture(t)
	if _, _, err := execute(t, "recipes", "publish", fifo); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("publish: %v", err)
	}
}

// fugaro recipes validate|publish FILE must refuse a symlink, not follow
// it: a symlink could name any file the user running the command can read.
//
// Mutation (run, restore): revert readRecipeFile to open the path
// directly (os.OpenFile without O_NOFOLLOW, as it did before), and this
// test fails: validate reads the file through the symlink and reports it
// valid.
func TestRecipesValidateRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	good := writeRecipeFile(t, dir, "team.yaml", projectTeam)
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "recipes", "validate", link); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
}

// validate and publish agree on the file name rule, wherever it is run.
func TestRecipesValidateStemRule(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "mine.yaml", projectTeam) // named team inside
	t.Chdir(dir)
	out, _, err := execute(t, "recipes", "validate", "mine.yaml")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "the file name and the name must agree") {
		t.Fatalf("out %q, err %v", out, err)
	}
	// Under .fugaro/recipes with a relative path, from inside it.
	writeRecipeFile(t, dir, ".fugaro/recipes/mine.yaml", projectTeam)
	t.Chdir(filepath.Join(dir, ".fugaro", "recipes"))
	if out, _, err = execute(t, "recipes", "validate", "mine.yaml"); ExitCode(err) != ExitUserError || !strings.Contains(out, "must agree") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// A bidi override in a description is refused at parse (hasControlChar, a
// field the generic-tool task 14 review added this check to), so unlike
// before it never reaches ls or show for them to escape; an escape sequence
// in an old object's YAML comment (recipes publish's replacement diff) is
// not a recipe field Parse looks at, so it is still displayed raw-then-
// escaped, not refused.
const (
	bidiRecipe = "version: 1\nname: team\ndescription: \"evil \\u202e txt\"\nsteps:\n  - review: {}\n"
	escRecipe  = "version: 1\nname: team\n# \x1b[2Jclear\nsteps:\n  - review: {}\n"
)

func TestRecipesOutputIsPrintable(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": bidiRecipe})
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "recipes", "ls")
	if err != nil || strings.ContainsRune(out, '\u202e') || !strings.Contains(out, "INVALID: description") || !strings.Contains(out, "must not contain control characters, bidi overrides, invisible characters or line/paragraph separators") {
		t.Fatalf("ls: out %q, err %v", out, err)
	}
	_, _, err = execute(t, "recipes", "show", "team")
	if ExitCode(err) != ExitUserError || strings.ContainsRune(err.Error(), '\u202e') || !strings.Contains(err.Error(), "must not contain control characters, bidi overrides, invisible characters or line/paragraph separators") {
		t.Fatalf("show: err %v", err)
	}
	writeBucketFile(t, f, recipe.ObjectKey("team"), escRecipe)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || strings.ContainsRune(errOut, 0x1b) || !strings.Contains(errOut, "\\u001b[2Jclear") {
		t.Fatalf("publish: stderr %q, err %v", errOut, err)
	}
}

// Decision D9: ls and show read the bucket, whatever a fresh cache holds.
func TestRecipesLsAndShowRefreshTheCache(t *testing.T) {
	for _, args := range [][]string{{"recipes", "show", "team"}, {"recipes", "ls"}} {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
		testutil.IsolateGit(t)
		t.Chdir(t.TempDir())
		stale := localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: repoTeam}
		if err := localcfg.SaveRecipeCache(os.Getenv, "aurora", "team", stale); err != nil {
			t.Fatal(err)
		}
		out, _, err := execute(t, args...)
		if err != nil || (args[1] == "show" && !strings.HasSuffix(out, projectTeam)) {
			t.Fatalf("%v: out %q, err %v", args, out, err)
		}
		if e, ok := localcfgRecipeCached(t, "team"); !ok || e.YAML != projectTeam {
			t.Fatalf("%v: cache = %+v, %v", args, e, ok)
		}
	}
}

func TestRecipesLsReportsSkippedFiles(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	writeBucketFile(t, f, recipe.ObjectPrefix+"Bad_Name.yaml", projectTeam)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/notes.txt", "x")
	t.Chdir(dir)
	_, errOut, err := execute(t, "recipes", "ls")
	if err != nil || !strings.Contains(errOut, "note: skipped .fugaro/recipes/notes.txt") || !strings.Contains(errOut, "note: skipped gs://fugaro-runs-proj-1234/"+recipe.ObjectPrefix+"Bad_Name.yaml") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}
