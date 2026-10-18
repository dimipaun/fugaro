package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

const dbBucket = "fugaro-runs-proj-1234"

// withRunsBucket gives the doctor rig a storage endpoint holding the runs
// bucket with policy, and the local config's lists and user.
func (r *doctorRig) withRunsBucket(t *testing.T, g *gcpfake.GCS, extra string, policy ...gcpfake.Binding) {
	t.Helper()
	g.AddBucket(dbBucket, 123456789012, map[string]string{"fugaro": "managed"})
	g.SetBucketPolicy(dbBucket, policy)
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { storage: "+g.URL+"/storage/v1/, ", 1) + extra
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hardenedPolicy() []gcpfake.Binding {
	return []gcpfake.Binding{
		{Role: "roles/storage.objectAdmin", Members: []string{"user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:a@example.com"}},
		{Role: "roles/storage.objectUser", Members: []string{"user:a@example.com"},
			Condition: &gcpfake.IAMCondition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(dbBucket)}},
	}
}

const dbLists = "terraform: { launchers: [\"user:a@example.com\", \"user:o@example.com\"], operators: [\"user:o@example.com\"] }\n"

// runDoctorJSON runs fugaro doctor --json against the rig's project.
func (r *doctorRig) runDoctorJSON(t *testing.T) doctorOutput {
	t.Helper()
	return doctorJSON(t)
}

func TestDoctorRunsBucketIAMHardenedIsOK(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	o := r.runDoctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || !c.OK {
		t.Fatalf("check = %+v (%v)", c, ok)
	}
}

func TestDoctorRunsBucketIAMOldGrantWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, gcpfake.Binding{Role: "roles/storage.objectAdmin", Members: []string{"user:a@example.com", "user:o@example.com"}})
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" && strings.Contains(c.Fix, "fugaro init") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning: %+v", o.Checks)
	}
	if !o.OK {
		t.Fatal("a warning failed doctor without --strict")
	}
	// The summary check itself (ID exactly "runs-bucket-iam", not one of the
	// runs-bucket-iam-N findings) must also carry the warning: that is what
	// --strict acts on, and a findingChecks that forgets to aggregate would
	// leave it OK here while still emitting the per-finding warnings above.
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK || sum.Severity != "warning" {
		t.Fatalf("summary check = %+v (%v), want a warning", sum, ok)
	}
}

func TestDoctorRunsBucketIAMSelfTest(t *testing.T) {
	for _, tc := range []struct {
		name, user string
		held       []string
		sev        string
		ok         bool
	}{
		{"launcher, hardened", "a@example.com", nil, "", true},
		{"operator holds writes", "o@example.com", []string{"storage.objects.create", "storage.objects.delete"}, "", true},
		{"launcher holds writes", "a@example.com", []string{"storage.objects.create"}, "warning", false},
		{"unknown caller holds writes", "", []string{"storage.objects.create"}, "info", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, g := newDoctorRig(t), gcpfake.NewGCS(t)
			cfg := strings.Replace(r.cfg, "user: someone@example.com\n", "", 1)
			if tc.user != "" {
				cfg += "user: " + tc.user + "\n"
			}
			r.cfg = cfg
			r.withRunsBucket(t, g, dbLists)
			g.RefuseBucketPolicy(dbBucket)
			g.SetHeldPermissions(dbBucket, tc.held...)
			o := r.runDoctorJSON(t)
			c, found := doctorCheckByID(o.Checks, "runs-bucket-iam")
			if !found || c.OK != tc.ok || c.Severity != tc.sev {
				t.Fatalf("check = %+v (found %v)", c, found)
			}
		})
	}
}

// A test configuration (no_auth with no storage endpoint) reads no bucket.
func TestDoctorRunsBucketIAMSkippedWithoutAStorageEndpoint(t *testing.T) {
	r := newDoctorRig(t)
	o := r.runDoctorJSON(t)
	if _, found := doctorCheckByID(o.Checks, "runs-bucket-iam"); found {
		t.Fatal("checked a bucket under a test configuration")
	}
}

// A transient read failure (500, not a 403 refusal) must still surface a
// warning, never silence: the default: branch of doctorRunsBucketIAM's
// switch, which TestDoctorRunsBucketIAMSelfTest's RefuseBucketPolicy (403)
// cases never reach.
func TestDoctorRunsBucketIAMFailedPolicyReadWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	g.FailBucketPolicy(dbBucket)
	o := r.runDoctorJSON(t)
	c, found := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !found || c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "could not check who can write") {
		t.Fatalf("check = %+v (found %v)", c, found)
	}
}

// A launcher's project-level role (roles/editor, say) writes every bucket's
// objects whatever the bucket's own policy says: doctor must report it
// (infra.ProjectStorageWriters), not just the hardened bucket policy.
func TestDoctorRunsBucketIAMProjectEditorWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/editor", Members: []string{"user:a@example.com"}})
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" &&
			strings.Contains(c.Problem, "roles/editor") && strings.Contains(c.Fix, "fugaro init --operator") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no project-editor warning: %+v", o.Checks)
	}
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK {
		t.Fatalf("summary = %+v (%v), want a warning", sum, ok)
	}
}

// When the project's own policy can't be read, a launcher who bypasses the
// bucket's policy entirely through a project-level role would otherwise go
// undetected: that must be a warning, not a silent OK.
func TestDoctorRunsBucketIAMProjectPolicyUncheckedWarns(t *testing.T) {
	r, g := newDoctorRig(t), gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	r.crm.DenyPolicy("proj-1234")
	o := r.runDoctorJSON(t)
	var warned bool
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "runs-bucket-iam") && c.Severity == "warning" && strings.Contains(c.Problem, "could not check which launchers hold project-level storage roles") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no unchecked-project warning: %+v", o.Checks)
	}
	sum, ok := doctorCheckByID(o.Checks, "runs-bucket-iam")
	if !ok || sum.OK {
		t.Fatalf("summary = %+v (%v), want a warning", sum, ok)
	}
}

// findingChecks must redact a finding's Fix the same way it redacts Problem:
// Fix carries the raw IAM member (so the printed gcloud command is actually
// correct to paste), but the doctorCheck it becomes is printed to a
// terminal, so control characters and ANSI escapes in a member name must
// never reach it unescaped.
func TestFindingChecksRedactsControlCharactersInFix(t *testing.T) {
	const raw = "user:x\x1b[31m@example.com"
	checks := findingChecks([]infra.BucketFinding{{Severity: "warning", Problem: "p", Fix: "gcloud ... --member=" + raw}})
	if len(checks) != 2 {
		t.Fatalf("checks = %+v", checks)
	}
	fix := checks[1].Fix
	if strings.Contains(fix, "\x1b") {
		t.Fatalf("Fix leaked a raw escape byte: %q", fix)
	}
	if !strings.Contains(fix, `\u001b`) {
		t.Fatalf("Fix was not escaped: %q", fix)
	}
}

// jobBinding is a job account's objectUser grant on its own repository's
// runs/, cache/ and locks/, as the workflow module writes it.
func jobBinding(saID, slug string) gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:" + saID + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.BucketConditionTitle(saID), Expression: gcp.BucketCondition(dbBucket, gcp.JobBucketPrefixes, slug)}}
}

// jobTokenBinding is the same account's read-only grant on tokens/<slug>/.
func jobTokenBinding(saID, slug string) gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectViewer", Members: []string{"serviceAccount:" + saID + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.TokenBucketConditionTitle(saID), Expression: gcp.TokenBucketCondition(dbBucket, slug)}}
}

// TestDoctorJobTokenGrant: a job account that has its runs/ grant but not the
// read-only grant on tokens/ (a repository not re-applied with fugaro init
// --repo since the budget token moved there, budget-token-channel.md §7.2) is
// a warning naming the account and the cure: its runs cannot read the sealed
// token, and read it as missing. With the grant, the check is OK.
func TestDoctorJobTokenGrant(t *testing.T) {
	const sa, slug = "fugaro-acme-app-w-1234abcd", "acme-app-0123456789abcdef"
	t.Run("missing", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), jobBinding(sa, slug))...)
		o := r.runDoctorJSON(t)
		c, ok := doctorCheckByID(o.Checks, "job-token-grant")
		if !ok || c.OK || c.Severity != "warning" {
			t.Fatalf("check = %+v (%v), want a warning", c, ok)
		}
		var named bool
		for _, c := range o.Checks {
			if strings.HasPrefix(c.ID, "job-token-grant-") && strings.Contains(c.Problem, sa) && strings.Contains(c.Problem, "tokens/") && strings.Contains(c.Fix, "fugaro init --repo") {
				named = true
			}
		}
		if !named {
			t.Fatalf("no finding names the account and the cure: %+v", o.Checks)
		}
		if s, _ := doctorCheckByID(o.Checks, "runs-bucket-iam"); !s.OK {
			t.Fatalf("the job's own runs/ grant made runs-bucket-iam warn: %+v", s)
		}
	})
	t.Run("present", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), jobBinding(sa, slug), jobTokenBinding(sa, slug))...)
		o := r.runDoctorJSON(t)
		if c, ok := doctorCheckByID(o.Checks, "job-token-grant"); !ok || !c.OK {
			t.Fatalf("check = %+v (%v), want OK", c, ok)
		}
		if s, _ := doctorCheckByID(o.Checks, "runs-bucket-iam"); !s.OK {
			t.Fatalf("the job's tokens/ grant made runs-bucket-iam warn: %+v", s)
		}
	})
	t.Run("another slug's grant does not count", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), jobBinding(sa, slug), jobTokenBinding(sa, "other-0123456789abcdef"))...)
		o := r.runDoctorJSON(t)
		if c, ok := doctorCheckByID(o.Checks, "job-token-grant"); !ok || c.OK {
			t.Fatalf("check = %+v (%v), want a warning", c, ok)
		}
	})
}

// projectJobBinding is a shared project job account's objectUser grant on the
// whole of runs/, cache/ and locks/, as modules/project writes it
// (gcp.ProjectBucketCondition: no slug, because the job serves every
// repository admitted to its profile).
func projectJobBinding(saID string) gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:" + saID + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.BucketConditionTitle(saID), Expression: gcp.ProjectBucketCondition(dbBucket, gcp.JobBucketPrefixes)}}
}

// projectJobTokenBinding is the same account's read-only grant on the whole of
// tokens/ (gcp.ProjectTokenBucketCondition, gcp.ProjectTokenBucketConditionTitle).
func projectJobTokenBinding(saID string) gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectViewer", Members: []string{"serviceAccount:" + saID + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.ProjectTokenBucketConditionTitle(saID), Expression: gcp.ProjectTokenBucketCondition(dbBucket)}}
}

// TestDoctorJobTokenGrantProjectJob is the same check for a shared project job
// account (plan task 3.2 step 2; budget-token-channel.md §7.2). Its grants are
// the widened pair — objectUser on the three prefixes, objectViewer on the
// whole of tokens/ — and its cure is the installation's fugaro init, not a
// repository's fugaro init --repo: the account is created by the installation
// root, and nothing in a repository's checkout can re-apply it.
func TestDoctorJobTokenGrantProjectJob(t *testing.T) {
	sa := gcp.ProjectServiceAccountID("standard", "github", "")
	finding := func(t *testing.T, o doctorOutput) (doctorCheck, bool) {
		t.Helper()
		for _, c := range o.Checks {
			if strings.HasPrefix(c.ID, "job-token-grant-") && strings.Contains(c.Problem, sa) {
				return c, true
			}
		}
		return doctorCheck{}, false
	}
	t.Run("missing", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), projectJobBinding(sa))...)
		o := r.runDoctorJSON(t)
		c, ok := finding(t, o)
		if !ok || c.OK || c.Severity != "warning" {
			t.Fatalf("finding = %+v (%v), want a warning", c, ok)
		}
		if !strings.Contains(c.Problem, "tokens/") || !strings.Contains(c.Fix, "fugaro init") || strings.Contains(c.Fix, "--repo") {
			t.Fatalf("finding = %+v, want it to name tokens/ and the installation's fugaro init", c)
		}
		if s, _ := doctorCheckByID(o.Checks, "runs-bucket-iam"); !s.OK {
			t.Fatalf("the project job's own runs/ grant made runs-bucket-iam warn: %+v", s)
		}
	})
	t.Run("present", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), projectJobBinding(sa), projectJobTokenBinding(sa))...)
		o := r.runDoctorJSON(t)
		if c, ok := doctorCheckByID(o.Checks, "job-token-grant"); !ok || !c.OK {
			t.Fatalf("check = %+v (%v), want OK", c, ok)
		}
		if c, ok := finding(t, o); ok {
			t.Fatalf("a finding for the account that holds both grants: %+v", c)
		}
		if s, _ := doctorCheckByID(o.Checks, "runs-bucket-iam"); !s.OK {
			t.Fatalf("the project job's tokens/ grant made runs-bucket-iam warn: %+v", s)
		}
	})
	t.Run("a per-repository tokens grant does not count", func(t *testing.T) {
		// The shared account serves every repository, so a grant naming one
		// repository's tokens/ leaves every other repository's runs unable to
		// read their token: the whole-prefix grant is the one it must hold.
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), projectJobBinding(sa), jobTokenBinding(sa, "acme-app-0123456789abcdef"))...)
		o := r.runDoctorJSON(t)
		if c, ok := finding(t, o); !ok || c.OK {
			t.Fatalf("finding = %+v (%v), want a warning", c, ok)
		}
	})
	t.Run("a widened runs/ grant of another title is never silently green", func(t *testing.T) {
		// The account ID the tokens/ title must be built from comes out of
		// this title: one that is not this fugaro's shape leaves the account
		// with nothing to compare, which is said rather than passed over.
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		odd := projectJobBinding(sa)
		odd.Condition.Title = "custom-" + sa
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), odd, projectJobTokenBinding(sa))...)
		o := r.runDoctorJSON(t)
		c, ok := finding(t, o)
		if !ok || c.OK || c.Severity != "info" || !strings.Contains(c.Problem, "tokens/ read grant not checked") {
			t.Fatalf("finding = %+v (%v), want the unchecked line", c, ok)
		}
	})
}

// TestDoctorJobTokenGrantNeverSilentlyGreen: when doctor cannot judge the
// job accounts' tokens/ grants it says so, rather than dropping the check
// (a missing line reads as nothing wrong): a bucket policy it may not read
// (a launcher's usual case) or cannot read (a server error), and a job
// account whose runs/ grant is not in the exact shape this fugaro writes.
func TestDoctorJobTokenGrantNeverSilentlyGreen(t *testing.T) {
	const sa, slug = "fugaro-acme-app-w-1234abcd", "acme-app-0123456789abcdef"
	unchecked := func(t *testing.T, o doctorOutput, want string) {
		t.Helper()
		c, ok := doctorCheckByID(o.Checks, "job-token-grant")
		if !ok || c.OK || c.Severity == "" || !strings.Contains(c.Problem, want) {
			t.Fatalf("check = %+v (%v), want an unchecked line saying %q", c, ok, want)
		}
	}
	t.Run("policy read refused", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists)
		g.RefuseBucketPolicy(dbBucket)
		unchecked(t, r.runDoctorJSON(t), "cannot read the bucket policy")
	})
	t.Run("policy read failed", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
		g.FailBucketPolicy(dbBucket)
		unchecked(t, r.runDoctorJSON(t), "cannot read the bucket policy")
	})
	t.Run("a runs/ grant of another shape", func(t *testing.T) {
		r, g := newDoctorRig(t), gcpfake.NewGCS(t)
		odd := jobBinding(sa, slug)
		odd.Condition.Expression = gcp.BucketCondition(dbBucket, []string{"runs", "cache"}, slug)
		r.withRunsBucket(t, g, dbLists, append(hardenedPolicy(), odd)...)
		o := r.runDoctorJSON(t)
		c, ok := doctorCheckByID(o.Checks, "job-token-grant")
		if !ok || c.OK {
			t.Fatalf("check = %+v (%v), want not OK", c, ok)
		}
		var named bool
		for _, c := range o.Checks {
			if strings.HasPrefix(c.ID, "job-token-grant-") && strings.Contains(c.Problem, sa) && strings.Contains(c.Problem, "not checked") {
				named = true
			}
		}
		if !named {
			t.Fatalf("no line says the account was not checked: %+v", o.Checks)
		}
	})
}
