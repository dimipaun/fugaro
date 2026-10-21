package cli

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/basepin"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The images stage (stage 5, design m11-setup-and-skills.md §3.3) puts this
// release's images where the installation can read them, as the person
// running init: the history image is COPIED into the project's own registry
// (with --firebase, which is what deploys its job), and each base kind that
// is asked for (--base) or that the checkout's fugaro.yaml names is PINNED
// by digest through the project's remote (pull-through) repository, with
// nothing copied (design registry-remote-repository.md §3.1-§3.2). The copy
// is internal/mirror's: digest-verified, idempotent, source-allowlisted, no
// credential to the source.
//
// The two halves do not rest on the same anchor, and the difference is the
// point (design registry-remote-repository.md §3.2, §7):
//
//   - a base kind is resolved at the release's "-pinned" tag, its cosign
//     signature is verified against the release's own key, and the digest
//     that signature covers is read back THROUGH the remote repository
//     before it is recorded. The trust anchor is the signature, not the tag;
//   - a history image is COPIED, and ACCEPTED RISK (design §7, §12) still
//     applies to it: the trust anchor is the release tag in ghcr.io as it
//     resolves now. Whoever can write ghcr.io/dimipaun/fugaro-history:X.Y.Z
//     can make the image this copies, and so the user's cloud, run their
//     code. Digest verification proves the copy equals what the registry
//     served, not that the registry served the release. --expect-digest
//     pins a digest got out of band; a signed digest list in the release is
//     the planned fix (plan, follow-up to T2), not in this change.
//
// A kind whose base image the local config already points elsewhere (a dev
// tag, a hand-pushed image, --base-image) is left alone: nothing is
// mirrored for it. A development build has no published image, so nothing
// is mirrored at all and the stage says what to do instead; there is no
// fallback to some other version.

// releaseVersionRE is a release version's own shape, unanchored: the
// version marker a base pin keeps in its tag. Every reader of a pin works
// from it, so a pin whose tag is not one — "latest", a dev sha — is not a
// pin this tool wrote (cover.go's pinVersionRE is the same shape, on the
// other side of the fence).
const releaseVersionRE = `[0-9]+\.[0-9]+\.[0-9]+`

// releaseRE is a published CLI version; anything else is a development
// build.
var releaseRE = regexp.MustCompile(`^` + releaseVersionRE + `$`)

// storagePerGBMonth is Artifact Registry's storage price (about; the first
// 0.5 GB a month is free), for the cost line only.
const storagePerGBMonth = 0.10

// newMirror builds the copier with the person's Application Default
// Credentials (what the other Google calls of init use). Tests replace it.
// UNVERIFIED against the real services: ghcr.io's anonymous pull and
// Artifact Registry taking the OAuth2 access token as a Bearer credential
// for uploads (see internal/mirror); the live check covers both.
var newMirror = func(ctx context.Context, lc *localcfg.Config, allow []string) (*mirror.Mirror, error) {
	if lc.Endpoints.NoAuth {
		return nil, userErr("endpoints: no_auth is set, and the images come from a real registry with real credentials: unset it or use --base-image")
	}
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, userErr("no Google credentials to write the registry: run gcloud auth application-default login (%v)", err)
	}
	return &mirror.Mirror{Token: oauth2.ReuseTokenSource(nil, ts), Allow: allow}, nil
}

// resolvePinnedBase is the resolve-and-verify step of a base kind (design
// §3.2 steps 1-2): it reads the release's "-pinned" tag at source, verifies
// the cosign signature of the digest that tag names, and returns the bare
// digest — never a value that was not verified, and never a fallback to the
// plain version tag a nightly rebuild moves. Tests replace it; it is the
// only thing that may produce a digest this stage pins.
//
// The "-pinned" tag and its signature come from #318's release side; until
// a release publishes one, this refuses with the registry's own reason
// (there is no such tag), which is the design's intended state (§8: the
// live checks are unrunnable until such a release exists).
var resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", userErr("no Google credentials to verify the base image: run gcloud auth application-default login (%v)", err)
	}
	owner, repo, err := basepinSource(source)
	if err != nil {
		return "", err
	}
	m := &mirror.Mirror{Token: oauth2.ReuseTokenSource(nil, ts), Allow: []string{source}}
	ref, err := basepin.Resolve(ctx, basepin.MirrorResolver{M: m}, basepin.CosignVerifier{}, owner, repo, source, kind, version)
	if err != nil {
		return "", err
	}
	_, digest, _ := strings.Cut(ref, "@")
	if digest == "" {
		return "", userErr("verifying %s: no digest came back", ref)
	}
	return digest, nil
}

// basepinSource splits an allowed source prefix into the GitHub owner whose
// images.yml is trusted and the repository name to expect. The owner is the
// prefix's own second segment (never hard-coded), and the repository is
// "fugaro" by the same convention the release uses.
func basepinSource(source string) (owner, repo string, err error) {
	if err := mirror.ValidSourcePrefix(source); err != nil {
		return "", "", userErr("%v", err)
	}
	_, owner, _ = strings.Cut(source, "/")
	return owner, "fugaro", nil
}

// newARClient is the client the read-back through the remote repository
// uses: the person's Application Default Credentials, which carry their
// reader grant on that repository (Task 1, design §5.3). It refuses a
// redirect off the registry's own host — so the credential cannot be
// carried somewhere else — and it refuses any redirect that is not https,
// because a same-host redirect from https to http would carry the Bearer
// token in cleartext over a connection anyone on the path can read.
// Tests replace it.
var newARClient = func(ctx context.Context, m *mirror.Mirror) (*http.Client, error) {
	if m == nil || m.Token == nil {
		return nil, userErr("no Google credentials to read the base image back through the registry")
	}
	c := oauth2.NewClient(ctx, m.Token)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		if req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("refusing a redirect to %s: the registry credential stays on the registry's own host", req.URL.Host)
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: the registry credential is sent over https only, and following this one would put it on the wire in cleartext", req.URL)
		}
		return nil
	}
	return c, nil
}

// registryParts splits the installation's registry host
// ("<region>-docker.pkg.dev/<project>", infra.RegistryHost) into the bare
// host and the project, which the remote repository's path needs
// separately.
func registryParts(host string) (regHost, project string) {
	regHost, project, _ = strings.Cut(host, "/")
	return regHost, project
}

// imageItem is one image this run handles: a copy (history) or a pin (a
// base kind).
type imageItem struct {
	label string // history, or the base kind
	kind  string // the base kind; "" for history
	// src and dst are the copy's source and destination, for history. A
	// base kind is not copied anywhere (design §3.1), so it has neither:
	// what it carries instead is where the remote repository serves it.
	src, dst mirror.Ref
	// upstream is a base kind's path through the remote repository,
	// "<owner>/fugaro-<kind>", built from the allowed source prefix rather
	// than hard-coded so a fork's mirror keeps working.
	upstream string
	// version is a base kind's release version: the tag half of the pin,
	// which managedVersion, baseRefReleaseRE and refreshVerdict's
	// never-downgrade rule read.
	version string
}

type imagesStage struct {
	e *initEngine
	// only, when non-nil, is every base kind the stage handles (fugaro
	// image refresh's workflows'): --base and the checkout's kinds are not
	// added.
	// Nil means the default kinds; non-nil but empty means NO base kinds.
	// It limits base kinds only: the history image (--firebase) is unaffected.
	// Kinds are de-duplicated and unknown ones (not in config.Bases) dropped.
	only []string
}

func newImagesStage(e *initEngine) *imagesStage { return &imagesStage{e: e} }

func (s *imagesStage) Name() string                 { return initflow.Images }
func (s *imagesStage) SelfConfirming()              {}
func (s *imagesStage) Verify(context.Context) error { return nil }
func (s *imagesStage) Left() initflow.Left {
	return promptLeft(initflow.Images, "type the project's name to pin this release's base images and copy the release images", s.e.r.o.doorName())
}

// baseSourceHost is the only host a base kind's source may be on: the
// remote (pull-through) repository's own upstream is https://ghcr.io
// (design §3.1), so a pin through it can only name an owner there.
const baseSourceHost = "ghcr.io"

// releaseVersion is the CLI's published version, "" for a development build.
func releaseVersion() string {
	v := strings.TrimPrefix(Version, "v")
	if releaseRE.MatchString(v) {
		return v
	}
	return ""
}

// allow is the source prefix this run's images come from: --image-source,
// else the one the local config records, else the built-in default.
//
// The record is the fallback because the flag is installation-only (a
// `fugaro update --repo` run refuses it, and every refresh runs without it):
// with the flag alone, a fork's own recorded pin could not even be resolved
// on the run that re-pins it, and every reader would fall back to the
// default owner's images (design §3.6).
func (s *imagesStage) allow() []string {
	if p := s.e.r.o.imageSource; p != "" {
		return []string{p}
	}
	if lc := s.e.lc; lc != nil && lc.ImageSource != "" {
		return []string{lc.ImageSource}
	}
	return mirror.DefaultSources
}

// kinds are the base kinds to mirror: --base's, and the workflows' of the
// checkout init runs in (best effort: no checkout, or no valid fugaro.yaml,
// adds none).
func (s *imagesStage) kinds(ctx context.Context) []string {
	if s.only != nil {
		only := slices.DeleteFunc(slices.Clone(s.only), func(k string) bool { return !slices.Contains(config.Bases, k) })
		return slices.Compact(slices.Sorted(slices.Values(only)))
	}
	set := map[string]bool{}
	for _, k := range s.e.r.o.baseKinds {
		set[k] = true
	}
	if _, cfg, err := loadCheckoutConfigAt(ctx, "."); err == nil && cfg != nil {
		// loadCheckoutConfigAt only returns a cfg that validated (validate.go
		// checks w.Base against config.Bases), so every w.BaseKind() here is
		// already a member of config.Bases.
		for _, w := range cfg.Workflows {
			set[w.BaseKind()] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// baseOwner is the trusted owner a base pin under the remote repository is
// checked against: the owner half of the local config's recorded
// image_source (design §3.6). It is deliberately never derived from the
// reference being checked — the pin is the value the rule tests, so taking
// the owner out of it would accept any owner. "" means none is recorded,
// and every remote-shaped entry then refuses.
func baseOwner(lc *localcfg.Config) string {
	_, owner, _ := strings.Cut(lc.ImageSource, "/")
	return owner
}

// pinnedVersion is the release version of ref when ref is this
// installation's base pin, and only in exactly this shape (design §3.6):
//
//	<host>/<fugaro-ghcr>/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
//
// The owner is the recorded one (another owner's image is not ours to
// keep, and fails closed into customBaseRefusal's escape hatch), the tag
// is a release version, and the digest is required — a remote-repository
// reference without one is not this installation's pin.
func pinnedVersion(ref, host, kind, owner string) (string, bool) {
	if owner == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(ref, host+"/"+infra.GhcrRegistry+"/"+owner+"/fugaro-"+kind+":")
	if !ok {
		return "", false
	}
	v, d, ok := strings.Cut(rest, "@")
	return v, ok && releaseRE.MatchString(v) && expectRE.MatchString(d)
}

// baseIsCurrent reports whether ref — a base image this tool put in place
// for kind — is already what this release's run would pin: this release's
// own pin, or a NEWER pin an earlier, newer CLI made (never downgraded;
// that is why the version tag stays in the reference at all, design §3.1).
// A copy in the installation's own base registry is not: it is the
// pre-migration shape, so it is stale and the run re-pins it (design §5.4's
// B3).
func baseIsCurrent(ref, host, kind, owner, ver string) bool {
	if v, ok := pinnedVersion(ref, host, kind, owner); ok && !newer(ver, v) {
		return true
	}
	return newer(curVersion(ref, host, kind, owner), ver)
}

// foreignProxyRef is a base reference that lives under this installation's
// remote (pull-through) repository without being a pin this tool wrote for
// kind: a path whose owner is not the recorded one, or whose tag is not a
// release version, or which carries no digest. The proxy serves all of
// ghcr.io, so such a path is NOT "the operator's own base image" — its
// owner is whoever wrote the entry, and a run that used it would build and
// run their code. It is never used as a custom base: a custom base belongs
// in the installation's own registry (fugaro-base) or in --base-image.
func foreignProxyRef(ref, host, kind, owner string) bool {
	return strings.HasPrefix(ref, host+"/"+infra.GhcrRegistry+"/") && !isManaged(ref, host, kind, owner)
}

// foreignProxyRefusal is that refusal, in one wording for every caller:
// what the reference is, the shape that was wanted, and the two repairs.
func foreignProxyRefusal(what, ref, host, kind, owner string) error {
	return userErr("%s, %s, is under this installation's remote repository but is not a pin this tool wrote for it (want %s/%s/%s/fugaro-%s:<version>@sha256:<digest>): the proxy serves all of %s, so a path there is not your own image — every job would pull whatever its owner publishes. Remove the entry (fugaro update --base %s writes the pin), or point the kind at an image of your own with --base-image %s=IMAGE",
		what, pluginwire.Printable(ref), host, infra.GhcrRegistry, owner, kind, baseSourceHost, kind, kind)
}

// basePlanItem decides, offline, what this run does about one kind's
// recorded base image: pin it (pin true, note ""), or leave it alone (pin
// false, with the note the run prints). It never decides WHICH reference to
// pin — that is a digest through the remote repository, and it cannot be
// computed offline.
//
// err is a refusal, not a note: a foreign path under the remote
// repository's ID (foreignProxyRef) is not a custom base and the run does
// not go on with it.
func basePlanItem(cur, host, kind, owner, ver string) (note string, pin bool, err error) {
	switch {
	case cur == "":
		return "", true, nil
	case foreignProxyRef(cur, host, kind, owner):
		return "", false, foreignProxyRefusal("the local config's base image for "+kind, cur, host, kind, owner)
	case !isManaged(cur, host, kind, owner):
		return fmt.Sprintf("base %s: the local config's base image %s is used; nothing mirrored (--base-image KIND=IMAGE changes it)", kind, pluginwire.Printable(cur)), false, nil
	case baseIsCurrent(cur, host, kind, owner, ver):
		return fmt.Sprintf("base %s already has %s", kind, pluginwire.Printable(cur)), false, nil
	default:
		return "", true, nil
	}
}

// managedVersion is the release version of ref when ref is a base image
// this tool put in place for kind (so a newer CLI may move it on): a
// release image an earlier run copied into the installation's own base
// registry, or this release's pin through the remote repository. It is
// not a dev tag, a hand-pushed image, or another owner's image.
func managedVersion(ref, host, kind, owner string) (string, bool) {
	if rest, ok := strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":"); ok && releaseRE.MatchString(rest) {
		return rest, true
	}
	return pinnedVersion(ref, host, kind, owner)
}

// newer reports whether release version a is newer than b.
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return false
}

// parseExpectDigests reads --expect-digest KIND=sha256:<hex>.
func parseExpectDigests(vs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vs {
		k, d, ok := strings.Cut(v, "=")
		if ok && slices.Contains(config.LegacyBaseKinds, k) {
			return nil, fmt.Errorf("--expect-digest %q: want KIND=sha256:<64 hex digits>, KIND one of history, %s: %s was removed in 0.7.0 (%s)", v, strings.Join(config.Bases, ", "), k, config.MigrationDoc)
		}
		if !ok || !(k == "history" || slices.Contains(config.Bases, k)) || !expectRE.MatchString(d) {
			return nil, fmt.Errorf("--expect-digest %q: want KIND=sha256:<64 hex digits>, KIND one of history, %s", v, strings.Join(config.Bases, ", "))
		}
		out[k] = d
	}
	return out, nil
}

var expectRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func isManaged(ref, host, kind, owner string) bool {
	_, ok := managedVersion(ref, host, kind, owner)
	return ok
}

func curVersion(ref, host, kind, owner string) string {
	v, _ := managedVersion(ref, host, kind, owner)
	return v
}

// items are the images this run mirrors, and the notes of kinds it leaves
// alone. Nothing is read from the network.
func (s *imagesStage) items(ctx context.Context) (items []imageItem, notes []string, err error) {
	r := s.e.r
	lc := s.e.lc
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	ver := releaseVersion()
	if ver == "" {
		ver = "0.0.0" // refs are built only to name items; a dev build mirrors none
	}
	allow := s.allow()
	mk := func(label, kind, name, srcTag, dstName, dstTag string) error {
		src, err := mirror.ParseSource(allow, allow[0]+"/"+name+":"+srcTag)
		if err != nil {
			return userErr("%v", err)
		}
		dst, err := mirror.Dest(host, lc.GCPProject, dstName, dstTag)
		if err != nil {
			return userErr("%v", err)
		}
		items = append(items, imageItem{label: label, kind: kind, src: src, dst: dst})
		return nil
	}
	if r.o.firebase != "" && s.e.spec.History != nil {
		if err := mk("history", "", "fugaro-history", ver, infra.HistoryImagePackage, "latest"); err != nil {
			return nil, nil, err
		}
	}
	_, srcOwner, _ := strings.Cut(allow[0], "/")
	named := map[string]bool{} // kinds --base-image points elsewhere this run
	for _, v := range r.o.baseImages {
		if k, _, err := localcfg.ParseBaseImageFlag(v); err == nil {
			named[k] = true
		}
	}
	owner := baseOwner(lc)
	for _, k := range s.kinds(ctx) {
		if named[k] {
			notes = append(notes, fmt.Sprintf("base %s: --base-image points it at its own image; nothing mirrored", k))
			continue
		}
		note, pin, err := basePlanItem(lc.BaseImage(k), host, k, owner, ver)
		if err != nil {
			return nil, nil, err
		}
		if !pin {
			notes = append(notes, note)
			continue
		}
		// A base kind is pinned, not copied: the item names the path the
		// remote repository serves it at, and the digest comes from the
		// resolve-and-verify step in read().
		//
		// The pin is read through a repository whose upstream is
		// https://ghcr.io (design §3.1), so a source on any other host
		// could never be served: refused here, where the pin is formed,
		// rather than on --image-source itself — the history image is a
		// direct pull and keeps any host ValidSourcePrefix allows.
		if srcHost, _, _ := strings.Cut(allow[0], "/"); srcHost != baseSourceHost {
			return nil, nil, userErr("base %s is pinned through the remote repository, whose upstream is https://%s, so the base image's source must be %s/<owner>, and this run's is %s: pass --image-source %s/<owner>, or point the kind at your own image with --base-image %s=IMAGE",
				k, baseSourceHost, baseSourceHost, allow[0], baseSourceHost, k)
		}
		items = append(items, imageItem{
			label:    "base " + k,
			kind:     k,
			upstream: srcOwner + "/fugaro-" + k,
			version:  ver,
		})
	}
	return items, notes, nil
}

func (s *imagesStage) Check(ctx context.Context) (initflow.Status, error) {
	items, notes, err := s.items(ctx)
	switch {
	case err != nil:
		return initflow.Status{}, err
	case len(items) == 0 && len(notes) > 0:
		// "No changes" is the stage's word for nothing to do (design
		// image-refresh.md's step 2); the notes say which kind and why.
		return initflow.Status{State: initflow.Skipped, Detail: "No changes: " + strings.Join(notes, "; ")}, nil
	case len(items) == 0:
		return initflow.Status{State: initflow.Skipped, Detail: "no image to pin or copy: the history image goes with --firebase, a base image with --base KIND or a checkout whose fugaro.yaml names it"}, nil
	case releaseVersion() == "":
		return initflow.Status{State: initflow.Skipped, Detail: "development build, no published images to pin or copy: build one from a checkout (sh images/build-base.sh <kind> <tag> and docker push <tag>), then fugaro update --base-image KIND=<tag>"}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "pins this release's base images by digest and copies the release images your registry lacks"}, nil
}

func unpinnedText(u []string) string {
	if len(u) == 0 {
		return "none (every image this run copies is pinned with --expect-digest)"
	}
	return strings.Join(u, ", ")
}

// planned is one image's read-only plan: a copy's mirror.Plan for history,
// or a base kind's pin — the reference to record and the digest the
// signature was verified for, with no plan at all because there is nothing
// to copy.
type planned struct {
	item imageItem
	plan *mirror.Plan // nil for a base kind
	pin  string       // a base kind: what record() writes into base_images
	// digest is a base kind's verified digest, and for a copy it is the
	// plan's own; digestOf is the one reader.
	digest string
}

func (p planned) digestOf() string {
	if p.plan != nil {
		return p.plan.Digest
	}
	return p.digest
}

// pins are the base kinds this run records a pin for.
func pins(pl []planned) []planned {
	var out []planned
	for _, p := range pl {
		if p.item.kind != "" {
			out = append(out, p)
		}
	}
	return out
}

// key is what --expect-digest names the image by.
func (it imageItem) key() string {
	if it.kind == "" {
		return "history"
	}
	return it.kind
}

func (s *imagesStage) read(ctx context.Context, m *mirror.Mirror, items []imageItem) ([]planned, error) {
	expect, err := parseExpectDigests(s.e.r.o.expectDigests)
	if err != nil {
		return nil, userErr("%v", err)
	}
	have := map[string]bool{}
	for _, it := range items {
		have[it.key()] = true
	}
	for _, k := range slices.Sorted(maps.Keys(expect)) {
		if !have[k] {
			return nil, userErr("--expect-digest %s=... matches no image this run handles (it handles: %s): a pin that is not used would be a false assurance", k, strings.Join(slices.Sorted(maps.Keys(have)), ", "))
		}
	}
	for _, k := range slices.Sorted(slices.Values(s.e.r.o.replaceImages)) {
		if slices.Contains(config.Bases, k) {
			return nil, userErr("--replace-image %s: a base kind is not copied anywhere any more, so there is nothing to replace — it is pinned by digest through the remote repository (design registry-remote-repository.md §5.1). To use your own image for it, pass --base-image %s=IMAGE", k, k)
		}
		if !have[k] {
			return nil, userErr("--replace-image %s matches no image this run copies (it copies: %s): an intent that is not used would read as done", k, strings.Join(slices.Sorted(maps.Keys(have)), ", "))
		}
	}
	var out []planned
	for _, it := range items {
		if it.kind != "" {
			p, err := s.readBase(ctx, m, it, expect)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
			continue
		}
		p, err := m.Plan(ctx, it.src, it.dst, expect[it.key()])
		if err != nil {
			return nil, remote(fmt.Errorf("%s: %w", it.label, err))
		}
		// A tag in your registry that names another image is never moved
		// without --replace-image: a release tag, and history:latest too (the
		// live installs have a hand-pushed one, and we cannot tell a copy this
		// tool made from one an older CLI or a person made).
		replace := slices.Contains(s.e.r.o.replaceImages, it.key())
		if p.DestDigest != "" && !p.Present && !replace {
			return nil, userErr("%s: %s is %s in your registry and the release's image is %s: it is never replaced silently; pass --replace-image %s to replace it, and type the project's name at a real terminal to confirm (a base kind can instead be pointed at your own image with --base-image KIND=IMAGE)", it.label, it.dst, p.DestDigest, p.Digest, it.key())
		}
		p.AllowReplace = replace
		out = append(out, planned{item: it, plan: p})
	}
	return out, nil
}

// readBase is a base kind's read (design §3.2), in this order and no other:
// resolve the release's "-pinned" tag and verify its signature, check
// --expect-digest against that digest, then read the digest back THROUGH
// the remote repository — which is what proves the path the jobs will pull
// by works, with the operator's own reader grant on it. Any failure refuses
// and nothing is written. What comes out is the reference to record, not a
// copy: the proxy serves those bytes, and the pin names them by digest.
func (s *imagesStage) readBase(ctx context.Context, m *mirror.Mirror, it imageItem, expect map[string]string) (planned, error) {
	digest, err := resolvePinnedBase(ctx, s.allow()[0], it.kind, it.version)
	if err != nil {
		return planned{}, remote(fmt.Errorf("%s: %w", it.label, err))
	}
	if e := expect[it.key()]; e != "" && e != digest {
		return planned{}, userErr("%s: --expect-digest says %s and the verified digest is %s: a pin is never written against a value that disagrees with the signature", it.label, e, digest)
	}
	host, err := infra.RegistryHost(s.e.lc)
	if err != nil {
		return planned{}, userErr("%v", err)
	}
	regHost, project := registryParts(host)
	c, err := newARClient(ctx, m)
	if err != nil {
		return planned{}, err
	}
	if err := mirror.VerifyThroughRemote(ctx, c, regHost, project, infra.GhcrRegistry, it.upstream, digest); err != nil {
		return planned{}, remote(fmt.Errorf("%s: %w", it.label, err))
	}
	return planned{item: it, pin: mirror.RemoteRef(regHost, project, infra.GhcrRegistry, it.upstream, it.version, digest), digest: digest}, nil
}

// pending is the copies that have something to do: the destination tag is
// not yet the release image. Whether blobs are missing does not matter (a
// copy may be the manifest alone). A base kind is never here: there is
// nothing to copy, and whether it needs pinning is decided before this.
func pending(pl []planned) (n int, bytes int64) {
	for _, p := range pl {
		if p.plan != nil && !p.plan.Present {
			n++
			bytes += p.plan.TransferBytes
		}
	}
	return n, bytes
}

func (s *imagesStage) show(pl []planned) {
	w := s.e.r.w
	for _, p := range pl {
		if p.item.kind != "" {
			fmt.Fprintf(w, "  %s: pin %s (this release's base image, its signature verified and read back through the remote repository: nothing is copied)\n", p.item.label, p.pin)
			continue
		}
		d := p.item.dst
		to := d.Host + "/" + d.Repo + ":" + d.Tag
		switch {
		case p.plan.Present:
			fmt.Fprintf(w, "  %s: %s is already the release image %s, nothing to copy\n", p.item.label, to, p.plan.Digest)
		case p.plan.DestDigest != "":
			fmt.Fprintf(w, "  %s: copy %s to %s: REPLACES %s with %s (%.1f MB to send of %.1f MB, %d blobs)\n", p.item.label, p.item.src, to, p.plan.DestDigest, p.plan.Digest,
				float64(p.plan.TransferBytes)/1e6, float64(p.plan.TotalBytes)/1e6, len(p.plan.Blobs))
		default:
			fmt.Fprintf(w, "  %s: copy %s to %s: adds %s (%.1f MB to send of %.1f MB, %d blobs)\n", p.item.label, p.item.src, to, p.plan.Digest,
				float64(p.plan.TransferBytes)/1e6, float64(p.plan.TotalBytes)/1e6, len(p.plan.Blobs))
		}
	}
}

func (s *imagesStage) prepare(ctx context.Context) (*mirror.Mirror, []imageItem, error) {
	items, notes, err := s.items(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range notes {
		fmt.Fprintln(s.e.r.w, "  "+n)
	}
	if releaseVersion() == "" || len(items) == 0 {
		return nil, nil, nil
	}
	m, err := newMirror(ctx, s.e.lc, s.allow())
	if err != nil {
		return nil, nil, err
	}
	m.Log = func(l string) { fmt.Fprintln(s.e.r.w, "    "+l) }
	return m, items, nil
}

func (s *imagesStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	m, items, err := s.prepare(ctx)
	if err != nil || m == nil {
		return initflow.Plan{NothingToDo: true}, err
	}
	pl, err := s.read(ctx, m, items)
	if err != nil {
		return initflow.Plan{}, err
	}
	s.show(pl)
	n, bytes := pending(pl)
	base := pins(pl)
	if n == 0 && len(base) == 0 {
		return initflow.Plan{NothingToDo: true, Detail: "No changes"}, nil
	}
	return initflow.Plan{Detail: changeDetail(n, bytes, len(base))}, nil
}

// changeDetail is one line saying what the stage will do: how many images it
// copies and how many base kinds it pins.
func changeDetail(n int, bytes int64, base int) string {
	var parts []string
	if n > 0 {
		parts = append(parts, fmt.Sprintf("%d image(s) to copy, %.1f MB to send", n, float64(bytes)/1e6))
	}
	if base > 0 {
		parts = append(parts, fmt.Sprintf("%d base image(s) to pin by digest", base))
	}
	return strings.Join(parts, ", ")
}

// applyDetail is changeDetail's past tense, and it is built the same way:
// each clause appears only when the run did that thing. A base kind is
// pinned, never copied, and it never reaches the Copy loop — so a run that
// pinned a base image and copied nothing (a base-only run, or a rerun whose
// history tag is already the release image) must not say a copy happened.
func applyDetail(copied bool, base int) string {
	var parts []string
	if copied {
		parts = append(parts, "images copied and verified by digest")
	}
	if base > 0 {
		parts = append(parts, "base images pinned by digest through the remote repository")
	}
	return strings.Join(parts, "; ")
}

func (s *imagesStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if err := s.e.adoptGuard(initflow.Images); err != nil {
		return initflow.Outcome{}, err
	}
	r := s.e.r
	m, items, err := s.prepare(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if m == nil {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	pl, err := s.read(ctx, m, items)
	if err != nil {
		return initflow.Outcome{}, err
	}
	s.show(pl)
	n, bytes := pending(pl)
	changed := false
	expect, _ := parseExpectDigests(r.o.expectDigests)
	var unpinned []string
	for _, p := range pl {
		if p.plan != nil && !p.plan.Present && expect[p.item.key()] == "" {
			unpinned = append(unpinned, p.item.key())
		}
	}
	if err := s.confirmReplace(pl); err != nil {
		return initflow.Outcome{}, err
	}
	base := pins(pl)
	var done []planned // the plans whose destination tag is, now, the release image
	if n > 0 || len(base) > 0 {
		o := r.o
		yes := o.yes
		o.yes = env.Yes
		notCovered := ""
		for _, p := range pl {
			if p.plan != nil && !p.plan.Present && p.plan.DestDigest != "" {
				notCovered = "it replaces a tag in your registry"
			}
		}
		err := r.confirmOrdinary(s.confirmText(pl, n, bytes, unpinned), "nothing was copied and nothing was pinned", notCovered)
		o.yes = yes
		if err != nil {
			return initflow.Outcome{}, err
		}
	}
	for _, p := range pl {
		if p.item.kind != "" {
			// Nothing to copy: the remote repository serves the bytes, and
			// the pin names them by digest.
			done = append(done, p)
			continue
		}
		if p.plan.Present {
			done = append(done, p)
			continue
		}
		res, err := m.Copy(ctx, p.plan)
		if err != nil {
			return initflow.Outcome{}, remote(fmt.Errorf("%s: %w (a rerun resumes: blobs already copied are not sent again)", p.item.label, err))
		}
		fmt.Fprintf(r.w, "  %s: %s\n", p.item.label, res.Description)
		changed = changed || res.Changed
		done = append(done, p)
	}
	if err := s.record(ctx, done); err != nil {
		return initflow.Outcome{}, err
	}
	if !changed && len(base) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: applyDetail(changed, len(base))}, nil
}

// confirmText is the stage's own confirmation: what it copies (and how big,
// and how it is trusted) and what it pins. The two halves say different
// things about trust, and the difference is the point — a copy's digest is
// what the registry says, while a base pin's digest is what a signature
// was verified for — so they are never merged into one claim.
func (s *imagesStage) confirmText(pl []planned, n int, bytes int64, unpinned []string) string {
	r := s.e.r
	var parts []string
	if n > 0 {
		parts = append(parts, fmt.Sprintf("copies %d image(s), %.1f MB, from %s into your registry %s/%s/%s as you, exactly as listed above (about $%.2f a month of registry storage at $%.2f per GB). The digests above are what the release tags resolve to now, which is what the registry says, not a signature. NOT pinned with --expect-digest: %s",
			n, float64(bytes)/1e6, s.allow()[0], pl[0].item.dst.Host, r.gcpProject, mirror.BaseRepository, float64(bytes)/1e9*storagePerGBMonth, storagePerGBMonth, unpinnedText(unpinned)))
	}
	if base := pins(pl); len(base) > 0 {
		var refs []string
		for _, p := range base {
			refs = append(refs, fmt.Sprintf("base_images.%s = %s", p.item.kind, p.pin))
		}
		// Which verifier ran is part of what the pin rests on, so the
		// confirmation names it when it resolves; the verification itself
		// has already happened by now (read() runs before this), and a
		// missing binary refuses there rather than here.
		verifier := ""
		if p := basepin.CosignPath(); p != "" {
			verifier = fmt.Sprintf(" (verified by %s)", p)
		}
		parts = append(parts, fmt.Sprintf("pins %d base image(s) to this release's image at the digest its cosign signature was verified for%s, read back through the remote repository %s/%s (nothing is copied, and nothing is pushed into %s): %s",
			len(base), verifier, r.gcpProject, infra.GhcrRegistry, mirror.BaseRepository, strings.Join(refs, "; ")))
	}
	return strings.Join(parts, " — and it ") + " (as you, with your own credentials)"
}

// confirmReplace is the typed-only confirmation (initflow.Typed) of moving a
// tag in the registry that names another image, a release tag or
// history:latest. --replace-image KIND names the intent; the project's name
// typed at a real terminal confirms it, and --yes, --non-interactive, --json,
// a pipe and a coding agent never do. It is asked before anything is copied
// or pinned.
func (s *imagesStage) confirmReplace(pl []planned) error {
	var kinds, lines []string
	for _, p := range pl {
		if p.plan != nil && !p.plan.Present && p.plan.DestDigest != "" {
			kinds = append(kinds, p.item.key())
			lines = append(lines, fmt.Sprintf("%s (%s) from %s to %s", p.item.dst, p.item.label, p.plan.DestDigest, p.plan.Digest))
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	r := s.e.r
	ok, reachable, err := r.askTyped("REPLACES " + strings.Join(lines, "; ") + " in your registry: whatever uses those tags (jobs, builds) then runs the new image, and the old digest is no longer named by the tag")
	if err != nil {
		return err
	}
	if !reachable {
		args := []string{r.o.doorName()}
		if r.o.firebase != "" {
			args = append(args, "--firebase", quoteWord(r.o.firebase))
		}
		for _, k := range kinds {
			if k != "history" {
				args = append(args, "--base", quoteWord(k))
			}
			args = append(args, "--replace-image", quoteWord(k))
		}
		return &initflow.NeedsYouError{Left: promptLeft(initflow.Images, "type the project's name to confirm replacing "+strings.Join(kinds, ", ")+" when asked, after running", args...)}
	}
	if !ok {
		return userErr("not confirmed (the project's name was not typed); nothing was copied and nothing was pinned")
	}
	return nil
}

// record writes the base images whose destination tag is the release image
// into the local config's base_images (what the image builds start from),
// re-reading the file the earlier stages wrote, and prints the digest each
// tag resolved to. The history image is named by the installation, not
// recorded. A recorded mirrored image newer than this CLI's is kept.
func (s *imagesStage) record(ctx context.Context, pl []planned) error {
	r := s.e.r
	want := map[string]planned{}
	for _, p := range pl {
		if p.item.kind != "" {
			want[p.item.kind] = p
		}
	}
	if len(want) == 0 {
		return nil
	}
	old, err := os.ReadFile(s.e.path)
	if err != nil {
		return userErr("reading the local config to record the base images: %v", err)
	}
	cur, err := localcfg.Parse(old)
	if err != nil {
		return userErr("%s: %v", s.e.path, err)
	}
	next := *cur
	next.BaseImages = maps.Clone(cur.BaseImages)
	if next.BaseImages == nil {
		next.BaseImages = map[string]string{}
	}
	host, err := infra.RegistryHost(s.e.lc)
	if err != nil {
		return userErr("%v", err)
	}
	owner := baseOwner(s.e.lc)
	for _, k := range slices.Sorted(maps.Keys(want)) {
		p := want[k]
		if c := next.BaseImages[k]; c != "" && !isManaged(c, host, k, owner) {
			continue // pointed elsewhere since: not ours to change
		} else if c != "" && newer(curVersion(c, host, k, owner), p.item.version) {
			continue
		}
		next.BaseImages[k] = p.pin
		fmt.Fprintf(r.w, "  base_images.%s = %s (digest %s)\n", k, p.pin, p.digest)
	}
	// The trusted owner the pin is checked against is recorded with the pin
	// (design §3.6): --image-source sets it on the run that pins, and every
	// later reader takes it from here rather than from the flag, which an
	// update --repo run does not even accept.
	next.ImageSource = s.allow()[0]
	// The stage's own confirmation covered the copy; the config line it
	// records is shown as a diff like every config write.
	if err := r.writeLocalConfig(&next, s.e.path, old, true); err != nil {
		return err
	}
	// The engine's own view of the local config follows the file it just
	// wrote, so a rerun of this stage in the same process sees the pin and
	// says No changes rather than resolving and confirming it again.
	s.e.lc = &next
	if !r.o.planOnly {
		r.publishSharedConfig(ctx, &next)
	}
	return nil
}

// reviewLine is the images stage's line of the run's review screen: what it
// will copy and at what size, read now (read-only), else what it does in
// general when the registries cannot be read yet.
func (s *imagesStage) reviewLine(ctx context.Context) string {
	general := "images:         pins this release's base images by digest through the remote repository and copies the release images your registry lacks (sizes are shown when it copies)"
	items, _, err := s.items(ctx)
	if err != nil || len(items) == 0 || releaseVersion() == "" {
		return general
	}
	m, err := newMirror(ctx, s.e.lc, s.allow())
	if err != nil {
		return general
	}
	pl, err := s.read(ctx, m, items) // m.Log is nil: nothing printed
	if err != nil {
		return general
	}
	n, bytes := pending(pl)
	base := pins(pl)
	if n == 0 && len(base) == 0 {
		return "images:         nothing to copy (the release images are in your registry)"
	}
	var names []string
	for _, p := range base {
		names = append(names, p.item.kind+" (pinned by digest through the remote repository)")
	}
	for _, p := range pl {
		if p.plan != nil && !p.plan.Present {
			names = append(names, strings.TrimPrefix(p.item.label, "base ")+" "+p.item.dst.Tag)
		}
	}
	if n == 0 {
		return "images:         " + strings.Join(names, ", ")
	}
	return fmt.Sprintf("images:         %s (%.1f MB, about $%.2f/month)", strings.Join(names, ", "), float64(bytes)/1e6, float64(bytes)/1e9*storagePerGBMonth)
}

// sourceLine is where the images come from and whether their digests are
// pinned, for the review screen (before the typed name). Two trust anchors
// are named separately, because they are not the same one: a base kind is
// resolved at its release's "-pinned" tag and its cosign signature is
// verified, while a copy's digest is what the source registry says unless
// --expect-digest pins it.
func (s *imagesStage) sourceLine(ctx context.Context) string {
	items, _, err := s.items(ctx)
	expect, _ := parseExpectDigests(s.e.r.o.expectDigests)
	var copied, unpinned, verified []string
	for _, it := range items {
		switch {
		case it.kind != "":
			verified = append(verified, it.kind)
		default:
			copied = append(copied, it.key())
			if expect[it.key()] == "" {
				unpinned = append(unpinned, it.key())
			}
		}
	}
	src := pluginwire.Printable(s.allow()[0])
	var parts []string
	if len(verified) > 0 {
		parts = append(parts, fmt.Sprintf("the base image(s) %s are signature-verified at %s's -pinned tag and pinned by digest through the remote repository", strings.Join(verified, ", "), src))
	}
	// The copy clause belongs to a run that copies, exactly as applyDetail's
	// does: a base-only run (fugaro update --base base, no --firebase) pins a
	// base and copies nothing, so "every image copied is pinned with
	// --expect-digest" would claim a copy that did not happen — and a copy
	// is the only thing --expect-digest has anything to do with.
	if len(copied) > 0 {
		if err == nil && len(unpinned) == 0 {
			parts = append(parts, "every image copied is pinned with --expect-digest")
		} else {
			parts = append(parts, "the release tag as it resolves now is the trust anchor for what is copied; NOT pinned with --expect-digest: "+pluginwire.Printable(unpinnedText(unpinned)))
		}
	}
	return "image source: " + src + "; " + strings.Join(parts, "; ")
}
