package rigor

import "slices"

// The threshold table (D2, D7a): what each mode does with the findings its
// reviewer scored, as data rather than as a condition inside the runner.
//
// A finding carries three anchored 1–5 axes and a class (D1, D3;
// runner.VerdictSchema is the schema the reviewer is held to). The table reads
// those four and never the free-form `severity` string beside them (D2): the
// five words are a label derived from the score (label.go), so a word and a
// score cannot disagree, and a mode's decision is answerable to a reader
// rather than to the model's choice of adjective.
//
// Nothing acts on this yet. T2.3 lands the table and its tests; Slice 3's T3.1
// is the task that puts it inside runner.Decide, which is where readiness is
// decided and stays the one place that answers it (F4).

// Class is a finding's class: the closed vocabulary the reviewer chooses from
// (D3), and the fourth input to the table beside the three axes. It is where
// the spec's severity override attaches — "rare but catastrophic findings
// (data loss, security) block even in Startup mode" — so the two classes the
// spec names are the ones every mode blocks on whatever the axes say.
type Class string

// The classes, in runner.VerdictSchema's enum order. A class the schema gains
// is added here in the same change: the schema is what constrains the model,
// this list is what the table reads, and a class only one of them knows is a
// finding the table cannot read at all — which blocks in every mode, loudly
// rather than silently, but for no reason the reviewer can see.
const (
	// Correctness is the code doing the wrong thing.
	Correctness Class = "correctness"
	// Performance is the code doing the right thing too slowly.
	Performance Class = "performance"
	// Security is an exploitable weakness. Catastrophic in every mode.
	Security Class = "security"
	// DataLoss is a write that loses or corrupts what was there. Catastrophic
	// in every mode.
	DataLoss Class = "data_loss"
	// Maintainability is code that works and will be expensive to change.
	Maintainability Class = "maintainability"
	// Cosmetic is appearance alone.
	Cosmetic Class = "cosmetic"
)

// Classes is the whole vocabulary, in the schema's order. It is closed, the
// way Modes is: a value outside it is refused where it is written, and a
// finding carrying one is unscored rather than judged by a word the table
// does not know.
var Classes = []Class{Correctness, Performance, Security, DataLoss, Maintainability, Cosmetic}

// KnownClass reports whether c is one of Classes. The zero Class — a finding
// whose reviewer named none — is not.
func KnownClass(c Class) bool { return slices.Contains(Classes, c) }

// Score is the part of a finding the table reads (D2): the three axes and the
// class. It is deliberately not runner.Finding — internal/rigor may not import
// internal/runner, which reads this package — so the runner presents its own
// finding as this at the call site and the table's inputs are stated once,
// here, rather than as four field names a reader has to go and find.
//
// Zero is the unscored finding. The valid scale starts at 1 on every axis, so
// nothing a reviewer scores can read as absent, and a verdict written before
// the score existed decodes to exactly this (D3, D4).
type Score struct {
	// Likelihood is how often the finding's condition will occur: 1 rare, 5
	// certain.
	Likelihood int
	// Impact is how bad it is when it does: 1 cosmetic, 5 catastrophic.
	Impact int
	// FixCost is the effort to address it: 1 trivial, 5 rewrite. Only the
	// modes whose spec sentence weighs it read it.
	FixCost int
	// Class is what kind of issue it is.
	Class Class
}

// Risk is likelihood × impact: the one number the table's floors are stated
// against. A product rather than a pair of comparisons because the spec's two
// examples — rare but catastrophic against common but cosmetic — differ in
// both axes at once, and D1's reason for five values rather than three is that
// the product has to be able to tell them apart.
func (s Score) Risk() int { return s.Likelihood * s.Impact }

// Scored reports whether the finding carries a score the table can read: every
// axis inside 1–5 and a class in the vocabulary. An out-of-range value is not
// a score — the schema is what constrains the model, and the Go side reads
// whatever a stored transcript holds (T2.1) — so it is read as unscored and
// blocks, which is D4's rule applied to a value the table cannot place.
func (s Score) Scored() bool {
	return s.Likelihood >= 1 && s.Likelihood <= 5 &&
		s.Impact >= 1 && s.Impact <= 5 &&
		s.FixCost >= 1 && s.FixCost <= 5 &&
		KnownClass(s.Class)
}

// catastrophic reports whether the finding is one the spec's severity override
// covers: its class is one the spec names as catastrophic — data loss,
// security — or its impact is the top of the scale, the crash the severity axis
// names, which is what makes a rare finding block in Startup.
//
// It is the override stated once, and it is what the label derives from
// (label.go) and what every mode's row applies whole but Hotfix's; a mode that
// reads the override differently does so in its own row (override below)
// rather than by a second copy of this.
func (s Score) catastrophic() bool {
	return s.Class == Security || s.Class == DataLoss || s.Impact == 5
}

// override is one mode's reading of the spec's severity override — "rare but
// catastrophic findings (data loss, security) block even in Startup mode" — as
// the mode's own row rather than as a condition beside it: the table is data
// and Blocks is its one reader (D2, D7).
type override int

const (
	// overrideCatastrophic is the override as the spec states it: a Security or
	// DataLoss class, or an impact of the top of the scale. It is the zero
	// value, so a row that says nothing about the override keeps all of it — a
	// mode can only give part of it up by naming the part it keeps, which is
	// D4's fail-closed rule read at the table's own data.
	overrideCatastrophic override = iota
	// overrideSecurity is the security gate alone. Hotfix's row is the one that
	// reads its own sentence — "only findings that make the fix wrong or unsafe
	// block" — this way, by the owner's ruling of 2026-10-26: a finding of
	// class security blocks there whatever its axes say, and a finding whose
	// class is data_loss or whose impact is 5 is tolerated like any other.
	overrideSecurity
)

// blocks reports whether this reading of the override blocks s whatever its
// axes say. A value outside the two above is read as the whole override, the
// way an unknown mode blocks everything: a value this code cannot place gets
// the strict answer and never the lenient one.
func (o override) blocks(s Score) bool {
	if o == overrideSecurity {
		return s.Class == Security
	}
	return s.catastrophic()
}

// stop is one mode's at-the-stop row (the owner's ruling of 2026-11-08): what
// the mode does with the findings a run still has when it stops — the moment
// its review rounds are spent or the run reaches the configured fraction of its
// time budget (runner.Budget.PastStop), whichever comes first, without a ship
// verdict. It is the second rule the mode holds, beside the threshold its
// rounds are spent under, and it is data here for the same reason that one is:
// "merge as-is, follow up the rest" is Startup's own sentence read at the
// moment it applies.
type stop int

const (
	// stopDraft keeps every finding a stopped round left blocking: the pull
	// request stays a draft with the findings and the round named in its reason,
	// which is what every mode did before this row existed. It is the zero
	// value, so a mode that says nothing about the stop — and a mode the table
	// does not hold — keeps that behaviour rather than gaining an acceptance
	// nobody wrote.
	stopDraft stop = iota
	// stopCatastrophic accepts at the stop every finding the spec's severity
	// override does not cover: what still blocks is a Security or DataLoss class
	// or an impact of 5 (catastrophic), whatever the axes say. Startup's row,
	// whose sentence is "fix common; log rare/expensive to backlog" and whose
	// point is time to market, and POC's, whose own row already tolerates
	// everything else.
	stopCatastrophic
	// stopSecurity accepts at the stop every finding but an exploitable
	// weakness: Hotfix's reading of its sentence — "only findings that make the
	// fix wrong or unsafe block" — applied at the stop exactly as its override
	// applies it in a round. It names the reading rather than leaving it to the
	// override so the mode's answer at the stop is stated here beside every other
	// mode's, and not inferred from which half of the row is asked.
	stopSecurity
)

// blocks reports whether this reading of the stop rule still blocks s. A value
// outside the three above is read as stopDraft — the strict answer — the way an
// unknown mode blocks everything.
func (o stop) blocks(s Score) bool {
	switch o {
	case stopCatastrophic:
		return s.catastrophic()
	case stopSecurity:
		return s.Class == Security
	}
	return true
}

// threshold is one mode's findings policy, as data: how it reads the severity
// override, where on the risk axis the mode starts to block, the one escape
// its spec sentence names, and what it does with the findings left at the stop.
type threshold struct {
	// Risk is the smallest likelihood × impact that blocks. Zero means the
	// axes never block in this mode, so only the override and an unscored
	// finding reach it.
	Risk int
	// Rare and Expensive are the escape, and are set together: a finding whose
	// likelihood is at most Rare and whose fix cost is at least Expensive is
	// tolerated even at or above Risk. Rare 0 means the mode weighs no fix
	// cost.
	Rare      int
	Expensive int
	// Override is how this mode reads the spec's severity override: the whole
	// of it unless the row names the narrower reading. It is beside Risk rather
	// than inside a condition because it is the one part of the rule that two
	// modes read differently (D2).
	Override override
	// Stop is what the mode does with the findings a stopped run has left:
	// stopDraft, the zero value, keeps the pull request a draft, and the other
	// two accept everything the row names and file it (BlocksAtStop, D30). It
	// sits beside Risk because it is the mode's own sentence at a second moment
	// and not a second table: Pro's row says Pro's stop is a draft and POC's
	// says POC's is an acceptance, and no code outside this file asks which.
	Stop stop
}

// thresholds is the table, one row per mode, each row the mode's own sentence
// in spec.md read as a number. It is unexported and Blocks and BlocksAtStop are
// the only readers: each rule has one statement, and a second caller reaching
// for the numbers would be the second answer to "does this finding block" that
// D7's answer and F4 exist to prevent.
//
// Pro's row is the behaviour of every repository that configures nothing
// (D19), so it is the row a reviewer should read first and the one whose
// numbers a change to this table is really about.
var thresholds = map[Mode]threshold{
	// "POC — Ignore (except catastrophic)". The axes never block: what is left
	// goes to the pull request body's one line and the run record, which is
	// the whole account a POC run keeps (D30). Its stop row states the same
	// reading, so a POC run that spends its rounds is ready on the same terms
	// it reviewed under rather than a draft on the last round's count.
	POC: {Stop: stopCatastrophic},
	// "Startup — Fix common; log rare/expensive to backlog", and "Rare,
	// expensive corner cases go to the backlog": the floor is where a finding
	// stops being a corner case, and no fix-cost escape belongs beside it — a
	// rare finding is one the floor already tolerates, whatever its fix costs.
	//
	// The stop row is where the mode's other sentence lives — "merge as-is,
	// follow up the rest", the owner's ruling of 2026-11-08 — and it is wider
	// than the floor on purpose: a documentation-drift finding the mode would
	// have spent a round on blocks nothing once the rounds are gone, and the
	// override alone is what ends a Startup run a draft.
	Startup: {Risk: 8, Stop: stopCatastrophic},
	// "Pro — Weigh fix cost vs. likelihood × severity … extremely rare,
	// high-effort issues may stay unfixed": the lowest floor of the four
	// on-axis modes, which is what "aim for bug-free patches" costs, with the
	// spec's own escape for the one case it names — likelihood 1, fix cost 4
	// or more. It names no stop row: Pro's sentence is "aim for bug-free
	// patches", so a run of it stops at the stop on the terms it reviewed
	// under, which is the draft stopDraft is.
	Pro: {Risk: 4, Rare: 1, Expensive: 4},
	// "Critical — Every finding blocks": the whole scale is above the floor.
	// D7's answer keeps this mode's outcome and reason byte-identical to the
	// releases before it, apart from the mode line every run's body carries
	// (D18).
	Critical: {Risk: 1},
	// "Hotfix — Only findings that make the fix wrong or unsafe block", read
	// against the owner's ruling of 2026-10-26: the fewest gates of any mode,
	// so out of the severity override the row keeps the security gate and
	// nothing else — a data loss finding and an impact of 5 are tolerated and
	// filed as advisory by the mode's own filing row (D30), which is where a
	// hotfix's record of them belongs. What makes a hotfix's fix *wrong* is not
	// a finding at all: D14's deterministic diff rule gates the run, and the
	// agent's own confidence number is recorded and never trusted.
	//
	// Its stop row is that same security gate written again: at the stop a
	// hotfix run accepts what it tolerates in a round, which is what keeps the
	// mode's answer the same at both moments and the ruling of 2026-10-26
	// intact there.
	Hotfix: {Override: overrideSecurity, Stop: stopSecurity},
}

// Blocks reports whether a finding blocks at mode m.
//
// The order is the rule. An unscored finding blocks before any threshold is
// read (D4); the override is asked next, as the mode's own reading of it, so
// it holds in every mode including POC and — as the security gate alone, by the
// owner's ruling of 2026-10-26 — Hotfix; the mode's own row is asked last. A
// mode the table does not hold blocks on everything — the zero Mode a record
// written before the mode existed carries, and a name that reached here without
// having been parsed — because a value the table cannot place must never be the
// lenient answer.
func Blocks(m Mode, s Score) bool {
	row, known := thresholds[m]
	if !known {
		return true
	}
	if !s.Scored() || row.Override.blocks(s) {
		return true
	}
	if row.Risk == 0 || s.Risk() < row.Risk {
		return false
	}
	if row.Rare > 0 && s.Likelihood <= row.Rare && s.FixCost >= row.Expensive {
		return false
	}
	return true
}

// Split is a verdict's findings in two: the ones the mode blocks on and the
// ones it tolerates. Both groups keep the order they were given, because the
// runner prints the tolerated ones in the reviewer's own order in the pull
// request body and files one item per tolerated finding (D7, D30).
//
// It is Blocks asked once per finding and never a second rule: a caller that
// wanted the count of tolerated findings, or the ones to file, must not be
// able to disagree with the readiness rule about which they are.
func Split(m Mode, findings []Score) (blocking, tolerated []Score) {
	return splitWith(m, findings, Blocks)
}

// BlocksAtStop reports whether s still blocks at mode m's stop: the mode's own
// stop row read over its ordinary one, so what a mode already tolerates in a
// round is tolerated at the stop too and the stop can only ever widen what a
// run accepts. A mode whose row names no stop — Pro, Critical, the zero mode,
// and every name outside the vocabulary — answers exactly what Blocks does,
// which is the fail-closed zero value: a mode that says nothing about the stop
// keeps the draft.
//
// The order is Blocks first for the same reason Blocks asks the override before
// its floor: a finding the mode never blocks on is not one its stop row has to
// be consulted about, and an unscored finding blocks on both paths (D4).
func BlocksAtStop(m Mode, s Score) bool {
	if !Blocks(m, s) {
		return false
	}
	row, known := thresholds[m]
	if !known || !s.Scored() {
		// D4's rule, asked before the row for the same reason Blocks asks it
		// there: a table cannot accept a finding it cannot read, and the stop is
		// the moment a reviewer that stopped scoring would otherwise be rewarded
		// for it — the run is out of rounds and nothing more would be asked.
		return true
	}
	return row.Stop.blocks(s)
}

// SplitAtStop is Split at the stop (SplitAtStop's own rule): what the run still
// blocks on when it stops without a ship verdict, and what it accepts and files
// (D30). The two groups keep the order they were given, exactly as Split's do,
// and a mode that names no stop row answers with every finding blocking — which
// is the split that keeps Decide's tolerance from applying at all.
func SplitAtStop(m Mode, findings []Score) (blocking, accepted []Score) {
	return splitWith(m, findings, BlocksAtStop)
}

// splitWith is Split and SplitAtStop's one loop: the group each finding falls in
// is the predicate's answer, so neither reader can disagree with the rule it
// names about which findings they are.
func splitWith(m Mode, findings []Score, blocks func(Mode, Score) bool) (blocking, tolerated []Score) {
	for _, s := range findings {
		if blocks(m, s) {
			blocking = append(blocking, s)
			continue
		}
		tolerated = append(tolerated, s)
	}
	return blocking, tolerated
}
