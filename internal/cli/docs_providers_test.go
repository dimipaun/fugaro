package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// docSection returns the text of doc from heading (inclusive) up to, but not
// including, the next line starting with "## " (or to the end of doc). It
// fails the test if heading is not found, so every caller's assertions are
// anchored to the right section instead of the whole document.
//
// The cut is at the next heading of the same or a higher level than heading's
// own, not at the next "## " whatever heading's level is. Cutting at "## " is
// right for a "## " heading and wrong for a "### " one: a "### X" section runs
// on through every following "### " up to the next "## ", so a claim the
// caller means to read out of X can be satisfied by a sibling of X. That is
// how the credentials-service guards came to read SECURITY.md's "### The
// credentials service" together with all of "### Known limits" — the egress
// pin was green on the Known-limits bullet alone (code review of C1.22). A
// heading whose text is not itself an ATX heading keeps the old "## " cut,
// since there is no level to compare.
func docSection(t *testing.T, path, doc, heading string) string {
	t.Helper()
	idx := strings.Index(doc, heading)
	if idx == -1 {
		t.Fatalf("%s has no %q section", path, heading)
	}
	level := headingLevel(heading)
	if level == 0 {
		level = 2
	}
	section := doc[idx:]
	rest := section[len(heading):]
	cut, off, fenced := len(section), 0, false
	for len(rest) > 0 {
		line := rest
		if nl := strings.IndexByte(rest, '\n'); nl != -1 {
			line = rest[:nl]
		}
		if isFence(line) {
			fenced = !fenced
		} else if !fenced {
			if l := headingLevel(line); l > 0 && l <= level {
				cut = len(heading) + off
				break
			}
		}
		off += len(line) + 1
		rest = rest[len(line):]
		if len(rest) > 0 {
			rest = rest[1:]
		}
	}
	return section[:cut]
}

// headingLevel is the number of leading "#" in line when line is an ATX
// heading — a run of "#" followed by a space, or the whole line — and 0 when
// it is not one.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || (n < len(line) && line[n] != ' ') {
		return 0
	}
	return n
}

// isFence reports whether line opens or closes a fenced code block: three or
// more backticks or tildes, indented no more than three spaces. A "#" line
// inside a fence is a comment, not a heading, and cutting there would end a
// section in the middle of its own example.
func isFence(line string) bool {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 {
		return false
	}
	return strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~")
}

// TestDocsSectionHelperCutsAtItsOwnLevelAndSkipsFences is the guard on the
// guard: docSection is what every "read the claims out of THIS section" test in
// this package stands on, and the C1.22 code review found it cutting at the next
// "## " whatever the target's own level was. It is exercised on a document this
// test owns, so the properties are not at the mercy of a later rewrite of the
// real ones it is used on, and each is the property a caller's comment claims:
//
//   - a "### " read stops at the next "### " — a sibling must not satisfy it —
//     and at the next "## " above it;
//   - a "## " read still carries its own "### " children and stops at the next
//     "## ", which is what every "## " caller has always relied on;
//   - a "#" line inside a fenced code block is a comment and not a heading:
//     cutting there ends a section in the middle of its own example, which is
//     what a first, fence-blind version of this fix did to the Check
//     70a/70b/70c guards, whose bash blocks carry "#" comment lines.
//
// Both properties are mutation-proved, each by the change it is about, run and
// restored: cutting at "## " whatever the level is (the helper's old rule)
// fails it on the sibling's text, and a fence-blind headingLevel fails it on
// the fenced section losing its own body.
func TestDocsSectionHelperCutsAtItsOwnLevelAndSkipsFences(t *testing.T) {
	const path = "synthetic.md"
	const doc = `# Doc

## One

### One A

claim of one A

### One B

claim of one B

## Two

claim of two
`
	got := docSection(t, path, doc, "### One A")
	if !strings.Contains(got, "claim of one A") {
		t.Errorf("docSection(### One A) dropped the section's own text: %q", got)
	}
	for _, banned := range []string{"claim of one B", "claim of two", "## Two"} {
		if strings.Contains(got, banned) {
			t.Errorf("docSection(### One A) read %q: a ### section runs to the next heading of its own level or higher, and a sibling's text must not be able to satisfy a claim about this one", banned)
		}
	}
	if got := docSection(t, path, doc, "## One"); !strings.Contains(got, "claim of one B") {
		t.Errorf("docSection(## One) dropped its own ### children: a ## section runs to the next ## , which is what every ## caller reads")
	}
	// The fence, in a document of its own: a "#" line inside it is a comment,
	// and the heading after the fence still ends the section.
	fenced := "### Fenced\n\n```bash\n# not a heading\necho hi\n```\n\nstill the fenced section\n\n## Next\n\ngone\n"
	got = docSection(t, path, fenced, "### Fenced")
	for _, want := range []string{"# not a heading", "still the fenced section"} {
		if !strings.Contains(got, want) {
			t.Errorf("docSection(### Fenced) lost %q: a # line inside a fence is a comment, not a heading, and cutting there ends a section in the middle of its own example", want)
		}
	}
	if strings.Contains(got, "gone") {
		t.Error("docSection(### Fenced) ran past `## Next`: tracking the fence must not hide the heading that really does end the section")
	}
}

// TestMultiModelNamesDirectProviders: docs/multi-model.md must document
// sending a model's traffic straight to its vendor instead of OpenRouter
// (design generic-tool.md §6, G18): a "Direct to the vendor" section with a
// providers: example whose base_url is not OpenRouter's, the overlap rule,
// Claude's own rule and the live check that settles it. Every assertion
// below is checked against that section alone (docSection), not the whole
// document, so a claim that only appears elsewhere does not make this test
// pass.
//
// Claude's rule is the one the section's sentence changed under: it read
// "Claude models never go through a provider", and Phase 2's opt-in
// (design docs/design/mixed-credentials.md §4) made one exact anthropic/<id>
// claimable per route entry. The two assertions below are the two halves of
// what the section must say now -- direct by default, one exact ID claimed
// by an operator -- so an edit that drops either one fails here.
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
	if !strings.Contains(section, "Claude goes direct by default") {
		t.Error("the Direct to the vendor section does not say Claude goes direct by default")
	}
	// Phase 2's opt-in (design mixed-credentials.md §4) made the rule no
	// longer absolute, so the section has to say both halves: no pattern
	// claims a Claude model, and one exact anthropic/<id> does.
	if !strings.Contains(section, "One exact ID can be claimed") {
		t.Error("the Direct to the vendor section does not say a route may claim one exact anthropic/<id>")
	}
	if !strings.Contains(section, "Check 33") {
		t.Error("the Direct to the vendor section does not name Check 33")
	}
}

// TestReadmeOpenRouterSetup: README.md must carry a short, self-contained
// "how to set up OpenRouter" section (the owner asked for exactly this), and
// the claims in it that a reader acts on -- the account-side setup it links
// instead of duplicating, which repositories a provider may see, what a pin
// is refused with, and until which release the two catalog switches are
// refused -- must be the code's and the linked docs', not prose that drifted.
// Every assertion is anchored to that section alone (docSection), so a claim
// that only appears elsewhere in the README does not make this pass.
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
		"--repo <owner>/<repo>",                    // the other way to scope the key
		"coder: deepseek/deepseek-v4.1-flash",      // the pin in fugaro.yaml
		"fugaro validate",
		"fugaro update --repo",
		"fugaro doctor",
		"fugaro budget preview",
		"budget.per_day_usd", // the repository's own day cap
		"allow_data_to",
		"model_prices",
		"docs/multi-model.md",
		"docs/dogfooding.md", // the provider-coder setup the example is
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the README OpenRouter section never mentions %q", want)
		}
	}
	// The account-side setup comes first and is not duplicated here: the
	// section must link docs/multi-model.md's own section 1, and the four
	// things that section says a person must do on OpenRouter.
	if !strings.Contains(section, "docs/multi-model.md#1-on-the-openrouter-account-before-anything-in-fugaro") {
		t.Error("the README OpenRouter section does not link docs/multi-model.md's OpenRouter-account setup")
	}
	for _, want := range []string{"credit limit", "fallbacks", "data-collection", "route fee"} {
		if !strings.Contains(section, want) {
			t.Errorf("the README OpenRouter section's account step never mentions %q", want)
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

	// Claude goes direct by default and a provider route for it is an explicit
	// opt-in (one exact anthropic/<id> in a provider's models): the section
	// must say both, and the rule must still be the code's (CheckClaudeClaim
	// accepts that one shape and refuses a wildcard).
	if !strings.Contains(section, "Claude models go direct to Anthropic by default") {
		t.Error("the README OpenRouter section does not say Claude models go direct by default")
	}
	if !strings.Contains(section, "explicit opt-in") || !strings.Contains(section, "`anthropic/<id>`") {
		t.Error("the README OpenRouter section does not describe the explicit anthropic/<id> opt-in")
	}
	if why := config.CheckClaudeClaim("anthropic/claude-sonnet-5.5"); why != "" {
		t.Errorf("an exact anthropic/<id> is the one claim a provider may make, but the code says %q, so the README section is wrong", why)
	}
	if why := config.CheckClaudeClaim("anthropic/*"); why == "" {
		t.Error("a wildcard claim must be refused, but the code accepts it, so the README section is wrong")
	}

	// The section's "ship at <release>" sentence is catalogSince, and the
	// "leave both out" instruction lasts exactly as long as catalogShipped is
	// false (catalogSwitchSkew, internal/cli/recipes_skew.go). Tying both to
	// the constants means the one-line PR that ships the two switches -- or
	// that bumps the release they ship in -- cannot leave the README telling
	// users to leave the keys out, or the release number wrong.
	if !strings.Contains(section, "ship at "+catalogSince) {
		t.Errorf("the README OpenRouter section does not say require_zdr/price_policy ship at %s (catalogSince)", catalogSince)
	}
	if leftOut := strings.Contains(section, "leave both out"); leftOut == catalogShipped {
		t.Errorf("the README OpenRouter section's \"leave both out\" instruction disagrees with catalogShipped = %v", catalogShipped)
	}

	// The section states how narrow the catalog is, so a reader knows a model
	// it does not cover is refused rather than guessed. The count is the
	// embedded file's, and the model the section pins by example is one of
	// its rows: a regenerated catalog that changes either fails here.
	f, err := catalog.Load()
	if err != nil {
		t.Fatalf("the built-in price catalog does not load: %v", err)
	}
	count := map[int]string{1: "one model", 2: "two models", 3: "three models", 4: "four models", 5: "five models"}[len(f.Models)]
	if count == "" {
		count = strconv.Itoa(len(f.Models)) + " models"
	}
	if !strings.Contains(section, "only "+count) {
		t.Errorf("the README OpenRouter section does not say the catalog covers only %s (it holds %d)", count, len(f.Models))
	}
	if _, ok := f.Models[model]; !ok {
		t.Errorf("the README OpenRouter section pins %s by example, which the built-in catalog does not price", model)
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
