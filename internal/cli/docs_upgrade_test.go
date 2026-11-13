package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDocsNameUpgrade: the docs give the one upgrade command, never the old
// four steps, and every flag they give it is real.
//
// The one upgrade command is `fugaro update` now: U4 of
// docs/plugin-user-scope/implementation-plan.md retired `fugaro upgrade` and
// folded what it had into this door, so a document that told the reader to run
// the old name would be sending them to cobra's "unknown command". Three of
// the four documents this reads are another run's — README.md, docs/release.md
// and docs/design/v1.md are not this plan's paths — and
// .claude/skills/new-release/SKILL.md is the living-documents task's (U7),
// which rewrote it: so what is held here is what is true of the command this
// task owns — they name it, the flags they give it are flags it has, and the
// plugin spellings the marketplace replaced are absent.
func TestDocsNameUpgrade(t *testing.T) {
	cmd := newUpdateCmd()
	// flagRE anchors on a markdown code span opening right at "fugaro
	// update" and consumes, one at a time, either a bracketed usage group
	// (`[--flag]`, `[--flag X]`, `[--a \| --b \| --c]`, each optionally
	// followed by `...`) or a bare `--flag`, stopping at the first token
	// that is neither (a closing backtick, a placeholder like PATH..., or
	// unrelated prose) so it never reaches past the command's own usage.
	flagRE := regexp.MustCompile(`\x60fugaro update((?:\s+(?:\[[^\]]*\]\.*|--[a-z][a-z-]*))*)`)
	flagTokenRE := regexp.MustCompile(`--[a-z][a-z-]*`)
	for _, path := range []string{"../../README.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/design/v1.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro update") {
			t.Errorf("%s never names fugaro update, the one upgrade command", path)
		}
		for _, old := range []string{"/plugin install fugaro@fugaro", "/plugin marketplace add dimipaun/fugaro"} {
			if strings.Contains(doc, old) {
				t.Errorf("%s still lists %q", path, old)
			}
		}
		for _, m := range flagRE.FindAllStringSubmatch(doc, -1) {
			for _, f := range flagTokenRE.FindAllString(m[1], -1) {
				if name := strings.TrimLeft(f, "-"); cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: fugaro update has no %s", path, f)
				}
			}
		}
	}
	// The release sequence, which is `brew` for the CLI and the converge for
	// the checkout. Its tail is the half this work renamed: U7 rewrote it to
	// `fugaro update` in .claude/skills/new-release/SKILL.md, and README.md and
	// docs/release.md are another run's, so the sentence is held to the
	// sequence under either spelling until that run lands its own half.
	sequenceRE := regexp.MustCompile("brew upgrade dimipaun/tap/fugaro && fugaro (?:upgrade|update)")
	for _, path := range []string{"../../README.md", "../../.claude/skills/new-release/SKILL.md", "../../docs/release.md"} {
		data, _ := os.ReadFile(path)
		if !sequenceRE.Match(data) {
			t.Errorf("%s does not give the upgrade sequence (brew upgrade dimipaun/tap/fugaro && fugaro update)", path)
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	// README's getting-started note names the skill that does the local half.
	// The skill's own name changes with U6 (`upgrade` becomes `update`), so the
	// line is accepted under either.
	if !regexp.MustCompile("`/fugaro:(?:upgrade|update)` does the local half").Match(readme) {
		t.Error("README.md's Upgrading section never says /fugaro:upgrade does the local half and hands the user the rest")
	}
}

// TestDocsDesignListsEveryUpgradeStep: the design doc's step list is what the
// next reader trusts, and the facts a later edit is most likely to soften are
// the ones this pins: the one command the document is about (its title named
// the retired one until U7 rewrote it), the four steps in the order they run
// (the rules step before the policy step, because the policy write is gated on
// the deployed generation), the deletion of the pin step the list used to open
// with, the owner's ruling that replaced the open decision, and the three
// truths the plugin-user-scope work leaves in a session — a session moves no
// stale pin, the plugin stage is one of the steps that *do* run there, a
// machine-local stage runs although a cloud stage stopped, and the run still
// leaves the checkout for the user and exits 1.
func TestDocsDesignListsEveryUpgradeStep(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/upgrade.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"# `fugaro update` and `/fugaro:update`",
		"`fugaro update` is the one command that does all of it",
		"Each checkout runs four steps in order",
		"nothing merges the plugin's marketplace and enablement into `.claude/settings.json` any more",
		"1. **plugin**",
		"2. **cloud**",
		"3. **rules**",
		"4. **policy**",
		"ONE shared ownership check covers every config-driven budget-database write path",
		"a stale plugin pin is **not** moved there",
		"`left_for_you`",
		// The session's own half of the design (U1, U8): the plugin stage is
		// local under the agent marker and applies, the machine-local marker
		// runs a stage although a cloud stage stopped, and the migration writes
		// nothing there.
		"plugin stage **is** one of them since U1",
		"machine-local: ran although images waits for you",
		"In a coding agent's session the diff is printed and nothing is written",
		// `--check` survived the retired command and `--local` did not (D948).
		"**`--local` is gone**",
		// The usage block is this door's, not the retired command's under a new
		// name: `--repo` is a switch and PATH is its argument, `--check` takes
		// PATH too, and there is no `--workflow` (init.go's flag set, and its
		// own Use is "update [--repo [PATH]]").
		"fugaro update [--check [PATH]] [--yes]",
		"[--repo [PATH]] [--image-source S] [--expect-digest KIND=sha256:H]...",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/upgrade.md does not say %q", want)
		}
	}
	if strings.Contains(doc, "OPEN OWNER DECISION") {
		t.Error("docs/design/upgrade.md still carries the open decision the owner ruled on 2026-10-31")
	}
	// The pin step's deletion (D949, U2) is the edit that renumbered this list,
	// and the doc is where an older reader looks for the command's shape: the
	// step, its flag, its count and the retired command's own usage line stay
	// gone together.
	for _, gone := range []string{"--allow-fork", "five steps", "runs pin and plugin", "fugaro upgrade [PATH", "[--repo R]", "[--workflow W]"} {
		if strings.Contains(doc, gone) {
			t.Errorf("docs/design/upgrade.md still says %q", gone)
		}
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
