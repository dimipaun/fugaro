package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// `--name` on the orchestrator commands (CG1b of docs/console-fixes, D3206,
// D3207, D3218). A project has its own orchestrator, whose id is the project's
// name, and any number of named ones beside it, `<project>~<name>` (C-ORCH): one
// node each, one daemon each, one login unit each.
//
// What is pinned here is the three places the name reaches the released tree —
// the resolver every command builds its id with, the listing that says which
// orchestrators an installation has and who is driving them, and the login unit,
// which is one file per name — and one place it must not: `orchestrator serve`,
// the resident orchestrator under `var.orchestrator`, drives the default id and
// takes no flag at all (D3224).
//
// The ids a named orchestrator's *node* is keyed by are CG1a's (`NodeIDs`,
// checkAgentID and the `~claims` key are its files, and it lands before this
// task's commands are used): until it does, a named id seeded under
// `orchestrator/` is one the listing leaves out, so the name half of an entry is
// pinned against the function the listing fills entries with rather than through
// a fixture.

// TestOrchestratorNameFlag: `--name` is the name half of an orchestrator id, the
// project half is the selection every command shares, and the commands that act
// on one orchestrator take it.
func TestOrchestratorNameFlag(t *testing.T) {
	lc := &localcfg.Config{Version: localcfg.ConfigVersion, Name: orchestratorTestID}

	if id, err := orchestratorID(lc); err != nil || id != orchestratorTestID {
		t.Errorf("the project's own orchestrator resolves to %q (%v), and its id is the project's name", id, err)
	}
	if id, err := orchestratorIDFor(lc, "night"); err != nil || id != orchestratorTestID+"~night" {
		t.Errorf("--name night resolves to %q (%v), and a named orchestrator's id is <project>~<name>", id, err)
	}

	// A name the rule refuses is refused before anything is read or written: the
	// resolver is where the flag becomes a database key (and, for the unit
	// commands, a file name), so this is the one door every bad name comes to.
	for _, bad := range []string{
		"Night", "night time", "night/../night", "night~day", "-night",
		strings.Repeat("n", orchestrator.MaxOrchestratorNameLength+1),
	} {
		if id, err := orchestratorIDFor(lc, bad); err == nil {
			t.Errorf("--name %q was accepted as %q, and the name rule is 1 to %d characters of a-z, 0-9 and - starting with a letter or a digit",
				bad, id, orchestrator.MaxOrchestratorNameLength)
		}
	}
	// The project half comes from the selection and from nothing else, so a
	// project name the rule refuses names no orchestrator of it either.
	if id, err := orchestratorIDFor(&localcfg.Config{Name: "not a project"}, "night"); err == nil {
		t.Errorf("--name night resolved to %q for a project whose name is not one", id)
	}

	// The flag is on the four commands this task gives it to, and `serve` is the
	// one command of the family that must never take it. The family's other
	// readers — state, events, patch and import — are the next task's (CG1c), so
	// this test says nothing about them either way.
	orch := findCmd(t, NewRootCmd(), "orchestrator")
	wantFlag := map[string]bool{"run": true, "install": true, "uninstall": true, "status": true}
	for _, c := range orch.Commands() {
		has := c.Flags().Lookup(orchestratorNameFlag) != nil
		if c.Name() == "serve" {
			if has {
				t.Errorf("`orchestrator serve` takes --name, and the resident orchestrator drives the default id alone (D3224)")
			}
			continue
		}
		if wantFlag[c.Name()] && !has {
			t.Errorf("`orchestrator %s` does not take --name, and it acts on one orchestrator of the project", c.Name())
		}
	}

	// And the daemon reads it: a name the rule refuses stops `orchestrator run`
	// at the resolver, before it touches any node.
	newOrchestratorFixture(t)
	out, _, err := runOrchestrator(t, "", "orchestrator", "run", "--name", "Night", "--once")
	if err == nil {
		t.Fatalf("`orchestrator run --name Night` ran:\n%s", out)
	}
	if !strings.Contains(err.Error(), "Night") {
		t.Errorf("the run's refusal does not name the name it refused: %v", err)
	}
}

// seedOrchestratorDriver writes an id's driver node: the principal of the
// credential driving it, which is the sibling whoever holds the lease writes
// (D3218, F18).
func seedOrchestratorDriver(t *testing.T, f *budgetFixture, id, principal string) {
	t.Helper()
	path, err := orchestrator.PathDriver(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, map[string]any{"principal": principal, "at": orchestratorTestNow})
}

// TestOrchestratorLsNameDriver: the listing says which orchestrator each entry
// is and who is driving it. `name` is the name half of the id — "" for the
// project's own, which is an answer and not an absence — and `driver` is the
// principal the id's driver node names, absent for an id nothing has driven
// since that node was introduced.
func TestOrchestratorLsNameDriver(t *testing.T) {
	f := lsFixture(t)
	seedOrchestratorNode(t, f, orchestratorTestID)
	seedOrchestratorDriver(t, f, orchestratorTestID, "someone@example.com")
	// An id with a node and no driver node: every orchestrator driven before
	// this release's driver node existed is one of these.
	seedOrchestratorNode(t, f, "belong")

	out, _, err := runOrchestrator(t, "", "orchestrator", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Orchestrators []struct {
			Project string `json:"project"`
			Name    string `json:"name"`
			Driver  string `json:"driver"`
		} `json:"orchestrators"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Orchestrators) != 2 {
		t.Fatalf("the listing carries %d orchestrators, and the subtree holds 2:\n%s", len(doc.Orchestrators), out)
	}
	for _, e := range doc.Orchestrators {
		switch e.Project {
		case orchestratorTestID:
			if e.Name != "" {
				t.Errorf("the project's own orchestrator is %+v, and the name half of its id is empty", e)
			}
			if e.Driver != "someone@example.com" {
				t.Errorf("the entry is %+v, and the id's driver node names someone@example.com", e)
			}
		case "belong":
			if e.Name != "" {
				t.Errorf("belong is %+v, and the name half of its id is empty", e)
			}
			if e.Driver != "" {
				t.Errorf("belong is %+v, and it has no driver node, so it is driven by nobody and not by an empty name", e)
			}
		default:
			t.Errorf("the listing carries %q, which no fixture seeded", e.Project)
		}
	}

	// The name half the listing fills an entry with is the id's, so a named
	// orchestrator's entry carries its slug and not the whole id: this is the
	// function runOrchestratorLs builds every entry with, and a named id reaches
	// it as soon as NodeIDs lists one (CG1a).
	for _, tc := range []struct{ id, project, name string }{
		{orchestratorTestID, orchestratorTestID, ""},
		{orchestratorTestID + "~night", orchestratorTestID + "~night", "night"},
	} {
		e := orchestratorLsEntryOf(tc.id)
		if e.Project != tc.project || e.Name != tc.name {
			t.Errorf("the entry of %q is %+v, want project %q name %q", tc.id, e, tc.project, tc.name)
		}
		if e.Kind != orchestratorKindCentral {
			t.Errorf("the entry of %q is %q, and an id with no cached brief is central", tc.id, e.Kind)
		}
	}

	// The table names the driver too, so a person reading it sees who is driving
	// each id rather than having to ask for --json.
	out, _, err = runOrchestrator(t, "", "orchestrator", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "someone@example.com") {
		t.Errorf("the table does not name the driver:\n%s", out)
	}

	// A driver node this release cannot read is refused rather than reported as
	// nobody driving the id: the same reading DecodeDriver refuses for a node
	// naming no principal, and for a node that is no driver document at all.
	for _, tc := range []struct {
		name string
		doc  any
	}{
		{"a node naming no principal", map[string]any{"principal": "", "at": orchestratorTestNow}},
		{"a node this release does not model", map[string]any{"who": "someone@example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := lsFixture(t)
			seedOrchestratorNode(t, f, orchestratorTestID)
			path, err := orchestrator.PathDriver(orchestratorTestID)
			if err != nil {
				t.Fatal(err)
			}
			f.db.Set(path, tc.doc)
			out, _, err := runOrchestrator(t, "", "orchestrator", "ls", "--json")
			if err == nil {
				t.Fatalf("%s was read as a driver:\n%s", tc.name, out)
			}
			if strings.Contains(out, `"driver"`) {
				t.Errorf("the listing printed a driver anyway:\n%s", out)
			}
			// And the refusal reads as the node being unreadable rather than as
			// an id nobody drives: the decoder's own refusal for a node it does
			// not model, and DecodeDriver's for one naming no principal.
			if msg := err.Error(); !strings.Contains(msg, "does not model") && !strings.Contains(msg, "names no principal") {
				t.Errorf("the refusal is %v, and it does not read as this release refusing the node", err)
			}
		})
	}
}

// TestInstallUnitPerName: the login unit is one file per orchestrator name. The
// project's own is the unit this release has always written — `fugaro update`
// keeps writing it, byte for byte — and `--name` writes the named one's, which
// differs in its file, its label and the `--name` its daemon is run with, so a
// machine keeps both running at once (D3206, D3207).
func TestInstallUnitPerName(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)

	def, err := orchestratorUnitNamed("")
	if err != nil {
		t.Fatal(err)
	}
	// The default renderer is unchanged by this task: the unit the setup and
	// update steps write is still the one the converged installation has on disk.
	if today := currentUnit(t); def.Path != today.Path || def.Text != today.Text {
		t.Errorf("the project's own orchestrator's unit is %s, and this release has always written %s", def.Path, today.Path)
	}
	night, err := orchestratorUnitNamed("night")
	if err != nil {
		t.Fatal(err)
	}
	if night.Path == def.Path || night.Name == def.Name {
		t.Errorf("the named unit is %s / %s, which is the project's own unit", night.Name, night.Path)
	}
	for _, want := range []string{"night"} {
		if !strings.Contains(night.Name, want) || !strings.Contains(night.Path, want) {
			t.Errorf("the named unit is %s at %s, and neither names %q", night.Name, night.Path, want)
		}
	}
	// The named unit runs the id it is for. `--name` is the flag `orchestrator
	// run` takes, so the daemon the service manager restarts drives the named
	// orchestrator and not the project's own.
	if !strings.Contains(night.Text, "--name") || !strings.Contains(night.Text, "night") {
		t.Errorf("the named unit does not run the daemon with --name:\n%s", night.Text)
	}
	if run := findCmd(t, findCmd(t, NewRootCmd(), "orchestrator"), "run"); run.Flags().Lookup(orchestratorNameFlag) == nil {
		t.Errorf("the unit runs `orchestrator run --name`, and that command does not take the flag")
	}

	// A name that is not one is refused before anything is written: it becomes
	// the unit's own file name.
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install", "--name", "Night")
	if err == nil {
		t.Fatalf("install --name Night wrote a unit:\n%s", out)
	}
	if len(f.reloads) != 0 {
		t.Errorf("the refused install still reloaded %v", f.reloads)
	}
	if _, err := os.Stat(night.Path); err == nil {
		t.Errorf("the refused install wrote %s", night.Path)
	}

	// The named install writes the named unit and leaves the project's own
	// alone: the two files stand side by side.
	out, _, err = executeStdin(t, "install\n", "orchestrator", "install", "--name", "night")
	if err != nil {
		t.Fatalf("install --name night: %v\n%s", err, out)
	}
	data, err := os.ReadFile(night.Path)
	if err != nil {
		t.Fatalf("the named unit was not written: %v", err)
	}
	if string(data) != night.Text {
		t.Errorf("%s is not what this release renders:\n--- on disk\n%s\n--- rendered\n%s", night.Path, data, night.Text)
	}
	if _, err := os.Stat(def.Path); err == nil {
		t.Errorf("installing the named orchestrator's unit wrote the project's own at %s", def.Path)
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != night.Path {
		t.Errorf("the service manager was reloaded with %v, want one call for %s", f.reloads, night.Path)
	}

	// It is idempotent, like the default one: a second install re-renders the
	// same text and reloads the same file.
	if out, _, err = executeStdin(t, "install\n", "orchestrator", "install", "--name", "night"); err != nil {
		t.Fatalf("the second install --name night: %v\n%s", err, out)
	}
	if again, err := os.ReadFile(night.Path); err != nil || string(again) != night.Text {
		t.Errorf("the second install left %s (read %v):\n%s", night.Path, err, again)
	}

	// status and uninstall are about the same unit — the one the name installed
	// — and neither touches the project's own.
	out, _, err = executeStdin(t, "", "orchestrator", "status", "--name", "night")
	if err != nil {
		t.Fatalf("status --name night: %v\n%s", err, out)
	}
	if !strings.Contains(out, night.Path) {
		t.Errorf("status --name night does not name %s:\n%s", night.Path, out)
	}
	out, _, err = executeStdin(t, "", "orchestrator", "uninstall", "--name", "night")
	if err != nil {
		t.Fatalf("uninstall --name night: %v\n%s", err, out)
	}
	if _, err := os.Stat(night.Path); !os.IsNotExist(err) {
		t.Errorf("uninstall --name night left %s behind (%v)", night.Path, err)
	}
	if len(f.removes) != 1 || f.removes[0].Path != night.Path {
		t.Errorf("the service manager was unloaded with %v, want one call for %s", f.removes, night.Path)
	}
}
