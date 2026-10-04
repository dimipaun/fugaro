package cli

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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
	cmd.SetIn(spy)
	r := &initRun{cmd: cmd, o: &o, w: out, in: bufio.NewReader(spy), projectName: initProjectName, gcpProject: initProject}
	return r, spy, out
}

// The Firestore location is permanent: only a person typing it at a real
// terminal confirms it. Every other condition leaves it needs-you, reads no
// stdin, and never suggests --yes.
func TestTypedOnlyFirestoreLocationMatrix(t *testing.T) {
	for _, c := range conditions() {
		t.Run(c.name, func(t *testing.T) {
			r, spy, out := c.run(t, infra.FirestoreLocation+"\n")
			err := r.confirmLocation()
			can := c.terminal && c.agent == "" && !c.opts.yes && !c.opts.nonInteractive && !c.opts.asJSON
			if can {
				if err != nil || !spy.read {
					t.Fatalf("a person at a terminal could not confirm: %v", err)
				}
				return
			}
			var ny *initflow.NeedsYouError
			if err == nil || !asNeedsYou(err, &ny) || spy.read {
				t.Fatalf("err %v, stdin read %v: confirmed, or read, without a person typing", err, spy.read)
			}
			if strings.Contains(err.Error()+out.String()+strings.Join(ny.Left.Commands, " "), "--yes") {
				t.Errorf("the refusal suggests --yes: %v / %v", err, ny.Left)
			}
			if len(ny.Left.Commands) != 1 || strings.Contains(ny.Left.Commands[0], "\n") || !strings.Contains(ny.Left.Commands[0], "--firebase aurora-fp") {
				t.Errorf("the typed route is not the one-line command: %+v", ny.Left)
			}
		})
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

// The billable first image build is typed-only too, and a run that cannot
// take it leaves the build (and is needs-you at the end), never builds.
func TestTypedOnlyImageBuildMatrix(t *testing.T) {
	for _, c := range conditions() {
		t.Run(c.name, func(t *testing.T) {
			fb := gcpfake.NewBuild(t)
			r, spy, _ := c.run(t, initProjectName+"\n")
			lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
				BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"},
				Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
			cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
			spec := infra.RepoSpec{Name: initProjectName, BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r"}
			built, err := r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
			can := c.terminal && c.agent == "" && !c.opts.yes && !c.opts.nonInteractive && !c.opts.asJSON
			for _, rq := range fb.Requests() {
				if rq.Method == "POST" {
					t.Fatalf("a build was submitted without a person typing: %s", rq.Path)
				}
			}
			if built != 0 {
				t.Fatalf("built %d", built)
			}
			if can {
				// Confirmed: the run went on to build, and stopped at the
				// (deliberately incomplete) spec, after the confirmation.
				if err == nil || !strings.Contains(err.Error(), "no workflow") || !spy.read {
					t.Fatalf("err %v, read %v: the typed confirmation was not taken", err, spy.read)
				}
				return
			}
			if err != nil || spy.read || len(r.buildsLeft) != 1 {
				t.Fatalf("err %v, read %v, left %v", err, spy.read, r.buildsLeft)
			}
			if w := strings.Join(r.res.Warnings, "\n"); !strings.Contains(w, "billable") || strings.Contains(w, "pass --yes") {
				t.Errorf("warnings: %s", w)
			}
		})
	}
}

// An agent's environment confirms nothing the engines ask, --yes or not, and
// the refusal never offers --yes.
func TestAgentEnvironmentConfirmsNothing(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "1")
			var out strings.Builder
			r := &initRun{cmd: &cobra.Command{}, o: &initOptions{yes: true}, w: &out, in: bufio.NewReader(strings.NewReader("")), projectName: initProjectName, gcpProject: initProject}
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
		"--yes":                   {"init", "--yes", "--json"},
		"--yes --non-interactive": {"init", "--yes", "--non-interactive", "--json"},
		"no flags":                {"init", "--json"},
		"--config-only --yes":     {"init", "--config-only", "--yes", "--json"},
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
	out, _, err := executeStdin(t, "", "init", "--plan-only", "--json")
	if err != nil || !strings.Contains(out, "will-do") || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("--plan-only in an agent session: %v\n%s", err, out)
	}
}
