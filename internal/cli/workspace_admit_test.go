package cli

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/mobileauth"
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

// TestTheAdmissionIsStricterThanWhatFugaroUserWrites pins a divergence between
// the two rules THE ADMISSION applies, so that it stays a decision rather than
// becoming an accident.
//
// `fugaro user`'s userMember and the admission's own entry reader accept these
// spellings: they are one person written as `user:<address>`, which is all
// either of them asks. The address rule the admission then compares with
// (mobileauth.NormalizeEmail, in `ee/coordinator/oauth`'s admission —
// TestAdmitRefusesAListEntryTheAddressRuleCannotRead is its other half) refuses
// every one of them, so the entry admits nobody at any workspace sign-in. The
// cost is an operator who writes one and waits for a person who can never get
// in; the address rule cannot be loosened for them (its own doc says why: an
// address two writers could spell two ways is the last place to guess), and the
// entry is still this installation's own launcher or operator, which is what
// `terraform.launchers`/`operators` are applied as.
//
// Two ways to close it, and both are the owner's call rather than this task's:
// userMember could apply mobileauth.NormalizeEmail and refuse these spellings at
// the door — which would also refuse a launcher whose address is non-ASCII or
// not a dotted domain, people this installation can legitimately run for
// without any workspace — or the divergence stands and this test is what says
// so. Until that call, what it pins is that a spelling is either written and
// inert (here) or refused at one of the two doors — the display-name form
// TestTheMemberRulesAgreeEntryForEntry carries is refused by the entry rule
// before the address rule ever sees it.
func TestTheAdmissionIsStricterThanWhatFugaroUserWrites(t *testing.T) {
	for _, member := range []string{
		"user:person-1@localhost",
		"user:person-1@exämple.com",
		"user:person-1@example.com.",
		"user:a@b@c",
	} {
		if _, err := userMember(member); err != nil {
			t.Errorf("%q is refused by fugaro user (%v), and this test pins it as written: if the command has been tightened to apply the address rule, this case belongs in TestTheMemberRulesAgreeEntryForEntry instead", member, err)
		}
		named := projectpolicy.MemberListsOf([]string{member}, nil).Addresses()
		if len(named) != 1 {
			t.Errorf("%q names %v to the admission's reader, and it is one person's address", member, named)
			continue
		}
		if _, err := mobileauth.NormalizeEmail(named[0]); err == nil {
			t.Errorf("%q is an address the address rule can read: it is no longer inert at a workspace sign-in, and this test's premise (written, and never admitted) is gone", member)
		}
	}
}

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
