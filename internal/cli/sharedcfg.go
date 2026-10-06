package cli

import (
	"context"
	"fmt"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// publishShared writes the installation-wide part of the local config
// (Config.Shared) to the runs bucket as infra.SharedConfigObject, replacing
// what is there.
func publishShared(ctx context.Context, lc *localcfg.Config) error {
	data, err := lc.Shared().Marshal()
	if err != nil {
		return err
	}
	if len(data) > localcfg.SharedMaxBytes {
		return fmt.Errorf("the shared config is %d bytes, over the %d byte limit", len(data), localcfg.SharedMaxBytes)
	}
	b, err := sharedBucketOpener(ctx, lc.BucketURL())
	if err != nil {
		return err
	}
	defer b.Close()
	return b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"})
}

// sharedBucketOpener is a test seam; production opens the bucket with blobx.
var sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { return blobx.Open(ctx, url) }
