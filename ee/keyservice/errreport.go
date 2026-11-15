// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The API's error reporting (portal plan P1.6): what a mounted route's failure
// is written down as, and where it goes.
//
// A door that fails silently is a door nobody fixes. Every door of the API's
// table is answered through this file, which reports the panic or the 5xx
// answer that came out of it and leaves every other answer alone: the SPA shows
// a person its own sentence about a failure, and this is the other half, the
// one an operator reads.
//
// What a report is not is the request. Sentry is a third party's service and the
// API's bodies are a customer's records, so a report carries the method, the
// route's own pattern — never the path a caller typed, which names their
// records — and the status, and no body, no header and no address (§7.2's rule,
// which api.go's own refusals follow too).
//
// A DSN is what turns reporting on, and a deployment without one reports
// nowhere: the failures of a deployment that named no host belong in its own
// log. sentry-go would otherwise read SENTRY_DSN out of the process's
// environment for a client built without one, which is a report to a host the
// deploy never named — TestNoDSNReportsNothing pins that no client is built at
// all without a DSN, and F24 is why the dependency is here.
//
// That log line is not a figure of speech. The recover in wrap is what takes
// net/http's own panic log away — the server recovers a panic nobody else did
// and writes it down itself, and a panic recovered here never reaches it — so a
// deployment reporting nowhere writes the panic to the process's log instead
// (the same method, pattern and stack a report would carry, and no more of the
// request than that). A failure is recorded somewhere on every path: in the
// deploy's Sentry when it named one, and in its log when it did not.

import (
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

const (
	// errReportTimeout bounds one report's send. The API is a Cloud Run
	// service, which is the one case sentry-go's synchronous transport is
	// written for (its own comment): an instance may be frozen the moment its
	// answer is written, so an event left to a background worker is an event
	// that may never leave. A report therefore holds the answer it belongs to,
	// and this is how long it may.
	errReportTimeout = 5 * time.Second

	// apiFailedCode and apiFailedMessage are what a panicking route answers:
	// one of the API's own refusals (api.go), because the SPA parses one shape
	// and a panic is not a reason for it to meet another. The sentence says
	// nothing about the inside, which is what §7.2's rule is.
	apiFailedCode    = "internal_error"
	apiFailedMessage = "This request could not be served."
)

// errReporter is where one deployment's API failures go. Its zero value is off,
// which is what a deployment with no DSN holds: report does nothing and no
// client exists to reach anything.
type errReporter struct {
	client *sentry.Client
}

// off answers whether this deployment reports anywhere at all, which is the
// state the wrapper keeps a panic's trace by: with a client the report is the
// record, and without one the process's own log is.
func (r errReporter) off() bool { return r.client == nil }

// newErrReporter builds where one Handler's failures go, from the deploy's DSN
// and environment.
//
// A DSN Sentry cannot read is refused here rather than at the first failure: a
// deployment whose DSN is malformed reports nothing, and the log line says so
// at start. It cannot refuse the start itself, and that is deliberate — the
// mount is built from the table every area registered and has no error to
// return, and a service that answered its customers while reporting nowhere is
// better than one that did not start (the same reading as Deps' optional
// seams, api.go).
func newErrReporter(dsn, environment string) errReporter {
	if strings.TrimSpace(dsn) == "" {
		return errReporter{}
	}
	transport := sentry.NewHTTPSyncTransport()
	transport.Timeout = errReportTimeout
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:         dsn,
		Environment: environment,
		Transport:   transport,
	})
	if err != nil {
		log.Printf("keyservice: the API reports failures to the DSN this deploy names, and Sentry cannot read it, so the API reports nowhere: %v", err)
		return errReporter{}
	}
	return errReporter{client: client}
}

// wrap answers one of the API's doors through the reporter: a route that
// panics is reported and refused, and one that answers 5xx is reported as its
// answer goes out. Nothing else is reported, which is every answer a caller
// asked for and got — a 404 and a 401 are the API working.
//
// A panic is recorded under either reading of "reporting is on": sent to the
// deploy's Sentry when it named a readable DSN, and written to the process's own
// log when it named none (or one Sentry refused), because the recover below is
// what stops net/http from logging it.
func (r errReporter) wrap(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		held := &apiAnswer{ResponseWriter: w}
		defer func() {
			crashed := recover()
			if crashed != nil {
				r.report(req, path, http.StatusInternalServerError, crashed)
				if held.status == 0 {
					if r.off() {
						// The recover here is what took net/http's own panic log
						// away: the server recovers a panic nobody else did and
						// writes it down, and it never sees this one. A
						// deployment reporting nowhere has to hear about the
						// panic somewhere, and this is the log its own comment
						// says such failures belong in. What is written is what
						// a report carries and no more — the method, the route's
						// pattern, the panic and the stack — so the request is
						// still never read for a body, a header or an address.
						log.Printf("keyservice: the route %s %s panicked, and this deployment reports nowhere: %v\n%s", req.Method, path, crashed, debug.Stack())
					}
					refuseAPI(w, http.StatusInternalServerError, apiFailedCode, apiFailedMessage)
					return
				}
				// The route had already answered when it panicked, and half an
				// answer written out as a whole one is worse than none: the
				// panic goes on to the server, which closes the connection
				// rather than let a client read a truncated body as the answer —
				// and which writes the panic to the process's log itself, so
				// this path needs none of the log line above.
				panic(crashed)
			}
			if held.status >= http.StatusInternalServerError {
				r.report(req, path, held.status, nil)
			}
		}()
		next(held, req)
	}
}

// report sends one failure. The request is read for its method and nothing
// else: the body, the headers and the address are what §7.2 keeps out of the
// API's answers, and a report reaches a third party rather than the caller.
func (r errReporter) report(req *http.Request, path string, status int, crashed any) {
	if r.off() {
		return
	}
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = req.Method + " " + path + " failed"
	event.Tags = map[string]string{
		"method": req.Method,
		"route":  path,
		"status": strconv.Itoa(status),
	}
	if crashed != nil {
		// A panic is the API's own bug rather than a request's, so it is fatal
		// and it carries the stack for the same reason. The panic's value is
		// the only thing here a route chose, and a route that panics with a
		// caller's data has put it in its own message — the reporter never
		// reaches for the request to find any.
		event.Level = sentry.LevelFatal
		event.Exception = []sentry.Exception{{
			Type:       "panic",
			Value:      fmt.Sprintf("%v", crashed),
			Stacktrace: sentry.NewStacktrace(),
		}}
	}
	r.client.CaptureEvent(event, nil, nil)
}

// apiAnswer is one route's answer as the reporter reads it: the status it went
// out with, which is what tells a failure from an answer.
//
// The status is the first WriteHeader's, which is the one a client reads: a
// second call is a route's bug and the net/http writer ignores it, so a report
// that followed the second call would name a status no caller ever saw. An
// answer whose body is written with no header at all is a 200, as net/http
// makes it.
type apiAnswer struct {
	http.ResponseWriter
	status int
}

func (a *apiAnswer) WriteHeader(status int) {
	if a.status == 0 {
		a.status = status
	}
	a.ResponseWriter.WriteHeader(status)
}

func (a *apiAnswer) Write(body []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	return a.ResponseWriter.Write(body)
}
