package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// ErrChanged means a conditional replace found result.json changed (or
// gone) since it was read.
var ErrChanged = errors.New("result.json changed since it was read")

// RecordVersion is the result.json a conditional replace is matched
// against: its generation on GCS, its content elsewhere.
type RecordVersion struct {
	gen  int64
	data []byte
}

// encodeRecord is result.json's encoding.
func encodeRecord(r *Record) ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

func recordKey(slug, runID string) string { return Open(nil, slug, runID).prefix + "result.json" }

// ReadRecordVersion loads result.json of slug/runID together with the
// version ReplaceRecordIf matches. b must be the runs bucket opened with
// blobx.Open, so the match is generation-based (atomic) on GCS.
func ReadRecordVersion(ctx context.Context, b *blobx.Bucket, slug, runID string) (*Record, RecordVersion, error) {
	key := recordKey(slug, runID)
	data, gen, err := b.Read(ctx, key)
	if errors.Is(err, blobx.ErrNotExist) {
		return nil, RecordVersion{}, fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, RecordVersion{}, fmt.Errorf("reading %s: %w", key, err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, RecordVersion{}, fmt.Errorf("decoding %s: %w", key, err)
	}
	return &r, RecordVersion{gen: gen, data: data}, nil
}

// ReplaceRecordIf overwrites result.json of slug/runID with rec only if it
// is still version v (ErrChanged otherwise). It is atomic on GCS; on other
// drivers it compares contents first, which is not.
func ReplaceRecordIf(ctx context.Context, b *blobx.Bucket, slug, runID string, rec *Record, v RecordVersion) error {
	data, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	key := recordKey(slug, runID)
	_, err = b.ReplaceIf(ctx, key, data, v.gen, v.data)
	if errors.Is(err, blobx.ErrConflict) {
		return fmt.Errorf("%s: %w", key, ErrChanged)
	}
	if err != nil {
		return fmt.Errorf("replacing %s: %w", key, err)
	}
	return nil
}
