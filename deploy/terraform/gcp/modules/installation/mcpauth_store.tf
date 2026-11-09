# The MCP front door's own store (D935): the records a token is checked
# against — what was issued, what was revoked — live in a bucket of the
# installation's own and in nothing else. It is a file of its own for one
# reason: the front door's account may be named by exactly one grant in
# coordinator.tf (the runs bucket's read, which the test beside this file
# counts), and the account's file names the store only as the thing M10's
# tools reach, never as a grant it makes.
#
# The store is created with the front door's own opt-in (local.mcp_server_on)
# and with nothing else: an installation that never asked for a front door
# plans exactly what it planned before, which is every installation by default
# (D934). The whole of the store's IAM is the one binding at the bottom of this
# file: the front door's own account and no other member, ever.
#
# The name is a function of the GCP project alone, the shape the runs bucket
# and both state buckets have, and it deliberately carries no managed prefix:
# the managed deployer's storage role is a project-level binding conditioned on
# `fmgd-` (deployer.tf), so a store named under it would sit inside the reach
# the store exists to leave.

locals {
  # fugaro-mcpauth-<gcp-project>, derived and never a variable: the name is
  # predictable by design (the installation's own outputs publish it, and M2
  # opens the store by that name), and a name a caller could choose is a name
  # that could be pointed at another installation's records.
  mcpauth_store_bucket = "fugaro-mcpauth-${var.project}"
}

# The store itself. It holds token records and revocation state, so it is never
# destroyed and never versioned — the same floor the runs bucket has — and the
# two settings the installer's plan review refuses a bucket without are set
# explicitly: uniform bucket-level access, and public access prevention
# enforced, so no ACL and no public grant can exist beside the one binding
# below (internal/infra/tf/cover.go).
resource "google_storage_bucket" "mcpauth_store" {
  count = local.mcp_server_on ? 1 : 0

  project  = var.project
  name     = local.mcpauth_store_bucket
  location = upper(var.region)

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = { fugaro = "managed", fugaro_project = var.fugaro_project }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The store's one member (D935): the front door's own account, and objectAdmin
# rather than a narrower role because the records are written, read and deleted
# by the process that serves the endpoint — revocation is a delete, and the
# tools that answer about a token need to read the object it names. It is
# granted on this bucket alone and to this account alone, with no condition
# (unverified assumption A3): the store's whole content is the front door's
# state, and an identity that does not serve the front door has no reason to
# reach it.
#
# It is NOT the coordinator's own account. The sweep reads runs and never
# tokens (P5), and its account holds the GitHub App's key and the FP's database
# admin; naming it here would put the token records behind an identity that
# reads every run in the project, which is the reach D935 exists to separate.
resource "google_storage_bucket_iam_member" "mcpauth_store" {
  count = local.mcp_server_on ? 1 : 0

  bucket = google_storage_bucket.mcpauth_store[0].name
  role   = "roles/storage.objectAdmin"
  member = google_service_account.mcp_server[0].member
}
