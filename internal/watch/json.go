package watch

import (
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The JSON shapes of fugaro watch --json: one document per state change.
// Dollars are floats in USD, as in fugaro budget show --json.

type JSONKill struct {
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type JSONBurn struct {
	Known     bool    `json:"known"`
	USDPerMin float64 `json:"usd_per_min"`
	FastAlert bool    `json:"fast"`
}

type JSONConn struct {
	State      string  `json:"state"` // live, stale, offline, refused or polling
	Reason     string  `json:"reason,omitempty"`
	AgeSeconds float64 `json:"age_seconds"`
}

type JSONTotal struct {
	CountedUSD  float64   `json:"counted_usd"`
	SpentUSD    float64   `json:"spent_usd"`
	NotionalUSD float64   `json:"notional_usd"` // subscription list price: never billed, never capped
	DailyCapUSD *float64  `json:"daily_cap_usd"`
	Percent     *float64  `json:"percent"`
	Burn        JSONBurn  `json:"burn"`
	Kill        *JSONKill `json:"kill"`
	Runs        int       `json:"runs"`
	RunHours    float64   `json:"run_hours"`
}

type JSONRepo struct {
	Slug        string    `json:"slug"`
	Repo        string    `json:"repo"`
	CountedUSD  float64   `json:"counted_usd"`
	SpentUSD    float64   `json:"spent_usd"`
	NotionalUSD float64   `json:"notional_usd"`
	DailyCapUSD *float64  `json:"daily_cap_usd"`
	Percent     *float64  `json:"percent"`
	Burn        JSONBurn  `json:"burn"`
	Kill        *JSONKill `json:"kill"`
}

type JSONRun struct {
	Run      string   `json:"run"`
	Slug     string   `json:"slug"`
	Repo     string   `json:"repo"`
	Title    string   `json:"title"`
	Stage    string   `json:"stage"`
	Round    string   `json:"round"`
	Verify   string   `json:"verify"`
	Models   string   `json:"models"`
	Recipe   string   `json:"recipe,omitempty"`
	Auth     string   `json:"auth"`
	Notional bool     `json:"notional"` // SpentUSD is a subscription's list price
	SpentUSD *float64 `json:"spent_usd"`
	AgeSecs  *float64 `json:"age_seconds"`
	Health   string   `json:"health"`   // ok, silent or lost
	Deadline string   `json:"deadline"` // ok, near or over
	Halted   string   `json:"halted,omitempty"`
}

// JSONDoc is one watch --json document.
type JSONDoc struct {
	Project    string     `json:"project"`
	Mode       string     `json:"mode"`
	Day        string     `json:"day"`
	Now        string     `json:"now"`
	Connection JSONConn   `json:"connection"`
	Total      JSONTotal  `json:"total"`
	Repos      []JSONRepo `json:"repos"`
	Runs       []JSONRun  `json:"runs"`
}

func usdPtr(b Bar) (cap, pct *float64) {
	if !b.Has {
		return nil, nil
	}
	c, p := b.Cap.USD(), b.Percent
	return &c, &p
}

func jsonBurn(b Burn) JSONBurn {
	return JSONBurn{Known: b.Known, USDPerMin: b.PerMin.USD(), FastAlert: b.Fast}
}

func jsonKill(k KillState) *JSONKill {
	if !k.On {
		return nil
	}
	j := &JSONKill{By: k.By, Reason: k.Reason}
	if !k.At.IsZero() {
		j.At = k.At.UTC().Format(time.RFC3339)
	}
	return j
}

var connNames = [...]string{ConnLive: "live", ConnStale: "stale", ConnOffline: "offline", ConnRefused: "refused", ConnPolling: "polling"}

// BuildJSON is v as the --json document.
func BuildJSON(project string, v View) JSONDoc {
	d := JSONDoc{Project: project, Mode: v.Mode, Day: v.Day, Now: v.Now.UTC().Format(time.RFC3339),
		Connection: JSONConn{State: connNames[v.Conn.Kind], Reason: v.Conn.Reason, AgeSeconds: v.Conn.Age.Seconds()},
		Repos:      []JSONRepo{}, Runs: []JSONRun{}}
	p := v.Project
	d.Total = JSONTotal{CountedUSD: p.Counted.USD(), SpentUSD: p.Spent.USD(), NotionalUSD: p.Notional.USD(),
		Burn: jsonBurn(p.Burn), Kill: jsonKill(p.Kill), Runs: p.Runs, RunHours: p.RunHours}
	d.Total.DailyCapUSD, d.Total.Percent = usdPtr(p.Bar)
	for _, r := range v.Repos {
		jr := JSONRepo{Slug: safetext.Strip(r.Slug), Repo: r.Name, CountedUSD: r.Counted.USD(), SpentUSD: r.Spent.USD(),
			NotionalUSD: r.Notional.USD(), Burn: jsonBurn(r.Burn), Kill: jsonKill(r.Kill)}
		jr.DailyCapUSD, jr.Percent = usdPtr(r.Bar)
		d.Repos = append(d.Repos, jr)
		for _, run := range r.Runs {
			j := JSONRun{Run: run.Run, Slug: jr.Slug, Repo: r.Name, Title: run.Title, Stage: run.Stage, Round: run.Round,
				Verify: run.Verify, Models: run.Models, Recipe: run.Recipe, Auth: run.Auth, Notional: run.Notional, Halted: run.Halted,
				Health:   [...]string{HealthOK: "ok", HealthSilent: "silent", HealthLost: "lost"}[run.Health],
				Deadline: [...]string{DeadlineOK: "ok", DeadlineNear: "near", DeadlineOver: "over"}[run.Deadline]}
			if run.HasSpent {
				s := run.Spent.USD()
				j.SpentUSD = &s
			}
			if run.HasAge {
				a := run.Age.Seconds()
				j.AgeSecs = &a
			}
			d.Runs = append(d.Runs, j)
		}
	}
	return d
}

// FilterRepo narrows v to the repository whose wire slug key is key (the
// project line stays the whole project's).
func FilterRepo(v View, key string) View {
	var keep []RepoBlock
	for _, r := range v.Repos {
		if r.Slug == key {
			keep = append(keep, r)
		}
	}
	v.Repos = keep
	return v
}
