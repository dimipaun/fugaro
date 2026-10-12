package cli

import (
	"os"
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
