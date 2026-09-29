package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

func change(addr string, actions ...string) tf.ResourceChange {
	return tf.ResourceChange{Address: addr, Change: tf.Change{Actions: actions}}
}

// The rollback may delete the log isolation (and each view grant it
// holds), and nothing else.
func TestForgetAllowDelete(t *testing.T) {
	grant := `module.installation.google_logging_log_view_iam_member.runs["user:a@example.com"]`
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		change(ForgetDeletes[0], "delete"),
		change(grant, "delete"),
		change("module.installation.google_storage_bucket.runs", "no-op"),
		change(`module.installation.google_logging_log_view_iam_member.runsx["y"]`, "delete"),
	}}
	allow := ForgetAllowDelete(p)
	want := append(slices.Clone(ForgetDeletes), grant)
	slices.Sort(want)
	if !slices.Equal(allow, want) {
		t.Fatalf("allow = %q\nwant %q", allow, want)
	}
	if err := tf.Guard(p, allow); err == nil || !strings.Contains(err.Error(), "runsx") {
		t.Fatalf("the guard with the rollback's allow-list: %v", err)
	}
	for _, a := range ForgetDeletes {
		if !strings.HasPrefix(a, "module.installation.google_logging_") {
			t.Errorf("%s is not a log isolation resource", a)
		}
	}
	if !slices.Contains(ForgetDeletes, LogBucketAddress) {
		t.Errorf("the allow-list lacks the log bucket %s", LogBucketAddress)
	}
}

// The rollback's apply only takes away: a plan that creates something
// means the state isn't the installation's, and is refused.
func TestCheckForgetPlan(t *testing.T) {
	ok := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		change(ForgetDeletes[0], "delete"), change("module.installation.google_artifact_registry_repository.base", "update"),
	}}
	if err := CheckForgetPlan(ok); err != nil {
		t.Fatal(err)
	}
	bad := &tf.Plan{ResourceChanges: []tf.ResourceChange{change("module.installation.google_storage_bucket.runs", "create")}}
	if err := CheckForgetPlan(bad); err == nil || !strings.Contains(err.Error(), "google_storage_bucket.runs") {
		t.Fatalf("err = %v", err)
	}
}

func TestForgetSpec(t *testing.T) {
	s := ForgetSpec(installationSpec(t))
	if s.LogIsolation == nil || *s.LogIsolation || s.RegistryCleanup.Enabled {
		t.Fatalf("forget spec = %+v", s)
	}
	// The installation outputs dry_run, which every repository registry
	// copies: the rollback must not turn deletion on there.
	if !s.RegistryCleanup.DryRun {
		t.Errorf("forget spec's registry cleanup %+v is not a dry run", s.RegistryCleanup)
	}
	if got := LogBucketUndelete("proj-1234"); got != "gcloud logging buckets undelete "+LogBucket+" --location=global --project proj-1234" {
		t.Errorf("undelete = %q", got)
	}
}
