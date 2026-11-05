package cli

// The stateless orchestrator design folder, pinned.
//
// docs/stateless-orchestrator/ is design-only: spec.md (the owner's, verbatim,
// never edited), README.md, reconciliation.md (F1-F23, the decisions
// D1220-D1243) and implementation-plan.md (O0-O19). Nothing in it pins a
// command or a flag. What rots silently is pinned here: the block the decisions
// live in, the rulings that are not the recommendation (D1235 and the retry
// rule), Q2 answered a second time, the three round-1 questions the owner has
// now answered, the one tension the answers left open (T7, the daemon-unit
// carve-out of the HARD RULE), the facts a rewrite would quietly drop (D1243's
// cache writer and O10's level, the 14-day manual rollback, the unit moved out
// of `fugaro update` and into `fugaro setup`), and the absence of any
// secret-shaped literal.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	soDir        = "docs/stateless-orchestrator/"
	soReconciled = soDir + "reconciliation.md"
	soPlan       = soDir + "implementation-plan.md"
	soReadme     = soDir + "README.md"
)

func soRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// soRow returns the decision table's row for id, or "" when it has none.
func soRow(rec, id string) string {
	for _, line := range strings.Split(rec, "\n") {
		if strings.HasPrefix(line, "| "+id+" |") {
			return line
		}
	}
	return ""
}

func TestDocsStatelessOrchestratorDecisionsAreWritten(t *testing.T) {
	rec := soRead(t, soReconciled)
	for n := 1220; n <= 1243; n++ {
		id := "D" + strconv.Itoa(n)
		if !regexp.MustCompile(`(?m)^\| `+id+` \|`).MatchString(rec) && !strings.Contains(rec, "- **"+id+".") && !strings.Contains(rec, id+" (") && !strings.Contains(rec, id+", D") && !strings.Contains(rec, id+" are") {
			t.Errorf("%s: %s is not written anywhere", soReconciled, id)
		}
	}
	for _, f := range []string{soReadme, soReconciled, soPlan} {
		if !strings.Contains(soRead(t, f), "D1244-D1249") {
			t.Errorf("%s: the reserved remainder D1244-D1249 is not stated", f)
		}
	}
}

func TestDocsStatelessOrchestratorD1235IsNotTheRecommendation(t *testing.T) {
	rec := soRead(t, soReconciled)
	for _, line := range strings.Split(rec, "\n") {
		if strings.HasPrefix(line, "| D1235 |") {
			if !strings.Contains(line, "NOT THE RECOMMENDATION") {
				t.Errorf("the D1235 answer cell lost 'NOT THE RECOMMENDATION'")
			}
			return
		}
	}
	t.Errorf("%s: no D1235 row", soReconciled)
}

// The three questions the round-1 security review left open were answered on
// 2026-11-04, so the folder must not still be asking them: each has a dated
// answer cell, the reconciliation's own words for "unanswered" are gone, and
// the tension the answers left behind is the one item still put to the owner.
func TestDocsStatelessOrchestratorOwnerQuestionsAreAnswered(t *testing.T) {
	rec := soRead(t, soReconciled)
	for _, id := range []string{"D1241", "D1242", "D1243"} {
		row := soRow(rec, id)
		if row == "" {
			t.Fatalf("%s: %s has no decision row", soReconciled, id)
		}
		if !strings.Contains(row, "Answered 2026-11-04") {
			t.Errorf("%s: the %s answer cell carries no date", soReconciled, id)
		}
	}
	for _, gone := range []string{
		"new owner questions raised by the round-1 security review, unanswered",
		"Round-1 owner questions (new, unanswered",
		"and the open D1241-D1243",
	} {
		if strings.Contains(rec, gone) {
			t.Errorf("%s: an answered question is still called open (%q)", soReconciled, gone)
		}
	}
	if !strings.Contains(rec, "**T6.") || !strings.Contains(rec, "CLOSED") {
		t.Errorf("%s: T6 is not stated as closed", soReconciled)
	}
	if !strings.Contains(rec, "**T7.") || !strings.Contains(rec, "Unanswered: the owner rules") {
		t.Errorf("%s: the one tension the answers left open (T7) is not stated as open", soReconciled)
	}
	for _, want := range []string{"mitigation, not a barrier", "ClaimKey()", "bypasses the rules"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the corrected security statement %q is gone", soReconciled, want)
		}
	}
}

// The owner's rulings that are not the recommendation have to be readable as
// deviations: D1235 (pinned above) and the retry rule. A deviation recorded as
// a recommendation is a record the owner cannot read.
func TestDocsStatelessOrchestratorDeviationsAreMarked(t *testing.T) {
	plan := soRead(t, soPlan)
	readme := soRead(t, soReadme)
	if !strings.Contains(plan, "Retry rule (2026-11-04, not the recommendation)") {
		t.Errorf("%s: the retry rule is not marked as not the recommendation", soPlan)
	}
	if !strings.Contains(readme, "two rulings are not") {
		t.Errorf("%s: the README no longer names the rulings that are not the recommendation", soReadme)
	}
	if !strings.Contains(readme, "T7") || !strings.Contains(plan, "T7") {
		t.Errorf("the one open tension (T7) is not named in the README and the plan")
	}
}

// Q2 was ruled a second time: the first answer (an offer inside `fugaro
// update`) is superseded, `fugaro setup` installs the unit, `fugaro
// orchestrator install` upgrades it, and `update` never touches it — which
// closed T6 and opened T7 for the HARD RULE carve-out it leaves behind. A
// later edit that puts the unit back into `update`, or that drops the
// self-upgrade that stands in for it, is the change this design cannot afford.
func TestDocsStatelessOrchestratorQ2IsRuledTwice(t *testing.T) {
	plan := soRead(t, soPlan)
	rec := soRead(t, soReconciled)
	readme := soRead(t, soReadme)
	for _, f := range []struct{ name, text string }{{soPlan, plan}, {soReconciled, rec}, {soReadme, readme}} {
		if !strings.Contains(f.text, "superseded") {
			t.Errorf("%s: the superseded first Q2 answer is not named as superseded", f.name)
		}
	}
	for _, want := range []string{
		"`fugaro setup` installs the daemon",
		"`fugaro update` never touches the unit",
		"TestDaemonRestartsWhenItsBinaryIsNewer",
		"TestMigrateRunsAtDaemonStart",
		"TestUnitPointsAtTheStableBinaryPath",
		"TestSetupInstallsTheUnitAsAPlanStep",
		"TestUpdateNeverTouchesTheUnit",
		"`internal/cli/update.go`",
		"init_orchestrator_stage.go",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: Q2's ruled follow-through %q is gone", soPlan, want)
		}
	}
	for _, gone := range []string{
		"TestUpdateRerendersAndRestartsAPreviousVersionUnit",
		"TestUpdateOffersTheInstallOnlyAtATerminal",
		"TestUpdateInstallsNothingUnderAnAgentMarker",
		"TestUpdateMigratesTheStateSchemaVersion",
		"`internal/cli/update_orchestrator.go`",
		"offers** to install the daemon",
	} {
		if strings.Contains(plan, gone) {
			t.Errorf("%s: the superseded first Q2 answer is still built (%q)", soPlan, gone)
		}
	}
}

// The retry rule is the newest thing the plan builds, so the rule and the
// tests that hold it are pinned by name: a later edit that drops the retry, or
// the refusals it must not retry, is the change this design cannot afford.
func TestDocsStatelessOrchestratorRetryRuleIsFollowedThrough(t *testing.T) {
	plan := soRead(t, soPlan)
	for _, want := range []string{
		"One approval covers the first attempt and at most one automatic retry",
		"TestOneAutomaticRetryUnderTheSameApproval",
		"TestRetryRefusedWhenTheSpecChanged",
		"TestRefusalIsNotRetried",
		"TestAFailedAttemptNeedsANewApproval",
		"TestApprovalCarriesTheAttemptsItCovers",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: the retry rule's %q is gone", soPlan, want)
		}
	}
	if strings.Contains(plan, "One approval covers one attempt") || strings.Contains(plan, "one approval is one attempt") {
		t.Errorf("%s: the superseded 'one approval is one attempt' rule is stated as current", soPlan)
	}
}

// D1243's answer moved work and a level: the daemon writes the session-start
// cache, O10 reads it, and the level table and the file table follow.
func TestDocsStatelessOrchestratorCacheAnswerIsFollowedThrough(t *testing.T) {
	plan := soRead(t, soPlan)
	for _, want := range []string{
		"session-start cache (D1243)",
		"TestSessionStartPrintMakesNoNetworkCall",
		"TestCacheIsWrittenAtomically",
		"| 4 | O9, O10 |",
		"O10 <- O5, O8",
		"state --brief --offline",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: D1243's follow-through %q is gone", soPlan, want)
		}
	}
}

// T1 was ruled: the scripts are kept 14 days and the rollback is manual, with
// no command built for it.
func TestDocsStatelessOrchestratorRollbackIsManual(t *testing.T) {
	for _, f := range []string{soReadme, soPlan} {
		doc := soRead(t, f)
		for _, want := range []string{"14 days", "manual"} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s: the ruled rollback lost %q", f, want)
			}
		}
	}
	if !strings.Contains(soRead(t, soPlan), "No rollback command is built") {
		t.Errorf("%s: the absence of a rollback command is no longer stated", soPlan)
	}
}

func TestDocsStatelessOrchestratorHasNoSecretShapedLiteral(t *testing.T) {
	re := regexp.MustCompile(`AKIA[0-9A-Z]{12,}|ghp_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY`)
	for _, f := range []string{soReadme, soReconciled, soPlan} {
		if m := re.FindString(soRead(t, f)); m != "" {
			t.Errorf("%s: secret-shaped literal %q", f, m)
		}
	}
}
