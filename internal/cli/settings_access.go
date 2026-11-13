package cli

import (
	"context"
	"errors"
	"slices"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Every settings, config, policy and recipe write asks one question before it
// lands: may this caller make this write? (RS6b of docs/roles-scopes.)
// settingsAccessGate asks it for a project-level write and settingsAccessWrite
// for `settings set` and `settings unset`, whose layer decides which write it
// is.
//
// What is new is only WHO MAY CALL the write. What the write then does is
// unchanged: a repository's own value already goes through the project layer's
// only-tightening merge (finding F9), the widening fences are the ones they
// were, and no guardrail machinery is added here.
//
// The table is the layer the write lands in, which is what the registry's own
// Layers decide for each key (internal/settings, confirmed against it):
//
//   - the project layer's file, the published layer, the published policy and a
//     project recipe are project-level surfaces, and they take admin at project
//     scope (ActionManageProject, F19). An all-repos admin holds no
//     project-level thing, which is the whole difference between the two admin
//     scopes.
//   - a repository's own fugaro.yaml takes admin at a scope covering that
//     repository (ActionManageRepo), which an all-repos admin and a
//     repository-scoped admin hold over the repositories they name.
//   - a `--layer local` write is not judged: it edits this machine's own
//     project config, the file the person at this terminal owns, and SD18
//     answered that question as it stands — "anyone may edit their own file;
//     the boundary stays IAM at publish", with enforcing roles locally rejected
//     because a file the user owns cannot carry the enforcement
//     (docs/settings-commands spec §8). The layer file a `--layer project`
//     write edits is the project's own copy of its guardrails and is judged
//     with it, because F19 names the project layer itself.
//
// What this gate does NOT reach is stated here rather than left to be found:
// the write a `--layer local` edit leads to is not judged either. `fugaro
// update` publishes the layer (`init_layer_stage.go`) and the policy node
// (`init_policy_publish.go`) from its own stages, and asks no role, so a caller
// this gate refuses in front of `config publish` or `project policy publish`
// still reaches both writes through `update` — under whatever cloud
// permissions that caller holds, which is the boundary SD18 leaves in place.
// The published policy node has two other writers that ask no role either:
// `fugaro project repos add|rm` and `fugaro project sync` edit its allow_repos
// through the same ETag-guarded write `project policy publish` uses
// (putAllowRepos in project_enroll.go), so a caller this gate refuses the
// publish can still widen the list of repositories the project admits with
// `project repos add '*'` — the widening the gate on the publish exists to
// stop, and the gap in it worth closing first (Slice D of the plan: every
// management command asks the resolver). What bounds that write until then is
// the cloud permission the node itself needs.
// And the writers outside this task that reach the same single write path,
// `applySettingsEdit`, without asking are the ones whose keys live in the same
// three files: `fugaro rigor set` (the project layer file); `fugaro workflow
// add|rm` and `fugaro models use` (a repository's fugaro.yaml); and, in this
// machine's own project config, `project repos set`, `project routing set`,
// `project profile set|add|rm`, `models add|rm`, `rigor floor`, `accounts …`,
// `secrets …`, `workspace` and `project clone-url`. RS6b covers the commands in
// its own file list; the same list is in docs/roles-scopes/spec.md, and until
// the rest is gated the enforcement is partial rather than complete.
//
// Who the caller is comes from the credential and never from this machine's
// config or git (finding F21), and it is read through userCallerLookup, the one
// seam `fugaro user grant` reads it through. A service account's credential is
// admitted rather than refused, and it is the ONLY credential that is: such an
// address is not a person the grants can name, access.Caller refuses it as
// access.ErrServiceAccount, and the two explicit publish doors are what a
// script or a runbook with such a credential uses — `fugaro config publish
// FILE` and `fugaro project policy publish` keep working under it
// (settings-commands spec §9). Refusing there would turn this gate into a
// requirement to be a person, which no design here asks for.
//
// Every other way the caller cannot be read — no credentials, an expired or
// unverified one, the provider not answering — refuses the write instead. That
// is the difference between a fact and a gap: a service account is a credential
// this design knows the answer for, while an identity lookup that failed is a
// question left unanswered, and a check that admits whenever it cannot read the
// credential is off exactly when the credential is broken.
//
// An installation with no roles at all — no grants node and no lists of its own
// — is not judged: its roles are nobody's, which is the state every
// installation that has not migrated its lists is in, and one it converges out
// of at its next `fugaro update` (RS3). Neither is one whose database this CLI
// cannot read at all (settingsAccessInput). And a caller the roles it DOES have
// place nowhere is admitted rather than refused, which is the same answer
// `fugaro user grant` gives one (user_grant.go's own doc): this installation's
// GCP owners and editors are implicit project admins (F17.3) and live in its
// IAM policy, which the CLI does not read here. The last two are stated limits
// rather than oversights — the gate decides what it can read and never more —
// and they are pinned by
// TestAccessSettingsUnplacedCallerIsTheImplicitAdmin and
// TestAccessSettingsUnreachableDatabaseIsNotJudged.
//
// what names the write for the refusal.
func settingsAccessGate(ctx context.Context, lc *localcfg.Config, what string) error {
	in, roles, err := settingsAccessInput(ctx, lc)
	if err != nil || !roles {
		return err
	}
	return settingsAccessJudge(ctx, in, accesslib.ActionManageProject, what, settingsAccessNoRepo)
}

// settingsAccessWrite is settingsAccessGate for `settings set` and
// `settings unset`: the layer the write lands in is what makes it a
// project-level or a repository-level surface, and a repository-level one
// resolves the canonical repository the write is judged in. The local layer is
// neither: it is this machine's own file and SD18's answer is that whoever is
// at this terminal edits it.
//
// It is asked where the write would be made and never before, and it is asked
// for a write alone: a key that already reads what was asked for, or one that is
// not set at all, is no write (both commands return before this call), and a
// --dry-run makes none either — it answers with the document the write would
// produce, which is how a caller the roles refuse hands an admin the change to
// make (and the agent-session fence, which a dry run does meet, is about the
// session rather than the file).
func settingsAccessWrite(ctx context.Context, lc *localcfg.Config, o settingsWriteOptions, target settingsTarget, what string) error {
	if o.dryRun || target.layer == settingsLayerLocal {
		return nil
	}
	if target.layer != settingsLayerRepo {
		return settingsAccessGate(ctx, lc, what)
	}
	in, roles, err := settingsAccessInput(ctx, lc)
	if err != nil || !roles {
		return err
	}
	return settingsAccessJudge(ctx, in, accesslib.ActionManageRepo, what, func() (string, error) {
		return settingsAccessRepo(ctx, lc, o)
	})
}

// settingsAccessNoRepo is repoOf for a write judged at the project's scope: no
// repository enters that question.
func settingsAccessNoRepo() (string, error) { return "", nil }

// settingsAccessJudge is the judgement itself: who is calling, whether the
// roles this installation has place them at all, and whether they may make the
// write. repoOf is asked for the repository the write is about only once the
// caller is placed and only where the action reads one, so neither a checkout
// whose repository cannot be told nor a repository the caller never named is
// resolved for a write no role of theirs decides.
//
// Only a service account's credential is admitted unread — that is what
// access.ErrServiceAccount marks out, and the F21 paragraph above says why —
// and every other failure to read the caller refuses the write rather than
// letting it through, because a caller that admits whenever it cannot read a
// credential is one that stops checking as soon as the credential breaks.
func settingsAccessJudge(ctx context.Context, in accesslib.Input, action accesslib.Action, what string, repoOf func() (string, error)) error {
	caller, err := userCallerLookup(ctx)
	switch {
	case errors.Is(err, accesslib.ErrServiceAccount):
		return nil
	case err != nil:
		return settingsAccessUnreadable(what, err)
	}
	if !settingsAccessPlaces(in, caller) {
		return nil
	}
	repo := ""
	if action == accesslib.ActionManageRepo {
		if repo, err = repoOf(); err != nil {
			return err
		}
	}
	if accesslib.Allowed(in, caller, action, repo) {
		return nil
	}
	return settingsAccessRefused(action, repo, what, userHeldText(userGrantsOf(in.Grants, caller)))
}

// settingsAccessRepo is the canonical repository a `--layer repo` write is
// judged in. It takes the two routes settingsRepoFile takes to find the FILE,
// so the repository judged and the file written are resolved from one argument:
// the --repo the caller named (already reduced to owner/name by repoArg), else
// the repository of the checkout the command runs in.
//
// The checkout's repository comes from its own origin remote and never from
// this machine's config. settingsRepoFile resolves a `--layer repo` write in a
// checkout the config need not list — the checkout in the working directory
// needs no entry at all — so judging it through canonicalRepoArg, which takes a
// bare owner/name's provider from the repos: map, would refuse a write whose
// file had already been resolved, and refuse it with the advice to write the
// value as provider/owner/name: a value this command's own --repo check rejects
// ("a repository is owner/name"), so a remedy its caller cannot take. The
// origin remote has both halves the judgement needs: its host is the provider
// (providerForHost, the rule recordCheckout remembers a checkout by) and its
// path is the repository.
//
// A checkout whose repository cannot be told — no origin remote, an origin that
// is not a github or bitbucket URL, a host no provider lives on, an origin the
// checkout's own git config rewrites — is refused rather than guessed at: the
// write is a repository's own settings, and a repository this command cannot
// name is one it cannot say the caller is an admin of. The refusal names the two
// ways out, and both are real: a `--repo owner/name` writes the checkout this
// machine remembers for that repository, not the anonymous one the command runs
// in — the same route settingsRepoFile takes — so the remedy for a checkout
// nothing remembers is its own origin remote.
func settingsAccessRepo(ctx context.Context, lc *localcfg.Config, o settingsWriteOptions) (string, error) {
	if o.repo == "" {
		// The checkout's own origin, not originRepo's owner/name through
		// canonicalRepoArg: that takes a bare owner/name's provider from the
		// config's repos: map and refuses a checkout the config does not list,
		// which is the write the comment above says must go through.
		return settingsAccessOriginRepo(ctx)
	}
	repo, err := canonicalRepoArg(lc, o.repo)
	if err != nil {
		return "", settingsAccessUnlistedRepo(o.repo)
	}
	return repo, nil
}

// settingsAccessOriginRepo is provider/owner/name of the checkout the command
// runs in, read from its origin remote: readOrigin answers the host and the
// repository (and whether the checkout's own git config rewrites the origin,
// which makes the name untrustworthy), and providerForHost turns the host into
// the provider — the pair a checkout is remembered by.
func settingsAccessOriginRepo(ctx context.Context) (string, error) {
	oi, ok := readOrigin(ctx, ".")
	if ok && !oi.Rewritten {
		if provider := providerForHost(oi.Host); provider != "" {
			if canonical, err := projectpolicy.Canonical(provider, oi.Repo); err == nil {
				return canonical, nil
			}
		}
	}
	return "", settingsAccessNoOrigin()
}

// settingsAccessNoOrigin is that refusal. It does not advise writing the
// repository canonically: `--repo provider/owner/name` is refused by
// settingsRepoFile, which takes owner/name alone, and the checkout this command
// would then write is the one this machine remembers rather than the one it
// runs in.
func settingsAccessNoOrigin() error {
	return userErr("nothing was written: this checkout's repository cannot be told from its origin (a github.com or bitbucket.org remote naming owner/name), so the repository whose settings this write would change — and the role that write needs — cannot be named: add the origin remote (git remote add origin <url>), or write that repository's settings from a checkout this machine remembers with --repo owner/name")
}

// settingsAccessUnlistedRepo is the refusal of a --repo naming a repository
// this machine's config does not list. It is the gate's own sentence rather than
// canonicalRepoArg's, whose advice (write the value as provider/owner/name) is
// one this command cannot take: settingsRepoFile accepts owner/name alone.
func settingsAccessUnlistedRepo(repo string) error {
	return userErr("nothing was written: %s is not a repository this machine's config lists, and a --layer repo write is judged in the repository whose file it lands in: run it from that repository's checkout, where the origin remote names it, or add the repository to this machine's config (fugaro update --repo in its checkout)", safetext.Field(repo))
}

// settingsAccessInput is the resolver's own input, and whether this
// installation has any roles to judge the caller against at all.
//
// The roles are the grants node where this CLI can read one (D1585), and
// otherwise the two IAM lists of this machine's own config, which are the whole
// of the roles an installation with no Firebase backend has (accessNoBackend).
//
// A database it cannot read is NOT an error here, and that is deliberate: the
// file a settings write edits is this machine's own, so the one thing that must
// not decide the write is a network read of it. An unreachable database, one
// that is another project's, one whose read fails — each leaves this CLI with
// no node to judge against, which is the state settingsAccessNode reports, and
// the write is then judged the way an installation with no node is judged: by
// the lists this machine's own config carries, or not at all (SD18's boundary
// is IAM at publish, and the person at the terminal can edit these files with
// an editor). A local error stays an error: the lists are read from a file this
// CLI can read, and a malformed address in one is worth telling the caller
// about rather than swallowing.
func settingsAccessInput(ctx context.Context, lc *localcfg.Config) (accesslib.Input, bool, error) {
	in := accesslib.Input{Implicit: userImplicitAdmins(lc), AllowRepos: settingsAccessEveryRepo}
	if lc.Budget != nil && lc.Budget.RTDBURL != "" {
		if node, ok := settingsAccessNode(ctx, lc); ok {
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

// settingsAccessNode is the grants node where this CLI can read one: the
// database this machine's config names, proven to be this project's and
// reachable, and holding the node. Everything else — no budget backend, no
// credentials for it, an outage, another project's database, no node — is the
// same answer, "no roles here", because the only thing this gate may do with a
// database it cannot read is fall back to what it can read locally, and never
// refuse a file edit for it.
func settingsAccessNode(ctx context.Context, lc *localcfg.Config) (accesslib.Node, bool) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return accesslib.Node{}, false
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return accesslib.Node{}, false
	}
	node, err := accesslib.Read(ctx, db)
	if err != nil || !node.Found {
		return accesslib.Node{}, false
	}
	return node, true
}

// settingsAccessUnreadable refuses a write by a credential whose caller the CLI
// cannot read. It is not a role refusal — there is no role to name, because the
// caller is unknown — so it says which question could not be asked and what
// answers it, rather than naming an admin the caller might go and find. It is
// worded as `fugaro user grant` words the same refusal (user_grant.go): one
// fact, one sentence, and the same remedy.
func settingsAccessUnreadable(what string, err error) error {
	return userErr("nothing was written: %s, and the credential this command would use is one whose caller could not be read: %v. The caller is the credential and never the local config's `user:` key or git config user.email (finding F21): sign in with gcloud auth application-default login and run this again, and ask a project admin to make the write if it is not yours to make", what, err)
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
