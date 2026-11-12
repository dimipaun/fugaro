package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// The two lines a copy's bake is worth outside the build that composes it: what
// a build took off a copy it rewrote (noteBakeDropped), and what a mapping
// would expose on a copy that still carries a checkout (noteBakedExposure).
// Both read the same object — builds/catalog/<name>/Dockerfile — because the
// bake is a property of that copy and of nothing this machine holds: the
// catalog entry carries the entry's own definition, and the map says who runs
// the image, so only the copy says whose checkout is in it.

// bakedOwner is the repository a composed definition bakes, as this machine's
// project config names it. The marker names the repository's storage slug
// (imagebake.BakedSlug), which is a hash of the repository and its provider
// (task.Slug), so the repository is found by deriving the slug of each of the
// project's repositories the same way. A slug no repository here answers for —
// a repository the project no longer lists, or one whose provider this config
// does not record — is reported as unknown and the caller names the path
// instead, which is what the nightly check does with the same fact.
func bakedOwner(lc *localcfg.Config, slug string) (string, bool) {
	if lc == nil {
		return "", false
	}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if s, err := task.Slug(lc.Repos[name].Provider, name); err == nil && s == slug {
			return name, true
		}
	}
	return "", false
}

// bakedCheckout names a baked checkout for a person: the repository, when this
// machine's config answers for the slug, and the directory otherwise. It is one
// helper because both lines below name the same thing, and a directory a reader
// cannot place is the one thing they have left.
func bakedCheckout(lc *localcfg.Config, slug string) string {
	dir := imagebake.Root + "/" + slug
	if name, known := bakedOwner(lc, slug); known {
		return "the checkout of " + pluginwire.Printable(name) + " at " + pluginwire.Printable(dir)
	}
	return "a checkout at " + pluginwire.Printable(dir)
}

// noteBakeDropped is the line a build prints when the copy it wrote no longer
// bakes the checkout the copy it replaced carried: the copy is one object every
// repository of the entry reads, so the image its jobs pull has no checkout in
// it from this build on and they clone at start — a change no other part of the
// invocation shows, because the plan a build prints about its own bake
// (noteBakePlan) is quiet about the entries it composes nothing into.
//
// It is a note and not a refusal: the definition a copy holds is the entry's
// own bytes plus the stage the last build composed, so a build that composes
// none and finds one there is rewriting a derived artifact for the map and the
// switch it reads now — which is the direction that keeps another repository's
// source out of an image its jobs run (F18). What the operator owes is the
// line, not a decision.
func noteBakeDropped(w io.Writer, lc *localcfg.Config, entry, slug string) {
	fmt.Fprintf(w, "note: the copy of the catalog entry %s no longer carries %s: the definition this build composes bakes none, so that image's jobs clone at start from now on\n",
		pluginwire.Printable(entry), bakedCheckout(lc, slug))
}

// noteBakedExposure is the note `fugaro image map set` prints when the mapping
// it is about to write puts a repository on an image whose copy still carries a
// checkout baked for another one: the map is what a build reads to decide a
// bake (imagebake.Decide), and it is not what decides the copy, so a mapping
// that widens who runs the entry leaves that checkout in the image those jobs
// pull until the entry is built again — every repository the map puts on the
// entry after the write can read a checkout that is not its own (F18).
//
// The write is not refused, and that is the point of the sentence rather than
// an omission: the map is what the next build reads, so the mapping is exactly
// what makes a build compose a copy for it (the entry becomes one no repository
// of the project alone runs, and a bake lands nowhere else). A refusal here
// would leave the operator with no way to reach the map the rebuild needs.
//
// The copy is read best effort: a machine that cannot read the runs bucket
// writes the mapping without the check and says so, and an entry that was never
// built has no copy to read and nothing to warn about. What the read costs is
// one object, and what it buys is the only moment this hazard can be named
// before jobs run on it — the nightly check refuses such a definition only
// after the fact (internal/cli's catalog check).
func noteBakedExposure(ctx context.Context, w io.Writer, lc, next *localcfg.Config, entry string) {
	if lc == nil || lc.ImageCatalog == nil {
		return
	}
	e, ok := catalogEntryNamed(lc.ImageCatalog, entry)
	if !ok || e.Source.Kind == config.SourceImage {
		// A prebuilt entry has no copy: nothing is composed for it and no
		// checkout is baked into it.
		return
	}
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		return
	}
	url := recordReadURL(lc)
	if url == "" {
		return
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		fmt.Fprintf(w, "fugaro: warning: could not open the bucket the copy of the catalog entry %s is read from (%s), so this mapping is written without checking whether that image carries a checkout its jobs could read\n",
			pluginwire.Printable(entry), oneLine(err.Error()))
		return
	}
	def, _, err := b.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		// Nothing was copied for this entry yet, or this machine may not read
		// it: a bucket that answers 403 is ErrNotExist here (blobx.Read), and
		// an entry no build has composed a copy for is the ordinary case.
		return
	case err != nil:
		fmt.Fprintf(w, "fugaro: warning: could not read the copy of the catalog entry %s (%s), so this mapping is written without checking whether that image carries a checkout its jobs could read\n",
			pluginwire.Printable(entry), oneLine(err.Error()))
		return
	}
	slug, baked := imagebake.BakedSlug(def)
	if !baked {
		return
	}
	owner, known := bakedOwner(lc, slug)
	var exposed []string
	for _, name := range slices.Sorted(maps.Keys(next.ImageMap)) {
		if next.ImageMap[name] == entry && (!known || name != owner) {
			exposed = append(exposed, name)
		}
	}
	if len(exposed) == 0 {
		return
	}
	fmt.Fprintf(w, "note: the copy of the catalog entry %s carries %s, and this mapping puts %s on that image, whose jobs could read it: the entry's next build composes the copy for the map this write leaves, so run fugaro image build catalog %s from the checkout of one of those repositories\n",
		pluginwire.Printable(entry), bakedCheckout(lc, slug),
		strings.Join(printableNames(exposed), ", "), pluginwire.Printable(entry))
}
