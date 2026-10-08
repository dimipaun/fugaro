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
// the run's (Deps.Now), so tests drive it with a fake one.
type checkpointSchedule struct {
	tip      string    // the newest unpushed tip seen
	since    time.Time // when tip was first seen
	firstNew time.Time // when the oldest unpushed tip was first seen
	lastPush time.Time // the last successful push
	failedAt time.Time // the last failed push, zero after a success
}

// due reports whether tip, polled at now, is to be pushed; pushed is the
// run's pushed_head.
func (s *checkpointSchedule) due(now time.Time, tip, pushed string) bool {
	if tip == pushed {
		s.tip, s.since, s.firstNew = "", time.Time{}, time.Time{}
		return false
	}
	if s.tip == "" {
		s.firstNew = now
	}
	if tip != s.tip {
		s.tip, s.since = tip, now
	}
	switch {
	case !s.failedAt.IsZero():
		return now.Sub(s.failedAt) >= checkpointFallback
	case !s.lastPush.IsZero() && now.Sub(s.lastPush) < checkpointMinGap:
		return false
	}
	return now.Sub(s.since) >= checkpointQuiet || now.Sub(s.firstNew) >= checkpointFallback
}

// pushed records a successful push at now; failed a failed one.
func (s *checkpointSchedule) pushed(now time.Time) {
	*s = checkpointSchedule{lastPush: now}
}

func (s *checkpointSchedule) failed(now time.Time) { s.failedAt = now }
