package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/cmdlog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/srctree"
)

// troubleshoot is the local diagnosis of a command that failed on this machine
// (docs/diagnose-agent, D1604): `fugaro diagnose` explains a cloud run, and
// this explains the failure the person is standing in front of. It runs on the
// machine and never as a job, because the cloud setup is often exactly what is
// broken, and it reads no stdin: the typed command is the consent (F13,
// D1606).
//
// Everything the diagnosis does that reaches outside this process is a package
// variable, one per call, so a test can drive the whole command in the
// hermetic world (no network, no git, no claude) and assert what it prints:
//
//   - troubleshootSource and troubleshootChanges, the source tree and the
//     "changes since your version" file (D1602, D1610, D1611);
//   - troubleshootRun, the read-only agent (D1602, D1613);
//   - troubleshootDoctor, `fugaro doctor`, whose output is one of the four
//     evidence files;
//   - readSecretValue, the project's account read out of Secret Manager with
//     the person's own gcloud login (D1601) — an existing seam, reused rather
//     than a second one beside it.
//
// This is the shape the package already uses for layerRead and getenvOS.

const (
	// troubleshootReportName is D1605's report file, written into the evidence
	// folder beside the four files the collector writes. The person reads it,
	// checks it and posts it; Fugaro files nothing.
	troubleshootReportName = "report.md"
	// troubleshootLogLines is how much of the redacted log the offline path
	// prints (D1608). It is the end of the log, which is where a command that
	// failed says why, and it is short enough to read in a terminal.
	troubleshootLogLines = 40
	// troubleshootNoteLimit bounds one evidence file the offline path prints.
	// The doctor's output and the log are the local user's own text and are
	// printed as they are; this bound is only so that a file a hand has filled
	// with megabytes cannot fill the terminal.
	troubleshootNoteLimit = 512 << 10
)

// troubleshootSource, troubleshootChanges and troubleshootRun are the three
// calls this command makes outside its own process, named here because a test
// replaces them (the header comment above).
var (
	troubleshootSource  = srctree.Ensure
	troubleshootChanges = srctree.ChangesSince
	troubleshootRun     = diagnoseagent.Run
)

type troubleshootOptions struct {
	asJSON bool
	// maxTurns and maxBudgetUSD are D1613's cap. They are flags rather than
	// constants because a hard failure may need a longer look, and they are
	// never absent: a diagnosis that ran unbounded on a metered key is the
	// thing the cap exists to refuse.
	maxTurns     int
	maxBudgetUSD float64
}

// troubleshootDoc is `fugaro troubleshoot --json`. It is one shape for both
// answers the command gives: a diagnosis, and the offline path's evidence
// (D1608), which is told apart by Offline.
type troubleshootDoc struct {
	RunID   string `json:"run_id"`
	Command string `json:"command"`
	Version string `json:"version"`
	Model   string `json:"model"`
	// EvidenceDir and Guide are where the evidence is and where the guide a
	// person may read lives: both are printed by the offline path (D1608).
	EvidenceDir string `json:"evidence_dir"`
	Guide       string `json:"guide"`
	// SourceNote is set when the tree the agent read may not be this build
	// (D1611), which is the one thing a finding resting on a line of it has to
	// be read against.
	SourceNote string `json:"source_note,omitempty"`

	Outcome    string            `json:"outcome,omitempty"`
	Signature  string            `json:"signature,omitempty"`
	Answer     string            `json:"answer,omitempty"`
	Fixes      []troubleshootFix `json:"fixes,omitempty"`
	CostUSD    float64           `json:"cost_usd,omitempty"`
	ReportPath string            `json:"report_path,omitempty"`

	Offline bool     `json:"offline,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	LogTail []string `json:"log_tail,omitempty"`
	Doctor  string   `json:"doctor,omitempty"`
}

// troubleshootFix is one fix command D1614's check reached a verdict on: what
// was printed, and whether the CLI's own command tree holds it. Only a Checked
// command is shown as runnable.
type troubleshootFix struct {
	Command string   `json:"command"`
	Argv    []string `json:"argv"`
	Checked bool     `json:"checked"`
	Reason  string   `json:"reason,omitempty"`
}

func newTroubleshootCmd() *cobra.Command {
	var o troubleshootOptions
	cmd := &cobra.Command{
		Use:   "troubleshoot [RUN ID]",
		Short: "Explain a Fugaro command that failed on this machine, locally",
		Long: `Explain a Fugaro command that failed on this machine: its log, fugaro
doctor's output, the effective configuration and the Fugaro source at the
version that failed, read by a local agent that can only read (no shell, no
writes, no network) and answers with the cause and the fix.

With no RUN ID it takes the newest logged command that failed. RUN ID is the
one the failed command printed (update-20261107183012-48213), which is also
the name of its log. A cloud run's <repo-slug>/<run-id> is a different thing
and is refused here: fugaro diagnose explains those.

Log text is data: it holds Terraform and gcloud error bodies, API responses
and file names, so a command the answer prints is checked against this CLI's
own command tree before it is shown as runnable, and anything else is marked
as unchecked.

The command reads no stdin. Typing it is the consent: one line says which
provider and what class of data the redacted evidence goes to, and then the
diagnosis runs. With no credential, or with the source at this version out of
reach, it prints the evidence where it is and exits non-zero instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var ref string
			if len(args) == 1 {
				ref = args[0]
			}
			return runTroubleshoot(cmd, &o, ref)
		},
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	// The defaults are D1613's own, written out rather than left at zero: a
	// zero field takes the same value (Cap.withDefaults), and a reference page
	// that printed 0 would read as "no bound" to the person the cap protects.
	cmd.Flags().IntVar(&o.maxTurns, "max-turns", diagnoseagent.DefaultCap.Turns, "bound the diagnosis to this many agent turns")
	cmd.Flags().Float64Var(&o.maxBudgetUSD, "max-budget-usd", diagnoseagent.DefaultCap.USD, "bound the diagnosis to this many US dollars")
	return cmd
}

func runTroubleshoot(cmd *cobra.Command, o *troubleshootOptions, ref string) error {
	ctx := cmd.Context()
	id, err := troubleshootID(getenvOS, ref)
	if err != nil {
		return err
	}
	// The configuration is read before anything else is collected: it is both
	// one of the four evidence files and where D1607's model comes from.
	res := readTroubleshootResolution(ctx)
	// The credential is resolved before the evidence is collected, and not
	// because collecting needs one: whatever credential the diagnosis may use
	// has to be in the redaction list the evidence files are written through
	// (F4, spec.md), and the project account's token is a value this machine's
	// logs may hold — a command that printed it, an API body that carried it.
	// Its failure is reported later, where it was reported before: after the
	// source fetch, so that D1608's answer about the machine is the one the
	// person reads.
	handle, credErr := troubleshootCredential(ctx, res)
	secrets := cliSecrets(getenvOS)
	if credErr == nil {
		secrets = append(secrets, handle.Secret())
	}
	ev, err := diagnoseagent.Collect(ctx, id, diagnoseagent.Options{
		Version: Version,
		Commit:  Commit,
		Getenv:  getenvOS,
		GOOS:    runtime.GOOS,
		Doctor:  troubleshootDoctor,
		Config:  func(context.Context) ([]byte, error) { return res.text() },
		Secrets: secrets,
	})
	if err != nil {
		return userErr("%v", err)
	}
	d := troubleshootDoc{
		RunID: ev.RunID, Command: ev.Command, Version: ev.Version,
		EvidenceDir: ev.EvidenceDir, Guide: diagnoseagent.GuidePath(),
		Model: res.model(),
	}
	// The source tree is fetched next: it makes no call that needs the
	// credential, and D1608's path is the answer when the machine cannot
	// reach it.
	dir, rec, err := troubleshootSource(ctx, srctree.Options{Version: Version, Commit: Commit, Getenv: getenvOS, GOOS: runtime.GOOS})
	if err != nil {
		return troubleshootOffline(cmd, o, ev, d, fmt.Errorf("the Fugaro source at %s could not be fetched, so there is nothing for the agent to read the code against: %w", Version, err))
	}
	ev.SourceDir, ev.SourceNote = dir, troubleshootSourceNote(rec)
	d.SourceNote = ev.SourceNote
	// The guide the offline path prints is the one in that tree, beside the
	// code it is about; GuidePath alone is where it is in a repository.
	d.Guide = filepath.Join(dir, diagnoseagent.GuidePath())
	if err := troubleshootChanges(ctx, srctree.Options{Version: Version, Commit: Commit, Getenv: getenvOS, GOOS: runtime.GOOS}, dir, filepath.Join(ev.EvidenceDir, troubleshootChangesName)); err != nil {
		// The file lists the releases newer than the installed one, which is
		// what a fixed_in_newer_release answer reads: a git-less machine is
		// D1611's ordinary case and this is a note, not the end of anything.
		fmt.Fprintf(cmd.ErrOrStderr(), "note: the changes since your version were not written: %v\n", oneLine(err.Error()))
	}

	if credErr != nil {
		return troubleshootOffline(cmd, o, ev, d, credErr)
	}
	ev.Nonce = diagnoseagent.NewNonce()
	troubleshootEgress(cmd, o, d.Model, handle)
	diag, err := troubleshootRun(ctx, diagnoseagent.Request{
		Handle:      handle,
		Prompt:      diagnoseagent.Prompt(ev),
		Nonce:       ev.Nonce,
		Model:       d.Model,
		Cap:         diagnoseagent.Cap{Turns: o.maxTurns, USD: o.maxBudgetUSD},
		SourceDir:   dir,
		EvidenceDir: ev.EvidenceDir,
		ParentEnv:   os.Environ(),
		Stderr:      cmd.ErrOrStderr(),
	})
	if err != nil {
		return troubleshootOffline(cmd, o, ev, d, err)
	}
	d.Outcome, d.Signature, d.CostUSD = diag.Outcome, diag.Signature, diag.CostUSD
	check := troubleshootChecker(cmd.Root())
	d.Answer = diagnoseagent.FixText(diag.Text, check)
	for _, f := range diagnoseagent.Fixes(diag.Text, check) {
		d.Fixes = append(d.Fixes, troubleshootFix{Command: f.Command, Argv: f.Argv, Checked: f.Checked, Reason: f.Reason})
	}
	if troubleshootCarriesReport(diag.Outcome) {
		path, err := troubleshootWriteReport(ev, d)
		if err != nil {
			return userErr("%v", err)
		}
		d.ReportPath = path
	}
	return printTroubleshoot(cmd.OutOrStdout(), d, o.asJSON)
}

// troubleshootChangesName is the "changes since your version" file's name
// inside the evidence folder. Fugaro writes it (D1602), which is why the
// command names it and the guide only says it is there.
const troubleshootChangesName = "changes.txt"

// troubleshootID is the run the command diagnoses: the reference the person
// typed, or the newest logged command that failed. A cloud run's reference is
// refused rather than parsed: the two commands explain two different things
// (D1600), and a person who typed the wrong one is told which to run.
func troubleshootID(getenv func(string) string, ref string) (cmdlog.ID, error) {
	if ref == "" {
		id, err := cmdlog.Latest(getenv, true)
		switch {
		case errors.Is(err, cmdlog.ErrNoLog):
			return cmdlog.ID{}, userErr("no failed fugaro command is logged on this machine, so there is nothing to explain: run the command that failed and it will be logged, or pass the run ID it printed (for example update-20261107183012-48213)")
		case err != nil:
			return cmdlog.ID{}, userErr("%v", err)
		}
		return id, nil
	}
	if strings.Contains(ref, "/") {
		return cmdlog.ID{}, userErr("%q is a cloud run, and this command explains a fugaro command that failed on this machine: run `fugaro diagnose %s` for a run", ref, ref)
	}
	id, err := cmdlog.ParseID(ref)
	if err != nil {
		return cmdlog.ID{}, userErr("%v (a run ID is what the failed command printed, for example update-20261107183012-48213)", err)
	}
	return id, nil
}

// troubleshootResolution is what this machine's configuration resolves to,
// read before the diagnosis starts: the checkout's fugaro.yaml over the
// project layer that reaches it (in a checkout), or the project layer's own
// file (outside one). It is both the effective configuration the agent is
// given and where D1607's model comes from.
//
// Nothing here is fatal. A configuration that cannot be read is a note in the
// evidence folder rather than the end of the diagnosis: a configuration that
// contradicts what Fugaro expects is a large share of what this command
// exists to explain (F23), and the agent can say so from the log alone.
type troubleshootResolution struct {
	// lc is the selected project config, the same read the credential step
	// uses: the account names and the layer file are both in it, and reading
	// it twice would let two steps of one diagnosis disagree about which
	// project it is about.
	lc    *localcfg.Config
	cfg   *config.Config
	layer *config.ProjectLayer
	notes []string
}

// model is D1607's key: the repository's own agent.models.diagnose, else the
// project layer's defaults.agent.models.diagnose — which is what applies
// outside a checkout, where the repository has no say — else Fugaro's own
// choice. It never falls back on a run's model: a diagnosis is few calls of a
// model that reads code and logs, and it must not follow a setting that moves
// every run.
func (r troubleshootResolution) model() string {
	if r.cfg != nil && r.cfg.Agent.Models.Diagnose != "" {
		return r.cfg.Agent.Models.Diagnose
	}
	if r.layer != nil && r.layer.Defaults.Agent.Models.Diagnose != "" {
		return r.layer.Defaults.Agent.Models.Diagnose
	}
	return config.DefaultDiagnoseModel
}

// text is the effective configuration as one YAML document: the values the
// checkout resolves to, the project layer's own file beside them, and the
// notes about what could not be read. nil when there is nothing at all to
// write, which the collector writes down as such.
func (r troubleshootResolution) text() ([]byte, error) {
	if r.cfg == nil && r.layer == nil && len(r.notes) == 0 {
		return nil, nil
	}
	doc := map[string]any{}
	if r.cfg != nil {
		values, err := yaml.Marshal(r.cfg)
		if err != nil {
			return nil, err
		}
		var tree map[string]any
		if err := yaml.Unmarshal(values, &tree); err != nil {
			return nil, err
		}
		doc["resolved"] = tree
	}
	if r.layer != nil {
		doc["project_layer"] = r.layer
	}
	if len(r.notes) > 0 {
		doc["notes"] = r.notes
	}
	return yaml.Marshal(doc)
}

// readTroubleshootResolution reads the two documents above, best effort: a
// checkout whose file does not resolve falls through to the project layer's
// own file, and every failure is a note rather than an error.
func readTroubleshootResolution(ctx context.Context) troubleshootResolution {
	var r troubleshootResolution
	lc := selectedProjectConfigReadOnly(ctx)
	r.lc = lc
	// The checkout is resolved without recording it: this command explains a
	// failure and changes nothing on the machine, and remembering a checkout
	// is a write.
	co, err := resolveCheckout(ctx, "")
	switch {
	case err != nil:
		r.notes = append(r.notes, oneLine(err.Error()))
	case co != nil:
		data, err := readFugaroYAML(filepath.Join(co.Root, "fugaro.yaml"))
		if err != nil {
			r.notes = append(r.notes, oneLine(err.Error()))
			break
		}
		rf, err := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
		switch {
		case err != nil:
			r.notes = append(r.notes, oneLine(err.Error()))
		case len(rf.Problems) > 0:
			for _, p := range rf.Problems {
				r.notes = append(r.notes, oneLine(problemText(p)))
			}
		default:
			r.cfg, r.layer = rf.Cfg, rf.Layer.Layer
			if rf.Layer.Note != "" {
				r.notes = append(r.notes, oneLine(rf.Layer.Note))
			}
			return r
		}
	}
	// No checkout, or a file that did not resolve as a whole: the project
	// layer's own value applies, out of this machine's copy of it. The
	// published object is deliberately not read — the cloud is often what
	// failed — which is the same answer D1608 gives for the model key.
	if lc == nil {
		return r
	}
	path, err := localcfg.LayerFile(getenvOS, lc.Name)
	if err != nil {
		return r
	}
	data, err := readLayerFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.notes = append(r.notes, oneLine(err.Error()))
		}
		return r
	}
	layer, ps := projectLayerProblems(data, lc)
	if len(ps) > 0 {
		for _, p := range ps {
			r.notes = append(r.notes, oneLine(problemText(p)))
		}
		return r
	}
	r.layer = layer
	return r
}

// problemText is one configuration problem as one line, in the shape the
// person reads elsewhere in the CLI (path, then what is wrong with it).
func problemText(p config.Problem) string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// troubleshootCredential resolves the credential the diagnosis may use
// (D1601): this machine's own first, then the project's account over the
// person's own gcloud login, in memory and behind its allow_data_to.
func troubleshootCredential(ctx context.Context, res troubleshootResolution) (diagnoseagent.Handle, error) {
	accounts := diagnoseagent.Accounts{Repo: troubleshootRepo(ctx)}
	var read diagnoseagent.SecretReader
	if lc := res.lc; lc != nil {
		accounts.Block = lc.ClaudeAccounts
		accounts.Default = lc.DefaultClaudeAccount
		// The stages are left out: a local diagnosis stands for no stage of a
		// run, so every per-stage choice is about some other command's work.
		read = func(name string) (string, error) {
			b, err := readSecretValue(ctx, lc, name)
			return string(b), err
		}
	}
	return diagnoseagent.Resolve(os.Environ(), accounts, read)
}

// troubleshootRepo is this checkout's repository (owner/name), "" outside one.
// The account's allow_data_to is a per-repository list, and a command with no
// repository is refused the project's account rather than guessed at (F22).
func troubleshootRepo(ctx context.Context) string {
	if co, err := resolveCheckout(ctx, ""); err == nil && co != nil {
		if _, repo := checkoutOriginAt(co.Root); repo != "" {
			return repo
		}
	}
	return ""
}

// troubleshootSourceNote is what the person is told about the tree the agent
// read: D1611's sentence when the build could not be tied to it, and nothing
// when it could.
func troubleshootSourceNote(rec srctree.Record) string {
	if !rec.MayNotMatch {
		return ""
	}
	return fmt.Sprintf("this is a %s build of fugaro with no release tag, so the source tree is the newest release (%s) and may not be the code that failed", Version, rec.Version)
}

// troubleshootEgress is D1606's one line, printed before anything is sent and
// asking nothing: the typed command is the consent (F13). It names the
// provider, which account pays, and what class of data goes.
//
// In --json mode it goes to stderr, so that the document on stdout stays one
// document; the same sentence is what a person reads either way.
func troubleshootEgress(cmd *cobra.Command, o *troubleshootOptions, model string, h diagnoseagent.Handle) {
	account := "the project's Claude account " + h.Account()
	if h.Own() {
		account = "your own Claude login"
	}
	line := fmt.Sprintf("Sending to Anthropic on %s: the redacted log, fugaro doctor's output, the effective configuration and the Fugaro source at this version, read by the model %s.\n", account, oneLine(model))
	if o.asJSON {
		fmt.Fprint(cmd.ErrOrStderr(), line)
		return
	}
	fmt.Fprint(cmd.OutOrStdout(), line)
}

// troubleshootCarriesReport is the outcome whose answer is an issue report
// (D1605): a bug answer and an unclear one carry the same report, and a
// configuration or a fixed-in-a-newer-release answer carries none.
func troubleshootCarriesReport(outcome string) bool {
	return outcome == diagnoseagent.OutcomeBug || outcome == diagnoseagent.OutcomeUnclear
}

// troubleshootWriteReport writes D1605's report file into the evidence folder
// and returns its path. It holds the answer as the agent wrote it, under a
// header naming the run it is about, and nothing else: it is what a person
// reads, checks and posts, and Fugaro files nothing itself.
//
// The header carries no local path, and the answer is model output that has
// already passed both redaction passes with the child's credential in the list
// (F20). What neither can promise is that the text holds no name a person
// would not want public, which is what the guide tells the model to strip and
// what the report's own last line tells the reader to check.
func troubleshootWriteReport(ev diagnoseagent.Evidence, d troubleshootDoc) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s failed\n\n", d.Command)
	fmt.Fprintf(&b, "- fugaro %s (%s)\n", d.Version, d.RunID)
	if d.Signature != "" {
		fmt.Fprintf(&b, "- signature: %s\n", oneLine(d.Signature))
	}
	b.WriteString("\nRedacted, and review before posting it to an issue.\n\n")
	b.WriteString(multiLine(d.Answer))
	b.WriteString("\n")
	path := filepath.Join(ev.EvidenceDir, troubleshootReportName)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("the issue report could not be written: %w", err)
	}
	return path, nil
}

// troubleshootOffline is D1608: with no credential, with the source out of
// reach, or with the agent unable to answer, the evidence is printed where it
// is and the command exits non-zero with the reason. The person gets something
// to read and to keep — the log's end, the doctor's output and the version —
// rather than a failure line and nothing else.
func troubleshootOffline(cmd *cobra.Command, o *troubleshootOptions, ev diagnoseagent.Evidence, d troubleshootDoc, reason error) error {
	d.Offline, d.Reason = true, oneLine(reason.Error())
	d.LogTail = troubleshootLogTail(ev)
	d.Doctor = troubleshootEvidenceFile(ev, "doctor.txt")
	// The model is printed with the rest of the header and no answer is: no
	// agent ran, so there is none to print.
	if werr := printTroubleshoot(cmd.OutOrStdout(), d, o.asJSON); werr != nil {
		return werr
	}
	return &ExitError{Code: ExitUserError, Err: errors.New(d.Reason)}
}

// troubleshootLogTail is the end of the redacted log the collector wrote, in
// the shape the offline path and the document print it.
func troubleshootLogTail(ev diagnoseagent.Evidence) []string {
	text := troubleshootEvidenceFile(ev, ev.RunID+".log")
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > troubleshootLogLines {
		lines = lines[len(lines)-troubleshootLogLines:]
	}
	return lines
}

// troubleshootEvidenceFile reads one file of the evidence folder, "" when it
// is not there. The folder is this user's own, written 0600 inside a 0700
// directory by the collector, and the read is bounded so that a file something
// else filled cannot fill the terminal.
func troubleshootEvidenceFile(ev diagnoseagent.Evidence, name string) string {
	f, err := os.Open(filepath.Join(ev.EvidenceDir, filepath.Base(name)))
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, troubleshootNoteLimit))
	if err != nil {
		return ""
	}
	return string(b)
}

// troubleshootDoctor runs `fugaro doctor` the way the person would and keeps
// what it printed: it is one of the four evidence files. The collector's own
// timeout bounds the call (F23) — doctor makes cloud calls, and a cloud that
// does not answer is one of the things this diagnosis exists to explain.
//
// Doctor exits non-zero when a check fails, which is the ordinary state of a
// machine whose command just failed: that is not a failure to run doctor, so
// the text is kept and the exit is swallowed. Nothing printed at all is the one
// case with nothing to keep, and the collector writes that down as such.
var troubleshootDoctor = func(ctx context.Context) ([]byte, error) {
	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil && buf.Len() == 0 {
		return nil, err
	}
	return buf.Bytes(), nil
}

// troubleshootShellSyntax is the characters that make a word more than an
// argument of the command it stands beside. A line Fugaro vouches for is one
// command with the arguments that command takes: `;`, `&&` and `|` chain a
// second command onto it, a redirect writes a file, and a substitution, a
// quote or a backslash hides what the rest of the line runs. The words come
// from the log as readily as from the model — a Terraform body, an API
// response or a file name can put them there (F5) — and the label is what a
// person reads before pasting, so a line carrying any of them is one Fugaro
// cannot vouch for. The skill lint splits its lines on these same operators
// before it resolves a command; here the line is refused instead, because the
// whole line is what gets printed.
const troubleshootShellSyntax = ";|&$()`<>\\\"'"

// troubleshootChecker is D1614's check against the command tree this binary
// was built with. Only a `fugaro` command whose subcommand path exists, whose
// flags are that command's flags, and that is one command line rather than
// several, comes back checked; everything else is shown as text marked as
// unchecked.
//
// The walk is the skill lint's, for the same reason: a word is a subcommand
// when the command so far has one by that name, and a flag belongs to the
// command the path has reached. It is deliberately one direction — a command
// it does not understand is not checked, never silently accepted.
func troubleshootChecker(root *cobra.Command) diagnoseagent.Checker {
	return func(argv []string) error {
		if len(argv) == 0 || argv[0] != "fugaro" {
			return errors.New("it is not a fugaro command")
		}
		for _, w := range argv[1:] {
			if i := strings.IndexAny(w, troubleshootShellSyntax); i >= 0 {
				return fmt.Errorf("it carries %q, so it is not one command with the arguments that command takes", string(w[i]))
			}
		}
		cmd := root
		i, path := 1, 0
		for i < len(argv) {
			w := argv[i]
			if strings.HasPrefix(w, "-") {
				name, inline := flagToken(w)
				f := commandFlag(cmd, name)
				if f == nil {
					return fmt.Errorf("%s has no --%s flag", cmd.CommandPath(), name)
				}
				i++
				if !inline && commandFlagTakesValue(f) && i < len(argv) {
					i++
				}
				continue
			}
			sub := commandByName(cmd, w)
			if sub == nil {
				break
			}
			// A hidden command is in the tree without being one Fugaro offers
			// a person: the credentials service's entry point is hidden on
			// purpose, and cobra's own help and completion are hidden by
			// default. The skill lint refuses a line naming one, and the label
			// here says Fugaro endorses the line, so it is refused as well.
			if sub.Hidden {
				return fmt.Errorf("%s is not a command Fugaro offers a person", sub.CommandPath())
			}
			cmd, path, i = sub, path+1, i+1
		}
		if path == 0 {
			if i < len(argv) {
				return fmt.Errorf("fugaro has no subcommand %q", argv[i])
			}
			return errors.New("it names no fugaro subcommand")
		}
		if i < len(argv) && len(cmd.Commands()) > 0 && !cmd.Runnable() {
			return fmt.Errorf("%s has no subcommand %q", cmd.CommandPath(), argv[i])
		}
		// What is left is the command's own arguments and flags: a bare word is
		// the command's business, and a flag has to be one it takes.
		for ; i < len(argv); i++ {
			w := argv[i]
			if !strings.HasPrefix(w, "-") {
				continue
			}
			name, inline := flagToken(w)
			f := commandFlag(cmd, name)
			if f == nil {
				return fmt.Errorf("%s has no --%s flag", cmd.CommandPath(), name)
			}
			if !inline && commandFlagTakesValue(f) && i < len(argv) {
				i++
			}
		}
		return nil
	}
}

// commandByName is cmd's subcommand called name or carrying it as an alias.
func commandByName(cmd *cobra.Command, name string) *cobra.Command {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name || sub.HasAlias(name) {
			return sub
		}
	}
	return nil
}

// commandFlag is the flag of cmd (or of a command above it) that name or its
// one-letter shorthand stands for, nil when the command has none.
func commandFlag(cmd *cobra.Command, name string) *pflag.Flag {
	// InheritedFlags merges the persistent flags of every command above this
	// one into Flags, so the one set below holds everything it accepts.
	cmd.InheritedFlags()
	set := cmd.Flags()
	if f := set.Lookup(name); f != nil {
		return f
	}
	// A shorthand is one letter by definition, and pflag panics rather than
	// answering nil for anything longer: a flag a model printed as --nope is
	// an unknown flag, not a reason to take the command down.
	if len(name) == 1 {
		return set.ShorthandLookup(name)
	}
	return nil
}

// commandFlagTakesValue reports whether a flag consumes the word after it, "" a
// flag that does not: a boolean flag's value is the flag itself, so the word
// after it belongs to the command.
func commandFlagTakesValue(f *pflag.Flag) bool { return f.Value.Type() != "bool" }

// flagToken splits one word of a command line into the flag it names and
// whether it carries its value in the same word (--json=x, -vx).
func flagToken(w string) (name string, inline bool) {
	name = strings.TrimLeft(w, "-")
	if n, _, ok := strings.Cut(name, "="); ok {
		return n, true
	}
	return name, false
}

// printTroubleshoot prints the document as JSON, or as a short labelled
// section per item. Every string that came from the agent goes through oneLine
// or multiLine: it is model output about text nobody here wrote.
func printTroubleshoot(w io.Writer, d troubleshootDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run:       %s (%s)\n", oneLine(d.RunID), oneLine(d.Command))
	fmt.Fprintf(&b, "Version:   fugaro %s\n", oneLine(d.Version))
	fmt.Fprintf(&b, "Model:     %s\n", oneLine(d.Model))
	if d.SourceNote != "" {
		fmt.Fprintf(&b, "Source:    %s\n", oneLine(d.SourceNote))
	}
	if d.Offline {
		fmt.Fprintf(&b, "\nThe diagnosis did not run: %s\n", d.Reason)
		fmt.Fprintf(&b, "\nEvidence   %s\n", oneLine(d.EvidenceDir))
		fmt.Fprintf(&b, "Guide      %s\n", oneLine(d.Guide))
		if len(d.LogTail) > 0 {
			fmt.Fprintf(&b, "\nThe last %d lines of the log\n%s\n", len(d.LogTail), indent(multiLine(strings.Join(d.LogTail, "\n"))))
		}
		if d.Doctor != "" {
			fmt.Fprintf(&b, "\nfugaro doctor said\n%s\n", indent(multiLine(strings.TrimRight(d.Doctor, "\n"))))
		}
		_, err := io.WriteString(w, b.String())
		return err
	}
	fmt.Fprintf(&b, "Outcome:   %s\n", oneLine(d.Outcome))
	if d.Signature != "" {
		fmt.Fprintf(&b, "Signature: %s\n", oneLine(d.Signature))
	}
	fmt.Fprintf(&b, "\n%s\n", multiLine(strings.TrimRight(d.Answer, "\n")))
	if d.CostUSD > 0 {
		fmt.Fprintf(&b, "\nCost:      $%.4f (the model this diagnosis ran on)\n", d.CostUSD)
	}
	if d.ReportPath != "" {
		fmt.Fprintf(&b, "Report:    %s\n", oneLine(d.ReportPath))
	}
	fmt.Fprintf(&b, "Evidence:  %s\n", oneLine(d.EvidenceDir))
	fmt.Fprintf(&b, "Guide:     %s\n", oneLine(d.Guide))
	_, err := io.WriteString(w, b.String())
	return err
}
