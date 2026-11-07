package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
