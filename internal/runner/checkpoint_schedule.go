package runner

import "time"

// The checkpoint schedule (design checkpoint-pushes.md): when a polled
// branch tip is pushed. checkpoint.go runs it.

const (
	// checkpointPoll is how often a running stage's branch tip is read. The
	// read is local (no network), so it costs a few git commands.
	checkpointPoll = 10 * time.Second
	// checkpointQuiet is how long a new tip must stay unchanged before it
	// is pushed, so a burst of commits is one push of the latest.
	checkpointQuiet = 5 * time.Second
	// checkpointMinGap is the least time between two pushes while a stage
	// runs: at most one push a minute, and a commit inside the window is
	// pushed when it ends. Stage boundaries are exempt.
	checkpointMinGap = time.Minute
	// checkpointFallback is the slow path: the retry after a failed push,
	// and the most a tip waits when the agent never stops committing long
	// enough for checkpointQuiet.
	checkpointFallback = 3 * time.Minute
	// checkpointOpenTries bounds how many checkpoint pushes try to open the
	// draft; after that the verified boundary or finalize does.
	checkpointOpenTries = 2
)

// checkpointSchedule decides when a polled tip is pushed. Its clock is
// the run's (Deps.Now), so tests drive it with a fake one. It tolerates a
// clock that steps backwards: a time in the future counts as long past.
type checkpointSchedule struct {
	base     string    // the stage's base commit: never pushed as a checkpoint
	tip      string    // the newest unpushed tip seen
	since    time.Time // when tip was first seen
	firstNew time.Time // when the oldest unpushed tip was first seen
	lastPush time.Time // the last successful push
	failedAt time.Time // the last failed push, zero after a success
}

// newCheckpointSchedule starts a schedule for a stage that begins at base.
// pushed_head is empty before the first push, so the base is held here: a
// tip equal to it has no work of the agent's on it and is never due.
func newCheckpointSchedule(base string) *checkpointSchedule {
	return &checkpointSchedule{base: base}
}

// waited reports whether at least d has passed since t; a t after now (the
// clock stepped back) counts as passed.
func waited(now, t time.Time, d time.Duration) bool {
	e := now.Sub(t)
	return e < 0 || e >= d
}

// due reports whether tip, polled at now, is to be pushed; pushed is the
// run's pushed_head, which is empty before the first push. A tip equal to
// pushed or to the base is never due.
func (s *checkpointSchedule) due(now time.Time, tip, pushed string) bool {
	if tip == pushed || tip == s.base {
		s.tip, s.since, s.firstNew, s.failedAt = "", time.Time{}, time.Time{}, time.Time{}
		return false
	}
	if now.Before(s.since) || now.Before(s.firstNew) {
		// The clock stepped back: the quiet period starts again.
		s.tip, s.since, s.firstNew = "", time.Time{}, time.Time{}
	}
	if s.tip == "" {
		s.firstNew = now
	}
	if tip != s.tip {
		s.tip, s.since = tip, now
	}
	switch {
	case !s.failedAt.IsZero():
		return waited(now, s.failedAt, checkpointFallback)
	case !s.lastPush.IsZero() && !waited(now, s.lastPush, checkpointMinGap):
		return false
	}
	return now.Sub(s.since) >= checkpointQuiet || now.Sub(s.firstNew) >= checkpointFallback
}

// pushed records a successful push at now; failed a failed one.
func (s *checkpointSchedule) pushed(now time.Time) {
	*s = checkpointSchedule{base: s.base, lastPush: now}
}

func (s *checkpointSchedule) failed(now time.Time) { s.failedAt = now }
