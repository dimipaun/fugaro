package cli

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// setup_rigor_test.go is the rigor question's own tests (task W of
// docs/rigor-command/implementation-plan.md; D900, the owner's ruling of
// 2026-10-31).
//
// The question is one row of the wizard's table, and what these tests hold is
// what that row promises: the four modes printed with what each costs, no
// suggestion (Enter is not an answer), hotfix refused in the words the other
// door refuses it with, one error naming `--rigor <mode>` where nobody can be
// asked, and the answer WRITTEN AND PUBLISHED BY THE SAME RUN — which is the
// HARD RULE's half of the task: a person who answers the wizard's question owes
// no second command.
//
// The rig is the wizard's own (wizardRun, setup_wizard_test.go) for the
// questions, and R1's layer stage (newRigorStageRig, init_layer_stage_test.go)
// for the write: the answer reaches the file and the bucket through the stage
// `fugaro update` already publishes with, which is the whole point of writing
// it there.

// rigorStdin is the wizard's answers with the rigor question's own lines in
// place: every other row is wizardAnswers, and the rigor answer is the
// caller's. It is built from the table so that a question added to it — or
// asked in another order — cannot leave this harness answering the wrong one.
func rigorStdin(rigorAnswer string) string {
	var b strings.Builder
	for _, q := range setupQuestions {
		if q.flag == "--rigor" {
			b.WriteString(rigorAnswer)
			continue
		}
		b.WriteString(wizardAnswers[q.flag])
	}
	return b.String()
}

// rigorLine is the question's own line for one mode, or "" when it printed
// none. The lines are read off the output rather than compared whole, because
// what each says about review and rounds comes from the two tables that hold
// it (rigor/levels.go, rigor/rounds.go) and a test that spelled them out would
// be a second copy of those tables rather than a check of this question.
func rigorLine(out string, mode rigor.Mode) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  "+string(mode)+": ") {
			return line
		}
	}
	return ""
}

// TestSetupAsksTheRigorModeWithNoSilentDefault is the plan's red step (task W):
// the wizard asks the project's rigor mode, prints the four modes with what
// each costs and what review it runs, recommends `pro` by listing it first, and
// takes NO suggestion — Enter is not an answer, and the mode a person walks
// away from is not chosen for them.
func TestSetupAsksTheRigorModeWithNoSilentDefault(t *testing.T) {
	// The four modes in the question's own order, with an empty line first: the
	// wizard asks again rather than taking a default nobody typed.
	r, out := wizardRun(t, rigorStdin("\ncritical\n"), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	printed := out.String()

	if !strings.Contains(printed, rigorQuestionLead) {
		t.Errorf("the wizard does not print the rigor question in its own words:\n%s", printed)
	}
	// Every mode the answer takes is offered, and each line says what the mode
	// runs and what it spends — read off the tables, so a mode whose review or
	// rounds changed is a line that changed.
	for _, mode := range []rigor.Mode{rigor.Pro, rigor.Startup, rigor.POC, rigor.Critical} {
		line := rigorLine(printed, mode)
		if line == "" {
			t.Errorf("the question does not offer %s:\n%s", mode, printed)
			continue
		}
		levels, err := rigor.DefaultLevels(mode)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, rigorReviewWords(levels)) || !strings.Contains(line, rigorRoundWords(rigor.DefaultRounds(mode))) {
			t.Errorf("%s: the line does not say what the mode runs and what it spends: %q", mode, line)
		}
	}
	// The recommendation is the order: pro first, and it is first because it is
	// what a project that says nothing runs today (D19).
	if at := strings.Index(printed, "  pro: "); at < 0 {
		t.Errorf("the question does not offer pro:\n%s", printed)
	} else {
		for _, after := range []rigor.Mode{rigor.Startup, rigor.POC, rigor.Critical} {
			if i := strings.Index(printed, "  "+string(after)+": "); i >= 0 && i < at {
				t.Errorf("%s is listed above pro, and pro is the recommended answer:\n%s", after, printed)
			}
		}
		if !strings.Contains(rigorLine(printed, rigor.Pro), "built-in default") {
			t.Errorf("pro's line does not say why it is the recommendation:\n%s", printed)
		}
	}
	// hotfix is not a mode a layer sets (D834), so it is not one this question
	// offers: it is off the axis, and the four above are the whole answer.
	if rigorLine(printed, rigor.Hotfix) != "" {
		t.Errorf("the question offers hotfix, which no layer may set:\n%s", printed)
	}
	// The red step's own assertion: an empty line is refused and the question is
	// asked again.
	if !strings.Contains(printed, "  a value is needed") {
		t.Errorf("Enter answered the rigor question, and this is the one question of the wizard with no suggestion:\n%s", printed)
	}
	if r.o.rigor != string(rigor.Critical) {
		t.Errorf("after the run the answer is %q, want critical: the value typed after the refusal", r.o.rigor)
	}
}

// TestSetupRigorAnswerRefusesANearMiss: the answer is read by the closed
// vocabulary (rigor.Parse, through setupRigorMode), and everything outside it
// is refused in the words `fugaro run --mode` refuses it with — the list is
// rigor.ModeList's one spelling (F8), so the wizard and the runner name the
// same modes.
func TestSetupRigorAnswerRefusesANearMiss(t *testing.T) {
	r, out := wizardRun(t, rigorStdin("Pro\npro\n"), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	printed := out.String()
	want := "not a rigor mode: use " + rigor.ModeList()
	if !strings.Contains(printed, want) {
		t.Errorf("a near miss is not refused in the vocabulary's own words (%q):\n%s", want, printed)
	}
	if !strings.Contains(printed, rigor.ModeList()) {
		t.Errorf("the refusal does not name the modes that are accepted:\n%s", printed)
	}
	if r.o.rigor != string(rigor.Pro) {
		t.Errorf("after the run the answer is %q, want pro: the value typed after the refusal", r.o.rigor)
	}
}

// TestSetupRigorRefusesHotfix: hotfix is in the vocabulary and is not an
// answer to this question (D834, D12). The sentence is the one the other door
// gives it — `fugaro rigor set hotfix` — read from that command here rather
// than spelled a second time, because two doors to one refusal that drift are
// two refusals.
func TestSetupRigorRefusesHotfix(t *testing.T) {
	want := runRigorSet(&cobra.Command{}, "hotfix", rigorOptions{}).Error()
	if want == "" {
		t.Fatal("fugaro rigor set hotfix was not refused, so this test holds nothing")
	}
	r, out := wizardRun(t, rigorStdin("hotfix\npro\n"), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	printed := out.String()
	if !strings.Contains(printed, want) {
		t.Errorf("the wizard refuses hotfix in other words than `fugaro rigor set` does:\nwant %q\n%s", want, printed)
	}
	if !strings.Contains(printed, "fugaro run --mode hotfix") {
		t.Errorf("the refusal does not name the one door that reaches hotfix:\n%s", printed)
	}
	if rigorLine(printed, rigor.Hotfix) != "" {
		t.Errorf("the question offers hotfix beside the modes it accepts:\n%s", printed)
	}
	if r.o.rigor != string(rigor.Pro) {
		t.Errorf("after the run the answer is %q, want pro: the value typed after the refusal", r.o.rigor)
	}
}

// TestSetupWithoutATerminalIsRefusedWithTheRigorQuestion: every run that cannot
// ask — a pipe, --json, --yes, --non-interactive — is refused with the one
// error that names the flags answering the questions, and the rigor row is in
// it as `--rigor <mode>`.
//
// The mode is NOT taken for the caller: a scripted setup is exactly where a
// default nobody typed would be least visible, so the run stops and names the
// flag (D900).
func TestSetupWithoutATerminalIsRefusedWithTheRigorQuestion(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    *initOptions
	}{
		{"a pipe", &initOptions{}},
		{"--json", &initOptions{asJSON: true}},
		{"--yes", &initOptions{yes: true}},
		{"--non-interactive", &initOptions{nonInteractive: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstRunEnv(t)
			noTerminal(t)
			tc.o.door = setupDoor.name
			r := &initRun{o: tc.o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			err := r.gatherInputs(t.Context())
			if err == nil {
				t.Fatal("a first run that cannot ask its questions and was given no --rigor exited 0")
			}
			if !strings.Contains(err.Error(), questionFor("--rigor").missing) {
				t.Errorf("the refusal does not name %q: %v", questionFor("--rigor").missing, err)
			}
			if tc.o.rigor != "" {
				t.Errorf("the run chose the mode %q for a caller nobody could ask, want none", tc.o.rigor)
			}
		})
	}
}

// TestSetupYesNeverChoosesTheRigorMode is the flag that looks most like an
// answer: --yes covers the confirmations, and the rigor question is not one of
// them. A run that says yes to everything is still refused by name, and the
// project keeps no mode of its own.
func TestSetupYesNeverChoosesTheRigorMode(t *testing.T) {
	firstRunEnv(t)
	fakeTerminal(t)
	o := &initOptions{door: setupDoor.name, yes: true}
	r := &initRun{o: o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
	err := r.gatherInputs(t.Context())
	if err == nil {
		t.Fatal("fugaro setup --yes created a project without an answer to the rigor question")
	}
	if !strings.Contains(err.Error(), "--rigor <mode>") {
		t.Errorf("the refusal does not name the flag that answers the question: %v", err)
	}
	if o.rigor != "" {
		t.Errorf("--yes chose the rigor mode %q, want none: it is a confirmation, not an answer", o.rigor)
	}
}

// TestSetupRigorAnswerShowsTheValueItReplaces: the answer is written over the
// project layer's own `defaults.rigor.default`, and the write path prints the
// line it replaces beside the line it writes (settings_write.go). A teammate's
// mode is never replaced unseen, and the confirmation is made against a diff
// that names both values.
func TestSetupRigorAnswerShowsTheValueItReplaces(t *testing.T) {
	local := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  rigor:\n    default: pro\n"
	r := newRigorStageRig(t, "", local, "", string(rigor.Critical))
	if _, err := r.s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out)
	}
	var removed, added []string
	for _, line := range strings.Split(r.out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "  - "):
			removed = append(removed, strings.TrimPrefix(line, "  - "))
		case strings.HasPrefix(line, "  + "):
			added = append(added, strings.TrimPrefix(line, "  + "))
		}
	}
	if len(removed) != 1 || removed[0] != "    default: pro" {
		t.Errorf("the diff does not show the value it replaces: removed %q\n%s", removed, r.out)
	}
	if len(added) != 1 || added[0] != "    default: critical" {
		t.Errorf("the diff does not show the value it writes: added %q\n%s", added, r.out)
	}
}

// TestSetupRigorAnswerKeepsTheRestOfTheFile: the answer is one scalar spliced
// into the document that is there (yamledit.SetScalar), not a file rewritten
// from a template — the comments, the order and every other key of the owner's
// layer file survive it.
func TestSetupRigorAnswerKeepsTheRestOfTheFile(t *testing.T) {
	local := "# the project layer of aurora, edited by hand\nversion: 1\nproject: aurora\ngcp_project: proj-1234\ngovernance: { review_ratio: 20 }\ndefaults:\n  rigor:\n    default: pro\n  agent:\n    review_rounds: 4\n"
	r := newRigorStageRig(t, "", local, "", string(rigor.Critical))
	if _, err := r.s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out)
	}
	got := readFileString(t, localLayerPath(t))
	before, after := strings.Split(local, "\n"), strings.Split(got, "\n")
	if len(before) != len(after) {
		t.Fatalf("the write changed the file's shape: %d lines became %d\n%s", len(before), len(after), got)
	}
	changed := 0
	for i := range before {
		if before[i] == after[i] {
			continue
		}
		changed++
		if before[i] != "    default: pro" || after[i] != "    default: critical" {
			t.Errorf("line %d changed and it is not the rigor default: %q became %q", i+1, before[i], after[i])
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want the one the answer is written to:\n%s", changed, got)
	}
	for _, keep := range []string{"# the project layer of aurora", "review_ratio: 20", "review_rounds: 4"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the write dropped %q from the file:\n%s", keep, got)
		}
	}
}

// TestSetupRigorAnswerLeavesNoSecondCommandWhenDeclined: the write is this
// machine's file and the publish is the cloud step the confirmation belongs to,
// so a run whose confirmation is not given leaves the file WRITTEN and
// UNPUBLISHED. That is a state of its own, and it is not a state that costs a
// second command: the stage's next step is `fugaro update`, the one command
// this installation already has (the HARD RULE).
func TestSetupRigorAnswerLeavesNoSecondCommandWhenDeclined(t *testing.T) {
	r := newRigorStageRig(t, "", "", "", string(rigor.Critical))
	ctx := t.Context()

	// No confirmation: no terminal on the run's input, and no --yes.
	if _, err := r.s.Apply(ctx, initflow.Env{}); err == nil {
		t.Fatalf("the publish was made without a confirmation:\n%s", r.out)
	}
	if got := readFileString(t, localLayerPath(t)); !strings.Contains(got, "default: critical") {
		t.Errorf("the answer was not written before the confirmation:\n%s", got)
	}
	if _, ok := rigorObject(t, r); ok {
		t.Error("the run published the project layer without the confirmation")
	}

	// What the state is, in the words the next run reports it with, and the one
	// command that finishes it. The next run is `fugaro update`, which carries
	// no answer — the question belongs to the run that creates the installation
	// — so the answer is cleared before the stage is planned again.
	r.s.e.r.o.rigor = ""
	fresh := newLayerStage(&initEngine{r: r.s.e.r, lc: r.s.e.lc, path: r.s.e.path})
	st, err := fresh.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(st.Detail, "unpublished") {
		t.Errorf("the state the declined run left is not the unpublished file it is: %q", st.Detail)
	}
	if left := fresh.Left(); left.Text != selfCommand()+" update" {
		t.Errorf("the next step is %q, want %q: a declined publish costs no command of its own", left.Text, selfCommand()+" update")
	}
}

// TestUpdateNeverAsksTheRigorQuestion: the question belongs to the run that
// CREATES the installation. A project that already has a config is asked
// nothing — its mode is the project's own, and it is read from the layer rather
// than typed again — and `--rigor` is refused by name rather than honoured as a
// second door to the same key, on both doors that converge.
func TestUpdateNeverAsksTheRigorQuestion(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "--yes", "--rigor", "critical"},
		{"update", "--yes", "--rigor", "critical"},
	} {
		t.Run(args[0], func(t *testing.T) {
			newInitRig(t)
			out, errOut, err := executeStdin(t, "", args...)
			if err == nil {
				t.Fatalf("%v answered the rigor question on a project that already exists:\n%s\n%s", args, out, errOut)
			}
			if !strings.Contains(err.Error(), "fugaro rigor set") {
				t.Errorf("the refusal does not name the one command that changes a project's default: %v", err)
			}
			if !strings.Contains(err.Error(), "--rigor") {
				t.Errorf("the refusal does not name the flag it refuses: %v", err)
			}
			for _, asked := range []string{rigorQuestionLead, questionFor("--rigor").what, "Rigor mode"} {
				if strings.Contains(out+errOut, asked) {
					t.Errorf("the run asked the rigor question (%q) on a project that already has a config:\n%s\n%s", asked, out, errOut)
				}
			}
		})
	}
}

// TestUpdateSummaryShowsTheResolvedMode: the review screen is where a run says
// what it is about to do, and the rigor mode is one of the facts it now states
// — the mode rigor.Resolve answers for the project, with the word that says
// where it came from, so a person reading the review can tell a mode this
// project set from the built-in default.
func TestUpdateSummaryShowsTheResolvedMode(t *testing.T) {
	screen := func(t *testing.T, layer string, answered string) string {
		t.Helper()
		firstRunEnv(t)
		if layer != "" {
			path := localLayerPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(layer), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var b strings.Builder
		r := &initRun{cmd: &cobra.Command{}, o: &initOptions{rigor: answered}, w: &b, projectName: initProjectName, gcpProject: initProject}
		e := &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName}, spec: infra.InstallationSpec{RunsBucket: initRunsBucket, StateBucket: initStateBucket}, adminsRead: true}
		e.reviewScreen(context.Background(), "converged by this run")
		return b.String()
	}

	// A project that says nothing runs the built-in default, and the line says
	// so rather than naming a mode the project never set.
	if got := screen(t, "", ""); !strings.Contains(got, "  rigor mode:     pro (default)\n") {
		t.Errorf("the review does not state the mode a project that says nothing runs:\n%s", got)
	}
	// A project whose layer sets one: the mode it set, and the word that says
	// where the answer came from.
	layer := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  rigor:\n    default: critical\n"
	if got := screen(t, layer, ""); !strings.Contains(got, "  rigor mode:     critical (project)\n") {
		t.Errorf("the review does not state the mode the project's layer sets:\n%s", got)
	}
	// The first run that just answered the question: the answer is the project's
	// mode, and the review a person reads before the confirmation says which.
	if got := screen(t, "", string(rigor.POC)); !strings.Contains(got, "  rigor mode:     poc (project)\n") {
		t.Errorf("the review does not state the mode the wizard was just answered with:\n%s", got)
	}
}

// TestSetupInACodingAgentsSessionWritesNothing: the write goes through the one
// settings write path (applySettingsEdit), whose coding-agent refusal is the
// fence SD9 put there — and the stage refuses before it, so a session under
// CLAUDECODE reaches neither the file nor the bucket, --yes included.
//
// The refusal is the stage's own, not the helper's: this stage can publish the
// object every repository of the project resolves against, so a coding agent's
// session is refused the step and not just the write (init_layer_stage.go's
// Apply). What the run prints in that session is the loop's plan, which is why
// nothing here is asserted about a diff: no diff is ever built.
func TestSetupInACodingAgentsSessionWritesNothing(t *testing.T) {
	agentSession(t)
	r := newRigorStageRig(t, "", "", "", string(rigor.Critical))
	_, err := r.s.Apply(t.Context(), initflow.Env{Yes: true})
	if err == nil {
		t.Fatalf("a coding agent's session wrote the project layer:\n%s", r.out)
	}
	var ae *initflow.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("the refusal is not the run's agent refusal: %v", err)
	}
	if ae.Marker == "" {
		t.Error("the refusal does not name the marker it read")
	}
	if _, err := os.Stat(localLayerPath(t)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the session wrote %s, want nothing written", localLayerPath(t))
	}
	if _, ok := rigorObject(t, r); ok {
		t.Error("the session published the project layer")
	}
}
