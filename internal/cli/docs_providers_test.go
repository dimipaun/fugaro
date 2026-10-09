package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestMultiModelNamesDirectProviders: docs/multi-model.md must document
// sending a model's traffic straight to its vendor instead of OpenRouter
// (design generic-tool.md §6, G18): a "Direct to the vendor" section with a
// providers: example whose base_url is not OpenRouter's, the overlap and
// Claude-stays-direct rules, and the live check that settles it.
func TestMultiModelNamesDirectProviders(t *testing.T) {
	data, err := os.ReadFile("../../docs/multi-model.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	const heading = "## Direct to the vendor"
	idx := strings.Index(doc, heading)
	if idx == -1 {
		t.Fatalf("docs/multi-model.md has no %q section", heading)
	}
	section := doc[idx:]
	if next := strings.Index(section[len(heading):], "\n## "); next != -1 {
		section = section[:len(heading)+next]
	}
	if !strings.Contains(section, "providers:") {
		t.Error("the Direct to the vendor section has no providers: example")
	}
	urls := regexp.MustCompile(`base_url:\s*(\S+)`).FindAllStringSubmatch(section, -1)
	if len(urls) == 0 {
		t.Error("the Direct to the vendor section's example has no base_url")
	}
	nonOpenRouter := false
	for _, m := range urls {
		if !strings.Contains(m[1], "openrouter.ai") {
			nonOpenRouter = true
		}
	}
	if !nonOpenRouter {
		t.Error("the Direct to the vendor section's example has no base_url other than OpenRouter's")
	}
	if !strings.Contains(section, "may not overlap") {
		t.Error("the Direct to the vendor section does not say providers may not overlap")
	}
	if !strings.Contains(section, "Claude") || !strings.Contains(section, "never") {
		t.Error("the Direct to the vendor section does not say Claude models never go through a provider")
	}
	if !strings.Contains(section, "Check 33") {
		t.Error("the Direct to the vendor section does not name Check 33")
	}
}

// TestBackendsNamesTheControlPlaneSeam: docs/backends.md must say what a
// replacement control plane would have to provide, and name the packages
// that talk to it today (design generic-tool.md §11, G26). Docs only: no
// code is introduced by this.
func TestBackendsNamesTheControlPlaneSeam(t *testing.T) {
	data, err := os.ReadFile("../../docs/backends.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	const heading = "## The control plane seam"
	if !strings.Contains(doc, heading) {
		t.Fatalf("docs/backends.md has no %q section", heading)
	}
	section := doc[strings.Index(doc, heading):]
	for _, pkg := range []string{"internal/budget", "internal/rtdb", "internal/firestore", "internal/watch"} {
		if !strings.Contains(section, pkg) {
			t.Errorf("the control plane seam section never names %s", pkg)
		}
	}
}
