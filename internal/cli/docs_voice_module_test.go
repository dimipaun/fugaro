package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsVoiceModuleFolderIsWritten: docs/voice-module/ is the owner's voice
// module spec of 2026-10-18 and its reconciliation against this repository.
// The README must carry the status, the owner, the date and the verdict on
// typographical corrections; spec.md must be the owner's text, so the
// sentences below are asserted verbatim (a paraphrase, an editor's tidy-up or
// a dropped bullet fails here); the reconciliation must carry the table, the
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

	recon := readDoc(t, "docs/voice-module/reconciliation.md")
	for _, want := range []string{
		// The commit and the date the reconciliation was checked against.
		"82ba600d984a420c595cf06417af6d781c18535a",
		"2026-10-19",
		// The sections the brief names, by their headings.
		"## 2. The table: what the spec asks for, and what the tree already has",
		"## 4. Resolutions",
		"## 5. Open for the owner",
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
}

// TestDocsVoiceModuleReconciliationAllocatesNoDecisions: the brief's rule is
// that a finding needing the owner becomes a Q-row with options, a
// recommendation first and its consequences -- and that no decision number is
// allocated by this document (this repository's D-numbers are the owner's to
// hand out). Q1 must be the gate (the second explicit egress decision D44
// reserved), Q13 the last, and Q14 must not exist.
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
		// Every question recommends first -- the brief's ordering.
		"*(Recommended.)*",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	if strings.Contains(recon, "### Q14 —") {
		t.Error("docs/voice-module/reconciliation.md grew a Q14; re-read this test and the document's numbering rule")
	}
}

// TestDocsVoiceModuleReconciliationCitationsExist: the reconciliation's
// load-bearing citations are paths a reader has to be able to open. Every
// on-main path below is stat'ed, not merely required as text, so a document
// that is renamed out from under §1 or §2 fails here.
//
// Three cited paths are deliberately NOT stat'ed: docs/mobile/design/cloud-stt-tts.md
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
		"../mobile/design/cloud-stt-tts.md",
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

// Mutation (verified, run and restored): six, all run and restored.
// (4) Putting the marker claim back into the README ("the text between the
// `=== SPEC TEXT ===` markers") fails TestDocsVoiceModuleFolderIsWritten twice,
// once for each banned string. (5) Wrapping spec.md in "=== SPEC TEXT ===" and
// "=== END OF SPEC TEXT ===" lines fails it once per delimiter line. (6)
// Writing "Two paths cited below are not on `main`" back into the
// reconciliation fails TestDocsVoiceModuleReconciliationCitationsExist.
//
// And three older ones, one per test.
// (1) Paraphrasing "App stays in the foreground while talking (no background
// listening)." in spec.md fails TestDocsVoiceModuleFolderIsWritten with
// `docs/voice-module/spec.md lacks "App stays in the foreground while talking
// (no background listening)." (the spec must be verbatim)`. (2) Renumbering
// reconciliation.md's last owner question, "### Q13 —" to "### Q14 —", fails
// TestDocsVoiceModuleReconciliationAllocatesNoDecisions twice -- Q13 missing,
// Q14 present. (3) Renaming docs/design/voice.md on disk fails
// TestDocsVoiceModuleReconciliationCitationsExist with `cites
// "docs/design/voice.md", which does not exist at this commit`.
