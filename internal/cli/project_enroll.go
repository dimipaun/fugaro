package cli

// `fugaro project repos` and `fugaro project sync`: enrollment as data (plan
// task 4.1, docs/design/project-centric.md §4.3). A repository is tied to a
// project by three things, all data — its `project:` line, the project's
// allow-list, and the App's installation — and this file is the allow-list's
// own commands: `ls` prints it against what the installation has seen run,
// `add`/`rm` write one entry each, and `sync` offers what the App is installed
// on and the list does not name.
//
// Nothing here ever enrolls a repository on its own: `add` and `sync` are
// operator acts, confirmed, and a name a run wrote (the bucket's runs/<slug>/
// prefixes, Firestore's spendDaily) is printed as *seen*, never taken as an
// enrollment (B3: a slug is not invertible, and under phase 3's broadened
// runs/ grant a run of a profile can write those prefixes).

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// newProjectReposCmd is `fugaro project repos`: the interim enrollment path
// between phases 1 and 4 (P4, 12a item 3). `sync` and the discovery surface
// stay in phase 4 (task 4.1).
func newProjectReposCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "The project's allow-list: which repositories may run",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectReposLsCmd(), newProjectReposAddCmd(), newProjectReposRmCmd())
	return cmd
}

type projectReposOptions struct {
	cloud cloudOptions
	yes   bool
}

func newProjectReposLsCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the allow-list and the repositories running now",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectReposLs(cmd, &o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposAddCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "add <provider/owner/name | owner/name | *>",
		Short: "Enroll a repository: add it to the policy's allow-list",
		Long: "Add a canonical repository (github/acme/app, bitbucket/acme/*) to the\n" +
			"published policy's allow_repos, through the same ETag-guarded write the\n" +
			"publisher uses, and print the change. A bare owner/name takes its provider\n" +
			"from this machine's project config, and * is the list's own entry for every\n" +
			"repository: it is written as it is. Only allow_repos changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], false) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposRmCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "rm <provider/owner/name | owner/name | *>",
		Short: "Un-enroll a repository: remove it from the allow-list",
		Long: "Remove an entry from the published policy's allow_repos. Removing an allowed\n" +
			"repository stops its runs — a run of a repository the list does not admit is\n" +
			"refused before it mints a git token — unless another entry on the list still\n" +
			"admits it, which is the case a pattern makes (github/acme/app stays admitted\n" +
			"while github/acme/* is there); the confirmation says which of the two it is.\n" +
			"Removing * refuses every repository the rest of the list does not admit.\n" +
			"So it asks you to type the project's name, and while a run is in flight --yes\n" +
			"alone is not enough.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], true) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the removal without typing the project's name (not while a run is in flight)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectReposLs(cmd *cobra.Command, o *projectReposOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return policyReadErr(err)
	}
	if !found {
		return userErr("no policy is published for project %s (%s): enroll repositories by publishing one ('fugaro project policy publish'), which seeds the allow-list from this machine's config", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseUnknownPolicyKeys(p); err != nil {
		return err
	}
	sources, err := allowSources(ctx, lc)
	if err != nil {
		return err
	}
	running, err := runningRepos(ctx, db, lc)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "allow_repos (%s):\n", projectpolicy.PathShared())
	if len(p.AllowRepos) == 0 {
		fmt.Fprintln(out, "  (empty: a published policy with an empty allow-list admits no repository at all)")
	}
	for _, r := range p.AllowRepos {
		fmt.Fprintf(out, "  %s%s\n", r, sourceSuffix(sources[r]))
	}
	fmt.Fprintln(out, "repositories running now (from the run registry):")
	if len(running) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range running {
		mark := ""
		if !projectpolicy.AdmitRepo(p.AllowRepos, r) {
			mark = notAdmittedNote(r)
		}
		fmt.Fprintf(out, "  %s%s\n", r, mark)
	}
	return nil
}

// runProjectReposEdit adds or removes one allow_repos entry. It edits the
// node's allow_repos and nothing else: the rest of the published policy is
// written back as it was read, under the ETag the read returned, so a
// concurrent publisher is refused rather than overwritten.
//
// It is a writer, so it takes every refusal `policy publish` takes before it
// writes anything: the shared config and a database that is not this
// project's (openProjectBudget, whose comment above says why), and the
// deployed rules marker (requirePolicyRules) — a policy written to a
// deployment whose marker is absent or older than this Fugaro is one the runs
// it bounds cannot read, and an edit to the allow-list is a publish of the
// list. Without that last gate `repos add`/`repos rm` were the one way to
// write the shared node the runner reads on a deployment `policy publish`
// would refuse to touch.
func runProjectReposEdit(cmd *cobra.Command, o *projectReposOptions, repo string, remove bool) error {
	ctx := cmd.Context()
	// openProjectBudget, not openBudget: this writes the published policy node,
	// so it is a writer like `policy publish` and takes the same two refusals —
	// the shared config is not an operator's own installation (and its repos:
	// map is not the one a bare owner/name resolves against), and the database
	// must be this project's.
	lc, db, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	// The same read `policy publish` makes first, and for the same reason: the
	// marker says the deployed rules are new enough to read what is written.
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	canonical, err := canonicalRepoArg(lc, repo)
	if err != nil {
		return err
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return userErr("no policy is published for project %s (%s): 'fugaro project repos add' edits the published allow-list, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	// before is the list as the node holds it, under its own allocation: allow
	// is edited in place (slices.DeleteFunc shifts and clears the backing
	// array; append and Sort write it), and nonNil hands back the argument
	// itself, so the two would otherwise be one list and the diff line would
	// print the post-edit list on both sides — on a command whose point is
	// showing the operator what the edit removed.
	before := append([]string{}, nonNil(p.AllowRepos)...)
	allow := nonNil(p.AllowRepos)
	have := slices.Contains(p.AllowRepos, canonical)
	if remove && !have {
		// A name that is not an entry can still be admitted by a pattern on the
		// list, and then the removal removes nothing and refuses nothing: saying
		// only "nothing to remove" would read as if the repository had stopped
		// running.
		for _, pattern := range p.AllowRepos {
			if projectpolicy.MatchRepo(pattern, canonical) {
				return userErr("%s is not an entry of the allow-list of %s; the pattern %s admits it, so its runs keep running — remove that pattern to refuse them",
					canonical, projectpolicy.PathShared(), pattern)
			}
		}
		return userErr("%s is not on the allow-list of %s: nothing to remove", canonical, projectpolicy.PathShared())
	}
	if !remove && have {
		fmt.Fprintf(out, "%s is already on the allow-list; nothing changed\n", canonical)
		return nil
	}
	if remove {
		inflight, err := runsInFlight(ctx, db, lc)
		if err != nil {
			return err
		}
		what := removalWarning(allow, canonical)
		switch {
		case inflight > 0:
			// --yes was typed before the run started, so it is not the
			// confirmation here: the project's name is.
			if err := confirmRemovalStrict(cmd, lc, fmt.Sprintf("%s; %d run(s) are in flight", what, inflight)); err != nil {
				return err
			}
		default:
			if err := confirmRemoval(cmd, lc, o.yes, what); err != nil {
				return err
			}
		}
		allow = slices.DeleteFunc(allow, func(r string) bool { return r == canonical })
	} else {
		allow = append(allow, canonical)
		slices.Sort(allow)
	}
	allow = nonNil(allow)
	// The node is patched, not rewritten: only allow_repos changes, and a key
	// this Fugaro does not know inside `routing` — a newer release's, where
	// Decode cannot list it — stays exactly as it was (patchPolicyJSON).
	// Providers entries are handled above: an unknown key inside one is a
	// refusal, not something to preserve.
	patched, err := patchPolicyJSON(raw, "allow_repos", allow)
	if err != nil {
		return userErr("editing %s: %v; nothing was written", projectpolicy.PathShared(), err)
	}
	if err := checkPolicyBytes(patched); err != nil {
		return err
	}
	// The line is printed after the write, not before it: it is a claim about
	// the node, and a PutIfMatch that comes back refused (the policy changed
	// while this ran, or the credential may not write it) left the list
	// exactly as it was — an operator who read "allow_repos: [a] → [a b]" on a
	// failed write would take the entry for enrolled.
	if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(patched)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy changed while this ran; nothing was written: run it again")
		}
		return dbErr(lc, err, true)
	}
	fmt.Fprintf(out, "allow_repos: %v → %v\n", before, allow)
	fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
	return nil
}

// removalWarning is what `repos rm` asks the operator to confirm. Removing an
// entry refuses the repository's runs — a run of a repository the list does not
// admit is stopped before it mints a git token — but only if nothing else on
// the list still admits it: `github/acme/*` admits `github/acme/app` whatever
// happens to the concrete entry, and a confirmation that said "its runs are
// refused from now on" would be telling the operator a removal stops runs it
// does not stop. Admission is the list's own matcher (projectpolicy.AdmitRepo,
// which the runner and the launcher use), so the sentence is what the next run
// will actually do.
func removalWarning(allow []string, canonical string) string {
	if canonical == wildcardEntry {
		// The wildcard admits every repository, so what the removal refuses is
		// a set — "every repository the rest of the list does not admit", the
		// rule the command's own help states — and not one repository's runs.
		// admittedBy cannot answer this one either: it asks whether a *string*
		// is admitted, and no pattern matches the string "*". Saying "its runs
		// are refused from now on" over a list that still holds github/acme/*
		// would promise a stop the removal does not make.
		rest := entriesExcept(allow, canonical)
		if len(rest) == 0 {
			return "this removes " + canonical + " from the allow-list: it was the only entry, so runs are refused from now on, and a run in flight keeps the policy until it finishes"
		}
		owns := "their own"
		if len(rest) == 1 {
			owns = "its own"
		}
		return "this removes " + canonical + " from the allow-list: a repository is refused only if no other entry admits it, and " + strings.Join(rest, ", ") + " still admits " + owns + ", and a run in flight keeps the policy until it finishes"
	}
	if still, by := admittedBy(allow, canonical); still {
		return "this removes " + canonical + " from the allow-list, but " + by + " still admits it: its runs are not refused, and a run in flight keeps the policy until it finishes"
	}
	return "this removes " + canonical + " from the allow-list: its runs are refused from now on, and a run in flight keeps the policy until it finishes"
}

// wildcardEntry is the allow-list's own entry for every repository
// (projectpolicy.ValidPattern has a branch for it, and it is the one entry that
// is not a repository at all), so removing it is a question about the set the
// rest of the list leaves rather than about one repository.
const wildcardEntry = "*"

// entriesExcept is allow without entry: the list a removal leaves.
func entriesExcept(allow []string, entry string) []string {
	rest := make([]string, 0, len(allow))
	for _, p := range allow {
		if p != entry {
			rest = append(rest, p)
		}
	}
	return rest
}

// admittedBy is the entry of allow that still admits arg once arg's own entry
// is gone — the list the removal leaves, read with the same matcher the runner
// and the launcher use (projectpolicy.MatchRepo), so the sentence above is what
// the next run will actually do.
//
// One matcher answers both questions a removal raises. A concrete repository is
// admitted by any remaining entry that matches it: the wildcard, or a pattern
// such as github/acme/*. A *pattern* being removed is moot only if some
// remaining entry admits every repository it admitted — and MatchRepo answers
// that too, because it matches its second argument as a *repository*: an entry
// that matches the string github/acme/* is one whose own segments match
// "github", "acme" and "*", which is exactly what admitting github/acme/<any>
// takes. github/*/* matches it, github/ac*/* matches it (github/ac* is every
// owner whose name starts with "ac"), and github/*/app does not — its middle
// segment would have to match the literal string "*". The reading is sound in
// the direction that matters: a pattern that matches a string matches every
// expansion of it, since a "*" stands for any run of characters within one
// segment, so no entry can be reported as still admitting a repository the
// removal really does refuse.
//
// That soundness is a property of the matcher, not of this function, and it
// takes the "*" the second argument may carry being matched as the character
// it stands for: github/acme/*app removed while github/acme/* remains is not a
// revocation, and the matcher reads it that way only because a pattern's
// wildcard is tried before literal equality (projectpolicy.matchSeg).
//
// An entry is a pattern when it carries a wildcard, and that is not a question
// for this function: MatchRepo reads a concrete repository and a pattern alike,
// because ValidPattern accepts a concrete name too (a pattern with no "*"
// matches exactly itself) — so github/acme/app removed while github/acme/* is on
// the list reads "its runs are not refused", which is what the removal does.
func admittedBy(allow []string, arg string) (bool, string) {
	for _, p := range entriesExcept(allow, arg) {
		if projectpolicy.MatchRepo(p, arg) {
			return true, p
		}
	}
	return false, ""
}

// configRepo finds this machine's config entry for a bare owner/name, folding
// case the way canonicalRepoArg does. The config's repos: map is keyed by the
// operator's own spelling and the loader does not fold it, so the exact lookup
// this used to be made `repos add Acme/App` refuse where
// `repos add github/Acme/App` worked, although the doc above promises the
// fold (the review finding on this change). The exact spelling is tried first,
// so an entry the operator wrote with upper-case letters is read as written
// where the argument is written the same way.
func configRepo(lc *localcfg.Config, arg string) (localcfg.Repo, bool) {
	if r, ok := lc.Repos[arg]; ok {
		return r, true
	}
	folded := asciiLower(arg)
	for name, r := range lc.Repos {
		if asciiLower(name) == folded {
			return r, true
		}
	}
	return localcfg.Repo{}, false
}

// canonicalRepoArg is the operator's argument as a canonical allow-list
// entry. A bare owner/name takes its provider from this machine's config
// (repos: is keyed by owner/name); anything else must already be canonical,
// which is what the allow-list matches, at whatever depth the repository path
// has (a nested group is a repository path like any other). Upper-case letters
// are folded — ASCII only, and a concrete name goes through
// projectpolicy.Canonical — so the entry written is one the runner's own
// canonical form could have produced. The config lookup folds with the rest
// (configRepo): the repos: map is keyed by the operator's own spelling, which
// this does not require to be lower-case, so `acme/app` and `Acme/App` find
// each other's entry instead of the second spelling refusing on a
// case-sensitive lookup.
func canonicalRepoArg(lc *localcfg.Config, arg string) (string, error) {
	if arg == "*" {
		// The allow-list's own entry for every repository — the design's
		// explicit choice (docs/design/project-centric.md §4.3), which
		// MatchRepo reads as admitting anything. It is not a repository, so
		// neither the config lookup below (there is no owner/name to resolve a
		// provider from) nor projectpolicy.Canonical applies to it: it is
		// taken as it is — projectpolicy.ValidPattern has a branch of its own
		// for it, so there is nothing left to check here.
		// Without this branch it fell through the split to "write it as
		// provider/owner/name", so the widest list could not be written and a
		// list holding it could not be narrowed without a full republish.
		return arg, nil
	}
	parts := strings.Split(arg, "/")
	if len(parts) == 2 {
		r, ok := configRepo(lc, arg)
		if !ok || r.Provider == "" {
			return "", userErr("%q is not a repository this machine's config lists (its provider would come from there), so name it canonically: provider/owner/name, as in \"github/acme/app\" or \"bitbucket/acme/*\"", arg)
		}
		parts = []string{r.Provider, parts[0], parts[1]}
	}
	if len(parts) < 3 {
		return "", userErr("%q is not a repository: write it as provider/owner/name (github/acme/app), or as owner/name for a repository this machine's config lists", arg)
	}
	// Everything after the provider is the repository path, whatever its depth:
	// a repository may nest (task.CanonicalRepo admits "acme/group/app", and
	// projectpolicy's own Canonical pins it as valid), so
	// bitbucket/acme/group/app is the argument that allow-lists it — the form
	// ValidPattern refuses to cap and MatchRepo matches segment against segment
	// (the review finding on this change). Reading the path as exactly two
	// segments refused it with "write it as provider/owner/name", the same way
	// the cap did, and left the wildcard form as the only way to allow-list a
	// nested repository.
	provider, repo := asciiLower(parts[0]), asciiLower(strings.Join(parts[1:], "/"))
	canonical := provider + "/" + repo
	if !strings.Contains(canonical, "*") {
		// A concrete repository goes through projectpolicy.Canonical, the one
		// implementation of the form the runner compares an allow-list entry
		// against. A pattern is not a repository (Canonical would refuse its
		// "*"), so it is left to ValidPattern below.
		c, err := projectpolicy.Canonical(provider, repo)
		if err != nil {
			return "", userErr("%q is not a repository: %v", arg, err)
		}
		canonical = c
	}
	if err := projectpolicy.ValidPattern(canonical); err != nil {
		return "", userErr("%v", err)
	}
	return canonical, nil
}

// asciiLower lower-cases A–Z only, which is task's own rule for a repository
// (task.asciiLower, unexported there, reached through
// projectpolicy.Canonical). strings.ToLower case-folds Unicode instead: it
// turns a Kelvin sign K (U+212A) into an ASCII "k", so an argument typed with
// one would come out of here looking canonical while the runner's Canonical —
// ASCII-only, and the form the allow-list is matched in — refuses to produce
// that name at all. With this fold the look-alike survives into
// ValidPattern/Canonical, which refuse it.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
