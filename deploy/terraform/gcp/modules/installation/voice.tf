# The voice hop (P5.3.3, design docs/mobile/design/voice-evaluation-switch.md
# §4.4): the third process of this binary to be deployed as a Cloud Run service
# beside the credentials service (credentials.tf) and the intake (intake.tf),
# and the only one of the three a PHONE calls.
#
# WHAT IT IS FOR. A paired device holds a Firebase Auth ID token — item (1) of
# D111's two, the credential the pairing flow already minted for it — and the
# hop is what verifies that token, checks that the device's own node
# config/admin/devices/$uid is still there (D165's `m~` uid family, D166's
# node; revocation is the node's deletion), and enforces the project's voice
# policy node before any speech leaves the phone. internal/voicesvc is the
# whole of the service and internal/cli/voice_serve.go is the process; it
# forwards NOTHING. The provider call, the provider's key and the register are
# P5.3.5's half, at a later level, and no part of that half is deployed here.
#
# OFF BY DEFAULT, like the other two services and for the same reason (D147):
# with var.voice_service null this file creates nothing — no account and no
# service — and the installation is exactly what it was before the hop existed.
# Nothing here reads a value and nothing here carries a prevent_destroy:
# turning the hop off removes a service, an account and a public binding, and
# touches no data (there is no container, no store and no node of the hop's own
# — what it reads is the database and the policy node, and both outlive it).
#
# WHY THE SERVICE'S OWN ACCOUNT HOLDS EXACTLY ONE GRANT, and why that grant is
# in state_read.tf and not here. Verifying a Firebase ID token needs no IAM at
# all: the signature is checked against Google's PUBLISHED certificates
# (internal/credsvc's GoogleKeys, which the hop reaches through its own key
# source), and a published certificate is a fact of the internet rather than a
# grant of this installation. Calling a provider is plain network egress, which
# is not IAM either. The one thing the process reaches in this installation is
# its Realtime Database — the device node and the project policy node, two
# reads, D166 — so the account's whole reach is the instance-wide read role
# fugaroStateRead, bound to it in state_read.tf beside the front door's own
# binding (RTDB IAM has no path-level scope, so that role is a read of EVERY
# node and internal/voicesvc's own paths are what narrow it: internal/voicesvc/
# status.go, policy.go). SECURITY.md states it.
#
# AND NO PROVIDER KEY, YET. This file sets no secret, grants no accessor and
# puts no key in the container's environment: at P5.3.3 the hop holds no
# credential beyond its own identity, so a compromised revision of it brings
# the database read above and nothing else. P5.3.5's forwarding half is what
# adds the provider credential — a secret in secrets.tf and an accessor grant
# beside it, this file's account on both — and that change is the one that has
# to say what a revision of this process can then reach.

locals {
  # The whole file is gated on the one variable, like credentials.tf's and
  # intake.tf's own.
  voice_on = var.voice_service != null

  # The two facts the process is configured with, read out of the credentials
  # service's block — the one place this document carries the installation's
  # Firebase project and database, and the block the same `fugaro update`
  # writes or carries in the same pass that writes var.voice_service (DON1).
  # One fact and not two copies: a second pair of members on var.voice_service
  # would be a value a hand edit could pull apart from the database the
  # credentials service, the runs and the policy all read.
  #
  # try() is what keeps a hand-written tfvars carrying voice_service ALONE
  # from failing as "attempt to get attribute from null value": the locals are
  # evaluated eagerly, whatever the counts say, so the missing block has to be
  # a value here and the refusal has to be the service's own precondition
  # below, whose sentence an operator can act on.
  voice_firebase_project = local.voice_on ? try(var.credentials_service.firebase_project, "") : ""
  voice_rtdb_url         = local.voice_on ? try(var.credentials_service.rtdb_url, "") : ""
}

# The hop's own account. It holds no key and no bundle: the one project grant
# state_read.tf makes, to this account, is its whole reach. Nobody is granted
# actAs on it (history.tf's rule, and credentials.tf's for the same reason): a
# caller who could deploy a revision as this account could read the whole
# database through it.
resource "google_service_account" "voice" {
  count = local.voice_on ? 1 : 0

  project      = var.project
  account_id   = var.voice_service.account_id
  display_name = "Fugaro voice hop"

  lifecycle {
    precondition {
      condition     = var.voice_service != null
      error_message = "the voice hop's account needs var.voice_service"
    }
  }

  depends_on = [google_project_service.this]
}

# The service itself. It runs the released image's `voice serve` subcommand
# (args below) — there is no voice image of its own and no release builds one:
# the hop is a subcommand of the same binary the credentials service and the
# intake are, pinned by the same digest, and the module's validation refuses a
# tag for the reason credentials.tf's does (this account reads the whole
# database instance, and every operator may push a tag to the registry).
#
# IAM IS REQUIRED AND STATED, like every other service's of this tree, and the
# reachability of the hop is the PUBLIC BINDING BELOW rather than the flag's
# default (the front door's rule and the front door's own arrangement,
# coordinator.tf): invoker_iam_disabled = false is written out so that a later
# change of the provider's default cannot open this service silently, and the
# grant that admits everyone is the one resource a reviewer reads in the plan.
# The alternative — switching the IAM check off on the service itself — would
# be the same reachability carried by a flag nobody looks for, which is the
# direction this tree refuses everywhere.
#
# The ingress is ALL for the same reason the other two services' is, and for
# the front door's: the caller is a phone on the public internet, so
# INGRESS_TRAFFIC_INTERNAL_ONLY would refuse every caller there is, and the
# fence is not the network. The port is unnamed because this process serves
# HTTP/1.1 only — internal/cli/voice_serve.go serves through the credentials
# service's own credentialsHTTPServer (credentials_serve.go) — so it needs
# nothing of credentials.tf's h2c dance.
resource "google_cloud_run_v2_service" "voice" {
  count = local.voice_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.voice_service.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "voice"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false

  # The service-level scaling, written out beside the template's for the reason
  # credentials.tf states: the managed gate prices a Cloud Run service by its
  # minimum instance count at BOTH levels, and an attribute the provider
  # computes is unknown at plan time, so a service that states only one is
  # refused as unpriceable at the other. Zero is also the shape this service
  # has: nothing keeps an instance warm, and a phone waits on a cold start
  # rather than the installation paying around the clock for a hop that most
  # projects never call.
  scaling {
    scaling_mode       = "AUTOMATIC"
    min_instance_count = 0
  }

  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "voice"
    }
    service_account = google_service_account.voice[0].email
    timeout         = "300s"

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.voice_service.image

      # The subcommand of the released image, and the one thing that tells this
      # service from the credentials service running the same pin: that image's
      # default command is `credentials serve`, so without these args this
      # container would serve the other service's routes as the hop's account.
      # `args` replaces the image's command and no part of its entrypoint, so
      # the process is `fugaro voice serve` (internal/cli/voice_serve.go) —
      # there is no voice image of its own.
      args = ["voice", "serve"]

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The process's whole configuration, and it is TWO names
      # (internal/cli/voice_serve.go reads exactly these and no other): the
      # installation's Firebase project — the audience and the issuer a
      # device's ID token must carry, which is also the project whose database
      # the two reads are — and that database's URL. They are the same two
      # facts the credentials service is configured with, under the same names,
      # and neither is a credential: the process's identity is the account
      # above, and its only other input is the caller's own token.
      #
      # A process that does not know which project mints the identities it
      # admits, or which database the device nodes live in, must not serve at
      # all (D143's fail-closed half): the service's precondition below refuses
      # the plan that could not put them here, rather than a revision that
      # comes up and refuses every call. The service's own URL is deliberately
      # NOT in this environment: the audience here is the Firebase project and
      # not the service, which is the one real difference from the credentials
      # service and the intake — so there is no URL to read back from this
      # module's output and no second apply for it.
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = local.voice_firebase_project
      }
      env {
        name  = "FUGARO_RTDB_URL"
        value = local.voice_rtdb_url
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  lifecycle {
    ignore_changes = [client, client_version]

    # The two names above are the credentials service's own block, so a
    # hand-written or stale tfvars that carries var.voice_service and no
    # var.credentials_service has no database to read. That is a refusal with a
    # sentence rather than "attempt to get attribute from null value", and it
    # is the plan's and not the revision's: the account, the public binding and
    # the state read are all created by the same apply, and a hop that started
    # without a database would be a public endpoint that can admit a device and
    # enforce nothing.
    precondition {
      condition     = var.credentials_service != null
      error_message = "the voice hop reads the installation's Firebase database, and the two facts that name it — the Firebase project and the database URL — are var.credentials_service's own members (firebase_project, rtdb_url), which this document does not carry: write var.voice_service beside var.credentials_service, which every 'fugaro update' writes (DON1), and apply again."
    }
  }
}

# The hop's reachability, and the whole of it: roles/run.invoker to allUsers, on
# this service and no other. It is the front door's own arrangement
# (coordinator.tf's coordinator_front_door_public, D920) and it is written
# beside the service rather than inside it for that reason — a service's public
# reach is an act a reviewer reads on the plan's own screen, and
# deploy/terraform/terraform_test.go names the two bindings of this tree an
# allUsers member is admitted on, by path and by block, so that neither copying
# this resource under another name nor writing the member into another file can
# pass. internal/infra/tf's cover carries no allUsers member, exactly as it
# carries none for the front door, so the update whose plan holds this binding
# is a step the run's confirmation does not cover: it shows the finding, and it
# is applied (D1545's content class, internal/cli/init_review.go's askOrdinary)
# rather than passing as a plan somebody had already classified.
#
# WHY THE HOP IS PUBLIC, and the alternative that was refused. Cloud Run's
# invoker check admits a GOOGLE IAM principal. A phone has none to offer: what
# it presents is a Firebase ID token, whose subject is a Firebase uid (D165's
# `m~` family, D166's node) and not a Google identity, so there is no member an
# invoker binding could name — `user:` and `serviceAccount:` members would
# admit a Google identity rather than a device, and every device credential of
# the installation's own (a service-account key compiled into the app) is the
# third credential item on a device that the owner's ruling of 2026-10-19
# refused (design §4.4, Q2's answer). So the grant admits a caller to the
# CONTAINER and the handler is still the gate: the first thing the process does
# with a request is VerifyDevice (internal/voicesvc/auth.go, its own file's
# "AND THE FENCE IS THIS PROCESS"), which is the one check that can tell a
# device from anyone else.
#
# THE COST, STATED PLAINLY, because it is a real one and this is where a reader
# meets it. With this binding, ANY host on the internet can reach the service
# and make it do work: a call with no token, with a forged token or with an
# expired one all reach the container, and each one costs a Cloud Run request —
# plus, on the paths that get as far as a key lookup, a JWKS read and a
# database read of the device node. The service scales to zero, holds no
# credential and reaches nothing but the two nodes internal/voicesvc names, so
# what an unauthenticated flood costs is money and a cold start rather than
# data; that is the trade the design takes (Global Constraint 4: the app adds
# no credential), and the reviewer tier of P5.3.3 exists to weigh it.
resource "google_cloud_run_v2_service_iam_member" "voice_public" {
  count = local.voice_on ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_service.voice[0].location
  name     = google_cloud_run_v2_service.voice[0].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
