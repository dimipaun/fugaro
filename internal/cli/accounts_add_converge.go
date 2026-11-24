package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/report"
)

// This file is the second half of `fugaro accounts add` (spec §2.2 steps 5–8,
// §2.5): the secret, the mark, the three stages the command invokes in-process,
// and the read-back that is its answer.
//
// It is the half that makes the command one command. A declaration the owner
// still has to finish by hand — store the token, deploy the jobs, publish the
// node — is a command that leaves work behind, with the HARD RULE's promise
// broken by the command that is supposed to keep it.
//
// The stages are fugaro update's own, built by initStages and invoked here
// rather than forked as a child: a child would re-run the door's whole list
// (images, security rules, plugin, checkout) behind its own confirmation, and
// this command's output would be a transcript of another command rather than
// its own (spec §2.5).

// accountsAddConverge is steps 5 to 8 of spec §2.2, in that order.
//
// after is the local config as the command wrote it: the stages read the file
// the command just edited, and the account is in it. old is the bytes that
// write replaced, which the engines take for the same reason `fugaro update`
// does.
//
// A failure in here is an error, and that is the point: the last line of this
// command says there is nothing left to run, so a token that was not stored or
// a job set that was not deployed must stop the command rather than be
// announced as done. Re-running the command repairs it (spec §2.2).
// profile is the one the command resolved and the confirmation named, passed
// down rather than read off the file again: a project whose profiles list the
// account in more than one place would otherwise report a different profile
// than the one this run's edit — and its confirmation — were about.
//
// typ is the --type the command was given, "" when it was given none, and it
// decides the secret below: a key credential's value is stored under the
// credential's own derived name and not the account's (accountsAddSecretName,
// MC22). Every other step is the same one for both — a key credential is
// declared, bounded and converged exactly as an account is (D1669).
func accountsAddConverge(ctx context.Context, cmd *cobra.Command, after *localcfg.Config, path string, old []byte, name, typ, profile string, repos []string) error {
	// The command's own account (CL7a): the steps below print through one
	// reporter and the run's full output is kept in one log, and both are this
	// command's — the converge is invoked in-process, so a second run with a
	// log of its own would file two. The human lines go to stderr, which is
	// where this command's run already accounted for itself: its stdout is the
	// block below and nothing else.
	run := stepRunFor(cmd, "accounts-add", cmd.ErrOrStderr())
	defer run.close()

	env, err := openCloudFor(ctx, after)
	if err != nil {
		return err
	}
	defer env.Close()

	// Step 5: the secret, project-wide, as a new version of the secret this
	// run's type owns — resolved from the flag AND from the type the config
	// declares the name with, so a re-run without --type still writes the
	// credential's own container (accountsAddSecretType). It runs after the local
	// config and not before it because projectWideAccepted refuses a name whose
	// allow_data_to lists no repository: the field has to exist first (spec §2.2
	// step 5).
	secret := accountsAddSecretName(name, accountsAddSecretType(after, name, typ))
	version, err := accountsAddStoreSecret(run, cmd, env, after, name, accountsAddSecretType(after, name, typ), secret)
	if err != nil {
		return err
	}

	// Step 6: the mark a 401 or a usage limit left behind. The token has just
	// been replaced, and clearing the mark is the operator's own remedy for it.
	// Nothing here fails the command — see accountsAddClearMark.
	mark := accountsAddClearMark(ctx, cmd, after, name)

	// Step 7: the publish and the deploy, through the registered stages and
	// nothing else.
	stages, err := accountsAddRunStages(ctx, cmd, run, after, path, old, "accounts add")
	if err != nil {
		return err
	}

	// Step 8: the read-back, which is what the command's own last line is made
	// of. It is a step of the run's account only when it fails: the account it
	// prints on success IS this step's answer — the block below is read off it —
	// so a line of its own would be the run saying the same thing twice. An
	// abandoned step ends nothing and counts nothing (Reporter.Step), and a
	// read-back that fails ends it with a cross: this command's last line says
	// there is nothing left to run, and a run that cannot read back what it
	// wrote may not close on "Done" (G3, G8).
	readBack := run.rep.Step("Reading back " + pluginwire.Printable(repos[0]))
	defer readBack.Abandon()
	check, err := accountsAddCheck(cmd, repos[0])
	if err != nil {
		readBack.Failed(errors.New(oneLine(err.Error())), nil)
		return err
	}
	// The publish line is read off the node itself and not off what the
	// confirmation said the publish would write: this command's last line says
	// there is nothing left to run, so a block that names an account the node
	// does not carry would be the one sentence it may not print. The read is the
	// stage's own (readPolicyNode) and not a second one — nothing is written
	// here, and a node this reader cannot read says so instead.
	allow, unpublished := accountsAddNodeAccount(ctx, after, name)
	accountsAddReport(cmd, after, stages, name, typ, profile, repos, version, mark, check, allow, unpublished)
	return nil
}

// accountsAddNodeAccount reads accounts.<name> back off the published node and
// answers the repositories it admits. allow is nil for a node that does not
// carry the account — and unpublished then says which of the reasons it is: no
// reachable node, a node that cannot be read, or one that names another set of
// accounts than the config the command just wrote.
func accountsAddNodeAccount(ctx context.Context, lc *localcfg.Config, name string) (allow []string, unpublished string) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, oneLine(err.Error())
	}
	_, found, raw, err := readPolicyNode(ctx, db, lc)
	switch {
	case err != nil:
		return nil, oneLine(err.Error())
	case !found:
		return nil, "nothing is published at " + projectpolicy.PathShared()
	}
	p, err := publishedPolicyOf(raw, found)
	if err != nil {
		return nil, oneLine(err.Error())
	}
	acc, ok := p.Accounts[name]
	if !ok {
		return nil, "the node names no account " + pluginwire.Printable(name)
	}
	return acc.AllowRepos, ""
}

// accountsAddStoreSecret runs the store of step 5 and answers the version it
// wrote: project-wide, as a new version of the secret this run's type owns
// (secretsSetProjectWide, spec §2.2 step 5), or — for a provider key, whose
// home is the credentials service's bundle since KS2 (R2, D1670 reversed) —
// the bundle entries C1's family names, one per repository the credential is
// bound to, and no container of the credential's own. secret is the name the
// credential's value lives under, resolved by the caller from this run's type
// and from the type the config declares the name with (accountsAddSecretType):
// it is passed in rather than derived here so the step, the sentence and the
// block all name the one container. It opens the step through the one opener
// both stores share (secretsStoreStep), so this step of the account is `fugaro
// secrets set`'s own: one line naming the container, a cross carrying the
// server's error when the store fails, and the run's log beside it. The version
// then reaches this command's block as the store's answer and not as a line read
// back out of its output (CL7a): the step and the block are two accounts of one
// store, and neither is parsed out of the other.
func accountsAddStoreSecret(run *stepRun, cmd *cobra.Command, env *cloudEnv, lc *localcfg.Config, name, typ, secret string) (string, error) {
	if config.KeyCredentialType(typ) && config.CredentialServingPath(typ) == config.CredentialPathRunner {
		return secretsStoreStep(run, secret, func(st *report.Step) (string, string, error) {
			return providerKeyBundleStore(run, cmd, st, lc, name, secret, providerKeyCredentialRepos(lc, name), false)
		})
	}
	return secretsStoreStep(run, secret, func(st *report.Step) (string, string, error) {
		return secretsSetProjectWide(run, cmd, st, env, secretsOptions{}, secret)
	})
}

// accountsAddClearMark clears the account's mark (spec §2.2 step 6) and answers
// how it stands, for the block. Only the two states an operator's own action
// ends are cleared, exactly as `fugaro accounts clear` decides it: a backed-off
// mark is a probe's (C1.12) and a state this release does not know could be a
// hold a newer release placed, so both are left alone with the sentence that
// says so.
//
// Nothing here is a failure, and that is deliberate. The command's own work —
// the declaration and the token — is done by this point, and a project with no
// budget database, a database that is not this installation's, and a mark the
// service moved while this ran all leave a state the owner's own `fugaro
// accounts clear` or the cascade's next call settles. What the command may not
// do is stop after storing a token because a mark could not be cleared — and
// what it may not do either is call such a mark cleared.
func accountsAddClearMark(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, name string) string {
	w := cmd.OutOrStdout()
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return "not read: " + oneLine(err.Error())
	}
	// The two questions every writer into the database asks before it writes
	// (requireOwnBudgetDB, checkDBProject): a database this installation never
	// adopted, and one that belongs to another project, are both states the
	// clear declines in rather than failures of this command.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was cleared"); err != nil {
		return "left alone: " + oneLine(err.Error())
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return "left alone: " + oneLine(err.Error())
	}
	store := &creds.RTDBStore{DB: db}
	marks, err := store.Marks(ctx)
	if err != nil {
		return "not read: " + oneLine(err.Error())
	}
	prev, has := marks[name]
	switch {
	case !has, prev.State == "":
		return "nothing held"
	case prev.State == creds.StateAvailable:
		return "nothing held: " + pluginwire.Printable(name) + " is available"
	case prev.State == creds.StateBroken, prev.State == creds.StateExhausted:
	case prev.State == creds.StateBackedOff:
		fmt.Fprintf(w, "note: the mark of %s was left alone: a backed-off mark is a probe's to clear (C1.12), because clearing it by hand would route real work to a credential that has not answered since it was held — run `fugaro accounts clear %s` if that is what you mean\n",
			pluginwire.Printable(name), pluginwire.Printable(name))
		return "left as it was: backed off"
	default:
		fmt.Fprintf(w, "note: the mark of %s carries the state %s, which this Fugaro does not know, so it was left alone: clearing it could take off a hold a newer release placed — run `fugaro accounts clear %s` if that is what you mean\n",
			pluginwire.Printable(name), oneLine(string(prev.State)), pluginwire.Printable(name))
		return "left as it was: " + oneLine(string(prev.State))
	}
	_, err = store.UpdateMark(ctx, name, func(cur creds.Mark, found bool) (creds.Mark, error) {
		switch {
		case !found:
			// The mark was removed between the read and this write: there is
			// nothing to clear, and writing one would leave a mark node for a
			// credential that is not held.
			return cur, errMarkGone
		case cur.State != prev.State:
			// The service recorded something else while this ran. Its record is
			// the newer fact, and this command is not the operator's
			// confirmation of it: the write is a no-op rather than a clear
			// meant for a state that is no longer there.
			return cur, errMarkMoved
		}
		return creds.Cleared(), nil
	})
	switch {
	case errors.Is(err, errMarkGone), errors.Is(err, errMarkMoved):
		return "left alone: the mark changed while this ran"
	case err != nil:
		return "not read: " + oneLine(err.Error())
	}
	return "cleared (was " + stateText(accountStateOf(prev)) + ")"
}

// errMarkMoved and errMarkGone are the two ways the conditional clear above
// declines to write: the mark is not what the read said it was, or it is not
// there at all. Both are conditions and not failures — see the caller.
var (
	errMarkMoved = errors.New("the mark changed while this ran")
	errMarkGone  = errors.New("the mark was removed while this ran")
)

// accountsAddRunStages invokes the Policy, Installation and running-checkout
// Repository stages in-process (spec §2.5) and answers what each of them did,
// keyed by stage name.
//
// The order is the registered order and not this file's: initflow.Validate
// sorts the selection into the canonical one, so the job set is deployed
// against the installation before the checkout's repository root is converged,
// and the policy node — which every run of the project is admitted by — is
// published last, after the installation it is read against.
//
// The run's own confirmation is this command's (spec §2.5: one confirmation,
// which the person already answered), which is why the loop is given Yes, and
// why it carries asJSON: the stages and the loop print progress through the
// run's own writer, which newInitRun routes to stderr under asJSON, and the
// command's own writer is pointed at stderr for the length of the run besides,
// which is what keeps a stage that prints to cmd.OutOrStdout() directly — the
// policy stage's diff — off the stdout this command's answer is on.
// door is the command whose run this is — "accounts add" or "accounts rm",
// whichever called — and it is the caller's because both commands converge
// through this one loop: the door names the run on every line it prints (the
// header, the fix a stage leaves, the step left for the person), so a removal
// run that borrowed add's name would print its own progress as add's and hand
// the person the inverse command to finish with.
func accountsAddRunStages(ctx context.Context, cmd *cobra.Command, run *stepRun, lc *localcfg.Config, path string, old []byte, door string) (map[string]initflow.StageResult, error) {
	o := &initOptions{door: door, yes: true, nonInteractive: true, asJSON: true}
	r := newInitRun(cmd, o)
	// The caller's account, not a second one (CL7a): the stages print one line
	// each through the reporter this command opened, and the run's own output —
	// Terraform's above all — goes to the log this command keeps. A converge
	// invoked here with a reporter of its own would file a log the command
	// never names and close it behind the command's back. The engine's own
	// redaction list is not handed over with them: the three stages this door
	// selects hold no value (initEngine.hold's one caller is the secrets stage),
	// and what keeps the command's own token out of the log is the run's own
	// hold (stepRun.hold).
	r.rep, r.repLog = run.rep, run.log
	r.setProject(lc)
	// The workdirs are the converge's, whichever door converged them: the
	// Installation stage rebuilds the installation's tree and the Repository
	// stage the checkout's (both go through PrepareWorkdir), which is the pair
	// `fugaro update` takes the lock for. So this door takes it too — here,
	// where the project is known and before the loop prepares anything, and not
	// in a stage, for the reason runInit.lockWorkdirs states.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// installSpec and not installOptions: this door composes the same spec
	// `fugaro update` does and plans the same jobs, so a config that can no
	// longer compose the job of a default profile this installation holds must
	// not stop it (init_policy_migrate.go's heldDefaultProfiles answers every
	// door). A refusal here would leave the operator unable to add or remove an
	// account of a project their own `fugaro update` converges.
	spec, err := installSpec(o, lc, r.warn)
	if err != nil {
		return nil, err
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	// The run's own writer is stderr under asJSON, which newInitRun arranges,
	// and that is not the whole of it: a stage that prints to cmd.OutOrStdout()
	// directly — the policy stage's publish diff and its confirmation are the
	// loudest — would land on this command's stdout, where its answer is. The
	// command is pointed at stderr for the length of the run, the way the other
	// captures in this file do it, so stdout is the answer alone.
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	opts := e.options()
	// The reporter is this command's (ReportBorrowed): the loop's steps are
	// three of the eight this command accounts for, and the closing line is
	// printed where this command's own run ends — after the read-back, which is
	// what its last line is made of. The loop printing it here would account a
	// run "Done" before the step that may fail (G3).
	opts.ReportBorrowed = true
	res, err := initflow.Run(ctx, accountsAddStages(e), opts)
	cmd.SetOut(answer)
	if err != nil {
		return nil, userErr("%v", err)
	}
	if res.Failed != nil {
		// The stage's own error, which is a user error or a remote one already:
		// the fix line names the stage, and this command adds nothing to it.
		return nil, res.Cause
	}
	if len(res.Left) > 0 {
		// A stage that ended NeedsYou (or Blocked behind one that did) is not a
		// converged installation: the stages behind it never ran, so the node
		// the run is admitted by does not carry this account, and the loop
		// records the reason in Left rather than in Failed. The one-line user
		// error is the same one fugaro update ends on (init_inputs.go), because
		// the last line of this command says there is nothing left to run and
		// that is only true when the run it names is ready.
		return nil, userErr("%v", res.Left[0].Text)
	}
	out := make(map[string]initflow.StageResult, len(res.Stages))
	for _, st := range res.Stages {
		out[st.Name] = st
	}
	return out, nil
}

// accountsAddStages is the run's stage list: the three spec §2.5 names, and
// nothing else. It is built from initStages — the registered list — so this
// command invokes the same stages `fugaro update` does rather than a copy of
// them, and a stage that is not one of the three (the images, the security
// rules, the plugin wiring, the checkout's own steps) is not this command's
// business: an account's declaration changes none of them, and running them
// would make this command a second `fugaro update`.
//
// The three are named through stagesNamed (spec §2.5: a command runs the steps
// the change it made needs, in initflow's order), so this command and every
// later composite command name a subset the same way. The names are initflow's
// own constants, which are the registered names by construction — the second
// answer is discarded here because a name that is not a registered stage cannot
// be spelled with a constant, and a test pins the three the door runs.
func accountsAddStages(e *initEngine) []initflow.Stage {
	stages, _ := stagesNamed(e, initflow.Policy, initflow.Installation, initflow.Repository)
	return stages
}

// accountsAddCheck is spec §2.2 step 8: `fugaro accounts ls` for the first
// repository the command named, run in-process, so the command's answer is a
// read-back rather than a claim about one. The document is asked for as JSON —
// the machine-readable surface that stays this command's own — and printed by
// the caller as one line of the block.
func accountsAddCheck(cmd *cobra.Command, repo string) (accountsListDoc, error) {
	out := cmd.OutOrStdout()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := accountsLs(cmd, accountsOptions{repo: repo, asJSON: true})
	cmd.SetOut(out)
	if err != nil {
		return accountsListDoc{}, err
	}
	var doc accountsListDoc
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		return accountsListDoc{}, remote(fmt.Errorf("reading the answer of this command's own accounts ls: %w", err))
	}
	return doc, nil
}

// accountsAddReport is the command's answer (spec §2.6): one block naming what
// each step did, and then the one line that is the whole point — the thing the
// owner does next, which is a run and never an update.
//
// Every line but the last is read off something that happened: the version the
// store wrote, the state of the mark the database now holds, the stages' own
// results and the command's own read-back. A step that was skipped says so in
// the stage's words, so a project with no budget backend is a sentence rather
// than a silent "published".
func accountsAddReport(cmd *cobra.Command, lc *localcfg.Config, stages map[string]initflow.StageResult, name, typ, profile string, repos []string, version, mark string, check accountsListDoc, allow []string, unpublished string) {
	w := cmd.OutOrStdout()
	// The type this run's value was stored under, resolved exactly as the store
	// step resolved it: a re-run carries no --type, and both the container this
	// block names and the last line below are about the credential the config
	// declares and not about an account's token (accountsAddSecretType).
	typ = accountsAddSecretType(lc, name, typ)
	secret := accountsAddSecretName(name, typ)
	row := func(label, value, rest string) {
		fmt.Fprintf(w, "%-10s%-18s%s\n", label, pluginwire.Printable(value), rest)
	}
	if version != "" {
		row("account", name, secret+" (project-wide, version "+version+")")
	} else {
		row("account", name, secret+" (project-wide)")
	}
	// The list the account ENDS UP with, read back from the config this command
	// wrote, and not the --repos it was given: re-declaring an account adds to
	// the list it already has, so a line naming the flag would print a shorter
	// list than the file now holds. It is A1's confirmation's own source, for
	// A1's reason (accountsAddWhat).
	row("repos", strings.Join(lc.ClaudeAccounts[name].AllowDataTo, ", "), "claude_accounts."+pluginwire.Printable(name)+".allow_data_to")
	// The profile the command resolved, which is the one the confirmation named
	// and whose claude_accounts the edit above wrote. Read off the file only
	// when there is none: a project where two profiles list the account has no
	// single answer to read back, and the alphabetically first of them is not
	// the one this run was about.
	if profile == "" {
		profile = accountsAddProfileOf(lc, name)
	}
	if profile != "" {
		row("profile", profile, "profiles."+pluginwire.Printable(profile)+".claude_accounts += "+pluginwire.Printable(name))
	}
	row("mark", name, mark)
	row("publish", "policy node", accountsAddPublishLine(name, typ, stages[initflow.Policy], allow, unpublished))
	row("deploy", "jobs", accountsAddDeployLine(lc, typ, profile, name, stages[initflow.Installation]))
	if check.Repo != "" {
		row("check", "accounts ls", pluginwire.Printable(check.Repo)+" may use: "+strings.Join(check.Accounts, ", "))
	}
	for _, repo := range accountsAddUnconverged(cmd.Context(), repos) {
		fmt.Fprintf(w, "%s has no checkout here, so its own repository root was not converged: run `fugaro update` in that checkout (fugaro doctor says whether it matters)\n",
			pluginwire.Printable(repo))
	}
	// The last line is what the owner does next, and for an account it is a run.
	// It is not for a key credential: such a name is served by no Claude account
	// job and no `--token NAME` run, so what it says is the HALF that serves its
	// type and what has to exist before a run is authenticated with it (D1670,
	// MC23) — a run that cannot authenticate is the one thing a "ready" here
	// would promise.
	if config.KeyCredentialType(typ) {
		fmt.Fprintf(w, "%s\n", accountsAddKeyServingLine(name, typ))
		return
	}
	fmt.Fprintf(w, "Nothing else to run: fugaro run --token %s is ready.\n", pluginwire.Printable(name))
}

// accountsAddKeyServingLine is the last line for a key credential: the half of
// a run that serves the credential's type, what carries its key there, and —
// where that is not already in place — the one step that is left (D1670, MC23,
// config.CredentialServingPath).
//
// The two types are served by the two halves of a run, and neither is a job of
// the credential's own: the credentials service reads the bundle for the
// service-served type and reads C1's named entry for the runner-path one — the
// entry this very command wrote since KS2 (R2, D1670 reversed). The line
// therefore names the value's OWN container (config.CredentialSecret) for the
// service-served type and the bundle's entries for the runner-path one, so an
// operator who meets the credential again — in `fugaro accounts ls`, in a pool,
// in a run's log — can read out of it where the key goes without a second
// document.
//
// The service-served half needs one write of its own, and the line says so
// rather than closing on "nothing else to run": the value sits in the
// credential's own container after this run, and the bundle entry the service
// reads it from is written by `fugaro creds set`, which reads the value itself
// and is refused in an agent's session. The runner-path half is complete: this
// command entered the bundle's entries itself, and the provider whose address
// and auth they carry was refused as missing before a value was read
// (providerKeyFacts).
func accountsAddKeyServingLine(name, typ string) string {
	shown := pluginwire.Printable(name)
	secret := pluginwire.Printable(config.CredentialSecret(name))
	if config.CredentialServingPath(typ) == config.CredentialPathRunner {
		// A provider key's home is the credentials service's bundle since KS2
		// (R2, D1670 reversed by D2975): the value this command stored went into
		// C1's named entries, one per repository the credential is bound to, and
		// the service serves a routed call out of them — the address and the auth
		// it forwards to are the entry's own, read from providers.<name>.
		//
		// There is nothing else to run, and the one thing the line may not do is
		// leave the operator looking for the key in a mount: the runner-path
		// mount of the credential's own secret is still built into a provider's
		// job by this release (internal/infra's runnerKeyCredential, which
		// release B removes), and this write entered no version of it. A line
		// that named the mount as what serves the key would send the owner to a
		// container this run left empty.
		return fmt.Sprintf("Nothing else to run: %s is declared and its key is entered in the credentials service's bundle, one entry per repository claude_accounts.%s.allow_data_to names. The service serves a routed call from that entry, whose address and auth are providers.%s's, and the mount of %s a job of that provider carries is left without a version: this key is not stored in a container any more.\n",
			shown, shown, shown, secret)
	}
	return fmt.Sprintf("%s is declared and its key is stored under %s. The credentials service serves %s from the bundle it reads, and that bundle is written by one command: run `fugaro creds set %s` at your own terminal to put the value where the service reads it — a run whose craft's access names %s cannot authenticate with it until then.\n",
		shown, secret, shown, secret, shown)
}

// accountsAddPublishLine is what the published node now carries for this
// account, read back from the node and never from the derivation the
// confirmation named: the derivation is what the publish would write, and the
// one thing this line may not do is report it as written. A stage that was
// skipped prints its own reason, and every other state speaks in the node's
// terms — the account's list, or the fact that the node does not carry it.
//
// A key credential is the one state whose absence from the node is by design
// rather than a failure: the accounts block is derived from the profiles'
// claude_accounts lists (projectpolicy.DeriveAccounts) and no profile lists a
// key credential, because what that list serves is a run's Claude account
// token and never a key (accounts_add.go). Saying "not published: the node
// names no account team-a" there would read as the stage having failed to
// write what the command asked for.
func accountsAddPublishLine(name, typ string, st initflow.StageResult, allow []string, unpublished string) string {
	switch {
	case st.State == initflow.Skipped:
		return "skipped: " + oneLine(st.Detail)
	case config.KeyCredentialType(typ):
		return "no account entry: a key credential is not served to a run as a Claude account, so the node carries none — its reach is claude_accounts." + pluginwire.Printable(name) + ".allow_data_to"
	case allow != nil:
		return "accounts." + pluginwire.Printable(name) + ".allow_repos = [" + strings.Join(allow, ", ") + "]"
	}
	return "not published: " + unpublished
}

// accountsAddDeployLine is what the installation stage did, the same way. The
// job it names is the one this account's own entry in the job set is: one job
// and one service account per (profile, provider, account), with
// secretAccessor on that account's secret and no other account's.
//
// A key credential has no job of its own, and that is not a job missing: the
// name is in no profile (accounts_add.go), which is exactly what keeps a job
// from mounting an empty claude-oauth-token-<name> as the account's token. What
// carries its key to a call is the credentials service, for either of the two
// serving paths (KS2, R2) — and the runner-path mount a provider's job still
// carries (internal/infra's runnerKeyCredential, which release B removes) is
// not what serves it: the write entered no version of it.
func accountsAddDeployLine(lc *localcfg.Config, typ, profile, name string, st initflow.StageResult) string {
	switch st.State {
	case "":
		return "not run: the stage reported no result"
	case initflow.Changed:
		// The one state that applied: the job set was written by this run, and
		// the line that names the job is the line that says so.
	default:
		// Every other state is the stage's own answer, in its own words —
		// needs-you, blocked, billing-pending, done, will-do. A `+ <job>` line
		// over one of them would claim a deployment that did not happen, and
		// the last line of this command then tells the owner to start a run on
		// a job that is not there.
		return string(st.State) + ": " + oneLine(st.Detail)
	}
	if profile == "" {
		if config.KeyCredentialType(typ) {
			// The line says where the key IS served: a runner-path credential's
			// entries in the bundle are the provider's address and auth for the
			// repositories its declaration names, and a service-served one is read
			// from the same bundle (KS2, D1670 reversed by D2975). The mount a
			// provider's job still carries is named as left without a version
			// rather than as serving the key, which is what reading it would say.
			return "no job: a key credential rides no job of its own — the credentials service serves its key from the bundle, one entry per repository claude_accounts." + pluginwire.Printable(name) + ".allow_data_to names, and the mount of " + pluginwire.Printable(config.CredentialSecret(name)) + " a provider job carries is left without a version"
		}
		return "no job: no profile serves " + pluginwire.Printable(name) + " yet"
	}
	// The job's name is gcp.ProjectJobName's — the one function the job set,
	// the launcher and `fugaro run --token` all name a job through — so the
	// line and the job that exists cannot part company.
	job := gcp.ProjectJobName(profile, lc.Profiles[profile].Provider, name)
	return "+ " + pluginwire.Printable(job) + " (and its service account)"
}

// projectPublishMembers is the accounts block a publish derives from the local
// config, for the lines that have to say what the node carries: the derivation
// is projectpolicy.DeriveAccounts, which is the one the Policy stage writes
// through, so this is a read of the same derivation and never a second one.
func projectPublishMembers(lc *localcfg.Config) (map[string]projectpolicy.Account, error) {
	return projectpolicy.DeriveAccounts(policyProfiles(lc), lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts())
}

// accountsAddProfileOf is the profile that now serves name: what the block
// names, read off the config the command wrote rather than remembered from the
// command line, because a name no profile lists is the same account with no
// job at all.
func accountsAddProfileOf(lc *localcfg.Config, name string) string {
	for _, p := range slices.Sorted(maps.Keys(lc.Profiles)) {
		if slices.Contains(lc.Profiles[p].ClaudeAccounts, name) {
			return p
		}
	}
	return ""
}

// accountsAddUnconverged is D865's corner: a repository the command named whose
// checkout is not the one the command runs in. Its installation half is done —
// the job exists and serves it — and its own repository root is not, which the
// command names rather than leaving the owner to discover it (spec §2.5).
func accountsAddUnconverged(ctx context.Context, repos []string) []string {
	var out []string
	for _, repo := range repos {
		if checkoutRoot(ctx, repo) == "" {
			out = append(out, repo)
		}
	}
	return out
}
