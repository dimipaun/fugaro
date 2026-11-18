package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// project_binaries_test.go is K2's own tests (docs/config-ownership/
// implementation-plan.md): the grant the owner states in this machine's project
// config, the pattern rule applied at write, the cap, and the session fence on
// the widening direction.
//
// The read-back is localcfg.Parse and never a string search: the block is a
// member of the config file, and what a run of the project is held to is the
// block the loader reads out of it — the same loader `fugaro update` publishes
// from and the node's decode is held to (AR1, D981–D983). The contradiction is
// the one this command has to close: it wrote the carrier, so what a later edit
// or a publish finds is the bytes a person reads.

// projectBinariesConfig reads the file back the way every reader of the local
// config does.
func projectBinariesConfig(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("%s does not load after the write: %v", path, err)
	}
	return lc
}

// TestBinariesAllowWritesTheCarrier: `allow` adds one pattern to the
// binaries.allow list of this machine's project config, and the file states a
// block the loader reads that pattern out of. The widening sentence is the
// other half of the write: the pattern is the owner's grant over the bytes a run
// may add, and what a person reads before it lands says what it admits.
func TestBinariesAllowWritesTheCarrier(t *testing.T) {
	_, path := settingsWriteHome(t)
	out, _, err := execute(t, "project", "binaries", "allow", "brand/*.png")
	if err != nil {
		t.Fatalf("project binaries allow: %v", err)
	}
	lc := projectBinariesConfig(t, path)
	if lc.Binaries == nil || len(lc.Binaries.Allow) != 1 || lc.Binaries.Allow[0] != "brand/*.png" {
		t.Fatalf("binaries = %+v after the write, want the one pattern", lc.Binaries)
	}
	if lc.BinariesEffective(nil) == nil {
		t.Error("the block the file states reads as no block at all: a written block is what a publish carries")
	}
	for _, want := range []string{
		"add brand/*.png to binaries.allow in",
		"this widens the project's binary admission",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run printed no %q:\n%s", want, out)
		}
	}
	// A second call adds the second pattern and leaves the first where it is:
	// the block is a list and one call writes one entry of it.
	if _, _, err := execute(t, "project", "binaries", "allow", "assets/*.webp"); err != nil {
		t.Fatalf("the second allow: %v", err)
	}
	lc = projectBinariesConfig(t, path)
	if lc.Binaries == nil || len(lc.Binaries.Allow) != 2 {
		t.Fatalf("binaries = %+v after the second write, want both patterns", lc.Binaries)
	}
}

// TestBinariesAllowRefusesAMisspeltPattern: the pattern is held to the node's
// own rule before a byte is built, so a pattern the matcher cannot read is
// refused rather than written — an unreadable pattern is a lock that admits
// nothing while looking like a grant, and the run that meets it fails finalize
// naming a path nobody can find in the file.
//
// The refusal pinned here is the command's, and it is told from the loader's by
// its own sentence: the local config refuses the same bytes a second time
// (settingsReparse), so a test that read only the rule's words would pass with
// this check deleted and the file already written.
func TestBinariesAllowRefusesAMisspeltPattern(t *testing.T) {
	_, path := settingsWriteHome(t)
	before := readFileString(t, path)
	_, _, err := execute(t, "project", "binaries", "allow", "brand/[.png")
	if err == nil {
		t.Fatal("a pattern the matcher cannot read was accepted: it admits nothing and looks like a grant")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	for _, want := range []string{"binaries.allow[0]", "not a valid glob pattern", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("%s changed under a refused write:\n%s", path, got)
	}
}

// TestBinariesMaxBytesWrites: `max-bytes` writes the cap into the block the file
// states, and the document's own loader reads it back. A cap above the ceiling
// is the rule's refusal rather than a written member: a project may only lower
// the 2 MiB bound, and a cap of zero would silently raise it to that bound.
func TestBinariesMaxBytesWrites(t *testing.T) {
	_, path := settingsWriteHomeWith(t, "binaries:\n  allow:\n    - brand/*.png\n  max_bytes: 2097152\n")
	out, _, err := execute(t, "project", "binaries", "max-bytes", "1048576")
	if err != nil {
		t.Fatalf("project binaries max-bytes: %v", err)
	}
	lc := projectBinariesConfig(t, path)
	if lc.Binaries == nil || lc.Binaries.MaxBytes != 1048576 {
		t.Fatalf("binaries = %+v after the write, want the lowered cap", lc.Binaries)
	}
	if len(lc.Binaries.Allow) != 1 || lc.Binaries.Allow[0] != "brand/*.png" {
		t.Errorf("binaries = %+v: the cap write took the allow list with it", lc.Binaries)
	}
	if !strings.Contains(out, "set binaries.max_bytes to 1048576 in") {
		t.Errorf("the run does not name the cap it wrote:\n%s", out)
	}
	for _, tc := range []struct{ value, want string }{
		{"2097153", "at most 2097152 bytes"},
		{"0", "must be greater than 0"},
		{"1M", "takes a whole number of bytes"},
	} {
		_, _, err := execute(t, "project", "binaries", "max-bytes", tc.value)
		if err == nil {
			t.Errorf("max-bytes %s was accepted", tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("max-bytes %s: the refusal does not name %q: %v", tc.value, tc.want, err)
		}
		if got := projectBinariesConfig(t, path); got.Binaries.MaxBytes != 1048576 {
			t.Errorf("max-bytes %s changed the cap to %d", tc.value, got.Binaries.MaxBytes)
		}
	}
}

// TestBinariesAllowRefusedInAnAgentSession is D2617 for this command: admitting
// paths is the widening write, so a coding agent's session is refused it
// whatever the gate says — for every marker the tree sets (agentenv.Markers
// itself rather than a copy). The refusal prints the diff, names the command to
// run at the owner's own terminal, and writes nothing at all.
//
// The gate stands in for nothing here: this world's installation names no marks,
// so the same write lands at the owner's own terminal (the test above), and what
// refuses in the session is the edit's own widening marker (D1026, D1552).
func TestBinariesAllowRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			_, path := settingsWriteHome(t)
			before := readFileString(t, path)
			t.Setenv(marker, "1")
			_, _, err := execute(t, "project", "binaries", "allow", "brand/*.png")
			if err == nil {
				t.Fatal("a coding agent's session widened the project's binary admission")
			}
			for _, want := range []string{
				marker + " is set",
				"refusing to write",
				"run this yourself at your own terminal",
				"fugaro project binaries allow 'brand/*.png'",
				"nothing was written",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("%s changed under a refused write:\n%s", path, got)
			}
		})
	}
}

// TestBinariesDenyTakesThePatternAway: `deny` removes the entry it names and
// keeps every other one, and the last entry leaves a *written* empty list rather
// than a removed key — which is the difference between a file that states a
// block admitting nothing, the one spelling a publish takes a published grant
// away with, and a file that says nothing about the block, which keeps the
// node's (D987).
//
// A file that states no block at all is refused rather than answered: there is
// no entry beside the published list to take away, and the only list a removal
// could write there is an empty one.
func TestBinariesDenyTakesThePatternAway(t *testing.T) {
	_, path := settingsWriteHomeWith(t, "binaries:\n  allow:\n    - brand/*.png\n    - assets/*.webp\n")
	if _, _, err := execute(t, "project", "binaries", "deny", "brand/*.png"); err != nil {
		t.Fatalf("project binaries deny: %v", err)
	}
	lc := projectBinariesConfig(t, path)
	if lc.Binaries == nil || len(lc.Binaries.Allow) != 1 || lc.Binaries.Allow[0] != "assets/*.webp" {
		t.Fatalf("binaries = %+v after the removal, want the other pattern", lc.Binaries)
	}
	if _, _, err := execute(t, "project", "binaries", "deny", "assets/*.webp"); err != nil {
		t.Fatalf("denying the last pattern: %v", err)
	}
	lc = projectBinariesConfig(t, path)
	if lc.Binaries == nil {
		t.Fatalf("%s no longer states a binaries block, and the key taken away is the file saying nothing: the published grant would stand", path)
	}
	if lc.Binaries.Allow != nil && len(lc.Binaries.Allow) != 0 {
		t.Fatalf("binaries = %+v, want an empty list", lc.Binaries)
	}
	if got := lc.BinariesEffective(&projectpolicy.Binaries{Allow: []string{"brand/*.png"}}); got != nil {
		t.Errorf("the empty block publishes %+v, want none: a written empty block is how a published admission is taken away (D987)", got)
	}
	// A pattern the file does not carry is not a failure, and it writes nothing:
	// the list this file states is the grant a publish carries, so the entry is
	// already not admitted by anything this file says.
	out, _, err := execute(t, "project", "binaries", "deny", "nothing/*.none")
	if err != nil {
		t.Fatalf("denying a pattern the file does not carry: %v", err)
	}
	if !strings.Contains(out, "carries no nothing/*.none") {
		t.Errorf("the run does not say the list carries no such entry:\n%s", out)
	}
	// A file that states no block at all: a refusal, and the file is left alone.
	_, bare := settingsWriteHome(t)
	before := readFileString(t, bare)
	_, _, err = execute(t, "project", "binaries", "deny", "brand/*.png")
	if err == nil {
		t.Fatal("a denial was written into a file that states no binaries block: the only list it could write admits nothing")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if got := readFileString(t, bare); got != before {
		t.Errorf("%s changed under a refused write:\n%s", bare, got)
	}
}
