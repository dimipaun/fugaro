package watch

import (
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
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
	CountedUSD  float64 `json:"counted_usd"`
	SpentUSD    float64 `json:"spent_usd"`
	NotionalUSD float64 `json:"notional_usd"` // subscription list price: never billed, never capped
	// LeasedUSD is counted minus spent, never negative: the money open leases
	// hold and have not yet spent. It is always present, 0 when nothing is
	// leased, and it is *leased, not yet spent* rather than "live leases" --
	// the sweeper never releases (internal/budget/sweep.go), so a crashed
	// run's lease stays in the figure for the rest of the (UTC) day.
	LeasedUSD   float64  `json:"leased_usd"`
	DailyCapUSD *float64 `json:"daily_cap_usd"`
	Percent     *float64 `json:"percent"`
	// MaxParallel is the concurrent-run cap a launch is checked against
	// (checkMaxParallel, internal/cli/run.go): the selected config's
	// max_parallel as the config layer resolved it, which is a figure no budget
	// node holds -- WatchDeps.emit sets it, not BuildJSON. The engine's own
	// default (20) is part of that resolution: localcfg hands it to a file that
	// names none, and it is reported here because 20 is then what the launcher
	// compares against. Null means unknown -- the shape for a writer with no
	// config to read, never a cap this package invented.
	MaxParallel *int      `json:"max_parallel"`
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
	LeasedUSD   float64   `json:"leased_usd"` // counted - spent, floored at 0: see JSONTotal
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
	// Job is the Cloud Run job this run is in, and Profile, Provider and
	// Account are the (profile, provider, account) its name was computed from
	// (plan task 3.5 step 2): under the project-centric model a run's job is
	// shared by every repository admitted to the profile, so the triple is
	// what says which job a row came from, and the job's name does not spell
	// it out. The three are absent for a run on a per-repository job (its
	// name is not a project job the published policy defines) and for a run
	// whose launch.json the reader does not have. Account is present only for
	// a job of a named Claude account: a profile that lists claude_accounts
	// has one job per account beside its no-account job
	// (config.ProjectJobAccounts), and an absent Account means the name's
	// account axis is empty — the profile's no-account job, which holds the
	// default account's token when the profile lists "default" and no token
	// otherwise (config.ProjectJobTokenAccount) — never "this job mounts no
	// credential". Job is launch.json's, which launchers and shared-job runs
	// can write: display text, never an authority. The grouping stays by
	// repository, from the row's own slug.
	Job      string `json:"job,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account,omitempty"`
	// LaunchedBy is who started the run, as the run's own launch.json recorded
	// it at launch: a person's address, or the identity an automated launch
	// runs as (a service account, a CI runner). It is the full recorded
	// identity, where the text views show the short form a column has room for
	// (runview.LauncherShort). Absent when the record has none — one whose
	// launch.json the reader does not have, or a launch built without a
	// launcher (the recovery backfill, a local run with no execution, or the
	// synthetic launch `fugaro logs` builds) — never a value this package
	// invents.
	//
	// UNTRUSTED display text a launcher wrote: sanitised with safetext.Field
	// like every other bucket-derived string here, and never an authority — no
	// policy, admission or budget rule reads it.
	LaunchedBy string `json:"launched_by,omitempty"`
	// PlanTask is the plan task the run's launch.json recorded (task.Spec's
	// PlanTask, at fugaro run --plan-task): the display-only join hint the
	// console matches a run to a node of a plan's graph by (console
	// dependency graph plan, decision GD5). It is the same value runs ls
	// --json carries, sanitised the same way (RunFacts), and absent when the
	// launch record has none — never a value this package invents, and never
	// an identifier anything acts on.
	PlanTask string `json:"plan_task,omitempty"`
	// PRNumber is the pull request number the run has recorded, once it has
	// one: a draft PR opens early in a run (design generic-tool G24), so a
	// live row carries the number long before the run finishes, and the
	// console's graph screen shows it without waiting for a `plan status`
	// refresh (console dependency graph plan, task GV2.5). It is the number
	// the run's own record holds — runview.Row.RecordPR, the value `runs ls
	// --json` prints as record_pr — read from the registry entry's prUrl,
	// which the run writes from that same record, with this package's own
	// anchored GitHub pattern (internal/watch/prstate.go) rather than by a
	// second parse of the URL's text.
	//
	// Absent when the run has recorded no pull request — never 0, which no
	// repository numbering from 1 ever issues. Display-only, like the row's
	// other hints: no policy, admission or budget rule reads it, and a reader
	// builds no link from it (design §5.1 rule 2).
	PRNumber int `json:"pr_number,omitempty"`
	// Credential is the name — never the value — of the credential the run's
	// cascade is currently using (budget.AgentEntry.Credential, written by the
	// run itself under C1.3's rules; the credentials service chooses it). It is
	// a name a run wrote, sanitised like every other database-derived string
	// here, and absent when the run has not reported one — which is "not
	// recorded", never "no credential".
	Credential string `json:"credential,omitempty"`
	// WaitingUntil is when a run waiting for a credential expects to resume,
	// in RFC 3339 UTC, as the document spells every instant (`now`, a kill's
	// `at`). It is derived from budget.AgentEntry.WaitingUntil (epoch ms,
	// D152's "waiting, expected resume time") and is present only while the
	// run is still waiting: presence is the state, so a reader that sees the
	// field knows the run is held up rather than spending, and one that wants
	// a countdown compares it with `now`.
	WaitingUntil string `json:"waiting_until,omitempty"`
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
	// Job and the (profile, provider, account) behind its name, as in JSONRun:
	// a finished run's row is a run's row too, and a reader that follows a
	// run from runs[] into finished[] must not lose the job it ran on.
	Job      string `json:"job,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account,omitempty"`
	// LaunchedBy is who started the run, as in JSONRun: a reader that follows a
	// run into finished[] must not lose who launched it either. Absent when the
	// launch record has none.
	LaunchedBy string `json:"launched_by,omitempty"`
	// PlanTask is the plan task the run's launch.json recorded, as in
	// JSONRun: a reader that follows a run into finished[] must not lose the
	// node it was joined to either. Absent when the launch record has none.
	PlanTask string `json:"plan_task,omitempty"`
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
		Failed: r.Failed, PRURL: r.PRURL, PRNumber: r.PRNumber, AgeSecs: r.Age.Seconds(),
		Job: r.Job, Profile: r.Profile, Provider: r.Provider, Account: r.Account,
		LaunchedBy: r.LaunchedBy, PlanTask: r.PlanTask}
}

// LeasedUSD is the outstanding leased dollars: counted minus spent, floored at
// zero. A lease grant raises counted by the reserved amount and the gateway
// moves settled spend into spent (internal/budget/lease.go), so the difference
// is what open leases hold and have not yet spent. It is floored rather than
// trusted because counted below spent is a state no normal write makes, and a
// negative figure would read as a credit.
//
// Exported because both documents that carry the figure compute it from the
// two counters they already hold -- this file's JSONTotal/JSONRepo and `budget
// show --json`'s (internal/cli/budget.go) -- and one implementation is what
// keeps the two from disagreeing.
func LeasedUSD(counted, spent budget.Micros) float64 {
	return max(counted-spent, 0).USD()
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
		LeasedUSD: LeasedUSD(p.Counted, p.Spent),
		Burn:      jsonBurn(p.Burn), Kill: jsonKill(p.Kill), Runs: p.Runs, RunHours: p.RunHours}
	d.Total.DailyCapUSD, d.Total.Percent = usdPtr(p.Bar)
	for _, r := range v.Repos {
		jr := JSONRepo{Slug: safetext.Strip(r.Slug), Repo: r.Name, CountedUSD: r.Counted.USD(), SpentUSD: r.Spent.USD(),
			NotionalUSD: r.Notional.USD(), LeasedUSD: LeasedUSD(r.Counted, r.Spent),
			Burn: jsonBurn(r.Burn), Kill: jsonKill(r.Kill)}
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
				RequestedBy: run.RequestedBy,
				Job:         run.Job,
				Profile:     run.Profile,
				Provider:    run.Provider,
				Account:     run.Account,
				LaunchedBy:  run.LaunchedBy,
				PlanTask:    run.PlanTask,
				PRNumber:    run.PRNumber,
				Credential:  run.Credential}
			if run.Waiting {
				j.WaitingUntil = time.UnixMilli(run.WaitingUntil).UTC().Format(time.RFC3339)
			}
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
