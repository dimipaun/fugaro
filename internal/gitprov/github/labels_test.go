package github

// The label capability's tests for this adapter (D2431, D2439). Everything
// here is testdata/labels_*.json replayed by httpfixture: the requests are
// pinned as recorded — the endpoint, the body, and the installation token the
// call is made with — because what the daemon may write on somebody's pull
// request is exactly the thing worth pinning, and the fixtures are what make
// a call that invented an endpoint or a field fail.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// labelRunID is the id of the run a write is about, as the service sends it
// (O3). This adapter does not read it — the service corroborates the pull
// request's head itself (F17) — and the fixtures are what prove it stays out
// of the request: none of their paths or bodies carries it.
const labelRunID = "20260927-101500-abcd"

// labelerOf finds the provider's label capability the way a caller does (R7):
// by a type assertion, never by a concrete type, so a provider that lost the
// capability fails here rather than at a call site that no longer compiles.
func labelerOf(t *testing.T, p *Provider) gitprov.Labeler {
	t.Helper()
	l, ok := any(p).(gitprov.Labeler)
	if !ok {
		t.Fatalf("the GitHub provider is not found as a gitprov.Labeler by type assertion")
	}
	if !l.Supported() {
		t.Fatalf("Supported() = false on a host that has pull request labels")
	}
	return l
}

// TestGitHubEnsureLabelExists is D2440 on this host: the label is already
// there, GitHub answers 422 with the already_exists code, and the second
// creation of Fugaro's own label is success rather than a failure the
// daemon's tick would report every time it starts.
func TestGitHubEnsureLabelExists(t *testing.T) {
	l := labelerOf(t, open(t, "labels_ensure_exists.json"))
	if err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request"); err != nil {
		t.Fatalf("EnsureLabel on an existing label: %v", err)
	}
}

// TestGitHubEnsureLabelCreates is the other half: a label the repository does
// not have is created, with the name, colour and description the service
// passes through (D2437), and with the installation token this provider
// holds — the daemon holds none (D2439). The fixture pins all four.
func TestGitHubEnsureLabelCreates(t *testing.T) {
	l := labelerOf(t, open(t, "labels_ensure_created.json"))
	if err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request"); err != nil {
		t.Fatalf("EnsureLabel: %v", err)
	}
}

// TestGitHubEnsureLabelRefused is the refusing half of D2440: 422 is also
// GitHub's answer to a label it will not accept, so the status alone is not
// the already_exists answer and a creation refused this way is an error the
// daemon's tick reports rather than a success it hides. The error names the
// label, so an operator reading it knows which one the host turned down.
func TestGitHubEnsureLabelRefused(t *testing.T) {
	l := labelerOf(t, open(t, "labels_ensure_refused.json"))
	err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request")
	if err == nil {
		t.Fatalf("EnsureLabel reported success although the host refused the creation with an invalid error code")
	}
	if want := `creating label "fugaro:running"`; !strings.Contains(err.Error(), want) {
		t.Errorf("EnsureLabel error = %q, want it to carry %q", err.Error(), want)
	}
}

// TestGitHubEnsureLabelForbidden is the same refusal one status down: an
// installation whose token may not write labels answers 403 (U1, U2), which
// is no more success than the 422 above. Only the already_exists code is.
func TestGitHubEnsureLabelForbidden(t *testing.T) {
	l := labelerOf(t, open(t, "labels_ensure_forbidden.json"))
	err := l.EnsureLabel(ctx, "fugaro:running", "1D76DB", "A Fugaro job is working on this pull request")
	if err == nil {
		t.Fatalf("EnsureLabel reported success although the host refused the creation with 403")
	}
	if want := `creating label "fugaro:running"`; !strings.Contains(err.Error(), want) {
		t.Errorf("EnsureLabel error = %q, want it to carry %q", err.Error(), want)
	}
}

// TestGitHubAddLabels pins the add: the names go to the issue endpoint a pull
// request's labels live on, in one request, and the run id the caller sent is
// not part of it (F17).
func TestGitHubAddLabels(t *testing.T) {
	l := labelerOf(t, open(t, "labels_add.json"))
	if err := l.AddLabels(ctx, 12, labelRunID, []string{"fugaro:running", "spec:forge-labels"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
}

// TestGitHubAddLabelsNothingToAdd: the syncer writes only the difference
// (R5), so an empty set is an ordinary call and not a request GitHub would
// refuse for carrying no labels. The fixture holds no exchange at all, and
// httpfixture fails the test on any request.
func TestGitHubAddLabelsNothingToAdd(t *testing.T) {
	l := labelerOf(t, open(t, "labels_nothing_to_write.json"))
	if err := l.AddLabels(ctx, 12, labelRunID, nil); err != nil {
		t.Fatalf("AddLabels with no names: %v", err)
	}
}

// TestGitHubRemoveAbsentLabel: a label the pull request does not carry is
// success — the caller computed the difference from a listing that may have
// moved under it, and a removal that finds nothing to remove has done its
// job. GitHub answers it 404, the same status it gives for a pull request
// that is not there.
func TestGitHubRemoveAbsentLabel(t *testing.T) {
	l := labelerOf(t, open(t, "labels_remove_absent.json"))
	if err := l.RemoveLabels(ctx, 12, labelRunID, []string{"fugaro:failed"}); err != nil {
		t.Fatalf("RemoveLabels of an absent label: %v", err)
	}
}

// TestGitHubRemoveLabelsNoTokenFails: a removal that never reached the label
// is a failure. Here the App is no longer installed on the repository, so the
// lookup that mints the call's token answers 404 — the same status GitHub
// gives for a label that is not on the pull request — and that answer arrives
// wrapped inside the mint's error. Reading it as "the label was absent" would
// report every removal as done while nothing was removed at all.
func TestGitHubRemoveLabelsNoTokenFails(t *testing.T) {
	l := labelerOf(t, open(t, "labels_remove_no_installation.json"))
	err := l.RemoveLabels(ctx, 12, labelRunID, []string{"fugaro:failed"})
	if err == nil {
		t.Fatalf("RemoveLabels reported success although no token was minted and no request was made")
	}
}

// TestGitHubRemoveLabelsNoMintFails is the same failure one step later: the
// installation is found, and the mint itself is what GitHub answers 404 to.
// That answer is a POST's, so the same reading keeps it out — the removal is
// an error and not a label that was absent.
func TestGitHubRemoveLabelsNoMintFails(t *testing.T) {
	l := labelerOf(t, open(t, "labels_remove_no_mint.json"))
	err := l.RemoveLabels(ctx, 12, labelRunID, []string{"fugaro:failed"})
	if err == nil {
		t.Fatalf("RemoveLabels reported success although the mint was refused and no request was made")
	}
}

// TestGitHubRemoveLabel is the removal that finds the label: one request per
// name, on the issue endpoint, and success on GitHub's own answer.
func TestGitHubRemoveLabel(t *testing.T) {
	l := labelerOf(t, open(t, "labels_remove.json"))
	if err := l.RemoveLabels(ctx, 12, labelRunID, []string{"fugaro:running"}); err != nil {
		t.Fatalf("RemoveLabels: %v", err)
	}
}

// TestGitHubListOpenPRLabelsPaged: one read per repository gives every open
// pull request's labels, keyed by number, and it follows the listing's pages
// (D2435, R5). An open pull request with no labels is present with an empty
// list — the caller must be able to tell it from a number the listing did not
// report at all, which is a pull request that is no longer open.
func TestGitHubListOpenPRLabelsPaged(t *testing.T) {
	l := labelerOf(t, open(t, "labels_open_paged.json"))
	got, err := l.ListOpenPRLabels(ctx)
	if err != nil {
		t.Fatalf("ListOpenPRLabels: %v", err)
	}
	want := map[int][]string{
		12: {"fugaro:running", "spec:forge-labels"},
		9:  {},
		7:  {"bug"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListOpenPRLabels = %v, want %v", got, want)
	}
}

// TestGitHubPRLabelsClosed: the read for a pull request the open listing does
// not report answers for a closed one too, and reports its state rather than
// refusing it — clearing the status labels of a merged pull request is what
// the caller reads it for (R5, R8). The labels are returned as GitHub
// reports them, the person's own included.
func TestGitHubPRLabelsClosed(t *testing.T) {
	l := labelerOf(t, open(t, "labels_pr_closed.json"))
	state, labels, err := l.PRLabels(ctx, 12)
	if err != nil {
		t.Fatalf("PRLabels: %v", err)
	}
	if state != gitprov.PRMerged {
		t.Errorf("state = %q, want %q", state, gitprov.PRMerged)
	}
	if want := []string{"fugaro:failed", "spec:forge-labels"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}

// TestGitHubPRLabelsOpen is the same read on a pull request that is still
// open and carries nothing: the state is open and the labels are empty
// rather than absent, which is what tells the caller there is nothing to
// remove.
func TestGitHubPRLabelsOpen(t *testing.T) {
	l := labelerOf(t, open(t, "labels_pr_open.json"))
	state, labels, err := l.PRLabels(ctx, 12)
	if err != nil {
		t.Fatalf("PRLabels: %v", err)
	}
	if state != gitprov.PROpen {
		t.Errorf("state = %q, want %q", state, gitprov.PROpen)
	}
	if len(labels) != 0 {
		t.Errorf("labels = %v, want none", labels)
	}
}
