package cli

// This file is `fugaro project repos rm`: the whole removal, and the inverse of
// the converge `fugaro project repos add` runs (project_enroll_converge.go;
// plan task RE4 of docs/repos-management, D2405–D2408).
//
// Enrolling a repository is six acts, and un-enrolling it is the same six read
// backwards, each through the writer that wrote it: the published allow-list
// entry goes through the one ETag-guarded editor (putAllowRepos), the `repos:`
// entry and the image mapping go through this machine's project config, the
// published admission `config/policy/repos/<slug>` is narrowed by the publish's
// own removal path rather than by a second writer of that node (F18), and the
// repository's Cloud Run jobs are deleted by the retirement's own label-checked
// API delete (F16). A removal that reimplemented one of the four would be the
// drift the HARD RULE cannot survive, in the one direction that loses an
// operator's trust: two writers of one document, and a delete that is not the
// checked one.
//
// What this file deliberately does NOT do is delete anything the design says
// Fugaro never deletes (D2406): a secret or a secret version, a built image, a
// run's history. Those are named, twice — before the confirmation and after the
// write — and never reached.
//
// So is what the owner's ruling of 2026-11-17 keeps (amending D2405, D2406
// standing): the repository's Terraform state and the repository root's own IAM
// members, both of which the hand-run offboarding in docs/how-to/offboarding.md
// takes away and neither of which is this command's to take away — the state is
// what that sequence's apply and destroy plan from.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// reposRemoval is everything the removal read before it changed anything, and
// everything the pre-write warning is built from. Every field is a fact read
// from a file, from the project or from the database, so the warning names what
// this run really takes away rather than what a removal of that name usually
// takes away.
type reposRemoval struct {
	// canonical is provider/owner/name: the allow-list entry this removal
	// takes away. ownerName is owner/name, the local config's key for it, the
	// image map's key and the name every job is deployed for, and localKey the
	// key this machine's config holds it under, which is the operator's own
	// spelling where it holds one.
	canonical string
	ownerName string
	localKey  string
	// slug and label are the repository's identity in the cloud: the slug the
	// state, the run bucket and the build records are keyed by (task.Slug),
	// and the label every resource of it carries (gcp.RepoLabel), which is
	// what the retirement's delete checks before it deletes anything.
	slug  string
	label string
	// allow is the published allow-list as the read returned it — the list
	// removalWarning reads, so the warning says what the next run will do —
	// and raw, etag are the node the allow-list write is made against.
	allow []string
	raw   []byte
	etag  string
	// local is this machine's project config as the file holds it, path and
	// old being the file itself: the two local removals land in that one
	// document.
	local *localcfg.Config
	path  string
	old   []byte
	// entry is the `repos:` entry this machine holds, entryKnown whether it
	// holds one at all, and entryWhy the step's own line.
	entry      localcfg.Repo
	entryKnown bool
	entryWhy   string
	// imageNow is the effective repository-to-image map (R2: this file's
	// repo_images first, then the published node's, then the legacy image_map),
	// imageNext the map the file will hold — nil when the mapping is not this
	// file's to take away — and imageOwn whether the file names the map itself.
	imageNow  map[string]string
	imageNext map[string]string
	imageOwn  bool
	// imageKey is the key the served map is READ by, which is the one a sentence
	// names: the canonical provider/owner/name a provider-keyed map is keyed by,
	// the bare owner/name the legacy map is. The key the write takes out is
	// always plan.canonical, because what the write leaves is repo_images.
	// imageDropped holds the lines a migration dropped on the way, ready to
	// print.
	imageKey     string
	imageDropped []string
	imageWhy     string
	// inflight is every run the registry holds, runs the runs of this
	// repository. D2407: a run in flight is never disturbed, so the removal
	// asks the strict confirmation and names the run.
	inflight int
	runs     []reposRemovalRun
	// stateBucket is the state bucket this machine's project config records:
	// the sign that this machine has converged the installation and knows where
	// the repository's jobs live, and the one condition under which this command
	// looks at the project's own cloud resources at all. The state it holds is
	// kept by this removal (the owner's ruling of 2026-11-17) — the bucket is
	// read for its name, never to delete anything in it.
	stateBucket string
}

// reposRemovalLeaves is what `rm` deliberately leaves (D2406), in the order the
// owner wrote them. Nothing here is reachable from any path of this command,
// and each is named because a person removing a repository otherwise believes
// the thing they care about went with it.
var reposRemovalLeaves = []string{
	"the provider's App installation (a person's, on the provider's side: removing it ends the App's access to every repository it serves)",
	"the built images (Fugaro never deletes an image, and `fugaro image rm` does not either)",
	"the secrets and their versions (a container is protected against deletion, and no command of Fugaro's deletes one)",
	"the run history in the runs bucket (it is deleted at its own expiry, never by this command)",
	"every pull request a run opened",
	"the remote branch",
}

// runProjectReposRm is the whole removal: read, warn, ask once, remove.
//
// The order is the group's (runProjectReposEdit) and every refusal precedes the
// confirmation, so a yes is never followed by a failure that should have come
// first: the coding agent's refusal, the budget and the project's admin gate,
// the deployed-rules read, then the reads the warning is made of, then the one
// question — and the same question is the strict one while a run is in flight
// (D2407), with nothing on the command line standing in for it.
func runProjectReposRm(cmd *cobra.Command, o *projectReposOptions, arg string) error {
	ctx := cmd.Context()
	// An entry that is not a repository is the entry alone: `*` is the
	// allow-list's own entry for every repository (D2404), and a pattern such
	// as github/acme/* admits repositories without being one. Neither has a
	// provider (task.Slug needs one), a local entry, a slug, a job or a state,
	// so there is nothing here but the entry itself, and the group's own edit
	// path is what removes one — the door `--allow-list-only` is on the other
	// side of the group. A removal that reached the repository machinery with
	// no repository to reach it for would be guessing at one.
	//
	// The test is the argument's own, before any read: `*` anywhere in it is a
	// pattern and not a name, and no concrete repository carries one — a
	// checkout, a fugaro.yaml and a config entry are all read through
	// canonicalRepoArg, which hands a pattern to ValidPattern and never to
	// Canonical.
	if strings.Contains(arg, "*") {
		return runProjectReposEdit(cmd, o, arg, true)
	}
	// SD8: this writes the published node's allow_repos, which is what admits
	// a repository to the project at all, and then deletes the repository's
	// jobs and its state: a person's step and never a coding agent's, before
	// every read and before any cloud write is reached. The opener is the
	// group's own — the doors' test pins "nothing was written" for all three
	// commands that write the list — with the removal's own verb beside it and
	// the list of what would have gone.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written or removed: fugaro project repos rm removes the published allow-list entry, this machine's config entry, the repository's published admission and its Cloud Run jobs, which are what admit a repository to the project and what run it: %s", initflow.AgentRefusal(m))
	}
	// openProjectBudget, not openBudget: this writes the published policy node
	// and is a writer like `policy publish`, so it takes the same two refusals
	// — the shared config is not an operator's own installation, and the
	// database must be this project's. The declined sentence is the one the
	// group's removal has always printed, and it is the ownership and admin
	// refusals' own wording (TestReposAddRefusesAnotherInstallationsDatabase).
	declined := "nothing was removed from the allow-list"
	lc, db, configPath, err := openProjectBudget(ctx, o.cloud, declined)
	if err != nil {
		return err
	}
	// The allow-list is what admits a repository to the project, and removing
	// an entry is a project-level act: an admin of the repository itself still
	// cannot take the project's admission away. Refused before the deployed
	// rules are read and before the node is.
	if err := accessGateProject(ctx, lc, db, declined, "removing a repository from the project"); err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	plan, err := reposRemovalRead(ctx, o, lc, db, arg)
	if err != nil {
		return err
	}
	// The cloud clients the cloud step uses, built before the warning and
	// before the question: a machine whose credentials cannot be minted is
	// refused with nothing written rather than halfway through the removal.
	// Nothing is read with them here — the step deletes the repository's jobs
	// and nothing else in the project.
	c, err := reposRemovalClients(ctx, lc, plan)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	what := reposRemovalWarning(plan)
	fmt.Fprintln(out, what)
	// One question for the whole removal. Nothing on the command line answers
	// it — --yes is gone (F5, D2403) — and while a run is going only the typed
	// name does (confirmRemovalStrict), because a removal takes away the job
	// that run is executing.
	if plan.inflight > 0 {
		if err := confirmRemovalStrict(cmd, lc, what+"\n"+reposRemovalInflight(plan)); err != nil {
			return err
		}
	} else if err := confirmRemoval(cmd, lc, o.yes, what); err != nil {
		return err
	}
	return reposRemovalApply(ctx, cmd, o, lc, db, configPath, c, plan)
}

// reposRemovalClients builds the clients the cloud step uses, before the
// warning and before the question, so a machine whose credentials cannot be
// minted is refused with nothing written rather than halfway through a
// removal — the property that makes this command resumable is that everything
// it writes is written after this returns, and one `rm` reads its credentials
// once.
//
// Nothing is read with them: the removal deletes the repository's Cloud Run
// jobs and its Scheduler job, and it keeps the repository's Terraform state
// (the owner's ruling of 2026-11-17, amending D2405), so there is no state to
// read, no versioning to check and no refusal here. A machine whose config
// records no state bucket has nothing for the cloud step to look at and gets
// no clients: the step says so and skips itself.
func reposRemovalClients(ctx context.Context, lc *localcfg.Config, plan reposRemoval) (*infra.Clients, error) {
	if plan.stateBucket == "" {
		return nil, nil
	}
	return newInitClients(ctx, lc)
}

// reposRemovalRead reads everything the warning is built from. It writes
// nothing, and that is the order it exists for: every refusal lands before the
// operator is shown what the removal takes away and asked about it.
func reposRemovalRead(ctx context.Context, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, arg string) (reposRemoval, error) {
	var plan reposRemoval
	ownerName, err := reposEnrolmentName(arg)
	if err != nil {
		return plan, err
	}
	plan.ownerName = ownerName
	plan.localKey = configRepoKey(lc, ownerName)
	if plan.localKey == "" {
		plan.localKey = ownerName
	}
	plan.entry, plan.entryKnown = configRepo(lc, ownerName)
	provider, err := reposRemovalProvider(ctx, arg, ownerName, plan.entry, plan.entryKnown)
	if err != nil {
		return plan, err
	}
	canonical, err := projectpolicy.Canonical(provider, ownerName)
	if err != nil {
		return plan, userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	plan.canonical = canonical
	if plan.slug, err = task.Slug(provider, ownerName); err != nil {
		return plan, userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	if plan.label, err = gcp.RepoLabel(plan.slug); err != nil {
		return plan, userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	if plan.path, plan.local, plan.old, err = catalogConfig(ctx, o.cloud); err != nil {
		return plan, err
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return plan, err
	}
	if !exists {
		return plan, userErr("no policy is published for project %s (%s): `fugaro project repos rm` edits the published allow-list, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return plan, err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return plan, err
	}
	plan.etag, plan.raw, plan.allow = etag, raw, nonNil(p.AllowRepos)
	if !slices.Contains(plan.allow, plan.canonical) {
		// A name that is not an entry can still be admitted by a pattern on the
		// list, and then the removal removes nothing and refuses nothing:
		// saying only "nothing to remove" would read as if the repository had
		// stopped running. The sentence is runProjectReposEdit's own for the
		// same state — one group, one sentence for one fact.
		for _, pattern := range plan.allow {
			if projectpolicy.MatchRepo(pattern, plan.canonical) {
				return plan, userErr("%s is not an entry of the allow-list of %s; the pattern %s admits it, so its runs keep running — remove that pattern to refuse them",
					plan.canonical, projectpolicy.PathShared(), pattern)
			}
		}
		return plan, userErr("%s is not on the allow-list of %s: nothing to remove", plan.canonical, projectpolicy.PathShared())
	}
	plan.entryWhy = reposRemovalEntryWhy(plan)
	reposRemovalImage(ctx, &plan, p)
	plan.stateBucket = lc.Terraform.StateBucket
	if plan.inflight, plan.runs, err = reposRemovalRuns(ctx, db, lc, plan.slug, ownerName); err != nil {
		return plan, err
	}
	return plan, nil
}

// reposRemovalProvider is the repository's git provider, in the group's own
// order (reposEnrolmentProvider: this machine's entry first, because an
// enrolled repository keeps the provider it was enrolled with) with one source
// in front of the checkout that `add` has no use for: the provider the ARGUMENT
// names.
//
// The entry is this command's own first store, so a removal that took it away
// and no further — the half-done case `rm` exists to finish — leaves a machine
// whose config says nothing about the repository at all, and `add`'s reader
// would then answer from the working directory and refuse a second run for a
// provider the operator had written on the command line — the same fact the
// group's own refusal tells them to write ("write it as provider/owner/name
// (github/acme/app), or as owner/name for a repository this machine's config
// lists"), read back as the source it names.
//
// The entry still wins where there is one, and that is the whole of the order:
// the allow-list entry is the repository's own spelling, and a checkout that
// disagrees with an entry this machine holds is a disagreement this command is
// not the place to settle.
//
// One argument is not left to that order: one that names a provider DIFFERENT
// from the entry's. This machine's config is keyed by owner/name alone
// (configRepoKey — the key carries no provider), so `rm bitbucket/acme/app`
// over a machine whose repos.acme/app is github would resolve to
// github/acme/app and take that repository's enrolment and its jobs away, while
// the operator read the argument as naming the Bitbucket repository — a
// different repository, and a different entry of the allow-list. It is refused
// here, before the allow-list and the project are read, with the entry's own
// spelling in the sentence.
func reposRemovalProvider(ctx context.Context, arg, ownerName string, entry localcfg.Repo, known bool) (string, error) {
	named, hasNamed := reposRemovalProviderNamed(arg)
	entryProvider := task.ASCIILower(entry.Provider)
	if hasNamed && known && entry.Provider != "" && named != entryProvider {
		canonical := entryProvider + "/" + ownerName
		return "", userErr("%q names the %s provider, and this machine's project config holds %s as %s — a repos: entry keyed by owner/name, whoever its provider is — so that argument would take away %s's enrolment and its jobs, not the repository it names. Type `fugaro project repos rm %s` to remove the repository this machine holds",
			arg, named, pluginwire.Printable(ownerName), pluginwire.Printable(canonical),
			pluginwire.Printable(canonical), pluginwire.Printable(canonical))
	}
	if known && entry.Provider != "" {
		return entry.Provider, nil
	}
	if hasNamed {
		return named, nil
	}
	return reposEnrolmentProvider(ctx, ownerName, entry, known)
}

// reposRemovalProviderNamed is the provider an argument names, for the argument
// written canonically: reposEnrolmentName's own split, so the two cannot
// disagree about which arguments carry one. A path of two segments is
// owner/name and names no provider, and a longer one is a provider plus a
// repository only when its first segment is one this program knows — a nested
// group is a repository path like any other.
func reposRemovalProviderNamed(arg string) (string, bool) {
	parts := strings.Split(strings.Trim(task.ASCIILower(arg), "/"), "/")
	if len(parts) <= 2 {
		return "", false
	}
	return providerKind(parts[0])
}

// reposRemovalEntryWhy is the local step's line, read off what the file holds
// and never off what this command intends: a machine that never enrolled the
// repository says so rather than reporting a removal it did not make, which is
// the whole of `rm`'s half-done case (D2402's idempotence, read in the other
// direction).
func reposRemovalEntryWhy(plan reposRemoval) string {
	if !plan.entryKnown {
		return "left alone: this machine's project config holds no repos: entry for " + plan.ownerName
	}
	return "removes repos." + plan.localKey
}

// reposRemovalImage is the repository-to-image map step, and it is R8 and R13
// together: the key it takes out is the repository's own under ITS provider
// (repo_images), and a file whose map is still the legacy image_map has that map
// migrated first, by the same write — so the other repositories keep the entries
// they run, under the providers they run them at, and the file stops holding the
// member R2 ignores.
//
// Which map is this file's to edit is R2's answer, not D1906's alone: any
// repo_images wins over any image_map, per member and never per key. So a file
// that names a legacy map while the node publishes repo_images has nothing here
// to take away — the map the readers serve is the node's, and a write of the
// file's would be a map no reader reads — and the sentence says so. A published
// mapping is the published node's, and this command does not edit that node any
// more than `add` writes one there.
//
// The sentence for that case names the door that does exist: `fugaro image map`
// has only `set` (image_map.go), so there is no verb that unsets an entry, and
// the map is published from the file that names one — the entry goes out of that
// file, and `fugaro update` publishes the map. Naming a command that is not there
// would send the operator to an unknown-command error at the one moment they are
// cleaning up.
func reposRemovalImage(ctx context.Context, plan *reposRemoval, p *projectpolicy.Policy) {
	var fromRepoImages bool
	plan.imageNow, fromRepoImages = plan.local.RepoImagesEffective(p)
	// imageOwn is whether the file names the member the readers serve, and
	// imageKey is the key that member is READ by — the one the sentences name:
	// the bare owner/name a legacy map is keyed by, the provider-qualified name
	// repo_images is. The key the WRITE takes out is plan.canonical either way,
	// because what the write leaves is repo_images, whose keys a migration
	// writes canonical too (R13).
	if fromRepoImages {
		plan.imageOwn = plan.local.RepoImages != nil
		plan.imageKey = plan.canonical
	} else {
		plan.imageOwn = plan.local.ImageMap != nil
		plan.imageKey = plan.ownerName
	}
	switch {
	case !plan.imageOwn:
		if plan.imageNow[plan.imageKey] != "" {
			plan.imageWhy = "left alone: the published node's repository-to-image map (`repo_images`) gives " + plan.imageKey + " that entry, and this machine's project config names no map of its own to edit — a file that names no map keeps the published one, so the entry is removed in the project config that published it, and `fugaro update` publishes the map"
			return
		}
		plan.imageWhy = "left alone: this machine's project config names no repository-to-image map (`repo_images`) and the published node maps no entry for " + plan.imageKey
	case plan.imageNow[plan.imageKey] == "":
		plan.imageWhy = "left alone: this machine's repository-to-image map (`repo_images`) holds no entry for " + plan.imageKey
	default:
		m := maps.Clone(plan.imageNow)
		if !fromRepoImages {
			// The map the readers serve is the legacy one: migrating it is what
			// keeps every other repository's entry, and it is the same write.
			m, plan.imageDropped = migrateImageMap(ctx, plan.local, p)
		}
		delete(m, plan.canonical)
		plan.imageNext = m
		plan.imageWhy = "removes repo_images[" + plan.canonical + "], which this machine's project config names"
	}
}

// reposRemovalRun is one run of this repository the registry holds: its ID,
// which the strict confirmation names, and the workflow it is executing, which
// is what says which of the repository's jobs it is holding.
type reposRemovalRun struct {
	ID       string
	Workflow string
}

// reposRemovalRuns reads the run registry once: every entry the `agents` node
// holds is a run that is going, which is the same source `fugaro budget show`
// lists as running. The count is the whole registry's, the way the group's own
// removal reads it (runsInFlight), and the named runs are this repository's, so
// the strict sentence can name the run it is protecting without claiming a run
// of another repository is this one's.
func reposRemovalRuns(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, slug, ownerName string) (int, []reposRemovalRun, error) {
	var agents map[string]map[string]budget.AgentEntry
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return 0, nil, dbErr(lc, err, false)
	}
	n, mine := 0, []reposRemovalRun{}
	for at, runs := range agents {
		for run, e := range runs {
			n++
			if at == slug || task.ASCIILower(e.Repo) == ownerName {
				mine = append(mine, reposRemovalRun{ID: run, Workflow: e.Workflow})
			}
		}
	}
	slices.SortFunc(mine, func(a, b reposRemovalRun) int { return strings.Compare(a.ID, b.ID) })
	return n, mine, nil
}

// reposRemovalRunIDs are the runs' IDs, which is what the sentences name.
func reposRemovalRunIDs(runs []reposRemovalRun) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}

// reposRemovalInflight is the clause the strict confirmation carries: which run
// is going, and what it keeps until it ends. A run of another repository is
// named as that — the removal takes nothing away from it — while the
// confirmation stays the strict one for both, because a removal is never a
// flag's to answer (D2407).
func reposRemovalInflight(plan reposRemoval) string {
	if len(plan.runs) > 0 {
		return fmt.Sprintf("run(s) %s are going on %s: its job, its record and its ledger stay until they end (fugaro runs ls), and nothing here rewrites what they hold",
			strings.Join(reposRemovalRunIDs(plan.runs), ", "), pluginwire.Printable(plan.ownerName))
	}
	return fmt.Sprintf("%d run(s) of other repositories are in flight: nothing here rewrites what they hold, and this removal asks no run to finish", plan.inflight)
}

// reposRemovalWarning is what `rm` prints before the confirmation and, in its
// kept half, again after the write (D2406): removalWarning's own sentence about
// what the removal stops — which depends on whether another entry of the list
// still admits the repository — and then the six things it deliberately leaves.
//
// The third part is what the removal keeps by the owner's ruling of
// 2026-11-17 — the repository's Terraform state and the repository root's own
// IAM members — with the hand-run sequence that does remove them, and it is
// printed here, before the question, for the same reason the six are: the
// operator is deciding about a removal whose leftovers they should know before
// they type their project's name, not after.
func reposRemovalWarning(plan reposRemoval) string {
	return removalWarning(plan.allow, plan.canonical) + "\n" + reposRemovalKept(plan) + "\n" + reposRemovalOffboarding(plan)
}

// reposRemovalKept is the kept half on its own, so the sentence after the write
// says the same thing as the one before it.
func reposRemovalKept(plan reposRemoval) string {
	return "it leaves, and nothing here touches: " + strings.Join(reposRemovalLeaves, "; ") + "."
}

// reposRemovalOffboarding is the part of the repository this command keeps on
// purpose, and the one the owner ruled on (2026-11-17, amending D2405 with
// D2406 standing): the repository's Terraform state, and the repository root's
// own IAM members — the grants of its job accounts and of its build account, on
// the project, on the runs bucket, on the repository's secrets and on its image
// registry.
//
// The state stays for the reason the ruling gives: it is what the offboarding's
// own steps read. The apply that lowers the jobs' protection and the destroy
// that takes the job accounts, the build account and their grants plan from it,
// so a removal that deleted it would leave those resources untracked and steps
// 1 and 3 of docs/how-to/offboarding.md unrunnable — the ground the earlier
// behaviour stood on was that `--forget` is the last step and this command is
// not it.
//
// The sequence is not one-way, and the sentence says so rather than leaving the
// operator to find the repository's jobs back: step 1 is an apply of a root
// that still declares the workflow jobs, the check job and its schedule, so
// over a repository this command has just un-enrolled it recreates all of them
// and writes the `repos:` entry back (init.go's Repository stage, which
// rebuilds the entry from the checkout's spec and republishes the shared
// config). Only step 3's destroy takes them away again, and the state this
// command keeps is what that destroy plans from.
//
// It names the sequence rather than a verb because there is no verb: the retire
// path's API-delete half (RetireSplit's api group, JobDeleteLabels) covers Cloud
// Run jobs and nothing else, and its apply half is `fugaro update`'s own flow,
// which this command does not run. Naming `--retire` here would send the
// operator to a command that answers "Nothing to retire" over a repository
// whose `fugaro.yaml` still declares every address, at the one moment they are
// cleaning up.
func reposRemovalOffboarding(plan reposRemoval) string {
	state := "the repository's Terraform state"
	if plan.stateBucket != "" {
		state = "the repository's Terraform state under gs://" + plan.stateBucket + "/" + infra.RepoStatePrefix(plan.slug) + "/"
	}
	return fmt.Sprintf("it keeps %s and the repository root's own IAM members — the grants of %s's job accounts and of its build account, on the project, on the runs bucket, on its secrets and on its image registry. Neither is this command's to take away: the state is what an apply of that root plans from, and the grants are members only such an apply deletes, so both go with the sequence docs/how-to/offboarding.md lays out — and that sequence builds the repository's jobs back before it destroys them: step 1 `fugaro update --repo --allow-job-delete` lowers the jobs' deletion protection and, because the root still declares the workflow jobs, the check job and its daily check schedule, it recreates all of them over a state this command has left standing, and writes the repository's repos: entry back into this machine's config; step 3's destroy takes the jobs, the schedule, the job accounts, the build account and their grants away again; and step 4 `fugaro update --repo --forget` removes the state itself",
		state, pluginwire.Printable(plan.ownerName))
}

// reposRemovalApply runs the removals the warning named, in an order that
// makes a failure finishable rather than stranding the repository half removed.
//
// The two local removals land in one document and go first, because the
// admission below is derived from that document: the publish narrows the
// per-repository node from the config it is handed, and a config still holding
// the `repos:` entry still admits the repository. The jobs go next, and the
// allow-list entry is written last — it is the store the READ
// above gates on, so a failure anywhere before it leaves a machine on which
// this command still reads the repository and finishes the removal. A failure
// in that last write leaves the same state, and its own sentence says so.
func reposRemovalApply(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, configPath string, c *infra.Clients, plan reposRemoval) error {
	out := cmd.OutOrStdout()
	if err := reposRemovalWriteLocal(out, plan); err != nil {
		return err
	}
	after, err := localcfg.Load(plan.path)
	if err != nil {
		return userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	if err := reposRemovalAdmission(ctx, out, db, after, plan); err != nil {
		return err
	}
	if err := reposRemovalCloud(ctx, cmd, out, c, after, plan); err != nil {
		return err
	}
	allow := slices.DeleteFunc(slices.Clone(plan.allow), func(r string) bool { return r == plan.canonical })
	if err := putAllowRepos(ctx, out, configPath, lc, db, plan.etag, plan.raw, plan.allow, nonNil(allow)); err != nil {
		// putAllowRepos' own sentences are written for a caller whose write is
		// its first act — `add`'s, and the group's edit — and this one's is its
		// last, so "nothing was written" would read as a removal that never
		// happened. What is true is that one store is left.
		return userErr("%v; the removal's other stores are already gone — this machine's config entry, the published admission and the Cloud Run jobs — and %s is the one store left, so running this command again finishes it",
			err, plan.canonical)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, reposRemovalKept(plan))
	fmt.Fprintln(out, reposRemovalOffboarding(plan))
	return nil
}

// reposRemovalWriteLocal removes the `repos:` entry and this machine's own
// image mapping, in this machine's project config: one document, so the second
// write is made from the file the first one left, exactly as the converge's own
// two writes are.
func reposRemovalWriteLocal(out io.Writer, plan reposRemoval) error {
	cur, old := plan.local, plan.old
	if plan.entryKnown {
		next := *cur
		next.Repos = maps.Clone(cur.Repos)
		delete(next.Repos, plan.localKey)
		if err := userSave(out, plan.path, old, &next); err != nil {
			return err
		}
		cur = &next
		fmt.Fprintf(out, "repos.%s: removed\n", plan.localKey)
	} else {
		fmt.Fprintf(out, "%s\n", plan.entryWhy)
	}
	if plan.imageNext != nil {
		next := *cur
		next.RepoImages = plan.imageNext
		// The legacy member goes with the write: R2 reads repo_images first, so
		// a file left holding both would carry a map nothing reads (R14's file
		// half, done here because this write already has the file open).
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
		fmt.Fprintf(out, "repo_images[%s]: removed\n", plan.canonical)
		for _, line := range plan.imageDropped {
			fmt.Fprintf(out, "%s\n", line)
		}
	} else {
		fmt.Fprintf(out, "%s\n", plan.imageWhy)
	}
	if _, err := localcfg.Load(plan.path); err != nil {
		return userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	return nil
}

// reposRemovalAdmission takes away the published per-repository node, through
// the publish's own removal path and not through a second writer of it (F18):
// the node's admissions are derived from the config (DeriveAccounts,
// deriveRepoAccounts), so the question "is this repository still admitted" is
// answered by the publish's own build (policyNodeForPublish) and written by the
// publish's own narrowing (tightenAdmissions), which is the same pair
// `fugaro project policy publish` runs before it writes the shared node.
//
// A config that cannot be published leaves the node as it is, loudly: the
// repository's enrolment is what this command is here to remove, and a
// configuration this machine refuses to publish is a second problem, named with
// the one command that answers it rather than a reason to leave the allow-list,
// the jobs and the state behind.
func reposRemovalAdmission(ctx context.Context, out io.Writer, db *rtdb.Client, lc *localcfg.Config, plan reposRemoval) error {
	old, err := publishedPolicyOf(plan.raw, true)
	if err != nil {
		return err
	}
	identities, err := bitbucketTokenIdentities(ctx, db, lc)
	if err != nil {
		return err
	}
	_, pol, _, err := policyNodeForPublish(ctx, lc, old, plan.raw, true, false, false, identities)
	if err != nil {
		fmt.Fprintf(out, "%s: left as it is, because this machine's config cannot be published (%v): `fugaro project policy publish` takes the admission away once that is fixed\n",
			projectpolicy.PathRepo(plan.slug), oneLine(err.Error()))
		return nil
	}
	desired, err := repoAdmissions(pol)
	if err != nil {
		return err
	}
	// The parent is read whole and then cut down to this repository's own child.
	// tightenAdmissions narrows or removes every key it is handed, and the map it
	// is handed is this machine's derivation alone (policyNodeForPublish →
	// repoAdmissions → DeriveAccounts): a child belonging to a repository this
	// machine's config does not list — one onboarded from another machine, which
	// is what a shared project is — has no entry in `desired` at all, so the
	// narrowing reads it as a repository no account admits any more and deletes
	// it, taking away the cascade access of a repository this removal never named
	// and its confirmation never showed. This command removes one repository, so
	// it narrows one child; and a child it cannot read is refused only where that
	// child is this one (tightenAdmissions' own rule), which is also what keeps
	// somebody else's unreadable node from stopping a removal whose local config
	// is already written.
	var parent map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathRepos(), &parent); err != nil {
		return dbErr(lc, err, false)
	}
	existing := make(map[string]json.RawMessage, 1)
	if child, ok := parent[plan.slug]; ok {
		existing[plan.slug] = child
	}
	_, lines, err := tightenAdmissions(ctx, db, lc, desired, existing)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		fmt.Fprintf(out, "%s: already absent\n", projectpolicy.PathRepo(plan.slug))
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	// The node is read back rather than assumed gone: the admission is the
	// config's derivation and not this command's to compute, so what is here
	// says what the derivation answered, and a repository the project still
	// admits — an account whose allow_data_to still names it — is a fact the
	// operator has to see rather than a silent half-removal.
	var child json.RawMessage
	have, err := db.Get(ctx, projectpolicy.PathRepo(plan.slug), &child)
	if err != nil {
		return dbErr(lc, err, false)
	}
	if have && len(child) > 0 && string(child) != "null" {
		fmt.Fprintf(out, "note: %s still admits accounts (%s), because this machine's config still admits %s somewhere else: `fugaro accounts allow` is where an account's allow_data_to is edited, and the publish takes the admission away once no account names it\n",
			projectpolicy.PathRepo(plan.slug), oneLine(string(child)), plan.canonical)
	}
	return nil
}

// reposRemovalCloud removes the repository's Cloud Run jobs and its Scheduler
// job — its cloud resources, and the state is not one of them (the owner's
// ruling of 2026-11-17) — through the retirement machinery's own pieces and
// nothing else: Orphans reads
// the project by the repository's label — one jobs.list and one secrets.list,
// both read-only — JobDeleteLabels names the pairs the delete must find, and
// deleteOrphanedJob (F16) checks them before it deletes, treats a 404 as the
// work already done and polls the operation to a terminal state.
//
// A labelled secret container can never reach the delete: JobDeleteLabels
// answers for the two kinds of Cloud Run job and for nothing else, so the
// container a whole-repository read turns up is skipped by construction rather
// than by a branch that could drift. That is the third wall of "Fugaro never
// deletes a secret" (design §6), in the one command that removes most of a
// repository at once.
//
// The whole step is gated on the state bucket this machine's config records,
// because that record is what says this machine has converged the installation
// and knows where the repository lives: without it nothing here looks at the
// project at all, and the sentence says which command writes it.
//
// A job an in-flight run of this repository is executing is never deleted
// (D2407): the registry names the runs and their workflows, the job's own
// `fugaro_workflow` label says which variant it is, and a job whose variant is
// one of those workflows is the job that run holds — including the
// per-account variant of it, which is why the match is the workflow and its
// `_<account>` suffixes rather than the name alone. The run's record, its
// ledger and its counters are untouched by every path here; this is the one
// place a run could be disturbed at all, and it says so instead.
func reposRemovalCloud(ctx context.Context, cmd *cobra.Command, out io.Writer, c *infra.Clients, lc *localcfg.Config, plan reposRemoval) error {
	if plan.stateBucket == "" {
		fmt.Fprintf(out, "left alone: this machine's project config records no terraform state bucket, so nothing here knows where %s's Cloud Run jobs are — `fugaro update` records it; run that and run this again\n", pluginwire.Printable(plan.ownerName))
		return nil
	}
	// The spec is this one repository's and produces nothing: an empty workflow
	// map and no check job make every labelled job of the repository a
	// retirement, and an empty secrets map makes every labelled container a
	// note rather than a retirement (Orphans' own split), so the read answers
	// exactly "what of this repository is still in the project".
	spec := infra.RepoSpec{
		Name: plan.ownerName, Slug: plan.slug, Label: plan.label,
		GCPProject: lc.GCPProject, Region: lc.Region,
		Workflows: map[string]infra.WorkflowSpec{},
	}
	rets, _, warnings, err := infra.Orphans(ctx, c, spec, nil)
	if err != nil {
		return remote(err)
	}
	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	// The split is the retirement's own (RetireSplit), and the group taken here
	// is the one it hands the API delete: the jobs no apply can finish. It is
	// asked with no state addresses, because this command has no plan of the
	// repository's root and no Terraform state to read addresses out of — and it
	// needs none: every job Orphans returns is Protected (out of the
	// configuration, so no apply can lower its deletion_protection any more),
	// which is the case apiDeleted answers the API delete to whatever the state
	// holds.
	api, _, _ := infra.RetireSplit(nil, spec, rets)
	deleted, held := 0, 0
	for _, ret := range api {
		want, ok := infra.JobDeleteLabels(spec, ret)
		if !ok {
			continue
		}
		switch why, err := reposRemovalHeld(ctx, c, spec, ret, plan.runs); {
		case err != nil:
			return remote(err)
		case why != "":
			fmt.Fprintf(out, "%s\n", why)
			held++
			continue
		}
		if err := deleteOrphanedJob(ctx, c, spec.GCPProject, spec.Region, ret.Job, want); err != nil {
			return remote(fmt.Errorf("deleting the %s %s of %s: %w", ret.Kind, ret.Job, plan.ownerName, err))
		}
		fmt.Fprintf(out, "deleted the %s %s\n", ret.Kind, ret.Job)
		deleted++
	}
	if deleted == 0 && held == 0 {
		fmt.Fprintf(out, "already absent: %s has no Cloud Run job in %s\n", plan.ownerName, spec.GCPProject)
	}
	if err := reposRemovalScheduler(ctx, out, c, lc, plan); err != nil {
		return err
	}
	// The repository's state is NOT deleted. The owner's ruling of 2026-11-17
	// (amending D2405, D2406 standing) keeps it, because the state is what the
	// offboarding's own steps read: the apply that lowers the jobs' protection
	// and the destroy that takes the job accounts, the build account and their
	// grants plan from it, so a removal that deleted it would leave those
	// resources untracked and steps 1 and 3 of docs/how-to/offboarding.md
	// unrunnable. It is named, with the sequence that does remove it, before
	// the confirmation and again after the write (reposRemovalOffboarding).
	return nil
}

// reposRemovalHeld is whether a job about to be deleted is one an in-flight run
// of this repository is executing, and the line that says so. It reads the job's
// own labels rather than the registry's `Workflow` alone: the registry names the
// workflow a run resolved, and the job's label names the variant it was deployed
// as (`<workflow>` for the default account, `<workflow>_<account>` for a named
// one), so the two are compared as a workflow with its account suffixes.
//
// A job that is already gone is not held, and a job that cannot be read at all
// is an error rather than a silent delete: the read is the same one
// deleteOrphanedJob makes a moment later, and a permission that refuses it is
// reported the same way on both.
func reposRemovalHeld(ctx context.Context, c *infra.Clients, spec infra.RepoSpec, ret infra.Retirement, runs []reposRemovalRun) (string, error) {
	if len(runs) == 0 {
		return "", nil
	}
	name := "projects/" + spec.GCPProject + "/locations/" + spec.Region + "/jobs/" + ret.Job
	live, err := c.Run.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading the %s %s (run.jobs.get): %w", ret.Kind, ret.Job, err)
	}
	variant := live.Labels[gcp.LabelWorkflow]
	if variant == "" {
		// No workflow label: the check job, which no run executes, so nothing
		// holds it.
		return "", nil
	}
	for _, run := range runs {
		if variant == run.Workflow || strings.HasPrefix(variant, run.Workflow+"_") {
			return fmt.Sprintf("kept: the %s %s is the %s job run(s) %s are executing, and it stays until they end (fugaro runs ls)",
				ret.Kind, ret.Job, variant, strings.Join(reposRemovalRunIDs(runs), ", ")), nil
		}
		// A run whose workflow the registry does not record cannot be attributed
		// to a job, and this is a workflow job, so it may be that run's. The
		// registry's own sweep reads an entry with no workflow the same way
		// (budget.Sweeper.isLive: "errs towards keeping"), and the promise the
		// confirmation made is about the run's job, not about the runs this
		// command could name.
		if run.Workflow == "" {
			return fmt.Sprintf("kept: the %s %s is a workflow job run(s) %s may be executing — this machine's registry records no workflow for one of them — and it stays until they end (fugaro runs ls)",
				ret.Kind, ret.Job, strings.Join(reposRemovalRunIDs(runs), ", ")), nil
		}
	}
	return "", nil
}

// reposRemovalScheduler deletes the repository's Cloud Scheduler job — the one
// that fires its daily check — through the Scheduler API.
//
// It is the one resource of D2405's list that no retire path can reach: the
// module gives it no label (so Orphans, which reads labels, never sees it), it
// is not a Cloud Run job (so JobDeleteLabels and the API delete never see it
// either), and this command runs no apply at all — it keeps the state (the
// owner's ruling of 2026-11-17). Its name is Fugaro's own
// derivation from the repository's slug (gcp.SchedulerJobName), and the check
// before the delete is the job's own target: the URI it posts to is exactly the
// one the module writes for this repository's check job (check.tf), which is
// the same identity test discovery makes before it adopts a Scheduler job
// (infra.discovery.schedulerJob, through checkRunURI). A job that targets
// anything else is left alone with a line rather than deleted, the way every
// other delete here checks what it is about to delete first.
//
// The region is the config's scheduler_region when it names one and the
// nearest Scheduler location to the repository's region otherwise (the spec's
// own rule, spec.go). A region with no Scheduler location this program knows of
// is not a failure of the removal: it is a line, and the operator has the one
// command that deletes the job by hand.
func reposRemovalScheduler(ctx context.Context, out io.Writer, c *infra.Clients, lc *localcfg.Config, plan reposRemoval) error {
	if c.Scheduler == nil {
		return nil
	}
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			fmt.Fprintf(out, "left alone: %s; the repository's Scheduler job, if it has one, is deleted by hand (gcloud scheduler jobs delete %s --location <region> --project %s)\n",
				oneLine(err.Error()), gcp.SchedulerJobName(plan.slug), lc.GCPProject)
			return nil
		}
		region = r
	}
	short := gcp.SchedulerJobName(plan.slug)
	name := infra.SchedulerJobName(lc.GCPProject, region, short)
	job, err := c.Scheduler.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	if isNotFound(err) {
		fmt.Fprintf(out, "already absent: %s has no Scheduler job in %s\n", plan.ownerName, region)
		return nil
	}
	if err != nil {
		return remote(fmt.Errorf("reading the Scheduler job %s: %w", name, err))
	}
	if job.HttpTarget == nil || job.HttpTarget.Uri != reposRemovalCheckURI(lc, plan) {
		fmt.Fprintf(out, "left alone: the Scheduler job %s does not target the check job of %s, so it is not this repository's to delete; check it by hand (gcloud scheduler jobs describe %s --location %s --project %s)\n",
			short, pluginwire.Printable(plan.ownerName), short, region, lc.GCPProject)
		return nil
	}
	if _, err := c.Scheduler.Projects.Locations.Jobs.Delete(name).Context(ctx).Do(); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(out, "already absent: %s has no Scheduler job in %s\n", plan.ownerName, region)
			return nil
		}
		return remote(fmt.Errorf("deleting the Scheduler job %s: %w", name, err))
	}
	fmt.Fprintf(out, "deleted the Scheduler job %s\n", short)
	return nil
}

// reposRemovalCheckURI is the URI the repository root writes into the
// repository's Scheduler job: its own check job's :run endpoint, in the
// repository's region. It is infra.checkRunURI's string, spelled here because
// that one takes a whole RepoSpec (whose check half needs the base images and
// the workflows this command never reads) and this one has the slug.
func reposRemovalCheckURI(lc *localcfg.Config, plan reposRemoval) string {
	return "https://run.googleapis.com/v2/projects/" + lc.GCPProject + "/locations/" + lc.Region + "/jobs/" + gcp.CheckJobName(plan.slug) + ":run"
}
