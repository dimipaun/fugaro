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
		f.pins = config.UnpricedPins(agent, prices)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return f
	}
	f.modelKey = e.budgetStopModelKey(ctx, oi.Repo, agent.Auth, data)
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

// nodeBudgetMode is the budget mode the published node's own bytes write, ""
// where no node was read: a project that has published none, a database this
// machine cannot reach, or a node this reader refuses.
//
// It is the NODE's mode and not this machine's local config's, because the
// node's is the mode the apply reads — internal/infra builds a repository
// root's credentials switch out of Inputs.Policy's budget mode — while the
// local config's own mode is a value the same update is in the middle of
// rewriting.
//
// The read is fresh and not the run's memoized one (policyReadOnce): that memo
// answers the gates and the launch, whose answers must not change, and this
// reading is taken before the policy stage — a memo primed here would hand
// every later reader of the run the node from before the stage that rewrites
// it.
func (e *initEngine) nodeBudgetMode(ctx context.Context) string {
	if e.lc == nil {
		return ""
	}
	p, found, err := projectPolicyRead(ctx, e.lc)
	if err != nil || !found {
		return ""
	}
	return policyBudgetMode(p)
}

// budgetModeAfterMigration is the mode the policy stage of this same run leaves
// the node with: the word that is not a mode any more is rewritten to observe
// in place (projectpolicy's migrateRemovedMode, AB8a, D3010), and every other
// mode — and a node whose budget block names none — is left exactly where it
// is.
func budgetModeAfterMigration(mode string) string {
	if policy.Mode(mode).Removed() {
		return policy.ModeObserve
	}
	return mode
}

// credentialsServiceInstalled is the switch's first condition as this machine's
// local config carries it: the block the update that deployed the service wrote
// there, which is also the reading openBundleStore makes of a config before it
// reads a bundle at all.
//
// It is not the installation's own outputs, which is what the apply and the
// record read: those are a terraform state read, and this listing is printed
// before the update's first stage and may not spend one.
func (e *initEngine) credentialsServiceInstalled() bool {
	return e.lc != nil && e.lc.CredentialsService != nil && e.lc.CredentialsService.URL != ""
}

// bundleServes is the bundle's own answer for this repository: whether the
// credentials service holds what one of its runs would ask it for — the model
// credential its auth calls for, and the GitHub App key its git provider needs
// to mint with (credentialsBundleServes).
//
// It is the readiness half of the switch, read from the bundle and never from
// the local config's record: the record is the switch's whole past answer with
// the mode of the day folded into it (init_credentials.go's RecordSwitch), so
// every repository the switch held off carries a false record however ready its
// bundle is.
func (e *initEngine) bundleServes(ctx context.Context, auth, repo string, data []byte) bool {
	provider, _ := config.ProviderOf(data)
	if provider == "" {
		// A checkout whose file names no provider takes the one this machine's
		// config holds for it, which is how the repository stage resolves it.
		provider = e.lc.Repos[repoConfigKey(e.lc, repo)].Provider
	}
	var accounts []string
	if auth == config.AuthOAuth {
		// The accounts this repository's runs may go out on, exactly as
		// infra.WorkflowAccounts answers them for the oauth arm: the bundle has
		// to hold every one of them (D143). The other auths' readiness is read
		// from the bundle's own entries and names no account.
		accounts = config.ClaudeAccountTokenNames(e.lc.Credentials, config.AllowedClaudeAccounts(e.lc.ClaudeAccounts, repo))
	}
	ready, _ := e.r.credentialsBundleServes(ctx, e.lc, auth, accounts, provider)
	return ready
}

// budgetStopModelKey is F26 for this run's repository: the model credential the
// apply that follows moves out of its job, "" when none moves.
//
// The two modes are the published node's, as its bytes write it now and as the
// policy stage of this same run will leave it, because the node's mode is the
// mode the apply reads. The key moves exactly where those two disagree: an
// api-key or oauth repository on a node that says the word is one the switch
// holds off, and the same node rewritten to observe is one the switch serves
// where the bundle is ready — which is the apply that drops the key from its
// job.
func (e *initEngine) budgetStopModelKey(ctx context.Context, repo, auth string, data []byte) string {
	before := e.nodeBudgetMode(ctx)
	after := budgetModeAfterMigration(before)
	installed := e.credentialsServiceInstalled()
	// The mode half is asked first: the bundle is read only where the two modes
	// disagree, which is the only state this line can be printed in. A read
	// spent on every other repository would be a cloud call, and a warning
	// (readCredentialsBundle), for a line nobody prints.
	if movedModelKey(installed, true, auth, before, after) == "" {
		return ""
	}
	return movedModelKey(installed, e.bundleServes(ctx, auth, repo, data), auth, before, after)
}

// movedModelKey is F26's predicate: the model credential that leaves the
// repository's job, "" when none does, asked through the switch's own predicate
// twice — once on the mode the node writes now and once on the mode the policy
// stage leaves it with. infra.CredentialsServiceServes is the one predicate
// both answers come from, so this line cannot claim a move the apply does not
// make.
//
// ready is the bundle's own reading (bundleServes) and never the record in the
// local config: a record that is true means an earlier apply already dropped
// the key, which is not a move this apply makes.
func movedModelKey(installed, ready bool, auth, before, after string) string {
	if infra.CredentialsServiceServes(installed, ready, auth, before) {
		return ""
	}
	if !infra.CredentialsServiceServes(installed, ready, auth, after) {
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
