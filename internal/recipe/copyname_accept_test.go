package recipe_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// The name checks a recipe passes through live in other packages — the task's
// spec and the recipe cache read a name the parser has already accepted — and
// the schema is the format's published half of the same rule. R19: every one of
// them accepts a copy name and still refuses any other dotted name, so a
// migrated copy is stored, embedded, cached and edited like the name it copies.
// The four sub-tests sit in one file because the rule is one rule: a name check
// added anywhere is a call site added to recipe.IsNameOrCopy.

// accepted are the names every check here must accept: a copy name the grammar
// derives, and the longest one it admits (recipe.MaxCopyName is 96).
var accepted = []string{
	recipe.CopyName("review", "github", "acme/app-web"),
	strings.Repeat("n", 40) + "." + strings.Repeat("m", 46) + "-01234567",
}

// refused are dotted names no check here may accept: a copy spelled wrong in
// each of the ways an author gets it wrong. The last two are the bounds — a
// name half over 40, and a copy name over recipe.MaxCopyName.
var refused = []string{
	"review.acme-app-web",               // no hash
	"review.acme-app-web-1234567",       // seven hex
	"review.acme-app-web-123456789",     // nine hex
	"review.acme-app-web-1234567g",      // not hex
	"review.acme_app-web-12345678",      // outside the readable half's charset
	"review..acme-app-web-12345678",     // two dots
	"Review.acme-app-web-12345678",      // upper case
	"review.acme-app-web-12345678.yaml", // a file name's extension
	strings.Repeat("n", 41) + ".acme-app-web-12345678",
	strings.Repeat("n", 40) + "." + strings.Repeat("m", 47) + "-01234567",
}

// recipeText is a minimal recipe named name, written the way a copy's file is:
// the name is a plain scalar here because every name in the tables is made of
// bytes one holds.
func recipeText(name string) string {
	return "version: 1\nname: " + name + "\nsteps:\n  - review: {}\n"
}

func TestEveryNameCheckAcceptsACopy(t *testing.T) {
	t.Run("parse", func(t *testing.T) {
		for _, name := range accepted {
			if _, ps := recipe.Parse([]byte(recipeText(name))); len(ps) != 0 {
				t.Errorf("recipe.Parse(%q): %v, want no problems", name, ps)
			}
		}
		for _, name := range refused {
			_, ps := recipe.Parse([]byte(recipeText(name)))
			if len(ps) != 1 || ps[0].Path != "name" || !strings.Contains(ps[0].Message, "must be a recipe name") {
				t.Errorf("recipe.Parse(%q): problems = %v, want one name refusal", name, ps)
			}
		}
	})
	t.Run("task", func(t *testing.T) {
		spec := func(name string) *task.Spec {
			text := recipeText(name)
			return &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x",
				Recipe: &task.Recipe{Name: name, Source: "project", SHA256: recipe.Sum([]byte(text)), YAML: text}}
		}
		for _, name := range accepted {
			if err := spec(name).Validate(); err != nil {
				t.Errorf("Spec.Validate(%q): %v, want none", name, err)
			}
		}
		for _, name := range refused {
			if err := spec(name).Validate(); err == nil || !strings.Contains(err.Error(), "is not a recipe name") {
				t.Errorf("Spec.Validate(%q): err = %v, want a name refusal", name, err)
			}
		}
	})
	t.Run("cache", func(t *testing.T) {
		dir := t.TempDir()
		getenv := func(k string) string {
			if k == "XDG_CACHE_HOME" {
				return dir
			}
			return ""
		}
		e := localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: recipeText("review")}
		for _, name := range accepted {
			if err := localcfg.SaveRecipeCache(getenv, "aurora", name, e); err != nil {
				t.Errorf("SaveRecipeCache(%q): %v, want none", name, err)
			} else if _, ok := localcfg.LoadRecipeCache(getenv, "aurora", name); !ok {
				t.Errorf("LoadRecipeCache(%q) missed the entry just written", name)
			}
		}
		for _, name := range refused {
			if err := localcfg.SaveRecipeCache(getenv, "aurora", name, e); err == nil {
				t.Errorf("SaveRecipeCache(%q) wrote an entry", name)
			}
			if _, ok := localcfg.LoadRecipeCache(getenv, "aurora", name); ok {
				t.Errorf("LoadRecipeCache(%q) hit", name)
			}
		}
	})
}

func TestRecipeSchemaAcceptsACopy(t *testing.T) {
	sch := compileRecipeSchema(t)
	for _, name := range accepted {
		if err := sch.Validate(recipeInstance(t, recipeText(name))); err != nil {
			t.Errorf("the schema refuses %q, which recipe.Parse accepts: %v", name, err)
		}
	}
	for _, name := range refused {
		if err := sch.Validate(recipeInstance(t, recipeText(name))); err == nil {
			t.Errorf("the schema accepts %q, which recipe.Parse refuses", name)
		}
	}
	// The pattern is the same grammar the Go check spells, read by editors and
	// by every reader of the published format, so the two are held to it name
	// by name: a disagreement is a recipe an editor marks invalid, or one a
	// reader accepts and the parser refuses.
	pattern := regexp.MustCompile(schemaNamePattern(t))
	for _, name := range slices.Concat(accepted, refused) {
		if got, want := pattern.MatchString(name), recipe.IsNameOrCopy(name); got != want {
			t.Errorf("the schema's name pattern says %v of %q and recipe.IsNameOrCopy says %v", got, name, want)
		}
	}
}

// compileRecipeSchema loads schemas/recipe.schema.json under its $id, as a
// reader of the published format does.
func compileRecipeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// schemaNamePattern is the pattern the schema puts on a recipe's name.
func schemaNamePattern(t *testing.T) string {
	t.Helper()
	var raw struct {
		Properties map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaBytes(t), &raw); err != nil {
		t.Fatal(err)
	}
	p := raw.Properties["name"].Pattern
	if p == "" {
		t.Fatal("the schema puts no pattern on a recipe's name")
	}
	return p
}

func schemaBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "recipe.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// recipeInstance is a recipe as the validator's JSON value model holds it.
func recipeInstance(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return inst
}
