package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// authVertex is agent.auth's Vertex spelling, as config.AgentAuths holds it. The
// config package names no constant for this one — the two consts beside it exist
// for the readers that tell those values apart (config's AuthAPIKey, AuthOAuth)
// — and the switch below is a third such reader.
const authVertex = "vertex"

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
//
// For release 1 this record is written BESIDE the table and not instead of it
// (MG3, D3196, R25). A run that bootstrapped on the release behind carries no
// table — the table arrives with this release, and this update is what builds
// the images that carry it — and asks in the old wire, so the service it reaches
// reads this record for it (credsvc's legacy bridge, legacy_auth.go) while a run
// of this release is served by the table alone. X4 stops the write and removes
// the bridge together, which is why the record's content is derived here and
// nowhere else: it is the same answer the table is written from, projected into
// the two members this record has.
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
// It is the repository's own sources, projected into the record's two members
// (repoAuthSources), and nothing else: the record names an auth mode and, under
// oauth, the accounts its jobs go out on, because those are the two things the
// service narrows a non-cascade repository's call by (D2070 (a), R7). The
// projection is deliberately the ONLY place the record's content is decided:
// MG2 and RT5 write the table from the same function's answer, so the record and
// the table cannot name different reaches for one repository — which is the rule
// the migration's first release rests on (D3196, R25: the record gains no source
// the repository does not have, and an entry the table's checks refuse stays on
// the record because it is in no table at all).
//
// Accounts is empty for every other auth, and creds refuses a record that names
// any beside oauth: an account names a subscription, so a record that named one
// under `api-key` or `cascade` would state a reach nothing in the repository's
// configuration asked for. An unlisted auth (`vertex`) is recorded as it is:
// the service holds no credential of that kind and refuses every call for such
// a repository, which is the honest answer rather than a record that says
// something the config does not.
func repoAuthRecord(ready infra.RepoSpec, cfg *config.Config, lc *localcfg.Config) creds.RepoAuth {
	rec := creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: cfg.Agent.Auth}
	for _, src := range repoAuthSources(ready, cfg, lc) {
		if src.Kind == routing.KindPool {
			rec.Accounts = append(rec.Accounts, src.Pool)
		}
	}
	return rec
}

// repoAuthSources is the sources one repository's runs go out on, in the table's
// vocabulary: what its record stands for (credsvc's legacy bridge reads the
// record back into exactly these), and the set that record may never exceed.
//
// It is derived from the repository's own configuration — its agent.auth, and
// under oauth the accounts its own jobs go out on — which is what MG1's
// migration reads to write the table, so the record and the table are two
// projections of ONE answer rather than two answers that can drift. That is the
// whole of D3196's rule read from the writing side: every write of either
// (`update`, `routing set`, an admission change) derives the record from the
// same sources, so it gains no source the repository does not have, and nothing
// here consults the table's checks — an entry those checks refuse is in no table
// and stays on the record, which is exactly why the record is wider than the
// table by those entries and never wider than the repository's own
// pre-migration access.
//
// The oauth accounts are infra.WorkflowAccounts' own answer, and not the
// checkout's agent.oauth_token alone. That function is what decided which job
// variants exist and which token each mounts, and the record has to name the
// accounts those jobs go out on: a run may name an account with `--token` (the
// launch checks the name against config.AllowedClaudeAccounts, which is the same
// list), and the service refuses a declared account the record does not name —
// so a record written from the file's own key alone would refuse a run this
// repository may launch today, on a job that exists today. Reading it from the
// job set keeps the record and the deployed jobs from ever naming different
// accounts.
//
// The cascade's answer is the launcher's own credentials first and the project's
// key after them, which is the cascade's two tiers as the ordinary configuration
// writes them (R4: `fallback: auto` is the key in the chain). The record names
// no `credentials.fallback`, so the chain this function answers is the one MG1
// writes for a repository whose fallback is `auto`, which is the default and the
// case the cascade exists for.
func repoAuthSources(ready infra.RepoSpec, cfg *config.Config, lc *localcfg.Config) []routing.Source {
	switch cfg.Agent.Auth {
	case config.AuthOAuth:
		accounts := infra.WorkflowAccounts(cfg, lc, ready.Name)
		out := make([]routing.Source, 0, len(accounts))
		for _, account := range accounts {
			out = append(out, routing.Source{Kind: routing.KindPool, Pool: account})
		}
		return out
	case config.AuthCascade:
		return []routing.Source{{Kind: routing.KindLauncherPool}, {Kind: routing.KindAnthropic}}
	case config.AuthAPIKey:
		return []routing.Source{{Kind: routing.KindAnthropic}}
	case authVertex:
		return []routing.Source{{Kind: routing.KindVertex}}
	}
	return nil
}
