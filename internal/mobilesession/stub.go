package mobilesession

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
)

// This file is the scaffolding of C1, and it is the one part of this package
// that goes away: I1 wires the sides and deletes this file, which is why every
// stub lives here and nothing the sides keep does.
//
// Each stub returns the shape it is named for and decides nothing: it verifies
// no assertion, reads no roster and no member record, admits nobody, refuses
// nobody, spends no nonce, rotates no token and mints nothing. That is the
// whole of what a stub may do while the assertion service, the pairing routes,
// the project records and the app are written in parallel (A1, P1-P4, K1-K3,
// M1-M3): the callers compile against a signature that exists and their own
// tests inject their own doubles, and none of them can pass on a decision no
// side has made, because nothing here makes one.
//
// What every stub answers with is the fail-closed zero value the contract
// fixes: no claims, no role, no admission, no token, and false from every store
// that reports whether something happened. A caller that treated one of these
// answers as a decision would be reading a decision out of a stub that made
// none, and TestStubsDecideNothing is what holds that.

// StubVerifier is the shape of a verifier and nothing else: it returns the zero
// Claims for every assertion, which is an empty identity — no issuer, no
// audience, no subject, no email, EmailVerified false, AuthTime zero — and
// carries no error, because the failure of a stub is not a judgement about the
// assertion it was handed. Nothing about the argument reaches the answer: an
// assertion is not parsed, not decoded and not looked at.
func StubVerifier() Verifier { return stubVerifier{} }

type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string, Binding) (Claims, error) {
	return Claims{}, nil
}

// StubPolicy is the shape of a policy and nothing else: it refuses, which is
// what Verdict's zero value means, and it names no code, because a code is the
// answer to a question this stub did not ask. It reads no roster, no member
// record and no project policy, so the caller learns nothing about the identity
// it passed — which is the point: P3, P4 and K2 must each inject a policy of
// their own to test anything.
func StubPolicy() Policy { return stubPolicy{} }

type stubPolicy struct{}

func (stubPolicy) Allow(context.Context, string, Claims) (Verdict, error) {
	return Verdict{}, nil
}

// CrashReports answers off: the stub reads no published policy, so it holds no
// switch to report, and off is the fail-closed value the contract fixes — the
// same answer a real policy gives for a key it does not carry, and the one that
// sends nothing (D734). A caller that read an on out of this stub would be
// reading a decision no stub is allowed to make.
func (stubPolicy) CrashReports(context.Context, string) (bool, error) { return false, nil }

// StubInviteStore is the shape of an invite store and nothing else: it finds no
// invite for any nonce and spends nothing, which is the same answer a store
// gives for a nonce that was never issued. It holds no state, so two calls with
// one nonce are two misses rather than one use.
func StubInviteStore() InviteStore { return stubInviteStore{} }

type stubInviteStore struct{}

func (stubInviteStore) Consume(context.Context, string, string) (Invite, bool, error) {
	return Invite{}, false, nil
}

// StubMemberStore is the shape of a member store and nothing else: it remembers
// no member, records nothing and removes nothing. Member's false is the answer
// for an address nobody has paired with yet, which is what every address is to
// this stub — so a policy that admits on the strength of a member record reads
// no record at all and admits nobody.
func StubMemberStore() MemberStore { return stubMemberStore{} }

type stubMemberStore struct{}

func (stubMemberStore) Member(context.Context, string, string) (Member, bool, error) {
	return Member{}, false, nil
}

func (stubMemberStore) Record(context.Context, string, Member) error { return nil }

func (stubMemberStore) Remove(context.Context, string, string) error { return nil }

// StubRefreshStore is the shape of a refresh store and nothing else: it issues
// no family and rotates no token, so every grant it returns is empty — no
// token, no family, no expiry — and every revoke ends nothing. A caller that
// handed an empty token to a phone would be handing it a session that cannot be
// presented, which is the fail-closed end of a store nobody has written.
func StubRefreshStore() RefreshStore { return stubRefreshStore{} }

type stubRefreshStore struct{}

func (stubRefreshStore) Issue(context.Context, string, string) (RefreshGrant, error) {
	return RefreshGrant{}, nil
}

func (stubRefreshStore) Rotate(context.Context, string, string) (RefreshGrant, error) {
	return RefreshGrant{}, nil
}

func (stubRefreshStore) Revoke(context.Context, string, string) error { return nil }

// StubMinter is the shape of a minter and nothing else: it mints no token and
// signs nothing, so what it returns is an empty CustomToken with a zero expiry.
// It holds no signer grant, which is what keeps this package free of the
// device-only signing account until A1's real minter is wired.
func StubMinter() Minter { return stubMinter{} }

type stubMinter struct{}

func (stubMinter) Mint(context.Context, MintRequest) (CustomToken, error) {
	return CustomToken{}, nil
}

// StubClaimStore is the shape of a claim store and nothing else: it claims
// nothing, so every call reports false — the answer that says "someone else has
// this value" — and single-use is total while nothing is wired. False is never
// an error here, which is the contract's rule: a value already spent is the
// ordinary case the store exists for.
func StubClaimStore() ClaimStore { return stubClaimStore{} }

type stubClaimStore struct{}

func (stubClaimStore) Claim(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}

// StubSessionResponse is the shape of a session response and nothing else: it
// carries the project and the endpoint the caller named — the two facts every
// route already holds — and no credential of any kind. Every token field is
// empty, the role is access.RoleNone and the policy is the zero SessionPolicy,
// so a phone handed this answer can reach nothing and a caller that logged it
// would log no secret. The consumer key is empty too: a stub pins nothing,
// because pinning is a decision about which key is the project's.
func StubSessionResponse(project, endpoint string) SessionResponse {
	return SessionResponse{ProjectID: project, Endpoint: endpoint, Role: access.RoleNone}
}

// StubRefreshResponse is the shape of a refresh response and nothing else: no
// successor token, no custom token, no expiry, RoleNone and the zero policy. A
// refresh that answered this would leave the phone holding a session with
// nothing in it, which is the failure it should have.
func StubRefreshResponse() RefreshResponse { return RefreshResponse{} }

// StubError is a refusal and nothing else: it carries the code it was handed
// and the empty message, and it decides nothing about who is refused. It exists
// so that a caller can compile its refusal path and render a code before the
// rules that choose one are written; a handler that reaches it in production is
// a handler that refused for no reason it can name.
func StubError(code Code) Error { return Error{Code: code} }
