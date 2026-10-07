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
		Outdated: SeverityWarning, Newer: SeverityWarning, Unpinned: SeverityWarning, Foreign: SeverityWarning, NotWired: SeverityWarning, InstalledDiffers: SeverityWarning,
		NotInstalled: SeverityInfo, CannotCompare: SeverityInfo,
	} {
		if s.Severity() != want {
			t.Errorf("%q severity = %v", s, s.Severity())
		}
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

func TestDefaultInstalledPluginsHonorsConfigDir(t *testing.T) {
	old := userConfigDir
	defer func() { userConfigDir = old }()
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	if got := DefaultInstalledPlugins(); got != "/cfg/plugins/installed_plugins.json" {
		t.Errorf("got %q", got)
	}
}
