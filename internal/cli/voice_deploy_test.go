package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
)

// The voice hop is OPT-IN (P5.3.3, the design voice-evaluation-switch.md §4.4):
// `var.voice_service` is "off unless the installation turns it on", and the one
// route that turns it on is the installation root's own block. Nothing composes
// it from the local config, no command writes it and no flag asks for it — an
// owner who wants the hop writes the three members the module's variable takes
// (deploy/terraform/gcp/modules/installation/variables.tf), and from then on the
// one documented command keeps it.
//
// That the module plans nothing without the variable is the module's own half,
// held by internal/infra/tf's TestTheVoiceHopIsInTheVoiceGoldenOnly over the
// golden plans of every other installation fixture; what is here is the run's
// half, and it is the whole of the opt-in: what `fugaro update` writes, what it
// leaves alone, and what the applies it makes read.
//
// What is different about the hop among the three services, and what these
// tests hold: it has no address. The intake and the credentials service each
// carry a URL the module publishes and a later apply writes back
// (fillCredentialsAudience), because their callers' ID tokens are minted for
// it; the hop's callers are phones whose tokens are minted for the Firebase
// project (voice_serve.go), so its block is three members and there is no
// audience fill, no second apply and no local-config record to keep in step.
// The hop is also the one of the three that is a PUBLIC endpoint (voice.tf's
// allUsers binding), which is what makes an installation that has to ask for it
// the point rather than a formality.

// voiceServiceEntry is the voice_service entry the installation root's tfvars
// carry after a run.
func voiceServiceEntry(t *testing.T, r *imagesRig) map[string]any {
	t.Helper()
	svc, _ := r.tfvars(t)["voice_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the installation's tfvars carry no voice_service: %v", r.tfvars(t))
	}
	return svc
}

// seedVoiceService writes the installation root's voice_service block as the
// installation that turned the hop on holds it: the three members the module's
// variable takes, on the release that was current when it did. It is the only
// writer of the key there is, which is the opt-in's own shape.
func seedVoiceService(t *testing.T, r *imagesRig, image string) {
	t.Helper()
	seedVoiceServiceKey(t, r, map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    infra.VoiceServiceName,
		"image":      image,
	})
}

// seedVoiceServiceOff writes the installation root's voice_service as the
// owner's own off switch: the key present and null, which is what voice.tf's
// count reads as "no hop" and what a run may not compose a block back over
// (init.go's keepVoiceHop, infra.VoiceServiceRecorded).
func seedVoiceServiceOff(t *testing.T, r *imagesRig) {
	t.Helper()
	seedVoiceServiceKey(t, r, nil)
}

// seedVoiceServiceKey is the one write of the installation root's
// voice_service entry, by hand and in the installation's own document, which is
// how both states of it are reached: the block that asks for the hop, and the
// null that turns it off.
func seedVoiceServiceKey(t *testing.T, r *imagesRig, value any) {
	t.Helper()
	path := filepath.Join(r.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the installation's tfvars are not there to set the hop's block in: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["voice_service"] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// voiceServiceOutputs is credentialsOutputs with the third service the same
// module publishes: the hop, which the installation root's outputs carry as
// voice_service while var.voice_service is not null (outputs.tf) and as null
// while it is.
//
// It is a helper of its own and not a member of credentialsOutputs, because
// the two documents say different things: an installation whose outputs name
// the hop is one that opted in — the output exists exactly while the variable
// does — and every rig that reads the outputs for the credentials service alone
// is a rig whose installation has no hop. credentialsOutputs is those, and the
// opt-in's own tests say so themselves (seedVoiceService for a machine that
// holds the block, this one for a machine that has lost it).
//
// The URL is published and nothing reads it back: the hop's callers mint their
// ID tokens for the installation's Firebase project, so there is no audience
// fill and no second apply for it (VoiceServiceSpec).
func voiceServiceOutputs(t *testing.T, r *imagesRig) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
		"voice_service":       voiceServiceOutput(),
	})}
	r.save(t)
}

// voiceServiceOutput is the hop as the installation root's outputs carry it:
// the name, the region and the URL, with no image — the module publishes what
// Cloud Run assigned, and the image a revision runs is the installation's own
// tfvars' business.
func voiceServiceOutput() map[string]any {
	return voiceServiceOutputNamed(infra.VoiceServiceName)
}

// voiceServiceOutputNamed is the same document for an installation whose hop
// service is named something other than the constant: the module's variable
// takes any Cloud Run name (variables.tf), so the name in the output is the
// installation's own fact. The account ID is not — that one the variable holds
// to infra.VoiceAccountID — which is why only the name is a parameter here.
func voiceServiceOutputNamed(name string) map[string]any {
	return map[string]any{"name": name, "region": "us-east5", "url": voiceServiceURL}
}

// voiceServiceURL is the URL the hop's output carries in these tests, in the
// shape Cloud Run assigns. Nothing in this tree reads it.
const voiceServiceURL = "https://fugaro-voice-123456789012.us-east5.run.app"

// TestUpdateLeavesTheVoiceHopOffOnAnInstallationThatNeverOptedIn is the
// opt-in's own half: the run that deploys the credentials service on an
// installation that has none — and so writes that block and the intake's in the
// same document — writes NO voice_service, and the apply it makes reads a
// document without one. The hop is the third service of that one image, and it
// is the only one of the three an installation has to ask for.
//
// The plan follows from the document rather than from this test's fixture:
// voice.tf's count reads var.voice_service, so a document without the key is a
// plan with no account, no service and no public invoker binding in it, and the
// module's own goldens are where that is read (TestTheVoiceHopIsInTheVoiceGoldenOnly).
//
// Mutation (run, restore): put the hop's block back into
// deployCredentialsService (infra.SetVoiceService(doc, infra.VoiceServiceSpec{
// AccountID: infra.VoiceAccountID, Service: infra.VoiceServiceName,
// Image: pinned})), or carriedVoiceService back into writeCarriedCredentials,
// and this fails at the tfvars assertion with "the installation's tfvars carry
// voice_service" — which is every installation getting the hop, the state this
// change ends.
func TestUpdateLeavesTheVoiceHopOffOnAnInstallationThatNeverOptedIn(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", installationCredentialsPlan("create", r.credentialsRef())...)
	credentialsOutputs(t, r)
	applies := r.captureApplies(t)
	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation that never turned the hop on failed: %v\n%s", err, s)
	}
	// The run did its ordinary work — the service and the intake are in the
	// document — which is what makes the absence below the hop's own and not a
	// run that wrote nothing at all.
	credentialsServiceEntry(t, r)
	if _, ok := r.tfvars(t)["intake"].(map[string]any); !ok {
		t.Fatalf("the run wrote the hop's own two companions and this test read the wrong document: %v", r.tfvars(t))
	}
	if voice, ok := r.tfvars(t)["voice_service"]; ok {
		t.Errorf("the installation's tfvars carry voice_service = %v after an update of an installation that never turned the hop on: the hop is opt-in and it is a PUBLIC endpoint (voice.tf's allUsers binding), so an installation with no block plans no service, no account and no invoker binding\n%s", voice, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing read the document this test is about:\n%s", s)
	}
	for i, doc := range ran {
		if voice, ok := doc["voice_service"]; ok {
			t.Errorf("the installation root's apply %d ran with voice_service = %v: voice.tf's count reads that variable, so the plan built from this document creates the hop — its account, its service and its public invoker binding — on an installation that never turned it on\n%s", i+1, voice, s)
		}
	}

	// And a SECOND update writes nothing, which is CLAUDE.md's HARD RULE at
	// this feature's own scale: `fugaro update` is the whole upgrade, so an
	// installation that never turned the hop on converges without it — no
	// operator step, no second command, and no run left reporting work.
	before := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update")
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update of a converged installation failed: %v\n%s", err, s)
	}
	if after := len(r.ran(t, "apply")); after != before {
		t.Errorf("the second update applied %d time(s): a run after a converged update has nothing left to do, and one that applies again is an upgrade nobody finishes\n%s", after-before, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
	if voice, ok := r.tfvars(t)["voice_service"]; ok {
		t.Errorf("the second update gave the installation voice_service = %v, a hop it never asked for and a public endpoint nothing in its config names", voice)
	}
}

// TestUpdateKeepsTheVoiceHopOnAnInstallationThatOptedIn is the other half, and
// the HARD RULE from the opted-in side: the block IS the opt-in, so the one
// documented command has to keep it — across prepare's rewrite of the root's
// tfvars from a spec that never holds the key (init.go's CarriedVoiceService)
// and across the release that moved the image the two services beside it run.
//
// The image is the trap the carry and the move exist for, and the hop has it
// for the intake's reason: the block names the binary that revision runs with
// `voice serve` as its args (voice.tf), so an installation that opted in under
// an earlier release holds a reference the rest of this run has moved past, and
// the one documented command has to move the hop with it. The move is the
// credentials-service stage's own apply — the LAST of the run's applies of this
// root, after the published node has been raised — so the applies before it
// carry the recorded reference, which is the state the hop's service is already
// running and not a revision this run creates.
//
// Mutation (run, restore): drop the CarriedVoiceService loop from init.go's
// prepare and the first assertion fails — the block is gone from the document
// every apply reads, which is the plan of the hop's own destruction, its
// account and its public binding with it. Drop the moveCredentialsImage re-pin
// and no apply carries this release's pin.
func TestUpdateKeepsTheVoiceHopOnAnInstallationThatOptedIn(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice[0]", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("0.22.3"))
	seedVoiceService(t, r, oldCredentialsRef("0.22.3"))
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update of an installation that turned the hop on failed: %v\n%s", err, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing read the document this test is about:\n%s", s)
	}
	seenPin := false
	for i, doc := range ran {
		voice, _ := doc["voice_service"].(map[string]any)
		if voice == nil {
			t.Fatalf("the installation root's apply %d ran with no voice_service block: the block is the opt-in, so a run that dropped it plans the hop's service, its account and its public invoker binding away, and the installation is back to having asked for a hop it does not run\n%v", i+1, doc)
		}
		switch got := voice["image"]; got {
		case r.credentialsRef():
			seenPin = true
		case oldCredentialsRef("0.22.3"):
		default:
			t.Errorf("the installation root's apply %d ran with voice_service.image = %v, want the recorded reference or this release's pin: nothing else composes the hop's image, and a reference from anywhere else is a revision of a release no part of this run decided\n%s", i+1, got, s)
		}
	}
	if !seenPin {
		t.Errorf("no apply of the installation root ran with voice_service.image = %s: the block names the binary its revision runs with `voice serve` as its args, so a run that leaves the recorded release in it leaves the hop behind the release the two services beside it have moved to — the recorded binary may have no such subcommand at all, and the installation's phones are then refused until a later update happens to move it\n%s", r.credentialsRef(), s)
	}
	voice := voiceServiceEntry(t, r)
	for k, want := range map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    infra.VoiceServiceName,
		"image":      r.credentialsRef(),
	} {
		if voice[k] != want {
			t.Errorf("voice_service.%s = %v after one update, want %v\n%s", k, voice[k], want, s)
		}
	}
	// The block is those three members and nothing else. The hop has no address
	// of its own, and a block that carried one would be a value nothing fills:
	// the module publishes no URL for it, so the member would sit at whatever a
	// writer composed and the run would plan a revision the module does not
	// describe.
	if len(voice) != 3 {
		t.Errorf("voice_service carries %d members (%v), want the three the module's variable takes: the hop's callers mint their ID tokens for the Firebase project, so the block has no address to carry and nothing fills one\n%s", len(voice), voice, s)
	}
	// The three services are one image and one pin: the hop's block and the two
	// beside it must name the same reference, or the installation runs two
	// releases at once.
	svc := credentialsServiceEntry(t, r)
	intake, _ := r.tfvars(t)["intake"].(map[string]any)
	if intake == nil {
		t.Fatalf("the run left the hop with no intake beside it: %v", r.tfvars(t))
	}
	for name, got := range map[string]any{"credentials_service": svc["image"], "intake": intake["image"]} {
		if got != voice["image"] {
			t.Errorf("%s.image = %v and voice_service.image = %v: the three are one image and one resolve\n%s", name, got, voice["image"], s)
		}
	}
	// The resolve is one per run (credentialsResolve is memoized): the hop's
	// move, the intake's and the service's read the same answer, so pinning here
	// costs no second registry read.
	if r.resolveCalls != 1 {
		t.Errorf("resolve calls %d after one update, want 1: the hop's pin is the run's own resolve, not a second registry read", r.resolveCalls)
	}

	// And a SECOND update writes nothing: what the first left is the one
	// documented command's own output, not a state the next run moves. This is
	// the carry's own test from the other side — prepare rewrites the
	// installation root's tfvars from a spec that holds no voice_service key, so
	// a run whose carry is missing plans the whole service, its account, its
	// public binding and its revision away, and the plan it then applies is the
	// hop's own destruction.
	before := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update")
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update of a converged installation failed: %v\n%s", err, s)
	}
	if after := len(r.ran(t, "apply")); after != before {
		t.Errorf("the second update applied %d time(s): a run after a converged update has nothing left to do, and one that applies again is an upgrade nobody finishes\n%s", after-before, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
	if got := voiceServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("voice_service.image = %v after the second update, want %s: the hop an installation opted into is not a key the next run drops", got, r.credentialsRef())
	}
}

// TestUpdateMovesTheVoiceHopWhoseCredentialsImageIsCurrent is the HARD RULE at
// the one edge only the hop can be at: its block is the key the run NEVER
// writes — the installation writes it, by hand, which is the whole of the
// opt-in — so its image is the one that can be left behind while the
// credentials service's own is already this release's. A move that read only
// that service's image would answer "the recorded image is already this
// release's" for the whole installation and leave the hop where the owner's own
// reference put it: a revision that may have no `voice serve` at all, and one
// whose reader is a release behind the policy node this run's policy stage has
// just raised, which internal/voicesvc refuses — every device call of that
// installation refused, on every update, until some later release happened to
// move the credentials image.
//
// Mutation (run, restore): ask the credentials service's image alone in the
// move's gate and in the stage's skip (credentialsImageStale instead of
// servicesImageStale) and the first assertion fails — the run reports the
// recorded image is already this release's and the hop keeps the hand-written
// reference.
func TestUpdateMovesTheVoiceHopWhoseCredentialsImageIsCurrent(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice[0]", "update"))
	credentialsOutputs(t, r)
	// The credentials service is already this release's — the state a second
	// run of one upgrade leaves — and the hop's block is what the owner wrote by
	// hand: the three members the module's variable takes, on the release the
	// installation was at when they wrote it.
	seedCredentialsService(t, r, r.credentialsRef())
	seedVoiceService(t, r, oldCredentialsRef("0.22.3"))
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update of an installation whose hop lags its credentials service failed: %v\n%s", err, s)
	}
	if got := voiceServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Fatalf("voice_service.image = %v after one update, want %s: the hop's block is the one the run never writes, so its hand-written reference is the one that can lag the credentials service the installation already runs this release's image for — and a hop left on the release behind runs a binary that may have no `voice serve` and reads a node this release's policy stage has raised, so every device call is refused\n%s", got, r.credentialsRef(), s)
	}
	// The credentials service's own image is not what moved, and it is the same
	// reference before and after: this run's work is the hop's alone.
	if got := credentialsServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v after the update, want %s: the installation was already on this release's service, and the move re-pins the three to the one reference\n%s", got, r.credentialsRef(), s)
	}
	// The move is before the apply that deploys it: the document the
	// credentials-service stage's apply of the installation root reads carries
	// the hop's new pin, so the revision that lands is the one that can serve.
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing deployed the hop's move:\n%s", s)
	}
	seenPin := false
	for i, doc := range ran {
		voice, _ := doc["voice_service"].(map[string]any)
		if voice == nil {
			t.Fatalf("the installation root's apply %d ran with no voice_service block: the block is the opt-in, so a run that dropped it plans the hop away\n%v", i+1, doc)
		}
		switch got := voice["image"]; got {
		case r.credentialsRef():
			seenPin = true
		case oldCredentialsRef("0.22.3"):
		default:
			t.Errorf("the installation root's apply %d ran with voice_service.image = %v, want the hand-written reference or this release's pin\n%s", i+1, got, s)
		}
	}
	if !seenPin {
		t.Errorf("no apply of the installation root ran with voice_service.image = %s: the move has to be on disk before the apply that deploys the revision, or the hop's service is left on the reference the owner wrote\n%s", r.credentialsRef(), s)
	}

	// And a second update writes nothing: the move the first made is the one
	// documented command's own output, not a state the next run moves again.
	before := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update")
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update of a converged installation failed: %v\n%s", err, s)
	}
	if after := len(r.ran(t, "apply")); after != before {
		t.Errorf("the second update applied %d time(s): a run after a converged update has nothing left to do\n%s", after-before, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
	if got := voiceServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("voice_service.image = %v after the second update, want %s: the hop's move is not a key the next run undoes", got, r.credentialsRef())
	}
}

// TestUpdateKeepsTheVoiceHopOnAMachineWithNoTfvars is the opt-in read from the
// installation's own state rather than from this machine's copy of it.
//
// The block lives in ONE machine's workdir tfvars
// ($XDG_STATE_HOME/fugaro/terraform/<project>/installation, infra.Workdir), and
// every run rewrites that file from a spec that never holds the key — so the
// carry that keeps an opted-in installation's hop (CarriedVoiceService) reads a
// document only the machine that wrote it has. The owner's second machine of
// the same installation holds none, and neither does any machine whose state
// directory was lost, and the run there carried nothing: prepare wrote a
// document with no block, and voice.tf's count — which reads var.voice_service —
// planned the hop's service, its account, its one role binding and its public
// invoker binding away. The guard refuses that delete, so the failure the owner
// meets is the HARD RULE's own: `fugaro update`, the one documented command,
// stops converging on an installation nobody asked it to change. It is also
// SECURITY.md's sentence about this very hop ("an installation runs one only if
// it asks for it, and `fugaro update` then deploys and keeps it") read on the
// machine the sentence does not hold on.
//
// What the run reads instead is shared state: the module publishes the hop as
// the `voice_service` output, which exists exactly while the variable does, so
// the installation's own outputs — decoded a few lines above the plan whose
// document they have to be on disk for — are where the opt-in is derived from
// (init.go's keepVoiceHop).
//
// The machine's document is deleted before the run, and that is the second
// machine exactly: PrepareWorkdir snapshots the file into Workdir.PrevVars
// before it rebuilds the tree, so a file that is not there is a run with
// nothing to carry. Everything else about the run is the ordinary one: the
// credentials-service stage composes the service's block from this machine's
// config and this release's pin (deployCredentialsService), and the hop's block
// is composed off that same reference, because the three services are one pin
// of one image.
//
// Mutation (run, restore): drop the keepVoiceHop call from installRoot and this
// fails at the first apply's assertion — the document the installation root's
// apply reads carries no voice_service, which is the plan of the hop's own
// destruction, and on an installation whose state still holds the hop the guard
// is what stops the run instead.
func TestUpdateKeepsTheVoiceHopOnAMachineWithNoTfvars(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice[0]", "update"))
	voiceServiceOutputs(t, r)
	// This machine holds no document at all, which is the second machine and
	// the lost state directory both: PrepareWorkdir finds nothing to snapshot
	// into Workdir.PrevVars, so the run has nothing to carry the block from.
	// The rig writes one only when a test seeds one (seedCredentialsService,
	// seedVoiceService), and this one seeds none.
	if _, err := os.Stat(filepath.Join(r.root(), infra.VarsFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the rig's machine holds an installation document (%v), so this test is not the machine it means to be: %v", err, r.root())
	}
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update from a machine holding no document of the installation failed: %v\n%s", err, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing read the document this test is about:\n%s", s)
	}
	for i, doc := range ran {
		voice, _ := doc["voice_service"].(map[string]any)
		if voice == nil {
			t.Fatalf("the installation root's apply %d ran with no voice_service block on an installation whose own state has a hop: the block is the opt-in and the output is what the installation recorded it as, so a run that reads only this machine's document plans the hop's service, its account, its public invoker binding and its state read away — and the guard refuses that delete, which leaves the one documented command unable to converge\n%v", i+1, doc)
		}
	}
	// The block is the three members the module's variable takes, and the image
	// is this release's pin: the same reference the two services beside it were
	// just deployed with, which is what makes the hop's revision one that can
	// serve (`voice serve` is a subcommand of that same binary).
	voice := voiceServiceEntry(t, r)
	for k, want := range map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    infra.VoiceServiceName,
		"image":      r.credentialsRef(),
	} {
		if voice[k] != want {
			t.Errorf("voice_service.%s = %v after one update from a machine with no document, want %v\n%s", k, voice[k], want, s)
		}
	}
	if len(voice) != 3 {
		t.Errorf("voice_service carries %d members (%v), want the three the module's variable takes\n%s", len(voice), voice, s)
	}
	svc := credentialsServiceEntry(t, r)
	intake, _ := r.tfvars(t)["intake"].(map[string]any)
	if intake == nil {
		t.Fatalf("the run left the hop with no intake beside it: %v", r.tfvars(t))
	}
	for name, got := range map[string]any{"credentials_service": svc["image"], "intake": intake["image"]} {
		if got != voice["image"] {
			t.Errorf("%s.image = %v and voice_service.image = %v: the three are one image and one resolve\n%s", name, got, voice["image"], s)
		}
	}
	if r.resolveCalls != 1 {
		t.Errorf("resolve calls %d after one update, want 1: the hop's pin is the run's own resolve, not a second registry read", r.resolveCalls)
	}

	// And a SECOND update writes nothing, which is the same machine one run
	// later: the block the first run composed is now this machine's document,
	// and the carry keeps it from there.
	before := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update")
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update of a converged installation failed: %v\n%s", err, s)
	}
	if after := len(r.ran(t, "apply")); after != before {
		t.Errorf("the second update applied %d time(s): a run after a converged update has nothing left to do\n%s", after-before, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
	if got := voiceServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("voice_service.image = %v after the second update, want %s: the block the run composed from the installation's own state is not a key the next run drops", got, r.credentialsRef())
	}
}

// TestUpdateKeepsTheVoiceHopUnderTheInstallationsOwnServiceName is the other
// half of what a run composes a lost block out of, and it is the service's NAME
// rather than its account: the module's variable takes any Cloud Run name
// (variables.tf), so an installation that named its hop service something else
// holds that name in its own state, and the output the run reads back carries
// it (outputs.tf publishes google_cloud_run_v2_service.voice[0].name).
//
// Composing the constant instead would plan a REPLACE of that service: a Cloud
// Run service's name is a force-new attribute, so a document naming
// fugaro-voice where the state holds another name is a change that destroys the
// service the installation has — and the guard refuses every replace unless its
// address is named on --allow-delete (tf.Guard), which is the HARD RULE failing
// again on an installation nobody asked the run to change, this time one that
// named its hop service rather than one that lost its document
// (TestUpdateKeepsTheVoiceHopOnAMachineWithNoTfvars is that one).
//
// The account's ID is the constant, on the other side of the same question: the
// module's variable accepts no other (variables.tf), because the cover
// classifies the account's one project grant by that literal (internal/infra/tf's
// isVoiceMember), so an installation cannot be holding a different one and the
// run has none to read back.
//
// Mutation (run, restore): put infra.VoiceServiceName back into keepVoiceHop's
// Service member, and this fails at the first apply's assertion with the name
// the document would have replaced the installation's service with.
func TestUpdateKeepsTheVoiceHopUnderTheInstallationsOwnServiceName(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice[0]", "update"))
	named := infra.VoiceServiceName + "-eu"
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
		"voice_service":       voiceServiceOutputNamed(named),
	})}
	r.save(t)
	// The second machine, as in the test above: nothing here holds a document
	// of the installation, so the run has nothing to carry the block from.
	if _, err := os.Stat(filepath.Join(r.root(), infra.VarsFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the rig's machine holds an installation document (%v), so this test is not the machine it means to be: %v", err, r.root())
	}
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update from a machine holding no document of an installation whose hop service is named %s failed: %v\n%s", named, err, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing read the document this test is about:\n%s", s)
	}
	for i, doc := range ran {
		voice, _ := doc["voice_service"].(map[string]any)
		if voice == nil {
			t.Fatalf("the installation root's apply %d ran with no voice_service block on an installation whose own state has a hop: the block is the opt-in, so this document plans the hop's service, its account, its public invoker binding and its state read away\n%v", i+1, doc)
		}
		if voice["service"] != named {
			t.Fatalf("the installation root's apply %d ran with voice_service.service = %v, want the installation's own %s: the name is a force-new attribute of the Cloud Run service, so this document is the plan that REPLACES the hop's service — a delete the guard refuses, which leaves the one documented command unable to converge on an installation nobody asked it to change\n%s", i+1, voice["service"], named, s)
		}
	}
	// The three members the module's variable takes, on the installation's own
	// name and this release's pin.
	voice := voiceServiceEntry(t, r)
	for k, want := range map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    named,
		"image":      r.credentialsRef(),
	} {
		if voice[k] != want {
			t.Errorf("voice_service.%s = %v after one update from a machine with no document, want %v\n%s", k, voice[k], want, s)
		}
	}
}

// TestUpdatePlansTheVoiceHopAwayWhenTheOwnerTurnsItOff is the other end of the
// opt-in, and the one SECURITY.md leans on: the hop is a PUBLIC endpoint
// (voice.tf's allUsers binding) whose account reads the installation's whole
// database, so the spend it can cause outside every budget cap is bounded by
// three things, the last of which is "the installation's own switch, which
// takes the service and its grant away". A switch a later `fugaro update`
// writes back over is not one.
//
// The switch is the key written as null in the installation root's tfvars. It
// is null rather than the key deleted because a missing key is a fact this side
// cannot tell apart from a machine that never held the document
// (TestUpdateKeepsTheVoiceHopOnAMachineWithNoTfvars): keepVoiceHop composes the
// block back out of the installation's own state for that one, and it is right
// to — so a hop turned off by deleting the block would come back, service,
// account, public invoker binding and state read with it, on the next update.
// Null is a decision the document records (infra.VoiceServiceRecorded), so
// keepVoiceHop leaves it alone and prepare's carry keeps it across every later
// rewrite (infra.CarriedVoiceService).
//
// The plan follows from the document rather than from this test's fixture, as
// in the opt-in's own test: voice.tf's count reads var.voice_service, so the
// document every apply of this root reads — this one with the key null — is the
// plan that removes the hop's account, its service, its public invoker binding
// and its state read. A real run meets the guard on that plan, which is the
// delete route every other managed resource of this tree takes: the addresses
// are named on the command line, and nothing about the hop is special-cased.
//
// Mutation (run, restore): read the document with infra.VoiceServiceOf instead
// of infra.VoiceServiceRecorded in init.go's keepVoiceHop, or drop a null entry
// from infra.CarriedVoiceService, and this fails at the tfvars assertion and
// then at the first apply's: the run has composed the block back, and the plan
// it made is the one that creates the public endpoint again.
func TestUpdatePlansTheVoiceHopAwayWhenTheOwnerTurnsItOff(t *testing.T) {
	r := credentialsRig(t)
	// An ordinary update of this root, with a change beside the hop scripted as
	// its plan: what the run PLANS is this test's own fixture, while what the
	// plan is built from — the document every apply of this root reads — is the
	// point.
	r.setPlanAt(t, "installation", installationCredentialsPlan("update", r.credentialsRef())...)
	// The installation HAS the hop: its own outputs carry the service, which is
	// the module's voice_service output and exists exactly while the variable
	// does. That is what makes this the owner turning it off rather than an
	// installation that never asked for it.
	voiceServiceOutputs(t, r)
	seedCredentialsService(t, r, r.credentialsRef())
	seedVoiceServiceOff(t, r)
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update of an installation whose owner turned the hop off failed: %v\n%s", err, s)
	}
	if voice, ok := r.tfvars(t)["voice_service"]; !ok {
		t.Errorf("the installation's tfvars carry no voice_service key after the update: the owner's off switch is a key the document records, and one the run drops is what the next run reads as a machine that never had the block — which is the run that composes the hop, its public invoker binding and its read of the whole database back\n%s", s)
	} else if voice != nil {
		t.Errorf("voice_service = %v after the update, want null: the run composed the hop back over the owner's off switch, and the plan it wrote is the one that creates the service, its account and its public invoker binding again\n%s", voice, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing read the document this test is about:\n%s", s)
	}
	for i, doc := range ran {
		voice, ok := doc["voice_service"]
		if !ok {
			t.Errorf("the installation root's apply %d ran with no voice_service key: an installation that opted in and then had the key dropped is the state this run has to keep off, and one that wrote the key away is the run the next update reads as a machine with no document\n%v", i+1, doc)
			continue
		}
		if voice != nil {
			t.Errorf("the installation root's apply %d ran with voice_service = %v: voice.tf's count reads that variable, so the plan built from this document keeps the hop — its account, its service, its public invoker binding and its read of the whole database — on an installation whose owner turned it off\n%s", i+1, voice, s)
		}
	}

	// And a SECOND update leaves it off, which is the switch being a decision
	// rather than one run's state: the null is what prepare carried forward and
	// what keepVoiceHop read as the owner's own answer, on a machine whose
	// installation still publishes the hop.
	before := len(r.ran(t, "apply"))
	r.script["plan"] = map[string]any{"exit": 0}
	r.setPlanAt(t, "installation")
	r.setPlan(t)
	out, errOut, err = executeStdin(t, "", "update")
	s = out + errOut
	if err != nil {
		t.Fatalf("the second update of an installation whose owner turned the hop off failed: %v\n%s", err, s)
	}
	if after := len(r.ran(t, "apply")); after != before {
		t.Errorf("the second update applied %d time(s): an installation whose plan is empty has nothing left to do\n%s", after-before, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the second update does not report a converged installation:\n%s", s)
	}
	if voice, ok := r.tfvars(t)["voice_service"]; !ok || voice != nil {
		t.Errorf("voice_service = %v (present: %v) after the second update, want the null the owner wrote: the switch is not a state the next run undoes, and a block here is the public endpoint back\n%s", voice, ok, s)
	}
}
