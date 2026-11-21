package cli

// The daemon's merge-read seams (G3, D2705): what `fugaro orchestrator run`
// hands its tick so that an item whose run left a pull request is `done` once
// that pull request has merged and `failed` once it closed without merging.
//
// None of the read's own rules is restated here — which items wait, how often
// one repository is read, what a state means for the item are G3's
// (internal/orchestrator/mergeread.go). What this file answers is the one thing
// only a running daemon has: the host each repository's pull requests are read
// through, which is the installation's credentials service and not a forge
// (D2439).
//
// That host is the label syncer's own (orchestrator_labels.go): the same
// credentials-service client per repository, the same provider lookup that
// decides GitHub from Bitbucket and refuses a repository the installation
// records no provider for, and the same refusal of a state that is not one of
// the three. The two reads are one route's (F9, D2736, G5) and the two seams
// agree by construction rather than by a second copy of the rules.
//
// All or none, as MergeWaitSeams.Set asks. None is the answer where the
// installation has nothing to reach the route with — no credentials service
// URL, or no daemon account to mint a token as because the installation's
// tfvars carry no orchestrator block (orchestratorIdentityToken's nil) — which
// is the same direction the label seams fail in, and the one direction that is
// safe here: an item whose pull request is never read stays in `review`, where
// its dependents wait, rather than being called done on nobody's word.

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorMergeSeams is the merge-read seams this daemon's tick carries: the
// reader of each repository's pull request state, and the daemon's own warning
// stream. The log is the label seams' own sink (orchestratorDaemonLog), which
// is what every feature of this daemon writes its one line through.
func orchestratorMergeSeams(d *orchestratorDaemon) orchestrator.MergeWaitSeams {
	id := orchestratorIdentityOf(d.lc)
	identity := orchestratorIdentityToken(id)
	if identity == nil {
		return orchestrator.MergeWaitSeams{}
	}
	return orchestrator.MergeWaitSeams{
		Reader: &orchestratorMergeReader{hosts: &orchestratorLabelHosts{
			env: d.env, url: id.url, identity: identity,
			opened: map[string]gitprov.Labeler{},
		}},
		Log: orchestratorDaemonLog{id: d.id, w: d.warn},
	}
}

// orchestratorMergeReader is the daemon's read of the pull request state an item
// waits on (MergeWaitReader), over the labels route's client.
//
// The hosts are resolved through the label seams' lookup — one client per
// repository, built once, refused for a repository the installation records no
// provider or no App for — and everything else here is the two methods'
// translation and nothing more: which acts a host answers, and what a state it
// answered is called in this package's own vocabulary.
type orchestratorMergeReader struct {
	hosts *orchestratorLabelHosts
}

var _ orchestrator.MergeWaitReader = (*orchestratorMergeReader)(nil)

// ListOpen implements MergeWaitReader: every open pull request of one
// repository, by number.
//
// A host with no listing at all (Bitbucket) answers supported=false and is not
// asked: the route refuses the list act for that host one round trip before the
// service would, and the merge read's own rule for it is a `get` per item
// (R7, G5). The supported answer is what the caller reads to decide that, so
// the read is skipped rather than attempted and refused.
//
// What the listing answers is a label set per pull request, and the labels are
// dropped here: this read is about which pull requests the host still reports
// open, and the number is the whole of what the caller asked for.
func (r *orchestratorMergeReader) ListOpen(ctx context.Context, repo string) (map[int]bool, bool, error) {
	host, err := r.hosts.LabelerFor(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	if !host.Supported() {
		return nil, false, nil
	}
	labels, err := host.ListOpenPRLabels(ctx)
	if err != nil {
		return nil, false, err
	}
	open := make(map[int]bool, len(labels))
	for number := range labels {
		open[number] = true
	}
	return open, true, nil
}

// State implements MergeWaitReader: one pull request's lifecycle state.
//
// A refusal for want of a Bitbucket read consumer is the read's own sentinel
// (ErrMergeNoReadConsumer) and not an outage: the service answers both with the
// same status, so the client is where the two are told apart (creds'
// ErrLabelNoReadConsumer, the route's one wire code for it), and the daemon's
// merge read records the first once per repository as a fact naming the fix
// where the second is one line and the next cadence.
//
// A state that is not one of the three cannot reach the switch below — the
// label host refuses it before this (serviceLabelHost.PRLabels) — and it is
// refused here all the same rather than read as "not open": the caller acts on
// the state it is handed, and a state this code has never read the meaning of
// is the one thing that must not move an item.
func (r *orchestratorMergeReader) State(ctx context.Context, repo string, number int) (orchestrator.MergeState, error) {
	host, err := r.hosts.LabelerFor(ctx, repo)
	if err != nil {
		return "", err
	}
	state, _, err := host.PRLabels(ctx, number)
	if err != nil {
		if errors.Is(err, creds.ErrLabelNoReadConsumer) {
			return "", fmt.Errorf("%w: %s", orchestrator.ErrMergeNoReadConsumer, safetext.Field(repo))
		}
		return "", err
	}
	switch state {
	case gitprov.PROpen:
		return orchestrator.MergeOpen, nil
	case gitprov.PRMerged:
		return orchestrator.MergeMerged, nil
	case gitprov.PRClosed:
		return orchestrator.MergeClosed, nil
	}
	return "", fmt.Errorf("the host answered pull request %d of %s as %q, which is not a state this daemon reads",
		number, safetext.Field(repo), safetext.OneLine(string(state)))
}
