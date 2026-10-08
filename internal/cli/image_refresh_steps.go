package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// refreshTarget is the repository as the steps after the base step see it:
// the local config re-read (the base step recorded base_images), the
// checkout's fugaro.yaml, and the repository's spec.
type refreshTarget struct {
	lc   *localcfg.Config
	cfg  *config.Config
	spec infra.RepoSpec
}

// refreshBase is step 2: the images stage alone, for kinds (D5, D6), with
// its own confirmation and its own "No changes". The stage's own rules keep
// a custom or hand-pushed base untouched (it is left alone, never copied
// over); preflight's customBaseRefusal stops the run earlier for the same
// reason, so this is belt and suspenders, not the only guard.
func (r *initRun) refreshBase(ctx context.Context, e *initEngine, kinds []string) error {
	s := newImagesStage(e)
	s.only = kinds
	e.images = s
	st, err := s.Check(ctx)
	if err != nil {
		return err
	}
	if st.State == initflow.Skipped {
		fmt.Fprintf(r.w, "  %s\n", st.Detail)
		return nil
	}
	out, err := s.Apply(ctx, initflow.Env{Interactive: true})
	if err != nil {
		return err
	}
	fmt.Fprintf(r.w, "  base: %s\n", out.Detail)
	return nil
}

// refreshReload re-reads the local config the base step wrote and computes
// the repository's spec, as fugaro image build does without the
// installation's outputs (a kind the local config has no base for stands in
// with the release's, for the spec only).
func refreshReload(ctx context.Context, p *refreshPlan) (*refreshTarget, error) {
	lc, err := localcfg.Load(p.lcPath)
	if err != nil {
		return nil, userErr("%s: %v", p.lcPath, err)
	}
	repoURL, err := checkoutURL(ctx, p.root)
	if err != nil {
		return nil, err
	}
	specLC := *lc
	specLC.BaseImages = maps.Clone(lc.BaseImages)
	if specLC.BaseImages == nil {
		specLC.BaseImages = map[string]string{}
	}
	for _, w := range p.cfg.Workflows {
		if specLC.BaseImages[w.Base] == "" {
			if specLC.BaseImages[w.Base], err = image.BaseRef(w.Base, Version); err != nil {
				return nil, userErr("%v", err)
			}
		}
	}
	spec, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: p.repo, Cfg: p.cfg, RepoURL: repoURL})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return &refreshTarget{lc: lc, cfg: p.cfg, spec: spec}, nil
}

// refreshCheckJob is step 3 (D7): the check job's image and
// FUGARO_CHECK_SPEC's base images moved to the local config's, directly,
// after the typed name; no Terraform. PlanCheckJob's own owner check (its
// labels and its spec's repo) refuses a job that is not this repository's
// (ErrCheckJobShape), and a read that fails for any other reason is a
// remote error, never treated as "no job" or "current".
func (r *initRun) refreshCheckJob(ctx context.Context, t *refreshTarget) error {
	c, err := newInitClients(ctx, t.lc)
	if err != nil {
		return err
	}
	u, err := infra.PlanCheckJob(ctx, c, t.lc.GCPProject, t.lc.Region, gcp.CheckJobName(t.spec.Slug),
		infra.CheckJobOwner{Repo: t.spec.Name, Label: t.spec.Label}, t.lc.BaseImages)
	switch {
	case errors.Is(err, infra.ErrCheckJobShape):
		return userErr("%v", err)
	case err != nil:
		return remote(err)
	case u == nil:
		fmt.Fprintln(r.w, "  no daily image check job (every workflow has rebuild.check: off, or init --repo has not deployed it yet): nothing to update")
		return nil
	case !u.Changes():
		fmt.Fprintf(r.w, "  No changes: the daily image check job already runs from %s\n", pluginwire.Printable(u.NewImage()))
		return nil
	}
	fmt.Fprintf(r.w, "  %s: image %s -> %s\n  %s: %s\n    -> %s\n", pluginwire.Printable(u.Job()), pluginwire.Printable(u.OldImage()), pluginwire.Printable(u.NewImage()),
		infra.CheckSpecEnv, pluginwire.Printable(u.OldSpec()), pluginwire.Printable(u.NewSpec()))
	if err := r.confirm(fmt.Sprintf("updates the daily image check job %s in place through the Cloud Run Admin API, as you: its image and %s's base images, exactly as listed above; nothing else in the job changes, and the next fugaro init --repo from this local config plans no change to it",
		pluginwire.Printable(u.Job()), infra.CheckSpecEnv), "the daily image check job was not updated"); err != nil {
		return err
	}
	if err := infra.ApplyCheckJob(ctx, c, u); err != nil {
		return remote(err)
	}
	fmt.Fprintln(r.w, "  updated the daily image check job")
	return nil
}

// refreshBuilds is step 4 (D8, D9): each selected workflow whose image is
// not built from its current base is rebuilt, after its own typed
// confirmation, from the base the local config now records. A declined or
// failed build stops the loop: the builds after it are never submitted.
func (r *initRun) refreshBuilds(ctx context.Context, p *refreshPlan, t *refreshTarget) error {
	host, err := infra.RegistryHost(t.lc)
	if err != nil {
		return userErr("%v", err)
	}
	var todo, unrecorded []string
	for _, wf := range p.workflows {
		kind := t.cfg.Workflows[wf].Base
		want := t.lc.BaseImage(kind)
		if want == "" {
			return userErr("the local config has no base image for kind %s after the base step", kind)
		}
		rec, err := readRefreshRecord(ctx, t.lc, t.spec.Slug, wf)
		if err != nil {
			return err
		}
		build, why := refreshVerdict(rec, want, host, kind)
		fmt.Fprintf(r.w, "  %s: %s\n", wf, why)
		if build {
			todo = append(todo, wf)
		}
		if rec == nil {
			unrecorded = append(unrecorded, wf)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(r.w, "  No changes: every selected image is built from its current base")
		return nil
	}
	b, err := newCloudBuilder(ctx, t.lc)
	if err != nil {
		return remote(err)
	}
	for _, wf := range todo {
		ok, reachable, err := r.askTyped(cloudBuildBanner(t.spec.Name, wf, t.lc.Build.MachineType, t.spec.BuildServiceAccountEmail, t.spec.RegistryPath))
		if err != nil {
			return err
		}
		if !reachable || !ok {
			return userErr("the build of %s/%s was not confirmed (the project's name was not typed), so it and the builds after it were not submitted", t.spec.Name, wf)
		}
		if _, err := r.submitAndWait(ctx, b, t.lc, t.cfg, t.spec, wf, t.lc.BaseImage(t.cfg.Workflows[wf].Base)); err != nil {
			return err
		}
	}
	for _, wf := range unrecorded {
		fmt.Fprintf(r.w, "note: %s had no build record; if its job is not deployed yet, fugaro init --repo in this checkout deploys it\n", wf)
	}
	return nil
}
