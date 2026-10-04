//go:build darwin || linux

package cli

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// unknown makes the rig's repository one the project does not list: a clone
// of somebody's repository whose default-branch fugaro.yaml names this project.
func (r *secretsRig) unknown() { r.e.lc.Repos = nil }

// waitOut waits for text in the loop's account (stdout), where the typed
// opt-in prompt is shown.
func (r *secretsRig) waitOut(t *testing.T, f *ttyFixture, text string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(r.out.String(), text); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in stdout %q (stderr %q)", text, r.out.String(), r.errOut.String())
		}
	}
}

// A repository the project does not list is not asked about: a cloned
// third-party repository whose fugaro.yaml names this project cannot make the
// secrets stage prompt for, or store, anything. Nothing is read from stdin.
func TestSecretsStageBehindTheHostileGate(t *testing.T) {
	spy := &readerSpy{Reader: strings.NewReader(tokenValue + "\n")}
	r := newSecretsRig(t, githubOrigin, spy)
	r.unknown()
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if spy.read || r.store.setCalls() != 0 || len(r.store.secrets) != 0 {
		t.Fatalf("read %v, sets %d, secrets %v", spy.read, r.store.setCalls(), r.store.secrets)
	}
	if stateOf(res, initflow.Secrets) != initflow.NeedsYou || len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("stages %+v left %+v", res.Stages, res.Left)
	}
	var detail string
	for _, st := range res.Stages {
		detail = st.Detail
	}
	if !strings.Contains(detail, "acme/app") || !strings.Contains(detail, "not one of project") {
		t.Errorf("detail %q", detail)
	}
	if strings.Contains(strings.Join(res.Left[0].Commands, " "), "secrets set") {
		t.Errorf("the stage handed out the secrets commands for an unknown repository: %+v", res.Left[0])
	}
}

// A coding agent is never handed --onboard-repo to paste, and the flag itself
// does not opt a repository in when its environment is present.
func TestSecretsGateInAnAgentSession(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""), func(o *initOptions) { o.onboardRepo = "acme/app" })
	r.unknown()
	t.Setenv("CLAUDECODE", "1")
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(res, initflow.Secrets) != initflow.NeedsYou || r.store.setCalls() != 0 {
		t.Fatalf("stages %+v", res.Stages)
	}
	all := resultJSON(t, res)
	if strings.Contains(all, "--onboard-repo") {
		t.Errorf("an agent session is told to pass --onboard-repo:\n%s", all)
	}
	if !strings.Contains(all, "your own terminal") {
		t.Errorf("no typed route:\n%s", all)
	}
}

// At a real terminal the repository is typed first (its name and host are
// shown, a wrong answer stores nothing and shows no secret prompt), and only
// then does the hidden prompt come, naming the repository and its host.
func TestSecretsTypedOptInThenPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, typed string
		stored      bool
	}{{"wrong", "acme/other\n", false}, {"right", "acme/app\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTTY(t)
			r := newSecretsRig(t, bitbucketOrigin, f.tty)
			r.unknown()
			r.e.r.in = bufio.NewReader(f.tty)
			done := r.start(context.Background())
			r.waitOut(t, f, "Type acme/app to onboard it")
			if out := r.out.String(); !strings.Contains(out, "acme/app on host bitbucket.org") {
				t.Errorf("the prompt does not state the repository and its host:\n%s", out)
			}
			if _, err := f.master.WriteString(tc.typed); err != nil {
				t.Fatal(err)
			}
			if tc.stored {
				r.answer(t, f, "bitbucket-token (input hidden)", 1, "bb-secret-value-EXAMPLE\n")
				r.answer(t, f, "claude-oauth-token (input hidden)", 1, tokenValue+"\n")
			}
			lr := waitLoop(t, done)
			if lr.err != nil {
				t.Fatal(lr.err)
			}
			if tc.stored {
				if stateOf(lr.res, initflow.Secrets) != initflow.Changed || r.store.latest(bitbucketSlug, "bitbucket-token") == "" {
					t.Fatalf("%+v", lr.res.Stages)
				}
				if !strings.Contains(r.errOut.String(), "bitbucket-token for acme/app on bitbucket.org") {
					t.Errorf("the hidden prompt does not say whose secret it is:\n%s", r.errOut.String())
				}
				if !r.e.authorized["acme/app"] {
					t.Error("the typed opt-in is not remembered for the stages after")
				}
				return
			}
			if stateOf(lr.res, initflow.Secrets) != initflow.NeedsYou || r.store.setCalls() != 0 || strings.Contains(r.errOut.String(), "input hidden") {
				t.Fatalf("stages %+v, stderr %q", lr.res.Stages, r.errOut.String())
			}
		})
	}
}

// --onboard-repo is the non-interactive opt-in: for the checkout's own
// repository on a provider's host, a terminal then prompts for the values.
func TestSecretsOnboardRepoFlagOptsIn(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty, func(o *initOptions) { o.onboardRepo = "acme/app" })
	r.unknown()
	done := r.start(context.Background())
	r.bitbucketSession(t, f, "claude-oauth-token")
	lr := waitLoop(t, done)
	if lr.err != nil || stateOf(lr.res, initflow.Secrets) != initflow.Changed {
		t.Fatalf("%v %+v", lr.err, lr.res)
	}
	// A flag naming another repository is refused whatever else is true.
	r = newSecretsRig(t, bitbucketOrigin, strings.NewReader(""), func(o *initOptions) { o.onboardRepo = "acme/else" })
	r.unknown()
	if res, err := r.run(t); err != nil || res.Failed == nil {
		t.Fatalf("err %v res %+v", err, res)
	}
}

// What the stage asks for comes from the default branch's fugaro.yaml, not
// the working tree's: an uncommitted edit adds no secret.
func TestSecretsReadTheDefaultBranchFile(t *testing.T) {
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	r.writeConfig(t, checkoutYAML("bitbucket", "oauth", "aurora", ""))
	// A working-tree edit, never committed.
	edited := checkoutYAML("bitbucket", "api-key", "aurora", ", secrets: [{ name: evil-token, env: EVIL }]")
	if err := writeFileForTest(r.dir+"/fugaro.yaml", edited); err != nil {
		t.Fatal(err)
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if strings.Contains(cmds, "evil-token") || strings.Contains(cmds, "anthropic-api-key") || !strings.Contains(cmds, "claude-oauth-token") {
		t.Errorf("the working tree's fugaro.yaml was read:\n%s", cmds)
	}
	// A repository with only a working-tree fugaro.yaml has no default-branch
	// one: it is not the project's (unknown, no file): skipped.
	r2 := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r2.unknown()
	r2.removeConfig(t)
	if err := writeFileForTest(r2.dir+"/fugaro.yaml", checkoutYAML("github", "oauth", "aurora", "")); err != nil {
		t.Fatal(err)
	}
	res, err = r2.run(t)
	if err != nil || stateOf(res, initflow.Secrets) != initflow.Skipped {
		t.Fatalf("%v %+v", err, res.Stages)
	}
}
