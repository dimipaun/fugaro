// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P1.3's tests: the portal API's sign-in — the Firebase ID token the SPA
// presents, the rules a membership adds on top of it, and the OIDC token a
// Cloud Scheduler job presents.
//
// Every token here is signed by a key this process generates, and the key set
// the verifiers read is this file's own map: no fixture holds a usable key
// (.claude/rules/ee.md, R5), and no test reaches the network.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
)

const (
	// portalProject is the portal's own Firebase project id: the audience every
	// admitted ID token carries and the suffix of its issuer. portalOtherProject
	// is another project's, which no token of this one may name.
	portalProject      = "portal-project"
	portalOtherProject = "portal-other-project"
	portalKid          = "portal-published-key-1"
	portalUID          = "uid-portal-1"
	// portalEmail is a customer organisation's owner; portalOpsEmail is an
	// operations one, and neither is a real address.
	portalEmail    = "owner@example.com"
	portalOpsEmail = "operator@fugaro.example"
	// portalMarker cannot be mistaken for a token: it is the value the no-echo
	// assertions look for in a refusal.
	portalMarker = "marker-portal-sign-in-do-not-echo"
	// The Scheduler job's own identity and the audience its token is minted for
	// (F33): the API's answer URL, which is what a Cloud Scheduler job names.
	portalSchedulerAccount  = "scheduler@portal-project.iam.gserviceaccount.com"
	portalOtherAccount      = "another-job@portal-project.iam.gserviceaccount.com"
	portalSchedulerAudience = "https://api.portal.example/api/reminders"
	portalOtherAudience     = "https://api.portal.example/api/statements"
	// The Microsoft (Entra ID) directory a sign-in could be pinned to. It is the
	// test's own value and not a directory anybody owns.
	portalTenant = "tenant-portal-1"
)

// portalNow is the clock every test verifies against, so a token's window and a
// sign-in's age are the test's own arithmetic rather than the wall clock's.
var portalNow = time.Date(2030, 3, 4, 5, 6, 7, 0, time.UTC)

// portalKey is the key every portal token here is signed with, generated once:
// a 2048-bit keygen is the slowest thing in these tests.
var portalKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("keyservice test: generating a portal key: " + err.Error())
	}
	return k
})

// portalKeys is a KeySource over a fixed set, in the shape mcpauth's own tests
// use: the key id is the caller's own text and no refusal repeats it.
type portalKeys map[string]*rsa.PublicKey

func (p portalKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, ok := p[kid]
	if !ok {
		return nil, fmt.Errorf("%w: the token names a key id Google does not publish", mcpauth.ErrUnknownKeyID)
	}
	return key, nil
}

// signPortalToken returns a compact RS256 JWT signed by key.
func signPortalToken(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// portalClaims is an ID token this portal admits: a Google sign-in of a
// customer organisation's owner, inside its window and signed in a minute ago.
func portalClaims() map[string]any {
	return map[string]any{
		"iss":            "https://securetoken.google.com/" + portalProject,
		"aud":            portalProject,
		"sub":            portalUID,
		"user_id":        portalUID,
		"email":          portalEmail,
		"email_verified": true,
		"iat":            portalNow.Add(-time.Minute).Unix(),
		"exp":            portalNow.Add(time.Hour).Unix(),
		"auth_time":      portalNow.Add(-time.Minute).Unix(),
		"firebase": map[string]any{
			"identities":       map[string]any{"email": []any{portalEmail}},
			"sign_in_provider": "google.com",
		},
	}
}

// microsoftSignInClaims is the claim set Firebase mints for a Microsoft
// sign-in, as P1.3 records it before P2.2 reads one: the provider is
// `microsoft.com`, `firebase.identities` holds the provider's own user id, and
// the address is the directory's. No Microsoft (Entra ID) directory id is in
// it — the only tenant-shaped claim Firebase's set carries is `firebase.tenant`,
// which is Identity Platform's own tenant — which is why the tenant check below
// refuses every Microsoft sign-in a deployment can actually mint (D2207).
func microsoftSignInClaims() map[string]any {
	claims := portalClaims()
	claims["email"] = "owner@contoso.example"
	claims["firebase"] = map[string]any{
		"identities":       map[string]any{"email": []any{"owner@contoso.example"}, "microsoft.com": []any{"microsoft-user-1"}},
		"sign_in_provider": "microsoft.com",
	}
	return claims
}

// microsoftTenantClaims is a Microsoft sign-in whose token carries an Identity
// Platform tenant, which is the only tenant-shaped claim a Firebase ID token
// has: the pin below is what a reseller's list is compared against, and the
// recorded shape above is what says a deployment cannot mint one today.
func microsoftTenantClaims() map[string]any {
	claims := microsoftSignInClaims()
	claims["firebase"].(map[string]any)["tenant"] = portalTenant
	return claims
}

// schedulerClaims is an OIDC token a Cloud Scheduler job of this deployment
// presents: Google's issuer, the job's own account, the audience it was minted
// for, and a window.
func schedulerClaims() map[string]any {
	return map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            portalSchedulerAudience,
		"sub":            "1234567890",
		"email":          portalSchedulerAccount,
		"email_verified": true,
		"iat":            portalNow.Add(-time.Minute).Unix(),
		"exp":            portalNow.Add(time.Hour).Unix(),
	}
}

// portalVerifier is the verifier every test in this file drives, over the
// test's own clock.
func portalVerifier(t *testing.T) *PortalVerifier {
	t.Helper()
	v, err := NewPortalVerifier(portalProject, portalKeys{portalKid: &portalKey().PublicKey}, WithPortalClock(func() time.Time { return portalNow }))
	if err != nil {
		t.Fatalf("NewPortalVerifier: %v", err)
	}
	return v
}

// schedulerVerifier is the Scheduler verifier the same fixture drives: one
// account, one audience, the test's clock.
func schedulerVerifier(t *testing.T) *SchedulerVerifier {
	t.Helper()
	v, err := NewSchedulerVerifier(portalSchedulerAccount, portalSchedulerAudience, portalKeys{portalKid: &portalKey().PublicKey}, WithSchedulerClock(func() time.Time { return portalNow }))
	if err != nil {
		t.Fatalf("NewSchedulerVerifier: %v", err)
	}
	return v
}

// portalSignIn signs claims with the test's key and verifies them, which is
// what the rule tests below drive: a refusal here is the test's own bug rather
// than the rule's answer.
func portalSignIn(t *testing.T, v *PortalVerifier, claims map[string]any) SignIn {
	t.Helper()
	signIn, err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", claims))
	if err != nil {
		t.Fatalf("Verify refused a token this project signed: %v", err)
	}
	return signIn
}

// TestEmailLinkIsRefusedForOperations is P1.3's red step. A passwordless
// sign-in is refused for any request that uses an operations membership, and
// BOTH spellings the reconciliation leaves open are pinned: a `password` token
// and an `emailLink` one are each refused.
func TestEmailLinkIsRefusedForOperations(t *testing.T) {
	v := portalVerifier(t)
	operations := SSORule{Operations: true}

	// The admitted case is asserted beside the refusals: a rule that refused
	// every provider would pass the two cases below while refusing the SSO the
	// operations organisation requires (D2207).
	google, err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", portalClaims()))
	if err != nil {
		t.Fatalf("Verify refused a Google sign-in this portal admits: %v", err)
	}
	if err := v.RequireSSO(google, operations); err != nil {
		t.Errorf("an operations membership refused a Google sign-in: %v", err)
	}

	for _, provider := range []string{"password", "emailLink"} {
		claims := portalClaims()
		claims["email"] = portalOpsEmail
		claims["firebase"] = map[string]any{"identities": map[string]any{"email": []any{portalOpsEmail}}, "sign_in_provider": provider}

		signIn, err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", claims))
		if err != nil {
			t.Fatalf("%s: Verify refused a token this project signed: %v", provider, err)
		}
		if signIn.Provider != provider {
			t.Fatalf("%s: the verified sign-in carries provider %q, and the token named %q", provider, signIn.Provider, provider)
		}
		err = v.RequireSSO(signIn, operations)
		if err == nil {
			t.Errorf("%s: an operations membership admitted a sign-in that is not SSO (%s)", provider, portalMarker)
			continue
		}
		if !errors.Is(err, ErrSSORequired) {
			t.Errorf("%s: refused with %v, which is not the sentinel the SPA opens its SSO dialog from", provider, err)
		}
		if strings.Contains(err.Error(), provider) {
			t.Errorf("%s: the refusal repeats the provider out of the token: %q", provider, err.Error())
		}
	}
}

// TestTokenFromAnotherProjectIsRefused: the audience and the issuer are built
// from this project's id, so a token another Firebase project minted is refused
// at the door rather than read.
func TestTokenFromAnotherProjectIsRefused(t *testing.T) {
	v := portalVerifier(t)
	claims := portalClaims()
	claims["aud"] = portalOtherProject
	claims["iss"] = "https://securetoken.google.com/" + portalOtherProject
	tok := signPortalToken(t, portalKey(), portalKid, "RS256", claims)

	signIn, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatalf("Verify admitted %s, whose audience and issuer are another project's", portalMarker)
	}
	if !errors.Is(err, ErrUnverifiedSignIn) {
		t.Errorf("Verify refused another project's token with %v, which is not the sentinel the API answers with", err)
	}
	if signIn != (SignIn{}) {
		t.Errorf("Verify answered a sign-in beside a refusal: %+v", signIn)
	}
	message := err.Error()
	if strings.Contains(message, tok) || strings.Contains(message, portalOtherProject) {
		t.Errorf("the refusal names the token or the project that minted it: %q", message)
	}
}

// TestUnverifiedEmailIsRefused: an address nobody verified is an address its
// holder has not proved is theirs, and every membership in this tree is keyed on
// an address. It is required for the portal's project (the spec's auth section
// makes an invitation acceptable only by its own verified address, which is
// P2.2's rule and this one's).
func TestUnverifiedEmailIsRefused(t *testing.T) {
	v := portalVerifier(t)

	for name, mutate := range map[string]func(map[string]any){
		"false":  func(c map[string]any) { c["email_verified"] = false },
		"absent": func(c map[string]any) { delete(c, "email_verified") },
		"not a bool": func(c map[string]any) {
			c["email_verified"] = "true"
		},
	} {
		claims := portalClaims()
		mutate(claims)
		if _, err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", claims)); !errors.Is(err, ErrUnverifiedSignIn) {
			t.Errorf("%s: an email_verified that is not true was refused with %v, which is not the portal's sentinel", name, err)
		}
	}
}

// TestStaleSignInIsRefusedForMint: D2187's step-up is an age, and the age is
// `auth_time`'s. A sign-in older than the window is refused, one that attests no
// sign-in time at all is refused, and one from the future is refused — a token
// that says a person signed in ten minutes from now is a clock nobody can act
// on, and reading it as fresh is how a stale sign-in is smuggled past the mint.
func TestStaleSignInIsRefusedForMint(t *testing.T) {
	v := portalVerifier(t)
	const mintWindow = 5 * time.Minute

	cases := map[string]struct {
		authTime any
		within   time.Duration
		refused  bool
	}{
		"a minute ago":                 {authTime: portalNow.Add(-time.Minute).Unix(), within: mintWindow},
		"just inside the window":       {authTime: portalNow.Add(-mintWindow + time.Second).Unix(), within: mintWindow},
		"just outside the window":      {authTime: portalNow.Add(-mintWindow - time.Second).Unix(), within: mintWindow, refused: true},
		"this morning":                 {authTime: portalNow.Add(-6 * time.Hour).Unix(), within: mintWindow, refused: true},
		"no sign-in time in the token": {authTime: nil, within: mintWindow, refused: true},
		"ten minutes in the future":    {authTime: portalNow.Add(10 * time.Minute).Unix(), within: mintWindow, refused: true},
		"the window is the caller's":   {authTime: portalNow.Add(-4 * time.Minute).Unix(), within: time.Minute, refused: true},
		"a window of nothing":          {authTime: portalNow.Unix(), within: 0, refused: true},
	}
	for name, tc := range cases {
		claims := portalClaims()
		if tc.authTime == nil {
			delete(claims, "auth_time")
		} else {
			claims["auth_time"] = tc.authTime
		}
		err := v.FreshWithin(portalSignIn(t, v, claims), tc.within)
		if err == nil {
			if tc.refused {
				t.Errorf("%s: a sign-in this action must refuse was accepted as fresh (%s)", name, portalMarker)
			}
			continue
		}
		if !tc.refused {
			t.Errorf("%s: a fresh sign-in was refused: %v", name, err)
			continue
		}
		if !errors.Is(err, ErrStaleSignIn) {
			t.Errorf("%s: refused with %v, which is not the sentinel a caller answers 'sign in again' from", name, err)
		}
	}

	// The age is the SIGN-IN's and not the token's: a token minted a minute ago
	// whose auth_time is this morning is stale, which is the fact a step-up
	// measured on iat would miss.
	claims := portalClaims()
	claims["iat"] = portalNow.Add(-time.Minute).Unix()
	claims["auth_time"] = portalNow.Add(-6 * time.Hour).Unix()
	if err := v.FreshWithin(portalSignIn(t, v, claims), mintWindow); !errors.Is(err, ErrStaleSignIn) {
		t.Errorf("a token minted now that attests a sign-in from this morning was treated as fresh (%v): D2187's five minutes are about the person's sign-in and not about the token's age", err)
	}
}

// TestMicrosoftTenantIsPinned: D2207's condition for a reseller. A Microsoft
// sign-in is admitted only from a tenant on the organisation's list, so a
// directory nobody pinned is refused however valid the token is.
func TestMicrosoftTenantIsPinned(t *testing.T) {
	v := portalVerifier(t)
	signIn := portalSignIn(t, v, microsoftTenantClaims())
	if signIn.Provider != "microsoft.com" || signIn.TenantID != portalTenant {
		t.Fatalf("the verified sign-in is %q from tenant %q, and the token named microsoft.com and %q", signIn.Provider, signIn.TenantID, portalTenant)
	}

	reseller := SSORule{MicrosoftTenants: []string{portalTenant}}
	if err := v.RequireSSO(signIn, reseller); err != nil {
		t.Errorf("a reseller refused a Microsoft sign-in from the tenant it pinned: %v", err)
	}

	unlisted := SSORule{MicrosoftTenants: []string{"tenant-someone-else"}}
	err := v.RequireSSO(signIn, unlisted)
	if err == nil {
		t.Fatalf("a reseller admitted a Microsoft sign-in from a tenant it did not pin (%s)", portalMarker)
	}
	if !errors.Is(err, ErrTenantNotPinned) {
		t.Errorf("a tenant outside the list was refused with %v, which is not the tenant sentinel", err)
	}
	if strings.Contains(err.Error(), portalTenant) {
		t.Errorf("the refusal repeats the tenant out of the token: %q", err.Error())
	}
}

// TestEmptyTenantListRefusesMicrosoft: F40's rule, and the state a reseller is
// actually in (this file's recorded shape): with no tenant pinned, every
// Microsoft sign-in is refused rather than admitted by an empty list.
func TestEmptyTenantListRefusesMicrosoft(t *testing.T) {
	v := portalVerifier(t)

	for name, rule := range map[string]SSORule{
		"the zero rule":  {},
		"nil list":       {MicrosoftTenants: nil},
		"an empty list":  {MicrosoftTenants: []string{}},
		"another tenant": {MicrosoftTenants: []string{"tenant-1", "tenant-2"}},
	} {
		err := v.RequireSSO(portalSignIn(t, v, microsoftTenantClaims()), rule)
		if err == nil {
			t.Errorf("%s: a Microsoft sign-in was admitted with no tenant pinned (%s)", name, portalMarker)
			continue
		}
		if !errors.Is(err, ErrTenantNotPinned) {
			t.Errorf("%s: refused with %v, which is not the tenant sentinel", name, err)
		}
	}
}

// TestOperationsAcceptsGoogleOnly: D2207's answer for the operations
// organisation. Google is admitted, and a Microsoft sign-in is refused even
// when its tenant is one the rule lists — the list cannot widen the operations
// organisation, because the rule is Google alone.
func TestOperationsAcceptsGoogleOnly(t *testing.T) {
	v := portalVerifier(t)
	operations := SSORule{Operations: true, MicrosoftTenants: []string{portalTenant}}

	if err := v.RequireSSO(portalSignIn(t, v, portalClaims()), operations); err != nil {
		t.Errorf("the operations organisation refused a Google sign-in: %v", err)
	}

	err := v.RequireSSO(portalSignIn(t, v, microsoftTenantClaims()), operations)
	if err == nil {
		t.Fatalf("the operations organisation admitted a Microsoft sign-in whose tenant it pinned (%s): D2207 makes operations Google only", portalMarker)
	}
	if !errors.Is(err, ErrSSORequired) {
		t.Errorf("operations refused a Microsoft sign-in with %v, which is not the sentinel the SPA opens its SSO dialog from", err)
	}
}

// TestResellerMicrosoftNeedsAPinnedTenant: the same reseller rule over the
// Microsoft sign-in Firebase ACTUALLY mints — the recorded shape, whose token
// carries no tenant because Firebase forwards no Microsoft directory id. D2207's
// fallback follows: a reseller organisation is Google-only until the owner gives
// a directory a tenant Firebase can carry.
func TestResellerMicrosoftNeedsAPinnedTenant(t *testing.T) {
	v := portalVerifier(t)
	signIn := portalSignIn(t, v, microsoftSignInClaims())
	if signIn.TenantID != "" {
		t.Fatalf("the recorded Microsoft sign-in carries tenant %q, and P1.3's finding is that Firebase mints none: a reseller's pinned list can then be satisfied, which would change D2207's fallback and the reason for it", signIn.TenantID)
	}

	reseller := SSORule{MicrosoftTenants: []string{portalTenant}}
	err := v.RequireSSO(signIn, reseller)
	if err == nil {
		t.Fatalf("a reseller admitted the Microsoft sign-in a deployment can mint, which carries no tenant to pin (%s)", portalMarker)
	}
	if !errors.Is(err, ErrTenantNotPinned) {
		t.Errorf("the recorded Microsoft sign-in was refused with %v, which is not the tenant sentinel", err)
	}

	// The rule refuses the provider and not the person: the same reseller
	// admits the same address signed in with Google, which is what the fallback
	// leaves a reseller organisation with.
	google := portalClaims()
	google["email"] = "owner@contoso.example"
	if err := v.RequireSSO(portalSignIn(t, v, google), reseller); err != nil {
		t.Errorf("a reseller refused a Google sign-in: %v", err)
	}
}

// TestTheSignInsMemberIsTheTreesSpelling: F31's rule that both sides of an
// address comparison go through access.Member. The member folds the domain and
// keeps the local part byte for byte — a mail system that treats the local part
// otherwise is a fact this tree cannot read, and folding it could merge two
// people — and the same fold is what a stored record is compared with.
func TestTheSignInsMemberIsTheTreesSpelling(t *testing.T) {
	v := portalVerifier(t)
	claims := portalClaims()
	claims["email"] = "Owner@Example.COM"
	signIn := portalSignIn(t, v, claims)

	if signIn.Email != "owner@example.com" {
		t.Errorf("the sign-in's address is %q, and mcpauth folds the whole claim to lower case as ASCII", signIn.Email)
	}
	if signIn.Member != "user:owner@example.com" {
		t.Errorf("the sign-in's member is %q, and the member of that token's address is user:owner@example.com", signIn.Member)
	}
	if !SameMember("owner@example.com", "owner@EXAMPLE.com") {
		t.Error("same member, one domain in another case: the two sides go through access.Member, so a stored record spelled with a capitalised domain is the same person")
	}
	if SameMember("Owner@example.com", "owner@example.com") {
		t.Error("a local part's case was folded: access.Member keeps it byte for byte, and folding it could merge two people")
	}
	if SameMember("owner@example.com", "no-an-at-sign") {
		t.Error("an address that is no member was read as one")
	}
}

// TestSchedulerTokenNeedsItsAccountAndAudience: F33. The token a job presents
// is admitted only when it is Google's, was minted for this route's audience and
// carries the account this route runs as — the account tells one job's token
// from another identity's, and the audience tells this route's from the project's
// other jobs'.
func TestSchedulerTokenNeedsItsAccountAndAudience(t *testing.T) {
	v := schedulerVerifier(t)

	if err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims())); err != nil {
		t.Fatalf("the Scheduler verifier refused the token this deployment's job presents: %v", err)
	}

	// A second key: a token signed by a key the published set does not hold for
	// that kid is refused, which is the check a token's kid alone cannot be.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a second key: %v", err)
	}
	expired := schedulerClaims()
	expired["exp"] = portalNow.Add(-time.Minute).Unix()
	future := schedulerClaims()
	future["iat"] = portalNow.Add(2 * time.Hour).Unix()

	// schedulerToken signs the deployment's own claims with the one field the
	// case is about changed.
	schedulerToken := func(mutate func(map[string]any)) string {
		claims := schedulerClaims()
		mutate(claims)
		return signPortalToken(t, portalKey(), portalKid, "RS256", claims)
	}

	cases := map[string]struct {
		token string
		names []string
	}{
		"another account":                  {token: schedulerToken(func(c map[string]any) { c["email"] = portalOtherAccount }), names: []string{portalOtherAccount}},
		"another audience":                 {token: schedulerToken(func(c map[string]any) { c["aud"] = portalOtherAudience }), names: []string{portalOtherAudience}},
		"an unverified address":            {token: schedulerToken(func(c map[string]any) { c["email_verified"] = false })},
		"no address at all":                {token: schedulerToken(func(c map[string]any) { delete(c, "email") })},
		"another issuer":                   {token: schedulerToken(func(c map[string]any) { c["iss"] = "https://accounts.example.com" })},
		"an expired token":                 {token: signPortalToken(t, portalKey(), portalKid, "RS256", expired)},
		"issued in the future":             {token: signPortalToken(t, portalKey(), portalKid, "RS256", future)},
		"a key id Google does not publish": {token: signPortalToken(t, portalKey(), "another-kid", "RS256", schedulerClaims())},
		"signed by another key":            {token: signPortalToken(t, otherKey, portalKid, "RS256", schedulerClaims())},
		"a token signed another way":       {token: signPortalToken(t, portalKey(), portalKid, "HS256", schedulerClaims())},
		"not a jwt":                        {token: portalMarker},
	}
	for name, tc := range cases {
		err := v.Verify(context.Background(), tc.token)
		if err == nil {
			t.Errorf("%s: the Scheduler verifier admitted a token it must refuse (%s)", name, portalMarker)
			continue
		}
		if !errors.Is(err, ErrUnverifiedSchedulerToken) {
			t.Errorf("%s: refused with %v, which is not the sentinel the route answers 403 from", name, err)
		}
		message := err.Error()
		if strings.Contains(message, tc.token) || strings.Contains(message, portalMarker) {
			t.Errorf("%s: the refusal carries the token: %q", name, message)
		}
		for _, named := range append(tc.names, portalSchedulerAccount, portalSchedulerAudience) {
			if strings.Contains(message, named) {
				t.Errorf("%s: the refusal names %q, and a refusal reaches a log line and an answer body", name, named)
			}
		}
	}
}

// TestAVerifierNeedsItsConfiguration: a portal verifier without a project or a
// key set, and a Scheduler verifier without an account, an audience or a key
// set, are each refused at construction rather than built into one that admits
// nothing — or one whose comparison is against an empty string.
func TestAVerifierNeedsItsConfiguration(t *testing.T) {
	keys := portalKeys{portalKid: &portalKey().PublicKey}

	if _, err := NewPortalVerifier("", keys); err == nil {
		t.Error("NewPortalVerifier accepted an empty project id: the audience and the issuer are both built from it")
	}
	if _, err := NewPortalVerifier(portalProject, nil); err == nil {
		t.Error("NewPortalVerifier accepted a nil key set: a verifier with nowhere to read a key is one whose refusals look like a stranger's bad token")
	}
	if _, err := NewSchedulerVerifier("", portalSchedulerAudience, keys); err == nil {
		t.Error("NewSchedulerVerifier accepted an empty account: the account is the whole identity check, and one compared against nothing admits whatever audience matches")
	}
	if _, err := NewSchedulerVerifier(portalSchedulerAccount, "", keys); err == nil {
		t.Error("NewSchedulerVerifier accepted an empty audience: every job of the project mints a token, and the audience is what tells this route's from the others'")
	}
	if _, err := NewSchedulerVerifier(portalSchedulerAccount, portalSchedulerAudience, nil); err == nil {
		t.Error("NewSchedulerVerifier accepted a nil key set: nothing would be verified but the claims")
	}
}
