package watch

import "time"

// The pause a suspended run is in (contract-pause, R15): the instant its
// resume is due at, and the run that resumed it.
//
// Neither fact is in the paused run's own record, which is why they travel
// separately from the rows and are stamped on by MergePaused. The instant is
// the credentials service's own node for the pause (runstore.PauseNode,
// `paused/<slug>/<run>`), written before the run ends paused; the resumer is
// another run — a new run in the same chain, launched by the orchestrator,
// whose task names the paused run under `resumes`. The contract puts the
// second there and not on the node on purpose: the node is the service's
// record of the pause, and it holds nothing about a resume that had not
// happened when it was written.

// Pause is what one paused run's own pause says about it.
type Pause struct {
	// ResumeAt is the instant the resume is due at. Zero when the reader had
	// no node to take it from — a pause whose node could not be read, or a
	// view built without a read of the paused subtree at all.
	ResumeAt time.Time
	// ResumedBy is the run that resumed this pause, "" while none has.
	ResumedBy string
}

// Pauses is the pause of each paused run a reader knows of, by the run's key
// (RunKeyOf).
type Pauses map[RunKey]Pause

// PauseKeyOf is the key a paused node's own path segments name
// (runstore.PauseNodePath): the slug and the run id as the database holds
// them. They are deliberately not RunKeyOf's input — the database's segments
// are already escaped the way budget.Key escapes them, and running them
// through it a second time would escape the escapes (%2E would become %252E)
// and name no row at all. Only the run id is normalised further, exactly as a
// row's own is.
func PauseKeyOf(slug, run string) RunKey { return RunKey{Slug: slug, Run: clean(run)} }

// PausesOf joins the two reads a pause is known from into the map MergePaused
// stamps: resumeAt is what the service's `paused` subtree said, keyed the way
// the rows are, and facts is what the run scan read of every run it saw —
// among which is the run that resumed a pause, whose own task names the paused
// run under `resumes`.
//
// A key only one of the two knows is still a pause: a node with no resumer is
// a pause still standing, and a resumer whose paused run the frame does not
// list costs nothing. A key both know gets both halves.
func PausesOf(resumeAt map[RunKey]time.Time, facts Facts) Pauses {
	out := Pauses{}
	for key, at := range resumeAt {
		out[key] = Pause{ResumeAt: at}
	}
	for key, f := range facts {
		if f.Resumes == "" {
			continue
		}
		resumed := RunKeyOf(key.Slug, f.Resumes)
		if _, ok := out[resumed]; !ok {
			continue
		}
		p := out[resumed]
		// Two runs naming one pause is a state the resume guards exist to
		// prevent, so it is never what a frame is drawn from; the comparison
		// is here so that if it ever were, the answer is the later launch and
		// not whichever key this map happened to walk first — a frame that
		// flipped between two resumers on consecutive redraws would be worse
		// than either answer.
		if p.ResumedBy == "" || key.Run > p.ResumedBy {
			p.ResumedBy = key.Run
			out[resumed] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergePaused stamps each paused row with its resume instant and the run that
// resumed it. Only a row whose own record says it is paused is touched: the
// map is keyed by run, and a run that is not paused is not one a pause is
// about, whatever a stale node under its id might say.
//
// A paused row the map does not name keeps the word alone — the run's own
// record says it suspended itself, and a reader that read no node has nothing
// to add to that. It is the same degradation MergeFacts makes for a run it
// cannot place: the row the frame would have shown without this.
func MergePaused(v View, p Pauses) View {
	if len(p) == 0 {
		return v
	}
	for i := range v.Repos {
		b := &v.Repos[i]
		for _, rows := range [][]RunRow{b.Runs, b.Finished} {
			for j := range rows {
				if !rows[j].Paused {
					continue
				}
				f, ok := p[RunKey{Slug: b.Slug, Run: rows[j].Run}]
				if !ok {
					continue
				}
				if !f.ResumeAt.IsZero() {
					rows[j].ResumeAt = f.ResumeAt.UnixMilli()
				}
				// A run id a scan read, cleaned like every other string that
				// reaches the view (the row's own Run beside it is cleaned the
				// same way): it is a bucket-derived name, and a frame never
				// draws text this package did not strip.
				rows[j].ResumedBy = clean(f.ResumedBy)
			}
		}
	}
	return v
}
