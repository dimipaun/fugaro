//go:build darwin || linux

package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The secrets asked for are the ones the jobs mount: agent.auth decides the
// Claude credential (vertex needs none, and a rerun with the git credential
// stored is done), provider keys come only with api-key, and the workflows'
// own secrets are always asked for.
func TestNeededSecretsFollowAuth(t *testing.T) {
	left := func(t *testing.T, r *secretsRig) string {
		res, err := r.run(t)
		if err != nil || len(res.Left) != 1 {
			t.Fatalf("%v, %+v", err, res)
		}
		return strings.Join(res.Left[0].PrintedCommands(), "\n")
	}
	t.Run("vertex", func(t *testing.T) {
		r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
		r.writeConfig(t, checkoutYAML("bitbucket", "vertex", "aurora", ""))
		text := left(t, r)
		if !strings.Contains(text, "bitbucket-token") || strings.Contains(text, "claude") || strings.Contains(text, "anthropic") {
			t.Errorf("left = %q", text)
		}
		r.store.seed(bitbucketSlug, "bitbucket-token", "stored-bb-token-0001")
		for i := 0; i < 2; i++ { // and a rerun is the same
			res, err := r.run(t)
			if err != nil || stateOf(res, "secrets") != initflow.Done || len(res.Left) != 0 || r.store.setCalls() != 0 {
				t.Fatalf("run %d: %v, %+v", i, err, res.Stages)
			}
		}
	})
	t.Run("oauth", func(t *testing.T) {
		r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
		r.e.lc.Providers = map[string]config.ModelProvider{"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}}}
		text := left(t, r)
		if !strings.Contains(text, "bitbucket-token") || !strings.Contains(text, "claude setup-token\nfugaro secrets set claude-oauth-token") ||
			strings.Contains(text, "anthropic") || strings.Contains(text, "openrouter") {
			t.Errorf("left = %q", text)
		}
	})
	t.Run("api-key with a provider key and a workflow secret", func(t *testing.T) {
		r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
		r.writeConfig(t, checkoutYAML("bitbucket", "api-key", "aurora", ", secrets: [{ name: npm-token, env: NPM_TOKEN }]"))
		r.e.lc.Providers = map[string]config.ModelProvider{
			"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
			"elsewhere":  {Secret: "elsewhere-key", AllowDataTo: []string{"acme/other"}},
		}
		text := left(t, r)
		for _, want := range []string{"bitbucket-token", "anthropic-api-key", "openrouter-api-key", "npm-token"} {
			if !strings.Contains(text, "secrets set "+want+" --repo acme/app") {
				t.Errorf("left lacks %s: %q", want, text)
			}
		}
		if strings.Contains(text, "elsewhere") || strings.Contains(text, "claude") {
			t.Errorf("left = %q", text)
		}
	})
}

// Secret Manager refusing (no permission, API off) is the user's to fix, not
// a failure: exit 1, the cause and the fix on one line, no payload, nothing
// stored, and the stage after it does not run.
func TestSecretManagerRefusalIsNeedsYou(t *testing.T) {
	const val = "bb-secret-value-EXAMPLE"
	denied := func(msg string) error { return &googleapi.Error{Code: 403, Message: msg} }
	for name, tc := range map[string]struct {
		list, set error
		want      string
	}{
		"list denied":           {list: denied("denied for " + val), want: "roles/secretmanager.viewer"},
		"api disabled":          {list: denied("Secret Manager API has not been used in project 1 before or it is disabled"), want: "secretmanager.googleapis.com"},
		"store denied":          {set: denied("denied; payload " + val), want: "roles/secretmanager.secretVersionAdder"},
		"store denied (create)": {set: denied("denied; payload " + val), want: "secretmanager.secrets.create"},
		"store API off":         {set: denied("SERVICE_DISABLED " + val), want: "secretmanager.googleapis.com"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTTY(t)
			r := newSecretsRig(t, bitbucketOrigin, f.tty)
			r.store.listErr = tc.list
			if tc.set != nil {
				r.store.setHook = func(string, []byte) error { return tc.set }
			}
			repo := &stubStage{name: initflow.Repository}
			done := make(chan loopResult, 1)
			go func() {
				res, err := initflow.Run(context.Background(), []initflow.Stage{r.stage, repo}, r.e.options())
				done <- loopResult{res, err}
			}()
			if tc.set != nil {
				r.answer(t, f, "bitbucket-token (input hidden)", 1, val+"\n")
			}
			lr := waitLoop(t, done)
			res := lr.res
			if lr.err != nil || res.Failed != nil || res.ExitCode() != 1 || stateOf(res, "secrets") != initflow.NeedsYou || len(res.Left) != 1 {
				t.Fatalf("%v, %+v", lr.err, res)
			}
			if !strings.Contains(res.Left[0].Text, tc.want) || strings.ContainsAny(res.Left[0].Text, "\n\\") {
				t.Errorf("left = %q, want %q", res.Left[0].Text, tc.want)
			}
			if repo.applied || stateOf(res, "repository") != initflow.Blocked {
				t.Errorf("the next stage ran: %+v", res.Stages)
			}
			if len(r.store.secrets) != 0 {
				t.Error("something was stored")
			}
			noLeak(t, val, resultJSON(t, res), r.out.String(), r.errOut.String())
		})
	}
}

// With a coding agent's environment present the stage never prompts and
// never reads the terminal; it leaves the commands with the reason.
func TestAgentEnvNeverPrompts(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			f := newTTY(t)
			r := newSecretsRig(t, bitbucketOrigin, f.tty)
			t.Setenv(marker, "1")
			_, _ = f.master.WriteString(tokenValue + "\n")
			time.Sleep(100 * time.Millisecond)
			before := queued(t, f)
			res, err := r.run(t)
			if err != nil || stateOf(res, "secrets") != initflow.NeedsYou || r.store.setCalls() != 0 || strings.Contains(r.errOut.String(), "hidden") {
				t.Fatalf("%v, %+v, stderr %q", err, res.Stages, r.errOut.String())
			}
			if !strings.Contains(res.Left[0].Text, "not through a coding agent") {
				t.Errorf("left = %q", res.Left[0].Text)
			}
			// An IDE extension sets one marker in a person's own terminal: only
			// that refusal says how to get past it, and in no text an agent
			// could take as a recipe for the other markers.
			detail := ""
			for _, st := range res.Stages {
				if st.Name == "secrets" {
					detail = st.Detail
				}
			}
			if !strings.Contains(detail, "run fugaro secrets set in your own terminal window") {
				t.Errorf("detail = %q", detail)
			}
			if hint := strings.Contains(detail, "unset"); hint != (marker == "CLAUDE_CODE_SSE_PORT") {
				t.Errorf("the unset hint is for CLAUDE_CODE_SSE_PORT only: %s: %q", marker, detail)
			}
			if marker == "CLAUDE_CODE_SSE_PORT" && !strings.Contains(detail, "if this is your own IDE terminal") {
				t.Errorf("the hint is not worded for the person: %q", detail)
			}
			if queued(t, f) != before || before == 0 {
				t.Error("the terminal was read")
			}
		})
	}
}

// A redirected stderr or stdout (the prompt would go to a file and the user
// would type blind) is not a terminal: nothing is prompted or read.
func TestRedirectedOutputNeverPrompts(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty) // its out and err are buffers
	writerIsTerminal = realWriterIsTerminal
	_, _ = f.master.WriteString(tokenValue + "\n")
	time.Sleep(100 * time.Millisecond)
	before := queued(t, f)
	res, err := r.run(t)
	if err != nil || stateOf(res, "secrets") != initflow.NeedsYou || r.store.setCalls() != 0 || r.errOut.String() != "" || queued(t, f) != before {
		t.Fatalf("%v, %+v, stderr %q", err, res.Stages, r.errOut.String())
	}
}

// Escape sequences in a paste (arrow keys, the bracketed-paste markers some
// terminals wrap it in) are dropped, the terminal is already hidden when the
// prompt appears, and the prompt says Enter ends the paste.
func TestPEMPasteSurvivesEscapeSequences(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, githubOrigin, f.tty)
	done := r.start(context.Background())
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(r.errOut.String(), "github-app-key (input hidden)"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no prompt")
		}
	}
	if f.echo(t) {
		t.Error("the terminal still echoed when the prompt appeared")
	}
	if !strings.Contains(r.errOut.String(), "then press Enter") {
		t.Errorf("prompt: %q", r.errOut.String())
	}
	paste := strings.Replace(strings.ReplaceAll(pemValue, "\n", "\r"), "\rMIIE", "\r\x1b[AMIIE", 1)
	paste = "\x1b[200~" + strings.Replace(paste, "\rZmFr", "\r\x1bOPZmFr", 1) + "\x1b[201~\r" // an SS3 key (F1) mid-paste
	if _, err := f.master.WriteString(paste); err != nil {
		t.Fatal(err)
	}
	r.answer(t, f, "claude-oauth-token (input hidden)", 1, tokenValue+"\n")
	lr := waitLoop(t, done)
	if lr.err != nil || lr.res.Failed != nil {
		t.Fatalf("%v, %+v", lr.err, lr.res)
	}
	if got := r.store.latest(mustSlug("github", "acme/app"), "github-app-key"); got != pemValue {
		t.Fatalf("stored %q", got)
	}
}

// Every line of a held PEM is registered for redaction, not only the whole;
// the slots are the loop's own and are released; options() called twice keeps
// the one list.
func TestHeldValueLinesAreRedacted(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	first := r.e.options().Redact
	second := r.e.options().Redact
	release, err := r.e.hold([]byte(pemValue))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(pemValue, "\n") {
		if len(l) >= minSecretBytes && !slices.Contains(second, l) && l != pemValue {
			t.Errorf("line %q is not registered", l)
		}
	}
	if !slices.Contains(first, pemValue) || !slices.Contains(second, pemValue) {
		t.Error("the whole value is not in both lists")
	}
	release()
	for _, v := range first {
		if v != "" {
			t.Errorf("%q still held", v)
		}
	}
}

// Core dumps are off while a value is held, and restored after.
func TestNoCoreDumpsWhileHolding(t *testing.T) {
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &old); err != nil {
		t.Skip(err)
	}
	if old.Max == 0 {
		t.Skip("core dumps cannot be raised here")
	}
	raised := syscall.Rlimit{Cur: min(old.Max, 1<<20), Max: old.Max}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &raised); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = syscall.Setrlimit(syscall.RLIMIT_CORE, &old) })

	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty)
	var during syscall.Rlimit
	r.store.setHook = func(string, []byte) error {
		_ = syscall.Getrlimit(syscall.RLIMIT_CORE, &during)
		return nil
	}
	done := r.start(context.Background())
	r.bitbucketSession(t, f, "claude-oauth-token")
	waitLoop(t, done)
	var after syscall.Rlimit
	_ = syscall.Getrlimit(syscall.RLIMIT_CORE, &after)
	if during.Cur != 0 || after.Cur != raised.Cur {
		t.Errorf("core limit during %d, after %d (was %d)", during.Cur, after.Cur, raised.Cur)
	}
}

// A PEM needing more redaction slots than the engine keeps is refused, never
// held in part: a line left out of the redaction list could leak.
func TestHoldFailsClosedWhenSlotsRunOut(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	_ = r.e.options()
	pem := longPEM(200)
	release, err := r.e.hold([]byte(pem))
	if err == nil {
		release()
		t.Fatal("a 200-line value was held in part")
	}
	if strings.Contains(err.Error(), "EXAMPLE") || strings.Contains(err.Error(), "\n") {
		t.Errorf("the error carries the value: %q", err)
	}
	for _, v := range r.e.held {
		if v != "" {
			t.Errorf("%q is still held after the refusal", v)
		}
	}
	// What fits is still held.
	release, err = r.e.hold([]byte(longPEM(100)))
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// longPEM is a fake PEM of n lines in all, each one distinct.
func longPEM(n int) string {
	var b strings.Builder
	b.WriteString("-----BEGIN RSA PRIVATE KEY-----\n")
	for i := 0; i < n-2; i++ {
		fmt.Fprintf(&b, "MIIEowIBAAKCAQEAfake%04dEXAMPLE0000\n", i)
	}
	b.WriteString("-----END RSA PRIVATE KEY-----\n")
	return b.String()
}

// --json never prompts, even on a terminal in every other way: the stage is
// needs-you with the one-line commands, and nothing is read.
func TestJSONNeverPrompts(t *testing.T) {
	f := newTTY(t)
	r := newSecretsRig(t, bitbucketOrigin, f.tty, func(o *initOptions) { o.asJSON = true })
	_, _ = f.master.WriteString(tokenValue + "\n")
	time.Sleep(100 * time.Millisecond)
	before := queued(t, f)
	res, err := r.run(t)
	if err != nil || stateOf(res, "secrets") != initflow.NeedsYou || r.store.setCalls() != 0 || strings.Contains(r.errOut.String(), "hidden") {
		t.Fatalf("%v, %+v, stderr %q", err, res, r.errOut.String())
	}
	if len(res.Left) != 1 || !strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "secrets set bitbucket-token --repo acme/") {
		t.Errorf("left = %+v", res.Left)
	}
	if after := queued(t, f); after != before || before == 0 {
		t.Error("the terminal was read")
	}
}
