package pluginwire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// project makes a checkout (a .git directory) and returns the path of its
// settings file, with content written when not empty.
func project(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".claude", "settings.json")
	if content != "" {
		if err := os.Mkdir(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func noUserConfig(t *testing.T) {
	t.Helper()
	old := userConfigDir
	userConfigDir = func() string { return filepath.Join(t.TempDir(), "user-claude") }
	t.Cleanup(func() { userConfigDir = old })
}

func read(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return m
}

func at(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v: %v is not an object", path, cur)
		}
		cur = mm[k]
	}
	return cur
}

// Only a strict X.Y.Z is ever compared with, and nothing that could name a
// branch, a path, a pre-release or an injection counts as a release tag.
func TestPinsOnlyStrictTags(t *testing.T) {
	noUserConfig(t)
	for _, v := range []string{"main", "0.2", "0.2.0-rc1", "0.2.0+dirty", "v0.2.0-dirty", "0.2.0\n", "../v0.2.0", "01.2.3", "1.2.3.4", "latest", `0.2.0","x":"`, " 0.2.0", "1234567890.0.0", "v v0.2.0"} {
		if _, err := Tag(v); !errors.Is(err, ErrNotRelease) {
			t.Errorf("%q: err = %v", v, err)
		}
	}
	for v, want := range map[string]string{"0.2.0": "v0.2.0", "v1.20.300": "v1.20.300", "0.0.0": "v0.0.0"} {
		if got, err := Tag(v); err != nil || got != want {
			t.Errorf("Tag(%q) = %q, %v", v, got, err)
		}
	}
	for _, ref := range []string{"v0.2.0", "v10.0.1"} {
		if !ValidTag(ref) {
			t.Errorf("ValidTag(%q)", ref)
		}
	}
	for _, ref := range []string{"main", "0.2.0", "v0.2", "v0.2.0-rc1", "v0.2.0\n", "", "refs/tags/v0.2.0"} {
		if ValidTag(ref) {
			t.Errorf("ValidTag(%q)", ref)
		}
	}
}

// checkPath and replaceFile are the write guard that stays with the read side:
// they refuse every path that is not a checkout's own .claude/settings.json.
func TestOnlyProjectSettingsJSON(t *testing.T) {
	noUserConfig(t)
	root := t.TempDir()
	for _, p := range []string{
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(root, "settings.json"),
		filepath.Join(root, "other", "settings.json"),
	} {
		if err := checkPath(p); err == nil {
			t.Errorf("%s: accepted", p)
		}
	}
	// The user's own directory is never a project, even by this name.
	user := filepath.Join(t.TempDir(), ".claude")
	if err := os.Mkdir(user, 0o755); err != nil {
		t.Fatal(err)
	}
	userConfigDir = func() string { return user }
	if err := checkPath(filepath.Join(user, "settings.json")); err == nil || !strings.Contains(err.Error(), "user settings") {
		t.Errorf("user settings: err = %v", err)
	}
	if err := replaceFile(filepath.Join(user, "settings.json"), []byte("{}"), nil, false, 0o644); err == nil {
		t.Error("wrote the user's settings")
	}
	// A checkout's own file, missing or present, is the one accepted.
	if err := checkPath(project(t, "")); err != nil {
		t.Errorf("a project's settings: %v", err)
	}
}

func TestRefusesSymlinks(t *testing.T) {
	noUserConfig(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"keep":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// settings.json a symlink out of the repository
	p := project(t, `{}`)
	_ = os.Remove(p)
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p, []byte(`{"ours":1}`), []byte(`{}`), true, 0o644); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("file symlink: err = %v", err)
	}
	// .claude a symlink to a directory elsewhere
	other := t.TempDir()
	p2 := project(t, "")
	if err := os.Symlink(other, filepath.Dir(p2)); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p2, []byte("{}"), nil, false, 0o644); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("dir symlink: err = %v", err)
	}
	if es, _ := os.ReadDir(other); len(es) != 0 {
		t.Errorf("wrote through the symlinked dir: %v", es)
	}
	if b, _ := os.ReadFile(outside); string(b) != `{"keep":true}` {
		t.Errorf("wrote through the symlink: %s", b)
	}
	// Status never follows one either.
	if r := Status(p, "0.2.0", ""); r.Pin != NotWired {
		t.Errorf("status through a symlink = %v", r.Pin)
	}
	// A directory in the file's place.
	p3 := project(t, "")
	if err := os.MkdirAll(p3, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p3, []byte("{}"), nil, false, 0o644); err == nil {
		t.Error("wrote over a directory")
	}
}

func TestFileModeKeptAndNoTempLeft(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"a":1}`)
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p, []byte("{\"a\":2}\n"), []byte(`{"a":1}`), true, 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != "{\"a\":2}\n" {
		t.Errorf("content = %s", b)
	}
	es, _ := os.ReadDir(filepath.Dir(p))
	if len(es) != 1 {
		t.Errorf("leftovers in .claude: %v", es)
	}
}

func TestConcurrentEditIsNotOverwritten(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"a":1}`)
	// Someone else edits the file after the caller read it, before the write.
	if err := os.WriteFile(p, []byte(`{"a":1,"theirs":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p, []byte(`{"ours":1}`), []byte(`{"a":1}`), true, 0o644); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"a":1,"theirs":true}` {
		t.Errorf("overwrote: %s", b)
	}
	// A file appearing after a read that found none is refused too.
	p = project(t, "")
	_ = os.Mkdir(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(`{"theirs":1}`), 0o644)
	if err := replaceFile(p, []byte(`{"ours":1}`), nil, false, 0o644); err == nil {
		t.Error("overwrote a file created meanwhile")
	}
	// Parallel writes of one file all succeed or refuse; the result is valid.
	p = project(t, `{"a":1}`)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = replaceFile(p, []byte(`{"a":1,"w":true}`), []byte(`{"a":1}`), true, 0o644)
		}()
	}
	wg.Wait()
	m := read(t, p)
	if at(t, m, "w") != true || m["a"] != float64(1) {
		t.Errorf("after parallel writes: %v", m)
	}
}

func TestLocate(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	_ = os.MkdirAll(sub, 0o755)
	if _, ok := Locate(sub); ok {
		t.Fatal("found a checkout in a plain directory")
	}
	_ = os.Mkdir(filepath.Join(root, ".git"), 0o755)
	loc, ok := Locate(sub)
	if !ok || loc.Root != root || loc.Exists || loc.Settings != filepath.Join(root, ".claude", "settings.json") {
		t.Fatalf("loc = %+v ok=%v", loc, ok)
	}
	// A worktree has .git as a file; the nearest settings below the top win.
	_ = os.MkdirAll(filepath.Join(root, "a", ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "a", ".claude", "settings.json"), []byte(`{}`), 0o644)
	loc, ok = Locate(sub)
	if !ok || !loc.Exists || loc.Root != filepath.Join(root, "a") {
		t.Fatalf("loc = %+v", loc)
	}
	// Settings above the checkout's top are not looked at.
	outer := t.TempDir()
	_ = os.MkdirAll(filepath.Join(outer, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(outer, ".claude", "settings.json"), []byte(`{}`), 0o644)
	inner := filepath.Join(outer, "repo")
	_ = os.MkdirAll(inner, 0o755)
	_ = os.WriteFile(filepath.Join(inner, ".git"), []byte("gitdir: x"), 0o644)
	if loc, ok := Locate(inner); !ok || loc.Root != inner || loc.Exists {
		t.Fatalf("loc = %+v", loc)
	}
}

func TestLocateNeverTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.Mkdir(filepath.Join(home, ".git"), 0o755) // a dotfiles repository
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0o644)
	sub := filepath.Join(home, "notes")
	_ = os.Mkdir(sub, 0o755)
	for _, d := range []string{home, sub} {
		if loc, ok := Locate(d); ok {
			t.Errorf("Locate(%s) = %+v: the home directory is not a project", d, loc)
		}
	}
}

// A repository that disables the plugin is not wired: the read side says so,
// and nothing flips it (the write that could have was deleted with the merge,
// D949).
func TestDisabledPluginIsNotWired(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`)
	if r := Status(p, "0.2.0", ""); r.Pin != NotWired || !strings.Contains(r.Detail, "disabled by the repository") {
		t.Errorf("status = %+v", r)
	}
	m := read(t, p)
	if at(t, m, "enabledPlugins", "fugaro@fugaro") != false {
		t.Error("the file changed")
	}
}

func TestPrintableEscapesTerminalControls(t *testing.T) {
	for _, in := range []string{"a\u001b[2Jb", "x\u202eevil", "a\u2066b", "nul\x00", "bell\a", "del\x7f", "line\u2028sep"} {
		out := Printable(in)
		for _, r := range out {
			if r < ' ' && r != '\t' || r == 0x7f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x2028 {
				t.Errorf("Printable(%q) = %q still has %U", in, out, r)
			}
		}
	}
	if Printable("acme/fork-1.x") != "acme/fork-1.x" || Printable("tab\there") != "tab\there" {
		t.Error("ordinary text changed")
	}
	// Repo-controlled strings reach no output raw: the report's own fields.
	noUserConfig(t)
	p := project(t, `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"`+"a\\u001b[2Jb\\u202ec"+`","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`)
	r := Status(p, "0.2.0", "")
	if r.Pin != Foreign || strings.ContainsAny(r.Repo+r.Detail, "\x1b\u202e") {
		t.Errorf("report = %+v", r)
	}
}

func TestMalformedSourceIsNotForeign(t *testing.T) {
	noUserConfig(t)
	for _, src := range []string{`"x"`, `{}`, `{"source":"github"}`, `{"source":5}`, `null`} {
		p := project(t, `{"extraKnownMarketplaces":{"fugaro":{"source":`+src+`}},"enabledPlugins":{"fugaro@fugaro":true}}`)
		r := Status(p, "0.2.0", "")
		if r.Pin != NotWired || !strings.Contains(r.Detail, "malformed source") {
			t.Errorf("%s: %+v", src, r)
		}
	}
}

func TestLocateIgnoresSettingsOutsideACheckout(t *testing.T) {
	outer := t.TempDir()
	_ = os.MkdirAll(filepath.Join(outer, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(outer, ".claude", "settings.json"), []byte(`{}`), 0o644)
	sub := filepath.Join(outer, "sub")
	_ = os.Mkdir(sub, 0o755)
	if loc, ok := Locate(sub); ok {
		t.Errorf("settings with no .git above: %+v", loc)
	}
	// With .git above the settings it is found; below it, the settings win.
	_ = os.Mkdir(filepath.Join(outer, ".git"), 0o755)
	if loc, ok := Locate(sub); !ok || !loc.Exists || loc.Root != outer {
		t.Errorf("loc = %+v", loc)
	}
}

// A hostile repository can make any string long: what is printed from its
// files (a URL, a name) is cut, in the human text and in --json alike.
func TestPrintableTruncatesHostileStrings(t *testing.T) {
	long := strings.Repeat("x", 5000)
	out := Printable(long)
	if n := len([]rune(out)); n > MaxPrintable+16 || !strings.HasSuffix(out, "(truncated)") {
		t.Fatalf("%d runes: %.40q...", n, out)
	}
	if Printable(strings.Repeat("y", MaxPrintable)) != strings.Repeat("y", MaxPrintable) {
		t.Error("a string at the limit was cut")
	}
	// Escaped controls count once each, and a cut never splits a rune.
	if out := Printable(strings.Repeat("\x1b", 5000)); len([]rune(out)) > 20*MaxPrintable || !utf8.ValidString(out) {
		t.Errorf("%d runes", len([]rune(out)))
	}
	if out := Printable(strings.Repeat("é", 5000)); !utf8.ValidString(out) || !strings.HasSuffix(out, "(truncated)") {
		t.Errorf("%q", out[:20])
	}
}

// TestPrintableNUsesItsOwnLimit: PrintableN(s, n) cuts at n runes, not at
// MaxPrintable, so a caller with its own, larger size limit (a recipe's
// use_when, bounded to 300 bytes, so never more than 300 runes) does not
// lose text Printable's own 200-rune default would have cut.
func TestPrintableNUsesItsOwnLimit(t *testing.T) {
	s := strings.Repeat("x", 300)
	if out := PrintableN(s, 300); out != s {
		t.Fatalf("300 runes at a 300 limit: got %q", out)
	}
	if out := Printable(s); !strings.HasSuffix(out, "(truncated)") {
		t.Fatalf("300 runes at the default 200 limit should still truncate: got %q", out)
	}
	if out := PrintableN(s, 10); len([]rune(out)) > 10+16 || !strings.HasSuffix(out, "(truncated)") {
		t.Fatalf("a 10 limit should cut: got %q", out)
	}
}
