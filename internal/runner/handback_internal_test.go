package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/task"
)

// handBackFixture is a hand-back with all six sections and their kinds of
// entry: a plain bullet, an entry whose sub-points are a nested list (one
// entry, not two), and a section whose bullet runs over a line of its own.
const handBackFixture = `## Deferred

- the retry loop has no backoff; a fast 5xx would spin
  - a backoff belongs with the config work
- the metric name is hardcoded

## Open risks

- the migration is not reversible in place

## Verification

- the package's tests pass on the final commit
- the flaky test ran 20 times without failing

## Revert plan

- revert the two commits, then re-run the migration backwards

## Morning note

- what broke: the checkout path returned 500
- what was done: the nil guard is back
- what needs a proper fix: the cache's eviction

## Open questions

- should the metric keep its old name?
`

// handBackWant is what handBackFixture parses to, whichever file it came
// from: the entry text with its bullet marker removed, and nothing dropped.
func handBackWant() HandBack {
	return HandBack{
		Deferred: HandBackSection{Entries: []string{
			"the retry loop has no backoff; a fast 5xx would spin\n  - a backoff belongs with the config work",
			"the metric name is hardcoded",
		}},
		OpenRisks: HandBackSection{Entries: []string{"the migration is not reversible in place"}},
		Verification: HandBackSection{Entries: []string{
			"the package's tests pass on the final commit",
			"the flaky test ran 20 times without failing",
		}},
		RevertPlan: HandBackSection{Entries: []string{"revert the two commits, then re-run the migration backwards"}},
		MorningNote: HandBackSection{Entries: []string{
			"what broke: the checkout path returned 500",
			"what was done: the nil guard is back",
			"what needs a proper fix: the cache's eviction",
		}},
		OpenQuestions: HandBackSection{Entries: []string{"should the metric keep its old name?"}},
	}
}

// A first run's hand-back is its pr.md, as the runner reads it: the parser is
// handed the body rawPRText built from that file, already redacted, and reads
// every section out of it.
//
// Mutation (match a heading only at column zero, or read the first line as a
// section): the sections of a pr.md body — the file the agent wrote, minus
// the title line rawPRText cut off — no longer parse, and this test fails.
func TestHandBackParsesEverySectionFromPRFile(t *testing.T) {
	dir := t.TempDir()
	body := "# Fix the flaky checkout test\n\n" + handBackFixture
	if err := os.WriteFile(filepath.Join(dir, prFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &task.Spec{Repo: "acme/app", Task: "Fix the flaky checkout test"}
	_, prBody := prTitleRun(dir, spec, "20261018-120000-abcd").rawPRText()

	hb, err := parseHandBack(prBody, true)
	if err != nil {
		t.Fatalf("parseHandBack(pr.md) = %v", err)
	}
	if want := handBackWant(); !reflect.DeepEqual(hb, want) {
		t.Fatalf("the hand-back pr.md carries =\n%+v\nwant\n%+v", hb, want)
	}
}

// A follow-up writes followup.md instead (followup.SystemPromptLines), and
// its hand-back is the same six sections of the same file: the level reads
// what the run wrote, not which run wrote it.
//
// Mutation (read pr.md whatever the run is, as a caller that hardcodes the
// file would): a follow-up's own account parses to nothing.
func TestHandBackParsesEverySectionFromFollowupFile(t *testing.T) {
	dir := t.TempDir()
	// The title line a first run's file opens with is not one of the six, so
	// a follow-up's file that carries one — or any other preamble — parses
	// the same way.
	body := "# Fix the flaky checkout test\n\n" + handBackFixture
	if err := os.WriteFile(filepath.Join(dir, followupFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, followupFile))
	if err != nil {
		t.Fatal(err)
	}

	hb, err := parseHandBack(agent.Redact(string(data), nil), true)
	if err != nil {
		t.Fatalf("parseHandBack(followup.md) = %v", err)
	}
	if want := handBackWant(); !reflect.DeepEqual(hb, want) {
		t.Fatalf("the hand-back followup.md carries =\n%+v\nwant\n%+v", hb, want)
	}
}

// Every level uses a subset of the six, so a file that carries only the
// sections its level asks for is complete: the others are empty, and nothing
// about that is an error.
//
// Mutation (return an error for a section the text does not carry, or fill an
// absent section with something): a Pro run's honest file — deferred and
// nothing else — becomes a run that handed back nothing, and this test fails.
func TestHandBackSectionsTheFileOmitsAreEmpty(t *testing.T) {
	hb, err := parseHandBack("## Verification\n\n- the tests pass on the final commit\n", true)
	if err != nil {
		t.Fatalf("parseHandBack = %v, want no error for the sections the file omits", err)
	}
	want := HandBack{Verification: HandBackSection{Entries: []string{"the tests pass on the final commit"}}}
	if !reflect.DeepEqual(hb, want) {
		t.Fatalf("the hand-back =\n%+v\nwant\n%+v", hb, want)
	}

	// Only the contract's own heading opens a section, so a deeper one — the
	// subsection an agent might write instead — leaves the section empty
	// rather than filling it from somewhere else in the file.
	deeper, err := parseHandBack("### Deferred\n\n- a deferred item\n", true)
	if err != nil {
		t.Fatalf("parseHandBack(### Deferred) = %v", err)
	}
	if !reflect.DeepEqual(deeper, HandBack{}) {
		t.Fatalf("a deeper heading's hand-back =\n%+v\nwant nothing", deeper)
	}
}

// A file whose lines end with a carriage return parses the same as one that
// does not: the entry's own text is what a body prints and an item's title
// is read from, and a run whose agent wrote CRLF endings should not hand back
// entries with a stray carriage return in the middle of them.
//
// Mutation (trim only spaces and tabs from a continuation line): the entry
// keeps the carriage return the line ended with, and this test fails.
func TestHandBackReadsEitherLineEnding(t *testing.T) {
	hb, err := parseHandBack("## Deferred\r\n\r\n- one\r\n  - two\r\n", true)
	if err != nil {
		t.Fatalf("parseHandBack(CRLF) = %v", err)
	}
	want := HandBack{Deferred: HandBackSection{Entries: []string{"one\n  - two"}}}
	if !reflect.DeepEqual(hb, want) {
		t.Fatalf("the hand-back =\n%+v\nwant\n%+v", hb, want)
	}
}

// The hand-back comes from the file the agent wrote and never from the task
// text. A brief can carry a `## Revert plan` heading of its own — a plan
// document pasted as a task, a section the task quotes as an example — and
// the runner's fallback body embeds the brief verbatim under `## Task`
// (rawPRText), so a parser that read whatever text it was handed would let
// the task answer a Hotfix's revert plan and a Critical's verification.
//
// Mutation (drop the fromAgent guard in parseHandBack): the fallback body's
// own heading is read as the run's hand-back, and this test fails.
func TestHandBackOfTheFallbackBodyIsEmpty(t *testing.T) {
	brief := "# Task: keep the checkout up\n\n## Revert plan\n\n- the section the task text carries\n"
	spec := &task.Spec{Repo: "acme/app", Task: brief}
	_, body := prTitleRun(t.TempDir(), spec, "20261018-120000-abcd").rawPRText()

	// The text really does carry the heading: without this the test would
	// pass on a parser that reads no revert plan at all, whatever the flag
	// says.
	if hb, err := parseHandBack(body, true); err != nil || len(hb.RevertPlan.Entries) != 1 {
		t.Fatalf("the same text read as the agent's own file = %+v, %v; want the task's heading", hb, err)
	}

	hb, err := parseHandBack(body, false)
	if err != nil {
		t.Fatalf("parseHandBack(fallback) = %v", err)
	}
	if !reflect.DeepEqual(hb, HandBack{}) {
		t.Fatalf("the fallback body's hand-back =\n%+v\nwant nothing", hb)
	}
}

// A section over a bound keeps whole entries and records how many it left
// out, and never cuts one in half: half a deferred item, or a revert plan
// whose last step is missing, tells a reader something the agent did not say.
//
// Mutation (keep the entries that fit and skip the rest, or clip the
// section's text to the bound): the entries or the drop count change, and
// this test fails.
func TestHandBackBoundsASectionAndRecordsTheDrop(t *testing.T) {
	var b strings.Builder
	b.WriteString("## Deferred\n\n")
	for i := 0; i < maxHandBackEntries+5; i++ {
		fmt.Fprintf(&b, "- entry %d\n", i)
	}
	hb, err := parseHandBack(b.String(), true)
	if err != nil {
		t.Fatalf("parseHandBack(over the count) = %v", err)
	}
	if len(hb.Deferred.Entries) != maxHandBackEntries || hb.Deferred.Dropped != 5 {
		t.Fatalf("the section = %d entries, %d dropped, want %d and 5",
			len(hb.Deferred.Entries), hb.Deferred.Dropped, maxHandBackEntries)
	}
	if last := hb.Deferred.Entries[maxHandBackEntries-1]; last != fmt.Sprintf("entry %d", maxHandBackEntries-1) {
		t.Fatalf("the last kept entry = %q, want the %dth whole entry", last, maxHandBackEntries-1)
	}

	// Two entries of 20 KiB: the first is kept whole, the second would put the
	// section over the byte bound and is dropped.
	long := strings.Repeat("x", maxHandBackSectionBytes/2+1)
	hb, err = parseHandBack("## Open risks\n\n- "+long+"\n- "+long+"\n", true)
	if err != nil {
		t.Fatalf("parseHandBack(over the bytes) = %v", err)
	}
	if len(hb.OpenRisks.Entries) != 1 || hb.OpenRisks.Dropped != 1 {
		t.Fatalf("the section = %d entries, %d dropped, want 1 and 1",
			len(hb.OpenRisks.Entries), hb.OpenRisks.Dropped)
	}
	if got := hb.OpenRisks.Entries[0]; got != long {
		t.Fatalf("the kept entry is %d bytes, want the whole %d-byte entry", len(got), len(long))
	}

	// An entry that is over the bound on its own leaves the section empty and
	// is counted as dropped rather than cut down to the bound.
	hb, err = parseHandBack("## Verification\n\n- "+strings.Repeat("x", maxHandBackSectionBytes+1)+"\n", true)
	if err != nil {
		t.Fatalf("parseHandBack(one entry over the bound) = %v", err)
	}
	if len(hb.Verification.Entries) != 0 || hb.Verification.Dropped != 1 {
		t.Fatalf("the section = %d entries, %d dropped, want 0 and 1",
			len(hb.Verification.Entries), hb.Verification.Dropped)
	}
}

// The parser is handed redacted text — rawPRText runs agent.Redact over the
// agent's file before the pull request body is built from it — so an entry
// that quoted a credential arrives holding [REDACTED]: the parser is not a
// redaction point, and what reaches a body or an item is what the caller may
// publish.
//
// Mutation (call parseHandBack with the raw file text): LV4.3 files these
// entries as items and prints them in a pull request body, and the credential
// reaches both.
func TestHandBackEntriesArriveRedacted(t *testing.T) {
	const (
		credential = "handback-credential-4f7c"
		body       = "# Fix the flaky checkout test\n\n## Verification\n\n- ran the suite with the checkout credential " + credential + " exported\n"
	)
	hb, err := parseHandBack(agent.Redact(body, []string{credential}), true)
	if err != nil {
		t.Fatalf("parseHandBack = %v", err)
	}
	if len(hb.Verification.Entries) != 1 {
		t.Fatalf("the section = %+v, want one entry", hb.Verification)
	}
	if entry := hb.Verification.Entries[0]; strings.Contains(entry, credential) || !strings.Contains(entry, "[REDACTED]") {
		t.Fatalf("the entry = %q, want the credential redacted out of it", entry)
	}
	// Redaction removes the value, not the sentence it sat in.
	if want := "ran the suite with the checkout credential [REDACTED] exported"; hb.Verification.Entries[0] != want {
		t.Fatalf("the entry = %q, want %q", hb.Verification.Entries[0], want)
	}
}

// A section named twice is a refusal rather than a merge: two lists under one
// name are two answers to one question, and only the agent knew which it
// meant, so the runner reports the file instead of guessing — LV4.3 files
// these entries and LV5.1 reads these sections, and both would be acting on
// an answer nobody wrote.
//
// Mutation (append to the section on the second heading, as one map lookup
// would): the run carries a merged list and no error, and this test fails.
func TestHandBackRefusesASectionNamedTwice(t *testing.T) {
	_, err := parseHandBack("## Deferred\n\n- one\n\n## deferred\n\n- two\n", true)
	if err == nil || !strings.Contains(err.Error(), handBackDeferred) {
		t.Fatalf("parseHandBack = %v, want a refusal naming the section", err)
	}
}

// The section names are one contract written in two packages: the follow-up
// prompt's agent writes the names, and this package's parser reads them. A
// heading named in one and not the other is a section no run ever hands back
// (a prompt that spells a name nothing matches) or a section nothing writes
// (a parser entry no agent was told about).
//
// Mutation (rename one name in handBackHeadings, or drop one from
// SystemPromptLines): the two lists disagree, and this test fails.
func TestFollowUpPromptNamesTheHandBackSections(t *testing.T) {
	prompt := strings.Join(followup.SystemPromptLines(followup.PromptData{PR: 7, StateDir: "/work/state"}), "\n")
	for _, h := range handBackHeadings {
		if !strings.Contains(prompt, "`## "+h+"`") {
			t.Errorf("the follow-up prompt does not name the %q section:\n%s", h, prompt)
		}
	}
	if n := strings.Count(prompt, "`## "); n != len(handBackHeadings) {
		t.Errorf("the follow-up prompt names %d sections, want %d:\n%s", n, len(handBackHeadings), prompt)
	}
}
