package cli

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestDocsCommitAuthorIsAClaim (A4, D604, D643): a run's commit author is the
// launcher's claim and not a verified identity, and the two documents have to
// say so where a reader meets them — docs/git-providers.md in the section an
// operator reads to opt in, and SECURITY.md's "Known limits", which is the file
// that states what Fugaro does not prove. The claims are read out of their own
// sections (docSection) and not out of the whole document, so a sentence some
// other section carries cannot satisfy them.
//
// The claims pinned here are the ones the plan's A4 names: the default author
// and the `git.author: launcher` opt-in with its two local-config keys and the
// address rule (D649), that the author is a claim and not a verified identity
// (D604), that a review or artifact run never stamps, and that a launcher may
// approve a pull request their own name is on (D643).
//
// Mutation-proofed by the red step this file was written for: before the two
// sections exist, docSection fails on the missing heading, and the test fails
// for exactly that reason.
func TestDocsCommitAuthorIsAClaim(t *testing.T) {
	const path = "docs/git-providers.md"
	doc := readDoc(t, path)
	section := normSpace(docSection(t, path, doc, "## Commit author and trailers"))
	for _, want := range []string{
		// The default, which the opt-in replaces but never removes for a
		// repository that writes nothing.
		"the run's own identity",
		"fugaro@users.noreply.invalid",
		// The opt-in and the pair the launch records: the address's rule is
		// D649's, and a colleague's address is the one this refuses.
		"git.author: launcher",
		"the repository's own opt-in",
		"`commit_email`",
		"`commit_name`",
		"a forge noreply address",
		"refused at launch",
		// The committer is not the launcher's: only the author half moves.
		"the committer stays the run's own identity",
		// The claim, and what a reader may not take it for.
		"is a claim, not a verified identity",
		// Neither of the two runs that push nothing stamps.
		"A review run and an artifact run stamp no trailer",
		// The runner's own commits are the run's commits too.
		"runner's own commits",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's \"## Commit author and trailers\" does not say %q: it is the section an operator reads to opt in, and the author it documents is a claim the launcher makes", path, want)
		}
	}

	sec := readDoc(t, "SECURITY.md")
	limits := normSpace(docSection(t, "SECURITY.md", sec, "### Known limits"))
	for _, want := range []string{
		"the launcher's claim",
		"a record",
		"A launcher may approve a pull request their own name is on",
		// The limit the trailers' own claim leans on, named where it already
		// is: the agent shares the container and its user with the runner.
		`see "What one run's agent can reach" above`,
	} {
		if !strings.Contains(limits, want) {
			t.Errorf("SECURITY.md's \"### Known limits\" does not say %q: the authorship and the trailers are limits a security document states, not behaviour it leaves a reader to infer", want)
		}
	}
}

// trailerConstRE reads a trailer name out of internal/gitops/stamp.go's const
// block, and docTrailerRE every trailer-shaped token a document writes. The
// pair is what holds the documents to the code: the trailer names are read from
// stamp.go rather than copied, so a fourth trailer added there fails this test
// until docs/git-providers.md names it, and a document naming a trailer the
// hook does not write (`Fugaro-Signed-off-by`, say) fails on the same read.
var (
	trailerConstRE = regexp.MustCompile(`(?m)^\tTrailer\w+\s*=\s*"([^"]+)"$`)
	docTrailerRE   = regexp.MustCompile("`(Fugaro-[A-Za-z0-9-]+)`")
)

// TestDocsTrailerNames (A4, D647): the trailers are on every commit a run makes
// and are documented by their exact names, which are the names the hook writes.
//
// The names come from internal/gitops/stamp.go, so the documents and the code
// move together in both directions: a trailer the code gains is a failing test
// here until the documents name it, and a trailer a document invents is a
// failing test until the code writes it.
func TestDocsTrailerNames(t *testing.T) {
	src, err := os.ReadFile("../gitops/stamp.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range trailerConstRE.FindAllStringSubmatch(string(src), -1) {
		names = append(names, m[1])
	}
	if len(names) < 3 {
		t.Fatalf("read %d trailer names out of internal/gitops/stamp.go: the constants have moved, so this test would pass on any wording", len(names))
	}
	docs := map[string]string{
		"docs/git-providers.md": readDoc(t, "docs/git-providers.md"),
		"SECURITY.md":           readDoc(t, "SECURITY.md"),
	}
	section := normSpace(docSection(t, "docs/git-providers.md", docs["docs/git-providers.md"], "## Commit author and trailers"))
	for _, name := range names {
		if !strings.Contains(section, "`"+name+"`") {
			t.Errorf("docs/git-providers.md's \"## Commit author and trailers\" does not name the %s trailer, which internal/gitops/stamp.go writes on every commit a run makes", name)
		}
	}
	for path, doc := range docs {
		for _, m := range docTrailerRE.FindAllStringSubmatch(doc, -1) {
			if !slices.Contains(names, m[1]) {
				t.Errorf("%s names %s, which internal/gitops/stamp.go does not write: the documents may name the trailers the hook writes and no others", path, m[1])
			}
		}
	}
	// What each trailer holds, and the two facts a reader deciding whether to
	// rely on them needs: the model is the stage's pin and not the model that
	// served the commit, and the trailers are a record and not proof.
	for _, want := range []string{
		"the run's ID",
		"the rigor mode",
		"the pin and not the model that served it",
		"a record and not proof",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/git-providers.md's \"## Commit author and trailers\" does not say %q", want)
		}
	}
}

// TestDocsSupportedForges (A4, D612): the reader of docs/git-providers.md is
// told which forges Fugaro supports and which it merely accepts. "Cloud forges
// only" and the owner-allow-listed Bitbucket API root the owner ruled in
// (D474) are reconciled by exactly that pair of words, and a document that
// leaves the reader to infer either half is the one that gets it wrong: the
// allow-listed root is not a second supported forge, and the pin that keeps a
// cloud run's token on Bitbucket's own API or a root the policy allows is what
// makes the exception safe to state.
func TestDocsSupportedForges(t *testing.T) {
	const path = "docs/git-providers.md"
	doc := readDoc(t, path)
	section := normSpace(docSection(t, path, doc, "## Supported forges"))
	for _, want := range []string{
		"Bitbucket Cloud",
		"GitHub",
		"cloud",
		"owner-allow-listed Bitbucket API root",
		"accepted",
		"not supported",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's \"## Supported forges\" does not say %q: the two cloud forges are supported and an allow-listed Bitbucket root is accepted and unsupported, and a reader has to be able to tell those apart", path, want)
		}
	}
	// The pin behind the acceptance, read rather than described: the code that
	// refuses a root which is neither Bitbucket's own nor one the published
	// policy allows.
	runner := readDoc(t, "internal/runner/runner.go")
	if !strings.Contains(runner, "func (r *run) checkBitbucketAPIRoot(") {
		t.Errorf("docs/git-providers.md tells a reader an allow-listed root is accepted, and internal/runner/runner.go no longer has checkBitbucketAPIRoot: nothing then refuses a root the policy does not allow")
	}
}
