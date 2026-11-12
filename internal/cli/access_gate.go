package cli

import (
	"context"
	"fmt"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// access_gate.go is the enforcement half of the roles and scopes (RS6c of
// docs/roles-scopes/implementation-plan.md): the check the image, budget,
// accounts, secrets and project-repository commands make before they write, and
// the one `budget show` makes before it reads the project's spend (D1586).
//
// The roster is the grants node and nothing else (F21): this machine's own
// config lists are text a person edits, so a role read from them would be a
// courtesy rather than a check — the reason access.Caller is the credential and
// never localcfg's `user:` key — and an installation with no node has no roster
// at all. Such an installation is one whose `fugaro update` has not migrated
// its lists yet, and these commands behave exactly as they did before this
// task: the update is the one command of the HARD RULE and it arrives at once.
// What is NOT the rule is "everyone holds everything": from the moment the node
// exists every check below is made, and a node that cannot be READ is a refusal
// rather than an empty roster — failing open there would hand the strongest
// roles to whoever the database cannot answer for.
//
// Which action each command asks about is F19's project-level list and the
// decisions it carries: the image catalog and accounts are the project's
// (D1584), the spend document is read at project scope (D1586), and a budget or
// a secret that belongs to one repository needs admin on that repository.

// accessJudged is one command's judgement: the resolver's input, and the caller
// it is about.
type accessJudged struct {
	in     accesslib.Input
	caller string
}

// accessJudge reads the roster for one command, and answers nil when the
// installation has none. repoLevel also reads the published policy's
// allow_repos: it gates a repository-level answer before any grant is read
// (access.Effective), and a project-level question never consults it.
func accessJudge(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, repoLevel bool) (*accessJudged, error) {
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, nil
	}
	if db == nil {
		_, opened, err := budgetDBFor(ctx, lc)
		if err != nil {
			return nil, err
		}
		db = opened
	}
	node, err := accesslib.Read(ctx, db)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	if !node.Found {
		return nil, nil
	}
	in := accesslib.Input{Grants: node.Grants, Implicit: userImplicitAdmins(lc)}
	if repoLevel {
		allow, err := userLsAllowRepos(ctx, lc)
		if err != nil {
			return nil, err
		}
		in.AllowRepos = allow
	}
	caller, err := userCallerLookup(ctx)
	if err != nil {
		return nil, accessNoCaller(err)
	}
	return &accessJudged{in: in, caller: caller}, nil
}

// accessGateProject refuses a project-level change unless the caller holds
// admin at project scope (F19), and it writes nothing. declined opens the
// sentence ("nothing was written") and what names the change in it.
func accessGateProject(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, declined, what string) error {
	return accessGate(ctx, lc, db, accesslib.ActionManageProject, "", declined, what)
}

// accessGateProjectRead is accessGateProject for one of the project-level
// reads, and D1586's: nothing is written either way, so the sentence says what
// is not shown rather than what was not written.
func accessGateProjectRead(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, declined, what string) error {
	return accessGate(ctx, lc, db, accesslib.ActionViewProject, "", declined, what)
}

// accessGateRepo refuses a change to one repository unless the caller holds
// admin on it. The repository is the owner/name the command works in, and the
// provider — the other half of the canonical name a grant's scope is written
// in — is the one the command has already resolved that repository's storage
// slug by (repoProvider: the local config's entry, then the checkout's
// fugaro.yaml), so a repository the command can act on is one the resolver can
// be asked about.
func accessGateRepo(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, repo, declined, what string) error {
	provider := (&cloudEnv{lc: lc}).repoProvider(repo, checkoutConfig(ctx, repo))
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return userErr("%s: %v; nothing was written", safetext.Field(repo), err)
	}
	return accessGate(ctx, lc, db, accesslib.ActionManageRepo, canonical, declined, what)
}

// accessGate is the one check the three above make. declined opens the refusal
// and what names the change in it, and repo is the canonical repository a
// repository-level action names — "" for a project-level one.
func accessGate(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, action accesslib.Action, repo, declined, what string) error {
	role, known := accessActionRole(action)
	if !known {
		return remote(fmt.Errorf("cli: %s is not an action this gate judges", action))
	}
	judged, err := accessJudge(ctx, lc, db, repo != "")
	if err != nil || judged == nil {
		return err
	}
	if accesslib.Allowed(judged.in, judged.caller, action, repo) {
		return nil
	}
	return accessRefusal(judged, role, repo, declined, what)
}

// accessActionRole is the least role an action takes and whether this gate
// judges it at all. access's own table is the authority for both (F19): a
// command that asked about a role the resolver does not require would print a
// sentence about a rule nobody applies.
func accessActionRole(action accesslib.Action) (accesslib.Role, bool) {
	switch action {
	case accesslib.ActionViewProject:
		return accesslib.RoleViewer, true
	case accesslib.ActionManageRepo, accesslib.ActionManageProject:
		return accesslib.RoleAdmin, true
	}
	return accesslib.RoleNone, false
}

// accessRefusal is the sentence a refused command prints: the role and the
// scope the action needs, what the caller holds instead, and the grant to ask
// for. It is a refusal and never a prompt (the global constraints), and it
// names the action by what the command was doing rather than by the resolver's
// word for it.
func accessRefusal(j *accessJudged, role accesslib.Role, repo, declined, what string) error {
	held := userHeldText(userGrantsOf(j.in.Grants, j.caller))
	who := safetext.Field(j.caller)
	if repo == "" {
		return userErr("%s: %s needs %s at project scope, and you (%s) hold %s. A repository role covers repositories and not the project's own — ask a project admin (fugaro user grant %s --role %s --scope project)",
			declined, what, role, who, held, who, role)
	}
	if !projectpolicy.AdmitRepo(j.in.AllowRepos, repo) {
		// A repository the published allow-list does not admit is one nobody
		// holds a role in, whatever the node says: the same rule the launch
		// gate makes, and a refusal that named a role would send its reader to
		// ask for a grant that cannot change the answer.
		return userErr("%s: %s names %s, and the project's published allow_repos does not admit that repository, so no grant can cover it: %s",
			declined, what, safetext.Field(repo), projectpolicy.NotAdmitted(j.in.AllowRepos, repo))
	}
	return userErr("%s: %s needs %s on %s, and you (%s) hold %s. A role is held at the repositories its scope covers — ask a project admin for one covering it (fugaro user grant %s --role %s --scope %s)",
		declined, what, role, safetext.Field(repo), who, held, who, role, safetext.Field(repo))
}

// accessNoCaller is the refusal of a credential the CLI cannot read a member
// from: the caller is the credential and never the local config's `user:` key
// or git config user.email (F21), so a credential nobody can be named from is
// nobody this command can judge.
func accessNoCaller(err error) error {
	return userErr("nothing was written: this command decides what you may change from the credential it is using, and it could not read who that credential belongs to: %v. Sign in with gcloud auth application-default login and run it again", err)
}
