package voicesvc

// The register: the engines this hop holds, what each one's terms were read to
// be, and which way each is served (docs/mobile/design/voice-evaluation-
// switch.md §4.3, §7; mobile plan Task P5.3.5).
//
// It is a FILE in this image (register.json, embedded below) and not a database
// node, a policy member or a request. Three reasons, and each is a rule this
// package already keeps:
//
//   - **The policy node names engines; it never reaches one.** §8's rule is
//     that the hop calls a named constant endpoint and never a URL the policy
//     node carries, and the register is where "which names exist at all" is
//     answered. A name the node lists and this file does not hold is refused
//     (the handler's register gate) rather than resolved against anything a
//     caller or a node wrote.
//
//   - **The register is the hop's own record and the owner's to edit.** §7 ends
//     by saying the columns are the owner's to fill, so the file is plain JSON
//     a person reads and changes, in the image the release builds — and a
//     change to it is a diff a reviewer sees, which a database write is not.
//
//   - **Nothing here is a secret and nothing here is enforced at call time.**
//     The terms are a RECORD: the retention, training, price, languages and
//     latency a provider states, quoted with the URL they were read from and
//     the date, or the mark UNVERIFIED where §7 read nothing. No call behaves
//     differently because of a value here — the hop does not read a retention
//     sentence and decide anything from it. What the register DOES decide is
//     which names and which directions exist, and that decision is only ever a
//     refusal: a name that is not in this file is a call this service will not
//     make.
//
// The gate is fail-closed in the same direction as the policy's (the plan's
// own words): a name with no row and a name the project's policy does not admit
// are both refusals, and neither is an admission of something narrower. A
// name whose row does not serve the direction asked for is refused by the same
// reason as a name with no row at all, so which engines this hop holds is not
// something a caller can enumerate by asking for directions one at a time.
//
// The provider key is NOT here (§4.4 point 4). This file names the engines; the
// credential that reaches one is read from the installation's Secret Manager
// per call (provider.go) and never appears in this file, in an image layer or
// in the repository.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// registerDocument is the register, compiled into the binary. It is embedded
// rather than read from disk: the process runs on Cloud Run from an image
// whose working directory is not something this service should depend on, and
// a file it must find at runtime is a failure mode with nothing to gain.
//
//go:embed register.json
var registerDocument []byte

// The two readings of a column. A column is `read` with the value and the URL
// it was read from, or `unverified` with the mark below and nothing else.
const (
	// TermRead is a column that was read from the provider's own page on the
	// row's read date.
	TermRead = "read"
	// TermUnverified is a column §7 read nothing for. It is a first-class
	// answer and not an omission: the design says anything not read is marked
	// UNVERIFIED and that no number is inferred, averaged or guessed.
	TermUnverified = "unverified"
	// unverifiedMark is the literal every unverified value carries, so a
	// reader of the file — and a test — can see the mark without decoding a
	// state field first.
	unverifiedMark = "UNVERIFIED"
)

// registerNameRE is the shape of a row's name: the pattern
// internal/projectpolicy's voiceProviderNameRE holds policy entries to
// (`^[a-z][a-z0-9-]{0,19}$`), spelled again here for wire.go's reason and held
// to one answer by a test rather than by this comment
// (TestRegisterNamesAreThePolicyNodesNames): a name this file holds and the
// policy could not name would be an engine nobody can admit, and a name the
// policy admits and this file does not hold is a call refused at the gate.
var registerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// readDateFormat is the shape of a row's read date: the day §7 was read, and
// not a timestamp, because "when was this read" is a question about a document
// and a day is the resolution an owner rechecks it at.
const readDateFormat = "2006-01-02"

// Term is one column of one row: what was read, or the mark that nothing was.
//
// It is not a `*string`: an absent column and an unread one are different
// facts, and §7 spends a paragraph on the difference. `Source` is the URL the
// claim was read from, and it is required of a `read` column — a number with no
// page behind it is the thing the design refuses to write down.
type Term struct {
	// State is TermRead or TermUnverified.
	State string `json:"state"`
	// Value is the claim, quoted from the page, and for an unverified column
	// the mark UNVERIFIED. It is the provider's claim and not this service's
	// verification (§7's own sentence), so it is stored as text and never
	// parsed into a number, a duration or a boolean this service could act on.
	Value string `json:"value"`
	// Source is the URL the value was read from. It is required when State is
	// TermRead.
	Source string `json:"source,omitempty"`
}

// Terms is a row's five columns. The set is §7's and is closed: a column added
// to this struct is a column the register claims to have read for every
// provider, so it is a change to the design's register and not a field an
// entry may have on its own.
type Terms struct {
	// Retention is the provider's stated retention of the audio, the text or
	// the reply.
	Retention Term `json:"retention"`
	// Training is whether the provider states it trains on the data.
	Training Term `json:"training"`
	// Price is the rate card as read.
	Price Term `json:"price"`
	// Languages is which languages (and, for the STT direction, which models)
	// the provider lists.
	Languages Term `json:"languages"`
	// Latency is the streaming latency figure. §7 read none for any provider,
	// which is why every row here marks it UNVERIFIED: "ultra-low latency" is
	// a vendor's phrase and not a number.
	Latency Term `json:"latency"`
}

// RegisterRow is one engine: its name, what it is, the directions this hop
// serves for it, the day the terms below were read, and the terms.
type RegisterRow struct {
	// Name is the engine's name on the wire and in the policy node. It is the
	// string a caller sends in the path and the string
	// Enforcer.Decide matches the project's list against, and it is held to
	// the policy's own name pattern (registerNameRE).
	Name string `json:"name"`
	// Provider is the provider's own name for itself, for a reader. It is
	// never matched on: a name here that drifted from the page's spelling
	// would change which engine a call reaches, which is the one thing a
	// display string must not do.
	Provider string `json:"provider"`
	// Directions are the directions THIS HOP forwards for this name — "stt",
	// "tts", each spelled as Direction.String spells it. It is the hop's
	// capability and not the provider's menu: §7 read Google for both
	// directions and this file serves text only there, and the row's Note says
	// why. A direction absent here is a call refused at the gate, so a
	// direction this hop has no client for is a refusal and never a call that
	// reaches something else.
	Directions []string `json:"directions"`
	// ReadAt is the day the terms were read, in readDateFormat. It is the
	// design's fourth column and the one that dates the other four.
	ReadAt string `json:"read_at"`
	// Note is free text for a reader of this file: what the row is for, what
	// §7 read that the columns do not hold, and what is deliberately not
	// served. It is display only — no code path reads it.
	Note string `json:"note,omitempty"`
	// Terms is the five columns §7 records.
	Terms Terms `json:"terms"`
}

// Serves reports whether this hop forwards dir for this name.
func (r RegisterRow) Serves(dir Direction) bool {
	return slices.Contains(r.Directions, dir.String())
}

// Register is the parsed register: rows in file order, and a name index.
type Register struct {
	rows   []RegisterRow
	byName map[string]RegisterRow
}

// LoadRegister parses the register compiled into this binary.
//
// A failure here stops the process (the caller is the one that builds the
// server), which is the fail-closed answer: a hop that started with an
// unreadable register would refuse every call for a reason it could not name,
// and the operator would be reading a 503 with nothing behind it.
func LoadRegister() (*Register, error) { return ParseRegister(registerDocument) }

// ParseRegister reads and validates a register document.
//
// Every rule here is a shape check on a file that ships in this image, so a
// failure is this tree's own error and never a caller's: the message names the
// row and the column, and it can name them freely, because nothing in this file
// is a secret or a caller's text.
func ParseRegister(doc []byte) (*Register, error) {
	var file struct {
		Rows []RegisterRow `json:"rows"`
	}
	if err := decodeRegister(doc, &file); err != nil {
		return nil, err
	}
	if len(file.Rows) == 0 {
		return nil, fmt.Errorf("voicesvc: the register holds no rows: with none, every call is refused and this service could not serve anything, which is a build error and not a state to run in")
	}
	reg := &Register{rows: file.Rows, byName: make(map[string]RegisterRow, len(file.Rows))}
	for i, row := range file.Rows {
		at := fmt.Sprintf("register.rows[%d]", i)
		if !registerNameRE.MatchString(row.Name) {
			return nil, fmt.Errorf("voicesvc: %s names the engine %q, which is not an engine name: a name matches %s, the pattern internal/projectpolicy holds a policy node's entries to, so a name outside it could be listed by no project and reached by no call", at, row.Name, registerNameRE)
		}
		if _, dup := reg.byName[row.Name]; dup {
			return nil, fmt.Errorf("voicesvc: %s names %q, which an earlier row already names: each engine has one row, so one set of terms and one set of directions", at, row.Name)
		}
		if row.Provider == "" {
			return nil, fmt.Errorf("voicesvc: %s (%s) names no provider", at, row.Name)
		}
		if len(row.Directions) == 0 {
			return nil, fmt.Errorf("voicesvc: %s (%s) serves no direction: a row that serves none admits no call, and an engine this hop does not forward is a row that should not be here", at, row.Name)
		}
		for j, d := range row.Directions {
			if _, ok := parseDirection(d); !ok {
				return nil, fmt.Errorf("voicesvc: %s (%s) directions[%d] is %q, which is not a direction: the two are %q and %q", at, row.Name, j, d, STT.String(), TTS.String())
			}
			if slices.Contains(row.Directions[:j], d) {
				return nil, fmt.Errorf("voicesvc: %s (%s) directions[%d] repeats %q", at, row.Name, j, d)
			}
		}
		if row.ReadAt == "" {
			return nil, fmt.Errorf("voicesvc: %s (%s) carries no read_at: the day the terms were read is the column that dates the others, and §7 records every one of them as read on a day", at, row.Name)
		}
		if _, err := time.Parse(readDateFormat, row.ReadAt); err != nil {
			return nil, fmt.Errorf("voicesvc: %s (%s) read_at is %q, which is not a date: it is the day the terms were read, spelled %s", at, row.Name, row.ReadAt, readDateFormat)
		}
		for _, col := range columns(row.Terms) {
			if err := checkTerm(at, row.Name, col.name, col.term); err != nil {
				return nil, err
			}
		}
		reg.byName[row.Name] = row
	}
	return reg, nil
}

// column names one of the five and pairs it with the term, so the validation
// below can say which column failed without five copies of itself.
type column struct {
	name string
	term Term
}

// columns returns a row's five columns with their names, in §7's order.
func columns(t Terms) []column {
	return []column{
		{"retention", t.Retention},
		{"training", t.Training},
		{"price", t.Price},
		{"languages", t.Languages},
		{"latency", t.Latency},
	}
}

// checkTerm validates one column. The two states are the whole rule: a column
// that was read carries the claim and the URL, and a column that was not
// carries the mark and no URL — so a reader can tell "nobody read this" from
// "this was read and came back empty" without asking which release wrote the
// file.
func checkTerm(at, name, col string, t Term) error {
	switch t.State {
	case TermRead:
		if t.Value == "" {
			return fmt.Errorf("voicesvc: %s (%s) %s is %s and carries no value", at, name, col, TermRead)
		}
		if t.Source == "" {
			return fmt.Errorf("voicesvc: %s (%s) %s is %s and names no source: every column read is quoted with the page it was read from, and a claim with no page behind it is what the design refuses to write down", at, name, col, TermRead)
		}
	case TermUnverified:
		if !strings.HasPrefix(t.Value, unverifiedMark) {
			return fmt.Errorf("voicesvc: %s (%s) %s is %s and its value does not carry the mark %s: an unread column says so in the value a reader of this file sees, and not only in a state field", at, name, col, TermUnverified, unverifiedMark)
		}
	case "":
		return fmt.Errorf("voicesvc: %s (%s) %s carries no state: each column is %s or %s", at, name, col, TermRead, TermUnverified)
	default:
		return fmt.Errorf("voicesvc: %s (%s) %s carries the state %q, which is not one: each column is %s or %s", at, name, col, t.State, TermRead, TermUnverified)
	}
	return nil
}

// Row returns the row for an engine name, and whether the register holds one.
// The name is matched exactly and never folded: a provider name is lowercase by
// projectpolicy's own rule, so a name that differs in case is not the engine the
// policy could have listed (Enforcer.Decide's argument, one level down).
func (r *Register) Row(name string) (RegisterRow, bool) {
	row, ok := r.byName[name]
	return row, ok
}

// Names returns the engines this hop holds, in file order.
func (r *Register) Names() []string {
	out := make([]string, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row.Name)
	}
	return out
}

// parseDirection reads a direction's wire spelling. It is the one place the two
// spellings meet: register.json is a document a person edits, and Direction is
// the int the code compares.
func parseDirection(s string) (Direction, bool) {
	switch s {
	case STT.String():
		return STT, true
	case TTS.String():
		return TTS, true
	}
	return 0, false
}

// decodeRegister decodes the register strictly: an unknown member is refused
// rather than ignored (Convention 6), so a column a later release adds is a
// release skew this build says out loud instead of a value it silently drops.
//
// It is its own decoder and not wire.go's decodeStrict: that one's sentences are
// about a request body, and this document is a file in the image whose reader is
// an operator with a text editor.
func decodeRegister(doc []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("voicesvc: the register is not this shape: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("voicesvc: the register carries more than one document")
	}
	return nil
}
