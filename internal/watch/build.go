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
	// Names maps a repository's RTDB slug key to its owner/name, the way
	// `fugaro budget show` names it (localcfg.Config.RepoNames): the
	// project's own, trusted list, never a job-written field. A slug with
	// no entry falls back to the slug itself.
	Names map[string]string
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
	Auth      string
	Notional  bool // auth is oauth: Spent is notional dollars
	Spent     budget.Micros
	HasSpent  bool
	Age       time.Duration
	HasAge    bool
	Health    Health
	Deadline  Deadline
	Halted    string // reason, "" when not halted
	StartedAt int64  // epoch ms, for ordering
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
}

// View is the typed snapshot a renderer draws.
type View struct {
	Now     time.Time
	Day     string // YYYY-MM-DD, "" before the first day is set
	Mode    string // observe or enforce
	Conn    Connection
	Project ProjectLine
	Repos   []RepoBlock
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
		// The readable name: the project's own repo list, else the trusted
		// config/caps/repos/<slug>.repo (an admin can set one over IAM), else
		// the wire key decoded. The job-written registry repo field is free
		// text and never names a block (Slug carries the wire key, for paths
		// and identity, and is kept alongside for --json and --once text).
		b.Name = cfg.Names[slug]
		if rc.Repo != nil && rc.Repo.Repo != "" {
			b.Name = rc.Repo.Repo
		}
		if b.Name == "" {
			b.Name = clean(unkey(slug))
		}
		b.Name = clean(b.Name)
		if b.Name == "" {
			b.Name = "-"
		}
		sort.SliceStable(b.Runs, func(i, j int) bool {
			a, c := b.Runs[i], b.Runs[j]
			if a.StartedAt != c.StartedAt {
				return a.StartedAt < c.StartedAt
			}
			return a.Run < c.Run
		})
		for _, r := range b.Runs {
			v.Project.Runs++
			if r.HasAge {
				v.Project.RunHours += r.Age.Hours()
			}
		}
		// A rolling window still shows what was spent in the last few
		// minutes after every run of the repository stops (a kill, or all
		// runs finishing): a trailing rate decaying toward zero, not a
		// burn. With nothing live, there is no rate to show.
		if len(b.Runs) == 0 {
			b.Burn = Burn{}
		}
		v.Repos = append(v.Repos, b)
	}
	if v.Project.Runs == 0 {
		v.Project.Burn = Burn{}
	}

	// The project's counters are a single aggregate node; each repository's
	// is a separate one (design §6.2). They are written together, but a
	// repository this build never saw contribute to one (for example, one
	// whose own counter node was pruned or never arrived, while the run it
	// came from still counted at the project level) would otherwise make the
	// visible rows add up to less than the total with no visible reason. An
	// "other" row keeps the sum honest; Slug is "" (it can't be killed).
	var sc, ss, sn budget.Micros
	for _, r := range v.Repos {
		sc, ss, sn = sc+r.Counted, ss+r.Spent, sn+r.Notional
	}
	if oc, os, on := nonneg(v.Project.Counted-sc), nonneg(v.Project.Spent-ss), nonneg(v.Project.Notional-sn); oc > 0 || os > 0 || on > 0 {
		v.Repos = append(v.Repos, RepoBlock{Name: "other (not shown individually)", Counted: oc, Spent: os, Notional: on})
	}

	sort.SliceStable(v.Repos, func(i, j int) bool {
		a, b := v.Repos[i], v.Repos[j]
		if a.Kill.On != b.Kill.On {
			return a.Kill.On
		}
		if sa, sb := a.Spent+a.Notional, b.Spent+b.Notional; sa != sb {
			return sa > sb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Slug < b.Slug
	})
	return v
}

func unkey(slug string) string {
	if u, err := budget.Unkey(slug); err == nil {
		return u
	}
	return slug
}

func runRow(slug, run string, e budget.AgentEntry, now time.Time) RunRow {
	r := RunRow{
		Run: clean(unkey(run)), Slug: slug,
		Title: dash(e.Title), Stage: dash(e.Stage), Verify: dash(e.Verify), Auth: dash(e.Auth),
		Round:     "-",
		Notional:  e.Auth == "oauth",
		Spent:     nonneg(e.Spent),
		HasSpent:  e.Spent != 0,
		Halted:    clean(e.Halted),
		StartedAt: e.StartedAt,
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
