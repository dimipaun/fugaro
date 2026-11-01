package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/watch"
)

// fugaro budget (design §8): look at the project's caps, counters, kill
// switches and live runs in its Firebase Realtime Database, and, as a budget
// admin, change them. Everything goes through internal/rtdb as the person
// (ADC); the database's IAM roles decide who may read and who may write.

const (
	// priceStaleAfter is how old the built-in price table's check date may
	// get before prices warns.
	priceStaleAfter = 90 * 24 * time.Hour
	// maxKillReason bounds a switch's reason, which watch shows.
	maxKillReason = 200
	// setAttempts bounds the ETag retries of one set, kill or resume.
	setAttempts = budget.SetAttempts
	// staleAfter marks a registry entry whose runner has not heartbeaten
	// for this long (the heartbeat is every 15 s).
	staleAfter = 90 * time.Second
)

var (
	// budgetNow is the clock of the budget commands and of every surface
	// that prices a model from the catalog (validate, diagnose): tests
	// replace it so a catalog's 30-day staleness is decided by the fixture
	// and not by the day the test runs.
	budgetNow = time.Now
	// budgetTransport, when set, carries the database requests; tests wrap
	// the fake's transport with it.
	budgetTransport http.RoundTripper
)

func newBudgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Show and change the project's model-spend caps and kill switches",
		Long: "Look at and change the project's budget: the caps, today's counters, the kill\n" +
			"switches and the runs in flight, kept in the project's Firebase Realtime\n" +
			"Database (design §8). show needs the Viewer role on the Firebase project;\n" +
			"set, kill and resume need the budget-admin role (the project's owners and\n" +
			"editors, and terraform.budget_admins). preview needs the Viewer role too,\n" +
			"and reads the project's recipes from the runs bucket as recipes ls does,\n" +
			"which a plain project Viewer cannot (see docs/gcp-setup.md).",
	}
	cmd.AddCommand(newBudgetShowCmd(), newBudgetSetCmd(), newBudgetKillCmd(true), newBudgetKillCmd(false),
		newBudgetPreviewCmd(), newBudgetPricesCmd(), newBudgetHistoryCmd())
	return cmd
}

// ---------------------------------------------------------------- shared

// money is a dollar amount for people: whole cents at least, sub-cent
// precision when there is some.
func money(m budget.Micros) string {
	// The magnitude as uint64: -MinInt64 overflows int64, and the counters come
	// from database nodes that may hold anything.
	sign, u := "", uint64(m)
	if m < 0 {
		sign, u = "-", -uint64(m)
	}
	s := fmt.Sprintf("%d.%06d", u/1_000_000, u%1_000_000)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	if strings.HasSuffix(s, ".00") {
		s = strings.TrimSuffix(s, ".00")
	}
	return sign + "$" + s
}

func moneyPtr(m *budget.Micros) string {
	if m == nil {
		return "not set"
	}
	return money(*m)
}

func usdPtr(m *budget.Micros) *float64 {
	if m == nil {
		return nil
	}
	v := m.USD()
	return &v
}

// clipRunes is s cut to n runes.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// adminDenied says what a refused write means: D6.
func adminDenied(lc *localcfg.Config) error {
	return userErr("%s", budget.AdminDeniedText(lc.Name))
}

// dbErr classifies an error of a database call: a refusal is a user error
// (exit 1), everything else, an outage included, a remote one (exit 2).
func dbErr(lc *localcfg.Config, err error, write bool) error {
	switch {
	case errors.Is(err, rtdb.ErrPermission) && write:
		return adminDenied(lc)
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("project %s's budget database refused the read: you need the Viewer role on its Firebase project (GCP owners and editors have it); gcloud auth application-default login may be needed (%v)", lc.Name, err)
	}
	return remote(err)
}

// checkDBProject refuses a database that is not this project's: its
// /fugaro/project must be the project's name (plan R-9: a wrong project's
// backend). An unmarked database is refused too.
func checkDBProject(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) error {
	var raw json.RawMessage
	found, err := db.Get(ctx, budget.PathProject, &raw)
	var name string
	if err == nil && found && json.Unmarshal(raw, &name) != nil {
		return userErr("the database at budget.rtdb_url has a /fugaro/project that is not a project name: it is not project %s's budget database; fix budget.rtdb_url in its config", lc.Name)
	}
	switch {
	case err != nil:
		return dbErr(lc, err, false)
	case !found:
		return userErr("the database at budget.rtdb_url has no /fugaro/project: it is not a Fugaro project's budget database; an operator runs fugaro update --firebase-project %s for project %s", firebaseProjectIDWord(lc.GCPProject), lc.Name)
	case name != lc.Name:
		return userErr("the database at budget.rtdb_url belongs to project %q, not %q; fix budget.rtdb_url in project %s's config", oneLine(name), lc.Name, lc.Name)
	}
	return nil
}

// requireOwnBudgetDB is the ONE ownership question every writer into the budget
// database asks (budgetDBUnadopted — the owner's ruling of 2026-10-31), as a
// command's refusal. It is a call and never a second check worded alike: the
// `fugaro update` stages ask the same function, so a command and a stage cannot
// answer "is this database ours" differently. declined is what the caller would
// have written there and ends the refusal's sentence.
//
// checkDBProject beside it is not this check and stays: it reads the project's
// name at /fugaro/project, a fact of the database's contents, while this asks
// whether the database is one this installation may write at all.
func requireOwnBudgetDB(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, declined string) error {
	why, err := budgetDBUnadoptedErr(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), declined, selfCommand()+" update")
	switch {
	case err != nil:
		// Check's own error, classified the way it classified it: a refusal
		// (another installation's mark, or data and no mark) is the user's
		// (exit 1) and an outage is not (exit 2).
		var ue *infra.UserError
		if errors.As(err, &ue) {
			return userErr("%s", why)
		}
		return remote(errors.New(why))
	case why != "":
		return userErr("%s", why)
	}
	return nil
}

// openBudget opens the database and checks it is the project's.
func openBudget(ctx context.Context, o cloudOptions) (*localcfg.Config, *rtdb.Client, error) {
	lc, db, err := openBudgetDB(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, nil, err
	}
	return lc, db, nil
}

// repoSlugOf is repo's slug, as run and ls take it.
func repoSlugOf(ctx context.Context, lc *localcfg.Config, repo string) (string, error) {
	return (&cloudEnv{lc: lc}).repoSlug(repo, func() *config.Config { return checkoutConfig(ctx, repo) })
}

// confirmTyped shows what is about to happen and returns nil once the
// project's name is typed at a terminal, or yes is set. Without either it
// refuses: a raise or a switch change is never made unconfirmed.
func confirmTyped(cmd *cobra.Command, in *bufio.Reader, lc *localcfg.Config, yes bool, what string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", lc.Name, what)
	if yes {
		fmt.Fprintln(out, "  confirmed by --yes")
		return nil
	}
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s, or pass --yes once you have read what it does", initflow.NeedsTerminal("the typed confirmation of this step"))
	}
	fmt.Fprintf(out, "Type %s to confirm: ", lc.Name)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != lc.Name {
		return userErr("not confirmed (the project's name was not typed); nothing was changed")
	}
	return nil
}

// confirmTypedStrict is confirmTyped where nothing stands in for the typed
// name: no flag confirms the step, so the caller passes the sentence saying
// why (why), and the refusal names no flag at all — a refusal that named one
// would be read by whoever it refuses as the thing to pass next, and on a
// command that has no such flag it is advice that cannot be taken.
func confirmTypedStrict(cmd *cobra.Command, lc *localcfg.Config, why, what string) error {
	out := cmd.OutOrStdout()
	// The name is printed bounded (safetext.Field) and compared as itself
	// below: the bound is on the text and never on the comparison.
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", safetext.Field(lc.Name), what)
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s: %s", initflow.NeedsTerminal("the typed confirmation of this step"), why)
	}
	fmt.Fprintf(out, "Type %s to confirm: ", safetext.Field(lc.Name))
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != lc.Name {
		return userErr("not confirmed (the project's name was not typed); nothing was changed")
	}
	return nil
}

// --------------------------------------------------------------- show

type budgetShowOptions struct {
	cloud     cloudOptions
	repo      string
	all, json bool
	// compute asks for each repository's compute dollars, which are read from
	// the runs bucket's own records: the database holds no compute at all.
	compute bool
}

func newBudgetShowCmd() *cobra.Command {
	var o budgetShowOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show caps, today's counters, kill switches, headroom and the runs in flight",
		Long: "Show the project's budget: the mode, the caps, today's (UTC) counted, spent and\n" +
			"notional dollars, the headroom under the caps, the kill switches and the runs\n" +
			"in flight. By default the global figures and the project config's\n" +
			"repositories; --repo one repository; --all every repository the database\n" +
			"knows. Notional is a subscription's list-price figure, never billed and never\n" +
			"capped. Counted is spent plus what runs have reserved and not released; --json\n" +
			"carries that difference as leased_usd, the money leased and not yet spent: a\n" +
			"crashed run's lease is never released, so it stays in the figure for the rest\n" +
			"of the day. --compute adds each repository's compute dollars, read from the\n" +
			"runs bucket: a run's compute is in its own record and nowhere in the\n" +
			"database, so the flag costs a listing of the bucket's repositories and, for\n" +
			"each repository shown, a listing of its runs minted since the previous day\n" +
			"began plus a read of every one of those records (a run's start day is only\n" +
			"known once its record is read), and everything that only needs the counters\n" +
			"leaves it off. The figure is an estimate by design, priced from each run's\n" +
			"own price table, and covers the runs that started today and recorded a\n" +
			"result: a run still in flight has recorded none, so today's is partial, and\n" +
			"a repository whose runs recorded no estimate is null, never 0.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetShow(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.all, "all", false, "every repository in the database")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	f.BoolVar(&o.compute, "compute", false, "also read each repository's compute dollars from the runs bucket (an estimate; null and n/a where the day's runs recorded none)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

type showKill struct {
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type showRun struct {
	Slug      string  `json:"slug"`
	Run       string  `json:"run"`
	Repo      string  `json:"repo,omitempty"`
	Workflow  string  `json:"workflow,omitempty"`
	Title     string  `json:"title,omitempty"`
	Stage     string  `json:"stage,omitempty"`
	Round     int     `json:"round,omitempty"`
	Auth      string  `json:"auth,omitempty"`
	Coder     string  `json:"coder,omitempty"`
	Reviewer  string  `json:"reviewer,omitempty"`
	SpentUSD  float64 `json:"spent_usd"`
	PRURL     string  `json:"pr_url,omitempty"`
	Halted    string  `json:"halted,omitempty"`
	StartedAt string  `json:"started_at,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
	Stale     bool    `json:"stale,omitempty"`
}

type showGlobal struct {
	DailyCapUSD  *float64 `json:"daily_cap_usd"`
	PerRunCapUSD *float64 `json:"per_run_cap_usd"`
	CountedUSD   float64  `json:"counted_usd"`
	SpentUSD     float64  `json:"spent_usd"`
	NotionalUSD  float64  `json:"notional_usd"`
	// LeasedUSD is counted minus spent, never negative: the money open leases
	// hold and have not yet spent (watch --json carries the same figure). It is
	// *leased, not yet spent* rather than "live leases": the sweeper never
	// releases, so a crashed run's lease stays in it for the rest of the day.
	LeasedUSD   float64   `json:"leased_usd"`
	HeadroomUSD *float64  `json:"headroom_usd"`
	Kill        *showKill `json:"kill"`
}

type showDefaults struct {
	RepoDailyCapUSD  *float64 `json:"repo_daily_cap_usd"`
	RepoPerRunCapUSD *float64 `json:"repo_per_run_cap_usd"`
}

// computeFigure is showRepo's compute_usd. The key is absent unless
// `budget show --compute` asked for it, so a document from a command without
// the flag is byte for byte what it always was; with the flag it is null
// rather than 0 where the figure is unknown (the day recorded no estimate, or
// a figure that cannot be summed), because an unknown compute is n/a, never
// free. A *float64 cannot say both: omitempty drops a nil pointer instead of
// writing null.
type computeFigure struct {
	usd   float64
	known bool
}

func (f computeFigure) MarshalJSON() ([]byte, error) {
	if !f.known {
		return []byte("null"), nil
	}
	return json.Marshal(f.usd)
}

type showRepo struct {
	Slug         string   `json:"slug"`
	Repo         string   `json:"repo,omitempty"`
	DailyCapUSD  *float64 `json:"daily_cap_usd"`
	CapSource    string   `json:"daily_cap_source"` // repo, default or none
	PerRunCapUSD *float64 `json:"per_run_cap_usd"`
	// PerRunCapSource is where the effective per-run cap comes from: repo,
	// default, global, or none when no per-run cap is set anywhere.
	PerRunCapSource string         `json:"per_run_cap_source"`
	CountedUSD      float64        `json:"counted_usd"`
	SpentUSD        float64        `json:"spent_usd"`
	NotionalUSD     float64        `json:"notional_usd"`
	ComputeUSD      *computeFigure `json:"compute_usd,omitempty"`
	LeasedUSD       float64        `json:"leased_usd"` // counted - spent, floored at 0: see showGlobal
	HeadroomUSD     *float64       `json:"headroom_usd"`
	Kill            *showKill      `json:"kill"`
	Running         []showRun      `json:"running"`
}

// perRunCapSource names the scope whose per-run cap binds a repository: the
// smaller of the repository's side (its own node, else the default) and the
// global one, which is the fallback where the repository's side is unset
// (D6). "none" is no per-run cap anywhere: a run in enforce halts (no_cap).
func perRunCapSource(c budget.Caps) string {
	repo, repoOK := c.RepoPerRun()
	global, globalOK := c.GlobalPerRun()
	if repoOK && (!globalOK || repo <= global) {
		if c.Repo != nil && c.Repo.PerRunMicros != nil {
			return "repo"
		}
		return "default"
	}
	if globalOK {
		return "global"
	}
	return "none"
}

// budgetShowDoc is show --json's document.
type budgetShowDoc struct {
	Project    string `json:"project"` // the Fugaro project
	GCPProject string `json:"gcp_project"`
	// Mode is /config/mode: observe, enforce, or empty when unset (which
	// acts as observe).
	Mode          string       `json:"mode"`
	Day           string       `json:"day"` // UTC
	MaxReserveUSD *float64     `json:"max_reserve_usd"`
	Global        showGlobal   `json:"global"`
	Defaults      showDefaults `json:"defaults"`
	Repos         []showRepo   `json:"repos"`
	// Running is every run in flight in the shown repositories.
	Running  []showRun `json:"running"`
	Warnings []string  `json:"warnings"`
}

// dbConfig is /config.
type dbConfig struct {
	Mode string `json:"mode"`
	Caps struct {
		Global   *budget.GlobalCaps         `json:"global"`
		Defaults *budget.DefaultCaps        `json:"defaults"`
		Repos    map[string]budget.RepoCaps `json:"repos"`
	} `json:"caps"`
	Limits *budget.Limits `json:"limits"`
	Kill   struct {
		Global *budget.Kill           `json:"global"`
		Repos  map[string]budget.Kill `json:"repos"`
	} `json:"kill"`
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ms(t int64) string {
	if t <= 0 {
		return ""
	}
	return time.UnixMilli(t).UTC().Format(time.RFC3339)
}

func killView(k *budget.Kill) *showKill {
	if k == nil || !k.On {
		return nil
	}
	return &showKill{By: k.By, At: ms(k.At), Reason: k.Reason}
}

// headroom is the smallest room left under the daily caps that apply, nil
// when none does. It is the rules' cap view; a committed per_day_usd in a
// repository's fugaro.yaml can only lower it further.
func headroom(room ...func() (budget.Micros, bool)) *float64 {
	var best *budget.Micros
	for _, r := range room {
		if v, ok := r(); ok {
			v = max(v, 0)
			if best == nil || v < *best {
				best = &v
			}
		}
	}
	return usdPtr(best)
}

func roomUnder(cap func() (budget.Micros, bool), counted budget.Micros) func() (budget.Micros, bool) {
	return func() (budget.Micros, bool) {
		c, ok := cap()
		return c - counted, ok
	}
}

func runBudgetShow(cmd *cobra.Command, o *budgetShowOptions) error {
	ctx := cmd.Context()
	if o.repo != "" && o.all {
		return userErr("--repo and --all don't go together")
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	var wantSlug string
	if o.repo != "" {
		if wantSlug, err = repoSlugOf(ctx, lc, o.repo); err != nil {
			return err
		}
	}

	var cfg dbConfig
	if _, err := db.Get(ctx, "config", &cfg); err != nil {
		return dbErr(lc, err, false)
	}
	now := budgetNow()
	if sn, ok := db.ServerNow(); ok {
		now = sn
	}
	day := budget.Day(now)
	var global budget.Counters
	if _, err := db.Get(ctx, budget.PathSpendGlobal(day), &global); err != nil {
		return dbErr(lc, err, false)
	}
	repoSpend := map[string]budget.Counters{}
	if _, err := db.Get(ctx, "spend/"+budget.DayKey(day)+"/repos", &repoSpend); err != nil {
		return dbErr(lc, err, false)
	}
	agents := map[string]map[string]budget.AgentEntry{}
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return dbErr(lc, err, false)
	}

	// Which repositories: keys are the database's (escaped) slugs.
	keys := map[string]bool{}
	names := map[string]string{} // slug key -> repository, for those the config names
	for name, r := range lc.Repos {
		if s, err := task.Slug(r.Provider, name); r.Provider != "" && err == nil {
			names[budget.Key(s)] = name
		}
	}
	switch {
	case o.repo != "":
		keys[budget.Key(wantSlug)] = true
	case o.all:
		for _, m := range []map[string]struct{}{keySet(cfg.Caps.Repos), keySet(cfg.Kill.Repos), keySet(repoSpend), keySet(agents)} {
			for k := range m {
				keys[k] = true
			}
		}
	default:
		for name, r := range lc.Repos {
			if r.Provider == "" {
				continue
			}
			if s, err := task.Slug(r.Provider, name); err == nil {
				keys[budget.Key(s)] = true
			}
		}
	}

	caps := budget.Caps{Global: cfg.Caps.Global, Defaults: cfg.Caps.Defaults, Limits: cfg.Limits, Mode: cfg.Mode}
	doc := &budgetShowDoc{Project: lc.Name, GCPProject: lc.GCPProject, Mode: cfg.Mode, Day: budget.DayDate(day),
		MaxReserveUSD: nil, Repos: []showRepo{}, Running: []showRun{}, Warnings: []string{}}
	if v, ok := caps.MaxReserve(); ok {
		doc.MaxReserveUSD = usdPtr(&v)
	}
	if cfg.Mode != budget.ModeObserve && cfg.Mode != budget.ModeEnforce {
		doc.Mode = ""
		doc.Warnings = append(doc.Warnings, "/config/mode is not set: the caps are advisory (observe) until `fugaro budget set --global --mode enforce`; kill switches and the reserve limit apply either way")
	}
	if v, ok := caps.GlobalDaily(); ok {
		doc.Global.DailyCapUSD = usdPtr(&v)
		if n := budget.DailyWarnNote("the project's", global.Counted, v); n != "" {
			doc.Warnings = append(doc.Warnings, n)
		}
	}
	if cfg.Caps.Global != nil {
		doc.Global.PerRunCapUSD = usdPtr(cfg.Caps.Global.PerRunMicros)
	}
	doc.Global.CountedUSD, doc.Global.SpentUSD, doc.Global.NotionalUSD = global.Counted.USD(), global.Spent.USD(), global.Notional.USD()
	doc.Global.LeasedUSD = watch.LeasedUSD(global.Counted, global.Spent)
	doc.Global.HeadroomUSD = headroom(roomUnder(caps.GlobalDaily, global.Counted))
	doc.Global.Kill = killView(cfg.Kill.Global)
	if cfg.Caps.Defaults != nil {
		doc.Defaults = showDefaults{RepoDailyCapUSD: usdPtr(cfg.Caps.Defaults.RepoDailyMicros), RepoPerRunCapUSD: usdPtr(cfg.Caps.Defaults.RepoPerRunMicros)}
	}

	listed := 0
	var compute map[string]computeFigure
	if o.compute {
		// Only the rows this document will carry are read: a repository the
		// command does not list is not in the output, so its runs need no read.
		wanted := make([]string, 0, len(keys))
		for k := range keys {
			if s, err := budget.Unkey(k); err == nil {
				wanted = append(wanted, s)
			}
		}
		compute = repoCompute(ctx, lc, day, wanted, func(m string) { doc.Warnings = append(doc.Warnings, m) })
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		slug := k
		if s, err := budget.Unkey(k); err == nil {
			slug = s
		}
		rc := caps
		if c, ok := cfg.Caps.Repos[k]; ok {
			rc.Repo = &c
		}
		rs := showRepo{Slug: slug, Running: []showRun{}, CapSource: "none", PerRunCapSource: "none"}
		rs.Repo = names[k]
		if rc.Repo != nil && rc.Repo.Repo != "" {
			rs.Repo = rc.Repo.Repo
		}
		if v, ok := rc.RepoDaily(); ok {
			rs.DailyCapUSD = usdPtr(&v)
			rs.CapSource = "default"
			if rc.Repo != nil && rc.Repo.DailyMicros != nil {
				rs.CapSource = "repo"
			}
		}
		// The per-run cap is the effective one: the repository's own or the
		// default, and the global one where that side is unset (D6), so the
		// source names the scope whose cap binds this repository.
		if v, ok := rc.PerRun(); ok {
			rs.PerRunCapUSD = usdPtr(&v)
			rs.PerRunCapSource = perRunCapSource(rc)
		}
		sp := repoSpend[k]
		rs.CountedUSD, rs.SpentUSD, rs.NotionalUSD = sp.Counted.USD(), sp.Spent.USD(), sp.Notional.USD()
		if o.compute {
			// A repository the day recorded no estimate for keeps the zero
			// figure, which marshals as null: unknown is n/a, never free.
			f := compute[k]
			rs.ComputeUSD = &f
		}
		rs.LeasedUSD = watch.LeasedUSD(sp.Counted, sp.Spent)
		rs.HeadroomUSD = headroom(roomUnder(rc.RepoDaily, sp.Counted), roomUnder(caps.GlobalDaily, global.Counted))
		if kv, ok := cfg.Kill.Repos[k]; ok {
			rs.Kill = killView(&kv)
		}
		for _, run := range slices.Sorted(maps.Keys(agents[k])) {
			e := agents[k][run]
			if rs.Repo == "" {
				rs.Repo = e.Repo
			}
			r := showRun{Slug: slug, Run: run, Repo: e.Repo, Workflow: e.Workflow, Title: e.Title, Stage: e.Stage, Round: e.Round,
				Auth: e.Auth, Coder: e.Coder, Reviewer: e.Reviewer, SpentUSD: e.Spent.USD(), PRURL: e.PRURL, Halted: e.Halted,
				StartedAt: ms(e.StartedAt), UpdatedAt: ms(e.UpdatedAt)}
			r.Stale = e.UpdatedAt > 0 && now.Sub(time.UnixMilli(e.UpdatedAt)) > staleAfter
			rs.Running = append(rs.Running, r)
			doc.Running = append(doc.Running, r)
		}
		// After the agents loop: a repository the config does not name takes
		// its name from the registry, and the warning must call it what
		// printShow's heading does.
		if cap, ok := rc.RepoDaily(); ok {
			if n := budget.DailyWarnNote(repoScope(rs), sp.Counted, cap); n != "" {
				doc.Warnings = append(doc.Warnings, n)
			}
		}
		doc.Repos = append(doc.Repos, rs)
		listed++
	}
	if !o.all && o.repo == "" {
		all := map[string]bool{}
		for _, m := range []map[string]struct{}{keySet(cfg.Caps.Repos), keySet(cfg.Kill.Repos), keySet(repoSpend), keySet(agents)} {
			for k := range m {
				all[k] = true
			}
		}
		if extra := len(all) - countIn(all, keys); extra > 0 {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("%d more repositories in the database are not in this project config's repos; --all lists them", extra))
		}
	}

	// The project figures are the sum of every repository's. Rows that are not
	// all of them (no --all) sum to less; say by how much. Counters that
	// disagree even over every repository are a real accounting mismatch.
	var unlisted, every figures
	for k, sp := range repoSpend {
		every = figures{every.counted + sp.Counted, every.spent + sp.Spent, every.notional + sp.Notional}
		if !keys[k] {
			unlisted = figures{unlisted.counted + sp.Counted, unlisted.spent + sp.Spent, unlisted.notional + sp.Notional}
		}
	}
	if unlisted != (figures{}) {
		doc.Warnings = append(doc.Warnings, "repositories not listed above hold "+countersText(unlisted)+" of the project figures; --all lists them")
	}
	if diff := (figures{global.Counted - every.counted, global.Spent - every.spent, global.Notional - every.notional}); diff != (figures{}) {
		doc.Warnings = append(doc.Warnings, "the project figures and the sum of its repositories' do not add up: the project counters are over by "+countersText(diff)+" (negative: under); the day's rollover keeps the raw nodes when they differ")
	}

	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	printShow(cmd.OutOrStdout(), doc, o.compute)
	return nil
}

// repoCompute is today's compute dollars per repository, keyed the way the
// document's rows are. It reads the runs bucket through the same facts reader
// the spend report uses (budget.BucketFacts), narrowed to the repositories the
// document lists: a run's compute is in its own result.json and nowhere in the
// database. Only the repositories the day recorded an estimate for are in the
// map -- every other row stays unknown rather than 0. A read failure is a
// warning and no figures at all: `budget show` is what the console's rail and
// Projects screens call, so --compute may never fail the command.
func repoCompute(ctx context.Context, lc *localcfg.Config, day int64, slugs []string, warn func(string)) map[string]computeFigure {
	// No repository is not every repository: an empty Slugs is the whole
	// bucket to budget.BucketFacts, and a document that lists no repository
	// has no row to carry a figure for. Answer without opening the bucket.
	if len(slugs) == 0 {
		return map[string]computeFigure{}
	}
	facts := &lazyFacts{lc: lc, warn: warn, slugs: slugs}
	defer facts.close()
	byDay, _, err := facts.Facts(ctx, day, day)
	if err != nil {
		warn("compute is unavailable: the runs bucket could not be read: " + err.Error())
		return nil
	}
	out := map[string]computeFigure{}
	unusable := map[string]bool{}
	for _, f := range byDay[day] {
		if !f.ComputeEstimated {
			continue
		}
		k := budget.Key(f.Slug)
		if unusable[k] {
			continue
		}
		g := out[k]
		g.usd += f.ComputeUSD
		// A figure no reader can add up to a dollar amount is not one the
		// document may carry: json.Marshal refuses a non-finite number, and a
		// saturated or partial one would be a figure nobody can trust, so the
		// row goes back to unknown and a warning says why. internal/budget's
		// rollup guards the same figure, so the archived day record carries no
		// such number either.
		if math.IsNaN(g.usd) || math.IsInf(g.usd, 0) {
			unusable[k] = true
			delete(out, k)
			warn(fmt.Sprintf("compute for %s is unavailable: the day's figures do not add up to a number", f.Slug))
			continue
		}
		g.known = true
		out[k] = g
	}
	return out
}

func keySet[V any](m map[string]V) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func countIn(all map[string]bool, keys map[string]bool) int {
	n := 0
	for k := range all {
		if keys[k] {
			n++
		}
	}
	return n
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usdText(v *float64) string {
	if v == nil {
		return "not set"
	}
	m, _ := pricing.FromUSD(*v)
	return money(m)
}

// figures are dollar counters, comparable (budget.Counters holds a map).
type figures struct{ counted, spent, notional budget.Micros }

func countersText(c figures) string {
	return "counted " + money(c.counted) + ", spent " + money(c.spent) + ", notional " + money(c.notional)
}

func usdVal(v float64) string {
	m, _ := pricing.FromUSD(math.Max(v, 0))
	return money(m)
}

// computeText is compute_usd in the text view: `n/a`, never $0.00, where the
// day recorded no estimate -- the same figure, and the same rule, the JSON
// writes as null.
func computeText(f *computeFigure) string {
	if f == nil || !f.known {
		return "n/a"
	}
	return usdVal(f.usd)
}

func killText(k *showKill) string {
	if k == nil {
		return "off"
	}
	s := "ON"
	if k.At != "" {
		s += " since " + oneLine(k.At)
	}
	if k.By != "" {
		s += " by " + oneLine(k.By)
	}
	if k.Reason != "" {
		s += ": " + oneLine(k.Reason)
	}
	return s
}

// repoScope names a shown repository in a warning line: its name, or its slug
// when the config names none, the way printShow's own heading does.
func repoScope(r showRepo) string {
	if r.Repo != "" {
		return r.Repo + "'s"
	}
	return r.Slug + "'s"
}

func printShow(w io.Writer, d *budgetShowDoc, showCompute bool) {
	mode := d.Mode
	if mode == "" {
		mode = "not set (acts as observe)"
	}
	fmt.Fprintf(w, "mode: %s\n", mode)
	if d.Mode == budget.ModeEnforce {
		fmt.Fprintln(w, "  caps are enforced")
	} else {
		fmt.Fprintln(w, "  caps are advisory; kill switches and the reserve limit apply")
	}
	fmt.Fprintf(w, "day: %s (UTC)\n", d.Day)
	fmt.Fprintf(w, "largest single lease (max reserve): %s\n", usdText(d.MaxReserveUSD))
	g := d.Global
	fmt.Fprintf(w, "\nproject %s\n", oneLine(d.Project))
	fmt.Fprintf(w, "  daily cap %s, per-run cap %s\n", usdText(g.DailyCapUSD), usdText(g.PerRunCapUSD))
	fmt.Fprintf(w, "  today: counted %s, spent %s, notional %s (subscription, not billed)\n", usdVal(g.CountedUSD), usdVal(g.SpentUSD), usdVal(g.NotionalUSD))
	fmt.Fprintf(w, "  headroom: %s\n  kill switch: %s\n", usdText(g.HeadroomUSD), killText(g.Kill))
	fmt.Fprintf(w, "defaults for repositories without a cap of their own: daily %s, per-run %s\n", usdText(d.Defaults.RepoDailyCapUSD), usdText(d.Defaults.RepoPerRunCapUSD))
	for _, r := range d.Repos {
		name := r.Slug
		if r.Repo != "" {
			name = r.Repo + " (" + r.Slug + ")"
		}
		fmt.Fprintf(w, "\nrepository %s\n", oneLine(name))
		src := ""
		if r.CapSource != "none" {
			src = " (" + r.CapSource + ")"
		}
		// The per-run cap is the effective one, with the scope it comes from:
		// a repository without a cap of its own is bounded by the project's
		// global one (D6).
		psrc := " (" + r.PerRunCapSource + ")"
		if r.PerRunCapSource == "none" {
			psrc = " (no per-run cap anywhere)"
		}
		fmt.Fprintf(w, "  daily cap %s%s, per-run cap %s%s\n", usdText(r.DailyCapUSD), src, usdText(r.PerRunCapUSD), psrc)
		fmt.Fprintf(w, "  today: counted %s, spent %s, notional %s (subscription, not billed)\n", usdVal(r.CountedUSD), usdVal(r.SpentUSD), usdVal(r.NotionalUSD))
		if showCompute {
			fmt.Fprintf(w, "  compute: %s\n", computeText(r.ComputeUSD))
		}
		fmt.Fprintf(w, "  headroom: %s\n  kill switch: %s\n", usdText(r.HeadroomUSD), killText(r.Kill))
		if len(r.Running) == 0 {
			fmt.Fprintln(w, "  running: none")
			continue
		}
		fmt.Fprintln(w, "  running:")
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, e := range r.Running {
			state := oneLine(clipRunes(e.Stage, 200))
			if e.Round > 0 {
				state += " r" + strconv.Itoa(e.Round)
			}
			if e.Halted != "" {
				state += " HALTED " + oneLine(clipRunes(e.Halted, 200))
			}
			if e.Stale {
				state += " (no heartbeat)"
			}
			fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\t%s\n", oneLine(clipRunes(e.Run, 200)), oneLine(clipRunes(e.Workflow, 200)), state, usdVal(e.SpentUSD), oneLine(clipRunes(e.Title, 200)))
		}
		_ = tw.Flush()
	}
	if len(d.Repos) == 0 {
		fmt.Fprintln(w, "\nno repositories to show (--all lists every repository in the database)")
	}
	if showCompute {
		fmt.Fprintln(w, "\ncompute is an estimate, priced from each run's own price table, and counts the")
		fmt.Fprintln(w, "day's runs that started today and recorded a result: today's is partial, and")
		fmt.Fprintln(w, "n/a means none of the day's runs recorded one")
	}
	for _, m := range d.Warnings {
		fmt.Fprintf(w, "\nnote: %s\n", oneLine(m))
	}
}

// ---------------------------------------------------------------- set

type budgetSetOptions struct {
	cloud                  cloudOptions
	global, defaults       bool
	repo                   string
	daily, perRun, reserve float64
	mode                   string
	clear, yes             bool
}

func newBudgetSetCmd() *cobra.Command {
	var o budgetSetOptions
	cmd := &cobra.Command{
		Use:   "set (--global | --defaults | --repo [owner/name]) [--daily USD] [--per-run USD] [--max-reserve USD] [--mode observe|enforce] | --clear",
		Short: "Set a cap, the lease limit or the mode (budget admins)",
		Long: "Set caps in the project's budget database, with an ETag-guarded write that shows\n" +
			"the old and the new value.\n\n" +
			"  --global            the project's daily and per-run caps; also takes --max-reserve and --mode\n" +
			"  --defaults          the caps of every repository that has none of its own\n" +
			"  --repo [owner/name] one repository's caps; --repo with no value means the\n" +
			"                      repository this checkout belongs to, which needs no\n" +
			"                      owner/name at all\n\n" +
			"--max-reserve (the largest single lease) and --mode (observe, or enforce: caps\n" +
			"bite) are project-wide, so they go with --global. Amounts are US dollars from 0\n" +
			"to 100000, and a per-run cap may not exceed the daily cap of the same scope.\n" +
			"--clear removes the scope's caps.\n\n" +
			"--repo owner/name always works, from anywhere; the bare --repo is refused\n" +
			"outside a checkout whose origin names a repository, and never stands for the\n" +
			"project-wide caps. kill and resume take --repo the same way. preview needs no\n" +
			"bare form: left out, --repo already means this checkout's repository. show is\n" +
			"the one whose empty --repo is not the checkout (it means no repository\n" +
			"filter), so it takes owner/name.\n\n" +
			"Raising a cap (setting one that was unset counts), or lowering the mode to\n" +
			"observe, loosens what runs may spend, so it asks you to type the project's name\n" +
			"(or pass --yes). Lowering needs no confirmation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetSet(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.global, "global", false, "the project's own caps")
	f.BoolVar(&o.defaults, "defaults", false, "the default caps of repositories without their own")
	f.StringVar(&o.repo, "repo", "", "one repository's caps, given as owner/name (no value: this checkout's origin)")
	f.Float64Var(&o.daily, "daily", 0, "the daily cap, in US dollars")
	f.Float64Var(&o.perRun, "per-run", 0, "the per-run cap, in US dollars")
	f.Float64Var(&o.reserve, "max-reserve", 0, "the largest single lease, in US dollars (with --global)")
	f.StringVar(&o.mode, "mode", "", "observe or enforce (with --global)")
	f.BoolVar(&o.clear, "clear", false, "remove the scope's caps")
	f.BoolVar(&o.yes, "yes", false, "confirm a raise without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// setPlan is a validated set command.
type setPlan struct {
	scope                  string // global, defaults or repo
	repo                   string
	daily, perRun, reserve *budget.Micros
	mode                   string
	clear                  bool
}

func parsePlan(cmd *cobra.Command, o *budgetSetOptions) (*setPlan, error) {
	// A bare --repo (rewritten to --repo= at the argv boundary) is a scope
	// with an empty value, so the flag's presence is what names the scope and
	// not the string it carries: read off o.repo != "" and the bare form is
	// not a scope at all, which is "name one scope" for a command that named
	// one.
	n := 0
	for _, b := range []bool{o.global, o.defaults, cmd.Flags().Changed("repo")} {
		if b {
			n++
		}
	}
	if n != 1 {
		return nil, userErr("name one scope: --global, --defaults or --repo [owner/name]")
	}
	p := &setPlan{clear: o.clear, repo: o.repo}
	switch {
	case o.global:
		p.scope = "global"
	case o.defaults:
		p.scope = "defaults"
	default:
		p.scope = "repo"
	}
	fl := cmd.Flags()
	amount := func(name string, v float64) (*budget.Micros, error) {
		if !fl.Changed(name) {
			return nil, nil
		}
		m, err := pricing.FromUSD(v)
		if err != nil {
			return nil, userErr("--%s %v", name, err)
		}
		return &m, nil
	}
	var err error
	if p.daily, err = amount("daily", o.daily); err != nil {
		return nil, err
	}
	if p.perRun, err = amount("per-run", o.perRun); err != nil {
		return nil, err
	}
	if p.reserve, err = amount("max-reserve", o.reserve); err != nil {
		return nil, err
	}
	if fl.Changed("mode") {
		if o.mode != budget.ModeObserve && o.mode != budget.ModeEnforce {
			return nil, userErr("--mode %q: use observe or enforce", o.mode)
		}
		p.mode = o.mode
	}
	values := p.daily != nil || p.perRun != nil || p.reserve != nil || p.mode != ""
	switch {
	case p.clear && values:
		return nil, userErr("--clear removes the scope's caps and takes no values")
	case !p.clear && !values:
		return nil, userErr("nothing to set: give --daily, --per-run, --max-reserve, --mode or --clear")
	case (p.reserve != nil || p.mode != "") && p.scope != "global":
		return nil, userErr("--max-reserve and --mode are project-wide: use them with --global")
	}
	if p.daily != nil && p.perRun != nil && *p.perRun > *p.daily {
		return nil, userErr("--per-run %s exceeds --daily %s: a run's cap can't be more than its day's", money(*p.perRun), money(*p.daily))
	}
	return p, nil
}

// nodeEdit is one node's change.
type nodeEdit struct {
	path   string
	etag   string
	newVal any // nil deletes the node
	noop   bool
	lines  []string
	raise  bool
}

type microField struct {
	key, name string
	set       *budget.Micros
}

func microsOf(raw json.RawMessage) (*budget.Micros, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m budget.Micros
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("a cap in the database is not a whole number of micro-dollars: %v", err)
	}
	return &m, nil
}

// editMicroNode applies the fields to old (the node as stored), recording
// lines for the change and whether any field loosens. It returns the node's
// final values by key.
func editMicroNode(e *nodeEdit, old map[string]json.RawMessage, fields []microField) (map[string]*budget.Micros, error) {
	next := map[string]json.RawMessage{}
	maps.Copy(next, old)
	final := map[string]*budget.Micros{}
	changed := false
	for _, f := range fields {
		cur, err := microsOf(old[f.key])
		if err != nil {
			return nil, remote(err)
		}
		final[f.key] = cur
		if f.set == nil {
			continue
		}
		final[f.key] = f.set
		if cur != nil && *cur == *f.set {
			e.lines = append(e.lines, fmt.Sprintf("%s: %s (unchanged)", f.name, money(*f.set)))
			continue
		}
		changed = true
		if cur == nil || *f.set > *cur {
			e.raise = true
		}
		e.lines = append(e.lines, fmt.Sprintf("%s: %s -> %s", f.name, moneyPtr(cur), money(*f.set)))
		b, _ := json.Marshal(*f.set)
		next[f.key] = b
	}
	e.noop = !changed
	e.newVal = next
	return final, nil
}

// buildEdits reads the nodes plan touches and works out their changes.
func buildEdits(ctx context.Context, db *rtdb.Client, p *setPlan, slug string) ([]*nodeEdit, error) {
	read := func(path string, out any) (string, bool, error) { return db.GetETag(ctx, path, out) }
	var edits []*nodeEdit

	if p.mode == budget.ModeEnforce {
		// As init does: enforce needs the project-wide caps, and the two
		// absences cost different things (D6). Caps set by this same
		// invocation count.
		var g budget.GlobalCaps
		if _, err := db.Get(ctx, budget.PathCapsGlobal, &g); err != nil {
			return nil, remote(err)
		}
		var missing []string
		if p.daily == nil && g.DailyMicros == nil {
			missing = append(missing, "  dailyMicros: the rules read an absent cap as a refusal, so every lease is refused and every run halts")
		}
		if p.perRun == nil && g.PerRunMicros == nil {
			missing = append(missing, "  perRunMicros: a run whose repository has no per-run cap of its own and no default has none to compare against and halts (a repository with its own cap keeps it)")
		}
		if len(missing) > 0 {
			return nil, userErr("--mode enforce needs the database's global caps (%s), and some are missing:\n%s\nSet them first (or in this command) with fugaro budget set --global --daily <usd> --per-run <usd>", budget.PathCapsGlobal, strings.Join(missing, "\n"))
		}
	}
	if p.mode != "" {
		var cur string
		etag, found, err := read(budget.PathMode, &cur)
		if err != nil {
			return nil, err
		}
		e := &nodeEdit{path: budget.PathMode, etag: etag, newVal: p.mode}
		switch {
		case found && cur == p.mode:
			e.noop = true
			e.lines = []string{fmt.Sprintf("mode: %s (unchanged)", p.mode)}
		default:
			shown := cur
			if !found {
				shown = "not set (observe)"
			}
			e.lines = []string{fmt.Sprintf("mode: %s -> %s", oneLine(shown), p.mode)}
			// Only enforce -> observe loosens. Unset acts as observe.
			e.raise = cur == budget.ModeEnforce && p.mode == budget.ModeObserve
		}
		edits = append(edits, e)
	}

	if p.reserve != nil {
		var old map[string]json.RawMessage
		etag, _, err := read(budget.PathLimits, &old)
		if err != nil {
			return nil, err
		}
		e := &nodeEdit{path: budget.PathLimits, etag: etag}
		if _, err := editMicroNode(e, old, []microField{{"maxReserveMicros", "max reserve", p.reserve}}); err != nil {
			return nil, err
		}
		edits = append(edits, e)
	}

	if p.clear || p.daily != nil || p.perRun != nil {
		path, dk, pk := budget.PathCapsGlobal, "dailyMicros", "perRunMicros"
		label := "global caps"
		switch p.scope {
		case "defaults":
			path, dk, pk, label = budget.PathCapsDefaults, "repoDailyMicros", "repoPerRunMicros", "default repository caps"
		case "repo":
			path, label = budget.PathCapsRepo(slug), "caps of "+p.repo
		}
		var old map[string]json.RawMessage
		etag, found, err := read(path, &old)
		if err != nil {
			return nil, err
		}
		e := &nodeEdit{path: path, etag: etag}
		if p.clear {
			e.newVal = nil
			e.noop = !found
			if found && p.scope == "defaults" {
				// Every repository without a node of its own falls back to
				// the defaults; clearing them hands those repositories to
				// the global per-run cap (D6), which is a loosening when
				// that cap is higher, and ends every run there when there
				// is none. Compare the effective per-run cap of such a
				// repository before and after, as the repository's own
				// clear does.
				var g budget.GlobalCaps
				if _, _, err := read(budget.PathCapsGlobal, &g); err != nil {
					return nil, err
				}
				def := &budget.DefaultCaps{}
				if raw, ok := old[pk]; ok {
					m, err := microsOf(raw)
					if err != nil {
						e.raise = true // unreadable: be careful
					} else {
						def.RepoPerRunMicros = m
					}
				}
				before, beforeOK := budget.Caps{Defaults: def, Global: &g}.PerRun()
				after, afterOK := budget.Caps{Global: &g}.PerRun()
				// before unset too: the per-run side is unchanged
				// (clearing a daily-only default node only tightens).
				if beforeOK && (!afterOK || after > before) {
					e.raise = true
				}
			}
			if found && p.scope == "repo" {
				// The repository falls back to the defaults: a higher
				// default, or none, is a loosening to confirm.
				var def map[string]json.RawMessage
				if _, _, err := read(budget.PathCapsDefaults, &def); err != nil {
					return nil, err
				}
				for _, pair := range [][2]string{{"dailyMicros", "repoDailyMicros"}, {"perRunMicros", "repoPerRunMicros"}} {
					own, err1 := microsOf(old[pair[0]])
					dv, err2 := microsOf(def[pair[1]])
					if err1 != nil || err2 != nil {
						e.raise = true // unreadable: be careful
						continue
					}
					if own != nil && (dv == nil || *dv > *own) {
						e.raise = true
					}
				}
			}
			if found {
				e.lines = []string{label + ": removed"}
				for _, f := range []struct{ k, n string }{{dk, "daily"}, {pk, "per-run"}} {
					if m, err := microsOf(old[f.k]); err == nil && m != nil {
						e.lines = append(e.lines, fmt.Sprintf("  was %s %s", f.n, money(*m)))
					}
				}
			} else {
				e.lines = []string{label + ": not set (nothing to clear)"}
			}
		} else {
			fields := []microField{{dk, label + " daily", p.daily}, {pk, label + " per-run", p.perRun}}
			final, err := editMicroNode(e, old, fields)
			if err != nil {
				return nil, err
			}
			if d, r := final[dk], final[pk]; d != nil && r != nil && *r > *d {
				return nil, userErr("%s: the per-run cap %s would exceed the daily cap %s; set both, or the smaller first", label, money(*r), money(*d))
			}
			if p.scope == "repo" && !e.noop {
				m := e.newVal.(map[string]json.RawMessage)
				if _, ok := m["repo"]; !ok {
					b, _ := json.Marshal(p.repo)
					m["repo"] = b
				}
			}
		}
		edits = append(edits, e)
	}
	return edits, nil
}

// namedRepo is the repository a budget command was pointed at: --repo's value
// when it carries one, and the repository this checkout belongs to when it
// does not (a bare --repo, which NormalizeRepoArgs rewrote to --repo=).
//
// The reading of the checkout's origin is originRepo's -- the one helper run,
// ls, validate and secrets use, over repoFromOrigin, the one parser of a
// remote URL (https, ssh or scp-like, github.com or bitbucket alike). What is
// not reused is originRepo's message, which is written for a command that was
// given no --repo at all. Nothing is guessed here: outside a checkout, or with
// an origin that names no repository, the command refuses and names the fix,
// and it never falls back to the project-wide caps, which are a scope the
// person who typed --repo did not ask for.
//
// The shape check is the one resolveSecretRepo makes for the same flag, and it
// is what keeps a mistyped value a refusal: `--repo acme-app` reaches here as
// the value acme-app -- the argv rewrite leaves a --repo with a token after it
// alone -- and it must never be read as "no value" and answered with the
// checkout's repository instead.
func namedRepo(ctx context.Context, repo string) (string, error) {
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return "", userErr("--repo with no value means the repository this checkout belongs to, and this is not a checkout with an origin remote that names one: pass --repo owner/name")
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", userErr("--repo: %v", err) // CanonicalRepo's error doesn't quote its input
	}
	return repo, nil
}

func runBudgetSet(cmd *cobra.Command, o *budgetSetOptions) error {
	ctx := cmd.Context()
	p, err := parsePlan(cmd, o)
	if err != nil {
		return err
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was set"); err != nil {
		return err
	}
	slug := ""
	if p.scope == "repo" {
		if p.repo, err = namedRepo(ctx, p.repo); err != nil {
			return err
		}
		if slug, err = repoSlugOf(ctx, lc, p.repo); err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	for attempt := 1; attempt <= setAttempts; attempt++ {
		edits, err := buildEdits(ctx, db, p, slug)
		if err != nil {
			var ee *ExitError
			if errors.As(err, &ee) {
				return err
			}
			return dbErr(lc, err, false)
		}
		raise, changed := false, false
		for _, e := range edits {
			for _, l := range e.lines {
				fmt.Fprintf(out, "%s: %s\n", e.path, oneLine(l))
			}
			raise = raise || (e.raise && !e.noop)
			changed = changed || !e.noop
		}
		if !changed {
			fmt.Fprintln(out, "nothing changed")
			return nil
		}
		if raise {
			if err := confirmTyped(cmd, in, lc, o.yes, "this loosens the project's budget: runs may spend more than before"); err != nil {
				return err
			}
		}
		// Tightening first, loosening last: a failure half way never
		// leaves a loosened budget behind a tightened one.
		slices.SortStableFunc(edits, func(a, b *nodeEdit) int { return btoi(a.raise) - btoi(b.raise) })
		stale := false
		var written, pending []string
		for _, e := range edits {
			if !e.noop {
				pending = append(pending, e.path)
			}
		}
		for _, e := range edits {
			if e.noop {
				continue
			}
			err := db.PutIfMatch(ctx, e.path, e.etag, e.newVal)
			if errors.Is(err, rtdb.ErrPrecondition) {
				stale = true
				break
			}
			if err != nil {
				err = dbErr(lc, err, true)
				if len(written) > 0 {
					err = fmt.Errorf("%w\nthe update was only partly applied. written: %s; not written: %s", err, strings.Join(written, ", "), strings.Join(pending, ", "))
				}
				return err
			}
			written = append(written, e.path)
			pending = pending[1:]
			fmt.Fprintf(out, "set %s\n", e.path)
		}
		if !stale {
			return nil
		}
		fmt.Fprintln(out, "changed by someone else meanwhile; reading it again")
	}
	return remote(fmt.Errorf("the budget database kept changing under the write (%d attempts); nothing further was written; run it again", setAttempts))
}

// ---------------------------------------------------------- kill, resume

type budgetKillOptions struct {
	cloud  cloudOptions
	all    bool
	repo   string
	reason string
	yes    bool
}

func newBudgetKillCmd(kill bool) *cobra.Command {
	var o budgetKillOptions
	use, short, long := "resume (--all | --repo [owner/name]) [--reason TEXT]", "Clear a kill switch (budget admins)",
		"Clear the project's kill switch (--all) or one repository's (--repo [owner/name]),\n"+
			"recording who and when. --repo with no value means the repository this checkout\n"+
			"belongs to, as it does for set. Asks you to type the project's name (or pass\n"+
			"--yes): runs start again."
	if kill {
		use, short, long = "kill (--all | --repo [owner/name]) [--reason TEXT]", "Halt every run of the project or of one repository (budget admins)",
			"Set the project's kill switch (--all) or one repository's (--repo [owner/name]),\n"+
				"recording who, when and why. --repo with no value means the repository this\n"+
				"checkout belongs to, as it does for set. show takes owner/name (its empty\n"+
				"--repo means no repository filter, not a checkout), and preview already\n"+
				"means this checkout's repository without the flag.\n"+
				"Runs in flight halt within seconds (a run in its last stage finishes pushing\n"+
				"its work first) and new runs refuse. --all asks you to type the project's name\n"+
				"(or pass --yes); one repository needs no confirmation."
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Long: long, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetKill(cmd, &o, kill) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.all, "all", false, "the whole project")
	f.StringVar(&o.repo, "repo", "", "one repository, given as owner/name (no value: this checkout's origin)")
	f.StringVar(&o.reason, "reason", "", "why (recorded, shown by budget show and watch)")
	f.BoolVar(&o.yes, "yes", false, "confirm without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runBudgetKill(cmd *cobra.Command, o *budgetKillOptions, kill bool) error {
	ctx := cmd.Context()
	// The flag's presence names the scope, not the string it carries: a bare
	// --repo is a scope with an empty value (see parsePlan).
	if o.all == cmd.Flags().Changed("repo") {
		return userErr("name one scope: --all or --repo [owner/name]")
	}
	if utf8.RuneCountInString(o.reason) > maxKillReason {
		return userErr("--reason is %d characters; keep it to %d", utf8.RuneCountInString(o.reason), maxKillReason)
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	declined := "nothing was resumed"
	if kill {
		declined = "nothing was killed"
	}
	if err := requireOwnBudgetDB(ctx, lc, db, declined); err != nil {
		return err
	}
	me, err := lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	path, what := budget.PathKillGlobal, "project "+lc.Name
	if !o.all {
		repo, err := namedRepo(ctx, o.repo)
		if err != nil {
			return err
		}
		slug, err := repoSlugOf(ctx, lc, repo)
		if err != nil {
			return err
		}
		path, what = budget.PathKillRepo(slug), "repository "+repo
	}
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	res, err := budget.SetKill(ctx, db, path, kill, me, o.reason, budget.KillHooks{
		Confirm: func(budget.Kill) error {
			switch {
			case kill && o.all:
				return confirmTyped(cmd, in, lc, o.yes, "this halts every run of the project and refuses new ones")
			case !kill:
				return confirmTyped(cmd, in, lc, o.yes, "this lets "+what+" start and continue runs again")
			}
			return nil
		},
		Conflict: func() { fmt.Fprintln(out, "changed by someone else meanwhile; reading it again") },
		Now:      budgetNow,
	})
	var ke *budget.KillError
	switch {
	case errors.As(err, &ke):
		return dbErr(lc, err, ke.Write)
	case errors.Is(err, budget.ErrKillContention):
		return remote(fmt.Errorf("the kill switch kept changing under the write (%d attempts); run it again", setAttempts))
	case err != nil:
		return err
	}
	cur := res.Previous
	if res.Already {
		if kill {
			fmt.Fprintf(out, "%s is already killed (by %s at %s: %s); nothing changed\n", what, oneLine(cur.By), ms(cur.At), oneLine(cur.Reason))
		} else {
			fmt.Fprintf(out, "%s is not killed; nothing changed\n", what)
		}
		return nil
	}
	if kill {
		fmt.Fprintf(out, "killed %s (by %s). Running runs halt within seconds; undo with fugaro budget resume\n", what, oneLine(me))
	} else {
		fmt.Fprintf(out, "resumed %s (by %s)\n", what, oneLine(me))
		if !o.all {
			var g budget.Kill
			if _, err := db.Get(ctx, budget.PathKillGlobal, &g); err == nil && g.On {
				fmt.Fprintf(out, "warning: the project-wide kill switch is still on (by %s): %s stays halted until fugaro budget resume --all\n", oneLine(g.By), what)
			}
		}
	}
	return nil
}

// ------------------------------------------------------------- prices

type budgetPricesOptions struct {
	cloud cloudOptions
	json  bool
}

func newBudgetPricesCmd() *cobra.Command {
	var o budgetPricesOptions
	cmd := &cobra.Command{
		Use:   "prices",
		Short: "Show the effective model price table, offline",
		Long: "Show the model prices the budget accounts with: the table built into this\n" +
			"fugaro, its source and the day it was last checked (a warning when that is over\n" +
			"90 days ago), with the selected project config's model_prices applied and\n" +
			"marked local. A model a provider route claims is priced from the built-in\n" +
			"provider price catalog instead: its CATALOG column says which tier and which\n" +
			"end of that tier's range is charged, and the catalog has its own source, date\n" +
			"and 30-day staleness warning. It never reaches the network.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetPrices(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

type priceRow struct {
	ID               string   `json:"id"`
	Aliases          []string `json:"aliases,omitempty"`
	InputPerM        float64  `json:"input_per_m"`
	OutputPerM       float64  `json:"output_per_m"`
	CacheReadPerM    float64  `json:"cache_read_per_m"`
	CacheWrite5mPerM float64  `json:"cache_write_5m_per_m"`
	CacheWrite1hPerM float64  `json:"cache_write_1h_per_m"`
	WebSearchPer1k   float64  `json:"web_search_per_1k"`
	LongContext      string   `json:"long_context,omitempty"`
	Local            bool     `json:"local"`
	// Unverified marks a placeholder price nobody has checked; Source and
	// CheckedAt say where the row came from (empty: the table's own).
	Unverified bool   `json:"unverified"`
	Source     string `json:"source,omitempty"`
	CheckedAt  string `json:"checked_at,omitempty"`
	// Catalog is the provenance of a row the built-in provider price
	// catalog priced — its own tier, the end of that tier's range charged
	// and the endpoints behind it. nil on every other row: Source and
	// CheckedAt above stay the Claude table's, and the catalog's are its
	// own (design §7, plan Task 5 step 1).
	Catalog *priceCatalog `json:"catalog,omitempty"`
}

// priceCatalog is a catalog-priced row's provenance, as `budget prices
// --json` prints it.
type priceCatalog struct {
	Tier string `json:"tier"` // zdr | all
	// Bound is the end of the tier's range charged: min (cheapest) or max
	// (the dearest, whether price_policy asked for it or the catalog is
	// stale).
	Bound string `json:"bound"`
	// Endpoints is every endpoint the model is served by; ZdrEndpoints is
	// how many of them are ZDR-eligible (the zdr tier's own count).
	Endpoints    int    `json:"endpoints"`
	ZdrEndpoints int    `json:"zdr_endpoints"`
	Source       string `json:"source"`
	CheckedAt    string `json:"checked_at"`
	Stale        bool   `json:"stale"`
	// TierInputMaxPerM and TierOutputMaxPerM are the dearest input and
	// output price per million tokens the tier prices: the top of the range
	// the bound above was taken from, and so the most a call the provider
	// serves from the dearest endpoint can be billed (design D6, P11).
	TierInputMaxPerM  float64 `json:"tier_input_max_per_m"`
	TierOutputMaxPerM float64 `json:"tier_output_max_per_m"`
}

type pricesDoc struct {
	Source    string     `json:"source"`
	CheckedAt string     `json:"checked_at"`
	AgeDays   int        `json:"age_days"`
	Stale     bool       `json:"stale"`
	Models    []priceRow `json:"models"`
	// CatalogSource, CatalogCheckedAt, CatalogAgeDays and CatalogStale are
	// the built-in provider price catalog's own provenance, kept apart from
	// the embedded Claude table's above. Empty (and false) when no row is
	// catalog-priced.
	CatalogSource    string `json:"catalog_source,omitempty"`
	CatalogCheckedAt string `json:"catalog_checked_at,omitempty"`
	CatalogAgeDays   int    `json:"catalog_age_days,omitempty"`
	CatalogStale     bool   `json:"catalog_stale,omitempty"`
	// Refused is the models a provider route claims that the built-in
	// provider price catalog cannot price, with the reason: they have no
	// row above, and a run pinned to one is refused at bootstrap. Empty
	// when every claimed model has a row.
	Refused []priceRefused `json:"refused,omitempty"`
}

// priceRefused is one model with no row and the reason the catalog recorded:
// a require_zdr route on a model with no ZDR-eligible endpoint, or a rate the
// table cannot express.
type priceRefused struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func runBudgetPrices(cmd *cobra.Command, o *budgetPricesOptions) error {
	errw := cmd.ErrOrStderr()
	// A project config, when one is selected, contributes its routes and its
	// overrides. No config is fine (prices needs no cloud); a config that is
	// selected and wrong is an error.
	lc, err := pricesConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	table, ov, err := priceTable(lc, budgetNow())
	if err != nil {
		return userErr("%v", err)
	}
	local := map[string]bool{}
	for k := range ov {
		if m, ok := table.Lookup(k); ok {
			local[m.ID] = true
		}
	}

	doc := pricesDoc{Source: table.Source, CheckedAt: table.CheckedAt, Models: []priceRow{}}
	if t, err := time.Parse("2006-01-02", table.CheckedAt); err == nil {
		age := budgetNow().Sub(t)
		doc.AgeDays = int(age.Hours() / 24)
		doc.Stale = age > priceStaleAfter
	}
	ids := make([]string, 0, len(table.Models))
	for id := range table.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := table.Models[id]
		r := m.Rates
		row := priceRow{ID: id, Aliases: m.Aliases, InputPerM: r.InputPerM, OutputPerM: r.OutputPerM,
			CacheReadPerM: r.InputPerM * r.CacheRead, CacheWrite5mPerM: r.InputPerM * r.CacheWrite5m,
			CacheWrite1hPerM: r.InputPerM * r.CacheWrite1h, WebSearchPer1k: r.WebSearchPer1k, Local: local[id],
			Unverified: m.Unverified, Source: m.PriceSource, CheckedAt: m.PriceCheckedAt}
		if c := m.Catalog; c != nil {
			row.Catalog = &priceCatalog{Tier: c.Tier, Bound: c.Bound, Endpoints: c.Endpoints, ZdrEndpoints: c.ZdrEndpoints,
				Source: c.Source, CheckedAt: c.CheckedAt, Stale: c.Stale,
				TierInputMaxPerM: c.TierInputMax, TierOutputMaxPerM: c.TierOutputMax}
			if doc.CatalogSource == "" {
				doc.CatalogSource, doc.CatalogCheckedAt, doc.CatalogStale = c.Source, c.CheckedAt, c.Stale
				if t, err := time.Parse("2006-01-02", c.CheckedAt); err == nil {
					doc.CatalogAgeDays = int(budgetNow().Sub(t).Hours() / 24)
				}
			}
		}
		if r.LongContext != nil {
			row.LongContext = fmt.Sprintf("above %d input tokens: $%g in / $%g out", r.LongContext.AboveInputTokens, r.LongContext.InputPerM, r.LongContext.OutputPerM)
		}
		doc.Models = append(doc.Models, row)
	}
	// A model a route claims that the catalog cannot price has no row: the
	// owner is told which and why, rather than left with a model missing
	// from the table. Nothing is refused here — the table is the run's, and
	// a run that never names the model charges nothing — but a pin on one
	// is refused at bootstrap (design D9, P8).
	for _, r := range slices.Sorted(maps.Keys(table.Refused)) {
		doc.Refused = append(doc.Refused, priceRefused{ID: r, Reason: table.Refused[r]})
		fmt.Fprintf(errw, "warning: %s has no row: %s\n", oneLine(r), oneLine(table.Refused[r]))
	}
	if doc.Stale {
		fmt.Fprintf(errw, "warning: the built-in price table was last checked %s, older than 90 days (%d days ago); check %s and set model_prices in the project config where it differs\n",
			doc.CheckedAt, doc.AgeDays, doc.Source)
	}
	if doc.CatalogStale {
		// The catalog's own threshold is 30 days, not the Claude table's 90
		// (design D10): a marketplace price moves for reasons an age check
		// cannot see, so a stale catalog charges every model it prices at
		// the dearest end of its tier's range rather than under-pricing.
		fmt.Fprintf(errw, "warning: the built-in provider price catalog (%s) was last checked %s, older than 30 days (%d days ago); regenerate it with scripts/gen-price-catalog.sh and commit the result (catalog-priced models are charged the dearest end of their tier's range until then)\n",
			doc.CatalogSource, doc.CatalogCheckedAt, doc.CatalogAgeDays)
	}
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "model prices, US dollars per million tokens (source %s, checked %s)\n", doc.Source, doc.CheckedAt)
	if doc.CatalogCheckedAt != "" {
		// The table holds two sources once a route is priced from the
		// catalog: the embedded Claude table's own source and date are the
		// header line above, and the catalog's are this one (design §7).
		fmt.Fprintf(w, "provider price catalog: %s, checked %s (%d days ago)\n", doc.CatalogSource, doc.CatalogCheckedAt, doc.CatalogAgeDays)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE 5M\tCACHE WRITE 1H\tWEB SEARCH/1K\tVERIFIED\tCATALOG\tNOTES")
	for _, r := range doc.Models {
		var notes []string
		verified, catalog := "yes", "—"
		if r.Unverified {
			verified = "NO"
			notes = append(notes, "UNVERIFIED placeholder: "+r.Source+" ("+r.CheckedAt+")")
		}
		if c := r.Catalog; c != nil {
			// Which tier and which end of that tier's range is charged;
			// "highest" is the dearest end, whether price_policy asked for
			// it or the catalog is stale (the bound is the compiled one).
			bound := "cheapest"
			if c.Bound == "max" {
				bound = "highest"
			}
			catalog = c.Tier + "/" + bound
			// The endpoints behind the row: the tier's own count, of every
			// endpoint the model is served by. They differ only for a
			// require_zdr route (the zdr tier).
			note := fmt.Sprintf("catalog %s: %d endpoints, checked %s", c.Tier, c.Endpoints, c.CheckedAt)
			if c.Tier == "zdr" {
				note = fmt.Sprintf("catalog %s: %d/%d endpoints, checked %s", c.Tier, c.ZdrEndpoints, c.Endpoints, c.CheckedAt)
			}
			if c.Stale {
				note += " (stale: charged the dearest end)"
			}
			// The numbers behind the range: what the dearest endpoint of
			// the tier prices, which is the size of the undercharge the
			// cheapest end leaves (design D6, P11). Rendered from the row
			// itself, so this and the pin warning cannot drift.
			if m, ok := table.Lookup(r.ID); ok {
				if s := m.CatalogRange(); s != "" {
					note += "; " + s
				}
			}
			notes = append(notes, note)
		}
		if r.Local {
			notes = append(notes, "local override")
		}
		if r.LongContext != "" {
			notes = append(notes, r.LongContext)
		}
		if len(r.Aliases) > 0 {
			notes = append(notes, "aliases "+strings.Join(r.Aliases, ", "))
		}
		fmt.Fprintf(tw, "%s\t$%g\t$%g\t$%g\t$%g\t$%g\t$%g\t%s\t%s\t%s\n", oneLine(r.ID), r.InputPerM, r.OutputPerM, r.CacheReadPerM,
			r.CacheWrite5mPerM, r.CacheWrite1hPerM, r.WebSearchPer1k, verified, catalog, oneLine(strings.Join(notes, "; ")))
	}
	return tw.Flush()
}

// pricesConfig is the selected project config, nil when none is selected. A
// selection that fails because there is nothing to select is not an error
// for prices; any other failure is.
func pricesConfig(cmd *cobra.Command, o cloudOptions) (*localcfg.Config, error) {
	ctx := cmd.Context()
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		explicit := o.config != "" || o.project != "" || os.Getenv("FUGARO_PROJECT") != "" || os.Getenv("FUGARO_CONFIG") != ""
		if explicit {
			return nil, err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "note: no project config selected, so the built-in prices only (%v)\n", err)
		return nil, nil
	}
	return lc, announce(o, sel, lc)
}
