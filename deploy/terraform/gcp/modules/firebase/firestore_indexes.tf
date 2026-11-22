# The run index's composite indexes (docs/console-fixes/contract-index.md,
# C-INDEX; plan task RI2). Firestore indexes every single field by itself, so a
# query ordered by one field needs nothing here; a query that also filters on an
# equality needs a composite index of its own, and a query Firestore cannot
# serve FAILS rather than answering slowly — so each row below is one of the
# reads `runs ls`, `runs stats`, `plan status` and `graph` make.
#
# One document per run in the `runIndex` collection, whose document id is the
# run id: that is why the sort key is `updated_at` descending and why Firestore
# appends the document name to every index below as its final key, which is what
# makes the order total.
#
# fugaro update applies these as the operator, and nothing else can: the
# history job, the index's only writer (D3201), holds datastore.user, which may
# write documents and may not change an index (iam.tf), and the CLI reads the
# index as the person who runs it (D3216). WHEN they are applied is the gate
# below: only once the database they belong to is there.
locals {
  # One entry per row of contract-index.md's table, in its order: the fields in
  # the order the query orders by them, each with the direction Firestore is
  # asked for, and the sort key last.
  run_index_indexes = {
    # one repository's runs, newest first: `runs ls`, `runs stats`.
    slug_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # the active set across the installation (D3203).
    status_updated_at = [
      { field_path = "status", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # a repository's runs in one state: `runs ls --state`.
    slug_status_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "status", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one plan task's runs: `plan status`, `fugaro plan graph`.
    plan_task_updated_at = [
      { field_path = "plan_task", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # a plan's runs in one repository: `plan status`.
    slug_plan_task_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "plan_task", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one pull request's runs, unbounded by repository (D3204).
    pr_updated_at = [
      { field_path = "pr", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one identity's runs, unbounded by repository.
    requested_by_updated_at = [
      { field_path = "requested_by", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one batch's runs, unbounded by repository.
    batch_updated_at = [
      { field_path = "batch", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one repository's pull request: `runs ls --pr`, which needs exactly one
    # repository.
    slug_pr_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "pr", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one identity's runs in the repositories a listing resolved:
    # `runs ls --mine`.
    slug_requested_by_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "requested_by", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
    # one batch's runs in the repositories a listing resolved:
    # `runs ls --batch`.
    slug_batch_updated_at = [
      { field_path = "slug", order = "ASCENDING" },
      { field_path = "batch", order = "ASCENDING" },
      { field_path = "updated_at", order = "DESCENDING" },
    ]
  }
}

# THE GATE: nothing here is planned until the database is there. `fugaro update`
# reads the FP's Firestore database before it applies this root and passes the
# answer in var.firestore_database_exists (internal/infra's FirestoreStep.
# DatabaseExists), and it ensures that database as the LAST step of the stage
# that applies this root (D3027). Without the gate, a fresh installation would
# plan eleven creates against a database that does not exist yet — the create is
# refused, the apply fails, the run never reaches the step that creates the
# database, and every later update fails the same way: `fugaro update` could
# never converge, which is the one thing it must always do. With it, such an
# installation plans no index, converges, and gains the indexes on the next
# update, which reads the database it has by then. An installation that already
# has its database — every one that has completed a setup — gets them in one
# update.
resource "google_firestore_index" "run" {
  for_each = var.firestore_database_exists ? local.run_index_indexes : {}

  project = var.project
  # The installation's one Firestore database: `fugaro update` ensures it (its
  # creation is an idempotent REST step, H8), and its name is fixed — the
  # index's own database field names where the index is built, and an index in
  # another database would serve no read this file is about.
  database   = "(default)"
  collection = "runIndex"

  # The same dependency every other resource in this module takes: the API that
  # serves the call is enabled by the apply this resource is planned in, and the
  # index is created after it.
  depends_on = [google_project_service.this]

  dynamic "fields" {
    for_each = each.value
    content {
      field_path = fields.value.field_path
      order      = fields.value.order
    }
  }
}
