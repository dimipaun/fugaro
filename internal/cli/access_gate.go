package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// access_gate.go is where the commands that launch a run, cancel one, read a
// run's status, read its logs and list the history ask the resolver
// (docs/roles-scopes, D1580 and D1587; plan task RS6a).
//
// The check is a CLI check and its refusals say so: what a credential reaches
// in GCP is unchanged by it, and before RS12 the cloud holds no per-repository
// grant a scope could be enforced with (D1580, D1591(c)). What it buys is a
// refusal while the person is still there to read it, before a task is stored,
// a claim is written or an execution exists.
//
// Who the caller is comes from the credential the command is using
// (access.Caller, finding F21) and never from the local config's `user:` key or
// git config: both are the caller's own to write, so either would place
// anybody.
//
// An installation whose roles place NOBODY at all is not refused — no stored
// grant, no implicit admin, and, where there is no database to hold a node, no
// `user:` member on either of its own lists. There is nothing to judge against
// there, and a machine that has never held a role is not one whose owner should
// be locked out of their own runs by the check that reads it. The gate bites
// the moment one thing places one member, which every installation that has
// ever had an operator or an admin does: RS3 migrates those lists into the
// node, and the lists themselves are read below where there is no node.

// accessCallerLookup is who a command thinks it is, in the "user:<address>" form
// the grants are written with: access.Caller, the verified address of the
// credential the command will use (F21). It is a variable for the reason
// userCallerLookup and pairMeLookup are: a test's own function stands here, so
// no test reaches Google's userinfo endpoint.
var accessCallerLookup = func(ctx context.Context) (string, error) { return accesslib.Caller(ctx) }

// accessAction is how one action reads in this file's refusals: the gerund a
// person reads, and the least role the action takes.
//
// The role is the resolver's own table restated (access.Allowed's actionRules),
// which is deliberate and is the one duplication here: the check is the
// resolver's — a command asks access.Allowed and never this map — and the
// sentence has to name the role a person must be granted, which Allowed's
// boolean does not carry. TestAccessRunActionRolesAgreeWithTheResolver derives
// each action's least role from Allowed and fails on the day the two drift.
type accessAction struct {
	word string
	role accesslib.Role
}

// accessActions is the set of actions the commands of this plan ask about.
// An action that is not here is refused rather than allowed (the same
// fail-closed rule access.Allowed makes for one it does not know): the gate
// exists to refuse, and one it cannot word a role for is one it cannot judge.
var accessActions = map[accesslib.Action]accessAction{
	accesslib.ActionViewRepo: {"reading", accesslib.RoleViewer},
	accesslib.ActionLaunch:   {"launching", accesslib.RoleOperator},
	accesslib.ActionCancel:   {"cancelling", accesslib.RoleOperator},
}

// requireAccess refuses action on repo unless the credential's roles cover it.
//
// repo is a canonical repository (projectpolicy.Canonical): a scope is written
// in that form and access.Effective matches it there. what is the clause the
// refusal opens with, in the command's own words ("nothing was launched").
func requireAccess(ctx context.Context, env *cloudEnv, action accesslib.Action, repo, what string) error {
	in, caller, placed, err := accessJudge(ctx, env, what)
	if err != nil || !placed {
		return err
	}
	if accesslib.Allowed(in, caller, action, repo) {
		return nil
	}
	return accessRefusal(env, in, caller, action, repo, what)
}

// requireRunAccess is requireAccess for a run a command has the slug of and
// nothing else (cancel, logs): the repository is this machine's config entry
// for that slug (accessSlugRepos). A slug the config cannot name is judged at
// project scope by requireProjectAccess.
func requireRunAccess(ctx context.Context, env *cloudEnv, action accesslib.Action, slug, what string) error {
	if canonical, ok := accessSlugRepos(env.lc)[slug]; ok {
		return requireAccess(ctx, env, action, canonical, what)
	}
	return requireProjectAccess(ctx, env, action, slug, what)
}

// requireProjectAccess is requireAccess for a subject nothing here can name as
// a canonical repository — a run stored under a slug this machine's config does
// not list, a row whose task is unreadable. It asks the project-scope question
// instead: no repository-scoped grant can be about a repository nothing can
// name, so only a role covering the whole installation reaches it.
func requireProjectAccess(ctx context.Context, env *cloudEnv, action accesslib.Action, slug, what string) error {
	a, ok := accessActions[action]
	if !ok {
		return accessUnworded(action, what)
	}
	in, caller, placed, err := accessJudge(ctx, env, what)
	if err != nil || !placed {
		return err
	}
	if accesslib.EffectiveAtProject(in, caller).AtLeast(a.role) {
		return nil
	}
	return accessProjectRefusal(env, in, caller, action, slug, what)
}

// accessJudge is the gate's preamble: what the resolver is asked with, the
// credential's own member, and whether anything places anybody at all. placed
// false means the check does not apply, and no caller is read for it — an
// installation that place nobody cannot refuse the one credential it has.
func accessJudge(ctx context.Context, env *cloudEnv, what string) (in accesslib.Input, caller string, placed bool, err error) {
	if in, placed, err = accessInput(ctx, env); err != nil || !placed {
		return accesslib.Input{}, "", placed, err
	}
	caller, err = accessCallerLookup(ctx)
	if err != nil {
		return accesslib.Input{}, "", true, userErr("%s: the roles are read from the credential this command is using (finding F21), and it could not read who that credential belongs to: %v. Sign in with gcloud auth application-default login and run this again", what, err)
	}
	return in, caller, true, nil
}

// accessInput is what the resolver is asked with: the stored grants, the
// implicit admins this machine can name, and the published allow_repos. placed
// is whether anything in it places anybody.
func accessInput(ctx context.Context, env *cloudEnv) (in accesslib.Input, placed bool, err error) {
	in = accesslib.Input{
		Implicit:   userImplicitAdmins(env.lc),
		AllowRepos: accessAllowRepos(ctx, env),
	}
	if in.Grants, err = accessStoredGrants(ctx, env); err != nil {
		return accesslib.Input{}, false, err
	}
	return in, len(in.Grants)+len(in.Implicit) > 0, nil
}

// accessStoredGrants is the grants node's pairs, or what this machine's own
// lists hold when there is no node to read.
//
// An absent node and an installation with no database are one state in this
// reading: the roles are then this machine's own terraform.operators and
// terraform.admins, which is what the node is migrated FROM (RS3) and what
// `fugaro user ls` prints for the same installation. What the lists are not is
// a second opinion about a node that exists — a node is the installation's
// roles, and a list still naming somebody the node no longer places does not
// place them here. pair.go reads the same node to the same effect: the grants
// are what place the caller, and the two lists are only what update migrated
// into it.
//
// A node that is there and cannot be read is a refusal rather than a fallback.
// D1585 gives the node to a caller holding the database grant, which
// roles/firebasedatabase.viewer carries to every launcher and operator, so a
// credential it refuses holds no IAM on this project at all — the
// repository-scoped user of F29(b), who reaches nothing in the cloud either
// before RS12. Judging that caller by this machine's own lists would place them
// with a list they do not hold.
func accessStoredGrants(ctx context.Context, env *cloudEnv) ([]accesslib.Grant, error) {
	if env.lc.Budget == nil || env.lc.Budget.RTDBURL == "" {
		return accessListGrants(env.lc), nil
	}
	_, db, err := budgetDBFor(ctx, env.lc)
	if err != nil {
		return nil, err
	}
	node, err := accesslib.Read(ctx, db)
	switch {
	case errors.Is(err, rtdb.ErrPermission):
		return nil, userErr("the roles could not be read: %s refused this credential's read (%v), and the installation's roles are that node. Every launcher and operator of %s holds it (roles/firebasedatabase.viewer); a repository-scoped pair alone does not, and before RS12 it grants nothing in the cloud either (finding F29b)", accesslib.PathNode, err, safetext.Field(env.lc.Name))
	case err != nil:
		return nil, dbErr(env.lc, err, false)
	}
	if !node.Found {
		return accessListGrants(env.lc), nil
	}
	return node.Grants, nil
}

// accessListGrants is this machine's own two lists as the resolver's grants:
// terraform.operators at operator and terraform.admins at admin, both at
// project scope, which is the scope those IAM lists always meant and the shape
// RS3 migrates into the node.
//
// A member that is not a `user:` address is left out, and so is one
// access.Member refuses: a group:, domain: or serviceAccount: member keeps the
// IAM grant the installation derives but is not one the CLI can match against a
// caller (F25), and nothing else on the list is a role either. Both lists may
// then place nobody, which the caller reads as the check not applying.
func accessListGrants(lc *localcfg.Config) []accesslib.Grant {
	if lc == nil {
		return nil
	}
	project := accesslib.Scope{Kind: accesslib.ScopeProject}
	var out []accesslib.Grant
	for _, l := range []struct {
		role    accesslib.Role
		members []string
	}{
		{accesslib.RoleOperator, lc.Terraform.Operators},
		{accesslib.RoleAdmin, lc.Terraform.Admins},
	} {
		for _, m := range l.members {
			address, ok := strings.CutPrefix(m, "user:")
			if !ok {
				continue
			}
			member, err := accesslib.Member(address)
			if err != nil {
				continue
			}
			out = append(out, accesslib.Grant{User: member, Role: l.role, Scope: project})
		}
	}
	return out
}

// accessAllowRepos is the published policy's allow_repos, or an allow-list that
// admits everything when none is published or the node cannot be read.
//
// A scope never admits a repository outside allow_repos (F27), and reading an
// unreadable node as an empty list would refuse every repository instead. This
// gate is also not the reader that owns a policy it cannot read: the launch
// gate refuses that one itself, one step before this check runs, and reading
// the logs of a run that already exists is not a policy decision — the run is
// there whether the policy still admits its repository or not.
func accessAllowRepos(ctx context.Context, env *cloudEnv) []string {
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil || !found {
		return []string{"*"}
	}
	return p.AllowRepos
}

// accessRunRepo is the canonical repository a run belongs to, from the slug it
// is stored under and the repository its own record or task names. The slug is
// derived from the provider and that repository (task.Slug), so the pair names
// the provider back (runner.ProviderForSlug) and the canonical form follows.
// ok is false for a run nothing names a repository for — the record and the
// task both unreadable or absent — and for a pair no provider kind agrees
// about, and the callers above then judge at project scope rather than guess.
func accessRunRepo(slug, repo string) (canonical string, ok bool) {
	if slug == "" || repo == "" {
		return "", false
	}
	kind, ok := runner.ProviderForSlug(slug, repo)
	if !ok {
		return "", false
	}
	canonical, err := projectpolicy.Canonical(kind, repo)
	if err != nil {
		return "", false
	}
	return canonical, true
}

// accessSlugRepos is the canonical repository of each storage slug this
// machine's config lists, keyed by the slug itself: logs resolves a run from
// its slug, and the canonical form needs the provider, which only the config's
// own entry carries. An entry with no provider is left out rather than guessed
// at — the checkout's fugaro.yaml decides its provider at run time — which is
// what userKnownRepos leaves out for `fugaro user ls` and for the same reason.
func accessSlugRepos(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	if lc == nil {
		return out
	}
	for name, r := range lc.Repos {
		if r.Provider == "" {
			continue
		}
		slug, err := task.Slug(r.Provider, name)
		if err != nil {
			continue
		}
		canonical, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		out[slug] = canonical
	}
	return out
}

// accessRowGate is `runs ls`'s reading of the roles: the input and the caller
// are read once for the whole command, because a --watch redraw judges the same
// credential against the same node and against a policy that does not change
// under it.
type accessRowGate struct {
	in     accesslib.Input
	caller string
	// inert is the installation that places nobody: every row is listed, and
	// no credential was read for it (access_gate.go's own rule).
	inert bool
}

// accessRowsGate reads the roles for a listing. Its refusals are requireAccess's
// — a credential whose address cannot be read is refused rather than shown
// everything, which is the same choice every other command here makes.
func accessRowsGate(ctx context.Context, env *cloudEnv) (accessRowGate, error) {
	in, caller, placed, err := accessJudge(ctx, env, "nothing was listed")
	if err != nil {
		return accessRowGate{}, err
	}
	if !placed {
		return accessRowGate{inert: true}, nil
	}
	return accessRowGate{in: in, caller: caller}, nil
}

// listed is the rows this credential may read, and how many it may not. A row
// follows its own repository (accessRunRepo), and one nothing can name — its
// task unreadable, or a slug no provider kind agrees about — is judged at
// project scope, the rule requireProjectAccess words for a single run.
//
// D1586 is the reason the listing is filtered at all: a reader holds its role
// per repository, so the history it reads is the history of its own.
func (g accessRowGate) listed(rows []runview.Row) (kept []runview.Row, dropped int) {
	if g.inert {
		return rows, 0
	}
	for _, r := range rows {
		if g.visible(r) {
			kept = append(kept, r)
			continue
		}
		dropped++
	}
	return kept, dropped
}

// visible is whether this credential may read one row's repository.
func (g accessRowGate) visible(r runview.Row) bool {
	slug, _, err := runstore.ParseRef(r.Run)
	if err != nil {
		slug = r.Run
	}
	if canonical, ok := accessRunRepo(slug, r.Repo); ok {
		return accesslib.Allowed(g.in, g.caller, accesslib.ActionViewRepo, canonical)
	}
	return accesslib.EffectiveAtProject(g.in, g.caller).AtLeast(accesslib.RoleViewer)
}

// accessRefusal is what a repository the credential holds no role in reads: the
// role the action takes, the repository, what the credential holds there, and
// the one command that changes it.
func accessRefusal(env *cloudEnv, in accesslib.Input, caller string, action accesslib.Action, repo, what string) error {
	a, ok := accessActions[action]
	if !ok {
		return accessUnworded(action, what)
	}
	if !projectpolicy.AdmitRepo(in.AllowRepos, repo) {
		// The published policy answers this one and no grant can change it, so
		// the policy's own refusal is the sentence a person needs (F27; the
		// launcher's twin of it reads the same words).
		return userErr("%s: %s holds no role in %s and no grant can make one: %s", what, caller, repo, projectpolicy.NotAdmitted(in.AllowRepos, repo))
	}
	held := "no role"
	if r := accesslib.Effective(in, caller, repo); r != accesslib.RoleNone {
		held = "the role " + string(r)
	}
	return userErr("%s: %s %s needs the role %s in it, and the credential this command is using (%s) holds %s there.%s",
		what, a.word, repo, a.role, caller, held, accessFix(env, caller, a.role, repo))
}

// accessProjectRefusal is accessRefusal for a run whose repository this
// machine's config cannot name: the question is the credential's project-scope
// role, because a grant at a repository scope is about repositories that have a
// canonical name.
func accessProjectRefusal(env *cloudEnv, in accesslib.Input, caller string, action accesslib.Action, slug, what string) error {
	a, ok := accessActions[action]
	if !ok {
		return accessUnworded(action, what)
	}
	held := "no role at project scope"
	if r := accesslib.EffectiveAtProject(in, caller); r != accesslib.RoleNone {
		held = "the role " + string(r) + " at project scope"
	}
	return userErr("%s: the run is stored under %s, a repository this machine's config cannot name (provider/owner/name), so %s it is judged at project scope and the credential this command is using (%s) holds %s there.%s",
		what, safetext.Field(slug), a.word, caller, held, accessFix(env, caller, a.role, "project"))
}

// accessFix is the half of both refusals above that is about the credential
// rather than about the role: where the roles come from, what this credential
// holds, and the command that grants one. scope is what the grant's own flag
// takes — a canonical repository, or "project".
func accessFix(env *cloudEnv, caller string, role accesslib.Role, scope string) string {
	return fmt.Sprintf(" The roles are read from the credential the command is using, never from the local config's user: key or git config (finding F21), so a grant written for another address does not place this one. What this credential holds is what '%s user ls --repo %s' prints, and an admin of %s changes it with '%s user grant %s --role %s --scope %s'",
		selfCommand(), scope, safetext.Field(env.lc.Name), selfCommand(), caller, role, scope)
}

// accessUnworded is the refusal of an action this build has no role sentence
// for. It is a programming error rather than a state a person can be in, and it
// refuses: a gate that cannot name the role it is asking for is one that cannot
// judge the action either (access.Allowed makes the same choice for an action
// it does not know).
func accessUnworded(action accesslib.Action, what string) error {
	return userErr("%s: %q is not an action this build knows the role for, so it is refused rather than allowed", what, string(action))
}
