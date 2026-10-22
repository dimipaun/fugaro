package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The inputs a first run needs (design §3.1, "Inputs and defaults"): the
// Fugaro project's name, the GCP project's ID and the region. In a terminal
// each is asked once, with a suggestion Enter accepts; without one (or with
// --non-interactive, --yes or --json, which never prompt) the run is refused
// with every flag still missing named in one message. Once the local config
// exists nothing is asked.
//
// The GCP project's ID is the one question with two suggestions (D4): the
// default is a project to create, named after this one — `fugaro-<name>`,
// defaultGCPProject — and, when it differs, the project this machine is
// already working in (suggestGCPProject: GOOGLE_CLOUD_PROJECT or the quota
// project of the application default credentials) is offered second, with the
// one line saying why. That second suggestion never comes from gcloud's
// configured default project (the standing rule: a default project is how a
// command lands in the wrong project), so neither gcloud's properties file nor
// CLOUDSDK_CORE_PROJECT is read here.

const defaultRegion = "us-east5"

// regionRE is the shape of a Google Cloud region (us-east5, europe-west4).
var regionRE = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)

var nameJunkRE = regexp.MustCompile(`[^a-z0-9]+`)

// sanitiseName turns s into a project name (config.ProjectNameRE: 1 to 40 of
// a-z, 0-9 and '-', starting and ending with a letter or digit), or "" when
// nothing usable is left.
func sanitiseName(s string) string {
	s = strings.Trim(nameJunkRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if !config.ProjectNameRE.MatchString(s) {
		return ""
	}
	return s
}

// suggestName is the name suggested for the project: the owner of the
// checkout's origin repository, else the checkout's (or the working
// directory's) own name, sanitised.
func suggestName(ctx context.Context) string {
	_, n, _ := originOwner(ctx)
	if n != "" {
		return n
	}
	return dirName(ctx)
}

// originOwner is the checkout's origin repository's owner, sanitised.
func originOwner(ctx context.Context) (repo, name string, ok bool) {
	if repo, err := originRepo(ctx); err == nil {
		owner, _, _ := strings.Cut(repo, "/")
		if n := sanitiseName(owner); n != "" {
			return repo, n, true
		}
	}
	return "", "", false
}

func dirName(ctx context.Context) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	if out, err := gitCmd(ctx, ".", "rev-parse", "--show-toplevel").Output(); err == nil {
		dir = strings.TrimSpace(string(out))
	}
	return sanitiseName(filepath.Base(dir))
}

// adcPath is where the application default credentials are, through the one
// builder the CLI shares (adcpath.go's adcFilePath: F29 — this copy had no
// $XDG_CONFIG_HOME step where the other two had one, so the wizard and the
// rest of the CLI disagreed whenever that variable was set; the step itself
// was wrong on every platform, because gcloud reads $CLOUDSDK_CONFIG and no
// XDG variable, and all three put the file under `.config` on Windows). Tests
// replace it.
var adcPath = func() string {
	p, err := adcFilePath(os.Getenv, runtime.GOOS)
	if err != nil {
		return ""
	}
	return p
}

// adcQuotaProject is the quota project the application default credentials
// name. Only that one field is decoded; nothing else of the file is kept.
func adcQuotaProject() string {
	p := adcPath()
	if p == "" {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	var c struct {
		Quota string `json:"quota_project_id"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&c) != nil || !infra.ValidProjectID(c.Quota) {
		return ""
	}
	return c.Quota
}

// suggestGCPProject is the GCP project ID the credentials name — the project
// this machine is already working in — or "": never gcloud's configured
// default project. It is the wizard's **second** suggestion for the ID (D4),
// offered after the one below.
func suggestGCPProject() string {
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); infra.ValidProjectID(v) {
		return v
	}
	return adcQuotaProject()
}

// defaultGCPProject is the ID a first run suggests for a new GCP project: the
// Fugaro project's own name, prefixed (reconciliation F3, D4, answered
// 2026-10-20 as recommended). The wizard's default is therefore a name to
// *create* — fugaro-<name> — where the suggestion it used to make was a
// project to adopt.
//
// The prefix is not negotiable and the name is sanitised and cut to what an ID
// holds (6 to 30 characters, infra.ValidProjectID), so Enter is always a legal
// answer; "" is the answer for a name that leaves nothing to build one from,
// and the question then suggests nothing. The buckets derive from the ID, so a
// project that takes this default runs in fugaro-runs-fugaro-<name>: the
// double prefix is accepted, and nothing about bucket naming moves (D4).
//
// It is never suggestGCPProject(): a project this machine already works in is
// a project to adopt, and the wizard offers it as the second suggestion.
func defaultGCPProject(name string) string {
	n := sanitiseName(name)
	if n == "" {
		return ""
	}
	id := "fugaro-" + n
	if len(id) > 30 {
		id = strings.Trim(id[:30], "-")
	}
	if !infra.ValidProjectID(id) {
		return ""
	}
	return id
}

// projectIDSuggestions is what the ID question offers: the default this run
// would create — fugaro- plus the name this run would itself suggest (--name,
// the selected config's name, else suggestName) — and, when the credentials
// name a different project, that one second, with the line saying why.
//
// A checkout's fugaro.yaml naming a project does **not** become the ID's
// default, for the reason the name question has none from it either (a cloned
// repository's file can say anything): the name is then typed, and so is the
// ID built from it.
func projectIDSuggestions(ctx context.Context, name, fromCheckout string) (def, alt, why string) {
	switch {
	case name != "":
		def = defaultGCPProject(name)
	case fromCheckout == "":
		def = defaultGCPProject(suggestName(ctx))
	}
	if c := suggestGCPProject(); c != "" && c != def {
		return def, c, "you are already working in " + pluginwire.Printable(c)
	}
	return def, "", ""
}

// missingInput is one input a first run lacks.
type missingInput struct{ flag, what string }

func (m missingInput) String() string { return m.flag + " (" + m.what + ")" }

// firstRunMissing is what creating the project config still needs: the name
// (not with --config-only, which takes it from the installation), the GCP
// project and the region, or none when a config is selected.
func (r *initRun) firstRunMissing(ctx context.Context) (name, fromCheckout string, missing []missingInput, creating bool) {
	o := r.o
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return "", "", nil, false // loadInitConfig reports it
	}
	sel, lc, err := selectNamed(o.cloud, co, true, o.name)
	if err != nil || lc != nil {
		return "", "", nil, false
	}
	name = o.name
	if name == "" && sel.From == "checkout" {
		// A checkout's own fugaro.yaml must not name a brand-new, permanent
		// installation: it is somebody's file, perhaps a clone's.
		fromCheckout = sel.Name
	} else if name == "" {
		name = sel.Name
	}
	if name == "" && !o.configOnly {
		what := "the Fugaro project's name"
		if fromCheckout != "" {
			what = "this checkout's fugaro.yaml says " + pluginwire.Printable(fromCheckout) + ", which does not by itself name a new installation (the name is permanent): pass --name " + pluginwire.Printable(fromCheckout) + " to confirm it, or another name"
		}
		missing = append(missing, missingInput{"--name <name>", what})
	}
	if o.cloud.gcpProject == "" {
		missing = append(missing, missingInput{"--gcp-project <id>", "the GCP project ID"})
	}
	if o.cloud.region == "" {
		missing = append(missing, missingInput{"--region <region>", "the GCP region, such as " + defaultRegion})
	}
	return name, fromCheckout, missing, true
}

// canAsk is whether the run may show a prompt for an input: a terminal on
// stdin, and none of --non-interactive, --yes, --json (a script's flags) or
// --print-vars (stdout is the variables alone).
func (r *initRun) canAsk() bool {
	o := r.o
	return !o.nonInteractive && !o.yes && !o.asJSON && !o.printVars && stdinIsTerminal(r.cmd.InOrStdin())
}

// gatherInputs completes --name, --gcp-project and --region for a first run:
// it asks for what is missing in a terminal, and otherwise refuses, naming
// every missing flag (the GitHub App's ID too, when the checkout's repository
// needs it) in one error.
//
// It is the caller of the wizard's input phase and nothing else (W3.5): the
// questions are wizard_stage.go's stage, one run of the loop, which asks them
// all before the project config is loaded and before anything is applied. What
// is left here is the mapping from that run's outcome back to the one error
// this function has always returned, so that every mode — a person at a
// terminal, a pipe, --json, --yes, --non-interactive — prints exactly what it
// printed before the questions moved behind the loop.
//
// The wizard's project question is not one of the flags `missing` names. It is
// asked whenever the run can answer it, whether or not the other inputs came
// from flags, because no flag is required for it: a run that cannot ask takes
// its answer itself rather than demanding one (askProjectAnswer), so the
// question cannot be a row `missing` holds. With a project config nothing is
// asked at all.
func (r *initRun) gatherInputs(ctx context.Context) error {
	res, err := initflow.Run(ctx, wizardStages(ctx, r), r.wizardOptions())
	if err != nil {
		// The loop's own refusal, before a stage ran: --non-interactive's one
		// error naming every flag this run cannot answer for itself. It is the
		// error gatherInputs has always produced, from the same list
		// (wizardStage.Missing).
		return err
	}
	if f := res.Failed; f != nil {
		// A question's own refusal: the creation checks the answer runs, the
		// billing question's failures, a prompt that could not be read. The
		// stage's error is the one this run would have returned, with its
		// wording and its exit code (res.Cause, loop.go).
		if res.Cause == nil {
			return userErr("%s", f.Error)
		}
		return res.Cause
	}
	if len(res.Left) > 0 {
		// The run is waiting for a person: either it cannot ask at all, and the
		// Left names the flags that answer, or its input ended at a question
		// and the Left is that question. Both are the one-line user error they
		// have always been.
		return userErr("%v", res.Left[0].Text)
	}
	return nil
}

// installationName is the name of the installation that already exists in the
// GCP project, read from its runs bucket's marker; "" when there is none or it
// cannot be read (no credentials, no access, not Fugaro's): the name is then
// asked with the ordinary suggestion. Read-only. Tests replace it.
var installationName = func(ctx context.Context, gcpProject, region, runsBucket string) string {
	if gcpProject == "" || region == "" {
		return ""
	}
	lc, err := newProjectConfig("pending", gcpProject, region)
	if err != nil {
		return ""
	}
	if runsBucket != "" {
		lc.RunsBucket = runsBucket
	}
	return readInstallationName(ctx, lc)
}

// readInstallationName reads the marker of lc's runs bucket, accepted only
// from a bucket of lc's GCP project that carries Fugaro's label and names that
// project (infra.ReadProjectMarker), and returns the name it holds.
func readInstallationName(ctx context.Context, lc *localcfg.Config) string {
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return ""
	}
	num, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		return ""
	}
	m, err := infra.ReadProjectMarker(ctx, c, lc.RunsBucketName(), num)
	if err != nil || m == nil || m.GCPProject != lc.GCPProject || !config.ProjectNameRE.MatchString(m.Name) {
		return ""
	}
	return m.Name
}

// noAnswerError is a prompt that got no answer: the input ended, or three
// unusable answers were typed. It is not a refusal — the question is still
// open, and a rerun resumes at it — so the wizard's input phase reports it as
// the question it stopped at (wizardStage.answer, wizard_stage.go) rather than
// as a failure of the run.
//
// It wraps the user error it has always been, so a caller that keeps it as an
// error rather than reading it as a question (every prompt outside the phase)
// gets the same message, the same exit code and the same errors.As it got
// before: the type is added to what the value already was, not put in its
// place.
type noAnswerError struct {
	msg  string
	user error
}

func (e *noAnswerError) Error() string { return e.msg }
func (e *noAnswerError) Unwrap() error { return e.user }

// noAnswer builds one: the user error is the message's one definition, and msg
// is that error's own text.
func noAnswer(format string, args ...any) error {
	e := userErr(format, args...)
	return &noAnswerError{msg: e.Error(), user: e}
}

// prompt asks label with def as the suggestion and returns the validated
// answer. Three unusable answers, or the end of input, refuse the run. It is
// promptChoice with no second suggestion, and the questions that offer one
// answer are unchanged by it.
func (r *initRun) prompt(label, def string, valid func(string) error) (string, error) {
	return r.promptChoice(label, def, "", "", valid)
}

// promptChoice is prompt with a second suggestion: def is what Enter takes,
// and alt — offered after it, with the one line saying why, when it is neither
// empty nor def itself — is an answer a person may type instead. Only the GCP
// project's ID is asked this way (D4): a project to create, or the project the
// machine is already working in, are both reasonable answers and only the
// person knows which one they mean.
func (r *initRun) promptChoice(label, def, alt, why string, valid func(string) error) (string, error) {
	for range 3 {
		second := ""
		if alt != "" && alt != def {
			second = fmt.Sprintf(" (or %s: %s)", pluginwire.Printable(alt), why)
		}
		if def != "" {
			fmt.Fprintf(r.w, "%s [%s]%s: ", label, def, second)
		} else {
			fmt.Fprintf(r.w, "%s%s: ", label, second)
		}
		line, err := r.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", userErr("reading the answer: %v", err)
		}
		v := cmp.Or(strings.TrimSpace(line), def)
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
			return "", noAnswer("no answer for %q (the input ended)", label)
		}
		if v == "" {
			fmt.Fprintln(r.w, "  a value is needed")
			continue
		}
		if verr := valid(v); verr != nil {
			fmt.Fprintf(r.w, "  %v\n", verr)
			continue
		}
		return v, nil
	}
	return "", noAnswer("no usable answer for %q after 3 tries", label)
}
