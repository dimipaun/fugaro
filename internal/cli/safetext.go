package cli

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// Terminal-safe text. Much of what the views print
// comes from objects the job's service account, and so the agent, can
// write (result.json, launch.json, transcripts) or from Cloud Logging. Printed
// raw, an escape sequence in such a string could rewrite the operator's
// screen (CSI), plant a hyperlink (OSC 8) or set the clipboard (OSC 52).
// Every human-format field of such origin goes through oneLine or
// multiLine; JSON output is escaped by encoding/json instead.

// oneLine is s made safe for one line of a terminal: ESC, CSI, OSC, DCS,
// SOS, PM and APC sequences (7- and 8-bit) are dropped, and every other C0
// control but tab, DEL, C1 control, bidi control, zero-width character
// and invalid UTF-8 byte becomes "?". A newline becomes "?" too, so a
// field can't forge a line of its own.
func oneLine(s string) string { return safetext.OneLine(s) }

// cellOneLine is oneLine for a value that shares a tabwriter row with other
// cells (ls's table): unlike oneLine, a literal tab also becomes "?", since
// one would open a new column instead of staying inside this cell.
func cellOneLine(s string) string { return safetext.TableCell(s) }

// ErrorText is err's message made safe for the terminal, for the command's
// top-level error line. An error can quote a stored or remote value, so
// its controls go the way a view's do; its newlines stay, because some
// errors list several problems.
func ErrorText(err error) string { return multiLine(err.Error()) }

// shownError is a failure the command has already printed: the step that
// failed carries it on the screen, whole while the run has no log to keep it
// in and one clause when it has one, with the log's path and the command that
// resumes the run under it. The top-level line would be the same error a
// second time (the owner's feedback of 2026-11-13), so a failed converge
// hands its caller this instead of the bare error.
type shownError struct{ err error }

func (e *shownError) Error() string { return e.err.Error() }
func (e *shownError) Unwrap() error { return e.err }

// ShownOnScreen is whether the screen has already read this failure: main
// prints its own "fugaro: <error>" line for every command that returns one,
// and for these the run's own account is the whole of it — the exit code and
// the error itself are unchanged, so a caller that reads them sees exactly what
// it saw before.
func ShownOnScreen(err error) bool {
	var shown interface{ shownOnScreen() bool }
	return errors.As(err, &shown)
}

func (e *shownError) shownOnScreen() bool { return true }

// multiLine is like oneLine, but keeps newlines, for blocks printed as
// several lines (the agent's message).
func multiLine(s string) string { return safetext.MultiLine(s) }
