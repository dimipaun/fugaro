package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
)

// budget_stops.go is R8 of docs/always-a-budget/reconciliation.md: the one
// helper that answers what this release stops, so `fugaro update` can print it
// before it changes anything and `fugaro doctor` can report the same lines
// (AB3) without a second table of reasons beside this one.
//
// Five facts, and every one of them is read from a document rather than
// decided here: the checkout's own fugaro.yaml for the three repository lines
// (a pin the price table cannot charge, D3020; the word that is not a mode any
// more, D3022; the model key this apply moves to the credentials service, F26),
// and the installation for the two lines about it (the database's mode and caps
// where this machine's local off is being removed, D3024; the Cloud Batch
// backend, D3021). None of the five is a refusal this file invents: each is a
// rule another task states, named here first because the update is the one
// command the upgrade is (CLAUDE.md's HARD RULE) and a person is owed what will
// stop before it does.
//
// The reading is deliberately before validation (F22): the checkout's file and
// the local config are both read for the word the release before this one
// accepted, through localcfg.RemovedBudgetMode, and the Agent is taken with
// config.AgentOf, which decodes the repository's own block strictly and judges
// nothing — so a file that says off still says what it pins and which auth it
// runs under, and the listing is answered rather than refused.

// budgetStop is one line of what will stop, in R8's own order.
type budgetStop struct {
	line string
}

// budgetStopFacts is everything R8's lines are answered from, read by
// budgetStopFacts and rendered by budgetStops: one reading, two readers.
type budgetStopFacts struct {
	// The checkout's repository, when this run is in one: its fugaro.yaml, the
	// word that file says, the pins the price table cannot charge, and the
	// credential this apply moves out of its job ("" when none does).
	repoPath string
	repoOff  bool
	pins     []config.Problem
	modelKey string

	// The installation: the word this machine's local config says, and — read
	// only where it says it — the database's mode and caps, or why they could
	// not be read.
	localOff bool
	dbMode   string
	dbCaps   string
	dbWhy    string
	batch    bool
}

// budgetStops is R8's lines, in the order R8 states them: the repository's
// three and then the installation's two. An empty result is a release that
// stops nothing here, which is the ordinary case and prints nothing.
func budgetStops(f budgetStopFacts) []budgetStop {
	var stops []budgetStop
	for _, p := range f.pins {
		stops = append(stops, budgetStop{line: fmt.Sprintf("this repository's runs are refused: %s %s", p.Path, p.Message)})
	}
	if f.repoOff {
		stops = append(stops, budgetStop{line: fmt.Sprintf("%s says budget.mode: off, which is not a mode any more: this update offers the removal of the line, and the repository's runs are refused until it is gone", f.repoPath)})
	}
	if f.modelKey != "" {
		stops = append(stops, budgetStop{line: fmt.Sprintf("this repository's model key (%s) leaves its job at this apply: the credentials service serves the repository now", f.modelKey)})
	}
	if f.localOff {
		where := fmt.Sprintf("the database's mode %s and its caps (%s)", f.dbMode, f.dbCaps)
		if f.dbWhy != "" {
			where = fmt.Sprintf("the database's mode and caps, which could not be read (%s)", f.dbWhy)
		}
		stops = append(stops, budgetStop{line: fmt.Sprintf("this machine's local config says budget.mode: off, which is removed: the update rewrites it to observe, and this machine's runs come under %s", where)})
	}
	if f.batch {
		stops = append(stops, budgetStop{line: fmt.Sprintf("this installation launches runs on %s, and this release refuses every launch there until a run's token has a carrier on that backend", backend.CloudBatch)})
	}
	return stops
}

// budgetStopFacts reads the facts above, in the order they are cheap: the two
// local documents, then the price table, then the database — and only where
// this machine's local config says the word, because that is the one line whose
// subject is the database.
//
// Every read that does not answer leaves its fact out rather than failing the
// run: this is a listing printed before the update's first stage, and an update
// may not be stopped by a note (CLAUDE.md's HARD RULE). The two documents that
// are not there are the ordinary cases — a run outside a checkout, an
// installation whose config this run is creating — and print nothing.
func (e *initEngine) budgetStopFacts(ctx context.Context) budgetStopFacts {
	var f budgetStopFacts
	if e.lc != nil {
		if _, off := localcfg.RemovedBudgetMode(e.old); off {
			f.localOff = true
			e.readBudgetDatabase(ctx, &f)
		}
		f.batch = e.lc.Backend() == backend.CloudBatch
	}
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return f
	}
	f.repoPath = filepath.Join(root, "fugaro.yaml")
	data, err := readFugaroYAML(f.repoPath)
	if err != nil {
		return f
	}
	_, f.repoOff = localcfg.RemovedBudgetMode(data)
	// The repository's own block, decoded strictly and judged by nothing: a
	// file that says the removed word still answers what it pins and which auth
	// its runs use (config.AgentOf).
	agent, ok := config.AgentOf(data)
	if !ok {
		return f
	}
	if prices, _, err := priceTable(e.lc, budgetNow()); err == nil && prices != nil {
		f.pins = config.CheckPins(agent, prices)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return f
	}
	f.modelKey = movedModelKey(e.lc, oi.Repo, agent.Auth, e.writtenBudgetMode())
	return f
}

// readBudgetDatabase answers the installation's line where this machine's local
// config says the removed word: the mode and caps this machine's runs land
// under once it is gone (D3024 — the database's mode stands, and the caps beside
// it are what a run is now compared against).
func (e *initEngine) readBudgetDatabase(ctx context.Context, f *budgetStopFacts) {
	_, db, err := budgetDBFor(ctx, e.lc)
	if err != nil {
		f.dbWhy = oneLineCLI(err.Error())
		return
	}
	var mode string
	found, err := db.Get(ctx, budget.PathMode, &mode)
	if err != nil {
		f.dbWhy = oneLineCLI(err.Error())
		return
	}
	// An absent mode is observe, exactly as the raise and the session read it
	// (D1548).
	if !found {
		mode = budget.ModeObserve
	}
	var caps budget.GlobalCaps
	if _, err := db.Get(ctx, budget.PathCapsGlobal, &caps); err != nil {
		f.dbWhy = oneLineCLI(err.Error())
		return
	}
	var defaults budget.DefaultCaps
	if _, err := db.Get(ctx, budget.PathCapsDefaults, &defaults); err != nil {
		f.dbWhy = oneLineCLI(err.Error())
		return
	}
	f.dbMode, f.dbCaps = mode, capsText(caps, defaults)
}

// capsText is the database's caps as the line names them: the project-wide
// daily and per-run caps and the daily cap a repository without one of its own
// is compared against, each "unset" where the database holds none — which is
// the state enforce would halt a run in (update_budget_mode.go's own reading of
// the same three).
func capsText(caps budget.GlobalCaps, defaults budget.DefaultCaps) string {
	usd := func(m *budget.Micros) string {
		if m == nil {
			return "unset"
		}
		return fmt.Sprintf("$%.2f", m.USD())
	}
	return fmt.Sprintf("daily %s, per run %s, repository daily %s",
		usd(caps.DailyMicros), usd(caps.PerRunMicros), usd(defaults.RepoDailyMicros))
}

// writtenBudgetMode is this machine's budget mode as its file writes it, and
// not as the loader reads it: the loader folds the removed word to observe
// before the decode (localcfg's foldRemovedBudgetMode), so the one reader that
// has to tell "off" from "observe" reads the bytes the run found.
func (e *initEngine) writtenBudgetMode() string {
	if w, off := localcfg.RemovedBudgetMode(e.old); off {
		return w
	}
	if e.lc == nil || e.lc.Budget == nil {
		return ""
	}
	return e.lc.Budget.Mode
}

// movedModelKey is F26, asked through the switch's own predicate: the model
// credential this apply moves out of the repository's job, "" when none does.
//
// The switch is judged twice, on the mode the file writes and on what that mode
// means from now on, and the key moves exactly where the two disagree — an
// api-key or oauth repository that was in the word that is not a mode any more
// (or had none written, which was off too) is one the credentials service
// starts serving where its bundle is ready, and the next apply drops the key
// from its job. infra.CredentialsServiceServes is the one predicate both
// answers come from, so this line cannot claim a move the apply does not make.
func movedModelKey(lc *localcfg.Config, repo, auth, written string) string {
	if lc == nil || repo == "" {
		return ""
	}
	installed := lc.CredentialsService != nil && lc.CredentialsService.URL != ""
	local, _ := lc.RepoFor(repo)
	ready := local.CredentialsServiceOn()
	if infra.CredentialsServiceServes(installed, ready, auth, written) {
		return ""
	}
	if !infra.CredentialsServiceServes(installed, ready, auth, policy.Mode(written).Effective()) {
		return ""
	}
	switch auth {
	case config.AuthAPIKey:
		return config.ReservedSecrets[config.AnthropicSecret]
	case config.AuthOAuth:
		return config.ReservedSecrets[config.ClaudeOAuthToken]
	}
	return ""
}

// announceBudgetStops prints R8's lines, and it is the update door's alone:
// `fugaro setup` creates an installation rather than moving one, and the lines
// are about what this release stops on an installation that exists.
//
// It is printed before the runs-in-flight note and before the run's own
// confirmation, which is what "before it changes anything" means: the first
// stage writes after both. It says nothing with --json or --print-vars, the two
// modes whose stdout is a document rather than a transcript, and it is printed
// under --plan-only — a run whose whole purpose is to say what would happen is
// the one reader that wants the listing most, and this run changes nothing for
// it to be early about.
//
// It refuses nothing and asks nothing, and every read behind it is one that
// could not answer rather than an error: a listing may not stop the one command
// the upgrade is.
func (e *initEngine) announceBudgetStops(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.asJSON || o.printVars {
		return
	}
	stops := budgetStops(e.budgetStopFacts(ctx))
	if len(stops) == 0 {
		return
	}
	w := loggedOut(r.w, r.repLog)
	fmt.Fprintln(w, "This release is always a budget. Before this update changes anything, what will stop:")
	for _, s := range stops {
		fmt.Fprintf(w, "  %s\n", s.line)
	}
}
