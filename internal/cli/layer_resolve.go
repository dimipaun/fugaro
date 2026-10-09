package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// layeredSince is the first release that understands task.ProjectLayer and
// profiles: the version gate for a launch or build that carries one
// (design §8). Defined here because validate needs it before the launch
// path that owns the gate does; that path moves this definition to itself.
const layeredSince = "0.6.0"

// layerOptions say where findLayer takes the project layer from.
type layerOptions struct {
	File    string // --project-layer: read this file, not the bucket
	Offline bool   // --offline: the cache only
	Data    []byte // a layer already read and checked (Cloud Build, the check job)
	Where   string // where Data came from, for messages
	// NoBucket never reads the canonical object: without Data there is no
	// layer (the daily check job, whose account cannot read fugaro/).
	NoBucket bool
	// Lenient is for the commands that worked offline before 0.6.0
	// (validate, doctor, init's stages, secrets, image render): with no
	// project config selected they read only the cache, and a bucket that
	// cannot be read leaves the layer unknown, with a note, instead of
	// failing. An invalid object still fails. Launches and builds are
	// strict (decision L16).
	Lenient bool
}

// foundLayer is the project layer that applies to a checkout's fugaro.yaml.
type foundLayer struct {
	Layer      *config.ProjectLayer // nil: none applies
	Where      string               // gs://fugaro-runs-<gcp>/fugaro/project-layer.yaml, or the file
	Generation int64
	CheckedAt  time.Time // when the bucket was read (the cache's stamp when cached)
	// Unknown: whether one applies could not be told (--offline with
	// nothing cached).
	Unknown bool
	Note    string // a warning: a cached copy, or why none applies
}

// resolvedFile is a checkout's fugaro.yaml resolved over its project layer.
type resolvedFile struct {
	Cfg      *config.Config // nil when there are Problems
	Res      *config.Resolution
	Layer    foundLayer
	Problems []config.Problem
}

// layerRead reads the canonical object; tests replace it.
var layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
	return b.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
}

// layerBucketOpener opens the runs bucket; tests replace it.
var layerBucketOpener = blobx.Open

// layerBucketTimeout bounds opening the runs bucket and reading the layer
// from it: many lenient commands (validate, doctor, init, secrets, cancel,
// budget, run through checkoutConfig) now read this bucket on an ordinary
// run, and a flaky or offline network must not make them hang in a client
// retry loop instead of falling back to the cache (decision L13) or, in a
// strict command, failing with a clear error. A package var so a test can
// shorten it.
var layerBucketTimeout = 15 * time.Second

// findLayer is the project layer that applies to the fugaro.yaml data
// (docs/design/layered-config.md §3 and §7). None applies to a file
// without gcp_project: (decision L9), or to an installation whose runs
// bucket is not default-named. Otherwise it is the bucket's object, read
// on every call (decision L13): only when the bucket cannot be reached at
// all does a cached copy up to 7 days old stand in, with a note. A present
// but invalid object is an error; it never counts as none.
func findLayer(ctx context.Context, getenv func(string) string, data []byte, lc *localcfg.Config, o layerOptions, now time.Time) (foundLayer, error) {
	project, perr := config.ProjectOf(data)
	gcp, gerr := config.GCPProjectOf(data)
	if perr != nil || gerr != nil || gcp == "" || !config.ProjectNameRE.MatchString(project) || !config.GCPProjectRE.MatchString(gcp) {
		return foundLayer{}, nil // not anchored; the parse reports a malformed file
	}
	anchor := config.LayerAnchor{Project: project, GCPProject: gcp}
	parse := func(text []byte, where string) (*config.ProjectLayer, error) {
		l, ps := config.ParseProjectLayer(text, anchor)
		if len(ps) > 0 {
			return nil, userErr("%s is invalid, so nothing resolves against it: %s; ask an operator to publish a valid one (fugaro config publish)", where, pluginwire.Printable(layerProblemsText(ps)))
		}
		return l, nil
	}
	switch {
	case o.Data != nil:
		l, err := parse(o.Data, o.Where)
		return foundLayer{Layer: l, Where: o.Where}, err
	case o.File != "":
		text, err := readLayerFile(o.File)
		if err != nil {
			return foundLayer{}, userErr("%v", err)
		}
		l, err := parse(text, o.File)
		return foundLayer{Layer: l, Where: o.File}, err
	case o.NoBucket:
		return foundLayer{}, nil
	}
	// offline is forced, not the user's own --offline, when a lenient
	// command has no project config selected: it still only ever reads the
	// cache, but the messages below must say so differently (o.Offline
	// alone, never this local offline, decides whether to say "--offline:").
	offline := o.Offline
	if lc == nil && o.Lenient {
		offline = true
	}
	bucketName := "fugaro-runs-" + gcp
	bucketURL := "gs://" + bucketName
	// A selected project config whose own name differs from the
	// repository's project: never substitutes its bucket_url here: the
	// bucket a layer is read from is always the GCP project's default
	// name, keyed by gcp_project alone, regardless of which project is
	// locally selected. What actually refuses a repository and a layer
	// whose project fields disagree is the anchor check inside
	// config.ParseProjectLayer (which parse, above, runs with an anchor
	// built from this repository's own project: and gcp_project:) and,
	// for every other caller of config.Resolve (such as the runner,
	// against a different ref), config.Resolve's own anchorProblems.
	if lc != nil && lc.Name == project && lc.GCPProject == gcp {
		if lc.RunsBucketName() != bucketName {
			return foundLayer{Note: fmt.Sprintf("the project layer needs the default runs bucket name %s (this installation's is %q), so none applies", bucketName, lc.RunsBucketName())}, nil
		}
		bucketURL = lc.BucketURL()
	}
	where := "gs://" + bucketName + "/" + config.LayerKey
	cached, ok := localcfg.LoadLayerCache(getenv, project)
	matches := ok && cached.GCPProject == gcp && cached.Bucket == bucketName
	ours := matches && cached.UsableOffline(now)
	// fromCache parses a cached copy. Unlike the published object, this is
	// never "ask an operator to publish a valid one": the bucket may be
	// perfectly fine, it is this machine's local cache file that failed to
	// parse (hand-edited, or left over from an incompatible version), so
	// it is deleted and the caller is told to rerun, which reads the
	// bucket's own copy fresh.
	fromCache := func(note string) (foundLayer, error) {
		l, ps := config.ParseProjectLayer([]byte(cached.YAML), anchor)
		if len(ps) > 0 {
			_ = localcfg.DropLayerCache(getenv, project)
			return foundLayer{}, userErr("the local cache of %s's project layer is invalid, so it was deleted: %s; rerun to read the bucket's copy", project, pluginwire.Printable(layerProblemsText(ps)))
		}
		return foundLayer{Layer: l, Where: where, Generation: cached.Generation, CheckedAt: cached.CheckedAt, Note: note}, nil
	}
	if offline {
		prefix := ""
		if o.Offline {
			prefix = "--offline: "
		}
		if ours {
			return fromCache(prefix + fmt.Sprintf("using the cached project layer of %s, %s old", project, ageDays(now.Sub(cached.CheckedAt))))
		}
		// Each sentence says its own true reason: a config selected but
		// --offline passed names no project (never "no project config is
		// selected"); a cache that exists for this installation but is
		// over the 7-day offline limit (decision L13) is not "not cached".
		var reason string
		switch {
		case matches:
			reason = fmt.Sprintf("the cached project layer of %s is %s old, over the 7-day offline limit", project, ageDays(now.Sub(cached.CheckedAt)))
		default:
			reason = fmt.Sprintf("no project layer of %s is cached", project)
		}
		if !o.Offline {
			reason = "no project config is selected and " + reason
		} else {
			reason = prefix + reason
		}
		return foundLayer{Unknown: true, Note: reason + ", so it was not checked"}, nil
	}
	unread := func(err error) (foundLayer, error) {
		if o.Lenient {
			return foundLayer{Unknown: true, Note: fmt.Sprintf("the project layer of %s could not be read (%s), so it was not checked", project, oneLineCLI(err.Error()))}, nil
		}
		return foundLayer{}, err
	}
	// unreachableNote is the cache-fallback note (decision L13), shared by
	// a bucket that fails to open at all and one that opens but whose read
	// fails: either way the bucket "cannot be reached".
	unreachableNote := fmt.Sprintf("using the cached project layer of %s, %s old: %s is unreachable", project, ageDays(now.Sub(cached.CheckedAt)), bucketURL)
	if lc != nil && fakeEndpointsOnGS(lc, bucketURL) {
		// This installation's endpoints are fakes (a test, or a developer
		// pointed everything else at an emulator or no_auth): the gs://
		// bucket above is not the one they stand for, so it is never
		// opened for real (the same guard publishSharedWarn and
		// readRefreshRecord apply to their own bucket reads). Treated
		// exactly like a bucket that cannot be reached at all (decision
		// L13): the cache stands in when it is fresh enough, else unread
		// below leaves the layer unknown for a lenient caller and refuses
		// a strict one.
		if ours {
			return fromCache(unreachableNote)
		}
		return unread(fmt.Errorf("the project layer's bucket (%s) is a fake endpoint in this installation, so it is not read for real", bucketURL))
	}
	octx, cancel := context.WithTimeout(ctx, layerBucketTimeout)
	b, err := layerBucketOpener(octx, bucketURL)
	cancel()
	if err != nil {
		// isUnreachable is checked on the raw error, exactly as the read
		// failure below does: only when the bucket cannot be reached at
		// all does the cache stand in (decision L13), a open failure is no
		// different from a read failure here. Anything else (including a
		// 403 opening the bucket, or credentials Open could not find)
		// goes through bucketErrFor, which both names what was being done
		// ("the project layer of %s") and reports access denied as
		// "no access", not a generic remote failure.
		if isUnreachable(err) && ours {
			return fromCache(unreachableNote)
		}
		return unread(bucketErrFor(bucketURL, "opening the bucket", "the project layer of "+project, err))
	}
	defer b.Close()
	rctx, rcancel := context.WithTimeout(ctx, layerBucketTimeout)
	text, gen, err := layerRead(rctx, b)
	rcancel()
	switch {
	case err == nil:
	case errors.Is(err, blobx.ErrNotExist):
		_ = localcfg.DropLayerCache(getenv, project)
		return foundLayer{}, nil
	case errors.Is(err, blobx.ErrTooLarge):
		// Always an error, in both strict and lenient mode (unlike
		// unread's other failures): an object too large to read is
		// present and published, never "no layer", and never silently
		// swallowed by a lenient command.
		return foundLayer{}, userErr("%s is over the %d KiB limit; ask an operator to publish it again (fugaro config publish)", where, config.LayerMaxBytes>>10)
	case isUnreachable(err) && ours:
		return fromCache(unreachableNote)
	default:
		return unread(bucketErrFor(bucketURL, "reading "+config.LayerKey, "the project layer of "+project, err))
	}
	l, err := parse(text, where)
	if err != nil {
		_ = localcfg.DropLayerCache(getenv, project)
		return foundLayer{}, err
	}
	_ = localcfg.SaveLayerCache(getenv, project, localcfg.SharedCacheEntry{GCPProject: gcp, Bucket: bucketName, Generation: gen, CheckedAt: now, YAML: string(text)})
	return foundLayer{Layer: l, Where: where, Generation: gen, CheckedAt: now}, nil
}

// resolveFugaroYAML resolves data, a checkout's fugaro.yaml, exactly as the
// runner will (decision L16): over the project layer findLayer finds for
// it. Every in-checkout command goes through here.
func resolveFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config, o layerOptions) (resolvedFile, error) {
	fl, err := findLayer(ctx, os.Getenv, data, lc, o, time.Now())
	if err != nil {
		return resolvedFile{}, err
	}
	cfg, res, ps := config.Resolve(data, fl.Layer)
	if fl.Unknown {
		for i, p := range ps {
			if p.Code == config.CodeNeedsLayer {
				ps[i].Message += "; " + fl.Note + ": select the project config, connect, or pass --project-layer FILE"
			}
		}
	}
	return resolvedFile{Cfg: cfg, Res: res, Layer: fl, Problems: ps}, nil
}

// parseCheckoutFugaroYAML is config.Parse for a checkout's fugaro.yaml over
// its project layer, for the commands that want only the config and its
// problems. A failure to read the layer is one problem.
func parseCheckoutFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config) (*config.Config, []config.Problem) {
	rf, err := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
	if err != nil {
		return nil, []config.Problem{{Path: "project layer", Message: oneLineCLI(err.Error())}}
	}
	return rf.Cfg, rf.Problems
}

// readLayerFile reads a project layer file the user names: a regular file
// only, opened without blocking, read through a cap of the size limit.
func readLayerFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, config.LayerMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > config.LayerMaxBytes {
		return nil, fmt.Errorf("%s is over the %d KiB limit", path, config.LayerMaxBytes>>10)
	}
	return data, nil
}

// layerProblemsText is ps as one line.
func layerProblemsText(ps []config.Problem) string {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return strings.Join(msgs, "; ")
}
