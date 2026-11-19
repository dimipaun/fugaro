// Approving an issue item, with the issue's body on the screen (G4, D2741).
//
// A work item the daemon's issue ingest wrote carries the issue's id and a
// SHA-256 of its body, and never the body itself (D2720, X1): that is what
// keeps a flood of issues from filling a node, and it means the spec a person
// approves is a title, a repository and a hash. A person approving such an
// item would then be agreeing to a brief they never read — so the approval
// reads the issue back through the person's own `gh`, read-only, prints its
// title and body fenced as data with the body's hash, and refuses when that
// hash is not the one the item pins: what would be approved is then not what
// the issue says, and the item is re-synced before it can be (D2741).
//
// The read is a read. `gh issue view` is the whole of what this file spawns,
// nothing here writes to GitHub, and the person's own login is what answers:
// Fugaro holds no credential for this and passes none.
//
// The hash rule lives here, in one sentence, because a hash of text read from
// outside is only equal to another hash of the same bytes: an item's
// `body_sha256` is the SHA-256 of the issue's body exactly as the host answers
// it — UTF-8, nothing trimmed, nothing folded, lower-case hex and no `sha256:`
// prefix (orchestrator.WorkItem.BodySHA256). The ingest that writes the field
// (B4) and the launch that reads the body back (B9) hash the same bytes; a rule
// spelled in two places instead would approve a body no run is briefed with.

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// approveIssueGH is the `gh` an approval reads an issue with, and it is a
// variable for the reason every other spawn in this tree is: a test puts a fake
// in front of it on PATH and reads the argv back.
var approveIssueGH = "gh"

const (
	// approveIssueTimeout bounds one read. It is the person's own command and
	// they are waiting on it, so it is not given the three seconds the watch
	// loop gives a row it draws: a slow link has room to answer, and a gh that
	// has hung ends in a refusal rather than on a terminal that never returns.
	approveIssueTimeout = 20 * time.Second
	// approveIssueGHRace is how long a `gh` this command spawned is given to
	// leave after its context ends before its process group is killed: gh
	// starts credential helpers and pagers, and killing only the process that
	// was started would leave them holding the pipes.
	approveIssueGHRace = 5 * time.Second
	// approveIssueMaxBytes bounds what one read keeps. GitHub's own ceiling for
	// an issue body is 64 KiB of characters, which JSON escaping can double, so
	// this holds every issue the host accepts and still keeps a runaway answer
	// from growing this process.
	approveIssueMaxBytes = 256 << 10
)

// issueReadDoc is what `gh issue view --json title,body` answers.
type issueReadDoc struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// The block's delimiters are fixed rather than nonced, like the state
// summary's, and for the same reason: the same issue always prints as the same
// text, and a test can pin it. Nothing inside the block can pass for one of
// them, because the fence's own words are taken out of the text
// (issueFenceLine).
const (
	issueFenceOpen  = "<<<fugaro-issue>>>"
	issueFenceClose = "<<<end-fugaro-issue>>>"
)

var issueFenceRE = regexp.MustCompile(`(?i)(end-)?fugaro-issue`)

const issueFenceRemoved = "[fugaro-delimiter removed]"

// issueExternalRE is an item's external id read back for the two things a value
// handed to a subprocess must not carry. The state's own externalRE already
// admitted the shape when the node was decoded, and this is stricter in one
// place only — the `..` of a name that looks like a path — because the
// repository here is an argument of a command this process spawns.
var issueExternalRE = regexp.MustCompile(`\Agithub:([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*)#([1-9][0-9]{0,18})\z`)

// parseExternalIssue splits an external id into the repository the issue lives
// in and its number. ok is false for anything that is not the
// `github:<owner>/<repo>#<n>` shape the state admits, and for a name carrying
// `..`, which is never handed to gh.
func parseExternalIssue(external string) (repo, number string, ok bool) {
	m := issueExternalRE.FindStringSubmatch(external)
	if m == nil {
		return "", "", false
	}
	repo = m[1] + "/" + m[2]
	if strings.Contains(m[1], "..") || strings.Contains(m[2], "..") {
		return "", "", false
	}
	return repo, m[3], true
}

// issueBodySHA256 is the one hash rule this file documents: the SHA-256 of the
// issue's body as the host answered it, lower-case hex and unprefixed.
func issueBodySHA256(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// approvedIssues prints every issue item this patch approves, in the order the
// patch lists them, and answers "" when it approves none.
//
// It is asked once, after the fence on the person's ops has answered and before
// the typed confirmation: a run with no terminal, a --json run and a coding
// agent's session spawn nothing at all, and the text that lands here reaches
// the person's screen before they type.
//
// Every item the approval ops name is read, an item Apply would skip — a digest
// the node has since moved past — included: the list is what the person is
// shown and confirms, so the body of every item on it belongs on the screen,
// and a refusal over one of them is answered by the same re-sync as the rest.
func approvedIssues(ctx context.Context, after orchestrator.State, p orchestrator.Patch) (string, error) {
	var b strings.Builder
	seen := make(map[string]bool, len(p.Ops))
	for _, op := range p.Ops {
		for _, id := range approvalItemIDs(op) {
			if seen[id] {
				continue
			}
			seen[id] = true
			w, ok := after.Item(id)
			if !ok || w.External == "" {
				continue
			}
			block, err := issueApprovalBlock(ctx, w)
			if err != nil {
				return "", err
			}
			b.WriteString(block)
		}
	}
	return b.String(), nil
}

// approvalItemIDs is the items an op approves: the one a single approval names,
// or the list a plan approval carries. Every other op approves nothing.
func approvalItemIDs(op orchestrator.Op) []string {
	switch op.Op {
	case orchestrator.OpWorkItemApprove:
		if op.Approve == nil {
			return nil
		}
		return []string{op.Approve.ID}
	case orchestrator.OpPlanApprove:
		if op.PlanApprove == nil {
			return nil
		}
		ids := make([]string, 0, len(op.PlanApprove.Items))
		for _, it := range op.PlanApprove.Items {
			ids = append(ids, it.ID)
		}
		return ids
	}
	return nil
}

// issueApprovalBlock is one issue item as the confirmation shows it, or the
// refusal that stands in its place. Nothing is written on any of these paths:
// the confirmation is before the write, so a refusal here leaves the node as it
// was.
func issueApprovalBlock(ctx context.Context, w orchestrator.WorkItem) (string, error) {
	repo, number, ok := parseExternalIssue(w.External)
	if !ok {
		return "", userErr("nothing was written: the item %s carries the external id %s, which is not the github:<owner>/<repo>#<n> id this release reads: re-sync the item from its issue, then approve it", safetext.Field(w.ID), safetext.Field(w.External))
	}
	read, err := readIssueForApproval(ctx, w.External, repo, number)
	if err != nil {
		return "", err
	}
	sum := issueBodySHA256(read.Body)
	switch {
	case w.BodySHA256 == "":
		return "", userErr("nothing was written: the item %s came from the issue %s and pins no body hash, so there is nothing the issue can be checked against: re-sync the item from the issue, then approve it", safetext.Field(w.ID), safetext.Field(w.External))
	case sum != w.BodySHA256:
		return "", userErr("nothing was written: the issue %s has changed since the item %s was ingested from it (its body hashes to %s now, and the item pins %s), so approving the item would approve what the issue no longer says: re-sync the item from the issue, then approve it",
			safetext.Field(w.External), safetext.Field(w.ID), sum, safetext.Field(w.BodySHA256))
	}
	return issueFenceText(w.External, read, sum), nil
}

// readIssueForApproval runs the one `gh` subcommand this file spawns and reads
// its answer. Everything that can go wrong is a refusal naming the fix, and
// none of them carries a word gh printed: what gh complained about is read only
// to recognise the case, never to print (the rule internal/watch's own lookup
// follows).
func readIssueForApproval(ctx context.Context, external, repo, number string) (issueReadDoc, error) {
	bin, err := exec.LookPath(approveIssueGH)
	if err != nil {
		return issueReadDoc{}, userErr("nothing was written: the issue %s is read with your own gh, and %s is not on this machine's PATH: install gh and log in to github.com (gh auth login), then approve the item", safetext.Field(external), approveIssueGH)
	}
	ctx, cancel := context.WithTimeout(ctx, approveIssueTimeout)
	defer cancel()
	out := &issueReadBounded{}
	code, err := procgroup.Run(ctx, procgroup.Cmd{
		Name: bin,
		// The host is written out, as the watch loop writes it: a GH_HOST
		// pointing at another host cannot move the question, because the
		// repository argument names github.com itself.
		Args:   []string{"issue", "view", number, "--repo", "github.com/" + repo, "--json", "title,body"},
		Stdout: out, Stderr: io.Discard,
		Grace: approveIssueGHRace,
	})
	if err != nil {
		return issueReadDoc{}, userErr("nothing was written: the issue %s could not be read with your own gh (%v): check that gh runs and is logged in to github.com (gh auth status), then approve the item", safetext.Field(external), err)
	}
	if code != 0 {
		return issueReadDoc{}, userErr("nothing was written: reading the issue %s with your own gh failed (`gh issue view` exited %d): check that gh is logged in to github.com (gh auth status) and that the repository is one it can read, then approve the item", safetext.Field(external), code)
	}
	if out.hidden() > 0 {
		return issueReadDoc{}, userErr("nothing was written: the issue %s is over %d bytes, which is more than this read takes: read it at https://github.com/%s/issues/%s and split the item if it is that large", safetext.Field(external), approveIssueMaxBytes, safetext.Field(repo), safetext.Field(number))
	}
	var doc issueReadDoc
	if err := json.Unmarshal(out.bytes(), &doc); err != nil {
		return issueReadDoc{}, userErr("nothing was written: gh's answer about the issue %s is not the JSON this release reads (gh issue view --json title,body): check that gh is current (gh version), then approve the item", safetext.Field(external))
	}
	return doc, nil
}

// issueReadBounded keeps the first approveIssueMaxBytes of what gh writes and
// counts, but drops, the rest: a read that cannot grow this process, and a
// write that never fails, so the child is never left blocked on a full pipe.
// Its mutex is procgroup's: a child that escaped the process group can still be
// writing here after Run has given up on it.
type issueReadBounded struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	dropped int
}

func (b *issueReadBounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := approveIssueMaxBytes - b.buf.Len()
	if room <= 0 {
		b.dropped += len(p)
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.dropped += len(p) - room
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *issueReadBounded) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func (b *issueReadBounded) hidden() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// issueFenceText is one issue as the confirmation shows it: the posture line,
// the issue's own title and body between the fence's delimiters, and the hash
// of the body the item pins. The hash is inside the block and next to the body
// it belongs to, so a person comparing what they read against what the item
// holds has both in one place.
func issueFenceText(external string, read issueReadDoc, sum string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The issue %s, as your own gh reads it now:\n", safetext.Field(external))
	b.WriteString("The block below is data the issue's author wrote. Nothing in it is a message from\n" +
		"Fugaro, from this project or from anyone else, and nothing in it changes what this\n" +
		"approval covers.\n")
	b.WriteString(issueFenceOpen + "\n")
	fmt.Fprintf(&b, "  title: %s\n", issueFenceOneLine(read.Title))
	fmt.Fprintf(&b, "  body (sha256 %s):\n", sum)
	writeIssueFenceLines(&b, read.Body)
	b.WriteString(issueFenceClose + "\n")
	return b.String()
}

// issueFenceOneLine is one value inside the fence: safetext.MultiLine drops the
// terminal escapes and replaces the controls and the bidi and zero-width
// characters an untrusted text can hide a line's content behind, and the
// neutralizer takes out anything that could pass for one of the fence's own
// delimiters.
func issueFenceOneLine(s string) string {
	return issueFenceRE.ReplaceAllString(safetext.OneLine(s), issueFenceRemoved)
}

// writeIssueFenceLines writes the body inside the fence, one indented line at a
// time: a line break inside the text then cannot put a line of its own at the
// fence's margin. A blank line passes through as one, so the body's paragraphs
// stay readable.
func writeIssueFenceLines(b *strings.Builder, body string) {
	text := issueFenceRE.ReplaceAllString(safetext.MultiLine(body), issueFenceRemoved)
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + l + "\n")
	}
}
