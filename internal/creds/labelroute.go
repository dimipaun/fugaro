package creds

// The label route's wire contract (D2439, F5, F18): the shapes the
// installation's orchestrator daemon and the credentials service speak when the
// daemon asks the service to read or write a pull request's status and spec
// labels, the path they are spoken on, and the refusals a caller of this
// package reads an answer as.
//
// It is here rather than in the service because the daemon's client is built on
// this package and cannot import the service — `internal/credsvc` imports
// `internal/creds` and not the other way round — so the path is spelled on both
// sides and held to one value by TestLabelRoutePathMatchesClient in the
// service's package, which can read this one and which this one cannot read
// back. What is NOT here is a second reading of the service's contract: the
// service's own decode targets are these structs (credsvc/labelroute.go), so a
// field cannot be renamed on one side alone.
//
// The shape is deliberately a NAME and nothing else. The request carries no
// colour, no description and no permission: the text a label is created with is
// the service's own constants (D2437), the token the write is made with is the
// service's own mint (D2439, F5), and the names the service accepts are the two
// status labels and R3's spec shape (F26). A stolen daemon identity can
// therefore ask for an allowed label on a pull request the service corroborates
// and for nothing else, which is what bounds it (F18).

import "errors"

// LabelPath is the path one label call is asked for on, spelled as the service
// spells it (credsvc.LabelPath).
const LabelPath = "/v1/github/labels"

// The five acts the route performs, spelled as its own action field carries
// them. There is no act that writes arbitrary text and none that removes
// arbitrary names: `remove` takes the two status names alone (F26), and `ensure`
// takes a name whose colour and description the service supplies.
const (
	// LabelActionList reads every open pull request's labels in one paged
	// read of the repository (R5, D2435).
	LabelActionList = "list"
	// LabelActionGet reads one pull request's state and its labels, open,
	// merged or closed (R5, R8).
	LabelActionGet = "get"
	// LabelActionEnsure creates one label with D2437's colour and
	// description, treating one that already exists as success (R6, D2440).
	LabelActionEnsure = "ensure"
	// LabelActionAdd adds one label to one pull request, which the service
	// corroborates against RunID before it writes (F17, O3).
	LabelActionAdd = "add"
	// LabelActionRemove removes one of the two status labels from one pull
	// request, corroborated as Add's is.
	LabelActionRemove = "remove"
)

// LabelRequest is one label call's body: the repository the act is on, the act,
// the App ID the service signs its JWT as, and the fields that act takes.
//
// It is one label per call rather than a set, because that is what the route
// writes: the syncer computes the difference and sends the calls it implies, so
// a body that carried a list would be a body whose names the service had to
// check one by one anyway.
//
// The three optional fields are the caller's own records and not the host's.
// Number is the pull request the syncer read from the run record (F25), RunID is
// the id of the run that opened it, taken from the daemon's own intents (O3) —
// never from the run record, which the job can write — and Name is the label.
// AppID is not a secret: it is the one thing a GitHub App mint needs from its
// caller, and the service's own environment does not hold it (Global Constraint
// 12), so a caller names it exactly as it names it to the merge mint.
type LabelRequest struct {
	Repository string `json:"repository"`
	Action     string `json:"action"`
	AppID      string `json:"app_id"`
	Number     int    `json:"number,omitempty"`
	Name       string `json:"name,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

// LabelPullRequest is one pull request as the two reads answer it: the number
// it is keyed by, its lifecycle state, and the label names it carries.
//
// State is gitprov.PRState's own spelling ("open", "merged", "closed"), which is
// what the service's adapter reports and what the syncer compares against: a
// merged or closed pull request is answered rather than refused, because
// clearing the status labels of one that closed is exactly what the read is for
// (R5, R8).
//
// Labels is every label name the pull request carries, Fugaro's own and a
// person's alike. It is the host's answer and not a filtered one: which of them
// the daemon may write is R5's rule and the service's namespace check, and a
// read that dropped the others would hide from the syncer that a name it wants
// to add is already there.
type LabelPullRequest struct {
	Number int      `json:"number"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
}

// LabelAnswer is one label call's answer: what a read read, and nothing at all
// for the three acts that only change state.
//
// A write answers with no pull requests, and so does a read that found none:
// the two are told apart by the act the caller sent and not by this field, and a
// caller that asked to add a label reads the absence of an error as the answer
// it needs.
type LabelAnswer struct {
	PullRequests []LabelPullRequest `json:"pull_requests,omitempty"`
}

// The client's own refusals. They are sentinels so a daemon can tell a refusal
// it must report from an outage it must retry.
var (
	// ErrLabel is the service refusing, or the call to it failing. The
	// service's own error code is carried beside it and its sentence is not.
	ErrLabel = errors.New("creds: the credentials service did not perform the label call")
	// ErrLabelIdentity is a client that cannot present the daemon's own
	// identity, or a call this code will not make: a missing URL or App ID, no
	// ID token, a repository that is not one, an act that is not one of the
	// five, a pull request number that is not positive, or a label name the
	// route's own namespaces do not admit.
	ErrLabelIdentity = errors.New("creds: the orchestrator daemon has no identity to ask for a label call with")
)
