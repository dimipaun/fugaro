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

// refreshPlan is what preflight resolved, printed before anything changes.
type refreshPlan struct {
	root, repo, slug string
	lc               *localcfg.Config
	lcPath           string
	lcOld            []byte
	cfg              *config.Config
	workflows, kinds []string
	want             map[string]string
	why              map[string]string
	builds           []string
	kept             map[string]bool // kinds whose newer managed copy is kept, not copied
	yes              bool            // --yes: builds are confirmed without a prompt
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
		return nil, &refusedAsIs{userErr("fugaro image refresh copies this release's base images, and this is a development build (%s) with none published: use a release build, or build a base from a checkout and point at it with fugaro init --base-image KIND=<tag>", Version)}
	}
	if _, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel"); err != nil {
		target := "the repository"
		if o.repo != "" {
			target = o.repo
		}
		return nil, &noCheckout{target: target, err: userErr("fugaro image refresh reads fugaro.yaml and the origin from the repository's checkout, and this directory is not in one: run it in a checkout of %s", target)}
	}
	root, cfg, err := loadCheckoutConfigAt(ctx, ".")
	if err != nil {
		return nil, err
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return nil, err
	}
	if o.repo != "" {
		want, err := task.CanonicalRepo(o.repo)
		if err != nil {
			return nil, userErr("--repo %s: %v", o.repo, err)
		}
		if got, err := task.CanonicalRepo(repo); err != nil || got != want {
			return nil, userErr("--repo %s is not this checkout's origin (%s): run it in a checkout of %s", pluginwire.Printable(o.repo), pluginwire.Printable(repo), pluginwire.Printable(o.repo))
		}
	}
	lc, lcPath, old, err := loadRepoConfig(ctx, iopts, root)
	if err != nil {
		return nil, err
	}
	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		if !c.OK {
			return nil, userErr("%s; %s", c.Problem, c.Fix)
		}
	}
	if oi, ok := readOrigin(ctx, root); !ok || !repoKnown(lc, oi) {
		return nil, userErr("%s is not one of project %s's repositories in the local config, and fugaro image refresh never onboards a repository: onboard it with fugaro init in this checkout first", pluginwire.Printable(repo), lc.Name)
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
		set[cfg.Workflows[w].Base] = true
	}
	kinds := slices.Sorted(maps.Keys(set))
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return nil, userErr("%v", err)
	}
	var custom []string
	for _, k := range kinds {
		if cur := lc.BaseImage(k); cur != "" && !isManaged(cur, host, k) {
			custom = append(custom, k)
		}
	}
	if len(custom) > 0 {
		return nil, &refusedAsIs{customBaseRefusal(lc, lcPath, custom, o.again())}
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return nil, userErr("%v", err)
	}
	p := &refreshPlan{root: root, repo: repo, slug: slug, lc: lc, lcPath: lcPath, lcOld: old, cfg: cfg,
		workflows: wfs, kinds: kinds, want: map[string]string{}, why: map[string]string{}, kept: map[string]bool{}, yes: o.yes}
	for _, k := range kinds {
		if p.want[k], err = refreshBase(lc, k, ver); err != nil {
			return nil, err
		}
		if cur := lc.BaseImage(k); cur != "" && p.want[k] == cur && newer(curVersion(cur, host, k), ver) {
			p.kept[k] = true
		}
	}
	for _, w := range wfs {
		kind := cfg.Workflows[w].Base
		rec, err := readRefreshRecord(ctx, lc, slug, w)
		if err != nil {
			return nil, err
		}
		build, why := refreshVerdict(rec, p.want[kind], host, kind)
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
		if p.kept[k] {
			fmt.Fprintf(w, "  2. base %s: %s (a newer copy than this release's, kept as it is: nothing is copied)\n", k, p.want[k])
			continue
		}
		fmt.Fprintf(w, "  2. base %s: %s (copied into your registry if it lacks it, after its own confirmation)\n", k, p.want[k])
	}
	fmt.Fprintln(w, "  3. the daily image check job: its image and FUGARO_CHECK_SPEC's base images follow the local config's, through the Cloud Run Admin API after its own confirmation (no Terraform; No changes when current)")
	how := "confirmed by typing the project's name"
	if p.yes {
		how = "confirmed by --yes (no prompt)"
	}
	fmt.Fprintf(w, "  4. builds: %d of %d workflow(s), each billable, %s:\n", len(p.builds), len(p.workflows), how)
	for _, wf := range p.workflows {
		fmt.Fprintf(w, "     %s: %s\n", wf, p.why[wf])
	}
	fmt.Fprintln(w, "  5. then, if this checkout's fugaro.yaml lacks gcp_project: the fugaro init --anchor command to run next (fugaro image refresh never writes fugaro.yaml)")
}

func newImageRefreshCmd() *cobra.Command {
	var o refreshOptions
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Move a repository onto this release's base image: copy it, point the daily image check job at it, rebuild what is stale",
		Long: "Run it in the checkout of an onboarded repository, in your own terminal window\n" +
			"(or with --yes, unattended: see below).\n\n" +
			"It prints the whole plan first, then, each step with its own confirmation:\n" +
			"(2) copies this release's base image of each selected workflow's kind into\n" +
			"your registry if it lacks it and records it, as fugaro init --base does;\n" +
			"(3) points the repository's daily image check job at it (its image and\n" +
			"FUGARO_CHECK_SPEC's base images) through the Cloud Run Admin API, with no\n" +
			"Terraform; (4) rebuilds each selected workflow whose build record says it\n" +
			"was built from another base, or that has no build record or one that\n" +
			"cannot be read (billable: the project's name typed for each, or --yes);\n" +
			"(5) names fugaro init --anchor when fugaro.yaml lacks gcp_project.\n\n" +
			"A base_images entry that is not a release image fugaro init copied (a\n" +
			"development or hand-pushed image) stops it before anything changes, with or\n" +
			"without --yes: remove the entry from the local config, then rerun. It never\n" +
			"writes fugaro.yaml, never onboards a repository and never runs Terraform. A\n" +
			"coding agent's session is refused, --yes included (there is still no --json\n" +
			"or --plan-only to get around that). A run that stops says which steps\n" +
			"finished and what to rerun, --yes included when it was given; a rerun skips\n" +
			"what is done. Exit codes: 0 done, 1 a refusal or a confirmation not given,\n" +
			"2 a cloud failure.\n\n" +
			"Unattended use: pass --yes to run it from CI or a script, with no terminal.\n" +
			"It confirms every step, the base copy, the check-job update and EACH\n" +
			"billable build, with no cap on how many builds run: read the printed plan's\n" +
			"build count first. --yes does not cover a coding agent's session (refused,\n" +
			"same as without it) or a custom, dev or hand-pushed base image (still\n" +
			"refused with the one-line fix); there is still no --json or --plan-only.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageRefresh(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "owner/name of the repository; it must be this checkout's origin (refresh reads fugaro.yaml from the checkout)")
	f.StringArrayVar(&o.workflows, "workflow", nil, "a workflow to refresh (repeatable; default: every workflow in fugaro.yaml)")
	f.StringVar(&o.imageSource, "image-source", "", "the registry and owner the release base images are copied from, as for fugaro init (default ghcr.io/dimipaun)")
	f.StringArrayVar(&o.expectDigests, "expect-digest", nil, "pin the digest of a base image copied, KIND=sha256:<hex>, as for fugaro init (repeatable)")
	f.BoolVar(&o.yes, "yes", false, "confirm every step without asking (the base copy, the check-job update and each billable build, with no cap on how many run), and without a terminal: for CI or a script. The plan is still printed first. Never confirmed in a coding agent's session (CLAUDECODE and the like): nothing is applied there, --yes included. Without --yes nothing changes: a real terminal and the typed confirmations are still required, as always")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// refuseRefreshHere is decision D2, as reversed in part by the owner's
// 2026-10-08 decision: it still runs before anything else (the local
// config, git, credentials, the network). A coding agent's session applies
// nothing, --yes included: that check is never bypassed. Without yes,
// anything but a real terminal still cannot type the confirmations every
// step needs, exactly as before; with yes, no terminal is required (it may
// run from CI or a script). There is still no --json or --plan-only.
func refuseRefreshHere(cmd *cobra.Command, yes bool) error {
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("fugaro image refresh applies cloud changes: %s", initflow.AgentRefusal(m))
	}
	if yes {
		return nil
	}
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("fugaro image refresh asks for the project's name at each step and before each billable build, so it needs a real terminal: %s", initflow.NoTerminalAdvice)
	}
	return nil
}

// refreshStepNames are step 1 to 4's names, for refreshStopped.
var refreshStepNames = []string{"preflight", "base", "check job", "builds"}

// refreshStopped is decision D12: the step's own error and exit code, which
// steps finished, and the line to rerun (every flag kept, through again).
func refreshStopped(step int, done []string, again string, err error) error {
	var as *refusedAsIs
	if errors.As(err, &as) {
		return as.err
	}
	where := "in this checkout"
	var nc *noCheckout
	if errors.As(err, &nc) {
		where = "in a checkout of " + nc.target
	}
	finished := "none"
	if len(done) > 0 {
		finished = strings.Join(done, ", ")
	}
	return &ExitError{Code: ExitCode(err), Err: fmt.Errorf("%w; fugaro image refresh stopped at step %d (%s), steps finished: %s; once that is fixed, rerun %s %s: finished steps say No changes",
		err, step, refreshStepNames[step-1], finished, again, where)}
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
func refreshAnchorNote(ctx context.Context, w io.Writer, root string, lc *localcfg.Config) {
	if co, err := checkoutProject(ctx, root); err == nil && needsAnchorHint(ctx, co, lc) {
		fmt.Fprintln(w, "next: run fugaro init --anchor in this checkout to add the gcp_project line to fugaro.yaml (it checks the images first; fugaro image refresh never writes fugaro.yaml)")
	}
}

func runImageRefresh(cmd *cobra.Command, o refreshOptions) error {
	ctx := cmd.Context()
	if err := refuseRefreshHere(cmd, o.yes); err != nil {
		return err
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	again := o.again()
	// yes carries --yes to r.confirm/r.ask (steps 2 and 3) and to
	// refreshBuilds (step 4, which keeps its own rule: see askBuild).
	iopts := &initOptions{cloud: o.cloud, imageSource: o.imageSource, expectDigests: o.expectDigests, yes: o.yes}
	r := newInitRun(cmd, iopts)
	p, err := refreshPreflight(ctx, o, iopts)
	if err != nil {
		return refreshStopped(1, nil, again, err)
	}
	r.setProject(p.lc)
	spec, err := installOptions(iopts, p.lc)
	if err != nil {
		return refreshStopped(1, nil, again, err)
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
		return refreshStopped(2, done, again, err)
	}
	done = append(done, "base")
	fmt.Fprintln(r.w, "step 3, the daily image check job:")
	t, err := s.reload(ctx)
	if err == nil {
		err = s.checkJob(ctx, t)
	}
	if err != nil {
		return refreshStopped(3, done, again, err)
	}
	done = append(done, "check job")
	fmt.Fprintln(r.w, "step 4, builds:")
	if err := s.builds(ctx, t); err != nil {
		return refreshStopped(4, done, again, err)
	}
	if t.checkJobMissing {
		fmt.Fprintln(r.w, "note: step 3 found that the daily image check job was not found, though this repository's spec has one; run fugaro init --repo in this checkout to deploy it")
	}
	refreshAnchorNote(ctx, r.w, p.root, t.lc)
	fmt.Fprintf(r.w, "fugaro image refresh of %s is done\n", p.repo)
	return nil
}
