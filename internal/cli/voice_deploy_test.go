package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
)

// The voice hop's deploy (P5.3.3). `fugaro update` deploys it the way it
// deploys the credentials service and the secrets intake: the block is part of
// the state the installation stage's own confirmed apply converges, there is no
// flag and no second command, and the image it carries is the same verified pin
// all three subcommands of that one image run (infra.VoiceServiceSpec).
//
// What is different about the hop, and what these tests hold: it has no
// address. The intake and the credentials service each carry a URL the module
// publishes and a later apply writes back (fillCredentialsAudience), because
// their callers' ID tokens are minted for it; the hop's callers are phones
// whose tokens are minted for the Firebase project (voice_serve.go), so its
// block is three members and there is no audience fill, no second apply and no
// local-config record to keep in step.

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

// TestUpdateDeploysTheVoiceHopOnAnInstallationThatHasNoCredentialsService is
// the deploy half: the run that creates the credentials service on an
// installation that has none is the run that deploys the hop beside it, off the
// same resolve and in the same document.
//
// Mutation (run, restore): drop the SetVoiceService call from
// deployCredentialsService and this fails at voiceServiceEntry with "the
// installation's tfvars carry no voice_service" — the hop then ships only to
// installations that already had the credentials service, which is every
// installation except the one the release is installed on.
func TestUpdateDeploysTheVoiceHopOnAnInstallationThatHasNoCredentialsService(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice", "create"))
	credentialsOutputs(t, r)
	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation with no credentials service failed: %v\n%s", err, s)
	}
	voice := voiceServiceEntry(t, r)
	for k, want := range map[string]any{
		"account_id": infra.VoiceAccountID,
		"service":    infra.VoiceServiceName,
		"image":      r.credentialsRef(),
	} {
		if voice[k] != want {
			t.Errorf("voice_service.%s = %v, want %v\n%s", k, voice[k], want, s)
		}
	}
	// The hop has no address of its own, and a block that carried one would be
	// a value nothing fills: the module publishes no URL for it, so the member
	// would sit at whatever a writer composed and the run would plan a revision
	// the module does not describe.
	for _, k := range []string{"url", "audience"} {
		if _, ok := voice[k]; ok {
			t.Errorf("voice_service carries %s = %v: the hop's callers mint their ID tokens for the Firebase project, so the block has no address to carry and nothing fills one\n%s", k, voice[k], s)
		}
	}
	// The three services are one image and one pin: the hop's block and the two
	// the same run wrote must name the same reference, or the installation runs
	// two releases at once.
	svc := credentialsServiceEntry(t, r)
	intake, _ := r.tfvars(t)["intake"].(map[string]any)
	if intake == nil {
		t.Fatalf("the run wrote the hop with no intake beside it: %v", r.tfvars(t))
	}
	for name, got := range map[string]any{"credentials_service": svc["image"], "intake": intake["image"]} {
		if got != voice["image"] {
			t.Errorf("%s.image = %v and voice_service.image = %v: the three are one image and one resolve\n%s", name, got, voice["image"], s)
		}
	}
}

// TestUpdateConvergesAnInstallationThatSkippedTheVoiceHopInOneRun is the
// convergence half, and CLAUDE.md's HARD RULE at this feature's own scale: an
// installation between `brew upgrade` and `fugaro update` holds the credentials
// service's block and no hop's, and the one documented command has to leave it
// with the hop deployed — no second command, no operator step, and no
// `fugaro update` run twice.
//
// The image is the trap carriedIntake was written for, and the hop has it for
// the same reason: the block is written BEFORE the apply that CREATES the
// service, and the image in it is the binary that revision runs with `voice
// serve` as its args (voice.tf). The installation's recorded image here is an
// earlier release's, whose binary has no such subcommand, so a run that carried
// the recorded image forward would create a revision that exits on start — with
// the block already on disk, which is what every rerun then repeats.
//
// Mutation (run, restore): make carriedVoiceService carry the recorded image
// instead of resolving (drop the credentialsImageStale branch) and the first
// assertion fails on oldCredentialsRef("0.22.3") in the creating apply.
func TestUpdateConvergesAnInstallationThatSkippedTheVoiceHopInOneRun(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.voice", "create"))
	credentialsOutputs(t, r)
	// The installation's record: the service, on a release that predates the
	// hop, and no voice_service block at all.
	seedCredentialsService(t, r, oldCredentialsRef("0.22.3"))
	applies := r.captureApplies(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("one fugaro update of an installation that skipped the release the hop shipped in failed: %v\n%s", err, s)
	}
	ran := capturedApplies(t, applies, "installation")
	if len(ran) == 0 {
		t.Fatalf("the update applied the installation root not at all, so nothing created the hop the run reports:\n%s", s)
	}
	for i, doc := range ran {
		voice, _ := doc["voice_service"].(map[string]any)
		if voice == nil {
			t.Fatalf("the installation root's apply %d ran with no voice_service block, so the service the run reports as deployed was never created:\n%v", i+1, doc)
		}
		if got, want := voice["image"], r.credentialsRef(); got != want {
			t.Errorf("the installation root's apply %d ran with voice_service.image = %v, want %s: the first of these applies is the one that CREATES the hop's service, and the image in the block it reads is the binary that revision runs — the recorded one is a release whose binary has no `voice serve` subcommand, so the revision exits on start and the apply fails, with the block already written to make every rerun fail the same way\n%s", i+1, got, want, s)
		}
	}
	voice := voiceServiceEntry(t, r)
	if got, want := voice["image"], r.credentialsRef(); got != want {
		t.Errorf("voice_service.image = %v after one update, want %s", got, want)
	}
	if got, want := voice["account_id"], infra.VoiceAccountID; got != want {
		t.Errorf("voice_service.account_id = %v after one update, want %s", got, want)
	}
	if got, want := voice["service"], infra.VoiceServiceName; got != want {
		t.Errorf("voice_service.service = %v after one update, want %s", got, want)
	}
	// The resolve is one per run (credentialsResolve is memoized): the hop's
	// block, the intake's and the credentials-service stage's move read the same
	// answer, so pinning here costs no second registry read.
	if r.resolveCalls != 1 {
		t.Errorf("resolve calls %d after one update, want 1: the hop's pin is the run's own resolve, not a second registry read", r.resolveCalls)
	}

	// And a SECOND update writes nothing: what the first left is the one
	// documented command's own output, not a state the next run moves. This is
	// the carry's own test — prepare rewrites the installation root's tfvars
	// from a spec that holds no voice_service key, so a run whose carry is
	// missing plans the whole service, its account and its revision away, and
	// the plan it then applies is the hop's own destruction.
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
}
