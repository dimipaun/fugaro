package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// pemValue is a fake key; its body lines are what a leak check looks for.
const pemValue = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAfakefakefakefake0000EXAMPLE\nZmFrZWZha2VmYWtl0000EXAMPLEEXAMPLE0000\n-----END RSA PRIVATE KEY-----\n"

// syncBuf is a buffer a test reads while the stage writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// memStore is a Secret Manager that keeps what it is given in memory, with
// failure injection. It has no read of a value either.
type memStore struct {
	mu      sync.Mutex
	secrets map[string]*memSecret
	sets    int
	listErr error
	// setHook, when set, is called with each value before it is stored; its
	// error fails the store, leaving nothing.
	setHook func(id string, value []byte) error
}

type memSecret struct {
	labels   map[string]string
	versions [][]byte
}

func newMemStore() *memStore { return &memStore{secrets: map[string]*memSecret{}} }

func (m *memStore) seed(slug, name string, value string) {
	label, _ := gcp.RepoLabel(slug)
	id := gcp.SecretID(slug, name)
	s := &memSecret{labels: map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}}
	if value != "" {
		s.versions = append(s.versions, []byte(value))
	}
	m.secrets[id] = s
}

func (m *memStore) List(_ context.Context, labels map[string]string) ([]gcp.SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []gcp.SecretInfo
	for id, s := range m.secrets {
		match := true
		for k, v := range labels {
			match = match && s.labels[k] == v
		}
		if !match {
			continue
		}
		info := gcp.SecretInfo{ID: id, Labels: s.labels, Versions: len(s.versions)}
		if n := len(s.versions); n > 0 {
			info.Latest = fmt.Sprint(n)
		}
		out = append(out, info)
	}
	return out, nil
}

func (m *memStore) Set(_ context.Context, id string, value []byte, labels map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets++
	if m.setHook != nil {
		if err := m.setHook(id, value); err != nil {
			return "", err
		}
	}
	s := m.secrets[id]
	if s == nil {
		s = &memSecret{labels: labels}
		m.secrets[id] = s
	}
	s.versions = append(s.versions, append([]byte(nil), value...))
	return fmt.Sprint(len(s.versions)), nil
}

func (m *memStore) latest(slug, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.secrets[gcp.SecretID(slug, name)]; s != nil && len(s.versions) > 0 {
		return string(s.versions[len(s.versions)-1])
	}
	return ""
}

func (m *memStore) setCalls() int { m.mu.Lock(); defer m.mu.Unlock(); return m.sets }

// secretsRig is the secrets stage in the loop, in a checkout of origin, with
// the given stdin.
type secretsRig struct {
	e      *initEngine
	stage  *secretsStage
	store  *memStore
	out    *syncBuf // the loop's account (stdout)
	errOut *syncBuf // prompts and warnings (stderr)
	dir    string   // the checkout
}

var realWriterIsTerminal = writerIsTerminal

// checkoutYAML is a fugaro.yaml of project aurora for provider, with agent.auth auth
// (and extra lines of the workflow, such as secrets).
func checkoutYAML(provider, auth, project string, workflowExtra string) string {
	return "version: 1\nproject: " + project + "\ngit: { provider: " + provider + " }\nagent: { auth: " + auth + " }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh }" + workflowExtra + " }\n"
}

// writeConfig (re)writes the checkout's fugaro.yaml.
func (r *secretsRig) writeConfig(t *testing.T, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSecretsRig makes a checkout whose origin is origin, runs from it, and
// builds the stage over a memStore (open is replaced; the fake-server tests
// use the real client instead).
func newSecretsRig(t *testing.T, origin string, in io.Reader, opts ...func(*initOptions)) *secretsRig {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", origin)
	t.Chdir(dir)
	r := &secretsRig{store: newMemStore(), out: &syncBuf{}, errOut: &syncBuf{}, dir: dir}
	cmd := NewRootCmd()
	cmd.SetIn(in)
	cmd.SetErr(r.errOut)
	o := &initOptions{}
	for _, f := range opts {
		f(o)
	}
	r.e = &initEngine{r: &initRun{o: o, w: r.out, cmd: cmd}, lc: &localcfg.Config{Name: "aurora"}}
	provider := "github"
	if strings.Contains(origin, "bitbucket") {
		provider = "bitbucket"
	}
	r.writeConfig(t, checkoutYAML(provider, "oauth", "aurora", ""))
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	writerIsTerminal = func(io.Writer) bool { return true } // the tests' buffers stand for the terminal
	t.Cleanup(func() { writerIsTerminal = realWriterIsTerminal })
	r.stage = newSecretsStage(r.e)
	r.stage.open = func(context.Context) (secretStore, error) { return r.store, nil }
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	return r
}

func (r *secretsRig) run(t *testing.T) (*initflow.Result, error) {
	t.Helper()
	return initflow.Run(context.Background(), []initflow.Stage{r.stage}, r.e.options())
}

// noLeak fails when any of texts holds value, or its base64 or JSON form,
// or (for a multi-line value) one of its longer lines.
func noLeak(t *testing.T, value string, texts ...string) {
	t.Helper()
	esc, _ := json.Marshal(value)
	forms := []string{value, base64.StdEncoding.EncodeToString([]byte(value)), strings.Trim(string(esc), `"`)}
	for _, l := range strings.Split(value, "\n") {
		if len(l) >= 12 {
			forms = append(forms, l)
		}
	}
	for _, text := range texts {
		for _, f := range forms {
			if strings.Contains(text, f) {
				t.Fatalf("a secret value reached an output: %q in %q", f, text)
			}
		}
	}
}

func resultJSON(t *testing.T, res *initflow.Result) string {
	t.Helper()
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stateOf(res *initflow.Result, name string) initflow.State {
	for _, s := range res.Stages {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

const (
	githubOrigin    = "https://github.com/acme/app.git"
	bitbucketOrigin = "https://bitbucket.org/acme/app.git"
)

var bitbucketSlug = mustSlug("bitbucket", "acme/app")

// readerSpy is stdin that is not a file, and notes any read.
type readerSpy struct {
	io.Reader
	read bool
}

func (s *readerSpy) Read(p []byte) (int, error) { s.read = true; return s.Reader.Read(p) }

// Stdin that is not a terminal is never read for values: not a spy reader,
// not a pipe holding a value, not even when something claims it is a
// terminal. The stage leaves the one-line commands instead and stores nothing.
func TestNonTTYNeverReadForValues(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		spy := &readerSpy{Reader: strings.NewReader(tokenValue + "\n")}
		r := newSecretsRig(t, bitbucketOrigin, spy)
		res, err := r.run(t)
		if err != nil {
			t.Fatal(err)
		}
		if spy.read || r.store.setCalls() != 0 || stateOf(res, "secrets") != initflow.NeedsYou || len(res.Left) != 1 {
			t.Fatalf("read %v, sets %d, stages %+v, left %v", spy.read, r.store.setCalls(), res.Stages, res.Left)
		}
		noLeak(t, tokenValue, resultJSON(t, res), r.out.String(), r.errOut.String())
	})
	t.Run("pipe", func(t *testing.T) {
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pr.Close() })
		if _, err := pw.WriteString(tokenValue + "\n"); err != nil {
			t.Fatal(err)
		}
		r := newSecretsRig(t, bitbucketOrigin, pr)
		if _, err := r.run(t); err != nil {
			t.Fatal(err)
		}
		pw.Close()
		if rest, _ := io.ReadAll(pr); string(rest) != tokenValue+"\n" || r.store.setCalls() != 0 {
			t.Fatalf("the pipe was read: %q left; sets %d", rest, r.store.setCalls())
		}
	})
	// Even when the terminal test is fooled, the stage reads only a real
	// terminal file: a pipe or a reader still is not one.
	t.Run("claimed terminal", func(t *testing.T) {
		old := stdinIsTerminal
		stdinIsTerminal = func(io.Reader) bool { return true }
		t.Cleanup(func() { stdinIsTerminal = old })
		pr, pw, _ := os.Pipe()
		t.Cleanup(func() { pr.Close(); pw.Close() })
		_, _ = pw.WriteString(tokenValue + "\n")
		for _, in := range []io.Reader{pr, &readerSpy{Reader: strings.NewReader(tokenValue + "\n")}} {
			r := newSecretsRig(t, bitbucketOrigin, in)
			res, err := r.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if spy, ok := in.(*readerSpy); ok && spy.read {
				t.Fatal("a reader was read")
			}
			if r.store.setCalls() != 0 || stateOf(res, "secrets") != initflow.NeedsYou {
				t.Fatalf("sets %d, stages %+v", r.store.setCalls(), res.Stages)
			}
		}
		pw.Close()
		if rest, _ := io.ReadAll(pr); string(rest) != tokenValue+"\n" {
			t.Fatalf("the pipe was read: %q left", rest)
		}
	})
}

// The commands left for the user are one line each, name the secret and the
// repository, use the binary path the user ran (quoted as one word), and
// hold no value, no continuation and no heredoc.
func TestPrintedCommandsHaveNoValues(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   []string
	}{
		{githubOrigin, []string{"fugaro secrets set github-app-key --repo acme/app < PATH-TO-THE-KEY-FILE", "claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/app"}},
		{bitbucketOrigin, []string{"fugaro secrets set bitbucket-token --repo acme/app", "claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/app"}},
	} {
		r := newSecretsRig(t, tc.origin, strings.NewReader(""))
		res, err := r.run(t)
		if err != nil || len(res.Left) != 1 {
			t.Fatalf("%v, left %v", err, res.Left)
		}
		text := res.Left[0].Text
		if strings.ContainsAny(text, "\n\r\\") || res.Left[0].Kind != initflow.LeftCommand {
			t.Errorf("not one plain line: %+v", res.Left[0])
		}
		if got := res.Left[0].Commands; fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("commands %q, want %q", got, tc.want)
		}
		for _, c := range res.Left[0].Commands {
			if strings.ContainsAny(c, "\n\r\\;") || strings.Contains(c, "<<") || strings.Contains(c, "&&") {
				t.Errorf("not one plain command: %q", c)
			}
		}
		if !strings.Contains(r.out.String(), "left for you (secrets, command): "+text+"\n    "+strings.Join(tc.want, "\n    ")+"\n") {
			t.Errorf("not printed once, whole: %q", r.out.String())
		}
	}
}

func TestSelfCommandQuotesTheBinaryPath(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	for arg, want := range map[string]string{
		"fugaro":                "fugaro",
		"/usr/local/bin/fugaro": "/usr/local/bin/fugaro",
		"./bin/fugaro":          "./bin/fugaro",
		"/My Tools/fugaro":      "'/My Tools/fugaro'",
		"/it's/fugaro":          `'/it'\''s/fugaro'`,
		"/a\nb":                 "fugaro",
	} {
		os.Args = []string{arg}
		if got := selfCommand(); got != want {
			t.Errorf("selfCommand(%q) = %q, want %q", arg, got, want)
		}
	}
}

// Secrets are per repository (Q4): another repository's stored secret does
// not count, and what the stage names carries this repository's slug.
func TestSecretsArePerRepository(t *testing.T) {
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	other := mustSlug("bitbucket", "acme/other")
	r.store.seed(other, "bitbucket-token", "someone-elses-value")
	r.store.seed(other, "claude-oauth-token", "someone-elses-value")
	res, err := r.run(t)
	if err != nil || stateOf(res, "secrets") != initflow.NeedsYou {
		t.Fatalf("%v, %+v", err, res.Stages)
	}
	if !strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "--repo acme/app") || strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "other") {
		t.Errorf("left = %q", res.Left[0].Text)
	}
	if gcp.SecretID(bitbucketSlug, "bitbucket-token") == gcp.SecretID(other, "bitbucket-token") {
		t.Error("two repositories share a secret ID")
	}
}

// Lookups that cannot work yet (the installation is not applied) do not
// fail the preview: the stage plans to ask, and the commands name every
// candidate.
func TestSecretsCheckToleratesAnUnreadableStore(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.store.listErr = errors.New("secret manager is not enabled")
	res, err := r.run(t)
	if err != nil || res.Failed != nil {
		t.Fatalf("%v, %+v", err, res.Failed)
	}
	if stateOf(res, "secrets") != initflow.NeedsYou || !strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "github-app-key") || !strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "claude-oauth-token") {
		t.Errorf("stages %+v, left %v", res.Stages, res.Left)
	}
}

// Outside a checkout, or in one of another host's, the stage has nothing to take.
func TestSecretsSkippedOutsideAGithubOrBitbucketCheckout(t *testing.T) {
	r := newSecretsRig(t, "https://gitlab.example/acme/app.git", strings.NewReader(""))
	_ = os.Remove(filepath.Join(r.dir, "fugaro.yaml"))
	r.e.lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: "gitlab"}}
	res, err := r.run(t)
	if err != nil || stateOf(res, "secrets") != initflow.Skipped {
		t.Fatalf("%v, %+v", err, res.Stages)
	}
	r = newSecretsRig(t, githubOrigin, strings.NewReader(""))
	t.Chdir(t.TempDir())
	res, err = r.run(t)
	if err != nil || stateOf(res, "secrets") != initflow.Skipped {
		t.Fatalf("%v, %+v", err, res.Stages)
	}
}

// A checkout that is not this project's is skipped, and nothing is created
// for it: a fugaro.yaml of another project, a provider the local config and
// the file disagree on, a repository the project does not know.
func TestSecretsSkippedForAnUnrelatedCheckout(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml  string // "" removes the file
		local string // the local config's provider for acme/app
	}{
		"another project":       {yaml: checkoutYAML("github", "oauth", "other", "")},
		"no project":            {yaml: checkoutYAML("github", "oauth", "", "")},
		"provider disagreement": {yaml: checkoutYAML("github", "oauth", "aurora", ""), local: "bitbucket"},
		"unknown repository":    {yaml: ""},
	} {
		t.Run(name, func(t *testing.T) {
			spy := &readerSpy{Reader: strings.NewReader("")}
			r := newSecretsRig(t, githubOrigin, spy)
			if tc.yaml == "" {
				if err := os.Remove(filepath.Join(r.dir, "fugaro.yaml")); err != nil {
					t.Fatal(err)
				}
			} else {
				r.writeConfig(t, tc.yaml)
			}
			if tc.local != "" {
				r.e.lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: tc.local}}
			}
			res, err := r.run(t)
			if err != nil || stateOf(res, "secrets") != initflow.Skipped || r.store.setCalls() != 0 || len(r.store.secrets) != 0 || len(res.Left) != 0 {
				t.Fatalf("%v, %+v", err, res.Stages)
			}
		})
	}
}

// The local config's entry alone onboards a checkout without a fugaro.yaml:
// the git credential is asked for, the Claude credential is not assumed, and
// the stage says so.
func TestSecretsWithoutFugaroYAMLTakesOnlyTheGitCredential(t *testing.T) {
	r := newSecretsRig(t, "https://git.example.com/acme/app.git", strings.NewReader(""))
	if err := os.Remove(filepath.Join(r.dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	r.e.lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: "bitbucket"}}
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 || strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "claude") || !strings.Contains(strings.Join(res.Left[0].Commands, "\n"), "bitbucket-token") {
		t.Fatalf("%v, %+v", err, res)
	}
	var detail string
	for _, st := range res.Stages {
		detail = st.Detail
	}
	if !strings.Contains(detail, "no fugaro.yaml") {
		t.Errorf("the assumption is not said: %q", detail)
	}
}

// The secrets stage is ahead of the repository stage (whose first image
// build needs them stored) and --yes cannot reach it; a stage after it does
// not run while it needs the user.
func TestSecretsBeforeFirstBuild(t *testing.T) {
	names := initflow.Names()
	if slices.Index(names, initflow.Secrets) > slices.Index(names, initflow.Repository) || !slices.Contains(initflow.Needs(initflow.Repository), initflow.Secrets) {
		t.Fatalf("order %v", names)
	}
	if initflow.YesCovers(initflow.Secrets) {
		t.Fatal("--yes covers the secrets stage")
	}
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""), func(o *initOptions) { o.yes = true })
	repo := &stubStage{name: initflow.Repository}
	res, err := initflow.Run(context.Background(), []initflow.Stage{repo, r.stage}, r.e.options())
	if err != nil {
		t.Fatal(err)
	}
	if repo.applied || stateOf(res, "repository") != initflow.Blocked {
		t.Fatalf("the repository stage ran, or is not blocked: %+v", res.Stages)
	}
	// Once the secrets are stored, it runs.
	r.store.seed(bitbucketSlug, "bitbucket-token", "stored-already-0001")
	r.store.seed(bitbucketSlug, "claude-oauth-token", "stored-already-0002")
	res, err = initflow.Run(context.Background(), []initflow.Stage{repo, r.stage}, r.e.options())
	if err != nil || !repo.applied || stateOf(res, "secrets") != initflow.Done {
		t.Fatalf("%v, %+v", err, res.Stages)
	}
}

type stubStage struct {
	name    string
	applied bool
}

func (s *stubStage) Name() string { return s.name }
func (s *stubStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo}, nil
}
func (s *stubStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{}, nil
}
func (s *stubStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	s.applied = true
	return initflow.Outcome{Changed: true}, nil
}
func (s *stubStage) Verify(context.Context) error { return nil }
func (s *stubStage) Left() initflow.Left          { return initflow.Left{} }

// Through the init command, with the real Secret Manager client against the
// fake: --yes and --non-interactive leave the commands, read nothing from
// stdin, and store nothing; with the secrets stored, init finishes.
func TestInitSecretsStageThroughTheCommand(t *testing.T) {
	r := newInitRig(t)
	dir := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git") // the rig's local config knows it
	t.Chdir(dir)
	for _, flags := range [][]string{{"--yes"}, {"--non-interactive", "--yes"}, {"--non-interactive"}} {
		spy := &readerSpy{Reader: strings.NewReader(tokenValue + "\n")}
		cmd := NewRootCmd()
		var out, errOut bytes.Buffer
		cmd.SetIn(spy)
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(append([]string{"init", "--json"}, flags...))
		err := cmd.Execute()
		if ExitCode(err) != ExitUserError {
			t.Fatalf("%v: exit %d, err %v\n%s", flags, ExitCode(err), err, errOut.String())
		}
		var res convergeJSON
		if jerr := json.Unmarshal(out.Bytes(), &res); jerr != nil {
			t.Fatalf("%v:\n%s", jerr, out.String())
		}
		if spy.read {
			t.Fatalf("%v: stdin was read", flags)
		}
		if res.state("secrets") != "needs-you" && !(len(flags) == 1 && flags[0] == "--non-interactive") {
			t.Fatalf("%v: stages %+v", flags, res.Stages)
		}
		if len(res.Left) > 0 {
			last := res.Left[len(res.Left)-1]
			if last["stage"] == "secrets" && !strings.Contains(last["commands"], "secrets set bitbucket-token --repo acme/sandbox") {
				t.Fatalf("%v: left %v", flags, res.Left)
			}
		}
		noLeak(t, tokenValue, out.String(), errOut.String())
		for _, rq := range r.sm.Requests() {
			if rq.Method == "POST" {
				t.Fatalf("%v: the stage wrote to Secret Manager: %s %s", flags, rq.Method, rq.Path)
			}
		}
	}
	// Stored: the stage is done and init finishes.
	sandbox := mustSlug("bitbucket", "acme/sandbox")
	label, _ := gcp.RepoLabel(sandbox)
	labels := func(name string) map[string]string {
		return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}
	}
	r.sm.Seed(gcp.SecretID(sandbox, "bitbucket-token"), labels("bitbucket-token"), []byte(tokenValue))
	out, _, err := executeStdin(t, "", "init", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res convergeJSON
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || res.state("secrets") != "done" {
		t.Fatalf("%v: %+v", jerr, res.Stages)
	}
}
