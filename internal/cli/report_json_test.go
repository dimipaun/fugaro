package cli

// report_json_test.go is CL10: the pin that `--json` is never touched by the
// human formatting (G2, D1352, F8).
//
// `--json` is a contract the console, the plugin and the mobile app read
// (CLAUDE.md, "A field never changes meaning"), so what it prints is not a view
// anyone may style: stdout is one document on every outcome, and the human
// lines -- a step, a closing line, a failure's context, the log's path -- go to
// stderr or nowhere. The migration that could have broken it is the one this
// pin follows (CL5a, CL5b, CL6, CL7a-c: the converge, the accounts, secrets and
// creds family, the project and config writers, the run and runs writers), and
// the environment it is checked under is the one that would break it quietly: a
// real terminal on stdout, colour forced on from the environment, and --verbose.
//
// The fixtures are in report_json_fixtures_test.go, one hermetic run per
// command with a --json flag and one reason per command without one.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestJSONStdoutIsADocumentUnderEveryTerminalEnvironment is G2: for every
// command the pin has a fixture for, stdout is one JSON document and the human
// view is nowhere on it, even with a terminal on stdout, FORCE_COLOR and
// CLICOLOR_FORCE set and --verbose.
//
// A stray human line needs no assertion of its own: it is not JSON, so the
// parse fails and the line that broke the document is printed with the
// failure. What a parse cannot see is a byte an encoder may legitimately place
// inside a string, and both of the ones the human view is made of are checked
// for by hand: the escape that opens an ANSI sequence, and the carriage return
// the line in flight is drawn with.
func TestJSONStdoutIsADocumentUnderEveryTerminalEnvironment(t *testing.T) {
	for _, path := range jsonCommandPaths() {
		c, ok := jsonCases[path]
		if !ok || c.why != "" {
			continue
		}
		t.Run(path, func(t *testing.T) {
			jsonHarness(t)
			out, errOut, err := jsonRun(t, c)
			if strings.TrimSpace(out) == "" {
				t.Fatalf("`fugaro %s --json --verbose` printed no document (err %v):\n%s", path, err, errOut)
			}
			jsonStdoutIsADocument(t, path, c, out)
		})
	}
}

// jsonStdoutIsADocument holds one run's stdout to the contract: the human view
// is nowhere on it, and what is on it is a JSON document -- one for the whole
// output, or one a line for the reader that prints entries as it reads them.
func jsonStdoutIsADocument(t *testing.T, path string, c jsonCase, out string) {
	t.Helper()
	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("`fugaro %s --json` printed an escape byte at %d: the human view reached the document\n%q", path, i, out)
	}
	if strings.ContainsRune(out, '\r') {
		t.Errorf("`fugaro %s --json` printed a carriage return: a line in flight reached the document\n%q", path, out)
	}
	for _, line := range jsonNonEmptyLines(out) {
		if name := stepName(line); name != "" {
			t.Errorf("`fugaro %s --json` printed the step %q on stdout:\n%s", path, name, out)
		}
	}
	if c.lines {
		jsonStdoutIsOneDocumentALine(t, path, out)
		return
	}
	jsonStdoutIsOneDocument(t, path, out)
}

// jsonStdoutIsOneDocumentALine is the contract for the run whose --json is a
// stream: every non-empty line is one JSON document. How many there are is part
// of the check -- the fixture seeds more than one entry -- because a run that
// printed one line would pass the whole-output reading too, and the two
// readings are what this case exists to tell apart.
func jsonStdoutIsOneDocumentALine(t *testing.T, path, out string) {
	t.Helper()
	lines := jsonNonEmptyLines(out)
	if len(lines) < 2 {
		t.Errorf("`fugaro %s --json` printed %d line(s) for a run whose fixture seeds more than one entry: one document for the whole output is the reading this case rules out", path, len(lines))
	}
	for _, line := range lines {
		jsonLineIsOneDocument(t, path, line)
	}
}

// jsonLineIsOneDocument is one line of a stream: exactly one JSON value and
// nothing after it.
func jsonLineIsOneDocument(t *testing.T, path, line string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	var doc json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		t.Errorf("`fugaro %s --json` printed a line that is not a JSON document: %v\n%q", path, err, line)
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Errorf("`fugaro %s --json` printed more than one value in one line of its stream (err %v):\n%s", path, err, line)
	}
}

// jsonStdoutIsOneDocument holds one run's stdout to the contract: exactly one
// JSON value, no escape byte, no carriage return, and no step line.
func jsonStdoutIsOneDocument(t *testing.T, path, out string) {
	t.Helper()
	if i := strings.IndexByte(out, 0x1b); i >= 0 {
		t.Errorf("`fugaro %s --json` printed an escape byte at %d: the human view reached the document\n%q", path, i, out)
	}
	if strings.ContainsRune(out, '\r') {
		t.Errorf("`fugaro %s --json` printed a carriage return: a line in flight reached the document\n%q", path, out)
	}
	for _, line := range jsonNonEmptyLines(out) {
		if name := stepName(line); name != "" {
			t.Errorf("`fugaro %s --json` printed the step %q on stdout:\n%s", path, name, out)
		}
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var doc json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("`fugaro %s --json` did not print a JSON document: %v\n%q", path, err, out)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Errorf("`fugaro %s --json` printed more than one value on stdout (err %v):\n%s", path, err, out)
	}
}

// TestJSONReadsWriteNoLog is G7: no `--json` read and no read the console
// spawns writes a log. The two halves are one rule -- the commands the console
// polls are reads, and a read that filed a log would turn the console's own
// polling into the churn that evicts the log of the `fugaro update` a person
// wants to read after it failed (F6, D1352) -- so a fixture for a command the
// console spawns has to be marked as a read here.
func TestJSONReadsWriteNoLog(t *testing.T) {
	for _, path := range jsonCommandPaths() {
		c, ok := jsonCases[path]
		if !ok || c.why != "" || !c.reads {
			continue
		}
		t.Run(path, func(t *testing.T) {
			jsonHarness(t)
			// The run has to reach its own outcome for this to be evidence:
			// a read that refused before it read anything filed no log for a
			// reason that has nothing to do with being a read.
			out, errOut, err := jsonRun(t, c)
			if strings.TrimSpace(out) == "" {
				t.Fatalf("`fugaro %s --json` printed no document (err %v):\n%s", path, err, errOut)
			}
			jsonStdoutIsADocument(t, path, c, out)
			if logs := jsonLogs(t); len(logs) != 0 {
				t.Errorf("`fugaro %s --json` filed %d log(s), want none: %v", path, len(logs), logs)
			}
		})
	}
	// The rule is about the read the console spawns and not about --json, so a
	// read whose command carries no --json flag is run here too, spelled the
	// way the console spells it.
	for _, path := range consoleReadCases {
		t.Run(path, func(t *testing.T) {
			jsonHarness(t)
			out, errOut, err := execute(t, strings.Fields(strings.TrimPrefix(path, "fugaro "))...)
			if strings.TrimSpace(out) == "" {
				t.Fatalf("`%s`, a read the console spawns, printed nothing (err %v):\n%s", path, err, errOut)
			}
			if logs := jsonLogs(t); len(logs) != 0 {
				t.Errorf("`%s` is a read the console spawns and filed %d log(s), want none: %v", path, len(logs), logs)
			}
		})
	}
	assertEveryConsoleReadIsHeld(t)
}

// assertEveryConsoleReadIsHeld is G7's second half in full: the design's list
// is every command the console may spawn as a read, and each one of them is
// either a fixture above, run with --json, or a fixture in consoleReadCases,
// run without it, or named in consoleReadExempt with the reason this package
// has no run of it. The walk is over consoleReadPaths itself, so a read the
// design adds cannot land outside the rule without this failing.
func assertEveryConsoleReadIsHeld(t *testing.T) {
	t.Helper()
	for path := range consoleReadPaths {
		name := strings.TrimPrefix(path, "fugaro ")
		if c, ok := jsonCases[name]; ok {
			// A command with no fixture is exempt through the reason the
			// coverage test already holds it to (watch, today).
			if c.why == "" && !c.reads {
				t.Errorf("the console spawns `%s`, which the design lists as a read, and its fixture here is not marked as one: the run has to be held to writing no log", path)
			}
			continue
		}
		if slices.Contains(consoleReadCases, path) {
			continue
		}
		if _, ok := consoleReadExempt[path]; !ok {
			t.Errorf("the console spawns `%s` and this pin holds no run of it: add one to jsonCases, or to consoleReadCases for a read whose command carries no --json flag, or say in consoleReadExempt why this package has none", path)
		}
	}
	for _, path := range consoleReadCases {
		if !consoleReadPaths[path] {
			t.Errorf("consoleReadCases names %q, which the design does not list as a console read: the fixture holds a command the rule is not about", path)
		}
	}
	for path, why := range consoleReadExempt {
		if !consoleReadPaths[path] {
			t.Errorf("consoleReadExempt names %q, which the design does not list as a console read: a stale exemption is a hole nobody reads", path)
		}
		if slices.Contains(consoleReadCases, path) {
			t.Errorf("%q has a fixture and a reason it has none: they are exclusive", path)
		}
		if _, ok := jsonCases[strings.TrimPrefix(path, "fugaro ")]; ok {
			t.Errorf("%q is exempt while jsonCases carries an entry for it: the two say different things about one read", path)
		}
		if len(strings.Fields(why)) < 8 {
			t.Errorf("the reason `%s` has no run here is a stub, and it has to name the blocker: %q", path, why)
		}
	}
	if len(consoleReadExempt)*2 > len(consoleReadPaths) {
		t.Errorf("%d of the %d console reads have no run here: an exemption is for a read this package cannot make, not for one nobody wrote a fixture for", len(consoleReadExempt), len(consoleReadPaths))
	}
}

// TestJSONFixtureCoverage: the pin covers the tree, not a list someone
// maintains by hand. Every command with a --json flag is either run by a
// fixture or listed with the reason it has none, an entry that names a command
// the tree no longer has is stale, and the fixtures still have to be most of
// the list -- which is what keeps a future task from answering a command it
// cannot run by moving it to the reason column silently.
func TestJSONFixtureCoverage(t *testing.T) {
	paths := jsonCommandPaths()
	inTree := make(map[string]bool, len(paths))
	fixtures, reasons := 0, 0
	for _, path := range paths {
		inTree[path] = true
		c, ok := jsonCases[path]
		if !ok {
			t.Errorf("`fugaro %s` carries --json and is neither run by a fixture nor listed with a reason: add one to jsonCases", path)
			continue
		}
		switch {
		case c.why != "" && c.run != nil:
			t.Errorf("`fugaro %s` has a fixture and a reason for having none: they are exclusive", path)
		case c.why == "" && c.run == nil:
			t.Errorf("`fugaro %s` has neither a fixture nor a reason it has none", path)
		case c.why != "":
			reasons++
			if len(strings.Fields(c.why)) < 8 {
				t.Errorf("the reason `fugaro %s` has no fixture is a stub, and it has to name the blocker: %q", path, c.why)
			}
		default:
			fixtures++
		}
	}
	for path := range jsonCases {
		if !inTree[path] {
			t.Errorf("jsonCases names %q, which no command in the tree carries a --json flag on: a renamed or deleted command leaves a stale entry", path)
		}
	}
	if fixtures*2 < len(paths) {
		t.Errorf("%d of the %d --json commands have a fixture and %d have a reason: a reason is for a command this package cannot run, not for one nobody wrote a fixture for", fixtures, len(paths), reasons)
	}
}

// TestJSONFixtureReasonsAreReported is the last of the three promises the plan
// makes about a reason: it is visible. The list a reviewer reads is this one,
// printed on every run of the pin, rather than a comment nobody opens -- and it
// carries the console reads with no run here beside the --json commands with no
// fixture, because an exemption nobody prints is a hole.
func TestJSONFixtureReasonsAreReported(t *testing.T) {
	for _, path := range jsonCommandPaths() {
		if c, ok := jsonCases[path]; ok && c.why != "" {
			fmt.Printf("`fugaro %s --json`: %s\n", path, c.why)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(consoleReadExempt)) {
		fmt.Printf("`%s`, a read the console spawns, no run here: %s\n", path, consoleReadExempt[path])
	}
}
