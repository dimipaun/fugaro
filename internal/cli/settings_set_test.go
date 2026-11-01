package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
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

// TestSettingsCeilingKeyCoversGovernanceAndTheLayerPrefix: D846's prefix test
// is `budget.*` and `governance.*`, and the project layer's own block is the
// same setting under the `layer.` prefix (SD14), so the typed name guards it
// too.
func TestSettingsCeilingKeyCoversGovernanceAndTheLayerPrefix(t *testing.T) {
	for _, key := range []string{
		"budget.per_run_usd", "budget.mode", "budget.allowed_models",
		"governance.review_ratio", "governance.stage_budget",
		"layer.governance.review_ratio",
	} {
		if !settingsCeilingKey(key) {
			t.Errorf("settingsCeilingKey(%q) = false, and D846 makes it a ceiling", key)
		}
	}
	for _, key := range []string{"git.base_branch", "agent.max_run_tokens", "followup.allow_public", "layer.defaults.git.provider"} {
		if settingsCeilingKey(key) {
			t.Errorf("settingsCeilingKey(%q) = true, and it is no ceiling", key)
		}
	}
}

// TestSettingsLoosensRanksABudgetMode: budget.mode is a ceiling whose values
// are ordered, not compared as numbers: enforce is the tightest, then observe,
// then off, and only a write that moves down the order is a loosening
// (policy.Merge's order).
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
	_, _, err := execute(t, "settings", "set", "governance.review_ratio", "0.9", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed a loosened governance ceiling")
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
// it is typed exactly as `governance.*` is, --yes included.
func TestSettingsSetTypedTheProjectLayersGovernanceBlock(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, "governance:\n  review_ratio: 0.5\n")
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "set", "layer.governance.review_ratio", "0.9", "--layer", "project", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed a loosened layer ceiling")
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
	for _, spelling := range []string{"true", "True", "TRUE", "yes", "Yes", "on"} {
		t.Run(spelling, func(t *testing.T) {
			_, _, err := execute(t, "settings", "set", "followup.allow_public", spelling, "--layer", "repo", "--yes")
			if err == nil {
				t.Fatalf("--yes confirmed followup.allow_public %s", spelling)
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
// it edits is not the only place the value comes from.
func TestSettingsSetComparesACeilingAgainstTheValueARunGets(t *testing.T) {
	settingsWriteHomeWith(t, "budget:\n  per_run_usd: 10\n")
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	// The repository's file writes no cap; the layer below holds 10, so 50
	// raises what a run gets and is the typed form.
	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed a cap that raises what a run gets above the layer below")
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
// ceiling is the typed form, --yes included (D846).
func TestSettingsSetAddOnACeilingAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  allowed_models:\n    - claude-sonnet-5\n"))
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "set", "budget.allowed_models", "--add", "claude-opus-5-5", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes confirmed a model added to a ceiling")
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
// key turned on widens who can steer a run, so the typed name confirms it and
// --yes does not stand in (D846).
func TestSettingsSetTurningAFollowupKeyOnAsksForTheTypedProjectName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)
	_, _, err := execute(t, "settings", "set", "followup.allow_public", "true", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatal("--yes turned a followup key on")
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

// TestSettingsSetWritesMode0600AndLeavesNoTemporaryBehind: the new file is the
// old file's permissions and the rename is atomic, so no half-written document
// and no temporary is ever visible beside it (spec §4.2 step 5).
func TestSettingsSetWritesMode0600AndLeavesNoTemporaryBehind(t *testing.T) {
	_, localPath := settingsWriteHome(t)
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
	left, err := filepath.Glob(filepath.Join(filepath.Dir(localPath), ".config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("the write left temporaries beside the file: %v", left)
	}
}
