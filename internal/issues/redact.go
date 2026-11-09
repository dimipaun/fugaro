package issues

import (
	"regexp"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/redact"
)

// Redact is the gate every piece of an issue passes before it is written down
// or sent anywhere: the values this machine holds and the shapes nobody has to
// hold to know they are a credential, and then the two things only an issue
// has to lose — an email address and a path under a home directory (F5). The
// masks are not held back for the post: the issue file is read by a teammate
// and may be posted by them, so its text leaves the machine already without
// the person who wrote it.
//
// It substitutes and never cuts, so an excerpt that arrives short stays whole
// and stays short: the length of the excerpt is the collector's decision, not
// this pass's.
func Redact(text string, secrets []string) string {
	return redact.Report(redact.Text(text, secrets))
}

// scrubMin is the shortest name Scrub replaces. A name of two characters is
// as likely to sit inside an ordinary word as to be a name, and the leak it
// would close is a two-letter project name.
const scrubMin = 3

// Scrub removes the names of the project, the repository and the
// organisation from text on its way into a public post (F4): they are what
// says whose private work this is, and the reporter's own words name them as
// readily as a URL does. The names are matched without regard to case,
// because the same name is written `acme-widgets` in a path and `Acme-Widgets`
// in a sentence.
//
// The longest name is tried first, because one name is often the beginning of
// another — an organisation and the repositories under it — and the short one
// first would leave the rest of the long one standing beside the marker. All
// the names go in one alternation, and the text they are matched in carries
// no marker: `[REDACTED]` holds `red`, `act` and `ted` as readily as any other
// text does, so a pass that read a marker as text would hand the post
// `[[REDACTED]ACTED]` in place of the marker.
//
// It is not a boundary: a name the caller does not list survives, and one
// that is also an ordinary word takes that word with it. Both are the
// caller's to weigh, because only the caller knows what the project is
// called.
func Scrub(text string, names []string) string {
	longest := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); len(name) >= scrubMin {
			longest = append(longest, name)
		}
	}
	if len(longest) == 0 {
		return text
	}
	sort.Slice(longest, func(i, j int) bool { return len(longest[i]) > len(longest[j]) })
	quoted := make([]string, len(longest))
	for i, name := range longest {
		quoted[i] = regexp.QuoteMeta(name)
	}
	anyOf := regexp.MustCompile(`(?i)(?:` + strings.Join(quoted, "|") + `)`)
	// Splitting on the marker is what holds it aside: the marker the text
	// arrived with, written by Redact, and the one this pass writes are the
	// same bytes, and neither is text for a name to match in.
	segments := strings.Split(text, redactedValue)
	for i, segment := range segments {
		segments[i] = anyOf.ReplaceAllLiteralString(segment, redactedValue)
	}
	return strings.Join(segments, redactedValue)
}
