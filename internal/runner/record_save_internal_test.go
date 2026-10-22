package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRecordCredentialSaveIsRaceFree (C1.19's follow-up; run with -race): the
// credentials service's answer writes the record from the gateway's per-call
// goroutine (r.recordCredential, and the switch C1.16 logs with it), while a
// save encodes the whole record from the run's own goroutine — the stage
// boundary, the final write, and the mid-stage checkpoint poll, which is a
// goroutine of its own. Every one of those encodes reads every field of the
// record, so a write the callback makes without the run's lock is a data race
// on the record the run publishes.
//
// The test drives exactly that pair: one goroutine names credentials as fast as
// the service could, the other saves as often as the run does. Under -race it
// is the write of OAuthAccount (and the switch appended beside it) against the
// encode that would read it; the last save after the writer stops is what says
// the record still carries the name.
func TestRecordCredentialSaveIsRaceFree(t *testing.T) {
	const runID = "20261022-120000-abcd"
	bucket := memblob.OpenBucket(nil)
	defer bucket.Close()
	r := &run{
		d:     Deps{Store: runstore.Open(bucket, "acme-app", runID), Log: slog.New(slog.DiscardHandler)},
		rec:   &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Status: runstore.StatusRunning},
		owned: true,
	}
	// One name before anything is concurrent, so the record this test reads
	// back carries one whatever the scheduler did with the writer below.
	r.recordCredential("repo-shared-b")

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			r.recordCredential(fmt.Sprintf("repo-shared-%d", i))
		}
	}()
	for i := 0; i < 100; i++ {
		r.save(context.Background())
	}
	close(done)
	wg.Wait()
	r.save(context.Background())

	rec, err := r.d.Store.ReadRecord(context.Background())
	if err != nil {
		t.Fatalf("reading the record the run wrote: %v", err)
	}
	if !strings.HasPrefix(rec.OAuthAccount, "repo-shared-") {
		t.Fatalf("the written record names the credential %q, want the last one the service answered with", rec.OAuthAccount)
	}
}
