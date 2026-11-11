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
	// NewAuthenticator's own rule for the same list.
	Accounts []string
	// Keys is Google's published key set for Google ID tokens
	// (GoogleOAuthCertsURL): the keys the callers' tokens are signed with. It
	// is not the Firebase set, which signs run identities this class does not
	// hold.
	Keys KeySource
	// Now is the clock, for tests; nil means time.Now.
	Now func() time.Time
}

// Launchers verifies the launcher class's callers, and nothing else: it holds
// no run identity, no job account and no reader of the installation's state.
type Launchers struct {
	keys     KeySource
	accounts map[string]string
	audience string
	now      func() time.Time
}

// NewLaunchers validates the configuration and returns the verifier. It
// refuses a configuration it cannot check with — no audience, no key set, an
// address that is not printable ASCII, or an address that is a service
// account's — and an empty account list is none of those: it is a class that
// admits nobody, which is fail closed and not a missing gate.
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
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Launchers{keys: cfg.Keys, accounts: accounts, audience: cfg.Audience, now: now}, nil
}

// VerifyLauncher admits a launcher-class caller, or refuses it exactly as
// Authenticator.VerifyLauncher does: the same calls, in the same order, over
// this construction's own key set, list and audience.
func (l *Launchers) VerifyLauncher(ctx context.Context, tok string) (LauncherCaller, error) {
	return verifyLauncherCaller(ctx, l.keys, l.accounts, l.audience, l.now(), tok)
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
