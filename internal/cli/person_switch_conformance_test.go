// This file holds the one check internal/projectpolicy cannot make about
// itself: that the reader it builds is the reader internal/creds declared.
//
// internal/projectpolicy must not import internal/creds — creds imports
// projectpolicy, so the edge the other way is a cycle — and Go's structural
// satisfaction means nothing there *breaks* if creds.PersonSwitchReader's
// method set moves and projectpolicy.PersonSwitchReader's does not: the
// assignment that fails would be at the one call site that wires the service,
// and a compile error there is the only warning until then. This file states
// the conformance where both packages can be named, and it is internal/cli
// because that is where the two may be named: creds' own boundary test keeps
// the credential state out of every directory but its allow-list
// (internal/creds/boundary_test.go, TestCredsIsNotReachableFromARun), and
// internal/projectpolicy is a run-side package.
package cli

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
