# The resident orchestrator's own identity (mobile plan task P5.1b;
# docs/mobile/design/always-on-orchestrator.md section 4.3), and the whole of
# what it may do: the launcher's grants, assembled one binding at a time.
#
# What this file is NOT is as much of it as what it is. The account is
# deliberately NOT a member of var.launchers, which is the shape D146's
# credentials service took for the same reason one step over: the list is what
# the FIREBASE root grants `roles/firebasedatabase.viewer` to
# (modules/firebase/iam.tf's `viewer`, members local.readers), and that role is
# a project-level IAM read which BYPASSES the Realtime Database rules — so a
# launcher reads every mailbox of the installation without going through a
# rule, which is docs/mobile/functional-spec-reconciliation.md's G13 for the
# people on the list and would be a strictly wider reach for a service holding
# a phone's pairing key. Joining the list would also hand it
# `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`. The
# never-list's eleventh rule states it, orchestrator_test.go pins it (no
# resource in this module may name var.launchers), and the table below is the
# only way the account may get anything.
#
# Why every row is its own binding rather than one role or one list. A grant is
# the narrowest thing IAM can express, and each row of the design's table is a
# different reach with a different fence: the launcher's project role is
# metadata only, the token-minter grant is on ANOTHER project's account and is
# the widest of them all ("may be the phone" — section 4.5 E4), and the bucket's
# write is fenced by the launchers' own condition while its read is not fenced
# at all because `storage.objects.list` cannot be. A row that is not in the
# table is a defect rather than a rounding: the account launches runs, and
# every permission beyond that is one the design has not priced.
#
# The switch. `var.orchestrator` is null by default and this file creates
# NOTHING when it is — an installation that carries no block plans exactly what
# it planned before this file existed. `fugaro update` writes the block for an
# installation that can carry one (the owner's ruling of 2026-11-20), so the
# block is the installation's record that the update ran, and the account holds
# iam.serviceAccounts.signJwt on the budget signer from then on
# (docs/design/update-in-flight.md's row for the installation step). It is credentials_service.tf's and intake.tf's shape,
# one file over.
#
# The member string is WRITTEN OUT in every binding below rather than read off
# google_service_account.orchestrator[0].member, which is what a service
# account's other grants in this module do (state_read.tf's, coordinator.tf's).
# That attribute is computed, so reading it reaches the plan as an UNKNOWN
# member, and the member is the one thing every guard this file has rests on:
# internal/infra/tf's cover reads a principal's KIND off its address, and an
# unknown member of a plan that creates an account in the same module is an
# ordinary account (checkGrant's first case) — exactly the kind the design's
# eleventh never-rule keeps this account out of. Written from the account's own
# account_id and the project, the plan names the address, the cover reads it as
# toOrchestrator, and each binding below is held to section 4.3's table rather
# than to the table's toAccounts row. It is not a local either: the sweep that
# reads this tree (deploy/terraform/terraform_test.go) holds a member to its own
# reviewed list and says in as many words that a grant cannot hide behind a
# local.
#
# The token-minter row's ordering, stated here because it is the one row this
# file cannot make safe on its own: the signer account and the
# fugaroTokenMinter role belong to the FIREBASE root's project, and `fugaro
# update` applies the installation root BEFORE it applies Firebase. On an
# installation that already exists the signer is there and the binding is
# created; on an installation opting in on its very first apply the account
# does not exist yet and this binding fails that apply. The writer orders the
# two steps rather than this file: `fugaro update` writes var.orchestrator in
# the credentials-service stage, which runs AFTER the Firebase root
# (internal/cli's ensureOrchestratorBlock), and only for an installation whose
# config records the Firebase backend — so the block reaches this root only
# once the signer and its role exist, and an installation whose Firebase root
# has never been applied has no budget block and no Firebase outputs either,
# which is what the writer leaves it off for, with one printed line. The other way out
# stays open: moving this binding into modules/firebase, where the role it
# names is declared.
#
# What the account may never do is the design's section 6 and it is not
# expressible as a comment here: no IAM (nothing this file creates carries
# setIamPolicy or iam.roles.*), no managed apply (the runs bucket's root is
# outside the write condition below), no model credential, and no second
# installation's nodes (one project, one account).
#
# The ONE secret container this file names, and the one secretAccessor it
# grants, are the pairing key's cloud copy (P5.1e; the design's section 4.2 and
# its section 6 rule 5). They are not a widening of the row above: the tag is a
# symmetric HMAC, so the consumer that verifies a phone's messages in the cloud
# holds the key, and rule 5 states the copy as the exception the tag forces. The
# grant is the per-secret one on that one container — never the project-wide
# role, which dynamicSecretsGrant refuses for any account but the credentials
# service's — and section 4.3's reader table is what the note P5.1e writes in
# SECURITY.md says out loud: this account is a reader, and so is every project
# Owner or Editor, whose basic roles carry secretmanager.versions.access past
# any secret-level policy.

locals {
  # The switch, credentials.tf's and intake.tf's shape: a null variable is an
  # installation that has not opted in, and every resource below is counted on
  # it.
  orchestrator_on = var.orchestrator != null

  # The credentials-service invoker grant below needs BOTH switches, and it is
  # the one resource in this file that does. The account is this file's
  # (orchestrator_on), and the service it may call is credentials.tf's
  # (credentials_service_on): an installation whose credentials service is off
  # has no service to bind anything on, exactly as every grant in iam.tf and
  # build_identity.tf reads it. With either switch off the grant is not made,
  # which is the same no-op the whole file is (an installation that has not
  # opted in plans what it planned before this file existed).
  orchestrator_credentials_on = local.orchestrator_on && local.credentials_service_on

  # The route the tick calls, which is the process's ONLY route
  # (internal/cli/orchestrator_serve.go): the service answers POST /tick, 405 to
  # another method on it and 404 to anything else — so a tick pointed anywhere
  # else is a job that runs, fails and logs, every minute, forever. It is a local
  # because the URI is composed from the service's own URL, and it is pinned in
  # internal/infra/tf's cover_test.go against the route the case accepts.
  orchestrator_tick_path = "/tick"

  # The service's deterministic URL, <service>-<project number>.<region>.run.app,
  # which Cloud Run answers on beside the `.uri` attribute's
  # <service>-<hash>-<region code>.a.run.app form and accepts as an ID token's
  # audience. The tick is written from this one rather than from `.uri` because
  # the managed gate's cover can read a project number and a region out of this
  # form and nothing out of the hash (internal/infra/tf's orchestratorTickURI).
  # The audience is the URL without the route.
  orchestrator_url = local.orchestrator_on ? "https://${var.orchestrator.service}-${data.google_project.this[0].number}.${var.region}.run.app" : ""
}

# The identity itself. Nothing is granted actAs on it: executing the service
# needs none, and an operator who could deploy a new revision as it could widen
# the envelope this file is the whole of. The service's own revision is created
# by P5.1c, which is also the task that reads this account's email back
# (outputs.tf's orchestrator), so the name is published before the thing that
# runs as it exists.
resource "google_service_account" "orchestrator" {
  count = local.orchestrator_on ? 1 : 0

  project      = var.project
  account_id   = var.orchestrator.account_id
  display_name = "Fugaro resident orchestrator"
  description  = "The installation's resident orchestrator: launches this installation's runs and consumes this installation's mailbox. It holds a grant of its own, assembled one binding at a time, and is deliberately not a member of the launcher list."

  depends_on = [google_project_service.this]
}

# The pairing key's cloud copy (P5.1e; the design's section 4.2 and section 4.7
# step 2b): ONE container per installation, and one version per pairing inside
# it, written and destroyed by `fugaro pair` and `fugaro pair revoke` with the
# operator's own credential. Terraform creates the container and never a
# version, credentials.tf's rule for the same reason: a pairing key is drawn on
# the operator's machine and never reaches Terraform or its state
# (TestNoSecretVersionsInTerraform). A container with no version yet is fine —
# an installation that has opted in and paired nothing has nothing for the
# orchestrator to verify with.
#
# The labels are `fugaro=managed` and the installation's name, and deliberately
# NOT fugaro_secret, credentials.tf's reason exactly: that label is what
# `fugaro update`'s discovery reads to decide which logical secret of the
# project's secret set a container holds, and this container is not one of those
# — it is one version per pairing with two writers, neither of them Terraform.
#
# prevent_destroy is secrets.tf's rule: an edit that dropped the container must
# be a hard plan failure rather than a deletion of every pairing's key. The
# delete that ends a pairing is `fugaro pair revoke`'s, one version at a time.
resource "google_secret_manager_secret" "pairing_key" {
  count = local.orchestrator_on ? 1 : 0

  project   = var.project
  secret_id = "fugaro-orch-pairing"
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
  }

  # A container the CLI created is replicated in the installation's region, and
  # replication is creation-time only: a differing value here would plan a
  # replacement, which prevent_destroy refuses and which deletes every version
  # first. So the field is ignored below, and this block decides only a
  # container Terraform itself creates.
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [replication]
  }

  depends_on = [google_project_service.this]
}

# The reader, and the only one this file grants: the account above, on that one
# container. It is the per-secret row of section 4.3's table rather than the
# project-wide accessor, which is what the cover's own rule refuses for every
# principal but the credentials service's account. The member is written out
# like every other binding here and for the same reason: the account's `.member`
# is computed, and a computed member reaches the plan unknown.
resource "google_secret_manager_secret_iam_member" "orchestrator_pairing_key" {
  count = local.orchestrator_on ? 1 : 0

  project   = var.project
  secret_id = google_secret_manager_secret.pairing_key[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator]
}

# The launcher's project role: run.jobs.get/list, run.executions.get/list/
# cancel, run.operations.get and the two secret-list permissions. Metadata
# only — reading a secret's VALUE needs secretAccessor, which this account
# never gets (section 6 rule 5) — and it is what makes `fugaro ls`, `fugaro
# watch` and `fugaro cancel` work for a run the resident orchestrator started.
#
# The role is written as a LITERAL rather than as
# google_project_iam_custom_role.launcher.name, for state_read.tf's reason: a
# role resource's name is computed, so a reference reaches the plan as an
# UNKNOWN role and internal/infra/tf's cover could then admit any custom role
# of the plan. Written out, the plan-time string says exactly fugaroLauncher on
# this installation's project, and the cover pins the role's permission list by
# its ID (RolePermissions). `depends_on` is what orders the binding behind the
# role the literal names.
resource "google_project_iam_member" "orchestrator_launcher" {
  count = local.orchestrator_on ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroLauncher"
  member  = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator, google_project_iam_custom_role.launcher]
}

# The account's read of this installation's database (P5.1c's finding A). The
# tick reads orchestrator/<id>/{state,lease} out of the Realtime Database as
# this revision's own service account, and that read is an IAM one: the rules
# fence a client's write and a service-account bearer never passes them, so
# without a grant on the database the account reaches nothing at all and every
# tick answers 503 with the store's own words ("rtdb: GET
# /orchestrator/<id>/state: 401: Unauthorized request.").
#
# The role is state_read.tf's, and it is the ONE role this tree declares for
# reading the orchestrator's node — the front door and the voice hop read
# through it, and it holds firebasedatabase.instances.get and nothing else, so
# the account reads the whole instance and writes none of it. The binding names
# the project the role is declared in through state_read.tf's own local, the
# same expression the role and the other two bindings carry, so the grant can
# never name a project the role is not in. What the account reaches with the
# read is nothing new: it already holds iam.serviceAccounts.signJwt on the
# budget signer, which is "may be anyone" (section 4.5 E4), and what narrows
# this read to the node the tick needs is internal/orchestrator's own paths.
#
# The member is written out like every other binding in this file (the header
# says why), and this row lives here rather than beside the role in
# state_read.tf because section 4.3's table is this file's alone.
resource "google_project_iam_member" "orchestrator_state_read" {
  count = local.orchestrator_on ? 1 : 0

  project = local.state_read_firebase_project
  role    = "projects/${local.state_read_firebase_project}/roles/fugaroStateRead"
  member  = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator, google_project_iam_custom_role.state_read]
}

# The table's `run.jobs.run` and `run.jobs.runWithOverrides` row is the ONE row
# this file cannot carry, and it is one row of the table rather than a gap in
# it: the account must be able to start the installation's jobs, and the jobs
# are the installation root's project jobs, which this module does not create
# and cannot name. The grant is the root's own, beside the module call that
# makes them (deploy/terraform/gcp/roots/installation/main.tf,
# google_cloud_run_v2_job_iam_member.orchestrator_runner), and it reads the
# same custom role the project module already hands its launchers
# (module.installation.role_ids.job_runner), so the two grants of that role
# cannot drift apart.
#
# The launcher's write: runs/, tokens/ and issues/ of the runs bucket, under
# the launchers' own condition — the SAME title and expression every launcher
# is bound with, byte for byte, so the account's reach is the list's and not a
# spelling of it that happens to look similar. The claim, the task spec and the
# budget token live under runs/<slug>/; a launcher also writes its own launch's
# sealed budget token under tokens/ and its own issue under issues/ (D1727).
#
# What the condition withholds is the bucket root, `cache/`, `locks/` and
# `builds/` — the job accounts' and the build accounts' prefixes — which is
# section 6 rule 2's "no managed applies" held as an IAM fact rather than a
# promise: the managed marker is written at the bucket root, so an account that
# cannot write an object there cannot forge one. The precondition pins the
# condition to the bytes gcp.LauncherBucketCondition builds, as iam.tf's does,
# so a plan that widened the write area fails here rather than applying.
resource "google_storage_bucket_iam_member" "orchestrator_runs_launcher" {
  count = local.orchestrator_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator]

  condition {
    title      = var.launcher_bucket_condition.title
    expression = var.launcher_bucket_condition.expression
  }

  lifecycle {
    precondition {
      condition     = var.launcher_bucket_condition.title == "fugaro-launchers-runs-tokens-issues" && var.launcher_bucket_condition.expression == "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/\") || resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/issues/\")"
      error_message = "launcher_bucket_condition must be runs/, tokens/ and issues/ of the runs bucket, titled fugaro-launchers-runs-tokens-issues (gcp.LauncherBucketCondition)."
    }
  }
}

# The launcher's read, and unlike the write it carries no condition: listing a
# run needs `storage.objects.list`, and a condition on an object's NAME cannot
# grant a list, so a launcher lists the bucket root — `cache/`, `locks/` and
# `builds/` included. iam.tf's runs_reader says the same for the people on the
# list, and D683 is the answer that accepted it. Read, never write: the
# prefixes the condition withholds stay unwritable, which is the whole of what
# this row is for.
resource "google_storage_bucket_iam_member" "orchestrator_runs_reader" {
  count = local.orchestrator_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator]
}

# The token-minter row: `iam.serviceAccounts.signJwt` on the budget signer, which
# is the widest grant in the table and the design says so rather than pretending
# otherwise — "may mint" is exactly "may launch", but signJwt signs ANY claim
# set for ANY uid, so it is also "may be the phone and may be the consumer"
# (section 4.5 E4). SECURITY.md records the same limit for every launcher today;
# this design narrows neither and does not claim a narrow one.
#
# It is the one row of the table that names another root's project. The signer
# account and the fugaroTokenMinter role are the FIREBASE root's
# (modules/firebase/iam.tf), the row is bound on the signer ACCOUNT rather than
# on a project — which is what keeps it the signJwt role and not
# roles/iam.serviceAccountTokenCreator, whose holder could also mint access
# tokens as the signer — and the project and the account arrive as
# var.orchestrator's own fields rather than being derived here, because a name
# this module invented would be a second spelling of one `fugaro update`
# already computes.
#
# It is ALSO the one row in this file on a second switch, var.people_minter
# (launcher-does-not-mint plan task LM12, D3121, R8). That switch is the
# FIREBASE root's own name for the people's row — the launchers' and the
# operators' grant on this same signer (LM11) — and it is one name in both roots
# because the plan that moves minting to the credentials service turns both off
# at once: `fugaro update` applies both roots with the switch off once the
# service has proved it signs as this signer (LM14), and the people's row and
# this one are gone together. ON by default, so an installation that has not
# been asked to move its grant plans exactly what it planned before this switch
# existed; and it never gates the ISSUER's row, which is the plan's other half
# (modules/firebase/iam.tf's issuer_minter) — a switch that took that row away
# too would leave nobody able to mint a run's identity, which is the failure R8
# exists to avoid.
resource "google_service_account_iam_member" "orchestrator_token_minter" {
  count = local.orchestrator_on && var.people_minter ? 1 : 0

  service_account_id = "projects/${var.orchestrator.firebase_project}/serviceAccounts/${var.orchestrator.signer_account_id}@${var.orchestrator.firebase_project}.iam.gserviceaccount.com"
  role               = "projects/${var.orchestrator.firebase_project}/roles/fugaroTokenMinter"
  member             = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator]
}

# The service itself (mobile plan task P5.1c; the design's section 4.1: "one
# Cloud Run service per installation, in the installation's own GCP project,
# min-instances = 0, request-based billing, woken by a Cloud Scheduler tick").
# It is the account above, and the whole of what a wake-up may do is the table
# that account holds — this resource adds an identity to run AS, no reach of its
# own.
#
# It is a SERVICE and not a job, and the design refuses to pretend the choice is
# free: a scheduled job could do the same work and is cheaper (section 9's
# alternative A3), and the service is taken only because it can answer a
# request. Scale-to-zero is what makes "always on" mean available rather than
# busy: the phone's message waits in the mailbox for the whole 72-hour TTL
# (D118/O5) and the tick is what bounds the wait (U1), so the instance count has
# to be 0 when nothing is happening — check 660 reads `minScale` back off the
# live revision for exactly that, and `min-instances = 1` is the shape the
# design priced and did not take.
#
# The ingress is ALL and the fence is IAM, intake.tf's and credentials.tf's
# shape one file over: the caller is Cloud Scheduler over the public run.app
# URL, and `invoker_iam_disabled = false` is the front end refusing an
# unauthenticated request before the container runs. Those two together are what
# the plan's "no public ingress" means, and it is stated here rather than left to
# the provider's default: a service that does not require IAM is a service
# anybody on the internet can wake, and this one acts on an installation's own
# state.
#
# The image is the released pin, held to var.orchestrator's digest rule: the
# process is `fugaro orchestrator serve` (internal/cli/orchestrator_serve.go),
# a subcommand of the same binary the credentials service and the intake run,
# and there is no orchestrator image of its own. `args` replaces the image's
# command and no part of its entrypoint, so the two services running the same
# pin are told apart by their argv — which is what internal/infra/tf's case for
# this address reads.
#
# The environment is the process's whole configuration and it is six names,
# none of them a credential and none of them a secret (the process's own header
# counts them): the project that names this installation's node, the Firebase
# project that node belongs to, and the database URL it is read from — and,
# since P5.1d, the launch's own three: the GCP project the installation's jobs
# are in, the region they were deployed to, and the runs bucket's URL. There is
# deliberately no model key, no git credential and no secret ID here — section
# 6's rules 4 and 5, and the account's table has no secretAccessor for one to be
# read with. Nor is there a budget mode or a signer account: the launcher reads
# the Firebase project above and derives both (internal/infra's SignerAccountID,
# which the Firebase root's own outputs check), so a name an operator could set
# to another account's is not a field of this deployment.
resource "google_cloud_run_v2_service" "orchestrator" {
  count = local.orchestrator_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.orchestrator.service
  labels = {
    fugaro      = "managed"
    fugaro_role = "orchestrator"
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
      fugaro_role = "orchestrator"
    }
    service_account = google_service_account.orchestrator[0].email
    timeout         = "300s"

    scaling {
      min_instance_count = 0
    }

    containers {
      image = var.orchestrator.image
      args  = ["orchestrator", "serve"]

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "FUGARO_PROJECT"
        value = var.fugaro_project
      }
      env {
        name  = "FUGARO_FIREBASE_PROJECT"
        value = var.orchestrator.firebase_project
      }
      env {
        name  = "FUGARO_RTDB_URL"
        value = var.orchestrator.rtdb_url
      }
      # The launch's own three (mobile plan task P5.1d). The service starts the
      # runs the node records, and the launcher reads its backend, its region
      # and its runs bucket out of the config it builds from them: the project
      # the jobs are in and the region they were deployed to, and the URL of the
      # bucket the claim, the task, the launch record and the run's sealed budget
      # token live in. They are the same three names a run's own container
      # carries them under (internal/infra's GCPProjectEnv, RegionEnv and
      # BucketEnv), so what this service launches is a launch of the
      # installation's own jobs and never a second, differently-addressed one.
      # None of them is a credential: two are names, and the third is the bucket
      # the two bindings above already reach under runs/, tokens/ and issues/.
      # They are not a second spelling of anything above: the Firebase project is
      # where the database is, the GCP project is where the jobs are, and on an
      # installation whose database lives in another project the two differ.
      # internal/cli reads exactly this list as orchestratorServiceEnvNames, and
      # TestOrchestratorServiceEnvIsTheDeploymentsOwn holds the two to each other
      # in both directions.
      env {
        name  = "FUGARO_GCP_PROJECT"
        value = var.project
      }
      env {
        name  = "FUGARO_REGION"
        value = var.region
      }
      env {
        name  = "FUGARO_BUCKET"
        value = "gs://${var.runs_bucket}"
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

  depends_on = [google_service_account.orchestrator]
}

# The one caller the front end admits: the installation's own scheduler account,
# which is the identity the tick's Google ID token is minted as (the job below).
# Cloud Run's invoker is per service and there is nothing narrower (F36, the same
# sentence intake.tf's own invoker grant carries), so this is the whole of the
# class: nobody else in the installation, and no person, can wake this service.
#
# The member is the scheduler account's own attribute, as it is on the four other
# ticks this module creates (history.tf, coordinator.tf, reports.tf,
# catalogcheck.tf): the grant names the account this module makes rather than
# repeating its address, so the identity the tick mints its token as and the
# identity the front end admits cannot drift apart. On a plan that creates the
# account the attribute is unknown, and internal/infra/tf's cover reads an
# unknown member as an account of this run exactly when the module creates a
# service account with it (checkGrant's createsSA arm) — fugaro-scheduler is one
# of FugaroAccounts, the kind its run.invoker row admits, and that is also why
# this grant does not widen section 4.3's table: it is a grant TO the scheduler,
# and the orchestrator account appears nowhere in it.
resource "google_cloud_run_v2_service_iam_member" "orchestrator_invoker" {
  count = local.orchestrator_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.orchestrator.service
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member

  depends_on = [google_cloud_run_v2_service.orchestrator, google_service_account.orchestrator]
}

# The account's way INTO the credentials service (F19, O2 of
# docs/forge-labels and docs/outer-loop-escalation's E16): it is the one caller
# class of the service's label route and of its gated merge mint
# (credsvc.AuthConfig.OrchestratorAccounts, D2049), and Cloud Run's front end
# refuses a caller without roles/run.invoker before the container runs — so
# without this binding the daemon's token is minted and never served, which is
# the state that left both routes inert on every installation.
#
# It is a row section 4.3's table does not have, and it is added knowingly. The
# table is what this account may do on THIS installation's resources, and the
# design's twelfth envelope (section 6: no model credential, no second
# installation) is not touched by it: the service the grant names is the
# installation's own, its own container holds no credential in its environment,
# and what answers a call is the caller class — this account is named on
# FUGARO_ORCHESTRATOR_ACCOUNTS (credentials.tf) and on nothing else, so a caller
# whose token is not this account's is refused after the front end by
# credsvc's own class check. internal/infra/tf's cover reads the member as
# toOrchestrator and admits the cell on purpose (grantRules' run.invoker row);
# before this task Q13's answer of 2026-10-19 kept it out, on the premise that
# the orchestrator never calls the service at all.
#
# The member is written out rather than read off the account, like every other
# binding in this file (the header says why): a computed member reaches the plan
# as an unknown principal, which the cover reads as an ordinary account.
resource "google_cloud_run_v2_service_iam_member" "orchestrator_credentials_invoker" {
  count = local.orchestrator_credentials_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.credentials_service.service
  role     = "roles/run.invoker"
  member   = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_cloud_run_v2_service.credentials, google_service_account.orchestrator]
}

# The tick that wakes it (section 4.1; Q4's answer of 2026-10-19: the tick is
# the heartbeat's only writer, one write per tick, and Cloud Scheduler's shortest
# interval is one minute and it may fire up to that late — U1 says so, so a
# minute is the floor and the cadence this release takes).
#
# It is an HTTP target on the service's own URL, not the run API: a Cloud Run
# *service* is invoked through its URL, and that URL is what Cloud Run's IAM
# check reads `roles/run.invoker` against. The URL is the deterministic one
# (local.orchestrator_url, whose comment says why it is not the `.uri`
# attribute). The audience is that same URL without the route and not a second
# spelling of it: Cloud Run refuses an ID token whose audience is not the
# service's own URL, so the two come from one expression or the job
# authenticates against nothing.
#
# The token is an OIDC ID token, where every other tick in this tree carries an
# OAuth access token: the run API takes an access token, and a run.app URL takes
# an ID token whose audience is the service — an access token there is refused by
# the front end before the container runs, however the invoker grant reads.
# `service_account_email` is the scheduler account the invoker grant above names,
# and this file writes no actAs for it: every other tick in this module mints its
# token from the same account with no such grant (history.tf, coordinator.tf,
# reports.tf, catalogcheck.tf), so the Cloud Scheduler agent's own actAs on it is
# the installation's existing shape and not this task's to change.
#
# `retry_count = 0` is the module's rule for every tick in it: a failed pass logs
# its failure and the next minute is the retry, and a retry of a pass that
# half-ran is a second pass over the same node. The time zone is UTC as every
# other job's is. The region is var.orchestrator.scheduler_region, because Cloud
# Scheduler is not offered in every Cloud Run region.
resource "google_cloud_scheduler_job" "orchestrator_tick" {
  count = local.orchestrator_on ? 1 : 0

  project   = var.project
  region    = var.orchestrator.scheduler_region
  name      = var.orchestrator.scheduler_job
  schedule  = "* * * * *"
  time_zone = "Etc/UTC"

  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "${local.orchestrator_url}${local.orchestrator_tick_path}"

    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = local.orchestrator_url
    }
  }

  depends_on = [google_cloud_run_v2_service_iam_member.orchestrator_invoker]
}

# The operators' impersonation of the daemon's account, which is what lets the
# orchestrator daemon run on an operator's own machine. The daemon presents the
# credentials service with an ID token minted AS this account
# (internal/cli/orchestrator_identity.go: `gcloud auth print-identity-token
# --impersonate-service-account`), and minting it needs
# iam.serviceAccounts.getOpenIdToken on the account, which
# roles/iam.serviceAccountTokenCreator carries. Without this binding the daemon
# on the operator's machine is refused at its first call and the owner is told
# to grant it by hand, a second operator step that CLAUDE.md's HARD RULE forbids.
#
# What it lets an operator do, stated plainly: act as this account, which holds
# iam.serviceAccounts.signJwt on the budget signer and so may mint any identity
# in the installation (docs/mobile/design/always-on-orchestrator.md section 4.3,
# E4). The operators already hold the same signJwt themselves (the token-minter
# row on the people list), so the grant adds the account's other reaches, the
# launcher's grants and the pairing key's accessor, to what an operator can be.
#
# It is the verifier's and the deployer's shape: the account is the literal path
# built from var.project, because the resource's own attribute is unknown at plan
# time and a grant whose account nobody can name is one nobody can check. It is
# made to var.operators and to nobody else; the account itself is never added to
# any people list (orchestrator_test.go's TestOrchestratorIsNeverALauncher, which
# admits this one for_each and no other use of var.operators).
resource "google_service_account_iam_member" "fugaro_orchestrator_token_creator" {
  for_each = local.orchestrator_on ? toset(var.operators) : toset([])

  service_account_id = "projects/${var.project}/serviceAccounts/fugaro-orch@${var.project}.iam.gserviceaccount.com"
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = each.value

  depends_on = [google_service_account.orchestrator]
}

# The consumer's signing grant (mobile-identity-pairing plan task J6; O17): the
# whole of the account's reach on the installation's ONE Cloud KMS key, made in
# this file because section 4.3's table is this file's alone. The key, the ring
# and the role the account signs with are mailbox_key.tf's; what is here is the
# binding between them, the way every other row of that table is.
#
# Why the grant is the interesting half and the key is not. The role holds
# cloudkms.cryptoKeyVersions.useToSign alone (mailbox_key.tf), so the account
# can make a signature with a key and do nothing else with it — no material to
# read or export, no version to create or destroy. WHAT IT CANNOT SAY is which
# key, and the difference matters more than it looks: a grant on another key of
# the same project is a signature no phone of this installation verifies (they
# pinned the public half of this one, O16), made with material no step of this
# tree published and none of them can revoke. So crypto_key_id is the literal
# mailbox_key.tf composes rather than a reference to the key resource — a
# computed name reaches the plan as UNKNOWN and the cover could then admit a
# grant on any key of the project; local.mailbox_crypto_key_id is that literal,
# resolved at plan time — internal/infra/tf's mailboxSignerGrant
# refuses a grant of this role on another key, in another project, under a
# condition, or to an account that is not this installation's consumer, however
# a hand-edited plan spelled it. That is O17's "with signing granted to the
# mailbox consumer's account only" as a property of a PLAN.
#
# The member is this file's own literal, for the token-minter row's reason, and
# depends_on orders the binding behind the account, the key and the role the
# literals name: without the account's row, Terraform applies the two in
# parallel and this one fails with notFound when it runs first
# (deploy/terraform's TestStringNamedResourcesAreWaitedFor).
resource "google_kms_crypto_key_iam_member" "orchestrator_mailbox_signer" {
  count = local.orchestrator_on ? 1 : 0

  # No project argument: the binding's project is the key's, which
  # crypto_key_id names (the resource has no project field at this pin).
  crypto_key_id = local.mailbox_crypto_key_id
  role          = "projects/${var.project}/roles/fugaroMailboxSigner"
  member        = "serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"

  depends_on = [google_service_account.orchestrator, google_kms_crypto_key.mailbox_consumer, google_project_iam_custom_role.mailbox_signer]
}
