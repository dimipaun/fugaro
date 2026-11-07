package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
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
// in the confirmation before it goes, the command says plainly that deleting a
// secret does not revoke the token at the provider (D866), and a delete that
// fails puts the local config back behind a compare-and-swap rather than
// leaving a file whose declaration is gone while the credential is still there.
//
// Both worlds judge the same thing before anything moves (spec §4 step 1, and
// the managed plan's accounts handoff row): a profile that still lists the
// account refuses the removal, and so does a repository this machine can read
// whose committed fugaro.yaml names it in agent.oauth_token. What differs is
// who acts on the answer. A person's own run warns about the repository and
// goes on — that file is a repository's and not this installation's, so their
// edit may not be blocked by one this command cannot write (spec §4 step 2,
// D783's shape) — while a coding agent's session is admitted only by a managed
// project's gate, whose rules are those referrers read back (managed spec §4.2,
// D1180). Everywhere else a session keeps the refusal this command has always
// made.
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
confirmation before it goes, and the command says plainly that deleting the
secret does not revoke the token at the provider: it stays valid until it
expires or you revoke it in Anthropic's own console, which no command here
reaches.

Then it converges, in the same command and behind the same confirmation: the
policy node the account was published in, the job set and this checkout's
repository root, through the stages fugaro update runs.

If the delete fails after the config was written, the config is put back the
way it was and the command stops: an account whose declaration is gone while
its secret is still there is the one state this command may not leave behind.

Run it at your own terminal: this command deletes a credential, so a coding
agent's session is refused, --yes included, unless a managed project's gate
admits the removal — which it does only when no profile and no repository still
names the account.`,
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
// the file is touched, and what lets the confirmation name the secret's ID.
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
	// before the confirmation: one whose committed fugaro.yaml names the account
	// is a reference this command cannot edit, and it is what the gate refuses a
	// session on and what the confirmation warns a person about (spec §4 step 2,
	// managed spec §4.2).
	referrers, unseen := accountsRmRepos(lc, plan.name)

	// The managed gate, before anything is screened and written
	// (accounts_managed.go, add's own file, and the helper below): in a managed
	// project's coding agent's session it decides whether this removal is a step
	// the session may take, admitting one whose referrers are checked and clean
	// and refusing one whose are not — before the diff is shown, so a refusal
	// leaves the file and the secret exactly as they were. Everywhere else it
	// answers the zero Managed, which is every fence below unchanged.
	m, gate, err := accountsRmGate(cmd.Context(), lc, plan, next, referrers)
	if err != nil {
		return err
	}

	// The refusal a session with no admission keeps, in this command's own
	// words. It comes before the diff rather than after it, and --yes does not
	// answer it.
	if marker := agentMarker(os.Getenv); marker != "" && !initflow.CanConfirmManaged(initflow.YesOrTyped, initflow.Conditions{Agent: marker}, m) {
		return accountsRmSession(marker, plan, command)
	}

	// The one reference this command may not answer for, before the person
	// answers the confirmation and not after it: what it warns about is a break
	// the removal makes, and the confirmation is the moment that break can still
	// be declined.
	accountsRmWarn(cmd, plan, referrers, unseen)

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

	// The secret, which is the step the confirmation above named the ID for.
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

// accountsRmRepos is the repository half of the reference check (spec §4 step 2,
// managed spec §4.2): the repositories this project has enrolled whose committed
// fugaro.yaml names the account in agent.oauth_token, and the ones this machine
// cannot read — no checkout here, a file that does not parse — whose state is
// unknown rather than clean. It is the same walk `fugaro models rm` makes over
// the same set (modelsModelPinned), with no network and no cloud in it.
//
// The file is read through config.AgentOf and not through a resolved config:
// agent.oauth_token is repository-scope, so the file's own bytes are the whole
// answer, and a repository whose file leans on the project layer for something
// else is one this command can still read.
func accountsRmRepos(lc *localcfg.Config, name string) (referrers, unseen []string) {
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		checkouts = localcfg.EmptyCheckouts()
	}
	remembered := map[string]string{}
	for _, c := range checkouts.ProjectCheckouts(lc.Name) {
		remembered[localcfg.CheckoutIdentity{Provider: c.Provider, Repo: c.Repo}.Key()] = checkoutPath(c)
	}
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		dir := remembered[checkoutKeyFor(repo, remembered)]
		if dir == "" {
			unseen = append(unseen, repo)
			continue
		}
		data, err := readFugaroYAML(filepath.Join(dir, "fugaro.yaml"))
		if err != nil {
			unseen = append(unseen, repo)
			continue
		}
		agent, ok := config.AgentOf(data)
		if !ok {
			unseen = append(unseen, repo)
			continue
		}
		if agent.OAuthToken == name {
			referrers = append(referrers, repo)
		}
	}
	return referrers, unseen
}

// accountsRmWarn is the whole of the protection spec §4 step 2 leaves for a
// reference this command cannot edit (D783's shape, as `fugaro models rm` warns
// about a pin): it names every repository this machine can see whose committed
// fugaro.yaml names the account, what that repository's next run does once the
// declaration is gone, and the repositories it cannot read. It is a warning and
// never a block — that file is a repository's and not this installation's, so
// the owner's own edit may not be stopped by one this command cannot write —
// and it is printed while the confirmation below can still be declined, because
// the break it describes is one the removal makes.
func accountsRmWarn(cmd *cobra.Command, plan accountsRmPlan, referrers, unseen []string) {
	if !plan.drop {
		return
	}
	out := cmd.OutOrStdout()
	if len(referrers) > 0 {
		fmt.Fprintf(out, "warning: %s is named by %s's committed fugaro.yaml in agent.oauth_token\n",
			pluginwire.Printable(plan.name), strings.Join(referrers, ", "))
		fmt.Fprintf(out, "warning: the next run of each stops at launch: named Claude account %s is not one it may use once the declaration is gone. Take the name out of that file, or put the account back with `fugaro accounts add %s`\n",
			pluginwire.Printable(plan.name), quoteWord(plan.name))
	}
	if len(unseen) > 0 {
		fmt.Fprintf(out, "warning: this machine cannot read %s, so a reference there is unknown: run `fugaro accounts ls` in that checkout to see it\n",
			strings.Join(unseen, ", "))
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

// accountsRmSession is spec §4's last line as the managed gate leaves it
// (managed spec §4.2, D1181): the refusal a coding agent's session reads when no
// admission covers this removal. A managed project's gate admits an `accounts
// rm` whose referrers are checked and clean, and that is the only thing that
// does; a removal the gate refused returns the gate's own sentence instead of
// this one (accountsRmGate), so what reaches here is a project this program
// could not read as managed, or a mark read that did not answer — the two cases
// a mark that cannot be read is not a permission for.
func accountsRmSession(marker string, plan accountsRmPlan, command string) error {
	return userErr("refusing to remove %s in a coding agent's session (%s is set): this command deletes a credential, and only a managed project's gate admits it — with no admission covering this removal (no profile and no repository may still name the account, spec §4.2) nothing was read off this session, nothing was written and the secret %s is still there; run this yourself at your own terminal: %s",
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
func accountsRmGate(ctx context.Context, lc *localcfg.Config, plan accountsRmPlan, doc []byte, referrers []string) (initflow.Managed, managedGate, error) {
	marker := agentMarker(os.Getenv)
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
	applied, refused := accountsRmRules(plan, referrers)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, applied)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	// YesOrTyped is the class of the confirmation this step is: the ordinary
	// y/N the diff is answered with, and not a Typed step — the value D846
	// reserves for a loosened ceiling is what the declaration's own rules
	// already refuse here.
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
// D1172, D1180's row): a removal is admitted only when no profile and no
// repository this machine can read still references the account, and each
// refusal names the referrer it found rather than the key it would remove. A
// repository this machine cannot read is not one of these refusals but a warning
// (accountsRmWarn): refusing on it would refuse every project whose checkouts
// are not all on this machine, and what it would prevent is a run that stops at
// launch, which is a break the owner reads about rather than a permission.
func accountsRmRules(plan accountsRmPlan, referrers []string) (applied, refused []string) {
	if plan.drop {
		applied = append(applied, "no profile lists claude_accounts."+pluginwire.Printable(plan.name)+" once this step has run")
	} else {
		refused = append(refused, accountsAddProfiles(plan.rest)+" still lists claude_accounts."+pluginwire.Printable(plan.name)+" after this step, and a profile's claude_accounts names a job this installation has deployed: taking the account out from under a live job leaves a job the policy cannot describe, which only the owner can answer for (spec §4 step 1)")
	}
	if len(referrers) > 0 {
		refused = append(refused, "the committed fugaro.yaml of "+strings.Join(referrers, ", ")+" names "+pluginwire.Printable(plan.name)+" in agent.oauth_token, so the next run of each stops at launch: that file belongs to a repository this command cannot write, and repairing it is the owner's own edit (D783)")
	} else {
		applied = append(applied, "no repository this machine can read names "+pluginwire.Printable(plan.name)+" in its agent.oauth_token")
	}
	return applied, refused
}

// accountsRmWhat is the one confirmation's text (spec §4 step 2): every change
// this command makes — the profile entry, the declaration, the secret it
// deletes, the publish and the deploy behind them — and, because a deleted
// secret is the change the provider knows nothing about, the sentence that says
// what deleting it does NOT do (D866). The secret's ID is here for the same
// reason: it is named before it goes, so the person answering knows which
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
	stages, err := accountsAddRunStages(ctx, cmd, after, path, wrote)
	if err != nil {
		return err
	}
	// The publish line is read off the node and never off the derivation the
	// confirmation named: the derivation is what the publish would write, and a
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
		row("secret", config.ClaudeAccountSecret(plan.name), secret)
	} else {
		row("account", plan.name, "claude_accounts."+pluginwire.Printable(plan.name)+" stays: "+accountsAddProfiles(plan.rest)+" still lists it")
		// The secret's row is here too, and says the step did not run: a block
		// that leaves it out reads as a deletion that printed nothing, and this
		// is the one row a reader checks for whether the credential is gone.
		row("secret", config.ClaudeAccountSecret(plan.name), "kept, not deleted: "+accountsAddProfiles(plan.rest)+" still serves the account, so its token is untouched")
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
