// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The reminder route (portal plan P7.1): the door Cloud Scheduler's daily job
// posts to (P1.5's scheduler.tf, created paused and turned on by the deploy that
// ships this route).
//
// **The caller is the job and never a person.** Every other route under /api/
// reads one caller's own records; this one mails every organisation the
// deployment holds, so what may call it is the one account the deployment
// configured the job to run as, presenting a token minted for this service as
// its audience (P1.3's SchedulerVerifier, F33). A sign-in is refused like any
// other token that is not one: the route reads the Scheduler's document and does
// not reach the SPA's verifier at all, which is the whole of what "not a
// customer's door" means here.
//
// It answers nothing (204), like the switcher's own write: what a run did is in
// the log and in the mails, and the job retries a failed answer and nothing
// else. The refusal is the API's one shape, so a job misconfigured on the
// account or the audience reads one thing wherever it was pointed.

import (
	"errors"
	"log"
	"net/http"
)

const apiRemindersPath = "/api/reminders/run"

// reminderRunBytes is what the job's request may carry. A Cloud Scheduler job
// posts no body at all (scheduler.tf's http_target names a method and a URI),
// and this is what the route reads before it refuses a body larger than the
// service should hold — the mount's own cap is for the bodies an SPA posts, and
// this door is not one of those.
const reminderRunBytes = 1 << 10

func init() {
	registerAPI(http.MethodPost, apiRemindersPath, (*Service).apiRunReminders,
		withBody(reminderRunBytes), reaching(needDocs, needScheduler))
}

// apiRunReminders runs the day's reminders and answers nothing.
func (s *Service) apiRunReminders(w http.ResponseWriter, r *http.Request) {
	account, ok := s.schedulerCaller(w, r)
	if !ok {
		return
	}
	run, err := s.runReminders(r.Context())
	if err != nil {
		failAPI(w, err)
		return
	}
	// The account is the job's own and the counts are the run's (reminders.go):
	// what an operator reads here is who fired the run and what it mailed, and
	// neither is in a record the run wrote.
	log.Printf("keyservice: the reminder run as %s mailed %d renewal reminder(s) and %d binding reminder(s)",
		account, run.Renewals, run.Bindings)
	w.WriteHeader(http.StatusNoContent)
}

// schedulerCaller verifies the job's own token and answers the account it runs
// as. What it refuses, it refuses itself: a request with no bearer token is told
// to present one, and a token the Scheduler's verifier does not admit is refused
// as no sign-in at all rather than as an answer about the run — a caller that is
// not the job is told nothing about what the job does.
//
// A verifier that failed for its own reason (a key set it could not read) is the
// service's failure and not the caller's, for the reason apiCaller's is: the
// job's token may be perfectly good.
func (s *Service) schedulerCaller(w http.ResponseWriter, r *http.Request) (string, bool) {
	bearer, ok := portalToken(r)
	if !ok {
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", "This request carries no job token.")
		return "", false
	}
	account, err := s.scheduler.Verify(r.Context(), bearer)
	switch {
	case errors.Is(err, ErrUnverifiedSchedulerToken):
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", "This token is not one this route admits.")
		return "", false
	case err != nil:
		failAPI(w, err)
		return "", false
	}
	return account, true
}
