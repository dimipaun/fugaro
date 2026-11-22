package runstore

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestPauseShapesAreStable pins the pause contract's shapes: the word a paused
// run ends with, the two stored shapes (pause.json and the database node), the
// path the node lives at, the action the service names, and the member names
// the wire and the documents carry.
//
// They are pinned because they are the one thing three sides agree on without
// importing each other: the job writes pause.json and the node's path is read
// by the orchestrator and the views, while the node itself is written by the
// credentials service and read by both. A member renamed here, or a version
// changed, is a change to docs/model-credentials-routing/contract-pause.md and
// to every side at once — which is what this test is for: it fails at the
// contract, before a reader of another release meets the shape it cannot read.
func TestPauseShapesAreStable(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		if StatusPaused != "paused" {
			t.Errorf("StatusPaused is %q, want %q: the word is the plan's and every reader of a status spells it that way", StatusPaused, "paused")
		}
		// The execution that ends paused has ended: the runner releases its
		// lease and exits (R12). The word is a fifth status and not a reuse of
		// running, so Terminal's rule already holds for it.
		if !StatusPaused.Terminal() {
			t.Error("StatusPaused is not terminal: a paused execution has ended")
		}
	})

	t.Run("record", func(t *testing.T) {
		s := newStore(t)
		p := PauseRecord{
			Version:    PauseVersion,
			Stage:      "implement",
			Session:    "1a2b3c4d-5e6f-4a8b-9c0d-1e2f3a4b5c6d",
			Checkpoint: "3f1a2b4c5d6e7f8091a2b3c4d5e6f708192a3b4c",
		}
		if err := s.WritePause(ctx, p); err != nil {
			t.Fatalf("WritePause: %v", err)
		}
		raw, err := s.ReadFile(ctx, PauseFile)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", PauseFile, err)
		}
		pinMembers(t, PauseFile, raw, []string{"checkpoint", "session", "stage", "version"})

		got, err := s.ReadPause(ctx)
		if err != nil {
			t.Fatalf("ReadPause: %v", err)
		}
		if *got != p {
			t.Errorf("ReadPause read %+v, want %+v", *got, p)
		}

		if _, err := s.Sibling(runID + "-2").ReadPause(ctx); !errors.Is(err, ErrNotFound) {
			t.Errorf("ReadPause of a run that never suspended: %v, want ErrNotFound", err)
		}
	})

	t.Run("record-refusals", func(t *testing.T) {
		s := newStore(t)
		valid := PauseRecord{
			Version:    PauseVersion,
			Stage:      "implement",
			Session:    "1a2b3c4d-5e6f-4a8b-9c0d-1e2f3a4b5c6d",
			Checkpoint: "3f1a2b4c5d6e7f8091a2b3c4d5e6f708192a3b4c",
		}
		// The writer refuses what a reader would: a member this release cannot
		// place, a version it does not know, and the three values it needs.
		for _, c := range []struct {
			name string
			mut  func(*PauseRecord)
		}{
			{"version", func(p *PauseRecord) { p.Version = PauseVersion + 1 }},
			{"stage", func(p *PauseRecord) { p.Stage = "" }},
			{"session", func(p *PauseRecord) { p.Session = "not-a-session" }},
			{"checkpoint", func(p *PauseRecord) { p.Checkpoint = "" }},
			// A ref name is the value a hostile job would write: it passes a
			// reader that only asks whether the member is there, and it reaches
			// the resumed run's checkout, which is safe only against a commit.
			{"checkpoint-ref", func(p *PauseRecord) { p.Checkpoint = "main" }},
		} {
			bad := valid
			c.mut(&bad)
			if err := s.WritePause(ctx, bad); !errors.Is(err, ErrInvalidPause) {
				t.Errorf("WritePause with a bad %s: %v, want ErrInvalidPause", c.name, err)
			}
		}

		// A record this reader does not know the version of is refused with the
		// version it could not read, which is what the run's outcome names
		// (R18). The record itself is written by hand here: the writer above
		// would refuse it, and the point is what a reader of another release's
		// record does.
		future, err := json.Marshal(map[string]any{
			"version":    PauseVersion + 1,
			"stage":      "implement",
			"session":    valid.Session,
			"checkpoint": valid.Checkpoint,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PutFile(ctx, PauseFile, future, "application/json"); err != nil {
			t.Fatal(err)
		}
		got, err := s.ReadPause(ctx)
		if !errors.Is(err, ErrInvalidPause) || !strings.Contains(err.Error(), "version") {
			t.Fatalf("ReadPause of a record from a later release: %v, want ErrInvalidPause naming the version", err)
		}
		if got == nil || got.Version != PauseVersion+1 || got.Stage != "implement" {
			t.Errorf("ReadPause returned %+v beside its error, want the record it could read so the outcome can name what it was", got)
		}

		// A member this release does not know is refused rather than dropped:
		// it is a record written by a release this image is not, and guessing
		// at half of it is what R18 forbids.
		unknown, err := json.Marshal(map[string]any{
			"version":    PauseVersion,
			"stage":      "implement",
			"session":    valid.Session,
			"checkpoint": valid.Checkpoint,
			"round":      3,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PutFile(ctx, PauseFile, unknown, "application/json"); err != nil {
			t.Fatal(err)
		}
		// The refusal carries the same sentinel a bad version does, so the
		// outcome R18 names is the one name a runner can ask for with
		// errors.Is whichever of the three it met.
		if _, err := s.ReadPause(ctx); !errors.Is(err, ErrInvalidPause) {
			t.Errorf("ReadPause of a record carrying an unknown member: %v, want ErrInvalidPause", err)
		}
	})

	t.Run("node", func(t *testing.T) {
		n := PauseNode{
			Launcher:      "112233445566778899",
			Pool:          "team",
			ResumeAt:      1_800_000_000_000,
			Chain:         runID,
			PausedSeconds: 1800,
		}
		raw, err := n.Bytes()
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		pinMembers(t, "the node", raw, []string{"chain", "launcher", "paused_seconds", "pool", "resume_at"})

		got, err := ParsePauseNode(raw)
		if err != nil {
			t.Fatalf("ParsePauseNode: %v", err)
		}
		if *got != n {
			t.Errorf("ParsePauseNode read %+v, want %+v", *got, n)
		}

		// A cancelled pause keeps every other member: the mark stops the
		// resume, it does not erase the pause (D3197).
		cancelled := n
		cancelled.Cancelled = true
		raw, err = cancelled.Bytes()
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		pinMembers(t, "a cancelled node", raw, []string{"cancelled", "chain", "launcher", "paused_seconds", "pool", "resume_at"})
		if got, err = ParsePauseNode(raw); err != nil || !got.Cancelled {
			t.Errorf("ParsePauseNode of a cancelled node: %+v, %v, want the mark", got, err)
		}
	})

	t.Run("node-refusals", func(t *testing.T) {
		valid := PauseNode{Launcher: "112233445566778899", ResumeAt: 1_800_000_000_000, Chain: runID}
		for _, c := range []struct {
			name string
			mut  func(*PauseNode)
		}{
			{"launcher", func(n *PauseNode) { n.Launcher = "" }},
			{"resume_at", func(n *PauseNode) { n.ResumeAt = 0 }},
			{"chain", func(n *PauseNode) { n.Chain = "" }},
			{"paused_seconds", func(n *PauseNode) { n.PausedSeconds = -1 }},
		} {
			bad := valid
			c.mut(&bad)
			if _, err := bad.Bytes(); !errors.Is(err, ErrInvalidPauseNode) {
				t.Errorf("Bytes with a bad %s: %v, want ErrInvalidPauseNode", c.name, err)
			}
		}

		// A member this release does not know is refused, as the record's is:
		// the node is an input to the orchestrator, and it resumes nothing it
		// could not read.
		if _, err := ParsePauseNode([]byte(`{"launcher":"112233445566778899","resume_at":1800000000000,"chain":"20260926-221530-abcd","resumed_by":"20260926-221530-abce"}`)); err == nil {
			t.Error("ParsePauseNode read a node carrying an unknown member")
		}
		if _, err := ParsePauseNode([]byte(`{"launcher":"112233445566778899","resume_at":1800000000000,"chain":"20260926-221530-abcd"} {}`)); err == nil {
			t.Error("ParsePauseNode read two JSON values")
		}
	})

	t.Run("path", func(t *testing.T) {
		if got, want := PauseNodePath("acme-app", runID), "paused/acme-app/20260926-221530-abcd"; got != want {
			t.Errorf("PauseNodePath = %q, want %q", got, want)
		}
		// The escaping is internal/budget's Key, restated in this package
		// because internal/budget imports it and it cannot call back: a slug
		// and a run id are escaped for the database exactly as that rule
		// escapes them, or the node the service writes sits at a path the
		// orchestrator does not read.
		if got, want := PauseNodePath("acme/app", "a.b$c#d[e]f%g/h"), "paused/acme%2Fapp/a%2Eb%24c%23d%5Be%5Df%25g%2Fh"; got != want {
			t.Errorf("PauseNodePath escapes to %q, want %q", got, want)
		}
		if got, want := PauseNodePath("acme-app", "a\u0001b\u007fc"), "paused/acme-app/a%01b%7Fc"; got != want {
			t.Errorf("PauseNodePath escapes a control character to %q, want %q", got, want)
		}
		// An empty segment would collapse the path onto another node: the
		// database cleans paused//r1 into paused/r1.
		defer func() {
			if recover() == nil {
				t.Error("PauseNodePath accepted an empty slug: it names no node")
			}
		}()
		_ = PauseNodePath("", runID)
	})

	t.Run("action", func(t *testing.T) {
		for want, a := range map[string]PauseAction{"wait": ActionWait, "pause": ActionPause, "halt": ActionHalt} {
			if string(a) != want {
				t.Errorf("the action is spelled %q, want %q", a, want)
			}
			if !a.Known() {
				t.Errorf("%q is not Known", a)
			}
		}
		if PauseAction("resume").Known() {
			t.Error(`"resume" is Known: the answer names three actions and a resume is not one of them`)
		}
	})

	t.Run("names", func(t *testing.T) {
		for _, c := range []struct{ got, want, what string }{
			{PauseFile, "pause.json", "the record's object name"},
			{PauseMemberAction, "action", "the answer's action member"},
			{PauseMemberResumeAt, "resume_at", "the resume instant's member"},
			{PauseMemberResumedBy, "resumed_by", "the row's resumer member"},
			{PauseMemberResumes, "resumes", "the task's resumes member"},
			{PausePath, "/pause", "the request a run makes once it has saved"},
		} {
			if c.got != c.want {
				t.Errorf("%s is %q, want %q", c.what, c.got, c.want)
			}
		}
		if PauseVersion != 1 {
			t.Errorf("PauseVersion is %d, want 1: a reader of another version ends with a named outcome", PauseVersion)
		}
	})
}

// pinMembers asserts that a stored shape carries exactly these members, sorted:
// a member added, dropped or renamed is a shape another release reads, and the
// contract is where that is decided.
func pinMembers(t *testing.T, what string, raw []byte, want []string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decoding %s: %v", what, err)
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s carries %v, want exactly %v", what, got, want)
	}
}
