package cli

// This file is the convergent half of `fugaro project repos add` (RE3,
// docs/repos-management; the owner's order of 2026-11-14: "it's sooo difficult
// to add a repo, we need a command to take care of all this").
//
// Enrolling a repository used to be about six acts done by hand in the right
// order — the repository's own fugaro.yaml, the `repos:` entry, the image map
// entry, the published allow-list, the publish, and the repository's own root
// for the job and its run.invoker grant — and the HARD RULE (CLAUDE.md,
// "Things that bite") says the documented commands do the whole job. One
// confirmation, six steps, each of them idempotent, is that promise kept for
// enrolment.
//
// What this half does NOT do is invent a second copy of the reads or of the
// steps. "What is this repository made of" is RE0's enrolment view
// (repos_view.go): the row is built by reposViewRowOf, the same function `ls`
// and `status` build theirs with, so the image this command maps cannot drift
// from the image they report and a change to that derivation reaches this
// command with no second copy of it here. The published node is still written
// by the one ETag-guarded writer (putAllowRepos), and the publish and the
// deploy are `fugaro update`'s own registered stages, invoked in-process the
// way `fugaro accounts add` invokes them (accounts_add_converge.go). A
// converge that reimplemented a stage would be the one thing the HARD RULE
// cannot survive: two writers of the same document, drifting apart.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// reposEnrolment is everything the convergence read before it changed
// anything, and everything the printed plan is built from.
//
// Every field is a fact read from a file, from a checkout or from the project,
// never a guess: a value the command cannot read is either refused where it is
// read, naming the one command that supplies it, or left out with the line
// that says so (D2401). That is the whole difference between a converge and a
// wizard that fills things in.
type reposEnrolment struct {
	// canonical is provider/owner/name: the form of the allow-list entry this
	// command writes, and the name every job is deployed for. provider is the
	// kind it was read at (reposEnrolmentProvider), which is also the kind the
	// image step's key names: the two are one repository's identity, and a key
	// written at another kind is one no run's lookup reaches.
	canonical string
	provider  string
	// ownerName is owner/name: the local config's key for it, and the bare name
	// the image step resolves a key for (repoImageKey).
	ownerName string
	// localKey is the key this machine's config holds it under, which is the
	// operator's own spelling where it holds one and ownerName otherwise: the
	// write below is keyed by it, so a hand-written entry in another case
	// gains its workflows rather than a second entry beside it.
	localKey string
	// row is the enrolment view's answer for this one repository (RE0): the
	// provider, the base branch, the workflows with the catalog entry each
	// runs, and the Claude accounts, resolved the way `ls` and `status`
	// resolve them.
	row reposViewRepo
	// entry is the `repos:` entry the file must hold, entryNow the one it
	// holds, entryKnown whether it held one at all (ENROLMENT), and entryWhy
	// the step's own line.
	entry      localcfg.Repo
	entryNow   localcfg.Repo
	entryKnown bool
	entryWhy   string
	// accountAllow is the account whose allow_data_to this run extends with
	// the repository, and accountWhy what the plan and the write say about it
	// (reposEnrolmentAccounts).
	accountAllow string
	accountWhy   string
	// local is this machine's project config as the file holds it, path and
	// old being the file itself: every local write below lands in that one
	// document.
	local *localcfg.Config
	path  string
	old   []byte
	// imageEntry is the catalog entry the repository's job runs, imageKey the
	// repo_images key it is written under (provider/owner/name, S3) and imageWhy
	// the line for a step that writes no mapping.
	imageEntry string
	imageKey   string
	imageWhy   string
	// imageMap is the map this machine's file will hold: the project's effective
	// map, MIGRATED (R13) so every key of it carries its provider, with this
	// repository's own key written into it — and exactly imageNow when the step
	// writes no key, because a migration rides the write and never starts one.
	// imageNow is the map the file itself holds today — its repo_images where it
	// writes one, its legacy image_map otherwise — so the step can say whether
	// the document moves at all: the write is the difference between the two, and
	// the member it replaces is named at the write.
	imageMap map[string]string
	imageNow map[string]string
	// imageMigrated is true when the project's map was still the legacy one and
	// this step's write states it under provider keys for the first time, and
	// imageDropped is the one line per name that migration could not carry (R1).
	// Both are set only by that write: a step that wrote no map migrated none.
	imageMigrated bool
	imageDropped  []string
	// imageSeeded is true when the map comes from the published node because
	// this file names none: the note below says why that matters.
	imageSeeded bool
	// allow is the published allow-list as the read returned it, and allowWhy
	// the step's own line.
	allow    []string
	allowWhy string
	// checkout is the repository's checkout on this machine: the command runs
	// from it, because its fugaro.yaml is where its workflows and its base
	// branch come from and a job is deployed from one (D2401).
	checkout string
	// anchor is the gcp_project line that checkout's own fugaro.yaml lacks, as
	// the read prepared it: the plan prints the line, and the write offers it
	// through writeAnchor's own gate (R8). nil when there is nothing to offer —
	// the line is already there, or anchorNote says why none is (a runs bucket
	// that is not the conventional name, or a file shape that leaves the line
	// to a person).
	anchor *anchorEdit
	// anchorNote is that why, printed under the plan's first step.
	anchorNote string
	// starterPR is the pull request this run opened for a missing fugaro.yaml;
	// the enrolment stops there.
	starterPR string
}

// runProjectReposEnroll is the convergent path: read, show, ask once, write.
//
// The order is D2414's and every refusal precedes the confirmation, so a yes
// is never followed by a failure that should have come first: the wildcard's,
// the budget and the project's admin gate, the deployed-rules read, then the
// reads the plan is made of, then the one question. A coding agent's session
// carries no refusal of its own here — D3425 lifted it, the write being an edit
// (D3424) — and the confirmation is what it meets. The one exception is the
// starter: it opens a pull request in the operator's own repository, and this
// confirmation is never reached on that path, so it keeps a refusal of its own
// (enrolOpenStarter).
func runProjectReposEnroll(cmd *cobra.Command, o *projectReposOptions, arg string) error {
	ctx := cmd.Context()
	// `*` is the allow-list's own entry for every repository and not a
	// repository: it has no provider, no checkout, no workflow list and no
	// image, so there is nothing here to converge (D2404). Refused rather than
	// silently doing the narrow thing, and the flag that writes exactly that
	// wide list is named — a person who typed `*` meant that list.
	if arg == "*" {
		return userErr("`*` is the allow-list's entry for every repository and not a repository, so there is nothing here to enrol: no provider, no checkout, no workflow list and no image mapping. `fugaro project repos add * --allow-list-only` writes the wide list and nothing else")
	}
	// openProjectBudget, not openBudget: this writes the published policy
	// node, so it is a writer like `policy publish` and takes the same two
	// refusals — the shared config is not an operator's own installation, and
	// the database must be this project's.
	lc, db, configPath, err := openProjectBudget(ctx, o.cloud, "nothing was enrolled")
	if err != nil {
		return err
	}
	// The gate `repos add` has always carried, before the deployed rules are
	// read and before the node is: the allow-list is what admits a repository
	// to the project, and F19 puts it among the project-level actions — an
	// admin of every enrolled repository still cannot widen what it is
	// admitted by.
	if err := accessGateProject(ctx, lc, db, "nothing was enrolled", "enrolling a repository"); err != nil {
		return err
	}
	// The same read `policy publish` makes first, and for the same reason: the
	// marker says the deployed rules are new enough to read what is written.
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	plan, err := reposEnrolmentRead(ctx, cmd.OutOrStdout(), o, lc, db, arg)
	if err != nil {
		return err
	}
	if plan.starterPR != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "fugaro.yaml for %s: pull request %s\nmerge it, then run `fugaro project repos add %s` again to finish\n", pluginwire.Printable(arg), safetext.Field(plan.starterPR), arg)
		return nil
	}
	reposEnrolmentPlan(cmd.OutOrStdout(), lc, plan)
	// One question for the whole convergence. Nothing on the command line
	// answers it — `--yes` is gone (F5, D2403) and `--allow-list-only` takes
	// the other path entirely — and every refusal above already happened.
	// Safe while runs are in flight (CLAUDE.md): this asks no run to finish and
	// rewrites nothing a run holds — the allow-list write is the ETag-guarded
	// one, and the stages are `fugaro update`'s.
	// The sentence is the confirmation's own and it follows --no-apply: what the
	// run is about to do is what the person answers for, and under the flag the
	// publish and the deploy are not run (D1563, D1565).
	if err := confirmTyped(cmd, bufio.NewReader(cmd.InOrStdin()), lc, false, reposEnrolmentWhat(lc, plan, *o.noApply)); err != nil {
		return err
	}
	return reposEnrolmentApply(ctx, cmd, o, lc, db, configPath, plan)
}

// reposEnrolmentRead reads everything the plan is built from. It writes
// nothing, and that is the order it exists for: every refusal lands before the
// operator is shown a plan and asked about it.
//
// out is the command's own standard output, and the one thing that reaches it
// from here is the line the starter's Bitbucket token read prints before it
// reads (enrolOpenStarter); the plan and the report are printed by the caller.
func reposEnrolmentRead(ctx context.Context, out io.Writer, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, arg string) (reposEnrolment, error) {
	var plan reposEnrolment
	ownerName, err := reposEnrolmentName(arg)
	if err != nil {
		return plan, err
	}
	plan.ownerName = ownerName
	plan.localKey = configRepoKey(lc, ownerName)
	if plan.localKey == "" {
		plan.localKey = ownerName
	}
	plan.entryNow, plan.entryKnown = configRepo(lc, ownerName)
	// The checkout comes first among the reads, because everything the plan
	// needs about the repository itself — its provider's last resort, its
	// workflow list, its base branch, its image — is read out of it, and
	// because the one refusal this raises has to name the fixing command
	// before a person reads a plan built from half of it (D2401).
	if plan.checkout = checkoutRoot(ctx, ownerName); plan.checkout == "" {
		return plan, userErr("cannot read %s here: this command enrols a repository from its own checkout — its fugaro.yaml is where its workflows and its base branch come from, and its job set is deployed from there too. Run it from a checkout of %s, or write the allow-list entry alone with `fugaro project repos add %s --allow-list-only`",
			pluginwire.Printable(ownerName), pluginwire.Printable(ownerName), ownerName)
	}
	if plan.starterPR, err = enrolOpenStarter(ctx, out, lc, ownerName, plan.checkout, plan.entryNow, plan.entryKnown); err != nil {
		return plan, err
	}
	if plan.starterPR != "" {
		return plan, nil
	}
	provider, err := reposEnrolmentProvider(ctx, ownerName, plan.entryNow, plan.entryKnown)
	if err != nil {
		return plan, err
	}
	plan.provider = provider
	canonical, err := projectpolicy.Canonical(provider, ownerName)
	if err != nil {
		return plan, userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	plan.canonical = canonical
	// The project's own file and the published node are read once, here: the
	// two local writes below land in that file, and the allow-list step is
	// read off the node.
	if plan.path, plan.local, plan.old, err = catalogConfig(ctx, o.cloud); err != nil {
		return plan, err
	}
	// The row is RE0's, and it is the one read that answers "what is this
	// repository made of": its workflows with the catalog entry each runs, its
	// base branch, its image and its accounts. See reposEnrolmentRow for why
	// it is built by reposViewRowOf rather than by newReposView.
	row, allow, err := reposEnrolmentRow(ctx, lc, db, canonical, plan.localKey)
	if err != nil {
		return plan, err
	}
	plan.row = row
	// A row with no workflow is a checkout whose fugaro.yaml names none: there
	// is no selection for a run of it to be built from, and D2401 refuses
	// rather than letting the plan show a job that serves nothing.
	if len(row.Workflows) == 0 {
		return plan, userErr("cannot read %s's workflow list, and nothing here guesses one: it is the repository's own selection (fugaro.yaml, workflows:), read out of the checkout at %s. Add one to that file and run this again, or write the allow-list entry alone with `fugaro project repos add %s --allow-list-only`",
			pluginwire.Printable(ownerName), safetext.Field(plan.checkout), ownerName)
	}
	// A workflow whose image the resolver refused is a refusal and not a
	// warning (D2401): what a run of it would be is a question nothing here
	// can answer. The resolver's own sentence names what it needs, and the
	// command that changes the mapping is named beside it where the map is
	// what chose — where the workflow's own image_name: or needs: chose, the
	// repository's file is the one that decides and the catalog's own door is
	// named instead.
	//
	// Which door it was is read off the checkout's own definitions and not off
	// the refused workflow's Source: a resolution the resolver refused carries
	// no source at all (reposViewWorkflowOf), so a branch on it would answer
	// "the map" for every refusal, including the repository's own pin.
	_, defs := reposViewWorkflowDefs(ctx, lc, plan.localKey)
	for _, w := range row.Workflows {
		if w.State == "" {
			continue
		}
		if d := defs[w.Name]; d.ImageName != "" || len(d.Needs) > 0 {
			return plan, userErr("cannot read the image %s's job runs: %s. The entry is the one the repository's own fugaro.yaml names, so `fugaro image add <entry>` adds it to this project's catalog; `fugaro project repos add %s --allow-list-only` writes the allow-list entry alone",
				w.Name, w.State, ownerName)
		}
		return plan, userErr("cannot read the image %s's job runs: %s. `fugaro image map set %s <entry>` is how this project maps a repository to an entry it holds; `fugaro project repos add %s --allow-list-only` writes the allow-list entry alone",
			w.Name, w.State, ownerName, ownerName)
	}
	plan.entry, err = reposEnrolmentEntry(lc, ownerName, row)
	if err != nil {
		return plan, err
	}
	plan.entry.ClaudeAccounts = reposEnrolmentInherited(ctx, lc, plan, plan.entry.ClaudeAccounts)
	plan.entry = reposEntryMerge(plan.entryNow, plan.entry, plan.entryKnown)
	if err := reposEnrolmentAccounts(ctx, lc, &plan); err != nil {
		return plan, err
	}
	plan.entryWhy = reposEntryWhy(plan.entryNow, plan.entry, plan.entryKnown, plan.localKey) + plan.accountWhy
	if allow.State != "" {
		return plan, userErr("%s: this command writes the published allow-list, so publish one first ('fugaro project policy publish')", allow.State)
	}
	plan.allow = allow.Entries
	plan.allowWhy = allowStepWhy(allow.Entries, canonical)
	// The node the map's step is written against, read once and after the
	// allow-list's own refusal: the migration (R13) and the provider a new key
	// is written at are both facts of the project rather than of this machine.
	node, err := reposEnrolmentPolicyNode(ctx, lc, db)
	if err != nil {
		return plan, err
	}
	if err := reposEnrolmentImage(ctx, &plan, lc, node, row); err != nil {
		return plan, err
	}
	// The anchor last of the reads, and before the plan: it is the one write
	// this command makes into the checkout it stands in, and a file it may not
	// edit refuses here, before a person has answered for the enrolment.
	if plan.anchor, plan.anchorNote, err = reposEnrolmentAnchor(lc, plan.checkout); err != nil {
		return plan, err
	}
	return plan, nil
}

// reposEnrolmentAnchor is the enrolment's anchor step: the gcp_project line the
// checkout's own fugaro.yaml lacks, which is what lets a teammate's CLI and a
// CI job find this installation without setup. It returns the edit to offer
// (nil when the line is already there) and the note that says why none is
// offered: this installation's runs bucket is not the conventional name
// (customBucketNote), or the file's shape leaves the line to a person
// (byHandError) — the two answers `update --anchor` prints as notes too.
//
// Both halves of the step call it: the read, so the plan prints the line before
// the question, and the write, for the file as it is when the question has been
// answered and the repository step — deployed from this same checkout — has
// run.
func reposEnrolmentAnchor(lc *localcfg.Config, checkout string) (*anchorEdit, string, error) {
	if note := customBucketNote(lc); note != "" {
		return nil, note, nil
	}
	a, err := prepareAnchor(lc, checkout)
	var bh *byHandError
	switch {
	case errors.As(err, &bh):
		return nil, bh.Error(), nil
	case err != nil:
		return nil, "", err
	case !a.changed:
		return nil, "", nil
	}
	return a, "", nil
}

// reposEnrolmentRow is this repository's row out of the enrolment view (RE0),
// built with the function `ls` and `status` build their rows with, so the
// workflows, the base branch, the image and the accounts this command converges
// on are the ones the two listings print.
//
// It is reposViewRowOf — the view's own row builder, which newReposView calls
// for every row it holds — rather than newReposView, for one reason: the
// repository this command is about is usually enrolled in neither half yet
// (that is what `add` is for), and the view holds a row only for what is
// enrolled, so a repository with neither half would come back with no row,
// no workflows and an image resolved for a name nothing reads. The row the
// view would build for it once this command has written its entry is the row
// this is: the same function, the same project half, the same checkout.
//
// localKey is this machine's own key where it holds the repository and the
// operator's name otherwise — which is exactly the key the entry about to be
// written is stored under — so the checkout's fugaro.yaml and its base branch
// are read from the same place `ls` reads them.
func reposEnrolmentRow(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, canonical, localKey string) (reposViewRepo, reposViewAllow, error) {
	allow, cat, imap, projectBase, err := reposViewAllowOf(ctx, lc, db)
	if err != nil {
		return reposViewRepo{}, reposViewAllow{}, err
	}
	return reposViewRowOf(ctx, lc, cat, imap, projectBase, canonical, localKey, allow), allow, nil
}

// configRepoKey is this machine's own key for the repository, which is the
// operator's own spelling and not always the argument's: configRepo compares
// case-insensitively, and an entry an operator wrote by hand may carry
// upper-case letters. Writing under a second spelling would leave one
// repository with two entries and no reader able to say which is the one.
func configRepoKey(lc *localcfg.Config, ownerName string) string {
	if _, ok := lc.Repos[ownerName]; ok {
		return ownerName
	}
	folded := task.ASCIILower(ownerName)
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if task.ASCIILower(name) == folded {
			return name
		}
	}
	return ""
}

// reposEnrolmentName is the operator's argument as owner/name, with the
// provider segment dropped when the name was written canonically. A path of
// more than two segments is kept whole — a nested group is a repository path
// like any other, and it is what the allow-list matches and what the config
// keys by.
func reposEnrolmentName(arg string) (string, error) {
	parts := strings.Split(strings.Trim(task.ASCIILower(arg), "/"), "/")
	switch {
	case len(parts) == 2 && parts[0] != "" && parts[1] != "":
		return parts[0] + "/" + parts[1], nil
	case len(parts) > 2:
		if _, ok := providerKind(parts[0]); ok {
			return strings.Join(parts[1:], "/"), nil
		}
		return strings.Join(parts, "/"), nil
	}
	return "", userErr("%q is not a repository: write it as provider/owner/name (github/acme/app), or as owner/name for a repository this machine's config lists", arg)
}

// providerKind reports whether a segment names a git provider this program
// knows, and answers its canonical spelling.
func providerKind(kind string) (string, bool) {
	if slices.Contains(config.Providers, kind) {
		return kind, true
	}
	return "", false
}

// reposEnrolmentProvider is the repository's git provider, read and never
// guessed: the config's own entry first (an enrolled repository keeps the
// provider it was enrolled with), then the checkout's fugaro.yaml, then the
// checkout's origin host. A repository with none of the three is refused, and
// the sentence names the one command that supplies it.
func reposEnrolmentProvider(ctx context.Context, ownerName string, entry localcfg.Repo, known bool) (string, error) {
	if known && entry.Provider != "" {
		return entry.Provider, nil
	}
	if cfg := checkoutConfig(ctx, ownerName); cfg != nil {
		if p, ok := providerKind(cfg.Git.Provider); ok {
			return p, nil
		}
	}
	if p, ok := providerFromOrigin(ctx); ok {
		return p, nil
	}
	return "", userErr("cannot tell which git provider %s is on, and nothing here reads it: run this from its checkout, whose origin remote says which, or write it with 'fugaro settings set repos.%s.provider github' and run this again",
		pluginwire.Printable(ownerName), ownerName)
}

// providerFromOrigin is the provider of the checkout in the working
// directory, read from its origin remote's host: the one remote read this
// program makes (originRepo), reduced to the host, against the two providers
// the remote parsers know.
func providerFromOrigin(ctx context.Context) (string, bool) {
	out, err := gitCmd(ctx, ".", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}
	switch host := originHost(strings.TrimSpace(string(out))); {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return "github", true
	case host == "bitbucket.org" || strings.HasSuffix(host, ".bitbucket.org"):
		return "bitbucket", true
	}
	return "", false
}

// originHost is the host of a remote URL written https, ssh or scp-like:
// repoFromOrigin's three spellings, reduced to the host alone.
func originHost(origin string) string {
	s := origin
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
	} else if i := strings.Index(s, "@"); i >= 0 {
		s = strings.Replace(s[i+1:], ":", "/", 1)
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return task.ASCIILower(s)
}

// reposEnrolmentEntry is the `repos:` entry the file must hold, and the line
// the plan prints for it.
//
// The workflow list is the repository's own and comes from the row, which reads
// the checkout's fugaro.yaml: it is the repository's own statement, and a
// converge that guessed one would write a selection no run reads. The rest is
// read from the project — the App ID the project's other GitHub repositories
// are onboarded with, and the account they name — or carried as the entry
// already has it.
func reposEnrolmentEntry(lc *localcfg.Config, ownerName string, row reposViewRepo) (localcfg.Repo, error) {
	next := localcfg.Repo{Provider: row.Provider, BaseBranch: row.BaseBranch}
	for _, w := range row.Workflows {
		next.Workflows = append(next.Workflows, w.Name)
	}
	if next.Provider == "github" {
		appID, ok := projectGitHubAppID(lc, ownerName)
		if !ok {
			// The App ID is what a GitHub repository's job is built from, so
			// an entry written without one would deploy a job no run can
			// reach. A project with no GitHub repository to read it from is
			// the state this refuses, and the command that supplies it is
			// named rather than a number guessed (D2401).
			return next, userErr("cannot read the GitHub App ID %s's job is built from: no other repository of project %s names one, and this command never picks a number. Write it with 'fugaro settings set repos.%s.github_app_id <id>' and run this again, or write the allow-list entry alone with `fugaro project repos add %s --allow-list-only`",
				pluginwire.Printable(ownerName), pluginwire.Printable(lc.Name), ownerName, ownerName)
		}
		next.GitHubAppID = appID
	}
	next.ClaudeAccounts = projectClaudeAccounts(lc, ownerName)
	return next, nil
}

// accountUncovered reports whether account is a named Claude account this
// machine's config defines whose own allow_data_to does not name the
// repository: the one state the publish refuses (projectpolicy's
// repoAccountCovers). The default account, the axis's non-account values and
// an account nothing defines are other refusals with other fixes and are not
// this one.
func accountUncovered(lc *localcfg.Config, account, ownerName string) bool {
	if account == config.DefaultClaudeAccount || config.AxisNonAccountValue(account) {
		return false
	}
	acct, defined := lc.ClaudeAccounts[account]
	return defined && !acct.AllowsData(ownerName)
}

// repoAccountGap is one repository entry that names a named account whose own
// allow_data_to does not name it: what the publish refuses, found before it is
// run.
type repoAccountGap struct{ repo, account string }

// repoAccountGaps lists every such entry of this machine's project config, in
// the order of the repositories and of each entry's accounts.
func repoAccountGaps(lc *localcfg.Config) []repoAccountGap {
	var gaps []repoAccountGap
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		for _, a := range lc.Repos[repo].ClaudeAccounts {
			if accountUncovered(lc, a, repo) {
				gaps = append(gaps, repoAccountGap{repo, a})
			}
		}
	}
	return gaps
}

// fix is the one command that closes a gap, and the
// alternative, named so the person chooses where the token may go.
func (g repoAccountGap) fix() string {
	return fmt.Sprintf("`%s` lets the account serve it; or take %s out of repos.%s.claude_accounts in this machine's project config",
		accountsAllowCommand(g.account, []string{g.repo}, true), pluginwire.Printable(g.account), pluginwire.Printable(g.repo))
}

// repoDeclaredAccount is the named account the repository's own fugaro.yaml
// asks for (agent.oauth_token), which is the repository owner's own statement
// of the account its runs use and the only thing that lets this command
// extend an account's trust list. "" when the checkout names none.
func repoDeclaredAccount(ctx context.Context, plan reposEnrolment) string {
	if cfg := checkoutConfig(ctx, plan.localKey); cfg != nil {
		return cfg.Agent.OAuthToken
	}
	return ""
}

// reposEnrolmentInherited drops, from the accounts read off the project's other
// repositories, the ones that do not cover this repository and that its own
// file does not name. That read is a guess about a repository the account's
// list was never told about, and an entry carrying it is one the next publish
// refuses; the list is a credentials trust list, so the guess is dropped and
// never turned into a widening. An account the entry already holds is not a
// guess and is judged by reposEnrolmentAccounts.
func reposEnrolmentInherited(ctx context.Context, lc *localcfg.Config, plan reposEnrolment, accounts []string) []string {
	declared := repoDeclaredAccount(ctx, plan)
	return slices.DeleteFunc(slices.Clone(accounts), func(a string) bool {
		return accountUncovered(lc, a, plan.ownerName) && a != declared && !slices.Contains(plan.entryNow.ClaudeAccounts, a)
	})
}

// reposEnrolmentAccounts decides, for the entry the file will hold, whether
// every named account it lists covers the repository, so that this command
// never leaves a config the next `fugaro update` refuses.
//
// An account the repository's own file names (agent.oauth_token) is the
// repository owner's statement, and the person running this command is the
// project's own, past the same gate `fugaro accounts allow` stands behind, so
// the repository is added to that one account's allow_data_to in the same
// write. Any other uncovered account is refused with nothing written and the
// one command that grants it: this command does not decide where a token may
// go on a guess.
func reposEnrolmentAccounts(ctx context.Context, lc *localcfg.Config, plan *reposEnrolment) error {
	declared := repoDeclaredAccount(ctx, *plan)
	for _, a := range plan.entry.ClaudeAccounts {
		if !accountUncovered(lc, a, plan.ownerName) {
			continue
		}
		if a != declared {
			return userErr("nothing was written: repos.%s.claude_accounts names the account %s, whose allow_data_to does not name the repository, and the repository's own fugaro.yaml does not ask for it, so the next `fugaro update` would refuse the config. Give the account to the repository with `%s`, or take it out of repos.%s.claude_accounts in this machine's project config, then run this again",
				pluginwire.Printable(plan.ownerName), pluginwire.Printable(a),
				accountsAllowCommand(a, []string{plan.ownerName}, true), pluginwire.Printable(plan.ownerName))
		}
		plan.accountAllow = a
		plan.accountWhy = fmt.Sprintf(", and adds %s to claude_accounts.%s.allow_data_to, because its fugaro.yaml names that account (agent.oauth_token)",
			pluginwire.Printable(plan.ownerName), pluginwire.Printable(a))
	}
	if plan.accountAllow == "" {
		for _, a := range projectClaudeAccounts(lc, plan.ownerName) {
			if accountUncovered(lc, a, plan.ownerName) && !slices.Contains(plan.entry.ClaudeAccounts, a) {
				plan.accountWhy = fmt.Sprintf("; the account %s the project's other repositories name is left off it: its allow_data_to does not name this repository and its fugaro.yaml does not ask for it (`%s` grants it)",
					pluginwire.Printable(a), accountsAllowCommand(a, []string{plan.ownerName}, true))
			}
		}
	}
	return nil
}

// projectGitHubAppID is the App ID the project's other GitHub repositories
// are onboarded with, which is the only place this command may read one from.
// Two different IDs is no answer: it reads one and picks between none.
func projectGitHubAppID(lc *localcfg.Config, ownerName string) (string, bool) {
	ids := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "github" && r.GitHubAppID != "" && !sameRepoName(name, ownerName) {
			ids[r.GitHubAppID] = true
		}
	}
	if len(ids) != 1 {
		return "", false
	}
	return slices.Sorted(maps.Keys(ids))[0], true
}

// projectClaudeAccounts is the account the project's other repositories name,
// and only when there is exactly one to read: none or several is not an answer
// this command may pick, and an entry that names none is the ordinary state —
// its runs are served by its profile's accounts.
func projectClaudeAccounts(lc *localcfg.Config, ownerName string) []string {
	accounts := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if sameRepoName(name, ownerName) {
			continue
		}
		for _, a := range lc.Repos[name].ClaudeAccounts {
			accounts[a] = true
		}
	}
	if len(accounts) != 1 {
		return nil
	}
	return slices.Sorted(maps.Keys(accounts))
}

// sameRepoName reports whether two config keys name the same repository, one
// of them possibly written with its provider in front: this file writes the
// bare owner/name, and an entry an operator wrote by hand may carry either.
func sameRepoName(a, b string) bool {
	return bareRepoName(a) == bareRepoName(b)
}

// bareRepoName is a config key without its provider segment, when it has one.
func bareRepoName(name string) string {
	parts := strings.Split(task.ASCIILower(name), "/")
	if len(parts) > 2 {
		if _, ok := providerKind(parts[0]); ok {
			return strings.Join(parts[1:], "/")
		}
	}
	return task.ASCIILower(name)
}

// reposEntryMerge is the entry the file will hold: the one it holds now, with
// every key this command resolved stated over it. It is a merge and not a
// replacement because the entry carries keys this command does not own —
// Vertex and CredentialsServiceReady are `fugaro update`'s records, written
// from what the repository's own file declares — and an entry rebuilt from the
// plan alone would take them away with it.
func reposEntryMerge(old, next localcfg.Repo, known bool) localcfg.Repo {
	if !known {
		return next
	}
	out := old
	out.Provider = next.Provider
	if next.BaseBranch != "" {
		out.BaseBranch = next.BaseBranch
	}
	if len(next.Workflows) > 0 {
		out.Workflows = next.Workflows
	}
	if next.GitHubAppID != "" {
		out.GitHubAppID = next.GitHubAppID
	}
	if len(next.ClaudeAccounts) > 0 {
		out.ClaudeAccounts = next.ClaudeAccounts
	}
	return out
}

// reposEntryWhy is the step's line: what the entry will hold, and whether the
// write moves the file at all. It is read off the two entries rather than off
// the plan, because the question a reader has is exactly that.
func reposEntryWhy(old, next localcfg.Repo, known bool, ownerName string) string {
	have := "repos." + ownerName + ": " + reposEntryLine(next)
	switch {
	case !known:
		return "writes " + have
	case reposEntryEqual(old, next):
		return "already " + have
	}
	return "updates " + have
}

// reposEntryEqual reports whether the two entries state the same thing for
// every key this command writes.
func reposEntryEqual(a, b localcfg.Repo) bool {
	return a.Provider == b.Provider && a.BaseBranch == b.BaseBranch &&
		slices.Equal(a.Workflows, b.Workflows) && a.GitHubAppID == b.GitHubAppID &&
		slices.Equal(a.ClaudeAccounts, b.ClaudeAccounts)
}

// reposEntryLine is one entry as a line: the keys it holds, in the order the
// file's own scalars are read in.
func reposEntryLine(r localcfg.Repo) string {
	var parts []string
	for _, f := range []struct {
		key   string
		value string
	}{
		{"provider", r.Provider},
		{"base_branch", r.BaseBranch},
		{"workflows", enrolListLine(r.Workflows)},
		{"github_app_id", r.GitHubAppID},
		{"claude_accounts", enrolListLine(r.ClaudeAccounts)},
	} {
		if f.value != "" {
			parts = append(parts, f.key+" "+pluginwire.Printable(f.value))
		}
	}
	if len(parts) == 0 {
		return "{}"
	}
	return strings.Join(parts, ", ")
}

// enrolListLine is a list as the entry's line prints it, "" when it holds
// nothing.
func enrolListLine(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return "[" + strings.Join(ss, "] [") + "]"
}

// reposEnrolmentPolicyNode is the published policy node, read for the two halves
// of step 3 that are the project's rather than this machine's: the map's
// migration (R13), which rewrites the node's own allow_repos admits into keys,
// and the provider repoImageKey writes one from.
//
// A node that is not there answers nil: every path to here has already refused
// that state, which is the allow-list step's own state sentence above. A read
// that FAILS is returned, and never read as an absence: a node this command
// could not reach holds the project's whole map, and a step that took it for an
// empty one would write a one-entry file, which the keep rule serves whole.
func reposEnrolmentPolicyNode(ctx context.Context, lc *localcfg.Config, db *rtdb.Client) (*projectpolicy.Policy, error) {
	_, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return publishedPolicyOf(raw, true)
}

// fileImageMap is the map this machine's project config names itself — one of
// the two members: its repo_images where it writes one, the legacy image_map
// otherwise. It is the FILE's map and not the effective one, the published
// node's being no part of this document, which is what the write compares the
// file it is about to replace against.
func fileImageMap(lc *localcfg.Config) map[string]string {
	if lc.RepoImages != nil {
		return lc.RepoImages
	}
	return lc.ImageMap
}

// imageMapMigratedLine is the one sentence step 3 answers the migration with,
// printed under the plan's line and again where the file is written: one fact,
// one sentence (R11).
const imageMapMigratedLine = "note: the repository-to-image map (`repo_images`) is migrated: every mapping this project served as a bare name carries its provider now"

// reposEnrolmentImage is the catalog entry the repository's job runs — the
// row's answer, which is the same resolution `ls` and `status` print — and the
// map this machine's file will hold.
//
// A repository the map already names is left as it is: the mapping is the
// project's own statement about which entry that repository runs, and a
// converge that overwrote it with whatever this machine's catalog would choose
// would move a project setting it was not asked about.
//
// Otherwise the map is MERGED and never replaced. Under the keep rule (D1906,
// localcfg.ImageMapEffective) a file that writes a map states the project's
// whole map, so a file that names none — the state every machine is in before
// its first `fugaro image map set` — would publish a one-entry map over the
// node's and take every other repository's mapping away with it. The write
// starts from the effective map for that reason, as `fugaro image map set`'s
// does (imageMapWith, whose note says the project's map is carried into the
// file), so the node's entries survive.
//
// The migration (R13, S3) rides that write and never starts one. A project
// still serving the old member — on this machine or on the published node — has
// its mappings turned into provider keys first, so the file states the project's
// WHOLE map under the one rule: a partial repo_images beside the legacy member
// would make R2 read only the new one and drop every other mapping to the
// default (F23). A step with no key to write leaves the map alone, because
// migrating the node's map into a file that names none would state the project's
// map as this machine's own (D1906) and the next publish of this machine would
// serve that copy over a teammate's later mapping.
func reposEnrolmentImage(ctx context.Context, plan *reposEnrolment, lc *localcfg.Config, node *projectpolicy.Policy, row reposViewRepo) error {
	migrated, dropped := migrateImageMap(ctx, lc, node)
	plan.imageNow = fileImageMap(lc)
	_, byProvider := lc.RepoImagesEffective(node)
	// Migrated is what MOVED and not what was read: a project whose effective
	// map is the legacy one but empty has nothing to carry over, and the plan
	// must not answer a migration it did not make.
	projectLegacy := !byProvider && len(migrated) > 0
	if migrated == nil && plan.local.ImageMap != nil {
		// This file writes a map that names nothing, which the keep rule reads
		// as "nothing is mapped on purpose" — it is how a published map is taken
		// away. The migration carries that statement into the new member rather
		// than dropping it: a file that names neither member reads the node's
		// map again, and a mapping someone cleared would come back.
		migrated = map[string]string{}
	}
	// The map the file holds while this step writes no new key: exactly what it
	// holds now, so a file that names none keeps naming none. Only the write
	// below moves it.
	plan.imageMap = plan.imageNow
	plan.imageSeeded = plan.imageNow == nil && len(migrated) > 0
	// The provider this command already resolved for the repository
	// (reposEnrolmentProvider): the key's kind is the allow-list entry's kind,
	// read once and written once, so a project's config entry that names no
	// provider beside an allow_repos admit at another still gets the key of the
	// repository the command is actually enrolling. R1's first-hit resolution,
	// the checkout included, is what answered it.
	key, err := repoImageKey(ctx, lc, node, plan.ownerName, plan.provider)
	if err != nil {
		return err
	}
	plan.imageKey = key
	if e, ok := migrated[key]; ok {
		plan.imageEntry = e
		plan.imageWhy = "already repo_images[" + key + "] = " + pluginwire.Printable(e)
		return nil
	}
	if row.ImageEntry == "" {
		// No catalog entry chose this job's image: it runs the installation's
		// base image, the shared dependency layer, or the image this project
		// builds for the repository itself (reposViewImageSource). There is
		// nothing to map, and the step says which of them it is rather than
		// leaving the line empty.
		plan.imageWhy = "left out: this job's image is not a catalog entry's (" + string(row.ImageSource) + "), so there is no mapping to write"
		return nil
	}
	plan.imageEntry = row.ImageEntry
	plan.imageMap = enrolImageMap(migrated, key, row.ImageEntry)
	plan.imageWhy = "writes repo_images[" + key + "] = " + pluginwire.Printable(row.ImageEntry)
	if plan.imageSeeded {
		plan.imageWhy += " (from the map the published node holds, which this file does not name)"
	}
	// The migration's sentence and the names it could not carry belong to the
	// write that states them, and to no other: a step that wrote no map lost
	// nothing and migrated nothing.
	plan.imageMigrated = projectLegacy
	plan.imageDropped = dropped
	return nil
}

// enrolImageMap is the map the file will hold: the effective map — this
// machine's where it names one, the published node's otherwise, migrated either
// way — with this one repository's key set. It is imageMapWith's rule (copy,
// then set one key) over the map the step is merging into, which is the whole of
// why the step reads the project before it writes the file.
func enrolImageMap(now map[string]string, key, entry string) map[string]string {
	m := make(map[string]string, len(now)+1)
	maps.Copy(m, now)
	m[key] = entry
	return m
}

// allowStepWhy is the allow-list step's line: the entry the node already
// holds, or the list the write leaves.
func allowStepWhy(have []string, canonical string) string {
	if slices.Contains(have, canonical) {
		return "already on the allow-list of " + projectpolicy.PathShared()
	}
	after := append(slices.Clone(have), canonical)
	slices.Sort(after)
	return fmt.Sprintf("allow_repos: %v → %v", have, after)
}

// reposEnrolmentPlan prints the whole list of what the command will change, in
// the order the enrolment is read in: the repository's own fugaro.yaml, the
// local config's entry, the image map, the allow-list, the publish, and the
// job with its invoker grant (D2400).
//
// It is printed BEFORE the one question and it is the whole of what the
// question is about: a plan that named fewer steps than the command runs would
// make the yes it asks for a yes to something else.
func reposEnrolmentPlan(w io.Writer, lc *localcfg.Config, plan reposEnrolment) {
	fmt.Fprintf(w, "enrolling %s in project %s: this will change\n", pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name))
	lines := []string{
		"the repository's own fugaro.yaml: read for its workflows (" + enrolListLine(workflowNames(plan.row)) + ") and its base branch (" + safetext.Field(plan.row.BaseBranch) + "), and written only for the anchor line, through the gate, in that repository's own checkout",
		"project config: " + plan.entryWhy,
		"project config: " + plan.imageWhy,
		"allow-list: " + plan.allowWhy,
		"publish: the policy node " + projectpolicy.PathShared() + ", from this machine's config (fugaro update's policy stage)",
		"job: the repository's job set and its roles/run.invoker grant on the credentials service, from the checkout at " + safetext.Field(plan.checkout),
	}
	// The anchor line sits under the step that writes it: the first step writes
	// one thing, and a plan that said only "the anchor line" would leave a
	// person answering for a line they never read. A file whose shape leaves
	// the line to them gets the note instead of a line nothing will write.
	//
	// Step 3 gets the migration's sentence for its own reason: "writes
	// repo_images[...]" is one mapping, and the step may also move every mapping
	// the project serves onto a provider key — the same thing the answer will
	// say the command did, said before it does it.
	notes := map[int]string{}
	switch {
	case plan.anchor != nil:
		notes[0] = "writes " + plan.anchor.line() + " (so teammates use this installation without setup)"
	case plan.anchorNote != "":
		notes[0] = "note: " + plan.anchorNote
	}
	if plan.imageMigrated {
		notes[2] = imageMapMigratedLine
	}
	for i, l := range lines {
		fmt.Fprintf(w, "  %d. %s\n", i+1, l)
		if n := notes[i]; n != "" {
			fmt.Fprintf(w, "     %s\n", n)
		}
	}
	// A name the migration could not carry is a mapping the project loses, and
	// the plan is where a person answering for the step reads it: one line per
	// name, in the migration's own wording (R11).
	for _, l := range plan.imageDropped {
		fmt.Fprintf(w, "  %s\n", l)
	}
	// The build is named because it is the one thing enrolment needs and this
	// command does NOT do: the question above covers the six steps, and a
	// person who read "the job" as "its image too" would be surprised by a
	// bill nobody approved. `fugaro update --repo` leaves the same build for
	// the same reason.
	fmt.Fprintf(w, "the first image build is not part of this: it is billable and it waits for `fugaro image build <entry>`\n")
}

// workflowNames is the row's workflow names, for the plan's first line.
func workflowNames(row reposViewRepo) []string {
	names := make([]string, 0, len(row.Workflows))
	for _, w := range row.Workflows {
		names = append(names, w.Name)
	}
	return names
}

// reposEnrolmentWhat is the sentence the one question is about. noApply is
// --no-apply (D1565), and it changes the last clause only: under the flag the
// publish and the deploy are saved in this machine's record for `fugaro apply`
// rather than run here, and a question that named them as things about to
// happen would be asking about a run this invocation does not make.
func reposEnrolmentWhat(lc *localcfg.Config, plan reposEnrolment, noApply bool) string {
	tail := "publish the node from this machine's config, and converge and deploy its own root"
	if noApply {
		tail = "and save the publish of the node and the deploy of its own root for fugaro apply to run later"
	}
	return fmt.Sprintf("enrol %s in project %s: write its repos: entry and its image mapping, add it to the published allow-list, %s",
		pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name), tail)
}

// reposEnrolmentApply runs the steps the plan printed, in that order. Each is
// idempotent and each says what it did: a repository already enrolled converges
// to the same state, and a step with nothing to do says so rather than writing
// the same bytes again (D2402).
//
// The first step is the read the plan was built from and has nothing to write;
// the second and third land in one document; the fourth is the ETag-guarded
// policy write; and the last two are `fugaro update`'s stages.
//
// The anchor is not a step of that numbered list: it is the file edit step 1
// prints, so both paths make it — after the converge where one runs, and before
// the return where --no-apply saves the stages for `fugaro apply` instead.
func reposEnrolmentApply(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, configPath string, plan reposEnrolment) error {
	out := cmd.OutOrStdout()
	if err := reposEnrolmentWriteLocal(out, plan); err != nil {
		return err
	}
	if err := reposEnrolmentWriteAllow(ctx, out, configPath, lc, db, plan); err != nil {
		return err
	}
	// Steps 5 and 6 are `fugaro update`'s stages, and what they read is this
	// machine's config file. Step 4 wrote the published node, and that write
	// moves the generation the file's policy keys are read at, so it records the
	// generation it landed in the file (putAllowRepos, D854). A config read
	// before that record — the one the four steps above were made from — states a
	// generation the node has moved past, and the policy stage answers that state
	// with D854's re-seed of the file from the node rather than publishing, which
	// takes the two local steps above with it. So the stages are handed the file
	// read back now: the config every write of this command has left.
	after, err := localcfg.Load(plan.path)
	if err != nil {
		return userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	// The change's own follow-up (AP6), recorded here and not earlier: what the
	// record remembers is the steps that make the enrolment live, and a record
	// written before the writes above would name steps for an enrolment that was
	// never made — the same order `accounts rm` writes its own in (D1014's shape,
	// and the four steps above are this command's change). It is written before
	// the converge and not after it, because the converge is minutes of Terraform
	// and one that died in the middle would leave an enrolment nothing remembers
	// is not live (D1562, D1563).
	names, err := chainSteps(reposEnrolmentChanges())
	if err != nil {
		return err
	}
	queued, err := chainSave(after, reposEnrolmentChanges(), names)
	if err != nil {
		return err
	}
	if *o.noApply {
		chainSavedLine(out, names)
		// The anchor is not one of the stages, so --no-apply does not save it for
		// `fugaro apply`: the file edit the plan promised is made here, and it is
		// the line the whole enrolment exists to add. Nothing is deployed from
		// this checkout under the flag, so the order the stage path needs
		// (reposEnrolmentStages) has nothing to guard here.
		if err := reposEnrolmentAnchorWrite(ctx, cmd, after, plan); err != nil {
			return fmt.Errorf("the enrolment is saved for fugaro apply; only the gcp_project line of its fugaro.yaml was not written (%w)", err)
		}
		return nil
	}
	return reposEnrolmentStages(ctx, cmd, o, after, plan, queued, names)
}

// reposEnrolmentChanges is the change `project repos add` makes, as apply's
// table is keyed: a repository of this project. One row, whichever half of the
// enrolment a run makes — the repos: entry, the image mapping and the published
// allow-list entry are one change to the installation, and the steps that make
// it live are the node the admission is published in, the job set that serves
// the repository and the repository's own root (ResourceRepo's own comment).
func reposEnrolmentChanges() []apply.Change {
	return []apply.Change{{Kind: apply.Resource, Key: apply.ResourceRepo}}
}

// reposEnrolmentWriteLocal is steps 2 and 3: the `repos:` entry and the image
// map, in this machine's project config. They land in one document, so the
// second write is made from the file the first one left, and a step with
// nothing to change writes nothing at all (userSave is not reached).
//
// The file is read back through the loader when both writes are done, which is
// the one read that says the document is what this command thinks it is: a file
// this command left unparseable is refused here, before step 4 writes the
// published node against it. What the stages below read is read again, after
// that write (reposEnrolmentApply).
func reposEnrolmentWriteLocal(out io.Writer, plan reposEnrolment) error {
	cur, old := plan.local, plan.old
	if !reposEntryEqual(plan.entryNow, plan.entry) || plan.accountAllow != "" {
		next := *cur
		next.Repos = maps.Clone(cur.Repos)
		if next.Repos == nil {
			next.Repos = map[string]localcfg.Repo{}
		}
		next.Repos[plan.localKey] = plan.entry
		if a := plan.accountAllow; a != "" {
			next.ClaudeAccounts = maps.Clone(cur.ClaudeAccounts)
			acct := next.ClaudeAccounts[a]
			acct.AllowDataTo = append(slices.Clone(acct.AllowDataTo), plan.ownerName)
			next.ClaudeAccounts[a] = acct
		}
		if err := userSave(out, plan.path, old, &next); err != nil {
			return err
		}
		cur = &next
		fmt.Fprintf(out, "repos.%s: %s\n", plan.localKey, reposEntryLine(plan.entry))
		if a := plan.accountAllow; a != "" {
			fmt.Fprintf(out, "claude_accounts.%s.allow_data_to: + %s (its fugaro.yaml names the account, agent.oauth_token)\n", pluginwire.Printable(a), pluginwire.Printable(plan.ownerName))
		}
	} else {
		fmt.Fprintf(out, "%s is already as this enrolment wants it\n", safetext.Field(plan.path))
	}
	// A step with no key to write states no map: the migration rides the write
	// and the file that names no map keeps naming none (reposEnrolmentImage).
	if plan.imageEntry != "" && !maps.Equal(plan.imageNow, plan.imageMap) {
		next := *cur
		next.RepoImages = plan.imageMap
		// The legacy member goes with the write, which is what "never writes
		// image_map" means in a file that held one: the map is stated once, under
		// the member every reader of this release reads first (R2, S3).
		//
		// The publish this command runs carries repo_images (U1b, R14), so the
		// mapping reaches the node every teammate and the credentials service
		// read. The node's own legacy member is kept there until the step after
		// the credentials service takes it away (D3376), and the two name the
		// same repository: the key carries the provider the file now does.
		next.ImageMap = nil
		// The bytes the first write left, re-read rather than re-marshalled:
		// userSave's old is the file the write replaces, and a marshal of the
		// parsed value is not the same bytes as the document it was parsed from.
		data, err := os.ReadFile(plan.path)
		if err != nil {
			return userErr("reading %s: %v", safetext.Field(plan.path), err)
		}
		if err := userSave(out, plan.path, data, &next); err != nil {
			return err
		}
		fmt.Fprintf(out, "repo_images[%s] = %s\n", plan.imageKey, pluginwire.Printable(plan.imageEntry))
		if plan.imageMigrated {
			fmt.Fprintf(out, "%s\n", imageMapMigratedLine)
		}
		// A dropped name is a mapping the project loses, and this is the step
		// that lost it: the line the plan printed is printed again where it
		// happened, in the migration's own wording.
		for _, l := range plan.imageDropped {
			fmt.Fprintf(out, "%s\n", l)
		}
		if plan.imageSeeded {
			fmt.Fprintf(out, "note: this machine's project config named no repo-to-image map, so the mappings the published node holds are written into this file too (a file that states a map states the whole one)\n")
		}
	}
	if _, err := localcfg.Load(plan.path); err != nil {
		return userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	return nil
}

// reposEnrolmentWriteAllow is step 4: one entry in the published allow-list,
// through the same ETag-guarded write every other editor of the node uses
// (putAllowRepos). The node is re-read here rather than taken from the plan:
// the plan was printed for a person to read, and the ETag the write is guarded
// by has to be the one the node holds now.
//
// An entry the node already holds is step 4's idempotent case, and the sentence
// is `runProjectReposEdit`'s own for the same state (D2402, the format string
// at project_enroll.go:512): the two paths are one group, and an operator reads
// one sentence for one fact.
func reposEnrolmentWriteAllow(ctx context.Context, out io.Writer, configPath string, lc *localcfg.Config, db *rtdb.Client, plan reposEnrolment) error {
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return userErr("the policy node %s is gone: %s and this machine's config were written, and the allow-list entry was not — publish one ('fugaro project policy publish') and run this again", projectpolicy.PathShared(), pluginwire.Printable(plan.ownerName))
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	before := append([]string{}, nonNil(p.AllowRepos)...)
	if slices.Contains(p.AllowRepos, plan.canonical) {
		fmt.Fprintf(out, "%s is already on the allow-list; nothing changed\n", plan.canonical)
		return nil
	}
	allow := append(nonNil(p.AllowRepos), plan.canonical)
	slices.Sort(allow)
	return putAllowRepos(ctx, out, configPath, lc, db, etag, raw, before, nonNil(allow))
}

// reposEnrolmentStages is steps 5 and 6: the publish and the deploy, through
// `fugaro update`'s own registered stages and nothing else.
//
// The image build is off (noBuild), and that is the whole of the reason the one
// confirmation is about what it is: `fugaro update --repo` offers the first
// build behind its own billable confirmation, and a single question about
// enrolment may not stand for a bill (D2400).
func reposEnrolmentStages(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, plan reposEnrolment, queued *apply.Pending, names []string) error {
	run := stepRunFor(cmd, "project-repos-add", cmd.ErrOrStderr())
	defer run.close()
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	stages, err := reposEnrolmentRunStages(ctx, cmd, run, lc, plan.path, plan.old)
	if err != nil {
		// The steps this command wrote to the record are still owed, every one of
		// them, and the sentence that finishes them goes beside the stage's own
		// error: a person who reads only the error line would re-run this command,
		// and the record would read as an enrolment nobody remembers.
		applyKeptLine(cmd.OutOrStdout(), names)
		return err
	}
	if err := reposEnrolmentDeployed(plan, stages); err != nil {
		applyKeptLine(cmd.OutOrStdout(), names)
		return err
	}
	// The anchor, and only once the repository step has deployed from this
	// checkout: that step resolves its target through the checkout's own
	// fugaro.yaml and turns down a working tree its default branch does not
	// hold (resolveRepoTarget), so a line written into the file before it would
	// be a checkout the deploy refuses.
	if err := reposEnrolmentAnchorWrite(ctx, cmd, lc, plan); err != nil {
		applyKeptLine(cmd.OutOrStdout(), names)
		return fmt.Errorf("the repository is enrolled and its job deployed; only the gcp_project line of its fugaro.yaml was not written (%w): rerunning `fugaro project repos add %s` is safe", err, plan.ownerName)
	}
	// The chain is settled before the report and not after it: the report's last
	// line is the claim that `fugaro run --repo …` is ready, and it may not be
	// printed over a step the run did not answer — a line that says the change is
	// still owed belongs above it, or nowhere (D1563, and the settle is what
	// clears the record the run above answered).
	if err := chainSettle(cmd, lc, plan.path, queued, names, stages); err != nil {
		return err
	}
	reposEnrolmentReport(cmd, lc, plan, stages)
	return nil
}

// reposEnrolmentAnchorWrite is the anchor step's write (R8): the checkout's own
// fugaro.yaml gains the gcp_project line, offered through writeAnchor's own gate
// and no new question — nothing on this command's line answers it, --ask is no
// flag of it, and a person at the terminal has read the line in the plan. A
// session with no terminal is handed the line to add instead, and a coding
// agent's session is refused, which is the gate's own arm (reposAnchorGate).
//
// The image checks run once the line is being written (gcpProjectProblems), and
// they warn rather than refuse: they are about a build record nothing here
// changes, and the enrolment they came with has already landed. Nothing is
// printed when the line is not the file's to add — it is there already, or the
// plan's own note said why it is not.
func reposEnrolmentAnchorWrite(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, plan reposEnrolment) error {
	out := cmd.OutOrStdout()
	a, _, err := reposEnrolmentAnchor(lc, plan.checkout)
	if err != nil || a == nil {
		return err
	}
	if _, err := reposAnchorGate(cmd, out).write(a); err != nil {
		return err
	}
	for _, p := range gcpProjectProblems(ctx, lc, plan.path, plan.row.Provider, plan.ownerName, workflowBases(checkoutConfig(ctx, plan.localKey), nil)) {
		fmt.Fprintln(out, questionStyle(cmd, out).Warning(p.text))
	}
	return nil
}

// reposAnchorGate is the gate this command offers the anchor line through: the
// shared one, with the facts this door holds and no others (R8). There is no
// run here, so nothing stands in for a managed project, no flag confirms the
// edit (D2403), and the session is the whole of what answers — a person at the
// terminal has read the line in the plan, and a coding agent's session is
// refused by the same arm every other step of a run refuses it with.
func reposAnchorGate(cmd *cobra.Command, out io.Writer) anchorGate {
	terminal := stdinIsTerminal(cmd.InOrStdin())
	return anchorGate{
		w:           out,
		interactive: terminal,
		confirm: func(string) (bool, error) {
			if m := agentMarker(os.Getenv, terminal); m != "" {
				return false, &initflow.AgentError{Marker: m}
			}
			return terminal, nil
		},
	}
}

// reposEnrolmentDeployed refuses a run whose last step did not happen. The
// report below says "enrolled … fugaro run --repo … is ready", and that claim is
// step 6's: the repository's job set and its roles/run.invoker grant, which the
// Repository stage deploys. A stage that neither applied nor found the work
// already done — skipped, will-do, needs-you, blocked — leaves the repository
// enrolled in the project and unable to run, and a run that printed success over
// it would be the worst of the two outcomes: the operator reads "ready" and
// finds out at the first `fugaro run --repo`.
//
// The stage's own sentence is the fix (a checkout with no default branch to read
// says which ref to fetch and which door to rerun), and every step above has
// already landed, so what this adds is where to pick up — the wording the loop's
// own failure line uses.
func reposEnrolmentDeployed(plan reposEnrolment, stages map[string]initflow.StageResult) error {
	st := stages[initflow.Repository]
	switch st.State {
	case initflow.Changed, initflow.Done:
		return nil
	}
	detail := oneLine(st.Detail)
	if detail == "" {
		detail = "the stage reported no detail"
	}
	state := string(st.State)
	if state == "" {
		state = "no result"
	}
	return userErr("nothing was deployed for %s: step 6 of this enrolment is the repository's job set and its roles/run.invoker grant, and the repository stage reported %s (%s), so it did not happen. Fix what that says and rerun `fugaro project repos add %s`: it resumes at the repository step, and everything before it is already written",
		pluginwire.Printable(plan.canonical), state, detail, plan.ownerName)
}

// reposEnrolmentRunStages invokes the Policy, Installation and Repository
// stages in-process, the way `fugaro accounts add` invokes its own
// (accountsAddRunStages, spec §2.5). The order is the registered order and not
// this file's: initflow.Validate sorts the selection into it, so the job set is
// deployed against the installation before the checkout's own root is
// converged, and the policy node — which every run of the project is admitted
// by — is published last, after the installation it is read against.
//
// The run's own confirmation is this command's, which the person has already
// answered, which is why the loop is given Yes; it carries asJSON so the stages
// and the loop print through the run's own writer, and the command's stdout is
// pointed at stderr for the length of the run, so a stage that prints to
// cmd.OutOrStdout() directly — the policy stage's diff — cannot land on the
// answer.
func reposEnrolmentRunStages(ctx context.Context, cmd *cobra.Command, run *stepRun, lc *localcfg.Config, path string, old []byte) (map[string]initflow.StageResult, error) {
	o := &initOptions{door: "project repos add", yes: true, nonInteractive: true, asJSON: true, noBuild: true}
	r := newInitRun(cmd, o)
	// The caller's account, not a second one (CL7a): the stages print one line
	// each through the reporter this command opened, and the run's own output —
	// Terraform's above all — goes to the log this command keeps.
	r.rep, r.repLog = run.rep, run.log
	r.setProject(lc)
	// The workdirs are the converge's, whichever door converged them: the
	// Installation stage rebuilds the installation's tree and the Repository
	// stage the checkout's, which is the pair `fugaro update` takes the lock
	// for. So this door takes it too — here, where the project is known and
	// before the loop prepares anything.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// installSpec and not installOptions: this door composes the same spec
	// `fugaro update` does, so a config that can no longer compose the job of a
	// default profile this installation holds must not stop it.
	spec, err := installSpec(o, lc, r.withheld)
	if err != nil {
		return nil, err
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	opts := e.options()
	// The reporter is this command's (ReportBorrowed): the loop's steps are
	// three of the six this command accounts for, and the closing line is
	// printed where this command's own run ends.
	opts.ReportBorrowed = true
	res, err := e.r.runStages(ctx, reposEnrolmentStageList(e), opts)
	cmd.SetOut(answer)
	if err != nil {
		return nil, userErr("%v", err)
	}
	if res.Failed != nil {
		return nil, res.Cause
	}
	if len(res.Left) > 0 {
		return nil, userErr("%v", res.Left[0].Text)
	}
	out := make(map[string]initflow.StageResult, len(res.Stages))
	for _, st := range res.Stages {
		out[st.Name] = st
	}
	return out, nil
}

// reposEnrolmentStageList is the run's stage list, built from initStages
// through stagesNamed — the registered list and the one helper every composite
// command names a subset with — so this command invokes the same stages
// `fugaro update` does rather than a copy of them.
//
// All three run, and the Repository stage's own condition is the checkout: a
// repository with none was refused before the plan was printed (D2401), so by
// the time this list is built the checkout this command runs from is the one
// that stage converges.
func reposEnrolmentStageList(e *initEngine) []initflow.Stage {
	stages, _ := stagesNamed(e, initflow.Policy, initflow.Installation, initflow.Repository)
	return stages
}

// reposEnrolmentReport is the command's answer: one line per step, read off
// what happened rather than off what the plan said would happen.
func reposEnrolmentReport(cmd *cobra.Command, lc *localcfg.Config, plan reposEnrolment, stages map[string]initflow.StageResult) {
	w := cmd.OutOrStdout()
	row := func(label, value string) { fmt.Fprintf(w, "%-12s%s\n", label, value) }
	row("fugaro.yaml", "read from "+safetext.Field(plan.checkout)+": workflows "+enrolListLine(workflowNames(plan.row))+", base branch "+safetext.Field(plan.row.BaseBranch))
	row("local config", plan.entryWhy)
	row("image map", plan.imageWhy)
	row("allow-list", plan.allowWhy)
	row("publish", enrolStageLine(stages[initflow.Policy], func() string {
		return "the node " + projectpolicy.PathShared() + " carries this repository's admission and this machine's config"
	}))
	row("job", enrolStageLine(stages[initflow.Installation], func() string {
		return "the project's job set (fugaro update's installation stage)"
	}))
	row("repository", enrolStageLine(stages[initflow.Repository], func() string {
		return "the checkout's own root was converged and deployed"
	}))
	if plan.imageEntry != "" {
		fmt.Fprintf(w, "enrolled %s in project %s: the one step left is the first image build — `fugaro image build %s` — and then fugaro run --repo %s is ready\n",
			pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name), pluginwire.Printable(plan.imageEntry), plan.canonical)
		return
	}
	fmt.Fprintf(w, "enrolled %s in project %s: fugaro run --repo %s is ready\n",
		pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name), plan.canonical)
}

// enrolStageLine is one stage's answer in the report: the stage's own words
// for every state but the one that applied, and the caller's sentence for that
// one.
func enrolStageLine(st initflow.StageResult, applied func() string) string {
	switch st.State {
	case initflow.Changed:
		return applied()
	case "":
		return "not run: the stage reported no result"
	}
	return string(st.State) + ": " + oneLine(st.Detail)
}
