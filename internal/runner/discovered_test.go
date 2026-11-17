package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// discoveredName is the file the brief names (internal/runner/discovered.go's
// discoveredFile), spelled here rather than imported: the name is the
// interface between the sentence the agent reads and the path the reader
// opens, and a rename of either half has to fail this file rather than follow
// it.
const discoveredName = "discovered.json"

// writeState writes a file into the run's own state directory, which is what
// the brief tells the agent to do and the only place the run reads a
// discovered block from.
func writeState(body string) func(t *testing.T, req agent.Request) {
	return func(t *testing.T, req agent.Request) {
		t.Helper()
		p := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), discoveredName)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDiscoveredFileCopiedIntoRecord: what the agent writes where the brief
// says — the run's own state directory, outside the checkout — is on the
// record the run ends with, entry by entry, with the run's own words as the
// reason and each text bounded by the record's own bound. The run that wrote
// it is a run that succeeded like any other: the file is a note about the
// graph, and reading it may never fail a run (E2, D2709).
//
// Mutation (read the file from the checkout instead of the state directory):
// TestDiscoveredFileInCheckoutIgnored fails, and this one finds nothing.
// Mutation (copy the lists through unbounded): the clipped reason this test
// measures comes back at its written length. Mutation (copy them through
// runstore.NewDiscovered without redacting them): the value the file echoes
// out of the run's own environment comes back in the clear.
func TestDiscoveredFileCopiedIntoRecord(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	// redacted is the word every redaction writes in a value's place.
	const redacted = "[REDACTED]"
	long := strings.Repeat("why ", runstore.MaxDiscoveredText)
	body := `{"blocks":[` +
		`{"plan_task":"E3","reason":"the fold reads what this run writes"},` +
		`{"path":"internal/runner/discovered.go","reason":"` + long + `"},` +
		`{"plan_task":"E3","reason":"the key is ` + secret + `"}],` +
		`"needed":[{"plan_task":"X2","reason":"the record's own shapes"}]}`
	rec, err := h.run(t, then(implement("feature"), writeState(body)), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the run ended %s (%s), want a run that succeeded: a discovered file never fails one", rec.Status, rec.Reason)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := stored.Discovered
	if d == nil {
		t.Fatalf("the record carries no discovered block: %+v", stored)
	}
	if want := (runstore.DiscoveredItem{PlanTask: "E3", Reason: "the fold reads what this run writes"}); len(d.Blocks) != 3 || d.Blocks[0] != want {
		t.Fatalf("blocks = %+v, want the three entries the file named, in its order", d.Blocks)
	}
	if it := d.Blocks[1]; it.Path != "internal/runner/discovered.go" || len([]rune(it.Reason)) != runstore.MaxDiscoveredText || !strings.HasPrefix(long, it.Reason) {
		t.Fatalf("the second block entry = %+v, want the path entry with its reason clipped to %d runes", it, runstore.MaxDiscoveredText)
	}
	if want := (runstore.DiscoveredItem{PlanTask: "X2", Reason: "the record's own shapes"}); len(d.Needed) != 1 || d.Needed[0] != want {
		t.Fatalf("needed = %+v, want the entry the file named", d.Needed)
	}
	// The block is published text like everything else the run writes: an
	// agent that echoed a value out of its own environment into a reason does
	// not put it on the record in the clear.
	if got := d.Blocks[2]; got.Reason != "the key is "+redacted {
		t.Fatalf("the third block entry = %+v, want the value in its reason redacted", got)
	}
	if raw, err := json.Marshal(stored.Discovered); err != nil || strings.Contains(string(raw), secret) {
		t.Fatalf("the record's block carries the value in the clear (%v): %s", err, raw)
	}
	if !reflect.DeepEqual(rec.Discovered, d) {
		t.Fatalf("the run's own record carries %+v and the stored one %+v", rec.Discovered, d)
	}

	// A file an earlier execution left in the state directory is not this
	// run's note, even though the directory is operator-chosen and outlives
	// one run: bootstrap clears Fugaro's own entries there (clearStateDir), so
	// this run — which writes none — records none rather than another's.
	h = newHarness(t, "", nil)
	if err := os.MkdirAll(h.deps.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(h.deps.StateDir, discoveredName)
	if err := os.WriteFile(stale, []byte(`{"blocks":[{"plan_task":"X0","reason":"left by an earlier execution"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err = h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Discovered != nil {
		t.Fatalf("the record carries %+v, want nothing: it is the note an earlier execution left in the state directory", rec.Discovered)
	}
}

// TestDiscoveredFileInCheckoutIgnored: a file of that name inside the checkout
// is not the run's own note, even when the branch tracks it — here the stage
// plants it and the implement stage's `git add -A` commits it, so the file
// really is in the tree and on the pushed head. The reader opens the state
// directory's path and no other, so the record carries nothing, and a base
// branch that planted such a file has told this run nothing about the graph
// (E2, D2709; F41).
func TestDiscoveredFileInCheckoutIgnored(t *testing.T) {
	h := newHarness(t, "", nil)
	body := `{"blocks":[{"plan_task":"E3","reason":"planted in the checkout"}]}`
	plant := func(t *testing.T, _ context.Context, req agent.Request) (agent.Result, error) {
		testutil.WriteFiles(t, req.Dir, map[string]string{discoveredName: body})
		return agent.Result{}, nil
	}
	rec, err := h.run(t, plant, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, h.remote, "show", rec.HeadSHA+":"+discoveredName); got != body {
		t.Fatalf("the checkout's %s is %q on the pushed head, want the planted file: this test is only about a tracked file if it is one", discoveredName, got)
	}
	if rec.Discovered != nil {
		t.Fatalf("the record carries the checkout's file: %+v", rec.Discovered)
	}
}

// TestMalformedDiscoveredDropped: a file that is not the object the brief asks
// for is dropped with one log line, and the run ends exactly as the same run
// without it would — succeeded and ready, with no block on the record. Each
// case also says which line it is dropped by, so the guard that dropped it is
// the one the case is about rather than whichever guard happens to fail first:
// the shape is never improvised — an entry a reader could not place, a key the
// brief never named, a second object after the first, and a path that is a
// directory or a link are all the same answer (E2, D2709; F41).
//
// Mutation (drop the regular-file check from readDiscovered): the directory
// case's line becomes the decode's, not the one that says what the path is,
// and its assertion fails. Mutation (open the path without O_NOFOLLOW): the
// link case's block — a valid object of the right shape, at a path the agent
// chose — lands on the record.
func TestMalformedDiscoveredDropped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, req agent.Request)
		// want is the one log line the case leaves, as the reader writes it:
		// the guard that dropped the file, rather than a decode that failed on
		// what that guard had already refused.
		want string
	}{
		{"not JSON", writeState("this is not JSON at all\n"), "is not the JSON object the brief asks for"},
		{"an array where the object goes", writeState(`[{"plan_task":"E3","reason":"x"}]`), "is not the JSON object the brief asks for"},
		{"a key the brief never named", writeState(`{"blocks":[{"plan_task":"E3","reason":"x","why":"because"}]}`), "is not the JSON object the brief asks for"},
		{"a second object after the first", writeState(`{"blocks":[{"plan_task":"E3","reason":"x"}]}{"blocks":[]}`), "carries more than the one JSON object"},
		{"a directory under that name", func(t *testing.T, req agent.Request) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), discoveredName), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "is not a regular file"},
		{"a link to a valid block elsewhere", func(t *testing.T, req agent.Request) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "elsewhere.json")
			if err := os.WriteFile(target, []byte(`{"blocks":[{"plan_task":"E3","reason":"a file the link points at"}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), discoveredName)); err != nil {
				t.Fatal(err)
			}
		}, "reading " + discoveredName + " failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			var logs bytes.Buffer
			h.deps.Log = runner.NewLogger(&logs)
			rec, err := h.run(t, then(implement("feature"), tc.write), review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("the run ended %s/%s (%s), want the succeeded/ready run it would have been without the file", rec.Status, rec.Outcome, rec.Reason)
			}
			if rec.Discovered != nil {
				t.Fatalf("the record carries a block read from a file no reader can use: %+v", rec.Discovered)
			}
			lines := 0
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, discoveredName) {
					lines++
				}
			}
			if lines != 1 {
				t.Fatalf("%d log lines name %s, want the one line that says it was dropped:\n%s", lines, discoveredName, logs.String())
			}
			if !strings.Contains(logs.String(), tc.want) {
				t.Fatalf("no log line says %q:\n%s", tc.want, logs.String())
			}
		})
	}
}

// TestConflictPathsRecorded: a rebase that stops on another task's work on one
// path records that path (E2, D2710), whether or not a stage goes on to
// resolve it — the fact worth keeping is that the graph the branch was
// launched from had no edge between the two, and a conflict somebody resolved
// by hand is that fact as surely as one nobody could. The record holds each
// path once, in the order the run met it.
//
// Mutation (record the paths from the refusal alone, inside the bound's
// branch): the resolved case carries nothing.
func TestConflictPathsRecorded(t *testing.T) {
	setup := func(t *testing.T) *fuHarness {
		t.Helper()
		h := followUpHarness(t, "", nil, commitOnly("feature"), review("ship", 0))
		// The base moves on with its own version of the file the branch added:
		// the same path, different content, which is a conflict for every
		// commit the rebase replays.
		commitToRemote(t, h.harness, func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"feature.txt": "the base's side\n"})
		})
		h.followUpRecipe(t, followID, runID, "Put it back on main.", rebaseTask(t))
		return h
	}

	t.Run("a conflict no stage resolves", func(t *testing.T) {
		h := setup(t)
		rec, err := h.run(t, leaveTheConflict, leaveTheConflict, leaveTheConflict)
		if err != nil || rec.Status != runstore.StatusFailed {
			t.Fatalf("rec = %+v, err = %v; want a refused rebase", rec, err)
		}
		if len(rec.ConflictPaths) != 1 || rec.ConflictPaths[0] != "feature.txt" {
			t.Fatalf("conflict paths = %v, want the one path git stopped on", rec.ConflictPaths)
		}
	})

	t.Run("a conflict a stage resolves", func(t *testing.T) {
		h := setup(t)
		rec, err := h.run(t, resolveTheConflict)
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v; want the rebased branch pushed", rec, err)
		}
		if len(rec.ConflictPaths) != 1 || rec.ConflictPaths[0] != "feature.txt" {
			t.Fatalf("conflict paths = %v, want the one path git stopped on", rec.ConflictPaths)
		}
	})
}

// TestPromptNamesTheDiscoveredPath: every stage that writes code tells the
// agent where to leave what it found out about the order of the work — the
// implement stage and the fix rounds alike, because both are the coder with
// the same brief. The path is the run's own state directory, which is outside
// the checkout the agent commits from, so the note can never be one the run's
// own `git add -A` picks up (E2, D2709; F41).
func TestPromptNamesTheDiscoveredPath(t *testing.T) {
	h := newHarness(t, "", nil)
	prompts := map[string]string{}
	note := func(s step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			t.Helper()
			rec, err := h.store.ReadRecord(ctx)
			if err != nil {
				t.Fatal(err)
			}
			prompts[rec.Stage] = req.AppendSystemPrompt
			return s(t, ctx, req)
		}
	}
	// The review round that answers with changes buys the fix stage, which is
	// the other stage that writes code.
	if _, err := h.run(t, note(implement("feature")), note(review("changes", 1)), note(idle), note(review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.deps.StateDir, discoveredName)
	for _, stage := range []string{"implement", "fix"} {
		sys := prompts[stage]
		if !strings.Contains(sys, path) {
			t.Errorf("the %s stage's brief does not name %s:\n%s", stage, path, sys)
		}
		if !strings.Contains(sys, "outside your checkout") {
			t.Errorf("the %s stage's brief does not say the file is outside the checkout:\n%s", stage, sys)
		}
	}
	if rel, err := filepath.Rel(h.deps.WorkDir, h.deps.StateDir); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("the state directory %s is not outside the checkout %s (%q, %v)", h.deps.StateDir, h.deps.WorkDir, rel, err)
	}
}
