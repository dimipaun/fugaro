package cli

import (
	"strings"
	"testing"
)

// TestDocsContributingCLA (D304): an outside pull request is merged only after
// its author has signed the Contributor License Agreement, which a bot asks for
// on the first one, and a contribution to the three directories under the
// business licence (the desktop console's, the mobile app's and `ee/`; D311
// and the contribution rule of 2026-10-26) also accepts that licence's terms.
// The sentences are pinned without the console's path, which no Go file
// outside docs_console_boundary_test.go's allow-list names. The section is what a contributor reads before opening the first pull
// request, so the terms are stated there rather than left to the app the owner
// installs (O3: the wording holds before that install).
//
// CONTRIBUTING.md is also a file other docs tests hold sentences in — two of
// them by their own line number (docs_compute_backend_test.go) and one by
// wording (docs_runner_timeout_test.go) — so this test pins those sentences
// here as well: the edit that adds a section is exactly the edit that can move
// a cited line without touching its words.
func TestDocsContributingCLA(t *testing.T) {
	const path = "CONTRIBUTING.md"
	doc := readDoc(t, path)

	section := flattenDoc(docSection(t, path, doc, "## Licensing of contributions"))
	for _, want := range []string{
		// The three facts the section carries, in its own words: who signs,
		// who asks, and what a contribution to the business-licensed
		// directories accepts on top of the agreement.
		"an outside pull request is merged only after its author signs the Contributor License Agreement",
		"which a bot asks for on the first one",
		"`mobile/` and `ee/` are under a business licence of their own",
		"who accepts that licence's terms",
		"grants the relicensing rights for those directories too",
		"waits until the bot is live",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `## Licensing of contributions` section never says %q: the contribution terms are stated there, and a reader who has to guess them is one the CLA bot has to refuse", path, want)
		}
	}
	// The section says why the signature is asked for. A reader who understands
	// it is relicensing, not paperwork, is the reader the CLA asks about.
	if !strings.Contains(section, "relicensable") {
		t.Errorf("%s's `## Licensing of contributions` section does not say what the signature is for (the copyright staying relicensable): a requirement stated without its reason reads as a formality", path)
	}

	// The sentences the other docs tests hold. The two below are cited by
	// number, so a section inserted above them breaks those tests with a
	// message about a citation rather than about a word, and a section inserted
	// between them breaks a citation the plan's reader relies on.
	lines := strings.Split(doc, "\n")
	for _, want := range []struct {
		line int
		text string
	}{
		{25, "is the one CI does not reach until the staged `docs/pending-workflows/ci.yml` is copied in"},
		{32, "`compute` is added by the staged `docs/pending-workflows/ci.yml`, so until that file is copied in the new root is validated locally only"},
	} {
		if want.line > len(lines) {
			t.Errorf("%s has %d lines, so :%d no longer exists: docs_compute_backend_test.go cites that line by number", path, len(lines), want.line)
			continue
		}
		if got := lines[want.line-1]; !strings.Contains(got, want.text) {
			t.Errorf("%s:%d no longer says %q (it reads %q): the new section was put where it shifted a line docs_compute_backend_test.go cites by number", path, want.line, want.text, strings.TrimSpace(got))
		}
	}
	// The runner bound is the same fact in two files: the local command, and
	// whichever of the two forms docs_runner_timeout_test.go holds.
	if !strings.Contains(doc, runnerLocalCommand) {
		t.Errorf("%s no longer carries %q, the local half of the runner bound docs_runner_timeout_test.go holds it to", path, runnerLocalCommand)
	}
	for _, claim := range runnerTimeoutClaims {
		if claim.path != path {
			continue
		}
		if !strings.Contains(doc, claim.pending) && !strings.Contains(doc, claim.applied) {
			t.Errorf("%s carries neither form of the runner-bound claim (%q / %q), which docs_runner_timeout_test.go holds it to", path, claim.pending, claim.applied)
		}
	}
}

// TestDocsPRTemplateCLA (D304): the pull-request template asks every author for
// the CLA signature beside the checks it already carries, so the requirement
// reaches a contributor who never opens CONTRIBUTING.md. The four checks stay
// where they are: the line is added to the list, not traded against one of
// them (the template's own history is a four-item list).
func TestDocsPRTemplateCLA(t *testing.T) {
	const path = ".github/pull_request_template.md"
	doc := readDoc(t, path)

	cla := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "- [ ] ") && strings.Contains(line, "Contributor License Agreement") {
			if cla != "" {
				t.Errorf("%s carries a second checklist line about the Contributor License Agreement: the template asks once, and a duplicated item is one a reviewer skips", path)
			}
			cla = line
		}
	}
	if cla == "" {
		t.Fatalf("%s has no `- [ ]` line naming the Contributor License Agreement: the checklist is where the template asks for what the pull request needs (D304)", path)
	}
	if !strings.Contains(cla, "signed") {
		t.Errorf("%s's CLA line does not ask for a signature (it reads %q): the CLA is signed, not acknowledged", path, cla)
	}
	for _, want := range []string{
		"Tests added or updated (hermetic: no real cloud, no network)",
		"`gofmt`, `go vet ./...` and `go test -race ./...` pass",
		"Docs updated (README, `docs/`, schemas)",
		"No secrets, tokens or personal data in the diff, fixtures or logs",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries the check %q: the CLA line adds to the list rather than replacing an item", path, want)
		}
	}
}
