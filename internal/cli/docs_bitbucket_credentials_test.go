package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docs/bitbucket-credentials/ — the owner's spec for Bitbucket support through
// the credentials service, the reconciliation of that spec against the
// repository and against Bitbucket Cloud's own documentation, and the plan that
// turns the owner's answers into slices.
//
// There is no registry test over docs/ in this package: nothing pins the set of
// docs folders and nothing pins a folder's documents on its behalf, so a folder
// that wants its documents pinned writes its own file — this one, the same
// convention docs_rigor_modes_test.go states for docs/rigor-modes/ and
// docs_in_job_review_test.go for docs/in-job-review/.
//
// The folder is a plan whose owner gate is passed for the reconciliation only:
// the owner answered D360–D367 on 2026-10-26, four of them against the
// recommendation, and each task still needs the owner's own go. So what a guard
// can usefully hold is the folder's own shape and the folder's own claims about
// itself: the status lines, the spec staying the owner's text byte for byte, the
// decision block answered with the four not-recommended marks, the two number
// blocks with what is written in them and what is reserved, the parked slice,
// and every path the folder cites. Each of those is a sentence a later edit can
// quietly break, which is what makes them worth pinning rather than asserting in
// prose.
const (
	bitbucketCredentialsReadme = "docs/bitbucket-credentials/README.md"
	bitbucketCredentialsSpec   = "docs/bitbucket-credentials/spec.md"
	bitbucketCredentialsRecon  = "docs/bitbucket-credentials/reconciliation.md"
	bitbucketCredentialsPlan   = "docs/bitbucket-credentials/implementation-plan.md"

	// bitbucketCredentialsReadmeStatus is the index's status line, word for
	// word, and bitbucketCredentialsPlanStatus the plan's. They move together:
	// an index that says a task is approved while the plan says none is leaves
	// the two documents disagreeing about the one thing a reader starts from.
	bitbucketCredentialsReadmeStatus = "**Status: plan only; nothing is implemented; no task approved.**"
	bitbucketCredentialsPlanStatus   = "**Plan only. Nothing is implemented, and no task is approved.**"

	// bitbucketCredentialsS3Parked is the amendment of 2026-10-26, after #1104:
	// the run-token slice is deferred rather than cancelled, and its tasks keep
	// their numbers until the owner unparks it.
	bitbucketCredentialsS3Parked = "S3 is parked"

	// bitbucketCredentialsAnswered is the marker every decision in §5 carries
	// once the owner has answered it, and bitbucketCredentialsAgainst the mark
	// the four answers given against the recommendation carry. The second is
	// what keeps a later edit from reading as an approval of the plan's own
	// recommendation.
	bitbucketCredentialsAnswered = "**2026-10-26:"
	bitbucketCredentialsAgainst  = "Not the recommendation."

	// bitbucketCredentialsSpecSHA256 is spec.md's digest. The index promises the
	// file is the text this work received, "byte for byte", which no list of
	// sentences can keep: the checks below name its title and its header line,
	// and an edit to any other line of the file passes every one of them.
	bitbucketCredentialsSpecSHA256 = "90fe0b8a88f9692fdafa076efaab27016a6a13bd07777c7bd5b7a704ed433250"
)

// bitbucketCredentialsIndexedFiles are the documents the index must name. The
// index is the folder's entry point, so a document it does not name is one a
// reader cannot find.
var bitbucketCredentialsIndexedFiles = []string{
	"[spec.md](spec.md)",
	"[reconciliation.md](reconciliation.md)",
	"[implementation-plan.md](implementation-plan.md)",
}

// bitbucketDecisionRE matches one decision row of the reconciliation's §5 table:
// a row whose first cell is exactly one decision number. It is anchored on the
// closing `|` of that cell, so a citation of a decision in another row's prose
// is not a row of its own.
var bitbucketDecisionRE = regexp.MustCompile(`(?m)^\|\s*\*\*D(3[0-9][0-9])\*\*\s*\|`)

// bitbucketCredentialsAgainstRecommendation are the four decisions the owner
// answered against the recommendation (the index names the same four). The
// block is written to the answers rather than to the recommendations, so a
// document that lost one of these marks describes a decision the owner did not
// make.
var bitbucketCredentialsAgainstRecommendation = []string{"D360", "D362", "D363", "D367"}

// TestDocsBitbucketCredentialsIsWritten: the folder exists with its index and
// its three documents, the index carries the status line word for word and names
// the other three files, the plan carries its own status line and its slices,
// and both number blocks are stated with what is written in them and what is
// reserved. Its red state is this test: the folder's guard did not exist until
// this file did.
func TestDocsBitbucketCredentialsIsWritten(t *testing.T) {
	readme := flattenDoc(readDoc(t, bitbucketCredentialsReadme))
	recon := flattenDoc(readDoc(t, bitbucketCredentialsRecon))
	plan := flattenDoc(readDoc(t, bitbucketCredentialsPlan))

	for _, want := range append([]string{
		bitbucketCredentialsReadmeStatus,
		// The owner answered, and four of the eight answers went against the
		// recommendation: the index states both halves, because the plan is
		// written to the answers rather than to the recommendations.
		"went against the recommendation",
		// The amendment of 2026-10-26, after #1104: S3 is deferred, D360 is
		// narrowed back, and the active plan is S0–S2.
		"**Amended 2026-10-26, after #1104:**",
		bitbucketCredentialsS3Parked,
		"The active plan is **S0–S2**",
		// The two number blocks this work holds, with what is written in each
		// and what is reserved. A block is extended by appending to it and the
		// table that summarises it is elsewhere, which is how a range goes stale.
		"| **D360–D389** |",
		"| **Checks 145–154** |",
		"**D360–D367** are written and answered",
		"**D368–D389** are reserved and unused",
		"**145–149** are proposed by the plan",
		"**150–154** are reserved and unused",
	}, bitbucketCredentialsIndexedFiles...) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", bitbucketCredentialsReadme, want)
		}
	}

	for _, want := range []string{
		"# Bitbucket support through the credentials service: reconciliation",
		"**The owner answered decisions D360–D367 on 2026-10-26.**",
		"## 5. Decisions, with the owner's answers",
		"## 8. Numbers this work holds",
		// The block's own statement of itself, and the reading that the spec is
		// the owner's document while this one wins where the two disagree.
		"**D360–D389** and **Checks 145–154**",
		"**Checks 145–149** are proposed by the plan; all are owner-run and NOT RUN",
		"this document wins",
		"## 9. UNVERIFIED",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", bitbucketCredentialsRecon, want)
		}
	}

	for _, want := range []string{
		bitbucketCredentialsPlanStatus,
		"**The active plan is S0–S2.**",
		"S3 is kept below as a parked slice",
		"### S0: pins and the check register",
		"**T0.1 Pin this folder.**",
		"**T0.2 Register Checks 145, 146 and 149.**",
		// The check block, stated where the checks are allocated rather than
		// only in the index: a check number claimed in one place and not the
		// other is the pair a later reader cannot reconcile.
		"## Checks this plan takes (Checks 145–154)",
		"Checks 150–154 are reserved and unused.",
		// The plan's own statement that no agent takes a live cloud action, and
		// that the checks are the owner's.
		"**No agent takes a live cloud action.** Checks 145–149 are the owner's.",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", bitbucketCredentialsPlan, want)
		}
	}
}

// TestDocsBitbucketCredentialsSpecIsVerbatim: spec.md is the owner's document
// and nothing else. It carries the spec's own title and its header line, and it
// carries none of the reconciliation's apparatus: a reconciliation paragraph
// pasted into the spec, or a decision heading written into it, would leave the
// next reader unable to tell which of the two documents is the owner's. Those
// are sentence checks, and sentences are not bytes: the index's promise is that
// the file is the text this work received "byte for byte", which no list of
// sentences can keep — so the file is pinned by its SHA-256 as well, in the
// shape docs/coordinator and docs/voice-module pin their own specs.
func TestDocsBitbucketCredentialsSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, bitbucketCredentialsSpec)
	for _, want := range []string{
		"# Spec: Bitbucket support through the credentials service",
		"Owner: the project owner, 2026-10-26 (near verbatim, as relayed).",
		// The spec's own question, which §5 of the reconciliation answers: a
		// spec that lost it would leave D363's answer pointing at nothing.
		"more specific ones (per repository, per run) through that key",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", bitbucketCredentialsSpec, want)
		}
	}
	// The reconciliation's own vocabulary, which must never appear in the
	// owner's text: a decision heading, an answer marker, a classification, a
	// section of the findings or the UNVERIFIED table.
	for _, notWant := range []string{
		"### D36", "**Answered", "UNVERIFIED", "## 4. Findings", bitbucketCredentialsAgainst,
	} {
		if strings.Contains(spec, notWant) {
			t.Errorf("%s carries %q, which belongs to the reconciliation: the spec is the owner's text verbatim", bitbucketCredentialsSpec, notWant)
		}
	}
	sum := sha256.Sum256([]byte(spec))
	if got := hex.EncodeToString(sum[:]); got != bitbucketCredentialsSpecSHA256 {
		t.Errorf("%s is not the text this work received (sha256 %s, want %s): the index promises it is copied byte for byte, so any edit here -- one byte included -- is a difference from the owner's copy and needs the owner", bitbucketCredentialsSpec, got, bitbucketCredentialsSpecSHA256)
	}
}

// TestDocsBitbucketCredentialsDecisionsAreAnswered: the eight decisions of §5
// are D360–D367 with no gap, and every one carries the owner's answer of
// 2026-10-26. Four of them were answered against the recommendation, and each of
// those four says so — the index names the same four, because the plan is
// written to the answers and a mark that went missing would leave a task
// reading as the plan's own recommendation.
func TestDocsBitbucketCredentialsDecisionsAreAnswered(t *testing.T) {
	recon := readDoc(t, bitbucketCredentialsRecon)

	idx := bitbucketDecisionRE.FindAllStringSubmatchIndex(recon, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no decision rows to answer", bitbucketCredentialsRecon)
	}
	var against []string
	for n, m := range idx {
		id := "D" + recon[m[2]:m[3]]
		if want := "D" + strconv.Itoa(360+n); id != want {
			t.Fatalf("%s states %s where the block's %dth decision is %s: the block is D360–D367 with no gap", bitbucketCredentialsRecon, id, n+1, want)
		}
		end := len(recon)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		body := recon[m[1]:end]
		if !strings.Contains(body, bitbucketCredentialsAnswered) {
			t.Errorf("%s: %s carries no %q marker: the owner answered every decision in the block on 2026-10-26", bitbucketCredentialsRecon, id, bitbucketCredentialsAnswered)
		}
		if strings.Contains(body, bitbucketCredentialsAgainst) {
			against = append(against, id)
		}
	}
	if got, want := strings.Join(against, ", "), strings.Join(bitbucketCredentialsAgainstRecommendation, ", "); got != want {
		t.Errorf("%s: the decisions answered against the recommendation are %s, want %s: the index names that set, and the plan's tasks are written to the answers rather than to the recommendations", bitbucketCredentialsRecon, got, want)
	}

	readme := flattenDoc(readDoc(t, bitbucketCredentialsReadme))
	if !strings.Contains(readme, "D360, D362, D363 and D367 went against the recommendation") {
		t.Errorf("%s does not name the four decisions answered against the recommendation: a reader who takes the block for an approval of the plan's own recommendation has read the opposite of what the owner ruled", bitbucketCredentialsReadme)
	}
}

// bitbucketPathRE matches a backticked repository path, in the top-level
// directories this folder cites. It is the planPaths spelling
// (docs_mobile_mailbox_plan_test.go) with the console and `images/` added: this
// folder's plan amends console P6, so it cites the console's files by path.
var bitbucketPathRE = regexp.MustCompile("`((?:internal|console|docs|deploy|website|scripts|images|shared|mobile)/[A-Za-z0-9_./-]*)`")

// bitbucketLinkRE matches a markdown link target. The folder cites its own
// documents that way (`[spec.md](spec.md)`), and a path is cited however it is
// written.
var bitbucketLinkRE = regexp.MustCompile(`\]\(([^)]+)\)`)

// bitbucketIsPath keeps package-qualified symbols out of the list the way
// isPlanPath does, and admits the three extensions that helper does not carry
// and this folder cites: `.sh` (the live-check scripts the plan will add), and
// `.tf` and `.hcl` (the Terraform modules and roots, with the `.tftest.hcl`
// fixtures beside them). A citation this predicate dropped would be a cited path
// nothing checked, which is the one failure the derivation exists to prevent.
func bitbucketIsPath(p string) bool {
	switch path.Ext(p) {
	case ".sh", ".tf", ".hcl":
		return true
	}
	return isPlanPath(p)
}

// bitbucketCitedPaths is every repository path one of the folder's documents
// names — in backticks and as a link — sorted and deduplicated. It is read out
// of the document at test time, so a path a later edit adds is checked the
// moment it is written down. A trailing slash (a directory written with one) is
// stripped, so the two spellings of one directory are one entry,
// and a link is resolved against docs/bitbucket-credentials/, which is where the
// documents live.
func bitbucketCitedPaths(doc string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSuffix(p, "/")
		if !bitbucketIsPath(p) || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, m := range bitbucketPathRE.FindAllStringSubmatch(doc, -1) {
		add(m[1])
	}
	for _, m := range bitbucketLinkRE.FindAllStringSubmatch(doc, -1) {
		add(path.Join("docs/bitbucket-credentials", m[1]))
	}
	sort.Strings(out)
	return out
}

// bitbucketWillCreate is the paths the folder names that its own tasks create:
// they are deliberately not in the tree yet, and each one must still be absent
// and still be cited. Naming them here rather than leaving them out of the
// derivation is the point — a path the plan cites is either in the tree, or a
// path this plan's tasks create, or this list, and this list is checked the same
// way, so it cannot rot into a hiding place for a path that has landed.
//
// The list shrinks as the tasks land, and the test says so when it does not:
// T0.1's own file left it in the commit that wrote this guard and T0.2's in the
// commit that registered the checks, and T1.1's two files left it after T1.1
// landed, so all of them are checked like every other cited path now.
var bitbucketWillCreate = map[string]string{
	// S2, retargeted 2026-11-01: `coordinator show` reads Bitbucket through the
	// service's read consumer.
	"internal/gitprov/bitbucket/oauth.go":              "BB1",
	"internal/gitprov/bitbucket/oauth_test.go":         "BB1",
	"internal/credsvc/bitbucket.go":                    "BB4",
	"internal/credsvc/bitbucket_test.go":               "BB4",
	"internal/gitprov/bitbucket/service.go":            "BB4",
	"internal/gitprov/bitbucket/service_test.go":       "BB4",
	"internal/cli/coordinator_sweep_bitbucket_test.go": "BB5",
	"scripts/live-checks/check146.sh":                  "BB5",
	"scripts/live-checks/check146_test.sh":             "BB5",
	"internal/cli/docs_bitbucket_runbook_test.go":      "T2.6",
	"scripts/live-checks/check145.sh":                  "BB3",
	"scripts/live-checks/check145_test.sh":             "BB3",
	"internal/cli/docs_bitbucket_security_test.go":     "T2.11",
	// Parked with S3, and still absent for as long as S3 is.
	"internal/gitprov/bitbucket/comments_test.go": "T3.3 (parked)",
	"scripts/live-checks/check147.sh":             "T3.5 (parked)",
	"scripts/live-checks/check147_test.sh":        "T3.5 (parked)",
}

// bitbucketMoved are the paths this dated plan cites that the tree has since
// moved, each with where it went. The plan is a record of the task as it was
// written and is not edited when code it names moves, so the move is recorded
// here instead — narrowly, one entry per citation, and held in three directions:
// the old path must NOT resolve any more (a copy left behind would make this a
// hiding place), the new path must, and the folder must still cite the old one,
// or the entry is excusing a citation that is gone.
//
// The coordinator's library and command left `internal/` for `ee/` in E2.1b
// (ee-reorg, D434), the move that put these here. `internal/coordinator/project_test.go`
// was the eighth: the 2026-11-01 retarget rewrote the one S2 task that named it, so
// the folder cites the live path now and the entry left with the citation.
var bitbucketMoved = map[string]string{
	"internal/cli/coordinator.go":           "ee/coordinator/cli/coordinator.go",
	"internal/cli/coordinator_view.go":      "ee/coordinator/cli/coordinator_view.go",
	"internal/cli/coordinator_view_test.go": "ee/coordinator/cli/coordinator_view_test.go",
	"internal/coordinator/project.go":       "ee/coordinator/project.go",
	"internal/coordinator/state.go":         "ee/coordinator/state.go",
	"internal/coordinator/state_test.go":    "ee/coordinator/state_test.go",
	"internal/coordinator/sweep.go":         "ee/coordinator/sweep.go",
}

// TestDocsBitbucketCredentialsEveryCitedPathExists: every backticked repository
// path and every link target the folder names resolves in the tree — or is one
// of the paths this plan's tasks create, each of which must still be absent and
// still be named. The check is derived from the documents rather than kept by
// hand, so a path a later edit adds is checked the moment it is written down,
// and the exceptions are held in both directions: an entry that lands must leave
// the list, and an entry the folder no longer cites must leave it too.
func TestDocsBitbucketCredentialsEveryCitedPathExists(t *testing.T) {
	paths := map[string]bool{}
	for _, doc := range []string{
		bitbucketCredentialsReadme, bitbucketCredentialsRecon, bitbucketCredentialsPlan,
	} {
		for _, p := range bitbucketCitedPaths(readDoc(t, doc)) {
			paths[p] = true
		}
	}
	// A regex that stopped matching would make every assertion below vacuous.
	if len(paths) < 40 {
		t.Fatalf("bitbucketCitedPaths found %d paths in docs/bitbucket-credentials/: the derivation, not the folder, is what broke", len(paths))
	}
	for p := range paths {
		if why, ok := bitbucketWillCreate[p]; ok {
			if _, err := os.Stat("../../" + p); err == nil {
				t.Errorf("%s cites %s, which exists now (%s), so it is no longer a path this plan will create: it must leave bitbucketWillCreate and be checked like any other", bitbucketCredentialsPlan, p, why)
			}
			continue
		}
		if to, ok := bitbucketMoved[p]; ok {
			if _, err := os.Stat("../../" + p); err == nil {
				t.Errorf("%s cites %s, which resolves again, so it is no longer a path the tree moved: it must leave bitbucketMoved and be checked like any other", bitbucketCredentialsPlan, p)
			}
			if !pathExists(to) && !eeCitationWithoutEE(t, to) {
				t.Errorf("bitbucketMoved sends %s to %s, which is not in the tree: the record of a move has to name where the file went", p, to)
			}
			continue
		}
		if !pathExists(p) {
			t.Errorf("docs/bitbucket-credentials/ cites %s, which is not in the tree: a path the plan names and cannot be found is a later implementer's dead end", p)
		}
	}
	for p, to := range bitbucketMoved {
		if !paths[p] {
			t.Errorf("bitbucketMoved names %s (%s), which the folder no longer cites: the list must only excuse paths the documents actually name", p, to)
		}
	}
	for p, why := range bitbucketWillCreate {
		if !paths[p] {
			t.Errorf("bitbucketWillCreate names %s (%s), which the folder no longer cites: the list must only excuse paths the documents actually name", p, why)
		}
	}
}
