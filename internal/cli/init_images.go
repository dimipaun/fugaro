package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
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
	return initflow.Left{Stage: initflow.Images, Kind: initflow.LeftPrompt, Text: "run fugaro init in your own terminal and type the project's name to copy the images"}
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

// managed reports whether ref is a release image an earlier run copied into
// this project's registry for kind (so a newer CLI may move it on), as
// opposed to a dev tag or a hand-pushed image the config points at.
func managed(ref, host, kind string) bool {
	rest, ok := strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":")
	return ok && releaseRE.MatchString(rest)
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
		case cur != "" && !managed(cur, host, k):
			notes = append(notes, fmt.Sprintf("base %s: the local config's base image %s is used; nothing mirrored (--base-image KIND=IMAGE changes it)", k, cur))
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

// planned is one image's read-only plan.
type planned struct {
	item imageItem
	plan *mirror.Plan
}

func (s *imagesStage) read(ctx context.Context, m *mirror.Mirror, items []imageItem) ([]planned, error) {
	var out []planned
	for _, it := range items {
		p, err := m.Plan(ctx, it.src, it.dst, "")
		if err != nil {
			return nil, remote(fmt.Errorf("%s: %w", it.label, err))
		}
		out = append(out, planned{it, p})
	}
	return out, nil
}

func (s *imagesStage) show(pl []planned) (transfer int64) {
	w := s.e.r.w
	for _, p := range pl {
		if p.plan.Present {
			fmt.Fprintf(w, "  %s: %s is already the release image (%s), nothing to copy\n", p.item.label, p.item.dst, short(p.plan.Digest))
			continue
		}
		fmt.Fprintf(w, "  %s: copy %s to %s as %s (%.1f MB to send of %.1f MB, %d blobs)\n", p.item.label, p.item.src, p.item.dst.Host+"/"+p.item.dst.Repo+":"+p.item.dst.Tag, short(p.plan.Digest),
			float64(p.plan.TransferBytes)/1e6, float64(p.plan.TotalBytes)/1e6, len(p.plan.Blobs))
		transfer += p.plan.TransferBytes
	}
	return transfer
}

func short(d string) string {
	if len(d) > 19 {
		return d[:19]
	}
	return d
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
	transfer := s.show(pl)
	if transfer == 0 {
		return initflow.Plan{NothingToDo: true, Detail: "No changes"}, nil
	}
	return initflow.Plan{Detail: fmt.Sprintf("%.1f MB to copy", float64(transfer)/1e6)}, nil
}

func (s *imagesStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
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
	transfer := s.show(pl)
	changed := false
	if transfer > 0 {
		o := r.o
		yes := o.yes
		o.yes = env.Yes
		err := r.confirm(fmt.Sprintf("copies %.1f MB from %s into your registry %s/%s, as you (about $%.2f a month of registry storage at $%.2f per GB)",
			float64(transfer)/1e6, s.allow()[0], pl[0].item.dst.Host, r.gcpProject+"/"+mirror.BaseRepository,
			float64(transfer)/1e9*storagePerGBMonth, storagePerGBMonth), "nothing was copied")
		o.yes = yes
		if err != nil {
			return initflow.Outcome{}, err
		}
		for _, p := range pl {
			res, err := m.Copy(ctx, p.plan)
			if err != nil {
				return initflow.Outcome{}, remote(fmt.Errorf("%s: %w (a rerun resumes: blobs already copied are not sent again, and no tag moved)", p.item.label, err))
			}
			fmt.Fprintf(r.w, "  %s: %s\n", p.item.label, res.Description)
			changed = changed || res.Changed
		}
	}
	if err := s.record(pl); err != nil {
		return initflow.Outcome{}, err
	}
	if !changed {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "images copied and verified by digest"}, nil
}

// record writes the mirrored base images into the local config's
// base_images (what the image builds start from), re-reading the file the
// earlier stages wrote. The history image is named by the installation, not
// recorded.
func (s *imagesStage) record(pl []planned) error {
	r := s.e.r
	want := map[string]string{}
	for _, p := range pl {
		if p.item.kind != "" {
			want[p.item.kind] = p.item.dst.String()
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
	for k, v := range want {
		next.BaseImages[k] = v
	}
	// The stage's own confirmation covered the copy; the config line it
	// records is shown as a diff like every config write.
	return r.writeLocalConfig(&next, s.e.path, old, true)
}
