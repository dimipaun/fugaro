package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro image refresh (design image-refresh.md): one repository onto this
// release's base image, in five steps, each with the confirmation it has
// today.

type refreshOptions struct {
	cloud         cloudOptions
	repo          string
	workflows     []string
	imageSource   string
	expectDigests []string
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
	if o.cloud.project != "" {
		args = append(args, "--project", quoteWord(o.cloud.project))
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
}

// refreshPreflight is step 1, read-only: the release, the checkout and its
// origin, the local config listing the repository, the selected workflows
// and their kinds, the custom-base stop (D4), and each workflow's verdict
// from its build record (the only cloud read).
func refreshPreflight(ctx context.Context, o refreshOptions, iopts *initOptions) (*refreshPlan, error) {
	ver := releaseVersion()
	if ver == "" {
		return nil, userErr("fugaro image refresh copies this release's base images, and this is a development build (%s) with none published: use a release build, or build a base from a checkout and point at it with fugaro init --base-image KIND=<tag>", Version)
	}
	if _, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel"); err != nil {
		target := "the repository"
		if o.repo != "" {
			target = o.repo
		}
		return nil, userErr("fugaro image refresh reads fugaro.yaml and the origin from the repository's checkout, and this directory is not in one: run it in a checkout of %s", target)
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
			return nil, userErr("--repo %s is not this checkout's origin (%s): run it in a checkout of %s", o.repo, repo, o.repo)
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
		return nil, customBaseRefusal(lc, lcPath, custom, o.again())
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return nil, userErr("%v", err)
	}
	p := &refreshPlan{root: root, repo: repo, slug: slug, lc: lc, lcPath: lcPath, lcOld: old, cfg: cfg,
		workflows: wfs, kinds: kinds, want: map[string]string{}, why: map[string]string{}}
	for _, k := range kinds {
		if p.want[k], err = refreshBase(lc, k, ver); err != nil {
			return nil, err
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
		fmt.Fprintf(w, "  2. base %s: %s (copied into your registry if it lacks it, after its own confirmation)\n", k, p.want[k])
	}
	fmt.Fprintln(w, "  3. the daily image check job: its image and FUGARO_CHECK_SPEC's base images follow the local config's, through the Cloud Run Admin API after its own confirmation (no Terraform; No changes when current)")
	fmt.Fprintf(w, "  4. builds: %d of %d workflow(s), each billable and confirmed by typing the project's name:\n", len(p.builds), len(p.workflows))
	for _, wf := range p.workflows {
		fmt.Fprintf(w, "     %s: %s\n", wf, p.why[wf])
	}
	fmt.Fprintln(w, "  5. then, if this checkout's fugaro.yaml lacks gcp_project: the fugaro init --anchor command to run next (fugaro image refresh never writes fugaro.yaml)")
}
