package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/settings"
)

// layerKeyPrefix is the project layer's own block in the registry (SD14): the
// layer's `profiles.<n>` is a workflow profile and the local config's is a job
// profile, and one key may not mean two things.
const layerKeyPrefix = "layer."

// layerSide is the read's layer half (S4): this machine's own layer file, the
// published object it is compared with, and which of the two answers the
// project layer's rows and its one source.
//
// The layer has two sides and this is the task that reads both. The local file
// is the layer's one writer (spec §2.2) and the store of every `layer.*` key,
// so the rows read it; the published object is what a run actually resolves, so
// the sha256 comparison between the two is what claims `unpublished` (SD3,
// spec §2.5) — the same comparison, and the same sentence, doctor and
// `fugaro config layer` decide that word by (localLayerState).
type layerSide struct {
	// ran is whether this half ran at all. --offline and an installation with
	// no backend leave the zero value, and the read then answers for the
	// layer's rows exactly as it did before this task: the cached copy, with
	// the age its state is the cache's for.
	ran bool
	// file is the local layer file's path, "" when this machine cannot name
	// one, and local its parsed bytes: nil for a file that is not there and for
	// one this Fugaro does not read, which state.Exists and state.Unreadable
	// tell apart. Malformed is why a file that is there did not parse.
	file      string
	local     *config.ProjectLayer
	malformed string
	// published is the object read from the installation's runs bucket, nil
	// when none is published, and unread why it was not read at all. Only the
	// first of the two may claim `unpublished`: a comparison against an object
	// nobody read is not a difference (spec §2.5).
	published *config.ProjectLayer
	unread    string
	// where is what the comparison was about, for the source of a layer that
	// was not read: the file when the file is the one that could not be read,
	// the bucket when the object is.
	where string
	// state is the shared sha256 comparison, and the sentence `unpublished`
	// prints — the file, both sha256s, which of the two causes it is and the
	// remedy that fits it.
	state layerLocalState
	// values are the keys the file states that the published object does not
	// carry the same way, keyed for the row that prints `unpublished`.
	values map[string]bool
}

// layer is the layer the `layer.*` rows read (spec §2.2): this machine's own
// file when it parsed — the layer's one writer, and the store of every key of
// it — else the published object, for a machine that has not been seeded from
// it yet. Nil when neither is there, and the rows then print their defaults.
//
// The file comes first and not the object because the two answer different
// questions: the object is what a run gets, and it is the STATE column
// (`unpublished`) that says the file's value is not that yet.
func (s layerSide) layer() *config.ProjectLayer {
	if s.local != nil {
		return s.local
	}
	return s.published
}

// layers is the layer a `layer.` row reads, with the age its state is the
// cache's for. The second return is the age because it is what prints
// `stale <age>`: a value read from this machine's file or from the bucket is
// not a copy of anything, so it has none, and only the layer the offline half
// cached is the one that can be out of date.
func (r *settingsRead) layers() (*config.ProjectLayer, time.Duration) {
	if l := r.layerSide.layer(); l != nil {
		return l, 0
	}
	// The side ran and found neither: the cached copy is still the best answer
	// for these rows, exactly as it was before this task — and it says so, with
	// the age of the copy.
	return r.layer, r.layerAge
}

// answers reports whether the layer side has something to report to a consumer:
// one of the two sides is there, or one of them could not be read. A side that
// ran and found neither says nothing, because a source that answered nothing is
// not listed (settingsRead.sources).
func (s layerSide) answers() bool {
	return s.ran && (s.state.Exists || s.state.Unreadable != "" || s.published != nil || s.unread != "")
}

// sourceWords is what the sources list says about the project layer: the status
// it answered for itself and what the layer was. The pair is ("", "") for a
// layer side that did not run, and the renderer then prints the cached copy's
// note as it always has.
//
// The status carries the reason and the note names the layer's side, which is
// the shape the two cloud sources already have (`unread: <reason>` on the
// status, the database's URL or the node's path on the note): a consumer shows
// the reason once beside the layer rather than on every row.
func (s layerSide) sourceWords() (status, note string) {
	if !s.answers() {
		return "", ""
	}
	switch {
	case s.state.Unreadable != "":
		return unreadState(s.state.Unreadable), "the local layer file " + s.file
	case s.malformed != "":
		// The file is there and is not a layer this Fugaro reads, so no value
		// of it was read — and a file with no values to compare is not
		// `unpublished` either: that word is decided per key (spec §2.5), and
		// the parse's own sentence is what the owner needs.
		return unreadState("the local layer file is not a layer this Fugaro reads"),
			fmt.Sprintf("the local layer file %s: %s", s.file, s.malformed)
	case s.state.Unpublished:
		return stateUnpublished, s.state.unpublishedText()
	case s.local != nil && s.unread != "":
		// The file's own values are what these rows read, so the layer was
		// read and the state is ok: what could not be read is the object the
		// file would have been compared with, and `unpublished` is not claimed
		// on a comparison nobody could make (spec §2.5).
		return stateOK, fmt.Sprintf("the local layer file %s (sha256 %s): the published project layer was not read (%s), so nothing is compared with it",
			s.file, shortSHA(s.state.SHA256), s.unread)
	case s.local != nil && s.published != nil:
		return stateOK, fmt.Sprintf("the local layer file %s (sha256 %s) is the published project layer (generation %d)",
			s.file, shortSHA(s.state.SHA256), s.state.Generation)
	case s.local != nil:
		return stateOK, fmt.Sprintf("the local layer file %s (sha256 %s) is not published: this project has published no layer, and %s publishes this one",
			s.file, shortSHA(s.state.SHA256), selfCommand()+" update")
	case s.published != nil:
		return stateOK, fmt.Sprintf("the local layer file %s does not exist: what these rows would carry is the published project layer (generation %d, sha256 %s)",
			s.file, s.state.Generation, shortSHA(s.state.PublishedSHA))
	case s.unread != "":
		return unreadState(s.unread), "the project layer's object " + s.where
	}
	return "", ""
}

// readLayerSide is the layer half of the cloud read (spec §2.7: the project
// layer is its cache, or one bucket read). It is part of the cloud half and not
// of the offline one for the flag's own promise: `--offline` reads no published
// value, so it can neither compare the file with one nor say that it could not
// (spec §2.5, §2.6), and readCloudLayers never reaches this with the flag set.
//
// Nothing here writes: the cache belongs to the commands that read the layer
// for a run (findLayer, which a launch and `fugaro config layer` call), and a
// read command that refreshed it would be a read with a side effect — the one
// thing `fugaro settings` promises not to have.
func (r *settingsRead) readLayerSide(ctx context.Context) {
	t := settingsNow()
	s := layerSide{ran: true}
	// The path cannot fail to resolve here: the read that selected this project
	// already resolved a config directory and a project name, and localcfg
	// validates the name it is given.
	s.file, _ = localcfg.LayerFile(getenvOS, r.project)
	pub, gen, unread, where := r.readPublishedLayer(ctx)
	s.published, s.unread, s.where = pub, unread, where
	s.state = localLayerState(r.local, r.project, pub, gen)
	if s.state.Exists {
		// The state owns the comparison and the sha256 (localLayerState), and
		// the parse is this read's own: the file's bytes are read once more
		// rather than the sha being computed a second way here.
		if data, err := readLayerFile(s.state.Path); err == nil {
			l, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: r.project, GCPProject: r.local.GCPProject})
			if len(ps) > 0 {
				s.malformed = pluginwire.Printable(layerProblemsText(ps))
			} else {
				s.local = l
			}
		}
	}
	// A file that is there and did not parse states no value this read can
	// print, so the rows read the published object instead — and no key is
	// claimed `unpublished` for it either: that word is decided between two
	// parsed layers, and the parse's own sentence is what its owner needs.
	s.values = layerDriftKeys(s.local, pub, s.unread == "" && pub == nil)
	r.layerSide = s
	r.layerMS = readMS(settingsNow().Sub(t))
}

// readPublishedLayer reads the project layer's object from the installation's
// runs bucket: the same object `fugaro config publish` writes (env.bucket, from
// the project config's own bucket_url) and the one a run resolves. It returns
// the parsed object and its generation (nil when none is published), or why it
// was not read at all.
//
// The three doors this does not go through are deliberate. findLayer writes the
// layer cache, which a read command may not. It also keys the bucket by the
// repository's gcp_project and refuses a bucket_url that is not the default
// name, while this read is the installation's own and asks its own bucket —
// the one its publish writes. And the policy node's and the project recipes'
// readers read other documents, not this object.
func (r *settingsRead) readPublishedLayer(ctx context.Context) (l *config.ProjectLayer, gen int64, unread, where string) {
	url := r.local.BucketURL()
	where = url + "/" + config.LayerKey
	if url == "" {
		return nil, 0, "this installation's config names no runs bucket", "the installation's config"
	}
	if fakeEndpointsOnGS(r.local, url) {
		// The bucket stands for a fake endpoint in this installation, so the
		// object is not read for real — the guard findLayer, publishSharedWarn
		// and readRefreshRecord apply to their own bucket reads. It is not an
		// error either: nothing was asked, so nothing is claimed about it.
		return nil, 0, "the project's storage endpoint is a fake in this installation", where
	}
	b, err := layerBucketOpener(ctx, url)
	if err != nil {
		return nil, 0, oneLineCLI(bucketErrFor(url, "opening the bucket", "the project layer of "+r.project, err).Error()), where
	}
	defer b.Close()
	// The cloud half's own deadline bounds this read: settingsCloudBudget is
	// shorter than layerBucketTimeout, so the report's bound is the one a
	// reader of the report sees.
	text, gen, err := layerRead(ctx, b)
	switch {
	case err == nil:
	case errors.Is(err, blobx.ErrNotExist):
		// Nothing is published, which is an answer and not a failure: the
		// comparison then has one side, and every key the file states is
		// `unpublished` — readPolicyLayer's own reading of an absent node.
		return nil, 0, "", where
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, 0, fmt.Sprintf("%s is over the %d KiB limit", where, config.LayerMaxBytes>>10), where
	default:
		return nil, 0, oneLineCLI(bucketErrFor(url, "reading "+config.LayerKey, "the project layer of "+r.project, err).Error()), where
	}
	parsed, ps := config.ParseProjectLayer(text, config.LayerAnchor{Project: r.project, GCPProject: r.local.GCPProject})
	if len(ps) > 0 {
		return nil, 0, fmt.Sprintf("%s is invalid: %s; ask an operator to publish a valid one (%s)", where, pluginwire.Printable(layerProblemsText(ps)), selfCommand()+" config publish"), where
	}
	return parsed, gen, "", where
}

// layerDriftKeys is the registry keys the local layer file states that the
// published object does not carry the same way, keyed for the row that prints
// `unpublished` (SD3). nothingPublished is the object's absence, known by the
// read that looked: with no object published every key the file sets is one,
// which is readPolicyLayer's own answer for an absent node — the file's value
// is not what a run gets, and `unpublished` is what says so.
//
// The keys are the registry's and not the file's: a row is what a consumer
// reads, and a key of the file no row carries has no row to mark.
func layerDriftKeys(local, published *config.ProjectLayer, nothingPublished bool) map[string]bool {
	if local == nil {
		return nil
	}
	out := map[string]bool{}
	for _, row := range settings.Rows() {
		key, ok := strings.CutPrefix(row.Key, layerKeyPrefix)
		if !ok {
			continue
		}
		// A wildcard row is asked the way projectRow asks it, with its `*`
		// literal: the row stands for the concrete spellings, and the file
		// states one of them.
		v, ok := valueAt(local, key)
		if !ok {
			continue
		}
		if nothingPublished {
			out[row.Key] = true
			continue
		}
		if pv, ok := valueAt(published, key); !ok || pv != v {
			out[row.Key] = true
		}
	}
	return out
}
