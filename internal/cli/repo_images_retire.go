package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The legacy repo-to-image member leaving the published policy node: the step
// `fugaro update` runs after the credentials service's own stage (U1c of
// docs/image-map-by-provider, D3376 as amended).
//
// **Why the member outlives the publish.** A release from before L11 reads a
// repository's image out of `image_map`, and the credentials service of that
// release finds no map at all on a node carrying only `repo_images` — so the
// policy stage writes the new member and KEEPS the old one, and this step takes
// the old one away once the process that reads it has been replaced. The order
// is the whole of it: Policy, then CredentialsService, then this.
//
// **While both members are on the node, `repo_images` wins** (R2), so an update
// that stops between the two steps leaves a node every reader of either release
// reads correctly, and the next `fugaro update` removes the old member.
//
// **It holds nothing and it changes nothing a run holds.** The write is one
// conditional PUT of the node with the one member removed, under the ETag the
// stage's own read took, and the generation it lands is recorded in this
// machine's config (D854) so that record does not go stale the moment the write
// lands — a stale record is what refuses the operator's next edit. No run in
// flight is asked to wait for it and no lease is touched: what changes is one
// member no reader of this release reads at all.
//
// **A node another release wrote is skipped, not refused.** The run's own
// preview reads the node before the policy stage has migrated it, and every
// installation whose node is far enough behind is in that state: the member is
// only ever taken off a document this release reads, so this step waits for
// that stage — in the execution pass behind the preview, where the removal then
// lands on the node the migration wrote.
//
// **A coding agent's session is skipped, not refused.** The member is the map a
// release behind reads, and the service that reads it is one only the owner's
// own session replaces (credentialsSessionHeld), so the session deploys nothing
// and removes nothing; the owner's next `fugaro update` does both.

// The lines this stage prints. They are one line each, lowercase and factual:
// what the node serves once the run is done, and what it still serves when the
// run leaves the member where it is.
const (
	// imageMapMemberKey is the legacy top-level member the node carries and this
	// stage removes. It is spelled here rather than in internal/projectpolicy
	// because the package holds the member's own type and rules (RepoImages is
	// its successor) and nothing of the old one's, and a constant added there
	// for one caller would be a second name for the same string.
	imageMapMemberKey = "image_map"

	imageMapRetireDone   = "the repository-to-image map: repo_images only"
	imageMapRetireWaits  = "the repository-to-image map: image_map stays, repo_images is not on the node yet"
	imageMapRetireNoNode = "the repository-to-image map: no policy is published"
	// imageMapRetireOtherVersion is the node written by another release, which
	// is what a run reads at its own preview, before the policy stage — which
	// runs earlier in this order — has migrated the node. The removal is the
	// write the node that stage leaves gets: a member is only ever taken off a
	// document this release reads.
	imageMapRetireOtherVersion = "the repository-to-image map: the published policy is another version, so the policy stage rewrites it before this step: the member leaves the node that stage writes"
	imageMapRetireWhat         = "this takes the legacy image_map member off the published policy node: the credentials service that read it is this release's, and the node serves the repository-to-image map from repo_images alone"
)

// imageMapRetireSkip is one reason this run leaves the node exactly as it is.
// undone marks the two skips that leave the member OWED — a node this run could
// not read, and a service this run did not replace — as against the member that
// is simply not there, which is the state the stage exists to reach.
type imageMapRetireSkip struct {
	reason string
	undone bool
}

func (e *imageMapRetireSkip) Error() string { return e.reason }

// imageMapRetireSkipOf is the skip inside err, with the false a refusal or a
// failed read answers.
func imageMapRetireSkipOf(err error) (*imageMapRetireSkip, bool) {
	var skip *imageMapRetireSkip
	ok := errors.As(err, &skip)
	return skip, ok
}

// imageMapRetirePlan is one read of the node: what it carries, the ETag the
// write is conditional on, and the document this run would write.
type imageMapRetirePlan struct {
	lc   *localcfg.Config
	db   *rtdb.Client
	etag string
	// body is the node this run writes: the published document with its
	// image_map member removed and every other byte as it was. It is built
	// once, so the bytes the confirmation is about and the bytes the one
	// conditional PUT puts at the node are the same.
	body []byte
	// retired is whether this run has the member to take off, and served is
	// whether the node serves its map from repo_images today (R2): only a node
	// that answers repo_images may lose the member the release behind reads.
	retired bool
	served  bool
	// found is whether a policy node is published at all, and wroteIt whether
	// this run put the document at it.
	found   bool
	wroteIt bool
}

// line is the stage's one answer about the node, in both the plan view and the
// outcome.
func (p *imageMapRetirePlan) line() string {
	switch {
	case !p.found:
		return imageMapRetireNoNode
	case !p.retired:
		return imageMapRetireDone
	case !p.served:
		return imageMapRetireWaits
	}
	return imageMapRetireDone
}

// writes is whether this run has a write to make: the node carries the old
// member, its map is served from repo_images (R2), and the credentials service
// that reads the old member has been replaced by this run.
func (p *imageMapRetirePlan) writes() bool { return p.retired && p.served }

// imageMapRetireStage is the stage itself. It is SelfConfirming, like the
// policy, the credentials copy and the required-CLI stamp: it takes its own
// confirmation (confirmStageWrite) for the document it rewrites, and a coding
// agent's session is skipped rather than refused — the session replaces no
// credentials service, so there is nothing for the removal to be safe for.
type imageMapRetireStage struct {
	e *initEngine
	// p is what Apply read and wrote, kept for Verify. Nothing is carried
	// across Check, Plan and Apply: each reads the node itself, so the ETag the
	// write is conditional on is the one the write's own read took.
	p *imageMapRetirePlan
}

func newImageMapRetireStage(e *initEngine) *imageMapRetireStage { return &imageMapRetireStage{e: e} }

func (imageMapRetireStage) Name() string    { return initflow.ImageMapRetire }
func (imageMapRetireStage) SelfConfirming() {}

func (imageMapRetireStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.ImageMapRetire, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// read is the one read of the node the stage's four entry points share.
//
// The order is the ownership predicate's, asked before anything is opened in
// the database: a coding agent's session removes nothing; then the config's own
// half (rulesUnavailable — no shared budget backend, or a URL that is not the
// Firebase project's own); then budgetDBUnadopted, the one ownership question
// every writer into the database asks (F22). The service this run must have
// replaced is asked last, on the plan the write would be made from, because the
// answer is about the document rather than about the database.
func (s *imageMapRetireStage) read(ctx context.Context) (*imageMapRetirePlan, error) {
	if agentMarker(os.Getenv, s.e.r.stdinTerminal()) != "" {
		return nil, &imageMapRetireSkip{reason: credentialsSessionHeld}
	}
	lc := s.e.lc
	if why := rulesUnavailable(lc); why != "" {
		return nil, &imageMapRetireSkip{reason: why, undone: true}
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage removes no policy member there", s.e.r.o.doorProgram()); why != "" {
		return nil, &imageMapRetireSkip{reason: why, undone: true}
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return nil, err
	}
	p := &imageMapRetirePlan{lc: lc, db: db, etag: etag, found: found}
	if !found {
		return p, nil
	}
	node, ok, err := projectpolicy.Decode(raw)
	if err != nil {
		// A node another release wrote refuses Decode, and it is the ordinary
		// state of the run's own preview: every installation one release behind
		// reads version N here, before the policy stage has migrated it. It is
		// a skip rather than a failure — a failure would end the preview at
		// this stage and leave the run without the review screen it exists for,
		// which is exactly what the workspace converge tests caught. The
		// removal follows the migration, in the execution pass, on the node the
		// policy stage writes.
		return nil, &imageMapRetireSkip{reason: imageMapRetireOtherVersion, undone: true}
	}
	if !ok || node == nil {
		return p, nil
	}
	if _, has, err := jsonMember(raw, imageMapMemberKey); err != nil {
		return nil, err
	} else if !has {
		return p, nil
	}
	p.retired = true
	// The node's own answer, asked through the one function that states it
	// (EffectiveRepoImages, R2): a node carrying an empty repo_images serves
	// the legacy map, and taking that member away would take the project's map
	// with it.
	_, p.served = node.EffectiveRepoImages()
	if !p.served {
		return p, nil
	}
	if why := s.held(); why != "" {
		return nil, &imageMapRetireSkip{reason: s.heldLine(why), undone: true}
	}
	body, err := dropPolicyJSONMember(raw, imageMapMemberKey)
	if err != nil {
		return nil, userErr("removing the repository-to-image map's legacy member from %s: %v", projectpolicy.PathShared(), err)
	}
	p.body = body
	return p, nil
}

// held is why this run may not take the legacy member away even though there is
// one to take, "" when it may. The member exists for the release behind's
// credentials service, which reads it and finds no map at all on a node
// carrying only repo_images, so the removal waits for the process that reads it
// to be this release's.
//
// It is credentialsMoveHeld first — the same predicate that holds the policy
// node's version, which is the session and dev-build pair — and then the
// recorded image itself, exactly as the required-CLI stamp asks it: a dev build
// resolves no released image, so the stage above leaves the service where it is,
// and a member taken off under it is a service that reads no map until a later
// update succeeds.
//
// The image is read only once the credentials-service stage has had its turn
// (e.wd, which that stage's own prepare is what sets): before it — the
// preview's pass, where every stage's check runs before any has run — the
// tfvars on disk still carry the release behind's image, which says nothing
// about this run, because it is a service the run itself is about to replace.
func (s *imageMapRetireStage) held() string {
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
	if stale, err := servicesImageStale(releaseVersion(), doc); err == nil && stale {
		return "this installation's recorded credentials service image is not this release's"
	}
	return ""
}

// heldLine is what the run says while it waits, naming the member that stays
// and the door that clears it.
func (s *imageMapRetireStage) heldLine(why string) string {
	return fmt.Sprintf("the repository-to-image map: image_map stays on %s and this run holds it there: %s; the credentials service of the release behind reads that member and finds no map on a node carrying only repo_images, so the removal waits — %s, run from the owner's own session, replaces the service and removes the member in one run",
		projectpolicy.PathShared(), why, selfCommand()+" update")
}

// Check reports what the node carries. The three states that leave the node
// exactly where it was — the member that is not there, a map the node serves
// from image_map, and a service this run did not replace — are one word in the
// plan view and two facts to a caller, and only the ones that leave the removal
// owed carry Undone.
func (s *imageMapRetireStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.read(ctx)
	if skip, ok := imageMapRetireSkipOf(err); ok {
		return initflow.Status{State: initflow.Skipped, Detail: skip.reason, Undone: skip.undone}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	if !p.found || !p.retired {
		return initflow.Status{State: initflow.Done, Detail: p.line()}, nil
	}
	if !p.served {
		return initflow.Status{State: initflow.Skipped, Detail: p.line()}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.line()}, nil
}

// Plan is what the run says it is about to do. The line is printed where a
// person can read it — a terminal, or --yes, which is a person's word that they
// have read it — because the confirmation is made against it. A run that holds
// the member says why and prints nothing for a confirmation.
func (s *imageMapRetireStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.read(ctx)
	if skip, ok := imageMapRetireSkipOf(err); ok {
		return initflow.Plan{NothingToDo: true, Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	if !p.writes() {
		return initflow.Plan{NothingToDo: true, Detail: p.line()}, nil
	}
	if env.Interactive || env.Yes {
		fmt.Fprintln(s.e.r.cmd.OutOrStdout(), imageMapRetireWhat)
	}
	return initflow.Plan{Detail: p.line()}, nil
}

// Apply is the write: one conditional PUT of the node with the member removed,
// under the ETag the stage's own read took, and never a retry — a lost race is
// a refusal the operator reads, and the next `fugaro update` removes the member.
//
// The generation follows the write (recordPublishedGeneration) and never the
// confirmation, so a write that did not land records nothing: the record is the
// node this machine's config was written from, and a record moved for a write
// the node never received is what refuses the operator's next edit.
func (s *imageMapRetireStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	p, err := s.read(ctx)
	if skip, ok := imageMapRetireSkipOf(err); ok {
		return initflow.Outcome{Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !p.writes() {
		return initflow.Outcome{Detail: p.line()}, nil
	}
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return confirmStageWrite(cmd, p.lc, s.e.r.conditions(), env.Yes, imageMapRetireWhat)
	}); err != nil {
		return initflow.Outcome{}, err
	}
	lost, err := p.write(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	s.p = p
	if lost != nil {
		return initflow.Outcome{Detail: lost.Error()}, nil
	}
	if err := recordPublishedGeneration(ctx, cmd.OutOrStdout(), p.db, p.lc, s.e.path, p.body, nil); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: p.line()}, nil
}

// write puts the node with the member removed at the path with the ETag it was
// read at, once. The second answer is the lost race, which is a state and not a
// failure: another writer holds the node, nothing was written, and the run says
// so rather than trying again.
func (p *imageMapRetirePlan) write(ctx context.Context) (*imageMapRetireLostRace, error) {
	if err := p.db.PutIfMatch(ctx, projectpolicy.PathShared(), p.etag, json.RawMessage(p.body)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return &imageMapRetireLostRace{}, nil
		}
		return nil, dbErr(p.lc, err, true)
	}
	p.wroteIt = true
	return nil, nil
}

// imageMapRetireLostRace is what the run says when its write's precondition
// failed.
type imageMapRetireLostRace struct{}

func (e *imageMapRetireLostRace) Error() string {
	return fmt.Sprintf("the policy node at %s changed while this update was running (another update or a hand edit landed meanwhile), so nothing was written: read what the node now holds with 'fugaro project policy show', then run %s again",
		projectpolicy.PathShared(), selfCommand()+" update")
}

// Verify reads the node back: the member is gone, and the map the node serves
// is the one this run read.
func (s *imageMapRetireStage) Verify(ctx context.Context) error {
	if s.p == nil || !s.p.wroteIt {
		// The run wrote nothing: a member that was not there, a map the node
		// serves from image_map, or a service this run did not replace each
		// leave the node as it was, and there is nothing of this run's to read
		// back.
		return nil
	}
	p := s.p
	_, found, raw, err := readPolicyNode(ctx, p.db, p.lc)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the policy node is gone, though this run rewrote it")
	}
	if _, has, err := jsonMember(raw, imageMapMemberKey); err != nil {
		return err
	} else if has {
		return fmt.Errorf("the policy node still carries its %s member, though this run removed it", imageMapMemberKey)
	}
	return nil
}
