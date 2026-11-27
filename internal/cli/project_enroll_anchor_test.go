package cli

// U9a's tests: `fugaro project repos add` is the door the gcp_project line is
// written through (R8, R14). The checkout the command deploys from is the one
// whose fugaro.yaml gets the line, the plan says so before the one question, and
// the edit is offered through writeAnchor's own gate — a person at the terminal
// and a coding agent's session are the arms every other caller of that gate
// already has, and this command adds no question of its own.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestReposAddPrintsTheAnchorLine is U9a's red step: the file this command used
// to promise never to write is one it writes a single line into, so the plan
// names that line under the step it belongs to — before the question, so the yes
// a person types is a yes to a line they have read.
//
// The second half is the state the line is already in: a checkout whose
// fugaro.yaml carries it (the operator committed what the first run left, which
// is the step between two runs) is offered nothing, and the plan says nothing
// about the anchor rather than offering an edit with nothing to change.
func TestReposAddPrintsTheAnchorLine(t *testing.T) {
	r := enrolRig(t)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	conf := strings.Index(out, "⚠ CONFIRM")
	if conf < 0 {
		t.Fatalf("the one confirmation was not asked:\n%s", out)
	}
	want := "writes gcp_project: " + initProject + " (so teammates use this installation without setup)"
	if plan := out[:conf]; !strings.Contains(plan, "\n     "+want+"\n") {
		t.Errorf("the plan's first step does not carry the anchor line %q:\n%s", want, plan)
	}
	// The first run leaves the line in the working tree, which is a checkout the
	// Repository stage of a second run refuses: landing it is the operator's own
	// step between the two, exactly as the note the write prints says.
	enrolLanded(t)
	second, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("the second run: %v\nstdout:\n%s\nstderr:\n%s", err, second, errOut)
	}
	if secondConf := strings.Index(second, "⚠ CONFIRM"); secondConf < 0 {
		t.Fatalf("the second run asked no confirmation:\n%s", second)
	} else if plan := second[:secondConf]; strings.Contains(plan, "writes gcp_project:") || strings.Contains(plan, "note: gcp_project") {
		t.Errorf("the second run offers an anchor line the checkout's fugaro.yaml already holds:\n%s", plan)
	}
}

// TestReposAddOffersTheAnchorThroughTheGate is R8's second half: the edit goes
// through writeAnchor's own gate and no new question. The two arms are the
// gate's own and one fixture differs by its stdin alone — a person at the
// terminal has read the line in the plan and the file is written without
// anything typed, and a pipe is handed the line to add and left alone. Neither
// is asked anything: the [y/N] the gate used to carry is gone (T7).
//
// The image checks are the ones the line's arrival runs (gcpProjectProblems),
// and they warn rather than refuse: the enrolment they came with is already
// made, and what they are about is a build record nothing here changes.
func TestReposAddOffersTheAnchorThroughTheGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
	}{
		{"a person at the terminal", true},
		{"a pipe", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := enrolRig(t)
			if tc.terminal {
				ownerAtTheTerminal(t)
			} else {
				noTerminal(t)
			}
			cmd := newProjectReposAddCmd()
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(""))
			path := enrolCheckoutFile(t)
			before := readFileString(t, path)

			if err := reposEnrolmentAnchorWrite(t.Context(), cmd, enrolAnchorLocal(t, r.cfg), enrolAnchorPlan(t, r)); err != nil {
				t.Fatalf("the anchor step failed: %v\n%s", err, out.String())
			}
			got := readFileString(t, path)
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("the anchor's write asked a question: the gate writes at a person's terminal without one\n%s", out.String())
			}
			if !strings.Contains(out.String(), "is not ready for gcp_project in fugaro.yaml") {
				t.Errorf("the write ran no image check:\n%s", out.String())
			}
			if tc.terminal {
				// The line lands where the plan said it would: next to project:,
				// which is where a teammate's CLI and a CI job read it from.
				if !strings.Contains(got, "project: aurora\ngcp_project: "+initProject+"\n") {
					t.Errorf("the checkout's fugaro.yaml did not gain the anchor line:\n%s", got)
				}
				if !strings.Contains(out.String(), "Updated "+path+" with gcp_project") {
					t.Errorf("the write does not say what it wrote:\n%s", out.String())
				}
				return
			}
			if got != before {
				t.Errorf("a session with no terminal wrote the line:\n--- before\n%s\n--- after\n%s", before, got)
			}
			if !strings.Contains(out.String(), "add this line to "+path) {
				t.Errorf("a session with no terminal is not handed the line to add:\n%s", out.String())
			}
		})
	}
}

// TestReposAddAnchorRefusedForAnAgent is the gate's third arm, and the one this
// door depends on the gate for: the anchor step is reached in an agent's session
// only through the gate, and the gate refuses it — the same refusal every other
// step of a run answers an agent with, before the file is touched. The command's
// own door carries no refusal of its own any more (D3425 lifted it), which is
// why this asserts the anchor's arm and nothing else.
func TestReposAddAnchorRefusedForAnAgent(t *testing.T) {
	r := enrolRig(t)
	ownerAtTheTerminal(t)
	cmd := newProjectReposAddCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(""))
	lc := enrolAnchorLocal(t, r.cfg)
	plan := enrolAnchorPlan(t, r)
	a, note, err := reposEnrolmentAnchor(lc, plan.checkout)
	if err != nil || a == nil || note != "" {
		t.Fatalf("the checkout offers no anchor line to refuse (a %v, note %q, err %v)", a, note, err)
	}
	path := enrolCheckoutFile(t)
	before := readFileString(t, path)

	t.Setenv(agentMarkers[0], "1")
	written, err := reposAnchorGate(cmd, &out).write(a)
	var refused *initflow.AgentError
	if !errors.As(err, &refused) {
		t.Fatalf("a coding agent's session got (%v, %v), want the gate's own refusal", written, err)
	}
	if written || readFileString(t, path) != before {
		t.Errorf("the line was written in a coding agent's session:\n%s", out.String())
	}
}

// TestReposAddAnchorLandsUnderNoApply is the anchor on the one path that runs
// no stage: the plan prints the line before the question either way, so the run
// that answered it makes the edit either way. --no-apply saves the publish and
// the deploy, which are the two steps that are `fugaro update`'s, and the
// anchor is not one of them — a person who enrolled with the flag and then ran
// `fugaro apply` would otherwise hold the enrolment and not the line.
func TestReposAddAnchorLandsUnderNoApply(t *testing.T) {
	r := enrolRig(t)
	out, errOut, err := r.enrol(t, "--no-apply", enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s --no-apply: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if !strings.Contains(out, "writes gcp_project: "+initProject) {
		t.Errorf("the plan under --no-apply does not print the line:\n%s", out)
	}
	got := readFileString(t, enrolCheckoutFile(t))
	if !strings.Contains(got, "gcp_project: "+initProject+"\n") {
		t.Errorf("--no-apply left the checkout without the line its plan printed:\n%s", got)
	}
	if owed := pendingSteps(record(t)); len(owed) == 0 {
		t.Errorf("--no-apply ran the steps it saves for fugaro apply instead of leaving them owed")
	}
}

// enrolAnchorLocal is the machine's config as the door holds it when the anchor
// step runs, read back through the loader the command reads it with.
func enrolAnchorLocal(t *testing.T, cfg string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// enrolAnchorPlan is the plan the anchor step needs and no more: the checkout it
// stands in (where the file is), the local config's file (for the advice about a
// base image), the repository and its provider (for the build record the checks
// read). Those are the members the step reads, and building the whole plan here
// would put a converge's fixture in front of a step that has none.
func enrolAnchorPlan(t *testing.T, r *addRig) reposEnrolment {
	t.Helper()
	file := enrolCheckoutFile(t)
	return reposEnrolment{
		checkout:  filepath.Dir(file),
		path:      r.cfg,
		localKey:  enrolRepo,
		ownerName: enrolRepo,
		row:       reposViewRepo{Provider: "github"},
	}
}
