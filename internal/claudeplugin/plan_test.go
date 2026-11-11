package claudeplugin

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// argsOf is a plan's calls as argument lists: what each test pins.
func argsOf(calls []Call) [][]string {
	out := make([][]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Args)
	}
	return out
}

func TestDecide(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	at := func(ref string) []Market {
		return []Market{
			{Name: "fugaro", Source: "github", Repo: "dimipaun/fugaro", Ref: ref},
			{Name: "claude-plugins-official", Source: "github", Repo: "anthropics/claude-plugins-official"},
		}
	}
	pinned, unpinned, older, newer := at("v0.5.2"), at(""), at("v0.5.1"), at("v0.6.0")
	user := func(v string) Install { return Install{ID: "fugaro@fugaro", Version: v, Scope: "user"} }
	project := func(v, at string) Install {
		return Install{ID: "fugaro@fugaro", Version: v, Scope: "project", ProjectPath: at}
	}
	add := []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro#v0.5.2"}
	// What a clear puts back: the marketplace as the release before this one
	// declared it, at the ref it had and with none when it had none.
	back := []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro"}
	backAt := func(ref string) []string { return append(slices.Clone(back[:len(back)-1]), "dimipaun/fugaro#"+ref) }
	rm := []string{"plugin", "marketplace", "remove", "fugaro", "--scope", "user"}
	upd := []string{"plugin", "marketplace", "update", "fugaro"}
	inst := []string{"plugin", "install", "fugaro@fugaro", "--scope", "user"}
	updU := []string{"plugin", "update", "fugaro@fugaro", "--scope", "user"}
	for _, tc := range []struct {
		name      string
		markets   []Market
		installed []Install
		want      [][]string
		before    string
		restore   [][]string
	}{
		{"a fresh machine", nil, nil, [][]string{add, inst}, "", nil},
		{"marketplace only", pinned, nil, [][]string{upd, inst}, "", nil},
		{"current: nothing to do", pinned, []Install{user("0.5.2")}, nil, "0.5.2", nil},
		{"user install behind", pinned, []Install{user("0.5.1")}, [][]string{upd, updU}, "0.5.1", nil},
		// A project or local install is a checkout's own settings and not this
		// step's to move (D949, U2): a stale one is not something to do, so it
		// is no call and no reason for one. It is no install of the machine's
		// own either, so it is not what Before reports: that is the user-scope
		// install, the one this step's line and its check after the calls are
		// about, and the checkout's own version is Effective's answer, which
		// nothing else reads (D942).
		{"this checkout's project install behind", pinned, []Install{user("0.5.2"), project("0.5.1", root)}, nil, "0.5.2", nil},
		{"another checkout's install is not ours to update", pinned, []Install{user("0.5.2"), project("0.4.0", other)}, nil, "0.5.2", nil},
		{"both behind", pinned, []Install{project("0.5.0", root), user("0.5.1")}, [][]string{upd, updU}, "0.5.1", nil},
		// The machine has no install of the plugin at all — the only one that
		// applies to the checkout is the checkout's own, put there by its
		// settings when the folder was trusted. That is not an install this step
		// has left alone; it is one it has none of, so the user-scope install is
		// planned even though something applies (D949, U2), which is also what
		// the check after the calls asks for.
		{"this checkout's project install alone, at this release", pinned, []Install{project("0.5.2", root)}, [][]string{upd, inst}, "", nil},
		{"another plugin does not count", pinned, []Install{{ID: "superpowers@claude-plugins-official", Version: "6.4.1", Scope: "user"}}, [][]string{upd, inst}, "", nil},
		{"a managed install is left alone", pinned, []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "managed"}}, [][]string{upd, inst}, "", nil},
		{"marketplace missing, plugin current", nil, []Install{user("0.5.2")}, [][]string{add}, "0.5.2", nil},
		// A marketplace with no ref follows the repository's default branch, so
		// on a release main does not carry yet it offers another plugin than
		// this binary: it is re-declared at this release's tag even when every
		// install is current (the 0.21.1 failure of 2026-11-09). The clear comes
		// first, because claude refuses an add of a name already declared from a
		// different source. It takes every install of the marketplace with it
		// when it leaves the name undeclared, so the plan installs the plugin at
		// user scope (what puts back the install the clear took, which is why
		// the restore is the declaration and the install, in that order) and
		// still updates every stale install where it stands, because an install
		// of one that is already there does not move it.
		{"unpinned marketplace, current install", unpinned, []Install{user("0.5.2")}, [][]string{rm, add, upd, inst}, "0.5.2", [][]string{back, inst}},
		{"unpinned marketplace, install behind", unpinned, []Install{user("0.5.1")}, [][]string{rm, add, upd, inst, updU}, "0.5.1", [][]string{back, inst}},
		{"marketplace at an older tag", older, []Install{user("0.5.1")}, [][]string{rm, add, upd, inst, updU}, "0.5.1", [][]string{backAt("v0.5.1"), inst}},
		// This checkout's own settings declare the marketplace too, so the clear
		// leaves the project install standing, at the version it was installed
		// at, and a checkout's own install is not this step's to move (D949, U2):
		// the user scope install is all the plan makes. Before is "" — the
		// machine's own install is the one it had none of.
		{"unpinned marketplace, this checkout's project install", unpinned, []Install{project("0.5.1", root)}, [][]string{rm, add, upd, inst}, "", [][]string{back, inst}},
		// Nothing installed, so the clear takes the declaration alone, and the
		// declaration alone is what a failed add puts back.
		{"unpinned marketplace, nothing installed", unpinned, nil, [][]string{rm, add, upd, inst}, "", [][]string{back}},
		// A ref that is not a release tag is not one this binary passes to
		// claude, so the clear puts the plain source back: what the marketplace
		// was before any fugaro pinned it.
		{"marketplace on a branch", at("main"), []Install{user("0.5.1")}, [][]string{rm, add, upd, inst, updU}, "0.5.1", [][]string{back, inst}},
		// U10: a ref a newer fugaro wrote is never lowered, and a plugin that
		// applies is left to it: nothing to do at all.
		{"marketplace at a newer tag", newer, []Install{user("0.5.2")}, nil, "0.5.2", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Decide(Input{Root: root, Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: tc.markets, Installed: tc.installed})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(argsOf(p.Calls), tc.want, slices.Equal[[]string]) || p.Before != tc.before {
				t.Fatalf("calls %q before %q, want %q %q", argsOf(p.Calls), p.Before, tc.want, tc.before)
			}
			if !slices.EqualFunc(p.Restore, tc.restore, slices.Equal[[]string]) {
				t.Fatalf("restore %q, want %q", p.Restore, tc.restore)
			}
			for _, args := range append(argsOf(p.Calls), p.Restore...) {
				if args != nil {
					if err := Allowed(args); err != nil {
						t.Errorf("a planned call is not allowed: %v", err)
					}
				}
			}
		})
	}
}

func TestDecideNeverReplacesAnotherMarketplace(t *testing.T) {
	for _, m := range []Market{{Name: "fugaro", Source: "github", Repo: "someone/fugaro"}, {Name: "fugaro", Source: "directory"}} {
		_, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: []Market{m}})
		var om *OtherMarketError
		if !errors.As(err, &om) || !strings.Contains(err.Error(), "never replaces it") || !strings.Contains(err.Error(), "/plugin marketplace remove fugaro") {
			t.Fatalf("%+v: %v", m, err)
		}
	}
	// GitHub names are not case-sensitive.
	if _, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: []Market{{Name: "fugaro", Source: "github", Repo: "DimiPaun/Fugaro"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestDecideFork(t *testing.T) {
	p, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "someone/fugaro"})
	if err != nil || len(p.Calls) == 0 || !slices.Equal(p.Calls[0].Args, []string{"plugin", "marketplace", "add", "--scope", "user", "someone/fugaro#v0.5.2"}) {
		t.Fatalf("%q %v", argsOf(p.Calls), err)
	}
	if _, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "-rf/x"}); err == nil {
		t.Fatal("a repository that reads as an option was planned")
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		c    int
		ok   bool
	}{
		{"0.5.2", "0.5.2", 0, true}, {"v0.5.1", "0.5.2", -1, true}, {"0.10.0", "0.9.9", 1, true},
		{"0.5", "0.5.0", 0, false}, {"01.0.0", "1.0.0", 0, false}, {"dev", "0.5.2", 0, false}, {"", "0.5.2", 0, false},
		{"-1.0.0", "0.5.2", 0, false}, {"0.5.2", "0.-1.0", 0, false}, {"1.2.3.4", "0.5.2", 0, false},
	} {
		if c, ok := Compare(tc.a, tc.b); c != tc.c || ok != tc.ok {
			t.Errorf("Compare(%q, %q) = %d %v, want %d %v", tc.a, tc.b, c, ok, tc.c, tc.ok)
		}
	}
}

func TestDecideOtherSourceSameRepoString(t *testing.T) {
	// A non-github source whose repo string equals ours is still not ours.
	for _, src := range []string{"git", "directory", ""} {
		_, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: []Market{{Name: "fugaro", Source: src, Repo: "dimipaun/fugaro"}}})
		var om *OtherMarketError
		if !errors.As(err, &om) {
			t.Errorf("source %q: err = %v, want OtherMarketError", src, err)
		}
	}
}

// Pins one update per scope; the stale dedupe in Decide is behaviourally
// equivalent to omitting it (the scopes loop already emits once per scope).
func TestDecideOneUpdatePerStaleScope(t *testing.T) {
	root := t.TempDir()
	ours := []Market{{Name: "fugaro", Source: "github", Repo: "dimipaun/fugaro", Ref: "v0.5.2"}}
	user := Install{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "user"}
	p, err := Decide(Input{Root: root, Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: ours, Installed: []Install{user, user}})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"plugin", "marketplace", "update", "fugaro"}, {"plugin", "update", "fugaro@fugaro", "--scope", "user"}}
	if !slices.EqualFunc(argsOf(p.Calls), want, slices.Equal[[]string]) {
		t.Fatalf("calls %q, want %q", argsOf(p.Calls), want)
	}
}

func TestDecideInstallNewerThanWant(t *testing.T) {
	// Documented behaviour: any install whose version differs from Want is
	// stale, newer included, so an older fugaro updates the plugin to its own
	// release rather than leaving a newer one alone.
	root := t.TempDir()
	ours := []Market{{Name: "fugaro", Source: "github", Repo: "dimipaun/fugaro", Ref: "v0.5.2"}}
	p, err := Decide(Input{Root: root, Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: ours, Installed: []Install{{ID: "fugaro@fugaro", Version: "0.6.0", Scope: "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"plugin", "marketplace", "update", "fugaro"}, {"plugin", "update", "fugaro@fugaro", "--scope", "user"}}
	if !slices.EqualFunc(argsOf(p.Calls), want, slices.Equal[[]string]) || p.Before != "0.6.0" {
		t.Fatalf("calls %q before %q, want %q 0.6.0", argsOf(p.Calls), p.Before, want)
	}
}

func TestApplyingExcludesManagedAtRoot(t *testing.T) {
	root := t.TempDir()
	got := Applying(root, []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "managed", ProjectPath: root}})
	if len(got) != 0 {
		t.Fatalf("a managed install applied: %+v", got)
	}
}

func TestApplyingNonexistentProjectPath(t *testing.T) {
	root := t.TempDir()
	got := Applying(root, []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "project", ProjectPath: root + "/does/not/exist"}})
	if len(got) != 0 {
		t.Fatalf("a nonexistent project path matched the checkout: %+v", got)
	}
}

// TestUserVersion: the machine's own install, the one Decide maintains and the
// one the step's check after the calls asks about. A checkout's project or
// local install is not it — what applies there is Effective's answer, and the
// step moves none of them (D949, U2).
func TestUserVersion(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name      string
		installed []Install
		want      string
	}{
		{"a user install", []Install{{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "user"}}, "0.5.1"},
		{"this checkout's project install alone", []Install{{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "project", ProjectPath: root}}, ""},
		{"this checkout's local install alone", []Install{{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "local", ProjectPath: root}}, ""},
		{"a project install that shadows the user one", []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "project", ProjectPath: root}, {ID: "fugaro@fugaro", Version: "0.5.1", Scope: "user"}}, "0.5.1"},
		{"another plugin", []Install{{ID: "superpowers@claude-plugins-official", Version: "1.0.0", Scope: "user"}}, ""},
		{"a managed install", []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "managed"}}, ""},
		{"nothing", nil, ""},
	} {
		if got := UserVersion(tc.installed); got != tc.want {
			t.Errorf("%s: UserVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestDecideKindsOfTheMovedPlan: the calls that carry the pin's fate are
// distinguishable from the update's own work, because the step takes their
// failures differently: a clear Claude Code has nothing to clear leaves the
// plan on its way, a declaration it refuses means the plugin cannot be had on
// this machine at all (the step reports it instead of failing the update), and
// the install and the updates after it on that path are Repair — which of them
// the clear left anything to do is not knowable before it runs, so the step
// prints their failures and lets the check after the calls decide.
func TestDecideKindsOfTheMovedPlan(t *testing.T) {
	ours := []Market{{Name: "fugaro", Source: "github", Repo: "dimipaun/fugaro"}}
	p, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro",
		Markets: ours, Installed: []Install{{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{Clear, Declare, Change, Repair, Repair}
	if len(p.Calls) != len(want) {
		t.Fatalf("calls %q, want %d", argsOf(p.Calls), len(want))
	}
	for i, k := range want {
		if p.Calls[i].Kind != k {
			t.Errorf("call %d (%q) is kind %v, want %v", i, p.Calls[i].Args, p.Calls[i].Kind, k)
		}
	}
	// A plan that declares a marketplace it has never seen is a declaration
	// too: its refusal is the same one, and it is not the update's to fail.
	fresh, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro"})
	if err != nil || len(fresh.Calls) == 0 || fresh.Calls[0].Kind != Declare {
		t.Fatalf("a fresh machine's plan is %q, %v", argsOf(fresh.Calls), err)
	}
}

// TestDecideRefusesARefThatIsNotAReleaseTag: the ref reaches claude's command
// line as part of the marketplace source, so nothing but a strict release tag
// (pluginwire.ValidTag) may get there: a branch name would follow a moving
// target, and an option-shaped ref would read as an argument of fugaro's own
// choosing.
func TestDecideRefusesARefThatIsNotAReleaseTag(t *testing.T) {
	for _, ref := range []string{"", "main", "0.5.2", "v0.5", "v0.5.2.1", "v01.5.2", "v0.5.2-rc1", "--scope", "v0.5.2#x"} {
		p, err := Decide(Input{Root: t.TempDir(), Want: "0.5.2", Ref: ref, Repo: "dimipaun/fugaro"})
		if err == nil || len(p.Calls) != 0 {
			t.Errorf("ref %q was planned as %q, want a refusal", ref, argsOf(p.Calls))
		}
	}
}
