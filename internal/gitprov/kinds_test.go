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
// what says where every one of those switches is today.
//
// The Go table is checked for completeness by TestEveryKindSwitchIsInventoried
// and the non-Go one for staleness, so neither a new switch on a forge kind nor
// a site that stopped naming one can pass unnoticed.
//
// A file that names a kind is the unit, not a line: a file may switch on the
// kinds in several places and one row covers them all, and what the row says is
// what the file does with them, because that is what a third kind has to answer
// again.
var forgeKindGoSites = map[string]string{
	"ee/ab/admit.go":                          "the A/B admission of a Bitbucket repository",
	"ee/coordinator/cli/coordinator.go":       "the coordinator's read-only token source and the providers it opens",
	"ee/coordinator/project.go":               "the project-wide view's read mint, and the kinds it reads",
	"internal/backend/gcp/build.go":           "the image build's credential env, its secret env and the provider it is given",
	"internal/backend/gcp/catalog.go":         "the catalog build's credential env, its secret env and the provider it is given",
	"internal/catalog/fetch.go":               "the kinds whose catalog sources this build can read (Readable)",
	"internal/cli/catalog_provider.go":        "the credential environment a catalog build reads a Dockerfile with",
	"internal/cli/doctor.go":                  "doctor's GitHub App checks and its slug",
	"internal/cli/doctor_brokering.go":        "the repositories the brokering report reads",
	"internal/cli/exec.go":                    "--provider's admission of the two kinds beside fake",
	"internal/cli/gitcred.go":                 "`fugaro image git-credential`'s credential per provider",
	"internal/cli/image_build_catalog.go":     "the GitHub App ID a catalog build hands to infra",
	"internal/cli/imagecheck.go":              "`fugaro image check`'s credential per provider",
	"internal/cli/init.go":                    "the init flow's GitHub App check",
	"internal/cli/init_firebase.go":           "the Firebase init's GitHub repositories",
	"internal/cli/launch_budget.go":           "the proxied-Bitbucket read a launch makes",
	"internal/cli/reviewverify.go":            "reading a pull request's head per provider",
	"internal/credsvc/bitbucket.go":           "the credentials service's admission of a Bitbucket provider",
	"internal/credsvc/github.go":              "the credentials service's admission of a GitHub provider",
	"internal/credsvc/githubread.go":          "the credentials service's GitHub read mint",
	"internal/gitprov/gitprov.go":             "KindForHost and KindForURL's host table",
	"internal/gitprov/kinds.go":               "the declaration itself",
	"internal/gitprov/providers/providers.go": "FromEnv and Proxied's admission of a kind, and the refusal's text",
	"internal/infra/catalogcheck.go":          "the catalog check's credential per provider",
	"internal/infra/spec.go":                  "GitSecret, the git user and the credential the spec mounts",
	"internal/repourl/cloneurl.go":            "the host a clone URL is built on per kind",
	"internal/runner/prflow.go":               "the PR flow's Bitbucket form",
	"internal/runner/project.go":              "the runner's kind for a slug, and the list the refusal names",
	"internal/runner/provision.go":            "the follow-up gate only the GitHub repositories pass",
	"internal/runner/refused.go":              "the refusals only a GitHub repository reaches",
	"internal/runner/reviewmode.go":           "review mode's Bitbucket form",
	"internal/runner/runner.go":               "the run's provider kind, its host table and NoWorkflows",
}

// forgeKindNonGoSites are the places outside Go where a forge kind is written
// down, each with a line it must still carry: the image build's credential
// step, Terraform's validation and secret names, the plugin skills and the
// schema. No scan finds these (a yaml or a .md has no identifiers to parse), so
// this half is checked for staleness alone — each row's needle must still be
// there, or the row is the to-do list pointing at nothing.
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
// non-test Go file that names a forge kind identifier is a row, and every row
// is a file that still names one. It reads identifiers, not text, so a comment,
// a doc string or a literal list that merely spells "github" is not a hit —
// config.Providers and runner's jobProviderKinds are literal lists of their own,
// held together by internal/cli's TestProviderKindsAgreeWithTheConfigsList.
//
// A row pointing into a tree this checkout does not carry at all is skipped:
// CI's core-without-ee job deletes ee/ and then builds and tests the untagged
// core, and a guard that a licence-boundary workflow takes down is a guard that
// gets weakened. See absentTree.
func TestEveryKindSwitchIsInventoried(t *testing.T) {
	names := map[string]bool{}
	for _, c := range declaredKindConstants(t) {
		names[c.name] = true
	}
	root := testutil.ModuleRoot()
	found := map[string][]string{}
	err := eachModuleGoFile(root, func(rel, path string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		uses, err := kindUses(path, names)
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
			t.Errorf("%s names %s, and forgeKindGoSites has no row for it: add the row and say what the file does with the kinds, because a second forge has to visit it too",
				rel, strings.Join(uses, ", "))
		}
	}
	for rel := range forgeKindGoSites {
		if _, ok := found[rel]; ok {
			continue
		}
		// ee/ is deleted before the core-without-ee job builds and tests the
		// untagged core, so a row under a tree this checkout does not carry at
		// all is skipped: the row is not stale, the directory is simply not
		// here. A row whose own directory IS here and whose file is not is a
		// stale row like any other.
		if absentTree(root, rel) {
			continue
		}
		t.Errorf("forgeKindGoSites lists %s, which names no forge kind any more: drop the row, or restore the file to %s", rel, strings.Join(namesIn(names), ", "))
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

// absentTree reports whether a row points into a part of the tree this checkout
// does not carry at all: the file is not there AND neither is its directory.
// The whole-directory case is CI's core-without-ee job, which deletes ee/ and
// then builds and tests the untagged core; the one-file case is a stale row.
func absentTree(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.Dir(filepath.FromSlash(rel))))
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

// namesIn is a set of identifiers as a sorted list, for a message.
func namesIn(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// kindUses parses one Go file and answers, sorted, the kind identifiers it
// names. Parsing rather than grepping is what keeps a comment or a string that
// spells one out of the result, and reading every ast.Ident is what catches
// both the qualified use (gitprov.KindGitHub, whose Sel is an Ident) and the
// unqualified one inside this package.
func kindUses(path string, names map[string]bool) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || !names[ident.Name] || seen[ident.Name] {
			return true
		}
		seen[ident.Name] = true
		out = append(out, ident.Name)
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
