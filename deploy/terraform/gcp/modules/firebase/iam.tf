# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted. Run job accounts get nothing in
# the FP: a run holds a Firebase ID token, which isn't an IAM identity. The one
# job's account that can be granted here is the coordinator's writer, below.

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

# The devices' own signer (mobile plan task P5; D2782 as amended by F31, O8),
# and the reason there are TWO signers in this project rather than one.
#
# Firebase accepts a custom token only from a service account OF the Firebase
# project: a token signed by an account of the installation's own project is
# refused by the Auth API. An installation can span two projects — the
# fixture's aurora-fp beside proj-1234 is exactly that shape, and D2781's
# dedicated identity project makes it the common one — so a devices' signer
# that was the signer above would simply not work. The runs' signer keeps its
# own job (the budget's and the resident orchestrator's rows,
# modules/installation/orchestrator.tf).
#
# What the second account buys is a key that can be disabled alone and an audit
# trail of its own, and NO narrowing of what may be signed: signJwt signs ANY
# claim set for ANY uid, and IAM cannot express "device claims only". The
# control is the mobile service's own fail-closed route allowlist — no route
# takes a caller-chosen uid or claim — together with the tests over it, and
# SECURITY.md states the residual risk (O8, D1). This file says it here because
# this is where a reader meets the account.
#
# It is granted NO roles, in the FP or anywhere, like the signer above: signing
# goes through IAM signJwt AS this account, and nothing on this account's own
# identity is ever used. Its one grant is the public mobile service's, written
# in this file with it (mobile_token_minter below) rather than in the
# installation root that creates the service: the account is created HERE, and a
# grant in a root applied before this one would name an account that does not
# exist yet (the ordering mobile_token_minter states).
#
# It is created exactly when the installation hands over an account to sign FOR
# (var.mobile_account, empty until the credentials image runs the mode the mobile
# service is started in): a signer no service holds signJwt on is a key nothing
# uses, and an installation whose mobile service is off plans neither it nor the
# grant — which is what makes the first update carrying this change a plan with
# nothing new in it (D2782, F31).
resource "google_service_account" "device_signer" {
  count = var.mobile_account == "" ? 0 : 1

  project      = var.project
  account_id   = "fugaro-device-signer"
  display_name = "Fugaro device token signer"
  description  = "Signs the custom tokens of the Fugaro project ${var.fugaro_project}'s paired devices. Holds no roles; the installation's public mobile service holds signJwt on it."

  depends_on = [google_project_service.this]
}

# The public mobile service's runtime account, on the device-only signer and on
# nothing else (mobile plan task P5; O1, O8). It is orchestrator.tf's cross-root
# row one account over — the row is bound on the signer ACCOUNT rather than on a
# project, which is what keeps it signJwt and not
# roles/iam.serviceAccountTokenCreator, whose holder could also mint access
# tokens as the signer — and it is HERE, beside the account it signs as, because
# of the order `fugaro update` applies the two roots in
# (internal/cli/init_stages.go: preflight, installation, Firebase, images, ...):
# the installation root creates the mobile service and its account, the Firebase
# stage creates this signer, and a grant written in the installation root would
# name an account this one had not created yet on the first update that carried
# it — a 404 in the middle of an apply, on an installation whose runs are doing
# nothing wrong. Written here, the plan that first carries it is also the plan
# that creates what it is on.
#
# THE SENTENCE THIS ROW MUST CARRY (the task's acceptance). iam.serviceAccounts.
# signJwt cannot be limited to a claim set: whoever holds it on an account can
# sign ANY claims for ANY uid as that account, which is why orchestrator.tf
# calls it the widest grant in the design's table. What holds this account to
# device tokens is therefore NOT this grant: it is the route allowlist the
# process mounts in its mobile mode — no route takes a caller-chosen uid or
# claim — and the tests over it (I1's cross-class test among them). The
# separation this account buys is a key that can be disabled alone and an audit
# trail of its own. SECURITY.md states the residual risk (O8, D1).
#
# It is granted on the DEVICE signer and not the runs' own: signJwt on
# google_service_account.signer would make the public service the identity that
# mints every run's and every launcher's token, which is a reach O1 did not
# announce and no reviewer priced.
resource "google_service_account_iam_member" "mobile_token_minter" {
  count = var.mobile_account == "" ? 0 : 1

  # The account and the role are written out as literals of THIS file rather
  # than as references to the two resources above, and that is the one thing
  # about this row a reader has to know: `google_service_account.device_signer[0]
  # .name` and `google_project_iam_custom_role.token_minter.name` are COMPUTED,
  # so the plan would carry an unknown string — a principal and a role nobody
  # can read off the review screen, which is what internal/infra/tf's cover
  # refuses a grant for (mobileMinterGrant: the account has to be nameable).
  # The three strings are the two resources' own facts, held together by
  # deploy/terraform/terraform_test.go (TestDeviceSignerIsOneSpelling), which is
  # what a literal costs and what keeps the plan readable.
  #
  # The role's ID is a literal for a second reason, which is this tree's rule
  # for every role that is not `roles/...` (TestIAMResourcesUseKnownExpressions):
  # an interpolated one is a role no review screen can read, so the ONE
  # cross-root row of the same kind — the installation root's signJwt row on the
  # runs' signer (orchestrator.tf, section 4.3) — spells the same ID the same
  # way, and the test above holds this literal to the constant internal/infra
  # writes into the root's tfvars (MinterRoleID, the module's own
  # names.minter_role_id). What declares the role is role_id below, from that
  # variable: one role, one literal that names it, and the two are pinned
  # together rather than derived from one another.
  service_account_id = "projects/${var.project}/serviceAccounts/fugaro-device-signer@${var.project}.iam.gserviceaccount.com"
  role               = "projects/${var.project}/roles/fugaroTokenMinter"
  member             = "serviceAccount:${var.mobile_account}"

  depends_on = [google_service_account.device_signer, google_project_iam_custom_role.token_minter]
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
  # Everyone who reads the database and the spend history: the launchers, the
  # operators, the admins (who read both through datastore_viewer) and the
  # viewers. It is a local of its own and not `people` widened, because the
  # minter grant below is exactly `people`: a viewer reads the database and
  # holds no way to mint an identity (roles.md §4).
  readers = setunion(local.people, toset(var.viewers))
}

# Launchers and operators mint tokens, granted on the signer account alone.
# The viewers are deliberately not among them: this is the grant that would
# let a viewer mint the identity of anyone in the project, which is F44's
# problem and the reason roles.md §4 forbids it.
#
# It is behind var.people_minter (launcher-does-not-mint plan task LM11,
# D3121), and the switch is ON: the row is exactly what this module has always
# planned, so an installation taking this change plans nothing new. What turns
# it off is `fugaro update`, in the one plan that removes the row once the
# credentials service has proved it signs as this signer (LM14, R8) — the
# removes-the-row half of ask 1, which takes the way to mint an identity off
# every launcher and operator.
resource "google_service_account_iam_member" "minter" {
  for_each = var.people_minter ? local.people : toset([])

  service_account_id = google_service_account.signer.name
  role               = google_project_iam_custom_role.token_minter.name
  member             = each.value
}

# The issuer's own row (launcher-does-not-mint plan task LM11; D3121, F7, F8):
# the credentials service's account, which mints a run's identity once the
# people's row above is gone. It is the ONE row this design adds, and the row
# that makes a launcher's own credentials unable to produce a run's token: no
# launcher, operator or group holds it, and a launch asks the service instead
# (internal/credsvc's launcher class). What it costs is stated where it is
# decided and not softened here: iam.serviceAccounts.signJwt signs ANY claim
# set for ANY uid as the account it is on, so the service becomes the identity
# that mints every run's and every launcher's token — the honest size of the
# amendment to `.claude/rules/credentials-service.md`'s sentence on this
# account's grants (F7, F8), and a single point of every launch.
#
# Why the account and the role are written out rather than read off the two
# resources beside them. `google_service_account.signer.name` and
# `google_project_iam_custom_role.token_minter.name` are COMPUTED, so a grant
# written with them reaches a plan as an unknown string: a principal and a
# role no review screen can read. iam.serviceAccounts.signJwt on an account IS
# that account (orchestrator.tf calls it the widest grant in the design's
# table), so that is the one row of this file whose signer may never be an
# unknown. The refusal that shape needs is not in the tree yet: the cover's
# fugaroTokenMinter cell admits people, the resident orchestrator and the
# mobile service, and `issuerMinterGrant` — the rule beside `mobileMinterGrant`,
# which holds the devices' row to this run's own signer — is the one LM13 adds
# to name the account THIS row may be on. Until it lands, this row is one the
# tables warn about and apply (D1545: a tripwire, not a fence), and what holds
# its spelling to the signer and the role it stands for is deploy/terraform's
# TestIssuerMinterIsOneSpelling. The account is spelled the way this module
# creates it (google_service_account.signer's account_id,
# var.names.signer_account_id) and the role is the ID this file declares
# (role_id, var.names.minter_role_id): a literal of the plan, held to both
# resources by that test, which is what a literal costs and what keeps the plan
# readable.
#
# It is created exactly when an account is named (var.issuer_account, empty
# until the update that passes the credentials service's own account): a row
# naming nobody is a row IAM refuses, and an installation whose issuer is not
# named yet is one whose runs are still minted the old way.
resource "google_service_account_iam_member" "issuer_minter" {
  count = var.issuer_account == "" ? 0 : 1

  service_account_id = "projects/${var.project}/serviceAccounts/${var.names.signer_account_id}@${var.project}.iam.gserviceaccount.com"
  role               = "projects/${var.project}/roles/fugaroTokenMinter"
  member             = "serviceAccount:${var.issuer_account}"

  depends_on = [google_service_account.signer, google_project_iam_custom_role.token_minter]
}

# ... and read the database (fugaro watch, budget show).
resource "google_project_iam_member" "viewer" {
  for_each = local.readers

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
# datastore.user, below. Launchers, operators, admins and viewers; no domain
# or wildcard member can get here (the variables' validation).
resource "google_project_iam_member" "datastore_viewer" {
  for_each = setunion(local.readers, local.admins)

  project = var.project
  role    = "roles/datastore.viewer"
  member  = each.value
}

# The history job's account: its sweep edits the registry and the run
# ledgers, and deletes the Auth users of expired runs. It is the most
# privileged new identity (firebasedatabase.admin could lift caps; its code
# never writes /config, which IAM can't enforce). Trivy flags a privileged
# role on a service account: accepted by design §11. No other service
# account holds firebasedatabase.admin on the FP except the coordinator's
# writer below, once coordinator_account is set (coordinator plan P5).
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
# permission set, held by the history account and by every principal
# datastore_viewer above names, viewers included: the spend history is one of
# the reads a viewer is for (roles.md §4), and a viewer without it is a
# datastore.viewer every Firestore call refuses. The two sets are one set by
# construction and the Firebase root test asserts they stay equal; no domain
# or wildcard member can get here (the variables' validation).
resource "google_project_iam_member" "usage_consumer" {
  for_each = setunion(local.readers, local.admins)

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
# (P5, modules/installation/coordinator.tf) and hands it here as
# coordinator_account, so this grant exists exactly when the account does: an
# installation whose owner has not opted in to the coordinator (fugaro update
# --firebase-project <id> --coordinator), which is every installation by
# default, passes an empty account and nothing is granted.
#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "coordinator_database" {
  count   = var.coordinator_account == "" ? 0 : 1
  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = "serviceAccount:${var.coordinator_account}"
}
