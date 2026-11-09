package cli

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// accounts_managed.go's tests: `fugaro accounts add` in a managed project (the
// managed plan's §2 accounts handoff row, M6's one helper).
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
// The owner's ruling of 2026-11-09 adds the other door, and the tests for it
// are the same shape. A managed project asks its owner for this step and
// nobody else, so the red step there is the owner's own run CONVERGING after
// the one confirmation — which no build that refuses every caller can do — and
// the refusals are every caller that is not that person: a marker, a pipe, a
// run with no terminal, --yes, a terminal on stdin but not on stdout, and an
// answer that does not name the project. Each of those is what one term of
// accountsAddOwnTerminal answers for, and the table below names the term so a
// build with that term dropped fails on the case it would admit.
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
	r.managed = marks == addMarksManaged
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

// The two refusals this file's cases read exactly, held as the sentences
// themselves rather than as fragments: the owner's ruling names two short plain
// lines, and a fragment assertion would pass on a paragraph that happened to
// contain them.
const (
	// accountsAddRefusalRun is what a caller that is not the owner's own
	// terminal and is not a coding agent's session reads — a pipe, a run with
	// no terminal, --yes.
	accountsAddRefusalRun = "This adds and stores the token of Claude account team-a, and a managed project asks its owner for that.\n" +
		"Run this in your own terminal."
	// accountsAddRefusalSession is the same step asked for by a session: there
	// is a narrower step the session itself can take, and the second line is it.
	accountsAddRefusalSession = "This adds and stores the token of Claude account team-a, and a managed project asks its owner for that.\n" +
		"Run this in your own terminal, not in an agent session."
	// accountsAddRefusalWidening is a declaration that would send the token
	// beyond the checkout the command runs in.
	accountsAddRefusalWidening = "This would send the token of Claude account team-a to acme/other, beyond this checkout.\n" +
		"Name only this checkout's repository, or run without --repos."
	// accountsAddRefusalToken is the step no gate admits, reached by a session
	// the gate admitted the declaration to.
	accountsAddRefusalToken = "No token was stored for Claude account team-a: a session may write the declaration, and never the token.\n" +
		"Run this in your own terminal, not in an agent session."
)

// fakeOutputTerminal is the other half of fakeTerminal for the commands whose
// owner's door is a real terminal on BOTH streams: the confirmation prints the
// repositories the token may be sent to, and a person who cannot read them is
// not being asked. A test that is about the owner's own run replaces both.
func fakeOutputTerminal(t *testing.T) {
	t.Helper()
	old := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = old })
}

// ownerAtTheTerminal is the owner's own session: no coding agent's marker, a
// real terminal on the run's input and on its output.
func ownerAtTheTerminal(t *testing.T) {
	t.Helper()
	noAgentSession(t)
	fakeTerminal(t)
	fakeOutputTerminal(t)
}

// ownerAddStdin is what the owner types at this command's two reads, in the
// order the command makes them: the project's own name at the one confirmation
// this step has, and then the token.
func ownerAddStdin() string { return initProjectName + "\n" + addToken() + "\n" }

// gateCmdStdin is gateCmd with this run's input: the helper's boundary tests
// call the gate directly, and the confirmation reads the command's own stdin.
// Without it the command would read the test binary's own, which is green on a
// developer's terminal and blocked in CI.
func gateCmdStdin(in string) *cobra.Command {
	cmd := gateCmd()
	cmd.SetIn(strings.NewReader(in))
	return cmd
}

// The red step: in a managed project's coding agent session, the declaration
// is written and its admission is recorded, where every refusal this command
// made before the gate existed refused to write the file at all. The run
// carries no token — nothing declares one, and no gate admits one — so what it
// leaves behind is the declaration, the record of the gate's verdict, and the
// two lines naming the one step left for the owner's own terminal.
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
	// was read off stdin and none was stored, and the command says so in the
	// same two lines every other refusal of this file uses.
	if len(writes.created) != 0 || len(writes.added) != 0 {
		t.Errorf("a token was stored in a coding agent's session: created %v, added %v", writes.created, writes.added)
	}
	if err == nil {
		t.Fatalf("the command reported success after declaring an account it cannot finish:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if err.Error() != accountsAddRefusalToken {
		t.Errorf("the session's refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalToken)
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

	m, gate, err := accountsAddGate(t.Context(), gateCmd(), lc, accountsAddOptions{yes: true}, "team-a", "standard", nil, nil, doc)
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
// checkout the command runs in is refused in a coding agent's session, before
// anything is written, and the refusal is recorded. This is D868's widening —
// the sentence the owner's own confirmation carries — and what the managed case
// may not do is answer it on the owner's behalf.
func TestAccountsAddManagedRefusesAWideningInAnAgentSession(t *testing.T) {
	r := newManagedAddRig(t, managedAddLocalWide(t), addMarksManaged)
	writes := r.secretWrites(t)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a,acme/other", "--yes")
	if err == nil {
		t.Fatalf("a widening was applied in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if err.Error() != accountsAddRefusalWidening {
		t.Errorf("the widening's refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalWidening)
	}
	// No rule number reaches the person reading it: the design's vocabulary is
	// the record's and the code's, and the two lines are what to do next.
	for _, rule := range []string{"D868", "D1026", "D1012"} {
		if strings.Contains(err.Error(), rule) {
			t.Errorf("the refusal prints the rule number %s: %v", rule, err)
		}
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
// no gate admits a token. It is refused in a coding agent's session before the
// login is screened and before anything is written, so a run that asked for one
// leaves the file exactly as it was.
func TestAccountsAddManagedRefusesClaudeInAnAgentSession(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	if err == nil {
		t.Fatalf("a run carrying a token entry was admitted:\n%s", out)
	}
	if err.Error() != accountsAddRefusalSession {
		t.Errorf("the token entry's refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalSession)
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
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "no plan is a token") }) {
		t.Errorf("the refused record's rules (%v) do not name the rule that refused", recs[0].Rules)
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
	if err.Error() != accountsAddRefusalToken {
		t.Errorf("the refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalToken)
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

// A caller that is not a person at a terminal and is not a session either — a
// pipe, a run with no terminal, or the same command with --yes — is refused on
// a managed project before a byte is written. It is the half of this row that
// had no fence at all: the hidden token prompt is fed by a pipe as readily as
// by a hand, so before the owner's ruling of 2026-11-09 a run could store the
// token of an account a managed project is owed a question about.
//
// --yes is the case worth naming: it is a flag, and a flag answers nothing
// here. It never satisfies the confirmation, so a scripted `accounts add
// --yes` on a managed project is refused exactly as a bare pipe is.
func TestAccountsAddManagedRefusesARunThatAnswersNothing(t *testing.T) {
	for _, tc := range []struct {
		what  string
		local func(*testing.T) string
		args  []string
		want  string
	}{
		{
			what:  "--yes",
			local: func(*testing.T) string { return addRigLocal },
			args:  []string{"--repos", "owner/repo-a", "--yes"},
			want:  accountsAddRefusalRun,
		},
		{
			what:  "a pipe with no flag",
			local: func(*testing.T) string { return addRigLocal },
			args:  []string{"--repos", "owner/repo-a"},
			want:  accountsAddRefusalRun,
		},
		{
			// The widening's own fixture, which admits acme/other: the subset
			// rule runs before the gate, and a declaration this run could not
			// make at all would be refused by that rule and never reach the
			// door this file is about.
			what:  "--yes and a widening",
			local: managedAddLocalWide,
			args:  []string{"--repos", "owner/repo-a,acme/other", "--yes"},
			want: "This would send the token of Claude account team-a to acme/other, beyond this checkout.\n" +
				"Run this in your own terminal.",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedAddRig(t, tc.local(t), addMarksManaged)
			writes := r.secretWrites(t)
			before := readFileString(t, r.cfg)
			noAgentSession(t)
			noTerminal(t)

			out, _, err := executeStdin(t, addToken()+"\n", append([]string{"accounts", "add", "team-a"}, tc.args...)...)
			if err == nil {
				t.Fatalf("a run with nobody at the terminal was admitted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if err.Error() != tc.want {
				t.Errorf("the refusal is:\n%q\nwant:\n%q", err.Error(), tc.want)
			}
			if after := readFileString(t, r.cfg); after != before {
				t.Errorf("the file changed for a run nobody answered:\n%s", after)
			}
			if len(writes.created) != 0 || len(writes.added) != 0 {
				t.Errorf("the token a pipe delivered was stored: created %v, added %v", writes.created, writes.added)
			}
			// A run nobody answered is not a verdict of the gate either: the
			// gate judged no document, so the day holds no record of it
			// (D1014 — the record is the gate's).
			if recs := r.audits(t); len(recs) != 0 {
				t.Errorf("a run nobody answered filed %+v", recs)
			}
			if strings.Contains(out, "Nothing else to run") {
				t.Errorf("the refused run ended on the command's ready line:\n%s", out)
			}
		})
	}
}

// The one door this step has on a managed project, and the four terms that make
// it the owner's own: a coding agent's marker, --yes, a terminal on the run's
// input, and a terminal on its output. Each case below is one term dropped —
// the mutation each catches is named with it — and each asserts the two lines
// and the state the run left behind, so a build that admitted the case fails on
// the file rather than on the wording alone.
//
// The marker case is the one that ends differently, and deliberately: a session
// is not refused the declaration (D1012 admits a bounded one), so what it reads
// is the token step's own two lines and what it leaves behind is the
// declaration. A build with the marker term dropped would ask that session for
// the confirmation and converge, which is the failure this case catches.
func TestAccountsAddManagedAdmitsOnlyTheOwnerAtTheirOwnTerminal(t *testing.T) {
	for _, tc := range []struct {
		what   string
		mutate string
		setup  func(t *testing.T)
		want   string
		// declared is whether the declaration is written before the refusal:
		// the session's case is the only one where it is.
		declared bool
	}{
		{
			what:     "a coding agent's marker at a real terminal",
			mutate:   "accountsAddOwnTerminal without its marker term",
			setup:    func(t *testing.T) { ownerAtTheTerminal(t); t.Setenv(accountsAddAgent, "1") },
			want:     accountsAddRefusalToken,
			declared: true,
		},
		{
			what:   "--yes at a real terminal",
			mutate: "accountsAddOwnTerminal without its --yes term",
			setup:  ownerAtTheTerminal,
			want:   accountsAddRefusalRun,
		},
		{
			what:   "a terminal on the run's input only",
			mutate: "accountsAddOwnTerminal without its writerIsTerminal term",
			setup: func(t *testing.T) {
				noAgentSession(t)
				fakeTerminal(t)
				old := writerIsTerminal
				writerIsTerminal = func(io.Writer) bool { return false }
				t.Cleanup(func() { writerIsTerminal = old })
			},
			want: accountsAddRefusalRun,
		},
		{
			what:   "no terminal at all",
			mutate: "accountsAddOwnTerminal without its stdinIsTerminal term",
			setup:  func(t *testing.T) { noAgentSession(t); noTerminal(t); fakeOutputTerminal(t) },
			want:   accountsAddRefusalRun,
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedAddRig(t, addRigLocal, addMarksManaged)
			writes := r.secretWrites(t)
			before := readFileString(t, r.cfg)
			tc.setup(t)

			args := []string{"accounts", "add", "team-a", "--repos", "owner/repo-a"}
			if strings.Contains(tc.what, "--yes") {
				args = append(args, "--yes")
			}
			// The owner's answer is on stdin for every case: the run that is
			// admitted is admitted because of the terminal and not because a
			// line happened to be there, and the run that is refused is refused
			// where it stands.
			out, _, err := executeOwnerStdin(t, ownerAddStdin(), args...)
			if err == nil {
				t.Fatalf("a run that is not the owner at their own terminal was admitted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if err.Error() != tc.want {
				t.Errorf("the refusal is:\n%q\nwant:\n%q", err.Error(), tc.want)
			}
			if len(writes.created) != 0 || len(writes.added) != 0 {
				t.Errorf("the token of a run that is not the owner's was stored: created %v, added %v", writes.created, writes.added)
			}
			lc := r.addLocal(t)
			declared := !slices.Equal(lc.ClaudeAccounts["team-a"].AllowDataTo, []string(nil))
			if declared != tc.declared {
				t.Errorf("the declaration was written = %v, want %v: claude_accounts.team-a = %+v", declared, tc.declared, lc.ClaudeAccounts["team-a"])
			}
			if !tc.declared && readFileString(t, r.cfg) != before {
				t.Errorf("the file changed for a run that was refused:\n%s", readFileString(t, r.cfg))
			}
		})
	}
}

// The owner's own terminal is admitted on a managed project after the one
// confirmation this step has: the repositories the token may be sent to, the
// sentence saying what that means, and the project's own name typed at the
// terminal. The run then converges like any other first-account run — the
// declaration, the token, the policy, the job — so the red step here is the
// installation being ready, which no build that refuses every caller can reach.
//
// Nothing is recorded and nothing is refused: the record is the GATE's verdict,
// and this step is the owner's, answered at the door D1552 and D1555 name.
func TestAccountsAddManagedOwnerTerminalIsAskedWhatTheTokenMayBeUsedBy(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	writes := r.secretWrites(t)
	ownerAtTheTerminal(t)

	out, _, err := executeOwnerStdin(t, ownerAddStdin(), "accounts", "add", "team-a", "--repos", "owner/repo-a")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused: %v\nstdout:\n%s", err, out)
	}
	// The confirmation names the repositories and what they mean, in the words
	// the ruling gives: a job on these repositories can read this token.
	wantConfirm := "⚠ CONFIRM (project " + initProjectName + "): claude_accounts.team-a.allow_data_to = [owner/repo-a], and a job on these repositories can read this token"
	if !strings.Contains(out, wantConfirm) {
		t.Errorf("the confirmation does not print the repositories and the sentence:\n%s", out)
	}
	if !strings.Contains(out, "Type "+initProjectName+" to confirm:") {
		t.Errorf("the run did not ask for the project's own name:\n%s", out)
	}
	if lc := r.addLocal(t); !slices.Equal(lc.ClaudeAccounts["team-a"].AllowDataTo, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the declared repository", lc.ClaudeAccounts["team-a"].AllowDataTo)
	}
	if len(writes.created) == 0 && len(writes.added) == 0 {
		t.Errorf("the owner's own run stored no token: created %v, added %v", writes.created, writes.added)
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the owner's own run did not converge:\n%s", out)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("the owner's own step filed %+v: the record is the gate's verdict, and this step is the owner's", recs)
	}
}

// --ask keeps its two promises at the owner's own terminal, which is the only
// session this command's managed case admits: the line saying the prompts of a
// managed project are asked here instead, and the question the flag restores.
//
// Both halves are asserted against their opposites. The line must not be the one
// for a project whose marks did not answer "managed" — this project's marks did,
// and a sentence about another project would be this command telling the owner
// something false about the file it is about to write — and the question must be
// a question: with nothing typed for it the run stops, writes nothing and stores
// nothing, which a build that printed a line about a question would pass.
func TestAccountsAddManagedOwnerTerminalKeepsTheAskPromise(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	writes := r.secretWrites(t)
	before := readFileString(t, r.cfg)
	ownerAtTheTerminal(t)

	// The name answers the confirmation this step asks for the token; nothing is
	// left for the question --ask restores.
	out, _, err := executeOwnerStdin(t, initProjectName+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--ask")
	if err == nil {
		t.Fatalf("--ask asked for nothing and the run went through:\n%s", out)
	}
	if !strings.Contains(out, initflow.AskRestored()) {
		t.Errorf("--ask did not say the prompts of this managed project are asked here instead:\n%s", out)
	}
	if strings.Contains(out, initflow.AskUnmanaged()) {
		t.Errorf("--ask said the marks of this project did not answer managed, and they do:\n%s", out)
	}
	if !strings.Contains(out, "make this change? [y/N]:") {
		t.Errorf("--ask restored no question for the write it covers:\n%s", out)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("an unanswered --ask question changed the file:\n%s", after)
	}
	if len(writes.created) != 0 || len(writes.added) != 0 {
		t.Errorf("an unanswered --ask question stored a token: created %v, added %v", writes.created, writes.added)
	}
}

// The confirmation refused: the question is asked and the answer is not the
// project's name, so the step stops there — before the login, before the diff
// is applied and before the token is read. The run that answers nothing at all
// is the same refusal, which is what makes the typed name a confirmation rather
// than a formality.
func TestAccountsAddManagedOwnerTerminalRefusesWithoutTheAnswer(t *testing.T) {
	for _, tc := range []struct {
		what string
		feed string
	}{
		{"another line typed", "yes\n"},
		{"nothing typed", ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedAddRig(t, addRigLocal, addMarksManaged)
			writes := r.secretWrites(t)
			before := readFileString(t, r.cfg)
			ownerAtTheTerminal(t)

			out, _, err := executeStdin(t, tc.feed, "accounts", "add", "team-a", "--repos", "owner/repo-a")
			if err == nil {
				t.Fatalf("a run that did not answer the confirmation converged:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			// The question was asked — the run reached the door and stopped at
			// the answer, which is the half a refusal alone could not show.
			if !strings.Contains(out, "Type "+initProjectName+" to confirm:") {
				t.Errorf("the run did not ask the confirmation it refused:\n%s", out)
			}
			if !strings.Contains(err.Error(), "nothing was changed") {
				t.Errorf("the refusal does not say the run changed nothing: %v", err)
			}
			if after := readFileString(t, r.cfg); after != before {
				t.Errorf("the file changed for an unanswered confirmation:\n%s", after)
			}
			if len(writes.created) != 0 || len(writes.added) != 0 {
				t.Errorf("an unanswered confirmation stored a token: created %v, added %v", writes.created, writes.added)
			}
			if recs := r.audits(t); len(recs) != 0 {
				t.Errorf("an unanswered confirmation filed %+v", recs)
			}
		})
	}
}

// The widening at the owner's own terminal, which is the door it has. A
// widening is D868's question and only the owner can answer it, so at that
// terminal the confirmation is asked and the declaration is written; the same
// run in a coding agent's session is refused, because a session cannot answer
// for a repository the run cannot see (TestAccountsAddManagedRefusesAWideningInAnAgentSession).
func TestAccountsAddManagedOwnerTerminalWidensTheDeclaration(t *testing.T) {
	r := newManagedAddRig(t, managedAddLocalWide(t), addMarksManaged)
	writes := r.secretWrites(t)
	ownerAtTheTerminal(t)

	out, _, err := executeOwnerStdin(t, ownerAddStdin(), "accounts", "add", "team-a", "--repos", "owner/repo-a,acme/other")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused the widening it is the door to: %v\nstdout:\n%s", err, out)
	}
	lc := r.addLocal(t)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a", "acme/other"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the declaration this run asked for", got)
	}
	// The confirmation carries the whole list, both repositories, because the
	// list is what the owner is answering for.
	wantConfirm := "claude_accounts.team-a.allow_data_to = [owner/repo-a, acme/other], and a job on these repositories can read this token"
	if !strings.Contains(out, wantConfirm) {
		t.Errorf("the confirmation does not name every repository the token may be sent to:\n%s", out)
	}
	if len(writes.created) == 0 && len(writes.added) == 0 {
		t.Errorf("the owner's own run stored no token: created %v, added %v", writes.created, writes.added)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("the owner's own step filed %+v", recs)
	}
}

// The other class the door covers is the token entry: --claude runs a login and
// then reads a token, which is the step no plan gate admits (accountsAddRules),
// so in a managed project the flag has to work at the owner's own terminal —
// after the same confirmation, and only there. The control is the gate's own
// boundary, which answers "managed and not admitted" for that run: the caller
// behind it asks the question --ask restores and writes on the confirmation the
// owner answered, never on a verdict — the admission is the flag GateConfirms
// reads, and it is false — and the login below is the one the value never comes
// from.
func TestAccountsAddManagedOwnerTerminalRunsTheClaudeLogin(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	lc := r.addLocal(t)
	ownerAtTheTerminal(t)

	m, gate, err := accountsAddGate(t.Context(), gateCmdStdin(initProjectName+"\n"), lc,
		accountsAddOptions{claude: true}, "team-a", "standard", nil, []string{"owner/repo-a"}, []byte("version: 1\n"))
	if err != nil {
		t.Fatalf("--claude at the owner's own terminal was refused: %v", err)
	}
	if want := (initflow.Managed{Managed: true}); m != want {
		t.Errorf("--claude at the owner's own terminal was answered %+v, want %+v: the project IS managed — every sentence about it is decided under that — and no gate admits the token entry, so the admission stays false", m, want)
	}
	if err := gate.recorded(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("--claude at the owner's own terminal filed %+v", recs)
	}
	// And the same call in a coding agent's session, which is the control: the
	// gate refuses it and records the refusal, so the door is the terminal and
	// not the flag.
	t.Setenv(accountsAddAgent, "1")
	_, _, err = accountsAddGate(t.Context(), gateCmd(), lc,
		accountsAddOptions{claude: true}, "team-a", "standard", nil, nil, []byte("version: 1\n"))
	if err == nil || err.Error() != accountsAddRefusalSession {
		t.Fatalf("--claude in a coding agent's session was not refused in this command's own two lines: %v", err)
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the session's refusal left %+v, want exactly one refused record", recs)
	}
}
