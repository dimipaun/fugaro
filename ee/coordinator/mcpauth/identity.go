// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// This file is M7's identity half of the front door: who a token acts for,
// once reaching the endpoint no longer means holding a Google identity (D933).
//
// **Two lists decide it, and they are the installation's own.** A launcher may
// call the task tools and an operator may also read the audit record (D933);
// the lists are the ones `fugaro user` and `fugaro setup` maintain, applied to
// the installation by its owner, and nothing here reads a person's own
// machine: one operator's local config lists the members *that operator* knows
// about, and a front door reading it would admit whoever that machine last
// wrote and refuse whoever it had not heard of — finding M-f.
//
// **Only exact `user:` entries are read** (D923). An entry is one person
// written as `user:<email>`; `group:`, `domain:`, `serviceAccount:`,
// `allUsers` and `allAuthenticatedUsers` are refused, which is the rule
// `internal/cli/user.go`'s `userMember` applies at its own door. Here it is
// enforced by construction rather than by a second list of prefixes: the
// identity is checked to be one `user:` entry before the lists are read at
// all, and an entry is compared to it exactly, so an entry of another kind can
// never be equal to it — a list holding `domain:example.com` beside its
// `user:` entries admits the people it names and no domain.
//
// **The spelling is exact, and it is `internal/cli/roles.go`'s.** That file
// places a member on these lists with `slices.Contains` (operatorMember,
// launcherMember) and never folds a case; the one comparison in the CLI that
// folds is the local self-removal guard's, which says of itself that it is
// "not the rule the lists are read or written by". The addresses on both sides
// are lower-cased where they are derived — `foldEmail` on the token's side and
// `internal/cli/init_members.go` on the CLI's — so the two agree in practice
// and neither side carries a rule the other has to match. What follows is
// stated rather than left to be discovered: a list entry typed with a capital
// letter in the address is not the entry `fugaro user` writes, and the person
// it names is refused here and holds no role at this installation either.

// Members is one installation's member lists, exactly as an operator wrote
// them: every entry verbatim, including the entries this door does not read.
type Members struct {
	// Launchers hold the launcher role: they may call the task tools.
	Launchers []string
	// Operators hold the operator role: they may call the task tools and read
	// the audit record.
	Operators []string
}

// Roster is where the resource server reads the installation's own lists.
//
// It is an interface because what stands behind it is the installation's
// published state — the lists the owner applied, which only an operator can
// write (finding M-f) — and that source is a later task's to name: the front
// door is dormant (D936) and no binary of this repository reaches this package
// yet, so M7 fixes the seam and the rule and leaves the reader to the task
// that wires the door. Nothing that reads one machine's local config may
// implement it, which is the whole of why it is an interface with one method
// rather than a lookup this package performs.
//
// It is read on every request rather than once at startup, because the lists
// are changeable without a deployment: a member removed from the installation
// stops working at the next call, and one added starts.
type Roster interface {
	// Members answers both lists as the installation has them now.
	Members(ctx context.Context) (Members, error)
}

// Role is what one identity holds at this installation, and the half of D933
// that a later tool's own rule reads: an operator may read the audit record
// where a launcher may not.
type Role struct {
	// Launcher is whether the identity is on the launcher list.
	Launcher bool
	// Operator is whether the identity is on the operator list.
	Operator bool
}

// Admitted reports whether a role reaches this door at all. A person on
// neither list is refused before a tool is chosen, because D933's answer is
// that the lists decide who may call anything.
func (r Role) Admitted() bool { return r.Launcher || r.Operator }

// ErrIdentityOffTheLists is the refusal of a caller this installation does not
// admit: an identity that is not one `user:` entry, or one that is and is on
// neither list.
//
// It is a sentinel and not a sentence because the door has to tell it from the
// other way this resolution fails: a caller the lists refuse gets a 403 it can
// do nothing about, and a front door that could not read the lists at all gets
// a 500: reporting the second as the first would tell a person their
// membership was revoked when in fact the service cannot read the lists, and
// they would stop asking. Neither answer says which list was read, or whether
// the address is on one.
var ErrIdentityOffTheLists = errors.New("mcpauth: the identity is on neither the installation's launcher nor its operator list")

// resolveIdentity answers what the identity a token acts for holds at this
// installation, or the refusal of a caller it does not admit.
func (s *Server) resolveIdentity(ctx context.Context, identity string) (Role, error) {
	if s.roster == nil {
		return Role{}, fmt.Errorf("mcpauth: this server was built with no roster, so it admits nobody: the lists come from the installation's own published state, which a later task supplies: %w", ErrIdentityOffTheLists)
	}
	members, err := s.roster.Members(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("mcpauth: the installation's member lists could not be read: %w", err)
	}
	return membership(members, identity)
}

// membership is the rule itself, over lists a caller already has: what the
// identity holds, or ErrIdentityOffTheLists.
func membership(lists Members, identity string) (Role, error) {
	if _, ok := userEntry(identity); !ok {
		return Role{}, fmt.Errorf("mcpauth: %q is not one person written as user:<email>: %w", identity, ErrIdentityOffTheLists)
	}
	var role Role
	for _, entry := range lists.Launchers {
		if entry == identity {
			role.Launcher = true
		}
	}
	for _, entry := range lists.Operators {
		if entry == identity {
			role.Operator = true
		}
	}
	if !role.Admitted() {
		return Role{}, fmt.Errorf("mcpauth: %q is on neither list: %w", identity, ErrIdentityOffTheLists)
	}
	return role, nil
}

// userEntry reports whether an entry is one person written as `user:<email>`,
// and is the form the identity side is held to.
//
// The identity is checked and the list's entries are not, which is deliberate:
// an entry of another kind can never be equal to an identity that passed this,
// so a `domain:`, `group:` or `serviceAccount:` entry is inert here — it is
// read past rather than refused, and a list carrying one beside its people is
// still a list of people. Refusing such a list outright would refuse the whole
// installation for an entry this door does not read.
//
// It is `internal/cli/user.go`'s `userMember` in another file, and it is a
// second implementation of one rule: that function refuses a member the CLI is
// about to write, with the message a person reads at a terminal, and this one
// answers whether an identity a token already carries is one. The two are the
// same test on purpose — an identity this admits is an entry that command
// writes — and a change to either belongs in both.
func userEntry(entry string) (string, bool) {
	if entry != strings.TrimSpace(entry) {
		return "", false
	}
	address, ok := strings.CutPrefix(entry, "user:")
	if !ok || address == "" || !strings.Contains(address, "@") {
		return "", false
	}
	if strings.ContainsAny(address, "*?[] \t\r\n") {
		return "", false
	}
	return entry, true
}
