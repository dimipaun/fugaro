package failure

import "github.com/dimipaun/fugaro/internal/verify"

// ClassForVerify is which class a verify record gives the commit it is about:
// the record that does not leave sha verified is unverified_final_commit, and
// one that ran the repository's tests and failed on it is tests_failed. A
// record that does leave sha verified — a passing test on that commit, with a
// clean tree — has no refusal to name, and answers with no class at all.
//
// It is here rather than beside its caller because it is the class's own
// definition of the state, and because a definition does not read the record's
// words: the verdict is verify.Record's own fields (its kind, its commit, its
// tree and its exit), never the reason a run wrote about it, which is what
// design §5's reader needs the field for in the first place. The classes are
// this package's, so a site cannot spell one wrong and still compile.
func ClassForVerify(rec verify.Record, sha string) Class {
	if rec.Kind != verify.KindTest || rec.HeadSHA != sha || !rec.CleanTree {
		return UnverifiedFinalCommit
	}
	if !rec.Passed {
		return TestsFailed
	}
	return ""
}
