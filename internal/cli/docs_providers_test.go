package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
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

// TestReadmeOpenRouterSetup: README.md must carry a short, self-contained
// "how to set up OpenRouter" section (the owner asked for exactly this), and
// the two claims in it that a reader acts on -- which repositories a provider
// may see, and what a pin is refused with -- must be the code's, not prose
// that drifted. Every assertion is anchored to that section alone
// (docSection), so a claim that only appears elsewhere in the README does
// not make this pass.
func TestReadmeOpenRouterSetup(t *testing.T) {
	const path = "../../README.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, path, string(data), "## Other models through OpenRouter")

	// The steps a new user needs, each spelled the way the CLI spells it.
	for _, want := range []string{
		"~/.config/fugaro/projects/<project>.yaml", // where the provider is declared
		"fugaro secrets set openrouter-api-key",    // how the key is stored
		"--project-wide",                           // project-wide vs per-repository
		"coder: deepseek/deepseek-v4.1-flash",      // the pin in fugaro.yaml
		"fugaro validate",
		"fugaro init --repo",
		"fugaro doctor",
		"fugaro budget preview",
		"allow_data_to",
		"model_prices",
		"docs/multi-model.md",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the README OpenRouter section never mentions %q", want)
		}
	}
	// A setup guide must not carry a value: the placeholders are the point.
	for _, key := range []string{"sk-or-", "sk-ant-", "sk-"} {
		if strings.Contains(section, key) {
			t.Errorf("the README OpenRouter section looks like it carries a real key (%q)", key)
		}
	}

	// The gate the section describes is the code's: an absent allow_data_to
	// (or the explicit ["*"]) lets every repository send code to a provider,
	// a real list lets only the repositories it names, and a repository the
	// list leaves out is refused with the message the README quotes. Prices
	// are nil, which skips CheckProviderPolicy's own price rule.
	const model = "deepseek/deepseek-v4.1-flash"
	agent := config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: model, Reviewer: model}}
	for _, tc := range []struct {
		name      string
		allow     []string
		repo      string
		refused   bool
		openToAll bool
	}{
		{"absent means every repository", nil, "acme/app", false, true},
		{"the wildcard means every repository", []string{"*"}, "acme/app", false, true},
		{"a list names only its repositories", []string{"acme/other"}, "acme/app", true, false},
		{"a listed repository is allowed", []string{"acme/app"}, "acme/app", false, false},
	} {
		p := config.ModelProvider{Kind: config.ModelProviderKind, Models: []string{"deepseek/*"}, AllowDataTo: tc.allow}
		if got := p.OpenToEveryRepo(); got != tc.openToAll {
			t.Errorf("%s: OpenToEveryRepo() = %v, want %v", tc.name, got, tc.openToAll)
		}
		ps := config.CheckProviderPolicy(agent, nil, tc.repo, map[string]config.ModelProvider{"openrouter": p}, nil)
		if got := len(ps) > 0; got != tc.refused {
			t.Fatalf("%s: refused = %v (%v), want %v", tc.name, got, ps, tc.refused)
		}
		if tc.refused {
			text := layerProblemsText(ps)
			want := "whose allow_data_to does not name `" + tc.repo + "`"
			if !strings.Contains(text, want) {
				t.Errorf("%s: the refusal does not say %q: %s", tc.name, want, text)
			}
			// The README quotes this refusal, so the two cannot drift apart.
			if !strings.Contains(section, "does not name") {
				t.Error("the README OpenRouter section does not state the allow_data_to refusal")
			}
		}
	}

	// Claude goes direct, never through a provider: the section must say so,
	// and the rule must still be the code's.
	if !strings.Contains(section, "Claude models never go through a provider") {
		t.Error("the README OpenRouter section does not say Claude models never go through a provider")
	}
	if why := config.CheckModelPattern("anthropic/claude-sonnet-5-5"); why == "" {
		t.Error("a provider may claim an anthropic/ model, so the README section is wrong")
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
