package cli

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/watch"
)

// The paused subtree, read for the frame: what each paused run's own pause says
// about it (contract-pause, R15; model-credentials-routing PR7).
//
// A paused run's row is a finished row — its registry entry is gone, so the
// bucket scan finds it like any other run that is over — and the run's own
// record carries the word and nothing else. The two facts a reader wants beside
// it are elsewhere: the instant the resume is due at is the credentials
// service's node for the pause (`paused/<slug>/<run>`, written before the run
// ends paused), and the run that resumed it is another run's task naming this
// one under `resumes` (which the bucket scan already reads).
//
// So this is a second reader beside the bucket scan, and it is a background one
// for the same reason the scan is: the render path asks for the map on every
// frame, and no frame may wait on the network. It reads the whole subtree in
// one GET — it is small, one node per pause that is standing, and a per-run
// read would be one request per paused row on every poll — and joins it to the
// scan's facts (watch.PausesOf).

// pausePollEvery is how often the paused subtree is read. It is well under the
// minute a displayed resume instant is rounded to, and the read is one GET of a
// subtree that holds one small node per standing pause.
const pausePollEvery = 15 * time.Second

// pauseStepTimeout bounds one read of the subtree: past it the read is abandoned
// and the last good map is kept, so a database that has gone quiet never holds
// the reader's goroutine.
const pauseStepTimeout = 10 * time.Second

// readPauseNodes reads the paused subtree: the instant each pause is due to
// resume at, by the run's key. A node this release cannot read is left out
// rather than failing the read — the run's own record still says it is paused,
// so the frame loses a fact and claims nothing false — and an empty subtree is
// no pauses rather than an error.
func readPauseNodes(ctx context.Context, db *rtdb.Client) (map[watch.RunKey]time.Time, error) {
	var raw json.RawMessage
	found, err := db.Get(ctx, runstore.PauseNodeRoot, &raw)
	if err != nil {
		return nil, err
	}
	if !found || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var bySlug map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bySlug); err != nil {
		return nil, err
	}
	out := make(map[watch.RunKey]time.Time, len(bySlug))
	for slug, runs := range bySlug {
		for run, node := range runs {
			p, err := runstore.ParsePauseNode(node)
			if err != nil {
				continue
			}
			out[watch.PauseKeyOf(slug, run)] = time.UnixMilli(p.ResumeAt).UTC()
		}
	}
	return out, nil
}

// pauseSource reads the paused subtree on its own goroutine and clock and
// caches the pauses a frame stamps onto its rows, exactly as queuedSource does
// for the bucket: the render loops call Get on every frame and it never blocks
// on the network, and the first frame never waits for the first read.
//
// A read that fails keeps the map from the last good one: a pause that is still
// standing keeps the instant and the resumer it had, which is what the run's own
// record still says, and dropping them would make a screen blink for a database
// that missed one poll.
type pauseSource struct {
	db *rtdb.Client
	// facts is the bucket scan's latest map, asked for on every refresh: the
	// resumer of a pause is a run the scan read, so the two readers are joined
	// here rather than by a third one that would have to hold both.
	facts func() watch.Facts
	wg    sync.WaitGroup

	mu     sync.Mutex
	pauses watch.Pauses
}

// startPauseSource starts reading at once, then every pausePollEvery until ctx
// ends. The caller's Wait, after ctx is cancelled, returns promptly: the read
// runs under its own timeout and stops when ctx ends.
func startPauseSource(ctx context.Context, db *rtdb.Client, facts func() watch.Facts) *pauseSource {
	ps := &pauseSource{db: db, facts: facts}
	ps.wg.Add(1)
	go func() {
		defer ps.wg.Done()
		for {
			ps.refresh(ctx)
			t := time.NewTimer(pausePollEvery)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	return ps
}

func (ps *pauseSource) refresh(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, pauseStepTimeout)
	defer cancel()
	nodes, err := readPauseNodes(rctx, ps.db)
	if ctx.Err() != nil {
		return // shutting down: nothing will render it
	}
	if err != nil {
		return // the last good map stands
	}
	var facts watch.Facts
	if ps.facts != nil {
		facts = ps.facts()
	}
	pauses := watch.PausesOf(nodes, facts)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pauses = pauses
}

// Get is the latest pauses, safe to call on every frame: it never blocks on the
// network. Nil until the first read lands, which MergePaused reads as "no pause
// to stamp".
func (ps *pauseSource) Get() watch.Pauses {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.pauses
}

// Wait blocks until the background reader has stopped (ctx must already be
// done, or this never returns).
func (ps *pauseSource) Wait() { ps.wg.Wait() }
