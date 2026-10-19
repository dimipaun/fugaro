package cli

// The budget commands' bare --repo: `fugaro budget set --repo` with no value
// means the repository this checkout belongs to, the reading secrets set/ls
// already give the same spelling (NormalizeRepoArgs rewrites a bare --repo to
// --repo= at the argv boundary, and the empty value is the checkout's origin).
//
// The property every test here defends is the same one: the bare form is the
// *explicit* form. `--repo` in a checkout of acme/app must write exactly the
// bytes `--repo acme/app` writes, and it must never stand for anything the
// person did not ask for -- not the project's first enrolled repository, not
// the project-wide scope. A value that is not owner/name shaped is refused,
// never reinterpreted as the checkout.

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// addRepo appends a second repository to the fixture's local config, so a
// test can tell "the checkout's repository" from "the one that was named".
func (f *budgetFixture) addRepo(t *testing.T, repo string) {
	t.Helper()
	const own = "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	f.rewriteConfig(t, own, own+"  "+repo+": { provider: github, base_branch: main, workflows: [web] }\n")
}

// checkoutWithoutOrigin makes the working directory a git checkout with no
// origin remote at all: a repository in hand that names no repository.
func checkoutWithoutOrigin(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	t.Chdir(dir)
}

// repoCapsOf reads a repository's caps node out of the fixture's database.
func repoCapsOf(t *testing.T, f *budgetFixture, slug string) map[string]any {
	t.Helper()
	m, _ := f.db.Value(budget.PathCapsRepo(slug)).(map[string]any)
	return m
}

// TestBudgetSetBareRepoIsTheCheckout: `budget set --repo --daily 5` in a
// checkout of acme/app sets acme/app's caps, with the repository's own node,
// its repo label and its slug in the printed lines -- exactly what
// `--repo acme/app` writes.
func TestBudgetSetBareRepoIsTheCheckout(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.Checkout(t, "")
	out, _, err := execute(t, "budget", "set", "--repo", "--daily", "5", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	got := repoCapsOf(t, f, appSlug)
	if got == nil {
		t.Fatalf("acme/app's caps were not written: %v", f.db.Value(budget.PathCapsRepo(appSlug)))
	}
	if got["dailyMicros"].(json.Number).String() != "5000000" || got["repo"] != "acme/app" {
		t.Fatalf("repo caps = %v", got)
	}
	for _, want := range []string{budget.PathCapsRepo(appSlug), "caps of acme/app", "daily: not set -> $5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// Nothing else moved: not the project-wide caps, not the defaults.
	for _, path := range []string{budget.PathCapsGlobal, budget.PathCapsDefaults} {
		if f.db.Value(path) != nil {
			t.Errorf("bare --repo wrote %s", path)
		}
	}
}

// TestBudgetSetBareRepoMatchesTheExplicitForm: the bare form and the value
// that names the same repository are the same command. The printed lines are
// compared byte for byte, because the display is what the operator reads
// before confirming.
func TestBudgetSetBareRepoMatchesTheExplicitForm(t *testing.T) {
	run := func(args ...string) (string, any) {
		f := newBudgetFixture(t, "")
		f.Checkout(t, "")
		out, _, err := execute(t, append([]string{"budget", "set"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out, f.db.Value(budget.PathCapsRepo(appSlug))
	}
	bare, bareNode := run("--repo", "--daily", "5", "--yes")
	named, namedNode := run("--repo", "acme/app", "--daily", "5", "--yes")
	eq, eqNode := run("--repo=acme/app", "--daily", "5", "--yes")
	if bare != named {
		t.Errorf("`--repo` printed\n%s\nand `--repo acme/app` printed\n%s", bare, named)
	}
	if bare != eq {
		t.Errorf("`--repo` printed\n%s\nand `--repo=acme/app` printed\n%s", bare, eq)
	}
	if !reflect.DeepEqual(bareNode, namedNode) || !reflect.DeepEqual(bareNode, eqNode) {
		t.Errorf("the three spellings wrote different nodes: %v / %v / %v", bareNode, namedNode, eqNode)
	}
}

// TestBudgetSetRepoValueWinsOverTheCheckout: a checkout of acme/app that is
// told `--repo acme/other` acts on acme/other. The bare form is a default,
// never an override of what was typed.
func TestBudgetSetRepoValueWinsOverTheCheckout(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.addRepo(t, "acme/other")
	f.Checkout(t, "")
	other := mustSlug("github", "acme/other")
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/other", "--daily", "5", "--yes"); err != nil {
		t.Fatal(err)
	}
	if repoCapsOf(t, f, other) == nil {
		t.Fatal("acme/other's caps were not written")
	}
	if repoCapsOf(t, f, appSlug) != nil {
		t.Fatal("--repo acme/other wrote the checkout's repository instead")
	}
}

// TestBudgetSetRepoFlagRefusesATypo: a value that is not owner/name shaped is
// refused, whichever spelling carries it. It is never read as "no value" --
// that would silently write caps for the checkout when the person meant
// another repository and mistyped it.
//
// The last case is a repository string the CLI's own rule admits (a nested
// path) but no remote can name (repoFromOrigin refuses one, so the bare form
// can never produce it): it is refused too, later, for having no provider this
// project knows. What matters here is that it is refused rather than answered
// with the checkout's repository.
func TestBudgetSetRepoFlagRefusesATypo(t *testing.T) {
	cases := []struct {
		args      []string
		saysShape bool
	}{
		{[]string{"--repo", "acme-app"}, true},
		{[]string{"--repo=acme-app"}, true},
		{[]string{"--repo", "https://github.com/acme/app"}, true},
		{[]string{"--repo", "acme/"}, true},
		{[]string{"--repo", "group/sub/app"}, false},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.Checkout(t, "")
			_, _, err := execute(t, append([]string{"budget", "set"}, append(tc.args, "--daily", "5", "--yes")...)...)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if tc.saysShape && !strings.Contains(err.Error(), "owner/name") {
				t.Errorf("the refusal does not say what a repository looks like: %v", err)
			}
			if repoCapsOf(t, f, appSlug) != nil {
				t.Error("a mistyped --repo wrote the checkout's repository anyway")
			}
		})
	}
}

// TestBudgetSetBareRepoWithoutACheckoutRefuses: outside a checkout, or in one
// whose origin names no repository, there is nothing the bare form can mean.
// It refuses with the fix and writes nothing anywhere -- above all it never
// falls back to the project-wide scope, which is the one thing a person who
// typed --repo did not ask for.
func TestBudgetSetBareRepoWithoutACheckoutRefuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"not a checkout", func(t *testing.T) { t.Chdir(t.TempDir()) }},
		{"a checkout with no origin", checkoutWithoutOrigin},
		{"an origin that names no repository", func(t *testing.T) {
			testutil.IsolateGit(t)
			dir := t.TempDir()
			testutil.Git(t, dir, "init", "-q")
			testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/group/sub/app")
			t.Chdir(dir)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			tc.setup(t)
			_, _, err := execute(t, "budget", "set", "--repo", "--daily", "5", "--yes")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), "pass --repo owner/name") {
				t.Errorf("the refusal does not name the fix: %v", err)
			}
			for _, path := range []string{budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathCapsRepo(appSlug)} {
				if f.db.Value(path) != nil {
					t.Errorf("a refused bare --repo wrote %s", path)
				}
			}
		})
	}
}

// TestBudgetSetRepoAndGlobalStillRefuseTogether: the bare form is still the
// --repo scope. `--global --repo` names two scopes whichever way --repo is
// spelled, and one of them must never be picked for the person.
func TestBudgetSetRepoAndGlobalStillRefuseTogether(t *testing.T) {
	for _, args := range [][]string{
		{"--global", "--repo", "--daily", "5", "--yes"},
		{"--global", "--repo", "acme/app", "--daily", "5", "--yes"},
		{"--defaults", "--repo", "--daily", "5", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.Checkout(t, "")
			_, _, err := execute(t, append([]string{"budget", "set"}, args...)...)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), "name one scope") {
				t.Errorf("the refusal does not say a scope must be named: %v", err)
			}
			if f.db.Value(budget.PathCapsGlobal) != nil || repoCapsOf(t, f, appSlug) != nil {
				t.Error("two scopes together wrote something")
			}
		})
	}
	// A bare --repo alone is one scope, and needs no other flag to be one.
	f := newBudgetFixture(t, "")
	f.Checkout(t, "")
	if _, _, err := execute(t, "budget", "set", "--repo", "--daily", "5", "--yes"); err != nil {
		t.Fatalf("a bare --repo on its own was not read as a scope: %v", err)
	}
}

// TestBudgetKillResumeBareRepoIsTheCheckout: kill and resume take --repo the
// same way set does, so the three budget subcommands that name one repository
// agree on what a bare --repo means.
func TestBudgetKillResumeBareRepoIsTheCheckout(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.Checkout(t, "")
	out, _, err := execute(t, "budget", "kill", "--repo", "--reason", "runaway")
	if err != nil {
		t.Fatal(err)
	}
	k, _ := f.db.Value(budget.PathKillRepo(appSlug)).(map[string]any)
	if k == nil || k["on"] != true || k["reason"] != "runaway" {
		t.Fatalf("kill switch = %v", k)
	}
	if !strings.Contains(out, "repository acme/app") {
		t.Errorf("kill did not name the checkout's repository:\n%s", out)
	}
	if f.db.Value(budget.PathKillGlobal) != nil {
		t.Error("a bare --repo kill set the project-wide switch")
	}
	out, _, err = execute(t, "budget", "resume", "--repo", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := f.db.Value(budget.PathKillRepo(appSlug)).(map[string]any); k["on"] != false {
		t.Fatalf("switch after resume = %v", k)
	}
	if !strings.Contains(out, "repository acme/app") {
		t.Errorf("resume did not name the checkout's repository:\n%s", out)
	}
	// --all and a bare --repo still name two scopes.
	if _, _, err := execute(t, "budget", "kill", "--all", "--repo"); err == nil {
		t.Fatal("--all with a bare --repo was accepted")
	}
}

// TestBudgetKillBareRepoWithoutACheckoutRefuses: the refusal and its fix are
// the same for kill and resume as for set.
func TestBudgetKillBareRepoWithoutACheckoutRefuses(t *testing.T) {
	for _, verb := range []string{"kill", "resume"} {
		t.Run(verb, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			t.Chdir(t.TempDir())
			_, _, err := execute(t, "budget", verb, "--repo", "--yes")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), "pass --repo owner/name") {
				t.Errorf("the refusal does not name the fix: %v", err)
			}
			if f.db.Value(budget.PathKillGlobal) != nil || f.db.Value(budget.PathKillRepo(appSlug)) != nil {
				t.Error("a refused bare --repo wrote a kill switch")
			}
		})
	}
}

// TestBudgetNormalizeRepoArgs is the argv rewrite's budget half: a bare --repo
// after `budget set`, `budget kill` or `budget resume` becomes --repo= (which
// the commands read as the checkout's repository), the space-separated form
// every script writes is left alone, and nothing outside those three verbs is
// touched -- show and preview keep taking owner/name, and `--repo` before the
// verb is not this flag at all.
func TestBudgetNormalizeRepoArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"bare set", []string{"budget", "set", "--repo", "--daily", "5"},
			[]string{"budget", "set", "--repo=", "--daily", "5"}},
		{"bare set at the end", []string{"budget", "set", "--repo"},
			[]string{"budget", "set", "--repo="}},
		{"with a value", []string{"budget", "set", "--repo", "acme/app", "--daily", "5"},
			[]string{"budget", "set", "--repo", "acme/app", "--daily", "5"}},
		{"equals form", []string{"budget", "set", "--repo=acme/app", "--daily", "5"},
			[]string{"budget", "set", "--repo=acme/app", "--daily", "5"}},
		{"a typo is left for the command to refuse", []string{"budget", "set", "--repo", "acme-app"},
			[]string{"budget", "set", "--repo", "acme-app"}},
		{"bare kill", []string{"budget", "kill", "--repo", "--reason", "x"},
			[]string{"budget", "kill", "--repo=", "--reason", "x"}},
		{"bare resume", []string{"budget", "resume", "--repo"},
			[]string{"budget", "resume", "--repo="}},
		{"bare followed by --", []string{"budget", "set", "--repo", "--"},
			[]string{"budget", "set", "--repo=", "--"}},
		{"after -- it is positional", []string{"budget", "set", "--", "--repo"},
			[]string{"budget", "set", "--", "--repo"}},
		{"global flags stay put", []string{"--config", "/tmp/c.yaml", "budget", "set", "--repo"},
			[]string{"--config", "/tmp/c.yaml", "budget", "set", "--repo="}},
		{"show keeps its required value", []string{"budget", "show", "--repo"},
			[]string{"budget", "show", "--repo"}},
		{"preview keeps its required value", []string{"budget", "preview", "--repo", "--recipe", "standard"},
			[]string{"budget", "preview", "--repo", "--recipe", "standard"}},
		{"another budget verb", []string{"budget", "prices", "--repo"},
			[]string{"budget", "prices", "--repo"}},
		{"no verb after budget", []string{"budget", "--repo"},
			[]string{"budget", "--repo"}},
		{"the secrets family is untouched by this half", []string{"secrets", "ls", "--repo"},
			[]string{"secrets", "ls", "--repo="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeRepoArgs(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeRepoArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBudgetBoolFlagsMatchTheFlags keeps budgetBoolFlags equal to the flags
// the budget commands declare with a NoOptDefVal, the invariant
// TestSecretsBoolFlagsMatchTheFlags keeps for the secrets family: the argv
// scan decides from that map whether the token after a long flag is its value,
// and pflag decides the same question from the flag's own NoOptDefVal. A bool
// flag missing from the map is read as taking a value, which moves the verb
// the scan looks for. --help is the one entry the commands do not declare
// here: cobra adds it to every command at Execute time, with the same
// NoOptDefVal.
func TestBudgetBoolFlagsMatchTheFlags(t *testing.T) {
	declared := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.NoOptDefVal != "" {
				declared["--"+f.Name] = true
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newBudgetCmd())
	if len(declared) == 0 {
		t.Fatal("read no no-value flags out of the budget commands: the walk saw nothing, so this check proves nothing")
	}
	for name := range declared {
		if !budgetBoolFlags[name] {
			t.Errorf("the budget commands declare %s with a NoOptDefVal (it takes no value), and the argv scan does not know it: a token after it would be read as its value", name)
		}
	}
	for name := range budgetBoolFlags {
		if name == "--help" {
			continue
		}
		if !declared[name] {
			t.Errorf("budgetBoolFlags lists %s, which the budget commands do not declare as a flag that takes no value", name)
		}
	}
}

// TestBudgetRepoFlagHelpSaysWhichSubcommands: the help text is where a person
// finds out that --repo takes no value, and which subcommands read it that
// way. show and preview do not, and the reader must not be left to guess.
func TestBudgetRepoFlagHelpSaysWhichSubcommands(t *testing.T) {
	for _, name := range []string{"set", "kill", "resume"} {
		cmd, _, err := NewRootCmd().Find([]string{"budget", name})
		if err != nil {
			t.Fatal(err)
		}
		f := cmd.Flags().Lookup("repo")
		if f == nil || !strings.Contains(f.Usage, "checkout") {
			t.Errorf("budget %s --help does not say a bare --repo means this checkout: %q", name, f.Usage)
		}
		if !strings.Contains(cmd.Long, "no value") {
			t.Errorf("budget %s --help does not say --repo takes no value:\n%s", name, cmd.Long)
		}
		if !strings.Contains(cmd.Use, "[owner/name]") {
			t.Errorf("budget %s usage line does not show --repo's value as optional: %q", name, cmd.Use)
		}
	}
	// The two that do not take it that way say what they do take.
	for _, name := range []string{"show", "preview"} {
		cmd, _, err := NewRootCmd().Find([]string{"budget", name})
		if err != nil {
			t.Fatal(err)
		}
		f := cmd.Flags().Lookup("repo")
		if f == nil || strings.Contains(f.Usage, "no value") {
			t.Errorf("budget %s --repo claims the optional form: %q", name, f.Usage)
		}
	}
}
