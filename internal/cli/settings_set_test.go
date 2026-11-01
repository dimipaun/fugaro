package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
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
// ceiling key asks for the project's name rather than y/N, and --yes never
// stands in for the typed form (D846).
func TestSettingsSetLooseningACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)

	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed a loosened ceiling")
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
// same boundary as loosening, so --yes does not stand in for the typed name
// (spec §4.3).
func TestSettingsUnsetOfACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed the removal of a ceiling")
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
