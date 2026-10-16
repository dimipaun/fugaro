package cli

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// docsProjectLayerFuture lists the commands docs/project-layer.md deliberately
// names as not yet built (section 10, "Not yet"): a reference to real work that
// does not exist yet, not a typo to flag. `fugaro project policy publish` and
// the rest of the group left this list when they shipped (phase 1 task 1.6);
// `config extract` is still phase 4 of the layered-config plan.
var docsProjectLayerFuture = map[string]bool{"config extract": true}

// cmdHasFlag reports whether c, or an ancestor of c (a persistent flag),
// declares a flag named name.
func cmdHasFlag(c *cobra.Command, name string) bool {
	for cur := c; cur != nil; cur = cur.Parent() {
		if cur.Flags().Lookup(name) != nil || cur.PersistentFlags().Lookup(name) != nil {
			return true
		}
	}
	return false
}

// TestDocsProjectLayerCommandsExist keeps every `fugaro <command...>` and
// every --flag docs/project-layer.md shows real: the command must resolve
// in the cobra tree, and the flag must belong to that command or one of
// its ancestors. Renaming or dropping a flag or a subcommand that the doc
// still names fails this test, instead of leaving the doc describing a
// command line nobody can actually run.
func TestDocsProjectLayerCommandsExist(t *testing.T) {
	data, err := os.ReadFile("../../docs/project-layer.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	root := NewRootCmd()

	wordRE := regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	cmdRE := regexp.MustCompile("`(fugaro [^`]*)`")

	for _, m := range cmdRE.FindAllStringSubmatch(doc, -1) {
		fields := strings.Fields(m[1])
		var path []string
		for _, f := range fields[1:] { // fields[0] is "fugaro"
			if !wordRE.MatchString(f) {
				break
			}
			path = append(path, f)
		}
		if len(path) == 0 || docsProjectLayerFuture[strings.Join(path, " ")] {
			continue
		}
		c, _, err := root.Find(path)
		if err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("docs/project-layer.md names %q, which is not a command", m[1])
			continue
		}
		for _, fm := range flagRE.FindAllStringSubmatch(m[1], -1) {
			if !cmdHasFlag(c, fm[1]) {
				t.Errorf("docs/project-layer.md's %q names --%s, which fugaro %s has no such flag", m[1], fm[1], strings.Join(path, " "))
			}
		}
	}
}

// TestDocsProjectLayerSaysWhatDoctorReports: doctor reports the policy's
// state (phase 1 task 1.6 part B), and §10's "Not yet" must stop claiming it
// does not — a doc that still lists shipped work as missing sends an
// operator looking for a command they already have.
//
// Mutation (verified, run and restored): remove the "It also reports the
// project's policy" paragraph from §7 and put back the "doctor does not yet
// report the policy's state" clause in §10: this test fails, both ways.
func TestDocsProjectLayerSaysWhatDoctorReports(t *testing.T) {
	doc := readDoc(t, "docs/project-layer.md")
	for _, want := range []string{
		"It also reports the project's policy",
		"a policy **newer than this Fugaro**",
		"a policy **older than this Fugaro**",
		"**empty `allow_repos`** in a published policy",
		"**a repository this machine's config names that the allow-list does not admit**",
		"**deployed rules older than the marker**",
		"**a rules marker below the generation this Fugaro reads a policy under**",
		"**deployed rules newer than this Fugaro**",
		"the eight states that need the operator",
		"`model_prices` one line per model",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/project-layer.md lacks %q", want)
		}
	}
	for _, notWant := range []string{
		"`fugaro doctor` does not yet report the policy's state",
		"`upgrade` does not yet say that a job's environment copy of the ceiling is inert",
	} {
		if strings.Contains(doc, notWant) {
			t.Errorf("docs/project-layer.md still says %q, which shipped", notWant)
		}
	}
}

// TestDocsMultiModelSaysThePolicyGovernsPrices: the prices a run uses come
// from the published policy when there is one (phase 1 task 1.6), which is
// the half of docs/multi-model.md's price section that a reader changing a
// price has to know before they edit the local config and re-run init --repo
// for every repository.
//
// It also pins what the node does *not* govern, which the sentence it used to
// carry got wrong: `providers` reaches a run through the job's own environment
// copy — cut to that repository's allow_data_to — and never from the policy
// node, so a provider change is still a reason to re-run `init --repo`. The
// sentence claimed the node governed it (review finding on this change); no
// run reads pp.Providers.
//
// Mutation (verified, run and restored): remove the "In a project that
// publishes a policy, the prices, the ceiling and the routing come from the
// policy" paragraph, or fold `providers` back into that sentence: this test
// fails.
func TestDocsMultiModelSaysThePolicyGovernsPrices(t *testing.T) {
	doc := readDoc(t, "docs/multi-model.md")
	for _, want := range []string{
		"the prices, the ceiling and the routing come from the policy",
		"fugaro project policy publish",
		"rules older than the marker `config/limits/policyRules`",
		"`providers` is the exception, and it is the one that decides what a routed call costs and where it may go",
		"still needs `fugaro init --repo` for every repository",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/multi-model.md lacks %q", want)
		}
	}
	for _, notWant := range []string{
		"the prices, the providers and the ceiling come from the policy",
	} {
		if strings.Contains(doc, notWant) {
			t.Errorf("docs/multi-model.md still says %q: a run never routes by the policy node's providers", notWant)
		}
	}
}

// TestDocsProjectLayerSaysRoutingIsApplied: a published `routing` block is no
// longer inert. The run rewrites its Claude pins from the published switch
// (`internal/runner/routing.go`, one call in `resolvePolicy`) and
// `fugaro project policy publish` writes the block from the operator's config,
// so the four places that said "decoded but not applied" described the
// *opposite* of what this release does: an operator reading them would publish
// a switch believing their ordinary Claude calls still went to Anthropic.
//
// `providers` stays in that state (a run routes by its job's own copy), so the
// two keys are separate rows now rather than one, which is also what makes the
// difference visible.
//
// Mutation (verified, run and restored): restore any of the four sentences —
// "`providers` and `routing` are decoded, validated and accepted by a run, but
// nothing applies them yet", the joined `providers`, `routing` table row,
// "this release reads and keeps it; phase 1b writes it", or the
// "nothing applies them yet" bullet in §10 — and this test fails, naming it.
func TestDocsProjectLayerSaysRoutingIsApplied(t *testing.T) {
	doc := readDoc(t, "docs/project-layer.md")
	for _, want := range []string{
		"`routing` is **applied**",
		"the run rewrites the Claude pins the published switch names",
		"the run rewrites the Claude pins the switch names — `routing.claude.models` is the opt-in list (D1a) — from the canonical ID (`claude-sonnet-5-5`) to the route's own ID (`anthropic/claude-sonnet-5.5`)",
		"**halts the run** rather than falling back to the direct path",
		"`fugaro project policy publish` writes the block from the operator's own config",
		"`budget.*`, `model_prices`, `allow_repos` and `routing` take effect in this release",
		"a run routes by the providers its own job carries (`FUGARO_MODEL_PROVIDERS`)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/project-layer.md lacks %q", want)
		}
	}
	for _, notWant := range []string{
		"`providers` and `routing` are decoded, validated and accepted by a run, but nothing applies them yet",
		"| `providers`, `routing` | decoded and accepted, **not applied**",
		"this release reads and keeps it; phase 1b writes it",
		"**`providers` and `routing` of a published policy**",
		"nothing applies them yet",
	} {
		if strings.Contains(doc, notWant) {
			t.Errorf("docs/project-layer.md still says %q, which this release made false: a published routing switch is applied by every run of the project", notWant)
		}
	}
}

// TestDocsProjectLayerShowsEveryFlagOfTheCommandsItShows is the other half of
// TestDocsProjectLayerCommandsExist: that one checks the docs' flags are real,
// and this one checks a real flag is in the docs. A flag an operator needs is
// otherwise invisible — `--remove-routing` was in neither docs/project-layer.md
// nor README.md, and the direction the old test covered cannot see that (review
// finding on this change; CONTRIBUTING requires the docs to move when a flag
// does).
//
// The scope is §7's command table, which is where the flags are shown: for each
// row, every flag the command itself declares must appear in that row. The
// exception is the set this doc does not own: the selection flags every cloud
// command carries (`--config`, `--project`, `--gcp-project`, `--region`), which
// docs/gcp-setup.md documents once for all of them, and `--help`, which is
// cobra's.
//
// Mutation (verified, run and restored): drop `[--remove-routing]` from the
// publish row (or from the command's Use line, which the row copies), and this
// test fails naming --remove-routing.
func TestDocsProjectLayerShowsEveryFlagOfTheCommandsItShows(t *testing.T) {
	const heading = "## 7. Commands"
	// The selection flags addCloudFlags puts on every cloud command: documented
	// once, in docs/gcp-setup.md, not per row here.
	sharedSelectionFlags := map[string]bool{"config": true, "project": true, "gcp-project": true, "region": true, "help": true}
	doc := readDoc(t, "docs/project-layer.md")
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("docs/project-layer.md has no %q section", heading)
	}
	section, _, _ := strings.Cut(doc[i+len(heading):], "\n## ")
	root := NewRootCmd()
	cmdRE := regexp.MustCompile("`(fugaro [^`]*)`")
	wordRE := regexp.MustCompile(`^[a-z][a-z-]*$`)
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `fugaro ") {
			continue
		}
		rows++
		m := cmdRE.FindStringSubmatch(line)
		var path []string
		for _, f := range strings.Fields(m[1])[1:] {
			if !wordRE.MatchString(f) {
				break
			}
			path = append(path, f)
		}
		if len(path) == 0 || docsProjectLayerFuture[strings.Join(path, " ")] {
			continue
		}
		c, _, err := root.Find(path)
		if err != nil {
			t.Errorf("docs/project-layer.md's %q is not a command: %v", m[1], err)
			continue
		}
		var flags []string
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if !sharedSelectionFlags[f.Name] {
				flags = append(flags, f.Name)
			}
		})
		for _, name := range slices.Sorted(slices.Values(flags)) {
			if !strings.Contains(line, "--"+name) {
				t.Errorf("docs/project-layer.md's %q row does not show --%s: a flag the operator cannot find is a flag they cannot use", m[1], name)
			}
		}
	}
	if rows < 8 {
		t.Fatalf("read %d command rows out of §7: the table has moved, so this check saw nothing", rows)
	}
}
