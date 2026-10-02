package budget

import (
	"context"
	"errors"
	"time"
)

// checkKills reads the two switches this run honours and halts the run if one
// is on. Its outcome feeds the grace under source.
func (s *Session) checkKills(ctx context.Context, source string) {
	var kg, kr Kill
	fg, err1 := s.db.Get(ctx, PathKillGlobal, &kg)
	fr, err2 := s.db.Get(ctx, PathKillRepo(s.cfg.Slug), &kr)
	if err := errors.Join(err1, err2); err != nil {
		s.observe(source, firstClassified([]error{err1, err2}))
		return
	}
	s.dbOK()
	var k Kills
	if fg {
		k.Global = &kg
	}
	if fr {
		k.Repo = &kr
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
