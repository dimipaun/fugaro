// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake's outbound mail: the key itself. It is a seam rather than a client
// because the deploy's provider is S3.5's and because what §7.2's rule is about
// is exactly this: a Message is a recipient, a subject and a body, and nothing an
// applicant typed is any of the three.

import (
	"context"
	"fmt"
	"time"
)

// Message is one outbound message as the intake built it: the headers and the
// body, never a rendered wire message. To is the address the intake verified,
// Subject is the service's own constant, and everything an applicant typed
// lives in Body as text.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer is the intake's one route out. A failure to send is a failed request
// and never a silent one: a key nobody receives is a key nobody holds, and an
// invitation nobody receives is one nobody can accept.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// The subject is a constant, so an applicant's text cannot reach a header
// through it (§7.2, TestApplicantFieldsCannotInjectMailHeaders).
const keySubject = "Your Fugaro licence key"

// humanTTL writes the period a link lives as the mail's own sentence about it,
// because the number is configuration and the mail should not read like one.
func humanTTL(ttl time.Duration) string {
	hours := int(ttl.Hours())
	if hours <= 1 {
		return "an hour"
	}
	if hours < 48 {
		return fmt.Sprintf("%d hours", hours)
	}
	return fmt.Sprintf("%d days", hours/24)
}
