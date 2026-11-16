# The secrets intake (D1931, DS2.2): the one writer of this installation's
# dynamic secret containers, and the reader half of the same fence.
#
# D1931 splits the platform credentials from the user's own. What stays in the
# bundle keeps Q6's one unconditioned grant (credentials.tf); what a user
# supplies for themselves lives in a container of its own, named by the
# internal/secretname grammar — fugaro_u_<sub>_<name> for one person,
# fugaro_p_<name> for the project — and the intake is the only identity that
# creates, adds versions to and deletes them. It cannot read one: the account
# holds no accessor and no secrets.get, so a caller reaches the intake with its
# Google ID token, writes a value into a container the intake never sees the
# contents of, and the credentials service reads it back through the
# prefix-conditioned accessor credentials.tf grants.
#
# OFF BY DEFAULT, like the credentials service and for the same reason
# (D147): with var.intake null this file creates nothing, no account, no role
# and no service. Nothing here reads a value, so nothing here carries a
# prevent_destroy: turning the intake off removes a service and an account and
# touches no container of anybody's.
#
# WHY THE GRANTS ARE TWO AND NOT ONE. The fence is a name-prefix condition on
# the resource name (F30), and the owner measured what it can and cannot hold
# on 2026-11-12 (Check 510, 0.23.0, exit 0): a conditioned
# secretmanager.secrets.create is ADMITTED for a fugaro_u_ name and REFUSED
# (403) for a name outside both prefixes, so create belongs inside the
# condition with version-add and delete — and secretmanager.secrets.list is
# REFUSED under that same condition, so the one call the intake's own routes
# are built on (internal/intake/gcp.go lists by label, because the account may
# not get a container's metadata by name) cannot be conditioned at all. An IAM
# binding's condition cannot be lifted for one permission of its role, so the
# list is a role of its own, unconditioned, holding nothing but
# secretmanager.secrets.list. Both roles are named here so that both halves of
# one fence are read in one place.

locals {
  # The whole file is gated on the one variable, like credentials.tf.
  intake_on = var.intake != null

  # The fence itself, written ONCE and carried by both halves: the intake's
  # write grant below and the credentials service's accessor. One expression
  # and not two (an IAM condition is a single CEL expression; two bindings
  # would be two fences that can drift), and the two prefixes are the ones
  # internal/secretname renders — the per-user prefix and the shared one.
  dynamic_secret_names_condition = {
    title      = "fugaro-intake-secret-names"
    expression = "resource.name.extract(\"/secrets/{secret}\").startsWith(\"fugaro_u_\") || resource.name.extract(\"/secrets/{secret}\").startsWith(\"fugaro_p_\")"
  }

  # The one object of the runs bucket the intake reads: the derived admin list
  # `fugaro update` publishes (DS2.0, access.PathAdminList). It is addressed by
  # name and never by listing: the intake holds no database role (D1940), so the
  # roster reaches it as a document, and a bucket-wide read would hand a
  # service account every run record the installation keeps.
  intake_admin_list_object = "admin/intake-admins.json"

  # The audience the first revision carries before the update stage reads the
  # real URL back (credentials.tf's own approach, and the same reasoning): this
  # host is under the reserved .invalid TLD (RFC 2606), it is not a run.app URL,
  # and no ID token is ever minted for it.
  intake_url_unassigned = "https://intake-url-unassigned.invalid"

  # Who may invoke the two services this fence belongs to: the roster's
  # launchers AND its operators, as the members IAM takes. Cloud Run grants the
  # invoker per service and there is no narrower scope than the service, so a
  # grant here admits its member to every route of it; the class check inside
  # each process (FUGARO_LAUNCHER_ACCOUNTS, FUGARO_PERSON_ACCOUNTS) is what
  # decides who is served after that (F36). The launchers are here because a
  # launcher launches runs and asks for secrets (F31: until this task the
  # credentials service admitted the operators alone, so an installation whose
  # roster named a launcher who was not an operator had a caller IAM refused
  # before any process could refuse it). A group or a domain member is left
  # out for the reason the class lists are: no ID token carries one as its
  # email claim, so the grant would be a door no class could open.
  launcher_invoker_members = toset([for m in var.launchers : m if startswith(m, "user:")])
}

# The intake's own account. It holds no key, no bundle and no database role:
# the two roles below and the one object read are its whole reach, and the
# service runs as it.
resource "google_service_account" "intake" {
  count = local.intake_on ? 1 : 0

  project      = var.project
  account_id   = var.intake.account_id
  display_name = "Fugaro secrets intake"

  lifecycle {
    precondition {
      condition     = var.intake != null
      error_message = "the intake's account needs var.intake"
    }
  }

  depends_on = [google_project_service.this]
}

# The intake's write half: create, version-add and delete, and nothing else.
# All three are conditioned by name in the binding below, so the account can
# touch a container of the intake's grammar and no other, whatever the process
# does with a request. There is deliberately no secretmanager.secrets.get and no
# secretmanager.versions.access here: the intake writes values it never reads
# back (R3), and a read permission on this role would be a reader the fence
# does not cover and no test of this task would expect.
resource "google_project_iam_custom_role" "intake_secrets" {
  count = local.intake_on ? 1 : 0

  project = var.project
  role_id = "fugaroIntakeSecrets"
  title   = "Fugaro secrets intake (the conditioned writes)"
  permissions = [
    "secretmanager.secrets.create", # the container, named by the caller's own sub (R10, R12)
    "secretmanager.secrets.delete", # the container and every version in it, on a removal the owner asked for
    "secretmanager.versions.add",   # the value, as a new version, never an update of an existing one
  ]
}

# The list half, and a role of its own for the reason this file's header gives:
# a name condition refuses secretmanager.secrets.list outright (Check 510), so
# the one call internal/intake/gcp.go makes to find containers by their labels
# cannot ride the conditioned role. It is shared with the credentials service
# (credentials.tf's lister binding), so one permission has one home and the two
# services that must list hold the same thing: the names and metadata of the
# project's secrets, and never a value.
#
# Both switches are in the count rather than the intake's alone, because the
# credentials service binds this role too and a role that is not created is a
# grant Terraform cannot make (the two are turned on independently).
resource "google_project_iam_custom_role" "secret_lister" {
  count = local.intake_on || local.credentials_service_on ? 1 : 0

  project = var.project
  role_id = "fugaroSecretLister"
  title   = "Fugaro dynamic secrets (names and metadata only)"
  permissions = [
    "secretmanager.secrets.list", # the names, labels and annotations of the project's containers — never a version and never a payload
  ]
}

# ... and the account's one conditioned binding. The role is spelled LITERALLY
# and not read from the role resource's own name attribute: a custom role's
# name is computed during apply, so the reference reaches the plan as an UNKNOWN
# role and internal/infra/tf's cover cannot then say which role the grant is
# (credentials.tf's rule for the database role, and deployer.tf's before it).
resource "google_project_iam_member" "intake_secrets" {
  count = local.intake_on ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroIntakeSecrets"
  member  = google_service_account.intake[0].member

  condition {
    title      = local.dynamic_secret_names_condition.title
    expression = local.dynamic_secret_names_condition.expression
  }

  depends_on = [google_project_iam_custom_role.intake_secrets]
}

# The list, unconditioned, in the account's second binding. It answers metadata
# for the whole project and no value: the container's own name, labels,
# annotations and create time are what the intake's routes report, and a caller
# that may list learns nothing it could authenticate with (F30, Check 510).
resource "google_project_iam_member" "intake_lister" {
  count = local.intake_on ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroSecretLister"
  member  = google_service_account.intake[0].member

  depends_on = [google_project_iam_custom_role.secret_lister]
}

# The derived admin list, and the intake's only reach outside Secret Manager:
# ONE object of the runs bucket (access.PathAdminList). The condition is what
# makes it one object — an unconditioned objectViewer would read every run
# record, prompt and transcript the installation keeps — and the read is by
# name, so no storage.objects.list is needed here (iam.tf's runs_reader states
# the case that does need one: listing cannot be expressed as a name
# condition). var.runs_bucket is the bucket's NAME, known when this root is
# planned, which is what lets the expression be a plan-time string.
resource "google_storage_bucket_iam_member" "intake_admin_list" {
  count = local.intake_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.intake[0].member

  condition {
    title      = "fugaro-intake-admin-list"
    expression = "resource.name == \"projects/_/buckets/${var.runs_bucket}/objects/${local.intake_admin_list_object}\""
  }
}

# The service itself. The image carries the digest pin the credentials service's
# does (var.intake's validation): it runs as the account above, and the registry
# grants roles/artifactregistry.writer to every operator (iam.tf), so a mutable
# tag would let an operator push the code that writes an installation's
# credentials for it.
#
# It requires IAM like the credentials service: no allUsers, no
# allAuthenticatedUsers, and invoker_iam_disabled stated rather than left to the
# provider's default — the front end refuses an unauthenticated request before
# the container runs. The ingress is ALL for the same reason the credentials
# service's is: the caller is a person's own machine or a Cloud Run job of this
# project over the public run.app URL, and the fence is the IAM check, not the
# network. The class check inside the process is the second fence, and the only
# one that can be finer than the service (F36).
#
# The port is unnamed on purpose: this process serves HTTP/1.1 and carries no
# large body, so it needs nothing of credentials.tf's h2c dance.
resource "google_cloud_run_v2_service" "intake" {
  count = local.intake_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.intake.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "intake"
  }
  ingress = "INGRESS_TRAFFIC_ALL"

  invoker_iam_disabled = false

  scaling {
    scaling_mode       = "AUTOMATIC"
    min_instance_count = 0
  }

  deletion_protection = false

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "intake"
    }
    service_account = google_service_account.intake[0].email
    timeout         = "300s"

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.intake.image

      # The subcommand of the released image, and the one thing that tells this
      # service from the credentials service running the same pin: that image's
      # entrypoint is the binary and its default command is `credentials serve`,
      # so without these args this container would serve the other service's
      # routes as the intake's account. `args` replaces the image's command and
      # no part of its entrypoint, so the process is `fugaro intake serve`
      # (internal/cli/intake_serve.go) — there is no intake image of its own,
      # and no release builds one.
      args = ["intake", "serve"]

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # The process's whole configuration (internal/cli/intake_serve.go reads
      # exactly these names). None of them is a credential, and none is a secret
      # ID: the intake addresses containers by the caller's own prefix and
      # derives every name from it.
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      # The service's own URL, which is the audience a caller's Google ID token
      # is minted for — the one thing that binds such a token to THIS service.
      # Cloud Run assigns it, so it is a variable the update stage fills from
      # this module's output (outputs.tf), and the unassigned case is the
      # reserved .invalid host below: the first revision starts, is healthy and
      # admits nobody, rather than refusing to start and failing the very apply
      # that would have produced the URL it needs.
      env {
        name  = "FUGARO_INTAKE_URL"
        value = coalesce(var.intake.url, local.intake_url_unassigned)
      }
      env {
        name  = "FUGARO_RUNS_BUCKET"
        value = var.runs_bucket
      }
      # The launcher class (D1938, F25), the same list the invoker grant above
      # is made from: rendered only when the roster names somebody, so an
      # installation whose roster names no launcher starts with a class that
      # admits nobody (fail closed) instead of refusing to bind a port.
      dynamic "env" {
        for_each = length(var.launcher_accounts) == 0 ? [] : [jsonencode(var.launcher_accounts)]
        content {
          name  = "FUGARO_LAUNCHER_ACCOUNTS"
          value = env.value
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

# The invoker grant, one per launcher: the front end admits them, and the
# process's own class check decides who is served (F36 — Cloud Run's invoker is
# per service and there is nothing narrower). On the service name from the
# variable rather than the resource, so the grant is readable in a plan.
resource "google_cloud_run_v2_service_iam_member" "intake_invoker" {
  for_each = local.intake_on ? local.launcher_invoker_members : toset([])

  project  = var.project
  location = var.region
  name     = var.intake.service
  role     = "roles/run.invoker"
  member   = each.value
}
