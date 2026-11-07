package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// The two tests here are the ones DON4 of docs/credentials-service/default-on.md
// requires and that did not exist: the repository's **one** story about the
// credentials service's posture (the owner's ruling of 2026-11-05 — it is
// deployed by `fugaro update` on every installation, and what is per-repository
// is the switch a run reads) was stated in the documents and pinned nowhere, so
// a document could go on saying the service is off unless an owner opted in
// while every test still passed. That is what happened: SECURITY.md, its
// website mirror, the rules file, the operator runbook and the live checklist
// all carried the retired "off unless an installation opts in" posture, four
// docs tests pinned the retired `--provision-credentials` flag and passed, and
// nothing failed.

// postureScanRoots is where the posture is read from: the documents a person
// reads before deciding anything about the service — the security document and
// its generated mirror under website/, the repository's own guidance, the rules
// that load when a path is touched, the release template whose footer is the
// body published with every GitHub release, and every living document under
// docs/.
//
// It is deliberately the same walk as docs_live_init_census_test.go's, reusing
// its two boundary helpers: `censusOutput` steps over the build outputs and
// dependency trees whose text is rendered rather than written, and
// `censusExempt` steps over `datedRecords` — the dated roots D9 ruled a rename
// (or any later wording change) does not enter, because a record of what was
// decided or of what shipped is not rewritten to say what the tree says now.
// `docs/credentials-service/` is one of those roots, and its own posture
// documents (the plan this task is written in) are therefore out of this scan
// by the same ruling that keeps them out of every other.
var postureScanRoots = []string{
	"SECURITY.md",
	"CLAUDE.md",
	".goreleaser.yaml",
	".claude/rules",
	"docs",
	"website",
}

// postureRecordRoots is the second exemption class, and it is an extension of
// `datedRecords` rather than a new idea: each entry is another artifact's own
// record — the README, spec, implementation-plan and reconciliation of work
// planned on its own date, in the four-file shape `docs/setup-wizard/` already
// has and is already exempt for. A sentence in one of them ("the service ships
// dormant", a decision table's "stays opt-in as today", the opt-in a design's
// precondition waits on) states what was decided then; rewriting it would edit
// another artifact's plan, which is exactly what the dated rule forbids. The
// live documents in the same folders — `docs/gcp-live-checklist.md`,
// `docs/release.md`, the terms — are NOT exempt, and the scan covers them.
var postureRecordRoots = []string{
	"docs/bitbucket-credentials/",
	"docs/c39-review-identity/",
	"docs/cloud-senior-review/",
	"docs/console-coordinator-v2/",
	"docs/ee-reorg/",
	"docs/forge-authorship-images/",
	"docs/licensing/implementation-plan.md",
	"docs/licensing/reconciliation.md",
	"docs/mcp-server/",
	"docs/project-image-catalog/",
	"docs/project-workflow-secrets/",
	"docs/review-levels-ab-jobmax/",
	"docs/run-without-checkout/",
	"docs/secrets-brokering/",
	"docs/security-classification/",
	"docs/settings-commands/",
	"docs/stateless-orchestrator/",
	"docs/subscriptions/",
}

// postureOtherFeature names the documents whose "dormant" belongs to something
// that is not this service, and what it belongs to. It is a check rather than a
// blanket exemption: the test asserts that every hit in such a file is the
// other feature's, so a sentence about *this* service added beside one fails
// here instead of riding along under it.
var postureOtherFeature = map[string]string{
	"docs/accounts-add/spec.md": "the accounts' own `auto` mode, which ships dormant (D875) and is not this service",
}

// postureRetired are the spellings the ruling retires. Each is a phrase that
// was true until 2026-11-05 and is false now, so a document that carries one
// tells a reader the opposite of what the code does: the service is deployed by
// `fugaro update` (DON1) and a run reaches it where the per-repository switch is
// on (DON8).
var postureRetired = []string{
	"ships dormant",
	"optional credentials service",
	"fugaro update --provision-credentials",
}

// postureOptIn are the spellings the service's own opt-in was written with.
// They are retired **only when the sentence is about this service** -- other
// features here have real opt-ins (the coordinator's aggregation job,
// `--check-github-app`, the accounts' `auto` mode, `git.author`) -- so each is
// read together with the service's own names on the same paragraph rather than
// on its own. The list is the spellings the retired posture actually used and
// not every way "opt-in" could be written: a scan that flagged any "opt-in"
// near the word "service" would fail on the paragraphs that decide something
// else, and the documents it is meant to hold are the ones below.
var postureOptIn = []string{
	"opted-in installation",
	"an installation that has not opted in",
	"an installation that has opted in",
	"has opted in to the credentials service",
	"has opted into the credentials service",
	"opt in to the credentials service",
	"opt into the credentials service",
	"opt into a **credentials service**",
	"the opt-in alone",
	"the opt-in by itself",
	"drops the opt-in",
	"the opt-in's",
}

var postureServiceNames = []string{"credentials service", "credentials_service"}

// postureRetirementText is the third allowance, and it is the census's
// `retirementText` idiom: a sentence that says the opt-in is *gone* has to
// spell the retired thing or it cannot say what it retired — the rulebook's own
// header, a `doctor` Fix string and the plan of record all do — so a line that
// names it as retired is the one place the phrase is correct.
var postureRetirementText = []string{"no longer an opt-in", "retired", "is gone", "out of scope"}

// TestNoDocumentCallsTheCredentialsServiceOptional: no living document says the
// credentials service is off unless an installation opts in. The scan is over
// flattened text — `//` markers dropped and whitespace collapsed by
// docs_base_test.go's flattenDoc — so a phrase wrapped across a line break
// counts, and its exemptions are the two the census already rules on plus the
// other-feature check above.
//
// Mutation (verified, run and restored): put "the service ships dormant" back
// into SECURITY.md → FAIL, naming the file; put the retired
// `fugaro update --provision-credentials` back into the live checklist → FAIL,
// and into .goreleaser.yaml's footer → FAIL, which is the release body itself
// and the reason that root is in the list;
// and delete the accounts' exemption's guard by writing a sentence about *this*
// service into docs/accounts-add/spec.md → FAIL, which is what the guard is for.
func TestNoDocumentCallsTheCredentialsServiceOptional(t *testing.T) {
	root := testutil.ModuleRoot()
	for _, r := range postureScanRoots {
		p := filepath.Join(root, filepath.FromSlash(r))
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("posture scan root %s: %v — the scan is only as wide as its roots, so a missing one silently shortens it", r, err)
		}
		if !info.IsDir() {
			checkPostureFile(t, root, r, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if censusOutput(rel) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || postureExempt(rel) {
				return nil
			}
			checkPostureFile(t, root, rel, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", r, err)
		}
	}
}

// postureExempt is the two record classes, applied by path prefix.
func postureExempt(rel string) bool {
	if censusExempt(rel) {
		return true
	}
	for _, prefix := range postureRecordRoots {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// checkPostureFile flattens one file and reports every retired spelling in it,
// line by line, so the failure names the sentence rather than the file.
func checkPostureFile(t *testing.T, root, rel, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	if strings.ContainsRune(string(data), 0) {
		return
	}
	// Flattening collapses whitespace and newlines together (docs_base_test.go),
	// so the scan is scoped to a paragraph first: a mention wrapped across a
	// line break still counts, while a paragraph about another feature is not
	// read together with a sentence about this service three pages later.
	other, known := postureOtherFeature[rel]
	for _, para := range strings.Split(string(data), "\n\n") {
		line := flattenDoc(para)
		if strings.TrimSpace(line) == "" || postureLineAllowed(line) {
			continue
		}
		why := ""
		for _, phrase := range postureRetired {
			if strings.Contains(line, phrase) {
				why = phrase
			}
		}
		if why == "" && namesTheService(line) && namesAnOptIn(line) {
			why = "an opt-in of this service's"
		}
		if why == "" {
			continue
		}
		if known {
			if !strings.Contains(line, "`auto`") {
				t.Errorf("%s says %q, and %s exempts only %s: a sentence about this service may not ride along under that exemption", rel, why, rel, other)
			}
			continue
		}
		t.Errorf("%s says %q, and `fugaro update` deploys the credentials service on every installation now, so what is per-repository is the switch a run reads (docs/credentials-service/default-on.md, the owner's ruling of 2026-11-05). The sentence: …%s…", rel, why, postureSnippet(line, why))
	}
}

// postureLineAllowed is the third allowance: a line that names the retired
// thing in order to say it is retired.
func postureLineAllowed(line string) bool {
	for _, phrase := range postureRetirementText {
		if strings.Contains(line, phrase) {
			return true
		}
	}
	return false
}

func namesTheService(line string) bool {
	for _, name := range postureServiceNames {
		if strings.Contains(line, name) {
			return true
		}
	}
	return false
}

func namesAnOptIn(line string) bool {
	for _, phrase := range postureOptIn {
		if strings.Contains(line, phrase) {
			return true
		}
	}
	return false
}

// TestTheCredentialsServiceIsDeployedForEveryInstallation: the sentence the
// ruling turns into is stated where the posture is, in SECURITY.md, and in the
// generated website mirror — which is `SECURITY.md` byte for byte below its
// header (see docs_website_security_test.go, which fails on any drift between
// the two), so the mirror is read here rather than trusted.
//
// Mutation (verified, run and restored): delete the sentence from SECURITY.md
// → FAIL on both files, because the mirror is generated from it.
func TestTheCredentialsServiceIsDeployedForEveryInstallation(t *testing.T) {
	const heading = "### The credentials service"
	const posture = "**Deployed for every installation, and reached only where the switch is on (the owner's ruling of 2026-11-05; DON1 and DON8).**"
	const deploy = "**`fugaro update` deploys it on every installation**"
	const perRepo = "the service is idle until the installation's bundle holds a model credential **and** that repository's own root has been re-applied with the switch on"
	for _, path := range []string{"SECURITY.md", "website/content/security.md"} {
		sec := docSection(t, path, readDoc(t, path), heading)
		for _, want := range []string{posture, deploy, perRepo} {
			if !strings.Contains(sec, want) {
				t.Errorf("%s's %q section does not state %q: the service is deployed by `fugaro update` on every installation now, and a security document that does not say so leaves a reader believing the installation has to opt in", path, heading, want)
			}
		}
	}
}

// postureSnippet is the sentence the failure is about, so the message points at
// the text rather than at the file: the paragraph is flattened and can be a
// whole page, so the window is the phrase and the words around it.
func postureSnippet(para, phrase string) string {
	i := strings.Index(para, phrase)
	if i < 0 {
		return para
	}
	lo, hi := i-220, i+len(phrase)+220
	if lo < 0 {
		lo = 0
	}
	if hi > len(para) {
		hi = len(para)
	}
	return para[lo:hi]
}
