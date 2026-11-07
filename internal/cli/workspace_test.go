package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
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
// beside aurora's: `member add` and `cap set` write the config of the project
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
      - marker-job@aurora-gcp.iam.gserviceaccount.com
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/v1
`

// workspaceMemberAddArgs is the whole of one `member add`, so the tests that
// vary one flag vary one flag.
func workspaceMemberAddArgs(extra ...string) []string {
	return append([]string{"workspace", "member", "add", "workspace-alpha", "aurora",
		"--job-service-account", "marker-job@aurora-gcp.iam.gserviceaccount.com",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v1",
		"--yes"}, extra...)
}

// workspaceAddArgs is the whole of one `workspace add`.
func workspaceAddArgs(extra ...string) []string {
	return append([]string{"workspace", "add", "workspace-alpha",
		"--firebase-project", "project-ws",
		"--issuer", "https://securetoken.google.com/project-ws",
		"--assertion-key", "marker-assertion-key",
		"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com",
		"--yes"}, extra...)
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
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
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
	out, _, err := execute(t, "workspace", "rm", "workspace-alpha", "--yes")
	if err != nil {
		t.Fatalf("workspace rm: %v\n%s", err, out)
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the removal did not restore the file:\n--- before\n%s--- after\n%s", before, after)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
}

// TestWorkspaceRmRefusesAWorkspaceTheProjectDoesNotName: a removal is of the
// membership the file states, and a project that names another workspace (or
// none) is refused rather than edited.
func TestWorkspaceRmRefusesAWorkspaceTheProjectDoesNotName(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)
	if _, _, err := execute(t, "workspace", "rm", "workspace-alpha", "--yes"); err == nil {
		t.Fatal("rm removed a membership the project does not have")
	}
	if after := readFileString(t, path); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
	if _, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	_, _, err := execute(t, "workspace", "rm", "workspace-beta", "--yes")
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
			"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com",
			"--yes")
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
		"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com",
		"--yes")
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

// TestMemberAddWritesTheOwnersHalfInTheWorkspacesOwnConfig: `member add` is the
// owner's side (spec §5). It writes the entry into the config of the project
// the workspace is named after, and the member's own config is another
// machine's file, which it does not touch.
func TestMemberAddWritesTheOwnersHalfInTheWorkspacesOwnConfig(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	auroraBefore, ownerBefore := readFileString(t, aurora), readFileString(t, owner)

	out, _, err := execute(t, workspaceMemberAddArgs("--repo", "owner/repo-a", "--repo", "owner/repo-b")...)
	if err != nil {
		t.Fatalf("workspace member add: %v\n%s", err, out)
	}
	if got := readFileString(t, aurora); got != auroraBefore {
		t.Errorf("member add wrote the member's own config:\n%s", got)
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
		Repos:              []string{"owner/repo-a", "owner/repo-b"},
		JobServiceAccounts: []string{"marker-job@aurora-gcp.iam.gserviceaccount.com"},
		FirebaseProject:    "aurora-fb",
		FP:                 "aurora",
		ExchangeURL:        "https://exchange.example.com/v1",
	}
	if got.FirebaseProject != want.FirebaseProject || got.FP != want.FP || got.ExchangeURL != want.ExchangeURL {
		t.Errorf("the entry is %+v, want the values the flags gave: %+v", got, want)
	}
	if strings.Join(got.JobServiceAccounts, ",") != strings.Join(want.JobServiceAccounts, ",") {
		t.Errorf("job service accounts = %v, want %v", got.JobServiceAccounts, want.JobServiceAccounts)
	}
	if strings.Join(got.Repos, ",") != strings.Join(want.Repos, ",") {
		t.Errorf("repos = %v, want %v", got.Repos, want.Repos)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
}

// TestMemberAddReplacesTheEntryItAlreadyDeclares: a member the table already
// declares is replaced whole, and not added to. AddListItem appends and leaves
// what it finds alone, so a declaration that merged would keep every account
// and every repository the earlier one named — and re-declaring a member is how
// a retired or a compromised job service account leaves the table, so the
// merged entry would leave that identity admitted as a caller of the
// workspace's credentials while the confirmation named only the new one.
func TestMemberAddReplacesTheEntryItAlreadyDeclares(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, `workspace_members:
  aurora:
    job_service_accounts:
      - marker-retired@aurora-gcp.iam.gserviceaccount.com
      - marker-job@aurora-gcp.iam.gserviceaccount.com
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/v1
    repos:
      - owner/repo-old
`)
	ours := []string{"workspace", "member", "add", "workspace-alpha", "aurora",
		"--job-service-account", "marker-new@aurora-gcp.iam.gserviceaccount.com",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v2",
		"--repo", "owner/repo-new",
		"--yes"}
	out, _, err := execute(t, ours...)
	if err != nil {
		t.Fatalf("workspace member add: %v\n%s", err, out)
	}
	after := readFileString(t, owner)
	// Nothing the replaced entry carried survives it: not the retired account,
	// not the account that was still live, not the repository.
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
	if !slices.Equal(got.JobServiceAccounts, []string{"marker-new@aurora-gcp.iam.gserviceaccount.com"}) {
		t.Errorf("job service accounts = %v, want the one the declaration named", got.JobServiceAccounts)
	}
	if !slices.Equal(got.Repos, []string{"owner/repo-new"}) {
		t.Errorf("repos = %v, want the one the declaration named", got.Repos)
	}
	if got.ExchangeURL != "https://exchange.example.com/v2" {
		t.Errorf("exchange URL = %q, want the one the declaration named", got.ExchangeURL)
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
	if out, _, err := execute(t, ours...); err != nil {
		t.Fatalf("workspace member add (again): %v\n%s", err, out)
	} else if !strings.Contains(out, "no change") {
		t.Errorf("a repeated declaration is not a no-op:\n%s", out)
	}
	if readFileString(t, owner) != before {
		t.Errorf("a repeated declaration rewrote the file:\n%s", readFileString(t, owner))
	}
}

// TestMemberAddRefusesToDeclareADatabaseURL: the member's database is derived
// from the Firebase project the verified `iss` names (finding 6), so it is not
// a value the owner may set -- an owner that could would pair one member's
// accounts with another's database. The flag is refused by name rather than by
// pflag's bare "unknown flag".
func TestMemberAddRefusesToDeclareADatabaseURL(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	_, _, err := execute(t, workspaceMemberAddArgs("--database-url", "https://aurora-fb.firebaseio.com")...)
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

// TestMemberAddWritesNoRunsBucket: O1's answer dropped the bucket grant, so the
// table carries no bucket and the flag is refused by name. The second half is
// the written entry itself: nothing in it is bucket-shaped, so a later field
// cannot be added back without this test failing first.
func TestMemberAddWritesNoRunsBucket(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	_, _, err := execute(t, workspaceMemberAddArgs("--runs-bucket", "fugaro-runs-aurora-gcp")...)
	if err == nil {
		t.Fatal("--runs-bucket was accepted")
	}
	if !strings.Contains(err.Error(), "the workspace holds no read of a member's runs") {
		t.Errorf("the refusal does not say why the bucket is not the owner's: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
	if _, _, err := execute(t, workspaceMemberAddArgs()...); err != nil {
		t.Fatalf("workspace member add: %v", err)
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

// TestMemberRmRemovesTheEntryAndTheEmptyTable: the other direction, and the one
// that shows DropEmptyParents: the last member removed takes the empty
// workspace_members block with it, so a table declared and undeclared leaves
// the file byte-identical.
func TestMemberRmRemovesTheEntryAndTheEmptyTable(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	if _, _, err := execute(t, workspaceMemberAddArgs()...); err != nil {
		t.Fatalf("workspace member add: %v", err)
	}
	out, _, err := execute(t, "workspace", "member", "rm", "workspace-alpha", "aurora", "--yes")
	if err != nil {
		t.Fatalf("workspace member rm: %v\n%s", err, out)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the removal did not restore the file:\n--- before\n%s--- after\n%s", before, after)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
}

// TestMemberRmRefusesAnUndeclaredMember: nothing to remove is a refusal and not
// an empty edit.
func TestMemberRmRefusesAnUndeclaredMember(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	before := readFileString(t, owner)
	_, _, err := execute(t, "workspace", "member", "rm", "workspace-alpha", "aurora", "--yes")
	if err == nil || !strings.Contains(err.Error(), "declares no member aurora") {
		t.Fatalf("removing an undeclared member: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the file:\n%s", after)
	}
}

// TestWorkspaceCapSetWritesTheLocalConfigOnly: W8's own test, in the file this
// task's plan gives `cap set` -- the cap is recorded in the local config of the
// workspace's own project, the member's config is untouched, and the cloud is
// written by nobody here (W2's publish is the stage that does).
func TestWorkspaceCapSetWritesTheLocalConfigOnly(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	auroraBefore := readFileString(t, aurora)
	out, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", "--yes")
	if err != nil {
		t.Fatalf("workspace cap set: %v\n%s", err, out)
	}
	if got := readFileString(t, aurora); got != auroraBefore {
		t.Errorf("cap set wrote another project's config:\n%s", got)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	if lc.WorkspaceCap == nil || lc.WorkspaceCap.Monthly != 50 {
		t.Errorf("the cap is %+v, want 50 a month", lc.WorkspaceCap)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
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
		_, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", tc.amount, "--yes")
		if err == nil {
			t.Fatalf("--monthly %s was accepted", tc.amount)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--monthly %s: %v, want a refusal naming %q", tc.amount, err, tc.want)
		}
	}
	if _, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--yes"); err == nil {
		t.Error("a cap with no amount was accepted")
	}
	if _, _, err := execute(t, "workspace", "cap", "set", "--monthly", "50", "--yes"); err == nil ||
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
      - marker-other@aurora-gcp.iam.gserviceaccount.com
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/other
workspace_cap:
  monthly: 40
`)
	auroraBefore, ownerBefore := readFileString(t, aurora), readFileString(t, owner)

	t.Setenv(agentMarkers[0], "1")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"add", workspaceAddArgs()},
		{"rm", []string{"workspace", "rm", "workspace-alpha", "--yes"}},
		{"member add", workspaceMemberAddArgs()},
		{"member rm", []string{"workspace", "member", "rm", "workspace-alpha", "aurora", "--yes"}},
		{"cap set", []string{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", "--yes"}},
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

// TestMemberAddRefusesAWorkspaceThisMachineDoesNotHold: the owner's half lives
// in the workspace's own project config, and a machine that has none is a
// member's machine -- which writes neither the table nor the cap.
func TestMemberAddRefusesAWorkspaceThisMachineDoesNotHold(t *testing.T) {
	workspaceWriteHome(t)
	for _, args := range [][]string{
		workspaceMemberAddArgs(),
		{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", "--yes"},
		{"workspace", "member", "rm", "workspace-alpha", "aurora", "--yes"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || !strings.Contains(err.Error(), "holds no config for workspace workspace-alpha's own project") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// TestWorkspaceKeysAreNeverPublished: the membership, the pin, the member table
// and the cap are this machine's own. The shared config is the file any
// launcher with objectAdmin on the runs bucket can write, and a launcher who
// could write the membership key would be choosing which workspace reaches this
// project's runs, and one who could write the pin would be aiming this
// project's exchange (spec §2's rule 1). So the publisher drops all four
// (localcfg.Config.Shared) and the reader refuses them.
func TestWorkspaceKeysAreNeverPublished(t *testing.T) {
	for _, key := range []string{"workspace", "workspace_pin", "workspace_members", "workspace_cap"} {
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
