package cli

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestTheMemberRulesAgreeEntryForEntry is the cross-check W5 needs and neither
// side can make alone. Two files decide what a member entry means, in two
// packages that cannot call each other: `fugaro user`'s userMember (user.go) is
// the rule for what may be WRITTEN to terraform.launchers and
// terraform.operators, and projectpolicy's own memberUserEntry (members.go) is
// the rule for which entries name one person when the workspace's admission
// READS them. A spelling one of them changes alone is a member the operator
// wrote and no workspace admits — or, in the other direction, an entry a
// workspace would admit that this command refuses to write, which is the hole
// F42 is about (a domain: entry handing every account in a domain the launcher
// role, and with it the token signer's own, F44).
//
// The refusals themselves are TestUserAddRefusesDomain's to pin. What this test
// pins is that the two rules are the same rule: every spelling below is either
// one person to both of them or nobody to both.
func TestTheMemberRulesAgreeEntryForEntry(t *testing.T) {
	for _, member := range []string{
		"user:person-1@example.com",
		"user:Person-1@example.com",
		"user:person-1@EXAMPLE.COM",
		"domain:example.com",
		"Domain:example.com",
		"group:eng@example.com",
		"serviceAccount:bot@proj-1234.iam.gserviceaccount.com",
		"allUsers",
		"allAuthenticatedUsers",
		"USER:person-1@example.com",
		"User:person-1@example.com",
		" user:person-1@example.com",
		"user:person-1@example.com ",
		"user:person-1@example.com ",
		"user:*@example.com",
		"user:person-1@example.com*",
		"user:",
		"user:person-1",
		"user:Person One <person-1@example.com>",
	} {
		_, err := userMember(member)
		written := err == nil
		named := len(projectpolicy.MemberListsOf([]string{member}, nil).Addresses()) == 1
		if written != named {
			t.Errorf("%q is %v to fugaro user (err %v) and %v to the member's own admission: the writing rule and the reading rule are one rule, and a spelling only one of them reads is a member who is admitted without being written, or written and never admitted",
				member, map[bool]string{true: "one person", false: "refused"}[written], err, map[bool]string{true: "one person", false: "inert"}[named])
		}
	}
}

// TestTheListsFugaroUserWritesAreTheOnesTheAdmissionReads takes the lists off
// this installation the way each reader does: the file the command wrote (the
// two lists a member's admission is read from) and the `ls --json` document
// (the same two lists as this program reports them). Both must name exactly the
// people the command put there, and nobody else.
func TestTheListsFugaroUserWritesAreTheOnesTheAdmissionReads(t *testing.T) {
	r := userRigFor(t, []string{"user:person-1@example.com"}, nil)
	if _, _, err := userCommand(t, "add", "user:person-2@example.com", "--role", "operator"); err != nil {
		t.Fatalf("fugaro user add: %v", err)
	}
	launchers, operators := r.lists(t)
	want := []string{"person-1@example.com", "person-2@example.com"}
	if got := projectpolicy.MemberListsOf(launchers, operators).Addresses(); !slices.Equal(got, want) {
		t.Errorf("the lists this installation holds are %v / %v and the member's admission reads them as %v, want %v", launchers, operators, got, want)
	}

	js, _, err := userCommand(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc userListDoc
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("the --json document: %v\n%s", err, js)
	}
	if got := projectpolicy.MemberListsOf(doc.Launchers, doc.Operators).Addresses(); !slices.Equal(got, want) {
		t.Errorf("the document names %v / %v and the member's admission reads them as %v, want %v", doc.Launchers, doc.Operators, got, want)
	}
}
