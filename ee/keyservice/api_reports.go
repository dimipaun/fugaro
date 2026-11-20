// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The report route (portal plan P5.2): the month of sales the operations team
// reads.
//
// **Why this is `/api/ops/reports` and not an ops table entry.** The SPA's half
// of the contract is `ee/portal/src/screens/ops/api.ts` — it asks for
// `/ops/reports?month=<month>` and draws `ReportMonth` (P5.3) — and api_ops.go
// says so where its own table stops: the month of reports is this area's route
// and this area's shape. What it shares with that table is the gate and not the
// file: operations' read rule (D2187), and the one audit line every operations
// read writes (D456).
//
// **A report is a month and not a range.** The month is the read's own query
// (`?month=YYYY-MM`, the same spelling a statement carries), so the SPA can ask
// for the month a person picked and the answer is keyed the way the ledger's own
// month read is. A month that is not one is `invalid_query` rather than an empty
// report: a month nobody bought in and a month spelled wrong are two different
// answers to a reader reconciling a quarter.
//
// **The read is the only act here.** A report changes nothing, so this area has
// one route and no body: nothing a caller sends can name whose money is in it,
// which is the operations organisation's and never the request's.

import "net/http"

const apiOpsReportsPath = "/api/ops/reports"

func init() {
	registerAPI(http.MethodGet, apiOpsReportsPath, (*Service).apiOpsReport, reaching(needDocs, needSignIn))
}

// apiOpsReport answers one month of sales, split by channel, as the operations
// screen reads it.
//
// The line is written between the gate and the read (operationsLine, D456): a
// caller who looked at a month's sales is a fact the log holds, and the query is
// validated first so that a typo is a refusal and not an entry in the log.
func (s *Service) apiOpsReport(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	month, ok := operationsMonth(r.URL.Query().Get("month"))
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionReportRead, nil, nil, &month) {
		return
	}
	held, err := s.reportMonth(r.Context(), month)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}
