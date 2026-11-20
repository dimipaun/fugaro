# The workspace root: the workspace's OWN service in the workspace's own
# project (docs/workspaces/implementation-plan.md W3, spec.md §2, §5, §9), and
# the SA fallback's IAM account (WU3 of
# docs/workspace-projects-users/implementation-plan.md, D2803).
#
# A workspace has a project of its own — the project the workspace is named
# after, whose config carries the owner's half of the membership (D1290, D1291)
# — and its Firebase, its Realtime Database, its signer and the owner's own
# people reach it through the installation's roots, exactly as any project's do:
# `fugaro setup --name <workspace>` created that project and `fugaro update`
# converges it. What no other root creates is the workspace's SERVICE: the
# account the workspace's own bundle runs as (W6), which reads the record at
# workspaces/<name>, keeps the admitted-token hashes (D1296) and meters the
# calls the workspace's credentials pay for (W7) — all of it over IAM, because
# the database's rules deny every client (internal/budget/rules/workspace.go).
#
# So this root creates that account, the one role that names its reach and the
# ONE grant that binds them, plus the SA fallback's account with no role of its
# own — and nothing else. That "nothing else" is the security property, not a
# simplification: spec §9 has a workspace never holding another project's runs,
# buckets, jobs or repositories, and the whole point of D1290's answer is that
# the workspace's project is nobody's member. A job, a runs bucket or a
# repository deployed here would be a workspace whose compromise reaches a
# member's work, and an installation's converge would be the thing that put it
# there.
#
# fugaro update writes this root's terraform.tfvars.json into a workdir of its
# own (internal/infra's WorkspaceWorkdir, state fugaro/workspace) and plans and
# applies it there, the way it does the Firebase root's. The provider
# enablement the workspace's sign-in needs is NOT here: Identity Platform is
# initialized with no provider by `fugaro setup`'s own step, which is right for
# a member's project and wrong for a workspace's, so the workspace's providers
# are an owner-run step listed in the release notes and checked in
# docs/gcp-live-checklist.md (W3, W9).
terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

# The workspace's own project, and API quota is billed to it, as it is with user
# credentials and a quota project. It is the project the workspace is named
# after.
provider "google" {
  project               = var.project
  billing_project       = var.project
  user_project_override = true
}

# The workspace's own service: the identity the workspace's bundle runs as. It
# is a Fugaro account of THIS project and of no other, so a member's project
# never holds it and a member holds no grant on this one (spec §5).
resource "google_service_account" "workspace" {
  project      = var.project
  account_id   = var.names.workspace_account_id
  display_name = "Fugaro workspace service"
  description  = "The workspace ${var.workspace}'s own service: it reads the workspace record at workspaces/${var.workspace}, keeps the admitted-token hashes and meters the calls the workspace's credentials pay for."
}

# What that account reaches, as a role of its own: the database's two DATA
# permissions and nothing else.
#
# It is a custom role and not roles/firebasedatabase.admin for the reason
# .claude/rules/terraform.md gives — "list the exact permissions and use the
# narrowest custom role" — and for the reading SECURITY.md makes of the
# credentials service's own role: a Realtime Database IAM grant has no
# path-level scope and no condition can fence one (spec §8), so a grant here is
# the whole database whatever it is called, and the only narrower thing left to
# choose is WHICH OPERATIONS it allows. admin adds the instance lifecycle —
# instances.list, create, delete, disable, reenable, undelete — on top of the
# two the workspace's own service uses; nothing in this design creates, deletes
# or disables a database, and the identity that holds the workspace's record and
# its admitted-token hashes must not be the one that can destroy the database
# they live in.
#
# get is the read (the record at workspaces/<name>, the admitted-token hashes,
# the member ledgers the alarm compares) and update the write, because a
# Realtime Database node has no separate create or delete and the meter is one
# transaction that reserves the room and one that settles what the call cost
# (W7). The same pair is what the credentials service's database role holds
# (deploy/terraform/gcp/modules/installation/credentials.tf), which is the
# reviewed precedent for it. A third permission is a decision a person makes
# here AND in internal/infra/tf/workspace.go together: the plan cover pins this
# id and these two permissions (WorkspaceNotCovered).
resource "google_project_iam_custom_role" "workspace_database" {
  project     = var.project
  role_id     = "fugaroWorkspaceDatabase"
  title       = "Fugaro workspace service (database, data only)"
  description = "The workspace ${var.workspace}'s own service's reach over the workspace's own database: the instance's two data permissions, and no lifecycle permission."
  permissions = [
    "firebasedatabase.instances.get",
    "firebasedatabase.instances.update",
  ]
}

# The one grant: a member resource and never a binding, so nothing here takes
# access away from anyone else.
#
# The role is spelled as the literal path projects/<project>/roles/<id> and not
# as the role resource's own name attribute, because a grant whose role is
# computed cannot be read out of a plan — internal/infra/tf's cover reads this
# one, and a role it cannot read is a role it cannot hold to the id above. The
# member is google_service_account.workspace.member, which IS the
# serviceAccount:<address> string IAM wants, rather than a hand-built one that
# could drift from it.
#
# It is bound to THIS project's account and to no other member: the workspace's
# own service is the only identity that reaches the workspace's own database,
# and a plan granting the role to anything else is refused by internal/infra/tf
# (WorkspaceNotCovered), which is where the decision is enforced rather than
# here.
resource "google_project_iam_member" "workspace_database" {
  project    = var.project
  role       = "projects/${var.project}/roles/fugaroWorkspaceDatabase"
  member     = google_service_account.workspace.member
  depends_on = [google_project_iam_custom_role.workspace_database]
}

# The SA fallback's identity (D2803, WU3): the account a member's owner grants
# roles/owner on their project — and on their Firebase project when it is a
# separate one — when there is no organization to move the project into, and
# which the workspace's admins only ever IMPERSONATE (WU20).
#
# Two accounts, never one (F20). This one holds roles/owner on every linked
# member; the account above reads the workspace's own database and the members'
# ledgers. Neither is the credentials service's account, which holds no write on
# a member at all (D1299).
#
# The root creates it and grants it NOTHING. Its token creators are the
# workspace admins, written live by `workspace user add --admin` (D2818), so a
# grant of this account in a plan would be a role no `fugaro update` asked for —
# and internal/infra/tf's WorkspaceNotCovered refuses any grant of this root
# whose member is not the service account above.
#
# NO KEY IS WRITTEN HERE, and this comment is what "no key" means: the root
# writes none, which is not the claim that none can exist. The account lives in
# a project Fugaro does not own alone, and two paths make one anyway —
# iam.serviceAccounts.actAs, which roles/editor and roles/iam.serviceAccountUser
# carry, and iam.serviceAccountKeys.create, which roles/editor resolves to. The
# organization policy constraints/iam.disableServiceAccountKeyCreation is the
# only control that closes the second; WU11's `status` takes up both. The tftest
# case beside this root says the same, and
# TestWorkspaceRootMakesNoKeyForTheIAMAccount reads this file for the first half.
#
# The id is a literal rather than a var.names field, as the role's is, so the
# plan carries the name internal/infra/tf pins (WorkspaceIAMAccountID) and no
# value could differ from it.
resource "google_service_account" "workspace_iam" {
  project      = var.project
  account_id   = "fugaro-workspace-iam"
  display_name = "Fugaro workspace IAM"
  description  = "The workspace ${var.workspace}'s IAM account: the identity a member's owner grants roles/owner on their project when the workspace has no folder (D2803). The workspace's admins impersonate it, and Terraform writes no key for it."
}
