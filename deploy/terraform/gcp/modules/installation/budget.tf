# An optional budget on the project; its alerts only email the billing account's
# administrators and users. Creating it needs billing-account permissions.
data "google_project" "this" {
  count = var.budget == null ? 0 : 1

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
