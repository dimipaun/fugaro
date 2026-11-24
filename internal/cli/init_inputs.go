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
	"github.com/dimipaun/fugaro/internal/place"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The inputs a first run needs (design §3.1, "Inputs and defaults"): the
// Fugaro project's name, the GCP project's ID and the region. In a terminal
// each is asked once, with a suggestion Enter accepts; without one (or with
// --non-interactive, --yes or --json, which never prompt) the run is refused
// with every flag still missing named in one message. Once the local config
// exists nothing is asked.
//
// The GCP project's ID (D4): `fugaro setup`'s suggestion is a project to
// create, named after this one — `fugaro-<name>`, defaultGCPProject — and
// nothing else. That name is one to create, or to adopt where a project
// already carries it and holds nothing of Fugaro's (the owner's ruling of
// 2026-11-22). `fugaro update` on a new
// machine asks for an installation that already exists, so it also offers the
// project this machine is already working in (suggestGCPProject:
// GOOGLE_CLOUD_PROJECT or the quota project of the application default
// credentials) second, with the one line saying why. That suggestion never comes from gcloud's
// configured default project (the standing rule: a default project is how a
// command lands in the wrong project), so neither gcloud's properties file nor
// CLOUDSDK_CORE_PROJECT is read here.

// defaultRegion is the region the installation's jobs and the scheduler run
// in, and the region a first run gets when only the place is chosen: it is the
// US place's own answer, read off the mapping so the two cannot drift (D3086,
// LP1). The region is the one setting an installation stores; the place is
// derived from it (D3080), and a region typed in full (--region) settles both.
//
// It is not the Firestore database's location (C1): the mapping derives that
// one from the region (place.For, D3085), so it equals this value only where
// Firestore is offered in the region — an installation in us-west8 runs its
// jobs in this region and keeps its history in us-east5 — and a location is
// permanent, which is why the person confirms it at the terminal rather than
// this constant settling it.
var defaultRegion = place.DefaultRegion(place.US)

// regionRE is the shape of a Google Cloud region (us-east5, europe-west4).
var regionRE = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)

// parsePlace is the place an answer names: the two the product offers
// (D3081), case-insensitively, and nothing else. A region typed in full is not
// a place answer — it is the precise form of the same answer, which the run
// takes through --region (D3080).
func parsePlace(v string) (place.Place, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "us":
		return place.US, true
	case "eu":
		return place.EU, true
	}
	return place.None, false
}

// placeAnswer is the wizard's own reading of an answer to the place question
// (F2, D3080, D3081): us or eu, or a region typed in full — the place question
// and the region question are one question, so the person may answer either
// way. It returns the place the answer settles and the region it carries, the
// region being "" for a place (which the mapping's default region fills).
//
// The refusal is the one the region question has always printed, with the two
// places in front of it: a person who types something that is neither gets a
// sentence naming what the answer can be.
func placeAnswer(v string) (p place.Place, region string, err error) {
	if p, ok := parsePlace(v); ok {
		return p, "", nil
	}
	if regionRE.MatchString(v) {
		return place.Of(v), v, nil
	}
	return place.None, "", errors.New("answer us, eu, or a region (a region looks like us-east5 or europe-west4)")
}

// settleLocation turns --location into the region this run carries, and
// refuses the one thing two answers to one question can be: a disagreement
// (R2, D3080). The place itself is not stored anywhere — the region is the one
// stored setting, and the place is derived from it wherever it is read
// (place.Of) — so this runs before anything is asked or planned and leaves the
// run's own field for the region.
//
// A place with no region typed beside it takes the place's default region
// (place.DefaultRegion: us-east5, europe-west1, D3086). With both given they
// must agree, and a `--region` in neither place (London, Zürich) agrees with
// neither: it is a region typed in full, and --location is not how it is
// written.
func (o *initOptions) settleLocation() error {
	if o.location == "" {
		return nil
	}
	p, ok := parsePlace(o.location)
	if !ok {
		return userErr("--location %s is not a place: answer us or eu, or name the region itself with --region <region>", pluginwire.Printable(o.location))
	}
	if o.cloud.region == "" {
		o.cloud.region = place.DefaultRegion(p)
		return nil
	}
	if got := place.Of(o.cloud.region); got != p {
		return userErr("--location %s and --region %s disagree: --region %s is %s, and the place and the region are one answer (pass one of the two, or a region of the place --location names)",
			pluginwire.Printable(o.location), pluginwire.Printable(o.cloud.region), pluginwire.Printable(o.cloud.region), got)
	}
	return nil
}

// setupLocationRefusal is --location on a run that creates no installation
// (setupCreates): the place is the answer of the run that CREATES the
// installation, and a run that creates none has none to give.
//
// Left alone, the place is materialized into the place's default region
// (settleLocation) — a region the caller never typed — which the region-move
// guard then refuses in the region's own words, printing a --region the caller
// never passed as the fix for the --location they did; and where that default
// happens to be the installation's own region, the answer is dropped in
// silence. Refused by name instead, where --rigor is (LP4's review; setup.go's
// rigor refusals are the same refusal for the neighbouring first-run flag),
// with the region named as what such a run takes.
func setupLocationRefusal(answer string) error {
	if answer == "" {
		return nil
	}
	return userErr("--location (or the location member of an answers file, which is the same answer) is a first run's flag: it chooses the place fugaro setup creates the installation in, and this run creates none, so there is no place to choose (an installation's region is where it was created and cannot move) — the region itself is what a run names, with --region <region>; nothing was asked and nothing was written")
}

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
// default project. It is `fugaro update`'s **second** suggestion for the ID
// (D4) on a new machine, offered after the one below; `fugaro setup` never
// offers it, since the project it names exists.
func suggestGCPProject() string {
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); infra.ValidProjectID(v) {
		return v
	}
	return adcQuotaProject()
}

// defaultGCPProject is the ID a first run suggests for a new GCP project: the
// Fugaro project's own name, prefixed (reconciliation F3, D4, answered
// 2026-10-20 as recommended). The wizard's default is therefore a name to
// *create* — fugaro-<name>.
//
// The prefix is not negotiable and the name is sanitised and cut to what an ID
// holds (6 to 30 characters, infra.ValidProjectID), so Enter is always a legal
// answer; "" is the answer for a name that leaves nothing to build one from,
// and the question then suggests nothing. The buckets derive from the ID, so a
// project that takes this default runs in fugaro-runs-fugaro-<name>: the
// double prefix is accepted, and nothing about bucket naming moves (D4).
//
// It is never suggestGCPProject(): a project this machine already works in
// exists, and fugaro setup only creates.
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
// the selected config's name, else suggestName) — and, for fugaro update only
// (existing), when the credentials name a different project, that one second,
// with the line saying why.
//
// A checkout's fugaro.yaml naming a project does **not** become the ID's
// default, for the reason the name question has none from it either (a cloned
// repository's file can say anything): the name is then typed, and so is the
// ID built from it.
func projectIDSuggestions(ctx context.Context, name, fromCheckout string, existing bool) (def, alt, why string) {
	switch {
	case name != "":
		def = defaultGCPProject(name)
	case fromCheckout == "":
		def = defaultGCPProject(suggestName(ctx))
	}
	if c := suggestGCPProject(); existing && c != "" && c != def {
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
	if o.cloud.region == "" && o.location == "" {
		// One question, two ways to answer it (F2, R2): where the installation
		// lives (us or eu), or the region itself. A run that cannot ask is
		// refused with both, in that order, because either one is a whole
		// answer and neither is a default this run may take for a person.
		if o.setupCreates(true) {
			missing = append(missing, missingInput{"--location <us|eu>", "where the installation lives: us or eu"})
		}
		missing = append(missing, missingInput{"--region <region>", "the GCP region, such as " + defaultRegion})
	}
	return name, fromCheckout, missing, true
}

// selectedPending is whether the project config this run selects is the one a
// first run of fugaro setup wrote when it paused for billing
// (localcfg.Config.SetupPending).
func (r *initRun) selectedPending(ctx context.Context) bool {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return false
	}
	_, lc, err := selectNamed(r.o.cloud, co, true, r.o.name)
	return err == nil && lc != nil && lc.SetupPending != ""
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

// wizardAsk is one question as the wizard draws it: the label, what the
// question says about the shape of the answer, the answer Enter takes and the
// words that answer is offered in, and the second answer a person may type
// instead with the one line saying why.
//
// def and show are two fields because Enter's answer and the words the question
// offers it in are different strings in two places: the bake switch is drawn
// [Y/n] and takes yes, and the billing account is drawn by its name and taken
// by its number (askBilling). Everywhere else they are one string, and show is
// left empty.
type wizardAsk struct {
	label string
	hint  string
	def   string
	show  string
	alt   string
	why   string
	// empty is what the question says when Enter is pressed at it and it has no
	// suggestion to take, in place of the "a value is needed" every other
	// question says. Only the rigor question has none, and saying so here is
	// what lets it stop being said up front in a sentence above the four modes
	// (askRigor).
	empty string
}

// ask prints one question and reads its answer. It is the one way the wizard
// asks, and what it prints is one line: the label, what the answer looks like,
// and the suggestion Enter takes in brackets, with the colon the answer is
// typed after — the value entry beneath adds nothing, because a second line
// asking for the same value in the same words is one question asked twice
// (valueSpec.asked).
//
// Only the GCP project's ID has a second suggestion (alt, with the one line
// saying why): a project to create and the project this machine already works
// in are both reasonable answers, and only the person knows which they mean.
//
// The value itself comes through the one value-entry helper (readValue, D1540):
// at a terminal it is read, beside no terminal it is refused at once naming the
// flag or the variable that supplies it, and neither is a confirmation — so
// what is read here is a value and never a yes or a no. Enter takes the
// suggestion where there is one, and the entry has no default of its own, so a
// value that cannot be defaulted is refused rather than guessed (a run with no
// terminal gets the refusal, never the suggestion in the person's place), and
// an input that ended with nothing on the line is noAnswer rather than an empty
// line — the suggestion is what the person took with Enter, not what this run
// takes for them when they walked away. A value the caller's own rule refuses
// is asked again, question line and all, three attempts in all, and a question
// that runs out of attempts is noAnswer — a run that stopped at a question, not
// a refusal.
func (r *initRun) askValue(a wizardAsk, spec valueSpec, valid func(string) error) (string, error) {
	spec.asked = true
	for range 3 {
		line := r.qs.Ask(a.label, a.hint, cmp.Or(a.show, a.def))
		if a.alt != "" && a.alt != a.def {
			line += fmt.Sprintf(" (or %s: %s)", pluginwire.Printable(a.alt), a.why)
		}
		fmt.Fprint(r.w, line)
		b, err := readValueIn(r.ctx(), r.in, r.w, spec)
		if errors.Is(err, io.EOF) {
			// The input ended with nothing on the line: a question nobody
			// answered, which the suggestion is not taken in their place for —
			// an empty project ID is a project created with a guessed name, and
			// an empty billing answer links an account nobody chose.
			return "", noAnswer("no answer for %q (the input ended)", spec.what)
		}
		if err != nil {
			return "", err
		}
		v := cmp.Or(strings.TrimSpace(string(b)), a.def)
		if v == "" {
			fmt.Fprintln(r.w, "  "+cmp.Or(a.empty, "a value is needed"))
			continue
		}
		if verr := valid(v); verr != nil {
			fmt.Fprintf(r.w, "  %v\n", verr)
			continue
		}
		return v, nil
	}
	return "", noAnswer("no usable answer for %q after 3 tries", spec.what)
}
