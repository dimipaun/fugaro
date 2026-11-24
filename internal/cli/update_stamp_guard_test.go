package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// update_stamp_guard_test.go is the door's own fence (update_stamp_guard.go,
// T4 of docs/required-version-stamp/implementation-plan.md, D3266, R8): an
// `update` from a binary below the installation's required CLI refuses before
// its first write, in every mode it has.
//
// The fixture is the update door's own (updateRig: this binary's version is
// 0.2.0, a state bucket exists, the plan is empty) with the installation's
// database on it (buildBudgetForRig), so the node is read by the real reader
// through the real client: a fake Source would pin the guard's wiring and not
// the read the CLI actually makes. The two modes that read no stamp stub the
// seam instead (noStampSource), because what they assert is that no read
// happens at all.
//
// The refusal is asserted as *versionstamp.BelowError and not as its sentence:
// the sentence is T1's own test's (TestBelowErrorIsTheSpecSentence), and a
// spelling of it here would pass while the refusal drifted.

// stampNode puts a well-formed stamp on the installation's database.
func stampNode(db *gcpfake.RTDB, min string) {
	db.Set(versionstamp.Path, map[string]any{
		"schema": versionstamp.SchemaVersion, "min": min, "set_at": "2026-11-20T12:00:00Z",
	})
}

// stampGuardRig is the update door's fixture with the installation's own
// database, whose stamp a test then writes.
func stampGuardRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r := updateRig(t)
	return r, buildBudgetForRig(t, r)
}

// dbState is the fake database's whole content as bytes: what a run that wrote
// nothing leaves exactly as it was.
func dbState(t *testing.T, db *gcpfake.RTDB) string {
	t.Helper()
	b, err := json.Marshal(db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// readWhole is a file's bytes as a string.
func readWhole(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// refuseBelowTheStamp runs one door command and requires the fence's own
// refusal: exit 1, the stamp the fixture wrote, this binary's version, and the
// brew command. It returns everything the run said.
func refuseBelowTheStamp(t *testing.T, min string, args ...string) string {
	t.Helper()
	out, errOut, err := executeStdin(t, "", args...)
	if err == nil {
		t.Fatalf("fugaro %s exited 0 below the stamp:\n%s%s", strings.Join(args, " "), out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("fugaro %s exited %d, want %d", strings.Join(args, " "), got, ExitUserError)
	}
	var be *versionstamp.BelowError
	if !errors.As(err, &be) {
		t.Fatalf("fugaro %s did not refuse with the stamp's own error: %v", strings.Join(args, " "), err)
	}
	if be.Stamp.Min != min || be.Version != Version {
		t.Errorf("the refusal says %s requires %s and this is %s, want %s and %s", strings.Join(args, " "), be.Stamp.Min, be.Version, min, Version)
	}
	if !strings.Contains(err.Error(), updatecheck.BrewCommand) {
		t.Errorf("the refusal does not name the fix %q: %v", updatecheck.BrewCommand, err)
	}
	return refusalText(out, errOut, err)
}

// noStampSource replaces the stamp's reader with one that records whether it
// was asked anything: the two modes that make no cloud call must not read the
// node, and a test that only asserted their output would pass if they did.
func noStampSource(t *testing.T) *countingStampSource {
	t.Helper()
	src := &countingStampSource{}
	was := newStampSource
	newStampSource = func(versionstamp.Options) versionstamp.Source { return src }
	t.Cleanup(func() { newStampSource = was })
	return src
}

// countingStampSource answers nothing and counts the calls.
type countingStampSource struct{ calls int }

func (s *countingStampSource) Required(context.Context, versionstamp.Query) (versionstamp.Result, error) {
	s.calls++
	return versionstamp.Result{}, nil
}

// An `update` below the stamp refuses before its first write: the converge, the
// catalog guard, syncAccess and rewriteLocalBudgetMode all write, and none of
// them runs. `--forget` is the red step (F24): a check that sat only at the
// converge would roll the installation's Terraform state back here, because
// --forget writes nothing of the converge and reaches no stage.
func TestUpdateBelowTheStampRefusesBeforeAnyWrite(t *testing.T) {
	r, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	beforeDB, beforeCfg := dbState(t, db), readWhole(t, r.cfg)
	refuseBelowTheStamp(t, "0.9.9", "update")
	// Nothing was written, anywhere: no Terraform call, the database's whole
	// content, the local config file with the image catalog it carries, and not
	// one request to the bucket the catalog and the state live in.
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("fugaro update ran terraform below the stamp: %q", calls)
	}
	if got := dbState(t, db); got != beforeDB {
		t.Errorf("the database moved below the stamp:\n%s\nwant\n%s", got, beforeDB)
	}
	if got := readWhole(t, r.cfg); got != beforeCfg {
		t.Errorf("the local config moved below the stamp:\n%s\nwant\n%s", got, beforeCfg)
	}
	for _, rq := range r.gcs.Requests() {
		t.Errorf("fugaro update reached the runs bucket below the stamp: %s %s", rq.Method, rq.Path)
	}
}

// --forget rolls Terraform's state back, and it is refused too.
func TestForgetBelowTheStampRefuses(t *testing.T) {
	r, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	refuseBelowTheStamp(t, "0.9.9", "update", "--forget")
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("fugaro update --forget rolled the state back below the stamp: %q", calls)
	}
}

// --config-only writes the local config, and it is refused too.
func TestConfigOnlyBelowTheStampRefuses(t *testing.T) {
	r, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	before := readWhole(t, r.cfg)
	refuseBelowTheStamp(t, "0.9.9", "update", "--config-only")
	if got := readWhole(t, r.cfg); got != before {
		t.Errorf("fugaro update --config-only wrote the local config below the stamp:\n%s", got)
	}
}

// --plan-only writes nothing at all, and it is refused all the same: a plan
// from a binary below the installation is the downgrade the refusal stops
// (R8), and refusing costs nothing.
func TestUpdatePlanOnlyBelowTheStampRefuses(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	refuseBelowTheStamp(t, "0.9.9", "update", "--plan-only")
}

// `update --check` and `update --print-vars` are the two modes of the door that
// make no cloud call, and neither reads the stamp (R8, F24). `--check` answers
// from the checkout's own files and promises no cloud call at all; --print-vars
// renders the installation's variables the same way.
func TestUpdateCheckModeReadsNoStamp(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	src := noStampSource(t)
	out, errOut, _ := executeStdin(t, "", "update", "--check")
	if src.calls != 0 {
		t.Errorf("update --check read the stamp %d times, and promises no cloud call:\n%s%s", src.calls, out, errOut)
	}
}

func TestPrintVarsReadsNoStamp(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	src := noStampSource(t)
	out, errOut, err := executeStdin(t, "", "update", "--print-vars")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if src.calls != 0 {
		t.Errorf("update --print-vars read the stamp %d times, and makes no cloud call:\n%s%s", src.calls, out, errOut)
	}
}

// `setup` is the other door newInitCmdWith builds, and an installation ahead of
// this binary refuses it too: it converges the same installation, so an older
// binary re-applying its own images and unit is the same downgrade.
func TestSetupBelowTheStampRefuses(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	refuseBelowTheStamp(t, "0.9.9", "setup")
}

// A stamp this binary cannot read refuses with *versionstamp.MalformedError's
// own sentence, which names the node and the one thing that repairs it: an
// unknown schema was written by a newer release (the upgrade), and everything
// else is a hand edit (the Firebase console).
func TestUpdateRefusesAMalformedStamp(t *testing.T) {
	for name, tc := range map[string]struct {
		node map[string]any
		fix  string
	}{
		"a schema this binary does not read": {
			node: map[string]any{"schema": versionstamp.SchemaVersion + 1, "min": "0.9.9", "set_at": "2026-11-20T12:00:00Z"},
			fix:  updatecheck.BrewCommand,
		},
		"a min that is not a release": {
			node: map[string]any{"schema": versionstamp.SchemaVersion, "min": "next", "set_at": "2026-11-20T12:00:00Z"},
			fix:  "Firebase console",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, db := stampGuardRig(t)
			db.Set(versionstamp.Path, tc.node)
			out, errOut, err := executeStdin(t, "", "update")
			if err == nil {
				t.Fatalf("update converged past a stamp it cannot read:\n%s%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			var me *versionstamp.MalformedError
			if !errors.As(err, &me) {
				t.Fatalf("the refusal is not the malformed stamp's own: %v", err)
			}
			said := refusalText(out, errOut, err)
			if !strings.Contains(said, versionstamp.Path) || !strings.Contains(said, tc.fix) {
				t.Errorf("the refusal does not name the node and its fix (%s):\n%s", tc.fix, said)
			}
		})
	}
}

// `update --repo PATH` acts on the checkout at PATH, so the fence reads *that*
// checkout's installation and not the working directory's: a run started from
// inside another project's checkout is still the downgrade of the installation
// it is about to write to, and a fence that resolved the working directory would
// let it through (or refuse a project the run never touches).
func TestUpdateRepoBelowTheStampRefuses(t *testing.T) {
	r, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	// The installation's file where a checkout's `project:` finds it: the rig
	// keeps its project config beside the test and $FUGARO_CONFIG is the other
	// spelling of the same thing, so the checkout decides here as it does on a
	// real machine.
	path, err := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(readWhole(t, r.cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	dir := t.TempDir()
	app := checkoutsAt(t, filepath.Join(dir, "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n",
		"https://github.com/acme/app.git")
	// The working directory is another project's checkout, and it is not the
	// one this run is about.
	t.Chdir(checkoutsAt(t, filepath.Join(dir, "other"), "version: 1\nproject: beta\n", "https://github.com/acme/other.git"))
	out, errOut, err := executeStdin(t, "", "update", "--repo", app)
	if err == nil {
		t.Fatalf("update --repo %s exited 0 below the stamp:\n%s%s", app, out, errOut)
	}
	var be *versionstamp.BelowError
	if !errors.As(err, &be) {
		t.Fatalf("update --repo %s did not refuse with the installation's own stamp: %v\n%s%s", app, err, out, errOut)
	}
	if be.Stamp.Min != "0.9.9" || be.Version != Version {
		t.Errorf("the refusal says %s requires %s and this is %s, want 0.9.9 and %s", app, be.Stamp.Min, be.Version, Version)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("fugaro update --repo ran terraform below the stamp: %q", calls)
	}
}

// Under --json the refusal still prints the one object a refusal before any
// stage prints, with the reason in it and the same exit code as without --json
// (TestInitJSONOnARefusalBeforeAnyStage's contract).
func TestUpdateJSONRefusalIsOneObject(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	out, _, err := executeStdin(t, "", "update", "--json")
	if err == nil {
		t.Fatal("update exited 0 below the stamp under --json")
	}
	_, _, plainErr := executeStdin(t, "", "update")
	if ExitCode(err) != ExitCode(plainErr) {
		t.Errorf("exit %d with --json, %d without", ExitCode(err), ExitCode(plainErr))
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var got struct {
		Error      string `json:"error"`
		LeftForYou []any  `json:"left_for_you"`
	}
	if jerr := dec.Decode(&got); jerr != nil || dec.More() {
		t.Fatalf("stdout is not exactly one JSON object (%v):\n%q", jerr, out)
	}
	if got.Error != err.Error() || got.LeftForYou == nil {
		t.Errorf("the object says %q (want %q), left_for_you %v", got.Error, err.Error(), got.LeftForYou)
	}
}

// At or below the stamp the door runs: the stamp is a minimum, so a binary at
// it passes (spec, "Mixed-CLI corner cases"), and so does one above an older
// stamp.
func TestUpdateAtTheStampPasses(t *testing.T) {
	for name, tc := range map[string]struct {
		atTheStamp bool
		min        string
	}{
		"at the stamp":         {atTheStamp: true},
		"above an older stamp": {min: "0.1.0"},
	} {
		t.Run(name, func(t *testing.T) {
			// The rig is what sets this binary's version (releaseBuild), so the
			// stamp the case is about is read after it and not in the table.
			_, db := stampGuardRig(t)
			min := tc.min
			if tc.atTheStamp {
				min = Version
			}
			stampNode(db, min)
			fakeTerminal(t)
			out, errOut, err := executeStdin(t, "", "update")
			if err != nil {
				t.Fatalf("update refused a stamp of %s with this binary at %s: %v\n%s%s", min, Version, err, out, errOut)
			}
		})
	}
}

// A database that cannot be read fails open: the one command that brings the
// installation forward is not stopped by a blip, and it says why on stderr
// (D3264).
func TestUpdateFailsOpenWhenTheStampCannotBeRead(t *testing.T) {
	_, db := stampGuardRig(t)
	db.RefusePath("required_cli", 503, "UNAVAILABLE", "", "down")
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("update stopped on a stamp it could not read: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "could not read the installation's required CLI") {
		t.Errorf("the run did not say the stamp could not be read:\n%s", errOut)
	}
}

// A fresh read that fails still refuses on a cached value above the binary that
// is younger than the trust window (T1's rule, D3264 bounded by D3268): the
// first run's successful read wrote the cache, and the second run's read — the
// database is down now — falls back to it.
func TestUpdateRefusesOnACachedStampWhenTheReadFails(t *testing.T) {
	_, db := stampGuardRig(t)
	stampNode(db, "0.9.9")
	refuseBelowTheStamp(t, "0.9.9", "update")
	// The stamp is off the database and the database is down: only the file the
	// first run read it into can answer now.
	db.Set(versionstamp.Path, nil)
	db.Refuse(503, "UNAVAILABLE", "", "down")
	refuseBelowTheStamp(t, "0.9.9", "update")
}
