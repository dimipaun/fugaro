package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// latestVerifiedTest returns the last test record on sha with a clean tree.
func latestVerifiedTest(records []verify.Record, sha string) *verify.Record {
	var latest *verify.Record
	for i := range records {
		r := &records[i]
		if r.Kind == verify.KindTest && r.HeadSHA == sha && r.CleanTree {
			latest = r
		}
	}
	return latest
}

// ReasonNoVerifiedTest is Decide's reason when no clean-tree test record
// exists for the final commit.
const ReasonNoVerifiedTest = "no verified test run on the final commit"

// Decide applies the PR outcome rule (design §4.2): ready only if the latest
// test record on finalSHA with a clean tree passed and the last review shipped.
func Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary) (bool, string) {
	latest := latestVerifiedTest(records, finalSHA)
	switch {
	case latest == nil:
		return false, ReasonNoVerifiedTest
	case !latest.Passed:
		return false, "tests failing on the final commit"
	case last == nil:
		return false, "no review verdict"
	case last.Verdict != "ship":
		return false, fmt.Sprintf("review round %d still has %s", last.Round, Plural(last.Findings, "finding"))
	}
	return true, ""
}

// Plural formats "1 finding" / "3 findings".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
