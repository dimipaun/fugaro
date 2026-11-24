package recipe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The name rule is R19: every check that reads a recipe name accepts a copy
// name and still refuses any other dotted name, so a migrated copy is stored,
// embedded, cached and edited like the name it copies. The checks live in three
// packages — this one's parser, the task's spec and the recipe cache — and the
// schema is the format's published half of the same rule. Each package's test
// of its own half is named TestEveryNameCheckAcceptsACopy, so the whole rule is
// one -run away; the parser's is here, with the tables of names every half
// agrees about.

// maxCopy is the longest copy name the grammar admits (MaxCopyName): the
// longest recipe name, and a repository long enough that CopyName keeps all 46
// characters of the readable half.
var maxCopy = CopyName(strings.Repeat("n", 40), "github", "acme/"+strings.Repeat("m", 60))

// copyAcceptedNames are the names every check must accept: a copy name the
// grammar derives, and the longest one it admits.
var copyAcceptedNames = []string{
	CopyName("review", "github", "acme/app-web"),
	maxCopy,
}

// copyRefusedNames are dotted names no check may accept: a copy spelled wrong
// in each of the ways an author gets it wrong. The last two are the bounds — a
// name half over 40, and a copy name over MaxCopyName.
var copyRefusedNames = []string{
	"review.acme-app-web",               // no hash
	"review.acme-app-web-1234567",       // seven hex
	"review.acme-app-web-123456789",     // nine hex
	"review.acme-app-web-1234567g",      // not hex
	"review.acme_app-web-12345678",      // outside the readable half's charset
	"review..acme-app-web-12345678",     // two dots
	"Review.acme-app-web-12345678",      // upper case
	"review.acme-app-web-12345678.yaml", // a file name's extension
	strings.Repeat("n", 41) + ".acme-app-web-12345678",
	maxCopy + "m",
}

// copyRecipeText is a minimal recipe named name, written the way a copy's file
// is: the name is a plain scalar here because every name in the tables is made
// of bytes one holds.
func copyRecipeText(name string) string {
	return "version: 1\nname: " + name + "\nsteps:\n  - review: {}\n"
}

func TestEveryNameCheckAcceptsACopy(t *testing.T) {
	// The table above is only the bounds if the longest accepted name really is
	// the longest one: a MaxCopyName that grew would leave the over-bound case
	// short of the bound and refusing nothing.
	if len(maxCopy) != MaxCopyName {
		t.Fatalf("maxCopy is %d characters, want MaxCopyName (%d)", len(maxCopy), MaxCopyName)
	}
	t.Run("parse", func(t *testing.T) {
		for _, name := range copyAcceptedNames {
			if _, ps := Parse([]byte(copyRecipeText(name))); len(ps) != 0 {
				t.Errorf("Parse(%q): %v, want no problems", name, ps)
			}
		}
		for _, name := range copyRefusedNames {
			_, ps := Parse([]byte(copyRecipeText(name)))
			if len(ps) != 1 || ps[0].Path != "name" || !strings.Contains(ps[0].Message, "must be a recipe name") {
				t.Errorf("Parse(%q): problems = %v, want one name refusal", name, ps)
			}
		}
	})
	// The refusal names the copy grammar, since a name that is neither a recipe
	// name nor a copy of one is most often a copy someone spelled wrong.
	if _, ps := Parse([]byte(copyRecipeText("review.acme-app-web-1234567"))); len(ps) != 1 || !strings.Contains(ps[0].Message, "copy") {
		t.Errorf("the name refusal does not name the copy grammar: %v", ps)
	}
}

func TestRecipeSchemaAcceptsACopy(t *testing.T) {
	sch := compileRecipeSchema(t)
	for _, name := range copyAcceptedNames {
		if err := sch.Validate(schemaRecipeInstance(t, name)); err != nil {
			t.Errorf("the schema refuses %q, which Parse accepts: %v", name, err)
		}
	}
	for _, name := range copyRefusedNames {
		if err := sch.Validate(schemaRecipeInstance(t, name)); err == nil {
			t.Errorf("the schema accepts %q, which Parse refuses", name)
		}
	}
	// The pattern is the same grammar the Go check spells, read by editors and
	// by every reader of the published format, so the two are held to it name
	// by name: a disagreement is a recipe an editor marks invalid, or one a
	// reader accepts and the parser refuses.
	pattern := regexp.MustCompile(schemaNamePattern(t))
	for _, name := range slices.Concat(copyAcceptedNames, copyRefusedNames) {
		if got, want := pattern.MatchString(name), IsNameOrCopy(name); got != want {
			t.Errorf("the schema's name pattern says %v of %q and IsNameOrCopy says %v", got, name, want)
		}
	}
}

// compileRecipeSchema loads schemas/recipe.schema.json under its $id, as a
// reader of the published format does.
func compileRecipeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(schemaBytes(t), &doc); err != nil {
		t.Fatal(err)
	}
	id, _ := doc["$id"].(string)
	if id == "" {
		t.Fatal("the schema names no $id")
	}
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

// schemaRecipeInstance is a minimal recipe named name in the validator's value
// model. The name is concatenated into the JSON rather than marshalled: every
// name in the tables holds no byte a JSON string escapes.
func schemaRecipeInstance(t *testing.T, name string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(`{"version":1,"name":"`+name+`","steps":[{"review":{}}]}`), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
