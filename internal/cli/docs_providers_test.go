package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// docSection returns the text of doc from heading (inclusive) up to, but not
// including, the next line starting with "## " (or to the end of doc). It
// fails the test if heading is not found, so every caller's assertions are
// anchored to the right section instead of the whole document.
func docSection(t *testing.T, path, doc, heading string) string {
	t.Helper()
	idx := strings.Index(doc, heading)
	if idx == -1 {
		t.Fatalf("%s has no %q section", path, heading)
	}
	section := doc[idx:]
	if next := strings.Index(section[len(heading):], "\n## "); next != -1 {
		section = section[:len(heading)+next]
	}
	return section
}

// TestMultiModelNamesDirectProviders: docs/multi-model.md must document
// sending a model's traffic straight to its vendor instead of OpenRouter
// (design generic-tool.md §6, G18): a "Direct to the vendor" section with a
// providers: example whose base_url is not OpenRouter's, the overlap and
// Claude-stays-direct rules, and the live check that settles it. Every
// assertion below is checked against that section alone (docSection), not
// the whole document, so a claim that only appears elsewhere does not make
// this test pass.
func TestMultiModelNamesDirectProviders(t *testing.T) {
	const path = "../../docs/multi-model.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, path, string(data), "## Direct to the vendor")
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
	if !strings.Contains(section, "Claude models never go through a provider") {
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
	const path = "../../docs/backends.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, path, string(data), "## The control plane seam")
	for _, pkg := range []string{"internal/budget", "internal/rtdb", "internal/firestore", "internal/watch"} {
		if !strings.Contains(section, pkg) {
			t.Errorf("the control plane seam section never names %s", pkg)
		}
	}
}

// TestChecklistHasCheck33: docs/gcp-live-checklist.md must carry the
// vendor-endpoint live check (design generic-tool.md §6, G18) as a
// user-run, not-yet-run, sandbox-only check, so a reader doesn't mistake it
// for something CI already covers.
func TestChecklistHasCheck33(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, path, string(data), "## Check 33")
	if !strings.Contains(section, "USER-RUN, NOT RUN") {
		t.Error("the Check 33 section does not say USER-RUN, NOT RUN")
	}
	if !strings.Contains(section, "sandbox") {
		t.Error("the Check 33 section does not say sandbox")
	}
}
