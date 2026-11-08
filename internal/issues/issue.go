// Package issues keeps the issues `fugaro troubleshoot` finds: one Markdown
// record per issue under the user's state directory, with the whole issue in
// it (F4) and a strict header, and the store `fugaro issue` reads, dismisses
// and files from.
//
// A record is a YAML header between two `---` lines and a Markdown body, so a
// person can read one and a teammate can send one. The header decodes
// strictly: a field this format does not define, a class or a status outside
// the ones it knows, and a record version this binary does not write are all
// errors rather than fields that quietly disappear (D1726, F23). Nothing here
// talks to the network or to the cloud; the shared store is a later slice.
package issues

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/redact"
	"gopkg.in/yaml.v3"
)

// RecordVersion is the record's format version, written in the header and the
// only one this binary reads. A reader outside the binary reads the same
// shape, so a field whose meaning has to change gets a new name and this
// number goes up rather than the field being reused.
const RecordVersion = 1

// headerFence opens and closes a record's header. It is a YAML document
// marker, so the header reads as YAML to anything that wants to.
const headerFence = "---"

// localPrefix starts the local ID and the file name of an issue that has not
// been uploaded yet: `local-<run-stem>` (D1723).
const localPrefix = "local-"

// Class is the bucket a saved issue belongs to: the two outcomes that have
// something to save. A finding that is only this machine's — `configuration`
// or `fixed_in_newer_release` — has no class, so nothing can persist one
// (F6).
type Class string

const (
	// ClassFugaro is a bug in Fugaro itself.
	ClassFugaro Class = "fugaro"
	// ClassUnclear is a finding the diagnosis could not attribute.
	ClassUnclear Class = "unclear"
)

// Valid reports whether c is one of the two classes a record may carry.
func (c Class) Valid() bool { return c == ClassFugaro || c == ClassUnclear }

// UnmarshalYAML refuses a class outside the two, so a hand-edited or a
// steered file cannot invent one — the draft's `local` was exactly that
// (TestIssueRejectsLocalClass).
func (c *Class) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	v := Class(s)
	if !v.Valid() {
		return fmt.Errorf("issues: %q is not a class, want %q or %q", s, ClassFugaro, ClassUnclear)
	}
	*c = v
	return nil
}

// Status is where an issue stands. Once an issue is uploaded the shared store
// is the truth (F13); this is the copy on the machine that reads it.
type Status string

const (
	// StatusPending is saved and not yet filed.
	StatusPending Status = "pending"
	// StatusSubmitted is filed on GitHub.
	StatusSubmitted Status = "submitted"
	// StatusDismissed was filed or set aside by a person.
	StatusDismissed Status = "dismissed"
)

// Valid reports whether s is one of the three statuses a record may carry.
func (s Status) Valid() bool {
	return s == StatusPending || s == StatusSubmitted || s == StatusDismissed
}

// UnmarshalYAML refuses a status outside the three, for the reason Class
// gives.
func (s *Status) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	v := Status(raw)
	if !v.Valid() {
		return fmt.Errorf("issues: %q is not a status, want %q, %q or %q", raw, StatusPending, StatusSubmitted, StatusDismissed)
	}
	*s = v
	return nil
}

// Issue is one issue: the header a reader outside this binary consumes and
// the body the user reads. The header's field names are the wire names, so
// they do not change meaning.
type Issue struct {
	// Version is the record's format version. Marshal writes RecordVersion
	// and a caller leaves it zero; Validate refuses any other value.
	Version int `yaml:"version"`
	// Number is the issue's project number, zero until it is uploaded
	// (D1723).
	Number int `yaml:"number"`
	// LocalID is `local-<run stem>`: the name the issue has before it is
	// uploaded, and the argument that finds it (F23).
	LocalID string `yaml:"local_id"`
	// Run is the run stem of the command that failed, D1609's
	// `<command>-<14-digit UTC>-<pid>`.
	Run string `yaml:"run"`
	// Reporter is who this machine is: the header keeps it, the post never
	// does (D1722).
	Reporter string `yaml:"reporter"`
	// Created is when the issue was saved, from the caller's clock.
	Created time.Time `yaml:"created"`
	// FailedCommand is the command that failed, redacted (D1732): printed by
	// `update`, never run and never posted.
	FailedCommand string `yaml:"failed_command"`
	// Class is the bucket the issue is saved in.
	Class Class `yaml:"class"`
	// FugaroVersion is the version that failed, as the reporter's binary
	// spelled it.
	FugaroVersion string `yaml:"fugaro_version"`
	// OS is the platform that failed.
	OS string `yaml:"os"`
	// Status is where the issue stands.
	Status Status `yaml:"status"`
	// URL is the GitHub issue this was filed as, once it has been.
	URL string `yaml:"url"`
	// Uploaded says the issue is in the shared store.
	Uploaded bool `yaml:"uploaded"`
	// Fingerprint is the hash of the signature the diagnosis returned; two
	// reports of one bug share it (D1724).
	Fingerprint string `yaml:"fingerprint"`
	// ResolvedIn is the release that fixed the issue, filled in by the
	// `update` command's own spec and empty until then (F27, D1736).
	ResolvedIn string `yaml:"resolved_in"`
	// Regression says the reporter's version is already at or above
	// ResolvedIn, so this is the same bug back (F25).
	Regression bool `yaml:"regression"`
	// OriginalURL is the issue this one repeats, when Regression is set.
	OriginalURL string `yaml:"original_url"`
	// Notified says `update` has already offered to retry this one (D1735).
	Notified bool `yaml:"notified"`

	// Summary, Diagnosis, Steps and Logs are the body a person reads; they
	// follow the header's fence rather than sitting in it.
	Summary   string   `yaml:"-"`
	Diagnosis string   `yaml:"-"`
	Steps     string   `yaml:"-"`
	Logs      []string `yaml:"-"`
}

// FileName is the name the issue's file has: `<NNNN>-<stem>.md` once it has
// a number and `local-<stem>.md` before (D1723). It validates the record
// first, so a name is never built from a stem that could be a path (F20).
func (i Issue) FileName() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	if i.Number == 0 {
		return i.LocalID + ".md", nil
	}
	return fmt.Sprintf("%04d-%s.md", i.Number, i.Run), nil
}

// Validate refuses a record this package would not write: a version it does
// not read, a run stem that is not D1609's shape, a number outside the field
// the file name prints, a class or a status it does not know, and a header
// value that spans lines — which would break the fence the body follows.
func (i Issue) Validate() error {
	if i.Version != 0 && i.Version != RecordVersion {
		return fmt.Errorf("issues: the record is version %d, and this fugaro writes %d", i.Version, RecordVersion)
	}
	if err := validateRun(i.Run); err != nil {
		return err
	}
	if i.LocalID != localPrefix+i.Run {
		return fmt.Errorf("issues: local ID %q does not name the run %q", i.LocalID, i.Run)
	}
	if i.Number != 0 {
		if err := validateNumber(i.Number); err != nil {
			return err
		}
	}
	if i.Uploaded && i.Number == 0 {
		return errors.New("issues: an uploaded issue has a number")
	}
	if !i.Class.Valid() {
		return fmt.Errorf("issues: %q is not a class, want %q or %q", i.Class, ClassFugaro, ClassUnclear)
	}
	if !i.Status.Valid() {
		return fmt.Errorf("issues: %q is not a status, want %q, %q or %q", i.Status, StatusPending, StatusSubmitted, StatusDismissed)
	}
	if i.Created.IsZero() {
		return errors.New("issues: the record has no creation time")
	}
	for _, f := range []struct{ name, value string }{
		{"reporter", i.Reporter},
		{"failed command", i.FailedCommand},
		{"fugaro version", i.FugaroVersion},
		{"os", i.OS},
		{"url", i.URL},
		{"fingerprint", i.Fingerprint},
		{"resolved_in", i.ResolvedIn},
		{"original url", i.OriginalURL},
	} {
		if strings.ContainsAny(f.value, "\r\n") {
			return fmt.Errorf("issues: the record's %s spans lines, which its header cannot hold", f.name)
		}
	}
	return nil
}

// Marshal renders the record: its header, then the body's four sections in
// the order this file writes them.
func Marshal(i Issue) ([]byte, error) {
	i.Version = RecordVersion
	if err := i.Validate(); err != nil {
		return nil, err
	}
	head, err := yaml.Marshal(i)
	if err != nil {
		return nil, fmt.Errorf("issues: %w", err)
	}
	var b strings.Builder
	b.WriteString(headerFence + "\n")
	b.Write(head)
	b.WriteString(headerFence + "\n")
	writeTextSection(&b, "Summary", i.Summary)
	writeTextSection(&b, "Diagnosis", i.Diagnosis)
	writeTextSection(&b, "Steps", i.Steps)
	b.WriteString("## Log\n\n")
	for _, excerpt := range i.Logs {
		writeFenced(&b, excerpt)
	}
	return []byte(b.String()), nil
}

// Unmarshal parses a record and refuses anything outside the format: a
// header field or a class this binary does not know, a record version it does
// not read, and a body whose sections are not the four, in order.
func Unmarshal(data []byte) (Issue, error) {
	head, body, err := splitRecord(data)
	if err != nil {
		return Issue{}, err
	}
	var i Issue
	dec := yaml.NewDecoder(bytes.NewReader(head))
	dec.KnownFields(true)
	if err := dec.Decode(&i); err != nil {
		return Issue{}, fmt.Errorf("issues: the record's header: %w", err)
	}
	// The header has to be the only document in it: YAML's `...` ends one, and
	// a field written after it would otherwise be dropped without a word, which
	// is the opposite of reading strictly (F23).
	var rest any
	if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
		return Issue{}, errors.New("issues: the record's header carries more than one document")
	}
	if i.Version != RecordVersion {
		return Issue{}, fmt.Errorf("issues: the record is version %d, and this fugaro reads %d", i.Version, RecordVersion)
	}
	if i.Summary, i.Diagnosis, i.Steps, i.Logs, err = parseBody(body); err != nil {
		return Issue{}, err
	}
	if err := i.Validate(); err != nil {
		return Issue{}, err
	}
	return i, nil
}

// splitRecord cuts a record at the fence that ends its header.
func splitRecord(data []byte) (head, body []byte, err error) {
	rest, ok := bytes.CutPrefix(data, []byte(headerFence+"\n"))
	if !ok {
		return nil, nil, fmt.Errorf("issues: the record does not start with %q", headerFence)
	}
	at := bytes.Index(rest, []byte("\n"+headerFence+"\n"))
	if at < 0 {
		return nil, nil, errors.New("issues: the record's header is never closed")
	}
	head = rest[:at]
	if len(bytes.TrimSpace(head)) == 0 {
		return nil, nil, errors.New("issues: the record has no header")
	}
	return head, rest[at+len("\n"+headerFence+"\n"):], nil
}

// bodySections are the record's body, in the order Marshal writes them.
var bodySections = []string{"Summary", "Diagnosis", "Steps", "Log"}

// parseBody reads the body's four sections. A section this format does not
// define, a missing one, a repeat and a section out of order are all errors:
// the body is a shape this package writes, and a file that is not that shape
// is not one to guess at.
//
// A text section holds exactly one block, which is what makes the body total:
// the text is the agent's own words, so it can carry a line that reads like a
// heading or like a fence, and only a block of its own keeps it whole.
func parseBody(body []byte) (summary, diagnosis, steps string, logs []string, err error) {
	sections, err := splitSections(string(body))
	if err != nil {
		return "", "", "", nil, err
	}
	if len(sections) != len(bodySections) {
		return "", "", "", nil, fmt.Errorf("issues: the record has %d sections, want %d (%s)", len(sections), len(bodySections), strings.Join(bodySections, ", "))
	}
	for idx, s := range sections {
		if s.name != bodySections[idx] {
			return "", "", "", nil, fmt.Errorf("issues: the record's section %d is %q, want %q", idx+1, s.name, bodySections[idx])
		}
	}
	texts := make([]string, len(bodySections)-1)
	for idx, name := range bodySections[:len(bodySections)-1] {
		blocks, err := parseExcerpts(sections[idx].lines)
		if err != nil {
			return "", "", "", nil, err
		}
		if len(blocks) != 1 {
			return "", "", "", nil, fmt.Errorf("issues: the record's %s section holds %d blocks, want 1", name, len(blocks))
		}
		texts[idx] = blocks[0]
	}
	logs, err = parseExcerpts(sections[len(bodySections)-1].lines)
	if err != nil {
		return "", "", "", nil, err
	}
	return texts[0], texts[1], texts[2], logs, nil
}

// bodySection is one `## <name>` heading and the lines under it.
type bodySection struct {
	name  string
	lines []string
}

// splitSections cuts the body at its `## ` headings, stepping over the fences
// so a section's own text cannot look like a heading.
func splitSections(body string) ([]bodySection, error) {
	var sections []bodySection
	var open string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case isFence(line) && open == "":
			open = line
		case isFence(line) && len(line) >= len(open):
			open = ""
		}
		if open == "" && strings.HasPrefix(line, "## ") {
			sections = append(sections, bodySection{name: strings.TrimPrefix(line, "## ")})
			continue
		}
		if len(sections) == 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("issues: %q stands outside the record's sections", line)
		}
		last := &sections[len(sections)-1]
		last.lines = append(last.lines, line)
	}
	return sections, nil
}

// parseExcerpts reads one section's blocks: the text of a section is one
// block, the log's excerpts are one each, in the order they were written.
func parseExcerpts(lines []string) ([]string, error) {
	var out []string
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		open := lines[i]
		if !isFence(open) {
			return nil, fmt.Errorf("issues: %q stands outside a section's block", lines[i])
		}
		i++
		start := i
		for i < len(lines) && !closesFence(lines[i], open) {
			i++
		}
		if i == len(lines) {
			return nil, errors.New("issues: a block's fence is never closed")
		}
		out = append(out, strings.Join(lines[start:i], "\n"))
		i++
	}
	return out, nil
}

// writeTextSection writes one text section: its heading, then the text in a
// fence of its own.
func writeTextSection(b *strings.Builder, name, text string) {
	b.WriteString("## " + name + "\n\n")
	writeFenced(b, text)
}

// writeFenced writes one block of free text — a section's text, a log excerpt —
// in a fence longer than any run of backticks in it, so the text cannot end its
// own block.
func writeFenced(b *strings.Builder, text string) {
	fence := strings.Repeat("`", max(3, longestBacktickRun(text)+1))
	b.WriteString(fence + "\n")
	b.WriteString(text)
	// Always, even when the text already ends with one: the closing fence needs
	// a line of its own, and the reader takes back exactly the newline written
	// here by joining the lines it read, which is what tells a text that ends
	// with a newline from one that does not.
	b.WriteString("\n")
	b.WriteString(fence + "\n\n")
}

// isFence reports whether a line is a fence: three backticks or more and
// nothing else.
func isFence(line string) bool {
	return len(line) >= 3 && strings.Trim(line, "`") == ""
}

// closesFence reports whether line closes the fence open opened: as many
// backticks or more, which is CommonMark's rule and what lets an excerpt
// carry a shorter fence of its own.
func closesFence(line, open string) bool {
	return isFence(line) && len(line) >= len(open)
}

// longestBacktickRun is the longest run of backticks in s.
func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}

// runCommandRE is what a command name may be in a run stem: the shape
// internal/report gives a log's name (D1609), and nothing that could be a
// path.
var runCommandRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// numberMax is the largest number a record may carry: the file name prints it
// in four digits and the store sorts by them.
const numberMax = 999999

// validateRun refuses a run stem that is not D1609's
// `<command>-<14-digit UTC>-<pid>`, so a stem from the shared store cannot
// become a path (F20). The command may carry dashes, so the stem is split
// from its right end.
func validateRun(stem string) error {
	rest, pid, ok := cutLast(stem, '-')
	if !ok || !digits(pid) || len(pid) > 7 {
		return notARunStem(stem)
	}
	command, stamp, ok := cutLast(rest, '-')
	if !ok || len(stamp) != 14 || !digits(stamp) || !runCommandRE.MatchString(command) {
		return notARunStem(stem)
	}
	return nil
}

// notARunStem is the one refusal for every way a stem can be wrong: it names
// the shape the caller has to produce.
func notARunStem(stem string) error {
	return fmt.Errorf("issues: %q is not a run stem (`<command>-<14-digit UTC>-<pid>`)", stem)
}

// validateNumber refuses a number the file name cannot hold.
func validateNumber(n int) error {
	if n < 1 || n > numberMax {
		return fmt.Errorf("issues: %d is not an issue number (1 to %d)", n, numberMax)
	}
	return nil
}

// parseRef reads an issue reference — a number, or the local ID
// `local-<stem>` (F23) — and validates its shape before anything builds a
// path from it (F20).
func parseRef(ref string) (number int, localID string, err error) {
	if digits(ref) {
		n, err := strconv.Atoi(ref)
		if err != nil || validateNumber(n) != nil {
			return 0, "", fmt.Errorf("issues: %q is not an issue number", ref)
		}
		return n, "", nil
	}
	if stem, ok := strings.CutPrefix(ref, localPrefix); ok {
		if err := validateRun(stem); err != nil {
			return 0, "", err
		}
		return 0, localPrefix + stem, nil
	}
	return 0, "", fmt.Errorf("issues: %q is neither an issue number nor a local ID", ref)
}

// cutLast cuts s at its last instance of sep.
func cutLast(s string, sep byte) (before, after string, ok bool) {
	at := strings.LastIndexByte(s, sep)
	if at < 0 {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}

// digits reports whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// redactedValue is what a value the header must not keep is replaced with.
// It is the marker internal/redact substitutes, so a reader sees one word for
// "something was here".
const redactedValue = "[REDACTED]"

// commandValueRE is what makes a token a value rather than a word: a path or
// a home directory, a URL, a mark an identifier carries (`.`, `_`, `@`, `:`,
// `=`), or a digit. `update`, `--repo` and `my-task` are none of these and
// survive, which is what keeps the stored command readable.
var commandValueRE = regexp.MustCompile(`[/\\~]|://|[._@:=]|[0-9]`)

// RedactCommand is the failed command as the record's header keeps it
// (D1732): the command name and its flags, with every value that could be a
// path, a URL or an identifier replaced. The arguments of a failing command
// are where a project name, a path or a credential ends up, and the header is
// the one place the whole team can read it from.
//
// The result is one line: `fugaro update` prints it (never runs it, F26) and
// a header value cannot span lines.
func RedactCommand(argv []string) string {
	out := make([]string, 0, len(argv))
	for idx, arg := range argv {
		if idx > 0 && strings.HasPrefix(arg, "-") {
			name, value, ok := strings.Cut(arg, "=")
			if !ok {
				out = append(out, oneLine(arg))
				continue
			}
			out = append(out, oneLine(name)+"="+redactValue(value))
			continue
		}
		// The command word goes through the same rule: a user who ran the
		// binary by path has that path — their home directory and their user
		// name — in os.Args[0] (the rule internal/cli already applies to
		// os.Args[0]), and this header is the one place the whole team reads.
		// Run by name, `fugaro` survives.
		out = append(out, redactValue(arg))
	}
	return redact.Text(strings.Join(out, " "), nil)
}

// redactValue replaces a value the header must not keep.
func redactValue(value string) string {
	value = oneLine(value)
	if value == "" || !commandValueRE.MatchString(value) {
		return value
	}
	return redactedValue
}

// oneLine collapses the whitespace a single-line header field cannot carry.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
