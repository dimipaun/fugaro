package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
// Mutation (verified, run and restored): remove the "In a project that
// publishes a policy, the prices, the providers and the ceiling come from
// the policy" paragraph: this test fails.
func TestDocsMultiModelSaysThePolicyGovernsPrices(t *testing.T) {
	doc := readDoc(t, "docs/multi-model.md")
	for _, want := range []string{
		"the prices, the providers and the ceiling come from the policy",
		"fugaro project policy publish",
		"rules older than the marker `config/limits/policyRules`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/multi-model.md lacks %q", want)
		}
	}
}
