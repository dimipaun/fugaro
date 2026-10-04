// Package mirror copies one image from a public registry into the
// project's own Artifact Registry, in Go and with no Docker daemon, as the
// person running fugaro init (design m11-setup-and-skills.md §3.3).
//
// What it will and will not do is fixed here, not by its callers:
//
//   - the source is read anonymously and only from an allowlist of
//     registry/owner prefixes (DefaultSources, or the one a fork names);
//     no credential of ours is ever sent there;
//   - the destination is built only from the installation's own registry
//     host (<region>-docker.pkg.dev/<project>) and the fugaro-base
//     repository, and the person's token is sent only there, never
//     followed to another host;
//   - every manifest and every blob is checked against the digest it is
//     named by before it is trusted or finalized; the tag is read from the
//     source once, everything else is fetched by digest, and a tag that
//     moved between the read and the push is refused;
//   - the destination's tag is written last, so a copy that fails part-way
//     leaves no tag moved (a rerun resumes: blobs already there are not
//     sent again), and the pushed tag is read back and compared.
//
// ACCEPTED RISK: the trust anchor is the source tag as the registry resolves
// it now. Digest verification proves the copy equals what the registry served,
// not that it is the release: whoever can write ghcr.io/<owner>/fugaro-*:X.Y.Z
// can make the user's cloud run their code. Plan's expect digest (init
// --expect-digest) pins one obtained out of band; cosign verification and a
// signed digest list in the release are the planned fix.
//
// Only the linux/amd64 image of a multi-platform index is copied (what
// Cloud Build and Cloud Run run); the destination tag then names that
// image manifest, whose digest is what is verified end to end.
//
// UNVERIFIED against the real services (tests use an in-process fake of the
// OCI distribution subset): ghcr.io's anonymous token flow for a public
// package, Artifact Registry accepting an OAuth2 access token as a Bearer
// credential for blob upload (POST, one PATCH, PUT) and manifest PUT, its
// Location headers staying on its own host, and ghcr's blob redirect target
// (a CDN host, followed without credentials). Cross-repository blob mount
// is deliberately not used. The user checks these in the live check. An
// Artifact Registry access token lasts about an hour; it is read per request,
// and a single upload that outlives it fails with a clear message and is
// resumed by a rerun. A transfer that makes no progress for Mirror.Idle is
// cancelled. A token endpoint is accepted on the registry's own host or a
// sibling under the same two-label domain, never http elsewhere.
package mirror

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultSources are the registry/owner prefixes an image may be copied
// from unless a fork names its own.
var DefaultSources = []string{"ghcr.io/dimipaun"}

// BaseRepository is the repository of the installation's registry the
// copies go to.
const BaseRepository = "fugaro-base"

// Ref is a registry, a repository and a tag.
type Ref struct {
	Host string // registry host, e.g. ghcr.io
	Repo string // repository path, e.g. dimipaun/fugaro-go
	Tag  string
}

func (r Ref) String() string { return r.Host + "/" + r.Repo + ":" + r.Tag }

var (
	hostRE      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$`)
	repoRE      = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagRE       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	registryRE  = regexp.MustCompile(`^([a-z]+-[a-z]+[0-9]+)-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])$`)
	imageNameRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

// ValidSourcePrefix reports whether p is a registry host and an owner path
// (no scheme, no credentials, no tag), the shape a fork may name.
func ValidSourcePrefix(p string) error {
	host, repo, ok := strings.Cut(p, "/")
	if !ok || !hostRE.MatchString(host) || !repoRE.MatchString(repo) || strings.Contains(host, "@") {
		return fmt.Errorf("image source %q is not <registry host>/<owner> (for example ghcr.io/dimipaun)", p)
	}
	return nil
}

// ParseSource reads ref (<host>/<repo>:<tag>, a tag and no digest) and
// refuses one outside the allowed prefixes.
func ParseSource(allow []string, ref string) (Ref, error) {
	if strings.ContainsAny(ref, "@ \t\r\n") {
		return Ref{}, fmt.Errorf("source image %q: give the release tag (<host>/<repo>:<tag>), not a digest or a reference with spaces", ref)
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if slash < 0 || colon < slash {
		return Ref{}, fmt.Errorf("source image %q has no tag", ref)
	}
	name, tag := ref[:colon], ref[colon+1:]
	host, repo, _ := strings.Cut(name, "/")
	if !hostRE.MatchString(host) || !repoRE.MatchString(repo) || !tagRE.MatchString(tag) {
		return Ref{}, fmt.Errorf("source image %q is not <registry host>/<repo>:<tag>", ref)
	}
	for _, p := range allow {
		if strings.HasPrefix(host+"/"+repo, p+"/") {
			return Ref{Host: host, Repo: repo, Tag: tag}, nil
		}
	}
	return Ref{}, fmt.Errorf("source image %s is not under an allowed source (%s): images are copied only from the release's own registry, and a fork names its own with --image-source", name, strings.Join(allow, ", "))
}

// Dest is the destination for image name:tag in the installation's registry
// (<region>-docker.pkg.dev/<project>) and its fugaro-base repository. The
// registry host must be exactly that shape and name gcpProject: nothing
// else can be a destination.
func Dest(registryHost, gcpProject, name, tag string) (Ref, error) {
	m := registryRE.FindStringSubmatch(registryHost)
	switch {
	case m == nil:
		return Ref{}, fmt.Errorf("registry host %q is not <region>-docker.pkg.dev/<project>", registryHost)
	case m[2] != gcpProject:
		return Ref{}, fmt.Errorf("registry host %s names project %s, not %s", registryHost, m[2], gcpProject)
	case !imageNameRE.MatchString(name):
		return Ref{}, fmt.Errorf("image name %q is not a plain name", name)
	case !tagRE.MatchString(tag):
		return Ref{}, fmt.Errorf("image tag %q is not a tag", tag)
	}
	return Ref{Host: m[1] + "-docker.pkg.dev", Repo: m[2] + "/" + BaseRepository + "/" + name, Tag: tag}, nil
}

// Typed refusals the stage and the tests tell apart.
var (
	ErrUnreadableSource = errors.New("the source image cannot be read anonymously")
	ErrNoAmd64          = errors.New("the image has no linux/amd64 build")
	ErrTagMoved         = errors.New("the source tag moved while it was being copied")
	ErrDigestMismatch   = errors.New("digest mismatch")
	ErrWouldReplace     = errors.New("the destination tag is another image")
	ErrDestMoved        = errors.New("the destination tag changed while it was being copied")
)
