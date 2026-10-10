package watch

import (
	"sort"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Thresholds (fixed, design §6.5; not configuration).
const (
	SilentAfter = 60 * time.Second
	LostAfter   = 180 * time.Second
	// NearDeadline is the fraction of a stage's time after which it is amber.
	NearDeadline = 0.8
	// maxText is the clip of every database-derived string, in runes.
	maxText = 200
	// DefaultBurnHours spreads the daily cap into the default burn alert.
	DefaultBurnHours = 8
)

// Config is what Build needs besides the state.
type Config struct {
	// BurnAlertPerHour is watch.burn_alert_usd_per_hour in micro-dollars;
	// nil means the effective daily cap spread over DefaultBurnHours (none
	// when there is no cap).
	BurnAlertPerHour *budget.Micros
	// RepoNames maps a repository's wire slug key to the readable name the
	// local config gives it (as `fugaro budget show` prints it); a repository
	// not listed is named by its decoded slug.
	RepoNames map[string]string
}

// Connection is the state of the link, for the header.
type Connection struct {
	Kind   ConnKind
	Reason string        // sanitised
	Age    time.Duration // since the last event of any kind (0 before the first)
}

// Bar is a spend-against-cap figure. Only counted (model dollars) feeds it;
// notional dollars count toward no cap.
type Bar struct {
	Has     bool // false: no cap is set ("no cap", never 0%)
	Used    budget.Micros
	Cap     budget.Micros
	Percent float64
}

// Burn is a spend rate.
type Burn struct {
	Known  bool // false for the first BurnWarmup after a (re)connect
	PerMin budget.Micros
	Fast   bool // above the alert
}

// KillState is a kill switch as shown.
type KillState struct {
	On     bool
	By     string // sanitised
	At     time.Time
	Reason string // sanitised
	// Unreadable: the node exists but does not decode. Runs treat that as ON
	// (budget.killNode), so it is shown as on.
	Unreadable bool
}

// ProjectLine is the whole project's totals.
type ProjectLine struct {
	Counted, Spent, Notional budget.Micros
	Bar                      Bar
	Burn                     Burn
	Kill                     KillState
	Runs                     int
	RunHours                 float64 // sum of now - startedAt over the runs
}

// Health is how recently a run reported.
type Health int

const (
	HealthOK Health = iota
	HealthSilent
	HealthLost
)

// Deadline is where a run is against its stage deadline.
type Deadline int

const (
	DeadlineOK Deadline = iota
	DeadlineNear
	DeadlineOver
)

// RunRow is one running agent. Text fields hold "-" when the registry has
// no value; every string is sanitised and clipped.
type RunRow struct {
	Run, Slug string // run id; wire slug
	Title     string
	Stage     string
	Round     string
	Verify    string
	Models    string
	Recipe    string // the run's recipe when not default
	Auth      string
	Notional  bool // auth is oauth: Spent is notional dollars
	Spent     budget.Micros
	HasSpent  bool
	Age       time.Duration
	HasAge    bool
	Health    Health
	Deadline  Deadline
	// DeadlineAt is e.StageDeadline, epoch ms; 0 when the registry has none.
	// The detail view formats it; Deadline above is only the OK/near/over
	// flag the row and the colour of FLAGS use.
	DeadlineAt int64
	Halted     string // reason, "" when not halted
	StartedAt  int64  // epoch ms, for ordering

	// Queued marks a run known only from the runs bucket (a launch claim or
	// launch.json, no registry entry yet): its stage, round and spend are
	// unknown, so only Age, Workflow, Recipe and RequestedBy carry anything.
	// Stuck means it has gone QueuedStuckAfter since launch without a
	// registry entry showing up.
	Queued      bool
	Stuck       bool
	Workflow    string
	RequestedBy string

	// Finished marks a row built from the runs bucket's result.json, not
	// RTDB: the run's registry entry is already gone (design generic-tool
	// §10.1). Outcome is the record's outcome (ready, draft or none);
	// Failed is set when Stage's status is not "succeeded"; PRNumber, when
	// the run opened one.
	Finished bool
	Outcome  string
	Failed   bool
	// PRURL is the run's PR link, once it has one: for a live run, from the
	// registry's prUrl (design generic-tool G24); for a finished row, from
	// the runs bucket's result.json. Always an https:// URL or "" (cleanURL).
	PRURL    string
	PRNumber int
}

// RepoBlock is one repository.
type RepoBlock struct {
	Slug                     string // raw wire key, for paths and identity only: never render it (use Name)
	Name                     string // unescaped, sanitised
	Counted, Spent, Notional budget.Micros
	Bar                      Bar
	Burn                     Burn
	Kill                     KillState
	Runs                     []RunRow
	// Finished are rows from the runs bucket, not RTDB (MergeFinished):
	// runs whose registry entry is already gone. Newest first.
	Finished []RunRow
}

// View is the typed snapshot a renderer draws.
type View struct {
	Now     time.Time
	Day     string // YYYY-MM-DD, "" before the first day is set
	Mode    string // observe or enforce
	Conn    Connection
	Project ProjectLine
	Repos   []RepoBlock
	// QueuedNote is a one-line, already-sanitised reason the runs bucket
	// could not be read for queued rows; "" when it was, or when nobody
	// asked (MergeQueued was never called).
	QueuedNote string
}

func oneLine(s string) string { return safetext.Strip(s) }

// clip cuts s to maxText runes (an ellipsis marks the cut).
func clip(s string) string {
	n := 0
	for i := range s {
		if n == maxText {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// clean is a database-derived string made safe: no escapes or controls, one
// line, clipped. dash replaces an empty result when it is "-".
func clean(s string) string { return clip(safetext.Strip(s)) }

func dash(s string) string {
	if s = clean(s); s == "" {
		return "-"
	}
	return s
}

func nonneg(v budget.Micros) budget.Micros { return max(v, 0) }

func counters(get func(*budget.Counters) (bool, error)) budget.Counters {
	var c budget.Counters
	if ok, err := get(&c); !ok || err != nil {
		return budget.Counters{}
	}
	c.Counted, c.Spent, c.Notional = nonneg(c.Counted), nonneg(c.Spent), nonneg(c.Notional)
	return c
}

func bar(used budget.Micros, cap budget.Micros, has bool) Bar {
	if !has {
		return Bar{}
	}
	b := Bar{Has: true, Used: used, Cap: cap, Percent: 100}
	if cap > 0 {
		b.Percent = float64(used) / float64(cap) * 100
	}
	return b
}

func (s *State) kill(path string, now time.Time) KillState {
	var k budget.Kill
	ok, err := s.Config.Decode(path, &k)
	if ok && err != nil {
		return KillState{On: true, Unreadable: true, Reason: "unreadable kill switch (runs treat it as ON)"}
	}
	if !ok || err != nil || !k.On {
		return KillState{}
	}
	return KillState{On: true, By: clean(k.By), At: time.UnixMilli(k.At).UTC(), Reason: clean(k.Reason)}
}

func burnOf(s *State, key string, now time.Time, daily budget.Micros, hasCap bool, cfg Config) Burn {
	per, ok := s.Burn(key, now)
	if !ok {
		return Burn{}
	}
	b := Burn{Known: true, PerMin: per}
	var alert budget.Micros
	switch {
	case cfg.BurnAlertPerHour != nil:
		alert = *cfg.BurnAlertPerHour
	case hasCap:
		alert = daily / DefaultBurnHours
	default:
		return b
	}
	b.Fast = per*60 > alert
	return b
}

// Build is the view of s at now.
func Build(s *State, now time.Time, cfg Config) View {
	v := View{Now: now, Conn: s.conn(now), Mode: budget.ModeObserve}
	if s.Day != 0 {
		v.Day = budget.DayDate(s.Day)
	}
	var mode string
	s.Config.Decode("mode", &mode)
	if mode == budget.ModeEnforce {
		v.Mode = mode
	}

	var caps budget.Caps
	s.Config.Decode("caps/global", &caps.Global) // errors read as an absent node
	s.Config.Decode("caps/defaults", &caps.Defaults)
	gDaily, gHas := caps.GlobalDaily()

	g := counters(func(c *budget.Counters) (bool, error) { return s.Global.Decode("/", c) })
	v.Project = ProjectLine{
		Counted: g.Counted, Spent: g.Spent, Notional: g.Notional,
		Bar:  bar(g.Counted, gDaily, gHas),
		Burn: burnOf(s, "", now, gDaily, gHas, cfg),
		Kill: s.kill(budget.PathKillGlobal[len("config/"):], now),
	}

	// Repositories: anything with an agent, a counter or a switch.
	slugs := map[string]bool{}
	for _, k := range s.Agents.Keys("/") {
		slugs[k] = true
	}
	for _, k := range s.Repos.Keys("/") {
		slugs[k] = true
	}
	for _, k := range s.Config.Keys("kill/repos") {
		if s.kill("kill/repos/"+k, now).On { // an unreadable switch counts as on
			slugs[k] = true
		}
	}

	for slug := range slugs {
		b := RepoBlock{Slug: slug}
		c := counters(func(c *budget.Counters) (bool, error) { return s.Repos.Decode(slug, c) })
		b.Counted, b.Spent, b.Notional = c.Counted, c.Spent, c.Notional
		b.Kill = s.kill("kill/repos/"+slug, now)

		rc := caps
		rc.Repo = nil
		var rcaps budget.RepoCaps
		if ok, err := s.Config.Decode("caps/repos/"+slug, &rcaps); ok && err == nil {
			rc.Repo = &rcaps
		}
		daily, has := rc.RepoDaily()
		b.Bar = bar(b.Counted, daily, has)
		b.Burn = burnOf(s, slug, now, daily, has, cfg)

		for _, run := range s.Agents.Keys(slug) {
			var e budget.AgentEntry
			if ok, err := s.Agents.Decode(slug+"/"+run, &e); !ok || err != nil {
				continue // a malformed entry shows no row
			}
			b.Runs = append(b.Runs, runRow(slug, run, e, now))
		}
		// The name is the wire key decoded: the authenticated identity. The
		// job-written repo field is free text and never names a block.
		// A readable name from the local config (trusted, still sanitised)
		// wins over the decoded slug; the slug stays in Slug.
		b.Name = repoDisplayName(slug, cfg)
		sortRuns(b.Runs)
		for _, r := range b.Runs {
			v.Project.Runs++
			if r.HasAge {
				v.Project.RunHours += r.Age.Hours()
			}
		}
		if len(b.Runs) == 0 {
			b.Burn = Burn{} // the window still holds the spend of runs that ended
		}
		v.Repos = append(v.Repos, b)
	}
	if v.Project.Runs == 0 {
		v.Project.Burn = Burn{} // no run is live: a decaying trailing value is no rate
	}

	sort.SliceStable(v.Repos, func(i, j int) bool { return lessRepoBlock(v.Repos[i], v.Repos[j]) })
	return v
}

func unkey(slug string) string {
	if u, err := budget.Unkey(slug); err == nil {
		return u
	}
	return slug
}

// repoDisplayName is slug's name as a repository block shows it: the wire
// key decoded, unless cfg.RepoNames gives it a readable one.
func repoDisplayName(slug string, cfg Config) string {
	name := clean(unkey(slug))
	if n := clean(cfg.RepoNames[slug]); n != "" {
		name = n
	}
	if name == "" {
		name = "-"
	}
	return name
}

// lessRepoBlock orders repository blocks: killed first, then by name and
// slug, so a later spend change never moves a block the cursor may be on
// (design generic-tool G23). Build and MergeQueued share this so a
// queued-only repository sorts in among the rest the same way.
func lessRepoBlock(a, b RepoBlock) bool {
	if a.Kill.On != b.Kill.On {
		return a.Kill.On
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.Slug < b.Slug
}

// sortRuns orders b's runs by when each began (StartedAt), then by id: Build
// and MergeQueued share this so a queued run sorts in among the running ones
// chronologically.
func sortRuns(runs []RunRow) {
	sort.SliceStable(runs, func(i, j int) bool {
		a, c := runs[i], runs[j]
		if a.StartedAt != c.StartedAt {
			return a.StartedAt < c.StartedAt
		}
		return a.Run < c.Run
	})
}

func runRow(slug, run string, e budget.AgentEntry, now time.Time) RunRow {
	r := RunRow{
		Run: clean(unkey(run)), Slug: slug,
		Title: dash(e.Title), Stage: dash(e.Stage), Verify: dash(e.Verify), Auth: dash(e.Auth),
		Round:      "-",
		Notional:   e.Auth == "oauth",
		Spent:      nonneg(e.Spent),
		HasSpent:   e.Spent != 0,
		Halted:     clean(e.Halted),
		Recipe:     clean(e.Recipe),
		StartedAt:  e.StartedAt,
		PRURL:      cleanURL(e.PRURL),
		DeadlineAt: e.StageDeadline,
	}
	if e.Round > 0 {
		r.Round = strconv.Itoa(e.Round)
	}
	r.Models = models(e.Coder, e.Reviewer)
	if e.StartedAt > 0 {
		r.Age, r.HasAge = max(now.Sub(time.UnixMilli(e.StartedAt)), 0), true
	}

	// Silence: since the last heartbeat, else since the start.
	beat := e.UpdatedAt
	if beat == 0 {
		beat = e.StartedAt
	}
	if beat > 0 {
		switch quiet := now.Sub(time.UnixMilli(beat)); {
		case quiet > LostAfter:
			r.Health = HealthLost
		case quiet > SilentAfter:
			r.Health = HealthSilent
		}
	}

	// Stage deadline.
	if e.StageDeadline > 0 {
		dl := time.UnixMilli(e.StageDeadline)
		start := e.StageStartedAt
		if start == 0 {
			start = e.StartedAt
		}
		switch {
		case now.After(dl):
			r.Deadline = DeadlineOver
		case start > 0 && start < e.StageDeadline:
			amber := time.UnixMilli(start).Add(time.Duration(NearDeadline * float64(dl.Sub(time.UnixMilli(start)))))
			if now.After(amber) {
				r.Deadline = DeadlineNear
			}
		}
	}
	return r
}

func models(coder, reviewer string) string {
	coder, reviewer = clean(coder), clean(reviewer)
	switch {
	case coder == "" && reviewer == "":
		return "-"
	case reviewer == "" || reviewer == coder:
		return coder
	case coder == "":
		return reviewer
	}
	return clip(coder + " / " + reviewer)
}
