package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repofiles"
)

// openRepoJobFiles is how this step opens a repository to read its
// default-branch fugaro.yaml: the project's own provider settings, the way
// every other read of a repository's files opens one (repofiles). It is a
// variable for the reason openSurveyProvider and openFollowUpsProvider are:
// the tests hand it a provider that answers without a network.
var openRepoJobFiles = OpenProvider

// update_jobimages.go is the missing half of the owner's rule of 2026-11-24:
// `fugaro image map set acme/app entry` and `fugaro update` published the map,
// and the repository's own job kept the entry it had been deployed on, because
// a plain update converges the installation and the checkout it runs in and
// never another repository's root. Moving that job took a second command, from
// that repository's checkout (`fugaro update --repo .`), which is what the
// HARD RULE ("update is the whole upgrade") forbids.
//
// So this step: every repository this machine's config lists is read at its
// default branch through the git provider (repofiles, no checkout and no
// working tree), what its workflows' jobs should run is resolved by the same
// resolver a deploy uses (infra.Repo over the same Inputs the --repo engine
// assembles for it), and a job whose deployed image is not that one is moved
// through the Cloud Run Admin API — the call `fugaro image refresh` already
// moves a clone workflow's job with. No Terraform runs here: the repository
// roots stay the --repo engine's, and this step is what makes a deployed job
// agree with the images the project's map now names.
//
// It is a best-effort step of the converge's tail, beside backfillRunSummary and
// reindexRunIndex, and it never fails an update (the HARD RULE): a repository
// nobody could read, a spec that cannot be resolved, a job that is not
// deployed, an API call the project refuses — each is one line, and the next
// `fugaro update` tries again. Nothing here builds an image either: an entry
// whose image the registry does not hold yet is the build's to make, and a job
// moved onto one that is not there would die at its first run instead of being
// told.
//
// It asks no confirmation, and that is the policy rather than an omission
// (CLAUDE.md: a command asks for nothing, docs/no-prompt-policy/): what a
// person would be confirming is the map they edited themselves, and a second
// command that asks it is the defect this step removes. It is not a Terraform
// plan, so the managed gate has no document to judge and none is invented for
// it.
func (e *initEngine) reconcileRepoJobs(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in backfillRunSummary and reindexRunIndex: this is the
	// upgrade's step, and the hook the doors share is the end of the converge
	// (init_stages.go's outcome). `fugaro setup` creates an installation, and
	// an installation it has just created has no repository job anywhere.
	if o.doorName() != updateDoor.name || o.planOnly || o.forget || o.printVars {
		return
	}
	lc := e.lc
	if lc == nil || len(lc.Repos) == 0 || e.r.res.Outputs == nil {
		return
	}
	// Then the session, the rule the steps beside it keep: what this step does
	// is a cloud write, and a coding agent's session under a marker `--yes`'s
	// help promises it "changes nothing outside this machine". The gate's
	// exception is D1016's — where the marks say the project is managed, the
	// session IS the run that converges the installation.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	_, open, err := openRepoJobFiles(os.Environ(), func(msg string) { r.warn(msg) })
	switch {
	case err != nil:
		r.warn("no repository job was checked: " + oneLineCLI(err.Error()))
		return
	case open == nil:
		return
	}
	listed := make([]repofiles.Listed, 0, len(lc.Repos))
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		listed = append(listed, repofiles.Listed{Repo: name, Provider: lc.Repos[name].Provider})
	}
	repos, _, err := repofiles.Read(ctx, repofiles.Input{Repos: listed}, open)
	if err != nil {
		return
	}
	node, found, readErr := repoPolicyRead(ctx, lc)
	policy := repoPolicyInput(lc, node, found, readErr)
	c, cerr := newInitClients(ctx, lc)
	if cerr != nil {
		r.warn("no repository job was checked: " + oneLineCLI(cerr.Error()))
		return
	}
	unread := 0
	for _, repo := range repos {
		if repo.Unread != "" || len(repo.YAML) == 0 {
			unread++
			continue
		}
		if _, err := e.moveRepoJobImages(ctx, c, policy, repo); err != nil {
			r.warn(fmt.Sprintf("%s: its jobs were not checked (%s)", pluginwire.Printable(repo.Repo), oneLineCLI(err.Error())))
			unread++
		}
	}
	// One line for what could not be read, never one per repository: a project
	// with a repository this machine cannot reach is an ordinary project, and a
	// sentence per one of them is the paragraph this release took out of the
	// update's account.
	if unread > 0 {
		r.warn(fmt.Sprintf("%d of %d repositories were not checked: a job of theirs may still run an image this project no longer maps to it", unread, len(repos)))
	}
}

// moveRepoJobImages moves every job of one repository whose deployed image is
// not the image its workflow resolves to. It answers how many it moved, and an
// error only for the repository as a whole — its file, its spec, one call to
// the API — never for a workflow: a workflow whose job is not there is a
// repository nobody deployed, which is not an error here.
func (e *initEngine) moveRepoJobImages(ctx context.Context, c *infra.Clients, policy *projectpolicy.Policy, repo repofiles.Repo) (int, error) {
	lc := e.lc
	provider := lc.Repos[repo.Repo].Provider
	cfg, problems := parseCheckoutFugaroYAML(ctx, repo.YAML, selectedProjectConfig(ctx))
	if cfg == nil {
		why := "its fugaro.yaml is not valid"
		if len(problems) > 0 {
			why = oneLineCLI(problems[0].String())
		}
		return 0, fmt.Errorf("%s", why)
	}
	if cfg.Project != "" && cfg.Project != lc.Name {
		return 0, fmt.Errorf("its fugaro.yaml names another project")
	}
	// A repository a deployed profile admits runs on the project's shared jobs
	// (M6, D2312): it has no per-repository job for this step to move, and the
	// entry its workflows resolve to reaches its runs through the shared job
	// the profile deploys.
	if infra.SharedRepo(policy, provider, repo.Repo) {
		return 0, nil
	}
	spec, err := infra.Repo(infra.Inputs{LC: lc, Repo: repo.Repo, Cfg: cfg, Installation: *e.r.res.Outputs, Policy: policy})
	if err != nil {
		return 0, err
	}
	owner := infra.CheckJobOwner{Repo: spec.Name, Label: spec.Label}
	moved := 0
	for _, wf := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[wf]
		if ws.Job == "" || ws.Image == "" {
			continue
		}
		u, err := infra.PlanWorkflowJob(ctx, c, lc.GCPProject, lc.Region, ws.Job, wf, ws.Image, owner)
		switch {
		case err != nil:
			return moved, err
		case u == nil || !u.Changes():
			continue
		}
		if err := infra.ApplyWorkflowJob(ctx, c, u); err != nil {
			return moved, err
		}
		e.r.note(jobImageLine(repo.Repo, wf, u.OldImage(), u.NewImage()))
		moved++
	}
	return moved, nil
}

// jobImageLine is the one line a moved job leaves: which entry the job runs
// now, and which it ran. Only the image's own name is printed — the registry,
// the project and the repository are the same on both sides, and a line a
// person reads is not the place for them.
func jobImageLine(repo, workflow, oldImage, newImage string) string {
	return fmt.Sprintf("%s %s: job image %s -> %s", pluginwire.Printable(repo), workflow,
		pluginwire.Printable(imageTail(oldImage)), pluginwire.Printable(imageTail(newImage)))
}

// imageTail is an image's last path segment, the entry's own name with its
// tag.
func imageTail(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
