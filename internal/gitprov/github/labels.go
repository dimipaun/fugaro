package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// This adapter's side of gitprov.Labeler: the label reads and writes the
// credentials service performs for the orchestrator's daemon (D2439). Every
// call here is made with the provider's own installation token, which is the
// service's: the daemon holds no token of its own for this (F5, D2439), and
// the run id the writes carry is the service's to corroborate, not this
// adapter's (F17).
//
// Labels on GitHub are the issue endpoints' — a pull request is an issue —
// so the writes below go to /issues/{number}/labels rather than to /pulls,
// exactly as ApplyReady's own label write already does.

var _ gitprov.Labeler = (*Provider)(nil)

// labelPage is how many pull requests one page of the open listing asks for
// (GitHub's own maximum), and labelMaxPages bounds the walk, so a host that
// answered a next link for ever cannot spin the daemon's tick. The bound is
// generous rather than tight: the listing is one page per repository in a
// real installation (D2435), and a repository with 5,000 open pull requests
// is read in full rather than refused.
const (
	labelPage     = 100
	labelMaxPages = 50
)

// Supported implements gitprov.Labeler: GitHub has pull request labels.
//
// What it does not report is whether this installation's token may write one:
// a label belongs to the issue endpoints, so the write needs a permission the
// App may or may not hold, and an installation without it answers 403 at the
// write rather than here. That is the answer a caller gets instead of a
// capability, which is the same fail-closed direction every other write in
// this package fails in (U1, U2).
func (p *Provider) Supported() bool { return true }

// EnsureLabel implements gitprov.Labeler: it creates the label, and a label
// that is already there is success (D2440).
//
// A label that already exists is left as it is, colour included: this call
// exists so that the first add of a name has something to add to, not to
// converge a label a person may have made in one of Fugaro's own namespaces.
// GitHub answers the second creation with 422 and an already_exists error
// code, and that answer is the only one read as success.
func (p *Provider) EnsureLabel(ctx context.Context, name, colour, description string) error {
	// GitHub spells the field "color"; the capability's own word for it is
	// the one the service's constants use (D2437).
	body := map[string]any{"name": name, "color": colour, "description": description}
	if err := p.api.Do(ctx, "POST", p.repoPath("/labels"), body, nil); err != nil {
		if labelAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("creating label %q: %w", name, err)
	}
	return nil
}

// AddLabels implements gitprov.Labeler. The run id is the service's to
// corroborate against the pull request's head (F17, O3), so it is not read
// here: this adapter is reached only through the service's own gate, and a
// host call that checked the head again would be a second answer to a
// question the gate already asked.
//
// An empty set of names sends no request: GitHub refuses an add with no
// labels, and the caller writes only differences, so an empty difference is
// an ordinary case rather than a bad call.
func (p *Provider) AddLabels(ctx context.Context, number int, runID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	body := map[string]any{"labels": names}
	if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/labels", number)), body, nil); err != nil {
		return fmt.Errorf("adding labels to pull request #%d: %w", number, err)
	}
	return nil
}

// RemoveLabels implements gitprov.Labeler, one request per name — GitHub's
// REST API removes labels one at a time — and a name the pull request does
// not carry is success.
//
// GitHub answers the removal of a label that is not on the pull request with
// 404, which is the same status it gives for a pull request that is not
// there. Reading it as success is safe because of who calls this — the
// service corroborates the pull request before it writes (F17), so a 404
// about the pull request is not one that reaches here — and because of which
// request it answers: labelAbsent reads only the DELETE's own 404, so a
// removal that failed earlier is an error rather than a success. Every other
// refusal is kept and the remaining names are still tried, so one failed
// removal does not leave the others unwritten for a reason that had nothing
// to do with them.
func (p *Provider) RemoveLabels(ctx context.Context, number int, runID string, names []string) error {
	var errs []error
	for _, name := range names {
		path := p.repoPath(fmt.Sprintf("/issues/%d/labels/%s", number, url.PathEscape(name)))
		if err := p.api.Do(ctx, "DELETE", path, nil, nil); err != nil {
			if labelAbsent(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("removing label %q from pull request #%d: %w", name, number, err))
		}
	}
	return errors.Join(errs...)
}

// openPullLabels is one entry of the open pull request listing: the number
// the listing is keyed by and the labels GitHub reports on it. It is the
// listing's own shape rather than pullDetail's, which is one pull request's
// fuller answer.
type openPullLabels struct {
	Number int `json:"number"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListOpenPRLabels implements gitprov.Labeler: one paged walk of the
// repository's open pull requests, keyed by number (D2435, R5).
//
// The listing carries each pull request's labels, so this is one read for
// the whole repository rather than one read per pull request — which is what
// makes a tick that reads every tick affordable (F10). A pull request that
// is open with no labels is in the map with an empty list; a number the map
// does not hold is one the host did not report as open.
func (p *Provider) ListOpenPRLabels(ctx context.Context) (map[int][]string, error) {
	q := url.Values{"state": {"open"}, "per_page": {strconv.Itoa(labelPage)}}
	path := p.repoPath("/pulls?" + q.Encode())
	out := map[int][]string{}
	for page := 0; ; page++ {
		if page >= labelMaxPages {
			return nil, fmt.Errorf("listing the open pull requests: more than %d pages", labelMaxPages)
		}
		var pulls []openPullLabels
		next, err := p.api.DoPage(ctx, "GET", path, nil, &pulls)
		if err != nil {
			return nil, fmt.Errorf("listing the open pull requests: %w", err)
		}
		for _, pr := range pulls {
			names := make([]string, 0, len(pr.Labels))
			for _, l := range pr.Labels {
				names = append(names, l.Name)
			}
			out[pr.Number] = names
		}
		if next == "" {
			return out, nil
		}
		path = next
	}
}

// PRLabels implements gitprov.Labeler: one pull request's state and labels,
// through the read every other call here already uses. A closed or merged
// pull request is reported with its state and its labels rather than
// refused, because clearing the status labels of one that closed is exactly
// what the caller reads it for (R5, R8).
func (p *Provider) PRLabels(ctx context.Context, number int) (gitprov.PRState, []string, error) {
	d, info, err := p.getPull(ctx, number)
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(d.Labels))
	for _, l := range d.Labels {
		names = append(names, l.Name)
	}
	return info.State, names, nil
}

// labelAlreadyExists reports whether err is GitHub refusing to create a label
// because one with that name is already there: 422 with the error code
// already_exists, which is what the second creation of Fugaro's own label
// gets (D2440). The status alone is not enough — 422 is also GitHub's answer
// to a name it will not accept — so the code is what is read.
func labelAlreadyExists(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnprocessableEntity &&
		strings.Contains(strings.ToLower(se.Body), "already_exists")
}

// labelAbsent reports whether err is GitHub answering that a label is not on
// the pull request, which this adapter reads as success.
//
// The 404 has to be the DELETE's own. A removal can fail before it reaches
// the label — the installation lookup that mints the call's token answers 404
// when the App is no longer installed on the repository — and that answer
// arrives wrapped, with its status still inside it, so a reading that stopped
// at the status would report every such removal as done while nothing was
// removed. The token mint is a GET or a POST, never a DELETE, so the method
// is what tells the two apart.
func labelAbsent(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Method == http.MethodDelete && se.Status == http.StatusNotFound
}
