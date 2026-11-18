// Package issuesync brings a project's open issues into the orchestrator's
// backlog node, and writes an item of that node back out as an issue (B4,
// D2718, D2719, D2720, D2728).
//
// It is the ingest and nothing else: it reads one repository's issues through
// an Adapter — the seam the credentials service's issue route (B3) is bound to
// in internal/cli, so nothing here holds a credential, names a host or knows
// how a call is made — and answers the patch one daemon turn makes to the
// backlog node. Every write goes through the daemon's own fence
// (orchestrator.Apply), and the item that lands holds a reference and never the
// issue's body: the external id, a title of at most 80 characters and the
// body's SHA-256 (D2720).
//
// Nothing here reads a clock, a file or a network of its own: the adapter is
// handed in, the stamp is the cycle's own reading of the database server's
// clock (D1230), and a turn that cannot read a repository writes nothing at
// all.

package issuesync

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// Issue is one issue as an adapter answers one: the four fields the sync acts
// on and the two that say who opened it (creds.Issue, B3's own shape).
//
// Body is here and is stored nowhere. The item keeps the body's SHA-256
// (D2720), and the hash, the title and the text a mirrored issue carries are
// all computed from this text, so an adapter that dropped it would leave the
// sync unable to hash an issue it has not ingested yet — while the state, the
// log and every fact stay free of it.
type Issue struct {
	// Number is the issue's number in its repository, from one: half of the
	// external id an item is keyed on (D2728).
	Number int
	// Title is the issue's own line and Body its text, both as the host reports
	// them and both untrusted: a title passes safetext on the way into an item
	// (D2747) and a body is never one.
	Title string
	Body  string
	// State is the host's own word, "open" or "closed". It is read for the
	// listing's sake: ListOpen answers open issues only, so an item whose number
	// the listing does not carry is one whose issue closed.
	State string
	// AuthorLogin is the account that opened the issue, and AppSlug the slug of
	// the GitHub App the action was performed through — empty for an issue a
	// person's own account opened. The two together are the whole of "the
	// installation's App opened this issue" (D2728).
	AuthorLogin string
	AppSlug     string
}

// Adapter is one repository's issues as the sync reaches them (D2718): the
// open ones, and the one act that writes. It is the seam the daemon binds the
// credentials service's issue route to, so a second host is a second
// implementation and nothing above this line changes.
//
// One adapter is one repository's. The cadence the daemon holds a repository's
// reads to is per repository (D2742), the route's acts are per repository, and
// the external id an item is keyed on names the repository — so the repository
// is the adapter's own construction and never an argument of a call.
type Adapter interface {
	// ListOpen returns every open issue of the repository, in the host's own
	// order.
	//
	// A pull request is not an issue and is never among them (D2720): GitHub
	// answers both from one listing, and the adapter is where they are told
	// apart, because an item keyed on a pull request's number would be a work
	// item for somebody's patch.
	//
	// An error is a host that would not answer, and it is the whole of that
	// repository's turn: a listing that failed is not an empty one, and a sync
	// that read it as one would archive every item the repository has (see the
	// caller in internal/orchestrator, which logs the line and goes on).
	ListOpen(ctx context.Context) ([]Issue, error)
	// Open opens one issue in the repository and answers it as the host created
	// it: its number is what the item's external id is written from (D2719).
	Open(ctx context.Context, title, body string) (Issue, error)
}

// botSuffix is GitHub's own mark of an App's bot account: the App's slug and
// this, and no account a person holds carries it (gitprov/github's botaccount,
// which holds a run's commits to the same login).
const botSuffix = "[bot]"

// AppOpened reports whether an App opened this issue rather than a person's own
// account (D2728). Both of the route's signals have to agree for it to count:
// the action was performed through an App (AppSlug), and the issue's author is
// that App's own bot login, exactly "<slug>[bot]" — an account that merely ends
// in "[bot]" is some other App's, and a login without the suffix is a person's
// whatever the slug says.
//
// What this cannot tell is whether the App is *this installation's*: the
// route's answer carries a slug and no id, and the daemon knows its App only by
// the id its installation was recorded with. An issue another App opened with a
// marker in it therefore counts here as one of ours. What it is worth is still
// the whole of the fence the marker needs: only a GitHub App's bot can have
// performed the action, so a person cannot write a marker that counts by
// editing their own issue, and a marker written by one App is read as an App's
// own — never as a person's.
func (i Issue) AppOpened() bool { return i.AppSlug != "" && i.AuthorLogin == i.AppSlug+botSuffix }

// externalHost is the one host an external id names, and it is GitHub's alone
// (D2718): a second host here would have to mean a second ingest, and a key
// naming a host nothing reads is a key nothing can dedupe (orchestrator's
// externalRE, which holds an id to this spelling).
const externalHost = "github:"

// ExternalID is the dedupe key one issue's item carries:
// `github:<owner>/<repo>#<n>`, lower-case as the App answers a repository name
// and in the one shape orchestrator's externalRE accepts (D2728). repo is a
// canonical repository name, which the caller checks (CanonicalRepo) before a
// key is built from it: a key in any other spelling is one no lookup reaches.
func ExternalID(repo string, number int) string {
	return externalHost + repo + "#" + strconv.Itoa(number)
}

// ExternalNumber reads back the issue number an external id names, and whether
// the id is one of repo's issues at all. An id naming another repository is
// that repository's turn and never this one's, which is what keeps the backlog
// of a project syncing two repositories from archiving one's items on the
// other's listing.
func ExternalNumber(repo, external string) (int, bool) {
	rest, ok := strings.CutPrefix(external, externalHost+repo+"#")
	if !ok {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil || number < 1 {
		return 0, false
	}
	return number, true
}

// The mirror marker: what an issue carries when the daemon opened it for an
// item of its own, and what tells the next turn's ingest that the issue it is
// looking at is that item's other face (D2728).
//
// It is an HTML comment, so GitHub renders it as nothing and a person reading
// the issue reads the item's own text; it is one line of its own, so a body
// that merely quotes a marker inside a sentence is not one; and it carries the
// item's id and nothing else, so a reader of the body learns a name the daemon
// already knows and never anything about the repository or the project.
const (
	markerPrefix = "<!-- fugaro:backlog-item "
	markerSuffix = " -->"
)

// markerIDRE is the shape of an item id as a marker may carry one
// (orchestrator's idRE, which the fence holds every item id to): a name of
// digits, letters, hyphens and underscores. Anything else in a marker is text,
// not an item.
var markerIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Marker is the line an issue the daemon opens for one item carries.
func Marker(itemID string) string { return markerPrefix + itemID + markerSuffix }

// markedItem is the item id a body's marker names, and whether the body carries
// one at all. A marker counts where a line of the body is the marker and
// nothing else: a body that mentions one inside a sentence, indents it or
// quotes it in a block is text about a marker rather than a marker.
func markedItem(body string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		id, ok := strings.CutPrefix(line, markerPrefix)
		if !ok {
			continue
		}
		id, ok = strings.CutSuffix(id, markerSuffix)
		if !ok || !markerIDRE.MatchString(id) {
			continue
		}
		return id, true
	}
	return "", false
}

// issueTitle is an issue's title as an item stores one: safetext's single line
// (D2747), clipped to the 80 characters D2720 bounds an ingested title to.
//
// The clip is also what keeps the mirror's own write inside the route's bound
// (256 characters, B3): an item's title may be twice that, and an issue opened
// with one would be refused by the service rather than by anything here.
func issueTitle(title string) string {
	return safetext.Clip(safetext.OneLine(title), MaxTitleRunes)
}

// MaxTitleRunes is how much of an issue's title an item keeps (D2720): the
// title is a reference to work read elsewhere and never the work itself, so it
// is one short line whatever the issue's title is.
const MaxTitleRunes = 80
