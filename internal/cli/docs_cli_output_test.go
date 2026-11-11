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
// Two pins, each failing on a change no other test would see:
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
//
// Mutation (each run and restored): change the document's retention to "the
// newest 500 files" -> TestDocsCliOutputStatesTheContract fails, naming the
// sentence; move the POSIX fallback the state directory is read from
// (localcfg.platformDirs) to `.local/state2` ->
// TestDocsCliOutputLogPathMatchesTheDirectoryRule fails, naming the path the
// document should have given instead; and take `XDG_STATE_HOME` out of that
// directory's variables, so it is resolved from HOME alone -> the same test
// fails first on the sink it opens, which is then refused a directory to keep
// the run's log in at all.

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
