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
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/access"
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
	// The Microsoft (Entra ID) directory a sign-in could be pinned to, and an
	// Identity Platform tenant a client could name at sign-in beside it. Both are
	// the test's own values and neither is a directory or a tenant anybody owns;
	// keeping them different is what makes "a client-named tenant never admits"
	// a case the fixtures can spell.
	portalDirectory      = "directory-portal-1"
	portalClientTenant   = "tenant-portal-1"
	portalOtherDirectory = "directory-someone-else"
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

// noKeys is the one key source a verifier gets no refusal out of: it answers
// neither a key nor an error.
type noKeys struct{}

func (noKeys) Key(context.Context, string) (*rsa.PublicKey, error) { return nil, nil }

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
	return signSegments(t, key, string(head), string(payload))
}

// signSegments signs a header and a payload written out as text. It is what a
// case presents when the text it hands the decoder is already what a presenter
// would sign: a case built by rewriting a segment of an already-signed token
// meets the signature's refusal first, and the decoder guard it is about never
// runs.
func signSegments(t *testing.T, key *rsa.PrivateKey, head, payload string) string {
	t.Helper()
	signing := base64.RawURLEncoding.EncodeToString([]byte(head)) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
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
// sign-in, as P1.3 records it: the provider is `microsoft.com`,
// `firebase.identities` holds the provider's own user id, and the address is the
// directory's. No Microsoft (Entra ID) directory id is in it, which is the shape
// a provider that forwards no `tid` leaves behind: a reseller's pinned directory
// is never satisfied by it and RequireSSO refuses it rather than admitting it by
// a claim it does not carry (D2207's fallback).
func microsoftSignInClaims() map[string]any {
	claims := portalClaims()
	claims["email"] = "owner@contoso.example"
	claims["firebase"] = map[string]any{
		"identities":       map[string]any{"email": []any{"owner@contoso.example"}, "microsoft.com": []any{"microsoft-user-1"}},
		"sign_in_provider": "microsoft.com",
	}
	return claims
}

// microsoftDirectoryClaims is a Microsoft sign-in whose verified token reports
// the Entra directory the account lives in, in the one shape read:
// `firebase.sign_in_attributes.tid`, where Identity Platform puts a federated
// provider's own claims. That object is the provider's report of the sign-in,
// which is what makes it the value a reseller's list can be pinned to — unlike
// `firebase.tenant`, the Identity Platform tenant a client names at sign-in, and
// unlike a `tid` in the token's own custom-claims level, neither of which the
// rule reads (D2207 as amended on 2026-11-13).
func microsoftDirectoryClaims() map[string]any {
	claims := microsoftSignInClaims()
	claims["firebase"].(map[string]any)["sign_in_attributes"] = map[string]any{"tid": portalDirectory}
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
func portalSignIn(t *testing.T, v *PortalVerifier, claims map[string]any) VerifiedSignIn {
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
	if signIn != (VerifiedSignIn{}) {
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

// TestAnAddressThatIsNoMemberIsRefused: mcpauth's fold admits any printable
// ASCII as an address, and access.Member is the stricter rule — exactly one @
// with a local part and a domain on either side of it. An identity whose member
// cannot be computed is refused here and not returned with an empty Member,
// which is the invariant VerifiedSignIn.Member documents. It is a guard rather than a
// courtesy: a caller comparing an empty member against a stored record is
// comparing two empty things, and the record that would match is one nobody
// wrote.
func TestAnAddressThatIsNoMemberIsRefused(t *testing.T) {
	v := portalVerifier(t)

	for _, address := range []string{"no-at-sign", "two@at@signs", "trailing@", "@leading"} {
		claims := portalClaims()
		claims["email"] = address
		signIn, err := v.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", claims))
		if !errors.Is(err, ErrUnverifiedSignIn) {
			t.Errorf("the address %q was refused with %v, which is not the portal's sentinel", address, err)
			continue
		}
		if signIn != (VerifiedSignIn{}) {
			t.Errorf("the address %q answered %+v beside its refusal, and a refused sign-in carries nothing", address, signIn)
		}
		if strings.Contains(err.Error(), address) {
			t.Errorf("the refusal repeats the address out of the token: %q", err.Error())
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
		// The window here is the widest a caller can pass, and the age of a token
		// that attests nothing saturates on the way to it: time.Time.Sub answers
		// this exact value rather than a larger one, so the magnitude of the
		// difference admits the token and the guard above the comparison is the
		// only thing that refuses it.
		"no sign-in time, and the widest window": {authTime: nil, within: time.Duration(math.MaxInt64), refused: true},
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

// TestMicrosoftDirectoryIsPinned: D2207's condition for a reseller, as the
// owner's ruling of 2026-11-13 pins it. A Microsoft sign-in is admitted only
// when the VERIFIED token's Microsoft (Entra ID) directory id (`tid`, read out
// of `firebase.sign_in_attributes`) is on the organisation's list, so a
// directory nobody pinned is refused however valid the token is.
//
// This is also the task's mutation check, and the fixture is what makes it one:
// the admitted token carries a client-named Identity Platform tenant that is NOT
// on the pinned list (asserted below), so an implementation comparing
// signIn.TenantID against the list refuses the token and the admission assertion
// fails. (Verified by hand — the comparison swapped to TenantID, the admission
// line failing, then restored.)
func TestMicrosoftDirectoryIsPinned(t *testing.T) {
	v := portalVerifier(t)
	claims := microsoftDirectoryClaims()
	claims["firebase"].(map[string]any)["tenant"] = portalClientTenant
	signIn := portalSignIn(t, v, claims)
	if signIn.Provider != "microsoft.com" || signIn.DirectoryID != portalDirectory {
		t.Fatalf("the verified sign-in is %q from directory %q, and the token named microsoft.com and %q", signIn.Provider, signIn.DirectoryID, portalDirectory)
	}
	if signIn.TenantID != portalClientTenant {
		t.Fatalf("the verified sign-in carries tenant %q, and the token named %q", signIn.TenantID, portalClientTenant)
	}

	reseller := SSORule{MicrosoftDirectories: []string{portalDirectory}}
	if slices.Contains(reseller.MicrosoftDirectories, signIn.TenantID) {
		t.Fatalf("the pinned list holds the token's client-named tenant (%q): the mutation this test exists for — reading the tenant instead of the directory — would then pass it too", signIn.TenantID)
	}
	if err := v.RequireSSO(signIn, reseller); err != nil {
		t.Errorf("a reseller refused a Microsoft sign-in from the directory it pinned: %v", err)
	}

	unlisted := SSORule{MicrosoftDirectories: []string{portalOtherDirectory}}
	err := v.RequireSSO(signIn, unlisted)
	if err == nil {
		t.Fatalf("a reseller admitted a Microsoft sign-in from a directory it did not pin (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("a directory outside the list was refused with %v, which is not the directory sentinel", err)
	}
	message := err.Error()
	if strings.Contains(message, portalDirectory) || strings.Contains(message, portalClientTenant) {
		t.Errorf("the refusal repeats a value out of the token: %q", message)
	}
	if !strings.Contains(message, "tid") {
		t.Errorf("the refusal %q does not name the `tid` claim the pin is read from: the old text named the tenant, and the person reading it has to be told which value the rule is about", message)
	}

	// A `tid` in the token's own claim set is a CUSTOM claim and not a directory:
	// that level is the namespace this project writes `mu`, `fs` and `fr` in, so
	// a pin read from it is one any project-side writer of custom claims can
	// satisfy — `firebase.tenant`'s hole in another claim, which is why the
	// reader ignores it (mcpauth's `directoryID`, TestATopLevelTidIsNotADirectory).
	top := microsoftSignInClaims()
	top["tid"] = portalDirectory
	err = v.RequireSSO(portalSignIn(t, v, top), reseller)
	if err == nil {
		t.Fatalf("a reseller admitted a Microsoft sign-in whose token names the pinned directory in its own claim set, which is a value any project-side writer of custom claims can mint (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("a custom-claim tid was refused with %v, which is not the directory sentinel", err)
	}
}

// TestAClientNamedTenantNeverAdmitsMicrosoft: the hole #2515's fourth review
// round found, held shut. `firebase.tenant` is the Identity Platform tenant the
// CLIENT names when it calls the SDK — a door and not an identity — so a token
// that names the pinned value there, or any tenant at all, is refused unless its
// VERIFIED `tid` is on the list.
func TestAClientNamedTenantNeverAdmitsMicrosoft(t *testing.T) {
	v := portalVerifier(t)
	reseller := SSORule{MicrosoftDirectories: []string{portalDirectory}}

	// The client names the pinned value itself: the token carries the tenant and
	// no directory, which is the shape the old rule admitted.
	named := microsoftSignInClaims()
	named["firebase"].(map[string]any)["tenant"] = portalDirectory
	err := v.RequireSSO(portalSignIn(t, v, named), reseller)
	if err == nil {
		t.Fatalf("a reseller admitted a Microsoft sign-in whose client-named tenant is the pinned directory and whose token carries no tid (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("a token with a client-named tenant and no directory was refused with %v, which is not the directory sentinel", err)
	}

	// A client-named tenant beside a directory that is not the pinned one: the
	// tenant cannot carry the admission either, and the directory is what is
	// read.
	beside := microsoftDirectoryClaims()
	beside["firebase"].(map[string]any)["tenant"] = portalDirectory
	beside["firebase"].(map[string]any)["sign_in_attributes"] = map[string]any{"tid": portalOtherDirectory}
	err = v.RequireSSO(portalSignIn(t, v, beside), reseller)
	if err == nil {
		t.Fatalf("a reseller admitted a Microsoft sign-in from an unpinned directory because its client-named tenant was the pinned value (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("an unpinned directory beside a pinned client-named tenant was refused with %v, which is not the directory sentinel", err)
	}

	// And the same token with no tenant at all is the ordinary admission: what
	// this test refuses is the tenant as a route in and not the sign-in.
	if err := v.RequireSSO(portalSignIn(t, v, microsoftDirectoryClaims()), reseller); err != nil {
		t.Errorf("a reseller refused a Microsoft sign-in from the directory it pinned: %v", err)
	}
}

// TestEmptyDirectoryListRefusesMicrosoft: F40's rule, and the state a reseller
// is actually in when its provider forwards no `tid` (this file's recorded
// shape): with no directory pinned, every Microsoft sign-in is refused rather
// than admitted by an empty list.
func TestEmptyDirectoryListRefusesMicrosoft(t *testing.T) {
	v := portalVerifier(t)

	for name, rule := range map[string]SSORule{
		"the zero rule":     {},
		"nil list":          {MicrosoftDirectories: nil},
		"an empty list":     {MicrosoftDirectories: []string{}},
		"another directory": {MicrosoftDirectories: []string{portalOtherDirectory, "directory-2"}},
	} {
		err := v.RequireSSO(portalSignIn(t, v, microsoftDirectoryClaims()), rule)
		if err == nil {
			t.Errorf("%s: a Microsoft sign-in was admitted with no directory pinned (%s)", name, portalMarker)
			continue
		}
		if !errors.Is(err, ErrDirectoryNotPinned) {
			t.Errorf("%s: refused with %v, which is not the directory sentinel", name, err)
		}
	}
}

// TestOperationsAcceptsGoogleOnly: D2207's answer for the operations
// organisation. Google is admitted, and a Microsoft sign-in is refused even
// when its directory is one the rule lists — the list cannot widen the
// operations organisation, because the rule is Google alone.
func TestOperationsAcceptsGoogleOnly(t *testing.T) {
	v := portalVerifier(t)
	operations := SSORule{Operations: true, MicrosoftDirectories: []string{portalDirectory}}

	if err := v.RequireSSO(portalSignIn(t, v, portalClaims()), operations); err != nil {
		t.Errorf("the operations organisation refused a Google sign-in: %v", err)
	}

	err := v.RequireSSO(portalSignIn(t, v, microsoftDirectoryClaims()), operations)
	if err == nil {
		t.Fatalf("the operations organisation admitted a Microsoft sign-in whose directory it pinned (%s): D2207 makes operations Google only", portalMarker)
	}
	if !errors.Is(err, ErrSSORequired) {
		t.Errorf("operations refused a Microsoft sign-in with %v, which is not the sentinel the SPA opens its SSO dialog from", err)
	}
}

// TestResellerMicrosoftNeedsAPinnedDirectory: the same reseller rule over a
// Microsoft sign-in whose token carries no directory — the recorded shape, and
// the shape a provider that forwards no `tid` leaves behind. D2207's fallback
// follows: a reseller organisation is Google-only until the owner pins a
// directory its Microsoft provider puts into the token, because a list over
// anything else — the Identity Platform tenant a client names — is a rule the
// client satisfies (portalauth.go's comment). This test holds the fallback's own
// half: the shape that carries no directory is refused.
func TestResellerMicrosoftNeedsAPinnedDirectory(t *testing.T) {
	v := portalVerifier(t)
	signIn := portalSignIn(t, v, microsoftSignInClaims())
	if signIn.DirectoryID != "" {
		t.Fatalf("the recorded Microsoft sign-in carries directory %q, and this shape carries none: a reseller's pinned list can then be satisfied, which would change D2207's fallback and the reason for it", signIn.DirectoryID)
	}

	reseller := SSORule{MicrosoftDirectories: []string{portalDirectory}}
	err := v.RequireSSO(signIn, reseller)
	if err == nil {
		t.Fatalf("a reseller admitted the Microsoft sign-in a deployment can mint, which carries no directory to pin (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("the recorded Microsoft sign-in was refused with %v, which is not the directory sentinel", err)
	}

	// A list whose one entry is the empty string is what an empty line in an
	// owner's config writes, and it must refuse this sign-in: the token carries
	// no directory, so the empty entry would otherwise match it and admit every
	// Microsoft sign-in of this shape — the fallback above, undone by one blank
	// line.
	blank := SSORule{MicrosoftDirectories: []string{""}}
	err = v.RequireSSO(signIn, blank)
	if err == nil {
		t.Fatalf("a reseller whose pinned list is one empty entry admitted a Microsoft sign-in carrying no directory (%s)", portalMarker)
	}
	if !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("an empty list entry was refused with %v, which is not the directory sentinel", err)
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

// TestTheSeamCarriesEveryFactOfASignIn: PortalSignIn is what a deployment hands
// Deps.SignIn, so it is the production path by which the API's routes reach
// every rule in this file, and signInOf/callerOf are the one place a fact can be
// lost between the seam's Caller and the VerifiedSignIn the rules are written
// over. Every field of the conversion is pinned in both directions, and each
// fact a rule reads is read once through the seam rather than out of the
// conversion's own struct: a field dropped here compiles, fails closed, and
// refuses a person who did nothing wrong.
func TestTheSeamCarriesEveryFactOfASignIn(t *testing.T) {
	ctx := context.Background()
	v := portalVerifier(t)
	seam := PortalSignIn{Verifier: v}

	// A Microsoft sign-in carrying both a client-named Identity Platform tenant
	// and the Entra directory the verified token reported, which is the case
	// every carried fact is legible on: a Google sign-in has neither and is
	// admitted by every rule.
	claims := microsoftDirectoryClaims()
	claims["firebase"].(map[string]any)["tenant"] = portalClientTenant
	caller, err := seam.Verify(ctx, signPortalToken(t, portalKey(), portalKid, "RS256", claims))
	if err != nil {
		t.Fatalf("the seam refused a token this project signed: %v", err)
	}
	want := Caller{
		UID:         portalUID,
		Email:       "owner@contoso.example",
		Provider:    signInMicrosoft,
		AuthTime:    portalNow.Add(-time.Minute),
		TenantID:    portalClientTenant,
		DirectoryID: portalDirectory,
	}
	if caller.UID != want.UID {
		t.Errorf("the seam's caller names the account %q, want the token's subject %q", caller.UID, want.UID)
	}
	if caller.Email != want.Email {
		t.Errorf("the seam's caller carries the address %q, want the token's own %q", caller.Email, want.Email)
	}
	if caller.Provider != want.Provider {
		t.Errorf("the seam's caller carries the provider %q, want %q: the SSO rule reads it", caller.Provider, want.Provider)
	}
	if !caller.AuthTime.Equal(want.AuthTime) {
		t.Errorf("the seam's caller carries the sign-in instant %s, want the token's auth_time %s: the freshness rule reads it", caller.AuthTime, want.AuthTime)
	}
	if caller.TenantID != want.TenantID {
		t.Errorf("the seam's caller carries the client-named tenant %q, want the token's %q", caller.TenantID, want.TenantID)
	}
	if caller.DirectoryID != want.DirectoryID {
		t.Errorf("the seam's caller carries the directory %q, want the token's verified tid %q: D2207's reseller rule reads it", caller.DirectoryID, want.DirectoryID)
	}

	// The other direction, whole: a caller the seam answered, as the sign-in the
	// rules are written over and back, is the same caller field for field — so
	// neither conversion drops one, whichever way a later rule reaches it.
	if back := callerOf(signInOf(caller)); back != caller {
		t.Errorf("a caller through signInOf and back is %+v, want the one Verify answered %+v", back, caller)
	}

	// And each rule reads what it carried, through the seam rather than through
	// the conversion alone: an operations organisation admits a Microsoft sign-in
	// nowhere, and a reseller's list admits this one for the directory the
	// verified token reported and refuses it for a directory nobody pinned.
	if err := seam.RequireSSO(caller, SSORule{Operations: true}); !errors.Is(err, ErrSSORequired) {
		t.Errorf("an operations rule answered a Microsoft caller %v, want the SSO refusal", err)
	}
	if err := seam.RequireSSO(caller, SSORule{MicrosoftDirectories: []string{portalDirectory}}); err != nil {
		t.Errorf("a reseller's list that pins this sign-in's directory refused it (%v): the provider and the directory did not cross the seam", err)
	}
	if err := seam.RequireSSO(caller, SSORule{MicrosoftDirectories: []string{portalOtherDirectory}}); !errors.Is(err, ErrDirectoryNotPinned) {
		t.Errorf("a reseller's list pinning another directory answered %v, want the directory refusal: the rule read the caller's own directory", err)
	}
	if err := v.FreshWithin(signInOf(caller), 5*time.Minute); err != nil {
		t.Errorf("a sign-in a minute old was not fresh within five minutes (%v): the instant did not cross the seam", err)
	}
	if err := v.FreshWithin(signInOf(caller), 30*time.Second); !errors.Is(err, ErrStaleSignIn) {
		t.Errorf("a sign-in a minute old was treated as fresh within thirty seconds (%v): the rule read the caller's own sign-in instant", err)
	}

	// The admitted direction, so that none of the above is a seam that refuses
	// everything: a Google caller without a tenant is the caller an operations
	// organisation admits.
	google, err := seam.Verify(ctx, signPortalToken(t, portalKey(), portalKid, "RS256", portalClaims()))
	if err != nil {
		t.Fatalf("the seam refused a Google token this project signed: %v", err)
	}
	if google.Provider != signInGoogle || google.TenantID != "" || google.DirectoryID != "" {
		t.Errorf("a Google sign-in arrived as provider %q, tenant %q and directory %q, want google.com with neither", google.Provider, google.TenantID, google.DirectoryID)
	}
	if err := seam.RequireSSO(google, SSORule{Operations: true}); err != nil {
		t.Errorf("an operations rule refused a Google caller: %v", err)
	}

	// What the seam refuses is this verifier's refusal and not one of its own: a
	// token from another project is the same sentinel the API's door branches
	// on, answered by the verifier through the adapter.
	elsewhere := portalClaims()
	elsewhere["aud"] = portalOtherProject
	elsewhere["iss"] = "https://securetoken.google.com/" + portalOtherProject
	_, err = seam.Verify(ctx, signPortalToken(t, portalKey(), portalKid, "RS256", elsewhere))
	if !errors.Is(err, ErrUnverifiedSignIn) {
		t.Errorf("the seam answered another project's token with %v, want ErrUnverifiedSignIn: the adapter is a pass-through and not a second verifier", err)
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
	if signIn.Member != "user:Owner@example.com" {
		t.Errorf("the sign-in's member is %q, and access.Member keeps the local part byte for byte: the member of Owner@Example.COM is user:Owner@example.com, and a member folded whole is one a stored record spelled that way can never equal", signIn.Member)
	}
	folded, err := access.Member(signIn.Email)
	if err != nil {
		t.Fatalf("access.Member(%q): %v", signIn.Email, err)
	}
	if signIn.Member == folded {
		t.Error("the member was built from the folded address: access.Member of owner@example.com is another member, and merging the two spellings admits a person the tree's own rule calls somebody else")
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
		"not valid yet":                    {token: schedulerToken(func(c map[string]any) { c["nbf"] = portalNow.Add(2 * time.Hour).Unix() })},
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

	// A key source that answers neither a key nor an error is the one shape the
	// refusal above cannot come from the source itself, and the answer has to be
	// a refusal rather than a signature check over nothing.
	empty, err := NewSchedulerVerifier(portalSchedulerAccount, portalSchedulerAudience, noKeys{})
	if err != nil {
		t.Fatalf("NewSchedulerVerifier over an empty set: %v", err)
	}
	if err := empty.Verify(context.Background(), signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims())); !errors.Is(err, ErrUnverifiedSchedulerToken) {
		t.Errorf("a key set that answers no key: refused with %v, want the sentinel the route answers 403 from", err)
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

// TestASchedulerTokenIsOneStringAndOneObject: a Scheduler token is the string
// Google signed and exactly one JSON object per segment, and every spelling of
// one that a permissive decoder would read as the token is refused here.
//
// The newline case is the load-bearing one, and mcpauth's copy of this rule is
// held by TestANewlineInASpentTokensSignatureIsRefused for the same reason: Go's
// base64 decoder ignores a CR or an LF ANYWHERE in its input — the Strict decode
// included — and verifyOIDCRS256 hashes the segments as the strings they are, so
// a newline inside the SIGNATURE segment leaves the signature verifying while
// the token is another string. The header and the payload are the signing input
// and a byte added there breaks the signature by itself, which is why the
// signature segment is the one a decoder has to read as it was written.
func TestASchedulerTokenIsOneStringAndOneObject(t *testing.T) {
	v := schedulerVerifier(t)
	valid := signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims())

	// fresh signs the deployment's own claims again: a case that rewrites a
	// segment needs a token whose signature is over what it rewrites, or the
	// refusal would be the signature's rather than the decoder's.
	fresh := func() string { return signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims()) }

	object, err := json.Marshal(map[string]any{"alg": "RS256", "kid": portalKid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling a header: %v", err)
	}
	// twoObjects is a payload carrying the deployment's own claim object and a
	// second object after it, signed as it stands: every claim in the first is one
	// the verifier admits, so the single-object guard is the only thing that can
	// refuse it.
	twoObjects, err := json.Marshal(schedulerClaims())
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }

	// splice writes raw into one segment as its own base64url text: the shape a
	// presenter controls, where appending a byte would change nothing.
	splice := func(i int, raw string) string {
		parts := strings.Split(fresh(), ".")
		parts[i] = encode(raw)
		return strings.Join(parts, ".")
	}

	cases := map[string]string{
		"a LF inside the signature":        segmentWith(fresh(), 2, "\n"),
		"a CRLF inside the signature":      segmentWith(fresh(), 2, "\r\n"),
		"a space inside the signature":     segmentWith(fresh(), 2, " "),
		"padding on the signature":         segmentWith(fresh(), 2, "="),
		"a byte of no shape in the header": segmentWith(fresh(), 0, "\x7f"),
		"a LF inside the payload":          segmentWith(fresh(), 1, "\n"),
		"an empty header segment":          splice(0, ""),
		"an empty payload segment":         splice(1, ""),
		"an empty signature segment":       splice(2, ""),
		"two objects in one payload":       signSegments(t, portalKey(), string(object), string(twoObjects)+`{"iss":"y"}`),
		"a payload that is not an object":  splice(1, `[1,2]`),
		"a payload past the claims bound":  splice(1, `{"iss":"`+strings.Repeat("a", maxOIDCClaimsBytes)+`"}`),
		"a header past its own bound":      splice(0, string(object)+strings.Repeat(" ", maxOIDCHeaderBytes)),
		"a token past its own size bound":  strings.Repeat("a", maxOIDCTokenBytes+1),
		"a token that is not a JWT at all": portalMarker,
	}

	if err := v.Verify(context.Background(), valid); err != nil {
		t.Fatalf("the Scheduler verifier refused the token this deployment's job presents: %v", err)
	}
	for name, tok := range cases {
		err := v.Verify(context.Background(), tok)
		if err == nil {
			t.Errorf("%s: the Scheduler verifier admitted it (%s)", name, portalMarker)
			continue
		}
		if !errors.Is(err, ErrUnverifiedSchedulerToken) {
			t.Errorf("%s: refused with %v, which is not the sentinel the route answers 403 from", name, err)
		}
	}
}

// segmentWith appends lit to one segment of a compact JWT: the bytes a
// presenter would have written, kept as they are rather than re-encoded.
func segmentWith(tok string, i int, lit string) string {
	parts := strings.Split(tok, ".")
	parts[i] += lit
	return strings.Join(parts, ".")
}

// outageKeys is the key set that could not be read at all — an endpoint that
// timed out or answered an error. It is the shape a verifier has to pass
// through rather than answer as a refusal of the caller.
type outageKeys struct{}

func (outageKeys) Key(context.Context, string) (*rsa.PublicKey, error) {
	return nil, fmt.Errorf("%w: the published set could not be read", mcpauth.ErrUnavailable)
}

// TestAKeySetOutageIsNotARefusalOfTheCaller: an unreadable key set is this
// service's failure and not the caller's, so it reaches the route as
// ErrUnavailable rather than as "not you". The distinction is deliberate — a
// route that answered a sign-in refusal for its own outage would send every
// person to sign in again — and it is what a caller branches on.
func TestAKeySetOutageIsNotARefusalOfTheCaller(t *testing.T) {
	ctx := context.Background()

	portal, err := NewPortalVerifier(portalProject, outageKeys{}, WithPortalClock(func() time.Time { return portalNow }))
	if err != nil {
		t.Fatalf("NewPortalVerifier over an unreadable key set: %v", err)
	}
	_, err = portal.Verify(ctx, signPortalToken(t, portalKey(), portalKid, "RS256", portalClaims()))
	err = mustRefuse(t, "a portal sign-in", err)
	if !errors.Is(err, mcpauth.ErrUnavailable) {
		t.Errorf("the portal verifier answered an unreadable key set with %v, and the caller cannot tell it from a bad token", err)
	}
	if errors.Is(err, ErrUnverifiedSignIn) {
		t.Errorf("the portal verifier answered its own outage as a refused sign-in (%v): every person would be sent to sign in again", err)
	}

	scheduler, err := NewSchedulerVerifier(portalSchedulerAccount, portalSchedulerAudience, outageKeys{}, WithSchedulerClock(func() time.Time { return portalNow }))
	if err != nil {
		t.Fatalf("NewSchedulerVerifier over an unreadable key set: %v", err)
	}
	err = mustRefuse(t, "a Scheduler token", scheduler.Verify(ctx, signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims())))
	if !errors.Is(err, mcpauth.ErrUnavailable) {
		t.Errorf("the Scheduler verifier answered an unreadable key set with %v, and the caller cannot tell it from a bad token", err)
	}
	if errors.Is(err, ErrUnverifiedSchedulerToken) {
		t.Errorf("the Scheduler verifier answered its own outage as a refused token (%v): the job's failure would read as a stranger's token", err)
	}
}

// mustRefuse returns err, failing when there is none: every case it is called
// for is a refusal, and a nil would make the assertions below say nothing.
func mustRefuse(t *testing.T, what string, err error) error {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was admitted over a key set that could not be read", what)
	}
	return err
}
