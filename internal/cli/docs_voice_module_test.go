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
		"## 5. Open for the owner",
		// The owner's answer of 2026-10-19, right after the questions it
		// answers, and the two things it has to state.
		"## 5a. Owner's answers",
		"### What it changes in this spec's sections",
		"### What the answer does not do",
		"**Q1 — answered 2026-10-19",
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

	// §1's and §2's tables, row by row.
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
	// cellIdx is negative-counted from the end, because a cell may itself
	// contain a pipe: the last cell before the closing one is the finding
	// column in §2, and §1's is the second.
	rows := func(section string, cellIdx int) map[string]string {
		out := map[string]string{}
		for _, line := range strings.Split(section, "\n") {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			cells := strings.Split(line, "|")
			if len(cells) < 4 || strings.HasPrefix(strings.TrimSpace(cells[1]), "---") {
				continue
			}
			i := cellIdx
			if i < 0 {
				i = len(cells) + i
			}
			out[cells[1]] = cells[i]
		}
		return out
	}
	audit := func(label, body string, cellIdx int, wants []voiceModuleRowAudit) {
		table := rows(body, cellIdx)
		for _, w := range wants {
			concept, cell := "", ""
			for c, v := range table {
				if strings.Contains(c, w.row) {
					concept, cell = c, v
					break
				}
			}
			if concept == "" {
				t.Errorf("%s: no row of %s matches %q; the table or the audit moved", path, label, w.row)
				continue
			}
			var got []int
			for _, m := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(cell, -1) {
				n, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("%s: unparsable citation %q in the %s row %q", path, m[1], label, concept)
				}
				got = append(got, n)
			}
			slices.Sort(got)
			want := slices.Clone(w.findings)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s: the %s row %q cites %v; the audit, read from §3's findings, expects %v", path, label, concept, got, want)
			}
			for _, n := range got {
				if _, ok := titles[n]; !ok {
					t.Errorf("%s: the %s row %q cites #%d, which §3 does not define", path, label, concept, n)
				}
			}
		}
	}
	audit("§1", section("## 1. ", "## 2. "), 2, voiceModuleSection1Audit)
	audit("§2", section("## 2. ", "## 3. "), -2, voiceModuleSection2Audit)

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

// Mutation (verified, run and restored): ten, all run and restored.
// (7) Pointing §2's Haiku row at #33 (Apple's OS 26 floor) instead of #26 fails
// TestDocsVoiceModuleReconciliationFindingsResolve with `the §2 row " A small
// fast LLM (Haiku) turns a batch into one spoken line " cites [11 33]; the
// audit, read from §3's findings, expects [11 26]`. (8) Writing "#45" back into
// #18's consequence fails it with `cites #45, which §3 does not define: the
// findings run #1..#44`. (9) Pointing §1's Q3 row at #25 instead of #19 fails
// it on the §1 row's own line. (10) Replacing the register paragraph's
// (#26)/(#28) with (#36) fails it three times: both names missing, and the
// paragraph citing the dependency-licence gap. (11) Dropping
// "*(Recommended.)*" from Q5's first option bullet fails
// TestDocsVoiceModuleReconciliationAllocatesNoDecisions with `### Q5 — Which
// wake word ... does not recommend first`. (12) Renaming §5a's heading fails
// TestDocsVoiceModuleFolderIsWritten with `lacks "## 5a. Owner's answers"`.
// (13) Removing "103" from the README's check list fails the same test with
// `docs/voice-module/README.md does not list check 103`. (14) Reverting every
// `voice-evaluation-switch.md` link to `cloud-stt-tts.md` fails
// TestDocsVoiceModuleReconciliationCitationsExist with `does not disclose its
// branch-only citations`. (15) Adding "and **111** to the console line" to the
// README's sentence fails TestDocsVoiceModuleFolderIsWritten the other way
// round, with `docs/voice-module/README.md lists check 111, which finding #44
// does not: the index and the document have drifted apart` -- the direction the
// old substring loop could not see. (16) Editing a line of spec.md that no
// verbatim check covers ("Log latency to first partial" -> "Log the latency to
// first partial") fails it with `docs/voice-module/spec.md is not the text
// committed verbatim at eea279f1 (sha256 b82c8f84..., want 26a2e884...)`: the
// whole file is pinned, which is what the README's "byte-for-byte" means.
//
// And three older ones, all run and restored.
// (4) Putting the marker claim back into the README ("the text between the
// `=== SPEC TEXT ===` markers") fails TestDocsVoiceModuleFolderIsWritten twice,
// once for each banned string. (5) Wrapping spec.md in "=== SPEC TEXT ===" and
// "=== END OF SPEC TEXT ===" lines fails it once per delimiter line, and the
// SHA-256 pin too (the spec is no longer the text committed at eea279f1). (6)
// Writing "Two paths cited below are not on `main`" back into the
// reconciliation fails TestDocsVoiceModuleReconciliationCitationsExist.
//
// And three older ones, one per test.
// (1) Paraphrasing "App stays in the foreground while talking (no background
// listening)." in spec.md fails TestDocsVoiceModuleFolderIsWritten with
// `docs/voice-module/spec.md lacks "App stays in the foreground while talking
// (no background listening)." (the spec must be verbatim)` -- and with the
// SHA-256 pin, which reports the same edit as a whole-file mismatch rather than
// as one missing sentence. (2) Renumbering
// reconciliation.md's last owner question, "### Q13 —" to "### Q14 —", fails
// TestDocsVoiceModuleReconciliationAllocatesNoDecisions twice -- Q13 missing,
// Q14 present. (3) Renaming docs/design/voice.md on disk fails
// TestDocsVoiceModuleReconciliationCitationsExist with `cites
// "docs/design/voice.md", which does not exist at this commit`.
