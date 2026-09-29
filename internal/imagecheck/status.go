package imagecheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// Status is what the runs bucket says about one workflow's image: its
// record and its last check, each nil when there is none.
type Status struct {
	Image  *ImageStatus `json:"image"`
	Check  *CheckState  `json:"check"`
	Errors []string     `json:"errors,omitempty"`
}

// ImageStatus is the part of a record fugaro image status shows.
type ImageStatus struct {
	BuiltAt      time.Time `json:"built_at"`
	AgeS         int64     `json:"age_s"`
	SourceCommit string    `json:"source_commit"`
	BaseRef      string    `json:"base_ref"`
	BaseDigest   string    `json:"base_digest"`
	Image        string    `json:"image"`
	ImageDigest  string    `json:"image_digest"`
	BuildID      string    `json:"build_id"`
}

// ReadStatus reads the workflow's image.json and check.json. An object
// that is missing leaves its part nil; one that can't be read or parsed
// does too, with the problem in Errors. Only the bucket failing to answer
// is an error.
func ReadStatus(ctx context.Context, b *blobx.Bucket, slug, workflow string, now time.Time) (Status, error) {
	var st Status
	data, _, err := b.Read(ctx, RecordKey(slug, workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case errors.Is(err, blobx.ErrTooLarge):
		st.Errors = append(st.Errors, err.Error())
	case err != nil:
		return Status{}, fmt.Errorf("reading %s: %w", RecordKey(slug, workflow), err)
	default:
		rec, perr := ParseRecord(data)
		if perr != nil {
			st.Errors = append(st.Errors, RecordKey(slug, workflow)+": "+perr.Error())
			break
		}
		st.Image = &ImageStatus{
			BuiltAt: rec.BuiltAt, AgeS: int64(now.Sub(rec.BuiltAt) / time.Second), SourceCommit: rec.SourceCommit,
			BaseRef: rec.BaseRef, BaseDigest: rec.BaseDigest, Image: rec.Image, ImageDigest: rec.ImageDigest, BuildID: rec.BuildID,
		}
	}
	data, _, err = b.Read(ctx, CheckKey(slug, workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case errors.Is(err, blobx.ErrTooLarge):
		st.Errors = append(st.Errors, err.Error())
	case err != nil:
		return Status{}, fmt.Errorf("reading %s: %w", CheckKey(slug, workflow), err)
	default:
		cs, perr := ParseCheckState(data)
		if perr != nil {
			st.Errors = append(st.Errors, CheckKey(slug, workflow)+": "+perr.Error())
			break
		}
		st.Check = cs
	}
	return st, nil
}
