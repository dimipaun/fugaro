package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts rm`: the inverse of `accounts add`, in the
// order that cannot strand a running job (spec §4). A profile's
// claude_accounts is a job the installation has deployed, so the account is
// taken out of the profile first and removed only once no profile lists it —
// the state ValidateProfileClaudeAccounts refuses at publish, and therefore the
// one state this command must never write. Then the declaration goes, the
// secret goes through the store's own Delete, and the same stages as add are
// converged, so the job the account owned is gone from the installation.
//
// The delete is the one step here no other command reaches (spec §4 step 2),
// and it is the step the whole file is written around: the secret's ID is named
// in the sentence the run prints before it goes, the command says plainly that deleting a
// secret does not revoke the token at the provider (D866), and a delete that
// fails puts the local config back behind a compare-and-swap rather than
// leaving a file whose declaration is gone while the credential is still there.
//
// Both worlds judge the same thing before anything moves (spec §4 step 1, and
// the managed plan's accounts handoff row): a profile that still lists the
// account refuses the removal, and so does a repository whose COMMITTED
// fugaro.yaml names it in agent.oauth_token — read out of the commit the next
// run of that repository resolves its account from, never out of the working
// tree, which a coding agent's session owns and may have edited without
// committing. What differs is who acts on the answer. A person's own run warns
// about the repository and goes on — that file is a repository's and not this
// installation's, so their edit may not be blocked by one this command cannot
// write (spec §4 step 2, D783's shape) — while a coding agent's session is
// refused outright, whatever the walk found: the refs and the records that check
// reads are the session's own, so a clean read is its own word for what origin
// carries (D1172, managed spec §4.1), and the way out is the same removal in the
// person's own terminal window, where it prints every reference it finds before
// the removal below it goes ahead.
//
// The local config half is internal/yamledit's edit and the settings commands'
// shared write path, as add's is (D847); the converge is add's own runner,
// so the two commands invoke the same registered stages rather than a copy.

// accountsRmOptions are the flags `accounts rm` takes. There is no --json and
// no flag that carries a value: the whole output is the diff and the block a
// person reads (spec §4).
type accountsRmOptions struct {
	profile string
	yes     bool
}

func newAccountsRmCmd() *cobra.Command {
	var o accountsRmOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a named Claude account, its secret and the job that served it",
		Long: `Remove a named Claude OAuth account from this project and converge the
installation on its absence.

The account is removed in the order that cannot strand a running job. While a
profile still lists it, the command refuses — naming each profile — because a
profile's claude_accounts is a job this installation has deployed, and removing
the account under a live job leaves a job the policy cannot describe:
fugaro accounts rm NAME --profile PROFILE takes the name out of that profile's
list alone, and this command removes the declaration, the secret and the job
once no profile lists it.

If a repository's committed fugaro.yaml still names the account in
agent.oauth_token, the command warns instead, naming the repository and the run
that stops at launch: that file belongs to the repository and not to this
installation, so your own edit is not blocked by one this command cannot write.

Removing the declaration also deletes the secret claude-oauth-token-<name>
through Secret Manager's own delete. That delete cannot be undone — Secret
Manager keeps no grace period for a secret — and the copy of the credential
left behind is the token at the provider. The secret's ID is named in the
sentence this run prints before it goes, and the command says plainly that deleting the
secret does not revoke the token at the provider: it stays valid until it
expires or you revoke it in Anthropic's own console, which no command here
reaches.

Then it converges, in the same command and after that one sentence: the
policy node the account was published in, the job set and this checkout's
repository root, through the stages fugaro update runs.

If the delete fails after the config was written, the config is put back the
way it was and the command stops: an account whose declaration is gone while
its secret is still there is the one state this command may not leave behind.

Run it at your own terminal: this command deletes a credential, so a coding
agent's session is refused, --yes included — a managed project's gate refuses
the removal too, because the refs and the checkouts record its referrer check
reads are documents the session writes, so no read here answers for what origin
carries.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsRm(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.profile, "profile", "", "the profile whose claude_accounts loses the name (a profile that lists accounts is taken out of it one at a time)")
	f.BoolVar(&o.yes, "yes", false, "confirm the removal without a prompt")
	return cmd
}

// accountsRmPlan is what one run removes, resolved from the config before
// anything is written: the profile whose list loses the name, whether the
// declaration and the secret go with it, and the profiles that still list the
// account afterwards. Resolving it first is what lets every refusal fire before
// the file is touched, and what lets the sentence this run prints name the
// secret's ID.
type accountsRmPlan struct {
	name     string
	profile  string
	entry    bool // the profile's list loses the name
	drop     bool // the declaration, its secret and its job go too
	allow    []string
	secretID string
	rest     []string // the profiles that still list the account after the edit
}

// runAccountsRm is the order spec §4 forces: everything that can refuse does,
// before the file is written and before anything is deleted.
func runAccountsRm(cmd *cobra.Command, name string, o accountsRmOptions) error {
	// A name here is a REFERENCE and never a definition: what is removed is a
	// key of this file's claude_accounts block, and the rule for a reference is
	// ValidClaudeAccountRef's — the one the loader applies to a profile's list.
	// It is not ValidateClaudeAccounts' because "default" is a legal thing for
	// a profile to list and an illegal thing to define, and a name that is not
	// there is refused below rather than by the definition's rule.
	if msg := config.ValidClaudeAccountRef(name); msg != "" {
		return userErr("%s", msg)
	}

	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	plan, next, after, err := accountsRmResolve(sel.Path, lc, name, o.profile, old)
	if err != nil {
		return err
	}
	command := accountsRmCommand(plan)

	// The repositories this project has enrolled, read before the gate and
	// before the sentence this run prints: one whose committed fugaro.yaml names
	// the account is a reference this command cannot edit, and it is what the
	// gate refuses a session on and what that sentence warns a person about
	// (spec §4 step 2,
	// managed spec §4.2). The marker is read once and decides both what the walk
	// answers about a checkout this machine read here (accountsRmRepos) and the
	// gate's own call below.
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	read := accountsRmRepos(cmd.Context(), &cloudEnv{lc: lc}, plan.name, marker != "")

	// The managed gate, before anything is screened and written
	// (accounts_managed.go, add's own file, and the helper below): in a managed
	// project's coding agent's session it refuses this removal outright, because
	// every document its referrer check would read is one the session writes
	// (accountsRmRules's session rule) — before the diff is shown, so a refusal
	// leaves the file and the secret exactly as they were. Everywhere else it
	// answers the zero Managed, which is the fence below unchanged: the refusal
	// this command makes itself.
	m, gate, err := accountsRmGate(cmd.Context(), cmd, lc, plan, next, read)
	if err != nil {
		return err
	}

	// The refusal a session with no admission keeps, in this command's own
	// words. It comes before the diff rather than after it, and --yes does not
	// answer it.
	if marker != "" && !initflow.CanConfirmManaged(initflow.YesOrTyped, initflow.Conditions{Agent: marker}, m) {
		return accountsRmSession(marker, plan, command)
	}

	// The one reference this command may not answer for, before the write and
	// not after it: what it warns about is a break the removal makes, and it is
	// said in the same breath as the sentence that names what goes, since
	// nothing here waits for an answer to either (D1540).
	accountsRmWarn(cmd, plan, read)

	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    accountsRmWhat(plan),
		Kind:    settingsDocLocal,
		Managed: m,
		Command: command,
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	// The record of the admission, written after the write it was admitted for
	// and never before it (D1014, spec §6): a step the gate admitted that never
	// reached its write leaves no record at all.
	if yamledit.Changed(old, next) {
		if err := gate.recorded(cmd.Context()); err != nil {
			return err
		}
	}

	// The secret, which is the step the sentence above named the ID for.
	// It runs before the converge and after the write, in spec §4's own order
	// (step 2 removes, step 3 converges): a run launched in the window between
	// them finds the published policy still naming an account whose secret is
	// gone, which is a failed run, while the other order would leave a deleted
	// job's credential live for the length of the converge.
	secret := ""
	if plan.drop {
		if secret, err = accountsRmDelete(cmd, lc, sel.Path, next, old, plan); err != nil {
			return err
		}
	}
	return accountsRmConverge(cmd.Context(), cmd, after, next, sel.Path, plan, secret)
}

// accountsRmResolve reads the config as it is and answers what this run
// removes, or refuses. Nothing is written here, and the two refusals that
// matter are spec §4 step 1's: a name a profile still lists is not removed,
// because a profile's claude_accounts is a job this installation has deployed
// and the state such a removal would write — a profile serving an account the
// config does not define — is the one ValidateProfileClaudeAccounts refuses.
func accountsRmResolve(path string, lc *localcfg.Config, name, want string, old []byte) (accountsRmPlan, []byte, *localcfg.Config, error) {
	if _, ok := lc.ClaudeAccounts[name]; !ok {
		return accountsRmPlan{}, nil, nil, userErr("claude_accounts.%s is not declared in %s, so there is nothing to remove; `fugaro accounts ls` lists the accounts this project's repositories may use", pluginwire.Printable(name), path)
	}
	if want != "" {
		// --profile means what it means in add, and a profile this project does
		// not define is the same refusal (accountsAddProfile): the two commands
		// are one pair, and the same flag may not answer two ways.
		if _, err := accountsAddProfile(want, lc); err != nil {
			return accountsRmPlan{}, nil, nil, err
		}
	}
	serving := accountsRmServing(lc, name)
	switch {
	case want != "" && !slices.Contains(serving, want):
		return accountsRmPlan{}, nil, nil, accountsRmNoEntry(name, want, serving)
	case want == "" && len(serving) > 0:
		return accountsRmPlan{}, nil, nil, accountsRmServed(name, serving)
	}

	plan := accountsRmPlan{
		name:     name,
		profile:  want,
		entry:    want != "",
		allow:    slices.Clone(lc.ClaudeAccounts[name].AllowDataTo),
		secretID: gcp.ProjectSecretID(lc.Name, config.ClaudeAccountSecret(name)),
	}
	// The declaration goes when nothing lists it any more: after the entry
	// above, or at once for an account no profile ever listed.
	plan.drop = !plan.entry || len(serving) == 1
	for _, p := range serving {
		if p != plan.profile {
			plan.rest = append(plan.rest, p)
		}
	}

	next := old
	if plan.entry {
		n, err := yamledit.RemoveListItemWithOptions(next, []string{"profiles", plan.profile, "claude_accounts"}, name, yamledit.RemoveOptions{DropEmptyParents: true})
		if err != nil {
			return accountsRmPlan{}, nil, nil, userErr("profiles.%s.claude_accounts: %v", pluginwire.Printable(plan.profile), err)
		}
		next = n
	}
	if plan.drop {
		n, err := yamledit.RemoveKeyWithOptions(next, []string{"claude_accounts", name}, yamledit.RemoveOptions{DropEmptyParents: true})
		if err != nil {
			return accountsRmPlan{}, nil, nil, userErr("claude_accounts.%s: %v", pluginwire.Printable(name), err)
		}
		next = n
	}
	// The edit is read back strictly before it is written, as add's is: a file
	// this Fugaro cannot load is a file the owner's very next command refuses,
	// and this command is the one that can leave one — a declaration removed
	// under a profile that still lists it is exactly that file.
	after, err := localcfg.Parse(next)
	if err != nil {
		return accountsRmPlan{}, nil, nil, userErr("the local config this would write does not read back: %v", err)
	}
	return plan, next, after, nil
}

// accountsRmServing is the profiles that list the account, in the order every
// refusal and every line of the block names them.
func accountsRmServing(lc *localcfg.Config, name string) []string {
	var out []string
	for _, p := range slices.Sorted(maps.Keys(lc.Profiles)) {
		if slices.Contains(lc.Profiles[p].ClaudeAccounts, name) {
			out = append(out, p)
		}
	}
	return out
}

// accountsRmReferrers is what that walk answered, bucketed by the kind of
// answer: the committed files that name the account, the repositories this
// machine holds no checkout of, the checkouts whose committed read did not
// answer at all, the ones it answered cleanly and which are no evidence about
// origin in a session, and — the document every row of the walk comes from —
// this machine's record of its own checkouts, when that cannot be read.
type accountsRmReferrers struct {
	referrers  []string // a committed fugaro.yaml names the account in agent.oauth_token
	unseen     []string // no checkout here: the question was never asked (D783's warning)
	unresolved []string // read here, and not evidence about origin in a session
	unread     []string // the committed document, or the ref it lives at, could not be read
	state      string   // the record of this machine's checkouts could not be read
	// session is whether the caller is a coding agent's session. Every bucket
	// above is read the same way either way — the owner's warning is worth
	// having in a session too — and the rules answer the buckets that are not
	// the referrer itself differently on the two paths: a person's own run is
	// the repo's own checkout's word, and a session's is not (accountsRmRules).
	session bool
}

// accountsRmRepos is the repository half of the reference check (spec §4 step 2,
// managed spec §4.2): the repositories this project has enrolled whose committed
// fugaro.yaml names the account in agent.oauth_token, the ones this machine has
// no checkout of, and the checkouts whose own fugaro.yaml this machine could not
// resolve — a document the strict reader refuses, no document at all, or a base
// branch the policy node did not answer. It is the same walk `fugaro models rm`
// makes over the same set (modelsModelPinned), and every read in it is local
// except one: the project's own base branch, which is a launch's layer 3 and is
// read from the policy node only when neither layer above it names one
// (accountsRmRefs, basebranch.go's projectBaseBranchErr).
//
// The document judged is the COMMITTED one and never the working tree, because
// the committed file is what the next run of the repository resolves its account
// from (project_job.go's agentAtRef: "never the network and never the working
// tree"). A checkout whose working tree has the line removed while the branch it
// was made from still names the account is exactly the state this check has to
// see: a coding agent's session owns that working tree, and its edits are
// uncommitted until someone pushes them. No ref a session's own file picks is
// read either — accountsRmCommittedYAML says why — and a name only the working
// tree carries is therefore not a referrer this command reports: the committed
// read is what fails, and its failure is the unknown this check refuses a
// session on and warns the owner about. The conservative direction is the only
// one available here: a document this machine could not read is never scored
// clean, and every ref the runs of the repository can be cut from
// (accountsRmRefs) is read rather than one of them, with the base branch each
// of those documents names read beside it (accountsRmCommittedYAML).
//
// unseen, unread and unresolved are three of the ways this machine can come up
// short, and they are not the same: no checkout here — no row at all, or a row
// whose directory is gone — means the question was never asked, which is a
// warning (refusing on it would refuse every machine that does not hold every
// checkout of the project); a checkout that IS there and whose own file does not
// resolve, or whose base branch could not be resolved to read it at, is a value
// the gate cannot read at plan time, which is a refusal and never a pass
// (managed spec §4.1); and a checkout whose committed file WAS read here is not
// that value either in a coding agent's session, which is what session below
// decides. state is the fourth, and it is not a repository at all: the document
// that says which checkouts this machine holds could not be read, so not one row
// of the walk below could be established.
//
// session is whether the caller is a coding agent's session (the marker
// agentMarker reads). It decides one thing and it is the whole of the difference
// a session makes to the walk: a repository whose committed file this machine
// read out of its own checkout is unresolved there, because the ref that read
// resolves is a ref the checkout holds — refs/remotes/origin/<ref> and the
// branch of the same name (project_job.go's checkoutFugaroYAMLAtRef) — and a
// session owns that checkout and can point such a ref at a commit that hides the
// name, which makes a clean local read indistinguishable from a forged one.
// accountsRmRules carries the refusal, and the owner's own run keeps the warning
// and the write: it is the owner's own checkout, and a checkout's
// committed file is what the runs of the repository resolve their account from.
//
// The file is read through config.AgentOf and not through a resolved config:
// agent.oauth_token is repository-scope, so the file's own bytes are the whole
// answer, and a repository whose file leans on the project layer for something
// else is one this command can still read.
func accountsRmRepos(ctx context.Context, env *cloudEnv, name string, session bool) accountsRmReferrers {
	read := accountsRmReferrers{session: session}
	lc := env.lc
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		// The record of this machine's own checkouts is there and wrong, which
		// LoadCheckouts refuses to hand back as "nothing recorded" — and taking
		// it for an empty one is the whole harm: every enrolled repository
		// becomes one this machine does not hold, the warning, and the question
		// of what any of them names is never asked, so the gate finds no
		// referrer and no unread checkout and admits a session to the one-way
		// delete (the same error is a hard refusal in settings_set.go's read of
		// the same file, and checkouts.go says why a corrupt document is not an
		// empty one). Nothing was read here, so nothing is scored clean: the
		// document is the unknown, and an unknown is the refusal a session gets
		// and the warning the owner reads.
		return accountsRmReferrers{state: err.Error()}
	}
	remembered := map[string]string{}
	for _, c := range checkouts.ProjectCheckouts(lc.Name) {
		remembered[localcfg.CheckoutIdentity{Provider: c.Provider, Repo: c.Repo}.Key()] = checkoutPath(c)
	}
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		dir := remembered[checkoutKeyFor(repo, remembered)]
		if dir == "" {
			read.unseen = append(read.unseen, repo)
			continue
		}
		// A row whose directory is not there any more is the same answer as a
		// repository this machine never checked out (config_list.go's `missing`):
		// there is no checkout here to read, so the question of what it names was
		// never asked — which is the warning below and not the refusal an
		// unreadable DOCUMENT gets.
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			read.unseen = append(read.unseen, repo)
			continue
		}
		refs, err := accountsRmRefs(ctx, env, repo)
		if err != nil {
			// The base branch this repository's runs are cut from could not be
			// resolved (basebranch.go's projectBaseBranchErr), so the refs to read
			// the committed file at are unknown and no read below happened. That is
			// the same unknown an unreadable document is — the file that answers was
			// not reached either way — and never the default: a policy node main
			// stands in for would score a repository clean at a branch no run of it
			// uses, which is the value this check may not resolve its way past.
			read.unread = append(read.unread, repo)
			continue
		}
		// Every ref a run of this repository can be cut from is read, and with each
		// of those documents the branch it names as its base — beside it and never
		// in its place, because a run reads its account out of the file at the ref
		// it is cut from (accountsRmCommittedYAML). The answer is the referrer when
		// ANY of them names the account, before the read that did not answer: a name
		// at a ref the runs use is a fact, while an unread document beside it is the
		// absence of one, and a repository that is both is reported as what it is —
		// a referrer, and therefore a refusal for a session and a warning for the
		// owner either way.
		var named, failed bool
		seen := map[string]bool{}
		queue := slices.Clone(refs)
		for len(queue) > 0 {
			ref := queue[0]
			queue = queue[1:]
			if seen[ref] {
				continue
			}
			seen[ref] = true
			data, base, err := accountsRmCommittedYAML(ctx, dir, ref)
			if err != nil {
				failed = true
				continue
			}
			agent, ok := config.AgentOf(data)
			if !ok {
				failed = true
				continue
			}
			if agent.OAuthToken == name {
				named = true
			}
			if base != "" && !seen[base] {
				queue = append(queue, base)
			}
		}
		switch {
		case named:
			read.referrers = append(read.referrers, repo)
		case failed:
			read.unread = append(read.unread, repo)
		case session:
			read.unresolved = append(read.unresolved, repo)
		}
	}
	return read
}

// accountsRmRefs is the refs a run of this repository can be cut from, as this
// machine reads them: the base branch the launch's own layers below the file
// name, and — beside it, never instead of it — the ref a launch from this
// working directory is cut from (newRunRef's answer, which is the branch this
// checkout is on when origin has it and it is not that base).
//
// The base is layersBaseBranch's answer and not a second resolution of the same
// config: that function is the launch's own for these layers, so the local
// config's repos.<r>.base_branch, the installation config's own base_branch and
// the base_branch the published policy node carries are all read here exactly as
// a launch reads them. Resolving the first two by hand is what this check did
// before, and it read main on an installation whose default lives only in the
// policy node — a repository naming the account at the policy's branch was
// scored clean, the gate admitted a session's removal and the one-way delete
// ran while every run of it stopped at launch. What is deliberately NOT read is
// the one layer above these: the working directory's own file (basebranch.go's
// layer 1), which belongs to the session that owns the tree and can write a
// git.base_branch into it uncommitted — accountsRmCommittedYAML says why a ref
// that file picks is not one this check resolves.
//
// The base branch alone is not the question this check asks. A run with no
// --ref launched from a checkout of a pushed branch other than the base is cut
// from THAT branch (run_ref.go), and it is the common case: the person standing
// in the branch the account's removal is about. A checkout whose base branch
// file is clean while the branch it is on names the account is therefore a
// repository every run launched from here stops on, and judging only the base
// would score it clean, admit a session's removal and run the one-way delete
// while those runs keep stopping at launch.
//
// The ref a launch would use is taken from newRunRef itself rather than
// re-derived, so this check cannot drift from the launch it is judging; its
// warning line is a launch's and is dropped here.
//
// The base comes from layersBaseBranchErr and not from layersBaseBranch: the
// read that resolves the project's layer can fail, and the launcher's own
// function folds that failure into main (basebranch.go's projectBaseBranchErr
// says why a launch may). A base this check could not read is not main and is
// not any other branch either — it is the ref the file was never read at, and
// an error here scores the repository unread rather than clean (accountsRmRepos).
func accountsRmRefs(ctx context.Context, env *cloudEnv, repo string) ([]string, error) {
	base, err := env.layersBaseBranchErr(ctx, repo)
	if err != nil {
		return nil, err
	}
	refs := []string{base}
	if launched := newRunRef(ctx, repo, base, io.Discard); launched != base {
		refs = append(refs, launched)
	}
	return refs, nil
}

// accountsRmCommittedYAML reads a repository's committed fugaro.yaml at one ref
// the way a run of it reads that file: the blob at ref, out of the checkout's
// object database, through the reader the launch uses (readFugaroYAMLAtRef,
// which is git cat-file). An error is "this machine could not read the document
// that answers", which is unknown and not clean. Beside the document it answers
// the branch that document itself names as its base (config.GitBaseBranchOf),
// which accountsRmRepos reads as a ref of its own and never in the ref's place.
//
// ref comes from accountsRmRefs and NEVER from the checkout's file. A launch
// consults that file first (basebranch.go's layer 1), but it belongs to the
// checkout a session owns: a ref it names — one this checkout may not hold at
// all — fails the read, so a ref the file picks is a ref the same uncommitted
// edit that hides the account can pick, and the read that then falls back to a
// working tree without the name scores a referrer as clean. What the file may
// not pick is the ref; the branch this checkout is ON is another matter and is
// judged (accountsRmRefs), because that is the ref a launch from here is cut
// from whether or not any file says so.
//
// The base branch is read BESIDE the ref and never in its place. A run cut from
// ref reads its account out of the file at ref itself (project_job.go's
// agentAtRef and projectJobAgentAuth: spec.Ref is the ref the job clones and
// resolves, and the base a file names is what the run measures its work against
// — a different branch, not a different document for the credential), so a file
// at a ref a run uses that names the account is a referrer even when the branch
// it names is clean. Reading that branch in the file's place is what scored
// exactly that repository clean. The named branch is a ref in its own right: a
// run with an explicit --ref names it, and there it reads its account out of
// that branch's file, so that file is judged too — and a ref this checkout does
// not hold is an error rather than an older copy of the file.
func accountsRmCommittedYAML(ctx context.Context, dir, ref string) ([]byte, string, error) {
	data, err := readFugaroYAMLAtRef(ctx, dir, ref)
	if err != nil {
		return nil, "", err
	}
	base, ok := config.GitBaseBranchOf(data)
	if !ok || base == ref {
		return data, "", nil
	}
	return data, base, nil
}

// accountsRmWarn is the whole of the protection spec §4 step 2 leaves for a
// reference this command cannot edit (D783's shape, as `fugaro models rm` warns
// about a pin): it names every repository this machine can see whose committed
// fugaro.yaml names the account, what that repository's next run does once the
// declaration is gone, and the repositories whose state it could not read at
// all — no checkout here, a checkout whose own file did not resolve, and the
// record of the checkouts itself when that document could not be read, which is
// every repository at once. It is a warning and never a block — that file is a
// repository's and not this installation's, so the owner's own edit may not be
// stopped by one this command cannot write — and it is printed before the write
// below, because the break it describes is one the removal makes.
//
// The session-only bucket is not read here: a session holding one was refused
// by the gate before this point, and the owner's own run never fills it.
func accountsRmWarn(cmd *cobra.Command, plan accountsRmPlan, read accountsRmReferrers) {
	if !plan.drop {
		return
	}
	referrers, unseen, unread, state := read.referrers, read.unseen, read.unread, read.state
	out := cmd.OutOrStdout()
	if len(referrers) > 0 {
		fmt.Fprintf(out, "warning: %s is named by %s's committed fugaro.yaml in agent.oauth_token\n",
			pluginwire.Printable(plan.name), strings.Join(referrers, ", "))
		fmt.Fprintf(out, "warning: the next run of each stops at launch: named Claude account %s is not one it may use once the declaration is gone. Take the name out of that file, or put the account back with `fugaro accounts add %s`\n",
			pluginwire.Printable(plan.name), quoteWord(plan.name))
	}
	if len(unseen) > 0 {
		fmt.Fprintf(out, "warning: this machine has no checkout of %s, so a reference there is unknown: run `fugaro accounts ls` in that checkout to see it\n",
			strings.Join(unseen, ", "))
	}
	if len(unread) > 0 {
		fmt.Fprintf(out, "warning: this machine could not read the committed fugaro.yaml of %s, so a reference there is unknown: run `fugaro accounts ls` in that checkout to see it\n",
			strings.Join(unread, ", "))
	}
	if state != "" {
		fmt.Fprintf(out, "warning: this machine's record of its own checkouts could not be read (%s), so no repository's committed fugaro.yaml was checked for %s and a reference in any of them is unknown: run `fugaro accounts ls` in the checkout of each to see it\n",
			state, pluginwire.Printable(plan.name))
	}
}

// accountsRmServed is spec §4 step 1's refusal, and it is the reason this
// command is not one edit: a profile's claude_accounts names a job the
// installation has deployed, so the account is taken out of the profile first,
// with the same command and the same flag add takes.
func accountsRmServed(name string, serving []string) error {
	return userErr("claude_accounts.%s is still served by %s: a profile's claude_accounts names a job this installation has deployed, and removing the account under a live job leaves a job the policy cannot describe, which a publish refuses. Take the name out of the profile first — %s — and this command removes the declaration, the secret and the job once no profile lists it; nothing was written",
		pluginwire.Printable(name), accountsAddProfiles(serving), accountsRmProfileCommand(name, serving[0]))
}

// accountsRmNoEntry is the --profile whose profile does not list the account:
// naming a profile is naming the entry to remove, and a flag answered with a
// removal nothing asked for would be this command guessing which profile the
// person meant.
func accountsRmNoEntry(name, want string, serving []string) error {
	if len(serving) == 0 {
		return userErr("--profile %s: no profile of this project lists %s, so there is no entry to remove; run `fugaro accounts rm %s`, which removes the declaration, the secret and the job the account owned — nothing was written",
			pluginwire.Printable(want), pluginwire.Printable(name), quoteWord(name))
	}
	return userErr("--profile %s: profiles.%s.claude_accounts does not list %s, and the flag takes the name out of the profile it names alone; this project's profiles that list it are %s — nothing was written",
		pluginwire.Printable(want), pluginwire.Printable(want), pluginwire.Printable(name), accountsAddProfiles(serving))
}

// accountsRmSession is this command's own refusal in a coding agent's session
// (spec §4's last line, D1181's rule): what a session reads when no gate spoke
// at all — a project this program could not read as managed, or a mark read that
// did not answer, the two cases a mark that cannot be read is not a permission
// for. A managed project's gate refuses a session's `accounts rm` as a class
// (accountsRmRules's session rule: the refs and the records its check reads are
// the session's own), and that refusal reaches the session in the gate's own
// words instead of this one (accountsRmGate).
func accountsRmSession(marker string, plan accountsRmPlan, command string) error {
	return userErr("refusing to remove %s in a coding agent's session (%s is set): this command deletes a credential, and no read a session makes is evidence about origin — the refs the referrer check resolves, this machine's record of its checkouts and the installation's own list of enrolled repositories are documents the session writes, so a clean read is the session's own word for what origin carries — nothing was read off this session, nothing was written and the secret %s is still there; run this yourself at your own terminal: %s",
		pluginwire.Printable(plan.name), marker, plan.secretID, command)
}

// accountsRmGate is the whole of this command's call of the gate, add's shape
// (accounts_managed.go): read, judge, admit. It answers the value the write
// below is decided under (the zero value is "not managed", which keeps today's
// refusal), the gate itself — whose recorded call the caller makes after its
// write — and the gate's own refusal, in the gate's own words, when the gate
// refused the document.
//
// doc is the file as this command would write it, and it is the same bytes the
// rules were read from and the same bytes the caller applies.
func accountsRmGate(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, plan accountsRmPlan, doc []byte, read accountsRmReferrers) (initflow.Managed, managedGate, error) {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if marker == "" {
		return initflow.Managed{}, managedGate{}, nil
	}
	clients, err := newInitClients(ctx, lc)
	if err != nil {
		return initflow.Managed{}, managedGate{}, nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return initflow.Managed{}, managedGate{}, nil
	}
	gate, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: clients.Storage, crm: clients.CRM, db: db}, managedGateFacts{
		Subcommand: "accounts rm",
		Argv:       os.Args[1:],
		// What this command would have written, for the ownership question
		// D1017 asks with the database it is about to write.
		Declined:    "nothing was removed",
		AgentMarker: marker,
		// The whole-project inventory (D59) is `fugaro managed apply`'s read and
		// is not consulted on this path: a command that has none passes false.
		InventoryFallback: false,
	})
	if err != nil {
		// No installation to read, a mark that did not answer, or the ownership
		// question's own refusal: the project is not one this program can prove
		// is managed, so the session keeps this command's refusal and nothing is
		// recorded.
		return initflow.Managed{}, managedGate{}, nil
	}
	applied, refused := accountsRmRules(plan, read)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, applied)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	// YesOrTyped is the class this step is: the ordinary edit the diff
	// describes, and not a Typed step — the value D846 reserves for a loosened
	// ceiling is what the declaration's own rules already refuse here.
	m, err := gate.admit(ctx, initflow.YesOrTyped, conv)
	if err == nil {
		return m, gate, nil
	}
	if errors.Is(err, managedmode.ErrNotConverged) {
		return m, gate, &ExitError{Code: ExitUserError, Err: err}
	}
	// The second read did not answer, immediately before the write: a mark that
	// cannot be read is not a permission.
	return initflow.Managed{}, managedGate{}, nil
}

// accountsRmRules are the rules the gate judges this removal by, in the words
// the record and the refusal carry: the rules that were applied and passed, and
// the ones that refused. They are this command's own and live here rather than
// in internal/managed, where the SHAPE of a verdict and the one class rule are
// (D1021).
//
// The rule is the design's whole content for this command (managed spec §4.2,
// D1172, D1180's row): a removal is admitted only when no profile names the
// account and no repository this machine can read names it in its committed
// agent.oauth_token — and never from a coding agent's session, whatever those
// reads say, for the reason the last rule below gives. Each refusal names the
// referrer it found rather than the key it would remove, and the ways the read
// can come up short are answered differently on purpose — all of them on the
// owner's path, where the buckets and the rules below are read as written.
//
// A repository this machine holds no checkout of is a warning (accountsRmWarn)
// and not one of these refusals: refusing on it would refuse every project whose
// checkouts are not all on this machine, and what it would prevent is a run that
// stops at launch, which is a break the owner reads about rather than a
// permission. A checkout whose own committed fugaro.yaml does not resolve is a
// refusal, because there the question WAS asked and the answer could not be read
// — "a value the gate cannot resolve at plan time" is "a refusal, never a pass"
// (managed spec §4.1) — and a document that was not read is never scored clean,
// so admitting on it would take the one-way delete while the repository may
// still name the account. state is that same refusal for the third case: the
// record of this machine's own checkouts could not be read, so no repository's
// question was asked at all — the widest form of the value this gate may not
// resolve, and the one that would otherwise leave a removal admitted with
// nothing read.
//
// unresolved is the fourth, and it is this command's answer to a read the
// session can write: the committed fugaro.yaml was read here and did not name
// the account, so the walk above scored it clean — but the refs that read
// resolved (refs/remotes/origin/<ref>, else the branch of the same name) live in
// the checkout a coding agent's session owns, and `git commit && git update-ref
// refs/remotes/origin/<ref> HEAD` moves one of them to a commit that hides the
// name while every run from origin still stops at launch. Nothing this command
// can read locally tells origin's copy from a session's forged one, so the value
// is unresolved, and an unresolved value is a refusal and never a pass (managed
// spec §4.1). The refusal names the repositories, so the owner's own run of the
// same removal — which reads the same files without this rule, since the checkout
// is theirs — is still what answers it.
//
// Every one of those answers the walk gives is a document the session can write,
// which is what the last rule says: a session's removal is refused whatever the
// buckets hold, because a rule per move is a rule per move that comes next — the
// update-ref of the refs, the checkout moved away, its row taken out of the
// record, the repository taken off the installation, and whichever of them no
// version of this file has named yet. All of them write the answer this check
// reads, so a check that writes its own answer is not a check (managed spec
// §4.1), and the refusal says which documents those are rather than which one
// was used. It is the only rule that fires for a session and no other path: the
// owner reads the same buckets as warnings, and the sentence the write prints.
func accountsRmRules(plan accountsRmPlan, read accountsRmReferrers) (applied, refused []string) {
	referrers, unread, state := read.referrers, read.unread, read.state
	if plan.drop {
		applied = append(applied, "no profile lists claude_accounts."+pluginwire.Printable(plan.name)+" once this step has run")
	} else {
		refused = append(refused, accountsAddProfiles(plan.rest)+" still lists claude_accounts."+pluginwire.Printable(plan.name)+" after this step, and a profile's claude_accounts names a job this installation has deployed: taking the account out from under a live job leaves a job the policy cannot describe, which only the owner can answer for (spec §4 step 1)")
	}
	if len(referrers) > 0 {
		refused = append(refused, "the committed fugaro.yaml of "+strings.Join(referrers, ", ")+" names "+pluginwire.Printable(plan.name)+" in agent.oauth_token, so the next run of each stops at launch: that file belongs to a repository this command cannot write, and repairing it is the owner's own edit (D783)")
	}
	if len(unread) > 0 {
		refused = append(refused, "this machine holds a checkout of "+strings.Join(unread, ", ")+" and could not read the committed fugaro.yaml its agent.oauth_token resolves from, so whether it names "+pluginwire.Printable(plan.name)+" is a value this step cannot resolve at plan time, and an unresolved value is a refusal and never a pass (managed spec §4.1)")
	}
	if len(read.unresolved) > 0 {
		refused = append(refused, "this machine holds a checkout of "+strings.Join(read.unresolved, ", ")+" and read the committed fugaro.yaml its agent.oauth_token resolves from, but the ref that read resolved is one this checkout holds (refs/remotes/origin/<ref>, else the branch of the same name) and a coding agent's session owns that checkout: `git commit && git update-ref refs/remotes/origin/<ref> HEAD` moves such a ref to a commit that does not name "+pluginwire.Printable(plan.name)+", so whether origin's copy still names it is a value this step cannot resolve at plan time, and an unresolved value is a refusal and never a pass (managed spec §4.1)")
	}
	if state != "" {
		refused = append(refused, "this machine's record of its own checkouts could not be read ("+state+"), so no repository's committed fugaro.yaml was checked for "+pluginwire.Printable(plan.name)+": whether any of them names it is a value this step cannot resolve at plan time, and an unresolved value is a refusal and never a pass (managed spec §4.1)")
	}
	// A session is refused whatever the walk above found, and this rule is the
	// class the three above are instances of: every document a referrer check
	// reads is one the session writes — the committed file through the refs the
	// session's own checkout holds (refs/remotes/origin/<ref>, else the branch
	// of the same name, project_job.go's checkoutFugaroYAMLAtRef), the record of
	// which checkouts this machine holds (a missing or emptied one is
	// EmptyCheckouts, not an error), and the installation's own list of enrolled
	// repositories, which is what decides which questions are asked at all. Each
	// move was reproduced before it was answered: `git update-ref` on the
	// remote-tracking ref, the checkout moved away or its row taken out, and the
	// repository taken off the installation. Closing them one at a time is what
	// this rule replaces — the next one would be the one the last fix did not
	// name — so a session's removal is refused as an unresolved value rather
	// than admitted on a check that writes its own answer, which is managed spec
	// §4.1's rule and not an exception to it. The way out — the same removal in
	// the owner's own terminal window — follows the class statement instead of
	// closing the rule, because the gate prints each rule through safetext.Field
	// (512 runes, elided): a session that reads its refusal reads the two things
	// it acts on, and the sentence the owner reads in full is the record.
	if read.session {
		refused = append(refused, "no read a coding agent's session makes is evidence about origin, so run `"+accountsRmCommand(plan)+"` in your own terminal window, where the same removal prints every reference it finds before it goes ahead: the committed fugaro.yaml it reads is the copy at refs/remotes/origin/<ref> (else the branch of the same name) in a checkout the session owns, the record of which checkouts this machine holds is a document the session writes, and so is the installation's own list of enrolled repositories — so whether a repository still names "+pluginwire.Printable(plan.name)+" is a value this step cannot resolve at plan time in a session, and an unresolved value is a refusal and never a pass (managed spec §4.1)")
	}
	if len(referrers) == 0 && len(unread) == 0 && len(read.unresolved) == 0 && state == "" && !read.session {
		applied = append(applied, "no repository this machine holds a checkout of names "+pluginwire.Printable(plan.name)+" in the committed agent.oauth_token of its fugaro.yaml")
	}
	return applied, refused
}

// accountsRmWhat is the sentence the run prints before it makes any of them
// (spec §4 step 2): every change
// this command makes — the profile entry, the declaration, the secret it
// deletes, the publish and the deploy behind them — and, because a deleted
// secret is the change the provider knows nothing about, the sentence that says
// what deleting it does NOT do (D866). The secret's ID is here for the same
// reason: it is named before it goes, so whoever reads the run knows which
// credential this is.
func accountsRmWhat(plan accountsRmPlan) string {
	parts := []string{}
	if plan.entry {
		parts = append(parts, fmt.Sprintf("remove %s from profiles.%s.claude_accounts",
			pluginwire.Printable(plan.name), pluginwire.Printable(plan.profile)))
	}
	if !plan.drop {
		parts = append(parts,
			fmt.Sprintf("keep claude_accounts.%s: %s still lists it, and its secret is not touched",
				pluginwire.Printable(plan.name), accountsAddProfiles(plan.rest)),
			fmt.Sprintf("publish and deploy, so the job %s served for %s is gone",
				pluginwire.Printable(plan.profile), pluginwire.Printable(plan.name)))
		return strings.Join(parts, "; ")
	}
	parts = append(parts, fmt.Sprintf("remove claude_accounts.%s (its allow_data_to was [%s])",
		pluginwire.Printable(plan.name), strings.Join(plan.allow, ", ")))
	parts = append(parts,
		fmt.Sprintf("delete the secret %s (Secret Manager's own delete, which cannot be undone here)", plan.secretID),
		fmt.Sprintf("deleting the secret does not revoke the token at the provider: %s's token stays valid until it expires or you revoke it in Anthropic's own console, which no command here reaches",
			pluginwire.Printable(plan.name)),
		"publish and deploy, so the job the account owned is gone")
	return strings.Join(parts, "; ")
}

// accountsRmCommand is the command a refusal in a coding agent's session prints
// for the person to run at their own terminal: this same removal, spelled
// whole, because nothing they type here is remembered.
func accountsRmCommand(plan accountsRmPlan) string {
	if plan.entry {
		return accountsRmProfileCommand(plan.name, plan.profile)
	}
	return "fugaro accounts rm " + quoteWord(plan.name)
}

func accountsRmProfileCommand(name, profile string) string {
	return "fugaro accounts rm " + quoteWord(name) + " --profile " + quoteWord(profile)
}

// accountsRmDelete deletes the account's secret through the store's own Delete
// — the first caller of it in this tree — and answers the line the block prints
// for the secret. A secret the store does not have is not a failure: a delete
// that 404s has reached the state this step exists for, and reporting it as an
// error would send the owner back to a declaration this command already
// removed.
//
// Every other failure leaves the config written and the credential still there,
// which is the state this file may not leave behind, so the file is put back
// first (accountsRmRestore) and the command stops before the converge.
func accountsRmDelete(cmd *cobra.Command, lc *localcfg.Config, path string, wrote, back []byte, plan accountsRmPlan) (string, error) {
	deleted, err := accountsRmDeleteSecret(cmd.Context(), lc, plan.secretID)
	switch {
	case err == nil && deleted:
		return "deleted: " + plan.secretID, nil
	case err == nil:
		return "already gone: " + plan.secretID, nil
	}
	if rerr := accountsRmRestore(cmd, path, wrote, back, plan.name); rerr != nil {
		return "", rerr
	}
	return "", remote(fmt.Errorf("deleting the secret %s: %w; %s was put back the way it was, so claude_accounts.%s is declared again and nothing was converged — run this again once the delete works",
		plan.secretID, err, path, pluginwire.Printable(plan.name)))
}

// accountsRmDeleteSecret makes the call. ok is false for a secret the store
// does not have.
func accountsRmDeleteSecret(ctx context.Context, lc *localcfg.Config, id string) (ok bool, err error) {
	// openCloudFor is what every cloud-writing command opens through, and here
	// it is also the fence a delete needs before it runs: it reads the project
	// marker out of the runs bucket and refuses a project whose marker names
	// another one, so a config pointed at the wrong installation cannot delete
	// a credential in it.
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return false, err
	}
	defer env.Close()
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return false, remote(err)
	}
	if err := sm.Delete(ctx, id); err != nil {
		if accountsRmAbsent(err) {
			return false, nil
		}
		return false, remote(err)
	}
	return true, nil
}

// accountsRmAbsent reports whether a refused delete is Secret Manager saying
// the secret is not there: the one failure whose goal state is already reached.
func accountsRmAbsent(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusNotFound
}

// accountsRmRestore puts the local config back the way this command found it,
// after a step that failed behind the write. It is the same compare-and-swap
// the write path itself makes, through the same reader
// (readSettingsCurrent, which is the reader the write used): the file is put
// back only while it still holds what this command wrote, because a file that
// moved belongs to a later edit and writing the older bytes over it would
// discard that edit. That case, and a file the restore cannot read or write,
// are reported with what the file is left holding — the account declared
// nowhere while its secret is still there — and never smoothed over.
func accountsRmRestore(cmd *cobra.Command, path string, wrote, back []byte, name string) error {
	current, err := readSettingsCurrent(settingsEdit{Path: path, Kind: settingsDocLocal})
	switch {
	case err != nil:
		return remote(fmt.Errorf("%s was not read back to put it back: %v — claude_accounts.%s is gone from it and the secret is still there: put the entry back by hand before anything else runs", path, err, pluginwire.Printable(name)))
	case !bytes.Equal(current, wrote):
		return remote(fmt.Errorf("%s changed while this ran, so it was not put back: it does not declare claude_accounts.%s and the secret is still there — read it and put back what belongs there by hand before anything else runs", path, pluginwire.Printable(name)))
	}
	if err := writeFileAtomic(path, back); err != nil {
		return remote(fmt.Errorf("putting %s back failed: %v — claude_accounts.%s is gone from it and the secret is still there: put the entry back by hand before anything else runs", path, err, pluginwire.Printable(name)))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "put %s back the way it was: claude_accounts.%s is declared again and nothing was converged\n",
		path, pluginwire.Printable(name))
	return nil
}

// accountsRmConverge is spec §4 step 3: the same publish and deploy as add,
// through add's own runner over the registered stages, so the job the account
// owned is gone from the installation and the node no longer names it.
func accountsRmConverge(ctx context.Context, cmd *cobra.Command, after *localcfg.Config, wrote []byte, path string, plan accountsRmPlan, secret string) error {
	// The removal's own account (CL7a), filed under the removal's own name: the
	// block below is stdout, so the run's steps go to stderr, which is where
	// this command's converge already accounted for itself.
	run := stepRunFor(cmd, "accounts-rm", cmd.ErrOrStderr())
	defer run.close()
	stages, err := accountsAddRunStages(ctx, cmd, run, after, path, wrote, "accounts rm")
	if err != nil {
		return err
	}
	// The publish line is read off the node and never off the derivation the
	// write announced: the derivation is what the publish would write, and a
	// block that claimed the node had dropped the account without reading it
	// would be the one sentence this command may not print.
	allow, carried, why := accountsRmNode(ctx, after, plan.name)
	accountsRmReport(cmd, after, stages, plan, secret, allow, carried, why)
	return nil
}

// accountsRmNode reads accounts.<name> back off the published node. It is
// add's reader's twin and not add's own (accountsAddNodeAccount): add asks what
// the node carries, while a removal needs the fact that the node no longer
// carries the account, which an empty list cannot say — so carried is answered
// beside the list. why says why the node could not be read, and is "" when it
// was.
func accountsRmNode(ctx context.Context, lc *localcfg.Config, name string) (allow []string, carried bool, why string) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, false, oneLine(err.Error())
	}
	_, found, raw, err := readPolicyNode(ctx, db, lc)
	switch {
	case err != nil:
		return nil, false, oneLine(err.Error())
	case !found:
		return nil, false, "nothing is published at " + projectpolicy.PathShared()
	}
	p, err := publishedPolicyOf(raw, found)
	if err != nil {
		return nil, false, oneLine(err.Error())
	}
	acc, ok := p.Accounts[name]
	if !ok {
		return nil, false, ""
	}
	return acc.AllowRepos, true, ""
}

// accountsRmReport is the command's answer: one block naming what each step
// did, read off what happened, and then the one line that says what is left.
//
// A step that was skipped or that could not be read says so in the stage's own
// words or in the reader's, so a project with no budget backend is a sentence
// rather than a silent "gone" — and the two states that would mean the removal
// did not reach the installation (a node that still carries the account, a node
// the reader could not reach) are named as what they are.
func accountsRmReport(cmd *cobra.Command, lc *localcfg.Config, stages map[string]initflow.StageResult, plan accountsRmPlan, secret string, allow []string, carried bool, why string) {
	w := cmd.OutOrStdout()
	row := func(label, value, rest string) {
		fmt.Fprintf(w, "%-10s%-18s%s\n", label, pluginwire.Printable(value), rest)
	}
	if plan.entry {
		row("profile", plan.profile, "profiles."+pluginwire.Printable(plan.profile)+".claude_accounts -= "+pluginwire.Printable(plan.name))
	}
	if plan.drop {
		row("account", plan.name, "claude_accounts."+pluginwire.Printable(plan.name)+" removed, its allow_data_to was ["+strings.Join(plan.allow, ", ")+"]")
		// The account's name is the value column and the secret's ID opens the
		// rest, as add's report has it: the ID is longer than that column, so
		// naming it there would run into the sentence that says what happened to
		// it ("claude-oauth-token-team-adeleted: …").
		row("secret", plan.name, config.ClaudeAccountSecret(plan.name)+" "+secret)
	} else {
		row("account", plan.name, "claude_accounts."+pluginwire.Printable(plan.name)+" stays: "+accountsAddProfiles(plan.rest)+" still lists it")
		// The secret's row is here too, and says the step did not run: a block
		// that leaves it out reads as a deletion that printed nothing, and this
		// is the one row a reader checks for whether the credential is gone.
		row("secret", plan.name, config.ClaudeAccountSecret(plan.name)+" kept, not deleted: "+accountsAddProfiles(plan.rest)+" still serves the account, so its token is untouched")
	}
	row("publish", "policy node", accountsRmPublishLine(plan, stages[initflow.Policy], allow, carried, why))
	row("deploy", "jobs", accountsRmDeployLine(lc, plan, stages[initflow.Installation]))
	fmt.Fprintln(w, accountsRmLastLine(plan))
}

// accountsRmPublishLine is what the published node now carries for this
// account, read back from the node: gone, still carried (which no publish of
// the config this command wrote should have left), or unread in the reader's
// own words.
func accountsRmPublishLine(plan accountsRmPlan, st initflow.StageResult, allow []string, carried bool, why string) string {
	if st.State == initflow.Skipped {
		return "skipped: " + oneLine(st.Detail)
	}
	switch {
	case why != "":
		return "not read: " + why
	case plan.drop && carried:
		return "the node still carries accounts." + pluginwire.Printable(plan.name) + ".allow_repos = [" + strings.Join(allow, ", ") + "], and a publish of the config this wrote should have dropped it: run `fugaro update`"
	case plan.drop:
		return "accounts." + pluginwire.Printable(plan.name) + " is gone from " + projectpolicy.PathShared()
	case carried:
		return "accounts." + pluginwire.Printable(plan.name) + ".allow_repos = [" + strings.Join(allow, ", ") + "] (still served by " + accountsAddProfiles(plan.rest) + ")"
	}
	return "accounts." + pluginwire.Printable(plan.name) + " is gone from the node while " + accountsAddProfiles(plan.rest) + " still lists it: run `fugaro update`"
}

// accountsRmDeployLine is what the installation stage did, the same way. The
// job it names is the one this account's own entry in the job set was — one job
// and one service account per (profile, provider, account) — and it is the job
// the profile that lost the entry served, because the profiles that still list
// the account keep theirs.
func accountsRmDeployLine(lc *localcfg.Config, plan accountsRmPlan, st initflow.StageResult) string {
	switch st.State {
	case "":
		return "not run: the stage reported no result"
	case initflow.Changed:
	default:
		return string(st.State) + ": " + oneLine(st.Detail)
	}
	if plan.profile == "" {
		return "no job: no profile served " + pluginwire.Printable(plan.name)
	}
	job := gcp.ProjectJobName(plan.profile, lc.Profiles[plan.profile].Provider, plan.name)
	line := "- " + pluginwire.Printable(job) + " (and its service account)"
	if len(plan.rest) > 0 {
		line += "; " + accountsAddProfiles(plan.rest) + " still serves " + pluginwire.Printable(plan.name) + ", and its job stays"
	}
	return line
}

// accountsRmLastLine is the command's last line: the thing the owner does next,
// which is nothing. It is a run and never an update, as add's is, and what it
// says about the token is the half of D866 the owner needs after the fact:
// the installation no longer names the account, and the provider still does
// until the token expires or they revoke it there.
func accountsRmLastLine(plan accountsRmPlan) string {
	if !plan.drop {
		return fmt.Sprintf("Nothing else to run: %s no longer serves %s; %s still does, and its job, the account's secret and its allow_data_to are untouched.\n",
			pluginwire.Printable(plan.profile), pluginwire.Printable(plan.name), accountsAddProfiles(plan.rest))
	}
	return fmt.Sprintf("Nothing else to run: %s is gone from this installation (the token is not revoked at the provider).\n",
		pluginwire.Printable(plan.name))
}
