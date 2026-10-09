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
	if lc == nil && o.Lenient {
		o.Offline = true
	}
	bucketName := "fugaro-runs-" + gcp
	bucketURL := "gs://" + bucketName
	if lc != nil && lc.Name == project && lc.GCPProject == gcp {
		if lc.RunsBucketName() != bucketName {
			return foundLayer{Note: fmt.Sprintf("the project layer needs the default runs bucket name %s (this installation's is %q), so none applies", bucketName, lc.RunsBucketName())}, nil
		}
		bucketURL = lc.BucketURL()
	}
	where := "gs://" + bucketName + "/" + config.LayerKey
	cached, ok := localcfg.LoadLayerCache(getenv, project)
	ours := ok && cached.GCPProject == gcp && cached.Bucket == bucketName && cached.UsableOffline(now)
	fromCache := func(note string) (foundLayer, error) {
		l, err := parse([]byte(cached.YAML), "the cached copy of "+where)
		if err != nil {
			_ = localcfg.DropLayerCache(getenv, project)
			return foundLayer{}, err
		}
		return foundLayer{Layer: l, Where: where, Generation: cached.Generation, CheckedAt: cached.CheckedAt, Note: note}, nil
	}
	if o.Offline {
		if ours {
			return fromCache(fmt.Sprintf("--offline: using the cached project layer of %s, %s old", project, ageDays(now.Sub(cached.CheckedAt))))
		}
		return foundLayer{Unknown: true, Note: fmt.Sprintf("no project config is selected and no project layer of %s is cached, so the project layer was not checked", project)}, nil
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
	b, err := layerBucketOpener(ctx, bucketURL)
	if err != nil {
		err = remote(err)
		if isUnreachable(err) && ours {
			return fromCache(unreachableNote)
		}
		return unread(err)
	}
	defer b.Close()
	text, gen, err := layerRead(ctx, b)
	switch {
	case err == nil:
	case errors.Is(err, blobx.ErrNotExist):
		_ = localcfg.DropLayerCache(getenv, project)
		return foundLayer{}, nil
	case errors.Is(err, blobx.ErrTooLarge):
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
