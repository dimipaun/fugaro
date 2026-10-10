package watch

import (
	"strings"
	"testing"
	"time"
)

// RenderPlain shows a repository's finished runs (status, PR, age) and, at
// the end, the "Ready for your review" section (design generic-tool §10.1
// and §10.3): the same information the interactive screen shows, in the
// non-interactive text frame.
func TestRenderPlainShowsFinishedAndReady(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Name: "acme/app"}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-ready", Slug: "a", Title: "Fix the bug", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/7", PRNumber: 7, FinishedAt: t0.Add(-time.Hour)},
		{Run: "r-failed", Slug: "a", Title: "Half-done", Status: "failed", FinishedAt: t0.Add(-2 * time.Hour)},
	}, t0)
	var b strings.Builder
	if err := RenderPlain(&b, "aurora", v, PlainOptions{}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"r-ready", "READY FOR REVIEW", "r-failed", "FAILED", "Ready for your review (1)", "https://github.com/acme/app/pull/7", "Fix the bug"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	// Only the ready run appears in the review section, not the failure.
	section := got[strings.Index(got, "Ready for your review"):]
	if strings.Contains(section, "r-failed") {
		t.Fatalf("the failure must not be in the review section:\n%s", section)
	}
}
