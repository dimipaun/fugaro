package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// writeRepoAuth records what this repository's runs go out on (creds.RepoAuth,
// D2070 (a)). It is the boundary a non-cascade repository's calls are narrowed
// to at the service: the installation's bundle holds every Claude account and
// the paid key alike, so which of them THIS repository's own configuration says
// its runs use is a fact the bundle does not carry, and the run cannot be asked
// for it — the agent holds the run identity and the job's ID token, so a
// statement a caller makes about itself is not a boundary (G16, R7). `fugaro
// update` therefore writes the answer here, from the repository's own config,
// and the service reads it on every call (internal/credsvc's narrowToRepoAuth).
//
// It is written beside the workflow secrets record and out of the same two
// facts (writeWorkflowSecrets, update_workflowsecrets.go): the config a run of
// this repository checks out (deploymentConfig, so a working tree that writes
// one auth and reads another is judged for the document a run sees) and the
// spec of the root this run applied. Every door that applies a repository's
// root reaches it — `fugaro update --repo` and `fugaro setup --repo` are the
// same engine — and which door it was decides nothing: the record says what the
// repository's own configuration says, whichever command read it.
//
// Nothing here stops an upgrade, and nothing here fails one. Every refusal is a
// warning and the run goes on (CLAUDE.md's HARD RULE): the node is new with
// this release, so an installation that ran the release behind has no record at
// all, and the first `fugaro update` after it writes one with no other step.
// --plan-only writes nothing, which is what a coding agent's session reaches
// this engine with and what `--yes`'s help promises there.
//
// The accounts are infra.WorkflowAccounts' own answer for this repository, and
// not the checkout's agent.oauth_token alone. That function is what decided
// which job variants exist and which token each mounts (WorkflowAccounts,
// internal/infra), and the record has to name the accounts those jobs go out
// on: a run may name an account with `--token` (the launch checks the name
// against config.AllowedClaudeAccounts, which is the same list), and the
// service refuses a declared account the record does not name — so a record
// written from the file's own key alone would refuse a run this repository may
// launch today, on a job that exists today. Reading it from the job set keeps
// the record and the deployed jobs from ever naming different accounts.
func (r *initRun) writeRepoAuth(ctx context.Context, lc *localcfg.Config, ready infra.RepoSpec, cfg *config.Config) {
	// A plan changes nothing outside this machine, exactly as --plan-only's
	// help says, and a coding agent's session reaches this engine that way.
	if r.o.planOnly {
		return
	}
	// The record is a node of the installation's budget database, reached over
	// IAM: a run that never opened the cloud, or an installation with no shared
	// backend at all, has none to write to — and the service that reads this
	// record needs the same database, so there is nothing to record for it. A
	// spec with no slug would build no path (creds refuses an empty segment),
	// and a spec always has one: task.Slug answered it earlier in this engine.
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" || ready.Slug == "" {
		return
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("the repository auth record was not written: the database the service reads it from could not be opened (%s)", safetext.FieldErr(err)))
		return
	}
	// The one ownership question every reader and writer of that database asks
	// (budgetDBUnadopted), before anything is written into it: a record written
	// into another installation's database would name this repository's auth to
	// a service that does not serve it.
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "the repository auth record was not written", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	rec := repoAuthRecord(ready, cfg, lc)
	if err := creds.PutRepoAuth(ctx, db, ready.Slug, rec); err != nil {
		r.warn(fmt.Sprintf("the repository auth record was not written: %s", safetext.FieldErr(err)))
		return
	}
	fmt.Fprintf(r.w, "repository auth: %s recorded for %s at %s\n", rec.Auth, ready.Name, creds.PathRepoAuth(ready.Slug))
}

// repoAuthRecord is the record for one repository: its agent.auth, and — under
// oauth — the accounts its runs may go out on.
//
// Accounts is empty for every other auth, and creds refuses a record that names
// any beside one: an account names a subscription, so a record that named one
// under `api-key` or `cascade` would state a reach nothing in the repository's
// configuration asked for. An unlisted auth (`vertex`) is recorded as it is:
// the service holds no credential of that kind and refuses every call for such
// a repository, which is the honest answer rather than a record that says
// something the config does not.
func repoAuthRecord(ready infra.RepoSpec, cfg *config.Config, lc *localcfg.Config) creds.RepoAuth {
	rec := creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: cfg.Agent.Auth}
	if cfg.Agent.Auth == config.AuthOAuth {
		rec.Accounts = infra.WorkflowAccounts(cfg, lc, ready.Name)
	}
	return rec
}
