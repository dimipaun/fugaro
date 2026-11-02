package cli

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// settingsAimHome is the aim tests' fixture: a config home holding two
// installations and nothing that selects one — no FUGARO_PROJECT, no
// FUGARO_CONFIG, no coding-agent marker — so only a command's own --project and
// --config can say which installation it acts on. Each config carries its own
// max_parallel, so a read or a write that landed in the other file is visible in
// the value rather than in a path a test would have to guess at.
func settingsAimHome(t *testing.T) (first, second string) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	isolateProjects(t, t.TempDir())
	first = settingsAimParallel(t, writeProject(t, "aurora", "aurora-gcp"), 3)
	second = settingsAimParallel(t, writeProject(t, "basalt", "basalt-gcp"), 7)
	return first, second
}

// settingsAimParallel writes one distinguishing key into a project config and
// returns its path.
func settingsAimParallel(t *testing.T, path string, parallel int) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("max_parallel: "+strconv.Itoa(parallel)+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSettingsAimedAtAnInstallationReadsThatInstallationsFile: with two
// installations and nothing in the environment selecting one, --project says
// which one the read is about, and the document says it back — the rows it
// prints are the named installation's file and no other's. Without the flag
// this is the "several project configs" refusal, which is the failure the
// console cannot recover from: it needs the read aimed, not guessed.
func TestSettingsAimedAtAnInstallationReadsThatInstallationsFile(t *testing.T) {
	_, second := settingsAimHome(t)

	doc := readSettingsJSON(t, "max_parallel", "--project", "basalt")
	if doc.Project != "basalt" {
		t.Errorf("the aimed read reports project %q, want %q", doc.Project, "basalt")
	}
	if got := *rowOf(t, doc, "max_parallel", "project").Value; got != "7" {
		t.Errorf("max_parallel = %q, want %q: the read did not read the named installation's config", got, "7")
	}
	var local string
	for _, s := range doc.Sources {
		if s.Layer == "local" {
			local = s.Note
		}
	}
	if local != second {
		t.Errorf("the local layer was read from %q, want %q", local, second)
	}
}

// TestSettingsSetAimedAtAnInstallationWritesThatFile: the flag decides which
// file a write lands in. The named installation's config is edited and the
// other one's is byte-identical afterwards — the failure this guards against is
// not an error but a silent write into the wrong installation, which on a
// machine with one config would have looked like it worked.
func TestSettingsSetAimedAtAnInstallationWritesThatFile(t *testing.T) {
	first, second := settingsAimHome(t)
	beforeFirst := readFileString(t, first)

	out, _, err := execute(t, "settings", "set", "max_parallel", "9", "--layer", "local", "--project", "basalt", "--yes")
	if err != nil {
		t.Fatalf("the aimed write was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, second); !strings.Contains(got, "max_parallel: 9") {
		t.Errorf("the named installation's config does not carry the write:\n%s", got)
	}
	if got := readFileString(t, first); got != beforeFirst {
		t.Errorf("the write reached the installation that was not named:\n%s", got)
	}

	// --config aims the same way, and wins over --project where the two agree.
	if _, _, err := execute(t, "settings", "set", "max_parallel", "11", "--layer", "local", "--project", "basalt", "--config", second, "--yes"); err != nil {
		t.Fatalf("the write aimed by --config was refused: %v", err)
	}
	if got := readFileString(t, second); !strings.Contains(got, "max_parallel: 11") {
		t.Errorf("the config --config named does not carry the write:\n%s", got)
	}
	if got := readFileString(t, first); got != beforeFirst {
		t.Errorf("the write aimed by --config reached the other installation:\n%s", got)
	}
}

// TestSettingsAimedAtAnUnknownInstallationIsRefused: an aim that names nothing
// is refused, on the read and on the write, with the name that was given. It
// may never fall through to whatever else the machine would have selected: a
// write aimed at a name this machine does not have is a write aimed nowhere.
func TestSettingsAimedAtAnUnknownInstallationIsRefused(t *testing.T) {
	first, second := settingsAimHome(t)
	beforeFirst, beforeSecond := readFileString(t, first), readFileString(t, second)

	_, _, err := execute(t, "settings", "max_parallel", "--project", "nimbus", "--json")
	if err == nil {
		t.Fatal("the read accepted --project nimbus, which is no installation on this machine")
	}
	if !strings.Contains(err.Error(), "nimbus") {
		t.Errorf("the refusal does not name the installation that was asked for: %v", err)
	}

	_, _, err = execute(t, "settings", "set", "max_parallel", "9", "--layer", "local", "--project", "nimbus", "--yes")
	if err == nil {
		t.Fatal("the write accepted --project nimbus, which is no installation on this machine")
	}
	if !strings.Contains(err.Error(), "nimbus") {
		t.Errorf("the write's refusal does not name the installation that was asked for: %v", err)
	}
	if readFileString(t, first) != beforeFirst || readFileString(t, second) != beforeSecond {
		t.Error("the refused write changed a config")
	}
}

// TestSettingsAimedAtTwoInstallationsIsRefused: --project and --config name one
// installation between them, so the two that disagree are refused with both
// names rather than resolved by a precedence the person cannot see. The pair
// that agrees is the one that works.
func TestSettingsAimedAtTwoInstallationsIsRefused(t *testing.T) {
	_, second := settingsAimHome(t)

	_, _, err := execute(t, "settings", "max_parallel", "--project", "aurora", "--config", second, "--json")
	if err == nil {
		t.Fatal("the read resolved --project aurora with a --config naming basalt")
	}
	for _, want := range []string{"aurora", "basalt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	doc := readSettingsJSON(t, "max_parallel", "--project", "basalt", "--config", second)
	if got := *rowOf(t, doc, "max_parallel", "project").Value; got != "7" {
		t.Errorf("max_parallel = %q, want %q: the agreeing pair was not resolved by --config", got, "7")
	}
}

// TestSettingsAimDoesNotChangeTheUnaimedRead: the two flags are the whole of
// P1's change to how a settings command resolves its installation. With neither
// of them the command resolves exactly as it did before: the environment
// selects, and with nothing selecting at all the read refuses rather than
// choosing among the configs on this machine.
func TestSettingsAimDoesNotChangeTheUnaimedRead(t *testing.T) {
	settingsAimHome(t)

	_, _, err := execute(t, "settings", "max_parallel", "--json")
	if err == nil {
		t.Fatal("the unaimed read picked one of two installations")
	}
	for _, want := range []string{"aurora", "basalt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not list %q among this machine's installations: %v", want, err)
		}
	}

	t.Setenv("FUGARO_PROJECT", "basalt")
	doc := readSettingsJSON(t, "max_parallel")
	if doc.Project != "basalt" {
		t.Errorf("the unaimed read reports project %q, want %q: $FUGARO_PROJECT no longer selects", doc.Project, "basalt")
	}
	if got := *rowOf(t, doc, "max_parallel", "project").Value; got != "7" {
		t.Errorf("max_parallel = %q, want %q", got, "7")
	}
}
