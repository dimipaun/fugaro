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
// a fixture. The same fact is why nothing here installs or runs a named
// orchestrator: the path rule refuses one, so the resolver and the install guard
// refuse it too — cleanly, and before a unit exists — and the assertions that
// flip the day a named id is addressable are marked as such.

// TestOrchestratorNameFlag: `--name` is the name half of an orchestrator id, the
// project half is the selection every command shares, and the commands that act
// on one orchestrator take it.
func TestOrchestratorNameFlag(t *testing.T) {
	lc := &localcfg.Config{Version: localcfg.ConfigVersion, Name: orchestratorTestID}

	if id, err := orchestratorID(lc); err != nil || id != orchestratorTestID {
		t.Errorf("the project's own orchestrator resolves to %q (%v), and its id is the project's name", id, err)
	}
	// A name the rule admits builds the id D3207 gives it — `<project>~night` —
	// and is then refused by the rule this release's node paths are built with,
	// which admits the project's own id alone. The plan lands the task that widens
	// it (CG1a) before this task's commands are used, so what is pinned here is the
	// state of this branch: a named id is well-formed and unaddressable, and the
	// refusal says which of the two rules said no rather than letting a run reach
	// the database and die there. The day a named id is addressable this assertion
	// is the one that changes: `<project>~night` and no error.
	if id, err := orchestratorIDFor(lc, "night"); err == nil {
		t.Errorf("--name night resolved to %q, and this release cannot address a named orchestrator: the rule the paths are built with admits the project's own id alone (CG1a)", id)
	} else {
		for _, want := range []string{"night", orchestratorTestID + "~night", "nothing is read and nothing is written"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal of --name night is %v, and it does not carry %q", err, want)
			}
		}
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

	// And the daemon reads it: `orchestrator run --name night --once` is refused
	// at the resolver, before it takes a lease or writes anything, so a named unit
	// a person wrote by hand cannot leave a daemon looping on an id this release
	// cannot address — and so a unit this release's own install writes cannot
	// either, which is what the install guard beside this one is for.
	f := newOrchestratorFixture(t)
	out, _, err := runOrchestrator(t, "", "orchestrator", "run", "--name", "night", "--once")
	if err == nil {
		t.Fatalf("`orchestrator run --name night --once` ran:\n%s", out)
	}
	if !strings.Contains(err.Error(), "night") {
		t.Errorf("the run's refusal does not name the name it refused: %v", err)
	}
	// Nothing was written for it: the node the id would live at, and the claims
	// node every item one orchestrator claims is keyed under, are both absent.
	node := orchestrator.SubtreeRoot + "/" + orchestratorTestID + "~night"
	if v := f.db.Value(node); v != nil {
		t.Errorf("the refused run wrote %s: %v", node, v)
	}
	if v := f.db.Value(orchestrator.ClaimsRoot); v != nil {
		t.Errorf("the refused run claimed an item under %s: %v", orchestrator.ClaimsRoot, v)
	}

	// A name the rule refuses is refused earlier still, in the name rule itself,
	// and the refusal names the name rather than an id it never built.
	out, _, err = runOrchestrator(t, "", "orchestrator", "run", "--name", "Night", "--once")
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
// principal the id's driver node names, "" for an id that node says nothing
// about.
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
	// The driver is a pointer so that absent and empty are told apart: it is
	// written on every entry, and "" is the answer — not a missing field — for
	// an id whose driver node says nothing.
	var doc struct {
		Orchestrators []struct {
			Project string  `json:"project"`
			Name    string  `json:"name"`
			Driver  *string `json:"driver"`
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
			if e.Driver == nil || *e.Driver != "someone@example.com" {
				t.Errorf("the entry is %+v, and the id's driver node names someone@example.com", e)
			}
		case "belong":
			if e.Name != "" {
				t.Errorf("belong is %+v, and the name half of its id is empty", e)
			}
			if e.Driver == nil || *e.Driver != "" {
				t.Errorf("belong is %+v, and it has no driver node, so what its driver is, is unknown", e)
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

	// A driver node this release cannot read is not a refusal and not a driver
	// either: the entry's driver is empty, which the contract's own reading is
	// the answer for a node that was absent or unreadable — never "nobody drives
	// this" — and a listing that failed whole over one node it cannot read would
	// hide every orchestrator of the installation behind it.
	for _, tc := range []struct {
		name string
		doc  any
	}{
		{"a node naming no principal", map[string]any{"principal": "", "at": orchestratorTestNow}},
		{"a node this release does not model", map[string]any{"who": "someone@example.com"}},
		{"a node that is no document at all", "someone@example.com"},
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
			if err != nil {
				t.Fatalf("%s failed the listing: %v\n%s", tc.name, err, out)
			}
			// The field is written and it is empty: a consumer reads "unknown"
			// from it, and the pointer is what says the field is there at all.
			var doc struct {
				Orchestrators []struct {
					Project string  `json:"project"`
					Driver  *string `json:"driver"`
				} `json:"orchestrators"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			for _, e := range doc.Orchestrators {
				if e.Project != orchestratorTestID {
					continue
				}
				if e.Driver == nil || *e.Driver != "" {
					t.Errorf("%s: the entry is %+v, and its driver is written and empty", tc.name, e)
				}
			}
			// And the table says so in a word: "unknown" and not "nobody", which
			// would be the claim that this id is driven by no one.
			out, _, err = runOrchestrator(t, "", "orchestrator", "ls")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			row := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, orchestratorTestID+" ") {
					row = line
				}
			}
			if !strings.Contains(row, "unknown") {
				t.Errorf("%s: the id's row does not read as an unknown driver:\n%s", tc.name, out)
			}
		})
	}
}

// TestInstallUnitPerName: the login unit is one file per orchestrator name. The
// project's own is the unit this release has always written — `fugaro update`
// keeps writing it, byte for byte — and a named one is a unit of its own: its own
// file, its own label, and a daemon run with `--name`, so a machine keeps both
// running at once (D3206, D3207). What this release installs is the project's own
// alone: a named orchestrator's node cannot be built from its paths yet (CG1a),
// so `install --name` refuses rather than writing a unit that could only loop.
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
	// orchestrator and not the project's own. Nothing this release installs is a
	// named unit yet — the guard below is why — and the render is pinned here
	// because it is what a release whose paths take a named id installs.
	if !strings.Contains(night.Text, "--name") || !strings.Contains(night.Text, "night") {
		t.Errorf("the named unit does not run the daemon with --name:\n%s", night.Text)
	}
	if run := findCmd(t, findCmd(t, NewRootCmd(), "orchestrator"), "run"); run.Flags().Lookup(orchestratorNameFlag) == nil {
		t.Errorf("the unit runs `orchestrator run --name`, and that command does not take the flag")
	}

	// A name that is not one is refused before anything is written: it becomes
	// the unit's own file name. The refusal is the name rule's own and not the
	// guard below — a name the rule refuses is refused as the name it is.
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install", "--name", "Night")
	if err == nil {
		t.Fatalf("install --name Night wrote a unit:\n%s", out)
	}
	for _, want := range []string{"Night", "is not one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of install --name Night is %v, and it does not carry %q", err, want)
		}
	}
	if len(f.reloads) != 0 {
		t.Errorf("the refused install still reloaded %v", f.reloads)
	}
	if _, err := os.Stat(night.Path); err == nil {
		t.Errorf("the refused install wrote %s", night.Path)
	}

	// And a name the rule admits is refused too, before the unit exists: this
	// release cannot run a named orchestrator's daemon, and install is the one of
	// the three unit commands that writes code a service manager starts at every
	// login and restarts when it stops. A unit whose daemon is refused at its
	// first lease is a login loop rather than a refusal a person ever sees, so the
	// install is where the release says no. CG1a flips this step: once a named id
	// is addressable, the install writes the file and what follows here becomes
	// the assertions the named unit deserves.
	out, _, err = executeStdin(t, "install\n", "orchestrator", "install", "--name", "night")
	if err == nil {
		t.Fatalf("install --name night wrote a unit this release cannot run:\n%s", out)
	}
	for _, want := range []string{"night", "Nothing is installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of install --name night is %v, and it does not carry %q", err, want)
		}
	}
	if len(f.reloads) != 0 {
		t.Errorf("the refused install reloaded %v", f.reloads)
	}
	if len(f.removes) != 0 {
		t.Errorf("the refused install unloaded %v", f.removes)
	}
	if _, err := os.Stat(night.Path); err == nil {
		t.Errorf("the refused install wrote %s", night.Path)
	}
	if _, err := os.Stat(def.Path); err == nil {
		t.Errorf("the refused install wrote the project's own unit at %s", def.Path)
	}

	// status and uninstall are about the file and not about the node, so a named
	// unit a person wrote by hand — or one a later release installs — stays theirs
	// to read and to remove. Neither takes the guard the install does, and there
	// is no named unit on this machine, which each of them says in its own words.
	out, _, err = executeStdin(t, "", "orchestrator", "status", "--name", "night")
	if err != nil {
		t.Fatalf("status --name night: %v\n%s", err, out)
	}
	if !strings.Contains(out, night.Path) || !strings.Contains(out, "not installed") {
		t.Errorf("status --name night does not report the absent unit at %s:\n%s", night.Path, out)
	}
	out, _, err = executeStdin(t, "", "orchestrator", "uninstall", "--name", "night")
	if err != nil {
		t.Fatalf("uninstall --name night: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("uninstall --name night does not report that there was nothing to remove:\n%s", out)
	}
	if len(f.removes) != 0 {
		t.Errorf("uninstall --name night unloaded %v, and there was no unit to unload", f.removes)
	}
}
