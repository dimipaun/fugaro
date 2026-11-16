// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The manual queue (§4.5): the requests the checks send to a person, the
// notification that one is waiting, and the record of what the owner made of
// it.
//
// The queue is not a fallback for a check that failed; it is the surface the
// Terms give the requests the checks **cannot decide** — an individual without a
// domain address, a multi-seat trial, an academic or nonprofit application — so
// a queued request is not a refusal, and what the applicant is told about it is
// the uniform answer it already has rather than a second page that says which
// one it was (§7.1).

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// Decision is what a person made of one queued application. It is written when
// the owner answers one, and it is what makes the queue the requests nobody has
// looked at rather than every request ever made.
type Decision string

const (
	// Undecided is an application nobody has answered. It is the zero value and
	// it is what the page's queue reads.
	Undecided Decision = ""
	// Approved is an application the owner minted for. It is written after the
	// key is out, so a failure between the two leaves the request in the queue
	// where the owner can answer it again (§5.2's order, one step out).
	Approved Decision = "approved"
	// Declined is an application the owner refused. Nothing is minted and
	// nothing is mailed: the applicant's answer is the uniform one it already
	// has, and a second page saying otherwise is the lookup §7.1 refuses.
	Declined Decision = "declined"
)

// Alert is what a queued request notifies the owner with: a subject the service
// wrote and a body carrying what the owner has to see. It has Message's shape
// for Message's reason — nothing an applicant typed is a header (§7.2) — and it
// is a type of its own because the applicant's mail path and the owner's are two
// different doors: the manual form mails the applicant nothing at all, and a
// notification that went through Mailer would be one.
type Alert struct {
	Subject string
	Body    string
}

// queueSubject is a constant, so an applicant's text cannot reach the owner's
// notification as a header line any more than it can reach a mail's
// (TestQueueNotifiesTheOwner, §7.2).
const queueSubject = "A Fugaro licence request is waiting"

// Notifier tells the owner that a request is waiting to be decided (§4.5). It
// is required rather than optional: a queue nobody is told about is a queue
// nobody reads, and the request an individual without a company address is the
// one the Terms promise will be looked at.
type Notifier interface {
	Notify(ctx context.Context, alert Alert) error
}

// enqueue writes one request into the queue and tells the owner it is there.
//
// The notification is what makes a failed enqueue a failed request: a request
// written and never mentioned is one nobody will look at, and the applicant's
// form is the only place it existed.
func (s *Service) enqueue(ctx context.Context, a Application) error {
	if a.ID == "" {
		return errors.New("a queued request with no id is one the store cannot tell from another")
	}
	if a.At.IsZero() {
		a.At = s.now()
	}
	if err := s.store.SaveApplication(ctx, a); err != nil {
		return err
	}
	return s.alerts.Notify(ctx, queueAlert(s.cfg.URL, a))
}

// queue is every request nobody has answered, oldest first: the order the queue
// arrived in, which is the order a person works through it.
func (s *Service) queue(ctx context.Context) ([]Application, error) {
	held, err := s.store.Applications(ctx)
	if err != nil {
		return nil, err
	}
	open := make([]Application, 0, len(held))
	for _, a := range held {
		if a.Decision == Undecided {
			open = append(open, a)
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].At.Before(open[j].At) })
	return open, nil
}

// application reads one queued request by the id the page carries. The id is
// the record's own, and it is a secret rather than a counter, so a request's
// address and text are not reachable by guessing a number (§4.5, §7.3).
func (s *Service) application(ctx context.Context, id string) (Application, bool, error) {
	held, err := s.store.Applications(ctx)
	if err != nil {
		return Application{}, false, err
	}
	for _, a := range held {
		if a.ID == id {
			return a, true, nil
		}
	}
	return Application{}, false, nil
}

// decide writes what the owner made of one request. It mints nothing: an
// approval is the page's minting action, which writes the record §5.2's
// idempotency reads, and this is only the answer.
func (s *Service) decide(ctx context.Context, a Application, decision Decision) error {
	a.Decision = decision
	a.DecidedAt = s.now()
	return s.store.SaveApplication(ctx, a)
}

// queueAlert is what one queued request tells the owner. The applicant's own
// text is body text and never the subject, and the address the request is about
// is in it because the owner has to be able to tell one request from another
// without opening the page.
func queueAlert(intakeURL string, a Application) Alert {
	var body strings.Builder
	body.WriteString("A request is waiting in the queue.\n\n")
	body.WriteString("Address: " + a.Email + "\n")
	if a.Plan != "" {
		body.WriteString("Plan: " + a.Plan + "\n")
	}
	if a.Domain != "" {
		body.WriteString("Domain: " + a.Domain + "\n")
	}
	if a.Organisation != "" {
		body.WriteString("Organisation: " + a.Organisation + "\n")
	}
	if len(a.Repositories) > 0 {
		body.WriteString("Repositories: " + strings.Join(a.Repositories, ", ") + "\n")
	}
	body.WriteString("\n" + a.Detail + "\n\n")
	body.WriteString(strings.TrimSuffix(intakeURL, "/") + RouteAdmin + "\n")
	return Alert{Subject: queueSubject, Body: body.String()}
}
