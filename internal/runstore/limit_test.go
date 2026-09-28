package runstore

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// TestReadsAreCapped pins S-M4: the job's service account (and so the
// agent) can write every object here, so a hostile one of several GB
// must fail with ErrTooLarge rather than exhaust the operator's memory.
func TestReadsAreCapped(t *testing.T) {
	s := newStore(t)
	huge := []byte(`{"version":1,"run_id":"` + runID + `","pad":"` + strings.Repeat("x", MaxRecordBytes) + `"}`)
	for _, name := range []string{"result.json", "launch.json", "task.json", "launching"} {
		if err := s.PutFile(ctx, name, huge, "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	reads := map[string]func() error{
		"ReadRecord": func() error { _, err := s.ReadRecord(ctx); return err },
		"ReadLaunch": func() error { _, err := s.ReadLaunch(ctx); return err },
		"ReadTask":   func() error { _, err := s.ReadTask(ctx); return err },
		"ReadClaim":  func() error { _, err := s.ReadClaim(ctx); return err },
		"ReadFile":   func() error { _, err := s.ReadFile(ctx, "task.json"); return err },
		"ReadRecordVersion": func() error {
			_, _, err := ReadRecordVersion(ctx, blobx.Wrap(s.bucket), s.Slug(), runID)
			return err
		},
	}
	for name, read := range reads {
		if err := read(); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s of an oversized object = %v, want ErrTooLarge", name, err)
		}
	}
}

// TestTranscriptsHaveALargerCap: a transcript may be well past a record's
// cap, but not past its own.
func TestTranscriptsHaveALargerCap(t *testing.T) {
	s := newStore(t)
	big := bytes.Repeat([]byte("y"), 2*MaxRecordBytes)
	if err := s.PutFile(ctx, "transcripts/implement-1.jsonl", big, "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	if data, err := s.ReadFile(ctx, "transcripts/implement-1.jsonl"); err != nil || len(data) != len(big) {
		t.Fatalf("ReadFile of a 2 MiB transcript = %d bytes, %v", len(data), err)
	}
	if err := s.PutFile(ctx, "transcripts/review-1.jsonl", make([]byte, MaxTranscriptBytes+1), "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadFile(ctx, "transcripts/review-1.jsonl"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadFile of an oversized transcript = %v, want ErrTooLarge", err)
	}
}
