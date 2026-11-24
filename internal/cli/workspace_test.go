package cli

import (
	"bytes"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The workspace writes' fixture: project aurora in a config home of its own,
// with no coding-agent marker set, so a write that is meant to land is not
// refused by the session the test binary happens to run in. It is the models
// writes' fixture (modelsWriteHome), for the same reason: the two families
// share the one write path and the one fence.
func workspaceWriteHome(t *testing.T) (home, path string) {
	t.Helper()
	return modelsWriteHomeWith(t, "")
}

// workspaceWriteHomeWith is workspaceWriteHome with extra keys appended to
// aurora's own config, for the tests that start from a project that already
// names a workspace.
func workspaceWriteHomeWith(t *testing.T, extra string) (home, path string) {
	t.Helper()
	return modelsWriteHomeWith(t, extra)
}

// workspaceOwnerHome is the fixture with the workspace's own project config
// beside aurora's: `project add` and `cap set` write the config of the project
// the workspace is named after, so a test of either needs one.
func workspaceOwnerHome(t *testing.T) (home, aurora, owner string) {
	t.Helper()
	home, aurora = workspaceWriteHome(t)
	return home, aurora, writeProject(t, "workspace-alpha", "ws-gcp")
}

// workspaceAppend adds keys to a config the fixture wrote, which is how a test
// starts from a file the commands did not write.
func workspaceAppend(t *testing.T, path, extra string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(extra)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// workspaceOwnerTable is the workspace's own config declaring aurora as a
// member, with values a pin must never be taken from: they are another Firebase
// project, another fp and another exchange than the ones the member's own flags
// name.
const workspaceOwnerTable = `workspace_members:
  aurora:
    job_service_accounts:
      - account: marker-job@aurora-gcp.iam.gserviceaccount.com
        repos:
          - marker-org/aurora
    database_region: europe-west1
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/v1
`

// workspaceSameJobAccount compares two job service accounts of a member, which
// is the slice-equality the tests read an entry back with: the account with the
// repositories it is a job of, in order.
func workspaceSameJobAccount(a, b localcfg.WorkspaceJobAccount) bool {
	return a.Account == b.Account && slices.Equal(a.Repos, b.Repos)
}

// workspaceProjectAddArgs is the whole of one `workspace project add`: the flags
// `workspace member add` carried, unchanged (D2802), plus the workspace the
// member is declared in (D2821). It is the invocation a test drives to reach a
// refusal that is made before any seam is opened; the tests that need the write
// to land run the command over WU5's own fakes instead
// (workspaceProjectAddRun), because the add reaches the workspace's roster.
func workspaceProjectAddArgs(extra ...string) []string {
	return append([]string{"workspace", "project", "add", "aurora", "--workspace", "workspace-alpha",
		"--job-service-account", "marker-job@aurora-gcp.iam.gserviceaccount.com",
		"--job-repos", "marker-job@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v1"}, extra...)
}

// workspaceProjectAddRun runs one `workspace project add` over WU5's own fakes
// (workspace_project_test.go) on the fixture's configs, and answers the cloud
// the fakes are read back from and what the command printed and returned. The
// two things the add cannot be run without are here: a workspace whose roster
// names this machine's caller as one of its admins, and the fakes for the roster,
// the trail and the member's own database that keep the whole run hermetic.
//
// WU5's fixture passes --no-apply, because WU5's own tests are about the local
// declaration and stop there. The tests here go on to the chain's own ending --
// D1563's sentence and the non-zero exit that says the workspace's own project
// is still owed (workspaceAssertSavedNotLive) -- so the fixture's flag is turned
// off first. A caller that wants the save-and-return ending passes --no-apply
// itself and wins, because pflag keeps the last occurrence of a flag.
func workspaceProjectAddRun(t *testing.T, extra ...string) (*wu5Cloud, string, error) {
	t.Helper()
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{{Address: "owner@example.com", Kind: workspace.KindHuman}},
		Admins: []string{"owner@example.com"},
	}))
	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs(append([]string{"--no-apply=false"}, extra...)...)...)
	return cloud, out, err
}

// workspaceProjectAddWholeRun is workspaceProjectAddRun for a test that names the
// whole declaration itself. WU5's fixture already declares a job service account,
// and a declaration is one statement per account: passing the fixture's flags
// beside the test's own would name that account twice and list its repository
// twice, which the table refuses. So this one passes none of them, and only the
// member, the workspace and the caller's own flags.
func workspaceProjectAddWholeRun(t *testing.T, flags ...string) (*wu5Cloud, string, error) {
	t.Helper()
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{{Address: "owner@example.com", Kind: workspace.KindHuman}},
		Admins: []string{"owner@example.com"},
	}))
	args := append([]string{"aurora", "--workspace", "workspace-alpha"}, flags...)
	out, err := wu5Run(t, cloud.seams(t), append(args, "--no-apply=false")...)
	return cloud, out, err
}

// workspaceAddArgsNoYes is the whole of one `workspace add` without its --yes:
// the same invocation a script drives and the one the tests that are about the
// write asking nothing run.
func workspaceAddArgsNoYes(extra ...string) []string {
	return append([]string{"workspace", "add", "workspace-alpha",
		"--firebase-project", "project-ws",
		"--issuer", "https://securetoken.google.com/project-ws",
		"--assertion-key", "marker-assertion-key",
		"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com"}, extra...)
}

// workspaceAddArgs is the whole of one `workspace add`.
func workspaceAddArgs(extra ...string) []string {
	return append(workspaceAddArgsNoYes(), append([]string{}, extra...)...)
}

// TestWorkspaceAddAsksNothing and TestWorkspaceRmAsksNothing are the two
// directions of D1540 on this family: the membership is written and taken back
// with neither --yes nor a terminal, and what stands where the y/N was is the
// sentence naming the key that goes in or comes out. A run that still asked
// would sit at the question until the package's own deadline failed it.
func TestWorkspaceAddAsksNothing(t *testing.T) {
	_, path := workspaceWriteHome(t)

	out, _, err := execute(t, workspaceAddArgsNoYes()...)
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if after := readFileString(t, path); !strings.Contains(after, "workspace: workspace-alpha\n") {
		t.Errorf("the membership was not written:\n%s", after)
	}
}

// TestWorkspaceCapSetAsksNothing is the rest of this family's D1540 half on the
// write the fixture reaches: the cap is set with neither --yes nor a terminal,
// and the sentence naming the edit stands where the y/N was. The member table's
// two directions are `workspace project add` and `project rm`, whose own tests
// carry this (WU5's workspace_project_test.go, WU6's workspace_project_rm_test.go).
//
// AP6 puts the change's own follow-up behind each of them (spec rule 1), and
// the follow-up of the owner's half is the workspace's own project plus the
// published record (apply's ResourceWorkspace row). The workspace's own project
// is an access grant, and a grant is confirmed by the name typed at its own
// terminal — never by a flag and never by a chain, which asks nothing at all
// (spec §6, D1540) — so on this fixture, whose workspace has never been applied,
// the change is saved and the two steps stay owed: D1563's outcome, said in
// D1563's sentence, and the door that can ask is `fugaro update`
// (applyKeptLine's own branch). TestWorkspaceWritesChain is the other half of
// this: a machine whose workspace's own root is already converged runs both
// steps and ends with nothing owed.
// TestWorkspaceCapSetAsksNothing writes the cap with no --yes and no terminal.
func TestWorkspaceCapSetAsksNothing(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)

	out, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50")
	assertNoPrompt(t, out)
	workspaceAssertSavedNotLive(t, "workspace cap set", out, err)
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	if lc.WorkspaceCap == nil || lc.WorkspaceCap.Monthly != 50 {
		t.Errorf("the cap is %+v, want 50 a month", lc.WorkspaceCap)
	}
}

// workspaceAssertSavedNotLive holds a workspace write's owner-half chain to
// D1563: the change is written, the two steps the owner's half owes are the ones
// this machine still has to run, and the sentence names the door that can
// confirm the grant. The command exits non-zero — a run that said "live" here
// would be the lie D1563 exists to prevent — and it asks nothing on the way
// (D1540).
func workspaceAssertSavedNotLive(t *testing.T, what, out string, err error) {
	t.Helper()
	owed := []string{initflow.WorkspaceProject, initflow.Workspace}
	if err == nil {
		t.Fatalf("%s reported the change live with the workspace's own project unapplied:\n%s", what, out)
	}
	if want := "saved, not live yet: " + strings.Join(owed, ", ") + " still owed"; !strings.Contains(out, want) {
		t.Errorf("%s does not say the change is saved and not live yet (%q):\n%s", what, want, out)
	}
	if !strings.Contains(out, "Run `fugaro update` in your own terminal window") {
		t.Errorf("%s does not name the door that can confirm the workspace's own project:\n%s", what, out)
	}
	if got := workspaceOwedFor(t, "workspace-alpha", "ws-gcp"); !slices.Equal(got, owed) {
		t.Errorf("%s left %v owed, want %v", what, got, owed)
	}
}

// workspaceOwedFor is what this machine owes for one project, read the way the
// command reads it: bound to the project name and the GCP project the change was
// made in. A record bound to another pair is an error rather than an empty
// answer (apply.LoadPending's own rule), so a mis-bound entry fails a test
// instead of reading as "nothing owed".
func workspaceOwedFor(t *testing.T, project, gcp string) []string {
	t.Helper()
	p, err := apply.LoadPending(os.Getenv, project, gcp)
	if err != nil {
		t.Fatal(err)
	}
	return pendingSteps(p)
}

// TestWorkspaceRmAsksNothing is the removal half: the key and the pin it
// carries come out, and nothing is asked about them.
func TestWorkspaceRmAsksNothing(t *testing.T) {
	_, path := workspaceWriteHome(t)
	if _, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add: %v", err)
	}

	out, _, err := execute(t, "workspace", "rm", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace rm: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if after := readFileString(t, path); strings.Contains(after, "workspace: workspace-alpha") {
		t.Errorf("the membership is still in the config after the removal:\n%s", after)
	}
}

// TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte is the red step: the
// command did not exist. It is the test the task exists for -- a workspace
// membership must be one key added to a file that is otherwise byte-identical
// (comments, ordering, quoting), the same promise the `models` writes make and
// the same one `yamledit` exists to keep.
func TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, workspaceAddArgs()...)
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "workspace: workspace-alpha\n") {
		t.Errorf("the membership key was not written:\n%s", after)
	}
	// Every line of the file as it was is still there, in order: the edit adds
	// the two keys and rewrites nothing else.
	rest := after
	for _, line := range strings.Split(strings.TrimSuffix(before, "\n"), "\n") {
		i := strings.Index(rest, line+"\n")
		if i < 0 {
			t.Fatalf("the edit rewrote the line %q:\n--- before\n%s--- after\n%s", line, before, after)
		}
		rest = rest[i+len(line)+1:]
	}
	// What is left is exactly the keys the command added.
	added := strings.TrimSuffix(strings.TrimPrefix(rest, "\n"), "\n")
	for _, want := range []string{"workspace: workspace-alpha", "workspace_pin:"} {
		if !strings.Contains(added, want) {
			t.Errorf("the edit added %q, which does not carry %q:\n%s", added, want, after)
		}
	}
	if strings.Count(after, "workspace:") != 1 {
		t.Errorf("the membership key was written more than once:\n%s", after)
	}
	workspaceAssertNamesUpdate(t, "workspace add", out, "aurora", "aurora-gcp")
}

// TestWorkspaceRmRemovesTheKeyAndNothingElse: the other direction of the red
// step. A membership added and removed leaves the file byte-identical, comments
// and quoting included -- which is what `workspace rm` removing the pin before
// the key, and yamledit dropping the empty parents, are for.
func TestWorkspaceRmRemovesTheKeyAndNothingElse(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)
	if _, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	out, _, err := execute(t, "workspace", "rm", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace rm: %v\n%s", err, out)
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the removal did not restore the file:\n--- before\n%s--- after\n%s", before, after)
	}
	workspaceAssertNamesUpdate(t, "workspace rm", out, "aurora", "aurora-gcp")
}

// workspaceAssertNamesUpdate holds the MEMBER's half to the one command that
// carries it: `fugaro update`, this project's own, which publishes the
// membership, the grant and the address inside the policy-side record the runs
// read at bootstrap (R3, R4). The member's half owes no apply step — apply's
// `workspace` and `workspace_pin.` rows say why — and a write that named no
// command would read as one that is live already.
func workspaceAssertNamesUpdate(t *testing.T, what, out, project, gcp string) {
	t.Helper()
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("%s does not name `fugaro update`, which is what carries the member's half to the runs:\n%s", what, out)
	}
	if got := workspaceOwedFor(t, project, gcp); len(got) != 0 {
		t.Errorf("%s left %v owed, and the member's own half declares no step", what, got)
	}
}

// workspaceAssertNoSecondCommand holds a write whose change is live the moment
// the file is written to the shape AP6 gives it: no command is named as the
// next thing to run, and nothing is left owed. `workspace project rm` is that
// once the change takes the last member out of the table: the config then
// declares no workspace of its own, both stages skip, and a skip that did
// nothing is this stage's answer that there is nothing here to converge
// (workspaceProjectSkip).
func workspaceAssertNoSecondCommand(t *testing.T, what, out, project, gcp string) {
	t.Helper()
	for _, forbidden := range []string{"next:", "fugaro update", "fugaro apply"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%s sends the person to run something else (%q):\n%s", what, forbidden, out)
		}
	}
	if got := workspaceOwedFor(t, project, gcp); len(got) != 0 {
		t.Errorf("%s left %v owed, and the member's own half declares no step", what, got)
	}
}

// TestWorkspaceRmRefusesAWorkspaceTheProjectDoesNotName: a removal is of the
// membership the file states, and a project that names another workspace (or
// none) is refused rather than edited.
func TestWorkspaceRmRefusesAWorkspaceTheProjectDoesNotName(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)
	if _, _, err := execute(t, "workspace", "rm", "workspace-alpha"); err == nil {
		t.Fatal("rm removed a membership the project does not have")
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
	if _, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	_, _, err := execute(t, "workspace", "rm", "workspace-beta")
	if err == nil || !strings.Contains(err.Error(), "names workspace workspace-alpha, not workspace-beta") {
		t.Errorf("removing another workspace's name: %v", err)
	}
}

// TestWorkspaceAddWritesThePinTheMemberWasGiven: spec §2 -- the Firebase
// project, the issuer, the assertion key and the service account are the
// MEMBER's own values, and no code path reads them from the workspace's record.
// The fixture makes the record say something else about the same project, so a
// refactor that "helpfully" filled the pin from the table would write those
// values and this test would read them back.
func TestWorkspaceAddWritesThePinTheMemberWasGiven(t *testing.T) {
	_, path, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)

	out, _, err := execute(t, workspaceAddArgs()...)
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	want := localcfg.WorkspacePin{
		FirebaseProject: "project-ws",
		Issuer:          "https://securetoken.google.com/project-ws",
		AssertionKey:    "marker-assertion-key",
		ServiceAccount:  "marker-workspace@project-ws.iam.gserviceaccount.com",
	}
	if lc.Workspace != "workspace-alpha" {
		t.Errorf("workspace = %q, want workspace-alpha", lc.Workspace)
	}
	if lc.WorkspacePin != want {
		t.Errorf("the pin is %+v, want the values the flags gave: %+v", lc.WorkspacePin, want)
	}
	// The workspace's own config is another machine's half: this command does
	// not touch it.
	if got := readFileString(t, owner); !strings.Contains(got, "firebase_project: aurora-fb") {
		t.Errorf("workspace add rewrote the workspace's own config:\n%s", got)
	}
}

// TestWorkspaceAddRefusesAnIssuerTheProjectDoesNotMint: the signature is
// verified against Google's one global set of Firebase signing keys, so the
// aud/iss pair is the whole of what tells one project's tokens from another's.
// A mistyped half is refused here, and again at the load, rather than admitted
// by one and refused by the other for the installation's life.
func TestWorkspaceAddRefusesAnIssuerTheProjectDoesNotMint(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)
	for _, issuer := range []string{
		"https://securetoken.google.com/other-project",
		"securetoken.google.com/project-ws",
		"https://accounts.google.com",
	} {
		out, _, err := execute(t, "workspace", "add", "workspace-alpha",
			"--firebase-project", "project-ws",
			"--issuer", issuer,
			"--assertion-key", "marker-assertion-key",
			"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com")
		if err == nil {
			t.Fatalf("--issuer %s was accepted:\n%s", issuer, out)
		}
		if !strings.Contains(err.Error(), "https://securetoken.google.com/project-ws") {
			t.Errorf("--issuer %s: the refusal does not name the issuer it must be: %v", issuer, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("a refused add wrote the file:\n%s", after)
	}
}

// TestWorkspaceAddRefusesAPinThatIsNotWhole: the four values are one statement,
// and a membership pinned by three of them is a member whose admission depends
// on which one is absent.
func TestWorkspaceAddRefusesAPinThatIsNotWhole(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)
	_, _, err := execute(t, "workspace", "add", "workspace-alpha",
		"--firebase-project", "project-ws",
		"--issuer", "https://securetoken.google.com/project-ws",
		"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com")
	if err == nil || !strings.Contains(err.Error(), "--assertion-key") {
		t.Fatalf("a pin without an assertion key: %v", err)
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
}

// TestWorkspaceAddRefusesTheWorkspacesOwnProject: a workspace's own project is
// the workspace and not a member of it, and its config is where the member
// table lives.
func TestWorkspaceAddRefusesTheWorkspacesOwnProject(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	t.Setenv("FUGARO_CONFIG", owner)
	_, _, err := execute(t, workspaceAddArgs()...)
	if err == nil || !strings.Contains(err.Error(), "IS workspace workspace-alpha") {
		t.Fatalf("a workspace joining itself: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
}

// TestProjectAddWritesTheOwnersHalfInTheWorkspacesOwnConfig: project add is the
// owner's side (spec §5). It writes the entry into the config of the project
// the workspace is named after, and the member's own config is another
// machine's file, which it does not touch.
func TestProjectAddWritesTheOwnersHalfInTheWorkspacesOwnConfig(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	auroraBefore, ownerBefore := readFileString(t, aurora), readFileString(t, owner)

	// The edit lands and this returns the chain's own answer, which in this
	// fixture is D1563's sentence and a non-zero exit: the workspace's own
	// project has no Firebase backend here to publish its record in, and its
	// grant is confirmed only by a name typed at a terminal
	// (workspaceAssertSavedNotLive holds both below). What this test reads the
	// file for is the write, which happened either way.
	_, out, chainErr := workspaceProjectAddRun(t, "--repo", "owner/repo-a", "--repo", "owner/repo-b")
	if got := readFileString(t, aurora); got != auroraBefore {
		t.Errorf("the add wrote the member's own config:\n%s", got)
	}
	after := readFileString(t, owner)
	rest := after
	for _, line := range strings.Split(strings.TrimSuffix(ownerBefore, "\n"), "\n") {
		i := strings.Index(rest, line+"\n")
		if i < 0 {
			t.Fatalf("the edit rewrote the line %q:\n--- before\n%s--- after\n%s", line, ownerBefore, after)
		}
		rest = rest[i+len(line)+1:]
	}
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("%v\n%s", err, after)
	}
	if lc.WorkspaceMembers == nil {
		t.Fatalf("no member table was written:\n%s", after)
	}
	got := (*lc.WorkspaceMembers)["aurora"]
	want := localcfg.WorkspaceMember{
		Repos: []string{"owner/repo-a", "owner/repo-b"},
		JobAccounts: []localcfg.WorkspaceJobAccount{{
			Account: "marker-job@aurora-gcp.iam.gserviceaccount.com",
			Repos:   []string{"marker-org/aurora"},
		}},
		FirebaseProject: "aurora-fb",
		FP:              "aurora",
		ExchangeURL:     "https://exchange.example.com/v1",
	}
	if got.FirebaseProject != want.FirebaseProject || got.FP != want.FP || got.ExchangeURL != want.ExchangeURL {
		t.Errorf("the entry is %+v, want the values the flags gave: %+v", got, want)
	}
	if !slices.EqualFunc(got.JobAccounts, want.JobAccounts, workspaceSameJobAccount) {
		t.Errorf("job service accounts = %+v, want %+v", got.JobAccounts, want.JobAccounts)
	}
	if strings.Join(got.Repos, ",") != strings.Join(want.Repos, ",") {
		t.Errorf("repos = %v, want %v", got.Repos, want.Repos)
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
}

// TestProjectAddReplacesTheEntryItAlreadyDeclares: a member the table already
// declares is replaced whole, and not added to. AddListItem appends and leaves
// what it finds alone, so a declaration that merged would keep every account
// and every repository the earlier one named — and re-declaring a member is how
// a retired or a compromised job service account leaves the table, so the
// merged entry would leave that identity admitted as a caller of the
// workspace's credentials while the confirmation named only the new one.
func TestProjectAddReplacesTheEntryItAlreadyDeclares(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, `workspace_members:
  aurora:
    job_service_accounts:
      - account: marker-retired@aurora-gcp.iam.gserviceaccount.com
        repos:
          - marker-org/aurora
      - account: marker-job@aurora-gcp.iam.gserviceaccount.com
        repos:
          - owner/repo-old
    database_region: europe-west1
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/v1
    repos:
      - owner/repo-old
`)
	ours := []string{
		"--job-service-account", "marker-new@aurora-gcp.iam.gserviceaccount.com",
		"--job-repos", "marker-new@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v2",
		"--repo", "owner/repo-new"}
	// The declaration is this test's own whole, so the fixture's flags stay out
	// of it: the entry it writes is exactly what the assertions below read back.
	//
	// The replaced entry is the write, and it lands either way; what this
	// returns is the chain's answer about the two steps that make it live
	// (workspaceAssertSavedNotLive at the end of this test).
	_, out, chainErr := workspaceProjectAddWholeRun(t, ours...)
	after := readFileString(t, owner)
	// Nothing the replaced entry carried survives it: not the retired account,
	// not the account that was still live, not the repository, not the exchange
	// URL. The region it recorded does stand, and the assertion below holds it:
	// LP9 reads it from the member's own database, this declaration derives none
	// (the fixture's member config writes no budget.rtdb_url), and a job account
	// added on a machine that cannot see where the member's database is must not
	// move where the workspace's service reads it (D3084).
	for _, gone := range []string{"marker-retired@", "marker-job@", "owner/repo-old", "exchange.example.com/v1"} {
		if strings.Contains(after, gone) {
			t.Errorf("the replaced entry still carries %q:\n%s", gone, after)
		}
	}
	// And the entry is exactly the declaration, not a superset of it.
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("%v\n%s", err, after)
	}
	got := (*lc.WorkspaceMembers)["aurora"]
	wantAccounts := []localcfg.WorkspaceJobAccount{{
		Account: "marker-new@aurora-gcp.iam.gserviceaccount.com",
		Repos:   []string{"marker-org/aurora"},
	}}
	if !slices.EqualFunc(got.JobAccounts, wantAccounts, workspaceSameJobAccount) {
		t.Errorf("job service accounts = %+v, want %+v", got.JobAccounts, wantAccounts)
	}
	if !slices.Equal(got.Repos, []string{"owner/repo-new"}) {
		t.Errorf("repos = %v, want the one the declaration named", got.Repos)
	}
	if got.ExchangeURL != "https://exchange.example.com/v2" {
		t.Errorf("exchange URL = %q, want the one the declaration named", got.ExchangeURL)
	}
	if got.DatabaseRegion != "europe-west1" {
		t.Errorf("database region = %q, want europe-west1, the region the entry already recorded", got.DatabaseRegion)
	}
	// The confirmation says which of the two it is doing, because the sentence
	// the owner reads is what tells them the old account is going: a merge that
	// printed only the new account is what made the old one invisible.
	if !strings.Contains(out, "this replaces the entry workspace workspace-alpha declares for aurora") ||
		!strings.Contains(out, "is gone from the table") {
		t.Errorf("the confirmation does not read as a replacement:\n%s", out)
	}
	// The same declaration again is the same file: a replace writes the entry
	// it is given and nothing else, so a re-run has nothing to do.
	before := readFileString(t, owner)
	if _, out, err := workspaceProjectAddWholeRun(t, ours...); err != nil {
		t.Fatalf("workspace project add (again): %v\n%s", err, out)
	} else if !strings.Contains(out, "no change") {
		t.Errorf("a repeated declaration is not a no-op:\n%s", out)
	}
	if readFileString(t, owner) != before {
		t.Errorf("a repeated declaration rewrote the file:\n%s", readFileString(t, owner))
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
}

// TestProjectAddRefusesToDeclareADatabaseURL: the member's database is derived
// from the Firebase project the verified `iss` names (finding 6), so it is not
// a value the owner may set -- an owner that could would pair one member's
// accounts with another's database. The flag is refused by name rather than by
// pflag's bare "unknown flag".
func TestProjectAddRefusesToDeclareADatabaseURL(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	_, _, err := execute(t, workspaceProjectAddArgs("--database-url", "https://aurora-fb.firebaseio.com")...)
	if err == nil {
		t.Fatal("--database-url was accepted")
	}
	if !strings.Contains(err.Error(), "derived from the Firebase project its verified iss names") {
		t.Errorf("the refusal does not say where the database URL comes from: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
}

// TestProjectAddWritesNoRunsBucket: O1's answer dropped the bucket grant, so the
// table carries no bucket and the flag is refused by name. The second half is
// the written entry itself: nothing in it is bucket-shaped, so a later field
// cannot be added back without this test failing first.
func TestProjectAddWritesNoRunsBucket(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	_, _, err := execute(t, workspaceProjectAddArgs("--runs-bucket", "fugaro-runs-aurora-gcp")...)
	if err == nil {
		t.Fatal("--runs-bucket was accepted")
	}
	if !strings.Contains(err.Error(), "the workspace holds no read of a member's runs") {
		t.Errorf("the refusal does not say why the bucket is not the owner's: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
	// The entry lands (the assertions below read it back). The chain ends with
	// the owner's two steps owed in this fixture, which is D1563's sentence in
	// out and a non-zero exit beside it — WU5's own tests hold that answer, and
	// a refusal of the write is what this fails on.
	_, out, err := workspaceProjectAddRun(t)
	if err != nil && !strings.Contains(out, "saved, not live yet") {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}
	// Only the entry, not the whole file: the config's own runs_bucket is the
	// installation's, and it is none of this table's business.
	after := readFileString(t, owner)
	i := strings.Index(after, "workspace_members:")
	if i < 0 {
		t.Fatalf("no member table was written:\n%s", after)
	}
	for _, banned := range []string{"bucket", "database", "db_url"} {
		if strings.Contains(strings.ToLower(after[i:]), banned) {
			t.Errorf("the member entry carries %q:\n%s", banned, after[i:])
		}
	}
}

// TestWorkspaceCapSetRefusesAnAmountTheFileCannotState: the block's presence is
// the claim that there is a ceiling, so a cap of zero is refused rather than
// read as a cap that forbids every call -- leaving the block out is how a
// workspace states none.
func TestWorkspaceCapSetRefusesAnAmountTheFileCannotState(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	for _, tc := range []struct{ amount, want string }{
		{"0", "less than one micro-dollar"},
		{"-1", "must be a number from 0 to 100000"},
		{"200000", "must be a number from 0 to 100000"},
	} {
		_, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", tc.amount)
		if err == nil {
			t.Fatalf("--monthly %s was accepted", tc.amount)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--monthly %s: %v, want a refusal naming %q", tc.amount, err, tc.want)
		}
	}
	if _, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha"); err == nil {
		t.Error("a cap with no amount was accepted")
	}
	if _, _, err := execute(t, "workspace", "cap", "set", "--monthly", "50"); err == nil ||
		!strings.Contains(err.Error(), "never spelled globally") {
		t.Errorf("a cap with no workspace: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused cap set wrote the file:\n%s", after)
	}
}

// TestWorkspaceWritesNothingUnderACodingAgentsMarker: the models rule, --yes
// included (SD9). Every write of this family is a local config edit, so every
// one of them prints the diff and the command for the person to run and writes
// nothing at all. The assertion key is a key: the command it prints names it
// and does not spell it.
func TestWorkspaceWritesNothingUnderACodingAgentsMarker(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	// The state every write below would change, written by hand so the marker
	// is the only thing refusing them. Every value differs from the one the
	// command would write, so every edit has something to do: an edit that
	// changed nothing would return before the fence and prove nothing.
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: other-project
  issuer: https://securetoken.google.com/other-project
  assertion_key: marker-other-key
  service_account: marker-other@other-project.iam.gserviceaccount.com
`)
	workspaceAppend(t, owner, `workspace_members:
  aurora:
    job_service_accounts:
      - account: marker-other@aurora-gcp.iam.gserviceaccount.com
        repos:
          - marker-org/aurora
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/other
workspace_cap:
  monthly: 40
workspace_shared:
  marker-model-key:
    kind: model
    projects:
      - aurora
`)
	auroraBefore, ownerBefore := readFileString(t, aurora), readFileString(t, owner)

	t.Setenv(agentMarkers[0], "1")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"add", workspaceAddArgs()},
		{"rm", []string{"workspace", "rm", "workspace-alpha"}},
		{"cap set", []string{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50"}},
		// The owner's shared list is the fourth thing this family writes, and
		// it is the one entry below the fixture already carries: unshare takes
		// it out and share puts a different credential in, so each has a diff.
		{"share", workspaceShareArgsNoYes()},
		{"unshare", []string{"workspace", "unshare", "marker-model-key", "--workspace", "workspace-alpha"}},
	} {
		out, _, err := execute(t, tc.args...)
		if err == nil {
			t.Errorf("%s wrote under a coding agent's marker:\n%s", tc.name, out)
			continue
		}
		if !strings.Contains(err.Error(), "coding agent's session") {
			t.Errorf("%s: the refusal is not the fence's: %v", tc.name, err)
		}
		if !strings.Contains(err.Error(), "run this yourself at your own terminal") {
			t.Errorf("%s: the refusal does not print the command to run: %v", tc.name, err)
		}
		// The diff is printed to stdout and the refusal to the error, which is
		// what a person reads before they decide to run the command.
		if !strings.Contains(out, "- ") && !strings.Contains(out, "+ ") {
			t.Errorf("%s: no diff was printed:\n%s", tc.name, out)
		}
		if strings.Contains(err.Error(), "marker-assertion-key") {
			t.Errorf("%s: the refusal spells the assertion key: %v", tc.name, err)
		}
	}
	if got := readFileString(t, aurora); got != auroraBefore {
		t.Errorf("a refused write changed the member's config:\n%s", got)
	}
	if got := readFileString(t, owner); got != ownerBefore {
		t.Errorf("a refused write changed the workspace's config:\n%s", got)
	}
}

// TestWorkspaceLsListsEveryMembershipOffline: the read opens no bucket, calls
// no API and needs no credential -- it answers from this machine's own configs,
// which is what lets it run in a coding agent's session. Two rows: one this
// machine declares, and one it only names.
func TestWorkspaceLsListsEveryMembershipOffline(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)
	workspaceAppend(t, owner, workspaceOwnerTable)
	// A second project naming a workspace this machine holds no config for.
	elsewhere := writeProject(t, "borealis", "borealis-gcp")
	workspaceAppend(t, elsewhere, `workspace: workspace-beta
workspace_pin:
  firebase_project: project-beta
  issuer: https://securetoken.google.com/project-beta
  assertion_key: marker-assertion-key
  service_account: marker-beta@project-beta.iam.gserviceaccount.com
`)

	out, errOut, err := execute(t, "workspace", "ls")
	if err != nil {
		t.Fatalf("workspace ls: %v\n%s", err, out)
	}
	if errOut != "" {
		t.Errorf("workspace ls warned about a fixture that reads:\n%s", errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows:\n%s", out)
	}
	if !strings.HasPrefix(lines[0], "WORKSPACE") || !strings.Contains(lines[0], "MEMBERS") || !strings.Contains(lines[0], "MEMBERSHIPS") {
		t.Errorf("the header is not the listing's:\n%s", lines[0])
	}
	// Name order, and each row says which half this machine holds: the first is
	// the workspace this machine declares, the second one it only names.
	if f := strings.Fields(lines[1]); f[0] != "workspace-alpha" || f[1] != "1" || f[2] != "1" {
		t.Errorf("row %q: want workspace-alpha with one member and one membership", lines[1])
	}
	if f := strings.Fields(lines[2]); f[0] != "workspace-beta" || f[1] != "-" || f[2] != "1" {
		t.Errorf("row %q: want workspace-beta with an unknown member list and one membership", lines[2])
	}
	if strings.Contains(out, "PROBLEMS") {
		t.Errorf("the listing reports drift where both halves agree:\n%s", out)
	}
}

// TestWorkspaceLsReportsAHalfDoneMembership: W2's two-sided check, as far as
// one machine can make it. The owner's table declares aurora, and aurora's own
// config here names nothing -- both halves are printed and neither silently
// wins.
func TestWorkspaceLsReportsAHalfDoneMembership(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	out, _, err := execute(t, "workspace", "ls")
	if err != nil {
		t.Fatalf("workspace ls: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PROBLEMS") || !strings.Contains(out, "aurora's own config here names no workspace") {
		t.Errorf("the half-done membership is not reported:\n%s", out)
	}
}

// TestWorkspaceLsReportsAMembershipTheTableDoesNotDeclare is the second
// direction of the same check: a project here names the workspace, and the
// workspace's own table here declares no such member.
func TestWorkspaceLsReportsAMembershipTheTableDoesNotDeclare(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)
	workspaceAppend(t, owner, "workspace_cap:\n  monthly: 50\n")
	out, _, err := execute(t, "workspace", "show", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "the workspace's table here declares no member aurora") {
		t.Errorf("the unlisted membership is not reported:\n%s", out)
	}
}

// TestWorkspaceShowOnAMemberSaysTheRecordIsNotReadableHere: a member holds no
// grant on the workspace's project, so its own half is the only one it has, and
// the command says so rather than printing an empty member list (spec §6).
func TestWorkspaceShowOnAMemberSaysTheRecordIsNotReadableHere(t *testing.T) {
	_, aurora := workspaceWriteHome(t)
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)
	out, _, err := execute(t, "workspace", "show", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "the group's own record cannot be read from here") {
		t.Errorf("the member's machine does not say which half it holds:\n%s", out)
	}
	if strings.Contains(out, "MEMBERS (") {
		t.Errorf("the member's machine prints a member list:\n%s", out)
	}
	for _, want := range []string{"aurora", "https://securetoken.google.com/project-ws", "assertion key: set"} {
		if !strings.Contains(out, want) {
			t.Errorf("the member's own half is missing %q:\n%s", want, out)
		}
	}
	// The key itself is never printed.
	if strings.Contains(out, "marker-assertion-key") {
		t.Errorf("the assertion key was printed:\n%s", out)
	}
}

// TestWorkspaceShowRefusesAWorkspaceThisMachineDoesNotKnow: a name no config
// names and no config is named after is a refusal that points at the listing.
func TestWorkspaceShowRefusesAWorkspaceThisMachineDoesNotKnow(t *testing.T) {
	workspaceWriteHome(t)
	_, _, err := execute(t, "workspace", "show", "workspace-gamma")
	if err == nil || !strings.Contains(err.Error(), "fugaro workspace ls") {
		t.Fatalf("showing an unknown workspace: %v", err)
	}
}

// TestWorkspaceWritesRefuseAWorkspaceThisMachineDoesNotHold: the owner's half
// lives in the workspace's own project config, and a machine that has none is a
// member's machine -- which writes neither the table nor the cap. The removal is
// the same fence said in its own words, and it is WU6's own test
// (workspace_project_rm_test.go).
func TestWorkspaceWritesRefuseAWorkspaceThisMachineDoesNotHold(t *testing.T) {
	workspaceWriteHome(t)
	for _, args := range [][]string{
		workspaceProjectAddArgs(),
		{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || !strings.Contains(err.Error(), "holds no config for workspace workspace-alpha's own project") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// TestWorkspaceKeysAreNeverPublished: the membership, the pin, the member table,
// the cap and the owner's shared list are this machine's own. The shared config
// is the file any launcher with objectAdmin on the runs bucket can write, and a
// launcher who could write the membership key would be choosing which workspace
// reaches this project's runs, one who could write the pin would be aiming this
// project's exchange, and one who could write the shared list would be handing
// this project's credentials to whoever it names (spec §2's rule 1, D1298). So
// the publisher drops all five (localcfg.Config.Shared) and the reader refuses
// them.
func TestWorkspaceKeysAreNeverPublished(t *testing.T) {
	for _, key := range []string{"workspace", "workspace_pin", "workspace_members", "workspace_cap", "workspace_shared"} {
		if !slices.Contains(sharedForbidden, key) {
			t.Errorf("sharedForbidden does not name %s, so a published membership would be read", key)
		}
	}
	data := validShared() + "workspace: workspace-alpha\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying workspace: was accepted")
	}
	for _, want := range []string{"workspace", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestWorkspaceCommandsRegistered is I1's pin on the family's tree: exactly the
// commands the plan names, each mutating one wrapped with the reporter (D1354),
// each reading one a listing, and `workspace member` gone -- its two verbs are
// `workspace project add|rm` now (D2802), so a tree that still answers to the
// name is a tree that kept the command this change exists to remove.
//
// The wrapping is checked through the census rather than through the command
// object, because the census is what holds the name to the source:
// TestEveryMutatingCommandWritesALog walks the syntax tree for the literal a
// reporter constructor is called with, so a row here whose command is
// registered without the wrapper is a row no call site backs.
func TestWorkspaceCommandsRegistered(t *testing.T) {
	parents := map[string][]string{
		"workspace":         {"add", "cap", "domains", "init", "login", "ls", "project", "rm", "share", "show", "unshare", "user"},
		"workspace project": {"add", "link", "ls", "reconcile", "rm", "save", "status", "unlink"},
		"workspace user":    {"add", "grant", "ls", "revoke", "rm"},
		"workspace domains": {"add", "ls", "rm"},
	}
	for _, path := range slices.Sorted(maps.Keys(parents)) {
		parent := findCommand(strings.Fields(path)...)
		if parent == nil {
			t.Errorf("`fugaro %s` is not in the tree, so the commands under it cannot be reached", path)
			continue
		}
		var got []string
		for _, child := range parent.Commands() {
			got = append(got, child.Name())
		}
		slices.Sort(got)
		if !slices.Equal(got, parents[path]) {
			t.Errorf("`fugaro %s` runs %v, want %v", path, got, parents[path])
		}
	}

	// Every command I1 registered is either on the reporter (it writes) or a
	// listing (it reads): a command of this family that is neither is a command
	// printing an account of its own, which is what the census admits of the
	// five rows that predate this change and of nothing here.
	mutating := []string{
		"workspace init",
		"workspace project add", "workspace project link", "workspace project reconcile",
		"workspace project rm", "workspace project save", "workspace project unlink",
		"workspace user add", "workspace user grant", "workspace user revoke", "workspace user rm",
		"workspace domains add", "workspace domains rm",
	}
	listing := []string{
		"workspace project ls", "workspace project status",
		"workspace user ls", "workspace domains ls",
	}
	for _, path := range mutating {
		name, ok := reporterCommands[path]
		if !ok {
			t.Errorf("`fugaro %s` writes and is not on the census's reporter set, so its run prints through nothing", path)
			continue
		}
		if want := strings.ReplaceAll(path, " ", "-"); name != want {
			t.Errorf("`fugaro %s` files its log under %q, want %q", path, name, want)
		}
		if findCommand(strings.Fields(path)...) == nil {
			t.Errorf("`fugaro %s` is on the census and not in the tree", path)
		}
	}
	for _, path := range listing {
		if _, ok := exemptListings[path]; !ok {
			t.Errorf("`fugaro %s` reads and is not on the census's listing set, so its stdout is claimed as an account", path)
		}
		if findCommand(strings.Fields(path)...) == nil {
			t.Errorf("`fugaro %s` is on the census and not in the tree", path)
		}
	}

	if findCommand("workspace", "member") != nil {
		t.Error("`fugaro workspace member` is still in the tree: project add|rm carry its verbs now (D2802)")
	}
	for _, path := range []string{"workspace member add", "workspace member rm", "workspace member ls"} {
		if _, ok := reporterCommands[path]; ok {
			t.Errorf("the census still carries a reporter row for %q", path)
		}
	}
}

// TestAmbientSidesPrintWhereTheCommandDoes pins the one wiring the state
// writer's own report depends on. `project rm`, `save` and `reconcile` carry no
// config flags: their side selects the ambient config while the tree is built,
// and the tree holds that side as a fixed value. A side built without a writer
// discards what WU15 prints through it -- the file it wrote and, before a push,
// the repository the snapshot was aimed at and the layer that aimed it (D2824),
// which is the one thing nothing can read back afterwards -- so each of the
// three commands points the side at its own output when it runs.
func TestAmbientSidesPrintWhereTheCommandDoes(t *testing.T) {
	for _, what := range []string{
		"fugaro workspace project rm",
		"fugaro workspace project save",
		"fugaro workspace project reconcile",
	} {
		side := workspaceAmbientSide(what)
		if side.out != io.Discard {
			t.Errorf("%s is built with a writer already (%v), so the side is pinned to the tree's build rather than to the command's own output", what, side.out)
			continue
		}
		cmd := &cobra.Command{Use: "save"}
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		if err := workspaceAmbientPoint(side)(cmd); err != nil {
			t.Errorf("%s: pointing the side at the command failed: %v", what, err)
			continue
		}
		if side.out != buf {
			t.Errorf("%s: the side still writes where the command does not (%v), so its report is discarded", what, side.out)
		}
		if got := (workspaceSaveWriter{out: side.out}).out; got != buf {
			t.Errorf("%s: the state writer prints through %v, want the command's own output", what, got)
		}
	}
}
