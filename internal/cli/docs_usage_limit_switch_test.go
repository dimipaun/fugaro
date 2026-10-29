package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func readSkillFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDocsUsageLimitSwitchTriggerIsScoped pins the guidance's most
// safety-critical claim: a run is only ever a candidate for an account
// switch when its evidence names a Claude usage limit, matched loosely
// (case-insensitively, wherever it appears) because the exact wording is
// Claude Code's own and unverified here (docs/design/named-oauth-tokens.md
// §7 item U4) — never inferred from a revoked token, a failing test, a
// review finding or a plain bug. A skill edit that drops the "usage limit"
// match, the case-insensitivity, or the exclusion of other failures fails
// this test.
func TestDocsUsageLimitSwitchTriggerIsScoped(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		`"usage limit"`,
		"case-insensitively",
		"Never treat any other failure as a usage limit",
		"revoked or expired token",
		"failing test",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
	skill := readSkillFile(t, "../../plugin/skills/working/SKILL.md")
	if !strings.Contains(skill, "never any other kind of failure") {
		t.Error("SKILL.md's usage-limit summary no longer scopes the trigger to a usage limit alone")
	}
}

// TestDocsUsageLimitSwitchKeepsATriedListAndNeverLoops pins the no-retry-loop
// rule: a tried list kept in the conversation, each allowed account tried at
// most once for a task, and a hard stop (never a retry loop) once every
// allowed account has been tried.
func TestDocsUsageLimitSwitchKeepsATriedListAndNeverLoops(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		"tried list for this task",
		"Never try one twice",
		"never loop back through the list",
		"every allowed account has hit its limit, and stop",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
}

// TestDocsUsageLimitSwitchRequiresExplicitToken pins the one rule that makes
// the switch actually work: a follow-up inherits its previous run's account
// by design, so the relaunch command must name --token explicitly, in both
// the follow-up and the fresh-launch (no PR yet) cases, or it would silently
// retry the very account that just hit its limit.
func TestDocsUsageLimitSwitchRequiresExplicitToken(t *testing.T) {
	followup := readSkillFile(t, "../../plugin/skills/working/reference/followup.md")
	if !strings.Contains(followup, "inherits the previous run's Claude account by design") {
		t.Error("followup.md no longer says a follow-up inherits its account by design")
	}
	if !strings.Contains(followup, "fugaro run --pr N --repo <owner/name> --token NAME --run-id <id> --json") {
		t.Error("followup.md's account-switch relaunch no longer names --token explicitly")
	}
	launch := readSkillFile(t, "../../plugin/skills/working/reference/launch.md")
	if !strings.Contains(launch, "fugaro run --repo <owner/name> --workflow <name> --ref <base-branch> --token NAME --run-id <id> --json") {
		t.Error("launch.md's no-PR-yet relaunch no longer names --token explicitly")
	}
}

// TestDocsNamedOAuthTokensAccountsCommandDesign pins
// docs/design/named-oauth-tokens.md §12's claims about fugaro accounts ls
// against the real command: its flags, that it is offline (no Secret
// Manager client is ever opened), and that the design no longer lists the
// command as an unbuilt bigger-version item.
func TestDocsNamedOAuthTokensAccountsCommandDesign(t *testing.T) {
	doc := readSkillFile(t, "../../docs/design/named-oauth-tokens.md")
	for _, want := range []string{
		"fugaro accounts ls [--repo OWNER/NAME] [--json]",
		"no Secret Manager call",
		"explicitly unverified",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/named-oauth-tokens.md no longer says %q", want)
		}
	}
	if strings.Contains(doc, "A `fugaro accounts` command to list configured names and which repositories may use each (today,") {
		t.Error(`docs/design/named-oauth-tokens.md still lists fugaro accounts as an unbuilt bigger-version item, but it is now built`)
	}
	ls := newAccountsLsCmd()
	if ls.Flags().Lookup("repo") == nil || ls.Flags().Lookup("json") == nil {
		t.Fatal("fugaro accounts ls has no --repo or --json flag, but the design doc claims both")
	}
}

// TestDocsUsageLimitSwitchNamesTheAccountsCommand ties the skill files'
// `fugaro accounts ls` mentions to the real command (not just prose the
// plugin lint's commandIndex happens to resolve): the command exists, is
// named ls under accounts, and takes --repo and --json.
func TestDocsUsageLimitSwitchNamesTheAccountsCommand(t *testing.T) {
	var hasLs bool
	for _, c := range newAccountsCmd().Commands() {
		hasLs = hasLs || c.Name() == "ls"
	}
	if !hasLs {
		t.Fatal("fugaro accounts has no ls subcommand")
	}
	ls := newAccountsLsCmd()
	if ls.Flags().Lookup("repo") == nil || ls.Flags().Lookup("json") == nil {
		t.Fatal("fugaro accounts ls has no --repo or --json flag")
	}
	// Every file that tells the agent to switch accounts names the real
	// command directly; launch.md's no-PR-yet case defers to
	// reference/diagnose.md for it instead (TestDocsUsageLimitSwitchRequiresExplicitToken
	// pins launch.md's own command, the relaunch itself).
	for _, path := range []string{
		"../../plugin/skills/working/SKILL.md",
		"../../plugin/skills/working/reference/diagnose.md",
		"../../plugin/skills/working/reference/followup.md",
	} {
		doc := readSkillFile(t, path)
		if !strings.Contains(doc, "fugaro accounts ls") {
			t.Errorf("%s never names fugaro accounts ls", path)
		}
	}
}

// TestDocsUsageLimitSwitchSeedsTriedListFromOAuthAccount pins task 9b round
// 2 review comment #2: the skill must identify the account that actually
// failed from row.oauth_account, not assume it, and seed the tried list
// with it; "" means the default account (runview.Join falls back to the
// launching task's own account when a run died before recording one,
// round 3 comment #3, so this no longer needs its own "or predates the
// field" caveat), and a checkout's agent.oauth_token is the same fallback.
func TestDocsUsageLimitSwitchSeedsTriedListFromOAuthAccount(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		"row.oauth_account",
		"falling back to the launching task's own account when a run died before ever recording one",
		"agent.oauth_token",
		"seed a tried list for this task with it",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
	if !hasJSONField(reflect.TypeOf(Diagnosis{}), "row.oauth_account") {
		t.Fatal("Diagnosis has no row.oauth_account field any more, but the skill tells the agent to read it")
	}
}

// TestDocsUsageLimitSwitchExcludesCompletedWorkAndMentions pins review
// comment #3: the trigger is Claude Code's own error (row.reason, log_tail,
// or an agent_message that reads as an error), never a summary of
// completed work, and a task that merely discusses usage limits in its
// summary (without ever hitting one) must not look like a match.
func TestDocsUsageLimitSwitchExcludesCompletedWorkAndMentions(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		"Claude Code's own error",
		"not a summary of work it did",
		"log_tail",
		"that reads as an error",
		"otherwise finished its task normally never counts",
		"was *about* usage limits",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
}

// TestDocsUsageLimitSwitchSaysAMissIsSafe pins review comment #4: the real
// Claude Code wording may not contain "usage limit" at all, and a miss is
// the safe direction — no switch, tell the user — never a forced match.
func TestDocsUsageLimitSwitchSaysAMissIsSafe(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		`may not contain the words "usage limit" at all`,
		"limit reached",
		"A miss is the safe direction",
		"has not been verified against a live run",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
}

// TestDocsUsageLimitSwitchOnlyForOAuthWorkflows pins review comment #6: the
// switch only ever applies to a workflow whose agent.auth is oauth, the
// same rule claudeAccountAllowed enforces at launch.
func TestDocsUsageLimitSwitchOnlyForOAuthWorkflows(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	if !strings.Contains(diagnose, "applies only to a workflow whose `agent.auth` is `oauth`") {
		t.Error("reference/diagnose.md no longer scopes the usage-limit switch to agent.auth: oauth workflows")
	}
}

// TestDocsAccountsLsCompleteFieldIsReal pins review comment #5: fugaro
// accounts ls --json's "complete" and "note" fields (added so a --json
// caller can tell a genuine "no other accounts" from "selection failed,
// this list may be short") are real fields of accountsListDoc, and the
// skill step that reads them (reference/diagnose.md step 1) says so.
// fieldTokenRE (skills_test.go) only catches underscore- or dot-shaped
// backtick tokens, so a bare word like `complete` or `note` would silently
// pass TestSkillJSONClaimsMatchTheTable even if renamed — this test closes
// that gap directly against the struct, the same way
// TestProjectLayerSkillClaimsRealFields does for config layer --json.
func TestDocsAccountsLsCompleteFieldIsReal(t *testing.T) {
	for _, f := range []string{"repo", "accounts", "complete", "note"} {
		if !hasJSONField(reflect.TypeOf(accountsListDoc{}), f) {
			t.Errorf("accountsListDoc has no %q field any more, but reference/diagnose.md tells the agent to read it from fugaro accounts ls --json", f)
		}
	}
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	for _, want := range []string{
		"Check `complete` first",
		"`note` says which",
	} {
		if !strings.Contains(diagnose, want) {
			t.Errorf("reference/diagnose.md no longer says %q", want)
		}
	}
}

// TestDocsSkillSummaryDoesNotRestateTheOldLooseTrigger pins review finding
// (task 9b round 3): SKILL.md's top-level "usage limit" paragraph used to
// restate the trigger itself ("usage limit" in row.reason or
// agent_message), which went stale and contradicted
// reference/diagnose.md's own section once that section was narrowed to
// Claude Code's own error text, excluding log_tail and finished work
// (task 9b round 2). SKILL.md must point to diagnose.md's section instead
// of restating the rule, so the two can never drift apart silently again.
func TestDocsSkillSummaryDoesNotRestateTheOldLooseTrigger(t *testing.T) {
	skill := readSkillFile(t, "../../plugin/skills/working/SKILL.md")
	if !strings.Contains(skill, "A usage limit on the Claude account") {
		t.Fatal("SKILL.md no longer has its usage-limit summary paragraph")
	}
	for _, stale := range []string{
		`"usage limit" (case-insensitively, in`,
		"row.reason` or `agent_message`) ran out",
	} {
		if strings.Contains(skill, stale) {
			t.Errorf("SKILL.md restates the old loose trigger (%q); it must point to reference/diagnose.md's section instead", stale)
		}
	}
	if !strings.Contains(skill, "reference/diagnose.md") || !strings.Contains(skill, "usage-limit section") {
		t.Error("SKILL.md's usage-limit paragraph no longer points to reference/diagnose.md's usage-limit section")
	}
}

// TestDocsUsageLimitSwitchDoesNotApplyToACascadeRun is Task C1.22's change to
// §5. The switch is a *personal* Claude subscription's: the section already
// scopes itself to `agent.auth: oauth`, and a `cascade` workflow is a second
// auth that can run out of Claude headroom without being one — its tier A is a
// subscription token the credentials service holds and chooses, not an account
// this machine's local config admits, so `fugaro accounts ls` cannot answer
// what it may use and `--token NAME` cannot name what the service will pick.
// The paragraph is what stops the section's advice being followed for a run it
// does not fit: a cascade run that ran out of tier A is the *service's*
// business (it marks the credential and switches to the next tier), and the
// skill sends the user to the runbook rather than to the account switch.
func TestDocsUsageLimitSwitchDoesNotApplyToACascadeRun(t *testing.T) {
	diagnose := readSkillFile(t, "../../plugin/skills/working/reference/diagnose.md")
	section := docSection(t, "plugin/skills/working/reference/diagnose.md", diagnose, "## 5. A Claude usage limit")
	flat := flattenDoc(section)
	for _, want := range []string{
		"`agent.auth` is `cascade`",
		"the credentials service",
		"docs/credentials-service/runbook.md",
		"`fugaro accounts ls` cannot answer",
		"`--token NAME` does not name",
		"is not a run this section switches",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("diagnose.md §5 never says %q: a cascade run that ran out of tier A is the service's to answer, and the section's switch does not fit it", want)
		}
	}
}
