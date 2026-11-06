package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// promoteCheckoutYAML is a checkout a person runs the read-only commands
// against, with a comment above the workflow: a command that re-marshals a
// fugaro.yaml rather than editing it drops that comment, so its presence is
// what says the commands below left the file's bytes alone.
//
// It carries no image definition, because a fugaro.yaml cannot have one any
// more: the repository-image removal refuses image:, dockerfile: and rebuild:
// by name (D1195) and a repository's image is the project catalog's entry it
// names. The two definitions the promotion used to be tested on — an image:
// block and a dockerfile: pointer — went with the keys.
const promoteCheckoutYAML = `version: 1
project: aurora
git: { provider: github }
# the app's fugaro.yaml: reading it must not rewrite these bytes
workflows:
  app:
    commands: { build: sh build.sh, test: sh test.sh }
`

// TestImagePromoteIsNeverAutomatic: nothing converts a repository's image on
// its own. The promotion is the only writer of image_name in the CLI, and the
// commands that read a checkout leave both files alone (D1147, the spec's
// "existing repo-level images are never converted automatically").
//
// The second half read a repository carrying its own definition before the
// repository-image removal; the property it holds is the same one on the shape
// a file has after it — a command a person runs against a checkout writes
// neither fugaro.yaml nor the project config, and no catalog comes out of
// reading either.
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

	newCloudFixture(t)
	dir := catalogCheckout(t, promoteCheckoutYAML)
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
