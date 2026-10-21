package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The `orchestrators` block: one entry per central orchestrator of the project
// (central-orchestrator plan P2, decisions D6 and D12), keyed by the
// orchestrator's name.
//
// This block is the project's *administration* of central orchestrators, and it
// is deliberately not the authorisation. The authorisation is the orchestrator
// access record — the device node `config/admin/devices/<uid>` extended with
// the administered email and role (D6's answer, internal/budget/rules'
// orchestratorAccess) — and the deployed rules decide it there, per identity,
// on every request. What this block answers is the other two things D12's `add`
// has to be able to name: which identities this project has admitted to a named
// orchestrator, and what that orchestrator's jobs may spend.
//
// It is one block and not two because the two are written together by one
// command (`fugaro orchestrator add`), read together by `ls`, and — the reason
// that matters — because the second is meaningless without the first: a cap on
// an orchestrator nobody is admitted to is a number about nothing.
//
// What it is not, stated because it is the obvious next question: it is not
// what the rules read. A grant whose predicate consulted this block per `$slug`
// would make a published policy a second authorisation document inside the
// rules, and the owner's D6 answer chose the one-node record instead (the
// alternative that answer rejects). So the admitted set here and the
// administered set in the database are two views of one decision, and `fugaro
// orchestrator ls` is what shows they agree — see internal/cli's orchestrator.go
// for the invariant it asserts and the drift it reports.

// Orchestrator is one central orchestrator's entry.
//
// CapUSD is the ceiling on what one job this orchestrator launches may spend,
// in US dollars. It is a value of its own and not `budget.per_run_usd`: that
// one is a developer's ceiling for a run a person started, and the spec is
// explicit that automated work carries its own cap ("automated jobs have their
// own spending cap, separate from developers' caps"). An orchestrator with no
// cap is an orchestrator whose jobs nobody bounded, which is why the field is
// required and why `add` refuses without it.
//
// Allow is the uids this project has admitted to *this* orchestrator, sorted
// and without duplicates. It names uids and never emails: D6's answer is that
// the allowlist is the device node, and an email list here would be the second
// allowlist that answer rejects. Each uid must be key-safe, because the same
// uid is a database key on the access record (see validOrchestratorUID).
type Orchestrator struct {
	CapUSD float64  `json:"cap_usd"`
	Allow  []string `json:"allow"`
}

// orchestratorMembers is the member names an orchestrators entry is spelled
// with, read from the struct's own json tags the way accountMembers and
// profileMembers are (carrierMembers), so a field this type gains is held to
// its spelling without a second list.
var orchestratorMembers = carrierMembers(reflect.TypeOf(Orchestrator{}))

// Limits on the block. They are bounds on an operator-written document rather
// than on anything a run does, so they are generous: a project with more
// orchestrators than this has a problem a bound is not the answer to, and the
// point of a number here is that the node cannot be made to cost an unbounded
// read.
const (
	// MaxOrchestrators bounds the number of entries in the block.
	MaxOrchestrators = 32
	// MaxOrchestratorAllow bounds one entry's admitted identities.
	MaxOrchestratorAllow = 64
	// MaxUIDLength bounds one uid. It is Firebase Auth's own limit on a user
	// id.
	MaxUIDLength = 128
)

// OrchestratorNames is the block's names, sorted. A map's range is random, so
// every reader that prints or iterates the block goes through this.
func (p *Policy) OrchestratorNames() []string {
	names := make([]string, 0, len(p.Orchestrators))
	for name := range p.Orchestrators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OrchestratorFor is the entry of that name, and whether the block names it.
func (p *Policy) OrchestratorFor(name string) (Orchestrator, bool) {
	o, ok := p.Orchestrators[name]
	return o, ok
}

// AdmittedTo reports whether the block admits uid to the named orchestrator.
// It is the question `fugaro orchestrator ls` asks of each administered
// identity, and it fails closed: an orchestrator the block does not name
// admits nobody.
func (p *Policy) AdmittedTo(name, uid string) bool {
	o, ok := p.Orchestrators[name]
	if !ok {
		return false
	}
	for _, u := range o.Allow {
		if u == uid {
			return true
		}
	}
	return false
}

// decodeOrchestratorBlock reads the block off the node, refusing a key inside
// an entry that this Fugaro does not know — decodeAccountBlock's rule and
// decodeProfileBlock's reason: a misspelt key here is not a dropped field but
// an enforced one gone quiet. A misspelt cap_usd decodes to the zero cap, and
// what a reader then holds is an orchestrator whose jobs nothing bounds while
// the node appears to state a ceiling; a misspelt allow leaves the nil slice,
// and an orchestrator nobody is admitted to reads exactly like one whose
// identities were dropped. Unknown *top-level* keys keep the P8 tolerance; a
// newer member inside an entry bumps Version, which is what refuses a node this
// binary cannot read.
//
// The member names are held to the documented spelling too (checkMemberNames'
// duplicate half, plus spelledMember against orchestratorMembers): encoding/json
// folds case, so a strict decode alone would read {"cap_usd": 5, "Cap_USD":
// null} as whichever of the two the document wrote last.
func decodeOrchestratorBlock(raw json.RawMessage) (map[string]Orchestrator, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("projectpolicy: orchestrators: %w", err)
	}
	// The block's own keys, duplicates included, before the map above has
	// folded them away: a key written twice is read as the last of the two, so
	// a repeated orchestrator is one whose allow-list this reader never saw.
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: orchestrators: %w", err)
	}
	if err := checkNoDuplicateNames("orchestrators", names); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	if len(entries) > MaxOrchestrators {
		return nil, fmt.Errorf("projectpolicy: orchestrators: %d entries; at most %d are allowed", len(entries), MaxOrchestrators)
	}
	out := make(map[string]Orchestrator, len(entries))
	for _, name := range sortedStringKeys(entries) {
		dec := json.NewDecoder(bytes.NewReader(entries[name]))
		dec.DisallowUnknownFields()
		var o Orchestrator
		if err := dec.Decode(&o); err != nil {
			return nil, fmt.Errorf("projectpolicy: orchestrators.%s: %w", name, err)
		}
		members, err := objectMemberNames(entries[name])
		if err != nil {
			return nil, fmt.Errorf("projectpolicy: orchestrators.%s: %w", name, err)
		}
		if err := checkNoDuplicateNames("orchestrators."+name, members); err != nil {
			return nil, fmt.Errorf("projectpolicy: %w", err)
		}
		if err := checkOrchestratorMemberSpelling(name, members); err != nil {
			return nil, err
		}
		if err := validOrchestrator(name, &o); err != nil {
			return nil, err
		}
		out[name] = o
	}
	return out, nil
}

// checkOrchestratorMemberSpelling holds an entry's member names to the
// documented spelling, in checkWrittenAccountFields' shape and for its reason:
// a name that differs only in case is matched by encoding/json and by nothing
// else here, so a misspelt cap_usd leaves the zero cap while the node appears
// to state one.
//
// Through Decode the unknown-name arm is unreachable — the strict decode above
// refuses a name no member answers to first — and it is returned anyway,
// because a check that depends on another one running first is a check a later
// change to that one can silently disable.
func checkOrchestratorMemberSpelling(name string, members []string) error {
	for _, member := range members {
		known := spelledMember(orchestratorMembers, member)
		if known == member {
			continue
		}
		if known == "" {
			return fmt.Errorf("projectpolicy: orchestrators.%s: unknown field %q", name, member)
		}
		return fmt.Errorf("projectpolicy: orchestrators.%s.%s: an orchestrator entry's members are spelled as the policy spells them — write %s. A name that differs only in case is read by the decoder and not by this document's own checks, so the value it names would be the one the decoder read", name, member, known)
	}
	return nil
}

// validOrchestrator checks one entry: its name, its cap and its admitted
// identities.
func validOrchestrator(name string, o *Orchestrator) error {
	at := "projectpolicy: orchestrators." + name
	if err := ValidOrchestratorName(name); err != nil {
		return fmt.Errorf("projectpolicy: orchestrators: %w", err)
	}
	if o.CapUSD <= 0 {
		return fmt.Errorf("%s.cap_usd: an orchestrator must name the ceiling on what one of its jobs may spend; there is no default (a job nobody bounded is a job nobody capped)", at)
	}
	if micros, err := pricing.FromUSD(o.CapUSD); err != nil {
		return fmt.Errorf("%s.cap_usd: %v is not a number from 0 to %d US dollars", at, o.CapUSD, pricing.MaxUSD)
	} else if micros < 1 {
		return fmt.Errorf("%s.cap_usd: %v rounds to nothing; the smallest cap is $0.000001", at, o.CapUSD)
	}
	if len(o.Allow) == 0 {
		return fmt.Errorf("%s.allow: an orchestrator must name at least one identity; an entry with nobody on it admits nobody and is a policy about nothing — remove the entry instead", at)
	}
	if len(o.Allow) > MaxOrchestratorAllow {
		return fmt.Errorf("%s.allow: %d identities; at most %d are allowed", at, len(o.Allow), MaxOrchestratorAllow)
	}
	seen := map[string]bool{}
	for i, uid := range o.Allow {
		if err := ValidOrchestratorUID(uid); err != nil {
			return fmt.Errorf("%s.allow[%d]: %w", at, i, err)
		}
		if seen[uid] {
			return fmt.Errorf("%s.allow[%d]: %s is listed twice; list each identity once", at, i, uid)
		}
		seen[uid] = true
	}
	for i := 1; i < len(o.Allow); i++ {
		if o.Allow[i-1] > o.Allow[i] {
			return fmt.Errorf("%s.allow: the identities must be sorted (%s comes after %s); the node is written sorted so that two publishes of the same set are the same bytes", at, o.Allow[i-1], o.Allow[i])
		}
	}
	return nil
}

// ValidOrchestratorName checks a central orchestrator's name. It is the project
// name's vocabulary (internal/config's ProjectNameRE) for the profile names'
// reason: the name becomes a key of this block, it is typed on a command line
// and it is what an operator says out loud ("talk to shepherd"), so a name with
// a space or a dot in it is a name nobody can repeat.
func ValidOrchestratorName(name string) error {
	if name == "" {
		return fmt.Errorf("a central orchestrator's name must not be empty")
	}
	if !config.ProjectNameRE.MatchString(name) {
		return fmt.Errorf("central orchestrator name %q must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", name)
	}
	return nil
}

// ValidOrchestratorUID checks one admitted identity, and it checks the one
// property the *deployment* needs rather than the one a reader would expect:
// the uid is a database **key**.
//
// The orchestrator access record lives at `config/admin/devices/<uid>` — the
// deployed rules reach it as `root.child('config/admin/devices').child(auth.uid)`
// and the command that writes it appends the uid to the same path — and the
// database refuses `. $ # [ ] /` and control characters in a key. So a uid with
// a dot in it is an identity this project could publish here and never
// administer, and the two would disagree about who is admitted; the refusal is
// what keeps them one answer. It is why the emulator cases' uids are key-safe
// (internal/budget/rules' `alice-uid`) while an email, which is a value and
// never a key, is not held to this.
//
// Nothing here is escaped on the way to the database, deliberately: both sides
// spell the path literally, and escaping one of them is the drift this check
// exists to prevent.
func ValidOrchestratorUID(uid string) error {
	if uid == "" {
		return fmt.Errorf("an identity's uid must not be empty")
	}
	if len(uid) > MaxUIDLength {
		return fmt.Errorf("uid %q is %d characters; at most %d are allowed", uid, len(uid), MaxUIDLength)
	}
	for i := 0; i < len(uid); i++ {
		switch c := uid[i]; {
		case c == '.' || c == '$' || c == '#' || c == '[' || c == ']' || c == '/':
			return fmt.Errorf("uid %q carries %q, which the database refuses in a key: the account's access record is keyed by its uid (config/admin/devices/<uid>), so an identity it cannot be keyed by is one this project cannot administer", uid, string(c))
		case c < 0x20 || c == 0x7f:
			return fmt.Errorf("uid %q carries a control character, which the database refuses in a key", uid)
		}
	}
	return nil
}
