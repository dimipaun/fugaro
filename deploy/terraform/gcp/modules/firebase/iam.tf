# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted. Job accounts get nothing in
# the FP: a run holds a Firebase ID token, which isn't an IAM identity.

# The key that signs the runs' custom tokens. It is granted NO roles, in the
# FP or anywhere: signing goes through IAM signJwt as this account, and
# nothing on this account's own identity is ever used.
resource "google_service_account" "signer" {
  project      = var.project
  account_id   = var.names.signer_account_id
  display_name = "Fugaro token signer"
  description  = "Signs the custom tokens of the Fugaro project ${var.fugaro_project}'s runs. Holds no roles."

  depends_on = [google_project_service.this]
}

# signJwt and nothing else: narrower than serviceAccountTokenCreator, which
# would also let its holder mint access tokens as the signer.
resource "google_project_iam_custom_role" "token_minter" {
  project     = var.project
  role_id     = var.names.minter_role_id
  title       = "Fugaro token minter"
  permissions = ["iam.serviceAccounts.signJwt"]

  depends_on = [google_project_service.this]
}

locals {
  people = toset(concat(var.launchers, var.operators))
  admins = toset(concat(var.admins, var.budget_admins))
}

# Launchers and operators mint tokens, granted on the signer account alone.
resource "google_service_account_iam_member" "minter" {
  for_each = local.people

  service_account_id = google_service_account.signer.name
  role               = google_project_iam_custom_role.token_minter.name
  member             = each.value
}

# ... and read the database (fugaro watch, budget show).
resource "google_project_iam_member" "viewer" {
  for_each = local.people

  project = var.project
  role    = "roles/firebasedatabase.viewer"
  member  = each.value
}

# Budget admins (D6) write /config over IAM, which bypasses the rules.
resource "google_project_iam_member" "admin" {
  for_each = local.admins

  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = each.value
}

# The history job's account: its sweep edits the registry and the run
# ledgers, and deletes the Auth users of expired runs. It is the most
# privileged new identity (firebasedatabase.admin could lift caps; its code
# never writes /config, which IAM can't enforce). Trivy flags a privileged
# role on a service account: accepted by design §11, and only this account
# (and no job account) holds one on the FP.
#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "history_database" {
  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = "serviceAccount:${var.history_account}"
}

#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "history_auth" {
  project = var.project
  role    = "roles/firebaseauth.admin"
  member  = "serviceAccount:${var.history_account}"
}
