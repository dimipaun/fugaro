package infra

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// LogBucketAddress is the log bucket's address in the installation root.
const LogBucketAddress = "module.installation.google_logging_project_bucket_config.fugaro[0]"

// TagMoverRoleAddress is the tag mover role's address in the installation
// root.
const TagMoverRoleAddress = "module.installation.google_project_iam_custom_role.tag_mover"

// SharedRegistryAddress is the shared image registry's address in the
// installation root (design docs/forge-authorship-images/shared-images.md
// §2.6).
const SharedRegistryAddress = "module.installation.google_artifact_registry_repository.shared"

// ForgetCreates are the resources a rollback's plan may create: each is one
// the installation's own config declares and an installation applied before
// it existed doesn't have, so the plan creates it, it stays, and the next
// fugaro update adopts it by its mark (DiscoverInstallation) rather than
// colliding with it. Every other create means the state isn't this
// installation's, which CheckForgetPlan refuses.
var ForgetCreates = []string{
	TagMoverRoleAddress,
	SharedRegistryAddress,
}

// ForgetDeletes are the addresses the rollback's apply may delete: the log
// isolation, which is behaviour M5 added and M4 doesn't expect (the
// exclusion keeps job logs out of _Default, where M4 reads them).
var ForgetDeletes = []string{
	LogBucketAddress,
	"module.installation.google_logging_project_sink.fugaro[0]",
	"module.installation.google_logging_project_exclusion.fugaro_from_default[0]",
	"module.installation.google_logging_log_view.runs[0]",
}

// forgetViewGrants is the log view's grants, one instance per launcher or
// operator, which the rollback also deletes.
const forgetViewGrants = "module.installation.google_logging_log_view_iam_member.runs"

// ForgetSpec is spec with the behaviour M5 added turned off: no log
// isolation and no registry cleanup.
func ForgetSpec(spec InstallationSpec) InstallationSpec {
	spec.LogIsolation = new(false)
	// Off, and a dry run, as --registry-cleanup=off is: the installation
	// outputs dry_run, which every repository registry copies.
	spec.RegistryCleanup = RegistryCleanup{DryRun: true}
	return spec
}

// ForgetAllowDelete is the rollback's allow-list for p: ForgetDeletes, and
// each instance of the log view's grants that p deletes. Nothing else.
func ForgetAllowDelete(p *tf.Plan) []string {
	allow := slices.Clone(ForgetDeletes)
	for _, rc := range p.ResourceChanges {
		if strings.HasPrefix(rc.Address, forgetViewGrants+"[") {
			allow = append(allow, rc.Address)
		}
	}
	slices.Sort(allow)
	return slices.Compact(allow)
}

// CheckForgetPlan refuses a rollback plan that creates anything: it only
// takes M5's behaviour away, so a create means the state is not the
// installation's (or is empty). The exceptions are the resources in
// ForgetCreates, which an installation applied before they existed doesn't
// have yet: the plan creates them, and they stay.
func CheckForgetPlan(p *tf.Plan) error {
	var creates []string
	for _, rc := range p.ResourceChanges {
		if slices.Equal(rc.Change.Actions, []string{"create"}) && slices.Contains(ForgetCreates, rc.Address) {
			continue
		}
		if slices.Contains(rc.Change.Actions, "create") {
			creates = append(creates, rc.Address)
		}
	}
	if len(creates) > 0 {
		return &UserError{Err: fmt.Errorf("the rollback's plan would create %d resource(s), so the state doesn't hold this installation; nothing was applied:\n  %s",
			len(creates), strings.Join(creates, "\n  "))}
	}
	return nil
}

// LogBucketUndelete is the command that restores the log bucket while it
// is pending deletion (7 days), at the location the bucket is in (R5: it is
// permanent, so the command names the bucket's own and nothing derives it).
func LogBucketUndelete(project, location string) string {
	return "gcloud logging buckets undelete " + LogBucket + " --location=" + location + " --project " + project
}

// LogBucketWhere is where an installation's log bucket is, read by the same
// listing discovery reads it with — the project's buckets in every location,
// ours by ID — so a command that must name the bucket's location has one read
// to make and no second lookup (F10). It is what `fugaro update --forget`
// prints its undelete command with: it runs no discovery, and an installation
// made before this mapping has its bucket in `global` while a new one is where
// place.For says.
//
// An empty location, with no error, means the project holds no such bucket.
// The caller is a message printed after a rollback, so a read that fails is
// theirs to fall back on, not a reason to fail the rollback.
func LogBucketWhere(ctx context.Context, c *Clients, project, id string) (string, error) {
	b, err := logBucketNamed(ctx, c, project, id)
	if err != nil || b == nil {
		return "", err
	}
	loc, _ := logBucketLocationID(b.Name)
	return loc, nil
}
