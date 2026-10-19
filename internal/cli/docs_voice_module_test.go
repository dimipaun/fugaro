package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestDocsVoiceModuleFolderIsWritten: docs/voice-module/ is the owner's voice
// module spec of 2026-10-18 and its reconciliation against this repository.
// The README must carry the status, the owner, the date and the verdict on
// typographical corrections; spec.md must be the owner's text, so the
// sentences below are asserted verbatim (a paraphrase, an editor's tidy-up or
// a dropped bullet fails here) and the whole file is pinned by its SHA-256,
// which is what "byte-for-byte" means; the reconciliation must carry the table, the
// findings, the resolutions, the owner questions and the verification
// sections, and must name the commit it was checked against.
func TestDocsVoiceModuleFolderIsWritten(t *testing.T) {
	readme := readDoc(t, "docs/voice-module/README.md")
	for _, want := range []string{
		// Status, owner and date, the three facts the brief names.
		"**reconciliation only; nothing is implemented.**",
		"Dimi Paun",
		"2026-10-19",
		// The two files it indexes, and the commit the spec was committed in.
		"[spec.md](spec.md)",
		"[reconciliation.md](reconciliation.md)",
		"eea279f1",
		// The owner's answers of 2026-10-19, and the rule that goes with them:
		// an answer is not approval, and nothing is built on one.
		"all thirteen answered by the owner on 2026-10-19",
		"an answer is not approval of a plan or of any code",
		// The verdict on the text: the owner's spec needed no correction.
		"## Typographical corrections",
		"**None.**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("docs/voice-module/README.md lacks %q", want)
		}
	}
	// The index must not describe its own artifact falsely. spec.md is the
	// spec itself, with no wrapper: the delimiters the request used to mark the
	// text are that request's quoting convention, and neither the file nor the
	// README describes them as being in it (a review caught the README claiming
	// the text sat "between the markers", which no file in this repository has).
	for _, banned := range []string{"SPEC TEXT", "==="} {
		if strings.Contains(readme, banned) {
			t.Errorf("docs/voice-module/README.md claims the spec carries %q; spec.md has no marker line and the index must describe the file it indexes", banned)
		}
	}
	// And it must not still be asking for answers it has: the owner answered all
	// thirteen on 2026-10-19, and the index is what a reader sees first.
	for _, banned := range []string{
		"Q2–Q13 still open",
		"Q2–Q13 stay open",
		"The spec's open questions",
		"not to be built on",
	} {
		if strings.Contains(readme, banned) {
			t.Errorf("docs/voice-module/README.md still says a question is open: %q", banned)
		}
	}

	spec := readDoc(t, "docs/voice-module/spec.md")
	for _, want := range []string{
		"# Fugaro Voice Module Spec",
		"Oct 18, 2026 · Dimi Paun",
		// The Goal, including the foreground-only rule.
		"App stays in the foreground while talking (no background listening).",
		// The architecture: a pipeline, and one interface per direction.
		"Pipeline, not speech-to-speech: speech-to-text → orchestrator/LLM → text-to-speech.",
		"One common interface per direction (audio frames in → partial/final transcripts out; text in → audio stream out).",
		// The engines and the wake word.
		`wake word "Fugaro" via openWakeWord or Porcupine routes speech to the app vs the active agent.`,
		"Local: Kokoro on all platforms (one voice, one code path); Apple voices as fallback",
		// The echo-cancellation rule that forces the audio graph to be one graph.
		"TTS must play through the same audio pipeline the canceller monitors (not a separate native player).",
		// The credential rule and the on-demand digest.
		"Security service mints short-lived temporary tokens per session (Deepgram, AssemblyAI, Cartesia support this); clients stream directly to providers. No long-lived keys on devices.",
		`On-demand digest: "what's going on?"`,
		// The phone item with no home in the mobile plan.
		"Keep the screen awake on the voice screen.",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/voice-module/spec.md lacks %q (the spec must be verbatim)", want)
		}
	}
	// And nothing else: no delimiter line, no wrapper. The file is the spec.
	for _, line := range strings.Split(spec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "===") {
			t.Errorf("docs/voice-module/spec.md carries the delimiter line %q: the quoting markers belong to the request that carried the text, not to the document", strings.TrimSpace(line))
		}
	}
	// "Byte-for-byte" is not a figure of speech. The README promises spec.md is
	// exactly the owner's text and names the commit (eea279f1) that carried it,
	// and no list of sentences can keep that promise: an edit to any other line
	// of the 61-line file would pass the checks above. So the whole file is
	// pinned by its SHA-256. The checks above stay because they say *what* the
	// spec has to say when they fail; this one says the file was touched at all.
	// Changing spec.md means the owner's text changed: re-read it, update the
	// constant, and record in the reconciliation what changed and why.
	const specSHA256 = "26a2e8843b0a195657c2785cff586f86cd520003c788fc7a17649e64a13e96dd"
	specSum := sha256.Sum256([]byte(spec))
	if got := hex.EncodeToString(specSum[:]); got != specSHA256 {
		t.Errorf("docs/voice-module/spec.md is not the text committed verbatim at eea279f1 (sha256 %s, want %s): the README promises it is byte-for-byte the owner's, so an edit here needs the owner", got, specSHA256)
	}

	recon := readDoc(t, "docs/voice-module/reconciliation.md")
	for _, want := range []string{
		// The commit and the date the reconciliation was checked against.
		"82ba600d984a420c595cf06417af6d781c18535a",
		"2026-10-19",
		// The sections the brief names, by their headings.
		"## 2. The table: what the spec asks for, and what the tree already has",
		"## 4. Resolutions",
		"## 5. The owner's questions — all thirteen answered on 2026-10-19",
		// The owner's answer of 2026-10-19, right after the questions it
		// answers, and the two things it has to state.
		"## 5a. Owner's answers",
		"### The answers to Q2–Q13, 2026-10-19",
		"### What it changes in this spec's sections",
		"### What the answer does not do",
		"**Q1 — answered 2026-10-19",
		"**An answer is not approval of a plan or of any code**",
		"## 6. Verification: the providers' public documentation, 2026-10-19",
		"## 7. UNVERIFIED",
		// The five classifications, and the standing of the unverifiable ones.
		"**Wrong**",
		"**Exists**",
		"**Gap**",
		"**Rule**",
		"**Open**",
		"UNVERIFIED",
		// The gate the brief puts on everything: the owner answers first.
		"reconciliation only; nothing is implemented",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("docs/voice-module/reconciliation.md lacks %q", want)
		}
	}

	// Two claims in this document were wrong about the text of the document they
	// describe, and both are pinned the way they failed. The design's own §11 is
	// the authority on what its first revision's questions became -- six
	// consolidated into four, the first revision's Q1 no longer written as a
	// question, only Q4 carried forward -- and spec.md states the foreground rule
	// once, in the *Goal*. A reader who is told the questions were kept, or that
	// the *Goal* says something twice, checks a design and a spec that say
	// otherwise.
	for _, want := range []string{
		"consolidated its first revision's six questions into four",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("docs/voice-module/reconciliation.md lacks %q: §1 has to state what the design's own §11 records about its first revision's questions", want)
		}
	}
	for _, banned := range []string{
		"kept Q1–Q3",
		// The *Goal* is named rather than "the spec", so the ban holds wherever
		// in a sentence the claim comes back.
		"*Goal* states it twice",
		"*Goal* says twice",
	} {
		if strings.Contains(recon, banned) {
			t.Errorf("docs/voice-module/reconciliation.md says %q: the design's §11 says its six questions were consolidated into four and only Q4 was carried forward, and spec.md states the foreground rule exactly once", banned)
		}
	}

	// The README's list of the checks this module touches and finding #44's must
	// be the same list, in both directions: the index says what the document
	// says, and a check in one and not the other is how "103" went missing from
	// the index -- while a README naming a check #44 does not list is the other
	// half of the same drift, and is asserted here too. Both lists are read out
	// of the documents rather than repeated here: #44 states its checks in bold
	// and so does the README's sentence, so a number is matched as a whole token
	// and never as a substring of another number or of a date.
	i := strings.Index(recon, "**#44 —")
	if i < 0 {
		t.Fatal("docs/voice-module/reconciliation.md has no finding #44 to read the check list from")
	}
	finding44 := recon[i:]
	if j := strings.Index(finding44, "\n## "); j >= 0 {
		finding44 = finding44[:j]
	}
	// Drop the heading line, whose own bold run carries the finding's number.
	if j := strings.Index(finding44, "\n"); j >= 0 {
		finding44 = finding44[j:]
	}

	j := strings.Index(readme, "The checks this module touches")
	if j < 0 {
		t.Fatal("docs/voice-module/README.md has no sentence saying which checks this module touches")
	}
	readmeChecks := readme[j:]
	if k := strings.Index(readmeChecks, "\n\n"); k >= 0 {
		readmeChecks = readmeChecks[:k]
	}

	inFinding := boldCheckNumbers(finding44)
	inReadme := boldCheckNumbers(readmeChecks)
	if len(inFinding) < 5 {
		t.Fatalf("finding #44 names only %d checks in bold; the parse or the document changed shape", len(inFinding))
	}
	for _, n := range sortedCheckNumbers(inFinding) {
		if !inReadme[n] {
			t.Errorf("docs/voice-module/README.md does not list check %s, which finding #44 lists for this work", n)
		}
	}
	for _, n := range sortedCheckNumbers(inReadme) {
		if !inFinding[n] {
			t.Errorf("docs/voice-module/README.md lists check %s, which finding #44 does not: the index and the document have drifted apart", n)
		}
	}
}

// TestDocsVoiceModuleReconciliationAllocatesNoDecisions: the brief's rule is
// that a finding needing the owner becomes a Q-row with options, a
// recommendation first and its consequences -- and that no decision number is
// allocated by this document (this repository's D-numbers are the owner's to
// hand out). Q1 must be the gate (the second explicit egress decision D44
// reserved), Q13 the last, and Q14 must not exist.
//
// "Recommendation first" is checked PER QUESTION, on the first option bullet of
// each Q-block. A document-wide search for the marker would pass while twelve
// of the thirteen questions had lost theirs, which is what an earlier revision
// of this test did.
func TestDocsVoiceModuleReconciliationAllocatesNoDecisions(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)
	for _, want := range []string{
		"### Q1 —",
		"### Q2 —",
		"### Q3 —",
		"### Q4 —",
		"### Q5 —",
		"### Q6 —",
		"### Q7 —",
		"### Q8 —",
		"### Q9 —",
		"### Q10 —",
		"### Q11 —",
		"### Q12 —",
		"### Q13 —",
		// The statement of the rule, in the document's own words.
		"No D-number is allocated",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	if strings.Contains(recon, "### Q14 —") {
		t.Error("docs/voice-module/reconciliation.md grew a Q14; re-read this test and the document's numbering rule")
	}

	// Every question recommends first, and this is the per-question half.
	questions := regexp.MustCompile(`(?m)^### Q\d+ — .*$`).FindAllStringIndex(recon, -1)
	if len(questions) == 0 {
		t.Fatal("no owner question headings found: the parse or the document changed shape")
	}
	for i, q := range questions {
		heading := recon[q[0]:q[1]]
		block := recon[q[1]:]
		if i+1 < len(questions) {
			block = recon[q[1]:questions[i+1][0]]
		} else if j := strings.Index(block, "\n## "); j >= 0 {
			block = block[:j]
		}
		first := strings.Index(block, "\n- **(")
		if first < 0 {
			t.Errorf("%s: %s has no option bullet", path, heading)
			continue
		}
		bullet := block[first+1:]
		if end := strings.Index(bullet, "\n- **("); end >= 0 {
			bullet = bullet[:end]
		}
		if !strings.Contains(bullet, "*(Recommended.)*") {
			t.Errorf("%s: %s does not recommend first: its first option bullet lacks %q", path, heading, "*(Recommended.)*")
		}
	}
}

// optionMentionRE reads an option by the name the document gives it in prose:
// "option (1)". It is deliberately not the bullets' own "**(1)" form -- the
// answer paragraphs and §5a's table cells say "option (N)", and the bullets are
// the options themselves.
var optionMentionRE = regexp.MustCompile(`(?i)option \((\d+)\)`)

// optionNumbers lists the options a piece of prose names, in reading order.
func optionNumbers(s string) []int {
	var out []int
	for _, m := range optionMentionRE.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// TestDocsVoiceModuleReconciliationRecordsEveryAnswer: the owner answered all
// thirteen questions on 2026-10-19, and each answer is the option the question
// recommended, recorded rather than designed. The same fact is written in three
// places, so all three are read and compared:
//
//   - the question's own heading in §5 carries the marker with the date;
//   - the answer paragraph under it names the option;
//   - §5a's table carries one row per question, naming the same option.
//
// A question that loses its marker, an answer that names an option the question
// does not recommend, and a §5a row that disagrees with the question it
// summarises are three different drifts, and each fails here. So does an answer
// that cites a finding the option it records does not cite: the answers are the
// owner's, not new design, and a new finding number in one would be new design.
//
// Q1 is exempt from the option check, and the exemption is the point: its
// answer is not one of the options it was asked with -- the owner approved the
// evaluation switch instead -- so its marker names no option and the check
// would have nothing to compare.
func TestDocsVoiceModuleReconciliationRecordsEveryAnswer(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)

	// Whatever the heading or the paragraph claims, the document may not go
	// back to saying these questions are open. Each string below is a sentence
	// that was true before the answers of 2026-10-19 and false after them.
	for _, banned := range []string{
		"§5's questions are the gate",
		"come first and are the gate",
		"No other question is answered here",
		"It does not answer Q2–Q13",
		"leaves every question *inside* that frame open",
		"§5 Q9 is the question about supersession",
		"§5 Q6 asks whether it is worth buying",
		"§5 Q12 asks which identity",
		"still open, unanswered",
	} {
		if strings.Contains(recon, banned) {
			t.Errorf("%s still says a question is open: %q", path, banned)
		}
	}
	if regexp.MustCompile(`§5 Q\d+ asks`).MatchString(recon) {
		t.Errorf("%s still has a finding pointing at §5 in the present tense (\"§5 Qn asks\"): the questions are answered, and a pointer that says otherwise sends a reader to look for an answer that is already there", path)
	}

	questions := regexp.MustCompile(`(?m)^### Q(\d+) — .*$`).FindAllStringIndex(recon, -1)
	if len(questions) == 0 {
		t.Fatal("no owner question headings found: the parse or the document changed shape")
	}
	answeredWith := map[int]int{}
	for i, q := range questions {
		heading := recon[q[0]:q[1]]
		block := recon[q[1]:]
		if i+1 < len(questions) {
			block = recon[q[1]:questions[i+1][0]]
		} else if j := strings.Index(block, "\n## "); j >= 0 {
			block = block[:j]
		}
		m := regexp.MustCompile(`^### Q(\d+)`).FindStringSubmatch(heading)
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: unparsable question number in %q", path, heading)
		}
		if !strings.Contains(heading, "*Answered by the owner on 2026-10-19") || !strings.HasSuffix(heading, "*") {
			t.Errorf("%s: Q%d's heading does not carry the answered marker: it must name the date the owner answered and where the answer is recorded", path, n)
		}

		// The question's own recommendation, read from its bullets.
		recommended, recommendedText := 0, ""
		for _, piece := range strings.Split(block, "\n- **(")[1:] {
			cut := strings.Index(piece, ")")
			if cut < 0 {
				continue
			}
			num, err := strconv.Atoi(piece[:cut])
			if err != nil {
				continue
			}
			if strings.Contains(piece, "*(Recommended.)*") {
				if recommended != 0 {
					t.Errorf("%s: Q%d marks two options *(Recommended.)*: (%d) and (%d)", path, n, recommended, num)
				}
				recommended, recommendedText = num, piece
			}
		}
		if recommended == 0 {
			t.Errorf("%s: Q%d has no option marked %q, so there is no recommendation to answer with", path, n, "*(Recommended.)*")
		}

		mi := strings.Index(block, "**Answered, 2026-10-19:")
		if mi < 0 {
			t.Errorf("%s: Q%d has no answer paragraph: every answered question carries %q under its heading", path, n, "**Answered, 2026-10-19: ...**")
			continue
		}
		para := block[mi:]
		if j := strings.Index(para, "\n\n"); j >= 0 {
			para = para[:j]
		}
		got := optionNumbers(para)
		if n == 1 {
			if len(got) != 0 {
				t.Errorf("%s: Q1's answer names option (%d), but its answer is not one of the options it was asked with", path, got[0])
			}
		} else {
			switch {
			case len(got) == 0:
				t.Errorf("%s: Q%d's answer names no option: it must record which of the options below the owner took", path, n)
			case len(got) > 1:
				t.Errorf("%s: Q%d's answer names %d options %v: an answer is one option, not a range", path, n, len(got), got)
			case got[0] != recommended:
				t.Errorf("%s: Q%d's answer is option (%d); the question recommends option (%d)", path, n, got[0], recommended)
			default:
				answeredWith[n] = got[0]
			}
		}

		// The answer is the option recorded, not a new design: a finding the
		// answer cites has to be one the option itself cites. Three-digit
		// numbers are pull requests (the convention the rest of this file uses).
		for _, m := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(para, -1) {
			cite, err := strconv.Atoi(m[1])
			if err != nil || cite >= 100 {
				continue
			}
			if !strings.Contains(recommendedText, "#"+m[1]) {
				t.Errorf("%s: Q%d's answer cites #%d, which the option it records does not cite: the answer records the option, it does not extend it", path, n, cite)
			}
		}
	}

	// §5a's table: the answers read together, one row per question.
	i := strings.Index(recon, "### The answers to Q2–Q13, 2026-10-19")
	if i < 0 {
		t.Fatal("docs/voice-module/reconciliation.md has no §5a table of the Q2–Q13 answers")
	}
	table := recon[i:]
	if j := strings.Index(table, "\n### "); j >= 0 {
		table = table[:j]
	}
	rows, seen := 0, map[int]bool{}
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "| **Q") {
			continue
		}
		rows++
		cells := markdownTableCells(line)
		if len(cells) < 2 {
			t.Errorf("%s: the §5a row %q has %d cells, so it has no answer cell", path, strings.TrimSpace(line), len(cells))
			continue
		}
		m := regexp.MustCompile(`\*\*Q(\d+)\*\*`).FindStringSubmatch(cells[0])
		if m == nil {
			t.Errorf("%s: the §5a row %q does not start with a question number", path, strings.TrimSpace(cells[0]))
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: unparsable question number in the §5a row %q", path, strings.TrimSpace(cells[0]))
		}
		if seen[n] {
			t.Errorf("%s: §5a has two rows for Q%d", path, n)
		}
		seen[n] = true
		if n < 2 || n > 13 {
			t.Errorf("%s: §5a's table carries Q%d; it is the table of the Q2–Q13 answers", path, n)
		}
		got := optionNumbers(cells[1])
		if len(got) != 1 {
			t.Errorf("%s: the §5a row for Q%d names %d options %v, want exactly 1", path, n, len(got), got)
			continue
		}
		if want, ok := answeredWith[n]; ok && got[0] != want {
			t.Errorf("%s: §5a says Q%d took option (%d); §5's own answer for it says option (%d)", path, n, got[0], want)
		}
	}
	if want := len(questions) - 1; rows != want {
		t.Errorf("%s: §5a's table has %d rows and §5 has %d questions besides Q1: every answer must be in both places", path, rows, want)
	}
	for n := 2; n <= 13; n++ {
		if !seen[n] {
			t.Errorf("%s: §5a's table has no row for Q%d", path, n)
		}
	}
}

// voiceModuleRowAudit is one row of the hand audit behind
// TestDocsVoiceModuleReconciliationFindingsResolve: a distinctive substring of
// the row's first cell, and the finding numbers that discuss that row.
type voiceModuleRowAudit struct {
	row      string
	findings []int
}

// voiceModuleSection1Audit is §1's table: which findings back each of the
// design's own questions. Derived by reading §3's findings, not by trusting the
// table -- which is how six of §2's citations were found to name findings about
// something else.
var voiceModuleSection1Audit = []voiceModuleRowAudit{
	// Q0 is the design's own gate; the spec is silent on it and no finding
	// covers it.
	{"**Q0** —", nil},
	{"**Q1** —", []int{20, 31}},
	{"**Q2** —", []int{28, 29, 30, 31, 32}},
	{"**Q3** —", []int{8, 19}},
	{"**Q4** —", []int{44}},
}

// voiceModuleSection2Audit is §2's table, one entry per spec concept, in the
// spec's own order.
var voiceModuleSection2Audit = []voiceModuleRowAudit{
	{"Hands-free voice interface", []int{1}},
	{"Pipeline, not speech-to-speech", []int{1, 20}},
	{"One common interface per direction", []int{2}},
	{"a separable module", []int{3}},
	{"App stays in the foreground", []int{4}},
	{"Apple SpeechAnalyzer", []int{5, 33}},
	{"Android on-device SpeechRecognizer", []int{20}},
	{"whisper.cpp on Linux/Windows", []int{20}},
	{"Deepgram and AssemblyAI", []int{6}},
	{"Silero VAD", []int{21}},
	{"Wake word", []int{7, 34, 35}},
	{"Kokoro on all platforms", []int{20, 39}},
	{"Cartesia; ElevenLabs later", []int{6, 31}},
	{"A cloud icon on the voice screen", []int{8, 19}},
	{"Record raw session audio", []int{22}},
	{"Log latency to first partial", []int{41}},
	{"Provider keys at project level", []int{28, 29}},
	{"Short-lived temporary tokens", []int{14, 30, 31}},
	{"Rules-based classifier", []int{9}},
	{"Debounce window", []int{10}},
	{"A small fast LLM (Haiku)", []int{11, 26}},
	{"On-demand digest", []int{10}},
	{"Interrupt switch", []int{12}},
	{"Echo cancellation", []int{37, 38}},
	{"Phone: routing, interruptions", []int{42}},
	{"No destructive voice commands", []int{18, 43}},
}

// TestDocsVoiceModuleReconciliationFindingsResolve: every "#N" the document
// cites must be a finding §3 defines, and each row of §1's and §2's tables must
// cite exactly the findings that discuss that row. The second half is the one
// that catches a real defect rather than a typo: the document once cited #33 --
// Apple's OS 26 floor -- for the Haiku summariser, and #36 -- the
// dependency-licence gap -- for the provider retention register, and both are
// numbers that exist, so only a per-row audit finds them.
//
// The audit runs in both directions, and the second direction is the one that
// keeps it honest: every audit entry must name a row (the table did not move
// out from under it) and every row must be named by exactly one audit entry. A
// row added or reworded with no entry would otherwise escape the audit
// entirely, since the entries are what the test iterates. Rows are read in
// document order and a first cell may not repeat, so the entry a citation is
// judged against is never a matter of map iteration order.
func TestDocsVoiceModuleReconciliationFindingsResolve(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)

	// §3's findings, parsed from their headings. They run #1..#N with no gap
	// and no repeat: a document that cites #45 when findings stop at #44 fails
	// here, and so does one that renumbers a finding.
	titles := map[int]string{}
	for _, m := range regexp.MustCompile(`(?m)^\*\*#(\d+) — (.+?) \([^)]*\)\*\*$`).FindAllStringSubmatch(recon, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: unparsable finding number %q", path, m[1])
		}
		if _, dup := titles[n]; dup {
			t.Errorf("%s: finding #%d is defined twice", path, n)
		}
		titles[n] = m[2]
	}
	if len(titles) == 0 {
		t.Fatal("no finding headings parsed: the parse or the document changed shape")
	}
	for n := 1; n <= len(titles); n++ {
		if _, ok := titles[n]; !ok {
			t.Errorf("%s: finding #%d is missing; the findings must run #1..#%d with no gap", path, n, len(titles))
		}
	}

	// Every "#N" in the prose is a finding that exists. The document writes
	// pull-request numbers as three-digit numbers -- the oldest in flight here
	// is #584 -- so a one- or two-digit "#N" is a finding reference and must be
	// one §3 defines. That is the half which catches "#45" in a document whose
	// findings stop at #44.
	for _, m := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatchIndex(recon, -1) {
		n, err := strconv.Atoi(recon[m[2]:m[3]])
		if err != nil {
			t.Fatalf("%s: unparsable citation %q", path, recon[m[0]:m[1]])
		}
		if n >= 100 {
			continue
		}
		if _, ok := titles[n]; !ok {
			t.Errorf("%s cites #%d, which §3 does not define: the findings run #1..#%d", path, n, len(titles))
		}
	}

	// §1's and §2's tables, row by row, and both halves of the audit: every
	// audit entry must name a row (the table did not move out from under it),
	// and every row must be named by exactly one audit entry (a row added or
	// reworded later does not escape the citation audit by having no entry).
	section := func(heading, next string) string {
		i := strings.Index(recon, heading)
		if i < 0 {
			t.Fatalf("%s lacks the heading %q", path, heading)
		}
		j := strings.Index(recon[i:], next)
		if j < 0 {
			t.Fatalf("%s lacks %q after %q", path, next, heading)
		}
		return recon[i : i+j]
	}
	// A row is an ordered list of cells, read with the escape-aware splitter: a
	// pipe inside a cell has to be written `\|`, and it comes back as the pipe
	// it stands for. cellIdx is counted from the start when it is not negative
	// and from the end when it is, over the real cells -- §1's finding
	// citations live in its middle cell, §2's in its last.
	type tableRow struct{ concept, cell string }
	rows := func(label, body string, cellIdx int) []tableRow {
		var out []tableRow
		seen := map[string]int{}
		prevTable := false
		for n, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "|") {
				prevTable = false
				continue
			}
			header := !prevTable
			prevTable = true
			cells := markdownTableCells(line)
			if header || isTableSeparator(cells) {
				continue
			}
			i := cellIdx
			if i < 0 {
				i += len(cells)
			}
			if i < 0 || i >= len(cells) {
				t.Errorf("%s: the %s row %q has %d cells, so it has no cell %d: the row and the column moved apart", path, label, strings.TrimSpace(cells[0]), len(cells), cellIdx)
				continue
			}
			if first, dup := seen[cells[0]]; dup {
				t.Errorf("%s: the %s table has two rows whose first cell is %q (section lines %d and %d), so the audit cannot tell which one a citation came from", path, label, strings.TrimSpace(cells[0]), first, n)
				continue
			}
			seen[cells[0]] = n
			out = append(out, tableRow{cells[0], cells[i]})
		}
		return out
	}
	audit := func(label, body string, cellIdx int, wants []voiceModuleRowAudit) {
		table := rows(label, body, cellIdx)
		if len(table) != len(wants) {
			t.Errorf("%s: %s has %d rows and the audit has %d entries: every row must be audited, and every entry must name a row", path, label, len(table), len(wants))
		}
		matched := map[int]bool{}
		for _, row := range table {
			var hits []int
			for wi, w := range wants {
				if strings.Contains(row.concept, w.row) {
					hits = append(hits, wi)
				}
			}
			if len(hits) != 1 {
				t.Errorf("%s: the %s row %q matches %d audit entries, want exactly 1: a row the audit cannot place is a row whose citations nothing checks", path, label, strings.TrimSpace(row.concept), len(hits))
				continue
			}
			wi := hits[0]
			matched[wi] = true
			var got []int
			for _, m := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(row.cell, -1) {
				n, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("%s: unparsable citation %q in the %s row %q", path, m[1], label, row.concept)
				}
				got = append(got, n)
			}
			slices.Sort(got)
			want := slices.Clone(wants[wi].findings)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s: the %s row %q cites %v; the audit, read from §3's findings, expects %v", path, label, row.concept, got, want)
			}
			for _, n := range got {
				if _, ok := titles[n]; !ok {
					t.Errorf("%s: the %s row %q cites #%d, which §3 does not define", path, label, row.concept, n)
				}
			}
		}
		for wi, w := range wants {
			if !matched[wi] {
				t.Errorf("%s: no row of %s matches the audit entry %q; the table or the audit moved", path, label, w.row)
			}
		}
	}
	audit("§1", section("## 1. ", "## 2. "), 1, voiceModuleSection1Audit)
	audit("§2", section("## 2. ", "## 3. "), -1, voiceModuleSection2Audit)

	// The register the brief caught: the design's provider retention-and-training
	// register is a condition on the cloud rows, and the findings that carry it
	// are the summariser's ungoverned egress (#26) and the credentials service's
	// jobs-only scope (#28) -- not the dependency-licence gap (#36), which is
	// what an earlier revision of §1's table cited for it. This is prose, not a
	// table row, so it is pinned by its own paragraph rather than by the audit.
	i := strings.Index(recon, "**One condition in the design is not one of its questions")
	if i < 0 {
		t.Fatal("§1's paragraph stating the register as a condition is gone")
	}
	para := recon[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, want := range []string{"(#26)", "(#28)"} {
		if !strings.Contains(para, want) {
			t.Errorf("%s: §1's register paragraph does not name %s, the finding behind it", path, want)
		}
	}
	if strings.Contains(para, "#36") {
		t.Error("§1's register paragraph cites #36, which is the dependency-licence gap: the register's findings are #26 and #28")
	}
}

// TestDocsVoiceModuleReconciliationCitationsExist: the reconciliation's
// load-bearing citations are paths a reader has to be able to open. Every
// on-main path below is stat'ed, not merely required as text, so a document
// that is renamed out from under §1 or §2 fails here.
//
// Three cited paths are deliberately NOT stat'ed: docs/mobile/design/voice-evaluation-switch.md
// (PR #633), docs/design/console-styling.md (PR #634) and
// docs/design/project-named-oauth-tokens.md (PR #584) exist only on their
// branches, not on this tree's main. The document says so in its own words and
// this test pins that disclosure, so a reader who finds the link dead is told
// why rather than left to guess -- the same convention
// internal/cli/docs_budget_token_channel_test.go uses for `main`'s files.
func TestDocsVoiceModuleReconciliationCitationsExist(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)
	for _, cited := range []string{
		// The three documents this spec revises or depends on.
		"docs/design/voice-spec.md",
		"docs/design/voice.md",
		"docs/credentials-service/",
		// The phone work and its reconciliation.
		"docs/mobile/README.md",
		"docs/mobile/implementation-plan.md",
		"docs/mobile/functional-spec-reconciliation.md",
		// The console's security model and its one write. Its feed contract is
		// cited by the reconciliation as well, but deliberately NOT stat'ed
		// here: docs_console_boundary_test.go's consoleBoundaryAllowList is a
		// ONE-entry exception -- internal/cli/docs_desktop_console_test.go, the
		// file that reads that contract as bytes -- and a sub-test refuses a
		// second entry, so no path under the console directory may be written
		// into this file at all, not even inside a comment. The contract's own
		// existence is that test's business.
		"docs/design/desktop-console.md",
		"docs/design/desktop-console-v2.md",
		// The plan the earlier spec's reconciliation points at, and the register.
		"docs/plans/2026-10-16-voice.md",
		"docs/gcp-live-checklist.md",
		// The credentials-service documents the "security service" turns out to be.
		"docs/credentials-service/spec.md",
		// The credential machinery the minting precedent lives in.
		"docs/design/named-oauth-tokens.md",
		"docs/design/project-secrets.md",
		"docs/design/m9-budget-and-dashboard.md",
		// The repository's own licence and security posture.
		"SECURITY.md",
		"LICENSE",
	} {
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %q, which does not exist at this commit: %v", path, cited, err)
		}
	}
	for _, want := range []string{
		// The three branch-only paths, named, with the reason they are dead
		// links. project-named-oauth-tokens.md is the third: a review caught it
		// annotated as #584's base when it is on that branch and on neither its
		// base nor main.
		"../mobile/design/voice-evaluation-switch.md",
		"../design/console-styling.md",
		"../design/project-named-oauth-tokens.md",
		"**Three paths cited below are not on `main`",
		"will not resolve until their pull requests merge",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s does not disclose its branch-only citations (%q missing)", path, want)
		}
	}
}

// TestDocsVoiceModuleReconciliationRecordsTheAppliedConsoleWorkflow: the console
// release workflow is not a proposal waiting to be copied in. The owner copied
// the staged file in as plan Task 4.5 (7fdfa61, 2026-10-17) and main carries the
// revision merged as 9b61d881 (PR #626), whose first tag is v0.8.7; the copy
// under docs/pending-workflows/ is kept so that
// TestDocsConsoleReleaseWorkflowIsStagedAndPinned can hold the two files
// byte-identical, not because it is still pending. A finding that called it
// staged would tell the voice work that the pipeline its nested helper has to
// fit into does not exist yet, and the nested-helper consequence is read off
// that pipeline, so both halves are pinned: the verdict, and the facts about the
// pipeline the consequence rests on.
func TestDocsVoiceModuleReconciliationRecordsTheAppliedConsoleWorkflow(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)

	// The staging verdict, gone from the whole document. Both spellings: the
	// finding wrote it in capitals, and a document that says it in either is
	// wrong in the same way. The workflow's own header still carries a staging
	// note, so the document must not quote that note as the file's status.
	for _, banned := range []string{"STAGED, NOT APPLIED", "staged, not applied"} {
		if strings.Contains(recon, banned) {
			t.Errorf("%s says %q about .github/workflows/console-release.yml: the owner copied it in as plan Task 4.5 (7fdfa61) and main carries the #626 revision (9b61d881), so the file is applied", path, banned)
		}
	}

	finding := voiceModuleFinding(t, recon, 17)
	for _, want := range []string{
		// Applied, and the three commits that say so: the copy-in, the
		// revision main carries now, and the first tag that runs it.
		"applied, not staged",
		"7fdfa61",
		"9b61d881",
		"v0.8.7",
		// The staged copy is kept for a test, not because it is pending.
		"TestDocsConsoleReleaseWorkflowIsStagedAndPinned",
		"byte-identical",
		// The pipeline the nested-helper consequence is read off: notarisation
		// in a script outside the packer, the whole bundle submitted once, the
		// retry that wraps only the stapler calls, and the deep verification of
		// the bundle inside each shipped artifact.
		"notarize-and-staple.sh",
		"-c.mac.notarize=false",
		"ditto -c -k --keepParent",
		"retry_apple_network",
		"codesign --verify --deep --strict",
	} {
		if !strings.Contains(finding, want) {
			t.Errorf("%s: finding #17 does not state %q; the console release pipeline is applied and the nested-helper consequence is read off it", path, want)
		}
	}
}

// voiceModuleQuotation is one quotation the reconciliation makes, paired with
// the file it attributes those words to. A quotation that is not verbatim is
// the failure class a review of this document found twice -- the gateway was
// described in words the package does not contain, and the mailbox schema's
// size bound was quoted as "<=4096 cu" when the README says "1-4096 code
// units" -- so every pair is checked in both directions: the words must be in
// the document, and they must be in the source the document names. The quote
// is written here as the source has it; voiceModuleQuoteText below is what
// makes a quotation spanning two lines of a Go comment compare equal.
type voiceModuleQuotation struct {
	quote  string // the words, exactly as the source has them
	source string // the repo-relative path the document attributes them to
}

var voiceModuleQuotations = []voiceModuleQuotation{
	// #29: the gateway's shape is its own package doc's sentence, not a
	// paraphrase of it. The document said "a loopback, in-runner proxy that
	// swaps a short-lived token for the real key"; "in-runner" is in no file,
	// and the swap is not what the package says it does.
	{
		"a loopback proxy between Claude Code and the model API that holds the real credential",
		"internal/gateway/gateway.go",
	},
	// #29: the agent's side of the same mechanism, from the doc comment that
	// carries GatewayVars.
	{
		"never the real model credential",
		"internal/agent/env.go",
	},
	// #29: --project-wide's sentence is the comment on secretsOptions.projectWide
	// in the CLI, which is where a reader looking for the flag finds it. The
	// document cited docs/design/project-secrets.md section 4, which does not
	// carry this sentence.
	{
		"stores or lists the secret once per Fugaro project rather than per repository (design docs/design/project-secrets.md §4)",
		"internal/cli/secrets.go",
	},
	// #29: the reader rule is in that design's "Who reads" paragraph, not in
	// its section 4 -- the same design file, a different place in it.
	{
		"Only a job's own service account, granted `secretAccessor` **per secret, per job**, exactly as today",
		"docs/design/project-secrets.md",
	},
	// #27: the schema's size bound and the redaction promise, in the README's
	// own words.
	{
		"1–4096 code units",
		"shared/mailbox-schema/README.md",
	},
	{
		"**already redacted** when it arrives (P1.3)",
		"shared/mailbox-schema/README.md",
	},
	// #37: the spec's Cleanup line says "record", not "report". The finding
	// turns on the requirement being new, so the requirement's own verb is the
	// thing that has to be quoted correctly.
	{
		"record how much of the reply was actually heard and pass that to the agent",
		"docs/voice-module/spec.md",
	},
	// #25: voice.md D54 part (3)'s condition, in full: the document's shortened
	// "only if U7 finds it can surface a prompt" is not the sentence.
	{
		"only if U7 finds that a callback can surface a prompt the app can show and wait on",
		"docs/design/voice.md",
	},
	// #4 and #20: two finding titles that quoted the spec's Goal and its
	// pipeline box in words the spec does not use.
	{
		"App stays in the foreground while talking (no background listening)",
		"docs/voice-module/spec.md",
	},
	{
		"orchestrator/LLM",
		"docs/voice-module/spec.md",
	},
	// R13: the example utterance is the grammar's own cancel command, so the
	// rule it illustrates is illustrated with a phrase that exists.
	{
		"Fugaro, cancel that",
		"shared/command/README.md",
	},
}

// voiceModuleQuoteText folds a quotation and its source into one comparable
// line: the comment leaders that split a Go comment, the emphasis and code
// markers that wrap markdown words, and every run of whitespace. Without this
// a quotation is only checkable against a source that happens to wrap its
// lines the way the quoting document does.
func voiceModuleQuoteText(s string) string {
	s = strings.ReplaceAll(s, "//", " ")
	s = strings.NewReplacer("**", "", "`", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// TestDocsVoiceModuleReconciliationQuotesAreVerbatim: every quotation this
// document makes of a file in this repository is in that file, word for word.
// A review of an earlier revision found two quotations whose words the cited
// source does not contain -- the failure class is silent, because a quotation
// mark reads as evidence -- so the pairs are pinned, the wrong versions are
// banned, and the two citations #29 got wrong are pinned in their corrected
// form. Quotations of the providers' public documentation, of the brief that
// asked for this document, and of the document's own headings are outside this
// test by construction: the document says in section 0 which is which.
func TestDocsVoiceModuleReconciliationQuotesAreVerbatim(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)
	flat := voiceModuleQuoteText(recon)
	for _, q := range voiceModuleQuotations {
		want := voiceModuleQuoteText(q.quote)
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer quotes %q from %s", path, q.quote, q.source)
		}
		source := voiceModuleQuoteText(readDoc(t, q.source))
		if !strings.Contains(source, want) {
			t.Errorf("%s quotes %q as %s's words, and that file does not contain them", path, q.quote, q.source)
		}
	}
	// The wrong versions must not come back: each was a quotation the cited
	// source does not carry.
	for _, banned := range []string{
		"a loopback, in-runner proxy that swaps a short-lived token for the real key",
		"≤4096 cu",
		"report how much of the reply was actually heard",
		"only if U7 finds it can surface a prompt",
		`"Stays in the foreground, no background listening"`,
		`"The orchestrator/LLM"`,
		`"Summarising spoken lines must never include secrets"`,
		`"may this leave the machine at all, under what conditions, and how the answer is carried"`,
		`"the app needs a process"`,
		`"Fugaro, cancel it"`,
	} {
		if strings.Contains(recon, banned) {
			t.Errorf("%s carries %q again; that is a quotation its source does not contain, or this document's own words in quotation marks", path, banned)
		}
	}
	// The two citations #29 got wrong, pinned in the corrected form.
	for _, want := range []string{
		"the flag's own comment on `secretsOptions.projectWide`, in that file",
		`the reader rule from ` + "`docs/design/project-secrets.md`" + `'s "Who reads" paragraph`,
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q; #29 must cite where the quoted words actually are", path, want)
		}
	}
	// And section 0 must keep the audit's own accounting: which quotations are
	// outside the tree, and which renderings are the only ones used.
	for _, want := range []string{
		"**Quotations.** Every quotation in this document was checked word for word against its source",
		"**Six quotations were not verbatim and are corrected in this revision**",
		"**two citations were wrong**",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q; section 0 is where the quotation audit is accounted for", path, want)
		}
	}
}

// TestDocsVoiceModuleReconciliationPinsItsBaseline: the document's baseline is
// one commit, and it says which of its citations that commit does not carry.
// A review found the framing claiming "every path exists there" while finding
// #17 names the console build's notarize-and-staple.sh script, which #626 added
// after 82ba600d -- so the exception is named in section 0 and again in #17, and the
// README's baseline row says it too. The same paragraph's claim about the
// mobile plan's phases was wrong in the other direction: Phase 3 is the app,
// and its merged part is under mobile/src/ (P3.1: the skeleton and the account
// store), not under shared/, which is where Phases 0 and 1 live.
func TestDocsVoiceModuleReconciliationPinsItsBaseline(t *testing.T) {
	const path = "docs/voice-module/reconciliation.md"
	recon := readDoc(t, path)
	for _, want := range []string{
		"the **one exception** is",
		"notarize-and-staple.sh",
		"**#626 added after the baseline**",
		"**Phase 3 is the app and lives under `mobile/src/`, not `shared/`**",
		"no Phase 3 task writes anything under `shared/`",
		"**P3.1** (the skeleton and the account store, `mobile/src/skeleton` and `mobile/src/accounts`)",
		// Which Phase 3 parts are merged, and which are not, with the commits
		// that merged the two that landed after the baseline.
		"**P3.2** (the feed reader, PR #590, merged as `ebe6914`)",
		"**P3.6** (the mailbox client, PR #604, merged as `3d02625`)",
		"**P3.3** (`mobile/src/jobs/`)",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	for _, banned := range []string{
		// The sentence a review caught: it merged Phase 3 into shared/.
		"Phases 0, 1 and parts of 3 are **merged on `main`** as the pure-logic packages under `shared/`",
		// And the unqualified claim about the baseline.
		"every path exists there and every symbol named was read there",
	} {
		if strings.Contains(recon, banned) {
			t.Errorf("%s carries %q again; that is the claim a review corrected", path, banned)
		}
	}
	// #17 must say the script is post-baseline, not just who added it.
	finding := voiceModuleFinding(t, recon, 17)
	if !strings.Contains(finding, "after the baseline commit") {
		t.Errorf("%s: finding #17 names the notarize script without saying it postdates the baseline", path)
	}
	// The README's baseline row carries the same exception, because that table
	// is what a reader checks the document's freshness against.
	readme := readDoc(t, "docs/voice-module/README.md")
	if !strings.Contains(readme, "was added by #626 after this commit") {
		t.Errorf("docs/voice-module/README.md's baseline row does not carry the one post-baseline path")
	}
	// And the correction is checked against the tree it describes: Phase 3's
	// merged packages are under mobile/src/, and shared/ carries none of them.
	for _, dir := range []string{"mobile/src/skeleton", "mobile/src/accounts"} {
		if _, err := os.Stat("../../" + dir); err != nil {
			t.Errorf("%s: %s does not exist, so Phase 3's merged part is not where the document says: %v", path, dir, err)
		}
	}
	for _, dir := range []string{"shared/feed", "shared/mailbox-client"} {
		if _, err := os.Stat("../../" + dir); err == nil {
			t.Errorf("%s: %s exists, so Phase 3 is under shared/ after all and the correction is wrong", path, dir)
		}
	}
	// And the three Phase 3 tasks the document says are not merged have no
	// directory: the claim is about this tree, so it is checked against it.
	for _, dir := range []string{"mobile/src/jobs", "mobile/src/audio", "mobile/src/voice"} {
		if _, err := os.Stat("../../" + dir); err == nil {
			t.Errorf("%s: %s exists, so that Phase 3 task is merged and the document's list of what is not is stale", path, dir)
		}
	}
}

// voiceModuleFinding returns finding #n's block: its heading up to the next
// finding or section heading. Findings are addressed by number, so a retitled
// finding does not move the pin.
func voiceModuleFinding(t *testing.T, recon string, n int) string {
	t.Helper()
	i := strings.Index(recon, "**#"+strconv.Itoa(n)+" — ")
	if i < 0 {
		t.Fatalf("docs/voice-module/reconciliation.md has no finding #%d", n)
	}
	block := recon[i:]
	for _, stop := range []string{"\n**#", "\n## "} {
		if j := strings.Index(block, stop); j >= 0 {
			block = block[:j]
		}
	}
	return block
}

// markdownTableCells splits one GFM table line into its cells. The split is on
// unescaped pipes only, and an escaped pipe comes back as the pipe it stands
// for: "| a \| b | c |" is two cells, the first of them "a | b". An unescaped
// pipe inside a cell is not a pipe in the text -- GFM ends the cell there -- so
// the splitter is what tells the two apart, and it is the reason a quoted
// sentence containing one has to be written with the backslash.
func markdownTableCells(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '|' {
			b.WriteByte('|')
			i++
			continue
		}
		if s[i] == '|' {
			cells = append(cells, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(s[i])
	}
	return append(cells, b.String())
}

// tableSeparatorCellRE matches one cell of a table's `|---|` line.
var tableSeparatorCellRE = regexp.MustCompile(`^:?-{3,}:?$`)

// isTableSeparator reports whether a parsed row is the line that makes the line
// above it a table header rather than an ordinary paragraph starting with a
// pipe.
func isTableSeparator(cells []string) bool {
	for _, c := range cells {
		if !tableSeparatorCellRE.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// TestDocsVoiceModuleFolderTablesAreWellFormed: every table in the folder
// parses. A cell that carries an unescaped pipe ends there, so the rest of the
// row is read as further cells -- finding #42's verdict cell was lost that way,
// its quoted "the network is down | local only" splitting a three-column row
// into four -- and the wrong cell count is the only symptom. The header's cell
// count is the contract: every row under it must match, and the line under the
// header must be the separator, or GFM renders the block as text and the whole
// table disappears rather than one cell.
func TestDocsVoiceModuleFolderTablesAreWellFormed(t *testing.T) {
	for _, path := range []string{
		"docs/voice-module/README.md",
		"docs/voice-module/reconciliation.md",
		"docs/voice-module/spec.md",
	} {
		lines := strings.Split(readDoc(t, path), "\n")
		for i := 0; i < len(lines); i++ {
			if !strings.HasPrefix(lines[i], "|") {
				continue
			}
			header := markdownTableCells(lines[i])
			// The block runs to the first line that is not a table row.
			end := i
			for end < len(lines) && strings.HasPrefix(lines[end], "|") {
				end++
			}
			if end-i < 2 {
				t.Errorf("%s:%d: a table of one line: a header needs the separator line under it", path, i+1)
			} else if !isTableSeparator(markdownTableCells(lines[i+1])) {
				t.Errorf("%s:%d: the line under the header is %q, not a |---| separator: GFM renders this block as text, so the table is not a table", path, i+2, strings.TrimSpace(lines[i+1]))
			}
			for j := i; j < end; j++ {
				if got := len(markdownTableCells(lines[j])); got != len(header) {
					t.Errorf("%s:%d: the row has %d cells and the header has %d: an unescaped `|` in a cell ends it there and the rest of the row is read as more cells, so write it `\\|` (row: %s)", path, j+1, got, len(header), strings.TrimSpace(lines[j]))
				}
			}
			i = end - 1
		}
	}
}

// boldCheckNumbers returns the check numbers a document states in bold, one
// entry per whole number. The bold runs are the tokens, so "**64, 65 and 66**"
// yields 64, 65 and 66, while a number written plainly -- a date, a "Phase 6",
// a range like "80–89" -- is not read, and no number is matched as a substring
// of another ("64" cannot be satisfied by "164").
func boldCheckNumbers(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`\*\*([^*]+)\*\*`).FindAllStringSubmatch(s, -1) {
		for _, n := range regexp.MustCompile(`\d+`).FindAllString(m[1], -1) {
			out[n] = true
		}
	}
	return out
}

// sortedCheckNumbers lists a set from boldCheckNumbers in a stable order, so a
// failure names the first drifted check rather than a map iteration order.
func sortedCheckNumbers(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// voiceModuleMutationHeader opens this file's trailing mutation log: the one
// line that states how many mutations the log records.
const voiceModuleMutationHeader = "// Mutation (verified, run and restored): "

// voiceModuleMutationEntryRE matches one entry of that log: a comment line
// opening with the entry's number. Every mutation is written that way -- its
// number first, then what was planted and which test caught it -- so the count
// the header states and the entries under it can be compared.
var voiceModuleMutationEntryRE = regexp.MustCompile(`^// \((\d+)\) `)

// TestDocsVoiceModuleMutationLogIsSelfConsistent reads this file's own trailing
// mutation log and checks it against itself. The log is a claim like any other
// in this repository -- it says how many mutations were planted, run, seen to
// fail and restored -- and that count went stale once: the header said
// twenty-four while the log listed thirty, because a commit added to the log
// and not to the sentence above it. The count and the entries are compared here
// rather than trusted, and the entries must run from (1) with no gap and no
// repeat.
func TestDocsVoiceModuleMutationLogIsSelfConsistent(t *testing.T) {
	const path = "internal/cli/docs_voice_module_test.go"
	lines := strings.Split(readDoc(t, path), "\n")

	stated := 0
	for i, line := range lines {
		if !strings.HasPrefix(line, voiceModuleMutationHeader) {
			continue
		}
		if stated != 0 {
			t.Errorf("%s:%d: a second mutation log header (%q): the file carries one log, and a second count states it twice", path, i+1, line)
		}
		if n, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(line[len(voiceModuleMutationHeader):])); err == nil {
			stated = n
		}
	}
	if stated == 0 {
		t.Fatalf("%s: no mutation log header (%q ...) stating a count: the log and its count are checked against each other, so neither can be read", path, voiceModuleMutationHeader)
	}

	seen := map[int]bool{}
	var entries []int
	for i, line := range lines {
		m := voiceModuleMutationEntryRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if seen[n] {
			t.Errorf("%s:%d: the mutation log numbers an entry (%d) twice: each mutation is planted, run and restored once, and a repeat hides the entry that lost its number", path, i+1, n)
		}
		seen[n] = true
		entries = append(entries, n)
	}

	if len(entries) != stated {
		t.Errorf("%s: the mutation log's header says %d and the log has %d entries (%v): the count is a claim about the entries under it, and it went stale once when a commit added to the log without editing the header", path, stated, len(entries), entries)
	}
	for n := 1; n <= stated; n++ {
		if !seen[n] {
			t.Errorf("%s: the mutation log has no entry (%d) though its header says %d: the entries run from (1) with no gap, so a missing number is a deleted or a misnumbered entry", path, n, stated)
		}
	}
	for n := range seen {
		if n > stated {
			t.Errorf("%s: the mutation log has an entry (%d) above the %d its header states: the header undercounts the log", path, n, stated)
		}
	}
}

// Mutation (verified, run and restored): 52 entries, numbered (1)–(52).
// (52) Three edits to §0's Phase 3 sentence each fail
// TestDocsVoiceModuleReconciliationPinsItsBaseline: replacing "no Phase 3 task
// writes anything under `shared/`" with "Phase 3 is partly under `shared/`";
// dropping the merge commits from the P3.2 and P3.6 clauses; and replacing the
// list of what is not merged with "nothing else in Phase 3 is merged". The
// merged Phase 3 work is under `mobile/src/`, and which of P3.1–P3.6 is merged
// is a fact about the tree, so all three halves are pinned.
// (38) Writing "a loopback, in-runner proxy that swaps a short-lived token for
// the real key" back into #29 fails
// TestDocsVoiceModuleReconciliationQuotesAreVerbatim twice: once as the banned
// string, once as the package doc's own sentence missing from the document.
// (39) Writing "≤4096 cu" back into #27 fails it the same way, and with `no
// longer quotes "1–4096 code units" from shared/mailbox-schema/README.md`.
// (40) Changing the spec's *Cleanup* line back to "report how much of the reply
// was actually heard" fails it: spec.md says "record".
// (41) Shortening voice.md D54's condition back to "only if U7 finds it can
// surface a prompt" fails it, as a ban and as a missing quotation.
// (42) Restoring finding #4's title to "Stays in the foreground, no background
// listening" fails it: spec.md's *Goal* says "App stays in the foreground while
// talking (no background listening)".
// (43) Restoring finding #20's title to "The orchestrator/LLM" fails it:
// spec.md's pipeline says "orchestrator/LLM", with no article and lower case.
// (44) Replacing #29's corrected citation ("the flag's own comment on
// `secretsOptions.projectWide`, in that file") with the old
// `docs/design/project-secrets.md` §4 fails it.
// (45) Shortening "the reader rule from `docs/design/project-secrets.md`'s "Who
// reads" paragraph" back to "the reader rule" fails it: the design's §4 is the
// storage decision, and the rule is in the paragraph named.
// (46) Pointing the gateway quotation at `internal/agent/env.go` instead of
// `internal/gateway/gateway.go` fails it with `quotes ... as
// internal/agent/env.go's words, and that file does not contain them` -- the
// source direction is live, not only the document-side ban.
// (47) Replacing §0's Quotations paragraph with "**Quotations.** All quotes
// checked." fails it with `lacks "**Quotations.** Every quotation ..."`.
// (48) Writing "Phases 0, 1 and parts of 3 are **merged on `main`** as the
// pure-logic packages under `shared/`" back into §0 fails
// TestDocsVoiceModuleReconciliationPinsItsBaseline: Phase 3 is the app, and it
// lives under `mobile/src/`.
// (49) Deleting the baseline paragraph's exception clause fails that test with
// `lacks "the **one exception** is ..."`, and restores "every path exists
// there", which it bans.
// (50) Changing #17's "after the baseline commit" to "before it" fails it: the
// console build's notarize script postdates 82ba600d.
// (51) Dropping the README's baseline-row exception ("was added by #626 after
// this commit") fails it: the table is what a reader checks freshness against.
// (34) Writing "kept Q1–Q3" back into §1's sentence about the design's first
// revision fails TestDocsVoiceModuleFolderIsWritten with `says "kept Q1–Q3"`:
// the design's own §11 says its six questions were consolidated into four, so
// the ban is what keeps the corrected sentence from drifting back.
// (35) Writing the *Goal*'s foreground rule back as "states it twice" in
// finding #4 fails it with `says "*Goal* states it twice"`: spec.md states it
// once, in one sentence, and this document's own "twice" elsewhere means "in
// two places".
// (36) Dropping the consolidation from §1 ("consolidated its first revision's
// questions") fails it with `lacks "consolidated its first revision's six
// questions into four"` -- the positive half of the same pin, so the claim has
// to be stated, not merely not-contradicted.
// (37) Writing "says twice" back into Q4's option (1) fails it with `says
// "*Goal* says twice"`: the option is the record of what was asked, and its
// one description of the spec has to be true for the same reason.
// (31) Changing this log's own header to "24 entries" while the log still lists
// thirty fails TestDocsVoiceModuleMutationLogIsSelfConsistent with `the mutation
// log's header says 24 and the log has 30 entries` -- the stale count that test
// exists for, reproduced on purpose.
// (32) Renumbering the log's last entry (30) to (31) fails it with `the
// mutation log has no entry (30) though its header says 30` and `the mutation
// log has an entry (31) above the 30 its header states`.
// (33) Renumbering (29) to (28) fails it with `the mutation log numbers an
// entry (28) twice` and `the mutation log has no entry (29)`.
// (25) Stripping the answered marker from Q7's heading ("### Q7 — ... D45 says
// nothing bounds it?") fails
// TestDocsVoiceModuleReconciliationRecordsEveryAnswer with `Q7's heading does
// not carry the answered marker`: the thirteen headings are what a reader scans,
// so a question whose marker went missing reads as one still waiting.
// (26) Changing Q4's answer paragraph to "option (2)" fails it with `Q4's
// answer is option (2); the question recommends option (1)` -- the answer and
// the option it records are compared, so an answer cannot silently take a
// different option from the one the question recommended.
// (27) Writing "§5's
// questions are the gate" back into §0 fails it with `still says a question is
// open: "§5's questions are the gate"`.
// (28) Renaming §5a's Q9 table row so it
// no longer starts with a question number fails it twice: `§5a's table has 11
// rows and §5 has 12 questions besides Q1: every answer must be in both places`
// and `§5a's table has no row for Q9`.
// (29) Adding an invented citation to Q5's
// answer ("the spec's openWakeWord/Porcupine bullet is **dropped** (#33)") fails
// it with `Q5's answer cites #33, which the option it records does not cite:
// the answer records the option, it does not extend it` -- #33 is a finding
// that exists, which is why only a per-answer comparison catches it.
// (30) Putting "Q2–Q13 stay open" back into the README's question line fails
// TestDocsVoiceModuleFolderIsWritten with `docs/voice-module/README.md still
// says a question is open: "Q2–Q13 stay open"`.
// (20) Writing the pipe in finding #42's quoted sentence unescaped again ("the
// network is down | local only") fails
// TestDocsVoiceModuleFolderTablesAreWellFormed with `the row has 4 cells and
// the header has 3`: that is the defect the escape fixes, and the row's verdict
// cell is what GFM drops.
// (21) Adding a §2 row with no audit entry ("| A
// brand-new spec claim nobody audited |") fails
// TestDocsVoiceModuleReconciliationFindingsResolve twice -- `§2 has 27 rows and
// the audit has 26 entries` and `the §2 row "A brand-new spec claim nobody
// audited" matches 0 audit entries` -- which is the direction the old
// audit-only-iterates-the-slice loop could not see.
// (22) Duplicating §2's
// debounce row fails it with `the §2 table has two rows whose first cell is
// "Debounce window batches bursts into one utterance"`.
// (23) Deleting the
// `{"Debounce window", []int{10}}` entry from voiceModuleSection2Audit fails it
// with `§2 has 26 rows and the audit has 25 entries` and `the §2 row "Debounce
// window batches bursts into one utterance" matches 0 audit entries`.
// (24) Rewording that row's first cell to "Burst batching into one utterance"
// fails it both ways at once: `matches 0 audit entries` for the row, and `no row of
// §2 matches the audit entry "Debounce window"` for the entry.
// (17) Writing the staging verdict back into finding #17 ("which is
// **STAGED, NOT APPLIED**") fails
// TestDocsVoiceModuleReconciliationRecordsTheAppliedConsoleWorkflow twice: the
// document-level refusal (`says "STAGED, NOT APPLIED" about
// .github/workflows/console-release.yml: ... so the file is applied`) and the
// finding's own `applied, not staged`.
// (18) Dropping the copy-in commit
// (`7fdfa61`, "Add console release workflow") from #17 fails it with `finding
// #17 does not state "7fdfa61"`.
// (19) Replacing the deep verification clause
// with a generic one ("`codesign --verify --deep --strict` -- `--deep` is what
// reaches the nested binary -- plus" -> "a signature check plus") fails it with
// `finding #17 does not state "codesign --verify --deep --strict"`: the
// nested-binary half of the consequence is what the pipeline actually checks.
// (7) Pointing §2's Haiku row at #33 (Apple's OS 26 floor) instead of #26 fails
// TestDocsVoiceModuleReconciliationFindingsResolve with `the §2 row " A small
// fast LLM (Haiku) turns a batch into one spoken line " cites [11 33]; the
// audit, read from §3's findings, expects [11 26]`.
// (8) Writing "#45" back into
// #18's consequence fails it with `cites #45, which §3 does not define: the
// findings run #1..#44`.
// (9) Pointing §1's Q3 row at #25 instead of #19 fails
// it on the §1 row's own line.
// (10) Replacing the register paragraph's
// (#26)/(#28) with (#36) fails it three times: both names missing, and the
// paragraph citing the dependency-licence gap.
// (11) Dropping
// "*(Recommended.)*" from Q5's first option bullet fails
// TestDocsVoiceModuleReconciliationAllocatesNoDecisions with `### Q5 — Which
// wake word ... does not recommend first`.
// (12) Renaming §5a's heading fails
// TestDocsVoiceModuleFolderIsWritten with `lacks "## 5a. Owner's answers"`.
// (13) Removing "103" from the README's check list fails the same test with
// `docs/voice-module/README.md does not list check 103`.
// (14) Reverting every
// `voice-evaluation-switch.md` link to `cloud-stt-tts.md` fails
// TestDocsVoiceModuleReconciliationCitationsExist with `does not disclose its
// branch-only citations`.
// (15) Adding "and **111** to the console line" to the
// README's sentence fails TestDocsVoiceModuleFolderIsWritten the other way
// round, with `docs/voice-module/README.md lists check 111, which finding #44
// does not: the index and the document have drifted apart` -- the direction the
// old substring loop could not see.
// (16) Editing a line of spec.md that no
// verbatim check covers ("Log latency to first partial" -> "Log the latency to
// first partial") fails it with `docs/voice-module/spec.md is not the text
// committed verbatim at eea279f1 (sha256 b82c8f84..., want 26a2e884...)`: the
// whole file is pinned, which is what the README's "byte-for-byte" means.
//
// And three older ones, all run and restored.
// (4) Putting the marker claim back into the README ("the text between the
// `=== SPEC TEXT ===` markers") fails TestDocsVoiceModuleFolderIsWritten twice,
// once for each banned string.
// (5) Wrapping spec.md in "=== SPEC TEXT ===" and
// "=== END OF SPEC TEXT ===" lines fails it once per delimiter line, and the
// SHA-256 pin too (the spec is no longer the text committed at eea279f1).
// (6) Writing "Two paths cited below are not on `main`" back into the
// reconciliation fails TestDocsVoiceModuleReconciliationCitationsExist.
//
// And three older ones, one per test.
// (1) Paraphrasing "App stays in the foreground while talking (no background
// listening)." in spec.md fails TestDocsVoiceModuleFolderIsWritten with
// `docs/voice-module/spec.md lacks "App stays in the foreground while talking
// (no background listening)." (the spec must be verbatim)` -- and with the
// SHA-256 pin, which reports the same edit as a whole-file mismatch rather than
// as one missing sentence.
// (2) Renumbering
// reconciliation.md's last owner question, "### Q13 —" to "### Q14 —", fails
// TestDocsVoiceModuleReconciliationAllocatesNoDecisions twice -- Q13 missing,
// Q14 present.
// (3) Renaming docs/design/voice.md on disk fails
// TestDocsVoiceModuleReconciliationCitationsExist with `cites
// "docs/design/voice.md", which does not exist at this commit`.
