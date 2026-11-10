package managed

import (
	"os"
	"strings"
	"testing"
)

// The audit record's control, read off the module that ships it
// (docs/no-prompt-policy/reconciliation.md, F15 and T2's ruling of 2026-11-07):
// under D1545 the record of a managed apply is what stands in for the person
// who would have read the plan, and an unversioned runs bucket made it erasable
// by whoever applied — the record's writer and its eraser were the same
// identity. The ruling's choice (a) is object versioning on the bucket plus a
// lifecycle rule that never purges the audit prefix, and both live in the
// installation module's bucket.tf rather than in this package. This test is the
// link between the two: the Go side's claim (audit.go's WriteAudit comment) is
// only true while the module really is versioned and really leaves fugaro/
// alone, and a module edited without it would leave the record erasable in place
// with nothing in this package changing.

// auditBucketTF is the installation module's runs-bucket definition, relative
// to this package's directory.
const auditBucketTF = "../../deploy/terraform/gcp/modules/installation/bucket.tf"

// TestAuditRecordBucketIsVersioned pins the two things the record depends on:
// the bucket is versioned, so a write here leaves the version before it
// readable, and no lifecycle rule that purges NONCURRENT versions reaches the
// fugaro/ prefix, so a deletion of an audit record cannot be made permanent by
// the bucket's own housekeeping. Reconciliation F15 names both; a failure here
// is F15's reconciliation coming undone in the module.
func TestAuditRecordBucketIsVersioned(t *testing.T) {
	src, err := os.ReadFile(auditBucketTF)
	if err != nil {
		t.Fatalf("reading %s: %v", auditBucketTF, err)
	}
	text := string(src)

	// The versioning block: an enabled one, not a block that merely exists.
	var versioned bool
	for _, b := range bucketBlocks(t, text, "versioning") {
		if strings.Contains(b, "enabled = true") {
			versioned = true
		}
	}
	if !versioned {
		t.Fatalf("%s carries no `versioning { enabled = true }` block, so a managed audit record is erasable in place and the record the D1545 ruling rests on (reconciliation F15) is not the append-only one audit.go's WriteAudit claims: the writer and the eraser would be the same identity", auditBucketTF)
	}

	// Every rule that purges noncurrent versions, and which prefixes it reaches.
	// A rule that names fugaro/ would let the bucket's own housekeeping erase the
	// audit record's noncurrent versions — which is exactly the hardening T2's
	// ruling of 2026-11-07 bought, given away.
	var noncurrent, reachingRuns int
	for _, b := range bucketBlocks(t, text, "lifecycle_rule") {
		if !strings.Contains(b, "days_since_noncurrent_time") {
			continue
		}
		noncurrent++
		if strings.Contains(b, `"fugaro/`) || strings.Contains(b, `"builds/`) {
			t.Errorf("a lifecycle rule purges noncurrent versions under a prefix the audit record or a build record lives in:\n%s\n%s", auditBucketTF, b)
		}
		if strings.Contains(b, `"runs/`) || strings.Contains(b, `"cache/`) {
			reachingRuns++
		}
	}
	// Non-vacuous: the versioned bucket has to purge the two prefixes it deletes
	// in volume, or this test would pass on a module with no noncurrent rule at
	// all — and the rule this file guards against is one that reached too far, so
	// the fixture has to be the rule it is about.
	if noncurrent == 0 || reachingRuns == 0 {
		t.Errorf("%s has %d noncurrent-version rule(s), %d naming runs/ or cache/: the versioned bucket needs one for the prefixes it deletes in volume, and this test asserts nothing about fugaro/ until it exists", auditBucketTF, noncurrent, reachingRuns)
	}
}

// bucketBlocks returns every block of the given kind in src: from "<kind> {" to
// the brace that closes it, counted over braces so a nested block does not end
// it early. It is a text scan and not an HCL parse — this test's whole subject
// is what the module's own text says — and a block that never closes fails the
// test rather than being read as the rest of the file. A `dynamic "lifecycle_rule"`
// wrapper is not matched, which is intended: the dynamic rules here carry no
// noncurrent condition.
func bucketBlocks(t *testing.T, src, kind string) []string {
	t.Helper()
	var out []string
	needle := kind + " {"
	for i := 0; ; {
		j := strings.Index(src[i:], needle)
		if j < 0 {
			return out
		}
		start := i + j
		depth, k := 0, start+len(needle)-1
		for ; k < len(src); k++ {
			switch src[k] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if depth != 0 {
			t.Fatalf("the %s block starting at byte %d of %s never closes", kind, start, auditBucketTF)
		}
		out = append(out, src[start:k+1])
		i = k + 1
	}
}
