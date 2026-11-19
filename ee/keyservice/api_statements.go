// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The statement routes (portal plan P6.3): the monthly run's door, the
// reseller's own read, and operations' record of money arriving.
//
// **Three routes, three callers, and each route carries the gate its own act
// needs** (api_resellers.go's own rule: a route is gated by the rule it needs
// and not by the prefix it carries). The run is the job's — P1.5's scheduler.tf
// posts to it once a month, and P7.1's reminders share the door: F33's OIDC
// token, and the sign-in verifier is never reached. The read is the reseller
// organisation's own, which is what `resellerCaller` asks and what no part of a
// request can name. The payment is operations' alone (D2190: only a person sees
// the money arrive), so it is gated the way the rest of operations' writes are —
// the role, and the fresh sign-in D2187 asks for.
//
// **The statement a payment is for is the path's.** It is not a field of the
// body, which is what makes a payment one statement's and not a transfer a
// request could name twice (statements.schema.json's own sentence).
//
// A run answers nothing (204), like the reminder run's door: what it did is in
// the log and in the mails, the job retries a failed answer and nothing else,
// and a body of counts would be a second thing to keep true.

import (
	"errors"
	"log"
	"net/http"
)

const (
	apiStatementsPath        = "/api/statements"
	apiStatementsRunPath     = "/api/statements/run"
	apiStatementPaymentsPath = "/api/statements/{id}/payments"
)

// statementRunBytes is what the job's request may carry, for the reason
// reminderRunBytes is: a Cloud Scheduler job posts no body at all (scheduler.tf's
// http_target names a method and a URI), and this is what the route reads before
// it refuses a body larger than the service should hold.
const statementRunBytes = 1 << 10

// maxStatementsBodyBytes is what a payment may carry: a reference is text a
// person typed, and the reseller area's own bodies are the same cap.
const maxStatementsBodyBytes = 4 << 10

func init() {
	registerAPI(http.MethodGet, apiStatementsPath, (*Service).apiStatements,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiStatementsRunPath, (*Service).apiRunStatements,
		withBody(statementRunBytes), reaching(needDocs, needScheduler))
	registerAPI(http.MethodPost, apiStatementPaymentsPath, (*Service).apiRecordStatementPayment,
		withBody(maxStatementsBodyBytes), reaching(needDocs, needSignIn))
}

// apiStatements answers the caller's own reseller organisation's statements,
// newest month first, which is what the reseller's screen draws (P6.4).
func (s *Service) apiStatements(w http.ResponseWriter, r *http.Request) {
	_, held, ok := s.resellerCaller(w, r)
	if !ok {
		return
	}
	statements, err := s.resellerStatements(r.Context(), held.Organisation.ID)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusOK, statements)
}

// apiRunStatements issues the month's statements and answers nothing.
func (s *Service) apiRunStatements(w http.ResponseWriter, r *http.Request) {
	account, ok := s.schedulerCaller(w, r)
	if !ok {
		return
	}
	run, err := s.runStatements(r.Context())
	if err != nil {
		failAPI(w, err)
		return
	}
	// The account is the job's own and the counts are the run's (statements.go):
	// what an operator reads here is who fired the run and what it issued and
	// mailed, and none of that is in a record the run wrote.
	log.Printf("keyservice: the statement run as %s issued %d statement(s) and mailed %d message(s)",
		account, run.Issued, run.Mailed)
	w.WriteHeader(http.StatusNoContent)
}

// apiRecordStatementPayment records money operations was told arrived against
// one statement, and answers the statement as it stands: the two figures, the
// state and the instant settled, which is what the screen shows next to the
// month.
func (s *Service) apiRecordStatementPayment(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	var asked WireStatementPayment
	if !operationsBody(w, r, &asked) {
		return
	}
	held, err := s.recordStatementPayment(r.Context(), actor, r.PathValue("id"), asked)
	if err != nil {
		refuseStatement(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// refuseStatement maps this area's two refusals and hands everything else to the
// one operations answers with, which is where the gates' own failures are mapped
// — a caller who is not operations, and a sign-in too old to change anything
// (D2187).
func refuseStatement(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchStatement):
		refuseAPI(w, http.StatusNotFound, "not_found", "No statement in this portal has that id.")
	case errors.Is(err, errStatementBadPayment):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_payment",
			"A payment is an amount of one micro or more and a reference a person can be given. Neither may be zero or empty.")
	default:
		refuseOperations(w, err)
	}
}
