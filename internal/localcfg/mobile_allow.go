package localcfg

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// validateMobileAllow holds the declared allowlist (docs/mobile-auth, D231) to
// the rules the device node it seeds is held to, at the read: every entry is a
// canonical address or domain, carries a role out of the closed set, and names
// a uid the publish can key a node by. A malformed entry is refused, and it is
// never repaired: a role this file cannot parse is an authority nobody granted,
// and the only safe reading of it is none — the same answer mobileauth's
// resolver gives a node carrying one.
//
// It is asked at load and therefore by every command that reads this file,
// `fugaro validate` among them, rather than by the publish alone. The
// declaration is an authority over who may talk to the project's runs, and a
// file whose entries cannot be read is a file a later publish would otherwise
// seed a node from at whatever moment the operator happened to run it.
func (c *Config) validateMobileAllow(bad func(string, ...any)) {
	addr := map[string]mobileauth.Role{}
	uid := map[string]bool{}
	for i, e := range c.MobileAllow {
		at := fmt.Sprintf("mobile_allow[%d]", i)
		address, err := e.Canonical()
		if err != nil {
			bad("%s: address %q %v", at, e.Address, err)
			continue
		}
		role, ok := mobileauth.ParseRole(string(e.Role))
		if !ok {
			bad("%s: role %q is not one of %s: a declared role is an authority, and an entry whose role cannot be read is refused rather than given one", at, e.Role, roleList())
			continue
		}
		if e.UID != "" {
			if why := dbKeyProblem(e.UID); why != "" {
				bad("%s: uid %q %s, and the uid is the key the device node at config/admin/devices is written under", at, e.UID, why)
				continue
			}
			if uid[e.UID] {
				bad("%s: uid %q is declared twice, and one uid is one device node", at, e.UID)
				continue
			}
			uid[e.UID] = true
		}
		if have, seen := addr[address]; seen && have != role {
			bad("%s: %s is declared twice, as %s and as %s; one address resolves to one role, so the two entries have to agree", at, address, have, role)
			continue
		}
		addr[address] = role
	}
}

// dbKeyProblem is why a declared uid cannot be a Firebase Realtime Database
// key, "" when it can. The database refuses a key holding a dot, a hash, a
// dollar, a bracket or a slash (its own rule for a node name), so a declaration
// carrying one would be refused by the write that seeds the node rather than
// here, at the edit that wrote it — and a uid is never repaired by stripping
// the character, because the uid is what the rule that admits the device reads:
// a rewritten uid admits nobody.
func dbKeyProblem(k string) string {
	switch {
	case k == "":
		return "is empty"
	case strings.TrimSpace(k) != k:
		return "has a leading or trailing space"
	case strings.ContainsAny(k, ".#$[]/"):
		return "holds a character a database key may not (. # $ [ ] /)"
	case strings.ContainsAny(k, "\x00\n\r\t"):
		return "holds a control character"
	}
	return ""
}

// roleList names the closed set the way a refusal prints it.
func roleList() string {
	roles := mobileauth.Roles()
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return strings.Join(out, ", ")
}
