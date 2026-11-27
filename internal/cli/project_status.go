package cli

// `fugaro project repos status [REPO]` is the doctor for a repository's
// enrolment (D2411): the nine rows of the spec's §2.4, in that order, for one
// repository a caller names or for every repository this project knows.
//
// Why it exists at all. Enrolling a repository takes six steps spread over four
// stores, and nothing told an operator what was left. The failure that made
// that visible is the empty container: a repository's `github-app-key`
// container is Terraform's and holds no version until a person stores one, so
// the readiness gate plans NO JOB for a workflow mounting it -- the enrolment
// reads as complete and the repository has no job at all. The fact was known
// (`doctor`'s project-secret-stored check, the App check, readiness.go's own
// sentence), it was not where an operator enrolling a repository looks, and
// `doctor` is not per-repository (F11).
//
// It is not a second doctor (F12). Where a check already exists this command
// calls doctor's own function rather than restating it -- claudeAccountsStoredCheck
// for the account tokens, doctorListedJobs and invokerGranted for the job and
// its grant, newestEnabledVersion for a version's age -- and keeps doctor's own
// ids where the row IS doctor's check. What it adds is the four facts that had
// no check -- the image map entry (F8), credentials_service_ready (F13), the
// job and its run.invoker grant (F10) and the last run -- plus the material the
// view already resolves (RE0's repos_view.go), which is the one derivation of
// "what is this repository made of" and not a third one (F8, F9).
//
// It writes nothing, on any path, --json included (D2412): every read here is a
// listing, a metadata read or an object read, and no secret VALUE is ever
// fetched (D2415). The credential row names a container, whether it holds an
// enabled version and that version's age -- never what the version holds.
//
// Every failing row names one command: `secrets set` for a container with no
// enabled version, `image build <entry>` for an entry nothing built,
// `update --repo <checkout>` for a missing job, and `project repos add` for a
// repository nothing admits. A row that says what is wrong and not what to type
// is the state the empty container already put the operator in (F11).
//
// The exit status is checksFail's own rule (D2412): nonzero when a row that
// blocks a launch fails, zero otherwise, and a warning row never changes it. A
// row whose READ did not answer is a warning rather than a failure -- "I could
// not look" is not "it is broken", and reporting the first as the second makes
// every script that calls this command read a network blip as a broken
// installation.
//
// A repository named on the command line that no half of the enrolment knows is
// its OWN row and a nonzero exit (F25, D2411): an operator who mistyped a name
// must not be told that a repository they do not have is misconfigured, and
// must not be shown an empty screen either -- a read that answered nothing and
// a read that did not answer are different facts (F14's rule).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The rows' identifiers. They are stable and they are the document's own: a
// reader outside this binary (the console's json contract) tells one row from
// another by this member, and a row that means a fact keeps its id whatever
// else moves. claude-accounts-stored is doctor's own id, and so is the
// project-layer row's spelling, because those rows ARE doctor's facts.
const (
	reposStatusRowApp         = "app-installation"
	reposStatusRowCredential  = "credential-version"
	reposStatusRowAccounts    = "claude-accounts-stored"
	reposStatusRowImage       = "image-map"
	reposStatusRowAllowDataTo = "allow-data-to"
	reposStatusRowBroker      = "credentials-service-ready"
	reposStatusRowJobGrant    = "job-and-grant"
	reposStatusRowLayerCopy   = "project-layer-copy"
	reposStatusRowLastRun     = "last-run"
	// reposStatusRowEnrolment is the tenth row and it is not one of the nine:
	// it is the answer for a repository the caller NAMED that no half of the
	// enrolment knows (F25). It is a row of its own rather than a failing one
	// of the nine, because "you do not have this repository" and "this
	// repository is misconfigured" are answers an operator acts on
	// differently.
	reposStatusRowEnrolment = "enrolment"
)

// reposStatusDoc is `status --json`'s document: the version, the installation
// the read was aimed at, and one entry per repository.
//
// Its shape is closed in the sense every document of this group is (RE0's
// repos_view.go): a member this build does not write is not in it, and every
// list is [] and never null. It carries its own version because the desktop
// console and the history read it outside this binary, and a reused member name
// whose meaning shifted would draw a wrong figure with no error (CLAUDE.md's
// wire-format rule).
type reposStatusDoc struct {
	Version int               `json:"version"`
	Project string            `json:"project"`
	Repos   []reposStatusRepo `json:"repos"`
}

// reposStatusRepo is one repository's answer: which repository it is, whether
// anything enrolls it, the sentence for an enrolment that could not be decided,
// and the rows.
type reposStatusRepo struct {
	Canonical string `json:"canonical"`
	// Enrolled is whether either half of the enrolment knows the repository.
	// False means "nothing enrolls it" only when State is empty: with a half
	// unread, the same false is a read that did not answer.
	Enrolled bool `json:"enrolled"`
	// State is the sentence for an enrolment that could not be decided, and ""
	// when it could.
	State string `json:"state"`
	// Rows are the nine of spec §2.4 in order, and the enrolment row alone for
	// a repository nothing enrolls.
	Rows []reposStatusRow `json:"rows"`
}

// reposStatusRow is one row as the document writes it: the check's own id, its
// verdict, its problem sentence and its fix.
//
// It is its own type rather than doctorCheck because doctorCheck's JSON member
// names are Go names and this document is read outside this binary: the
// contract's member names are lower case and spelled here.
type reposStatusRow struct {
	ID string `json:"id"`
	OK bool   `json:"ok"`
	// Problem is the row's sentence whether it passed or failed: a passing row
	// says what was found, which is what makes the document readable rather
	// than a list of booleans.
	Problem  string `json:"problem"`
	Fix      string `json:"fix,omitempty"`
	Severity string `json:"severity,omitempty"`
	// Skipped is true for a row this build did not evaluate. No row writes it
	// today -- status never skips one of the nine -- and the member is here so
	// that a reader can tell "not checked" from "checked and fine" without a
	// document version bump if a later release adds a skip.
	Skipped bool `json:"skipped,omitempty"`
}

// newProjectReposStatusCmd is the command. It takes at most one repository: a
// second one is a usage failure, which cobra answers before RunE runs and which
// this package's ExitCode maps to ExitUserError.
func newProjectReposStatusCmd() *cobra.Command {
	var o projectReposOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [REPO]",
		Short: "Whether a repository can run: the enrolment's rows, each failing one naming its fix",
		Long: "Print a doctor for a repository's enrolment in this project: one row per fact that\n" +
			"decides whether a run of it can start, in the order the design fixes, with the one\n" +
			"command that puts each failing row right. With no argument it prints one line per\n" +
			"repository and the failing rows under it; with a canonical name it prints that\n" +
			"repository's rows in full.\n" +
			"\n" +
			"A repository named that nothing enrolls is a line of its own and a nonzero exit,\n" +
			"rather than an empty screen or a row saying a repository it does not have is\n" +
			"misconfigured.\n" +
			"\n" +
			"It writes nothing, on every path including --json, and it reads credentials'\n" +
			"presence and never a value: a container's name, whether it holds an enabled\n" +
			"version, and that version's age.\n" +
			"\n" +
			"The exit status is nonzero when a row that blocks a launch fails, and zero\n" +
			"otherwise; a warning row never changes it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposStatus(cmd, &o, args, asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectReposStatus(cmd *cobra.Command, o *projectReposOptions, args []string, asJSON bool) error {
	ctx := cmd.Context()
	// The usage check is first and it is pure: a name that is not canonical is
	// refused before the project is opened, the gate is asked or any read is
	// made. Doing it later would ask a role -- and read a store -- for a command
	// line this program can already see is not usable.
	named := ""
	if len(args) == 1 {
		want, err := reposViewCanonicalOf(args[0])
		if err != nil {
			return err
		}
		named = want
	}
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	// The read's own gate (D2414), asked after the usage check and before the
	// view reads the policy, the bucket or Secret Manager.
	if err := accessGateProjectRead(ctx, lc, db,
		"the enrolment's state is a project-level read, so nothing about this repository is shown",
		"reading the enrolment"); err != nil {
		return err
	}
	v, err := newReposView(ctx, lc, db, named)
	if err != nil {
		return err
	}
	reads := &reposStatusReads{lc: lc, view: v}
	doc := reposStatusDoc{Version: reposStatusDocVersion, Project: lc.Name}
	// A named repository is the whole screen: the caller asked about one, and
	// the nine rows of every OTHER repository are not an answer to that. Where
	// the name is one the view built no row for -- a repository no half of the
	// enrolment knows -- the row is drawn from the view's own answer for it
	// rather than left out (F25, D2411): an empty screen is the one thing a
	// mistyped name must not produce.
	if named != "" {
		rows := v.Repos
		if i := slices.IndexFunc(rows, func(r reposViewRepo) bool { return r.Canonical == named }); i >= 0 {
			rows = rows[i : i+1]
		} else {
			rows = []reposViewRepo{{Canonical: named, Provider: strings.SplitN(named, "/", 2)[0]}}
		}
		for _, row := range rows {
			doc.Repos = append(doc.Repos, reposStatusRepoOf(ctx, lc, row, reads))
		}
	} else {
		for _, row := range v.Repos {
			doc.Repos = append(doc.Repos, reposStatusRepoOf(ctx, lc, row, reads))
		}
	}
	doc.Repos = nonNil(doc.Repos)
	return reposStatusPrint(cmd.OutOrStdout(), doc, asJSON)
}

// reposStatusReads carries what more than one row reads, so a screen that shows
// every repository asks each store once rather than once per row.
type reposStatusReads struct {
	lc   *localcfg.Config
	view *reposView

	projectOnce bool
	projectList []gcp.SecretInfo
	projectOK   bool

	jobsOnce bool
	jobs     []doctorListedJob
	jobsOK   bool
	jobsWhy  string

	bindOnce     bool
	bindings     []iamBinding
	bindUnknown  bool
	bindReadable bool
	bindWhy      string

	bucketOnce bool
	bucket     *blobx.Bucket
	bucketWhy  string
}

// reposStatusRepoOf builds one repository's answer.
//
// The default is the listing's answer, and it is Enrolled: a row exists only
// for a repository a half of the enrolment knows (reposViewRowNames' two
// sources), so every row of a listing is enrolled by construction and its state
// is decided. D2411's reading of the pair — false with an empty state sentence
// means "nothing enrolls it" — would otherwise be printed about repositories
// this project does have, with no error anywhere, which is the one figure the
// console must never draw about an enrolled repository. Only the answer for a
// repository the caller NAMED can say anything else, and only about that one.
func reposStatusRepoOf(ctx context.Context, lc *localcfg.Config, row reposViewRepo, reads *reposStatusReads) reposStatusRepo {
	out := reposStatusRepo{Canonical: row.Canonical, Enrolled: true}
	if n := reads.view.Named; n != nil && n.Canonical == row.Canonical {
		out.Enrolled, out.State = n.Enrolled, n.State
		// A repository the caller named that no half of the enrolment knows is
		// the enrolment row alone (F25): it has no enrolment for the nine to be
		// about, and printing them against a repository this project does not
		// have is exactly the empty screen D2411 refuses.
		if !n.Enrolled {
			out.Rows = reposStatusRows([]doctorCheck{reposStatusRowEnrolmentOf(row.Canonical)})
			return out
		}
	}
	out.Rows = reposStatusRows(reposStatusChecks(ctx, lc, row, reads))
	return out
}

// reposStatusChecks is the nine rows of spec §2.4, in order.
//
// The credential containers are read once for the repository and handed to the
// two rows that need them: the view reads them for the repository a caller
// named and for no other, while this screen draws every repository, and a read
// per row would be two Secret Manager listings per repository for one answer.
func reposStatusChecks(ctx context.Context, lc *localcfg.Config, row reposViewRepo, reads *reposStatusReads) []doctorCheck {
	creds := row.Credentials
	if creds == nil {
		creds = reposViewCredentialsOf(ctx, lc, row.Provider, reposViewLocalNameOf(row.Canonical, row.Local.Key))
	}
	return []doctorCheck{
		reposStatusRowAppOf(lc, row),
		reposStatusRowCredentialOf(lc, row, creds),
		reposStatusRowAccountsOf(ctx, lc, row, creds, reads),
		reposStatusRowImageOf(ctx, lc, row, reads),
		reposStatusRowAllowDataToOf(lc, row),
		reposStatusRowBrokerOf(lc, row),
		reposStatusRowJobGrantOf(ctx, lc, row, reads),
		reposStatusRowLayerCopyOf(row),
		reposStatusRowLastRunOf(ctx, lc, row, reads),
	}
}

// reposStatusRows is doctorCheck's shape as the document writes it.
func reposStatusRows(checks []doctorCheck) []reposStatusRow {
	out := make([]reposStatusRow, 0, len(checks))
	for _, c := range checks {
		out = append(out, reposStatusRow{ID: c.ID, OK: c.OK, Problem: c.Problem, Fix: c.Fix, Severity: c.Severity, Skipped: c.Skipped})
	}
	return out
}

// reposStatusRowEnrolmentOf is the answer for a repository nothing enrolls.
func reposStatusRowEnrolmentOf(canonical string) doctorCheck {
	return doctorCheck{ID: reposStatusRowEnrolment,
		Problem: fmt.Sprintf("%s is not enrolled in this project: neither this machine's config holds it nor does the published allow-list admit it, so no run of it can start here", pluginwire.Printable(canonical)),
		Fix:     selfCommand() + " project repos add " + quoteWord(canonical) + " enrolls it, and " + selfCommand() + " project repos ls lists what is enrolled now"}
}

// reposStatusRowAppOf is the first row: the provider's App is installed on the
// repository, which for GitHub means the two stores this machine holds record
// it -- the published allow-list admits it and this machine carries the App's
// id -- and for Bitbucket means the workspace token's own admission.
//
// It reads no provider API. Asking GitHub about the installation is
// `doctor --check-github-app`'s own call, made behind its flag because it needs
// the App's private key and the network and is refused in an agent's session;
// this row answers what the stores this program already holds say.
func reposStatusRowAppOf(lc *localcfg.Config, row reposViewRepo) doctorCheck {
	if !row.Published.Admitted {
		return doctorCheck{ID: reposStatusRowApp,
			Problem: fmt.Sprintf("the published allow-list admits no entry for %s, so a run of it is refused before it mints a git token", pluginwire.Printable(row.Canonical)),
			Fix:     selfCommand() + " project repos add " + quoteWord(row.Canonical)}
	}
	if row.Provider != "github" {
		return doctorCheck{ID: reposStatusRowApp, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the allow-list admits %s by %s, and its provider credential is the workspace token", pluginwire.Printable(row.Canonical), pluginwire.Printable(row.Published.Pattern))}
	}
	if !row.Local.Present || lc.Repos[row.Local.Key].GitHubAppID == "" {
		return doctorCheck{ID: reposStatusRowApp,
			Problem: fmt.Sprintf("%s is admitted, and this machine's config records no GitHub App id for it, so what a run of it authenticates as is not recorded here", pluginwire.Printable(row.Canonical)),
			Fix:     selfCommand() + " project repos add " + quoteWord(row.Canonical)}
	}
	return doctorCheck{ID: reposStatusRowApp, OK: true, Severity: "info",
		Problem: fmt.Sprintf("the allow-list admits %s and this machine records GitHub App %s for it; whether the App is really installed on the repository is %s's own reading",
			pluginwire.Printable(row.Canonical), pluginwire.Printable(lc.Repos[row.Local.Key].GitHubAppID), selfCommand()+" doctor --check-github-app")}
}

// reposStatusRowCredentialOf is the row the whole order exists for: the
// repository's mounted credential holds an ENABLED version.
//
// Presence, a name, a version's state and an age, never a value (D2415). The
// read is RE0's own (reposViewCredentialsOf), which lists a container's
// metadata through gcp.Secrets.List and dates its newest enabled version
// through doctor's own newestEnabledVersion. A repository the caller named
// already carries that read in the view; every other one is read here, because
// a screen that shows one line per repository would otherwise say nothing about
// the failure F11 is about.
//
// A listing that did not answer is a warning and not a failure: reporting "I
// could not look" as a container with no version would send an operator to
// store a value that is already there.
func reposStatusRowCredentialOf(lc *localcfg.Config, row reposViewRepo, creds *reposViewCredentials) doctorCheck {
	want := config.GitHubAppKeySecret
	if row.Provider != "github" {
		want = config.BitbucketSecret
	}
	repo := reposViewLocalNameOf(row.Canonical, row.Local.Key)
	if creds.State != "" {
		return doctorCheck{ID: reposStatusRowCredential, Severity: "warning",
			Problem: fmt.Sprintf("whether %s's %s container holds an enabled version could not be read: %s", pluginwire.Printable(row.Canonical), pluginwire.Printable(want), creds.State),
			Fix:     "rerun with Secret Manager reachable and your credentials available, or check by hand: " + selfCommand() + " secrets ls --repo " + quoteWord(repo)}
	}
	i := slices.IndexFunc(creds.Entries, func(e reposViewCredential) bool { return e.Name == want })
	switch {
	case i < 0:
		return doctorCheck{ID: reposStatusRowCredential,
			Problem: fmt.Sprintf("%s has no %s container among the credentials this project stores for it, so no run of a workflow mounting it can start: Terraform plans no job for a secret with no version", pluginwire.Printable(row.Canonical), pluginwire.Printable(want)),
			Fix:     selfCommand() + " secrets set " + quoteWord(want) + " --repo " + quoteWord(repo)}
	case !creds.Entries[i].Enabled:
		return doctorCheck{ID: reposStatusRowCredential,
			Problem: fmt.Sprintf("%s's %s container holds no enabled version: this is the empty container Terraform creates and never fills, no run of a workflow mounting it can start, and the readiness gate plans no job for it", pluginwire.Printable(row.Canonical), pluginwire.Printable(want)),
			Fix:     selfCommand() + " secrets set " + quoteWord(want) + " --repo " + quoteWord(repo)}
	}
	e := creds.Entries[i]
	// An enabled version older than the operator's own rotation period is a
	// warning and never a failure (D2412): the runs work, and the row is where
	// the rotation is asked for.
	if e.AgeDays != nil && lc.SecretRotationDays > 0 && *e.AgeDays >= lc.SecretRotationDays {
		return doctorCheck{ID: reposStatusRowCredential, Severity: "warning",
			Problem: fmt.Sprintf("%s's %s container holds an enabled version %s old, past the %d-day period this machine records",
				pluginwire.Printable(row.Canonical), pluginwire.Printable(want), ageDays(time.Duration(*e.AgeDays)*24*time.Hour), lc.SecretRotationDays),
			Fix: selfCommand() + " secrets set " + quoteWord(want) + " --repo " + quoteWord(repo)}
	}
	return doctorCheck{ID: reposStatusRowCredential, OK: true, Severity: "info",
		Problem: fmt.Sprintf("%s's %s container holds an enabled version (%s)", pluginwire.Printable(row.Canonical), pluginwire.Printable(want), credentialAge(e))}
}

// credentialAge words an enabled container's age, and says so when the version
// could not be dated rather than drawing "less than a day": the two are
// different facts and one of them is an answer this read did not get.
func credentialAge(e reposViewCredential) string {
	if e.AgeDays == nil {
		return "its creation time was not read"
	}
	return ageDays(time.Duration(*e.AgeDays)*24*time.Hour) + " old"
}

// reposStatusRowAccountsOf is doctor's own claude-accounts-stored check for this
// repository, and a passing row of its own where doctor has nothing to say (a
// repository whose config names no account of its own).
func reposStatusRowAccountsOf(ctx context.Context, lc *localcfg.Config, row reposViewRepo, creds *reposViewCredentials, reads *reposStatusReads) doctorCheck {
	repo := reposViewLocalNameOf(row.Canonical, row.Local.Key)
	if len(namedClaudeAccounts(lc, repo)) == 0 {
		return doctorCheck{ID: reposStatusRowAccounts, OK: true, Severity: "info",
			Problem: fmt.Sprintf("%s names no Claude account of its own, so it has no separate token to have stored", pluginwire.Printable(row.Canonical))}
	}
	// nil says "the listing could not be attempted", which is the warning
	// doctor's own check draws and not an installation that stores nothing.
	// Only the credential read's own state earns that: where it answered, the
	// entry list is the repository's containers whether or not any of them is
	// an account token.
	var listed []secretEntry
	if creds.State == "" {
		listed = []secretEntry{}
		for _, e := range creds.Entries {
			latest := ""
			if e.Enabled {
				latest = "enabled"
			}
			listed = append(listed, secretEntry{Name: e.Name, SecretInfo: gcp.SecretInfo{ID: e.ID, Latest: latest}})
		}
	}
	project, ok := reads.projectSecrets(ctx)
	c := claudeAccountsStoredCheck(lc, repo, listed, project, ok)
	if c == nil {
		return doctorCheck{ID: reposStatusRowAccounts, OK: true, Severity: "info",
			Problem: fmt.Sprintf("%s names no Claude account of its own", pluginwire.Printable(row.Canonical))}
	}
	return *c
}

// namedClaudeAccounts is the accounts a repository may be served by, the
// default excluded -- config.AllowedClaudeAccounts' own list, which is the one
// both this row and doctor's check read.
func namedClaudeAccounts(lc *localcfg.Config, repo string) []string {
	var out []string
	for _, n := range config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo) {
		if n != config.DefaultClaudeAccount {
			out = append(out, n)
		}
	}
	return out
}

// reposStatusRowImageOf is the image map entry (F8): which catalog entry this
// repository's job runs, which answer chose it, and whether that entry is
// built.
//
// The entry and the source are RE0's resolution, so the entry `add` writes and
// the entry this reports are one answer (F9, D2416). The freshness half reads
// the build record the entry's build writes (catalogEntryRecord's shape, best
// effort, as doctor's catalogImageChecks reads it) and compares it with the
// definition the runs bucket holds; what cannot be read says nothing here
// rather than claiming an entry is unbuilt.
func reposStatusRowImageOf(ctx context.Context, lc *localcfg.Config, row reposViewRepo, reads *reposStatusReads) doctorCheck {
	var unresolved []string
	for _, w := range row.Workflows {
		if w.State != "" {
			unresolved = append(unresolved, pluginwire.Printable(w.Name)+": "+w.State)
		}
	}
	if len(unresolved) > 0 {
		return doctorCheck{ID: reposStatusRowImage,
			Problem: fmt.Sprintf("%s's image is not resolved: %s", pluginwire.Printable(row.Canonical), strings.Join(unresolved, "; ")),
			Fix:     selfCommand() + " update --repo in " + quoteWord(reposViewLocalNameOf(row.Canonical, row.Local.Key)) + "'s checkout re-resolves it, or fix the image_name: or needs: line the repository's fugaro.yaml names"}
	}
	problem := fmt.Sprintf("%s runs %s, chosen by the %s", pluginwire.Printable(row.Canonical), imageEntryWord(row), imageSourceWord(row.ImageSource))
	// Only a catalog entry a Fugaro build writes has a build record to read: a
	// checkout: clone or shared job's own image is the installation's, and a
	// repository's own image is `fugaro update`'s. Those are said and not
	// judged, because a row that called one of them unbuilt would send a person
	// to a build that does not happen.
	switch row.ImageSource {
	case reposViewImageWorkflow, reposViewImageMap, reposViewImageDefault:
	default:
		return doctorCheck{ID: reposStatusRowImage, OK: true, Severity: "info", Problem: problem}
	}
	state, why := reads.entryBuilt(ctx, row.ImageEntry)
	switch {
	case why != "":
		return doctorCheck{ID: reposStatusRowImage, OK: true, Severity: "info",
			Problem: problem + "; whether that entry is built is not in this answer: " + why}
	case state == entryUnbuilt:
		return doctorCheck{ID: reposStatusRowImage,
			Problem: problem + ", and no build has built that entry, so the jobs of a repository that runs it have no image to start",
			Fix:     selfCommand() + " image build " + quoteWord(row.ImageEntry) + " builds it once, and the daily image check rebuilds it from then on"}
	case state == entryStale:
		return doctorCheck{ID: reposStatusRowImage, Severity: "warning",
			Problem: problem + ", and its image was built from another definition than the runs bucket holds now",
			Fix:     "the daily image check rebuilds it; to do it now: " + selfCommand() + " image build " + quoteWord(row.ImageEntry)}
	}
	return doctorCheck{ID: reposStatusRowImage, OK: true, Severity: "info", Problem: problem + ", and that entry is built and current"}
}

// entryState is what the build record says about a catalog entry.
type entryState int

const (
	entryBuilt entryState = iota
	entryUnbuilt
	entryStale
)

// entryBuilt reads the build record of a catalog entry over the slugs of the
// repositories this machine's config holds -- the record lives under the slug
// of the repository the build ran as, which for an entry whose source names a
// repository is that repository's -- and compares its definition digest with
// the copy the runs bucket holds.
//
// Best effort, deliberately: a bucket that could not be opened, a record that
// is there and unreadable and an entry whose name is not one a record is keyed
// by all give up the freshness half with a sentence rather than failing the
// row. An entry with no record at all is the one case that reads as unbuilt,
// which is what doctor's own catalog check does with it.
func (r *reposStatusReads) entryBuilt(ctx context.Context, entry string) (entryState, string) {
	b, why := r.records(ctx)
	if b == nil {
		return entryBuilt, why
	}
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		return entryBuilt, "the entry's name is not one a build record is keyed by"
	}
	var rec *imagecheck.CatalogRecordState
	for _, slug := range r.entrySlugs() {
		k, err := gcp.CatalogRecordKey(slug, entry)
		if err != nil {
			continue
		}
		data, _, err := b.Read(ctx, k)
		if errors.Is(err, blobx.ErrNotExist) {
			continue
		}
		if err != nil {
			return entryBuilt, "the build record could not be read"
		}
		parsed, err := imagecheck.ParseCatalogRecordState(data)
		if err != nil {
			return entryBuilt, "the build record could not be read"
		}
		rec = parsed
		break
	}
	if rec == nil {
		return entryUnbuilt, ""
	}
	data, _, err := b.Read(ctx, key)
	if err != nil {
		return entryBuilt, "the definition the runs bucket holds could not be read"
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) == rec.DefinitionSHA256 {
		return entryBuilt, ""
	}
	return entryStale, ""
}

// entrySlugs is where an entry's build record may live: the slugs of the
// repositories this machine's config holds, which is doctor's
// catalogEntrySlugs list without the checkout it reads a source repository
// through.
func (r *reposStatusReads) entrySlugs() []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(r.lc.Repos)) {
		slug, err := task.Slug(r.lc.Repos[name].Provider, name)
		if err == nil && !slices.Contains(out, slug) {
			out = append(out, slug)
		}
	}
	return out
}

// imageEntryWord is the entry in a row's sentence: the catalog entry's name, or
// the image the project or the installation provides where no entry is involved.
func imageEntryWord(row reposViewRepo) string {
	if row.ImageEntry != "" {
		return "the project image catalog's entry " + pluginwire.Printable(row.ImageEntry)
	}
	switch row.ImageSource {
	case reposViewImageRepo:
		return "the image this project builds for it"
	case reposViewImageBase:
		return "the installation's base image"
	case reposViewImageShared:
		return "the shared dependency-layer image"
	}
	return "no image this project can resolve"
}

// imageSourceWord names which answer chose the entry, in the words the resolver
// itself uses (D2409): the three that name a catalog entry are told apart
// because a person reading the row is asking about their own file, and the
// resolver answers one word for all three.
func imageSourceWord(s reposViewImageSource) string {
	switch s {
	case reposViewImageWorkflow:
		return "repository's own image_name:/needs:"
	case reposViewImageMap:
		return "project's image map"
	case reposViewImageDefault:
		return "catalog's default entry"
	case reposViewImageRepo:
		return "project's own answer for the repository"
	case reposViewImageBase:
		return "installation's base image"
	case reposViewImageShared:
		return "shared dependency layer"
	}
	return "nothing, which is a refusal"
}

// reposStatusRowAllowDataToOf is the fifth row: the accounts whose
// allow_data_to names this repository, plus the provider names this machine
// leaves open to every repository (config.OpenProviders, doctor's own reading
// of the same fact).
func reposStatusRowAllowDataToOf(lc *localcfg.Config, row reposViewRepo) doctorCheck {
	name := reposViewLocalNameOf(row.Canonical, row.Local.Key)
	admits := namedClaudeAccounts(lc, name)
	problem := fmt.Sprintf("no Claude account's allow_data_to names %s", pluginwire.Printable(name))
	if len(admits) > 0 {
		problem = fmt.Sprintf("%s is named by the account(s) %s", pluginwire.Printable(name), strings.Join(printableNames(admits), ", "))
	}
	open := config.OpenProviders(lc.Providers)
	if len(open) == 0 {
		return doctorCheck{ID: reposStatusRowAllowDataTo, OK: true, Severity: "info", Problem: problem}
	}
	// A provider open to every repository is a warning and never a failure: the
	// owner ruled that state valid, and this only makes it visible beside the
	// repository it admits.
	return doctorCheck{ID: reposStatusRowAllowDataTo, Severity: "warning",
		Problem: problem + ", and every repository may also use " + strings.Join(printableNames(open), ", ") + ", which name no repository (an absent allow_data_to, or the explicit [\"*\"] marker)",
		Fix:     "add allow_data_to: [" + quoteWord(name) + "] to those providers in your local config, or leave them open to every repository on purpose"}
}

// reposStatusRowBrokerOf is credentials_service_ready (F13): the RECORDED fact,
// read through localcfg's own reader (CredentialsServiceOn) rather than
// re-derived from the bundle, because "no record" and "recorded false" are
// different documents and the field is a pointer for exactly that reason.
func reposStatusRowBrokerOf(lc *localcfg.Config, row reposViewRepo) doctorCheck {
	p := pluginwire.Printable(row.Canonical)
	if row.Local.Key == "" {
		return doctorCheck{ID: reposStatusRowBroker, Severity: "warning",
			Problem: fmt.Sprintf("this machine records no entry for %s, so whether the credentials service serves it is not in this answer", p),
			Fix:     selfCommand() + " project repos add " + quoteWord(row.Canonical)}
	}
	r := lc.Repos[row.Local.Key]
	switch {
	case lc.CredentialsService == nil:
		return doctorCheck{ID: reposStatusRowBroker, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the installation has no credentials service (%s), so nothing is brokered for %s and its runs hold their own model credential", infra.CredentialsServiceName, p)}
	case r.CredentialsServiceReady == nil:
		return doctorCheck{ID: reposStatusRowBroker, Severity: "warning",
			Problem: fmt.Sprintf("the installation has the credentials service (%s) and nothing records whether %s is served by it: no apply has run at this repository's own root since the service was deployed", infra.CredentialsServiceName, p),
			Fix:     selfCommand() + " update --repo in " + quoteWord(row.Canonical) + "'s checkout"}
	case !r.CredentialsServiceOn():
		return doctorCheck{ID: reposStatusRowBroker, Severity: "warning",
			Problem: fmt.Sprintf("%s's own root recorded that the credentials service does NOT serve it: the service is deployed and this repository's runs hold their own credential, which is what an agent.auth other than cascade gives it", p),
			Fix:     selfCommand() + " update --repo in " + quoteWord(row.Canonical) + "'s checkout re-applies it once its fugaro.yaml runs with agent.auth: cascade"}
	}
	return doctorCheck{ID: reposStatusRowBroker, OK: true, Severity: "info",
		Problem: fmt.Sprintf("recorded: the credentials service serves %s", p)}
}

// reposStatusRowLayerCopyOf is the eighth row: this machine's project config
// holds the repository's repos: entry, which is the half a launch from here
// reads its workflows, base branch and accounts from.
func reposStatusRowLayerCopyOf(row reposViewRepo) doctorCheck {
	if row.Local.Present {
		return doctorCheck{ID: reposStatusRowLayerCopy, OK: true, Severity: "info",
			Problem: fmt.Sprintf("this machine's project config holds %s under %s", pluginwire.Printable(row.Canonical), pluginwire.Printable(row.Local.Key))}
	}
	return doctorCheck{ID: reposStatusRowLayerCopy,
		Problem: fmt.Sprintf("this machine's project config holds no entry for %s, so a launch of it from here has no workflows, base branch or accounts to read", pluginwire.Printable(row.Canonical)),
		Fix:     selfCommand() + " project repos add " + quoteWord(row.Canonical)}
}

// reposStatusRowLastRunOf is the ninth row: the last run of the repository and
// its outcome, read from the runs bucket's own record through runstore -- the
// writer the runner uses and the one place the outcome lives.
func reposStatusRowLastRunOf(ctx context.Context, lc *localcfg.Config, row reposViewRepo, reads *reposStatusReads) doctorCheck {
	p := pluginwire.Printable(row.Canonical)
	repo := reposViewLocalNameOf(row.Canonical, row.Local.Key)
	slug, err := task.Slug(row.Provider, repo)
	if err != nil {
		return doctorCheck{ID: reposStatusRowLastRun, Severity: "warning",
			Problem: fmt.Sprintf("%s is not a repository whose runs could be looked up: %v", p, err)}
	}
	b, why := reads.records(ctx)
	if b == nil {
		return doctorCheck{ID: reposStatusRowLastRun, Severity: "warning",
			Problem: "the runs bucket could not be opened, so " + p + "'s last run is not in this answer: " + why}
	}
	ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, time.Time{})
	if err != nil {
		return doctorCheck{ID: reposStatusRowLastRun, Severity: "warning",
			Problem: "the runs bucket's records could not be listed, so " + p + "'s last run is not in this answer: " + oneLineCLI(err.Error())}
	}
	if len(ids) == 0 {
		return doctorCheck{ID: reposStatusRowLastRun, OK: true, Severity: "info",
			Problem: fmt.Sprintf("no run of %s is recorded in this project's runs bucket", p)}
	}
	rec, err := runstore.Open(b.Bucket, slug, ids[0]).ReadRecord(ctx)
	if err != nil {
		return doctorCheck{ID: reposStatusRowLastRun, Severity: "warning",
			Problem: fmt.Sprintf("%s's newest run (%s) has no readable record, so its outcome is not in this answer: %s", p, pluginwire.Printable(ids[0]), oneLineCLI(err.Error()))}
	}
	return doctorCheck{ID: reposStatusRowLastRun, OK: true, Severity: "info",
		Problem: fmt.Sprintf("%s's last run is %s, and its outcome is %s", p, pluginwire.Printable(ids[0]), pluginwire.Printable(string(rec.Outcome)))}
}

// reposStatusRowJobGrantOf is the seventh row: the repository's job is
// deployed, and its job account holds run.invoker on the credentials service
// (F10).
//
// Both reads and both judgements are doctor's own -- doctorListedJobs for the
// region's jobs, and the service's IAM policy judged by invokerGranted over
// runInvokeRoles -- so a grant this row calls missing is one doctor calls
// missing.
func reposStatusRowJobGrantOf(ctx context.Context, lc *localcfg.Config, row reposViewRepo, reads *reposStatusReads) doctorCheck {
	p := pluginwire.Printable(row.Canonical)
	repo := reposViewLocalNameOf(row.Canonical, row.Local.Key)
	slug, err := task.Slug(row.Provider, repo)
	if err != nil {
		return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
			Problem: fmt.Sprintf("%s is not a repository whose jobs could be looked up: %v", p, err)}
	}
	jobs, ok, why := reads.listedJobs(ctx)
	if !ok {
		return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
			Problem: "the project's Cloud Run jobs could not be listed, so whether " + p + "'s job is deployed is not in this answer: " + why,
			Fix:     "rerun with the Cloud Run API reachable and your credentials available"}
	}
	if !slices.ContainsFunc(jobs, func(j doctorListedJob) bool { return j.Labels[gcp.LabelRepo] == slug }) {
		return doctorCheck{ID: reposStatusRowJobGrant,
			Problem: fmt.Sprintf("%s has no job in %s: the readiness gate plans none for a workflow mounting a secret with no enabled version, so an enrolment can look complete and a launch still be refused", p, pluginwire.Printable(lc.Region)),
			Fix:     selfCommand() + " update --repo in " + quoteWord(row.Canonical) + "'s checkout (store the credential the workflow mounts first, or it plans no job again)"}
	}
	if lc.CredentialsService == nil {
		return doctorCheck{ID: reposStatusRowJobGrant, OK: true, Severity: "info",
			Problem: fmt.Sprintf("%s's job is deployed, and the installation has no credentials service (%s) for it to invoke", p, infra.CredentialsServiceName)}
	}
	accounts, err := infra.CredentialsJobAccounts(lc)
	if err != nil {
		return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
			Problem: "the job accounts that may invoke the credentials service could not be derived, so the grant was not read: " + oneLineCLI(err.Error())}
	}
	bindings, unknown, readable := reads.serviceBindings(ctx)
	if !readable {
		return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
			Problem: "the credentials service's IAM policy could not be read, so whether " + p + "'s job account may invoke it is not in this answer: " + reads.bindWhy,
			Fix:     "rerun with the Cloud Run and IAM APIs reachable, or read it yourself: gcloud run services get-iam-policy " + infra.CredentialsServiceName}
	}
	granted, matched := false, false
	for _, j := range accounts {
		if j.Repo != repo {
			continue
		}
		matched = true
		member := "serviceAccount:" + gcp.ServiceAccountID(slug, j.Workflow) + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
		yes, unread := invokerGranted(bindings, member)
		if unread || unknown {
			return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
				Problem: fmt.Sprintf("%s (%s) may or may not hold %s on the credentials service: a custom role on its way could not be read", p, pluginwire.Printable(j.Workflow), runInvokeRoles[0]),
				Fix:     "rerun with the IAM API available, or read the policy yourself: gcloud run services get-iam-policy " + infra.CredentialsServiceName}
		}
		if yes {
			granted = true
		}
	}
	// No job account of this repository's at all is a read this row cannot
	// judge, and never "it holds no grant": the list is derived from the
	// installation's profiles, and a repository whose workflows none of them
	// names has nothing here to judge rather than a missing grant.
	if !matched {
		return doctorCheck{ID: reposStatusRowJobGrant, Severity: "warning",
			Problem: fmt.Sprintf("%s's job is deployed, and the installation's profiles name no job account of its own, so whether one may invoke the credentials service is not in this answer", p),
			Fix:     selfCommand() + " update --repo in " + quoteWord(row.Canonical) + "'s checkout plans its jobs once its fugaro.yaml names a workflow the profile covers"}
	}
	if !granted {
		return doctorCheck{ID: reposStatusRowJobGrant,
			Problem: fmt.Sprintf("%s's job account holds no %s on the credentials service (%s), so a run of it that is on the switch would fail to fetch a model credential", p, runInvokeRoles[0], infra.CredentialsServiceName),
			Fix:     selfCommand() + " update --repo in " + quoteWord(row.Canonical) + "'s checkout grants its job accounts " + runInvokeRoles[0] + " on the service"}
	}
	return doctorCheck{ID: reposStatusRowJobGrant, OK: true, Severity: "info",
		Problem: fmt.Sprintf("%s's job is deployed and its job account holds %s on the credentials service", p, runInvokeRoles[0])}
}

// listedJobs lists the region's Cloud Run jobs once for the whole document.
func (r *reposStatusReads) listedJobs(ctx context.Context) ([]doctorListedJob, bool, string) {
	if r.jobsOnce {
		return r.jobs, r.jobsOK, r.jobsWhy
	}
	r.jobsOnce = true
	if r.lc.Endpoints.NoAuth && r.lc.Endpoints.Run == "" {
		r.jobsWhy = "this configuration names no Cloud Run endpoint"
		return nil, false, r.jobsWhy
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(r.lc, r.lc.Endpoints.Run)...)
	if err != nil {
		r.jobsWhy = oneLineCLI(err.Error())
		return nil, false, r.jobsWhy
	}
	jobs, err := doctorListedJobs(ctx, svc, doctorJobsParent(r.lc))
	if err != nil {
		r.jobsWhy = oneLineCLI(err.Error())
		return nil, false, r.jobsWhy
	}
	r.jobs, r.jobsOK = jobs, true
	return r.jobs, true, ""
}

// serviceBindings reads the credentials service's IAM policy once for the whole
// document, the way doctor's own check reads it.
func (r *reposStatusReads) serviceBindings(ctx context.Context) ([]iamBinding, bool, bool) {
	if r.bindOnce {
		return r.bindings, r.bindUnknown, r.bindReadable
	}
	r.bindOnce = true
	if r.lc.Endpoints.NoAuth && r.lc.Endpoints.Run == "" {
		r.bindWhy = "this configuration names no Cloud Run endpoint"
		return nil, true, false
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(r.lc, r.lc.Endpoints.Run)...)
	if err != nil {
		r.bindWhy = oneLineCLI(err.Error())
		return nil, true, false
	}
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", r.lc.GCPProject, r.lc.Region, infra.CredentialsServiceName)
	pol, err := svc.Projects.Locations.Services.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		r.bindWhy = oneLineCLI(err.Error())
		return nil, true, false
	}
	r.bindings, r.bindUnknown, r.bindReadable = bindingsOf(pol.Bindings, runBindingFields), false, true
	return r.bindings, false, true
}

// projectSecrets is the installation's project-wide containers, once, for
// doctor's account-token check (its second input) -- and whether the listing
// answered at all, which that check draws as a warning rather than as an
// installation that stores nothing.
func (r *reposStatusReads) projectSecrets(ctx context.Context) ([]gcp.SecretInfo, bool) {
	if r.projectOnce {
		return r.projectList, r.projectOK
	}
	r.projectOnce = true
	_, all, ok := doctorProjectSecrets(ctx, r.lc)
	r.projectList, r.projectOK = all, ok
	return r.projectList, r.projectOK
}

// records is the runs bucket, opened once for the whole document.
func (r *reposStatusReads) records(ctx context.Context) (*blobx.Bucket, string) {
	if r.bucketOnce {
		return r.bucket, r.bucketWhy
	}
	r.bucketOnce = true
	if r.lc.BucketURL() == "" {
		r.bucketWhy = "this project's config names no runs bucket"
		return nil, r.bucketWhy
	}
	b, err := blobx.Open(ctx, r.lc.BucketURL())
	if err != nil {
		r.bucketWhy = oneLineCLI(err.Error())
		return nil, r.bucketWhy
	}
	r.bucket = b
	return b, ""
}

// reposStatusPrint writes the document, or the screen, and then decides the
// exit status.
func reposStatusPrint(w io.Writer, doc reposStatusDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return err
		}
	} else {
		printReposStatus(w, doc)
	}
	if reposStatusFails(doc) {
		return &ExitError{Code: ExitUserError, Err: errors.New("a repository's enrolment has a failing row; see above")}
	}
	return nil
}

// reposStatusFails is checksFail's own rule over the whole document (D2412):
// any row that blocks a launch fails it, and a warning or an informational row
// never does. It reuses checksFail rather than walking the rows itself, so the
// verdict here and doctor's cannot disagree about which checks count.
func reposStatusFails(doc reposStatusDoc) bool {
	for _, r := range doc.Repos {
		checks := make([]doctorCheck, 0, len(r.Rows))
		for _, row := range r.Rows {
			checks = append(checks, doctorCheck{ID: row.ID, OK: row.OK, Problem: row.Problem, Fix: row.Fix, Severity: row.Severity, Skipped: row.Skipped})
		}
		if checksFail(checks, false) {
			return true
		}
	}
	return false
}

// printReposStatus prints one line per repository and, under it, the rows that
// failed: a passing row collapses into the repository's own line, and a failing
// one carries its whole sentence and its fix, the way doctor's own lines do.
func printReposStatus(w io.Writer, doc reposStatusDoc) {
	if len(doc.Repos) == 0 {
		fmt.Fprintf(w, "project %s enrolls no repository and this machine's config holds none\n", pluginwire.Printable(doc.Project))
		fmt.Fprintf(w, "  %s project repos add <provider/owner/name> enrolls one\n", selfCommand())
		return
	}
	for _, r := range doc.Repos {
		var bad []reposStatusRow
		for _, row := range r.Rows {
			if !row.OK && row.Severity != "info" {
				bad = append(bad, row)
			}
		}
		head := pluginwire.Printable(r.Canonical) + ": "
		if len(bad) == 0 {
			fmt.Fprintln(w, doctorClip(head+"ok ("+strconv.Itoa(len(r.Rows))+" rows checked)", doctorLineLimit))
			continue
		}
		fmt.Fprintln(w, doctorClip(head+strconv.Itoa(len(bad))+" of "+strconv.Itoa(len(r.Rows))+" rows failed", doctorLineLimit))
		for _, row := range bad {
			fmt.Fprintln(w, "  "+doctorClip(statusRowLine(row), doctorLineLimit-2))
		}
	}
}

// statusRowLine is one failing row in the shape doctor's own lines use -- the
// id, the whole problem and the whole fix -- and it is that shape on purpose:
// an operator who has read `fugaro doctor` reads this the same way.
func statusRowLine(r reposStatusRow) string {
	line := r.ID + ": " + r.Problem
	if r.Fix != "" {
		line += " (" + r.Fix + ")"
	}
	return line
}
