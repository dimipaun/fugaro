package budget

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// killNode decodes a kill switch for a running run: a node that exists but
// does not decode (a wrong type, a stray scalar) counts as ON, because the
// reason an admin wrote it is lost but the intent to stop is not. A switch
// nobody can read must not be one nobody honours.
type killNode struct{ Kill }

func (k *killNode) UnmarshalJSON(b []byte) error {
	type plain Kill
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		k.Kill = Kill{On: true, Reason: "unreadable switch"}
		return nil
	}
	k.Kill = Kill(p)
	return nil
}

// checkKills reads the two switches this run honours and halts the run if one
// is on. Its outcome feeds the grace under source.
func (s *Session) checkKills(ctx context.Context, source string) {
	var kg, kr killNode
	fg, err1 := s.db.Get(ctx, PathKillGlobal, &kg)
	fr, err2 := s.db.Get(ctx, PathKillRepo(s.cfg.Slug), &kr)
	if err := errors.Join(err1, err2); err != nil {
		s.observe(source, firstClassified([]error{err1, err2}))
		return
	}
	s.dbOK()
	s.grace.OK("kill-poll") // only a successful kill read ends this clock
	var k Kills
	if fg {
		k.Global = &kg.Kill
	}
	if fr {
		k.Repo = &kr.Kill
	}
	if h := killHalt(k); h != nil {
		s.halt(*h)
	}
}

// watchKills listens to the two switches (R8, design §5.10). Rules let a run
// read exactly these two nodes, not /config/kill as a whole, so there are two
// streams. A stream's event is only a prompt: the node is read afresh, so
// nothing depends on parsing partial updates. A stream that fails starts the
// grace; the heartbeat's poll is the backstop for one that is silently stale.
func (s *Session) watchKills(ctx context.Context) {
	for _, w := range []struct{ path, source string }{
		{PathKillGlobal, "kill-global"},
		{PathKillRepo(s.cfg.Slug), "kill-repo"},
	} {
		s.goSafely(ctx, w.source, func() { s.watchKill(ctx, w.path, w.source) })
	}
}

func (s *Session) watchKill(ctx context.Context, path, source string) {
	for ctx.Err() == nil {
		for ev := range s.db.Stream(ctx, path) {
			switch ev.Type {
			case "error":
				if ev.Err != nil {
					s.streamFailed(source, ev.Err)
				}
			case "put", "patch":
				s.grace.OK(source)
				s.safely(source, func() { s.checkKills(ctx, "kill-poll") })
			default: // keep-alive, auth_revoked: the server is there
				s.grace.OK(source)
			}
		}
		// The channel closes when the server withdrew the listen (cancel) or
		// the context ended. The poll covers meanwhile; listen again later.
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cfg.KillRestart):
		}
	}
}

// SetAttempts bounds the ETag retries of one SetKill (and of the CLI's other
// conditional writes).
const SetAttempts = 5

// KillDB is the part of the database SetKill needs; *rtdb.Client is one.
type KillDB interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// ErrKillContention: the switch kept changing under SetAttempts writes.
var ErrKillContention = errors.New("the kill switch kept changing under the write")

// KillError is a failed read or write of a switch, so a caller can word a
// refusal (a write the database denied) differently from a refused read.
type KillError struct {
	Write bool
	Err   error
}

func (e *KillError) Error() string { return e.Err.Error() }
func (e *KillError) Unwrap() error { return e.Err }

// KillHooks lets a caller act between SetKill's steps. Both may be nil.
type KillHooks struct {
	// Confirm runs after the current value is read and found to differ from
	// the wanted state, before every write attempt. Its error aborts SetKill
	// and is returned unchanged; nothing is written.
	Confirm func(prev Kill) error
	// Conflict runs when the write lost an ETag race, before the re-read.
	Conflict func()
	// Now stamps the switch; nil is time.Now.
	Now func() time.Time
}

// SetResult says what SetKill did.
type SetResult struct {
	Written  bool // the node was written
	Already  bool // it was already in the wanted state; nothing was written
	Previous Kill // the node as last read
	Wrote    Kill // what was written, when Written
}

// SetKill turns the switch at path (PathKillGlobal or PathKillRepo) on or
// off, recording by, the time and reason. It is the one writer of a switch:
// the CLI and the watch TUI both call it, so they cannot diverge. A switch
// already in the wanted state is a no-op, not a rewrite (it would erase who
// first set it). The write is conditional on the ETag read; a lost race is
// re-read up to SetAttempts times.
func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {
	now := h.Now
	if now == nil {
		now = time.Now
	}
	for attempt := 1; attempt <= SetAttempts; attempt++ {
		var cur Kill
		etag, _, err := db.GetETag(ctx, path, &cur)
		if err != nil {
			return SetResult{}, &KillError{Err: err}
		}
		if cur.On == on {
			return SetResult{Already: true, Previous: cur}, nil
		}
		if h.Confirm != nil {
			if err := h.Confirm(cur); err != nil {
				return SetResult{Previous: cur}, err
			}
		}
		next := Kill{On: on, By: by, At: now().UnixMilli(), Reason: reason}
		err = db.PutIfMatch(ctx, path, etag, next)
		if errors.Is(err, rtdb.ErrPrecondition) {
			if h.Conflict != nil {
				h.Conflict()
			}
			continue
		}
		if err != nil {
			return SetResult{Previous: cur}, &KillError{Write: true, Err: err}
		}
		return SetResult{Written: true, Previous: cur, Wrote: next}, nil
	}
	return SetResult{}, ErrKillContention
}

// AdminDeniedText is what a refused write of a cap or a kill switch means
// (D6), shared by the CLI and watch.
func AdminDeniedText(project string) string {
	return "you are not a budget admin for project " + project + ": only the GCP project's owners and editors and terraform.budget_admins may change caps and kill switches; ask one of them, or to be added"
}
