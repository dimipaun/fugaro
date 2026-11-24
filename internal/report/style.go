package report

import (
	"io"
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
	// colourHeader is one section's arrow, the blue a heading is drawn in.
	colourHeader = termenv.ANSIColor(4)
	// colourWarning is a line the run has to say but that is not a failure, and
	// it is the same yellow a step left for the user is drawn in: the two are
	// "something for you to read" rather than "something went wrong".
	colourWarning = termenv.ANSIColor(3)
)

// Style is the CLI's one colour decision as a value a caller can hold before it
// has a reporter: the wizard's questions are asked before the converge builds
// one — they are what the converge is about — and a question drawn in one
// syntax with the steps under it in another would be two hands on one screen.
// NewStyle is the whole of how one is made; everything that prints after the
// converge has a reporter goes through that instead. The zero Style is plain
// text.
type Style struct{ profile termenv.Profile }

// NewStyle is the colour decision for one command's run, made by the same rule
// the reporter uses (New): a terminal, colour not turned off (--no-color,
// NO_COLOR, TERM=dumb), markers not kept to ASCII, and no --json.
func NewStyle(o Options) Style {
	if o.Getenv == nil {
		o.Getenv = func(string) string { return "" }
	}
	if o.IsTerminal == nil {
		o.IsTerminal = func(io.Writer) bool { return false }
	}
	if !o.JSON && !o.ASCII && !NoColor(o.NoColor, o.Getenv) && o.IsTerminal(o.Out) {
		// ANSI: the sixteen colours, which every terminal draws and which the
		// legacy Windows console draws once virtual terminal processing is on.
		return Style{profile: termenv.ANSI}
	}
	return Style{}
}

// plain is whether this style draws nothing at all, which the zero Style does.
// NewStyle answers ANSI or plain and nothing else, so the question is "is this
// the one profile that colours" — and it is asked this way round because
// termenv's own zero profile is TrueColor: a run built without a style (a test's
// hand-built engine) would otherwise paint in a profile nothing chose.
func (s Style) plain() bool { return s.profile != termenv.ANSI }

// Header renders one section's heading the way Homebrew draws it: a bold blue
// arrow, and the text in bold after it. It is what a block of a run's output is
// announced by, so a person scrolling a long run sees where one part ends and
// the next begins.
func (s Style) Header(text string) string {
	return s.styled("==>", styleOpts{colour: colourHeader, paint: true, bold: true}) + " " + s.Bold(text)
}

// Error renders a failure's own label: `Error:` in bold red. The message goes
// after it, and is not coloured — the label is what a person scans for.
func (s Style) Error(text string) string {
	return s.styled("Error:", styleOpts{colour: colourFailed, paint: true, bold: true}) + " " + text
}

// Warning renders a warning's own label: `Warning:` in bold yellow. What it
// warns about is not a failure and the run goes on, which is why it is not the
// failure's red.
func (s Style) Warning(text string) string {
	return s.styled("Warning:", styleOpts{colour: colourWarning, paint: true, bold: true}) + " " + text
}

// Bold is text drawn in bold, for a line that has to stand out from the ones
// around it. A pipe, a --no-color run and a --json document all leave it as it
// is.
func (s Style) Bold(text string) string { return s.styled(text, styleOpts{bold: true}) }

// Dim is text drawn faint, for what is said beside the thing it belongs to: a
// suggested answer, a count of steps a stopped run never reached. A suggested
// answer is dim rather than coloured because it is a value the run offers and
// not a result it reports.
func (s Style) Dim(text string) string { return s.styled(text, styleOpts{faint: true}) }

// Ask renders one question's own line: the label in bold, what the question
// says about the shape of the answer after it, and the suggested answer dimmed
// inside brackets — which is the whole of what "press Enter to take it" would
// otherwise have to say in a sentence. It ends at the suggestion: the caller
// writes the colon the answer is typed after, because a question is a prompt
// and not a line.
func (s Style) Ask(label, hint, suggestion string) string {
	line := s.Bold(label)
	if hint != "" {
		line += ": " + hint
	}
	if suggestion != "" {
		line += " " + s.Dim("["+suggestion+"]")
	}
	return line
}

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
	return r.status(k, word, width, true)
}

// PlainStatus is Status as a log keeps it: the same mark in the same alphabet,
// the same word and the same padding, with no colour. A caller that prints its
// own lines writes the log's copy of a row with it, because a log is read with
// a pager, where the colour that helps a person watching is noise (D1352).
func (r *Reporter) PlainStatus(k Kind, word string, width int) string {
	return r.status(k, word, width, false)
}

// status is the one renderer behind the two: paint is whether the mark and the
// word are drawn in their colour. A nil reporter never is, so a caller driving
// the converge with no reporter at all (--print-vars, a test's hand-built
// engine) gets the plain text it printed before one existed.
func (r *Reporter) status(k Kind, word string, width int, paint bool) string {
	m := marks[k]
	mark := m.g.text(r != nil && r.o.ASCII)
	pad := statusPad(mark, word, width)
	if !paint || r == nil {
		return mark + " " + word + pad
	}
	return r.painted(mark+" "+word, m.c) + pad
}

// statusPad is the spaces that line a caller's columns up: width minus the
// status's visible text, which is the mark, the space and the word. The escape
// sequences termenv writes are not counted, which is the whole reason the
// padding is added here and not by the caller.
func statusPad(mark, word string, width int) string {
	if n := width - len([]rune(mark)) - 1 - len([]rune(word)); n > 0 {
		return strings.Repeat(" ", n)
	}
	return ""
}

// text is the glyph in the alphabet the --ascii decision picked.
func (g glyph) text(ascii bool) string {
	if ascii {
		return g.ascii
	}
	return g.sym
}

// styled is the decoration one piece of text is drawn with: the colour it
// carries when it carries one, and whether it is bold or faint. It is the one
// renderer behind every styled helper above, so the colour decision those
// helpers rest on is made in exactly one place.
type styleOpts struct {
	colour termenv.ANSIColor
	paint  bool
	bold   bool
	faint  bool
}

// painted renders one mark in the profile the reporter decided on. termenv
// is used only to render: its own colour decision (ColorProfile, and the
// CLICOLOR_FORCE inside it) is never consulted, so an environment that forces
// colour cannot put ANSI on a pipe (T4(a), D1355). The Ascii profile is what
// keeps colour-off to one call rather than a branch at every print.
func (s Style) painted(text string, c termenv.ANSIColor) string {
	return s.styled(text, styleOpts{colour: c, paint: true})
}

func (s Style) styled(text string, o styleOpts) string {
	if s.plain() {
		return text
	}
	out := s.profile.String(text)
	if o.bold {
		out = out.Bold()
	}
	if o.faint {
		out = out.Faint()
	}
	if o.paint {
		out = out.Foreground(o.colour)
	}
	return out.String()
}
