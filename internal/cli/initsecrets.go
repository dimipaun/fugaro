package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/task"
)

// The secrets stage (stage 7, design §3.5): the credentials only the person
// at the keyboard may hold, for the checkout init runs in. Its rules:
//
//   - values come only from a hidden prompt on a real terminal. A stdin that
//     is not a terminal is never read, not even a pipe (an agent could be
//     holding the other end); the stage then leaves the user the one-line
//     fugaro secrets set commands, which keep their own pipe and file forms.
//   - --yes and --non-interactive never reach it: with either, it prompts
//     for nothing and reads nothing.
//   - a secret that already has a version is skipped, and is never read,
//     overwritten or rotated here (that is fugaro secrets set).
//   - a value lives in memory only from the prompt to the store call, is
//     registered with the loop's redaction while it does, and is never in an
//     output, the result JSON, an error, a log or an argument or variable of
//     a subprocess (the stage starts none).
//
// It runs before the repository stage, so the repository's containers do
// not exist yet: the store call creates the secret with the same labels
// init --repo's Terraform adopts (the roots import an existing secret), so
// the first image build finds its secrets stored.

// maxHeldSecrets is the redaction slots the engine keeps for values a stage
// is holding (the secrets stage holds one at a time).
const maxHeldSecrets = 4

// secretStore is what the stage needs of Secret Manager: metadata and
// storing a value; it has no read of a value.
type secretStore interface {
	List(ctx context.Context, labels map[string]string) ([]gcp.SecretInfo, error)
	Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error)
}

// selfCommand is how the one-line commands name the binary: the path the
// user ran, quoted for one shell word. Tests replace it.
var selfCommand = func() string {
	arg := os.Args[0]
	switch {
	case arg == "" || strings.ContainsAny(arg, "\r\n\x00"):
		return "fugaro"
	case regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`).MatchString(arg):
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// hold registers value with the loop's redaction until release.
func (e *initEngine) hold(value []byte) (release func()) {
	for i := range e.held {
		if e.held[i] == "" {
			e.held[i] = string(value)
			return func() { e.held[i] = "" }
		}
	}
	return func() {}
}

// secretsStage is stage 7.
type secretsStage struct {
	e *initEngine
	// open connects to Secret Manager; tests replace it.
	open func(ctx context.Context) (secretStore, error)
	// out is where prompts go (stderr, as fugaro secrets set does).
	out func() io.Writer

	tg      *secretTarget
	skip    string
	checked bool
	missing []secretNeed
	stored  []string // names stored in this run, for Verify
}

func newSecretsStage(e *initEngine) *secretsStage {
	return &secretsStage{e: e,
		open: func(ctx context.Context) (secretStore, error) {
			sm, err := gcp.NewSecrets(ctx, gcpOptions(e.lc))
			if err != nil {
				return nil, remote(err)
			}
			return sm, nil
		},
		out: func() io.Writer { return e.r.cmd.ErrOrStderr() },
	}
}

// secretTarget is the repository whose secrets the stage takes.
type secretTarget struct {
	repo, slug, label, provider string
}

// secretNeed is one thing still missing: a named secret, or the choice of
// Claude credential when neither exists.
type secretNeed struct {
	name   string // "" for the Claude credential
	claude bool
}

func (n secretNeed) names() []string {
	if n.claude {
		return []string{"claude-oauth-token", "anthropic-api-key"}
	}
	return []string{n.name}
}

func (s *secretsStage) Name() string { return initflow.Secrets }

// canPrompt is whether this run may show a hidden prompt: a terminal, and
// neither --non-interactive nor --yes (which never supplies a secret, and
// is the flag of an unattended run).
func (s *secretsStage) canPrompt() bool {
	o := s.e.r.o
	return !o.nonInteractive && !o.yes && stdinIsTerminal(s.e.r.cmd.InOrStdin())
}

// resolve finds the repository and its provider: the local config's entry,
// else the checkout's fugaro.yaml, else the origin's host (github.com or
// bitbucket.org). skip says why the stage does not apply.
func (s *secretsStage) resolve(ctx context.Context) {
	if s.tg != nil || s.skip != "" {
		return
	}
	repo, err := originRepo(ctx)
	if err != nil {
		s.skip = "not in a checkout of a repository: run fugaro init there to take its secrets"
		return
	}
	provider := ""
	if want, err := task.CanonicalRepo(repo); err == nil {
		for name, r := range s.e.lc.Repos {
			if c, err := task.CanonicalRepo(name); err == nil && c == want {
				provider = r.Provider
			}
		}
	}
	if provider == "" {
		if cfg := checkoutConfig(ctx, repo); cfg != nil {
			provider = cfg.Git.Provider
		}
	}
	if provider == "" {
		provider = originProvider(ctx)
	}
	if provider != "github" && provider != "bitbucket" {
		s.skip = "this checkout's git provider is not github or bitbucket"
		return
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		s.skip = "this checkout's repository has no usable name"
		return
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		s.skip = "this checkout's repository has no usable name"
		return
	}
	s.tg = &secretTarget{repo: repo, slug: slug, label: label, provider: provider}
}

// originProvider is the provider the origin's host names, or "".
func originProvider(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	u, err := url.Parse(image.HTTPSOrigin(strings.TrimSpace(string(out))))
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com":
		return "github"
	case "bitbucket.org":
		return "bitbucket"
	}
	return ""
}

func (t *secretTarget) providerSecret() string {
	if t.provider == "bitbucket" {
		return "bitbucket-token"
	}
	return "github-app-key"
}

// find lists the repository's secrets (metadata only) and returns what is
// still missing.
func (s *secretsStage) find(ctx context.Context, st secretStore) ([]secretNeed, error) {
	list, err := st.List(ctx, map[string]string{gcp.LabelRepo: s.tg.label})
	if err != nil {
		return nil, err
	}
	has := func(name string) bool {
		id := gcp.SecretID(s.tg.slug, name)
		return slices.ContainsFunc(list, func(i gcp.SecretInfo) bool { return i.ID == id && i.Latest != "" })
	}
	var out []secretNeed
	if p := s.tg.providerSecret(); !has(p) {
		out = append(out, secretNeed{name: p})
	}
	if !has("claude-oauth-token") && !has("anthropic-api-key") {
		out = append(out, secretNeed{claude: true})
	}
	return out, nil
}

func describeNeeds(needs []secretNeed) string {
	var parts []string
	for _, n := range needs {
		if n.claude {
			parts = append(parts, "claude-oauth-token (or anthropic-api-key)")
		} else {
			parts = append(parts, n.name)
		}
	}
	return "no value stored for " + strings.Join(parts, ", ")
}

// Check lists which secrets have no version, by Secret Manager metadata. A
// listing that fails (before the installation exists, for one) is not an
// error here: Apply lists again and fails for real.
func (s *secretsStage) Check(ctx context.Context) (initflow.Status, error) {
	s.resolve(ctx)
	if s.skip != "" {
		return initflow.Status{State: initflow.Skipped, Detail: s.skip}, nil
	}
	st, err := s.open(ctx)
	if err == nil {
		s.missing, err = s.find(ctx, st)
	}
	if err != nil {
		s.checked = false
		return initflow.Status{State: initflow.Todo, Detail: "which secrets are stored is read when it runs"}, nil
	}
	s.checked = true
	if len(s.missing) == 0 {
		return initflow.Status{State: initflow.Done, Detail: "the provider and Claude credentials are stored"}, nil
	}
	detail := describeNeeds(s.missing)
	if !s.canPrompt() {
		lf := s.Left()
		return initflow.Status{State: initflow.NeedsYou, Detail: detail, Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: detail + "; asks for each at a hidden prompt"}, nil
}

func (s *secretsStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	st, err := s.Check(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State == initflow.Done}, nil
}

// Left is the one line the user runs in their own terminal: each missing
// secret's command (all the candidates when the listing did not work),
// joined on one line. None holds a value: the value is typed at the hidden
// prompt, or the PEM is redirected from a file the user names.
func (s *secretsStage) Left() initflow.Left {
	needs := s.missing
	if !s.checked && s.tg != nil {
		needs = []secretNeed{{name: s.tg.providerSecret()}, {claude: true}}
	}
	if s.tg == nil {
		return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: "run fugaro init in a checkout of the repository, in your own terminal"}
	}
	var cmds []string
	for _, n := range needs {
		set := selfCommand() + " secrets set "
		switch {
		case n.claude:
			cmds = append(cmds, "claude setup-token; "+set+"claude-oauth-token --repo "+s.tg.repo)
		case n.name == multilineSecret:
			cmds = append(cmds, set+n.name+" --repo "+s.tg.repo+" < PATH-TO-THE-KEY-FILE")
		default:
			cmds = append(cmds, set+n.name+" --repo "+s.tg.repo)
		}
	}
	return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: strings.Join(cmds, "; ")}
}

// terminal is stdin as a real terminal file, else nil. The stage reads
// nothing from any other stdin, whatever stdinIsTerminal says.
func (s *secretsStage) terminal() *os.File {
	if f, ok := s.e.r.cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return f
	}
	return nil
}

func (s *secretsStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.resolve(ctx)
	f := s.terminal()
	if s.skip != "" || s.tg == nil || !env.Interactive || !s.canPrompt() || f == nil {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	st, err := s.open(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return initflow.Outcome{}, remote(err)
	}
	s.missing, s.checked = needs, true
	if len(needs) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	w := s.out()
	for _, n := range needs {
		name := n.name
		if n.claude {
			if name, err = s.chooseClaude(ctx, f, w); err != nil {
				return initflow.Outcome{}, err
			}
		}
		if err := s.take(ctx, st, f, w, name); err != nil {
			return initflow.Outcome{}, err
		}
		s.stored = append(s.stored, name)
	}
	return initflow.Outcome{Changed: true, Detail: "stored " + strings.Join(s.stored, ", ")}, nil
}

// chooseClaude asks which Claude credential, at a hidden prompt too (a
// value pasted by mistake must not echo), default the OAuth token.
func (s *secretsStage) chooseClaude(ctx context.Context, f *os.File, w io.Writer) (string, error) {
	fmt.Fprintln(w, "Claude credential: 1 = claude-oauth-token (default; run claude setup-token in your own terminal first), 2 = anthropic-api-key")
	b, err := readHiddenLine(ctx, f, w, "Type 1 or 2, then press Enter (empty is 1): ")
	if err != nil {
		return "", err
	}
	defer clear(b)
	switch string(b) {
	case "", "1":
		return "claude-oauth-token", nil
	case "2":
		return "anthropic-api-key", nil
	}
	return "", userErr("not a choice: type 1 or 2")
}

// take prompts for one secret, validates it and stores it. The value is
// held only inside this call.
func (s *secretsStage) take(ctx context.Context, st secretStore, f *os.File, w io.Writer, name string) error {
	var (
		value []byte
		err   error
	)
	if name == multilineSecret {
		value, err = readHiddenPEM(ctx, f, w, name)
	} else {
		if value, err = readHidden(ctx, f, w, name); err == nil {
			err = validateSecret(value, false)
		}
	}
	if err != nil {
		clear(value)
		return err
	}
	defer clear(value)
	defer s.e.hold(value)()

	id := gcp.SecretID(s.tg.slug, name)
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: s.tg.label, gcp.LabelSecret: name}
	if _, err := st.Set(ctx, id, value, labels); err != nil {
		// A server may echo what it received, raw, base64 or JSON-escaped.
		esc, _ := json.Marshal(string(value))
		msg := agent.Redact(err.Error(), []string{string(value), base64.StdEncoding.EncodeToString(value), strings.Trim(string(esc), `"`)})
		msg = fmt.Sprintf("storing %s failed: %s", name, oneLine(msg))
		if errors.Is(err, gcp.ErrForeignSecret) {
			return userErr("%s; refusing to add a version to it", msg)
		}
		return remote(errors.New(msg))
	}
	fmt.Fprintf(w, "stored %s\n", name)
	return nil
}

// Verify lists again and checks each secret this run stored has a version.
func (s *secretsStage) Verify(ctx context.Context) error {
	if len(s.stored) == 0 {
		return nil
	}
	st, err := s.open(ctx)
	if err != nil {
		return err
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return remote(err)
	}
	if len(needs) > 0 {
		return remote(errors.New("stored " + strings.Join(s.stored, ", ") + ", but " + describeNeeds(needs)))
	}
	return nil
}

// readHiddenPEM reads a multi-line value (a PEM) pasted at the terminal
// without echo: the terminal goes raw once for the whole paste, because a
// line-by-line read would turn echo back on between lines and show what has
// already arrived. It ends at the "-----END" line. Ctrl-C cancels (with
// nothing stored) and Ctrl-D ends the input early; both restore the
// terminal. The result has "\n" line ends and one trailing newline, and is
// validated like any value; the paste's first line must be a -----BEGIN
// line. Errors never quote the value. As readHidden, a cancelled read
// leaves its goroutine blocked, for a process that exits soon after.
func readHiddenPEM(ctx context.Context, f *os.File, prompt io.Writer, name string) ([]byte, error) {
	fd := int(f.Fd())
	fmt.Fprintf(prompt, "Paste the private key for %s (input hidden), from its -----BEGIN line to its -----END line: ", name)
	state, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(prompt)
		return nil, userErr("reading the terminal's state: %v", err)
	}
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var all, line []byte
		buf := make([]byte, 4096)
		fail := func(err error) {
			clear(all)
			clear(line)
			clear(buf)
			done <- result{nil, err}
		}
		for {
			n, rerr := f.Read(buf)
			for _, c := range buf[:n] {
				switch {
				case c == 3:
					fail(errCancelled)
					return
				case c == 4 && len(line) == 0:
					fail(userErr("the input ended before the -----END line"))
					return
				case c == '\r' || c == '\n':
					if len(line) == 0 {
						continue
					}
					if len(all) == 0 && !bytes.HasPrefix(line, []byte("-----BEGIN ")) {
						fail(userErr("the paste does not start with a -----BEGIN line"))
						return
					}
					all = append(append(all, line...), '\n')
					end := bytes.HasPrefix(line, []byte("-----END "))
					clear(line)
					line = line[:0]
					if end {
						clear(buf)
						done <- result{all, nil}
						return
					}
				case c == 0x7f || c == 8:
					if len(line) > 0 {
						line[len(line)-1] = 0
						line = line[:len(line)-1]
					}
				case c >= 0x20 || c == '\t':
					line = append(line, c)
				}
				if len(all)+len(line) > maxSecretBytes+3 {
					fail(userErr("the value is over Secret Manager's 64 KiB limit"))
					return
				}
			}
			clear(buf[:n])
			if rerr != nil {
				fail(userErr("reading the value from the terminal: %v", rerr))
				return
			}
		}
	}()
	finish := func() { _ = term.Restore(fd, state); fmt.Fprintln(prompt) }
	select {
	case r := <-done:
		finish()
		if ctx.Err() != nil {
			clear(r.b)
			return nil, errCancelled
		}
		if r.err == nil {
			if r.err = validateSecret(r.b, true); r.err != nil {
				clear(r.b)
			}
		}
		if r.err != nil {
			return nil, r.err
		}
		return r.b, nil
	case <-ctx.Done():
		finish()
		return nil, errCancelled
	}
}
