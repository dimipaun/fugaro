package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// publishedRelease is the one question this stage asks about its own release:
// is <version> a published release of fugaro (D3269)? Only a published release
// may be named as a project's required CLI — an unreleased build that stamped
// itself would refuse every member of the project, including the ones on the
// release this binary is ahead of, over a version nobody can install.
//
// It is updatecheck.Published with the production client and endpoint, and both
// halves are the ones the daily check already uses: updateCheckHTTPClient
// refuses a redirect (a tag is never followed to another host) and
// stampReleaseEndpoint is the releases collection. A failure of it — the
// network, a responder that is not GitHub's — answers exactly what "not
// published" answers: the stamp is left where it is, and the run says so in one
// line rather than writing a fence it cannot vouch for.
var publishedRelease = func(ctx context.Context, version string) (bool, error) {
	return updatecheck.Published(ctx, updateCheckHTTPClient, stampReleaseEndpoint, version)
}

// stampReleaseEndpoint is the endpoint the bound asks, and a package variable
// for the reason updateCheckEndpoint is one: a test whose project has a stamp to
// raise points it at a server of its own rather than reaching GitHub. It is NOT
// updateCheckEndpoint, which is the latest-release endpoint and answers a
// different question — Published appends a tag's own path to what it is given,
// so the two are not interchangeable.
var stampReleaseEndpoint = updatecheck.ReleasesEndpoint

// The lines this stage prints. stampCurrent is the whole line for a project
// whose stamp already names a release at or below this binary's — the run did
// nothing and the subject of the line is the node, so the one word is enough.
const stampCurrentLine = "required CLI: current"

// versionStampWhat is what the operator reads above the one write: the node
// refuses every fugaro below the release it names, so a raise is a fact about
// every member of the project and never a local edit.
const versionStampWhat = "this raises the project's required CLI to this release: every fugaro below it is refused until it upgrades, and the node is never lowered"

// versionStampUnreadable is what the run says about a node it could not read:
// the step is left where it is, and the door's own warning has already named
// the node (spec, "Read failures fail open"). A node that IS read and cannot be
// decoded is the one read failure that refuses instead
// (versionstamp.MalformedError, D3264).
const versionStampUnreadable = "the project's required CLI could not be read, so this run leaves it exactly as it is"

// devBuildReason is why a development or pre-release binary leaves the node
// alone (spec, "Dev builds"): it has no release to name, and a version that is
// not plain X.Y.Z cannot be compared with the stamp (versionstamp.Below).
const devBuildReason = "this is a development build, which names no release: the required CLI is left exactly as it is"

// versionStampSkip is one reason this run leaves the node where it is. It
// carries undone, which is whether the stamp is a step this machine still owes
// (initflow.Status.Undone): a build with no release to name never owes one, and
// neither does a project whose database is not this installation's, but a
// project this run could have stamped and did not is one the next `fugaro
// update` has to finish.
type versionStampSkip struct {
	reason string
	undone bool
}

func (e *versionStampSkip) Error() string { return e.reason }

// versionStampSkipOf is the skip inside err, with the false a refusal or a
// failed read answers.
func versionStampSkipOf(err error) (*versionStampSkip, bool) {
	var skip *versionStampSkip
	ok := errors.As(err, &skip)
	return skip, ok
}

// versionStampPlan is one read of the node: what the project's required CLI is
// now, what this release would raise it to, and the ETag the write is
// conditional on.
type versionStampPlan struct {
	lc  *localcfg.Config
	db  *rtdb.Client
	ver string
	// etag is the node's ETag as it was read, which is the whole precondition
	// of the write: another writer between the read and the PUT refuses it.
	etag string
	// found is whether a stamp is there at all. A project with no node has no
	// fence, which is a state of its own and not the same as a node already at
	// this release.
	found   bool
	stamp   versionstamp.Stamp
	wroteIt bool
}

// fromText is the node's value for the line: the release it names, or none for
// a project that has never been stamped.
func (p *versionStampPlan) fromText() string {
	if !p.found {
		return "none"
	}
	return p.stamp.Min
}

// wanted is whether this run has a stamp to raise: a node that is not there at
// all, or one whose stored min is BELOW the release this binary carries. A node
// at or above this release is left exactly as it is — the stamp never falls
// (D3262), and one a newer release wrote is a state a later `update` moves
// forward, never back.
//
// The question is asked in versionstamp.Below's own terms — "is version below
// the stamp?" — with this release's value as the stamp, so the stored min is
// the version that is asked about: a min below this release is a fence that has
// to move up to it, and a min at or above it is one this binary may not touch.
// It is the same comparison the reader refuses a binary with (Below, at the
// root fence), read for the other direction: there the running version is the
// version and the node is the stamp, here the node is the version and this
// release is the stamp.
func (p *versionStampPlan) wanted() bool {
	if !p.found {
		return true
	}
	return versionstamp.Below(versionstamp.Stamp{Min: p.ver}, p.stamp.Min)
}

// line is the stage's one answer about the node, in both the plan view and the
// outcome: what the required CLI is moving from and to, or that it is already
// where this release would put it.
func (p *versionStampPlan) line() string {
	if !p.wanted() {
		return stampCurrentLine
	}
	return fmt.Sprintf("required CLI: %s → %s (members below it are refused until they upgrade)", p.fromText(), p.ver)
}

// versionStampStage is the stage itself. It is SelfConfirming, like the policy,
// the workspace, the route, the layer and the credentials copy: it takes its
// own confirmation (confirmStageWrite) and refuses a coding agent's session.
//
// A managed project's gate is asked before that refusal (gatedStages,
// versionStampStage.gateStep): the document it judges is the exact bytes this
// stage's one conditional PUT writes, and an admitted step proceeds without the
// confirmation the verdict replaces.
type versionStampStage struct {
	e *initEngine
	// p is what Apply read and wrote, kept for Verify. Nothing is carried
	// across Check, Plan and Apply: each of them reads the node itself, so the
	// ETag the write is conditional on is the one the write's own read took.
	p *versionStampPlan
}

func newVersionStampStage(e *initEngine) *versionStampStage { return &versionStampStage{e: e} }

func (versionStampStage) Name() string    { return initflow.VersionStamp }
func (versionStampStage) SelfConfirming() {}

func (versionStampStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.VersionStamp, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// read is the one read of the node the stage's four entry points share.
//
// The order is the ownership predicate's, asked before anything is opened in
// the database: a dev build names no release and leaves the node alone without
// reading it at all; then the config's own half (rulesUnavailable — no shared
// budget backend, or a URL that is not the Firebase project's own); then
// budgetDBUnadopted, the one ownership question every writer into the database
// asks (F22), which is where a database marked by another installation is
// refused with that function's own sentence.
//
// A malformed stamp is returned as it is and not turned into a skip: it is the
// one read failure the spec fails closed on (spec, D3264), and this stage is a
// writer that must never overwrite a node it cannot read.
func (s *versionStampStage) read(ctx context.Context) (*versionStampPlan, error) {
	ver := releaseVersion()
	if ver == "" {
		return nil, &versionStampSkip{reason: devBuildReason}
	}
	lc := s.e.lc
	if why := rulesUnavailable(lc); why != "" {
		return nil, &versionStampSkip{reason: why, undone: true}
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage writes no required CLI there", s.e.r.o.doorProgram()); why != "" {
		return nil, &versionStampSkip{reason: why, undone: true}
	}
	var raw json.RawMessage
	etag, found, err := db.GetETag(ctx, versionstamp.Path, &raw)
	if err != nil {
		// A node this run cannot reach at all is a fence this run cannot read,
		// and that fails open with one line (spec, "Read failures fail open",
		// D3264): the stamp is a fence and not a boundary, so a database blip
		// must not stop the one command that brings the installation forward.
		// The door's own guard has already warned in the same words before the
		// loop's first stage (refuseUpdateBelowTheStamp), and the reason whole
		// goes to this run's log rather than onto that line.
		fmt.Fprintf(s.e.r.stageDetail(), "the required CLI was not read: %s\n", oneLineCLI(err.Error()))
		return nil, &versionStampSkip{reason: versionStampUnreadable, undone: true}
	}
	p := &versionStampPlan{lc: lc, db: db, ver: ver, etag: etag, found: found}
	if found {
		stamp, present, err := versionstamp.Decode(raw)
		if err != nil {
			return nil, err
		}
		if present {
			p.stamp = stamp
		} else {
			p.found = false
		}
	}
	return p, nil
}

// held is why this run may not raise the node even though there is a stamp to
// raise, "" when it may. The two facts are the engine's own, and neither is
// about the stamp: what the node says is a promise about the installation this
// run is converging, so a run that leaves the credentials service on the
// release behind may not make that promise (D3267, F20).
//
// It is credentialsMoveHeld first — the same predicate that holds the policy
// node's version below projectpolicy.Version, which is the session and
// dev-build pair — and then the recorded image itself: even in the owner's own
// run, a tfvars whose credentials service is not this release's is a process
// that stays where it is, and a stamp raised over it refuses every cascade and
// Bitbucket-proxied call until a later update succeeds (the owner's ruling of
// 2026-11-10, init_stages.go).
//
// The image is read only once the credentials-service stage has had its turn
// (e.wd, which that stage's own prepare is what sets). Before it — the
// preview's pass, where every stage's check runs before any has run — the
// tfvars on disk still carry the release behind's image, which says nothing
// about this run: it is a service the run itself is about to replace. Holding
// on it there would make the review screen report the stamp as left for the
// owner and then write it in the same run, which is the one thing a plan view
// may never do.
func (s *versionStampStage) held(p *versionStampPlan) string {
	if why := s.e.credentialsMoveHeld(); why != "" {
		return why
	}
	if s.e.wd == nil {
		return ""
	}
	doc, _, err := s.e.recordedCredentialsImage()
	if err != nil || doc == nil {
		return ""
	}
	stale, err := servicesImageStale(p.ver, doc)
	if err != nil || !stale {
		return ""
	}
	return "this installation's recorded credentials service image is not this release's"
}

// heldLine is what the run says while it waits, naming the fact it waits for and
// the door that clears it: the one command that raises the stamp and moves the
// service together.
func (s *versionStampStage) heldLine(p *versionStampPlan, why string) string {
	return fmt.Sprintf("the project's required CLI is %s and this run holds it there: %s; a stamp raised while the credentials service still runs an older release refuses every call that reads the node, so the raise waits — %s, run from the owner's own session, raises the stamp and moves the service in one run",
		p.fromText(), why, selfCommand()+" update")
}

// promised is what this run would really do, and the one line it says about it:
// whether the node moves to this release, and what to say when it does not.
//
// The release bound is asked here and not only in Apply because a stage that
// plans a change is a stage the loop stops at, and a stop blocks every stage
// behind it (F2): a coding agent's session, which may not write the node at
// all, would end at this slot with workspace-project and the five stages after
// it left unrun — over a raise that a lookup cannot confirm and that Apply
// would then not make. What a run is about to do is asked of all of it, the
// bound included, so the answer a session stops for is one the owner's own
// terminal would carry out too.
func (s *versionStampStage) promised(ctx context.Context, p *versionStampPlan) (bool, string) {
	if !p.wanted() {
		return false, stampCurrentLine
	}
	if why := s.held(p); why != "" {
		return false, s.heldLine(p, why)
	}
	published, err := publishedRelease(ctx, p.ver)
	if err != nil {
		// One line for the person and the reason whole for the run's own log
		// (D1352): what a lookup failure carries is a URL, a proxy and a
		// dial error, and none of that belongs on the screen of an update
		// that did the rest of its work. The line is not a withheld write
		// (initRun.withheld): nothing is left for the operator to finish
		// here — the next `fugaro update` raises the stamp — so this is a
		// state of the run's own read and not a warning about the project.
		fmt.Fprintf(s.e.r.stageDetail(), "the required CLI was not raised: the release lookup failed: %s\n", oneLineCLI(err.Error()))
		return false, fmt.Sprintf("the required CLI is left as it is: whether fugaro %s is a published release could not be read (the reason is in this run's log)", p.ver)
	}
	if !published {
		// A version nobody can install may not be required of the project, and
		// the build that would require it is the one that is not a release:
		// the operator reads the fact, and the way out is the release they
		// install (D3269).
		return false, fmt.Sprintf("the required CLI is left as it is: fugaro %s is not a published release, and only a published release may be required", p.ver)
	}
	return true, p.line()
}

// Check reports what the project's required CLI is. A malformed node is refused
// with its own sentence and never overwritten. The three states that leave the
// node exactly where it was — a build with no release, a database that is not
// this installation's, and a run that may not move the service with it — are
// one word in the plan view and two facts to a caller, and only the ones that
// leave the stamp owed carry Undone.
func (s *versionStampStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.read(ctx)
	if skip, ok := versionStampSkipOf(err); ok {
		return initflow.Status{State: initflow.Skipped, Detail: skip.reason, Undone: skip.undone}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	if p.wanted() {
		if why := s.held(p); why != "" {
			return initflow.Status{State: initflow.Skipped, Detail: s.heldLine(p, why), Undone: true}, nil
		}
	}
	if !p.wanted() {
		return initflow.Status{State: initflow.Done, Detail: stampCurrentLine}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.line()}, nil
}

// Plan is what the run says it is about to do. The line is printed where a
// person can read it — a terminal, or --yes, which is a person's word that they
// have read it — because the confirmation is made against it.
func (s *versionStampStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.read(ctx)
	if skip, ok := versionStampSkipOf(err); ok {
		return initflow.Plan{NothingToDo: true, Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	raise, line := s.promised(ctx, p)
	if !raise {
		return initflow.Plan{NothingToDo: true, Detail: line}, nil
	}
	if env.Interactive || env.Yes {
		fmt.Fprintln(s.e.r.cmd.OutOrStdout(), line)
	}
	return initflow.Plan{Detail: line}, nil
}

// Apply is the write. A managed project's gate is asked first (D1016) and the
// session that may not write to the cloud refuses where the gate did not admit
// the step, as the policy and workspace stages' applies do: an admitted step
// proceeds, and the marker check below is kept whole for every other case —
// the plan is read-only, so a session that is going to refuse refuses after the
// same reads it has always made.
//
// The release bound is asked before the gate, the confirmation and the write,
// and each of its two answers leaves the node exactly where it is: a lookup
// that cannot be made and a version that is not a published release are both
// "nothing is written" states, and a refusal here must never be a stamp the
// operator has to repair by hand.
//
// The document is built once (payload) and handed to the gate as well as to the
// write, so the bytes the gate judged and the bytes that land are the same
// (D1022), under the ETag the stage's own read took.
//
// The write is one conditional PUT and never a retry: a lost race — another
// update, or a hand edit through the console — is a refusal the operator reads,
// and the next `fugaro update` raises the stamp. Re-reading and writing again
// would suppress a change nobody read, which is exactly what D3262's no-lowering
// rule and the policy stage's own conditional write exist to prevent. The
// record follows the write and never the admission (D1014), so a lost race
// records nothing and retries nothing: there is no apply to account for.
func (s *versionStampStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	gate := s.e.r.gateUnderway()
	if !gate {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
	}
	p, err := s.read(ctx)
	if skip, ok := versionStampSkipOf(err); ok {
		return initflow.Outcome{Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if raise, line := s.promised(ctx, p); !raise {
		return initflow.Outcome{Detail: line}, nil
	}
	body, err := p.payload()
	if err != nil {
		return initflow.Outcome{}, err
	}
	admitted, err := s.gateStep(ctx, p, body)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !admitted {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
		cmd := s.e.r.cmd
		if err := s.e.r.asking(func() error {
			return confirmStageWrite(cmd, p.lc, restoreAsk(s.e.r.conditions(), s.e.r.asksBack()), s.e.r.gateStandsIn(), env.Yes, versionStampWhat)
		}); err != nil {
			return initflow.Outcome{}, err
		}
	}
	lost, err := p.write(ctx, body)
	if err != nil {
		return initflow.Outcome{}, err
	}
	s.p = p
	if lost != nil {
		return initflow.Outcome{Detail: lost.Error()}, nil
	}
	if admitted {
		if err := s.e.r.gateApplied(ctx); err != nil {
			return initflow.Outcome{}, err
		}
	}
	return initflow.Outcome{Changed: true, Detail: p.line()}, nil
}

// payload is the document this stage writes: the stamp the node is raised to,
// encoded once, so the bytes the gate judged and the bytes the one conditional
// PUT puts at the node are the same (D1022). The set_at is this run's now,
// which is why the document is built here and not read back from the plan.
func (p *versionStampPlan) payload() ([]byte, error) {
	return versionstamp.Encode(versionstamp.Stamp{
		Schema: versionstamp.SchemaVersion,
		Min:    p.ver,
		SetAt:  time.Now(),
	})
}

// write puts body on the node with the ETag it was read at, once. The second
// answer is the lost race, which is a state and not a failure: another writer
// holds the node, nothing was written, and the run says so rather than trying
// again.
func (p *versionStampPlan) write(ctx context.Context, body []byte) (*versionStampLostRace, error) {
	if err := p.db.PutIfMatch(ctx, versionstamp.Path, p.etag, json.RawMessage(body)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return &versionStampLostRace{}, nil
		}
		return nil, dbErr(p.lc, err, true)
	}
	p.wroteIt = true
	return nil, nil
}

// versionStampLostRace is what the run says when its write's precondition
// failed.
type versionStampLostRace struct{}

func (e *versionStampLostRace) Error() string {
	return fmt.Sprintf("%s changed while this update was running (another update or a hand edit landed meanwhile), so nothing was written: read what the node now holds with 'fugaro project policy show', then run %s again — the next update raises the stamp",
		versionstamp.Path, selfCommand()+" update")
}

// Verify reads the node back. A write that claims a release it did not land is
// a fence this binary would try to raise on every run; one that landed and was
// then lowered by another writer is the same state one run later.
func (s *versionStampStage) Verify(ctx context.Context) error {
	if s.p == nil || !s.p.wroteIt {
		// The run wrote nothing: a held stamp, an unadopted database, a dev
		// build or a release the lookup could not confirm each leave the node
		// as it was, and there is nothing of this run's to read back.
		return nil
	}
	p := s.p
	var raw json.RawMessage
	_, found, err := p.db.GetETag(ctx, versionstamp.Path, &raw)
	if err != nil {
		return dbErr(p.lc, err, false)
	}
	if !found {
		return errors.New("the required CLI node is gone, though this run wrote it")
	}
	stamp, _, err := versionstamp.Decode(raw)
	if err != nil {
		return err
	}
	if versionstamp.Below(versionstamp.Stamp{Min: p.ver}, stamp.Min) {
		return fmt.Errorf("the required CLI is still %s, though this run raised it to %s", stamp.Min, p.ver)
	}
	return nil
}
