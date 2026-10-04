package pluginwire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

func TestMergeIntoEmptyFile(t *testing.T) {
	noUserConfig(t)
	for name, content := range map[string]string{"missing": "", "empty": "\n"} {
		p := project(t, content)
		if content != "" {
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Wire(p, "0.2.0", false); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := read(t, p)
		if got := at(t, m, "extraKnownMarketplaces", "fugaro", "source", "ref"); got != "v0.2.0" {
			t.Errorf("%s: ref = %v", name, got)
		}
		if at(t, m, "extraKnownMarketplaces", "fugaro", "source", "repo") != "dimipaun/fugaro" || at(t, m, "extraKnownMarketplaces", "fugaro", "source", "source") != "github" {
			t.Errorf("%s: source = %v", name, at(t, m, "extraKnownMarketplaces", "fugaro"))
		}
		if at(t, m, "enabledPlugins", "fugaro@fugaro") != true {
			t.Errorf("%s: not enabled", name)
		}
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode = %v", name, fi.Mode())
		}
	}
}

func TestMergeKeepsEveryOtherKey(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"permissions":{"allow":["Bash(go test:*)"],"deny":[]},"env":{"A":"1"},"model":"opus","n":1.50,"list":[1,2,{"x":null}],"extraKnownMarketplaces":{}}`)
	if _, err := Wire(p, "0.2.0", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	for _, want := range []string{`"Bash(go test:*)"`, `"A": "1"`, `"model": "opus"`, `1.50`, `"x": null`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lost %s:\n%s", want, b)
		}
	}
	// Key order is kept: unrelated keys first, in their order.
	if strings.Index(string(b), `"permissions"`) > strings.Index(string(b), `"env"`) || strings.Index(string(b), `"env"`) > strings.Index(string(b), `"model"`) {
		t.Errorf("key order changed:\n%s", b)
	}
}

func TestMergeKeepsOtherMarketplacesAndPlugins(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"extraKnownMarketplaces":{"acme":{"source":{"source":"github","repo":"acme/tools"}}},"enabledPlugins":{"acme@acme":true,"old@x":false}}`)
	if _, err := Wire(p, "0.2.0", false); err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	if at(t, m, "extraKnownMarketplaces", "acme", "source", "repo") != "acme/tools" {
		t.Error("lost the other marketplace")
	}
	if at(t, m, "enabledPlugins", "acme@acme") != true || at(t, m, "enabledPlugins", "old@x") != false {
		t.Error("lost the other plugins")
	}
	if at(t, m, "enabledPlugins", "fugaro@fugaro") != true {
		t.Error("not enabled")
	}
}

func TestMergeNeverRewritesInvalidJSON(t *testing.T) {
	noUserConfig(t)
	for name, content := range map[string]string{
		"comment":         "{\n // keep\n \"a\": 1\n}",
		"trailing comma":  `{"a":1,}`,
		"truncated":       `{"a":`,
		"array":           `[]`,
		"string":          `"x"`,
		"null":            `null`,
		"duplicate key":   `{"enabledPlugins":{},"enabledPlugins":{"x":true}}`,
		"marketplaces":    `{"extraKnownMarketplaces":[]}`,
		"entry":           `{"extraKnownMarketplaces":{"fugaro":"x"}}`,
		"no source":       `{"extraKnownMarketplaces":{"fugaro":{}}}`,
		"source string":   `{"extraKnownMarketplaces":{"fugaro":{"source":"x"}}}`,
		"github no repo":  `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github"}}}}`,
		"plugins array":   `{"enabledPlugins":[]}`,
		"plugins null":    `{"enabledPlugins":null}`,
		"trailing object": `{"a":1}{"b":2}`,
	} {
		p := project(t, content)
		_, err := Wire(p, "0.2.0", false)
		if !IsInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
		if b, _ := os.ReadFile(p); string(b) != content {
			t.Errorf("%s: the file was rewritten: %q", name, b)
		}
	}
}

func TestForeignMarketplaceKeptNotRewritten(t *testing.T) {
	noUserConfig(t)
	// A fork's repository is kept; only its ref moves, other fields stay.
	content := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"acme/fugaro-fork","ref":"v0.1.0"},"autoUpdate":false}}}`
	p := project(t, content)
	// Without explicit permission the fork's ref is not moved, nothing is written.
	var fk *ForkError
	if _, err := Wire(p, "0.2.0", false); !errors.As(err, &fk) || fk.Repo != "acme/fugaro-fork" {
		t.Fatalf("without allowFork: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != content {
		t.Fatalf("rewritten without allowFork: %s", b)
	}
	ch, err := Wire(p, "0.2.0", true)
	if err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	if at(t, m, "extraKnownMarketplaces", "fugaro", "source", "repo") != "acme/fugaro-fork" || at(t, m, "extraKnownMarketplaces", "fugaro", "source", "ref") != "v0.2.0" {
		t.Errorf("entry = %v", at(t, m, "extraKnownMarketplaces", "fugaro"))
	}
	if at(t, m, "extraKnownMarketplaces", "fugaro", "autoUpdate") != false {
		t.Error("lost a field of the entry")
	}
	if !strings.Contains(ch.Note, "acme/fugaro-fork") || ch.Foreign != "acme/fugaro-fork" {
		t.Errorf("note = %q foreign = %q", ch.Note, ch.Foreign)
	}
	// A fork already at our tag changes nothing but is still reported.
	if ch, err := Plan(p, "0.2.0", false); err != nil || ch.Changed || ch.Foreign != "acme/fugaro-fork" {
		t.Errorf("fork at the tag: %v %+v", err, ch)
	}
	// Any other kind of source is not ours to rewrite: nothing is written.
	for _, content := range []string{
		`{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"git","url":"https://evil.invalid/x.git"}}}}`,
		`{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"directory","path":"/tmp/x"}}}}`,
	} {
		p := project(t, content)
		_, err := Wire(p, "0.2.0", false)
		var fe *ForeignError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v", content, err)
		}
		if b, _ := os.ReadFile(p); string(b) != content {
			t.Errorf("rewritten: %s", b)
		}
	}
}

func TestWireIdempotent(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"model":"opus"}`)
	if _, err := Wire(p, "0.2.0", false); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(p)
	ch, err := Wire(p, "0.2.0", false)
	if err != nil || ch.Changed || ch.Diff() != "" {
		t.Fatalf("second run: %v changed=%v", err, ch != nil && ch.Changed)
	}
	if second, _ := os.ReadFile(p); string(second) != string(first) {
		t.Error("the second run rewrote the file")
	}
	// An already-wired file in someone's own formatting is not reformatted.
	own := `{"enabledPlugins": {"fugaro@fugaro": true},
	"extraKnownMarketplaces": {"fugaro": {"source": {"source": "github", "repo": "dimipaun/fugaro", "ref": "v0.2.0"}}}}`
	p = project(t, own)
	if ch, err := Wire(p, "0.2.0", false); err != nil || ch.Changed {
		t.Fatalf("wired file: %v %v", err, ch)
	}
	if b, _ := os.ReadFile(p); string(b) != own {
		t.Error("reformatted a wired file")
	}
	// Moving the ref up and "down" (a lower binary) are both only a ref.
	ch, err = Wire(p, "0.3.0", false)
	if err != nil || !ch.Changed || !strings.Contains(ch.Diff(), `"ref": "v0.3.0"`) {
		t.Fatalf("bump: %v\n%s", err, ch.Diff())
	}
}

func TestDevBinaryWritesNoPin(t *testing.T) {
	noUserConfig(t)
	p := project(t, "")
	if _, err := Wire(p, "dev", false); !errors.Is(err, ErrDev) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(p)); !os.IsNotExist(err) {
		t.Error("a dev binary created .claude")
	}
	p = project(t, `{"a":1}`)
	if _, err := Wire(p, "", false); !errors.Is(err, ErrDev) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"a":1}` {
		t.Errorf("rewritten: %s", b)
	}
	if !strings.Contains(Snippet("dev"), "<release tag>") {
		t.Error("dev snippet has no placeholder")
	}
}

// Only a strict X.Y.Z is ever pinned: nothing that could name a branch, a
// path, a pre-release or an injection reaches the file.
func TestPinsOnlyStrictTags(t *testing.T) {
	noUserConfig(t)
	for _, v := range []string{"main", "0.2", "0.2.0-rc1", "0.2.0+dirty", "v0.2.0-dirty", "0.2.0\n", "../v0.2.0", "01.2.3", "1.2.3.4", "latest", `0.2.0","x":"`, " 0.2.0", "1234567890.0.0", "v v0.2.0"} {
		p := project(t, "")
		_, err := Wire(p, v, false)
		if !errors.Is(err, ErrNotRelease) {
			t.Errorf("%q: err = %v", v, err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%q: wrote a file", v)
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
	if _, err := Wire(p, "0.2.0", false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("file symlink: err = %v", err)
	}
	// .claude a symlink to a directory elsewhere
	other := t.TempDir()
	p2 := project(t, "")
	if err := os.Symlink(other, filepath.Dir(p2)); err != nil {
		t.Fatal(err)
	}
	if _, err := Wire(p2, "0.2.0", false); err == nil || !strings.Contains(err.Error(), "symlink") {
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
	// A directory, or a fifo-like non-regular file, in the file's place.
	p3 := project(t, "")
	if err := os.MkdirAll(p3, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Wire(p3, "0.2.0", false); err == nil {
		t.Error("wrote over a directory")
	}
}

func TestOnlyProjectSettingsJSON(t *testing.T) {
	noUserConfig(t)
	root := t.TempDir()
	for _, p := range []string{
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(root, "settings.json"),
		filepath.Join(root, "other", "settings.json"),
	} {
		if _, err := Wire(p, "0.2.0", false); err == nil {
			t.Errorf("%s: wired", p)
		}
	}
	// The user's own directory is never a project, even by this name.
	user := filepath.Join(t.TempDir(), ".claude")
	if err := os.Mkdir(user, 0o755); err != nil {
		t.Fatal(err)
	}
	userConfigDir = func() string { return user }
	if _, err := Wire(filepath.Join(user, "settings.json"), "0.2.0", false); err == nil || !strings.Contains(err.Error(), "user settings") {
		t.Errorf("user settings: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(user, "settings.json")); !os.IsNotExist(err) {
		t.Error("wrote the user's settings")
	}
}

func TestFileModeKeptAndNoTempLeft(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"a":1}`)
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Wire(p, "0.2.0", false); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
	es, _ := os.ReadDir(filepath.Dir(p))
	if len(es) != 1 {
		t.Errorf("leftovers in .claude: %v", es)
	}
}

func TestConcurrentEditIsNotOverwritten(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"a":1}`)
	ch, err := Plan(p, "0.2.0", false)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else edits the file after the plan, before the write.
	if err := os.WriteFile(p, []byte(`{"a":1,"theirs":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ch.Apply(); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"a":1,"theirs":true}` {
		t.Errorf("overwrote: %s", b)
	}
	// The file appearing after a plan against a missing one is refused too.
	p = project(t, "")
	ch, err = Plan(p, "0.2.0", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Mkdir(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(`{"theirs":1}`), 0o644)
	if err := ch.Apply(); err == nil {
		t.Error("overwrote a file created meanwhile")
	}
	// Parallel wires of one file all succeed or refuse; the result is valid.
	p = project(t, `{"a":1}`)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = Wire(p, "0.2.0", false) }()
	}
	wg.Wait()
	m := read(t, p)
	if at(t, m, "extraKnownMarketplaces", "fugaro", "source", "ref") != "v0.2.0" || m["a"] != float64(1) {
		t.Errorf("after parallel wires: %v", m)
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

func TestLineDiff(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"a":1}`)
	ch, err := Plan(p, "0.2.0", false)
	if err != nil {
		t.Fatal(err)
	}
	d := ch.Diff()
	if !strings.Contains(d, `+   "extraKnownMarketplaces": {`) || !strings.Contains(d, `- {"a":1}`) || !strings.Contains(d, `"v0.2.0"`) {
		t.Errorf("diff:\n%s", d)
	}
}

func TestDisabledPluginStaysDisabled(t *testing.T) {
	noUserConfig(t)
	p := project(t, `{"enabledPlugins":{"fugaro@fugaro":false}}`)
	ch, err := Wire(p, "0.2.0", false)
	if err != nil || !ch.Disabled || !strings.Contains(ch.Note, "disabled by this repository") {
		t.Fatalf("%v %+v", err, ch)
	}
	m := read(t, p)
	if at(t, m, "enabledPlugins", "fugaro@fugaro") != false {
		t.Error("flipped a disabled plugin to enabled")
	}
	if r := Status(p, "0.2.0", ""); r.Pin != NotWired || !strings.Contains(r.Detail, "disabled by the repository") {
		t.Errorf("status = %+v", r)
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
	// Repo-controlled strings reach no output raw: report, note and diff.
	noUserConfig(t)
	evil := "a\u001b[2Jb\u202ec"
	p := project(t, `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"`+"a\\u001b[2Jb\\u202ec"+`","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`)
	r := Status(p, "0.2.0", "")
	if r.Pin != Foreign || strings.ContainsAny(r.Repo+r.Detail, "\x1b\u202e") {
		t.Errorf("report = %+v", r)
	}
	ch, err := Wire(p, "0.2.0", true)
	if err != nil || strings.ContainsAny(ch.Note+ch.Diff()+ch.Foreign, "\x1b\u202e") {
		t.Errorf("note/diff unescaped: %v %q %q", err, ch.Note, ch.Diff())
	}
	_ = evil
	// A raw bidi character inside a JSON string is escaped in the diff too.
	p = project(t, "{\"k\": \"x\u202ey\"}")
	ch, _ = Plan(p, "0.2.0", false)
	if strings.ContainsRune(ch.Diff(), 0x202e) {
		t.Error("the diff has a raw bidi override")
	}
	fe := (&ForeignError{Path: "p", Kind: "g\u001bit"}).Error()
	if strings.Contains(fe, "\x1b") {
		t.Error("ForeignError unescaped")
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

func TestDiffIsBounded(t *testing.T) {
	big := make([]string, 5000)
	for i := range big {
		big[i] = `"k` + strings.Repeat("x", i%7) + `": 1`
	}
	d := lineDiff(big, big[:4999], true)
	if !strings.Contains(d, "too long to diff") {
		t.Errorf("diff = %.80s", d)
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
