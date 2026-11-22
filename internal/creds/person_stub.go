package creds

import "context"

// C1's stub reader: the other side of PersonSwitchReader, before a member's published policy is
// read at all. I1 deletes this file once WC6's real reader exists and the sides are wired; nothing
// on a production path names it, and it is what a test passes where no real reader is built yet.
//
// **It answers the one thing a side can be written against without a policy:** the switch is off for
// every project. That is not a placeholder answer — it is today's behaviour stated as a value, since
// absent is off (R9) and no member has the key today — which is what lets WC5 offer no personal
// candidate, WC7 ask for no grant and WC11 report the switch off, and lets a hermetic test drive
// every one of those paths with no member, no policy and no workspace.
//
// It is deliberately not a reader that could turn anything on, and it is deliberately the
// TIGHTER answer rather than an error: a stub that refused would make every caller of it test its
// failure path, and the failure path it would exercise is the one a policy that cannot be read
// takes — a refusal for the member's behalf, which is nothing the absent key ever means.
type StubPersonSwitch struct{}

var _ PersonSwitchReader = StubPersonSwitch{}

// PersonSwitchOn answers off for every project: see the file's own comment.
func (StubPersonSwitch) PersonSwitchOn(context.Context, string) (bool, error) {
	return false, nil
}
