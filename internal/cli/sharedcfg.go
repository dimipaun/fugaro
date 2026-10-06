package cli

import (
	"context"
	"errors"
	"fmt"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// publishShared writes the installation-wide part of the local config
// (localcfg.MergeShared) to the runs bucket as infra.SharedConfigObject,
// merged with what is published there already, so a machine that lacks
// what another onboarded does not erase it.
func publishShared(ctx context.Context, lc *localcfg.Config) error {
	return publishSharedWarn(ctx, lc, func(string) {})
}

// publishSharedWarn is publishShared that reports what it set aside through
// warn: a published object of another installation, which it ignores.
func publishSharedWarn(ctx context.Context, lc *localcfg.Config, warn func(string)) error {
	b, err := sharedBucketOpener(ctx, lc.BucketURL())
	if err != nil {
		return err
	}
	defer b.Close()
	published, err := readShared(ctx, b)
	if err != nil {
		return err
	}
	if published != nil && !localcfg.SameInstallation(published, lc) {
		warn(fmt.Sprintf("the published shared config is another installation's (project %s, GCP project %s), so it is replaced, not merged", published.Name, published.GCPProject))
	}
	data, err := localcfg.MergeShared(published, lc).Marshal()
	if err != nil {
		return err
	}
	if len(data) > localcfg.SharedMaxBytes {
		return fmt.Errorf("the shared config is %d bytes, over the %d byte limit", len(data), localcfg.SharedMaxBytes)
	}
	return b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"})
}

// readShared is the published object, nil when it is absent, over
// localcfg.SharedMaxBytes or not a valid config: those count as absent. A
// read that fails otherwise is an error.
func readShared(ctx context.Context, b *blobx.Bucket) (*localcfg.Config, error) {
	data, _, err := b.Read(ctx, infra.SharedConfigObject)
	switch {
	case errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrTooLarge):
		return nil, nil
	case err != nil:
		return nil, err
	case len(data) > localcfg.SharedMaxBytes:
		return nil, nil
	}
	c, err := localcfg.Parse(data)
	if err != nil {
		return nil, nil
	}
	return c, nil
}

// sharedBucketOpener is a test seam; production opens the bucket with blobx.
var sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { return blobx.Open(ctx, url) }
