package budget

import (
	"context"
	"sort"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// LiveDays computes, read-only, the DayRecords of the given days from the
// Realtime Database with Rollup, as the rollover would, for the days that are
// not archived yet (today and any day without a final document). A day with
// no node in the database has no entry. facts may be nil (compute and run
// hours then read as "not estimated"); a facts failure is reported through
// warn and the days are computed without them. Nothing is written.
func LiveDays(ctx context.Context, db *rtdb.Client, facts Facts, days []int64, warn func(string)) (map[int64][]DayRecord, error) {
	out := map[int64][]DayRecord{}
	if len(days) == 0 {
		return out, nil
	}
	days = append([]int64(nil), days...)
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	r := &Roller{DB: db, Facts: facts, Warn: warn}
	var caps struct {
		Defaults *DefaultCaps        `json:"defaults"`
		Repos    map[string]RepoCaps `json:"repos"`
	}
	if _, err := db.Get(ctx, "config/caps", &caps); err != nil {
		return nil, err
	}
	snap, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var factsByDay map[int64][]RunFact
	repoNames := map[string]string{}
	if facts != nil {
		var ferr error
		if factsByDay, repoNames, ferr = facts.Facts(ctx, days[0]-1, days[len(days)-1]); ferr != nil {
			r.warnf("run facts unavailable, so compute and run hours are n/a: %v", ferr)
			factsByDay, repoNames = nil, map[string]string{}
		}
	}
	for _, d := range days {
		if !snap.present[d] {
			continue
		}
		dayFacts := append(append([]RunFact(nil), factsByDay[d-1]...), factsByDay[d]...)
		in, err := r.input(d, snap.spend, snap.outcomes, snap.runs, snap.last, caps.Defaults, caps.Repos, dayFacts, repoNames)
		if err != nil {
			return nil, err
		}
		if recs := Rollup(in); len(recs) > 0 {
			out[d] = recs
		}
	}
	return out, nil
}
