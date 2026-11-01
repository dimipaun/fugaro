package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// configListDoc is `fugaro config list --json`: the installations this
// machine has, as a document.
//
// It is the read the v1 design's D13 names — "fugaro config list --json" —
// and it closes that design's gap 5 ("nothing enumerates installations",
// docs/design/desktop-console.md §4.3), which the console's installation
// picker and its first run are built on (console v2 plan V2.1.1; design
// F1/F2, D13, D31 and §3.4 rule 7). Two local sources, and no third:
//
//   - the project configs (localcfg.Projects, ProjectPath, LoadProject: the
//     same loader and the same validation every other command uses, never a
//     second parser), for the name, the GCP project, the region and what is
//     known of the shared copy;
//   - the remembered checkouts (localcfg.LoadCheckouts: a machine-local
//     state file beside them, never a key in a project config), for the
//     repositories of each project and where they are checked out.
//
// It is read-only and offline in the strict sense: it opens no bucket, calls
// no API, takes no project selection and needs no credentials, so it works
// outside a checkout, with no config at all (an empty list) and with no
// network. And it writes nothing — the checkouts it reads are re-validated
// here, never refreshed, so a stale entry is reported rather than repaired.
//
// **The document is a named set of fields and it is deliberately NOT a
// marshalled localcfg.Config.** That struct carries the owner's providers,
// their base URLs, the named Claude accounts, the reserved project secrets,
// the Terraform block and the whole repository list — none of which the
// console needs, and all of which would silently join this document the day
// a field is added to the struct. The console is one field away from a
// credential dump here, so the fields are spelled out in the types below and
// config_list_test.go fails if a field outside this set reaches the output.
type configListDoc struct {
	Installations []configListInstallation `json:"installations"`
	// Warnings are the lines a human listing prints before its table: an
	// installation whose config could not be read is still listed (its name
	// is a fact of the directory), with its fields empty, and the warning
	// says why. Never a silent omission — the picker must not offer a name
	// whose data is missing without saying so.
	Warnings []string `json:"warnings"`
}

// configListInstallation is ONE installation. Every field is one the console
// needs and no more (V2.1.1): the project name, the GCP project, the region,
// whether a shared config was merged, when the shared copy was last checked,
// the base branch its runs are cut from, and where this machine has a checkout
// of the project's repositories.
type configListInstallation struct {
	// Name is the project name, i.e. projects/<name>.yaml's name, which the
	// file must agree with (LoadProject refuses one that does not).
	Name string `json:"name"`
	// GCPProject is the installation's GCP project ID, "" when its config
	// could not be read (the warning says why).
	GCPProject string `json:"gcp_project"`
	// Region is the installation's region, "" when its config could not be
	// read.
	Region string `json:"region"`
	// SharedMerged says this machine holds the installation's published
	// shared config: a cache entry naming this installation's GCP project
	// exists under $XDG_CACHE_HOME/fugaro/shared-config/<name>.json. That
	// entry is the published copy MergeShared merges from and the same-
	// installation test (SameInstallation) is what decides it belongs here —
	// the cache is keyed by the project name, so the name half of that test
	// is the file's own name and the GCP project half is compared here.
	// False means no shared config was read on this machine, never that the
	// installation publishes none.
	SharedMerged bool `json:"shared_merged"`
	// SharedCheckedAt is when that copy was last checked against the runs
	// bucket, null when SharedMerged is false. It is the cache's own
	// CheckedAt: how fresh the copy is read off it with SharedFreshFor and
	// SharedOfflineFor, which are the constants the reader (fetchSharedConfig)
	// uses, so this document never claims a freshness of its own.
	SharedCheckedAt *time.Time `json:"shared_checked_at"`
	// BaseBranch is the installation's own recorded base branch: the
	// project-level `base_branch` of projects/<name>.yaml, "" when it records
	// none. It is one of the two layers a console launch can be cut from, and
	// the one that answers for every repository of the installation that
	// records no branch of its own (configListCheckout.BaseBranch,
	// config.EffectiveBaseBranch's order).
	//
	// It is read off that local file and nothing else: not the published policy
	// node, which carries the same setting for the runner and which
	// `config list` cannot read without opening a bucket, and not a checkout's
	// own fugaro.yaml, which the CLI's merged order reads *first* and which a
	// console launch never has (the launch's working directory is the app's
	// own, empty), so a branch taken from it would be a branch the run is not
	// cut from.
	BaseBranch string `json:"base_branch"`
	// Checkouts are the repositories this machine remembers a checkout of for
	// this project, in identity order, oldest path first — and where each of
	// them is, re-validated on this read. It is what makes the console's
	// first run zero-configuration: the installation list and the local
	// checkouts arrive in one document (docs/design/desktop-console.md D13
	// and §3.4 rule 7).
	//
	// An older CLI does not carry this key at all, so a reader must read an
	// absent `checkouts` as "this CLI does not know about checkouts" and an
	// empty array as "none are remembered" (the console's JSON contract,
	// §1's availability states and §6.8's row for this field).
	Checkouts []configListCheckout `json:"checkouts"`
}

// configListCheckout is ONE remembered checkout: a repository this machine has
// recorded a checkout of for this installation's project, and the path it is
// at.
//
// It is a flat pair of (repository, path) rather than the nested
// repository-then-paths shape the state file holds: the console renders a list
// of "where is this repository on this machine", and a row per path is what
// that list is.
type configListCheckout struct {
	// Repository is the identity the path was recorded under, as
	// "<provider>/<owner>/<name>" — the provider and the repository name its
	// git origin gives (localcfg.CheckoutIdentity.Key), which is the same
	// string the state file's map key is built from.
	Repository string `json:"repository"`
	// Path is the absolute path that was recorded, made printable and
	// length-bounded: it is a string off this machine's disk.
	Path string `json:"path"`
	// BaseBranch is the recorded base branch of this repository:
	// repos.<owner/name>.base_branch in the installation's own project config,
	// "" when that config names no such entry or records no branch for it. It is
	// the repository's own layer of the two a console launch can be cut from
	// (configListInstallation.BaseBranch is the other, and answers when this is
	// empty).
	//
	// It is a property of the repository the row was recorded under and not of
	// the directory the path names: a row whose path has been deleted still
	// records which branch that repository's runs are cut from, and a row that
	// is not enrolled or not representable still carries the value the operator
	// wrote. The reader of this document is what decides whether the row may be
	// launched from at all.
	BaseBranch string `json:"base_branch"`
	// Enrolled is whether this machine's own project config enrolls this
	// repository (localcfg.Config.Enrolls: the repos: map of
	// projects/<name>.yaml, the one local fact about enrollment there is — the
	// published allow-list is the cloud's copy of it and is not read here). It
	// is a field of its own, next to Valid, because the two questions are not
	// the same one: a checkout's fugaro.yaml saying `project: aurora` is a
	// claim the checkout makes, and a repository the installation does not
	// enroll is not one of its checkouts however its file reads. False, too,
	// when the project config could not be read: nothing can be proved about
	// it, so the row fails closed.
	Enrolled bool `json:"enrolled"`
	// Valid is whether the path is still a checkout of this project, of this
	// repository, and of a repository the project enrolls — the whole
	// conjunction, as of this read (see validateCheckoutPath). It is the only
	// field the console may act on.
	Valid bool `json:"valid"`
	// Reason is the code for why it is not, "" when Valid. A code and not a
	// sentence: the console renders it, and a sentence built here would be a
	// claim about a directory this command only looked at once.
	Reason string `json:"reason,omitempty"`
}

// The reason codes a remembered path is reported with. They are a closed set
// (validateCheckoutPath returns one of them), and each names the first thing
// that was wrong, in the order a reader would check:
const (
	// checkoutMissing: the path is not a directory that exists any more (it
	// was deleted, or moved; a symlink loop is this too, since the path
	// resolves to nothing).
	checkoutMissing = "missing"
	// checkoutNotACheckout: it exists, but it is not a git checkout's own
	// root (no git toplevel there, no fugaro.yaml in it, or the path is now
	// inside some other checkout).
	checkoutNotACheckout = "not_a_checkout"
	// checkoutUnreadable: it is a checkout with a fugaro.yaml this command
	// cannot read (a file it cannot open, or one whose project: is not a
	// string).
	checkoutUnreadable = "unreadable"
	// checkoutOtherProject: its fugaro.yaml names another project than the
	// one the path was recorded for (or names none).
	checkoutOtherProject = "other_project"
	// checkoutOtherRepository: its origin is another repository than the one
	// the path was recorded for, or an origin git config rewrites.
	checkoutOtherRepository = "other_repository"
	// checkoutNotEnrolled: the path is a checkout of this project and this
	// repository, and the project does not enroll that repository
	// (configListCheckout.Enrolled). A clone whose own fugaro.yaml claims the
	// project — a fork, a third-party repository, a hostile pull request — is
	// not one of the installation's checkouts, and the console must not be
	// sent to it as the working directory of an installation's command.
	checkoutNotEnrolled = "not_enrolled"
	// checkoutNotRepresentable: the row cannot be taken as it stands, so
	// nothing about it is checked and nothing may be acted on: the identity is
	// not a provider's owner/name, the path is not absolute (a relative one
	// would be resolved against *this* command's working directory, which is
	// not where it was recorded), printing it changed it — the escaping and
	// the length bound below are what a row is printed through, and a printed
	// path that is not the recorded path names a directory that may exist and
	// is not this one — or the path is under this machine's temporary
	// directory, where this machine records no checkout and reads none
	// (localcfg.InTempRootsAsWritten): a scratchpad a coding agent made is a
	// live checkout, and the row is a working directory the console would
	// otherwise be offered.
	checkoutNotRepresentable = "not_representable"
)

func newConfigListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Fugaro installations this machine has",
		Long: "List the project configs under $XDG_CONFIG_HOME/fugaro/projects (else\n" +
			"~/.config/fugaro/projects), with the GCP project and region of each, what\n" +
			"is known about its published shared config, and the checkouts of its\n" +
			"repositories this machine remembers.\n\n" +
			"The checkouts are remembered by the commands that run in one and are\n" +
			"not reads the desktop console spawns (fugaro update --repo, fugaro run,\n" +
			"fugaro validate, fugaro backends and the rest of them), in a machine-local\n" +
			"state file beside the project configs. The commands the console's spawn\n" +
			"allow-list covers never record: fugaro doctor and fugaro accounts ls\n" +
			"resolve a checkout too, but a read writes nothing, whoever runs it — the\n" +
			"command cannot tell who spawned it. Every path is re-validated on this\n" +
			"read: a path that\n" +
			"was moved or deleted, that is no longer a git checkout, or whose\n" +
			"fugaro.yaml no longer names this project and this repository is reported\n" +
			"with \"valid\": false and a reason, and is never silently dropped. The\n" +
			"paths are local state: they are never published to the runs bucket, to\n" +
			"the shared config or to any log.\n\n" +
			"This is a local read: it opens no bucket and needs no project selected, so it\n" +
			"works outside a checkout and with no config at all (an empty list).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc := buildConfigList(cmd.Context(), getenvOS, time.Now())
			return printConfigList(cmd.OutOrStdout(), doc, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// buildConfigList is the document: every project config localcfg.Projects
// names, in its order, with what can be read of each without opening a
// bucket, plus the checkouts this machine remembers for it. now is the clock
// the shared cache's freshness is read against; ctx carries the checkout
// parse cache, so a path re-validated here costs one git call per checkout
// and not one per field.
func buildConfigList(ctx context.Context, getenv func(string) string, now time.Time) configListDoc {
	// Both are seeded: an empty array is a fact ("there are none", "there
	// were no warnings") and a null is not, which is the console's own
	// availability rule the console's JSON contract states (§1).
	doc := configListDoc{Installations: []configListInstallation{}, Warnings: []string{}}
	names, err := localcfg.Projects(getenv)
	if err != nil {
		// A projects directory that cannot be read is not "no
		// installations": say so rather than print an empty list.
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("listing the project configs failed: %v", err))
		return doc
	}
	// A state file that cannot be read is not "no checkouts": the projects
	// are still listed (they are facts of the directory), the warning says
	// why the checkouts are missing, and nothing is written over it.
	known := localcfg.EmptyCheckouts()
	if c, err := localcfg.LoadCheckouts(getenv); err != nil {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("the remembered checkouts could not be read, so no checkout is listed: %v", err))
	} else {
		// The write path's bounds, applied on this read: the file is one this
		// process did not write, and every path examined here costs a stat and
		// about three git executions. What the bounds cut is said, never
		// silently absorbed.
		bounded, dropped := c.Bounded(names)
		known = bounded
		if dropped > 0 {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf(
				"the remembered checkouts hold %d entries more than this listing examines (at most %d repositories per project, %d paths each, %d rows in all): the newest are listed and the rest are not checked",
				dropped, localcfg.MaxCheckoutRepos, localcfg.MaxCheckoutPaths, localcfg.MaxCheckoutRows))
		}
	}
	for _, name := range names {
		inst := configListInstallation{Name: name, Checkouts: []configListCheckout{}}
		lc, path, err := localcfg.LoadProject(getenv, name)
		if err != nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("%s could not be read: %v", path, err))
			// The checkouts are listed anyway: which directory holds this
			// project's checkout is a fact of this machine, not of the file
			// that could not be parsed, and it is the one thing a reader can
			// still act on. None of them is valid, though: enrollment is read
			// off that same file, so none of them can be proved enrolled
			// (configListCheckout.Enrolled) and the reader fails closed.
			inst.Checkouts = configListCheckouts(ctx, known, name, nil)
			doc.Installations = append(doc.Installations, inst)
			continue
		}
		inst.GCPProject, inst.Region = lc.GCPProject, lc.Region
		inst.BaseBranch = lc.BaseBranch
		if e, ok := localcfg.LoadSharedCache(getenv, name); ok && e.GCPProject == lc.GCPProject {
			inst.SharedMerged = true
			checked := e.CheckedAt
			inst.SharedCheckedAt = &checked
			if !e.Fresh(now) {
				// Not a field of its own: the copy is still the one this
				// machine has, and how old it is follows from CheckedAt and
				// the constants the reader uses (SharedFreshFor,
				// SharedOfflineFor). The human listing says it in words.
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("%s's shared config was last checked %s ago, over the %s the reader refreshes it within",
					name, now.Sub(e.CheckedAt).Round(time.Second), localcfg.SharedFreshFor))
			}
		}
		inst.Checkouts = configListCheckouts(ctx, known, name, lc)
		doc.Installations = append(doc.Installations, inst)
	}
	return doc
}

// configListCheckouts is one project's remembered checkouts, re-validated on
// this read and flattened to one entry per path. lc is the installation's own
// project config, nil when it could not be read: it is where enrollment is
// read from (localcfg.Config.Enrolls, a local fact).
func configListCheckouts(ctx context.Context, known *localcfg.Checkouts, project string, lc *localcfg.Config) []configListCheckout {
	out := []configListCheckout{}
	for _, r := range known.ProjectCheckouts(project) {
		id := localcfg.CheckoutIdentity{Provider: r.Provider, Repo: r.Repo}
		enrolled := lc.Enrolls(id.Provider, id.Repo)
		for _, p := range r.Paths {
			// The row as it is printed, and the row as it is: an identity no
			// provider serves, a relative path, or a string the escaping or
			// the length bound below changed is a row nothing can be checked
			// against, so nothing is checked and it is reported as what it is.
			printed := pluginwire.Printable(p.Path)
			representable := printed == p.Path &&
				pluginwire.Printable(id.Key()) == id.Key() &&
				filepath.IsAbs(p.Path) && validIdentity(id)
			// A path under this machine's temporary directory is refused the
			// same way and for the same reason — nothing about it is checked
			// and nothing may be acted on — and the verdict comes from the
			// row's own string: no link is followed, no stat and no git runs
			// for it, so a scratchpad an agent made is refused whether or not
			// it is still there and whatever it is a checkout of. The writer
			// records nothing under one (localcfg.RecordCheckout) and drops a
			// row under one; a row already in the state file predates that,
			// and this is the read refusing it now.
			valid, reason := false, checkoutNotRepresentable
			if representable && !localcfg.InTempRootsAsWritten(p.Path, checkoutTempRoots()) {
				valid, reason = validateCheckoutPath(ctx, p.Path, project, id, enrolled)
			}
			out = append(out, configListCheckout{
				// The repository goes through the escaping like the path:
				// both come from a file on this machine that this process
				// did not write (a hand-edited state file, or one an older
				// fugaro wrote).
				Repository: pluginwire.Printable(id.Key()),
				Path:       printed,
				BaseBranch: recordedBaseBranch(lc, id.Repo),
				Enrolled:   enrolled,
				Valid:      valid,
				Reason:     reason,
			})
		}
	}
	return out
}

// validIdentity reports whether an entry's identity from the state file is one
// this tree can name a repository by: a known provider and an owner/name. The
// write path refuses anything else (RecordCheckout), so an entry that is not
// is a hand-edited file's, and the row it makes is not one the console may act
// on or even read as a repository name.
func validIdentity(id localcfg.CheckoutIdentity) bool {
	if !slices.Contains(config.Providers, id.Provider) {
		return false
	}
	_, err := task.CanonicalRepo(id.Repo)
	return err == nil
}

// recordedBaseBranch is the base branch the installation records for one
// repository: repos.<owner/name>.base_branch in its project config, "" when
// that config names no such entry, records no branch for it, or could not be
// read (lc nil, which fails closed exactly as RepoFor does).
//
// The entry is resolved by localcfg.Config.RepoFor, which is the lookup a
// launch resolves repos.<r>.base_branch through as well
// (cloudEnv.localRepo). One resolver for the two readers is the point: the
// branch printed here is the branch the run is cut from, so an entry spelled
// "acme/app.git", or two spellings of one repository, cannot make this row say
// one branch and the run use another (docs/design/desktop-console.md §5.1
// rule 11, "the branch the window shows is the branch the run is cut from").
func recordedBaseBranch(lc *localcfg.Config, repo string) string {
	r, ok := lc.RepoFor(repo)
	if !ok {
		return ""
	}
	return r.BaseBranch
}

// validateCheckoutPath re-validates one remembered path, on the read: the
// directory exists, it is a git checkout, its fugaro.yaml still names this
// project and the same repository, and that repository is one the project
// enrolls (enrolled, read off the project config by the caller). It reports
// the first thing that is wrong as a reason code (the consts above), and does
// not repair or drop anything — the write path is what prunes
// (localcfg.RecordCheckout), so a path that is stale here is still there for
// the operator to see, and still there if the directory comes back.
//
// The caller has already checked that the row is representable (an absolute
// path, a well-formed identity, a path printing does not change), which is why
// nothing here touches the filesystem with a string that is not a path.
//
// resolveCheckout and not checkoutProject: this is a read, and reading must
// not record (or re-record, under an identity the checkout has since
// changed to) what it looked at.
func validateCheckoutPath(ctx context.Context, path, project string, id localcfg.CheckoutIdentity, enrolled bool) (bool, string) {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false, checkoutMissing
	}
	co, err := resolveCheckout(ctx, path)
	if err != nil {
		return false, checkoutUnreadable
	}
	if co == nil || !sameCheckoutRoot(path, co.Root) {
		// co.Root is the git toplevel git resolves for path: a path that is
		// not that toplevel is a checkout of nothing (a subdirectory of
		// another checkout, or a directory that has since become one).
		return false, checkoutNotACheckout
	}
	if co.Project != project {
		return false, checkoutOtherProject
	}
	oi, ok := readOrigin(ctx, co.Root)
	if !ok || oi.Rewritten {
		return false, checkoutOtherRepository
	}
	// The provider is the origin's host's, exactly as it is where the
	// checkout was recorded (providerForHost) — the file's own git.provider
	// is never consulted, not even for a host no provider lives on: a path
	// whose directory says bitbucket over a github.com origin is a github
	// checkout here, one remembered as bitbucket/... is another repository
	// than this one, and a directory on a host no provider serves (with a
	// file claiming github) is no provider's repository at all.
	provider := providerForHost(oi.Host)
	// sameRepo, not a string compare: owner/name is the same repository
	// whichever case it is written in, and task.CanonicalRepo is the rule
	// the rest of the tree compares repository names by.
	if provider != id.Provider || !sameRepo(oi.Repo, id.Repo) {
		return false, checkoutOtherRepository
	}
	// Last, after the checkout is known to be what it says it is: is it one of
	// this installation's repositories at all? A clone whose own fugaro.yaml
	// claims the project — a fork, a third-party repository, a hostile pull
	// request — is a checkout of *a* repository of that project's name, and
	// not a checkout of one the operator enrolled.
	if !enrolled {
		return false, checkoutNotEnrolled
	}
	return true, ""
}

// sameCheckoutRoot reports whether path names the same directory as root, the
// git toplevel git resolves for it. The two are normally the same string,
// because a recorded path is a toplevel a command was run in — but a
// hand-written state file, or one an older fugaro wrote, can hold a spelling
// that is not the resolved one: on darwin every /var/... temp directory is
// /private/var/..., so a comparison of the strings alone would call a real
// checkout "no longer a checkout". The resolved forms are compared when the
// strings differ, and only then: the common case costs nothing, and both
// spellings of one directory are one directory.
func sameCheckoutRoot(path, root string) bool {
	if filepath.Clean(path) == root {
		return true
	}
	if p, err := filepath.EvalSymlinks(path); err == nil && p == root {
		return true
	}
	if r, err := filepath.EvalSymlinks(root); err == nil && r == filepath.Clean(path) {
		return true
	}
	return false
}

func printConfigList(w io.Writer, doc configListDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if len(doc.Installations) == 0 {
		fmt.Fprintln(w, "no Fugaro installations on this machine; run `fugaro setup` to set one up")
	}
	for _, n := range doc.Warnings {
		fmt.Fprintln(w, "warning: "+pluginwire.Printable(n))
	}
	if len(doc.Installations) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tGCP PROJECT\tREGION\tSHARED\tSHARED CHECKED\tCHECKOUTS")
	for _, inst := range doc.Installations {
		shared, checked := "no", "-"
		if inst.SharedMerged {
			shared = "yes"
			if inst.SharedCheckedAt != nil {
				checked = time.Since(*inst.SharedCheckedAt).Round(time.Second).String() + " ago"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			pluginwire.Printable(inst.Name), pluginwire.Printable(inst.GCPProject),
			pluginwire.Printable(inst.Region), shared, checked, checkoutCount(inst.Checkouts))
	}
	return tw.Flush()
}

// checkoutCount is the table's summary of an installation's checkouts: how
// many this machine remembers, and how many of them are not valid any more.
// The paths and the reasons are the document's, not the table's — a column
// per path would be a table nobody reads, and the reasons are codes.
func checkoutCount(cs []configListCheckout) string {
	if len(cs) == 0 {
		return "-"
	}
	stale := 0
	for _, c := range cs {
		if !c.Valid {
			stale++
		}
	}
	if stale == 0 {
		return fmt.Sprintf("%d", len(cs))
	}
	return fmt.Sprintf("%d (%d stale)", len(cs), stale)
}
