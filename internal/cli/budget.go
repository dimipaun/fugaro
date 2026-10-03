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
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
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
	// budgetNow is the clock of the budget commands; tests replace it.
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
			"Database (design §8). show needs the Viewer role on the Firebase project; set,\n" +
			"kill and resume need the budget-admin role (the GCP project's owners and\n" +
			"editors, and terraform.budget_admins).",
	}
	cmd.AddCommand(newBudgetShowCmd(), newBudgetSetCmd(), newBudgetKillCmd(true), newBudgetKillCmd(false), newBudgetPricesCmd(), newBudgetHistoryCmd())
	return cmd
}

// ---------------------------------------------------------------- shared

// money is a dollar amount for people: whole cents at least, sub-cent
// precision when there is some.
func money(m budget.Micros) string {
	s := strconv.FormatFloat(m.USD(), 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	if strings.HasSuffix(s, ".00") {
		s = strings.TrimSuffix(s, ".00")
	}
	return "$" + s
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
		return userErr("the database at budget.rtdb_url has no /fugaro/project: it is not a Fugaro project's budget database; an operator runs fugaro init --firebase for project %s", lc.Name)
	case name != lc.Name:
		return userErr("the database at budget.rtdb_url belongs to project %q, not %q; fix budget.rtdb_url in project %s's config", oneLine(name), lc.Name, lc.Name)
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
		return userErr("this step needs a confirmation: run it at a terminal and type the project's name, or pass --yes once you have read what it does")
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

// --------------------------------------------------------------- show

type budgetShowOptions struct {
	cloud     cloudOptions
	repo      string
	all, json bool
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
			"capped. Counted is spent plus what runs have reserved and not released.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetShow(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.all, "all", false, "every repository in the database")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
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
	DailyCapUSD  *float64  `json:"daily_cap_usd"`
	PerRunCapUSD *float64  `json:"per_run_cap_usd"`
	CountedUSD   float64   `json:"counted_usd"`
	SpentUSD     float64   `json:"spent_usd"`
	NotionalUSD  float64   `json:"notional_usd"`
	HeadroomUSD  *float64  `json:"headroom_usd"`
	Kill         *showKill `json:"kill"`
}

type showDefaults struct {
	RepoDailyCapUSD  *float64 `json:"repo_daily_cap_usd"`
	RepoPerRunCapUSD *float64 `json:"repo_per_run_cap_usd"`
}

type showRepo struct {
	Slug         string    `json:"slug"`
	Repo         string    `json:"repo,omitempty"`
	DailyCapUSD  *float64  `json:"daily_cap_usd"`
	CapSource    string    `json:"daily_cap_source"` // repo, default or none
	PerRunCapUSD *float64  `json:"per_run_cap_usd"`
	CountedUSD   float64   `json:"counted_usd"`
	SpentUSD     float64   `json:"spent_usd"`
	NotionalUSD  float64   `json:"notional_usd"`
	HeadroomUSD  *float64  `json:"headroom_usd"`
	Kill         *showKill `json:"kill"`
	Running      []showRun `json:"running"`
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
	}
	if cfg.Caps.Global != nil {
		doc.Global.PerRunCapUSD = usdPtr(cfg.Caps.Global.PerRunMicros)
	}
	doc.Global.CountedUSD, doc.Global.SpentUSD, doc.Global.NotionalUSD = global.Counted.USD(), global.Spent.USD(), global.Notional.USD()
	doc.Global.HeadroomUSD = headroom(roomUnder(caps.GlobalDaily, global.Counted))
	doc.Global.Kill = killView(cfg.Kill.Global)
	if cfg.Caps.Defaults != nil {
		doc.Defaults = showDefaults{RepoDailyCapUSD: usdPtr(cfg.Caps.Defaults.RepoDailyMicros), RepoPerRunCapUSD: usdPtr(cfg.Caps.Defaults.RepoPerRunMicros)}
	}

	listed := 0
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		slug := k
		if s, err := budget.Unkey(k); err == nil {
			slug = s
		}
		rc := caps
		if c, ok := cfg.Caps.Repos[k]; ok {
			rc.Repo = &c
		}
		rs := showRepo{Slug: slug, Running: []showRun{}, CapSource: "none"}
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
		if rc.Repo != nil && rc.Repo.PerRunMicros != nil {
			rs.PerRunCapUSD = usdPtr(rc.Repo.PerRunMicros)
		} else if cfg.Caps.Defaults != nil {
			rs.PerRunCapUSD = usdPtr(cfg.Caps.Defaults.RepoPerRunMicros)
		}
		sp := repoSpend[k]
		rs.CountedUSD, rs.SpentUSD, rs.NotionalUSD = sp.Counted.USD(), sp.Spent.USD(), sp.Notional.USD()
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

	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	printShow(cmd.OutOrStdout(), doc)
	return nil
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

func usdVal(v float64) string {
	m, _ := pricing.FromUSD(math.Max(v, 0))
	return money(m)
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

func printShow(w io.Writer, d *budgetShowDoc) {
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
		fmt.Fprintf(w, "  daily cap %s%s, per-run cap %s\n", usdText(r.DailyCapUSD), src, usdText(r.PerRunCapUSD))
		fmt.Fprintf(w, "  today: counted %s, spent %s, notional %s (subscription, not billed)\n", usdVal(r.CountedUSD), usdVal(r.SpentUSD), usdVal(r.NotionalUSD))
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
		Use:   "set (--global | --defaults | --repo R) [--daily USD] [--per-run USD] [--max-reserve USD] [--mode observe|enforce] | --clear",
		Short: "Set a cap, the lease limit or the mode (budget admins)",
		Long: "Set caps in the project's budget database, with an ETag-guarded write that shows\n" +
			"the old and the new value.\n\n" +
			"  --global    the project's daily and per-run caps; also takes --max-reserve and --mode\n" +
			"  --defaults  the caps of every repository that has none of its own\n" +
			"  --repo R    one repository's caps\n\n" +
			"--max-reserve (the largest single lease) and --mode (observe, or enforce: caps\n" +
			"bite) are project-wide, so they go with --global. Amounts are US dollars from 0\n" +
			"to 100000, and a per-run cap may not exceed the daily cap of the same scope.\n" +
			"--clear removes the scope's caps.\n\n" +
			"Raising a cap (setting one that was unset counts), or lowering the mode to\n" +
			"observe, loosens what runs may spend, so it asks you to type the project's name\n" +
			"(or pass --yes). Lowering needs no confirmation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetSet(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.global, "global", false, "the project's own caps")
	f.BoolVar(&o.defaults, "defaults", false, "the default caps of repositories without their own")
	f.StringVar(&o.repo, "repo", "", "one repository's caps (owner/name)")
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
	n := 0
	for _, b := range []bool{o.global, o.defaults, o.repo != ""} {
		if b {
			n++
		}
	}
	if n != 1 {
		return nil, userErr("name one scope: --global, --defaults or --repo R")
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
		// As init does: enforce with no global caps would halt every run
		// (the rules read an absent cap as a refusal). Caps set by this same
		// invocation count.
		var g budget.GlobalCaps
		if _, err := db.Get(ctx, budget.PathCapsGlobal, &g); err != nil {
			return nil, remote(err)
		}
		if (p.daily == nil && g.DailyMicros == nil) || (p.perRun == nil && g.PerRunMicros == nil) {
			return nil, userErr("--mode enforce needs the database's global caps (%s: dailyMicros and perRunMicros), and some are missing: the rules read an absent cap as a refusal, so every run would halt. Set them first (or in this command) with fugaro budget set --global --daily <usd> --per-run <usd>", budget.PathCapsGlobal)
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
	slug := ""
	if p.scope == "repo" {
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
	use, short, long := "resume (--all | --repo R) [--reason TEXT]", "Clear a kill switch (budget admins)",
		"Clear the project's kill switch (--all) or one repository's (--repo R), recording who\n"+
			"and when. Asks you to type the project's name (or pass --yes): runs start again."
	if kill {
		use, short, long = "kill (--all | --repo R) [--reason TEXT]", "Halt every run of the project or of one repository (budget admins)",
			"Set the project's kill switch (--all) or one repository's (--repo R), recording\n"+
				"who, when and why. Runs in flight halt within seconds (a run in its last stage\n"+
				"finishes pushing its work first) and new runs refuse. --all asks you to type\n"+
				"the project's name (or pass --yes); one repository needs no confirmation."
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Long: long, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetKill(cmd, &o, kill) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.all, "all", false, "the whole project")
	f.StringVar(&o.repo, "repo", "", "one repository (owner/name)")
	f.StringVar(&o.reason, "reason", "", "why (recorded, shown by budget show and watch)")
	f.BoolVar(&o.yes, "yes", false, "confirm without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runBudgetKill(cmd *cobra.Command, o *budgetKillOptions, kill bool) error {
	ctx := cmd.Context()
	if o.all == (o.repo != "") {
		return userErr("name one scope: --all or --repo R")
	}
	if utf8.RuneCountInString(o.reason) > maxKillReason {
		return userErr("--reason is %d characters; keep it to %d", utf8.RuneCountInString(o.reason), maxKillReason)
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	me, err := lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	path, what := budget.PathKillGlobal, "project "+lc.Name
	if !o.all {
		slug, err := repoSlugOf(ctx, lc, o.repo)
		if err != nil {
			return err
		}
		path, what = budget.PathKillRepo(slug), "repository "+o.repo
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
			"marked local. It never reaches the network.",
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
}

type pricesDoc struct {
	Source    string     `json:"source"`
	CheckedAt string     `json:"checked_at"`
	AgeDays   int        `json:"age_days"`
	Stale     bool       `json:"stale"`
	Models    []priceRow `json:"models"`
}

func runBudgetPrices(cmd *cobra.Command, o *budgetPricesOptions) error {
	errw := cmd.ErrOrStderr()
	table := pricing.Embedded()
	local := map[string]bool{}
	// A project config, when one is selected, contributes its overrides. No
	// config is fine (prices needs no cloud); a config that is selected and
	// wrong is an error.
	if lc, err := pricesConfig(cmd, o.cloud); err != nil {
		return err
	} else if lc != nil {
		ov, err := lc.Overrides()
		if err != nil {
			return userErr("%v", err)
		}
		if table, err = table.With(ov); err != nil {
			return userErr("%v", err)
		}
		for k := range ov {
			if m, ok := table.Lookup(k); ok {
				local[m.ID] = true
			}
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
			CacheWrite1hPerM: r.InputPerM * r.CacheWrite1h, WebSearchPer1k: r.WebSearchPer1k, Local: local[id]}
		if r.LongContext != nil {
			row.LongContext = fmt.Sprintf("above %d input tokens: $%g in / $%g out", r.LongContext.AboveInputTokens, r.LongContext.InputPerM, r.LongContext.OutputPerM)
		}
		doc.Models = append(doc.Models, row)
	}
	if doc.Stale {
		fmt.Fprintf(errw, "warning: the built-in price table was last checked %s, older than 90 days (%d days ago); check %s and set model_prices in the project config where it differs\n",
			doc.CheckedAt, doc.AgeDays, doc.Source)
	}
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "model prices, US dollars per million tokens (source %s, checked %s)\n", doc.Source, doc.CheckedAt)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE 5M\tCACHE WRITE 1H\tWEB SEARCH/1K\tNOTES")
	for _, r := range doc.Models {
		var notes []string
		if r.Local {
			notes = append(notes, "local override")
		}
		if r.LongContext != "" {
			notes = append(notes, r.LongContext)
		}
		if len(r.Aliases) > 0 {
			notes = append(notes, "aliases "+strings.Join(r.Aliases, ", "))
		}
		fmt.Fprintf(tw, "%s\t$%g\t$%g\t$%g\t$%g\t$%g\t$%g\t%s\n", oneLine(r.ID), r.InputPerM, r.OutputPerM, r.CacheReadPerM,
			r.CacheWrite5mPerM, r.CacheWrite1hPerM, r.WebSearchPer1k, oneLine(strings.Join(notes, "; ")))
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
