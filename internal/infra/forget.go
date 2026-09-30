package infra

import (
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
// installation's (or is empty). The one exception is the tag mover role,
// which an installation applied before it existed doesn't have yet: the
// plan creates it, and it stays, ungranted, like the other custom roles.
func CheckForgetPlan(p *tf.Plan) error {
	var creates []string
	for _, rc := range p.ResourceChanges {
		if rc.Address == TagMoverRoleAddress && slices.Equal(rc.Change.Actions, []string{"create"}) {
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
// is pending deletion (7 days), which a retried migration runs first.
func LogBucketUndelete(project string) string {
	return "gcloud logging buckets undelete " + LogBucket + " --location=global --project " + project
}
