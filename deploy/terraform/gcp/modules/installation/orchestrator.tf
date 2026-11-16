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
# NOTHING when it is — an installation that has not opted in plans exactly what
# it planned before this file existed, which is what makes the update that
# carries this file a no-op for every installation that has not asked for a
# resident orchestrator (docs/design/update-in-flight.md's row for the
# installation step). It is credentials_service.tf's and intake.tf's shape,
# one file over.
#
# The token-minter row's ordering, stated here because it is the one row this
# file cannot make safe on its own: the signer account and the
# fugaroTokenMinter role belong to the FIREBASE root's project, and `fugaro
# update` applies the installation root BEFORE it applies Firebase. On an
# installation that already exists the signer is there and the binding is
# created; on an installation opting in on its very first apply the account
# does not exist yet and this binding fails that apply. Nothing can opt in
# today (the variable has no CLI writer, below), and the release that adds one
# either orders the two steps or moves this binding into modules/firebase,
# which is where the role it names is declared.
#
# What the account may never do is the design's section 6 and it is not
# expressible as a comment here: no IAM (nothing this file creates carries
# setIamPolicy or iam.roles.*), no managed apply (the runs bucket's root is
# outside the write condition below), no model credential (no secretAccessor,
# no secret container named anywhere in this file), and no second
# installation's nodes (one project, one account).

locals {
  orchestrator_on = var.orchestrator != null

  # The account's member string, written out here rather than read off
  # google_service_account.orchestrator[0].member. That attribute is computed,
  # so reading it would reach the plan as an UNKNOWN member, and the member is
  # the one thing every guard this file has rests on: internal/infra/tf's cover
  # reads a principal's KIND off its address, and an unknown member of a plan
  # that creates an account in the same module reads as an ordinary account
  # (checkGrant's first case) — which is exactly the kind the design's eleventh
  # never-rule keeps this account out of. Written from the account's own
  # account_id and the project, the plan names the address, the cover reads it
  # as this account's own kind, and the four bindings below are checked against
  # section 4.3's table rather than against a toAccounts row.
  #
  # The ternary is credentials.tf's shape and it is not decoration: the account
  # is counted, so the index exists only when the installation has opted in, and
  # the branch that would read it is not evaluated when it has not.
  orchestrator_member = local.orchestrator_on ? "serviceAccount:${google_service_account.orchestrator[0].account_id}@${var.project}.iam.gserviceaccount.com" : ""
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
  member  = local.orchestrator_member

  depends_on = [google_project_iam_custom_role.launcher]
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
  member = local.orchestrator_member

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
  member = local.orchestrator_member
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
resource "google_service_account_iam_member" "orchestrator_token_minter" {
  count = local.orchestrator_on ? 1 : 0

  service_account_id = "projects/${var.orchestrator.firebase_project}/serviceAccounts/${var.orchestrator.token_signer}"
  role               = "projects/${var.orchestrator.firebase_project}/roles/fugaroTokenMinter"
  member             = local.orchestrator_member
}
