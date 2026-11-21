package runner

import "github.com/dimipaun/fugaro/internal/rigor"

// The persona a stage runs under: the level's own text, read at the one seam
// below and handed to the prompt builders (LD1, spec §2 and §5).
//
// It is the level's text and never the runner's: a persona is embedded in the
// binary (LD10) and no layer, task or repository can write one, so what a
// stage reads here is the level its own configuration resolved. The readiness
// rule cannot see any of it: Decide acts on the scored findings alone, and
// persona_internal_test.go holds outcome.go to that.

// personaFor is the one place this package turns a level and a role into text,
// and the seam a test can stand a known text at. Everything that carries a
// persona reads it through here — coderPersona and reviewerPersona below are
// the only callers — and no other file in the package names rigor.Persona, so
// there is one answer to "where does a prompt's persona come from" rather than
// one per stage.
//
// It is a variable rather than a direct call for the reason the prompts are
// checked by their text: a prompt is a string a test can only assert about by
// putting a known text in it, and the level's own texts are pinned in their own
// package (internal/rigor's persona_test.go).
var personaFor = rigor.Persona

// coderPersona is the coder text of the level the run has reached: the level
// D22's last moment resolved, and not the one the run was launched with, so a
// brief built after a rise carries the raised level's stance — a run that
// climbs into an area's level mid-flight works the rest of its rounds there.
//
// It is "" only where nothing has resolved a level: a record with no rigor
// block, which every prompt site leaves behind at bootstrap, or a level the
// table does not know. A stage handed no persona is a stage asked nothing —
// never another level's stance.
func (r *run) coderPersona() string { return r.persona(rigor.Coder) }

// reviewerPersona is the same seam for the reviewer's half of the level: the
// spec's review matching, where a reviewer judges by the task's own level
// rather than by a standard of its own (§5, LF14).
func (r *run) reviewerPersona() string { return r.persona(rigor.Reviewer) }

// persona is one role's text at the level the run has reached, and the one
// place reachedRigor is read for a persona.
//
// A run with no record at all answers "" beside a run whose record carries no
// rigor block: both have resolved no level, which is reachedRigor's own answer
// before the first moment (rigor.go) and the same empty text a level outside
// the table gives. A prompt builder therefore reads a persona without first
// having to hold a whole record, which is what a review prompt rendered from a
// spec alone — reviewmode's own tests, and any later reader of one prompt —
// would otherwise have to.
func (r *run) persona(role rigor.Role) string {
	if r.rec == nil {
		return ""
	}
	return personaFor(r.reachedRigor(), role)
}
