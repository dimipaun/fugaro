package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// doctorStampRig is a policy rig whose project carries a required-CLI stamp at
// min: the state cli-below-required exists for. The rig's binary is 0.2.0
// (withVersion in newDoctorRig), so a min above it is a stamp this binary is
// below, and one at or under it is not.
func doctorStampRig(t *testing.T, min string) *gcpfake.RTDB {
	t.Helper()
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	seedStamp(t, db, min)
	return db
}

// seedStamp writes the project's stamp node the way `fugaro update` writes it
// (versionstamp.Encode's shape), so the reader under test decodes real bytes and
// not a fixture built to pass it.
func seedStamp(t *testing.T, db *gcpfake.RTDB, min string) {
	t.Helper()
	raw, err := versionstamp.Encode(versionstamp.Stamp{
		Schema: versionstamp.SchemaVersion, Min: min,
		SetAt: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	db.Set(versionstamp.Path, node)
}

// stampRow runs `fugaro doctor --json` with args and returns the
// cli-below-required row of the document, read the way a reader outside this
// binary reads it.
func stampRow(t *testing.T, args ...string) doctorCheck {
	t.Helper()
	out, _, _ := execute(t, append([]string{"doctor", "--json", "--dir", t.TempDir()}, args...)...)
	c, ok := doctorRow(t, out, cliBelowRequiredID)
	if !ok {
		t.Fatalf("doctor --json carries no %s row:\n%s", cliBelowRequiredID, out)
	}
	return c
}

// doctorRow decodes one `doctor --json` document and picks a check out of it.
func doctorRow(t *testing.T, out, id string) (doctorCheck, bool) {
	t.Helper()
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	return doctorCheckByID(o.Checks, id)
}

// TestDoctorReportsCLIBelowRequired is T5's own check: doctor has a
// cli-below-required row at all. With the stamp above this binary it is a
// warning in the update class, naming both releases and the two commands that
// upgrade; at or below the stamp there is nothing to say, and a node that is not
// there is a project with no fence.
func TestDoctorReportsCLIBelowRequired(t *testing.T) {
	t.Run("a stamp above this binary warns", func(t *testing.T) {
		doctorStampRig(t, "0.3.0")
		c := stampRow(t)
		if c.OK || c.Severity != "warning" {
			t.Fatalf("cli-below-required = %+v, want a warning: the project requires a release this binary is below", c)
		}
		if c.Class != doctorClassUpdate {
			t.Errorf("cli-below-required is classed %q, want %q: the class's one line is the upgrade and the update", c.Class, doctorClassUpdate)
		}
		for _, want := range []string{"0.3.0", "0.2.0"} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("cli-below-required's problem does not name %s: %q", want, c.Problem)
			}
		}
		if !strings.Contains(c.Fix, "brew upgrade") || !strings.Contains(c.Fix, "fugaro update") {
			t.Errorf("cli-below-required's fix is not the brew command and then the update: %q", c.Fix)
		}
		// And the default view prints it as an item, with the update class's own
		// one line (doctorUpdateFixes), which is the whole point of the class.
		view, _, _ := execute(t, "doctor", "--dir", t.TempDir())
		const head = "warning cli-below-required: this project requires a newer Fugaro release than this binary."
		if !strings.Contains(view, head) {
			t.Errorf("the default view does not print the stamp warning:\n%s", view)
		}
		if !strings.Contains(view, cliBelowRequiredFix) {
			t.Errorf("the item does not name the two upgrade commands:\n%s", view)
		}
	})

	t.Run("at the stamp and below it there is nothing to say", func(t *testing.T) {
		for _, min := range []string{"0.2.0", "0.1.0"} {
			doctorStampRig(t, min)
			if c := stampRow(t); !c.OK {
				t.Errorf("cli-below-required = %+v with the stamp at %s: the stamp is a minimum, so a binary at or above it passes", c, min)
			}
			// A passing check is an item of no view: the default one prints the
			// checks that ask something of the reader.
			out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
			if strings.Contains(out, cliBelowRequiredID) {
				t.Errorf("the default view prints a passing cli-below-required (stamp %s):\n%s", min, out)
			}
		}
	})

	t.Run("a project with no stamp is a project with no fence", func(t *testing.T) {
		_, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		if db.Value(versionstamp.Path) != nil {
			t.Fatalf("the rig seeded %s, and this case is the project that never wrote one", versionstamp.Path)
		}
		if c := stampRow(t); !c.OK {
			t.Errorf("cli-below-required = %+v with no stamp node, want a pass: a node that is not there fences nothing", c)
		}
	})

	t.Run("a dev build has nothing to compare", func(t *testing.T) {
		// The rig's own newDoctorRig pins 0.2.0, so the dev build comes after it.
		doctorStampRig(t, "0.3.0")
		withVersion(t, "dev")
		if c := stampRow(t); !c.OK {
			t.Errorf("cli-below-required = %+v for a dev build, want a silent pass (spec, Dev builds)", c)
		}
	})
}

// TestDoctorOfflineAnswersFromTheStampCache: under --offline the check answers
// from this machine's stamp cache (R9). The cache is primed by the ordinary run
// over the same project, and the offline run behind it reports the same warning
// with not one request reaching the database.
func TestDoctorOfflineAnswersFromTheStampCache(t *testing.T) {
	db := doctorStampRig(t, "0.3.0")
	// The ordinary run reads the node and writes the cache: the state of a
	// machine that has run any project command since the stamp rose.
	if c := stampRow(t); c.OK || c.Severity != "warning" {
		t.Fatalf("the ordinary run did not report the stamp: %+v", c)
	}
	before := len(db.Requests())
	c := stampRow(t, "--offline")
	if after := len(db.Requests()); after != before {
		t.Fatalf("--offline made %d database requests: R9 answers from the cache and reads no database", after-before)
	}
	if c.Skipped || c.OK || c.Severity != "warning" {
		t.Fatalf("cli-below-required = %+v under --offline with a cache entry, want the warning the entry holds", c)
	}
	if !strings.Contains(c.Problem, "0.3.0") {
		t.Errorf("the offline answer does not name the required release: %q", c.Problem)
	}
}

// TestDoctorOfflineSkipsWithNoCache: with no cache entry to answer from,
// --offline skips the check with its reason rather than reading the database
// (R9): a skipped check is not ok, says what it could not read, and fails
// nothing. The skip is the absence of a file, not a refusal to answer, so the
// same flag answers once an ordinary run has written one.
func TestDoctorOfflineSkipsWithNoCache(t *testing.T) {
	db := doctorStampRig(t, "0.3.0")
	before := len(db.Requests())
	c := stampRow(t, "--offline")
	if after := len(db.Requests()); after != before {
		t.Fatalf("--offline made %d database requests with no cache entry", after-before)
	}
	if !c.Skipped || c.OK {
		t.Fatalf("cli-below-required = %+v with no cache entry, want a skip: the flag reads no database", c)
	}
	if c.Severity != "info" {
		t.Errorf("a skip is severity %q, want info: it fails nothing, with or without --strict", c.Severity)
	}
	if !strings.Contains(c.Problem, "--offline") {
		t.Errorf("the skip does not say why it was not answered: %q", c.Problem)
	}
	if c.Fix != doctorOfflineAsk {
		t.Errorf("the skip's fix is %q, want every skip's own line %q", c.Fix, doctorOfflineAsk)
	}
	if c := stampRow(t); c.OK || c.Severity != "warning" {
		t.Fatalf("the ordinary run behind the skip did not report the stamp: %+v", c)
	}
	if c := stampRow(t, "--offline"); c.Skipped {
		t.Errorf("cli-below-required is still skipped after an ordinary run cached the stamp: %+v", c)
	}
}

// TestDoctorCLIBelowRequiredIsNotOwnFix: the below-stamp state's line is the
// update class's own (the upgrade, then `fugaro update`), not the check's own
// text — which is what ownFix would print — because those two commands are
// exactly what repairs it. The one state that does carry ownFix is a stamp this
// binary cannot read at all, which is why the two are pinned together here.
func TestDoctorCLIBelowRequiredIsNotOwnFix(t *testing.T) {
	db := doctorStampRig(t, "0.3.0")
	c := stampCheck(t, db)
	if c.OK || c.Severity != "warning" {
		t.Fatalf("the check the rig's own node builds = %+v, want the warning", c)
	}
	if c.ownFix {
		t.Fatalf("the below-stamp state carries ownFix, and the class's two commands are its repair")
	}
	if got := doctorClassOf(c); got != doctorClassUpdate {
		t.Fatalf("cli-below-required is classed %q, want %q", got, doctorClassUpdate)
	}
	if got := doctorShortFix(c); got != cliBelowRequiredFix {
		t.Errorf("the default view's line is %q, want the class's %q", got, cliBelowRequiredFix)
	}
	if fix, ok := doctorUpdateFixes[cliBelowRequiredID]; !ok || fix != cliBelowRequiredFix {
		t.Errorf("doctorUpdateFixes[%s] = %q (present %v), want %q", cliBelowRequiredID, fix, ok, cliBelowRequiredFix)
	}
	if !doctorHasPrefix(cliBelowRequiredID, doctorUpdatePrefixes) {
		t.Errorf("%s is not in doctorUpdatePrefixes, so the class's line is never reached", cliBelowRequiredID)
	}
	if _, ok := doctorShorts[cliBelowRequiredID]; !ok {
		t.Errorf("%s has no short form, so the default view falls back to the check's own long sentence", cliBelowRequiredID)
	}

	// The malformed state, the one that keeps its own line: `fugaro update`
	// refuses a stamp it cannot read rather than repairing it, so the class's
	// line would send the reader in a circle. Its own database, because the
	// ordinary state above cached a stamp for its URL and a cache answers before
	// the node is ever read.
	_, bad := doctorPolicyRig(t, doctorPolicy(nil), "")
	bad.Set(versionstamp.Path, map[string]any{
		"schema": versionstamp.SchemaVersion, "min": "not-a-release",
		"set_at": time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)})
	c = stampCheck(t, bad)
	if !c.ownFix {
		t.Errorf("a malformed stamp = %+v, want ownFix: the class's line is the wrong repair for a hand edit", c)
	}
	if !strings.Contains(c.Problem, versionstamp.Path) {
		t.Errorf("the malformed stamp's line does not name the node: %q", c.Problem)
	}
	if got := doctorShortFix(c); got == cliBelowRequiredFix || got != firstSentence(c.Fix) {
		t.Errorf("the malformed stamp's line is %q, want the error's own first sentence", got)
	}
}

// stampCheckReader answers the node straight out of the rig's database, so a
// test can build the check with no HTTP and no cache in its way.
type stampCheckReader struct{ node any }

func (r stampCheckReader) Get(_ context.Context, _ string, out any) (bool, error) {
	if r.node == nil {
		return false, nil
	}
	b, err := json.Marshal(r.node)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, out)
}

// stampCheck builds cli-below-required in process against db's node.
func stampCheck(t *testing.T, db *gcpfake.RTDB) doctorCheck {
	t.Helper()
	old := newStampSource
	newStampSource = func(o versionstamp.Options) versionstamp.Source {
		node := db.Value(versionstamp.Path)
		o.Open = func(context.Context, string, bool) (versionstamp.Reader, error) {
			return stampCheckReader{node: node}, nil
		}
		return versionstamp.New(o)
	}
	t.Cleanup(func() { newStampSource = old })
	lc := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{RTDBURL: db.URL}}
	return cliBelowRequiredCheck(context.Background(), lc, false, "0.2.0", budgetNow())
}
