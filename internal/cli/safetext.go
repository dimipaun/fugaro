package cli

import "github.com/dimipaun/fugaro/internal/safetext"

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

// ErrorText is err's message made safe for the terminal, for the command's
// top-level error line. An error can quote a stored or remote value, so
// its controls go the way a view's do; its newlines stay, because some
// errors list several problems.
func ErrorText(err error) string { return multiLine(err.Error()) }

// multiLine is like oneLine, but keeps newlines, for blocks printed as
// several lines (the agent's message).
func multiLine(s string) string { return safetext.MultiLine(s) }
