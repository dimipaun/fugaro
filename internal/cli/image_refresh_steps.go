package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

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
	lc    *localcfg.Config
	cfg   *config.Config
	spec  infra.RepoSpec
	kinds []string // the selected kinds (the plan's), for telling the others apart in the check job's diff

	// checkJobMissing is set when step 3 finds no daily image check job for
	// a repository whose spec has one (a 404 that should not be one): the
	// final summary repeats it, so it is not just a line that scrolled past.
	checkJobMissing bool
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
	out, err := s.Apply(ctx, initflow.Env{Yes: r.o.yes, Interactive: true})
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
	// Re-resolved strictly through the shared helper, against this
	// freshly reloaded lc, rather than reused from p.cfg
	// (refreshPreflight's loadCheckoutConfigAt, read lc==nil, flag-blind,
	// and lenient): resolveForBuild's own doc comment has why.
	rf, err := resolveForBuildFile(ctx, p.root, lc)
	if err != nil {
		return nil, err
	}
	cfg := rf.Cfg
	repoURL, err := checkoutURL(ctx, p.root)
	if err != nil {
		return nil, err
	}
	specLC := *lc
	specLC.BaseImages = maps.Clone(lc.BaseImages)
	if specLC.BaseImages == nil {
		specLC.BaseImages = map[string]string{}
	}
	for _, w := range cfg.Workflows {
		kind := w.BaseKind()
		if specLC.BaseImages[kind] == "" {
			if specLC.BaseImages[kind], err = image.BaseRef(Version); err != nil {
				return nil, userErr("%v", err)
			}
		}
	}
	spec, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: p.repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(rf), RepoURL: repoURL})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return &refreshTarget{lc: lc, cfg: cfg, spec: spec, kinds: p.kinds}, nil
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
	case u == nil && t.spec.Check != nil:
		t.checkJobMissing = true
		fmt.Fprintf(r.w, "  the daily image check job %s was not found, though this repository's spec has one: run fugaro update --repo in the checkout to deploy it (nothing was updated)\n", pluginwire.Printable(gcp.CheckJobName(t.spec.Slug)))
		return nil
	case u == nil:
		fmt.Fprintln(r.w, "  no daily image check job (every workflow has rebuild.check: off): nothing to update")
		return nil
	case !u.Changes():
		fmt.Fprintf(r.w, "  No changes: the daily image check job already runs from %s\n", pluginwire.Printable(u.NewImage()))
		return nil
	}
	diff, err := checkSpecDiff(u, t.kinds)
	if err != nil {
		return userErr("%v", err)
	}
	fmt.Fprintf(r.w, "  %s: image %s -> %s\n  %s base_images:\n%s  no other field of %s or the job changes; its execution tokens (startExecutionToken, runExecutionToken) are not sent back\n", pluginwire.Printable(u.Job()),
		pluginwire.Printable(u.OldImage()), pluginwire.Printable(u.NewImage()), infra.CheckSpecEnv, diff, infra.CheckSpecEnv)
	if err := r.confirm(fmt.Sprintf("updates the daily image check job %s in place through the Cloud Run Admin API, as you: its image and %s's base images, as listed above; no other field of %s or the job changes; its execution tokens (startExecutionToken, runExecutionToken) are not sent back, and the next fugaro update --repo from this local config is designed to plan no change to it (not yet verified against the real API: see docs/gcp-live-checklist.md Check 30)",
		pluginwire.Printable(u.Job()), infra.CheckSpecEnv, infra.CheckSpecEnv), "the daily image check job was not updated"); err != nil {
		return err
	}
	if err := infra.ApplyCheckJob(ctx, c, u); err != nil {
		return remote(err)
	}
	fmt.Fprintln(r.w, "  updated the daily image check job")
	return nil
}

// checkSpecDiff lists, per kind, how the update moves FUGARO_CHECK_SPEC's
// base_images ("kind: old -> new", each ref through Printable): the whole
// spec is never printed, because Printable cuts it and base_images is its
// last field. A kind outside the selection that RewriteCheckSpec also moves
// to the local config's value is listed and flagged; a kind that does not
// move is not listed.
func checkSpecDiff(u *infra.CheckJobUpdate, selected []string) (string, error) {
	var old, cur infra.CheckJobSpec
	if err := json.Unmarshal([]byte(u.OldSpec()), &old); err != nil {
		return "", fmt.Errorf("%s: %v", infra.CheckSpecEnv, err)
	}
	if err := json.Unmarshal([]byte(u.NewSpec()), &cur); err != nil {
		return "", fmt.Errorf("%s: %v", infra.CheckSpecEnv, err)
	}
	kinds := map[string]bool{}
	for k := range old.BaseImages {
		kinds[k] = true
	}
	for k := range cur.BaseImages {
		kinds[k] = true
	}
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(kinds)) {
		if old.BaseImages[k] == cur.BaseImages[k] {
			continue
		}
		flag := ""
		if !slices.Contains(selected, k) {
			flag = " (not selected: also moved to the local config's base)"
		}
		fmt.Fprintf(&b, "    %s: %s -> %s%s\n", pluginwire.Printable(k), pluginwire.Printable(old.BaseImages[k]), pluginwire.Printable(cur.BaseImages[k]), flag)
	}
	return b.String(), nil
}

// withNote adds note to err, keeping its exit code.
func withNote(err error, note string) error {
	code := ExitCode(err)
	var ee *ExitError
	if errors.As(err, &ee) {
		err = ee.Err
	}
	return &ExitError{Code: code, Err: fmt.Errorf("%w; %s", err, note)}
}

// builtSoFar adds the workflows already built to err, keeping its exit code.
func builtSoFar(err error, built []string) error {
	list := "none"
	if len(built) > 0 {
		list = strings.Join(built, ", ")
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.Code, Err: fmt.Errorf("%w; built before this: %s", ee.Err, list)}
	}
	return fmt.Errorf("%w; built before this: %s", err, list)
}

// askBuild is step 4's confirmation of one billable build: the project's
// name typed at a real terminal, or, with --yes, confirmed without one
// (2026-10-08 owner decision: --yes now covers every build here, with no
// cap, superseding D2's "no --yes" for this command only; askTyped's own
// Typed class, used elsewhere, still never takes --yes). A coding agent's
// session is refused here too, before the --yes shortcut, even though
// refuseRefreshHere already refused before any step ran (defence in depth).
func (r *initRun) askBuild(what string) (confirmed, reachable bool, err error) {
	if m := agentMarker(os.Getenv); m != "" {
		return false, false, &initflow.AgentError{Marker: m}
	}
	if r.o.yes {
		fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", r.projectName, r.gcpProject, what)
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return true, true, nil
	}
	return r.askTyped(what)
}

// refreshCloneJobs is part of step 4: a checkout: clone workflow has no
// derived image to rebuild, so its job's container image moves to the
// local config's base_images.base directly, through the Cloud Run Admin
// API (as refreshCheckJob moves the daily check job's), after its own
// confirmation; no Cloud Build is ever submitted for it.
//
// Before any of that (review finding, 0.7.0 safety fix round 2): while the
// gate was shut no fugaro release could run a checkout: clone workflow's
// job at all, so this refused to move it onto base_images.base in the first
// place, before the cloud calls below — the same gate planRepo applied
// before deploying one. cloneShipped has been true since 0.9.0 shipped the
// flip, so the call below is a no-op on every plan today and stays for the
// flip-back the constant's comment describes. refuseClonesNotShipped's error is
// cloneErr to refreshBuilds' caller, so it never stops the baked rebuild
// offers the rest of this step makes for the repository's other workflows
// (same as a declined clone confirmation already does not, see
// TestRefreshDeclinedCloneJobDoesNotBlockBakedRebuilds).
func (r *initRun) refreshCloneJobs(ctx context.Context, p *refreshPlan, t *refreshTarget) error {
	var clones []string
	for _, wf := range p.workflows {
		if t.cfg.Workflows[wf].Checkout == config.CheckoutClone {
			clones = append(clones, wf)
		}
	}
	if len(clones) == 0 {
		return nil
	}
	if err := refuseClonesNotShipped(t.spec.Name, "fugaro image refresh", "move", clones); err != nil {
		return err
	}
	newImage := t.lc.BaseImage(config.BaseKind)
	if newImage == "" {
		return userErr("the local config has no base_images.%s after the base step, which every checkout: clone workflow's job runs (%s)", config.BaseKind, strings.Join(clones, ", "))
	}
	c, err := newInitClients(ctx, t.lc)
	if err != nil {
		return err
	}
	owner := infra.CheckJobOwner{Repo: t.spec.Name, Label: t.spec.Label}
	// Every job of a clone workflow is moved, not only the default
	// account's: with named Claude accounts (gcp.WorkflowVariant, design
	// docs/design/named-oauth-tokens.md §4) one fugaro.yaml workflow has a
	// job per account, each labelled with its own variant key and each
	// pinned to base_images.base itself (unlike a baked workflow's jobs,
	// which share the derived image's :latest tag and so move together on
	// one rebuild).
	var jobs []string
	for _, wf := range clones {
		for _, key := range slices.Sorted(maps.Keys(t.spec.Workflows)) {
			if key == wf || t.spec.Workflows[key].Name == wf {
				jobs = append(jobs, key)
			}
		}
	}
	for _, wf := range jobs {
		ws := t.spec.Workflows[wf]
		u, err := infra.PlanWorkflowJob(ctx, c, t.lc.GCPProject, t.lc.Region, ws.Job, wf, newImage, owner)
		switch {
		case errors.Is(err, infra.ErrCheckJobShape):
			return userErr("%v", err)
		case err != nil:
			return remote(err)
		case u == nil:
			fmt.Fprintf(r.w, "  %s: the job %s was not found: run fugaro update --repo in the checkout to deploy it (nothing was updated)\n", wf, pluginwire.Printable(ws.Job))
			continue
		case !u.Changes():
			fmt.Fprintf(r.w, "  %s: No changes: the job already runs from %s\n", wf, pluginwire.Printable(u.NewImage()))
			continue
		}
		fmt.Fprintf(r.w, "  %s: image %s -> %s\n", wf, pluginwire.Printable(u.OldImage()), pluginwire.Printable(u.NewImage()))
		if err := r.confirm(fmt.Sprintf("updates the checkout: clone workflow %s's job %s in place through the Cloud Run Admin API, as you: its container image moves to %s; no Cloud Build runs, and no other field of the job changes",
			wf, pluginwire.Printable(u.Job()), pluginwire.Printable(u.NewImage())), "the job was not updated"); err != nil {
			return err
		}
		if err := infra.ApplyWorkflowJob(ctx, c, u); err != nil {
			return remote(err)
		}
		fmt.Fprintf(r.w, "  updated %s's job\n", wf)
	}
	return nil
}

// refreshBuilds is step 4 (D8, D9): each selected workflow whose image is
// not built from its current base is rebuilt, after its own confirmation
// (askBuild), from the base the local config now records. A declined or
// failed build stops the loop: the builds after it are never submitted. A
// checkout: clone workflow has no build to decide (refreshCloneJobs moves
// its job's image directly instead) and is skipped here.
//
// refreshCloneJobs runs first, but its outcome (including a declined
// confirmation) never stops the baked rebuild offers below: moving a clone
// workflow's job image and rebuilding a baked workflow's are independent
// per-workflow actions, bundled into this one step only because neither
// touches Terraform (review fix: a decline on the clone side used to short-
// circuit the whole step, so a mixed repository's baked rebuilds were never
// even offered). cloneErr is returned only once the baked section below has
// had its own chance to run; a baked-side error, which already carries its
// own "built so far" detail, takes precedence over it.
func (r *initRun) refreshBuilds(ctx context.Context, p *refreshPlan, t *refreshTarget) error {
	cloneErr := r.refreshCloneJobs(ctx, p, t)
	host, err := infra.RegistryHost(t.lc)
	if err != nil {
		return userErr("%v", err)
	}
	var todo, unrecorded []string
	for _, wf := range p.workflows {
		if t.cfg.Workflows[wf].Checkout == config.CheckoutClone {
			continue
		}
		kind := t.cfg.Workflows[wf].BaseKind()
		want := t.lc.BaseImage(kind)
		if want == "" {
			return userErr("the local config has no base image for kind %s after the base step", kind)
		}
		rec, err := readRefreshRecord(ctx, t.lc, t.spec.Slug, wf)
		if err != nil {
			return err
		}
		build, why := refreshVerdict(rec, want, p.ver, host, kind, baseOwner(t.lc))
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
		return cloneErr
	}
	b, err := newCloudBuilder(ctx, t.lc)
	if err != nil {
		return remote(err)
	}
	notes := func() {
		for _, wf := range unrecorded {
			fmt.Fprintf(r.w, "note: %s had no build record; if its job is not deployed yet, fugaro update --repo in this checkout deploys it\n", wf)
		}
	}
	// The provider credential's ID the rebuilds' clones read: a refresh has
	// just applied nothing, so unlike init's own first builds it resolves
	// live and compares that with the build account's own grant (B1), the
	// same way a standalone fugaro image build does.
	gitID, err := gitSecretID(ctx, t.lc, t.spec, "the image refresh of "+t.spec.Name)
	if err != nil {
		return err
	}
	var built []string
	for _, wf := range todo {
		ok, reachable, err := r.askBuild(cloudBuildBanner(t.spec.Name, wf, t.lc.Build.MachineType, t.spec.BuildServiceAccountEmail, t.spec.RegistryPath))
		if err != nil {
			return err
		}
		if !reachable {
			notes()
			return builtSoFar(userErr("the build of %s/%s needs a terminal where the project's name can be typed (pass --yes to confirm builds without one, from CI or a script; a coding agent's session never confirms one), so it and the builds after it were not submitted", t.spec.Name, wf), built)
		}
		if !ok {
			notes()
			return builtSoFar(userErr("the build of %s/%s was not confirmed (the project's name was not typed), so it and the builds after it were not submitted", t.spec.Name, wf), built)
		}
		if id, err := r.submitAndWait(ctx, b, t.lc, t.cfg, t.spec, wf, t.lc.BaseImage(t.cfg.Workflows[wf].BaseKind()), gitID); err != nil {
			notes()
			if id != "" {
				// Submitted, then the wait failed (Ctrl-C included): the
				// build is not cancelled.
				err = withNote(err, fmt.Sprintf("Cloud Build build %s of %s/%s was already submitted and KEEPS RUNNING (and billing) in Cloud Build: follow it with gcloud builds describe %s --region %s --project %s; a rerun started now may find no build record yet and offer a duplicate build, so wait for it to finish first",
					oneLine(id), t.spec.Name, wf, quoteWord(id), quoteWord(t.lc.BuildRegion()), quoteWord(t.lc.GCPProject)))
			}
			return builtSoFar(err, built)
		}
		built = append(built, wf)
	}
	notes()
	return cloneErr
}
