package cli

// The enrolment view: the one read that answers "what is this repository made
// of", and the two document versions the group's readers print it in.
//
// `ls`, `status` and `add` all need that answer — `ls` prints one row per
// repository, `status` renders a row per fact that decides whether a run can
// start, and `add` converges an installation towards it — and a derivation
// written three times is three answers that drift (F8, F9). So it is written
// once, here, and it registers no command of its own: it is a read the three
// verbs are built from, and each of them takes its own gate before it calls
// (D2414 — the view performs no write on any path and asks for no role).
//
// It is not a second `doctor` (F12). It computes no check, holds no `Problem`
// and no `Fix`: the checks stay doctor's — their IDs, their sentences and
// their fixing commands — and `status` renders them beside what this view
// holds. What is here is the material those rows are about, resolved the way
// the rest of the program resolves it, so that the image `add` writes and the
// image `status` reports are one derivation and not two (D2416).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// The two documents the group's readers carry, each with its own name and its
// own version. They are new documents rather than members of an older one
// because the desktop console, the history and the plugin read them outside
// this binary, and a field whose meaning has to change gets a new name rather
// than a second meaning under the old one (CLAUDE.md's wire-format rule).
// RE1 renders the status document, RE2 the `ls` one; the view below is the
// read both are built from, and neither version moves without the other's
// readers being told.
//
// The version member belongs to those documents and not to the view: D2410
// gives each document its own version, and one view printed under two
// documents would have to carry two. A reader builds its document by
// embedding the view and adding its own "version" (the JSON tags here are
// written so that embedding flattens to the member names D2410 names).
const (
	reposLsDocVersion     = 1
	reposStatusDocVersion = 1
)

// reposViewImageSource names which answer chose a job's image (D2409). Four of
// them are a repository that runs a project image: the workflow's own catalog
// reference, the project's image map, the catalog's default entry, and — where
// the project names none of the three — the image this project builds for the
// repository itself.
//
// The next two are the two checkout kinds whose job runs the installation's own
// image, which the selection decides nothing for: a row still has to name what
// such a job runs, so they are named here rather than folded into an empty
// source. The last is not a source: it is the resolver's refusal, which is a
// state a row has to be able to carry.
//
// The names are the view's own and not catalog.Source's. The resolver answers
// `catalog` for three of the four — a workflow's reference, the map and the
// default are one Source distinguished by Reference, and `Reference` is true
// for the first two — and *which* of the three it was is exactly what a person
// reading a row asks about their own file: "did my image_name: do this, or the
// map, or did nothing of mine and the catalog chose".
type reposViewImageSource string

const (
	// reposViewImageWorkflow is the workflow's own reference to the catalog:
	// its image_name:, or the tags its needs: states.
	reposViewImageWorkflow reposViewImageSource = "workflow"
	// reposViewImageMap is the project's effective image map (D1906).
	reposViewImageMap reposViewImageSource = "map"
	// reposViewImageDefault is the catalog's default entry, which is what a
	// repository that names nothing runs.
	reposViewImageDefault reposViewImageSource = "default"
	// reposViewImageRepo is the image this project builds for the repository.
	reposViewImageRepo reposViewImageSource = "repo"
	// reposViewImageBase is the installation's base image, which a
	// checkout: clone job runs directly.
	reposViewImageBase reposViewImageSource = "base"
	// reposViewImageShared is the shared dependency-layer image a
	// checkout: shared job runs.
	reposViewImageShared reposViewImageSource = "shared"
	// reposViewImageUnresolved is no source at all: the resolver refused this
	// workflow's image, so nothing chose one. It is named rather than left
	// empty for the reason every member here is — an empty source reads as a
	// job that runs nothing — and the sentence saying why is the workflow's
	// own State, because the refusal is per workflow and a row can hold
	// several.
	reposViewImageUnresolved reposViewImageSource = "unresolved"
)

// reposView is the value: one row per repository the project knows, the
// published allow-list read beside them, what has run here, and this machine's
// own config's half.
//
// The shape is CLOSED, and a consumer reads it that way: a member this build
// does not write is not in it. Every member that can be absent carries its own
// state sentence rather than a zero, which is `managedStatusDoc`'s rule (F14)
// and for its reason — a policy node that did not answer read as an empty
// allow-list draws a project that admits nothing, and a local config that
// could not be read as one that holds nothing draws a machine that does not
// know the repository.
//
// Every list member is [] and never null, the state sentence or not: the two
// documents are read outside this binary, and a contract where one member says
// [] for an absence and the member beside it says null for the same one makes
// each reader special-case members it cannot tell apart.
//
// The seen list is a member of its own and is never merged into the rows
// (D2409). It is a reading of what has RUN here, not of what is enrolled: a
// repository seen running and no longer admitted would read as enrolled, and
// one admitted and never run would read as unenrolled. Its two sources are not
// one window either — the runs bucket's runs/<slug>/ prefixes have no window
// at all and only Firestore's spendDaily is read over seenWindowDays — so each
// entry carries the sources it was read from and no member of this shape
// states a window over the list.
type reposView struct {
	// Project is the installation the read was aimed at.
	Project string `json:"project"`
	// Repos are the rows, one per repository, ordered by canonical name: the
	// repositories this machine's config holds, the concrete names the
	// published allow-list admits, and — for a caller that named one — the
	// repository it named where only a pattern admits it. A pattern
	// (`github/acme/*`) is not itself one of them — it names no repository, it
	// admits them — and a repository that only appears in Seen is not one
	// either.
	Repos []reposViewRepo `json:"repos"`
	// Allow is the published allow-list and the image map and catalog it
	// publishes, with the generation it was read at.
	Allow reposViewAllow `json:"allow"`
	// Seen is what has run on this installation, kept apart from the rows.
	Seen reposViewSeen `json:"seen"`
	// Local is this machine's own config's half.
	Local reposViewLocal `json:"local"`
	// Named is the answer for the one repository a caller named, and nil when
	// it named none.
	Named *reposViewNamed `json:"named,omitempty"`
}

// reposViewRepo is one repository: what it is made of, and both halves of its
// enrolment.
//
// The two halves are two members and not one boolean (D2409): a repository
// admitted by the allow-list that this machine has never configured is a
// different state from one configured here that the list does not admit — the
// first runs, the second does not — and a single "enrolled" column would draw
// them as the same figure.
type reposViewRepo struct {
	Canonical string `json:"canonical"`
	Provider  string `json:"provider"`
	// BaseBranch is the branch this repository's runs start from, resolved
	// through config.EffectiveBaseBranch — the one function every layer
	// resolves through — in the order a launch takes: the working directory's
	// checkout of the repository where its fugaro.yaml writes git.base_branch,
	// else this machine's own entry for it, else the project's (this
	// installation's top-level base_branch, else the published node's), else
	// main. It is never empty: a repository no layer names is one whose runs
	// start from main, which is what a launch of it resolves.
	BaseBranch string `json:"base_branch"`
	// Workflows are the repository's workflows, each with the catalog entry
	// its job runs. They are the checkout's own where this machine has one
	// (the file the deploy-time job spec is built from), and this machine's
	// config's names otherwise — a name alone, which resolves through the map
	// or the catalog's default because a name carries no image_name:.
	Workflows []reposViewWorkflow `json:"workflows"`
	// ImageEntry is the catalog entry the repository's job runs, and "" when
	// its image is not a catalog entry's.
	ImageEntry string `json:"image_entry"`
	// ImageSource is which answer chose it: the repository's job's, which is
	// its first workflow's by name where it states any and the project's own
	// answer for the repository where it states none. A repository whose
	// workflows disagree is a state its own per-workflow entries carry, and
	// this member is the summary a listing has room for.
	ImageSource reposViewImageSource `json:"image_source"`
	// ClaudeAccounts are the accounts this repository's own runs may be
	// served by, as this machine's config records them. An empty list is a
	// true answer and not an unread member here — this machine records none —
	// and Local.Present is the member that says why it records none: the
	// repository is not in this machine's config at all.
	ClaudeAccounts []string `json:"claude_accounts"`
	// Published is the allow-list's half: whether an entry admits it, and the
	// entry that does.
	Published reposViewRepoPublished `json:"published"`
	// Local is this machine's half.
	Local reposViewRepoLocal `json:"local"`
	// Credentials are the credential rows' inputs, and only for the one
	// repository a caller named: reading every repository's would be a Secret
	// Manager call per repository for a screen that shows one repository's
	// rows in full.
	Credentials *reposViewCredentials `json:"credentials,omitempty"`
}

// reposViewWorkflow is one workflow's image answer.
type reposViewWorkflow struct {
	Name  string `json:"name"`
	Entry string `json:"entry"`
	// Source is which answer chose this workflow's entry; see
	// reposViewImageSource for the list.
	Source reposViewImageSource `json:"source"`
	// Warnings are the resolver's own deploy-time sentences about this
	// workflow's image (D1146, D1900), in order.
	Warnings []string `json:"warnings,omitempty"`
	// State is the sentence for a resolution the resolver refused, and "" for
	// one it answered. A pin the catalog does not hold and a docker-tagged
	// entry on a backend with no daemon are both refusals, and a resolution
	// that was refused has no entry to name: a zero here would read as a
	// workflow that runs nothing.
	State string `json:"state,omitempty"`
}

// reposViewRepoPublished is the allow-list's half of a row (D2409).
type reposViewRepoPublished struct {
	Admitted bool `json:"admitted"`
	// Pattern is the allow-list entry that admits it: the repository's own
	// canonical name where the list names it, and the wildcard entry
	// otherwise (github/acme/* admits github/acme/app). "" when nothing
	// admits it.
	Pattern string `json:"pattern"`
}

// reposViewRepoLocal is this machine's config's half of a row (D2409).
type reposViewRepoLocal struct {
	Present bool `json:"present"`
	// Key is the repositories: key this machine holds it under, which is the
	// operator's own spelling ("acme/app") and not the canonical name: the
	// allow-list is canonical and this file is not.
	Key string `json:"key"`
}

// reposViewAllow is the published allow-list read (D2410): the entries, the
// generation they were read at, and the state of a read that did not answer.
type reposViewAllow struct {
	Entries []string `json:"entries"`
	// Generation is the node's ETag as the read returned it, and "" when
	// there was no node to read. It is the generation a publisher compares
	// against before it writes (D854), so a reader can tell a list that moved
	// since it was printed.
	Generation string `json:"generation"`
	// State is the sentence for a read that did not answer — no node
	// published, a database that refused — and "" for one that did. Entries is
	// empty in that case, and this member is the only thing that tells it apart
	// from a policy published with an empty allow-list: the first is a list
	// nobody could read, the second is a project that admits nothing, and
	// NotAdmitted's own sentence makes the same distinction. There is
	// deliberately no fallback here — the image selection falls back to this
	// machine's file, and no local file states which repositories a project
	// admits.
	//
	// A node that ANSWERED and that this build refuses — an unknown top-level
	// key, a version this binary does not read — is deliberately not a state:
	// every CLI reader of the node refuses it (refuseUnknownPolicyKeys,
	// publishedPolicyOf), and a listing that carried the refusal as a sentence
	// would be the one reader that acted on a half-read policy. The view
	// returns that refusal rather than stating it.
	State string `json:"state"`
}

// reposViewSeen is what has run here, as its own member (D2409).
type reposViewSeen struct {
	Entries []reposViewSeenRepo `json:"entries"`
	// Notes are what one of the two sources could not answer — a GitHub
	// project with no Firebase backend, a database with no history mark. They
	// are not a failure: the allow-list is printed either way, and a note is
	// what keeps an empty list from reading as "nothing has ever run here".
	Notes []string `json:"notes"`
	// State is the sentence for a read that did not answer at all.
	State string `json:"state"`
}

// reposViewSeenRepo is one repository this installation has seen a run of.
type reposViewSeenRepo struct {
	// Name is the canonical name when this machine's config names the
	// repository, and the storage slug otherwise: a slug is a hash of the
	// provider and the canonical name and cannot be turned back into one.
	Name string `json:"name"`
	// Provider is the provider the name carries, and "" for a slug.
	Provider string `json:"provider"`
	// Sources are the durable sources that recorded a run of it. They are
	// where, not when: the runs bucket has no window and only spendDaily is
	// read over one, so no window belongs to this list.
	Sources []string `json:"sources"`
}

// reposViewLocal is this machine's own config's half.
type reposViewLocal struct {
	// Repos are the canonical names this machine's configuration holds, in
	// name order.
	Repos []string `json:"repos"`
	// State is the sentence for a config whose entries could not all be read
	// as canonical names, and "" when every one was. An entry with no
	// provider is the case: its provider is the checkout's fugaro.yaml's to
	// decide, and a read that listed the rest silently would draw a machine
	// that does not hold the repository.
	State string `json:"state"`
}

// reposViewNamed is the answer for the one repository a caller named (D2411).
// A repository that is enrolled in neither half is its own state — neither a
// failing row nor a passing one — and a caller reads it here rather than
// drawing an absent row as a pass.
type reposViewNamed struct {
	Canonical string `json:"canonical"`
	// Enrolled is whether either half of the enrolment knows it: a row was
	// built for it, which is to say this machine's config holds it or an
	// allow-list entry admits it — by its own name, or as a pattern that admits
	// it, for which the row is built here rather than in the listing.
	Enrolled bool `json:"enrolled"`
	// State is the sentence for an enrolment that could not be decided, and
	// "" when it could. Enrolled false means "nothing enrolls it" only when
	// both halves were read: with the allow-list unread, the same false means
	// a read that did not answer on the half that would have admitted it,
	// which is the distinction managed status's rule exists for.
	State string `json:"state"`
}

// reposViewCredentials is the credential rows' inputs for one repository: its
// containers, each one's name, whether it holds an enabled version and how old
// that version is. Never a value (D2415) — presence, a name, a version's state
// and a rotation age are the whole of it, on every path including this one.
type reposViewCredentials struct {
	Entries []reposViewCredential `json:"entries"`
	// State is the sentence for a read that did not answer — no Secret
	// Manager endpoint, no credentials, a listing that failed. A screen that
	// read it as "no containers" would report the empty container's own
	// failure as a repository that has none.
	State string `json:"state"`
}

// reposViewCredential is one credential container.
type reposViewCredential struct {
	// Name is the logical name the container carries (github-app-key), and
	// the container's ID when it carries none.
	Name string `json:"name"`
	// ID is the container's own Secret Manager ID, which is a name and not a
	// value.
	ID string `json:"id"`
	// Enabled is whether the container holds an enabled version: the state
	// Terraform's container has when nobody has stored a value yet, which is
	// the failure readiness.go plans no job for.
	Enabled bool `json:"enabled"`
	// AgeDays is the age in whole days of the newest enabled version, and
	// nil when there is none to date. It is a pointer because a container
	// with no enabled version and one whose value was stored today are
	// different facts, and a zero would draw them the same.
	AgeDays *int `json:"age_days"`
}

// newReposView is the one call: what the project's handle holds about every
// repository it knows, and — for the one repository a caller names — what its
// credential containers are.
//
// It reads and writes nothing else. The gate is the caller's (D2414): a read
// takes accessGateProjectRead before it calls, and a writer takes
// accessGateProject before it converges.
//
// Nothing here fails on a read that did not answer. A policy node that cannot
// be read, a runs bucket that cannot be listed, a Secret Manager that cannot
// be reached and a local config whose entries cannot be spelled as canonical
// names each leave their own state sentence (F14), because the alternative —
// an empty list — is a claim about the project drawn from a failure to read it.
//
// Two reads are the other half, and the two things returned rather than stated:
// the sources that carry the project's identity belong to it, and what a listing
// would draw from another project's is another installation's data
// (refusedBySeen); and a published node that ANSWERED and that this build
// refuses — an unknown key, a version it does not read — is a refusal every
// reader of that node makes, which a state sentence would turn into a listing
// drawn from the half of the policy that decoded (reposViewAllowOf).
func newReposView(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, canonical string) (*reposView, error) {
	if lc == nil {
		return nil, userErr("the enrolment view needs this machine's project config: select one, or run this from a checkout of the repository")
	}
	allow, cat, imap, projectBase, err := reposViewAllowOf(ctx, lc, db)
	if err != nil {
		return nil, err
	}
	local, keys := reposViewLocalOf(lc)

	v := &reposView{Project: lc.Name, Allow: allow, Local: local}
	for _, name := range reposViewRowNames(allow, keys) {
		v.Repos = append(v.Repos, reposViewRowOf(ctx, lc, cat, imap, projectBase, name, keys[name], allow))
	}
	v.Repos = nonNil(v.Repos)

	seen, notes, err := seenRepos(ctx, lc, budgetNow())
	switch {
	case refusedBySeen(err):
		// A refusal and not a read that did not answer: the reads of what has
		// run here are the ones that carry the project's identity — the runs
		// bucket's marker and Firestore's installation mark (checkCloudName,
		// spendRepos) — and each refuses a config pointed at another project's
		// sources. `project repos ls` stops on exactly this error, which is the
		// one thing that keeps a listing from printing another installation's
		// data, and this view is what `ls` and `status` read: a state sentence
		// here would draw the allow-list, the rows built from it and the named
		// repository's credential names and ages for a project whose sources
		// are not its own.
		return nil, err
	case err != nil:
		v.Seen.State = "the repositories that have run here could not be read: " + oneLineCLI(err.Error())
	default:
		v.Seen.Notes = notes
		for _, r := range seen {
			v.Seen.Entries = append(v.Seen.Entries, reposViewSeenRepo{Name: r.Name, Provider: r.Provider, Sources: nonNil(r.Sources)})
		}
	}
	v.Seen.Entries, v.Seen.Notes = nonNil(v.Seen.Entries), nonNil(v.Seen.Notes)

	if canonical != "" {
		want, err := reposViewCanonicalOf(canonical)
		if err != nil {
			return nil, err
		}
		v.Named = &reposViewNamed{Canonical: want}
		row, found := slices.BinarySearchFunc(v.Repos, want, func(r reposViewRepo, want string) int {
			return strings.Compare(r.Canonical, want)
		})
		// The rows are built from concrete names and a pattern is not one
		// (reposViewRowNames), so a repository a wildcard admits has none — and
		// the question a caller asks by name is where that admission becomes a
		// row. Reading enrolment off row existence without this would answer
		// "nothing enrolls it", with no state sentence, about a repository a
		// published entry does enroll and whose credential containers were
		// never read.
		if !found && reposViewPublishedOf(allow, want).Admitted {
			v.Repos = slices.Insert(v.Repos, row, reposViewRowOf(ctx, lc, cat, imap, projectBase, want, keys[want], allow))
			found = true
		}
		v.Named.Enrolled = found
		// The two halves an absent row could have come from are the two whose
		// reads can fail, and either of them failing makes "nothing enrolls
		// it" a claim this answer cannot make.
		if !found && (allow.State != "" || local.State != "") {
			v.Named.State = "whether " + want + " is enrolled could not be decided: the repository is in none of the halves that were read, and a half that would have named it did not answer"
		}
		if found {
			v.Repos[row].Credentials = reposViewCredentialsOf(ctx, lc, v.Repos[row].Provider, reposViewLocalNameOf(want, v.Repos[row].Local.Key))
		}
	}
	return v, nil
}

// refusedBySeen is whether the read of what has run here was refused rather
// than failing to answer. A refusal is what this package states as a user error
// (exit 1): the identity checks — the runs bucket's marker, Firestore's
// installation mark — an incomplete config, and a source the config cannot
// reach without credentials. A remote failure (exit 2) is not one: the read was
// attempted and did not answer, which is the state sentence's own case.
func refusedBySeen(err error) bool {
	var ee *ExitError
	return errors.As(err, &ee) && ee.Code == ExitUserError
}

// reposViewCanonicalOf is the named repository, in the one spelling the rows
// and the allow-list use: "<provider>/<owner>/<name>" (task.CanonicalRepo's
// own form, with the provider in front, as projectpolicy.Canonical builds it).
//
// A caller reaching this function has already decided which repository the
// person meant; what it must not do is accept a spelling that would silently
// match no row — "acme/app" alone cannot say which provider it is, and a row
// keyed by the wrong one is a named repository the view reports as not
// enrolled.
func reposViewCanonicalOf(canonical string) (string, error) {
	provider, local, ok := strings.Cut(canonical, "/")
	if !ok || !slices.Contains(config.Providers, provider) {
		return "", userErr("%q is not a canonical repository name: spell it with its git provider, as in \"github/acme/app\" (a repository is canonical only once its provider kind is known)", canonical)
	}
	c, err := projectpolicy.Canonical(provider, local)
	if err != nil {
		return "", userErr("%s is not a repository of provider %s: %v", canonical, provider, err)
	}
	return c, nil
}

// reposViewAllowOf reads the published node: the allow-list with the
// generation it was read at, and the catalog and image map the node publishes,
// which are the pair every row's image is resolved against (D1140, D1906).
//
// The project's base branch comes back with them: this installation's own
// top-level base_branch where it names one, else the node's, which is the order
// projectBaseBranchErr resolves layer 3 in — an installation that names one does
// not read the node for it at all. It is what a repository whose entry here
// names none starts its runs from, with "main" behind it (config's default).
//
// A node that did not answer leaves a state sentence and this machine's own
// pair, and the project's base branch with it: that layer is this machine's own
// answer, which a node that did not answer is no reason to lose.
//
// A policy read that failed is deliberately not a refusal: `ls` and `status`
// still have rows to draw and a bucket to read, and a policy read that failed is
// one member of the answer rather than the whole of it — with the sentence
// saying so, which is the half an empty list loses. **A node that answered and
// that this build refuses is the other half, and it is returned rather than
// stated**: an unknown top-level key and a version no reader here reads are
// refusals every reader of the node makes (refuseUnknownPolicyKeys' own posture,
// P8: the runner is the tolerant side), and the one reader that turned them into
// a member would act on the half of the policy it could decode.
func reposViewAllowOf(ctx context.Context, lc *localcfg.Config, db *rtdb.Client) (reposViewAllow, *config.Catalog, imageMapping, string, error) {
	fileOnly := func(state string) (reposViewAllow, *config.Catalog, imageMapping, string, error) {
		return reposViewAllow{Entries: []string{}, State: state}, lc.ImageCatalogEffective(nil), configImageMap(lc, nil), lc.BaseBranch, nil
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	switch {
	case err != nil:
		return fileOnly("the published policy could not be read, so the allow-list and the image selection it publishes are not in this answer: " + oneLineCLI(err.Error()))
	case !found:
		return fileOnly(fmt.Sprintf("no policy is published for project %s (%s), so no repository is admitted by an allow-list", lc.Name, projectpolicy.PathShared()))
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return reposViewAllow{}, nil, imageMapping{}, "", err
	}
	projectBase := lc.BaseBranch
	if projectBase == "" {
		projectBase = p.BaseBranch
	}
	return reposViewAllow{Entries: nonNil(p.AllowRepos), Generation: etag},
		lc.ImageCatalogEffective(p.ImageCatalog), configImageMap(lc, p), projectBase, nil
}

// reposViewLocalOf is this machine's own half: the canonical names its config
// holds — the form the allow-list is written in — and the key each is held
// under, which is the operator's own spelling.
//
// An entry recorded without a provider cannot be spelled as one. Its provider
// is the checkout's fugaro.yaml's to decide and no checkout is read here, so
// the entry is named in the member's state sentence rather than dropped: a
// machine that holds a repository and does not say so draws the wrong row.
func reposViewLocalOf(lc *localcfg.Config) (reposViewLocal, map[string]string) {
	var out reposViewLocal
	keys := map[string]string{}
	var unreadable []string
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			unreadable = append(unreadable, name)
			continue
		}
		out.Repos = append(out.Repos, c)
		keys[c] = name
	}
	// By canonical name and not by the config's key: the two disagree the
	// moment a key carries an upper-case letter, and a member whose order is
	// the file's would make two machines holding the same repositories print
	// two different documents.
	slices.Sort(out.Repos)
	out.Repos = nonNil(out.Repos)
	if len(unreadable) > 0 {
		out.State = fmt.Sprintf("this machine's config holds %s without a git provider, so whether it holds those repositories is not in this answer: name the provider there, or run this from a checkout of the repository",
			strings.Join(unreadable, ", "))
	}
	return out, keys
}

// reposViewRowNames is the rows' repositories: this machine's config's, and the
// concrete names the published allow-list admits. A pattern is not one — it
// admits repositories rather than being one — and neither is a name that
// appears only in the seen list (D2409).
//
// keys is filled in with "" for a repository the allow-list names and this
// machine's config does not: the row exists, and its local half is absent.
func reposViewRowNames(allow reposViewAllow, keys map[string]string) []string {
	for _, e := range allow.Entries {
		if strings.Contains(e, "*") {
			continue
		}
		if _, ok := keys[e]; !ok {
			keys[e] = ""
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

// reposViewRowOf builds one row.
//
// Its base branch is resolved through config.EffectiveBaseBranch, the one
// function every layer resolves through (launchBaseBranch hands it the same four
// arguments), so a row prints the branch a run of that repository really starts
// from and never "" — the default is main. Reading the entry and the project's
// value alone would print "" for an installation that names neither, in a
// versioned document the console reads, with no state sentence to say what the
// empty string meant.
func reposViewRowOf(ctx context.Context, lc *localcfg.Config, cat *config.Catalog, imap imageMapping, projectBase, canonical, localKey string, allow reposViewAllow) reposViewRepo {
	row := reposViewRepo{
		Canonical: canonical,
		Provider:  providerOf(canonical),
		Published: reposViewPublishedOf(allow, canonical),
		Local:     reposViewRepoLocal{Present: localKey != "", Key: localKey},
	}
	repoBranch := ""
	if localKey != "" {
		r := lc.Repos[localKey]
		repoBranch = r.BaseBranch
		row.ClaudeAccounts = slices.Clone(r.ClaudeAccounts)
	}
	// Layer 1 is the checkout this working directory is in, and only an entry
	// here gives the repository the name that checkout is found under:
	// checkoutRoot canonicalises the name a launch writes against origin's, and
	// a repository this machine holds no entry for is one no launch of its names
	// here.
	file, fileSets := "", false
	if localKey != "" {
		file, fileSets = checkoutBaseBranch(ctx, localKey)
	}
	row.BaseBranch, _ = config.EffectiveBaseBranch(file, fileSets, repoBranch, projectBase)

	// The workflows are the checkout's where this machine has one: image_name:
	// and needs: live in the repository's own fugaro.yaml, which is the file
	// the deploy-time job spec is read from, so this machine's list of names
	// could not answer the question anyway (the same choice catalogPointedAt
	// makes). Without a checkout the names are all there is, and a name alone
	// resolves through the map or the catalog's default.
	// The resolver is asked with the owner/name and the row's provider apart:
	// catalog.ImageRequest.Canonical is the bare name the legacy map is keyed by
	// and the form the resolver canonicalizes again, while the provider is what
	// turns it into the repo_images key — a "github/acme/app" handed in as
	// Canonical would read as an owner called "github/acme" and find no mapping.
	localName := reposViewLocalNameOf(canonical, localKey)
	names, defs := reposViewWorkflowDefs(ctx, lc, localKey)
	for _, name := range names {
		row.Workflows = append(row.Workflows, reposViewWorkflowOf(lc, cat, imap, localName, row.Provider, name, defs[name]))
	}
	// The row names one image, and it is the repository's job's: the first
	// workflow by name where the repository states any, and the project's own
	// answer for the repository where it states none. A repository whose
	// workflows disagree is a state its per-workflow entries carry.
	row.ImageEntry, row.ImageSource = reposViewRowImage(lc, cat, imap, localName, row.Provider, row.Workflows)
	row.Workflows, row.ClaudeAccounts = nonNil(row.Workflows), nonNil(row.ClaudeAccounts)
	return row
}

// reposViewRowImage is the row's single image answer.
func reposViewRowImage(lc *localcfg.Config, cat *config.Catalog, imap imageMapping, localName, provider string, workflows []reposViewWorkflow) (string, reposViewImageSource) {
	if len(workflows) > 0 {
		return workflows[0].Entry, workflows[0].Source
	}
	sel, err := catalog.JobImage(imap.request(cat, localName, provider, config.Workflow{}, catalog.Backend(lc.Backend())))
	if err != nil {
		return "", reposViewImageUnresolved
	}
	return sel.Entry.Name, reposViewSourceOf(sel, config.Workflow{})
}

// reposViewWorkflowDefs is the workflow names the repository states and the
// definition behind each. The checkout's own document wins where this machine
// has one; otherwise this machine's config's names stand for workflows whose
// keys nothing here can read.
func reposViewWorkflowDefs(ctx context.Context, lc *localcfg.Config, localKey string) ([]string, map[string]config.Workflow) {
	defs := map[string]config.Workflow{}
	if localKey == "" {
		return nil, defs
	}
	if cfg := checkoutConfig(ctx, localKey); cfg != nil {
		names := slices.Sorted(maps.Keys(cfg.Workflows))
		for _, n := range names {
			defs[n] = cfg.Workflows[n]
		}
		return names, defs
	}
	names := slices.Clone(lc.Repos[localKey].Workflows)
	for _, n := range names {
		defs[n] = config.Workflow{}
	}
	return names, defs
}

// reposViewWorkflowOf resolves one workflow's image through the one resolver.
func reposViewWorkflowOf(lc *localcfg.Config, cat *config.Catalog, imap imageMapping, localName, provider, name string, w config.Workflow) reposViewWorkflow {
	out := reposViewWorkflow{Name: name}
	sel, err := catalog.JobImage(imap.request(cat, localName, provider, w, catalog.Backend(lc.Backend())))
	if err != nil {
		out.Source = reposViewImageUnresolved
		out.State = fmt.Sprintf("this workflow's image is not resolved: %s", oneLineCLI(err.Error()))
		return out
	}
	out.Entry, out.Source, out.Warnings = sel.Entry.Name, reposViewSourceOf(sel, w), sel.Warnings
	return out
}

// reposViewSourceOf names which answer chose the image.
//
// JobImage stays the whole of the resolution: this reads none of the catalog
// and none of the map, and answers from the selection it returned plus the one
// key the selection does not carry. A workflow that stated its own reference
// to the catalog is the workflow's answer whatever the selection says, because
// the resolver's first arm is exactly that condition; everything else that
// came back as a catalog entry was the map's (Reference) or the default's.
func reposViewSourceOf(sel catalog.Selection, w config.Workflow) reposViewImageSource {
	switch sel.Source {
	case catalog.SourceBase:
		return reposViewImageBase
	case catalog.SourceShared:
		return reposViewImageShared
	case catalog.SourceRepo:
		return reposViewImageRepo
	}
	switch {
	case w.ImageName != "" || len(w.Needs) > 0:
		return reposViewImageWorkflow
	case sel.Reference:
		return reposViewImageMap
	default:
		return reposViewImageDefault
	}
}

// reposViewPublishedOf is the allow-list's half: whether an entry admits the
// repository and which entry it is (D2409). The repository's own entry wins
// over a pattern that also admits it, so the answer does not depend on the
// order the list happens to hold its entries in: "admitted by name" and
// "admitted by github/acme/*" are different facts about the enrolment, and a
// reader asked which it is.
func reposViewPublishedOf(allow reposViewAllow, canonical string) reposViewRepoPublished {
	if slices.Contains(allow.Entries, canonical) {
		return reposViewRepoPublished{Admitted: true, Pattern: canonical}
	}
	for _, e := range allow.Entries {
		if projectpolicy.MatchRepo(e, canonical) {
			return reposViewRepoPublished{Admitted: true, Pattern: e}
		}
	}
	return reposViewRepoPublished{}
}

// reposViewLocalNameOf is the owner/name a canonical name carries, for the
// reads keyed by a repository's storage slug. localKey is this machine's own
// spelling where it holds the repository, and the canonical name's tail
// otherwise — a repository the allow-list admits from another machine is still
// one whose containers are named after it.
func reposViewLocalNameOf(canonical, localKey string) string {
	if localKey != "" {
		return localKey
	}
	_, rest, _ := strings.Cut(canonical, "/")
	return rest
}

// reposViewCredentialsOf reads one repository's credential containers: their
// names, whether each holds an enabled version, and how old that version is.
//
// It is the whole of what the two credential rows of status may print (D2415):
// a value is read nowhere on this path. gcp.Secrets.List returns metadata and
// never a value, and the age is the newest ENABLED version's creation time,
// which is a time. The container's own creation time is not the answer — a
// rotated container is as old as its first value — so the versions are listed
// through newestEnabledVersion, the same reader doctor's rotation check uses.
func reposViewCredentialsOf(ctx context.Context, lc *localcfg.Config, provider, localName string) *reposViewCredentials {
	// The list member is empty from the start, so every path out of here — each
	// of the state sentences below as much as the listing — carries [] and not
	// null beside a sibling member that did answer.
	out := &reposViewCredentials{Entries: []reposViewCredential{}}
	if lc.Endpoints.NoAuth && lc.Endpoints.SecretManager == "" {
		// The guard the other reads behind a fake use: an empty endpoint under
		// no_auth would send the call to Google.
		out.State = "this machine's config names no Secret Manager endpoint, so the repository's credential containers are not in this answer"
		return out
	}
	slug, err := task.Slug(provider, localName)
	if err != nil {
		out.State = fmt.Sprintf("%s is not a repository whose containers could be looked up: %v", localName, err)
		return out
	}
	opts := gcpOptions(lc)
	secrets, err := gcp.NewSecrets(ctx, opts)
	if err != nil {
		out.State = "Secret Manager could not be reached, so the repository's credential containers are not in this answer: " + oneLineCLI(err.Error())
		return out
	}
	list, err := secrets.List(ctx, map[string]string{gcp.LabelRepo: slug})
	if err != nil {
		out.State = "the repository's credential containers could not be listed: " + oneLineCLI(err.Error())
		return out
	}
	var sm *secretmanager.Service
	now := time.Now()
	for _, s := range list {
		e := reposViewCredential{Name: s.Labels[gcp.LabelSecret], ID: s.ID, Enabled: s.Latest != ""}
		if e.Name == "" {
			e.Name = s.ID
		}
		if e.Enabled {
			if sm == nil {
				if svc, err := gcp.NewSecretManager(ctx, opts); err == nil {
					sm = svc
				}
			}
			if sm != nil {
				if created, dated, err := newestEnabledVersion(ctx, sm, lc.GCPProject, s.ID); err == nil && dated {
					days := int(now.Sub(created).Hours() / 24)
					e.AgeDays = &days
				}
			}
		}
		out.Entries = append(out.Entries, e)
	}
	slices.SortFunc(out.Entries, func(a, b reposViewCredential) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}
