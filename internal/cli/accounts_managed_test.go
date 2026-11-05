package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// accounts_managed.go's tests: `fugaro accounts add` in a managed project,
// under a coding agent's marker (the managed plan's §2 accounts handoff row,
// M6's one helper).
//
// What these hold, and why each is a test rather than a comment. The row's
// whole content is a NEGATIVE: the declaration an agent's session may write,
// and the four things it may not — a reserved name, a widening, a token entry,
// and a write in a project whose marks do not say managed. Every one of those
// is a failure to act, so a command with the gate missing does exactly what it
// did before this row landed (it refuses at the fence, writing nothing), and a
// test that only asserted "the refusal is unchanged" would pass on a build
// with no gate at all. The red step is therefore the positive half: the
// declaration IS written, its admission IS recorded, and the run stops at the
// token — which no build without the gate can do.
//
// Every world here is hermetic: internal/gcpfake's GCS and Resource Manager
// behind the endpoints the rig writes, the installation rig's own budget
// database and runs bucket, t.TempDir() for the checkout the widening is
// measured against, and the credentials read off a path in that directory so a
// developer's own Application Default Credentials can never decide what a
// record says. No test contacts a cloud, and none needs an account.

// managedAddMarker is the creation fact a managed installation's runs bucket
// holds: version 1, naming the GCP project the read is asked about.
func managedAddMarker() []byte {
	return []byte(`{"version":1,"gcp_project":"` + initProject + `"}`)
}

// newManagedAddRig is the accounts-add rig in a project whose marks say managed
// (or, with managed false, in the ordinary installation this row must leave
// exactly as it was), running in a checkout whose origin is owner/repo-a: the
// --repos that is not a widening is decided by that origin, and nothing else in
// this file is.
func newManagedAddRig(t *testing.T, local string, marks managedAddMarks) *addRig {
	t.Helper()
	// The rig first and the checkout after it: the rig builds the fake
	// terraform once per test binary, from the module this checkout is not in.
	r := newAddRigWith(t, local)
	accountsAddCheckout(t, "owner/repo-a")
	// The identity a record names is this test's own: a file that happened to
	// be on the machine would otherwise name a real address in a record, and in
	// this test's failure output. The time is fixed for the same reason —
	// a record is filed under its day, and a test reads one day back.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(r.dir, "no-application-default-credentials.json"))
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })
	// Both marks are required and neither is a shortcut for the other: the
	// creation fact in the runs bucket the GCP project's own number owns,
	// carrying the installation's label, and the fugaro_managed label on the
	// project itself. A world with one of the two is a world this program must
	// answer "not managed" about, which is why the fixtures below exist.
	switch marks {
	case addMarksManaged:
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
		r.gcs.Put(initRunsBucket, managed.MarkerObject, managedAddMarker())
		labelled(r.crm, initProject)
	case addMarksUnlabelled:
		r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
		r.gcs.Put(initRunsBucket, managed.MarkerObject, managedAddMarker())
	}
	return r
}

// managedAddMarks is which of the two marks a rig's project carries. The
// ordinary installation is addMarksNone, and it is the world every refusal this
// row must NOT change is tested in.
type managedAddMarks int

const (
	// addMarksNone is a project with neither mark: the ordinary installation.
	addMarksNone managedAddMarks = iota
	// addMarksManaged is both marks, which is the only world the gate admits
	// anything in.
	addMarksManaged
	// addMarksUnlabelled is the creation fact without the project's label: one
	// half of the fact, which is not the fact.
	addMarksUnlabelled
)

// managedAddLocalWide is the fixture with a second repository on the profile's
// own allow_repos: the world a widening is possible in at all. The subset rule
// runs before the gate, so an account that covered less than the profile admits
// would be refused by that rule and never reach the one this file is about.
func managedAddLocalWide(t *testing.T) string {
	t.Helper()
	const listed = "      - github/owner/repo-a\n"
	if !strings.Contains(addRigLocal, listed) {
		t.Fatalf("the fixture's profile block is not the one this widens:\n%s", addRigLocal)
	}
	wide := strings.Replace(addRigLocal, listed, listed+"      - github/acme/other\n", 1)
	wide += "  acme/other: { provider: github, github_app_id: \"12345\", workflows: [web] }\n"
	return wide
}

// audits is the day's gate records, read back through the reader a surface
// uses: what a test asserts about is what doctor and report would print.
func (r *addRig) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), managedStorage(t, r.gcs), initRunsBucket, managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// accountsAddAgent is the one marker every test here sets, held in one place so
// a test that forgot it is a test that says so.
const accountsAddAgent = "CLAUDECODE"

// The red step: in a managed project's coding agent session, the declaration
// is written and its admission is recorded, where every refusal this command
// made before the gate existed refused to write the file at all. The run
// carries no token — nothing declares one, and no gate admits one — so what it
// leaves behind is the declaration, the record of the gate's verdict, and the
// sentence naming the one command the owner runs at their own terminal.
func TestAccountsAddManagedAdmitsTheDeclarationInAnAgentSession(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	writes := r.secretWrites(t)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	after := readFileString(t, r.cfg)
	if after == before {
		t.Fatalf("the declaration was not written in a managed project's coding agent session: %v\nstdout:\n%s", err, out)
	}
	lc := r.addLocal(t)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want [owner/repo-a]", got)
	}
	if !slices.Contains(lc.Profiles["standard"].ClaudeAccounts, "team-a") {
		t.Errorf("the profile does not serve the new account: %v", lc.Profiles["standard"].ClaudeAccounts)
	}

	// The record is the gate's, it is written after the write, and the digest
	// is of the bytes the command applied — the same artifact, provably.
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one admission of this write: %+v", len(recs), recs)
	}
	rec := recs[0]
	if !strings.HasSuffix(rec.Command, " accounts add") {
		t.Errorf("the record names %q, want the command a person types", rec.Command)
	}
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q: the step was applied", rec.Verdict, managedmode.AuditAdmitted)
	}
	if want := gateDigest([]byte(after)); rec.PlanDigest != want {
		t.Errorf("the record's plan_digest is %q and the bytes written hash to %q: the record does not name the document that was applied", rec.PlanDigest, want)
	}
	if rec.AgentMarker != accountsAddAgent {
		t.Errorf("the record's agent_marker is %q, want the session's own marker", rec.AgentMarker)
	}
	if rec.Identity != "user:"+managedGateUser {
		t.Errorf("the record names identity %q, want the credentials in force", rec.Identity)
	}
	if rec.MarkerVersion != 1 {
		t.Errorf("the record names creation fact version %d, want the one in force", rec.MarkerVersion)
	}
	for _, want := range []string{"reserved", "covers every repository", "widens no repository", "no token entry"} {
		if !slices.ContainsFunc(rec.Rules, func(rule string) bool { return strings.Contains(rule, want) }) {
			t.Errorf("the record's rules (%v) do not name the rule %q: the record is what an owner reads to see what was judged", rec.Rules, want)
		}
	}

	// The token is the one step no gate admits, so the run ends there: no value
	// was read off stdin and none was stored, and the command says which of the
	// two it is.
	if len(writes.created) != 0 || len(writes.added) != 0 {
		t.Errorf("a token was stored in a coding agent's session: created %v, added %v", writes.created, writes.added)
	}
	if err == nil {
		t.Fatalf("the command reported success after declaring an account it cannot finish:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), accountsAddAgent) || !strings.Contains(err.Error(), "fugaro accounts add") {
		t.Errorf("the refusal names neither the marker nor the command to run at a terminal: %v", err)
	}
	if strings.Contains(out, "Nothing else to run") {
		t.Errorf("the command ended on its ready line, which is only true of a converged installation:\n%s", out)
	}
}

// The record follows the write and never precedes it: the gate admits the
// document, files nothing at all while the command has written nothing, and
// files the one admission when — and only when — the command asks it to, after
// its own write (D1014, spec §6, M6's helper). The other half of that rule is
// the no-change re-run below, which records nothing because it wrote nothing.
func TestAccountsAddManagedFilesTheAdmissionOnlyAfterTheWrite(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	lc := r.addLocal(t)
	doc := []byte("version: 1\n")
	t.Setenv(accountsAddAgent, "1")

	m, gate, err := accountsAddGate(t.Context(), gateCmd(), lc, accountsAddOptions{yes: true}, "team-a", "standard", nil, doc)
	if err != nil {
		t.Fatalf("a bounded declaration was refused: %v", err)
	}
	if !m.Managed || !m.Admitted || m.Fallback {
		t.Fatalf("the gate answered %+v, want an admitted document in a managed project", m)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Fatalf("the admission was filed before the command wrote anything: %+v", recs)
	}
	if err := gate.recorded(t.Context()); err != nil {
		t.Fatalf("recording the admission: %v", err)
	}
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("after the write the day holds %d records, want exactly one", len(recs))
	}
	if recs[0].PlanDigest != gateDigest(doc) {
		t.Errorf("the record's digest is %q, want the sha256 of the document the gate judged", recs[0].PlanDigest)
	}
	// One admission is one record, however many times the caller asks.
	if err := gate.recorded(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.audits(t)); n != 1 {
		t.Errorf("the day holds %d records after the admission was recorded twice, want 1", n)
	}
}

// The widening: a declaration whose allow_data_to names a repository beyond the
// checkout the command runs in is refused BY THE GATE, before anything is
// written, and the refusal is recorded like every other. This is D868's
// widening — the sentence the owner's own confirmation carries — and what the
// managed case may not do is answer it on the owner's behalf.
func TestAccountsAddManagedRefusesAWideningByTheGate(t *testing.T) {
	r := newManagedAddRig(t, managedAddLocalWide(t), addMarksManaged)
	writes := r.secretWrites(t)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a,acme/other", "--yes")
	if err == nil {
		t.Fatalf("a widening was applied in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the widening was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "acme/other") {
		t.Errorf("the refusal does not name the repository the token would reach: %v", err)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed under a refused widening:\n%s", after)
	}
	if len(writes.created) != 0 || len(writes.added) != 0 {
		t.Errorf("a refused widening stored a token: created %v, added %v", writes.created, writes.added)
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
	if recs[0].AgentMarker != accountsAddAgent {
		t.Errorf("the refused record's agent_marker is %q, want the session's own marker", recs[0].AgentMarker)
	}
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "acme/other") }) {
		t.Errorf("the refused record's rules (%v) do not name what refused: %v", recs[0].Rules, err)
	}
}

// The token entry: --claude runs Anthropic's login and then reads a token, and
// no gate admits a token. It is refused BY THE GATE, before the login is
// screened and before anything is written, so a run that asked for one leaves
// the file exactly as it was.
func TestAccountsAddManagedRefusesClaudeByTheGate(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	if err == nil {
		t.Fatalf("a run carrying a token entry was admitted:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the token entry was not refused by the gate: %v", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the refusal does not say that a token is what it refused: %v", err)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed under a refused token entry:\n%s", after)
	}
	if strings.Contains(out, "setup-token") {
		t.Errorf("the login was screened or run for a step the gate refused:\n%s", out)
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// The pasted token: the other half of "no gate admits a token". A session that
// pipes a value at the hidden prompt is refused at that step — the declaration
// above it stands, because the gate admitted the declaration — and the value is
// read by nobody, so nothing is stored and nothing can be.
func TestAccountsAddManagedDoesNotReadAPastedToken(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	writes := r.secretWrites(t)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	if err == nil {
		t.Fatalf("a token entry was read and applied in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), accountsAddAgent) {
		t.Errorf("the refusal does not say which step and which session it refused: %v", err)
	}
	if len(writes.created) != 0 || len(writes.added) != 0 {
		t.Errorf("the piped value was stored: created %v, added %v", writes.created, writes.added)
	}
	// The declaration is the whole of what the gate admitted, and it stands.
	lc := r.addLocal(t)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the admitted declaration", got)
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Errorf("the admitted declaration left %+v, want exactly one admitted record", recs)
	}
}

// A command that applied nothing records nothing: a rotation re-run — the
// account already declared and the profile already serving it — makes both
// edits no-ops, so applySettingsEdit returns before it writes a byte. The gate
// still admitted the step, and a record saying "admitted" about a write that
// never happened is worse than no record at all (D1014).
func TestAccountsAddManagedRecordsNothingWhenThereIsNoWrite(t *testing.T) {
	r := newManagedAddRig(t, rotationLocal(t), addMarksManaged)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	if err == nil {
		t.Fatalf("a rotation re-run in a coding agent's session reported success:\n%s", out)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed on a re-run that had nothing to write:\n%s", after)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("the command filed %+v for a write it never made", recs)
	}
}

// An installation that is not managed keeps today's refusal exactly: the
// declaration is not written, no record is filed — D1025 writes down nothing
// about a project that is not managed — and the sentence is the one this
// command has always used.
func TestAccountsAddUnmanagedKeepsTheRefusalInAnAgentSession(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksNone)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	if err == nil {
		t.Fatalf("the command wrote in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal is not this command's own: %v", err)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed in a coding agent's session:\n%s", after)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("an unmanaged project filed %+v", recs)
	}
}

// A mark read that does not answer fails closed: one half of the fact is not
// the fact, so the project is not managed, the command refuses as it always
// did, and nothing is recorded. The wrong answer here would be the admitting
// one, and this is the test that makes it the refusing one.
func TestAccountsAddManagedUnreadMarksKeepTheRefusal(t *testing.T) {
	for name, unread := range map[string]func(t *testing.T) *addRig{
		"the creation fact cannot be read": func(t *testing.T) *addRig {
			r := newManagedAddRig(t, addRigLocal, addMarksManaged)
			r.gcs.RefusePath("managed.json", 500, "InternalServerError", "", "the creation fact did not answer")
			return r
		},
		"the project carries no label": func(t *testing.T) *addRig {
			return newManagedAddRig(t, addRigLocal, addMarksUnlabelled)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := unread(t)
			before := readFileString(t, r.cfg)
			t.Setenv(accountsAddAgent, "1")

			out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
			if err == nil {
				t.Fatalf("a project this program cannot prove is managed was admitted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal is not this command's own: %v", err)
			}
			if after := readFileString(t, r.cfg); after != before {
				t.Errorf("the file changed under a mark that could not be read:\n%s", after)
			}
			if recs := r.audits(t); len(recs) != 0 {
				t.Errorf("a project that is not managed filed %+v", recs)
			}
		})
	}
}

// A run that asks nothing pays nothing: --yes answers the declaration itself,
// so the helper reads no mark at all. Nothing asks for the creation fact
// object, and a project whose marks are stale, unreadable or on another machine
// cannot slow down or stop a run that was never going to be asked.
func TestAccountsAddYesReadsNoMark(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	lc := r.addLocal(t)

	// The helper's own boundary is where this is asked, and it has to be: the
	// command's remaining path reads the marks for its own reasons — planning
	// the installation asks whether the project is managed, and that read is a
	// stage's and not this gate's — so a count of the whole command's calls
	// could not tell the two apart. What is this row's to hold is that the gate
	// itself asks nothing here, and that is a count of the calls the helper made.
	m, gate, err := accountsAddGate(t.Context(), gateCmd(), lc, accountsAddOptions{yes: true}, "team-a", "standard", []string{"acme/other"}, []byte("version: 1\n"))
	if err != nil {
		t.Fatalf("a run with --yes was refused by the gate: %v", err)
	}
	if m != (initflow.Managed{}) {
		t.Errorf("a run with --yes was answered %+v, want the zero value every fence reads exactly as it did before this row", m)
	}
	if err := gate.recorded(t.Context()); err != nil {
		t.Fatalf("recording an admission nobody read: %v", err)
	}
	if reqs := r.gcs.Requests(); len(reqs) != 0 {
		t.Errorf("a run with --yes paid %d calls to read marks it skips no confirmation by:\n%v", len(reqs), reqs)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a run with --yes filed %+v", recs)
	}

	// And the command with that same flag: it converges, and it leaves no
	// record — the half of D1025 an owner can check in their own bucket.
	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a")
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the owner's own run did not converge:\n%s", out)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a run with --yes filed %+v", recs)
	}
}

// The owner's own terminal in a managed project is the second session this
// command reads the gate for (the ruling of 2026-11-01): the declaration's y/N
// is a question the gate can answer, so the marks are read exactly where that
// question would be asked — a real terminal, without --yes — and the gate's
// verdict on the document replaces it (applySettingsEdit's own gate arm).
//
// The control is the same call, the same terminal and a project the marks do
// not prove is managed: the read is made and answers nothing, the answer is the
// zero value, and the y/N is asked exactly as it always was. The mutation
// caught: Managed.Removes answering true for every project.
func TestAccountsAddManagedOwnerTerminalReadsTheGate(t *testing.T) {
	for _, tc := range []struct {
		what    string
		marks   managedAddMarks
		managed bool
	}{
		{"the project is managed", addMarksManaged, true},
		{"the project is unproven", addMarksUnlabelled, false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedAddRig(t, addRigLocal, tc.marks)
			lc := r.addLocal(t)
			noAgentSession(t)
			fakeTerminal(t)

			m, _, err := accountsAddGate(t.Context(), gateCmd(), lc, accountsAddOptions{}, "team-a", "standard", nil, []byte("version: 1\n"))
			if err != nil {
				t.Fatalf("a person's own terminal was refused by the gate: %v", err)
			}
			if tc.managed {
				if !m.Managed || !m.Admitted {
					t.Errorf("a managed project's own terminal was answered %+v, want the admission that stands in for the y/N", m)
				}
				if len(r.gcs.Requests()) == 0 {
					t.Error("a managed project's own terminal was not read for at all, so the y/N below is still asked")
				}
				return
			}
			if m != (initflow.Managed{}) {
				t.Errorf("a project the marks do not prove is managed was answered %+v, want the zero value that keeps the y/N", m)
			}
			if recs := r.audits(t); len(recs) != 0 {
				t.Errorf("a project that is not managed filed %+v", recs)
			}
		})
	}
}
