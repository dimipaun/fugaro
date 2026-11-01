package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDocsNameUpgrade: the docs give the one upgrade command, never the old
// four steps, and every flag they give it is real. /fugaro:upgrade (Task 9)
// was once banned here because it hadn't merged yet (upgrade task 10, PR
// #208); now that it has, README.md's Upgrading section must say the skill
// does the local half and hands the user the rest.
func TestDocsNameUpgrade(t *testing.T) {
	cmd := newUpgradeCmd()
	// flagRE anchors on a markdown code span opening right at "fugaro
	// upgrade" and consumes, one at a time, either a bracketed usage group
	// (`[--flag]`, `[--flag X]`, `[--a \| --b \| --c]`, each optionally
	// followed by `...`) or a bare `--flag`, stopping at the first token
	// that is neither (a closing backtick, a placeholder like PATH..., or
	// unrelated prose) so it never reaches past the command's own usage.
	flagRE := regexp.MustCompile(`\x60fugaro upgrade((?:\s+(?:\[[^\]]*\]\.*|--[a-z][a-z-]*))*)`)
	flagTokenRE := regexp.MustCompile(`--[a-z][a-z-]*`)
	for _, path := range []string{"../../README.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/design/v1.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro upgrade") {
			t.Errorf("%s never names fugaro upgrade", path)
		}
		for _, old := range []string{"/plugin install fugaro@fugaro", "/plugin marketplace add dimipaun/fugaro"} {
			if strings.Contains(doc, old) {
				t.Errorf("%s still lists %q", path, old)
			}
		}
		for _, m := range flagRE.FindAllStringSubmatch(doc, -1) {
			for _, f := range flagTokenRE.FindAllString(m[1], -1) {
				if name := strings.TrimLeft(f, "-"); cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: fugaro upgrade has no %s", path, f)
				}
			}
		}
	}
	for _, path := range []string{"../../README.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/release.md"} {
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "brew upgrade dimipaun/tap/fugaro && fugaro upgrade") {
			t.Errorf("%s does not give the upgrade sequence", path)
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "`/fugaro:upgrade` does the local half and hands you the rest") {
		t.Error("README.md's Upgrading section never says /fugaro:upgrade does the local half and hands the user the rest")
	}
}

// TestDocsDesignListsEveryUpgradeStep: the design doc's step list is what the
// next reader trusts, and the facts a later edit is most likely to soften are
// the ones this pins: the five steps in the order they run (the rules step
// before the policy step, because the policy write is gated on the deployed
// generation), the owner's ruling that replaced the open decision, and the
// two truths of the agent-session doors — a session moves no stale pin, and
// the update door there leaves the checkout for the user and exits 1.
func TestDocsDesignListsEveryUpgradeStep(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/upgrade.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"Each checkout runs five steps in order",
		"4. **rules**",
		"5. **policy**",
		"ONE shared ownership check covers every config-driven budget-database write path",
		"a stale plugin pin is **not** moved there",
		"`left_for_you`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/upgrade.md does not say %q", want)
		}
	}
	if strings.Contains(doc, "OPEN OWNER DECISION") {
		t.Error("docs/design/upgrade.md still carries the open decision the owner ruled on 2026-10-31")
	}
}

// TestDocsPolicyMigrationChecklistItem: the release checklist carries the item
// this change depends on — a policy version bump and its migrator ship in the
// same release — and the doc the item points at states the two things an
// operator has to be able to find: that `fugaro update` migrates the node in
// place, and when `--delete` is still the answer. The pin is the sentence, not
// the file: the checklist is read by whoever tags, and an item that drifts into
// "publish again" is the 0.14.0 failure again.
func TestDocsPolicyMigrationChecklistItem(t *testing.T) {
	release, err := os.ReadFile("../../docs/release.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"**Every policy version bump ships its migrator.**",
		"docs/policy-migration.md",
	} {
		if !strings.Contains(string(release), want) {
			t.Errorf("docs/release.md does not carry %q", want)
		}
	}
	doc, err := os.ReadFile("../../docs/policy-migration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"migrates the node **in place**",
		"## When `--delete` is still the answer",
		"### For operators",
		"Run `fugaro update`.",
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("docs/policy-migration.md does not say %q", want)
		}
	}
}
