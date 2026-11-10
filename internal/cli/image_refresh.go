package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro image refresh (design image-refresh.md): one repository onto this
// release's base image, in five steps, each with the confirmation it has
// today, or, with --yes, confirmed unattended (2026-10-08 owner decision,
// superseding design decision D2's "no --yes"; see askBuild).

type refreshOptions struct {
	cloud         cloudOptions
	repo          string
	workflows     []string
	imageSource   string
	expectDigests []string
	// yes is --yes (2026-10-08 owner decision, superseding D2 for this
	// command): it confirms every step, with no terminal needed, but never
	// in a coding agent's session.
	yes bool
	// ask is --ask (the owner's rule of 2026-11-04, and --yes's symmetric
	// half): in a project whose marks say it is managed, where this command
	// asks nothing — the owner's ruling of 2026-11-01 — the flag asks the
	// confirmations it has always asked instead: the typed project's name at
	// steps 2 and 3 and before every billable build. Unmanaged, it says so and
	// changes nothing; a run that could not answer refuses rather than
	// proceeding (managed_ask.go). It is refused beside --yes.
	ask bool
	// dir is the checkout to refresh ("" is the working directory).
	dir string
	// rerun, when set, is the whole line a stop names instead of again()
	// (fugaro upgrade's own, which already names the checkout). It is
	// printed as it is: the caller must quote every word with quoteWord
	// (and sanitise what it builds from), as again() does.
	rerun string
	// onPlan, when set, sees the plan once preflight is done, before
	// anything changes. It is READ-ONLY and gets a copy (refreshPlan.snapshot):
	// changing it changes nothing about the run (fugaro upgrade only needs
	// current()). It fires BEFORE installOptions, which can still fail at
	// step 1, so a caller must take the final state from the error runImageRefresh
	// returns, never from the hook alone.
	onPlan func(*refreshPlan)
	// managed is the gate this run is under: the value openRefreshManaged
	// returned, set by runImageRefresh and nil for every run the gate has
	// nothing to answer — one that is neither a coding agent's session nor a
	// person's own run at a terminal that would be asked. It travels with the options
	// rather than as another parameter because it is not a flag and because it
	// is the same value at every step of the run: the plan carries it to step 4
	// (refreshPlan.managed), and preflight uses the installation the gate
	// already read instead of selecting one a second time.
	managed *refreshManaged
	// in is the reader the calling run already reads stdin through, when a run
	// drives this refresh rather than the fugaro image refresh command: the
	// checkout stage of the converge (init_checkout_upgrade.go). A second
	// reader over the same stdin would lose whatever the first one has
	// buffered — an interactive run's typed-ahead input. Nil for the command,
	// which makes its own.
	in io.Reader
	// out is the writer the calling run already prints through, when one drives
	// this refresh: the checkout stage passes its own run's writer
	// (init_checkout_upgrade.go). The refresh is a run of its own here
	// (newInitRun), whose writer is the command's — a writer that draws nothing
	// and asks under a screen somebody else owns — so every line this refresh
	// prints would be written while the converge's spinner kept redrawing the
	// step in flight, over the line just printed and over the prompt under it
	// (G1), a billable build's confirmation included. Given the caller's
	// writer, each line takes that step down and puts it back, exactly as the
	// stage's own lines do. Nil for the command, whose own writer the refresh
	// uses.
	out io.Writer
}

// checkoutName is how a message names the checkout: "this checkout" when
// the working directory is the one, the directory itself (Printable) when
// o.dir sets another.
func (o refreshOptions) checkoutName(root string) string {
	if o.dir == "" {
		return "this checkout"
	}
	return "the checkout " + pluginwire.Printable(root)
}

// again is the command line that reruns this refresh.
func (o refreshOptions) again() string {
	args := []string{selfCommand(), "image", "refresh"}
	if o.repo != "" {
		args = append(args, "--repo", quoteWord(o.repo))
	}
	for _, w := range o.workflows {
		args = append(args, "--workflow", quoteWord(w))
	}
	for _, f := range []struct{ name, v string }{
		{"config", o.cloud.config}, {"project", o.cloud.project}, {"gcp-project", o.cloud.gcpProject},
		{"region", o.cloud.region}, {"image-source", o.imageSource},
	} {
		if f.v != "" {
			args = append(args, "--"+f.name, quoteWord(f.v))
		}
	}
	for _, d := range o.expectDigests {
		args = append(args, "--expect-digest", quoteWord(d))
	}
	if o.yes {
		args = append(args, "--yes")
	}
	return strings.Join(args, " ")
}

// checkoutDir is the checkout refresh reads: o.dir, or the working directory.
func (o refreshOptions) checkoutDir() string {
	if o.dir == "" {
		return "."
	}
	return o.dir
}

// rerunLine is what a stop tells the user to rerun.
func (o refreshOptions) rerunLine() string {
	if o.rerun != "" {
		return o.rerun
	}
	return o.again()
}

// refreshPlan is what preflight resolved, printed before anything changes.
type refreshPlan struct {
	root, repo, slug string
	lc               *localcfg.Config
	lcPath           string
	lcOld            []byte
	cfg              *config.Config
	workflows, kinds []string
	// want[kind] is the reference the kind's builds move to, and is set
	// only when that reference is already known offline: the entry the
	// config records when it is already this release's pin or a newer one.
	// A stale kind's pin is a digest through the remote repository, which
	// cannot be computed offline, so want[kind] is "" and step 2 resolves
	// and verifies it (design §5.4's B3).
	want   map[string]string
	atPin  map[string]bool // kinds whose recorded entry already is this release's pin (or a newer one)
	why    map[string]string
	builds []string
	ver    string // the release version this run targets
	yes    bool   // --yes: builds are confirmed without a prompt
	// managed is the gate this run is under, and nil for every run that is
	// not a coding agent's session in a managed project (openRefreshManaged):
	// each build is admitted through it, one at a time, and a nil one leaves
	// every build with the confirmation it has today.
	managed *refreshManaged
}

// snapshot is the copy onPlan sees: every slice and map is cloned, and the
// local config and fugaro.yaml are shallow copies (BaseImages cloned), so
// changing it cannot change what the run writes or builds. The nested maps
// of the config copies are shared: the hook is read-only.
func (p *refreshPlan) snapshot() *refreshPlan {
	c := *p
	c.lcOld = slices.Clone(p.lcOld)
	c.workflows, c.kinds, c.builds = slices.Clone(p.workflows), slices.Clone(p.kinds), slices.Clone(p.builds)
	c.want, c.why, c.atPin = maps.Clone(p.want), maps.Clone(p.why), maps.Clone(p.atPin)
	if p.lc != nil {
		lc := *p.lc
		lc.BaseImages = maps.Clone(p.lc.BaseImages)
		c.lc = &lc
	}
	if p.cfg != nil {
		cfg := *p.cfg
		c.cfg = &cfg
	}
	return &c
}

// refusedAsIs marks a refusal that is printed exactly as written, without
// the stop message: the development build (D1) and the custom base (D4)
// refusals say what to do themselves, the second ending with its own rerun.
type refusedAsIs struct{ err error }

func (e *refusedAsIs) Error() string { return e.err.Error() }
func (e *refusedAsIs) Unwrap() error { return e.err }

// noCheckout marks the refusal made outside any checkout: the stop message
// then says to rerun in a checkout of the repository, not "this checkout".
type noCheckout struct {
	err    error
	target string
}

func (e *noCheckout) Error() string { return e.err.Error() }
func (e *noCheckout) Unwrap() error { return e.err }

// refreshPreflight is step 1, read-only: the release, the checkout and its
// origin, the local config listing the repository, the selected workflows
// and their kinds, the custom-base stop (D4), and each workflow's verdict
// from its build record (the only cloud read).
func refreshPreflight(ctx context.Context, o refreshOptions, iopts *initOptions) (*refreshPlan, error) {
	ver := releaseVersion()
	if ver == "" {
		return nil, &refusedAsIs{userErr("fugaro image refresh pins this release's base images through the remote repository, and this is a development build (%s) with none published: use a release build, or build a base from a checkout and point at it with fugaro update --base-image KIND=<tag>", Version)}
	}
	// --repo, resolved before the checkout is looked for: where o.repo is a
	// path -- or is neither owner/name nor one -- the refusal below would
	// otherwise name the value as it was typed, and a value that reaches a
	// --repo may have been meant for another flag. o itself is left alone, so
	// the rerun line (again) repeats the flag as the caller wrote it.
	repoFlag := o.repo
	if repoFlag != "" {
		var err error
		if repoFlag, err = repoArg(ctx, o.cloud, repoFlag); err != nil {
			return nil, err
		}
	}
	if _, err := gitRead(ctx, o.checkoutDir(), "rev-parse", "--show-toplevel"); err != nil {
		target := "the repository"
		if repoFlag != "" {
			target = pluginwire.Printable(repoFlag)
		}
		dirName := "this directory"
		if o.dir != "" {
			dirName = "the directory " + pluginwire.Printable(o.dir)
		}
		return nil, &noCheckout{target: target, err: userErr("fugaro image refresh reads fugaro.yaml and the origin from the repository's checkout, and %s is not in one: run it in a checkout of %s", dirName, target)}
	}
	root, cfg, err := loadCheckoutConfigAt(ctx, o.checkoutDir())
	if err != nil {
		return nil, err
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return nil, err
	}
	if repoFlag != "" {
		want, err := task.CanonicalRepo(repoFlag)
		if err != nil {
			// repoArg answers with owner/name either way, so this is the shape
			// fold and nothing more; its error doesn't quote its input.
			return nil, userErr("--repo: %v", err)
		}
		if got, err := task.CanonicalRepo(repo); err != nil || got != want {
			return nil, userErr("--repo %s is not %s's origin (%s): run it in a checkout of %s", pluginwire.Printable(repoFlag), o.checkoutName(root), pluginwire.Printable(repo), pluginwire.Printable(repoFlag))
		}
	}
	// The gate's own read already selected this installation, and it is the
	// same selection this command acts on: it is used rather than made twice so
	// that the header, the --region override and the config file read are one
	// read, and so that no second selection can answer with another project
	// than the one the plan was judged against.
	var lc *localcfg.Config
	var lcPath string
	var old []byte
	if m := o.managed; m != nil {
		lc, lcPath, old = m.lc, m.lcPath, m.lcOld
	} else if lc, lcPath, old, err = loadRepoConfig(ctx, iopts, root); err != nil {
		return nil, err
	}
	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		if !c.OK {
			return nil, userErr("%s; %s", c.Problem, c.Fix)
		}
	}
	if oi, ok := readOrigin(ctx, root); !ok || !repoKnown(lc, oi) {
		return nil, userErr("%s is not one of project %s's repositories in the local config, and fugaro image refresh never onboards a repository: onboard it with fugaro update --repo in %s first", pluginwire.Printable(repo), lc.Name, o.checkoutName(root))
	}
	all := slices.Sorted(maps.Keys(cfg.Workflows))
	wfs := all
	if len(o.workflows) > 0 {
		for _, w := range o.workflows {
			if _, ok := cfg.Workflows[w]; !ok {
				return nil, userErr("fugaro.yaml has no workflow %q (it has: %s)", w, strings.Join(all, ", "))
			}
		}
		wfs = slices.Compact(slices.Sorted(slices.Values(o.workflows)))
	}
	set := map[string]bool{}
	for _, w := range wfs {
		// A checkout: clone workflow always runs the Fugaro base directly
		// (design generic-tool.md §2), whatever its (legacy) base: says.
		if cfg.Workflows[w].Checkout == config.CheckoutClone {
			set[config.BaseKind] = true
			continue
		}
		set[cfg.Workflows[w].BaseKind()] = true
	}
	kinds := slices.Sorted(maps.Keys(set))
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return nil, userErr("%v", err)
	}
	var custom []string
	for _, k := range kinds {
		if cur := lc.BaseImage(k); cur != "" && !isManaged(cur, host, k, baseOwner(lc)) {
			custom = append(custom, k)
		}
	}
	if len(custom) > 0 {
		return nil, &refusedAsIs{customBaseRefusal(lc, lcPath, custom, o.rerunLine())}
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return nil, userErr("%v", err)
	}
	p := &refreshPlan{root: root, repo: repo, slug: slug, lc: lc, lcPath: lcPath, lcOld: old, cfg: cfg,
		workflows: wfs, kinds: kinds, want: map[string]string{}, atPin: map[string]bool{}, why: map[string]string{}, ver: ver, yes: o.yes, managed: o.managed}
	for _, k := range kinds {
		var ref string
		if ref, p.atPin[k], err = refreshBase(lc, k, ver); err != nil {
			return nil, err
		}
		if p.atPin[k] {
			p.want[k] = ref
		}
	}
	for _, w := range wfs {
		// A clone workflow has no build record to compare: its job image
		// moves straight to base_images.base, in refreshBuilds, with no
		// Cloud Build submission.
		if cfg.Workflows[w].Checkout == config.CheckoutClone {
			p.why[w] = "runs the base image directly (checkout: clone): its job's image moves to base_images." + config.BaseKind + ", no build"
			continue
		}
		kind := cfg.Workflows[w].BaseKind()
		rec, err := readRefreshRecord(ctx, lc, slug, w)
		if err != nil {
			return nil, err
		}
		build, why := refreshVerdict(rec, p.want[kind], p.ver, host, kind, baseOwner(lc))
		p.why[w] = why
		if build {
			p.builds = append(p.builds, w)
		}
	}
	return p, nil
}

// print is the whole plan (design: kinds, the check job, the builds),
// before anything changes.
func (p *refreshPlan) print(w io.Writer) {
	fmt.Fprintf(w, "fugaro image refresh of %s (project %s, GCP project %s), workflows %s:\n", p.repo, p.lc.Name, p.lc.GCPProject, strings.Join(p.workflows, ", "))
	fmt.Fprintln(w, "  1. preflight: done (nothing changed)")
	for _, k := range p.kinds {
		if p.atPin[k] {
			fmt.Fprintf(w, "  2. base %s: %s (already this release's pin, or a newer one kept as it is: nothing is resolved and nothing is copied)\n", k, p.want[k])
			continue
		}
		fmt.Fprintf(w, "  2. base %s: this release's base image is resolved at its -pinned tag, its signature is verified, and base_images.%s is pinned to the digest the remote repository serves (nothing is copied; step 2 prints the reference and confirms it)\n", k, k)
	}
	fmt.Fprintln(w, "  3. the daily image check job: its image and FUGARO_CHECK_SPEC's base images follow the local config's, through the Cloud Run Admin API after its own confirmation (no Terraform; No changes when current)")
	how := "confirmed at your own terminal (nothing is typed)"
	if p.yes {
		how = "confirmed by --yes (no prompt)"
	}
	fmt.Fprintf(w, "  4. builds: %d of %d workflow(s), each billable, %s:\n", len(p.builds), len(p.workflows), how)
	for _, wf := range p.workflows {
		fmt.Fprintf(w, "     %s: %s\n", wf, p.why[wf])
	}
	fmt.Fprintln(w, "  5. then, if this checkout's fugaro.yaml lacks gcp_project: the fugaro update --anchor command to run next (fugaro image refresh never writes fugaro.yaml)")
}

// current: nothing for steps 2 and 4 to do, since every kind already records
// the base it moves to and no workflow needs a build. Step 3 still reads the
// check job.
func (p *refreshPlan) current() bool {
	if len(p.builds) > 0 {
		return false
	}
	for _, k := range p.kinds {
		if !p.atPin[k] {
			return false
		}
	}
	return true
}

func newImageRefreshCmd() *cobra.Command {
	var o refreshOptions
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Move a repository onto this release's base image: pin it, point the daily image check job at it, rebuild what is stale",
		Long: "Run it in the checkout of an onboarded repository, in your own terminal window\n" +
			"(or with --yes, unattended: see below).\n\n" +
			"It prints the whole plan first, then, each step with its own confirmation:\n" +
			"(2) resolves this release's base image of each selected workflow's kind at\n" +
			"its -pinned tag, verifies its cosign signature, reads the digest back\n" +
			"through the installation's remote repository and pins base_images.<kind> to\n" +
			"it, as fugaro update --base does (nothing is copied);\n" +
			"(3) points the repository's daily image check job at it (its image and\n" +
			"FUGARO_CHECK_SPEC's base images) through the Cloud Run Admin API, with no\n" +
			"Terraform; (4) rebuilds each selected workflow whose build record says it\n" +
			"was built from another base, or that has no build record or one that\n" +
			"cannot be read (billable: the project's name typed for each, or --yes);\n" +
			"(5) names fugaro update --anchor when fugaro.yaml lacks gcp_project.\n\n" +
			"A base_images entry that is neither this release's pin nor a release image\n" +
			"an earlier fugaro copied (a development or hand-pushed image) stops it\n" +
			"before anything changes, with or\n" +
			"without --yes: remove the entry from the local config, then rerun. It never\n" +
			"writes fugaro.yaml, never onboards a repository and never runs Terraform. A\n" +
			"coding agent's session is refused, --yes included (there is still no --json\n" +
			"or --plan-only to get around that). A run that stops says which steps\n" +
			"finished and what to rerun, --yes included when it was given; a rerun skips\n" +
			"what is done. Exit codes: 0 done, 1 a refusal or a confirmation not given,\n" +
			"2 a cloud failure.\n\n" +
			"Unattended use: pass --yes to run it from CI or a script, with no terminal.\n" +
			"It confirms every step, the base pin, the check-job update and EACH\n" +
			"billable build, with no cap on how many builds run: read the printed plan's\n" +
			"build count first. --yes does not cover a coding agent's session (refused,\n" +
			"same as without it) or a custom, dev or hand-pushed base image (still\n" +
			"refused with the one-line fix); there is still no --json or --plan-only.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageRefresh(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "owner/name (or a path inside its checkout) of the repository; it must be this checkout's origin (refresh reads fugaro.yaml from the checkout)")
	f.StringArrayVar(&o.workflows, "workflow", nil, "a workflow to refresh (repeatable; default: every workflow in fugaro.yaml)")
	f.StringVar(&o.imageSource, "image-source", "", "the registry and owner the release base images come from, as for the converge (default ghcr.io/dimipaun)")
	f.StringArrayVar(&o.expectDigests, "expect-digest", nil, "cross-check the digest of a base image's release signature, KIND=sha256:<hex>, as for the converge (repeatable)")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmations this run would otherwise skip (the project's name typed at each step, and before each billable build) instead of applying on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the questions back. Refused where nobody can answer them (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed; refused beside --yes")
	f.BoolVar(&o.yes, "yes", false, "confirm every step without asking (the base copy, the check-job update and each billable build, with no cap on how many run), and without a terminal: for CI or a script. The plan is still printed first. Never confirmed in a coding agent's session (CLAUDECODE and the like): nothing is applied there, --yes included. Without --yes nothing changes: a real terminal and the typed confirmations are still required, as always")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// refuseRefreshHere is decision D2, as reversed in part by the owner's
// 2026-10-08 decision and in part by the managed gate (M4, D1011): it still
// runs before anything else (git, the credentials, the network — the gate's
// own read is the one exception, and where it is made is openRefreshManaged's
// business). A coding agent's session applies nothing, --yes included, UNLESS the
// project is managed and the gate admitted every step it reaches: g is that
// gate, and it is nil for every other session, so the sentence below is
// exactly the one this command has always printed — a project that is not
// managed, a mark that could not be read and a session with no installation to
// read about all arrive here as nil. Without yes, anything but a real terminal
// still cannot type the confirmations every step needs, exactly as before;
// with yes, no terminal is required (it may run from CI or a script). There is
// still no --json or --plan-only.
func refuseRefreshHere(cmd *cobra.Command, yes bool, g *refreshManaged) error {
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		if g != nil {
			return nil
		}
		return userErr("fugaro image refresh applies cloud changes: %s", initflow.AgentRefusal(m))
	}
	if yes {
		return nil
	}
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("fugaro image refresh confirms every step with the person at the terminal who runs it — nothing is typed at a confirmation any more — so it needs a real terminal: %s", initflow.NoTerminalAdvice)
	}
	return nil
}

// refreshStepNames are step 1 to 4's names, for refreshStopped.
var refreshStepNames = []string{"preflight", "base", "check job", "builds"}

// refreshStopped is decision D12: the step's own error and exit code, which
// steps finished, and the line to rerun (every flag kept: o.rerunLine). A
// rerun line from fugaro upgrade already names the checkout, so no "in this
// checkout" follows it.
func refreshStopped(step int, done []string, o refreshOptions, err error) error {
	var as *refusedAsIs
	if errors.As(err, &as) {
		return as.err
	}
	where := " in " + o.checkoutName(o.dir)
	var nc *noCheckout
	switch {
	case o.rerun != "":
		where = ""
	case errors.As(err, &nc):
		where = " in a checkout of " + nc.target
	}
	finished := "none"
	if len(done) > 0 {
		finished = strings.Join(done, ", ")
	}
	return &ExitError{Code: ExitCode(err), Err: fmt.Errorf("%w; fugaro image refresh stopped at step %d (%s), steps finished: %s; once that is fixed, rerun %s%s: finished steps say No changes",
		err, step, refreshStepNames[step-1], finished, o.rerunLine(), where)}
}

// refreshSteps are steps 2 to 4; tests replace newRefreshSteps so the
// command's own order and stop-message logic can be tested without the
// cloud.
type refreshSteps struct {
	base     func(ctx context.Context) error
	reload   func(ctx context.Context) (*refreshTarget, error)
	checkJob func(ctx context.Context, t *refreshTarget) error
	builds   func(ctx context.Context, t *refreshTarget) error
}

var newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
	return refreshSteps{
		base:     func(ctx context.Context) error { return r.refreshBase(ctx, e, p.kinds) },
		reload:   func(ctx context.Context) (*refreshTarget, error) { return refreshReload(ctx, p) },
		checkJob: func(ctx context.Context, t *refreshTarget) error { return r.refreshCheckJob(ctx, t) },
		builds:   func(ctx context.Context, t *refreshTarget) error { return r.refreshBuilds(ctx, p, t) },
	}
}

// refreshAnchorNote is step 5 (D10): the next command when the checkout
// lacks gcp_project; refresh never writes it.
func refreshAnchorNote(ctx context.Context, w io.Writer, root, here string, lc *localcfg.Config) {
	if co, err := checkoutProject(ctx, root); err == nil && needsAnchorHint(ctx, co, lc) {
		fmt.Fprintf(w, "next: run fugaro update --anchor in %s to add the gcp_project line to fugaro.yaml (it checks the images first; fugaro image refresh never writes fugaro.yaml)\n", here)
	}
}

func runImageRefresh(cmd *cobra.Command, o refreshOptions) error {
	ctx := cmd.Context()
	// A leak first, before anything opens the cloud: GODEBUG is read before
	// main runs and cannot be unset in time, and the gate's own read below is a
	// cloud call carrying the operator's credentials, so a session that would
	// make Go print those requests is refused before any of them is made. It is
	// the order `fugaro managed` and `fugaro credentials serve` already use.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	// yes carries --yes to r.confirm/r.ask (steps 2 and 3) and to
	// refreshBuilds (step 4, which keeps its own rule: see askBuild). The steps
	// behind r.confirm/r.ask ask the gate beside it: in a managed project's own
	// terminal the marks are read and the typed name is replaced
	// (refreshManaged.asksNothing, and r.refresh below).
	iopts := &initOptions{cloud: o.cloud, imageSource: o.imageSource, expectDigests: o.expectDigests, yes: o.yes, ask: o.ask}
	o.managed = openRefreshManaged(ctx, cmd, o, iopts)
	// --ask answers for the marks, and it answers before the command's own
	// refusal: the flag's whole rule is that a managed project's confirmations
	// asked for where nobody can answer them are a refusal and never a
	// proceed, and that refusal has to be this flag's sentence rather than the
	// one a run without it would have read (managed_ask.go). Nothing is
	// applied by either.
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask, yes: o.yes}, o.managed != nil, askConditions(cmd, false, false)); err != nil {
		return err
	}
	if err := refuseRefreshHere(cmd, o.yes, o.managed); err != nil {
		return err
	}
	r := newInitRun(cmd, iopts)
	// The steps that are not builds ask the run whether the gate has taken
	// their confirmation (r.ask, refreshManaged.asksNothing): the read above is
	// the whole of the permission there, and a nil one is every other session.
	r.refresh = o.managed
	if o.in != nil {
		// The caller's reader, not a second one over the same stdin: see
		// refreshOptions.in.
		r.in = o.in
	}
	if o.out != nil {
		// The caller's writer, not the command's own: see refreshOptions.out.
		r.w = runWriter{r: r, out: o.out}
	}
	p, err := refreshPreflight(ctx, o, iopts)
	if err != nil {
		return refreshStopped(1, nil, o, err)
	}
	// The hook fires before installOptions, which can still fail at step 1:
	// a caller takes the final state from the returned error, not the hook.
	if o.onPlan != nil {
		o.onPlan(p.snapshot())
	}
	r.setProject(p.lc)
	spec, err := installOptions(iopts, p.lc)
	if err != nil {
		return refreshStopped(1, nil, o, err)
	}
	// The same local-config path goes to the engine, where step 2 (the
	// images stage) records the new base, and to step 3's reload, which
	// reads it back: p.lcPath, both times.
	e := &initEngine{r: r, lc: p.lc, spec: spec, path: p.lcPath, old: p.lcOld}
	p.print(r.w)
	s := newRefreshSteps(r, e, p)
	done := []string{"preflight"}
	fmt.Fprintln(r.w, "step 2, base:")
	if err := s.base(ctx); err != nil {
		return refreshStopped(2, done, o, err)
	}
	done = append(done, "base")
	fmt.Fprintln(r.w, "step 3, the daily image check job:")
	t, err := s.reload(ctx)
	if err == nil {
		err = s.checkJob(ctx, t)
	}
	if err != nil {
		return refreshStopped(3, done, o, err)
	}
	done = append(done, "check job")
	fmt.Fprintln(r.w, "step 4, builds:")
	if err := s.builds(ctx, t); err != nil {
		return refreshStopped(4, done, o, err)
	}
	if t.checkJobMissing {
		fmt.Fprintf(r.w, "note: step 3 found that the daily image check job was not found, though this repository's spec has one; run fugaro update --repo in %s to deploy it\n", o.checkoutName(p.root))
	}
	refreshAnchorNote(ctx, r.w, p.root, o.checkoutName(p.root), t.lc)
	fmt.Fprintf(r.w, "fugaro image refresh of %s is done\n", p.repo)
	return nil
}
