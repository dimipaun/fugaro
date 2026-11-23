package cli

// workspace_project_status.go is WU11: `workspace project ls` and `workspace
// project status [<project>]` (D2819, D2820, D2822, D2823, D2824, D2825, R1).
//
// `status` is the workspace's own doctor: one row per fact that decides whether
// a member can run against the workspace, each failing row naming the one
// command that puts it right, in the order the plan's acceptance fixes --
// reachable, linked, the credentials service, the credentials, the rebuild
// images, the caps, the grants waiting for the member's own `fugaro update`, the
// grants left behind by an unlink, the state repository and drift, and then the
// workspace's own rows: admin tampering, a single admin, the workspace IAM
// account's keys and every principal that can act as it. `ls` is the same read
// with one line per member, so an owner sees which member is worth a full look.
//
// Four rules shape the file, and they are why it is one file rather than rows
// spread through the family.
//
//   - It writes nothing, on every path. `--json` and `--check-credentials`
//     included (D2809). The seam below has no write to call for the reads C2
//     does not carry, and the C1/C2 sides it does carry are called only for
//     their reads -- `MemberOps.ReadGrants`, `StateWriter.LastSaved`,
//     `Authority.Holds` -- never for `WriteGrants`, `Save`, `Link` or
//     `Offboard`. TestStatusWritesNothing is what holds that, with fakes whose
//     every write fails the test, and it is why the red this task opened with
//     is the write-nothing test.
//   - A row that could not be read is NEVER a pass. Every read that does not
//     answer becomes a warn whose sentence says what was read and what was not,
//     with the word "not measured" where a comparison could not be made at all
//     (D2815's own rule for a comparison that cannot decide, which the
//     acceptance borrows for `PendingApply`). A member whose facts this machine
//     cannot see therefore reads as a warn that names the command on the
//     member's own machine -- never as a clean member.
//   - Every string a member or its owner controls is escaped at the moment the
//     row is built (pluginwire.Printable), for the reason doctor's own rows do
//     it: an address, a project name, a credential name and a key id all come
//     from a file or an API this program does not write, and a row is printed
//     in a terminal.
//   - The commands are built here and REGISTERED by I1, which also wraps them
//     with the reporter, exactly as WU22's probe is: the family's parent
//     (`workspace project`) is another task's, so this file hands over two
//     constructors and the run function behind them. `newWorkspaceProjectSides`
//     is the one thing I1 fills: until it does, every C1/C2 read answers "this
//     build has no member side" and the rows say so, rather than a nil side
//     panicking or a row pretending to have measured something.
//
// What this machine can read of a member, and what it cannot, is the one design
// decision worth stating here. A workspace admin's own credentials hold the
// mechanism's grant (folder owners, or the workspace IAM account's `roles/owner`
// under D2803), so the reads that go to the member's own projects -- its Secret
// Manager, its Scheduler jobs, its published config -- are made with them and
// nowhere else (D2801: nothing of a member's installation is applied from
// another machine, and nothing here writes one). A member's GCP project is not
// written in the owner's member table, which names the member's Firebase project
// and its job accounts; it is read off the job account's own domain
// (`<id>@<project>.iam.gserviceaccount.com`, which is the project the account
// lives in) and the member's Firebase project stands in when no job account is
// declared, since that is the project a member's installation IS unless its
// two are split (F2, R10). A member the table does not let this machine place
// says so in its rows rather than being read as a project at random.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	scheduler "google.golang.org/api/cloudscheduler/v1"
	iam "google.golang.org/api/iam/v1"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wsProjectDocVersion is the version of the document `--json` writes. The
// console, the mobile app and the history read it outside this binary, so a
// member's name is its contract: a name whose meaning must change gets a new
// name, and a removal is a version bump (CLAUDE.md's wire-format rule).
const wsProjectDocVersion = 1

// The row ids, in the order the acceptance fixes them. They are the document's
// member names and the words a person reads on the line, so they are spelled
// once here and nowhere else.
const (
	rowReachable        = "reachable"
	rowLinked           = "linked"
	rowCredentialsSvc   = "credentials-service"
	rowCredentials      = "credentials"
	rowCredentialsProbe = "credentials-probe"
	rowImages           = "images"
	rowCaps             = "caps"
	rowPending          = "pending-apply"
	rowGrantsLeft       = "unlinked-grants-left"
	rowStateRepo        = "state-repo"
	rowDrift            = "drift"

	rowAdminBinding = "admin-binding"
	rowSingleAdmin  = "single-admin"
	rowAccountKeys  = "workspace-account-keys"
	rowAccountActAs = "workspace-account-actas"
)

// The two windows a row needs and nothing else states. Both are deliberately
// generous: this is a screen an owner reads, not a gate, and a row that went to
// warn over a build that ran on Sunday is a row people learn to ignore.
const (
	// wsImageFreshFor is how stale a member's last successful rebuild may be
	// before the images row warns.
	wsImageFreshFor = 7 * 24 * time.Hour
	// wsSavedFor is how old a member's last saved state snapshot may be before
	// the state row warns.
	wsSavedFor = 14 * 24 * time.Hour
)

// wsStatusLevel is the three-word verdict a row carries: pass, warn or fail.
// The acceptance names exactly these three, and there is no fourth: a fact this
// machine could not read is a warn whose sentence says so (the file's own rule
// above), so a reader never has to tell "fine" from "unread" by the words.
type wsStatusLevel string

const (
	wsPass wsStatusLevel = "pass"
	wsWarn wsStatusLevel = "warn"
	wsFail wsStatusLevel = "fail"
)

// wsRank orders the three levels so a member's worst row can be found.
func wsRank(l wsStatusLevel) int {
	switch l {
	case wsFail:
		return 2
	case wsWarn:
		return 1
	}
	return 0
}

// wsProjectRow is one row of the screen and of the document.
type wsProjectRow struct {
	// ID is the check's id: the contract's member name in the document and the
	// first word of the line.
	ID string `json:"id"`
	// Status is pass, warn or fail.
	Status wsStatusLevel `json:"status"`
	// Detail is the row's sentence whether it passed or not: a passing row says
	// what was found, which is what makes the screen a report rather than a list
	// of verdicts.
	Detail string `json:"detail"`
	// Fix is the one command that puts a row that is not passing right. It is
	// empty on a passing row, and a row that is warn or fail always carries one.
	Fix string `json:"fix,omitempty"`
}

// wsProjectMember is one member's answer: which member it is, its worst row's
// verdict, and the rows in the acceptance's order.
type wsProjectMember struct {
	Project string `json:"project"`
	// Worst is the worst of Rows, which is what `ls` prints beside the member's
	// name: the one line an owner gets per member.
	Worst wsStatusLevel  `json:"worst"`
	Rows  []wsProjectRow `json:"rows"`
}

// wsProjectsDoc is the document both commands write with --json. Its shape is
// closed the way this program's documents are: every list is [] and never null,
// and it carries its own version.
type wsProjectsDoc struct {
	Version   int    `json:"version"`
	Workspace string `json:"workspace"`
	// CheckedCredentials is whether the run was made with --check-credentials,
	// which is the one row whose presence depends on a flag: a reader that
	// compared two documents must be able to tell "the probe found nothing"
	// from "the probe was not asked".
	CheckedCredentials bool `json:"checked_credentials"`
	// Members are the members the owner's member table declares, in
	// project-name order.
	Members []wsProjectMember `json:"members"`
	// Rows are the workspace's own rows: the admins' bindings, the single-admin
	// warning, and the workspace IAM account's keys and actAs holders.
	Rows []wsProjectRow `json:"rows"`
}

// wsMember is one member as the owner's own config declares it, with the pair
// C2's calls name it by.
type wsMember struct {
	// Name is the member's Fugaro project name: the key of the member table.
	Name string
	// Target is the member's GCP project and, when it is a project of its own,
	// its Firebase project (F2, R10).
	Target workspace.MemberTarget
	// Repos are the repositories the member contributes, as the table lists them.
	Repos []string
	// Entry is the table's own entry, for the reads that need what it carries
	// (the member's Fugaro project name, its exchange URL).
	Entry localcfg.WorkspaceMember
}

// wsMemberFacts is everything one pass reads about one member. It is gathered
// once per member so that a screen showing every member asks each store once,
// and so that a row builder is a pure function of what was read rather than a
// second read of its own.
type wsMemberFacts struct {
	// member is the member's own published config, and memberErr why it could
	// not be read. A member this machine cannot read is a set of rows that say
	// so; it is never a set of rows that passed.
	member    *localcfg.Config
	memberErr string

	reachErr string

	creds    memberCredentials
	credsErr string

	broker    brokerState
	brokerErr string

	images    []imageJob
	imagesErr string

	caps    memberCaps
	capsErr string

	held     bool
	holdsErr string

	pending    workspace.Pending
	pendingErr string

	saved      time.Time
	savedFound bool
	savedErr   string

	drift    workspace.DriftReport
	driftErr string

	probe    []workspace.CredentialResult
	probeErr string

	// bindings are what the member's live IAM carries, with the kinds Fugaro
	// knows (D2806, F17). They are what places the workspace IAM account.
	bindings    []workspace.MemberBinding
	bindingsErr string
}

// newWorkspaceProjectLsCmd is `workspace project ls`. It takes no argument: it
// is the one-line-per-member read.
func newWorkspaceProjectLsCmd() *cobra.Command {
	var o cloudOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "One line per member of this workspace, with its worst row",
		Long: "Print one line per member of this workspace: the member, its worst row's\n" +
			"verdict, and that row's sentence and fix. It is `workspace project status`\n" +
			"summarised, so an owner can see which member is worth a full look.\n" +
			"\n" +
			"It reads and writes nothing but reads: no apply, no grant, no snapshot, on\n" +
			"every path including --json.\n" +
			"\n" +
			"The exit status is nonzero when a member has a failing row, and zero otherwise.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceProjectStatus(cmd, &o, nil, asJSON, false, true)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

// newWorkspaceProjectStatusCmd is `workspace project status [PROJECT]`.
// --check-credentials is WU22's probe (D2820): it reads credential values, so
// it is refused in a coding agent's session and in --json it is a row like any
// other. The flag is the one constant WU22's file names, so the refusal this
// command gives and the probe's own cannot come to disagree.
func newWorkspaceProjectStatusCmd() *cobra.Command {
	var o cloudOptions
	var asJSON, check bool
	cmd := &cobra.Command{
		Use:   "status [PROJECT]",
		Short: "Whether a member can run against this workspace, one row per fact",
		Long: "Print a doctor for the workspace's members: one row per fact that decides\n" +
			"whether a member can run against it -- reachable, linked, the credentials\n" +
			"service, the credentials, the rebuild images, the caps, the grants waiting\n" +
			"for the member's own fugaro update, the grants an unlink left, the state\n" +
			"repository, drift -- and then the workspace's own rows: the admins' live\n" +
			"bindings, a single admin, and the workspace IAM account's keys and every\n" +
			"principal that can act as it.\n" +
			"\n" +
			"With a project name it prints that member's rows and the workspace's; with\n" +
			"none it prints every member's.\n" +
			"\n" +
			"Every row is pass, warn or fail, and every row that is not pass names the\n" +
			"one command that puts it right. A fact this machine could not read is a\n" +
			"warn that says what was read and what was not, never a pass: a member whose\n" +
			"own installation is out of this machine's reach is reported as such.\n" +
			"\n" +
			"--check-credentials asks each provider whether the member's model credentials\n" +
			"still work (one minimal authenticated call each, the value never printed).\n" +
			"It reads credential values with your own credentials, so it runs in your own\n" +
			"terminal and is refused in a coding agent's session; a personal token under\n" +
			"a person's own prefix is never probed.\n" +
			"\n" +
			"It writes nothing, on every path including --json and --check-credentials.\n" +
			"\n" +
			"The exit status is nonzero when a row fails, and zero otherwise.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceProjectStatus(cmd, &o, args, asJSON, check, false)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().BoolVar(&check, "check-credentials", false, "ask each provider whether the member's model credentials still work (your own terminal only)")
	addCloudFlags(cmd, &o)
	return cmd
}

// runWorkspaceProjectStatus is the two commands' one run function. The usage
// check comes first and is pure: a project name that is not a member is refused
// before the project is opened or any read is made, and the refusal lists the
// members the table does declare, because a mistyped name must not print an
// empty screen.
func runWorkspaceProjectStatus(cmd *cobra.Command, o *cloudOptions, args []string, asJSON, check, summary bool) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, *o)
	if err != nil {
		return err
	}
	s := newWorkspaceProjectStatus(lc, db)
	s.check, s.summary = check, summary
	named := ""
	if len(args) == 1 {
		named = args[0]
	}
	doc, err := s.Doc(ctx, named)
	if err != nil {
		return err
	}
	return s.Print(cmd.OutOrStdout(), doc, asJSON)
}

// workspaceProjectSides is what I1 installs: the concrete C1 and C2 sides, one
// factory each, and the member-facing reads this file declares. It is a struct
// of function values rather than an interface so that a side this build does not
// have is a nil field that a row reports, and so that I1 can fill it in one
// place. I1 deletes nothing here: it assigns workspaceProjectWiring.
type workspaceProjectSides struct {
	Store     func(ctx context.Context, lc *localcfg.Config, name string) (workspace.Store, error)
	Authority func(ctx context.Context, lc *localcfg.Config) (workspace.Authority, error)
	Member    func(ctx context.Context, lc *localcfg.Config) (workspace.MemberOps, error)
	Writer    func(ctx context.Context, lc *localcfg.Config) (workspace.StateWriter, error)
	Drift     func(ctx context.Context, lc *localcfg.Config) (workspace.Drift, error)
	Probe     func(ctx context.Context, lc *localcfg.Config) (workspace.CredentialProbe, error)
}

// workspaceProjectWiring is the one seam I1 assigns (it is nil until then, and
// every row that needs a side says so). It is a variable and not a parameter so
// that the two command constructors stay argument-free, as the family's other
// constructors are.
var workspaceProjectWiring func(lc *localcfg.Config) workspaceProjectSides

// newWorkspaceProjectStatus builds the read. lc is the config of the project
// this machine selected: the owner's, whose member table is what `ls` lists. db
// is that project's own database, which is where the workspace's credential
// service keeps the marks a run's credential is broken or held under.
func newWorkspaceProjectStatus(lc *localcfg.Config, db *rtdb.Client) *WorkspaceProjectStatus {
	s := &WorkspaceProjectStatus{
		lc:       lc,
		getenv:   os.Getenv,
		terminal: processTerminal(),
		now:      time.Now,
		cloud:    NewWorkspaceMemberCloud(lc, db),
	}
	if workspaceProjectWiring != nil {
		s.sides = workspaceProjectWiring(lc)
	}
	return s
}

// WorkspaceProjectStatus is WU11's read: the members, the rows and the document.
// It is a concrete type with function-typed seams, the shape
// workspace_probe.go uses, so a test drives every row of `status` -- and the
// write-nothing test drives every path -- without a cloud, a bucket or a
// network.
type WorkspaceProjectStatus struct {
	lc       *localcfg.Config
	getenv   func(string) string
	terminal bool
	check    bool
	// summary is the `ls` form: one line per member with its worst row, rather
	// than every row of every member.
	summary bool
	now     func() time.Time

	// sides are the C1/C2 reads, and cloud the member-facing ones.
	sides workspaceProjectSides
	cloud workspaceMemberReads

	// roster is the workspace's own node, read once per run, and the rest of
	// the run's once-only reads.
	rosterOnce bool
	roster     *workspace.Roster
	rosterErr  error

	authOnce bool
	auth     workspace.Authority
	authErr  error

	memberOnce bool
	ops        workspace.MemberOps
	opsErr     error

	writerOnce bool
	writer     workspace.StateWriter
	writerErr  error

	driftOnce bool
	drift     workspace.Drift
	driftErr  error
}

// The four once-helpers below build a C2 side at most once per run. A side that
// cannot be built is remembered as an error, so a screen of twelve members does
// not try twelve times to do the same impossible thing.
func (s *WorkspaceProjectStatus) authority(ctx context.Context) (workspace.Authority, error) {
	if !s.authOnce {
		s.authOnce = true
		if s.sides.Authority == nil {
			s.authErr = errNoWorkspaceSide("the mechanism the workspace holds its members by")
		} else {
			s.auth, s.authErr = s.sides.Authority(ctx, s.lc)
		}
	}
	return s.auth, s.authErr
}

func (s *WorkspaceProjectStatus) memberOps(ctx context.Context) (workspace.MemberOps, error) {
	if !s.memberOnce {
		s.memberOnce = true
		if s.sides.Member == nil {
			s.opsErr = errNoWorkspaceSide("the member's own documents and IAM")
		} else {
			s.ops, s.opsErr = s.sides.Member(ctx, s.lc)
		}
	}
	return s.ops, s.opsErr
}

func (s *WorkspaceProjectStatus) stateWriter(ctx context.Context) (workspace.StateWriter, error) {
	if !s.writerOnce {
		s.writerOnce = true
		if s.sides.Writer == nil {
			s.writerErr = errNoWorkspaceSide("the member's state snapshots")
		} else {
			s.writer, s.writerErr = s.sides.Writer(ctx, s.lc)
		}
	}
	return s.writer, s.writerErr
}

func (s *WorkspaceProjectStatus) driftFunc(ctx context.Context) (workspace.Drift, error) {
	if !s.driftOnce {
		s.driftOnce = true
		if s.sides.Drift == nil {
			s.driftErr = errNoWorkspaceSide("the member's live IAM")
		} else {
			s.drift, s.driftErr = s.sides.Drift(ctx, s.lc)
		}
	}
	return s.drift, s.driftErr
}

// Doc is the whole read: the members in table order, each with its rows, and
// then the workspace's own rows. A named member that the table does not declare
// is a refusal and not an empty document.
func (s *WorkspaceProjectStatus) Doc(ctx context.Context, named string) (wsProjectsDoc, error) {
	// The flag's own refusal comes first, before anything is read: the probe
	// reads credential values with the owner's own credentials, so an agent's
	// session must not reach a read at all (D2820).
	if s.check {
		if err := refuseKeyReadAt(s.getenv, s.terminal, checkCredentialsFlag, "a member project's model credentials"); err != nil {
			return wsProjectsDoc{}, err
		}
	}
	members, err := s.members()
	if err != nil {
		return wsProjectsDoc{}, err
	}
	if named != "" {
		i := slices.IndexFunc(members, func(m wsMember) bool { return m.Name == named })
		if i < 0 {
			names := make([]string, 0, len(members))
			for _, m := range members {
				names = append(names, m.Name)
			}
			return wsProjectsDoc{}, userErr("%q is not a member of the workspace %s: its member table lists %s. A member is added with %s",
				pluginwire.Printable(named), pluginwire.Printable(s.lc.Name), strings.Join(names, ", "), selfCommand()+" workspace project add <project> --workspace "+pluginwire.Printable(s.lc.Name))
		}
		members = members[i : i+1]
	}
	roster, rosterErr := s.readRoster(ctx)
	doc := wsProjectsDoc{Version: wsProjectDocVersion, Workspace: s.lc.Name, CheckedCredentials: s.check}
	facts := make([]wsMemberFacts, 0, len(members))
	for _, m := range members {
		f := s.gather(ctx, m, roster, rosterErr)
		facts = append(facts, f)
		doc.Members = append(doc.Members, s.memberDoc(ctx, m, f))
	}
	doc.Members = nonNil(doc.Members)
	doc.Rows = nonNil(s.workspaceRows(ctx, roster, rosterErr, facts))
	return doc, nil
}

// members is the workspace's member table: the owner's own declaration, in
// project-name order. A config that declares no table at all is a refusal that
// says whose file this is, because this command reads the workspace's own
// half -- the member table lives in the config of the project the workspace is
// named after (spec §5) -- and a member's own machine holds neither half.
func (s *WorkspaceProjectStatus) members() ([]wsMember, error) {
	if s.lc == nil || s.lc.WorkspaceMembers == nil {
		return nil, userErr("this machine's config declares no member table, so there is no workspace to report on: the table lives in the config of the project the workspace is named after -- `fugaro workspace project add <project> --workspace <workspace>` writes it -- and a member's own machine holds neither the table nor the cap")
	}
	table := workspaceTable(s.lc)
	if len(table) == 0 {
		return nil, userErr("the workspace %s declares no member: a workspace with none admits nobody, so declare the projects it groups with %s",
			pluginwire.Printable(s.lc.Name), selfCommand()+" workspace project add <project> --workspace "+pluginwire.Printable(s.lc.Name))
	}
	out := make([]wsMember, 0, len(table))
	for _, name := range slices.Sorted(maps.Keys(table)) {
		entry := table[name]
		out = append(out, wsMember{
			Name:   name,
			Target: wsMemberTarget(entry),
			Repos:  slices.Clone(entry.Repos),
			Entry:  entry,
		})
	}
	return out, nil
}

// wsMemberTarget is the pair C2's calls name a member by (F2, R10). The owner's
// member table names the member's Firebase project and its job accounts, and
// never its GCP project, so the GCP project is read off a job account's own
// domain -- an account's domain IS the project it lives in -- and the Firebase
// project stands in when the table declares no job account, since that is the
// project a member's installation is unless the two are split. A table that
// names neither is a member whose GCP project this machine does not know, and
// the rows say so rather than reading a project at random.
func wsMemberTarget(entry localcfg.WorkspaceMember) workspace.MemberTarget {
	gcpProject := ""
	for _, a := range entry.JobAccounts {
		if p, ok := wsProjectOfJobAccount(a.Account); ok {
			gcpProject = p
			break
		}
	}
	if gcpProject == "" {
		gcpProject = entry.FirebaseProject
	}
	t := workspace.MemberTarget{Project: gcpProject}
	if entry.FirebaseProject != "" && entry.FirebaseProject != gcpProject {
		t.Firebase = entry.FirebaseProject
	}
	return t
}

// wsProjectOfJobAccount is the project a job service account lives in: the
// domain of `<id>@<project>.iam.gserviceaccount.com`, held to the project-name
// grammar so a hand-written entry cannot name a project that is not one.
func wsProjectOfJobAccount(account string) (string, bool) {
	const suffix = ".iam.gserviceaccount.com"
	if !strings.HasSuffix(account, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(account, suffix)
	i := strings.LastIndex(id, "@")
	if i < 0 || i == len(id)-1 {
		return "", false
	}
	project := id[i+1:]
	if !config.ProjectNameRE.MatchString(project) {
		return "", false
	}
	return project, true
}

// readRoster reads the workspace's own node once per run. A workspace whose
// roster was never written is not an error here: it is the state the workspace
// rows report, and the rows that would have read it say it has not been written.
func (s *WorkspaceProjectStatus) readRoster(ctx context.Context) (*workspace.Roster, error) {
	if s.rosterOnce {
		return s.roster, s.rosterErr
	}
	s.rosterOnce = true
	if s.sides.Store == nil {
		s.rosterErr = errNoWorkspaceSide("the workspace's own roster")
		return nil, s.rosterErr
	}
	store, err := s.sides.Store(ctx, s.lc, s.lc.Name)
	if err != nil {
		s.rosterErr = err
		return nil, s.rosterErr
	}
	_, raw, err := store.Read(ctx)
	if err != nil {
		s.rosterErr = err
		return nil, s.rosterErr
	}
	if len(raw) == 0 {
		s.rosterErr = errors.New("the workspace's roster has never been written")
		return nil, s.rosterErr
	}
	r, err := workspace.DecodeRoster(raw)
	if err != nil {
		s.rosterErr = err
		return nil, s.rosterErr
	}
	s.roster = r
	return r, nil
}

// errNoWorkspaceSide is what a row says when this build has no side to read
// through yet: I1 assigns workspaceProjectWiring, and until it does the read is
// named as missing rather than answered with a zero value no side produced.
func errNoWorkspaceSide(what string) error {
	return fmt.Errorf("this build has no member side installed, so %s was not read (a build that registers these commands installs one)", what)
}

// gather is the one pass over one member. Every read is made here and once, so
// that a screen showing every member asks each store a single time, and so that
// a row builder is a pure function of what was read.
func (s *WorkspaceProjectStatus) gather(ctx context.Context, m wsMember, roster *workspace.Roster, rosterErr error) wsMemberFacts {
	f := wsMemberFacts{}
	// The member's own published config: its credentials service, its model
	// accounts and its two project names. It is read as another installation's
	// published config is (sharedFetch), because that is what it is, and a
	// member whose config this machine cannot read is a row that says so.
	if m.Entry.FP == "" || m.Target.Project == "" {
		f.memberErr = "the member's own project and its Fugaro project name (`fp`) are not both in this machine's member table, so the member's own config could not be located"
	} else {
		cfg, _, err := sharedFetch(ctx, s.getenv, s.now(), m.Entry.FP, m.Target.Project)
		if err != nil {
			f.memberErr = "the member's own published config could not be read: " + oneLineCLI(err.Error())
		} else {
			f.member = cfg
		}
	}
	if s.cloud.reachable != nil {
		if err := s.cloud.reachable(ctx, m.Target); err != nil {
			f.reachErr = oneLineCLI(err.Error())
		}
	} else {
		f.reachErr = errNoWorkspaceSide("the member's project").Error()
	}
	if s.cloud.credentials != nil {
		var err error
		f.creds, err = s.cloud.credentials(ctx, m.Target, f.member)
		if err != nil {
			f.credsErr = oneLineCLI(err.Error())
		}
	} else {
		f.credsErr = errNoWorkspaceSide("the member's stored credentials").Error()
	}
	if s.cloud.broker != nil {
		var err error
		f.broker, err = s.cloud.broker(ctx, f.member)
		if err != nil {
			f.brokerErr = oneLineCLI(err.Error())
		}
	} else {
		f.brokerErr = errNoWorkspaceSide("the member's credentials service").Error()
	}
	if s.cloud.images != nil {
		var err error
		f.images, err = s.cloud.images(ctx, f.member)
		if err != nil {
			f.imagesErr = oneLineCLI(err.Error())
		}
	} else {
		f.imagesErr = errNoWorkspaceSide("the member's rebuild jobs").Error()
	}
	if s.cloud.caps != nil {
		var err error
		f.caps, err = s.cloud.caps(ctx, m.Name)
		if err != nil {
			f.capsErr = oneLineCLI(err.Error())
		}
	} else {
		f.capsErr = errNoWorkspaceSide("the member's caps").Error()
	}
	if auth, err := s.authority(ctx); err != nil {
		f.holdsErr = oneLineCLI(err.Error())
	} else {
		held, err := auth.Holds(ctx, m.Target)
		switch {
		case err != nil:
			f.holdsErr = oneLineCLI(err.Error())
		default:
			f.held = held
		}
	}
	// PendingApply is asked with the admin list the record does state. Its
	// other two lists are the member installation's own outputs, which no
	// method here reads (D2801: nothing of a member's installation is read
	// from another machine), and the row says so rather than reading the
	// member's apply as clean.
	if ops, err := s.memberOps(ctx); err != nil {
		f.pendingErr = oneLineCLI(err.Error())
	} else {
		want := workspace.ApplyWant{Admins: s.wantAdmins(m, roster)}
		var perr error
		f.pending, perr = ops.PendingApply(ctx, m.Target, want)
		if perr != nil {
			f.pendingErr = oneLineCLI(perr.Error())
		}
	}
	if w, err := s.stateWriter(ctx); err != nil {
		f.savedErr = oneLineCLI(err.Error())
	} else {
		at, found, err := w.LastSaved(ctx, workspace.Snapshot{Workspace: s.lc.Name, Member: m.Target})
		switch {
		case err != nil:
			f.savedErr = oneLineCLI(err.Error())
		default:
			f.saved, f.savedFound = at, found
		}
	}
	if d, err := s.driftFunc(ctx); err != nil {
		f.driftErr = oneLineCLI(err.Error())
	} else {
		dr, derr := d(ctx, m.Target)
		if derr != nil {
			f.driftErr = oneLineCLI(derr.Error())
		} else {
			f.drift = dr
		}
	}
	if s.check {
		f.probe, f.probeErr = s.runProbe(ctx, m)
	}
	if ops, err := s.memberOps(ctx); err != nil {
		f.bindingsErr = oneLineCLI(err.Error())
	} else {
		b, berr := ops.Bindings(ctx, m.Target)
		if berr != nil {
			f.bindingsErr = oneLineCLI(berr.Error())
		} else {
			f.bindings = b
		}
	}
	return f
}

// wantAdmins is the admin list the record states for one member: the workspace's
// own admins, who hold their grant on every member the mechanism covers (D2818),
// and every stored grant on this member that is an admin role (D2818's mark).
func (s *WorkspaceProjectStatus) wantAdmins(m wsMember, roster *workspace.Roster) []string {
	if roster == nil {
		return nil
	}
	out := slices.Clone(roster.Admins)
	for _, g := range roster.Grants {
		if g.Project == m.Name && g.Role == workspace.RoleAdmin && !slices.Contains(out, g.User) {
			out = append(out, g.User)
		}
	}
	slices.Sort(out)
	return out
}

// runProbe is the opt-in credential probe, once per member. A probe that
// refused is returned as the row's own warn: the command has already refused
// the session before any read, and a probe that answered nothing is not a
// failure of the member.
func (s *WorkspaceProjectStatus) runProbe(ctx context.Context, m wsMember) ([]workspace.CredentialResult, string) {
	if s.sides.Probe == nil {
		return nil, errNoWorkspaceSide("the credential probe").Error()
	}
	p, err := s.sides.Probe(ctx, s.lc)
	if err != nil {
		return nil, oneLineCLI(err.Error())
	}
	res, err := p.Probe(ctx, m.Target)
	if err != nil {
		return nil, oneLineCLI(err.Error())
	}
	return res, ""
}

// wsEsc is every string a member, its owner or a cloud API controls, put in a
// row: a project name, an address, a credential name and a key id are all
// outside this program, and a row is printed in somebody's terminal.
func wsEsc(s string) string { return pluginwire.Printable(s) }

// wsJoins is a list of escaped names for a sentence, or a word for the empty
// list. It is one place so that "nothing" and "nothing I could read" are not
// spelled two ways in two rows.
func wsJoins(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// wsRow builds one row with the escaping and the fix rule in one place: a row
// that is not passing always carries a fix, and a passing one never does.
func wsRow(id string, level wsStatusLevel, detail, fix string) wsProjectRow {
	if level == wsPass {
		fix = ""
	}
	return wsProjectRow{ID: id, Status: level, Detail: detail, Fix: fix}
}

// wsWorst is a member's worst row, which is the level `ls` prints beside it.
func wsWorst(rows []wsProjectRow) wsStatusLevel {
	worst := wsPass
	for _, r := range rows {
		if wsRank(r.Status) > wsRank(worst) {
			worst = r.Status
		}
	}
	return worst
}

// memberDoc builds one member's rows, in the order the acceptance fixes them.
func (s *WorkspaceProjectStatus) memberDoc(ctx context.Context, m wsMember, f wsMemberFacts) wsProjectMember {
	out := wsProjectMember{Project: m.Name}
	out.Rows = append(out.Rows,
		s.rowReachable(m, f),
		s.rowLinked(m, f),
		s.rowCredentialsService(m, f),
		s.rowCredentials(m, f),
	)
	if s.check {
		out.Rows = append(out.Rows, s.rowCredentialsProbe(m, f))
	}
	out.Rows = append(out.Rows,
		s.rowImages(m, f),
		s.rowCaps(m, f),
		s.rowPending(m, f),
	)
	if r, ok := s.rowGrantsLeft(m, f); ok {
		out.Rows = append(out.Rows, r)
	}
	out.Rows = append(out.Rows, s.rowStateRepo(m, f), s.rowDrift(m, f))
	out.Worst = wsWorst(out.Rows)
	out.Rows = nonNil(out.Rows)
	return out
}

// memberFixIn is the one sentence every row that could not reach a member's own
// installation ends with: the command on the member's own machine that reads it.
func memberFixIn(name string) string {
	return selfCommand() + " doctor --project " + wsEsc(name) + " (on that project's own machine)"
}

// wsCapFix is the one command the caps row names: the workspace's own cap, which
// is the only cap this side can state (the members' own budgets are their own
// machines' business). The family has no read subcommand, so naming one would
// name a command that does not exist.
func wsCapFix(workspace string) string {
	return selfCommand() + " workspace cap set --workspace " + wsEsc(workspace) + " --monthly <amount>"
}

// rowReachable is the first row: the member's own project answering a read at
// all. A member the table does not let this machine place is named here, because
// every row under it was then read of a project nobody named.
func (s *WorkspaceProjectStatus) rowReachable(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowReachable
	if m.Target.Project == "" {
		return wsRow(id, wsWarn,
			"the member's Google Cloud project is not named in this machine's member table: its entry declares no job service account and no firebase_project, and the member's project is read off one of them, so nothing was reached at all",
			selfCommand()+" workspace project add "+wsEsc(m.Name)+" --workspace "+wsEsc(s.lc.Name)+" --job-service-account ACCOUNT@PROJECT.iam.gserviceaccount.com")
	}
	if f.reachErr != "" {
		return wsRow(id, wsFail,
			fmt.Sprintf("the member's project %s did not answer a read: %s", wsEsc(m.Target.Project), f.reachErr),
			"check that "+wsEsc(m.Target.Project)+" exists and that your own account can read its Secret Manager, then run "+memberFixIn(m.Name))
	}
	return wsRow(id, wsPass,
		fmt.Sprintf("the member's project %s answered a read, and its Secret Manager listed its credentials", wsEsc(m.Target.Project)), "")
}

// rowLinked is D2819's row: whether the mechanism still holds the member. The
// workspace's stored grants mean nothing while it does not, and this is the row
// that says so first -- "not linked" -- with the command that links it again.
func (s *WorkspaceProjectStatus) rowLinked(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowLinked
	if f.holdsErr != "" {
		return wsRow(id, wsWarn,
			"whether the workspace still holds this member could not be read, so neither linked nor unlinked was measured: "+f.holdsErr,
			selfCommand()+" doctor --project "+wsEsc(s.lc.Name))
	}
	if !f.held {
		return wsRow(id, wsFail,
			fmt.Sprintf("not linked: the workspace does not hold %s by any mechanism now, so its admins are not trusted there and no grant of it is enforced; a member's own owner runs the command that gives the authority back, from their own credentials", wsEsc(m.Name)),
			selfCommand()+" workspace project link "+wsEsc(m.Name))
	}
	return wsRow(id, wsPass, fmt.Sprintf("the workspace holds %s by this workspace's own mechanism", wsEsc(m.Name)), "")
}

// rowCredentialsService is the member's credentials service: deployed and
// serving, read through the member's own published config with doctor's own
// brokering reads rather than a second implementation of them.
func (s *WorkspaceProjectStatus) rowCredentialsService(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowCredentialsSvc
	if f.memberErr != "" {
		return wsRow(id, wsWarn,
			"the member's own config could not be read, so whether it runs a credentials service is not measured here: "+f.memberErr,
			memberFixIn(m.Name))
	}
	if f.brokerErr != "" {
		return wsRow(id, wsWarn,
			"whether the member's credentials service answers was not measured here: "+f.brokerErr,
			memberFixIn(m.Name))
	}
	if !f.broker.Serving {
		return wsRow(id, wsWarn,
			"the member's credentials service is not serving: "+wsOr(f.broker.Detail, "its own config names no credentials service"),
			selfCommand()+" update on "+wsEsc(m.Name)+"'s own machine (its own update applies the service)")
	}
	return wsRow(id, wsPass, "the member's credentials service is deployed and serving:"+wsOr(f.broker.Detail, " no route was read"), "")
}

// rowCredentials is D2820's first half: what the member's project holds, with
// the marks the workspace's own credential state keeps for the credentials a
// run of it is served. The two halves are read separately and reported
// separately: a container nobody can see is a fail, and a fact this machine
// could not read at all is a warn that says so ("not measured"), never a pass.
func (s *WorkspaceProjectStatus) rowCredentials(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowCredentials
	if f.credsErr != "" {
		return wsRow(id, wsWarn,
			"the member's stored credentials were not read here, so whether they are stored and unmarked was not measured: "+f.credsErr,
			memberFixIn(m.Name))
	}
	if missing := f.creds.Missing; len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, n := range missing {
			names = append(names, wsEsc(n))
		}
		return wsRow(id, wsFail,
			fmt.Sprintf("the member's config names the Claude account(s) %s, and this member's project holds no container for them: a run of a repository allowed one of them has no token to mount", strings.Join(names, ", ")),
			selfCommand()+" accounts add "+wsEsc(missing[0])+" (on "+wsEsc(m.Name)+"'s own machine)")
	}
	if len(f.creds.Broken) > 0 {
		names := make([]string, 0, len(f.creds.Broken))
		for _, b := range f.creds.Broken {
			names = append(names, wsEsc(b.Name)+" (broken "+wsAge(s.now(), b.Since)+")")
		}
		return wsRow(id, wsWarn,
			fmt.Sprintf("the workspace's credential state holds %s as broken, and the selection skips a broken credential, so every call only it covers is refused", strings.Join(names, ", ")),
			selfCommand()+" creds rm NAME and store a working value with "+selfCommand()+" creds set NAME")
	}
	if !f.creds.ReadMarks {
		return wsRow(id, wsWarn,
			"the member's stored credentials were listed, and the marks the workspace's credential state holds for them were not read: "+f.creds.MarksWhy+"; whether one of them is marked broken is therefore not measured here",
			selfCommand()+" creds ls --json reads the marks this machine can see")
	}
	return wsRow(id, wsPass,
		fmt.Sprintf("%d model credential(s) of this member are stored and none is marked broken (the newest of them %s)", len(f.creds.Stored), wsAge(s.now(), f.creds.Newest)), "")
}

// rowCredentialsProbe is D2820's second half, present only under
// --check-credentials: what the providers themselves said. The value is never
// in the row -- the probe holds it in memory and throws it away -- and a call
// nothing answered is reported as unanswered rather than as a credential to
// replace.
func (s *WorkspaceProjectStatus) rowCredentialsProbe(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowCredentialsProbe
	if f.probeErr != "" {
		return wsRow(id, wsWarn,
			"the credential probe did not answer, so whether the member's model credentials work is not measured: "+f.probeErr,
			selfCommand()+" workspace project status --check-credentials "+wsEsc(m.Name)+" (in your own terminal)")
	}
	if len(f.probe) == 0 {
		return wsRow(id, wsWarn,
			"the probe found no model credential of the member to ask about: the member's project holds no model key and no project-owned Claude account, so nothing was asked",
			selfCommand()+" accounts add NAME (on "+wsEsc(m.Name)+"'s own machine)")
	}
	var refused, parts []string
	for _, r := range f.probe {
		word := "accepted"
		if !r.OK {
			word = "refused"
			refused = append(refused, wsEsc(r.Name))
		}
		parts = append(parts, wsEsc(r.Name)+" ("+string(r.Kind)+"): the provider "+word+" it"+wsOr(r.Status, ""))
	}
	if len(refused) > 0 {
		return wsRow(id, wsWarn,
			"the provider refused "+strings.Join(refused, ", ")+": "+strings.Join(parts, "; "),
			selfCommand()+" secrets set NAME --repo REPO replaces the value (the provider's own answer is not a Fugaro state)")
	}
	return wsRow(id, wsPass, "every credential the probe asked about was accepted: "+strings.Join(parts, "; "), "")
}

// rowImages is the rebuild Scheduler jobs' last success. The job is the member's
// own (its update creates it); a job that never ran and one whose last attempt
// failed are different rows, because one is a schedule that is broken and the
// other a project that has not caught up yet.
func (s *WorkspaceProjectStatus) rowImages(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowImages
	if f.imagesErr != "" {
		return wsRow(id, wsWarn,
			"the member's rebuild jobs were not read here, so whether its images are fresh is not measured: "+f.imagesErr,
			memberFixIn(m.Name))
	}
	if len(f.images) == 0 {
		return wsRow(id, wsWarn,
			"the member's project holds no rebuild Scheduler job, so nothing rebuilds its images: the job is created by the member's own update",
			selfCommand()+" update on "+wsEsc(m.Name)+"'s own machine")
	}
	worst := wsPass
	var parts []string
	for _, j := range f.images {
		state := "last succeeded " + wsAge(s.now(), j.LastSuccess)
		level := wsPass
		switch {
		case j.Failed:
			state, level = "its last attempt FAILED", wsFail
		case j.LastSuccess.IsZero():
			state, level = "it has never run", wsWarn
		case s.now().Sub(j.LastSuccess) > wsImageFreshFor:
			state, level = "it last succeeded "+wsAge(s.now(), j.LastSuccess)+", longer than "+wsImageFreshFor.String()+" ago", wsWarn
		}
		if wsRank(level) > wsRank(worst) {
			worst = level
		}
		parts = append(parts, wsEsc(j.Name)+": "+state)
	}
	fix := selfCommand() + " image rebuild (on " + wsEsc(m.Name) + "'s own machine) rebuilds the member's images"
	if worst == wsPass {
		return wsRow(id, wsPass, "the member's rebuild jobs are fresh: "+strings.Join(parts, "; "), "")
	}
	return wsRow(id, worst, "the member's rebuild jobs: "+strings.Join(parts, "; "), fix)
}

// rowCaps is the caps row: the workspace's own ceiling and what the workspace's
// credentials have paid for this member. A member over its cap has its calls
// refused, which is a fail and not a warning; a ceiling nobody stated is a warn,
// because an uncapped member is a member whose spend nobody decided.
func (s *WorkspaceProjectStatus) rowCaps(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowCaps
	if f.capsErr != "" {
		return wsRow(id, wsWarn,
			"the member's caps were not read here: "+f.capsErr,
			wsCapFix(s.lc.Name))
	}
	if !f.caps.Set {
		return wsRow(id, wsWarn,
			"the workspace states no cap, so nothing bounds what its own credentials pay for this member's calls",
			wsCapFix(s.lc.Name))
	}
	if !f.caps.SpentRead {
		return wsRow(id, wsWarn,
			fmt.Sprintf("the workspace's cap is %s a month, and what this member has spent against it was not measured: %s", wsUSD(f.caps.Monthly), f.caps.Why),
			wsCapFix(s.lc.Name))
	}
	line := fmt.Sprintf("the workspace's cap is %s a month and this member has spent %s of it", wsUSD(f.caps.Monthly), wsUSD(f.caps.Spent))
	if f.caps.Spent >= f.caps.Monthly {
		return wsRow(id, wsFail, "the member has spent its whole cap: "+line+", so every call of it is refused until the next window",
			wsCapFix(s.lc.Name))
	}
	return wsRow(id, wsPass, line, "")
}

// rowPending is D2818's row: what the record states that the member has not
// applied yet. The two grants that wait are the ones a member's own `fugaro
// update` turns into bindings, and "not measured" is carried through for the
// lists nothing can compare (D2815) rather than dropped.
func (s *WorkspaceProjectStatus) rowPending(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowPending
	if f.pendingErr != "" {
		return wsRow(id, wsWarn,
			"what the record states and the member has not applied was not measured here: "+f.pendingErr,
			memberFixIn(m.Name))
	}
	var waiting []string
	for _, g := range f.pending.Launchers {
		waiting = append(waiting, "launcher "+wsEsc(g))
	}
	for _, g := range f.pending.Operators {
		waiting = append(waiting, "operator "+wsEsc(g))
	}
	for _, g := range f.pending.Admins {
		waiting = append(waiting, "admin "+wsEsc(g))
	}
	unmeasured := ""
	if len(f.pending.NotMeasured) > 0 {
		names := make([]string, 0, len(f.pending.NotMeasured))
		for _, n := range f.pending.NotMeasured {
			names = append(names, wsEsc(n))
		}
		unmeasured = "; not measured here: " + strings.Join(names, ", ")
	}
	if len(waiting) > 0 {
		return wsRow(id, wsWarn,
			fmt.Sprintf("%d grant(s) of this member are waiting for its own update: %s%s", len(waiting), strings.Join(waiting, ", "), unmeasured),
			selfCommand()+" update on "+wsEsc(m.Name)+"'s own machine")
	}
	if unmeasured != "" {
		return wsRow(id, wsWarn,
			"nothing the record states is waiting for this member's update, and some of it could not be compared"+unmeasured,
			memberFixIn(m.Name))
	}
	return wsRow(id, wsPass, "every grant the record states for this member has reached it", "")
}

// rowGrantsLeft is D2822's state: the member's own owner unlinked, and the
// workspace's stored grants are still written in the members' rosters until the
// workspace project's owner removes the member from the record.
func (s *WorkspaceProjectStatus) rowGrantsLeft(m wsMember, f wsMemberFacts) (wsProjectRow, bool) {
	const id = rowGrantsLeft
	if !f.holdsErrOk() || f.held {
		return wsProjectRow{}, false
	}
	grants := s.rosterGrants(m.Name)
	if len(grants) == 0 {
		return wsProjectRow{}, false
	}
	names := make([]string, 0, len(grants))
	for _, g := range grants {
		names = append(names, wsEsc(g.User)+" ("+string(g.Role)+")")
	}
	return wsRow(id, wsWarn,
		fmt.Sprintf("unlinked, grants left: the workspace holds nothing on %s any more, and its record still stores %d grant(s) there -- %s -- which only this command removes", wsEsc(m.Name), len(grants), wsJoins(names)),
		selfCommand()+" workspace project rm "+wsEsc(m.Name)), true
}

// rowStateRepo is D2824's row: when this member's settings and spend summary
// were last saved. A member nothing ever saved is not an error -- it is a member
// whose state a re-apply would have to reconstruct by hand.
func (s *WorkspaceProjectStatus) rowStateRepo(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowStateRepo
	if f.savedErr != "" {
		return wsRow(id, wsWarn,
			"when this member's state was last saved was not read here: "+f.savedErr,
			memberFixIn(m.Name))
	}
	save := selfCommand() + " workspace project save " + wsEsc(m.Name)
	if !f.savedFound {
		return wsRow(id, wsWarn,
			"no state snapshot of this member has ever been saved, so a re-apply of it has nothing to start from",
			save)
	}
	if s.now().Sub(f.saved) > wsSavedFor {
		return wsRow(id, wsWarn,
			"this member's state was last saved "+wsAge(s.now(), f.saved)+" ago, longer than "+wsSavedFor.String(),
			save)
	}
	return wsRow(id, wsPass, "this member's state was saved "+wsAge(s.now(), f.saved)+" ago", "")
}

// rowDrift is D2813's fence: the member's live IAM against what the workspace
// node records, with a console change named rather than counted and "not
// measured" carried through. An empty report with an unmeasured list is NOT a
// member in sync, which is the one thing this row must never say.
func (s *WorkspaceProjectStatus) rowDrift(m wsMember, f wsMemberFacts) wsProjectRow {
	const id = rowDrift
	if f.driftErr != "" {
		return wsRow(id, wsWarn,
			"the member's live IAM was not compared against the record, so drift was not measured: "+f.driftErr,
			selfCommand()+" workspace project reconcile "+wsEsc(m.Name))
	}
	var findings []string
	for _, b := range f.drift.Extra {
		findings = append(findings, "extra "+wsEsc(b.Principal)+" as "+wsEsc(b.Role)+" on "+wsEsc(b.Project))
	}
	for _, b := range f.drift.Missing {
		findings = append(findings, "missing "+wsEsc(b.Principal)+" as "+wsEsc(b.Role)+" on "+wsEsc(b.Project))
	}
	for _, c := range f.drift.ConsoleChanges {
		findings = append(findings, "changed in the console: "+wsEsc(c.Principal)+" as "+wsEsc(c.Role)+" on "+wsEsc(c.Project)+" ("+wsEsc(c.Entry)+")")
	}
	unmeasured := ""
	if len(f.drift.Unmeasured) > 0 {
		names := make([]string, 0, len(f.drift.Unmeasured))
		for _, u := range f.drift.Unmeasured {
			names = append(names, wsEsc(u))
		}
		unmeasured = "; not measured: " + strings.Join(names, ", ")
	}
	fix := selfCommand() + " workspace project reconcile " + wsEsc(m.Name)
	if len(findings) > 0 {
		return wsRow(id, wsWarn,
			fmt.Sprintf("%d difference(s) between this member's live IAM and the record: %s%s", len(findings), strings.Join(findings, ", "), unmeasured),
			fix)
	}
	if unmeasured != "" {
		return wsRow(id, wsWarn, "no difference was found and part of the member was not compared"+unmeasured, fix)
	}
	return wsRow(id, wsPass, "the member's live IAM matches the record", "")
}

// holdsErrOk reports whether the mechanism's read answered, which is what the
// unlinked-grants row needs: an unread mechanism is not an unlink.
func (f wsMemberFacts) holdsErrOk() bool { return f.holdsErr == "" }

// rosterGrants are the grants the workspace's own record stores for one member
// (D2818's mark). It is the record and never the member's own roster: the
// member's roster is read at the member's own machine, and this is what the
// workspace believes it wrote there.
func (s *WorkspaceProjectStatus) rosterGrants(project string) []workspace.StoredGrant {
	if s.roster == nil {
		return nil
	}
	var out []workspace.StoredGrant
	for _, g := range s.roster.Grants {
		if g.Project == project {
			out = append(out, g)
		}
	}
	return out
}

// wsKeyOutlivesRemove is the one sentence both account rows carry, in the fix of
// a row that warns and in the detail of one that passes, so that the removal
// commands' own output and this screen cannot come to say two things about what
// a key is. A key is not a binding: `workspace user rm --admin` removes the binding and
// leaves every user-managed key on the account alive, and the only control that
// stops one being created is an organization policy Fugaro cannot set for the
// owner.
const wsKeyOutlivesRemove = "a user-managed key on this account outlives 'fugaro workspace user rm <address> --workspace <name> --admin', which removes the binding and not the key, and it survives until it is deleted on the account or the account itself is disabled; 'constraints/iam.disableServiceAccountKeyCreation' is the organization policy that stops new ones, and it is the owner's to set (Fugaro cannot set it for you)"

// workspaceRows is the workspace's own half of the screen: the admins' live
// bindings (D2823), a single admin (R1), and the workspace IAM account's
// user-managed keys and every principal that can act as it. The account is
// discovered from the members' own bindings, which is the one read that places
// it (D2806, F17), and a workspace held by the folder mechanism has no account
// of its own to report.
func (s *WorkspaceProjectStatus) workspaceRows(ctx context.Context, roster *workspace.Roster, rosterErr error, facts []wsMemberFacts) []wsProjectRow {
	rows := make([]wsProjectRow, 0, 4)
	if rosterErr != nil {
		why := oneLineCLI(rosterErr.Error())
		rows = append(rows,
			wsRow(rowAdminBinding, wsWarn,
				"the workspace's own record could not be read, so whether its admins' bindings match it is not measured: "+why,
				selfCommand()+" workspace show "+wsEsc(s.lc.Name)),
			wsRow(rowSingleAdmin, wsWarn,
				"how many admins the workspace has was not measured: "+why,
				selfCommand()+" workspace show "+wsEsc(s.lc.Name)))
	} else {
		rows = append(rows, s.rowAdminBinding(ctx, roster), s.rowSingleAdmin(roster))
	}
	rows = append(rows, s.accountRows(ctx, roster, facts)...)
	return rows
}

// wsAdminAddress is one of the mechanism's live bindings as the record spells
// the same person. The two sides spell a principal differently: the record's
// `Admins` holds the bare address, because that is what a roster is a list of,
// and IAM holds the member string (`user:` and the address, access.Member's
// spelling). Every comparison between the two -- D2823's two questions and the
// actAs one -- is decided between the spellings, never inside one of them: read
// as written, an untampered workspace would report every one of its admins as
// tampering with, and `status` would exit nonzero on a workspace nothing is
// wrong with.
//
// Only a person is translated. A workspace admin is a person by the record's own
// validation, and a role held through a group, a domain or a service account is
// that principal and not a person of the record: its spelling is returned as it
// is, so it cannot come to equal an admin's address and is reported as the
// binding holder the record does not name.
func wsAdminAddress(member string) string {
	return strings.TrimPrefix(member, "user:")
}

// rowAdminBinding is D2823: an `Admins` entry with no live binding, or a live
// binding whose holder is not in `Admins`. Either direction is a fail, because
// the accounts that can rewrite `Admins` cannot set a binding: the record is a
// claim about IAM and the live binding is the permission.
func (s *WorkspaceProjectStatus) rowAdminBinding(ctx context.Context, roster *workspace.Roster) wsProjectRow {
	const id = rowAdminBinding
	auth, err := s.authority(ctx)
	if err != nil {
		return wsRow(id, wsWarn,
			"the live bindings of the workspace's admins were not read, so whether the record matches them is not measured: "+oneLineCLI(err.Error()),
			selfCommand()+" doctor --project "+wsEsc(s.lc.Name))
	}
	live, err := auth.AdminBindings(ctx)
	if err != nil {
		return wsRow(id, wsWarn,
			"the live bindings of the workspace's admins were not read, so whether the record matches them is not measured: "+oneLineCLI(err.Error()),
			selfCommand()+" doctor --project "+wsEsc(s.lc.Name))
	}
	var claimed, unbound []string
	for _, a := range roster.Admins {
		if !slices.ContainsFunc(live, func(m string) bool { return wsAdminAddress(m) == a }) {
			claimed = append(claimed, wsEsc(a))
		}
	}
	for _, a := range live {
		if !slices.Contains(roster.Admins, wsAdminAddress(a)) {
			unbound = append(unbound, wsEsc(a))
		}
	}
	if len(claimed) == 0 && len(unbound) == 0 {
		return wsRow(id, wsPass, fmt.Sprintf("the record's %d admin(s) and the live bindings name the same people", len(roster.Admins)), "")
	}
	var parts []string
	if len(claimed) > 0 {
		parts = append(parts, "the record lists "+strings.Join(claimed, ", ")+" as admin(s) and the live bindings do not name them")
	}
	if len(unbound) > 0 {
		parts = append(parts, "the live bindings name "+strings.Join(unbound, ", ")+" and the record does not list them")
	}
	return wsRow(id, wsFail, strings.Join(parts, "; "),
		selfCommand()+" workspace user add <address> --admin gives the missing binding, and "+selfCommand()+" workspace user rm <address> --workspace "+wsEsc(s.lc.Name)+" --admin takes the extra one away")
}

// rowSingleAdmin is R1: a workspace with one admin has one person who can
// administer it. It is a warn and not a fail: the workspace works, and the thing
// that is one accident away is its administration.
func (s *WorkspaceProjectStatus) rowSingleAdmin(roster *workspace.Roster) wsProjectRow {
	const id = rowSingleAdmin
	if len(roster.Admins) >= 2 {
		return wsRow(id, wsPass, fmt.Sprintf("the workspace has %d admins", len(roster.Admins)), "")
	}
	if len(roster.Admins) == 1 {
		return wsRow(id, wsWarn,
			fmt.Sprintf("a single workspace admin (%s): if they lose access to their account, nobody can administer this workspace, and the mechanism's binding goes with them", wsEsc(roster.Admins[0])),
			selfCommand()+" workspace user add <address> --admin")
	}
	return wsRow(id, wsWarn,
		"the workspace has no admin at all: nobody can approve a member's link or remove one from the record",
		selfCommand()+" workspace user add <address> --admin")
}

// accountRows is the workspace IAM account's own two rows: the user-managed
// keys it holds, each with its id and age, and every principal that can act as
// it. Both are warns when they find something, and both carry
// wsKeyOutlivesRemove, so an owner never reads one of them as "the binding is
// still there" or the other as "the key is gone with the admin".
func (s *WorkspaceProjectStatus) accountRows(ctx context.Context, roster *workspace.Roster, facts []wsMemberFacts) []wsProjectRow {
	account, why := s.workspaceAccount(roster, facts)
	if why != "" {
		return []wsProjectRow{
			wsRow(rowAccountKeys, wsWarn, "the workspace IAM account was not placed, so its user-managed keys were not read: "+why, selfCommand()+" workspace project status"),
			wsRow(rowAccountActAs, wsWarn, "the workspace IAM account was not placed, so who can act as it was not read: "+why, selfCommand()+" workspace project status"),
		}
	}
	if account == "" {
		return []wsProjectRow{
			wsRow(rowAccountKeys, wsPass, "this workspace holds its members by the folder and has no IAM account of its own, so it has no user-managed keys: "+wsKeyOutlivesRemove, ""),
			wsRow(rowAccountActAs, wsPass, "this workspace holds its members by the folder, so no account of its own can be acted as: "+wsKeyOutlivesRemove, ""),
		}
	}
	if s.cloud.account == nil {
		why := errNoWorkspaceSide("the workspace IAM account's keys and actAs holders").Error()
		return []wsProjectRow{
			wsRow(rowAccountKeys, wsWarn, "the workspace IAM account's user-managed keys were not read: "+why, selfCommand()+" doctor --project "+wsEsc(s.lc.Name)),
			wsRow(rowAccountActAs, wsWarn, "who can act as the workspace IAM account was not read: "+why, selfCommand()+" doctor --project "+wsEsc(s.lc.Name)),
		}
	}
	acc, err := s.cloud.account(ctx, account)
	if err != nil {
		why := oneLineCLI(err.Error())
		return []wsProjectRow{
			wsRow(rowAccountKeys, wsWarn, "the workspace IAM account's user-managed keys were not read: "+why, selfCommand()+" doctor --project "+wsEsc(s.lc.Name)),
			wsRow(rowAccountActAs, wsWarn, "who can act as the workspace IAM account was not read: "+why, selfCommand()+" doctor --project "+wsEsc(s.lc.Name)),
		}
	}
	rows := make([]wsProjectRow, 0, 2)
	if len(acc.Keys) == 0 {
		rows = append(rows, wsRow(rowAccountKeys, wsPass,
			"the workspace IAM account "+wsEsc(account)+" holds no user-managed key: "+wsKeyOutlivesRemove, ""))
	} else {
		parts := make([]string, 0, len(acc.Keys))
		for _, k := range acc.Keys {
			parts = append(parts, wsEsc(k.ID)+" ("+wsAge(s.now(), k.Created)+" old)")
		}
		rows = append(rows, wsRow(rowAccountKeys, wsWarn,
			fmt.Sprintf("the workspace IAM account %s holds %d user-managed key(s): %s. %s", wsEsc(account), len(acc.Keys), strings.Join(parts, ", "), wsKeyOutlivesRemove),
			"gcloud iam service-accounts keys delete KEY_ID --iam-account "+wsEsc(account)+", or disable the account; "+wsKeyOutlivesRemove))
	}
	// The principals that can act as the account are compared against the
	// record's admins: a workspace admin holding actAs is the mechanism working
	// (they may impersonate the account the workspace acts through), and a
	// principal the record does not name is somebody who holds the workspace's
	// own authority without appearing in it. The record's `Admins` holds bare
	// addresses and the account's policy holds member strings, so the two are
	// compared through wsAdminAddress -- read as written, every legitimate admin
	// would be reported as a stranger holding the workspace's own authority.
	var strangers []string
	for _, h := range acc.ActAs {
		if !slices.Contains(roster.Admins, wsAdminAddress(h)) {
			strangers = append(strangers, wsEsc(h))
		}
	}
	if len(strangers) == 0 {
		rows = append(rows, wsRow(rowAccountActAs, wsPass,
			fmt.Sprintf("%d principal(s) can act as the workspace IAM account %s, and every one of them is in the record's admin list: %s. %s", len(acc.ActAs), wsEsc(account), wsJoins(escaped(acc.ActAs)), wsKeyOutlivesRemove), ""))
	} else {
		rows = append(rows, wsRow(rowAccountActAs, wsWarn,
			fmt.Sprintf("the workspace IAM account %s can be acted as by %s, who the record does not list as an admin: whoever can act as this account holds the workspace's own authority over every member. %s", wsEsc(account), strings.Join(strangers, ", "), wsKeyOutlivesRemove),
			"gcloud iam service-accounts remove-iam-policy-binding "+wsEsc(account)+" --member=MEMBER --role=ROLES/IAM.SERVICEACCOUNTUSER; "+wsKeyOutlivesRemove))
	}
	return rows
}

// escaped is a list of escaped names, for a row that lists what it found.
func escaped(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, wsEsc(n))
	}
	return out
}

// workspaceAccount is the workspace IAM account's email, and why it could not be
// placed. Under the service-account mechanism it is named by the members' own
// live bindings (PrincipalWorkspaceIAM, D2806/F17) -- the one read that says
// which account the workspace binds with -- and a workspace whose members name
// none is a workspace whose account this machine cannot see. Under the folder
// mechanism there is no account at all, which the empty name says.
func (s *WorkspaceProjectStatus) workspaceAccount(roster *workspace.Roster, facts []wsMemberFacts) (account, why string) {
	if roster == nil {
		return "", "the workspace's own record could not be read, so which mechanism it holds its members by is unknown"
	}
	if roster.Mechanism != workspace.MechanismSA {
		return "", ""
	}
	for _, f := range facts {
		if f.bindingsErr != "" {
			continue
		}
		for _, b := range f.bindings {
			if b.Kind == workspace.PrincipalWorkspaceIAM {
				return strings.TrimPrefix(b.Principal, "serviceAccount:"), ""
			}
		}
	}
	return "", "no member's live IAM names the workspace IAM account, and that binding is the only place this machine reads its name (the members are read with the workspace's own credentials, so a member that cannot be read cannot name it either)"
}

// Print writes the document, or the screen, and then decides the exit status. A
// failing row is what makes the command exit nonzero, exactly as a failing check
// makes doctor exit nonzero: a warn never does, and neither does a row this
// build could not read.
func (s *WorkspaceProjectStatus) Print(w io.Writer, doc wsProjectsDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return err
		}
	} else if s.summary {
		printWorkspaceProjectLs(w, doc)
	} else {
		printWorkspaceProjectStatus(w, doc)
	}
	if wsProjectsFail(doc) {
		return &ExitError{Code: ExitUserError, Err: errors.New("a workspace row failed; see above")}
	}
	return nil
}

// wsProjectsFail is whether any row of the document fails, the members' rows and
// the workspace's own alike.
func wsProjectsFail(doc wsProjectsDoc) bool {
	for _, m := range doc.Members {
		for _, r := range m.Rows {
			if r.Status == wsFail {
				return true
			}
		}
	}
	for _, r := range doc.Rows {
		if r.Status == wsFail {
			return true
		}
	}
	return false
}

// wsRowLine is one row as a line: the level, the id, the whole sentence and the
// fix, clipped to doctor's own line length. It is doctor's own shape on purpose:
// an operator who has read `fugaro doctor` reads this the same way, and the
// level word is what the acceptance asks `status` to print beside each row.
func wsRowLine(r wsProjectRow) string {
	line := string(r.Status) + "  " + r.ID + ": " + r.Detail
	if r.Fix != "" {
		line += " (fix: " + r.Fix + ")"
	}
	return line
}

// wsMemberHead is one member's own line: its name and its worst row, which is
// what `ls` prints and what `status` heads each member's block with.
func wsMemberHead(m wsProjectMember) string {
	worst := wsProjectRow{ID: "", Detail: "every row passed"}
	for _, r := range m.Rows {
		if wsRank(r.Status) == wsRank(m.Worst) {
			worst = r
			break
		}
	}
	head := wsEsc(m.Project) + ": " + string(m.Worst)
	if m.Worst == wsPass {
		return head + " (" + fmt.Sprintf("%d rows checked", len(m.Rows)) + ")"
	}
	return head + " -- " + wsRowLine(worst)
}

// printWorkspaceProjectLs is the listing: one line per member with its worst
// row. It is the same read as `status`, summarised.
func printWorkspaceProjectLs(w io.Writer, doc wsProjectsDoc) {
	if len(doc.Members) == 0 {
		fmt.Fprintf(w, "workspace %s declares no member\n", wsEsc(doc.Workspace))
		return
	}
	for _, m := range doc.Members {
		fmt.Fprintln(w, doctorClip(wsMemberHead(m), doctorLineLimit))
	}
}

// printWorkspaceProjectStatus is the full screen: each member's rows and then
// the workspace's own. Every row is printed, passing ones included, because a
// passing row's sentence is what says what was found.
func printWorkspaceProjectStatus(w io.Writer, doc wsProjectsDoc) {
	if len(doc.Members) == 0 {
		fmt.Fprintf(w, "workspace %s declares no member\n", wsEsc(doc.Workspace))
	}
	for _, m := range doc.Members {
		fmt.Fprintln(w, doctorClip(wsMemberHead(m), doctorLineLimit))
		for _, r := range m.Rows {
			fmt.Fprintln(w, "  "+doctorClip(wsRowLine(r), doctorLineLimit-2))
		}
	}
	fmt.Fprintf(w, "workspace %s:\n", wsEsc(doc.Workspace))
	for _, r := range doc.Rows {
		fmt.Fprintln(w, "  "+doctorClip(wsRowLine(r), doctorLineLimit-2))
	}
}

// wsOr is a row's optional tail: the detail when there is one, and the default
// sentence when the read answered with nothing to add.
func wsOr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return " " + s
}

// wsAge is a duration in the words a row uses: seconds up to a minute, then
// minutes, hours and days. A zero time is "never", which is a different fact
// from "just now" and must not be printed as one.
func wsAge(now, at time.Time) string {
	if at.IsZero() {
		return "never"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// wsUSD is a micro-dollar amount as the dollars a person reads. It is display
// only -- every figure in this program is integer micro-dollars from the parse
// on, and nothing here does arithmetic in a float (CLAUDE.md's money rule).
func wsUSD(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	return fmt.Sprintf("%s$%d.%02d", sign, micros/1_000_000, (micros%1_000_000)/10_000)
}

// The member-facing reads, as a struct of function values (the shape
// workspace_probe.go uses for its own seams). A nil field is a read this build
// does not make, which every row turns into a warn that says so -- there is no
// interface to satisfy and no method that writes: nothing below this line can
// write to a member, on any path.
type workspaceMemberReads struct {
	// reachable reports whether the member's own project answered a read at all.
	reachable func(ctx context.Context, m workspace.MemberTarget) error
	// credentials reads what the member's project stores and the marks the
	// workspace's own credential state keeps for it.
	credentials func(ctx context.Context, m workspace.MemberTarget, member *localcfg.Config) (memberCredentials, error)
	// broker reads the member's credentials service: deployed, and serving.
	broker func(ctx context.Context, member *localcfg.Config) (brokerState, error)
	// images reads the member's rebuild Scheduler jobs and their last success.
	images func(ctx context.Context, member *localcfg.Config) ([]imageJob, error)
	// caps reads the workspace's cap and what the workspace's own credentials
	// have paid for this member: the owner's file, and the workspace's own
	// meter node for the member the name belongs to.
	caps func(ctx context.Context, name string) (memberCaps, error)
	// account reads one workspace IAM account's user-managed keys and every
	// principal that can act as it.
	account func(ctx context.Context, account string) (accountAccess, error)
}

// memberCredential is one model credential of a member's project.
type memberCredential struct {
	// Name is the credential's logical name, as its own config and its own
	// `fugaro secrets ls` spell it.
	Name string
	// Stored is whether its container holds an enabled version.
	Stored bool
	// Created is when that version was created; zero when nothing is stored.
	Created time.Time
}

// memberMark is one mark the credential state holds for a credential a run of
// the member is served (D139, C1.21): a credential the provider refused is
// broken until an operator clears it.
type memberMark struct {
	Name  string
	Since time.Time
}

// memberCredentials is what one read of a member's stored credentials answers.
// The two halves are deliberately separate: what the member's project holds is
// read from its own Secret Manager, and the marks are the workspace's own
// credential state. ReadMarks false is a read that did not happen and never
// "nothing is broken" -- the row that reads this warns when it is false, which
// is the fail-closed direction the plan's global constraints require.
type memberCredentials struct {
	// Stored are the member's own model credentials.
	Stored []memberCredential
	// Newest is the newest stored version's creation time, for the age a row
	// prints. Zero for a member that stores none.
	Newest time.Time
	// Missing are the names the member's own config expects that its project
	// holds no container for.
	Missing []string
	// Broken are the marks the credential state holds as broken.
	Broken []memberMark
	// ReadMarks is whether the marks were read at all, and MarksWhy what
	// stopped it when they were not.
	ReadMarks bool
	MarksWhy  string
}

// brokerState is the member's credentials service as one read of it answers.
type brokerState struct {
	// Serving is whether it is deployed and serving at all.
	Serving bool
	// Detail is what was read: the revision, the route, the refusal. It is a
	// sentence fragment and never a credential of any kind.
	Detail string
}

// imageJob is one rebuild Scheduler job of a member's project.
type imageJob struct {
	Name string
	// LastSuccess is when the job last ran and Google reported it OK. Zero for
	// a job that has never run. Scheduler reports the last ATTEMPT with its
	// status rather than a success of its own, so a job whose last attempt
	// succeeded is a job whose last success is that attempt, and the row says
	// which it read.
	LastSuccess time.Time
	// Failed is true when Google reported the job's last attempt as a failure.
	Failed bool
}

// memberCaps is the workspace's own ceiling and what the workspace's credentials
// have paid for one member. SpentRead false is a spend this build did not read,
// which the row warns about rather than reading as zero (a zero spend and an
// unread one are the same number and different facts).
type memberCaps struct {
	Set     bool
	Monthly int64
	Spent   int64
	// SpentRead is whether Spent was read at all, and Why what stopped it when
	// it was not.
	SpentRead bool
	Why       string
}

// accountKey is one user-managed key of the workspace IAM account.
type accountKey struct {
	ID      string
	Created time.Time
}

// accountAccess is one workspace IAM account as the two account rows read it.
type accountAccess struct {
	Keys  []accountKey
	ActAs []string
}

// workspaceMemberCloud is the concrete member-facing read: what a workspace
// admin's own credentials can see of a member. Every read is made with them and
// nothing else (the mechanism's grant is what makes them possible), and nothing
// here writes anything anywhere: the type has no write to call.
type workspaceMemberCloud struct {
	lc  *localcfg.Config
	db  *rtdb.Client
	now func() time.Time

	// lists caches one member project's Secret Manager listing for the run: the
	// reachable row and the credentials row read the same listing.
	lists map[string][]gcp.SecretInfo

	// The seams below are the calls themselves, so a test drives every read of
	// the concrete side without a network.
	secrets func(ctx context.Context, o gcp.Options) ([]gcp.SecretInfo, error)
	marks   func(ctx context.Context) (map[string]creds.Mark, error)
	serving func(ctx context.Context, member *localcfg.Config) (brokerState, error)
	jobs    func(ctx context.Context, member *localcfg.Config) ([]imageJob, error)
	keys    func(ctx context.Context, account string) (accountAccess, error)
}

// NewWorkspaceMemberCloud is the read both commands are built with. lc is the
// owner's own configuration -- its endpoints, its region and its cap -- and db
// the workspace's own database, which is where the credential state's marks
// live.
func NewWorkspaceMemberCloud(lc *localcfg.Config, db *rtdb.Client) workspaceMemberReads {
	c := &workspaceMemberCloud{lc: lc, db: db, now: time.Now, lists: map[string][]gcp.SecretInfo{}}
	c.secrets = func(ctx context.Context, o gcp.Options) ([]gcp.SecretInfo, error) {
		sm, err := gcp.NewSecrets(ctx, o)
		if err != nil {
			return nil, err
		}
		return sm.List(ctx, nil)
	}
	c.marks = func(ctx context.Context) (map[string]creds.Mark, error) {
		if db == nil {
			return nil, errors.New("this run holds no database of the workspace's own project, so the credential state was not read")
		}
		return (&creds.RTDBStore{DB: db}).Marks(ctx)
	}
	c.serving = workspaceMemberServing
	c.jobs = workspaceMemberJobs
	c.keys = func(ctx context.Context, account string) (accountAccess, error) {
		return workspaceAccessKeys(ctx, lc, account)
	}
	return workspaceMemberReads{
		reachable:   c.reachable,
		credentials: c.credentials,
		broker:      c.broker,
		images:      c.images,
		caps:        c.caps,
		account:     c.account,
	}
}

// reachable is the member's own project answering a read: its Secret Manager
// listing, which is the cheapest read that says the member exists and that the
// workspace's grant is really live there.
func (c *workspaceMemberCloud) reachable(ctx context.Context, m workspace.MemberTarget) error {
	if m.Project == "" {
		return errors.New("the member's Google Cloud project is not named in this machine's member table")
	}
	_, err := c.list(ctx, m.Project)
	return err
}

// list is one member project's Secret Manager listing, cached per project for
// the run: three rows read it and none of them asks twice.
func (c *workspaceMemberCloud) list(ctx context.Context, project string) ([]gcp.SecretInfo, error) {
	if c.lists == nil {
		c.lists = map[string][]gcp.SecretInfo{}
	}
	if list, ok := c.lists[project]; ok {
		return list, nil
	}
	list, err := c.secrets(ctx, probeOptions(c.lc, project))
	if err != nil {
		return nil, err
	}
	c.lists[project] = list
	return list, nil
}

// credentials reads the member's own model credentials and the marks the
// workspace's credential state holds. The families are WU22's own
// (probeKindOf): the project's model key and the project-owned Claude accounts,
// which are the credentials a run of the member is served and the only ones
// whose names settle what they are. A name the member's own config expects and
// its project does not hold is reported as missing, with the command that stores
// it.
func (c *workspaceMemberCloud) credentials(ctx context.Context, m workspace.MemberTarget, member *localcfg.Config) (memberCredentials, error) {
	var out memberCredentials
	list, err := c.list(ctx, m.Project)
	if err != nil {
		return out, err
	}
	// have is keyed the way doctor's own claude-accounts-stored check keys it --
	// by the container's logical name, which is the `fugaro_secret` label -- so
	// that "is this account stored" is the same question with the same answer
	// here as there.
	have := map[string]bool{}
	for _, s := range list {
		name := probeLogicalName(s)
		stored := s.Latest != ""
		if label := s.Labels[gcp.LabelSecret]; label != "" {
			have[label] = stored
		}
		if _, ok := probeKindOf(name); !ok {
			continue
		}
		out.Stored = append(out.Stored, memberCredential{Name: name, Stored: stored, Created: s.Created})
		if stored && s.Created.After(out.Newest) {
			out.Newest = s.Created
		}
	}
	if member != nil {
		for _, repo := range slices.Sorted(maps.Keys(member.Repos)) {
			for _, name := range config.AllowedClaudeAccounts(member.ClaudeAccounts, repo) {
				if name == config.DefaultClaudeAccount {
					continue
				}
				// The container this name's value lives in, by the member's own
				// declaration of it (config.CredentialValueSecret, which is what
				// doctor's own check asks for): a named account the member
				// declares as a KEY credential has no token container and never
				// will, so asking for the token's name would report a stored
				// value as missing forever.
				if have[config.CredentialValueSecret(member.Credentials, name)] {
					continue
				}
				if !slices.Contains(out.Missing, name) {
					out.Missing = append(out.Missing, name)
				}
			}
		}
	}
	marks, err := c.marks(ctx)
	if err != nil {
		out.MarksWhy = oneLineCLI(err.Error())
		return out, nil
	}
	out.ReadMarks = true
	for _, name := range slices.Sorted(maps.Keys(marks)) {
		if marks[name].Broken() {
			out.Broken = append(out.Broken, memberMark{Name: name, Since: marks[name].Since})
		}
	}
	return out, nil
}

// broker is the member's credentials service, read through doctor's own
// brokering checks against the member's published config: one implementation of
// "is it deployed and can it serve", not a second one here.
func (c *workspaceMemberCloud) broker(ctx context.Context, member *localcfg.Config) (brokerState, error) {
	if member == nil {
		return brokerState{}, errors.New("the member's own config was not read")
	}
	return c.serving(ctx, member)
}

// images is the member's rebuild jobs.
func (c *workspaceMemberCloud) images(ctx context.Context, member *localcfg.Config) ([]imageJob, error) {
	if member == nil {
		return nil, errors.New("the member's own config was not read")
	}
	return c.jobs(ctx, member)
}

// caps is the workspace's own cap (the owner's file) and what the workspace's
// credentials have paid for one member, which is the workspace's own METER: the
// month's node the workspace's credentials service reserves and settles in
// (D1301, internal/budget/workspace.go), in the workspace's own database. It is
// the figure the workspace's cap governs and not the member's own ledger, which
// is a different quantity with its own cap (spec §5: the member's gateway keeps
// enforcing the member's own budget, and neither figure answers the other's
// question). What is counted is what the calls have settled for this member plus
// the room the calls still in flight hold for it, which is the meter's own
// reading of a member's month.
//
// A month with NO NODE is not read as a spend of zero. It is one absent node for
// two different facts -- a workspace whose credentials have paid for nothing
// this month, and a workspace whose service does not meter yet -- and today it is
// the second: nothing writes the meter, because the credentials service's
// paid-call path takes no reservation (internal/credsvc/workspace_cap.go says so
// itself). SpentRead stays false and Why says exactly that, so the row warns
// with the truth instead of passing a figure nobody produced.
func (c *workspaceMemberCloud) caps(ctx context.Context, name string) (memberCaps, error) {
	var out memberCaps
	if c.lc.WorkspaceCap != nil {
		monthly, err := pricing.FromUSD(c.lc.WorkspaceCap.Monthly)
		if err != nil {
			return out, fmt.Errorf("workspace_cap.monthly: %v", err)
		}
		out.Set = true
		out.Monthly = int64(monthly)
	}
	if c.db == nil {
		out.Why = "this machine holds no database of the workspace's own project, which is where its spend meter is kept, so what the workspace's credentials have paid for here was not read"
		return out, nil
	}
	now := c.now()
	month := budget.MonthKey(now)
	spend, found, err := budget.ReadWorkspaceSpend(ctx, c.db, c.lc.Name, month)
	if err != nil {
		return out, fmt.Errorf("the workspace's own spend meter: %v", err)
	}
	if !found {
		out.Why = fmt.Sprintf("the workspace's own meter holds no node for %s, and this build's credentials service takes no reservation on the paid-call path yet, so nothing has measured what its credentials pay for here: a month with no node is not a spend of zero", month)
		return out, nil
	}
	live := spend.Live(now)
	out.Spent = int64(live.Members[name] + live.ReservedBy(name))
	out.SpentRead = true
	return out, nil
}

// account is one workspace IAM account's keys and actAs holders, through the IAM
// API with the owner's own credentials: the account lives in the workspace's own
// project, so this is a read of this machine's own project and not of a member's.
func (c *workspaceMemberCloud) account(ctx context.Context, account string) (accountAccess, error) {
	if account == "" {
		return accountAccess{}, errors.New("the workspace IAM account is not placed")
	}
	return c.keys(ctx, account)
}

// workspaceMemberServing is the member's credentials service as doctor reads its
// own: the deployment's checks and the bundle's, mapped onto one answer. It is
// doctor's two functions and not a second implementation of them, so a member's
// service and this machine's are read by the same code; a check neither could
// read makes the whole answer an error, because "not read" is not "serving".
func workspaceMemberServing(ctx context.Context, member *localcfg.Config) (brokerState, error) {
	if member.CredentialsService == nil {
		return brokerState{}, nil
	}
	checks := append(doctorCredentialsServiceDeployed(ctx, member), doctorCredentialsServiceServing(ctx, member, rowCredentialsSvc)...)
	var problems, unread []string
	for _, c := range checks {
		switch {
		case c.unread:
			unread = append(unread, oneLineCLI(c.Problem))
		case !c.OK && c.Severity != "info":
			problems = append(problems, oneLineCLI(c.Problem))
		}
	}
	switch {
	case len(problems) > 0:
		return brokerState{Detail: strings.Join(problems, "; ")}, nil
	case len(unread) > 0:
		return brokerState{}, errors.New(strings.Join(unread, "; "))
	}
	return brokerState{Serving: true, Detail: " " + wsEsc(member.CredentialsService.URL)}, nil
}

// workspaceMemberJobs is the member's rebuild Scheduler job and its last
// attempt. Scheduler reports the last attempt with its status and no success of
// its own, so a job whose last attempt Google reports OK is a job whose images
// were rebuilt then, and one that has never run at all is a job of its own kind.
func workspaceMemberJobs(ctx context.Context, member *localcfg.Config) ([]imageJob, error) {
	region := member.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(member.Region)
		if err != nil {
			return nil, err
		}
		region = r
	}
	svc, err := scheduler.NewService(ctx, doctorAPIOpts(member, member.Endpoints.CloudScheduler)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Scheduler: %w", err)
	}
	name := infra.SchedulerJobName(member.GCPProject, region, infra.PreloadSchedulerJob)
	j, err := svc.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading the rebuild job %s: %w", infra.PreloadSchedulerJob, err)
	}
	job := imageJob{Name: infra.PreloadSchedulerJob}
	if at, err := time.Parse(time.RFC3339, j.LastAttemptTime); err == nil {
		job.LastSuccess = at
	}
	if j.Status != nil && j.Status.Code != 0 {
		job.Failed = true
	}
	return []imageJob{job}, nil
}

// workspaceAccessKeys is one workspace IAM account's user-managed keys and every
// principal that can act as it: the keys from the account's own key list, and
// the actAs holders from the account's own policy, which is where Google keeps
// them (the roles that carry iam.serviceAccounts.actAs, actAsRoles). A custom
// role that carries the permission is not resolved here -- doctor resolves
// custom roles, and the row's fix sends an owner there for exactly that reason.
func workspaceAccessKeys(ctx context.Context, lc *localcfg.Config, account string) (accountAccess, error) {
	name := "projects/" + lc.GCPProject + "/serviceAccounts/" + account
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" {
		return accountAccess{}, errors.New("this configuration names no IAM endpoint, so the account could not be read")
	}
	svc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
	if err != nil {
		return accountAccess{}, fmt.Errorf("connecting to the IAM API: %w", err)
	}
	keys, err := svc.Projects.ServiceAccounts.Keys.List(name).KeyTypes("USER_MANAGED").Context(ctx).Do()
	if err != nil {
		return accountAccess{}, fmt.Errorf("listing %s's user-managed keys: %s", wsEsc(account), oneLineCLI(err.Error()))
	}
	out := accountAccess{}
	for _, k := range keys.Keys {
		at, _ := time.Parse(time.RFC3339, k.ValidAfterTime)
		out.Keys = append(out.Keys, accountKey{ID: pathTail(k.Name), Created: at})
	}
	pol, err := svc.Projects.ServiceAccounts.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return accountAccess{}, fmt.Errorf("reading %s's own policy: %s", wsEsc(account), oneLineCLI(err.Error()))
	}
	for _, b := range pol.Bindings {
		if !slices.Contains(actAsRoles, b.Role) {
			continue
		}
		for _, m := range b.Members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}
			out.ActAs = append(out.ActAs, m)
		}
	}
	slices.Sort(out.ActAs)
	out.ActAs = slices.Compact(out.ActAs)
	return out, nil
}
