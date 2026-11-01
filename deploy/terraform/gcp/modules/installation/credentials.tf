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
# keeps that true. Its reach in the realtime database is the OTHER thing this
# file grants, and it is granted the same way — one unconditioned binding, to
# the service's own account — with the permissions spelled out at the role.

locals {
  # The whole file is gated on the one opt-in: null creates nothing, and a
  # single boolean keeps the six resources of this file in step (the account,
  # the bundle container, the accessor grant on it, the database role and its
  # binding, and the service — TestCredentialsServiceResourcesAreCounted is
  # what holds this number and the file's own resources together, in both
  # directions).
  credentials_service_on = var.credentials_service != null

  # The release the image pin names, or "" when the reference carries no
  # released version. The digest says nothing about which process is inside it,
  # and the version beside it is the only thing that does: C1.20's stage
  # resolves the release's own image and writes both (`mirror.RemoteRef`, so
  # `…/fugaro-credentials:<version>@sha256:<digest>`), and a reference an
  # operator wrote by hand, a digest alone, or a version that is not
  # three numbers reads as "" — the direction that claims nothing.
  credentials_image_release = local.credentials_service_on ? try(regex(":([0-9]+\\.[0-9]+\\.[0-9]+)@sha256:", var.credentials_service.image)[0], "") : ""

  # Whether that process serves HTTP/2 cleartext, which is what the port's name
  # below is read as by Cloud Run's front end. The two MUST move together, and
  # what makes them is that the image is pinned by the run that plans this root:
  # the owner-run credentials stage when the service is switched on, and — an
  # installation that opted in under an earlier release — an ordinary
  # `fugaro update`, which re-resolves this release's image and re-pins its
  # digest before planning (internal/cli/init_stages.go's
  # pinCredentialsImage). So the apply that moves the image onto this release
  # is the one that renames the port. The direction of the name is the other
  # half: h2c in front of a process that answers HTTP/1.1 only serves nothing
  # at all — the front end reads the HTTP/2 preface as a malformed request line
  # and halts every call the installation makes — while an unnamed port in
  # front of an HTTP/2 process is merely slower than it could be. So the name is
  # read out of the image rather than assumed, and an image this release did not
  # write reads as not-h2c: an older CLI's update carries the reference forward
  # (infra.CarriedCredentialsService), and an operator's own tfvars may carry no
  # version at all. That is why the release the h2c support first ships in
  # (internal/cli/credentials_serve.go, 0.15.0, the release the plan puts B1.0
  # in) is a FLOOR: at or above it the port is named, below it — and where no
  # version can be read — today's HTTP/1.1 port stays.
  #
  # That release number is written in three places and they move together: this
  # threshold, the image run "credentials_service_on" pins in
  # roots/installation/tests/installation.tftest.hcl (asserting the port is
  # named h2c), and the image run
  # "credentials_service_port_follows_the_image" pins there — the release just
  # before this one, asserting no port at all. The pair is what holds the
  # threshold: a release that changes it changes both fixtures and this local.
  credentials_image_serves_h2c = local.credentials_image_release == "" ? false : (
    tonumber(split(".", local.credentials_image_release)[0]) > 0 ||
    tonumber(split(".", local.credentials_image_release)[1]) >= 15
  )

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
# deliberately NOT fugaro_secret: that label is what `fugaro update`'s discovery
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

# The service's reach in the realtime database, and why it is a custom role
# rather than one of the two predefined ones.
#
# What the service does there (internal/credsvc, internal/creds,
# internal/projectpolicy, internal/budget). It READS the kill switches
# (config/kill/global, config/kill/repos/$slug), the project policy
# (config/policy/shared, config/policy/repos/$slug), a run's outcome node
# (outcomes/$day/$slug/$run — how it decides a caller is a real run) and the
# credential state it did not write itself (creds/, whose marks, current nodes,
# run leases and probe leases every other revision of the service also reads).
# It WRITES the credential state it owns — creds/marks/$name,
# creds/current/$slug/$run, creds/leases/... and creds/probes/$name — by
# compare-and-set, and deletes the leases it finds lapsed. Nothing else in the
# installation writes any of those nodes (the repository jobs read their own
# node through the service, never directly).
#
# Why these two permissions are the narrowest expression of that. Realtime
# Database IAM has NO path-level scope: a binding is per instance and reaches
# the whole database, so what the role says is which OPERATIONS the account may
# make, and the two predefined roles are the only other choice:
#
#   firebasedatabase.instances.get    — read any node of the instance. The
#     only read permission there is: get is both the instance read and the
#     data read (modules/firebase/iam.tf grants roles/firebasedatabase.viewer,
#     which is exactly get+list, for "read the database").
#   firebasedatabase.instances.update — write any node of the instance. The
#     only write permission: RTDB has no separate create/delete of a node —
#     a write is an update — and update is what roles/firebasedatabase.admin
#     carries for the budget admins who "write /config over IAM".
#
# roles/firebasedatabase.viewer is too narrow: it cannot write creds/ at all,
# so every run would be refused. roles/firebasedatabase.admin is broader than
# the service needs in the one direction that matters — it adds
# instances.list, and the instance LIFECYCLE the service never touches:
# firebasedatabase.instances.create, delete, disable, reenable and undelete
# (deploy/terraform/testdata/custom-role-permissions.txt is the whole set
# Google allows in a custom role, and those five are the difference). So the
# service would hold the ability to disable or DELETE the installation's
# database, which is the opposite of what a component that holds every model
# credential should be able to do to the installation.
#
# The role is declared in the FIREBASE project, not in var.project, and the
# distinction is a two-project installation's whole point: the account is
# created in the installation's project (the resource above), but the database
# belongs to the Firebase root's project (D6), which is
# var.credentials_service.firebase_project. A binding of a role declared in
# one project to a member of another is what cross-project IAM is, and it is
# the shape the existing history account's database grant has. The role is a
# plain role_id with no condition: a custom role carries no scope of its own,
# and this file's one condition stays the account's precondition.
#
# TestCredentialsServiceDatabaseRoleIsTheTwoDataPermissions is what holds every
# part of this comment together: the exact two permissions and no third, the
# Firebase project, the account as the only member, and no condition.
resource "google_project_iam_custom_role" "credentials_database" {
  count = local.credentials_service_on ? 1 : 0

  project = var.credentials_service.firebase_project
  role_id = "fugaroCredentialsDatabase"
  title   = "Fugaro credentials service (database data only)"
  permissions = [
    "firebasedatabase.instances.get",    # read: the kill switches, the policy, a run's outcome node, and the credential state another revision wrote
    "firebasedatabase.instances.update", # write: the service's own creds/ nodes, by compare-and-set
  ]
}

# ... and the one binding, on that role, to the service's own account. No
# condition: RTDB cannot express a path prefix in IAM, so a condition here
# would be a fence that looks like one and is not — the same reasoning Q6 used
# to replace the name-prefix fence in Secret Manager. No second member: no
# operator and no job account reads the database through this role, and in
# particular nobody is granted actAs on the account (the rule at the account
# above).
#
# The role is spelled LITERALLY, not read from the role resource's own name
# attribute — the rule deployer.tf's nine bindings state, and here it is not
# about a condition but about what a plan can be read as. google_project_iam_
# custom_role's name is computed (the provider fills it during apply), so
# `google_project_iam_custom_role.credentials_database[0].name` reaches the
# plan as an UNKNOWN role, and internal/infra/tf's cover then cannot say which
# role this grant is: it accepts any custom role of the plan that some row
# admits for this account (checkGrant's unknown-role arm). Writing the path out
# keeps the role a plain plan-time string, so the confirmation that covers this
# apply covers exactly firebasedatabase.instances.get+update on the Firebase
# project, and a widened role is refused rather than invisible.
# var.credentials_service.firebase_project is known at plan time (the variable's
# validation), so the literal is.
resource "google_project_iam_member" "credentials_database" {
  count = local.credentials_service_on ? 1 : 0

  project = var.credentials_service.firebase_project
  role    = "projects/${var.credentials_service.firebase_project}/roles/fugaroCredentialsDatabase"
  member  = google_service_account.credentials[0].member

  depends_on = [google_project_iam_custom_role.credentials_database]
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

      # The port the container listens on, and its name is the whole point of
      # this block: Cloud Run reads that name and talks to the container the way
      # it spells. `h2c` is HTTP/2 cleartext, which is what B1.0 is for — the
      # plan made that task conditional on the owner's Check 190 failing a large
      # push, because the hop in front of this process buffers an HTTP/1 request
      # body and refuses one a large git pack exceeds (413). None of that is
      # measured on this tree: the checklist still records Check 190 as not run,
      # and the reconciliation marks the reading it rests on (U2: that HTTP/2
      # streaming keeps the pack off that buffer) unverified. So the name is
      # what the plan expects to turn that hop's HTTP/2 on, not a result. The
      # service's own server speaks both protocols on its one listener
      # (internal/cli/credentials_serve.go), so the HTTP/1.1 callers it has
      # today keep working. 8080 is the number the process itself falls back to
      # when Cloud Run's PORT is unset (servePort), and it is Cloud Run's own
      # default.
      #
      # The name follows the image: an unnamed port is HTTP/1.1, and a front end
      # told h2c in front of a process that cannot answer it serves nothing at
      # all, so only an image whose own server speaks h2c gets the name (the
      # local above says why the two cannot be assumed to have moved together).
      # An installation that has not re-pinned its image yet keeps today's
      # unnamed port and today's working HTTP/1.1 service.
      dynamic "ports" {
        for_each = local.credentials_image_serves_h2c ? [8080] : []
        content {
          name           = "h2c"
          container_port = ports.value
        }
      }

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
