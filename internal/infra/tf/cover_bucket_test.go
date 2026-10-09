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
