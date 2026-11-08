// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

import (
	"context"
	"crypto/rsa"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The admission's fixtures. What a test varies is the member's own lists and
// the identity the workspace asserts; the assertions themselves come from the
// exchange's fixtures (workspace_test.go), so nothing here restates the claims
// an honest signer writes.

// listsFunc is the member's own admission lists as a source: the entries it
// holds, or the failure of a host that cannot read them at all.
type listsFunc func(ctx context.Context) ([]string, error)

func (f listsFunc) Addresses(ctx context.Context) ([]string, error) { return f(ctx) }

// memberLists is a source holding the entries `fugaro user` writes, read
// through the member's REAL reader — projectpolicy.MemberLists, its one rule
// for which entries are addresses and mobileauth's one rule for addresses —
// rather than through a fake free to answer differently from the list a
// member's own configuration holds. Every test below is then the composed
// decision: the exchange's verification and the member's own lists together,
// which is what W5 is.
func memberLists(launchers, operators []string) listsFunc {
	m := projectpolicy.MemberListsOf(launchers, operators)
	return func(context.Context) ([]string, error) { return m.Addresses(), nil }
}

// unreachableLists is the member's own lists when the host cannot read them.
func unreachableLists(err error) listsFunc {
	return func(context.Context) ([]string, error) { return nil, err }
}

// memberWithKey is one member of a workspace, with its own pin and the key its
// workspace signs with: the starting point of every test here.
func memberWithKey(t *testing.T) (*testMember, *rsa.PrivateKey) {
	t.Helper()
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	return member, key
}

// newTestAdmission is a member's whole admission: its exchange and its lists.
func newTestAdmission(t *testing.T, m *testMember, lists Lists) *Admission {
	t.Helper()
	a, err := NewAdmission(m.ex, lists)
	if err != nil {
		t.Fatalf("NewAdmission for %s: %v", m.name, err)
	}
	return a
}

// exchangeURL is a member's own `/oauth/authorize`, which is the audience of
// every assertion minted for it.
func exchangeURL(host string) string { return "https://" + host + AuthorizePath }

// TestAdmitRefusesAnAddressTheMembersListsDoNotName is the test this task
// exists for. The workspace's signer minted an assertion for this member, the
// key is the one the member pins and every claim in the window is the one an
// honest signer writes — and the person it names is not one this member's own
// launcher or operator lists name. The admission must refuse, because the
// lists are the whole of the admission (D1295): a member admits the people its
// own `fugaro user` wrote and nobody the workspace chose.
func TestAdmitRefusesAnAddressTheMembersListsDoNotName(t *testing.T) {
	member, key := memberWithKey(t)
	admission := newTestAdmission(t, member, memberLists(
		[]string{"user:person-2@example.com"},
		[]string{"user:person-3@example.com"},
	))

	sess, err := admission.Admit(context.Background(), assertionFor(t, key, testProject, exchangeURL(member.name), nil))
	refuse(t, err, "an assertion for a person neither of this member's lists names")
	if strings.Contains(err.Error(), testEmail) {
		t.Fatalf("the refusal repeats the address it refused (%v): a refusal that names the person off the lists tells whoever holds the assertion whether that person is a member here, which is the enumeration a refusal must not do", err)
	}
	_ = sess
}

// TestAdmitDoesNotWidenAMembersLists is the other half of the red step, and it
// is the half that makes the test above worth having: a refusal proves nothing
// on its own — an admission that refused everybody would pass it. What is
// proved here is that the admission is exactly the member's own lists. The
// workspace's own administration cannot add anybody to them (D1295): the two
// assertions below are minted by the same workspace, with the same key, for the
// same member, and the one difference between them is whether the person is one
// the member's own configuration already names.
func TestAdmitDoesNotWidenAMembersLists(t *testing.T) {
	member, key := memberWithKey(t)
	admission := newTestAdmission(t, member, memberLists([]string{"user:person-1@example.com"}, nil))
	ctx := context.Background()

	sess, err := admission.Admit(ctx, assertionFor(t, key, testProject, exchangeURL(member.name), nil))
	if err != nil {
		t.Fatalf("the admission refused the one person this member's own launcher list names: %v", err)
	}
	if sess.Identity.Email != testEmail || !sess.Identity.EmailVerified {
		t.Fatalf("the admitted identity is %+v, and it is the assertion's own verified address", sess.Identity)
	}
	// The member's OWN limits, which the assertion cannot state: the workspace
	// is not the party that decides what a person may spend here.
	if !reflect.DeepEqual(sess.Limits, memberLimits) {
		t.Fatalf("the session's limits are %+v, and they are the member's own", sess.Limits)
	}

	// The same workspace, the same key, the same member, and a person the
	// member's lists do not name — the launcher one was admitted, and this one
	// is not, so the admission is the lists rather than the workspace's word.
	off := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) {
		a.Email = "person-2@example.com"
		a.JTI = "jti-2"
	})
	refuse(t, mustErr(admission.Admit(ctx, off)), "an assertion for somebody the workspace could have named but this member's lists do not")
}

// TestAdmitReadsTheListsOnEveryRequest: the lists are read at the decision and
// not once at startup. A member that held a value it read earlier would be
// admitting people by a record that may since have changed — the stale local
// value W5's fail-closed rule refuses — and would refuse a person the operator
// added this morning.
func TestAdmitReadsTheListsOnEveryRequest(t *testing.T) {
	member, key := memberWithKey(t)
	held := []string(nil)
	admission := newTestAdmission(t, member, listsFunc(func(context.Context) ([]string, error) {
		return projectpolicy.MemberListsOf(held, nil).Addresses(), nil
	}))
	ctx := context.Background()

	refuse(t, mustErr(admission.Admit(ctx, assertionFor(t, key, testProject, exchangeURL(member.name), nil))), "an assertion for a person no list names yet")

	held = []string{"user:" + testEmail}
	sess, err := admission.Admit(ctx, assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) {
		a.JTI = "jti-2"
	}))
	if err != nil {
		t.Fatalf("the admission refused a person the member's lists name, once they name them: %v", err)
	}
	if sess.Identity.Email != testEmail {
		t.Fatalf("the admitted identity is %+v", sess.Identity)
	}
}

// TestAdmitRefusesAnUnverifiedAddress is the credentials service's own rule for
// every class of caller (internal/credsvc/auth.go's checkCallerToken, at :1018),
// applied at the exchange: an address nobody proved they own is not one this
// member may admit. The claim is checked as a claim — absent, false, and a
// value that is not a bool at all are three ways of failing it — and the
// refusal names the claim, never the address the assertion carried.
func TestAdmitRefusesAnUnverifiedAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(claims map[string]any)
	}{
		{"absent", func(claims map[string]any) { delete(claims, "email_verified") }},
		{"false", func(claims map[string]any) { claims["email_verified"] = false }},
		{"not a bool", func(claims map[string]any) { claims["email_verified"] = "true" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			member, key := memberWithKey(t)
			// The assertion is signed claim by claim rather than minted:
			// mintAssertion refuses to write an unverified address at all
			// (the signer's own check), and what this test is about is the
			// member's, which must not depend on the workspace having one.
			claims := testAssertion(testProject, exchangeURL(member.name)).claims()
			tc.edit(claims)
			// The person IS on this member's list, so nothing but the claim
			// can be what refuses them here.
			admission := newTestAdmission(t, member, memberLists([]string{"user:" + testEmail}, nil))

			err := mustErr(admission.Admit(context.Background(), signClaims(t, key, claims)))
			refuse(t, err, "an assertion whose emailed claim is "+tc.name)
			// The plan's rule is that the refusal names the CLAIM. The
			// sentence the exchange writes for this one is that the address
			// "is not marked verified" (workspace.go), so what a test can hold
			// is the claim it is about rather than a literal spelling of it.
			if !strings.Contains(err.Error(), "verified") {
				t.Fatalf("the refusal of an unverified address is %v, and it does not name the claim it is about", err)
			}
			if strings.Contains(err.Error(), testEmail) {
				t.Fatalf("the refusal repeats the address it refused: %v", err)
			}
		})
	}
}

// TestAdmitIsCaseFoldedOnTheDomainOnly is mobileauth's one rule for addresses
// (internal/mobileauth/email.go), which is the rule both sides of this
// comparison are written and read with: the domain folds, the local part is
// kept byte for byte because a mail host may treat two local parts that differ
// in case as two mailboxes. So a workspace whose Firebase folded the domain, a
// list the operator wrote in another case, and the address in the assertion all
// meet; and two spellings that differ in the local part are two people.
func TestAdmitIsCaseFoldedOnTheDomainOnly(t *testing.T) {
	member, key := memberWithKey(t)
	ctx := context.Background()

	// The domain is folded on the list's side and on the assertion's.
	admission := newTestAdmission(t, member, memberLists([]string{"user:" + testEmail}, nil))
	folded := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) {
		a.Email = "person-1@EXAMPLE.com"
	})
	sess, err := admission.Admit(ctx, folded)
	if err != nil {
		t.Fatalf("the admission refused an address that differs from the listed one only in the case of its domain: %v", err)
	}
	if sess.Identity.Email != testEmail {
		t.Fatalf("the admitted identity carries %q, and the address of a session is the folded one", sess.Identity.Email)
	}
	// Every assertion here is a separate sign-in, so each one carries an id of
	// its own: the member spends the id of every assertion it verifies, and a
	// second presentation of one is refused for that reason instead of the one
	// under test.
	caseFoldedEntry := newTestAdmission(t, member, memberLists([]string{"user:person-1@EXAMPLE.COM"}, nil))
	entry := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) { a.JTI = "jti-2" })
	if _, err := caseFoldedEntry.Admit(ctx, entry); err != nil {
		t.Fatalf("the admission refused a listed address written with a folded-away domain case: %v", err)
	}

	// The local part is not folded: this is another address, and another
	// person, whatever the mail host does with it.
	localPart := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) {
		a.Email = "Person-1@example.com"
		a.JTI = "jti-3"
	})
	refuse(t, mustErr(admission.Admit(ctx, localPart)), "an address whose local part differs from the listed one only in case")
}

// TestAdmitRefusesADisplayNameForm: a name is not an admission, on either side
// of the comparison. An assertion whose address is a display name is one this
// member cannot read at all (mobileauth refuses it rather than parsing the
// address out of it, which is how a name would become an admission), and an
// entry written that way in the member's own lists is inert for the same
// reason — it is not one person written as `user:<address>`, and the operator
// who wrote it has an installation whose own door refuses it too
// (`fugaro user`'s userMember).
func TestAdmitRefusesADisplayNameForm(t *testing.T) {
	member, key := memberWithKey(t)
	ctx := context.Background()
	admission := newTestAdmission(t, member, memberLists([]string{"user:" + testEmail}, nil))

	displayName := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) {
		a.Email = "Person One <" + testEmail + ">"
	})
	refuse(t, mustErr(admission.Admit(ctx, displayName)), "an assertion whose address is a display name")

	wrapping := newTestAdmission(t, member, memberLists([]string{"user:Person One <" + testEmail + ">"}, nil))
	refuse(t, mustErr(wrapping.Admit(ctx, assertionFor(t, key, testProject, exchangeURL(member.name), nil))), "an assertion for a person whose only list entry is a display name wrapping their address")
}

// TestAdmitRefusesACustomTokenProvider is finding 7's second half, at the
// member. A custom token is one anyone holding `signBlob` or `TokenCreator` on
// any service account of the workspace's project can mint, with `email` and
// `email_verified` set to whatever they like — so a member that took the
// signer's word for the provider would let such a minter name any of its own
// people. The provider is re-checked in the assertion, against the set the
// member pins, and this is what refuses a workspace whose signer let one
// through.
func TestAdmitRefusesACustomTokenProvider(t *testing.T) {
	member, key := memberWithKey(t)
	// The person IS on the member's list: whatever refuses them is the
	// provider, and nothing else.
	admission := newTestAdmission(t, member, memberLists([]string{"user:" + testEmail}, nil))

	custom := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) { a.Provider = "custom" })
	refuse(t, mustErr(admission.Admit(context.Background(), custom)), "an assertion whose provider is a custom token")
}

// TestAdmitRefusesAnAnonymousProvider: an anonymous sign-in proves no address
// at all, so there is nothing for the two lists to name.
func TestAdmitRefusesAnAnonymousProvider(t *testing.T) {
	member, key := memberWithKey(t)
	admission := newTestAdmission(t, member, memberLists([]string{"user:" + testEmail}, nil))

	anon := assertionFor(t, key, testProject, exchangeURL(member.name), func(a *assertion) { a.Provider = "anonymous" })
	refuse(t, mustErr(admission.Admit(context.Background(), anon)), "an assertion whose provider is anonymous")
}

// TestExchangeRefusesAMemberThatCannotReachTheWorkspacesFirebase is the
// fail-closed rule, and its name is the plan's. A member's machine with no
// route to the workspace's Firebase has no signer to ask, so the only thing a
// sign-in there could be decided from is a value already on the machine — a
// cached assertion, or a list read earlier — and the answer has to be a refusal
// rather than an admission from either.
//
// The lists are that value here: the member's own configuration cannot be read,
// so the admission cannot say who this member admits, and it refuses. What the
// test reads beside the refusal is that nothing was admitted from the valid,
// unspent assertion the machine was holding, and that the assertion's id was
// not even spent — the member refused before it spent anything, which is what
// "fails closed" means in a decision whose store outlives the request.
func TestExchangeRefusesAMemberThatCannotReachTheWorkspacesFirebase(t *testing.T) {
	member, key := memberWithKey(t)
	unreachable := errors.New("no route to the member's own lists")
	admission := newTestAdmission(t, member, unreachableLists(unreachable))

	// A valid, unspent assertion for a person this member's lists would name
	// if they could be read: exactly the cached value the rule refuses.
	raw := assertionFor(t, key, testProject, exchangeURL(member.name), nil)
	sess, err := admission.Admit(context.Background(), raw)
	if err == nil {
		t.Fatalf("the admission admitted %+v while it could not read a single one of its own lists", sess)
	}
	if !errors.Is(err, ErrMemberListsUnreadable) {
		t.Fatalf("the refusal is %v, and a member that cannot read its own lists refuses with ErrMemberListsUnreadable", err)
	}
	if !errors.Is(err, unreachable) {
		t.Fatalf("the refusal is %v, and it carries the failure of the source that could not be read", err)
	}
	if spent := jtisSpent(member); len(spent) != 0 {
		t.Fatalf("the member spent %d assertion ids while failing closed", len(spent))
	}

	// And a member constructed with no lists at all admits nobody either: a
	// member's own lists are what the admission is, so a nil source is refused
	// where it is wired rather than at the first sign-in.
	if _, err := NewAdmission(member.ex, nil); err == nil {
		t.Fatalf("NewAdmission accepted a nil lists source: a member with no lists admits nobody, and it must say so where it is built")
	}
}
