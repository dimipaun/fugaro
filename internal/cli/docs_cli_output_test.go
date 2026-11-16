package cli

// The CLI output contract's pin: docs/design/v1.md §9.2, written by task CL11
// of docs/cli-output-console-graph/implementation-plan-output.md.
//
// The converge family moved onto the reporter first (CL5b) and the census
// settled which commands are on it and which are listings (CL9); what was left
// for the design of record was the half a reader cannot get from a command's
// --help: the default view, the three outcomes and their exit codes, the log
// beside a run with its directory, retention and modes, and what --verbose
// turns back on. A contract the design does not state is a contract the next
// command invents again — one colour rule, one log path and one summary line
// at a time — which is the state the reporter exists to end.
//
// Three pins, each failing on a change no other test would see:
//
//   - TestDocsCliOutputStatesTheContract: §9.2 carries the sentences. Drop the
//     T6 sentence (a read command writes no log) and a later command reads the
//     design as licensing one for `runs ls` — the console's own poll, and the
//     reason the retention is per command name at all (F6): a log it wrote
//     would evict the `fugaro update` log somebody is trying to read.
//   - TestDocsCliOutputLogPathMatchesTheDirectoryRule: the directory the
//     document gives is the one the code resolves, per platform, and the sink
//     CL3 opens lands in it. Move the state directory (D1351) and the document
//     fails here rather than describing a directory nothing writes to.
//   - TestDocsCliOutputPluginCitationsNameItsSection: the plugin section this
//     commit renumbers keeps its referrers with it. §9.2 was the plugin's
//     number until CL11 wrote the output contract above it, and three citations
//     inside the document — the `plugin` stage row, the plugin artifact row and
//     the `working` skill's follow-up part — sent a reader to the plugin by
//     that number, which after the renumbering is the output section. A design
//     of record that contradicts itself is read as the number it gives, and no
//     other test reads v1.md's §9.x citations at all.
//
// Mutation (each run and restored): change the document's retention to "the
// newest 500 files" -> TestDocsCliOutputStatesTheContract fails, naming the
// sentence; move the POSIX fallback the state directory is read from
// (localcfg.platformDirs) to `.local/state2` ->
// TestDocsCliOutputLogPathMatchesTheDirectoryRule fails, naming the path the
// document should have given instead; take `XDG_STATE_HOME` out of that
// directory's variables, so it is resolved from HOME alone -> the same test
// fails first on the sink it opens, which is then refused a directory to keep
// the run's log in at all; and write the plugin stage row's citation back as
// `(§9.2)` -> TestDocsCliOutputPluginCitationsNameItsSection fails, naming the
// row and the number the heading carries.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
)

// docsCliOutputDoc is the design of record's CLI section, which the two tests
// below read.
const docsCliOutputDoc = "docs/design/v1.md"

// TestDocsCliOutputStatesTheContract: the document states the output contract
// the CLI now follows — the default view, the three outcomes and their exit
// codes, the log and where it lives, its retention and modes, --verbose, the
// colour rule and --json's exemption from all of it.
func TestDocsCliOutputStatesTheContract(t *testing.T) {
	doc := readDoc(t, docsCliOutputDoc)
	for _, want := range []string{
		"### 9.2 The default output, the log and `--verbose`",
		"one line per step",
		"*done*, *failed* or *left for you*",
		"a step left for you is never a green tick",
		"`Done in 4.2s, 6 steps`",
		"`Failed in 12.0s, 4 steps`",
		"`Left for you in 8.1s, 5 steps`",
		"0 done or only planned, 1 a failure or a step left for you, 2 a cloud error",
		"the last lines of context on the screen",
		"a step left for you prints that path under it too",
		"the line in flight is taken down before anything reads stdin",
		"a read command writes no log at all and prints its error as it always has",
		"`$XDG_STATE_HOME/fugaro/logs`, else `~/.local/state/fugaro/logs` on macOS and Linux and `%LOCALAPPDATA%\\fugaro\\logs` on Windows",
		"mode 0600 in a 0700 directory",
		"Retention is per command name: 14 days, or the newest 50 files, whichever comes first",
		"never fails the command and never changes its exit code",
		"`fugaro update -v`",
		"`fugaro -v` is still the version",
		"`recipes --verbose` keeps its own meaning",
		"`CLICOLOR_FORCE` is ignored",
		"stdout is one document with no ANSI, no spinner and no log line",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not state the output contract: %q is missing", docsCliOutputDoc, want)
		}
	}
}

// TestDocsCliOutputLogPathMatchesTheDirectoryRule: the log directory the
// document names is the one the CLI resolves and writes into, on each platform
// the CLI runs on.
//
// The state directory is localcfg.UserDir's (D1351): $XDG_STATE_HOME where it
// is set, else the home directory's own `.local/state`, and %LOCALAPPDATA% on
// Windows. The document gives all three; the check is that each is still the
// rule the code reads, and that CL3's sink lands in the first of them, so the
// path in the failure block is the path the document promises.
func TestDocsCliOutputLogPathMatchesTheDirectoryRule(t *testing.T) {
	doc := readDoc(t, docsCliOutputDoc)

	state := t.TempDir()
	sink, err := report.OpenLog("update", func(name string) string {
		if name == "XDG_STATE_HOME" {
			return state
		}
		return ""
	}, "linux", time.Now, nil)
	if err != nil {
		t.Fatalf("a log under XDG_STATE_HOME was refused: %v", err)
	}
	defer sink.Close()
	if watch := filepath.Join(state, "fugaro", "logs"); filepath.Dir(sink.Path()) != watch {
		t.Errorf("the log is in %s, and %s gives `$XDG_STATE_HOME/fugaro/logs`", filepath.Dir(sink.Path()), docsCliOutputDoc)
	}

	// The POSIX fallback, as the code builds it: the home directory's own
	// `.local/state`, which the document writes as `~/.local/state`.
	home := t.TempDir()
	base, err := localcfg.UserDir(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}, "linux", localcfg.DirState)
	if err != nil {
		t.Fatal(err)
	}
	posix := "`~/" + strings.TrimPrefix(base, home+"/") + "/fugaro/logs`"
	if !strings.Contains(doc, posix) {
		t.Errorf("%s does not give %s, the state directory the code falls back to with no XDG variable", docsCliOutputDoc, posix)
	}

	// Windows: %LOCALAPPDATA% is the variable that decides the state directory
	// there, which is the one the document's sentence names. The value's own
	// spelling is the platform's — a Windows path is not absolute to a check
	// running on Linux, and CL3's build-tagged test is where the shape of the
	// path there is pinned — so the value here is one this host calls absolute.
	local := t.TempDir()
	win, err := localcfg.UserDir(func(name string) string {
		if name == "LOCALAPPDATA" {
			return local
		}
		return ""
	}, "windows", localcfg.DirState)
	if err != nil {
		t.Fatal(err)
	}
	if win != local {
		t.Errorf("the Windows state directory is %s, not %%LOCALAPPDATA%%'s own value: %s", win, docsCliOutputDoc)
	}
}

// docsPluginHeading is the plugin's heading with the number left off: the
// number is what the test below reads out of the document and holds the
// citations to, so writing it here as well would put the same drift one line up
// from where it is checked.
const docsPluginHeading = " Claude Code plugin (`plugin/`)"

// TestDocsCliOutputPluginCitationsNameItsSection: every citation inside the
// document that sends a reader to the plugin names the number the plugin's
// heading carries.
//
// The plugin and this contract swapped numbers in one commit (§9.2 was the
// plugin's, and the section is now §9.3), and three citations inside the
// document went on sending readers to the plugin by the old number — the
// `plugin` stage row of the converge's stage table, the plugin artifact row and
// the `working` skill's follow-up part — so the design of record said the
// plugin was stated where the run's output is. Each is pinned with the words
// that make it that citation, so one renumbered by hand is caught in its own
// sentence, and the document has to carry it once: a stale copy left beside the
// new one is the same contradiction.
func TestDocsCliOutputPluginCitationsNameItsSection(t *testing.T) {
	doc := readDoc(t, docsCliOutputDoc)
	section := docsPluginSection(t, doc)

	for _, cite := range []struct{ prefix, what string }{
		{"the merge of the marketplace and the plugin into `.claude/settings.json` is deleted (D949) (§", "the `plugin` stage row of the converge's stage table"},
		{"repositories pin the marketplace to that tag (§", "the plugin artifact row"},
		{"the follow-up part of the `working` skill (§", "the `working` skill's follow-up part"},
	} {
		switch n := strings.Count(doc, cite.prefix); {
		case n == 0:
			t.Errorf("%s no longer cites the plugin's section at all (%s), which is the citation that has to move with it", docsCliOutputDoc, cite.what)
		case n > 1:
			t.Errorf("%s cites the plugin's section %d times in %s, and one of them is stale", docsCliOutputDoc, n, cite.what)
		default:
			at := strings.Index(doc, cite.prefix) + len(cite.prefix)
			stated, _, _ := strings.Cut(doc[at:], ")")
			if stated != section {
				t.Errorf("%s's %s cites §%s for the plugin, whose heading is §%s: the reader is sent to the section that number now names", docsCliOutputDoc, cite.what, stated, section)
			}
		}
	}
}

// docsPluginSection reads the number out of the plugin's heading — "9.3" in
// `### 9.3 Claude Code plugin (`plugin/`)` — and fails the test if the heading
// is gone, since every citation it checks is measured against this.
func docsPluginSection(t *testing.T, doc string) string {
	t.Helper()
	at := strings.Index(doc, docsPluginHeading)
	if at < 0 {
		t.Fatalf("%s no longer has a plugin section heading, which the citations below are read against", docsCliOutputDoc)
	}
	start := at
	for start > 0 && (doc[start-1] == '.' || (doc[start-1] >= '0' && doc[start-1] <= '9')) {
		start--
	}
	if !strings.HasSuffix(doc[:start], "### ") {
		t.Fatalf("%s's plugin heading carries no number, and the number is what its citations are checked against", docsCliOutputDoc)
	}
	return doc[start:at]
}
