package watch

import (
	"strings"
	"testing"
	"time"
)

// A mode: artifact run succeeded, but nothing about it is like a succeeded
// implement run: its deliverable is in the runs bucket and it opened no pull
// request. Both watches therefore say so in words (design artifact-runs.md
// §3), and neither ever leaves the bare stage word, SUCCEEDED, as the last
// thing an owner reads on that row.
//
// Mutations: drop the "artifacts" arm of finishedOutcomeText and the plain
// table prints SUCCEEDED (this test fails on the first assertion); drop the
// arm of (*rend).finishedRow and the dashboard line prints SUCCEEDED (the
// second assertion fails).
func TestWatchShowsAnArtifactRunsOutcome(t *testing.T) {
	run := RunRow{Run: "r-art", Title: "nightly bundle", Stage: "succeeded", Outcome: "artifacts", Finished: true}

	// The plain table, end to end: RenderPlain writes the row.
	var b strings.Builder
	if err := RenderPlain(&b, "acme/app", View{Repos: []RepoBlock{{Slug: "acme/app", Finished: []RunRow{run}}}}, nil, PlainOptions{ASCII: true}); err != nil {
		t.Fatal(err)
	}
	text := b.String()
	if !strings.Contains(text, "ARTIFACTS, NO PR") {
		t.Fatalf("the plain table does not name the artifact outcome:\n%s", text)
	}
	if strings.Contains(text, "SUCCEEDED") {
		t.Fatalf("the plain table shows the bare stage word:\n%s", text)
	}

	// The dashboard line, drawn by finishedRow as Render draws it.
	r := &rend{w: 120, g: glyphsOf(true)}
	line := r.finishedRow(run, false)
	if !strings.Contains(line, "artifacts, no PR") {
		t.Fatalf("the dashboard row does not name the artifact outcome: %q", line)
	}
	if strings.Contains(strings.ToUpper(line), "SUCCEEDED") {
		t.Fatalf("the dashboard row shows the bare stage word: %q", line)
	}
}

// ReadyForReview is the deliberate non-change (design artifact-runs.md §3):
// it still selects only Outcome "ready", so an artifact run — which opens no
// pull request at all — is never listed under "ready for your review", and
// neither is a "draft" or "none".
//
// Mutation: widen the filter to anything but "none" (say, skip only draft)
// and the artifacts row below appears in the list.
func TestArtifactRunIsNotReadyForReview(t *testing.T) {
	now := time.Now()
	v := View{Repos: []RepoBlock{{Slug: "acme/app", Finished: []RunRow{
		{Run: "r-art", Outcome: "artifacts", Finished: true, StartedAt: now.UnixMilli()},
		{Run: "r-draft", Outcome: "draft", Finished: true, StartedAt: now.UnixMilli()},
		{Run: "r-ready", Outcome: "ready", Finished: true, StartedAt: now.UnixMilli()},
	}}}}
	got := ReadyForReview(v)
	if len(got) != 1 || got[0].Run != "r-ready" {
		t.Fatalf("ReadyForReview = %+v, want only r-ready", got)
	}
}
