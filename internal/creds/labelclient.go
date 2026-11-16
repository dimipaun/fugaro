package creds

// The label client (T4, docs/forge-labels): the call the installation's
// orchestrator daemon makes when it reads and writes the status and spec labels
// of the pull requests its runs opened. The SERVICE performs the act; this
// client only asks for it, and no token comes back (D2439).
//
// It is here because this package is the library the daemon is built on, and
// because the route's shapes are here already (labelroute.go): the body this
// client sends is creds.LabelRequest and the answer it reads is
// creds.LabelAnswer, which are the service's own decode and encode targets too
// (credsvc/labelroute.go), so a field cannot be renamed on one side alone — and
// the two spellings of the path are held to each other by
// TestLabelRoutePathMatchesClient in the service's package.
//
// The daemon's identity is the class the route is gated on: this client presents
// the daemon's own Google ID token, minted for the service's URL, and NOTHING
// else — no run identity, no job — exactly as ServiceMergeMint presents it for
// the merge route. The token comes from IDToken, which is the daemon's own
// credential and not the operator's: a person's ambient token is refused by the
// service's audience check (credsvc's VerifyOrchestrator).
//
// Nothing here puts a value in an error. The service's own answer is read for
// its status and its error code alone, and the sentence beside that code is
// written for the operator's log and not for this caller, so it is dropped
// rather than relayed.
//
// The Labeler interface below is this package's own rather than gitprov's,
// because gitprov sits BELOW this package and this one cannot import it — the
// same seam gitprov/merge.go declares its own MergeMinter behind. T7 adapts this
// client to gitprov.Labeler where the daemon wires it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Labeler is the label capability as the daemon's own library holds it: the
// method set of gitprov.Labeler (T1), with the pull request state as the string
// the route's own shape carries it in (LabelPullRequest.State, which is
// gitprov.PRState's spelling) — the one type this package cannot name.
//
// It is what the daemon depends on and what T7 adapts, so that the wiring
// depends on the capability and not on the way a label is reached.
type Labeler interface {
	// Supported reports whether the host behind this capability has pull
	// request labels at all.
	Supported() bool
	// EnsureLabel creates name with colour and description, treating one that
	// already exists as success (D2440).
	EnsureLabel(ctx context.Context, name, colour, description string) error
	// AddLabels adds names to pull request number. runID is the id of the run
	// that opened it, which the service corroborates the pull request's head
	// against before it writes (F17, O3).
	AddLabels(ctx context.Context, number int, runID string, names []string) error
	// RemoveLabels removes names from pull request number. runID has the
	// meaning AddLabels gives it.
	RemoveLabels(ctx context.Context, number int, runID string, names []string) error
	// ListOpenPRLabels returns every open pull request's labels, keyed by
	// pull request number.
	ListOpenPRLabels(ctx context.Context) (map[int][]string, error)
	// PRLabels returns one pull request's state and its labels, open, merged
	// or closed.
	PRLabels(ctx context.Context, number int) (state string, labels []string, err error)
}

// The two label names this route writes and the namespace a spec label is
// spelled with. They are the route's own constants and not fields of the call
// (F26), respelled here from the service's (credsvc's labelRunningName and
// labelFailedName) so that a name this client would send is one the service
// writes: a status label Fugaro has no label for, a bare namespace and a name
// outside both namespaces are all refused.
const (
	labelRunningName = "fugaro:running"
	labelFailedName  = "fugaro:failed"
	labelSpecPrefix  = "spec:"
)

// labelSpecRE is R3's shape for the part after the namespace, respelled from
// the service's own rule (credsvc's labelSpecRE): lowercase letters, digits and
// dashes, at most 44 of them, so that `spec:` plus the name fits GitHub's
// 50-character limit (F20). The first character is alphanumeric, as R3's own
// regex has it.
var labelSpecRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,43}$`)

// labelRunIDRE is the shape of a run id, respelled from the rule the service
// holds a write to (credsvc's is task.ValidRunID, which this package cannot
// import). The run id is not decoration: it is what the service corroborates
// the pull request's head branch against, so a call that carries none could not
// be corroborated and is refused here.
var labelRunIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// maxLabelAnswerBytes bounds the answer this client reads. It is far larger
// than the mint's (maxMergeAnswerBytes) because a listing answers with EVERY
// open pull request of the repository and every label each of them carries: it
// is the one answer of this contract whose size is a repository's and not a
// constant, so the bound is a bound on a runaway answer rather than a size
// estimate.
const maxLabelAnswerBytes = 1 << 20

// ServiceLabeler is the label route reached over HTTP at the installation's
// credentials service. It is ONE repository's client: the method set takes no
// repository because the route's request carries the one the client was built
// with, and the daemon holds one per repository, which is how two repositories
// stay two hosts' labels.
//
// Every field but HTTP is required, and a missing one is a refusal
// (ErrLabelIdentity) rather than a call the service would answer with less.
type ServiceLabeler struct {
	// URL is the credentials service's base URL, from the installation's own
	// configuration (the service's Cloud Run URL).
	URL string
	// Repository is the "owner/name" every call of this client is made for.
	Repository string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a call names beyond the act. The service's environment does not
	// hold it, so the caller names it — and cannot name anything else: the key
	// that signs, the permissions the token takes and the names the route
	// writes are the service's.
	AppID string
	// HTTP is the client the calls are made with; nil means this package's own
	// hardened one (serviceCallClient: no redirects, no jar, a bounded
	// timeout).
	HTTP *http.Client
	// IDToken mints the daemon's own Google ID token for URL — the identity
	// the service's orchestrator class is. It is called on every call and never
	// cached here: the token's life is the platform's business, and a cached
	// one would be a second place a credential lives.
	IDToken func(ctx context.Context) (string, error)
}

// ServiceLabeler is a Labeler, which is what the daemon's wiring consumes.
var _ Labeler = (*ServiceLabeler)(nil)

// Supported implements Labeler, and it is true: the route this client speaks is
// GitHub's (/v1/github/labels), so the host behind it has pull request labels,
// and a repository whose host has none is never given this client at all — the
// daemon resolves a capability per repository (labelSeams) and leaves the rest
// unlabelled (R7). The service answers an act asked of a host without the
// capability with a refusal rather than an empty success, which is the same
// answer one side over.
func (s *ServiceLabeler) Supported() bool { return true }

// EnsureLabel implements Labeler: it asks the service to create name, treating
// one that already exists as success (D2440).
//
// The colour and the description are taken and DROPPED, which is the point of
// the route rather than an oversight: the request's shape has no field for
// either (LabelRequest), so a stolen daemon identity cannot choose how a label
// looks or what it says (F26), and the text the service applies is its own
// constant (D2437). They are in the signature because the capability's method
// set has them (T1) and the daemon's syncer passes the table's.
func (s *ServiceLabeler) EnsureLabel(ctx context.Context, name, colour, description string) error {
	_, err := s.call(ctx, LabelRequest{Action: LabelActionEnsure, Name: name})
	return err
}

// AddLabels implements Labeler.
func (s *ServiceLabeler) AddLabels(ctx context.Context, number int, runID string, names []string) error {
	return s.writeNames(ctx, LabelActionAdd, number, runID, names)
}

// RemoveLabels implements Labeler.
func (s *ServiceLabeler) RemoveLabels(ctx context.Context, number int, runID string, names []string) error {
	return s.writeNames(ctx, LabelActionRemove, number, runID, names)
}

// ListOpenPRLabels implements Labeler.
func (s *ServiceLabeler) ListOpenPRLabels(ctx context.Context) (map[int][]string, error) {
	ans, err := s.call(ctx, LabelRequest{Action: LabelActionList})
	if err != nil {
		return nil, err
	}
	labels := make(map[int][]string, len(ans.PullRequests))
	for _, pr := range ans.PullRequests {
		if pr.Number < 1 {
			// The number is the key the caller reads the map back by, and a
			// listing that named no pull request would put one there that no
			// host has.
			return nil, fmt.Errorf("%w: the service's answer names no pull request number", ErrLabel)
		}
		labels[pr.Number] = pr.Labels
	}
	return labels, nil
}

// PRLabels implements Labeler.
func (s *ServiceLabeler) PRLabels(ctx context.Context, number int) (string, []string, error) {
	ans, err := s.call(ctx, LabelRequest{Action: LabelActionGet, Number: number})
	if err != nil {
		return "", nil, err
	}
	if len(ans.PullRequests) != 1 {
		// The read is for one pull request and the service answers the one it
		// was asked for: an answer that is empty is a state this client would
		// otherwise report as none, and one that carries several is a number
		// the caller did not ask about.
		return "", nil, fmt.Errorf("%w: the service's answer is not one pull request's", ErrLabel)
	}
	pr := ans.PullRequests[0]
	if pr.Number != number {
		// A state and a set of labels for ANOTHER pull request would be read
		// by the syncer as this one's — the merge mint refuses an answer for
		// another repository for the same reason.
		return "", nil, fmt.Errorf("%w: the service's answer is for another pull request", ErrLabel)
	}
	return pr.State, pr.Labels, nil
}

// writeNames writes one set of names through the route, one call per name: the
// request carries one label, so a set is a set of calls, and the set is checked
// whole before any of it is sent.
//
// A set with nothing in it sends nothing, which is not a failure: the syncer
// writes differences, and a difference that is empty is a tick with nothing to
// write.
//
// The first refusal ends the set. What the route refuses is either a rate for
// the repository (F22) or a substantive refusal — a pull request that is not
// the one the run id opened, a name it does not write — and asking it for the
// rest of a set spends a rate the next tick needs. Nothing is lost by stopping:
// the syncer recomputes its difference from a fresh read every tick, so a set
// that was half written is the difference the tick after this one sees.
func (s *ServiceLabeler) writeNames(ctx context.Context, action string, number int, runID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if err := checkLabelWrite(number, runID); err != nil {
		return err
	}
	for _, name := range names {
		if err := checkLabelName(name, action); err != nil {
			return err
		}
	}
	for _, name := range names {
		if _, err := s.call(ctx, LabelRequest{Action: action, Number: number, Name: name, RunID: runID}); err != nil {
			return err
		}
	}
	return nil
}

// call makes one label call and answers what the service answered: the
// repository, the App ID and the daemon's own ID token are this client's and are
// added here, and the rest of the request is the act's.
//
// The order of the checks is the merge mint's order and its reasons: what this
// code will not send is refused before the URL is looked at, the URL before the
// identity is minted — a token minted for a service this code then refuses to
// send it to is a credential this process did not need to hold — and the token
// before anything is built.
func (s *ServiceLabeler) call(ctx context.Context, req LabelRequest) (LabelAnswer, error) {
	if s.URL == "" || s.AppID == "" || s.Repository == "" || s.IDToken == nil {
		return LabelAnswer{}, fmt.Errorf("%w: this daemon has no credentials service to ask for a label call at", ErrLabelIdentity)
	}
	if !repoShape(s.Repository) {
		return LabelAnswer{}, fmt.Errorf("%w: the call names no repository (owner/name)", ErrLabelIdentity)
	}
	req.Repository, req.AppID = s.Repository, s.AppID
	if err := checkLabelCall(req); err != nil {
		return LabelAnswer{}, err
	}
	// The URL is checked before anything is sent to it: a call presents the
	// daemon's own ID token, which is a bearer credential, and over plain http
	// it would travel in cleartext. https, or http on a loopback address, is
	// the rule the run's own mint and the merge mint hold their service URL to.
	if err := checkServiceURL(s.URL, ErrLabel); err != nil {
		return LabelAnswer{}, err
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the daemon's identity token could not be minted: %v", ErrLabelIdentity, err)
	}
	if !printableValue(id) {
		return LabelAnswer{}, fmt.Errorf("%w: the daemon's identity token is not one header value", ErrLabelIdentity)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the label call's request could not be built", ErrLabel)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+LabelPath, bytes.NewReader(body))
	if err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the label call's request could not be built", ErrLabel)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+id)
	resp, err := serviceCallClient(s.HTTP).Do(httpReq)
	if err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the credentials service could not be reached", ErrLabel)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLabelAnswerBytes+1))
	if err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the service's answer could not be read", ErrLabel)
	}
	if len(data) > maxLabelAnswerBytes {
		return LabelAnswer{}, fmt.Errorf("%w: the service's answer is larger than a label call's", ErrLabel)
	}
	if resp.StatusCode != http.StatusOK {
		return LabelAnswer{}, fmt.Errorf("%w: the service answered %d%s", ErrLabel, resp.StatusCode, refusalCode(data))
	}
	var ans LabelAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return LabelAnswer{}, fmt.Errorf("%w: the service's answer is not a label call's", ErrLabel)
	}
	return ans, nil
}

// checkLabelCall refuses a call this client will not send, respelled from the
// service's own per-act rules (credsvc's readLabelCall): each act takes its own
// fields and no others, a listing names nothing but the repository, a read names
// a pull request, and a write names a pull request, the run id that opened it
// (F17, O3) and a label of the route's own namespaces (F26, R3).
//
// It is here for two reasons and both are about the answer rather than the cost:
// a call the service would refuse is refused one round trip earlier, and — this
// is the one that matters for the sets — the whole set is checked before any of
// its calls is made, so a set carrying a name the route does not write writes
// none of its names instead of writing the ones before it.
func checkLabelCall(req LabelRequest) error {
	switch req.Action {
	case LabelActionList:
		if req.Number != 0 || req.Name != "" || req.RunID != "" {
			return fmt.Errorf("%w: a listing names no pull request, no label and no run", ErrLabelIdentity)
		}
		return nil
	case LabelActionGet:
		if req.Number < 1 {
			return fmt.Errorf("%w: the call names no pull request number", ErrLabelIdentity)
		}
		if req.Name != "" || req.RunID != "" {
			return fmt.Errorf("%w: a pull request read names no label and no run", ErrLabelIdentity)
		}
		return nil
	case LabelActionEnsure:
		if req.Number != 0 || req.RunID != "" {
			return fmt.Errorf("%w: a label is created for the repository, and the call names no pull request and no run", ErrLabelIdentity)
		}
	case LabelActionAdd, LabelActionRemove:
		if err := checkLabelWrite(req.Number, req.RunID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: the call names no act this route performs: it performs %s, %s, %s, %s and %s",
			ErrLabelIdentity, LabelActionList, LabelActionGet, LabelActionEnsure, LabelActionAdd, LabelActionRemove)
	}
	return checkLabelName(req.Name, req.Action)
}

// checkLabelWrite holds an act that names one pull request to the fields such an
// act takes: a pull request number that is one and the run id of the run that
// opened it. A number of zero or less is refused (F17), and so is a run id this
// code does not read, because the corroboration against the head is what the run
// id is for.
func checkLabelWrite(number int, runID string) error {
	if number < 1 {
		return fmt.Errorf("%w: the call names no pull request number", ErrLabelIdentity)
	}
	if !labelRunIDRE.MatchString(runID) {
		return fmt.Errorf("%w: the call carries no run id: a label write names the id of the run that opened the pull request", ErrLabelIdentity)
	}
	return nil
}

// checkLabelName refuses a label name this route does not write, respelled from
// the service's own rule (credsvc's labelName, F26, R3). action is what tells
// the two the rule differs by: `remove` takes the two status names alone, so a
// stolen daemon identity cannot take off a spec label a person added (F23).
//
// The ASCII check comes FIRST, and that order is the rule and not a detail: the
// name is folded to lower case, and Unicode folding maps characters that only
// look like ASCII onto it — the Kelvin sign K onto "k", the dotted capital I
// onto "i" — so a name the host would never match against Fugaro's own could be
// lowered into one that reads as its own. Folding A-Z cannot do that, so a name
// with any byte outside ASCII is refused rather than folded. What is accepted
// after that is the two status names, matched the way the host matches label
// names (case-insensitively), and a `spec:` name whose remainder passes R3's
// shape; and the name is sent as the caller spelled it, because the service
// writes its canonical spelling of it and a caller cannot use a name's case to
// write a second label beside Fugaro's own.
func checkLabelName(name, action string) error {
	if name == "" {
		return fmt.Errorf("%w: the call names no label", ErrLabelIdentity)
	}
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			return fmt.Errorf("%w: the label name is not ASCII, and a name folded with Unicode can lower into one of Fugaro's own that the host would never match", ErrLabelIdentity)
		}
	}
	lower := strings.ToLower(name)
	switch lower {
	case labelRunningName, labelFailedName:
		return nil
	}
	if spec, ok := strings.CutPrefix(lower, labelSpecPrefix); ok {
		if !labelSpecRE.MatchString(spec) {
			return fmt.Errorf("%w: the label names a spec this route does not label: a spec name is lowercase letters, digits and dashes, at most 44 of them, and it is written as spec:<name>", ErrLabelIdentity)
		}
		if action == LabelActionRemove {
			return fmt.Errorf("%w: the call removes no label this route removes: a removal takes %s and %s alone", ErrLabelIdentity, labelRunningName, labelFailedName)
		}
		return nil
	}
	return fmt.Errorf("%w: the call names no label this route writes: it writes %s, %s and a spec: name of R3's shape",
		ErrLabelIdentity, labelRunningName, labelFailedName)
}

// repoShape reports whether repo is one "owner/name". It is the rule the service
// itself applies (gitprov.SplitRepo, which this package cannot import), respelled
// so that a repository this client would send is one the service recognises.
func repoShape(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}
