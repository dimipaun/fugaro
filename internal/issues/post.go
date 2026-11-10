package issues

// This file builds the one artifact of an issue that leaves this machine for a
// public repository: the post `fugaro issue submit` files with `gh issue
// create` (D1722). Everything else stays where the team reads it — the local
// file and the shared store keep the reporter, the run, the project and the
// redacted failed command — and the post is a strict projection of the record,
// put through the gate again and stripped of the project's own names, so what
// a reader of the public repository learns is the bug and not whose machine it
// was (F4, F5).
//
// The projection is by construction rather than by removal: BuildPost reads
// the fields it posts and never the header, so a field added to the record
// later cannot start leaving the machine because a builder copied a struct.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// postTitleRunes is how much of the summary the title carries. GitHub's title
// is one line with a limit of its own (256 characters), and a summary is a
// paragraph the diagnosis wrote, so the title is cut well inside that limit —
// a title is there to tell one issue from another in a list, not to hold the
// finding.
const postTitleRunes = 120

// postLogBudget is how much of the log the post carries. The spec asks for a
// short excerpt and not the log dump (spec 3), and the end of a failed
// command's log is where the failure is: what a reader needs is the last of
// it, and a whole log would bury the diagnosis under its dials.
const postLogBudget = 2 << 10

// postLogCutNotice says the excerpt is the end of a longer log. Without it a
// reader takes a log that starts mid-line for the whole story.
const postLogCutNotice = "The log is cut: this is the end of it."

// Post is one issue as GitHub would receive it: the bytes of the issue and the
// name the fallback link uses for the rest of the text. Nothing else of the
// record is here, and nothing here is a path on this machine.
type Post struct {
	// Title and Body are what `gh issue create` is given as --title and
	// --body-file.
	Title string
	Body  string
	// Ref is the reference `fugaro issue show` takes for this issue: the
	// number once the issue has one, its local ID before that (F23). It is
	// not part of the post and it is not in the fallback link either — the
	// link is public bytes, and an unnumbered issue's reference is its local
	// ID, which is built from the run's own stem and stamp and is identity
	// (D1722). It is for the terminal: the command printing the fallback puts
	// `fugaro issue show <ref>` beside the link, where the person filing it
	// reads it and nobody else does.
	Ref string
}

// BuildPost is the record as the public post carries it (D1722): the Fugaro
// version, the platform, the class, the summary, the diagnosis, the steps and
// a short tail of the log — redacted, and with the project, the repository and
// the organisation's own names taken out.
//
// names are those names, as this machine knows them (a project name, a
// repository slug, an organisation), and the caller passes what it has: Scrub
// is not a boundary, and a name nobody lists survives (F24). The header is the
// part of the record that is not projected at all — the reporter, the run, the
// installation, the status, the URLs and the failed command stay on this
// machine.
func BuildPost(i Issue, names []string) Post {
	return Post{
		Title: postTitle(i, names),
		Body:  Scrub(postBody(i, names), names),
		Ref:   postRef(i),
	}
}

// postRef is the issue's name in the fallback link's tail: the number once the
// shared store gave it one, and the local ID until then, which `fugaro issue
// show` takes as its own reference (F23). The command names the issue and never
// a file: a path in a public post would name this machine's user (D1722).
func postRef(i Issue) string {
	if i.Number == 0 {
		return i.LocalID
	}
	return strconv.Itoa(i.Number)
}

// postTitle is the summary as GitHub's title line takes it: the summary's
// first non-empty line — the diagnosis writes the point of the finding first —
// cut to postTitleRunes and made safe for one line (F6). A summary with
// nothing in it leaves the title to the version and the platform, which a
// filer still needs: GitHub refuses an issue with no title at all.
func postTitle(i Issue, names []string) string {
	line := ""
	for _, l := range strings.Split(i.Summary, "\n") {
		if line = strings.TrimSpace(l); line != "" {
			break
		}
	}
	if line == "" {
		line = "fugaro " + i.FugaroVersion + " on " + i.OS
	}
	// The gate and the names run before the cut: a value or a name sitting on
	// the boundary is then replaced whole, rather than cut in half and left as
	// something no rule recognises as the thing it was.
	return safetext.Clip(Scrub(safetext.OneLine(line), names), postTitleRunes)
}

// postBody is the post's body: the one line a reader needs to place the
// finding, then the record's three text sections and the log's tail. A section
// with nothing in it is left out rather than printed as an empty heading, and
// the log keeps its fence, so a log line cannot be read as part of the prose
// around it.
func postBody(i Issue, names []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fugaro %s on %s. Class: %s.\n", i.FugaroVersion, i.OS, i.Class)
	for _, s := range []struct{ name, text string }{
		{"Summary", i.Summary},
		{"Diagnosis", i.Diagnosis},
		{"Steps to reproduce", i.Steps},
	} {
		if strings.TrimSpace(s.text) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.name, s.text)
	}
	tail, cut := postLog(i.Logs, names)
	if tail != "" {
		if cut {
			b.WriteString("\n" + postLogCutNotice + "\n")
		}
		b.WriteString("\n## Log\n\n")
		writeFenced(&b, tail)
	}
	return b.String()
}

// postLog is the log as the post carries it: the last postLogBudget bytes of
// the excerpts that were saved, cut at a line boundary so the excerpt does not
// begin with half a line.
//
// The boundary is taken only while it keeps half the budget — the rule
// cutForURL follows for the fallback body. A log whose last line is longer
// than the whole budget has no boundary in reach that leaves anything: the one
// newline left is the one at the end of the text, and advancing to it empties
// the tail. postBody writes the section only when there is a tail to write, so
// that advance would drop a multi-kilobyte log from the post with no excerpt
// and no cut notice to say one was missing — the one thing the post must not
// do with the log it was filed for. Cutting mid-line keeps the log and the
// notice both.
//
// The text goes through the gate and through Scrub *before* it is cut, and
// that order is the point: both passes match a whole value, so a credential or
// a project name the cut fell in the middle of would be text no rule here
// recognises, and the half left standing is still half of it. The body then
// goes through Scrub once more with the rest of it, which is F5's second pass
// over the text of a record that will leave the team's machine.
func postLog(logs []string, names []string) (tail string, cut bool) {
	text := Scrub(strings.Join(logs, "\n\n"), names)
	if len(text) <= postLogBudget {
		return text, false
	}
	at := len(text) - postLogBudget
	if nl := strings.IndexByte(text[at:], '\n'); nl >= 0 && nl+1 <= postLogBudget/2 {
		at += nl + 1
	}
	for at < len(text) && !utf8.RuneStart(text[at]) {
		at++
	}
	return text[at:], true
}

// Preview is the post as a person reads it before anything is filed: the
// title, the body, and the hash of the bytes a `submit --ask` would send
// (F19).
//
// The text is printed through the terminal pass on its way out (F6). The body
// is the diagnosis' own words about log text this machine did not write, and a
// preview that printed an escape sequence would let an issue text steer the
// terminal of the person about to file it. The hash is of the bytes and not of
// what is printed here, which is the one difference between them: the pass
// marks a control character where the bytes carry it, and the person's yes
// files the bytes.
func Preview(p Post) string {
	var b strings.Builder
	b.WriteString("Title: " + safetext.OneLine(p.Title) + "\n\n")
	b.WriteString(safetext.MultiLine(p.Body))
	if !strings.HasSuffix(p.Body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\nHash: " + p.Hash() + " (sha256 of the bytes that would be filed)\n")
	return b.String()
}

// Hash is the SHA-256 of the post's exact bytes, in lower-case hex (F19). The
// title and the body are both filed, so both are in it: a post whose bytes
// differ anywhere differs here, which is what lets `submit --ask` file the
// bytes it previewed and nothing else, and what lets two submitters agree on
// which record they are filing (T-IS7, T-IS12).
func (p Post) Hash() string {
	sum := sha256.Sum256(p.bytes())
	return hex.EncodeToString(sum[:])
}

// bytes is the post as one document: the title, a blank line, the body. `gh`
// receives the two as separate arguments, and this is the one form they come
// from, so a hash over it covers both.
func (p Post) bytes() []byte { return []byte(p.Title + "\n\n" + p.Body) }

// newIssueURLBudget is how much of the fallback link's body survives encoding.
// A browser and GitHub both refuse a long URL, so the body is cut to a fixed
// budget and GitHub is never sent more than it can take (F16); the budget is
// conservative because the exact limit is not measured (U3).
const newIssueURLBudget = 6 << 10

// newIssueURLCutNotice is what a cut body ends with: it says the text is cut
// and names the command that prints the whole issue on the machine that holds
// it, with `<id>` standing where the reference goes.
//
// The reference itself is not here, and that is the point. These bytes are in
// a URL that becomes a public issue, and an issue with no number yet is named
// by its local ID — the run's own stem and stamp, which TestPostStripsIdentity
// counts as identity — so a link that named it would put the run on GitHub
// under a heading that says the post carries no identity (D1722). The command
// is a placeholder rather than absent because a body that stops mid-sentence
// with nothing said would read as the whole story; the person filing it is
// told the reference itself on their own terminal, beside the link.
const newIssueURLCutNotice = "\n\nThe text is cut: `fugaro issue show <id>` on the machine that filed this prints the whole issue.\n"

// NewIssueURL is the link the fallback prints when there is no `gh` to file
// with (spec 5, F16): GitHub's new-issue form with the title and the body
// already in it, so a person with a browser and no gh can still send exactly
// what the preview showed. The body is cut to newIssueURLBudget once encoded —
// the form's query carries it, and the escaping is what the browser will
// decode — and the cut body ends with the command that prints the rest, naming
// no issue: the reference is identity for an issue with no number yet
// (newIssueURLCutNotice, D1722).
//
// The repository is updatecheck.Repo's, and the link is a URL and not a
// command: nothing here runs `gh`, and the person holds the submit button.
func NewIssueURL(p Post) string {
	head := "https://github.com/" + updatecheck.Repo + "/issues/new?title=" +
		url.QueryEscape(safetext.OneLine(p.Title)) + "&body="
	if len(url.QueryEscape(p.Body)) <= newIssueURLBudget {
		return head + url.QueryEscape(p.Body)
	}
	return head + url.QueryEscape(cutForURL(p.Body)+newIssueURLCutNotice)
}

// cutForURL is the longest prefix of body that fits the budget once
// newIssueURLCutNotice is appended and the whole of it is percent-encoded.
// Escaping is monotonic in the length of its input, so the largest prefix that
// fits is found by bisection rather than by cutting and re-encoding one line at
// a time: a body of a few bytes and one of many kilobytes take the same handful
// of passes.
//
// That cut then backs off to the last line boundary in it — a person reads
// whole lines — but only while the boundary keeps at least half of what fit.
// A link carries a finding and not a text, and a body whose content line is
// longer than the whole allowance (a summary written as one paragraph, a pasted
// URL with no space in it) has no interior boundary to retreat to: backing off
// to the one before it hands the person a link holding a header and a pointer
// and none of the finding. Half is where tidiness stops being worth the budget;
// below it the cut stays mid-line, and the notice that follows says so either
// way.
func cutForURL(body string) string {
	fits := func(n int) bool {
		return len(url.QueryEscape(body[:n]+newIssueURLCutNotice)) <= newIssueURLBudget
	}
	if fits(len(body)) {
		return body
	}
	lo, hi := 0, len(body)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if at := strings.LastIndexByte(body[:lo], '\n'); at >= lo/2 {
		lo = at
	}
	return body[:lo]
}
