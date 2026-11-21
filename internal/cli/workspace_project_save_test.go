package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspace_project_save_test.go is WU23: `workspace project save`, driven
// directly. The command is registered by I1, so what is held here is the
// command's own contract: one member saved through C2's StateWriter, every
// member with no argument, the workspace's audit export behind --audit, and the
// refusals — a coding agent's session, a workspace whose roster was never
// written, a project the owner's member table does not hold.
//
// Every test is hermetic: a fake C1 Store and a fake C2 StateWriter declared
// below, a *localcfg.Config built in memory, and no cloud, no file and no
// terminal.

// wu23Workspace is the workspace's own project name: the name the config of
// the project the workspace is named after carries, which is the name a
// snapshot is filed under.
const wu23Workspace = "workspace-alpha"

// wu23NoAgent clears the markers a coding agent's session sets, so a test that
// drives the save itself is a person's session. The refusal that reads them is
// TestProjectSaveRefusesInAnAgentSession's.
func wu23NoAgent(t *testing.T) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
}

// wu23Owner is the config of the project the workspace is named after: the
// member table D2821 puts on the owner's machine, one entry per member named
// here, each with the Firebase project the member's own config declares. The
// names given are the table's keys, which are the members' Fugaro project names
// — the names a snapshot is filed under, and not GCP project ids.
func wu23Owner(members ...string) *localcfg.Config {
	table := make(map[string]localcfg.WorkspaceMember, len(members))
	for _, m := range members {
		table[m] = localcfg.WorkspaceMember{
			FirebaseProject: m + "-fb",
			FP:              m,
			ExchangeURL:     "https://exchange.example.com/v1",
		}
	}
	return &localcfg.Config{Name: wu23Workspace, WorkspaceMembers: &table}
}

// wu23Roster is a roster of the shape a service-account workspace holds: what
// C1's Store reads back, encoded by C1's own encoder so the bytes are a
// document this build reads.
func wu23Roster(t *testing.T) []byte {
	t.Helper()
	raw, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People:    []workspace.Person{{Address: "alice@example.com", Kind: workspace.KindHuman}},
		Admins:    []string{"alice@example.com"},
		Domains:   []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("EncodeRoster refuses the roster the fake store reads: %v", err)
	}
	return raw
}

// wu23FakeStore is C1's Store with the truth a test sets: bytes or a failure,
// and a Write that is a failure because a save never writes the roster.
type wu23FakeStore struct {
	raw   []byte
	found bool
	err   error
}

func (s *wu23FakeStore) Path() string { return "workspaceRosters/" + wu23Workspace }

func (s *wu23FakeStore) Read(context.Context) (workspace.Revision, []byte, error) {
	if s.err != nil {
		return workspace.Revision{}, nil, s.err
	}
	return workspace.Revision{ETag: "wu23-etag", Found: s.found}, s.raw, nil
}

func (s *wu23FakeStore) Write(context.Context, workspace.Revision, []byte) error {
	return errors.New("wu23: a save never writes the roster")
}

// wu23FakeWriter is C2's StateWriter: it records every snapshot it is asked to
// save and answers where it left one, and it can be made to fail.
type wu23FakeWriter struct {
	saved []workspace.Snapshot
	err   error
}

func (w *wu23FakeWriter) Save(_ context.Context, s workspace.Snapshot) (workspace.SnapshotWritten, error) {
	w.saved = append(w.saved, s)
	if w.err != nil {
		return workspace.SnapshotWritten{}, w.err
	}
	name := "settings-" + s.Member.Project + ".json"
	if s.Audit {
		name = "audit-" + s.Workspace + ".json"
	}
	return workspace.SnapshotWritten{
		File:        "/tmp/wu23/" + name,
		PullRequest: "https://example.test/wu23/pull/1",
	}, nil
}

func (w *wu23FakeWriter) LastSaved(context.Context, workspace.Snapshot) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("wu23: a save reads no last-saved time")
}

// wu23Run drives the command as a person's session and answers what it printed
// and what it returned.
func wu23Run(t *testing.T, deps workspaceProjectSaveDeps, args ...string) (string, error) {
	t.Helper()
	wu23NoAgent(t)
	cmd := newWorkspaceProjectSaveCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	// The run first, and the buffer read after it: the two are one return
	// statement's operands, and Go evaluates them left to right.
	err := cmd.Execute()
	return buf.String(), err
}

func TestProjectSaveOneMember(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a", "member-b"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	out, err := wu23Run(t, deps, "member-a")
	if err != nil {
		t.Fatalf("saving one member failed: %v\n%s", err, out)
	}
	want := []workspace.Snapshot{{
		Workspace: wu23Workspace,
		Member:    workspace.MemberTarget{Project: "member-a", Firebase: "member-a-fb"},
	}}
	if !reflect.DeepEqual(w.saved, want) {
		t.Errorf("the save wrote %+v, want exactly one member's snapshot:\n got %+v\nwant %+v", w.saved, w.saved, want)
	}
	// The save names where it left the snapshot, which is the local file the
	// writer always writes, and the pull request where there is one.
	for _, want := range []string{"/tmp/wu23/settings-member-a.json", "https://example.test/wu23/pull/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the save did not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "member-b") {
		t.Errorf("saving one member saved or named another:\n%s", out)
	}
}

func TestProjectSaveEveryMember(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-b", "member-a"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	out, err := wu23Run(t, deps)
	if err != nil {
		t.Fatalf("saving every member failed: %v\n%s", err, out)
	}
	// The table's order is the file's, never the order a save takes: two runs
	// over one workspace save the same members in the same order.
	want := []workspace.Snapshot{
		{Workspace: wu23Workspace, Member: workspace.MemberTarget{Project: "member-a", Firebase: "member-a-fb"}},
		{Workspace: wu23Workspace, Member: workspace.MemberTarget{Project: "member-b", Firebase: "member-b-fb"}},
	}
	if !reflect.DeepEqual(w.saved, want) {
		t.Errorf("the save wrote %+v, want every member's snapshot:\n got %+v\nwant %+v", w.saved, w.saved, want)
	}
	for _, name := range []string{"member-a", "member-b"} {
		if !strings.Contains(out, name) {
			t.Errorf("the save of every member did not name %s:\n%s", name, out)
		}
	}
}

func TestProjectSaveAuditExport(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a", "member-b"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	out, err := wu23Run(t, deps, "--audit")
	if err != nil {
		t.Fatalf("saving the audit export failed: %v\n%s", err, out)
	}
	want := []workspace.Snapshot{{Workspace: wu23Workspace, Audit: true}}
	if !reflect.DeepEqual(w.saved, want) {
		t.Errorf("--audit wrote %+v, want the workspace's own audit export and no member:\n got %+v\nwant %+v", w.saved, w.saved, want)
	}
	if !strings.Contains(out, "/tmp/wu23/audit-"+wu23Workspace+".json") {
		t.Errorf("--audit did not name the file it wrote:\n%s", out)
	}

	// The audit export is the workspace's own and is not any member's, so a
	// project argument beside it is refused rather than ignored.
	w2 := &wu23FakeWriter{}
	deps.Writer = w2
	out, err = wu23Run(t, deps, "--audit", "member-a")
	if err == nil {
		t.Fatalf("--audit with a project was accepted:\n%s", out)
	}
	if len(w2.saved) != 0 {
		t.Errorf("--audit with a project saved %+v instead of refusing", w2.saved)
	}
}

func TestProjectSaveRefusesInAnAgentSession(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	t.Setenv(agentMarkers[0], "1")
	cmd := newWorkspaceProjectSaveCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"member-a"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("a save ran in a coding agent's session:\n%s", buf.String())
	}
	if len(w.saved) != 0 {
		t.Errorf("a coding agent's session saved %+v, and a refusal writes nothing", w.saved)
	}
	if !strings.Contains(err.Error(), agentMarkers[0]) {
		t.Errorf("the refusal does not name the marker that made the session an agent's: %v", err)
	}
}

// TestProjectSaveRefusesHTTP2Debug: every cloud command of this repository
// refuses to run while GODEBUG holds http2debug, because Go then prints HTTP/2
// requests — the OAuth bearer token of the owner's own ADC included — to
// stderr, and GODEBUG is read before main runs so it cannot be unset in time
// (refuseHTTP2Debug). A save reads the workspace's roster through C1's Store and
// hands the owner's credentials to a writer that pushes with them, and it
// resolves the workspace's own project rather than selecting one, so it never
// passes through openCloud or openBudgetDB, which is where the project scopes
// get the refusal: without its own it is the one save that leaks the token.
func TestProjectSaveRefusesHTTP2Debug(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a", "member-b"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	t.Setenv("GODEBUG", "http2debug=2")

	for _, args := range [][]string{{"member-a"}, {}, {"--audit"}} {
		out, err := wu23Run(t, deps, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
			t.Errorf("project save %v under GODEBUG=http2debug: %v (printed %q), want the refusal every cloud command makes: Go would print the owner's bearer token to stderr", args, err, out)
		}
	}
	if len(w.saved) != 0 {
		t.Errorf("a save that ran under GODEBUG=http2debug wrote snapshots: %+v", w.saved)
	}
}

func TestProjectSaveNeedsAnInitialisedWorkspace(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a"),
		Store:  &wu23FakeStore{found: false},
		Writer: w,
	}
	out, err := wu23Run(t, deps, "member-a")
	if err == nil {
		t.Fatalf("a workspace whose roster was never written was saved:\n%s", out)
	}
	if len(w.saved) != 0 {
		t.Errorf("a save ran against a workspace with no roster: %+v", w.saved)
	}
	if !strings.Contains(err.Error(), "workspace init") {
		t.Errorf("the refusal does not name the command that writes the first roster: %v", err)
	}
}

func TestProjectSaveNeedsAWorkspaceOwnConfig(t *testing.T) {
	w := &wu23FakeWriter{}
	store := &wu23FakeStore{raw: wu23Roster(t), found: true}

	// A project declaring none of the owner's half is not a workspace's own,
	// and a save read from one would answer "nothing to save" to the owner of a
	// workspace whose file is not the one they meant.
	plain := &localcfg.Config{Name: "plain-project"}
	out, err := wu23Run(t, workspaceProjectSaveDeps{Owner: plain, Store: store, Writer: w}, "member-a")
	if err == nil {
		t.Fatalf("a project that is not a workspace's own was saved:\n%s", out)
	}
	if len(w.saved) != 0 {
		t.Errorf("a save ran against a project that is not a workspace's own: %+v", w.saved)
	}
	if !strings.Contains(err.Error(), "member table") {
		t.Errorf("the refusal does not say what the file lacks: %v", err)
	}

	// A workspace's own config that declares only its cap holds a table of no
	// members: that is a state `workspace show` reports and not one to fail on.
	capped := &localcfg.Config{Name: wu23Workspace, WorkspaceCap: &localcfg.WorkspaceCap{Monthly: 25}}
	out, err = wu23Run(t, workspaceProjectSaveDeps{Owner: capped, Store: store, Writer: w})
	if err != nil {
		t.Fatalf("a workspace declaring only its cap was refused: %v\n%s", err, out)
	}
	if len(w.saved) != 0 {
		t.Errorf("a workspace declaring no members saved %+v", w.saved)
	}
	if !strings.Contains(out, "no members") {
		t.Errorf("the save of a workspace with no members did not say so:\n%s", out)
	}
}

func TestProjectSaveRefusesAMemberTheTableDoesNotHold(t *testing.T) {
	w := &wu23FakeWriter{}
	deps := workspaceProjectSaveDeps{
		Owner:  wu23Owner("member-a"),
		Store:  &wu23FakeStore{raw: wu23Roster(t), found: true},
		Writer: w,
	}
	out, err := wu23Run(t, deps, "member-z")
	if err == nil {
		t.Fatalf("a project the owner's member table does not hold was saved:\n%s", out)
	}
	if len(w.saved) != 0 {
		t.Errorf("a save ran for a project the table does not hold: %+v", w.saved)
	}
	if !strings.Contains(err.Error(), "member-z") {
		t.Errorf("the refusal does not name the project it refused: %v", err)
	}
}
