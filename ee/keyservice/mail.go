// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake's outbound mail: the verification link (§4.1) and the key itself.
// It is a seam rather than a client because the deploy's provider is S3.5's and
// because what §7.2's rule is about is exactly this: a Message is a recipient,
// a subject and a body, and nothing an applicant typed is any of the three.

import (
	"context"
	"fmt"
	"strings"
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

// Mailer is the intake's one route out. A failure to send is a failed
// application and not a silent one: the token is then a link nobody has.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// The two subjects are constants, so an applicant's text cannot reach a header
// through either (§7.2, TestApplicantFieldsCannotInjectMailHeaders).
const (
	verificationSubject = "Verify your address for a Fugaro licence key"
	keySubject          = "Your Fugaro licence key"
)

// verificationMail is the link that proves an address reads mail at it. The
// link is built from the intake's own origin and never from the request's Host
// header: a caller that sets Host cannot make the intake mail a link to
// somewhere else (§7.2).
func verificationMail(intakeURL, token string, ttl time.Duration) Message {
	return Message{
		Subject: verificationSubject,
		Body: "Someone asked for a Fugaro licence key for this address.\n\n" +
			strings.TrimSuffix(intakeURL, "/") + RouteVerify + "?token=" + token + "\n\n" +
			"The link works once, and it stops working " + humanTTL(ttl) + " from now.\n" +
			"If this was not you, nothing happens: no key is issued until the link is followed.\n",
	}
}

// keyMail carries the key. It is the only place a key text is written down
// outside the key ring: the store never holds one (§7.3).
func keyMail(to, key string) Message {
	return Message{
		To:      to,
		Subject: keySubject,
		Body: "Your Fugaro licence key:\n\n" + key + "\n\n" +
			"Install it in the console, or keep it for the CLI.\n",
	}
}

// humanTTL writes the period the link lives as the mail's own sentence about
// it, because the number is configuration and the mail should not read like one.
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
