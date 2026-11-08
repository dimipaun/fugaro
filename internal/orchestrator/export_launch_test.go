package orchestrator

// The windows the spawn caps count over. They are exposed so the launcher's
// tests place an intent inside the window a case is about rather than at an
// offset beside it: a window that moved would move the tests with it, and a
// test that means to sit just inside the hour has to know how long it is.
const (
	WindowMinute = windowMinute
	WindowHour   = windowHour
	WindowDay    = windowDay
)

// BreakerStreak is how many cycles in a row the breaker has counted, and 0
// when it is counting none.
func BreakerStreak(b *Breaker) int { return b.consecutive }
