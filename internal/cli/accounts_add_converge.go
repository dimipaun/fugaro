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
func accountsAddConverge(ctx context.Context, cmd *cobra.Command, after *localcfg.Config, path string, old []byte, name string, repos []string) error {
	env, err := openCloudFor(ctx, after)
	if err != nil {
		return err
	}
	defer env.Close()

	// Step 5: the secret, project-wide, as a new version of the account's own
	// ID. It runs after the local config and not before it because
	// projectWideAccepted refuses a name whose allow_data_to lists no
	// repository: the field has to exist first (spec §2.2 step 5).
	version, err := accountsAddStoreSecret(cmd, env, name)
	if err != nil {
		return err
	}

	// Step 6: the mark a 401 or a usage limit left behind. The token has just
	// been replaced, and clearing the mark is the operator's own remedy for it.
	// Nothing here fails the command — see accountsAddClearMark.
	mark := accountsAddClearMark(ctx, cmd, after, name)

	// Step 7: the publish and the deploy, through the registered stages and
	// nothing else.
	stages, err := accountsAddRunStages(ctx, cmd, after, path, old)
	if err != nil {
		return err
	}

	// Step 8: the read-back, which is what the command's own last line is made
	// of.
	check, err := accountsAddCheck(cmd, repos[0])
	if err != nil {
		return err
	}
	// The publish line is read off the node itself and not off what the
	// confirmation said the publish would write: this command's last line says
	// there is nothing left to run, so a block that names an account the node
	// does not carry would be the one sentence it may not print. The read is the
	// stage's own (readPolicyNode) and not a second one — nothing is written
	// here, and a node this reader cannot read says so instead.
	allow, unpublished := accountsAddNodeAccount(ctx, after, name)
	accountsAddReport(cmd, after, stages, name, repos, version, mark, check, allow, unpublished)
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

// accountsAddStoreSecret runs the project-wide store (secretsSetProjectWide,
// spec §2.2 step 5) and answers the version it wrote. The store's own line is
// captured rather than printed: this command answers with one block, and the
// version belongs on that block's account line. Nothing is swallowed — a line
// this release cannot read is printed as it came.
func accountsAddStoreSecret(cmd *cobra.Command, env *cloudEnv, name string) (string, error) {
	secret := config.ClaudeAccountSecret(name)
	out := cmd.OutOrStdout()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := secretsSetProjectWide(cmd, env, secretsOptions{}, secret)
	cmd.SetOut(out)
	if err != nil {
		return "", err
	}
	if v, ok := storedVersion(buf.String()); ok {
		return v, nil
	}
	// A store this release cannot read back is still a store: its own line is
	// the evidence, and printing it beats dropping it.
	_, _ = out.Write(buf.Bytes())
	return "", nil
}

// storedVersion is the version `fugaro secrets set` prints ("stored <id>,
// version <n>"), read off the line the store wrote. ok is false for a line this
// release does not recognise, which the caller prints rather than losing.
func storedVersion(line string) (string, bool) {
	i := strings.LastIndex(line, ", version ")
	if i < 0 {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimSuffix(line[i+len(", version "):], "\n"))
	if v == "" {
		return "", false
	}
	return v, true
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
// run's own writer, which newInitRun routes to stderr under asJSON, so this
// command's stdout is its answer alone.
func accountsAddRunStages(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, path string, old []byte) (map[string]initflow.StageResult, error) {
	o := &initOptions{door: "accounts add", yes: true, nonInteractive: true, asJSON: true}
	r := newInitRun(cmd, o)
	r.setProject(lc)
	spec, err := installOptions(o, lc)
	if err != nil {
		return nil, err
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	res, err := initflow.Run(ctx, accountsAddStages(e), e.options())
	if err != nil {
		return nil, userErr("%v", err)
	}
	if res.Failed != nil {
		// The stage's own error, which is a user error or a remote one already:
		// the fix line names the stage, and this command adds nothing to it.
		return nil, res.Cause
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
func accountsAddStages(e *initEngine) []initflow.Stage {
	var out []initflow.Stage
	for _, st := range initStages(e) {
		switch st.Name() {
		case initflow.Policy, initflow.Installation, initflow.Repository:
			out = append(out, st)
		}
	}
	return out
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
func accountsAddReport(cmd *cobra.Command, lc *localcfg.Config, stages map[string]initflow.StageResult, name string, repos []string, version, mark string, check accountsListDoc, allow []string, unpublished string) {
	w := cmd.OutOrStdout()
	secret := config.ClaudeAccountSecret(name)
	row := func(label, value, rest string) {
		fmt.Fprintf(w, "%-10s%-18s%s\n", label, pluginwire.Printable(value), rest)
	}
	if version != "" {
		row("account", name, secret+" (project-wide, version "+version+")")
	} else {
		row("account", name, secret+" (project-wide)")
	}
	row("repos", strings.Join(repos, ", "), "claude_accounts."+pluginwire.Printable(name)+".allow_data_to")
	profile := accountsAddProfileOf(lc, name)
	if profile != "" {
		row("profile", profile, "profiles."+pluginwire.Printable(profile)+".claude_accounts += "+pluginwire.Printable(name))
	}
	row("mark", name, mark)
	row("publish", "policy node", accountsAddPublishLine(name, stages[initflow.Policy], allow, unpublished))
	row("deploy", "jobs", accountsAddDeployLine(lc, profile, name, stages[initflow.Installation]))
	if check.Repo != "" {
		row("check", "accounts ls", pluginwire.Printable(check.Repo)+" may use: "+strings.Join(check.Accounts, ", "))
	}
	for _, repo := range accountsAddUnconverged(cmd.Context(), repos) {
		fmt.Fprintf(w, "%s has no checkout here, so its own repository root was not converged: run `fugaro update` in that checkout (fugaro doctor says whether it matters)\n",
			pluginwire.Printable(repo))
	}
	fmt.Fprintf(w, "Nothing else to run: fugaro run --token %s is ready.\n", pluginwire.Printable(name))
}

// accountsAddPublishLine is what the published node now carries for this
// account, read back from the node and never from the derivation the
// confirmation named: the derivation is what the publish would write, and the
// one thing this line may not do is report it as written. A stage that was
// skipped prints its own reason, and every other state speaks in the node's
// terms — the account's list, or the fact that the node does not carry it.
func accountsAddPublishLine(name string, st initflow.StageResult, allow []string, unpublished string) string {
	switch {
	case st.State == initflow.Skipped:
		return "skipped: " + oneLine(st.Detail)
	case allow != nil:
		return "accounts." + pluginwire.Printable(name) + ".allow_repos = [" + strings.Join(allow, ", ") + "]"
	}
	return "not published: " + unpublished
}

// accountsAddDeployLine is what the installation stage did, the same way. The
// job it names is the one this account's own entry in the job set is: one job
// and one service account per (profile, provider, account), with
// secretAccessor on that account's secret and no other account's.
func accountsAddDeployLine(lc *localcfg.Config, profile, name string, st initflow.StageResult) string {
	switch st.State {
	case "":
		return "not run: the stage reported no result"
	case initflow.Skipped:
		return "skipped: " + oneLine(st.Detail)
	case initflow.Done:
		return "already current"
	}
	if profile == "" {
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
	return projectpolicy.DeriveAccounts(policyProfiles(lc), lc.ClaudeAccounts, lc.ProjectSecrets)
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
