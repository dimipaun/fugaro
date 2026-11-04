package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The run's ordered failover over the owner's Claude accounts (docs/accounts-add
// spec §11.3, D874; plan task A7). It is the only code in the design that
// chooses a credential for the owner, and it ships dormant: the switch
// (claude_account_auto, §11.1) is off unless the owner's own act turns it on,
// off is explicit-only, and explicit-only is what §11.2's chain already gives
// and what this release ships (D875, §11.4).
//
// Three properties are the whole of the design, and each is a way this code
// could go wrong:
//
//   - **The walk is the list the owner reads.** Its order is the order
//     `fugaro accounts ls` prints — the unsuffixed default first, then the
//     admitted names sorted — and it is computed from the published node
//     (accounts.<name>.allow_repos, projectpolicy.DeriveAccounts) rather than
//     from a second list kept in step by hand. The floor every rung of §11.2
//     stands on is the same rule asked here: an account the repository is not
//     admitted to is never a candidate, whatever the node says elsewhere.
//   - **The walk never leaves the stage's own scope.** A stage that shares
//     the run's job has no scope of its own, and the coding run's own in-job
//     review stage is one of those; a stage that runs in a job of its own is
//     bounded by the accounts that job exists for. The one such stage the
//     design names is the independent review job's (r.reviewJob), whose walk
//     is limited to the review-class accounts (§11.3, D1062): an account
//     admitted for the repository but of no profile's own axis is never
//     chosen for it.
//   - **The walk never moves a stage that is already running.** A stage's
//     transcript, its cost and its verdict belong to one account, so the
//     only thing that advances the walk is the credential service's typed
//     answer ending an attempt (C1.17, noteStageExhausted): the attempt that
//     follows is the next account's, and nothing inside an attempt can move
//     it.
//
// What the walk cannot do is serve a stage the account it chose, and that is
// why a choice which is not the account this run already holds is a refusal
// and never a silent fallback: a Cloud Run job mounts one account's token
// before the run starts (LC4), the run's own credential is that account, and a
// stage that ran as another one would leave a record that says which account
// served the stage while another account's token answered it. Delivering a
// chosen account is the credentials layer's (the credentials service holds the
// credentials a job does not, and a review job is deployed per account), and
// no file this task owns reaches it: until that path exists, a failover past
// the run's own account ends the run named rather than switching to an account
// this container does not hold.

// accountChoice is one run's walk: the switch as this run read it, and what
// each stage has already been through. It is written on the stage's own
// goroutine (stageOnce's hook and noteStageExhausted), the way stageN is.
type accountChoice struct {
	// read is whether the switch has been read. It is read at most once per
	// run: the node cannot change under it, and the answer decides a
	// credential.
	read bool
	// auto is claude_account_auto: whether Fugaro may choose an account for
	// the owner at all (D874, D875).
	auto bool
	// failovers counts, per stage, how many of that stage's attempts the
	// credential service's exhausted answer has ended: it is the index the
	// walk stands on, so the attempt that follows starts on the next account.
	failovers map[string]int
}

// accountAuto reports whether this run may choose an account for the owner.
//
// The switch is read from the owner's published policy node and not from the
// run's own environment: the value decides which credential serves a stage,
// and every member of that node is there because the owner writes it and the
// run fails closed on it, while an environment value of a shared job is
// launcher-writable — the reason CloneURLs and bitbucket_api_roots are node
// members too. Its spelling is the local config's own key (§11.1, A1), so no
// second name for it is invented here.
//
// The member is read off the node's document because projectpolicy's decoder
// does not carry it yet. No publisher writes one today, so the switch is off
// in every installation and the walk is dormant there, which is what D875
// ships; a later task adds the publisher half. Until it does, this is the one
// reader of a top-level key that reader calls unknown — a node carrying it is
// still readable (P8 tolerates unknown top-level keys) and the run's own
// policy read warns about it, which is reported with this task rather than
// worked around here.
//
// An absent member, a member that is not a JSON true, and a node this run
// cannot read are all off: the switch's enabled state is a fact this run has
// to be able to read, and "may Fugaro choose a credential for the owner" is
// answered "no" by everything else.
func (r *run) accountAuto(ctx context.Context) bool {
	c := &r.accounts
	if c.read {
		return c.auto
	}
	c.read = true
	if !r.accountWalkApplies() {
		return false
	}
	if _, found, err := r.projectPolicy(ctx); err != nil || !found {
		return false
	}
	db, err := r.budgetClient(ctx)
	if err != nil || db == nil {
		return false
	}
	var node map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathShared(), &node); err != nil {
		r.d.Log.Warn("policy: the account switch could not be read; Fugaro chooses no account for this run", "node", projectpolicy.PathShared())
		return false
	}
	raw, ok := node["claude_account_auto"]
	if !ok {
		return false
	}
	var on bool
	if err := json.Unmarshal(raw, &on); err != nil {
		// A value that is neither true nor false is a person's mistake, and
		// it is not read as "on": the value itself is never echoed, for the
		// reason every message about a node's content here gives.
		r.d.Log.Warn("policy: the account switch is not true or false; Fugaro chooses no account for this run", "node", projectpolicy.PathShared())
		return false
	}
	c.auto = on
	return c.auto
}

// accountWalkApplies reports whether this run stands on the rung the walk
// replaces. The walk is §11.2's rung 5, so an explicit account above it —
// the --token flag, a stage's own key, the committed agent.oauth_token or the
// project default, all of which reach this run as spec.OAuthToken or as the
// resolved agent.oauth_token — leaves the walk nothing to choose, and
// accounts are an OAuth feature: a run authenticating another way names no
// account at all (claudeAccountAllowed refuses one before any launch).
func (r *run) accountWalkApplies() bool {
	if claudeAccountOf(r.spec.OAuthToken) != "" {
		return false
	}
	if r.cfg == nil || r.cfg.Agent.Auth != "oauth" {
		return false
	}
	return claudeAccountOf(r.cfg.Agent.OAuthToken) == ""
}

// noteStageExhausted records that the attempt of stage that has just ended was
// ended by the credential service's exhausted answer, so the stage's next
// attempt is the walk's next account. It is the only thing that advances the
// walk, and it runs where the answer is turned into the stage's end
// (stageOnce): nothing inside an attempt can move a stage to another account.
func (r *run) noteStageExhausted(stage string) {
	c := &r.accounts
	if c.failovers == nil {
		c.failovers = map[string]int{}
	}
	c.failovers[stage]++
}

// checkStageAccount is the walk's stage-start decision, and it returns the
// refusal for a stage this run may not run. "" is every stage this run may:
// the switch is off, the run named its own account, or the walk's choice is
// the account this run holds.
func (r *run) checkStageAccount(ctx context.Context, stage string) string {
	if !r.accountAuto(ctx) {
		return ""
	}
	candidates, err := r.stageAccountCandidates(ctx)
	if err != nil {
		return err.Error()
	}
	at := r.accounts.failovers[stage]
	if at >= len(candidates) {
		return fmt.Sprintf("every account this stage may be served by has already refused it: the run stood on %d of them (%s), and the list `fugaro accounts ls` prints for this repository holds no further one",
			len(candidates), strings.Join(candidates, ", "))
	}
	name := candidates[at]
	if at > 0 {
		r.d.Log.With("stage", stage).Info("account failover: the stage's next attempt is the next account",
			"from", candidates[at-1], "to", name)
	}
	if !r.d.Local {
		// The account a Cloud Run job holds was mounted before this run
		// started, and it is one account's token: the run's own credential
		// (its task's selection, the default account and no selection being
		// the same selection). A stage served by another account is a stage
		// whose record would name an account that did not answer it, so the
		// walk stops the run here rather than switching to a credential this
		// container does not hold.
		if own := claudeAccountOf(r.spec.OAuthToken); own != claudeAccountOf(name) {
			return fmt.Sprintf("the stage's account under claude_account_auto is %q, and this run's own credential is %q: a run holds one account's token, mounted into its job before the run starts, so a stage of it is served by that account and no other. The run stops rather than running this stage as an account its own job does not hold — the record would name an account that did not answer it, and the account's own allow_repos is what bounds which repositories may use its token. Choosing an account per stage needs a job (or a credentials service) that holds the accounts the walk reaches, which this release does not deploy",
				selectedName(name), selectedName(r.spec.OAuthToken))
		}
	}
	r.recordStageAccount(stage, name)
	return ""
}

// recordStageAccount writes the account a stage's attempt is served by into
// the record (D876, §11.5). It is written where the account is chosen, and the
// failover line above is its other half: the line is what the run's log shows
// as it happens, the field what the record keeps afterwards, and neither is
// asked to do the other's job. The record's oauth_account does not move here:
// that field is the account the run used last, as the answers of its calls
// report it (recordCredential), and a second writer would take its meaning.
func (r *run) recordStageAccount(stage, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec == nil {
		return
	}
	if r.rec.StageAccounts == nil {
		r.rec.StageAccounts = map[string]string{}
	}
	r.rec.StageAccounts[stage] = name
}

// stageAccountCandidates is the ordered list this stage's walk stands on: the
// accounts the repository is admitted to, in the order `fugaro accounts ls`
// prints them, narrowed — for a stage that runs in a job of its own — to the
// accounts that job's own variants are built for (stageScope).
func (r *run) stageAccountCandidates(ctx context.Context) ([]string, error) {
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if !found || p == nil {
		// Unreachable through accountAuto, which reads the switch from the
		// node this list comes from, and kept as the floor it would be: a run
		// that can read no policy has one account, the unsuffixed default.
		return []string{config.DefaultClaudeAccount}, nil
	}
	kind := ""
	if job, project := r.projectJob(); project {
		kind = job.Provider
	} else if k, ok := providerForSlug(r.d.Store.Slug(), r.spec.Repo); ok {
		kind = k
	}
	canonical := ""
	if kind != "" {
		if canonical, err = projectpolicy.Canonical(kind, r.spec.Repo); err != nil {
			return nil, err
		}
	}
	return stageAccountCandidates(p, canonical, r.reviewJob()), nil
}

// stageAccountCandidates is the walk's list itself, over the policy the run
// read: the default account first — it is the account a run with no selection
// already uses, and `fugaro accounts ls` prints it first — then every account
// the policy defines whose own list admits this repository, sorted. An account
// the repository is not admitted to is never a candidate (the negative half:
// the walk is the list the owner reads and not a way around claude_accounts),
// and neither is one outside the stage's own scope.
func stageAccountCandidates(p *projectpolicy.Policy, canonical string, reviewClass bool) []string {
	var scope map[string]bool
	if reviewClass {
		scope = reviewClassAccounts(p)
	}
	out := []string{config.DefaultClaudeAccount}
	var named []string
	for name, acct := range p.Accounts {
		if name == config.DefaultClaudeAccount {
			continue
		}
		if canonical == "" || !projectpolicy.AdmitRepo(acct.AllowRepos, canonical) {
			continue
		}
		if scope != nil && !scope[name] {
			continue
		}
		named = append(named, name)
	}
	slices.Sort(named)
	return append(out, named...)
}

// reviewClassAccounts is the set of accounts the independent review job's
// variants exist for: the account axis of every profile the published policy
// publishes, which is the axis those variants are deployed on and the one
// function that computes it (config.ProjectJobAccounts — the deployer, the
// launcher and the credentials service's own set all read the same rule). The
// default account rides the no-account job every profile has, so it is a
// member whenever a profile is; a policy that defines no profile defines no
// review job either, and the empty set then admits no account rather than the
// whole list.
func reviewClassAccounts(p *projectpolicy.Policy) map[string]bool {
	out := map[string]bool{}
	for _, name := range p.ProfileNames() {
		pr, err := p.ProfileFor(name)
		if err != nil {
			// A profile the policy cannot read is not a review job, as it is
			// not one for the credentials service (ReviewJobAccountsFor).
			continue
		}
		for _, account := range config.ProjectJobAccounts(pr.JobProfile) {
			out[selectedName(account)] = true
		}
	}
	return out
}
