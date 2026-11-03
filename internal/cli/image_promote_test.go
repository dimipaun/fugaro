package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// promoteYAML is a repository's own image definition: a workflow that
// customises the image it builds (image:), with a comment above it that the
// promotion must leave where it is.
const promoteYAML = `version: 1
project: aurora
git: { provider: github }
# the app's image: jq is what the build scripts need
workflows:
  app:
    image: { tools: { jq: "1.7" } }
    commands: { build: sh build.sh, test: sh test.sh }
`

// TestImagePromoteCopiesTheDefinitionAndRepointsTheRepo: `fugaro image
// promote` reads the repository's existing definition, writes it into the
// catalog as an entry and points the repository's workflow at that entry, in
// the checkout it is run from (D1147).
func TestImagePromoteCopiesTheDefinitionAndRepointsTheRepo(t *testing.T) {
	catalogFixture(t)
	dir := catalogCheckout(t, promoteYAML)
	yamlPath := filepath.Join(dir, "fugaro.yaml")

	out, _, err := execute(t, "image", "promote", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "app") || !strings.Contains(out, "acme/app") {
		t.Errorf("image promote printed %q, want the entry and the repository", out)
	}

	// The entry is in the project config, and its source is the rendered
	// definition the workflow had: a definition that exists only as
	// configuration travels into the entry as an inline Dockerfile.
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the config after the promotion: %v\n%s", err, data)
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 3 {
		t.Fatalf("ImageCatalog = %+v, want the fixture's two entries plus the promoted one", lc.ImageCatalog)
	}
	e := lc.ImageCatalog.Entries[2]
	if e.Name != "app" {
		t.Fatalf("the promoted entry is %q, want the repository's name", e.Name)
	}
	if e.Source.Kind != "inline" || !strings.Contains(e.Source.Inline, "FROM ${FUGARO_BASE}") {
		t.Errorf("the promoted entry's source = %+v, want the workflow's rendered definition", e.Source)
	}
	if !strings.Contains(e.Source.Inline, "jq") {
		t.Errorf("the promoted definition does not carry the workflow's image: block:\n%s", e.Source.Inline)
	}

	// The repository is repointed in the same change, and the file is edited
	// rather than re-marshalled: the comment it came with is still there.
	edited, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(edited), "image_name: app") {
		t.Errorf("fugaro.yaml after the promotion:\n%s", edited)
	}
	if !strings.Contains(string(edited), "# the app's image: jq is what the build scripts need") {
		t.Errorf("the promotion dropped the file's comment:\n%s", edited)
	}
	if !strings.Contains(string(edited), "jq") || !strings.Contains(string(edited), "image:") {
		t.Errorf("the promotion dropped the workflow's own definition, which stays until the operator commits the change:\n%s", edited)
	}

	// A second promotion of the same repository is refused: the workflow
	// already names an entry, and the file is left as it is.
	_, _, err = execute(t, "image", "promote", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "app") {
		t.Fatalf("promoting twice: exit %d, err %v", ExitCode(err), err)
	}
	after, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(edited) {
		t.Error("a refused promotion still wrote fugaro.yaml")
	}
}

// TestImagePromoteIsNeverAutomatic: nothing converts a repository's image
// definition on its own. The promotion is the only writer of image_name in
// the CLI, and the commands that read a checkout leave both files alone
// (D1147, the spec's "existing repo-level images are never converted
// automatically").
func TestImagePromoteIsNeverAutomatic(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var writers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"image_name"`) {
			writers = append(writers, name)
		}
	}
	if !slices.Equal(writers, []string{"image_promote.go"}) {
		t.Errorf("the CLI writes image_name in %v, want only image_promote.go: a second writer is a conversion nothing asked for", writers)
	}

	// And the commands a person runs against a checkout do not touch it: a
	// repository with an image: block keeps it, and the project gains no
	// catalog entry.
	newCloudFixture(t)
	dir := catalogCheckout(t, promoteYAML)
	yamlPath := filepath.Join(dir, "fugaro.yaml")
	before, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	configPath := os.Getenv("FUGARO_CONFIG")
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"validate", yamlPath}, {"image", "render"}, {"image", "ls"}} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	after, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a command that only reads the checkout rewrote fugaro.yaml:\n%s", after)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Errorf("a command that only reads wrote the project config:\n%s", configAfter)
	}
	if strings.Contains(string(configAfter), "image_catalog") {
		t.Errorf("a command that only reads wrote a catalog:\n%s", configAfter)
	}
}
