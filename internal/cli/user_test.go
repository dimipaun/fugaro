package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// userCommand runs the user command tree on its own. `fugaro user` is not
// registered yet (E1a leaves the registration to E1b, after the pairing
// wiring), so execute — which runs the root tree — cannot reach it.
func userCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return userCommandOn(t, newUserCmd(), args...)
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

// userRigFor builds the rig with these launchers and operators in BOTH the
// local config and the installation's outputs, which is the state a change is
// allowed to start from: the two must agree, or the command refuses.
func userRigFor(t *testing.T, launchers, operators []string) *userRig {
	t.Helper()
	noAgentSession(t)
	r := &userRig{initRig: newInitRig(t)}
	r.appendConfig(t, "terraform:\n  launchers: "+yamlList(launchers)+"\n  operators: "+yamlList(operators)+"\n")
	r.installationSets(t, launchers, operators)
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
func (r *userRig) installationSets(t *testing.T, launchers, operators []string) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"launchers": launchers, "operators": operators})}
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
func (r *userRig) lists(t *testing.T) (launchers, operators []string) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc.Terraform.Launchers, lc.Terraform.Operators
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

// TestUserAddWritesLauncher: the whole of a successful add — one member added
// to one list, the other list untouched, nothing applied anywhere.
func TestUserAddWritesLauncher(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "add", "user:person-2@example.com", "--role", "launcher")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	launchers, operators := r.lists(t)
	want := []string{"user:person-1@example.com", "user:person-2@example.com"}
	if strings.Join(launchers, ",") != strings.Join(want, ",") {
		t.Errorf("launchers = %v, want %v", launchers, want)
	}
	if len(operators) != 1 || operators[0] != "user:person-1@example.com" {
		t.Errorf("the operators list was changed: %v", operators)
	}
	if !strings.Contains(out, "user:person-2@example.com") || !strings.Contains(out, "now a launcher") {
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

// TestUserAddRequiresRole: --role has no default. Adding a person to the wrong
// list is a grant, and no default can be the right one for both.
func TestUserAddRequiresRole(t *testing.T) {
	r := userRigFor(t, nil, nil)
	for _, args := range [][]string{
		{"add", "user:person-1@example.com"},
		{"add", "user:person-1@example.com", "--role", "admin"},
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

// TestUserAddRefusesDomain: the launcher and operator roles carry the token
// signer's own token-minter role (F44), so a domain: member would hand a whole
// domain the right to mint a launcher's identity — which is what F42 found
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
			out, _, err := userCommand(t, "add", member, "--role", "launcher")
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
// another operator's member would revoke that member in the same apply — the
// second trap F42 found. The outputs are read first, and a config that
// disagrees with them is refused, whichever of the two lists it is.
func TestUserRefusesStaleLocalList(t *testing.T) {
	for _, tc := range []struct {
		name     string
		local    []string
		remote   []string
		wantList string
	}{
		{
			name: "a member this machine has never seen", local: []string{"user:person-1@example.com"},
			remote:   []string{"user:person-1@example.com", "user:person-2@example.com"},
			wantList: "launchers",
		},
		{
			name: "a stale list here and only operators differ", local: []string{"user:person-1@example.com"},
			remote:   []string{},
			wantList: "launchers",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := userRigFor(t, tc.local, tc.local)
			r.installationSets(t, tc.remote, tc.remote)
			out, _, err := userCommand(t, "add", "user:person-3@example.com", "--role", "launcher")
			if err == nil {
				t.Fatalf("a stale list was edited:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.wantList) {
				t.Errorf("the refusal does not name the %s list: %v", tc.wantList, err)
			}
			if !strings.Contains(err.Error(), "fugaro update") {
				t.Errorf("the refusal does not name the command that settles it: %v", err)
			}
			r.unchanged(t)
			// And the same read refuses rm: removing a member from a stale
			// list drops the members it has never seen just as an add does.
			if _, _, err := userCommand(t, "rm", "user:person-1@example.com", "--role", "launcher"); err == nil {
				t.Error("a stale list was edited by rm")
			}
			r.unchanged(t)
			r.terraformReadsOnly(t)
		})
	}
}

// TestUserRefusesStaleOperators: the operators list is checked too, and it is
// the list the refusal names when only it differs.
func TestUserRefusesStaleOperators(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com"})
	r.installationSets(t, []string{"user:person-1@example.com"}, []string{"user:person-1@example.com", "user:person-2@example.com"})
	out, _, err := userCommand(t, "add", "user:person-3@example.com", "--role", "operator")
	if err == nil {
		t.Fatalf("a stale operators list was edited:\n%s", out)
	}
	if !strings.Contains(err.Error(), "operators") || strings.Contains(err.Error(), "launchers") {
		t.Errorf("the refusal does not name the operators list and only it: %v", err)
	}
	r.unchanged(t)
}

// TestUserRmRemoves: a removal takes one member off one list, and removing a
// member who is not there writes nothing and says so.
func TestUserRmRemoves(t *testing.T) {
	r := userRigFor(t,
		[]string{"user:person-1@example.com", "user:person-2@example.com"},
		[]string{"user:person-1@example.com"})
	out, _, err := userCommand(t, "rm", "user:person-2@example.com", "--role", "launcher")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	launchers, operators := r.lists(t)
	if len(launchers) != 1 || launchers[0] != "user:person-1@example.com" {
		t.Errorf("launchers = %v, want the first member alone", launchers)
	}
	if len(operators) != 1 || operators[0] != "user:person-1@example.com" {
		t.Errorf("the operators list was changed: %v", operators)
	}
	if !strings.Contains(out, "no longer a launcher") {
		t.Errorf("the command does not say what it did:\n%s", out)
	}
	r.terraformReadsOnly(t)
	r.noCloudWrites(t)

	// Removing a member who is not on the list writes nothing and says so. A
	// rig of its own, because the removal above left this machine's list one
	// member shorter than the installation's — the state the next write
	// refuses (TestUserRefusesStaleLocalList).
	absent := userRigFor(t, []string{"user:person-1@example.com"}, nil)
	out, _, err = userCommand(t, "rm", "user:person-2@example.com", "--role", "launcher")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "not a launcher") || !strings.Contains(out, "nothing was written") {
		t.Errorf("removing an absent member does not say nothing was written:\n%s", out)
	}
	absent.unchanged(t)
	absent.noCloudWrites(t)
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
	for _, want := range []string{"user:person-1@example.com", "user:person-2@example.com", "launchers", "operators", initProjectName} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, out)
		}
	}
	if p1, p2 := strings.Index(out, "user:person-1@example.com"), strings.Index(out, "user:person-2@example.com"); p1 > p2 {
		t.Errorf("the listing is not the launchers and then the operators:\n%s", out)
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

// TestUserRefusedInAgentSession: add and rm are run by a person at their own
// terminal (allow_delete.go's rule), so a coding agent's session is refused
// before anything is read and nothing is written.
func TestUserRefusedInAgentSession(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, nil)
	t.Setenv("CLAUDECODE", "1")
	for _, args := range [][]string{
		{"add", "user:person-2@example.com", "--role", "launcher"},
		{"rm", "user:person-1@example.com", "--role", "launcher"},
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
	out, _, err := userCommand(t, "add", "user:person-1@example.com", "--role", "launcher")
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

// TestUserAddIsIdempotent: adding a member the list already holds changes
// nothing — the list is not reordered and no repeat is written — and says so.
func TestUserAddIsIdempotent(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com", "user:person-2@example.com"}, nil)
	out, _, err := userCommand(t, "add", "user:person-1@example.com", "--role", "launcher")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already a launcher") || !strings.Contains(out, "nothing was written") {
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
