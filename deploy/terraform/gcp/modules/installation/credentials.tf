# The credentials service (design docs/credentials-service, D146, D147): the
# one identity that holds the project's model credentials, and the cascade
# every review's calls go through. It is opt-in per installation
# (credentials_service, default off) and it is Fugaro's own infrastructure,
# in the installation root: `fugaro managed apply` never deploys it (D146,
# Global Constraint 7). With the opt-in off this file creates nothing and
# every repository's job keeps the mounts it has today.
#
# What it is not: it holds no bucket access, so it cannot read a run's files;
# and nobody is granted actAs on its account but the operators, who need it to
# apply the service.
#
# KNOWN GAP, and a blocker for turning the opt-in on: the state the cascade
# shares (exhausted and broken marks, locks, per-run leases) lives in RTDB
# under creds/ (D136, spec.md §"State"), and the account that reads and writes
# it needs firebasedatabase.admin on the Firebase project — the same role the
# history account holds (modules/firebase/iam.tf's history_database, the only
# place that role is granted today). This file does NOT grant it and cannot:
# the Firebase project is the Firebase root's, a different root with a
# different state, and in the split layout it is not even this project. The
# grant belongs there, beside the history account's, and until it lands an
# installation that turns the opt-in on gets a service that cannot reach the
# state it exists to share. The PR records this.

locals {
  credentials = var.credentials_service
}

resource "google_service_account" "credentials" {
  count = local.credentials == null ? 0 : 1

  project      = var.project
  account_id   = local.credentials.account_id
  display_name = "Fugaro credentials service"

  depends_on = [google_project_service.this]
}

# Deploying or updating the service acts as its account, so the operators
# hold actAs on it and nobody else does: not the launchers, who never apply
# the installation, and no repository's job account (Check 94 asserts it).
#
# The account is a literal path built from var.project and the opt-in's own
# account ID, not the account resource's name attribute: that attribute is
# unknown at plan time (the account is created by this same apply), and a
# plan whose service-account grant does not say which account it is on is one
# init's cover cannot check — onDeployer (internal/infra/tf) refuses it in a
# module that creates the deployer account, which this one does, so the apply
# would ask its own confirmation for the module's own grant. The ID is
# validated as a service account ID by the variable, so the literal cannot
# name anything but the account above.
resource "google_service_account_iam_member" "credentials_operator" {
  for_each = toset(local.credentials == null ? [] : var.operators)

  service_account_id = "projects/${var.project}/serviceAccounts/${var.credentials_service.account_id}@${var.project}.iam.gserviceaccount.com"
  role               = "roles/iam.serviceAccountUser"
  member             = each.value

  depends_on = [google_service_account.credentials]
}

# The model secrets, and only those: one grant per secret the opt-in names,
# at the secret level, never the project's. They are the logical names of
# the project's own containers, which the installation module holds as IDs
# (project_secrets); a name with no container there is refused by the
# variable's own validation, because a grant on a secret that does not exist
# fails the apply. This is the grant that moves: modules/workflow/sa.tf drops
# exactly these secrets from every job account's accessor when the opt-in is
# on, so from then on the service is the only Fugaro identity that can read
# a model credential (Check 94).
#
# secret_id is read off the container resource rather than off the input map:
# each.value is a logical name, which is a key of
# google_secret_manager_secret.project, and reading the ID through that
# resource is what makes this grant wait for the container to exist. An ID
# taken straight from var.project_secrets (a literal, from
# terraform.tfvars.json) carries no such edge, and on a fresh installation
# this grant could be created before the container it names and fail with a
# 404 — the ordering the module's own grants keep (secrets.tf's operator
# grant, and outputs.tf's project_secrets).
resource "google_secret_manager_secret_iam_member" "credentials" {
  for_each = toset(local.credentials == null ? [] : local.credentials.secrets)

  project   = var.project
  secret_id = google_secret_manager_secret.project[each.value].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.credentials[0].member
}

# The service. Ingress with the IAM check on (invoker_iam_disabled false) is
# its whole door: the only way in is a run.invoker grant, and the only ones
# modules/workflow/sa.tf adds are the job accounts of this installation's
# repositories, one each (D135). A job therefore reaches the cascade with its
# own identity and holds no model credential; and because the service is the
# single point, an unreachable service halts a run rather than falling back
# to a key in the job (D143).
#
# min_instance_count 0 with request-based billing: nothing runs, and
# nothing is billed, between reviews. timeout 3600s (Cloud Run's maximum)
# because the service forwards streaming calls that run for the length of a
# review, far past the 300s default; Check 91 measured what the front end
# does with a long stream. The container port is the one Cloud Run injects
# as PORT (the default, written out so the Go service and this file cannot
# drift), and the environment is the second generation: a first-generation
# instance's network is not the one a proxy needs.
resource "google_cloud_run_v2_service" "credentials" {
  count = local.credentials == null ? 0 : 1

  project  = var.project
  location = var.region
  name     = local.credentials.name
  ingress  = "INGRESS_TRAFFIC_ALL"
  labels = {
    fugaro      = "managed"
    fugaro_role = "credentials"
  }
  deletion_protection  = true
  invoker_iam_disabled = false

  template {
    service_account       = google_service_account.credentials[0].email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"
    timeout               = "3600s"

    scaling {
      min_instance_count = 0
    }

    containers {
      image = local.credentials.image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # No credential in the environment: the service's identity is its
      # account, and the secrets it reads it reads from Secret Manager as
      # that account. What it cannot work out for itself is its own project
      # (the fp claim of every run identity it verifies, and the project its
      # secrets live in), the Fugaro project's name (the same claim, which
      # the rules hold against /fugaro/project) and the database the
      # cascade's state lives in (D136).
      env {
        name  = "FUGARO_PROJECT"
        value = var.fugaro_project
      }
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      env {
        name  = "FUGARO_RTDB_URL"
        value = local.credentials.rtdb_url
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.credentials]
}

# No project-level role is granted to the service's account, on purpose: it
# runs no Cloud Run job and reads no registry, so nothing of fugaroLauncher
# belongs on it. The one role it does need, firebasedatabase.admin, is not
# here either — see the note at the top of this file: it is the Firebase
# root's to grant, it is not granted there yet, and an installation that
# turns the opt-in on before it lands gets a service that cannot reach the
# state it exists to share.
