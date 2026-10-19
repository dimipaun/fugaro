package tf

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	cbBucket  = "fugaro-runs-proj-1234"
	cbPerson  = "user:launcher@example.com"
	cbJobSAID = "fugaro-acme-web-1a2b3c4d"
	cbJobSA   = "serviceAccount:" + cbJobSAID + "@proj-1234.iam.gserviceaccount.com"
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

// bucketGrantHeld is bucketGrant with the grant's own module creating the
// service account sa in the same plan: a bucket grant's member is a Fugaro
// job account of this run only when the module the grant is in holds or
// creates it (planAccounts), so a row whose grant is a job account's own says
// which account its plan makes.
func bucketGrantHeld(member, role string, cond map[string]any, sa string) *Plan {
	p := bucketGrant(member, role, cond)
	p.ResourceChanges = append(p.ResourceChanges, ResourceChange{
		Address: `module.installation.google_service_account.job`,
		Type:    "google_service_account",
		Change:  Change{Actions: []string{"create"}, After: map[string]any{"account_id": sa}},
	})
	return p
}

func cond(title, expr string) map[string]any {
	return map[string]any{"title": title, "expression": expr, "description": nil}
}

func TestCoverBucketConditions(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	launcher := cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket))
	job := cond("fugaro-x", gcp.BucketCondition(cbBucket, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef"))
	tokens := cond(gcp.TokenBucketConditionTitle(cbJobSAID), gcp.TokenBucketCondition(cbBucket, "acme-web-0123456789abcdef"))
	for _, tc := range []struct {
		name          string
		member, role  string
		cond          map[string]any
		sa            string // the account the grant's module creates, "" for none
		covered       bool
		wantInProblem string
	}{
		{"launcher writes runs/ and tokens/", cbPerson, "roles/storage.objectUser", launcher, "", true, ""},
		// The budget token's move (docs/design/budget-token-channel.md §7.2,
		// D16): the job account reads its own repository's tokens/<slug>/ and
		// nothing else, and may never write it.
		{"job account reads its tokens/<slug>/", cbJobSA, "roles/storage.objectViewer", tokens, cbJobSAID, true, ""},
		// The expression alone is not the modules' grant: the condition's
		// title must be the one built from the account (gcp.TokenBucketConditionTitle)
		// and the member a Fugaro job account of this run that the grant's own
		// module holds — otherwise a stranger's grant carrying the same
		// expression, and any job account whose title names someone else,
		// would read as the module's own.
		{"the history account reads tokens/<slug>/ under the job's title",
			"serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com", "roles/storage.objectViewer", tokens, "", false, "not the job account"},
		{"an account the grant's module does not hold reads tokens/<slug>/", cbJobSA, "roles/storage.objectViewer", tokens, "fugaro-other-web-1a2b3c4d", false, "not the job account"},
		{"tokens/<slug>/ under another account's title", cbJobSA, "roles/storage.objectViewer",
			cond(gcp.TokenBucketConditionTitle("fugaro-other-web-1a2b3c4d"), gcp.TokenBucketCondition(cbBucket, "acme-web-0123456789abcdef")), cbJobSAID, false, "not the job account"},
		{"job account writes tokens/<slug>/", cbJobSA, "roles/storage.objectUser", tokens, cbJobSAID, false, "condition"},
		{"job account's objectUser condition reaching tokens/", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs", "tokens"}, "acme-web-0123456789abcdef")), "", false, "condition"},
		{"job account reads every repository's tokens/", cbJobSA, "roles/storage.objectViewer",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/tokens/")`), "", false, "condition"},
		{"job account reads tokens/ and runs/", cbJobSA, "roles/storage.objectViewer",
			cond("t", gcp.TokenBucketCondition(cbBucket, "s")+" || "+gcp.BucketCondition(cbBucket, []string{"runs"}, "s")), "", false, "condition"},
		{"job account reads another bucket's tokens/", cbJobSA, "roles/storage.objectViewer",
			cond("t", gcp.TokenBucketCondition("fugaro-runs-other", "s")), "", false, "condition"},
		// The clause's slug is not tied to the grant's account, and cannot be:
		// an account ID is the profile/provider/account triple and names no
		// repository, so a plan cannot read which slug belongs to the account
		// it binds (bucketCondition's own note). The title and the member are
		// checked (the rows above); the slug is not. Accepted, deliberately:
		// this is the one tokens/ case in this table that is not a refusal.
		{"job account reads another repository's tokens/<slug>/", cbJobSA, "roles/storage.objectViewer",
			cond(gcp.TokenBucketConditionTitle(cbJobSAID), gcp.TokenBucketCondition(cbBucket, "some-other-repo-0123456789abcdef")), cbJobSAID, true, ""},
		{"a person reads tokens/<slug>/ under a condition", cbPerson, "roles/storage.objectViewer", tokens, "", false, "condition"},
		{"job account reads tokens/ with a description", cbJobSA, "roles/storage.objectViewer",
			map[string]any{"title": "t", "expression": gcp.TokenBucketCondition(cbBucket, "s"), "description": "x"}, "", false, "description"},
		{"launcher's pre-seal runs/-only condition", cbPerson, "roles/storage.objectUser",
			cond("fugaro-launchers-runs", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/runs/")`), "", false, "condition"},
		{"launcher condition widened past tokens/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(cbBucket)+` || resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/")`), "", false, "condition"},
		{"launcher reads, unconditioned", cbPerson, "roles/storage.objectViewer", nil, "", true, ""},
		{"operator objectAdmin, unconditioned", cbPerson, "roles/storage.objectAdmin", nil, "", true, ""},
		{"job account under its prefixes", cbJobSA, "roles/storage.objectUser", job, "", true, ""},
		{"person objectUser without a condition", cbPerson, "roles/storage.objectUser", nil, "", false, "condition"},
		{"person objectUser on fugaro/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/")`), "", false, "condition"},
		{"person objectUser on another bucket's runs/", cbPerson, "roles/storage.objectUser",
			cond(gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition("fugaro-runs-other")), "", false, "condition"},
		{"person objectUser, right expression, other title", cbPerson, "roles/storage.objectUser",
			cond("mine", gcp.LauncherBucketCondition(cbBucket)), "", false, "condition"},
		{"person objectUser with a description", cbPerson, "roles/storage.objectUser",
			map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition(cbBucket), "description": "x"}, "", false, "description"},
		{"objectAdmin under a condition", cbPerson, "roles/storage.objectAdmin", launcher, "", false, "condition"},
		{"objectViewer under a condition", cbPerson, "roles/storage.objectViewer", launcher, "", false, "condition"},
		{"account objectUser without a condition", cbJobSA, "roles/storage.objectUser", nil, "", false, "condition"},
		{"account objectUser with the launchers' condition", cbJobSA, "roles/storage.objectUser", launcher, "", false, "condition"},
		{"account objectUser on fugaro/<x>/", cbJobSA, "roles/storage.objectUser",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/fugaro/acme/")`), "", false, "condition"},
		{"account objectUser, one clause on another bucket", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs"}, "s")+" || "+gcp.BucketCondition("fugaro-runs-other", []string{"cache"}, "s")), "", false, "condition"},
		// accountClause must anchor the end of the clause too: without it, a
		// clause that starts with a legitimate prefix but keeps going (widening
		// the CEL expression past the closing quote) would match on its valid
		// prefix alone and the extra text would ride along uncovered.
		{"account objectUser, a clause with trailing text after the closing quote", cbJobSA, "roles/storage.objectUser",
			cond("t", gcp.BucketCondition(cbBucket, []string{"runs"}, "s")+` && true`), "", false, "condition"},
		// If the slug group were ever loosened to something permissive like
		// `.*`, this single "clause" (no spaces around || so the naive " || "
		// split never splits it) would match end-to-end despite smuggling an
		// unconditional `||true||` past the Go-side check: CEL has no such
		// space requirement, so Google's policy engine would read it as an
		// always-true condition. The slug charset must stay strict enough that
		// this can never match.
		{"account objectUser, a clause smuggling ||true|| past the space-only split", cbJobSA, "roles/storage.objectUser",
			cond("t", `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/runs/x")||true||resource.name.startsWith("y/")`), "", false, "condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := bucketGrant(tc.member, tc.role, tc.cond)
			if tc.sa != "" {
				p = bucketGrantHeld(tc.member, tc.role, tc.cond, tc.sa)
			}
			got := c.NotCovered(p, nil)
			if tc.covered && len(got) != 0 {
				t.Fatalf("not covered: %v", got)
			}
			if !tc.covered && (len(got) != 1 || !strings.Contains(got[0], tc.wantInProblem)) {
				t.Fatalf("got %v, want one problem naming %q", got, tc.wantInProblem)
			}
		})
	}
}

// A tokens/<slug>/ grant whose member the module computes is covered only for
// the account its title names, and only when the same module creates that
// account in this plan: the title is built from the account (gcp.TokenBucketConditionTitle),
// so a grant whose title names an account the module does not create is not
// the module's own grant.
func TestCoverBucketTokenGrantComputedMember(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}}
	expr := gcp.TokenBucketCondition(cbBucket, "acme-web-0123456789abcdef")
	computed := func(title, sa string) *Plan {
		p := bucketGrantHeld("", "roles/storage.objectViewer", cond(title, expr), sa)
		p.ResourceChanges[0].Change.AfterUnknown = map[string]any{"member": true, "condition": []any{map[string]any{}}}
		return p
	}
	if got := c.NotCovered(computed(gcp.TokenBucketConditionTitle(cbJobSAID), cbJobSAID), nil); len(got) != 0 {
		t.Fatalf("a computed member naming the account the module creates is not covered: %v", got)
	}
	for name, p := range map[string]*Plan{
		"the title names an account the module does not create": computed(gcp.TokenBucketConditionTitle("fugaro-other-web-1a2b3c4d"), cbJobSAID),
		"the title is not an account's":                         computed("fugaro-tokens", cbJobSAID),
	} {
		t.Run(name, func(t *testing.T) {
			got := c.NotCovered(p, nil)
			if len(got) != 1 || !strings.Contains(got[0], "not the job account") {
				t.Fatalf("got %v, want one problem naming the account rule", got)
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
