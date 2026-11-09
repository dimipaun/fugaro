package triage

import "github.com/dimipaun/fugaro/internal/rigor"

// Source is who placed a finding where it is, recorded per entry so a reader of
// the run can tell a judge's own answer from the table's (D1850).
type Source string

const (
	// SourceAgent is the judge's own verdict, applied as it was written: it
	// deferred a finding the table blocks and the floor does not name, or it
	// blocked one.
	SourceAgent Source = "agent"
	// SourceTable is the mode's table deciding on its own: a finding it
	// tolerates is filed as a follow-up, and one the floor or the owner flag
	// holds, or that the answer left out, blocks whatever the judge said.
	SourceTable Source = "table"
	// SourceFailSafe is the strict direction taken when no answer could be
	// read: the call failed, or the answer could not be placed on one finding
	// (D1846). Every finding the table blocks blocks.
	SourceFailSafe Source = "fail_safe"
)

// The reason recorded for a placement the judge did not write. A reason is one
// line and reaches a follow-up task's brief and the pull request's deferred
// section (T-TR9), so a placement with no judge behind it says what put the
// finding there rather than nothing.
const (
	reasonTolerated   = "the mode's table tolerates this finding outside the judge's reach"
	reasonFloor       = "the floor holds this finding: no verdict may defer it"
	reasonNeedsOwner  = "only the owner can settle this finding: no verdict may defer it"
	reasonUnanswered  = "the verdict answered nothing for this finding, so the table's block stands"
	reasonUnplaceable = "the verdict could not be placed on one finding, so this one blocks"
	reasonUnreadable  = "the verdict's word is neither block nor defer, so this finding blocks"
	reasonFailSafe    = "the triage call failed, so every finding the table blocks blocks"
)

// Input is one finding of the round, as applying a verdict reads it: the score
// the mode's table reads, the owner flag the score does not carry (F22), and
// the key that says which finding the verdict is about (D1850). The caller
// builds one per finding in the round's own order, which is the order an
// entry's index counts.
type Input struct {
	Score      rigor.Score
	NeedsOwner bool
	Key        string
}

// Placed is one finding's placement: the entry a run records — the verdict's
// own fields, so the index, key, word, reason and group a reader sees are one
// shape — and the source that placed it. It embeds Entry rather than restating
// its fields, because this is the entry the verdict held and nothing about it
// changes on the way to the record.
type Placed struct {
	Entry
	Source Source
}

// Outcome is one round's findings after the verdict was applied inside the
// floor: the ones that hold the merge, and the ones filed as follow-ups. Both
// keep the round's order, and a finding is in exactly one of them.
type Outcome struct {
	Blocking []Placed
	Deferred []Placed
}

// Apply reads a judge's verdict inside the floor and answers where each finding
// of the round went. The verdict steers exactly the findings the mode's table
// blocks and the floor does not name (D1857) — rigour's own rule asked per
// finding, since Split returns bare scores and this needs the index and key
// each one carries — and everywhere else the table stands:
//
//   - A finding only the owner can settle is Blocking with source table,
//     whatever the table and whatever the verdict say: a tolerated one is not
//     filed as a follow-up task either, because the run is waiting for a person
//     and neither a mode's leniency nor a judge's word is that person
//     (D1856, F22; the same rule Decide reads above every mode's tolerance).
//   - A finding the table tolerates is Deferred with source table, whatever
//     any entry says. The judge's reach is what the table blocks, so it can
//     neither add a block the table does not hold nor claim a deferral the
//     table already made.
//   - A finding the floor holds (rigor.Floor) is Blocking with source table:
//     no verdict moves it.
//   - A finding the table blocks that the answer left out is Blocking with
//     source table: a missing verdict for a blocked finding blocks it (D1846).
//   - ok == false — the call failed, timed out, or answered something Decode
//     refused — blocks every finding the table blocks, with source fail_safe.
//     A tolerated finding is still deferred: the table, not the judge, is what
//     lets it through. This is asked after the owner's flag, the one placement
//     a failed call does not reattribute: the run waits for a person whether or
//     not the call that would have judged the round answered.
//
// The verdict is untrusted data throughout. An entry naming no finding of the
// round is dropped; an entry whose index and key disagree, and two entries for
// one finding, block every finding either could name, because a reader taking
// the index and one taking the key would otherwise defer different findings
// (D1850). An entry whose word is neither block nor defer — which Decode
// refuses, so only a caller that skipped it can carry one — blocks the finding
// it names rather than deferring it. Each of those blocks records source
// fail_safe: the answer could not be read as an answer, so the strict direction
// was taken, exactly as for a call that failed.
func Apply(m rigor.Mode, in []Input, v Verdict, ok bool) Outcome {
	var out Outcome

	named := make([]*Entry, len(in))
	contested := make([]bool, len(in))
	if ok {
		byKey := make(map[string]int, len(in))
		for i, f := range in {
			if _, seen := byKey[f.Key]; !seen {
				byKey[f.Key] = i
			}
		}
		for k := range v.Findings {
			e := &v.Findings[k]
			if e.Index >= 0 && e.Index < len(in) && in[e.Index].Key == e.Key {
				if named[e.Index] != nil {
					contested[e.Index] = true
					continue
				}
				named[e.Index] = e
				continue
			}
			if e.Index >= 0 && e.Index < len(in) {
				contested[e.Index] = true
			}
			if j, found := byKey[e.Key]; found {
				contested[j] = true
			}
		}
	}

	for i, f := range in {
		switch {
		// The owner's flag is asked before the table's leniency, not after it:
		// a finding only the owner can settle holds the merge even where the
		// mode tolerates it, so it is never a follow-up task and no mode may
		// hand it out.
		case f.NeedsOwner:
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonNeedsOwner, SourceTable))
		case !rigor.Blocks(m, f.Score):
			out.Deferred = append(out.Deferred, place(i, f.Key, Defer, reasonTolerated, SourceTable))
		case !ok:
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonFailSafe, SourceFailSafe))
		case rigor.Floor(m, f.Score):
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonFloor, SourceTable))
		case contested[i]:
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonUnplaceable, SourceFailSafe))
		case named[i] == nil:
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonUnanswered, SourceTable))
		case named[i].Verdict == Defer:
			out.Deferred = append(out.Deferred, Placed{Entry: *named[i], Source: SourceAgent})
		case named[i].Verdict == Block:
			out.Blocking = append(out.Blocking, Placed{Entry: *named[i], Source: SourceAgent})
		default:
			// A word outside the two reads as a block: this is the last case,
			// and the strict direction is the one a value this code cannot
			// place gets. Decode refuses such a word (verdict.go), so only a
			// caller that skipped it can reach here; the placement records the
			// word the round acts on and not the one it could not read, because
			// the record's own verdict is one of the two.
			out.Blocking = append(out.Blocking, place(i, f.Key, Block, reasonUnreadable, SourceFailSafe))
		}
	}
	return out
}

// place is one finding's placement that no verdict wrote: the table's, or the
// fail-safe's. The group is empty because a group ties deferrals a judge
// grouped together, and these are not the judge's.
func place(index int, key string, w Word, reason string, source Source) Placed {
	return Placed{
		Entry:  Entry{Index: index, Key: key, Verdict: w, Reason: reason},
		Source: source,
	}
}
