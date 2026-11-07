package sensitivity_test

// The classifier is a guard, and these are the cases that make it one: it reads
// paths and nothing else, it is an allowlist rather than a denylist, and it
// fails closed. Each test is written to fail if the answer moves toward the
// cheaper class — an empty path list read as Routine, a denylist that lets an
// unlisted tree through, a `_test.go` shortcut that makes a guard's own test
// edit routine, or a signature that grows a description the model could write.
//
// The console's own paths (its spawn and IPC allow-lists, the launch and
// settings writes, the pairing check) are in the fixture and not here: a Go file
// of this module may mention a path under the console's directory only from a
// pinned allow-list in internal/cli's boundary guard, and a classifier test has
// no case for spending that exception. The fixture carries them, and
// TestRoutingRuleFixtureIsNeverRoutine is what holds them to Sensitive.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

const (
	reconciliationDoc = "docs/stateless-orchestrator/reconciliation.md"
	planDoc           = "docs/stateless-orchestrator/implementation-plan.md"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// section is one heading of testdata/paths.txt: the category as the routing
// text names it, and the paths the fixture gives that category.
type section struct {
	category string
	paths    []string
}

// pathsFixture reads the routing rule's paths. A "#" line is prose, a "##" line
// opens a section, and every other non-blank line is a path — so a section with
// no paths, or a path before the first heading, is a fixture that stopped
// saying what it means.
func pathsFixture(t *testing.T) []section {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "paths.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var sections []section
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "## "):
			sections = append(sections, section{category: strings.TrimSpace(line[3:])})
		case strings.HasPrefix(line, "#"):
		case len(sections) == 0:
			t.Fatalf("testdata/paths.txt:%d: %q is a path before any section heading", i+1, line)
		default:
			sections[len(sections)-1].paths = append(sections[len(sections)-1].paths, line)
		}
	}
	if len(sections) < 2 {
		t.Fatalf("testdata/paths.txt has %d sections; the fixture covers the routing rule's categories", len(sections))
	}
	for _, s := range sections {
		if s.category == "" {
			t.Errorf("testdata/paths.txt has a section heading with no category name")
		}
		if len(s.paths) == 0 {
			t.Errorf("testdata/paths.txt's %q section has no paths", s.category)
		}
	}
	return sections
}

// TestEmptyPathListIsSensitive is the fail-closed rule: a change whose paths the
// daemon could not read is not a change it may hand to the cheaper model. A nil
// list, an empty one, and a list of empty strings are all the same answer.
func TestEmptyPathListIsSensitive(t *testing.T) {
	for _, paths := range [][]string{nil, {}, {""}, {"", ""}} {
		if got := sensitivity.Classify(paths); got != sensitivity.Sensitive {
			t.Errorf("Classify(%q) = %q, want %q: with no path to place, the class fails closed", paths, got, sensitivity.Sensitive)
		}
	}
}

// TestOnlyAllowlistedPathsAreRoutine: the routine trees are documentation and
// the website, and every path of the change has to be inside one of them — a
// change with one guard file and nine doc files is a guard change. A `_test.go`
// file is routine only because its tree is, never on its own (the guard-tree
// case is TestTestEditsInGuardTreesAreSensitive).
func TestOnlyAllowlistedPathsAreRoutine(t *testing.T) {
	routine := []string{
		"docs/README.md",
		"docs/design/v1.md",
		"docs/backlog/20260101-01-an-item.md",
		"docs/stateless-orchestrator/implementation-plan.md",
		"docs/schemas_test.go",
		"website/src/pages/index.astro",
		"website/content/cli-reference.md",
		"website/test/cli.test.mjs",
	}
	for _, p := range routine {
		if got := sensitivity.Classify([]string{p}); got != sensitivity.Routine {
			t.Errorf("Classify([%q]) = %q, want %q", p, got, sensitivity.Routine)
		}
	}
	if got := sensitivity.Classify([]string{"docs/a.md", "website/b.md"}); got != sensitivity.Routine {
		t.Errorf("a change inside both routine trees is %q, want %q", got, sensitivity.Routine)
	}
	if got := sensitivity.Classify([]string{"docs/a.md", "internal/cli/root.go", "website/b.md"}); got != sensitivity.Sensitive {
		t.Errorf("a change with one path outside the routine trees is %q, want %q: every path decides", got, sensitivity.Sensitive)
	}
}

// TestUnlistedPathIsSensitive: the allowlist is closed. A tree the classifier
// has never been told about, a path that is not a repository-relative file
// path, and a routine tree's own exception are all Sensitive — the answer a
// later task wants when it adds a tree nobody has classified yet.
func TestUnlistedPathIsSensitive(t *testing.T) {
	unlisted := []string{
		// Trees the routing rule names and the allowlist does not.
		"internal/cli/root.go",
		"internal/orchestrator/sensitivity/classify.go",
		"schemas/fugaro.schema.json",
		"images/base/Dockerfile",
		"mobile/src/app/app.js",
		".github/workflows/ci.yml",
		"go.mod",
		// The one tree the allowlist opens and then closes again: a person
		// copies these files into .github/workflows, and CI is the routing
		// rule's own category.
		"docs/pending-workflows/shared-tests.yml",
		"docs/pending-workflows/README.md",
		// Not paths a change can carry: absolute, escaping, uncleaned, empty
		// of a name, or a directory rather than a file.
		"",
		"docs",
		"/docs/README.md",
		"../docs/README.md",
		"./docs/README.md",
		"docs/../website/index.astro",
		"docs/./README.md",
		"docs//README.md",
		"docs/",
		`docs\README.md`,
		`C:\docs\README.md`,
		"DOCS/README.md",
		"docs/README.md\x00.txt",
	}
	for _, p := range unlisted {
		if got := sensitivity.Classify([]string{p}); got != sensitivity.Sensitive {
			t.Errorf("Classify([%q]) = %q, want %q", p, got, sensitivity.Sensitive)
		}
	}
}

// TestTestEditsInGuardTreesAreSensitive: a test is not a cheaper kind of change.
// A test in a guard's tree is how the guard is weakened, so the file being a
// `_test.go` buys nothing outside an allowlisted tree.
func TestTestEditsInGuardTreesAreSensitive(t *testing.T) {
	for _, p := range []string{
		"internal/orchestrator/state_test.go",
		"internal/orchestrator/sensitivity/classify_test.go",
		"internal/initflow/stage_test.go",
		"internal/agentenv/agentenv_test.go",
		"internal/managed/preflight_test.go",
		"schemas/orchestrator-state_test.go",
		"ee/orchestrator/batch_test.go",
		".claude/hooks/session_start_test.go",
		"mobile/src/mailbox/gate.test.js",
	} {
		if got := sensitivity.Classify([]string{p}); got != sensitivity.Sensitive {
			t.Errorf("Classify([%q]) = %q, want %q: a test in a guard's tree is a guard change", p, got, sensitivity.Sensitive)
		}
	}
}

// TestPromptPathsAreAlwaysSensitive: the routing rule's categories do not stop
// being security-sensitive because a change is mostly words. Prompts, recipes,
// the repository's instructions to its agents and the plugin's skills are where
// a written instruction becomes behaviour, so none of them may be routine.
//
// Reported and not decided here: the plan allowlists docs/** wholesale, so a
// prompt file moved into docs/ would be Routine. Today's prompt paths are all
// outside it, and this fixture is what would notice such a move.
func TestPromptPathsAreAlwaysSensitive(t *testing.T) {
	for _, p := range []string{
		"CLAUDE.md",
		"AGENTS.md",
		".claude/rules/ee.md",
		".claude/skills/golang-how-to/SKILL.md",
		"plugin/skills/working/SKILL.md",
		"plugin/skills/orchestrator/SKILL.md",
		".fugaro/review.md",
		"shared/prompts/index.js",
		"internal/runner/prompts.go",
		"internal/followup/prompt.go",
		"internal/recipe/recipe.go",
		"fugaro.yaml",
	} {
		if got := sensitivity.Classify([]string{p}); got != sensitivity.Sensitive {
			t.Errorf("Classify([%q]) = %q, want %q", p, got, sensitivity.Sensitive)
		}
	}
}

// TestDescriptionCannotLowerTheClass: the class is a function of the changed
// paths and of nothing else (D1234, G6). A signature that grew a title, a
// description or a model's own guess would be the hole the owner ruled out — a
// security change reviewed cheaply because it says "docs" — so the shape of
// Classify is pinned: one argument, a slice of paths, and a Class back.
func TestDescriptionCannotLowerTheClass(t *testing.T) {
	ft := reflect.TypeOf(sensitivity.Classify)
	if ft == nil || ft.Kind() != reflect.Func {
		t.Fatalf("Classify is %v, want a function", ft)
	}
	if ft.IsVariadic() {
		t.Fatalf("Classify is variadic; the paths it classifies must be one list, not arguments a caller can forget")
	}
	if ft.NumIn() != 1 {
		t.Fatalf("Classify takes %d arguments; the class must be decided by the changed paths alone", ft.NumIn())
	}
	if in := ft.In(0); in.Kind() != reflect.Slice || in.Elem().Kind() != reflect.String {
		t.Errorf("Classify's argument is %v, want a slice of paths", in)
	}
	want := reflect.TypeOf(sensitivity.Sensitive)
	if ft.NumOut() != 1 || ft.Out(0) != want {
		t.Fatalf("Classify returns %v, want one %v", ft.Out(0), want)
	}
}

// TestClassValuesArePinned: the two names are what a policy's model key is
// written against (O4) and what the state carries, so the spelling is interface
// rather than detail. A renamed class would not fail a decode — it would stop
// matching the key and read as "no model pinned", which is the wrong way round
// for a guard: the sensitive class would fall back up, the routine one silently
// down.
func TestClassValuesArePinned(t *testing.T) {
	if sensitivity.Routine != "routine" || sensitivity.Sensitive != "sensitive" {
		t.Errorf("the classes are %q and %q, want \"routine\" and \"sensitive\"", sensitivity.Routine, sensitivity.Sensitive)
	}
}

// TestRoutingRuleFixtureIsNeverRoutine holds the fixture, and with it the
// routing rule's own trees, to the classifier: not one path of a category the
// routing text calls security-sensitive may come back Routine. The floor on the
// path count is deliberate — a fixture that lost its paths would pass this test
// vacuously, which is the failure mode of every table-driven guard.
func TestRoutingRuleFixtureIsNeverRoutine(t *testing.T) {
	sections := pathsFixture(t)
	paths := 0
	for _, s := range sections {
		for _, p := range s.paths {
			paths++
			got := sensitivity.Classify([]string{p})
			if got != sensitivity.Sensitive {
				t.Errorf("Classify([%q]) = %q, want %q: %q carries the routing rule's %q category", p, got, sensitivity.Sensitive, p, s.category)
			}
			if got := sensitivity.Classify([]string{"docs/README.md", p}); got != sensitivity.Sensitive {
				t.Errorf("a change with one routine path and %q is %q, want %q", p, got, sensitivity.Sensitive)
			}
		}
	}
	if paths < 20 {
		t.Errorf("testdata/paths.txt carries %d paths, want at least 20: the fixture has stopped covering the routing rule", paths)
	}
}

// beyondTheRoutingText are the fixture's sections the routing text does not
// name. mobile/ and the console's directory hold credentials and MACs, so the
// design adds them to the rule's categories rather than reading them out of it.
// Every other section name must be one the text itself uses, so a category
// cannot be invented in the fixture or quietly dropped from the doc.
var beyondTheRoutingText = map[string]bool{"mobile": true, "the console": true}

// theRoutineTrees and theRoutineException are the allowlist the plan's O1
// states (D1234), as globs: the two trees a change may be Routine in, and the
// one tree inside them that is not.
var (
	theRoutineTrees     = []string{"docs/**", "website/**"}
	theRoutineException = "docs/pending-workflows/**"
)

// TestRulesTableMatchesTheReviewerRoutingText is the two-way pin between the
// classifier's table and the rule it is code for. The sensitive half: every
// category the fixture carries is a category of the owner's routing rule as
// reconciliation.md F15 documents it for reviewers, and no routine rule admits
// a path of one. The routine half: the trees the table opens are the ones the
// plan's O1 names, and the tree it opens and closes again is the exception it
// names. A table that widened itself, or a doc that lost a category, is red
// here rather than a cheaper review nobody notices.
func TestRulesTableMatchesTheReviewerRoutingText(t *testing.T) {
	text := routingText(t)
	routine := sensitivity.RoutineRules()
	exceptions := sensitivity.SensitiveExceptions()
	if len(routine) == 0 {
		t.Fatal("the classifier has no routine rule at all, so nothing is Routine")
	}

	for _, s := range pathsFixture(t) {
		if !beyondTheRoutingText[s.category] && !strings.Contains(text, s.category) {
			t.Errorf("testdata/paths.txt's %q category is not a category of the routing text (%s)", s.category, reconciliationDoc)
		}
		for _, p := range s.paths {
			for _, r := range routine {
				if strings.HasPrefix(p, r.Prefix) {
					t.Errorf("the routine rule %q (%q) admits %q, which the routing text calls security-sensitive", r.Name, r.Prefix, p)
				}
			}
			for _, e := range exceptions {
				if strings.HasPrefix(p, e.Prefix) {
					t.Errorf("the exception %q (%q) covers %q, a path of the routing rule's own category", e.Name, e.Prefix, p)
				}
			}
		}
	}

	plan := planO1(t)
	for _, want := range append([]string{theRoutineException}, theRoutineTrees...) {
		if !strings.Contains(plan, "`"+want+"`") {
			t.Errorf("%s's O1 no longer names %s as an allowlisted tree", planDoc, want)
		}
	}
	if got, want := prefixes(routine), prefixesOf(theRoutineTrees); !reflect.DeepEqual(got, want) {
		t.Errorf("the routine table is %q, want the plan's O1 trees %q", got, want)
	}
	if got, want := prefixes(exceptions), prefixesOf([]string{theRoutineException}); !reflect.DeepEqual(got, want) {
		t.Errorf("the exception table is %q, want the plan's O1 exception %q", got, want)
	}
}

// routingText is the owner's routing rule as reconciliation.md writes it for
// reviewers (F15), whitespace-normalised: the paragraph is hard-wrapped, so a
// phrase it carries is split across lines in the file and whole in the text.
func routingText(t *testing.T) string {
	t.Helper()
	doc := repoFile(t, reconciliationDoc)
	start, end := strings.Index(doc, "**F15."), strings.Index(doc, "**F16.")
	if start < 0 || end < start {
		t.Fatalf("%s has no F15 paragraph: the owner's routing rule is what this table is code for", reconciliationDoc)
	}
	return strings.Join(strings.Fields(doc[start:end]), " ")
}

// planO1 is the plan's O1 section, normalised the same way: it is where the
// allowlist's trees are stated.
func planO1(t *testing.T) string {
	t.Helper()
	doc := repoFile(t, planDoc)
	start, end := strings.Index(doc, "### O1. "), strings.Index(doc, "### O2. ")
	if start < 0 || end < start {
		t.Fatalf("%s has no O1 section", planDoc)
	}
	return strings.Join(strings.Fields(doc[start:end]), " ")
}

func prefixes(rows []sensitivity.RuleRow) []string {
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Prefix)
	}
	return got
}

// prefixesOf turns the plan's globs into the path prefixes the table matches
// with: `docs/**` and the prefix `docs/` are the same tree.
func prefixesOf(globs []string) []string {
	got := make([]string, 0, len(globs))
	for _, g := range globs {
		got = append(got, strings.TrimSuffix(g, "**"))
	}
	return got
}
