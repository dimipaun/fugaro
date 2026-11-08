// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import "fmt"

// This file is M7's scope half of the front door: how the scopes a token
// carries are read at the door that enforces them, and how a refusal names
// one. The vocabulary itself is M1's (limits.go) — D927's two names — and
// KnownScope is the only test a scope is put to, so a value outside it cannot
// be a near miss of one.
//
// Which scope a tool needs is tools.go's catalogue and not this file's: the
// mapping is a statement about the tool set ("every tool maps to a scope",
// finding M-e), and the registry is the file that decides what a client can
// call.

// CheckScopes reads the scopes a token carries on the door's side, and it is
// the one place this door decides whether a token's scopes may be read at all.
//
// An unknown scope is a refusal and never a narrowing (D927), and this is the
// door's half of a rule the mint already applies: a scope is a wire contract
// the moment a token can hold it, so a door that dropped a name its build does
// not know would serve the call as if the operator had minted less than they
// did — the holder would see a token that works for some calls and is refused
// on others with nothing saying why, and the unknown name would reach nobody
// who could fix it. The refusal is of the whole token: it is not usable here
// until it is minted again against a build that knows every name it carries,
// and the caller is told which name this build does not know, which is a value
// out of the caller's own token and never the token itself.
//
// A token that reaches no tool at all is refused for the same reason in the
// other direction: Limits.Validate refuses to mint one, and a door that
// admitted one would turn that mistake into a per-tool refusal everywhere,
// where it reads as a tools/list that answers nothing rather than as the
// deployment mistake it is.
func CheckScopes(scopes []string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("mcpauth: the token carries no scope: %w", ErrNoScope)
	}
	for _, s := range scopes {
		if !KnownScope(s) {
			return fmt.Errorf("mcpauth: the token carries the scope %q, which this build does not know: %w", s, ErrUnknownScope)
		}
	}
	return nil
}
