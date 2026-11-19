# The public mobile service (mobile plan task P5; D2782 as amended by F31, O8):
# a SECOND Cloud Run service of the credentials service's own image, running
# beside it and reached by anybody, which is where the mobile pairing, refresh
# and restore endpoints live. credentials.tf's service stays private and its
# invoker grant is untouched.
#
# WHY A SECOND SERVICE AT ALL, AND NOT ROUTES ON THE FIRST. F31 is the finding:
# credentials.tf's service requires IAM and grants the invoker to named classes
# alone, and it never grants it to allUsers — that is the shape every run's call
# passes through, with every model credential of the installation behind it. A
# phone has no Google IAM identity to present (it holds a Firebase ID token, an
# identity of the Firebase project, which the front end cannot check), so the
# only way a device reaches a Cloud Run service is an unauthenticated invoker
# binding — and that binding on the credentials service would put the bundle
# behind a URL anyone may call. So the endpoints are served by the same image,
# the same pin and the same code, from a service whose ONLY grant is the reach
# they need. There is no mobile image of its own: this is the binary the
# credentials service runs, started in its `mobile` mode (the args below), which
# mounts the allowlisted mobile routes and none of the existing ones (I1 owns
# what that mode mounts, and its cross-class test is the other half of this
# file's promise).
#
# WHAT THE TWO CALLER CLASSES ARE, and why the fence is not IAM. The private
# service's callers are the installation's own jobs, operators and launchers:
# named Google identities, and IAM is the fence. This service's caller is a
# PHONE: it presents a Firebase ID token or a K3-signed assertion, and the first
# thing the process does with a request is verify it (the Verifier of C1/P1 for
# pairing and restore, the Firebase ID token for the mobile routes' own reads)
# before any route acts. So the invoker binding admits a caller to the
# CONTAINER, and the process is the gate — the front door's own arrangement
# (coordinator.tf) and the hop's (voice.tf).
#
# THE COST, STATED PLAINLY. Any host on the internet can reach this service and
# make it do work: a call with no token, a forged one or an expired one all
# reach the container, and each costs a Cloud Run request plus, on the paths
# that get as far as a database read, an RTDB read. What it must never reach is
# a model credential: this process holds no bundle read (the container below
# carries no Secret Manager grant, and the account holds none), so the worst an
# unauthenticated flood costs is money and a cold start. That is the trade the
# owner weighed and confirmed (O8), and the reviewer tier of this task exists to
# re-weigh it.
#
# OFF BY DEFAULT, with the credentials service it belongs to: with
# var.credentials_service null nothing here is created, and this task adds NO
# variable of its own — an installation that runs the credentials service gets
# the mobile endpoints, which is what the pairing tasks (P2, P3, P4) are for,
# and one that runs none gets neither service. Turning it off removes a service,
# an account and two grants; nothing in this file holds data, so no removal here
# meets prevent_destroy.
#
# ITS GRANTS, and the whole of them. The runtime account (below) holds
# iam.serviceAccounts.signJwt on the DEVICE-ONLY signer account of the Firebase
# project — a second signer created beside the runs' own
# (modules/firebase/iam.tf), because Firebase accepts a custom token only from
# an account of the Firebase project and an installation can span two — and the
# unchanged database role fugaroCredentialsDatabase, bound in credentials.tf
# beside the private service's own binding. O8's correction is what a reader of
# this file has to take away: signJwt is the widest grant in this tree, it signs
# ANY claim set for ANY uid, and IAM cannot limit which claims a signer signs.
# What makes this account's reach the devices' is the process's route allowlist
# (no route takes a caller-chosen uid or claim) and the tests over it; the
# account's own separation buys a key that can be disabled alone and an audit
# trail of its own, and nothing more. SECURITY.md states the residual risk (D1).

# The runtime account. It holds no bundle read and no project role of its own:
# the two grants are the signJwt below and credentials.tf's database binding,
# and that is the whole of its reach. Nobody is granted actAs on it
# (history.tf's rule, and credentials.tf's for the same reason): whoever could
# deploy a revision as this account could mint a device token for any uid.
resource "google_service_account" "mobile" {
  count = local.credentials_service_on ? 1 : 0

  project      = var.project
  account_id   = "fugaro-mobile"
  display_name = "Fugaro mobile service"

  lifecycle {
    precondition {
      condition     = var.credentials_service != null
      error_message = "the mobile service's account needs var.credentials_service"
    }
  }

  depends_on = [google_project_service.this]
}

# The service itself: the credentials image, this time in its mobile mode, and
# the ONE public service of this installation.
#
# The name and the account are literals of this file rather than members of
# var.credentials_service. The account is the one that may mint a device token
# for any uid, and a name an operator's hand-edited tfvars could move is a name
# a review screen cannot read; the service follows the same rule so that a
# reader of a plan knows which public endpoint they are looking at.
resource "google_cloud_run_v2_service" "mobile" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = "fugaro-mobile"
  labels = {
    fugaro      = "managed"
    fugaro_role = "mobile"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  # IAM is required and stated, exactly as on every other service of this tree:
  # the reachability of this one is the PUBLIC BINDING BELOW and not the flag's
  # default, so a later change of the provider's default cannot open it
  # silently, and the grant that admits everyone is a resource a reviewer reads
  # in the plan. Setting this flag true instead would be the same reachability
  # carried by an attribute nobody looks for — the direction voice.tf and
  # coordinator.tf both refuse.
  invoker_iam_disabled = false

  # The service-level scaling, written out beside the template's for the reason
  # credentials.tf states: a managed project's gate prices a Cloud Run service
  # by its minimum instance count at BOTH levels, and an attribute the provider
  # computes is unknown at plan time, so a service that states only one is
  # refused as unpriceable. Zero is also the shape this service has: a device's
  # pairing is a rare call, and nothing here keeps an instance warm.
  scaling {
    scaling_mode       = "AUTOMATIC"
    min_instance_count = 0
  }

  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "mobile"
    }
    service_account = google_service_account.mobile[0].email
    timeout         = "300s"

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.credentials_service.image

      # The mode, and the one thing that tells this service from the private one
      # running the same pin: that image's default command is the credentials
      # service, so without these args a public URL would serve the credentials
      # routes as this account — the outcome F31 exists to prevent. `args`
      # replaces the image's command and no part of its entrypoint, so the
      # process is `fugaro credentials serve --mobile`
      # (internal/cli/credentials_serve.go, whose mobile mode mounts the
      # allowlisted mobile routes and none of the existing ones — I1).
      args = ["credentials", "serve", "--mobile"]

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The process's whole configuration (internal/cli's
      # credentialsServiceConfig), and it is the SAME seven names the private
      # service carries — the service is the same binary, and the seven are what
      # it refuses to bind a port without, whatever mode it is in. They are read
      # from the same block for that reason: a second set of names, or a second
      # spelling of a value, would be a public service configured from
      # something other than the installation's own document.
      #
      # What the mobile routes use of them is a subset — the Firebase project
      # whose identity project signs a device's assertion, the database the
      # device nodes live in, and the project the signer and the database role
      # belong to — and FUGARO_CREDENTIALS_SERVICE_URL is deliberately the
      # PRIVATE service's URL rather than this one: it is the audience the
      # private routes' Google ID tokens are minted for, nothing on the mobile
      # routes reads it, and this service's own URL cannot be written here (a
      # resource cannot read its own uri). The mobile routes derive their
      # audience from the caller's own assertion instead (P3).
      #
      # No name here is a credential, and no entry takes its value from a source
      # (a secret_key_ref or the like): internal/infra/tf's mobileServiceEnv is
      # the allow-list init's cover applies to this container, and the plan
      # refuses anything else.
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
      env {
        name  = "FUGARO_RTDB_URL"
        value = var.credentials_service.rtdb_url
      }
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = var.credentials_service.firebase_project
      }
      env {
        name  = "FUGARO_CREDENTIALS_SERVICE_URL"
        value = coalesce(var.credentials_service.url, local.credentials_service_url_unassigned)
      }
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

    # Every name above is var.credentials_service's own, so a hand-written or
    # stale tfvars that carries this file's switch and no such block has no
    # database, no Firebase project and no account to run as. That is a refusal
    # with a sentence rather than "attempt to get attribute from null value",
    # and it is the plan's and not the revision's: a public service that came up
    # without them would be an endpoint that admits a device and can verify
    # nothing.
    precondition {
      condition     = var.credentials_service != null
      error_message = "the mobile service is the credentials image in its mobile mode, and every name it is configured with — the Firebase project, the database URL, the image and the job table — is var.credentials_service's own members: write this service's block beside var.credentials_service, which every 'fugaro update' writes, and apply again."
    }
  }
}

# The public reach, and the whole of it: roles/run.invoker to allUsers, on this
# service and no other. It is the front door's arrangement (coordinator.tf) and
# the hop's (voice.tf), and it is written beside the service rather than inside
# it for the same reason: a public reach is an act a reviewer reads on the
# plan's own screen, and deploy/terraform/terraform_test.go names the three
# bindings of this tree an allUsers member is admitted on, by path and by block,
# so neither copying this resource under another name nor writing the member
# into another file passes. internal/infra/tf's cover carries no allUsers member
# — exactly as it carries none for the front door — so the update whose plan
# holds this binding is a step the run's confirmation does not cover: it shows
# the finding and is applied (D1545's content class), rather than passing as a
# plan somebody had already classified.
resource "google_cloud_run_v2_service_iam_member" "mobile_public" {
  count = local.credentials_service_on ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_service.mobile[0].location
  name     = google_cloud_run_v2_service.mobile[0].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# The runtime account's signJwt, on the device-only signer of the FIREBASE
# project (O1, O8). It is orchestrator.tf's cross-root row one account over: the
# account and the role belong to the Firebase root (modules/firebase/iam.tf,
# whose plan creates them), the row is bound on the signer ACCOUNT rather than
# on a project — which is what keeps it signJwt and not
# roles/iam.serviceAccountTokenCreator, whose holder could also mint access
# tokens as the signer — and the path is a literal of this file, because the
# credentials block carries no signer field and a name this module invented from
# a variable would be a second spelling of one the Firebase root already writes.
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
# fugaro-token-signer would make this public service the identity that mints
# every run's and every launcher's token, which is a reach O1 did not announce
# and no reviewer priced.
resource "google_service_account_iam_member" "mobile_token_minter" {
  count = local.credentials_service_on ? 1 : 0

  service_account_id = "projects/${var.credentials_service.firebase_project}/serviceAccounts/fugaro-device-signer@${var.credentials_service.firebase_project}.iam.gserviceaccount.com"
  role               = "projects/${var.credentials_service.firebase_project}/roles/fugaroTokenMinter"
  member             = google_service_account.mobile[0].member

  depends_on = [google_service_account.mobile]
}