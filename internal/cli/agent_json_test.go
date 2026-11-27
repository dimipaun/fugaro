package cli

// agent_json_test.go is CO4: an agent session gets the --json document (D3437).
//
// A coding agent reads a command's result document and the person it works for
// reads the short lines, so a command that has --json behaves as if the flag
// had been passed when the session is an agent's — the document on stdout, the
// human view on stderr — while a person's own run is left exactly as it was.
//
// The command under test is `fugaro cancel`, which prints one JSON document on
// stdout and its step line and summary through the reporter, so a single run
// shows both halves of the rule: where the document went, and where the human
// view went. An IDE terminal is a person's (agentenv.Session): the marker the
// Claude Code IDE extension sets is a coding agent's only without a terminal on
// the run's input, which an agent's own tool call never has.

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend"
)

// cancelSeedRun is the run the three tests below cancel: launched and running,
// so `cancel --now` reaches the document rather than a refusal.
func cancelSeedRun(t *testing.T) string {
	t.Helper()
	f := jsonCloud(t)
	id := "20260927-100000-abcd"
	f.run.SetState(seedRun(t, f, id, "", "", true), backend.StateRunning)
	return id
}

// pinStdinTerminal replaces the terminal seam for one test, so the session is
// classified by the environment alone rather than by how the test was launched.
func pinStdinTerminal(t *testing.T, terminal bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return terminal }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// jsonNeedsFlagArgs is each paired command's own way to the refusal its pair
// makes, so the subtest below runs the command rather than the table: the
// positional name it takes, and for `secrets set` the policy write its --json
// would be the document of. The refusal comes before anything the command
// reads — that is what makes the pair a pair — so nothing else is needed.
var jsonNeedsFlagArgs = map[string][]string{
	"fugaro image build":    {"image", "build", "app"},
	"fugaro image add":      {"image", "add", "app"},
	"fugaro models add":     {"models", "add", "app"},
	"fugaro secrets rm":     {"secrets", "rm", "appv"},
	"fugaro secrets set":    {"secrets", "set", "appv", "--allow-data-to", "acme/app"},
	"fugaro settings set":   {"settings", "set", "agent.review_rounds", "2"},
	"fugaro settings unset": {"settings", "unset", "agent.review_rounds"},
}

// jsonRefusedWithArgs is each of those commands' own way to its refusal: the
// reference `logs` reads, and nothing for the other two, whose refusal is the
// first thing they reach.
var jsonRefusedWithArgs = map[string][]string{
	"fugaro logs":        {"logs", "abcd-1234"},
	"fugaro runs digest": {"runs", "digest"},
	"fugaro update":      {"update"},
}

// TestAgentSessionGetsJSON: a command that has --json writes its result
// document to stdout and its human lines to stderr in a coding agent's
// session, without the flag being asked for.
func TestAgentSessionGetsJSON(t *testing.T) {
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	pinStdinTerminal(t, false)
	id := cancelSeedRun(t)

	out, errOut, err := execute(t, "cancel", "--now", id)
	if err != nil {
		t.Fatalf("cancel in an agent session: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	var doc cancelResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		t.Fatalf("an agent session's stdout is not the result document: %v\nstdout:\n%s", err, out)
	}
	if doc.Status != cancelCancelled || !doc.Hard {
		t.Errorf("the document says status=%q hard=%v, want the cancelled run's", doc.Status, doc.Hard)
	}
	if !strings.Contains(errOut, "Done in") {
		t.Errorf("the human view is not on stderr: %q", errOut)
	}

	// Every entry of jsonNeedsFlag has to earn its place: --json alone is
	// refused there, so the rule would turn the command into a sentence about a
	// flag nobody passed. Each table and the test's own args are the same set,
	// so neither a dropped pair nor a new one goes unnoticed.
	if len(jsonNeedsFlagArgs) != len(jsonNeedsFlag) {
		t.Errorf("jsonNeedsFlagArgs pins %d commands and jsonNeedsFlag holds %d: they are the same set", len(jsonNeedsFlagArgs), len(jsonNeedsFlag))
	}
	for _, path := range slices.Sorted(maps.Keys(jsonNeedsFlag)) {
		argv, pinned := jsonNeedsFlagArgs[path]
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if !pinned {
				t.Fatalf("jsonNeedsFlag holds %s and this test does not run it, so the exception is unproven", path)
			}
			flag := jsonNeedsFlag[path]
			cmd, _, err := NewRootCmd().Find(argv)
			if err != nil || cmd == nil || cmd.Flags().Lookup(flag) == nil {
				t.Fatalf("%s has no --%s in the tree, so jsonNeedsFlag names a pair that is not one: %v", path, flag, err)
			}
			out, errOut, err := execute(t, append(slices.Clone(argv), "--json")...)
			if err == nil || !strings.Contains(err.Error(), "--"+flag) {
				t.Fatalf("%s --json alone = %v, want the refusal naming --%s (stdout %q, stderr %q)", path, err, flag, out, errOut)
			}
			if json.Valid([]byte(strings.TrimSpace(out))) {
				t.Errorf("%s --json alone printed a document, so the exception excuses a command that needs none: %q", path, out)
			}
		})
	}

	// jsonRefusedWith is the other way round — there --json is refused beside
	// the flag, which is the answer the caller asked for — so each entry has to
	// refuse that pair, and the flag has to be left alone when it was passed.
	if len(jsonRefusedWithArgs) != len(jsonRefusedWith) {
		t.Errorf("jsonRefusedWithArgs pins %d commands and jsonRefusedWith holds %d: they are the same set", len(jsonRefusedWithArgs), len(jsonRefusedWith))
	}
	for _, path := range slices.Sorted(maps.Keys(jsonRefusedWith)) {
		argv, pinned := jsonRefusedWithArgs[path]
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if !pinned {
				t.Fatalf("jsonRefusedWith holds %s and this test does not run it, so the exception is unproven", path)
			}
			cmd, _, err := NewRootCmd().Find(argv)
			if err != nil || cmd == nil {
				t.Fatalf("%s is not in the tree: %v", path, err)
			}
			for _, flag := range jsonRefusedWith[path] {
				out, errOut, err := execute(t, append(slices.Clone(argv), "--"+flag, "--json")...)
				if err == nil || !strings.Contains(err.Error(), "--json") {
					t.Fatalf("%s --%s --json = %v, want the refusal naming --json (stdout %q, stderr %q)", path, flag, err, out, errOut)
				}
				asked, _, _ := NewRootCmd().Find(argv)
				_ = asked.Flags().Set(flag, "true")
				agentJSON(asked)
				if boolFlag(asked, jsonFlag) {
					t.Errorf("%s --%s left the flag on, so the run is answered with a complaint about a flag it never gave", path, flag)
				}
			}
		})
	}
}

// TestTerminalUnchanged: outside an agent session nothing changes, so a person
// at a terminal gets the human view on stdout and no document.
func TestTerminalUnchanged(t *testing.T) {
	noAgentSession(t)
	pinStdinTerminal(t, true)
	id := cancelSeedRun(t)

	out, errOut, err := execute(t, "cancel", "--now", id)
	if err != nil {
		t.Fatalf("cancel at a person's terminal: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if json.Valid([]byte(strings.TrimSpace(out))) {
		t.Errorf("a person's run printed a document on stdout: %q", out)
	}
	if !strings.Contains(out, "cancelled the execution") {
		t.Errorf("a person's run does not print its line on stdout: %q", out)
	}
}

// TestIDETerminalUnchanged: the Claude Code IDE extension's marker with a real
// terminal on the run's input is a person, not an agent.
func TestIDETerminalUnchanged(t *testing.T) {
	noAgentSession(t)
	t.Setenv(agentenv.IDEMarker, "1")
	pinStdinTerminal(t, true)
	id := cancelSeedRun(t)

	out, errOut, err := execute(t, "cancel", "--now", id)
	if err != nil {
		t.Fatalf("cancel in an IDE terminal: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if json.Valid([]byte(strings.TrimSpace(out))) {
		t.Errorf("an IDE terminal's run printed a document on stdout: %q", out)
	}
	if !strings.Contains(out, "cancelled the execution") {
		t.Errorf("an IDE terminal's run does not print its line on stdout: %q", out)
	}
}
