package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// TestPostStripsIdentity: the post leaves this machine for a public
// repository, so the parts of the record that say whose machine it was are not
// in it (D1722, F4). The record it is built from carries every one of them, so
// a builder that copied the header rather than projecting it fails here.
//
// The rows are not all the same defence: the header's own values — the run
// stem, the local ID, the installation, the URLs, the fingerprint, the failed
// command — are absent because the builder never reads them, which is what
// makes a field added to the record later unable to start leaving the machine.
// The reporter's address is absent twice over, because the gate would remove
// an address from any text it was in (F5); a mutation that copies the header
// into the body is therefore caught by this test on the rows the gate cannot
// save, and the reporter's row is what pins the second half of the claim.
func TestPostStripsIdentity(t *testing.T) {
	i := issueSample()
	i.Summary = "the Acme-Widgets launcher failed: the update stops half way"
	p := BuildPost(i, []string{"Acme-Widgets", " acme "})
	post := p.Title + "\n" + p.Body

	for _, f := range []struct{ name, value string }{
		{"the reporter", i.Reporter},
		{"the run stem", i.Run},
		{"the local ID", i.LocalID},
		{"the failed command", i.FailedCommand},
		{"the installation", i.Installation},
		{"the issue URL", i.URL},
		{"the original issue URL", i.OriginalURL},
		{"the fingerprint", i.Fingerprint},
	} {
		if strings.Contains(post, f.value) {
			t.Errorf("the post carries %s (%q):\n%s", f.name, f.value, post)
		}
	}
	for _, name := range []string{"acme-widgets", "acme"} {
		if strings.Contains(strings.ToLower(post), name) {
			t.Errorf("the post names the project (%q):\n%s", name, post)
		}
	}

	for _, want := range []struct{ what, value string }{
		{"the version", i.FugaroVersion},
		{"the platform", i.OS},
		{"the class", string(i.Class)},
		{"the summary", "the update stops half way"},
		{"the diagnosis", "the provider cache is stale"},
		{"the steps", "run fugaro update"},
		{"the log", "line two"},
	} {
		if !strings.Contains(post, want.value) {
			t.Errorf("the post does not carry %s (%q):\n%s", want.what, want.value, post)
		}
	}

	// The reference is the issue's name in the fallback link, never a path on
	// this machine (F16, D1722).
	if p.Ref != "7" {
		t.Errorf("Ref = %q, want the issue's number", p.Ref)
	}
	if got := BuildPost(issueLocal(), nil).Ref; got != i.LocalID {
		t.Errorf("Ref = %q for an issue with no number, want its local ID %q", got, i.LocalID)
	}

	// The preview shows the post's own bytes: a person reads what would be
	// filed (F19).
	if preview := Preview(p); !strings.Contains(preview, p.Body) {
		t.Errorf("Preview does not show the post's body:\n%s", preview)
	}
}

// TestPostExcludesFailedCommand: the command whose failure produced the issue
// is the one header field that is a person's own line — a path, a host, a flag
// value, whatever they typed — so it stays on the machine (D1722, F4). The post
// is a projection built field by field, and this pins every form the command
// could leave in: the title, the body, the preview a person reads, and the
// fallback link, which carries the post's bytes into a browser.
func TestPostExcludesFailedCommand(t *testing.T) {
	i := issueSample()
	i.FailedCommand = "fugaro run --repo acme/widget --note db-7.internal"
	record, err := Marshal(i)
	if err != nil {
		t.Fatalf("marshalling the record: %v", err)
	}
	if !strings.Contains(string(record), i.FailedCommand) {
		t.Fatalf("the record does not carry the failed command, so this test pins nothing:\n%s", record)
	}
	p := BuildPost(i, nil)
	for _, doc := range []struct{ what, text string }{
		{"the title", p.Title},
		{"the body", p.Body},
		{"the preview", Preview(p)},
		{"the fallback link", NewIssueURL(p)},
	} {
		if strings.Contains(doc.text, i.FailedCommand) {
			t.Errorf("%s carries the failed command:\n%s", doc.what, doc.text)
		}
	}
	// The words of it, too, and not only the whole line: a builder that copied
	// the command into the body and cut it would be caught here, and the link is
	// where a body that did carry them would show them.
	for _, part := range []string{"acme/widget", "db-7.internal"} {
		if strings.Contains(p.Title+p.Body, part) || strings.Contains(NewIssueURL(p), url.QueryEscape(part)) {
			t.Errorf("the post carries %q, which only the record's header holds:\n%s", part, p.Title+p.Body)
		}
	}
}

// TestPostReRedacts: the text of an issue is redacted on its way into the
// public post as well, whoever wrote it and whatever the record already lost
// (F5). The rows are the sample secrets built from fragments, planted in every
// part of the record the post projects.
func TestPostReRedacts(t *testing.T) {
	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			i := issueSample()
			i.Class = ClassUnclear
			i.Summary = "the command failed: " + tc.in
			i.Diagnosis = "the cause is " + tc.in
			i.Steps = "1. run it again\n2. " + tc.in
			i.Logs = []string{"a line before\n" + tc.in + "\na line after"}
			p := BuildPost(i, []string{"acme-widgets"})
			if strings.Contains(p.Title+"\n"+p.Body, tc.leak) {
				t.Errorf("the post carries %q:\n%s", tc.leak, p.Body)
			}
			if !strings.Contains(p.Body, redactedValue) {
				t.Errorf("the post of %q carries nothing redacted:\n%s", tc.in, p.Body)
			}
		})
	}

	// A name the caller lists goes as well, in a part of the record the gate
	// alone would keep: Scrub is what the post adds to the gate (F4).
	i := issueSample()
	i.Summary = "the Acme-Widgets launcher failed"
	i.Logs = []string{"sending to https://github.com/acme-widgets/acme-widgets"}
	p := BuildPost(i, []string{"acme-widgets"})
	if strings.Contains(strings.ToLower(p.Title+"\n"+p.Body), "acme-widgets") {
		t.Errorf("the post names the repository:\n%s", p.Body)
	}
	if !strings.Contains(p.Body, "launcher failed") {
		t.Errorf("the post ate the finding as well:\n%s", p.Body)
	}
}

// straddling is one log whose last line is longer than the log budget and holds
// value so the cut lands into bytes inside it. head is whatever has to stand in
// front of value for a rule to know it is a value at all. The line has no
// newline after the cut — a transcript's own long line is one line — so the cut
// has nowhere to retreat to and where it lands is where the tail begins.
func straddling(head, value string, into int) string {
	return strings.Repeat("a line that is not the last one\n", 40) +
		strings.Repeat("x", 500) + head + value +
		strings.Repeat("x", postLogBudget-len(value)+into)
}

// assertNoFragment: no part of value long enough to be read as part of it
// survived in body. The whole of it is the first check; the sliding window is
// what catches the tail of it, which is the shape a cut taken before the passes
// leaves behind: text no rule recognises as the value it came from, and still
// the end of it in a public post.
func assertNoFragment(t *testing.T, body, value string, into int) {
	t.Helper()
	if strings.Contains(body, value) {
		t.Fatalf("the post carries %q", value)
	}
	if frag := value[into:]; strings.Contains(body, frag) {
		t.Errorf("the tail kept %q, the end of %q: the cut ran before the passes, so no rule saw the whole value", frag, value)
	}
	for n := 6; n < len(value); n++ {
		for i := 0; i+n <= len(value); i++ {
			if strings.Contains(body, value[i:i+n]) {
				t.Errorf("the tail kept %q of %q", value[i:i+n], value)
			}
		}
	}
}

// TestPostLogScrubsBeforeTheCut: the gate and the names run *before* the tail
// is taken, and this is the case that tells the two orders apart (F5). Both
// passes match a whole value, so a value the cut fell in the middle of would be
// text no rule recognises and the half left standing is still half of it — half
// a project name, or half a credential, in a public post.
//
// The whole-repository tests pass either way: what pins the order is the
// fragment, which only exists once the cut has happened. The log's last line is
// therefore long and unbroken, and the value sits at the cut.
func TestPostLogScrubsBeforeTheCut(t *testing.T) {
	t.Run("a project name", func(t *testing.T) {
		const name = "acme-widgets-prod"
		i := issueSample()
		i.Logs = []string{straddling("sending to https://github.com/acme/", name, 9)}
		assertNoFragment(t, BuildPost(i, []string{name}).Body, name, 9)
	})

	t.Run("a credential", func(t *testing.T) {
		secret := "ghp_" + strings.Repeat("A1b2C3d4", 5)
		i := issueSample()
		i.Logs = []string{straddling("remote: ", secret, 12)}
		assertNoFragment(t, BuildPost(i, nil).Body, secret, 12)
	})
}

// TestPostLogKeepsTheTailOfTheLog: the excerpt's cut advances to the next line
// boundary so it does not begin with half a line, but that boundary is worth
// taking only while it keeps half the budget, the rule cutForURL follows for
// the fallback body. A log whose last line is longer than the whole budget has
// no boundary that leaves anything: the only newline in reach is the one at
// the end of the text, and advancing to it empties the tail. postBody writes
// the section only when the tail is non-empty, so a multi-kilobyte log would
// leave the post with no excerpt and no cut notice — the log the issue was
// filed for, gone from the issue without a word.
func TestPostLogKeepsTheTailOfTheLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  string
	}{
		{"one line longer than the whole budget", strings.Repeat("x", 3000) + "\n"},
		{"a boundary that would leave 47 bytes", strings.Repeat("x", 2953) + "\n" + strings.Repeat("y", 47)},
		// A last line of characters wider than a byte, which is where the cut is
		// a byte cut in the middle of one: the tail begins at the character the
		// cut fell in, not at its second byte.
		{"a line of characters wider than a byte", strings.Repeat("ok\n", 20) + strings.Repeat("→", 3000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail, cut := postLog([]string{tc.log}, nil)
			if !cut {
				t.Fatalf("postLog of a %d byte log is not cut", len(tc.log))
			}
			if !strings.HasSuffix(tc.log, tail) {
				t.Errorf("the excerpt is not the end of the log: %q", tail)
			}
			if !utf8.ValidString(tail) {
				t.Errorf("the excerpt is not valid UTF-8: the cut landed inside a character:\n%q", tail[:60])
			}
			if len(tail) < postLogBudget/2 {
				t.Errorf("the excerpt kept %d of the %d byte budget: most of the log given up for a line boundary",
					len(tail), postLogBudget)
			}

			// And what the person filing it sees: the section, and the notice
			// that says the log was cut.
			i := issueSample()
			i.Logs = []string{tc.log}
			body := BuildPost(i, nil).Body
			for _, want := range []string{"## Log", postLogCutNotice} {
				if !strings.Contains(body, want) {
					t.Errorf("the post does not carry %q:\n%s", want, body)
				}
			}
		})
	}
}

// TestPostHashCoversTheBody: the hash is of the bytes that would be filed, so
// two submitters can agree on which record they are filing and `submit --ask`
// can refuse a record that changed between the preview and the yes (F19). It
// is computed here the way a reader outside this binary recomputes it — from
// the title and the body it was given — because a hash nobody else can
// reproduce pins nothing.
func TestPostHashCoversTheBody(t *testing.T) {
	p := BuildPost(issueSample(), nil)
	sum := sha256.Sum256([]byte(p.Title + "\n\n" + p.Body))
	if got := p.Hash(); got != hex.EncodeToString(sum[:]) {
		t.Errorf("Hash() = %q, want the SHA-256 of the post's bytes (%q)", got, hex.EncodeToString(sum[:]))
	}

	other := p
	other.Body += "\nand one more line"
	if other.Hash() == p.Hash() {
		t.Error("the hash does not cover the body: two different bodies hash alike")
	}

	title := p
	title.Title += " again"
	if title.Hash() == p.Hash() {
		t.Error("the hash does not cover the title: two different titles hash alike")
	}
}

// TestNewIssueURLCutsLongBody: the fallback link carries the post in its query
// (spec 5), and a URL has a length a browser refuses, so a body past the
// budget is cut and points at the command that prints the rest (F16).
func TestNewIssueURLCutsLongBody(t *testing.T) {
	prefix := "https://github.com/" + updatecheck.Repo + "/issues/new?"

	t.Run("a body inside the budget is the whole body", func(t *testing.T) {
		p := BuildPost(issueLocal(), nil)
		link := NewIssueURL(p)
		if !strings.HasPrefix(link, prefix) {
			t.Fatalf("link = %q, want it to start with %q", link, prefix)
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("parsing %q: %v", link, err)
		}
		q := u.Query()
		if got := q.Get("title"); got != p.Title {
			t.Errorf("title = %q, want %q", got, p.Title)
		}
		if got := q.Get("body"); got != p.Body {
			t.Errorf("body = %q, want the whole post:\n%s", got, p.Body)
		}
		if strings.Contains(q.Get("body"), newIssueURLCutNotice) {
			t.Error("a body that was not cut points at the rest of itself")
		}
	})

	// linkBody is the fallback link's body as a reader's browser would send it,
	// with the assertions every cut shares: inside the budget, and ending in the
	// command that prints the rest.
	linkBody := func(t *testing.T, p Post) string {
		t.Helper()
		link := NewIssueURL(p)
		if !strings.HasPrefix(link, prefix) {
			t.Fatalf("link = %q, want it to start with %q", link, prefix)
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("parsing %q: %v", link, err)
		}
		body := u.Query().Get("body")
		if len(body) >= len(p.Body) {
			t.Errorf("the body was not cut: %d bytes of a %d byte post", len(body), len(p.Body))
		}
		if got := url.QueryEscape(body); len(got) > newIssueURLBudget {
			t.Errorf("the encoded body is %d bytes, past the %d byte budget", len(got), newIssueURLBudget)
		}
		if !strings.HasSuffix(body, newIssueURLCutNotice) {
			t.Errorf("the cut body does not end with the command that prints the rest:\n%q", body)
		}
		return body
	}

	t.Run("a long body of lines is cut at a line boundary", func(t *testing.T) {
		i := issueLocal()
		i.Summary = strings.Repeat("the update stops half way\n", 1000)
		p := BuildPost(i, nil)
		body := linkBody(t, p)
		cut := strings.TrimSuffix(body, newIssueURLCutNotice)
		if cut == "" {
			t.Fatal("the link carries nothing of the post")
		}
		if !strings.HasPrefix(p.Body, cut) {
			t.Errorf("the link's body is not the post's own text:\n%q", cut)
		}
		if p.Body[len(cut)] != '\n' {
			t.Errorf("the cut is not at a line boundary: the post goes on with %q", p.Body[len(cut):][:20])
		}
		// A path would name this machine's user in a public post, and would be
		// of no use to the reader anyway (D1722). The reference a body with no
		// number is named by is the local ID, and that carries no separator:
		// the run stem it is built from is validated by shape before any path
		// is built from it (F20).
		if strings.ContainsAny(i.LocalID, `/\`) {
			t.Errorf("the local ID %q could be read as a path", i.LocalID)
		}
		for _, path := range []string{"/home/", "/Users/", `c:\users\`} {
			if strings.Contains(body, path) {
				t.Errorf("the link carries the local path %q", path)
			}
		}
	})

	// One line longer than the whole allowance, which is the shape a paragraph
	// summary or a pasted URL has: there is no line boundary to retreat to
	// before the budget runs out. The cut stays mid-line, because the other
	// answer — the last boundary before it — hands the person a link with the
	// header and the pointer in it and none of the finding, which is the one
	// thing the fallback exists to carry.
	t.Run("a body of one long line still carries the finding", func(t *testing.T) {
		i := issueLocal()
		i.Summary = strings.Repeat("the update stops half way. ", 2000)
		p := BuildPost(i, nil)
		body := linkBody(t, p)
		if got := len(url.QueryEscape(body)); got < newIssueURLBudget/2 {
			t.Errorf("the link carries %d of the %d byte allowance, most of it given up for a line boundary:\n%q",
				got, newIssueURLBudget, body)
		}
		if !strings.Contains(body, "the update stops half way. the update") {
			t.Errorf("the link does not carry the finding:\n%q", body)
		}
	})

	// The cut is a byte cut and the body is not bytes: a cut inside a multi-byte
	// character keeps the first bytes of it and drops the rest, and those bytes
	// are what the browser decodes and GitHub writes into a public issue — a
	// sequence that is not a text. Roughly half the cuts of a body that is not
	// ASCII land inside a character, so this row is the difference between a
	// link that carries the post and one that carries a broken byte sequence.
	t.Run("the cut lands on a character, never inside one", func(t *testing.T) {
		for _, unit := range []string{"é", "→", "𝄞"} {
			i := issueLocal()
			i.Summary = strings.Repeat(unit, 4000)
			p := BuildPost(i, nil)
			if !utf8.ValidString(p.Body) {
				t.Fatalf("the post of %q is not valid UTF-8, so the cut is not what broke it", unit)
			}
			if body := linkBody(t, p); !utf8.ValidString(body) {
				cut := strings.TrimSuffix(body, newIssueURLCutNotice)
				t.Errorf("the link's body of %q is not valid UTF-8: the cut landed inside a character:\n%q",
					unit, cut[max(0, len(cut)-30):])
			}
		}
	})

	// The link is public bytes: whoever opens it reads the body, and the person
	// who clicks submit files it. So the reference the fallback prints on the
	// terminal — the number, or the local ID an unnumbered issue is named by —
	// cannot be in it: the local ID is built from the run's stem and stamp, and
	// `TestPostStripsIdentity` counts that as identity the post must not carry
	// (D1722). Both cases are here, because the numbered one is the one it is
	// tempting to put back.
	t.Run("the link carries no identity", func(t *testing.T) {
		for _, i := range []Issue{issueLocal(), issueSample()} {
			p := BuildPost(i, nil)
			if p.Ref == "" {
				t.Fatal("no reference to keep out of the link")
			}
			// A cut body is where the reference used to go, so the post is made
			// long enough to be cut.
			long := i
			long.Summary = strings.Repeat("the update stops half way. ", 2000)
			body := linkBody(t, BuildPost(long, nil))
			for _, f := range []struct{ name, value string }{
				{"the run stem", i.Run},
				{"the local ID", i.LocalID},
			} {
				if strings.Contains(body, f.value) {
					t.Errorf("the link carries %s (%q):\n%q", f.name, f.value, body)
				}
			}
			for _, path := range []string{"/home/", "/Users/", `c:\users\`} {
				if strings.Contains(body, path) {
					t.Errorf("the link carries the local path %q", path)
				}
			}
		}
	})
}

// TestInstallHintByPlatform: a machine without gh is told how to get it, in
// the words of its own platform and distribution, and is sent to the install
// page when this file does not know the distribution (F15).
func TestInstallHintByPlatform(t *testing.T) {
	for _, tc := range []struct {
		name      string
		goos      string
		osRelease string
		want      string
	}{
		{"macOS", "darwin", "", "brew install gh"},
		{"Windows", "windows", "", "winget install GitHub.cli"},
		{"Debian", "linux", "ID=debian\nID_LIKE=debian\n", "sudo apt install gh"},
		{"Ubuntu", "linux", "ID=ubuntu\nID_LIKE=debian\n", "sudo apt install gh"},
		{"a derivative that names its family", "linux", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", "sudo apt install gh"},
		{"Fedora", "linux", "ID=fedora\n", "sudo dnf install gh"},
		{"RHEL", "linux", "ID=\"rhel\"\nID_LIKE=\"fedora\"\n", "sudo dnf install gh"},
		{"a distribution this file does not know", "linux", "ID=alpine\n", GHInstallPage},
		{"a linux with no os-release", "linux", "", GHInstallPage},
		{"another platform entirely", "freebsd", "", GHInstallPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InstallHint(tc.goos, tc.osRelease); got != tc.want {
				t.Errorf("InstallHint(%q, %q) = %q, want %q", tc.goos, tc.osRelease, got, tc.want)
			}
		})
	}
}
