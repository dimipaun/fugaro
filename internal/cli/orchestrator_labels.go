package cli

// The daemon's label seams (T7, docs/forge-labels): what `fugaro orchestrator
// run` hands its tick so that the pull requests of the runs it launched carry
// the status and spec labels.
//
// None of the syncer's rules are restated here — which labels a phase wants,
// what a difference is, when a pull request is cleared are T5's
// (internal/orchestrator/labelplan.go, labelsync.go). What this file answers is
// the three things only a running daemon has: the repository an item is on, the
// run id the item's own intent recorded, and the pull request number the run's
// record carries. The host behind them is the installation's credentials
// service and not a forge, because the service is the one holder of the App key
// and the one writer of these labels (D2439): the daemon asks it for every read
// and every write, and no token is minted here at all.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorLabelSeams is the label seams this daemon's tick carries: the
// hosts each repository's labels are kept through, the reader of the pull
// request behind a run, and the daemon's own warning stream. All three or none,
// as LabelSeams.Set asks.
//
// None is the answer where the installation has nothing to reach the route
// with: no credentials service URL, or no daemon account to mint a token as
// because the installation's tfvars carry no orchestrator block — an identity
// that is incomplete (orchestratorIdentityToken's nil). A tick with no seams
// labels nothing at all and says nothing about it, which is the state a project
// with no credentials service is in, the state an installation that never opted
// in is in, and the state a project on a host without labels is in for ever
// (R7). A daemon that HAS an identity and fails
// to mint one is a different thing, and never this: the refusal travels back
// through the client and the syncer writes it as one line (the caller's own
// ErrLabelIdentity), because a daemon silently labelling nothing with the
// installation configured for it would be the failure nobody reads.
//
// The URL is read from the installation's own records rather than from the
// environment, the way every other reader of it does (serviceReadTarget): the
// value is published into the local config by `fugaro update`, and it is the
// same string the service's audience check wants, so a daemon that read it from
// anywhere else could mint a token for an audience no revision answers to.
func orchestratorLabelSeams(d *orchestratorDaemon) orchestrator.LabelSeams {
	id := orchestratorIdentityOf(d.lc)
	identity := orchestratorIdentityToken(id)
	if identity == nil {
		return orchestrator.LabelSeams{}
	}
	return orchestrator.LabelSeams{
		Labelers: &orchestratorLabelHosts{
			env: d.env, url: id.url, identity: identity,
			opened: map[string]gitprov.Labeler{},
		},
		PRs: &orchestratorLabelPRs{bucket: d.env.bucket.Bucket, numbers: map[string]int{}},
		Log: orchestratorLabelLog{id: d.id, w: d.warn},
	}
}

// orchestratorLabelHosts resolves the repository an item is on to the host its
// labels are kept through, one client per repository and one per daemon.
//
// Per repository and not one for the installation, because a repository is one
// client's own fact: the route's request carries the repository the client was
// built with, and two repositories' labels are two hosts' (T4's ServiceLabeler,
// and the reason LabelerLookup is a lookup rather than one Labeler). Built once
// and kept, because a client built per tick would be a client per cycle for a
// value that never changes.
//
// A host that cannot be built is an error the syncer writes one line about and
// goes on from: a repository this installation records no GitHub App ID for is
// one whose writes the service could not sign, and the honest answer is no
// label rather than a call the service refuses.
type orchestratorLabelHosts struct {
	env      *cloudEnv
	url      string
	identity func(context.Context) (string, error)
	opened   map[string]gitprov.Labeler
}

var _ orchestrator.LabelerLookup = (*orchestratorLabelHosts)(nil)

// LabelerFor implements LabelerLookup.
func (h *orchestratorLabelHosts) LabelerFor(_ context.Context, repo string) (gitprov.Labeler, error) {
	if repo == "" {
		return nil, errors.New("no repository to label")
	}
	if l, ok := h.opened[repo]; ok {
		return l, nil
	}
	l, err := h.open(repo)
	if err != nil {
		return nil, err
	}
	h.opened[repo] = l
	return l, nil
}

// open is one repository's client, chosen by the provider the installation
// records for it.
//
// A Bitbucket repository gets a client whose Supported is false rather than an
// error: its host has no pull request labels at all (D2436), and R7 asks for a
// silent no-op there and never a line each tick. The client is the same type
// because the route serves the read on that host — which is the merge path's
// business and not this one's — and the syncer asks Supported before it relies
// on any answer.
//
// The kind comes from the local config's own entry (repoProvider, with no
// checkout to fall back on: a daemon has none). A repository the installation
// records no provider for is refused here rather than guessed at, because the
// guess would be which host a label write is sent to.
func (h *orchestratorLabelHosts) open(repo string) (gitprov.Labeler, error) {
	switch h.env.repoProvider(repo, nil) {
	case gitprov.KindGitHub:
		r, ok := h.env.localRepo(repo)
		if !ok || r.GitHubAppID == "" {
			return nil, fmt.Errorf("this installation records no GitHub App ID for %s, and the credentials service signs the token it writes with as that App", safetext.Field(repo))
		}
		return serviceLabelHost{&creds.ServiceLabeler{
			URL: h.url, Repository: repo, AppID: r.GitHubAppID, IDToken: h.identity,
		}}, nil
	case gitprov.KindBitbucket:
		return serviceLabelHost{&creds.ServiceLabeler{
			URL: h.url, Repository: repo, Bitbucket: true, IDToken: h.identity,
		}}, nil
	default:
		return nil, fmt.Errorf("this installation records no git provider for %s, so there is no host to label it through", safetext.Field(repo))
	}
}

// serviceLabelHost is one repository's credentials-service client as the
// syncer's own capability (T7): creds.Labeler adapted to gitprov.Labeler.
//
// The two method sets are the same but for one thing, and that thing is why
// this type exists: the pull request state, which internal/creds spells as the
// route's own string because that package cannot import internal/gitprov (T4's
// comment) and which the syncer compares against gitprov.PROpen.
type serviceLabelHost struct{ l creds.Labeler }

var _ gitprov.Labeler = serviceLabelHost{}

func (h serviceLabelHost) Supported() bool { return h.l.Supported() }

func (h serviceLabelHost) EnsureLabel(ctx context.Context, name, colour, description string) error {
	return h.l.EnsureLabel(ctx, name, colour, description)
}

func (h serviceLabelHost) AddLabels(ctx context.Context, number int, runID string, names []string) error {
	return h.l.AddLabels(ctx, number, runID, names)
}

func (h serviceLabelHost) RemoveLabels(ctx context.Context, number int, runID string, names []string) error {
	return h.l.RemoveLabels(ctx, number, runID, names)
}

func (h serviceLabelHost) ListOpenPRLabels(ctx context.Context) (map[int][]string, error) {
	return h.l.ListOpenPRLabels(ctx)
}

// PRLabels implements gitprov.Labeler: the state of one pull request, open,
// merged or closed, and the labels it carries.
//
// A state that is not one of the three is refused rather than passed on, and
// that refusal is the one thing this adapter decides. The syncer reads a state
// that is not open as a pull request that has closed and clears both status
// labels off it (R4, R8), so an answer this code did not understand — a
// spelling a newer service introduced, a field the route stopped carrying —
// would be read as "closed" and would take `fugaro:running` off a pull request
// whose run is in flight. A refusal costs one line in the log and the next tick
// reads it again.
func (h serviceLabelHost) PRLabels(ctx context.Context, number int) (gitprov.PRState, []string, error) {
	state, labels, err := h.l.PRLabels(ctx, number)
	if err != nil {
		return "", nil, err
	}
	switch s := gitprov.PRState(state); s {
	case gitprov.PROpen, gitprov.PRMerged, gitprov.PRClosed:
		return s, labels, nil
	default:
		return "", nil, fmt.Errorf("the credentials service answered a pull request as %q, which is not a state this daemon reads", safetext.OneLine(state))
	}
}

// orchestratorLabelPRs answers the pull request number behind a run, which is
// the one thing the syncer needs that is neither in the node nor in the item:
// the runner writes the number into the run's own record when it opens the pull
// request (F25), and the daemon reads it back from there.
//
// It is read from the record and never from the item, and the run id it is
// asked about comes from the item's own intents (O3) — the two halves are
// separate on purpose: the id is the daemon's own record of what it launched,
// and the number is the host's answer as the runner recorded it.
//
// A positive number is cached per run id for the daemon's life, because it is
// fixed once a run has opened a pull request: a second tick, or a hundredth,
// reads the run's storage once. An answer that there is no pull request yet is
// NOT cached — a run that opens one a moment later is a run the next tick has to
// see — and neither is a read that failed, which the syncer leaves to the tick
// after it.
type orchestratorLabelPRs struct {
	bucket  *blob.Bucket
	numbers map[string]int
}

var _ orchestrator.LabelPRReader = (*orchestratorLabelPRs)(nil)

// PRNumber implements LabelPRReader.
func (r *orchestratorLabelPRs) PRNumber(ctx context.Context, runID string) (int, bool, error) {
	if n, ok := r.numbers[runID]; ok {
		return n, true, nil
	}
	slug, run, err := runstore.Locate(ctx, r.bucket, runID)
	if errors.Is(err, runstore.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	rec, err := runstore.Open(r.bucket, slug, run).ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	if rec == nil || rec.PR == nil || rec.PR.Number <= 0 {
		return 0, false, nil
	}
	r.numbers[runID] = rec.PR.Number
	return rec.PR.Number, true, nil
}

// orchestratorLabelLog is where the syncer's failures go: one cleaned line on
// this daemon's own warning stream, prefixed with the id it drives, which is
// what every other seam of this daemon does with a line (orchestratorTriage's
// warnOnce). The syncer's lines already say what failed and why.
type orchestratorLabelLog struct {
	id string
	w  io.Writer
}

var _ orchestrator.LabelLog = orchestratorLabelLog{}

func (l orchestratorLabelLog) Warn(_ context.Context, line string) {
	if l.w == nil {
		return
	}
	fmt.Fprintf(l.w, "orchestrator %s: %s\n", safetext.Field(l.id), safetext.OneLine(line))
}
