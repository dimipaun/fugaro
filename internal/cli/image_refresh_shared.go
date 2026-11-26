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
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// This file is one repository's refresh onto a shared dependency-layer image
// (design docs/forge-authorship-images/shared-images.md §3.3, §4, §5, §6, and
// D653, which makes a shared image a catalog entry keyed by the resolved
// dependency set). It is the order §3.3 and §5 state, in one place:
//
//  1. the lock is read, and the entry this repository's job is on is found;
//  2. the entry holds the set the checkout resolves → nothing (an image
//     already in the lock is never rebuilt);
//  3. it does not hold it → a divergence, which is proposed and answered by
//     --on-divergence and never forked silently;
//  4. the repository is on no entry, and the lock holds its key → adopt;
//  5. else the lock holds a superset of its set (§4) → adopt;
//  6. else build once for the key, write the lock after the build succeeded
//     (§3.3), and move this repository's job onto the digest it recorded.
//
// Only this repository's job moves, whatever the choice (§6): a sibling
// repository's job is neither read nor written, which is what makes a
// divergence local to the repository whose refresh this is.
//
// It is unreachable in production until the images ship: config's
// SharedCheckoutRefusal refuses a `checkout: shared` workflow, so no
// repository can declare one and no plan selects one. The engine is written
// against sharedCloud, so its order, its refusals and its lock writes are
// decided here and tested without a bucket, a registry or a Cloud Run job.

// sharedBuild is one shared image the refresh has decided to build: the key
// and the name §2.6 derives from it, and the set the lock will record under
// it. The key is computed on the operator's machine and passed in (§2.5,
// D622); the build writes it as given and never recomputes one.
type sharedBuild struct {
	Key      string
	Image    string
	Workflow string
	Resolved imagecheck.Resolved
}

// sharedBuilt is what a successful build reports: the digest it promoted and
// the build's own ID, both of which the lock entry carries.
type sharedBuilt struct {
	Digest  string
	BuildID string
}

// sharedCloud is the shared refresh's whole outside world: the installation's
// lock, one build, and this repository's job. It is an interface so that
// every decision above is testable on its own, and so that the one piece
// that is not built yet (the source-less dependency layer, sharedBuild's
// production side) is a seam rather than a hole in the order.
type sharedCloud interface {
	ReadLock(ctx context.Context) (*imagecheck.Lock, error)
	// WriteLock applies plan to the lock the way §3.4 does: create-or-replace,
	// one re-plan on a conflict, and nothing written for a plan that returns
	// nil.
	WriteLock(ctx context.Context, plan func(*imagecheck.Lock) (*imagecheck.Lock, error)) error
	Build(ctx context.Context, b sharedBuild) (sharedBuilt, error)
	// MoveJob puts workflow's job of this repository on image, which is the
	// image the lock recorded with its digest.
	MoveJob(ctx context.Context, workflow, image string) error
	// Close releases what ReadLock and WriteLock opened.
	Close() error
}

// sharedRefresh is one workflow of one repository, on its way onto a shared
// image.
type sharedRefresh struct {
	repo     string
	workflow string
	job      string
	key      string
	image    string
	resolved imagecheck.Resolved
	choice   onDivergence
	// agent is the coding agent's environment marker, non-empty in an agent's
	// session: the choice a flag names is a confirmation, and a session is
	// refused every one of them (design §5, T4 of docs/no-prompt-policy).
	agent  string
	cloud  sharedCloud
	out    io.Writer
	detail io.Writer
	warn   func(string)
	now    func() time.Time
}

// run is the order at the head of this file.
func (s *sharedRefresh) run(ctx context.Context) error {
	if s.choice.choice() && s.agent != "" {
		return &initflow.AgentError{Marker: s.agent}
	}
	lock, err := s.cloud.ReadLock(ctx)
	if err != nil {
		return remote(err)
	}
	if lock == nil {
		lock = &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{}}
	}
	onKey, on, found := onEntry(lock, s.repo, s.job)
	if found && on.Holds(s.resolved) {
		fmt.Fprintf(s.detail, "  %s: No changes: its image already holds the set this checkout resolves (key %s)\n", s.workflow, shortKey(onKey))
		return nil
	}
	if found {
		return s.diverged(ctx, lock, onKey, on)
	}
	if e, ok := lock.Images[s.key]; ok {
		return s.adopt(ctx, s.key, e, "the lock already holds this key")
	}
	if k, ok := imagecheck.Superset(lock, s.resolved); ok {
		return s.adopt(ctx, k, lock.Images[k], "an image already holds this set in full")
	}
	return s.buildAndMove(ctx)
}

// diverged is §5: the entry this repository is on does not hold the set its
// checkout resolves, so nothing happens until the person names one of the
// three choices. The proposal is printed for the run that cannot answer, and
// the flag is the whole of how a run answers it.
func (s *sharedRefresh) diverged(ctx context.Context, lock *imagecheck.Lock, onKey string, on imagecheck.LockedImage) error {
	d := &divergence{
		repo: s.repo, workflow: s.workflow, resolved: s.resolved, key: s.key,
		on: on, onKey: onKey,
		candidates: candidatesFor(lock, s.repo, s.resolved, onKey),
	}
	switch {
	case s.choice.pin:
		s.warn(fmt.Sprintf("%s keeps the image it is on (key %s), which does not hold the set this checkout resolves: nothing was built and its job was not moved; %s=fork builds this set an image of its own",
			s.workflow, shortKey(onKey), onDivergenceFlag))
		return nil
	case s.choice.adopt != "":
		key, img, err := entryOfRepo(lock, s.choice.adopt)
		if err != nil {
			return err
		}
		if !img.Holds(s.resolved) {
			return userErr("the image %s is on (key %s) does not hold the whole of this checkout's set at the same versions: %s=adopt= names only a repository whose image is a superset of it",
				pluginwire.Printable(s.choice.adopt), shortKey(key), onDivergenceFlag)
		}
		return s.adopt(ctx, key, img, "the image "+pluginwire.Printable(s.choice.adopt)+" is on")
	case s.choice.fork:
		return s.buildAndMove(ctx)
	}
	d.print(s.out)
	return d.unanswered()
}

// adopt puts this repository on an image the lock already holds: the lock is
// written first and the job moves onto the digest the write recorded, so a job
// never points at an image the lock does not name.
func (s *sharedRefresh) adopt(ctx context.Context, key string, img imagecheck.LockedImage, why string) error {
	if img.Digest == "" {
		return userErr("the lock's entry for key %s names no digest, so there is nothing to put this repository's job on: fugaro image ls lists the images it holds", shortKey(key))
	}
	if err := s.writeLock(ctx, key, nil); err != nil {
		return err
	}
	if err := s.cloud.MoveJob(ctx, s.workflow, digestRef(img)); err != nil {
		return remote(err)
	}
	fmt.Fprintf(s.detail, "  %s: moved onto %s (key %s): %s; nothing was built\n", s.workflow, pluginwire.Printable(digestRef(img)), shortKey(key), why)
	return nil
}

// buildAndMove is §3.3's order: build, and only then write the lock, and only
// then move this repository's job. A build that failed leaves the lock exactly
// as it was, which is what keeps an entry's digest the one a build really
// promoted.
func (s *sharedRefresh) buildAndMove(ctx context.Context) error {
	built, err := s.cloud.Build(ctx, sharedBuild{Key: s.key, Image: s.image, Workflow: s.workflow, Resolved: s.resolved})
	if err != nil {
		return err
	}
	if built.Digest == "" {
		return fmt.Errorf("the build of the image for key %s reported no digest", shortKey(s.key))
	}
	entry := imagecheck.LockedImage{
		Key: s.key, Image: s.image, Digest: built.Digest,
		BaseRef: s.resolved.BaseRef, BaseDigest: s.resolved.BaseDigest,
		Packages: slices.Clone(s.resolved.Packages), Tools: slices.Clone(s.resolved.Tools),
		BuiltAt: s.now().UTC(), BuildID: built.BuildID,
	}
	// §3.4: another machine's refresh can create this key while this one is
	// building, and then its entry is the one that stays. The plan re-decides
	// from the lock it is handed, so the write is the same whichever read it
	// follows.
	var stored imagecheck.LockedImage
	if err := s.writeLock(ctx, s.key, func(l *imagecheck.Lock) {
		if _, ok := l.Images[s.key]; !ok {
			l.Images[s.key] = entry
		}
		stored = l.Images[s.key]
	}); err != nil {
		return err
	}
	if stored.Digest == "" {
		return fmt.Errorf("the lock recorded no digest for key %s", shortKey(s.key))
	}
	if err := s.cloud.MoveJob(ctx, s.workflow, digestRef(stored)); err != nil {
		return remote(err)
	}
	fmt.Fprintf(s.detail, "  %s: built the image for key %s and moved its job onto %s; the lock records it\n",
		s.workflow, shortKey(s.key), pluginwire.Printable(digestRef(stored)))
	return nil
}

// writeLock applies the entry write and the repository's move in one plan:
// the entry (with, when it is new, whatever aSecond adds) and this
// repository's job on that key, removed from every other (§3.1: a job is on at
// most one entry of the lock).
func (s *sharedRefresh) writeLock(ctx context.Context, key string, extra func(*imagecheck.Lock)) error {
	at := s.now().UTC()
	return s.cloud.WriteLock(ctx, func(l *imagecheck.Lock) (*imagecheck.Lock, error) {
		if l.Images == nil {
			l.Images = map[string]imagecheck.LockedImage{}
		}
		if extra != nil {
			extra(l)
		}
		if _, ok := l.Images[key]; !ok {
			return nil, fmt.Errorf("the lock holds no entry for key %s, so this repository's job cannot be put on it", shortKey(key))
		}
		putRepository(l, key, s.repo, s.job, at)
		return l, nil
	})
}

// digestRef is how a job names a shared image: the image and the digest the
// lock recorded for it, so the job runs the image the lock names and not
// whatever a moving tag points at later.
func digestRef(img imagecheck.LockedImage) string {
	return img.Image + "@" + img.Digest
}

// onEntry finds the image the repository's job is on: the entry whose Jobs map
// names this repository with this job's name. A job renamed between two entries
// is the one thing §3.1's pair cannot catch, and this is the writer that knows
// the rename: an entry naming this repository under another job name is the
// image it is on, and the write puts the current name there.
func onEntry(lock *imagecheck.Lock, repo, job string) (string, imagecheck.LockedImage, bool) {
	var renamed string
	var renamedImg imagecheck.LockedImage
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		name, ok := img.Jobs[repo]
		switch {
		case ok && name == job:
			return key, img, true
		case ok && renamed == "":
			renamed, renamedImg = key, img
		}
	}
	if renamed != "" {
		return renamed, renamedImg, true
	}
	return "", imagecheck.LockedImage{}, false
}

// entryOfRepo is the entry a repository named by --on-divergence=adopt= is on.
// A repository on two entries is refused by name rather than guessed at: the
// choice names one image, and which of the two it means is not a fact the
// command has.
func entryOfRepo(lock *imagecheck.Lock, repo string) (string, imagecheck.LockedImage, error) {
	var keys []string
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		if _, ok := img.Jobs[repo]; ok || slices.Contains(img.Repositories, repo) {
			keys = append(keys, key)
		}
	}
	switch len(keys) {
	case 0:
		return "", imagecheck.LockedImage{}, userErr("%s is on no image the lock holds: fugaro image ls lists the images and the repositories on each", pluginwire.Printable(repo))
	case 1:
		return keys[0], lock.Images[keys[0]], nil
	}
	return "", imagecheck.LockedImage{}, userErr("%s is on %d images the lock holds (%s), so %s=adopt=%s names no one of them: write %s=adopt=<another repository>, or %s=fork",
		pluginwire.Printable(repo), len(keys), strings.Join(shortKeys(keys), ", "), onDivergenceFlag, pluginwire.Printable(repo), onDivergenceFlag, onDivergenceFlag)
}

// putRepository places repo's job on key's entry and takes it off every other
// (§3.1): a fork leaves the old entry naming no job, which is what §9's
// clean-up reads, and a job is never named by two entries at once.
func putRepository(l *imagecheck.Lock, key, repo, job string, at time.Time) {
	for k, img := range l.Images {
		if k == key {
			continue
		}
		_, named := img.Jobs[repo]
		if !named && !slices.Contains(img.Repositories, repo) {
			continue
		}
		img.Repositories = slices.DeleteFunc(img.Repositories, func(r string) bool { return r == repo })
		if img.Jobs != nil {
			delete(img.Jobs, repo)
		}
		l.Images[k] = img
	}
	img := l.Images[key]
	if !slices.Contains(img.Repositories, repo) {
		img.Repositories = append(img.Repositories, repo)
		slices.Sort(img.Repositories)
	}
	if img.Jobs == nil {
		img.Jobs = map[string]string{}
	}
	img.Jobs[repo] = job
	img.LastUsedAt = at
	l.Images[key] = img
}

// shortKeys renders keys the way a message names them.
func shortKeys(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = shortKey(k)
	}
	return out
}

// refreshSharedImages is step 4's shared half: every selected workflow the
// repository declares as `checkout: shared` is refreshed onto the shared image
// its resolved set keys. It runs before the baked rebuilds and never stops
// them: the two are independent per-workflow actions bundled into one step
// only because neither touches Terraform, as refreshCloneJobs' own note says.
//
// It is a no-op on every plan today, and every plan of the shared half is
// unreachable with it: config refuses a `checkout: shared` workflow until the
// images ship, so no repository can declare one. The order, the refusals and
// the lock writes are here rather than behind that gate so that the flip is a
// gate and a build, not a rewrite.
func (r *initRun) refreshSharedImages(ctx context.Context, p *refreshPlan, t *refreshTarget) error {
	var shared []string
	for _, wf := range p.workflows {
		if t.cfg.Workflows[wf].Checkout == config.CheckoutShared {
			shared = append(shared, wf)
		}
	}
	if len(shared) == 0 {
		return nil
	}
	if err := refuseSharedNotShipped(t.spec.Name, "fugaro image refresh", shared); err != nil {
		return err
	}
	choice, err := parseOnDivergence(p.onDivergence)
	if err != nil {
		return err
	}
	tree, err := sharedTree(ctx, p, t)
	if err != nil {
		return err
	}
	cloud, err := r.newSharedCloud(ctx, t)
	if err != nil {
		return err
	}
	defer cloud.Close()
	host, err := infra.RegistryHost(t.lc)
	if err != nil {
		return userErr("%v", err)
	}
	baseRef, baseDigest := splitBase(t.lc.BaseImage(config.BaseKind))
	agent := agentMarker(os.Getenv, r.stdinTerminal())
	var first error
	for _, wf := range shared {
		resolved, err := imagecheck.Resolve(t.cfg, wf, tree, baseRef, baseDigest)
		if err != nil {
			var unkeyed *imagecheck.UnkeyedError
			if errors.As(err, &unkeyed) {
				// §2.5: a set that does not reduce leaves the repository
				// unkeyed, on its own per-workflow image, as it is today.
				fmt.Fprintf(r.stageDetail(), "  %s: %v: this repository is unkeyed, so it keeps its own per-workflow image and is on no shared one\n", wf, unkeyed)
				continue
			}
			return userErr("%v", err)
		}
		key := imagecheck.Key(resolved)
		image, err := gcp.SharedImageName(host, key)
		if err != nil {
			return userErr("%v", err)
		}
		s := &sharedRefresh{
			repo: t.spec.Name, workflow: wf, job: t.spec.Workflows[wf].Job,
			key: key, image: image, resolved: resolved, choice: choice, agent: agent,
			cloud: cloud, out: r.w, detail: r.stageDetail(), warn: r.warn, now: time.Now,
		}
		if err := s.run(ctx); err != nil {
			first = errors.Join(first, err)
		}
	}
	return first
}

// sharedTree is the tree the key is computed from: the checkout at the base
// branch head (§2.5, D622), and never the working directory, because the build
// clones the base branch and a set read from another commit would key an image
// built from a tree nothing has. A refresh with no checkout at all — one
// `fugaro update` drove from the project's own list — has no tree to read and
// says so rather than keying a set off nothing.
func sharedTree(ctx context.Context, p *refreshPlan, t *refreshTarget) (imagecheck.Tree, error) {
	if p.view != nil {
		return nil, userErr("a shared image's key is computed from a checkout at the base branch head and this refresh has none: run %s in the checkout", pluginwire.Printable(selfCommand()+" image refresh"))
	}
	branch := t.cfg.Git.BaseBranch
	if branch == "" {
		branch = config.DefaultBaseBranch
	}
	rev := "refs/remotes/origin/" + branch
	tree, err := imagecheck.Open(ctx, p.root, rev, nil)
	if err != nil {
		return nil, userErr("this checkout has no %s to compute the key from: run git fetch origin %s, or run this from a checkout of that branch", rev, pluginwire.Printable(branch))
	}
	return tree, nil
}

// splitBase splits a base image reference into §2.1's base ref and base digest.
// The local config pins a base by digest, so a reference with no @ is one that
// has not been through the base step, and its digest is the empty string the
// lock records for it.
func splitBase(base string) (ref, digest string) {
	ref, digest, _ = strings.Cut(base, "@")
	return ref, digest
}

// refuseSharedNotShipped refuses a refresh of a shared workflow before any
// cloud call while no fugaro builds a shared image: the same belt as
// refuseClonesNotShipped, and the config's own refusal (SharedCheckoutRefusal)
// has already stopped every plan that could reach here.
func refuseSharedNotShipped(repo, cmd string, names []string) error {
	if config.SharedCheckoutShipped() || len(names) == 0 {
		return nil
	}
	return userErr("%s refuses to refresh %s workflow %s: no fugaro builds a shared image yet, so its job could not run; use checkout: baked instead", cmd, pluginwire.Printable(repo), strings.Join(names, ", "))
}

// sharedCloudProd is the shared refresh's production seam: the installation's
// lock in the runs bucket, one build, and this repository's job through the
// Cloud Run Admin API.
type sharedCloudProd struct {
	bucket *blobx.Bucket
	c      *infra.Clients
	lc     *localcfg.Config
	spec   infra.RepoSpec
	owner  infra.CheckJobOwner
}

// newSharedCloud opens the bucket the lock lives in and the client the job
// update needs. The refresh takes the same steps the baked path does to open
// them, so a fence (D1541) and a credentials fault land where they always do.
func (r *initRun) newSharedCloud(ctx context.Context, t *refreshTarget) (sharedCloud, error) {
	b, err := blobx.Open(ctx, t.lc.BucketURL())
	if err != nil {
		return nil, remote(fmt.Errorf("opening the runs bucket %s: %w", t.lc.BucketURL(), err))
	}
	c, err := newInitClients(ctx, t.lc)
	if err != nil {
		_ = b.Close()
		return nil, err
	}
	return &sharedCloudProd{
		bucket: b, c: c, lc: t.lc, spec: t.spec,
		owner: infra.CheckJobOwner{Repo: t.spec.Name, Label: t.spec.Label},
	}, nil
}

func (p *sharedCloudProd) Close() error { return p.bucket.Close() }

// ReadLock is §3.1's read of the installation's lock.
func (p *sharedCloudProd) ReadLock(ctx context.Context) (*imagecheck.Lock, error) {
	return imagecheck.ReadLock(ctx, p.bucket)
}

// WriteLock is §3.4's create-or-replace, one re-plan on a conflict, decided in
// imagecheck.Update.
func (p *sharedCloudProd) WriteLock(ctx context.Context, plan func(*imagecheck.Lock) (*imagecheck.Lock, error)) error {
	return imagecheck.Update(ctx, p.bucket, plan)
}

// Build is the one piece of the shared path that no release ships yet, and it
// refuses rather than building something the design does not define: §12's
// shared image holds the dependency layer alone, and D653 makes the catalog
// its definition — and the catalog has no entry keyed by a resolved dependency
// set, so there is no Dockerfile to build it from. Adopting a key the lock
// already holds is the whole of the shared path a repository can use today.
func (p *sharedCloudProd) Build(ctx context.Context, b sharedBuild) (sharedBuilt, error) {
	return sharedBuilt{}, userErr("nothing builds a shared image yet: no catalog entry is keyed by a dependency set (D653), so only a key the lock already holds can be adopted")
}

// MoveJob is §6's single-repository update: this workflow's job, read through
// the Cloud Run Admin API, moved onto image and nothing else changed.
func (p *sharedCloudProd) MoveJob(ctx context.Context, workflow, image string) error {
	ws, ok := p.spec.Workflows[workflow]
	if !ok {
		return userErr("this repository's spec has no job for workflow %s", pluginwire.Printable(workflow))
	}
	u, err := infra.PlanWorkflowJob(ctx, p.c, p.lc.GCPProject, p.lc.Region, ws.Job, workflow, image, p.owner)
	switch {
	case errors.Is(err, infra.ErrCheckJobShape):
		return userErr("%v", err)
	case err != nil:
		return remote(err)
	case u == nil:
		return userErr("the job %s of workflow %s was not found: run fugaro update --repo in the checkout to deploy it", pluginwire.Printable(ws.Job), pluginwire.Printable(workflow))
	case !u.Changes():
		return nil
	}
	return remote(infra.ApplyWorkflowJob(ctx, p.c, u))
}
