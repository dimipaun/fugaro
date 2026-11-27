package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is MG4 of docs/model-credentials-routing/implementation-plan.md: the
// account and craft commands stop writing access.
//
// A craft's access and a grade's access were how an installation said which
// credential a stage's calls go out on, and the cascade's fallback and threshold
// said what a run does when that credential is exhausted. The project's routing
// table says both now (D3170, D3182): one entry per model, published with the
// project's policy, and a subscription token is reached through a pool rather
// than named by a craft. So the five commands of this family write those keys no
// more: each writes what it still means — a switch the account-choice keys, an
// alias the block's one entry, a cascade its backoff and its two headroom
// controls — or refuses, and every refusal names the command that owns the write
// now: `fugaro routing set` for the table, `fugaro accounts pool` for a token
// that has to sit in a pool first.
//
// A repository's auth mode (`agent.auth`, `agent.oauth_token`) is a fugaro.yaml
// key, and no command of this family ever wrote one: the fence that keeps these
// commands out of a repository's file is the fence the whole settings path has,
// so what the test below holds them to is the machine's own config, key by key.

// accountsFixNamed fails unless the sentence names the command that owns the
// retired write now. It is the acceptance's second half: a refusal that names no
// way forward leaves a person with a key they cannot move.
func accountsFixNamed(t *testing.T, what, sentence string) {
	t.Helper()
	if !strings.Contains(sentence, "fugaro routing set") {
		t.Errorf("%s: the sentence does not name `fugaro routing set`, so it names no way forward:\n%s", what, sentence)
	}
}

// accountsUnchanged fails unless the file is byte for byte what it was: a
// refused write lands nothing.
func accountsUnchanged(t *testing.T, what, path, before string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != before {
		t.Errorf("%s: the file is not what it was:\n%s", what, got)
	}
}

// accountsRetiredCraftsBlock is the leftover a migrated installation holds: the
// craft access `fugaro update` derived the table's entry from, which no command
// of this family may write any more and which every command here has to leave
// exactly as it is.
const accountsRetiredCraftsBlock = `crafts:
  coder: { access: team-a }
`

// TestAccountsCommandsWriteNoAccess is MG4's acceptance, one sub-test per
// command: none of the five writes a craft's access, a grade's access or a
// cascade member the table owns, and every refusal names the command that owns
// the write now.
func TestAccountsCommandsWriteNoAccess(t *testing.T) {
	t.Run("switch", func(t *testing.T) {
		switchFixture(t, switchChoiceTail)
		path := os.Getenv("FUGARO_CONFIG")
		before := switchFile(t)

		// The dry run prints the plan and writes nothing: the plan is the whole of
		// what a switch is judged on here, and it is also the read that needs no
		// cloud.
		out, _, err := execute(t, "accounts", "switch", "team-a", "team-b", "--dry-run")
		if err != nil {
			t.Fatalf("accounts switch: %v\n%s", err, out)
		}
		// The account-choice keys are still what this command is for, and they all
		// move: a command that refused the whole switch would leave the owner's nine
		// hand edits with no command at all.
		for _, key := range []string{
			"default_claude_account",
			"stages.review.claude_account",
			"repos.acme/app.claude_accounts",
			"profiles.standard.claude_accounts",
		} {
			if !strings.Contains(out, key+": team-a -> team-b") {
				t.Errorf("the switch no longer moves %s:\n%s", key, out)
			}
		}
		// The craft's access is the one key of that shape the plan may not carry,
		// and the line about it is not a move.
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "crafts.") && strings.Contains(line, "->") {
				t.Errorf("the switch still rewrites a craft's access: %s", line)
			}
		}
		if !strings.Contains(out, "crafts.coder.access names team-a and stays where it is") {
			t.Errorf("the switch does not say what it leaves alone, so the craft keeps spending the account the person moved off:\n%s", out)
		}
		accountsFixNamed(t, "accounts switch", out)
		accountsUnchanged(t, "accounts switch --dry-run", path, before)
	})

	t.Run("alias", func(t *testing.T) {
		// The alias is the indirection the choice keys name, and this command writes
		// the block's one entry: no retired key is its business, and the leftover
		// craft's access beside it is left exactly as it was.
		r := newAddRigWith(t, aliasRigLocal+accountsRetiredCraftsBlock)
		node := addPolicyNode()
		node["default_claude_account"] = "team-b"
		node["claude_account_stages"] = map[string]any{"review": "team-b"}
		r.db.Set(projectpolicy.PathShared(), node)

		out, _, err := execute(t, "accounts", "alias", "set", "reviewer", "team-b", "--yes")
		if err != nil {
			t.Fatalf("accounts alias set: %v\n%s", err, out)
		}
		lc := r.addLocal(t)
		if got := lc.ClaudeAccountAliases["reviewer"]; got != "team-b" {
			t.Errorf("claude_account_aliases.reviewer = %q, want team-b: the alias is what this command writes", got)
		}
		if got := lc.Crafts.Of(config.CraftCoder).Access; got != "team-a" {
			t.Errorf("crafts.coder.access = %q, want team-a left alone: the alias write is not a craft's access", got)
		}
	})

	t.Run("craft", func(t *testing.T) {
		path := accountsCraftHome(t, "")
		before := readFileString(t, path)

		out, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-b")
		if err == nil {
			t.Fatalf("accounts craft set wrote a craft's access:\n%s", out)
		}
		accountsFixNamed(t, "accounts craft set", err.Error())
		accountsUnchanged(t, "accounts craft set", path, before)
	})

	t.Run("grade", func(t *testing.T) {
		// The grade's access lives in a repository's own fugaro.yaml, the layer its
		// registry row defaults to, and the refusal leaves that checkout untouched.
		modelsWriteHomeWith(t, "")
		path := modelsWriteGitCheckout(t, "owner/repo-a",
			settingsRepoYAML("model_grades:\n  strong:\n    model: claude-sonnet-5-5\n"))
		before := readFileString(t, path)

		out, _, err := execute(t, "accounts", "grade", "set", "strong", "--access", "vertex")
		if err == nil {
			t.Fatalf("accounts grade set wrote a grade's access:\n%s", out)
		}
		accountsFixNamed(t, "accounts grade set", err.Error())
		accountsUnchanged(t, "accounts grade set", path, before)
	})

	t.Run("cascade", func(t *testing.T) {
		path := accountsCascadeHome(t)
		before := readFileString(t, path)

		// The two members the chain replaced, refused whether or not the value is
		// one the old vocabulary knew: what a run does when its credential is
		// exhausted is the chain's own order, and the chain says it at once rather
		// than after a wait (R4 of the reconciliation).
		for _, tc := range []struct{ flag, value string }{
			{"--fallback", "oauth-only"},
			{"--wait-threshold", "30m"},
		} {
			out, _, err := execute(t, "accounts", "cascade", "set", tc.flag, tc.value)
			if err == nil {
				t.Fatalf("accounts cascade set %s wrote a member the table owns:\n%s", tc.flag, out)
			}
			accountsFixNamed(t, "accounts cascade set "+tc.flag, err.Error())
			accountsUnchanged(t, "accounts cascade set "+tc.flag, path, before)
		}
		// The members the cascade still owns are still written: a command that
		// refused everything would leave the backoff and the two headroom controls
		// of D142/D153 with no door at all.
		out, _, err := execute(t, "accounts", "cascade", "set", "--concurrency-per-token", "2")
		if err != nil {
			t.Fatalf("accounts cascade set --concurrency-per-token: %v\n%s", err, out)
		}
		if got := readFileString(t, path); !strings.Contains(got, "concurrency_per_token: 2") {
			t.Errorf("the member the cascade still owns was not written:\n%s", got)
		}
	})
}
