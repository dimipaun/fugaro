// Package projectpolicy_test holds the one check this package cannot make
// about itself: that its reader is the reader internal/creds declared.
//
// internal/projectpolicy must not import internal/creds — creds imports
// projectpolicy, so the edge the other way is a cycle — and Go's structural
// satisfaction means nothing here *breaks* if creds.PersonSwitchReader's
// method set moves and projectpolicy.PersonSwitchReader's does not: the
// assignment that fails would be in internal/credsvc, at the one call site
// that wires the service, and a compile error there is the only warning until
// then. This file states the conformance where both packages can be named, so
// the two sides of WC6's seam are held together by a test rather than by a
// coincidence of method names.
package projectpolicy_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The value the service, the launcher and doctor are handed is this one, and
// it has to be usable as creds' own interface without an adapter: an adapter is
// where a name and a meaning drift apart.
var _ creds.PersonSwitchReader = projectpolicy.PersonSwitchReader{}
var _ creds.PersonSwitchReader = projectpolicy.PersonSwitchReader{Read: func(context.Context, string) (*projectpolicy.Policy, bool, error) {
	return nil, false, nil
}}

// TestTheNodeNamesTheSwitchUnderCredsKey: the key is one spelling, not two.
// The node's member is read by the service through creds.PersonSwitchKey, and
// the member the launcher publishes is written by this package, so a rename on
// either side is a switch the service reads as absent and answers "off" to
// forever — with no error anywhere. Pinning the two together here is the only
// place both spellings can be named at once (the cycle is the reason the
// package cannot pin it in its own test).
func TestTheNodeNamesTheSwitchUnderCredsKey(t *testing.T) {
	on := true
	raw, err := json.Marshal(&projectpolicy.Policy{PersonalWorkspaceTokens: &on})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"` + creds.PersonSwitchKey + `":true`; !strings.Contains(string(raw), want) {
		t.Errorf("the node writes %s, want it to carry %s: the member's admin sets the switch through this key", raw, want)
	}
}
