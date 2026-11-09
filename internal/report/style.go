package report

import (
	"strings"

	"github.com/muesli/termenv"
)

// NoColor is the CLI's one rule for turning colour off: --no-color, NO_COLOR
// set to anything at all (the no-color.org rule is "present and not an empty
// string"), and TERM=dumb. It is fugaro watch's own rule today, copied here
// rather than moved: CL2 is the task that makes watch take it from here, so
// that one binary has one answer.
//
// CLICOLOR and CLICOLOR_FORCE are deliberately not read: they colour a pipe
// when they force it, against the spec's "off when not a terminal" (T4(a)).
// The reporter decides, and termenv only renders.
func NoColor(flag bool, getenv func(string) string) bool {
	if flag || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return true
	}
	return false
}

// glyph is a mark a step line starts with, in the two alphabets the reporter
// draws: the symbols, and the ASCII fallback for a console or a font that
// cannot draw them (--ascii, as the watch screen has).
type glyph struct{ sym, ascii string }

var (
	glyphDone    = glyph{"✔", "+"}
	glyphTodo    = glyph{"○", "o"}
	glyphSkipped = glyph{"–", "-"}
	glyphWaiting = glyph{"↳", ">"}
	glyphFailed  = glyph{"✖", "x"}
	glyphLeft    = glyph{"!", "!"}
)

// The colours are the ANSI sixteen, which every terminal draws and the legacy
// Windows console draws once virtual terminal processing is on (Check 311). A
// step left for the user has its own colour, because it is neither a failure
// nor a success.
const (
	colourRunning = termenv.ANSIColor(6)
	colourDone    = termenv.ANSIColor(2)
	colourTodo    = termenv.ANSIColor(6)
	colourSkipped = termenv.ANSIColor(8)
	colourFailed  = termenv.ANSIColor(1)
	colourLeft    = termenv.ANSIColor(3)
)

// Kind is the status a line of the account carries: the mark it starts with
// and the colour that mark and its word are drawn in.
//
// The four ends of a step are Done, Skipped, Failed and Left; the converge's
// plan view adds two a step's own outcomes cannot say — a stage with something
// to do (Todo) and one that waits for another (Waiting) — because its rows
// stand for work not done yet and are not runs (see initflow's view). The word
// beside the kind is the caller's: the converge names its states ("done",
// "will do", "waiting", "left for you"), and the reporter owns only the
// alphabet they are drawn in.
type Kind uint8

const (
	KindDone Kind = iota
	KindTodo
	KindSkipped
	KindWaiting
	KindFailed
	KindLeft
)

// marks is the alphabet one status is drawn in, indexed by Kind: the symbol
// and the ASCII fallback, and the colour. Waiting and Skipped are the same
// dim, because both are "not this run's work" — neither a success nor a
// failure nor something the user owes.
var marks = [...]struct {
	g glyph
	c termenv.ANSIColor
}{
	KindDone:    {glyphDone, colourDone},
	KindTodo:    {glyphTodo, colourTodo},
	KindSkipped: {glyphSkipped, colourSkipped},
	KindWaiting: {glyphWaiting, colourSkipped},
	KindFailed:  {glyphFailed, colourFailed},
	KindLeft:    {glyphLeft, colourLeft},
}

// Status renders one line's status for a caller that prints its own line: the
// mark in the run's alphabet and colour, the word beside it, and the padding
// that lines the caller's columns up.
//
// It is a method on the reporter and not a free function because the reporter
// is the one place that decides whether colour is on (T4(a), D1355) — and it
// is nil-safe, so a caller driving the converge with no reporter at all
// (--print-vars, a test's hand-built engine) gets the plain text it printed
// before one existed. width pads to the visible text; the escape sequences
// termenv writes are not counted, which is the whole reason the padding is
// added here and not by the caller.
func (r *Reporter) Status(k Kind, word string, width int) string {
	m := marks[k]
	plain := m.g.text(false) + " " + word
	pad := ""
	if n := width - len([]rune(plain)); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	if r == nil {
		return plain + pad
	}
	return r.painted(m.g.text(r.o.ASCII)+" "+word, m.c) + pad
}

// text is the glyph in the alphabet the --ascii decision picked.
func (g glyph) text(ascii bool) string {
	if ascii {
		return g.ascii
	}
	return g.sym
}

// painted renders one mark in the profile the reporter decided on. termenv
// is used only to render: its own colour decision (ColorProfile, and the
// CLICOLOR_FORCE inside it) is never consulted, so an environment that forces
// colour cannot put ANSI on a pipe (T4(a), D1355). The Ascii profile is what
// keeps colour-off to one call rather than a branch at every print.
func (r *Reporter) painted(text string, c termenv.ANSIColor) string {
	return r.profile.String(text).Foreground(c).String()
}
