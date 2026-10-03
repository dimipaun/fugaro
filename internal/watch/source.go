package watch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Options tunes a Supervisor; the zero value is the production setting.
type Options struct {
	Poll     bool          // --poll: never stream, read by GET
	Interval time.Duration // polling period (default 5 s)
	Tick     time.Duration // clock period (default 1 s)
	SSERetry time.Duration // how often polling tries the stream again (default 60 s)
	// Now replaces the clock (tests). Default: the database's clock
	// (Client.ServerNow), else the local one.
	Now func() time.Time
}

// Supervisor owns the four streams of one project's database and turns them
// into Updates. It does not interpret them: the model folds them into a State
// (State.Handle) and builds a View at Update.Now.
type Supervisor struct {
	db   *rtdb.Client
	o    Options
	out  chan Update
	ctx  context.Context
	fail int // consecutive error events with the same cause
	last string
}

// Start begins supervising db until ctx ends. Updates is closed once every
// goroutine of the supervisor has stopped.
func Start(ctx context.Context, db *rtdb.Client, o Options) *Supervisor {
	if o.Interval <= 0 {
		o.Interval = 5 * time.Second
	}
	if o.Tick <= 0 {
		o.Tick = time.Second
	}
	if o.SSERetry <= 0 {
		o.SSERetry = 60 * time.Second
	}
	s := &Supervisor{db: db, o: o, out: make(chan Update, 64), ctx: ctx}
	go func() {
		defer close(s.out)
		s.run()
	}()
	return s
}

// Updates is the message stream; it is closed after cancel, once everything
// has stopped.
func (s *Supervisor) Updates() <-chan Update { return s.out }

func (s *Supervisor) now() time.Time {
	if s.o.Now != nil {
		return s.o.Now().UTC()
	}
	if t, ok := s.db.ServerNow(); ok {
		return t
	}
	return time.Now().UTC()
}

func (s *Supervisor) emit(u Update) bool {
	u.Now = s.now()
	select {
	case s.out <- u:
		return true
	case <-s.ctx.Done():
		return false
	}
}

type phase int

const (
	phaseStream phase = iota
	phaseTrial        // stream again from polling: back to polling on the first error
	phasePoll
	phaseHalt // refused or withdrawn: no more retries, the clock keeps running
	phaseDone
)

func (s *Supervisor) run() {
	day := budget.Day(s.now())
	if !s.emit(Update{Kind: UpdDay, Day: day}) {
		return
	}
	ph := phaseStream
	if s.o.Poll {
		ph = phasePoll
	}
	for s.ctx.Err() == nil && ph != phaseDone {
		switch ph {
		case phaseStream, phaseTrial:
			ph = s.streamPhase(&day, ph == phaseTrial)
		case phasePoll:
			ph = s.pollPhase(&day)
		case phaseHalt:
			ph = s.haltPhase(&day)
		}
	}
}

// rollDay emits UpdDay when the clock crossed UTC midnight; true if it did.
func (s *Supervisor) rollDay(day *int64) (changed, ok bool) {
	d := budget.Day(s.now())
	if d == *day {
		return false, true
	}
	*day = d
	return true, s.emit(Update{Kind: UpdDay, Day: d})
}

func (s *Supervisor) paths(day int64) [4]string {
	return [4]string{"config", budget.PathSpendGlobal(day), "spend/" + budget.DayKey(day) + "/repos", "agents"}
}

type tagged struct {
	src Source
	ev  rtdb.Event
}

// cause names why an error event happened, ignoring which path it was on.
func cause(err error) string {
	var e *rtdb.Error
	if errors.As(err, &e) {
		return e.Op + " " + e.Msg + " " + string(rune('0'+e.Status/100))
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Supervisor) streamPhase(day *int64, trial bool) phase {
	sctx, cancel := context.WithCancel(s.ctx)
	in := make(chan tagged)
	var all, dayWG sync.WaitGroup
	defer func() { cancel(); all.Wait() }()

	open := func(ctx context.Context, wg *sync.WaitGroup, src Source, path string) {
		ch := s.db.Stream(ctx, path)
		wg.Add(1)
		all.Add(1)
		go func() {
			defer wg.Done()
			defer all.Done()
			// Drain until the stream closes, so its goroutine is gone
			// when the wait returns.
			for ev := range ch {
				if ctx.Err() != nil {
					continue
				}
				select {
				case in <- tagged{src, ev}:
				case <-ctx.Done():
				}
			}
		}()
	}
	var dayCancel context.CancelFunc
	openDay := func(d int64) {
		var dctx context.Context
		dctx, dayCancel = context.WithCancel(sctx)
		p := s.paths(d)
		open(dctx, &dayWG, SrcGlobal, p[1])
		open(dctx, &dayWG, SrcRepos, p[2])
	}
	p := s.paths(*day)
	var base sync.WaitGroup
	open(sctx, &base, SrcConfig, p[0])
	open(sctx, &base, SrcAgents, p[3])
	openDay(*day)

	tick := time.NewTicker(s.o.Tick)
	defer tick.Stop()
	s.fail, s.last = 0, ""
	for {
		select {
		case <-s.ctx.Done():
			return phaseDone
		case <-tick.C:
			if !s.emit(Update{Kind: UpdTick}) {
				return phaseDone
			}
			if d := budget.Day(s.now()); d != *day {
				dayCancel()
				dayWG.Wait() // the old pair is closed before the new one opens
				if _, ok := s.rollDay(day); !ok {
					return phaseDone
				}
				openDay(d)
			}
		case m := <-in:
			if !s.emit(Update{Kind: UpdEvent, Src: m.src, Ev: m.ev}) {
				return phaseDone
			}
			switch m.ev.Type {
			case "cancel":
				return phaseHalt
			case "error":
				if errors.Is(m.ev.Err, rtdb.ErrPermission) {
					return phaseHalt
				}
				if trial {
					return phasePoll
				}
				c := cause(m.ev.Err)
				if c == s.last {
					s.fail++
				} else {
					s.fail, s.last = 1, c
				}
				if s.fail >= 3 {
					s.fail = 0
					if s.probe(*day) {
						return phasePoll
					}
				}
			default:
				s.fail, s.last = 0, ""
				if trial {
					trial = false
					if !s.emit(Update{Kind: UpdPolling, On: false}) {
						return phaseDone
					}
				}
			}
		}
	}
}

// probe is whether a plain GET of the four paths works.
func (s *Supervisor) probe(day int64) bool {
	for _, p := range s.paths(day) {
		var raw json.RawMessage
		if _, err := s.db.Get(s.ctx, p, &raw); err != nil {
			return false
		}
	}
	return true
}

// pollOnce reads the four paths and delivers each as a put of "/". A failed
// read is delivered as an error event. It reports whether to stop retrying.
func (s *Supervisor) pollOnce(day int64) (halt, done bool) {
	for i, p := range s.paths(day) {
		var raw json.RawMessage
		found, err := s.db.Get(s.ctx, p, &raw)
		if s.ctx.Err() != nil {
			return false, true
		}
		var ev rtdb.Event
		switch {
		case err != nil:
			ev = rtdb.Event{Type: "error", Err: err}
		case !found:
			ev = rtdb.Event{Type: "put", Path: "/", Data: json.RawMessage("null")}
		default:
			ev = rtdb.Event{Type: "put", Path: "/", Data: raw}
		}
		if !s.emit(Update{Kind: UpdEvent, Src: Source(i), Ev: ev}) {
			return false, true
		}
		if err != nil {
			return errors.Is(err, rtdb.ErrPermission), false
		}
	}
	return false, false
}

func (s *Supervisor) pollPhase(day *int64) phase {
	if !s.emit(Update{Kind: UpdPolling, On: true}) {
		return phaseDone
	}
	tick := time.NewTicker(s.o.Tick)
	defer tick.Stop()
	poll := time.NewTimer(0)
	defer poll.Stop()
	retry := time.NewTimer(s.o.SSERetry)
	defer retry.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return phaseDone
		case <-tick.C:
			if !s.emit(Update{Kind: UpdTick}) {
				return phaseDone
			}
		case <-poll.C:
			if _, ok := s.rollDay(day); !ok {
				return phaseDone
			}
			halt, done := s.pollOnce(*day)
			if done {
				return phaseDone
			}
			if halt {
				return phaseHalt
			}
			poll.Reset(s.o.Interval)
		case <-retry.C:
			if s.o.Poll { // --poll forces polling: never stream
				retry.Reset(s.o.SSERetry)
				continue
			}
			return phaseTrial
		}
	}
}

// haltPhase keeps the clock running (the view keeps ageing) with nothing else.
func (s *Supervisor) haltPhase(day *int64) phase {
	tick := time.NewTicker(s.o.Tick)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return phaseDone
		case <-tick.C:
			if !s.emit(Update{Kind: UpdTick}) {
				return phaseDone
			}
		}
	}
}
