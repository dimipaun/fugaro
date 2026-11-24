package cli

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/infra"
)

// update_orchestrator.go is the resident orchestrator's writer (mobile plan
// tasks P5.1b and P5.1c; docs/mobile/design/always-on-orchestrator.md sections
// 4.1 and 4.3; the owner's ruling of 2026-11-20 that `fugaro update` writes the
// block): the installation root's `orchestrator` variable, composed by the
// credentials-service stage and carried by every later run.
//
// It replaces `--orchestrator`, which was the only writer and which a person at
// a real terminal had to ask for by name. The ruling is that the one documented
// upgrade command gives an installation its resident orchestrator by itself
// (CLAUDE.md's HARD RULE: no second command), so the stage that already moves
// the credentials service's image onto this release's also writes the block, for
// an installation that can carry one.
//
// What default-on does about the widest grant of the design's section 4.3 table,
// iam.serviceAccounts.signJwt on the budget signer. The opt-in's own gate (a real
// terminal, no --yes, no agent's session) is not what makes that grant safe, and
// it is not carried over as one: it was a confirmation, and Fugaro asks for
// nothing. What stands in its place is that the apply is an ordinary apply of the
// installation root: it is planned, it is reviewed by the guard and the cover
// (internal/infra/tf's cover holds the grant to the budget signer of THIS run in
// its own cell — orchestratorMinterGrant — and names any other target), it is
// confirmed or gated like every other plan of that root, and the step announces
// the grant in the line it prints before it applies. A coding agent's session is
// refused before any of it, as it is for every cloud write: the stage is skipped
// there, so no agent ever writes the block.
//
// Nothing a run in flight holds is touched: the account, its bindings, the
// pairing container, the service and the tick are creates, and the one change to
// an existing resource is the credentials service's new revision, which names
// the daemon's account in the orchestrator class and drains the one a run
// started on exactly as an image move does (docs/design/update-in-flight.md).

// orchestratorVerdict answers, for the installation whose tfvars are doc and
// whose credentials service runs image, whether this run writes the block, and
// when it does not and the installation does not hold one either, why — in
// words a printed line can carry. Both answers come from records the run already
// holds: it reads no cloud.
//
// Holding the block is the answer that is neither a write nor a reason: the
// block is the installation's record, written once and carried by every later
// run (infra.CarriedOrchestrator), and a run that recomposed it would be
// re-deciding grants that are state.
func (e *initEngine) orchestratorVerdict(doc []byte, image string) (write bool, why string) {
	if doc == nil {
		return false, "the installation has no credentials service, which is the orchestrator's caller class"
	}
	held, err := infra.OrchestratorRecorded(doc)
	switch {
	case err != nil:
		return false, ""
	case held:
		return false, ""
	}
	if b := e.lc.Budget; b == nil || b.FirebaseProject == "" || b.RTDBURL == "" {
		return false, "the installation records no Firebase budget backend for it to read (fugaro update --firebase-project <gcp-project-id> records one)"
	}
	if _, err := infra.Orchestrator(e.lc, image); err != nil {
		return false, "its image is not one this release can pin: " + oneLineCLI(err.Error())
	}
	return true, ""
}

// orchestratorLeftOff is the sentence a skipped credentials-service stage
// appends to its own, naming why the installation has no resident orchestrator
// and this run writes none. It is empty for an installation that holds the block
// or whose run writes it: only the installation that cannot carry one hears of
// it, and it is told on every update until it can, in one line.
func (e *initEngine) orchestratorLeftOff(doc []byte, image string) string {
	if _, why := e.orchestratorVerdict(doc, image); why != "" {
		return "; the resident orchestrator is left off: " + why
	}
	return ""
}

// orchestratorToWrite is the stage's skip predicate: whether the run has a block
// to write, which is a reason for the credentials-service stage to apply the
// installation root even when every image is already this release's.
func (e *initEngine) orchestratorToWrite() bool {
	doc, image, err := e.recordedCredentialsImage()
	if err != nil {
		return false
	}
	write, _ := e.orchestratorVerdict(doc, image)
	return write
}

// ensureOrchestratorBlock writes the installation root's orchestrator block
// when this run is to, composed from state the run already holds: the Firebase
// facts are the local config's budget block, and the image is the one the
// credentials service now runs (moveCredentialsImage has just moved it), because
// `fugaro orchestrator serve` is a subcommand of that one released binary.
//
// It runs where the block's one ordering constraint is already met: the
// token-minter row is bound on the Firebase root's signer account, and the
// installation this reads is one whose config records the Firebase backend, so
// that root has been applied and the signer exists. An installation whose first
// apply this is (setup) has no backend recorded until the run that creates it
// has written its config, and takes the block on the next update.
//
// An installation that cannot carry the block is never an error: the stage
// prints one line naming why and goes on with the rest of the update. What comes
// back is the spec it composed and whether it wrote anything, and the caller
// decides which of the two the run's outcome makes true (unsetOrchestratorBlock).
func (e *initEngine) ensureOrchestratorBlock() (infra.OrchestratorSpec, bool, error) {
	base, err := e.wd.Vars()
	if err != nil {
		return infra.OrchestratorSpec{}, false, userErr("reading the installation's tfvars: %v", err)
	}
	if len(base) == 0 {
		base = e.wd.PrevVars
	}
	if len(base) == 0 {
		return infra.OrchestratorSpec{}, false, nil
	}
	image, have, err := infra.CredentialsServiceImageOf(base)
	if err != nil {
		return infra.OrchestratorSpec{}, false, userErr("reading the installation's %s: %v", infra.VarsFile, err)
	}
	if !have {
		image = ""
	}
	write, why := e.orchestratorVerdict(orElse(have, base), image)
	if !write {
		if why != "" {
			fmt.Fprintf(e.r.w, "the resident orchestrator is left off: %s\n", why)
		}
		return infra.OrchestratorSpec{}, false, nil
	}
	spec, err := infra.Orchestrator(e.lc, image)
	if err != nil {
		return infra.OrchestratorSpec{}, false, err
	}
	doc, err := infra.SetOrchestrator(base, spec)
	if err != nil {
		return infra.OrchestratorSpec{}, false, userErr("setting the resident orchestrator in the installation's %s: %v", infra.VarsFile, err)
	}
	if err := e.wd.WriteVars(doc); err != nil {
		return infra.OrchestratorSpec{}, false, userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	fmt.Fprintf(e.r.w, "adds the resident orchestrator: %s will run the service %s, woken every minute by %s, holding the launcher's grants and iam.serviceAccounts.signJwt on the budget signer (docs/mobile/design/always-on-orchestrator.md, section 4.3)\n",
		infra.OrchestratorAccountID+"@"+e.lc.GCPProject+".iam.gserviceaccount.com", spec.Service, spec.SchedulerJob)
	return spec, true, nil
}

// orElse is doc when the installation carries a credentials service and nil
// when it does not: orchestratorVerdict reads nil as "no credentials service".
func orElse(have bool, doc []byte) []byte {
	if have {
		return doc
	}
	return nil
}

// unsetOrchestratorBlock takes the block ensureOrchestratorBlock wrote back out
// of the installation's tfvars, for the run that never applied it.
//
// Nothing is created and nothing is promised then: the document is the one the
// run's own writers left, minus the key, so the installation is exactly what it
// was and the next update writes the block again. A tfvars the run cannot read
// back, or one this program cannot write, is a failure of its own rather than a
// silent leave-behind: the caller joins it with whatever ended the run.
func (e *initEngine) unsetOrchestratorBlock() error {
	vars, err := e.wd.Vars()
	if err != nil {
		return userErr("reading the installation's tfvars: %v", err)
	}
	if len(vars) == 0 {
		return nil
	}
	doc, err := infra.UnsetOrchestrator(vars)
	if err != nil {
		return userErr("removing the resident orchestrator from the installation's %s: %v", infra.VarsFile, err)
	}
	if err := e.wd.WriteVars(doc); err != nil {
		return userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	fmt.Fprintf(e.r.w, "the resident orchestrator was not applied: this run created nothing for it, and the next 'fugaro %s' writes it again\n", e.r.o.doorName())
	return nil
}

// orchestratorApplied settles the block a stage wrote once its installation
// root has been planned: it is a fact about the installation where the apply
// carried it or the plan found every resource it names already there, and is
// taken back out of the document everywhere else (a plan the guard refused, a
// confirmation declined, --plan-only's stop).
func (e *initEngine) orchestratorApplied(wrote bool, runErr error) error {
	if !wrote || e.r.rootApplied || e.r.rootPlanEmpty {
		return runErr
	}
	if undoErr := e.unsetOrchestratorBlock(); undoErr != nil {
		if runErr == nil {
			return undoErr
		}
		return errors.Join(runErr, undoErr)
	}
	return runErr
}

// orchestratorInstallLabel is what the stage's confirmation names the apply as.
func orchestratorInstallLabel(wrote bool) string {
	if wrote {
		return " (this release's credentials service and the resident orchestrator)"
	}
	return " (this release's credentials service)"
}
