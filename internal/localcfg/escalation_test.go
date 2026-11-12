package localcfg

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// escalationBlock is the ladder's block written out whole: every key the local
// file may hold, one scalar and one mode's own member among them.
const escalationBlock = `escalation:
  r2_after_hours: 6
  r2_after_attempts: 4
  r3_after_hours: 18
  critical_path_factor: 0.4
  cost_ceiling: 40
  r2_coder: strongest
  merge_judge: strong
  modes:
    pro:
      r2_after_hours: 8
      r2_coder: strong
`

// TestEscalationKeysDefaultsPerMode: the file states thresholds per rigor mode,
// and the table a run gets is the shipped one (escalation.DefaultThresholds)
// with this file's keys laid over it — a scalar over every mode, a mode's own
// member over that mode alone. The two directions are what the keys are for: a
// person raises or lowers the ladder for the whole project, or for the one
// rigor mode whose work is the one stalling.
func TestEscalationKeysDefaultsPerMode(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := escalation.DefaultThresholds()
	got := c.EscalationThresholds()
	if got.CriticalPathFactor != DefaultEscalationCriticalPathFactor || got.CriticalPathFactor != want.CriticalPathFactor {
		t.Errorf("critical_path_factor = %v for a file that sets none, want the default %v", got.CriticalPathFactor, DefaultEscalationCriticalPathFactor)
	}
	if got.CostCeiling != 0 || want.CostCeiling != 0 {
		t.Errorf("cost_ceiling = %v for a file that sets none, want the 0 that means no ceiling", got.CostCeiling)
	}
	for _, mode := range rigor.Modes {
		if got.Rows[mode] != want.Rows[mode] {
			t.Errorf("%s: row = %+v, want the shipped %+v", mode, got.Rows[mode], want.Rows[mode])
		}
	}
	// The builder hands out a fresh table, so a config that overrides a row
	// cannot reach the next caller's copy of the shipped one.
	if escalation.DefaultThresholds().Rows[rigor.POC].R2Hours != 3 {
		t.Error("the shipped table moved: EscalationThresholds edited the package's own rows")
	}

	lc, err := Parse([]byte(sample + escalationBlock))
	if err != nil {
		t.Fatal(err)
	}
	th := lc.EscalationThresholds()
	if th.CriticalPathFactor != 0.4 {
		t.Errorf("critical_path_factor = %v, want the written 0.4", th.CriticalPathFactor)
	}
	if th.CostCeiling != 40 {
		t.Errorf("cost_ceiling = %v, want the written 40", th.CostCeiling)
	}
	for _, mode := range rigor.Modes {
		row := th.Rows[mode]
		// Pro is the mode whose own members the block overrides; every other
		// mode reads the block's scalars.
		wantHours, wantGrade := 6.0, escalation.Strongest
		if mode == rigor.Pro {
			wantHours, wantGrade = 8, escalation.Strong
		}
		if row.RunsBeforeR2 != 4 || row.R2Hours != wantHours || row.CeilingHours != 18 || row.Grade != wantGrade {
			t.Errorf("%s: %+v, want the block's scalars over every mode, and pro's own member over them (4 runs, %v h before rung 2, 18 h ceiling, %s)", mode, row, wantHours, wantGrade)
		}
	}
	pro := th.Rows[rigor.Pro]
	// What a mode does at its ceiling is not a threshold a file states: it is
	// who is told and who must approve, and the shipped row keeps it.
	if pro.AtCeiling != escalation.CeilingAutoMergeNotify {
		t.Errorf("pro: AtCeiling = %q, and the block states no such key", pro.AtCeiling)
	}
}

// TestEscalationReadyDraftsDefaultsOff: readying a draft is authority nobody
// asked for when the file says nothing (D2046, T3), so the key ships off and a
// written true is the only way it is on.
func TestEscalationReadyDraftsDefaultsOff(t *testing.T) {
	if DefaultEscalationReadyDrafts {
		t.Error("the default is on, and the daemon's readiness authority ships off")
	}
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.EscalationReadyDrafts() {
		t.Error("ready_drafts is on for a file that sets the block nowhere")
	}
	for _, tc := range []struct {
		doc  string
		want bool
	}{
		{sample + "escalation:\n  ready_drafts: true\n", true},
		{sample + "escalation:\n  ready_drafts: false\n", false},
	} {
		lc, err := Parse([]byte(tc.doc))
		if err != nil {
			t.Fatalf("%q is refused: %v", tc.doc, err)
		}
		if got := lc.EscalationReadyDrafts(); got != tc.want {
			t.Errorf("ready_drafts = %v, want %v", got, tc.want)
		}
	}

	// The judge's grade is the other key read by name rather than by figure,
	// and a file that names none answers no grade at all rather than one this
	// code chose (G5): a judge nobody appointed is a question, not a default.
	if _, ok := c.EscalationMergeJudge(); ok {
		t.Error("a file that names no merge judge answers a grade")
	}
	graded, err := Parse([]byte(sample + "escalation:\n  merge_judge: strongest\n"))
	if err != nil {
		t.Fatal(err)
	}
	g, ok := graded.EscalationMergeJudge()
	if !ok || g != escalation.Strongest {
		t.Errorf("merge_judge = (%q, %v), want (strongest, true)", g, ok)
	}
}

// TestEscalationKeysRefuseUnreadableValues: every key is held to the range the
// decision reads (G5). A threshold nothing can compare, a scale of zero and a
// grade outside the vocabulary are refused where the file is loaded, so the
// daemon never meets a figure it has to guess at.
func TestEscalationKeysRefuseUnreadableValues(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"r2_after_hours", "-1", "r2_after_hours"},
		{"r2_after_attempts", "-1", "r2_after_attempts"},
		{"r3_after_hours", "0", "r3_after_hours"},
		{"critical_path_factor", "0", "critical_path_factor"},
		{"critical_path_factor", "1.5", "critical_path_factor"},
		{"cost_ceiling", "0", "cost_ceiling"},
		{"cost_ceiling", "-5", "cost_ceiling"},
		{"r2_coder", "tier2", "r2_coder"},
		{"merge_judge", "medium", "merge_judge"},
	} {
		doc := sample + "escalation:\n  " + tc.key + ": " + tc.value + "\n"
		_, err := Parse([]byte(doc))
		if err == nil {
			t.Errorf("escalation.%s: %s is accepted", tc.key, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("escalation.%s: %s is refused with %q, which does not name the key", tc.key, tc.value, err)
		}
	}
	// A mode's own member is held to the same ranges, and the mode itself to
	// the vocabulary rigor.Parse owns.
	for _, tc := range []struct{ key, value, want string }{
		{"modes.poc.r2_after_hours", "-2", "modes.poc.r2_after_hours"},
		{"modes.poc.r2_coder", "sonnet", "modes.poc.r2_coder"},
		{"modes.fast.r2_after_hours", "1", "fast"},
	} {
		doc := sample + "escalation:\n  " + tc.key + ": " + tc.value + "\n"
		_, err := Parse([]byte(doc))
		if err == nil {
			t.Errorf("escalation.%s: %s is accepted", tc.key, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("escalation.%s: %s is refused with %q, which does not name %s", tc.key, tc.value, err, tc.want)
		}
	}
}

// TestEscalationKeysRefusedInRepo: a repository's `fugaro.yaml` is a reviewed
// file a run pushes, so the ladder's thresholds — who may merge, and at what
// cost — are refused there (G3, D2032): the strict decoder refuses the block
// by name, no config.Scopes key spells it, and the local file is the only
// store the same lines are read from.
func TestEscalationKeysRefusedInRepo(t *testing.T) {
	const head = "version: 1\nproject: acme\ngcp_project: acme-1\n"
	for _, doc := range []string{
		head + "escalation:\n  r2_after_hours: 6\n",
		head + "escalation:\n  ready_drafts: true\n",
		head + "escalation:\n  merge_judge: strongest\n",
	} {
		if _, ps := config.Parse([]byte(doc)); len(ps) == 0 {
			t.Errorf("a repository's fugaro.yaml accepts:\n%s", doc)
		}
	}
	for _, s := range config.Scopes {
		if s.Key == "escalation" || strings.HasPrefix(s.Key, "escalation.") {
			t.Errorf("%s: config.Scopes carries the key, so a repository could state it in its own file", s.Key)
		}
	}
	if _, err := Parse([]byte(sample + "escalation:\n  r2_after_hours: 6\n")); err != nil {
		t.Errorf("the local config refuses the keys it is the only store of: %v", err)
	}
}

// TestEscalationKeysRefusedInProjectLayer: the project layer is the file
// `fugaro config publish` carries and every run of the project reads, so a
// block that decides when a run may merge itself is refused there too (G3):
// the layer's own strict parsers refuse it by name, the published shared file
// never holds it, and the receiver that published it keeps its own copy.
func TestEscalationKeysRefusedInProjectLayer(t *testing.T) {
	for _, doc := range []string{
		"escalation:\n  r2_after_hours: 6\n",
		"defaults:\n  escalation:\n    ready_drafts: true\n",
		"profiles:\n  web:\n    escalation:\n      r2_coder: strongest\n",
	} {
		if _, ps := config.ParseProjectLayer([]byte(doc), config.LayerAnchor{}); len(ps) == 0 {
			t.Errorf("the project layer accepts:\n%s", doc)
		}
	}
	lc, err := Parse([]byte(sample + escalationBlock))
	if err != nil {
		t.Fatal(err)
	}
	if lc.Escalation == nil {
		t.Fatal("the fixture records no block to drop")
	}
	out, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"escalation", "r2_after_hours", "ready_drafts", "merge_judge"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("the published shared file carries %q:\n%s", forbidden, out)
		}
	}
	if _, err := Parse(out); err != nil {
		t.Errorf("the published file does not read back: %v", err)
	}
	if got := lc.EscalationThresholds().CostCeiling; got != 40 {
		t.Errorf("Shared cleared the receiver's own block: cost_ceiling = %v, want the written 40", got)
	}
}
