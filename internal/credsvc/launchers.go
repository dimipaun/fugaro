// launchers.go is the launcher class's own construction: the check
// VerifyLauncher makes, as a value a process that verifies launchers and
// nothing else can hold (the intake, DS2.1).
//
// Why it exists beside AuthConfig, and not as one more constructor of it:
// NewAuthenticator requires everything the SERVICE checks — the installation's
// job accounts, Google's Firebase key set and a reader of the run outcomes —
// and the intake holds none of them and may hold none of them. It verifies one
// class of caller, over Google's ID-token keys, and it holds no database role
// at all (D1940, R10): the roster reaches it as a bucket object and nothing
// else. A construction that demanded the three would make the intake either
// hold a reader it must not have or pass a stand-in for one, and a service
// built on a stand-in is one that admits what it cannot check.
//
// The check itself is NOT spelled twice. verifyLauncherCaller is the one body
// of it and callerClaimsFrom the one token check under it, and
// Authenticator.VerifyLauncher is a call to the same function with its own key
// set and list (DS1.2, DS1.3) exactly as Launchers.VerifyLauncher is. A rule
// added to either reaches both, which is the whole reason the class is a
// lookup in a list and not a second token check.
//
// The roster's SERVICE ACCOUNTS are the second class this construction holds
// (LM3, D3123): a `serviceAccount:` member of the roster presents a Google ID
// token exactly as a person does, and the class it is admitted as is what tells
// the two apart. It is a list of its own and not an entry of the launcher
// list's — that list is people and refuses the shape (F4, G13) — and the two
// classes are two entry points rather than one (VerifyLauncher and
// VerifyServiceAccountLauncher), so the class a caller is admitted as is the
// entry point that admitted it and never a flag a caller could be read
// differently by. The bodies follow the same rule as the launcher class's own:
// verifyServiceAccountCaller is the one body of the service-account class, and
// both constructions call it.

package credsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LauncherConfig is the launcher class's whole configuration: the audience a
// launcher's Google ID token must carry, the addresses the class admits, and
// the key set those tokens are signed with.
type LauncherConfig struct {
	// Audience is the audience every launcher's ID token must carry: the
	// process's own URL. GcloudAudience is admitted beside it by the class's
	// own line (checkCallerAudience), for the person class's reason.
	Audience string
	// Accounts are the identities the class admits: the installation's roster
	// as `update` derives it, one bare `email` claim each, in the spelling the
	// deployment configured. An empty list is a statement rather than a
	// missing gate — an installation whose roster names no launcher admits
	// nobody — and a `*.gserviceaccount.com` address is a configuration error
	// rather than an entry that could never be a caller, which is
	// NewAuthenticator's own rule for the same list: the class is people, and
	// the roster's service accounts are the class below.
	Accounts []string
	// ServiceAccounts are the roster's service accounts (LM3, D3123): the
	// class a person's list cannot hold, each a bare `*.gserviceaccount.com`
	// address and never a `group:` or `domain:` entry, which holds no token to
	// present. An address that is NOT a service account's is a configuration
	// error here for the reason the launcher list refuses one there: the two
	// lists are two classes, and a deployment that put a person on this one
	// would have an entry no caller of the class could ever match.
	//
	// An empty list is a statement rather than a missing gate, like the
	// launcher list's: an installation whose roster names no service account
	// has a class nobody is in, and every caller of it is refused.
	ServiceAccounts []string
	// Keys is Google's published key set for Google ID tokens
	// (GoogleOAuthCertsURL): the keys the callers' tokens are signed with. It
	// is not the Firebase set, which signs run identities this class does not
	// hold.
	Keys KeySource
	// Now is the clock, for tests; nil means time.Now.
	Now func() time.Time
}

// Launchers verifies the launcher class's callers and the roster's service
// accounts, and nothing else: it holds no run identity, no job account and no
// reader of the installation's state.
type Launchers struct {
	keys            KeySource
	accounts        map[string]string
	serviceAccounts map[string]string
	audience        string
	now             func() time.Time
}

// NewLaunchers validates the configuration and returns the verifier. It
// refuses a configuration it cannot check with — no audience, no key set, an
// address that is not printable ASCII, or an address of the wrong shape for
// the list it is on (a service account's on the launcher list, a person's on
// the service-account list) — and an empty account list is none of those: it
// is a class that admits nobody, which is fail closed and not a missing gate.
func NewLaunchers(cfg LauncherConfig) (*Launchers, error) {
	switch {
	case cfg.Audience == "":
		return nil, errors.New("credsvc: no audience: a launcher's Google ID token could not be checked")
	case cfg.Keys == nil:
		return nil, errors.New("credsvc: no key source: a launcher's ID token signature could not be checked")
	}
	accounts, err := foldLauncherAccounts(cfg.Accounts)
	if err != nil {
		return nil, err
	}
	serviceAccounts, err := foldLauncherServiceAccounts(cfg.ServiceAccounts)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Launchers{keys: cfg.Keys, accounts: accounts, serviceAccounts: serviceAccounts, audience: cfg.Audience, now: now}, nil
}

// VerifyLauncher admits a launcher-class caller, or refuses it exactly as
// Authenticator.VerifyLauncher does: the same calls, in the same order, over
// this construction's own key set, list and audience.
func (l *Launchers) VerifyLauncher(ctx context.Context, tok string) (LauncherCaller, error) {
	return verifyLauncherCaller(ctx, l.keys, l.accounts, l.audience, l.now(), tok)
}

// VerifyServiceAccountLauncher admits a caller of the roster's service-account
// class, or refuses it exactly as Authenticator.VerifyServiceAccountLauncher
// does: the same calls, in the same order, over this construction's own key
// set, list and audience. It is a class of its own and not a second list of
// the launcher's: the entry point a caller is admitted by is what its class
// is, and the two lists cannot hold one address (each refuses the other's
// shape).
func (l *Launchers) VerifyServiceAccountLauncher(ctx context.Context, tok string) (LauncherCaller, error) {
	return verifyServiceAccountCaller(ctx, l.keys, l.serviceAccounts, l.audience, l.now(), tok)
}

// foldLauncherAccounts builds the launcher list's lookup: the folded address a
// presented claim is matched by, mapped to the configured spelling the
// identity carries and the audit line writes.
//
// It is NewAuthenticator's own loop for the launcher list, and it is one
// function because the two constructions have to agree about the class: the
// service's Authenticator and the intake's Launchers admitting different
// spellings of the same roster would be two classes wearing one name. The rule
// it carries beyond the fold is the person list's — an entry may not be a
// `*.gserviceaccount.com` address, because the class is people and a service
// account is a job of this installation or of another — and it is a
// configuration error here rather than an admission that silently admits
// nobody (see AuthConfig.LauncherAccounts).
func foldLauncherAccounts(emails []string) (map[string]string, error) {
	accounts := make(map[string]string, len(emails))
	for _, email := range emails {
		folded, ok := foldEmail(email)
		if !ok {
			return nil, errors.New("credsvc: a launcher account is not a printable ASCII email address")
		}
		if strings.HasSuffix(folded, ".gserviceaccount.com") {
			return nil, errors.New("credsvc: a launcher account is a service account address, and the launcher class is the people who launch runs")
		}
		accounts[folded] = email
	}
	return accounts, nil
}

// foldLauncherServiceAccounts builds the roster's service-account class's
// lookup: the folded address a presented claim is matched by, mapped to the
// configured spelling the identity carries and the audit line writes.
//
// It is the launcher list's fold with the shape rule turned around, and the
// two rules are one rule read from two sides: an entry of THIS list must be a
// `*.gserviceaccount.com` address, because the class is the roster's service
// accounts and a person's address here is an entry no caller of the class
// could match — and, the other way, the launcher list refuses exactly that
// shape, because its class is people (D1938, G13). Nothing else differs: the
// fold is the same ASCII one every list in this package is read by, and a
// value that is not printable ASCII is a configuration error rather than a
// class that silently admits nobody.
//
// What the shape rules buy beyond the two refusals is that the two lists are
// disjoint by construction: no address is both a person's and a service
// account's, so a caller cannot be admitted as one class by one entry point
// and as the other by the other.
func foldLauncherServiceAccounts(emails []string) (map[string]string, error) {
	accounts := make(map[string]string, len(emails))
	for _, email := range emails {
		folded, ok := foldEmail(email)
		if !ok {
			return nil, errors.New("credsvc: a roster service account is not a printable ASCII email address")
		}
		if !strings.HasSuffix(folded, ".gserviceaccount.com") {
			return nil, errors.New("credsvc: a roster service account is not a service account address, and the class is the roster's service accounts")
		}
		accounts[folded] = email
	}
	return accounts, nil
}

// verifyLauncherCaller is the launcher class's whole check, and the one body
// of it: the service's Authenticator and the intake's Launchers differ in what
// they hold and not in what they check. It answers the account and the account
// id the token names, or the zero value with an error — never a partially
// verified caller.
func verifyLauncherCaller(ctx context.Context, keys KeySource, accounts map[string]string, audience string, now time.Time, tok string) (LauncherCaller, error) {
	claims, folded, err := callerClaimsFrom(ctx, keys, tok)
	if err != nil {
		return LauncherCaller{}, err
	}
	account, ok := accounts[folded]
	if !ok {
		// The reason names the class and never the address: it is the caller's
		// own claim set, and the package doc's no-echo rule is that no refusal
		// repeats what a caller presented.
		return LauncherCaller{}, fmt.Errorf("%w: the caller is not one of this installation's launchers, so it may not be granted a slip", ErrUnverifiedJob)
	}
	// The audience is this class's own line, and it admits the two the person
	// class admits (checkCallerAudience is per class for exactly this reason).
	if err := checkCallerAudience(claims, audience, GcloudAudience); err != nil {
		return LauncherCaller{}, err
	}
	if err := checkCallerToken(claims, now); err != nil {
		return LauncherCaller{}, err
	}
	sub, ok := launcherSub(claims)
	if !ok {
		// The refusal names the claim and never its content: a `sub` this class
		// will not carry is caller text, and the package's no-echo rule holds
		// for it as it does for the key id. It is not ErrUnverified: the
		// identity is not in doubt — what is missing is the one fact the routes
		// that consume this class need, and there is nothing a caller could fix
		// by presenting another token of the same account.
		return LauncherCaller{}, fmt.Errorf("%w: the caller's ID token carries no Google account id (a numeric sub), so no secret could be written under its prefix", ErrUnverifiedJob)
	}
	return LauncherCaller{Account: account, Sub: sub}, nil
}

// verifyServiceAccountCaller is the roster service-account class's whole check,
// and the one body of it: the service's Authenticator and the intake's
// Launchers differ in what they hold and not in what they check, exactly as
// they do for the launcher class. It answers the account and the account id
// the token names, or the zero value with an error — never a partially
// verified caller.
//
// It is verifyLauncherCaller's body with two lines of its own, and both are
// the class's own fact rather than a spelling of the launcher's:
//
//   - the audience is THIS SERVICE'S OWN URL ALONE. The launcher class admits
//     gcloud's client id beside it (GcloudAudience) because a launcher's token
//     is minted by a person's own credentials, which cannot mint for another
//     audience at all; a service account's token is obtained on purpose, with
//     the audience asked for, so the audience a person's ambient credentials
//     produce is refused here whatever list it was matched against — the rule
//     VerifyOrchestrator, VerifyBuildCaller and VerifyRepoBuild already follow
//     for every class whose caller is not a person.
//   - the list is the roster's service accounts, and the refusal names the
//     class and never the address, for the launcher class's reason: the
//     address is the caller's own claim set and no refusal repeats what a
//     caller presented.
//
// Everything else is shared and deliberately not respelled: the signature, the
// issuer and the ASCII fold (callerClaimsFrom), the token's own window
// (checkCallerToken), and the account id the class answers with (launcherSub —
// the same shape rule, because the class answers the same value the launcher's
// routes key on).
func verifyServiceAccountCaller(ctx context.Context, keys KeySource, accounts map[string]string, audience string, now time.Time, tok string) (LauncherCaller, error) {
	claims, folded, err := callerClaimsFrom(ctx, keys, tok)
	if err != nil {
		return LauncherCaller{}, err
	}
	account, ok := accounts[folded]
	if !ok {
		return LauncherCaller{}, fmt.Errorf("%w: the caller is not one of this installation's roster service accounts, so it may not be admitted as one", ErrUnverifiedJob)
	}
	if err := checkCallerAudience(claims, audience); err != nil {
		return LauncherCaller{}, err
	}
	if err := checkCallerToken(claims, now); err != nil {
		return LauncherCaller{}, err
	}
	sub, ok := launcherSub(claims)
	if !ok {
		return LauncherCaller{}, fmt.Errorf("%w: the caller's ID token carries no Google account id (a numeric sub), so the caller cannot be named by one", ErrUnverifiedJob)
	}
	return LauncherCaller{Account: account, Sub: sub}, nil
}

// callerClaimsFrom is the caller's ID token's check: the signature, the
// issuer, and the fold of the address claim, over a key set this function is
// handed rather than one it holds. It is callerClaims' body, and it is here
// rather than there so that a process verifying one class of caller over
// Google's ID-token keys alone (Launchers) runs the same check as the service
// and not a second spelling of it.
func callerClaimsFrom(ctx context.Context, keys KeySource, tok string) (map[string]any, string, error) {
	head, claims, err := decodeJWT(tok)
	if err != nil {
		return nil, "", fmt.Errorf("%w: the caller's ID token is not a JWT", ErrUnverifiedJob)
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return nil, "", fmt.Errorf("%w: the caller's ID token is not signed with RS256", ErrUnverifiedJob)
	}
	key, err := keys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			return nil, "", fmt.Errorf("%w: %w", ErrUnverifiedJob, err)
		}
		return nil, "", err
	}
	if err := verifySignature(tok, key); err != nil {
		return nil, "", fmt.Errorf("%w: the caller's ID token's signature does not verify", ErrUnverifiedJob)
	}
	if iss := claimString(claims, "iss"); iss != "https://accounts.google.com" && iss != "accounts.google.com" {
		return nil, "", fmt.Errorf("%w: the caller's ID token was not issued by Google", ErrUnverifiedJob)
	}
	folded, ok := foldEmail(claimString(claims, "email"))
	if !ok {
		return nil, "", fmt.Errorf("%w: the caller's email claim is not a service account address", ErrUnverifiedJob)
	}
	return claims, folded, nil
}
