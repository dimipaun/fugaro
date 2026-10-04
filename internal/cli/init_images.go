package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The images stage (stage 5, design m11-setup-and-skills.md §3.3) copies the
// release's images into the project's own registry, as the person running
// init: the history image (with --firebase, which is what deploys its job)
// and the base kinds that are asked for (--base) or that the checkout's
// fugaro.yaml names. The copy is internal/mirror's: digest-verified,
// idempotent, source-allowlisted, no credential to the source.
//
// ACCEPTED RISK (design §7, §12): the trust anchor is the release tag in
// ghcr.io as it resolves now. Whoever can write ghcr.io/dimipaun/fugaro-*:X.Y.Z
// can make the images this copies, and so the user's cloud, run their code.
// Digest verification proves the copy equals what the registry served, not
// that the registry served the release. --expect-digest pins a digest got
// out of band; cosign verification and a signed digest list in the release
// are the planned fix (plan, follow-up to T2), not in this change.
//
// A kind whose base image the local config already points elsewhere (a dev
// tag, a hand-pushed image, --base-image) is left alone: nothing is
// mirrored for it. A development build has no published image, so nothing
// is mirrored at all and the stage says what to do instead; there is no
// fallback to some other version.

// releaseRE is a published CLI version; anything else is a development
// build.
var releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

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
		return nil, userErr("endpoints: no_auth is set, and the images are copied from a real registry with real credentials: unset it or use --base-image")
	}
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, userErr("no Google credentials to write the registry: run gcloud auth application-default login (%v)", err)
	}
	return &mirror.Mirror{Token: oauth2.ReuseTokenSource(nil, ts), Allow: allow}, nil
}

// imageItem is one image to mirror.
type imageItem struct {
	label    string // history, or the base kind
	kind     string // the base kind; "" for history
	src, dst mirror.Ref
}

type imagesStage struct {
	e *initEngine
}

func newImagesStage(e *initEngine) *imagesStage { return &imagesStage{e: e} }

func (s *imagesStage) Name() string                 { return initflow.Images }
func (s *imagesStage) SelfConfirming()              {}
func (s *imagesStage) Verify(context.Context) error { return nil }
func (s *imagesStage) Left() initflow.Left {
	return promptLeft(initflow.Images, "type the project's name to copy the images", "init")
}

// releaseVersion is the CLI's published version, "" for a development build.
func releaseVersion() string {
	v := strings.TrimPrefix(Version, "v")
	if releaseRE.MatchString(v) {
		return v
	}
	return ""
}

func (s *imagesStage) allow() []string {
	if p := s.e.r.o.imageSource; p != "" {
		return []string{p}
	}
	return mirror.DefaultSources
}

// kinds are the base kinds to mirror: --base's, and the workflows' of the
// checkout init runs in (best effort: no checkout, or no valid fugaro.yaml,
// adds none).
func (s *imagesStage) kinds(ctx context.Context) []string {
	set := map[string]bool{}
	for _, k := range s.e.r.o.baseKinds {
		set[k] = true
	}
	if _, cfg, err := loadCheckoutConfigAt(ctx, "."); err == nil && cfg != nil {
		for _, w := range cfg.Workflows {
			if slices.Contains(config.Bases, w.Base) {
				set[w.Base] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// managedVersion is the release version of ref when it is a release image
// an earlier run copied into this project's registry for kind (so a newer
// CLI may move it on), as opposed to a dev tag or a hand-pushed image the
// config points at.
func managedVersion(ref, host, kind string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":")
	return rest, ok && releaseRE.MatchString(rest)
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
		if !ok || !(k == "history" || slices.Contains(config.Bases, k)) || !expectRE.MatchString(d) {
			return nil, fmt.Errorf("--expect-digest %q: want KIND=sha256:<64 hex digits>, KIND one of history, %s", v, strings.Join(config.Bases, ", "))
		}
		out[k] = d
	}
	return out, nil
}

var expectRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func isManaged(ref, host, kind string) bool { _, ok := managedVersion(ref, host, kind); return ok }

func curVersion(ref, host, kind string) string { v, _ := managedVersion(ref, host, kind); return v }

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
	mk := func(label, kind, name, dstName, dstTag string) error {
		src, err := mirror.ParseSource(allow, allow[0]+"/"+name+":"+ver)
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
		if err := mk("history", "", "fugaro-history", infra.HistoryImagePackage, "latest"); err != nil {
			return nil, nil, err
		}
	}
	named := map[string]bool{} // kinds --base-image points elsewhere this run
	for _, v := range r.o.baseImages {
		if k, _, err := localcfg.ParseBaseImageFlag(v); err == nil {
			named[k] = true
		}
	}
	for _, k := range s.kinds(ctx) {
		switch cur := lc.BaseImage(k); {
		case named[k]:
			notes = append(notes, fmt.Sprintf("base %s: --base-image points it at its own image; nothing mirrored", k))
		case cur != "" && !isManaged(cur, host, k):
			notes = append(notes, fmt.Sprintf("base %s: the local config's base image %s is used; nothing mirrored (--base-image KIND=IMAGE changes it)", k, cur))
		case isManaged(cur, host, k) && newer(curVersion(cur, host, k), ver):
			notes = append(notes, fmt.Sprintf("base %s: the local config has %s, newer than this CLI's %s; kept (an older CLI does not downgrade it)", k, cur, ver))
		default:
			if err := mk("base "+k, k, "fugaro-"+k, "fugaro-"+k, ver); err != nil {
				return nil, nil, err
			}
		}
	}
	return items, notes, nil
}

func (s *imagesStage) Check(ctx context.Context) (initflow.Status, error) {
	items, notes, err := s.items(ctx)
	switch {
	case err != nil:
		return initflow.Status{}, err
	case len(items) == 0 && len(notes) > 0:
		return initflow.Status{State: initflow.Skipped, Detail: strings.Join(notes, "; ")}, nil
	case len(items) == 0:
		return initflow.Status{State: initflow.Skipped, Detail: "no image to copy: the history image goes with --firebase, a base image with --base KIND or a checkout whose fugaro.yaml names it"}, nil
	case releaseVersion() == "":
		return initflow.Status{State: initflow.Skipped, Detail: "development build, no published images to copy: build one from a checkout (sh images/build-base.sh <kind> <tag> and docker push <tag>), then fugaro init --base-image KIND=<tag>"}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "copies the release images that are missing from your registry"}, nil
}

func unpinnedText(u []string) string {
	if len(u) == 0 {
		return "none (every image copied is pinned)"
	}
	return strings.Join(u, ", ")
}

// planned is one image's read-only plan.
type planned struct {
	item imageItem
	plan *mirror.Plan
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
			return nil, userErr("--expect-digest %s=... matches no image this run copies (it copies: %s): a pin that is not used would be a false assurance", k, strings.Join(slices.Sorted(maps.Keys(have)), ", "))
		}
	}
	for _, k := range slices.Sorted(slices.Values(s.e.r.o.replaceImages)) {
		if !have[k] {
			return nil, userErr("--replace-image %s matches no image this run copies (it copies: %s): an intent that is not used would read as done", k, strings.Join(slices.Sorted(maps.Keys(have)), ", "))
		}
	}
	var out []planned
	for _, it := range items {
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
		out = append(out, planned{it, p})
	}
	return out, nil
}

// pending is the plans that have something to do: the destination tag is
// not yet the release image. Whether blobs are missing does not matter (a
// copy may be the manifest alone).
func pending(pl []planned) (n int, bytes int64) {
	for _, p := range pl {
		if !p.plan.Present {
			n++
			bytes += p.plan.TransferBytes
		}
	}
	return n, bytes
}

func (s *imagesStage) show(pl []planned) {
	w := s.e.r.w
	for _, p := range pl {
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
	if n == 0 {
		return initflow.Plan{NothingToDo: true, Detail: "No changes"}, nil
	}
	return initflow.Plan{Detail: fmt.Sprintf("%d image(s) to copy, %.1f MB to send", n, float64(bytes)/1e6)}, nil
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
		if !p.plan.Present && expect[p.item.key()] == "" {
			unpinned = append(unpinned, p.item.key())
		}
	}
	if err := s.confirmReplace(pl); err != nil {
		return initflow.Outcome{}, err
	}
	var done []planned // the plans whose destination tag is, now, the release image
	if n > 0 {
		o := r.o
		yes := o.yes
		o.yes = env.Yes
		err := r.confirm(fmt.Sprintf("copies %d image(s), %.1f MB, from %s into your registry %s/%s/%s as you, exactly as listed above (about $%.2f a month of registry storage at $%.2f per GB). The digests above are what the release tags resolve to now, which is what the registry says, not a signature. NOT pinned with --expect-digest: %s",
			n, float64(bytes)/1e6, s.allow()[0], pl[0].item.dst.Host, r.gcpProject, mirror.BaseRepository, float64(bytes)/1e9*storagePerGBMonth, storagePerGBMonth, unpinnedText(unpinned)), "nothing was copied")
		o.yes = yes
		if err != nil {
			return initflow.Outcome{}, err
		}
	}
	for _, p := range pl {
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
	if err := s.record(done); err != nil {
		return initflow.Outcome{}, err
	}
	if !changed {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "images copied and verified by digest"}, nil
}

// confirmReplace is the typed-only confirmation (initflow.Typed) of moving a
// tag in the registry that names another image, a release tag or
// history:latest. --replace-image KIND names the intent; the project's name
// typed at a real terminal confirms it, and --yes, --non-interactive, --json,
// a pipe and a coding agent never do. It is asked before anything is copied.
func (s *imagesStage) confirmReplace(pl []planned) error {
	var kinds, lines []string
	for _, p := range pl {
		if !p.plan.Present && p.plan.DestDigest != "" {
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
		args := []string{"init"}
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
		return userErr("not confirmed (the project's name was not typed); nothing was copied")
	}
	return nil
}

// record writes the base images whose destination tag is the release image
// into the local config's base_images (what the image builds start from),
// re-reading the file the earlier stages wrote, and prints the digest each
// tag resolved to. The history image is named by the installation, not
// recorded. A recorded mirrored image newer than this CLI's is kept.
func (s *imagesStage) record(pl []planned) error {
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
	for _, k := range slices.Sorted(maps.Keys(want)) {
		p := want[k]
		if c := next.BaseImages[k]; c != "" && !isManaged(c, host, k) {
			continue // pointed elsewhere since: not ours to change
		} else if c != "" && newer(curVersion(c, host, k), p.item.dst.Tag) {
			continue
		}
		next.BaseImages[k] = p.item.dst.String()
		fmt.Fprintf(r.w, "  base_images.%s = %s (digest %s)\n", k, p.item.dst, p.plan.Digest)
	}
	// The stage's own confirmation covered the copy; the config line it
	// records is shown as a diff like every config write.
	return r.writeLocalConfig(&next, s.e.path, old, true)
}
