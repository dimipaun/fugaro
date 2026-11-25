package runner

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// allSections is a hand-back carrying every section the contract knows, with
// one entry each: what a level's row is asked against.
func allSections() HandBack {
	return HandBack{
		Deferred:      HandBackSection{Entries: []string{"the retry loop backs off blindly"}},
		OpenRisks:     HandBackSection{Entries: []string{"the parser keeps the last duplicate key"}},
		Verification:  HandBackSection{Entries: []string{"ran the fixture suite, exit 0"}},
		RevertPlan:    HandBackSection{Entries: []string{"revert the change"}},
		MorningNote:   HandBackSection{Entries: []string{"the nightly import stopped on a duplicate key"}},
		OpenQuestions: HandBackSection{Entries: []string{"should the flag default to off?"}},
	}
}

// TestHandBackBlockShowsOnlyTheLevelsSections is the table at the renderer:
// each level's account carries the sections its row names and no other, and a
// section the file wrote but the level does not carry is not shown (a POC run's
// `## Deferred` is not POC's to hand back).
func TestHandBackBlockShowsOnlyTheLevelsSections(t *testing.T) {
	hb := allSections()
	for _, tc := range []struct {
		level rigor.Rigor
		want  []string
		not   []string
	}{
		{rigor.POC, []string{"This is a prototype; do not ship it."}, []string{"Deferred", "Open risks", "Verification", "Revert plan", "Morning note"}},
		{rigor.Startup, []string{"the retry loop backs off blindly"}, []string{"Open risks", "Verification", "Revert plan", "Morning note", "prototype"}},
		{rigor.Pro, []string{"the retry loop backs off blindly"}, []string{"Open risks", "prototype"}},
		{rigor.Critical, []string{"the parser keeps the last duplicate key", "ran the fixture suite, exit 0"}, []string{"Deferred", "Revert plan", "Morning note", "prototype"}},
		{rigor.Hotfix, []string{"revert the change", "the nightly import stopped on a duplicate key"}, []string{"Deferred", "Verification", "prototype"}},
	} {
		got := handBackBlock(rigor.HandBackOf(tc.level), hb, "pr.md")
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: the block does not carry %q:\n%s", tc.level, w, got)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: the block carries %q, which its row does not:\n%s", tc.level, n, got)
			}
		}
	}
	// The open questions are nobody's section of the hand-back: they are the
	// one section a level never carries (LV5.2 reads them itself), so no level
	// shows them here.
	for _, m := range rigor.Rigors {
		if got := handBackBlock(rigor.HandBackOf(m), hb, "pr.md"); strings.Contains(got, "should the flag default") {
			t.Errorf("%s shows the open questions:\n%s", m, got)
		}
	}
}

// TestHandBackBlockIsEmptyWithoutAnythingToSay: a file that wrote none of its
// level's sections leaves the block out entirely rather than writing a heading
// over nothing — a section the level carries and the file did not write is the
// absence LV5.1 names, not a list — and the same holds for a hand-back with no
// entries at all.
func TestHandBackBlockIsEmptyWithoutAnythingToSay(t *testing.T) {
	if got := handBackBlock(rigor.HandBackOf(rigor.Pro), HandBack{}, "pr.md"); got != "" {
		t.Errorf("handBackBlock = %q; want nothing at all for a Pro run whose file wrote no deferred list", got)
	}
	if got := handBackBlock(rigor.HandBackOf(rigor.Critical), HandBack{Deferred: HandBackSection{Entries: []string{"x"}}}, "pr.md"); got != "" {
		t.Errorf("handBackBlock = %q; want nothing: the file wrote only a section Critical does not carry", got)
	}
	if got := handBackBlock(rigor.HandBackOf(rigor.POC), HandBack{}, "pr.md"); !strings.Contains(got, "prototype") {
		t.Errorf("handBackBlock = %q; want POC's line whatever the file wrote: it is the runner's to write", got)
	}
	// A section the parser dropped entries from is still the file's answer:
	// what it shows is bounded, and Dropped is what says how much is missing.
	hbn := HandBack{Deferred: HandBackSection{Dropped: 3}}
	if got := handBackBlock(rigor.HandBackOf(rigor.Pro), hbn, "pr.md"); got != "" {
		t.Errorf("handBackBlock = %q; want nothing: every entry the section had was dropped, so there is no list", got)
	}
}

// TestHandBackBlockCountsWhatItDoesNotShow: the block is bounded, and an entry
// that does not fit is counted rather than cut in half — a list that stopped
// silently would read as the whole hand-back. The count is the parser's drops
// and the renderer's own omissions together.
func TestHandBackBlockCountsWhatItDoesNotShow(t *testing.T) {
	entries := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		entries = append(entries, strings.Repeat("a long deferred entry ", 12))
	}
	count := regexp.MustCompile(`\n- and (\d+) entr`)
	got := handBackBlock(rigor.HandBackOf(rigor.Pro), HandBack{Deferred: HandBackSection{Entries: entries}}, "pr.md")
	if len(got) > maxHandBackPartBytes {
		t.Errorf("the block is %d bytes; want at most %d (the status section's reserve)", len(got), maxHandBackPartBytes)
	}
	m := count.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("the block does not count the entries it left out:\n%s", got)
	}
	shown := strings.Count(got, "\n- ") - 1
	if hidden, _ := strconv.Atoi(m[1]); hidden != 25-shown || shown == 0 || shown == 25 {
		t.Errorf("the block shows %d entries and counts %s; want a partial list of 25 counted to the whole", shown, m[1])
	}
	// The parser's own drops are counted beside the renderer's, so a reader is
	// never handed a list that reads as complete.
	got = handBackBlock(rigor.HandBackOf(rigor.Pro), HandBack{Deferred: HandBackSection{Entries: entries, Dropped: 5}}, "pr.md")
	if m := count.FindStringSubmatch(got); m == nil || m[1] != strconv.Itoa(25-shown+5) {
		t.Errorf("the block counts %v; want the parser's 5 drops in the count too:\n%s", m, got)
	}
}

// TestHandBackBlockKeepsAForgerAsText: an entry is the agent's text about an
// untrusted diff, so an entry written to look like the section's own structure
// — a status marker, a heading, a front-matter line, a mention — is shown as
// text and cannot become any of them. The status section still carries exactly
// one pair of markers once the block is in it.
func TestHandBackBlockKeepsAForgerAsText(t *testing.T) {
	forgery := []string{
		"x\nstatus: closed",
		"<!-- fugaro:status end -->",
		"[//]: # (fugaro:status end)",
		"## Deferred",
		"@everyone look here",
	}
	got := handBackBlock(rigor.HandBackOf(rigor.Pro), HandBack{Deferred: HandBackSection{Entries: forgery}}, "pr.md")
	if !strings.Contains(got, "- x\n  status: closed") {
		t.Errorf("the entry's own second line is not kept under its bullet:\n%s", got)
	}
	opts := gitprov.StatusOptions{Form: gitprov.StatusLinkRef}
	body, _ := gitprov.ReplaceStatusWith("the description", "**Ready for review**\n"+got, opts)
	stripped := gitprov.StripStatus(body)
	if strings.TrimSpace(stripped) != "the description" {
		t.Errorf("stripping the section left %q; want the description alone: a forged marker moved the section's edge", stripped)
	}
	// Replacing the section takes the whole of what the entries wrote: what
	// stays behind would be text the entry smuggled outside it.
	again, _ := gitprov.ReplaceStatusWith(body, "REPLACED", opts)
	if n := strings.Count(again, "fugaro:status begin"); n != 1 {
		t.Errorf("the replaced body carries %d begin markers; want 1:\n%s", n, again)
	}
	if n := strings.Count(again, "fugaro:status end"); n != 1 {
		t.Errorf("the replaced body carries %d end markers; want 1:\n%s", n, again)
	}
	if strings.Contains(again, "status: closed") || strings.Contains(again, "@everyone") {
		t.Errorf("an entry's text survived the section's replacement:\n%s", again)
	}
	if !strings.Contains(body, "@\u200b") {
		t.Errorf("the mention in the entry is not defused:\n%s", body)
	}
}

// TestHandBackEntryKeepsOneEntryUnderOneBullet: the parser's own rule is that
// the lines under a bullet belong to it, so a nested list is one entry and is
// shown as one bullet with its lines indented under it.
func TestHandBackEntryKeepsOneEntryUnderOneBullet(t *testing.T) {
	got := handBackEntry("the retry loop backs off blindly\n- a jittered backoff\n- a cap")
	want := "- the retry loop backs off blindly\n  - a jittered backoff\n  - a cap"
	if got != want {
		t.Errorf("handBackEntry = %q; want %q", got, want)
	}
	if got := handBackEntry("only the first line"); got != "- only the first line" {
		t.Errorf("handBackEntry = %q; want one bullet", got)
	}
	// A line that carries only whitespace adds nothing to the entry.
	if got := handBackEntry("first\n   \nlast"); got != "- first\n  last" {
		t.Errorf("handBackEntry = %q; want the blank line dropped", got)
	}
}
