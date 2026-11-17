package projectpolicy

import (
	"strings"
	"testing"
)

// reportsCommitNode is a policy node carrying a reports_commit block, written
// at the version this Fugaro reads. The version is interpolated from the
// constant and never typed: a fixture that pinned a number would fail for the
// bump rather than for the block, and these tests are about the block.
func reportsCommitNode(block string) []byte {
	return []byte(`{"version": ` + itoa(Version) + `, "reports_commit": ` + block + `}`)
}

// TestAReportsCommitNodeWithoutTheMemberDesignatesNothing: the member is
// optional and its absence is a statement — no repository is designated, so no
// report may be committed anywhere — which is what a project that never named
// one means and what every node written before this member existed answers byte
// for byte. The two accessors are the whole of what a reader has to tell the
// two states apart, so a node without the member, a policy that is nil and a
// node of the release behind (version 9, which no release wrote the member
// into) all answer the same thing: no repository, and the layout's own folder.
//
// The folder is answered even where no repository is, because it is the folder
// a designation would use: a reader that took the zero value would commit the
// reports at the repository root the day a project designated one.
func TestAReportsCommitNodeWithoutTheMemberDesignatesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"a node at this version with no member", []byte(`{"version": ` + itoa(Version) + `, "allow_repos": ["github/acme/app"]}`)},
		{"a node of the release behind", []byte(`{"version": 9, "allow_repos": ["github/acme/app"]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(tc.raw)
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if p.ReportsCommit != nil {
				t.Errorf("ReportsCommit = %+v, want nil: the node carries no member", p.ReportsCommit)
			}
			if got := p.ReportsCommitRepo(); got != "" {
				t.Errorf("ReportsCommitRepo() = %q, want \"\": a node with no member designates no repository", got)
			}
			if got := p.ReportsCommitDir(); got != DefaultReportsCommitDir {
				t.Errorf("ReportsCommitDir() = %q, want %q: an absent dir is the layout's own folder", got, DefaultReportsCommitDir)
			}
		})
	}
	// A caller holding no policy at all — the runner before a project has
	// published one — gets the same two answers rather than a panic.
	var none *Policy
	if got := none.ReportsCommitRepo(); got != "" {
		t.Errorf("a nil policy answers ReportsCommitRepo() = %q, want \"\"", got)
	}
	if got := none.ReportsCommitDir(); got != DefaultReportsCommitDir {
		t.Errorf("a nil policy answers ReportsCommitDir() = %q, want %q", got, DefaultReportsCommitDir)
	}
	// And the designation itself, read back: the canonical spelling a run
	// compares against its own repository, and the folder the block names.
	p, found, err := Decode(reportsCommitNode(`{"repo": "ACME/Reports.git", "dir": "docs/reports"}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if got := p.ReportsCommitRepo(); got != "acme/reports" {
		t.Errorf("ReportsCommitRepo() = %q, want acme/reports: the answer is task.CanonicalRepo's form, which is what task.Slug is built from", got)
	}
	if got := p.ReportsCommitDir(); got != "docs/reports" {
		t.Errorf("ReportsCommitDir() = %q, want docs/reports", got)
	}
}

// TestReportsCommitRefusesABlockWithNoRepo is the block's own rule (D2751, the
// validRouting/validBudget shape): a block that designates nothing reads as a
// designation, so it is refused where it is read rather than left for a commit
// run to find no repository. The refusals are the ones a hand-written or
// misspelt node falls into — an empty block, a block naming only the folder, a
// repo written as null, empty or whitespace, a repo this tree cannot read as a
// repository at all, a member the block does not model and a known member
// spelled in another case.
func TestReportsCommitRefusesABlockWithNoRepo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  string
	}{
		{"an empty block", `{}`, "reports_commit.repo is required"},
		{"a block naming only the folder", `{"dir": "docs/reports"}`, "reports_commit.repo is required"},
		{"a repo written as null", `{"repo": null}`, "reports_commit.repo is null"},
		{"an empty repo", `{"repo": ""}`, "reports_commit.repo is required"},
		{"whitespace alone", `{"repo": "   "}`, "reports_commit.repo is required"},
		{"a name that is not owner/name", `{"repo": "reports"}`, "must look like owner/name"},
		{"a path with a .. segment", `{"repo": "acme/../etc"}`, "must look like owner/name"},
		{"a repo with a trailing slash", `{"repo": "acme/"}`, "must look like owner/name"},
		{"a member the block does not model", `{"repo": "acme/reports", "folder": "docs"}`, "folder"},
		{"a known member in another case", `{"repo": "acme/reports", "DIR": "docs"}`, "DIR"},
		{"a member written twice", `{"repo": "acme/reports", "repo": "acme/other"}`, "repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(reportsCommitNode(tc.block))
			if err == nil {
				t.Fatalf("Decode accepted %s as a designation: p = %+v, so a commit run would read a block that designates nothing", tc.block, p)
			}
			if p != nil {
				t.Errorf("Decode returned a policy beside the refusal: %+v", p)
			}
			if !found {
				t.Error("found must be true: the node exists and cannot be used (fail closed)")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v does not say %q, the reason the block is refused", err, tc.want)
			}
		})
	}
	// The refusal is the block and not the member's presence: a readable block
	// decodes, so a rule that refused every block would fail here.
	if _, _, err := Decode(reportsCommitNode(`{"repo": "acme/reports"}`)); err != nil {
		t.Errorf("Decode(a block with a repo and no dir) = %v, want the node read: dir is optional", err)
	}
}

// TestReportsCommitRefusesADirTheLayoutCannotName: the folder is a path
// relative to the designated repository's root, and a path that leaves it —
// an absolute one, one starting with "/", one with a ".." component — is
// refused at the decode rather than joined under the checkout, where it would
// put a generated report and the commit that carries it somewhere no report run
// may write. A "." segment and a trailing "/" are not refused: they name the
// same folder once the path is joined, so refusing them would be a spelling
// rule and not a safety one.
func TestReportsCommitRefusesADirTheLayoutCannotName(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{"an absolute path", "/etc/fugaro", "must not start with /"},
		{"a .. component", "../elsewhere", "must not contain a .."},
		{"a .. that is the whole path", "..", "must not contain a .."},
		{"a .. in the middle", "docs/../../etc", "must not contain a .."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(reportsCommitNode(`{"repo": "acme/reports", "dir": "` + tc.dir + `"}`))
			if err == nil {
				t.Fatalf("Decode accepted %q as the reports folder: p = %+v", tc.dir, p)
			}
			if p != nil {
				t.Errorf("Decode returned a policy beside the refusal: %+v", p)
			}
			if !found {
				t.Error("found must be true: the node exists and cannot be used (fail closed)")
			}
			if !strings.Contains(err.Error(), "reports_commit.dir") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v does not name the member and say %q", err, tc.want)
			}
		})
	}
	for _, dir := range []string{"docs/fugaro-reviews", "reports", "a/b/c", "./docs", "docs/"} {
		p, found, err := Decode(reportsCommitNode(`{"repo": "acme/reports", "dir": "` + dir + `"}`))
		if err != nil || !found || p == nil {
			t.Errorf("Decode(dir %q) = (%v, %v, %v), want the folder read", dir, p, found, err)
			continue
		}
		if got := p.ReportsCommitDir(); got != dir {
			t.Errorf("ReportsCommitDir() = %q, want %q: the node's own spelling is what it carries", got, dir)
		}
	}
}
