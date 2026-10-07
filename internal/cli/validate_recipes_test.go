package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const validateYAML = "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n%s" +
	"workflows:\n  app: { base: go, commands: { build: go build ./..., test: go test ./... } }\n"

func validateDir(t *testing.T, agentLines string, files map[string]string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	files["fugaro.yaml"] = strings.Replace(validateYAML, "%s", agentLines, 1)
	testutil.WriteFiles(t, dir, files)
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

func has(ps []config.Problem, path, sub string) bool {
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, sub) {
			return true
		}
	}
	return false
}

// Review Focus 4.
func TestRecipeDirNameMismatch(t *testing.T) {
	doc, err := validateDir(t, "", map[string]string{".fugaro/recipes/mine.yaml": "version: 1\nname: claude-solo\nsteps:\n  - review: {}\n"})
	if ExitCode(err) != ExitUserError || !has(doc.Problems, ".fugaro/recipes/mine.yaml", "the file name and the name must agree") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

func TestRecipeDirInvalidRecipe(t *testing.T) {
	doc, err := validateDir(t, "", map[string]string{".fugaro/recipes/loop.yaml": "version: 1\nname: loop\nsteps:\n  - first_line: {}\n"})
	if ExitCode(err) != ExitUserError || !has(doc.Problems, ".fugaro/recipes/loop.yaml", "must end with a review step") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

func TestRecipeDirValid(t *testing.T) {
	doc, err := validateDir(t, "  recipe: loop\n", map[string]string{".fugaro/recipes/loop.yaml": "version: 1\nname: loop\nsteps:\n  - review: {}\n"})
	if err != nil || !doc.Valid || has(doc.Warnings, "agent.recipe", "neither in") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

// Review Focus 1.
func TestValidateWarnsAgentRecipeNeeds050(t *testing.T) {
	doc, err := validateDir(t, "  recipe: claude-solo\n", map[string]string{})
	if err != nil || !doc.Valid || !has(doc.Warnings, "agent.recipe", "fugaro before 0.5.0 refuses this key") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
	doc, _ = validateDir(t, "", map[string]string{})
	if has(doc.Warnings, "agent.recipe", "") {
		t.Fatalf("a file without agent.recipe is warned: %+v", doc.Warnings)
	}
}

func TestRecipeDirUnknownAgentRecipeWarns(t *testing.T) {
	doc, err := validateDir(t, "  recipe: team\n", map[string]string{})
	if err != nil || !has(doc.Warnings, "agent.recipe", "must be a project recipe") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

// The reviewer of a recipe that maps it to the coder is the coder: an
// off-list agent.models.reviewer is never used, so it is no problem.
func TestValidateAliasesReviewerForRecipe(t *testing.T) {
	const agent = "  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5, reviewer: claude-opus-5-5 }"
	const ceiling = "budget: { allowed_models: [claude-sonnet-5-5, claude-haiku-4-5] }\n"
	// Without a recipe the reviewer is off the list.
	path := budgetProject(t, ceiling, agent)
	out, err := validateProblems(t, path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.reviewer") {
		t.Fatalf("no recipe: exit %d: %s", ExitCode(err), out)
	}
	// With claude-solo (reviewer: coder) the reviewer is the coder.
	path = budgetProject(t, ceiling, agent+"\n  recipe: claude-solo")
	if out, err = validateProblems(t, path); err != nil || strings.Contains(out, "agent.models.reviewer") {
		t.Fatalf("catalog recipe: %v: %s", err, out)
	}
	// A recipe of the checkout does the same.
	path = budgetProject(t, ceiling, agent+"\n  recipe: mine")
	testutil.WriteFiles(t, filepath.Dir(path), map[string]string{".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nroles:\n  reviewer: coder\nsteps:\n  - review: {}\n"})
	if out, err = validateProblems(t, path); err != nil || strings.Contains(out, "agent.models.reviewer") {
		t.Fatalf("repo recipe: %v: %s", err, out)
	}
	// A recipe that does not alias leaves the problem.
	path = budgetProject(t, ceiling, agent+"\n  recipe: default")
	if out, err = validateProblems(t, path); ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.reviewer") {
		t.Fatalf("default recipe: exit %d: %s", ExitCode(err), out)
	}
}

func TestRecipeDirRefusesSymlinks(t *testing.T) {
	// A symlinked recipe file.
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.yaml")
	if err := os.WriteFile(outside, []byte("version: 1\nname: loop\nsteps:\n  - review: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".fugaro", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".fugaro", "recipes", "loop.yaml")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": strings.Replace(validateYAML, "%s", "", 1)})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("%q: %v", out, jerr)
	}
	if ExitCode(err) != ExitUserError || !has(doc.Problems, ".fugaro/recipes/loop.yaml", "not a regular file") {
		t.Fatalf("file link: doc = %+v, err = %v", doc, err)
	}
	// A symlinked recipes directory.
	dir2 := t.TempDir()
	real := filepath.Join(t.TempDir(), "recipes")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "loop.yaml"), []byte("version: 1\nname: loop\nsteps:\n  - review: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir2, ".fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir2, ".fugaro", "recipes")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir2, map[string]string{"fugaro.yaml": strings.Replace(validateYAML, "%s", "", 1)})
	out, _, err = execute(t, "validate", "--json", filepath.Join(dir2, "fugaro.yaml"))
	doc = validateOutput{}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("%q: %v", out, jerr)
	}
	if ExitCode(err) != ExitUserError || !has(doc.Problems, ".fugaro/recipes/loop.yaml", "is not a directory") {
		t.Fatalf("dir link: doc = %+v, err = %v", doc, err)
	}
}

func TestRecipeDirNonYAMLAndBadName(t *testing.T) {
	doc, err := validateDir(t, "", map[string]string{
		".fugaro/recipes/notes.txt":     "x",
		".fugaro/recipes/Bad_Name.yaml": "version: 1\nname: bad\nsteps:\n  - review: {}\n",
	})
	if ExitCode(err) != ExitUserError || !has(doc.Warnings, ".fugaro/recipes/notes.txt", "is not a .yaml file") ||
		!has(doc.Problems, ".fugaro/recipes/Bad_Name.yaml", "not a recipe name") {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

func TestValidateProjectRecipeAnnotatesReviewerProblems(t *testing.T) {
	const agent = "  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5, reviewer: claude-opus-5-5 }"
	const ceiling = "budget: { allowed_models: [claude-sonnet-5-5, claude-haiku-4-5] }\n"
	path := budgetProject(t, ceiling, agent+"\n  recipe: team")
	out, err := validateProblems(t, path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.reviewer") || !strings.Contains(out, "(if project recipe team maps reviewer to coder, this key is ignored; remove it)") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// A recipe found locally gets no such hint.
	path = budgetProject(t, ceiling, agent+"\n  recipe: default")
	if out, err = validateProblems(t, path); !strings.Contains(out, "agent.models.reviewer") || strings.Contains(out, "if project recipe") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
}

func TestValidateWarnsCatalogRecipeMayBeShadowed(t *testing.T) {
	doc, _ := validateDir(t, "  recipe: claude-solo\n", map[string]string{})
	if !has(doc.Warnings, "agent.recipe", "a project recipe of this name takes precedence") {
		t.Fatalf("warnings = %+v", doc.Warnings)
	}
	doc, _ = validateDir(t, "  recipe: claude-solo\n", map[string]string{".fugaro/recipes/claude-solo.yaml": "version: 1\nname: claude-solo\nsteps:\n  - review: {}\n"})
	if has(doc.Warnings, "agent.recipe", "takes precedence") {
		t.Fatalf("a repository recipe wins over any project one: %+v", doc.Warnings)
	}
}
