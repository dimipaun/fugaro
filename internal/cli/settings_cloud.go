package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// stateUnpublished is the state the read exists for (SD3): the local value and
// the published one differ, so what the file says reaches runs on the next
// `fugaro update` and not before. The word is shared with `doctor`, `config
// layer` and the rigor side of the same comparison, and none of them says
// `drift` (spec §2.5).
const stateUnpublished = "unpublished"

// settingsCloudBudget bounds the read's cloud half. It is `accounts ls`'s own
// precedent (accountsStateBudget): the table is a report, and a report that
// hangs on a database that accepts the request and never answers is worth less
// than one that says which layer it could not read. The deadline travels into
// every request through the context, so internal/rtdb's own per-call timeout is
// a ceiling this only shortens.
const settingsCloudBudget = 5 * time.Second

// cloudLayer is one cloud layer's read: its state, what it cost, what it was, and
// the keys it sets.
type cloudLayer struct {
	// status is a STATE word — stateOK, or unreadState(reason) — and the empty
	// string for a layer this read never entered.
	status string
	ms     int64
	note   string
	// values are the keys the layer sets, rendered the way the read prints them.
	// A key absent from the map is one the layer does not set, which is not the
	// same answer as a key set to the empty string.
	values map[string]string
}

// readCloudLayers is the read's cloud half (spec §2.7): the published policy
// node and the Realtime Database's caps, read through the readers the commands
// that write them already use — `projectpolicy.Read` and the budget client
// (F6: `settings` adds no second reader for anything, and calls no Terraform
// state and no Secret Manager method, D850).
//
// Both layers keep the shape of every other answer here: a source that cannot
// be read is `unread: <reason>` on the rows it owns, never an error that takes
// the offline table down with it. The two exceptions are refuseHTTP2Debug, which
// is the repository's rule for every command that opens the cloud and aborts
// this one before it does, and the project that has no budget backend: nothing
// is dialled for the two database layers there, while the layer side — one
// bucket read, which needs no backend — is read as it is everywhere else (SD4).
func readCloudLayers(ctx context.Context, r *settingsRead, _ settingsOptions) error {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, settingsCloudBudget)
	defer cancel()
	// The three are read in parallel and under one deadline: they are
	// independent sources, and a database that stops answering one of them must
	// not eat the others' whole budget (spec §2.7). The third is the layer side
	// (settings_layer.go): the published object this machine's layer file is
	// compared with, which is the one read here a bucket answers.
	//
	// The layer side is the one of the three an installation with no budget
	// backend still has: its object is in that installation's own runs bucket,
	// and cloudRefusal's own comment calls the no-backend project the ordinary
	// shape of one that never turned the budget on. Skipping it there would print
	// the default with `ok` for a key this machine's file sets — a wrong answer
	// about every repository of the project rather than a missing one. A read
	// with no project config at all has no installation to read a layer from, so
	// that one the layer side does not run for either.
	reason := r.cloudRefusal()
	var wg sync.WaitGroup
	if r.local != nil {
		wg.Add(1)
		go func() { defer wg.Done(); r.readLayerSide(ctx) }()
	}
	if reason != "" {
		// The two database layers have nothing to dial: they are marked unread
		// with the one reason that answers for both.
		r.unreadCloud(reason)
	} else {
		wg.Add(2)
		go func() { defer wg.Done(); r.readPolicyLayer(ctx) }()
		go func() { defer wg.Done(); r.readCapsLayer(ctx) }()
	}
	wg.Wait()
	// The layer's own drift is merged after the wait rather than inside its
	// reader: r.drift is assigned whole by the policy layer's reader, and two
	// goroutines writing one map is a race whatever the keys.
	if len(r.layerSide.values) > 0 {
		if r.drift == nil {
			r.drift = map[string]bool{}
		}
		maps.Copy(r.drift, r.layerSide.values)
	}
	return nil
}

// cloudRefusal is why this installation has no cloud layer to read, or "". It is
// the answer `accounts ls` gives and for the reason it gives it: a project with
// no Firebase backend is the ordinary shape of an installation that never turned
// the budget on, and the sentence names the command that adds one.
func (r *settingsRead) cloudRefusal() string {
	if r.local == nil {
		return "no project config is selected, so no cloud layer could be read"
	}
	if r.local.Budget == nil || r.local.Budget.RTDBURL == "" {
		return fmt.Sprintf("project %s has no Firebase budget backend: budget.rtdb_url is not set in its project config, which fugaro update --firebase-project %s adds", r.local.Name, firebaseProjectIDWord(r.local.GCPProject))
	}
	return ""
}

// unreadCloud marks both cloud layers unread with the one reason that answers
// for both of them: no backend at all, or --offline, which is the flag's promise
// kept on the row rather than by leaving it out (spec §2.6).
func (r *settingsRead) unreadCloud(reason string) {
	r.policy = cloudLayer{status: unreadState(reason)}
	r.caps = cloudLayer{status: unreadState(reason)}
}

// readPolicyLayer reads the published policy node: the ceiling the project's
// runs are admitted by, and the local members it does not carry. A node the read
// refuses — a version skew, a permission, an unknown key — is that layer's
// unread reason and not this command's failure: the offline table is still the
// answer to every other question the read was asked.
func (r *settingsRead) readPolicyLayer(ctx context.Context) {
	t := settingsNow()
	p, found, err := projectPolicyRead(ctx, r.local)
	ms := readMS(settingsNow().Sub(t))
	if err != nil {
		r.policy = cloudLayer{status: unreadState(cloudReason(ctx, err)), ms: ms, note: projectpolicy.PathShared()}
		return
	}
	if !found {
		// Nothing is published: either the node is absent or the rules this
		// database runs under are older than the generation the policy model
		// needs (PolicyRulesVersion). Both read the same way here — there is no
		// published value to compare the file against, so every member the
		// publish stage writes is `unpublished`, which is what it is — and the
		// empty node is what says so rather than a second branch below.
		p = &projectpolicy.Policy{}
	}
	r.policy = cloudLayer{status: stateOK, ms: ms, note: projectpolicy.PathShared(), values: policyValues(p)}
	r.drift = policyDriftSet(r.local, p)
}

// policyValues is the members of the published node the registry has keys for,
// rendered the way the local config's own values render: the node's Mode,
// PerRunUSD, MaxRunTokens and AllowedModels are the same facts the local file's
// `budget:` block carries, so budget.per_run_usd reads `5` from either side and
// the ceiling comparison compares two spellings of one value.
//
// The credentials cascade (D152, D153) is the second family here, and it is the
// one family whose store is the node and nothing else: the table's `credentials.*`
// rows are `LayerPolicy`, and an installation where the node carries no cascade
// block at all keeps the protective defaults above — so a member the node does
// not carry is left out of this map for the same reason the budget members are,
// and `credentials.fallback` reads `auto` exactly as a run of that project would
// get it. The daemon's switch and the project's issue adapter are the third
// family of that kind (R1 of docs/orchestrator-graph-estimates): the node is
// their only store too, so they read out of the same block and by the same rule.
//
// A member the node does not carry is left out of the map rather than rendered
// as the zero value the decode gives it. The node's budget members are not
// pointers, so an absent node — nothing published, or a node a hand edit left
// without a budget block — decodes as mode "", 0 dollars, 0 tokens and no list;
// and those are exactly the values policy.Merge reads as "this layer writes
// nothing" (`l.PerRunUSD > 0`, `l.MaxRunTokens > 0`, `l.Mode != ""`,
// `AllowedModels != nil`). A map that carried them would print `VALUE 0` with
// `WINS policy` for a cap nobody set, put that row under --changed, and give
// --why a `policy 0` level the node does not carry — while the ceiling a run
// gets is the file's or the default's, and `unpublished` is what says the node
// does not carry what the file says.
func policyValues(p *projectpolicy.Policy) map[string]string {
	out := map[string]string{}
	// The mode is read through C1 (AB3): a node that still says the old word
	// acts as observe, and this row shows the mode a run of the project would
	// get rather than a word that is not a mode any more. The node itself is
	// named where it is refused, by doctor and at the run (R4), so this view
	// does not have to carry the word to be honest about it.
	if m := p.Budget.Mode; m != "" {
		out["budget.mode"] = policy.Mode(m).Effective()
	}
	if p.Budget.PerRunUSD > 0 {
		out["budget.per_run_usd"] = strconv.FormatFloat(p.Budget.PerRunUSD, 'f', -1, 64)
	}
	if p.Budget.MaxRunTokens > 0 {
		out["budget.max_run_tokens"] = strconv.FormatInt(p.Budget.MaxRunTokens, 10)
	}
	if p.Budget.AllowedModels != nil {
		out["budget.allowed_models"] = strings.Join(p.Budget.AllowedModels, ", ")
	}
	if c := p.Credentials; c != nil {
		// Every pointer here is `nil` for a member the node does not carry, and
		// that is the difference this map keeps: `false` is the written opt-out
		// of stop_after_exhaustion_per_window and not the absent default, which
		// is why the struct spells it as a pointer in the first place.
		if c.Fallback != "" {
			out["credentials.fallback"] = c.Fallback
		}
		if c.WaitThreshold != "" {
			out["credentials.wait_threshold"] = c.WaitThreshold
		}
		if c.Backoff != nil {
			if c.Backoff.Initial != "" {
				out["credentials.backoff.initial"] = c.Backoff.Initial
			}
			if c.Backoff.Cap != "" {
				out["credentials.backoff.cap"] = c.Backoff.Cap
			}
		}
		if c.StopAfterExhaustionPerWindow != nil {
			out["credentials.stop_after_exhaustion_per_window"] = strconv.FormatBool(*c.StopAfterExhaustionPerWindow)
		}
		if c.ConcurrencyPerToken != nil {
			out["credentials.concurrency_per_token"] = strconv.Itoa(*c.ConcurrencyPerToken)
		}
	}
	// The daemon's own two (R1 of docs/orchestrator-graph-estimates, D2701,
	// D2733, D2739) follow the same rule, and for the switch it is the whole
	// point of the key: `*bool` is nil for a member the node does not carry and
	// false for the written opt-out, so a map that rendered nil as `false` would
	// print every project that never mentioned the switch as a project whose
	// daemon is stopped. The adapter renders as the node's own JSON, which is
	// what this read does with a block no file spells.
	if p.OrchestratorEnabled != nil {
		out["orchestrator_enabled"] = strconv.FormatBool(*p.OrchestratorEnabled)
	}
	if t := jsonText(p.IssueSync); t != "" {
		out["issue_sync"] = t
	}
	return out
}

// policyDriftSet is the registry keys the published node does not carry what the
// local config says, keyed for the row that prints `unpublished`. It is
// `policyConfigDrift`'s own answer, called rather than restated: that function is
// what `fugaro update`'s policy stage writes from, so a key the stage would
// publish and the read does not call unpublished is a difference a consumer
// would never see (G1, G2).
//
// The identities the typed Bitbucket tokens carry (R12) are read out of the
// creds state store by the doors that publish them and not by this one: this
// read is a report, it dials no second store, and the member the comparison
// would add is one the registry has no row for — its Bitbucket token rows are
// the per-field wildcards (`bitbucket_tokens.*.coverage`), which a drift map
// keyed by the member's own name is never consulted for. So no row of the table
// is marked by that member either way, and a nil set here changes nothing the
// operator reads.
func policyDriftSet(lc *localcfg.Config, p *projectpolicy.Policy) map[string]bool {
	out := map[string]bool{}
	for _, d := range policyConfigDrift(lc, p, nil) {
		out[d.Member] = true
	}
	return out
}

// readCapsLayer reads the Realtime Database's own keys (spec §2.2): the mode
// every lease compares against, the two cap scopes, the reserve limit and the
// global kill switch.
func (r *settingsRead) readCapsLayer(ctx context.Context) {
	t := settingsNow()
	values, err := readCapsValues(ctx, r.local)
	ms := readMS(settingsNow().Sub(t))
	layer := cloudLayer{ms: ms, note: r.local.Budget.RTDBURL}
	if err != nil {
		layer.status = unreadState(cloudReason(ctx, err))
	} else {
		layer.status, layer.values = stateOK, values
	}
	r.caps = layer
}

// readCapsValues reads the nodes under /config a run is enforced by, each one
// through the type `internal/budget` declares for it. checkDBProject is asked
// first, as every other reader of this database asks it: a budget.rtdb_url
// pointing at another project's database would otherwise have this read print
// that project's caps as this project's, which is a wrong answer rather than a
// missing one.
func readCapsValues(ctx context.Context, lc *localcfg.Config) (map[string]string, error) {
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, err
	}
	// Every failure is worded the way the budget commands word it (dbErr): a
	// rule that denies the read is the reader's own permission — and the
	// sentence names the role and the login that fix it — while an outage is a
	// remote error. The row prints that sentence as its unread reason.
	read := func(path string, out any) error {
		if _, err := db.Get(ctx, path, out); err != nil {
			return dbErr(lc, err, false)
		}
		return nil
	}
	var mode string
	if err := read(budget.PathMode, &mode); err != nil {
		return nil, err
	}
	var global budget.GlobalCaps
	if err := read(budget.PathCapsGlobal, &global); err != nil {
		return nil, err
	}
	var defaults budget.DefaultCaps
	if err := read(budget.PathCapsDefaults, &defaults); err != nil {
		return nil, err
	}
	var limits budget.Limits
	if err := read(budget.PathLimits, &limits); err != nil {
		return nil, err
	}
	kills, err := budget.ReadGlobalKill(ctx, db)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	// An absent node and an absent member are both "this layer sets nothing":
	// the map carries a key only where the database holds the value, so the
	// row prints its default rather than a cap nobody set.
	out := map[string]string{}
	if mode != "" {
		out["caps.mode"] = mode
	}
	if global.PerRunMicros != nil {
		out["caps.global.per_run_usd"] = money(*global.PerRunMicros)
	}
	if global.DailyMicros != nil {
		out["caps.global.per_day_usd"] = money(*global.DailyMicros)
	}
	if defaults.RepoPerRunMicros != nil {
		out["caps.defaults.per_run_usd"] = money(*defaults.RepoPerRunMicros)
	}
	if defaults.RepoDailyMicros != nil {
		out["caps.defaults.per_day_usd"] = money(*defaults.RepoDailyMicros)
	}
	if limits.MaxReserveMicros != nil {
		out["caps.max_reserve_usd"] = money(*limits.MaxReserveMicros)
	}
	if kills.Global != nil {
		out["kill.global.on"] = strconv.FormatBool(kills.Global.On)
	}
	return out, nil
}

// cloudReason words a cloud layer's failure for the STATE column. A deadline
// that passed is the bound this read sets and not the error underneath it —
// internal/rtdb would say the request timed out, which leaves the reader asking
// how long the command waited — and everything else is the reader's own
// sentence, flattened into the one line the column holds.
func cloudReason(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return fmt.Sprintf("no answer within %s", settingsCloudBudget)
	}
	return oneLine(err.Error())
}

// settingsCapsKey reports whether a row is one of the Realtime Database's own
// keys (spec §2.1, §2.2): the caps and the kill switches, whose only store is
// the database. `budget.*` is not one of them — that family is the ceiling, which
// the local config, the published node and fugaro.yaml all carry — and the two
// modes are named apart for exactly that reason (SD6).
func settingsCapsKey(key string) bool {
	return strings.HasPrefix(key, "caps.") || strings.HasPrefix(key, "kill.")
}

// cloudProjectRow is the cloud half of a project row: the caps rows, the rows
// only the published node stores, the ceiling keys a level above the file
// clamps, and the `unpublished` state the comparison sets. The four are one
// function because they answer one question — what the cloud says about this key
// — and a row reaching none of them is a row that prints a value no run gets.
func (r *settingsRead) cloudProjectRow(s setting, row settings.Row) setting {
	switch {
	case settingsCapsKey(row.Key):
		s = r.capsRow(s, row)
	case settingsPolicyOnlyRow(row):
		s = r.policyRow(s, row)
	case row.Merge == settings.Tighten:
		s = r.nodeCeiling(s, row)
	}
	if r.drift[row.Key] {
		// The local file and the published node differ, so the file's value
		// reaches runs on the next update and not before (SD3). It is claimed at
		// the project scope, where those two documents meet: a repository's own
		// file is published by its own path and not by this node, so this
		// comparison is not evidence about it.
		s.State = stateUnpublished
	}
	return s
}

// capsRow is a row whose only store is the database. The three answers are the
// layer's own: it was read and holds the value, it was read and holds none, or
// it was not read at all. The third is the STATE and nothing else: a row whose
// layer was never read keeps what a key no layer decided prints — the default,
// with WINS default (spec §2.3), the shape the repository scope's `unknown`
// already has — and `unread: <reason>` is the one column that says the read
// which would have answered it did not happen (D850). Putting the word in the
// VALUE column instead would be a non-value where a consumer reads a value, and
// claiming the caps layer for it would list these rows under --changed on every
// installation whose cloud is out of reach: seven changes nobody made.
func (r *settingsRead) capsRow(s setting, row settings.Row) setting {
	if r.caps.status != stateOK {
		s.State = r.caps.status
		return s
	}
	v, ok := r.caps.values[row.Key]
	if !ok {
		// The layer was read and the node holds nothing: the row keeps the
		// default it already carries, WINS stays `default`, and nothing claims
		// a level that set no value.
		s.State = stateOK
		return s
	}
	s.Value, s.Wins, s.State = v, settings.LayerCaps, stateOK
	s.Levels = []level{{Layer: settings.LayerCaps, Value: v, Outcome: "wins"}}
	return s
}

// settingsPolicyOnlyRow reports whether the published node is a row's only
// store: the row's one layer is `policy`, so no file, no environment and no
// default is a value a run gets. It is the `credentials.*` family (R1), the
// cascade the credentials service runs on — the local file's own cascade is a
// different family (`credential_cascade.*`, N1) because it is a different
// document, and a row that let either of them stand for the other would print
// the operator's file as the value a run gets when the node is what decides.
func settingsPolicyOnlyRow(row settings.Row) bool {
	return len(row.Layers) == 1 && row.Layers[0] == settings.LayerPolicy
}

// policyRow is a row whose only store is the published node. It reads the way
// capsRow reads a row whose only store is the database, and for the same
// reason: the layer was read and holds the value, it was read and holds none,
// or it was not read at all. The third is the STATE and nothing else — the row
// keeps the default with WINS default, which is the answer for a key no layer
// decided — and the second is the protective default the registry states, which
// is what a run of that project gets when the node is silent about the key.
// Reading the node as the row's own layer rather than as a clamp is the whole
// difference from nodeCeiling: there is no value under it to clamp.
func (r *settingsRead) policyRow(s setting, row settings.Row) setting {
	if r.policy.status != stateOK {
		s.State = r.policy.status
		return s
	}
	v, ok := r.policy.values[row.Key]
	if !ok {
		s.State = stateOK
		return s
	}
	s.Value, s.Wins, s.State = v, settings.LayerPolicy, stateOK
	s.Levels = []level{{Layer: settings.LayerPolicy, Value: v, Outcome: "wins"}}
	return s
}

// nodeCeiling applies the ceiling rule to a row the offline half already decided
// (spec §2.2, D849): the published node and the file hold caps for the same key,
// and the tighter of the two is the value a run gets. This is where `settings`
// differs from `config show`, which reports a repository's 50 as source `repo`
// while the run is held to the node's 10 (F17): the row reads 10 with WINS
// policy, and --why carries the 50 and the clamp.
//
// A level that sets the key and loses is printed rather than hidden, and the
// loser's outcome names the winner, so a reader can see which of the two to move.
func (r *settingsRead) nodeCeiling(s setting, row settings.Row) setting {
	if r.policy.status != stateOK {
		return s
	}
	node, ok := r.policy.values[row.Key]
	if !ok || node == "" {
		return s
	}
	if s.Wins == settings.LayerDefault {
		// No layer under the node sets the key: the node is the only level, and
		// it decides.
		s.Value, s.Wins = node, settings.LayerPolicy
		s.Levels = []level{{Layer: settings.LayerPolicy, Value: node, Outcome: "wins"}}
		return s
	}
	if node == s.Value {
		// The node carries exactly what the file says. The node is the level a
		// run is admitted by, and a second level holding the same value adds
		// nothing to --why; the row's `ok` and its missing `unpublished` are
		// what say the two agree. This is the state `fugaro update` converges
		// an edit to (S11).
		s.Wins = settings.LayerPolicy
		s.Levels = []level{{Layer: settings.LayerPolicy, Value: node, Outcome: "wins"}}
		return s
	}
	if row.Key == settingsAllowedModelsKey {
		return r.nodeAllowedModels(s, node)
	}
	if settingsLoosens(row.Key, s.Value, node) {
		s.Levels = append(s.Levels, level{Layer: settings.LayerPolicy, Value: node, Outcome: "ignored (looser than " + s.Wins + ")"})
		return s
	}
	// The node's cap is the tighter one: it is the value the run gets, and the
	// file's is the level that loses to it.
	loser := level{Layer: s.Wins, Value: s.Value, Outcome: "ignored (looser than " + settings.LayerPolicy + ")"}
	s.Value, s.Wins = node, settings.LayerPolicy
	s.Levels = []level{{Layer: settings.LayerPolicy, Value: node, Outcome: "wins"}, loser}
	return s
}

// settingsAllowedModelsKey is the one ceiling key whose layers combine by set
// intersection rather than by a comparison of two values (spec §2.2). It is
// named here because the rule below is the only place in this read that has to
// know it: settingsLoosens asks the same key the same question for the write
// path, so a second key of this kind would be a second rule there too.
const settingsAllowedModelsKey = "budget.allowed_models"

// nodeAllowedModels is the ceiling rule for budget.allowed_models: a run may
// call the models both lists carry, so the effective value is the intersection
// and not the file's list — a list neither of them contains is neither of them
// either, which is what the comparison every other key uses cannot say (it can
// only pick one side or the other).
//
// The intersection is policy.Merge's own (intersectAllowList, the rule the
// spec states once and the runner applies), called rather than restated: a
// second implementation here would be a second answer to "which models may
// this run call", and the row exists to answer that question.
//
// --why keeps both real lists: the ceiling is marked as the level a run is
// admitted by and shows what the node carries, and the file's list is shown as
// ignored because it names models outside the admitted set. The one row whose
// winner's value is not the row's own is the mixed case below — the effective
// list is then no layer's list, and the row prints what a run gets.
func (r *settingsRead) nodeAllowedModels(s setting, node string) setting {
	file := level{Layer: s.Wins, Value: s.Value}
	eff := strings.Join(policy.Merge(
		policy.Layer{AllowedModels: settingsListItems(node)},
		policy.Layer{AllowedModels: settingsListItems(s.Value)},
	).AllowedModels, ", ")
	switch eff {
	case node:
		// The file's list is not narrower than the node's: the node's is what
		// a run may use, and the file's loses to it.
		s.Value, s.Wins = node, settings.LayerPolicy
		file.Outcome = "ignored (looser than " + settings.LayerPolicy + ")"
		s.Levels = []level{{Layer: settings.LayerPolicy, Value: node, Outcome: "wins"}, file}
	case s.Value:
		// The node's list is the wider one: the file's is the effective list,
		// and the ceiling is the level that loses to it.
		s.Levels = append(s.Levels, level{Layer: settings.LayerPolicy, Value: node, Outcome: "ignored (looser than " + s.Wins + ")"})
	default:
		// Neither list contains the other: what a run gets is the intersection,
		// which is no layer's own list, and both are printed as the levels that
		// were read.
		s.Value, s.Wins = eff, settings.LayerPolicy
		file.Outcome = "ignored (looser than " + settings.LayerPolicy + ")"
		s.Levels = []level{{Layer: settings.LayerPolicy, Value: node, Outcome: "wins"}, file}
	}
	return s
}

// settingsListItems reads a rendered list back into its entries. The read
// prints a list joined with ", ", and every comparison of two of them splits on
// the comma (settingsListSubset, the write path's own rule), so no entry carries
// one. An empty rendering is no list at all — policy.Layer's AllowedModels nil,
// which admits every model — and not an empty list, which would admit none.
func settingsListItems(rendered string) []string {
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	items := strings.Split(rendered, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}
