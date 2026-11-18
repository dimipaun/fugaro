package cli

// fugaro orchestrator patch, approving an issue item (G4, D2741): the person
// sees the issue before they approve it.
//
// An item the daemon's issue ingest wrote holds the issue's id and a hash of its
// body, and never the body (D2720), so the only place the body can reach the
// person is here — the read is their own gh, read-only, and the approval lands
// only when what the issue says now hashes to what the item pins. The tests
// drive the real command tree, and the fake `gh` records its argv, so "the call
// was a read" is a claim about the process that ran and not about a log line.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// issueItemSeed writes one issue item as the daemon: `external` and the body's
// hash are the ingest's own fields, and no other class may write them (D2728),
// so an issue item is seeded through the daemon's own upsert and not through the
// session seedOrchestratorState makes. An empty bodySHA leaves the hash off,
// which is the item that pins no body at all.
func issueItemSeed(t *testing.T, f *budgetFixture, id, external, bodySHA string) orchestrator.State {
	t.Helper()
	s := seedOrchestratorState(t, f)
	op := workItemUpsert(id, "the issue's title as the ingest wrote it")
	op.WorkItem.External = strPtr(external)
	if bodySHA != "" {
		op.WorkItem.BodySHA256 = strPtr(bodySHA)
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{op}},
		orchestrator.Actor{Class: orchestrator.Daemon, Name: "daemon", Now: orchestratorTestNow})
	if err != nil {
		t.Fatalf("seeding the issue item: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(orchestratorPath(t, orchestrator.StateNode), json.RawMessage(raw))
	return next
}

// issueGHReply is a fake `gh` answering `issue view --json title,body`, built
// with the same encoder the reader decodes with so the fixture cannot disagree
// with it about escaping. The JSON is handed over in a heredoc: a body is an
// issue author's text and carries quotes as readily as anything else, so it is
// never spliced into a shell word.
func issueGHReply(t *testing.T, title, body string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	return "cat <<'FUGARO_ISSUE_JSON'\n" + string(raw) + "\nFUGARO_ISSUE_JSON\n"
}

// issueGHCalls is what the fake `gh` was run as: one line per invocation, its
// argv as the wrapper recorded it, or nothing when it never ran.
func issueGHCalls(t *testing.T, calls string) []string {
	t.Helper()
	raw, err := os.ReadFile(calls)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// noApproveGH makes the machine's gh not the one this command looks for: a
// person's computer without gh is the case the refusal names.
func noApproveGH(t *testing.T) {
	t.Helper()
	old := approveIssueGH
	approveIssueGH = "gh-that-is-not-installed-here"
	t.Cleanup(func() { approveIssueGH = old })
}

// TestApproveIssueItemShowsBodyAsData is G4's own test for D2741: approving an
// item with a GitHub external reads the issue through the person's own gh,
// prints its title and body fenced as data with the body's SHA-256, and lands
// the approval only when that hash is the item's. The person reads the body
// before they are asked to type, and a run with no terminal reads nothing at
// all — the existing refusal stands, and it stands before any process runs.
func TestApproveIssueItemShowsBodyAsData(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	// The body is the issue author's text: two lines, so the fence has to keep a
	// line break inside it rather than let it forge a line of its own.
	body := "the test passes on my machine\nthe CI runner fails it every third run"
	sum := sha256Hex([]byte(body))
	s := issueItemSeed(t, f, "issue-1", "github:acme/repo#12", sum)
	calls := testutil.FakeCommand(t, "gh", issueGHReply(t, "the flaky test", body))
	path := patchFile(t, patchDoc(t, s.Rev, planApprove(t, s, "backlog", "issue-1")))

	// No terminal: the refusal the fence already gives, and gh never runs.
	out, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path)
	if err == nil {
		t.Fatalf("a run with no terminal approved an issue item:\n%s", out)
	}
	if !strings.Contains(err.Error(), "real terminal") {
		t.Errorf("the refusal does not say what it needs, and the fence's own refusal is what stands: %v", err)
	}
	if got := issueGHCalls(t, calls); got != nil {
		t.Errorf("a run with no terminal read the issue: %v", got)
	}
	if w, _ := storedOrchestratorState(t, f).Item("issue-1"); w.Approved != nil {
		t.Fatal("a run with no terminal approved the item")
	}

	// A real terminal and the project's name: the issue is on the screen, fenced
	// as data, with the hash the item pins, and the approval lands as the person.
	typed(t)
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"github:acme/repo#12, as your own gh reads it now",
		issueFenceOpen, issueFenceClose,
		"title: the flaky test",
		"the test passes on my machine",
		"the CI runner fails it every third run",
		"body (sha256 " + sum + ")",
		"[person-only] approving the items of backlog",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not show %q:\n%s", want, out)
		}
	}
	// The body is read before the person is asked to type: what the typed line
	// confirms has to be on the screen first.
	fenced, asked := strings.Index(out, issueFenceClose), strings.Index(out, "⚠ CONFIRM")
	if fenced < 0 || asked < 0 || fenced > asked {
		t.Errorf("the issue is not on the screen before the confirmation is asked for:\n%s", out)
	}
	// The one process this spawned is the read, and it is the person's own gh
	// asking one question about one repository.
	got := issueGHCalls(t, calls)
	if len(got) != 1 || got[0] != "issue view 12 --repo github.com/acme/repo --json title,body" {
		t.Errorf("the read was %v, and the issue is read once through gh issue view", got)
	}
	w, _ := storedOrchestratorState(t, f).Item("issue-1")
	if w.Approved == nil || w.Approved.By != orchestratorPerson {
		t.Fatalf("the item carries %+v, and the confirmed patch approves it as the person", w.Approved)
	}
}

// TestApproveIssueItemRefusesChangedBody is the other half of D2741: an item
// whose issue no longer hashes to what the item pins is refused with the fix
// printed, and nothing is written — the approval would otherwise be of a body
// the issue does not say, which is exactly what the pin exists to prevent.
func TestApproveIssueItemRefusesChangedBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		// pinned is the hash the item carries, "" for an item that pins none.
		pinned string
		// body is what the issue says now; noGH is a machine with no gh at all.
		body string
		noGH bool
		// hashes is whether the refusal is about the two hashes, which is the
		// case only where there is an issue read to compare one with the other.
		hashes bool
		// want is what the refusal has to name: the reason and the fix.
		want []string
	}{
		{
			name:   "the issue's body changed",
			pinned: sha256Hex([]byte("the body the ingest saw")),
			body:   "the body somebody edited since",
			hashes: true,
			want:   []string{"changed since", "re-sync"},
		},
		{
			name: "the item pins no body",
			body: "the issue's body",
			want: []string{"pins no body hash", "re-sync"},
		},
		{
			name:   "the machine has no gh",
			pinned: sha256Hex([]byte("the issue's body")),
			body:   "the issue's body",
			noGH:   true,
			want:   []string{"not on this machine's PATH"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			asPerson(t)
			typed(t)
			s := issueItemSeed(t, f, "issue-1", "github:acme/repo#12", tc.pinned)
			if tc.noGH {
				noApproveGH(t)
			} else {
				testutil.FakeCommand(t, "gh", issueGHReply(t, "the flaky test", tc.body))
			}
			path := patchFile(t, patchDoc(t, s.Rev, planApprove(t, s, "backlog", "issue-1")))

			out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path)
			if err == nil {
				t.Fatalf("the approval landed although the issue could not be checked:\n%s", out)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say that nothing was written: %v", err)
			}
			want := tc.want
			if tc.hashes {
				// Both hashes are named, so the person can see what moved.
				want = append(want, tc.pinned, sha256Hex([]byte(tc.body)))
			}
			for _, w := range want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal does not name %q: %v", w, err)
				}
			}
			after := storedOrchestratorState(t, f)
			if after.Rev != s.Rev {
				t.Errorf("the refused approval wrote something: rev %d, and the node was at %d", after.Rev, s.Rev)
			}
			if w, _ := after.Item("issue-1"); w.Approved != nil {
				t.Fatalf("the refused approval carries %+v", w.Approved)
			}
		})
	}
}

// TestApproveIssueItemMakesNoWrite: what approving an issue item runs is a
// read. `gh issue view` is the whole of it — no close, no comment, no edit, no
// API call of this command's own — and the read alone does not touch the state:
// a run whose typed line is not the project's name leaves the node at the
// revision it read, having read the issue on the way.
func TestApproveIssueItemMakesNoWrite(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	body := "the issue's own text"
	s := issueItemSeed(t, f, "issue-1", "github:acme/repo#12", sha256Hex([]byte(body)))
	calls := testutil.FakeCommand(t, "gh", issueGHReply(t, "the flaky test", body))
	typed(t)
	path := patchFile(t, patchDoc(t, s.Rev, planApprove(t, s, "backlog", "issue-1")))

	// A typed line that is not the project's name: the issue was read, and the
	// node is untouched.
	if _, _, err := runOrchestrator(t, "yes\n", "orchestrator", "patch", "-f", path); err == nil {
		t.Fatal("a typed line that is not the project's name confirmed the patch")
	}
	if after := storedOrchestratorState(t, f); after.Rev != s.Rev {
		t.Errorf("the unconfirmed run wrote the node: rev %d, and it read %d", after.Rev, s.Rev)
	}

	got := issueGHCalls(t, calls)
	if len(got) != 1 {
		t.Fatalf("the run spawned %v, and one read is the whole of what approving an issue item runs", got)
	}
	line := got[0]
	if !strings.HasPrefix(line, "issue view ") {
		t.Errorf("the call is %q, and it is an issue read", line)
	}
	for _, write := range []string{" close", " comment", " create", " edit", " delete", " reopen", " api "} {
		if strings.Contains(line, write) {
			t.Errorf("the call writes to GitHub: %q", line)
		}
	}
	if !strings.Contains(line, "--json title,body") || !strings.Contains(line, "--repo github.com/acme/repo") {
		t.Errorf("the call is %q, and the read names the fields and the repository it is about", line)
	}
}
