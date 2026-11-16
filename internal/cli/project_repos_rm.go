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
	// imageNow is the effective repo-to-image map (the keep rule, D1906: the
	// file's where it names one, the published node's otherwise), imageNext
	// the map the file will hold — nil when the mapping is not this file's to
	// take away — and imageOwn whether the file names the map itself.
	imageNow  map[string]string
	imageNext map[string]string
	imageOwn  bool
	imageWhy  string
	// inflight is every run the registry holds, runs the runs of this
	// repository. D2407: a run in flight is never disturbed, so the removal
	// asks the strict confirmation and names the run.
	inflight int
	runs     []string
	// stateBucket is the state bucket this machine's project config records,
	// which is where the repository's state lives and the one condition under
	// which this command looks at the project's own cloud resources at all.
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
		return userErr("nothing was written or removed: fugaro project repos rm removes the published allow-list entry, this machine's config entry, the repository's published admission, its Cloud Run jobs and its Terraform state, which are what admit a repository to the project and what run it: %s", initflow.AgentRefusal(m))
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
	return reposRemovalApply(ctx, cmd, o, lc, db, configPath, plan)
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
	reposRemovalImage(&plan, p)
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
func reposRemovalProvider(ctx context.Context, arg, ownerName string, entry localcfg.Repo, known bool) (string, error) {
	if known && entry.Provider != "" {
		return entry.Provider, nil
	}
	if p, ok := reposRemovalProviderNamed(arg); ok {
		return p, nil
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

// reposRemovalImage is the image-map step, under the keep rule D1906 reads the
// map by: the effective map is this file's own only when the file names one, so
// a machine that names none publishes the node's map rather than its own and
// has nothing here to take away. A published mapping is the published node's,
// and this command does not edit that node's image_map any more than `add`
// writes one there.
func reposRemovalImage(plan *reposRemoval, p *projectpolicy.Policy) {
	plan.imageNow = plan.local.ImageMapEffective(p.ImageMap)
	plan.imageOwn = plan.local.ImageMap != nil
	switch {
	case !plan.imageOwn:
		if _, mapped := plan.imageNow[plan.ownerName]; mapped {
			plan.imageWhy = "left alone: image_map[" + plan.ownerName + "] is the published node's mapping, and this machine's project config names no map of its own to edit (`fugaro image map rm " + plan.ownerName + "` takes it away)"
			return
		}
		plan.imageWhy = "left alone: this machine's project config names no repo-to-image map and the published node maps no entry for " + plan.ownerName
	case plan.imageNow[plan.ownerName] == "":
		plan.imageWhy = "left alone: this machine's repo-to-image map holds no entry for " + plan.ownerName
	default:
		m := make(map[string]string, len(plan.imageNow))
		maps.Copy(m, plan.imageNow)
		delete(m, plan.ownerName)
		plan.imageNext = m
		plan.imageWhy = "removes image_map[" + plan.ownerName + "], which this machine's project config names"
	}
}

// reposRemovalRuns reads the run registry once: every entry the `agents` node
// holds is a run that is going, which is the same source `fugaro budget show`
// lists as running. The count is the whole registry's, the way the group's own
// removal reads it (runsInFlight), and the named runs are this repository's, so
// the strict sentence can name the run it is protecting without claiming a run
// of another repository is this one's.
func reposRemovalRuns(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, slug, ownerName string) (int, []string, error) {
	var agents map[string]map[string]budget.AgentEntry
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return 0, nil, dbErr(lc, err, false)
	}
	n, mine := 0, []string{}
	for at, runs := range agents {
		for run, e := range runs {
			n++
			if at == slug || task.ASCIILower(e.Repo) == ownerName {
				mine = append(mine, run)
			}
		}
	}
	slices.Sort(mine)
	return n, mine, nil
}

// reposRemovalInflight is the clause the strict confirmation carries: which run
// is going, and what it keeps until it ends. A run of another repository is
// named as that — the removal takes nothing away from it — while the
// confirmation stays the strict one for both, because a removal is never a
// flag's to answer (D2407).
func reposRemovalInflight(plan reposRemoval) string {
	if len(plan.runs) > 0 {
		return fmt.Sprintf("run(s) %s are going on %s: its job, its record and its ledger stay until they end (fugaro runs ls), and nothing here rewrites what they hold",
			strings.Join(plan.runs, ", "), pluginwire.Printable(plan.ownerName))
	}
	return fmt.Sprintf("%d run(s) of other repositories are in flight: nothing here rewrites what they hold, and this removal asks no run to finish", plan.inflight)
}

// reposRemovalWarning is what `rm` prints before the confirmation and, in its
// kept half, again after the write (D2406): removalWarning's own sentence about
// what the removal stops — which depends on whether another entry of the list
// still admits the repository — and then the six things it deliberately leaves.
func reposRemovalWarning(plan reposRemoval) string {
	return removalWarning(plan.allow, plan.canonical) + "\n" + reposRemovalKept(plan)
}

// reposRemovalKept is the kept half on its own, so the sentence after the write
// says the same thing as the one before it.
func reposRemovalKept(plan reposRemoval) string {
	return "it leaves, and nothing here touches: " + strings.Join(reposRemovalLeaves, "; ") + "."
}

// reposRemovalApply runs the removals the warning named, in an order that keeps
// every write of one store behind the read the next one is made against.
//
// The two local removals land in one document and go first, because the
// admission below is derived from that document: the publish narrows the
// per-repository node from the config it is handed, and a config still holding
// the `repos:` entry still admits the repository. The allow-list entry goes
// through the one ETag-guarded editor, and the config is read back afterwards,
// because that write records the generation it landed in the file (putAllowRepos,
// D854) and a config read before it states a generation the node has moved past.
func reposRemovalApply(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, configPath string, plan reposRemoval) error {
	out := cmd.OutOrStdout()
	if err := reposRemovalWriteLocal(out, plan); err != nil {
		return err
	}
	allow := slices.DeleteFunc(slices.Clone(plan.allow), func(r string) bool { return r == plan.canonical })
	if err := putAllowRepos(ctx, out, configPath, lc, db, plan.etag, plan.raw, plan.allow, nonNil(allow)); err != nil {
		return err
	}
	after, err := localcfg.Load(plan.path)
	if err != nil {
		return userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	if err := reposRemovalAdmission(ctx, out, db, after, plan); err != nil {
		return err
	}
	if err := reposRemovalCloud(ctx, cmd, out, after, plan); err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, reposRemovalKept(plan))
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
		next.ImageMap = plan.imageNext
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
		fmt.Fprintf(out, "image_map[%s]: removed\n", plan.ownerName)
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
	var existing map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathRepos(), &existing); err != nil {
		return dbErr(lc, err, false)
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

// reposRemovalCloud removes the repository's cloud resources and its state,
// through the retirement machinery's own pieces and nothing else: Orphans reads
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
func reposRemovalCloud(ctx context.Context, cmd *cobra.Command, out io.Writer, lc *localcfg.Config, plan reposRemoval) error {
	if plan.stateBucket == "" {
		fmt.Fprintf(out, "left alone: this machine's project config records no terraform state bucket, so nothing here knows where %s's state and its jobs are — `fugaro update` records it; run that and run this again\n", pluginwire.Printable(plan.ownerName))
		return nil
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
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
	deleted := 0
	for _, ret := range api {
		want, ok := infra.JobDeleteLabels(spec, ret)
		if !ok {
			continue
		}
		if err := deleteOrphanedJob(ctx, c, spec.GCPProject, spec.Region, ret.Job, want); err != nil {
			return remote(fmt.Errorf("deleting the %s %s of %s: %w", ret.Kind, ret.Job, plan.ownerName, err))
		}
		fmt.Fprintf(out, "deleted the %s %s\n", ret.Kind, ret.Job)
		deleted++
	}
	if deleted == 0 {
		fmt.Fprintf(out, "already absent: %s has no Cloud Run job in %s\n", plan.ownerName, spec.GCPProject)
	}
	objects, err := infra.RepoStateObjects(ctx, c, plan.stateBucket, plan.slug)
	if err != nil {
		return remote(err)
	}
	if len(objects) == 0 {
		fmt.Fprintf(out, "already absent: gs://%s/%s/ holds nothing\n", plan.stateBucket, infra.RepoStatePrefix(plan.slug))
	} else {
		// The versioning check is forgetRepo's own (init.go), for its reason:
		// the objects are deleted, and only the bucket's own versioning keeps a
		// copy of what a rollback would need.
		switch versioned, err := infra.StateBucketVersioned(ctx, c, plan.stateBucket); {
		case err != nil:
			return remote(err)
		case !versioned:
			return userErr("gs://%s has no object versioning, so the state objects of %s could not be restored once deleted; turn versioning on (gcloud storage buckets update gs://%s --versioning) and run this again; nothing else was removed either way",
				plan.stateBucket, pluginwire.Printable(plan.ownerName), plan.stateBucket)
		}
		gone, err := infra.ForgetRepoState(ctx, c, plan.stateBucket, plan.slug)
		if err != nil {
			return remote(err)
		}
		for _, name := range gone {
			fmt.Fprintf(out, "deleted gs://%s/%s\n", plan.stateBucket, name)
		}
	}
	// The two resources this door cannot reach, named rather than left for the
	// operator to discover: they are the repository root's own, and the apply
	// that deletes them is `fugaro update --repo`'s (`--retire`), which is the
	// one command that removes what a configuration no longer produces. Saying
	// nothing here would make this command read as a removal that took
	// everything, which is the one thing the six-thing list exists to prevent.
	fmt.Fprintf(out, "not removed here: the repository's Scheduler job and the grants of its job account are the repository root's own resources, and only its apply deletes them — run `fugaro update --repo . --retire` from a checkout of %s\n", pluginwire.Printable(plan.ownerName))
	return nil
}
