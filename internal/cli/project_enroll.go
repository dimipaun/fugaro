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
//
// The interim path 1.6 opened (P4, 12a item 3) is closed here: a new repository
// needs `repos add` or a confirmed `sync`, no Terraform and no update --repo.

import (
	"bufio"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// newProjectReposCmd is `fugaro project repos`: the project's allow-list as
// data. `ls` prints it against what this installation has seen run, and
// `add`/`rm` edit one entry each (task 1.6's write path, unchanged).
// `project sync` — what the App is installed on — is its sibling.
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
	// The Short names the list this command prints, never "running now": that
	// is `project show`'s list — the run registry's runs in flight — while
	// what is printed here is the durable seen, the runs bucket's
	// runs/<slug>/ prefixes and spendDaily, which outlive every run.
	// docs/project-layer.md §7 words the contrast ("the durable answer is one
	// command away rather than in that output") and the sibling add/rm
	// commands point at it.
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the allow-list and the repositories that have run here",
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
	// The seen set is read before the node is printed: it is the half of this
	// command that can fail on the way to the data (the bucket's marker, the
	// Firestore mark), and a listing that printed half of itself before
	// refusing would read as one that had finished.
	seen, notes, err := seenRepos(ctx, lc, budgetNow())
	if err != nil {
		return err
	}
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
	fmt.Fprintf(out, "allow_repos (%s):\n", projectpolicy.PathShared())
	if len(p.AllowRepos) == 0 {
		fmt.Fprintln(out, "  (empty: a published policy with an empty allow-list admits no repository at all)")
	}
	for _, r := range p.AllowRepos {
		fmt.Fprintf(out, "  %s%s\n", r, sourceSuffix(sources[r]))
	}
	fmt.Fprintln(out, seenHeader)
	for _, n := range notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	if len(seen) == 0 {
		fmt.Fprintln(out, "  (none: no run of any repository has been recorded here)")
	}
	// The listing is bounded twice, and both bounds are needed. One entry is
	// printableName (bounded to one inert line, and distinct from every other
	// name, so the entry the allow-list refuses cannot print as the one it
	// admits): every value on this list was written by a run, not by this
	// program — a runs/<slug>/ prefix is a directory name in the runs bucket
	// and spendDaily's slug is a field of a document the rollup writes, and
	// neither path checks the shape of the name. Raw, an escape sequence in
	// one rewrites the operator's screen, a newline adds a line that reads as
	// another entry, and a long one pushes the note off the column. And the
	// count is bounded (safetext.FieldListMax) because the list is as long as
	// whoever wrote those names made it: a listing that grew with its input
	// would push this command's own words — the header, the sources, the way
	// to enroll — off the screen. What is left out is counted in this
	// program's own words (safetext.FieldListNote), so a listing that stopped
	// at twenty does not read as a list of twenty. The admission question
	// above is asked of the name itself; only what is printed is bounded.
	for i, r := range seen {
		if i == safetext.FieldListMax {
			fmt.Fprintf(out, "  (%s)\n", safetext.FieldListNote(len(seen)-i))
			break
		}
		fmt.Fprintf(out, "  %s%s\n", printableName(r.Name), seenNote(r, projectpolicy.AdmitRepo(p.AllowRepos, r.Name)))
	}
	return nil
}

// seenHeader introduces the seen half of `project repos ls`. It names both
// sources because the list is their union: an operator reading an entry has to
// know which of them still holds it — the bucket keeps a repository's runs
// until its objects go, spendDaily only the days inside the window read below.
const seenHeader = "repositories seen here (the runs bucket's runs/<slug>/ prefixes and Firestore's spendDaily):"

// seenWindowDays is how far back spendDaily is read. The collection holds one
// document per (day, repository) and only a finished day's document is
// durable, so the window bounds a query rather than the history: a repository
// whose runs are older than it is still named by the runs bucket, which is
// the source that keeps them.
const seenWindowDays = 30

// seenRepo is one repository this installation has seen a run of, and where
// that was read.
type seenRepo struct {
	// Name is the canonical name ("github/acme/app") when this machine's
	// config names the repository, and the storage slug otherwise: a slug is
	// a hash of the provider and the canonical name (task.Slug) and cannot be
	// turned back into one.
	Name string
	// Provider is the git provider the name carries, and "" for a slug:
	// nothing durable records which provider a slug was hashed from.
	Provider string
	// Sources are the durable sources that recorded a run of it, in the
	// order they are printed.
	Sources []string
}

// seenRepos reads the two durable sources of "which repositories have run
// here": the runs bucket's runs/<slug>/ prefixes and Firestore's spendDaily.
//
// The notes are what one of them could not answer — a project with no spend
// history, a Firebase project whose database has no mark — and never a reason
// to print nothing: the allow-list this command exists for is printed either
// way, and a note that says which half is missing is what keeps an empty seen
// list from reading as "nothing has ever run here".
//
// It writes nothing. The seen set is derived (design §4.3: "no registration
// record" — no principal on this path may write one) and a listing that wrote
// one would be the record the design refused.
func seenRepos(ctx context.Context, lc *localcfg.Config, now time.Time) ([]seenRepo, []string, error) {
	bySlug := map[string]*seenRepo{}
	add := func(slug, source string) {
		if slug == "" {
			return
		}
		r, ok := bySlug[slug]
		if !ok {
			r = &seenRepo{Name: slug, Sources: []string{source}}
			bySlug[slug] = r
			return
		}
		if !slices.Contains(r.Sources, source) {
			r.Sources = append(r.Sources, source)
		}
	}
	// The runs bucket: the durable record of the runs themselves. It is
	// opened the way every cloud command opens it, checkCloudName included —
	// a config pointing at another project's bucket is refused rather than
	// listed, which is a refusal this command must not swallow to show an
	// allow-list.
	slugs, err := runsBucketSlugs(ctx, lc, now)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range slugs {
		add(s, sourceRunsBucket)
	}
	recs, why, err := spendRepos(ctx, lc, now)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	if why != "" {
		notes = append(notes, why)
	}
	for _, r := range recs {
		// spendDaily's own repo field is a display name ("acme/app") with no
		// provider, so it is not carried into the answer: a name this command
		// printed would be read as one `repos add` takes, and a bare
		// owner/name resolves only through this machine's config — the one
		// source that answers below, already.
		add(r.Slug, sourceSpendDaily)
	}
	// A slug is resolved only through this machine's config (repoBySlug,
	// which pairs a slug with the provider only the config knows): the
	// canonical form an allow-list entry needs cannot be recovered from the
	// slug, which is why an unresolved entry is printed as the slug it is.
	known := repoBySlug(lc)
	out := make([]seenRepo, 0, len(bySlug))
	for _, s := range slices.Sorted(maps.Keys(bySlug)) {
		r := *bySlug[s]
		if c, ok := known[budget.Key(s)]; ok {
			r.Name, r.Provider = c, providerOf(c)
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b seenRepo) int { return strings.Compare(a.Name, b.Name) })
	return out, notes, nil
}

// The two sources, as the listing names them.
const (
	sourceRunsBucket = "the runs bucket"
	sourceSpendDaily = "spendDaily"
)

// runsBucketSlugs is the bucket's repositories: one runs/<slug>/ prefix per
// repository that has run, which is what a run's objects are stored under.
func runsBucketSlugs(ctx context.Context, lc *localcfg.Config, now time.Time) ([]string, error) {
	if lc.BucketURL() == "" {
		return nil, userErr("project %s's config names no runs bucket (bucket_url), so the repositories that have run here cannot be listed", lc.Name)
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return nil, remote(err)
	}
	defer b.Close()
	if err := checkCloudName(ctx, b, lc, os.Getenv, now); err != nil {
		return nil, err
	}
	slugs, err := runstore.ListSlugs(ctx, b.Bucket)
	if err != nil {
		return nil, remote(fmt.Errorf("listing the runs bucket's repositories: %w", err))
	}
	return slugs, nil
}

// spendRepos is Firestore's spendDaily over the last seenWindowDays days: one
// document per (day, repository), the durable source of what ran. why is
// non-empty when the project has no spend history to read at all — the same
// degraded answers `report` gives, so the two surfaces agree on what "no
// history" means; a read that fails is an error.
//
// The database is checked to be this project's before anything is read from
// it (the mark, exactly as reportHistory checks it): these documents name
// repositories, and a config pointing budget.firebase_project at another
// installation's Firebase project would otherwise print that installation's
// repositories as this project's.
func spendRepos(ctx context.Context, lc *localcfg.Config, now time.Time) (recs []budget.DayRecord, why string, err error) {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, "project " + lc.Name + " has no Firebase budget backend, so its spend history was not read", nil
	}
	// The client `report` reads its history with: one factory, so the two
	// cannot disagree about which credentials reach which database.
	fs, err := newReportFirestore(ctx, lc)
	if err != nil {
		return nil, "", err
	}
	doc, err := fs.Get(ctx, budget.MetaCollection, budget.MarkDocument)
	switch {
	case errors.Is(err, firestore.ErrNoDatabase):
		return nil, "the project's Firebase project has no Firestore database, so its spend history was not read", nil
	case errors.Is(err, firestore.ErrNotFound):
		return nil, "the project's Firestore database has no history mark (fugaro update --firebase-project has not set it up), so its spend history was not read", nil
	case err != nil:
		return nil, "", firestoreErr(lc, err)
	}
	if p, _ := doc.Fields["project"].(string); p != lc.Name {
		return nil, "", userErr("the Firestore database of Firebase project %s belongs to project %q, not %q; fix budget.firebase_project in project %s's config", lc.Budget.FirebaseProject, safetext.Strip(p), lc.Name, lc.Name)
	}
	today := budget.Day(now)
	docs, err := fs.Query(ctx, budget.SpendCollection, "date", budget.DayDate(today-seenWindowDays+1), budget.DayDate(today))
	if err != nil {
		return nil, "", firestoreErr(lc, err)
	}
	for _, d := range docs {
		// A document whose *fields* this Fugaro cannot read is left out rather
		// than failing the listing: it is one day of one repository — the
		// bucket still names the repository, and an operator who has to repair
		// a spendDaily document does not need this command to stop listing
		// what the allow-list holds (report totals such a document as a
		// warning because its figures are its answer; here they are not).
		//
		// What the query itself refuses is not left out, and this is the
		// distinction the sentence above must not be read as covering: a
		// document whose *name* firestore.Client's decode does not accept (not
		// exactly two segments after /documents/) fails the query, and the
		// listing reports that error and exits without printing — it fails
		// closed rather than silently listing a shorter history.
		//
		// That is no promise about the slug printed below, which is read from
		// the document's *fields* (budget.FromFields), not from its name.
		// Fugaro's own writer never stores a slug with a "/" in it (the ID is
		// <date>_<slug>, and firestore's validName refuses a "/" in an ID), but
		// a document written by hand can carry any slug field, a multi-segment
		// one included. Such a name is treated like every other seen name:
		// printed bounded and inert, and offered as a command only when
		// enrollableArg says it is a repository identifier.
		if r, err := budget.FromFields(d.Fields); err == nil {
			recs = append(recs, r)
		}
	}
	return recs, "", nil
}

// providerOf is the provider a canonical name carries.
func providerOf(canonical string) string {
	p, _, _ := strings.Cut(canonical, "/")
	return p
}

// seenNote is one seen entry's parenthetical: where it was read, what the
// allow-list does with it, and — for a name that is a slug — why it cannot be
// passed to the remedy as it stands. The refusal and the slug's meaning are
// project_cmd.go's own sentences (notAdmittedSentence, slugIsAHash), so the
// seen list and `project show` cannot word the same state differently.
func seenNote(r seenRepo, admitted bool) string {
	parts := []string{"seen in " + strings.Join(r.Sources, " and ")}
	switch {
	case !admitted:
		// The sentence carries its own slug half when the name is one.
		parts = append(parts, notAdmittedSentence(r.Name))
	case r.Provider == "":
		parts = append(parts, slugIsAHash)
	}
	return "  (" + strings.Join(parts, "; ") + ")"
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
	// SD8: this command writes the published node's allow_repos, which is what
	// admits a repository to the project at all, so it is a person's write and
	// not a coding agent's — an agent that enrolled a repository would have
	// widened what every run of the project is admitted by on its own. It is
	// the same refusal `policy publish` carries, and it comes before the read
	// and before the ETag, so nothing is written and no cloud write is even
	// reached.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro project repos add and repos rm edit the published allow-list, which is what admits a repository to the project: %s", initflow.AgentRefusal(m))
	}
	// openProjectBudget, not openBudget: this writes the published policy node,
	// so it is a writer like `policy publish` and takes the same two refusals —
	// the shared config is not an operator's own installation (and its repos:
	// map is not the one a bare owner/name resolves against), and the database
	// must be this project's. The config's path is not this command's business:
	// editing the allow-list moves no generation of the node's policy keys.
	lc, db, _, err := openProjectBudget(ctx, o.cloud)
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
	return putAllowRepos(ctx, out, lc, db, etag, raw, before, nonNil(allow))
}

// putAllowRepos is the write every editor of allow_repos ends in: the node is
// patched to hold allow, and written back under the ETag the read returned.
//
// The node is patched, not rewritten: only allow_repos changes, and a key this
// Fugaro does not know inside `routing` — a newer release's, where Decode
// cannot list it — stays exactly as it was (patchPolicyJSON). Providers entries
// are the other half of that rule, handled before this point: an unknown key
// inside one is a refusal, not something to preserve.
//
// The line is printed after the write, not before it: it is a claim about the
// node, and a PutIfMatch that comes back refused (the policy changed while this
// ran, or the credential may not write it) left the list exactly as it was — an
// operator who read "allow_repos: [a] → [a b]" on a failed write would take the
// entry for enrolled.
func putAllowRepos(ctx context.Context, out io.Writer, lc *localcfg.Config, db *rtdb.Client, etag string, raw []byte, before, allow []string) error {
	patched, err := patchPolicyJSON(raw, "allow_repos", allow)
	if err != nil {
		return userErr("editing %s: %v; nothing was written", projectpolicy.PathShared(), err)
	}
	if err := checkPolicyBytes(patched); err != nil {
		return err
	}
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
	folded := task.ASCIILower(arg)
	for name, r := range lc.Repos {
		if task.ASCIILower(name) == folded {
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
	provider, repo := task.ASCIILower(parts[0]), task.ASCIILower(strings.Join(parts[1:], "/"))
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

// ---------------------------------------------------------------- sync

// syncAppKeyFlag is the flag that allows `project sync` to read the project's
// GitHub App private key from Secret Manager. Same rule as
// --check-github-app (githubapp.go): the read is behind a flag the command
// name does not imply, so a plain `fugaro project sync` never reaches a key,
// and the flag is refused under a coding agent's marker before anything is
// read.
const syncAppKeyFlag = "--github-app-key"

// syncAppIDFlag is the flag carrying the App's ID, which is a number and not a
// secret (init's own words for it).
const syncAppIDFlag = "--github-app-id"

// projectSyncOptions are `project sync`'s flags.
type projectSyncOptions struct {
	cloud cloudOptions
	// githubAppID is the App whose installations are listed; "" takes the one
	// this machine's config holds for its repositories.
	githubAppID string
	// githubAppKey is --github-app-key: it allows the key's Secret Manager
	// read with the operator's own credentials. Without it nothing is read.
	githubAppKey bool
	// yes confirms the batch without the typed project name.
	yes bool
}

func newProjectSyncCmd() *cobra.Command {
	var o projectSyncOptions
	cmd := &cobra.Command{
		Use:   "sync [--github-app-id <id>] [--github-app-key] [--yes]",
		Short: "Offer the GitHub App's installed repositories that the allow-list does not admit",
		Long: "Ask GitHub which repositories the project's GitHub App is installed on (the\n" +
			"App API, with the App's own credentials) and offer the ones the published\n" +
			"allow_repos does not admit, as one list confirmed once. The accepted list is\n" +
			"written through the same ETag-guarded write `project repos add` uses; entries\n" +
			"are only ever added, so a sync never un-enrolls a repository — including one\n" +
			"the App is not installed on at all (another provider's, or one whose\n" +
			"installation was removed). A repository the list already admits, a pattern\n" +
			"included, is not offered: its runs are admitted already.\n\n" +
			"Reading the App's private key is --github-app-key: without it, nothing is\n" +
			"read. Under it the key is read from the project's github-app-key secret with\n" +
			"your own credentials and held in memory only to sign the App's 9-minute JWT\n" +
			"(never printed, never passed as an argument, never in an error), and the\n" +
			"flag is refused in a coding agent's session. The App ID is a number, not a\n" +
			"secret: --github-app-id, or the one this machine's config already holds.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectSync(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.githubAppID, "github-app-id", "", "the GitHub App's ID (a number, not a secret); default: the one this machine's config holds")
	f.BoolVar(&o.githubAppKey, "github-app-key", false, "read the project's GitHub App private key from Secret Manager to ask GitHub (your own credentials; refused in a coding agent's session)")
	f.BoolVar(&o.yes, "yes", false, "confirm the batch without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// runProjectSync lists the App's installed repositories and adds the ones the
// allow-list does not admit, in one confirmed batch.
//
// It is a writer of config/policy/shared, so it takes the writer's refusals
// before it reads anything: every one runProjectReposEdit takes (the shared
// config, a database that is not this project's, the deployed rules marker).
// What it never does is remove: the list it writes is the list it read plus
// what it was told to add.
func runProjectSync(cmd *cobra.Command, o *projectSyncOptions) error {
	ctx := cmd.Context()
	// SD8: sync writes the same allow_repos `repos add` writes, so it carries
	// the same refusal and it carries it first — before the App key's own
	// refusal (refuseKeyReadInAgent, which guards the secret read and not the
	// write) and before the flag's own complaint, so an agent session is told
	// the one thing that is true of every way of running this command: the
	// write is not its to make. And it comes before the GitHub call, so nothing
	// of the project leaves the machine either.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro project sync adds to the published allow-list, which is what admits a repository to the project, and --github-app-key reads the App's private key with your own credentials to ask GitHub which repositories the App is installed on: %s", initflow.AgentRefusal(m))
	}
	lc, db, _, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	appID := o.githubAppID
	if appID == "" {
		appID = anyStoredAppID(lc)
	}
	if appID == "" {
		return userErr("no GitHub App ID: this machine's config holds none for its repositories; pass %s (the number on the App's settings page, not a secret)", syncAppIDFlag)
	}
	key, err := syncAppKey(ctx, cmd, lc, o.githubAppKey)
	if err != nil {
		return err
	}
	installed, err := infra.InstalledRepos(ctx, infra.GitHubApp{AppID: appID, PrivateKey: key, BaseURL: githubAPIURL, HTTP: githubAPIClient})
	var bad *github.BadCredentialsError
	switch {
	case errors.As(err, &bad):
		return userErr("%v; the key read is the project's github-app-key secret: store the right App's .pem again with 'fugaro secrets set github-app-key --project-wide < PATH-TO-THE-PEM-FILE'", bad)
	case err != nil:
		return remote(fmt.Errorf("listing the repositories the GitHub App %s is installed on: %w", appID, err))
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return userErr("no policy is published for project %s (%s): 'fugaro project sync' adds to the published allow-list, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	// The offer is what the list does not *admit*, not what it does not spell
	// out: a repository a pattern already admits runs today, so adding its
	// concrete name would change nothing and read as an enrollment the
	// operator still had to make.
	missing := slices.DeleteFunc(slices.Clone(installed), func(name string) bool {
		return projectpolicy.AdmitRepo(p.AllowRepos, name)
	})
	if len(missing) == 0 {
		fmt.Fprintf(out, "the GitHub App %s is installed on %d repositories, and the allow-list of %s already admits every one of them; nothing to add\n", appID, len(installed), projectpolicy.PathShared())
		return nil
	}
	fmt.Fprintf(out, "the GitHub App %s is installed on %d repositories; %s does not admit %d of them:\n", appID, len(installed), projectpolicy.PathShared(), len(missing))
	for _, r := range missing {
		fmt.Fprintf(out, "  %s\n", r)
	}
	before := append([]string{}, nonNil(p.AllowRepos)...)
	allow := append(nonNil(p.AllowRepos), missing...)
	slices.Sort(allow)
	what := fmt.Sprintf("add %d repositories to the allow-list and remove none: %s", len(missing), strings.Join(missing, ", "))
	if err := confirmTyped(cmd, bufio.NewReader(cmd.InOrStdin()), lc, o.yes, what); err != nil {
		return err
	}
	return putAllowRepos(ctx, out, lc, db, etag, raw, before, nonNil(allow))
}

// syncAppKey is the App's private key, parsed, behind --github-app-key.
//
// It is the one secret this command needs and it is treated like
// `--check-github-app` treats the same key: the flag is refused under a
// coding agent's marker before the read, the read is announced in one line,
// and only the PEM copy is cleared — the encoded payload and the parsed key
// live until garbage collection, which is what signing needs them for.
func syncAppKey(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, allowed bool) (*rsa.PrivateKey, error) {
	if !allowed {
		return nil, userErr("%s lists the repositories the GitHub App is installed on, which needs the project's github-app-key secret: rerun with %s, which reads it with your own credentials", "fugaro project sync", syncAppKeyFlag)
	}
	if err := refuseKeyReadInAgent(os.Getenv, syncAppKeyFlag, "the project's GitHub App private key"); err != nil {
		return nil, err
	}
	secretID := gcp.ProjectSecretID(lc.Name, config.GitHubAppKeySecret)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: reads the GitHub App's private key from %s with your own credentials and holds it in memory only to sign a 9-minute token; only the PEM byte copy is cleared afterwards (the encoded payload and the parsed key live until garbage collection); GODEBUG=http2debug in your own environment would print the token's Authorization header, so unset it first\n", syncAppKeyFlag, secretID)
	pem, err := readAppKey(ctx, lc, secretID)
	switch {
	case errors.Is(err, gcp.ErrNoVersion):
		return nil, userErr("the project has no github-app-key secret with a version, so the App's repositories cannot be listed: store it with 'fugaro secrets set github-app-key --project-wide < PATH-TO-THE-PEM-FILE'")
	case errors.Is(err, gcp.ErrNoAccess):
		return nil, userErr("you may not read the project's github-app-key secret (a project owner can), and this command signs with the App's key to ask GitHub")
	case err != nil:
		return nil, userErr("the GitHub App's private key could not be read: %s", oneLineCLI(err.Error()))
	}
	key, err := github.ParsePrivateKey(pem)
	clear(pem)
	if err != nil {
		// A parse error's message never holds key material.
		return nil, userErr("the private key stored for the GitHub App is not a PEM RSA private key (%s); store the App's .pem again with 'fugaro secrets set github-app-key --project-wide < PATH-TO-THE-PEM-FILE'", oneLineCLI(err.Error()))
	}
	return key, nil
}
