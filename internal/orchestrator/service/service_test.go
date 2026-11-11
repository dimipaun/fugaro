package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The goldens are two files, one per platform, and unit-a is the launchd one:
// a plist and a systemd unit are two formats, and one of them compared against
// the other's golden would pin nothing. Each is the whole file, because that is
// what the update step compares against what is on disk — a golden that covered
// only the lines the test read by hand would let the rest drift.
//
// The two paths are the two package managers' stable links, one per platform,
// and they are the paths the units are expected to name: Homebrew's opt link
// and not a Cellar path (see the CLI's stableBinaryPath for why).
const (
	goldenA       = "testdata/unit-a.golden"
	goldenB       = "testdata/unit-b.golden"
	darwinBinary  = "/opt/homebrew/opt/fugaro/bin/fugaro"
	linuxBinary   = "/home/linuxbrew/.linuxbrew/opt/fugaro/bin/fugaro"
	darwinHome    = "/Users/someone"
	systemdConfig = "/home/someone/.config"
)

// golden reads one golden file.
func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// mapGetenv is a getenv of a fixed environment: this package reads the home
// directory and the config directory through a function so both platforms are
// testable from one checkout, and nothing here may read the test runner's own.
func mapGetenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestLaunchdPlistMatchesTheGolden(t *testing.T) {
	u, err := Render(mapGetenv(map[string]string{"HOME": darwinHome}), "darwin", darwinBinary)
	if err != nil {
		t.Fatal(err)
	}
	if u.Text != golden(t, goldenA) {
		t.Errorf("the rendered plist is not the golden:\n--- rendered\n%s\n--- golden\n%s", u.Text, golden(t, goldenA))
	}
	if want := filepath.Join(darwinHome, "Library", "LaunchAgents", Label+".plist"); u.Path != want {
		t.Errorf("the plist's path is %s, want %s", u.Path, want)
	}
	if u.Platform != "launchd" || u.Name != Label {
		t.Errorf("the unit is %s %s, want launchd %s", u.Platform, u.Name, Label)
	}
}

func TestSystemdUnitMatchesTheGolden(t *testing.T) {
	u, err := Render(mapGetenv(map[string]string{"XDG_CONFIG_HOME": systemdConfig}), "linux", linuxBinary)
	if err != nil {
		t.Fatal(err)
	}
	if u.Text != golden(t, goldenB) {
		t.Errorf("the rendered unit is not the golden:\n--- rendered\n%s\n--- golden\n%s", u.Text, golden(t, goldenB))
	}
	if want := filepath.Join(systemdConfig, "systemd", "user", systemdName); u.Path != want {
		t.Errorf("the unit's path is %s, want %s", u.Path, want)
	}
	if u.Platform != "systemd" || u.Name != systemdName {
		t.Errorf("the unit is %s %s, want systemd %s", u.Platform, u.Name, systemdName)
	}
}

// TestUnitPointsAtTheStableBinaryPath pins the property the template exists
// for: the path is named twice — as the program to run, and as the `--binary`
// argument the daemon compares the version at — and both are the path the unit
// re-runs. A unit that named only the program would leave the daemon comparing
// its own os.Executable, which on Linux is the versioned path an upgrade
// deletes, so the check would fail after every upgrade and the daemon would
// never hand over (O8's orchestrator_version.go).
func TestUnitPointsAtTheStableBinaryPath(t *testing.T) {
	for _, tc := range []struct {
		goos, binary, env, key string
	}{
		{"darwin", darwinBinary, "HOME", darwinHome},
		{"linux", linuxBinary, "XDG_CONFIG_HOME", systemdConfig},
	} {
		u, err := Render(mapGetenv(map[string]string{tc.env: tc.key}), tc.goos, tc.binary)
		if err != nil {
			t.Fatalf("%s: %v", tc.goos, err)
		}
		if n := strings.Count(u.Text, tc.binary); n != 2 {
			t.Errorf("%s: the unit names %s %d times, want 2 (the program and --binary):\n%s", tc.goos, tc.binary, n, u.Text)
		}
		if !strings.Contains(u.Text, "--binary") {
			t.Errorf("%s: the unit names no --binary argument:\n%s", tc.goos, u.Text)
		}
	}
}

// TestUnitPinsKeepAliveForACleanExit pins the restart rule and the reason for
// it: the daemon exits 0 on purpose when the binary the unit names has moved
// on, so a unit that treated a clean exit as done — launchd's SuccessfulExit,
// systemd's on-failure — would leave the id undriven exactly when the new
// binary is waiting to take it.
func TestUnitPinsKeepAliveForACleanExit(t *testing.T) {
	a := golden(t, goldenA)
	if !strings.Contains(a, "<key>KeepAlive</key>\n\t<true/>") {
		t.Errorf("the plist does not carry a plain KeepAlive true:\n%s", a)
	}
	if strings.Contains(a, "SuccessfulExit") {
		t.Errorf("the plist conditions KeepAlive on a successful exit, which is the one exit the daemon makes on purpose:\n%s", a)
	}
	b := golden(t, goldenB)
	if !strings.Contains(b, "\nRestart=always\n") {
		t.Errorf("the unit does not carry Restart=always:\n%s", b)
	}
	if strings.Contains(b, "Restart=on-failure") || strings.Contains(b, "Restart=on-success") {
		t.Errorf("the unit conditions the restart on how the daemon exited:\n%s", b)
	}
}

// TestUnitRunsTheDaemonCommand: the unit runs `orchestrator run`, which is the
// foreground daemon and not a converge, a patch or a doctor: a login unit that
// ran anything else would write on a schedule nobody asked for.
func TestUnitRunsTheDaemonCommand(t *testing.T) {
	for _, tc := range []struct{ goos, binary, env, key string }{
		{"darwin", darwinBinary, "HOME", darwinHome},
		{"linux", linuxBinary, "XDG_CONFIG_HOME", systemdConfig},
	} {
		u, err := Render(mapGetenv(map[string]string{tc.env: tc.key}), tc.goos, tc.binary)
		if err != nil {
			t.Fatalf("%s: %v", tc.goos, err)
		}
		if !strings.Contains(u.Text, "orchestrator") || !strings.Contains(u.Text, "run") {
			t.Errorf("%s: the unit does not run `orchestrator run`:\n%s", tc.goos, u.Text)
		}
	}
}

func TestForRefusesAPlatformWithNoLoginUnitNamed(t *testing.T) {
	_, err := For("windows")
	var un *UnsupportedError
	if !errors.As(err, &un) {
		t.Fatalf("For(windows) = %v, want an UnsupportedError", err)
	}
	if un.GOOS != "windows" {
		t.Errorf("the refusal names %q, want windows", un.GOOS)
	}
	for _, want := range []string{"windows", "not supported", "launchd", "systemd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %s", want, err)
		}
	}
	if _, err := Render(mapGetenv(nil), "windows", "fugaro"); err == nil {
		t.Error("Render on windows returned no error")
	}
}

// TestSystemdQuotesAPathWithWhitespace: systemd splits ExecStart on whitespace
// with its own quoting rules, and a home directory with a space in it is
// ordinary. Every path is quoted, so the template has one shape rather than one
// per person.
func TestSystemdQuotesAPathWithWhitespace(t *testing.T) {
	u, err := Render(mapGetenv(map[string]string{"HOME": "/home/a b"}), "linux", "/opt/my tools/fugaro")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Text, `ExecStart="/opt/my tools/fugaro" orchestrator run --binary "/opt/my tools/fugaro"`) {
		t.Errorf("a path with a space is not quoted as one argument:\n%s", u.Text)
	}
	if got, want := u.Path, "/home/a b/.config/systemd/user/"+systemdName; got != want {
		t.Errorf("the unit's path is %s, want %s", got, want)
	}
}

// TestSystemdDoublesASpecifier: % is a specifier to systemd's own parser, so a
// path holding one has to be doubled or the path the unit runs is not the path
// that was written.
func TestSystemdDoublesASpecifier(t *testing.T) {
	if got, want := quoteSystemdArg("/opt/100%/fugaro"), `"/opt/100%%/fugaro"`; got != want {
		t.Errorf("quoteSystemdArg = %s, want %s", got, want)
	}
	if got, want := quoteSystemdArg(`/opt/a"b\c/fugaro`), `"/opt/a\"b\\c/fugaro"`; got != want {
		t.Errorf("quoteSystemdArg = %s, want %s", got, want)
	}
}

// TestSystemdEscapesADollarAndALineBreak: quoting is not the only rewriting
// systemd's parser does. It expands a variable in an ExecStart line whatever the
// quoting, so a path holding a dollar has to be doubled into a literal; and a
// raw newline ends the directive, so the rest of the path would be parsed as a
// directive of its own — which is how a path could name a second thing to run
// rather than the program it is.
func TestSystemdEscapesADollarAndALineBreak(t *testing.T) {
	if got, want := quoteSystemdArg("/opt/$HOME/fugaro"), `"/opt/$$HOME/fugaro"`; got != want {
		t.Errorf("quoteSystemdArg = %s, want %s", got, want)
	}
	if got, want := quoteSystemdArg("/opt/${fugaro}/fugaro"), `"/opt/$${fugaro}/fugaro"`; got != want {
		t.Errorf("quoteSystemdArg = %s, want %s", got, want)
	}
	if got, want := quoteSystemdArg("/opt/a\nb/fugaro"), `"/opt/a\nb/fugaro"`; got != want {
		t.Errorf("quoteSystemdArg = %s, want %s", got, want)
	}

	// And the template the escape ends up in: a path that would otherwise end
	// the ExecStart line stays on it — the newline is the two characters \n —
	// so no line of the unit is a directive the path carried in.
	u, err := Render(mapGetenv(map[string]string{"HOME": "/home/a"}), "linux",
		"/opt/a\nRestart=no\nExecStartPre=/bin/false\n$X/fugaro")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(u.Text, "\n") {
		for _, own := range []string{"Restart=no", "ExecStartPre="} {
			if strings.HasPrefix(line, own) {
				t.Errorf("the path carried %q into the unit as a directive of its own:\n%s", own, u.Text)
			}
		}
	}
	if got, want := len(strings.Split(u.Text, "\n")), len(strings.Split(systemdUnitTemplate, "\n")); got != want {
		t.Errorf("the unit is %d lines, and the template is %d: the path added one:\n%s", got, want, u.Text)
	}
	// The path's dollar is doubled, twice: the path is named twice, and neither
	// naming is left as one systemd would expand.
	if n := strings.Count(u.Text, "$$X/fugaro"); n != 2 {
		t.Errorf("the path's dollar is doubled %d times, and the unit names the path twice:\n%s", n, u.Text)
	}
	if n := strings.Count(u.Text, "$"); n != 4 {
		t.Errorf("the unit carries %d dollars, and four is the two doubled pairs:\n%s", n, u.Text)
	}
}

// TestLaunchdEscapesXMLText: a plist is XML, so a path holding an ampersand is
// character data and not markup. Every one of the five predefined entities is
// escaped, because a path is arbitrary text and a template that carried only
// some of them would be one escaped character away from a plist the parser
// reads as markup.
func TestLaunchdEscapesXMLText(t *testing.T) {
	u, err := Render(mapGetenv(map[string]string{"HOME": darwinHome}), "darwin", "/opt/a&b/fugaro")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Text, "<string>/opt/a&amp;b/fugaro</string>") {
		t.Errorf("the plist does not escape the path as XML text:\n%s", u.Text)
	}
	for _, tc := range []struct{ in, want string }{
		{"&", "&amp;"}, {"<", "&lt;"}, {">", "&gt;"}, {`"`, "&quot;"}, {"'", "&apos;"},
	} {
		if got := xmlText("/opt/a" + tc.in + "b/fugaro"); !strings.Contains(got, tc.want) {
			t.Errorf("xmlText(%q) = %q, which does not carry %s", tc.in, got, tc.want)
		}
	}
}

// TestRenderRefusesAnEnvironmentWithNoHome: neither platform may invent a path
// under a directory the person does not have, and the refusal names the
// variable whose shell would have decided it.
func TestRenderRefusesAnEnvironmentWithNoHome(t *testing.T) {
	if _, err := Render(mapGetenv(map[string]string{"HOME": "relative/path"}), "darwin", "fugaro"); err == nil {
		t.Error("a relative HOME was accepted on darwin")
	}
	if _, err := Render(mapGetenv(nil), "linux", "fugaro"); err == nil {
		t.Error("an environment with no HOME and no XDG_CONFIG_HOME was accepted on linux")
	}
}

// TestNothingRunsAServiceManager: the two managers' commands are only ever run
// through a context a test supplies, and neither Reload nor Remove reaches a
// real service manager without one — the whole point of the interface being
// here rather than in the CLI. A canceled context is the one call a test may
// make: it fails before exec starts anything.
func TestNothingRunsAServiceManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, goos := range []string{"darwin", "linux"} {
		m, err := For(goos)
		if err != nil {
			t.Fatal(err)
		}
		u := Unit{Platform: goos, Name: "name", Path: filepath.Join(t.TempDir(), "unit")}
		if err := m.Reload(ctx, u); err == nil {
			t.Errorf("%s: Reload with a canceled context returned no error", goos)
		}
		// Remove ignores its own failures — a unit that was never loaded has
		// nothing to remove — so what is pinned is that it returns.
		if err := m.Remove(ctx, u); err != nil {
			t.Errorf("%s: Remove with a canceled context = %v, want nil", goos, err)
		}
	}
}
