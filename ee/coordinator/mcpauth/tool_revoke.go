// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
)

// This file is M10's revocation tool: the person's own surface for the one
// delete D930 makes revocation (spec.md §6, "either way it is one delete, and
// the next call with the revoked token is refused").
//
// **It is the same delete as POST /oauth/revoke and not a second one.** The
// endpoint is the API's surface (token.go's serveRevoke, which authenticates
// the client that presents a token) and this is the surface a person has at the
// front door, where the only credential anybody holds is family id; both call
// Store.RevokeFamily and nothing else, so there is one revocation in this
// package and two ways to ask for it. It is idempotent and unconditional, which
// is the store's own rule: a revoke must not lose a race with the rotation it
// is revoking, so the delete is never generation-matched.
//
// **Who may call it, and which family.** An operator (D933's split gives a
// launcher the task tools and nothing else), and the family id it takes is the
// one the audit record names (audit_record.go's AuditEntry.FamilyID). That is
// the pair the design asks for and the reason the record carries a family at
// all: a call that was not the person's own is visible in their record, the
// record names the family that made it, and the operator ends that family
// through this tool rather than through a console this package does not have.
//
// What it does not take is a token. A family id is an id — it names a family
// and is not a value any door accepts — so this tool has nothing a caller could
// present afterwards, and nothing it could write into a line (spec.md §9).

// revokeFamilyInputSchema is the form a client renders for this tool: one
// family id, which is the `family_id` the audit record's entries carry.
const revokeFamilyInputSchema = `{"type":"object","properties":{"family_id":{"type":"string","description":"The token family to revoke, as the audit record names it"}},"required":["family_id"],"additionalProperties":false}`

// RevokeFamilyTool is the person's own surface for revocation, ready to
// register: one family id in, one delete out.
//
// Its answer is data and says what the delete did — the family it ended and the
// fact that every token of it now resolves to nothing — rather than echoing
// anything about the family's tokens, which this tool never reads. A family id
// no family answers to is not an error: the store's revoke is idempotent and
// unconditional (D930), and a second revocation of a family somebody else
// already ended has to be the answer "it is revoked" rather than a refusal that
// leaves an operator unsure which of the two happened.
func RevokeFamilyTool(store Store) Tool {
	return Tool{
		Name:        "revoke_family",
		InputSchema: json.RawMessage(revokeFamilyInputSchema),
		Handler: func(ctx context.Context, call Call) (any, error) {
			if !call.Role.Operator {
				return nil, ErrOperatorOnly
			}
			var args struct {
				FamilyID string `json:"family_id"`
			}
			if err := argumentsOf(call.Arguments, &args); err != nil {
				return nil, err
			}
			if err := store.RevokeFamily(ctx, args.FamilyID); err != nil {
				// The store's own error stays out of what the caller reads: it
				// names this installation's objects, and the caller can do
				// nothing with it. The sentinel is what callTool maps to the
				// door's own fault, so a revocation that could not be made is
				// not recorded as one this door refused.
				return nil, fmt.Errorf("%w: the family was not revoked", ErrStoreUnreadable)
			}
			return map[string]any{
				"family_id": args.FamilyID,
				"revoked":   true,
			}, nil
		},
	}
}
