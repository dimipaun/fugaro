package projectpolicy

import (
	"slices"
	"strings"
)

// The member installation's own admission lists: the two lists `fugaro user`
// writes, read the way the workspace's exchange reads them when a person signs
// in through a workspace (docs/workspaces/spec.md §3 step 3, reconciliation
// D1295, the security review's finding 7).
//
// **The lists are the member's own, and a workspace adds nothing to them.** A
// person reaches this installation through a workspace only when this
// installation's own `terraform.launchers` or `terraform.operators` names them,
// so a workspace cannot make anybody a launcher here and rule 1 of the folder
// survives: the workspace's published record names its members and says nothing
// about who they admit, and the exchange reads these lists without ever writing
// them (D1295 — the member's own lists are the whole of the admission).
//
// **What is read is the addresses, and only the entries that are one person.**
// An entry is read only when it is one person written as `user:<address>`.
// Everything else is inert rather than refused: `domain:`, `group:`,
// `serviceAccount:`, `allUsers` and `allAuthenticatedUsers` name no one person
// (a domain entry in particular would hand every account in a domain the
// launcher and operator roles — F42's own reason for refusing it at `fugaro
// user`), an entry with whitespace around it or inside its address is not one
// address, and an address carrying a wildcard or a bracket is not one either.
// An operator's list is the operator's: an entry this reader cannot read leaves
// the entries beside it exactly as they were, and refusing the whole list for
// it would refuse everybody the operator did name.
//
// The addresses come back as the operator wrote them. **The rule for what an
// address is is applied where the comparison is** — the exchange's admission,
// which holds the verified address and the lists at once and folds both sides
// with mobileauth.NormalizeEmail (the one rule for addresses in this tree, the
// same rule the CLI writes these lists with). This package cannot call it:
// mobileauth reads the allowlist through internal/access, which reads this
// package, so the folding that needs it happens at the decision rather than
// here.
//
// The entry rule below is `internal/cli/user.go`'s userMember and the MCP
// door's own userEntry (ee/coordinator/mcpauth), read from this side, and
// `fugaro user`'s own test holds the spelling this reader admits to the
// spelling that command writes (internal/cli/workspace_admit_test.go).
type MemberLists struct {
	launchers []string
	operators []string
}

// MemberListsOf reads the two lists an installation's configuration holds: the
// raw IAM member entries, exactly as `fugaro user` wrote them. The value is
// what the lists said when it was built, so a caller that holds one across
// requests is holding a stale local value — read them where the decision is
// made.
func MemberListsOf(launchers, operators []string) MemberLists {
	return MemberLists{launchers: memberAddresses(launchers), operators: memberAddresses(operators)}
}

// Addresses returns every address these two lists name, as written, sorted and
// without exact repeats. It is a copy, so a caller cannot write through it to
// the lists it was read from. The two lists are one answer: a person is a
// launcher or an operator, and an admission that had to know which of the two
// would be deciding something this reader was not asked.
func (m MemberLists) Addresses() []string {
	out := make([]string, 0, len(m.launchers)+len(m.operators))
	out = append(out, m.launchers...)
	out = append(out, m.operators...)
	slices.Sort(out)
	return slices.Compact(out)
}

// memberAddresses is the addresses one list names, in the order it writes them.
func memberAddresses(entries []string) []string {
	var out []string
	for _, entry := range entries {
		if addr, ok := memberUserEntry(entry); ok {
			out = append(out, addr)
		}
	}
	return out
}

// memberUserEntry reads one `user:` entry's address. It is the shape
// `fugaro user` writes and refuses, byte for byte: exactly the `user:` prefix,
// no whitespace around the entry, an address that is not empty, carries an `@`
// and carries none of the wildcard, bracket or whitespace bytes a member entry
// may not have. A `User:` or `Domain:` spelled in another case is not a member
// here either, because the prefix is read as it is written.
func memberUserEntry(entry string) (string, bool) {
	if entry != strings.TrimSpace(entry) || !strings.HasPrefix(entry, "user:") {
		return "", false
	}
	addr := strings.TrimPrefix(entry, "user:")
	if addr == "" || !strings.Contains(addr, "@") || strings.ContainsAny(addr, "*?[] \t\r\n") {
		return "", false
	}
	return addr, true
}
