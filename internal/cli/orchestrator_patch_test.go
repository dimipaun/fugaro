package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// orchestratorPerson is the email the fixture's own credentials belong to: the
// name an approval is recorded with. Tests replace the seam, never the network.
const orchestratorPerson = "person@example.com"

// asPerson makes the run's own credentials a person's, the way a real
// installation's ADC is.
func asPerson(t *testing.T) {
	t.Helper()
	old := authenticatedUser
	authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {
		return orchestratorPerson, nil
	}
	t.Cleanup(func() { authenticatedUser = old })
}

// patchDoc is the patch a run hands the command, built by the same encoder the
// format's readers use.
func patchDoc(t *testing.T, rev int64, ops ...orchestrator.Op) string {
	t.Helper()
	raw, err := json.Marshal(orchestrator.Patch{BaseRev: rev, Ops: ops})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// patchFile writes a patch document where the command reads it.
func patchFile(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// goalPatch is the simplest op only a person may land.
func goalPatch(t *testing.T, rev int64, goal string) string {
	t.Helper()
	return patchDoc(t, rev, orchestrator.Op{Op: orchestrator.OpGoalSet, Goal: &orchestrator.GoalOp{Goal: goal}})
}

// personDecision is a question only a person may close.
func personDecision(id string) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
		ID: id, Question: "which repository do we cut first?", WaitingOn: orchestrator.OnPerson,
	}}
}

// TestPersonOpsNeedATypedConfirmation is O5's red step and the whole of the
// person-class fence at the CLI (G2, D1225): a patch that closes a person's
// decision, sets the goal or approves a work item lands only after a real
// terminal has typed the project's name, and a session that has no terminal
// writes nothing at all.
func TestPersonOpsNeedATypedConfirmation(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	s := seedOrchestratorState(t, f, personDecision("pick-a-repo"))
	doc := patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: "pick-a-repo", Answer: "the web one"}})
	path := patchFile(t, doc)

	// No terminal: refused, and the decision is still open.
	out, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("a patch that closes a person's decision landed with no terminal:\n%s", out)
	}
	if !strings.Contains(err.Error(), "real terminal") {
		t.Errorf("the refusal does not say what it needs: %v", err)
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the run printed a confirmation nobody could answer:\n%s", out)
	}
	if d, _ := storedOrchestratorState(t, f).Decision("pick-a-repo"); d.Resolved() {
		t.Fatal("the decision was closed by a run with no terminal")
	}

	// A real terminal, the project's name typed: the person's own word lands
	// the patch, and the answer is recorded as a person's.
	typed(t)
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the confirmation was not shown:\n%s", out)
	}
	// What the person types their project's name over is the write: the
	// question and the answer that lands, and not only the op's kind.
	if !strings.Contains(out, "which repository do we cut first?") || !strings.Contains(out, "the web one") {
		t.Errorf("the confirmation does not show the question and the answer it closes the decision with:\n%s", out)
	}
	d, ok := storedOrchestratorState(t, f).Decision("pick-a-repo")
	if !ok || !d.Resolved() {
		t.Fatal("the confirmed patch closed nothing")
	}
	if d.By != orchestrator.Person {
		t.Errorf("the decision was closed by a %s, and only a person may close it", d.By)
	}

	// A typed line that is not the project's name confirms nothing.
	s = storedOrchestratorState(t, f)
	doc = patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpGoalSet, Goal: &orchestrator.GoalOp{Goal: "ship slice one"}})
	path = patchFile(t, doc)
	if _, _, err := runOrchestrator(t, "yes\n", "orchestrator", "patch", "-f", path); err == nil {
		t.Fatal("a typed line that is not the project's name confirmed the patch")
	}
	if storedOrchestratorState(t, f).Goal != "" {
		t.Error("the goal was set by an unconfirmed patch")
	}

	// --json is not a terminal either: an agent's own output shape never
	// stands in for a person's word.
	if _, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path, "--json"); err == nil {
		t.Fatal("--json confirmed a person's op")
	}
	if storedOrchestratorState(t, f).Goal != "" {
		t.Error("the goal was set by a --json run")
	}
}

// digestAfter is the digest of an item as the patch under test leaves it: the
// upsert applied first, as a session, so the test computes what the command
// computes rather than spelling a digest out.
func digestAfter(t *testing.T, s orchestrator.State, up orchestrator.Op, id string) orchestrator.Digest {
	t.Helper()
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{up}},
		orchestrator.Actor{Class: orchestrator.Session, Name: "session", Now: orchestratorTestNow})
	if err != nil {
		t.Fatalf("applying the upsert: %v", err)
	}
	w, ok := next.Item(id)
	if !ok {
		t.Fatalf("the upsert left no item %s", id)
	}
	return orchestrator.SpecDigest(w)
}

// TestThePersonConfirmationShowsTheGoalThatLands: setting the goal is a
// person's op as well, and the line the person reads before typing their
// project's name is the goal itself — that it is a goal is the op's kind, and
// the person is the only one who can say whether the text is what they meant.
func TestThePersonConfirmationShowsTheGoalThatLands(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	typed(t)
	s := seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))
	path := patchFile(t, goalPatch(t, s.Rev, "ship slice one and nothing else"))

	out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "ship slice one and nothing else") {
		t.Errorf("the confirmation does not show the goal it sets:\n%s", out)
	}
	if got := storedOrchestratorState(t, f).Goal; got != "ship slice one and nothing else" {
		t.Errorf("the goal that landed is %q", got)
	}
}

// TestTheConfirmationShowsEveryOpThePatchCarries: there is one actor per
// document, so the typed confirmation raises the whole patch to Person, and the
// ops that ride along with a person's own land under that person's authority
// and attribution. The person is therefore shown all of them, in the order they
// are applied, with the ops that need the person's own word marked: closing a
// question the daemon was asked is a person's to do (Apply lets a person close
// any decision), and a person who did not see it in the list answered nothing.
func TestTheConfirmationShowsEveryOpThePatchCarries(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	typed(t)
	// A question waiting on the daemon and an item: neither is an op only a
	// person may land, and both are what a patch built by a session carries.
	s := seedOrchestratorState(t, f,
		workItemUpsert("web-fix", "fix the flaky test"),
		orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
			ID: "daemon-stuck", Question: "which branch should the retry cut from?", WaitingOn: orchestrator.OnDaemon,
		}})
	doc := patchDoc(t, s.Rev,
		orchestrator.Op{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{
			ID: "daemon-stuck", Answer: "cut from main and say so in the note"}},
		orchestrator.Op{Op: orchestrator.OpFactSet, Fact: &orchestrator.FactOp{Key: "branch", Value: "release/1.2"}},
		orchestrator.Op{Op: orchestrator.OpGoalSet, Goal: &orchestrator.GoalOp{Goal: "ship slice one"}})
	path := patchFile(t, doc)

	out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"which branch should the retry cut from?",
		"cut from main and say so in the note",
		"setting fact branch = release/1.2",
		"ship slice one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not show %q:\n%s", want, out)
		}
	}
	// The order the ops are listed in is the order they are applied in.
	closed, goal := strings.Index(out, "closing decision"), strings.Index(out, "setting the goal")
	if closed < 0 || goal < 0 || closed > goal {
		t.Errorf("the confirmation does not list the ops in the order they are applied:\n%s", out)
	}
	// Which ops need the person's own word is marked, and which merely ride
	// along is not, so the person can tell what the confirmation is for.
	if !strings.Contains(out, "[person-only] setting the goal") {
		t.Errorf("the confirmation does not mark the op that needs the person's own word:\n%s", out)
	}
	if strings.Contains(out, "[person-only] closing decision") {
		t.Errorf("the confirmation marks a question the daemon was asked as an op only a person may land:\n%s", out)
	}
	// All of it landed as that person, the ops that ride along included.
	after := storedOrchestratorState(t, f)
	if after.Goal != "ship slice one" {
		t.Errorf("the goal that landed is %q", after.Goal)
	}
	if d, _ := after.Decision("daemon-stuck"); !d.Resolved() || d.By != orchestrator.Person || d.Answer != "cut from main and say so in the note" {
		t.Errorf("the daemon's question is %+v, and the answer that closed it is the person's own", d)
	}
	if got := after.Facts["branch"]; got.Value != "release/1.2" || got.By != orchestrator.Person {
		t.Errorf("the fact that landed is %+v, and a fact this patch set is attributed to the actor that landed it", got)
	}
	if !strings.Contains(out, "as a person") {
		t.Errorf("the write is not reported as the person's:\n%s", out)
	}
}

// TestPatchFromAnAgentSessionRefusesPersonOps: a coding agent's session is
// refused the person's word even at a real terminal — the accident G2 is
// written against. It is a mitigation and not a barrier (P1's negative
// answer), which is exactly why the typed confirmation above is what the fence
// rests on.
func TestPatchFromAnAgentSessionRefusesPersonOps(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	typed(t)
	clearAgentMarkers(t)
	t.Setenv("CLAUDECODE", "1")
	s := seedOrchestratorState(t, f, personDecision("pick-a-repo"))
	path := patchFile(t, goalPatch(t, s.Rev, "ship slice one"))

	out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("an agent's session landed a person's op:\n%s", out)
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the run asked a coding agent's session to confirm:\n%s", out)
	}
	if storedOrchestratorState(t, f).Goal != "" {
		t.Error("an agent's session set the goal")
	}

	// The same run with no marker in the environment is the person's, which is
	// what makes the marker a mitigation: whoever can unset it is outside it.
	clearAgentMarkers(t)
	if _, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path); err != nil {
		t.Fatalf("the person's own run was refused: %v", err)
	}
	if storedOrchestratorState(t, f).Goal == "" {
		t.Error("the person's own run set nothing")
	}
}

// TestPatchRejectsAStaleRev: a patch built against another revision is refused
// rather than landed on top of it (G9, F5), and the node is left as it was.
func TestPatchRejectsAStaleRev(t *testing.T) {
	f := newOrchestratorFixture(t)
	s := seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))
	path := patchFile(t, patchDoc(t, s.Rev-1, orchestrator.Op{Op: orchestrator.OpFactSet,
		Fact: &orchestrator.FactOp{Key: "branch", Value: "main"}}))

	out, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("a patch from a stale revision landed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "revision") {
		t.Errorf("the refusal does not name the revision: %v", err)
	}
	if after := storedOrchestratorState(t, f); after.Rev != s.Rev || len(after.Facts) != 0 {
		t.Errorf("the refused patch wrote something: rev %d, %d facts", after.Rev, len(after.Facts))
	}

	// The same patch at the revision the node is really at lands.
	path = patchFile(t, patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpFactSet,
		Fact: &orchestrator.FactOp{Key: "branch", Value: "main"}}))
	out, _, err = runOrchestrator(t, "", "orchestrator", "patch", "-f", path, "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Kind  string `json:"kind"`
		Rev   int64  `json:"rev"`
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "patch" || doc.Rev != s.Rev+1 || doc.Actor != string(orchestrator.Session) {
		t.Errorf("the document says %+v, and a session's patch landed at rev %d", doc, s.Rev+1)
	}
	if after := storedOrchestratorState(t, f); after.Rev != s.Rev+1 {
		t.Errorf("the node is at rev %d after a patch from rev %d", after.Rev, s.Rev)
	}
}

// TestApproveShowsTheSpecItDigests: the confirmation shows the spec the
// approval binds — every field SpecDigest covers and not only the digest — and
// the approval is written with that digest and the person's own name. The
// approve op is also the one op whose payload the person has to be shown: an
// opaque digest is not an approval of anything, and a person who types their
// project's name over a digest alone has attested to nothing they could read.
func TestApproveShowsTheSpecItDigests(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	typed(t)
	s := seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))
	item, ok := s.Item("web-fix")
	if !ok {
		t.Fatal("the fixture has no item")
	}
	digest := orchestrator.SpecDigest(item)
	path := patchFile(t, patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpWorkItemApprove,
		Approve: &orchestrator.ApproveOp{ID: "web-fix", SpecDigest: digest}}))

	out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Every digested field: the person reads the repository, the recipe and the
	// rigor tier they are about to launch a run for, and the brief the run is
	// handed, and not an opaque digest alone.
	for _, want := range []string{
		"web-fix", string(digest),
		"title: fix the flaky test",
		"repo acme/repo, recipe code, rigor small",
		"brief: make the change fix the flaky test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not show %q:\n%s", want, out)
		}
	}
	approved, _ := storedOrchestratorState(t, f).Item("web-fix")
	if approved.Approved == nil {
		t.Fatal("the approval was not written")
	}
	if approved.Approved.SpecDigest != digest || approved.Approved.By != orchestratorPerson {
		t.Errorf("the approval is %+v, and it is the approved digest under the person's own name", approved.Approved)
	}

	// A patch that rewrites the item and approves it in the same document: the
	// digest is the one the rewrite produces, so what the person is shown has to
	// be the spec that digest binds and not the one the node still holds —
	// otherwise the person attests to a spec by reading another one. Nothing is
	// hidden behind the title either: a rewrite can change where the run goes
	// and what it is told to do, and both are what the person is approving.
	before := storedOrchestratorState(t, f)
	up := orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID: "web-fix", Title: strPtr("the title the person is approving"),
		Repo: strPtr("acme/other-repo"), Brief: strPtr("delete the production database"),
	}}
	digest = digestAfter(t, before, up, "web-fix")
	path = patchFile(t, patchDoc(t, before.Rev, up, orchestrator.Op{Op: orchestrator.OpWorkItemApprove,
		Approve: &orchestrator.ApproveOp{ID: "web-fix", SpecDigest: digest}}))
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"title: the title the person is approving",
		"repo acme/other-repo",
		"brief: delete the production database",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not show %q, the rewritten spec the digest binds:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"fix the flaky test", "acme/repo", "make the change"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the confirmation shows %q, the spec the node held before the patch:\n%s", unwanted, out)
		}
	}
	if w, _ := storedOrchestratorState(t, f).Item("web-fix"); w.Approved == nil || w.Approved.SpecDigest != digest {
		t.Errorf("the approval that landed is %+v, and it is the rewritten spec's", w.Approved)
	}

	// A digest that is not the item's is refused, and the person is not asked
	// to confirm a spec that is not the one stored.
	cur := storedOrchestratorState(t, f)
	path = patchFile(t, patchDoc(t, cur.Rev, orchestrator.Op{Op: orchestrator.OpWorkItemApprove,
		Approve: &orchestrator.ApproveOp{ID: "web-fix", SpecDigest: orchestrator.SpecDigest(orchestrator.WorkItem{ID: "other"})}}))
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("an approval of another spec landed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("the refusal does not say the spec is not the item's: %v", err)
	}
	if after := storedOrchestratorState(t, f); after.Rev != cur.Rev {
		t.Error("the refused approval wrote something")
	}
}

// TestPatchNeverActsAsTheDaemon: the bookkeeping only the daemon writes is
// refused from a session, and nothing a caller can set — a flag, an
// environment variable, a field in the document — makes this command the
// daemon or the decision agent (G1, O5's interface).
func TestPatchNeverActsAsTheDaemon(t *testing.T) {
	f := newOrchestratorFixture(t)
	typed(t)
	s := seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))

	// A phase is the daemon's, whoever asks for it.
	phase := orchestrator.PhaseRunning
	doc := patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID: "web-fix", Phase: &phase,
	}})
	path := patchFile(t, doc)
	out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("a session wrote a work item's phase:\n%s", out)
	}
	if !strings.Contains(err.Error(), "phase") {
		t.Errorf("the refusal does not name what a session may not write: %v", err)
	}
	if item, _ := storedOrchestratorState(t, f).Item("web-fix"); item.Phase != orchestrator.PhaseQueued {
		t.Errorf("the item's phase is %q after a refused patch", item.Phase)
	}

	// An environment variable that names a class changes nothing: the class is
	// the command's own, and a fact records which one wrote it.
	for _, name := range []string{"FUGARO_ORCHESTRATOR_ACTOR", "FUGARO_ACTOR", "ORCHESTRATOR_ACTOR"} {
		t.Setenv(name, "daemon")
	}
	t.Setenv("FUGARO_ORCHESTRATOR_ROLE", "daemon")
	path = patchFile(t, patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpFactSet,
		Fact: &orchestrator.FactOp{Key: "branch", Value: "main"}}))
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "as a session") {
		t.Errorf("the run does not say which class wrote:\n%s", out)
	}
	after := storedOrchestratorState(t, f)
	if got := after.Facts["branch"].By; got != orchestrator.Session {
		t.Errorf("the fact was written by a %s, and the command acts as a session", got)
	}

	// No flag names one either: an argument the tree does not know is refused
	// rather than guessed at.
	if _, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path, "--actor", "daemon"); err == nil {
		t.Error("--actor daemon was accepted, and no flag selects the class")
	}
}

// TestPatchReadsTheDocumentStrictlyAndRefusesTheRest: the format is the schema
// O0 pins, and a document that is not one is refused before anything is read
// from the node.
func TestPatchReadsTheDocumentStrictlyAndRefusesTheRest(t *testing.T) {
	f := newOrchestratorFixture(t)
	s := seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"an unknown field", `{"base_rev":` + orchNum(int(s.Rev)) + `,"ops":[],"who":"me"}`},
		{"an unknown op", `{"base_rev":` + orchNum(int(s.Rev)) + `,"ops":[{"op":"state_replace"}]}`},
		{"no document at all", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := patchFile(t, tc.doc)
			out, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path)
			if err == nil {
				t.Fatalf("the patch landed:\n%s", out)
			}
			if after := storedOrchestratorState(t, f); after.Rev != s.Rev {
				t.Errorf("the refused patch wrote something: rev %d", after.Rev)
			}
		})
	}

	// -f is the document's own source, and a run with none says so.
	out, _, err := runOrchestrator(t, "", "orchestrator", "patch")
	if err == nil {
		t.Fatalf("a patch with no document returned success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "-f") {
		t.Errorf("the refusal does not name the flag the document comes from: %v", err)
	}
}
