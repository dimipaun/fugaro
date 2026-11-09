package cli

// The stage accounts as a registered setting (the incident of 2026-11-09: a
// review account ran out, and repointing the stage at another one meant editing
// the local config by hand).
//
// These tests are the whole of what the owner was promised: `settings set
// stages.<stage>.claude_account <name>` writes the key, `settings get` and the
// bare read answer it under the spelling the person wrote, `unset` takes it
// away, `--dry-run` reports the edit and writes nothing, and a name the project
// does not declare — or one whose allow_data_to covers fewer repositories than
// the project onboards — is refused with nothing attempted. The registry's own
// census is internal/settings' TestRegistryCarriesEveryRunStagesAccount and
// TestSettingsWritesEveryRunStageAccountKey below.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// settingsStageAccounts is the local config the tests below write into, with
// the two repositories the project onboards: one account covers both of them,
// and one covers only acme/app.
const settingsStageAccounts = "\nclaude_accounts:\n" +
	"  acme-narrow: { allow_data_to: [acme/app] }\n" +
	"  acme-wide: { allow_data_to: [acme/app, acme/other] }\n" +
	"repos:\n  acme/app: { provider: github }\n  acme/other: { provider: github }\n"

// settingsStageHome is a project config carrying those accounts, with no
// coding-agent marker in the environment.
func settingsStageHome(t *testing.T) (home, localPath string) {
	t.Helper()
	return settingsWriteHomeWith(t, settingsStageAccounts)
}

// TestSettingsSetWritesAStageAccount: the write lands at --layer local, the file
// carries the key, and the read answers the concrete spelling of the registry's
// stage-account row — with the layer that decided it — rather than the write
// path's unknown-key refusal.
func TestSettingsSetWritesAStageAccount(t *testing.T) {
	_, localPath := settingsStageHome(t)
	if _, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-wide", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("settings set stages.review.claude_account: %v", err)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, localPath)))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if got := lc.ClaudeAccountStages["review"].ClaudeAccount; got != "acme-wide" {
		t.Fatalf("stages.review.claude_account = %q, want acme-wide", got)
	}

	out, _, err := execute(t, "settings", "get", "stages.review.claude_account")
	if err != nil {
		t.Fatalf("settings get stages.review.claude_account: %v", err)
	}
	for _, want := range []string{"stages.review.claude_account", "acme-wide", "local"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings get stages.review.claude_account lacks %q:\n%s", want, out)
		}
	}
	// The wildcard spelling of the same block still answers, and it carries the
	// account the file holds.
	out, _, err = execute(t, "settings", "stages.*.claude_account")
	if err != nil {
		t.Fatalf("settings stages.*.claude_account: %v", err)
	}
	if !strings.Contains(out, "acme-wide") {
		t.Errorf("the wildcard row does not carry the account the file holds:\n%s", out)
	}
}

// TestSettingsSetStageAccountDryRunWritesNothing: the dry run classifies the
// edit and leaves the file byte-identical.
func TestSettingsSetStageAccountDryRunWritesNothing(t *testing.T) {
	_, localPath := settingsStageHome(t)
	before := readFileString(t, localPath)
	out, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-wide", "--dry-run")
	if err != nil {
		t.Fatalf("settings set --dry-run: %v", err)
	}
	for _, want := range []string{"stages.review.claude_account", "local", "true", "acme-wide"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run lacks %q:\n%s", want, out)
		}
	}
	if readFileString(t, localPath) != before {
		t.Error("--dry-run changed the local config")
	}
}

// TestSettingsUnsetTakesAStageAccountAway: unset removes the key the set wrote,
// and the read falls back to the default's own sentence.
func TestSettingsUnsetTakesAStageAccountAway(t *testing.T) {
	_, localPath := settingsStageHome(t)
	if _, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-wide", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("settings set: %v", err)
	}
	if _, _, err := execute(t, "settings", "unset", "stages.review.claude_account", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("settings unset: %v", err)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, localPath)))
	if err != nil {
		t.Fatalf("the edited file does not read back: %v", err)
	}
	if got := lc.ClaudeAccountStages["review"].ClaudeAccount; got != "" {
		t.Errorf("stages.review.claude_account = %q after unset, want the key gone", got)
	}
	out, _, err := execute(t, "settings", "get", "stages.review.claude_account")
	if err != nil {
		t.Fatalf("settings get after unset: %v", err)
	}
	if !strings.Contains(out, "the project's default account") {
		t.Errorf("the row does not fall back to its default sentence:\n%s", out)
	}
}

// TestSettingsSetRefusesAnUndeclaredStageAccount: an account this project does
// not declare is refused with the declared names, before anything is written —
// as a write and as a dry run, so a script cannot learn the file's shape from an
// edit that would be refused.
func TestSettingsSetRefusesAnUndeclaredStageAccount(t *testing.T) {
	_, localPath := settingsStageHome(t)
	before := readFileString(t, localPath)
	for _, args := range [][]string{
		{"settings", "set", "stages.review.claude_account", "acme-nowhere", "--layer", "local", "--yes"},
		{"settings", "set", "stages.review.claude_account", "acme-nowhere", "--dry-run"},
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v wrote an account this project does not declare", args)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, ExitCode(err), ExitUserError)
		}
		for _, want := range []string{"acme-nowhere", "acme-narrow", "acme-wide", "nothing was written"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the refusal lacks %q: %v", args, want, err)
			}
		}
	}
	if readFileString(t, localPath) != before {
		t.Error("a refused write changed the local config")
	}
}

// TestSettingsSetRefusesAStageAccountThatCoversLess: an account whose
// allow_data_to misses a repository the project onboards is refused, naming the
// repositories it misses — the launch would otherwise refuse every run of the
// stage on them.
func TestSettingsSetRefusesAStageAccountThatCoversLess(t *testing.T) {
	_, localPath := settingsStageHome(t)
	before := readFileString(t, localPath)
	_, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-narrow", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("settings set wrote an account that does not cover the project's repositories")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	for _, want := range []string{"acme-narrow", "acme/other", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
	// The same name is refused as a dry run.
	if _, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-narrow", "--dry-run"); err == nil {
		t.Error("--dry-run reported an edit the write refuses")
	}
	// And the project's own default account is always admitted: no account
	// covers less than the unsuffixed one, which every repository may choose.
	if _, _, err := execute(t, "settings", "set", "stages.review.claude_account", "default", "--layer", "local", "--yes"); err != nil {
		t.Errorf("settings set stages.review.claude_account default: %v", err)
	}
}

// TestSettingsSetRefusesAStageAccountInACodingAgentsSession: the credential
// -routing fence holds for this key as it does for every write outside a
// repository — a coding agent's session writes nothing, --yes included.
func TestSettingsSetRefusesAStageAccountInACodingAgentsSession(t *testing.T) {
	_, localPath := settingsStageHome(t)
	before := readFileString(t, localPath)
	for _, k := range agentMarkers {
		t.Setenv(k, "1")
	}
	_, _, err := execute(t, "settings", "set", "stages.review.claude_account", "acme-wide", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("a coding agent's session wrote the stage account")
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal is not the session's own: %v", err)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsWritesEveryRunStageAccountKey: every stage name the config accepts
// is a key this path writes and the read offers an edit for. The registry's rows
// are derived from config.RunStages (internal/settings' stageAccountRows) and
// this list is not, so a seventh stage would leave the read offering a row the
// write refuses — which is the state this test exists to fail on.
func TestSettingsWritesEveryRunStageAccountKey(t *testing.T) {
	settingsWriteHomeWith(t, settingsStageAccounts)
	for _, stage := range config.RunStages {
		key := "stages." + stage + ".claude_account"
		row, err := settingsWriteRow(key)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if row.Owner != "settings set" {
			t.Errorf("%s: Owner = %q, want the generic path", key, row.Owner)
		}
		out, _, err := execute(t, "settings", "get", key)
		if err != nil {
			t.Errorf("settings get %s: %v", key, err)
			continue
		}
		if !strings.Contains(out, key) {
			t.Errorf("settings get %s does not print its own row:\n%s", key, out)
		}
	}
}
