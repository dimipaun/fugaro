// Package basepin resolves what a release's fugaro-base -pinned tag
// currently names in the registry and verifies that specific digest's
// OCI-attached cosign signature, from images.yml's own tag-push identity
// (design retire-image-mirror.md §2). It is called when a base pin is
// formed and only then (fugaro update --base / image refresh,
// internal/cli/init_images.go) — never at a Cloud Build submission — and
// nothing records its result anywhere beyond the pin it feeds: since the
// registry-remote-repository change the base image is not copied, so the
// verified digest IS the result, and the pin is where it lands (design
// registry-remote-repository.md §3.2).
//
// It exists because the trust anchor of the image path used to be "the
// tag as the registry resolves it now": whoever can write
// ghcr.io/<owner>/fugaro-*:X.Y.Z can make the user's cloud run their code
// (internal/mirror's own ACCEPTED RISK). Resolving the nightly-immune
// "-pinned" tag and checking the signature its release attached to that
// exact digest replaces that anchor with "a tag push of this repository
// signed this digest".
//
// Nothing is ever returned unverified: a resolve failure, a verification
// failure, or a signature for a different digest is an error, and no
// caller has a fallback to a plain tag.
//
// UNVERIFIED against the real services: whether ghcr.io stores cosign v3's
// signature as an OCI referrer or under the sha256-<hex>.sig fallback tag,
// and whether cosign's own verification path reads it there. The live
// check settles it; until a release publishes a signed -pinned tag, every
// call here fails closed.
package basepin

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/mirror"
)

// TagResolver resolves what source+"/fugaro-"+kind+":"+version+"-pinned"
// currently names in the registry — the same anonymous, digest-verified
// read internal/mirror already does, against the nightly-immune "-pinned"
// tag, never the plain version tag a nightly rebuild moves. kind is the
// bare base kind ("base" — history is out of scope), NOT pre-prefixed
// with "fugaro-": the implementation builds the full image name itself.
type TagResolver interface {
	ResolveDigest(ctx context.Context, source, kind, version string) (digest string, err error)
}

// CertIdentity is what a signing certificate must name: the workflow ref
// GitHub's OIDC issues for a TAG PUSH specifically, the issuer that issued
// it, and the two Fulcio extensions that say the run really was a push of
// this repository.
//
// The ref alone is not enough. A certificate subject of
// ".../images.yml@refs/tags/vX.Y.Z" says which workflow ran and which ref it
// ran against — and a workflow_dispatch run started against that same tag
// ref gets the very same subject. Trigger and Repository are the extensions
// (1.3.6.1.4.1.57264.1.2 "event_name" and .1.5 "repository") that separate
// the two, and cosign's --certificate-github-workflow-trigger/-repository
// check them (flag names and semantics confirmed against cosign v2 and v3).
type CertIdentity struct {
	WorkflowRef string // "https://github.com/<owner>/<repo>/.github/workflows/images.yml@refs/tags/vX.Y.Z"
	Issuer      string // "https://token.actions.githubusercontent.com"
	Trigger     string // the GitHub event that started the run: "push"
	Repository  string // the repository the run was in: "<owner>/<repo>"
}

// Issuer is GitHub's OIDC issuer: the only one images.yml's keyless
// signing can have come from.
const Issuer = "https://token.actions.githubusercontent.com"

// TriggerPush is the GitHub event a release's certificate must name: the
// tag push. The nightly and a workflow_dispatch are the events that must
// not pass, and a dispatch against the tag ref is the one the workflow ref
// above cannot tell apart from the push.
const TriggerPush = "push"

// Identity is the certificate identity a release's tag push signs with.
// owner and repoOnGitHub name the GitHub repository whose images.yml is
// trusted; version is the release version, without a leading "v".
func Identity(owner, repoOnGitHub, version string) CertIdentity {
	return CertIdentity{
		WorkflowRef: "https://github.com/" + owner + "/" + repoOnGitHub + "/.github/workflows/images.yml@refs/tags/v" + version,
		Issuer:      Issuer,
		Trigger:     TriggerPush,
		Repository:  owner + "/" + repoOnGitHub,
	}
}

// OCIVerifier checks a specific digest's OCI-attached cosign signature
// against an identity. ref is "<source>/fugaro-<kind>@sha256:<hex>" — the
// verifier MUST pin its own artifact-digest policy to exactly this digest
// (never a "without artifact" / unsafe mode that accepts a signature for
// some other digest). Tests use a fake.
type OCIVerifier interface {
	Verify(ctx context.Context, ref string, id CertIdentity) error
}

// Resolve resolves (via the "-pinned" tag), verifies and returns the
// pinned reference "<source>/fugaro-<kind>:<version>@<digest>" — the CLEAN
// version, no "-pinned" suffix anywhere in the returned string, so
// baseRefReleaseRE and every version-skew gate built on it keep parsing a
// plain "X.Y.Z" unmodified. owner/repoOnGitHub name the GitHub repository
// whose images.yml workflow is trusted. Nothing is ever returned
// unverified.
func Resolve(ctx context.Context, tr TagResolver, v OCIVerifier, owner, repoOnGitHub, source, kind, version string) (string, error) {
	digest, err := tr.ResolveDigest(ctx, source, kind, version)
	if err != nil {
		return "", fmt.Errorf("resolving %s/fugaro-%s:%s-pinned: %w", source, kind, version, err)
	}
	ref := source + "/fugaro-" + kind + "@" + digest
	if err := v.Verify(ctx, ref, Identity(owner, repoOnGitHub, version)); err != nil {
		return "", fmt.Errorf("verifying the signature of %s: %w", ref, err)
	}
	return source + "/fugaro-" + kind + ":" + version + "@" + digest, nil
}

// MirrorResolver is the real TagResolver: it reads the "-pinned" tag
// anonymously through internal/mirror, which already verifies what it
// reads against the digest the registry names.
//
// The owner comes from source (the "--image-source" prefix a fork names),
// never hard-coded, so a fork's own registry works.
type MirrorResolver struct {
	M *mirror.Mirror
}

// ResolveDigest implements TagResolver. source is a registry/owner prefix
// such as "ghcr.io/dimipaun"; kind is the bare base kind.
func (r MirrorResolver) ResolveDigest(ctx context.Context, source, kind, version string) (string, error) {
	if err := mirror.ValidSourcePrefix(source); err != nil {
		return "", err
	}
	host, owner, _ := strings.Cut(source, "/")
	if r.M == nil {
		return "", fmt.Errorf("no registry client for %s", source)
	}
	return r.M.ResolveDigest(ctx, mirror.Ref{Host: host, Repo: owner + "/fugaro-" + kind, Tag: version + "-pinned"})
}
