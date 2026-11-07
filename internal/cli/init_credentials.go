package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
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
