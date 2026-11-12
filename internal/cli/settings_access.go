package cli

import (
	"context"
	"slices"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// settingsAccessGate is the one question every settings, config, policy and
// recipe write asks before it lands: may this caller make this write? (RS6b of
// docs/roles-scopes.)
//
// What is new is only WHO MAY CALL the write. What the write then does is
// unchanged: a repository's own value already goes through the project layer's
// only-tightening merge (finding F9), the widening fences are the ones they
// were, and no guardrail machinery is added here.
//
// The table is the layer the write lands in, which is what the registry's own
// Layers decide for each key (internal/settings, confirmed against it):
//
//   - the project layer's file, this machine's own project config, the
//     published layer, the published policy and a project recipe are
//     project-level surfaces, and they take admin at project scope
//     (ActionManageProject, F19). An all-repos admin holds no project-level
//     thing, which is the whole difference between the two admin scopes.
//   - a repository's own fugaro.yaml takes admin at a scope covering that
//     repository (ActionManageRepo), which an all-repos admin and a
//     repository-scoped admin hold over the repositories they name.
//
// Who the caller is comes from the credential and never from this machine's
// config or git (finding F21), and it is read through userCallerLookup, the one
// seam `fugaro user grant` reads it through.
//
// An installation with no roles at all — no grants node and no lists of its own
// — is not judged: its roles are nobody's, which is the state every
// installation that has not migrated its lists is in, and one it converges out
// of at its next `fugaro update` (RS3). And a caller the roles it DOES have
// place nowhere is admitted rather than refused, which is the same answer
// `fugaro user grant` gives one (user_grant.go's own doc): this installation's
// GCP owners and editors are implicit project admins (F17.3) and live in its
// IAM policy, which the CLI does not read here. Both are stated limits rather
// than oversights, and the second one is pinned by
// TestAccessSettingsUnplacedCallerIsTheImplicitAdmin.
//
// repo is the canonical repository a repository-level write is about, and it is
// ignored by a project-level one (Allowed reads the project scope instead).
// what names the write for the refusal.
func settingsAccessGate(ctx context.Context, lc *localcfg.Config, action accesslib.Action, repo, what string) error {
	in, roles, err := settingsAccessInput(ctx, lc)
	if err != nil || !roles {
		return err
	}
	caller, err := userCallerLookup(ctx)
	if err != nil {
		return userErr("this command decides what you may change from the credential it is using, and it could not read who that credential belongs to: %v. The caller is the credential and never the local config's `user:` key or git config user.email (finding F21): sign in with gcloud auth application-default login and run this again", err)
	}
	if !settingsAccessPlaces(in, caller) {
		return nil
	}
	if accesslib.Allowed(in, caller, action, repo) {
		return nil
	}
	return settingsAccessRefused(action, repo, what, userHeldText(userGrantsOf(in.Grants, caller)))
}

// settingsAccessWrite is settingsAccessGate for `settings set` and
// `settings unset`: the layer the write lands in is what makes it a
// project-level or a repository-level surface, and a repository-level one
// resolves the canonical repository the write is judged in first.
//
// It is asked where the write would be made and never before: a key that
// already reads what was asked for, or one that is not set at all, is no write
// and needs no role (both commands return before this call).
func settingsAccessWrite(ctx context.Context, lc *localcfg.Config, o settingsWriteOptions, target settingsTarget, what string) error {
	if target.layer != settingsLayerRepo {
		return settingsAccessGate(ctx, lc, accesslib.ActionManageProject, "", what)
	}
	repo, err := settingsAccessRepo(ctx, lc, o)
	if err != nil {
		return err
	}
	return settingsAccessGate(ctx, lc, accesslib.ActionManageRepo, repo, what)
}

// settingsAccessRepo is the canonical repository a `--layer repo` write is
// judged in. It takes the two routes settingsRepoFile takes to find the FILE,
// so the repository judged and the file written are resolved from one argument:
// the --repo the caller named (already reduced to owner/name by repoArg), else
// the repository of the checkout the command runs in.
//
// A checkout whose repository cannot be told — no origin remote, an origin that
// is not a github or bitbucket URL — is refused rather than guessed at: the
// write is a repository's own settings, and a repository this command cannot
// name is one it cannot say the caller is an admin of.
func settingsAccessRepo(ctx context.Context, lc *localcfg.Config, o settingsWriteOptions) (string, error) {
	repo := o.repo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return "", userErr("this checkout's repository cannot be told from its origin, so the role this write needs cannot be resolved (%v): pass --repo owner/name", err)
		}
	}
	return canonicalRepoArg(lc, repo)
}

// settingsAccessInput is the resolver's own input, and whether this
// installation has any roles to judge the caller against at all.
//
// The roles are the grants node where the installation has one (D1585), and
// otherwise the two IAM lists of this machine's own config, which are the whole
// of the roles an installation with no Firebase backend has (accessNoBackend).
// The node is read once, under the same two checks every other reader of that
// database makes (the database is this project's, and it is reachable at all):
// a write judged against another project's grants, or against none because a
// read failed, would be a write judged by nobody.
func settingsAccessInput(ctx context.Context, lc *localcfg.Config) (accesslib.Input, bool, error) {
	in := accesslib.Input{Implicit: userImplicitAdmins(lc), AllowRepos: settingsAccessEveryRepo}
	if lc.Budget != nil && lc.Budget.RTDBURL != "" {
		_, db, err := budgetDBFor(ctx, lc)
		if err != nil {
			return in, false, err
		}
		if err := checkDBProject(ctx, db, lc); err != nil {
			return in, false, err
		}
		node, err := accesslib.Read(ctx, db)
		if err != nil {
			return in, false, dbErr(lc, err, false)
		}
		if node.Found {
			in.Grants = node.Grants
			return in, true, nil
		}
	}
	grants, err := migrationGrants(lc.Terraform.Operators, lc.Terraform.Admins)
	if err != nil {
		return in, false, err
	}
	in.Grants = grants
	return in, len(in.Grants)+len(in.Implicit) > 0, nil
}

// settingsAccessEveryRepo is the allow_repos this gate resolves under, and it
// is deliberately the list that admits everything.
//
// The published policy's allow_repos decides which repositories can RUN, and
// the role a person holds in one is resolved under it (access.Effective) so
// that nobody is said to hold a role in a repository the project does not have.
// A settings write is not a run: it is an edit of a file — a checkout's own
// file, or this machine's copy of the project's — and making it depend on the
// published node would have `fugaro settings set` read the runs bucket before
// every edit of a file. A repository the policy does not admit is refused where
// it is admitted or launched, by the gates that already read the node.
var settingsAccessEveryRepo = []string{"*"}

// settingsAccessPlaces reports whether either input this CLI can read places
// caller at all: a grant naming them, or an implicit admin it can see.
func settingsAccessPlaces(in accesslib.Input, caller string) bool {
	return len(userGrantsOf(in.Grants, caller)) > 0 || slices.Contains(in.Implicit, caller)
}

// settingsAccessRefused is the refusal of a write the caller's own roles do not
// hold. It names the role and the scope the write needs and what the caller
// holds (F14: a role refusal names both, and it never asks a question), and it
// says which of the two rules refused rather than leaving the caller to work
// out why their own repository's admin does not reach the project's file.
func settingsAccessRefused(action accesslib.Action, repo, what, held string) error {
	if action == accesslib.ActionManageProject {
		return userErr("nothing was written: %s is a project-level write, and it needs admin at project scope — an all-repos admin manages every repository's own settings and none of the project's, and a repository-scoped admin holds their own repository's (finding F19). You hold %s; ask a project admin, or this installation's owner", what, held)
	}
	return userErr("nothing was written: %s changes %s's own settings, and it needs admin at a scope covering %s (finding F19). You hold %s; a viewer and an operator hold no settings write at all, and an admin's write reaches the repositories their scope names", what, safetext.Field(repo), safetext.Field(repo), held)
}
