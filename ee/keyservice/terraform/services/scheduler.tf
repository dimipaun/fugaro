# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The portal's two scheduled runs: the monthly statements (P6.3) and the renewal
# and binding reminders (P7.1). Both are created PAUSED, which is the plan's word
# (P1.5): the routes they call belong to those later tasks, so a job that fired
# before them would answer 404 at best, and an owner turns each one on with the
# deploy that ships its route — a mail over live data is not something an apply
# starts.
#
# Each call carries an OIDC token minted for the scheduler's own account with the
# API's URL as its audience: the API verifies exactly that pair (P1.3, F33), so a
# token from another account is refused and one minted for another audience is
# refused too. That account is the whole of why it exists, and it holds no IAM
# role in this project: it calls a public service, and the check it passes is the
# API's own rather than the platform's.
#
# Neither job calls the minter, and neither names it: the minter's one caller is
# the API (Check 571).

resource "google_service_account" "scheduler" {
  project      = var.project
  account_id   = var.scheduler_account
  display_name = "Fugaro portal scheduler"

  description = "Mints the scheduled calls' OIDC tokens. The API verifies this account and its audience, and it holds no IAM role anywhere (P1.3, F33)."

  depends_on = [google_project_service.this]
}

# The first of the month, early in the UTC morning: the month's statements are
# computed and mailed once, so the run is monthly and the day is the first.
resource "google_cloud_scheduler_job" "statements" {
  project     = var.project
  region      = var.region
  name        = var.statement_job
  description = "The monthly statements run: posts to the API's statement route, which mails each reseller its month (P6.3). Paused until the deploy ships that route."
  schedule    = "0 3 1 * *"
  time_zone   = "Etc/UTC"
  paused      = true

  retry_config {
    retry_count = 3
  }

  http_target {
    http_method = "POST"
    uri         = "${google_cloud_run_v2_service.api.uri}${var.statement_job_path}"

    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = google_cloud_run_v2_service.api.uri
    }
  }
}

# Daily, because what it looks for is a date: a key Paddle does not renew runs out
# on its own day, and an unpaid binding comes due on its own day.
resource "google_cloud_scheduler_job" "reminders" {
  project     = var.project
  region      = var.region
  name        = var.reminder_job
  description = "The renewal and binding reminder run: posts to the API's reminder route, which mails the owners whose keys or bindings come due (P7.1). Paused until the deploy ships that route."
  schedule    = "0 7 * * *"
  time_zone   = "Etc/UTC"
  paused      = true

  retry_config {
    retry_count = 3
  }

  http_target {
    http_method = "POST"
    uri         = "${google_cloud_run_v2_service.api.uri}${var.reminder_job_path}"

    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = google_cloud_run_v2_service.api.uri
    }
  }
}
