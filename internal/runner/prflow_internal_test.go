package runner

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestInlineTextNeutralisesMarkdownAndMarkers: free text placed in the
// status section is one plain line, however hostile.
func TestInlineTextNeutralisesMarkdownAndMarkers(t *testing.T) {
	in := "stage `x` failed <!-- fugaro:status end -->\n## Heading\n[//]: # (fugaro:status begin)\n[link](http://evil) | *bold* _it_ <script>\r\x00\x1b[31m" + strings.Repeat("long ", 100)
	out := inlineText(in)
	plain := strings.NewReplacer("\\*", "", "\\_", "").Replace(out) // escaped emphasis characters are fine
	for _, bad := range []string{"\n", "\r", "<", ">", "[", "]", "`", "|", "*", "_", "#", "\x00", "\x1b"} {
		if strings.Contains(plain, bad) {
			t.Errorf("inlineText kept %q: %q", bad, out)
		}
	}
	if n := len([]rune(out)); n > maxStatusText {
		t.Errorf("not clipped: %d runes", n)
	}
	// What is left cannot be read as a marker by the parser either.
	if body := gitprov.StripStatus("x\n" + out + "\n"); !strings.Contains(body, "stage") {
		t.Errorf("body = %q", body)
	}
}

func TestScrubDescriptionRemovesForgedMarkers(t *testing.T) {
	in := "real\n[//]: # (fugaro:status begin)\nforged\n[//]: # (fugaro:status end)\n  <!-- fugaro:Status begin -->\n<!-- fugaro:report run=20200101-000000-aaaa -->\ntail"
	out := scrubDescription(in)
	if strings.Contains(strings.ToLower(out), "fugaro:") || strings.Contains(out, "forged") || !strings.Contains(out, "real") || !strings.Contains(out, "tail") {
		t.Fatalf("scrubbed = %q", out)
	}
}

func TestDescDigestIgnoresWhitespaceAndDraftPrefix(t *testing.T) {
	a := descDigest("Title", "body text")
	if a != descDigest("[DRAFT] Title", "body text\r\n") || a != descDigest("Title", "\nbody text  ") {
		t.Fatal("digest depends on whitespace or the draft prefix")
	}
	if a == descDigest("Title", "other") || a == descDigest("Other", "body text") {
		t.Fatal("digest ignores a change")
	}
}

func TestInlineTextNeutralisesMentions(t *testing.T) {
	if out := inlineText("thanks @octocat and @org/team"); strings.Contains(out, "@o") {
		t.Fatalf("mention kept: %q", out)
	}
}

// TestShortGitHead: a head the section prints is an object id or it is
// "(invalid)" — seven characters of a SHA-1, twelve of a SHA-256, and nothing of
// anything else, so a hand cannot put a line break or a marker in the body
// through Head or PushedHead.
func TestShortGitHead(t *testing.T) {
	sha1 := strings.Repeat("a1", 20)
	sha256 := strings.Repeat("f0", 32)
	if got := shortGitHead(sha1); got != sha1[:7] {
		t.Errorf("shortGitHead(sha1) = %q, want its first 7 characters", got)
	}
	if got := shortGitHead(sha256); got != sha256[:12] {
		t.Errorf("shortGitHead(sha256) = %q, want its first 12 characters", got)
	}
	for _, bad := range []string{
		"",
		"abc",
		sha1[:39],
		sha1 + "a",
		strings.ToUpper(sha1),
		strings.Repeat("g", 40),
		"HEAD",
		"abc\n## shipped\n<!-- fugaro:status end -->",
		strings.Repeat("é", 20),
	} {
		if got := shortGitHead(bad); got != "(invalid)" {
			t.Errorf("shortGitHead(%q) = %q, want (invalid)", bad, got)
		}
	}
}

// TestStatusLineShortID: the status section names the run by its short ID
// (spec.md §1, D1701), and the command it tells a reader to paste names the run
// exactly. A lookup resolves an ending only once one accepts it -- resolveRun is
// written and tested, and SR14 wires diagnose to it -- so this expectation is
// what SR14 changes, and what fails if the hint is shortened ahead of the CLI
// that would answer it. A hint whose own argument is refused is worse than a
// longer line: a full run ID is what every version of the CLI takes.
func TestStatusLineShortID(t *testing.T) {
	r := &run{rec: &runstore.Record{RunID: "20261024-120000-3bb2"}}
	got := r.sectionLines("**Ready for review**")
	const want = "**Ready for review**\nRun `3bb2` (a stale time here means the run died: `fugaro diagnose 20261024-120000-3bb2`)"
	if got != want {
		t.Errorf("the status section is\n%q\nwant\n%q", got, want)
	}
}

// changeSetSpec is a member's task: two runs launched together (D642), of which
// this one is the first, plus a repository spelled with a capital so a record
// written from it has to be reduced to the canonical form the member list
// holds.
func changeSetSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "Acme/App", Ref: "main", Task: "x",
		ChangeSet: &task.ChangeSet{ID: "20260926-221530-a1b2", Members: []task.ChangeSetMember{
			{Repo: "acme/app", RunID: "20260926-221530-a1b2"},
			{Repo: "acme/other", RunID: "20260926-221531-c3d4"},
		}}}
}

// TestChangeSetSectionNamesTheSiblings: a member's section says which set it is
// in and names the runs beside it (D1) — a reader of one member's pull request
// has no other way to find them. The sibling's pull request is not named: a run
// reaches only its own repository's prefixes in the runs bucket
// (SECURITY.md), so the block a member writes cannot know it, and the line says
// so rather than leaving a blank a reader would read as "opened none".
func TestChangeSetSectionNamesTheSiblings(t *testing.T) {
	r := &run{spec: changeSetSpec(), rec: &runstore.Record{RunID: "20260926-221530-a1b2"}}
	got := r.sectionLines("**Running**")
	for _, want := range []string{
		"**Change set** `a1b2`: 2 runs, one per repository",
		"- `acme/app` run `a1b2` (this run)",
		"- `acme/other` run `c3d4`: its pull request is not known to this run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section is missing %q:\n%s", want, got)
		}
	}
	// The block is above the "Run" line the section always ends with, and it
	// reaches the pull request's body through the section writer whole.
	body, _ := gitprov.ReplaceStatusWith("Desc", got, gitprov.StatusOptions{})
	if !strings.Contains(body, "- `acme/other` run `c3d4`") {
		t.Errorf("the block did not survive the write:\n%s", body)
	}
	// A run in no change set renders the section it rendered before (D1's
	// byte-identity requirement): the block is the set's, never "none".
	plain := &run{spec: &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app"}, rec: r.rec}
	if out := plain.sectionLines("**Running**"); strings.Contains(out, "Change set") {
		t.Errorf("a run in no set names one:\n%s", out)
	}
}

// TestChangeSetMemberRecord: the member records its own pull request under its
// own prefix, where the step that settles a whole set reads it (D3). The
// repository is the canonical form of the task's own: the member list every
// member is checked against is canonical, and a record spelling its repository
// differently would not match the list it repeats. The verdict is not this
// run's to write here — it joins the record when the run ends (D2) — and a run
// in no change set writes no record at all.
func TestChangeSetMemberRecord(t *testing.T) {
	ctx := context.Background()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	store := runstore.Open(bucket, "acme-app", "20260926-221530-a1b2")
	at := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	r := &run{spec: changeSetSpec(), d: Deps{Store: store, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return at }}}
	r.noteChangeSetMember(ctx, gitprov.PR{Number: 7, URL: "https://github.com/acme/app/pull/7"})
	m, err := store.ReadChangeSetMember(ctx)
	if err != nil {
		t.Fatalf("the member's record: %v", err)
	}
	want := runstore.ChangeSetMember{Version: 1, SetID: "20260926-221530-a1b2", Repo: "acme/app", RunID: "20260926-221530-a1b2",
		PRURL: "https://github.com/acme/app/pull/7", At: at}
	if *m != want || !m.At.Equal(at) {
		t.Fatalf("the record is %+v, want %+v", *m, want)
	}
	// A run of the same repository in no set writes nothing: the record is a
	// member's, and a run that is in no set has no set to record.
	plain := &run{spec: &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app"}, d: r.d}
	plain.noteChangeSetMember(ctx, gitprov.PR{Number: 8, URL: "https://github.com/acme/app/pull/8"})
	if m, err := store.ReadChangeSetMember(ctx); err != nil || m.PRURL != want.PRURL {
		t.Fatalf("a run in no set touched the record: %+v, %v", m, err)
	}
}
