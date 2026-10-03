package gitprov

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The status section of a pull request description: a block the runner
// keeps current while a run is in progress. It sits between two marker
// lines and carries a visible "### Fugaro status" header.
//
// Two marker forms are recognised everywhere; the writer picks one:
//
//	[//]: # (fugaro:status begin)      link reference: renders nothing
//	<!-- fugaro:status begin -->       HTML comment: for a provider that
//	                                   shows link references
//
// Rules, all pinned by tests:
//   - A marker is a whole line (up to 3 spaces of indent, case and inner
//     spacing tolerated). Markers are recognised everywhere, code fences
//     included: tracking fences would let an unclosed fence in the
//     description hide the real section from the parser.
//   - A section is a begin line followed by an end line. A begin met
//     while another is open makes the earlier one a stray; an end with no
//     open begin is a stray. Strays are ordinary text and are never
//     touched, so one lone marker means "no section" and a fresh one is
//     appended.
//   - Every byte outside a section survives Replace and Strip. Duplicate
//     sections collapse into the first, which is replaced in place.
//   - Replace appends the block after "\n\n" (or "\r\n\r\n" when the body
//     uses CRLF), nothing when the body is empty; Strip undoes exactly
//     that, so Strip(Replace(b, s)) == b for any b without a section.
//   - The section text can't forge a marker: marker-looking text and
//     report markers are removed from it.

// StatusHeader is the visible first line of the section.
const StatusHeader = "### Fugaro status"

// Description size limits in bytes (the strictest reading of each
// provider's character limit, so a multibyte body never overflows).
const (
	GitHubBodyLimit    = 65536
	BitbucketBodyLimit = 32768
)

// statusTruncNote ends a section cut to fit the limit.
const statusTruncNote = "_(status truncated to fit the description size limit)_"

// StatusForm is the marker syntax the writer uses.
type StatusForm int

const (
	// StatusLinkRef is `[//]: # (fugaro:status begin)`.
	StatusLinkRef StatusForm = iota
	// StatusHTML is `<!-- fugaro:status begin -->`.
	StatusHTML
)

// StatusOptions tunes ReplaceStatusWith.
type StatusOptions struct {
	Form StatusForm
	// Limit is the maximum size of the whole body in bytes; 0 means none.
	Limit int
}

var (
	statusLineRE = regexp.MustCompile(`(?i)^ {0,3}(?:\[//\]:[ \t]*#[ \t]*\([ \t]*fugaro:status[ \t]+(begin|end)[ \t]*\)|<!--[ \t]*fugaro:status[ \t]+(begin|end)[ \t]*-->)[ \t]*$`)
	// statusInlineRE finds marker-looking text anywhere, for sanitising.
	openReportRE   = regexp.MustCompile(`(?i)<!--\s*fugaro:report`)
	statusInlineRE = regexp.MustCompile(`(?is)<!--\s*fugaro:status.*?-->|\[//\]:\s*#\s*\(\s*fugaro:status[^)\n]*\)?|<!--\s*fugaro:status`)
)

type statusSpan struct{ start, end int } // end excludes the end line's terminator

// statusSpans finds the sections of body.
func statusSpans(body string) []statusSpan {
	var spans []statusSpan
	open := -1
	for pos := 0; pos < len(body); {
		nl := strings.IndexByte(body[pos:], '\n')
		next, line := len(body), body[pos:]
		if nl >= 0 {
			next, line = pos+nl+1, body[pos:pos+nl]
		}
		line = strings.TrimSuffix(line, "\r")
		if m := statusLineRE.FindStringSubmatch(line); m != nil {
			if strings.EqualFold(m[1]+m[2], "begin") {
				open = pos
			} else if open >= 0 {
				e := pos + len(line)
				spans = append(spans, statusSpan{open, e})
				open = -1
			}
		}
		pos = next
	}
	return spans
}

func eolOf(body string) string {
	if i := strings.IndexByte(body, '\n'); i > 0 && body[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// removeSpan deletes a section and the blank-line separation around it.
func removeSpan(body string, sp statusSpan) string {
	s, e := sp.start, sp.end
	// the end line's own terminator
	if strings.HasPrefix(body[e:], "\r\n") {
		e += 2
	} else if strings.HasPrefix(body[e:], "\n") {
		e++
	}
	switch {
	case e >= len(body) && s > 0:
		// at the end: take the separator Replace put before it
		if strings.HasSuffix(body[:s], "\r\n\r\n") {
			s -= 4
		} else if strings.HasSuffix(body[:s], "\n\n") {
			s -= 2
		}
	default:
		// followed by a blank line: take it too, so the text on both
		// sides keeps one blank line between them
		if strings.HasPrefix(body[e:], "\r\n") {
			e += 2
		} else if strings.HasPrefix(body[e:], "\n") {
			e++
		}
	}
	return body[:s] + body[e:]
}

// StripStatus removes every status section (either marker form) from body
// and nothing else.
func StripStatus(body string) string {
	for {
		spans := statusSpans(body)
		if len(spans) == 0 {
			return body
		}
		body = removeSpan(body, spans[len(spans)-1])
	}
}

// ReplaceStatus sets the status section of body to section (the text
// between the markers; the header is added when missing) using link
// reference markers and no size limit.
func ReplaceStatus(body, section string) string {
	out, _ := ReplaceStatusWith(body, section, StatusOptions{})
	return out
}

// ReplaceStatusWith is ReplaceStatus with a marker form and a size limit.
// It reports whether the section was cut (or, when even the markers don't
// fit, left out) to honour the limit; the rest of the body is never cut.
func ReplaceStatusWith(body, section string, o StatusOptions) (string, bool) {
	// Collapse duplicates into the first section.
	for {
		spans := statusSpans(body)
		if len(spans) < 2 {
			break
		}
		body = removeSpan(body, spans[len(spans)-1])
	}
	spans := statusSpans(body)
	// Judge the line ending without the section, so it is the same before
	// and after the section is added.
	eol := eolOf(StripStatus(body))
	rest := len(body)
	sep := ""
	if len(spans) == 1 {
		rest -= spans[0].end - spans[0].start
	} else if body != "" {
		sep = eol + eol
	}
	block, trunc := statusBlock(section, o, eol, o.Limit-rest-len(sep))
	if block == "" { // nothing fits: leave the body as it is
		return body, true
	}
	if len(spans) == 1 {
		return body[:spans[0].start] + block + body[spans[0].end:], trunc
	}
	return body + sep + block, trunc
}

// statusBlock renders the marked block, cut to budget bytes when o.Limit is
// set. It returns "" when even the bare markers exceed the budget.
func statusBlock(section string, o StatusOptions, eol string, budget int) (string, bool) {
	begin, end := "[//]: # (fugaro:status begin)", "[//]: # (fugaro:status end)"
	if o.Form == StatusHTML {
		begin, end = "<!-- fugaro:status begin -->", "<!-- fugaro:status end -->"
	}
	lines := cleanSection(section)
	frame := len(begin) + len(end) + 2*len(eol)
	size := func(ls []string) int {
		n := frame
		for _, l := range ls {
			n += len(l) + len(eol)
		}
		return n
	}
	trunc := false
	if o.Limit > 0 && size(lines) > budget {
		trunc = true
		note := statusTruncNote
		kept := lines
		for len(kept) > 0 && size(append(append([]string{}, kept...), note)) > budget {
			kept = kept[:len(kept)-1]
		}
		lines = append(append([]string{}, kept...), note)
		if size(lines) > budget {
			// even the first line is too long: cut it at a rune boundary
			room := budget - frame - len(eol) - len(note) - len(eol)
			first := ""
			if len(cleanSection(section)) > 0 && room > 0 {
				first = cutRunes(cleanSection(section)[0], room)
			}
			lines = nil
			if first != "" {
				lines = append(lines, first)
			}
			lines = append(lines, note)
			if size(lines) > budget {
				lines = nil
				if size(lines) > budget {
					return "", true
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString(begin + eol)
	for _, l := range lines {
		b.WriteString(l + eol)
	}
	b.WriteString(end)
	return b.String(), trunc
}

func cutRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

// cleanSection normalises the text to lines without terminators, makes
// sure the header leads, and removes anything that could forge a status
// or report marker.
func cleanSection(section string) []string {
	s := strings.ReplaceAll(section, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for { // removing one match can join text into another
		t := statusInlineRE.ReplaceAllString(s, "")
		t = anyMarkerRE.ReplaceAllString(t, "")
		if t == s {
			break
		}
		s = t
	}
	s = strings.TrimRight(openReportRE.ReplaceAllString(s, ""), "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != StatusHeader {
		lines = append([]string{StatusHeader}, lines...)
	}
	return lines
}
