package cli

// The stateless orchestrator design folder, pinned.
//
// docs/stateless-orchestrator/ is design-only: spec.md (the owner's, verbatim,
// never edited), README.md, reconciliation.md (F1-F23, the decisions
// D1220-D1247) and implementation-plan.md (O0-O19). Nothing in it pins a
// command or a flag — BF2's plan names `fugaro runs watch`, which no task here
// has built. What rots silently is pinned here: the block the decisions
// live in, the rulings that are not the recommendation (D1235, the retry rule
// and T7), Q2 answered a second time, the three round-1 questions the owner has
// now answered, the last tension the answers left open — T7, the daemon-unit
// carve-out of the HARD RULE, ruled on 2026-11-05 and refused in favour of
// `fugaro update` re-rendering an installed unit — BF2's long-run triage rule
// (D1244-D1247, O14, answered 2026-11-08: the threshold, the three inputs, the
// three outcomes, the fail-closed reading and the read-only print), the facts a
// rewrite would quietly drop (D1243's cache writer and O10's level, the 14-day
// manual rollback, the unit's first install in `fugaro setup` and its re-render
// in `fugaro update`, the approval an intent spends and the refusal that burns
// no attempt, the binary the unit names) and the absence of any secret-shaped
// literal.

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
	for n := 1220; n <= 1247; n++ {
		id := "D" + strconv.Itoa(n)
		if !regexp.MustCompile(`(?m)^\| `+id+` \|`).MatchString(rec) && !strings.Contains(rec, "- **"+id+".") && !strings.Contains(rec, id+" (") && !strings.Contains(rec, id+", D") && !strings.Contains(rec, id+" are") {
			t.Errorf("%s: %s is not written anywhere", soReconciled, id)
		}
	}
	for _, f := range []string{soReadme, soReconciled, soPlan} {
		if !strings.Contains(soRead(t, f), "D1248-D1249") {
			t.Errorf("%s: the reserved remainder D1248-D1249 is not stated", f)
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
// answer cell, and the reconciliation's own words for "unanswered" are gone —
// from T7 too, which the owner ruled on 2026-11-05.
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
	if strings.Contains(rec, "Unanswered: the owner rules") {
		t.Errorf("%s: T7 is still called unanswered after the owner's ruling of 2026-11-05", soReconciled)
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
	if !strings.Contains(readme, "three rulings are not") {
		t.Errorf("%s: the README no longer names the rulings that are not the recommendation", soReadme)
	}
	if !strings.Contains(readme, "T7") || !strings.Contains(plan, "T7") {
		t.Errorf("the tension T7 is not named in the README and the plan")
	}
}

// Q2 was ruled a second time: the first answer (an offer inside `fugaro
// update`) is superseded, `fugaro setup` installs the unit, `fugaro
// orchestrator install` is the explicit command, and `update` never installs
// one — which closed T6 and left T7, since ruled (T7IsRuled below). A later
// edit that puts the unit's *install* back into `update`, or that drops the
// self-upgrade, is the change this design cannot afford.
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
		"TestDaemonRestartsWhenTheUnitBinaryDiffers",
		"TestMigrateRunsAtDaemonStart",
		"TestUnitPointsAtTheStableBinaryPath",
		"TestSetupInstallsTheUnitAsAPlanStep",
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

// T7 is the newest owner ruling and it is not the recommendation: it amends
// Q2 — `fugaro update` re-renders an installed unit's template rather than
// leaving the person a second command — so the HARD RULE is literally true.
// What a later edit must not drop: the ruling, its date, the boundary that
// `update` never installs a unit, the step that carries it, and the tests that
// hold it. A folder that still says T7 is open, or that leaves the unit's
// template out of `update` again, is the change this design cannot afford.
func TestDocsStatelessOrchestratorT7IsRuled(t *testing.T) {
	plan := soRead(t, soPlan)
	rec := soRead(t, soReconciled)
	readme := soRead(t, soReadme)
	for _, f := range []struct{ name, text string }{{soPlan, plan}, {soReconciled, rec}, {soReadme, readme}} {
		if !strings.Contains(f.text, "2026-11-05") {
			t.Errorf("%s: T7's ruling carries no date", f.name)
		}
		if !strings.Contains(f.text, "not the recommendation") {
			t.Errorf("%s: T7's ruling is not marked as not the recommendation", f.name)
		}
	}
	for _, want := range []string{
		"re-renders the daemon's unit",
		"never installs one",
		"update_unit_stage.go",
		"TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged",
		"TestUpdateNeverInstallsAUnit",
		"`fugaro orchestrator install` stays",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: T7's ruled follow-through %q is gone", soPlan, want)
		}
	}
	if !strings.Contains(rec, "ANSWERED 2026-11-05") {
		t.Errorf("%s: T7 is not marked answered with its date", soReconciled)
	}
	for _, gone := range []string{
		"Unanswered: the owner rules",
		"the owner is asked to rule",
		"until T7 is",
		"the carve-out that\n  leaves is T7",
		"TestUpdateNeverTouchesTheUnit",
		"no release changes the daemon unit's template",
	} {
		for _, f := range []struct{ name, text string }{{soPlan, plan}, {soReconciled, rec}, {soReadme, readme}} {
			if strings.Contains(f.text, gone) {
				t.Errorf("%s: the superseded T7 wording is still there (%q)", f.name, gone)
			}
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

// The retry allowance is tied to the approval that spends it, a refusal only
// burns no attempt when the launcher refuses before the start, the daemon
// checks the binary the unit names, and the rules refuse every token writer:
// each is a hole a rewrite would silently reopen (a successful run leaves a
// standing approval, a crashed retry is abandoned, a Linuxbrew upgrade never
// restarts the daemon, a shape-validating token writer gets in).
func TestDocsStatelessOrchestratorApprovalIsBoundToItsIntents(t *testing.T) {
	plan := soRead(t, soPlan)
	rec := soRead(t, soReconciled)
	for _, want := range []string{
		"ApprovalAt", "SpentApprovalAt",
		"TestRetryCountsOnlyIntentsOfTheStandingApproval",
		"TestIntentCarriesTheApprovalItSpends",
		"TestRetentionKeepsTheIntentsOfAStandingApproval",
		"TestRecoveryBeforeTaskJSONOfASpentRetryStartsOnce",
		"TestAPreStartRefusalReissuesTheSameIntent",
		"TestAStartedRunsRefusalCounts",
		"TestOrchestratorRefusesEveryTokenWriter",
		"TestUnitPinsKeepAliveForACleanExit",
		"TestSetupInstallsInAManagedProjectWithoutAPrompt",
		"TestDoctorReportsAnOutOfDateUnitTemplate",
		"run --binary",
		"that changes the daemon unit's template is carried by `fugaro update` like any other upgrade",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: the approval-and-refusal follow-through %q is gone", soPlan, want)
		}
	}
	if !strings.Contains(plan, "`os.Executable()`") {
		t.Errorf("%s: the reason the check reads the unit's path and not os.Executable() is gone", soPlan)
	}
	for _, want := range []string{"refuse a job identity *and* a token identity", "`os.Executable()`"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the corrected token-writer and binary-path statement %q is gone", soReconciled, want)
		}
	}
	if strings.Contains(plan, "TestDaemonRestartsWhenItsBinaryIsNewer") || strings.Contains(rec, "binary on disk is newer") {
		t.Errorf("the superseded 'newer' rule is stated as current")
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

// BF2 (2026-11-08) is the newest addition to the folder and it is plan only:
// the long-run triage rule (D1244-D1247, task O14) has to stay readable as the
// code rule it is — a local threshold under the run's budget, three reads (the
// run record, the pull request's state and BF1's open-findings file), four
// outcomes with the merge automatic only on positive evidence, the fail-closed
// reading of everything it cannot read, the audit id and a read-only print. A
// rewrite that turns the rule into a model call, or that lets it merge on a
// finding set it could not read, is the change this design cannot afford.
// The merge is also fenced to a run that is **over** and a pull request that is
// **ready**, and the four outcomes are decided by a fixed order, so a run still
// in flight is never merged and a finished, stalled one is not restarted by
// accident; the local-only key is pinned to the six files that make it local
// (the shared block's field, its registry row, its schema entry and the
// project-path refusal, with their tests), and the built-task status names O2,
// O4 and O6 beside O0, O1 and O3.
func TestDocsStatelessOrchestratorLongRunTriageIsFollowedThrough(t *testing.T) {
	plan := soRead(t, soPlan)
	rec := soRead(t, soReconciled)
	readme := soRead(t, soReadme)
	for _, f := range []struct{ name, text string }{{soPlan, plan}, {soReconciled, rec}, {soReadme, readme}} {
		if !strings.Contains(f.text, "2026-11-08") {
			t.Errorf("%s: BF2's answers carry no date", f.name)
		}
	}
	for _, id := range []string{"D1244", "D1245", "D1246", "D1247"} {
		row := soRow(rec, id)
		if row == "" {
			t.Fatalf("%s: %s has no decision row", soReconciled, id)
		}
		for _, want := range []string{"Answered 2026-11-08", "the recommendation"} {
			if !strings.Contains(row, want) {
				t.Errorf("%s: the %s row lost %q", soReconciled, id, want)
			}
		}
	}
	for _, want := range []string{
		"### O14. The long-run triage rule and `fugaro runs watch` (BF2, slice 1)",
		"**Level 7.**",
		"| G12 |",
		"orchestrator.triage_after",
		"default `2h`",
		"BF1's open-findings file",
		"no model call",
		"merge as-is plus a follow-up task",
		"none high in a security path",
		"fails closed",
		"`<run>:triage`",
		"fugaro runs watch",
		"no flag that acts",
		"waiting_on: person",
		"TestTriageNeverCallsAModel",
		"TestTriageMergesOnlyWithGreenCIAndANonCriticalFindingSet",
		"TestTriageReadsCIThroughTheProvider",
		"TestTriageFailsClosedWithoutACIAnswer",
		"TestTriageNeverMergesWhileCIIsPending",
		"TestGitHubCIStateFromChecks",
		"TestBitbucketCIStateFromPipeline",
		"TestFakeCIStateIsSettable",
		"`gitprov.Provider.CI(ctx, number) (CIState, error)`",
		"CIUnknown",
		"TestTriageFailsClosedWithoutAFindingsFile",
		"TestTriageFailsClosedOnAnUnknownSeverity",
		"TestTriageRecommendsOnlyWhenAnInputCannotBeRead",
		"TestTriageNeverCancelsOnAnUnreadableFindingsFile",
		"TestTriageBlocksTheMergeOnASensitiveChangedPath",
		"TestTriageNeverMergesATruncatedPathSet",
		"TestTriageReadsTheFindingsFileFromTheRunsArtifactNotTheCheckout",
		"TestTriageRefusesAnUnknownSeverityWithoutCancelling",
		"`sensitivity.Classify(record.ChangedFiles.Paths) == sensitivity.Routine`",
		"`RecommendOnly`",
		"TestTriageUsesThePathClassifierForSecurity",
		"TestTriageCancelsThroughTheCancelPath",
		"TestTriageIsWrittenToTheAuditLog",
		"TestRunsWatchPrintsTheTriageWithoutWriting",
		"TestRunsWatchHasNoFlagThatActs",
		"TestTriageAfterIsLocalOnly",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: BF2's follow-through %q is gone", soPlan, want)
		}
	}
	for _, want := range []string{"U10", "split into sub-tasks", "the owner's ruling"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: BF2's %q is gone", soReconciled, want)
		}
	}
	// An input nobody could read is the recommend-only outcome, never a cancel,
	// and the merge is gated on the whole diff, not only on the finding set.
	for _, want := range []string{
		"the **recommend-only** outcome",
		"an input nobody read is not evidence that the run is bad",
		"**the whole changed-path set is `Routine`**",
		// The order is what makes that a guarantee and not a promise: the
		// fail-closed outcome is tried before the two that act, so a run whose
		// record would otherwise be restarted — cancelled — is only recommended
		// on while any input is unreadable.
		"escalate, then recommend-only, then merge-as-is, then restart",
		"Chosen when **every input was read**",
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the triage's fail-closed reading lost %q", soReconciled, want)
		}
	}
	// The merge is only for a run that is over and a pull request a person could
	// read, and the four outcomes are decided by a fixed order rather than by the
	// order a tick happens to check: without both, a run still in flight (or a
	// finished, stalled one) can be merged or restarted by accident.
	for _, want := range []string{
		"the record's status is **terminal**",
		"`gitprov.PRInfo.Draft` false",
		"the first that matches is the one taken",
		"a signal that counts only while the run is still in flight",
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the triage's merge gate lost %q", soReconciled, want)
		}
	}
	for _, want := range []string{
		"**The four are tried in one fixed order and the first that matches wins",
		"`Escalate`, then `RecommendOnly`, then `MergeAsIs`, then",
		"escalate, then recommend-only, then merge-as-is, then restart",
		"tried before `MergeAsIs` and",
		"**`MergeAsIs` also requires the run to be over and its pull request ready**",
		"TestTriageNeverMergesAnInFlightRun",
		"TestTriageNeverMergesADraftPullRequest",
		"TestTriagePrefersEscalateOverEveryOtherOutcome",
		"TestTriageMergesAFinishedStalledRunRatherThanRestartingIt",
		// The key is local-only because four named files make it so, not because
		// the plan says so: the field on the shared block, its registry row, its
		// settings-schema entry and the by-name refusal on the project path.
		"TestProjectPolicyRefusesTheLocalOnlyTriageKey",
		"`locLocal`",
		"`internal/settings/registry.go`",
		"`schemas/settings.schema.json`",
		"$defs.keyDefaults",
		"`checkOrchestratorMembers` refuses `triage_after` **by name**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s: BF2's follow-through %q is gone", soPlan, want)
		}
	}
	// The status is what rots first: six tasks are built on main — O0, O1, O2, O3,
	// O4 and O6 — so each document states its own count-bearing sentence, and all
	// six commits are named in all three. A bare "five" would pin nothing: the
	// plan says "five" of its own critical path two hundred lines further down.
	for _, f := range []struct{ name, text, count string }{
		{soPlan, plan, "**Status (2026-11-08): six tasks of this plan are built, all merged on `main`.**"},
		{soReconciled, rec, "**Status (2026-11-08): six of the plan's tasks are built, all merged on `main`.**"},
		{soReadme, readme, "**Status (2026-11-08): six tasks of the [implementation plan](implementation-plan.md) are built, all merged on `main`.**"},
	} {
		if !strings.Contains(f.text, f.count) {
			t.Errorf("%s: the built-task status lost %q", f.name, f.count)
		}
		for _, want := range []string{"36abeffc", "405dae8b", "5053cbce", "70ec7800", "8e534f5d", "6145e445", "#1825"} {
			if !strings.Contains(f.text, want) {
				t.Errorf("%s: the built-task status lost %q", f.name, want)
			}
		}
	}
	// P5's open item is where the first draft's reading of a missing findings file
	// survived: an absent or unattributable file is an unreadable input — the
	// recommend-only outcome — and not a critical finding set. With the fixed order
	// trying Escalate first, the draft's reading would cancel every long run while
	// BF1 is missing, which D1247 and TestTriageNeverCancelsOnAnUnreadableFindingsFile
	// both forbid.
	for _, f := range []struct{ name, text string }{{soPlan, plan}, {soReconciled, rec}} {
		for _, bad := range []string{"as a critical finding set", "absent file as a critical"} {
			if strings.Contains(f.text, bad) {
				t.Errorf("%s: an absent findings file reads as critical again (%q)", f.name, bad)
			}
		}
	}
	if !strings.Contains(plan, "reading an absent or unattributable file as an **unreadable input**") {
		t.Errorf("%s: P5 no longer reads an absent findings file as an unreadable input", soPlan)
	}
	// The CI read is new code, not a read the tree already makes: D1244 must keep
	// saying so, or the merge's one input reads as free when O14 has to write it.
	for _, want := range []string{"and this CI read does not exist yet", "so **O14 adds it**"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the triage's CI read no longer reads as new code (%q)", soReconciled, want)
		}
	}
	for _, want := range []string{"BF2, 2026-11-08", "D1244-D1247", "merge as-is with follow-ups"} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s: BF2's %q is gone", soReadme, want)
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
