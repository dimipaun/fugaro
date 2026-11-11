# The workspace's read of this project's database: ONE custom role holding ONE
# permission, and ONE binding of it to the workspace service account the
# member's own config pins (docs/workspaces/spec.md §5; task W7 of
# docs/workspaces/implementation-plan.md).
#
# The workspace's credentials service meters every call it pays for and
# enforces the workspace's cap against the figure it keeps in its OWN database.
# What it needs from a member is the member's own ledger, read once per
# reconciliation run to compare the two figures — a READ, and nothing else. The
# read is live before its consumer is: the alarm that would perform it has no
# caller yet (budget.ReconcileWorkspace is uncalled outside its tests), so a
# member's account is granted this read from the day the member pins one while
# nothing reads through it (the plan's W7 paragraph states it).
# Realtime Database IAM has no path-level scope: a binding is per instance and
# reaches the whole database (the reasoning credentials.tf spells out at
# length), so the narrowest grant that can do it is the instance's read
# permission alone. It is the read half of the credentials service's own custom
# role (modules/installation/credentials.tf) and deliberately NOT that role:
# firebasedatabase.instances.update is not bound here at all, so the write
# direction of "a member's database is the member's" is a property of IAM
# rather than of the service's code. There is no write permission for a later
# revision of the service to use, and nothing to enforce it with.
#
# The role is declared in this project (var.project is the FP, and the database
# belongs to the FP, D6) and it is spelled LITERALLY at the binding rather than
# read from the role resource's own name attribute, for the reason
# credentials.tf gives: google_project_iam_custom_role's name is computed, so a
# plan could not say which role a grant carries, and internal/infra/tf's cover
# accepts only a role it can read. var.project is known at plan time, so the
# literal is.
#
# Who is bound, and who decides it. The account is the WORKSPACE's service
# account — a Fugaro account of another project — named in the MEMBER's own
# config (localcfg's WorkspacePin.ServiceAccount, beside the issuer, the
# Firebase project and the assertion key the member pins), and never read from
# the workspace's published record: §2's record names no service account, for
# the same reason it names no issuer, so a workspace cannot widen its own reach
# by publishing a different one. A member whose config names no workspace
# passes "" and nothing is created — the absent pin is not an account.
#
# A managed project's plan gate refuses this binding, and that is the correct
# answer rather than a limitation: the member hands read access to a principal
# that is not this installation's own, which is a loosening, and the managed
# cover admits no project-level IAM at all (internal/infra/tf/managed_cover.go,
# "google_project_iam_member"). What the gate is never handed is this plan: the
# root this file belongs to is planned only by `fugaro update
# --firebase-project`, that flag shapes the installation rather than converging
# it, and a shaped run keeps no gate at all (internal/cli/init.go's
# installationFlags, initRun.gateable) — so the binding is applied by the
# stage's own typed confirmation and the cover's refusal is what would answer if
# a later shape of this grant were ever judged. The confirmation is the member's
# owner's, given at their own terminal, once per member, at the moment they join
# the workspace.
#
# The account must already exist, exactly as coordinator_account's must: IAM
# refuses a member it cannot find, so this root is applied after the
# workspace's own installation created the account.

variable "workspace_service_account" {
  description = "The email of the workspace service account this member's database is read by (docs/workspaces/spec.md §5): the account the member's own workspace_pin names. When set, it is granted a custom role holding firebasedatabase.instances.get and nothing else, so the workspace's credentials service can read the member's ledger for the reconciliation alarm. Empty — the default, and the state of every installation that belongs to no workspace — creates no role and no binding."
  type        = string
  default     = ""

  validation {
    condition     = var.workspace_service_account == "" || can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\\.iam\\.gserviceaccount\\.com$", var.workspace_service_account))
    error_message = "workspace_service_account must be empty or a service account email, <id>@<project>.iam.gserviceaccount.com."
  }
}

resource "google_project_iam_custom_role" "workspace_read" {
  count = var.workspace_service_account == "" ? 0 : 1

  project = var.project
  role_id = "fugaroWorkspaceRead"
  title   = "Fugaro workspace (database read only)"
  permissions = [
    "firebasedatabase.instances.get", # read: a member's ledger, for the workspace's reconciliation alarm. No write, and no instance lifecycle.
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_member" "workspace_read" {
  count = var.workspace_service_account == "" ? 0 : 1

  project = var.project
  role    = "projects/${var.project}/roles/fugaroWorkspaceRead"
  member  = "serviceAccount:${var.workspace_service_account}"

  depends_on = [google_project_iam_custom_role.workspace_read]
}
