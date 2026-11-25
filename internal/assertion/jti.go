package assertion

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
)

// spend is the single use of an assertion: the jti is claimed through C1's
// compare-and-set store, so that the second presentation of one assertion is
// refused by the service rather than by the process that happened to see it
// first. It is a store and not a map here for the reason C1 gives -- two
// instances of the public service are two places a value would otherwise be
// unused in twice (F39) -- and it is C1's store rather than one of this
// package's so that a jti, a restore challenge and an invite nonce are spent
// through one mechanism with one set of namespaces.
//
// The value claimed is the jti as the assertion spells it. It is not hashed:
// it is read out of a payload whose signature has already verified, it is not
// a credential, and a store that held hashes would be a store no operator could
// read while debugging a replay.
//
// What "single use" costs is one store round trip per assertion, and that is
// the price F39 accepts: without it a replay inside the assertion's two minutes
// is a second pairing or a second token, and with a per-process map it is a
// replay the instance next door admits one request later.
func (v *Verifier) spend(ctx context.Context, claims mobilesession.Claims, now time.Time) error {
	// The life the id is remembered for: the assertion's own life plus the
	// skew, so that the store outlives every instant at which this verifier
	// would still accept the assertion. A shorter ttl would leave a window --
	// between the assertion's exp and the store's -- in which the very same
	// assertion is refused for exp and yet its id is free again, which is a
	// window only a replay can be in.
	ttl := time.Unix(claims.ExpiresAt, 0).Sub(now) + ClockSkew
	if ttl <= 0 {
		// Unreachable while Verify's exp check holds. ClockSkew rather than
		// zero, because a store asked for a zero ttl is a store free to forget
		// the id at once, and this function's refusal must never depend on how
		// a store rounds.
		ttl = ClockSkew
	}
	spent, err := v.claims.Claim(ctx, mobilesession.NamespaceJTI, claims.JTI, ttl)
	if err != nil {
		// A store that failed is not an assertion that was refused: nothing
		// about the assertion is known to be wrong, and the verifier refuses
		// anyway, because single use is the store's and an assertion whose id
		// cannot be recorded is an assertion nobody can spend.
		return fmt.Errorf("%w: the assertion's jti could not be claimed: %v", ErrStore, err)
	}
	if !spent {
		return refuse("jti", "this assertion has already been spent")
	}
	return nil
}
