package cli

import (
	"encoding/json"
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
	path := filepath.Join(r.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the installation's tfvars are not there to set the hop's block in: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["voice_service"] = map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    infra.VoiceServiceName,
		"image":      image,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

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
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
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
