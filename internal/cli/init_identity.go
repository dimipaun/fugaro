package cli

// init_identity.go is LM14 of docs/launcher-does-not-mint: the update stage
// that removes the people's minter rows. It is R8's late move — a run's
// identity is minted by the credentials service, the launcher-does-not-mint
// switch (LM11, LM12) is what keeps the old grant in place, and this is the one
// run that turns it off.
//
// It runs inside the credentials service's own stage (init_stages.go's
// newCredentialsStage) and not as a stage of its own: the canonical order has
// one stage per name (internal/initflow), this move has no name of its own, and
// what it must follow is that stage's own work — the service deployed on this
// release's image, which is the image the resident orchestrator runs too. The
// probe is made after moveCredentialsImage and before the apply that carries
// the switch, so the order R8 asks for is structural: the row of a service that
// would sign with the release behind's binary never goes.
//
// Three things license the move, and all three are R8's:
//
//   - the service answers the probe (R2's route) at all;
//   - it answers as the runs' signer the local config records
//     (lc.Budget.TokenSigner), which is the whole of what F33 asks — a ticket
//     route that answers proves nothing about signing;
//   - the switch is written into the two roots' own documents, so the removal
//     is this program's own decision and costs the operator no flag (CLAUDE.md's
//     HARD RULE about `fugaro update`).
//
// When any of the three fails the rows stay and one line says why. Nothing is
// asked and nothing is retried beyond the probe's own read: an installation
// whose service is not ready yet moves the grant on its next update.
//
// The policy version this release publishes has moved by the time the stage
// runs (F24), and the reconciliation's D3135 answer is that an installation
// with no published policy keeps no lever for an older CLI: `fugaro update`
// publishes no minimal node, and that CLI meets IAM's own 403 until
// `fugaro project policy publish` gives it one.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// maxProbeAnswerBytes bounds what the probe route may answer, as
// maxTicketAnswerBytes bounds the ticket route's: the service is another
// writer, and an answer nothing bounds is an answer this command renders into
// memory.
const maxProbeAnswerBytes = 8 << 10

// identityProbe is the probe itself, and a package variable for the reason
// launchAskTicket is one: no test may reach a service or mint an identity
// token. The default mints this machine's own Google ID token for the service's
// URL — the probe is R2's operators-only route of the person class, so the
// identity is the one every other ask of that service presents (launchIdentity)
// — and reads the answer back strictly.
var identityProbe = func(ctx context.Context, service string) (identitywire.ProbeResponse, error) {
	return probeIssuer(ctx, service, launchIdentity)
}

// probeIssuer is identityProbe's default: the contract's ask, the identity
// mint, and the answer read for its status, its size and its shape alone.
// Nothing here can put a value a caller sent into an error.
func probeIssuer(ctx context.Context, service string, mint func(context.Context, string) (string, error)) (identitywire.ProbeResponse, error) {
	var out identitywire.ProbeResponse
	actx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	identity, err := mint(actx, service)
	if err != nil {
		return out, err
	}
	body, err := json.Marshal(identitywire.ProbeRequest{})
	if err != nil {
		// The ask has no field, so this cannot fire.
		return out, errProbeUnusable
	}
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, strings.TrimSuffix(service, "/")+identitywire.ProbePath, bytes.NewReader(body))
	if err != nil {
		return out, errProbeUnusable
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return out, errProbeUnreachable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeAnswerBytes+1))
	if err != nil {
		return out, errProbeUnusable
	}
	if slipRouteAbsent(resp.StatusCode) {
		// R7's third bridge answer, for the probe: a service whose release does
		// not serve the route is a release behind, and its identity half is not
		// this release's.
		return out, errProbeReleaseBehind
	}
	if resp.StatusCode != http.StatusOK {
		return out, errProbeRefused
	}
	if len(data) > maxProbeAnswerBytes {
		return out, errProbeUnusable
	}
	if err := identitywire.DecodeStrict(data, &out); err != nil {
		// Strictly, and generously for nothing: a field this release does not
		// know is an answer read out of a document it does not understand.
		return out, errProbeUnusable
	}
	return out, nil
}

// The probe's own failures, each with the clause the one line says. They are
// told apart because a person acts on them differently, and none of them is
// ever a refusal of the update: the rows stay and the next update tries again.
var (
	errProbeUnreachable   = fmt.Errorf("the credentials service could not be reached")
	errProbeReleaseBehind = fmt.Errorf("the credentials service's release does not serve the probe route")
	errProbeRefused       = fmt.Errorf("the credentials service refused the probe")
	errProbeUnusable      = fmt.Errorf("the credentials service's probe answer could not be used")
)

// minterGrantOwed reports whether this run has a minter grant to move: the
// installation records a credentials service on this release's image, the
// service's own address and the runs' signer are recorded, and the switch is
// still on in the documents the two roots were written with.
//
// It is the credentials-service stage's second reason to run (init_stages.go):
// a converged installation whose images are this release's is skipped with one
// line, and an installation whose grant has not moved yet is not — the move is
// owed until the probe licenses it, and a skipped stage would never make it.
//
// Every read behind it is a document this machine already holds, and one that
// cannot be read answers "not owed": the sentence the stage prints is the state
// the run leaves, and a stage that refused here would stop the one command the
// upgrade is over a read of its own. The probe and the apply that follow are
// what decide the move; this only says whether asking is worth a stage.
func (e *initEngine) minterGrantOwed() bool {
	if e.lc == nil || e.lc.Budget == nil || e.lc.Budget.TokenSigner == "" {
		// No signer recorded is no row to move and nothing to judge an answer
		// against (F33): the switch is the runs' identity, and a run of an
		// installation whose config names no signer is not one this release
		// can have moved.
		return false
	}
	if configuredCredentialsServiceURL(e.lc) == "" {
		// An installation that records no service has no issuer to probe and
		// keeps its people's rows: a launcher of it signs locally, and the row
		// is the grant that lets it.
		return false
	}
	ver := releaseVersion()
	if ver == "" {
		// A development build names no release, so no recorded image can be
		// this build's and no orchestrator can be shown to run one.
		return false
	}
	doc, _, err := e.recordedCredentialsImage()
	if err != nil || doc == nil {
		return false
	}
	stale, err := servicesImageStale(ver, doc)
	if err != nil {
		// A document whose images this build cannot read is one the stage's own
		// skip has already refused to answer about.
		return false
	}
	if stale {
		// The service this run would probe is not this release's, or the
		// resident orchestrator beside it is not: an image of the release
		// behind signs locally and fails (R8), and the credentials stage's own
		// move is what puts this release's image there. The stage's skip has
		// already answered for the run that cannot move an image at all.
		return false
	}
	off, err := infra.PeopleMinterOff(doc)
	if err != nil || !off {
		return true // the installation's own document still keeps the rows
	}
	return !e.firebaseMinterOff()
}

// firebaseMinterOff reports whether the Firebase root's own document says the
// switch is off. A document that is not there, and one this build cannot read,
// answer false: the rows may still be in the Firebase project, and the move is
// the direction that must not be missed.
func (e *initEngine) firebaseMinterOff() bool {
	lc := e.lc
	if lc == nil {
		return false
	}
	dir, err := infra.FirebaseWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return false
	}
	vars, err := infra.ReadWorkdirVars(dir, "firebase")
	if err != nil {
		return false
	}
	off, err := infra.PeopleMinterOff(vars)
	return err == nil && off
}

// probeTheIssuer asks the installation's credentials service to sign the
// contract's fixed payload as the runs' signer, and answers whether the grant
// may move and — when it may not — the clause the one line says.
//
// --plan-only answers "not owed" without asking: the switch is a key of the
// roots' own tfvars, and a preview that wrote it would leave a later run
// planning the rows away with no probe behind it. The plan a preview shows is
// the installation as it stands, which is the state a reader of --plan-only
// asked about.
func (e *initEngine) probeTheIssuer(ctx context.Context) (bool, string) {
	if e.r.o.planOnly {
		return false, ""
	}
	service := configuredCredentialsServiceURL(e.lc)
	if service == "" {
		return false, ""
	}
	if err := checkSlipServiceURL(service); err != nil {
		// The ask carries this machine's own ID token, and over plain http to
		// another host it would travel in cleartext.
		return false, oneLine(err.Error())
	}
	ans, err := identityProbe(ctx, service)
	if err != nil {
		return false, oneLine(err.Error())
	}
	want := e.lc.Budget.TokenSigner
	if ans.Signer == "" {
		return false, "the service answered without naming a signer"
	}
	if !strings.EqualFold(ans.Signer, want) {
		// F33: the route answered, and the account it signed as is not the one
		// this installation's runs are signed by. Moving the grant then would
		// take the row away from the people while the service mints as somebody
		// else, and every launch would be refused at its claim.
		return false, fmt.Sprintf("the service signs as %s, and this installation's runs are signed by %s", oneLine(ans.Signer), safetext.Field(want))
	}
	return true, ""
}

// removePeopleRows applies the Firebase root with the switch off: the second
// half of R8's move, and the one the installation root's own apply cannot make,
// the people's rows living in the Firebase root (modules/firebase/iam.tf).
//
// It is applyFirebaseRoot and not the Firebase stage: that stage's work — the
// database, Identity Platform, the rules, the local config — is done and
// unchanged, and what this run owes is the one apply that plans the rows away.
// The state decides the run index's gate (dbKnown false), which is the
// direction that never plans an installation's indexes away, and the issuer's
// row is the installation root's own output, read back from the apply the
// caller has just made.
func (e *initEngine) removePeopleRows(ctx context.Context, outs infra.InstallationOutputs, spec infra.InstallationSpec) error {
	lc := e.lc
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		// No Firebase project recorded is no Firebase root: the rows live in
		// one this installation has never had.
		return nil
	}
	admins, skipped, _, err := infra.ProjectAdmins(ctx, e.c, lc.GCPProject)
	if err != nil {
		return initErr(err)
	}
	for _, s := range skipped {
		e.r.warn("not a budget admin (owners and editors who are users or groups are; a service account can be added with --budget-admin): " + s)
	}
	issuer := ""
	if cs := outs.CredentialsService; cs != nil {
		issuer = cs.Account
	}
	off := false
	_, _, err = e.r.applyFirebaseRoot(ctx, e.c, e.bin, lc, spec, firebaseRootInputs{
		fp: lc.Budget.FirebaseProject, admins: admins, outs: outs, issuer: issuer,
		peopleMinter: &off,
	})
	return err
}

// announceIdentityMove prints R10's lines at the update door, before the note
// about runs in flight and before the run's own confirmation: what the move of
// the runs' identity changes for the people who launch.
//
// It says nothing with --json or --print-vars (stdout is a document, not a
// transcript), nothing at any door but the update's (a setup creates an
// installation rather than moving one), and nothing at all when the grant has
// already moved. It refuses nothing, asks nothing, and cannot fail the update:
// every read behind it is one that answered nothing rather than an error.
func (e *initEngine) announceIdentityMove(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.asJSON || o.printVars {
		return
	}
	if !e.minterGrantOwed() {
		return
	}
	w := loggedOut(r.w, r.repLog)
	fmt.Fprintln(w, "This update moves the runs' identity to the credentials service. Before it changes anything:")
	for _, line := range e.identityMoveLines(ctx) {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// identityMoveLines is R10's four facts, in the order the reconciliation names
// them. The first is silent when no group is on the roster, and the last is
// silent when nothing is queued: a line about a thing that is not there is a
// line a person reads past.
func (e *initEngine) identityMoveLines(ctx context.Context) []string {
	var out []string
	for _, group := range e.launcherGroups() {
		out = append(out, fmt.Sprintf("%s can no longer launch: a launch is admitted by the caller's own address now, so name each person it holds (%s user add user:<email>)", group, selfCommand()))
	}
	out = append(out,
		"a CLI older than this release cannot launch until it is upgraded, and it is told so when it tries",
		fmt.Sprintf("`%s pair` now asks the credentials service", selfCommand()),
	)
	if n := e.queuedExecutions(ctx); n > 0 {
		out = append(out, fmt.Sprintf("%s queued and not started halt with the policy this update publishes: relaunch them afterwards", runsPhrase(n)))
	}
	return out
}

// launcherGroups is the roster's `group:` entries, which are the ones the move
// takes away: IAM admits a group's members through the binding, and the service
// admits a caller by its own address, so a group entry is a grant the people
// behind it lose.
//
// The roster is this run's own lists (update_access.go's deriveAccess, which
// the door's roles step has already run): the installation's launchers and
// operators, which is the list the Firebase root's people's rows are planned
// from and the list the service's person class is verified against.
func (e *initEngine) launcherGroups() []string {
	var out []string
	for _, m := range append(slices.Clone(e.spec.Launchers), e.spec.Operators...) {
		if strings.HasPrefix(m, "group:") {
			out = append(out, safetext.Field(m))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// queuedExecutions counts the runs this update's policy version bump will halt
// (F24, D3131): executions the installation has created that have not started.
// They are not the runs in flight — those have bootstrapped and finish — they
// are the ones still in the platform's queue, and the bump is what refuses them
// at bootstrap with the skew sentence.
//
// The read is the platform's own listing over every job of the region, bounded
// by a window (queuedExecutionsSince) because a queue older than it is not what
// this line is about. A read that could not answer counts nothing: the line is
// information, and a door that failed over it would invent a new way for the one
// command the upgrade is to stop.
func (e *initEngine) queuedExecutions(ctx context.Context) int {
	if e.lc == nil {
		return 0
	}
	n, err := countQueuedExecutions(ctx, e.lc)
	if err != nil {
		return 0
	}
	return n
}

// queuedExecutionsSince bounds the queue's read: a launch queues for seconds,
// and an execution the platform has not started within this window is not one
// this update's bump is about to reach.
const queuedExecutionsSince = 6 * time.Hour

// countQueuedExecutions is the read behind the door's last line, and a package
// variable so a test answers it with no cloud. It opens the installation's own
// backend and lists the executions that are not terminal, counting the ones
// that have not started.
var countQueuedExecutions = func(ctx context.Context, lc *localcfg.Config) (int, error) {
	opts := gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager,
		CloudBuild: lc.Endpoints.CloudBuild, NoAuth: lc.Endpoints.NoAuth}}
	be, err := backend.Open(ctx, lc.Backend(), cloudOpeners(lc, opts))
	if err != nil {
		return 0, err
	}
	xs, err := be.List(ctx, backend.ListFilter{Since: time.Now().Add(-queuedExecutionsSince), ActiveOnly: true})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range xs {
		if x.Started.IsZero() {
			n++
		}
	}
	return n, nil
}
