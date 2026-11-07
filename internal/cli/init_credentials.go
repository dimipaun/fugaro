package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
)

// The credentials service's image, and the two names the release publishes it
// under. The service itself is deployed by `fugaro update` on every
// installation (docs/credentials-service/default-on.md, the owner's ruling of
// 2026-11-05): what is left in this file is the resolve the deploy pins
// through -- init_stages.go's pinCredentialsImage, which writes the
// installation root's block and moves an existing one onto this release's
// image -- and the two constants the gates that name the service read.

// resolveCredentialsImage is the service's image, resolved and verified
// exactly as a base kind's pin is: the release's "-pinned" tag, its cosign
// signature, and the digest read back through the installation's remote
// repository with the operator's own reader grant on it. What comes out is the
// reference the tfvars carry, digest-pinned; a tag is never returned.
//
// It takes the run's engine rather than a stage because two paths pin through
// it and both must pin the same way: the deploy that gives an installation its
// first block, and the carry that moves an installation's recorded image
// forward to this release's (init_stages.go's pinCredentialsImage). Neither
// writes a digest this did not return: the release's own signed "-pinned"
// image, read back through the remote repository the job pulls by.
func resolveCredentialsImage(ctx context.Context, e *initEngine) (string, error) {
	ver := releaseVersion()
	if ver == "" {
		return "", userErr("this is a development build, so there is no released credentials service image to pin: the service runs the released image, resolved at its signed %q tag, and a build from a checkout has none; run this stage with a released fugaro", credentialsKind+"-pinned")
	}
	allow := e.images.allow()
	srcHost, srcOwner, _ := strings.Cut(allow[0], "/")
	if srcHost != baseSourceHost {
		return "", userErr("the credentials service's image is pinned through the installation's remote repository, whose upstream is https://%s, so its source must be %s/<owner>, and this run's is %s: pass --image-source %s/<owner>", baseSourceHost, baseSourceHost, allow[0], baseSourceHost)
	}
	digest, err := resolvePinnedBase(ctx, allow[0], credentialsKind, ver)
	if err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	host, err := infra.RegistryHost(e.lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	m, err := newMirror(ctx, e.lc, allow)
	if err != nil {
		return "", err
	}
	regHost, project := registryParts(host)
	client, err := newARClient(ctx, m)
	if err != nil {
		return "", err
	}
	upstream := srcOwner + "/fugaro-" + credentialsKind
	if err := mirror.VerifyThroughRemote(ctx, client, regHost, project, infra.GhcrRegistry, upstream, digest); err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	return mirror.RemoteRef(regHost, project, infra.GhcrRegistry, upstream, ver, digest), nil
}

// credentialsKind is the release image's kind: the name the release publishes
// it under (ghcr.io/<owner>/fugaro-credentials) and the "-pinned" tag's own
// word.
const credentialsKind = "credentials"

// credsServiceSince is the release that carries the credentials service, for
// the gates that name what to upgrade to.
const credsServiceSince = "0.10.0"

// credentialsBundleReady is whether the installation's credentials service
// holds at least one MODEL credential: the readiness half of the switch
// (infra.CredentialsServiceServes, docs/credentials-service/default-on.md
// §1.5), read once for the run and recorded per repository by
// writeRepoConfig.
//
// It is the service's OWN loader and never a second reading of the secret: the
// same store `fugaro creds set` writes through (openBundleStore), the same
// newest-version read, and the same credsvc.ParseBundle the running service
// parses with, so "the bundle holds a model credential" means here exactly
// what it means to the process that has to hand one back. The document's own
// entries answer it and not the container's existence: a container with no
// version at all, one whose newest version is DISABLED, and one that parses
// with no KindModel entry are three ways of holding nothing.
//
// Every failure answers false, and none of them fails the run. The bundle is a
// secret this machine's identity may not be able to read, and a run that could
// not read it is not a run that knows the service is ready: the repository
// keeps its model credentials mounted, which is the fail-safe direction a
// `fugaro update` with the operator's own identity corrects on the next run.
// What each failure is says so as a warning, because the record it leaves
// behind is the reason a repository is not switched on.
//
// Nothing here reads the BUDGET backend or the node: the switch's other half,
// the repository's agent.auth, is read where the spec is built, from the
// checkout's own fugaro.yaml (infra.CredentialsServiceServes).
func (r *initRun) credentialsBundleReady(ctx context.Context, lc *localcfg.Config) bool {
	if r.credsRead {
		return r.credsReady
	}
	r.credsRead, r.credsReady = true, false
	if lc == nil || lc.CredentialsService == nil {
		// No service, nothing to serve a run with — and no read to spend:
		// the block's absence is the answer (the installation could not
		// deploy the service, or its owner removed it).
		return false
	}
	unreadable := func(err error) bool {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s): the repositories of this installation keep the model credentials they mount until one run can read it", oneLineCLI(err.Error())))
		return false
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable(err)
	}
	if err := store.check(ctx); err != nil {
		return unreadable(err)
	}
	meta, err := store.newest(ctx)
	if errors.Is(err, errNoBundleVersion) {
		// An empty container is a state and not a failure: the service was
		// deployed and nothing has been stored in it yet, which is exactly
		// the installation a fresh deploy leaves behind.
		return false
	}
	if err != nil {
		return unreadable(err)
	}
	if meta.state == "DISABLED" {
		return false
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable(err)
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unreadable(err)
	}
	r.credsReady = len(b.Credentials()) > 0
	return r.credsReady
}

// recordCredentialsServiceReady records this run's bundle reading on the ONE
// repository entry whose root this run applies (docs/credentials-service/
// default-on.md, DON8's record, amended by the review of 2026-11-07).
//
// It is the repository door's (repoEngine, init.go), and it runs before the
// repository root's spec is built, so the plan that root makes and the record
// the launch reads are the same answer. Without this the record lags the plan
// by one run — the plan of a held-off root keeps the job's model mounts and
// grants its account no roles/run.invoker, while the record written afterwards
// tells the next launch the service is ready, which is a job handed the
// service's address with no grant to knock with, and the service refuses it
// rather than falling back (D143).
//
// One entry and not every entry the local config holds, which is what this
// helper did first: the record is read as "this repository's root has been
// applied with the service on", and the ONLY thing that makes the service
// usable to a repository is the roles/run.invoker grant its own root's apply
// makes (modules/workflow/sa.tf's google_cloud_run_v2_service_iam_member.
// credentials; the installation root grants run.invoker to operators, the
// coordinator and the verifier and to no job account). Recording readiness for
// a repository whose root this run does not apply is therefore a record of
// something that did not happen: its launch would be handed the service's
// address while its job accounts hold no grant, and a `cascade` run has no
// credential of its own to fall back to, so it halts at the service's door
// (D143). The installation door (install, init.go) applies no repository root
// at all and so writes no record — which is also why a run outside a checkout,
// whose repository stage is skipped, leaves every entry as it was.
//
// The value is written false as well as true: a bundle that is later emptied
// switches the repository off again, and that is the fail-safe direction (see
// writeRepoConfig).
//
// outs is the installation's own record of the service, used when the local
// config does not carry the block yet: on the run that deploys it the config
// is written by the very step this precedes, and the outputs are the only
// place the service's existence is known. Only the block's PRESENCE is read
// here — the bundle's container is derived from the project and the
// installation's name (openBundleStore) — so a URL the first apply could not
// know yet is not a reason to answer false.
func (r *initRun) recordCredentialsServiceReady(ctx context.Context, lc *localcfg.Config, outs infra.InstallationOutputs, repo string) {
	if lc == nil || len(lc.Repos) == 0 {
		return
	}
	key := repoConfigKey(lc, repo)
	entry, ok := lc.Repos[key]
	if !ok {
		// A repository the local config does not hold has nothing for the
		// spec below to read, so this run deploys its root held off — mounts
		// kept, no invoker grant — and its record has to stay false: the
		// write at the end of this door creates the entry from the value the
		// spec was built with, and the run after this one, in the same
		// checkout, is the one that switches it on. Recording it ready here
		// would be a record of a grant this run never made.
		return
	}
	probe := lc
	if probe.CredentialsService == nil {
		// The block from the installation's outputs, which is where it is
		// known on the run that deploys it. A nil one leaves the probe as it
		// is: with no service anywhere the answer is false without a read,
		// which is what credentialsBundleReady says of a config that has no
		// block.
		if block := credentialsServiceBlock(outs.CredentialsService); block != nil {
			withBlock := *lc
			withBlock.CredentialsService = block
			probe = &withBlock
		}
	}
	entry.CredentialsServiceReady = r.credentialsBundleReady(ctx, probe)
	lc.Repos[key] = entry
}

// deploymentConfig is the checkout's resolved config as this run DEPLOYS the
// repository root from: the same document with agent.auth taken from the
// fugaro.yaml at the default branch — the file the runs of this repository
// read — where the working tree would answer differently.
//
// The switch (infra.CredentialsServiceServes) is judged on agent.auth, so a
// plan that reads it from another document than the launch does is a job
// deployed for one auth and launched for another. `fugaro update --repo` reads
// the working tree, which an uncommitted edit or a checkout parked on another
// branch can leave saying oauth over a default branch that says cascade; the
// launcher reads the file at the ref the run checks out (internal/cli/run.go's
// launchFileConfig). Worktree oauth over a cascade default branch plans a root
// that keeps the job's model mounts and grants its account no
// roles/run.invoker, and the launch hands that job the service's address
// anyway, which is the halt with no fallback D143 is about. The other
// direction drops the mount and withholds the address, which leaves the job
// with no model credential at all. The two answers have to come from one
// document, and the document is the default branch's: the repository stage
// already holds the working tree to it, byte for byte (init_repo_target.go's
// readFugaroYAML comparison, "the engine reads the working tree: it must be
// the default branch's file"), and it is the ref a run is cut from unless the
// checkout is parked on another branch origin has (run_ref.go's newRunRef). A
// run cut with --ref from a third branch whose own fugaro.yaml says something
// else again is the case this cannot reach: no update can know which branch a
// future run is cut from, and the default branch is the ref a plan can be held
// to.
//
// It is agent.auth alone that moves, and it moves for the whole deployment and
// not only for the switch: agent.auth is also what decides which model
// credential a job mounts and whether it is a Vertex one (infra's SecretMounts
// and WorkflowAccounts), so a deployment planned for one auth and launched for
// another is incoherent on those axes too. Nothing else is taken from the
// default branch — the workflows, the secrets and the base branch this engine
// deploys are the working tree's, as they have always been.
//
// A default branch this checkout cannot answer for — no remote-tracking ref,
// no file at it, a file that does not resolve, or no project layer to resolve
// it over — leaves the config exactly as it was, which is the fallback the
// launcher's own read takes (launchFileConfig). In the converge door the two
// documents are the same file anyway: the repository stage refuses a checkout
// whose fugaro.yaml is not the default branch's before this engine runs.
func deploymentConfig(ctx context.Context, root string, cfg *config.Config) *config.Config {
	if cfg == nil || root == "" {
		return cfg
	}
	db := defaultBranchParse(ctx, root)
	if db.cfg == nil || db.cfg.Agent.Auth == "" || db.cfg.Agent.Auth == cfg.Agent.Auth {
		return cfg
	}
	out := *cfg
	out.Agent.Auth = db.cfg.Agent.Auth
	return &out
}
