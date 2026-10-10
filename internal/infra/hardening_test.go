package infra

import (
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

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
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 0 || sev(fs, "info") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("not hardened: a launcher with objectAdmin and no launcher grants", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{{Role: "roles/storage.objectAdmin", Members: []string{a, o}}}}
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
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
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
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
		fs := RunsBucketFindings(p, b, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("lists unknown: unconditioned writers are info", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, nil, nil)
		if sev(fs, "warning") != 0 {
			t.Fatalf("findings %+v", fs)
		}
	})
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
