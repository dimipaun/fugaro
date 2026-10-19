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
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
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
	lists   int
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
	m.lists++
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

// seedProject stores a project-wide copy: the labels fugaro secrets set
// --project-wide writes and every project-wide listing reads (design §4),
// under the installation's own ID (gcp.ProjectSecretID), not a repository's.
func (m *memStore) seedProject(project, name, value string) {
	s := &memSecret{labels: map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: project, gcp.LabelSecret: name}}
	if value != "" {
		s.versions = append(s.versions, []byte(value))
	}
	m.secrets[gcp.ProjectSecretID(project, name)] = s
}

func (m *memStore) latest(slug, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.secrets[gcp.SecretID(slug, name)]; s != nil && len(s.versions) > 0 {
		return string(s.versions[len(s.versions)-1])
	}
	return ""
}

// latestProject is latest for a project-wide copy (D2's default scope for a
// name that has a project-wide meaning, gcp.ProjectSecretID).
func (m *memStore) latestProject(project, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.secrets[gcp.ProjectSecretID(project, name)]; s != nil && len(s.versions) > 0 {
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
	return "version: 1\nproject: " + project + "\ngit: { provider: " + provider + " }\nagent: { auth: " + auth + " }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh }" + workflowExtra + " }\n"
}

// writeConfig (re)writes the checkout's fugaro.yaml.
// The stage reads the default branch's file (a checkout's working tree is not
// to be trusted), so it is committed and fetched as origin/main.
func (r *secretsRig) writeConfig(t *testing.T, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, r.dir, "add", "fugaro.yaml")
	testutil.Git(t, r.dir, "commit", "-q", "--allow-empty", "-m", "fugaro.yaml")
	fetched(t, r.dir)
}

// removeConfig is a default branch with no fugaro.yaml.
func (r *secretsRig) removeConfig(t *testing.T) {
	t.Helper()
	testutil.Git(t, r.dir, "rm", "-q", "-f", "fugaro.yaml")
	testutil.Git(t, r.dir, "commit", "-q", "-m", "no fugaro.yaml")
	fetched(t, r.dir)
}

// newSecretsRig makes a checkout whose origin is origin, runs from it, and
// builds the stage over a memStore (open is replaced; the fake-server tests
// use the real client instead).
func newSecretsRig(t *testing.T, origin string, in io.Reader, opts ...func(*initOptions)) *secretsRig {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
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
	// The repository is the project's own (the local config lists it): the
	// hostile-checkout gate has its own tests.
	r.e.lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: provider}}
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

// The commands left for the user are one line each, name the secret and --
// for a name that keeps repository scope -- the repository, use the binary
// path the user ran (quoted as one word), and hold no value, no continuation
// and no heredoc. D2 stores a swept-in reserved name project-wide, where no
// repository belongs in the command, so those lines carry no --repo.
func TestPrintedCommandsHaveNoValues(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   []string
	}{
		{githubOrigin, []string{"fugaro secrets set github-app-key < PATH-TO-THE-KEY-FILE", "claude setup-token", "fugaro secrets set claude-oauth-token --repo acme/app"}},
		// bitbucket-token's own command carries --repo: a Bitbucket
		// credential is a repository access token, so it has no project-wide
		// default (config.PerRepositorySecret, owner ruling of 2026-10-15).
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

// A repository the project's own allow_data_to does not name keeps
// repository scope (code review, high): the scope decision is per repository
// (infra.ProjectSecretCandidate, the same answer find() reads to call a name
// covered), so project_secrets.bitbucket-token listing acme/other alone
// leaves acme/app's own command naming acme/app's copy -- the container its
// job will mount -- rather than the shared copy it may not mount. A
// repo-unaware test called the name project-scope here, because the list is
// non-empty, and the command it printed stored a value over acme/other's.
func TestLeftKeepsRepoScopeForARepoTheListExcludes(t *testing.T) {
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	r.e.lc.ProjectSecrets = map[string]config.ProjectSecret{
		"bitbucket-token": {AllowDataTo: []string{"acme/other"}},
	}
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 {
		t.Fatalf("%v, left %v", err, res.Left)
	}
	want := []string{
		"fugaro secrets set bitbucket-token --repo acme/app",
		"claude setup-token",
		"fugaro secrets set claude-oauth-token --repo acme/app",
	}
	if got := res.Left[0].Commands; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("commands %q, want %q", got, want)
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
	r.removeConfig(t)
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
				r.removeConfig(t)
			} else {
				r.writeConfig(t, tc.yaml)
			}
			r.e.lc.Repos = nil // the project does not list it, unless the case says it does
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
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	r.removeConfig(t)
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

// A repository the local config allows two named Claude accounts for is
// asked for both, each as its own claude setup-token + secrets set pair, in
// addition to the default; a name that already has a stored version is not
// asked for again.
func TestInitSecretsListsEveryAllowedName(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.e.lc.ClaudeAccounts = map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app"}},
		"acme-overflow": {AllowDataTo: []string{"acme/app"}},
		"other-team":    {AllowDataTo: []string{"acme/other"}}, // not this repository
	}
	r.store.seed(mustSlug("github", "acme/app"), "claude-oauth-token-acme-overflow", "stored-already-0001")
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 {
		t.Fatalf("%v, left %v", err, res.Left)
	}
	// The whole list, not a piece of it: the default's pair, then
	// acme-personal's, with nothing for the name that already has a stored
	// version and nothing for the one this repository is not allowed.
	// strings.Contains over the joined lines would pass for a command that
	// only *begins* with the wanted text — a different name suffix, another
	// --repo, another flag — which is the shape this test exists to pin.
	wantCmds := []string{
		"fugaro secrets set github-app-key < PATH-TO-THE-KEY-FILE",
		"claude setup-token",
		"fugaro secrets set claude-oauth-token --repo acme/app",
		"claude setup-token",
		"fugaro secrets set claude-oauth-token-acme-personal",
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if got := res.Left[0].Commands; fmt.Sprint(got) != fmt.Sprint(wantCmds) {
		t.Errorf("commands %q, want %q", got, wantCmds)
	}
	if strings.Contains(cmds, "acme-overflow") {
		t.Errorf("a stored name was asked for again: %q", cmds)
	}
	if strings.Contains(cmds, "other-team") {
		t.Errorf("a name this repository is not allowed got asked for: %q", cmds)
	}
	if n := strings.Count(cmds, "claude setup-token"); n != 2 {
		t.Errorf("claude setup-token count = %d, want 2 (default + acme-personal)", n)
	}
}

// TestInitSecretsSkipsNamedAccountsForApiKeyAuth: the named-account prompts
// are gated on agent.auth: oauth exactly like the bare default secret is —
// api-key auth means no Claude OAuth secret is relevant at all, allowed
// accounts configured or not. Review finding (LOW test gap): a mutation
// that always runs the gate (auth checked or not) stayed green before this
// test existed, since every other case here uses oauth.
func TestInitSecretsSkipsNamedAccountsForApiKeyAuth(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.e.lc.ClaudeAccounts = map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app"}},
	}
	r.writeConfig(t, checkoutYAML("github", "api-key", "aurora", ""))
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 {
		t.Fatalf("%v, left %v", err, res.Left)
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if strings.Contains(cmds, "claude") {
		t.Errorf("api-key auth prompted for a Claude credential: %q", cmds)
	}
}

// TestInitFiltersProviderSecretNamedLikeClaudeOAuth is a true end-to-end
// exercise of resolve()'s allowedClaudeOAuthMounts call (not just the
// function in isolation, which TestAllowedClaudeOAuthMountsDropsDisallowedNames
// already pins): a model provider's own secret name is free-form and
// config.Validate does not police its shape, so it can still land on
// claude-oauth-token-<x> by accident or typo, with cfg != nil (unlike the
// workflow-secret probe below, which config.Validate now makes
// unreachable, cfg == nil). Even though nothing stops the local config
// from being misconfigured this way, init must never treat it as a
// legitimate named Claude account's secret.
func TestInitFiltersProviderSecretNamedLikeClaudeOAuth(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.e.lc.Providers = map[string]config.ModelProvider{
		"oops": {Kind: config.ModelProviderKind, BaseURL: "https://example.invalid", Auth: "bearer",
			Secret: "claude-oauth-token-evil", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
	}
	r.writeConfig(t, checkoutYAML("github", "api-key", "aurora", ""))
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 {
		t.Fatalf("%v, left %v", err, res.Left)
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if strings.Contains(cmds, "claude-oauth-token-evil") {
		t.Fatalf("a provider secret that merely looks like a named Claude account was prompted for: %q", cmds)
	}
}

// TestTakeRefusesDisallowedClaudeOAuthName unit-tests take()'s own
// independent backstop directly (not through Apply(), which resolve()'s
// filter already keeps such a name out of tg.wanted for, so a disallowed
// name never reaches take() on the normal path): even called for a name
// resolve() should never have offered, take() refuses it itself before
// reading or storing anything, so the backstop holds even if resolve()'s
// filter were ever wrong or bypassed.
func TestTakeRefusesDisallowedClaudeOAuthName(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.stage.tg = &secretTarget{repo: "acme/app", slug: appSlug}
	err := r.stage.take(context.Background(), r.store, nil, io.Discard, "claude-oauth-token-nobody")
	if err == nil || !strings.Contains(err.Error(), "not a configured claude_accounts name") {
		t.Fatalf("err = %v", err)
	}
	if r.store.setCalls() != 0 {
		t.Fatal("a value was stored despite the refusal")
	}
}

// TestTakeRefusesCodexOAuthName is the same independent backstop for the
// Codex harness's reserved names (config.CodexOAuthToken and the whole
// codex-oauth-token-<name> namespace, owner ruling of 2026-10-18):
// allowedHarnessOAuthMounts keeps them out of tg.wanted, and take() refuses
// one itself — before the prompt, before the value is read, before anything
// is stored — even called for a name that filter should never have offered.
func TestTakeRefusesCodexOAuthName(t *testing.T) {
	for _, name := range []string{"codex-oauth-token", "codex-oauth-token-acme"} {
		spy := &readerSpy{Reader: strings.NewReader(tokenValue + "\n")}
		r := newSecretsRig(t, githubOrigin, spy)
		r.stage.tg = &secretTarget{repo: "acme/app", slug: appSlug}
		err := r.stage.take(context.Background(), r.store, nil, io.Discard, name)
		if err == nil || !strings.Contains(err.Error(), "reserved for the Codex harness's OAuth credential") {
			t.Fatalf("%s: err = %v", name, err)
		}
		if spy.read {
			t.Fatalf("%s: the value was read before the refusal", name)
		}
		if r.store.setCalls() != 0 {
			t.Fatalf("%s: a value was stored despite the refusal", name)
		}
		if s := r.out.String() + r.errOut.String(); strings.Contains(s, name) {
			t.Fatalf("%s: a prompt was written for a reserved name: %q", name, s)
		}
	}
}

// TestTakeRefusesAnotherProvidersConventionalName: the name reserved for a
// configured provider's key is refused by take() too when the provider's key
// is stored under another name — the same independent backstop, for the same
// reason as the Codex names — while the provider's own key is what this stage
// exists to take. A refusal here is what keeps init from storing a workflow's
// value under a name that becomes the provider's credential the day the key is
// renamed to the convention.
func TestTakeRefusesAnotherProvidersConventionalName(t *testing.T) {
	const key = "not-a-real-key-1234"
	// openrouter's key is off convention on purpose: openrouter-api-key is
	// then a name reserved for it and nobody's .secret today.
	provider := config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api",
		Auth: "bearer", Secret: "other-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}}

	spy := &readerSpy{Reader: strings.NewReader(key + "\n")}
	r := newSecretsRig(t, githubOrigin, spy)
	r.e.lc.Providers = map[string]config.ModelProvider{"openrouter": provider}
	r.stage.tg = &secretTarget{repo: "acme/app", slug: appSlug}
	err := r.stage.take(context.Background(), r.store, nil, io.Discard, "openrouter-api-key")
	if err == nil || !strings.Contains(err.Error(), "reserved for model provider openrouter") {
		t.Fatalf("err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if r.store.setCalls() != 0 {
		t.Fatal("a value was stored despite the refusal")
	}

	// The provider's own key is still asked for: the reservation is about the
	// name the convention gives a provider's key, never about the key itself.
	r2 := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r2.e.lc.Providers = map[string]config.ModelProvider{"openrouter": provider}
	// A pin openrouter claims, so its key is mounted and therefore wanted.
	r2.writeConfig(t, "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key, model: deepseek/deepseek-v4-flash }\n"+
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	res, err := r2.run(t)
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if !strings.Contains(cmds, "secrets set other-key") {
		t.Fatalf("the provider's own key is not asked for: %v", res.Left)
	}
	if strings.Contains(cmds, "openrouter-api-key") {
		t.Fatalf("a name reserved for openrouter's key was asked for: %v", res.Left)
	}
}

// TestInitNeverPromptsForWorkflowDeclaredClaudeOAuthName is the review's
// exact probe: a workflow's own secrets: names
// claude-oauth-token-other-team, where other-team is a real
// claude_accounts entry this repository is not even allowed (allow_data_to
// lists only acme/other). This must never get the "claude setup-token"
// treatment a legitimate named account gets, and must never be asked for
// or stored, whatever layer catches it. config.Validate
// (internal/config/validate.go) now refuses to parse a fugaro.yaml that
// declares a secret in this namespace at all, so this checkout's cfg comes
// back nil and the stage falls back to asking only for the git credential
// — proving the probe is defeated end-to-end, not merely that one
// function's return value changed.
func TestInitNeverPromptsForWorkflowDeclaredClaudeOAuthName(t *testing.T) {
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	r.e.lc.ClaudeAccounts = map[string]config.ClaudeAccount{
		"other-team": {AllowDataTo: []string{"acme/other"}}, // not this repository
	}
	r.writeConfig(t, "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n"+
		"  app: { commands: { build: sh build.sh, test: sh test.sh }, secrets: [{ name: claude-oauth-token-other-team, env: FOO_TOKEN }] }\n")
	res, err := r.run(t)
	if err != nil || len(res.Left) != 1 {
		t.Fatalf("%v, left %v", err, res.Left)
	}
	cmds := strings.Join(res.Left[0].Commands, "\n")
	if strings.Contains(cmds, "other-team") || strings.Contains(cmds, "FOO_TOKEN") {
		t.Fatalf("the workflow-declared claude-oauth-token-<name> secret was prompted for: %q", cmds)
	}
	var detail string
	for _, st := range res.Stages {
		detail = st.Detail
	}
	if !strings.Contains(detail, "no fugaro.yaml") {
		t.Errorf("the invalid fugaro.yaml was not treated as absent: %q", detail)
	}
}

// TestAllowedHarnessOAuthMountsDropsDisallowedNames is a surgical,
// bypass-the-YAML-pipeline unit test of resolve()'s defense-in-depth
// filter: config.Validate is supposed to stop infra.SecretMounts from ever
// carrying a claude-oauth-token-<name> entry a workflow declared itself
// (TestInitNeverPromptsForWorkflowDeclaredClaudeOAuthName proves that end
// to end), but this filter must not assume that upstream refusal ran —
// constructing the mounts slice directly, as SecretMounts would return it
// for an (otherwise impossible, post-Validate) workflow secret of that
// shape, proves the filter itself drops it independently. The Codex names
// (codex-oauth-token, reserved ahead of the harness) and the name reserved
// for a configured provider's key are dropped by the same filter, and the
// provider's *own* key mount (the conventional name, actually in use) is
// kept.
func TestAllowedHarnessOAuthMountsDropsDisallowedNames(t *testing.T) {
	accounts := map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app"}},
		"other-team":    {AllowDataTo: []string{"acme/other"}}, // not acme/app
	}
	providers := map[string]config.ModelProvider{
		// Its key IS the conventional name: the mount SecretMounts builds for
		// it must survive the filter.
		"openrouter": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
		// Its key is not: `deepseek-api-key` is reserved for it and belongs to
		// no mount of anyone else's.
		"deepseek": {Kind: config.ModelProviderKind, BaseURL: "https://api.deepseek.com/anthropic", Auth: "bearer",
			Secret: "ds-key", Models: []string{"qwen/*"}, AllowDataTo: []string{"acme/app"}},
	}
	mounts := []infra.SecretMount{
		{Logical: "github-app-key", Env: "FUGARO_GITHUB_APP_PRIVATE_KEY"},
		{Logical: "claude-oauth-token", Env: "CLAUDE_CODE_OAUTH_TOKEN"},
		{Logical: "claude-oauth-token-acme-personal", Env: "SHOULD_NOT_APPEAR"},        // allowed
		{Logical: "claude-oauth-token-other-team", Env: "FOO_TOKEN"},                   // configured, not allowed here
		{Logical: "claude-oauth-token-nobody", Env: "BAR_TOKEN"},                       // not configured at all
		{Logical: "codex-oauth-token", Env: "CODEX_TOKEN"},                             // reserved: no Codex harness
		{Logical: "codex-oauth-token-acme", Env: "CODEX_ACME_TOKEN"},                   // reserved: not a workflow's
		{Logical: "deepseek-api-key", Env: "DS_TOKEN"},                                 // reserved for deepseek's key
		{Logical: "openrouter-api-key", Env: "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY"}, // openrouter's own key
		{Logical: "npm-token", Env: "NPM_TOKEN"},                                       // an ordinary workflow secret, untouched
	}
	got := allowedHarnessOAuthMounts(&localcfg.Config{ClaudeAccounts: accounts, Providers: providers}, "acme/app", mounts)
	var logicals []string
	for _, m := range got {
		logicals = append(logicals, m.Logical)
	}
	want := []string{"github-app-key", "claude-oauth-token", "claude-oauth-token-acme-personal", "openrouter-api-key", "npm-token"}
	if strings.Join(logicals, ",") != strings.Join(want, ",") {
		t.Fatalf("allowedHarnessOAuthMounts = %v, want %v", logicals, want)
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

// projectSharedForApp is the local config shape Task 9 exists for: the names
// every kind of secret may be stored project-wide under, all allowed for
// acme/app -- the reserved ones (the git credential, the default Claude
// account's bare name) through project_secrets, a model provider's key
// through its own allow_data_to.
func projectSharedForApp(r *secretsRig, repo string) {
	r.e.lc.ProjectSecrets = map[string]config.ProjectSecret{
		"bitbucket-token":    {AllowDataTo: []string{repo}},
		"claude-oauth-token": {AllowDataTo: []string{repo}},
	}
	r.e.lc.Providers = map[string]config.ModelProvider{
		"openrouter": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{repo}},
	}
}

// A name that may be stored project-wide is not missing when the
// installation's copy has a version and the repository's own copy has none.
// Before this, find() looked only at the repository's ID: at a terminal init
// prompted for a value the project already holds (and storing it into the
// repository's own copy would then win forever, a silent precedence flip --
// exactly the re-entry project-wide secrets exist to remove), and without a
// terminal it returned NeedsYou and blocked the init.
func TestInitSecretsFindSkipsNameWithProjectVersion(t *testing.T) {
	ctx := context.Background()

	t.Run("project_secrets: the git credential and the Claude account", func(t *testing.T) {
		r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
		projectSharedForApp(r, "acme/app")
		r.stage.resolve(ctx)
		want := []string{"bitbucket-token", "claude-oauth-token"}
		before, err := r.stage.find(ctx, r.store)
		if err != nil || !slices.Equal(before, want) {
			t.Fatalf("with nothing stored: find = %v, %v; want %v", before, err, want)
		}
		for _, name := range want {
			if _, ok := infra.ProjectSecretCandidate(r.e.lc, name, "acme/app"); !ok {
				t.Fatalf("%s is not a project candidate here: the test would prove nothing", name)
			}
			r.store.seedProject(r.e.lc.Name, name, tokenValue)
		}
		after, err := r.stage.find(ctx, r.store)
		if err != nil || len(after) != 0 {
			t.Fatalf("with the project copy stored: find = %v, %v; want nothing missing", after, err)
		}
	})

	t.Run("a model provider's key", func(t *testing.T) {
		r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
		projectSharedForApp(r, "acme/app")
		r.writeConfig(t, checkoutYAML("github", "api-key", "aurora", "")) // when a provider is mounted at all
		r.stage.resolve(ctx)
		slug := mustSlug("github", "acme/app")
		r.store.seed(slug, "github-app-key", tokenValue)
		r.store.seed(slug, "anthropic-api-key", tokenValue)
		if got, err := r.stage.find(ctx, r.store); err != nil || !slices.Equal(got, []string{"openrouter-api-key"}) {
			t.Fatalf("find = %v, %v; want only the provider's key missing", got, err)
		}
		if _, ok := infra.ProjectSecretCandidate(r.e.lc, "openrouter-api-key", "acme/app"); !ok {
			t.Fatal("the provider's key is not a project candidate here: the test would prove nothing")
		}
		r.store.seedProject(r.e.lc.Name, "openrouter-api-key", tokenValue)
		if got, err := r.stage.find(ctx, r.store); err != nil || len(got) != 0 {
			t.Fatalf("find = %v, %v; want nothing missing", got, err)
		}
	})

	// The other half of the same rule: a project copy this repository may not
	// use is not a value it has -- dropping the candidate check would make
	// init skip a name that would then have nothing to mount.
	t.Run("a project copy this repository may not use", func(t *testing.T) {
		r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
		r.e.lc.ProjectSecrets = map[string]config.ProjectSecret{
			"bitbucket-token": {AllowDataTo: []string{"acme/other"}},
		}
		r.stage.resolve(ctx)
		r.store.seedProject(r.e.lc.Name, "bitbucket-token", tokenValue)
		if _, ok := infra.ProjectSecretCandidate(r.e.lc, "bitbucket-token", "acme/app"); ok {
			t.Fatal("a project copy acme/app may not use was treated as a candidate")
		}
		got, err := r.stage.find(ctx, r.store)
		if err != nil || !slices.Contains(got, "bitbucket-token") {
			t.Fatalf("find = %v, %v; want the git credential still missing", got, err)
		}
	})
}

// TestInitRepoDoesNotAskForAProjectCoveredSecret: D2 extends #343's 40i to
// the swept-in reserved names (plan Task 2.1 step 4). A git credential the
// project already holds is not missing here even though the local config has
// no project_secrets entry and no allow-list at all: eligibility for a
// reserved name no longer needs one, so the stage must not ask for a value
// that would then be stored as a repository copy and win over the project's
// forever.
//
// The GitHub App key is the git credential this holds for now (githubOrigin):
// bitbucket-token is no longer swept in at all (round 2, item 2), and
// TestInitRepoAsksForTheBitbucketToken pins the other side.
func TestInitRepoDoesNotAskForAProjectCoveredSecret(t *testing.T) {
	ctx := context.Background()
	r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
	if r.e.lc.ProjectSecrets != nil {
		t.Fatalf("the rig's local config has project_secrets; this test proves nothing: %v", r.e.lc.ProjectSecrets)
	}
	r.stage.resolve(ctx)
	if _, ok := infra.ProjectSecretCandidate(r.e.lc, "github-app-key", "acme/app"); !ok {
		t.Fatal("the swept-in name is not a project candidate here: the test would prove nothing")
	}
	r.store.seedProject(r.e.lc.Name, "github-app-key", tokenValue)
	got, err := r.stage.find(ctx, r.store)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got, "github-app-key") {
		t.Errorf("find = %v, want the project-covered git credential not missing", got)
	}
	if r.store.setCalls() != 0 {
		t.Fatal("a value was stored")
	}
}

// TestInitRepoAsksForTheBitbucketToken: bitbucket-token has no project-wide
// default (config.PerRepositorySecret, the owner's ruling of 2026-10-15), so
// the initialisation of a Bitbucket repository asks for it even when a
// project-wide copy of that name exists: the name is not a project candidate
// for this repository, nothing may mount the project copy, and reading it as
// covered would leave the repository's runs authenticating with another
// repository's token. The stage's scope decision is the same
// infra.ProjectSecretCandidate the store uses, so where it asks is where the
// value goes -- the repository's own copy.
func TestInitRepoAsksForTheBitbucketToken(t *testing.T) {
	ctx := context.Background()
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	if r.e.lc.ProjectSecrets != nil {
		t.Fatalf("the rig's local config has project_secrets; this test proves nothing: %v", r.e.lc.ProjectSecrets)
	}
	r.stage.resolve(ctx)
	if _, ok := infra.ProjectSecretCandidate(r.e.lc, "bitbucket-token", "acme/app"); ok {
		t.Fatal("bitbucket-token was treated as project-wide eligible")
	}
	if r.stage.projectScope("bitbucket-token") {
		t.Fatal("init would store the per-repository credential project-wide")
	}
	// A project-wide copy of the name exists -- as one built before this
	// ruling leaves behind -- and does not cover this repository.
	r.store.seedProject(r.e.lc.Name, "bitbucket-token", tokenValue)
	got, err := r.stage.find(ctx, r.store)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "bitbucket-token") {
		t.Errorf("find = %v, want the git credential still missing: a project copy of it is not this repository's", got)
	}
}

// End to end at a terminal: the stage never reaches the prompt for a name the
// project copy already covers. It is not merely told the names exist -- the
// stage is Done, so nothing it would print or store is anywhere, and no
// command is left for the user either (before the fix this run ended
// NeedsYou with "fugaro secrets set bitbucket-token --repo acme/app").
func TestInitSecretsDoesNotPromptForProjectCoveredName(t *testing.T) {
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	projectSharedForApp(r, "acme/app")
	fakeTerminal(t) // canPrompt() is true: a name wrongly called missing would be prompted for
	covered := []string{"bitbucket-token", "claude-oauth-token"}
	for _, name := range covered {
		r.store.seedProject(r.e.lc.Name, name, tokenValue)
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := stateOf(res, initflow.Secrets); got != initflow.Done {
		t.Fatalf("state = %q, want done (out %q, errOut %q)", got, r.out.String(), r.errOut.String())
	}
	if n := r.store.setCalls(); n != 0 {
		t.Fatalf("%d value(s) stored: a project-covered name was prompted for", n)
	}
	text := r.out.String() + r.errOut.String()
	for _, want := range []string{"secrets set", "Paste the value for"} {
		if strings.Contains(text, want) {
			t.Fatalf("%q reached the output: a project-covered name was asked for", want)
		}
	}
	for _, l := range res.Left {
		if strings.Contains(strings.Join(l.Commands, "\n"), "secrets set") {
			t.Fatalf("a project-covered name was left as a command: %v", l.Commands)
		}
	}
	noLeak(t, tokenValue, text)
}

// The regression guard: a repository whose own copy has a version is not
// missing, with or without a project copy as well (today's rule, unchanged by
// the project-copy read above -- the fix must not make the project copy look
// like the preferred one).
func TestInitSecretsPrecedenceUnchangedWhenRepoAlreadyStored(t *testing.T) {
	ctx := context.Background()
	slug := mustSlug("bitbucket", "acme/app")
	for _, tc := range []struct {
		name    string
		project bool
	}{
		{"the repository's own copy only", false},
		{"the repository's copy and a project copy", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
			projectSharedForApp(r, "acme/app")
			r.stage.resolve(ctx)
			for _, name := range []string{"bitbucket-token", "claude-oauth-token"} {
				r.store.seed(slug, name, tokenValue)
				if tc.project {
					r.store.seedProject(r.e.lc.Name, name, tokenValue)
				}
			}
			if got, err := r.stage.find(ctx, r.store); err != nil || len(got) != 0 {
				t.Fatalf("find = %v, %v; want nothing missing", got, err)
			}
		})
	}
}

// §3a: a provider configured with no allow_data_to may be used by every
// repository, so a project-wide copy of its key covers each repository's init
// -- not only one some list happens to name.
func TestInitSecretsProviderNoListNeverPromptsAnyRepo(t *testing.T) {
	for _, repo := range []string{"acme/app", "acme/other"} {
		t.Run(repo, func(t *testing.T) {
			ctx := context.Background()
			r := newSecretsRig(t, "https://github.com/"+repo+".git", strings.NewReader(""))
			r.e.lc.Repos = map[string]localcfg.Repo{repo: {Provider: "github"}}
			r.e.lc.Providers = map[string]config.ModelProvider{
				"openrouter": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
					Secret: "openrouter-api-key", Models: []string{"deepseek/*"}}, // no allow_data_to at all
			}
			r.writeConfig(t, checkoutYAML("github", "api-key", "aurora", ""))
			slug := mustSlug("github", repo)
			r.store.seed(slug, "github-app-key", tokenValue)
			r.store.seed(slug, "anthropic-api-key", tokenValue)
			r.store.seedProject(r.e.lc.Name, "openrouter-api-key", tokenValue)
			if _, ok := infra.ProjectSecretCandidate(r.e.lc, "openrouter-api-key", repo); !ok {
				t.Fatalf("the provider's key is not a project candidate for %s: the test would prove nothing", repo)
			}
			r.stage.resolve(ctx)
			if got, err := r.stage.find(ctx, r.store); err != nil || len(got) != 0 {
				t.Fatalf("find = %v, %v; want nothing missing for %s", got, err, repo)
			}
			res, err := r.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if got := stateOf(res, initflow.Secrets); got != initflow.Done {
				t.Fatalf("state = %q, want done (out %q)", got, r.out.String())
			}
			for _, l := range res.Left {
				if strings.Contains(strings.Join(l.Commands, "\n"), "secrets set") {
					t.Fatalf("a project-covered provider key was left as a command: %v", l.Commands)
				}
			}
		})
	}
}

// The legitimate bootstrap case: neither the repository nor the project has a
// version. find()'s answer and canPrompt()/set()'s behavior are byte-for-byte
// what they were -- the name is still missing, and at a terminal the stage
// still asks for it and leaves the one-line command.
func TestInitSecretsStillPromptsWhenNothingStoredAnywhere(t *testing.T) {
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	projectSharedForApp(r, "acme/app")
	fakeTerminal(t)
	ctx := context.Background()
	r.stage.resolve(ctx)
	want := []string{"bitbucket-token", "claude-oauth-token"}
	if got, err := r.stage.find(ctx, r.store); err != nil || !slices.Equal(got, want) {
		t.Fatalf("find = %v, %v; want %v", got, err, want)
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := stateOf(res, initflow.Secrets); got != initflow.NeedsYou {
		t.Fatalf("state = %q, want needs-you (the values are the user's to type)", got)
	}
	// The fix line carries the scope the default stores to (D2): both names
	// are allow-listed in project_secrets here, so neither line names --repo.
	// Every command is compared whole: `strings.Contains` on the joined lines
	// passes for a line that merely begins with the wanted text, and the
	// exact form is the thing under test — the command a user pastes.
	var got []string
	for _, l := range res.Left {
		got = append(got, l.Commands...)
	}
	if wantCmds := []string{
		"fugaro secrets set bitbucket-token",
		"claude setup-token",
		"fugaro secrets set claude-oauth-token",
	}; fmt.Sprint(got) != fmt.Sprint(wantCmds) {
		t.Errorf("commands %q, want %q (no --repo: neither name has repository scope here)", got, wantCmds)
	}
	if r.store.setCalls() != 0 {
		t.Fatal("a value was stored with none to store from")
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
			// The whole command (code review, low): `secrets set
			// bitbucket-token` alone is a prefix of both scopes, and this
			// name's scope is the repository's own copy.
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

// TestInitSecretsAnthropicKeyOnlyWhenAPinGoesDirect (design
// docs/design/project-centric.md §6.1): with the gateway enforcing and every
// pin served by a provider the repository may send code to, the run never
// calls Anthropic, so init asks for no Anthropic key; a pin that goes direct
// keeps the prompt.
func TestInitSecretsAnthropicKeyOnlyWhenAPinGoesDirect(t *testing.T) {
	const providerYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: api-key, model: deepseek/deepseek-v4-flash }
budget: { mode: enforce, per_run_usd: 5 }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	const claudeYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: api-key, model: claude-sonnet-5-5 }
budget: { mode: enforce, per_run_usd: 5 }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	// No budget: block at all, so the project's own mode is the effective one.
	const ceilingYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: api-key, model: deepseek/deepseek-v4-flash }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	for _, tc := range []struct {
		name, yaml string
		// enforce is the local config's own budget mode, for the file that
		// leaves the mode to the project's ceiling.
		enforce bool
		want    bool
	}{
		{"every pin served by a provider", providerYAML, false, false},
		{"a Claude pin goes direct", claudeYAML, false, true},
		{"every pin served by a provider, the project's ceiling enforcing", ceilingYAML, true, false},
		{"the same file under a project whose ceiling is off", ceilingYAML, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSecretsRig(t, githubOrigin, strings.NewReader(""))
			r.e.lc.Providers = map[string]config.ModelProvider{
				"openrouter": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
					Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
			}
			if tc.enforce {
				r.e.lc.Budget = &localcfg.Budget{Mode: localcfg.BudgetEnforce}
			}
			r.writeConfig(t, tc.yaml)
			res, err := r.run(t)
			if err != nil || len(res.Left) != 1 {
				t.Fatalf("%v, left %v", err, res.Left)
			}
			cmds := strings.Join(res.Left[0].Commands, "\n")
			if got := strings.Contains(cmds, "fugaro secrets set anthropic-api-key"); got != tc.want {
				t.Errorf("anthropic prompt = %v, want %v: %q", got, tc.want, cmds)
			}
			if !strings.Contains(cmds, "github-app-key") {
				t.Errorf("the git credential is not asked for: %q", cmds)
			}
		})
	}
}
