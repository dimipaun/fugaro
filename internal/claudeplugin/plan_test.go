package claudeplugin

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

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
	upd := []string{"plugin", "marketplace", "update", "fugaro"}
	inst := []string{"plugin", "install", "fugaro@fugaro", "--scope", "user"}
	updU := []string{"plugin", "update", "fugaro@fugaro", "--scope", "user"}
	updP := []string{"plugin", "update", "fugaro@fugaro", "--scope", "project"}
	for _, tc := range []struct {
		name      string
		markets   []Market
		installed []Install
		want      [][]string
		before    string
	}{
		{"a fresh machine", nil, nil, [][]string{add, inst}, ""},
		{"marketplace only", pinned, nil, [][]string{upd, inst}, ""},
		{"current: nothing to do", pinned, []Install{user("0.5.2")}, nil, "0.5.2"},
		{"user install behind", pinned, []Install{user("0.5.1")}, [][]string{upd, updU}, "0.5.1"},
		{"this checkout's project install behind", pinned, []Install{user("0.5.2"), project("0.5.1", root)}, [][]string{upd, updP}, "0.5.1"},
		{"another checkout's install is not ours to update", pinned, []Install{user("0.5.2"), project("0.4.0", other)}, nil, "0.5.2"},
		{"both behind", pinned, []Install{project("0.5.0", root), user("0.5.1")}, [][]string{upd, updP, updU}, "0.5.0"},
		{"another plugin does not count", pinned, []Install{{ID: "superpowers@claude-plugins-official", Version: "6.4.1", Scope: "user"}}, [][]string{upd, inst}, ""},
		{"a managed install is left alone", pinned, []Install{{ID: "fugaro@fugaro", Version: "0.4.0", Scope: "managed"}}, [][]string{upd, inst}, ""},
		{"marketplace missing, plugin current", nil, []Install{user("0.5.2")}, [][]string{add}, "0.5.2"},
		// A marketplace with no ref follows the repository's default branch, so
		// on a release main does not carry yet it offers another plugin than
		// this binary: it is re-declared at this release's tag even when every
		// install is current (the 0.21.1 failure of 2026-11-09).
		{"unpinned marketplace, current install", unpinned, []Install{user("0.5.2")}, [][]string{add, upd}, "0.5.2"},
		{"unpinned marketplace, install behind", unpinned, []Install{user("0.5.1")}, [][]string{add, upd, updU}, "0.5.1"},
		{"marketplace at an older tag", older, []Install{user("0.5.1")}, [][]string{add, upd, updU}, "0.5.1"},
		// U10: a ref a newer fugaro wrote is never lowered, and a plugin that
		// applies is left to it: nothing to do at all.
		{"marketplace at a newer tag", newer, []Install{user("0.5.2")}, nil, "0.5.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Decide(Input{Root: root, Want: "0.5.2", Ref: "v0.5.2", Repo: "dimipaun/fugaro", Markets: tc.markets, Installed: tc.installed})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(p.Calls, tc.want, slices.Equal[[]string]) || p.Before != tc.before {
				t.Fatalf("calls %q before %q, want %q %q", p.Calls, p.Before, tc.want, tc.before)
			}
			for _, c := range p.Calls {
				if err := Allowed(c); err != nil {
					t.Errorf("a planned call is not allowed: %v", err)
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
	if err != nil || len(p.Calls) == 0 || !slices.Equal(p.Calls[0], []string{"plugin", "marketplace", "add", "--scope", "user", "someone/fugaro#v0.5.2"}) {
		t.Fatalf("%q %v", p.Calls, err)
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
	if !slices.EqualFunc(p.Calls, want, slices.Equal[[]string]) {
		t.Fatalf("calls %q, want %q", p.Calls, want)
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
	if !slices.EqualFunc(p.Calls, want, slices.Equal[[]string]) || p.Before != "0.6.0" {
		t.Fatalf("calls %q before %q, want %q 0.6.0", p.Calls, p.Before, want)
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
