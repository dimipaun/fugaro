# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted. Run job accounts get nothing in
# the FP: a run holds a Firebase ID token, which isn't an IAM identity. The one
# account granted here that is a job's is the coordinator's writer, below.

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

# ... and read the spend history in Firestore (fugaro report). Viewer only:
# nobody but the history account writes it, and it writes through
# datastore.user, below. Launchers, operators and admins; no domain or
# wildcard member can get here (the variables' validation).
resource "google_project_iam_member" "datastore_viewer" {
  for_each = setunion(local.people, local.admins)

  project = var.project
  role    = "roles/datastore.viewer"
  member  = each.value
}

# The history job's account: its sweep edits the registry and the run
# ledgers, and deletes the Auth users of expired runs. It is the most
# privileged new identity (firebasedatabase.admin could lift caps; its code
# never writes /config, which IAM can't enforce). Trivy flags a privileged
# role on a service account: accepted by design §11. Two accounts hold
# firebasedatabase.admin on the FP, this one and the coordinator's writer
# below; no other does.
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

# The rollover writes the spend history: documents only (datastore.user can't
# create or delete a database or change indexes; never datastore.owner).
resource "google_project_iam_member" "history_firestore" {
  project = var.project
  role    = "roles/datastore.user"
  member  = "serviceAccount:${var.history_account}"
}

# The Firestore client sends X-Goog-User-Project: <this project> (user
# credentials need a quota project, and a service account's own project does
# not have the Firestore API enabled), which requires
# serviceusage.services.use on it. serviceUsageConsumer is that one
# permission set, held by the history account and by the people who get
# datastore.viewer above, on this project only; no domain or wildcard member
# can get here (the variables' validation).
resource "google_project_iam_member" "usage_consumer" {
  for_each = setunion(local.people, local.admins)

  project = var.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = each.value
}

resource "google_project_iam_member" "history_usage" {
  project = var.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "serviceAccount:${var.history_account}"
}

# The coordinator's writer: the account the aggregation job runs as, and the
# only identity that writes the coordinator's state (D194,
# docs/coordinator/implementation-plan.md P3). It holds
# roles/firebasedatabase.admin beside the history account's above, for the
# reason that account holds it: the coordinator's records are written under
# the rules' floor, and the rules grant the coordinator's subtree to the
# signed-in reader alone. The role bypasses the rules, so the writer's code is
# what keeps it to that subtree — IAM cannot, and this account can write the
# whole database, /config included. The installation root creates the account
# in P5, as it creates the history account's; IAM refuses a member that does
# not exist, so until then coordinator_account is empty and nothing is granted.
#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "coordinator_database" {
  count   = var.coordinator_account == "" ? 0 : 1
  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = "serviceAccount:${var.coordinator_account}"
}
