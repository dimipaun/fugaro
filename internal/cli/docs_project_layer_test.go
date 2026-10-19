package cli

import (
	"io/fs"
	"os"
	"path/filepath"
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
		"**a rules marker below the generation that carries the policy nodes**",
		"**a rules marker below the generation this Fugaro deploys but at or above that one**",
		"**deployed rules newer than this Fugaro**",
		"the nine states that need the operator",
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

// TestProjectPolicyPublishSaysTheDiffFollowsTheWrite: the publish prints its
// +/~/- diff after the ETag-guarded write, not before it — the diff is a claim
// about the node, and printing it first left an operator reading a change that
// did not happen on a write that failed (the review finding on this change).
// Both texts an operator reads still said "printing what changes first" after
// that ordering changed, because nothing pinned either of them; this is the
// pin, over the command's own Long and the §7 row that documents it.
//
// Mutation (verified, run and restored): put "printing what changes first" back
// in either text and this fails naming it.
func TestProjectPolicyPublishSaysTheDiffFollowsTheWrite(t *testing.T) {
	for _, tc := range []struct{ what, text string }{
		{"the publish command's help", newProjectPolicyPublishCmd().Long},
		{"the publish row of docs/project-layer.md", readDoc(t, "docs/project-layer.md")},
	} {
		if strings.Contains(tc.text, "printing what changes first") {
			t.Errorf("%s still says the diff is printed before the write: the diff follows the write, so a write that failed prints no diff", tc.what)
		}
		if !strings.Contains(tc.text, "diff follows the write") {
			t.Errorf("%s does not say when the diff is printed: name that the diff follows the write, so a failed write prints none", tc.what)
		}
		if !strings.Contains(tc.text, "prints no diff") {
			t.Errorf("%s does not say what a failed write prints: no diff at all, rather than a diff of a change that did not happen", tc.what)
		}
	}
}

// TestDocsProjectLayerStatesTheFailClosedAdmission: the two sentences of
// docs/project-layer.md that describe the phase-1 review's fail-closed
// behaviour, which the code implements and the prose did not.
//
// §5 said "A run with no Cloud Run execution — a local `fugaro exec --provider
// fake` — has no job identity to bind to, so neither check applies to it", and
// that is the fail-open reading the follow-up review of the phase-1 code
// removed: `Local`'s zero value means the cloud (internal/backend's
// platform.go), and a job whose environment was edited after launch carries no
// execution name either, so a process **no backend claims** was exempted from
// the slug↔task binding, the remote's host and the allow-list while its git
// credential stayed scoped to whatever remote that environment named.
// `admissionCheck` skips the checks for a run the CLI resolved as *local*
// (`r.d.Local && r.d.Execution == ""`) and for nothing else; a run the operator
// cannot name that way fails closed.
//
// §7's doctor paragraph said the opposite skew — deployed rules newer than this
// Fugaro — meant the binary "cannot read a policy written under them", which is
// the opposite of `projectpolicy.Read` (a marker at or above `RulesVersion`
// reads as deployed, so a run under newer rules *does* read the published
// policy) and of the sentence `doctorRulesSkew` prints for that state. An
// operator told the policy is unreadable looks for a node that is being read.
//
// Mutation (verified, run and restored): restore either old sentence and this
// test fails naming it.
func TestDocsProjectLayerStatesTheFailClosedAdmission(t *testing.T) {
	doc := readDoc(t, "docs/project-layer.md")
	for _, want := range []string{
		"Only a local invocation",
		"a process in the cloud that no backend claims",
		"a policy published under them **is** read",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/project-layer.md lacks %q: the doc must state the fail-closed admission and what Read does with newer rules", want)
		}
	}
	for _, notWant := range []string{
		"A run with no Cloud Run execution",
		"cannot read a policy written under them",
	} {
		if strings.Contains(doc, notWant) {
			t.Errorf("docs/project-layer.md still says %q, which is the fail-open reading the code does not implement", notWant)
		}
	}
}

// TestDocsProjectLayerSaysWhichGateRefusesAnOAuthLaunch: docs/project-layer.md
// is the operator-facing reference for what a repository may set, and its
// `agent.oauth_token` row and shared-job section said, until this change, that
// no profile may mount a Claude OAuth token "in this release", that every
// project job therefore runs as no account, and that an installation on
// `auth: oauth` "has no usable shared job until phase 5" — naming
// `internal/cli`'s `checkProjectJobOAuth` as the gate. Named accounts shipped
// (plan task 3): that function is gone, a profile listing an account serves
// that account's token on that account's job, and the `default` account rides
// the profile's no-account job, so the old sentences document the opposite of
// what the binary does at the feature's central point (review finding on this
// change). Nothing pinned them, so this does: the gate the doc must name, the
// condition it must state, and the sentences that must not come back.
//
// Mutation (verified, run and restored): put the reviewed sentence back into
// the `agent.oauth_token` row — "In this release no profile may mount a Claude
// OAuth token (`fugaro init` refuses it), so every project job runs as no
// account" — and this test fails on both the missing wants and the notWant.
func TestDocsProjectLayerSaysWhichGateRefusesAnOAuthLaunch(t *testing.T) {
	doc := readDoc(t, "docs/project-layer.md")
	for _, want := range []string{
		// The paragraph that replaced the shared-job table's OAuth row.
		"Claude OAuth tokens are no longer one of the missing pieces",
		"one shared job per (profile, provider, account)",
		"the `default` account rides the profile's no-account job",
		"admitted when the profile lists the account the run's own credential selects",
		// The gate that exists, on both sides.
		"`projectJobAccountFor`",
		"`checkProjectJobAccount`",
		"`checkProjectJobAccountCovers`",
		"`internal/runner`'s `checkProjectJobAccount`",
		// What a repository's own file may choose among, and where the list is.
		"`claude_accounts`",
		"`accounts.<n>.allow_repos`",
		// The one half of the old claim that is still true, kept apart from it:
		// a Claude token in a profile's `secrets:` is still refused, and the
		// profile states accounts in `claude_accounts` instead.
		"A profile that mounts a Claude OAuth token in its `secrets:` is still refused",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/project-layer.md lacks %q: the reference must name the shipped gate and the condition an OAuth launch is admitted on", want)
		}
	}
	for _, notWant := range []string{
		"checkProjectJobOAuth",
		"no profile may mount a Claude OAuth token",
		"has no usable shared job",
		"every project job runs as no account",
		"an `agent.auth: oauth` run has no token on a shared job",
		// The same claim in its three other spellings in this file.
		"it is always empty",
		"which is empty in this release",
		"no profile may mount one yet",
	} {
		if strings.Contains(doc, notWant) {
			t.Errorf("docs/project-layer.md still says %q: that was true before named accounts shipped, and the gate it names is gone", notWant)
		}
	}
}

// TestNoShippedTextNamesTheRetiredOAuthGate: `checkProjectJobOAuth` is gone
// (plan task 3 replaced it with the account gate), and a shipped file that
// still names it in prose sends a reader looking for a function that does not
// exist — the review found exactly that in internal/projectpolicy/version.go's
// version-3 note and in docs/project-layer.md's `agent.oauth_token` row. What
// is scanned is what a reader of the binary is pointed at: every non-test Go
// file under internal/ and cmd/, and the operator-facing docs directly under
// docs/ (docs/design and docs/plans are excluded on purpose: they record what
// the code was when they were written, cite line numbers, and are corrected by
// the plan that supersedes them, not by this test).
//
// Mutation (verified, run and restored): write the retired name back into
// internal/projectpolicy/version.go's comment — the file the review found it
// in — and this fails naming that file.
func TestNoShippedTextNamesTheRetiredOAuthGate(t *testing.T) {
	const retired = "checkProjectJobOAuth"
	roots := []string{"../../internal", "../../cmd", "../../docs"}
	var scanned int
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// The two historical doc trees, and nothing else, are skipped.
				if d.Name() == "design" || d.Name() == "plans" {
					return fs.SkipDir
				}
				return nil
			}
			isGo := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
			isDoc := strings.HasSuffix(path, ".md") && filepath.Dir(path) == "../../docs"
			if !isGo && !isDoc {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), retired) {
				t.Errorf("%s names %s, which this release removed: name the gate that exists (internal/cli's projectJobAccountFor, checkProjectJobAccount and checkProjectJobAccountCovers, and internal/runner's checkProjectJobAccount)", path, retired)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the tree has moved, so this check saw nothing", scanned)
	}
}
