package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// required_cli_e2e_test.go is T8: the required-CLI stamp's whole life, in one
// hermetic run over the fakes. Every other test of this feature pins one piece
// — the package (T1), the stage (T2a), the gate (T2b), the fence (T3), the door
// (T4), doctor (T5), the daemon (T6) — and each of them drives its own piece
// through the seam that piece sits behind. This one replaces none of the
// product's: the release bound is asked of a loopback releases API, the node is
// the installation's own fake database read and written through the real reader
// and the real client, and `Version` — the build's own seam — is the one thing
// that moves. What it walks is the six states the acceptance criteria name, in
// their order.

// TestRequiredCLIEndToEnd walks those six states: an update raises the node to
// its own release, and the binaries on either side of that release are what the
// fence, the door, doctor and the kill switch do about it.
func TestRequiredCLIEndToEnd(t *testing.T) {
	// The rig is the update door's own world — its binary is the release the
	// update below stamps at — with the installation's budget database on it.
	r, db := updateBudgetRig(t)
	clearAgentMarkers(t)
	// The release this installation's update runs as, and a binary one release
	// behind it: the two sides of the stamp.
	release, behind := releaseVersion(), "0.1.0"
	if !updatecheck.Newer(release, behind) {
		t.Fatalf("the fixture's own versions do not straddle the stamp: release %q, behind %q", release, behind)
	}
	// The releases API the run's own bound asks (D3269), on loopback and
	// answering the tag it is asked about: this release is a published one, and
	// no test reaches GitHub.
	stampReleaseAnswers(t, http.StatusOK)
	// A person is at this run's terminal, which is the whole confirmation every
	// self-confirming step of an update takes: nothing is typed below.
	fakeTerminal(t)

	// 1. An update at a published release R writes min = R.
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("fugaro update at %s: %v\n%s%s", release, err, out, errOut)
	}
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	stamp, present := stampOn(t, e)
	if !present {
		t.Fatalf("the update wrote no required CLI into the database:\n%s%s", out, errOut)
	}
	if stamp.Min != release {
		t.Fatalf("the node names %s, want this run's own release %s:\n%s%s", stamp.Min, release, out, errOut)
	}

	// 2. A binary at R-1 is refused by `fugaro run`, with the sentence and exit
	// 1 — one line on stderr, nothing on stdout, `--json` included.
	useVersion(t, behind)
	out, _, err = execute(t, "run", "--json")
	line := fenceRefusal(t, out, err)
	for _, want := range []string{"requires fugaro " + release + " or newer", "this is " + behind, updatecheck.BrewCommand} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal of `fugaro run` does not say %q: %s", want, line)
		}
	}

	// 3. A binary at R-1 is refused by `fugaro update` too, before its first
	// write (D3266, R8): no Terraform call, and the database's whole content and
	// the local config with its image catalog both exactly as they were.
	beforeDB, beforeCfg := dbState(t, db), readWhole(t, r.cfg)
	beforeCalls := len(r.calls(t))
	out, errOut, err = executeStdin(t, "", "update")
	fenceRefusal(t, out, err)
	if calls := r.calls(t); len(calls) != beforeCalls {
		t.Errorf("fugaro update ran terraform below the stamp: %q", calls[beforeCalls:])
	}
	if got := dbState(t, db); got != beforeDB {
		t.Errorf("the database moved below the stamp:\n%s\nwant\n%s", got, beforeDB)
	}
	if got := readWhole(t, r.cfg); got != beforeCfg {
		t.Errorf("the local config moved below the stamp:\n%s\nwant\n%s", got, beforeCfg)
	}

	// 4. doctor warns, before the person's next command is refused (T5): the
	// row names both releases, while this binary is still at R-1.
	installProject(t, r.cfg)
	out, _, _ = execute(t, "doctor", "--json", "--dir", t.TempDir())
	row, ok := doctorRow(t, out, cliBelowRequiredID)
	if !ok {
		t.Fatalf("doctor --json carries no %s row:\n%s", cliBelowRequiredID, out)
	}
	if row.OK || row.Severity != "warning" {
		t.Fatalf("cli-below-required = %+v, want a warning: the project requires a release this binary is below", row)
	}
	for _, want := range []string{release, behind} {
		if !strings.Contains(row.Problem, want) {
			t.Errorf("the warning does not name %s: %q", want, row.Problem)
		}
	}

	// 5. `fugaro budget kill` still works below the stamp (D3270): stopping
	// spend is the one write that must always land, so the fence never asks
	// about it and the switch is really set. The project is a managed one (a
	// kill halts every run, which only an installation Fugaro created may do,
	// D1541) and its admin is the caller.
	r.managedRuns(t, initProjectName, initProject)
	accessAs(t, db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "project"))
	out, errOut, err = execute(t, "budget", "kill", "--all")
	if err != nil {
		t.Fatalf("fugaro budget kill below the stamp: %v\n%s%s", err, out, errOut)
	}
	if v := db.Value(budget.PathKillGlobal); v == nil {
		t.Errorf("budget kill below the stamp wrote no kill switch:\n%s%s", out, errOut)
	}

	// 6. A binary at R passes, and does its work: the stamp is a minimum (spec,
	// "Mixed-CLI corner cases"), so a project command the fence would have
	// refused a release earlier is served here — this one writes the caps.
	useVersion(t, release)
	out, errOut, err = execute(t, "budget", "set", "--global", "--per-run", "3")
	if err != nil {
		t.Fatalf("fugaro budget set at %s: %v\n%s%s", release, err, out, errOut)
	}
	if v := db.Value(budget.PathCapsGlobal); v == nil {
		t.Errorf("budget set at %s wrote no caps:\n%s%s", release, out, errOut)
	}
}

// installProject copies the rig's own config to where this machine's
// installations live (localcfg.Projects/ProjectPath, which is the config home's
// projects/ directory). The rig keeps its config where FUGARO_CONFIG points,
// which is not a directory anything scans, and doctor asks whether any
// installation is configured before it selects one — so without this its run is
// the "no Fugaro installation is configured" one and never reaches a check
// about a project.
func installProject(t *testing.T, cfg string) {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
