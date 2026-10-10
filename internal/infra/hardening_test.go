package infra

import (
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const hardeningProject = "proj-1234"

func TestRunsBucketFindings(t *testing.T) {
	const b = "fugaro-runs-proj-1234"
	const a, o = "user:a@example.com", "user:o@example.com"
	launcherCond := &storage.Expr{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(b)}
	hardened := []*storage.PolicyBindings{
		{Role: "roles/storage.objectAdmin", Members: []string{o}},
		{Role: "roles/storage.objectViewer", Members: []string{a, "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"}},
		{Role: "roles/storage.objectUser", Members: []string{a}, Condition: launcherCond},
		{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"},
			Condition: &storage.Expr{Title: "fugaro-x", Expression: gcp.BucketCondition(b, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef")}},
		{Role: "roles/storage.legacyBucketOwner", Members: []string{"projectOwner:proj-1234", "projectEditor:proj-1234"}},
	}
	sev := func(fs []BucketFinding, s string) (n int) {
		for _, f := range fs {
			if f.Severity == s {
				n++
			}
		}
		return n
	}
	t.Run("hardened", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 0 || sev(fs, "info") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("not hardened: a launcher with objectAdmin and no launcher grants", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{{Role: "roles/storage.objectAdmin", Members: []string{a, o}}}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 2 { // the objectAdmin and the missing grants
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "fugaro init") {
				t.Errorf("a launcher's fix is not fugaro init: %+v", f)
			}
		}
	})
	t.Run("a stranger with a conditional write on fugaro/", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "roles/storage.objectUser", Members: []string{"user:x@example.com"},
			Condition: &storage.Expr{Title: "t", Expression: `resource.name.startsWith("projects/_/buckets/` + b + `/objects/fugaro/")`}})}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "remove-iam-policy-binding") {
				t.Errorf("no remove command: %+v", f)
			}
		}
	})
	t.Run("a custom role is flagged as cannot tell", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "projects/proj-1234/roles/mine", Members: []string{"user:y@example.com"}})}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("lists unknown: unconditioned writers are info", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, hardeningProject, nil, nil)
		if sev(fs, "warning") != 0 {
			t.Fatalf("findings %+v", fs)
		}
	})
	// A launcher's objectUser grant carries the right condition title but a
	// wrong expression (not runs/ scoped, here a permissive "true"): it must
	// not be accepted as the hardened writer grant, and the launcher must
	// still be reported as lacking it (since "writer" was never set true).
	t.Run("a launcher's grant has the right title but the wrong expression", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{
			{Role: "roles/storage.objectViewer", Members: []string{a}},
			{Role: "roles/storage.objectUser", Members: []string{a},
				Condition: &storage.Expr{Title: gcp.LauncherBucketConditionTitle, Expression: "true"}},
		}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 2 { // the bad condition itself, and "lacks the grants"
			t.Fatalf("findings %+v", fs)
		}
		var sawBadCondition bool
		for _, f := range fs {
			if strings.Contains(f.Problem, "under a condition that is not the launchers' runs/ condition") {
				sawBadCondition = true
			}
		}
		if !sawBadCondition {
			t.Errorf("the wrong-expression grant was not flagged: %+v", fs)
		}
	})
	// A service account shaped exactly like a job/build account, but minted
	// in a different GCP project, must not be silently exempted: nothing
	// this installation's own Terraform could have created can live in
	// another project.
	t.Run("a same-shaped service account from another project is a warning, not exempted", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{
			{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@other-project.iam.gserviceaccount.com"},
				Condition: &storage.Expr{Title: "fugaro-x", Expression: gcp.BucketCondition(b, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef")}},
		}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
		if !strings.Contains(fs[0].Problem, "condition that is not the launchers' runs/ condition") {
			t.Errorf("not flagged as an unrecognized condition: %+v", fs)
		}
	})
	// An unrecognized service account holding ANY unconditioned write role is
	// a warning once the launcher/operator lists are known (there is no
	// legitimate reason for it), but still only info when they are not
	// (doctor cannot tell who is who). Every predefined write role, not just
	// objectAdmin/storage.admin: the review finding this pins.
	for _, role := range []string{
		"roles/storage.objectAdmin", "roles/storage.admin",
		"roles/storage.objectUser", "roles/storage.objectCreator",
		"roles/storage.legacyBucketWriter", "roles/storage.legacyBucketOwner",
	} {
		t.Run("an unrecognized service account with unconditioned "+role+" is a warning when lists are known", func(t *testing.T) {
			p := &storage.Policy{Bindings: []*storage.PolicyBindings{
				{Role: role, Members: []string{"serviceAccount:stranger@other-project.iam.gserviceaccount.com"}},
			}}
			known := RunsBucketFindings(p, b, hardeningProject, []string{o}, []string{o})
			if sev(known, "warning") != 1 || sev(known, "info") != 0 {
				t.Fatalf("known-lists findings %+v", known)
			}
			unknown := RunsBucketFindings(p, b, hardeningProject, nil, nil)
			if sev(unknown, "warning") != 0 || sev(unknown, "info") != 1 {
				t.Fatalf("unknown-lists findings %+v", unknown)
			}
		})
	}
}

func TestProjectStorageWriters(t *testing.T) {
	p := &crm.Policy{Bindings: []*crm.Binding{
		{Role: "roles/editor", Members: []string{"user:a@example.com", "user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:b@example.com"}},
	}}
	fs := ProjectStorageWriters(p, []string{"user:a@example.com", "user:b@example.com", "user:o@example.com"}, []string{"user:o@example.com"})
	if len(fs) != 1 || fs[0].Member != "user:a@example.com" || fs[0].Role != "roles/editor" || fs[0].Severity != "warning" {
		t.Fatalf("findings %+v", fs)
	}
}
