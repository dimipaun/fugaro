package images_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestDogfoodWorkflowPinsTheCatalogEntry: this repository runs its own `go`
// workflow on a project image-catalog entry, not on an image Fugaro builds for
// this repository and this workflow (docs/repo-image-removal, R0). The pin is
// the mechanical half of the removal's dogfood property -- no workflow of this
// repository names a per-repository image -- and it is what makes the entry,
// rather than the workflow's image: block, the thing the job's image comes
// from.
//
// Its second half reads the Dockerfile the entry's source names, at the
// convention this repository's own entry follows -- images/catalog/<entry>/Dockerfile,
// the path the owner's `fugaro image add --dockerfile <repo>:images/catalog/go/Dockerfile`
// hands the catalog -- and fails unless its last instruction is
// finalize-checkout. That call is the one step a hand-written Dockerfile can
// omit with no other symptom (D1208): the image builds, the job runs, and the
// baked checkout still holds a credential setting, which is the hazard the
// script exists for. The renderer ended every derived image with it, and an
// entry that replaces a rendered image has to as well.
func TestDogfoodWorkflowPinsTheCatalogEntry(t *testing.T) {
	const workflow = "go"
	data, err := os.ReadFile("../fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("../fugaro.yaml: %v", problems)
	}
	w, ok := cfg.Workflows[workflow]
	if !ok {
		t.Fatalf("../fugaro.yaml has no %s workflow", workflow)
	}
	if w.ImageName == "" {
		t.Errorf("the %s workflow names no catalog entry: it still builds its own image, and image_name: is what points it at the project's catalog", workflow)
	}
	if !w.Image.IsZero() || w.Dockerfile != "" {
		t.Errorf("the %s workflow carries its own image definition (image: or dockerfile:) beside image_name: %q: a job's image is the catalog's choice and nothing else", workflow, w.ImageName)
	}
	entry := w.ImageName
	if entry == "" {
		// No pin yet: the second half still reads the entry the interface
		// names for this workflow, so its own failure is not hidden behind
		// the one above.
		entry = workflow
	}
	path := filepath.Join("..", "images", "catalog", entry, "Dockerfile")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the Dockerfile of the catalog entry %s: %v", entry, err)
	}
	if last := lastInstruction(string(raw)); !strings.Contains(last, "/usr/local/lib/fugaro/finalize-checkout") {
		t.Errorf("%s does not end with finalize-checkout: its last instruction is %q", path, last)
	}
}

// lastInstruction is the last line of a Dockerfile that is not blank and not a
// comment: what the build runs last, which is what "ends with" means here.
func lastInstruction(dockerfile string) string {
	last := ""
	for _, line := range strings.Split(dockerfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		last = line
	}
	return last
}
