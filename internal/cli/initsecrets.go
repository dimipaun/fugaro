package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// The secrets stage (stage 7, design §3.5): the credentials only the person
// at the keyboard may hold, for the checkout init runs in. Its rules:
//
//   - values come only from a hidden prompt on a real terminal. A stdin that
//     is not a terminal is never read, not even a pipe (an agent could be
//     holding the other end); the stage then leaves the user the one-line
//     fugaro secrets set commands, which keep their own pipe and file forms.
//   - --yes, --non-interactive and --json never reach it: with any of them,
//     it prompts for nothing and reads nothing.
//   - nothing is prompted when a coding agent's environment is present
//     (agentMarkers), or when stdin, stdout or stderr is not a terminal (a
//     redirected stderr would send the prompt to a file and make the user
//     type blind). This is a mitigation, not a barrier: an agent that already
//     holds a secret's bytes, or drives a pty, can get around any gate here.
//     The real controls are that the skills never touch a value (their
//     forbidden-instruction lint) and that the user types it.
//   - the secrets asked for are the ones the repository's jobs mount
//     (infra.SecretMounts, the function that mounts them), read from the
//     checkout's fugaro.yaml: the git credential, the Claude credential the
//     agent.auth names (none for vertex), the allowed providers' keys and the
//     workflows' own.
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
// is holding (the secrets stage holds one at a time: the value and each line of a
// multi-line one).
const maxHeldSecrets = 128

// maxPEMLines is the most lines a pasted PEM may have: each long enough line
// takes a redaction slot, and the whole value takes one (a 4096-bit RSA key is
// about 52 lines).
const maxPEMLines = 120

// secretStore is what the stage needs of Secret Manager: metadata and
// storing a value; it has no read of a value.
type secretStore interface {
	List(ctx context.Context, labels map[string]string) ([]gcp.SecretInfo, error)
	Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error)
}

// selfCommand is how every command init prints names the binary: the one
// rule is the program as the user ran it (os.Args[0], so a bare "fugaro" from
// the PATH stays "fugaro", and a path stays the path), as one shell word
// through quoteWord; plain "fugaro" when that cannot be one line. Tests
// replace it.
var selfCommand = func() string {
	arg := os.Args[0]
	if arg == "" || strings.ContainsAny(arg, "\r\n\x00") {
		return "fugaro"
	}
	return quoteWord(arg)
}

// hold registers value with the loop's redaction until release: the whole
// value and each of its lines (a PEM's body lines are as secret as the whole).
// It fails closed: when a slot is missing for any of them it registers
// nothing and returns an error, so a value is never held in part. The error
// does not carry the value.
func (e *initEngine) hold(value []byte) (release func(), err error) {
	vals := []string{string(value)}
	if bytes.Contains(value, []byte("\n")) {
		for _, l := range bytes.Split(value, []byte("\n")) {
			if len(l) >= minSecretBytes {
				vals = append(vals, string(l))
			}
		}
	}
	var free []int
	for i := range e.held {
		if e.held[i] == "" {
			free = append(free, i)
		}
	}
	if len(free) < len(vals) {
		return nil, userErr("the value has more lines than can be kept out of the output (at most %d); nothing was stored", maxPEMLines)
	}
	taken := free[:len(vals)]
	for n, v := range vals {
		e.held[taken[n]] = v
	}
	return func() {
		for _, i := range taken {
			e.held[i] = ""
		}
	}, nil
}

// agentMarkers are the environment variables a coding agent's session sets.
// With any of them present the stage never prompts: the person typing must be
// at their own terminal, not behind an agent. CLAUDE_CODE_SSE_PORT is also set
// by the Claude Code IDE extension in the integrated terminals of VS Code and
// JetBrains, so a person's own IDE terminal can be refused: the refusal says
// how to get past it.
var agentMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}

// agentMarker is the first marker getenv shows set, or "" with none: a coding
// agent's session.
func agentMarker(getenv func(string) string) string {
	for _, k := range agentMarkers {
		if getenv(k) != "" {
			return k
		}
	}
	return ""
}

// agentEnv reports whether getenv shows a coding agent's session.
func agentEnv(getenv func(string) string) bool { return agentMarker(getenv) != "" }

// writerIsTerminal reports whether w is a terminal. Tests replace it.
var writerIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// secretsStage is stage 7.
type secretsStage struct {
	e *initEngine
	// open connects to Secret Manager; tests replace it.
	open func(ctx context.Context) (secretStore, error)
	// out is where prompts go (stderr, as fugaro secrets set does).
	out func() io.Writer

	tg      *secretTarget
	origin  originInfo // the checkout's origin, for the hostile-checkout gate
	root    string     // the checkout's top
	skip    string
	checked bool
	missing []string // names with no stored value
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
	wanted                      []string // every secret its jobs mount, in order
	noConfig                    bool     // no fugaro.yaml here: only the git credential is known
}

func (s *secretsStage) Name() string { return initflow.Secrets }

// canPrompt is whether this run may show a hidden prompt: stdin, stdout and
// stderr are terminals, no coding agent's environment is present, and
// neither --non-interactive nor --yes (which never supplies a secret, and is
// the flag of an unattended run) nor --json (the output of a caller that reads
// it: a hard rule, --json never prompts) is set.
func (s *secretsStage) canPrompt() bool {
	o, cmd := s.e.r.o, s.e.r.cmd
	return !o.nonInteractive && !o.yes && !o.asJSON && !agentEnv(os.Getenv) &&
		stdinIsTerminal(cmd.InOrStdin()) && writerIsTerminal(cmd.ErrOrStderr()) && writerIsTerminal(cmd.OutOrStdout())
}

// resolve finds the repository the stage works on and what its jobs mount.
// It applies only to a checkout of a repository of this project, found as
// fugaro secrets set finds one: the provider comes from the local config's
// entry and the checkout's fugaro.yaml (never from the origin's host) and
// must agree between them; a fugaro.yaml naming another project, or a
// checkout the project does not know, is skipped, so a container is never
// created for an unrelated checkout. skip says why.
func (s *secretsStage) resolve(ctx context.Context) {
	if s.tg != nil || s.skip != "" {
		return
	}
	lc := s.e.lc
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	oi, ok := readOrigin(ctx, root)
	if err != nil || !ok {
		s.skip = "not in a checkout of a repository: run fugaro init there to take its secrets"
		return
	}
	repo := oi.Repo
	var local localcfg.Repo
	known := false
	if want, err := task.CanonicalRepo(repo); err == nil {
		for name, r := range lc.Repos {
			if c, err := task.CanonicalRepo(name); err == nil && c == want {
				local, known = r, true
			}
		}
	}
	// The default branch's file, as the repository stage reads it: not the
	// working tree's, which whoever made the checkout could have changed.
	cfg := defaultBranchConfig(ctx, root)
	if cfg != nil && cfg.Project != lc.Name {
		s.skip = "this checkout's fugaro.yaml names another project (or none): its secrets are not taken here"
		return
	}
	if !known && cfg == nil {
		s.skip = "this checkout is not a repository of this project yet: onboard it first"
		return
	}
	provider := local.Provider
	if cfg != nil {
		switch {
		case provider != "" && cfg.Git.Provider != "" && provider != cfg.Git.Provider:
			s.skip = "the local config and this checkout's fugaro.yaml disagree on the git provider; make them agree"
			return
		case provider == "":
			provider = cfg.Git.Provider
		}
	}
	if _, ok := infra.GitSecret(provider); !ok {
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
	tg := &secretTarget{repo: repo, slug: slug, label: label, provider: provider}
	gitSecret, _ := infra.GitSecret(provider) // the provider is github or bitbucket here
	tg.wanted = []string{gitSecret}
	if cfg == nil {
		tg.noConfig = true
	} else {
		for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
			mounts, err := infra.SecretMounts(gitSecret, cfg, name, cfg.Workflows[name], lc, repo)
			if err != nil {
				s.skip = "fugaro.yaml's secrets cannot be worked out: " + oneLine(err.Error())
				return
			}
			for _, m := range mounts {
				if !slices.Contains(tg.wanted, m.Logical) {
					tg.wanted = append(tg.wanted, m.Logical)
				}
			}
		}
	}
	s.tg, s.origin, s.root = tg, oi, root
}

// defaultBranchConfig is the fugaro.yaml of the checkout's default branch
// (origin/HEAD, main or master, as last fetched), read from git; nil with none
// or one that does not parse.
func defaultBranchConfig(ctx context.Context, root string) *config.Config {
	ref := defaultBranchRef(ctx, root)
	if ref == "" {
		return nil
	}
	data, err := gitCmd(ctx, root, "cat-file", "blob", "refs/remotes/"+ref+":fugaro.yaml").Output()
	if err != nil {
		return nil
	}
	cfg, _ := config.Parse(data)
	return cfg
}

// authorize is the hostile-checkout gate (init_repo_gate.go) in front of the
// stage's cloud calls and prompts: a repository the local config does not
// list takes a value for nobody until it is opted in, by --onboard-repo or by
// typing its owner/name at the user's own terminal. A checkout whose default
// branch names this project is no proof of anything.
func (s *secretsStage) authorize(env initflow.Env) error {
	switch a, err := s.e.authState(s.origin); {
	case err != nil:
		return err
	case a == authNeeded || (a == authAsk && !env.Interactive):
		return &initflow.NeedsYouError{Left: onboardLeft(s.origin)}
	case a == authAsk:
		ok, err := s.e.confirmRepo(s.root, s.origin)
		if err != nil {
			return err
		}
		if !ok {
			return &initflow.NeedsYouError{Left: onboardLeft(s.origin)}
		}
	}
	return nil
}

// find lists the repository's secrets (metadata only) and returns what its
// jobs mount that has no value yet.
func (s *secretsStage) find(ctx context.Context, st secretStore) ([]string, error) {
	list, err := st.List(ctx, map[string]string{gcp.LabelRepo: s.tg.label})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range s.tg.wanted {
		id := gcp.SecretID(s.tg.slug, name)
		if !slices.ContainsFunc(list, func(i gcp.SecretInfo) bool { return i.ID == id && i.Latest != "" }) {
			out = append(out, name)
		}
	}
	return out, nil
}

func describeNeeds(names []string) string {
	return "no value stored for " + strings.Join(names, ", ")
}

// Check lists which secrets have no version, by Secret Manager metadata. A
// listing that fails (before the installation exists, for one) is not an
// error here: Apply lists again and says what it needs.
func (s *secretsStage) Check(ctx context.Context) (initflow.Status, error) {
	s.resolve(ctx)
	if s.skip != "" {
		return initflow.Status{State: initflow.Skipped, Detail: s.skip}, nil
	}
	// Before any cloud call: a repository the project does not list yet is
	// not asked about.
	switch a, err := s.e.authState(s.origin); {
	case err != nil:
		return initflow.Status{}, err
	case a == authNeeded:
		lf := onboardLeft(s.origin)
		return initflow.Status{State: initflow.NeedsYou, Detail: unknownDetail(s.e.lc.Name, s.origin), Left: &lf}, nil
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
	note := ""
	if s.tg.noConfig {
		note = " (no fugaro.yaml on the default branch as last fetched, so the Claude credential is not assumed; git fetch, then rerun)"
	}
	if len(s.missing) == 0 {
		return initflow.Status{State: initflow.Done, Detail: "the secrets the jobs mount are stored" + note}, nil
	}
	detail := describeNeeds(s.missing) + note
	if !s.canPrompt() {
		if m := agentMarker(os.Getenv); m != "" {
			detail += "; a coding agent's session is present (" + m + " is set), so " + initflow.NeedsTerminal("typing the values") + "; if this is your own IDE terminal, unset " + m + " or run " + selfCommand() + " secrets set"
		} else {
			detail += "; " + initflow.NeedsTerminal("typing the values")
		}
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
// each on a line of its own. None holds a value: the value is typed at the hidden
// prompt, or the PEM is redirected from a file the user names.
func (s *secretsStage) Left() initflow.Left {
	if s.tg == nil {
		return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: "in a checkout of the repository, in your own terminal window, run", Commands: []string{selfCommand() + " init"}}
	}
	needs := s.missing
	if !s.checked {
		needs = s.tg.wanted
	}
	var cmds []string
	for _, name := range needs {
		set := selfCommand() + " secrets set " + quoteWord(name) + " --repo " + quoteWord(s.tg.repo)
		switch name {
		case "claude-oauth-token":
			cmds = append(cmds, "claude setup-token", set)
		case multilineSecret:
			cmds = append(cmds, set+" < PATH-TO-THE-KEY-FILE")
		default:
			cmds = append(cmds, set)
		}
	}
	text := "run these in your own terminal window, one line at a time, in this order"
	if agentEnv(os.Getenv) {
		text = "run these in your own terminal window, not through a coding agent, one line at a time, in this order"
	}
	return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: text, Commands: cmds}
}

// terminal is stdin as a real terminal file, else nil. The stage reads
// nothing from any other stdin, whatever stdinIsTerminal says.
func (s *secretsStage) terminal() *os.File {
	if f, ok := s.e.r.cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return f
	}
	return nil
}

// smProblem turns a refusal by Secret Manager (permission denied, the API
// not enabled) into what the user does about it: not a failure of the run,
// and never carrying the server's text (it could echo a payload).
func (s *secretsStage) smProblem(err error) *initflow.NeedsYouError {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != 403 {
		return nil
	}
	text := "Secret Manager refused: you need roles/secretmanager.viewer to list, secretmanager.secrets.create to create a secret's container (roles/secretmanager.admin or a custom role) and roles/secretmanager.secretVersionAdder to store a value, on the project's secrets (ask the project owner), then rerun " + selfCommand() + " init"
	if m := strings.ToLower(ae.Message); strings.Contains(m, "has not been used") || strings.Contains(m, "is disabled") || strings.Contains(m, "service_disabled") {
		text = "the Secret Manager API (secretmanager.googleapis.com) is not enabled on the project: enable it (the owner's fugaro init does), then rerun " + selfCommand() + " init"
	}
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftConsole, Text: text}}
}

func (s *secretsStage) apiErr(err error) error {
	if ny := s.smProblem(err); ny != nil {
		return ny
	}
	return remote(err)
}

func (s *secretsStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.resolve(ctx)
	if err := s.e.adoptGuard(initflow.Secrets); err != nil {
		return initflow.Outcome{}, err
	}
	f := s.terminal()
	if s.skip != "" || s.tg == nil || !env.Interactive || !s.canPrompt() || f == nil {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if err := s.authorize(env); err != nil {
		return initflow.Outcome{}, err
	}
	st, err := s.open(ctx)
	if err != nil {
		return initflow.Outcome{}, s.apiErr(err)
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return initflow.Outcome{}, s.apiErr(err)
	}
	s.missing, s.checked = needs, true
	if len(needs) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	w := s.out()
	for _, name := range needs {
		if err := s.take(ctx, st, f, w, name); err != nil {
			return initflow.Outcome{}, err
		}
		s.stored = append(s.stored, name)
	}
	return initflow.Outcome{Changed: true, Detail: "stored " + strings.Join(s.stored, ", ")}, nil
}

// take prompts for one secret, validates it and stores it. The value is
// held only inside this call, with core dumps off while it is.
func (s *secretsStage) take(ctx context.Context, st secretStore, f *os.File, w io.Writer, name string) error {
	defer noCoreDumps()()
	var (
		value []byte
		err   error
	)
	fmt.Fprintf(w, "%s for %s on %s (stored in project %s's Secret Manager):\n", name, pluginwire.Printable(s.origin.Repo), pluginwire.Printable(s.origin.Host), pluginwire.Printable(s.e.lc.GCPProject))
	if name == "claude-oauth-token" {
		fmt.Fprintln(w, "claude-oauth-token: run claude setup-token in your own terminal first, then paste the token it prints")
	}
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
	release, err := s.e.hold(value)
	if err != nil {
		return err
	}
	defer release()

	id := gcp.SecretID(s.tg.slug, name)
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: s.tg.label, gcp.LabelSecret: name}
	if _, err := st.Set(ctx, id, value, labels); err != nil {
		if ny := s.smProblem(err); ny != nil {
			return ny
		}
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
		return s.apiErr(err)
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return s.apiErr(err)
	}
	if len(needs) > 0 {
		return remote(errors.New("stored " + strings.Join(s.stored, ", ") + ", but " + describeNeeds(needs)))
	}
	return nil
}

// readHiddenPEM reads a multi-line value (a PEM) pasted at the terminal
// without echo: the terminal goes raw once for the whole paste, before the
// prompt is shown (a line-by-line read would turn echo back on between lines
// and show what has already arrived). It ends at the "-----END" line once
// Enter is pressed. Ctrl-C cancels (with nothing stored) and Ctrl-D ends the
// input with the error "the input ended before the -----END line"; both
// store nothing and restore the terminal, as does a panic. Escape sequences
// (arrow keys, the bracketed-paste markers a terminal may wrap a paste in,
// SS3 keys such as ESC O P) are dropped. More than maxPEMLines lines is
// refused. The result has "\n" line ends and one trailing newline, and
// is validated like any value; the paste's first line must be a -----BEGIN
// line. Errors never quote the value. As readHidden, a cancelled read leaves
// its goroutine blocked, for a process that exits soon after.
func readHiddenPEM(ctx context.Context, f *os.File, prompt io.Writer, name string) ([]byte, error) {
	fd := int(f.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, userErr("reading the terminal's state: %v", err)
	}
	defer func() { _ = term.Restore(fd, state) }()
	fmt.Fprintf(prompt, "Paste the private key for %s (input hidden), from its -----BEGIN line to its -----END line, then press Enter: ", name)
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
		defer func() {
			if r := recover(); r != nil {
				_ = term.Restore(fd, state)
				fail(userErr("reading the value from the terminal failed"))
			}
		}()
		esc := 0 // 1 after ESC, 2 inside a CSI sequence, 3 after ESC O (an SS3 key: one more byte)
		lines := 0
		for {
			n, rerr := f.Read(buf)
			for _, c := range buf[:n] {
				switch {
				case esc == 1:
					switch c {
					case '[':
						esc = 2
					case 'O':
						esc = 3
					default:
						esc = 0
					}
					continue
				case esc == 3:
					esc = 0
					continue
				case esc == 2:
					if c >= 0x40 && c <= 0x7e { // the sequence's final byte
						esc = 0
					}
					continue
				case c == 0x1b:
					esc = 1
					continue
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
					if lines++; lines > maxPEMLines {
						fail(userErr("the paste has more than %d lines: nothing was stored; use fugaro secrets set github-app-key < PATH-TO-THE-KEY-FILE", maxPEMLines))
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
