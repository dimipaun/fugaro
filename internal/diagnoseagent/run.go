package diagnoseagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/redact"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The read-only agent runner: the one child a diagnosis starts, and the whole
// of what it is allowed to be (D1602, F5). A command log holds text nobody
// here wrote — Terraform and gcloud error bodies, GitHub and API responses,
// repository and file names — and this child reads that text with the person's
// own credential in its environment. So the child is built to be unable to act
// on anything the log says: it has three read tools and no fourth, no setting
// source of the machine's own, no MCP server, a working directory and one
// folder beside it as the roots it is given, and a cap that ends it.
//
// Those two are the roots it is *given* and not the whole of what it can
// reach: the client does not confine its file tools to them (F16, U2), so what
// closes a path is the deny payload below, which names the places a diagnosis
// never has a reason to read. A path that is in neither the roots nor that
// list is a path this child can read, and the list is therefore the file's own
// account of the reads it stops.
//
// What replaces the run's local internal/gateway, which F16 asks this task to
// weigh: the gateway is the runner's answer to "the child must not hold the
// credential", and it is not available here. It is built from a job's price
// table, its budget lease, its routes and its upstream credential, and a
// diagnosis has none of those — it runs *after* a failure, often when the
// cloud is what is broken, which is the whole reason it is local (D1602, F16).
// What is here instead is narrower and worth stating plainly: one credential
// lives in the Handle and reaches one child's environment (T-DG5), its value
// is registered with the redactor before that child starts, the child cannot
// print anything without passing through that redactor, and D1613's cap is
// enforced twice — by the client's own --max-turns and --max-budget-usd, and by
// a check of what the run reports it cost. A local diagnosis on the person's
// own login is metered by their own provider, not by a Fugaro ledger, and a
// loop on that key is what the two caps exist to stop (F14).

// toolAllowList is the whole of what the child may do, as the client's --tools
// value (D1602). It is a restriction and not a pre-approval: --allowedTools
// would name what may run without asking while leaving every other tool
// available, and the point here is that Bash, Write, Edit, WebFetch and the
// rest are not there to be asked about at all. Nothing reads this list to
// check an answer, and nothing should: what makes it true is that the client
// is started with it.
const toolAllowList = "Read,Grep,Glob"

// childFileTools are the tool names the read rules of the settings payload are
// written for. Deny rules are matched by tool name, and the three tools this
// child has read files, so all three are named rather than the one whose name
// the rule syntax borrows.
var childFileTools = []string{"Read", "Grep", "Glob"}

// syncBuffer is the child's standard output, and the read that follows it is
// why it is a type of its own: the answer is parsed out of what the child
// wrote once procgroup.Run has returned, and Run can return while a copy
// goroutine is still writing here — procgroup abandons that goroutine when a
// child escaped the process group and held the pipe past its drain timeout.
// internal/mailbox/keychain's boundedBuffer and internal/watch's are this type
// for the same reason; reading a plain bytes.Buffer here would be a race on
// the diagnosis itself.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// stream is the child's output as a slice of its own: a write the abandoned
// copy goroutine makes after this one cannot reach it.
func (b *syncBuffer) stream() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.buf.Bytes())
}

// syncWriter serializes the writes to the child's standard error with the
// flush that follows them: the same abandoned copy goroutine that can still be
// writing there can still be inside the redactor, whose pending state the
// flush reads.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// do runs f with the lock held, for the flush that has to come after the last
// write rather than beside it.
func (s *syncWriter) do(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// childReadDeny are the places a diagnosis never has a reason to read.
//
// They are the line, and not a second one behind the working directory: the
// client does not confine its file tools to the working directory and the
// --add-dir folders unless that confinement is asked for, and this argv does
// not ask for it (U2). What is closed is therefore closed by name, and a path
// not named here is a path the child can read — which is why the test pins
// this list whole rather than sampling it.
//
// It is not a list of everything sensitive on a machine: that list cannot be
// written, and a rule denying the whole filesystem would deny the source tree
// with it. It is the set a log steering the agent would go for first: the SSH,
// cloud, Claude, GitHub and Fugaro configuration, the files a package manager
// or an infrastructure tool keeps a token or a cluster credential in, and
// /proc, whose environ entries hold every secret this process deliberately
// kept out of the child's environment.
var childReadDeny = []string{
	"~/.ssh/**",
	"~/.aws/**",
	"~/.config/gcloud/**",
	"~/.claude/**",
	"~/.config/fugaro/**",
	"~/.config/gh/**",
	"~/.config/rclone/**",
	"~/.docker/**",
	"~/.terraform.d/**",
	"~/.kube/**",
	"~/.gnupg/**",
	"~/.git-credentials",
	"~/.gitconfig",
	"~/.netrc",
	"~/.npmrc",
	"~/.pgpass",
	"~/.pypirc",
	"/proc/**",
}

// childSettings is the --settings payload, as a JSON document: the two things
// no flag expresses.
//
// disableAllHooks is there because a hook is a command the client runs with
// the child's environment, and this child's environment holds the credential
// (F7). No setting source is loaded, so nothing of the machine's declares one
// — and the payload says it anyway, because "nothing declares a hook" is an
// argument about the other flags and this is a statement about this one.
//
// The deny rules are childReadDeny, one rule per tool.
var childSettings = struct {
	DisableAllHooks bool `json:"disableAllHooks"`
	Permissions     struct {
		Deny []string `json:"deny"`
	} `json:"permissions"`
}{}

// childSettingsJSON is childSettings rendered once, when this package loads.
// Order is the struct's, so the value is the same on every run, which is what
// lets a test pin the command line whole.
var childSettingsJSON = renderChildSettings()

func renderChildSettings() string {
	s := childSettings
	s.DisableAllHooks = true
	for _, path := range childReadDeny {
		for _, tool := range childFileTools {
			s.Permissions.Deny = append(s.Permissions.Deny, tool+"("+path+")")
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		// Marshalling a struct of strings and a bool cannot fail: an error
		// here is a bug in this package and not something a person can reach.
		panic("diagnoseagent: the child's settings payload: " + err.Error())
	}
	return string(b)
}

// childEnvAllowList is what the child inherits from the person's environment:
// the variables a program needs to start and to reach a provider through a
// company proxy, and nothing that carries a value of its own. Everything else
// is dropped, so a second credential the machine holds — a cloud login, a
// GitHub token, a second model key — is unreachable from the diagnosis even
// though the process running it holds all of them.
//
// NODE_OPTIONS is deliberately absent although the runner's own agent allow-list
// has it: it names code the client would load at startup, which is a way to run
// something in a process holding the credential, and a diagnosis has no reason
// to need it. The proxy and CA variables are here because a machine behind one
// cannot reach a provider without them; a proxy URL may carry the person's own
// proxy login, which is theirs and is what the child must present.
var childEnvAllowList = []string{
	"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TERM", "TZ",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO",
}

// Cap bounds one diagnosis (D1613, F14): a local run on the person's own
// credential is metered by their provider and by no Fugaro ledger, so the two
// limits here are the whole of what stops a run that does not settle.
type Cap struct {
	// Turns is the client's --max-turns.
	Turns int
	// USD is the client's --max-budget-usd.
	USD float64
}

// DefaultCap is D1613's default bound: one dollar, and a turn count a
// diagnosis of one failed command fits inside several times over.
var DefaultCap = Cap{Turns: 30, USD: 1}

// withDefaults fills a field a caller left at zero. A cap is not optional, so
// "unset" means DefaultCap's value rather than no bound: a request that ran
// unbounded would be the one D1613 exists to refuse.
func (c Cap) withDefaults() Cap {
	if c.Turns <= 0 {
		c.Turns = DefaultCap.Turns
	}
	if c.USD <= 0 {
		c.USD = DefaultCap.USD
	}
	return c
}

// Request is one diagnosis run: what the child is given, and what bounds it.
type Request struct {
	// Handle is the credential the child is handed, and the only one it has
	// (F7, T-DG5). A zero Handle is refused: it is a caller that resolved no
	// credential, and a child started without one would reach its provider on
	// whatever else the machine holds.
	Handle Handle
	// Prompt is the whole prompt, from Prompt(e). It goes to the child on
	// stdin, and everything in it that is Fugaro's own text is Fugaro's: the
	// evidence's content is read by the agent from EvidenceDir rather than
	// copied in.
	Prompt string
	// Nonce is that prompt's fence nonce. The answer is read out of the block
	// this nonce fences and no other, so a fence a log guessed at cannot close
	// the answer early and name an outcome of its own (BlockOpen).
	Nonce string
	// Model is the model the child runs on, from D1607's key, read by the
	// command; "" leaves the client's own default.
	Model string
	// Cap bounds the run (D1613). A zero field takes DefaultCap's value.
	Cap Cap
	// SourceDir is the read-only source tree at the installed version: the
	// child's working directory, and the first of the two folders it may read.
	SourceDir string
	// EvidenceDir holds the redacted log, the doctor output, the version and
	// the effective configuration: the second folder, named with --add-dir.
	EvidenceDir string
	// Bin is the claude binary; "" is `claude` on PATH.
	Bin string
	// ParentEnv is the environment the child's own is composed from, in
	// os.Environ's form. nil is this process's own.
	ParentEnv []string
	// Transcript receives the client's raw stream, with the credential
	// redacted out of it. nil discards it.
	Transcript io.Writer
	// Stderr receives the client's own stderr, redacted the same way, so a
	// person watching the command sees it work. nil discards it.
	Stderr io.Writer
}

// Diagnosis is what one read-only run answered: the outcome the command reads
// back, the signature a saved finding is matched by, and the answer itself.
//
// Every string here has already passed the exact and the pattern pass with the
// child's credential in the list, and has had the terminal control characters
// a log can hold stripped out of it (F20): it is model output about untrusted
// text, and it is printed to a terminal.
type Diagnosis struct {
	// Outcome is one of Outcomes.
	Outcome string
	// Signature is the block's second line: the failing command and the error
	// class, which Fugaro hashes to recognise a bug it already knows about.
	Signature string
	// Text is the rest of the block: the cause and the fix, or the report.
	Text string
	// CostUSD is what the client reported the run cost.
	CostUSD float64
}

// Args returns the client command line of one diagnosis. The prompt goes on
// stdin, as it does for a run's own stages; everything a stage's argv would
// carry that lets an agent act — --dangerously-skip-permissions, a permission
// mode, a settings source of the machine's, an MCP configuration — is absent
// on purpose, and the value of --tools is what says so.
func Args(req Request) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", toolAllowList,
		// No setting source of the machine's own: this runs on the person's
		// machine, so the default would load their user level — their hooks,
		// their plugins, their MCP servers and their permission rules — into a
		// process holding the credential. The sources are a list of levels and
		// an empty token means no level; a settings file an administrator
		// manages still applies, which is policy and not a file this machine's
		// user or a log decides. --strict-mcp-config with no --mcp-config
		// beside it says the same for MCP servers, which a settings file is
		// otherwise a way in to.
		"--setting-sources", "",
		"--strict-mcp-config",
		// A session file is the client's own record of the run's reads and
		// answer, written under the person's home. A diagnosis is one question
		// asked once, and nothing it reads belongs in a store on their disk.
		"--no-session-persistence",
		"--settings", childSettingsJSON,
		"--add-dir", req.EvidenceDir,
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	cap := req.Cap.withDefaults()
	return append(args,
		"--max-turns", strconv.Itoa(cap.Turns),
		"--max-budget-usd", cap.budgetArg())
}

// budgetArg is the dollar cap as the client's flag takes it and as the
// messages below name it: one spelling, so the number the client is given and
// the number the person is told are always the same one. The value is written
// exactly rather than at two decimals, because rounding is a cap the client
// does not apply: a cap of a few tenths of a cent is "0.00" there, which is
// no budget at all, and the in-flight half of D1613 would be gone while the
// run still reported that it had one.
func (c Cap) budgetArg() string { return strconv.FormatFloat(c.USD, 'f', -1, 64) }

// Run starts the person's own claude as one child and returns the diagnosis it
// answered. It is the only place in this package that starts a process, and
// everything it hands that process comes from a Request field: the argv from
// Args, the environment from childEnvironment, the working directory from
// SourceDir, and the credential from the Handle and nowhere else.
func Run(ctx context.Context, req Request) (Diagnosis, error) {
	if err := req.validate(); err != nil {
		return Diagnosis{}, err
	}
	secrets := []string{req.Handle.Secret()}
	bin := req.Bin
	if bin == "" {
		bin = "claude"
	}

	out := &syncBuffer{}
	var stderr io.Writer
	var stderrRed *redact.Redactor
	var stderrW *syncWriter
	if req.Stderr != nil {
		stderrRed = redact.NewRedactor(req.Stderr, secrets)
		stderrW = &syncWriter{w: stderrRed}
		stderr = stderrW
	}
	parent := req.ParentEnv
	if parent == nil {
		parent = os.Environ()
	}
	code, runErr := procgroup.Run(ctx, procgroup.Cmd{
		Name:   bin,
		Args:   Args(req),
		Dir:    req.SourceDir,
		Env:    childEnvironment(parent, req.Handle),
		Stdin:  strings.NewReader(req.Prompt),
		Stdout: out,
		Stderr: stderr,
	})
	if stderrW != nil {
		stderrW.do(func() { _ = stderrRed.Flush() })
	}
	if runErr != nil {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent could not be run: %w", runErr)
	}
	// The stream is parsed after the child is gone rather than while it runs:
	// a diagnosis is one answer and not a run's long transcript, and reading
	// it whole is what lets an answer be checked as one document rather than
	// line by line.
	var transcript io.Writer
	var transcriptRed *redact.Redactor
	if req.Transcript != nil {
		transcriptRed = redact.NewRedactor(req.Transcript, secrets)
		transcript = transcriptRed
	}
	res, found, err := agent.ParseStream(bytes.NewReader(out.stream()), transcript)
	if transcriptRed != nil {
		_ = transcriptRed.Flush()
	}
	cap := req.Cap.withDefaults()
	switch {
	case err != nil:
		return Diagnosis{}, fmt.Errorf("reading the diagnosis agent's output: %w", err)
	case found && isCapSubtype(res.Subtype):
		// A run one of the two caps stopped is asked about before the answer
		// is read at all: its result event carries no block, so reading one
		// first would report "the agent wrote no block" for a run that ended
		// exactly as D1613 says a run that does not settle should end.
		return Diagnosis{}, fmt.Errorf("the diagnosis was stopped by the cap on this run: the agent ended with %s, which is claude's own report that it reached --max-turns %d or --max-budget-usd %s (D1613). Raise the cap and run it again if the answer is worth the spend",
			safetext.OneLine(res.Subtype), cap.Turns, cap.budgetArg())
	case !found:
		return Diagnosis{}, fmt.Errorf("the diagnosis agent exited with code %d without a result event, so it answered nothing", code)
	case res.IsError:
		return Diagnosis{}, fmt.Errorf("the diagnosis agent reported an error (%s) and answered nothing", safetext.OneLine(res.Subtype))
	}
	// The client's own accounting, checked rather than trusted (F14): the
	// client prices a run against its own estimate of the model, and a run
	// that reports more than the cap was given did not stay inside it whatever
	// its --max-budget-usd was set to.
	if res.CostUSD > cap.USD {
		return Diagnosis{}, fmt.Errorf("the diagnosis cost $%.2f and the cap for this run is $%s (D1613): the run went past the bound it was given, so its answer is not shown. Raise the cap and run it again if the answer is worth the spend", res.CostUSD, cap.budgetArg())
	}
	d, err := parseAnswer(res.Text, req.Nonce)
	if err != nil {
		return Diagnosis{}, err
	}
	d.CostUSD = res.CostUSD
	d.Signature = redactOutput(d.Signature, secrets)
	d.Text = redactOutput(d.Text, secrets)
	return d, nil
}

// validate refuses a request that would start a child this package cannot
// stand behind. Each of these is a caller's mistake rather than anything a
// person did, and each of them is a way the child would end up with less
// confinement or less of a credential than the command promises.
func (r Request) validate() error {
	switch {
	case r.Handle.Secret() == "":
		return fmt.Errorf("a diagnosis is run on one credential and this request carries none: the Handle is the only thing that says which account the child reaches its model on, and a child started without one would fall back on whatever else this machine's environment holds")
	case r.Prompt == "":
		return fmt.Errorf("a diagnosis needs the prompt to give the child, and this request carries none")
	case !nonceRE.MatchString(r.Nonce):
		return fmt.Errorf("a diagnosis's answer is read out of the block its own nonce fences, and this request carries no nonce from NewNonce")
	}
	for _, dir := range []struct {
		what string
		path string
	}{
		{"the source tree", r.SourceDir},
		{"the evidence folder", r.EvidenceDir},
	} {
		if dir.path == "" {
			return fmt.Errorf("%s is one of the two folders the agent may read and this request names none (D1602)", dir.what)
		}
		info, err := os.Stat(dir.path)
		if err != nil {
			return fmt.Errorf("%s is not readable at %s: %w", dir.what, dir.path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is %s, which is not a directory", dir.what, dir.path)
		}
	}
	return nil
}

// childEnvironment is the whole environment of the one child: the allow-listed
// variables the machine has, in os.Environ's form, and the handle's credential
// under its own name.
//
// The credential is written last and once. A name appearing twice is an answer
// to "which credential does this child present" that no two readers agree on —
// a getenv takes the first match and a map the last — so an allow-listed name
// that happened to be the handle's is skipped rather than preceding it.
func childEnvironment(parent []string, h Handle) []string {
	credential := h.Env()
	credName := ""
	if len(credential) > 0 {
		credName, _, _ = strings.Cut(credential[0], "=")
	}
	env := make([]string, 0, len(childEnvAllowList)+len(credential))
	for _, name := range childEnvAllowList {
		if name == credName {
			continue
		}
		if v := envValue(parent, name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return append(env, credential...)
}

// isCapSubtype reports whether a result event's subtype is the client saying
// one of the two caps ended the run. The names are matched by their stem
// rather than pinned whole: the two are claude's own words rather than a
// contract of Fugaro's, and a client that renames one to
// error_max_budget_exceeded still means the same thing, while a subtype that
// matches neither falls through to the check of what the run cost.
func isCapSubtype(sub string) bool {
	for _, stem := range []string{"max_turns", "max_budget"} {
		if strings.Contains(sub, stem) {
			return true
		}
	}
	return false
}

// parseAnswer reads the diagnosis out of the answer's block: the last block
// this diagnosis's nonce fences, whose first line names one of Outcomes and
// whose second is the signature.
//
// The last, and not the first: the prompt asks for the answer as the last
// thing the agent writes, and a log that carried a guessed fence would then be
// a block *before* the real one rather than the one read. The nonce is what
// makes a guess hopeless — it is 16 random hex digits generated for this run
// and written into no file the log can reach — and taking the last block is
// what keeps a wrong guess from mattering even so.
//
// Nothing this returns quotes the answer: an error here says what the block
// was missing, never what it said, because what it said is text Fugaro did not
// write and the message is printed.
func parseAnswer(s, nonce string) (Diagnosis, error) {
	open, closeFence := BlockOpen(nonce), BlockClose(nonce)
	i := strings.LastIndex(s, open)
	if i < 0 {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent wrote no answer: the block between the fences this diagnosis asked for is not in its output")
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, closeFence)
	if j < 0 {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent opened an answer block and never closed it")
	}
	lines := strings.Split(rest[:j], "\n")
	k := 0
	for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
		k++
	}
	if k >= len(lines) {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent's answer block is empty: its first line has to name the outcome")
	}
	outcome, ok := answerField(lines[k], OutcomeField)
	if !ok {
		return Diagnosis{}, fmt.Errorf("the first line of the diagnosis agent's answer block does not name the outcome as `%s: ...`, so the command cannot tell what it found", OutcomeField)
	}
	if !slices.Contains(Outcomes, outcome) {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent named an outcome Fugaro does not know: its first line has to say `%s: ` and then one of %s", OutcomeField, strings.Join(Outcomes, ", "))
	}
	if k+1 >= len(lines) {
		return Diagnosis{}, fmt.Errorf("the diagnosis agent's answer block states an outcome and nothing after it: the second line has to be `%s: <the failing command> <the error class>`", SignatureField)
	}
	signature, ok := answerField(lines[k+1], SignatureField)
	if !ok || signature == "" {
		return Diagnosis{}, fmt.Errorf("the second line of the diagnosis agent's answer block is not `%s: <the failing command> <the error class>`, which is the line a saved finding is matched by", SignatureField)
	}
	return Diagnosis{Outcome: outcome, Signature: signature, Text: strings.TrimSpace(strings.Join(lines[k+2:], "\n"))}, nil
}

// answerField reads one `name: value` line of the answer block.
func answerField(line, name string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), name+":")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// redactOutput is the last thing that happens to text before a person reads
// it: the terminal control characters come off first, then both redaction
// passes, exactly. The order is the point. An escape sequence can stand
// *inside* a value — a log line whose colour codes land in the middle of a
// token — and the exact pass is a string match that a byte in the wrong place
// defeats, so stripping first is what puts the bytes back in the order the
// passes can match. The pattern pass runs second, over text the exact pass has
// already taken the known values out of, and the masks go last so they cannot
// expose a path by rewriting the bytes in front of it (Report).
//
// This runs on model output and on nothing else: the log and the configuration
// the agent reads were redacted before the child ever started, and this is the
// second look rather than the only one (F20).
func redactOutput(s string, secrets []string) string {
	return redact.Report(redact.Text(safetext.Text(s, true), secrets))
}
