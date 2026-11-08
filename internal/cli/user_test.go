package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// userCommand runs the user command tree on its own, so a case here drives one
// command's own flags and output without the root's persistent hooks (the
// plugin staleness line, the update check). That the root reaches the command
// is TestUserIsRegisteredOnTheRoot's to say, not this harness's.
func userCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return userCommandOn(t, newUserCmd(), args...)
}

// TestUserIsRegisteredOnTheRoot: `fugaro user` is wired into the released tree
// (root.go). Every other case here calls newUserCmd() directly, so nothing else
// in this file would notice the command falling out of the root.
func TestUserIsRegisteredOnTheRoot(t *testing.T) {
	cmd, _, err := NewRootCmd().Find([]string{"user", "add"})
	if err != nil {
		t.Fatalf("fugaro user add does not resolve on the root tree: %v", err)
	}
	if cmd == nil || cmd.CommandPath() != "fugaro user add" {
		t.Fatalf("the root tree resolves fugaro user add to %v, want fugaro user add", cmd)
	}
	if parent := cmd.Parent(); parent == nil || parent.Name() != "user" || parent.Hidden {
		t.Errorf("fugaro user add's parent is %v, want the visible fugaro user command", parent)
	}
}

// userCommandOn is userCommand on a command tree the caller built.
func userCommandOn(t *testing.T, cmd *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// userStateJSON is the installation's state as its read proves it managed:
// terraform state rm leaves the root's outputs behind, so outputs alone do
// not say the installation is there.
const userStateJSON = `{"format_version":"1.2","values":{"root_module":{"resources":[{"address":"module.installation.google_storage_bucket.runs"}]}}}`

// userRig is the initRig with the read both writing subcommands make: the
// installation's own outputs, and a state that still manages something.
type userRig struct {
	*initRig
	before []byte // the local config as it was, for the refusals that write nothing
}

// userRigFor builds the rig with these operators and admins in BOTH the
// local config and the installation's outputs, which is the state a change is
// allowed to start from: the two must agree, or the command refuses.
func userRigFor(t *testing.T, operators, admins []string) *userRig {
	t.Helper()
	noAgentSession(t)
	r := &userRig{initRig: newInitRig(t)}
	r.appendConfig(t, "terraform:\n  launchers: "+yamlList(operators)+"\n  operators: "+yamlList(admins)+"\n")
	r.installationSets(t, operators, admins)
	r.script["show -json"] = map[string]any{"stdout": userStateJSON}
	r.save(t)
	r.before = r.configNow(t)
	return r
}

// yamlList is a member list as the config writes it: quoted, so a member's
// own punctuation can never be read as YAML's.
func yamlList(members []string) string {
	if len(members) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(members))
	for _, m := range members {
		quoted = append(quoted, `"`+m+`"`)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// installationSets rewrites what the installation's own outputs list.
func (r *userRig) installationSets(t *testing.T, operators, admins []string) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"launchers": operators, "operators": admins})}
	r.save(t)
}

// configNow is the local config as it is on disk now.
func (r *userRig) configNow(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// unchanged fails the test when the local config is not byte for byte what it
// was: a refused command writes nothing at all.
func (r *userRig) unchanged(t *testing.T) {
	t.Helper()
	if now := r.configNow(t); !bytes.Equal(now, r.before) {
		t.Errorf("the local config changed:\n%s\n→\n%s", r.before, now)
	}
}

// lists is the two lists the local config holds now.
func (r *userRig) lists(t *testing.T) (operators, admins []string) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc.Terraform.Operators, lc.Terraform.Admins
}

// terraformReadsOnly fails the test when the run reached a Terraform
// subcommand that is not a read: the change this command makes is the local
// config's, and nothing is planned or applied.
func (r *userRig) terraformReadsOnly(t *testing.T) {
	t.Helper()
	for _, c := range r.calls(t) {
		switch c[0] {
		case "init", "output", "show", "version":
		default:
			t.Errorf("the command ran terraform %s", strings.Join(c, " "))
		}
	}
}

// noCloudWrites fails the test when any of the fakes was sent a call that
// would change something. The lists are the local config's; an IAM grant is
// what a later fugaro update writes, in its own apply, and never this
// command.
func (r *userRig) noCloudWrites(t *testing.T) {
	t.Helper()
	for name, s := range map[string]*gcpfake.Server{
		"artifact_registry": r.ar.Server, "crm": r.crm.Server, "gcs": r.gcs.Server,
		"iam": r.iam.Server, "logging": r.logs.Server, "run": r.run.Server,
		"scheduler": r.sched.Server, "secrets": r.sm.Server, "service_usage": r.su.Server,
	} {
		for _, req := range s.Requests() {
			switch req.Method {
			case "GET", "HEAD":
			default:
				t.Errorf("the command sent %s %s to %s", req.Method, req.Path, name)
			}
		}
	}
}

// TestUserAddWritesOperator: the whole of a successful add — one member added
// to one list, the other list untouched, nothing applied anywhere.
func TestUserAddWritesOperator(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "add", "user:person-2@example.com", "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	operators, admins := r.lists(t)
	want := []string{"user:person-1@example.com", "user:person-2@example.com"}
	if strings.Join(operators, ",") != strings.Join(want, ",") {
		t.Errorf("operators = %v, want %v", operators, want)
	}
	if len(admins) != 1 || admins[0] != "user:person-1@example.com" {
		t.Errorf("the admins list was changed: %v", admins)
	}
	if !strings.Contains(out, "user:person-2@example.com") || !strings.Contains(out, "now an operator") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	// The write is what the read above it allowed: the installation's own
	// outputs were read, so a list that had drifted from them could not have
	// reached the file (TestUserRefusesStaleLocalList is that refusal).
	if len(r.ran(t, "output")) == 0 || len(r.ran(t, "show")) == 0 {
		t.Errorf("the installation's own outputs were not read: %v", r.calls(t))
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)
}

// TestUserAddWritesAdmin: the admin branch of the write, which is the
// same code path as the operator's with the other list behind it — and the one
// that decides who may settle the installation, so it is exercised on its own.
func TestUserAddWritesAdmin(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "add", "user:person-2@example.com", "--role", "admin")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	operators, admins := r.lists(t)
	if strings.Join(admins, ",") != "user:person-1@example.com,user:person-2@example.com" {
		t.Errorf("admins = %v, want both members", admins)
	}
	if strings.Join(operators, ",") != "user:person-1@example.com" {
		t.Errorf("the operators list was changed: %v", operators)
	}
	if !strings.Contains(out, "now an admin") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)
}

// TestUserAddDoesNotPersistRegion: --region addresses this invocation, and the
// config this command writes is the one on disk — the selection's copy has the
// flag applied to it (selectProject → announce → localcfg.Config.Override), so
// a document marshalled from that copy would make an ephemeral --region the
// installation's own region, which the next fugaro update applies to every
// resource of the project.
func TestUserAddDoesNotPersistRegion(t *testing.T) {
	r := userRigFor(t, nil, nil)
	out, _, err := userCommand(t, "add", "user:person-1@example.com", "--role", "operator", "--region", "europe-west1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lc, err := localcfg.Parse(r.configNow(t))
	if err != nil {
		t.Fatal(err)
	}
	if lc.Region != "us-east5" {
		t.Errorf("the written config's region is %q, want the file's own us-east5: an ephemeral --region became the installation's region on disk", lc.Region)
	}
	if got := strings.Join(lc.Terraform.Operators, ","); got != "user:person-1@example.com" {
		t.Errorf("the add did not write the list it was for: %q", got)
	}
}

// TestUserAddRequiresRole: --role has no default. Adding a person to the wrong
// list is a grant, and no default can be the right one for both.
func TestUserAddRequiresRole(t *testing.T) {
	r := userRigFor(t, nil, nil)
	for _, args := range [][]string{
		{"add", "user:person-1@example.com"},
		{"add", "user:person-1@example.com", "--role", "owner"},
		{"rm", "user:person-1@example.com"},
		{"rm", "user:person-1@example.com", "--role", ""},
	} {
		out, _, err := userCommand(t, args...)
		if err == nil {
			t.Fatalf("%v ran without a usable --role:\n%s", args, out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), "--role") {
			t.Errorf("%v: the refusal does not name --role: %v", args, err)
		}
	}
	r.unchanged(t)
}

// TestUserAddRefusesDomain: the operator and admin roles carry the token
// signer's own token-minter role (F44), so a domain: member would hand a whole
// domain the right to mint an operator's identity — which is what F42 found
// localcfg's own member rule lets through for these two lists. Everything that
// is not one person is refused, in any case and with or without surrounding
// whitespace, and nothing is written.
func TestUserAddRefusesDomain(t *testing.T) {
	for _, member := range []string{
		"domain:example.com",
		"Domain:example.com",
		"DOMAIN:Example.com",
		"group:eng@example.com",
		"Group:eng@example.com",
		"serviceAccount:bot@proj-1234.iam.gserviceaccount.com",
		"allUsers",
		"allAuthenticatedUsers",
		"ALLUSERS",
		"user:*",
		"user:person-1@example.com*",
		"user:person-1@*",
		" user:person-1@example.com",
		"user:person-1@example.com ",
		"USER:person-1@example.com",
		"User:person-1@example.com",
		"user:person-1@example.com\u00a0",
		"user:person-1",
	} {
		t.Run(member, func(t *testing.T) {
			r := userRigFor(t, nil, nil)
			out, _, err := userCommand(t, "add", member, "--role", "operator")
			if err == nil {
				t.Fatalf("%q was accepted as a member:\n%s", member, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "user:") {
				t.Errorf("the refusal does not say what a member must be: %v", err)
			}
			r.unchanged(t)
		})
	}
}

// TestUserRefusesStaleLocalList: this machine's lists are what its next
// `fugaro update` applies, so a change written from a list that predates
// another admin's member would revoke that member in the same apply — the
// second trap F42 found. The outputs are read first, and a config that
// disagrees with them is refused, whichever of the two lists it is.
func TestUserRefusesStaleLocalList(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		localO, localA, remoteO, remoteA []string
		wantList                         string
	}{
		{
			name:   "a member this machine has never seen",
			localO: []string{"user:person-1@example.com"}, localA: []string{"user:person-1@example.com"},
			remoteO: []string{"user:person-1@example.com", "user:person-2@example.com"},
			remoteA: []string{"user:person-1@example.com"},
			// Only the operators differ, so only they may be named.
			wantList: "operators",
		},
		{
			name:   "the installation lists no operator at all",
			localO: []string{"user:person-1@example.com"}, localA: []string{"user:person-1@example.com"},
			remoteO: nil, remoteA: []string{"user:person-1@example.com"},
			wantList: "operators",
		},
		{
			name:   "only the admins differ",
			localO: []string{"user:person-1@example.com"}, localA: []string{"user:person-1@example.com"},
			remoteO:  []string{"user:person-1@example.com"},
			remoteA:  []string{"user:person-1@example.com", "user:person-2@example.com"},
			wantList: "admins",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := userRigFor(t, tc.localO, tc.localA)
			r.installationSets(t, tc.remoteO, tc.remoteA)
			out, _, err := userCommand(t, "add", "user:person-3@example.com", "--role", "operator")
			if err == nil {
				t.Fatalf("a stale list was edited:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.wantList) {
				t.Errorf("the refusal does not name the %s list: %v", tc.wantList, err)
			}
			other := "operators"
			if tc.wantList == "operators" {
				other = "admins"
			}
			if strings.Contains(err.Error(), other) {
				t.Errorf("the refusal names the %s list although only the %s differs: %v", other, tc.wantList, err)
			}
			if !strings.Contains(err.Error(), "fugaro update") {
				t.Errorf("the refusal does not name the command that settles it: %v", err)
			}
			r.unchanged(t)
			// And the same read refuses rm: removing a member from a stale
			// list drops the members it has never seen just as an add does.
			if _, _, err := userCommand(t, "rm", "user:person-1@example.com", "--role", "operator"); err == nil {
				t.Error("a stale list was edited by rm")
			}
			r.unchanged(t)
			r.terraformReadsOnly(t)
		})
	}
}

// TestUserRefusesStaleAdmins: the admins list is checked too, and it is
// the list the refusal names when only it differs.
func TestUserRefusesStaleAdmins(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	r.installationSets(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com", "user:person-2@example.com"})
	out, _, err := userCommand(t, "add", "user:person-3@example.com", "--role", "admin")
	if err == nil {
		t.Fatalf("a stale admins list was edited:\n%s", out)
	}
	if !strings.Contains(err.Error(), "admins") || strings.Contains(err.Error(), "operators") {
		t.Errorf("the refusal does not name the admins list and only it: %v", err)
	}
	r.unchanged(t)
}

// TestUserRmRemoves: a removal takes one member off one list, and removing a
// member who is not there writes nothing and says so.
func TestUserRmRemoves(t *testing.T) {
	r := userRigFor(t,
		[]string{"user:person-1@example.com", "user:person-2@example.com"},
		[]string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "rm", "user:person-2@example.com", "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	operators, admins := r.lists(t)
	if len(operators) != 1 || operators[0] != "user:person-1@example.com" {
		t.Errorf("operators = %v, want the first member alone", operators)
	}
	if len(admins) != 1 || admins[0] != "user:person-1@example.com" {
		t.Errorf("the admins list was changed: %v", admins)
	}
	if !strings.Contains(out, "no longer an operator") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)

	// Removing a member who is not on the list writes nothing and says so. A
	// rig of its own, because the removal above left this machine's list one
	// member shorter than the installation's — the state the next write
	// refuses (TestUserRefusesStaleLocalList).
	absent := userRigFor(t, []string{"user:person-1@example.com"}, nil)
	out, _, err = userCommand(t, "rm", "user:person-2@example.com", "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "not an operator") || !strings.Contains(out, "nothing was written") {
		t.Errorf("removing an absent member does not say nothing was written:\n%s", out)
	}
	absent.unchanged(t)
	absent.noCloudWrites(t)
}

// TestUserRmRemovesAdmin: a removal from the admins list, the other half
// of the pair TestUserAddWritesAdmin covers.
func TestUserRmRemovesAdmin(t *testing.T) {
	r := userRigFor(t,
		[]string{"user:person-1@example.com"},
		[]string{"user:person-1@example.com", "user:person-2@example.com"})
	out, _, err := userCommand(t, "rm", "user:person-2@example.com", "--role", "admin")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	operators, admins := r.lists(t)
	if strings.Join(admins, ",") != "user:person-1@example.com" {
		t.Errorf("admins = %v, want the first member alone", admins)
	}
	if strings.Join(operators, ",") != "user:person-1@example.com" {
		t.Errorf("the operators list was changed: %v", operators)
	}
	if !strings.Contains(out, "no longer an admin") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)
}

// TestUserRmRefusesTheLastAdmin: an installation with no admin at all
// has nobody this machine can be told to settle it — an update, an add, a
// budget — and the next fugaro update would apply the empty list over the one
// the installation holds, dropping every admin binding. A project Owner can
// restore it from outside, so this is a refusal with what to do first in it
// rather than a break-glass. The operators list may be emptied: a run nobody
// can start is not the same as an installation nobody can put right here.
func TestUserRmRefusesTheLastAdmin(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "rm", "user:person-1@example.com", "--role", "admin")
	if err == nil {
		t.Fatalf("the last admin was removed:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "no admin") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say what it would leave and that nothing was written: %v", err)
	}
	r.unchanged(t)

	l := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	if out, _, err := userCommand(t, "rm", "user:person-1@example.com", "--role", "operator"); err != nil {
		t.Errorf("the last operator was not removable: %v\n%s", err, out)
	}
	if operators, _ := l.lists(t); len(operators) != 0 {
		t.Errorf("operators = %v, want none", operators)
	}
}

// userRigMe is who the rig's local config says the caller is — initConfig's
// own `user: test@example.com` — written as the member text the two lists
// hold, which is the member roles.go's meMember writes.
const userRigMe = "user:test@example.com"

// TestUserRmRefusesRemovingYourselfWithTwoAdmins: the person running the
// command cannot take their own member off the admins list, even when the
// list holds somebody else and removing them would leave it populated. The
// admins list is who can be told to settle the installation, and settling
// it is not a thing a person hands off to themselves: another admin has to
// remove them, or a project Owner from outside the list.
func TestUserRmRefusesRemovingYourselfWithTwoAdmins(t *testing.T) {
	r := userRigFor(t, nil, []string{userRigMe, "user:person-2@example.com"})
	out, _, err := userCommand(t, "rm", userRigMe, "--role", "admin")
	if err == nil {
		t.Fatalf("the caller removed themselves from the admins:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	for _, want := range []string{"remove yourself", "another admin", "project Owner", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	r.unchanged(t)
}

// TestUserRmRefusesRemovingYourselfAsLastAdmin: both refusals apply to the
// only admin removing themselves, and the self-removal is the one shown —
// it is the one with the way out in it (somebody else does it), and the count
// of admins is a consequence of the member being the caller, not the reason.
func TestUserRmRefusesRemovingYourselfAsLastAdmin(t *testing.T) {
	r := userRigFor(t, nil, []string{userRigMe})
	out, _, err := userCommand(t, "rm", userRigMe, "--role", "admin")
	if err == nil {
		t.Fatalf("the only admin removed themselves:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "remove yourself") {
		t.Errorf("the refusal is not the self-removal one: %v", err)
	}
	if strings.Contains(err.Error(), "no admin at all") {
		t.Errorf("the last-admin refusal was the one shown: %v", err)
	}
	r.unchanged(t)
}

// TestUserRmAllowsRemovingAnotherAdmin: removing somebody else is what the
// command is for, and the refusal is the caller's own member alone.
func TestUserRmAllowsRemovingAnotherAdmin(t *testing.T) {
	r := userRigFor(t, nil, []string{userRigMe, "user:person-2@example.com"})
	out, _, err := userCommand(t, "rm", "user:person-2@example.com", "--role", "admin")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, admins := r.lists(t); strings.Join(admins, ",") != userRigMe {
		t.Errorf("admins = %v, want the caller alone", admins)
	}
	if !strings.Contains(out, "no longer an admin") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	r.noCloudWrites(t)
}

// TestUserRmSelfMatchIsNormalised: the caller is placed by the same
// normalisation every role check places them with — meMember turns the local
// config's own `user:` into the `user:<address>` the lists hold — so the two
// spellings are one person and the refusal turns on the identity.
func TestUserRmSelfMatchIsNormalised(t *testing.T) {
	if got := meMember("test@example.com"); got != userRigMe {
		t.Fatalf("meMember(%q) = %q, want %q", "test@example.com", got, userRigMe)
	}
	// The same member text is removable from the operators: the refusal is
	// the admins list's, and the identity it turns on is placed on both.
	r := userRigFor(t, []string{userRigMe, "user:person-2@example.com"}, []string{userRigMe, "user:person-2@example.com"})
	out, _, err := userCommand(t, "rm", userRigMe, "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if operators, _ := r.lists(t); strings.Join(operators, ",") != "user:person-2@example.com" {
		t.Errorf("operators = %v, want the caller's member gone", operators)
	}
}

// TestUserRmSelfRefusalWritesNothing: the refusal is made before anything is
// written — the config is byte for byte what it was, both members are still
// listed, no cloud call changes anything and Terraform is read, never applied.
func TestUserRmSelfRefusalWritesNothing(t *testing.T) {
	r := userRigFor(t, nil, []string{userRigMe, "user:person-2@example.com"})
	out, _, err := userCommand(t, "rm", userRigMe, "--role", "admin")
	if err == nil {
		t.Fatalf("the caller removed themselves from the admins:\n%s", out)
	}
	r.unchanged(t)
	if _, admins := r.lists(t); strings.Join(admins, ",") != userRigMe+",user:person-2@example.com" {
		t.Errorf("admins = %v, want both members", admins)
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)
}

// setUser rewrites who the rig's local config says the caller is — its `user:`
// line, which localcfg.Me answers with before it looks at git config user.email
// — and takes the new file as the state a refusal must leave alone.
func (r *userRig) setUser(t *testing.T, user string) {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(r.configNow(t)), "\n") {
		if strings.HasPrefix(line, "user: ") {
			line = "user: " + user
		}
		out = append(out, line)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Join(out, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	r.before = r.configNow(t)
}

// TestUserRmSelfMatchIgnoresEmailCase: an address's case is not part of who it
// is, and the two spellings the guard compares are written by different hands —
// the config's `user:` (or git config user.email) keeps whatever case its owner
// typed, while the member read back from the installation's own outputs is
// lower-cased. So a caller spelled Person-1@Example.test is the admin the
// list holds as user:person-1@example.test, and removing that member is the
// self-removal the guard refuses rather than a removal that goes through.
func TestUserRmSelfMatchIgnoresEmailCase(t *testing.T) {
	r := userRigFor(t, nil, []string{"user:person-1@example.test", "user:person-2@example.test"})
	r.setUser(t, "Person-1@Example.test")
	out, _, err := userCommand(t, "rm", "user:person-1@example.test", "--role", "admin")
	if err == nil {
		t.Fatalf("the caller removed themselves from the admins:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "remove yourself") {
		t.Errorf("the refusal is not the self-removal one: %v", err)
	}
	r.unchanged(t)
}

// dropUser rewrites the rig's local config without its user line, which is the
// state a caller whose identity cannot be read is in.
func (r *userRig) dropUser(t *testing.T) {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(string(r.configNow(t)), "\n") {
		if strings.HasPrefix(line, "user:") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUserRmRefusesWhenCallerIdentityUnreadable: the self-removal guard turns
// on who is running the command, and a caller the local config cannot tell —
// no user, git config user.email answering nothing — is refused rather than
// read as "not the caller". The empty member meMember answers is on no list, so
// reading lc.Me's error that way is what would let an admin remove
// themselves from the list that is who can be told to settle the installation,
// which is the one thing the guard exists to stop.
func TestUserRmRefusesWhenCallerIdentityUnreadable(t *testing.T) {
	r := userRigFor(t, nil, []string{userRigMe, "user:person-2@example.com"})
	// After the rig, whose fake terraform is built by `go build`: a GIT_DIR of
	// this test's would fail its VCS stamping.
	testutil.IsolateGit(t)
	t.Setenv("GIT_DIR", t.TempDir())
	r.dropUser(t)
	before := string(r.configNow(t))
	out, _, err := userCommand(t, "rm", userRigMe, "--role", "admin")
	if err == nil {
		t.Fatalf("an admin removed themselves with the identity unread:\n%s", out)
	}
	for _, want := range []string{"cannot tell whether", "set user in the local config", "git config user.email", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if now := string(r.configNow(t)); now != before {
		t.Errorf("the refused command wrote the config:\n%s", now)
	}
	if _, admins := r.lists(t); strings.Join(admins, ",") != userRigMe+",user:person-2@example.com" {
		t.Errorf("admins = %v, want both members", admins)
	}
	r.noCloudWrites(t)
}

// TestUserLsListsBoth: the listing is this machine's own config, both lists,
// with the addresses escaped the way this program prints a value it did not
// write.
func TestUserLsListsBoth(t *testing.T) {
	r := userRigFor(t,
		[]string{"user:person-1@example.com"},
		[]string{"user:person-2@example.com"})
	out, _, err := userCommand(t, "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"user:person-1@example.com", "user:person-2@example.com", "operators", "admins", initProjectName} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, out)
		}
	}
	if p1, p2 := strings.Index(out, "user:person-1@example.com"), strings.Index(out, "user:person-2@example.com"); p1 > p2 {
		t.Errorf("the listing is not the operators and then the admins:\n%s", out)
	}
	js, _, err := userCommand(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc userListDoc
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("the --json document: %v\n%s", err, js)
	}
	if doc.Project != initProjectName || doc.GCPProject != initProject {
		t.Errorf("the document names %q/%q, want %q/%q", doc.Project, doc.GCPProject, initProjectName, initProject)
	}
	if strings.Join(doc.Launchers, ",") != "user:person-1@example.com" || strings.Join(doc.Operators, ",") != "user:person-2@example.com" {
		t.Errorf("the document lists %v / %v", doc.Launchers, doc.Operators)
	}
	// Neither form reads the cloud or runs Terraform: ls is the two lists the
	// config holds, and nothing else.
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("ls ran terraform: %v", calls)
	}
	r.noCloudWrites(t)
	// And an agent's session may read them: the refusal is for the two
	// writing subcommands.
	t.Setenv("CLAUDECODE", "1")
	if _, _, err := userCommand(t, "ls"); err != nil {
		t.Errorf("ls refused in a coding agent's session: %v", err)
	}
}

// TestUserLsRefusesHTTP2Debug: ls is open to a coding agent, and its
// selection can read the runs bucket with the person's credentials, so
// GODEBUG=http2debug is refused before anything is read.
func TestUserLsRefusesHTTP2Debug(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-2@example.com"})
	t.Setenv("GODEBUG", "http2debug=2")
	out, _, err := userCommand(t, "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("ls under GODEBUG=http2debug: %v\n%s", err, out)
	}
	if strings.Contains(out, "person-1") {
		t.Errorf("ls listed members before refusing:\n%s", out)
	}
	r.noCloudWrites(t)
}

// TestUserRefusedInAgentSession: add and rm are run by a person at their own
// terminal (allow_delete.go's rule), so a coding agent's session is refused
// before anything is read and nothing is written.
func TestUserRefusedInAgentSession(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, nil)
	t.Setenv("CLAUDECODE", "1")
	for _, args := range [][]string{
		{"add", "user:person-2@example.com", "--role", "operator"},
		{"rm", "user:person-1@example.com", "--role", "operator"},
	} {
		out, _, err := userCommand(t, args...)
		if err == nil {
			t.Fatalf("%v ran in a coding agent's session:\n%s", args, out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
			t.Errorf("%v: the refusal is not the one allow-delete prints: %v", args, err)
		}
	}
	r.unchanged(t)
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("a refused command read the installation: %v", calls)
	}
}

// TestUserInviteHasNoSecret: the invite is text for a person to paste. It
// carries the project's name, the install line and where the project is
// named; it carries no credential of any kind — no token, key or secret is
// minted, read or printed, and no email is sent.
func TestUserInviteHasNoSecret(t *testing.T) {
	userRigFor(t, nil, nil)
	out, _, err := userCommand(t, "add", "user:person-1@example.com", "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The invite itself is the text a person pastes, and the words are looked
	// for in it alone: the lines above it name this test's own temporary
	// directory, which carries the test's name.
	at := strings.Index(out, "invite for")
	if at < 0 {
		t.Fatalf("no invite was printed:\n%s", out)
	}
	invite := out[at:]
	for _, material := range []string{"secret", "token", "key", "password", "credential", "oauth", "private", "BEGIN "} {
		if strings.Contains(strings.ToLower(invite), material) {
			t.Errorf("the invite carries %q:\n%s", material, invite)
		}
	}
	for _, material := range []string{"-----BEGIN", "password", "oauth", "token"} {
		if strings.Contains(strings.ToLower(out), material) {
			t.Errorf("the command printed %q:\n%s", material, out)
		}
	}
	for _, want := range []string{
		initProjectName,
		"install: brew install dimipaun/tap/fugaro",
		"the repository's fugaro.yaml names the project; no setup is needed",
		"fugaro update",
		"no email is sent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the invite does not hold %q:\n%s", want, out)
		}
	}
}

// TestUserInstallLineMatchesREADME: the invite hands a person the command that
// installs fugaro, and the README hands a person the same one. Nothing else
// ties the two copies together (reconciliation item 21 counts them as a drift
// risk), so this does.
func TestUserInstallLineMatchesREADME(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), userInstallLine) {
		t.Errorf("README.md does not carry the invite's install line %q: the invite is a person's first instruction and the README is where they check it", userInstallLine)
	}
}

// TestUserAddIsIdempotent: adding a member the list already holds changes
// nothing — the list is not reordered and no repeat is written — and says so.
func TestUserAddIsIdempotent(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com", "user:person-2@example.com"}, nil)
	out, _, err := userCommand(t, "add", "user:person-1@example.com", "--role", "operator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already an operator") || !strings.Contains(out, "nothing was written") {
		t.Errorf("adding a member who is already listed does not say nothing was written:\n%s", out)
	}
	r.unchanged(t)
	r.noCloudWrites(t)
}

// TestUserRefusesASelectionWithNoFile: the lists live in this machine's own
// project config, and a selection that fell back to the shared one has none
// (a shared config carries no terraform block at all): there is nowhere to
// write, and an empty two lists would be a lie.
func TestUserRefusesASelectionWithNoFile(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName}
	err := userLocalConfig(localcfg.Selection{Name: initProjectName, From: "shared config"}, lc)
	if err == nil {
		t.Fatal("a selection with no file was answered for")
	}
	if !strings.Contains(err.Error(), "fugaro setup") {
		t.Errorf("the refusal does not name the door that writes one: %v", err)
	}
}
