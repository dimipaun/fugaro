package report

import "github.com/muesli/termenv"

// NoColor is the CLI's one rule for turning colour off: --no-color, NO_COLOR
// set to anything at all (the no-color.org rule is "present and not an empty
// string"), and TERM=dumb. It is the rule fugaro watch has today, which now
// takes it from here, so one binary has one answer.
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
	glyphSkipped = glyph{"–", "-"}
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
	colourSkipped = termenv.ANSIColor(8)
	colourFailed  = termenv.ANSIColor(1)
	colourLeft    = termenv.ANSIColor(3)
)

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
