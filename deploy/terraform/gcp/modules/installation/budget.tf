# An optional budget on the project; its alerts only email the billing account's
# administrators and users. Creating it needs billing-account permissions.
#
# The project's own record is read here for the budget's filter and for the
# front door's URL (coordinator.tf, which needs the project NUMBER: Cloud Run's
# deterministic service URL is built from it), so it is read when either of the
# two is on and not otherwise — an installation that has neither makes no call
# for it, which is what keeps a plan with no network a plan.
data "google_project" "this" {
  count = var.budget == null && !local.mcp_server_on ? 0 : 1

  project_id = var.project

  depends_on = [google_project_service.this]
}

resource "google_billing_budget" "this" {
  count = var.budget == null ? 0 : 1

  billing_account = var.budget.billing_account
  display_name    = "Fugaro"

  budget_filter {
    projects = ["projects/${data.google_project.this[0].number}"]
  }

  amount {
    specified_amount {
      currency_code = var.budget.currency_code
      units         = tostring(var.budget.amount)
    }
  }

  dynamic "threshold_rules" {
    for_each = var.budget.thresholds
    content {
      threshold_percent = threshold_rules.value
    }
  }

  depends_on = [google_project_service.this]
}
