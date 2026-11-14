package gitprov

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// The two tables below are the to-do list a second forge works from (D610,
// D611). D610 keeps GitLab out of 0.14 until it has a named need and its own
// reconciliation, and when it comes it is an L-sized change across config,
// infra, the builds, the credentials service and the skills; these tables are
// what says where the switches on a forge kind are today.
//
// The first table is Go, and TestEveryKindSwitchIsInventoried holds it complete:
// it finds a kind wherever a non-test Go file names one, as an identifier or as
// a string literal, and a file with no row fails. The second is hand-kept,
// because a yaml, a .tf, a .md or the console's TypeScript has no identifiers to
// parse: its rows name the sites outside Go, each with the line it must still
// carry, so a row that goes stale fails. Neither table sees a site nobody has
// listed yet — what makes the Go half complete is the scan, and the non-Go half
// is a list a reader has to keep, which is why the switches a third forge must
// visit are named here rather than left to be rediscovered.
//
// A file is the unit, not a line: a file may switch on the kinds in several
// places and one row covers them all, and what the row says is what the file
// does with them, because that is what a third kind has to answer again.
var forgeKindGoSites = map[string]string{
	"ee/ab/admit.go":                           "the A/B admission of a Bitbucket repository",
	"ee/coordinator/cli/coordinator.go":        "the coordinator's read-only token source and the providers it opens",
	"ee/coordinator/project.go":                "the project-wide view's read mint, and the kinds it reads",
	"internal/backend/gcp/build.go":            "the image build's credential env, its secret env and the provider it is given",
	"internal/backend/gcp/catalog.go":          "the catalog build's credential env, its secret env and the provider it is given",
	"internal/catalog/fetch.go":                "the kinds whose catalog sources this build can read (Readable)",
	"internal/claudeplugin/plan.go":            "NOT a forge kind: the plugin marketplace's source, which is GitHub whatever a repository's provider is",
	"internal/cli/catalog_provider.go":         "the credential environment a catalog build reads a Dockerfile with",
	"internal/cli/doctor.go":                   "doctor's GitHub App checks and its slug",
	"internal/cli/doctor_brokering.go":         "the repositories the brokering report reads",
	"internal/cli/exec.go":                     "--provider's admission of the two kinds beside fake",
	"internal/cli/gitcred.go":                  "`fugaro image git-credential`'s credential per provider",
	"internal/cli/image_build_catalog.go":      "the GitHub App ID a catalog build hands to infra",
	"internal/cli/imagecheck.go":               "`fugaro image check`'s credential per provider",
	"internal/cli/init.go":                     "the init flow's GitHub App check",
	"internal/cli/init_firebase.go":            "the Firebase init's GitHub repositories",
	"internal/cli/init_repo_target.go":         "init's GitHub App check on the provider a repository already names",
	"internal/cli/launch_budget.go":            "the proxied-Bitbucket read a launch makes",
	"internal/cli/project_enroll.go":           "the kind `project sync --bitbucket` lists a workspace's repositories through, and the kind each listed name is canonicalised as",
	"internal/cli/review.go":                   "the kind the App-ID gate a refusal names belongs to: the one provider whose implicit review-only job is composed only with an App ID recorded",
	"internal/cli/reviewverify.go":             "reading a pull request's head per provider",
	"internal/config/validate.go":              "config.Providers, the config-side list, and the per-provider identity checks — held to runner.jobProviderKinds by internal/cli's TestProviderKindsAgreeWithTheConfigsList",
	"internal/credsvc/bitbucket.go":            "the credentials service's admission of a Bitbucket provider",
	"internal/credsvc/bitbucketproxy.go":       "the proxied Bitbucket request's provider",
	"internal/credsvc/github.go":               "the credentials service's admission of a GitHub provider",
	"internal/credsvc/githubread.go":           "the credentials service's GitHub read mint",
	"internal/gitprov/gitprov.go":              "KindForHost and KindForURL's host table",
	"internal/gitprov/kinds.go":                "the declaration itself",
	"internal/gitprov/providers/providers.go":  "FromEnv and Proxied's admission of a kind, and the refusal's text",
	"internal/gitproxy/rules.go":               "the proxy's repository routes, whose canonical path is built from the kind",
	"internal/imagesurvey/signals.go":          "NOT a forge kind: the bitbucket-pipelines.yml marker the survey looks for",
	"internal/infra/catalogcheck.go":           "the catalog check's credential per provider",
	"internal/infra/discover_github.go":        "the kind every repository the App answers with carries",
	"internal/infra/projectjobs.go":            "githubProvider, bitbucketProvider and sharedJobProviders: the shared coding jobs only GitHub has, and the implicit review job each kind gets",
	"internal/infra/spec.go":                   "GitSecret, the git user and the credential the spec mounts",
	"internal/localcfg/select.go":              "ProviderHosts, the host each kind is reached at",
	"internal/pluginwire/remove.go":            "the plugin wire's removal, and the source kind it refuses to take a pin out of",
	"internal/pluginwire/status.go":            "the plugin wire's GitHub-only status form",
	"internal/projectpolicy/bitbucket.go":      "the published policy's Bitbucket block and its canonical slug",
	"internal/projectpolicy/profiles.go":       "the profiles' Bitbucket-only validation",
	"internal/projectpolicy/tokens_shape.go":   "the reach one Bitbucket token's coverage may name: the provider segment itself (ValidBitbucketTokenCoverage), which a second forge's tokens would spell the same way",
	"internal/repourl/cloneurl.go":             "the host a clone URL is built on per kind",
	"internal/runner/prflow.go":                "the PR flow's Bitbucket form",
	"internal/runner/project.go":               "the runner's kind for a slug, and the list the refusal names",
	"internal/runner/provision.go":             "the follow-up gate only the GitHub repositories pass",
	"internal/runner/refused.go":               "the refusals only a GitHub repository reaches",
	"internal/runner/reviewmode.go":            "review mode's Bitbucket form",
	"internal/runner/runner.go":                "the run's provider kind, its host table and NoWorkflows",
	"internal/runner/runnertest/runnertest.go": "the slug the test runner fixtures a GitHub repository with",
}

// forgeKindNonGoSites are the places outside Go where a forge kind is written
// down, each with a line it must still carry: the plugin's skills, the image
// build's credential step, Terraform's validation, its secret name and the two
// conditions only GitHub satisfies, and the schema. No scan finds these (a yaml,
// a .tf or a .md has no Go identifiers to parse), so this half is checked for
// staleness alone — each row's needle must still be there, or the row is the
// to-do list pointing at nothing.
//
// A new non-Go site is not caught by anything: this list is a reader's to keep,
// which is why it names every switch a third forge has to visit today.
//
// The console is the one such artifact that is deliberately NOT here, and a
// second forge still has to visit it: it holds a launch and a pull-request
// provider list, a per-provider link host and its own provider type. Its files
// are not paths this module may name — the module's console boundary test keeps
// every Go file from writing one down unless the file is on its allow-list, a
// list whose size is pinned so an entry is an argued widening of that boundary
// rather than a habit (plan Task 0.3 a3) — so its half of this to-do list is the
// console's to keep.
var forgeKindNonGoSites = []struct {
	path   string
	needle string
	what   string
}{
	{
		path:   "images/derived/cloudbuild.yaml",
		needle: "PROVIDER=bitbucket",
		what:   "the image build's credential step, Bitbucket form (the GitHub one is build.go's env)",
	},
	{
		path:   "deploy/terraform/gcp/modules/repo/variables.tf",
		needle: `contains(["bitbucket", "github"], var.repo.provider)`,
		what:   "the plan's validation of a repository's provider",
	},
	{
		path:   "deploy/terraform/gcp/modules/installation/variables.tf",
		needle: `app_key_secret   = optional(string, "github-app-key")`,
		what:   "the coordinator's forge credential secret name",
	},
	{
		path:   "deploy/terraform/gcp/modules/installation/variables.tf",
		needle: `alltrue([for r in var.coordinator.repos : r.provider != "github"])`,
		what:   "the coordinator's App ID, required exactly when one of the repositories it reconciles is GitHub (a coordinator whose repositories are all Bitbucket names no App)",
	},
	{
		path:   "deploy/terraform/gcp/modules/installation/coordinator.tf",
		needle: `for r in var.coordinator.repos : r.provider == "github"`,
		what:   "the repositories the coordinator mints an App token for",
	},
	{
		path:   "deploy/terraform/gcp/modules/project/job.tf",
		needle: `var.job.provider != "github" || can(regex(`,
		what:   "the App ID a project job must carry when it is GitHub's",
	},
	{
		path:   "plugin/skills/setup/reference/discovery.md",
		needle: "`github.com` is `github`, `bitbucket.org` is `bitbucket`",
		what:   "the setup skill's reading of git.provider off a remote",
	},
	{
		path:   "plugin/skills/setup/SKILL.md",
		needle: "`github-app-key` for GitHub, `bitbucket-token` for Bitbucket",
		what:   "the setup skill's credential name per provider",
	},
	{
		path:   "schemas/fugaro.schema.json",
		needle: `"provider": { "enum": ["github", "bitbucket"] }`,
		what:   "fugaro.yaml's schema for git.provider",
	},
}

// TestKindsListedOnce checks that Kinds is the whole list: every kind constant
// this package declares is in it, and each is in it once. A third forge is then
// a declaration and one entry, and a constant that is declared but not listed
// (or listed twice) fails here rather than in whichever switch forgot it.
func TestKindsListedOnce(t *testing.T) {
	declared := declaredKindConstants(t)
	if len(declared) == 0 {
		t.Fatal("no `Kind* = \"…\"` constant found in this package: the scan looks in the package's own non-test files")
	}
	listed := slices.Clone(Kinds)
	slices.Sort(listed)
	want := make([]string, 0, len(declared))
	for _, c := range declared {
		want = append(want, c.value)
	}
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("Kinds = %v, want the kinds this package declares, in any order: %v.\nA kind is declared once and listed once; a new one is both", Kinds, want)
	}
	seen := map[string]bool{}
	for _, kind := range Kinds {
		if seen[kind] {
			t.Errorf("Kinds lists %q twice", kind)
		}
		seen[kind] = true
	}
}

// TestEveryKindSwitchIsInventoried holds forgeKindGoSites to the module: every
// non-test Go file that names a forge kind — as the identifier or as the string
// — is a row, and every row is a file that still names one. It reads the syntax
// tree, not the text, so a comment that mentions one is not a hit; it reads a
// string literal, so a file that spells a kind out (config.Providers,
// runner.jobProviderKinds, a `provider == "github"`) is one, which is what makes
// the table complete for Go — a file whose only kind test is `k == "github"`
// cannot pass this guard unlisted. Two rows record a literal that is not a forge
// kind at all, because the scan cannot tell the difference and a reader can.
//
// A row pointing into a tree this checkout does not carry at all is skipped:
// CI's core-without-ee job deletes ee/ and then builds and tests the untagged
// core, and a guard that a licence-boundary workflow takes down is a guard that
// gets weakened. See absentTree.
func TestEveryKindSwitchIsInventoried(t *testing.T) {
	kinds := kindSet{}
	declared := declaredKindConstants(t)
	kinds.names = make(map[string]bool, len(declared))
	kinds.values = make(map[string]bool, len(declared))
	for _, c := range declared {
		kinds.names[c.name] = true
		kinds.values[c.value] = true
	}
	root := testutil.ModuleRoot()
	found := map[string][]string{}
	err := eachModuleGoFile(root, func(rel, path string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		uses, err := kindUses(path, kinds)
		if err != nil {
			return err
		}
		if len(uses) > 0 {
			found[rel] = uses
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatalf("no non-test Go file under %s names a forge kind: the scan is looking in the wrong place", root)
	}
	for rel, uses := range found {
		if _, ok := forgeKindGoSites[rel]; !ok {
			t.Errorf("%s names %s, and forgeKindGoSites has no row for it: add the row and say what the file does with the kinds — or that the kind it spells is not a provider kind — because a second forge has to visit it too",
				rel, strings.Join(uses, ", "))
		}
	}
	for rel := range forgeKindGoSites {
		if _, ok := found[rel]; ok {
			continue
		}
		// ee/ is deleted before the core-without-ee job builds and tests the
		// untagged core, so a row under a tree this checkout does not carry at
		// all is skipped: the row is not stale, the tree is simply not here.
		// A row whose own package was deleted is a stale row like any other,
		// and absentTree asks about the top-level tree for exactly that reason.
		if absentTree(root, rel) {
			continue
		}
		t.Errorf("forgeKindGoSites lists %s, which names no forge kind any more: drop the row, or restore the file to %s", rel, strings.Join(namesIn(kinds.names), ", "))
	}
	for _, site := range forgeKindNonGoSites {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(site.path)))
		if err != nil {
			t.Errorf("forgeKindNonGoSites lists %s (%s) and it cannot be read: %v", site.path, site.what, err)
			continue
		}
		if !strings.Contains(string(src), site.needle) {
			t.Errorf("forgeKindNonGoSites lists %s (%s) and it no longer carries %q: a third forge still has to change this site, so fix the row to the line it has now",
				site.path, site.what, site.needle)
		}
	}
}

// TestAbsentTreeSkipsOnlyAWholeMissingTree pins the rule the stale-row half of
// TestEveryKindSwitchIsInventoried rests on: a row is skipped only when the
// whole top-level tree it points into is gone (CI's core-without-ee job deletes
// ee/), and NOT when the package the row names was deleted — that is a stale
// row and has to be reported, because a listed package that disappears takes
// its switch with it and nothing else would notice.
func TestAbsentTreeSkipsOnlyAWholeMissingTree(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/runner", "deploy/terraform"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		rel  string
		want bool
		why  string
	}{
		{"ee/ab/admit.go", true, "ee/ is not in this checkout at all: the core-without-ee job deletes it"},
		{"internal/runner/runnertest/runnertest.go", false, "internal/ is here and this package is not: a deleted package is a stale row"},
		{"internal/runner/runner.go", false, "the tree is here and so is the file"},
		{"deploy/terraform/gcp/main.tf", false, "the tree is here and the file is not: a stale row"},
		{"main.go", false, "a module-root row has no tree above it that can be missing"},
	} {
		if got := absentTree(root, tc.rel); got != tc.want {
			t.Errorf("absentTree(%q) = %v, want %v: %s", tc.rel, got, tc.want, tc.why)
		}
	}
}

// absentTree reports whether a row points into a whole tree this checkout does
// not carry: the row's TOP-LEVEL directory under the module root — ee/,
// internal/, deploy/ — is not there at all. That is CI's core-without-ee job,
// which deletes ee/ and then builds and tests the untagged core: the row is not
// stale, the tree is simply not here.
//
// It asks about the top-level component and not the row's own directory on
// purpose. A package deleted outright must be reported as the stale row it is,
// and a rule that shrugged at any missing parent directory would wave exactly
// that through: rm -rf internal/runner/runnertest would leave its row silently
// unreported. See TestAbsentTreeSkipsOnlyAWholeMissingTree.
func absentTree(root, rel string) bool {
	top, _, nested := strings.Cut(rel, "/")
	if !nested {
		// A row naming a module-root file has no tree above it that can be
		// missing: the module root is always here, so the file's absence is a
		// stale row.
		return false
	}
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(top)))
	return errors.Is(err, fs.ErrNotExist)
}

// kindConst is one `Kind* = "…"` constant of this package.
type kindConst struct{ name, value string }

// declaredKindConstants reads this package's own kind constants out of its
// source, ordered by name. Reading them rather than writing them out again is
// what makes a new kind visible to both tests above without either being
// edited: the constants are the list, and every kind is named by a constant
// whose name starts with Kind.
func declaredKindConstants(t *testing.T) []kindConst {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []kindConst
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					if !strings.HasPrefix(ident.Name, "Kind") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("%s: %s: %v", name, ident.Name, err)
					}
					out = append(out, kindConst{name: ident.Name, value: value})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b kindConst) int { return strings.Compare(a.name, b.name) })
	return out
}

// kindSet is the kinds as a scan has to look for them: by the identifier a
// switch names them with, which is the spelling a constant gives, and by the
// string a file that has no constant to name spells instead. Both are the kind
// written down, and a file that writes either down is one a second forge has to
// visit.
type kindSet struct {
	names  map[string]bool
	values map[string]bool
}

// namesIn is a set of identifiers as a sorted list, for a message.
func namesIn(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// kindUses parses one Go file and answers, sorted, the kinds it writes down:
// the identifiers it names them by, and the exact string literals that spell
// one. Parsing rather than grepping is what keeps a comment out of the result,
// and reading every ast.Ident is what catches both the qualified use
// (gitprov.KindGitHub, whose Sel is an Ident) and the unqualified one inside
// this package.
//
// The literals are matched on their unquoted value, so a file that writes
// `kind == "github"` is a hit -- that is a switch on the kind like any other,
// and B3 is told to work from this table. A literal that merely contains a kind
// ("bitbucket-pipelines.yml", a URL) is not: matching whole values is what keeps
// the table a list of switches rather than a list of every string that mentions
// a forge. The literal comes back as Go source, quoted, so a message shows the
// reader what the file actually says.
func kindUses(path string, kinds kindSet) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if kinds.names[x.Name] {
				add(x.Name)
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(x.Value); err == nil && kinds.values[v] {
				add(strconv.Quote(v))
			}
		}
		return true
	})
	slices.Sort(out)
	return out, nil
}

// eachModuleGoFile walks the module and calls fn with every .go file in it, as
// a module-relative slash path and as a path to read. Dot-directories,
// underscore-directories and testdata/ are skipped, as they are for the module
// itself: none of them is a package the go tool builds.
func eachModuleGoFile(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), path)
	})
}
