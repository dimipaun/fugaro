package creds

// The issue route's wire contract (D2718, B3): the shapes the installation's
// orchestrator daemon and the credentials service speak when the daemon asks
// the service to read the repository's open issues, read one back, or open one
// of its own, the path they are spoken on, and the refusals a caller of this
// package reads an answer as.
//
// It is here rather than in the service for the label route's reason
// (creds/labelroute.go): the daemon's daemon-side code is built on this package
// and cannot import the service — `internal/credsvc` imports `internal/creds`
// and not the other way round — so the path is spelled on both sides and held
// to one value by TestIssueRoutePathMatchesClient in the service's package,
// which can read this one and which this one cannot read back. What is NOT here
// is a second reading of the service's contract: the service's own decode and
// encode targets are these structs (credsvc/issueroute.go), so a field cannot
// be renamed on one side alone.
//
// The shape is deliberately small, and every field of it is the daemon's own
// already: the request carries the repository the act is for, the act, the App
// ID the service signs its own token as, and the three things an act takes —
// an issue number, a title and a body. Nothing names a permission, a token, a
// state or an author: the token is the service's own mint and never leaves it
// (D2439's pattern, D2718), and the marker D2728 reads is a fact of the ANSWER
// and not of the call.
//
// There is no act that closes an issue, and its absence is deliberate: closing
// is GitHub's own work on the `Fixes #N` a merged run's pull request carries
// (D2727). A close act here would be a second way to close one, and the one
// the design chose leaves the record in the pull request.

import (
	"errors"
	"net/http"
)

// IssuePath is the path one issue call is asked for on, spelled as the service
// spells it (credsvc.IssuePath).
const IssuePath = "/v1/github/issues"

// IssueGoneStatus is the status the service answers a `get` for an issue its
// repository does not hold (404): the ticket a read named is gone — deleted, or
// moved to another repository. It is the whole of the evidence this client
// reads it by, and it is spelled here rather than only in the service because
// the CLIENT is what switches on it (issueclient.go's call); credsvc imports
// creds and not the other way round, so the service's own spelling
// (credsvc.StatusIssueGone) is held to this one by
// TestIssueRouteGoneMatchesClient in the service's package, which can read this
// one and which this one cannot read back.
//
// It is apart from the 503 every other refusal is answered with, and that is
// the whole of why it exists: a refusal the service could answer again is an
// outage to retry, and a ticket that is no longer there is a fact about the
// item the read was made for (B9, D2720).
const IssueGoneStatus = http.StatusNotFound

// The three acts the route performs, spelled as its own action field carries
// them. `list` reads the repository's open issues, which is what the daemon's
// sync ingests from; `get` reads one issue back, open or closed, by number,
// which is what a brief is built from and what tells a closed issue's item
// that its issue is gone; `open` creates one, which is what the mirror writes
// (D2719). Nothing here carries text but `open`, and `open` carries the title
// and the body the item holds.
const (
	// IssueActionList reads every open issue of the repository, pull requests
	// excluded, in GitHub's own order (B11).
	IssueActionList = "list"
	// IssueActionGet reads one issue by number.
	IssueActionGet = "get"
	// IssueActionOpen creates one issue from a title and a body.
	IssueActionOpen = "open"
)

// IssueRequest is one issue call's body: the repository the act is on, the act,
// the App ID the service signs its JWT as, and the fields that act takes.
//
// AppID is not a secret — it is the one thing a GitHub App mint needs from its
// caller, and the service's own environment does not hold it (Global Constraint
// 12) — and every act of this route takes one, because every host this route
// serves is GitHub: unlike the label route there is no second host whose calls
// name none (D2718, D2736).
//
// Title and Body are the OPEN act's alone and are the item's own text: the
// bounds the service holds them to (256 and 8000 characters) are its rule and
// not this contract's, so a caller cannot widen them by writing a bigger body
// — it is refused. What an issue's text may be is this route's answer and not
// a field of the call (D2718).
type IssueRequest struct {
	Repository string `json:"repository"`
	Action     string `json:"action"`
	AppID      string `json:"app_id"`
	Number     int    `json:"number,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
}

// Issue is one issue as the two reads and the one creation answer it: the
// number it is keyed by, the text it carries, its lifecycle state, and who
// opened it.
//
// Body is deliberately on this shape for BOTH reads. The daemon stores no body
// in its state — an item holds the body's SHA-256 and never the body (D2720) —
// but the hash and the brief are the daemon's own work and both are computed
// from the text, so a listing that dropped it would leave the sync unable to
// hash an issue it has not yet ingested and a `get` unable to build a brief.
// The reach of the text is therefore bounded by the class that may call this
// route (the orchestrator daemon alone) and by nothing in this file.
//
// AuthorLogin, AuthorID, AppSlug and AppID are B11's own signals for "this
// installation's App opened this issue" (D2728): the App's bot login, the slug
// of the App the action was performed through, and that App's id — all three
// empty for an issue a person's own account opened. The id is the one that
// says WHICH App: a slug names an App within the host, and any App the
// repository has installed opens issues with a bot login that looks like ours,
// so the marker rule needs the id the installation recorded its App by. The
// rule itself is the sync's and not this route's; what the route does is
// refuse to hide the fact the rule needs.
//
// There is no field here for the token GitHub answered with, the permissions
// the service asked for, or anything else the service holds: the act is
// performed BY the service, with its own mint, and a caller reads the issue
// and nothing else (D2439, D2718).
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url,omitempty"`
	// State is GitHub's own word: "open" or "closed". The listing answers open
	// issues only, so every entry of it carries "open"; the single read answers
	// for a closed issue too, which is what keeps a caller from reading "the
	// read worked" as "the issue is still open" (B11).
	State string `json:"state"`
	// AuthorLogin is the account's login and AuthorID its numeric id, both as
	// GitHub reports them, and both empty for a deleted account.
	AuthorLogin string `json:"author_login,omitempty"`
	AuthorID    string `json:"author_id,omitempty"`
	// AppSlug is the slug of the GitHub App an action was performed through,
	// empty when it was performed by a person's own account.
	AppSlug string `json:"app_slug,omitempty"`
	// AppID is that App's id, as the host reports it and as the installation
	// records its own App (localcfg's github_app_id): the value that tells
	// THIS installation's App from any other App the repository has
	// installed, whose bot login and slug would otherwise read the same.
	AppID string `json:"app_id,omitempty"`
}

// IssueAnswer is one issue call's answer: what a read read, and the issue a
// creation created.
//
// A listing answers every open issue, a `get` answers the one it named, and an
// `open` answers the issue it just created — whose number the item's external
// id is written from (D2728). The three are told apart by the act the caller
// sent and not by this field, and a read that found none answers an empty list
// rather than an error: a repository with no open issues is a fact and not a
// failure.
type IssueAnswer struct {
	Issues []Issue `json:"issues,omitempty"`
}

// The client's own refusals. They are sentinels so a daemon can tell a refusal
// it must report from an outage it must retry.
var (
	// ErrIssue is the service refusing, or the call to it failing. The
	// service's own error code is carried beside it and its sentence is not.
	ErrIssue = errors.New("creds: the credentials service did not perform the issue call")
	// ErrIssueIdentity is a client that cannot present the daemon's own
	// identity, or a call this code will not make: a missing URL or App ID, no
	// ID token, a repository that is not one, an act that is not one of the
	// three, an issue number that is not positive, or a title the open act
	// cannot be made with.
	ErrIssueIdentity = errors.New("creds: the orchestrator daemon has no identity to ask for an issue call with")
	// ErrIssueGone is the service answering that this repository holds no such
	// issue (404): the ticket a read named is gone — deleted, or moved to
	// another repository. It is apart from ErrIssue because asking again cannot
	// clear it, and a caller has to tell the two apart: what a launch reads is
	// the ticket an item was approved with, so a ticket that is no longer there
	// is a fact about the item to report rather than an outage of the service to
	// retry. The `get` act is the one it is answered for; the route's other two
	// acts read a listing and create an issue, and neither names one.
	ErrIssueGone = errors.New("creds: the repository holds no such issue")
)
