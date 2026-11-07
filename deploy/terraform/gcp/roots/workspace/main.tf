# The workspace root: the workspace's OWN service in the workspace's own
# project (docs/workspaces/implementation-plan.md W3, spec.md §2, §5, §9).
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
# So this root creates the account and the ONE grant that reaches the database,
# and nothing else. That "nothing else" is the security property, not a
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

# The one grant, and its own comment says why it is the whole-database role: a
# Realtime Database IAM grant has no path-level scope and no condition can fence
# one (spec §8, deploy/terraform/gcp/modules/installation/credentials.tf), so
# there is no narrower grant of the read this account needs — and the write it
# also needs, because the meter is one transaction that reserves the room and
# one that settles what the call cost (W7). It is a member resource and never a
# binding, so nothing here takes access away from anyone else.
#
# The member is spelled serviceAccount:<address> and not the bare address: the
# address is computed by the account this root creates, and the prefix is what
# says which kind of principal it is when terraform knows it at plan time.
#
# It is bound to THIS project's account and to no other member: the workspace's
# own service is the only identity that reaches the workspace's own database,
# and a plan granting the role to anything else is refused by internal/infra/tf
# (WorkspaceNotCovered), which is where the decision is enforced rather than
# here.
#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "workspace_database" {
  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = "serviceAccount:${google_service_account.workspace.email}"
}
