package credsvc

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
)

// The access contract's wire (C1 (e), R5, F44): the headers a new runner
// declares its craft, its task's grade and the credential it chose in, beside
// the auth header and never in it, and the validation of each value.
//
// # A new header beside the old (R5)
//
// The three are NEW NAMES, and that is the whole of the arrangement: a call that
// carries the auth header is an older image's and is narrowed by the OLD record
// (CM6), exactly as it is today, and a call that carries one of these is a new
// runner's and is narrowed by the access record (creds/repoaccess/<slug>). A new
// runner's statement written into the old header would be read as an older
// image's and answered out of a record that says nothing about crafts; and a
// value the two sides spelled differently once is a repository served what its
// record does not name.
//
// # What a declaration may and may not do (G16's direction)
//
// These are the caller's own words, as the auth header is (R5): the agent holds
// the run identity and the job's ID token, so it can send whatever it likes.
// What they buy is nothing on their own — the service resolves them against the
// repository's access record and serves no more than the record names for that
// craft — and what they must be is DECIDABLE, which is what the validation here
// is for: a craft this run does not have, a grade this project does not have or
// a name no credential could carry is refused at the door rather than compared
// against a record and found unequal, which would send an operator looking in
// the wrong document.
//
// The service wires these into RequestFromHTTP with the narrowing itself (CM6);
// this file is the names and the rules, so the gateway that sends them
// (internal/gateway/accesswire.go, which may not import this package) and the
// runner that sets them are held to one spelling.
const (
	// HeaderAccessCraft is the craft a call is declared for: one of
	// config.CraftNames, which is the key the service reads the repository's
	// access record by (CM6). A call with no craft is an older image's and
	// carries no access header at all.
	HeaderAccessCraft = "X-Fugaro-Access-Craft"
	// HeaderAccessGrade is the task's own model grade where it names one (F38):
	// one of config.GradeNames. It is sent only by a run whose task names a
	// grade, and it is what lets the service serve a grade's own access rather
	// than the craft's.
	HeaderAccessGrade = "X-Fugaro-Access-Grade"
	// HeaderAccessChosen is the credential the run CHOSE, where it chose one
	// (F44, D3069): the name a --token run names, or the account a walk moved
	// to. It is the one thing about a call the record cannot answer from the
	// craft alone — the record names what a run MAY be served by, and this says
	// which of those names it is being served for — and the service serves it
	// only where the record names it for that craft or for --token.
	//
	// It is a name and never a value, and it is NOT HeaderCredential: that one
	// is the service's ANSWER, naming the credential that answered, and a field
	// never changes meaning.
	HeaderAccessChosen = "X-Fugaro-Access-Chosen"
)

// ValidAccessCraft reports whether s may be the craft a call declares. It is
// config's own list of crafts — the same one the access record is keyed by — so
// a craft this service has never heard of is refused here rather than read as
// one whose entry the record does not hold.
func ValidAccessCraft(s string) bool { return slices.Contains(config.CraftNames, s) }

// ValidAccessGrade reports whether s may be the grade a call declares, in
// config's own list of grades. A task names a grade or names none; a value
// outside the list is refused rather than read as a grade with no entry.
func ValidAccessGrade(s string) bool { return slices.Contains(config.GradeNames, s) }

// ValidAccessChosen reports whether s may be the credential a call declares it
// chose: a credential's name as this wire spells one (ValidCredentialName), or
// the project's default account (config.DefaultClaudeAccount), which is what a
// --token run names the unsuffixed Claude subscription with (D3069) even though
// it is not a credential's name in the config's own block.
func ValidAccessChosen(s string) bool {
	return s == config.DefaultClaudeAccount || ValidCredentialName(s)
}
