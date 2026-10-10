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
	// Queued marks a run known only from the runs bucket: launched (or about
	// to be) but with no registry entry yet. Stuck means it has gone
	// QueuedStuckAfter without one showing up. Workflow and RequestedBy are
	// from task.json, when a queued run's; a live run leaves them "".
	Queued      bool   `json:"queued,omitempty"`
	Stuck       bool   `json:"stuck,omitempty"`
	Workflow    string `json:"workflow,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
}

// JSONFinished is a finished run (design generic-tool §10.1 and §10.3): a
// result.json already exists, so it is read from the runs bucket, not RTDB.
type JSONFinished struct {
	Run      string  `json:"run"`
	Slug     string  `json:"slug"`
	Repo     string  `json:"repo"`
	Title    string  `json:"title"`
	Status   string  `json:"status"` // the runstore status: succeeded, failed, halted, cancelled, infra_error
	Outcome  string  `json:"outcome"`
	Failed   bool    `json:"failed"`
	PRURL    string  `json:"pr_url,omitempty"`
	PRNumber int     `json:"pr_number,omitempty"`
	AgeSecs  float64 `json:"age_seconds"`
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
	// Finished and Ready are unfiltered (design generic-tool §10.3): every
	// finished run, and the ones among them whose Outcome is "ready", are
	// listed regardless of age or count; --all, --keep and --keep-count only
	// bound the interactive and plain views.
	Finished []JSONFinished `json:"finished"`
	Ready    []JSONFinished `json:"ready"`
	// QueuedNote is a one-line, already-sanitised reason the runs bucket
	// could not be read for queued rows; absent when it was (or wasn't asked).
	QueuedNote string `json:"queued_note,omitempty"`
}

func jsonFinished(slug, repo string, r RunRow) JSONFinished {
	return JSONFinished{Run: r.Run, Slug: slug, Repo: repo, Title: r.Title, Status: r.Stage, Outcome: r.Outcome,
		Failed: r.Failed, PRURL: r.PRURL, PRNumber: r.PRNumber, AgeSecs: r.Age.Seconds()}
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
		Repos:      []JSONRepo{}, Runs: []JSONRun{}, Finished: []JSONFinished{}, Ready: []JSONFinished{}, QueuedNote: v.QueuedNote}
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
				Health:      [...]string{HealthOK: "ok", HealthSilent: "silent", HealthLost: "lost"}[run.Health],
				Deadline:    [...]string{DeadlineOK: "ok", DeadlineNear: "near", DeadlineOver: "over"}[run.Deadline],
				Queued:      run.Queued,
				Stuck:       run.Stuck,
				Workflow:    run.Workflow,
				RequestedBy: run.RequestedBy}
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
		for _, f := range r.Finished {
			d.Finished = append(d.Finished, jsonFinished(jr.Slug, r.Name, f))
		}
	}
	for _, item := range ReadyRowsOf(v) {
		d.Ready = append(d.Ready, jsonFinished(safetext.Strip(item.Run.Slug), item.Repo, item.Run))
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
