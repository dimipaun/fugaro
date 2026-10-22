# The credentials service (D146): the one place a model credential lives once
# an installation opts in. It is Fugaro's own infrastructure, in the
# installation root — `fugaro managed apply` never deploys it (Global
# Constraint 7) — and it is OFF by default: with var.credentials_service null
# this file creates nothing, and every job keeps the model-secret accessors
# it has today (D147).
#
# Q6 (the owner's ruling, 2026-10-19). The service's reach in Secret Manager
# is ONE secret container and ONE accessor grant on it (Global Constraint 12):
# every credential it needs is one YAML document, read in one go by the loader
# (C1.11), and there is no per-credential grant and no `condition` over the
# model secrets — no secretmanager.googleapis.com/secretId filter, no
# name-prefix fence. TestCredentialsServiceGrantsAreTheOneBundle below is what
# keeps that true.

locals {
  # The whole file is gated on the one opt-in: null creates nothing, and a
  # single boolean keeps the four resources of this file in step (the account,
  # the bundle container, the accessor grant on it, and the service —
  # TestCredentialsServiceResourcesAreCounted is what holds this number and the
  # file's own resources together, in both directions).
  credentials_service_on = var.credentials_service != null

  # The audience for the first apply of an installation whose tfvars carry no
  # credentials_service.url yet (C1.14's `fugaro credentials serve` refuses to
  # bind a port, by name, without one).
  #
  # Why a placeholder rather than an empty value or a guess. Cloud Run's front
  # end checks a caller's Google ID token against the service's OWN URL, so the
  # audience has to be that URL and cannot be a constant, and nothing in this
  # file may read google_cloud_run_v2_service.credentials[0].uri from inside
  # that resource (Terraform refuses a self-referential block). The URL also
  # carries the project NUMBER, which this module never learns offline. An
  # empty value is not the answer: the revision would refuse to serve, the
  # container would exit, and the create this plan is making would have to
  # finish before the owner could read the URL it needs — a deadlock, whose
  # re-run would then plan "no changes" and report an installation whose
  # service never answered. So the first revision starts and is healthy with an
  # audience no ID token can carry: this host is under the reserved .invalid
  # TLD (RFC 2606), it is not a run.app URL, and nothing is ever minted for it.
  # The service admits no caller until the owner-run init stage reads the real
  # URL back from this module's own output (outputs.tf) and applies again —
  # inside the same stage, so an installation init reports is one that serves.
  credentials_service_url_unassigned = "https://credentials-service-url-unassigned.invalid"
}

# The service's own account. It reads the bundle and nothing else: no project
# role, no bucket, and — the rule history.tf states — nobody is granted actAs
# on it, operators included, because anyone who could deploy a revision as it
# could read every model credential the installation holds.
resource "google_service_account" "credentials" {
  count = local.credentials_service_on ? 1 : 0

  project      = var.project
  account_id   = var.credentials_service.account_id
  display_name = "Fugaro credentials service"

  lifecycle {
    precondition {
      condition     = var.credentials_service != null
      error_message = "the credentials service's account needs var.credentials_service"
    }
  }

  depends_on = [google_project_service.this]
}

# The one container, and the only Secret Manager secret this service ever
# reads. Terraform creates the container and never a version: the document is
# written by the owner-run `fugaro creds set` (C1.25) with the owner's own
# identity, so no credential value ever reaches Terraform or its state
# (TestNoSecretVersionsInTerraform). A container with no document yet is fine:
# the loader fails closed on a missing or unparseable bundle (C1.11), so the
# service answers a named refusal rather than an empty credential set.
#
# The labels are `fugaro=managed` and the installation's name, and
# deliberately NOT fugaro_secret: that label is what `fugaro init`'s discovery
# and `fugaro secrets ls --project-wide` read to decide which *logical secret*
# of the project's secret set a container holds (internal/infra/discover.go's
# liveProjectSecrets, internal/cli/doctor.go), and this container is not one of
# those — it is one document with one writer. Carrying it would make discovery
# union the container into repo.project_secrets beside this resource, and the
# apply would then plan the same secret ID twice. The two ownership marks are
# what make it recognizably this installation's.
#
# prevent_destroy is secrets.tf's rule for the same reason: a config edit that
# drops the container must be a hard plan failure, not a deletion of every
# credential in it.
resource "google_secret_manager_secret" "credentials_bundle" {
  count = local.credentials_service_on ? 1 : 0

  project   = var.project
  secret_id = var.credentials_service.bundle_secret
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
  }

  replication {
    auto {}
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.this]
}

# The ONE grant (Q6): roles/secretmanager.secretAccessor, to the service's own
# account, on that one container. No condition block at all — a condition here
# would be the name-prefix fence the ruling replaced — and no second member
# anywhere. The member is the account this module creates, which cover.go's
# grant rules already bound.
resource "google_secret_manager_secret_iam_member" "credentials_bundle" {
  count = local.credentials_service_on ? 1 : 0

  project   = var.project
  secret_id = google_secret_manager_secret.credentials_bundle[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.credentials[0].member
}

# The service itself. It requires IAM: nothing here grants roles/run.invoker to
# allUsers or allAuthenticatedUsers, and invoker_iam_disabled is set to false
# explicitly rather than left to the provider's default, so the front end
# refuses an unauthenticated request before the container runs and a later
# change of that default cannot open the service silently. The ingress is ALL
# because the caller is a Cloud Run *job* of this project, which reaches the
# service over its public run.app URL (a job is not on a VPC network); the
# fence is the IAM check, not the network — the only member is each job
# account's own invoker grant (modules/workflow/sa.tf). INGRESS_TRAFFIC_INTERNAL_ONLY
# would refuse every caller there is today (independent security review of this
# file, n3, which agreed with ALL): revisit it the day a job reaches the
# service over Direct VPC egress instead, because then the network is a fence
# that costs nothing.
#
# The image is a variable (C1.20's init stage fills it with the released
# image's reference, as it fills var.history.image) and it is pinned BY DIGEST:
# var.credentials_service's validation refuses a tag. That is a security rule,
# not a preference — the registry grants roles/artifactregistry.writer to every
# operator (iam.tf), so a mutable tag would let an operator push the code this
# account runs, and this account reads every credential in the bundle
# (independent security review of this file, M1). No command or args are set:
# the image's own entrypoint is the service (C1.14). The timeout is the
# longest Cloud Run allows, because a proxied streaming call is long-lived
# (Check 91 measures what a Cloud Run front end does with one).
resource "google_cloud_run_v2_service" "credentials" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.credentials_service.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "credentials"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  # IAM is required, stated rather than implied: this is the fence every
  # caller passes through, and the service's own ID-token check (Q6) is
  # layered on top of it, never instead of it.
  invoker_iam_disabled = false

  # The service holds no data of its own: what is worth keeping is the bundle
  # container, and that one carries prevent_destroy above. So dropping the
  # opt-in is NOT a removal a plan may make: with the container in state the
  # plan fails ("Instance cannot be destroyed") and applies nothing — the
  # service, this account and the grant above cannot be removed in that plan
  # either (independent security review of this file, M2, whose first version
  # of this comment claimed the opposite). Turning the service off is two
  # owner steps, in this order, and C1.22's runbook carries them — together
  # with a third that this file cannot do for itself: every repository root is
  # re-applied too, and before these two steps. The `run.invoker` grants and
  # the model-secret accessors this service displaced live in the REPOSITORY
  # roots, so a repository root that is not re-applied keeps declaring a
  # service that step 2 is about to remove, and its own apply then plans an
  # invoker grant on a resource that no longer exists (independent security
  # review of this file, re-review N2):
  #
  #   1. terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'
  #      — forgets the container. It deletes nothing in GCP and leaves every
  #      credential in it alone. The address is the installation ROOT's: this
  #      resource is inside `module "installation"`, so the prefix is not
  #      optional (the bare address names nothing in that state and
  #      `terraform state rm` refuses it), and the single quotes are: `[0]` is
  #      a glob to zsh, which would refuse the command before terraform ran
  #      (independent security review of this file, re-review N1).
  #   2. an ordinary plan and apply, with credentials_service unset, which
  #      removes this service, its account and the grant, and touches no
  #      secret.
  #
  # deletion_protection stays false so step 2 can do its work; the container's
  # prevent_destroy, not this flag, is what keeps a credential from being
  # deleted by a plan.
  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "credentials"
    }
    service_account = google_service_account.credentials[0].email
    timeout         = "3600s"

    containers {
      image = var.credentials_service.image

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The service's whole configuration (C1.14's `fugaro credentials serve`
      # reads exactly these seven names, and internal/infra/tf's
      # credentialsServiceEnv is the allow-list that refuses an eighth at plan
      # time). No credential is among them: it reads the one bundle secret as
      # its own account, by the name it derives from FUGARO_PROJECT (the
      # project-wide container of the logical name "creds-bundle",
      # internal/backend/gcp's ProjectSecretID — the same value
      # var.credentials_service.bundle_secret carries, which is what the
      # container above is created with), so no secret ID and no secret value
      # is ever an environment variable here.
      env {
        name  = "FUGARO_PROJECT"
        value = var.fugaro_project
      }
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      env {
        name  = "FUGARO_REGION"
        value = var.region
      }
      # The installation's database and the Firebase project a run's identity
      # is verified against: the history job's own two names (history.tf),
      # taken from this variable rather than from var.history so an
      # installation that runs this service without a budget job can still
      # state them (the database belongs to the Firebase root, not to the
      # history job).
      env {
        name  = "FUGARO_RTDB_URL"
        value = var.credentials_service.rtdb_url
      }
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = var.credentials_service.firebase_project
      }
      # The service's own URL: the audience every caller's Google ID token is
      # minted for, and the one thing that binds such a token to THIS service.
      # Cloud Run assigns it, so it is a variable the init stage fills from
      # this module's output (outputs.tf) rather than anything read here; the
      # unassigned case is local.credentials_service_url_unassigned above.
      # Fail-closed either way: a revision that guessed its own audience would
      # accept a token minted for another service, and one that served with no
      # audience would halt every run (D143) while looking healthy.
      env {
        name  = "FUGARO_CREDENTIALS_SERVICE_URL"
        value = coalesce(var.credentials_service.url, local.credentials_service_url_unassigned)
      }
      # The installation's per-repository jobs, as the triples the service
      # derives the job accounts from (credsvc.JobAccountsFor over
      # task.Slug and gcp.ServiceAccountID): the emails themselves are derived
      # there rather than passed, because an account's name and the
      # repository's slug are both hashes of these three fields, and a list of
      # emails here would be a second derivation that can drift from the
      # accounts the deployer created. This is the one name whose value is a
      # document, and it is the installation's own shape rather than a
      # credential.
      env {
        name  = "FUGARO_JOB_ACCOUNTS"
        value = jsonencode(var.credentials_service.job_accounts)
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.credentials_bundle]
}
