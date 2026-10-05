//go:build darwin || linux

package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type loopResult struct {
	res *initflow.Result
	err error
}

// start runs the stage's loop in the background, as init does on a terminal.
func (r *secretsRig) start(ctx context.Context) chan loopResult {
	done := make(chan loopResult, 1)
	go func() {
		res, err := initflow.Run(ctx, []initflow.Stage{r.stage}, r.e.options())
		done <- loopResult{res, err}
	}()
	return done
}

func waitLoop(t *testing.T, done chan loopResult) loopResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not return")
		return loopResult{}
	}
}

// answer waits for the nth prompt containing marker and for the terminal to
// stop echoing, then types input as the user would.
func (r *secretsRig) answer(t *testing.T, f *ttyFixture, marker string, n int, input string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); strings.Count(r.errOut.String(), marker) < n; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no prompt %q (%d): stderr %q", marker, n, r.errOut.String())
		}
	}
	f.waitEcho(t, false)
	if len(input) > 4096 {
		// More than the terminal buffers: the stage stops reading at the
		// error, so the rest of the paste is not for this goroutine to wait on.
		go func() { _, _ = f.master.WriteString(input) }()
		return
	}
	if _, err := f.master.WriteString(input); err != nil {
		t.Fatal(err)
	}
}

func queued(t *testing.T, f *ttyFixture) int {
	t.Helper()
	n, err := unix.IoctlGetInt(int(f.tty.Fd()), ioctlInputQueue)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// pastes: the answers of a Bitbucket checkout's whole prompt sequence.
func (r *secretsRig) bitbucketSession(t *testing.T, f *ttyFixture, claude string) {
	r.answer(t, f, "bitbucket-token (input hidden)", 1, "bb-secret-value-EXAMPLE\n")
	r.answer(t, f, claude+" (input hidden)", 1, tokenValue+"\n")
}

// The prompts are hidden, validate, store through the engine, and nothing
// the user types is ever shown, printed or put in the result.
func TestSecretPromptHidden(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty)
	done := r.start(context.Background())
	r.bitbucketSession(t, f, "claude-oauth-token")
	lr := waitLoop(t, done)
	if lr.err != nil || lr.res.Failed != nil || stateOf(lr.res, "secrets") != initflow.Changed {
		t.Fatalf("%v, %+v", lr.err, lr.res)
	}
	if got := r.store.latest(bitbucketSlug, "bitbucket-token"); got != "bb-secret-value-EXAMPLE" {
		t.Errorf("bitbucket-token = %q", got)
	}
	if got := r.store.latest(bitbucketSlug, "claude-oauth-token"); got != tokenValue { // the default choice
		t.Errorf("claude-oauth-token = %q", got)
	}
	f.waitEcho(t, true)
	time.Sleep(100 * time.Millisecond) // let any echo arrive
	if d := f.displayed(); strings.Contains(d, "EXAMPLE") {
		t.Fatalf("the terminal echoed a value: %q", d)
	}
	if !strings.Contains(r.errOut.String(), "(input hidden)") {
		t.Errorf("prompts: %q", r.errOut.String())
	}
	// Nothing the user typed is in any output; held values are released.
	for _, v := range []string{"bb-secret-value-EXAMPLE", tokenValue} {
		noLeak(t, v, resultJSON(t, lr.res), r.out.String(), r.errOut.String())
	}
	for _, h := range r.e.redact {
		if h != "" {
			t.Errorf("a value is still held for redaction: %q", h)
		}
	}
}

// The Claude credential asked for is the one agent.auth names, with no
// choice offered: api-key asks for the API key and never for the OAuth token.
func TestSecretPromptFollowsAgentAuth(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty)
	r.writeConfig(t, checkoutYAML("bitbucket", "api-key", "aurora", ""))
	done := r.start(context.Background())
	r.bitbucketSession(t, f, "anthropic-api-key")
	lr := waitLoop(t, done)
	if lr.err != nil || lr.res.Failed != nil {
		t.Fatalf("%v, %+v", lr.err, lr.res)
	}
	if r.store.latest(bitbucketSlug, "anthropic-api-key") != tokenValue || r.store.latest(bitbucketSlug, "claude-oauth-token") != "" {
		t.Errorf("stored the wrong Claude credential")
	}
	if strings.Contains(r.errOut.String(), "Type 1 or 2") {
		t.Error("a choice was offered")
	}
}

// A multi-line PEM is pasted once, hidden, and stored whole with its line
// breaks, whatever line ends the terminal sends.
func TestMultilinePEMAccepted(t *testing.T) {
	for name, nl := range map[string]string{"CR": "\r", "CRLF": "\r\n", "LF": "\n"} {
		t.Run(name, func(t *testing.T) {
			f := newTTY(t)
			r := newSecretsRig(t, githubOrigin, f.tty)
			done := r.start(context.Background())
			r.answer(t, f, "github-app-key (input hidden)", 1, strings.ReplaceAll(pemValue, "\n", nl))
			r.answer(t, f, "claude-oauth-token (input hidden)", 1, tokenValue+"\n")
			lr := waitLoop(t, done)
			if lr.err != nil || lr.res.Failed != nil {
				t.Fatalf("%v, %+v", lr.err, lr.res)
			}
			if got := r.store.latest(mustSlug("github", "acme/app"), "github-app-key"); got != pemValue {
				t.Fatalf("stored %q", got)
			}
			// Typed in this run: the App pre-check may use it from memory.
			if string(r.e.r.appKeyMem) != pemValue {
				t.Fatalf("the typed key is not held in memory for the pre-check")
			}
			f.waitEcho(t, true)
			time.Sleep(100 * time.Millisecond)
			if d := f.displayed(); strings.Contains(d, "EXAMPLE") || strings.Contains(d, "MIIE") {
				t.Fatalf("the terminal echoed the key: %q", d)
			}
			noLeak(t, pemValue, resultJSON(t, lr.res), r.out.String(), r.errOut.String())
		})
	}
}

// A paste that is not a PEM, and Ctrl-C mid-paste, store nothing, restore
// the terminal and never quote what was typed.
func TestPEMPasteRefusedOrCancelled(t *testing.T) {
	for name, input := range map[string]string{
		"not a pem":      "just-some-text-EXAMPLE\r",
		"ctrl-c":         "-----BEGIN RSA PRIVATE KEY-----\rMIIEEXAMPLEpartial0000\r\x03",
		"ctrl-d":         "-----BEGIN RSA PRIVATE KEY-----\rMIIEEXAMPLEpartial0000\r\x04",
		"over the cap":   "",
		"too many lines": "",
	} {
		t.Run(name, func(t *testing.T) {
			if name == "over the cap" {
				input = "-----BEGIN RSA PRIVATE KEY-----\r" + strings.Repeat("A", 70*1024) + "\r"
			}
			if name == "too many lines" {
				input = strings.ReplaceAll(longPEM(200), "\n", "\r")
			}
			f := newTTY(t)
			r := newSecretsRig(t, githubOrigin, f.tty)
			r.store.seed(mustSlug("github", "acme/app"), "claude-oauth-token", "stored-already-0001")
			done := r.start(context.Background())
			r.answer(t, f, "github-app-key (input hidden)", 1, input)
			lr := waitLoop(t, done)
			if lr.res == nil || lr.res.Failed == nil || r.store.setCalls() != 0 {
				t.Fatalf("%+v sets %d", lr.res, r.store.setCalls())
			}
			if lr.res.Failed != nil {
				if strings.Contains(lr.res.Failed.Error+lr.res.Cause.Error()+r.errOut.String(), "EXAMPLE") {
					t.Fatalf("an error quoted the paste: %+v", lr.res.Failed)
				}
			}
			f.abandoned = false
			f.waitEcho(t, true)
		})
	}
}

// --yes and --non-interactive never prompt and never read the terminal:
// the value typed ahead stays in the terminal's queue.
func TestFlagsNeverPrompt(t *testing.T) {
	for name, set := range map[string]func(*initOptions){
		"--yes":                   func(o *initOptions) { o.yes = true },
		"--non-interactive":       func(o *initOptions) { o.nonInteractive = true },
		"--yes --non-interactive": func(o *initOptions) { o.yes, o.nonInteractive = true, true },
		"--json":                  func(o *initOptions) { o.asJSON = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := newTTY(t)
			r := newSecretsRig(t, bitbucketOrigin, f.tty, set)
			if _, err := f.master.WriteString(tokenValue + "\n"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			before := queued(t, f)
			res, err := r.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(r.errOut.String(), "hidden") || r.store.setCalls() != 0 || stateOf(res, "secrets") != initflow.NeedsYou {
				t.Fatalf("stderr %q, sets %d, %+v", r.errOut.String(), r.store.setCalls(), res.Stages)
			}
			if after := queued(t, f); after != before || before == 0 {
				t.Fatalf("the terminal was read: %d bytes queued before, %d after", before, after)
			}
			if !f.echo(t) {
				t.Error("the terminal's echo was touched")
			}
			noLeak(t, tokenValue, resultJSON(t, res), r.out.String(), r.errOut.String())
		})
	}
}

// A secret that already has a version is skipped: no prompt, no read, no new
// version (rotating is fugaro secrets set); only what is missing is asked.
func TestExistingSecretNotOverwritten(t *testing.T) {
	t.Run("all stored", func(t *testing.T) {
		f := newTTY(t)
		r := newSecretsRig(t, bitbucketOrigin, f.tty)
		r.store.seed(bitbucketSlug, "bitbucket-token", "stored-bb-token-0001")
		r.store.seed(bitbucketSlug, "claude-oauth-token", "stored-oauth-0001")
		_, _ = f.master.WriteString("typed-ahead-EXAMPLE\n")
		time.Sleep(100 * time.Millisecond)
		before := queued(t, f)
		res, err := r.run(t)
		if err != nil || stateOf(res, "secrets") != initflow.Done || r.store.setCalls() != 0 || r.errOut.String() != "" {
			t.Fatalf("%v, %+v, sets %d, stderr %q", err, res.Stages, r.store.setCalls(), r.errOut.String())
		}
		if queued(t, f) != before {
			t.Error("the terminal was read")
		}
		if r.store.latest(bitbucketSlug, "bitbucket-token") != "stored-bb-token-0001" {
			t.Error("a stored secret changed")
		}
	})
	t.Run("one missing", func(t *testing.T) {
		f := newTTY(t)
		r := newSecretsRig(t, bitbucketOrigin, f.tty)
		r.store.seed(bitbucketSlug, "bitbucket-token", "stored-bb-token-0001")
		done := r.start(context.Background())
		r.answer(t, f, "claude-oauth-token (input hidden)", 1, tokenValue+"\n")
		lr := waitLoop(t, done)
		if lr.err != nil || lr.res.Failed != nil || r.store.setCalls() != 1 || strings.Contains(r.errOut.String(), "bitbucket-token") {
			t.Fatalf("%v, %+v, sets %d, stderr %q", lr.err, lr.res, r.store.setCalls(), r.errOut.String())
		}
		if r.store.latest(bitbucketSlug, "bitbucket-token") != "stored-bb-token-0001" {
			t.Error("a stored secret changed")
		}
	})
	t.Run("a container without a version is missing", func(t *testing.T) {
		f := newTTY(t)
		r := newSecretsRig(t, bitbucketOrigin, f.tty)
		r.store.seed(bitbucketSlug, "bitbucket-token", "") // made by init --repo's Terraform, no value yet
		r.store.seed(bitbucketSlug, "claude-oauth-token", "stored-oauth-0001")
		done := r.start(context.Background())
		r.answer(t, f, "bitbucket-token (input hidden)", 1, "bb-secret-value-EXAMPLE\n")
		lr := waitLoop(t, done)
		if lr.err != nil || r.store.latest(bitbucketSlug, "bitbucket-token") != "bb-secret-value-EXAMPLE" {
			t.Fatalf("%v, %+v", lr.err, lr.res)
		}
	})
}

// A failing store leaves nothing stored, names the secret and not the value
// (whatever form a server echoes it in), and every output stays clean.
func TestSecretNeverInErrors(t *testing.T) {
	const val = "bb-secret-value-EXAMPLE"
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty)
	r.store.setHook = func(id string, value []byte) error {
		return errors.New("backend rejected " + string(value) + " / " + "YmItc2VjcmV0LXZhbHVlLUVYQU1QTEU=" + "\nsecond line " + string(value))
	}
	done := r.start(context.Background())
	r.answer(t, f, "bitbucket-token (input hidden)", 1, val+"\n")
	lr := waitLoop(t, done)
	if lr.res.Failed == nil || lr.res.Cause == nil {
		t.Fatalf("%+v", lr.res)
	}
	if !strings.Contains(lr.res.Failed.Error, "bitbucket-token") {
		t.Errorf("the failure does not name the secret: %q", lr.res.Failed.Error)
	}
	if r.store.latest(bitbucketSlug, "bitbucket-token") != "" || len(r.store.secrets) != 0 {
		t.Error("a failed store left state behind")
	}
	noLeak(t, val, lr.res.Failed.Error, lr.res.Failed.Fix, lr.res.Cause.Error(), resultJSON(t, lr.res), r.out.String(), r.errOut.String())
	for _, h := range r.e.redact {
		if h != "" {
			t.Errorf("a value is still held for redaction: %q", h)
		}
	}
	// Exit code 2: a remote failure, like fugaro secrets set.
	if ExitCode(lr.res.Cause) != ExitRemoteError {
		t.Errorf("exit %d", ExitCode(lr.res.Cause))
	}
}

// Against the real client and the Secret Manager fake: a good store creates
// the secret with the labels init --repo adopts; a server that echoes the
// payload (raw, or base64 as sent) in its error cannot make it reach an
// output, and nothing is stored.
func TestSecretsStageAgainstSecretManagerFake(t *testing.T) {
	const val = "bb-secret-value-EXAMPLE"
	session := func(t *testing.T, sm *gcpfake.Secrets, f *ttyFixture) loopResult {
		r := newSecretsRig(t, bitbucketOrigin, f.tty)
		r.e.lc = &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Repos: map[string]localcfg.Repo{"acme/app": {Provider: "bitbucket"}}, Endpoints: localcfg.Endpoints{SecretManager: sm.URL + "/", NoAuth: true}}
		r.stage = newSecretsStage(r.e) // the real client, not the memStore
		sm.Seed(gcp.SecretID(bitbucketSlug, "claude-oauth-token"), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: mustLabel(t, bitbucketSlug), gcp.LabelSecret: "claude-oauth-token"}, []byte(tokenValue))
		done := r.start(context.Background())
		r.answer(t, f, "bitbucket-token (input hidden)", 1, val+"\n")
		lr := waitLoop(t, done)
		noLeak(t, val, resultJSON(t, lr.res), r.out.String(), r.errOut.String())
		if lr.res.Cause != nil {
			noLeak(t, val, lr.res.Cause.Error(), lr.res.Failed.Error)
		}
		return lr
	}
	t.Run("stored", func(t *testing.T) {
		sm := gcpfake.NewSecrets(t)
		lr := session(t, sm, newTTY(t))
		if lr.res.Failed != nil || stateOf(lr.res, "secrets") != initflow.Changed {
			t.Fatalf("%+v", lr.res)
		}
		id := gcp.SecretID(bitbucketSlug, "bitbucket-token")
		if string(sm.Latest(id)) != val {
			t.Fatalf("stored %q", sm.Latest(id))
		}
		var labels map[string]string
		for _, rq := range sm.Requests() {
			if rq.Method == "POST" && strings.HasSuffix(rq.Path, "/secrets") {
				var body struct{ Labels map[string]string }
				_ = json.Unmarshal(rq.Body, &body)
				labels = body.Labels
			}
		}
		if labels[gcp.LabelManaged] != gcp.ManagedValue || labels[gcp.LabelRepo] != mustLabel(t, bitbucketSlug) || labels[gcp.LabelSecret] != "bitbucket-token" {
			t.Errorf("labels = %v", labels)
		}
	})
	t.Run("the server echoes the payload", func(t *testing.T) {
		sm := gcpfake.NewSecrets(t)
		sm.FailAddVersion = "bad payload " + val + " " + base64.StdEncoding.EncodeToString([]byte(val))
		lr := session(t, sm, newTTY(t))
		if lr.res.Failed == nil || !strings.Contains(lr.res.Failed.Error, "bitbucket-token") {
			t.Fatalf("%+v", lr.res)
		}
		if sm.Latest(gcp.SecretID(bitbucketSlug, "bitbucket-token")) != nil {
			t.Error("a value was stored")
		}
	})
}

func mustLabel(t *testing.T, slug string) string {
	t.Helper()
	l, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Through the init command on a terminal (the whole converge, the real
// client against the fake): the values land in Secret Manager and in no
// output, the terminal's display or the error text. (--json never prompts:
// TestJSONNeverPromptsThroughTheCommand.)
func TestSecretNeverInOutput(t *testing.T) {
	initOnTTY(t, false)
}

// --json never prompts, on a terminal in every other way: the secrets stage
// is needs-you with the one-line commands, nothing is read or stored.
func TestJSONNeverPromptsThroughTheCommand(t *testing.T) {
	initOnTTY(t, true)
}

func initOnTTY(t *testing.T, asJSON bool) {
	const val = "bb-secret-value-EXAMPLE"
	r := newInitRig(t)
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(checkoutYAML("bitbucket", "oauth", "aurora", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
	fetched(t, dir) // the stages read the default branch's file
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = realWriterIsTerminal })
	t.Chdir(dir)

	f := newTTY(t)
	cmd := NewRootCmd()
	var out, errOut syncBuf
	cmd.SetIn(f.tty)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	args := []string{"init"}
	if asJSON {
		args = append(args, "--json")
	}
	cmd.SetArgs(args)
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	rig := &secretsRig{errOut: &errOut}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(errOut.String()+out.String(), "Type aurora to apply"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no confirmation prompt: %q", errOut.String())
		}
	}
	_, _ = f.master.WriteString("aurora\n") // the engine's own typed confirmation (the config rewrite)
	if asJSON {
		_, _ = f.master.WriteString(val + "\n") // typed ahead: must stay unread
		time.Sleep(100 * time.Millisecond)
		before := queued(t, f)
		var err error
		select {
		case err = <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("init did not return")
		}
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, errOut.String())
		}
		var res convergeJSON
		if jerr := json.Unmarshal([]byte(out.String()), &res); jerr != nil || res.state("secrets") != "needs-you" {
			t.Fatalf("%v:\n%s", jerr, out.String())
		}
		last := res.Left[len(res.Left)-1]
		if last["stage"] != "secrets" || !strings.Contains(last["commands"], "secrets set bitbucket-token --repo acme/sandbox") {
			t.Fatalf("left %v", res.Left)
		}
		if strings.Contains(errOut.String(), "hidden") || queued(t, f) != before || before == 0 {
			t.Fatalf("a prompt was shown or the terminal read: %q", errOut.String())
		}
		for _, rq := range r.sm.Requests() {
			if rq.Method == "POST" {
				t.Fatalf("the stage wrote to Secret Manager: %s", rq.Path)
			}
		}
		noLeak(t, val, out.String(), errOut.String()) // the terminal's own echo of the typed-ahead line is not the program's
		return
	}
	rig.answer(t, f, "bitbucket-token (input hidden)", 1, val+"\n")
	rig.answer(t, f, "claude-oauth-token (input hidden)", 1, tokenValue+"\n")
	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("init did not return")
	}
	// The repository stage after the secrets is not what is under test: this
	// rig has no base image for it, which it refuses before any cloud call.
	if err != nil && !strings.Contains(err.Error(), "no base_images entry") {
		t.Fatalf("%v\n%s", err, errOut.String())
	}
	slug := mustSlug("bitbucket", "acme/sandbox")
	if string(r.sm.Latest(gcp.SecretID(slug, "bitbucket-token"))) != val || string(r.sm.Latest(gcp.SecretID(slug, "claude-oauth-token"))) != tokenValue {
		t.Fatal("the values were not stored")
	}
	f.waitEcho(t, true)
	time.Sleep(100 * time.Millisecond)
	for _, v := range []string{val, tokenValue} {
		noLeak(t, v, out.String(), errOut.String(), f.displayed())
	}
}
