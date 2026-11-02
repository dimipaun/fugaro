package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
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

// settingsAimDisagreement runs one write whose two flags name different
// installations and holds it to the refusal. The write path resolves the pair
// through its own selection, so the read's refusal of the same pair says nothing
// about it: a write that kept only --config would edit basalt's file and report
// success, which is a silent write into an installation the person did not name
// — and the one thing the console's caller cannot recover from.
func settingsAimDisagreement(t *testing.T, op string, rest ...string) {
	t.Helper()
	first, second := settingsAimHome(t)
	beforeFirst, beforeSecond := readFileString(t, first), readFileString(t, second)

	argv := append([]string{"settings", op}, rest...)
	argv = append(argv, "--layer", "local", "--project", "aurora", "--config", second, "--yes")
	out, _, err := execute(t, argv...)
	if err == nil {
		t.Fatalf("%s resolved --project aurora with a --config naming basalt:\n%s", op, out)
	}
	for _, want := range []string{"aurora", "basalt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if readFileString(t, first) != beforeFirst || readFileString(t, second) != beforeSecond {
		t.Errorf("the refused %s changed a config", op)
	}
}

// TestSettingsSetAimedAtTwoInstallationsIsRefused: the write's own selection
// refuses the disagreeing pair, naming both. --yes is passed so that the
// selection is the only thing that can refuse: with the pair accepted, the write
// would go through and land in the installation --config names.
func TestSettingsSetAimedAtTwoInstallationsIsRefused(t *testing.T) {
	settingsAimDisagreement(t, "set", "max_parallel", "9")
}

// TestSettingsUnsetAimedAtTwoInstallationsIsRefused: `unset` resolves its
// installation through the same selection and is held to the same refusal —
// removing a key from whichever installation the environment happened to pick
// is the same silent write in the other direction.
func TestSettingsUnsetAimedAtTwoInstallationsIsRefused(t *testing.T) {
	settingsAimDisagreement(t, "unset", "max_parallel")
}

// TestSettingsAimedWriteInAnAgentSessionIsRefused: aiming a write does not lift
// the fence. The aim says which installation the edit is about; it says nothing
// about who is asking, and the refusal comes first with --yes answering nothing
// (D1107). The command the refusal prints carries the aim, because the person
// who pastes it at their own terminal has the same environment as the session
// that was refused: without the two flags the pasted command would edit whichever
// installation that environment selects. The loop is over agentenv.Markers itself
// rather than a copy, so a marker the tree adds is one this rule is held to.
//
// The config sits in a directory with a space in its name, so the printed
// command has to quote the path: a person copies that one line, and an unquoted
// path would arrive as two arguments.
func TestSettingsAimedWriteInAnAgentSessionIsRefused(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			first, second := settingsAimHome(t)
			beforeFirst, beforeSecond := readFileString(t, first), readFileString(t, second)

			dir := filepath.Join(t.TempDir(), "aimed installation")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			aimed := filepath.Join(dir, "basalt.yaml")
			if err := os.WriteFile(aimed, []byte(beforeSecond), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(marker, "1")

			_, _, err := execute(t, "settings", "set", "max_parallel", "9", "--layer", "local", "--project", "basalt", "--config", aimed, "--yes")
			if err == nil {
				t.Fatal("--yes lifted the agent-session fence for a write aimed at an installation")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "--project basalt") {
				t.Errorf("the printed command drops --project, so pasting it edits whichever installation this environment selects: %v", err)
			}
			if !strings.Contains(err.Error(), "--config '"+aimed+"'") {
				t.Errorf("the printed command does not carry --config %s as one quoted word: %v", aimed, err)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			if readFileString(t, first) != beforeFirst || readFileString(t, second) != beforeSecond || readFileString(t, aimed) != beforeSecond {
				t.Error("the refused write changed a config in a coding agent's session")
			}
		})
	}
}
