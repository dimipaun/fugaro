package cli

// This file is the convergent half of `fugaro project repos add` (RE1, the
// owner's order of 2026-11-14: "it's sooo difficult to add a repo, we need a
// command to take care of all this").
//
// Enrolling a repository used to be about six acts done by hand in the right
// order — the `repos:` entry, the image map entry, the published allow-list,
// the publish, and `fugaro update --repo` for the job and its run.invoker
// grant — and the HARD RULE (CLAUDE.md, "Things that bite") says the
// documented commands do the whole job. One confirmation, five steps, each of
// them idempotent, is that promise kept for enrolment.
//
// What this half does NOT do is invent a second copy of the steps: the
// published node is still written by the one ETag-guarded writer
// (putAllowRepos), and the publish and the deploy are `fugaro update`'s own
// registered stages, invoked in-process the way `fugaro accounts add`
// invokes them (accounts_add_converge.go, spec §2.5). A converge that
// reimplemented a stage would be the one thing the HARD RULE cannot survive:
// two writers of the same document, drifting apart.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// enrolBelongName is the installation the owner-run live checks never touch
// (internal/agentenv's own words: "every live step is owner-run, on
// installation fugaro and a sandbox repository only, never `belong`";
// TestLiveCredsRules refuses it by name). The steps this command runs are the
// same kind of act those checks are — a real installation's policy node, its
// job set and an IAM grant — so it keeps their rule, by the same name and the
// same spelling.
const enrolBelongName = "belong"

// reposEnrolment is everything the convergence read before it changed
// anything, and everything the printed plan is built from.
//
// Every field is a fact read from a file or from the project, never a guess: a
// value the command cannot read is either refused where it is read, naming the
// one command that supplies it, or left out with the line that says so. That
// is the whole difference between a converge and a wizard that fills things in.
type reposEnrolment struct {
	// canonical is provider/owner/name: the form of every entry this command
	// writes — the allow-list's, the image map's key, and what the job set is
	// deployed for.
	canonical string
	// ownerName is the local config's key for it, which is owner/name: the
	// spelling `fugaro update --repo` and `fugaro settings set` key an entry
	// by.
	ownerName string
	// entry is the `repos:` entry the file must hold, entryNow the one it
	// holds, entryKnown whether it held one at all, and entryWhy the step's own
	// line.
	entry      localcfg.Repo
	entryNow   localcfg.Repo
	entryKnown bool
	entryWhy   string
	// local is this machine's project config as the file holds it, path and
	// old being the file itself: every local write below lands in that one
	// document.
	local *localcfg.Config
	path  string
	old   []byte
	// imageEntry is the catalog entry the repository's workflow selects, and
	// imageWhy the line for a map step that writes nothing.
	imageEntry string
	imageWhy   string
	// imageMap is the map the file will hold, imageNow the one it holds, so
	// the step can say whether the document moves at all.
	imageMap map[string]string
	imageNow map[string]string
	// imageSeeded is true when the map is seeded from the published node
	// because this file names none: the note below says why that matters.
	imageSeeded bool
	// allow is the published allow-list as the node holds it, and allowWhy the
	// step's own line.
	allow    []string
	allowWhy string
	// checkout is the repository's checkout on this machine, "" when there is
	// none: the repository stage converges a checkout, and a repository whose
	// checkout is elsewhere is the one step this command cannot do here.
	checkout string
}

// runProjectReposEnroll is the convergent path: read, show, ask once, write.
func runProjectReposEnroll(cmd *cobra.Command, o *projectReposOptions, arg string) error {
	ctx := cmd.Context()
	// `*` is the allow-list's own entry for every repository and not a
	// repository: it has no provider, no workflow list and no image, so a
	// convergence over it has nothing to converge. Refused rather than
	// silently doing the narrow thing, and the flag that writes exactly the
	// wide list is named — a person who typed `*` meant that list.
	if arg == "*" {
		return userErr("`*` is the allow-list's entry for every repository and not a repository, so there is nothing here to enrol: no provider, no workflow list and no image mapping. `fugaro project repos add * --allow-list-only` writes the wide list and nothing else")
	}
	// SD8, and the same refusal `repos rm` and `policy publish` carry: this
	// writes the published node, the job set and an IAM grant, so it is a
	// person's step and never a coding agent's — an agent that enrolled a
	// repository would have widened what every run of the project is admitted
	// by on its own. It comes before every read and before every
	// confirmation, so nothing is written and no cloud write is even reached.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: fugaro project repos add writes the published allow-list, the job set and the repository's run.invoker grant, which are what admit a repository to the project: %s", initflow.AgentRefusal(m))
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
	if err := refuseEnrolmentOfBelong(lc); err != nil {
		return err
	}
	plan, err := reposEnrolmentRead(ctx, o, lc, db, arg)
	if err != nil {
		return err
	}
	reposEnrolmentPlan(cmd.OutOrStdout(), lc, plan)
	// One question for the whole convergence, and --yes answers that question
	// and nothing else: every refusal above already happened, and not one of
	// them is a confirmation. Safe while runs are in flight (CLAUDE.md): this
	// asks no run to finish and rewrites nothing a run holds — the allow-list
	// write is the ETag-guarded one, and the stages are `fugaro update`'s.
	if err := confirmTyped(cmd, bufio.NewReader(cmd.InOrStdin()), lc, o.yes, reposEnrolmentWhat(lc, plan)); err != nil {
		return err
	}
	return reposEnrolmentApply(ctx, cmd, o, lc, db, configPath, plan)
}

// refuseEnrolmentOfBelong refuses the one installation the owner-run live
// checks never touch, which is the checks' own rule kept here in their own
// shape (internal/agentenv; internal/e2e's TestLiveCredsRules refuses
// `strings.EqualFold(install, "belong")`). The name is what that rule names,
// so a test can set it without standing anything up.
func refuseEnrolmentOfBelong(lc *localcfg.Config) error {
	if !strings.EqualFold(lc.Name, enrolBelongName) {
		return nil
	}
	return userErr("nothing was enrolled: this is installation %s, and every step that writes a real installation is owner-run, on a sandbox and never %s (internal/agentenv's rule, the one TestLiveCredsRules keeps). Run this on the sandbox, or write the allow-list alone with `fugaro project repos add <repo> --allow-list-only`, which touches no job and no grant",
		safetext.Field(lc.Name), enrolBelongName)
}

// reposEnrolmentRead reads everything the plan is built from. It writes
// nothing, and that is the order it exists for: every refusal lands before the
// operator is shown a plan and asked about it, so a yes is never followed by a
// failure that should have come first.
func reposEnrolmentRead(ctx context.Context, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, arg string) (reposEnrolment, error) {
	var plan reposEnrolment
	ownerName, err := reposEnrolmentName(arg)
	if err != nil {
		return plan, err
	}
	plan.ownerName = ownerName
	plan.entryNow, plan.entryKnown = configRepo(lc, ownerName)
	provider, err := reposEnrolmentProvider(ctx, ownerName, plan.entryNow, plan.entryKnown)
	if err != nil {
		return plan, err
	}
	canonical, err := projectpolicy.Canonical(provider, ownerName)
	if err != nil {
		return plan, userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	plan.canonical = canonical
	plan.checkout = checkoutRoot(ctx, ownerName)
	cfg := checkoutConfig(ctx, ownerName)
	if plan.entry, plan.entryWhy, err = reposEnrolmentEntry(ctx, lc, ownerName, plan, provider, cfg); err != nil {
		return plan, err
	}
	// The project's own file and the published node are read once, here: the
	// map step is built from both (the keep rule below is what makes the
	// difference matter) and the allow-list step from the node, and neither
	// write happens before the confirmation.
	if plan.path, plan.local, plan.old, err = catalogConfig(ctx, o.cloud); err != nil {
		return plan, err
	}
	_, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return plan, err
	}
	if !exists {
		return plan, userErr("no policy is published for project %s (%s): this command writes the published allow-list, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return plan, err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return plan, err
	}
	plan.allow = nonNil(p.AllowRepos)
	plan.allowWhy = allowStepWhy(p.AllowRepos, canonical)
	if plan.imageEntry, plan.imageWhy, err = reposEnrolmentImage(plan, cfg, p.ImageMap); err != nil {
		return plan, err
	}
	return plan, nil
}

// reposEnrolmentName is the operator's argument as the local config keys a
// repository: owner/name, with the provider segment dropped when the name was
// written canonically. A path of more than two segments is kept whole — a
// nested group is a repository path like any other, and it is what the
// allow-list matches and what the config keys by.
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
	if checkoutRoot(ctx, ownerName) != "" {
		if p, ok := providerFromOrigin(ctx); ok {
			return p, nil
		}
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
// The workflow selection comes from the checkout's own fugaro.yaml and from
// nowhere else: it is the repository's own statement, and a converge that
// guessed one would write a selection no run reads. The rest is read from the
// project — the App ID the project's other GitHub repositories are onboarded
// with, and the account they name — or carried as the entry already has it.
func reposEnrolmentEntry(ctx context.Context, lc *localcfg.Config, ownerName string, plan reposEnrolment, provider string, cfg *config.Config) (localcfg.Repo, string, error) {
	next := plan.entryNow
	next.Provider = provider
	if cfg != nil {
		next.Workflows = slices.Sorted(maps.Keys(cfg.Workflows))
	}
	if len(next.Workflows) == 0 {
		return next, "", userErr("cannot read %s's workflow list, and nothing here guesses one: it is the repository's own selection (fugaro.yaml, workflows:), read from its checkout. Run this from that checkout, or write it with 'fugaro settings set repos.%s.workflows' and run this again",
			pluginwire.Printable(ownerName), ownerName)
	}
	if next.BaseBranch == "" && cfg != nil {
		next.BaseBranch = cfg.Git.BaseBranch
	}
	if next.GitHubAppID == "" && provider == "github" {
		appID, ok := projectGitHubAppID(lc, ownerName)
		if !ok {
			// The App ID is what a GitHub repository's job is built from, so
			// an entry written without one would deploy a job no run can
			// reach. A project with no GitHub repository to read it from is
			// the state this refuses, and the command that supplies it is
			// named rather than a number guessed.
			return next, "", userErr("cannot read the GitHub App ID %s's job is built from: no other repository of project %s names one, and this command never picks a number. Write it with 'fugaro settings set repos.%s.github_app_id <id>' and run this again",
				pluginwire.Printable(ownerName), pluginwire.Printable(lc.Name), ownerName)
		}
		next.GitHubAppID = appID
	}
	if len(next.ClaudeAccounts) == 0 {
		next.ClaudeAccounts = projectClaudeAccounts(lc, ownerName)
	}
	next = reposEntryMerge(plan.entryNow, next, plan.entryKnown)
	return next, reposEntryWhy(plan.entryNow, next, plan.entryKnown, ownerName), nil
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

// reposEntryMerge is the entry the file will hold: the fields this command
// read, applied to a copy of what the file has, so a field it does not read —
// the Vertex flag, the credentials-service record — is carried as it stands. A
// repository the file does not list gets the read entry as it is.
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

// reposEntryWhy is the step's line: what the entry holds, and whether the
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

// listLine is a list as the entry's line prints it, "" when it holds nothing.
func enrolListLine(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return "[" + strings.Join(ss, " ") + "]"
}

// reposEnrolmentImage is the catalog entry the repository's job runs, chosen
// with the one rule every job's image is resolved by (catalog.Choose: the pin
// a workflow names, else the fewest-tagged entry covering its needs), and the
// map the file will hold.
//
// The map is MERGED and never replaced. Under the keep rule (D1906,
// localcfg.ImageMapEffective) a file that writes a map states the project's
// whole map, so a file that names none — the state every machine is in before
// its first `fugaro image map set` — would publish a one-entry map over the
// node's and take every other repository's mapping away with it. That is the
// trap `fugaro image map set` warns about in its own note; here the published
// map is read and this entry is added to it instead.
func reposEnrolmentImage(plan reposEnrolment, cfg *config.Config, published projectpolicy.ImageMap) (string, string, error) {
	plan.imageNow = plan.local.ImageMap
	if plan.imageNow == nil && len(published) > 0 {
		plan.imageNow = maps.Clone(published)
		plan.imageSeeded = true
	}
	if e, ok := plan.imageNow[plan.canonical]; ok {
		plan.imageMap = imageMapWith(plan.local, plan.canonical, e).ImageMap
		return e, "already image_map[" + plan.canonical + "] = " + e, nil
	}
	if cfg == nil || plan.local.ImageCatalog == nil || len(plan.local.ImageCatalog.Entries) == 0 {
		plan.imageMap = plan.imageNow
		return "", "left out: this machine cannot read " + imageWhyMissing(cfg, plan.local) + ", so nothing here chooses an entry — " + imageMapCommand("<repo>", "<entry>") + " writes the mapping", nil
	}
	var repos []catalog.Repo
	for _, name := range plan.entry.Workflows {
		w, ok := cfg.Workflows[name]
		if !ok {
			continue
		}
		repos = append(repos, catalog.Repo{Slug: name, Pin: w.ImageName, Needs: w.Needs})
	}
	if len(repos) == 0 {
		plan.imageMap = plan.imageNow
		return "", "left out: the checkout's fugaro.yaml names none of the workflows this project runs, so no entry is selected here", nil
	}
	choice, err := catalog.Choose(catalog.Job{Catalog: *plan.local.ImageCatalog, Repos: repos, Backend: catalog.Backend(plan.local.Backend())})
	if err != nil {
		plan.imageMap = plan.imageNow
		return "", "left out: " + oneLine(err.Error()) + " — " + imageMapCommand("<repo>", "<entry>") + " writes the mapping", nil
	}
	next := imageMapWith(plan.local, plan.canonical, choice.Entry.Name)
	plan.imageMap = next.ImageMap
	return choice.Entry.Name, "", nil
}

// imageWhyMissing is which read was missing, in the words the person reading
// the plan can act on.
func imageWhyMissing(cfg *config.Config, lc *localcfg.Config) string {
	var missing []string
	if cfg == nil {
		missing = append(missing, "its workflow list")
	}
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) == 0 {
		missing = append(missing, "this project's image catalog")
	}
	return strings.Join(missing, " or ")
}

// allowStepWhy is the allow-list step's line: the entry the node already
// holds, or the list the write leaves.
func allowStepWhy(have []string, canonical string) string {
	if slices.Contains(have, canonical) {
		return "already on the allow-list of " + projectpolicy.PathShared()
	}
	after := append(slices.Clone(nonNil(have)), canonical)
	slices.Sort(after)
	return fmt.Sprintf("allow_repos: %v → %v", nonNil(have), after)
}

// reposEnrolmentPlan prints the whole list of what the command will change, in
// the order the enrolment is read in: the local config entry, the image map,
// the allow-list, the publish, and the job with its invoker grant.
//
// It is printed BEFORE the one question and it is the whole of what the
// question is about: a plan that named fewer steps than the command runs would
// make the yes it asks for a yes to something else.
func reposEnrolmentPlan(w io.Writer, lc *localcfg.Config, plan reposEnrolment) {
	fmt.Fprintf(w, "enrolling %s in project %s: this will change\n", pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name))
	image := "image_map[" + plan.canonical + "] = " + pluginwire.Printable(plan.imageEntry)
	switch {
	case plan.imageEntry == "":
		image = plan.imageWhy
	case plan.imageSeeded:
		image += " (added to the map the published node holds, which this file does not name)"
	}
	lines := []string{
		"local config: " + plan.entryWhy,
		"image map: " + image,
		"allow-list: " + plan.allowWhy,
		"publish: the policy node " + projectpolicy.PathShared() + ", from this machine's config (fugaro update's policy stage)",
		"job: " + reposEnrolmentJobLine(plan),
	}
	for i, l := range lines {
		fmt.Fprintf(w, "  %d. %s\n", i+1, l)
	}
	// The build is named because it is the one thing enrolment needs and this
	// command does NOT do: the question below covers the five steps, and a
	// person who read "the job" as "its image too" would be surprised by a bill
	// nobody approved. `fugaro update --repo` leaves the same build for the
	// same reason.
	fmt.Fprintf(w, "the first image build is not part of this: it is billable and it waits for `fugaro image build <entry>`\n")
}

// reposEnrolmentJobLine is the job step's line: what it does here, or the one
// command that does it where the checkout is.
func reposEnrolmentJobLine(plan reposEnrolment) string {
	if plan.checkout == "" {
		return "left for you: this machine has no checkout of " + plan.canonical + ", and a job is deployed from one — run `fugaro update --repo` in its checkout"
	}
	return "deploy the repository's job set and its roles/run.invoker grant on the credentials service, from the checkout at " + safetext.Field(plan.checkout)
}

// reposEnrolmentWhat is the sentence the one question is about.
func reposEnrolmentWhat(lc *localcfg.Config, plan reposEnrolment) string {
	job := "deploy its job set and its run.invoker grant"
	if plan.checkout == "" {
		job = "deploy nothing here (this machine has no checkout of it: fugaro update --repo in one creates its job)"
	}
	return fmt.Sprintf("enrol %s in project %s: write its repos: entry and its image mapping, add it to the published allow-list, publish the node from this machine's config, and %s",
		pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name), job)
}

// reposEnrolmentApply runs the five steps in the order the plan printed them.
// Each is idempotent and each says what it did: a repository already enrolled
// converges to the same state, and a step with nothing to do says so rather
// than writing the same bytes again.
func reposEnrolmentApply(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, db *rtdb.Client, configPath string, plan reposEnrolment) error {
	out := cmd.OutOrStdout()
	cur, err := reposEnrolmentWriteLocal(cmd, out, o, plan)
	if err != nil {
		return err
	}
	if err := reposEnrolmentWriteAllow(ctx, out, configPath, lc, db, plan); err != nil {
		return err
	}
	return reposEnrolmentStages(ctx, cmd, o, cur, plan)
}

// reposEnrolmentWriteLocal is steps 1 and 2: the `repos:` entry and the image
// map, in this machine's project config. They land in one document, so the
// second write is made from the file the first one left, and a step with
// nothing to change writes nothing at all (userSave is not reached).
//
// It answers the config the file holds when the steps are done, which is what
// the stages below read: they are `fugaro update`'s, and that command reads
// the file.
func reposEnrolmentWriteLocal(cmd *cobra.Command, out io.Writer, o *projectReposOptions, plan reposEnrolment) (*localcfg.Config, error) {
	cur, old := plan.local, plan.old
	changed := false
	if !plan.entryKnown || !reposEntryEqual(plan.entryNow, plan.entry) {
		next := *cur
		next.Repos = maps.Clone(cur.Repos)
		if next.Repos == nil {
			next.Repos = map[string]localcfg.Repo{}
		}
		next.Repos[plan.ownerName] = plan.entry
		if err := userSave(out, plan.path, old, &next); err != nil {
			return nil, err
		}
		cur, changed = &next, true
		fmt.Fprintf(out, "repos.%s: %s\n", plan.ownerName, reposEntryLine(plan.entry))
	}
	if plan.imageEntry != "" && !maps.Equal(plan.imageNow, plan.imageMap) {
		next := *cur
		next.ImageMap = plan.imageMap
		data, err := cur.Marshal()
		if err != nil {
			return nil, err
		}
		if err := userSave(out, plan.path, data, &next); err != nil {
			return nil, err
		}
		cur, changed = &next, true
		fmt.Fprintf(out, "image_map[%s] = %s\n", plan.canonical, pluginwire.Printable(plan.imageEntry))
		if plan.imageSeeded {
			fmt.Fprintf(out, "note: this machine's project config names no repo-to-image map, so the mapping is written beside the one the published node holds rather than over it\n")
		}
	}
	if !changed {
		fmt.Fprintf(out, "%s is already as this enrolment wants it\n", safetext.Field(plan.path))
	}
	// The stages read the file, not this value, and they read it through the
	// loader: the one read that says the document is what this command thinks
	// it is.
	written, err := localcfg.Load(plan.path)
	if err != nil {
		return nil, userErr("reading back %s: %v", safetext.Field(plan.path), err)
	}
	return written, nil
}

// reposEnrolmentWriteAllow is step 3: one entry in the published allow-list,
// through the same ETag-guarded write every other editor of the node uses
// (putAllowRepos). The node is re-read here rather than taken from the plan:
// the plan was printed for a person to read, and the ETag the write is
// guarded by has to be the one the node holds now.
func reposEnrolmentWriteAllow(ctx context.Context, out io.Writer, configPath string, lc *localcfg.Config, db *rtdb.Client, plan reposEnrolment) error {
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return userErr("the policy node %s is gone: %s was written to the local config, and the allow-list entry was not — publish one ('fugaro project policy publish') and run this again", projectpolicy.PathShared(), pluginwire.Printable(plan.ownerName))
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
		fmt.Fprintf(out, "%s is already on the allow-list; nothing changed there\n", plan.canonical)
		return nil
	}
	allow := append(nonNil(p.AllowRepos), plan.canonical)
	slices.Sort(allow)
	return putAllowRepos(ctx, out, configPath, lc, db, etag, raw, before, nonNil(allow))
}

// reposEnrolmentStages is steps 4 and 5: the publish and the deploy, through
// `fugaro update`'s own registered stages and nothing else.
//
// The repository stage is selected only when this machine holds a checkout of
// the repository: it converges the WORKING DIRECTORY's checkout, so asking it
// for a repository that is not here would leave the stage needs-you and end
// the command on it — after the four steps above had already landed. A
// repository enrolled from elsewhere says one line naming the command, which
// is the same corner `fugaro accounts add` names (accountsAddUnconverged).
//
// The image build is off (noBuild), and that is the whole of the reason the
// one confirmation is about what it is: `fugaro update --repo` offers the
// first build behind its own billable confirmation, and a single question
// about enrolment may not stand for a bill.
func reposEnrolmentStages(ctx context.Context, cmd *cobra.Command, o *projectReposOptions, lc *localcfg.Config, plan reposEnrolment) error {
	run := stepRunFor(cmd, "project-repos-add", cmd.ErrOrStderr())
	defer run.close()
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	stages, err := reposEnrolmentRunStages(ctx, cmd, run, lc, plan)
	if err != nil {
		return err
	}
	reposEnrolmentReport(cmd, lc, plan, stages)
	return nil
}

// reposEnrolmentRunStages invokes the Policy and Installation stages, and the
// Repository stage when there is a checkout to converge, in-process.
//
// The order is the registered order and not this file's: initflow.Validate
// sorts the selection into it, so the job set is deployed against the
// installation before the checkout's own root is converged, and the policy
// node — which every run of the project is admitted by — is published last,
// after the installation it is read against.
//
// The run's own confirmation is this command's, which the person has already
// answered, which is why the loop is given Yes; it carries asJSON so the
// stages and the loop print through the run's own writer, and the command's
// stdout is pointed at stderr for the length of the run, so a stage that
// prints to cmd.OutOrStdout() directly — the policy stage's diff — cannot land
// on the answer.
func reposEnrolmentRunStages(ctx context.Context, cmd *cobra.Command, run *stepRun, lc *localcfg.Config, plan reposEnrolment) (map[string]initflow.StageResult, error) {
	path := plan.path
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("reading %s: %v", safetext.Field(path), err)
	}
	o := &initOptions{door: "project repos add", yes: true, nonInteractive: true, asJSON: true, noBuild: true}
	r := newInitRun(cmd, o)
	r.rep, r.repLog = run.rep, run.log
	r.setProject(lc)
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return nil, err
	}
	defer unlock()
	spec, err := installOptions(o, lc)
	if err != nil {
		return nil, err
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	opts := e.options()
	opts.ReportBorrowed = true
	res, err := initflow.Run(ctx, reposEnrolmentStageList(e, plan.checkout != ""), opts)
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
	outMap := make(map[string]initflow.StageResult, len(res.Stages))
	for _, st := range res.Stages {
		outMap[st.Name] = st
	}
	return outMap, nil
}

// reposEnrolmentStageList is the run's stage list, built from initStages —
// the registered list — so this command invokes the same stages `fugaro
// update` does rather than a copy of them.
func reposEnrolmentStageList(e *initEngine, withRepo bool) []initflow.Stage {
	var out []initflow.Stage
	for _, st := range initStages(e) {
		switch st.Name() {
		case initflow.Policy, initflow.Installation:
			out = append(out, st)
		case initflow.Repository:
			if withRepo {
				out = append(out, st)
			}
		}
	}
	return out
}

// reposEnrolmentReport is the command's answer: one line per step, read off
// what happened rather than off what the plan said would happen.
func reposEnrolmentReport(cmd *cobra.Command, lc *localcfg.Config, plan reposEnrolment, stages map[string]initflow.StageResult) {
	w := cmd.OutOrStdout()
	row := func(label, value string) { fmt.Fprintf(w, "%-12s%s\n", label, value) }
	row("local config", plan.entryWhy)
	if plan.imageEntry != "" {
		row("image map", "image_map["+plan.canonical+"] = "+pluginwire.Printable(plan.imageEntry))
	} else {
		row("image map", plan.imageWhy)
	}
	row("allow-list", plan.allowWhy)
	row("publish", enrolStageLine(plan, stages[initflow.Policy], func() string {
		return "the node " + projectpolicy.PathShared() + " carries this repository's admission and this machine's config"
	}))
	row("job", enrolStageLine(plan, stages[initflow.Installation], func() string {
		return "the project's job set (fugaro update's installation stage)"
	}))
	switch {
	case plan.checkout == "":
		fmt.Fprintf(w, "%s has no checkout here, so its own repository root was not converged: run `fugaro update --repo` in one — that is what creates its job and its run.invoker grant\n", pluginwire.Printable(plan.canonical))
	default:
		if st, ok := stages[initflow.Repository]; ok {
			row("repository", enrolStageLine(plan, st, func() string { return "the checkout's own root" }))
		}
	}
	fmt.Fprintf(w, "enrolled %s in project %s: fugaro run --repo %s is ready once its job is deployed\n", pluginwire.Printable(plan.canonical), pluginwire.Printable(lc.Name), plan.canonical)
}

// stageLine is one stage's answer in the report: the stage's own words for
// every state but the one that applied, and the caller's sentence for that
// one.
func enrolStageLine(_ reposEnrolment, st initflow.StageResult, applied func() string) string {
	switch st.State {
	case initflow.Changed:
		return applied()
	case "":
		return "not run: the stage reported no result"
	}
	return string(st.State) + ": " + oneLine(st.Detail)
}
