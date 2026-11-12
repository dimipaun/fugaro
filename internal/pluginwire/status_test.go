package pluginwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wired(repo, ref string, enabled bool) string {
	src := `{"source":"github","repo":"` + repo + `"`
	if ref != "" {
		src += `,"ref":"` + ref + `"`
	}
	src += `}`
	en := "true"
	if !enabled {
		en = "false"
	}
	return `{"extraKnownMarketplaces":{"fugaro":{"source":` + src + `}},"enabledPlugins":{"fugaro@fugaro":` + en + `}}`
}

func TestPinStates(t *testing.T) {
	noUserConfig(t)
	for _, tc := range []struct {
		name, content, version string
		want                   State
	}{
		{"ok", wired(Repo, "v0.2.0", true), "0.2.0", OK},
		{"outdated", wired(Repo, "v0.1.0", true), "0.2.0", Outdated},
		{"outdated minor over patch", wired(Repo, "v0.1.9", true), "0.2.0", Outdated},
		{"newer", wired(Repo, "v0.3.0", true), "0.2.0", Newer},
		{"ten is newer than nine", wired(Repo, "v0.10.0", true), "0.9.0", Newer},
		{"no ref", wired(Repo, "", true), "0.2.0", Unpinned},
		{"branch ref", wired(Repo, "main", true), "0.2.0", Unpinned},
		{"pre-release ref", wired(Repo, "v0.2.0-rc1", true), "0.2.0", Unpinned},
		{"other repo", wired("acme/fork", "v0.2.0", true), "0.2.0", Foreign},
		{"other repo and no ref", wired("acme/fork", "", true), "0.2.0", Foreign},
		{"other kind of source", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"git","url":"https://x.invalid"}}},"enabledPlugins":{"fugaro@fugaro":true}}`, "0.2.0", Foreign},
		{"no entry", `{"model":"x"}`, "0.2.0", NotWired},
		{"plugin not enabled", wired(Repo, "v0.2.0", false), "0.2.0", NotWired},
		{"plugin not listed", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}}}`, "0.2.0", NotWired},
		{"not JSON", `{`, "0.2.0", NotWired},
		{"dev binary, pinned", wired(Repo, "v0.2.0", true), "dev", CannotCompare},
		{"dev binary, unpinned", wired(Repo, "", true), "dev", Unpinned},
		{"dev binary, foreign", wired("acme/fork", "v0.2.0", true), "dev", Foreign},
		{"pre-release binary", wired(Repo, "v0.2.0", true), "0.2.0-rc1", CannotCompare},
	} {
		p := project(t, tc.content)
		r := Status(p, tc.version, "")
		if r.Pin != tc.want {
			t.Errorf("%s: pin = %q (%s), want %q", tc.name, r.Pin, r.Detail, tc.want)
		}
	}
	// No file at all.
	if r := Status(project(t, ""), "0.2.0", ""); r.Pin != NotWired {
		t.Errorf("missing: %v", r.Pin)
	}
}

func TestSeverities(t *testing.T) {
	for s, want := range map[State]Severity{
		OK: SeverityNone, "": SeverityNone,
		Outdated: SeverityWarning, Newer: SeverityWarning, Unpinned: SeverityWarning, Foreign: SeverityWarning, InstalledDiffers: SeverityWarning,
		// not wired is informational since D942: nothing in the tree writes a
		// pin any more (D949), so the normal repository is the un-wired one,
		// and a CI mode may not fail it (F21, U3 of
		// docs/plugin-user-scope/implementation-plan.md).
		NotWired:     SeverityInfo,
		NotInstalled: SeverityInfo, CannotCompare: SeverityInfo,
	} {
		if s.Severity() != want {
			t.Errorf("%q severity = %v, want %v", s, s.Severity(), want)
		}
	}
	if r := (Report{Pin: NotWired}); r.Worst() != SeverityInfo {
		t.Error("not wired must be informational: a repository that never opted in is the normal one")
	}
	if r := (Report{Pin: OK, Install: NotInstalled}); r.Worst() != SeverityInfo {
		t.Error("not installed must stay informational")
	}
	if r := (Report{Pin: OK, Install: InstalledDiffers}); r.Worst() != SeverityWarning {
		t.Error("installed differs is a warning")
	}
}

func installedFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plugins", "installed_plugins.json")
	if content != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestInstalledVersionBestEffort(t *testing.T) {
	noUserConfig(t)
	set := project(t, wired(Repo, "v0.2.0", true))
	root := filepath.Dir(filepath.Dir(set))
	entry := func(scope, version, projectPath string) string {
		return `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"` + scope + `","version":"` + version + `","projectPath":"` + projectPath + `","installPath":"/x","gitCommitSha":"abc"}]}}`
	}
	for _, tc := range []struct {
		name, content string
		want          State
	}{
		{"missing file", "", NotInstalled},
		{"same version, this project", entry("project", "0.2.0", root), OK},
		{"other version, this project", entry("project", "0.1.0", root), InstalledDiffers},
		{"user scope counts", entry("user", "0.2.0", ""), OK},
		{"other project's install", entry("project", "0.2.0", "/some/other/repo"), NotInstalled},
		{"other plugins only", `{"version":2,"plugins":{"x@y":[{"scope":"user","version":"1.0.0"}]}}`, NotInstalled},
		{"an object, not a list", `{"plugins":{"fugaro@fugaro":{"scope":"user","version":"0.2.0"}}}`, OK},
		{"empty file", " ", CannotCompare},
		{"malformed", `{"plugins":`, CannotCompare},
		{"an array", `[1,2]`, CannotCompare},
		{"no plugins key", `{"version":2}`, CannotCompare},
		{"plugins is a string", `{"plugins":"x"}`, CannotCompare},
		{"entry is a number", `{"plugins":{"fugaro@fugaro":3}}`, CannotCompare},
		{"no version", `{"plugins":{"fugaro@fugaro":[{"scope":"user"}]}}`, CannotCompare},
		{"version not semver", `{"plugins":{"fugaro@fugaro":[{"scope":"user","version":"abc"}]}}`, CannotCompare},
		{"binary garbage", "\x00\x01\x02", CannotCompare},
	} {
		r := Status(set, "0.2.0", installedFile(t, tc.content))
		if r.Pin != OK || r.Install != tc.want {
			t.Errorf("%s: pin=%q install=%q", tc.name, r.Pin, r.Install)
		}
	}
	// An unknown path, or a directory where the file should be, never fails.
	if r := Status(set, "0.2.0", ""); r.Install != CannotCompare {
		t.Errorf("no path: %q", r.Install)
	}
	// Both at once, which is where the report's two questions differ: Install
	// and Version answer about the install that applies to this checkout, whose
	// own project-scope install is taken over the machine's user-scope one
	// (D942), and UserInstall and UserVersion answer about the machine's own
	// (D949, U2) — the plugin step under --check reads those, because the
	// install it maintains is the machine's and never the checkout's.
	both := func(user, project string) string {
		return `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"` + user + `"},` +
			`{"scope":"project","version":"` + project + `","projectPath":"` + root + `"}]}}`
	}
	if r := Status(set, "0.2.0", installedFile(t, both("0.2.0", "0.1.0"))); r.Install != InstalledDiffers || r.Version != "0.1.0" || r.UserInstall != OK || r.UserVersion != "0.2.0" {
		t.Errorf("this checkout's own behind beside the machine's current: install=%q version=%q user=%q userVersion=%q", r.Install, r.Version, r.UserInstall, r.UserVersion)
	}
	if r := Status(set, "0.2.0", installedFile(t, both("0.1.0", "0.2.0"))); r.Install != OK || r.Version != "0.2.0" || r.UserInstall != InstalledDiffers || r.UserVersion != "0.1.0" {
		t.Errorf("this checkout's own current beside the machine's behind: install=%q version=%q user=%q userVersion=%q", r.Install, r.Version, r.UserInstall, r.UserVersion)
	}
	// A checkout-scoped install alone: what applies to the checkout is read,
	// and the machine has none.
	for _, e := range []string{entry("project", "0.2.0", root), entry("project", "0.1.0", root)} {
		if r := Status(set, "0.2.0", installedFile(t, e)); r.UserInstall != NotInstalled || r.UserVersion != "" {
			t.Errorf("a checkout-scoped install alone reads as the machine's own: %+v", r)
		}
	}
	// And a machine whose user-scope install is the only one: both answers are
	// that install.
	if r := Status(set, "0.2.0", installedFile(t, entry("user", "0.2.0", ""))); r.Install != OK || r.UserInstall != OK || r.UserVersion != "0.2.0" {
		t.Errorf("the machine's own install alone: %+v", r)
	}
	dir := t.TempDir()
	if r := Status(set, "0.2.0", dir); r.Install != CannotCompare {
		t.Errorf("a directory: %q", r.Install)
	}
	// A symlinked installed file is not followed.
	real := installedFile(t, entry("user", "0.2.0", ""))
	link := filepath.Join(t.TempDir(), "link.json")
	_ = os.Symlink(real, link)
	if r := Status(set, "0.2.0", link); r.Install != CannotCompare {
		t.Errorf("symlink: %q", r.Install)
	}
}

func TestNotInstalledIsInformational(t *testing.T) {
	noUserConfig(t)
	set := project(t, wired(Repo, "v0.2.0", true))
	r := Status(set, "0.2.0", installedFile(t, ""))
	if r.Pin != OK || r.Install != NotInstalled || r.Worst() != SeverityInfo {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"trust", "/plugin marketplace add dimipaun/fugaro", "/plugin install fugaro@fugaro", "restart"} {
		if !strings.Contains(NotInstalled.Fix(), want) {
			t.Errorf("fix text lacks %q: %s", want, NotInstalled.Fix())
		}
	}
	if strings.Contains(NotInstalled.Fix(), "by itself") || strings.Contains(NotInstalled.Fix(), "without a prompt") {
		t.Error("the fix still claims a silent install")
	}
}

func TestInstallNotEvaluatedWithoutAPin(t *testing.T) {
	noUserConfig(t)
	for _, c := range []string{wired(Repo, "", true), wired("acme/fork", "v0.2.0", true), `{}`} {
		if r := Status(project(t, c), "0.2.0", installedFile(t, "")); r.Install != "" {
			t.Errorf("%s: install = %q", c, r.Install)
		}
	}
}

// The machine's own install is read on exactly those checkouts, and on one with
// no settings file at all: nothing in the tree writes a pin any more (D949, U2),
// so that is what every checkout looks like now, and fugaro upgrade --check has
// to answer about the plugin on it. A report that left UserInstall unevaluated
// there would print "cannot tell" and exit 0 on a machine whose plugin is a
// release behind, and the upgrade skill would never run the install it exists to
// run.
func TestUserInstallIsReadWithoutAWiring(t *testing.T) {
	noUserConfig(t)
	behind := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.1.0"}]}}`
	for name, set := range map[string]string{"no settings file": "", "settings with no pin": `{}`} {
		t.Run(name, func(t *testing.T) {
			r := Status(project(t, set), "0.2.0", installedFile(t, behind))
			if r.Pin != NotWired {
				t.Fatalf("the fixture is wired: %+v", r)
			}
			if r.UserInstall != InstalledDiffers || r.UserVersion != "0.1.0" {
				t.Errorf("the machine's own install was not read: %+v", r)
			}
		})
	}
}

func TestDefaultInstalledPluginsHonorsConfigDir(t *testing.T) {
	old := userConfigDir
	defer func() { userConfigDir = old }()
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	if got := DefaultInstalledPlugins(); got != "/cfg/plugins/installed_plugins.json" {
		t.Errorf("got %q", got)
	}
}
