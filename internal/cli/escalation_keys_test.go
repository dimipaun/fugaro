package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The outer loop's ladder is stated in the machine's own config and nowhere
// else (E3, D2032, G3), and these are the three facts the write path owes it:
// the agent-session fence, the typed form for the directions that loosen, and
// the per-mode override table the generic path has to reach at all.

// TestSettingsEscalationKeysRefusedFromAgentSession: the ladder's thresholds
// decide when an item comes off its normal recipe and at what cost it is
// merged, so a coding agent's session writes none of them — --yes included.
// The refusals beside this one are the write path's own (settings_write.go
// refuses every local write in a session); what is checked here is that the
// keys reach that fence rather than the allowlist's "not a key the generic path
// writes", which would refuse them for the wrong reason and leave the owner
// unable to write them at all.
func TestSettingsEscalationKeysRefusedFromAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			for _, edit := range [][]string{
				{"set", "escalation.ready_drafts", "true"},
				{"set", "escalation.r2_after_hours", "1"},
				{"set", "escalation.cost_ceiling", "80"},
				{"set", "escalation.modes.pro.r2_after_hours", "2"},
				{"unset", "escalation.merge_judge"},
			} {
				noTerminal(t)
				_, localPath := settingsWriteHomeWith(t, "escalation:\n  r2_after_hours: 6\n  cost_ceiling: 40\n  merge_judge: strong\n")
				before := readFileString(t, localPath)
				t.Setenv(marker, "1")

				args := append(slices.Clone(edit), "--layer", "local")
				out, _, err := execute(t, append([]string{"settings"}, args...)...)
				if err == nil {
					t.Fatalf("a session wrote %v:\n%s", edit, out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("%v: exit %d, want a user error: %v", edit, ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("%v: the refusal does not name %s, so nobody knows which variable to unset: %v", edit, marker, err)
				}
				if !strings.Contains(err.Error(), "escalation") {
					t.Errorf("%v: the refusal does not name the key: %v", edit, err)
				}
				if got := readFileString(t, localPath); got != before {
					t.Errorf("%v: the file changed in a coding agent's session:\n%s", edit, got)
				}
			}
		})
	}
}

// TestSettingsEscalationLooseningIsTheTypedForm: four of the ladder's keys are
// the wider direction at a *lower* figure, and the fifth — the cost ceiling —
// at a higher one. The classification is the write path's own answer, read here
// from the dry run the console draws its dialog from, and the tightening
// direction is the ordinary write beside it.
//
// The rows with no `old` are the first write: a file that states no escalation
// block is not a file with nothing to lower, because what a run gets today is
// the shipped table (settingsEscalationBase). Their figures are read off that
// table — 9 h and three runs before rung 2, 12 h of ceiling, the factor 0.5 —
// and the `seed` rows state the block's own scalar for a member write that
// names no member.
func TestSettingsEscalationLooseningIsTheTypedForm(t *testing.T) {
	for _, tc := range []struct {
		key, old, new string
		seed          string // the file's own block, where it is not this one key
		widening      bool
	}{
		{"escalation.r2_after_hours", "6", "2", "", true},
		{"escalation.r2_after_hours", "6", "12", "", false},
		{"escalation.r2_after_attempts", "4", "1", "", true},
		{"escalation.r2_after_attempts", "4", "9", "", false},
		{"escalation.r3_after_hours", "18", "4", "", true},
		{"escalation.r3_after_hours", "18", "30", "", false},
		{"escalation.cost_ceiling", "40", "80", "", true},
		{"escalation.cost_ceiling", "40", "20", "", false},
		{"escalation.modes.pro.r2_after_hours", "9", "3", "", true},
		{"escalation.modes.pro.r2_after_hours", "9", "14", "", false},
		// First writes into a file that states no escalation block.
		{"escalation.r2_after_hours", "", "1", "", true},
		{"escalation.r2_after_hours", "", "30", "", false},
		{"escalation.r2_after_attempts", "", "1", "", true},
		{"escalation.r2_after_attempts", "", "9", "", false},
		{"escalation.r3_after_hours", "", "1", "", true},
		{"escalation.r3_after_hours", "", "30", "", false},
		// A mode's own member over the shipped row for that mode: POC's 3 h.
		{"escalation.modes.poc.r2_after_hours", "", "1", "", true},
		{"escalation.modes.poc.r2_after_hours", "", "6", "", false},
		// A member write over a block that states its scalar and no member.
		{"escalation.modes.poc.r2_after_hours", "", "2", "escalation:\n  r2_after_hours: 6\n", true},
		{"escalation.modes.poc.r2_after_hours", "", "10", "escalation:\n  r2_after_hours: 6\n", false},
		// The critical-path factor is the fourth key whose lower figure is the
		// wider one, and 0.5 is what a run scales by today.
		{"escalation.critical_path_factor", "0.5", "0.1", "", true},
		{"escalation.critical_path_factor", "0.5", "0.9", "", false},
		{"escalation.critical_path_factor", "", "0.1", "", true},
	} {
		t.Run(tc.key+"/"+tc.old+"/"+tc.new, func(t *testing.T) {
			switch {
			case tc.seed != "":
				settingsWriteHomeWith(t, tc.seed)
			case tc.old != "":
				settingsWriteHomeWith(t, "escalation:\n"+escalationFixtureLine(tc.key, tc.old))
			default:
				// The file carries no escalation block at all.
				settingsWriteHome(t)
			}
			noTerminal(t)
			doc := settingsDryRunDoc(t, "settings", "set", tc.key, tc.new, "--layer", "local")
			if doc.Widening != tc.widening {
				t.Errorf("%s: %s over %s is classified widening = %v, want %v (why: %q)", tc.key, tc.new, tc.old, doc.Widening, tc.widening, doc.Why)
			}
			if tc.widening && strings.TrimSpace(doc.Why) == "" {
				t.Errorf("%s: the widening carries no sentence saying what it widens", tc.key)
			}
			if !tc.widening && doc.Why != "" {
				t.Errorf("%s: the ordinary write carries a widening sentence: %q", tc.key, doc.Why)
			}
		})
	}

	// ready_drafts is the row's own direction rather than a figure: turning it
	// on hands the daemon authority over a draft a person left alone (D2041,
	// D2046), and turning it off is the ordinary write.
	settingsWriteHome(t)
	noTerminal(t)
	if doc := settingsDryRunDoc(t, "settings", "set", "escalation.ready_drafts", "true", "--layer", "local"); !doc.Widening || doc.Why == "" {
		t.Errorf("turning ready_drafts on is widening = %v with why %q, want the row's own sentence", doc.Widening, doc.Why)
	}
	settingsWriteHomeWith(t, "escalation:\n  ready_drafts: true\n")
	if doc := settingsDryRunDoc(t, "settings", "set", "escalation.ready_drafts", "false", "--layer", "local"); doc.Widening {
		t.Error("turning ready_drafts off is classified as a widening, and it takes the authority away")
	}
}

// escalationFixtureLine is one key of the ladder's block as a line under it,
// with the mode segment expanded for a per-mode key.
func escalationFixtureLine(key, value string) string {
	if mode, leaf, ok := strings.Cut(strings.TrimPrefix(key, "escalation.modes."), "."); ok && strings.HasPrefix(key, "escalation.modes.") {
		return "  modes:\n    " + mode + ":\n      " + leaf + ": " + value + "\n"
	}
	return "  " + strings.TrimPrefix(key, "escalation.") + ": " + value + "\n"
}

// TestSettingsWritesTheEscalationBlockAndItsModes: the whole block is the
// generic path's, the per-mode override table included — a mode's member is a
// map entry with a varying segment, which is the one spelling of the block the
// allowlist has to name to be reachable at all (D1161). What lands is read back
// through the loader, so the value is the one a run's table would carry.
func TestSettingsWritesTheEscalationBlockAndItsModes(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	for _, edit := range [][2]string{
		{"escalation.r2_after_hours", "6"},
		{"escalation.modes.pro.r2_after_hours", "8"},
		{"escalation.modes.pro.r2_coder", "strongest"},
		{"escalation.merge_judge", "strong"},
	} {
		out, _, err := execute(t, "settings", "set", edit[0], edit[1], "--layer", "local")
		if err != nil {
			t.Fatalf("%s was refused: %v\n%s", edit[0], err, out)
		}
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, localPath)))
	if err != nil {
		t.Fatalf("the config the writes produced does not load: %v", err)
	}
	if got := lc.EscalationThresholds().Rows["pro"].R2Hours; got != 8 {
		t.Errorf("pro's r2_after_hours reads %v, want the written 8", got)
	}
	if got := lc.EscalationThresholds().Rows["pro"].Grade; string(got) != "strongest" {
		t.Errorf("pro's r2_coder reads %q, want the written strongest", got)
	}
	if got := lc.EscalationThresholds().Rows["poc"].R2Hours; got != 6 {
		t.Errorf("poc's r2_after_hours reads %v, want the block's scalar 6", got)
	}
	if g, ok := lc.EscalationMergeJudge(); !ok || string(g) != "strong" {
		t.Errorf("merge_judge reads (%q, %v), want (strong, true)", g, ok)
	}
}

// TestSettingsEscalationIsNeverPublished: the block decides when every
// teammate's runs merge, so the publisher never writes it into the runs
// bucket's shared file (D2032, G3) — one list refuses the key at the door and
// the local loader drops the block on the way out.
func TestSettingsEscalationIsNeverPublished(t *testing.T) {
	if !slices.Contains(sharedForbidden, "escalation") {
		t.Error("sharedForbidden does not name escalation, so a published ladder would be read by every teammate's runs")
	}
}
