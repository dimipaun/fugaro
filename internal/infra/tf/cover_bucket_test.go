package tf

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	cbBucket = "fugaro-runs-proj-1234"
	cbPerson = "user:launcher@example.com"
	cbJobSA  = "serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"
)

func bucketGrant(member, role string, cond map[string]any) *Plan {
	conds := []any{}
	if cond != nil {
		conds = []any{cond}
	}
	return &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": cbBucket, "member": member, "role": role, "condition": conds,
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}}}},
	}}}
}

func cond(title, expr string) map[string]any {
	return map[string]any{"title": title, "expression": expr, "description": nil}
}

func TestCoverBucketConditions(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	launcher := cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket))
	job := cond("fugaro-x", gcp.BucketCondition(cbBucket, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef"))
	for _, tc := range []struct {
		name          string
		member, role  string
		cond          map[string]any
		covered       bool
		wantInProblem string
	}{
		{"launcher writes runs/", cbPerson, "roles/storage.objectUser", launcher, true, ""},
		{"launcher reads, unconditioned", cbPerson, "roles/storage.objectViewer", nil, true, ""},
		{"operator objectAdmin, unconditioned", cbPerson, "roles/storage.objectAdmin", nil, true, ""},
		{"job account under its prefixes", cbJobSA, "roles/storage.objectUser", job, true, ""},
		{"person objectUser without a condition", cbPerson, "roles/storage.objectUser", nil, false, "condition"},
		{"person objectUser on fugaro/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/")`), false, "condition"},
		{"person objectUser on another bucket's runs/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition("fugaro-runs-other")), false, "condition"},
		{"person objectUser, right expression, other title", cbPerson, "roles/storage.objectUser",
			cond("mine", gcp.LauncherBucketCondition(cbBucket)), false, "condition"},
		{"person objectUser with a description", cbPerson, "roles/storage.objectUser",
			map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition(cbBucket), "description": "x"}, false, "description"},
		{"objectAdmin under a condition", cbPerson, "roles/storage.objectAdmin", launcher, false, "condition"},
		{"objectViewer under a condition", cbPerson, "roles/storage.objectViewer", launcher, false, "condition"},
		{"account objectUser without a condition", cbJobSA, "roles/storage.objectUser", nil, false, "condition"},
		{"account objectUser with the launchers' condition", cbJobSA, "roles/storage.objectUser", launcher, false, "condition"},
		{"account objectUser on fugaro/<x>/", cbJobSA, "roles/storage.objectUser",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/acme/")`), false, "condition"},
		{"account objectUser, one clause on another bucket", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs"}, "s")+" || "+gcp.BucketCondition("fugaro-runs-other", []string{"cache"}, "s")), false, "condition"},
		// accountClause must anchor the end of the clause too: without it, a
		// clause that starts with a legitimate prefix but keeps going (widening
		// the CEL expression past the closing quote) would match on its valid
		// prefix alone and the extra text would ride along uncovered.
		{"account objectUser, a clause with trailing text after the closing quote", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs"}, "s")+` && true`), false, "condition"},
		// If the slug group were ever loosened to something permissive like
		// `.*`, this single "clause" (no spaces around || so the naive " || "
		// split never splits it) would match end-to-end despite smuggling an
		// unconditional `||true||` past the Go-side check: CEL has no such
		// space requirement, so Google's policy engine would read it as an
		// always-true condition. The slug charset must stay strict enough that
		// this can never match.
		{"account objectUser, a clause smuggling ||true|| past the space-only split", cbJobSA, "roles/storage.objectUser",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/runs/x")||true||resource.name.startsWith("y/")`), false, "condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NotCovered(bucketGrant(tc.member, tc.role, tc.cond), nil)
			if tc.covered && len(got) != 0 {
				t.Fatalf("not covered: %v", got)
			}
			if !tc.covered && (len(got) != 1 || !strings.Contains(got[0], tc.wantInProblem)) {
				t.Fatalf("got %v, want one problem naming %q", got, tc.wantInProblem)
			}
		})
	}
}

// A condition known only after apply is never covered.
func TestCoverBucketConditionUnknown(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	p := bucketGrant(cbPerson, "roles/storage.objectUser", cond(gcp.LauncherBucketConditionTitle, ""))
	p.ResourceChanges[0].Change.AfterUnknown = map[string]any{"condition": []any{map[string]any{"expression": true}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "not known") {
		t.Fatalf("got %v", got)
	}
}

// objects() silently drops a list entry that is not a map, so a condition
// list holding something else (not a block at all) must not read as "no
// condition": an objectAdmin grant with condition: ["true"] must still be
// refused, not covered as unconditioned.
func TestCoverBucketConditionNonBlockListEntryRefused(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	for name, condition := range map[string]any{
		"a string in the list":  []any{"true"},
		"a number in the list":  []any{7},
		"condition is a string": "true",
		"condition is a number": 7,
		"condition is a bool":   true,
	} {
		t.Run(name, func(t *testing.T) {
			p := &Plan{ResourceChanges: []ResourceChange{{
				Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
				Type:    "google_storage_bucket_iam_member",
				Change: Change{Actions: []string{"create"}, After: map[string]any{
					"bucket": cbBucket, "member": cbPerson, "role": "roles/storage.objectAdmin",
					"condition": condition,
				}, AfterUnknown: map[string]any{}},
			}}}
			if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
				t.Fatalf("got %v, want one problem naming condition", got)
			}
		})
	}
}

// A list with one valid condition block and one entry that is not a block at
// all must not read as "exactly one condition" (objects() would silently
// drop the bad entry, leaving what looks like a single legitimate launcher
// condition): a person's objectUser grant like this must still be refused,
// not wrongly covered because the surviving entry happens to match.
func TestCoverBucketConditionOneGoodOneBadListEntryRefused(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	p := &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": cbBucket, "member": cbPerson, "role": "roles/storage.objectUser",
			"condition": []any{cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket)), "true"},
		}, AfterUnknown: map[string]any{}},
	}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
		t.Fatalf("got %v, want one problem naming condition", got)
	}
}

// A condition's description known only after apply is not covered, the same
// as an unknown title or expression.
func TestCoverBucketConditionDescriptionUnknown(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	p := bucketGrant(cbPerson, "roles/storage.objectUser", cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket)))
	p.ResourceChanges[0].Change.AfterUnknown = map[string]any{"condition": []any{map[string]any{"description": true}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "not known") {
		t.Fatalf("got %v", got)
	}
}

// Exactly two condition blocks must be refused as "more than one condition",
// not covered under a table that stopped counting at two.
func TestCoverBucketConditionTwoBlocksRefused(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	launcher := cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket))
	p := &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": cbBucket, "member": cbPerson, "role": "roles/storage.objectUser",
			"condition": []any{launcher, cond("x", "true")},
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}, map[string]any{}}}},
	}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "more than one condition") {
		t.Fatalf("got %v, want one problem naming \"more than one condition\"", got)
	}
}

// A person's condition must match the grant's own bucket attribute, not just
// name some bucket that happens to be one of this run's others.
func TestCoverBucketConditionPersonMatchesItsOwnBucket(t *testing.T) {
	const otherBucket = "fugaro-runs-other"
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket, otherBucket}, Listed: []string{cbPerson}}
	// bucketGrant's own "bucket" attribute is always cbBucket; the condition
	// here names otherBucket, this run's other bucket.
	p := bucketGrant(cbPerson, "roles/storage.objectUser", cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(otherBucket)))
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
		t.Fatalf("got %v, want one problem naming condition: a person's condition naming another of this run's buckets than the grant's own", got)
	}
}

// An account's clause must name one of this run's buckets, even when the
// grant's own bucket attribute is not yet known (it is created in the same
// module): a clause naming a bucket outside this run entirely must still be
// refused.
func TestCoverBucketConditionAccountClauseMustNameARunBucket(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	bucketRes := ResourceChange{
		Address: `module.installation.google_storage_bucket.runs`,
		Type:    "google_storage_bucket",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"public_access_prevention": "enforced", "uniform_bucket_level_access": true,
		}, AfterUnknown: map[string]any{}},
	}
	grant := ResourceChange{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"member": cbJobSA, "role": "roles/storage.objectUser",
			"condition": []any{cond("t", gcp.BucketCondition("fugaro-runs-evil", []string{"runs"}, "s"))},
		}, AfterUnknown: map[string]any{"bucket": true, "condition": []any{map[string]any{}}}},
	}
	if got := c.NotCovered(&Plan{ResourceChanges: []ResourceChange{bucketRes, grant}}, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
		t.Fatalf("got %v, want one problem naming condition: a clause naming a bucket outside this run", got)
	}
}

// A configured runs bucket is not charset-checked everywhere it is read
// (localcfg only validates runs_bucket, not a bucket_url, so a name like
// "MyBucket" reaches Cover.Buckets verbatim): NotCovered must still refuse
// the grant, not panic, even though gcp.LauncherBucketCondition itself
// panics on such a name.
func TestCoverBucketConditionBadConfiguredBucketNeverPanics(t *testing.T) {
	const badBucket = "MyBucket"
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{badBucket}, Listed: []string{cbPerson}}
	p := &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": badBucket, "member": cbPerson, "role": "roles/storage.objectUser",
			"condition": []any{cond(gcp.LauncherBucketConditionTitle, `resource.name.startsWith("projects/_/buckets/`+badBucket+`/objects/runs/")`)},
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}}}},
	}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
		t.Fatalf("got %v, want one problem naming condition (no panic)", got)
	}
}

// An account's bucket condition may name any of this run's buckets in
// general, but every clause of one grant must match that grant's own
// "bucket" attribute: a job account's clause naming a different (but still
// listed) bucket than the grant's own must not be covered.
func TestCoverBucketConditionAccountClauseMatchesItsOwnBucket(t *testing.T) {
	const otherBucket = "fugaro-runs-other"
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket, otherBucket}, Listed: []string{cbPerson}}
	p := &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.installation.google_storage_bucket_iam_member.x["k"]`,
		Type:    "google_storage_bucket_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"bucket": cbBucket, "member": cbJobSA, "role": "roles/storage.objectUser",
			"condition": []any{cond("t", gcp.BucketCondition(otherBucket, []string{"runs"}, "s"))},
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}}}},
	}}}
	if got := c.NotCovered(p, nil); len(got) != 1 || !strings.Contains(got[0], "condition") {
		t.Fatalf("got %v, want one problem naming condition: a clause on another of this run's buckets than the grant's own", got)
	}
}
