package infra

import (
	"regexp"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"

	"github.com/dimipaun/fugaro/internal/runmsg"
)

// msgRuleRE is the message rule as the installation module must write it: a
// plain lifecycle_rule — not a dynamic one, which exists only when the
// variable it is gated on is set — deleting at the message age, under tokens/
// and matching the store's own suffix. GCS matches a prefix and a suffix and
// never a middle segment, so the suffix is the whole of what keeps this rule
// off the budget token beside it under tokens/ (F11).
func msgRuleRE() *regexp.Regexp {
	return regexp.MustCompile(`lifecycle_rule \{\s*action \{\s*type = "Delete"\s*\}\s*condition \{\s*` +
		`age\s+= var\.bucket_lifecycle\.messages_days\s*` +
		`matches_prefix\s+= \["tokens/"\]\s*` +
		`matches_suffix\s+= \["` + regexp.QuoteMeta(runmsg.Suffix) + `"\]\s*\}\s*\}`)
}

// TestMessageLifecycleSuffixMatchesRunmsg pins the module's message rule to
// the store that writes the objects it reaps: the HCL carries a literal
// suffix, the store carries runmsg.Suffix, and an edit to either alone leaves
// the bucket reaping something nobody writes — or keeping plaintext messages
// for ever — with no test failing. The rule also has to be unconditional: the
// budget token's rule exists only with enable_budget, and a run accepts a
// message whether or not the budget is on (F11).
func TestMessageLifecycleSuffixMatchesRunmsg(t *testing.T) {
	b, err := terraform.FS.ReadFile("gcp/modules/installation/bucket.tf")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	loc := msgRuleRE().FindStringIndex(text)
	if loc == nil {
		t.Fatalf("the installation module has no unconditional rule deleting objects under tokens/ ending %q at var.bucket_lifecycle.messages_days: a launcher's message to a running run would stay in the bucket for ever, and the versioned bucket would keep it readable as a noncurrent version after that (F11)", runmsg.Suffix)
	}
	// A rule inside a dynamic block is planned only when the block's variable
	// is set, which is the budget token's shape and not the message's.
	if strings.HasSuffix(strings.TrimSpace(text[:loc[0]]), "content {") {
		t.Errorf("the message rule sits inside a dynamic block, so it is planned only when a variable is set: F11's rule is unconditional, because a message reaches a run whether or not the budget is enabled")
	}
}
