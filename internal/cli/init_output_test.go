package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/shellword"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/init_commands.golden")

// shellWord is one word of a printed command: the text as printed, the value
// a shell would read, and whether it was quoted.
type shellWord struct {
	raw, value string
	quoted     bool
}

// splitCommand reads line as a POSIX shell would for the cases init prints:
// single-quoted runs and '\” escapes. A chain operator, a pipe, a command
// substitution, a backslash or a newline outside the quotes is reported.
func splitCommand(line string) (words []shellWord, bad string) {
	var raw, val strings.Builder
	inWord, quoted, inQ := false, false, false
	flush := func() {
		if inWord {
			words = append(words, shellWord{raw.String(), val.String(), quoted})
		}
		raw.Reset()
		val.Reset()
		inWord, quoted = false, false
	}
	if strings.ContainsAny(line, "\r\n") {
		return nil, "a line break"
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQ:
			raw.WriteByte(c)
			if c == '\'' {
				inQ = false
			} else {
				val.WriteByte(c)
			}
		case c == '\'':
			inWord, quoted, inQ = true, true, true
			raw.WriteByte(c)
		case c == '\\' && inWord && quoted && i+1 < len(line) && line[i+1] == '\'':
			// quoteWord's '\'' : the closed quote, an escaped quote, the next ' reopens
			raw.WriteString(`\'`)
			val.WriteByte('\'')
			i++
		case c == '\\':
			return nil, "a backslash"
		case c == ' ':
			flush()
		case strings.ContainsRune(";&|`", rune(c)) || (c == '$' && i+1 < len(line) && line[i+1] == '('):
			return nil, fmt.Sprintf("the operator %q", string(c))
		default:
			inWord = true
			raw.WriteByte(c)
			val.WriteByte(c)
		}
	}
	if inQ {
		return nil, "an unclosed quote"
	}
	flush()
	return words, ""
}

var (
	flagRE        = regexp.MustCompile(`^--?[a-z][a-z0-9-]*(=[A-Za-z0-9_./:@%+=,<>-]*)?$`)
	placeholderRE = regexp.MustCompile(`<[a-z][a-z -]*>`)
)

// checkPrinted fails unless line is one command, one line, starting with one
// of the expected programs, every value in it a safe or quoted shell word.
func checkPrinted(t *testing.T, where, line string, programs ...string) {
	t.Helper()
	words, bad := splitCommand(line)
	if bad != "" {
		t.Errorf("%s: %q holds %s", where, line, bad)
		return
	}
	if len(words) == 0 {
		t.Errorf("%s: empty command", where)
		return
	}
	known := false
	for _, p := range programs {
		known = known || words[0].raw == p
	}
	if !known {
		t.Errorf("%s: %q does not start with one of %q", where, line, programs)
	}
	for i, w := range words[1:] {
		switch {
		case w.quoted:
			if shellword.Quote(w.value) != w.raw {
				t.Errorf("%s: %q: %s is not the shared quoting of its value", where, line, w.raw)
			}
		case w.raw == "<", w.raw == "--", flagRE.MatchString(w.raw), placeholderRE.MatchString(w.raw):
		case shellword.Quote(w.raw) != w.raw:
			t.Errorf("%s: %q: %s needs quoting", where, line, w.raw)
		}
		if w.raw == "switch" && words[0].raw == "git" && (i+2 >= len(words) || words[i+2].raw != "--") {
			t.Errorf("%s: %q: git switch must take its branch after --", where, line)
		}
	}
}

// printedLeft is a Left as the stage hands it over, with the program its
// commands may start with.
type printedLeft struct {
	name string
	lf   initflow.Left
}

func (p printedLeft) programs(bin string) []string {
	return []string{bin, "git", "gcloud", "claude"}
}

func outputHygieneLefts(t *testing.T) []printedLeft {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	e := &initEngine{r: &initRun{o: &initOptions{}}}
	e.r.o.cloud.gcpProject = "acme-prod"
	e.lc = &localcfg.Config{Name: "aurora"}
	var out []printedLeft
	add := func(name string, lf initflow.Left) { out = append(out, printedLeft{name, lf}) }

	add("preflight", preflightStage{}.Left())
	add("project/gcp", (&projectStage{e: e, id: "acme-prod"}).Left())
	e.r.o.createProject = true
	e.r.o.parent = "folders/123"
	e.r.o.linkBilling = "0123AB-4567CD-89EF01"
	add("project/create", (&projectStage{e: e, id: "acme-prod", create: true}).Left())
	add("project/guided", (&projectStage{e: e, id: "acme-prod", guide: true}).Left())
	add("installation", newInstallationStage(e).Left())
	add("firebase", newFirebaseStage(e).Left())
	add("images", newImagesStage(e).Left())
	add("installation-2", newInstallation2Stage(e).Left())
	add("secrets/no-checkout", (&secretsStage{e: e}).Left())
	add("secrets/github", (&secretsStage{e: e, tg: &secretTarget{repo: "acme/app", wanted: []string{multilineSecret, "claude-oauth-token"}}, checked: true, missing: []string{multilineSecret, "claude-oauth-token"}}).Left())
	add("secrets/bitbucket", (&secretsStage{e: e, tg: &secretTarget{repo: "acme/app", wanted: []string{"bitbucket-token"}}}).Left())
	add("secrets/odd-repo", (&secretsStage{e: e, tg: &secretTarget{repo: "ac me/ap'p", wanted: []string{"bitbucket-token"}}}).Left())
	add("plugin/update", (&pluginStage{e: e}).Left())
	add("plugin/fork", forkLeft())
	add("repository", newRepositoryStage(e).Left())
	add("repository/onboard", onboardLeft(originInfo{Repo: "acme/app", Host: "github.com"}))
	add("repository/onboard-odd", onboardLeft(originInfo{Repo: "acme/app;rm -rf", Host: "github.com"}))
	add("repository/unusual-host", onboardLeft(originInfo{Repo: "acme/app", Host: "git.example.com"}))
	add("repository/switch", switchLeft("main"))
	add("repository/switch-dash", switchLeft("-fix;rm -rf"))
	add("repository/switch-quote", switchLeft("it's"))
	return out
}

// TestPrintedNextStepsAreOneLine drives every stage's left-for-you through
// the same checks, for the binary named as a bare word and as a path that
// needs quoting: each printed command is one line, one command, starts with
// the binary, and every value in it is shell-quoted by the shared helper.
func TestPrintedNextStepsAreOneLine(t *testing.T) {
	for _, bin := range []string{"fugaro", `'/My Tools/fugaro'`, `./bin/fugaro`} {
		t.Run(bin, func(t *testing.T) {
			old := selfCommand
			selfCommand = func() string { return bin }
			t.Cleanup(func() { selfCommand = old })
			for _, p := range outputHygieneLefts(t) {
				if strings.ContainsAny(p.lf.Text, "\r\n") || strings.HasSuffix(p.lf.Text, `\`) {
					t.Errorf("%s: text %q is not one line", p.name, p.lf.Text)
				}
				cmds := p.lf.PrintedCommands()
				if p.lf.Kind != initflow.LeftConsole && len(cmds) == 0 {
					t.Errorf("%s: a %s with no command to run", p.name, p.lf.Kind)
				}
				for _, c := range cmds {
					checkPrinted(t, p.name, c, p.programs(bin)...)
				}
			}
		})
	}
}

// The secrets' commands never chain: the Claude token's two steps are two
// lines, and no line carries a value.
func TestSecretsLeftSplitsTheChain(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	e := &initEngine{r: &initRun{o: &initOptions{}}, lc: &localcfg.Config{Name: "aurora"}}
	lf := (&secretsStage{e: e, tg: &secretTarget{repo: "acme/app", wanted: []string{"claude-oauth-token"}}}).Left()
	want := []string{"claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/app"}
	if fmt.Sprint(lf.Commands) != fmt.Sprint(want) {
		t.Fatalf("commands = %q, want %q", lf.Commands, want)
	}
}

// A leading-dash value is quoted and printed after "--".
func TestGitSwitchPrintsDoubleDash(t *testing.T) {
	if got := switchLeft("-weird").Text; got != "git switch -- '-weird'" {
		t.Fatalf("got %q", got)
	}
	if got := switchLeft("main").Text; got != "git switch -- main" {
		t.Fatalf("got %q", got)
	}
}

// The commands printed beside "Still missing" and the owner's role commands
// obey the same rule.
func TestMissingAndRoleCommandsAreOneLine(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	var buf bytes.Buffer
	r := &initRun{w: &buf, o: &initOptions{}}
	spec := infra.RepoSpec{Name: "acme/app", Secrets: map[string]string{"claude-oauth-token": "app-claude", "bitbucket-token": "app-bb"}}
	r.printMissing(spec, map[string]bool{}, nil)
	for _, c := range printedMissingCommands(buf.String()) {
		checkPrinted(t, "missing", c, "fugaro", "claude")
	}
	for _, n := range roleNotes(infra.InstallationOutputs{Launchers: []string{"user:a@example.com"}}) {
		if _, c, ok := strings.Cut(n, "run: "); ok {
			checkPrinted(t, "roles", c, "fugaro")
		}
	}
}

// printedMissingCommands is the four-space-indented lines of printMissing.
func printedMissingCommands(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "    ") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

func TestSelfCommandRule(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	for arg, want := range map[string]string{
		"fugaro":           "fugaro",
		"/opt/fugaro":      "/opt/fugaro",
		"/My Tools/fugaro": "'/My Tools/fugaro'",
		"-fugaro":          "'-fugaro'",
		"":                 "fugaro",
	} {
		os.Args = []string{arg}
		if got := selfCommand(); got != want {
			t.Errorf("selfCommand(%q) = %q, want %q", arg, got, want)
		}
	}
}

// Every refusal for a missing terminal ends with the same advice.
func TestNoTerminalMessageUniform(t *testing.T) {
	msgs := map[string]string{
		"loop":     (&initflow.NoTerminalError{Stage: "installation"}).Error(),
		"helper":   initflow.NeedsTerminal("typing it"),
		"firebase": "",
		"confirm":  "",
	}
	for _, name := range []string{"firebase", "confirm"} {
		msgs[name] = initflow.NeedsTerminal("the typed confirmation of this step")
	}
	for name, m := range msgs {
		if !strings.Contains(m, "needs a real terminal: "+initflow.NoTerminalAdvice) {
			t.Errorf("%s: %q", name, m)
		}
	}
	if !strings.Contains(initflow.NoTerminalAdvice, "your own terminal window") || !strings.Contains(initflow.NoTerminalAdvice, "coding agent") || !strings.Contains(initflow.NoTerminalAdvice, "pipe") {
		t.Fatalf("advice: %q", initflow.NoTerminalAdvice)
	}
}

// Golden: every command init prints for the user, per stage, so a change to
// one is reviewed.
func TestPrintedCommandsGolden(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	var b strings.Builder
	for _, p := range outputHygieneLefts(t) {
		fmt.Fprintf(&b, "[%s] %s: %s\n", p.name, p.lf.Kind, p.lf.Text)
		for _, c := range p.lf.Commands {
			fmt.Fprintf(&b, "    %s\n", c)
		}
	}
	path := filepath.Join("testdata", "init_commands.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no golden file: run go test ./internal/cli -run TestPrintedCommandsGolden -update")
		}
		t.Fatal(err)
	}
	if string(want) != b.String() {
		t.Fatalf("printed commands changed (go test -update to accept):\n%s\nwant:\n%s", b.String(), want)
	}
}
