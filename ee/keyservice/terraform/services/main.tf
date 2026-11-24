# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The services root: the portal's two Cloud Run services and everything one
# project holds for them (D2194, S3.5, docs/key-signing-service/spec.md §3). The
# API is the internet-facing half — the SPA's calls, the merchant's events, the
# scheduler's monthly and daily runs — and the minter is the one service that
# signs: only the API's account may call it, and the call carries an ID token
# Cloud Run verifies (D450(a)).
#
# The identities are the point rather than the services: the API's account holds
# nothing that signs, so the internet-facing process cannot sign even if its code
# is fully in an attacker's hands, and the minter's account holds the signer role
# on the key ring and nothing else — no store, no mail path, no merchant secret
# (§3.3, Check 571). What the API's account holds is one list: the portal's own
# documents, the authenticated call to the minter, and the handover ring it seals
# and opens a reveal with (D2182). Everything else either process holds is a
# secret this root creates an empty container for and the deploy (P9.2, S3.7)
# fills.
#
# Every one of these lives in the signer's own project (D2194): the fence between
# two projects is gone, so what stands between the internet and a signature is the
# IAM check on the minter, its internal ingress, and the one permission the signer
# role carries. Check 571 is the list a person reads to see that, and the tests in
# ../infra_test.go are the machine's half of it.
#
# Applied after keyring/ and only once the entity exists (S3.7): the design's
# step 3. The key ring is read through a data source, so this root fails to plan
# while S3.8's apply has not run — the order the runbook puts the two in.

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

provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? toset([
    "run.googleapis.com",
    "firestore.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "cloudkms.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
    "compute.googleapis.com",
  ]) : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}

# The network the API's egress runs over. P1.5 asks for the minter's ingress to
# be internal, and internal means "from this project's VPC": Cloud Run counts a
# caller as internal only when its request is routed through the network, so
# without this the platform refuses every mint call before the IAM check is
# reached and nothing can be minted in either environment. The API's revision
# therefore takes Direct VPC egress below, and the three resources here are what
# it egresses onto.
#
# The network is this root's own rather than the project's `default`: a default
# network arrives with auto-created subnets and its own firewall rules, none of
# which this tree reviews, and a name this root declares is a network a plan
# shows as a create rather than one a reader has to go and look at.
resource "google_compute_network" "portal" {
  project                 = var.project
  name                    = var.network
  auto_create_subnetworks = false

  depends_on = [google_project_service.this]
}

# The subnet the API's revisions take their egress addresses from. Private Google
# access is what carries the calls to Google's own APIs — Firestore, Secret
# Manager, KMS through the minter's URL — over the network instead of the public
# path, and the NAT below covers everything that is not Google's, which is where
# Paddle and Resend are.
resource "google_compute_subnetwork" "portal" {
  project                  = var.project
  name                     = var.subnetwork
  network                  = google_compute_network.portal.id
  region                   = var.region
  ip_cidr_range            = var.subnet_cidr
  private_ip_google_access = true
}

# The way out that is not Google's, and the half of an all-traffic egress that is
# easy to leave out: a revision on this network has no external address, so with
# no NAT it reaches the minter and the Google APIs and nothing else — no Paddle
# charge, no Paddle event read back, no mail through Resend (deploy/terraform's
# compute module provisions the same pair for the same reason: private Google
# access is not a substitute for a NAT).
resource "google_compute_router" "portal" {
  project = var.project
  name    = var.router
  region  = var.region
  network = google_compute_network.portal.id

  depends_on = [google_project_service.this]
}

resource "google_compute_router_nat" "portal" {
  project = var.project
  name    = var.nat
  router  = google_compute_router.portal.name
  region  = var.region

  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"

  # Only this root's own subnet: a NAT translates the traffic of the subnetworks
  # it names, and a sibling's traffic is not this root's to put on the internet.
  subnetwork {
    name                    = google_compute_subnetwork.portal.name
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}

# The environment's key ring, the one keyring/'s apply of the same environment
# created: the name is derived from the same two variables there and here, so an
# apply of this root against the other environment's ring is a plan that fails
# rather than a grant on a ring nobody reviewed.
data "google_kms_key_ring" "signing" {
  project  = var.project
  name     = "${var.keyring}-${var.environment}"
  location = var.region
}

# The project's number, which a Cloud Run URL is spelled with. It is read rather
# than left to the deploy: the URL below is what the API checks a scheduled
# call's token against (P1.3, F33), and a value the deploy passed would be a
# second spelling of the audience the jobs' tokens carry.
data "google_project" "this" {
  project_id = var.project
}

# The two services' environments, and the whole of them: every variable P1.7's
# programs read before either one serves (ee/keyservice/config.go). Nothing else
# sets them — P9.2's deploy hands `gcloud run deploy` an image and nothing more —
# so a variable left out here is a revision that refuses at startup and never
# becomes ready, which is a deploy that looks like a rollout and is a service
# nothing answers on. The reader is strict in both directions, so this file and
# that one are a pair: every FUGARO_ name below is one it reads, and every one it
# reads is below. TestTheServicesSetEveryVariableTheProgramsRead is the machine's
# half of that pair.
#
# **Why Terraform and not the deploy script.** This root owns both revisions'
# templates: every apply rewrites a container's image, and the lifecycle blocks
# below ignore only `client` and `client_version`. An environment set by
# `gcloud run deploy` would be erased by the next apply — silently, and with a
# service that then refuses to start — so the values live in the template itself.
#
# The minter's set is three names and not one secret, which is the identity
# boundary reaching the deployment: this project's own apply is what keeps a
# merchant secret, a mail key and a store out of the process that signs
# (§3.3, Check 571).
locals {
  # The API's own URL, which is the audience both sides of a scheduled call name:
  # it is what FUGARO_SCHEDULER_AUDIENCE carries below and what scheduler.tf's
  # `oidc_token` blocks are minted for, because the API compares that claim
  # exactly. Neither place can simply read the service's own `uri` — a container's
  # env cannot read an attribute of the resource it is declared in, and Cloud Run
  # answers with its assigned URL anyway, the `<service>-<hash>-<code>` form under
  # `.a.run.app`, which is a different string from this one. It is Cloud Run's own
  # form,
  # `<service>-<project number>.<region>`, the spelling deploy/terraform's
  # coordinator uses for its issuer. A change in that form is a scheduled call
  # refused for an audience nobody minted for, and the jobs go quiet rather than
  # loud — the two places are one value, and this is the one of the two that
  # cannot be an attribute.
  api_url = "https://${var.api_service}-${data.google_project.this.number}.${var.region}.run.app"

  # The KMS key version the minter signs with (S2.1): this environment's licence
  # key, whose only version is the one Cloud KMS created with it. Rotation is a
  # new key id published in all three verifiers (§11), so it moves the
  # `cryptoKeys` segment above and never the version number — an asymmetric key
  # has no primary version to fall forward to, and a signer naming a version
  # nobody published signs nothing.
  signer_key_version = "projects/${var.project}/locations/${var.region}/keyRings/${var.keyring}-${var.environment}/cryptoKeys/${var.keys[var.environment]}/cryptoVersions/1"

  # Which container holds which secret: FUGARO_SECRETS, the config's own roles
  # (knownSecretRoles) against this root's containers. The merchant's role is the
  # webhook secret, the value merchant.go verifies a Paddle event's signature
  # with. The DSN is named only when `var.secrets` names a container for it: a
  # container nobody has filled has no version to read, so naming the role
  # unconditionally would refuse a deployment that chose P1.6's off.
  secret_roles = merge(
    {
      merchant       = var.secrets["paddle_webhook"]
      admin          = var.secrets["admin_secret"]
      paddle_api_key = var.secrets["paddle_api_key"]
      resend_api_key = var.secrets["resend_api_key"]
    },
    contains(keys(var.secrets), "sentry_dsn") ? { sentry_dsn = var.secrets["sentry_dsn"] } : {},
  )

  api_env = {
    FUGARO_ENVIRONMENT        = var.environment
    FUGARO_KEYS               = jsonencode(var.keys)
    FUGARO_URL                = var.intake_url
    FUGARO_PLANS              = var.plans
    FUGARO_FREE_MAIL_DOMAINS  = join(",", var.free_mail_domains)
    FUGARO_VERIFICATION_TTL   = var.verification_ttl
    FUGARO_CODE_HOSTS         = join(",", var.code_hosts)
    FUGARO_MAX_REPOSITORIES   = tostring(var.max_repositories)
    FUGARO_THROTTLE           = var.throttle
    FUGARO_INVITE_RATE        = var.invite_rate
    FUGARO_ADMIN_ACCOUNT      = var.admin_account
    FUGARO_ADMIN_TOKEN_TTL    = var.admin_token_ttl
    FUGARO_PADDLE_PRICE_ID    = var.paddle_price_id
    FUGARO_SCHEDULER_AUDIENCE = local.api_url
    FUGARO_SCHEDULER_ACCOUNT  = google_service_account.scheduler.email
    FUGARO_SEALER_KEY_NAME    = google_kms_crypto_key.handover.id
    FUGARO_TENANTS_COLLECTION = var.tenants_collection
    FUGARO_TERMS_VERSION      = var.terms_version
    FUGARO_SECRETS            = jsonencode(local.secret_roles)
    FUGARO_PROJECT            = var.project
    FUGARO_MINTER_URL         = google_cloud_run_v2_service.minter.uri
    FUGARO_MAIL_FROM          = var.mail_from
    FUGARO_NOTIFY_ADDRESS     = var.notify_address
  }

  minter_env = {
    FUGARO_ENVIRONMENT        = var.environment
    FUGARO_KEYS               = jsonencode(var.keys)
    FUGARO_SIGNER_KEY_VERSION = local.signer_key_version
  }
}

# The portal's one document store: its own native Firestore database, separate
# from the installation's realtime database (spec §4, D2188). The rules it is
# deployed with deny every client and live beside the SPA
# (ee/portal/firestore.rules), so the documents are reachable through the API's
# account and through nothing else.
resource "google_firestore_database" "portal" {
  project     = var.project
  name        = "portal"
  location_id = var.region
  type        = "FIRESTORE_NATIVE"

  depends_on = [google_project_service.this]
}

# The secrets' empty containers, and nothing more: the deploy (P9.2, S3.7) adds
# the versions, because a value that reaches this repository reaches every clone of
# it. Creating the containers here is what makes the deployment a step that fills
# named holes rather than one that invents names, and an empty container is a
# finding a person can see on a plan.
resource "google_secret_manager_secret" "this" {
  for_each = var.secrets

  project   = var.project
  secret_id = each.value

  replication {
    auto {}
  }

  labels = {
    fugaro = "portal"
  }

  depends_on = [google_project_service.this]
}

# Who reads them: the API's account, per container. It is the process that holds
# the Paddle key, the merchant's webhook secret, the mail key and the error DSN
# (P1.7's config), and the minter — the one identity that signs — reads none of
# them (§3.3, Check 571: it holds no merchant secret).
#
# One grant per secret rather than one on the project: an accessor granted at the
# project reaches every secret the project ever holds, including any a later root
# creates for another purpose, and this list is the whole of what the API may
# read. The map is the containers' own, so the two cannot drift.
resource "google_secret_manager_secret_iam_member" "api_reads" {
  for_each = google_secret_manager_secret.this

  project   = var.project
  secret_id = each.value.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.api.member
}

resource "google_service_account" "api" {
  project      = var.project
  account_id   = var.api_account
  display_name = "Fugaro portal API"

  description = "The portal's API: the SPA's calls, the merchant's events and the scheduled runs. Holds nothing that signs, and reaches the signer only through the minter (Check 571)."

  depends_on = [google_project_service.this]
}

resource "google_service_account" "minter" {
  project      = var.project
  account_id   = var.minter_account
  display_name = "Fugaro key minter"

  description = "The one identity that signs: the signer role on the key ring, and read access to nothing an applicant typed."

  depends_on = [google_project_service.this]
}

# The API is reachable by everyone, because that is who calls it: the SPA in a
# person's browser, the merchant's webhook, the scheduler. IAM is required and
# stated rather than left to the provider's default, so a later change of that
# default cannot open or close the service silently; the public binding is beside
# it, where a plan's review screen finds it.
resource "google_cloud_run_v2_service" "api" {
  project  = var.project
  location = var.region
  name     = var.api_service
  ingress  = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.api.email

    # The API's egress, and the whole reason it is here: the minter's ingress is
    # internal, and a call is internal only when it arrives over this project's
    # network. ALL_TRAFFIC rather than the private ranges alone, because the
    # minter answers on its run.app URL, which resolves like any other host — so
    # the call that signs takes the same path as the rest of the API's traffic,
    # and the NAT above keeps that path a way out to Paddle and Resend.
    vpc_access {
      egress = "ALL_TRAFFIC"

      network_interfaces {
        network    = google_compute_network.portal.id
        subnetwork = google_compute_subnetwork.portal.id
      }
    }

    # Scale to zero: a public endpoint nobody is calling costs nothing, and a
    # cold start is what a caller waits for.
    #
    # **And one instance at most, which is what makes the two mint limits the
    # limits the specs state.** D454's per-tier rate (ops.go) and a reseller's
    # credit limit (accrual.go) are counted in the process's own memory, in the
    # one lock each takes: the store has no counter for either — both are counted
    # over the entitlements by queries of their own, and a query inside a grant's
    # transaction is a read every concurrent write conflicts with — so what each
    # lock closes is one process's window. The platform's own ceiling is about a
    # hundred instances, scaled by the caller's traffic, which is the shape that
    # makes the gap dangerous rather than merely wide: the caller a mint limit is
    # meant to refuse is the one whose traffic buys the instances, so a reseller
    # minting in a burst would get a limit's worth of sales on each replica and
    # the plan's own constraint — two concurrent reseller mints cannot pass the
    # credit limit together — would hold of a process and not of the
    # installation. A second instance buys this API nothing it cannot serve from
    # the first: it answers the console's own requests and the platform's
    # callbacks, and a cold start is what a caller pays for either way.
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }

    containers {
      image = var.api_image

      # The API's own set, one entry per variable its loader reads: `local.api_env`
      # above, and the reader refuses a name that is not in its set, so an entry
      # added here without a change in ee/keyservice/config.go is a revision that
      # does not start.
      dynamic "env" {
        for_each = local.api_env

        content {
          name  = env.key
          value = env.value
        }
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
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
}

# The minter: no public invoker, and its ingress internal. The fence is the IAM
# check the platform runs before the container, and the ingress is what keeps the
# call the grant allows the only one the network delivers — an internal-only
# service accepts traffic from this project's network, which the API's revision
# egresses through (see the network at the top of this file), so a caller outside
# it is refused before IAM is consulted.
#
# The ingress is stated here rather than left to the provider: with ALL, the
# internet could reach the container and the invoker grant would be the only thing
# refusing it.
resource "google_cloud_run_v2_service" "minter" {
  project  = var.project
  location = var.region
  name     = var.minter_service
  ingress  = "INGRESS_TRAFFIC_INTERNAL_ONLY"

  invoker_iam_disabled = false
  deletion_protection  = false

  template {
    service_account = google_service_account.minter.email

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.minter_image

      # The minter's three, and none of the API's: `local.minter_env` above. A
      # deployment that handed this container the API's environment is refused by
      # its own loader before it signs anything (Check 174), so the two sets are
      # two fences rather than one list used twice.
      dynamic "env" {
        for_each = local.minter_env

        content {
          name  = env.key
          value = env.value
        }
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
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

  # The intake reads this service's answer as strictly as this service reads the
  # intake's request, so a field a newer minter adds to the record is a whole
  # answer a released intake refuses and every mint fails for as long as that
  # intake is the one serving: this service is the revision that has to land
  # second, and the one apply that changes both is the only place the order can
  # be stated. There is nothing to guard the other way round — an answer that
  # leaves a field out is that field's zero value to its reader — so the pair is
  # ordered one way and not symmetrically.
  depends_on = [google_cloud_run_v2_service.intake]
}

# The seed (P0.5, P2.2). `seed-operations <address>` — the API program's one
# subcommand — writes the operations organisation and its first invitation, once
# per installation, and **no terminal can run it**: the program's loader requires
# the whole of what the API requires (ee/keyservice/config.go, twenty-odd
# variables and five secrets), because it builds the service the API serves from
# before it seeds, and an owner's own credentials carry none of them. So the seed
# is this job: the API's image, the API's account and **the API's own environment,
# `local.api_env`** — one set and not a second copy of it, so a variable added to
# the service is on the job in the same apply.
#
# Its identity is the API's account and no other, for the same reason the service
# is: the seed reads the portal's documents, the deployment's secrets and the
# handover key, which is exactly that account's list (api_reads, api_datastore,
# api_seals) and no part of the minter's.
#
# The args below name the subcommand and no address, and that is the safe default
# rather than a working invocation: a job executed with no override refuses in the
# program's own words and seeds nothing, so an operator who runs this from the
# console gets a refusal that names the address instead of an organisation nobody
# asked for. The deploy passes both (scripts/deploy-portal.sh's
# `--seed-operations`, `gcloud run jobs execute --args=seed-operations,<address>`).
resource "google_cloud_run_v2_job" "seed" {
  project  = var.project
  location = var.region
  name     = var.seed_job

  deletion_protection = false

  template {
    task_count = 1

    template {
      # One execution, one task, one organisation. A retry would run the seed
      # again against a deployment it has already written to — the program refuses
      # that (errOperationsSeeded) — so a failure is one execution an operator
      # reads rather than three of them in a log.
      max_retries = 0

      # Longer than the program's own bound on the seed (cmd/portal-api's
      # seedTimeout, two minutes), so a seed that hangs ends with the program's
      # own line in the log rather than as a task the platform killed.
      timeout = "300s"

      service_account = google_service_account.api.email

      # Every other Cloud Run job in the tree sets this (deploy/terraform's
      # reaper.tf and the jobs beside it), and this one is the API's program as
      # well: a second-generation sandbox is what the services' own revisions get.
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      # The API's egress route, for the API's reason: the mint call is not made
      # here — the seed reaches no route — but the mail the invitation is goes out
      # through the same NAT (see the network at the top of this file), and a
      # deployment that changed the API's egress would otherwise have a seed that
      # fails on the one thing it does differently from a service.
      vpc_access {
        egress = "ALL_TRAFFIC"

        network_interfaces {
          network    = google_compute_network.portal.id
          subnetwork = google_compute_subnetwork.portal.id
        }
      }

      containers {
        image = var.api_image
        args  = ["seed-operations"]

        dynamic "env" {
          for_each = local.api_env

          content {
            name  = env.key
            value = env.value
          }
        }

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }
}

# The API alone is public: roles/run.invoker to allUsers, on the API and on
# nothing else in this file.
resource "google_cloud_run_v2_service_iam_member" "api_public" {
  project  = var.project
  location = google_cloud_run_v2_service.api.location
  name     = google_cloud_run_v2_service.api.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# Only the API's account may call the minter, which is what makes the minter
# non-public without a second front door: the ID token the call carries is minted
# for this account and the platform checks it before the container runs.
resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter" {
  project  = var.project
  location = google_cloud_run_v2_service.minter.location
  name     = google_cloud_run_v2_service.minter.name
  role     = "roles/run.invoker"
  member   = google_service_account.api.member
}

# The signing grant, and the whole of it: the signer role, at this environment's
# key ring (keyring/ creates it), to one account. Any second member here is a
# second identity that can mint a licence key, and Check 571 measures that there
# is not one.
resource "google_kms_key_ring_iam_member" "minter_signs" {
  key_ring_id = data.google_kms_key_ring.signing.id
  role        = "projects/${var.project}/roles/${var.signer_role}"
  member      = google_service_account.minter.member
}

# The document store, and the one identity that reaches it: every organisation's
# records, billing and reveal bundles are written by the API through its own
# account, and the minter — which holds the signer role and nothing else — reads
# none of it (§3.3, Check 571). The SPA reads the same documents through the API
# and never through the client SDK (D2188).
resource "google_project_iam_member" "api_datastore" {
  project = var.project
  role    = "roles/datastore.user"
  member  = google_service_account.api.member
}

# A person deleting their own account takes their sign-in with them (D2195), and
# the act on the Identity Toolkit that does it — `accounts:delete` — is an admin
# call: the API is the process that makes it, through the account it already runs
# as, and `firebaseauth.users.delete` is the whole of what that call needs.
#
# The predefined role that permission comes in, `firebaseauth.admin`, is not that
# call's reach and is not what this account may hold: it also lists and reads
# every user in the project, creates and updates users (an address set as
# verified among them) and changes the project's sign-in settings — its providers
# and its authorized domains. The internet-facing account holds none of those, so
# the role is a custom one with the one permission. The history job holds the
# predefined role for its own deletion of Auth users (the firebase module), where
# no request reaches the account.
resource "google_project_iam_custom_role" "deletes_signins" {
  project     = var.project
  role_id     = var.deletes_signins_role
  title       = "Fugaro sign-in deleter"
  description = "Deletes a sign-in on the Identity Toolkit, which the API does only for the account that asked for its own deletion. The API's service account is its only holder (D2195, Check 571)."
  permissions = ["firebaseauth.users.delete"]

  depends_on = [google_project_service.this]
}

# The API's grant of that role, and the whole of it: one member, the account the
# internet-facing service runs as. It reaches no key, so Check 571's boundary
# around the API's other four reaches is where it was.
resource "google_project_iam_member" "api_deletes_signins" {
  project = var.project
  role    = "projects/${var.project}/roles/${var.deletes_signins_role}"
  member  = google_service_account.api.member
}

# The reveal handover's key: the API seals the bundle it stores and opens it once,
# 24 hours later, and nothing else in this project may use the key (D2182,
# Check 571). ENCRYPT_DECRYPT with the symmetric algorithm, because this key seals
# and opens and produces no signature at all — the signing is the minter's, and the
# signer role, granted at a RING, reaches no key in this one.
#
# The ring is its own rather than the licence ring's, for that same reason: a ring
# holding both an encryption key and a signing key is a ring where the one grant
# that mints reaches the sealer too. Ring and key share `var.handover_key`, so the
# key id the deploy hands the API is this ring's path plus that name.
resource "google_kms_key_ring" "handover" {
  project  = var.project
  name     = var.handover_key
  location = var.region

  depends_on = [google_project_service.this]
}

resource "google_kms_crypto_key" "handover" {
  key_ring = google_kms_key_ring.handover.id
  name     = var.handover_key

  purpose = "ENCRYPT_DECRYPT"

  version_template {
    algorithm = "GOOGLE_SYMMETRIC_ENCRYPTION"
  }
}

# The sealer role, and the whole of what sealing takes: the API encrypts a
# handover and decrypts it once, and signs nothing anywhere (D2182).
resource "google_project_iam_custom_role" "handover" {
  project     = var.project
  role_id     = var.handover_role
  title       = "Fugaro handover sealer"
  description = "Encrypts and decrypts a reveal handover. The API's service account is its only holder (D2182, Check 571)."
  permissions = ["cloudkms.cryptoKeyVersions.useToEncrypt", "cloudkms.cryptoKeyVersions.useToDecrypt"]

  depends_on = [google_project_service.this]
}

# The API's grant on the handover ring, and the whole of it: one member. A second
# member here is a second identity that can open every sealed reveal in the store,
# which is the one thing the encryption is for.
resource "google_kms_key_ring_iam_member" "api_seals" {
  key_ring_id = google_kms_key_ring.handover.id
  role        = "projects/${var.project}/roles/${var.handover_role}"
  member      = google_service_account.api.member
}
