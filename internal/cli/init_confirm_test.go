package cli

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// A run's conditions, for the tests of what each can confirm.
type condition struct {
	name     string
	opts     initOptions
	terminal bool
	agent    string
}

func conditions() []condition {
	return []condition{
		{"--yes at a terminal", initOptions{yes: true}, true, ""},
		{"--yes, a pipe", initOptions{yes: true}, false, ""},
		{"--non-interactive at a terminal", initOptions{nonInteractive: true}, true, ""},
		{"--yes --non-interactive", initOptions{yes: true, nonInteractive: true}, true, ""},
		{"--json at a terminal", initOptions{asJSON: true}, true, ""},
		{"--yes --json at a terminal", initOptions{yes: true, asJSON: true}, true, ""},
		{"no terminal", initOptions{}, false, ""},
		{"a coding agent at a terminal", initOptions{}, true, "CLAUDECODE"},
		{"a coding agent, --yes", initOptions{yes: true}, true, "CURSOR_AGENT"},
		{"a terminal", initOptions{}, true, ""},
	}
}

// withDoor is the same condition at the other front door: the door is the only
// thing that differs, and it is what a step's confirmation class may case on.
func (c condition) withDoor(door string) condition {
	c.opts.door = door
	return c
}

func (c condition) run(t *testing.T, stdin string) (*initRun, *readSpy, *strings.Builder) {
	t.Helper()
	canConfirmTyped = initflow.CanConfirm // the real rule, whatever a rig set
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return c.terminal }
	t.Cleanup(func() { stdinIsTerminal = old })
	if c.agent != "" {
		t.Setenv(c.agent, "1")
	}
	spy := &readSpy{Reader: strings.NewReader(stdin)}
	out := &strings.Builder{}
	o := c.opts
	o.firebase = "aurora-fp"
	cmd := &cobra.Command{}
	// The flags the two sanctioned readers look up on the command rather than
	// on the options (askConfirm reads --ask, --yes, --json and
	// --non-interactive there): a test's run must carry the same ones a real
	// command does, or --ask answers from a command that never passed it.
	cmd.Flags().Bool("ask", o.ask, "")
	cmd.Flags().Bool("yes", o.yes, "")
	cmd.Flags().Bool("json", o.asJSON, "")
	cmd.Flags().Bool("non-interactive", o.nonInteractive, "")
	cmd.SetIn(spy)
	// One writer for the step's own lines and for --ask's question, as a real
	// run has (newInitRun writes through the command's own output).
	cmd.SetOut(out)
	r := &initRun{cmd: cmd, o: &o, w: out, in: bufio.NewReader(spy), projectName: initProjectName, gcpProject: initProject}
	if o.ask {
		// A rig that passes --ask is the project whose marks answered
		// "managed": that is the only project the flag has a confirmation to
		// put back in (initRun.gateRestores, and initflow.AskDecision's first
		// rule — in any other project it is the no-op line its own help
		// describes). The read is the converge's own, made before the loop.
		r.gate = &initGate{managed: true}
	}
	return r, spy, out
}

// TestInitAsksNoConfirmation is the no-prompt policy's own test at the point
// every confirmation of the converge comes through (T7, docs/no-prompt-policy
// and its spec: "any confirmation prompt in Fugaro is a bug"). What answers a
// step is the session itself, and no arm of it types anything: --yes stands in
// where the run has the flag, a person at their own terminal confirms the step
// by being there, and a session nobody can answer — no terminal, --json,
// --non-interactive, a coding agent's — leaves the step for the user rather
// than reading a line nobody could end. The one arm that reads is --ask, and
// it reads a y/N and not a name.
func TestInitAsksNoConfirmation(t *testing.T) {
	const question = "apply all of the steps above to GCP project " + initProject + "?"
	for _, c := range conditions() {
		t.Run(c.name, func(t *testing.T) {
			r, spy, out := c.run(t, initProjectName+"\n")
			ok, err := r.confirmNow(question)
			if err == nil {
				// What the session is decides, and nothing about it is typed:
				// the flag stands in, and otherwise the person at the terminal
				// is the whole of the answer (canType). A run that leaves the
				// step says so by answering no, and the caller is the one that
				// refuses with what that left undone.
				want := c.opts.yes || (c.terminal && !c.opts.nonInteractive && !c.opts.asJSON)
				if ok != want {
					t.Fatalf("confirmed %v, want %v", ok, want)
				}
			} else {
				var ag *initflow.AgentError
				if ok || c.agent == "" || !errors.As(err, &ag) || ag.Marker != c.agent {
					t.Fatalf("confirmed %v (err %v), want the agent refusal naming %s", ok, err, c.agent)
				}
			}
			if spy.read {
				t.Fatalf("stdin was read: no confirmation of this run is typed any more")
			}
			if line := out.String(); line != "" {
				t.Fatalf("the step printed a prompt: %q", line)
			}
		})
	}
	t.Run("--ask puts the question back as a y/N", func(t *testing.T) {
		c := condition{name: "--ask", opts: initOptions{ask: true}, terminal: true}
		r, spy, out := c.run(t, "y\n")
		ok, err := r.confirmNow(question)
		if err != nil || !ok || !spy.read {
			t.Fatalf("confirmed %v, err %v, read %v: --ask asks the step's own question", ok, err, spy.read)
		}
		if want := question + " [y/N]: "; !strings.Contains(out.String(), want) {
			t.Fatalf("--ask printed %q, want %q", out.String(), want)
		}
	})
	t.Run("--ask where nobody can answer refuses without reading", func(t *testing.T) {
		for _, c := range []condition{
			{name: "--ask, a pipe", opts: initOptions{ask: true}},
			{name: "--ask --json at a terminal", opts: initOptions{ask: true, asJSON: true}, terminal: true},
			{name: "--ask --non-interactive at a terminal", opts: initOptions{ask: true, nonInteractive: true}, terminal: true},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, spy, out := c.run(t, "y\n")
				ok, err := r.confirmNow(question)
				if ok || err == nil || spy.read || !strings.Contains(err.Error(), "asked for") || strings.Contains(out.String(), "[y/N]") {
					t.Fatalf("confirmed %v, err %v, read %v, printed %q: --ask a confirmation nobody can answer is a refusal", ok, err, spy.read, out.String())
				}
			})
		}
		// A coding agent's marker is read before --ask's own question, so the
		// agent's session gets the marker's refusal rather than the asked-for
		// one: the marker is the whole of what keeps an agent out of a write,
		// and it must not be answered by the flag either.
		c := condition{name: "--ask in a coding agent's session", opts: initOptions{ask: true}, terminal: true, agent: "CLAUDECODE"}
		r, spy, out := c.run(t, "y\n")
		ok, err := r.confirmNow(question)
		var ag *initflow.AgentError
		if ok || err == nil || !errors.As(err, &ag) || ag.Marker != c.agent {
			t.Fatalf("confirmed %v, err %v: --ask does not answer an agent's session", ok, err)
		}
		if spy.read || strings.Contains(out.String(), "[y/N]") {
			t.Fatalf("read %v, printed %q: nothing is asked of an agent's session", spy.read, out.String())
		}
	})
}

// TestTypedOnlyFirestoreLocationMatrix: the Firestore location is permanent,
// and it is the ONE step W5.1 changes in a single door (D12's answer of
// 2026-10-20): `setup` keeps it Typed — where only a person at a real
// terminal may confirm it, and since T7 their being there IS the
// confirmation, with nothing typed — and `update --yes` covers it, because a
// re-converge of a database that already exists is not a first-time decision.
// The three steps the answer never relaxes, in either door, are creating the
// project and linking the billing account (which `update` refuses by flag, so
// they cannot arise here at all) and this location in `setup`.
func TestTypedOnlyFirestoreLocationMatrix(t *testing.T) {
	for _, door := range []string{setupDoor.name, updateDoor.name} {
		for _, c := range conditions() {
			t.Run(door+" "+c.name, func(t *testing.T) {
				c := c.withDoor(door)
				r, spy, out := c.run(t, infra.FirestoreLocation+"\n")
				err := r.confirmLocation()
				// Typed in setup, and the ordinary covered class in update: the
				// same call site, and the class is the door's (D12).
				class := initflow.Typed
				if door == updateDoor.name {
					class = initflow.YesOrTyped
				}
				can := canConfirmStep(class, r.conditions())
				if can {
					if err != nil {
						t.Fatalf("the location was not confirmed: %v", err)
					}
					if spy.read {
						t.Fatalf("stdin was read: nothing is typed at a confirmation any more (T7)")
					}
					if c.opts.yes && !strings.Contains(out.String(), "confirmed by --yes") {
						t.Errorf("update --yes confirmed the location without saying so:\n%s", out)
					}
					return
				}
				var ny *initflow.NeedsYouError
				if err == nil || !asNeedsYou(err, &ny) || spy.read {
					t.Fatalf("err %v, stdin read %v: confirmed, or read, without a confirmation", err, spy.read)
				}
				if strings.Contains(err.Error()+out.String()+strings.Join(ny.Left.Commands, " "), "--yes") {
					t.Errorf("the refusal suggests --yes: %v / %v", err, ny.Left)
				}
				if len(ny.Left.Commands) != 1 || strings.Contains(ny.Left.Commands[0], "\n") || !strings.Contains(ny.Left.Commands[0], "--firebase-project aurora-fp") {
					t.Errorf("the typed route is not the one-line command: %+v", ny.Left)
				}
			})
		}
	}
}

func asNeedsYou(err error, target **initflow.NeedsYouError) bool {
	for err != nil {
		if ny, ok := err.(*initflow.NeedsYouError); ok {
			*target = ny
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestTypedOnlyImageBuildMatrix: the billable first image build under every
// condition. W5.1 moves it out of the Typed class — D12's answer of 2026-10-20
// makes it an ordinary covered row of BOTH front doors, so `--yes` takes it
// here and in `setup` alike — and the name stays the one the image-refresh plan
// records for this matrix. What does not change is the direction of the
// mistake: a coding agent's environment, --json, --non-interactive and a run
// with no terminal at all never confirm it, and a run that cannot take it
// leaves the build (and is needs-you at the end), never builds.
func TestTypedOnlyImageBuildMatrix(t *testing.T) {
	for _, c := range conditions() {
		t.Run(c.name, func(t *testing.T) {
			fb := gcpfake.NewBuild(t)
			r, spy, out := c.run(t, initProjectName+"\n")
			lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
				BaseImages: map[string]string{"base": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3"},
				Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
			cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {}}}
			spec := infra.RepoSpec{Name: initProjectName, BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r"}
			built, err := r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
			// YesOrTyped, exactly: --yes takes it, a real terminal takes it
			// with no --non-interactive and no --json (the person being there
			// is the confirmation, since T7), and a coding agent's environment
			// takes it from neither.
			can := canConfirmStep(initflow.YesOrTyped, r.conditions())
			for _, rq := range fb.Requests() {
				if rq.Method == "POST" {
					t.Fatalf("a build was submitted without a confirmation: %s", rq.Path)
				}
			}
			if built != 0 {
				t.Fatalf("built %d", built)
			}
			if can {
				// Confirmed: the run went on to build, and stopped at the
				// (deliberately incomplete) spec, after the confirmation. Only
				// the typed route reads stdin; --yes confirms without asking.
				if err == nil || !strings.Contains(err.Error(), "no workflow") {
					t.Fatalf("err %v: the confirmation was not taken", err)
				}
				if spy.read {
					t.Fatalf("stdin was read: nothing is typed at a confirmation any more (T7)")
				}
				if c.opts.yes && !strings.Contains(out.String(), "confirmed by --yes") {
					t.Errorf("--yes confirmed the build without saying so:\n%s", out)
				}
				return
			}
			if err != nil || len(r.buildsLeft) != 1 {
				t.Fatalf("err %v, read %v, left %v", err, spy.read, r.buildsLeft)
			}
			if w := strings.Join(r.res.Warnings, "\n"); !strings.Contains(w, "billable") || strings.Contains(w, "pass --yes") {
				t.Errorf("warnings: %s", w)
			}
		})
	}
}

// An agent's environment confirms nothing the engines ask, --yes or not, and
// the refusal never offers --yes. The run's cmd carries a stdin that is not a
// terminal, so every one of the six markers is an agent's session here
// whatever the test binary's own stdin is; the IDE extension's marker alone,
// at a real terminal, is a person's and is its own test
// (TestIDETerminalTypedConfirmation).
func TestAgentEnvironmentConfirmsNothing(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "1")
			var out strings.Builder
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(""))
			r := &initRun{cmd: cmd, o: &initOptions{yes: true}, w: &out, in: bufio.NewReader(strings.NewReader("")), projectName: initProjectName, gcpProject: initProject}
			ok, err := r.ask("applies a plan")
			var ag *initflow.AgentError
			if ok || err == nil || !asAgent(err, &ag) {
				t.Fatalf("ok %v err %v", ok, err)
			}
			if strings.Contains(out.String(), "confirmed by --yes") || strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "your own terminal window") {
				t.Errorf("%v / %s", err, out.String())
			}
			if cerr := r.confirm("applies a plan", "nothing was applied"); cerr == nil {
				t.Error("confirm passed")
			}
		})
	}
}

func asAgent(err error, target **initflow.AgentError) bool {
	for err != nil {
		if a, ok := err.(*initflow.AgentError); ok {
			*target = a
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Through the command: a coding agent's session applies nothing, with --yes
// or without, whichever stage it reaches; the plan still runs.
func TestInitInAnAgentSessionAppliesNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"--yes":                   {"setup", "--yes", "--json"},
		"--yes --non-interactive": {"setup", "--yes", "--non-interactive", "--json"},
		"no flags":                {"setup", "--json"},
		"--config-only --yes":     {"setup", "--config-only", "--yes", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			r.stateBucket()
			canConfirmTyped = initflow.CanConfirm
			t.Setenv("CLAUDECODE", "1")
			out, errOut, err := executeStdin(t, "", args...)
			if err == nil {
				t.Fatalf("exit 0 in an agent session:\n%s", out)
			}
			if n := len(r.ran(t, "apply")); n != 0 {
				t.Errorf("terraform applied %d time(s): %q", n, r.calls(t))
			}
			if all := out + errOut + err.Error(); strings.Contains(all, "or pass --yes") || strings.Contains(all, "pass --yes") {
				t.Errorf("an agent session was told to pass --yes:\n%s", all)
			}
			if !strings.Contains(out+errOut+err.Error(), "your own terminal window") {
				t.Errorf("no typed route:\n%s%s%v", out, errOut, err)
			}
		})
	}
	// Planning stays allowed.
	r := newInitRig(t)
	r.stateBucket()
	t.Setenv("CLAUDECODE", "1")
	out, _, err := executeStdin(t, "", "setup", "--plan-only", "--json")
	if err != nil || !strings.Contains(out, "will-do") || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("--plan-only in an agent session: %v\n%s", err, out)
	}
}
