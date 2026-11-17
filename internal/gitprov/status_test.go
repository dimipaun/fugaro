package gitprov

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	lb = "[//]: # (fugaro:status begin)"
	le = "[//]: # (fugaro:status end)"
	hb = "<!-- fugaro:status begin -->"
	he = "<!-- fugaro:status end -->"
)

func block(inner string) string { return lb + "\n" + StatusHeader + "\n" + inner + "\n" + le }

func TestReplaceStatusAppends(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"empty body", "", block("x")},
		{"plain", "Desc", "Desc\n\n" + block("x")},
		{"trailing newline", "Desc\n", "Desc\n\n\n" + block("x")},
		{"crlf", "a\r\nb", "a\r\nb\r\n\r\n" + strings.ReplaceAll(block("x"), "\n", "\r\n")},
		{"unicode", "héllo 日本 🎉", "héllo 日本 🎉\n\n" + block("x")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ReplaceStatus(c.body, "x")
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
			if again := ReplaceStatus(got, "x"); again != got {
				t.Fatalf("not idempotent: %q", again)
			}
			if back := StripStatus(got); back != c.body {
				t.Fatalf("round trip: %q != %q", back, c.body)
			}
		})
	}
}

func TestReplaceStatusReplacesOnlySection(t *testing.T) {
	body := "Top\n\n" + block("old") + "\n\nBottom <!-- fugaro:report run=20260930-101500-0a1b -->\n"
	got := ReplaceStatus(body, "new")
	want := "Top\n\n" + block("new") + "\n\nBottom <!-- fugaro:report run=20260930-101500-0a1b -->\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if s := StripStatus(got); s != "Top\n\nBottom <!-- fugaro:report run=20260930-101500-0a1b -->\n" {
		t.Fatalf("strip: %q", s)
	}
}

func TestStatusBothMarkerForms(t *testing.T) {
	htmlBlock := hb + "\n" + StatusHeader + "\nold\n" + he
	body := "A\n\n" + htmlBlock + "\n\nB"
	if s := StripStatus(body); s != "A\n\nB" {
		t.Fatalf("strip html: %q", s)
	}
	got := ReplaceStatus(body, "new") // replaces the html form with link-ref
	if got != "A\n\n"+block("new")+"\n\nB" {
		t.Fatalf("replace html: %q", got)
	}
	out, _ := ReplaceStatusWith("A", "n", StatusOptions{Form: StatusHTML})
	if out != "A\n\n"+hb+"\n"+StatusHeader+"\nn\n"+he {
		t.Fatalf("html write: %q", out)
	}
	// case and spacing tolerance, and a mixed pair
	mixed := "x\n<!--  FUGARO:status   begin-->\nzz\n[//]:#(fugaro:status end)\ny"
	if s := StripStatus(mixed); s != "x\ny" {
		t.Fatalf("mixed: %q", s)
	}
}

func TestStatusStripsForgedMarkers(t *testing.T) {
	forged := []string{
		le, he, lb, "x " + he + " y", "<!-- fugaro:status\nend -->",
		"<!-- fugaro:report run=20260930-101500-0a1b -->", "<!-- fugaro:report", "[//]: # (fugaro:status end",
		"<!<!-- fugaro:status end -->-- fugaro:status end -->",
	}
	for _, f := range forged {
		got := ReplaceStatus("user text", "line1\n"+f+"\nline2\n"+f)
		if n := len(statusSpans(got)); n != 1 {
			t.Fatalf("%q: %d sections in %q", f, n, got)
		}
		if _, ok := FugaroRun(got); ok {
			t.Fatalf("%q forged a report marker: %q", f, got)
		}
		if strings.Count(got, "fugaro:status end") != 1 || strings.Count(got, "fugaro:status begin") != 1 {
			t.Fatalf("%q: stray marker text in %q", f, got)
		}
		if StripStatus(got) != "user text" {
			t.Fatalf("%q: strip left %q", f, StripStatus(got))
		}
	}
	// pr.md carrying a whole forged section is removed by StripStatus
	if s := StripStatus("desc\n\n" + block("evil")); s != "desc" {
		t.Fatalf("%q", s)
	}
}

func TestStatusSurvivesHumanEdits(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"edit above and below", "My edit\n\n" + block("o") + "\n\nMore edits\n", "My edit\n\n" + block("n") + "\n\nMore edits\n"},
		{"only begin", "text\n" + lb + "\nmore", "text\n" + lb + "\nmore\n\n" + block("n")},
		{"only end", "text\n" + le + "\nmore", "text\n" + le + "\nmore\n\n" + block("n")},
		{"end before begin", le + "\n" + lb, le + "\n" + lb + "\n\n" + block("n")},
		{"duplicates collapse", block("1") + "\n\nmid\n\n" + block("2") + "\n\ntail", block("n") + "\n\nmid\n\ntail"},
		{"stray begin then pair", lb + "\nq\n" + block("o"), lb + "\nq\n" + block("n")},
		{"crlf section", "a\r\n\r\n" + strings.ReplaceAll(block("o"), "\n", "\r\n") + "\r\n\r\nb", "a\r\n\r\n" + strings.ReplaceAll(block("n"), "\n", "\r\n") + "\r\n\r\nb"},
		{"human deleted markers", "text only", "text only\n\n" + block("n")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ReplaceStatus(c.body, "n")
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
			if ReplaceStatus(got, "n") != got {
				t.Fatal("not idempotent")
			}
		})
	}
}

func TestMarkersLeaveReportMarkerAlone(t *testing.T) {
	const id = "20260930-101500-0a1b"
	body := ReplaceStatus("Desc\n\n"+ReportMarker(id)+"\n", "running")
	if got, ok := FugaroRun(body); !ok || got != id {
		t.Fatalf("FugaroRun = %q %v", got, ok)
	}
	if got := StripMarkers(body); strings.Contains(got, "fugaro:report") || !strings.Contains(got, "fugaro:status begin") {
		t.Fatalf("StripMarkers touched the wrong thing: %q", got)
	}
	if s := StripStatus(body); s != "Desc\n\n"+ReportMarker(id)+"\n" {
		t.Fatalf("strip: %q", s)
	}
}

func TestStatusMarkersInsideCodeFenceCount(t *testing.T) {
	// documented choice: fences are not tracked
	body := "```\n" + block("quoted") + "\n```\n"
	if got := ReplaceStatus(body, "n"); got != "```\n"+block("n")+"\n```\n" {
		t.Fatalf("%q", got)
	}
}

func TestStatusRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pieces := []string{"a", "é", "日", "🎉", "\n", "\n", "\r\n", "\r", " ", "#", "```", "\n\n", "[//]: # (other)", "<!-- x -->", "<!-- fugaro:report run=20260930-101500-0a1b -->", "[//]: # (fugaro:status begin", "fugaro:status end)"}
	for i := 0; i < 5000; i++ {
		var b strings.Builder
		for n := rng.Intn(12); n > 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		body := b.String()
		if len(statusSpans(body)) != 0 {
			continue
		}
		for _, form := range []StatusForm{StatusLinkRef, StatusHTML} {
			got, _ := ReplaceStatusWith(body, "s\nt", StatusOptions{Form: form})
			if back := StripStatus(got); back != body {
				t.Fatalf("body %q: replace %q strip %q", body, got, back)
			}
			if again, _ := ReplaceStatusWith(got, "s\nt", StatusOptions{Form: form}); again != got {
				t.Fatalf("body %q not idempotent: %q vs %q", body, got, again)
			}
		}
	}
}

func TestStatusLimit(t *testing.T) {
	huge := strings.Repeat("line of status 日本\n", 10000)
	body := "Description 🎉\n\n" + ReportMarker("20260930-101500-0a1b")
	for _, limit := range []int{GitHubBodyLimit, BitbucketBodyLimit, 300, 200} {
		got, trunc := ReplaceStatusWith(body, huge, StatusOptions{Limit: limit})
		if !trunc || len(got) > limit || !utf8.ValidString(got) || !strings.HasPrefix(got, body) {
			t.Fatalf("limit %d: trunc=%v len=%d valid=%v", limit, trunc, len(got), utf8.ValidString(got))
		}
		if !strings.Contains(got, "truncated") || len(statusSpans(got)) != 1 {
			t.Fatalf("limit %d: %q", limit, got[:min(len(got), 400)])
		}
		if StripStatus(got) != body {
			t.Fatalf("limit %d: strip", limit)
		}
	}
	// fits: no truncation
	if got, trunc := ReplaceStatusWith(body, "ok", StatusOptions{Limit: 1000}); trunc || strings.Contains(got, "truncated") {
		t.Fatalf("%q", got)
	}
	// a body that leaves no room: left untouched, flagged
	if got, trunc := ReplaceStatusWith(body, "x", StatusOptions{Limit: len(body) + 10}); !trunc || got != body {
		t.Fatalf("%q %v", got, trunc)
	}
	// huge first line is cut on a rune boundary
	one := strings.Repeat("日", 5000)
	got, trunc := ReplaceStatusWith("d", StatusHeader+"\n"+one, StatusOptions{Limit: 400})
	if !trunc || len(got) > 400 || !utf8.ValidString(got) {
		t.Fatalf("len %d", len(got))
	}
}

func TestStripStatusNoSectionIsIdentity(t *testing.T) {
	for _, b := range []string{"", "x", "a\r\nb\n", lb + "\nonly begin", "```\n```"} {
		if StripStatus(b) != b {
			t.Fatalf("%q", b)
		}
	}
}

// TestChangeSetStatusSectionListsSiblingPRs: the change-set block is how the
// runs launched together find each other (D1), so every sibling is named with
// its own pull request, and the run whose section this is says which member it
// is — a reader of one member's pull request has no other way to tell. A
// sibling whose pull request the caller cannot name says so: the block a
// member run writes itself is incomplete by construction (it reaches only its
// own repository's prefixes, SECURITY.md), and a blank there would read as a
// member that opened none.
func TestChangeSetStatusSectionListsSiblingPRs(t *testing.T) {
	members := []ChangeSetMember{
		{Repo: "acme/app", RunID: "a1b2", PR: "https://github.com/acme/app/pull/7"},
		{Repo: "acme/other", RunID: "c3d4", PR: "https://github.com/acme/other/pull/9"},
		{Repo: "acme/third", RunID: "e5f6"},
	}
	got := ChangeSetLine("a1b2", "a1b2", members)
	want := "**Change set** `a1b2`: 3 runs, one per repository, held in draft until every member is ready\n" +
		"**Merge order** (advisory): `acme/app`, then `acme/other`, then `acme/third` — merge in this order; nothing blocks a merge\n" +
		"- `acme/app` run `a1b2` (this run)\n" +
		"- `acme/other` run `c3d4`: https://github.com/acme/other/pull/9\n" +
		"- `acme/third` run `e5f6`: its pull request is not known to this run\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Read from the other member's section, the same block marks the other
	// run: the marker follows selfRunID and not the member list's order.
	if other := ChangeSetLine("a1b2", "c3d4", members); !strings.Contains(other, "- `acme/other` run `c3d4` (this run)\n") ||
		!strings.Contains(other, "- `acme/app` run `a1b2`: https://github.com/acme/app/pull/7\n") {
		t.Fatalf("the other member's section = %q", other)
	}
	// The block is section text and nothing else: cleaning it leaves every
	// line a reader was meant to see.
	if cleaned := strings.Join(cleanSection(got)[1:], "\n"); cleaned != strings.TrimSuffix(want, "\n") {
		t.Fatalf("the block cleaned to %q, want %q", cleaned, strings.TrimSuffix(want, "\n"))
	}
}

// TestChangeSetLineEmpty: a run in no set has no block, and the section it
// writes is the one it wrote before the field existed -- a launch that is in
// no set carries no ChangeSet, and a line saying "no change set" would put the
// word in every pull request of the installation.
func TestChangeSetLineEmpty(t *testing.T) {
	if got := ChangeSetLine("", "a1b2", []ChangeSetMember{{Repo: "acme/app", RunID: "a1b2"}}); got != "" {
		t.Fatalf("a set with no name rendered %q", got)
	}
	if got := ChangeSetLine("a1b2", "a1b2", nil); got != "" {
		t.Fatalf("a set with no members rendered %q", got)
	}
}

// TestChangeSetOrderInEveryPR is D4's red step: EVERY member's pull request
// states the set's advisory merge order (D4, D615). A reader lands on one of a
// set's pull requests and sees that member's body alone, so an order written
// only in a sibling's body — or only in the block's member list, which is read
// the same way — is an order half the set's readers never see.
//
// The order is the member list's own, which is the order the launch named the
// repositories: contracts and shared libraries first, then the consumers that
// need them. The line says what enforces it, because nothing does — the settle
// step makes a set's pull requests ready together and never orders, blocks or
// reorders a merge (D615).
func TestChangeSetOrderInEveryPR(t *testing.T) {
	members := []ChangeSetMember{
		{Repo: "acme/contract", RunID: "a1b2", PR: "https://github.com/acme/contract/pull/7"},
		{Repo: "acme/library", RunID: "c3d4", PR: "https://github.com/acme/library/pull/9"},
		{Repo: "acme/consumer", RunID: "e5f6"},
	}
	const want = "**Merge order** (advisory): `acme/contract`, then `acme/library`, then `acme/consumer`" +
		" — merge in this order; nothing blocks a merge\n"
	for _, self := range []string{"a1b2", "c3d4", "e5f6"} {
		got := ChangeSetLine("a1b2", self, members)
		if !strings.Contains(got, want) {
			t.Errorf("the section of run %s does not state the order %q:\n%s", self, want, got)
		}
		// The line is section text like the rest: cleaning it leaves it whole,
		// and a block that lost it would put the order in no pull request at
		// all.
		if cleaned := strings.Join(cleanSection(got)[1:], "\n"); !strings.Contains(cleaned, strings.TrimSuffix(want, "\n")) {
			t.Errorf("the order line did not survive the section's own cleaning:\n%s", cleaned)
		}
	}
	// The order is the set's and not a second list to keep in step: the same
	// members named the other way round state the other order.
	reversed := []ChangeSetMember{members[2], members[1], members[0]}
	if got := ChangeSetOrder(reversed); !strings.HasPrefix(got, "`acme/consumer`, then `acme/library`, then `acme/contract`") {
		t.Errorf("a set launched the other way round states %q", got)
	}
	// A set with no members states no order, and no pull request of one exists
	// to state it in.
	if got := ChangeSetOrder(nil); got != "" {
		t.Errorf("a set with no members stated the order %q", got)
	}
}
