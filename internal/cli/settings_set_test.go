package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// settingsWriteHome is the write tests' fixture: a project config on this
// machine and no coding-agent marker, so a write that is meant to land is not
// refused by the session the test binary happens to run in. It returns the
// config home and the selected project config's path.
func settingsWriteHome(t *testing.T) (home, localPath string) {
	t.Helper()
	return settingsWriteHomeWith(t, "")
}

// settingsWriteHomeWith is settingsWriteHome with extra YAML appended to the
// project config, for a test that needs the file to carry a comment or a key
// the edit must leave alone.
func settingsWriteHomeWith(t *testing.T, extra string) (home, localPath string) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	home = settingsFixture(t, extra)
	return home, os.Getenv("FUGARO_CONFIG")
}

// settingsWriteCheckout is a real checkout of repo, carrying fugaroYAML, with
// the process's working directory moved into it: `--layer repo` without --repo
// takes the current directory's checkout, which is what these tests exercise.
func settingsWriteCheckout(t *testing.T, fugaroYAML string) string {
	t.Helper()
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(fugaroYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return filepath.Join(dir, "fugaro.yaml")
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSettingsSetRefusesAKeyOwnedByAnotherFamily: `settings set` is the
// generic path, and a key a dedicated family writes is refused here with that
// family's command named rather than written by a second path (D853). The
// family check runs before the layer is even looked up, so a refusal never
// depends on where the write would have landed.
func TestSettingsSetRefusesAKeyOwnedByAnotherFamily(t *testing.T) {
	settingsWriteHome(t)
	_, _, err := execute(t, "settings", "set", "agent.model", "claude-sonnet-5", "--layer", "repo")
	if err == nil {
		t.Fatal("settings set wrote a key the models family owns")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "fugaro models") {
		t.Errorf("the refusal does not name the family's command: %v", err)
	}
}

// TestSettingsSetRefusesALayerTheKeyMayNotBeSetIn: a key's layers come from the
// registry, and a write at a layer the key may not be set in is refused with
// the layers it may (spec §4.2 step 2).
func TestSettingsSetRefusesALayerTheKeyMayNotBeSetIn(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	_, _, err := execute(t, "settings", "set", "max_parallel", "4", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("settings set wrote max_parallel at --layer repo, which the registry does not allow")
	}
	if !strings.Contains(err.Error(), "local") {
		t.Errorf("the refusal does not name the layers the key may be set in: %v", err)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsSetRefusesAValueTheLoaderRefuses: the edited document is held
// against its own strict loader, so a value the loader would refuse never
// reaches the file, and the refusal carries the loader's own message (spec §4.2
// step 3).
func TestSettingsSetRefusesAValueTheLoaderRefuses(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	_, _, err := execute(t, "settings", "set", "update_check", "maybe", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("settings set wrote a value the local config's loader refuses")
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("the refusal does not carry the loader's own message: %v", err)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsSetRepoLayerWithoutRepoUsesTheCurrentCheckout: with no --repo the
// write lands in the current directory's checkout (spec §4.2 step 2).
func TestSettingsSetRepoLayerWithoutRepoUsesTheCurrentCheckout(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("fugaro.yaml does not carry the write:\n%s", got)
	}
}

// TestSettingsSetRepoLayerOutsideACheckoutIsRefused: the other half of that
// step. A directory that is not a checkout is refused rather than guessed at,
// and nothing is written anywhere.
func TestSettingsSetRepoLayerOutsideACheckoutIsRefused(t *testing.T) {
	settingsWriteHome(t)
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("settings set guessed a repository for --layer repo outside a checkout")
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("the refusal does not say what to pass instead: %v", err)
	}
}

// TestSettingsSetRefusesInAnAgentSessionEvenWithYes: a write outside the
// repository is refused in a coding agent's session, and the refusal prints the
// diff and the command and writes nothing, --yes included (SD9).
func TestSettingsSetRefusesInAnAgentSessionEvenWithYes(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, "settings", "set", "max_parallel", "5", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("settings set wrote the local config from a coding agent's session")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro settings set max_parallel 5 --layer local") {
		t.Errorf("the refusal does not print the command to run: %v", err)
	}
	if !strings.Contains(out, "+ max_parallel: 5") {
		t.Errorf("the refusal does not print the diff:\n%s", out)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsSetRepoLayerIsAllowedInAnAgentSession: the other half — a file
// inside the repository is one an agent may edit anyway, so an ordinary write
// there runs (spec §4.2 step 4).
func TestSettingsSetRepoLayerIsAllowedInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("fugaro.yaml does not carry the write:\n%s", got)
	}
}

// TestSettingsSetLooseningACeilingAsksForTheTypedProjectName: loosening a
// ceiling key asks for the project's name typed at a terminal. --yes answers
// that same form outside a coding agent's session (D1107), and this is the path
// that passes it to nothing: no terminal and no flag is still a refusal.
func TestSettingsSetLooseningACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)

	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
	if err == nil {
		t.Fatal("a widening with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	out, _, err := executeStdin(t, "aurora\n", "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("fugaro.yaml does not carry the loosened ceiling:\n%s", got)
	}
}

// TestSettingsSetRepoLayerRefusesALooseningInAnAgentSession: the typed-name
// carve-out (spec §4.2 step 4). The file decides the ceiling of every run the
// branch starts, so an agent session may propose that edit but not make it,
// even at --layer repo and even with --yes.
func TestSettingsSetRepoLayerRefusesALooseningInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("a coding agent's session loosened a repository's ceiling")
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro settings set budget.per_run_usd 50 --layer repo") {
		t.Errorf("the refusal does not print the command to run: %v", err)
	}
	if !strings.Contains(out, "+   per_run_usd: 50") {
		t.Errorf("the refusal does not print the diff:\n%s", out)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}
}

// TestSettingsSetBacksUpAndWritesAtomically: the old file is kept beside the
// new one, mode 0600, and the new bytes are in place (spec §4.2 step 5).
func TestSettingsSetBacksUpAndWritesAtomically(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "commit_name: person-1") {
		t.Errorf("the local config does not carry the write:\n%s", got)
	}
	backups, err := filepath.Glob(localPath + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("%d backups beside the config, want one", len(backups))
	}
	if got := readFileString(t, backups[0]); got != before {
		t.Errorf("the backup does not hold the old bytes:\n%s", got)
	}
	fi, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the backup's mode is %o, want 600", perm)
	}
}

// TestSettingsSetKeepsTheRestOfTheFile: the edit splices bytes, so a comment
// and every line the write does not have to touch survive it (D847).
func TestSettingsSetKeepsTheRestOfTheFile(t *testing.T) {
	_, localPath := settingsWriteHomeWith(t, "# the owner's own note, which an edit must not eat\n")
	out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, localPath)
	for _, want := range []string{
		"# the owner's own note, which an edit must not eat\n",
		"region: us-east5\n",
		"runs_bucket: fugaro-runs-aurora-gcp\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the edit dropped %q:\n%s", want, got)
		}
	}
}

// TestSettingsAddAndRemoveOnAListKey: a string-list key takes --add and
// --remove, and the edits accumulate rather than replacing the list.
func TestSettingsAddAndRemoveOnAListKey(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	if out, _, err := execute(t, "settings", "set", "git.pr.labels", "--add", "bug", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _, err := execute(t, "settings", "set", "git.pr.labels", "--add", "urgent", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, path)
	for _, want := range []string{"- bug", "- urgent"} {
		if !strings.Contains(got, want) {
			t.Errorf("fugaro.yaml does not carry %q:\n%s", want, got)
		}
	}
	if out, _, err := execute(t, "settings", "set", "git.pr.labels", "--remove", "bug", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got = readFileString(t, path)
	if strings.Contains(got, "bug") || !strings.Contains(got, "- urgent") {
		t.Errorf("--remove did not remove exactly one item:\n%s", got)
	}
}

// TestSettingsRefusesEmptyingAListKey: a list key emptied by --remove is
// refused, because the loader tells an absent key from an empty list and the
// two mean different things (spec §4.2).
func TestSettingsRefusesEmptyingAListKey(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	if out, _, err := execute(t, "settings", "set", "git.pr.labels", "--add", "bug", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "set", "git.pr.labels", "--remove", "bug", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--remove emptied the list")
	}
	if !strings.Contains(err.Error(), "unset") {
		t.Errorf("the refusal does not name the command that removes the key: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}
}

// TestSettingsUnsetRemovesTheKeyRatherThanWritingTheDefault: unset hands the
// decision back to the layer below and never writes a default in the key's
// place (spec §4.3).
func TestSettingsUnsetRemovesTheKeyRatherThanWritingTheDefault(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	if out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _, err := execute(t, "settings", "unset", "commit_name", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, localPath)
	if strings.Contains(got, "commit_name") {
		t.Errorf("unset left the key behind:\n%s", got)
	}
}

// TestSettingsUnsetOfACeilingAsksForTheTypedProjectName: removal crosses the
// same boundary as loosening (spec §4.3), so with no terminal and no --yes it
// is refused; --yes is the other answer D1107 allows outside a coding agent's
// session.
func TestSettingsUnsetOfACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo")
	if err == nil {
		t.Fatal("removing a ceiling with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "unset", "budget.per_run_usd", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); strings.Contains(got, "per_run_usd") {
		t.Errorf("unset left the ceiling behind:\n%s", got)
	}
}

// TestSettingsUnsetOfNothingIsAnExitZeroSentence: nothing to remove is a no-op
// with a sentence and exit 0, so an idempotent script does not fail (spec §4.3).
func TestSettingsUnsetOfNothingIsAnExitZeroSentence(t *testing.T) {
	settingsWriteHome(t)
	out, _, err := execute(t, "settings", "unset", "commit_name", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("unset of nothing = %v, want exit 0", err)
	}
	if !strings.Contains(out, "nothing to unset") {
		t.Errorf("the no-op prints no sentence:\n%s", out)
	}
	if strings.Contains(out, "next:") {
		t.Errorf("the no-op claims there is something to publish:\n%s", out)
	}
}

// TestSettingsSetPrintsOnlyFugaroUpdateNext: every local edit ends with one
// line, and it is `next: fugaro update` — never a list of steps (spec §1).
func TestSettingsSetPrintsOnlyFugaroUpdateNext(t *testing.T) {
	settingsWriteHome(t)
	out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	next := []string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "next:") {
			next = append(next, line)
		}
	}
	if len(next) != 1 || next[0] != "next: fugaro update" {
		t.Errorf("next lines = %q, want exactly one `next: fugaro update`", next)
	}
}

// TestSettingsSetWritesNothingInTheCloud: the write path edits a local file and
// opens nothing else. It runs here with no GCP credentials, no Firebase project
// and no network configured, which a cloud write could not do.
func TestSettingsSetWritesNothingInTheCloud(t *testing.T) {
	for _, k := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "FUGARO_RTDB_URL"} {
		t.Setenv(k, "")
	}
	_, localPath := settingsWriteHome(t)
	out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, localPath), "commit_name: person-1") {
		t.Error("the write did not land in the local file")
	}
	for _, word := range []string{"gs://", "bucket", "database", "firebase"} {
		if strings.Contains(strings.ToLower(out), word) {
			t.Errorf("the write mentions %q, which is not a local edit:\n%s", word, out)
		}
	}
}

// TestTheThreeEditorsPreserveBytesTheSameWay: one editor, three documents
// (D847). The same shape of edit — a scalar added under a mapping, with a
// comment above it and lines the edit does not touch — leaves the same things
// standing in the local config, the local layer file and a repository's
// fugaro.yaml.
func TestTheThreeEditorsPreserveBytesTheSameWay(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	layerPath, err := localcfg.LayerFile(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layerPath, []byte("# the layer's own note\nversion: 1\nproject: aurora\ngcp_project: aurora-gcp\ndefaults:\n  git:\n    provider: github\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))

	cases := []struct {
		name  string
		path  string
		args  []string
		key   string
		added string
	}{
		{"local config", localPath, []string{"settings", "set", "commit_name", "person-1", "--layer", "local", "--yes"}, "commit_name", "commit_name: person-1"},
		{"project layer", layerPath, []string{"settings", "set", "layer.defaults.agent.max_budget_usd", "30", "--layer", "project", "--yes"}, "layer.defaults.agent.max_budget_usd", "max_budget_usd: 30"},
		{"fugaro.yaml", repoPath, []string{"settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes"}, "git.base_branch", "base_branch: develop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := readFileString(t, tc.path)
			out, _, err := execute(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			after := readFileString(t, tc.path)
			if !strings.Contains(after, tc.added) {
				t.Errorf("the edit did not land:\n%s", after)
			}
			// Every line of the old file that is not the added one stands
			// exactly as it was, comments included.
			for _, line := range strings.Split(strings.TrimRight(before, "\n"), "\n") {
				if !strings.Contains(after, line) {
					t.Errorf("the edit dropped %q:\n%s", line, after)
				}
			}
		})
	}
}

// TestSettingsGetOnAKeySetNowhereIsTheDefaultRowAndExitZero: `get` is this
// task's subcommand, and a key set nowhere prints its default row and exits 0
// rather than failing an idempotent script (spec §4.1, D852).
func TestSettingsGetOnAKeySetNowhereIsTheDefaultRowAndExitZero(t *testing.T) {
	settingsWriteHome(t)
	out, _, err := execute(t, "settings", "get", "max_parallel")
	if err != nil {
		t.Fatalf("settings get on a key set nowhere = %v, want exit 0", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("one key prints its header and its row, got:\n%s", out)
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 || fields[0] != "max_parallel" {
		t.Fatalf("row = %q, want max_parallel and its columns", lines[1])
	}
	if fields[3] != "default" {
		t.Errorf("WINS = %q, want default", fields[3])
	}
	if fields[2] != fields[4] {
		t.Errorf("VALUE = %q and DEFAULT = %q, want the default in both", fields[2], fields[4])
	}
}

// TestSettingsGetShowsTheRowOfAKeyALayerSets: the same subcommand on a key a
// layer does set prints the winning value and names the layer that decided it,
// so `get` is the read and not a second default-printing path.
func TestSettingsGetShowsTheRowOfAKeyALayerSets(t *testing.T) {
	settingsWriteHome(t)
	if out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _, err := execute(t, "settings", "get", "commit_name")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("one key prints its header and its row, got:\n%s", out)
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 || fields[0] != "commit_name" {
		t.Fatalf("row = %q, want commit_name and its columns", lines[1])
	}
	if fields[2] != "person-1" {
		t.Errorf("VALUE = %q, want the value the layer holds", fields[2])
	}
	if fields[3] != "local" {
		t.Errorf("WINS = %q, want local", fields[3])
	}
}

// TestSettingsGetRefusesAnUnknownKeyWithTheNearest: `get` refuses a name no
// key matches the way the read does — non-zero, with the nearest registry
// names — rather than printing an empty table a script would read as "unset".
func TestSettingsGetRefusesAnUnknownKeyWithTheNearest(t *testing.T) {
	settingsWriteHome(t)
	out, _, err := execute(t, "settings", "get", "budget.per_run_uds")
	if err == nil {
		t.Fatalf("an unknown key is refused, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "budget.per_run_usd") {
		t.Errorf("refusal = %q, want the nearest registry name", err.Error())
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitUserError)
	}
}

// TestSettingsSetUnknownKeyHintNamesOnlyKeysItCouldWrite: the refusal's nearest
// names come from the keys `set` writes, not from the whole registry. Two rows
// the read prints — budget.per_day_usd and budget.mode — are refused by name
// here, and a hint that answered a misspelling of one with it would send the
// person from this refusal to the next one.
func TestSettingsSetUnknownKeyHintNamesOnlyKeysItCouldWrite(t *testing.T) {
	settingsWriteHome(t)
	for _, c := range []struct{ key, want string }{
		// The misspelling of a refused key is answered with a key this command
		// writes, which is the nearest of those.
		{"budget.per_day_uds", "budget.per_run_usd"},
		{"budget.per_dy_usd", "budget.per_run_usd"},
		{"budget.mod", ""},
	} {
		t.Run(c.key, func(t *testing.T) {
			_, _, err := execute(t, "settings", "set", c.key, "1", "--layer", "local", "--yes")
			if err == nil {
				t.Fatalf("settings set wrote the unknown key %s", c.key)
			}
			_, hint, ok := strings.Cut(err.Error(), "the nearest are ")
			if !ok {
				t.Fatalf("the refusal names no nearest keys: %v", err)
			}
			for _, refused := range []string{"budget.mode", "budget.per_day_usd"} {
				if strings.Contains(hint, refused) {
					t.Errorf("the hint names %s, which this command refuses:\n%s", refused, hint)
				}
			}
			if c.want != "" && !strings.Contains(hint, c.want) {
				t.Errorf("the hint does not name %s:\n%s", c.want, hint)
			}
		})
	}
}

// settingsRefusalHint is the nearest-names tail of a refusal: the rows it
// named, or a failure when it named none.
func settingsRefusalHint(t *testing.T, err error) string {
	t.Helper()
	_, hint, ok := strings.Cut(err.Error(), "the nearest are ")
	if !ok {
		t.Fatalf("the refusal names no nearest keys: %v", err)
	}
	return hint
}

// settingsRefusedNames is the keys a refusal's hint named, in the order it
// printed them.
func settingsRefusedNames(hint string) []string {
	out := []string{}
	for _, name := range strings.Split(hint, ", ") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// settingsHintNeverNames pins the keys a write's hint must never name: the rows
// this command refuses by name — budget.mode and budget.per_day_usd — the
// installation's own plumbing an endpoints.* name is part of, and a row the
// registry marks Sensitive, whose value the diff would print.
func settingsHintNeverNames(t *testing.T, hint string) {
	t.Helper()
	refused := []string{"budget.per_day_usd", "budget.mode", "budget.firebase_api_key"}
	for _, row := range settings.Rows() {
		if strings.HasPrefix(row.Key, "endpoints.") || row.Sensitive {
			refused = append(refused, row.Key)
		}
	}
	for _, key := range refused {
		if strings.Contains(hint, key) {
			t.Errorf("the hint names %s, which this command refuses:\n%s", key, hint)
		}
	}
}

// settingsWriteFixtures lays out this machine's three writable documents — the
// local project config, the local project layer file and a checkout's
// fugaro.yaml — so a test can probe a key in the layer it is set in.
func settingsWriteFixtures(t *testing.T) {
	t.Helper()
	settingsWriteHome(t)
	settingsWriteLayerFile(t, "")
	settingsWriteCheckout(t, settingsRepoYAML(""))
}

// settingsProbeItem is the item --add writes when a test only needs to know
// whether a key takes a list: a plain string, which a label, a reviewer and a
// model ID all take.
const settingsProbeItem = "probe-item"

// settingsProbeItems are the items of the list keys whose validator is
// stricter than "any string": budget.allowed_models refuses a name that is not
// an explicit model ID (config.CheckModelID) and followup.trusted refuses
// anything but a GitHub numeric user ID. The probe writes the item its key
// needs, and every other key takes settingsProbeItem.
var settingsProbeItems = map[string]string{
	"budget.allowed_models": "claude-probe-9-9",
	"followup.trusted":      "100000002",
}

// settingsAddLands reports whether `--add` lands on key, in the layer the row
// is set in. It asks the command rather than a second list, so what "a key that
// takes a list" means is the documents' own loaders' answer.
func settingsAddLands(t *testing.T, key string) bool {
	t.Helper()
	row, ok := settings.Lookup(key)
	if !ok {
		t.Fatalf("%s is not a registry row", key)
	}
	layer := settingsDefaultLayer(row)
	if layer == "" {
		t.Fatalf("%s may be set in no layer a write lands in", key)
	}
	item := settingsProbeItem
	if strict, ok := settingsProbeItems[key]; ok {
		item = strict
	}
	_, _, err := execute(t, "settings", "set", key, "--add", item, "--layer", layer, "--yes")
	return err == nil
}

// TestSettingsSetUnknownKeyHintNamesOnlyRowsTheWriteAccepts: the refusal's
// nearest names come from the rows this command accepts, and never from a
// registry row it refuses (spec §4.4). The allowlist alone is not that set: it
// carries the blocks whole, so `agent.model` reaches the hint through the
// `agent.` prefix while the models family owns the row and `settings set`
// refuses it, and the hint that answered `budget.mod` with it sent the person
// from this refusal to the next one.
func TestSettingsSetUnknownKeyHintNamesOnlyRowsTheWriteAccepts(t *testing.T) {
	settingsWriteHome(t)
	_, _, err := execute(t, "settings", "set", "budget.mod", "1", "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("settings set wrote the unknown key budget.mod")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitUserError)
	}
	hint := settingsRefusalHint(t, err)
	settingsHintNeverNames(t, hint)
	named := settingsRefusedNames(hint)
	if len(named) == 0 {
		t.Fatalf("the refusal named no key at all: %v", err)
	}
	for _, key := range named {
		if _, err := settingsWriteRow(key); err != nil {
			t.Errorf("the hint names %s, which this command refuses: %v", key, err)
		}
	}
}

// TestSettingsSetAddHintNamesOnlyListKeysTheWriteAccepts: `--add` and `--remove`
// edit a string-list key, so the hint that answers a misspelling of one names
// the keys that take a list and nothing else. A scalar row answered `--add`
// with itself sends the person from this refusal to the loader's, which is
// where `--add` on a scalar ends.
func TestSettingsSetAddHintNamesOnlyListKeysTheWriteAccepts(t *testing.T) {
	settingsWriteFixtures(t)
	_, _, err := execute(t, "settings", "set", "budget.mod", "--add", settingsProbeItem, "--layer", "local", "--yes")
	if err == nil {
		t.Fatal("settings set --add wrote the unknown key budget.mod")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitUserError)
	}
	hint := settingsRefusalHint(t, err)
	settingsHintNeverNames(t, hint)
	named := settingsRefusedNames(hint)
	if len(named) == 0 {
		t.Fatalf("the refusal named no key at all: %v", err)
	}
	for _, key := range named {
		if _, err := settingsWriteRow(key); err != nil {
			t.Errorf("the hint names %s, which this command refuses: %v", key, err)
		}
		if !settingsAddLands(t, key) {
			t.Errorf("the hint names %s, where --add does not land:\n%s", key, hint)
		}
	}
	if !strings.Contains(hint, "budget.allowed_models") {
		t.Errorf("the hint names no list key near budget.mod:\n%s", hint)
	}
}

// TestSettingsAddHintListKeysAreTheKeysThatTakeAList: the list the --add hint
// draws from is the write path's own answer and not a second opinion. Every row
// the write accepts is probed once, in the layer it is set in, and the probe
// has to agree with the list: --add lands on the rows it names and the
// document's own loader refuses it on the rest. A list key added to the
// registry later — or a row that stops taking one — fails here, where a stale
// list would leave the hint naming keys that cannot take it.
func TestSettingsAddHintListKeysAreTheKeysThatTakeAList(t *testing.T) {
	settingsWriteFixtures(t)
	accepted := 0
	for _, row := range settings.Rows() {
		if !settingsHintRow(row) {
			continue
		}
		accepted++
		if got, want := settingsAddLands(t, row.Key), settingsHintListKeys[row.Key]; got != want {
			t.Errorf("%s: --add lands = %v, the hint's list says %v", row.Key, got, want)
		}
	}
	if accepted < len(settingsHintListKeys) {
		t.Fatalf("probed %d accepted rows, fewer than the hint's own list carries", accepted)
	}
}

// settingsWriteLayerFile writes the local project layer file the way
// `fugaro update` seeds it: the anchors its own loader requires, plus whatever
// the caller adds.
func settingsWriteLayerFile(t *testing.T, extra string) string {
	t.Helper()
	path, err := localcfg.LayerFile(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	body := "version: 1\nproject: aurora\ngcp_project: aurora-gcp\n" + extra
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSettingsGenericKeyIsSpec44sAllowlist: the generic path writes spec §4.4's
// names and nothing else, and the list is an allowlist — a name the spec does
// not hand it is refused whatever the registry holds. The names are spelled the
// way the registry spells them: the project layer's own block carries the
// `layer.` prefix (SD14), so §4.4's `default_profile` and `defaults.git.*` are
// `layer.default_profile` and `layer.defaults.git.*` here.
//
// The list is the first of the refusals, not the only one: a name it holds is
// still refused when a family owns it (layer.defaults.agent.model) or when it
// has a varying segment (workflows.*.commands.test).
func TestSettingsGenericKeyIsSpec44sAllowlist(t *testing.T) {
	allowed := []string{
		// §4.4: "budget.{per_run_usd,max_run_tokens,allowed_models,unreachable_grace}".
		"budget.per_run_usd", "budget.max_run_tokens", "budget.allowed_models", "budget.unreachable_grace",
		"base_branch", "max_parallel", "update_check", "watch.burn_alert_usd_per_hour",
		"commit_name", "commit_email",
		"agent.review_rounds", "agent.max_budget_usd", "agent.recipe",
		"git.base_branch", "git.pr.labels",
		"followup.trusted", "followup.allow_public",
		"governance.review_ratio", "governance.stage_budget", "layer.governance.review_ratio",
		"layer.default_profile", "layer.defaults.git.provider", "layer.defaults.agent.review_rounds",
		"credentials.fallback", "clone_urls.github",
	}
	for _, key := range allowed {
		if !settingsGenericKey(key) {
			t.Errorf("settingsGenericKey(%q) = false, and spec §4.4 hands it to the generic path", key)
		}
	}
	refused := []string{
		"endpoints.compute", "terraform.state_bucket", "policy_generation", "layer_generation",
		"credentials_service.url", "budget.rtdb_url", "budget.firebase_api_key",
		"name", "runs_bucket", "bucket_url", "repos.*.base_branch",
		"providers.*.base_url", "bitbucket_api_roots", "model_prices", "routing.claude",
		// Registry rows the spec's §4.4 list does not name, so the generic path
		// refuses them however the audit or the local config classifies them.
		"budget.mode", "budget.per_day_usd",
	}
	for _, key := range refused {
		if settingsGenericKey(key) {
			t.Errorf("settingsGenericKey(%q) = true, and spec §4.4 does not hand it to the generic path", key)
		}
	}
}

// TestSettingsSetRefusesEveryKeyOutsideSpec44: the installation's own plumbing
// is refused by name, so `settings set` can never be turned into a second path
// that writes an endpoint, a Terraform switch or a generation counter (spec
// §4.4, D853). The refusal names the key and nothing is written anywhere.
func TestSettingsSetRefusesEveryKeyOutsideSpec44(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	for _, key := range []string{
		"endpoints.compute", "terraform.state_bucket", "policy_generation", "layer_generation",
		"credentials_service.url", "budget.rtdb_url", "name", "runs_bucket", "bucket_url",
		// The two registry rows §4.4 leaves out: they are read by `settings`
		// and written by `fugaro setup` and the local config, and the generic
		// path does not write them.
		"budget.mode", "budget.per_day_usd",
	} {
		t.Run(key, func(t *testing.T) {
			out, _, err := execute(t, "settings", "set", key, "x", "--yes")
			if err == nil {
				t.Fatalf("settings set wrote %s:\n%s", key, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("the refusal does not name the key: %v", err)
			}
			if readFileString(t, localPath) != before {
				t.Errorf("the refused write changed the local config")
			}
		})
	}
}

// TestSettingsSetRefusesASensitiveKeyAndNeverPrintsItsValue: a row marked
// Sensitive is refused before the diff is built, so the value the person typed
// never reaches the output — not the diff, not the refusal (spec §2.3).
func TestSettingsSetRefusesASensitiveKeyAndNeverPrintsItsValue(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	const value = "not-a-real-credential-value"
	out, _, err := execute(t, "settings", "set", "budget.firebase_api_key", value, "--layer", "local", "--yes")
	if err == nil {
		t.Fatalf("settings set wrote a sensitive row:\n%s", out)
	}
	if !strings.Contains(err.Error(), "sensitive") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if printed := out + err.Error(); strings.Contains(printed, value) {
		t.Errorf("the sensitive value reached the output:\n%s", printed)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsCeilingKeyCoversTheCapsAndTheLayerSpellings: D846's prefix test
// is `budget.*` and `governance.*`, the agent block's two caps are the same
// boundary under their own names, and the project layer is the same setting
// under the `layer.` prefix (SD14) — its own block and its `defaults.` block,
// which is the cap every repository the layer templates runs with.
func TestSettingsCeilingKeyCoversTheCapsAndTheLayerSpellings(t *testing.T) {
	for _, key := range []string{
		"budget.per_run_usd", "budget.mode", "budget.allowed_models",
		"governance.review_ratio", "governance.stage_budget",
		"layer.governance.review_ratio",
		"agent.max_budget_usd", "agent.max_run_tokens",
		"layer.defaults.agent.max_budget_usd",
	} {
		if !settingsCeilingKey(key) {
			t.Errorf("settingsCeilingKey(%q) = false, and D846 makes it a ceiling", key)
		}
	}
	for _, key := range []string{"git.base_branch", "agent.review_rounds", "followup.allow_public", "layer.defaults.git.provider"} {
		if settingsCeilingKey(key) {
			t.Errorf("settingsCeilingKey(%q) = true, and it is no ceiling", key)
		}
	}
}

// TestSettingsLoosensRanksABudgetMode: budget.mode is a ceiling whose values
// are ordered, not compared as numbers: enforce is the tightest, then observe,
// then off, and only a write that moves down the order is a loosening
// (policy.Merge's order). The write path never reaches this — the allowlist
// refuses budget.mode — but the publish door does, so a node republished at a
// stronger mode has to stay outside the loosening it would otherwise be.
func TestSettingsLoosensRanksABudgetMode(t *testing.T) {
	cases := []struct {
		old, new string
		want     bool
	}{
		{"enforce", "observe", true},
		{"enforce", "off", true},
		{"observe", "enforce", false},
		{"observe", "off", true},
		{"enforce", "enforce", false},
	}
	for _, tc := range cases {
		if got := settingsLoosens("budget.mode", tc.old, tc.new); got != tc.want {
			t.Errorf("settingsLoosens(budget.mode, %q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}

// TestSettingsLoosensTreatsAWiderModelListAsAWidening: budget.allowed_models
// loosens when the new list holds a model the old one did not — a subset is a
// tightening, so dropping a model is not the typed form.
func TestSettingsLoosensTreatsAWiderModelListAsAWidening(t *testing.T) {
	cases := []struct {
		old, new string
		want     bool
	}{
		{"a,b", "a", false},
		{"a,b", "a,b", false},
		{"a", "a,b", true},
		{"a,b", "a,c", true},
	}
	for _, tc := range cases {
		if got := settingsLoosens("budget.allowed_models", tc.old, tc.new); got != tc.want {
			t.Errorf("settingsLoosens(budget.allowed_models, %q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}

// TestSettingsLoosensTreatsAnUnknownValueFormAsAWidening: a ceiling value this
// Fugaro cannot compare is not proof that the write tightens, so it takes the
// typed name. D846's asymmetry — the safe half is asking — is the point.
func TestSettingsLoosensTreatsAnUnknownValueFormAsAWidening(t *testing.T) {
	for _, tc := range []struct{ old, new string }{
		{"10", "many"},
		{"many", "10"},
		{"enforce", "unknown-mode"},
	} {
		if !settingsLoosens("budget.per_run_usd", tc.old, tc.new) {
			t.Errorf("settingsLoosens(budget.per_run_usd, %q, %q) = false, want true (an unknown form is not a proven tightening)", tc.old, tc.new)
		}
	}
}

// TestSettingsSetLooseningAGovernanceCeilingAsksForTheTypedProjectName: the
// other half of D846's prefix test, and the one at --layer repo — a governance
// ratio written into a repository's own file crosses the same boundary.
func TestSettingsSetLooseningAGovernanceCeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("governance:\n  review_ratio: 0.5\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "governance.review_ratio", "0.9", "--layer", "repo")
	if err == nil {
		t.Fatal("a governance ceiling with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "governance.review_ratio", "0.9", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "review_ratio: 0.9") {
		t.Errorf("fugaro.yaml does not carry the loosened ceiling:\n%s", got)
	}
}

// TestSettingsSetTypedTheProjectLayersGovernanceBlock: the project layer's own
// `layer.governance.*` is the same ceiling under the prefix SD14 gives it, so
// it is typed exactly as `governance.*` is.
func TestSettingsSetTypedTheProjectLayersGovernanceBlock(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, "governance:\n  review_ratio: 0.5\n")
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "layer.governance.review_ratio", "0.9", "--layer", "project")
	if err == nil {
		t.Fatal("a layer ceiling with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "layer.governance.review_ratio", "0.9", "--layer", "project"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "review_ratio: 0.9") {
		t.Errorf("the layer file does not carry the loosened ceiling:\n%s", got)
	}
}

// TestSettingsSetTreatsEveryBooleanSpellingAsTurningAFollowupKeyOn: the value
// on the command line is read the way the config decoder reads it, so `True`,
// `TRUE`, `yes` and `on` are the same widening as `true` and ask for the typed
// name just the same. The decoder is asked here, not a second table, so the two
// cannot part company.
func TestSettingsSetTreatsEveryBooleanSpellingAsTurningAFollowupKeyOn(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)
	noTerminal(t)
	for _, spelling := range []string{"true", "True", "TRUE", "yes", "Yes", "on"} {
		t.Run(spelling, func(t *testing.T) {
			_, _, err := execute(t, "settings", "set", "followup.allow_public", spelling, "--layer", "repo")
			if err == nil {
				t.Fatalf("followup.allow_public %s was turned on with no terminal and no --yes", spelling)
			}
			if !strings.Contains(err.Error(), "typed confirmation") {
				t.Errorf("the refusal does not name the typed form: %v", err)
			}
			if readFileString(t, path) != before {
				t.Error("the refused write changed fugaro.yaml")
			}
		})
	}
	// The decoder's own answer, for the spellings above that are not the
	// literal `true`: the file a run reads turns the follow-up on.
	cfg, ps := config.Parse([]byte(settingsRepoYAML("followup:\n  allow_public: yes\n")))
	if len(ps) > 0 {
		t.Fatalf("the decoder refuses the spelling this command reads as true: %s", ps[0].String())
	}
	if !cfg.Followup.AllowPublic {
		t.Error("the config decoder reads followup.allow_public: yes as false")
	}
	// And the other direction: false is not a widening, so --yes writes it.
	if out, _, err := execute(t, "settings", "set", "followup.allow_public", "false", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "allow_public: false") {
		t.Errorf("fugaro.yaml does not carry the tightened key:\n%s", got)
	}
}

// TestSettingsSetComparesACeilingAgainstTheValueARunGets: D846 guards what a
// run gets, not what one file says, so a write into a repository's file that
// raises a ceiling a layer below already set is the typed form too — the file
// it edits is not the only place the value comes from. A run gets the tighter
// of the two, so a write that raises either one is the typed form.
func TestSettingsSetComparesACeilingAgainstTheValueARunGets(t *testing.T) {
	settingsWriteHomeWith(t, "budget:\n  per_run_usd: 10\n")
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	// The repository's file writes no cap; the layer below holds 10, so 50
	// raises what a run gets and is the typed form.
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
	if err == nil {
		t.Fatal("a cap that raises what a run gets was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	// Tightening under the same layer is the ordinary write, which is what
	// makes the comparison a comparison and not "always typed".
	if out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "5", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 5") {
		t.Errorf("fugaro.yaml does not carry the tightened cap:\n%s", got)
	}

	// The other side of the same rule: the file is looser than the layer
	// below, so a write between the two raises the file's own ceiling even
	// though a run still gets the layer's.
	loose := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 50\n"))
	_, _, err = execute(t, "settings", "set", "budget.per_run_usd", "20", "--layer", "repo")
	if err == nil {
		t.Fatal("a cap that raises the file's own ceiling was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if got := readFileString(t, loose); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("the refused write changed fugaro.yaml:\n%s", got)
	}
}

// TestSettingsSetRefusesASymlinkedFugaroYAML: a checkout's file is read through
// config.ReadRegular, the one reader of them, so a symlink is refused rather
// than written through to whatever it names.
func TestSettingsSetRefusesASymlinkedFugaroYAML(t *testing.T) {
	settingsWriteHome(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	target := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(target, []byte(settingsRepoYAML("")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	before := readFileString(t, target)
	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("settings set wrote through a symlinked fugaro.yaml")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("the refusal does not say the file is a symlink: %v", err)
	}
	// The read refuses it, and not only the compare-and-swap the write does
	// just before it: a command that had to read a symlink first would be
	// reading somebody else's file to build the diff it shows.
	if strings.Contains(err.Error(), "re-reading") {
		t.Errorf("the symlink was read and refused only at the write: %v", err)
	}
	if readFileString(t, target) != before {
		t.Error("the refused write reached through the symlink")
	}
}

// TestSettingsSetRepoLayerRefusesACheckoutOfAnotherProject: `--layer repo`
// writes the selected project's own repository, so a checkout whose file is
// anchored to another project is refused rather than edited — the file decides
// the ceiling of a run that belongs to somebody else.
func TestSettingsSetRepoLayerRefusesACheckoutOfAnotherProject(t *testing.T) {
	settingsWriteHome(t)
	other := strings.Replace(settingsRepoYAML(""), "project: aurora", "project: other", 1)
	path := settingsWriteCheckout(t, other)
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("settings set edited a checkout of another project")
	}
	for _, want := range []string{"other", "aurora"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the other project's fugaro.yaml")
	}
}

// TestSettingsSetRefusesAMapKey: a key with a varying segment is a map entry,
// and a map has no generic form (spec §4.2 step 1), so the `*` spelling is
// refused as a map entry rather than as a key nobody listed.
func TestSettingsSetRefusesAMapKey(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	out, _, err := execute(t, "settings", "set", "workflows.*.commands.test", "go test", "--yes")
	if err == nil {
		t.Fatalf("settings set wrote a map entry:\n%s", out)
	}
	if !strings.Contains(err.Error(), "map") {
		t.Errorf("the refusal does not say it is a map entry: %v", err)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused write changed the local config")
	}
}

// TestSettingsSetRefusesAWildcardRowOutsideTheGenericKeys: the pattern lookup
// resolves a wildcard registry row only for a spelling settingsGenericKeys
// carries by name, and `stages.*.claude_account` is the only one it does. A
// concrete spelling of the project layer's `workflows.*.*` or the project
// family's `repos.*.*` is refused like any other key the generic path does not
// write, and nothing is written.
func TestSettingsSetRefusesAWildcardRowOutsideTheGenericKeys(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	for _, key := range []string{"workflows.build.commands.test", "repos.web.base_branch"} {
		t.Run(key, func(t *testing.T) {
			// The rule itself, so the test says which of the two reasons the
			// refusal has: this key resolves no row here at all.
			if _, _, ok := settingsWriteLookup(key); ok {
				t.Errorf("settingsWriteLookup(%q) resolved a wildcard row the generic allowlist does not carry", key)
			}
			out, _, err := execute(t, "settings", "set", key, "x", "--yes")
			if err == nil {
				t.Fatalf("settings set wrote %s:\n%s", key, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
			}
			if readFileString(t, localPath) != before {
				t.Errorf("the refused write changed the local config")
			}
		})
	}
}

// TestSettingsSetRepoLayerChecksTheRepoFlag: --repo names a repository, so it
// goes with --layer repo and nowhere else, it has to be owner/name, and the
// checkout it names has to be one this machine remembers. Each is a refusal
// with nothing written.
func TestSettingsSetRepoLayerChecksTheRepoFlag(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"a repo with a local write", []string{"settings", "set", "commit_name", "person-1", "--layer", "local", "--repo", "owner/repo-a", "--yes"}, "--layer repo"},
		{"a repo with a layer write", []string{"settings", "set", "layer.default_profile", "svc", "--layer", "project", "--repo", "owner/repo-a", "--yes"}, "--layer repo"},
		{"a repo that is not owner/name", []string{"settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "notarepo", "--yes"}, "owner/name"},
		{"a repo with no remembered checkout", []string{"settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "owner/repo-a", "--yes"}, "remembered"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("settings set accepted %v:\n%s", tc.args, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not mention %q: %v", tc.want, err)
			}
			if readFileString(t, localPath) != before {
				t.Error("the refused write changed the local config")
			}
		})
	}
}

// TestSettingsSetRepoLayerPicksTheNamedCheckout: --repo names one repository,
// and the checkout the write lands in has to be that one. A machine that
// remembers several of the project's checkouts must refuse a --repo that is
// none of them rather than take whichever comes first, so this remembers two
// and names a third: the refusal names it and neither remembered file moves
// (M14). The named one is then written, so the refusal is a comparison and not
// "a remembered checkout is never used".
func TestSettingsSetRepoLayerPicksTheNamedCheckout(t *testing.T) {
	home, _ := settingsWriteHome(t)
	aPath := filepath.Join(settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML("")), "fugaro.yaml")
	bPath := filepath.Join(settingsCheckout(t, home, "owner/repo-b", settingsRepoYAML("")), "fugaro.yaml")
	aBefore, bBefore := readFileString(t, aPath), readFileString(t, bPath)

	_, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "owner/repo-c", "--yes")
	if err == nil {
		t.Fatal("settings set wrote a repository this machine does not remember")
	}
	if !strings.Contains(err.Error(), "owner/repo-c") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
	if readFileString(t, aPath) != aBefore || readFileString(t, bPath) != bBefore {
		t.Error("the refused write changed a remembered checkout's fugaro.yaml")
	}

	if out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--repo", "owner/repo-b", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, bPath); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the named checkout's fugaro.yaml does not carry the write:\n%s", got)
	}
	if readFileString(t, aPath) != aBefore {
		t.Error("the write landed in the checkout that was not named")
	}
}

// TestSettingsSetReparsesTheRepoAndLayerDocuments: the edited bytes are held
// against the document's own strict loader, so a value that loader refuses
// never reaches a repository's fugaro.yaml or the local project layer file
// (spec §4.2 step 3).
func TestSettingsSetReparsesTheRepoAndLayerDocuments(t *testing.T) {
	settingsWriteHome(t)
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))
	repoBefore := readFileString(t, repoPath)
	_, _, err := execute(t, "settings", "set", "git.provider", "nosuchforge", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("settings set wrote a provider the repository's loader refuses")
	}
	if !strings.Contains(err.Error(), "would not read back") {
		t.Errorf("the refusal is not the loader's: %v", err)
	}
	if readFileString(t, repoPath) != repoBefore {
		t.Error("the refused write changed fugaro.yaml")
	}

	layerPath := settingsWriteLayerFile(t, "profiles:\n  svc:\n    cpu: \"1\"\n")
	layerBefore := readFileString(t, layerPath)
	_, _, err = execute(t, "settings", "set", "layer.default_profile", "nosuchprofile", "--layer", "project", "--yes")
	if err == nil {
		t.Fatal("settings set wrote a default profile the layer's loader refuses")
	}
	if !strings.Contains(err.Error(), "would not read back") {
		t.Errorf("the refusal is not the loader's: %v", err)
	}
	if readFileString(t, layerPath) != layerBefore {
		t.Error("the refused write changed the layer file")
	}
}

// TestSettingsSetAddOnACeilingAsksForTheTypedProjectName: a member added to a
// ceiling's list widens it — the list is what a run may use — so --add on a
// ceiling is the typed form (D846). --yes is the other answer to it outside a
// coding agent's session (D1107); this is the path with no terminal and none.
func TestSettingsSetAddOnACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  allowed_models:\n    - claude-sonnet-5\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "budget.allowed_models", "--add", "claude-opus-5-5", "--layer", "repo")
	if err == nil {
		t.Fatal("a model added to a ceiling with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "budget.allowed_models", "--add", "claude-opus-5-5", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "claude-opus-5-5") {
		t.Errorf("fugaro.yaml does not carry the added model:\n%s", got)
	}
}

// TestSettingsSetTurningAFollowupKeyOnAsksForTheTypedProjectName: a follow-up
// key turned on widens who can steer a run, so the typed name confirms it. With
// no terminal and no --yes it is refused; --yes is the second answer D1107
// allows outside a coding agent's session.
func TestSettingsSetTurningAFollowupKeyOnAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "followup.allow_public", "true", "--layer", "repo")
	if err == nil {
		t.Fatal("a followup key was turned on with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "followup.allow_public", "true", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "allow_public: true") {
		t.Errorf("fugaro.yaml does not carry the key:\n%s", got)
	}
}

// TestSettingsSetTrustedAddIsTyped: a name added to followup.trusted widens who
// can steer a run that holds the repository's credentials, so `--add` on this
// key is the widening D846 types and not the harmless direction `--add` is on a
// list that narrows something. `--remove` takes someone off the list, which is
// the tightening half, and keeps the ordinary confirmation.
func TestSettingsSetTrustedAddIsTyped(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("followup:\n  trusted:\n    - \"100000001\"\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "followup.trusted", "--add", "100000002", "--layer", "repo")
	if err == nil {
		t.Fatal("an account added to followup.trusted with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	out, _, err := executeStdin(t, "aurora\n", "settings", "set", "followup.trusted", "--add", "100000002", "--layer", "repo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "100000002") {
		t.Errorf("fugaro.yaml does not carry the added account:\n%s", got)
	}
	// The prompt says what this edit does: it adds an account to a list, and a
	// sentence about turning a key on names neither. The confirmation shows it
	// before the prompt, so it is there whether the person types the name or
	// the write is refused.
	if !strings.Contains(out, "this widens who is trusted") {
		t.Errorf("the confirmation does not say what adding a name widens:\n%s", out)
	}
	if strings.Contains(out, "this turns a followup key on") {
		t.Errorf("adding a name is confirmed with the sentence for turning a key on:\n%s", out)
	}

	// Taking a name off the list narrows who can steer the run, so it is the
	// ordinary y/N write: a terminal answering y is enough, where the typed
	// form would refuse a y that is not the project's name.
	if out, _, err := executeStdin(t, "y\n", "settings", "set", "followup.trusted", "--remove", "100000001", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); strings.Contains(got, "100000001") {
		t.Errorf("fugaro.yaml still carries the removed account:\n%s", got)
	}
}

// TestSettingsSetAgentBudgetRaiseIsTyped: agent.max_budget_usd is a stage's own
// spend cap, so raising it is the widening D846 types — the file it would edit
// decides what every run this branch starts may spend — and the comparison is
// against what a run gets today, not against the file's own text: the project
// layer's defaults and the layer file itself are ceilings of the same kind one
// door along.
func TestSettingsSetAgentBudgetRaiseIsTyped(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("agent:\n  max_budget_usd: 30\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "40", "--layer", "repo")
	if err == nil {
		t.Fatal("a raised agent budget was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "agent.max_budget_usd", "40", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "max_budget_usd: 40") {
		t.Errorf("fugaro.yaml does not carry the raised budget:\n%s", got)
	}

	// The project layer's own defaults are the same cap for every repository
	// the layer templates, and the layer file holds one: raising it is typed.
	settingsWriteLayerFile(t, "defaults:\n  agent:\n    max_budget_usd: 30\n")
	noTerminal(t)
	_, _, err = execute(t, "settings", "set", "layer.defaults.agent.max_budget_usd", "40", "--layer", "project")
	if err == nil {
		t.Fatal("a raised layer default for the agent budget was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}

	// And what a run gets is the resolved value, not the file being edited: a
	// repository's file that writes no cap of its own is typed too when the
	// layer below already holds one.
	settingsWriteCheckout(t, settingsRepoYAML(""))
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp",
		Bucket:     "fugaro-runs-aurora-gcp",
		CheckedAt:  time.Now(),
		YAML:       "version: 1\nproject: aurora\ngcp_project: aurora-gcp\ndefaults:\n  agent:\n    max_budget_usd: 30\n",
	}); err != nil {
		t.Fatal(err)
	}
	noTerminal(t)
	_, _, err = execute(t, "settings", "set", "agent.max_budget_usd", "40", "--layer", "repo")
	if err == nil {
		t.Fatal("a budget that raises what a run gets was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
}

// TestSettingsSetAgentTokensZeroIsTyped: agent.max_run_tokens is a whole run's
// token ceiling, and 0 is not a small cap but no cap at all (`config.Agent`:
// "0 is none"), so writing it is the act `unset` is under another name — the
// typed form whatever it replaces, a file that holds no cap yet included.
func TestSettingsSetAgentTokensZeroIsTyped(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("agent:\n  max_run_tokens: 300000\n"))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "agent.max_run_tokens", "0", "--layer", "repo")
	if err == nil {
		t.Fatal("a token cap was removed with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	_, _, err = execute(t, "settings", "unset", "agent.max_run_tokens", "--layer", "repo")
	if err == nil {
		t.Fatal("a token cap was unset with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("a refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "agent.max_run_tokens", "0", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "max_run_tokens: 0") {
		t.Errorf("fugaro.yaml does not carry the removed cap:\n%s", got)
	}

	// A file that holds no cap at all is the same act: no cap is the loosest a
	// ceiling can be written, so a first write of 0 is typed as well.
	fresh := settingsWriteCheckout(t, settingsRepoYAML(""))
	beforeFresh := readFileString(t, fresh)
	noTerminal(t)
	_, _, err = execute(t, "settings", "set", "agent.max_run_tokens", "0", "--layer", "repo")
	if err == nil {
		t.Fatal("a first write of 0 was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, fresh) != beforeFresh {
		t.Error("the refused write changed fugaro.yaml")
	}
}

// TestSettingsSetAgentBudgetLowerIsNotTyped: lowering an agent spend cap takes
// spend away, so it is the ordinary write — a terminal answering y is enough,
// where the typed form refuses a y that is not the project's name. It is the
// other half of the ruling that makes a raise the typed form: the comparison is
// a comparison and not "always typed".
func TestSettingsSetAgentBudgetLowerIsNotTyped(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("agent:\n  max_budget_usd: 30\n"))
	fakeTerminal(t)
	if out, _, err := executeStdin(t, "y\n", "settings", "set", "agent.max_budget_usd", "20", "--layer", "repo"); err != nil {
		t.Fatalf("a lowered agent budget is the ordinary write: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "max_budget_usd: 20") {
		t.Errorf("fugaro.yaml does not carry the lowered budget:\n%s", got)
	}

	// The project layer's own defaults are the same cap, and lowering one is
	// the ordinary write there too.
	settingsWriteLayerFile(t, "defaults:\n  agent:\n    max_budget_usd: 30\n")
	noTerminal(t)
	if out, _, err := execute(t, "settings", "set", "layer.defaults.agent.max_budget_usd", "20", "--layer", "project", "--yes"); err != nil {
		t.Fatalf("a lowered layer default is the ordinary write: %v\n%s", err, out)
	}
}

// TestSettingsSetAgentBudgetRaiseFromTheBuiltInDefaultIsTyped: a repository that
// writes no budget of its own does not start without one — the agent block's
// built-in default is config.DefaultMaxBudgetUSD, and it is a ceiling of the
// same kind as a number written in a file. Raising what no layer has written
// yet is therefore the widening D846 types, and the read answering "" for a key
// whose winning layer is the default is what used to let it through as a first
// write: an agent could name its own spend cap 40 times the one it started
// under, from a file it may edit anyway. The layer's own spelling of the same
// cap is filed under the same default.
func TestSettingsSetAgentBudgetRaiseFromTheBuiltInDefaultIsTyped(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)
	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "1000", "--layer", "repo")
	if err == nil {
		t.Fatal("a budget raised from the built-in default was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	// The layer file's own defaults are the same cap, and a layer file that
	// carries none is under the same built-in one.
	settingsWriteLayerFile(t, "")
	noTerminal(t)
	_, _, err = execute(t, "settings", "set", "layer.defaults.agent.max_budget_usd", "1000", "--layer", "project")
	if err == nil {
		t.Fatal("a layer default raised from the built-in default was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}

	// Lowering below the built-in default takes spend away, so it stays the
	// ordinary write: a terminal answering y is enough, where the typed form
	// refuses a y that is not the project's name. The base is the default and
	// not "", or every first write would read as a raise.
	fakeTerminal(t)
	if out, _, err := executeStdin(t, "y\n", "settings", "set", "agent.max_budget_usd", "10", "--layer", "repo"); err != nil {
		t.Fatalf("a budget lowered below the built-in default is the ordinary write: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "max_budget_usd: 10") {
		t.Errorf("fugaro.yaml does not carry the lowered budget:\n%s", got)
	}
}

// TestSettingsSetAgentBudgetRaiseFromTheDefaultRefusedInAnAgentSession: the
// built-in default is a ceiling too, so the raise the test above refuses for a
// session with no terminal is refused in a coding agent's session outright —
// --yes included, which is where the widening would otherwise land without
// anyone having read it.
func TestSettingsSetAgentBudgetRaiseFromTheDefaultRefusedInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "1000", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("a budget raised from the built-in default was written in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the variable to unset: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro settings set") {
		t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the refusal does not name the file it refused to write:\n%s", out)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed in a coding agent's session")
	}
}

// TestSettingsSetTheNewTypedRowsAreRefusedInAnAgentSession: the fence SD9 puts
// around a coding agent's session belongs to the file, not to the edit — it
// comes before the confirmation, and --yes never lifts it. A row added to the
// typed directions is one more edit that fence has to hold, and the loop is over
// agentenv.Markers itself rather than a copy, so a marker the tree adds is one
// these rows are held to. Every case names a repository's own file, the one a
// session may otherwise edit: without the typed form each of them would land.
func TestSettingsSetTheNewTypedRowsAreRefusedInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	trusted := "followup:\n  trusted:\n    - \"100000001\"\n"
	tokens := "agent:\n  max_run_tokens: 300000\n"
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			for _, c := range []struct {
				name string
				args []string
				yaml string
			}{
				{"a name added to followup.trusted", []string{"settings", "set", "followup.trusted", "--add", "100000002", "--layer", "repo", "--yes"}, trusted},
				{"an agent budget raised", []string{"settings", "set", "agent.max_budget_usd", "1000", "--layer", "repo", "--yes"}, ""},
				{"an agent token cap taken to 0", []string{"settings", "set", "agent.max_run_tokens", "0", "--layer", "repo", "--yes"}, tokens},
				{"an agent token cap unset", []string{"settings", "unset", "agent.max_run_tokens", "--layer", "repo", "--yes"}, tokens},
			} {
				t.Run(c.name, func(t *testing.T) {
					path := settingsWriteCheckout(t, settingsRepoYAML(c.yaml))
					before := readFileString(t, path)
					t.Setenv(marker, "1")

					out, _, err := execute(t, c.args...)
					if err == nil {
						t.Fatal("the write landed in a coding agent's session")
					}
					if !strings.Contains(err.Error(), marker) {
						t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
					}
					if !strings.Contains(err.Error(), "nothing was written") {
						t.Errorf("the refusal does not say nothing was written: %v", err)
					}
					// The removal spells its diff with a - line and the writes
					// with a + one, so the test looks for either.
					if !strings.Contains(out, path) || (!strings.Contains(out, "  + ") && !strings.Contains(out, "  - ")) {
						t.Errorf("the refusal does not print the file and the diff:\n%s", out)
					}
					if readFileString(t, path) != before {
						t.Error("the file changed in a coding agent's session")
					}
				})
			}
		})
	}
}

// TestSettingsSetWritesMode0600AndLeavesNoTemporaryBehind: the new file is the
// old file's permissions and the rename is atomic, so no half-written document
// and no temporary is ever visible beside it (spec §4.2 step 5).
func TestSettingsSetWritesMode0600AndLeavesNoTemporaryBehind(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	// The file the person left at 0644 is the one to watch: the write is a new
	// file renamed over it, so the mode is the write path's 0600 whatever the
	// old one was and the inode is the new file's, not the old one's truncated
	// (M29: os.WriteFile in place keeps both).
	if err := os.Chmod(localPath, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := execute(t, "settings", "set", "commit_name", "person-1", "--layer", "local", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fi, err := os.Stat(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the written file's mode is %o, want 600", perm)
	}
	if os.SameFile(before, fi) {
		t.Error("the write truncated the file in place; the write path renames a new file over it")
	}
	left, err := filepath.Glob(filepath.Join(filepath.Dir(localPath), ".config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("the write left temporaries beside the file: %v", left)
	}
}

// TestSettingsSetNoOpOnADocumentThatDoesNotReadBack: the reparse holds what a
// write would produce, so a command asked for no change at all is not run
// through it. The document below is one this Fugaro refuses to read back, and
// the test shows that first: the same document asked for a real change is
// refused by the reparse. The no-op then reports the no-op, and the file is
// left as it was — a write that reports a file's own state as the fault of an
// edit is worse than no answer, because the person is sent to fix a file
// nothing asked to write.
func TestSettingsSetNoOpOnADocumentThatDoesNotReadBack(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, "governance:\n  review_ratio: 1\nnonsense: 1\n")
	before := readFileString(t, path)

	_, _, err := execute(t, "settings", "set", "layer.governance.review_ratio", "2", "--layer", "project", "--yes")
	if err == nil || !strings.Contains(err.Error(), "would not read back") {
		t.Fatalf("a change to a document that does not read back = %v, want the reparse refusal", err)
	}

	out, _, err := execute(t, "settings", "set", "layer.governance.review_ratio", "1", "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("a no-op on a document that does not read back: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already reads what was asked for") {
		t.Errorf("the no-op answers something else:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the no-op changed the layer file:\n%s", got)
	}

	// The unset path holds the same order: a key that is not in the document
	// is not a write, so the document's own state is not reported as this
	// command's failure.
	_, _, err = execute(t, "settings", "unset", "layer.governance.max_subagents", "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("unsetting a key the document does not carry: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the no-op unset changed the layer file:\n%s", got)
	}
}

// TestSettingsValueInRendersASequenceTheWayTheListComparisonReadsOne: a
// sequence is the model list, and it is rendered as the members
// settingsListSubset splits on — not as fmt.Sprint's "[a b]", a form no parser
// here reads, which would make every member of a written list read as absent
// and so make every write a widening.
func TestSettingsValueInRendersASequenceTheWayTheListComparisonReadsOne(t *testing.T) {
	data := []byte("budget:\n  allowed_models:\n    - claude-opus-5-5\n    - claude-sonnet-5\n")
	got := settingsValueIn(data, []string{"budget", "allowed_models"})
	if got != "claude-opus-5-5,claude-sonnet-5" {
		t.Fatalf("settingsValueIn = %q, want the members comma-separated", got)
	}
	if settingsLoosens("budget.allowed_models", got, "claude-opus-5-5") {
		t.Error("a list narrower than the file's reads as a widening")
	}
	if !settingsLoosens("budget.allowed_models", got, "claude-opus-5-5,claude-haiku-4-5") {
		t.Error("a list wider than the file's does not read as a widening")
	}
}

// TestSettingsCeilingBaseIsNeverLooserThanTheFile: the base a ceiling write is
// measured against is the tighter of the file's own value and what a run gets
// today, and the file is always one of the two. Here no layer below resolves a
// cap at all — the state a read that could not see a layer leaves behind — so
// the file is the whole of the base: 60 raises it and is the typed form, 40
// tightens it and is the ordinary write. A base that fell back to "no layer
// resolves one" would let the first write through untyped.
func TestSettingsCeilingBaseIsNeverLooserThanTheFile(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 50\n"))

	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "60", "--layer", "repo")
	if err == nil {
		t.Fatal("a cap that raises the file's own ceiling was written with no terminal and no --yes")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("the refused write changed fugaro.yaml:\n%s", got)
	}

	if out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "40", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 40") {
		t.Errorf("fugaro.yaml does not carry the tightened cap:\n%s", got)
	}
}

// TestSettingsLoosensReadsZeroAsNoCap: 0 is not a small cap, it is no cap at
// all (`config.Budget`: "0 sets none"; the governance block's own rule is "0
// and null mean unset"), so writing it loosens whatever it replaces — and a
// write over a 0 is a tightening however the number rises, because any cap is
// tighter than none.
func TestSettingsLoosensReadsZeroAsNoCap(t *testing.T) {
	for _, c := range []struct {
		old, new string
		want     bool
	}{
		{"3", "0", true},
		{"", "0", true},
		{"0", "3", false},
		{"3", "5", true},
		{"5", "3", false},
		{"", "5", false},
	} {
		if got := settingsLoosens("budget.per_run_usd", c.old, c.new); got != c.want {
			t.Errorf("settingsLoosens(%q, %q) = %v, want %v", c.old, c.new, got, c.want)
		}
	}
}

// TestSettingsSetTypesAWriteThatRemovesACap: the command-line half of the same
// rule, over every ceiling key at every layer it may be written at. Writing 0
// has exactly the effect `unset` has, and `unset` is the typed form, so a
// write of 0 is refused without the project's own name however it is spelled,
// so with no terminal and no --yes the file is left as it was.
func TestSettingsSetTypesAWriteThatRemovesACap(t *testing.T) {
	cases := []struct {
		key, layer, cap string
	}{
		// The repository's own block: what a repository's runs read.
		{"budget.per_run_usd", "repo", "3"},
		{"governance.review_ratio", "repo", "3"},
		{"governance.review_floor_calls", "repo", "3"},
		{"governance.max_subagents", "repo", "3"},
		// The local file: the ceiling the deployed job carries.
		{"budget.per_run_usd", "local", "3"},
		{"budget.max_run_tokens", "local", "300000"},
		// The project layer's own block (SD14), which is the same keys.
		{"layer.governance.review_ratio", "project", "3"},
		{"layer.governance.max_subagents", "project", "3"},
	}
	for _, c := range cases {
		t.Run(c.key+"/"+c.layer, func(t *testing.T) {
			_, localPath := settingsWriteHome(t)
			layerPath := settingsWriteLayerFile(t, "")
			repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))
			path := localPath
			switch c.layer {
			case "project":
				path = layerPath
			case "repo":
				path = repoPath
			}

			// A cap where no layer held one is the ordinary write: it takes
			// nothing away, so it is not the typed form.
			if out, _, err := execute(t, "settings", "set", c.key, c.cap, "--layer", c.layer, "--yes"); err != nil {
				t.Fatalf("the first write of a cap: %v\n%s", err, out)
			}
			before := readFileString(t, path)

			noTerminal(t)
			_, _, err := execute(t, "settings", "set", c.key, "0", "--layer", c.layer)
			if err == nil {
				t.Fatal("a cap was removed with no terminal and no --yes")
			}
			if !strings.Contains(err.Error(), "typed confirmation") {
				t.Errorf("the refusal does not name the typed form: %v", err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}

			// And the typed name still does it: the refusal is a confirmation,
			// not a wall.
			fakeTerminal(t)
			if out, _, err := executeStdin(t, "aurora\n", "settings", "set", c.key, "0", "--layer", c.layer); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := readFileString(t, path); !strings.Contains(got, "0") {
				t.Errorf("the confirmed write did not land:\n%s", got)
			}
		})
	}
}
