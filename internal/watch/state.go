// Package watch is the core of `fugaro watch`: a pure model of the budget
// database as one live view. State folds the four event streams into trees
// (rtdb.Tree), tracks the connection and the burn-rate samples; Build turns
// a State into a typed View. Nothing here touches the network, the terminal
// or the clock: callers pass the time in, so everything is deterministic.
package watch

import (
	"errors"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Source names one of the four streams.
type Source int

const (
	SrcConfig Source = iota // /config
	SrcGlobal               // /spend/<today>/global
	SrcRepos                // /spend/<today>/repos
	SrcAgents               // /agents
)

const (
	// StaleAfter is the silence (keep-alives count as events) after which
	// the view says its data is stale: keep-alives come about every 30 s.
	StaleAfter = 45 * time.Second
	// BurnWindow is the rolling window of the burn rate.
	BurnWindow = 5 * time.Minute
	// BurnWarmup is how long a window must span before a rate is shown.
	BurnWarmup = 60 * time.Second
)

// ConnKind is how trustworthy the view is.
type ConnKind int

const (
	ConnLive ConnKind = iota
	ConnStale
	ConnOffline
	ConnRefused // permission error: the person lacks the Viewer role
	ConnPolling
)

// sample is one reading of spent+notional.
type sample struct {
	at time.Time
	v  budget.Micros
}

// State is everything the view is built from.
type State struct {
	Config, Global, Repos, Agents rtdb.Tree

	// Day is the UTC epoch day the two spend streams show (0 until set).
	Day int64

	// Polling is set by the supervisor while it reads by GET instead of SSE.
	Polling bool

	lastEvent time.Time // any event, keep-alives included
	err       error     // set by an error event, cleared by any other
	cancelled bool

	burn map[string][]sample // "" is the project, otherwise the wire slug
}

// NewState is an empty state.
func NewState() *State { return &State{burn: map[string][]sample{}} }

// Apply folds one event of src in at now. A failed event data parse is
// returned and leaves the tree as it was; the event still counts as proof of
// life.
func (s *State) Apply(src Source, ev rtdb.Event, now time.Time) error {
	switch ev.Type {
	case "error":
		s.err = ev.Err
		if s.err == nil {
			s.err = errors.New("connection error")
		}
		return nil
	case "cancel":
		s.cancelled = true
		s.err = errors.New("the server withdrew the listen")
		return nil
	}
	s.lastEvent, s.err, s.cancelled = now, nil, false
	switch ev.Type {
	case "put", "patch":
		if err := s.tree(src).Apply(ev.Path, ev.Data, ev.Type == "patch"); err != nil {
			return err
		}
		if src == SrcGlobal || src == SrcRepos {
			s.observe(now)
		}
	}
	return nil
}

func (s *State) tree(src Source) *rtdb.Tree {
	switch src {
	case SrcConfig:
		return &s.Config
	case SrcGlobal:
		return &s.Global
	case SrcRepos:
		return &s.Repos
	}
	return &s.Agents
}

// SetDay starts a new day: the two spend trees and the burn windows are
// emptied (the counters restart at zero, a negative slope is not a rate).
func (s *State) SetDay(day int64) {
	if s.Day == day {
		return
	}
	s.Day = day
	s.Global.Reset()
	s.Repos.Reset()
	s.burn = map[string][]sample{}
}

// Reconnected empties the burn windows: after a (re)connect they start
// empty, so the first 60 s show no rate. The trees are rebuilt by the
// connection's first put of "/".
func (s *State) Reconnected() { s.burn = map[string][]sample{} }

// observe records a burn sample for the project and every repository.
func (s *State) observe(now time.Time) {
	var g budget.Counters
	s.Global.Decode("/", &g) // a bad node reads as zero
	s.record("", now, g.Spent+g.Notional)
	for _, slug := range s.Repos.Keys("/") {
		var c budget.Counters
		s.Repos.Decode(slug, &c)
		s.record(slug, now, c.Spent+c.Notional)
	}
}

func (s *State) record(key string, now time.Time, v budget.Micros) {
	if s.burn == nil {
		s.burn = map[string][]sample{}
	}
	w := s.burn[key]
	if n := len(w); n > 0 {
		last := w[n-1]
		if v < last.v { // a counter went back: a reset, not negative spend
			w = nil
		} else if v == last.v && !now.Before(last.at) && n > 1 && w[n-2].v == v {
			// extend a flat run in place so a quiet counter keeps few samples
			w[n-1].at = now
			s.burn[key] = w
			return
		}
	}
	w = append(w, sample{now, v})
	cut := now.Add(-BurnWindow)
	i := 0
	for i < len(w)-1 && w[i].at.Before(cut) {
		i++
	}
	s.burn[key] = append([]sample(nil), w[i:]...)
}

// Burn is the spend rate of the project ("" slug) or one repository (wire
// slug) in micro-dollars per minute, over the window ending at now. ok is
// false until the window spans BurnWarmup. The rate is never negative.
func (s *State) Burn(key string, now time.Time) (perMin budget.Micros, ok bool) {
	w := s.burn[key]
	cut := now.Add(-BurnWindow)
	for len(w) > 1 && w[0].at.Before(cut) {
		w = w[1:]
	}
	if len(w) == 0 {
		return 0, false
	}
	first, last := w[0], w[len(w)-1]
	span := now.Sub(first.at)
	if span < BurnWarmup || last.v < first.v {
		return 0, false
	}
	return budget.Micros(float64(last.v-first.v) * float64(time.Minute) / float64(span)), true
}

// Conn is the connection as of now.
func (s *State) conn(now time.Time) Connection {
	c := Connection{Kind: ConnLive}
	switch {
	case s.err != nil && errors.Is(s.err, rtdb.ErrPermission):
		c.Kind, c.Reason = ConnRefused, "access refused"
	case s.err != nil:
		c.Kind, c.Reason = ConnOffline, clip(oneLine(s.err.Error()))
	case s.lastEvent.IsZero():
		c.Kind, c.Reason = ConnOffline, "connecting"
	case now.Sub(s.lastEvent) > StaleAfter:
		c.Kind = ConnStale
	case s.Polling:
		c.Kind = ConnPolling
	}
	if !s.lastEvent.IsZero() {
		c.Age = max(now.Sub(s.lastEvent), 0)
	}
	return c
}
